# Pilot Plan

This document defines the business/pilot workstream for the `Enterprise Pilot
Review Packet`. The goal is to validate whether a reviewed `gongmcp` deployment
can answer a narrow set of business questions without giving business users
`gongctl`, Gong credentials, raw transcript access, or local database access.

## Pilot Objective

Prove that a business user can get useful, bounded answers from a reviewed local
cache through a narrow MCP tool set, while operator-owned sync and profile
workflows remain separate. Enforce the approved tool set with
`gongmcp --tool-preset business-pilot` or a reviewed custom allowlist, and layer host policy
on top if the deployment needs stricter prompts or routing.

This is now a software-enforced pilot lane for the first risk gates: `gongmcp`
can enforce the reviewed MCP tool subset, and restricted/company CLI mode blocks
high-risk raw API, transcript/export, raw cached JSON, and extended-context
commands unless an approved operator supplies the explicit override. Storage,
host, retention, and user-access controls still remain company responsibilities.

## Non-Negotiable Boundary

- Business users do not run `gongctl`.
- Business users do not receive Gong credentials.
- `gongmcp` reads a reviewed cache only and does not call Gong live.
- The pilot does not expose live tenant names, raw call/customer/CRM IDs,
  transcript text, secrets, or private database/file paths. Approved scorecard
  tools may expose scorecard, workspace, or question IDs as configuration
  metadata if reviewers accept that exposure.

## In-Scope Workflows

- Business-user Q&A on aggregate conversation mix, transcript coverage, and
  transcript backlog.
- Review of cached scorecard inventory and question text.
- Cache/readiness verification through `get_sync_status`.
- Sponsor review of whether the answers are useful enough for a larger rollout.

## Out-Of-Scope Workflows

- Raw transcript search or transcript export.
- Exact call review, call-level coaching, or participant-level monitoring.
- Directed CRM value lookup.
- Live API troubleshooting inside the business-user session.
- Multi-tenant analysis.
- Operator tasks such as sync, profile discovery/import, Docker changes, or
  storage-path management.
- HR, compensation, disciplinary, legal, or compliance judgments.

## Participants And Responsibilities

- Pilot sponsor: defines the approved business questions and owns the
  go/no-go decision.
- Pilot operator: runs `gongctl`, owns credentials, refreshes the cache, checks
  readiness, and exposes only the approved business-user MCP tool set.
- Security or RevOps reviewer: signs off on storage, retention, access, and
  acceptable-use boundaries.
- Business users: stay within approved prompts and escalate anything outside the
  documented scope.

## Approved Business Questions

The pilot is successful only if it can answer questions in these categories:

- Coverage: Where is transcript coverage weak by lifecycle, scope, direction,
  or month?
- Mix: Where is conversation volume concentrated across lifecycle, duration
  bucket, transcript status, or forecast category?
- Backlog: Which business-useful conversation slices should the operator
  prioritize for transcript refresh?
- Coaching surface: Which scorecards are present, and what question areas are
  already configured?
- Readiness: Which questions are blocked because CRM context, settings sync,
  transcript coverage, or lifecycle profile state is incomplete?

## Approved MCP Tool Set

The business-user pilot should expose only:

- `get_sync_status`
- `summarize_call_facts`
- `summarize_calls_by_lifecycle`
- `rank_transcript_backlog`
- `list_scorecards`
- `get_scorecard`

`list_scorecards` and `get_scorecard` can expose stable scorecard, workspace,
or question IDs. Treat those as sensitive configuration metadata and only enable
them when the pilot explicitly needs scorecard inventory.

Any wider tool surface requires a separate sponsor and reviewer decision.

## Dataset Limits

Keep the pilot intentionally narrow:

- One approved tenant cache.
- One approved sponsor and one operator-owned refresh workflow.
- One approved business unit, segment, or pilot cohort at a time.
- A reviewed time window, typically 30 to 90 days, rather than full account
  history.
- Enough synced calls and transcripts to answer coverage questions, but not a
  blanket mandate to ingest all available history.
- Scorecard/settings sync only to the extent needed for business-user inventory
  questions.

These are pilot governance limits, not claims about software-enforced hard caps.

## Cache Freshness And Readiness Rules

- The operator must complete at least one reviewed sync cycle before business
  access begins.
- Business sessions begin with `get_sync_status`.
- If readiness shows stale cache state, incomplete transcript coverage for the
  target question, missing settings inventory, or stale profile cache, the
  business session stops until the operator resolves it.
- Lifecycle-heavy questions require a reviewed active profile or must be labeled
  directional only.

## Success Criteria

The pilot passes only if all of these are true:

- Business users can answer the approved question set using only the approved
  MCP tool set.
- Business users never receive Gong credentials and never run `gongctl`.
- Outputs remain free of raw transcript text, secrets, raw call/customer/CRM
  IDs, private filesystem paths, and live tenant names. Approved scorecard
  configuration IDs are the only expected identifier exception.
- Sponsor-reviewed answers are materially useful for coverage, backlog, or
  scorecard-readiness decisions.
- Cache freshness and readiness limitations are visible and understood before
  answers are used.
- At least one refresh/review cycle proves the operator workflow is repeatable.

## Stop Gates

Stop the pilot immediately if any of these occur:

- A business user is asked to run `gongctl` or handle Gong credentials.
- The host app exposes raw transcripts, raw cached JSON, secrets, private file
  paths, or exact call/customer/CRM identifiers.
- The business workflow depends on live Gong calls instead of the reviewed
  cache.
- Sponsor questions cannot be answered without operator-only tools or directed
  CRM value lookup.
- Cache staleness or profile ambiguity is hidden, causing users to treat stale
  answers as current fact.
- Business users push into HR, compensation, disciplinary, legal, or compliance
  workflows.

## Expand Gates

Expand only after the initial pilot passes and reviewers agree on the next
surface. Reasonable expansion candidates are:

- A broader time window for the same approved tenant.
- Additional business units under the same operator workflow.
- A second reviewed prompt family that still stays aggregate-first.
- A carefully reviewed additional MCP tool, with updated allowlist and exposure
  review.

Expansion should not happen in the same step as initial pilot approval.

## Decision Framework

- Expand: all success criteria met, no stop-gate events, and sponsor confirms
  the answers are useful enough to justify a larger reviewed surface.
- Hold: the business value is plausible, but refresh cadence, lifecycle profile
  quality, or transcript coverage is not yet reliable enough.
- Stop: any stop gate fires, or the business case depends on workflows outside
  the reviewed cache-and-MCP boundary.
