# Remote MCP Auth And Connector Setup

## Purpose

This guide explains how a customer can expose `gongmcp` as a remote HTTPS MCP
endpoint for approved users without turning this repo into a vendor-hosted
service.

For the DevOps-facing deployment checklist, infrastructure requirements, smoke
test sequence, and security guardrails, start with
[Remote MCP deployment requirements](remote-mcp-deployment-requirements.md).
This document focuses on auth boundaries, broker options, and connector setup.

Current boundary:

- `gongmcp` serves read-only MCP over an existing customer-controlled cache:
  SQLite via `--db PATH`, or Postgres via `GONG_DATABASE_URL` / `DATABASE_URL`
  with no `--db`.
- `gongmcp` can enforce bearer auth itself for private pilots.
- Production SSO/OAuth should normally sit in a customer-managed gateway,
  broker, or application layer in front of `gongmcp`.
- The writable `gongctl` sync job remains separate and should not be exposed to
  ChatGPT, Claude, or end-user MCP clients.

## Hosted Client Reachability Requirement

Hosted chat clients and hosted API MCP connectors do not call `gongmcp` from
the user's browser or laptop. ChatGPT Apps/connectors, OpenAI remote MCP tool
calls, Claude.ai connectors, and Anthropic-hosted MCP connector calls originate
from the provider's infrastructure. For those paths, the customer edge MCP
endpoint must be reachable from the public internet over HTTPS, usually at a
URL like:

```text
https://gong-mcp.example.com/mcp
```

A `localhost`, stdio-only, RFC1918/private-network, VPN-only, or internal DNS
endpoint will not work for those hosted connector paths unless the provider's
backend can reach it. "Public" does not mean exposing the raw internal MCP
process. The production shape should be:

```text
Hosted MCP client
  -> public HTTPS /mcp at the customer edge
  -> OAuth/MCP broker, API gateway, tunnel, WAF, or reverse proxy
  -> internal bearer-authenticated HTTP
  -> private gongmcp service
  -> read-only SQLite cache or Postgres serving DB
```

Cloudflare Tunnel, ngrok, or an equivalent managed tunnel is acceptable for a
short lab or demo when it terminates at, or forwards through, an auth boundary.
Do not tunnel directly to unauthenticated `gongmcp`. Production customer
deployments should prefer the company's standard public edge, identity,
logging, rate-limit, and secret-rotation controls. Keep the upstream `gongmcp`
service private and authenticated with an internal bearer token unless a future
native OAuth mode is implemented and tested.

Local clients are different. Claude Desktop, Claude Code, Codex, Cursor, MCP
Inspector, or a custom MCP client running inside the company network can use
stdio, localhost, or private HTTP when they run in the same trust boundary as
the MCP server.

## Implemented Now

`gongmcp` supports:

- stdio MCP for local desktop hosts
- Streamable HTTP-style POST `/mcp` for private pilots
- GET `/mcp` returns 405 because server-sent streaming is not implemented yet
- static bearer-token validation for HTTP mode
- required HTTP tool preset or allowlist
- Origin validation for HTTP requests
- read-only SQLite access, plus reviewed read-only Postgres presets and
  allowlists
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
| End-user remote MCP | ChatGPT/Claude-style shared user access | Customer HTTPS plus direct OIDC gateway or OAuth/MCP broker | Required for production remote use |
| Native OAuth in `gongmcp` | Direct OAuth resource-server deployment | `gongmcp` validates issuer/audience/scopes | Not implemented yet |

## Remote MCP Auth Decision

A customer-hosted remote MCP deployment has two separate auth decisions:

| Decision | Owner | Recommended pilot choice |
| --- | --- | --- |
| Human login and SSO | Customer IT/security | Existing IdP such as JumpCloud, Okta, Entra, Auth0, Keycloak, Cognito, or Cloudflare Access |
| MCP-compatible token validation, discovery, and forwarding | Customer platform/security | Direct OIDC gateway or OAuth broker in front of `gongmcp`, or a future native OAuth layer |

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
- for Claude custom connectors specifically, one of the supported connector
  auth modes such as Dynamic Client Registration, Client ID Metadata Document,
  or Anthropic-held client credentials; user-pasted static bearer tokens are not
  a Claude.ai custom connector auth mode
- scoped access tokens with issuer, audience/resource, expiry, and scope
  validation
- refresh-token or offline-token policy that matches the target client behavior
- tool-level authorization mapping, even if the first pilot maps all approved
  users to the same read-only tool preset
- user identity plus dedicated group, role, or temporary smoke-test email claims
  in the access token when the gateway enforces per-user access
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
requested scopes, refresh-token behavior, supported client-registration modes,
and `_meta` contents. Claude Desktop local stdio MCP is different: it does not
use this remote OAuth path. Claude custom connectors can use remote MCP through
Claude.ai infrastructure, but the auth server still needs a Claude-supported
OAuth registration or client-credential strategy before Claude will send bearer
tokens to `/mcp`.

Protocol references:

- MCP Streamable HTTP transport:
  <https://modelcontextprotocol.io/specification/2025-11-25/basic/transports>
- OpenAI Apps SDK quickstart for adding a connector to ChatGPT:
  <https://developers.openai.com/apps-sdk/quickstart#add-your-app-to-chatgpt>
- OpenAI MCP server guide:
  <https://developers.openai.com/api/docs/mcp>
- ChatGPT developer mode guide:
  <https://developers.openai.com/api/docs/guides/developer-mode>
- Anthropic MCP connector limitations:
  <https://docs.anthropic.com/en/docs/agents-and-tools/mcp-connector>
- Claude connector authentication:
  <https://claude.com/docs/connectors/building/authentication>
- Claude Code MCP local and remote transport setup:
  <https://docs.anthropic.com/en/docs/claude-code/mcp>
- JumpCloud OIDC SSO:
  <https://jumpcloud.com/support/sso-with-oidc>
- MCP authorization specification:
  <https://modelcontextprotocol.io/specification/draft/basic/authorization>

## Option A: Private Pilot Bearer Token

Use this only for a private pilot where the company controls the endpoint and
participants.

The bearer token must be random, at least 32 characters, and contain no
whitespace or control characters. Prefer a mounted secret file generated by the
customer secret manager or a command such as `openssl rand -base64 32`.

```bash
GONGMCP_BEARER_TOKEN_FILE=/run/secrets/gongmcp_token \
GONGMCP_ALLOWED_ORIGINS=https://approved-client.example.com \
GONGMCP_TOOL_PRESET=business-pilot \
  gongmcp \
    --http 127.0.0.1:8080 \
    --auth-mode bearer \
    --db /srv/gongctl/gong-mcp-governed.db
```

For Postgres, omit `--db` and set `GONG_DATABASE_URL` or `DATABASE_URL` to the
read-only MCP role. Customer-exclusion deployments should point that URL at the
physically redacted serving database produced by
`gongctl governance refresh-serving-db`.

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

Bearer-only HTTP is not a production substitute for the public edge
gateway/broker when the endpoint is reachable from the internet. Use it as the
private upstream leg or for controlled internal tests.

## Option B: OAuth Broker In Front Of `gongmcp`

Use this when the customer wants real SSO/OAuth compatibility without modifying
`gongmcp` yet.

```text
Remote MCP client
  -> HTTPS /mcp
  -> OAuth/MCP broker or gateway
  -> bearer-authenticated internal HTTP
  -> gongmcp --http 127.0.0.1:8080 --auth-mode bearer
  -> read-only SQLite cache or Postgres reader
```

The broker owns:

- OAuth discovery endpoints
- user login through the customer's IdP
- consent and scope presentation
- access-token issuance
- token validation and mapping to internal bearer auth
- Origin validation at the edge plus a matching internal
  `GONGMCP_ALLOWED_ORIGINS` policy when browser clients are involved
- audit logging for user/app/tool access

For companies whose IdP can issue usable OIDC access tokens directly, prefer
the direct OIDC gateway pattern first. It keeps the moving parts close to the
Keycloak proof: the gateway publishes MCP metadata, validates the IdP token
issuer/audience/scope/user policy, and forwards approved requests to private
`gongmcp` with the internal bearer token.

For AWS-first deployments where the company explicitly wants Cognito hosted UI,
or where direct OIDC/static-client testing cannot satisfy the hosted client's
registration or token behavior, use the
[AWS Cognito MCP gateway starter](../deploy/remote-mcp-auth/aws-cognito-gateway/README.md)
as a fallback. Treat optional Cognito DCR as a compatibility path that needs a
live Claude smoke before customer production rollout.

`gongmcp` owns:

- MCP tool catalog
- tool preset/allowlist enforcement
- read-only cache queries over SQLite or reviewed Postgres reader surfaces
- result-size limits

## OAuth Broker Options

| Broker option | Fit | Notes |
| --- | --- | --- |
| Cloudflare Access plus Cloudflare OAuth Provider Library | Fastest public example path | Cloudflare documents Access, third-party OAuth providers, bring-your-own providers such as Stytch/Auth0/WorkOS, and self-handled auth patterns for MCP servers. |
| Direct OIDC gateway for JumpCloud, Okta, Entra, Auth0, Keycloak, or similar | Preferred first pilot when the IdP can issue access tokens the hosted client can obtain and the gateway can validate | The gateway owns MCP metadata, challenge behavior, scope/group policy, and private upstream proxying; Keycloak is the open-source surrogate proof. |
| AWS Cognito-backed broker | AWS-specific fallback | Use when the company wants Cognito hosted UI/user pools or needs Cognito DCR compatibility; do not make it the default solely because JumpCloud is present. |
| Cloudflare, Stytch, WorkOS, or custom broker | Valid alternatives | Choose based on DCR/static-client support, audit requirements, and deployment platform. |
| Native `gongmcp --auth-mode oauth2` | Future repo work | Would reduce moving parts but needs careful implementation and interop testing across MCP clients. |

Cloudflare reference:

- <https://developers.cloudflare.com/agents/model-context-protocol/authorization/>

## Recommended First Pilot Path

Use a direct OIDC gateway first when the company already has JumpCloud, Okta,
Entra, Auth0, Keycloak, or another OIDC provider that can support the hosted
client's OAuth flow. The gateway should own the MCP-specific work:

- OAuth protected-resource metadata for the `/mcp` resource
- authorization-server metadata/discovery
- Dynamic Client Registration, Client ID Metadata Document, or a documented
  static-client fallback accepted by the target hosted client
- PKCE-compatible authorization-code flow
- issuer, audience/resource, expiry, scope, and user/group claim validation
- forwarding approved requests to `gongmcp` with an internal bearer token

This keeps `gongmcp` simple and customer-controlled: it remains a read-only MCP
server over SQLite or reviewed Postgres reader surfaces, with no Gong
credentials and no native OAuth dependency.

Use Cloudflare Workers with the Cloudflare OAuth Provider Library when the
company wants a full MCP-shaped broker with Dynamic Client Registration. Use
Cognito when the company explicitly wants AWS-managed hosted UI/user pools or
when the direct OIDC path fails a live hosted-client OAuth proof.

## Provider Starters

For customers who want a concrete configurable example, use the provider-specific
starters under `deploy/remote-mcp-auth/`:

- [Remote MCP auth examples](../deploy/remote-mcp-auth/README.md)
- [JumpCloud Docker Compose](../deploy/remote-mcp-auth/jumpcloud/docker-compose.yml)
- [JumpCloud env example](../deploy/remote-mcp-auth/jumpcloud/jumpcloud.env.example)
- [TradeCentric / company direct OIDC handoff](../deploy/remote-mcp-auth/tradecentric-jumpcloud/README.md)
- [Cloudflare Worker OAuth broker](../deploy/remote-mcp-auth/cloudflare-worker/README.md)
- [AWS Cognito gateway fallback](../deploy/remote-mcp-auth/aws-cognito-gateway/README.md)
- [AWS Cognito Docker Compose](../deploy/remote-mcp-auth/cognito/docker-compose.yml)
- [AWS Cognito env example](../deploy/remote-mcp-auth/cognito/cognito.env.example)

The JumpCloud and Cognito Compose files are static-client/JWT-validation shapes,
not full OAuth brokers. In those examples, a shim or broker validates a bearer
JWT at `/mcp` and forwards approved requests to `gongmcp` with the internal
bearer token. Use them only when the target MCP client supports a
pre-registered/static OAuth client path or when an upstream gateway already
performs MCP-compatible OAuth work.

Hardening notes for static-client/JWT shims:

- Read the provider's OIDC discovery document and validate against its
  `jwks_uri`; do not assume a Keycloak-style certificate path.
- For JumpCloud, verify issuer string, access-token format, audience/client
  claim, and JWKS endpoint before treating JWT validation as working.
- Trusted proxy identity headers are disabled by default. Enable
  `TRUST_PROXY_HEADERS=1` only behind a reviewed upstream gateway that
  overwrites those headers, and set `TRUST_PROXY_CIDRS` to that gateway's source
  range.
- The bundled Caddy `/mcp` route strips inbound proxy identity headers before
  forwarding.
- The included `oauth2-proxy` service rehearses browser/session login only; it
  does not provide Dynamic Client Registration, Client ID Metadata Document
  support, or MCP-scoped access-token issuance for ChatGPT/Claude.

For the fully MCP-shaped DCR broker path, use the Cloudflare Worker example. It
uses Cloudflare's OAuth Provider Library for Dynamic Client Registration, PKCE,
token issuance, and MCP-facing metadata, then forwards authorized requests to
the same internal `gongmcp` bearer-auth service.

The older lab-auth stack remains useful as a disposable local rehearsal harness:

- [Lab auth deployment](lab-auth-deployment.md)
- [`deploy/lab-auth/docker-compose.yml`](../deploy/lab-auth/docker-compose.yml)
- [`deploy/lab-auth/.env.example`](../deploy/lab-auth/.env.example)

The lab uses Keycloak as a disposable OIDC provider, Caddy as the local gateway,
`oauth2-proxy` for the browser/session path, a small MCP auth shim for protected
resource metadata and token checks, and `gongmcp` as the read-only internal MCP
service.

```text
Remote MCP client
  -> HTTPS /mcp
  -> gateway/broker
  -> internal bearer auth
  -> gongmcp --http 0.0.0.0:8080 --auth-mode bearer
  -> read-only SQLite cache or Postgres reader
```

Minimum configuration values to replace in a customer pilot:

| Setting | Purpose |
| --- | --- |
| `PUBLIC_BASE_URL` / `LAB_PUBLIC_BASE_URL` | Public HTTPS MCP base URL, for example `https://gong-mcp.example.com` |
| `GONGMCP_DB` / `LAB_DB` / mounted DB path | SQLite starter cache path for MCP |
| `GONG_DATABASE_URL` / `DATABASE_URL` | Postgres reader URL for reviewed Postgres MCP deployments |
| `GONGMCP_TOOL_PRESET` | Initial tool surface, usually `business-pilot` |
| `GONGMCP_ALLOWED_ORIGINS` | Browser/client origins accepted by `gongmcp` |
| provider-specific OIDC client settings | Broker application credentials for the chosen IdP, such as `JUMPCLOUD_OIDC_CLIENT_ID` or `COGNITO_OIDC_CLIENT_ID` |
| approved email/group settings | User allowlist or group policy, such as `PILOT_ALLOWED_EMAILS` or `gong-mcp-users` |
| internal bearer token or token file | Secret used only between the broker/shim and `gongmcp`; random, at least 32 characters, no whitespace/control characters |

The lab and static-client Compose files are not production identity-provider
configurations. They are runnable shapes to copy when explaining the moving
pieces or validating direct bearer-token/JWT handling. For a production
Cloudflare Workers pilot, replace the Keycloak/oauth2-proxy/shim pieces with a
Worker using the Cloudflare OAuth Provider Library, and keep the same internal
`gongmcp` bearer-auth service. Use a read-only SQLite mount for SQLite starters
or `GONG_DATABASE_URL` / `DATABASE_URL` for Postgres reader deployments.

## JumpCloud And Direct OIDC Pattern

Use JumpCloud, Okta, Entra, Auth0, Keycloak, or a similar OIDC provider as the
human identity provider and token issuer. The MCP gateway is still the public
resource server boundary in front of private `gongmcp`.

Recommended customer implementation:

1. Configure the company IdP OIDC app for the gateway or hosted MCP client.
2. Configure the gateway with the IdP issuer, client ID, required scope, and
   allowed group/email/subject policy. For the current gateway starter, set the
   JWKS URL from the provider's OIDC discovery `jwks_uri` instead of relying on
   an inferred provider-specific path.
3. Gateway publishes MCP protected-resource metadata and `WWW-Authenticate`
   challenges for `https://gong-mcp.example.com/mcp`.
4. Hosted client completes OAuth and sends a bearer access token to `/mcp`.
5. Gateway validates issuer, JWKS signature, audience/client ID, expiry, scope,
   and approved user/group claim.
6. Gateway maps approved scopes to the internal `gongmcp` tool preset or
   allowlist.
7. Gateway forwards only approved MCP requests to `gongmcp`.

Minimum scopes to define for a first read-only pilot:

- `gongmcp:status`
- `gongmcp:aggregate`
- `gongmcp:search`

The gateway enforces these scopes today and maps approved scopes to the internal
`gongmcp` preset or allowlist. Keep sync/admin operations out of MCP scopes.

If Claude probes `/.well-known/oauth-protected-resource` successfully and then
POSTs `/mcp` without an `Authorization` header, do not debug that as a JumpCloud
password problem. It usually means Claude found metadata but could not complete
a supported registration/token flow, or the edge route is still sending `/mcp`
to a browser-session proxy instead of an MCP auth broker. `oauth2-proxy` can be
useful for browser login rehearsal, especially with PKCE S256 enabled where the
provider supports it, but it is not the full MCP OAuth broker.

Direct OIDC providers do not all use Cognito-shaped access tokens. Before
declaring the gateway compatible with a provider, decode a local test access
token and check the exact claims the gateway will enforce.

| Claim area | Cognito-shaped assumption | Direct OIDC / JumpCloud check |
| --- | --- | --- |
| Token type | `token_use=access` | Some providers omit this claim. Keep it required for Cognito, but make any relaxation provider-profile scoped. |
| Client identity | top-level `client_id` | Some providers bind the client or resource through `aud`; validate exactly what the issuer documents and what Claude receives. |
| Scope | space-delimited `scope` string | Some providers use `scp` as an array or alternate scope field. |
| Group | top-level `cognito:groups` or configured top-level claim | JumpCloud/Ory-shaped tokens may use `groups`, `memberOf`, or nested `ext.memberOf`; production should use one or more dedicated MCP groups. |
| Email | top-level `email` | Email can be absent from access tokens or nested under `ext`; do not use email allowlist as the final policy when groups are required. |

The current product gateway has historical Cognito-oriented validation names
and assumptions. JumpCloud/Ory compatibility should be implemented as an
explicit direct-OIDC provider profile with tests, not as a broad weakening of
the default Cognito checks.

## AWS Cognito Pattern

Use Cognito only when the customer wants AWS-managed hosted UI, user pools, and
OIDC integration, or when direct OIDC cannot satisfy the target hosted client's
OAuth registration/token behavior.

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
| Metadata probes succeed, then `/mcp` gets unauthenticated `401` or `403` | client could not complete a supported registration/token flow, or `/mcp` is still routed to a browser-session proxy such as `oauth2-proxy` | auth server metadata for DCR/CIMD/static client support, edge route for `/mcp`, `WWW-Authenticate` challenge, broker logs |
| Gateway logs `missing bearer token` | hosted client reached `/mcp` without an Authorization bearer token | static client setup, redirect URI, token exchange, and whether `/mcp` is routed to the MCP gateway |
| IdP reports `invalid client` during Claude auth | Claude and the IdP disagree on client ID, secret, redirect URI, or client authentication mode | recreate connector if needed, compare IdP app settings to Claude Advanced settings, verify `https://claude.ai/api/mcp/auth_callback` |
| Browser login succeeds, token exchange fails | refresh/offline token policy or client grant policy is missing | `offline_access`, refresh-token grant, public/confidential client settings |
| Authenticated `/mcp` gets 401 | token issuer, audience/resource, expiry, signature, or group claim is wrong | gateway/shim logs and decoded access token claims |
| Gateway logs `required group "..." missing` or `required group membership missing` | bearer token is present, but the configured group claim or user membership does not match policy | decode the access token locally; check `groups`, `memberOf`, nested `ext`, and whether the user belongs to at least one dedicated MCP group |
| Tools import but first tool call fails | MCP JSON payload compatibility problem | `_meta` tolerance, argument schema, result size, tool allowlist |

For the step-by-step incident runbook, see
[Remote MCP OAuth troubleshooting](runbooks/remote-mcp-oauth-troubleshooting.md).

For the TC/JumpCloud incident notes and code follow-up decision, see
[TradeCentric JumpCloud remote MCP RCA](runbooks/tc-jumpcloud-remote-mcp-rca-2026-05-29.md).

## Claude Remote Setup

Claude Desktop local stdio setup can use `scripts/install-claude-stdio-mcp.sh`.
Claude add-by-URL flows are remote-server flows and should receive an
`https://.../mcp` endpoint from the customer's gateway.

The OAuth checklist above applies to Claude remote MCP as well as ChatGPT. It
does not apply to local Claude Desktop stdio MCP, where the trust boundary is
the local host configuration and OS user account rather than browser OAuth.

For Claude custom connectors using a pre-registered/static client, enter:

```text
MCP server URL: https://gong-mcp.example.com/mcp
OAuth client ID: <company IdP app client ID>
OAuth client secret: <client secret, if the app is confidential>
Callback URL configured in IdP: https://claude.ai/api/mcp/auth_callback
```

The proof is not complete when the browser login page succeeds. The proof is
complete when Claude can call `get_sync_status` through the connector and the
gateway logs show an authenticated MCP request with the expected user policy.

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
Use maintained OAuth/OIDC and JWT/JWKS libraries for discovery, signature
verification, claim validation, refresh handling, and cache behavior; do not
extend the lab shim's hand-rolled token parsing into a production auth layer.

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
