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
| MCP binary/runtime policy | `cmd/gongmcp/main.go`, `internal/mcphttp/server.go` | flag/env wiring, stdio vs HTTP, auth, Origin validation, access logging, governance mode |
| Business profiles | `internal/profile/`, `internal/cli/profile.go` | YAML schema, validation, staged import, diff, activation |
| AI governance exclusions | `internal/governance/`, `internal/cli/governance.go` | audit, suppressed calls, filtered DB export |
| Support bundle | `internal/cli/support.go` | metadata-only support artifact contents |
| Claude Desktop installer | `scripts/install-claude-stdio-mcp.sh`, `internal/cli/install_claude_stdio_mcp_script_test.go` | generated Docker stdio MCP config, preset catalog lookup, path containment |
| Deployment examples | `compose.yaml`, `deploy/`, `scripts/` | Docker, lab auth, HTTP MCP smoke tests, Terraform starters |
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
- `cmd/gongmcp/main.go` owns flag/env parsing, SQLite open, governance setup,
  and process lifecycle. HTTP auth, Origin/CORS checks, `/healthz`, and access
  logging live in `internal/mcphttp`.
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
- HTTP `/mcp` accepts POST only. CORS preflight is allowed through OPTIONS when
  the requested method is POST and requested headers are from the narrow allow
  list in `internal/mcphttp/server.go`.
- MCP request/response frames are capped at 1 MiB. Tool results are capped just
  below that after MCP framing. Row-returning tools use `internal/mcp.LimitPolicy`;
  update schema generation, handler enforcement, `cmd/gongmcp` flags/env, and
  docs together when changing a cap family.
- MCP `limit.maximum` in `tools/list` is a server maximum, not always the
  omitted-request row count. For example, default search tools advertise a
  maximum of 100 rows, but omitted `limit` uses the lower default request limit
  in `internal/mcp/limits.go` unless the configured maximum is lower.
- The running server's `tools/list` reflects `gongmcp --max-*` flags and
  `GONGMCP_MAX_*` env vars. `gongctl mcp tool-info NAME` is an offline catalog
  inspection path; it can reflect env vars, but it cannot see flags passed to a
  separate running server process.
- High-volume MCP tools keep the legacy array/object output shape below the
  effective limit. When they return exactly the effective limit, they may switch
  to a cap-feedback envelope with `results`, `returned`, `limit`, `capped: true`,
  `tool`, and `suggested_refinements`. Client code should tolerate both shapes
  for the capped row tools listed in `docs/mcp-data-exposure.md`.
- Quote search requires cached transcript segments. `transcript_status=missing`
  is invalid for `search_transcript_quotes_with_attribution`; use
  `missing_transcripts` or backlog tools for transcript-coverage work.
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
- `scripts/install-claude-stdio-mcp.sh` validates preset names against the
  `gongmcp --list-tool-presets` JSON catalog. By default it inspects the
  selected Docker image with `docker run --rm --network none --entrypoint
  /usr/local/bin/gongmcp IMAGE --list-tool-presets`; tests use an explicit
  `--preset-catalog-bin` helper instead. It canonicalizes `--db` and
  `--data-dir`, rejects `..` and symlink escapes outside the read-only mount,
  and emits `GONGMCP_TOOL_PRESET` unless `--compat-expanded-allowlist` is set.

## Practical Development Loops

Inspect the MCP catalog after changing tools:

```bash
go run ./cmd/gongctl mcp tools
go run ./cmd/gongctl mcp tool-info search_transcript_segments
GONGMCP_MAX_SEARCH_RESULTS=250 go run ./cmd/gongctl mcp tool-info search_transcript_segments
go run ./cmd/gongmcp --list-tool-presets
go test -count=1 ./internal/mcp ./cmd/gongmcp
go test -count=1 ./internal/mcp -run TestToolCatalogInvariants
```

Smoke a configured cap without a host app:

```bash
go run ./cmd/gongmcp --db /path/to/cache.db --tool-preset analyst --max-search-results 25
```

Then inspect the running server with an MCP `tools/list` request. Use
`gongctl mcp tool-info` only for the static catalog/env view, not as proof of a
live server's flag state.

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
go test -count=1 ./cmd/gongmcp ./internal/mcphttp -run 'TestRunStdioFlagOverridesHTTPAddrEnv|TestResolveHTTPConfig|TestHTTPHandler'
```

Check the Claude Desktop stdio installer without touching a real Claude config:

```bash
go test -count=1 ./internal/cli -run 'TestInstallClaudeStdioMCPScript'
scripts/install-claude-stdio-mcp.sh --db "$HOME/gongctl-data/gong.db" --print
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

## Common Failure Modes

Use this table before debugging from first principles.

| Symptom | Likely Cause | First Check |
| --- | --- | --- |
| `gongmcp --db ...` exits with `db file not found` | MCP is read-only and will not create caches | Run `gongctl sync status --db PATH` or another writable sync/cache command first |
| MCP startup fails with an old schema error | SQLite cache was created by an older binary | Run a writable `gongctl sync status --db PATH` or the relevant sync/profile command to migrate, then retry MCP |
| HTTP MCP refuses to start | HTTP requires explicit preset/allowlist and bearer setup unless local no-auth dev is enabled | Re-run with `--tool-preset business-pilot`; for non-local binds also set `--allow-open-network` and `--allowed-origins` |
| HTTP requests get `401` | Missing/invalid bearer token or weak token rejected by `internal/mcphttp` | Use a strong token or `--bearer-token-file` with `0600` permissions |
| HTTP requests get `403 invalid origin` | Origin is absent from the configured allowlist for a non-local bind | Set `--allowed-origins` or `GONGMCP_ALLOWED_ORIGINS` to exact `scheme://host[:port]` values |
| HTTP GET `/mcp` returns `405` | MCP endpoint is POST-only; streaming GET is not implemented | Use POST JSON-RPC or `/healthz` for health checks |
| Claude Desktop installer prints `invalid preset catalog JSON` | Selected Docker image or explicit catalog binary did not return the expected preset schema | Check `gongmcp --list-tool-presets` from that image/binary |
| Claude Desktop generated config points at `/data/...` unexpectedly | Installer maps the host DB path relative to the read-only Docker mount | Verify `--db` is inside `--data-dir`; symlinks outside the mount are rejected |
| A profile appears valid but CI should fail on semantic invalidity | `profile validate` returns JSON and only exits nonzero for command/read/parse/runtime failures | Inspect the JSON `valid` field or use `profile import` for write-time rejection |

## Where To Make Likely Changes

| Change | Start Here | Tests To Run First |
| --- | --- | --- |
| Add or rename an MCP tool | `internal/mcp/server.go`, `internal/mcp/catalog.go`, `internal/mcp/server_test.go` | `go test -count=1 ./internal/mcp ./cmd/gongmcp` |
| Change HTTP auth/CORS/health/access logs | `internal/mcphttp/server.go` | `go test -count=1 ./internal/mcphttp` |
| Change `gongmcp` flags/env wiring | `cmd/gongmcp/main.go` | `go test -count=1 ./cmd/gongmcp ./internal/mcphttp` |
| Change Claude Desktop Docker config generation | `scripts/install-claude-stdio-mcp.sh` | `go test -count=1 ./internal/cli -run 'TestInstallClaudeStdioMCPScript'` |
| Add a sync command or cached entity | `internal/cli/sync.go`, `internal/syncsvc/`, `internal/store/sqlite/` | `go test -count=1 ./internal/cli ./internal/syncsvc ./internal/store/sqlite` |
| Change transcript search/sync | `internal/transcripts/`, `internal/store/sqlite/store_transcripts.go`, `internal/mcp/server.go` | `go test -count=1 ./internal/transcripts ./internal/store/sqlite ./internal/mcp` |
| Change profile validation/import semantics | `internal/profile/`, `internal/cli/profile.go`, `internal/store/sqlite/profile.go` | `go test -count=1 ./internal/profile ./internal/cli ./internal/store/sqlite` |
| Change restricted/sensitive export gates | `internal/cli/root.go`, command implementation in `internal/cli/` | `go test -count=1 ./internal/cli -run 'TestRestricted|TestReadOnlyCommands'` |

## Documentation Rule

Do not describe behavior from memory. For CLI behavior, read `internal/cli` or
`cmd/gongmcp`. For HTTP transport behavior, read `internal/mcphttp`. For MCP
output behavior, read `internal/mcp/server.go` and the store method it calls.
For cache behavior, read the migration and store method. Then include a
practical command or prompt that a new operator can actually run.
