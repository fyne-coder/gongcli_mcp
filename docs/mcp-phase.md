# MCP Phase Plan

## Phase 1: CLI

- Basic auth through `GONG_ACCESS_KEY` and `GONG_ACCESS_KEY_SECRET`.
- local SQLite cache for calls, users, transcripts, FTS, and sync state
- `sync calls`
- `sync users`
- `sync transcripts`
- `sync crm-integrations`
- `sync crm-schema`
- `sync settings`
- `sync status`
- `profile discover`
- `profile validate`
- `profile import`
- `profile show`
- `analyze calls`
- `analyze coverage`
- `analyze crm-schema`
- `analyze settings`
- `analyze scorecards`
- `analyze scorecard`
- `analyze transcript-backlog`
- `mcp tools`
- `mcp tool-info`
- `search transcripts`
- `search calls`
- `calls show --json`
- `auth check`
- `calls list`
- `calls export`
- `calls transcript`
- `calls transcript-batch`
- `users list`
- `api raw`
- retry, rate limiting, checkpointing, and JSONL exports

## Phase 2: Read-Only Local MCP

`cmd/gongmcp` is a read-only MCP server over the SQLite cache. Stdio remains
the default local/Desktop transport. A minimal HTTP `/mcp` request/response
transport is available for private company pilots when the operator supplies
bearer auth, an explicit tool allowlist, and a trusted TLS/proxy boundary. It
requires `--db PATH` and does not call Gong directly.

Implemented tools are boring and auditable:

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

Guardrails:

- no raw Gong API tool
- no arbitrary SQL tool
- no profile import or mutation through MCP
- no raw cached call JSON through MCP; `get_call` returns minimized metadata and CRM field names/counts only
- transcript search returns snippets only, not full transcript text
- CRM population matrices only group by allowlisted categorical fields such as `StageName`
- `search_crm_field_values` is the only CRM-value lookup tool; it requires a specific object type, field name, and query, redacts call IDs by default unless `include_call_ids=true`, and returns bounded short snippets plus call titles only with `include_value_snippets=true`
- lifecycle tools use an imported profile when active, otherwise the builtin compatibility lifecycle view
- every profile-aware lifecycle/fact response reports the lifecycle source and profile provenance
- profile-aware fact/lifecycle tools read from a materialized SQLite profile cache warmed by writable CLI sync/profile commands; read-only MCP reports stale cache state instead of writing to SQLite
- `summarize_call_facts` only allows safe business grouping dimensions through MCP; directed CRM value lookups go through `search_crm_field_values`
- lifecycle CRM comparison requires an explicit CRM `object_type`
- call-fact summaries use an allowlisted normalized view instead of arbitrary SQL or raw JSON exposure
- CRM schema/settings tools return cached metadata only, not raw settings payloads or CRM values; scorecard detail exposes question text as configuration metadata
- unmapped CRM field tools return redacted aggregate statistics only, not raw field example values
- `search_transcript_segments` returns bounded snippets, but redacts call IDs and speaker IDs by default; exact IDs require `include_call_ids=true` and `include_speaker_ids=true`
- `get_sync_status` includes public readiness flags and separates embedded CRM context from CRM integration/schema inventory so zero integration inventory is not mistaken for missing call CRM context
- `rank_transcript_backlog` and `prioritize_transcripts_by_lifecycle` favor External and Conference-style customer conversations and redact call IDs/titles in model-facing MCP output
- Opportunity aggregate tools redact opportunity IDs/names, owner IDs, amounts, close dates, and latest call IDs in model-facing MCP output; use CLI/operator workflows for exact local follow-up.

## Phase 3: Hosted/Managed Remote MCP

Phase 2 already includes the current private HTTP pilot. This phase is future
work for a hosted or centrally managed remote MCP layer with user and tenant
management. Only after the local/private-pilot MCP shape is stable:

- decide auth boundary explicitly
- keep browser/session auth separate from agent-client auth
- avoid default prompt injection of full transcripts
- prefer resource handles or local files for large transcript bodies

## Non-Goals

- No Gong credential sharing.
- No undocumented endpoints.
- No analysis pipeline embedded in the CLI.
- No customer data fixtures in the repo.
