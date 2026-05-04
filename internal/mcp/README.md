# MCP Adapter Boundary

`internal/mcp` implements the read-only MCP request handling over SQLite. Stdio
is the default transport; HTTP mode is a minimal private-pilot transport wrapper
around the same request handler.

Rules:

- read from the SQLite store surfaces; do not call Gong directly
- keep tools read-only
- require `--db` at the `cmd/gongmcp` boundary
- support MCP tool presets and allowlisting through `gongmcp --tool-preset`,
  `GONGMCP_TOOL_PRESET`, `--tool-allowlist`, or `GONGMCP_TOOL_ALLOWLIST`; when
  unset, stdio serves the full read-only catalog
- require an explicit preset or allowlist for all HTTP deployments
- generate tool schemas after server options are applied, so `tools/list`
  reflects the active allowlist and configured `LimitPolicy`
- keep row caps configurable through `LimitPolicy`, `GONGMCP_MAX_*` env vars,
  and `gongmcp --max-*` flags, but hard-cap them in code
- treat schema `maximum` as the configured ceiling; omitted `limit` values use
  the lower request defaults in `limits.go` unless the configured ceiling is
  lower
- return cap-feedback envelopes only for capped high-volume row tools that hit
  their effective limit; other capped aggregate/config tools may keep their
  normal result shape
- support private AI governance suppression through `--ai-governance-config` or
  `GONGMCP_AI_GOVERNANCE_CONFIG`; do not expose configured restricted names in
  MCP output, do not expose filtered-match counts, and require an explicit
  governance-compatible tool preset or allowlist
- keep browser/session auth separate from agent-client auth
- do not expose raw Gong API passthrough, arbitrary SQL, profile import, raw cached call JSON, or full transcript dumps
- return transcript snippets only, not full transcript bodies
- use `search_transcripts_by_call_facts` for scoped transcript evidence by date, lifecycle, scope, system, or direction; it may return bounded neighboring-segment excerpts, but must not return call IDs, titles, speaker IDs, or full transcript text
- use `search_transcript_quotes_with_attribution` when business users need bounded quote evidence with available Account/Opportunity attribution; call IDs, call titles, Account names/websites, and Opportunity names/close dates/probabilities require explicit opt-in flags, `transcript_status=missing` must fail closed because quote search starts from cached transcript segments, and person/contact titles must be reported as unavailable when not present in the cache
- redact call IDs and speaker IDs from transcript segment search by default; exact identifiers require explicit opt-in flags
- keep profile-aware tools tied to imported SQLite profile state
- return lifecycle source and profile provenance when profile-aware behavior is used
- keep unmapped CRM field output redacted by default
- treat `search_crm_field_values` as an explicit, bounded value lookup exception; call IDs are redacted unless `include_call_ids=true`, object IDs and names are always redacted, and value snippets plus call titles require `include_value_snippets=true`
- serve profile-aware fact/lifecycle queries from the SQLite profile cache keyed by active profile and canonical hash; writable CLI commands warm it, and read-only MCP reports stale cache state instead of writing
- keep `summarize_call_facts` on safe business grouping dimensions; directed CRM value lookup belongs in `search_crm_field_values`

Practical checks:

```bash
go run ./cmd/gongctl mcp tools
GONGMCP_MAX_SEARCH_RESULTS=250 go run ./cmd/gongctl mcp tool-info search_transcript_segments
go test -count=1 ./internal/mcp ./cmd/gongmcp
```
