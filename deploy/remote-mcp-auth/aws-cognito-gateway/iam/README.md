# IAM And Kubernetes Identity Examples For Cognito DCR

These templates grant the minimum AWS permissions for optional gateway Dynamic
Client Registration. DCR remains disabled by default with
`GATEWAY_DCR_ENABLED=0`.

## Files

| File | Purpose |
| --- | --- |
| `dcr-gateway-policy.json` | Runtime policy for `CreateUserPoolClient` and `DescribeUserPoolClient` on one user pool |
| `dcr-cleanup-policy.json` | Optional operator policy for listing/deleting DCR-created app clients |
| `eks-irsa-service-account.example.yaml` | EKS IRSA ServiceAccount and Deployment patch example |

## Scope The User Pool ARN

Replace placeholders in the JSON policies before attaching them to a role:

```text
<AWS_REGION>      us-east-1
<AWS_ACCOUNT_ID>  123456789012
<USER_POOL_ID>    us-east-1_XXXXXXXXX
```

Example resource ARN:

```text
arn:aws:cognito-idp:us-east-1:123456789012:userpool/us-east-1_XXXXXXXXX
```

Do not widen these policies to `Resource: "*"` for production gateway pods.

## Runtime Vs Cleanup Permissions

Attach `dcr-gateway-policy.json` to the gateway workload role used by
`gongmcp-gateway` when DCR is enabled.

Attach `dcr-cleanup-policy.json` only to an operator role or break-glass
automation that runs `scripts/cognito-dcr-cleanup.sh`. The gateway pod does not
need delete permissions for normal operation.

## EKS IRSA Or Pod Identity

On EKS, prefer IRSA or EKS Pod Identity over static AWS access keys in
Kubernetes Secrets.

1. Create the IAM role and attach `dcr-gateway-policy.json`.
2. Apply `eks-irsa-service-account.example.yaml` after replacing placeholders.
3. Set `spec.template.spec.serviceAccountName: gongmcp-gateway` on the gateway
   Deployment.
4. Ensure the pod receives `AWS_REGION` or `AWS_DEFAULT_REGION`.

For non-EKS clusters, use the customer's workload-identity equivalent and the
same scoped policy JSON.
