# Postgres Question-Parity Matrix

This matrix maps the SQLite-era business question set to the current Postgres
shared-deployment surface. It is written for controlled client pilots using a
redacted Postgres serving database, scoped reader grants, and a reviewed MCP
preset such as `analyst` / `analyst-expansion`.

SQLite remains the default local workflow with the broadest coverage. Postgres is the preferred
shared deployment path for multi-container pilots, but `all-readonly` parity is
still gated.

## Status Legend

- `supported`: expected to work in the current reviewed Postgres pilot surface.
- `supported with caveats`: works only with specific synced data, presets,
  opt-ins, or evidence boundaries.
- `blocked`: intentionally unavailable from Postgres MCP or still queued.

## Matrix

| SQLite-era question | Postgres status | Postgres path | Caveats and reader-facing wording |
| --- | --- | --- | --- |
| Is the cache fresh enough to use? | supported | `get_sync_status` | Start every session here. Results reflect the last approved sync, not live Gong. |
| How many calls/transcripts are available in the selected window? | supported | `get_sync_status`, `summarize_call_facts`, `summarize_calls_by_lifecycle` | Counts are safe to discuss. Use the deployment baseline for manual-test comparison. |
| Where is transcript coverage weak by lifecycle, scope, direction, or month? | supported | `summarize_call_facts`, `summarize_calls_by_lifecycle`, `rank_transcript_backlog` | Postgres answers from materialized facts and cached transcript state. |
| Which conversations should the operator prioritize for transcript refresh? | supported | `rank_transcript_backlog`, explicit/admin `missing_transcripts` allowlist | Business surfaces use redacted backlog metadata. Admin missing-transcript record references require explicit approval. |
| Find Business Discovery or other title-scoped cohorts. | supported | `build_call_cohort`, `inspect_call_cohort`, `search_calls_by_filters`, `get_call` with `call_ref` for follow-up detail | Title filtering works against cached call metadata. Treat call titles as customer operational metadata. Redacted follow-up can use stable `call_ref` without asking for raw call IDs. |
| Search transcript snippets for a phrase or theme. | supported | `search_transcript_segments`, `search_transcripts_by_filters`, `search_transcript_quotes_with_attribution` | Returns bounded snippets or quotes, not full transcripts. Provenance and raw IDs depend on explicit opt-ins. |
| Summarize one selected call. | supported with caveats | Bounded snippet/quote tools, then host-model synthesis | Do not claim a content summary unless the tool returns transcript evidence for that call. If only metadata is available, say so. |
| Discover broad themes in a cohort. | supported with caveats | `discover_themes_in_cohort`, `summarize_themes_by_dimension`, quote-pack tools | Broad discovery accepts a selective cohort filter without `theme_query`/`query` (Phase 13e seedless mode) and returns deterministic candidate seed terms plus a `broad_discovery_seedless` warning. Quote/evidence tools (`search_quotes_in_cohort`, `build_quote_pack`, `extract_theme_quotes`, `build_theme_brief`, etc.) still require an explicit `query`/`theme_query`. Label final narrative as evidence-backed synthesis unless the structured tool returns clean theme rows. |
| Compare themes across quarters or segments. | supported with caveats | `compare_themes_over_time`, `compare_themes_by_segment`, `compare_call_cohorts` | Needs comparable cohort definitions and enough transcript coverage in each slice. |
| Segment concerns by persona or participant title. | supported with caveats | `summarize_themes_by_persona`, `rank_personas_by_insight_quality`, `diagnose_attribution_coverage` | The persona dimension returns coarse, deterministic role buckets (`procurement`, `supplier_enablement`, `it_security_integration`, `finance`, `operations`, `sales_revenue`, `executive`, `other_title_present`) computed from cached party metadata and CRM Contact/Lead Title fields; raw participant title strings are never returned as dimension values. Calls with participants but no resolvable title still appear under `<blank>`; Postgres can also return `participant_title_present` when only the materialized `call_facts` flag remains. The existing `participant_title_missing_or_unmapped` warning continues to fire when no titles are present in the cohort. |
| Segment themes by industry or account attributes. | supported with caveats | `summarize_themes_by_industry`, `inspect_call_cohort`, CRM aggregate tools | Depends on synced CRM/account fields and the approved field allowlist. Do not infer industries from titles or snippets. |
| Search for a specific non-restricted company in an approved analyst session. | supported with caveats | Reviewed analyst/filter tools over the redacted serving DB | Allowed only when the deployment intentionally enables the broad analyst/redacted-serving mode. Some narrower readers still block raw `account_query` to prevent customer-name probing. |
| Verify a blocklisted or restricted customer is absent. | supported with caveats | Negative probe in manual-test checklist plus serving-DB smoke | Use only approved negative probes. Expected result is zero rows/evidence from the redacted serving DB; do not paste restricted names into shared artifacts. |
| Enumerate customer names or run broad account discovery. | blocked | none | Customer/account enumeration is not a client MCP workflow. Use scoped business filters and approved cohorts instead. |
| Analyze pipeline progression by theme. | supported with caveats | `compare_theme_outcomes`, `summarize_pipeline_progression_by_theme`, `compare_won_lost_theme_patterns` | Requires cached CRM outcome/stage coverage. Missing outcomes must be reported as unknown, not inferred from sentiment. |
| Analyze loss reasons by theme. | supported with caveats | `summarize_loss_reasons_by_theme`, `diagnose_attribution_coverage` | Only meaningful when loss-reason fields are populated. Small-cell suppression may omit low-count buckets. |
| Identify late-stage CRM risks or field population gaps. | supported with caveats | `analyze_late_stage_crm_signals`, `crm_field_population_matrix`, `compare_lifecycle_crm_fields` | Postgres uses reviewed aggregate surfaces and approved object/field pairs. It does not expose raw arbitrary CRM values. |
| List scorecards and scorecard question areas. | supported with caveats | `list_scorecards`, `get_scorecard`, `summarize_scorecard_activity` in approved presets | Scorecard names, questions, and stable IDs are sensitive configuration metadata. Enable only when the pilot includes scorecard inventory. |
| Summarize answered scorecard activity. | supported with caveats | `summarize_scorecard_activity` | Aggregate-only. Raw answer text, raw activity payloads, call IDs, and user IDs stay out of the Postgres reader output. |
| Pull Gong-native call briefs, highlights, outlines, or CRM entity briefs. | supported with caveats for highlights; blocked for entity briefs and topics/trackers/comments | `gongctl sync calls --include-highlights` plus typed `call_ai_highlights` Postgres read model surfaced through the reviewed `evidence.highlights.list` facade operation routed by `gong_get_evidence` | Highlights are captured opt-in and typed into a governed Postgres read model, including redacted serving DB filtering. The MCP-facing `evidence.highlights.list` requires explicit `call_ids`, returns only typed columns (no raw JSON), enumerates no accounts, filters runtime-suppressed calls, and warns that highlights are Gong AI accelerators while transcript quotes remain primary evidence. Entity briefs and topics/trackers/comments are still queued. |
| Pull live data from Gong during a business-user MCP session. | blocked | none | `gongmcp` reads the reviewed cache only. Operators refresh data through `gongctl` outside the business-user session. |
| Dump full transcripts, raw call JSON, raw CRM JSON, or arbitrary SQL. | blocked | none | Postgres MCP is not a raw database/API bridge. Use bounded snippets, aggregate tools, and operator-only exports when separately approved. |
| Use the full SQLite `all-readonly` catalog over Postgres. | blocked | queued | Postgres rejects `all-readonly`, `all-tools`, and `all` until full catalog parity and governance review are complete. |
| Apply customer blocklist changes without reloading all Gong data. | supported | `gongctl governance refresh-serving-db` | Edit the private YAML and rerun the serving DB refresh. `gongmcp` does not need restart when the same serving DB URL, reader role/grants, auth, binary, and preset stay unchanged. |

## Demo Guidance

- Start with readiness and coverage before analysis.
- For every synthesized business answer, separate `tool-returned evidence` from
  `host-model synthesis`.
- Treat missing transcript, missing persona/title, missing industry, and missing
  CRM outcome coverage as first-class limitations.
- Use the redacted serving database for client-facing demos when blocklisted
  customers must be physically absent from the MCP path.
- Keep `all-readonly` and raw export workflows in the trusted SQLite/operator
  lane until Postgres full-catalog parity is explicitly completed.
