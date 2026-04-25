# Contributing

Thanks for helping improve `gongctl`.

## Development

Run checks before opening a pull request:

```bash
go test -count=1 ./...
go vet ./...
go build -o bin/gongctl ./cmd/gongctl
go build -o bin/gongmcp ./cmd/gongmcp
```

## Data Safety

Do not commit real Gong credentials, SQLite databases, transcripts, recordings, CRM exports, customer names, call IDs, object IDs, emails, phone numbers, or tenant profile YAML.

Use synthetic fixtures under `testdata/fixtures/` for tests. Keep live validation databases and transcript exports outside the repository.

## Pull Requests

Keep changes scoped and include tests for behavior changes. For MCP changes, describe whether the output is aggregate-only, redacted, or intentionally operator-facing.
