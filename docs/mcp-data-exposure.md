# MCP Data Exposure

## Scope

This document describes the data exposure contract for the read-only `gongmcp`
server as implemented in `internal/mcp/server.go`. Stdio remains the default
local transport; HTTP `/mcp` is a private-pilot request/response transport over
the same read-only tool layer.

Current fixed boundaries:

- MCP reads a local SQLite cache only.
- MCP does not call Gong live.
- `gongmcp --tool-preset` / `GONGMCP_TOOL_PRESET` and
  `--tool-allowlist` / `GONGMCP_TOOL_ALLOWLIST` can reduce the exposed tool
  surface; HTTP mode requires an explicit preset or allowlist. When neither is
  set, the full read-only catalog remains available only for stdio.
- `gongmcp --ai-governance-config` and `GONGMCP_AI_GOVERNANCE_CONFIG` can
  suppress calls linked to private restricted-customer name/alias matches before
  MCP output reaches an LLM. The preferred blocklist path is
  `gongctl governance export-filtered-db`, which scans call titles, raw call
  metadata including participant emails, embedded CRM values, and transcript
  segment text, then points MCP at the physically filtered copy. The real list
  must stay outside the public repo.
- MCP does not expose raw Gong API passthrough, arbitrary SQL, raw cached call JSON, profile import, or full transcript dumps.
- HTTP mode can require bearer tokens, but bearer auth is an access gate, not
  tenant separation or data anonymization.

## Exposure Levels

| Level | Meaning |
| --- | --- |
| Aggregate | Counts, rates, readiness, or classification metadata with no direct record references |
| Config | Tenant configuration/schema metadata such as field names, scorecard names, question text, or inventory IDs |
| Record reference | Direct business record metadata such as call IDs, titles, object IDs, object names, or timestamps tied to a specific record |
| Snippet | Bounded transcript-derived text excerpts or bounded CRM value excerpts |
| Opt-in elevation | Additional identifiers or text returned only when the caller explicitly sets an exposure flag |

## Tool Exposure Matrix

| Tools | Pilot classification | Default exposure | Default protections | Residual risk |
| --- | --- | --- | --- | --- |
| `get_sync_status` | Safe-default | Aggregate | Redacts active profile name and canonical hash; returns counts/readiness only | Reveals tenant activity and coverage posture |
| `summarize_call_facts`, `summarize_calls_by_lifecycle` | Safe-default | Aggregate | Return rates, counts, classification logic, or allowlisted business dimensions only | Group labels can still expose tenant-specific terminology |
| `rank_transcript_backlog`, `prioritize_transcripts_by_lifecycle` | Safe-default with review | Aggregate | Server blanks call IDs and titles before returning ranked backlog rows | Still reveals lifecycle, confidence, duration, and prioritization rationale |
| `list_scorecards`, `get_scorecard` | Safe-default with review | Config | No raw settings payloads | Exposes scorecard names, question text, and scoring metadata, which may reflect internal QA/coaching policy |
| `list_crm_object_types`, `list_crm_fields`, `list_unmapped_crm_fields` | Restricted | Aggregate + Config | Counts and field metadata only; no field values by default | Field names and labels can still reveal tenant business model |
| `analyze_late_stage_crm_signals`, `crm_field_population_matrix`, `list_lifecycle_buckets`, `compare_lifecycle_crm_fields` | Restricted | Aggregate | Return rates, counts, classification logic, or allowlisted business dimensions only | Business groupings can reveal tenant-specific CRM structure |
| `list_crm_integrations`, `list_cached_crm_schema_objects`, `list_cached_crm_schema_fields`, `list_gong_settings` | Restricted | Config | No raw settings payloads | Still exposes integration IDs, object IDs, workspace IDs, tracker names, and related inventory metadata |
| `get_business_profile`, `list_business_concepts` | Restricted | Config | Redacts source path, source hash, canonical hash, and imported-by identity | Still exposes tenant lifecycle/methodology concepts and mapping logic |
| `opportunities_missing_transcripts`, `opportunity_call_summary` | Restricted | Aggregate + Config | Server blanks opportunity IDs, opportunity names, owner IDs, amount, close date, and latest call IDs | Still reveals stage, coverage, duration totals, and latest-call timing at an opportunity-summary level |
| `search_transcripts_by_call_facts` | Restricted | Snippet | No call IDs, titles, or speaker IDs in the result shape | Still returns bounded transcript/context excerpts plus lifecycle/scope/system/direction metadata |
| `search_transcript_quotes_with_attribution` | Restricted | Snippet + Opt-in elevation | Call IDs, call titles, Account names/websites, and Opportunity names/close dates/probabilities are blank unless explicitly requested; `account_query` is rejected unless Account-name output is explicitly enabled; returns participant/person-title readiness status | Still exposes bounded quote/context excerpts plus industry, stage, and other attribution metadata when present |
| `search_transcript_segments` | Restricted | Snippet | Call IDs and speaker IDs are blank unless explicitly requested | Default output still includes snippet text and time offsets |
| `search_transcripts_by_crm_context` | Restricted | Snippet | Server blanks call ID, title, object ID, object name, and speaker ID | Still returns transcript-derived snippets tied to an object type and call time |
| `search_calls`, `search_calls_by_lifecycle`, `missing_transcripts` | Admin-only | Record reference | Return minimized call metadata rather than raw JSON | Exposes call IDs, titles, timestamps, and durations |
| `get_call` | Admin-only | Record reference | Omits raw participant payloads, transcript payloads, CRM field values, and CRM object names; truncates object and field-name lists | Still exposes call ID/title plus CRM object IDs and field names for one call |
| `search_crm_field_values` | Admin-only | Config + Snippet | Object ID/name always blanked; call ID blank unless `include_call_ids=true`; title/value snippet returned only when `include_value_snippets=true` | Explicit opt-in can reveal bounded CRM value excerpts, call titles, and call IDs for targeted lookups |

## Analyst Cohort Tool Exposure

The full analyst cohort surface in `executor_tasks.md` is intended for trusted
analyst sessions after sponsor/operator approval. These tools must remain
read-only, bounded, and filter-driven. They must not accept raw SQL, arbitrary
table names, arbitrary column names, unbounded result sizes, or live Gong API
credentials.

Required filter contract:

- Accept a `call_filter` with allowlisted fields only:
  `title_query`, `query`, `from_date`, `to_date`, `quarter`,
  `lifecycle_bucket`, `scope`, `system`, `direction`, `transcript_status`,
  `industry`, `account_query`, `opportunity_stage`, `crm_object_type`,
  `crm_object_id`, `participant_title_query`, and `limit`.
- Echo the normalized filter in every cohort response so a host can reproduce
  the same call set after process restart.
- Treat `cohort_id` as a deterministic convenience handle, not as the only
  durable state. Hosts should carry the echoed normalized filter between calls;
  the server does not keep a restart-safe cohort registry.
- Require `query`, `theme_query`, or `filter.query` before any analyst tool
  returns transcript excerpts or quote candidates. Blank excerpt requests fail
  closed instead of sampling arbitrary transcript text.
- Return coverage, warning, and limitation metadata when attribution,
  transcript, persona, industry, opportunity, loss-reason, or won/lost fields
  are missing.
- Use a physically governance-filtered DB for analyst sessions when customer
  exclusions apply. Raw-DB AI governance mode intentionally fails closed for
  these aggregate cohort tools because their counts and slices are not
  recomputed over a filtered call set.

| Tools | Intended preset | Default exposure | Required protections | Residual risk |
| --- | --- | --- | --- | --- |
| `build_call_cohort`, `inspect_call_cohort`, `list_call_cohorts`, `compare_call_cohorts` | `analyst`, `all-readonly` | Aggregate + limited cohort metadata | Filter allowlist, deterministic cohort id, echoed normalized filter, counts, coverage summary, warning flags | Small cohorts or narrow filters can still make records recognizable to the operator |
| `search_calls_by_filters`, `summarize_calls_by_filters` | `analyst`, `all-readonly` | Aggregate or bounded record summaries | Bounded `limit`, no raw SQL, default identifier redaction unless a reviewed policy says otherwise | Call metadata can reveal business process or tenant terminology |
| `search_transcripts_by_filters` | `analyst`, `all-readonly` | Snippet | Requires a search term, bounded excerpts, transcript-status filtering, no full transcript text, redacted provenance by default | Snippets are still customer data and can be identifying |
| `discover_themes_in_cohort`, `summarize_themes_by_dimension`, `compare_themes_over_time`, `compare_themes_by_segment` | `analyst`, `all-readonly` | Aggregate + deterministic theme signals | Label outputs as cache-derived heuristic signals, include support counts and coverage, avoid LLM-style unsupported conclusions | Low-coverage themes can be overinterpreted by the host model |
| `extract_theme_quotes`, `search_quotes_in_cohort`, `rank_quotes_for_sales_use`, `build_quote_pack` | `analyst`, `all-readonly` | Snippet + optional evidence packaging | Requires a theme/search term, bounded quote count and length, safe attribution defaults, explicit opt-ins for names/titles/ids where policy supports them | Quote packs can carry sensitive customer language into the host model |
| `compare_theme_outcomes`, `summarize_pipeline_progression_by_theme`, `summarize_loss_reasons_by_theme`, `compare_won_lost_theme_patterns` | `analyst`, `all-readonly` | Aggregate + CRM outcome coverage | Graceful degradation when stage, close status, loss reason, or opportunity linkage is absent; no causal claims from correlation alone | Pipeline labels and outcome rates can expose sensitive revenue context |
| `summarize_themes_by_persona`, `summarize_themes_by_industry`, `rank_personas_by_insight_quality`, `diagnose_attribution_coverage` | `analyst`, `all-readonly` | Aggregate + attribution coverage | Report missing-title and missing-industry rates; never infer titles or industries from call names or snippets | Persona and industry slices may reveal go-to-market strategy |
| `generate_sales_hooks_from_themes`, `generate_outreach_sequence_inputs`, `recommend_target_personas_and_industries`, `build_theme_brief` | `analyst`, `all-readonly` | Structured synthesis inputs | Return evidence-backed inputs for the host model; label hypotheses; include evidence counts and limitations | Host-generated copy can overstate cache-derived evidence if limitations are dropped |
| `score_cohort_evidence_quality`, `explain_analysis_limitations`, `suggest_filter_refinements` | `analyst`, `all-readonly` | Aggregate + limitations | Always safe to call at the end of an analyst flow; recommend narrower filters and operator refreshes rather than filling missing data | Limitation summaries can still reveal where the tenant has weak process/data coverage |

## Highest-Risk MCP Tools

The following tools deserve the most review before enabling them in a model-facing host:

| Tool or group | Risk driver |
| --- | --- |
| `get_call`, `search_calls`, `search_calls_by_lifecycle`, `missing_transcripts` | Direct record references including call IDs and titles |
| `get_scorecard`, `list_gong_settings`, `list_scorecards`, `list_crm_integrations` | Internal configuration inventory and identifiers |
| `search_transcript_segments`, `search_transcripts_by_crm_context`, `search_transcripts_by_call_facts`, `search_transcript_quotes_with_attribution` | Transcript-derived snippet exposure |
| `search_crm_field_values` | Explicit value lookup path with opt-in snippets and call identifiers |

## Practical Usage Guidance

- Use aggregate tools first for readiness, coverage, and prioritization questions.
- In company deployments, set a server-side tool preset or allowlist instead
  of relying on host prompts alone.
- For customer-specific AI restrictions, run `gongctl governance audit` with the
  private config before MCP use, then start `gongmcp` with the same config.
  Prefer `gongctl governance export-filtered-db` and point MCP at the filtered
  copy whenever a blocklist exists. Raw-DB governance mode requires an explicit
  tool preset or allowlist and refuses unsupported aggregate/config tools instead of
  returning unfiltered counts or metadata. `search_crm_field_values` is
  intentionally unavailable in raw-DB governance mode because direct CRM value
  lookup can reveal whether configured customer-name variants are present in
  the cache.
- Treat config and profile tools as sensitive even when they do not include transcript text.
- Reserve record-reference tools for operator workflows that actually need exact calls.
- Reserve snippet and CRM-value lookup tools for narrowly scoped investigations with explicit user intent.
- Do not treat read-only MCP as a safe public endpoint; the host app inherits access to whatever each exposed tool returns.

## Default Posture And Optional Wider Surface

The defaults in this repo are shaped for an enterprise pilot where the operator
does not yet trust the host app or the model with broad tenant data. That
conservative posture is not the only supported posture: the same binaries
support a deliberately wider surface for trusted single-user analyst workflows
where deeper, identifier-bearing questions matter.

What the conservative defaults give you:

- In stdio mode, `gongmcp` exposes the full read-only catalog when no preset or
  allowlist is set, but most identifier-bearing fields are blanked, snippet
  tools redact call IDs and speaker IDs, and CRM-value lookups require explicit
  opt-in flags.
- Pilot deployments are expected to layer `--tool-preset business-pilot` or a
  custom allowlist on top so business users see only the approved subset rather
  than the full catalog.
- Company-managed `gongctl` jobs are expected to run with `GONGCTL_RESTRICTED=1`
  so high-risk raw API, raw call JSON, transcript export, and extended
  CRM-context flows fail closed unless the operator passes
  `--allow-sensitive-export`.

When opening up the surface is the right call:

- Single-user analyst on their own workstation, with their own Gong credentials,
  who needs exact call follow-up, named-opportunity attribution, or directed
  CRM-value lookup.
- A reviewed deep-dive session against a previously synced cache where the
  operator accepts that exact identifiers and bounded snippets will flow into
  the host model context.

How to open up the surface intentionally:

- In stdio mode, skip `--tool-preset` and `--tool-allowlist`, or use
  `--tool-preset all-readonly`, so the full read-only catalog is visible to the
  connected host. HTTP mode requires an explicit preset or allowlist; use
  `--tool-preset all-readonly` only for trusted admin/analyst sessions or
  fully reviewed filtered-DB deployments.
- Enable per-tool opt-ins when the question requires them:
  - `search_transcript_segments` with `include_call_ids=true` and
    `include_speaker_ids=true` returns exact identifiers alongside snippets.
  - `search_transcript_quotes_with_attribution` with the matching attribution
    flags returns Account/Opportunity context joined to the quote, plus
    `account_query` lookups.
  - `search_crm_field_values` with `include_call_ids=true` and
    `include_value_snippets=true` returns bounded value excerpts and call IDs
    for a specific object/field/value query.
- Use the record-reference tools (`search_calls`, `get_call`,
  `missing_transcripts`) when the workflow actually needs exact calls.
- Run `gongctl` without restricted mode, or with the `--allow-sensitive-export`
  override, for ad-hoc operator exploration that needs raw call JSON or
  transcript exports.

The trade-off is unchanged from the rest of this document: more useful answers
flow with more sensitive data. Pick a posture per deployment rather than per
prompt, and prefer to scope it through a named `--tool-preset` or custom
`--tool-allowlist` plus opt-in defaults rather than through ad-hoc host policy
alone.

## MCP Call Volume And Limits

`gongmcp` reads local SQLite. It does not call Gong. MCP traffic does not
consume the documented Gong API budget (about 3 calls per second and 10,000
calls per day) — that budget is spent by `gongctl sync ...` on the operator
side.

MCP traffic still has real per-call costs that scale poorly when an agent
loops:

- local SQLite I/O, especially full-text transcript searches against large
  caches
- wall-clock latency that compounds when the model fans out from one search
  result into per-call follow-up tools
- host model context tokens — every tool result chunk is added to the
  conversation, and snippet-bearing or identifier-bearing tools are the
  largest contributors
- host app billing or token quotas, which agents driving many MCP calls per
  turn can exhaust quickly

Server-enforced ceilings already in effect (see `internal/mcp/server.go`):

- maximum response frame of about 1 MiB
- search results capped at 100 rows
- missing-transcripts capped at 500 rows
- CRM field, lifecycle, and call-fact result sets capped at 100–200 rows
- inventory result sets capped at 200 rows
- call-detail object/field-name lists truncated to 20 objects and 50 field
  names

Practical recommendations:

- Pass tighter `limit` values when the question does not need the full cap.
- Start with aggregate tools (`summarize_calls_by_lifecycle`,
  `rank_transcript_backlog`, `summarize_call_facts`,
  `analyze_late_stage_crm_signals`) before reaching for identifier-bearing or
  snippet-bearing tools.
- Use `--tool-preset business-pilot`, `--tool-preset analyst`, or a custom
  `--tool-allowlist` to remove tools the host should not be reaching for
  reflexively in a given deployment lane. A narrow preset or allowlist is
  usually a better limit than relying on the agent to ration its own tool calls.
- Use `--tool-preset governance-search` only with raw-DB AI governance mode;
  for a physically filtered DB, choose the normal deployment preset instead.
- Avoid agent loops that call `search_transcript_segments` followed by
  `get_call` for every hit; the combined output is large in both context tokens
  and wall-clock time.
- If a host app drives many MCP-backed turns and triggers frequent
  `gongctl sync` runs in the background, cap the sync cadence separately so the
  daily Gong call budget is not consumed by per-session refreshes.

For company pilots, an explicit preset or allowlist plus a reviewed sync cadence
is usually the right pair of limits.

## Residual Risks

- Tool minimization reduces exposure, but it does not anonymize all tenant metadata.
- Stdio MCP has no built-in authentication; the trust boundary is the local
  machine and the connected host app.
- HTTP MCP can require bearer tokens, but it does not provide per-user identity,
  tenant separation, or an approval workflow.
- Redacted outputs can still be re-identifiable when combined with operator knowledge, timestamps, or external CRM context.
- Bounded snippets are still customer data and should be handled like restricted tenant content.
