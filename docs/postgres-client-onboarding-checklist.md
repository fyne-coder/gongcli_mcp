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

- Default client business-user surface: `business-workbench` (six stable
  facade tools; internal routing keeps reviewed analyst operations available
  without exposing a broad top-level tool list).
- Default ad-hoc question path: `gong_analyze` operation `question.answer`.
  It returns governed evidence packs for host-model synthesis and includes
  per-call duration, but it keeps call-title exposure constrained because
  titles can contain customer names.
- Legacy narrow aggregate/status surface: `business-pilot`.
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
- For scoped `business-workbench`, `business-pilot`, or analyst sessions, create the LOGIN role
  outside `gongctl`, then reconcile grants:

```bash
GONG_DATABASE_URL="$WRITER_URL" \
gongctl mcp postgres-reader-apply \
  --preset business-workbench \
  --role ROLE \
  --database DB \
  --dry-run
```

Review the dry run, then rerun with `--apply` if approved. Use
`--preset business-pilot` only for the legacy narrow aggregate/status pilot
lane. Repeat with `--preset analyst` for approved analyst sessions.

## 4. Review The Customer Profile

Treat `gongctl profile discover` as a starter, not a deployable customer
configuration. Before client-facing persona, industry, lifecycle, won/lost, or
loss-reason answers are used, RevOps or the process owner should review a YAML
profile kept outside git and confirm these mappings:

- CRM deal/opportunity object aliases and the exact stage field used for
  `open`, `closed_won`, and `closed_lost`.
- Loss reason, close reason, churn reason, competitor, forecast category,
  amount, close date, and opportunity type fields where the customer expects
  pipeline-outcome questions.
- Account/company object aliases plus industry, segment, region, and named
  account fields where industry or targeting questions matter.
- Contact/lead/participant title fields used for persona analysis, plus whether
  Gong speaker affiliation is populated enough to call snippets customer-side.
- Post-sales/renewal/support lifecycle values, with negative examples proving
  they do not catch active new-logo sales calls.

Run `gongctl profile validate`, import only the reviewed YAML, rebuild the
read model, then confirm `gongctl sync status` reports an active profile and a
fresh profile cache. If a source CRM field is not mapped or not populated,
`gongmcp` should report that as a data-readiness limitation rather than filling
the gap from transcript text.

## 5. Run Synthetic Repo Evidence

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

## 6. Run Customer-Platform Dry Run

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

## 7. Connect The First Business User

- Start with `business-workbench` unless the sponsor approved trained analyst
  workflows.
- Run `gong_status` first and confirm the `mcp_server` identity.
- Confirm transcript/profile/readiness caveats are visible.
- Keep first prompts inside the approved question set.
- Escalate stale cache, missing tools, unexpected raw identifiers, or
  suppression warnings the user does not understand back to the operator.

## 8. Record Closeout

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
