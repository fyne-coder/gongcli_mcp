# Remote MCP OAuth Troubleshooting

Use this runbook when a remote MCP client such as ChatGPT, Claude remote
add-by-URL, MCP Inspector, or a custom client fails against an HTTPS `/mcp`
endpoint.

Local Claude Desktop stdio MCP does not use this OAuth path.
For hosted ChatGPT/OpenAI or Claude/Anthropic connector paths, the HTTPS
`/mcp` endpoint must be reachable from the provider's infrastructure, not only
from the user's browser, laptop, VPN, or private network.
For pre-deploy infrastructure, auth split, and the ordered smoke test sequence,
see [Remote MCP deployment requirements](../remote-mcp-deployment-requirements.md).

## Failure Ladder

Check the path in order:

0. Public DNS and TLS resolve from outside the company network when the target
   client is a hosted connector.
1. OAuth protected-resource metadata resolves.
2. Authorization-server metadata resolves.
3. The client can register dynamically, or the documented static client exists.
4. Browser login succeeds.
5. Authorization-code token exchange succeeds.
6. The access token has expected issuer, audience/resource, expiry, scopes, user
   identity, and group/role/email claims.
7. Unauthenticated `/mcp` returns `401` with `WWW-Authenticate` and
   `resource_metadata`.
8. Authenticated `/mcp` `initialize` succeeds.
9. `tools/list` returns the approved tool preset.
10. A first `tools/call` succeeds, including client extension fields such as
    `_meta`.

Browser login success alone is not enough.

For the direct OIDC or Cognito gateway branch, run the bundled validator before
asking a Claude user to retry. Use the issuer for the provider that actually
minted the access token. For direct JumpCloud, that is the JumpCloud OIDC
issuer, not a Cognito pool issuer.

```bash
gongctl doctor mcp-gateway \
  --url https://mcp.customer.example.com \
  --issuer https://issuer.customer.example.com \
  --profile direct-oidc \
  --origin https://claude.ai \
  --required-scope gongmcp/read \
  --group-claim memberOf \
  --client-id <Claude app client ID> \
  --required-group <one expected dedicated MCP group> \
  --token-env GONGMCP_TEST_ACCESS_TOKEN
```

Use `--profile cognito` for Cognito fallback gateways. Use `--expect-dcr` when
the gateway DCR fallback is enabled. Use `--token-env ENV_NAME` only from an
operator shell that already has a test access token; the doctor inspects JWT
payload shape as untrusted diagnostics and does not print the token, email
values, group names, or raw tool response bodies. Authenticated `tools/list`
failures map `401` to missing/invalid bearer auth, `403` to scope/group/email
policy, and `502`/`503`/`504` to private `gongmcp` reachability. For
automation, inspect `.checks[]`; normal validation failures are reported as
JSON checks rather than a nonzero process exit.

## Lab Commands

For the auth lab:

```bash
LAB_VM=ssh-user@lab-host.example.com \
LAB_PUBLIC_BASE_URL=https://lab.example.com \
  deploy/lab-auth/scripts/lab-smoke.sh
```

Use the right deploy mode before asking a user to reconnect:

- `LAB_DEPLOY_MODE=app-only` preserves Keycloak, dynamic OAuth clients, Caddy,
  oauth2-proxy, and the auth shim. Use it for normal MCP binary, payload, or
  tool-behavior updates when the public issuer/origin and auth settings are
  unchanged. Existing ChatGPT/Claude custom MCP connectors should remain
  authorized, though schema/description changes may still require a tool
  refresh.
- `LAB_DEPLOY_MODE=full` resets the disposable Keycloak volume. Use it for
  first deploys, issuer/origin changes, Keycloak/user/group/policy changes, or
  proxy/auth-stack changes. Existing dynamic clients are deleted, so users must
  recreate or reconnect custom MCP connectors afterward.

Inspect payload-free logs:

```bash
ssh "$LAB_VM" \
  'cd "${REMOTE_ROOT:-/srv/gongctl}/source/deploy/lab-auth" && docker-compose logs --tail=120 keycloak shim caddy gongmcp'
```

Run the external OpenAI Responses API smoke when `OPENAI_API_KEY` is available:

```bash
LAB_VM=ssh-user@lab-host.example.com \
LAB_PUBLIC_BASE_URL=https://lab.example.com \
  deploy/lab-auth/scripts/lab-openai-responses-smoke.sh
```

## Symptom Map

| Symptom | Likely cause | Next check |
| --- | --- | --- |
| Dynamic client registration rejected | IdP or broker registration policy blocked the client | trusted hosts, redirect URI, client auth method, allowed scopes |
| Metadata probes succeed, then Claude or another hosted client POSTs `/mcp` without auth and gets `401`/`403` | the client could not complete a supported registration/token flow, or `/mcp` is routed to a browser-session proxy instead of the MCP broker | authorization-server metadata for DCR/CIMD/static-client support, edge route for `/mcp`, `WWW-Authenticate` challenge, broker logs |
| Gateway logs `missing bearer token` | the hosted client reached `/mcp` without an Authorization bearer token | static client setup, redirect URI, token exchange, client auth method, and `/mcp` route target |
| IdP logs `invalid client` during Claude auth | Claude and the IdP disagree on client ID, secret, redirect URI, or client authentication mode | compare the IdP OIDC app to Claude Advanced settings; confirm `https://claude.ai/api/mcp/auth_callback`; recreate the connector if stale state is suspected |
| Login succeeds but token exchange fails | refresh/offline token policy, grant type, or client type mismatch | IdP events around code-to-token exchange |
| Authenticated `/mcp` gets 401 | token does not match gateway validation | issuer, audience/resource, expiry, signature, group claim shape, OIDC `jwks_uri` discovery |
| Browser login completes but Claude still gets 401 on `/mcp` | access-token claims do not match gateway policy, or the gateway is still assuming a different provider's token shape | decode the access token locally; check `iss`, `aud`, `client_id`, `token_use` when applicable, `scope` or `scp`, and the configured group claim |
| Gateway logs `required group "..." missing` or `required group membership missing` | bearer token is present and validated far enough to reach authorization, but the group claim or membership does not match | verify the user is in at least one dedicated MCP group; inspect `groups`, `memberOf`, configured group claim, and nested `ext` in the access token |
| Tools import but `get_sync_status` fails | MCP JSON/tool-call compatibility issue | server logs for `_meta`, unknown fields, result size, or tool allowlist |
| A tool is missing | preset or allowlist is narrower than expected | `tools/list`, `GONGMCP_TOOL_PRESET`, `GONGMCP_TOOL_ALLOWLIST` |
| Connector worked before a deploy and now needs OAuth setup again | full lab deploy reset the disposable Keycloak volume and dynamic clients | reconnect once, then use `LAB_DEPLOY_MODE=app-only` for normal MCP changes |
| App-only deploy refused to run | no existing running lab stack, or requested auth/issuer settings changed | use `LAB_DEPLOY_MODE=full` for auth/issuer changes |
| Connector error is generic | client hid the underlying step | inspect IdP, gateway/shim, and `gongmcp` logs by timestamp |

## Provider-Agnostic Checks

For any IdP or broker, prove these final properties:

- the MCP resource has discoverable OAuth metadata
- the client has a usable dynamic or static registration path
- requested scopes and refresh/offline-token behavior are allowed
- access tokens include the MCP resource audience
- access tokens include the user/group/role/email claims the gateway validates
- direct-OIDC tokens have provider-compatible claim extraction for `scope` or
  `scp`, client binding through `client_id` or `aud`, and any nested claim
  container such as JumpCloud/Ory `ext`
- JWT validation uses the provider's OIDC discovery `jwks_uri`; do not assume a
  Keycloak-style certificate endpoint for JumpCloud, Cognito, Okta, Entra, or
  another IdP
- the gateway rejects unauthenticated requests with the right `401` challenge
- the MCP server tolerates reserved protocol extension fields such as `_meta`

The admin screens differ across Keycloak, Auth0, Okta, Entra, WorkOS,
Cloudflare Access, Cognito, and JumpCloud-backed brokers, but these acceptance
properties do not.
