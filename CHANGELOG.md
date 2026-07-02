# Changelog

## Unreleased

## 0.6.3 - 2026-07-02

- Renamed the reviewed CRM-dimension registry internals from Salesforce-specific
  wording to CRM-neutral field names, added non-Salesforce profile fixture
  coverage, and documented the remaining compatibility-column extraction plan.
- Added request-level `topic_packs` for `extract.buyer_questions` and
  `extract.objection_signals`. Generic B2B topic aliases and default seeds remain
  enabled by default; the opt-in `procurement` pack adds punchout/e-procurement
  vendor vocabulary.
- Added release-body public-surface scanning, fixture coverage, and CI/release
  gates so GitHub Release notes are checked before and after publication.
- Updated release-facing Docker image examples and deployment defaults to point
  at `v0.6.3`.

## 0.6.2 - 2026-07-02

- Removed integration-specific runtime text from the business-signal extraction
  warning, documented Salesforce-compatible CRM defaults as examples in the
  README, ignored local `gong-assistant-instructions.md`, and added
  `make public-surface-scan` to CI and release preflight.
- Updated release-facing Docker image examples and deployment defaults to point
  at `v0.6.2`.

## 0.6.1 - 2026-07-02

- Added row-level evidence provenance to Business Workbench
  `evidence.call_drilldown` responses so Gong AI condensed evidence and
  transcript-backed excerpts are explicitly distinguishable.
- Added drilldown answer-contract guidance and warnings for mixed-provenance
  and AI-condensed-only drilldowns so host assistants do not treat Gong AI
  dates, amounts, or summaries as buyer transcript quotes.
- Updated Business Workbench host instructions with business-user guidance for
  using `verbatim_transcript_excerpts` as customer-facing evidence and
  treating `ai_condensed_evidence` as directional context unless transcript
  excerpts support the claim.
- Fixed remote MCP auth bearer challenge metadata to advertise the
  endpoint-scoped protected-resource metadata URL and extended smoke
  verification for that URL.
- Updated release-facing Docker image examples and deployment defaults to point
  at `v0.6.1`.

## 0.6.0 - 2026-07-01

- Added a governed CRM-dimension registry for a fixed reviewed set of standard
  Account and Opportunity fields from cached Gong CRM context. Categorical and
  boolean promoted fields can be filtered and grouped directly; numeric and
  date fields are exposed through bucket/month/quarter dimensions instead of
  raw high-cardinality values.
- Promoted the reviewed CRM dimensions into SQLite and Postgres `call_facts`,
  MCP capability discovery, Business Workbench dimension filtering/counts,
  scoped-reader grants, migrations, and parity tests so analysts can use
  advertised dimensions such as `account_rating` without
  arbitrary CRM field probing.
- Improved Business Workbench dimension-filter handling, including contiguous
  quarter parsing, canonical dimension dispatch, lazy matcher context for
  backed dimensions, and duration-only fast paths.
- Added `GONGMCP_HTTP_TOOL_TIMEOUT` / `--http-tool-timeout` so approved
  large-cache HTTP MCP deployments can raise the per-tool deadline deliberately.
- Added a consolidated customer upgrade runbook covering 0.5.x promotion,
  schema/read-model handling, gateway smoke testing, rollback, and non-secret
  acceptance evidence.
- Updated release-facing Docker image examples and deployment defaults to point
  at `v0.6.0`.
- Operator note: the built-in promoted CRM dimensions are a fixed reviewed
  standard Account/Opportunity mapping set backed by cached Gong CRM context,
  not a generic custom-field discovery mechanism. Non-Salesforce and
  deployment-specific methodology/lifecycle mappings should still come from
  reviewed business profiles and the dimensions advertised by
  `gong_discover_capabilities`.

## 0.5.5 - 2026-06-25

- Added Business Workbench `query.call_count` and `query.dimension_counts`
  intent primitives, including guided repairs for malformed facade filters and
  explicit `query.calls` preview-limit metadata.
- Added participant domain, affiliation, and email dimension rollups with
  configurable internal participant domains and `hide_contact_emails` policy
  suppression for raw email buckets.
- Added Business Workbench host docs and remote MCP OAuth deployment
  configuration coverage for the new query operations.
- Added governed `filter.dimension_filters` support for Business Workbench
  business-analysis tools, starting with reviewed dimensions such as
  `account_revenue_range`, so ICP-style segmentation can be expressed without
  arbitrary CRM field probing.
- Added matching SQLite/Postgres business-analysis filtering, capability
  schema coverage, scoped-reader grant updates, and docs for the reviewed
  dimension-filter contract.
- Postgres upgrade note: superseded business-analysis SECURITY DEFINER
  function signatures are dropped and recreated by the new trailing migration;
  deploy the matching `gongctl`/`gongmcp` binaries with the migrated database.
- Added typed Postgres serving-refresh error classification, failed-step JSON,
  and `--statement-timeout` / `GONGCTL_REFRESH_STATEMENT_TIMEOUT` controls for
  `gongctl deploy postgres-refresh`.
- Extended `gongctl doctor postgres-deploy` and Postgres support bundles with
  sanitized deployment checks, serving-refresh marker freshness, read-model
  readiness, and `postgres-deployment.json` evidence.
- Documented the optional direct-OIDC group fallback for app-assignment-only
  deployments and added gateway tests for the fallback behavior.
- Updated the required local/release Go toolchain to 1.26.4.
- Updated release-facing Docker image examples and deployment defaults to point
  at `v0.5.5`.

## 0.5.4 - 2026-06-02

- Added an explicit no-governance-exclusions contract for Postgres MCP serving
  refreshes. Operators can now run `gongctl deploy postgres-refresh` or
  `gongctl governance refresh-serving-db` with `--no-governance-exclusions`
  instead of mounting an empty AI governance YAML when no customer exclusions
  exist.
- Added matching `gongmcp --no-governance-exclusions` /
  `GONGMCP_NO_GOVERNANCE_EXCLUSIONS=1` runtime startup support. Redacted
  serving mode now verifies that the serving DB was refreshed with the
  no-exclusions policy fingerprint before startup.
- Updated the Postgres deployment docs, Kubernetes starter, and single-VM
  Compose starter so no-exclusions deployments omit `ai-governance.yaml` and
  use the explicit flag/env contract.
- Updated release-facing Docker image examples and deployment defaults to point
  at `v0.5.4`.

## 0.5.3 - 2026-05-31

- Removed internal remote-MCP debug artifacts from the public docs surface
  and replaced the deployment-specific direct OIDC handoff with a
  company-generic operator starter.
- Added public-surface guardrails to `make secret-scan` so customer names,
  person names, support IDs, and lab hostnames from private debug loops cannot
  be reintroduced into tracked public files.
- Updated release-facing Docker image examples and deployment defaults to point
  at `v0.5.3`.

## 0.5.2 - 2026-05-31

- Added the customer-hosted remote MCP gateway (`gongmcp-gateway`) with
  protected-resource metadata, bearer challenges, JWT validation, trusted
  forwarding to private `gongmcp`, and an AWS Cognito Dynamic Client
  Registration starter.
- Added `gongctl doctor mcp-gateway`, remote-MCP smoke scripts, and Kubernetes,
  Compose, and IAM examples for validating hosted Claude/ChatGPT connector
  reachability before business users connect.
- Added the direct JumpCloud/OIDC compatibility path for static-client Claude
  connectors, keeping Cognito validation strict by default while supporting the
  tested JumpCloud/Ory token shapes under `OIDC_AUTH_PROFILE=direct-oidc`.
- Certified the remote MCP auth paths tested so far: Docker/Keycloak remained
  intact, and the direct JumpCloud + Claude custom connector reached live
  `get_sync_status` after using JumpCloud `Client Secret POST` and recreating
  stale Claude connector state. Public docs record the tested operator
  boundary without including internal debug notes.
- Added a company-generic direct OIDC gateway starter and updated operator docs
  with the current hosted connector checklist, including canonical `/mcp`,
  callback URL, client-auth method, stale-connector cleanup, and proof via
  first safe tool call rather than browser login alone.
- Updated release-facing Docker image examples and deployment defaults to point
  at `v0.5.2`, including the new `gongmcp-gateway` image.

## 0.5.1 - 2026-05-26

- Added a DevOps-oriented remote MCP deployment requirements guide covering
  hosted ChatGPT/Claude reachability, local/private MCP modes, required
  infrastructure, ownership boundaries, auth responsibilities, configuration
  inventory, smoke tests, common failure modes, and security guardrails.
- Clarified the public gateway/broker versus private upstream `gongmcp`
  boundary across the README, deployment, quickstart, security, and runbook
  docs so hosted connectors do not expose raw MCP services directly.
- Hardened JumpCloud/Cognito `oauth2-proxy` guidance with Dynamic Client
  Registration and Client ID Metadata caveats, OIDC discovery and `jwks_uri`
  requirements, metadata-success troubleshooting, unauthenticated POST failure
  triage, and explicit bearer-token validation expectations.
- Updated release-facing Docker image examples and deployment defaults to point
  at `v0.5.1`.
- Fixed the Postgres backup/restore smoke script so container-local Postgres
  client commands use explicit TCP instead of relying on Unix socket defaults.

## 0.5.0 - 2026-05-23

- Added deployment simplification for customer-hosted Postgres/Kubernetes
  pilots: `gongctl deploy postgres-refresh`, `gongctl doctor postgres-deploy`,
  preset-aware `sync status --preset`, a Kustomize starter, and an
  operator-owned Kubernetes smoke Job that does not require business-user MCP
  access.
- Added durable redacted-serving refresh markers and sanitized deployment
  diagnostics so operators can distinguish source/read-model, serving-refresh,
  marker freshness, and scoped-reader grant issues without printing database
  URLs, secrets, call IDs, call titles, customer names, or transcript text.
- Added single-VM and Kubernetes deployment-doc updates for pinned images,
  AI-governance config mounting, scoped reader validation, and fresh one-shot
  smoke Job execution. The Kubernetes starter was validated end to end on a
  disposable k3d cluster with synthetic Postgres data.

## 0.4.6 - 2026-05-19

- Added a Postgres Kubernetes operator setup guide that covers blank-database
  initialization, first sync scope, recurring refresh cadence, `gongctl` image
  invocation, and MCP smoke testing without a business-user login.
- Clarified Postgres scheduling docs so Kubernetes shared deployments use
  direct `gongctl sync ...` commands or reviewed shell wrappers instead of the
  SQLite-oriented `sync run --config` path.
- Expanded the Postgres/Kubernetes operator docs with explicit source writer,
  serving writer, and scoped reader URL separation, including phase-by-phase
  `GONG_DATABASE_URL` guidance for sync, serving refresh, grant reconciliation,
  and MCP validation.
- Documented the `broad-public-redacted` scoped-reader validation path: use
  `postgres-reader-apply --preset broad-public-redacted`, validate through
  `gongmcp` with the same reader URL and preset, and do not use generic
  `gongctl sync status` as the broad-redacted acceptance check.
- Clarified JumpCloud/OIDC deployments: JumpCloud remains the public
  gateway/broker auth layer while upstream `gongmcp` stays protected by
  internal bearer auth.
- Updated release-facing Docker image examples and deployment defaults to point
  at `v0.4.6`.

## 0.4.5 - 2026-05-10

- Reworked the top-level README to put the product benefits first: governed
  business questions over bounded Gong evidence, local/customer-controlled
  data handling, read-only MCP access without sharing Gong API credentials,
  and a clearer path from ad-hoc analysis to Gong-native or governed workflows.
- Updated release-facing Docker image examples and deployment defaults to point
  at `v0.4.5`.

## 0.4.4 - 2026-05-10

- Added customer-hosted Postgres deployment starters for AWS ECS runtime and
  simple single-VM Compose installs. The AWS starter runs `gongmcp` against an
  existing scoped Postgres serving DB with Secrets Manager-backed reader URL,
  bearer token, and AI governance YAML; the single-VM starter keeps source DB,
  redacted serving DB, scoped reader grants, operator jobs, and read-only HTTP
  `gongmcp` on one host while preserving credential boundaries.
- Updated deployment docs, customer-hosted package docs, onboarding, pilot,
  quickstart, and security checklists so IT/dev teams can choose between
  SQLite starters, single-VM Postgres, AWS ECS Postgres runtime, and the
  Postgres runbook without relying on the synthetic dev Compose file.

## 0.4.3 - 2026-05-10

- Cleaned up the remaining SQLite/Postgres documentation wording after the
  `v0.4.2` backend audit. Remote-auth env examples now label their
  `GONGMCP_DB` mounts as SQLite starter configuration, Postgres docs use
  shared-deployment/backend wording instead of stale "vertical slice" language,
  and release-facing image examples now point at `v0.4.3`.
- Updated user-facing Postgres unsupported-tool errors to refer to the
  reviewed Postgres backend instead of the older vertical-slice label.

## 0.4.2 - 2026-05-10

- Clarified AI governance documentation across SQLite and Postgres deployment
  modes. The docs now distinguish SQLite filtered DB export, raw/source
  Postgres prepared-policy governance for the narrowed `governance-search`
  preset, and physically redacted Postgres serving DB refresh for client-facing
  governed MCP deployments.

## 0.4.1 - 2026-05-10

- Closed the business-workbench GA follow-up slice with deterministic wrapper
  regression coverage, policy/readiness caveats, positive-prospect harness
  support, and app-only lab deploy documentation for preserving the auth stack
  while refreshing MCP code.
- Added public developer orientation and facade routing metadata so operators
  and downstream MCP hosts can inspect routed operations, deployment posture,
  and support boundaries without exposing raw tenant data.
- Tolerated in-place governance config during lab deploys, allowing app-only
  updates to reuse existing VM-side governance policy files instead of
  requiring a full auth-stack reset.

## 0.4.0 - 2026-05-07

- Added the customer-hosted Postgres `business-workbench` GA acceptance
  package. Operators can run `gongctl mcp ga-acceptance` or
  `scripts/postgres-ga-acceptance-smoke.sh` to produce a non-secret
  pass/degraded/fail report covering runtime identity, the six-tool facade
  surface, routed evidence operations, profile/data readiness,
  governance/redaction, `question.answer` -> `evidence.call_drilldown`
  workflow, and scoped-reader read-only posture.
- Added GA profile/readiness gates for customer deployments. `gongctl profile
  validate --ga-readiness` fails when a reviewed profile is missing lifecycle
  buckets, maps concepts only to `CreatedDate`, leaves methodology unmapped, or
  lacks loss-reason mapping; `gong_status` now exposes profile and
  call-facts-attribution readiness signals so data/config gaps are reported as
  setup limitations instead of hidden failures.
- Added a runtime provenance gate for GA acceptance. `version=dev`,
  `commit=unknown`, missing `build_date`, or equivalent non-release metadata
  now fails the `runtime_identity` acceptance check even when the MCP tool
  workflow itself works.
- Added redacted serving database acceptance evidence. The Postgres GA smoke
  accepts `REDACTION_AUDIT_JSON` or compact `REDACTION_AUDIT_*` fields so
  operators can record source-vs-serving redaction proof without including
  database URLs, raw call IDs, customer names, or transcript text.
- Added the six-tool `business-workbench` MCP preset for customer business
  users. The visible surface is `gong_status`,
  `gong_discover_capabilities`, `gong_query`, `gong_analyze`,
  `gong_get_evidence`, and `gong_explain_limitations`, with reviewed routed
  operations underneath for ad-hoc business questions, theme intelligence,
  quotes, Gong AI highlights, and call drilldown.
- Improved the customer sales/marketing business-workbench prompt path for GA:
  broad `question.answer` theme prompts now return `status=needs_theme_seed`
  with suggested seeded workflows instead of weak literal-word evidence;
  `theme_intelligence_report` and quote evidence support limited,
  attribution, and full `field_profile` presets plus buyer/seller
  `speaker_role` filtering; and `extract.buyer_questions` /
  `extract.objection_signals` provide seeded external-speaker evidence paths
  for sales enablement and marketing users.
- Added `question.answer`, `theme_intelligence_report`, and
  `evidence.call_drilldown` workflow improvements for business-user prompts,
  including deterministic fallback metadata, explicit synonym guidance,
  Gong AI brief/key-point/highlight source paths, per-call duration/title
  metadata where permitted, and speaker-role/title attribution that never
  infers missing titles from transcript text.
- Made the persona business-analysis dimension role-aware (Phase 13f). The
  `summarize_themes_by_persona`, `rank_personas_by_insight_quality`,
  `summarize_themes_by_dimension(dimension="persona")` and
  `analyze.themes.discover` paths now group calls into deterministic
  coarse role buckets: `procurement`, `supplier_enablement`,
  `it_security_integration`, `finance`, `operations`, `sales_revenue`,
  `executive`, and `other_title_present`, derived from cached party
  metadata (`parties` / `metaData.parties` `title`/`jobTitle`/`job_title`)
  and CRM Contact/Lead Title fields (`Title`, `JobTitle`, `Job_Title__c`,
  `JobTitle__c`). Raw participant titles are never returned as dimension
  values; calls with participants but no resolvable title fall back to
  `<blank>` (or `participant_title_present` when only the materialized
  `call_facts` flag survives), and the existing
  `participant_title_missing_or_unmapped` cohort warning continues to
  fire when no titles are present. SQLite and Postgres agree on the
  bucket assignment for synthetic representative fixtures. No new
  top-level MCP tool was added; small-cell suppression and existing
  redaction/governance gates remain in force. The Postgres
  `gongmcp_business_analysis_dimension` function now delegates persona
  classification to a SECURITY DEFINER helper
  `gongmcp_business_analysis_persona_bucket(text, boolean)` that is
  REVOKED from PUBLIC and only callable through the existing dimension
  function.
- Made broad cohort theme discovery less seed-fragile (Phase 13e). The
  `discover_themes_in_cohort` business-analysis tool and the
  `analyze.themes.discover` facade operation routed through `gong_analyze`
  now accept a selective cohort filter without `theme_query`/`query`. In
  that seedless mode they return deterministic candidate theme terms from
  a bounded transcript sample within the cohort, plus a
  `broad_discovery_seedless` warning, a
  `broad_discovery_seedless_sample_only_cache_derived_keywords`
  limitation, and the existing limit-policy / runtime-suppression
  filters. Evidence and quote tools (`search_quotes_in_cohort`,
  `build_quote_pack`, `extract_theme_quotes`, `build_theme_brief`,
  `search_transcripts_by_filters`, etc.) keep their query-required gates
  so seedless mode does not escalate into raw transcript dumps.
- Added a reviewed `evidence.highlights.list` facade operation routed through
  `gong_get_evidence` that exposes bounded, redacted Gong AI highlight rows
  from the Postgres `call_ai_highlights` read model. The operation is an
  internal facade-routed handler (not a new top-level MCP tool); it requires
  explicit `call_ids`, caps inputs and rows, never returns raw highlight
  JSON, performs no raw account or customer enumeration, and filters
  runtime-suppressed call IDs before rows leave the server. Restricted calls
  remain absent because the redacted serving DB has no rows for them. The
  result envelope includes caveats stating that highlights are Gong
  AI-generated accelerators and that transcript quotes remain primary
  evidence.
- Added a Postgres `call_ai_highlights` read model for opt-in Gong AI
  Highlights captured under `content.highlights`. The table is rebuilt from
  raw call payloads, excluded from generic read-only grants, purged with
  call-scoped cache cleanup, and rebuilt on the redacted serving database only
  for allowed calls. The reviewed `evidence.highlights.list` facade operation
  is the only path that exposes typed highlight rows from this table.
- Added environment defaults for
  `gongctl governance refresh-serving-db`: source URL from
  `GONGCTL_SOURCE_DATABASE_URL`, target URL from `GONGCTL_MCP_DATABASE_URL`,
  and private governance config from `GONGCTL_AI_GOVERNANCE_CONFIG` or
  `GONGMCP_AI_GOVERNANCE_CONFIG`. Docs now clarify that redacted serving DB
  blocklist refreshes do not require recreating `gongmcp` when the serving DB
  URL, reader grants, auth, binary, and preset are unchanged.
- Added Phase 13k opt-in capture of Gong AI Highlights / brief /
  next-step content via `gongctl sync calls --include-highlights`. The
  flag mirrors `--include-parties`: it requires `--allow-sensitive-export`
  in restricted mode, sends `contentSelector.exposedFields.highlights=true`
  on `POST /v2/calls/extensive`, and adds
  `include_highlights_requested=true` /
  `include_highlights_result=request_sent` markers to the sync-run
  request context. Highlights land in raw call payloads only; no new MCP
  tool, schema, or aggregate. Current public exposure notes live in
  `docs/postgres-question-parity.md` and `docs/mcp-data-exposure.md`.
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

- Added a disposable lab-auth deployment harness for remote MCP OAuth
  rehearsal, including protected-resource metadata,
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
- Added evidence-backed sample prompt templates now covered by
  `docs/business-user-quickstart.md`,
  `docs/pilot-sponsor-and-operator-guide.md`, and
  `docs/analyst-tool-reference.md`, including content-gap discovery from
  prospect questions, recurring objection mining, renewal/expansion vs. churn
  risk, and late-stage pipeline risk.
- Caught up the canonical MCP catalog lists in `docs/architecture.md` and
  `docs/mcp-data-exposure.md` to include `search_transcripts_by_call_facts`
  and `search_transcript_quotes_with_attribution`.
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
