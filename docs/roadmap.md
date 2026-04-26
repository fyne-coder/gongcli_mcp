# Roadmap

## MVP

1. `gongctl auth check`
2. SQLite-backed `sync calls`, `sync users`, `sync transcripts`, and `sync status`
3. SQLite-backed `search transcripts`, `search calls`, and `calls show --json`
4. `gongctl calls list --from --to --json`
5. `gongctl calls transcript --call-id`
6. `gongctl calls transcript-batch --ids-file --out-dir --resume`
7. JSONL output
8. rate limiting and `Retry-After` retry behavior
9. resumable checkpoints
10. sanitized fixtures and mock HTTP tests
11. tenant business profiles for CRM/lifecycle portability

## Distribution

- GitHub Actions test/build workflow
- GitHub Releases artifacts
- Homebrew tap formula after the binary interface stabilizes

## Later

- OAuth login/storage for Gong Collective-style app use
- read-only local MCP server
- remote MCP design after local MCP proves useful
- richer SQLite query/report surfaces after the current CLI contract settles
- transcript refresh policy for re-checking already cached transcripts whose Gong content may change after initial download
- optional value-example opt-in for unmapped fields, only with explicit redaction rules
