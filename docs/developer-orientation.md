# Developer Orientation

This page is the fast map for a new developer or agent. It is source-grounded:
when behavior matters, check the referenced package before changing docs.

## Mental Model

`gongctl` has two binaries:

- `cmd/gongctl`: writable operator CLI. It authenticates to Gong, syncs data,
  writes SQLite/transcript/profile/governance/support artifacts, and owns local
  operational commands.
- `cmd/gongmcp`: read-only MCP server. It opens an existing SQLite cache
  read-only and serves stdio by default or HTTP `/mcp` when explicitly enabled.

The data path is:

```text
Gong API -> gongctl sync/profile/governance -> customer SQLite/data root -> gongmcp -> MCP host/model
```

Do not add a feature that lets MCP call Gong directly, write profiles, run SQL,
or return raw transcript/call payloads unless the security model is deliberately
changed.

## Source Map

| Area | Source | What to read first |
| --- | --- | --- |
| CLI dispatch and global flags | `internal/cli/root.go` | command names, restricted mode, sensitive-export gate |
| Gong API client | `internal/gong/` | typed calls, raw fallback, pagination, retry/rate-limit client behavior |
| Sync orchestration | `internal/syncsvc/`, `internal/transcripts/` | how Gong responses become cache rows and transcript files |
| SQLite cache | `internal/store/sqlite/` | schema, migrations, read/write store APIs, profile cache, governance export |
| MCP catalog and presets | `internal/mcp/catalog.go` | built-in presets, preset aliases, governance-compatible tools |
| MCP server | `internal/mcp/server.go`, `internal/mcp/business_analysis_tools.go` | tool definitions, handlers, schemas, redaction, frame limits, strict argument decoding |
| MCP binary/runtime policy | `cmd/gongmcp/main.go` | stdio vs HTTP, auth, Origin validation, preset/allowlist resolution, governance mode |
| Business profiles | `internal/profile/`, `internal/cli/profile.go` | YAML schema, validation, staged import, diff, activation |
| AI governance exclusions | `internal/governance/`, `internal/cli/governance.go` | audit, suppressed calls, filtered DB export |
| Support bundle | `internal/cli/support.go` | metadata-only support artifact contents |
| Deployment examples | `compose.yaml`, `deploy/`, `scripts/` | Docker, lab auth, smoke tests, Terraform starters |
| Container contents | `Dockerfile` | current image builds only `gongctl` and `gongmcp` |

Useful entrypoint commands that do not need Gong credentials:

```bash
go run ./cmd/gongctl --help
go run ./cmd/gongctl mcp tools
go run ./cmd/gongmcp --list-tool-presets
go run ./cmd/gongctl profile schema
go test -count=1 ./internal/mcp ./cmd/gongmcp
```

If a doc or example names another binary, verify that it exists under `cmd/`
and is copied by `Dockerfile` before trusting the example.

## Behavior The Code Enforces

These are easy to miss if you only read the high-level docs.

- `gongctl` and `gongmcp` are separate trust boundaries. `gongctl` may write and
  use Gong credentials; `gongmcp` only reads an existing SQLite DB.
- `gongmcp --db PATH` checks that the DB file already exists before opening it.
  Schema migration is not a read-only MCP responsibility; use writable
  `gongctl` sync/profile/cache commands to create or migrate the cache first.
- Several CLI inspection commands also open SQLite read-only:
  `sync status`, `search transcripts`, `search calls`, `calls show --json`,
  `analyze calls`, `analyze coverage`, `analyze transcript-backlog`,
  `analyze crm-schema`, `analyze settings`, `analyze scorecards`, and
  `analyze scorecard`. Tests assert these paths do not create a missing DB or
  mutate an existing DB.
- Stdio MCP serves the full read-only tool catalog when no preset or allowlist
  is set. HTTP MCP is stricter: `cmd/gongmcp` refuses HTTP unless a tool preset
  or allowlist is explicit.
- Built-in MCP presets live in `internal/mcp/catalog.go`; `cmd/gongmcp/main.go`
  only resolves flag/env precedence and transport policy. Current presets are
  `business-pilot`, `operator-smoke`, `analyst`, `governance-search`, and
  `all-readonly`; run `gongmcp --list-tool-presets` for the expanded lists.
- HTTP MCP defaults to bearer auth. `auth-mode=none` is only allowed with
  `--dev-allow-no-auth-localhost` on a local bind.
- Non-local HTTP binds require `--allow-open-network` and an Origin allowlist.
  TLS, OAuth/SSO, DNS, WAF, and multi-user policy are expected at the customer
  proxy/gateway, not inside `gongmcp`.
- HTTP exposes `/healthz` separately from `/mcp`; use `/healthz` for infra
  checks instead of probing MCP tool calls.
- MCP request/response frames are capped at 1 MiB. Tool results are capped just
  below that after MCP framing.
- MCP argument decoding is strict for unknown fields, but strips the reserved
  MCP `_meta` field before strict decoding so ChatGPT-style tool calls work.
- Profile YAML uses a closed schema and closed rule operators. Imports are
  transactional and can be staged with `--activate=false`; `profile history`,
  `profile diff`, and `profile activate` are the rollback/review path.
- `profile validate` currently prints a JSON validation report and returns a
  command error only for command/read/parse/runtime failures. `profile import`
  is the command that rejects `valid:false` profiles before writing. Do not use
  `profile validate` as a CI gate for semantic validity unless you also inspect
  the JSON `valid` field.
- `sync run --config` resolves relative paths from the config file location,
  but YAML cannot self-authorize sensitive steps. Transcript sync, business/all
  call sync, and party capture still require the runtime sensitive-export gate.
- `governance export-filtered-db` creates a physical filtered SQLite copy for
  MCP use. Raw-DB governance mode exists, but the preferred path for blocklists
  is a filtered DB regenerated after each sync or governance-config change.
- The support bundle opens SQLite read-only and writes metadata-only JSON files.
  It intentionally excludes raw Gong payloads, transcript text, CRM values,
  direct customer-content identifiers, secrets, and local paths.

## Practical Development Loops

Inspect the MCP catalog after changing tools:

```bash
go run ./cmd/gongctl mcp tools
go run ./cmd/gongctl mcp tool-info search_transcript_segments
go run ./cmd/gongmcp --list-tool-presets
go test -count=1 ./internal/mcp ./cmd/gongmcp
go test -count=1 ./internal/mcp -run TestToolCatalogInvariants
```

Check profile behavior without live Gong access:

```bash
go run ./cmd/gongctl profile schema
go test -count=1 ./internal/profile ./internal/store/sqlite ./internal/cli
```

Check read-only cache behavior after changing CLI open paths:

```bash
go test -count=1 ./internal/cli -run 'TestReadOnlyCommands(MissingDBDoNotCreateDatabase|ExistingDBDoNotMutateDatabase)'
```

Check remote/HTTP MCP policy without a host app:

```bash
go run ./cmd/gongmcp --list-tool-presets
go test -count=1 ./cmd/gongmcp -run 'TestResolveHTTPConfig|TestRunStdioFlagOverridesHTTPAddrEnv'
```

Run broad local checks before publishing docs or code:

```bash
go test -count=1 ./...
go vet ./...
git diff --check
make secret-scan
```

When adding a tool, update all of these or explain why not:

- `internal/mcp/server.go` tool catalog and handler
- `internal/mcp/business_analysis_tools.go` when the tool is part of the
  business-analysis family
- `internal/mcp/catalog.go` presets and governance-compatible allowlist, if the
  tool belongs in a preset or raw-DB governance mode
- `internal/mcp/catalog_test.go` and `internal/mcp/server_test.go`
- `docs/mcp-data-exposure.md`
- `docs/architecture.md`
- `docs/mcp-phase.md`
- `internal/mcp/README.md`
- output-contract/redaction tests

When adding a sync surface, update all of these or explain why not:

- `internal/cli/sync.go`
- `internal/syncsvc/` or `internal/transcripts/`
- `internal/store/sqlite/`
- `docs/runbooks/operator-sync.md`
- `docs/quickstart.md` if it affects first-run setup
- restricted-mode tests if the command can cache sensitive payloads

When changing a read-only CLI command, update all of these or explain why not:

- the command implementation in `internal/cli/`
- `openSQLiteReadOnlyStore` usage in `internal/cli/sync.go` or direct
  `sqlite.OpenReadOnly` usage for cache/support/governance paths
- `TestReadOnlyCommandsMissingDBDoNotCreateDatabase`
- `TestReadOnlyCommandsExistingDBDoNotMutateDatabase`

## Documentation Rule

Do not describe behavior from memory. For CLI behavior, read `internal/cli` or
`cmd/gongmcp`. For MCP output behavior, read `internal/mcp/server.go` and the
store method it calls. For cache behavior, read the migration and store method.
Then include a practical command or prompt that a new operator can actually run.
