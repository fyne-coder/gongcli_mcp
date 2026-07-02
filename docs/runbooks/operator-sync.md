# Operator Sync Runbook

## Scope

This runbook is for the IT / RevOps operator who refreshes protected Gong cache
data for an enterprise pilot. It assumes the current product boundary:

- `gongctl` performs writable sync and profile operations
- `gongmcp` reads the configured cache store read-only: SQLite for
  local/single-host deployments, or a Postgres reader role for shared
  deployments
- business users consume approved MCP tools and do not run sync jobs

Do not paste secrets, transcript text, raw payloads, or deployment-specific IDs
into tickets, docs, or chat while executing this runbook.

## Prerequisites

Before running a sync:

- use a managed host or managed workstation approved for customer data
- confirm Gong credentials are available through environment variables or an
  ignored env file
- confirm the protected data root exists outside the source repo
- confirm there is enough free disk for SQLite growth, transcript output, and
  backup copies
- confirm you know which sync scope is approved for this tenant and pilot phase

Recommended protected directories under a generic `<data-root>`:

- `<data-root>/cache/` for SQLite databases
- `<data-root>/transcripts/` for transcript output
- `<data-root>/profiles/` for reviewed profile YAML
- `<data-root>/backups/` for pre-change snapshots

Set the parent `<data-root>` permissions before running syncs. The current
implementation may create subdirectories that are traversable by local users if
the parent directory is broad, so the parent data root must be restricted by the
host or volume ACLs, for example owner-only access on single-user pilots.

## Approved Operating Rules

- Run writable commands only from `gongctl`, never from `gongmcp`.
- Default company-managed CLI jobs to `GONGCTL_RESTRICTED=1`.
- Keep real SQLite, transcript, and profile files outside the checkout.
- Use metadata-only logs. Do not log transcript bodies or secret values.
- Do not give business users Gong credentials, writable storage, or transcript
  export commands.
- For containerized stdio MCP, require a read-only mount and `--network none`.
  HTTP MCP should expose only the MCP port through the approved proxy path.

## Preflight

1. Verify the binary or image version you intend to use.
2. Confirm the protected data root is mounted with the expected permissions.
3. Confirm the backup target is available.
4. Run a credential check.

Source build example:

```bash
export GONGCTL_RESTRICTED=1
bin/gongctl version
bin/gongctl auth check
```

Docker example:

```bash
docker run --rm --env-file .env gongctl:local version
docker run --rm --env-file .env -v <data-root>:/data gongctl:local auth check
```

If `auth check` fails, stop here and fix credentials before touching the cache.

For the Docker-based operator path, `scripts/docker-smoke.sh` is the bounded
end-to-end smoke after credentials and `<data-root>` are set. It covers `auth
check`, a one-page call sync, `sync status`, and a read-only `gongmcp`
`tools/list` request.

## Backup Before Refresh

Take a restorable copy before major refreshes, upgrades, or profile changes.

Minimum backup set:

- active SQLite file
- profile YAML files used by this tenant
- transcript directory if transcript sync is enabled for the pilot
- for Postgres shared deployments, the database backup or snapshot plus any
  WAL/PITR material, role/grant definitions, and external transcript/profile
  storage used by the same deployment

The exact copy command depends on the host. The requirement is operational, not
tool-specific: produce a dated backup in protected storage and verify the copy
completed before continuing.

## Writable Refresh Procedure

Run only the commands approved for this tenant. A common pilot refresh sequence
is:

```bash
bin/gongctl sync calls --db <data-root>/cache/gong.db --from YYYY-MM-DD --to YYYY-MM-DD --preset minimal
bin/gongctl sync users --db <data-root>/cache/gong.db
bin/gongctl sync transcripts --db <data-root>/cache/gong.db --out-dir <data-root>/transcripts --limit 50 --batch-size 100
bin/gongctl sync crm-integrations --db <data-root>/cache/gong.db
bin/gongctl sync crm-schema --db <data-root>/cache/gong.db --integration-id <approved-integration> --object-type ACCOUNT --object-type DEAL
bin/gongctl sync settings --db <data-root>/cache/gong.db --kind scorecards
bin/gongctl sync status --db <data-root>/cache/gong.db
```

Only add `--include-parties` after approval for participant-level data capture.
That option requests call participant fields such as names, emails, speaker IDs,
and titles and stores them in cached raw call payloads. If Gong rejects the
participant selector, the sync retries without parties and records
`include_parties_result=omitted_fallback` in sync history so operators can see
that participant/title data was not captured.

For scheduled or repeatable jobs, keep the approved stages in one YAML config
and dry-run the exact file before enabling it in cron, launchd, or a container
job:

```bash
cat > <data-root>/configs/company-sync.yaml <<'YAML'
version: 1
db: ../cache/gong.db
steps:
  - name: daily_calls
    action: calls
    from: 2026-04-01
    to: 2026-04-02
    preset: minimal
    governance_config: ../private/ai-governance.yaml
  - name: missing_transcripts
    action: transcripts
    out_dir: ../transcripts
    limit: 50
    batch_size: 100
    governance_config: ../private/ai-governance.yaml
  - name: directory_users
    action: users
  - name: tracker_settings
    action: settings
    settings_kind: trackers
  - name: scorecard_activity
    action: scorecard-activity
    call_from: 2026-01-01
    call_to: 2026-04-01
    review_method: BOTH
YAML

bin/gongctl sync run --config <data-root>/configs/company-sync.yaml --dry-run
bin/gongctl sync run --config <data-root>/configs/company-sync.yaml
bin/gongctl cache inventory --db <data-root>/cache/gong.db
bin/gongctl cache purge --db <data-root>/cache/gong.db --older-than 2026-04-01 --dry-run
GONG_DATABASE_URL="$GONGMCP_READER_DATABASE_URL" bin/gongctl cache purge --older-than 2026-04-01
GONG_DATABASE_URL="$GONGCTL_WRITER_DATABASE_URL" bin/gongctl cache purge --older-than 2026-04-01 --confirm
```

Notes:

- Use `--preset minimal` unless approved business questions require embedded CRM
  context from `business` or `all`.
- For clients with restricted-customer rules, supply the private governance
  config during call and transcript sync, either as direct CLI flags or as
  per-step `governance_config` values in reviewed `sync run --config` YAML.
  Example:
  `sync calls --governance-config /srv/gongctl/private/ai-governance.yaml` and
  `sync transcripts --governance-config /srv/gongctl/private/ai-governance.yaml`.
  Governed call sync skips matched calls before writing them to the cache and
  removes previously cached call-scoped rows for matched call IDs. Transcript
  sync re-evaluates cached call payloads for transcript candidates and excludes
  matches before making transcript requests.
- Transcript sync increases the sensitivity of the stored data. Only run it when
  transcript-backed search or analysis is in scope.
- In restricted mode, `sync transcripts`, `sync calls --preset business`,
  `sync calls --preset all`, `sync calls --include-parties`,
  `sync calls --include-highlights`,
  `calls list --context extended`, transcript export commands, raw API
  passthrough, and raw call JSON require `--allow-sensitive-export` or
  `GONGCTL_ALLOW_SENSITIVE_EXPORT=1`. Treat that override as an approval gate,
  not a convenience flag.
- Postgres highlight capture also populates `call_ai_highlights` during
  read-model refresh. The table is sensitive and remains operator/internal
  until a reviewed MCP facade operation is added.
- `sync run --config` files cannot contain a per-step sensitive-export bypass.
  Sensitive steps are visible in dry-run output, but restricted-mode approval is
  supplied only at runtime by the operator.
- `sync status` is the required post-refresh verification step because it shows
  cache population and readiness state.
- `cache inventory` is the secondary verification step for storage governance.
  It reports database size, table counts, oldest/newest call dates, transcript
  presence, CRM-context presence, profile status, and last sync metadata.
- `cache purge --older-than YYYY-MM-DD` is dry-run unless `--confirm` is present.
  Save the dry-run JSON plan with the retention/change record, then run the
  confirmed command only after backup, legal-hold, and data-owner checks pass.
  SQLite uses `--db`; Postgres omits `--db` and uses `GONG_DATABASE_URL` or
  `DATABASE_URL`. The Postgres reader URL can preview metadata-only counts, and
  confirmed Postgres purge requires a writable URL. The command removes matching
  cached calls and dependent transcript, CRM-context, read-model, profile
  fact-cache, scorecard-activity, and governance-suppression rows. SQLite
  confirmed purge additionally enables `secure_delete`, checkpoint/truncate WAL
  state, and runs `VACUUM`; Postgres WAL, replicas, snapshots, dumps, and
  backups remain outside the command. It does not remove transcript JSON files,
  snapshots, backups, profiles, CRM schema inventory, settings inventory, or
  sync history. Postgres keeps call-ID tombstones as operational metadata to
  block accidental rehydration of purged call-scoped rows by later sync steps.
- Run confirmed Postgres purge during a maintenance window with scheduled
  sync/write jobs stopped. The command takes a database advisory writer lock and
  deletes the materialized call ID set for that run, but operator scheduling is
  still the retention control of record.
- For pre-rollout validation, `scripts/postgres-contention-smoke.sh` exercises a
  larger synthetic Postgres dataset with concurrent read-model rebuild,
  profile-cache refresh, purge, reader status, and MCP smoke. Treat that as a
  repo-local release evidence for shipped writer-lock behavior at the
  configured synthetic size, not a benchmark or customer capacity proof.
- For profile-backed backlog, analyst presence dimensions, and
  transcript-search pre-rollout validation,
  `scripts/postgres-capacity-drill.sh` runs the Postgres load smoke at a
  bounded synthetic size, validates the generated profile-cache,
  profile-backlog, scoped `business-pilot` MCP, scoped `analyst` dimension MCP,
  profile EXPLAIN, analyst persona/loss-reason dimension EXPLAIN, and
  transcript-search EXPLAIN artifacts directly, and writes a sanitized
  `capacity-summary.json`. Archive `capacity-summary.json` plus only the
  generated files named in its `evidence` map after the drill and load-smoke
  leak scans pass; do not archive whole artifact directories, stdout/stderr, or
  intermediate files unless separately reviewed for the customer record. This
  is synthetic pre-rollout evidence only; it does not replace a
  customer-platform benchmark using the approved Postgres service class,
  backup/PITR settings, concurrency target, retention window, and real
  deployment limits.
- For client pilot analyst sessions, use a scoped analyst reader with
  `GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS=1`. The MCP server then suppresses
  business-analysis dimension buckets below 3 calls and reports
  `small_cell_suppression_applied` in the tool response. If a pilot needs a
  different minimum, a database-enforced governed aggregate design should be
  reviewed before sharing broader analyst outputs.

```bash
# Default bounded synthetic drill: 5,000 calls and 5,000 profile-cache rows.
./scripts/postgres-capacity-drill.sh

# Smaller repo validation run used in CI/local review.
GONGCTL_POSTGRES_CAPACITY_COMPOSE_PROJECT=gongctl-postgres-capacity-review \
GONGCTL_POSTGRES_CAPACITY_PORT=55545 \
GONGCTL_POSTGRES_CAPACITY_CALLS=1200 \
GONGCTL_POSTGRES_CAPACITY_PROFILE_ROWS=1200 \
./scripts/postgres-capacity-drill.sh
```

Supported sizing/path knobs are
`GONGCTL_POSTGRES_CAPACITY_COMPOSE_PROJECT`,
`GONGCTL_POSTGRES_CAPACITY_PORT`, `GONGCTL_POSTGRES_CAPACITY_CALLS`,
`GONGCTL_POSTGRES_CAPACITY_PROFILE_ROWS`, and
`GONGCTL_POSTGRES_CAPACITY_ARTIFACT_DIR`. The drill accepts 1,200-5,000 calls
and profile rows and requires explicit artifact directories to live under
`/tmp/gongctl-postgres-capacity.*`.
- For scheduled retention jobs, prefer a reviewed YAML policy instead of
  re-encoding the cutoff and approval state in job flags:

```yaml
version: 1
older_than: 2026-04-01
approval:
  reference: CHANGE-RETENTION-123
  approved_by: revops-retention-reviewer
  approved_at: 2026-05-05
  data_owner: revenue-operations
  backup_reference: backup-20260505-approved
  legal_hold_reviewed: true
```

Run the policy in dry-run mode first and archive the JSON plan with the change
record:

```bash
GONG_DATABASE_URL="$GONGMCP_READER_DATABASE_URL" bin/gongctl cache purge --config <data-root>/configs/retention-policy.yaml --dry-run
```

Confirmed config-driven purge fails closed unless the approval reference,
approver, approval date, data owner, backup reference, and legal-hold review are
present. Use the writable URL only for the approved change window:

```bash
GONG_DATABASE_URL="$GONGCTL_WRITER_DATABASE_URL" bin/gongctl cache purge --config <data-root>/configs/retention-policy.yaml --confirm
```

The command returns the policy SHA-256 and sanitized approval metadata, but does
not return database URLs, raw call IDs, transcript text, or the local policy
file path for Postgres output.
- If the pilot uses a reviewed business profile, validate and import it only
  from the protected profile path:

```bash
bin/gongctl profile validate --db <data-root>/cache/gong.db --profile <data-root>/profiles/gongctl-profile.yaml
bin/gongctl profile import --db <data-root>/cache/gong.db --profile <data-root>/profiles/gongctl-profile.yaml
bin/gongctl sync status --db <data-root>/cache/gong.db
```

## Read-Only MCP Verification

After a successful refresh, verify the read-only MCP runtime separately from the
writable sync process.

Docker example:

```bash
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"operator-smoke","version":"0"}}}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' \
| docker run --rm -i \
    --network none \
    -v <data-root>/cache:/data:ro \
    gongctl:mcp-local \
    --db /data/gong.db
```

Pass criteria:

- `gongmcp` starts without requesting Gong credentials
- the data mount is read-only
- the runtime works without network access
- direct `tools/list` shows only the configured preset or allowlist when
  `--tool-preset`, `GONGMCP_TOOL_PRESET`, `--tool-allowlist`, or
  `GONGMCP_TOOL_ALLOWLIST` is set

If you expose MCP through a host app, point that host at the same read-only
database path and expose only the approved tool set. Keep the `gongmcp`
allowlist and host-side tool policy aligned so wrapper configuration does not
accidentally broaden the business-user surface.

For shared private-pilot HTTP access, run `gongmcp` behind TLS termination at a
trusted company proxy/gateway and require bearer auth plus an explicit
allowlist:

```bash
GONGMCP_BEARER_TOKEN_FILE=/run/secrets/gongmcp_token \
GONGMCP_TOOL_PRESET=business-pilot \
  gongmcp --http 127.0.0.1:8080 --auth-mode bearer --db <data-root>/cache/gong.db
```

The token is owned by IT/platform operations and should live in a secret file,
systemd environment file, Docker/Kubernetes secret, or company secret manager.
Do not store it in the repo, images, SQLite, docs, or shared logs. If HTTP binds
to a non-local address, `--allow-open-network` is required and the deployment
must still use TLS termination or an equivalent trusted network boundary.

If the deployment has customer-specific AI-use restrictions, run a local audit
before exposing MCP:

```bash
gongctl governance audit \
  --db <data-root>/cache/gong.db \
  --config <private-config-root>/ai-governance.yaml
```

Default to a physically filtered MCP DB when a blocklist exists:

```bash
gongctl governance export-filtered-db \
  --db <data-root>/cache/gong.db \
  --config <private-config-root>/ai-governance.yaml \
  --out <data-root>/cache/gong-mcp-governed.db \
  --overwrite
```

Then point MCP at the filtered DB:

```bash
gongmcp --db <data-root>/cache/gong-mcp-governed.db
```

Raw-DB governance remains a fallback: start `gongmcp` with the same private
config through `GONGMCP_AI_GOVERNANCE_CONFIG` or `--ai-governance-config`.
Restart `gongmcp` after cache refreshes or governance-config changes. This is
mandatory because `gongmcp` fingerprints the cache and config, then fails
closed if either changes while the process is running.

## Scheduled Refresh Ownership

If refreshes are scheduled instead of manual:

- assign one named owner for the job and one backup owner
- record the approved cadence and scope
- keep the job config file under operator-controlled storage so the exact staged
  refresh is reviewable
- run scheduled sync where the writable data root and backup target are already
  available
- keep MCP consumer hosts read-only and separate from the refresh job when
  possible

Recommended ownership split:

- operator job owns credentials, sync, backups, and profile imports
- business-user MCP host owns read-only access only

## Backup, Retention, And Restore

Minimum controls:

- retain at least one known-good pre-upgrade SQLite backup
- retain profile backups alongside the active profile
- treat transcript retention separately because transcript volume and
  sensitivity may exceed the cache retention window

Restore test:

1. restore a backup copy into an isolated protected location
2. start `gongmcp` against the restored SQLite file with a read-only mount
3. verify `tools/list` or `get_sync_status`
4. only then treat the backup as usable

Postgres restore test:

1. restore the Postgres backup into an isolated database or instance
2. run `gongctl sync read-model --rebuild` with the writable database URL
3. compare source and restored table counts for the approved validation scope
4. start `gongmcp` with the restored read-only database URL and run
   `get_sync_status`, `search_calls`, and `search_transcript_segments`
5. prove the restored reader role cannot write and cannot directly read raw
   payload columns

For local release validation with synthetic fixtures only, run:

```bash
scripts/postgres-backup-restore-smoke.sh
```

That script writes synthetic-only evidence under
`/tmp/gongctl-postgres-restore-*` and checks those public evidence files for
database URLs, dev passwords, the local host marker, and raw payload markers.
The MCP smoke artifact can include synthetic transcript snippets; production
restore evidence must use synthetic or approved non-production data and must be
sanitized before sharing.

## Decommissioning

When the pilot ends or a tenant is removed:

1. stop manual and scheduled sync jobs
2. remove MCP host entries that reference the tenant cache
3. revoke or rotate the Gong credentials used for this pilot
4. archive or destroy SQLite, transcript, profile, and backup data per approved
   retention policy
5. remove stale local copies from operator machines and managed hosts

## Incident Response

### 1. Credential failure

Symptoms:

- `auth check` fails
- sync calls return auth errors

Response:

1. stop sync retries
2. verify the expected credential source
3. rotate or replace the credential if compromise is possible
4. rerun `auth check` before resuming scheduled jobs

### 2. Cache or schema mismatch

Symptoms:

- `gongmcp` fails during startup after a binary upgrade
- the CLI reports an older SQLite schema version

Response:

1. stop the MCP process
2. take a backup copy of the affected SQLite file
3. run a writable CLI command such as `sync status` against the protected cache
   to perform any required cache-side update
4. rerun the read-only MCP smoke
5. restart the MCP host only after the cache is current

Do not try to repair schema drift from the read-only MCP runtime.

### 3. Data exposure incident

Symptoms:

- transcript text appears in the wrong channel
- sensitive files were mounted too broadly
- an export landed in an unapproved location

Response:

1. stop the affected process and remove the bad mount or host config
2. preserve metadata-only evidence
3. remove or quarantine the exposed copy per company policy
4. rotate credentials if secrets were exposed
5. review whether sync scope, logging, or MCP tool exposure needs tightening

### 4. Failed or partial refresh

Symptoms:

- `sync status` shows incomplete readiness
- transcripts or settings are missing unexpectedly

Response:

1. keep the previous approved MCP runtime in place if possible
2. inspect operator-side stderr summaries and job metadata
3. rerun only the failed sync slice once the cause is fixed
4. confirm `sync status` before promoting the refreshed cache

## Verification Checklist

Before closing the change window, confirm:

- pre-refresh backup completed
- required sync commands completed
- `sync status` reflects the expected cache state
- read-only `gongmcp` smoke passed with no network and no credentials
- for Postgres changes, backup/restore or restore-drill evidence exists and the
  restored read-only MCP smoke passed
- for Postgres changes, repo-local contention smoke passed for the release
  candidate; archive only the files listed under `summary.json.artifacts` after
  the script's artifact leak scan passes, and do not archive generated binaries,
  profile YAML, or retention policy YAML unless separately reviewed
- for Postgres first-client or high-volume rollout, a customer-platform
  contention/capacity benchmark passed for the approved load target, or the
  deployment owner explicitly accepted that risk
- any scheduled job change has a named owner and documented cadence
- any scheduled retention job uses a reviewed `cache purge --config` policy or
  has an equivalent approval record tied to the dry-run plan
