# Remote MCP Auth Examples

These examples show the auth boundary in front of `gongmcp` for customer-hosted
remote MCP pilots.

The common shape is:

```text
Remote MCP client
  -> public HTTPS /mcp
  -> OAuth/MCP broker or gateway
  -> internal bearer-authenticated HTTP
  -> gongmcp --http 0.0.0.0:8080 --auth-mode bearer
  -> read-only governed SQLite cache
```

Use these examples as implementation starters, not production identity-provider
defaults. Each customer still needs to review redirect URI policy, token
lifetimes, refresh/offline-token behavior, allowed users/groups, logging,
network placement, and secret storage.

Important distinction:

- `cloudflare-worker/` is the only example here shaped as an MCP-facing OAuth
  broker with Dynamic Client Registration.
- `jumpcloud/` and `cognito/` are static-client/JWT-validation gateway
  examples. Their `/mcp` route is handled by the shim, which validates an
  incoming bearer JWT or trusted proxy identity header before forwarding to
  `gongmcp`. The included `oauth2-proxy` service is a browser/session helper for
  rehearsing the IdP login path; it does not make JumpCloud or Cognito provide
  Dynamic Client Registration or MCP-scoped access-token issuance.

## Which Example To Use

| Example | Use when | Client-registration model |
| --- | --- | --- |
| `cloudflare-worker/` | The customer can deploy Cloudflare Workers and wants the recommended MCP-shaped broker. | Dynamic Client Registration through Cloudflare's OAuth Provider Library. |
| `jumpcloud/` | JumpCloud is the customer IdP and the target MCP client can use a pre-registered/static OAuth client. | Static-client fallback. |
| `cognito/` | AWS Cognito is the customer IdP and the target MCP client can use a pre-registered/static OAuth client. | Static-client fallback. |

JumpCloud and Cognito are identity providers in these examples. They do not
replace the MCP broker requirement for clients that require Dynamic Client
Registration or MCP-specific token issuance.

## Files

- `cloudflare-worker/`: Worker scaffold for the recommended broker path.
- `jumpcloud/docker-compose.yml`: Compose gateway using JumpCloud OIDC.
- `jumpcloud/jumpcloud.env.example`: JumpCloud pilot settings.
- `cognito/docker-compose.yml`: Compose gateway using Cognito OIDC.
- `cognito/cognito.env.example`: Cognito pilot settings.

## Internal `gongmcp` Contract

All examples keep the same internal `gongmcp` service contract:

- `gongmcp` receives no Gong API credentials.
- `gongmcp` reads a mounted SQLite DB at `/data/gong.db`.
- `gongmcp` requires `GONGMCP_BEARER_TOKEN_FILE`.
- `GONGMCP_TOOL_PRESET` starts narrow, usually `business-pilot`.
- `GONGMCP_ALLOWED_ORIGINS` must match the browser/client Origin used in the
  pilot.
