# TradeCentric JumpCloud Remote MCP Deployment

Use this branch as the TradeCentric deployment handoff branch for a
Claude-first remote MCP pilot:

```text
codex/tc-jumpcloud-mcp-gateway
```

This branch starts from the deployable `gongmcp-gateway` work and packages the
TradeCentric path as one flow. The Keycloak lab proof remains useful evidence,
but it is not the production deployment branch.

## Target Shape

```text
Claude.ai custom connector
  -> public HTTPS https://mcp.tradecentric.example.com/mcp
  -> gongmcp-gateway
  -> private bearer-authenticated gongmcp
  -> read-only Postgres serving database
```

JumpCloud should be the human identity provider. The MCP-facing gateway still
needs to provide MCP OAuth metadata, `WWW-Authenticate` challenges, token
validation, user/group policy, and private forwarding to `gongmcp`.

Do not put public `/mcp` behind only `oauth2-proxy` browser cookies. That was
the likely cause of the earlier failure mode: Claude could read metadata, then
POSTed `/mcp` without a usable bearer token and received `401` or `403`.

## Recommended TC Path

Use Cognito as the OAuth/token broker with JumpCloud federated behind it:

```text
Claude
  -> gongmcp-gateway
  -> Cognito app client
  -> JumpCloud login
  -> Cognito access token
  -> gongmcp-gateway validates token
  -> private gongmcp
```

This mirrors the Keycloak proof most closely:

| Keycloak proof piece | TC deployment equivalent |
| --- | --- |
| Keycloak realm | Cognito user pool |
| Keycloak user/group | JumpCloud user/group federated into Cognito |
| Pre-registered Claude client | Cognito app client for Claude |
| `https://claude.ai/api/mcp/auth_callback` | same callback URL |
| Keycloak access token with group/email | Cognito access token with access-gate claim |
| Lab auth shim | `gongmcp-gateway` |
| Private `gongmcp` bearer token | same internal bearer-token pattern |

The important compatibility point is not Keycloak versus JumpCloud. It is that
Claude must complete an MCP-compatible OAuth flow and then call `/mcp` with a
bearer access token the gateway can validate.

## Branches

Use these branches this way:

| Branch | Purpose |
| --- | --- |
| `codex/tc-jumpcloud-mcp-gateway` | TC deployment branch for Kubernetes/gateway work. |
| `codex/keycloak-claude-remote-mcp-proof` | Lab proof branch for the Keycloak surrogate test. |
| `codex/mcp-cognito-gateway` | Generic upstream gateway work this TC branch is based on. |

## Required Values

Collect these before deployment:

```text
Public MCP base URL:       https://mcp.tradecentric.example.com
Public MCP endpoint:       https://mcp.tradecentric.example.com/mcp
Cognito issuer URL:        https://cognito-idp.<region>.amazonaws.com/<pool>
Cognito hosted UI domain:  https://<domain>.auth.<region>.amazoncognito.com
Cognito app client ID:     <dedicated Claude app client>
Cognito app client secret: <only if TC chooses confidential client>
Claude callback URL:       https://claude.ai/api/mcp/auth_callback
Required scope:            gongmcp/read
Access gate:               Cognito group, Cognito subject, or email claim
Private gongmcp URL:       http://gongmcp:8080
Internal bearer token:     customer-managed secret shared only by gateway/gongmcp
Postgres reader URL:       read-only governed serving DB URL
```

## Configure Cognito And JumpCloud

1. Create or select a Cognito user pool.
2. Create resource server `gongmcp` with scope `read`.
3. Create one dedicated Claude app client:
   - authorization code flow
   - PKCE S256
   - callback URL `https://claude.ai/api/mcp/auth_callback`
   - scopes `openid`, `email` if needed, and `gongmcp/read`
4. Federate JumpCloud into Cognito using SAML or OIDC.
5. Authorize only the pilot JumpCloud group or users.
6. Confirm the selected access gate is present in the Cognito access token:
   - preferred: `cognito:groups` contains `gongmcp-users`
   - fallback: allowlist the Cognito `sub` for a first non-prod smoke
   - email only if the access token actually includes `email`

ID-token claims are not enough. The gateway validates access tokens for `/mcp`.

If JumpCloud group data lands in a Cognito user attribute but not the access
token, use the [Pre Token Generation Lambda template][pre-token-template].

Copy the value into an access-token claim, then set `COGNITO_GROUP_CLAIM` to
that claim name.

[pre-token-template]: ../aws-cognito-gateway/lambda/pre-token-generation-jumpcloud-groups.py

## Configure The Gateway

Use the Kubernetes starter in:

```text
deploy/kubernetes/postgres-pilot/
```

Start from the example values in
[`gateway.env.example`](gateway.env.example), then map them into the
Kubernetes ConfigMap and Secret:

```yaml
PUBLIC_BASE_URL: https://mcp.tradecentric.example.com
GATEWAY_UPSTREAM_URL: http://gongmcp:8080
GATEWAY_ADDR: :8090
COGNITO_ISSUER_URL: https://cognito-idp.<region>.amazonaws.com/<pool>
COGNITO_REQUIRED_SCOPE: gongmcp/read
COGNITO_SCOPES_SUPPORTED: gongmcp/read
COGNITO_REQUIRED_GROUP: gongmcp-users
COGNITO_GROUP_CLAIM: cognito:groups
GATEWAY_DCR_ENABLED: "0"
GATEWAY_ALLOWED_ORIGINS: https://claude.ai
GONGMCP_ALLOWED_ORIGINS: https://mcp.tradecentric.example.com
```

Secret values:

```text
COGNITO_CLIENT_ID=<dedicated Claude app client ID>
GONGMCP_BEARER_TOKEN=<customer-managed internal random token>
GONGMCP_ANALYST_READER_URL=<read-only Postgres serving DB URL>
```

The gateway does not need the Cognito app client secret. If the app client is
confidential, the secret is entered into Claude's connector Advanced settings,
not sent to `gongmcp`.

## Deploy

Render and apply the Kubernetes starter:

```bash
kubectl kustomize deploy/kubernetes/postgres-pilot
kubectl apply -k deploy/kubernetes/postgres-pilot
kubectl -n gongctl-postgres-pilot rollout status deploy/gongmcp
kubectl -n gongctl-postgres-pilot rollout status deploy/gongmcp-gateway
```

Only expose `gongmcp-gateway` publicly. Keep `gongmcp` private.

## Verify Before Claude

Run the public gateway doctor from outside the cluster:

```bash
gongctl doctor mcp-gateway \
  --url https://mcp.tradecentric.example.com \
  --issuer https://cognito-idp.<region>.amazonaws.com/<pool> \
  --origin https://claude.ai
```

Expected:

- protected-resource metadata resolves
- endpoint-scoped metadata resolves
- authorization server metadata resolves
- JWKS is reachable
- unauthenticated `/mcp` returns `401` with `WWW-Authenticate`
- no public route reaches raw `gongmcp`

If TC enables the optional DCR fallback later, rerun with `--expect-dcr`.

## Test In Claude

Add a custom connector in Claude:

```text
MCP server URL: https://mcp.tradecentric.example.com/mcp
Authentication: OAuth
OAuth client ID: <Cognito app client ID>
OAuth client secret: <only if confidential client>
```

First prompt:

```text
Use the Gong MCP connector. Call get_sync_status. Then summarize total calls,
total transcripts, missing transcripts, and which tools are available next.
```

## Short RCA For The Earlier Failure

The observed TC logs were consistent with an MCP OAuth broker gap, not a
JumpCloud password failure. `oauth2-proxy` can rehearse browser login, but it
does not by itself provide the hosted MCP connector contract. Claude needs a
client registration/static-client path, OAuth metadata, token exchange, bearer
validation, and a `/mcp` endpoint that accepts bearer auth.

This branch gives TC the gateway deployment path that fills that missing layer.
