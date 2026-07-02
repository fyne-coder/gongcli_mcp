# AWS Cognito MCP Gateway Runbook

This is the step-by-step path for a Kubernetes DevOps owner deploying a
Claude-first remote MCP endpoint with AWS Cognito and JumpCloud.

The production shape is:

```text
Claude.ai custom connector
  -> public HTTPS ingress / ALB / WAF
  -> gongmcp-gateway service
  -> private ClusterIP gongmcp service
  -> Postgres reader role
```

`gongmcp-gateway` is the only internet-facing MCP service. It serves protected
resource metadata, returns OAuth `401` challenges, validates Cognito access
tokens, enforces scope/group/allowlist policy, and forwards approved requests to
private `gongmcp` with an internal bearer token.

## What DevOps Needs Before Starting

Collect these values:

```text
Public MCP base URL:     https://mcp.customer.example.com
Public MCP endpoint:     https://mcp.customer.example.com/mcp
AWS region:              us-east-1
Cognito user pool ID:    us-east-1_XXXXXXXXX
Cognito issuer URL:      https://cognito-idp.us-east-1.amazonaws.com/us-east-1_XXXXXXXXX
Cognito app client ID:   <from Cognito>
Cognito app secret:      <from Cognito, entered in Claude only if confidential>
Required MCP scope:      gongmcp/read
Allowed Cognito group:   gongmcp-users
Postgres reader URL:     postgres://reader:<password>@<host>:5432/<db>?sslmode=require
```

If Dynamic Client Registration is needed as a fallback, also collect:

```text
Cognito hosted UI domain:        https://<domain>.auth.<region>.amazoncognito.com
Cognito user pool ID:            us-east-1_XXXXXXXXX
Claude DCR callback allowlist:   https://claude.ai/api/mcp/auth_callback
Cognito IdP name for JumpCloud:  JumpCloud, or the exact provider name in Cognito
Gateway AWS identity:            IAM permission for cognito-idp:CreateUserPoolClient and cognito-idp:DescribeUserPoolClient on the user pool
```

The cluster must already have:

- Kubernetes namespace access for the deployment.
- Image pull access to GHCR or a mirrored internal registry.
- Network access from pods to the Postgres reader endpoint.
- An ingress controller or ALB path that can expose only `gongmcp-gateway`.
- Secret management through the customer's normal path, such as External
  Secrets, SealedSecrets, SOPS, Vault, or manually created Kubernetes Secrets.

## If Kubernetes And Postgres Already Work

If `gongmcp`, Postgres, ingress, and the public `/mcp` route are already
working, do not start by redeploying the stack. Finish only the auth path:

```text
JumpCloud group/user
  -> Cognito federated IdP
  -> Cognito app client for Claude
  -> Cognito access token with client_id, scope, and group/subject/email claim
  -> gongmcp-gateway validates token
  -> private gongmcp
```

Use this sequence:

1. Confirm Cognito has a user pool domain.
2. Create the Cognito resource server and `gongmcp/read` custom scope.
3. Create the `gongmcp-users` group or choose a subject/email fallback.
4. Add JumpCloud as a SAML or OIDC identity provider in the Cognito user pool.
5. Assign that IdP to the dedicated Claude app client.
6. Confirm the app client callback URL is
   `https://claude.ai/api/mcp/auth_callback`.
7. Confirm the app client requests/permits `gongmcp/read`.
8. Decide what claim the gateway should enforce:
   `cognito:groups`, a custom JumpCloud group claim, `sub`, or `email`.
9. Update only the gateway Kubernetes ConfigMap/Secret, Deployment/Service, and
   ingress if `gongmcp` is already running.
10. Restart only `deploy/gongmcp-gateway`.
11. Test the Cognito hosted login URL.
12. Test the Claude connector.

The most common blocker is not Kubernetes. It is that JumpCloud login succeeds,
but the Cognito access token does not contain the group/email claim the gateway
is configured to require.

## Support Shims In This Directory

Use these repo-provided templates when the gateway path is understood but AWS
operational details still block rollout:

| Path | Use when |
| --- | --- |
| [`lambda/pre-token-generation-jumpcloud-groups.py`](lambda/pre-token-generation-jumpcloud-groups.py) | JumpCloud login works, but the Cognito access token is missing the group claim configured in `COGNITO_GROUP_CLAIM` |
| [`iam/dcr-gateway-policy.json`](iam/dcr-gateway-policy.json) | DCR is enabled and the gateway pod needs least-privilege `CreateUserPoolClient` / `DescribeUserPoolClient` on one user pool |
| [`iam/dcr-cleanup-policy.json`](iam/dcr-cleanup-policy.json) | An operator role needs optional delete/list permissions for DCR cleanup automation |
| [`iam/eks-irsa-service-account.example.yaml`](iam/eks-irsa-service-account.example.yaml) | EKS IRSA or Pod Identity wiring for the gateway workload |
| [`../../../scripts/cognito-dcr-cleanup.sh`](../../../scripts/cognito-dcr-cleanup.sh) | List or delete DCR-created Cognito app clients by prefix; `--confirm-delete` is required to delete |
| [`../../../scripts/smoke-cognito-dcr.sh`](../../../scripts/smoke-cognito-dcr.sh) | Verify DCR metadata and, with `--create-client`, exercise `POST /register` before Claude |

Recommended order for a Kubernetes DevOps owner:

1. Finish JumpCloud federation and the pre-registered Claude app client path first.
2. If access-token group claims are missing, deploy the Pre Token Generation
   Lambda template and set `COGNITO_GROUP_CLAIM` to the target access-token
   claim.
3. Enable DCR only if Claude rejects the pre-registered client. Attach
   `iam/dcr-gateway-policy.json` through IRSA or Pod Identity.
4. Run `scripts/smoke-cognito-dcr.sh --url https://mcp.customer.example.com`
   for metadata-only verification.
5. Run `scripts/smoke-cognito-dcr.sh --create-client --delete-created-client`
   in non-prod before a live Claude DCR smoke.
6. Periodically run `scripts/cognito-dcr-cleanup.sh` with `--confirm-delete`
   to remove stale DCR clients.

These shims do not replace a live Claude connector smoke. They reduce AWS and
claim-mapping friction before that final check.

## Step 0: Confirm Cognito Prerequisites

Before creating the Claude app client, confirm the user pool has:

```text
[ ] Cognito user pool domain, for example <domain>.auth.<region>.amazoncognito.com
[ ] Resource server identifier: gongmcp
[ ] Resource server scope: read
[ ] Full custom scope visible to app clients: gongmcp/read
[ ] Cognito group if using group policy: gongmcp-users
```

Cognito custom OAuth scopes come from a user-pool resource server. If the
`gongmcp/read` scope is not created first, the app client cannot request it and
the hosted login flow can fail with `invalid_scope`.

Create the group only if group-based policy is the path:

```text
Cognito group: gongmcp-users
```

Federated JumpCloud users are not automatically added to Cognito user-pool
groups. Plan one of these paths before testing Claude:

| Path | Use when | Trade-off |
| --- | --- | --- |
| Manual Cognito group add | One or two non-prod test users | Fastest smoke; manual and not scalable |
| Post-confirmation/admin automation | User should become a Cognito group member after first sign-in | Keeps gateway on standard `cognito:groups`; requires automation |
| Pre Token Generation Lambda v2, trigger source `V2_0+` | JumpCloud group attribute must become an access-token claim | Most flexible; requires Cognito support for access-token customization |
| Subject allowlist | Need a quick first smoke without group mapping | Good temporary fallback; update per test user |

Use email allowlists only after verifying the access token contains `email`.
Many Cognito setups include email in the ID token but not the access token.

## Step 1: Create The Cognito App Client

Create one dedicated Cognito app client for Claude:

```text
Name: gongmcp-claude-prod
Grant type: authorization code
PKCE: S256
Callback URL: https://claude.ai/api/mcp/auth_callback
Allowed scopes: openid, email if needed, gongmcp/read
Identity provider: JumpCloud, if JumpCloud is federated into Cognito
```

Prefer a public app client with authorization code + PKCE for Claude. Use a
confidential client only if the customer's security policy requires one and
Claude is configured with the client secret in Advanced settings.

Record the app client ID. The gateway does not need the client secret; it
validates Cognito access tokens by issuer, JWKS, `client_id`, scope, and
group/allowlist policy.

## Step 1A: Decide Whether To Enable DCR

Default to the pre-registered app client above. Enable DCR only if Claude will
not accept the pre-registered Cognito client through Advanced settings.

When DCR is enabled, the gateway advertises itself as the MCP authorization
server so Claude can discover:

```text
https://mcp.customer.example.com/.well-known/oauth-authorization-server
https://mcp.customer.example.com/register
```

The gateway's `/register` endpoint creates a real Cognito app client using the
AWS Cognito API. It does not return a hard-coded shared client ID. The generated
client is public, uses authorization code + PKCE, has no client secret, is
limited to the exact redirect URI allowlist, and is limited to the configured
scopes and Cognito identity providers.

Compatibility note: in DCR mode, the MCP authorization server metadata is
served from the gateway so Claude can discover `/register`, but access tokens
are still minted by Cognito and carry the Cognito `iss` claim. The gateway
validates the Cognito issuer. Before a customer production rollout, run a live
Claude DCR smoke to confirm Claude accepts this metadata shape. If Claude
rejects it, the next step is a full OAuth proxy that owns `/authorize` and
`/token`, not a fake registration shim.

DCR mode still uses Cognito and JumpCloud for user login:

```text
Claude DCR POST /register
  -> gateway creates Cognito app client
  -> Claude redirects user to Cognito /oauth2/authorize
  -> Cognito sends user through JumpCloud
  -> Cognito /oauth2/token issues access token
  -> gateway validates Cognito JWT and proxies to private gongmcp
```

Give the gateway AWS credentials through the customer's standard Kubernetes
identity path. On EKS, prefer IRSA or EKS Pod Identity. The IAM policy should be
limited to the customer user pool and include only:

```text
cognito-idp:CreateUserPoolClient
cognito-idp:DescribeUserPoolClient
```

Copyable policy and EKS IRSA examples live under [`iam/`](iam/). Attach
`dcr-gateway-policy.json` to the gateway workload role. Use
`dcr-cleanup-policy.json` only on a separate operator role for
`scripts/cognito-dcr-cleanup.sh`.

Scope the policy resource to the user pool, not `*`:

```text
arn:aws:cognito-idp:<region>:<account-id>:userpool/<userPoolId>
```

Do not put AWS access keys in the ConfigMap or committed manifests.
For EKS IRSA/EKS Pod Identity, make sure the pod receives `AWS_REGION` or
`AWS_DEFAULT_REGION`; for non-EKS clusters, set the same region through the
customer's workload identity or secret-management path.

Because `/register` is intentionally reachable by the hosted MCP client, put it
behind the same WAF/rate-limit layer as `/mcp`. At minimum, rate-limit POSTs to
`/register`, alert on `CreateUserPoolClient`, and monitor Cognito app-client
quota usage.
Do not manually create Cognito app clients with the configured
`COGNITO_DCR_CLIENT_NAME_PREFIX`; the gateway uses that prefix to distinguish
gateway-created dynamic clients from other user-pool clients.

## Step 2: Federate JumpCloud Into Cognito

Use whichever federation mode the customer already standardizes on. For
JumpCloud, SAML is usually the simplest path for this setup.

### SAML Path

In JumpCloud, create or use an Amazon Cognito User Pools SAML SSO app. The
important Cognito service-provider values are:

```text
SP entity ID: urn:amazon:cognito:sp:<userPoolId>
ACS URL:      https://<cognito-domain>/saml2/idpresponse
```

Authorize the JumpCloud group that should be allowed to use the MCP connector.
Download the JumpCloud SAML metadata or copy its metadata URL.

In Cognito:

1. Open the user pool.
2. Add a SAML identity provider using the JumpCloud metadata.
3. Map stable attributes, at minimum email.
4. Add the new JumpCloud IdP to the Claude app client's managed login
   configuration.

### OIDC Path

If the customer prefers JumpCloud OIDC:

1. Create an OIDC app in JumpCloud.
2. Set the JumpCloud callback/redirect URI to:

   ```text
   https://<cognito-domain>/oauth2/idpresponse
   ```

3. In Cognito, add an OpenID Connect identity provider.
4. Use JumpCloud's issuer/discovery URL or manual HTTPS endpoints.
5. Add the new JumpCloud OIDC IdP to the Claude app client's managed login
   configuration.

## Step 3: Confirm Cognito Metadata

Run:

```bash
curl -fsS \
  https://cognito-idp.us-east-1.amazonaws.com/us-east-1_XXXXXXXXX/.well-known/openid-configuration \
  | python3 -m json.tool
```

Confirm the response includes:

```text
issuer
authorization_endpoint
token_endpoint
jwks_uri
```

Use the `issuer` value as `COGNITO_ISSUER_URL`. Do not use the Cognito hosted
UI domain as the issuer.

## Step 4: Confirm The Token Claim The Gateway Will Enforce

The gateway requires the correct `client_id` and `scope`. It also requires at
least one user access gate:

```text
COGNITO_REQUIRED_GROUP
COGNITO_ALLOWED_SUBJECTS
COGNITO_ALLOWED_EMAILS
```

Preferred production setting:

```text
COGNITO_REQUIRED_GROUP=gongmcp-users
COGNITO_GROUP_CLAIM=cognito:groups
```

If JumpCloud groups are mapped into a different Cognito access-token claim,
configure the claim name explicitly:

```text
COGNITO_REQUIRED_GROUP=gongmcp-users
COGNITO_GROUP_CLAIM=custom:jumpcloud_groups
```

This only works if the custom claim is present in the Cognito access token.
Seeing the claim in the ID token is not enough because the gateway rejects ID
tokens. For Cognito custom attributes or JumpCloud group attributes, that
usually means adding a Pre Token Generation Lambda v2 trigger to copy the group
value into the access token. In AWS terms, this is the Pre Token Generation
trigger source `V2_0+`. Start from
[`lambda/pre-token-generation-jumpcloud-groups.py`](lambda/pre-token-generation-jumpcloud-groups.py).

For the first non-prod smoke, if group mapping is not working yet, use a narrow
subject or email allowlist instead of opening the app to the whole Cognito app
client:

```text
COGNITO_REQUIRED_GROUP=
COGNITO_ALLOWED_SUBJECTS=<one test user's Cognito sub>
COGNITO_ALLOWED_EMAILS=
```

or, only if the access token includes email:

```text
COGNITO_REQUIRED_GROUP=
COGNITO_ALLOWED_SUBJECTS=
COGNITO_ALLOWED_EMAILS=test.user@example.com
```

Do not leave all three access gates empty; the gateway rejects that config.

Before choosing the gate, decode a non-prod access token locally and confirm:

```text
token_use = access
client_id = dedicated Claude app client ID
scope contains gongmcp/read
selected group, subject, or email claim is present
```

Do not paste real customer tokens into public JWT decoder sites.

## Step 5: Configure The Kubernetes Starter

The Kubernetes starter lives here:

```text
deploy/kubernetes/postgres-pilot/
```

For this Cognito remote MCP path, the relevant resources are:

```text
namespace.yaml
configmap.yaml
gongmcp-deployment.yaml
gongmcp-service.yaml
gongmcp-gateway-deployment.yaml
gongmcp-gateway-service.yaml
gongmcp-gateway-ingress.example.yaml
postgres-refresh-cronjob.yaml
postgres-deploy-smoke-job.yaml
```

Edit `deploy/kubernetes/postgres-pilot/configmap.yaml`:

```yaml
PUBLIC_BASE_URL: https://mcp.customer.example.com
GATEWAY_UPSTREAM_URL: http://gongmcp:8080
GATEWAY_ADDR: :8090
COGNITO_ISSUER_URL: https://cognito-idp.us-east-1.amazonaws.com/us-east-1_XXXXXXXXX
COGNITO_REQUIRED_SCOPE: gongmcp/read
COGNITO_SCOPES_SUPPORTED: gongmcp/read
COGNITO_REQUIRED_GROUP: gongmcp-users
COGNITO_GROUP_CLAIM: cognito:groups
GATEWAY_DCR_ENABLED: "0"
GATEWAY_ALLOWED_ORIGINS: https://claude.ai
GONGMCP_ALLOWED_ORIGINS: https://mcp.customer.example.com
```

If DCR is required, add these ConfigMap values:

```yaml
GATEWAY_DCR_ENABLED: "1"
COGNITO_DOMAIN_URL: https://your-domain.auth.us-east-1.amazoncognito.com
COGNITO_USER_POOL_ID: us-east-1_XXXXXXXXX
COGNITO_DCR_ALLOWED_REDIRECT_URIS: https://claude.ai/api/mcp/auth_callback
COGNITO_DCR_ALLOWED_SCOPES: openid,email,gongmcp/read
COGNITO_DCR_IDENTITY_PROVIDERS: JumpCloud
COGNITO_DCR_CLIENT_NAME_PREFIX: gongmcp-dcr
COGNITO_DCR_ACCESS_TOKEN_MINUTES: "60"
```

Also attach the IAM role or pod identity that allows the gateway to create and
describe Cognito app clients. Without that identity, the gateway will start but
`POST /register` will fail.

Replace every `vX.Y.Z` image tag in the Kubernetes manifests with the release
tag or internal registry image that the customer approved, for example:

```text
ghcr.io/fyne-coder/gongcli_mcp/gongctl:v0.6.4
ghcr.io/fyne-coder/gongcli_mcp/gongmcp:v0.6.4
ghcr.io/fyne-coder/gongcli_mcp/gongmcp-gateway:v0.6.4
```

If `gongmcp-gateway:v0.6.4` is not published yet, mirror or build the branch
image first. Once this PR is released, the publish workflow builds and scans
the `gongmcp-gateway` image alongside `gongctl` and `gongmcp`.

```bash
make docker-build-mcp-gateway
```

Then push it to the customer's registry and update
`gongmcp-gateway-deployment.yaml`.

## Step 6: Create Or Update The Secret

Do not apply `secret.example.yaml` directly. Create the real Secret through the
customer's secret-management path.

One-time manual shape for a non-prod namespace:

```bash
kubectl -n gongctl-postgres-pilot create secret generic gongctl-postgres-pilot-secrets \
  --from-literal=GONG_ACCESS_KEY='<gong access key>' \
  --from-literal=GONG_ACCESS_KEY_SECRET='<gong access key secret>' \
  --from-literal=GONGCTL_SOURCE_DATABASE_URL='postgres://source-writer:<password>@postgres.example.com:5432/gongctl_source?sslmode=require' \
  --from-literal=GONGCTL_MCP_DATABASE_URL='postgres://serving-writer:<password>@postgres.example.com:5432/gongctl_mcp?sslmode=require' \
  --from-literal=GONGMCP_ANALYST_READER_URL='postgres://gongmcp_business_workbench_reader:<password>@postgres.example.com:5432/gongctl_mcp?sslmode=require' \
  --from-literal=GONGMCP_BEARER_TOKEN="$(openssl rand -base64 48 | tr -d '\n')" \
  --from-literal=COGNITO_CLIENT_ID='<cognito app client id>' \
  --from-file=ai-governance.yaml=/secure/path/ai-governance.yaml
```

The same `GONGMCP_BEARER_TOKEN` is used by the private `gongmcp` service and
the gateway. It must not be sent to Claude, OpenAI, browsers, or users.

If the stack is already deployed, patch only the changed ConfigMap/Secret and
restart the gateway:

```bash
kubectl -n gongctl-postgres-pilot rollout restart deploy/gongmcp-gateway
kubectl -n gongctl-postgres-pilot rollout status deploy/gongmcp-gateway
```

For an already-working `gongmcp` deployment, the only Kubernetes resources
that need to be new or changed are:

```text
configmap.yaml                       # Cognito/gateway env values
secret                               # COGNITO_CLIENT_ID and internal bearer token
gongmcp-gateway-deployment.yaml
gongmcp-gateway-service.yaml
gongmcp-gateway-ingress.example.yaml # adapted to customer ingress
```

Do not restart or expose `gongmcp` unless its internal bearer token or private
service name changed.

## Step 7: Render And Apply

Render first:

```bash
kubectl kustomize deploy/kubernetes/postgres-pilot
```

Apply:

```bash
kubectl apply -k deploy/kubernetes/postgres-pilot
kubectl -n gongctl-postgres-pilot rollout status deploy/gongmcp
kubectl -n gongctl-postgres-pilot rollout status deploy/gongmcp-gateway
```

Confirm both services are ClusterIP:

```bash
kubectl -n gongctl-postgres-pilot get svc gongmcp gongmcp-gateway
```

`gongmcp` should not have a public load balancer.

## Step 8: Expose Only The Gateway

Use the customer's ingress standard. If they use AWS Load Balancer Controller,
start from:

```text
deploy/kubernetes/postgres-pilot/gongmcp-gateway-ingress.example.yaml
```

Before applying it, replace:

```text
mcp.customer.example.com
alb.ingress.kubernetes.io/certificate-arn
ingressClassName
```

The public route should be:

```text
https://mcp.customer.example.com/* -> service/gongmcp-gateway port 80
```

Do not expose `service/gongmcp` publicly.

## Step 9: Run The Operator Postgres Smoke

Run the existing Postgres deployment smoke before testing Claude:

```bash
kubectl -n gongctl-postgres-pilot delete job gongctl-postgres-deploy-smoke --ignore-not-found
kubectl -n gongctl-postgres-pilot apply -f deploy/kubernetes/postgres-pilot/postgres-deploy-smoke-job.yaml
kubectl -n gongctl-postgres-pilot logs -f job/gongctl-postgres-deploy-smoke
```

Expected final line:

```text
gongctl postgres deploy smoke passed
```

This proves the Postgres reader, governance config, preset, and private
`gongmcp` tool catalog before involving the hosted connector.

## Step 10: Verify The Public Gateway Before Claude

From outside the customer network:

```bash
gongctl doctor mcp-gateway \
  --url https://mcp.customer.example.com \
  --issuer https://cognito-idp.us-east-1.amazonaws.com/us-east-1_XXXXXXXXX \
  --origin https://claude.ai
```

If DCR is enabled:

```bash
gongctl doctor mcp-gateway \
  --url https://mcp.customer.example.com \
  --issuer https://cognito-idp.us-east-1.amazonaws.com/us-east-1_XXXXXXXXX \
  --expect-dcr \
  --origin https://claude.ai
```

Expected: metadata, endpoint-scoped metadata, unauthenticated `401` challenge,
OIDC discovery, JWKS reachability, and DCR metadata checks pass. The command
prints sanitized JSON and does not require AWS credentials or mutate Cognito.
For automation, inspect `.checks[]`; like `gongctl doctor postgres-deploy`,
normal validation failures are reported as JSON checks rather than a nonzero
process exit.

The raw `curl` checks below are useful when you need to inspect the exact
metadata body:

```bash
curl -fsS https://mcp.customer.example.com/.well-known/oauth-protected-resource \
  | python3 -m json.tool
```

Expected:

```text
resource = https://mcp.customer.example.com/mcp
authorization_servers[0] = https://cognito-idp.us-east-1.amazonaws.com/us-east-1_XXXXXXXXX
scopes_supported includes gongmcp/read
```

If DCR is enabled, the expected authorization server changes to the gateway
base URL because Claude must discover the gateway's `registration_endpoint`:

```text
authorization_servers[0] = https://mcp.customer.example.com
```

Then confirm authorization-server metadata:

```bash
curl -fsS https://mcp.customer.example.com/.well-known/oauth-authorization-server \
  | python3 -m json.tool
```

Expected DCR fields:

```text
registration_endpoint = https://mcp.customer.example.com/register
authorization_endpoint = https://<cognito-domain>/oauth2/authorize
token_endpoint = https://<cognito-domain>/oauth2/token
token_endpoint_auth_methods_supported includes none
code_challenge_methods_supported includes S256
```

Then confirm the unauthenticated MCP challenge:

```bash
curl -i -X POST https://mcp.customer.example.com/mcp \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}'
```

Expected:

```text
HTTP/1.1 401 Unauthorized
WWW-Authenticate: Bearer resource_metadata="https://mcp.customer.example.com/.well-known/oauth-protected-resource/mcp", scope="gongmcp/read"
```

If you have this repo locally, run the packaged smoke:

```bash
scripts/smoke-mcp-gateway.sh \
  --url https://mcp.customer.example.com \
  --issuer https://cognito-idp.us-east-1.amazonaws.com/us-east-1_XXXXXXXXX \
  --origin https://claude.ai
```

For DCR mode, omit `--issuer` and add `--expect-dcr`:

```bash
scripts/smoke-mcp-gateway.sh \
  --url https://mcp.customer.example.com \
  --expect-dcr \
  --origin https://claude.ai
```

For DCR registration against live Cognito, use the dedicated helper. Metadata
checks are the default; client creation requires an explicit flag:

```bash
scripts/smoke-cognito-dcr.sh \
  --url https://mcp.customer.example.com

scripts/smoke-cognito-dcr.sh \
  --url https://mcp.customer.example.com \
  --user-pool-id us-east-1_XXXXXXXXX \
  --region us-east-1 \
  --create-client \
  --delete-created-client
```

## Step 11: Test Cognito Hosted Login Before Claude

Open this URL in a browser, replacing values:

```text
https://<cognito-domain>/oauth2/authorize?response_type=code&client_id=<cognito-app-client-id>&redirect_uri=https%3A%2F%2Fclaude.ai%2Fapi%2Fmcp%2Fauth_callback&scope=openid+email+gongmcp%2Fread
```

Expected:

```text
Cognito shows JumpCloud as the sign-in option, or redirects to JumpCloud.
JumpCloud accepts the test user.
The final redirect goes to claude.ai with a code.
```

It is acceptable if the final Claude page does not complete outside the real
connector setup. This check only proves Cognito can initiate JumpCloud login
and return an authorization code for the configured app client and redirect
URI.

## Step 12: Configure Claude

In Claude custom connector setup:

```text
Connector URL: https://mcp.customer.example.com/mcp
OAuth Client ID: <Cognito app client ID>
OAuth Client Secret: <Cognito app client secret, if configured>
```

Claude should open the Cognito/JumpCloud login flow. After login, Claude should
list the MCP tools.

## Step 13: Run Authenticated Smoke

This step is optional if Claude has already completed login and can list tools.
It is useful when DevOps wants to test the gateway directly.

After obtaining a test Cognito access token for an allowed user:

```bash
GONGMCP_TEST_ACCESS_TOKEN="$ACCESS_TOKEN" \
  gongctl doctor mcp-gateway \
    --url https://mcp.customer.example.com \
    --issuer https://cognito-idp.us-east-1.amazonaws.com/us-east-1_XXXXXXXXX \
    --token-env GONGMCP_TEST_ACCESS_TOKEN
```

`gongctl doctor mcp-gateway` verifies that authenticated `tools/list` returns
success, but it does not print the token or the raw tool response body.

```bash
GONGMCP_GATEWAY_SMOKE_TOKEN="$ACCESS_TOKEN" \
  scripts/smoke-mcp-gateway.sh \
    --url https://mcp.customer.example.com \
    --issuer https://cognito-idp.us-east-1.amazonaws.com/us-east-1_XXXXXXXXX
```

Do not paste customer tokens into public JWT decoder sites. Decode locally if
you need to inspect claims.

If the app client allows password auth for a temporary local Cognito test user,
DevOps can get a non-prod access token with:

```bash
aws cognito-idp initiate-auth \
  --region us-east-1 \
  --auth-flow USER_PASSWORD_AUTH \
  --client-id "$COGNITO_CLIENT_ID" \
  --auth-parameters USERNAME="$TEST_USERNAME",PASSWORD="$TEST_PASSWORD" \
  --query 'AuthenticationResult.AccessToken' \
  --output text
```

This CLI shortcut usually does not exercise JumpCloud federation. For the real
JumpCloud path, use the Claude connector or the hosted login test above and
inspect gateway logs plus a locally decoded access token. If this command
returns `Auth flow not enabled for this client`, enable `ALLOW_USER_PASSWORD_AUTH`
on a separate non-prod test app client or skip this shortcut.

One local decode option that does not verify the signature, but is useful for
checking which claims Cognito placed in the access token:

```bash
python3 - "$ACCESS_TOKEN" <<'PY'
import base64
import json
import sys

token = sys.argv[1]
payload = token.split(".")[1]
payload += "=" * (-len(payload) % 4)
print(json.dumps(json.loads(base64.urlsafe_b64decode(payload)), indent=2, sort_keys=True))
PY
```

Use this only for non-prod troubleshooting. The gateway still performs real
signature and claim validation against Cognito JWKS.

## Production Acceptance Checklist

```text
[ ] public DNS resolves to the gateway ingress.
[ ] TLS certificate is valid for the public hostname.
[ ] service/gongmcp-gateway is public only through ingress/WAF.
[ ] service/gongmcp remains private ClusterIP only.
[ ] protected-resource metadata returns the exact public /mcp URL.
[ ] unauthenticated /mcp returns 401 with WWW-Authenticate.
[ ] Cognito issuer and JWKS metadata are reachable over HTTPS.
[ ] valid allowed user can list tools in Claude.
[ ] user outside the allowed group/allowlist is denied.
[ ] removed user loses access after token/session expiry.
[ ] if DCR is enabled, live Claude DCR smoke succeeds against this gateway.
[ ] if DCR is enabled, WAF/rate limits protect POST /register.
[ ] if DCR is enabled, CloudWatch/audit alerting covers CreateUserPoolClient.
[ ] if DCR is enabled, cleanup exists for old app clients with the DCR prefix.
[ ] Postgres account used by gongmcp is read-only.
[ ] no bearer tokens, client secrets, or raw transcript payloads are logged.
```

## Troubleshooting Quick Map

| Symptom | First Check |
| --- | --- |
| Claude cannot reach server | DNS, TLS certificate, ingress health, WAF rules |
| Metadata works but Claude still gets 401 | Cognito client ID/secret, callback URL, scopes, group claim |
| Browser login succeeds but tools do not list | Access token `client_id`, `scope`, `cognito:groups` or configured group claim, gateway logs |
| Browser login completes but `/mcp` is still 401 | federated user is not in the Cognito group, custom group claim is only in the ID token, or Pre Token Generation Lambda did not add the claim to the access token |
| Valid user gets denied | `COGNITO_REQUIRED_GROUP`, subject/email allowlist, JumpCloud group mapping |
| Operator smoke fails | Postgres URLs, reader grants, governance file, preset config |
| Gateway pod fails startup | `PUBLIC_BASE_URL`, `COGNITO_ISSUER_URL`, `COGNITO_CLIENT_ID`, access gate config |
| CORS/preflight fails | `GATEWAY_ALLOWED_ORIGINS`; CORS is not the auth policy |

## Docker Compose Fallback

`docker-compose.yml` and `gateway.env.example` remain in this directory for a
single-VM lab or local rehearsal. For this customer path, prefer the Kubernetes
starter above and keep Compose as a troubleshooting reference only.

## DCR Fallback

This starter now includes optional DCR support, but it remains a fallback. Leave
it disabled until Claude proves the pre-registered Cognito app client path is
not enough.

When enabled, DCR is intentionally narrow:

```text
[ ] exact redirect URI allowlist only
[ ] public PKCE clients only, no generated client secrets
[ ] configured scope allowlist only
[ ] required MCP scope must be present
[ ] configured Cognito IdP allowlist only
[ ] gateway-created clients must use the configured name prefix
[ ] gateway re-checks dynamic client IDs with Cognito before accepting tokens
```

Operational risks still exist: Cognito app-client sprawl, cleanup, AWS API
quotas, WAF/rate limiting, and audit review. Keep DCR behind the same WAF and
ingress controls as `/mcp`, monitor `CreateUserPoolClient`, and periodically
clean up unused clients with the configured `COGNITO_DCR_CLIENT_NAME_PREFIX`:

```bash
scripts/cognito-dcr-cleanup.sh \
  --user-pool-id us-east-1_XXXXXXXXX \
  --region us-east-1 \
  --prefix gongmcp-dcr

scripts/cognito-dcr-cleanup.sh \
  --user-pool-id us-east-1_XXXXXXXXX \
  --region us-east-1 \
  --prefix gongmcp-dcr \
  --confirm-delete
```

Do not raise the gateway's dynamic-client cache TTL without reviewing Cognito
client mutation and cleanup behavior.
