# Business Workbench Host Instructions

Use these instructions when wrapping the Gong MCP `business-workbench` preset in
Claude, ChatGPT, or another assistant for non-technical Sales and Marketing
users.

## Required Workflow

- Start each session with `gong_status` and `gong_discover_capabilities` when
  tool state is unknown.
- For broad questions such as "what are the main themes this quarter", call
  `question.answer` first. Do not call raw transcript search or present
  seedless keyword candidates as final themes.
- If the response is `needs_theme_seed`, show the suggested seed topics and run
  a seeded `theme_intelligence_report` before making claims.
- Treat Gong AI brief/keyPoint/highlight rows as directional candidate evidence.
  Use transcript quote evidence for customer-facing claims.
- For Sales coaching, use `extract.objection_signals` with seeded topics.
- For Marketing content gaps, use `extract.buyer_questions` with seeded topics.
- For a question about one named prospect or account across calls, use
  `prospect.question.answer` only when the user supplied the prospect/account
  name. Set `filter.account_query` and `include_account_names=true`; do not use
  this path to discover or enumerate customers.
- For drilldowns, pass the exact `drilldown_term` returned by
  `theme_intelligence_report`.

## Evidence Language

- `gong_ai_condensed_candidate`: directional Gong AI condensed evidence; not a
  final buyer-validated conclusion.
- `deterministic_keyword_synonym`: keyword/synonym evidence; not full semantic
  classification.
- `verbatim_transcript_quote`: strongest evidence for customer-facing claims.
- `dimension_rollup`: only reliable when `dimension_readiness` is `ready`.

Never say "buyers said" when `speaker_role` is `unknown` or
`speaker_role_status` is `affiliation_missing`. Use "unattributed transcript
evidence" or "external-or-unknown evidence" instead.

## Display Defaults

- Follow `evidence_policy.host_display_policy` when present.
- Default business-user answers should use `business_summary` mode: findings,
  evidence, caveats, and next step.
- Do not include an "Exact MCP operations exercised", raw tool trace, tool
  inventory, runtime identity table, or schema/debug section unless the user
  explicitly asks for it.
- When comparing filtered and unfiltered cohorts, avoid negative deltas in
  business prose. Say "excluded 1,337 low-signal calls" or "reduced from 1,573
  to 236" instead of showing `-1,337`.

## Caveats To Surface

- Show `data_readiness_caveats` when using industry, persona, stage, won/lost,
  loss reason, or methodology-style claims.
- Treat `field_profile` as a structured metadata preset, not a complete
  redaction layer. `limited` suppresses stable speaker refs; raw `speaker_id`
  visibility is controlled by the server policy switch `hide_speaker_ids`.
  Names embedded inside Gong AI brief/keyPoint/highlight text or transcript
  snippets are not automatically redacted.
- Do not infer loss reasons, MEDDICC, pain, champions, buying committees, or next
  steps unless the customer profile maps and populates those fields.

## What To Avoid For Business Users

- Do not expose raw seedless `analyze.themes.discover` as a final answer path.
- Do not use raw transcript search for broad theme prompts.
- Do not probe account/customer names unless explicitly permitted.
- Do not turn account-scoped analysis into customer enumeration. A named
  account query is allowed only when the user supplied the account/prospect name
  or the workflow has explicit account-name authorization.
- Do not hide sparse coverage behind confident prose.

## Release Harness

Before broad GA, run:

```bash
MCP_URL=https://example.com/mcp \
MCP_BEARER_TOKEN="$TOKEN" \
MCP_ORIGIN=https://example.com \
KEEP_ARTIFACTS=1 \
scripts/business-workbench-ga-harness.sh
```

The harness writes `business-workbench-ga-report.json` and scores each workflow
as `PASS`, `DEGRADED`, or `FAIL`. Manual Claude/ChatGPT testing remains useful
for exploration, but this deterministic harness is the release gate.
