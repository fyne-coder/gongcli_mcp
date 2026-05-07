# Postgres Client Manual-Test Checklist

Use this checklist after the operator has deployed the controlled Postgres pilot
and before a client-facing walkthrough. It is written for a reviewed
customer-hosted deployment using the redacted serving database and scoped
reader role. The recommended default for client MCP hosts is the
`business-workbench` preset (six stable facade tools; routed internally
through the reviewed analyst operation set). The broader `analyst-expansion`
preset remains available for trained analyst sessions over the 68-tool
surface, but should not be the default for new client deployments. Internal
broad-search lab testing can use `redacted-all-readonly` after the same
redacted serving DB and scoped-grant checks pass; it is internal-only and is
not a client-facing surface.
Deployment steps live in the
[Postgres client deployment runbook](runbooks/postgres-client-deployment.md).
Use the
[Postgres question-parity matrix](postgres-question-parity.md)
to decide whether a manual-test prompt should be marked supported, caveated, or
blocked.

Do not paste passwords, database URLs, raw transcripts, restricted customer
names, or whole tool transcripts into this checklist. Record pass/fail status,
counts, tool names, and reviewed evidence paths only.

## 1. Preconditions

- MCP URL is HTTPS and ends in `/mcp`.
- Authentication is enabled through the approved gateway.
- `gongmcp` receives only the redacted serving DB reader URL.
- `GONGMCP_TOOL_PRESET=business-workbench` for the recommended client
  business-user surface (six facade tools), `analyst-expansion` for trained
  analyst manual testing over the broader reviewed surface,
  `redacted-all-readonly` for internal broad-search testing only, or
  `broad-public-redacted` for customer pilots over a physically redacted
  serving DB with the blocklist enforced and the customer-policy switch
  contract.
- `GONGMCP_TRANSCRIPT_EVIDENCE_PROVENANCE=redacted` for client analyst testing,
  or `raw` for internal redacted-DB broad-search testing when exact call IDs are
  needed.
- `GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS=1`.
- `GONGMCP_POSTGRES_REDACTED_SERVING_DB=1`.
- `GONGMCP_DEPLOYMENT_ID` is set to a non-secret rollout label, image digest,
  or deployment ticket so `gong_status`, `get_sync_status`,
  `gong_discover_capabilities`, and `/healthz` can prove which MCP server a
  client is actually using.
- `all-readonly`, `all-tools`, and `all` remain rejected for Postgres.
- Operator has run the deployment smoke and stored sanitized evidence.
- Current cache counts are known. For the internal May 6, 2026 lab baseline:
  about `4,803` calls, `4,803` transcripts, and `0` missing transcripts.

## 2. Allowed Manual-Test Surface

For the recommended client-facing manual-test lane, the expected public preset
is `business-workbench`. It exposes only the six stable facade tools
(`gong_status`, `gong_discover_capabilities`, `gong_query`, `gong_analyze`,
`gong_get_evidence`, `gong_explain_limitations`) and routes internally through
the reviewed analyst operation set plus the typed AI-highlights handler.
`analyst-facade` and `facade-analyst` remain accepted as backwards-compatible
aliases.

For ad-hoc prompts, start with `gong_analyze` operation `question.answer`
instead of asking the model to manually compose several lower-level
operations. The response is an evidence pack for synthesis: searched scope,
coverage, reviewed calls, per-call duration, bounded quotes/evidence,
warnings, limitations, and suggested follow-ups. It intentionally does not
return unrestricted call titles in scoped Postgres mode; titles can contain
customer names. Use `call_ref` plus Gong brief/highlight rows and transcript
quotes as the stable identifier path.

For trained analyst manual testing over the broader 68-tool surface, the
`analyst-expansion` preset (an alias for `analyst`) remains available. Prefer
`business-workbench` for client business-user deployments so the host sees a
small, stable tool list while reviewed operations continue to evolve
underneath.

For internal redacted-DB broad testing, `redacted-all-readonly` exposes every
reviewed Postgres-readable MCP tool, including `search_calls`, `get_call`,
`search_crm_field_values`, CRM/settings inventory, scorecard activity
aggregates, facade tools, and the business-analysis catalog. With this preset
only, business-analysis calls may return remaining redacted-DB call titles and
raw call IDs when the caller sets the explicit include flags. This preset is
internal manual-testing only — not a client-facing default — and should not be
used against a raw unredacted database.

For customer-pilot deployments, prefer `broad-public-redacted`. It exposes the
same reviewed Postgres tool surface as `redacted-all-readonly` but enforces
stricter startup gates (governance/blocklist config required) and the
customer-policy switch contract. The reload contract is restart-required: set
`--policy-switches=<csv>` or `GONGMCP_POLICY_SWITCHES=<csv>` and restart
`gongmcp`. Available switches:

- `hide_account_names`
- `hide_call_titles`
- `hide_raw_call_ids` (on by default in `broad-public-redacted`)
- `hide_speaker_ids`
- `hide_contact_names`
- `hide_contact_emails`
- `hide_opportunity_names`
- `hide_loss_reasons`
- `hide_crm_value_snippets`

`gong_status`, `get_sync_status`, and `/healthz` echo the active switches in
`mcp_server.policy_switches_enabled` and the reload contract in
`mcp_server.policy_switch_reload_contract` so a manual tester can confirm
which posture a deployment is running.

Expected core tools:

- `get_sync_status`
- `list_crm_object_types`
- `list_crm_fields`
- `get_business_profile`
- `list_business_concepts`
- `list_unmapped_crm_fields`
- `analyze_late_stage_crm_signals`
- `opportunities_missing_transcripts`
- `search_transcripts_by_crm_context`
- `opportunity_call_summary`
- `crm_field_population_matrix`
- `list_lifecycle_buckets`
- `summarize_calls_by_lifecycle`
- `prioritize_transcripts_by_lifecycle`
- `compare_lifecycle_crm_fields`
- `summarize_call_facts`
- `rank_transcript_backlog`
- `search_transcript_segments`
- `search_transcripts_by_call_facts`
- `search_transcript_quotes_with_attribution`

Expected analyst tools:

- `build_call_cohort`
- `inspect_call_cohort`
- `list_call_cohorts`
- `compare_call_cohorts`
- `search_calls_by_filters`
- `summarize_calls_by_filters`
- `search_transcripts_by_filters`
- `discover_themes_in_cohort`
- `summarize_themes_by_dimension`
- `compare_themes_over_time`
- `compare_themes_by_segment`
- `extract_theme_quotes`
- `search_quotes_in_cohort`
- `rank_quotes_for_sales_use`
- `build_quote_pack`
- `compare_theme_outcomes`
- `summarize_pipeline_progression_by_theme`
- `summarize_loss_reasons_by_theme`
- `compare_won_lost_theme_patterns`
- `summarize_themes_by_persona`
- `summarize_themes_by_industry`
- `rank_personas_by_insight_quality`
- `diagnose_attribution_coverage`
- `generate_sales_hooks_from_themes`
- `generate_outreach_sequence_inputs`
- `recommend_target_personas_and_industries`
- `build_theme_brief`
- `score_cohort_evidence_quality`
- `explain_analysis_limitations`
- `suggest_filter_refinements`

Scorecard inventory tools `list_scorecards` and `get_scorecard` are part of the
`analyst-expansion` manual-test checklist as of Phase 13g. The activity
aggregate `summarize_scorecard_activity` is intentionally NOT exposed by
`analyst-expansion`; activity-aggregate testing remains in `analyst-core` and
`analyst-business-core`. Raw scorecard activity payloads, answer text, user
IDs, and call IDs continue to be off-limits for any preset.

## 3. Smoke Prompt

Prompt:

```text
Using the Gong Test MCP, check sync status first. Confirm the cache counts,
transcript coverage, and any limitations. Then list the tools you expect to use
for a Business Discovery analysis. Do not request raw transcripts, raw SQL, or
unrestricted account enumeration.
```

Expected result:

- `get_sync_status` succeeds.
- The status payload includes `mcp_server` with the expected `version`,
  `commit`, `tool_preset`, `deployment_id`, `started_at_utc`, tool counts, and
  transcript evidence provenance. Stop if these do not match the intended
  deployment, because the client may be connected to a stale MCP server.
- Counts match the current deployment baseline within the expected refresh
  window.
- Missing transcript count is acceptable for the pilot, ideally `0`.
- The model does not ask for Gong credentials or database access.

Fail if:

- The model cannot call `get_sync_status`.
- Counts are stale or unexpectedly zero.
- The model claims live Gong access from MCP.
- The model suggests raw SQL, raw transcript dump, or unrestricted account
  enumeration.

## 4. Business Discovery Cohort

Prompt:

```text
Using the Gong Test MCP, find recent Business Discovery calls from the reviewed
cache. Build a bounded cohort, inspect coverage, then summarize the main
discovery themes with transcript evidence and limitations.
```

Expected tool sequence:

- `build_call_cohort`
- `inspect_call_cohort`
- `discover_themes_in_cohort`
- `build_quote_pack` or `search_quotes_in_cohort`
- `diagnose_attribution_coverage`
- `explain_analysis_limitations`

Expected result:

- Title filtering works for "Business Discovery" or the approved equivalent.
- The answer is evidence-backed and labels gaps.
- Theme output distinguishes structured tool results from host-model synthesis.
- If the model selects a single returned `call_ref`, `get_call` can fetch
  minimized call detail with that `call_ref` without requiring or echoing the raw
  `call_id`.

Fail if:

- The model invents call content without quote/snippet support.
- The model treats missing participant title coverage as complete persona
  attribution.
- The model ignores coverage/limitation tool output.
- The model asks the operator for a raw call ID after it already has a returned
  `call_ref`.

## 5. Company Search And Restricted-Name Probe

Use placeholders in notes. Do not write the restricted company names into this
checklist.

Prompt:

```text
Using the Gong Test MCP, search for calls and transcript evidence involving an
approved non-restricted company name supplied by the operator. Then repeat the
same style of search for a restricted company name supplied by the operator.
Explain whether the restricted probe returned no rows because the serving DB is
redacted or because the tool policy blocks the query.
```

Expected tool sequence:

- `search_calls_by_filters`
- `search_transcripts_by_filters`
- `search_transcript_quotes_with_attribution` if quote evidence is needed
- `explain_analysis_limitations`

Expected result:

- Non-restricted company search can return bounded results when matching data
  exists.
- Restricted-company probes return a generic policy denial before query
  execution, not zero-row counts that could become a membership-inference signal.
- The answer explains that restricted data should be physically absent from the
  MCP serving DB and that matching restricted names/aliases are blocked at
  runtime as well.

Fail if:

- Restricted customer names appear in returned call titles, snippets, account
  names, opportunity names, CRM object names, or quote text.
- The model reports a raw account enumeration capability.
- The model treats a zero-row response as proof the customer never existed in
  Gong instead of expecting the generic policy denial.

## 6. Transcript Summary Check

Prompt:

```text
Using the Gong Test MCP, summarize one approved call from the reviewed cache.
Use bounded transcript excerpts or quote evidence. If no excerpts are available
for that specific call, say that and do not invent the summary.
```

Expected result:

- The model uses bounded snippet/quote tools.
- If a selected call has metadata but no returned snippet evidence, the answer
  says it cannot reliably summarize content.
- Metadata-only facts such as date, duration, stage, and industry are separated
  from transcript-derived claims.

Fail if:

- The model writes a content summary without evidence.
- The model implies full-transcript access when the tool returned no excerpts.
- The model exposes raw transcript text beyond bounded snippets.

## 7. Buyer Concern Themes

Prompt:

```text
Using the Gong Test MCP, analyze calls from the reviewed period and identify
buyer concerns around implementation timeline, IT integration, supplier
enablement, procurement process, and operations workflow. Include bounded
evidence snippets and note attribution limitations.
```

Expected tool sequence:

- `build_call_cohort`
- `inspect_call_cohort`
- `summarize_themes_by_dimension`
- `search_quotes_in_cohort` or `extract_theme_quotes`
- `rank_quotes_for_sales_use`
- `diagnose_attribution_coverage`

Expected result:

- Themes are commercially coherent and evidence-backed.
- Persona/title attribution is labeled as weak when coverage is weak.
- The model does not overstate participant roles.

Fail if:

- The answer ranks personas as authoritative without title coverage.
- The answer uses only call titles or CRM stages as transcript evidence.

## 8. Pipeline And Outcome Questions

Prompt:

```text
Using the Gong Test MCP, compare whether the main Business Discovery themes are
associated with lifecycle buckets, opportunity stages, or pipeline progression
in the reviewed cache. Include caveats about CRM outcome coverage.
```

Expected tool sequence:

- `compare_theme_outcomes`
- `summarize_pipeline_progression_by_theme`
- `summarize_loss_reasons_by_theme` when loss reason fields are populated
- `score_cohort_evidence_quality`
- `explain_analysis_limitations`

Expected result:

- The answer ties claims to cached CRM coverage only.
- Missing opportunity/loss/won-lost fields are labeled as limitations.
- Small-cell suppression is respected for scoped analyst dimensions.

Fail if:

- The answer infers win/loss or loss reason from transcript sentiment alone.
- Suppressed or missing buckets are treated as zero business activity.

## 9. Scorecard Inventory Decision

Prompt:

```text
Using the Gong Test MCP, tell me whether this deployed preset exposes scorecard
inventory. If the scorecard tools are unavailable, explain which preset or
operator approval would be needed before testing scorecard inventory questions.
```

Expected result:

- The model accurately reports whether `list_scorecards` and `get_scorecard`
  are available in the current `tools/list`.
- If unavailable under the current preset, it does not invent scorecard
  questions or activity from unrelated fields.

Fail if:

- The model claims scorecard inventory is available when `tools/list` does not
  include the tools.
- The model exposes raw scorecard activity payloads or answer text.

## 10. Pass/Fail Record

Record this table in the pilot notes. Use counts and reviewed artifact paths,
not sensitive values.

| Check | Result | Evidence | Notes |
| --- | --- | --- | --- |
| Authentication and approved user login |  |  |  |
| Blocked user denied |  |  |  |
| `get_sync_status` count baseline |  |  |  |
| Expected preset and tool surface |  |  |  |
| Business Discovery cohort |  |  |  |
| Non-restricted company search |  |  |  |
| Restricted-company negative probe |  |  |  |
| Transcript summary evidence boundary |  |  |  |
| Buyer concern themes |  |  |  |
| Pipeline/outcome caveats |  |  |  |
| Persona/title attribution caveats |  |  |  |
| Scorecard inventory preset decision |  |  |  |
| No raw identifiers or restricted names in output |  |  |  |

## 11. Rollback Path

If manual testing fails:

1. Disable business-user access at the auth gateway.
2. Repoint `gongmcp` to the prior reviewed serving DB or prior image digest.
3. Restart only `gongmcp` unless the gateway or database role changed.
4. Run the public smoke again.
5. Record the failure, deployed commit/image digest, affected preset, and
   sanitized evidence path.

Do not run a fresh Gong sync as the first rollback action. First restore the
last known-good MCP serving path, then investigate sync or refresh separately.
