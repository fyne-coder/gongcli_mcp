# Support

## Purpose

This document defines the default support posture for customer-hosted
`gongctl` deployments.

The support model is intentionally minimal-data:

- sanitized diagnostic bundle first
- synthetic reproduction preferred
- direct access by exception only
- every exception time-bound and audit-logged

## Default Support Access Policy

Vendor support should not require the following by default:

- raw logs
- raw transcripts
- failing raw request or response payloads
- direct access to customer cloud consoles
- shell access to customer hosts or containers
- direct access to customer SQLite cache files, transcript directories, or
  secret stores

The normal support lane should be enough to triage most issues with redacted,
metadata-only artifacts.

## Sanitized Diagnostic Bundle Workflow

The first requested artifact should be a sanitized diagnostic bundle created by
the customer or operator.

Generate the current bundle locally from the customer-owned SQLite cache:

```bash
mkdir -p "$HOME/gongctl-data/support-bundle"

docker run --rm \
  -v "$HOME/gongctl-data:/data:ro" \
  -v "$HOME/gongctl-data/support-bundle:/support" \
  ghcr.io/fyne-coder/gongcli_mcp/gongctl:v0.2.0 \
  support bundle --db /data/gong-mcp-governed.db --out /support
```

Add `--include-env` only when support needs environment-variable presence
checks. The command runs offline against local SQLite, opens the database
read-only, does not need Gong credentials, and does not make network calls.

Current `gongctl support bundle` files:

- `manifest.json`: product version, build metadata, Go runtime, database path
  class, path policy, file sizes, modified timestamp, cache call-date range,
  and read-only open mode
- `cache-summary.json`: table counts, transcript/CRM-context presence,
  aggregate cache counts, public readiness, profile readiness status, and sync
  run summaries without sync keys, cursors, raw errors, or request context
- `mcp-tools.json`: MCP tool catalog names, descriptions, and input schemas
- `redaction-policy.json`: excluded data classes and the default support policy
- `environment.json`: optional `--include-env` file containing presence
  booleans for known environment variables, never values

The bundle is customer-boundary operational metadata. It is designed to exclude
raw customer content, direct customer-content identifiers, secrets, and local
paths; it is not designed to be posted in public issues.
The generated response and redaction policy label this explicitly as
`sensitivity: customer_operational_metadata`; share it only under the
customer's support policy.

Operator-supplied case notes may add sanitized details such as:

- runtime mode: local CLI, Docker CLI, stdio MCP, or HTTP MCP
- exact command family and flags used, with secrets and local paths redacted
- error category, exit code, and timestamp
- restricted-mode status, tool preset/allowlist status, bearer-auth mode, and
  governance-config enabled yes/no status
- sanitized config shape with customer identifiers, aliases, URLs, and secrets
  removed
- sanitized audit trail for the failing operation: command started, command
  ended, status, and high-level error summary

The bundle should exclude by default:

- transcript text or transcript excerpts
- raw Gong API request or response bodies
- copied failing payload bodies
- bearer tokens, access keys, refresh tokens, cookies, or session material
- customer cloud account IDs, internal hostnames, or private IP inventories
- raw profile-discovery output containing tenant field values

If a problem can be isolated to schema shape or parser behavior, prefer a
minimal synthetic fixture over a larger sanitized bundle.

## Synthetic Reproduction Preference

When support needs a reproduction artifact, the preferred order is:

1. synthetic fixture created to match the failing shape
2. fully sanitized minimal example with customer identifiers removed
3. time-bound approved access to the customer environment only if the first two
   options cannot reproduce the issue

Support requests should explicitly state whether a synthetic reproduction was
attempted and why it did or did not reproduce the issue.

## Exception-Based Support Access

If sanitized evidence is insufficient, the customer may approve temporary
support access for a defined scope.

Required controls for exception access:

- named customer approver
- named vendor engineer or operator
- written justification tied to a support case or incident ID
- explicit systems in scope
- explicit data classes in scope
- start time and planned end time
- access revocation method
- audit-log entry recorded before access begins

Approved exception access should still avoid broad collection. Use the narrowest
possible method:

- read-only screen share before shell access
- single file or single query before full directory access
- metadata export before raw payload export
- bounded sample before bulk extraction

Standing access is out of scope for the default support model.

## Audit-Log Schema Expectations

Customer-hosted deployments should record support exceptions in an audit log
owned by the customer.

Minimum expected fields:

| Field | Expectation |
| --- | --- |
| `event_id` | Unique identifier for the support-access event |
| `case_id` | Support case, incident, or ticket ID |
| `requested_by` | Person requesting the exception |
| `approved_by` | Customer approver who authorized access |
| `performed_by` | Named vendor engineer or operator who used the access |
| `reason` | Short justification for the exception |
| `systems_in_scope` | Exact host, container, database, or cloud surface approved |
| `data_classes_in_scope` | Transcript, metadata, config, logs, payloads, or secrets; secrets should normally remain excluded |
| `access_method` | Screen share, temporary account, file transfer, paired session, or other reviewed method |
| `started_at` | Access start timestamp |
| `expires_at` | Planned access end timestamp |
| `revoked_at` | Actual revocation timestamp |
| `artifacts_shared` | Bundle names, file hashes, or ticket attachments shared during the session |
| `actions_taken` | High-level list of commands or actions performed |
| `outcome` | Resolved, mitigated, reproduced, or no finding |

Recommended additions:

- `customer_environment`
- `bundle_hash`
- `related_release`
- `follow_up_owner`

## Customer-Hosted Package Checklist

Before handing the package to an enterprise customer, confirm:

- the deployment model is documented as customer-hosted, not vendor-operated
- SQLite, transcript output, profiles, and backups are assigned to a protected
  customer-owned data root
- Gong credentials and MCP bearer tokens are stored in customer-managed secret
  storage
- restricted mode is enabled by default for operator-run jobs where applicable
- support contacts know that sanitized bundles are the default intake path
- a synthetic-fixture path exists for parser or schema reproduction work
- the customer has an exception approval path for temporary support access
- the customer has an audit-log destination for support exceptions
- no doc, ticket template, or runbook tells users to send raw transcripts,
  raw payload dumps, or secret-bearing logs by default

## Practical Support Flow

1. Customer opens a support case and provides a sanitized diagnostic bundle.
2. Vendor support attempts local or synthetic reproduction.
3. Vendor support requests one narrower artifact only if the first bundle is
   insufficient.
4. Customer approves time-bound direct access only if narrower artifacts still
   do not unblock diagnosis.
5. Customer revokes access, records the closeout, and retains the audit trail.
