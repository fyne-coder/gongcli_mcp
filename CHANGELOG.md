# Changelog

## Unreleased

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
