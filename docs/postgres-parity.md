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
| Read-model load/performance smoke | SQLite local cache has no shared write contention | complete for deterministic 750-call serial smoke, 1,200-call contention smoke, bounded over-cap profile-cache helper-call EXPLAIN evidence, and a synthetic capacity-drill wrapper | Capture rebuild timing, representative EXPLAIN plans, read-only MCP success, reader write/raw-read denial, stale startup denial, advisory-lock samples, post-contention row/profile-cache correctness, profile-backed helper-call plus equivalent index-probe evidence, and a sanitized capacity-drill summary at an explicit synthetic size | postgres-native equivalent | Phase 2/8b/9s/9t | `./scripts/postgres-load-smoke.sh`; `./scripts/postgres-contention-smoke.sh`; `./scripts/postgres-capacity-drill.sh` |
| Transcript FTS | SQLite FTS5 | Postgres `tsvector`/GIN with execute-only snippet function | Equivalent bounded search semantics, documented ranking differences | postgres-native equivalent | Phase 2 | synthetic search comparison tests |
| Call search | SQLite filters over calls/context | complete for normalized CRM object and builtin call-fact filters | Match safe filters or document intentional exclusions | must match | Phase 2 | `TestPostgresSearchCallsRawSafeFiltersMatchSQLite`; `scripts/postgres-smoke.sh` |
| MCP `get_call` | SQLite minimized call detail | complete for Postgres normalized context rows | Same minimized JSON contract over Postgres MCP | must match | Phase 2 | `TestPostgresGetCallDetailMatchesSQLiteForNormalizedContext`; `scripts/postgres-smoke.sh` |
| CLI `calls show` | SQLite raw cached JSON behind sensitive-export controls | complete for Postgres minimized call detail | Postgres uses `GetCallDetail` and does not return raw cached JSON, CRM field values, transcript text, or participant payloads | intentionally minimized for shared Postgres | Phase 2 | CLI call-detail smoke/tests |
| `business-pilot` MCP preset | SQLite full preset | complete/foundation over Postgres facts | Keep supported tools stable and read-only | must match | Phase 1 | `scripts/postgres-smoke.sh` |
| Tool-scoped Postgres reader roles | SQLite filesystem read-only boundary | complete for selected Postgres function grants | Generic `gongmcp_reader` remains supported; optional scoped mode validates required and extra `gongmcp_*` function EXECUTE grants against the selected preset/allowlist | postgres-native equivalent | Phase 9j | `TestPostgresReadOnlyOptionsForBusinessPilotAllowlist`; `scripts/postgres-smoke.sh` |
| Business-pilot scoped table grants | SQLite filesystem read-only boundary | complete for first Postgres business-pilot scoped role | Optional scoped startup validation checks selected function grants plus a first business-pilot table/column allowlist that denies direct `calls.call_id`, `calls.title`, `call_facts.call_id`, and `call_facts.title` while preserving generic reader compatibility | postgres-native hardening | Phase 9k | `TestPostgresReadOnlyOptionsForBusinessPilotAllowlist`; `scripts/postgres-smoke.sh` |
| Business-pilot scoped grant SQL handoff | Manual role/grant notes | complete for first Postgres business-pilot scoped role | `gongctl mcp postgres-reader-sql --preset business-pilot` is the canonical print-only operator command; `gongmcp --print-postgres-reader-grants --tool-preset business-pilot` is the MCP-only compatibility path. Both emit deterministic, secret-free grant SQL from the same reviewed function and column maps used by startup validation; profile-backed only until a sanitized builtin SQL surface exists | postgres-native operator handoff | Phase 9l | `TestPrintPostgresReaderGrantsForBusinessPilot`; `TestMCPPostgresReaderSQLBusinessPilot`; `scripts/postgres-smoke.sh` |
| Business-pilot scoped grant reconciliation | Manual SQL apply/stale grant repair | complete for existing Postgres business-pilot scoped roles | `gongctl mcp postgres-reader-apply --preset business-pilot` dry-runs the reviewed SQL by default and applies it only with `--apply` plus a writable `GONG_DATABASE_URL` / `DATABASE_URL`; role credentials remain external, output never includes database URLs or passwords, and smoke proves stale grants are repaired while scoped startup and direct-denial checks still pass | postgres-native operator lifecycle | Phase 9p | `TestMCPPostgresReaderApplySuccessJSONOmitsURL`; `scripts/postgres-smoke.sh` |
| Business-pilot sanitized profile-cache grants | SQLite filesystem read-only boundary | complete for first Postgres business-pilot scoped role | Exact `business-pilot` scoped roles grant `gongmcp_profile_call_fact_cache_sanitized_limited(bigint, text, integer)` instead of the identifier-bearing or unbounded sanitized profile-cache helpers; direct SQL through the scoped role is denied on both unbounded helpers and receives blank call IDs/titles from the capped helper | postgres-native hardening | Phase 9m/9o | `TestPostgresReadOnlyOptionsForBusinessPilotAllowlist`; `TestBuildScopedReaderGrantSQLBusinessPilot`; `scripts/postgres-smoke.sh` |
| Business-pilot scoped profile aggregate helpers | SQLite profile-cache aggregation | complete for exact Postgres business-pilot scoped role | Exact `business-pilot` scoped role grants sanitized lifecycle-summary and transcript-backlog helpers so profile aggregate MCP tools aggregate/filter in SQL before applying output limits instead of aggregating over the 1,000-row direct cache helper cap; bounded load smoke captures EXPLAIN for sanitized helper calls plus an equivalent profile-cache index probe and proves high-priority backlog rows outside the direct cap are still returned redacted | postgres-native hardening | Phase 9q/9s | `TestPostgresReadOnlyOptionsForBusinessPilotAllowlist`; `scripts/postgres-smoke.sh`; `scripts/postgres-load-smoke.sh` |
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
| MCP `list_unmapped_crm_fields` | SQLite profile-aware unmapped CRM field inventory | complete for explicit Postgres allowlists | Security-definer aggregate over normalized CRM context returns field metadata/counts/length stats only; mapped fields are filtered through the active profile and raw values remain denied | must match at MCP contract | Phase 9b | store parity test; MCP smoke; reader table/function denial smoke |
| MCP `search_crm_field_values` | SQLite explicit CRM value lookup | complete for explicit Postgres allowlists | Security-definer lookup over normalized CRM context with default redaction for call IDs, titles, object IDs/names, and values; explicit opt-ins return call IDs and bounded snippets/titles; the reader function never returns object IDs/names and direct table reads remain denied | must match at MCP contract | Phase 9a | store parity test; redacted/opt-in MCP smoke; reader function/table denial smoke |
| MCP `analyze_late_stage_crm_signals` | SQLite late-stage aggregate signal analysis | complete for explicit Postgres allowlists with `Opportunity.StageName` only | Security-definer aggregate over normalized CRM context returns stage counts, field names, population rates, and lift only; custom stage fields are rejected/empty to avoid raw value-distribution leakage, and raw arbitrary CRM values, CRM object IDs/names, call IDs, call titles, transcript text, and profile payloads remain denied | must match default MCP contract with safer Postgres field boundary | Phase 9c | store parity test; MCP smoke; reader table/function denial smoke |
| MCP `opportunities_missing_transcripts` | SQLite Opportunity transcript coverage aggregate | complete for explicit Postgres allowlists with SQL-boundary identifier redaction | Security-definer aggregate groups by Opportunity internally but returns only stage, call counts, missing/present transcript counts, and latest-call timing; the function/MCP output does not return Opportunity IDs/names, latest call IDs, object names, owner IDs, amount/close date, raw values, transcript text, or raw storage fields | must match redacted MCP contract | Phase 9d | store parity test; MCP smoke; reader function/table denial smoke |
| MCP `opportunity_call_summary` | SQLite Opportunity call aggregate | complete for explicit Postgres allowlists with SQL-boundary identifier redaction | Security-definer aggregate groups by Opportunity internally but returns only stage, call counts, transcript/missing-transcript counts, total duration, and latest-call timing; the function/MCP output does not return Opportunity IDs/names, latest call IDs, object names, owner IDs, amount/close date, raw values, transcript text, or raw storage fields | must match redacted MCP contract | Phase 9e | store parity test; MCP smoke; reader function/table denial smoke |
| MCP `crm_field_population_matrix` | SQLite CRM field population aggregate | complete for explicit Postgres allowlists with safe categorical object/field grouping | Security-definer aggregate over normalized CRM context returns group value, field name/label, object count, call count, and populated count only; MCP/store output derives population rate from those counts; unsafe object/field pairs are rejected before execution, the group-by field is excluded from cells, and object IDs/names, object keys, call IDs, non-group raw CRM values, raw JSON, raw hashes, titles, and transcript text remain denied | must match aggregate MCP contract with explicit Postgres grouping allowlist | Phase 9f | store parity test; MCP smoke; reader function/table denial smoke |
| MCP `compare_lifecycle_crm_fields` | SQLite lifecycle CRM field comparison aggregate | complete for explicit Postgres allowlists with aggregate-only SQL result shape | Security-definer aggregate compares field population between two builtin lifecycle buckets for the reviewed `Opportunity` object type and returns object type, field name/label, bucket call counts, bucket populated counts, rates, and rate delta only; unreviewed object types are rejected, governance-suppressed calls are excluded in SQL, and call IDs, titles, CRM object IDs/names/keys, raw CRM values, raw JSON, raw hashes, and transcript text remain denied | must match aggregate MCP contract with explicit Postgres allowlist | Phase 9h | store parity test; MCP smoke; reader function/table denial smoke |
| MCP `missing_transcripts` filtered search | SQLite missing-transcript record references with call/CRM filters | complete for explicit Postgres allowlists with reader-function CRM object ID filtering | Security-definer function returns bounded call IDs, titles, and started timestamps for admin transcript-backfill workflows, supports date/lifecycle/scope/system/direction/CRM object type/CRM object ID filters, and keeps CRM object IDs/names, raw CRM values, raw JSON, raw hashes, and transcript text out of function output and direct reader table grants | must match admin record-reference contract with explicit Postgres allowlist | Phase 9i | store parity test; MCP smoke; reader function/table denial smoke |
| MCP `search_transcripts_by_crm_context` | SQLite CRM-context transcript snippet search | complete for explicit Postgres allowlists with SQL-boundary identifier redaction | Security-definer snippet search intersects Postgres full-text transcript matches with normalized CRM context by object type and optional object ID; the SQL result omits call IDs, call titles, speaker IDs, CRM object IDs/names, object keys, raw CRM values, raw JSON, raw hashes, and full transcript text; Postgres uses `ts_rank_cd`/`ts_headline`, so SQLite FTS5 ranking and snippet text can differ | must match redacted MCP contract with documented FTS ranking differences | Phase 9g | store parity test; MCP smoke; reader function/table denial smoke |
| Scorecard activity | SQLite `scorecard_activity` | complete for aggregate Postgres slice | Same aggregate/read-only scorecard activity surfaces except raw reviewed-user grouping is rejected for Postgres read-only deployments; raw activity payloads and raw hashes are denied to the reader role | must match for aggregate surfaces | Phase 5d | scorecard activity parity tests; `scripts/postgres-smoke.sh` |
| Governance filtered DB export | SQLite physical filtered copy plus `VACUUM INTO` | Postgres policy-backed MCP suppression implemented for narrowed search slice; physical filtered export remains SQLite-only | Governed views, row-level security, or materialized governed snapshots before broad analyst/all-readonly GA | postgres-native equivalent | Phase 4 | restricted synthetic account absent from governed MCP search outputs |
| Governance audit | SQLite local audit against private YAML | complete for Postgres candidate scan plus persisted policy preparation | Audit Postgres coverage with writable operator role; read-only MCP validates policy/config/data fingerprints without exposing restricted names over MCP | postgres-native equivalent | Phase 4 | `gongctl governance audit --apply-postgres-policy`; governed smoke |
| Support bundle | SQLite sanitized support bundle | complete for metadata-only Postgres diagnostics | Sanitized Postgres diagnostics without secrets/customer payloads or database URLs | postgres-native equivalent | Phase 6a | support bundle fixture inspection; `scripts/postgres-smoke.sh` |
| Cache inventory | SQLite file/table inventory | complete for Postgres table/version/readiness diagnostics | Postgres table counts, schema version, read-model/profile readiness, and reader-role diagnostics without database URL export | postgres-native equivalent | Phase 6a | `TestPostgresCacheInventoryAndDiagnostics`; `scripts/postgres-smoke.sh` |
| Purge/retention | SQLite purge commands | complete for bounded call-scoped Postgres row cleanup plus scheduled policy YAML | Reader-role dry-run plan plus writable confirmed purge for calls and dependent transcript, CRM-context, read-model, profile-cache, scorecard-activity, and governance-suppression rows; config-driven approval metadata for scheduled retention; physical WAL/backups/replicas remain operator-owned | postgres-native equivalent | Phase 6b/8a | purge dry-run/confirm tests and smoke |
| Backup/restore | SQLite file copy guidance | complete for synthetic Postgres restore smoke and operator guidance | Postgres dump/restore, read-model rebuild, read-only MCP verification, reader denial checks, and role-grant guidance; PITR/replica/customer backup policy remains operator-owned | postgres-native equivalent | Phase 7 | `scripts/postgres-backup-restore-smoke.sh` |
| Release hardening | SQLite CI coverage plus release gates | complete for Postgres service tests plus backup/restore smoke in CI and image-publish gates | CI-backed Postgres service tests and versioned docs/images | must match release quality | Phase 7 | `.github/workflows/ci.yml`; `.github/workflows/publish-images.yml`; release checklist |

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
9. **Phase 8 retention/release follow-through**: scheduled retention policy
   YAML, customer-platform restore drills, and release hardening that depends on
   customer-owned infrastructure.
10. **Phase 9a targeted CRM value lookup**: explicit Postgres
    `search_crm_field_values` allowlist parity before broader full-preset
    enablement.
11. **Phase 9b targeted unmapped CRM field discovery**: explicit Postgres
    `list_unmapped_crm_fields` allowlist parity before broader full-preset
    enablement.
12. **Phase 9c targeted late-stage CRM signals**: explicit Postgres
    `analyze_late_stage_crm_signals` allowlist parity before broader
    full-preset enablement.
13. **Phase 9d targeted Opportunity transcript coverage**: explicit Postgres
    `opportunities_missing_transcripts` allowlist parity with SQL-boundary
    identifier redaction before broader full-preset enablement.
14. **Phase 9e targeted Opportunity call summary**: explicit Postgres
    `opportunity_call_summary` allowlist parity with SQL-boundary identifier
    redaction before broader full-preset enablement.
15. **Phase 9f targeted CRM field population matrix**: explicit Postgres
    `crm_field_population_matrix` allowlist parity with approved categorical
    grouping before broader full-preset enablement.
16. **Phase 9g targeted CRM-context transcript search**: explicit Postgres
    `search_transcripts_by_crm_context` allowlist parity with SQL-boundary CRM
    identifier redaction before broader full-preset enablement.
17. **Phase 9h targeted lifecycle CRM field comparison**: explicit Postgres
    `compare_lifecycle_crm_fields` allowlist parity with aggregate-only
    SQL-boundary output before broader full-preset enablement.
18. **Phase 9i filtered missing transcripts**: explicit Postgres
    `missing_transcripts` allowlist parity for admin transcript-backfill
    record references before broader full-preset enablement.
19. **Phase 9j tool-scoped reader grants**: optional Postgres reader-role
    function validation for selected MCP presets/allowlists before broader
    client deployments.
20. **Phase 9k business-pilot table grants**: optional Postgres startup
    validation for the first business-pilot scoped table/column boundary.
21. **Phase 9l business-pilot grant SQL handoff**: print-only helper emits the
    reviewed scoped reader grant SQL for customer-managed role provisioning.
22. **Phase 9m sanitized business-pilot profile cache grants**: replace the
    scoped-role dependency on the identifier-bearing profile-cache helper with
    a sanitized security-definer helper.
23. **Phase 9n sanitized business-pilot active profile grants**: replace the
    scoped-role dependency on the generic active-profile helper with a sanitized
    helper that omits source metadata, canonical hashes, canonical JSON, field
    evidence, and methodology IDs.
24. **Phase 9o bounded business-pilot profile helper grants**: replace exact
    business-pilot direct SQL access to unbounded sanitized profile-cache and
    CRM-value summary helpers with capped/sanitized helper surfaces.
25. **Phase 9p scoped reader grant reconciliation**: dry-run/apply operator
    command reconciles the reviewed business-pilot grant block for an existing
    role without managing credentials.
26. **Phase 9q scoped profile aggregate helpers**: move exact business-pilot
    profile lifecycle summary and transcript backlog reads onto sanitized SQL
    aggregate helpers so they do not depend on the capped direct row helper.
27. **Phase 9s profile backlog EXPLAIN/load evidence**: extend the bounded
    Postgres load smoke with synthetic over-cap active profile-cache data,
    sanitized helper-call EXPLAIN artifacts, a writer-only equivalent index
    probe, and assertions that ranked date-bounded backlog rows remain redacted
    and are not limited by the newest-1,000 direct helper cap.
28. **Phase 9t synthetic capacity drill harness**: add an operator-facing
    wrapper that runs the existing load smoke at an explicit larger synthetic
    target, validates generated profile/transcript artifacts directly, and
    writes a sanitized `capacity-summary.json` without claiming production
    capacity.

## Phase 9l Risk Status

- Closed for the first business-pilot table boundary: scoped startup validation
  now rejects extra direct column grants outside the reviewed business-pilot
  table/view surface, and the synthetic scoped role no longer has direct
  `calls.call_id`, `calls.title`, `call_facts.call_id`, or `call_facts.title`
  grants.
- Closed for first operator handoff: `gongctl mcp postgres-reader-sql --preset
  business-pilot` is the canonical print-only operator command, while `gongmcp
  --print-postgres-reader-grants --tool-preset business-pilot` remains the
  MCP-only compatibility path. Both emit deterministic SQL for the reviewed
  business-pilot scoped reader role without credentials or database URLs.
- Remaining risk after 9l: this is not a universal role generator. The first
  business-pilot scoped role is profile-backed rather than builtin lifecycle
  compatible, and non-business-pilot presets still use the generic/shared reader
  grant model until their own table/column maps are reviewed.

## Phase 9p Risk Status

- Closed for first scoped-role lifecycle usability: `gongctl mcp
  postgres-reader-apply --preset business-pilot` dry-runs the same reviewed SQL
  used by startup validation and reconciles it with `--apply` against a
  writable operator URL. The command emits sanitized JSON without database URLs
  or passwords and the synthetic smoke proves it can repair stale helper grants.
- Remaining risk: the command expects an existing customer-managed role and does
  not create LOGIN credentials, rotate passwords, or manage per-environment
  secret stores. It requires a standalone `NOINHERIT` role with no memberships,
  clears current public table/function grants, and validates final effective
  grants for current public objects, but it cannot clear default privileges
  created by other grantors for future objects. Operators should avoid default
  grants to the scoped service role and keep MCP startup privilege enforcement
  enabled. It remains business-pilot-only until other preset maps receive the
  same table/column/function review.

## Phase 9q Risk Status

- Closed for the first profile-backed business-pilot aggregate path: exact
  `business-pilot` scoped validation grants sanitized profile lifecycle-summary
  and transcript-backlog helpers. These helpers aggregate/filter against the
  full active profile cache in SQL before caller limits and keep direct SQL
  outputs identifier-minimized, while the direct row helper remains capped at
  1,000 rows.
- Phase 9s added bounded load evidence for this path: the Postgres load smoke
  seeds an over-cap synthetic profile cache, proves the direct capped helper
  returns only 1,000 rows while sanitized summary/backlog helpers use the full
  profile cache, captures `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)` artifacts
  for sanitized helper calls, and captures a writer-only companion
  profile-cache predicate EXPLAIN that shows available index use for the
  date-bounded filter at the synthetic size. PostgreSQL does not expose helper
  internals through the function-call EXPLAIN.
- Remaining risk after 9q: `rank_transcript_backlog` still returns
  identifier-minimized per-call operational metadata such as started time,
  lifecycle, confidence, duration, scope, system, direction, and rationale.
  The bounded load smoke is not production capacity proof. Other profile-backed
  surfaces still need their own reviewed SQL helpers before broad scoped role
  expansion.

## Phase 9s Risk Status

- Covered for bounded synthetic profile-cache evidence: `scripts/postgres-load-smoke.sh`
  now creates an active synthetic profile cache above the 1,000-row direct
  helper cap, captures `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)` artifacts for
  sanitized helper calls including the date-bounded backlog call, and records a
  writer-only equivalent profile-cache index probe artifact because the
  `SECURITY DEFINER` helpers can appear as function scans at the call boundary.
- The smoke proves the direct capped helper returns 1,000 rows and no
  `closed_won` rows from the oldest high-priority synthetic cohort, while the
  date-bounded sanitized backlog helper returns 25 ranked `closed_won` rows
  with blank call IDs/titles and positive priority.
- Remaining risk after 9s: this is bounded synthetic evidence, not a
  customer-capacity claim. Customer deployments still need tenant-scale
  profiling with real row counts, concurrency, retention windows, and platform
  Postgres settings before treating the profile-cache helper path as fully
  capacity-sized.

## Phase 9t Risk Status

- Added pre-rollout drill coverage: `scripts/postgres-capacity-drill.sh` runs
  the existing Postgres load smoke at a bounded synthetic target, validates the
  generated summary, profile-cache counts, redacted profile backlog counts,
  scoped `business-pilot` profile-source MCP output, profile helper-call
  EXPLAINs, the profile-cache index probe, and transcript-search EXPLAINs
  directly, then writes a sanitized `capacity-summary.json`.
- Remaining risk after 9t: the drill is still synthetic repo-local evidence. It
  is useful before introducing real tenant data to a client pilot environment,
  but it does not replace deployment-owned capacity testing against the target
  Postgres platform, concurrency, retention, backup/PITR, monitoring, and
  maintenance-window assumptions.

## Phase 9m/9n/9o Risk Status

- Closed for the first business-pilot profile-cache direct SQL gap:
  exact `business-pilot` scoped validation now requires
  `gongmcp_profile_call_fact_cache_sanitized_limited(bigint, text, integer)` and rejects the
  identifier-bearing `gongmcp_profile_call_fact_cache(bigint, text)` helper as
  an extra function grant.
- The synthetic scoped reader smoke proves direct SQL cannot execute the
  identifier-bearing or unbounded sanitized helpers, and the capped sanitized
  helper, currently fixed at 1,000 rows per direct helper call, returns zero
  identifier-bearing rows while still returning non-identifier fact rows needed
  by profile-backed MCP tools.
- Closed for the active-profile and profile-summary direct SQL gaps: exact
  `business-pilot` scoped validation now grants sanitized active-profile and
  profile-summary helpers. The active-profile helper omits source metadata,
  canonical hashes, canonical JSON, field evidence, tracker IDs, and scorecard
  question IDs. The profile-summary helper allows only non-CRM-value groupings
  for direct scoped SQL callers; CRM-value groupings remain on the compatibility
  reader path.
- Remaining risk: scoped reader URLs remain `gongmcp` service credentials,
  not analyst SQL logins. Selected functions still expose minimized operational
  metadata, timings, counts, tenant terminology, and a hashed profile-cache data
  fingerprint for freshness. Direct SQL callers can invoke capped sanitized
  profile-cache rows plus sanitized profile summary, lifecycle summary, and
  transcript backlog helpers; MCP result limits are still enforced above those
  SQL helpers. Explicit `lifecycle_source=builtin` still needs the compatibility
  reader until a sanitized builtin SQL surface exists.
- Still queued: per-surface table/column maps beyond `business-pilot`, optional
  richer role-template automation, and governed views/RLS/materialized snapshots
  for broader `analyst` / `all-readonly` readiness.

## Phase 9n Risk Status

- Closed for the first business-pilot active-profile direct SQL metadata gap:
  exact `business-pilot` scoped validation now requires
  `gongmcp_active_business_profile_sanitized()` and rejects the generic
  `gongmcp_active_business_profile()` helper as an extra function grant.
- The sanitized active-profile helper omits source path, source SHA, canonical
  SHA, imported_by, canonical_json, and field discovery evidence while
  preserving profile-backed MCP readiness and business terminology needed by
  the selected tools.
- Remaining risk: scoped reader URLs remain `gongmcp` service credentials,
  not analyst SQL logins. Selected functions still expose minimized operational
  metadata, timings, counts, and tenant terminology; generated SQL is
  fresh-role/additive; explicit `lifecycle_source=builtin` still needs the
  compatibility reader until a sanitized builtin SQL surface exists.

## Phase 9j Risk Status

- Closed for the first hardening slice: Postgres `gongmcp` can run in optional
  function-scoped grant-validation mode through
  `GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS=1` or
  `--enforce-tool-scoped-db-grants`. In that mode startup checks the selected
  preset/allowlist's required `gongmcp_*` functions and rejects executable
  functions outside that selected surface.
- The compatibility `gongmcp_reader` role remains supported for existing
  deployments. Treat that generic reader URL as a service secret because it can
  execute all reviewed reader functions granted to the role.
- Unresolved after Phase 9j: function-scoped validation does not yet validate
  table/column grants per selected tool. Scoped reader URLs remain service
  secrets because the shared baseline grants and selected functions can expose
  minimized call metadata such as call IDs, titles, timings, counts, and tenant
  terminology. This is a documented gap, not full SQL-layer preset isolation.
- Unresolved after Phase 9j: extra-grant validation scans `public.gongmcp_*`
  functions by prefix. A future app-owned reviewed-function inventory should
  replace prefix-based detection before broader customer role-generation
  automation.
- Still queued: installer/DDL helpers for customer-managed scoped roles,
  per-surface role templates beyond the synthetic business-pilot smoke, and
  database-enforced governance views/RLS/materialized snapshots for the broad
  `analyst` / `all-readonly` path.

## Phase 9i Risk Status

- Closed for the explicit allowlist slice: Postgres serves
  `missing_transcripts` with SQLite-compatible filters over normalized call
  facts and CRM context. MCP governance suppression overfetches before
  filtering so suppressed calls do not hide allowed rows behind the requested
  limit.
- Still queued: business-user-safe aggregate alternatives, profile-derived
  lifecycle source parity, per-surface reader roles, and broad `analyst` /
  `all-readonly` enablement. This tool intentionally returns call IDs and
  titles, so keep it admin/operator-only.

## Phase 9h Risk Status

- Closed for the explicit allowlist slice: Postgres serves
  `compare_lifecycle_crm_fields` through an execute-only reader function that
  compares CRM field population across two builtin lifecycle buckets for the
  reviewed `Opportunity` object type and returns only object type, field
  name/label, bucket call counts, bucket populated counts, rates, and rate
  delta. Unreviewed object types are rejected and governance-suppressed calls
  are excluded inside SQL. This is development-branch work after `v0.3.3` until
  a tagged release includes it.
- Still queued: broad `analyst` / `all-readonly` enablement, profile-derived
  lifecycle comparison parity, governance-safe aggregate variants, small-cell
  suppression, customer-scale aggregate performance testing, and per-surface
  reader roles. Field names/labels and exact lifecycle population rates can
  reveal tenant-specific CRM structure, so customer deployments should keep it
  behind explicit allowlists until aggregate privacy and performance hardening
  are complete.

## Phase 9g Risk Status

- Closed for the explicit allowlist slice: Postgres serves
  `search_transcripts_by_crm_context` through an execute-only reader function
  that joins transcript full-text matches to normalized CRM context and returns
  bounded snippets plus offsets without call titles, CRM object IDs/names,
  object keys, call IDs, speaker IDs, raw CRM values, raw JSON, raw hashes, or
  full transcript text in the SQL result shape. Governance-suppressed calls are
  excluded inside the reader function before MCP redaction.
- Still queued: broad `analyst` / `all-readonly` enablement, governance-safe
  aggregate/snippet variants, customer-scale ranking/performance testing,
  minimum-cell/coarser-time controls for CRM-context snippet searches, and
  per-surface reader roles. Snippets remain customer transcript content, and
  exact object-type/object-ID filtering can reveal the presence of a sensitive
  CRM-linked conversation when used by a trusted operator.

## Phase 9f Risk Status

- Closed for the explicit allowlist slice: Postgres serves
  `crm_field_population_matrix` through an execute-only reader function that
  returns aggregate field-population count cells by approved categorical
  object/field pairs and keeps object/call identifiers, non-group raw CRM
  values, raw JSON, and raw hashes out of the SQL result shape. Go and SQL both
  enforce reviewed object-type/group-field pairs for the current explicit
  slice.
- Still queued: broad `analyst` / `all-readonly` enablement, governance-safe
  aggregate variants, small-cell suppression, customer-scale aggregate
  performance testing, and per-surface reader roles. The current function can
  reveal approved group labels and exact population counts, and the final
  `LIMIT` caps output rather than scan work, so customer deployments should
  keep it behind explicit allowlists until aggregate privacy and performance
  hardening are complete.

## Phase 9e Risk Status

- Closed for the explicit allowlist slice: Postgres serves
  `opportunities_missing_transcripts` and `opportunity_call_summary` through
  execute-only reader functions that group by Opportunity internally while
  returning only redacted coverage and call-summary metadata.
- Closed for reader-role hardening: direct reader SELECT on
  `call_context_objects.object_id` is denied, and Postgres read-only call
  detail now redacts CRM object IDs.
- Still queued: broad `analyst` / `all-readonly` enablement, neighboring
  Opportunity CRM tools, governance-safe aggregate variants, minimum-cell or
  coarser time/duration redaction options, and customer-scale aggregate
  performance testing. The current Opportunity aggregates still perform full
  Opportunity-context grouping before applying the final row limit, and exact
  stage/count/duration/latest-call timing can fingerprint a small or unique
  deal when combined with allowed call metadata. Customer-scale deployments
  should treat them as explicit allowlists until a bounded/materialized
  aggregate and stronger aggregate privacy controls are added.

## Phase 9c Risk Status

- Closed for the explicit allowlist slice: Postgres serves
  `analyze_late_stage_crm_signals` through execute-only reader functions that
  classify late/non-late calls, count stage buckets, and return aggregate field
  lift without exposing raw arbitrary CRM values or identifiers. The Postgres
  slice is intentionally restricted to `Opportunity.StageName`; custom
  `stage_field` values are not enabled until a reviewed field-governance model
  exists.
- Still queued: broad `analyst` / `all-readonly` enablement, the remaining
  CRM-heavy tools, governance-safe aggregate variants, and customer-scale
  aggregate performance testing.

## Phase 7 Risk Status

- Closed for synthetic release hardening: `scripts/postgres-backup-restore-smoke.sh`
  starts an isolated Postgres service, syncs the synthetic fixture, rebuilds the
  read model, creates a custom-format `pg_dump`, restores into a second
  database, rebuilds readiness, verifies row-count equivalence, runs read-only
  `gongmcp` operator-smoke tools, and proves the restored reader role cannot
  write or directly read raw call JSON.
- Closed for CI/release gates: normal CI and the image publish workflow run
  Postgres-backed Go tests through `GONGCTL_TEST_POSTGRES_URL` and run the
  synthetic backup/restore smoke before release images publish.
- Closed for rollback guidance: release and operator docs now require a
  restorable pre-upgrade backup, writable migration/read-model validation, and
  read-only MCP smoke before promotion; rollback uses the prior image digest and
  prior verified backup.
- Accepted residual risk: production PITR, replica rewind, object-storage
  lifecycle, backup encryption, restore RTO/RPO, and cross-version customer-data
  restore drills are deployment-owned controls and must be validated in the
  customer's Postgres platform before GA.

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
  creates 750 synthetic calls and 1,500 transcript segments by default, rebuilds
  the Postgres read model, captures EXPLAIN artifacts for representative read
  paths, seeds an over-cap synthetic profile cache for business-pilot aggregate
  helper evidence, proves read-only MCP success through operator-smoke and
  business-pilot presets after rebuild, proves the reader role cannot write or
  directly read raw JSON payload columns, and proves stale startup denial.
- Closed for Phase 8b synthetic contention validation:
  `scripts/postgres-contention-smoke.sh` creates 1,200 synthetic calls with an
  active profile cache, holds the shared advisory writer lock while concurrent
  read-model rebuild, profile-cache refresh, and purge confirmation wait on it
  and reader status runs alongside them, samples named lock state, then verifies final
  read-model readiness, exact purge counts, profile-cache tombstone cleanup,
  reader write/raw-read denial, and MCP tools/list plus get_sync_status /
  rank_transcript_backlog success through the operator-smoke preset. This is
  repo-local release evidence for the shipped writer-lock behavior at the
  configured synthetic size, not customer capacity proof; customer-scale
  production benchmarking remains deployment-owned before broad or high-volume
  rollout.
- Closed for Phase 6a operations diagnostics: `cache inventory` and
  `support bundle` can run against Postgres through `GONG_DATABASE_URL` /
  `DATABASE_URL`, including read-only-reader smoke coverage, schema/readiness
  diagnostics, table counts, and support artifact checks that reject database
  URL, path, secret, raw payload, transcript, and raw CRM value leakage.
- Closed for Phase 6b retention diagnostics: `cache purge` can run against
  Postgres through `GONG_DATABASE_URL` / `DATABASE_URL`, previews metadata-only
  counts through the reader role, rejects confirmed purges with the reader URL,
  and deletes a transaction-materialized call ID set with a writable URL without
  exporting raw identifiers, transcript text, or database URLs. Postgres keeps
  call-ID tombstones as operational metadata to block accidental rehydration of
  purged call-scoped rows by later sync steps. Supported Postgres write paths
  and confirmed purge share a database advisory writer lock, but operators
  should still run destructive cleanup in a maintenance window with scheduled
  sync jobs stopped.
- Closed for Phase 8a scheduled retention policy YAML: `cache purge --config`
  accepts a reviewed policy file with cutoff, approval reference, approver,
  approval date, data owner, backup reference, and legal-hold review metadata.
  Config dry-runs require the Postgres reader role, confirmed config purges
  require complete approval metadata plus a writable URL, and command output
  includes policy SHA-256 plus sanitized approval metadata without local policy
  paths. Scheduler installation, production PITR/replica retention, and
  customer backup-policy enforcement remain deployment-owned.
- Closed for Phase 9a CRM field-value search parity: Postgres implements
  explicit `search_crm_field_values` allowlists through a security-definer
  function over normalized CRM context. MCP defaults redact call IDs, titles,
  object IDs/names, and value snippets; explicit opt-ins return call IDs plus
  bounded snippets/titles. The reader function enforces those flags, never
  returns object IDs/names, and the reader role still cannot directly select
  raw CRM field values.
- Closed for Phase 9b unmapped CRM fields parity: Postgres implements explicit
  `list_unmapped_crm_fields` allowlists through a security-definer aggregate
  over normalized CRM context. Output is limited to field metadata, population
  counts, distinct counts, and value length statistics; mapped fields are
  filtered through the active business profile, and raw values stay denied.
- Still queued: database-enforced governance filtering/RLS, full analyst and
  all-readonly query parity, customer-platform PITR/replica restore drills, and
  cross-version customer-data restore validation before GA.
