# Analyst Orientation

This guide is for the power analyst who will use `gongmcp` against an
operator-managed cache to answer real sales, marketing, and RevOps questions.
It covers what to ask the operator to set up, how to choose a tool preset,
how to map your sales process into a profile, and which tools to use for
which analytic intent.

You do not need Gong API credentials. You will use `gongmcp` from an MCP host
(Claude Desktop, ChatGPT, Cursor, or another approved client). Your IT or
RevOps operator is responsible for sync, profile import, and reader-role
provisioning.

For pilot boundary, host policy, and the full business-user contract, see
[Pilot sponsor and operator guide](pilot-sponsor-and-operator-guide.md). For
the data exposure model, see [MCP data exposure](mcp-data-exposure.md).

## What to ask your operator for

Hand the operator the following list. Each line maps to a real CLI command;
they should already be familiar.

| Item | Why you need it | Operator command (reference) |
|---|---|---|
| A reviewed cache (SQLite or Postgres reader role) | Read-only source for every analysis | `gongctl sync calls`, `gongctl sync transcripts`, `gongctl sync read-model` |
| An active business profile that maps your CRM objects, lifecycle stages, and methodology concepts | Lifecycle separation, persona/industry attribution, won/lost/post-sales splits | `gongctl profile import`, `gongctl profile activate` |
| A confirmed MCP preset (see next section) | Bounds the tool surface to what you actually need | `gongmcp --tool-preset <name>` or `GONGMCP_TOOL_PRESET=<name>` |
| Output of `gongctl sync status` and `gongctl profile show` | Confirms freshness, lifecycle readiness, and attribution coverage before you start | — |

## Pick the right MCP preset

`gongmcp --list-tool-presets` is the source of truth. The presets you will
care about as an analyst:

| Preset | Tools | When to use |
|---|---|---|
| `business-workbench` | 6 stable facade tools (`gong_status`, `gong_discover_capabilities`, `gong_query`, `gong_analyze`, `gong_get_evidence`, `gong_explain_limitations`) | Default for client-facing MCP hosts. Use this when you want a stable surface that hides internal tool churn. Routes internally to the analyst operations. |
| `analyst-core` (Postgres) | Core call, CRM context, profile, lifecycle, transcript-search tools | First analyst surface against a Postgres reader role. Good starter for "what conversations exist, with what coverage". |
| `analyst-business-core` (Postgres) | Adds bounded transcript-evidence + business-analysis tools | When you need quote packs, theme intelligence, and pipeline outcome tools but still want a narrower surface than full analyst. |
| `analyst` (Postgres) | Full reviewed analyst catalog | Approved analyst sessions over the reviewed Postgres catalog. Ask for this only when the narrower presets won't answer the question. |
| `all-readonly` (SQLite) | Full read-only catalog | Trusted SQLite admin/analyst sessions. Not the default for shared deployments. |

Rule of thumb: start with the narrowest preset that can answer the question,
and ask the operator to widen only when you hit a real gap. Wider presets
mean more tool descriptions in the model context window and more chances for
the model to call a tool you didn't intend.

## Map your sales process into a profile

Profile mapping is the single biggest determinant of whether you can answer
questions like "compare won vs lost themes by industry across Q1 and Q2".
Without a profile, lifecycle separation falls back to builtin compatibility
behavior and many analyst tools report `unavailable` for tenant-specific
concepts. See [Business profiles](profiles.md) for the full reference.

The required lifecycle core is fixed:

- `open` — active pipeline
- `closed_won`
- `closed_lost`
- `post_sales` — renewal, expansion, customer-success, support
- `unknown` — fallback for unmapped or missing data

Your job (with the RevOps process owner) is to decide which CRM stage values
fall into each bucket and which methodology concepts you want to track.
Common starting mappings:

| Process style | Deal object | Stage field | `open` stages | Notes |
|---|---|---|---|---|
| Salesforce-classic | `Opportunity` | `StageName` | `Discovery`, `Evaluation`, `Proposal`, `Negotiation` | `ForecastCategoryName` is segmentation, not lifecycle. |
| HubSpot-classic | `Deal` | `dealstage` | the equivalent in-pipeline stages | `pipeline` is segmentation, not the lifecycle stage. |
| Land-and-expand | Same as above | Same as above | Same as above | Add post-sales rules for `Renewal`, `Upsell`, `Customer Success`. |
| Renewal-led | Same as above | Same as above | Often empty for renewals; keep new-business pipeline only | Treat renewal cycles in `post_sales` and rely on `methodology` aliases. |

A starter `business-profile.example.yaml` lives at
[docs/examples/business-profile.example.yaml](examples/business-profile.example.yaml).
Copy it, edit, and hand it to your operator to import. Keep the real
customer profile out of Git.

Methodology concepts (`pain`, `champion`, `decision_criteria`, `metric`,
`timeline`, …) are aliases that drive theme retrieval. They do not need to
be exhaustive; map only what you intend to ask about.

Before activation, require evidence per mapped object/field/lifecycle bucket:

- population count and distinct value count
- top observed values from the cache
- two positive examples that should match
- two negative examples that should not match
- RevOps signoff

`gongctl profile validate --ga-readiness` is the operator gate; ask them to
run it and share the JSON report.

## Recommended tool sequences by analytic intent

These sequences assume the `analyst` (or `analyst-business-core`) preset on
Postgres, or `all-readonly` on SQLite. For the `business-workbench` facade,
use the listed `gong_*` operations instead.

### "Which themes are showing up in <process stage> conversations?"

1. `build_call_cohort` — bound by `quarter`, `lifecycle_bucket`, and one
   filter that matches the process stage (e.g. `title_query: "discovery"`)
2. `inspect_call_cohort` — confirm count and coverage before analysis
3. `discover_themes_in_cohort`
4. `summarize_themes_by_dimension` (lifecycle / persona / industry)
5. `diagnose_attribution_coverage` — surface missing-title or missing-industry rates
6. `explain_analysis_limitations`

`business-workbench` equivalent: `gong_query` (`query.calls`) →
`gong_analyze` (`theme_intelligence_report`) → `gong_explain_limitations`.

### "What do customers actually say about <objection or topic>?"

1. `build_call_cohort` — broader date range, narrow theme query
2. `search_quotes_in_cohort` or `extract_theme_quotes`
3. `rank_quotes_for_sales_use`
4. `build_quote_pack` — bounded snippet output for handoff
5. `score_cohort_evidence_quality` — grade whether the cohort can support a customer-facing claim

`business-workbench` equivalent: `gong_get_evidence`
(`evidence.quote_pack.build`) with `theme_query` and
`speaker_role: external_or_unknown`.

### "Won vs lost theme patterns"

1. `build_call_cohort` for `closed_won` and again for `closed_lost`
2. `compare_call_cohorts`
3. `compare_won_lost_theme_patterns`
4. `summarize_loss_reasons_by_theme`
5. `summarize_pipeline_progression_by_theme`
6. `diagnose_attribution_coverage`

### "Where is our coverage too thin to trust?"

1. `inspect_call_cohort`
2. `score_cohort_evidence_quality`
3. `suggest_filter_refinements`
4. `explain_analysis_limitations`

This is also the right sequence to run at the start of any session. Cheap
to call, and it prevents you from building an analysis on empty cells.

For SQLite operator/admin sessions, `gongctl analyze coverage` and
`gongctl analyze transcript-backlog` give the same answers from the CLI.

## The `call_filter` allowlist

The analyst tools accept only these filter fields:

`title_query`, `query`, `from_date`, `to_date`, `quarter`,
`lifecycle_bucket`, `scope`, `system`, `direction`, `transcript_status`,
`industry`, `account_query`, `opportunity_stage`, `crm_object_type`,
`crm_object_id`, `participant_title_query`, `limit`.

Use `limit` deliberately. Every tool result becomes model context, so a
1,000-row return from a wide filter often produces a worse answer than a
50-row return from a tight filter. If a tool returns `capped: true`, do not
just bump `limit`; narrow the filter first.

## Sanity checks before you trust an answer

- `gong_status` (or `gongctl sync status`) — cache freshness and active profile
- `dimension_readiness` and `data_readiness_caveats` in the response — explicit unavailable concepts
- `speaker_attribution_summary` — which fraction of quotes have a known speaker role
- Coverage caveats — never publish a customer-facing claim from a result that says coverage is missing or directional-only

## Working with a coding agent

If you are comfortable with a coding agent (Claude Code, Codex, Cursor, or
similar), use it to:

- draft the `business-profile.yaml` from your CRM stage list and validate
  it against the schema in [docs/profiles.md](profiles.md) before handing
  it to your operator
- assemble multi-step analyst tool sequences as a prompt template for the
  MCP host, so each session uses a reproducible plan
- generate the operator-facing handoff (which preset, which date window,
  which lifecycle subset) when you don't speak CLI fluently

A coding agent is *not* a substitute for the operator's review of profile
content or for `gongctl profile validate --ga-readiness`. Keep the real
profile out of any prompt you copy into a hosted agent unless your company
has approved that data path.

## Where to go deeper

- [MCP data exposure](mcp-data-exposure.md) — full tool surface, exposure
  classes, residual risks
- [Business profiles](profiles.md) — YAML schema, rule grammar, runtime
  state
- [Postgres parity matrix](postgres-parity.md) — feature support per
  backend
- [Postgres question parity](postgres-question-parity.md) — concise "can I
  ask X on backend Y?" matrix
- [Pilot sponsor and operator guide](pilot-sponsor-and-operator-guide.md) —
  analyst cohort workflow, worked examples, host instructions
- [Configuration surfaces](configuration-surfaces.md) — what is YAML, what
  is flags, what is env
