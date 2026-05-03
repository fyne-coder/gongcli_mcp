# Lab Auth Deployment

This lab emulates the customer-hosted enterprise auth path without requiring a
paid identity provider or hosted Cloudflare/JumpCloud setup.

It is intentionally a rehearsal harness, not a production reference
architecture.

## Shape

```text
OIDC user token from Keycloak
  -> Caddy gateway on the lab VM
  -> direct Bearer-token MCP path, or oauth2-proxy browser/session path
  -> lab auth shim
  -> internal bearer token
  -> gongmcp HTTP /mcp
  -> read-only SQLite cache
```

The lab keeps the same important product boundary as the customer-hosted pilot:

- `gongmcp` receives no Gong credentials.
- `gongmcp` reads a mounted SQLite DB.
- `gongmcp` still requires its own internal bearer token.
- browser/client `Origin` is validated by `gongmcp`.
- only `business-pilot` MCP tools are exposed.

The shim is the stand-in for a customer gateway/broker. It validates a lab
Keycloak JWT or trusted proxy identity headers, checks the approved group/email,
and injects the internal bearer token before forwarding to `gongmcp`.

For ChatGPT remote MCP testing, `/mcp` must be reachable at a trusted HTTPS URL.
The lab keeps Caddy and `gongmcp` on plain HTTP inside the VM and uses
Cloudflare Tunnel for external TLS termination, matching the customer-hosted
pattern where HTTPS ends at a trusted edge/gateway.

## OAuth Discovery

The lab reuses Keycloak as the OAuth/OIDC authorization server. The shim is the
OAuth protected resource for MCP and publishes discovery metadata:

```bash
curl -fsS https://docker.transcripts.fyne-llc.com/.well-known/oauth-protected-resource | jq .
curl -i https://docker.transcripts.fyne-llc.com/mcp
```

Expected protected-resource metadata:

- `resource`: `https://docker.transcripts.fyne-llc.com/mcp`
- `authorization_servers`: `https://docker.transcripts.fyne-llc.com/realms/gong-lab`
- `scopes_supported`: `openid`, `profile`, `email`, `offline_access`
- `audiences_supported`: `gong-lab-proxy`

Expected unauthenticated MCP response:

```text
HTTP/2 401
WWW-Authenticate: Bearer realm="gongctl-lab", resource_metadata="https://docker.transcripts.fyne-llc.com/.well-known/oauth-protected-resource", authorization_uri="https://docker.transcripts.fyne-llc.com/realms/gong-lab", scope="openid profile email offline_access"
```

Keycloak's OIDC metadata remains available at:

```bash
curl -fsS https://docker.transcripts.fyne-llc.com/realms/gong-lab/.well-known/openid-configuration | jq .
```

## Proxmox VM

Current lab VM:

- Proxmox host: `root@192.168.1.21`
- VMID/name: `1910` / `gongctl-auth-lab`
- IP: `192.168.1.205`
- OS/runtime: Debian 12, Docker, `docker-compose`
- data root: `/srv/gongctl`

## Deploy

From the repo root:

```bash
deploy/lab-auth/scripts/lab-up.sh
```

Defaults:

- `LAB_VM=root@192.168.1.205`
- `LAB_PUBLIC_BASE_URL=http://192.168.1.205`
- `LAB_DB=$HOME/gongctl-data/public-example-q1-2026/gong-example-q1-2026.db`

Override them with environment variables if needed.

The deploy script copies the current working tree to `/srv/gongctl/source`,
copies the synthetic public SQLite DB to
`/srv/gongctl/runtime/gong-mcp-governed.db`, renders the Keycloak realm import,
builds the MCP image, and starts the compose stack.

Because this is a disposable lab, each deploy recreates the Keycloak volume so
the imported realm, public issuer URL, and generated client secret stay in sync.
The deploy also relaxes Keycloak's anonymous dynamic-client-registration lab
policies that block ChatGPT connector setup. Specifically, it removes the
default anonymous `Trusted Hosts`, `Allowed Client Scopes`, `Allowed Protocol
Mapper Types`, `Consent Required`, and `Full Scope Disabled` registration
policies after import. This is intentionally lab-only; production should use a
reviewed static client or tighter registration policy.

Current allowed lab users:

- `pilot@example.com`
- `test@fyne-llc.com`

Both users are imported into the Keycloak `/gong-mcp-users` group. The
`test@fyne-llc.com` password is stored in the VM lab `.env` as `TEST_PASSWORD`
and defaults to the value provided for the lab if missing during deploy.
The allowed lab users are also granted Keycloak's `offline_access` realm role so
ChatGPT can complete its OAuth code-to-token exchange when it requests an
offline-capable refresh token. Without that role, login can succeed but Keycloak
will reject the token exchange with `Offline tokens not allowed for the user or
client`.
The deploy also attaches MCP audience and group protocol mappers to Keycloak's
`basic` client scope. Anonymous dynamic-client-registration clients created by
ChatGPT get `basic` as their default client scope, so this makes their access
tokens carry the `gong-lab-proxy` audience and `/gong-mcp-users` group claims
that the MCP auth shim validates.

## HTTPS Quick Tunnel

For ChatGPT/API HTTPS smoke testing, start an ephemeral Cloudflare quick tunnel:

```bash
deploy/lab-auth/scripts/lab-https-quick.sh
```

This starts `cloudflare/cloudflared` on the VM, discovers a temporary
`https://*.trycloudflare.com` URL, redeploys the lab with that URL as the
Keycloak issuer and allowed MCP origin, then runs the full smoke through HTTPS.

Stop the quick tunnel with:

```bash
deploy/lab-auth/scripts/lab-https-stop.sh
```

The quick tunnel URL is temporary. For a stable enterprise rehearsal URL, use a
named Cloudflare Tunnel and DNS route, then deploy with:

```bash
LAB_PUBLIC_BASE_URL=https://your-stable-hostname.example.com \
  deploy/lab-auth/scripts/lab-up.sh
LAB_PUBLIC_BASE_URL=https://your-stable-hostname.example.com \
  deploy/lab-auth/scripts/lab-smoke.sh
```

## Stable Cloudflare Tunnel

Current stable lab route:

- hostname: `https://docker.transcripts.fyne-llc.com`
- tunnel: `gongctl-auth-lab-docker-transcripts`
- tunnel ID: `b9d52fe1-e8f0-4708-aa59-e4be226be3ed`
- DNS: proxied CNAME to `b9d52fe1-e8f0-4708-aa59-e4be226be3ed.cfargotunnel.com`
- connector host: VM `1910` / `192.168.1.205`
- connector container: `gongctl-auth-lab-tunnel`
- connector token file: `/srv/gongctl/secrets/cloudflared-docker-transcripts-token`

The tunnel is remotely configured in Cloudflare with this ingress:

```text
docker.transcripts.fyne-llc.com -> http://127.0.0.1:80
fallback -> http_status:404
```

The VM runs the connector as a restartable Docker container:

```bash
ssh root@192.168.1.205 \
  'docker ps --filter name=gongctl-auth-lab-tunnel'
```

Redeploy and verify the lab against the stable hostname:

```bash
LAB_PUBLIC_BASE_URL=https://docker.transcripts.fyne-llc.com \
  deploy/lab-auth/scripts/lab-up.sh
LAB_PUBLIC_BASE_URL=https://docker.transcripts.fyne-llc.com \
  deploy/lab-auth/scripts/lab-smoke.sh
```

## Smoke

```bash
deploy/lab-auth/scripts/lab-smoke.sh
```

The smoke verifies:

- `/healthz` reaches `gongmcp`.
- OAuth protected-resource metadata is published.
- anonymous dynamic client registration succeeds for a ChatGPT-style redirect
  and `offline_access` scope.
- newly registered dynamic clients can receive access tokens with the
  `gong-lab-proxy` audience and `/gong-mcp-users` group claims.
- unauthenticated `/mcp` is denied with `401` and `WWW-Authenticate`.
- `test@fyne-llc.com` can request `offline_access` from Keycloak.
- a Keycloak user outside `gong-mcp-users` is denied.
- wrong `Origin` is denied.
- approved user token reaches MCP initialize.
- `tools/list` exposes `business-pilot` tools only.
- ChatGPT-style `tools/call` payloads with MCP `_meta` extension fields work.
- `gongmcp` has no Gong credential environment variables.
- the SQLite cache is mounted read-only.

## Manual ChatGPT Test

Create a fresh ChatGPT custom MCP connector after every lab redeploy because the
Keycloak volume and dynamic clients are disposable.

- MCP server URL: `https://docker.transcripts.fyne-llc.com/mcp`
- authentication: OAuth
- test user: `test@fyne-llc.com`

First prompt:

```text
Use the gongctl MCP connector. Call get_sync_status. Then summarize total
calls, total transcripts, missing transcripts, and which business-pilot tools
are available next.
```

Expected current lab result: `16` calls, `16` transcripts, and `0` missing
transcripts.

Analysis prompt:

```text
Use the gongctl MCP connector.

Analyze the available call data for business insight, not just metrics.

1. Call summarize_calls_by_lifecycle.
2. Call summarize_call_facts with useful business groupings if available.
3. Call rank_transcript_backlog.
4. Synthesize the results into:
   - the main sales/customer lifecycle pattern
   - where the strongest customer evidence appears
   - any gaps or risks in the dataset
   - 3 practical next actions for Sales, RevOps, or Marketing

Keep the answer concise, but include enough detail that it reads like an actual
business review rather than a status report. Mention which tools you used.
```

If ChatGPT shows a generic connector failure, inspect payload-free server logs:

```bash
ssh root@192.168.1.205 \
  'cd /srv/gongctl/source/deploy/lab-auth && docker-compose logs --tail=120 keycloak shim caddy gongmcp'
```

Common lab failures:

| Symptom | Likely cause | Fix |
| --- | --- | --- |
| `Trusted Hosts` rejected request | Keycloak anonymous dynamic-client-registration policy blocked ChatGPT | rerun `lab-up.sh`; it removes lab-blocking anonymous DCR policies |
| `Offline tokens not allowed for the user or client` | approved user or dynamic client lacks offline-token permission | verify allowed users have `offline_access` and DCR allows the requested scope |
| `token audience/client mismatch` | dynamic-client access token lacks the MCP resource audience | verify Keycloak's `basic` client scope has the `gong-lab-proxy` audience mapper |
| `required group` missing or user denied | token lacks `/gong-mcp-users` or email is not allowed | verify group mapper and `ALLOWED_EMAILS` |
| `unknown field "_meta"` | MCP server rejected client extension metadata | deploy a build that strips/ignores MCP `_meta` before strict argument decoding |

The same categories apply beyond Keycloak. Other IdPs and brokers use different
admin controls, but remote MCP clients still need successful registration or a
static client, token exchange, expected audience/resource, user/group claims,
and MCP-compatible tool-call payload handling.

## Responses API E2E

If `OPENAI_API_KEY` is available, run the external OpenAI Responses API smoke.
This obtains a Keycloak access token for `pilot@example.com`, passes it to the
Responses API as the MCP `authorization` value, imports the remote MCP tools,
and asks the model to call `get_sync_status`.

```bash
LAB_PUBLIC_BASE_URL=https://docker.transcripts.fyne-llc.com \
  deploy/lab-auth/scripts/lab-openai-responses-smoke.sh
```

Optional model override:

```bash
OPENAI_MODEL=gpt-5.5 \
LAB_PUBLIC_BASE_URL=https://docker.transcripts.fyne-llc.com \
  deploy/lab-auth/scripts/lab-openai-responses-smoke.sh
```

The script does not print the Keycloak token or OpenAI API key.

## Teardown

```bash
deploy/lab-auth/scripts/lab-down.sh
```

This stops the compose stack but does not delete `/srv/gongctl`, the copied DB,
or generated lab secrets.

## What This Does Not Prove

This lab does not make `gongmcp` itself an OAuth server. OAuth stays at the
gateway/shim and Keycloak layer, then the shim calls internally bearer-protected
`gongmcp`. Dynamic client registration and per-user MCP RBAC remain future
production slices.
