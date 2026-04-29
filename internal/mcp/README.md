# MCP Adapter Boundary

`internal/mcp` implements the local read-only stdio MCP adapter over SQLite.

Rules:

- read from the SQLite store surfaces; do not call Gong directly
- keep tools read-only
- require `--db` at the `cmd/gongmcp` boundary
- keep browser/session auth separate from agent-client auth
- do not expose raw Gong API passthrough, arbitrary SQL, profile import, raw cached call JSON, or full transcript dumps
- return transcript snippets only, not full transcript bodies
- use `search_transcripts_by_call_facts` for scoped transcript evidence by date, lifecycle, scope, system, or direction; it may return bounded neighboring-segment excerpts, but must not return call IDs, titles, speaker IDs, or full transcript text
- redact call IDs and speaker IDs from transcript segment search by default; exact identifiers require explicit opt-in flags
- keep profile-aware tools tied to imported SQLite profile state
- return lifecycle source and profile provenance when profile-aware behavior is used
- keep unmapped CRM field output redacted by default
- treat `search_crm_field_values` as an explicit, bounded value lookup exception; call IDs are redacted unless `include_call_ids=true`, object IDs and names are always redacted, and value snippets plus call titles require `include_value_snippets=true`
- serve profile-aware fact/lifecycle queries from the SQLite profile cache keyed by active profile and canonical hash; writable CLI commands warm it, and read-only MCP reports stale cache state instead of writing
- keep `summarize_call_facts` on safe business grouping dimensions; directed CRM value lookup belongs in `search_crm_field_values`
