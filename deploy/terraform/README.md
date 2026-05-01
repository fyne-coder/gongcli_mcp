# Terraform Examples

These are non-production starter examples for customer-hosted `gongmcp` HTTP
pilots. They are not reusable production modules or reference architectures.
Copy the closest example into the customer's infrastructure repo, wire it to
existing networking, storage, DNS, TLS, secret management, logging,
identity-aware gateway/SSO, WAF or equivalent controls, rate limits, token
rotation, and change-control standards, then pin image digests before
promotion.

The examples assume:

- the writable `gongctl` sync job is handled separately
- the MCP runtime uses the MCP-only image
- the SQLite cache is mounted read-only at `/data/gong.db`
- `gongmcp` receives no Gong API credentials
- HTTP mode uses bearer auth and an explicit tool allowlist
- public or cross-user access terminates TLS at customer-managed infrastructure

## Examples

| Path | Shape | Use when |
| --- | --- | --- |
| `aws-ecs` | ECS Fargate service behind an HTTPS ALB with EFS-mounted cache | AWS customer wants container scheduling and managed TLS/load balancing |
| `azure-container-apps` | Azure Container App with Azure Files-mounted cache | Azure customer already uses Container Apps and Azure Files |
| `gcp-compute-engine` | Compute Engine VM running the MCP-only container with a mounted disk | GCP customer wants the simplest POSIX filesystem path for SQLite |

## Required Customer Decisions

Before applying any example, decide:

- who owns the writable sync job
- how the SQLite cache is refreshed and promoted to the read-only MCP runtime
- whether the MCP DB is a physically filtered governance copy
- which tools are allowlisted
- which browser/client origins are allowed to call the MCP endpoint
- where the bearer token or OAuth broker secret lives
- how secrets stay out of Terraform state, shell history, image layers, logs,
  and Git
- which HTTPS endpoint users paste into ChatGPT, Claude, or another remote MCP
  client
- where MCP access logs are stored and how raw payload logging is disabled

## Validation

Run formatting before use:

```bash
terraform fmt -recursive deploy/terraform
```

Then run provider-specific validation in the copied infrastructure repo after
backend, provider, networking, and storage variables are filled in.
