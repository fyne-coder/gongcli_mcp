# Postgres Client Deployment Runbook

Use this runbook for a controlled customer-hosted Postgres pilot where
`gongctl` sync jobs and `gongmcp` run as separate services and MCP reads a
physically redacted serving database.

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
  `/run/secrets/ai-governance.yaml`
- auth gateway secrets such as OAuth client secrets, Keycloak credentials, or
  reverse-proxy bearer tokens

Never commit these values. Logs and evidence should use variable names, not
values.

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
  --batch-size 100 \
  --governance-config /run/secrets/ai-governance.yaml

gongctl sync crm-integrations
gongctl sync settings --kind trackers
gongctl sync settings --kind scorecards

gongctl sync read-model --rebuild
gongctl sync status
```

Use `--include-parties` only after sponsor approval for participant names,
emails, speaker IDs, and titles. In restricted mode, sensitive export steps
require explicit runtime approval as described in
[Operator sync runbook](operator-sync.md).

For repeatable jobs, prefer reviewed `sync run --config` YAML plus `--dry-run`
before enabling cron, launchd, Kubernetes, or container schedules.

## 7. Refresh Redacted Serving Database

Rebuild the MCP serving database from the source database after each approved
sync or governance/blocklist change:

```bash
gongctl governance refresh-serving-db \
  --source "$GONGCTL_SOURCE_DATABASE_URL" \
  --target "$GONGCTL_MCP_DATABASE_URL" \
  --config /run/secrets/ai-governance.yaml \
  > refresh-serving-db.json
```

Review `refresh-serving-db.json`. It should contain sanitized counts,
fingerprints, removed-call counts, and skipped-table notes. It must not contain
database URLs, customer names, blocklist values, call IDs, call titles, or raw
transcript text.

The current refresh rebuilds the target in place. For larger deployments, queue
or implement a later blue/green flow with `gongctl_mcp_next` and a controlled
reader cutover.

## 8. Reconcile Scoped Reader Grants

Apply scoped analyst grants on the serving database, using the serving writer
URL:

```bash
GONG_DATABASE_URL="$GONGCTL_MCP_DATABASE_URL" \
gongctl mcp postgres-reader-apply \
  --preset analyst-expansion \
  --role gongmcp_analyst_reader \
  --database gongctl_mcp \
  --dry-run > analyst-reader-grants.sql
```

Review the SQL. Then apply:

```bash
GONG_DATABASE_URL="$GONGCTL_MCP_DATABASE_URL" \
gongctl mcp postgres-reader-apply \
  --preset analyst-expansion \
  --role gongmcp_analyst_reader \
  --database gongctl_mcp \
  --apply > analyst-reader-apply.json
```

Retain only sanitized `analyst-reader-apply.json`; do not retain DB URLs or
passwords.

## 9. Start `gongmcp`

Run `gongmcp` against the scoped reader URL for `gongctl_mcp`:

```bash
GONG_DATABASE_URL="$GONGMCP_ANALYST_READER_URL" \
GONGMCP_TOOL_PRESET=analyst-expansion \
GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS=1 \
GONGMCP_POSTGRES_REDACTED_SERVING_DB=1 \
gongmcp
```

Only set `GONGMCP_POSTGRES_REDACTED_SERVING_DB=1` when `GONG_DATABASE_URL`
points at a serving DB generated by `governance refresh-serving-db`. Do not set
it for source/raw DB readers.

Expected behavior:

- `analyst-expansion` tools list succeeds.
- scoped grant startup validation succeeds.
- small-cell suppression is active for scoped Postgres analyst dimensions.
- Postgres `all-readonly`, `all-tools`, and `all` remain rejected.

## 10. Auth Gateway And MCP URL

Expose only the gateway URL to users, for example:

```text
https://mcp.example.com/mcp
```

Before user access, verify:

- HTTPS is valid.
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

## 11. Required Smoke

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

