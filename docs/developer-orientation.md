# Developer Orientation

This page is the fast map for a new developer or agent. It is source-grounded:
when behavior matters, check the referenced package before changing docs.

## What The System Does

`gongctl` has two binaries:

- `cmd/gongctl`: writable operator CLI. It authenticates to Gong, syncs data,
  writes SQLite/transcript/profile/governance/support artifacts, and owns local
  operational commands.
- `cmd/gongmcp`: read-only MCP server. It opens an existing SQLite cache or
  Postgres-backed business analysis store read-only and serves stdio by default
  or HTTP `/mcp` when explicitly enabled.

The project is a local-first Gong evidence workbench. It does not host tenant
data for users. The CLI writes a customer-controlled cache; MCP reads that
cache and exposes bounded, redacted tools to an AI host.

The data path is:

```text
Gong API -> gongctl sync/profile/governance -> customer cache/data root -> gongmcp -> MCP host/model
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
| MCP stable facade | `internal/mcp/facade.go`, `internal/mcp/facade_test.go` | six `gong_*` facade tools, operation registry, routed internal tools, dispatch schemas |
| MCP server | `internal/mcp/server.go`, `internal/mcp/business_analysis_tools.go` | tool definitions, handlers, schemas, redaction, frame limits, strict argument decoding |
| MCP binary/runtime policy | `cmd/gongmcp/main.go` | stdio vs HTTP, auth, Origin validation, preset/allowlist resolution, governance mode |
| Business profiles | `internal/profile/`, `internal/cli/profile.go` | YAML schema, validation, staged import, diff, activation |
| AI governance exclusions | `internal/governance/`, `internal/cli/governance.go` | audit, suppressed calls, filtered DB export |
| Support bundle | `internal/cli/support.go` | metadata-only support artifact contents |
| Deployment examples | `compose.yaml`, `deploy/`, `scripts/` | Docker, lab auth, smoke tests, Terraform starters |
| Container contents | `Dockerfile` | current image builds only `gongctl` and `gongmcp` |
| CI/release gates | `.github/workflows/ci.yml`, `.github/workflows/publish-images.yml`, `Makefile` | test, vet, secret scan, SBOM/checksum, Docker build, Postgres smoke gates |

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

## Main Run Paths

Use these paths to orient before changing code or docs:

| Workflow | Entry point | Cache/store | Source to read | Fast check |
| --- | --- | --- | --- | --- |
| Local CLI sync to SQLite | `gongctl sync calls/users/transcripts/... --db PATH` | SQLite file | `internal/cli/sync.go`, `internal/syncsvc/`, `internal/transcripts/`, `internal/store/sqlite/` | `go test -count=1 ./internal/cli ./internal/syncsvc ./internal/transcripts ./internal/store/sqlite` |
| Repeatable SQLite refresh plan | `gongctl sync run --config PATH` | SQLite file only | `internal/cli/sync.go`, `docs/examples/sync-run.example.yaml`, `docs/scheduling.md` | `go run ./cmd/gongctl sync run --config docs/examples/sync-run.example.yaml --dry-run` after replacing placeholders |
| Local read-only MCP over SQLite | `gongmcp --db PATH [--tool-preset NAME]` | existing SQLite file | `cmd/gongmcp/main.go`, `internal/mcp/`, `internal/store/sqlite/` | `go test -count=1 ./cmd/gongmcp ./internal/mcp` |
| Shared Postgres MCP reader | `GONG_DATABASE_URL=... gongmcp --tool-preset NAME` | Postgres reader URL | `cmd/gongmcp/main.go`, `internal/store/postgres/`, `docs/postgres-parity.md` | `scripts/postgres-smoke.sh` when Postgres test env is available |
| Postgres read-model maintenance | `gongctl sync read-model [--rebuild]` | writable Postgres URL | `internal/cli/sync.go`, `internal/store/postgres/read_model.go` | `go test -count=1 ./internal/store/postgres ./internal/cli` |
| Profile lifecycle mapping | `gongctl profile ...` | SQLite or Postgres profile state depending command | `internal/cli/profile.go`, `internal/profile/`, `internal/store/sqlite/profile.go`, `internal/store/postgres/profile.go` | `go test -count=1 ./internal/profile ./internal/cli` |
| AI governance filtering | `gongctl governance ...`, `gongmcp --ai-governance-config PATH` | SQLite filtered DB or Postgres policy state | `internal/cli/governance.go`, `internal/governance/`, `cmd/gongmcp/main.go` | `go test -count=1 ./internal/governance ./internal/cli ./cmd/gongmcp` |
| Business-workbench facade | `gongmcp --tool-preset business-workbench` | SQLite or reviewed Postgres slice | `internal/mcp/facade.go`, `internal/mcp/catalog.go`, `internal/mcp/business_*`, `cmd/gongmcp/main.go` | `go test -count=1 ./internal/mcp ./cmd/gongmcp -run 'Facade|BusinessWorkbench|ToolAllowlist'` |
| Docker packaging | `make docker-build`, `make docker-build-mcp`, `scripts/docker-smoke.sh` | external mounted data dir | `Dockerfile`, `compose.yaml`, `scripts/docker-smoke.sh`, `.dockerignore` | `docker compose config --quiet` and `make docker-build` |

## First Productive Pass

For a new developer or agent, use this order before editing:

```bash
go run ./cmd/gongctl --help
go run ./cmd/gongmcp --list-tool-presets
go run ./cmd/gongctl mcp tools
go test -count=1 ./internal/mcp ./cmd/gongmcp
```

Then read the files for the surface you intend to change:

- CLI behavior: start in `internal/cli/root.go`, then the command-specific
  file under `internal/cli/`.
- Gong API access: start in `internal/gong/client.go`; typed wrappers and
  sync services should call through this boundary.
- SQLite cache behavior: start in `internal/store/sqlite/store.go` and the
  feature-specific store file.
- Postgres shared-deployment behavior: start in `internal/store/postgres/` and
  confirm parity against `docs/postgres-parity.md` before claiming SQLite
  behavior exists on Postgres.
- Scheduled refresh behavior: start in `internal/cli/sync.go`. The
  `sync run --config` implementation currently opens SQLite with `openSQLiteStore`;
  Postgres has separate sync/read-model paths and does not use that YAML runner.
- MCP tool exposure: start in `internal/mcp/catalog.go`; for the stable
  business-user facade, also read `internal/mcp/facade.go`.
- Runtime MCP startup policy: start in `cmd/gongmcp/main.go`, not the catalog.

## Behavior The Code Enforces

These are easy to miss if you only read the high-level docs.

- `gongctl` and `gongmcp` are separate trust boundaries. `gongctl` may write and
  use Gong credentials; `gongmcp` only reads an existing cache/store.
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
  `business-workbench`, `business-pilot`, `operator-smoke`, `analyst-core`,
  `analyst-business-core`, `analyst`, `governance-search`,
  `redacted-all-readonly`, `broad-public-redacted`, and `all-readonly`; run
  `gongmcp --list-tool-presets` for aliases and expanded tool lists.
- `business-workbench` is the recommended client MCP preset. It exposes only
  six stable facade tool names:
  `gong_status`, `gong_discover_capabilities`, `gong_query`, `gong_analyze`,
  `gong_get_evidence`, and `gong_explain_limitations`. Its aliases are
  `analyst-facade` and `facade-analyst`.
- Facade operations are registered in `FacadeOperations()` and dispatched
  through the six facade tools. `gong_discover_capabilities` reports the
  operation registry, while each dispatch tool advertises its allowed
  `operation` enum in `tools/list`.
- Some facade operations route to hidden internal tools. `cmd/gongmcp` passes
  those hidden routed tools into the server and Postgres reader-grant checks
  through `ExpandToolPresetFacadeRoutedTools`. Keep visible facade tools,
  routed internals, and grant validation in sync when adding operations.
- HTTP MCP defaults to bearer auth. `auth-mode=none` is only allowed with
  `--dev-allow-no-auth-localhost` on a local bind.
- Non-local HTTP binds require `--allow-open-network` and an Origin allowlist.
  TLS, OAuth/SSO, DNS, WAF, and multi-user policy are expected at the customer
  proxy/gateway, not inside `gongmcp`.
- HTTP exposes `/healthz` separately from `/mcp`; use `/healthz` for infra
  checks instead of probing MCP tool calls.
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
- `sync run --config` resolves relative paths from the config file location and
  runs against SQLite through `openSQLiteStore`. The YAML runner does not
  switch to Postgres when `GONG_DATABASE_URL` is set. Postgres uses separate
  commands such as `sync read-model`, scoped-reader grant helpers, and the
  Postgres smoke scripts.
- `sync run --config` YAML cannot self-authorize sensitive steps. Transcript
  sync, business/all call sync, party capture, and highlight capture still
  require the runtime sensitive-export gate.
- `governance export-filtered-db` creates a physical filtered SQLite copy for
  MCP use. Raw-DB governance mode exists, but the preferred path for blocklists
  is a filtered DB regenerated after each sync or governance-config change.
- The support bundle opens the configured cache/store read-only and writes
  metadata-only JSON files.
  It intentionally excludes raw Gong payloads, transcript text, CRM values,
  direct customer-content identifiers, secrets, and local paths.

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
go test -count=1 ./cmd/gongmcp -run 'TestResolveHTTPConfig|TestRunStdioFlagOverridesHTTPAddrEnv'
```

Check the stable business-user facade after adding or changing a facade
operation:

```bash
go test -count=1 ./internal/mcp -run 'TestFacadeOperationsRegistryShape|TestFacadeDispatchSchemasAdvertiseRegisteredOperations|TestBusinessWorkbenchPresetExposesOnlyFacadeTools'
go test -count=1 ./cmd/gongmcp -run 'TestPostgresToolAllowlistAcceptsAnalystFacadePreset|TestPostgresToolAllowlistAcceptsBusinessWorkbenchPreset'
go run ./cmd/gongmcp --list-tool-presets
```

Check scheduled refresh config changes:

```bash
cp docs/examples/sync-run.example.yaml /tmp/gongctl-sync-run.yaml
# replace REPLACE_WITH_* placeholders in /tmp/gongctl-sync-run.yaml first
go run ./cmd/gongctl sync run --config /tmp/gongctl-sync-run.yaml --dry-run
go test -count=1 ./internal/cli -run 'TestSyncRun'
```

Run broad local checks before publishing docs or code:

```bash
go test -count=1 ./...
go vet ./...
git diff --check
make secret-scan
```

Documentation surfaces in this repo are Markdown files under the root,
`docs/`, `deploy/**/README.md`, `internal/mcp/README.md`, and
`testdata/fixtures/README.md`; YAML examples under `docs/examples/`; Docker,
Compose, Terraform, GitHub Actions, and shell scripts that docs link to; and
generated release metadata from `make sbom` / `make checksums`. There is no
docs-site config in this checkout at the time of writing. If a docs site is
added later, include its build/link checker in this section.

When changing docs, validate at least the touched links and paths:

```bash
git diff --check
rg -n 'old-doc-name|deleted-file-name' README.md docs CHANGELOG.md
```

For broad doc moves, run a Markdown link/path scan or an equivalent checker.
This repo currently does not ship a dedicated Markdown link checker, so use a
small local script or manual verification for changed links, headings, images,
and examples. Do not commit docs that link to untracked files.

When adding a tool, update all of these or explain why not:

- `internal/mcp/server.go` tool catalog and handler
- `internal/mcp/business_analysis_tools.go` when the tool is part of the
  business-analysis family
- `internal/mcp/catalog.go` presets and governance-compatible allowlist, if the
  tool belongs in a preset or raw-DB governance mode
- `internal/mcp/catalog_test.go` and `internal/mcp/server_test.go`
- `docs/mcp-data-exposure.md`
- `docs/architecture.md`
- `internal/mcp/README.md`
- output-contract/redaction tests

When adding a facade operation, update all of these or explain why not:

- `internal/mcp/facade.go` `FacadeOperations()` metadata, schema, examples,
  facade tool, routed tool, exposure level, and allowed presets
- the routed handler in `internal/mcp/server.go`,
  `internal/mcp/business_analysis_tools.go`, or the relevant feature file
- `internal/mcp/catalog.go` if a new hidden routed tool needs preset/grant
  coverage
- `internal/mcp/facade_test.go`, especially registry shape and dispatch schema
  coverage
- `internal/mcp/catalog_test.go` and `cmd/gongmcp/main_test.go` when visible
  presets, hidden routed tools, or Postgres grant behavior changes
- `docs/mcp-data-exposure.md`, `docs/pilot-sponsor-and-operator-guide.md`, and
  `docs/business-workbench-host-instructions.md` if business-user behavior or
  exposure changes

When changing Postgres MCP support, update all of these or explain why not:

- `internal/store/postgres/` store/read-model support
- `cmd/gongmcp/main.go` Postgres tool selection and startup gates
- `internal/mcp/catalog.go` presets, if the surface changes
- `docs/postgres-parity.md` and `docs/postgres-question-parity.md`
- the relevant Postgres smoke script under `scripts/`

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

Use the exact error and the owning source file before changing broad docs:

- `--db is required` from `gongmcp`: no SQLite DB was provided and no Postgres
  URL was found in `GONG_DATABASE_URL` or `DATABASE_URL`. Use `--db PATH` for
  SQLite or set the Postgres URL for the shared-deployment path.
- `db file not found`: `gongmcp` refuses to create or migrate SQLite caches.
  Run the writable `gongctl sync ... --db PATH` path first.
- HTTP startup rejects missing presets: HTTP MCP requires `--tool-preset` or
  `--tool-allowlist`. Stdio can serve the full read-only catalog without an
  explicit preset.
- HTTP no-auth fails on non-local or normal runs: `auth-mode=none` is a
  localhost-only development path and requires `--dev-allow-no-auth-localhost`.
- Non-local HTTP bind fails: add `--allow-open-network` and a concrete
  `--allowed-origins` list, or keep the bind local behind a proxy.
- `business-workbench` shows six visible tools but a facade operation fails:
  check the hidden routed allowlist with `ExpandToolPresetFacadeRoutedTools`
  and the operation registry with `gong_discover_capabilities`.
- A host hides or rejects a supported facade operation: inspect the
  dispatching facade tool schema in `tools/list`; stale `operation.enum`
  entries are caught by
  `TestFacadeDispatchSchemasAdvertiseRegisteredOperations`.
- Postgres MCP rejects a tool that works on SQLite: check
  `docs/postgres-parity.md`, `cmd/gongmcp/main.go`, and the Postgres scoped
  reader grants before assuming parity.
- `sync run --config` rejects an example config: replace all `REPLACE_WITH_*`
  placeholders, confirm required fields for each action in
  `normalizeSyncRunStep`, and remember this runner is SQLite-only.
- `profile validate` exits successfully but the profile is not valid: inspect
  the JSON `valid` field. The import command rejects invalid profiles; validate
  is a report command unless file parsing or runtime execution fails.

## Documentation Rule

Do not describe behavior from memory. For CLI behavior, read `internal/cli` or
`cmd/gongmcp`. For MCP output behavior, read `internal/mcp/server.go` and the
store method it calls. For cache behavior, read the migration and store method.
Then include a practical command or prompt that a new operator can actually run.
