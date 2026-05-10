# Configuration Surfaces

This note tracks which `gongctl` settings are already YAML-based and which
flag/env surfaces should probably move to optional YAML next.

## Already YAML

| Surface | File or command | Purpose | Status |
| --- | --- | --- | --- |
| Sync run plan | `gongctl sync run --config PATH` | Repeatable call/user/transcript/schema/settings/scorecard-activity refresh steps | Implemented |
| Business profile | `gongctl profile discover --out PATH`, then `profile validate/import --profile PATH` | Tenant CRM object, field, lifecycle, and methodology mappings | Implemented |
| AI governance exclusions | `gongctl governance audit --config PATH`, SQLite `export-filtered-db --config PATH`, and Postgres `refresh-serving-db --config PATH` | Customer/account names and aliases that should be excluded before MCP/AI use | Implemented |
| Docker Compose | `compose.yaml` | Local container wiring for CLI and MCP services | Implemented |
| Release/CI automation | `.goreleaser.yml`, `.github/workflows/*.yml` | Build, release, and publish automation | Implemented |

## Best Next YAML Candidate: MCP Runtime Config

Today `gongmcp` runtime policy is spread across flags, env vars, Docker args,
Claude Desktop JSON, and wrapper scripts:

- `--db`
- `--http`
- `--stdio`
- `--auth-mode`
- `--bearer-token-file`
- `--bearer-token-previous-file`
- `--allow-open-network`
- `--tool-preset` / `GONGMCP_TOOL_PRESET`
- `--tool-allowlist` / `GONGMCP_TOOL_ALLOWLIST`
- `--ai-governance-config`
- `--allow-unmatched-ai-governance`

This is the strongest YAML candidate because it is easy to make mistakes with a
long comma-separated tool list or HTTP flag set. A future config could look like:

```yaml
version: 1

db: /data/gong-mcp-governed.db

transport:
  type: stdio
  # type: http
  # addr: 127.0.0.1:8080
  # auth_mode: bearer
  # bearer_token_file: /run/secrets/gongmcp_token
  # bearer_token_previous_file: /run/secrets/gongmcp_token_previous
  # allow_open_network: false

tools:
  preset: business-pilot
  # preset: all-readonly
  # allowlist:
  #   - get_sync_status
  #   - summarize_calls_by_lifecycle
  #   - search_transcript_segments

governance:
  config: ""
  allow_unmatched: false
```

Recommended contract:

- `gongmcp --config PATH` loads this file.
- Flags and env vars can override YAML for local debugging and container
  platform integration.
- Built-in presets are the fast path for common deployments:
  `business-pilot`, `operator-smoke`, `analyst-core`,
  `analyst-business-core`, `analyst`, `governance-search`, and
  `all-readonly`.
- HTTP must still have an explicit tool policy, but that policy can be a named
  preset such as `business-pilot` or `all-readonly`, or a custom allowlist.
- Remote add-by-URL clients such as Claude's UI require an HTTPS `/mcp`
  endpoint. The YAML should describe the internal `gongmcp` HTTP listener, while
  DNS, TLS, and external auth remain proxy/gateway configuration.
- Raw-DB AI governance still validates that only governance-compatible tools are
  exposed.
- Bearer token values should not be stored in YAML; store only
  `bearer_token_file`/`bearer_token_previous_file` or use the deployment
  secret manager.

## Strong Candidate: Operator Workspace Config

Many docs currently repeat the same paths:

- data root
- raw operator DB
- governed MCP DB
- transcript output directory
- profile YAML path
- governance YAML path
- backup directory
- Docker image tags

An optional operator config would make local and customer-managed setups easier:

```yaml
version: 1

data_root: /srv/gongctl
paths:
  raw_db: cache/gong.db
  mcp_db: cache/gong-mcp-governed.db
  transcripts: transcripts
  profiles: profiles
  governance: private/ai-governance.yaml
  backups: backups

images:
  cli: ghcr.io/fyne-coder/gongcli_mcp/gongctl:v0.4.2
  mcp: ghcr.io/fyne-coder/gongcli_mcp/gongmcp:v0.4.2
```

Use published image tags only after the corresponding tag workflow has
completed and GHCR manifests are inspectable. For source checkouts ahead of a
published tag, use local image names such as `gongctl:local` and
`gongctl:mcp-local`.

This should not contain Gong credentials. Credentials should remain environment
variables, `.env` files outside Git, Docker/Kubernetes secrets, or a company
secret manager.

## Implemented: Retention Policy YAML

Scheduled cache purge can use reviewed retention policy YAML:

- `cache purge --older-than DATE`
- `cache purge --config PATH`
- `--dry-run`
- `--confirm`
- `--db PATH` for SQLite, or `GONG_DATABASE_URL` / `DATABASE_URL` for Postgres

The implemented policy shape is intentionally narrow:

```yaml
version: 1
older_than: 2026-04-01
approval:
  reference: CHANGE-RETENTION-123
  approved_by: revops-retention-reviewer
  approved_at: 2024-01-01
  data_owner: revenue-operations
  backup_reference: backup-20240101-approved
  legal_hold_reviewed: true
```

Confirmed deletion still requires the runtime `--confirm` flag and, for
Postgres, a writable operator URL. The YAML file does not install a scheduler,
self-authorize destructive deletes, or move WAL, replica, snapshot, transcript
file, profile, or backup retention into `gongctl`.

## Possible Candidate: Analysis Presets

Repeated analysis questions could become YAML presets:

```yaml
version: 1
name: renewal-risk-review
db: /srv/gongctl/cache/gong-mcp-governed.db
queries:
  - tool: summarize_calls_by_lifecycle
    args:
      lifecycle: renewal
      limit: 25
  - tool: search_transcript_segments
    args:
      query: budget risk
      limit: 10
```

This is lower priority than MCP runtime config because the current CLI/MCP tools
already work. It becomes useful when teams want repeatable review packs.

## Do Not Move These Into YAML

- Gong API secrets: keep in environment variables, ignored `.env` files, or a
  secret manager.
- MCP bearer token values: use secret files or a secret manager; YAML can point
  at a token file path.
- Sensitive-export approval: keep `--allow-sensitive-export` or
  `GONGCTL_ALLOW_SENSITIVE_EXPORT=1` as a runtime approval gate. The sync-run
  YAML intentionally cannot self-authorize sensitive export steps.
- Real restricted customer names in tracked examples: keep real governance YAML
  outside Git.

## Priority

1. Add `gongmcp --config PATH` for MCP runtime config.
2. Add an optional operator workspace config to reduce repeated path/image args.
3. Retention policy YAML is implemented for scheduled `cache purge --config`
   jobs; next retention work should focus on customer scheduler/runbook
   integration rather than expanding config shape by default.
4. Add analysis preset YAML after the operational config surface is stable.
