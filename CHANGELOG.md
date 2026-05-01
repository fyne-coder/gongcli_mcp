# Changelog

## Unreleased

## 0.3.0 - 2026-05-01

- Added a customer-hosted Data Boundary Statement, support-access runbook, and
  `gongctl support bundle` metadata-only diagnostic command for sanitized
  support intake without raw transcripts, payloads, secrets, local paths, or
  customer-content identifiers.
- Added a customer-hosted package index, remote MCP OAuth/SSO and ChatGPT
  connector setup guide, Terraform starter examples for AWS/Azure/GCP, and
  example security-questionnaire answers.
- Added `docs/quickstart.md` with a Docker-first path from Gong credentials to
  local SQLite cache and read-only MCP host configuration, plus links to deeper
  deployment, governance, security, and release docs.
- Added `docs/configuration-surfaces.md` to inventory existing YAML configs and
  rank the next YAML candidates, led by `gongmcp --config PATH` for MCP runtime
  policy.
- Added auth-ready HTTP MCP private-pilot mode for `gongmcp`, with bearer-token
  support, HTTP allowlist and non-local bind guardrails, request timeouts, and docs
  that separate stdio, private HTTP, and future hosted/OIDC service boundaries.
- Tightened HTTP MCP so bearer auth is the default for HTTP, unauthenticated
  HTTP requires an explicit localhost development flag, `/healthz` is available
  for infrastructure checks, and payload-free HTTP access logs record method,
  tool, status, duration, remote address, and auth mode.
- Added HTTP Origin validation for MCP requests; non-local HTTP deployments now
  require `GONGMCP_ALLOWED_ORIGINS` or `--allowed-origins`.
- Added private AI governance config support for deterministic customer-name
  exclusion from MCP output, plus a local `gongctl governance audit` command and
  synthetic-only docs/examples.
- Added `gongctl governance export-filtered-db` to create a physically filtered
  MCP SQLite copy that removes blocklisted call-dependent rows before LLM/MCP
  use. The export scans call titles, raw call metadata including participant
  emails, embedded CRM values, and transcript segment text.
- Added a GHCR publishing workflow for separate `gongctl` and MCP-only
  `gongmcp` images, plus release/docs updates for public Git and container
  consumption.
- Added GHCR release gates for tests, vet, secret scan, Docker smoke builds, and
  image vulnerability scans before image push.
- Refreshed enterprise pilot docs with a "Default Posture And Optional Wider
  Surface" section and an "MCP Call Volume And Limits" section in
  `docs/mcp-data-exposure.md` so single-user analyst workflows have a
  documented path to widen the catalog and turn on per-tool opt-ins, and so
  operators see the per-call cost model and server-enforced ceilings.
- Added four evidence-backed sample prompt templates to
  `docs/business-user-guide.md` covering content-gap discovery from prospect
  questions, recurring objection mining, renewal/expansion vs. churn risk, and
  late-stage pipeline risk; each lists required tools, opt-in flags, and an
  evidence-discipline rule.
- Caught up the canonical MCP catalog lists in `docs/architecture.md` and
  `docs/mcp-phase.md` to include `search_transcripts_by_call_facts` and
  `search_transcript_quotes_with_attribution`.
- Tagged Gate 1 (all six outcomes shipped) and Gate 2 (items 1, 2, 4, 5
  shipped; 3 and 6 partial) status in `docs/roadmap.md`.
- Cross-linked the new posture and volume sections from `README.md` and
  `docs/enterprise-deployment.md`, and pointed the business-user
  cache-freshness caveats at the operator sync runbook.
- Added `gongmcp --list-tool-presets` so operators can inspect the exact
  built-in MCP tool profiles from the binary they are deploying.
- Added current/previous bearer-token file support for private HTTP bridge
  rotation and payload-free access logs that record the accepted token slot.
- Added `gongctl profile history`, `profile diff`, `profile activate`,
  `profile import --activate=false`, and `profile schema` for RevOps profile
  review, staged activation, and rollback workflows.
- Pinned GitHub Actions to commit SHAs and added release docs for image digest
  verification and digest-pinned customer deployments.

## 0.2.0 - 2026-04-29

- Aligned the Go module path, Docker OCI source label, GoReleaser ldflags, and
  private vulnerability reporting URL to `github.com/fyne-coder/gongcli_mcp`.
- Added `gongmcp --tool-allowlist` and `GONGMCP_TOOL_ALLOWLIST` to enforce
  server-side MCP tool subsets for company deployments.
- Added restricted/company CLI mode for high-risk raw API, transcript,
  raw JSON, export, and extended CRM-context commands.
- Added `gongctl sync run --config ...` for repeatable operator refresh plans.
- Added `gongctl cache inventory` and dry-run-first `gongctl cache purge` for
  local cache governance and retention workflows.
- Added CI/release hardening for repo-local secret-pattern scanning, vet,
  staticcheck, govulncheck, Go module inventory, checksums, and default plus
  MCP-only Docker builds.
- Added `search_transcript_quotes_with_attribution` for bounded transcript quote
  evidence joined to safe Account/Opportunity attribution metadata.
- Added approved `sync calls --include-parties` participant capture for title
  readiness, with restricted-mode gating and sync-history fallback reporting.
- Changed the transcript sync default batch size to 100.

## 0.1.1 - 2026-04-29

- Added Docker packaging for local CLI and read-only MCP deployment.
- Added batch-aware transcript sync with a configurable batch size capped at 100.
- Added scoped MCP transcript evidence search by call facts for theme testing.
- Added release version metadata for CLI, MCP, Docker, and GoReleaser builds.
- Tightened MCP transcript segment search so call IDs and speaker IDs are redacted by default with explicit opt-in flags.

## 0.1.0

- Initial public `gongctl` CLI and SQLite-backed read-only MCP release baseline.
