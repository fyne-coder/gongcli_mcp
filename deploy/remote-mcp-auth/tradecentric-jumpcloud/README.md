# Company Direct OIDC Remote MCP Deployment

Use this as the company-generic handoff for a Claude-first remote MCP pilot
where the company wants its own OIDC identity provider in front of `gongmcp`.
TradeCentric is the current worked example.

For TC, use this single branch:

```text
codex/tc-jumpcloud-mcp-gateway
```

Do not ask TC to use a second branch for the deployment test. The Keycloak lab
proof is evidence that the flow works with an open-source OIDC surrogate before
spending JumpCloud trial time; it is not the customer production branch.
Lab proof branch, for evidence only: `codex/keycloak-claude-remote-mcp-proof`.
See [Lab auth deployment](../../../docs/lab-auth-deployment.md).

## Target Shape

```text
Claude.ai custom connector
  -> public HTTPS https://mcp.company.example.com/mcp
  -> OIDC-validating MCP gateway
  -> private bearer-authenticated gongmcp
  -> read-only SQLite cache or Postgres serving database
```

The company IdP can be JumpCloud, Okta, Entra, Auth0, Keycloak, Cognito, or a
similar OIDC provider. The IdP owns human login. The gateway owns MCP metadata,
`WWW-Authenticate` challenges, token validation, user/group policy, and private
forwarding to `gongmcp`.

Do not put public `/mcp` behind only `oauth2-proxy` browser cookies. That was
the likely shape of the earlier failure mode: Claude could read metadata, then
POSTed `/mcp` without a usable bearer token and received `401` or `403`.

## Recommended Company Path

Use direct OIDC first:

```text
Claude
  -> OIDC-validating MCP gateway
  -> company IdP login and token issuance
  -> gateway validates access token
  -> private gongmcp
```

This mirrors the successful Keycloak proof:

| Keycloak proof piece | Company deployment equivalent |
| --- | --- |
| Keycloak realm | Company IdP issuer such as JumpCloud |
| Keycloak user/group | Company user/group policy |
| Pre-registered Claude client | IdP OAuth/OIDC app client for Claude |
| `https://claude.ai/api/mcp/auth_callback` | same callback URL |
| Keycloak access token with group/email | Company IdP access token with allowed user/group claim |
| Lab auth shim | OIDC-validating MCP gateway |
| Private `gongmcp` bearer token | same internal bearer-token pattern |

The important compatibility point is not Keycloak versus JumpCloud. It is that
Claude must complete an MCP-compatible OAuth flow and then call `/mcp` with a
bearer access token the gateway can validate.

TC live debugging on 2026-05-29 confirmed this direct JumpCloud path can work
without Cognito and without DCR when Claude uses a pre-registered JumpCloud
client through Advanced settings. This branch makes the gateway's
JumpCloud/Ory token compatibility an explicit tested provider profile instead
of a one-off local patch.

Use Cognito only if the company explicitly wants AWS-managed hosted UI/user
pools, or if direct OIDC/static-client testing cannot satisfy Claude's
registration or token behavior. Do not make Cognito the default just because
JumpCloud is the IdP.

If the company must fall back to Cognito with JumpCloud-federated groups, map
group claims into the access token using the
[Pre Token Generation Lambda template](../aws-cognito-gateway/lambda/pre-token-generation-jumpcloud-groups.py)
documented in the
[AWS Cognito MCP gateway starter](../aws-cognito-gateway/README.md).

## Required Values

Collect these before deployment:

```text
Public MCP base URL:       https://mcp.company.example.com
Public MCP endpoint:       https://mcp.company.example.com/mcp
OIDC issuer URL:           https://issuer.company.example.com
OIDC JWKS URL:             <optional; use discovery jwks_uri when default differs>
OIDC app client ID:        <dedicated Claude app client>
OIDC app client secret:    <only if the app is confidential>
Claude callback URL:       https://claude.ai/api/mcp/auth_callback
Required scope:            gongmcp/read or company-approved equivalent
Access gate:               one or more dedicated groups, or temporary subject/email smoke allowlist
Private gongmcp URL:       http://gongmcp:8080
Internal bearer token:     customer-managed secret shared only by gateway/gongmcp
Cache location:            governed SQLite path or read-only Postgres serving DB URL
```

For the gateway env var, leave `OIDC_JWKS_URL` blank unless discovery
`jwks_uri` differs from `issuer + "/.well-known/jwks.json"`.

Access-token claims are the important claims for `/mcp`. ID-token-only group or
email claims are not enough unless the gateway is explicitly designed to use
that token type.

## Configure The IdP

1. Create one dedicated OAuth/OIDC app client for the hosted MCP connector.
2. Enable authorization-code flow with PKCE S256.
3. Add callback URL `https://claude.ai/api/mcp/auth_callback`.
4. Add the approved pilot scope, such as `gongmcp/read`.
5. Authorize only the dedicated MCP group(s) or test users.
6. Confirm the access token contains the claim the gateway will enforce:
   - preferred: group claim containing at least one approved MCP group
   - fallback: subject allowlist for first non-prod smoke
   - email only if the access token actually includes email
7. Keep the client secret in the company's secret manager and Claude connector
   Advanced settings. Do not send it to `gongmcp`.

For JumpCloud specifically, use its OIDC app settings and verify the issuer,
`jwks_uri`, audience/client claim, access-token format, and group/email claims
before treating auth as complete.

JumpCloud setup checks that mattered in the TC debug:

- The JumpCloud OIDC app is a web/confidential client accepted for Claude's
  authorization-code callback. If the JumpCloud UI only exposes refresh-token
  as an optional checkbox, do not treat that checkbox as proof that the hosted
  client/code exchange is configured correctly.
- Redirect URI is exactly `https://claude.ai/api/mcp/auth_callback`.
- Claude Advanced settings use the same client ID and secret policy as the
  JumpCloud app. If JumpCloud is configured for confidential clients, the
  client authentication method must match what Claude sends.
- The access token includes the required read scope.
- The access token includes a dedicated MCP group in the configured group
  claim. For JumpCloud/Ory-shaped tokens, the claim may be top-level or nested
  under `ext`; use `OIDC_AUTH_PROFILE=direct-oidc` and set
  `OIDC_GROUP_CLAIM` to the access-token group claim. If identity rollout
  needs more than one group, set `OIDC_REQUIRED_GROUPS` to a comma-separated
  list. Group matching is case-sensitive, so copy the emitted group values
  exactly.

## Configure The Gateway

Use the Kubernetes starter in
[`deploy/kubernetes/postgres-pilot/README.md`](../../kubernetes/postgres-pilot/README.md).

Start from the example values in
[`gateway.env.example`](gateway.env.example), then map them into the
Kubernetes ConfigMap and Secret. The gateway currently retains some historical
`COGNITO_*` aliases for backward compatibility. On this direct OIDC path, use
the provider-neutral `OIDC_*` settings first.

If you start from `deploy/kubernetes/postgres-pilot/configmap.yaml`, replace
the starter issuer, client, scope, and group placeholders with the direct OIDC
values below. In particular, set the group claim to the IdP's access-token group
claim, such as `groups` or `memberOf`.

```yaml
PUBLIC_BASE_URL: https://mcp.company.example.com
GATEWAY_UPSTREAM_URL: http://gongmcp:8080
GATEWAY_ADDR: :8090
OIDC_AUTH_PROFILE: direct-oidc
OIDC_ISSUER_URL: https://issuer.company.example.com
# Set OIDC_JWKS_URL only when the provider's discovery jwks_uri differs
# from issuer + "/.well-known/jwks.json".
OIDC_JWKS_URL: ""
OIDC_CLIENT_ID: <dedicated Claude app client ID>
OIDC_REQUIRED_SCOPE: gongmcp/read
OIDC_SCOPES_SUPPORTED: gongmcp/read
# Use OIDC_REQUIRED_GROUP for one group or OIDC_REQUIRED_GROUPS for any-match
# across multiple dedicated groups.
OIDC_REQUIRED_GROUP: <dedicated MCP JumpCloud group>
# OIDC_REQUIRED_GROUPS: <dedicated MCP JumpCloud group>,<second rollout group>
OIDC_GROUP_CLAIM: <access-token group claim, often groups or memberOf>
OIDC_ALLOWED_SUBJECTS: ""
OIDC_ALLOWED_EMAILS: ""
GATEWAY_DCR_ENABLED: "0"
GATEWAY_ALLOWED_ORIGINS: https://claude.ai
GONGMCP_ALLOWED_ORIGINS: https://mcp.company.example.com
```

ConfigMap value:

```text
GATEWAY_INTERNAL_BEARER_TOKEN_FILE=/run/secrets/gongmcp_token
```

Secret values:

```text
gongmcp_token=<customer-managed internal random token mounted at /run/secrets/gongmcp_token>
GONGMCP_BEARER_TOKEN=<customer-managed internal random token>
GONG_DATABASE_URL=<read-only Postgres serving DB URL, if using Postgres>
```

`gongmcp_token` and `GONGMCP_BEARER_TOKEN` must contain the same internal
random token value so the gateway can authenticate to private `gongmcp`.

Keep raw `gongmcp` private. Only expose the gateway publicly.

## Deploy

Render and apply the Kubernetes starter:

```bash
kubectl kustomize deploy/kubernetes/postgres-pilot
kubectl apply -k deploy/kubernetes/postgres-pilot
kubectl -n gongctl-postgres-pilot rollout status deploy/gongmcp
kubectl -n gongctl-postgres-pilot rollout status deploy/gongmcp-gateway
```

If the company already has a working private `gongmcp` deployment, deploy only
the gateway resources and point `GATEWAY_UPSTREAM_URL` at the private service.

## Verify Before Claude

Run the public gateway doctor from outside the cluster:

```bash
gongctl doctor mcp-gateway \
  --url https://mcp.company.example.com/mcp \
  --issuer https://issuer.company.example.com \
  --profile direct-oidc \
  --origin https://claude.ai \
  --required-scope gongmcp/read \
  --group-claim memberOf \
  --client-id <dedicated Claude app client ID> \
  --required-group <one expected dedicated MCP JumpCloud group> \
  --token-env GONGMCP_TEST_ACCESS_TOKEN
```

Set `GONGMCP_TEST_ACCESS_TOKEN` only in the operator shell. The doctor treats
decoded JWT payload shape as untrusted diagnostics and never prints the token,
email values, group names, or authenticated tool bodies.

Expected:

- protected-resource metadata resolves
- endpoint-scoped metadata resolves
- authorization server metadata resolves
- JWKS is reachable
- unauthenticated `/mcp` returns `401` with `WWW-Authenticate`
- token-shape diagnostics match the selected `direct-oidc` profile, if
  `--token-env` is provided
- no public route reaches raw `gongmcp`

If a fallback broker with DCR is enabled later, rerun with the matching
doctor/preflight option for that broker.

## Test In Claude

Add a custom connector in Claude:

```text
MCP server URL: https://mcp.company.example.com/mcp
Authentication: OAuth
OAuth client ID: <dedicated OIDC app client ID>
OAuth client secret: <only if confidential client>
```

First prompt:

```text
Use the Gong MCP connector. Call get_sync_status. Then summarize total calls,
total transcripts, missing transcripts, and which tools are available next.
```

Do not mark the deployment working after browser login alone. It is working
only after Claude completes token exchange and a first safe tool call succeeds.

## TradeCentric Current Handoff

For TC/Mihai:

- branch: `codex/tc-jumpcloud-mcp-gateway`
- auth stance: direct OIDC/JumpCloud first, no Cognito by default
- Keycloak proof: already used as the open-source surrogate for Claude remote
  MCP before burning JumpCloud trial time
- live result: direct JumpCloud worked after the connector was recreated and
  gateway token-claim handling was adjusted locally
- production gate: one or more dedicated JumpCloud groups for this MCP
  connector; do not use `AWS-Admin` or an email allowlist as the final
  production policy
- success proof TC should reproduce: Claude connector calls `get_sync_status`
  through the public `/mcp` gateway with an allowed user

If TC reports a failure, ask for:

- public MCP URL
- IdP issuer URL
- whether Claude used client ID and client secret in Advanced settings
- exact redirect URI configured in the IdP
- gateway doctor output with secrets redacted
- gateway logs around metadata, token exchange, and the first `/mcp` request

## Short RCA For The Earlier Failure

The observed TC logs were consistent with an MCP OAuth/gateway gap, not a
JumpCloud password failure. `oauth2-proxy` can rehearse browser login, but it
does not by itself provide the hosted MCP connector contract. Claude needs a
client registration/static-client path, OAuth metadata, token exchange, bearer
validation, and a `/mcp` endpoint that accepts bearer auth.

The live 2026-05-29 debug narrowed the issue in stages:

| Stage | Signal | What it meant |
| --- | --- | --- |
| Original logs | metadata probes succeeded, then `/mcp` received unauthenticated `401`/`403` | Claude reached the endpoint but did not have a usable bearer token for the MCP resource. This is a broker/static-client/token-exchange problem, not a user's JumpCloud password. |
| Claude connector error | authorization failed with an IdP-side `invalid client` class of failure | Claude and JumpCloud did not agree on static client ID, secret, redirect URI, or client authentication method. |
| Gateway log | `missing bearer token` | Still no bearer token at `/mcp`; group settings are not in play yet. |
| Gateway log | `required group "AWS-Admin" missing` or `required group membership missing` | Bearer token was now present and validated far enough to reach authorization policy; the next check is the configured group claim and user membership. |
| Final outcome | Claude safe tool call succeeded | Direct JumpCloud can work without Cognito/DCR for this connector path. |

Why Keycloak did not catch the last issue: the Keycloak lab proved the general
MCP OAuth shape and static-client flow, but it did not prove JumpCloud/Ory's
access-token claim shape. JumpCloud may place client identity in `aud`, use
`scp` instead of only `scope`, omit Cognito's `token_use`, and place group or
email claims under nested `ext`.

## Gateway Compatibility Decision

Do not copy Mihai's pasted `auth.go` change directly as an unreviewed customer
shim. It captured real JumpCloud/Ory compatibility needs, but it changes
security-sensitive token acceptance rules. The supported product path is the
gateway provider profile:

| Profile | Expected behavior |
| --- | --- |
| `cognito` | strict default: require `token_use=access`, `client_id`, configured scope, and Cognito/group policy as today |
| `direct-oidc` or `jumpcloud` | opt-in: accept provider-scoped claim shapes such as `scp`, `aud` as client/resource binding, and configured group/email claims under top-level claims or nested `ext` |

Before changing this code further, tests must continue to cover:

- Cognito still rejects missing `token_use=access` and missing/wrong
  `client_id`.
- JumpCloud-shaped tokens work only when the direct-OIDC provider profile is
  enabled.
- Wrong issuer, audience/client, scope, group, expiry, signature, and algorithm
  are rejected.
- Forged client-supplied identity headers remain ignored or stripped.

See the full RCA:
[`docs/runbooks/tc-jumpcloud-remote-mcp-rca-2026-05-29.md`](../../../docs/runbooks/tc-jumpcloud-remote-mcp-rca-2026-05-29.md).
