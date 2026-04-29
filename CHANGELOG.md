# Changelog

## Unreleased

## 0.1.1 - 2026-04-29

- Added Docker packaging for local CLI and read-only MCP deployment.
- Added batch-aware transcript sync with a configurable batch size capped at 100.
- Added scoped MCP transcript evidence search by call facts for theme testing.
- Added release version metadata for CLI, MCP, Docker, and GoReleaser builds.
- Tightened MCP transcript segment search so call IDs and speaker IDs are redacted by default with explicit opt-in flags.

## 0.1.0

- Initial public `gongctl` CLI and SQLite-backed read-only MCP release baseline.
