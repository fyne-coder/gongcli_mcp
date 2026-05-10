# Security Checklist

Single-page checklist for a security reviewer signing off a `gongctl` /
`gongmcp` deployment. Each item links to the deeper doc that describes the
control. If you cannot tick an item, do not deploy until the linked doc has
been read.

## Source code and supply chain

- [ ] Repo provenance verified (clone URL, signed tag if available) — see
  [Release process](release.md)
- [ ] Image digest pinned for each environment, not floating tags — see
  [Release process](release.md) and [Docker deployment](docker.md)
- [ ] No customer credentials, transcripts, or DB files in the public source
  tree (`git ls-files` clean of `.env`, `*.db`, `*.sqlite*`, transcript JSON)
- [ ] License acceptable for intended use (`LICENSE` is MIT)
- [ ] CI / build pipeline does not embed secrets in built artifacts

## Credential boundary

- [ ] Gong API key + secret stored in a secret manager, not in source, image,
  or crontab — see [Security model §Credential Flow](security-model.md#credential-flow)
- [ ] `gongctl` is the only process that receives Gong credentials; `gongmcp`
  does not — see [Security model §Capability Model](security-model.md#capability-model)
- [ ] HTTP MCP bearer token, if used, is delivered through a mounted secret
  file or platform secret manager (not a CLI flag, not an image layer) — see
  [Remote MCP auth](remote-mcp-auth.md)
- [ ] OIDC client secrets / cookie secrets for the auth gateway are
  customer-managed — see [Remote MCP auth](remote-mcp-auth.md)
- [ ] Postgres reader URL is the MCP service credential, not an analyst SQL
  login; reader role has no write privilege — see
  [Postgres client deployment runbook](runbooks/postgres-client-deployment.md)

## Data boundary

- [ ] Cache (SQLite or Postgres) lives outside any source / VCS tree — see
  [Data boundary statement](data-boundary-statement.md)
- [ ] Transcript output directory is owned by the service user with no group
  / world read — see [Data handling](data-handling.md)
- [ ] Profile YAML treated as restricted operator state when it describes a
  real tenant
- [ ] Tenant data class understood and recorded — see
  [Security model §Data Classification](security-model.md#data-classification)
- [ ] AI governance config (blocklist, customer-name aliases) reviewed and
  applied where required — see [AI governance](ai-governance.md)

## Process and capability boundary

- [ ] Restricted CLI mode enabled for company-managed jobs:
  `GONGCTL_RESTRICTED=1` — see
  [Enterprise deployment §Restricted CLI Mode](enterprise-deployment.md#restricted-cli-mode)
- [ ] `--allow-sensitive-export` / `GONGCTL_ALLOW_SENSITIVE_EXPORT=1` used
  only for explicitly approved operator actions, not as a default
- [ ] `gongmcp` started with the narrowest tool preset that answers the
  questions the deployment is approved for — see
  [MCP data exposure §Tool Exposure Matrix](mcp-data-exposure.md#tool-exposure-matrix)
- [ ] Stdio MCP container uses a read-only data mount and `--network none`
  — see [Docker deployment](docker.md)
- [ ] HTTP MCP bound to localhost or behind the approved proxy unless
  `--allow-open-network` is consciously set
- [ ] HTTP MCP requires bearer auth and explicit Origin allowlist for
  non-localhost deployments
- [ ] `GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS=1` set when running scoped
  Postgres reader roles

## Scheduling and lifecycle

- [ ] Scheduler (cron / systemd / launchd / K8s CronJob) runs as a non-root
  service user — see [Scheduling cache refreshes](scheduling.md)
- [ ] Scheduled job has a named owner and a backup owner; both documented
- [ ] Single-instance lock prevents stacked runs — see
  [Scheduling cache refreshes §Pattern 1 — `cron`](scheduling.md#pattern-1--cron)
- [ ] Pre-refresh backup taken; restore tested — see
  [Operator sync runbook §Backup, Retention, And Restore](runbooks/operator-sync.md#backup-retention-and-restore)
- [ ] Retention policy is a reviewed YAML (not ad-hoc flags) with all
  approval fields populated — see
  [docs/examples/retention-policy.example.yaml](examples/retention-policy.example.yaml)
- [ ] Refresh failure alerting wired to the on-call channel (exit-code +
  staleness probe)
- [ ] Decommissioning plan documented — see
  [Operator sync runbook §Decommissioning](runbooks/operator-sync.md#decommissioning)

## Network boundary

- [ ] Outbound to Gong API restricted to `gongctl` host(s) only
- [ ] `gongmcp` inbound traffic restricted to MCP host(s) / approved gateway
- [ ] TLS termination handled by a reviewed reverse proxy when MCP is exposed
  to non-local clients — see [Remote MCP auth](remote-mcp-auth.md)
- [ ] No raw `gongmcp` HTTP endpoint exposed on the public internet without
  the auth gateway

## Audit and incident response

- [ ] Logs do not contain transcript bodies or secret values (CLI default;
  custom wrappers verified)
- [ ] Sanitized diagnostic-bundle workflow understood — see
  [Support](support.md)
- [ ] Incident response steps known for: credential failure, schema mismatch,
  data exposure, partial refresh — see
  [Operator sync runbook §Incident Response](runbooks/operator-sync.md#incident-response)
- [ ] Channel for rotating Gong credentials and MCP bearer tokens documented

## AI host boundary (where applicable)

- [ ] AI host (ChatGPT / Claude / Cursor / other) is on the approved
  subprocessor list for the data class
- [ ] Host-side policy bounds tool calls to the approved preset; client does
  not assume `tools/list` is the whole catalog
- [ ] Coding-agent guidance for power users / IT: do not paste real Gong
  credentials, real customer profile YAML, or real transcript output into a
  hosted agent unless your company has approved that data path — see
  [docs/README.md](README.md#working-with-a-coding-agent)

## Pre-go-live smoke

- [ ] `gongctl auth check` passes from the operator host
- [ ] `gongctl sync run --config <yaml> --dry-run` passes for the scheduled
  config — see [docs/examples/sync-run.example.yaml](examples/sync-run.example.yaml)
- [ ] Postgres deployments: `scripts/postgres-smoke.sh` passes — see
  [Postgres client deployment runbook](runbooks/postgres-client-deployment.md)
- [ ] Read-only MCP responds to `tools/list` and `get_sync_status` from the
  configured host
- [ ] One end-to-end question answered through the approved AI host with the
  approved preset

## Where to go deeper

- [Security model](security-model.md) — full trust boundaries, capability
  model, residual risks
- [Security questionnaire](security-questionnaire.md) — pre-canned answers
  to the typical security-review formats
- [MCP data exposure](mcp-data-exposure.md) — per-tool exposure detail
- [Customer-hosted package](customer-hosted-package.md) — deployment-context
  audit map
