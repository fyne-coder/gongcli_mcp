# MCP Tool Intake Checklist

Use this checklist before adding a top-level MCP tool, facade operation, preset
membership, or Postgres allowlist entry. The goal is to keep MCP useful without
quietly widening the default exposure surface.

## Required Intake Record

Record the decision in the PR description, release notes, or local checkpoint:

- business question: the user question the tool answers and the operator role
  that owns it
- cache source: existing tables/files read by the tool and whether new sync
  capture is required
- backend scope: SQLite only, Postgres only, or both
- exposure level: one of the levels in [MCP Data Exposure](mcp-data-exposure.md)
- default preset decision: not exposed by default, business-workbench facade,
  business-pilot, analyst-core, analyst-business-core, analyst, governance
  search, redacted-serving broad surface, or SQLite `all-readonly`
- Postgres decision: unsupported, explicit allowlist only, reviewed preset, or
  future all-readonly candidate
- governance decision: whether AI governance suppression, blocklist checks,
  redacted serving DB refresh, or small-cell suppression applies
- output contract: bounded result shape, redaction defaults, title/sensitive
  metadata defaults, opt-in fields, and cap-hit feedback
- tests and smoke: synthetic fixtures only, read-only negative tests where
  applicable, and direct coverage for every new preset/allowlist path

## Implementation Gate

Do not merge a new MCP surface until all applicable items are true:

- [ ] The tool reads only the configured cache store; it does not call Gong
      live and does not accept arbitrary SQL, table names, or column names.
- [ ] The tool is listed in `docs/mcp-data-exposure.md` with classification,
      default protections, and residual risk.
- [ ] The tool has explicit limit/filter behavior and cannot return unbounded
      transcripts, raw JSON payloads, or full CRM value dumps.
- [ ] Sensitive identifiers and metadata are blanked by default, or the tool
      documents why specific metadata such as call titles is visible by default,
      names the policy/profile switch that suppresses it, and tests the default
      and suppressed paths.
- [ ] Business-user access goes through `business-workbench` facade operations
      unless the broader top-level tool list is intentionally approved.
- [ ] Postgres access is explicit: either rejected, narrow allowlist, reviewed
      preset, or redacted-serving broad surface with scoped-reader grants.
- [ ] Postgres `all-readonly` remains rejected unless full query parity,
      reader grants, governance behavior, and smoke coverage are reviewed for
      the entire catalog.
- [ ] If a scoped Postgres reader can use the tool, grant SQL and startup
      validation are updated together.
- [ ] AI governance and redacted-serving behavior are tested when the tool can
      reveal call, account, opportunity, participant, title, transcript, or CRM
      value context.
- [ ] Tests use synthetic fixtures only and include at least one negative case
      for redaction, allowlist, governance, read-only, unsupported backend, or
      invalid input behavior.
- [ ] Docs explain the intended operator or business-user workflow and any
      remaining limitations.
- [ ] `CHANGELOG.md` is updated when the surface is user-visible.

## Preset Rules

- `business-workbench`: expose stable facade tools only; route new business
  operations behind the facade after evidence policy and output contracts are
  reviewed.
- `business-pilot`: keep narrow status and aggregate tools only.
- `operator-smoke`: keep minimal deployment validation tools only; do not add
  business-analysis or admin/debug surfaces here unless the smoke contract
  explicitly needs them.
- `analyst-core` / `analyst-business-core` / `analyst`: add tools only after
  cache mapping, exposure classification, limits, and Postgres reader behavior
  are reviewed.
- `governance-search`: allow only governance-compatible search paths that can
  enforce the prepared policy without receiving restricted names in MCP output.
- `redacted-all-readonly`: internal manual testing only, and only over a
  physically redacted serving DB with scoped reader grants.
- `broad-public-redacted`: customer-test broad surface only with the redacted
  serving DB marker, scoped grants, governance config, and policy switches.
- `all-readonly`: SQLite trusted-admin/analyst surface. Postgres must fail
  closed until full-catalog parity is deliberately approved.

## Review Evidence

Before closeout, run the narrowest focused tests plus the repo gates required by
the change. For new or changed Postgres surfaces, include a smoke or synthetic
integration test that proves:

- the selected preset exposes the tool or facade operation;
- unsupported presets fail closed;
- the read-only role cannot write;
- raw/source-only tables or columns are not directly readable through a scoped
  role;
- the output shape matches the exposure decision.
