# Business User Guide

This guide is for the business-user lane of the `Enterprise Pilot Review Packet`.
It applies only to reviewed pilot use through `gongmcp` over a prebuilt local
cache.

Business users should not run `gongctl`, should not receive Gong credentials,
and should not be asked to manage sync jobs, profile imports, raw exports, or
local database files. Those workflows stay with the pilot operator.

## Who This Guide Is For

- Business users who need read-only answers from approved MCP tools.
- Pilot sponsors who need to understand what business users may ask.
- Security, RevOps, or IT reviewers who need the business boundary in one place.

## Operating Boundary

- Business users interact with a host application connected to `gongmcp`.
- `gongmcp` reads a reviewed SQLite cache only; it does not call Gong live.
- `gongmcp` can enforce a reviewed server-side tool subset through
  `--tool-allowlist` or `GONGMCP_TOOL_ALLOWLIST`. If those are not set, the
  full read-only catalog remains visible to the connected host.
- Results reflect the last approved sync and profile state, not current tenant
  state.
- Outputs must stay aggregate-first, metadata-oriented, and bounded.
- Business users must not request or receive Gong credentials, raw API access,
  transcript files, raw cached JSON, or direct filesystem/database access.

## Participant Roles

- Pilot sponsor: owns the business questions, approves the pilot scope, and
  decides whether the answers are useful enough to continue.
- Pilot operator: runs `gongctl`, manages credentials, refreshes the cache,
  validates profile state, and exposes only the approved MCP tool set through
  `gongmcp` allowlisting plus any host-side policy needed for the pilot.
- Security or RevOps reviewer: confirms acceptable-use boundaries, storage
  location, retention, and tool allowlist before business access starts.
- Business user: asks approved business questions through the host application
  and escalates anything outside scope instead of trying to bypass controls.

## Approved Business Prompts

Use prompts shaped like these:

- "Summarize conversation volume by lifecycle, scope, or direction for the
  reviewed pilot dataset."
- "Where is transcript coverage weakest by lifecycle or call type?"
- "Which lifecycle buckets or business segments have the largest missing
  transcript backlog?"
- "What scorecards are available in the reviewed cache, and what questions do
  they contain?"
- "Show a metadata-only rollup of calls by month, duration bucket, transcript
  status, or forecast category."
- "Ask the operator to compare separate reviewed `sync status` snapshots when a
  before-and-after refresh comparison is needed."
- "What business-ready signals are blocked because the cache, profile, or
  transcript coverage is incomplete?"

These prompts are in-bounds because they stay on reviewed cached metadata,
bounded scorecard configuration, and backlog prioritization.

## Disallowed Prompts

Do not use prompts like these:

- "Give me the full transcript."
- "Show the raw call JSON, raw API payload, or export file."
- "List customer names, tenant names, exact object IDs, call IDs, or direct
  participant records."
- "Search raw CRM values for a named account, opportunity, or person."
- "Pull the latest data from Gong right now."
- "Give me the database path, transcript directory, or operator config."
- "Import or edit the business profile."
- "Tell me which credentials to use or paste the access key/secret."
- "Judge an individual employee, rep, or manager from a single call."

If a user needs one of these workflows, stop and route it to the pilot operator
or sponsor for a separate review.

## Approved MCP Tools For Business Users

The pilot tool set should stay narrow. Configure native `gongmcp` tool
allowlisting for the deployment and keep host prompts or wrapper policy aligned
with the same approved set.

- `get_sync_status`
- `summarize_call_facts`
- `summarize_calls_by_lifecycle`
- `rank_transcript_backlog`
- `list_scorecards`
- `get_scorecard`

Why this allowlist:

- It answers the core pilot questions about coverage, lifecycle mix, backlog,
  and available coaching surfaces.
- It stays on cached metadata and scorecard configuration rather than exact
  records, raw transcript content, or directed CRM value lookup.
- `list_scorecards` and `get_scorecard` may expose stable scorecard,
  workspace, or question IDs as configuration metadata. Reviewers must approve
  that exposure before enabling scorecard inventory workflows.
- It keeps business users away from tools that can expose tenant-specific schema
  details, exact calls, or sensitive search pivots.

## MCP Tools Not Approved For Business Users

Do not expose these tools to business users during the pilot:

- `search_calls`
- `get_call`
- `missing_transcripts`
- `list_crm_object_types`
- `list_crm_fields`
- `list_crm_integrations`
- `list_cached_crm_schema_objects`
- `list_cached_crm_schema_fields`
- `list_gong_settings`
- `get_business_profile`
- `list_business_concepts`
- `list_unmapped_crm_fields`
- `search_crm_field_values`
- `analyze_late_stage_crm_signals`
- `opportunities_missing_transcripts`
- `search_transcripts_by_crm_context`
- `search_transcripts_by_call_facts`
- `opportunity_call_summary`
- `crm_field_population_matrix`
- `list_lifecycle_buckets`
- `search_calls_by_lifecycle`
- `prioritize_transcripts_by_lifecycle`
- `compare_lifecycle_crm_fields`
- `search_transcript_segments`

These tools are operator-only or expansion-candidate tools because they can
reveal tenant structure, allow directed value lookup, or move too close to
exact-call review for an initial business pilot.

## Cache Freshness Caveats

- The MCP server is not a live Gong connection. A business answer can be stale
  even when the host app is working correctly.
- `get_sync_status` should be the first check in every business-user session.
- `get_sync_status` can include recommended operator commands such as sync or
  profile actions. Business users should treat those as handoff instructions for
  the pilot operator, not commands to run themselves.
- If the cache age, transcript coverage, settings inventory, or profile cache
  state is not acceptable for the question, stop and ask the operator to
  refresh or review before using the answer.
- Lifecycle answers are only as reliable as the reviewed profile and synced CRM
  context. If profile-backed lifecycle status is absent or stale, treat the
  answer as directional rather than authoritative.
- Missing data should be reported as a pilot limitation, not silently filled in
  by inference.

## Acceptable-Use Boundaries

- Use the pilot for aggregate business review, coverage gaps, scorecard
  inventory, and transcript-backlog prioritization.
- Treat all outputs as internal reviewed pilot material.
- Keep prompts and downstream notes free of secrets, raw transcript text,
  private file paths, and direct identifiers.
- Do not use the pilot for HR, compensation, disciplinary, or legal decisions.
- Do not use the pilot to monitor or rank individuals from raw call-level
  evidence.
- Do not use the pilot output as the sole basis for pipeline, forecast, or
  customer-commitment decisions without operator review.

## Dataset Limits For Business Users

Business-user access should remain within a reviewed pilot slice:

- One approved tenant only.
- One approved business unit or call-program slice at a time.
- A time window small enough for manual sponsor review, usually the most recent
  30 to 90 days.
- Cached metadata, transcript status, and scorecard inventory only through the
  approved allowlist above.

If the business asks for broader history, multi-tenant comparison, or exact
call-level follow-up, treat that as an expansion request rather than silently
widening the pilot.

## Out Of Scope

- Running `gongctl` directly.
- Receiving or storing Gong credentials.
- Live Gong API pulls from the business-user host.
- Raw transcript review or transcript export.
- Raw CRM value mining.
- Tenant schema discovery for general exploration.
- Profile authoring, validation, or import.
- Full audit, compliance, legal hold, or eDiscovery workflows.
