# Postgres Client Deployment Runbook

Use this runbook for a controlled customer-hosted Postgres pilot where
`gongctl` sync jobs and `gongmcp` run as separate services and MCP reads a
physically redacted serving database.

For a simple all-in-one VM deployment, use the Compose starter at
`deploy/single-vm-postgres` as the runtime scaffold for this same source DB,
serving DB, scoped reader, and `gongmcp` separation. That starter keeps all
services on one host but still treats the source DB, serving DB, operator jobs,
and read-only MCP runtime as separate trust boundaries.

For customer-managed Kubernetes pilots, use the Kustomize starter at
`deploy/kubernetes/postgres-pilot`. It wires the same two-database boundary,
`gongmcp` Deployment, optional refresh CronJob, and operator smoke Job, but it
does not create Postgres databases, roles, backups, ingress, or external
secrets.

This is an operator runbook, not a business-user guide. Do not share database
URLs, passwords, Gong credentials, governance YAML, raw transcripts, raw cached
JSON, or unrestricted support bundles with business users.

## 1. Deployment Shape

Recommended client-safe layout:

- One customer-owned Postgres server or cluster.
- Source database: `gongctl_source`, the full operator cache.
- MCP serving database: `gongctl_mcp`, rebuilt from the source through
  governance redaction.
- Writer role: used only by operator sync and refresh jobs.
- Scoped MCP reader role: used only by `gongmcp` against `gongctl_mcp`.
- Auth gateway: customer-controlled HTTPS/OAuth or reverse-proxy boundary in
  front of `/mcp`.

`gongctl` gets Gong API credentials and writer DB credentials. `gongmcp` gets
only the scoped reader URL for `gongctl_mcp`; it must not receive Gong API
credentials or the source DB URL.

Keep the upstream `gongmcp` service on bearer auth even when JumpCloud/OIDC is
enabled. JumpCloud is the public user-auth layer in front of the service; the
gateway/broker should forward approved requests to `gongmcp` with the internal
bearer token. Do not use `auth-mode=none` outside localhost development.

## 2. Minimum Sizing

Start with customer platform guidance. For a first 90-day pilot similar to the
internal May 6, 2026 manual-test window, use at least:

- 4 vCPU
- 16 GiB RAM
- 2-4 GiB swap if running on a small VM
- encrypted persistent Postgres storage
- enough disk for source DB, serving DB, backups, logs, and temporary refresh
  headroom
- customer-managed backup/PITR policy

Tenant-scale sizing still needs customer-platform testing. Repo synthetic
smokes prove behavior, not production capacity.

## 3. Required Secrets

Store these in the customer secret manager or root-only deployment env files:

- `GONG_ACCESS_KEY`
- `GONG_ACCESS_KEY_SECRET`
- `GONGCTL_SOURCE_DATABASE_URL`
- `GONGCTL_MCP_DATABASE_URL`
- `GONGMCP_ANALYST_READER_URL`
- path to private governance config, for example
  `/run/secrets/ai-governance.yaml`; omit this and use
  `--no-governance-exclusions` / `GONGMCP_NO_GOVERNANCE_EXCLUSIONS=1` only when
  no customer exclusions exist
- auth gateway secrets such as OAuth client secrets, Keycloak credentials, or
  reverse-proxy bearer tokens

Never commit these values. Logs and evidence should use variable names, not
values.

Keep the Postgres URLs separate:

| Secret purpose | Example variable | Runtime use |
|---|---|---|
| Source writer URL | `GONGCTL_SOURCE_DATABASE_URL` | `gongctl sync ...` and `gongctl sync read-model --rebuild`. |
| Serving writer URL | `GONGCTL_MCP_DATABASE_URL` | `gongctl governance refresh-serving-db` and `gongctl mcp postgres-reader-apply`. |
| MCP scoped reader URL | `GONGMCP_ANALYST_READER_URL` | `gongmcp`, MCP smoke tests, and optional default-surface `gongctl sync status` checks. |

Each container receives only the URL it needs as `GONG_DATABASE_URL`. A writer
URL in a reader-side command will fail read-only posture checks; a reader URL
in a writer-side command cannot migrate, refresh, or reconcile grants.

For wrappers that execute several phases, assign `GONG_DATABASE_URL` on each
command or reset it at each phase boundary. Do not leave it globally set to the
serving writer URL while running source sync commands:

```bash
GONG_DATABASE_URL="$GONGCTL_SOURCE_DATABASE_URL" gongctl sync calls ...
GONG_DATABASE_URL="$GONGCTL_SOURCE_DATABASE_URL" gongctl sync read-model --rebuild

gongctl governance refresh-serving-db \
  --source "$GONGCTL_SOURCE_DATABASE_URL" \
  --target "$GONGCTL_MCP_DATABASE_URL" \
  --config "$GONGCTL_AI_GOVERNANCE_CONFIG"

GONG_DATABASE_URL="$GONGCTL_MCP_DATABASE_URL" \
  gongctl mcp postgres-reader-apply --preset broad-public-redacted ...

# For broad-public-redacted, validate through gongmcp with the same reader URL
# and preset, then call initialize, tools/list, and gong_status.
```

`GONGCTL_SOURCE_DATABASE_URL` and `GONGCTL_MCP_DATABASE_URL` are read directly
by `governance refresh-serving-db`; the other Postgres `gongctl` commands use
`GONG_DATABASE_URL` / `DATABASE_URL`.

## 4. Bootstrap Postgres

Create the source and serving databases on the same server or cluster:

```sql
CREATE DATABASE gongctl_source;
CREATE DATABASE gongctl_mcp;
```

Create roles outside `gongctl` using the customer's normal DBA process:

- source writer role for sync jobs
- serving writer/admin role for refresh and grant reconciliation
- scoped MCP reader role, for example `gongmcp_analyst_reader`

The scoped reader should be `LOGIN NOINHERIT` and should not be a member of
the writer role.

## 5. Pin Build And Images

For a tagged release, record the image tag and resolved digest:

```bash
docker buildx imagetools inspect ghcr.io/fyne-coder/gongcli_mcp/gongctl:vX.Y.Z
docker buildx imagetools inspect ghcr.io/fyne-coder/gongcli_mcp/gongmcp:vX.Y.Z
```

If using a development branch, record:

- git commit
- image build command
- image ID or local registry digest
- reason a published tag is not being used

Use the full `gongctl` image only for operator jobs. Use the MCP image or target
for `gongmcp`.

## 6. Sync Source Database

Set the source writer URL only in the operator job environment:

```bash
export GONG_DATABASE_URL="$GONGCTL_SOURCE_DATABASE_URL"
```

Run the approved sync scope. Example direct commands:

```bash
gongctl sync calls \
  --from YYYY-MM-DD \
  --to YYYY-MM-DD \
  --preset business \
  --governance-config /run/secrets/ai-governance.yaml

gongctl sync users

gongctl sync transcripts \
  --out-dir /srv/gongctl/transcripts \
  --limit 1000 \
  --batch-size 100 \
  --governance-config /run/secrets/ai-governance.yaml

gongctl sync crm-integrations
gongctl sync settings --kind trackers
gongctl sync settings --kind scorecards

gongctl sync read-model --rebuild
```

Use `--include-parties` only after sponsor approval for participant names,
emails, speaker IDs, and titles. In restricted mode, sensitive export steps
require explicit runtime approval as described in
[Operator sync runbook](operator-sync.md).
`sync transcripts` defaults to `--limit 100`; for first historical backfill,
set an approved higher limit or run repeated Jobs until an approved
reader-side smoke shows the expected transcript coverage.

Do not run `gongctl sync status` with the writable source URL still in
`GONG_DATABASE_URL`. In Postgres mode, `sync status` opens the database through
the default read-only validation path. For scoped MCP presets such as
`broad-public-redacted`, run the MCP smoke with the same preset after serving
DB refresh and scoped grants are in place.

For repeatable jobs, prefer reviewed `sync run --config` YAML plus `--dry-run`
for SQLite-backed schedules. For Postgres shared deployments, use direct
`gongctl sync ...` commands or a small reviewed wrapper that runs the approved
steps with `GONG_DATABASE_URL` set to the writable operator URL. The current
`sync run --config` runner is SQLite-oriented and should not be used as the
Postgres scheduler path.

In Kubernetes, the `gongctl` image has `gongctl` as its entrypoint and `--help`
as the default command. Override `args` for a single command, for example
`["sync", "calls", "--from", "YYYY-MM-DD", "--to", "YYYY-MM-DD", "--preset",
"minimal"]`, or override `command` to run a shell wrapper that executes the
approved sequence. Do not use the `gongmcp` image for writable sync jobs.

## 7. Refresh Redacted Serving Database

Rebuild the MCP serving database from the source database after each approved
sync or governance/blocklist change:

```bash
export GONGCTL_SOURCE_DATABASE_URL="postgres://..."
export GONGCTL_MCP_DATABASE_URL="postgres://..."
export GONGCTL_AI_GOVERNANCE_CONFIG=/run/secrets/ai-governance.yaml

gongctl deploy postgres-refresh > refresh-serving-db.json
```

When no customer exclusions exist, use the explicit contract instead of an
`ai-governance.yaml`:

```bash
gongctl deploy postgres-refresh \
  --source "$GONGCTL_SOURCE_DATABASE_URL" \
  --target "$GONGCTL_MCP_DATABASE_URL" \
  --no-governance-exclusions \
  > refresh-serving-db.json
```

Expected sanitized evidence in this case includes `suppressed_call_count: 0`.
The target remains an MCP serving database: the refresh copies all allowed
serving-slice rows but still skips operator/global tables documented by the
refresh output.

For one-off runs, explicit flags still override the environment:

```bash
gongctl deploy postgres-refresh \
  --source "$GONGCTL_SOURCE_DATABASE_URL" \
  --target "$GONGCTL_MCP_DATABASE_URL" \
  --preset business-workbench \
  --role gongmcp_business_workbench_reader \
  --database gongctl_mcp \
  --config /run/secrets/ai-governance.yaml \
  > refresh-serving-db.json
```

Review `refresh-serving-db.json`. It should contain sanitized counts,
fingerprints, removed-call counts, skipped-table notes, the refresh marker ID,
and the scoped-grant SQL hash. It must not contain database URLs, customer
names, blocklist values, call IDs, call titles, or raw transcript text.

`gongctl deploy postgres-refresh` runs the source read-model rebuild, redacted
serving refresh, and scoped reader grant reconciliation in one operator command.
The older `gongctl governance refresh-serving-db` command remains available for
low-level refresh/debug work, but it does not reconcile scoped reader grants.

After refresh, validate the deployment surface:

```bash
gongctl doctor postgres-deploy \
  --target "$GONGCTL_MCP_DATABASE_URL" \
  --preset business-workbench \
  --max-marker-age 24h > doctor-postgres-deploy.json

GONG_DATABASE_URL="$GONGMCP_ANALYST_READER_URL" \
gongctl sync status --preset business-workbench > sync-status.json
```

Both outputs are designed for operator evidence: they include pass/warn/fail
checks, preset validation, marker freshness, and sanitized fingerprints, not
database URLs or secret values.

Use the operator-held serving writer/admin URL for
`gongctl doctor postgres-deploy --target`. The scoped MCP reader role is not
granted direct `SELECT` on the refresh marker table; `sync status --preset`
validates the reader boundary, while `doctor postgres-deploy` performs the
operator-side marker attestation.

The current refresh rebuilds the target in place. For larger deployments that
need near-zero-downtime serving database refreshes, use a blue/green serving DB
pattern:

1. Keep `gongctl_mcp` as the active serving database.
2. Refresh `gongctl_mcp_next` from the operator/source database using the same
   governance config.
3. Apply the same scoped reader grants and policy checks to `gongctl_mcp_next`.
4. Run `gongmcp` smoke and the business-workbench GA harness against a staging
   MCP instance pointed at `gongctl_mcp_next`.
5. Cut over by changing the MCP reader URL or service secret to
   `gongctl_mcp_next`, then restart only `gongmcp`.
6. Keep the previous `gongctl_mcp` intact for rollback until the post-cutover
   smoke passes.

This is distinct from app-only deploys. App-only deploys refresh code/payloads
without rebuilding Keycloak/Caddy/oauth2-proxy. Blue/green serving DB refreshes
minimize downtime and rollback risk when the data plane changes.

When the same `gongctl_mcp` database, reader role/grants, auth settings,
binary/image, and tool preset remain in place, `gongmcp` does not need to be
recreated or restarted after this refresh; new MCP calls read the refreshed
serving database. Restart or redeploy `gongmcp` when changing
`GONG_DATABASE_URL`, the reader role or grants, auth/gateway settings, the
binary/image version, the tool preset/allowlist, or when cutting over to a
different serving database URL.

## 8. Reconcile Scoped Reader Grants

Apply scoped grants on the serving database, using the serving writer URL. This
step is required after the first serving DB build and after every serving DB
refresh, schema/image upgrade, or preset change. Do not rely on manual grant
edits; reconcile them through `postgres-reader-apply` so new reviewed columns
and functions are included.

If you ran `gongctl deploy postgres-refresh`, this reconciliation already ran
as part of the consolidated operator command. Run this section separately when
repairing grants, changing presets, or using the lower-level
`governance refresh-serving-db` command.

For the default customer-facing surface, use the same `business-workbench`
preset that `gongmcp` will run:

```bash
GONG_DATABASE_URL="$GONGCTL_MCP_DATABASE_URL" \
gongctl mcp postgres-reader-apply \
  --preset business-workbench \
  --role gongmcp_business_reader \
  --database gongctl_mcp \
  --dry-run > business-reader-grants.sql
```

Review the SQL. Then apply:

```bash
GONG_DATABASE_URL="$GONGCTL_MCP_DATABASE_URL" \
gongctl mcp postgres-reader-apply \
  --preset business-workbench \
  --role gongmcp_business_reader \
  --database gongctl_mcp \
  --apply > business-reader-apply.json
```

Retain only sanitized `business-reader-apply.json`; do not retain DB URLs or
passwords. Repeat the same pattern with `--preset analyst` or
`--preset analyst-expansion` only for approved analyst sessions that run a
matching `GONGMCP_TOOL_PRESET`.

For a broad redacted customer-test runtime using
`GONGMCP_TOOL_PRESET=broad-public-redacted`, apply matching scoped grants with
`--preset broad-public-redacted`. Older operator notes may refer to
`redacted-all-readonly`; that is the same underlying broad tool surface but an
internal/manual testing posture.

After applying grants, validate with the same surface the runtime will expose.
For `broad-public-redacted`, start or port-forward `gongmcp` with the scoped
reader URL and that preset, then call `initialize`, `tools/list`, and
`gong_status`. Generic `gongctl sync status` validates the default read-only
function set, so it can report missing functions that the scoped preset
intentionally does not grant.

In a multi-phase refresh wrapper, replace the final
`GONG_DATABASE_URL="$MCP_READER_DATABASE_URL" gongctl sync status` phase with:

1. Ensure the `gongmcp` runtime uses the scoped reader URL, not the source or
   serving writer URL.
2. Ensure the runtime preset matches the grant preset, for example
   `GONGMCP_TOOL_PRESET=broad-public-redacted`.
3. Port-forward or otherwise reach the internal `gongmcp` service before the
   public auth gateway.
4. Call MCP `initialize`, MCP `tools/list`, and MCP `tools/call` for
   `gong_status`.
5. Confirm `tools/list` shows the expected preset surface and `gong_status`
   returns successfully.

If this fails with `postgres read-only URL has CREATE privilege on public
schema`, confirm that the command is using the reader URL, not the source or
serving writer URL. If reader validation fails with missing column grants,
rerun `postgres-reader-apply` with the serving writer URL, the selected grant
preset, the actual reader role, and the current image version. If generic
`sync status` fails with missing function grants after a successful broad
redacted grant apply, switch to the MCP smoke; the CLI status path is not
preset-aware yet.

If it is unclear which role/database a Kubernetes secret reaches, inspect it
with an approved Postgres client and the same URL:

```sql
SELECT current_user, current_database();
```

The result should match the scoped reader role and the serving database for
reader-side checks.

## 9. Start `gongmcp`

Run `gongmcp` against the scoped reader URL for `gongctl_mcp`:

```bash
GONG_DATABASE_URL="$GONGMCP_ANALYST_READER_URL" \
GONGMCP_TOOL_PRESET=analyst-expansion \
GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS=1 \
GONGMCP_POSTGRES_REDACTED_SERVING_DB=1 \
gongmcp
```

When no customer exclusions exist, omit the governance YAML and pass
`GONGMCP_NO_GOVERNANCE_EXCLUSIONS=1` (or `gongmcp --no-governance-exclusions`)
for redacted-serving mode.

For the broad redacted customer-test surface with customer exclusions, the
runtime preset must match the grant preset and must have the same governance
config used to build the serving DB:

```bash
GONG_DATABASE_URL="$GONGMCP_ANALYST_READER_URL" \
GONGMCP_TOOL_PRESET=broad-public-redacted \
GONGMCP_POSTGRES_REDACTED_SERVING_DB=1 \
GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS=1 \
GONGMCP_AI_GOVERNANCE_CONFIG=/run/secrets/ai-governance.yaml \
gongmcp
```

For AWS ECS deployments, the Terraform runtime starter at
`deploy/terraform/aws-ecs-postgres` wires the same contract with the scoped
reader URL stored in AWS Secrets Manager as `GONG_DATABASE_URL`. That starter
does not create or manage Postgres databases, roles, backups, PITR, source-sync
jobs, or governance refresh jobs; complete sections 4 through 8 before using
it.

Only set `GONGMCP_POSTGRES_REDACTED_SERVING_DB=1` when `GONG_DATABASE_URL`
points at a serving DB generated by `deploy postgres-refresh` or the low-level
`governance refresh-serving-db`. Do not set it for source/raw DB readers.

Expected behavior:

- `analyst-expansion` tools list succeeds.
- for broad redacted customer-test deployments, `broad-public-redacted` tools
  list succeeds and `gong_status` reports the expected preset.
- scoped grant startup validation succeeds.
- startup logs include `postgres backend active: read-only MCP exposes ...`,
  `AI governance active: backend=postgres_redacted_serving ...`, and
  `serving mcp over http on ... path=/mcp auth_mode=bearer ...`.
- small-cell suppression is active for scoped Postgres analyst dimensions.
- Postgres `all-readonly`, `all-tools`, and `all` remain rejected.

If the pod logs show the Postgres backend, AI governance, and HTTP `/mcp`
listener as active, the DB/grant startup path has passed. Continue with the
internal MCP JSON-RPC smoke, then test the public JumpCloud/OIDC gateway path.

## 10. Auth Gateway And MCP URL

Expose only the gateway URL to users, for example:

```text
https://mcp.example.com/mcp
```

Before user access, verify:

- HTTPS is valid.
- For hosted ChatGPT/OpenAI or Claude/Anthropic connector paths, public DNS and
  TLS resolve from outside the company network and the provider backend can
  reach the gateway URL.
- unauthenticated `/mcp` is denied.
- blocked users are denied.
- approved users can authenticate.
- forged client-supplied identity headers are stripped or ignored.
- Origin allowlist is configured for browser-capable clients.
- `/healthz` is available for infrastructure checks.
- `/mcp` is used only for MCP JSON-RPC traffic.

For implementation details, use
[Remote MCP auth and connector setup](../remote-mcp-auth.md) and
[Remote MCP OAuth troubleshooting](remote-mcp-oauth-troubleshooting.md).

The public gateway can use JumpCloud/OIDC, but the upstream `gongmcp` auth mode
should remain `bearer` unless a future native OAuth mode is implemented and
tested.

## 11. Required Smoke

For Kubernetes pilots, first run the starter operator smoke Job. It validates
the DB-side deployment and a local stdio `gongmcp` `tools/list` call without
requiring a business-user MCP host, ChatGPT connector, Claude desktop config,
or OAuth session:

```bash
kubectl -n gongctl-postgres-pilot delete job gongctl-postgres-deploy-smoke --ignore-not-found
kubectl -n gongctl-postgres-pilot apply -f deploy/kubernetes/postgres-pilot/postgres-deploy-smoke-job.yaml
kubectl -n gongctl-postgres-pilot logs job/gongctl-postgres-deploy-smoke
```

Run the focused two-database smoke before client testing:

```bash
GONGCTL_PHASE13H_KEEP_ARTIFACTS=1 \
scripts/postgres-serving-db-analyst-smoke.sh
```

The smoke should prove:

- source and serving databases exist
- restricted synthetic rows are absent from the serving DB
- scoped analyst grants apply to the serving DB
- `gongmcp` can run `analyst-expansion` over the scoped reader URL
- broad company/title/transcript analyst probes work for allowed synthetic data
- restricted-company probes return zero results
- direct SQL raw reads are denied for the scoped role
- Postgres `all-readonly` remains rejected
- retained artifacts do not contain DB URLs, secrets, raw IDs, restricted
  values, or transcript text

For `broad-public-redacted`, use the JSON-RPC operator smoke from
[Postgres Kubernetes Operator Setup](../postgres-kubernetes-operator-setup.md#operator-mcp-smoke-test)
as the runtime acceptance check unless a preset-specific smoke script has been
added for that deployment. The existing analyst smoke targets
`analyst-expansion`, and the GA acceptance smoke targets the business-workbench
facade.

Then run the GA customer-acceptance smoke against the deployed MCP endpoint
to produce a non-secret pass/degraded/fail acceptance artifact for the pilot
record:

```bash
MCP_URL="https://mcp.example.com/mcp" \
MCP_BEARER_TOKEN="$REVIEWED_BEARER_TOKEN" \
READER_DB_URL="$GONGMCP_ANALYST_READER_URL" \
REDACTION_AUDIT_JSON="./serving-refresh-redaction-audit.json" \
KEEP_ARTIFACTS=1 \
ARTIFACT_DIR=./ga-acceptance-evidence \
scripts/postgres-ga-acceptance-smoke.sh
```

If the redaction audit is not stored as a JSON file, pass the compact
non-secret fields instead:

```bash
REDACTION_AUDIT_SOURCE_MINUS_REDACTED_ROWS="$SOURCE_MINUS_REDACTED_ROWS" \
REDACTION_AUDIT_GENERATED_AT="$REDACTION_AUDIT_GENERATED_AT" \
REDACTION_AUDIT_EVIDENCE_PATH="$REDACTION_AUDIT_EVIDENCE_PATH" \
scripts/postgres-ga-acceptance-smoke.sh
```

The audit values should point at the reviewed source-vs-serving validation
artifact from the refresh job. Do not pass database URLs, customer names, raw
call IDs, or transcript text through these fields.

The smoke validates seven contracts (runtime identity, six-tool surface,
routed operations, data readiness, governance/redaction, evidence workflow,
and scoped-reader read-only posture) and emits both a JSON report and an
operator Markdown summary. The runtime identity check is a GA release gate:
`version=dev`, `commit=unknown`, missing `build_date`, or equivalent
non-release provenance is a `fail` even if the MCP tools otherwise work. The
script exits 0 on `pass` or `degraded` (the JSON status field carries the
distinction) and exits 1 on `fail`. See
[Postgres client onboarding checklist §7](../postgres-client-onboarding-checklist.md)
for what each status means and how to record the result in the pilot
closeout.

Then run the manual business-user checklist:

- [Postgres client manual-test checklist](../postgres-client-manual-test-checklist.md)

## 12. Expected Baseline

Record these values in the pilot closeout:

- build version, git commit, image tag, and image digest
- source sync date window
- governance config fingerprint, not config contents
- serving refresh artifact path
- scoped reader apply artifact path
- `get_sync_status` call count, transcript count, and missing transcript count
- selected preset and role name
- MCP URL host, not credentials
- backup/restore artifact path
- open caveats and next review date

For the internal May 6, 2026 governed lab, the reviewed manual-test baseline
was approximately `4,803` calls, `4,803` transcripts, and `0` missing
transcripts. Do not reuse that count for a customer deployment; verify the live
target.

## 13. Backup And Restore

Before promotion:

- back up `gongctl_source`
- back up `gongctl_mcp`
- back up governance/profile/config files in protected storage
- verify restore into an isolated database
- run `gongctl sync read-model --rebuild` after restore when needed
- rerun `tools/list` and one approved `tools/call`
- verify scoped reader denial checks

Customer platform owners control backup encryption, PITR, retention, replica
strategy, and restore runbooks.

## 14. Rollback

Rollback order:

1. Disable user access at the auth gateway if needed.
2. Repoint `gongmcp` to the prior reviewed reader URL for the prior serving DB,
   or restore the prior MCP image digest.
3. Restart only `gongmcp` unless the gateway or database role changed.
4. Run `/healthz`, `tools/list`, `get_sync_status`, blocked-user denial, and
   direct DB denial checks.
5. Record the failed commit/image, affected preset, sanitized artifact paths,
   and operator action taken.

Do not start with a fresh Gong sync. Restore the last known-good MCP serving
path first, then diagnose sync or governance refresh separately.

## 15. Do Not Share

Do not put these in client-visible docs, support bundles, issue comments, or
chat transcripts:

- Gong access keys
- Postgres URLs
- OAuth client secrets
- governance/blocklist contents
- raw transcripts
- raw cached JSON
- restricted customer names
- unrestricted tool transcripts
- temporary artifact directories that have not been reviewed
