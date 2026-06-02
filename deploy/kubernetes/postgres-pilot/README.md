# Postgres Kubernetes Pilot Starter

This Kustomize starter is for customer-hosted pilots where the customer owns
Postgres, Kubernetes, secrets, ingress, auth, backups, and logs. It does not
create Postgres databases or roles.

The included resource requests and limits are starter defaults. Customers
should tune pod sizing for their datasets and add cluster-appropriate
NetworkPolicies before production access.

## Files

- `kustomization.yaml`: starter resource list.
- `namespace.yaml`: isolated namespace for the pilot.
- `configmap.yaml`: non-secret runtime defaults.
- `secret.example.yaml`: placeholder secret keys for reference only; it is not
  included in `kustomization.yaml`.
- `gongmcp-deployment.yaml`: read-only HTTP `gongmcp` runtime.
- `gongmcp-service.yaml`: cluster-internal private `gongmcp` service.
- `gongmcp-gateway-deployment.yaml`: OIDC-validating public MCP gateway.
- `gongmcp-gateway-service.yaml`: service targeted by the customer's ingress.
- `gongmcp-gateway-ingress.example.yaml`: AWS ALB-style ingress example; it is
  not included in `kustomization.yaml` because ingress annotations are
  customer-specific.
- `postgres-refresh-cronjob.yaml`: optional scheduled operator refresh.
- `postgres-deploy-smoke-job.yaml`: manual operator smoke that does not require
  business-user MCP host access; it is not included in the base Kustomize
  resources because Kubernetes Jobs are one-shot.

## Render

```bash
kubectl kustomize deploy/kubernetes/postgres-pilot
```

Before applying, replace the `vX.Y.Z` image tags with a pinned release tag or
digest. Create `gongctl-postgres-pilot-secrets` through the customer's secret
manager, an external-secrets controller, SealedSecrets, or a one-time
operator-owned `kubectl create secret` command. Do not add `secret.example.yaml`
to `kustomization.yaml`, and do not commit the rendered Secret with real
values.

One-time placeholder shape for a private test namespace:

```bash
kubectl -n gongctl-postgres-pilot create secret generic gongctl-postgres-pilot-secrets \
  --from-literal=GONG_ACCESS_KEY=replace \
  --from-literal=GONG_ACCESS_KEY_SECRET=replace \
  --from-literal=GONGCTL_SOURCE_DATABASE_URL='postgres://source-writer:replace@postgres.example.com:5432/gongctl_source?sslmode=require' \
  --from-literal=GONGCTL_MCP_DATABASE_URL='postgres://serving-writer:replace@postgres.example.com:5432/gongctl_mcp?sslmode=require' \
  --from-literal=GONGMCP_ANALYST_READER_URL='postgres://gongmcp_business_workbench_reader:replace@postgres.example.com:5432/gongctl_mcp?sslmode=require' \
  --from-literal=GONGMCP_BEARER_TOKEN=replace \
  --from-literal=OIDC_CLIENT_ID=replace \
  --from-file=ai-governance.yaml=/secure/path/ai-governance.yaml
```

When no customer exclusions exist, use `deploy postgres-refresh
--no-governance-exclusions` and `GONGMCP_NO_GOVERNANCE_EXCLUSIONS=1` instead of
mounting an `ai-governance.yaml`.

For hosted Claude/ChatGPT deployments, first choose the path in
[`docs/remote-mcp-deployment-requirements.md`](../../../docs/remote-mcp-deployment-requirements.md).
The starter defaults to direct OIDC-style issuer/client settings. If the
company explicitly chooses Cognito, also review the
[`AWS Cognito MCP gateway starter`](../../remote-mcp-auth/aws-cognito-gateway/README.md)
for Cognito app client, optional DCR fallback, and remote MCP smoke details.

For JumpCloud, do not use a broad admin group as the production gate. Create
one or more dedicated MCP groups and confirm at least one appears in the access
token claim named by `OIDC_GROUP_CLAIM`. Use `OIDC_REQUIRED_GROUP` for a single
group or `OIDC_REQUIRED_GROUPS` / `OIDC_ALLOWED_GROUPS` for any-match rollout
policy across multiple groups. JumpCloud/Ory-shaped tokens may emit scopes as
`scp` or place group/email claims under nested `ext`; use
`OIDC_AUTH_PROFILE=direct-oidc` for that compatibility path.

If DCR is enabled, the gateway pod also needs AWS identity for
`cognito-idp:CreateUserPoolClient` and `cognito-idp:DescribeUserPoolClient` on
the target user pool. Prefer IRSA or EKS Pod Identity instead of static AWS
keys in Kubernetes Secrets. Copyable policy JSON and a ServiceAccount example
live under
[`deploy/remote-mcp-auth/aws-cognito-gateway/iam/`](../../remote-mcp-auth/aws-cognito-gateway/iam/).

Support shims and validators:

| Artifact | Purpose |
| --- | --- |
| `gongctl doctor mcp-gateway` | First public gateway validator before asking Claude users to retry; use `--profile cognito` only for Cognito fallback |
| [`deploy/remote-mcp-auth/aws-cognito-gateway/lambda/`](../../remote-mcp-auth/aws-cognito-gateway/lambda/) | Cognito-only fallback: Pre Token Generation Lambda template for access-token group claims |
| [`scripts/smoke-cognito-dcr.sh`](../../../scripts/smoke-cognito-dcr.sh) | Cognito-only fallback: DCR metadata smoke; optional `--create-client` registration test |
| [`scripts/cognito-dcr-cleanup.sh`](../../../scripts/cognito-dcr-cleanup.sh) | Cognito-only fallback: list/delete DCR-created Cognito app clients by prefix |

If the company explicitly chooses the Cognito fallback, see the Cognito gateway
runbook for the recommended operator order:
[`deploy/remote-mcp-auth/aws-cognito-gateway/README.md`](../../remote-mcp-auth/aws-cognito-gateway/README.md).

## Operator Flow

Run the refresh job manually before user access or after approved sync and
governance changes:

```bash
kubectl -n gongctl-postgres-pilot create job \
  --from=cronjob/gongctl-postgres-refresh \
  gongctl-postgres-refresh-manual
```

Then run the smoke job. Delete any previous Job with the same name first so the
run is fresh:

```bash
kubectl -n gongctl-postgres-pilot delete job gongctl-postgres-deploy-smoke --ignore-not-found
kubectl -n gongctl-postgres-pilot apply -f deploy/kubernetes/postgres-pilot/postgres-deploy-smoke-job.yaml
kubectl -n gongctl-postgres-pilot logs job/gongctl-postgres-deploy-smoke
```

The smoke uses operator-held database secrets to run `gongctl doctor
postgres-deploy`, `gongctl sync status --preset`, and a local stdio
`gongmcp` `tools/list` probe against the scoped reader. It does not require a
business user's MCP client, OAuth session, ChatGPT connector, or Claude MCP
configuration.

After the private Postgres smoke passes and the public ingress points at
`gongmcp-gateway`, run the public auth validator from an operator machine:

```bash
gongctl doctor mcp-gateway \
  --url https://mcp.customer.example.com \
  --issuer https://issuer.customer.example.com \
  --profile direct-oidc \
  --origin https://claude.ai \
  --required-scope gongmcp/read \
  --group-claim memberOf \
  --client-id <dedicated Claude app client ID> \
  --required-group <one expected dedicated MCP group> \
  --token-env GONGMCP_TEST_ACCESS_TOKEN
```

Add `--expect-dcr` only if the gateway DCR fallback is enabled. Use
`--profile cognito` for Cognito fallback gateways. Use `--token-env ENV_NAME`
with an operator-held test access token when you want untrusted token-shape
diagnostics and authenticated `tools/list`; the token value, email values,
group names, and tool bodies are never printed.
