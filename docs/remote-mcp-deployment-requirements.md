# Remote MCP Deployment Requirements

This document is for IT and DevOps operators deploying a customer-hosted remote
MCP endpoint for Gong call intelligence. It assumes no prior experience with
LLMs or the Model Context Protocol (MCP).

For connector-specific OAuth details and broker examples, see
[Remote MCP auth and connector setup](remote-mcp-auth.md). For incident
response, see
[Remote MCP OAuth troubleshooting](runbooks/remote-mcp-oauth-troubleshooting.md).

## What You Are Deploying

MCP is the protocol a hosted assistant uses to call approved tools.
`gongmcp` is the read-only MCP server in this repo. It answers from a cached
Gong dataset; it does not call Gong live and should not hold Gong API
credentials in the MCP client path.

`gongctl` is the separate writable sync job that refreshes the cache. Do not
expose `gongctl` to end-user MCP clients.

In production, public HTTPS terminates at a customer-managed gateway or OAuth
broker, not on raw `gongmcp`:

```text
Hosted connector: ChatGPT, Claude, or custom app
  -> public HTTPS MCP gateway/broker
  -> private bearer-authenticated gongmcp
  -> read-only SQLite cache or Postgres reader
```

The writable sync path stays separate:

```text
Scheduler or operator
  -> gongctl sync with Gong credentials
  -> cache database
  -> gongmcp reads the cache read-only
```

## Deployment Modes

| Mode | Use when | Public HTTPS required | Auth owner | Status |
| --- | --- | --- | --- | --- |
| Local stdio | Claude Desktop, Claude Code, Codex, Cursor, or a single operator on one machine | No | Local OS/user config | Supported |
| Private HTTP bearer | MCP Inspector, curl, or internal services inside the company boundary | No, unless the company chooses public ingress | Static bearer plus private network controls | Supported |
| Hosted connector | ChatGPT/Claude add-by-URL or API-hosted MCP clients | Yes, on the gateway/broker | OAuth/MCP broker plus IdP | Required for production remote use |
| Lab/demo tunnel | Short demo through Cloudflare Tunnel, ngrok, or similar | Yes, but temporary | Same auth boundary as hosted connector | Lab/demo only; never tunnel to raw `gongmcp` |

If the user connects through a hosted Claude or ChatGPT connector, use the
hosted connector row. If the MCP client runs inside the company boundary, use
local stdio or private HTTP.

## Choose The Deployment Path

Pick the first row that matches the company's target client and infrastructure.
Do not start from an IdP brand name alone.

| Path | Choose when | Primary docs |
| --- | --- | --- |
| Local stdio MCP | Claude Desktop, Claude Code, Codex, Cursor, or another local client runs inside the same machine/trust boundary | [Quickstart](quickstart.md), [Docker deployment](docker.md) |
| Private HTTP bearer | Internal-only curl, MCP Inspector, or service-to-service testing needs HTTP but not hosted connectors | [Remote MCP auth](remote-mcp-auth.md#option-a-private-pilot-bearer-token) |
| Hosted remote MCP with direct OIDC gateway | Claude.ai, ChatGPT, or another hosted client needs `/mcp`, and the company wants its IdP such as JumpCloud, Okta, Entra, Auth0, or Keycloak to mint/validate OIDC tokens directly | [Remote MCP auth](remote-mcp-auth.md#jumpcloud-and-direct-oidc-pattern), [Remote MCP auth examples](../deploy/remote-mcp-auth/README.md) |
| Cloudflare OAuth broker | The company can deploy Cloudflare Workers and wants a full MCP-shaped OAuth broker with Dynamic Client Registration | [Cloudflare Worker OAuth broker](../deploy/remote-mcp-auth/cloudflare-worker/README.md) |
| AWS Cognito gateway fallback | The company explicitly wants Cognito hosted UI/user pools, or direct OIDC/static-client testing cannot satisfy the hosted client's registration/token behavior | [AWS Cognito MCP gateway starter](../deploy/remote-mcp-auth/aws-cognito-gateway/README.md) |
| Keycloak lab proof | Operators need a disposable open-source surrogate before spending IdP trial time or changing company IdP settings | [Lab auth deployment](lab-auth-deployment.md) |

TradeCentric's current handoff uses the hosted remote MCP/direct OIDC path on
branch `codex/tc-jumpcloud-mcp-gateway`. Cognito is not the default for that
handoff; keep it as an AWS-specific fallback if direct OIDC cannot pass the
Claude OAuth and first-tool-call proof.

## Required Infrastructure

Network and TLS:

- Public DNS name for the gateway or broker, for example
  `gong-mcp.example.com`.
- Valid TLS certificate at the gateway.
- Public reachability from the provider infrastructure for hosted connectors.
- Private network path from the gateway/broker to `gongmcp`.
- No public path that bypasses the gateway and reaches raw `gongmcp`.

Data plane:

- Governed SQLite cache or Postgres reader role for `gongmcp`.
- Separate writable sync job for `gongctl`.
- Secret manager entries for Gong sync credentials, Postgres credentials, and
  the internal MCP bearer token.
- Backup/restore and cache-refresh monitoring.

Auth plane:

- OAuth/MCP broker or gateway in front of `gongmcp`.
- IdP application registration for JumpCloud, Cognito, Okta, Entra, or the
  customer standard IdP.
- Supported hosted-client registration path, such as Dynamic Client
  Registration, Client ID Metadata Document, or a documented static-client path
  the target connector supports.
- PKCE-capable authorization-code flow.
- Dedicated group allowlist for production, with subject/email allowlists only
  as temporary non-prod smoke gates.
- Token validation based on the provider's OIDC discovery `jwks_uri`.
- Provider-specific access-token claim mapping. Do not assume every IdP emits
  Cognito-style `token_use`, `client_id`, `scope`, and top-level group claims.
  For JumpCloud/Ory-shaped tokens, explicitly check `aud`, `scp`, `memberOf`,
  and nested `ext` before production rollout.

Runtime and operations:

- `gongmcp` bound to loopback or a private interface.
- The same internal bearer secret on private `gongmcp` as
  `GONGMCP_BEARER_TOKEN[_FILE]` and on the gateway as
  `GATEWAY_INTERNAL_BEARER_TOKEN[_FILE]`.
- Narrow `GONGMCP_TOOL_PRESET` or `GONGMCP_TOOL_ALLOWLIST`.
- `GONGMCP_ALLOWED_ORIGINS` aligned to the approved browser origins.
- Payload-free access logs, metrics, rate limits, and alerting.

## Auth Responsibility Split

An IdP login page alone is not a remote MCP deployment.

| Layer | Responsibility | Examples |
| --- | --- | --- |
| IdP | Human login, MFA, directory groups | JumpCloud, Cognito, Okta, Entra |
| OAuth/MCP broker | MCP metadata, registration/client credentials, token exchange, token validation, forwarding approved requests | Cloudflare Worker OAuth broker, API gateway, custom broker |
| `gongmcp` | Tool catalog, read-only cache access, internal bearer auth, tool preset enforcement | `GONGMCP_TOOL_PRESET`, `GONGMCP_ALLOWED_ORIGINS` |
| `gongctl` | Writable sync from Gong into the cache | Scheduled job with Gong credentials |

`oauth2-proxy` is useful for browser-session login rehearsal, but it is not the
full MCP OAuth broker. If Claude probes metadata and then POSTs `/mcp` without
an `Authorization` header, debug the broker registration/token flow and edge
routing before asking the user to retry their JumpCloud password.

## Configuration Inventory

| Setting | Layer | Purpose |
| --- | --- | --- |
| `PUBLIC_BASE_URL` | Gateway/broker | Public base URL, for example `https://gong-mcp.example.com` |
| `/mcp` URL | Hosted connector | URL entered in ChatGPT/Claude or API config |
| IdP issuer URL | Broker | OIDC discovery and token issuer |
| Client ID/secret or DCR/CIMD config | Broker + IdP | How the hosted connector registers or authenticates |
| OIDC `jwks_uri` | Broker | Signing keys for JWT validation |
| Allowed groups/emails | Broker | Pilot access policy; prefer one or more dedicated groups for production |
| `GONGMCP_BEARER_TOKEN[_FILE]` / `GATEWAY_INTERNAL_BEARER_TOKEN[_FILE]` | Gateway + `gongmcp` | Same random secret; gateway uses `GATEWAY_INTERNAL_BEARER_TOKEN[_FILE]`, private `gongmcp` uses `GONGMCP_BEARER_TOKEN[_FILE]` |
| `GONGMCP_ALLOWED_ORIGINS` | `gongmcp` | Browser Origin allowlist |
| `GONGMCP_TOOL_PRESET` / `GONGMCP_TOOL_ALLOWLIST` | `gongmcp` | Approved MCP tool surface |
| `GONGMCP_DB` or `GONG_DATABASE_URL` | `gongmcp` | Read-only cache location |

Use maintained OAuth/OIDC/JWT/JWKS libraries or official SDKs for production
discovery, signature verification, claim validation, refresh handling, and JWKS
caching. Do not extend lab-only hand-rolled token parsing into production auth.
For the gateway starter, configure at least one group, subject, or email gate in
addition to the required scope. Prefer `OIDC_REQUIRED_GROUP` for one dedicated
group or `OIDC_REQUIRED_GROUPS` / `OIDC_ALLOWED_GROUPS` for any-match policy
across multiple dedicated groups. Use subject/email allowlists only for
temporary smoke tests. Legacy `COGNITO_*` policy names remain
backward-compatible aliases for existing deployments and Cognito-specific
fallback docs.

## Smoke Test Sequence

Run these in order. Do not mark the deployment ready after browser login alone.

1. Confirm external DNS and TLS from outside the company network:

   ```bash
   curl -I https://gong-mcp.example.com/mcp
   ```

   Any HTTPS response with a valid certificate, including `401` or `405`,
   confirms DNS and TLS. Validate auth behavior and response shape in the next
   steps.

2. Confirm OAuth metadata:

   ```bash
   curl -fsS https://gong-mcp.example.com/.well-known/oauth-protected-resource
   curl -fsS https://gong-mcp.example.com/.well-known/oauth-protected-resource/mcp
   curl -fsS https://gong-mcp.example.com/.well-known/oauth-authorization-server
   ```

   For an MCP resource at `/mcp`, the endpoint-scoped protected-resource
   metadata path `/.well-known/oauth-protected-resource/mcp` is the canonical
   check. Some brokers also provide the unscoped path for compatibility.

3. Confirm unauthenticated MCP challenge. For deployments that implement the
   MCP OAuth challenge at `/mcp`, expect `401` with `WWW-Authenticate` and
   `resource_metadata`. An unauthenticated `POST /mcp` must not return `200`.

   ```bash
   curl -i -X POST https://gong-mcp.example.com/mcp \
     -H "Content-Type: application/json" \
     -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'
   ```

4. Confirm registration or static-client setup:

   - DCR path: registration endpoint accepts the hosted connector.
   - CIMD/static path: the target connector accepts the configured client ID,
     secret policy, and redirect URI.

5. Complete OAuth login and token exchange. Decode the access token locally and
   verify issuer, audience/resource, expiry, scopes, and user/group/email
   claims. For direct JumpCloud or another non-Cognito IdP, verify whether
   scopes appear in `scope` or `scp`, whether client/resource binding appears
   in `client_id` or `aud`, and whether group/email claims are top-level or
   nested under a provider container such as `ext`. Do not paste real customer
   tokens into hosted JWT decoder sites.

6. Confirm authenticated MCP:

   ```bash
   curl -i -X POST https://gong-mcp.example.com/mcp \
     -H "Authorization: Bearer $ACCESS_TOKEN" \
     -H "Content-Type: application/json" \
     -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}'
   ```

7. Run the first safe tool, usually `get_sync_status`, before enabling search
   tools for users.

For private bearer pilots, use
[`scripts/smoke-http-mcp.sh`](../scripts/smoke-http-mcp.sh) with the approved
origin and internal bearer token.

For direct OIDC or static-client gateway deployments, use
`gongctl doctor mcp-gateway` from outside the cluster or private network:

```bash
gongctl doctor mcp-gateway \
  --url https://gong-mcp.example.com/mcp \
  --issuer https://issuer.example.com \
  --profile direct-oidc \
  --origin https://claude.ai \
  --required-scope gongmcp/read \
  --group-claim memberOf \
  --client-id <Claude app client ID> \
  --required-group <one expected dedicated MCP group> \
  --token-env GONGMCP_TEST_ACCESS_TOKEN
```

Set `GONGMCP_TEST_ACCESS_TOKEN` only in the operator shell. The doctor treats
decoded JWT payload shape as untrusted diagnostics and never prints the token,
email values, group names, or authenticated tool bodies.

For the AWS Cognito gateway starter, use
[`scripts/smoke-mcp-gateway.sh`](../scripts/smoke-mcp-gateway.sh). It checks
protected-resource metadata, HTTPS authorization-server metadata, endpoint
challenge behavior, challenge scope hints, optional CORS preflight, and an
optional authenticated `tools/list` call when provided a test Cognito access
token. Add `--expect-dcr` when the optional Cognito DCR fallback is enabled.
For DCR registration against live Cognito, use
[`scripts/smoke-cognito-dcr.sh`](../scripts/smoke-cognito-dcr.sh) with explicit
`--create-client` only in non-prod. JumpCloud claim mapping and IAM/IRSA
templates live under
[`deploy/remote-mcp-auth/aws-cognito-gateway/`](../deploy/remote-mcp-auth/aws-cognito-gateway/).

## Common Failure Map

| Symptom | Likely cause | First checks |
| --- | --- | --- |
| Hosted connector cannot reach server | DNS/TLS or public ingress is wrong | Public URL, cert chain, provider egress allowlist |
| Metadata succeeds, then `/mcp` gets unauthenticated `401`/`403` | Connector could not complete registration/token flow, or `/mcp` is routed to `oauth2-proxy` instead of the MCP broker | Auth-server metadata, DCR/CIMD/static-client support, edge route, broker logs |
| `/mcp` appears only in `oauth2-proxy` logs | Browser-session proxy is acting as the whole auth layer | Route `/mcp` to the MCP broker/shim; keep `oauth2-proxy` on login helper paths |
| Browser login succeeds but Claude still fails | Login happened, but token exchange or bearer forwarding did not | Token endpoint, refresh/offline policy, audience/resource, `WWW-Authenticate` challenge |
| Authenticated `/mcp` gets `401` | Token validation mismatch | OIDC `jwks_uri`, issuer, audience, expiry, group/email claims |
| Tools import but first tool call fails | MCP payload or tool allowlist issue | `_meta` tolerance, tool preset, server logs |
| Valid token gets `403` | Origin or group policy mismatch | `GONGMCP_ALLOWED_ORIGINS`, configured group claim, dedicated group allowlist |
| Answers are stale | Cache refresh is stale or failing | `get_sync_status`, sync schedule, Postgres/SQLite cache health |

## Security Guardrails

- Never expose raw unauthenticated `gongmcp` to the public internet.
- Keep Gong API credentials only in the restricted `gongctl` sync job.
- Use read-only DB credentials for `gongmcp`.
- Start with the narrowest useful tool preset.
- Rotate the internal bearer token and support current/previous token files
  during rotation where possible.
- Do not log prompts, transcripts, CRM values, bearer tokens, or raw MCP
  payload bodies.
- Rate-limit the public gateway and alert on auth failures or tool-call spikes.
- Strip trusted proxy identity headers unless a reviewed upstream gateway
  overwrites them and source CIDRs are pinned.
- Review hosted AI workspace data controls before connecting real customer data.

## Example Starters In This Repo

| Example | Role |
| --- | --- |
| [TradeCentric / company direct OIDC handoff](../deploy/remote-mcp-auth/tradecentric-jumpcloud/README.md) | Current TC handoff and reusable company template for direct OIDC with JumpCloud-style providers |
| [JumpCloud Compose starter](../deploy/remote-mcp-auth/jumpcloud/docker-compose.yml) | Direct OIDC/static-client gateway shape for JumpCloud-style providers; not a full hosted MCP broker by itself |
| [Cloudflare Worker OAuth broker](../deploy/remote-mcp-auth/cloudflare-worker/README.md) | Recommended MCP-shaped broker with Dynamic Client Registration |
| [AWS Cognito MCP gateway starter](../deploy/remote-mcp-auth/aws-cognito-gateway/README.md) | AWS-specific fallback/runbook for Cognito hosted UI, Cognito token validation, and optional Cognito DCR |
| [Cognito Compose starter](../deploy/remote-mcp-auth/cognito/docker-compose.yml) | Cognito IdP plus static-client/JWT gateway shape |
| [Lab auth stack](../deploy/lab-auth/docker-compose.yml) | Disposable Keycloak rehearsal harness, not a production IdP template |

## Related Documentation

- [Remote MCP auth and connector setup](remote-mcp-auth.md)
- [Remote MCP OAuth troubleshooting](runbooks/remote-mcp-oauth-troubleshooting.md)
- [Enterprise deployment](enterprise-deployment.md)
- [Docker deployment](docker.md)
- [Security checklist](security-checklist.md)
