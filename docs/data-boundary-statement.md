# Data Boundary Statement

## Purpose

This statement describes the intended data boundary for customer-hosted
`gongctl` deployments.

The current product shape is customer-hosted and local-first:

- `gongctl` is the writable operator tool that authenticates to Gong and
  refreshes customer-controlled cache state.
- `gongmcp` is a read-only MCP server over an existing customer-controlled
  SQLite cache.
- The vendor does not need a vendor-operated SaaS control plane, shared tenant
  database, or always-on remote access path to operate the package.

This document complements [Data handling](data-handling.md),
[Security model](security-model.md), and
[Enterprise deployment](enterprise-deployment.md).

## Boundary Summary

For the customer-hosted package, customer data is expected to remain inside the
customer's approved environment except when the customer deliberately exports or
shares a reviewed artifact.

Default boundary expectations:

- Gong credentials stay in customer-controlled secret storage and runtime
  environments.
- Raw Gong API responses, transcript text, transcript files, CRM context, and
  local SQLite cache files stay in customer-controlled storage.
- `gongmcp` reads the customer-controlled SQLite cache read-only and should not
  receive Gong credentials.
- The source repository, tracked docs, examples, and fixtures must remain
  tenant-free and secret-free.

## Customer Data Classes

| Data class | Examples | Default location | Boundary expectation |
| --- | --- | --- | --- |
| Credentials and access secrets | Gong access keys, bearer tokens, future OAuth secrets | Customer secret manager, customer environment, mounted secret file | Never commit, never include in support artifacts, never provide to `gongmcp` unless the secret is specifically for MCP access |
| Restricted tenant content | Transcript text, transcript JSON, raw call payloads, embedded CRM values, failing request/response payloads | Customer-controlled data root, protected local or mounted storage | Do not send to vendor support by default |
| Sensitive tenant metadata | Call titles, call IDs, workspace IDs, tracker names, scorecard question text, profile mappings | Customer-controlled SQLite cache, operator reports | Treat as customer data even when not full transcript text |
| Reduced operational metadata | Row counts, cache freshness timestamps, version info, enabled commands, table inventory, redacted error codes | Customer-run support bundle or audit logs | Preferred default for troubleshooting and support |
| Public repo content | Code, sanitized docs, synthetic fixtures | Source repo | Must not contain real customer content or secrets |

## No-Sensitive-Telemetry Statement

The customer-hosted package should not rely on vendor-operated sensitive
telemetry to function.

Default expectations:

- no automatic vendor collection of raw transcripts
- no automatic vendor collection of raw Gong payloads
- no automatic vendor collection of failing customer request/response bodies
- no automatic vendor collection of customer SQLite contents
- no automatic vendor collection of prompt text, tool outputs, or model traces
  that contain customer transcript or CRM content
- no requirement to grant the vendor standing access to the customer's cloud,
  host, database, or secret manager for normal support

If the customer adds its own logging, SIEM, APM, or cloud-monitoring stack,
that telemetry remains customer-managed and should stay metadata-oriented by
default. Shared dashboards or ticket attachments should exclude transcript text,
raw payloads, secrets, and tenant-specific identifiers unless explicitly
approved as an exception.

## Support Boundary

The default support posture is sanitized evidence first.

Support should begin with:

- a sanitized diagnostic bundle
- synthetic or fully sanitized reproduction material
- metadata-only audit records

Support should avoid by default:

- raw transcript files or transcript excerpts
- raw Gong API payloads
- failing payload bodies copied from customer systems
- shell or console access to customer cloud resources
- direct access to customer SQLite or transcript directories

When sanitized evidence is insufficient, direct access can be granted only as a
time-bound logged exception under the customer support-access policy described
in [Support](support.md).

## Customer-Hosted Deployment Expectations

An enterprise customer-hosted deployment should provide:

- a customer-owned runtime for `gongctl`
- a protected customer-owned data root for SQLite, transcript files, profile
  files, and backups
- customer-managed secrets for Gong and any MCP bearer token
- operator ownership for refresh cadence, retention, and incident handling
- customer-managed audit logging for support exceptions and administrative
  access

## Shared Responsibility

`gongctl` and `gongmcp` are designed to preserve a clean customer-hosted
boundary, but that boundary also depends on customer deployment choices.

The customer is expected to control:

- host access
- secret storage
- backup and restore policy
- retention windows
- network exposure
- downstream AI-provider approval when MCP or CLI outputs are sent to a model

The vendor is expected to support a minimal-data troubleshooting path and avoid
default workflows that require broad data export or remote access.
