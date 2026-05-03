# Customer Implementation Checklist

Use this worksheet before giving business users access to `gongmcp`.

## Role Handoff

| Role | Starts with | Owns | Hands off when |
| --- | --- | --- | --- |
| IT installer | Docker image, data root, HTTPS/auth decision | container runtime, secrets, TLS/proxy, allowed origins, logs, `/healthz`, rollback | `gongmcp` is reachable, read-only, authenticated, and smoke-tested |
| Sync operator | Gong credentials and sync plan | writable `gongctl` jobs, cache refresh, backups, governed DB export | the MCP DB is current and promoted to a read-only path |
| RevOps profile admin | cached CRM context, schema, settings | profile YAML review, validation, lifecycle/attribution signoff | `profile_readiness.active=true` and profile cache is fresh |
| Security reviewer | data boundary, support policy, governance list | tool preset/allowlist, restricted-data filter, support-bundle policy, audit expectations | strict pilot boundary is approved |
| Business user | approved host/app, server name, allowed prompts | `get_sync_status`, approved business questions, escalation | stale/cache/profile/tool issues are sent back to the operator |

## Required Decisions

- Data root path and filesystem owner.
- Writable source DB path and read-only MCP DB path.
- Whether remote users receive a physically filtered MCP DB.
- Sync job owner, schedule, and rollback path.
- Image reference pinned by tag and, for production, digest.
- Tool preset: `business-pilot`, `operator-smoke`, `analyst`,
  `governance-search`, or `all-readonly`.
- Bearer token location and rotation plan. Private bridge deployments can mount
  current and previous token files during rotation.
- Allowed browser origins.
- HTTPS/auth boundary owner: local stdio, private bearer bridge, or customer
  OAuth/MCP broker.
- For remote OAuth MCP: dynamic client registration or static client plan,
  redirect URI policy, requested scopes, refresh/offline-token behavior,
  token audience/resource, and user/group claim mapping.
- Log destination and payload logging policy.
- Support-bundle sharing policy.

## Named Tool Profiles

Use these names in tickets, deployment manifests, and reviews so business and
IT teams do not compare raw comma-separated lists by eye. `gongmcp` accepts
them with `--tool-preset NAME` or `GONGMCP_TOOL_PRESET=NAME`.

Operators can print the current built-in list from the exact binary they will
deploy:

```bash
gongmcp --list-tool-presets
```

| Profile | Tools | Use |
| --- | --- | --- |
| `business-pilot` | `get_sync_status,summarize_call_facts,summarize_calls_by_lifecycle,rank_transcript_backlog` | default business-user lane |
| `strict-business-pilot` | alias for `business-pilot` | backward-compatible docs/scripts alias |
| `operator-smoke` | `get_sync_status,search_calls,search_transcript_segments,rank_transcript_backlog` | operator-only install validation |
| `analyst` | broader evidence surface excluding admin-only record lookup, CRM value search, Gong settings, scorecards, and schema inventory | trusted analyst sessions after sponsor approval |
| `analyst-expansion` | alias for `analyst` | backward-compatible docs/scripts alias |
| `governance-search` | governance-compatible search/snippet tools only | raw-DB AI governance fallback when a filtered DB is not available |
| `all-readonly` | every current read-only MCP tool in `tools/list` | trusted single-user admin/analyst or fully reviewed filtered-DB deployments |

Do not expose `operator-smoke`, `analyst`, `governance-search`, or
`all-readonly` as the default business-user tool surface.

### Analyst Cohort Preset Guidance

Use `analyst` only after the sponsor approves the wider evidence workflow:
filter -> cohort -> inspect -> analyze -> quotes -> limitations. The built-in
analyst cohort surface includes these tools:

- Cohort: `build_call_cohort`, `inspect_call_cohort`, `list_call_cohorts`,
  `compare_call_cohorts`
- Generic filter/search: `search_calls_by_filters`,
  `summarize_calls_by_filters`, `search_transcripts_by_filters`
- Themes: `discover_themes_in_cohort`, `summarize_themes_by_dimension`,
  `compare_themes_over_time`, `compare_themes_by_segment`
- Quotes/evidence: `extract_theme_quotes`, `search_quotes_in_cohort`,
  `rank_quotes_for_sales_use`, `build_quote_pack`
- Outcome/pipeline: `compare_theme_outcomes`,
  `summarize_pipeline_progression_by_theme`,
  `summarize_loss_reasons_by_theme`, `compare_won_lost_theme_patterns`
- Persona/industry: `summarize_themes_by_persona`,
  `summarize_themes_by_industry`, `rank_personas_by_insight_quality`,
  `diagnose_attribution_coverage`
- Sales/marketing synthesis: `generate_sales_hooks_from_themes`,
  `generate_outreach_sequence_inputs`,
  `recommend_target_personas_and_industries`, `build_theme_brief`
- Coverage/quality: `score_cohort_evidence_quality`,
  `explain_analysis_limitations`, `suggest_filter_refinements`

Before enabling the preset for ChatGPT, Claude, or another host, run:

```bash
gongmcp --list-tool-presets
```

Then verify that the selected deployment actually returns the expected tools in
`tools/list`. If a tool is missing, stop and check the deployed binary version,
image tag, preset, and allowlist before testing analyst prompts.

Use `all-readonly` only when the analyst also needs every other read-only
operator/admin tool. For most approved business analysis, prefer `analyst` plus
safe per-tool defaults over `all-readonly`.

## Smoke Tests

Run these before user access:

```bash
scripts/smoke-http-mcp.sh \
  --url https://gong-mcp.example.com/mcp \
  --token "$TOKEN" \
  --origin https://approved-client.example.com
```

The script runs the same checks below without printing the token or response
payloads.

```bash
curl -fsS https://gong-mcp.example.com/healthz
```

Expected: JSON with `status=ok`, `service=gongmcp`, and a version.

```bash
curl -i https://gong-mcp.example.com/mcp \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}'
```

Expected: `401 Unauthorized`.

```bash
curl -i https://gong-mcp.example.com/mcp \
  -H "Origin: https://not-approved.example.com" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}'
```

Expected: `403 Forbidden` and a payload-free access log entry.

```bash
curl -fsS https://gong-mcp.example.com/mcp \
  -H "Origin: https://approved-client.example.com" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}'
```

Expected: only tools from the approved profile.

Then ask the host app:

```text
Use get_sync_status. Tell me whether the cache, profile, and transcript
coverage are ready for the approved pilot prompts. If not, name the operator
handoff.
```

For remote OAuth MCP clients such as ChatGPT, Claude remote add-by-URL, MCP
Inspector, or custom clients, also verify the OAuth path end to end:

- protected-resource metadata resolves for the `/mcp` resource
- unauthenticated `/mcp` returns `401` with `WWW-Authenticate` and
  `resource_metadata`
- dynamic client registration works, or the documented static client path works
- browser login completes
- authorization-code token exchange returns an access token
- decoded access token has expected issuer, audience/resource, expiry, scopes,
  user identity, and group/role/email claims
- authenticated `initialize`, `tools/list`, and `get_sync_status` succeed
- a ChatGPT-style `tools/call` with `_meta` succeeds

Local Claude Desktop stdio MCP does not use this remote OAuth path.

### Analyst JSON-RPC Smoke Commands

Run this smoke against a reviewed synthetic or customer-approved local cache.
Use a read-only DB path outside the source checkout.

```bash
DB="$HOME/gongctl-data/gong-mcp-governed.db"

printf '%s\n' \
'{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"analyst-smoke","version":"0"}}}' \
'{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' \
| gongmcp --db "$DB" --tool-preset analyst
```

After `tools/list` shows the analyst tools, run representative calls:

```bash
printf '%s\n' \
'{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"build_call_cohort","arguments":{"filter":{"title_query":"business discovery","quarter":"2026-Q1","transcript_status":"present","limit":25}}}}' \
'{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"inspect_call_cohort","arguments":{"filter":{"title_query":"business discovery","quarter":"2026-Q1","transcript_status":"present","limit":25}}}}' \
'{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"discover_themes_in_cohort","arguments":{"filter":{"title_query":"business discovery","quarter":"2026-Q1","transcript_status":"present","limit":25},"query":"pain point","limit":10}}}' \
'{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"summarize_themes_by_persona","arguments":{"filter":{"title_query":"business discovery","quarter":"2026-Q1","transcript_status":"present","limit":25},"theme_query":"manual process","limit":10}}}' \
'{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"summarize_themes_by_industry","arguments":{"filter":{"title_query":"business discovery","quarter":"2026-Q1","transcript_status":"present","limit":25},"theme_query":"manual process","limit":10}}}' \
'{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"build_quote_pack","arguments":{"filter":{"title_query":"business discovery","quarter":"2026-Q1","transcript_status":"present","limit":25},"theme_query":"manual process","limit":5}}}' \
'{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"summarize_pipeline_progression_by_theme","arguments":{"filter":{"title_query":"business discovery","quarter":"2026-Q1","transcript_status":"present","limit":25},"theme_query":"manual process","limit":10}}}' \
'{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"diagnose_attribution_coverage","arguments":{"filter":{"title_query":"business discovery","quarter":"2026-Q1","transcript_status":"present","limit":25}}}}' \
'{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"explain_analysis_limitations","arguments":{"filter":{"title_query":"business discovery","quarter":"2026-Q1","transcript_status":"present","limit":25}}}}' \
| gongmcp --db "$DB" --tool-preset analyst
```

Expected:

- `tools/list` includes every requested analyst cohort tool before the
  representative tool calls are run.
- `build_call_cohort` returns a deterministic `cohort_id`, normalized filter,
  count, coverage summary, and warning flags.
- Theme, quote, persona, industry, and pipeline tools return bounded outputs
  and coverage metadata.
- Limitation tools clearly identify missing transcript, title, industry,
  opportunity, loss-reason, or won/lost coverage instead of inferring it.

For an HTTP MCP bridge, use the same JSON-RPC payloads through `/mcp` with the
approved origin and bearer/OAuth boundary:

```bash
curl -fsS https://gong-mcp.example.com/mcp \
  -H "Origin: https://approved-client.example.com" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"build_call_cohort","arguments":{"filter":{"title_query":"business discovery","quarter":"2026-Q1","transcript_status":"present","limit":25}}}}'
```

### Analyst Host Examples

Business discovery title filtering:

```text
Build and inspect a Q1 2026 cohort where title_query is "business discovery"
and transcript_status is present. Then identify top themes, representative
quotes, persona and industry coverage, pipeline progression coverage, and
limitations.
```

Cross-quarter persona and industry themes:

```text
Compare business discovery cohorts for 2026-Q1 and 2026-Q2. Segment theme
signals by participant title and account industry only where attribution
coverage is present. Report missing-title and missing-industry rates first.
```

Top quotes:

```text
For the business discovery cohort, build a quote pack for the manual-process
theme with at most five bounded snippets. Rank each quote for sales use and
include why it is useful.
```

Pipeline outcomes:

```text
Compare manual-process and integration-risk themes against cached opportunity
stage, progression, won/lost status, and loss reason. If those fields are
missing or sparse, report the coverage gap and avoid causal language.
```

Attribution gaps:

```text
Diagnose whether this cohort can support persona, industry, opportunity-stage,
loss-reason, and won/lost analysis. Suggest safer filter refinements or
operator refresh/profile work before writing the final summary.
```

## Business User First 10 Minutes

IT or the pilot operator should give the business user:

- host application name
- server name or remote URL, if visible
- sign-in expectation
- approved tool profile
- pilot operator contact

First prompt:

```text
Use get_sync_status. Is this reviewed cache ready for strict business-pilot
questions? If anything is stale, missing, or unavailable, tell me what to send
to the pilot operator.
```

Second prompt:

```text
Summarize conversation volume by lifecycle and transcript coverage for the
reviewed pilot dataset. Keep the answer aggregate-only.
```

Common failures:

| Symptom | Meaning | Action |
| --- | --- | --- |
| Unauthorized | auth/gateway/token problem | send time and server name to IT |
| Tool unavailable | allowlist does not include that tool | use approved prompt or ask sponsor for expansion |
| Cache stale | sync job has not refreshed the DB | ask operator to refresh/promote cache |
| Profile stale or inactive | lifecycle/attribution is directional | ask RevOps/admin to validate/import profile |
| Low transcript coverage | quote-based answers are not reliable | ask operator to run transcript backlog/refresh |
| Connector setup succeeds but first tool call fails | remote MCP protocol compatibility issue | check server logs for `_meta`, schema, or result-size errors |

## RevOps Profile Signoff

For every mapped object, field, and lifecycle bucket, capture:

| Mapping | Population | Distinct values | Positive examples | Negative examples | Owner | Result |
| --- | --- | --- | --- | --- | --- | --- |
| `deal_stage -> StageName` |  |  |  |  |  | pass/fix |
| `account -> Account` |  |  |  |  |  | pass/fix |
| `post_sales` rules |  |  |  |  |  | pass/fix |

Do not expose profile-aware MCP answers until `sync status` shows an active,
fresh profile and the RevOps owner has signed off lifecycle and attribution
readiness.
