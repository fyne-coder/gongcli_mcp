# Lab Auth Deployment

This lab emulates the customer-hosted enterprise auth path without requiring a
paid identity provider or hosted Cloudflare/JumpCloud setup.

It is intentionally a rehearsal harness, not a production reference
architecture.

Use this document for disposable lab validation of the gateway/OAuth boundary.
For customer-facing deployment planning, start with
[Enterprise Deployment](enterprise-deployment.md) and
[Remote MCP auth and connector setup](remote-mcp-auth.md). Those docs cover the
current Postgres shared-deployment and `business-workbench` paths; this lab
still defaults to a synthetic SQLite cache unless the operator explicitly wires
another reviewed cache/store.

## Shape

```text
OIDC user token from Keycloak
  -> Caddy gateway on the lab VM
  -> direct Bearer-token MCP path, or oauth2-proxy browser/session path
  -> lab auth shim
  -> internal bearer token
  -> gongmcp HTTP /mcp
  -> read-only SQLite cache by default, or a Postgres reader when wired
```

The lab keeps the same important product boundary as the customer-hosted pilot:

- `gongmcp` receives no Gong credentials.
- `gongmcp` reads a mounted synthetic cache/store.
- `gongmcp` still requires its own internal bearer token.
- browser/client `Origin` is validated by `gongmcp`.
- expose the narrow preset under test, usually `business-workbench` for current
  client-facing facade validation or `business-pilot` for older aggregate-only
  smoke tests.

The shim is the stand-in for a customer gateway/broker. It validates a lab
Keycloak JWT, checks the approved group/email, and injects the internal bearer
token before forwarding to `gongmcp`. Trusted proxy identity headers are disabled
by default; enable `TRUST_PROXY_HEADERS=1` only behind a reviewed upstream gateway
that overwrites identity headers, and set `TRUST_PROXY_CIDRS` to that exact
gateway source range. The bundled Caddy `/mcp` route strips inbound proxy
identity headers before forwarding so a public client cannot forge
`X-Auth-Request-Email` or `X-Forwarded-Email`.

For ChatGPT remote MCP testing, `/mcp` must be reachable at a trusted HTTPS URL.
The lab keeps Caddy and `gongmcp` on plain HTTP inside the VM and uses
Cloudflare Tunnel for external TLS termination, matching the customer-hosted
pattern where HTTPS ends at a trusted edge/gateway.

## OAuth Discovery

The lab reuses Keycloak as the OAuth/OIDC authorization server. The shim is the
OAuth protected resource for MCP and publishes discovery metadata:

```bash
curl -fsS "$LAB_PUBLIC_BASE_URL/.well-known/oauth-protected-resource" | jq .
curl -i "$LAB_PUBLIC_BASE_URL/mcp"
```

Expected protected-resource metadata:

- `resource`: `$LAB_PUBLIC_BASE_URL/mcp`
- `authorization_servers`: `$LAB_PUBLIC_BASE_URL/realms/gong-lab`
- `scopes_supported`: `openid`, `profile`, `email`, `offline_access`
- `audiences_supported`: `gong-lab-proxy`

Expected unauthenticated MCP response:

```text
HTTP/2 401
WWW-Authenticate: Bearer realm="gongctl-lab", resource_metadata="$LAB_PUBLIC_BASE_URL/.well-known/oauth-protected-resource", authorization_uri="$LAB_PUBLIC_BASE_URL/realms/gong-lab", scope="openid profile email offline_access"
```

Keycloak's OIDC metadata remains available at:

```bash
curl -fsS "$LAB_PUBLIC_BASE_URL/realms/gong-lab/.well-known/openid-configuration" | jq .
```

## Lab Host

Use any disposable Linux host that can run Docker and `docker-compose`.

Required operator inputs:

- `LAB_VM`: SSH target for the lab host, for example `ssh-user@lab-host.example.com`.
- `LAB_PUBLIC_BASE_URL`: URL that MCP clients use, for example `https://lab.example.com`.
- `LAB_DB`: local path to the synthetic SQLite DB copied into the lab.
- `REMOTE_ROOT`: remote data root; defaults to `/srv/gongctl`.
- `LAB_APPROVED_EMAIL`: primary approved Keycloak test user.
- `LAB_SECONDARY_EMAIL`: second approved Keycloak test user for offline-token checks.
- `LAB_BLOCKED_EMAIL`: Keycloak user outside the approved group.

## Deploy

From the repo root:

```bash
export LAB_VM=ssh-user@lab-host.example.com
export LAB_PUBLIC_BASE_URL=https://lab.example.com
export LAB_DB=/path/to/synthetic-gong.db
deploy/lab-auth/scripts/lab-up.sh
```

The deploy script copies the current working tree to `$REMOTE_ROOT/source`,
copies the synthetic public SQLite DB to
`$REMOTE_ROOT/runtime/gong-mcp-governed.db`, renders the Keycloak realm import,
builds the MCP image, and starts the compose stack.

`lab-up.sh` has two deploy modes:

- `LAB_DEPLOY_MODE=full` (default): rebuilds the image, recreates the compose
  stack, and resets the disposable Keycloak volume. Use this for first deploys,
  issuer/origin changes, Keycloak realm changes, proxy/Caddy/oauth2-proxy
  changes, lab user/group changes, or DCR policy changes. A full deploy
  invalidates dynamic OAuth clients, so ChatGPT/Claude custom MCP connectors
  need to be recreated or reconnected afterward.
- `LAB_DEPLOY_MODE=app-only`: copies the current working tree, rebuilds/pulls
  only the MCP image, updates the lab `.env`, and recreates only the `gongmcp`
  container. It preserves Keycloak data, dynamic OAuth clients, Caddy,
  oauth2-proxy, and the auth shim. Use this for normal binary/payload/tool
  behavior changes when the public issuer/origin and auth settings are
  unchanged.

The full deploy also relaxes Keycloak's anonymous dynamic-client-registration
lab policies that block ChatGPT connector setup. Specifically, it removes the
default anonymous `Trusted Hosts`, `Allowed Client Scopes`, `Allowed Protocol
Mapper Types`, `Consent Required`, and `Full Scope Disabled` registration
policies after import. This is intentionally lab-only; production should use a
reviewed static client or tighter registration policy.

The approved lab users are imported into the Keycloak `/gong-mcp-users` group.
Their generated passwords are stored only in the remote lab `.env` as
`LAB_APPROVED_PASSWORD` and `LAB_SECONDARY_PASSWORD`.
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
LAB_VM=ssh-user@lab-host.example.com \
LAB_PUBLIC_BASE_URL=https://your-stable-hostname.example.com \
LAB_DB=/path/to/synthetic-gong.db \
  deploy/lab-auth/scripts/lab-up.sh
LAB_VM=ssh-user@lab-host.example.com \
LAB_PUBLIC_BASE_URL=https://your-stable-hostname.example.com \
  deploy/lab-auth/scripts/lab-smoke.sh
```

For a redacted Postgres serving DB, also pass the remote VM path to the private
governance policy. `lab-up.sh` copies it into `$REMOTE_ROOT/secrets` with
container-readable permissions, mounts that copy read-only into `gongmcp`, and
sets `GONGMCP_AI_GOVERNANCE_CONFIG=/run/secrets/ai-governance.yaml` so
restricted account-name probes are denied before query execution:

```bash
LAB_VM=ssh-user@lab-host.example.com \
LAB_PUBLIC_BASE_URL=https://your-stable-hostname.example.com \
LAB_GONG_DATABASE_URL='postgres://...' \
LAB_TOOL_PRESET=redacted-all-readonly \
LAB_GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS=1 \
LAB_GONGMCP_POSTGRES_REDACTED_SERVING_DB=1 \
LAB_GONGMCP_AI_GOVERNANCE_CONFIG_HOST=/srv/gongctl-governed/private/ai-governance.yaml \
LAB_GONGMCP_INTERNAL_PARTICIPANT_DOMAINS=internal.example \
  deploy/lab-auth/scripts/lab-up.sh
```

For a normal MCP-code update against an existing lab, preserve connected MCP
clients by using app-only mode:

```bash
LAB_VM=ssh-user@lab-host.example.com \
LAB_PUBLIC_BASE_URL=https://your-stable-hostname.example.com \
LAB_GONG_DATABASE_URL='postgres://...' \
LAB_TOOL_PRESET=business-workbench \
LAB_DEPLOY_MODE=app-only \
LAB_GONGMCP_DEPLOYMENT_ID=lab-business-workbench-$(date -u +%Y%m%dT%H%M%SZ) \
LAB_GONGMCP_INTERNAL_PARTICIPANT_DOMAINS=internal.example \
  deploy/lab-auth/scripts/lab-up.sh
```

Set `LAB_GONGMCP_INTERNAL_PARTICIPANT_DOMAINS` to the lab tenant's
comma-separated internal email domains when testing participant-domain or
participant-affiliation counts. The value is written into the remote lab
`.env` as `GONGMCP_INTERNAL_PARTICIPANT_DOMAINS`; the source-controlled example
uses `internal.example`.

Use a full deploy instead when changing `LAB_PUBLIC_BASE_URL`, approved lab
users, Keycloak policies, proxy settings, or the auth stack. App-only mode
fails closed if the existing lab stack is missing.

## Stable Cloudflare Tunnel

For a stable lab route, create a named Cloudflare Tunnel and route a hostname to
the lab host's local HTTP listener.

- hostname: `https://your-stable-hostname.example.com`
- tunnel: your named Cloudflare Tunnel
- tunnel ID: your Cloudflare Tunnel ID
- DNS: proxied CNAME to the tunnel target assigned by Cloudflare
- connector host: your lab VM or host
- connector container: the value of `TUNNEL_CONTAINER`, for example `gongctl-lab-tunnel`
- connector token file: `$REMOTE_ROOT/secrets/cloudflared-token`

Configure the tunnel ingress to forward the public hostname to the local Caddy
listener:

```text
your-stable-hostname.example.com -> http://127.0.0.1:80
fallback -> http_status:404
```

The VM runs the connector as a restartable Docker container:

```bash
ssh "$LAB_VM" \
  'docker ps --filter name="${TUNNEL_CONTAINER:-gongctl-lab-tunnel}"'
```

Redeploy and verify the lab against the stable hostname:

```bash
LAB_VM=ssh-user@lab-host.example.com \
LAB_PUBLIC_BASE_URL=https://your-stable-hostname.example.com \
LAB_DB=/path/to/synthetic-gong.db \
  deploy/lab-auth/scripts/lab-up.sh
LAB_VM=ssh-user@lab-host.example.com \
LAB_PUBLIC_BASE_URL=https://your-stable-hostname.example.com \
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
- forged proxy identity headers on the direct `/mcp` route are denied.
- the secondary approved user can request `offline_access` from Keycloak.
- a Keycloak user outside `gong-mcp-users` is denied.
- wrong `Origin` is denied.
- approved user token reaches MCP initialize.
- `tools/list` exposes `business-pilot` tools only.
- ChatGPT-style `tools/call` payloads with MCP `_meta` extension fields work.
- `gongmcp` has no Gong credential environment variables.
- the SQLite cache is mounted read-only, or Postgres mode uses a read-only
  `GONG_DATABASE_URL` / `DATABASE_URL`.

To prove an app-only deploy preserves existing dynamic OAuth clients, run:

```bash
LAB_VM=ssh-user@lab-host.example.com \
LAB_PUBLIC_BASE_URL=https://your-stable-hostname.example.com \
  deploy/lab-auth/scripts/lab-app-only-smoke.sh
```

The app-only smoke registers a temporary dynamic client, confirms it can obtain
a token, performs `LAB_DEPLOY_MODE=app-only` with a fresh deployment id, checks
`/healthz` for that deployment id, then confirms the same dynamic client can
still obtain a token.

## Manual ChatGPT Test

Create a fresh ChatGPT custom MCP connector after a full lab deploy because the
Keycloak volume and dynamic clients are disposable. App-only deploys preserve
dynamic clients; if only the MCP binary or response payload changed, refresh or
retry the existing connector first. If tool schemas or descriptions changed,
the host may still need a tool refresh/reconnect even though OAuth remains
valid.

- MCP server URL: `$LAB_PUBLIC_BASE_URL/mcp`
- authentication: OAuth
- test user: the value of `LAB_APPROVED_EMAIL` or `LAB_SECONDARY_EMAIL`

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
ssh "$LAB_VM" \
  'cd "${REMOTE_ROOT:-/srv/gongctl}/source/deploy/lab-auth" && docker-compose logs --tail=120 keycloak shim caddy gongmcp'
```

Common lab failures:

| Symptom | Likely cause | Fix |
| --- | --- | --- |
| Existing custom MCP connector lost authorization after deploy | A full lab deploy reset Keycloak and deleted the dynamic client | recreate/reconnect the connector, or use `LAB_DEPLOY_MODE=app-only` for future binary/payload-only deploys |
| `LAB_DEPLOY_MODE=app-only requires an existing running lab stack` | app-only was used before a full deploy or after the stack was stopped/reset | run one `LAB_DEPLOY_MODE=full` deploy, then use app-only for normal MCP changes |
| `LAB_DEPLOY_MODE=app-only cannot change LAB_PUBLIC_BASE_URL` | app-only would preserve a Keycloak issuer that no longer matches the requested public URL | use `LAB_DEPLOY_MODE=full` for issuer/origin changes |
| `Trusted Hosts` rejected request | Keycloak anonymous dynamic-client-registration policy blocked ChatGPT | rerun `lab-up.sh`; it removes lab-blocking anonymous DCR policies |
| `Offline tokens not allowed for the user or client` | approved user or dynamic client lacks offline-token permission | verify allowed users have `offline_access` and DCR allows the requested scope |
| `token audience/client mismatch` | dynamic-client access token lacks the MCP resource audience | verify Keycloak's `basic` client scope has the `gong-lab-proxy` audience mapper |
| `required group` missing or user denied | token lacks `/gong-mcp-users` or email is not allowed | verify group mapper and `ALLOWED_EMAILS` |
| `unknown field "_meta"` | MCP server rejected client extension metadata | deploy a build that strips/ignores MCP `_meta` before strict argument decoding |
| `redacted Postgres serving DB mode requires --ai-governance-config` | redacted-serving mode is enabled but the private governance YAML is not mounted into `gongmcp` | rerun `lab-up.sh` with `LAB_GONGMCP_AI_GOVERNANCE_CONFIG_HOST=/absolute/remote/path/to/ai-governance.yaml` |

The same categories apply beyond Keycloak. Other IdPs and brokers use different
admin controls, but remote MCP clients still need successful registration or a
static client, token exchange, expected audience/resource, user/group claims,
and MCP-compatible tool-call payload handling.

## Responses API E2E

If `OPENAI_API_KEY` is available, run the external OpenAI Responses API smoke.
This obtains a Keycloak access token for `LAB_APPROVED_EMAIL`, passes it to the
Responses API as the MCP `authorization` value, imports the remote MCP tools,
and asks the model to call `get_sync_status`.

```bash
LAB_VM=ssh-user@lab-host.example.com \
LAB_PUBLIC_BASE_URL=https://your-stable-hostname.example.com \
  deploy/lab-auth/scripts/lab-openai-responses-smoke.sh
```

Optional model override:

```bash
OPENAI_MODEL=gpt-5.5 \
LAB_VM=ssh-user@lab-host.example.com \
LAB_PUBLIC_BASE_URL=https://your-stable-hostname.example.com \
  deploy/lab-auth/scripts/lab-openai-responses-smoke.sh
```

The script does not print the Keycloak token or OpenAI API key.

## Teardown

```bash
deploy/lab-auth/scripts/lab-down.sh
```

This stops the compose stack but does not delete `$REMOTE_ROOT`, the copied DB,
or generated lab secrets.

## What This Does Not Prove

This lab does not make `gongmcp` itself an OAuth server. OAuth stays at the
gateway/shim and Keycloak layer, then the shim calls internally bearer-protected
`gongmcp`. Dynamic client registration and per-user MCP RBAC remain future
production slices.
