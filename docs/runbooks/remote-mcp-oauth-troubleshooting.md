# Remote MCP OAuth Troubleshooting

Use this runbook when a remote MCP client such as ChatGPT, Claude remote
add-by-URL, MCP Inspector, or a custom client fails against an HTTPS `/mcp`
endpoint.

Local Claude Desktop stdio MCP does not use this OAuth path.

## Failure Ladder

Check the path in order:

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

## Lab Commands

For the Proxmox auth lab:

```bash
LAB_PUBLIC_BASE_URL=https://docker.transcripts.fyne-llc.com \
  deploy/lab-auth/scripts/lab-smoke.sh
```

Inspect payload-free logs:

```bash
ssh root@192.168.1.205 \
  'cd /srv/gongctl/source/deploy/lab-auth && docker-compose logs --tail=120 keycloak shim caddy gongmcp'
```

Run the external OpenAI Responses API smoke when `OPENAI_API_KEY` is available:

```bash
LAB_PUBLIC_BASE_URL=https://docker.transcripts.fyne-llc.com \
  deploy/lab-auth/scripts/lab-openai-responses-smoke.sh
```

## Symptom Map

| Symptom | Likely cause | Next check |
| --- | --- | --- |
| Dynamic client registration rejected | IdP or broker registration policy blocked the client | trusted hosts, redirect URI, client auth method, allowed scopes |
| Login succeeds but token exchange fails | refresh/offline token policy, grant type, or client type mismatch | IdP events around code-to-token exchange |
| Authenticated `/mcp` gets 401 | token does not match gateway validation | issuer, audience/resource, expiry, signature, groups, email allowlist |
| Tools import but `get_sync_status` fails | MCP JSON/tool-call compatibility issue | server logs for `_meta`, unknown fields, result size, or tool allowlist |
| A tool is missing | preset or allowlist is narrower than expected | `tools/list`, `GONGMCP_TOOL_PRESET`, `GONGMCP_TOOL_ALLOWLIST` |
| Connector error is generic | client hid the underlying step | inspect IdP, gateway/shim, and `gongmcp` logs by timestamp |

## Provider-Agnostic Checks

For any IdP or broker, prove these final properties:

- the MCP resource has discoverable OAuth metadata
- the client has a usable dynamic or static registration path
- requested scopes and refresh/offline-token behavior are allowed
- access tokens include the MCP resource audience
- access tokens include the user/group/role/email claims the gateway validates
- the gateway rejects unauthenticated requests with the right `401` challenge
- the MCP server tolerates reserved protocol extension fields such as `_meta`

The admin screens differ across Keycloak, Auth0, Okta, Entra, WorkOS,
Cloudflare Access, Cognito, and JumpCloud-backed brokers, but these acceptance
properties do not.
