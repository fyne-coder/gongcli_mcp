# Changelog

## Unreleased

- Changed `search_transcript_segments` MCP output to omit raw `call_id` and `speaker_id` fields by default. Use aggregate and redacted MCP surfaces unless an explicit future opt-in is added.
- Added Docker packaging for local CLI and read-only MCP deployment.
- Added batch-aware transcript sync with a configurable batch size capped at 100.
- Added scoped MCP transcript evidence search by call facts for theme testing.
- Added release version metadata for CLI, MCP, Docker, and GoReleaser builds.

## 0.1.0

- Initial public `gongctl` CLI and SQLite-backed read-only MCP release baseline.
