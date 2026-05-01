# Example Security Questionnaire Answers

These answers are a starting point for customer review. Replace bracketed text
with customer-specific deployment facts before submitting to a security team.

## Product And Hosting

| Question | Example answer |
| --- | --- |
| What is the product? | `gongctl` is an unofficial local-first Gong API CLI plus `gongmcp`, a read-only MCP adapter over a customer-controlled SQLite cache. |
| Who hosts it? | The customer hosts the runtime, cache, secrets, logs, and network endpoint. The package does not require a vendor-hosted SaaS control plane. |
| Is this multi-tenant SaaS? | No. The current package is customer-hosted. Any multi-tenant routing or browser application would be a separate customer-owned or future application layer. |
| What environments are supported? | Local workstation, customer-managed VM/container, stdio MCP, and private HTTP MCP behind customer TLS/auth infrastructure. |

## Data Handling

| Question | Example answer |
| --- | --- |
| What data is processed? | Depending on sync scope, cached Gong call metadata, transcript text, users, selected CRM context, settings/scorecard metadata, sync state, and profile mappings. |
| Where is customer data stored? | In a customer-owned data root outside the source checkout, typically SQLite plus optional transcript/profile files. |
| Does the vendor receive transcript data by default? | No. There is no default outbound vendor telemetry or support upload path. |
| Are support bundles safe to send? | Support bundles are designed to exclude raw transcripts, raw payloads, customer-content identifiers, secrets, and local paths. They still contain customer operational metadata and should not be posted publicly. |
| Can restricted customer data be excluded from MCP use? | Yes. Operators can maintain a private AI governance YAML and export a physically filtered MCP database before starting `gongmcp`. |

## Credentials And Secrets

| Question | Example answer |
| --- | --- |
| Where are Gong credentials stored? | In customer-managed secret storage, environment variables, or ignored local `.env` files for the writable `gongctl` process only. |
| Does `gongmcp` need Gong credentials? | No. `gongmcp` reads an existing SQLite cache and should not receive Gong API credentials. |
| How are MCP bearer tokens stored? | Customer-managed secret manager, mounted secret file, systemd environment file, Docker secret, Kubernetes Secret, or equivalent platform secret. Tokens must not be committed or baked into images. |
| Is OAuth supported? | Native OAuth is not implemented in `gongmcp` yet. Production remote MCP should use a customer-managed OAuth broker/gateway in front of `gongmcp`, or a future native OAuth implementation. |

## Access Control

| Question | Example answer |
| --- | --- |
| How is write access controlled? | Only operator-owned `gongctl` sync jobs write to the cache. Business-user MCP runtimes mount the cache read-only. |
| How is tool access controlled? | HTTP MCP requires an explicit tool preset or allowlist. Stdio MCP can also use a preset or allowlist for business-user deployments. |
| Can users call raw Gong APIs through MCP? | No. MCP has no live Gong API passthrough and no write tools. |
| Can users export raw transcripts through MCP? | No raw transcript dump tool is exposed by MCP. Some search tools can return bounded snippets depending on the approved tool surface and opt-in flags. |

## Logging And Telemetry

| Question | Example answer |
| --- | --- |
| Does the package send telemetry to the vendor? | No default vendor telemetry is required for operation. |
| What logs are produced? | Customer-owned process, container, proxy, scheduler, and support/audit logs. Operators should keep logs metadata-oriented and avoid raw payload logging. |
| What should support ask for first? | A sanitized `gongctl support bundle`, synthetic reproduction fixture, or metadata-only error report. |
| Can support access the customer environment? | Only by customer-approved, time-bound, logged exception. Standing access is not part of the default support model. |

## Security Controls

| Question | Example answer |
| --- | --- |
| Is the MCP cache read-only? | `gongmcp` opens SQLite read-only and should run with a read-only mount. |
| Is network access required for MCP? | Stdio Docker MCP can run with `--network none`. HTTP MCP needs only the MCP listener behind customer-managed TLS/auth. |
| Does HTTP MCP validate browser origins? | Yes. Non-local HTTP requires `GONGMCP_ALLOWED_ORIGINS` or `--allowed-origins`; unexpected `Origin` headers are rejected before auth or tool dispatch. |
| Are result sizes bounded? | Yes. MCP tools enforce bounded result counts and the server enforces an MCP frame-size limit. |
| Is data encrypted at rest? | The package does not implement its own encryption layer. Use customer host, disk, volume, database, backup, and cloud encryption controls. |
| Is data encrypted in transit? | Gong API calls use HTTPS. Remote MCP should be exposed only through customer-managed HTTPS. Local stdio does not traverse the network. |

## AI Provider Review

| Question | Example answer |
| --- | --- |
| Which AI provider receives MCP results? | Customer-specific: `[ChatGPT Enterprise / Claude / internal model gateway / other]`. |
| Is provider approval required? | Yes. MCP results can contain Gong-derived metadata or snippets depending on the tools enabled, so downstream AI provider use requires customer approval. |
| Can the model call all tools? | Only the tools exposed by the MCP host and `gongmcp` preset/allowlist. `all-readonly` is available for trusted admin/analyst or fully reviewed filtered-DB deployments; start business users with status and aggregate tools. |

## Operations

| Question | Example answer |
| --- | --- |
| How are upgrades handled? | Pin image tags or digests, back up the cache first, run `sync status`, run MCP `tools/list`, then promote. |
| How is rollback handled? | Revert to the prior pinned image and prior cache backup if the new binary/cache combination fails validation. |
| How are retention deletes handled? | Use `gongctl cache purge` dry-run first, then confirm only after backup, legal-hold, and owner approval checks. |
| How is decommissioning handled? | Stop sync jobs, remove MCP host configs, revoke/rotate credentials, archive or destroy cache/transcript/profile data per policy, and remove unused images/volumes. |

## Known Limitations

- Native OAuth 2.1 is not implemented in `gongmcp`.
- Per-user MCP RBAC is not implemented in `gongmcp`.
- This package is not a multi-tenant SaaS layer.
- Customer cloud/IAM/TLS/logging controls are outside the repo and must be
  implemented by the customer deployment.
