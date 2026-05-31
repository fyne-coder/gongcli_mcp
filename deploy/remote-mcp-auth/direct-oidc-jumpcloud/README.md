# Direct OIDC Remote MCP Gateway

Use this as a company-generic starter for a hosted remote MCP deployment where
the company wants its own OIDC identity provider in front of private
`gongmcp`.

```text
Hosted MCP connector
  -> public HTTPS https://mcp.company.example.com/mcp
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
Public MCP base URL:       https://mcp.company.example.com
Public MCP endpoint:       https://mcp.company.example.com/mcp
OIDC issuer URL:           https://issuer.company.example.com
OIDC JWKS URL:             <optional; use discovery jwks_uri when default differs>
OIDC app client ID:        <dedicated hosted-connector app client>
OIDC app client secret:    <only if the app is confidential>
Connector callback URL:    <hosted connector callback URL>
Required scope:            gongmcp/read or company-approved equivalent
Access gate:               dedicated group policy, or temporary subject/email smoke allowlist
Private gongmcp URL:       http://gongmcp:8080
Internal bearer token:     customer-managed secret shared only by gateway/gongmcp
Cache location:            governed SQLite path or read-only Postgres serving DB URL
```

Leave `OIDC_JWKS_URL` blank unless the provider's discovery `jwks_uri` differs
from `issuer + "/.well-known/jwks.json"`.

Access-token claims are the important claims for `/mcp`. ID-token-only group or
email claims are not enough unless the gateway is explicitly designed to use
that token type.

## Configure The IdP

1. Create one dedicated OAuth/OIDC app client for the hosted MCP connector.
2. Enable authorization-code flow with PKCE S256 when the hosted client uses it.
3. Add the hosted connector callback URL required by the client.
4. Add the approved pilot scope, such as `gongmcp/read` or `openid`.
5. Authorize only the dedicated MCP group(s) or test users.
6. Confirm the access token contains the claim the gateway will enforce:
   - preferred: group claim containing at least one approved MCP group
   - fallback: subject allowlist for first non-prod smoke
   - email only if the access token actually includes email
7. Keep the client secret in the company's secret manager and hosted connector
   settings. Do not send it to `gongmcp`.

For JumpCloud static clients used by Claude-style connectors, configure token
endpoint client authentication as `Client Secret POST`. If the client ID,
secret, callback URI, scopes, or client authentication method changes, delete
and recreate the hosted connector before retrying.

## Configure The Gateway

Start from the example values in
[`gateway.env.example`](gateway.env.example), then map them into the
Kubernetes ConfigMap and Secret. The gateway retains some historical
`COGNITO_*` aliases for backward compatibility. On this direct OIDC path, use
the provider-neutral `OIDC_*` settings first.

```yaml
PUBLIC_BASE_URL: https://mcp.company.example.com
GATEWAY_UPSTREAM_URL: http://gongmcp:8080
GATEWAY_ADDR: :8090
OIDC_AUTH_PROFILE: direct-oidc
OIDC_ISSUER_URL: https://issuer.company.example.com
OIDC_JWKS_URL: ""
OIDC_CLIENT_ID: <dedicated hosted-connector app client ID>
OIDC_REQUIRED_SCOPE: gongmcp/read
OIDC_SCOPES_SUPPORTED: gongmcp/read
OIDC_REQUIRED_GROUP: <dedicated MCP group>
OIDC_GROUP_CLAIM: <access-token group claim, often groups or memberOf>
OIDC_ALLOWED_SUBJECTS: ""
OIDC_ALLOWED_EMAILS: ""
GATEWAY_DCR_ENABLED: "0"
GATEWAY_ALLOWED_ORIGINS: https://claude.ai
GONGMCP_ALLOWED_ORIGINS: https://mcp.company.example.com
```

Set `OIDC_REQUIRED_GROUPS` instead of `OIDC_REQUIRED_GROUP` when the rollout
allows any one of several dedicated groups. Group matching is case-sensitive.

## Give All Approved Users The Same Read-Only MCP Access

When the customer wants every approved business user to have read access to the
Gong MCP server, grant access at the IdP/gateway layer, not by giving users Gong
API credentials, database credentials, or direct access to private `gongmcp`.

Configure the access model this way:

1. Create a dedicated directory group, for example `GongMCP-Users`.
2. Add every approved user to that group, or map an existing approved
   all-users business group into `OIDC_REQUIRED_GROUPS`.
3. Assign that same group to the dedicated hosted-connector OIDC app.
4. Ensure the access token sent to `/mcp` includes the group claim the gateway
   checks, such as `groups`, `memberOf`, or nested `ext.memberOf`.
5. Give the connector one read-only scope, normally `gongmcp/read`.
6. Keep `gongmcp` on a read-only cache or scoped Postgres reader URL.
7. Set the MCP tool preset deliberately:
   - `GONGMCP_TOOL_PRESET=business-workbench` for the recommended
     business-user surface.
   - `GONGMCP_TOOL_PRESET=broad-public-redacted` only when the customer has
     approved the broader reviewed surface, the serving DB is physically
     redacted, scoped reader grants are enforced, and the governance config is
     mounted.

All users in the approved group receive the same MCP read surface. Do not add
sync/admin scopes to hosted MCP users; writable refresh remains an operator
job run by `gongctl`.

Secret values:

```text
GATEWAY_INTERNAL_BEARER_TOKEN_FILE=/run/secrets/gongmcp_token
gongmcp_token=<customer-managed internal random token>
GONGMCP_BEARER_TOKEN=<same customer-managed internal random token>
```

Keep raw `gongmcp` private. Only expose the gateway publicly.

## Verify Before Connecting A Hosted Client

Run the public gateway doctor from outside the cluster:

```bash
gongctl doctor mcp-gateway \
  --url https://mcp.company.example.com/mcp \
  --issuer https://issuer.company.example.com \
  --profile direct-oidc \
  --origin https://claude.ai \
  --required-scope gongmcp/read \
  --group-claim memberOf \
  --client-id <dedicated hosted-connector app client ID> \
  --required-group <expected dedicated MCP group> \
  --token-env GONGMCP_TEST_ACCESS_TOKEN
```

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

## Hosted Connector Smoke

Create the connector with:

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
