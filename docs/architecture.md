# Architecture

## Goal

`gongctl` is a conservative, local-first Gong API wrapper and evidence
workbench. It should make Gong sync, cache inspection, and bounded MCP analysis
repeatable without becoming a hosted data product or a raw data warehouse.

For a faster source-first onboarding path, start with
[Developer orientation](developer-orientation.md).

## Boundaries

- `cmd/gongctl`: CLI parsing and user-facing behavior.
- `internal/gong`: Gong HTTP/API client. Typed live commands and sync services call through this package.
- `internal/auth`: credential discovery and validation.
- `internal/ratelimit`: client-side pacing for documented Gong API limits.
- `internal/checkpoint`: resumable local batch state.
- `internal/export`: local file writers.
- `internal/redact`: helpers for safe diagnostics/logging.
- `internal/profile`: tenant profile parsing, canonicalization, discovery, validation, and closed rule evaluation.
- `internal/store/sqlite`: local cache for calls, users, transcripts, CRM schema/settings inventory, search indexes, and sync state.
- `internal/store/postgres`: first shared-deployment vertical slice for sync
  status, calls, users, transcripts, transcript segments, metadata-only support
  diagnostics, cache inventory, retention purge planning/cleanup, and read-only
  MCP `business-pilot`, approved `analyst`, and core operator search/detail
  tools. It is not full query parity with SQLite yet: database-enforced
  governance snapshots/RLS, backup/restore, and `all-readonly` remain
  follow-ups.
  [Postgres parity matrix](postgres-parity.md) tracks the full parity contract.
- `internal/syncsvc`: call/user/inventory sync orchestration on top of the Gong client plus the configured cache store.
- `internal/transcripts`: transcript sync/search helpers on top of the store interface plus the Gong client.
- `internal/mcp`: read-only MCP adapter over the store interface. `catalog.go` owns
  built-in presets and governance-compatible tool lists; `server.go` owns MCP
  request handling and tool execution.

## CLI Surface

Current public SQLite-backed commands:

- `gongctl sync calls --db PATH --from DATE --to DATE --preset business|minimal|all [--max-pages N]`
- `gongctl sync users --db PATH [--max-pages N]`
- `gongctl sync transcripts --db PATH --out-dir PATH [--limit N] [--batch-size N]`
- `gongctl sync crm-integrations --db PATH`
- `gongctl sync crm-schema --db PATH --integration-id ID --object-type TYPE`
- `gongctl sync settings --db PATH --kind trackers|scorecards|workspaces [--workspace-id ID]`
- `gongctl sync scorecard-activity --db PATH --call-from DATE --call-to DATE [--review-method AUTOMATIC|MANUAL|BOTH]`
- `gongctl sync status --db PATH` for SQLite, or omit `--db` and set
  `GONG_DATABASE_URL` / `DATABASE_URL` for the Postgres vertical slice
- `gongctl profile discover --db PATH --out PATH`
- `gongctl profile validate --db PATH --profile PATH`
- `gongctl profile import --db PATH --profile PATH`
- `gongctl profile show --db PATH [--format json|yaml]`
- `gongctl analyze calls --db PATH --group-by DIMENSION [--lifecycle-source auto|profile|builtin] [--limit N]`
- `gongctl analyze coverage --db PATH [--lifecycle-source auto|profile|builtin]`
- `gongctl analyze crm-schema --db PATH [--integration-id ID] [--object-type TYPE]`
- `gongctl analyze settings --db PATH [--kind KIND]`
- `gongctl analyze scorecards --db PATH [--active-only]`
- `gongctl analyze scorecard --db PATH --scorecard-id ID`
- `gongctl analyze scorecard-activity --db PATH [--group-by scorecard|review_method|reviewed_user|lifecycle|transcript_status]`
  (Postgres read-only rejects `reviewed_user` grouping to avoid returning user
  IDs.)
- `gongctl analyze transcript-backlog --db PATH [--lifecycle-source auto|profile|builtin] [--lifecycle BUCKET] [--limit N]`
- `gongctl mcp tools`
- `gongctl mcp tool-info NAME`
- `gongctl search transcripts --db PATH --query TEXT [--limit N]`
- `gongctl search calls --db PATH [--crm-object-type TYPE] [--crm-object-id ID] [--limit N]`
- `gongctl calls show --db PATH --call-id ID --json`
- `gongctl calls show --call-id ID --json` with `GONG_DATABASE_URL` for
  minimized Postgres call detail

Behavioral rules:

- `--db` is required for every SQLite-backed command.
- Sync commands print concise summaries to `stderr`; status/search/show emit JSON to `stdout`.
- `business` and `all` currently both request Gong `Extended` context; `minimal` requests no context.
- Transcript sync defaults to `--limit 100` and `--batch-size 100`, selects
  calls missing transcripts, writes transcript JSON files, and stores normalized
  transcript segments in SQLite.
- `sync run --config` resolves relative paths from the YAML file location and
  can stage calls, users, transcripts, CRM integrations/schema, settings, and
  scorecard activity; sensitive steps still require runtime authorization. The
  YAML file cannot self-approve transcript download, business/all call sync,
  party capture, or highlight capture. This runner currently opens SQLite via
  the configured `db` path; it is not the Postgres sync runner.
- CRM-context call search only works for rows that were synced with stored context.
- Business profiles are YAML source imported into SQLite runtime state. The rule grammar is closed and evaluated in Go; profiles cannot inject SQL or expressions.
- Profile import is transactional and idempotent by canonical hash. Identical re-imports are no-ops; source-only changes update source metadata without changing profile meaning. MCP reads only the imported SQLite state.

## MCP Boundary

The CLI remains the first integration contract because it is easy to inspect, script, and run in customer-controlled environments. MCP reads from the configured cache store instead of calling Gong directly; SQLite is the complete/default store, and Postgres currently supports the narrower shared-deployment slice.

Runtime details that matter when debugging MCP:

- stdio serves the full read-only catalog if no preset or allowlist is set
- HTTP mode requires an explicit tool preset or allowlist
- built-in presets are defined in `internal/mcp/catalog.go`, while
  `cmd/gongmcp/main.go` resolves flag/env precedence
- HTTP mode defaults to bearer auth; no-auth is localhost-development only
- non-local HTTP binds require explicit open-network approval and Origin
  allowlisting
- `/healthz` is for infrastructure checks; `/mcp` is the MCP endpoint
- MCP argument decoding rejects unknown fields except reserved `_meta`
- request/response frames are capped at 1 MiB
- row limits come from `internal/mcp.LimitPolicy`; `tools/list` reflects the
  running server's env/flag configuration, while `gongctl mcp tool-info` is an
  offline catalog view that can only see defaults plus `GONGMCP_MAX_*` env vars
- high-volume row tools may return a cap-feedback envelope only when they hit
  the effective limit, so consumers should handle both the legacy result shape
  and the `capped: true` envelope documented in `mcp-data-exposure.md`

Implemented MCP tools:

- `get_sync_status`
- `search_calls`
- `get_call`
- `list_crm_object_types`
- `list_crm_fields`
- `list_crm_integrations`
- `list_cached_crm_schema_objects`
- `list_cached_crm_schema_fields`
- `list_gong_settings`
- `list_scorecards`
- `get_scorecard`
- `summarize_scorecard_activity`
- `get_business_profile`
- `list_business_concepts`
- `list_unmapped_crm_fields`
- `search_crm_field_values`
- `analyze_late_stage_crm_signals`
- `opportunities_missing_transcripts`
- `search_transcripts_by_crm_context`
- `opportunity_call_summary`
- `crm_field_population_matrix`
- `list_lifecycle_buckets`
- `summarize_calls_by_lifecycle`
- `search_calls_by_lifecycle`
- `prioritize_transcripts_by_lifecycle`
- `compare_lifecycle_crm_fields`
- `summarize_call_facts`
- `rank_transcript_backlog`
- `search_transcript_segments`
- `search_transcripts_by_call_facts`
- `search_transcript_quotes_with_attribution`
- `missing_transcripts`

Do not expose raw Gong API passthrough, arbitrary SQL, raw cached call JSON, profile import, or raw transcript dumps. Transcript search returns segment metadata and snippets only, and redacts call/speaker IDs by default unless an operator explicitly opts in. CRM-context transcript search adds object-type/object-ID filtering but still returns only bounded snippets plus timing/context metadata through MCP. `get_call` returns minimized metadata plus CRM object field names/counts, not field values or participant payloads. CRM population matrices only group by allowlisted categorical fields such as `StageName`.

CRM schema/settings tools expose cached metadata such as integration IDs, CRM object/field names, tracker names, scorecard names, scorecard questions, and workspaces. They do not return raw settings payloads. Scorecard inventory lives in `gong_settings(kind='scorecards')`; answered scorecard fill-ins live separately in `scorecard_activity` so inventory and activity remain distinct. `summarize_scorecard_activity` is aggregate-only by default and does not return call IDs, scorecard IDs, user IDs, answer text, call titles, transcript snippets, emails, or raw payloads. `search_crm_field_values` is a deliberate narrow exception for explicit user-directed value lookup: it requires object type, field name, and value query, redacts call IDs by default unless `include_call_ids=true`, and returns bounded short value snippets plus call titles only when `include_value_snippets=true` is explicitly set. `analyze_late_stage_crm_signals` returns aggregate stage counts and field-lift rates only, not raw arbitrary CRM values or record identifiers; the current Postgres implementation supports only `Opportunity.StageName`. Postgres `search_transcripts_by_crm_context` returns CRM-constrained transcript snippets through an explicit allowlist and omits call IDs, speaker IDs, CRM object IDs/names, object keys, raw CRM values, raw JSON, raw hashes, call titles, and full transcript text from the reader function shape. Postgres `crm_field_population_matrix` returns aggregate population cells grouped only by approved categorical object/field pairs and omits object IDs/names, object keys, call IDs, non-group raw values, raw JSON, and raw hashes. Postgres `compare_lifecycle_crm_fields` returns aggregate lifecycle-bucket field population differences for the reviewed `Opportunity` object type, excludes governance-suppressed calls in SQL, and omits call IDs, titles, CRM object identifiers/names/keys, raw CRM values, raw JSON, raw hashes, and transcript text. Postgres `missing_transcripts` is an admin/operator record-reference surface for transcript backfill: it supports date, lifecycle, scope, system, direction, and CRM object filters, and returns call IDs, titles, and start times without raw cached JSON, transcript text, or CRM field values. The Postgres Opportunity aggregate tools group by Opportunity internally but return only redacted coverage and call-summary metadata, not Opportunity IDs/names, owner IDs, amount, close date, latest call IDs, or raw CRM values. Use `gongctl mcp tools` and `gongctl mcp tool-info NAME` to inspect the MCP catalog outside Claude/Codex host apps.

Lifecycle read surfaces support `lifecycle_source=auto|profile|builtin`. `auto` uses an active imported profile when one exists and falls back to the frozen builtin compatibility view. Profile-aware responses include profile provenance and unavailable concepts. The builtin view intentionally remains deterministic so business users can separate sales funnel, renewal, upsell/expansion, customer success, partner, and outbound prospecting work without needing full transcripts first. Lifecycle CRM comparison remains object-scoped by requiring an explicit CRM `object_type`.

Ad-hoc business rollups read metadata-only facts. The builtin path uses the SQLite `call_facts` view. The profile path materializes profile object aliases, field concepts, and lifecycle rules into a SQLite cache keyed by active profile and canonical hash. Writable CLI sync/profile commands rebuild or warm that cache; read-only MCP refuses stale-cache rebuilds instead of mutating SQLite. MCP only allows safe grouping dimensions for `summarize_call_facts`; directed CRM value lookups go through `search_crm_field_values` with explicit opt-in, while late-stage CRM signal analysis stays aggregate-only. Query APIs only expose allowlisted groupings and filters rather than raw SQL.

Unmapped CRM field surfaces are redacted by default. They return field names, types, cardinality, population/null rates, and length distribution, not raw example values.

Local SQLite state remains the complete proving ground and source of truth for
MCP query tools. The Postgres backend now covers bounded reviewed slices for the
shared store path, `business-pilot`, `analyst-core`, `analyst-business-core`,
governance-search, `analyst`, backup/restore and retention smoke, and explicit
operator/admin allowlists such as CRM field diagnostics, lifecycle CRM
comparison, CRM-context transcript snippets, and transcript-backfill call
references. `all-readonly`, broad catalog parity, database-enforced
governance/RLS, and customer-scale hardening remain follow-up work.

Postgres materializes builtin call/context facts because it cannot lean on
SQLite views at read time. The writable CLI owns read-model refresh and repair;
`gongctl sync read-model` checks state, `gongctl sync read-model --rebuild`
repairs stale builtin facts, and read-only MCP startup rejects stale/missing
facts instead of mutating the database.

Postgres maps SQLite FTS5 transcript search to PostgreSQL full-text search with
a `tsvector`/GIN index and `ts_headline`. Exact FTS5 ranking/snippet parity is
not guaranteed in this first slice.

## Endpoint Drift Strategy

Gong endpoint details may be gated behind authenticated customer docs. Keep `gongctl api raw` as the escape hatch and keep typed wrappers small so request payload fixes are localized.
