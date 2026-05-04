# Cloudflare Worker OAuth Broker Example

This is the recommended first pilot broker shape for a production-style remote
MCP endpoint.

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
  -> read-only governed SQLite cache
```

## Cloudflare Resources

Create:

- a Worker route or custom domain for `https://gong-mcp.example.com`
- a KV namespace bound as `OAUTH_KV`
- a reachable internal `gongmcp` HTTPS URL, for example through Cloudflare
  Tunnel, private service routing, or a customer gateway
- a Cloudflare Access policy or equivalent auth layer protecting `/authorize`

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
sets `CF-Access-Authenticated-User-Email` before `/authorize` is reached. Replace
`currentUserEmail()` with the customer's preferred login/SSO handler if Access is
not the auth layer.
