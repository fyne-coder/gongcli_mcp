# Changelog

## Unreleased

- Added a stable MCP facade vertical slice with `analyst-facade`,
  `gong_discover_capabilities`, and routed `gong_query` /
  `gong_analyze` / `gong_get_evidence` operations so approved clients can use
  a smaller durable tool surface while existing analyst tools remain available
  for operator testing.
- Added a Postgres question-parity matrix that maps SQLite-era business
  questions to the reviewed Postgres pilot status: supported, supported with
  caveats, or blocked.
- Added a Postgres client manual-test checklist with first-session prompts,
  expected tool sequences, pass/fail criteria, restricted-customer negative
  probes, transcript-summary caveats, scorecard preset checks, and rollback
  guidance for controlled client pilots.
- Added a Postgres client deployment runbook for the two-database
  source/serving layout, scoped analyst reader grants, auth gateway checks,
  smoke tests, backup/restore, and rollback.

## 0.3.4 - 2026-05-05

- Added Postgres client pilot release packet and onboarding
  checklist for controlled shared deployments, including scoped-role,
  synthetic-evidence, customer-platform dry-run, digest-pinning, and rollback
  requirements.
- Added scoped Postgres analyst reader support for reviewed
  `analyst` and `analyst-expansion` sessions under
  `GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS=1`, while keeping Postgres
  `all-readonly`, `all-tools`, and `all` rejected.
- Added MCP-layer small-cell suppression for enforced scoped Postgres analyst
  dimension outputs, omitting buckets below 3 calls with explicit
  `small_cell_suppression_applied` metadata while leaving SQLite defaults
  unchanged.
- Added Postgres explicit allowlist support for filtered
  `missing_transcripts`, matching SQLite date, lifecycle, scope, system,
  direction, and CRM object filters for admin transcript-backfill workflows.
- Added Postgres explicit allowlist support for
  `compare_lifecycle_crm_fields`, returning aggregate lifecycle-vs-CRM field
  population comparisons for the reviewed `Opportunity` object type without
  exposing call IDs, CRM object identifiers, raw CRM values, raw JSON, raw
  hashes, or transcript content.
- Added Postgres explicit allowlist support for
  `search_transcripts_by_crm_context`, returning CRM-constrained transcript
  snippets through a reader-executable function while redacting CRM object
  IDs/names, call titles, raw CRM values, raw JSON, raw hashes, and full
  transcript text from the SQL result shape.
- Added Postgres explicit allowlist support for
  `opportunity_call_summary`, returning redacted Opportunity call coverage
  aggregates through a reader-executable function without exposing Opportunity
  IDs/names, owner IDs, amount, close date, latest call IDs, or raw CRM values.
- Added Postgres explicit allowlist support for
  `crm_field_population_matrix`, returning aggregate field-population cells by
  approved categorical group fields without exposing object IDs/names, call IDs,
  raw CRM values, raw JSON, or raw hashes.

## 0.3.3 - 2026-05-04

- Hardened lab-auth trusted proxy identity handling so client-supplied
  `X-Auth-Request-*` and `X-Forwarded-*` identity headers are stripped at the
  bundled Caddy `/mcp` routes and are ignored unless proxy-header trust is
  explicitly enabled.
- Changed lab, JumpCloud, and Cognito auth examples so trusted proxy identity
  headers default to disabled and require an explicit `TRUST_PROXY_CIDRS` opt-in
  when `TRUST_PROXY_HEADERS=1`.
- Added shim regression tests and live lab smoke coverage for forged proxy
  identity header denial.
- Fixed CI invocation of `staticcheck` and `govulncheck` so quoted tool paths
  are executed correctly by the GitHub Actions shell.
- Added scorecard activity sync, analysis, and aggregate MCP summary surfaces
  for answered scorecard activity without exposing raw call/user IDs or answer
  text.
- Changed `search_transcript_segments` MCP output to omit raw `call_id` and
  `speaker_id` fields by default. Use aggregate and redacted MCP surfaces unless
  an explicit future opt-in is added.

## 0.3.2 - 2026-05-02

- Added a Proxmox/Cloudflare/Keycloak lab-auth deployment harness for remote
  MCP OAuth rehearsal, including protected-resource metadata,
  dynamic-client-registration checks, offline-token and audience/group claim
  validation, OpenAI Responses API smoke testing, and ChatGPT manual connector
  guidance.
- Prepared the public `v0.3.2` surfaces by aligning Docker/Claude helper
  defaults, generalizing lab-auth host examples, and clarifying when GHCR tag
  artifacts must exist before copy/paste use.
- Fixed MCP tool-call compatibility with clients that send reserved `_meta`
  extension fields by stripping `_meta` before strict argument decoding while
  preserving validation for real unknown fields.
- Expanded remote MCP OAuth documentation and troubleshooting for ChatGPT,
  Claude remote MCP, MCP Inspector, and custom clients, including token
  exchange, audience/resource claims, refresh/offline behavior, group claims,
  and first `tools/call` validation.

## 0.3.1 - 2026-05-01

- Fixed the GHCR image publishing workflow by updating the pinned Trivy action
  to a currently resolvable release.
- Updated release-facing docs and helper defaults to point at `v0.3.1`.

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
