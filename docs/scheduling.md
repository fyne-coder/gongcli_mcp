# Scheduling Cache Refreshes

`gongctl` does not install a scheduler. The scheduler is your responsibility
and should run on the host (or container) that already has the writable
data root mounted and credentials available. This guide ships starter
templates for the four common targets:

- `cron` (single-host, simplest)
- `systemd` timer + service (most enterprise Linux deployments)
- `launchd` (macOS admin-workstation pilot)
- Kubernetes `CronJob` (containerized shared deployments)

The cron, systemd, and launchd starters below use the SQLite-oriented
`sync run --config` primitive:

```bash
gongctl sync run --config /etc/gongctl/sync-run.yaml
```

A complete annotated template is at
[docs/examples/sync-run.example.yaml](examples/sync-run.example.yaml). Copy
it, replace the `REPLACE_WITH_*` placeholders, edit the date window / scope /
paths to match your tenant, then **always dry-run before enabling the
scheduler**:

```bash
gongctl sync run --config /etc/gongctl/sync-run.yaml --dry-run
```

For cache purge / retention scheduling, the same pattern applies with
[docs/examples/retention-policy.example.yaml](examples/retention-policy.example.yaml).

For Kubernetes with shared Postgres, skip `sync run --config` and use Pattern 4
below. Postgres schedules should run direct `gongctl sync ...` commands or a
reviewed shell wrapper with `GONG_DATABASE_URL` set to a writable operator URL.

## Operating principles

- The scheduled job runs the writable refresh; `gongmcp` stays read-only and
  separate.
- Every scheduled job has one named owner and one backup owner.
- Credentials come from a secret store, environment file, or systemd
  `EnvironmentFile=` — never from the crontab itself.
- The scheduler exit code is the alert signal. Capture stdout/stderr to a log
  the operator can grep without containing transcript bodies.
- Back up the cache before any major refresh, schema change, or upgrade.
- Run `gongctl sync status` (or `get_sync_status` over MCP) after every
  refresh so business users can see staleness.

## Pattern 1 — `cron`

Use this for a single-host pilot or admin-workstation deployment.

`/etc/cron.d/gongctl-sync` (or root crontab):

```cron
# Refresh the Gong cache every weekday at 02:15 local time.
# Logs go to a file the operator owns, not to /var/mail.
15 2 * * 1-5  gongctl  /usr/local/sbin/gongctl-sync.sh >> /var/log/gongctl/sync.log 2>&1

# Confirmed retention purge runs once a week, gated by the reviewed YAML.
30 3 * * 6    gongctl  /usr/local/sbin/gongctl-purge.sh >> /var/log/gongctl/purge.log 2>&1
```

`/usr/local/sbin/gongctl-sync.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail

# Source the env file with GONG_ACCESS_KEY / GONG_ACCESS_KEY_SECRET.
# Mode 0600, owned by the gongctl service user.
set -a
. /etc/gongctl/sync.env
set +a

CONFIG=/etc/gongctl/sync-run.yaml
LOCK=/var/run/gongctl/sync.lock

# Single-instance guard. If the previous run is still going, skip this tick
# instead of stacking jobs.
exec 9>"$LOCK"
flock -n 9 || { echo "$(date -u +%FT%TZ) sync still running; skipping"; exit 0; }

echo "$(date -u +%FT%TZ) starting sync run"
gongctl --restricted sync run --config "$CONFIG"
gongctl --restricted sync status --db /srv/gongctl/cache/gong.db
echo "$(date -u +%FT%TZ) sync run finished ok"
```

`/usr/local/sbin/gongctl-purge.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail
set -a
. /etc/gongctl/sync.env
set +a

# Always dry-run first. The dry-run output is part of the audit trail.
gongctl --restricted cache purge \
  --db /srv/gongctl/cache/gong.db \
  --config /etc/gongctl/retention-policy.yaml \
  --dry-run

# Confirmed run. Fails closed if approval fields are missing in the YAML.
gongctl --restricted cache purge \
  --db /srv/gongctl/cache/gong.db \
  --config /etc/gongctl/retention-policy.yaml \
  --confirm
```

Make both scripts mode `0750`, owned by the `gongctl` service user.

## Pattern 2 — systemd timer + service

The recommended pattern for enterprise Linux. Gives you per-job logs in
`journalctl`, a restart policy, environment-file loading, sandboxing, and
`OnFailure=` notifier hooks.

`/etc/systemd/system/gongctl-sync.service`:

```ini
[Unit]
Description=gongctl scheduled cache refresh
After=network-online.target
Wants=network-online.target
# Optional: notify on failure (define gongctl-sync-notify.service separately).
OnFailure=gongctl-sync-notify.service

[Service]
Type=oneshot
User=gongctl
Group=gongctl
EnvironmentFile=/etc/gongctl/sync.env
WorkingDirectory=/srv/gongctl
ExecStart=/usr/local/bin/gongctl --restricted sync run --config /etc/gongctl/sync-run.yaml
ExecStartPost=/usr/local/bin/gongctl --restricted sync status --db /srv/gongctl/cache/gong.db

# Sandboxing
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=/srv/gongctl /var/log/gongctl
```

`/etc/systemd/system/gongctl-sync.timer`:

```ini
[Unit]
Description=Run gongctl-sync every weekday at 02:15

[Timer]
OnCalendar=Mon..Fri 02:15
Persistent=true
Unit=gongctl-sync.service

[Install]
WantedBy=timers.target
```

Enable:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now gongctl-sync.timer
sudo systemctl list-timers | grep gongctl
```

Inspect a run:

```bash
journalctl -u gongctl-sync.service --since '24 hours ago'
```

For retention, ship a sibling pair `gongctl-purge.service` /
`gongctl-purge.timer` (same shape, weekly schedule, calls
`cache purge --config ...`).

## Pattern 3 — launchd (macOS admin-workstation pilot)

For a single operator on macOS. Place under `~/Library/LaunchAgents/` for a
user-level job or `/Library/LaunchDaemons/` for a system-level job.

`~/Library/LaunchAgents/io.gongctl.sync.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>io.gongctl.sync</string>

    <key>ProgramArguments</key>
    <array>
        <string>/usr/local/bin/gongctl</string>
        <string>--restricted</string>
        <string>sync</string>
        <string>run</string>
        <string>--config</string>
        <string>/Users/operator/gongctl/sync-run.yaml</string>
    </array>

    <key>EnvironmentVariables</key>
    <dict>
        <key>GONG_ACCESS_KEY</key>
        <string>set-via-launchctl-setenv-or-secret-tool</string>
        <key>GONG_ACCESS_KEY_SECRET</key>
        <string>set-via-launchctl-setenv-or-secret-tool</string>
    </dict>

    <key>StartCalendarInterval</key>
    <dict>
        <key>Hour</key>    <integer>2</integer>
        <key>Minute</key>  <integer>15</integer>
    </dict>

    <key>StandardOutPath</key>
    <string>/Users/operator/gongctl/logs/sync.out</string>
    <key>StandardErrorPath</key>
    <string>/Users/operator/gongctl/logs/sync.err</string>
</dict>
</plist>
```

Load:

```bash
launchctl load -w ~/Library/LaunchAgents/io.gongctl.sync.plist
launchctl list | grep gongctl
```

Prefer the macOS Keychain (`security add-generic-password`) or a secret
manager over inline plist credentials for any deployment beyond a single
operator pilot.

## Pattern 4 — Kubernetes CronJob (containerized shared deployment)

Use this when `gongctl` and `gongmcp` already run as containers and the
cache is Postgres. The CronJob does the writable refresh; the `gongmcp`
Deployment stays read-only and separate.

For Postgres, run direct `gongctl sync ...` commands with a writable
`GONG_DATABASE_URL`. Do not use `sync run --config` here; that config runner is
for SQLite cache schedules today.

```yaml
apiVersion: batch/v1
kind: CronJob
metadata:
  name: gongctl-sync
  namespace: gongctl
spec:
  schedule: "15 2 * * 1-5"          # Mon–Fri 02:15 UTC
  concurrencyPolicy: Forbid          # don't stack runs
  successfulJobsHistoryLimit: 3
  failedJobsHistoryLimit: 7
  jobTemplate:
    spec:
      backoffLimit: 0                # let the scheduler/alert decide
      template:
        spec:
          restartPolicy: Never
          serviceAccountName: gongctl
          containers:
            - name: gongctl
              image: ghcr.io/fyne-coder/gongcli_mcp/gongctl:v0.4.6
              command: ["/bin/sh", "-lc"]
              args:
                - |
                  set -eu
                  FROM="$(date -u -d 'yesterday' +%F)"
                  TO="$(date -u +%F)"
                  gongctl --restricted sync calls --from "$FROM" --to "$TO" --preset minimal
                  gongctl --restricted sync users
                  gongctl --restricted sync transcripts --out-dir /transcripts --batch-size 100 --limit 200 --allow-sensitive-export
                  gongctl --restricted sync settings --kind trackers
                  gongctl --restricted sync settings --kind scorecards
                  gongctl --restricted sync read-model --rebuild
                  gongctl --restricted sync status
              envFrom:
                - secretRef:
                    name: gongctl-gong-credentials   # GONG_ACCESS_KEY/SECRET
                - secretRef:
                    name: gongctl-postgres-writer    # GONG_DATABASE_URL
              volumeMounts:
                - name: transcripts
                  mountPath: /transcripts
              securityContext:
                allowPrivilegeEscalation: false
                runAsNonRoot: true
                runAsUser: 65532
                capabilities:
                  drop: ["ALL"]
                readOnlyRootFilesystem: true
          volumes:
            - name: transcripts
              persistentVolumeClaim:
                claimName: gongctl-transcripts
```

Create the `gongctl-transcripts` PVC separately, or remove the transcript step
and volume if transcript search is not approved. A `ReadWriteOnce` PVC is
usually sufficient for a single CronJob writer; size it for the approved
transcript retention window.

Watch a run:

```bash
kubectl -n gongctl get cronjob gongctl-sync
kubectl -n gongctl logs job/<job-name>
```

For retention, ship a sibling `gongctl-purge` CronJob with a weekly
schedule that mounts the retention policy from another ConfigMap.

For a first-run init, use the same image and writer secrets in a one-off Job,
but run a bounded historical window instead of yesterday-only calls. The schema
migration happens when `gongctl` opens Postgres with the writable URL; the
`gongmcp` Deployment should start only after the source/serving database and
scoped reader grants are ready.

Remove the transcript step if transcript search is not approved for the pilot.
When restricted mode is enabled, transcript sync requires
`--allow-sensitive-export` or `GONGCTL_ALLOW_SENSITIVE_EXPORT=1` as the
operator's explicit runtime approval. `sync transcripts` defaults to
`--limit 100`; the scheduled example uses a small daily limit, while a first
historical backfill Job should set a larger approved `--limit` or run repeated
Jobs until `sync status` shows the expected transcript coverage.

If the inline shell becomes too large for review, move the script into a
reviewed ConfigMap or customer-owned operator image and keep the same command
sequence. The important boundary is still that `gongctl` receives the writable
Postgres URL and `gongmcp` remains read-only.

## Computing rolling date windows

The SQLite `sync run --config` patterns above usually pass a static `from:` /
`to:` from the YAML. To compute a rolling window at firing time, write the
dates from a small wrapper before invoking the SQLite sync command:

```bash
FROM=$(date -u -d 'yesterday' +%F)
TO=$(date -u +%F)
gongctl --restricted sync calls \
  --db /srv/gongctl/cache/gong.db \
  --from "$FROM" --to "$TO" \
  --preset business --max-pages 50
```

Use this wrapper *before* the `sync run` invocation if you want incremental
calls + the rest of the YAML pipeline. Keep both invocations under the same
single-instance lock.

## Monitoring the schedule

The minimum monitoring loop:

1. Capture the scheduler's exit code and alert on non-zero. systemd does this
   via `OnFailure=`, K8s via `failedJobsHistoryLimit` + a kube-state-metrics
   alert, cron via the wrapper script's exit code logged to your monitoring
   pipeline.
2. After every successful run, `gongctl sync status --db <db>` (or
   `get_sync_status` over MCP) returns `last_sync_at` per stage. Alert if
   `last_sync_at` is older than the expected cadence + a grace window
   (e.g. cadence 24h, alert at 30h).
3. Tail logs for `error` / `failed` lines. Refuse to log transcript bodies
   or secret values; the CLI does not log them by default but custom
   wrappers can leak.
4. After a refresh, restart `gongmcp` so it re-fingerprints the cache and
   governance config (it fails closed if either changes mid-process — see
   the operator-sync runbook).

A simple "scheduler hasn't fired" alert: emit a heartbeat to your monitoring
system from the success path of the wrapper script (`curl -fsS
https://monitoring.example.com/heartbeat/gongctl-sync || true`). Alert when
the heartbeat is missing for two cadence windows.

## What to do when a scheduled run fails

1. Check the exit code and the last 100 log lines of the most recent run
   (`journalctl -u gongctl-sync.service`, K8s job logs, or
   `/var/log/gongctl/sync.log`).
2. If it's an auth failure, run `gongctl auth check` interactively as the
   service user. Rotate credentials in Gong if the key is no longer valid.
3. If it's a Gong rate-limit / 429 storm, lower `max_pages` in the YAML or
   stagger steps; do not raise the schedule frequency.
4. If it's a partial-refresh failure, follow the
   [operator-sync runbook §Incident Response](runbooks/operator-sync.md#incident-response).
   Do not delete or rebuild the cache without backup verification.
5. Re-run the same `sync run --config` invocation manually before the next
   scheduled tick to confirm the fix.

## Working with a coding agent

A coding agent (Claude Code, Codex, Cursor, or similar) is well-suited to
the boilerplate around scheduling:

- generate the systemd unit / launchd plist / K8s CronJob from the
  templates in this doc, parameterized to your paths and cadence
- diff a candidate `sync-run.yaml` against
  [docs/examples/sync-run.example.yaml](examples/sync-run.example.yaml) and
  point out missing fields before you commit it
- write the wrapper shell script (single-instance lock, log routing,
  heartbeat emission) for your specific monitoring stack
- review your `retention-policy.yaml` for missing approval fields before
  the `--confirm` run

Do not paste real Gong credentials, real customer profile YAML, or real
transcript output into a hosted agent unless your company has approved
that data path. The CLI surface, flags, env vars, YAML schema, and ignore
patterns from this repo are all safe to share.

## Where to go deeper

- [Operator sync runbook](runbooks/operator-sync.md) — pre-flight,
  backup, restore, decommissioning, verification checklist
- [Postgres client deployment runbook](runbooks/postgres-client-deployment.md)
  — Postgres-specific bootstrap, scoped reader grants, smoke
- [Enterprise deployment](enterprise-deployment.md) — deployment modes,
  storage classes, restricted CLI mode
- [Configuration surfaces](configuration-surfaces.md) — what is YAML, what
  is flags, what is env
- [Security model](security-model.md) — credential flow, capability model,
  trust boundaries
