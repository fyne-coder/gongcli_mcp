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
- Bearer token location and rotation plan.
- Allowed browser origins.
- HTTPS/auth boundary owner: local stdio, private bearer bridge, or customer
  OAuth/MCP broker.
- Log destination and payload logging policy.
- Support-bundle sharing policy.

## Named Tool Profiles

Use these names in tickets, deployment manifests, and reviews so business and
IT teams do not compare raw comma-separated lists by eye. `gongmcp` accepts
them with `--tool-preset NAME` or `GONGMCP_TOOL_PRESET=NAME`.

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
