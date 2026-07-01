# Analyst Tool Reference

Quick reference for the analyst-facing surfaces of `gongmcp`:

- the `business-workbench` facade (six stable tools, recommended for client
  hosts)
- the `analyst` / `analyst-core` / `analyst-business-core` direct-tool
  presets (named tools, used inside the facade and exposed directly for
  trusted analyst sessions)
- the `call_filter` allowlist used by every cohort, search, and analysis tool

For setup, profile design, and recommended tool sequences by analytic intent,
see [Analyst orientation](analyst-orientation.md). For the full data
exposure model, see [MCP data exposure](mcp-data-exposure.md). The fuller
analyst cohort workflow (with worked examples) lives in
[Pilot sponsor and operator guide](pilot-sponsor-and-operator-guide.md#analyst-cohort-workflow).

## `business-workbench` facade tools

Six stable tool names. The facade routes internally to the analyst
operations below; the host sees only the six names and so client integrations
do not break when the routed tool changes.

| Facade tool | Purpose |
|---|---|
| `gong_status` | Cache and sync status (`status.sync` → `get_sync_status`) |
| `gong_discover_capabilities` | Returns the operation registry, including the routed tool, exposure level, allowed presets, input schema, examples, and whether the routed tool is currently exposed |
| `gong_query` | Bounded query operations |
| `gong_analyze` | Bounded analysis operations |
| `gong_get_evidence` | Bounded evidence operations |
| `gong_explain_limitations` | Cache / governance / facade limitations summary |

### `gong_query` operations

| Operation | Routes to | What it returns |
|---|---|---|
| `query.calls` | `search_calls_by_filters` (fallback `search_calls`) | Bounded call rows matching a `call_filter` |
| `query.call_count` | internal `call_count` handler | Exact call count for a bounded filter without row payload |
| `query.dimension_counts` | internal `dimension_counts` handler | Per-bucket call counts for a backed summarize dimension inside a bounded cohort; supports participant_domain, participant_affiliation, and participant_email ranking by default, with request-level internal_domains, process-level `GONGMCP_INTERNAL_PARTICIPANT_DOMAINS` / `--internal-participant-domains`, participant_affiliation_filter, and `hide_contact_emails` as the privacy switch for disabling raw email buckets |
| `query.transcript_segments` | `search_transcript_segments` (fallback `search_transcripts_by_filters`) | Bounded transcript snippets |
| `query.scorecards` | `list_scorecards` | Scorecard inventory |
| `query.scorecard_detail` | `get_scorecard` | Scorecard configuration detail |

### `gong_analyze` operations

| Operation | Routes to | What it returns |
|---|---|---|
| `analyze.cohort.build` | `build_call_cohort` | Stateless cohort definition + count from a `call_filter` |
| `analyze.cohort.inspect` | `inspect_call_cohort` | Cohort coverage detail before analysis |
| `analyze.themes.discover` | `discover_themes_in_cohort` | Cache-derived theme signals for a cohort |
| `analyze.limitations.explain` | `explain_analysis_limitations` | Why a cohort can or cannot support a given question |
| `theme_intelligence_report` | (typed report handler) | End-to-end theme report for a cohort. Without a `theme_query` seed, returns Gong AI candidate themes; with a seed, returns a customer-facing-quality theme intelligence report. Returns `needs_theme_seed` when no candidate survives. |
| `question.answer` | (governed evidence-pack handler) | Ad-hoc business question. Returns interpreted question, scope, coverage, reviewed calls with stable `call_ref` values, bounded evidence/quotes, warnings, limitations, and an answer contract for the host model. Does **not** generate prose inside `gongmcp`. |
| `prospect.question.answer` | (account-scoped evidence-pack handler) | Ad-hoc business question scoped to a named account/prospect. Caller must provide the account identifier. |

### `gong_get_evidence` operations

| Operation | Routes to | What it returns |
|---|---|---|
| `evidence.quotes.search` | `search_quotes_in_cohort` (fallback `search_transcript_quotes_with_attribution`) | Bounded quote candidates |
| `evidence.quote_pack.build` | `build_quote_pack` | Bounded representative excerpts packaged for sales/marketing review |
| `evidence.highlights.list` | internal `list_call_ai_highlights` handler | Typed Gong AI highlights for **explicit** `call_ids` only. Never raw highlight JSON. No account/customer enumeration. Result envelope flags highlights as accelerators; transcript quotes remain primary evidence. |

For the source-of-truth list per release, call:

```text
gong_discover_capabilities
```

over MCP from your host. It returns the live registry with input schemas,
examples, and per-operation `routed_tool_available` flags.

## `call_filter`

Every cohort, search, and analysis tool accepts only these filter fields:

| Field | Type | Notes |
|---|---|---|
| `title_query` | string | Substring match on call title. Title-bearing surfaces return call titles by default unless `field_profile=limited` or `hide_call_titles` suppresses them. |
| `query` | string | Free-text query against searchable fields |
| `from_date` | `YYYY-MM-DD` | Inclusive lower bound |
| `to_date` | `YYYY-MM-DD` | Inclusive upper bound |
| `quarter` | e.g. `Q1 2026` | Convenience window; mutually exclusive with `from_date`/`to_date` |
| `lifecycle_bucket` | one of `open`, `closed_won`, `closed_lost`, `post_sales`, `unknown` | Requires an active profile or builtin lifecycle compatibility |
| `scope` | string | Profile-defined scope tag |
| `system` | string | Profile-defined system tag |
| `direction` | `inbound`/`outbound` | Call direction |
| `transcript_status` | `present`/`absent` | Used to narrow to calls with usable transcript evidence |
| `industry` | string | From cached CRM account industry; coverage varies |
| `account_query` | string | Substring match on cached account name |
| `opportunity_stage` | string | Raw CRM stage value |
| `crm_object_type` | e.g. `Account`, `Opportunity` | Constrain to a CRM record |
| `crm_object_id` | string | Specific CRM record |
| `participant_title_query` | string | Substring match on participant title |
| `dimension_filters` | array | Filters over backed business-analysis fields and dimensions. String fields use `equals`/`in`; numeric fields such as `duration_seconds` also support `gte`/`lte`/`between`. The default disallow list is empty. |
| `limit` | integer | Per-tool result cap. Always provide. For `query.calls`, this caps returned preview rows only; `count` and `coverage_summary.call_count` still describe the full matched cohort. |

`dimension_filters` entries use:

```json
{
  "dimension": "account_revenue_range",
  "operator": "in",
  "values": ["C: MM", "D: ENT"]
}
```

Other common `dimension_filters` dimensions include `duration_seconds`,
`participant_email`, `account_name`, `crm_object_id`, `persona`, `loss_reason`,
and `won_lost`. Participant email and identifier filters narrow the call set;
returned fields still follow the tool's normal output policy behavior.

Deployments may also expose governed CRM dimensions promoted from cached Gong
CRM context, for example `account_rating`. Use only dimensions
advertised by `gong_discover_capabilities`; the default promoted set is
a fixed reviewed standard Account/Opportunity mapping set rather than a
generic all-CRM-field abstraction. Numeric and date CRM fields are groupable
through bucket, month, or quarter dimensions instead of their raw values, and
tenant-specific lifecycle or methodology concepts still belong in reviewed
business profiles.

Multiple entries are combined with AND semantics; values inside one `in` entry
are OR alternatives. `quarter` values must use `YYYY-Q#`, and `call_month`
values must use `YYYY-MM`. The server accepts backed business-analysis fields
and dimensions unless an operator policy disallows them; this package ships
with an empty disallow list. `gong_discover_capabilities` advertises the
current backed set and operator support. Use the `query` and
`transcript_query` fields for transcript text search; `dimension_filters`
remain structured field predicates.

### Using `limit` deliberately

Every tool result becomes model context. A 1,000-row return from a wide
filter usually produces a worse answer than a 50-row return from a tight
filter.

- If a tool returns `capped: true`, do **not** just bump `limit` — narrow
  the filter first.
- For scalar questions such as "how many calls were longer than 5 minutes",
  use `query.call_count`. `query.calls` is for inspecting bounded call rows;
  its `limit` controls only the returned preview rows, not the matched
  cohort count.
- Prefer a concrete date window plus one or more of `lifecycle_bucket`,
  `scope`, `system`, `direction`, `crm_object_type`/`crm_object_id`, or a
  more specific `query`/`title_query`.
- Operators can configure higher MCP row caps for analyst or trusted-admin
  deployments via `GONGMCP_MAX_*` env vars or `gongmcp --max-*` flags;
  every family has a hard ceiling and `tools/list` reflects the active
  maximum.

## Direct-tool presets (analyst surfaces)

When the operator widens the surface beyond the facade, you may see these
named tools directly. Source of truth: `gongmcp --list-tool-presets`.

| Group | Tools | Use |
|---|---|---|
| Cohort | `build_call_cohort`, `inspect_call_cohort`, `list_call_cohorts`, `compare_call_cohorts` | Create reproducible call sets, inspect coverage, compare slices |
| Generic filter / search | `search_calls_by_filters`, `summarize_calls_by_filters`, `search_transcripts_by_filters` | Bounded calls, metadata summaries, snippets |
| Themes | `discover_themes_in_cohort`, `summarize_themes_by_dimension`, `compare_themes_over_time`, `compare_themes_by_segment` | Surface deterministic cache-derived theme signals |
| Quotes / evidence | `extract_theme_quotes`, `search_quotes_in_cohort`, `rank_quotes_for_sales_use`, `build_quote_pack` | Bounded representative excerpts; package evidence |
| Outcome / pipeline | `compare_theme_outcomes`, `summarize_pipeline_progression_by_theme`, `summarize_loss_reasons_by_theme`, `compare_won_lost_theme_patterns` | Compare theme presence to cached CRM progression and outcome fields |
| Persona / industry | `summarize_themes_by_persona`, `summarize_themes_by_industry`, `rank_personas_by_insight_quality`, `diagnose_attribution_coverage` | Persona/industry patterns and missing-attribution coverage |
| Sales / marketing synthesis | `generate_sales_hooks_from_themes`, `generate_outreach_sequence_inputs`, `recommend_target_personas_and_industries`, `build_theme_brief` | Structured inputs for the host model to turn into collateral |
| Coverage / quality | `score_cohort_evidence_quality`, `explain_analysis_limitations`, `suggest_filter_refinements` | Grade whether the cohort can support the requested answer |

## Recommended tool sequences

See [Analyst orientation §Recommended tool sequences by analytic
intent](analyst-orientation.md#recommended-tool-sequences-by-analytic-intent)
for the four canonical sequences (theme discovery, quote-pack, won/lost,
coverage) with the equivalent `gong_*` facade calls for each.
