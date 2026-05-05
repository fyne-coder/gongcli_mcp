# Postgres Parity Matrix

This matrix is the working contract for making Postgres a full peer to the
current SQLite backend. SQLite remains the complete/default implementation.
Postgres parity should be added deliberately, with each surface classified as
`must match`, `postgres-native equivalent`, or `sqlite-only by design`.

## Status Legend

- `complete`: implemented and covered by tests/smoke.
- `foundation`: schema or partial implementation exists, later query surfaces
  still need work.
- `queued`: accepted follow-up, not implemented yet.
- `sqlite-only`: intentionally remains SQLite-only unless the product decision
  changes.

## Matrix

| Area | SQLite Source Of Truth | Current Postgres Status | Target Postgres Behavior | Classification | Owner Phase | Verification |
| --- | --- | --- | --- | --- | --- | --- |
| Backend selection | `--db PATH` opens SQLite | `GONG_DATABASE_URL` / `DATABASE_URL` opens Postgres when `--db` is omitted | Preserve both contracts exactly | must match | Phase 0 | `go test -count=1 ./internal/cli ./cmd/gongmcp` |
| Schema versioning | `PRAGMA user_version` migrations | `gongctl_schema_migrations` | Postgres-native migration table with read-only startup validation | postgres-native equivalent | Phase 0 | `GONGCTL_TEST_POSTGRES_URL=... go test -count=1 ./internal/store/postgres` |
| Core sync tables | `sync_runs`, `sync_state`, `calls`, `users`, `transcripts`, `transcript_segments` | complete for first slice | Same record counts and durable sync state for shared deployments | must match | Phase 1 | `./scripts/postgres-smoke.sh` |
| CRM context objects | `call_context_objects` | foundation table added | Populated on call writes, queryable by object type/id/call | must match | Phase 1 | `TestPostgresUpsertRefreshesNormalizedReadModel` |
| CRM context fields | `call_context_fields` | foundation table added | Populated on call writes, queryable by field name/value/call | must match | Phase 1 | `TestPostgresUpsertRefreshesNormalizedReadModel` |
| Context normalization semantics | Go extraction in SQLite write path | complete in Phase 2 via shared-compatible Go extractor | Match object key/name fallback, field fallback, and value stringification at normalized-row layer | must match | Phase 2 | `TestPostgresNormalizedRowsMatchSQLiteForRepresentativeContextShapes` |
| Context extraction limits/diagnostics | SQLite local cache has no Postgres write-amplification risk | complete in Phase 2 with capped Postgres extraction diagnostics | Bound per-call object/field expansion and expose counts/cap hits without raw CRM values | postgres-native equivalent | Phase 2 | `TestPostgresReadModelExtractionCapsRecordDiagnostics`; `scripts/postgres-smoke.sh` |
| Builtin lifecycle facts | `call_lifecycle` view | materialized into `call_facts` | Deterministic builtin lifecycle columns from normalized context | must match at output layer | Phase 1 | SQLite-vs-Postgres lifecycle aggregate tests |
| Call facts | `call_facts` view | foundation table added | Maintained indexed facts table for grouping/filtering | must match at output layer | Phase 1 | SQLite-vs-Postgres call-fact aggregate tests |
| Derived read-model lifecycle | SQLite views rebuild from base tables at read time | complete in Phase 2c for builtin facts: migration/write refresh plus trigger-maintained readiness counters | Backfill on upgrade, refresh on writes, cheap version/stale detection before profile/governance phases | postgres-native equivalent | Phase 2 | `TestPostgresReadModelStateDetectsDeletedFactRowsAsStaleAndRebuildRepairs`; `TestPostgresReadModelReadinessRejectsRebuildInProgressState`; `gongctl sync read-model` |
| Operator read-model check/rebuild | SQLite profile cache readiness/rebuild is profile-aware | complete for Postgres builtin facts via `gongctl sync read-model [--rebuild]` | Writable CLI can check/rebuild; read-only MCP never rebuilds | postgres-native equivalent | Phase 2 | `GONG_DATABASE_URL=... gongctl sync read-model --rebuild` |
| Read-model load/performance smoke | SQLite local cache has no shared write contention | complete for deterministic 750-call synthetic smoke; broader customer-scale benchmark queued before GA | Capture rebuild timing, representative EXPLAIN plans, read-only MCP success, reader write/raw-read denial, and stale startup denial | postgres-native equivalent | Phase 2 | `./scripts/postgres-load-smoke.sh` |
| Transcript FTS | SQLite FTS5 | Postgres `tsvector`/GIN with execute-only snippet function | Equivalent bounded search semantics, documented ranking differences | postgres-native equivalent | Phase 2 | synthetic search comparison tests |
| Call search | SQLite filters over calls/context | complete for normalized CRM object and builtin call-fact filters | Match safe filters or document intentional exclusions | must match | Phase 2 | `TestPostgresSearchCallsRawSafeFiltersMatchSQLite`; `scripts/postgres-smoke.sh` |
| MCP `get_call` | SQLite minimized call detail | complete for Postgres normalized context rows | Same minimized JSON contract over Postgres MCP | must match | Phase 2 | `TestPostgresGetCallDetailMatchesSQLiteForNormalizedContext`; `scripts/postgres-smoke.sh` |
| CLI `calls show` | SQLite raw cached JSON behind sensitive-export controls | complete for Postgres minimized call detail | Postgres uses `GetCallDetail` and does not return raw cached JSON, CRM field values, transcript text, or participant payloads | intentionally minimized for shared Postgres | Phase 2 | CLI call-detail smoke/tests |
| `business-pilot` MCP preset | SQLite full preset | complete/foundation over Postgres facts | Keep supported tools stable and read-only | must match | Phase 1 | `scripts/postgres-smoke.sh` |
| `operator-smoke` MCP preset | SQLite health/search smoke | complete for Postgres core validation | Includes `get_sync_status`, `search_calls`, `search_transcript_segments`, `get_call`, and `rank_transcript_backlog` | must match | Phase 2 | MCP tools/list and tools/call smoke |
| `governance-search` MCP preset | SQLite governed search | complete for narrowed Postgres search slice | Postgres loads a prepared governance policy through the read-only role and narrows the preset to supported search tools | postgres-native equivalent | Phase 4 | governed synthetic smoke |
| `analyst-core` MCP preset | Postgres-specific starter surface | complete for core/profile/lifecycle/CRM-context inventory, cached CRM schema/settings inventory, scorecard inventory, and aggregate scorecard activity tools | Exposes only implemented Postgres analyst starter tools and keeps raw CRM values/raw settings payloads/raw scorecard activity payloads out of reader output | intentionally narrower than SQLite `analyst` | Phase 5a/5c/5d/5e | analyst-core tools/list, CRM inventory, cached schema/settings inventory, scorecard inventory, and scorecard activity smoke |
| `analyst-business-core` MCP preset | Postgres-specific business-analysis starter surface | complete for bounded cohort/transcript-evidence/dimension tools | Exposes implemented Phase 5b tools through security-definer transcript/evidence functions while keeping direct raw transcript/context grants denied and redacting raw account/opportunity names, websites, close dates, and probabilities at the SQL boundary | intentionally narrower than SQLite `analyst` | Phase 5b | analyst-business-core MCP smoke |
| `analyst` MCP preset | SQLite analyst/cohort tools | rejected in Postgres | Enable only after backing queries and governance are ready | must match before enablement | Phase 5 | preset parity tests |
| `all-readonly` MCP preset | SQLite full read-only catalog | rejected in Postgres | Enable only after full read-only query parity | must match before enablement | Phase 5 | catalog parity tests |
| Profile import/show | SQLite profile tables/cache | complete for metadata/import/show/readiness and profile fact-cache freshness | Postgres profile metadata, active-state storage, business-profile MCP reads, and sync-status readiness | must match for metadata; cache freshness complete for first profile slice | Phase 3/3b | `TestPostgresProfileImportShowAndReadinessMatchesSQLiteMetadata` |
| Profile lifecycle source | `lifecycle_source=auto|profile|builtin` with `profile_call_fact_cache` | complete for first business-pilot slice when active profile cache is fresh | `profile` requires an active fresh profile cache; `auto` falls back to builtin only when no profile exists and otherwise fails closed on missing/stale read-only cache | must match | Phase 3b | profile lifecycle cache parity tests; `scripts/postgres-smoke.sh` |
| Business-analysis calls/evidence | SQLite `business_analysis.go` helpers | complete for bounded Postgres starter slice | Equivalent read APIs over normalized context/facts/transcripts with Postgres-native FTS ranking/snippets | postgres-native equivalent | Phase 5b | business-analysis parity fixtures; analyst-business-core smoke |
| CRM schema/settings inventory | SQLite `crm_integrations`, schema, settings, scorecards | complete for bounded Postgres analyst-core slice: embedded CRM object/field aggregates, cached CRM integrations/schema fields, Gong settings, scorecards, and scorecard activity aggregates | Same cached metadata read surfaces without raw CRM values or raw settings payloads; broader settings/catalog query parity remains queued before full `analyst`/`all-readonly` | must match for implemented inventory surfaces | Phase 5a/5c/5d/5e | inventory/query tests; `scripts/postgres-smoke.sh` |
| Scorecard activity | SQLite `scorecard_activity` | complete for aggregate Postgres slice | Same aggregate/read-only scorecard activity surfaces except raw reviewed-user grouping is rejected for Postgres read-only deployments; raw activity payloads and raw hashes are denied to the reader role | must match for aggregate surfaces | Phase 5d | scorecard activity parity tests; `scripts/postgres-smoke.sh` |
| Governance filtered DB export | SQLite physical filtered copy plus `VACUUM INTO` | Postgres policy-backed MCP suppression implemented for narrowed search slice; physical filtered export remains SQLite-only | Governed views, row-level security, or materialized governed snapshots before broad analyst/all-readonly GA | postgres-native equivalent | Phase 4 | restricted synthetic account absent from governed MCP search outputs |
| Governance audit | SQLite local audit against private YAML | complete for Postgres candidate scan plus persisted policy preparation | Audit Postgres coverage with writable operator role; read-only MCP validates policy/config/data fingerprints without exposing restricted names over MCP | postgres-native equivalent | Phase 4 | `gongctl governance audit --apply-postgres-policy`; governed smoke |
| Support bundle | SQLite sanitized support bundle | complete for metadata-only Postgres diagnostics | Sanitized Postgres diagnostics without secrets/customer payloads or database URLs | postgres-native equivalent | Phase 6a | support bundle fixture inspection; `scripts/postgres-smoke.sh` |
| Cache inventory | SQLite file/table inventory | complete for Postgres table/version/readiness diagnostics | Postgres table counts, schema version, read-model/profile readiness, and reader-role diagnostics without database URL export | postgres-native equivalent | Phase 6a | `TestPostgresCacheInventoryAndDiagnostics`; `scripts/postgres-smoke.sh` |
| Purge/retention | SQLite purge commands | queued | Postgres retention diagnostics and dry-run/confirm semantics | postgres-native equivalent | Phase 6 | purge dry-run tests |
| Backup/restore | SQLite file copy guidance | queued | Postgres dump/restore, migration rollback, and role-grant guidance | postgres-native equivalent | Phase 7 | documented operator smoke |
| Release hardening | SQLite CI coverage plus release gates | queued for Postgres service tests | CI-backed Postgres service tests and versioned docs/images | must match release quality | Phase 7 | CI service test + release checklist |

## Phase Boundaries

1. **Phase 1 foundation**: normalized context/fact tables, maintained by writes,
   and business-pilot reads moved to indexed facts.
2. **Phase 2 read-model hardening and core query parity**: close normalized
   context/readiness risks first, then continue call/search/transcript/lifecycle
   output comparisons.
3. **Phase 3 profile metadata parity**: profile storage, import/show,
   active-state, MCP profile reads, and explicit profile lifecycle cache
   status.
4. **Phase 3b profile lifecycle cache parity**: profile-derived call-fact cache
   warming/freshness and `lifecycle_source=profile`.
5. **Phase 4 governance parity**: governed Postgres read surfaces and audit.
6. **Phase 5 analyst/all-readonly parity**: broader MCP and business-analysis
   surfaces.
7. **Phase 6 operations parity**: support bundle, cache inventory, purge, and
   diagnostics.
8. **Phase 7 release hardening**: CI service tests, backup/restore, migration
   rollback, docs, and versioned release artifacts.

## Phase 4 Risk Status

- Closed for first governed Postgres slice: writable `gongctl governance audit
  --apply-postgres-policy` scans Postgres candidates and persists the policy
  fingerprint, data fingerprint, and suppressed call IDs.
- Closed for read-only MCP startup: `gongmcp` with a Postgres reader role loads
  only the prepared policy, validates the same private config and current data
  fingerprint, and fails closed when policy/config/data are missing or stale.
- Closed for narrowed `governance-search`: Postgres maps the preset to supported
  search tools and suppresses configured calls from `search_calls`, `get_call`,
  `search_transcript_segments`, and `rank_transcript_backlog` outputs without
  exposing filtered counts over MCP.
- Closed after review: policy preparation now builds candidates, computes the
  data fingerprint, and persists suppressed call IDs inside one serializable
  transaction. Governed MCP search overfetches before post-query suppression so
  suppressed rows do not starve allowed rows at small limits.
- Accepted residual risk: `gongmcp_reader` remains a service secret. Direct SQL
  use of that URL can bypass MCP-layer suppression on minimized readable tables
  until database-enforced governed views/RLS/materialized snapshots replace
  direct table grants.
- Accepted residual performance risk: governed MCP still recomputes the raw-data
  governance fingerprint on each tool call. Replace this with a
  writer-maintained generation/fingerprint row before large-tenant GA.
- Still queued: database-enforced governed views/RLS/materialized snapshots and
  governance-safe analyst/all-readonly aggregates.

## Phase 3b Risk Status

- Closed for first profile lifecycle slice: Postgres now has
  `profile_call_fact_cache` plus active-profile/canonical-hash/data-fingerprint
  freshness checks. Writable `profile import`, `profile activate`, and
  `gongctl sync read-model --rebuild` warm the cache.
- Closed for supported business-pilot surfaces: `lifecycle_source=profile`
  works for lifecycle bucket definitions, lifecycle summary, lifecycle call
  search, transcript backlog ranking, call-fact summary, and coverage when the
  cache is fresh.
- Closed for read-only stale semantics: read-only Postgres/MCP never rebuilds
  profile facts. Missing or stale active-profile cache fails closed with an
  operator repair message.
- Closed for direct reader table boundary: `gongmcp_reader` cannot read raw
  profile YAML, canonical profile JSON columns, profile projection tables, or
  the physical profile fact-cache table. Profile field grouping uses an
  aggregate-only security-definer function; the per-call reader helper does not
  return mapped CRM `field_values_json`.
- Still queued: SQL-pushed profile search/backlog optimization for large
  tenants, profile cache logic-version invalidation, governance-aware profile
  filtering, and broader analyst/all-readonly profile surfaces.

## Phase 3 Risk Status

- Closed for metadata: Postgres now stores imported business profiles in
  profile metadata/mapping/warning tables, supports CLI import/history/activate
  and show when `--db` is omitted and a writer `GONG_DATABASE_URL` is set, and
  supports MCP `get_business_profile` / `list_business_concepts` through an
  execute-only reader helper function without granting direct reads on raw YAML,
  canonical JSON, or profile projection tables.
- Closed for operator visibility: Postgres `sync status` reports active profile
  readiness and profile cache status (`fresh`, `missing`, or `stale`).
- Closed by Phase 3b: Postgres `lifecycle_source=profile` is enabled for the
  first business-pilot profile slice. `auto` uses the active profile when the
  profile cache is fresh; with no active profile it uses builtin facts.
- Still queued: admin UX for profile MCP exposure, large-cache profile
  inventory optimization, and larger governance/analyst profile interactions.

## Phase 2 Risk Status

- Closed: Postgres no longer relies on SQL-only context extraction for the
  write-side normalized rows; it uses Go extraction aligned with SQLite row
  semantics.
- Closed: Postgres context extraction now records bounded diagnostics in
  `call_read_model_diagnostics` and keeps diagnostics to counts/flags/errors
  rather than raw CRM values.
- Closed: Postgres read-model state is versioned in
  `postgres_read_model_state`; read-only Postgres startup rejects missing/stale
  builtin facts instead of silently serving incomplete aggregates.
- Closed: Postgres read-model readiness no longer runs full table counts and
  anti-joins on the read/query hot path; `validateReadModelReady` reads the
  state row while triggers keep missing/orphan fact counters current for
  direct SQL changes and rebuild gaps.
- Closed: operators can run `gongctl sync read-model` to check state and
  `gongctl sync read-model --rebuild` with a writable Postgres URL to repair
  builtin facts.
- Closed for bounded synthetic validation: `scripts/postgres-load-smoke.sh`
  creates 750 synthetic calls and 1,500 transcript segments, rebuilds the
  Postgres read model, captures EXPLAIN artifacts for representative read
  paths, proves read-only MCP success through operator-smoke and business-pilot
  presets after rebuild, proves the reader role cannot write or directly read
  raw JSON payload columns, and proves stale startup denial. Keep a larger
  customer-scale benchmark queued before GA.
- Closed for Phase 6a operations diagnostics: `cache inventory` and
  `support bundle` can run against Postgres through `GONG_DATABASE_URL` /
  `DATABASE_URL`, including read-only-reader smoke coverage, schema/readiness
  diagnostics, table counts, and support artifact checks that reject database
  URL, path, secret, raw payload, transcript, and raw CRM value leakage.
- Still queued: database-enforced governance filtering/RLS, analyst and
  all-readonly query parity, purge/retention, larger
  customer-scale load benchmarking for read-model counter write contention, and
  release rollback/backup hardening.
