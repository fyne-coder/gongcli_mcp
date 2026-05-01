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
  `--tool-preset business-pilot`, `GONGMCP_TOOL_PRESET=business-pilot`, or a
  custom allowlist. If no preset or allowlist is set, the full read-only catalog
  remains visible to stdio hosts.
- Results reflect the last approved sync and profile state, not current tenant
  state.
- Outputs must stay aggregate-first, metadata-oriented, and bounded.
- Business users must not request or receive Gong credentials, raw API access,
  transcript files, raw cached JSON, or direct filesystem/database access.
- The default business-user tool preset is `business-pilot` from
  [Customer implementation checklist](implementation-checklist.md#named-tool-profiles).
  Wider presets such as `analyst`, `governance-search`, and `all-readonly`
  require operator/sponsor approval and are not the business-user default.

For the first-session handoff, use
[Business User First 10 Minutes](implementation-checklist.md#business-user-first-10-minutes).

## Participant Roles

- Pilot sponsor: owns the business questions, approves the pilot scope, and
  decides whether the answers are useful enough to continue.
- Pilot operator: runs `gongctl`, manages credentials, refreshes the cache,
  validates profile state, and exposes only the approved MCP tool set through
  `gongmcp` allowlisting plus any host-side policy needed for the pilot.
- Security or RevOps reviewer: confirms acceptable-use boundaries, storage
  location, retention, and tool preset/allowlist before business access starts.
- Business user: asks approved business questions through the host application
  and escalates anything outside scope instead of trying to bypass controls.

## Approved Business Prompts

Use prompts shaped like these:

- "Summarize conversation volume by lifecycle, scope, or direction for the
  reviewed pilot dataset."
- "Where is transcript coverage weakest by lifecycle or call type?"
- "Which lifecycle buckets or business segments have the largest missing
  transcript backlog?"
- "Show a metadata-only rollup of calls by month, duration bucket, transcript
  status, or forecast category."
- "Ask the operator to compare separate reviewed `sync status` snapshots when a
  before-and-after refresh comparison is needed."
- "What business-ready signals are blocked because the cache, profile, or
  transcript coverage is incomplete?"

These prompts are in-bounds because they stay on reviewed cached metadata and
backlog prioritization.

Scorecard inventory is optional, not part of the default strict pilot lane. Add
`list_scorecards` and `get_scorecard` only after the customer approves exposure
of coaching configuration, scorecard question text, and stable scorecard
metadata.

## Analyst Expansion Prompts

The prompts in the previous section are deliberately thin so they fit the
strict pilot allowlist. Real business work usually needs more structure: a
specific time window, prospect-side filtering, a required output shape, and an
explicit separation between evidence-backed findings and hypotheses.

The four templates below are not strict-pilot prompts. Use them only in an
approved analyst expansion where the operator has widened the MCP tool surface
beyond the strict business-user allowlist. They go beyond what the pilot
allowlist exposes by design, and each one names the additional tools and opt-in
flags it requires. See
[mcp-data-exposure.md](mcp-data-exposure.md#default-posture-and-optional-wider-surface)
for how to enable that wider posture intentionally.

### 1. Content gap discovery from prospect questions

Business intent: surface where prospects are repeatedly asking the same thing
on calls, and turn that into concrete recommendations for the website, nurture
sequences, or sales-enablement collateral.

Prompt:

> Use Gong to answer: What prospect questions in Q1 2026 indicate gaps in
> website, nurture, or sales enablement content?
>
> Only include prospect/customer-side questions where possible. For each gap
> category, provide:
>
> 1. exact question pattern
> 2. matching segment count
> 3. unique call count
> 4. top 5 representative quotes
> 5. call/company/contact/title if available
> 6. lifecycle/stage if available
> 7. confidence level
> 8. recommended content asset
>
> Separate evidence-backed findings from hypotheses. Do not claim an asset is
> missing unless the transcript evidence supports it; flag asset gaps as
> "possible" when the evidence only suggests a direction.

Tools required: `search_transcript_quotes_with_attribution` (with
`include_call_ids=true`, `include_call_titles=true`, and the matching
account/opportunity opt-ins when attribution is needed),
`search_transcript_segments`, `summarize_call_facts`, `get_sync_status`.

Output discipline: reject any "asset is missing" claim that does not list at
least the matching segment count, the unique call count, and a quote. Treat
contact/title as present only when the attribution tool reports
person-title status as available; never infer titles from call names.

### 2. Recurring objection mining for coaching playbook updates

Business intent: identify the top recurring prospect or customer objections by
lifecycle/segment over a recent window, decide which ones are already covered
by existing scorecard questions, and flag the ones that are not.

Prompt:

> Using cached Gong calls from the last 90 days, list the top recurring
> prospect or customer objections by lifecycle and segment. For each objection
> theme, provide:
>
> 1. theme label and one-sentence description
> 2. matching segment count and unique call count
> 3. top 5 representative customer-side quotes
> 4. lifecycle bucket and (if present) opportunity stage
> 5. which existing scorecard questions already address it
> 6. whether existing coaching coverage looks sufficient, partial, or missing
> 7. confidence level (low / medium / high)
>
> Treat coverage as "missing" only when no scorecard question matches the
> theme; treat it as "partial" when a scorecard question matches but does not
> appear in the rep-side responses on the same calls.

Tools required: `search_transcript_segments`, `search_transcripts_by_call_facts`,
`summarize_calls_by_lifecycle`, `list_scorecards`, `get_scorecard`,
`get_sync_status`. Add `include_call_ids=true` only if the operator needs to
follow up on individual calls.

Output discipline: treat "rep-side responses absent" as a transcript-coverage
question first; if scorecard tagging is missing because transcripts are not
synced, flag it instead of inferring sufficiency from metadata alone.

### 3. Renewal and expansion intent vs. churn risk

Business intent: for customer success leaders, separate post-sales calls that
show expansion or renewal intent from calls that show churn risk, and produce
a per-account briefing using only what is in the cached transcripts.

Prompt:

> For accounts in the renewal, upsell/expansion, or customer-success
> lifecycle buckets in the last 90 days, classify each account into one of:
> renewal-likely, expansion-signal, at-risk, or insufficient-evidence. For
> each account in the first three buckets, provide:
>
> 1. account name and (if cached) opportunity name and close date
> 2. matching segment count and unique call count
> 3. top 3 customer-side quotes that drove the classification
> 4. lifecycle bucket and lifecycle source (profile or builtin)
> 5. confidence level
> 6. recommended next step (executive review, expansion play, save play,
>    or "needs more transcript coverage")
>
> Place every account with fewer than two cached transcripts in the
> insufficient-evidence bucket regardless of metadata signals. Do not infer
> sentiment from call titles, durations, or scorecard scores alone.

Tools required: `search_calls_by_lifecycle`, `summarize_calls_by_lifecycle`,
`search_transcript_quotes_with_attribution` (with the Account and Opportunity
attribution opt-ins enabled), `opportunity_call_summary`, `get_sync_status`.
Imported business profile recommended; lifecycle answers from the builtin
compatibility view should be flagged as directional.

Output discipline: every classification must cite at least two customer-side
quotes from at least two distinct calls; otherwise the account drops into
insufficient-evidence rather than getting a soft label.

### 4. Late-stage pipeline risk from thin transcript evidence

Business intent: for RevOps or pipeline review, list late-stage opportunities
whose transcript coverage is too thin to support a confident forecast, and
quote the small amount of evidence that does exist so the deal review has
something to react to.

Prompt:

> List the late-stage opportunities (commit, best case, or equivalent) with
> the weakest transcript coverage in the cached dataset. For each
> opportunity, provide:
>
> 1. opportunity name, account name, stage, amount, and close date if cached
> 2. cached call count, transcript count, and total transcript minutes
> 3. days since the most recent call
> 4. up to 5 representative customer-side quotes that exist
> 5. risk drivers from the late-stage CRM signal analysis
> 6. confidence level for the forecast given the available evidence
> 7. recommended next step (operator transcript refresh, executive sponsor
>    call, deal-desk review, or "evidence sufficient")
>
> Only mark "evidence sufficient" if there are at least two calls with
> cached transcripts in the last 30 days and at least one customer-side
> quote covering pricing, decision criteria, or timing. Otherwise mark it as
> needing operator refresh and name the specific sync command the operator
> should run.

Tools required: `analyze_late_stage_crm_signals`,
`opportunities_missing_transcripts`, `opportunity_call_summary`,
`search_transcript_quotes_with_attribution` (with Opportunity attribution
opt-ins), `rank_transcript_backlog`, `get_sync_status`. The operator must have
enabled the wider analyst posture for these tools to be available.

Output discipline: do not turn missing transcript coverage into a forecast
recommendation by itself; treat it as a refresh request to the operator and a
deal-desk review trigger.

## Ad-Hoc Analysis And Gong AI Loop

Use `gongctl` as the analysis lab and Gong as the production conversation
intelligence system.

The local workflow is useful when the business question is not yet clean enough
for Gong-native configuration:

- the team is still discovering which prospect questions, objections, or buying
  signals recur often enough to deserve a tracker, theme, scorecard, or Gong AI
  prompt
- the analyst needs exact evidence counts, quote samples, transcript coverage,
  and CRM/lifecycle slices before trusting an AI summary
- the question cuts across Gong surfaces, such as transcript snippets plus
  lifecycle state plus scorecard configuration
- the answer needs to separate evidence-backed findings from hypotheses before
  it becomes a stakeholder-facing summary

Recommended loop:

1. Start with `get_sync_status` and coverage checks so the model knows what the
   cache can and cannot support.
2. Run a narrow ad-hoc analysis with bounded transcript or attribution tools,
   using a defined date window, lifecycle bucket, segment, and output shape.
3. Save the useful pattern as a candidate business definition: signal name,
   positive examples, negative examples, required quote evidence, and any CRM
   or lifecycle filters.
4. Convert that candidate into the right Gong-native artifact: tracker,
   scorecard question, call category, theme, or Gong AI prompt.
5. Use Gong AI against the improved native setup, then compare its output
   against the local evidence counts and quote samples before treating it as
   reliable.

This prevents a common failure mode: asking Gong AI a broad question before the
underlying trackers, scorecards, themes, or coverage expectations are clear.
`gongctl` should produce the evidence and definitions that make the Gong-native
AI question sharper.

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

Optional after reviewer approval:

- `list_scorecards`
- `get_scorecard`

Why this allowlist:

- It answers the core pilot questions about coverage, lifecycle mix, backlog,
  and available coaching surfaces.
- It stays on cached metadata and scorecard configuration rather than exact
  records, raw transcript content, or directed CRM value lookup.
- `list_scorecards` and `get_scorecard` may expose stable scorecard,
  workspace, question text, or question IDs as configuration metadata. Enable
  them only when the pilot checklist includes "coaching configuration exposure
  approved."
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
- `search_transcript_quotes_with_attribution`
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

`search_transcript_quotes_with_attribution` is the right tool for marketing
asks like “top quotes by Q1 theme, industry, and opportunity stage.” It returns
bounded snippets plus available CRM attribution. Contact/person title should be
used only when the tool reports it as present; do not infer titles from call
names or transcript wording.

## Cache Freshness Caveats

- The MCP server is not a live Gong connection. A business answer can be stale
  even when the host app is working correctly.
- `get_sync_status` should be the first check in every business-user session.
- `get_sync_status` can include recommended operator commands such as sync or
  profile actions. Business users should treat those as handoff instructions for
  the pilot operator, not commands to run themselves.
- If the cache age, transcript coverage, settings inventory, or profile cache
  state is not acceptable for the question, stop and ask the operator to
  refresh or review before using the answer. The operator-side refresh
  procedure lives in [runbooks/operator-sync.md](runbooks/operator-sync.md);
  business users should not run those commands themselves.
- Lifecycle answers are only as reliable as the reviewed profile and synced CRM
  context. If profile-backed lifecycle status is absent or stale, treat the
  answer as directional rather than authoritative.
- Missing data should be reported as a pilot limitation, not silently filled in
  by inference.

Status interpretation:

- Cache stale: stop and ask the pilot operator to refresh the reviewed cache.
- Profile stale or inactive: lifecycle and attribution answers are directional
  only.
- Transcript coverage low: do not draw quote-based conclusions.
- Tool unavailable: the tool is not approved for this pilot lane.
- Unexpected sensitive output: stop the session, do not paste the output
  elsewhere, and notify the pilot operator or security contact with the prompt,
  approximate time, and tool if the host exposes it.

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
