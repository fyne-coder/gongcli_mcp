# Cloudflare Worker OAuth Broker Example

This is the recommended broker shape for a production-style remote MCP
endpoint when the target hosted client cannot use the customer's direct
static-client IdP setup, or when the customer wants a broker-owned OAuth
surface. Direct JumpCloud can work with Claude when the custom connector and
JumpCloud app are configured with the right static-client settings; use this
Worker path as the broker fallback or as the preferred architecture when the
customer wants OAuth/DCR separated from the IdP.

It uses Cloudflare's OAuth Provider Library to handle MCP-facing OAuth metadata,
PKCE, token issuance, and Dynamic Client Registration. The Worker then forwards
authorized `/mcp` requests to an internal bearer-protected `gongmcp` HTTP
service.

```text
Remote MCP client
  -> https://gong-mcp.example.com/mcp
  -> Cloudflare Worker OAuth Provider
  -> internal bearer-authenticated HTTP
  -> gongmcp --http 0.0.0.0:8080 --auth-mode bearer
  -> read-only governed SQLite cache, or a Postgres reader configured on gongmcp
```

This Worker is database-agnostic. The upstream `gongmcp` can be the SQLite
file-mounted shape or the Postgres reader shape; Postgres uses
`GONG_DATABASE_URL` or `DATABASE_URL` on `gongmcp` and no `--db` flag.

## Cloudflare Resources

Create:

- a Worker route or custom domain for `https://gong-mcp.example.com`
- a KV namespace bound as `OAUTH_KV`
- a reachable internal `gongmcp` HTTPS URL, for example through Cloudflare
  Tunnel, private service routing, or a customer gateway
- a Cloudflare Access policy or equivalent auth layer protecting `/authorize`;
  for a JumpCloud tenant, configure JumpCloud as the Access identity provider so
  JumpCloud remains the human login and group source

## Configure

Install dependencies:

```bash
npm install
```

Create the KV namespace and update `wrangler.jsonc`:

```bash
npx wrangler kv namespace create OAUTH_KV
```

Set the internal bearer token as a Worker secret:

```bash
npx wrangler secret put GONGMCP_INTERNAL_BEARER_TOKEN
```

Set non-secret variables in `wrangler.jsonc`:

| Variable | Purpose |
| --- | --- |
| `PUBLIC_BASE_URL` | Public Worker base URL, for example `https://gong-mcp.example.com` |
| `GONGMCP_UPSTREAM_URL` | Internal HTTPS URL for `gongmcp`, including `/mcp` if the upstream gateway requires it |
| `GONGMCP_ALLOWED_SCOPES` | Space-separated scopes the broker can grant |
| `PILOT_ALLOWED_EMAILS` | Comma-separated pilot allowlist; leave empty only if Access policy fully owns authorization |

## Deploy

```bash
npx wrangler deploy
```

## Smoke

Unauthenticated MCP requests should challenge for OAuth:

```bash
curl -i https://gong-mcp.example.com/mcp
```

Expected implementation properties:

- protected-resource and authorization-server metadata resolve
- Dynamic Client Registration works at `/register`
- authorization-code + PKCE token exchange works at `/token`
- authorized `/mcp` requests are forwarded to `gongmcp` with only the internal
  bearer token
- `tools/list` returns only the configured `gongmcp` tool preset

This scaffold assumes Cloudflare Access, or an equivalent upstream auth check,
sets `CF-Access-Authenticated-User-Email` before `/authorize` is reached. For
JumpCloud pilots, keep JumpCloud behind Access for the user-facing login and let
the Worker expose the MCP-facing OAuth/DCR surface to Claude. Replace
`currentUserEmail()` with the customer's preferred login/SSO handler if Access is
not the auth layer.
