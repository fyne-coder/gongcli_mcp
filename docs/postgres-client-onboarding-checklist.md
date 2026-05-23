# Postgres Client Onboarding Checklist

Use this one-page checklist for a controlled customer-hosted Postgres
deployment. It assumes the operator has already reviewed the
[Postgres client pilot release packet](postgres-client-pilot-release-packet.md)
or the equivalent customer security packet for the tagged release.
Use the
[Postgres client manual-test checklist](postgres-client-manual-test-checklist.md)
for first-session prompts, pass/fail notes, expected tool sequences, and
restricted-customer negative probes.
Use the
[Postgres client deployment runbook](runbooks/postgres-client-deployment.md)
for the two-database source/serving setup, scoped reader grant sequence,
gateway smoke, backup/restore, and rollback.
For a small IT setup on one Linux host, use
[`deploy/single-vm-postgres`](../deploy/single-vm-postgres/README.md) as the
Compose scaffold; it still follows the same source DB, serving DB, scoped
reader, and read-only `gongmcp` boundary.
For Kubernetes operator Jobs, first-run DB initialization, recurring sync
cadence, image `args`, and non-user MCP smoke tests, use the
[Postgres Kubernetes operator setup](postgres-kubernetes-operator-setup.md).

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

- For GA, record the exact release image tag and resolved digest for both
  operator and MCP images.
- If using a development branch before a tag exists, record the git commit,
  local build command, and the fact that it is pilot-only rather than a
  versioned GA release.
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

Run both profile validation passes, import only the reviewed YAML, rebuild the
read model, then confirm `gongctl sync status` reports an active profile and a
fresh profile cache:

```bash
gongctl profile validate --db "$SQLITE_OR_SOURCE_DB" --profile "$REVIEWED_PROFILE"
gongctl profile validate --db "$SQLITE_OR_SOURCE_DB" --profile "$REVIEWED_PROFILE" --ga-readiness
gongctl profile import --db "$SQLITE_OR_SOURCE_DB" --profile "$REVIEWED_PROFILE"
```

For Postgres source DBs, omit `--db` and set the writable URL in the
environment before rebuilding:

```bash
GONG_DATABASE_URL="$GONGCTL_SOURCE_DATABASE_URL" gongctl sync read-model --rebuild
```

For SQLite pilot caches, rebuild behavior is handled by the writable profile
import path; `sync read-model` is Postgres-only.

The `--ga-readiness` pass is the customer-deployment gate. It exits non-zero
when baseline validation fails or when the mechanical readiness checklist finds
CreatedDate-only concepts, missing lifecycle buckets, unmapped methodology, or
missing loss-reason mapping. If a source CRM field is not mapped or not
populated, `gongmcp` should report that as a data-readiness limitation rather
than filling the gap from transcript text.

## 5. Run Synthetic Repo Evidence

Run these before introducing customer data:

```bash
go test -count=1 ./...
go vet ./...
make secret-scan
docker compose -f docker-compose.postgres.yml config --quiet
docker compose --env-file deploy/single-vm-postgres/single-vm.env.example \
  -f deploy/single-vm-postgres/docker-compose.yml config --quiet
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

## 7. Run The GA Customer Acceptance Smoke

Before connecting any business user, run the single GA acceptance command
against the deployed MCP endpoint. The smoke is read-only and produces a
non-secret JSON report plus an operator Markdown summary:

```bash
MCP_URL="https://mcp.example.com/mcp" \
MCP_BEARER_TOKEN="$REVIEWED_BEARER_TOKEN" \
READER_DB_URL="$GONGMCP_ANALYST_READER_URL" \
REDACTION_AUDIT_SOURCE_MINUS_REDACTED_ROWS="$SOURCE_MINUS_REDACTED_ROWS" \
REDACTION_AUDIT_GENERATED_AT="$REDACTION_AUDIT_GENERATED_AT" \
REDACTION_AUDIT_EVIDENCE_PATH="$REDACTION_AUDIT_EVIDENCE_PATH" \
KEEP_ARTIFACTS=1 \
ARTIFACT_DIR=./ga-acceptance-evidence \
scripts/postgres-ga-acceptance-smoke.sh
```

Instead of the three `REDACTION_AUDIT_*` fields, operators may pass
`REDACTION_AUDIT_JSON=/path/to/redaction-audit.json` with
`available`, `source_minus_redacted_rows`, `generated_at`, and
`evidence_path`. The evidence path should be a non-secret pointer to the
source-vs-serving validation artifact, not a database URL or raw data export.

The smoke validates seven contracts and reports each as `pass`, `degraded`, or
`fail`:

1. `runtime_identity` — `deployment_id`, release version, commit,
   `build_date`, `started_at_utc`, `tool_preset`, and visible tool count are
   present in `gong_status`. `version=dev`, `commit=unknown`, missing
   `build_date`, or equivalent non-release provenance is a `fail` for GA.
2. `tool_surface` — exactly the six business-workbench facade tools are
   visible to MCP clients.
3. `routed_operations` — `question.answer`, `theme_intelligence_report`,
   `evidence.quotes.search`, `evidence.highlights.list`, and
   `evidence.call_drilldown` are advertised by `gong_discover_capabilities`.
4. `data_readiness` — transcript coverage, profile readiness checklist
   (lifecycle, methodology, loss-reason mapping), call-facts attribution
   signals, AI highlight inventory, and scorecard inventory are populated.
5. `governance_redaction` — raw call IDs are hidden, `account_query` without
   `include_account_names=true` fails closed, `account_query` with the opt-in
   succeeds, and source-minus-redacted audit evidence is recorded when supplied
   through `REDACTION_AUDIT_JSON` or the compact `REDACTION_AUDIT_*` fields.
6. `evidence_workflow` — `question.answer` returns an evidence pack and one
   returned `call_ref` flows into `evidence.call_drilldown` to retrieve
   bounded transcript snippets plus Gong AI source paths.
7. `read_only_posture` — when `READER_DB_URL` is supplied, the scoped MCP
   role cannot write and cannot read raw source-only tables.

Status meaning:

- **pass** — all contracts satisfied; the deployment is ready for the first
  business-user session.
- **degraded** — the deployment is usable but missing optional probes or
  partial readiness (no AI highlights yet, no `READER_DB_URL` supplied,
  source-minus-redacted audit not yet generated). Record the degraded reasons
  in the closeout. Operator should still review before connecting business
  users.
- **fail** — the deployment is not ready; rerun after the failing check is
  remediated.

The smoke binary exits 0 on `pass` and `degraded` (the JSON status field
distinguishes them) and exits 1 on `fail`. Retain the JSON report and
Markdown summary in the pilot record. The smoke never writes secrets,
database URLs, or raw transcripts to artifacts.

## 8. Connect The First Business User

- Start with `business-workbench` unless the sponsor approved trained analyst
  workflows.
- Run `gong_status` first and confirm the `mcp_server` identity.
- Confirm transcript/profile/readiness caveats are visible.
- Keep first prompts inside the approved question set.
- Escalate stale cache, missing tools, unexpected raw identifiers, or
  suppression warnings the user does not understand back to the operator.

## 9. Record Closeout

For the pilot record, save:

- binary version, commit, image tag, and digest
- `mcp_server` identity from `gong_status` or `/healthz`, including
  `deployment_id`, `tool_preset`, `started_at_utc`, and transcript evidence
  provenance
- enabled preset and role name, not passwords or URLs
- sync scope and date window
- smoke command names and reviewed evidence paths, including the GA
  acceptance smoke JSON report and Markdown summary plus its overall status
  and any degraded reasons
- rollback artifact location
- open limitations and next review date

Do not save raw transcripts, database URLs, passwords, customer identifiers, or
whole temp directories in the client packet.
