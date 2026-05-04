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
| Builtin lifecycle facts | `call_lifecycle` view | materialized into `call_facts` | Deterministic builtin lifecycle columns from normalized context | must match at output layer | Phase 1 | SQLite-vs-Postgres lifecycle aggregate tests |
| Call facts | `call_facts` view | foundation table added | Maintained indexed facts table for grouping/filtering | must match at output layer | Phase 1 | SQLite-vs-Postgres call-fact aggregate tests |
| Derived read-model lifecycle | SQLite views rebuild from base tables at read time | Postgres facts are materialized on migration/write | Backfill on upgrade, refresh on writes, add version/stale detection before profile/governance phases | postgres-native equivalent | Phase 1/3 | migration backfill and stale-cache tests |
| Transcript FTS | SQLite FTS5 | Postgres `tsvector`/GIN | Equivalent bounded search semantics, documented ranking differences | postgres-native equivalent | Phase 2 | synthetic search comparison tests |
| Call search | SQLite filters over calls/context | limited Postgres date/transcript filters | Match safe filters or document intentional exclusions | must match | Phase 2 | SQLite-vs-Postgres search tests |
| `calls show` | SQLite minimized call detail | queued | Same minimized JSON contract over Postgres | must match | Phase 2 | CLI/MCP call-detail tests |
| `business-pilot` MCP preset | SQLite full preset | complete/foundation over Postgres facts | Keep supported tools stable and read-only | must match | Phase 1 | `scripts/postgres-smoke.sh` |
| `operator-smoke` MCP preset | SQLite health/search smoke | partial via explicit allowlist | Postgres equivalent for deployment validation | must match | Phase 2 | MCP tools/list and tools/call smoke |
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
2. **Phase 2 core query parity**: call/search/transcript/lifecycle output
   comparisons.
3. **Phase 3 profile parity**: profile storage, cache warming, and
   `lifecycle_source=profile`.
4. **Phase 4 governance parity**: governed Postgres read surfaces and audit.
5. **Phase 5 analyst/all-readonly parity**: broader MCP and business-analysis
   surfaces.
6. **Phase 6 operations parity**: support bundle, cache inventory, purge, and
   diagnostics.
7. **Phase 7 release hardening**: CI service tests, backup/restore, migration
   rollback, docs, and versioned release artifacts.
