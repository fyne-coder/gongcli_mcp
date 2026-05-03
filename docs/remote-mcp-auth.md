# Remote MCP Auth And Connector Setup

## Purpose

This guide explains how a customer can expose `gongmcp` as a remote HTTPS MCP
endpoint for approved users without turning this repo into a vendor-hosted
service.

Current boundary:

- `gongmcp` serves read-only MCP over an existing SQLite cache.
- `gongmcp` can enforce bearer auth itself for private pilots.
- Production SSO/OAuth should normally sit in a customer-managed gateway,
  broker, or application layer in front of `gongmcp`.
- The writable `gongctl` sync job remains separate and should not be exposed to
  ChatGPT, Claude, or end-user MCP clients.

## Implemented Now

`gongmcp` supports:

- stdio MCP for local desktop hosts
- Streamable HTTP-style POST `/mcp` for private pilots
- GET `/mcp` returns 405 because server-sent streaming is not implemented yet
- static bearer-token validation for HTTP mode
- required HTTP tool preset or allowlist
- Origin validation for HTTP requests
- read-only SQLite access
- non-local bind guardrails

Allowed browser origins also receive narrow CORS headers for `/mcp`: allowed
origin reflection, `POST, OPTIONS`, and the `Authorization`, `Content-Type`,
`MCP-Protocol-Version`, and `Mcp-Session-Id` request headers. CORS is not an
auth layer; the customer gateway and bearer/OAuth boundary still own access
control.

It does not currently implement native OAuth 2.1, Protected Resource Metadata,
dynamic client registration, per-user RBAC, tenant routing, or a browser-facing
consent application.

## Deployment Decision Table

| Lane | Use for | Auth boundary | Status |
| --- | --- | --- | --- |
| Local stdio | One operator or desktop MCP host | Local OS/user account | Supported now |
| Private bearer bridge | curl, MCP Inspector, internal service-to-service, gateway integration tests | Static bearer plus private network/proxy | Supported now for pilots |
| End-user remote MCP | ChatGPT/Claude-style shared user access | Customer HTTPS plus OAuth/MCP broker | Required for production remote use |
| Native OAuth in `gongmcp` | Direct OAuth resource-server deployment | `gongmcp` validates issuer/audience/scopes | Not implemented yet |

## Remote MCP Auth Decision

A customer-hosted remote MCP deployment has two separate auth decisions:

| Decision | Owner | Recommended pilot choice |
| --- | --- | --- |
| Human login and SSO | Customer IT/security | Existing IdP such as JumpCloud, Cognito, Okta, Entra, or Cloudflare Access |
| MCP-compatible token issuance and discovery | Customer platform/security | OAuth broker/gateway in front of `gongmcp`, or a future native OAuth layer |

Do not assume that an IdP login page alone is enough for ChatGPT or other
remote MCP clients. Remote MCP clients need protocol-compatible authorization
metadata, token issuance, and bearer-token validation.

## OAuth Broker Compatibility Checklist

Before testing with ChatGPT, Claude remote add-by-URL, or another remote MCP
client, confirm the endpoint provides:

- HTTPS URL ending at the MCP endpoint, for example
  `https://gong-mcp.example.com/mcp`
- a stable browser `Origin` value that can be allowlisted in `gongmcp` or at
  the gateway
- OAuth 2.1-compatible authorization flow for HTTP MCP clients
- protected-resource metadata for the MCP resource
- authorization-server discovery through OAuth/OIDC metadata
- PKCE support for public clients
- dynamic client registration or a documented static-client fallback supported
  by the target MCP client
- scoped access tokens with issuer, audience/resource, expiry, and scope
  validation
- refresh-token or offline-token policy that matches the target client behavior
- tool-level authorization mapping, even if the first pilot maps all approved
  users to the same read-only tool preset
- user identity plus group, role, or email claims in the access token when the
  gateway enforces per-user access
- MCP protocol tolerance for extension fields such as `_meta`, while preserving
  strict validation for real tool arguments
- no Gong credentials in the MCP client or OAuth broker

The complete interop path is:

```text
OAuth/MCP metadata discovery
  -> dynamic or static client registration
  -> browser login
  -> authorization-code token exchange
  -> access-token claims
  -> authenticated /mcp initialize and tools/list
  -> first tools/call
```

Do not mark a remote MCP deployment ready after browser login alone. A user can
successfully authenticate and still fail because the token exchange, audience,
group claim, refresh-token policy, or first tool-call payload is incompatible
with the MCP gateway.

These checks are provider-agnostic. Keycloak, Auth0, Okta, Entra, WorkOS,
Cloudflare Access, Cognito, and JumpCloud-backed brokers expose different admin
surfaces, but the MCP client still needs the same final properties: discoverable
auth metadata, usable registration, a valid bearer token for the MCP resource,
and tool calls accepted by the MCP server.

Client behavior differs. ChatGPT developer-mode connectors, Claude remote
add-by-URL, MCP Inspector, and custom clients can vary in redirect URI shape,
requested scopes, refresh-token behavior, dynamic-client-registration support,
and `_meta` contents. Claude Desktop local stdio MCP is different: it does not
use this remote OAuth path.

Protocol references:

- MCP Streamable HTTP transport:
  <https://modelcontextprotocol.io/specification/2025-11-25/basic/transports>
- OpenAI MCP server guide:
  <https://developers.openai.com/api/docs/mcp>
- ChatGPT developer mode guide:
  <https://developers.openai.com/api/docs/guides/developer-mode>
- MCP authorization specification:
  <https://modelcontextprotocol.io/specification/draft/basic/authorization>

## Option A: Private Pilot Bearer Token

Use this only for a private pilot where the company controls the endpoint and
participants.

```bash
GONGMCP_BEARER_TOKEN_FILE=/run/secrets/gongmcp_token \
GONGMCP_ALLOWED_ORIGINS=https://approved-client.example.com \
GONGMCP_TOOL_PRESET=business-pilot \
  gongmcp \
    --http 127.0.0.1:8080 \
    --auth-mode bearer \
    --db /srv/gongctl/gong-mcp-governed.db
```

During token rotation, `gongmcp` can accept both the current and previous
mounted token files:

```bash
GONGMCP_BEARER_TOKEN_FILE=/run/secrets/gongmcp_token_current \
GONGMCP_BEARER_TOKEN_PREVIOUS_FILE=/run/secrets/gongmcp_token_previous \
GONGMCP_ALLOWED_ORIGINS=https://approved-client.example.com \
GONGMCP_TOOL_PRESET=business-pilot \
  gongmcp --http 127.0.0.1:8080 --auth-mode bearer --db /srv/gongctl/gong-mcp-governed.db
```

Put TLS, DNS, and network access in front of that loopback service using a
customer gateway. This is not enough for ChatGPT's app-style OAuth flow, but it
is useful for curl, MCP Inspector, internal gateway testing, and controlled
service-to-service pilots. If the approved client sends an `Origin` header,
that origin must match `GONGMCP_ALLOWED_ORIGINS`; unexpected origins receive
`403 Forbidden` before auth or tool dispatch.

## Option B: OAuth Broker In Front Of `gongmcp`

Use this when the customer wants real SSO/OAuth compatibility without modifying
`gongmcp` yet.

```text
Remote MCP client
  -> HTTPS /mcp
  -> OAuth/MCP broker or gateway
  -> bearer-authenticated internal HTTP
  -> gongmcp --http 127.0.0.1:8080 --auth-mode bearer
  -> read-only SQLite cache
```

The broker owns:

- OAuth discovery endpoints
- user login through the customer's IdP
- consent and scope presentation
- access-token issuance
- token validation and mapping to internal bearer auth
- Origin validation at the edge plus a matching internal `GONGMCP_ALLOWED_ORIGINS`
  policy when browser clients are involved
- audit logging for user/app/tool access

`gongmcp` owns:

- MCP tool catalog
- tool preset/allowlist enforcement
- read-only SQLite queries
- result-size limits

## OAuth Broker Options

| Broker option | Fit | Notes |
| --- | --- | --- |
| Cloudflare Access plus Cloudflare OAuth Provider Library | Fastest public example path | Cloudflare documents Access, third-party OAuth providers, bring-your-own providers such as Stytch/Auth0/WorkOS, and self-handled auth patterns for MCP servers. |
| JumpCloud-backed broker | Likely customer fit when JumpCloud is the IdP | JumpCloud handles identity; the broker still needs to implement MCP OAuth/resource metadata and issue MCP-compatible access tokens. |
| AWS Cognito-backed broker | Likely customer fit on AWS | Cognito handles hosted UI and OIDC; the broker still needs MCP resource metadata, token audience/scope validation, and client registration strategy. |
| Auth0, Stytch, WorkOS, Keycloak, Entra, Okta | Valid alternatives | Choose based on the customer's existing IdP, DCR/static-client support, audit requirements, and deployment platform. |
| Native `gongmcp --auth-mode oauth2` | Future repo work | Would reduce moving parts but needs careful implementation and interop testing across MCP clients. |

Cloudflare reference:

- <https://developers.cloudflare.com/agents/model-context-protocol/authorization/>

## JumpCloud Pattern

Use JumpCloud as the human identity provider, not as the whole MCP
authorization layer.

Recommended customer implementation:

1. Configure JumpCloud OIDC/SAML for the customer's broker or gateway.
2. Broker authenticates users through JumpCloud.
3. Broker enforces group membership such as `gong-mcp-users`.
4. Broker issues MCP-compatible access tokens for `https://gong-mcp.example.com/mcp`.
5. Broker maps approved scopes to the internal `gongmcp` tool preset or
   allowlist.
6. Broker forwards only approved MCP requests to `gongmcp`.

Minimum scopes to define for a first read-only pilot:

- `gongmcp:status`
- `gongmcp:aggregate`
- `gongmcp:search`

Keep sync/admin operations out of MCP scopes.

## AWS Cognito Pattern

Use Cognito when the customer wants AWS-managed hosted UI, user pools, and OIDC
integration.

Recommended customer implementation:

1. Cognito user pool or federation to the customer's IdP.
2. Broker validates Cognito-issued identity tokens for login.
3. Broker issues or exchanges for MCP-compatible access tokens scoped to the
   MCP resource.
4. ALB/API Gateway/CloudFront terminates TLS.
5. Internal service forwards to `gongmcp` with a customer-managed bearer token
   or validates tokens directly if native OAuth support is later added.

Do not expose the writable `gongctl` container through the same connector.

## ChatGPT Connector Setup

For ChatGPT app/developer-mode testing, the customer needs a remote MCP server
URL that ChatGPT can reach over HTTPS.

Checklist:

1. Deploy `gongmcp` behind the customer's HTTPS OAuth broker.
2. Confirm the MCP endpoint is reachable at `https://.../mcp`.
3. Confirm the endpoint advertises or otherwise supports the auth flow required
   by the target ChatGPT connector path.
4. Keep the initial tool preset narrow, usually `business-pilot`.
5. Add the remote MCP server in ChatGPT Apps/Connectors developer mode.
6. Test with `get_sync_status` before enabling transcript or CRM-value search
   tools.
7. Review ChatGPT workspace settings, model/data controls, and approval prompts
   before real customer data is used.

For Responses API testing, OpenAI's remote MCP tool uses a `server_url` and may
also need an OAuth `authorization` value. The customer application owns OAuth
client registration and token acquisition.

Minimal API smoke shape:

```python
from openai import OpenAI

client = OpenAI()
access_token = "ACCESS_TOKEN_FROM_CUSTOMER_BROKER"

response = client.responses.create(
    model="MODEL_APPROVED_BY_CUSTOMER",
    input=(
        "Use get_sync_status. Tell me whether the cache, profile, and "
        "transcript coverage are ready for strict business-pilot prompts."
    ),
    tools=[
        {
            "type": "mcp",
            "server_label": "gongmcp",
            "server_url": "https://gong-mcp.example.com/mcp",
            "authorization": f"Bearer {access_token}",
            "allowed_tools": ["get_sync_status"],
        }
    ],
)
```

Common failures to check first are an unreachable `server_url`, invalid or
missing authorization, and `allowed_tools` names that do not match the MCP tool
catalog.

Remote-client OAuth failures that look like a generic connector error usually
fall into one of these buckets:

| Symptom | Likely cause | What to check |
| --- | --- | --- |
| Dynamic client registration rejected | IdP/broker registration policy blocked the MCP client | trusted hosts, redirect URI policy, allowed scopes, client auth method |
| Browser login succeeds, token exchange fails | refresh/offline token policy or client grant policy is missing | `offline_access`, refresh-token grant, public/confidential client settings |
| Authenticated `/mcp` gets 401 | token issuer, audience/resource, expiry, signature, or group claim is wrong | gateway/shim logs and decoded access token claims |
| Tools import but first tool call fails | MCP JSON payload compatibility problem | `_meta` tolerance, argument schema, result size, tool allowlist |

For the step-by-step incident runbook, see
[Remote MCP OAuth troubleshooting](runbooks/remote-mcp-oauth-troubleshooting.md).

## Claude Remote Setup

Claude Desktop local stdio setup can use `scripts/install-claude-stdio-mcp.sh`.
Claude add-by-URL flows are remote-server flows and should receive an
`https://.../mcp` endpoint from the customer's gateway.

The OAuth checklist above applies to Claude remote MCP as well as ChatGPT. It
does not apply to local Claude Desktop stdio MCP, where the trust boundary is
the local host configuration and OS user account rather than browser OAuth.

Use local stdio for quick desktop tests. Use remote HTTPS/OAuth only when the
customer has decided who owns TLS, auth, logging, retention, and user access.

## Smoke Tests

Bearer pilot smoke:

```bash
curl -i https://gong-mcp.example.com/mcp \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}'
```

Expected:

- unauthorized request returns `401`
- authorized request returns only approved tools
- `get_sync_status` works before any search tools are enabled
- HTTP access logs include request ID, JSON-RPC method, tool name when present,
  HTTP status, duration, remote address, and auth mode. They do not contain
  tool arguments, transcripts, CRM values, tokens, or raw payload bodies.

## Backlog For Native OAuth

Native OAuth in `gongmcp` should be a separate implementation slice.

Likely scope:

- `--auth-mode oauth2`
- Protected Resource Metadata endpoint at
  `/.well-known/oauth-protected-resource` and, if needed, endpoint-scoped
  metadata such as `/.well-known/oauth-protected-resource/mcp`
- `WWW-Authenticate` 401 challenges with `resource_metadata` and required
  scope hints
- issuer/audience/scope configuration
- JWKS discovery and cache
- business-level per-tool scope mapping such as `gong_evidence.search`,
  `gong_evidence.read_excerpt`, `account_timeline.read`, `brief.generate`, and
  `profiles.list`
- structured audit log for MCP calls
- interop tests with MCP Inspector, ChatGPT developer mode, Claude remote
  add-by-URL, and MCP extension fields such as `_meta`
