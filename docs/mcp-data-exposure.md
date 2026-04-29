# MCP Data Exposure

## Scope

This document describes the data exposure contract for the local `gongmcp` stdio server as implemented in `internal/mcp/server.go` on 2026-04-29.

Current fixed boundaries:

- MCP reads a local SQLite cache only.
- MCP does not call Gong live.
- `gongmcp --tool-allowlist` and `GONGMCP_TOOL_ALLOWLIST` can reduce the exposed tool surface; when neither is set, the full read-only catalog remains available.
- MCP does not expose raw Gong API passthrough, arbitrary SQL, raw cached call JSON, profile import, or full transcript dumps.

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
- In company deployments, set a server-side tool allowlist instead of relying on host prompts alone.
- Treat config and profile tools as sensitive even when they do not include transcript text.
- Reserve record-reference tools for operator workflows that actually need exact calls.
- Reserve snippet and CRM-value lookup tools for narrowly scoped investigations with explicit user intent.
- Do not treat read-only MCP as a safe public endpoint; the host app inherits access to whatever each exposed tool returns.

## Residual Risks

- Tool minimization reduces exposure, but it does not anonymize all tenant metadata.
- The MCP server has no built-in authentication, tenant separation, or approval gate; the trust boundary is the local machine and the connected host app.
- Redacted outputs can still be re-identifiable when combined with operator knowledge, timestamps, or external CRM context.
- Bounded snippets are still customer data and should be handled like restricted tenant content.
