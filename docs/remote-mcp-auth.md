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
- HTTP `/mcp` for private pilots
- static bearer-token validation for HTTP mode
- required HTTP tool allowlist
- read-only SQLite access
- non-local bind guardrails

It does not currently implement native OAuth 2.1, dynamic client registration,
per-user RBAC, tenant routing, or a browser-facing consent application.

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
- OAuth 2.1-compatible authorization flow for HTTP MCP clients
- protected-resource metadata for the MCP resource
- authorization-server discovery through OAuth/OIDC metadata
- PKCE support for public clients
- dynamic client registration or a documented static-client fallback supported
  by the target MCP client
- scoped access tokens with issuer, audience, expiry, and scope validation
- tool-level authorization mapping, even if the first pilot maps all approved
  users to the same read-only tool allowlist
- no Gong credentials in the MCP client or OAuth broker

Protocol references:

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
GONGMCP_TOOL_ALLOWLIST=get_sync_status,summarize_calls_by_lifecycle,summarize_call_facts,rank_transcript_backlog \
  gongmcp \
    --http 127.0.0.1:8080 \
    --auth-mode bearer \
    --db /srv/gongctl/gong-mcp-governed.db
```

Put TLS, DNS, and network access in front of that loopback service using a
customer gateway. This is not enough for ChatGPT's app-style OAuth flow, but it
is useful for curl, MCP Inspector, internal gateway testing, and controlled
service-to-service pilots.

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
- audit logging for user/app/tool access

`gongmcp` owns:

- MCP tool catalog
- tool allowlist enforcement
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
5. Broker maps approved scopes to the internal `gongmcp` tool allowlist.
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
4. Keep the initial tool allowlist narrow.
5. Add the remote MCP server in ChatGPT Apps/Connectors developer mode.
6. Test with `get_sync_status` before enabling transcript or CRM-value search
   tools.
7. Review ChatGPT workspace settings, model/data controls, and approval prompts
   before real customer data is used.

For Responses API testing, OpenAI's remote MCP tool uses a `server_url` and may
also need an OAuth `authorization` value. The customer application owns OAuth
client registration and token acquisition.

## Claude Remote Setup

Claude Desktop local stdio setup can use `scripts/install-claude-stdio-mcp.sh`.
Claude add-by-URL flows are remote-server flows and should receive an
`https://.../mcp` endpoint from the customer's gateway.

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
- logs do not contain transcripts, CRM values, tokens, or raw payload bodies

## Backlog For Native OAuth

Native OAuth in `gongmcp` should be a separate implementation slice.

Likely scope:

- `--auth-mode oauth2`
- protected-resource metadata endpoint
- issuer/audience/scope configuration
- JWKS discovery and cache
- per-tool scope mapping
- structured audit log for MCP calls
- interop tests with MCP Inspector, ChatGPT developer mode, and Claude remote
  add-by-URL
