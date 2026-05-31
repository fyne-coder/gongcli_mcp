# Remote MCP Auth Examples

These examples show the auth boundary in front of `gongmcp` for customer-hosted
remote MCP pilots.

Before choosing an example, read the operator checklist in
[Remote MCP deployment requirements](../../docs/remote-mcp-deployment-requirements.md).
It covers hosted-connector vs local-client paths, IdP vs broker
responsibilities, and the smoke test sequence.

The common shape is:

```text
Remote MCP client
  -> public HTTPS /mcp
  -> OAuth/MCP broker or gateway
  -> internal bearer-authenticated HTTP
  -> gongmcp --http 0.0.0.0:8080 --auth-mode bearer
  -> read-only governed SQLite cache in these examples
```

Hosted ChatGPT/OpenAI and Claude/Anthropic connector paths require that public
HTTPS `/mcp` URL because provider infrastructure initiates the request. Local
stdio, localhost HTTP, private DNS, or VPN-only URLs are valid only for MCP
clients running inside the company boundary.

If every MCP client runs inside the company boundary, you may not need these
remote-auth examples. Use stdio or a loopback/private HTTP bridge for Claude
Desktop, Claude Code, Codex, Cursor, MCP Inspector, or custom internal clients,
then add this gateway/broker layer only when hosted connectors or cross-boundary
clients need it.

Use these examples as implementation starters, not production identity-provider
defaults. Each customer still needs to review redirect URI policy, token
lifetimes, refresh/offline-token behavior, allowed users/groups, logging,
network placement, and secret storage.

Important distinction:

- `cloudflare-worker/` is the full MCP-facing OAuth broker example with Dynamic
  Client Registration.
- `direct-oidc-jumpcloud/` is the company-generic direct OIDC starter for
  hosted clients that can use a pre-registered/static OIDC client.
- `aws-cognito-gateway/` is an AWS-specific fallback path. It defaults to a
  pre-registered Cognito app client and includes optional Cognito app-client
  DCR for Claude fallback testing.
- `jumpcloud/` and `cognito/` are static-client/JWT-validation gateway
  examples. Their `/mcp` route is handled by a shim/broker that validates an
  incoming bearer JWT before forwarding to `gongmcp`; when adapting that shim to
  a real IdP, fetch keys from the provider's OIDC discovery `jwks_uri` and
  verify issuer, audience, token format, and group/email claims instead of
  assuming one provider's certificate path. Trusted proxy identity headers are
  disabled by default; enable `TRUST_PROXY_HEADERS=1` only behind a reviewed
  upstream gateway that overwrites those headers, and set `TRUST_PROXY_CIDRS` to
  that exact gateway source range. The bundled Caddy `/mcp` route strips inbound
  proxy identity headers before forwarding. The included `oauth2-proxy` service
  is a browser/session helper for rehearsing the IdP login path; it does not
  make JumpCloud or Cognito provide Dynamic Client Registration, Client ID
  Metadata Document support, or MCP-scoped access-token issuance.

## Which Example To Use

| Example | Use when | Client-registration model |
| --- | --- | --- |
| `direct-oidc-jumpcloud/` | The company wants a hosted-client direct OIDC gateway without making Cognito the default. | Pre-registered/static OIDC client first; Cognito only as fallback. |
| `jumpcloud/` | JumpCloud is the company IdP and the target MCP client can use a pre-registered/static OAuth client. | Static-client direct OIDC gateway shape. |
| `cloudflare-worker/` | The customer can deploy Cloudflare Workers and wants the recommended MCP-shaped broker. | Dynamic Client Registration through Cloudflare's OAuth Provider Library. |
| `aws-cognito-gateway/` | The customer explicitly chooses Cognito, optionally federated to JumpCloud, or needs Cognito DCR fallback testing. | Pre-registered Cognito client by default; optional gateway-backed DCR that creates real Cognito app clients. |
| `cognito/` | AWS Cognito is the customer IdP and the target MCP client can use a pre-registered/static OAuth client. | Static-client fallback. |

JumpCloud, Okta, Entra, Auth0, Keycloak, and Cognito are identity providers in
these examples. They do not replace the MCP gateway requirement for MCP
metadata, protected-resource challenges, policy enforcement, and private
upstream proxying. Use the
[AWS Cognito MCP gateway starter](aws-cognito-gateway/README.md) only when the
company explicitly chooses Cognito or when direct OIDC cannot satisfy the target
hosted client's OAuth registration/token behavior. Optional Cognito DCR creates
real Cognito app clients for compatibility testing; it is not the default
JumpCloud or direct-OIDC path.

Direct-OIDC provider support must be verified against the provider's actual
access-token shape. In particular, JumpCloud/Ory tokens may differ from
Cognito-style assumptions for `token_use`, `client_id`, `scope`, `aud`, and
nested group/email claims. Treat those differences as gateway provider-profile
compatibility work, not as a reason to make Cognito mandatory.

For Claude custom connectors, a successful metadata probe followed by
unauthenticated `/mcp` POSTs normally means Claude could not complete a supported
registration/token flow, or the public edge is still routing `/mcp` to a
browser-session proxy. Route `/mcp` to the MCP broker/shim, keep `oauth2-proxy`
limited to browser-login rehearsal paths, and use the Cloudflare Worker example
when the client needs a DCR-capable broker.

## Files

- `cloudflare-worker/`: Worker scaffold for the recommended broker path.
- `direct-oidc-jumpcloud/`: company-generic direct OIDC gateway starter.
- `aws-cognito-gateway/`: AWS/Cognito gateway starter for Claude custom
  connectors using pre-registered Cognito OAuth credentials, with optional DCR
  fallback.
- `jumpcloud/docker-compose.yml`: Compose gateway using JumpCloud OIDC.
- `jumpcloud/jumpcloud.env.example`: JumpCloud pilot settings.
- `cognito/docker-compose.yml`: Compose gateway using Cognito OIDC.
- `cognito/cognito.env.example`: Cognito pilot settings.

## Internal `gongmcp` Contract

All examples in this directory keep the same SQLite/file-mounted internal
`gongmcp` service contract:

- `gongmcp` receives no Gong API credentials.
- `gongmcp` reads a mounted SQLite DB at `/data/gong.db`.
- `gongmcp` requires `GONGMCP_BEARER_TOKEN_FILE`.
- `GONGMCP_TOOL_PRESET` starts narrow, usually `business-pilot`.
- `GONGMCP_ALLOWED_ORIGINS` must match the browser/client Origin used in the
  pilot.

For a Postgres-backed remote MCP deployment, keep the same auth boundary but run
`gongmcp` with `GONG_DATABASE_URL` or `DATABASE_URL` instead of `--db` and do not
mount `GONGMCP_DB`. Use the Postgres client deployment runbook for the source
database, redacted serving database, and scoped reader grants.
