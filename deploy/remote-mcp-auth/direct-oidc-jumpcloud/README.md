# Direct OIDC Remote MCP Gateway

Use this as a provider-neutral starter for a hosted remote MCP deployment where
an organization wants its own OIDC identity provider in front of private
`gongmcp`.

```text
Hosted MCP connector
  -> public HTTPS https://mcp.organization.example.com/mcp
  -> OIDC-validating MCP gateway
  -> private bearer-authenticated gongmcp
  -> read-only SQLite cache or Postgres serving database
```

The identity provider can be JumpCloud, Okta, Entra, Auth0, Keycloak, Cognito,
or another OIDC provider. The IdP owns human login. The gateway owns MCP
metadata, `WWW-Authenticate` challenges, access-token validation, user/group
policy, and private forwarding to `gongmcp`.

Do not put public `/mcp` behind only browser-session auth such as
`oauth2-proxy`. Hosted MCP clients need a bearer-token flow for `/mcp`, not
only a successful browser login.

## Required Values

Collect these before deployment:

```text
Public MCP base URL:       https://mcp.organization.example.com
Public MCP endpoint:       https://mcp.organization.example.com/mcp
OIDC issuer URL:           https://issuer.organization.example.com
OIDC JWKS URL:             <optional; use discovery jwks_uri when default differs>
OIDC app client ID:        <dedicated hosted-connector app client>
OIDC app client secret:    <only if the app is confidential>
Connector callback URL:    <hosted connector callback URL>
Required scope:            <exact scope present in access token and metadata>
Access gate:               group policy, subject/email allowlist, or app assignment
Private gongmcp URL:       http://gongmcp:8080
Internal bearer token:     deployment-managed secret shared only by gateway/gongmcp
Cache location:            governed SQLite path or read-only Postgres serving DB URL
```

Leave `OIDC_JWKS_URL` blank unless the provider's discovery `jwks_uri` differs
from `issuer + "/.well-known/jwks.json"`.

Access-token claims are the important claims for `/mcp`. ID-token-only group or
email claims are not enough unless the gateway is explicitly designed to use
that token type.

## Scope Selection

Do not assume one custom or standard scope works for every identity provider.
Select one exact scope only after all three observations agree:

1. the OIDC application is configured to issue or request it;
2. a fresh access token contains it in `scope` (space-delimited) or, for the
   `direct-oidc` profile, `scp`; and
3. the gateway protected-resource metadata and the OAuth authorization-server
   metadata advertise it in `scopes_supported`.

Set `OIDC_REQUIRED_SCOPE` to that exact value. Set `OIDC_SCOPES_SUPPORTED` to
a comma-separated set that includes it and reflects what this gateway
advertises. Pass the same exact value to the doctor instead of relying on the
CLI default.

A custom scope such as `gongmcp/read` is acceptable only when that provider/app
actually issues it. A standard scope such as `openid` is acceptable for the
gateway's required-scope check only when it appears in the real access token and
metadata; in app-assignment-only mode, the IdP application assignment remains
the per-user access-control boundary.

## Configure The IdP

1. Create one dedicated OAuth/OIDC app client for the hosted MCP connector.
2. Enable authorization-code flow with PKCE S256 when the hosted client uses it.
3. Add the hosted connector callback URL required by the client.
4. Add the approved scope observed in real access tokens and provider metadata.
5. Choose one access-policy mode:
   - group-gated: authorize only the dedicated MCP group(s) or test users;
   - subject/email allowlist: narrow non-prod smoke when group mapping is not ready;
   - app-assignment-only: rely on the IdP application assignment list when the
     client is dedicated to this connector.
6. Confirm the access token contains the claim the gateway will enforce.
7. Keep the client secret in the organization's secret manager and hosted
   connector settings. Do not send it to `gongmcp`.

For JumpCloud static clients used by hosted connectors, configure token endpoint
client authentication as `Client Secret POST`. If the client ID, secret,
callback URI, scopes, or client authentication method changes, delete and
recreate the hosted connector before retrying.

## Configure The Gateway

Start from [`gateway.env.example`](gateway.env.example), then map values into
the Kubernetes ConfigMap and Secret. The gateway retains historical `COGNITO_*`
aliases for backward compatibility. On this direct OIDC path, use the
provider-neutral `OIDC_*` settings first.

### Shared direct-OIDC base

```yaml
PUBLIC_BASE_URL: https://mcp.organization.example.com
GATEWAY_UPSTREAM_URL: http://gongmcp:8080
GATEWAY_ADDR: :8090
OIDC_AUTH_PROFILE: direct-oidc
OIDC_ISSUER_URL: https://issuer.organization.example.com
OIDC_JWKS_URL: ""
OIDC_CLIENT_ID: replace-with-dedicated-oidc-app-client-id
OIDC_REQUIRED_SCOPE: replace-with-issued-access-token-scope
OIDC_SCOPES_SUPPORTED: replace-with-issued-access-token-scope
GATEWAY_DCR_ENABLED: "0"
GATEWAY_ALLOWED_ORIGINS: https://hosted-client.example.com
```

Leave `GATEWAY_ALLOWED_ORIGINS` empty when no browser-based hosted client sends
an `Origin` header.

The example file fails closed until the operator deliberately selects one
complete policy block below. Do not uncomment only `OIDC_ALLOW_APP_ASSIGNMENT_ONLY`
while a group, subject, or email gate remains configured.

Before deploying either policy mode, remove these legacy variables from the
deployment source rather than leaving stale parallel policy behind:

```text
COGNITO_REQUIRED_GROUP
COGNITO_REQUIRED_GROUPS
COGNITO_ALLOWED_GROUPS
COGNITO_ALLOWED_SUBJECTS
COGNITO_ALLOWED_EMAILS
```

If any provider-neutral `OIDC_*` access-policy variable is present, the gateway
loads only the OIDC policy family. Explicit empty `OIDC_*` values override stale
legacy aliases instead of silently reactivating them.

### Group-gated direct OIDC

```yaml
OIDC_REQUIRED_GROUP: replace-with-exact-access-token-group
OIDC_REQUIRED_GROUPS: ""
OIDC_ALLOWED_GROUPS: ""
OIDC_GROUP_CLAIM: replace-with-access-token-group-claim
OIDC_ALLOWED_SUBJECTS: ""
OIDC_ALLOWED_EMAILS: ""
OIDC_ALLOW_APP_ASSIGNMENT_ONLY: "0"
```

Set `OIDC_REQUIRED_GROUPS` instead of `OIDC_REQUIRED_GROUP` when the rollout
allows any one of several dedicated groups. Group matching is case-sensitive.
The group claim may be `groups`, `memberOf`, or another provider-specific claim
name when that claim appears in the access token.

When more than one gate is configured, the checks combine with AND semantics.

### Subject or email allowlist (non-prod smoke)

Leave every group setting empty and configure a narrow subject or email
allowlist:

```yaml
OIDC_REQUIRED_GROUP: ""
OIDC_REQUIRED_GROUPS: ""
OIDC_ALLOWED_GROUPS: ""
OIDC_ALLOWED_SUBJECTS: <one or more approved access-token sub values>
OIDC_ALLOWED_EMAILS: ""
OIDC_ALLOW_APP_ASSIGNMENT_ONLY: "0"
```

or, only if the access token includes email:

```yaml
OIDC_REQUIRED_GROUP: ""
OIDC_REQUIRED_GROUPS: ""
OIDC_ALLOWED_GROUPS: ""
OIDC_ALLOWED_SUBJECTS: ""
OIDC_ALLOWED_EMAILS: approved.user@example.com
OIDC_ALLOW_APP_ASSIGNMENT_ONLY: "0"
```

Use the email fallback only when the IdP controls and verifies the email value
in the access token; otherwise prefer `OIDC_ALLOWED_SUBJECTS` because `sub` is
the stable OIDC subject identifier.

### Dedicated-app assignment only

```yaml
OIDC_REQUIRED_GROUP: ""
OIDC_REQUIRED_GROUPS: ""
OIDC_ALLOWED_GROUPS: ""
OIDC_GROUP_CLAIM: ""
OIDC_ALLOWED_SUBJECTS: ""
OIDC_ALLOWED_EMAILS: ""
OIDC_ALLOW_APP_ASSIGNMENT_ONLY: "1"
```

Use this only when all of the following are true:

- the OIDC application/client is dedicated to this connector;
- the identity provider prevents unassigned users from obtaining tokens for the
  application;
- every assigned user is approved for the same read-only MCP surface;
- the gateway still validates signature/algorithm, expiration, issuer, client
  binding, and the configured required scope; and
- the operator accepts that deassignment does not revoke already-issued JWTs
  before expiration.

Keep `OIDC_ALLOW_APP_ASSIGNMENT_ONLY` disabled for shared IdP apps, broad
business apps, or deployments where group or subject policy is the auditable
access boundary. This option is direct-OIDC only; Cognito deployments must keep
an explicit group, subject, or email gate.

If any group, subject, or email gate is configured alongside
`OIDC_ALLOW_APP_ASSIGNMENT_ONLY=1`, that gate is still enforced. The flag only
removes the startup requirement for an additional access gate when no such gate
is configured.

Do not leave every group, subject, and email access gate empty unless direct OIDC
explicitly enables app-assignment-only mode. The gateway rejects that
configuration at startup because it would allow any valid token for the
configured IdP client.

## Give All Approved Users The Same Read-Only MCP Access

When an organization wants every approved business user to have read access to the
Gong MCP server, grant access at the IdP/gateway layer, not by giving users Gong
API credentials, database credentials, or direct access to private `gongmcp`.

Configure the access model this way:

1. Create a dedicated directory group with an exact access-token group value.
2. Add every approved user to that group, or map an existing approved business
   group into `OIDC_REQUIRED_GROUPS`.
3. Assign that same group to the dedicated hosted-connector OIDC app.
4. Ensure the access token sent to `/mcp` includes the group claim the gateway
   checks.
5. Give the connector one read-only scope observed in real access tokens.
6. Keep `gongmcp` on a read-only cache or scoped Postgres reader URL.
7. Set the MCP tool preset deliberately:
   - `GONGMCP_TOOL_PRESET=business-workbench` for the recommended
     business-user surface.
   - `GONGMCP_TOOL_PRESET=broad-public-redacted` only when the organization has
     approved the broader reviewed surface, the serving DB is physically
     redacted, scoped reader grants are enforced, and the governance config is
     mounted.

All users in the approved group receive the same MCP read surface. Do not add
sync/admin scopes to hosted MCP users; writable refresh remains an operator
job run by `gongctl`.

Secret values:

```text
GATEWAY_INTERNAL_BEARER_TOKEN_FILE=/run/secrets/gongmcp_token
gongmcp_token=<deployment-managed internal random token>
GONGMCP_BEARER_TOKEN=<same deployment-managed internal random token>
```

Keep raw `gongmcp` private. Only expose the gateway publicly.

## Verify Before Connecting A Hosted Client

Run the public gateway doctor from outside the cluster. Use shell variables or
concrete deployment values; do not publish secrets or raw decoded tokens in
evidence.

### Group-gated mode

```bash
gongctl doctor mcp-gateway \
  --url "${PUBLIC_BASE_URL}/mcp" \
  --issuer "${OIDC_ISSUER_URL}" \
  --profile direct-oidc \
  --required-scope "${OIDC_REQUIRED_SCOPE}" \
  --client-id "${OIDC_CLIENT_ID}" \
  --group-claim "${OIDC_GROUP_CLAIM}" \
  --required-group "${OIDC_REQUIRED_GROUP}" \
  --token-env GONGMCP_TEST_ACCESS_TOKEN
```

If the deployment uses `OIDC_REQUIRED_GROUPS`, run once for a representative
expected group by passing that exact group to `--required-group`.

Add `--origin "${EXPECTED_HOSTED_CLIENT_ORIGIN}"` only when explicitly testing
the deployment's configured CORS origin.

### App-assignment-only mode

```bash
gongctl doctor mcp-gateway \
  --url "${PUBLIC_BASE_URL}/mcp" \
  --issuer "${OIDC_ISSUER_URL}" \
  --profile direct-oidc \
  --required-scope "${OIDC_REQUIRED_SCOPE}" \
  --client-id "${OIDC_CLIENT_ID}" \
  --token-env GONGMCP_TEST_ACCESS_TOKEN
```

Do not pass `--group-claim` or `--required-group` in app-assignment-only mode.
If protected-resource metadata advertises an OAuth authorization server
different from the issuer, pass the documented `--authorization-server` value as
well.

Set `GONGMCP_TEST_ACCESS_TOKEN` only in the operator shell. The doctor treats
decoded JWT payload shape as untrusted diagnostics and never prints the token,
email values, group names, or authenticated tool bodies.

Expected:

- protected-resource metadata resolves
- endpoint-scoped metadata resolves
- authorization-server metadata resolves
- JWKS is reachable
- unauthenticated `/mcp` returns `401` with `WWW-Authenticate`
- token-shape diagnostics match the selected `direct-oidc` profile, if
  `--token-env` is provided
- no public route reaches raw `gongmcp`

The local doctor proves metadata consistency, token shape, and an authenticated
`tools/list` call. It cannot prove the IdP's assignment rule. Production smoke
must separately show that an assigned user can obtain a fresh token and an
unassigned user cannot obtain one for the dedicated client; deassignment must be
tested with a fresh token after existing-token expiration.

## Hosted Connector Smoke

Create the connector with:

```text
MCP server URL: https://mcp.organization.example.com/mcp
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
only after the hosted client completes token exchange and a first safe tool call
succeeds.

## Gateway Compatibility Decision

Do not weaken the default Cognito profile to support arbitrary provider token
shapes. Use the provider profile explicitly:

| Profile | Expected behavior |
| --- | --- |
| `cognito` | strict default: require `token_use=access`, `client_id`, configured scope, and Cognito/group policy |
| `direct-oidc` or `jumpcloud` | opt-in: accept provider-scoped claim shapes such as `scp`, `aud` as client/resource binding, and configured group/email claims under top-level claims or nested `ext` |

Before changing this code further, tests must continue to cover:

- Cognito still rejects missing `token_use=access` and missing/wrong
  `client_id`.
- direct-OIDC-shaped tokens work only when the direct-OIDC provider profile is
  enabled.
- Wrong issuer, audience/client, scope, group, expiry, signature, and algorithm
  are rejected.
- Forged client-supplied identity headers remain ignored or stripped.
