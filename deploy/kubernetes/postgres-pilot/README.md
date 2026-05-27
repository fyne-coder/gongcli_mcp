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
- `gongmcp-gateway-deployment.yaml`: Cognito-validating public MCP gateway.
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
  --from-literal=COGNITO_CLIENT_ID=replace \
  --from-file=ai-governance.yaml=/secure/path/ai-governance.yaml
```

For Claude-first Cognito deployments, also review
[`deploy/remote-mcp-auth/aws-cognito-gateway/README.md`](../../remote-mcp-auth/aws-cognito-gateway/README.md).
That runbook explains the public gateway, Cognito app client, Claude connector,
optional DCR fallback, and remote MCP smoke sequence.

If DCR is enabled, the gateway pod also needs AWS identity for
`cognito-idp:CreateUserPoolClient` and `cognito-idp:DescribeUserPoolClient` on
the target user pool. Prefer IRSA or EKS Pod Identity instead of static AWS
keys in Kubernetes Secrets. Copyable policy JSON and a ServiceAccount example
live under
[`deploy/remote-mcp-auth/aws-cognito-gateway/iam/`](../../remote-mcp-auth/aws-cognito-gateway/iam/).

Support shims for JumpCloud claim mapping and DCR operations:

| Artifact | Purpose |
| --- | --- |
| [`deploy/remote-mcp-auth/aws-cognito-gateway/lambda/`](../../remote-mcp-auth/aws-cognito-gateway/lambda/) | Pre Token Generation Lambda template for access-token group claims |
| [`scripts/smoke-cognito-dcr.sh`](../../../scripts/smoke-cognito-dcr.sh) | DCR metadata smoke; optional `--create-client` registration test |
| [`scripts/cognito-dcr-cleanup.sh`](../../../scripts/cognito-dcr-cleanup.sh) | List/delete DCR-created Cognito app clients by prefix |

See the Cognito gateway runbook for the recommended operator order:
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
