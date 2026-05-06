# Deterministic AI Exclusion Filtering

`gongctl` can apply a private YAML file to produce an MCP-facing SQLite copy
that excludes calls linked to configured customer names, aliases, domains, or
email fragments before results reach an MCP host or LLM.

This is a deterministic name-based exclusion filter. It is not legal consent
management, contractual AI-restriction management, DPA enforcement, or a
substitute for customer approval workflows. Operators remain responsible for
the restricted-customer list, aliases, account hierarchy, domains, and review of
the local audit before MCP/AI use.

The public repo must not contain a company's real restricted-customer list. Keep
the real file in a private mounted path, secret-managed config volume, or
operator-controlled data directory.

## Config Shape

Use exact customer names and explicit aliases:

```yaml
version: 1
lists:
  no_ai:
    description: Customers requiring per-use consent before AI processing.
    customers:
      - name: "Example Restricted Corp"
        aliases:
          - "Example Restricted Corporation"
  notification_required:
    description: Customers excluded until third-party notification is complete.
    customers:
      - name: "Example Notice Customer"
        aliases:
          - "Example Notice Co"
```

Matching is deterministic and local:

- lowercase
- trim whitespace
- collapse punctuation and whitespace
- normalized token-phrase matching

There is no fuzzy matching and no LLM decision in the enforcement path. If an
audit shows a near variant that should be excluded, add it as an explicit alias.
Short names are matched on token boundaries, so `XYZ` matches `XYZ Process
Automation` but does not match `Xylophone Yard Zone`. Add domains such as
`example.com` as aliases when email-domain matches should also exclude a call.

## Audit Before MCP Use

Run the audit locally before enabling the MCP server:

```bash
gongctl governance audit \
  --db /srv/gongctl/gong.db \
  --config /srv/gongctl/private/ai-governance.yaml
```

The audit is operator-facing and may show configured customer names. Use it to
verify configured entry and alias counts, matched entries, unmatched entries
that may need aliases, and suppressed call count. Do not write audit output to
shared CI logs or public support tickets.

Use JSON for automation:

```bash
gongctl governance audit \
  --db /srv/gongctl/gong.db \
  --config /srv/gongctl/private/ai-governance.yaml \
  --json
```

For Postgres shared deployments, run the audit with the writable operator URL
and persist the policy for the read-only MCP container:

```bash
export GONG_DATABASE_URL="$GONGCTL_WRITER_DATABASE_URL"
gongctl governance audit \
  --config /srv/gongctl/private/ai-governance.yaml \
  --apply-postgres-policy \
  --json
```

The persisted Postgres policy stores aggregate audit counts, a config
fingerprint, a data fingerprint, and suppressed call IDs. It does not grant the
`gongmcp_reader` role direct access to raw candidate values or governance policy
tables.

## Ingest-Time Exclusion

For customer deployments where restricted customer data must not enter the
MCP/AI cache, pass the private governance config during sync:

```bash
gongctl sync calls \
  --from 2026-02-01 \
  --to 2026-05-01 \
  --preset business \
  --allow-sensitive-export \
  --governance-config /srv/gongctl/private/ai-governance.yaml
```

When the current call-list payload contains a configured restricted customer
name or alias, the call is skipped before it is written to the cache. If the
call was already cached before governance was enabled, governed sync removes the
call-scoped cache rows it owns before recording the skip. The skip ledger stores
minimized operator metadata: call ID, config fingerprint, matched list name,
source category, run ID, and timestamp. Treat that ledger as customer
operational metadata and exclude it from public support artifacts and shared
logs by default. It does not store the restricted customer name, alias, raw
matched value, transcript text, CRM field value, or raw call payload.

Then pass the same config to transcript sync. Transcript sync loads the
governance YAML, re-evaluates cached call payloads selected as transcript
candidates, records newly restricted candidates in the skip ledger, removes
their call-scoped cache rows, and excludes them before making Gong transcript
requests:

```bash
gongctl sync transcripts \
  --out-dir /srv/gongctl/transcripts \
  --allow-sensitive-export \
  --governance-config /srv/gongctl/private/ai-governance.yaml
```

This is a first ingest-time guard. It matches only data visible in the cached
call-list payload. Keep runtime governance and, for SQLite, filtered MCP DB
exports as defense in depth. If a later sync adds aliases or richer metadata,
rerun the governed sync or audit/export flow before MCP use.

## Filtered MCP Database

For MCP/LLM use, the preferred deployment is a physically filtered SQLite copy:

```bash
gongctl governance export-filtered-db \
  --db /srv/gongctl/gong.db \
  --config /srv/gongctl/private/ai-governance.yaml \
  --out /srv/gongctl/gong-mcp-governed.db
```

The source DB remains the complete operator cache. The filtered export scans
call titles, raw call payloads including participant emails, embedded CRM object
names, all cached CRM field values, and transcript segment text. It removes
matched call-dependent rows from `calls`, transcripts, transcript FTS, embedded
CRM context, and profile call-fact cache tables, then compacts the copy. Point
MCP hosts at the filtered DB by default whenever a blocklist exists:

```bash
gongmcp --db /srv/gongctl/gong-mcp-governed.db
```

This recovers more MCP tool capability because the server is not reading the raw
restricted call rows. Recreate the filtered DB after every sync or governance
config change. Keep the filtered DB outside Git and treat it as customer data.

The filtered export is call-record filtering. It does not delete unrelated
global configuration rows such as scorecards, trackers, workspaces, CRM schema
metadata, or internal user rows unless they are tied to a suppressed call. If
the policy is "no occurrence anywhere in any configuration metadata," add a
separate settings/schema/profile metadata scan before MCP use.

## Redacted Postgres Serving Database (Phase 13e4 vertical slice)

For Postgres deployments where the strongest requirement is that the MCP/LLM
path cannot see blocklisted companies, the recommended layout is one Postgres
server with two databases: `gongctl_source` for the full operator cache, and
`gongctl_mcp` for a physically redacted MCP serving cache. `gongctl` sync/write
jobs connect only to `gongctl_source`. A governance refresh command rebuilds
`gongctl_mcp` from `gongctl_source`, copying only non-restricted calls and the
allowed support tables. `gongmcp` connects only to `gongctl_mcp` with a
read-only role and never receives credentials for the source database.

```bash
gongctl governance refresh-serving-db \
  --source "$GONGCTL_SOURCE_DATABASE_URL" \
  --target "$GONGCTL_MCP_DATABASE_URL" \
  --config /run/secrets/ai-governance.yaml
```

The first slice rebuilds the target database in place: it determines suppressed
call IDs from the source via the existing governance audit logic, truncates the
target call-scoped tables, copies allowed `calls`, `users`, `transcripts`,
`transcript_segments`, `call_context_objects`, and `call_context_fields` rows,
rebuilds the Postgres read model on the target, and re-applies the governance
policy on the target. Output is sanitized JSON with row counts, removed-call
counts, and policy/data fingerprints; it does not include database URLs,
customer names, blocklist values, call IDs, or call titles.

The first slice intentionally skips several global metadata tables on the
target. The skipped list is included in the sanitized refresh output so a
reviewer can confirm the boundary; it currently covers `sync_runs`,
`sync_state`, `gong_settings`, the `crm_*` schema metadata tables, profile
tables, `scorecard_activity`, `governance_ingest_skipped_calls`, and
`purged_call_ids`. Broader copy and a documented blue/green
`gongctl_mcp_next` option for near-zero-downtime refresh in larger deployments
remain follow-ups.

### Phase 13h scoped analyst reader on the redacted serving DB

Once the redacted serving DB exists, the recommended customer-facing posture
is a preset-scoped analyst reader against `gongctl_mcp` with `gongmcp` running
under `GONGMCP_TOOL_PRESET=analyst-expansion` and
`GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS=1`. The scoped analyst reader role is
created on the Postgres server and granted only the column SELECTs and
function EXECUTEs that the analyst preset needs; raw call payloads, sync
state, and profile metadata stay denied at the database layer.

```bash
# 1. Refresh the redacted MCP serving DB from the operator cache.
gongctl governance refresh-serving-db \
  --source "$GONGCTL_SOURCE_DATABASE_URL" \
  --target "$GONGCTL_MCP_DATABASE_URL" \
  --config /run/secrets/ai-governance.yaml

# 2. Apply the analyst-expansion scoped reader on the serving DB.
GONG_DATABASE_URL="$GONGCTL_MCP_DATABASE_URL" \
gongctl mcp postgres-reader-apply \
  --preset analyst-expansion \
  --role gongmcp_analyst_reader \
  --database gongctl_mcp \
  --apply

# 3. Run gongmcp against the scoped analyst reader URL on the serving DB.
GONG_DATABASE_URL="$GONGMCP_ANALYST_READER_URL" \
GONGMCP_TOOL_PRESET=analyst-expansion \
GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS=1 \
  gongmcp
```

The combination of `analyst-expansion` plus `GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS=1`
on Postgres mode activates a small-cell suppression minimum cohort size of 3
on business-analysis dimension tools, so analyst dimension responses include
`small_cell_suppression_min_3` (limitation) and `small_cell_suppression_applied`
(warning) when any bucket falls below the threshold.

A focused local smoke that exercises this end to end, including direct-SQL
denial of raw call payloads and operator tables under the scoped role, lives
at `scripts/postgres-serving-db-analyst-smoke.sh`. It uses the existing
`docker-compose.postgres.yml` to start a disposable Postgres container, seeds
synthetic source data including a clearly-synthetic restricted customer
("Blocked Synthetic Corp"), and asserts that the restricted call rows are
absent from the redacted serving DB and from MCP outputs. The smoke writes
sanitized artifacts under a temporary directory with no DB URLs, secrets, or
restricted-customer values.

### Phase 13b broad analyst / company-search behavior on the serving DB

The same scoped analyst reader on `gongctl_mcp` is also expected to support
broad analyst flows: bounded company/title/transcript search,
lifecycle/CRM aggregates, theme dimensions, quote packs, and attribution
diagnostics. The Phase 13b acceptance is that every analyst answer is built
only from the redacted serving DB:

- Non-restricted company/title/transcript searches succeed against
  `gongctl_mcp` through the analyst-expansion preset.
- Restricted-company probes (account_query or theme_query targeting the
  blocklisted name) return zero results without leaking any restricted
  customer value, call ID, account/opportunity ID, or transcript text into
  retained evidence.
- `summarize_calls_by_lifecycle`, `crm_field_population_matrix`,
  `search_transcripts_by_crm_context`, `build_quote_pack`,
  `diagnose_attribution_coverage`, and `explain_analysis_limitations` all
  operate over the serving DB and never see restricted rows because the
  serving DB itself does not contain them.
- The `all-readonly` preset stays rejected under Postgres mode even when a
  scoped analyst reader role is configured.

The Phase 13h smoke script (`scripts/postgres-serving-db-analyst-smoke.sh`)
covers Phase 13b in the same run: after the scoped analyst session, it
issues a broad analyst session with
`GONGMCP_POSTGRES_REDACTED_SERVING_DB=1`, allowed-company and
restricted-company search probes, asserts the restricted probes return zero
results, redacts analyst-supplied restricted query strings before retaining
the JSON-RPC transcript, asserts artifacts contain no restricted values,
IDs/transcripts/URLs, asserts the lifecycle/CRM/quote/diagnostic tools
succeed against the serving DB, and confirms that
`GONGMCP_TOOL_PRESET=all-readonly` is rejected. Only set
`GONGMCP_POSTGRES_REDACTED_SERVING_DB=1` when `GONG_DATABASE_URL` points at a
serving DB generated by `governance refresh-serving-db`; raw/source DB
Postgres readers keep `account_query` disabled because it can probe customer
names. The artifact directory adds `broad-analyst-mcp.jsonl`,
`all-readonly-rejected.txt`, `target-restricted-text-counts.txt`, and
`broad-analyst-summary.json` alongside the Phase 13h artifacts.

## Raw-DB MCP Enforcement

Raw-DB governance is a fallback when a filtered DB has not been generated. Start
`gongmcp` with the same private config:

AI governance mode requires an explicit governance-compatible `--tool-preset`,
`GONGMCP_TOOL_PRESET`, `--tool-allowlist`, or `GONGMCP_TOOL_ALLOWLIST`. Use
`GONGMCP_TOOL_PRESET=governance-search` for the built-in raw-DB fallback.
Unsupported aggregate/config tools are refused while the filter is active. The
broader `business-pilot`, `analyst`, and `all-readonly` presets are not
governance-compatible on a raw DB because they include aggregate/config/status
tools that cannot prove suppressed calls were removed from their counts.
Directed CRM value lookup through
`search_crm_field_values` is also refused in governance mode because it can
answer whether a restricted customer name or legal-name variant is present in
cached CRM fields.

```bash
GONGMCP_AI_GOVERNANCE_CONFIG=/srv/gongctl/private/ai-governance.yaml \
GONGMCP_TOOL_PRESET=governance-search \
  gongmcp --db /srv/gongctl/gong.db
```

HTTP private pilots use the same config variable:

```bash
GONGMCP_AI_GOVERNANCE_CONFIG=/srv/gongctl/private/ai-governance.yaml \
GONGMCP_BEARER_TOKEN_FILE=/run/secrets/gongmcp_token \
GONGMCP_TOOL_PRESET=governance-search \
  gongmcp --http 127.0.0.1:8080 --auth-mode bearer --db /srv/gongctl/gong.db
```

For Postgres, omit `--db` and use the read-only database URL after the policy has
been prepared:

```bash
GONG_DATABASE_URL="$GONGMCP_READER_DATABASE_URL" \
GONGMCP_AI_GOVERNANCE_CONFIG=/srv/gongctl/private/ai-governance.yaml \
GONGMCP_TOOL_PRESET=governance-search \
  gongmcp
```

The Postgres `governance-search` preset is narrowed to the supported search
slice. Broader database-enforced RLS/materialized governed snapshots remain a
follow-up before analyst/all-readonly Postgres governance.

Treat the Postgres reader URL as a `gongmcp` service secret, not a general
analyst SQL credential. The current Postgres slice enforces governance at the
MCP layer over a prepared policy; direct SQL use of the reader role can still
query minimized readable tables until governed views/RLS/materialized snapshots
land.

MCP responses do not include configured restricted customer names or filtered
match counts. Aggregate tools that are not yet recomputed over the filtered call
set fail closed instead of returning counts that include excluded customers.

Restart `gongmcp` after cache refreshes or config changes. This is mandatory:
SQLite `gongmcp` fingerprints the config and cache at startup. Postgres
`gongmcp` validates the prepared policy, config fingerprint, and current data
fingerprint at startup and on each tool call. Both modes fail closed if the
governance state changes while the process is running.

By default, `gongmcp` refuses governance configs with unmatched entries. Use
`--allow-unmatched-ai-governance` only after the local audit confirms the
unmatched entries are expected for the current cache.

## Current Boundary

Filtered export physically removes matched call-dependent rows from the MCP copy
only; it does not delete data from the source operator SQLite cache. Ingest-time
exclusion is the deployment path for environments that cannot store restricted
customer data in the source cache. Raw-DB governance remains query/output-time
suppression and does not prevent local operators from running raw CLI cache
inspection commands. Postgres governance in this slice is a prepared-policy MCP
search boundary, not database-enforced RLS.
