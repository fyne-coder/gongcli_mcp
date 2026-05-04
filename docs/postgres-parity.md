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
| Derived read-model lifecycle | SQLite views rebuild from base tables at read time | complete in Phase 2 for builtin facts: migration/write refresh plus state table | Backfill on upgrade, refresh on writes, version/stale detection before profile/governance phases | postgres-native equivalent | Phase 2 | `TestPostgresReadModelStateDetectsDeletedFactRowsAsStaleAndRebuildRepairs`; `gongctl sync read-model` |
| Operator read-model check/rebuild | SQLite profile cache readiness/rebuild is profile-aware | complete for Postgres builtin facts via `gongctl sync read-model [--rebuild]` | Writable CLI can check/rebuild; read-only MCP never rebuilds | postgres-native equivalent | Phase 2 | `GONG_DATABASE_URL=... gongctl sync read-model --rebuild` |
| Transcript FTS | SQLite FTS5 | Postgres `tsvector`/GIN | Equivalent bounded search semantics, documented ranking differences | postgres-native equivalent | Phase 2 | synthetic search comparison tests |
| Call search | SQLite filters over calls/context | complete for normalized CRM object and builtin call-fact filters | Match safe filters or document intentional exclusions | must match | Phase 2 | `TestPostgresSearchCallsRawSafeFiltersMatchSQLite`; `scripts/postgres-smoke.sh` |
| MCP `get_call` | SQLite minimized call detail | complete for Postgres normalized context rows | Same minimized JSON contract over Postgres MCP | must match | Phase 2 | `TestPostgresGetCallDetailMatchesSQLiteForNormalizedContext`; `scripts/postgres-smoke.sh` |
| CLI `calls show` | SQLite minimized call detail | queued | Decide whether CLI should use Postgres `GetCallDetail` or remain SQLite-only until broader CLI parity | must match before enablement | Phase 2 | CLI call-detail tests |
| `business-pilot` MCP preset | SQLite full preset | complete/foundation over Postgres facts | Keep supported tools stable and read-only | must match | Phase 1 | `scripts/postgres-smoke.sh` |
| `operator-smoke` MCP preset | SQLite health/search smoke | partial; Postgres uses explicit operator allowlists for `get_call` | Postgres equivalent for deployment validation | must match | Phase 2 | MCP tools/list and tools/call smoke |
| `governance-search` MCP preset | SQLite governed search | queued | Governed Postgres search or explicit unsupported state | postgres-native equivalent | Phase 4 | governed synthetic smoke |
| `analyst` MCP preset | SQLite analyst/cohort tools | rejected in Postgres | Enable only after backing queries and governance are ready | must match before enablement | Phase 5 | preset parity tests |
| `all-readonly` MCP preset | SQLite full read-only catalog | rejected in Postgres | Enable only after full read-only query parity | must match before enablement | Phase 5 | catalog parity tests |
| Profile import/show | SQLite profile tables/cache | queued | Postgres profile metadata and active-state storage | must match | Phase 3 | profile import/show tests |
| Profile lifecycle source | `lifecycle_source=auto|profile|builtin` with `profile_call_fact_cache` | Postgres supports builtin/auto-as-builtin only | Profile cache parity and stale-cache read-only semantics | must match | Phase 3 | profile lifecycle parity tests |
| Business-analysis calls/evidence | SQLite `business_analysis.go` helpers | queued | Equivalent read APIs over normalized context/facts/transcripts | must match | Phase 5 | business-analysis parity fixtures |
| CRM schema/settings inventory | SQLite `crm_integrations`, schema, settings, scorecards | queued for Postgres | Same cached metadata read surfaces | must match | Phase 5 | inventory/query tests |
| Scorecard activity | SQLite `scorecard_activity` | queued for Postgres | Same aggregate/read-only scorecard activity surfaces | must match | Phase 5 | scorecard activity parity tests |
| Governance filtered DB export | SQLite physical filtered copy plus `VACUUM INTO` | queued | Governed views, row-level security, or materialized governed snapshots | postgres-native equivalent | Phase 4 | restricted synthetic account absent from all MCP outputs |
| Governance audit | SQLite local audit against private YAML | queued | Audit Postgres governed/read model coverage without exposing restricted names | postgres-native equivalent | Phase 4 | `gongctl governance audit` Postgres tests |
| Support bundle | SQLite sanitized support bundle | queued | Sanitized Postgres diagnostics without secrets/customer payloads | postgres-native equivalent | Phase 6 | support bundle fixture inspection |
| Cache inventory | SQLite file/table inventory | queued | Postgres DB/table/index/version/readiness inventory | postgres-native equivalent | Phase 6 | `cache inventory` Postgres tests |
| Purge/retention | SQLite purge commands | queued | Postgres retention diagnostics and dry-run/confirm semantics | postgres-native equivalent | Phase 6 | purge dry-run tests |
| Backup/restore | SQLite file copy guidance | queued | Postgres dump/restore, migration rollback, and role-grant guidance | postgres-native equivalent | Phase 7 | documented operator smoke |
| Release hardening | SQLite CI coverage plus release gates | queued for Postgres service tests | CI-backed Postgres service tests and versioned docs/images | must match release quality | Phase 7 | CI service test + release checklist |

## Phase Boundaries

1. **Phase 1 foundation**: normalized context/fact tables, maintained by writes,
   and business-pilot reads moved to indexed facts.
2. **Phase 2 read-model hardening and core query parity**: close normalized
   context/readiness risks first, then continue call/search/transcript/lifecycle
   output comparisons.
3. **Phase 3 profile parity**: profile storage, cache warming, and
   `lifecycle_source=profile`.
4. **Phase 4 governance parity**: governed Postgres read surfaces and audit.
5. **Phase 5 analyst/all-readonly parity**: broader MCP and business-analysis
   surfaces.
6. **Phase 6 operations parity**: support bundle, cache inventory, purge, and
   diagnostics.
7. **Phase 7 release hardening**: CI service tests, backup/restore, migration
   rollback, docs, and versioned release artifacts.

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
- Closed: operators can run `gongctl sync read-model` to check state and
  `gongctl sync read-model --rebuild` with a writable Postgres URL to repair
  builtin facts.
- Still queued: profile cache parity, governance filtering/RLS, analyst and
  all-readonly query parity, support/cache inventory, purge/retention, and
  release rollback/backup hardening.
