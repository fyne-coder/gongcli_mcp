# Configuration Surfaces

This note tracks which `gongctl` settings are already YAML-based and which
flag/env surfaces should probably move to optional YAML next.

## Already YAML

| Surface | File or command | Purpose | Status |
| --- | --- | --- | --- |
| Sync run plan | `gongctl sync run --config PATH` | Repeatable call/user/transcript/schema/settings refresh steps | Implemented |
| Business profile | `gongctl profile discover --out PATH`, then `profile validate/import --profile PATH` | Tenant CRM object, field, lifecycle, and methodology mappings | Implemented |
| AI governance exclusions | `gongctl governance audit --config PATH` and `export-filtered-db --config PATH` | Customer/account names and aliases that should be excluded before MCP/AI use | Implemented |
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
- `--allow-open-network`
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
  # allow_open_network: false

tools:
  mode: all
  # mode: allowlist
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
- `tools.mode: all` is allowed for stdio and for filtered DB deployments.
- HTTP must still have an explicit tool policy, but that policy can be
  `mode: all` or `mode: allowlist` in YAML.
- Remote add-by-URL clients such as Claude's UI require an HTTPS `/mcp`
  endpoint. The YAML should describe the internal `gongmcp` HTTP listener, while
  DNS, TLS, and external auth remain proxy/gateway configuration.
- Raw-DB AI governance still validates that only governance-compatible tools are
  exposed.
- Bearer token values should not be stored in YAML; store only
  `bearer_token_file` or use the deployment secret manager.

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
  cli: ghcr.io/fyne-coder/gongcli_mcp/gongctl:v0.2.0
  mcp: ghcr.io/fyne-coder/gongcli_mcp/gongmcp:v0.2.0
```

This should not contain Gong credentials. Credentials should remain environment
variables, `.env` files outside Git, Docker/Kubernetes secrets, or a company
secret manager.

## Strong Candidate: Retention And Cache Policy

Cache purge is currently flag-driven:

- `cache purge --older-than DATE`
- `--dry-run`
- `--confirm`

A YAML policy would help operators review and schedule retention decisions:

```yaml
version: 1

cache:
  db: /srv/gongctl/cache/gong.db
  retention:
    older_than: 2026-04-01
    require_backup: true
    dry_run_first: true
  preserve:
    transcripts_dir: true
    profiles: true
    sync_history: true
```

Confirmed deletion should still require a runtime confirmation flag or operator
approval. Do not let a checked-in YAML file self-authorize destructive deletes.

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
3. Add retention policy YAML only if scheduled cache purge becomes common.
4. Add analysis preset YAML after the operational config surface is stable.
