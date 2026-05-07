# Postgres Client Onboarding Checklist

Use this one-page checklist for a controlled customer-hosted Postgres pilot.
It assumes the operator has already reviewed the
[Postgres client pilot release packet](postgres-client-pilot-release-packet.md).
Use the
[Postgres client manual-test checklist](postgres-client-manual-test-checklist.md)
for first-session prompts, pass/fail notes, expected tool sequences, and
restricted-customer negative probes.
Use the
[Postgres client deployment runbook](runbooks/postgres-client-deployment.md)
for the two-database source/serving setup, scoped reader grant sequence,
gateway smoke, backup/restore, and rollback.

## 1. Choose The Pilot Surface

- Default business-user surface: `business-pilot`.
- Approved analyst surface: `analyst` or `analyst-expansion` only with scoped
  reader grants and `GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS=1`.
- Keep `all-readonly`, `all-tools`, and `all` disabled for Postgres.
- Record the sponsor-approved time window, business unit, and initial question
  set before syncing data.

## 2. Pin The Build

- If using a published release, record the exact image tag and resolved digest
  for both operator and MCP images.
- If using this development branch before a tag exists, record the git commit,
  local build command, and the fact that published image tags are not available
  yet.
- Business-user MCP hosts should use the MCP-only image or target. Keep the full
  `gongctl` image restricted to operator sync jobs.

## 3. Provision Postgres Roles

- Store the writable operator URL in the customer secret manager.
- Store the read-only MCP URL separately.
- For scoped `business-pilot` or analyst sessions, create the LOGIN role
  outside `gongctl`, then reconcile grants:

```bash
GONG_DATABASE_URL="$WRITER_URL" \
gongctl mcp postgres-reader-apply \
  --preset business-pilot \
  --role ROLE \
  --database DB \
  --dry-run
```

Review the dry run, then rerun with `--apply` if approved. Repeat with
`--preset analyst` for approved analyst sessions.

## 4. Run Synthetic Repo Evidence

Run these before introducing customer data:

```bash
go test -count=1 ./...
go vet ./...
make secret-scan
docker compose -f docker-compose.postgres.yml config --quiet
./scripts/postgres-smoke.sh
GONGCTL_POSTGRES_LOAD_CALLS=1200 \
GONGCTL_POSTGRES_LOAD_PROFILE_CACHE_ROWS=1200 \
./scripts/postgres-load-smoke.sh
```

Retain only reviewed evidence files: smoke summaries, `all-readonly` rejection,
scoped reader apply JSON, read-only denial artifacts, and analyst
small-cell/high-count evidence.

## 5. Run Customer-Platform Dry Run

Before real business users connect, run the same operating shape on the target
Postgres service class with synthetic or approved non-production data:

- backup and restore into an isolated database
- `gongctl sync read-model --rebuild`
- read-only `gongmcp` `tools/list`
- at least one approved `tools/call`
- read-only write-denial and raw-read-denial checks
- scoped reader reconciliation for each enabled preset
- statement-timeout, connection-limit, backup/PITR, and retention settings
- rollback using the prior image digest and prior restored cache/config

Stop if the dry run is not reviewed and signed off.

## 6. Connect The First Business User

- Start with `business-pilot` unless the sponsor approved analyst workflows.
- Run `get_sync_status` first.
- Confirm transcript/profile/readiness caveats are visible.
- Keep first prompts inside the approved question set.
- Escalate stale cache, missing tools, unexpected raw identifiers, or
  suppression warnings the user does not understand back to the operator.

## 7. Record Closeout

For the pilot record, save:

- binary version, commit, image tag, and digest
- `mcp_server` identity from `gong_status` or `/healthz`, including
  `deployment_id`, `tool_preset`, `started_at_utc`, and transcript evidence
  provenance
- enabled preset and role name, not passwords or URLs
- sync scope and date window
- smoke command names and reviewed evidence paths
- rollback artifact location
- open limitations and next review date

Do not save raw transcripts, database URLs, passwords, customer identifiers, or
whole temp directories in the client packet.
