# Roadmap

`gongctl` should mature through explicit deployment gates instead of treating all
hardening as one broad "production ready" milestone. The product boundary stays:
`gongctl` syncs and maintains a local cache; `gongmcp` reads that cache only.

## Current Baseline

- Local CLI for Gong auth, sync, search, analysis, transcript fetch/export, and
  SQLite-backed status checks.
- Read-only local stdio MCP server over SQLite.
- Docker packaging for local/company-managed CLI and MCP use.
- Version source in `VERSION`, changelog entries, and SemVer-style tags.
- Public-safe docs for local data handling, Docker, release flow, and readiness.

## Gate 1: Enterprise Pilot Ready

Goal: a company can run a limited, reviewed pilot with admin-only sync and
business-user MCP access over a read-only cache.

Required outcomes:

1. Repo identity is unambiguous across GitHub repo, Go module, Docker labels,
   release metadata, and security advisory links.
2. Client-facing docs exist for enterprise deployment, security model, MCP data
   exposure, operator sync, business-user use, and pilot scope.
3. Data refresh is repeatable through a documented config/runbook, with clear
   cache freshness and readiness signals.
4. `gongmcp` supports a tool allowlist so a company can expose only approved MCP
   tools.
5. High-risk CLI commands have a restricted/company mode that blocks or requires
   explicit override for raw API, transcript export/sync, raw cached JSON, and
   extended CRM context.
6. MCP output contracts are tested for read-only behavior, bounded results,
   redaction defaults, no raw JSON, and no full transcript dumps.

Pilot packet docs:

- [Enterprise deployment](enterprise-deployment.md)
- [Security model](security-model.md)
- [MCP data exposure](mcp-data-exposure.md)
- [Operator sync runbook](runbooks/operator-sync.md)
- [Business-user guide](business-user-guide.md)
- [Pilot plan](pilot-plan.md)

## Gate 2: Company Production Ready

Goal: the project is safe to operate repeatedly inside a company with defined
owners, retention, upgrade, rollback, and security controls.

Required outcomes:

1. Scheduled sync patterns are documented and supported by a config file, for
   example `gongctl sync run --config company-sync.yaml`.
2. Cache inventory and purge commands let operators answer what sensitive data is
   present and remove approved slices without manual SQLite work.
3. MCP tool intake is formal: every new tool starts from a business question,
   maps to cached data, gets an exposure classification, ships behind allowlists,
   and has regression tests.
4. Supply-chain checks cover Go tests, `go vet`, static analysis, vulnerability
   scanning, secret scanning, Docker scanning, SBOM/checksums, and pinned release
   artifacts.
5. A company can deploy a `gongmcp`-only image or target with no Gong
   credentials, no network, and a read-only SQLite mount.
6. Upgrade and rollback procedures protect existing SQLite caches and require
   migration testing on copies before production use.

## Gate 3: Public 1.0 Ready

Goal: outside companies can evaluate and adopt a stable release contract without
maintainer hand-holding.

Required outcomes:

1. Stable documented CLI/MCP contracts for supported commands and tools.
2. Signed or provenance-backed release artifacts and container images.
3. Security disclosure, versioning, deprecation, and compatibility policies.
4. Complete operator and business-user documentation with approved and disallowed
   workflows.
5. A tested feedback loop for adding MCP tools without widening the default data
   exposure surface.

## Feature Direction

- Keep MCP read-only over cached SQLite. Do not add live Gong API calls to MCP.
- Add new MCP tools only after the CLI/cache layer can ingest the required data
  safely.
- Prefer aggregate and metadata-first tools. Transcript snippets and CRM values
  remain bounded, redacted by default, and allowlist-controlled.
- Keep business users on `gongmcp`; keep `gongctl` for IT/RevOps operators.
- Treat customer data refresh as an operator workflow with explicit scope,
  schedule, storage, retention, and cache freshness reporting.
