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

## Proof Order: Keycloak Before JumpCloud

Use the Keycloak lab as the open-source surrogate to prove Claude remote MCP can
complete OAuth before JumpCloud trial time. Run
`deploy/lab-auth/scripts/lab-smoke.sh`, then
`deploy/lab-auth/scripts/lab-claude-remote-preflight.sh`, then the manual Claude
steps in [Lab auth deployment](../lab-auth-deployment.md#manual-claude-remote-test).

Move to JumpCloud only after that proof path passes. On JumpCloud, rerun the
same acceptance properties with provider-specific checks for issuer, JWKS,
audience/client claims, group/email claims, scopes, callback URL, and token
exchange.

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

If steps 1-2 pass but step 7 fails, stop before JumpCloud issuer/JWKS work.
That usually means dynamic client registration, broker/shim selection, Caddy
routing, or MCP callback URL handling is wrong even though metadata discovery
succeeds. Confirm `/mcp` reaches the MCP auth shim rather than `gongmcp` or
`oauth2-proxy` directly, then rerun
`deploy/lab-auth/scripts/lab-claude-remote-preflight.sh`.

Browser login success alone is not enough.

For the production-like Keycloak lab and other static-client rehearsals, Claude
also needs the static OAuth client details in its connector Advanced settings.
Use `claude-remote-mcp` for the Keycloak lab, provide that client's secret from
the operator-held secret store, and allow
`https://claude.ai/api/mcp/auth_callback` as an IdP redirect/callback URI.
`gong-lab-proxy` is the lab proxy/shim client, not the canonical Claude remote
client, unless you are intentionally testing a direct-OIDC shortcut.

For the longer-term public gateway check, run:

```bash
gongctl doctor mcp-gateway \
  --url https://gong-mcp.example.com \
  --issuer https://issuer.example.com/realms/customer
```

The doctor defaults to `--required-scope gongmcp/read`; override it when the
lab or provider advertises a different scope set, for example
`--required-scope openid` for the current Keycloak shim path. Use
`--expect-dcr` only when the gateway intentionally exposes dynamic client
registration metadata. Use `--token-env ENV_NAME` only from an operator shell
that already holds a non-prod access token; the doctor reports status and does
not print the token or raw tool response body. Add `--origin https://claude.ai`
only when the gateway is expected to support browser-style CORS preflight for
that origin; some hosted connector paths are server-to-server and do not need
that as a baseline proof.

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
| Protected-resource metadata resolves but unauthenticated `/mcp` is not `401`/`WWW-Authenticate` | registration, broker, shim, or routing mismatch | confirm `/mcp` hits the MCP auth shim; rerun `lab-claude-remote-preflight.sh`; fix Caddy/shim routing before JumpCloud issuer/JWKS debugging |
| Keycloak browser page says `Invalid parameter: redirect_uri` | the IdP client allowlist does not include Claude's callback URL | add `https://claude.ai/api/mcp/auth_callback` to the static Claude client's redirect URI allowlist, then recreate or reconnect the Claude connector |
| Claude redirects back with `oauth_error=unauthorized_client` / `mcp_token_exchange_failed`, and IdP logs show `invalid_client_credentials` | Claude attempted token exchange without the confidential/static client secret, or with the wrong client ID | recreate/update the Claude connector Advanced settings with OAuth Client ID `claude-remote-mcp` and the matching client secret; do not paste the secret into support logs |
| Dynamic client registration rejected | IdP or broker registration policy blocked the client | trusted hosts, redirect URI, client auth method, allowed scopes |
| Metadata probes succeed, then Claude or another hosted client POSTs `/mcp` without auth and gets `401`/`403` | the client could not complete a supported registration/token flow, or `/mcp` is routed to a browser-session proxy instead of the MCP broker | authorization-server metadata for DCR/CIMD/static-client support, edge route for `/mcp`, `WWW-Authenticate` challenge, broker logs |
| Login succeeds but token exchange fails | refresh/offline token policy, grant type, or client type mismatch | IdP events around code-to-token exchange |
| Authenticated `/mcp` gets 401 | token does not match gateway validation | issuer, audience/resource, expiry, signature, groups, email allowlist, OIDC `jwks_uri` discovery |
| Connector imports tools but first tool call fails with a required-scope, audience, or resource error | token was accepted far enough to connect, but the gateway/shim requires a scope/audience/resource not present in the access token | compare protected-resource `scopes_supported`, `WWW-Authenticate` scope, decoded token `scope`/`aud`, and gateway required-scope settings; then rerun `gongctl doctor mcp-gateway` with the expected `--required-scope` |
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
- JWT validation uses the provider's OIDC discovery `jwks_uri`; do not assume a
  Keycloak-style certificate endpoint for JumpCloud, Cognito, Okta, Entra, or
  another IdP
- the gateway rejects unauthenticated requests with the right `401` challenge
- the MCP server tolerates reserved protocol extension fields such as `_meta`

The admin screens differ across Keycloak, Auth0, Okta, Entra, WorkOS,
Cloudflare Access, Cognito, and JumpCloud-backed brokers, but these acceptance
properties do not.

For Claude remote add-by-URL, capture evidence in this order: metadata curl,
unauthenticated `/mcp` headers, `lab-smoke.sh`, Claude OAuth success, and first
`get_sync_status` output. Only after that sequence passes should you spend
JumpCloud trial time on issuer/JWKS and provider-specific claim tuning.
