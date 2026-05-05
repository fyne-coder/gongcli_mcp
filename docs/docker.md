# Docker Deployment

`gongctl` can run as a local container for three current use cases:

- one-shot CLI sync, search, and analysis commands
- read-only stdio MCP over a mounted SQLite cache
- read-only HTTP MCP private pilots over a mounted SQLite cache
- first-slice shared Postgres deployments where sync and MCP containers use one
  Postgres database instead of a shared SQLite filesystem

HTTP mode is explicit via `gongmcp --http ...`; the default MCP path remains
stdio. SQLite remains the complete/default cache, while the Postgres
business-pilot slice lets separate sync and MCP containers share one database.
Keep credentials and customer data outside the image.

## Build

```bash
docker build -t gongctl:local .
```

Build the MCP-only target for business-user hosts that should not contain the
writable CLI binary:

```bash
docker build --target mcp -t gongctl:mcp-local .
```

Or use Compose:

```bash
GONGCTL_DATA_DIR="$HOME/gongctl-data" docker compose build
```

## Published Images

Release images are published to GHCR as two separate packages:

- `ghcr.io/fyne-coder/gongcli_mcp/gongctl` for operator CLI sync, search, and analysis jobs
- `ghcr.io/fyne-coder/gongcli_mcp/gongmcp` for read-only stdio MCP hosts and
  private HTTP MCP pilots

Use immutable version tags or digest-pinned references in company deployments:

```bash
docker pull ghcr.io/fyne-coder/gongcli_mcp/gongctl:v0.3.2
docker pull ghcr.io/fyne-coder/gongcli_mcp/gongmcp:v0.3.2
```

A version tag is available only after the matching Git tag triggers
`.github/workflows/publish-images.yml` and that workflow publishes the GHCR
manifests. A `VERSION` bump or source release branch alone does not make an
image tag pullable. Until the manifest exists, build `gongctl:local` and
`gongctl:mcp-local` from source.

The `gongctl` image includes both binaries, but business-user MCP hosts should
use the `gongmcp` package so the writable CLI is not present in that runtime.

## Data And Credentials

Create a host data directory outside the source repo:

```bash
mkdir -p "$HOME/gongctl-data"
```

Use environment variables or an ignored `.env` file for Gong credentials:

```bash
cp .env.example .env
```

Never bake `.env`, SQLite databases, transcript files, or JSONL exports into the image. The Docker build context excludes those files.

## CLI Examples

Run a safe local smoke:

```bash
docker run --rm gongctl:local --help
```

Run a command with credentials and a mounted data directory:

```bash
docker run --rm \
  --env-file .env \
  -v "$HOME/gongctl-data:/data" \
  gongctl:local \
  sync status --db /data/gong.db
```

Run a bounded sync:

```bash
docker run --rm \
  --env-file .env \
  -v "$HOME/gongctl-data:/data" \
  gongctl:local \
  sync calls --db /data/gong.db --from 2026-04-01 --to 2026-04-24 --preset business --max-pages 2
```

Run the repeatable real-data smoke used before tagging:

```bash
scripts/docker-smoke.sh
```

The smoke uses the full local image for operator CLI steps and the MCP-only
image for the no-network MCP step by default. It runs:

- `auth check`
- `sync calls --preset minimal --max-pages 1`
- `sync status`
- a `gongmcp` `tools/list` request against the same SQLite DB with `--network none` and a read-only `/data` mount

With Compose, prefer an explicit external data directory:

```bash
export GONGCTL_DATA_DIR="$HOME/gongctl-data"
docker compose run --rm gongctl sync status --db /data/gong.db
```

Compose intentionally fails if `GONGCTL_DATA_DIR` is unset so customer data is not written under the source checkout by accident.

## Postgres Shared Deployment

Use Postgres when the operator sync container and `gongmcp` container do not
share a filesystem. SQLite remains the default for local/single-host pilots.

Configuration contract:

- SQLite: pass `--db PATH`.
- Postgres: omit `--db` and set `GONG_DATABASE_URL`; `DATABASE_URL` is accepted
  as a fallback.
- Use a writer URL for `gongctl` sync jobs.
- Use a reader URL for `gongmcp`.
- The Compose example binds Postgres to `127.0.0.1` for local smoke use and
  uses explicit dev passwords by default. Company deployments should provide
  `GONGCTL_POSTGRES_PASSWORD` and `GONGMCP_READER_PASSWORD` from an approved
  secret manager and should not publish the database port directly.

The first Postgres vertical slice supports:

- `gongctl sync synthetic`
- `gongctl sync calls`
- `gongctl sync users`
- `gongctl sync transcripts`
- `gongctl sync status`
- `gongctl sync read-model` and `gongctl sync read-model --rebuild` for
  Postgres builtin fact readiness and repair
- `gongctl search calls` for minimized read-model call metadata; use explicit
  raw-export commands with `--allow-sensitive-export` for raw payload access
- `gongctl search transcripts`
- `gongmcp --tool-preset business-pilot`: `get_sync_status`,
  `summarize_call_facts`, `summarize_calls_by_lifecycle`, and
  `rank_transcript_backlog`
- operator smoke/search allowlists for `search_calls`, `get_call`, and
  `search_transcript_segments`

It does not yet provide full SQLite query parity for governance filtered DB
export, profile lifecycle source, analyst/all-readonly presets,
business-analysis views, support bundles, cache inventory, or broader
CRM/lifecycle-heavy MCP tools. See the
[Postgres parity matrix](postgres-parity.md) for the phased parity contract.

Read-only `gongmcp` never rebuilds the Postgres read model. If startup reports a
missing or stale Postgres read model, run the writable operator command first:

```bash
export GONG_DATABASE_URL="$GONGCTL_WRITER_DATABASE_URL"
gongctl sync read-model --rebuild
```

Run the local synthetic smoke:

```bash
scripts/postgres-smoke.sh
```

Inspect the Compose shape:

```bash
docker compose -f docker-compose.postgres.yml config --quiet
```

The Compose example separates:

- `postgres`: shared database
- `gongctl`: writer/sync container
- `gongmcp`: read-only MCP container using a Postgres reader role

## MCP Over Docker

Point an MCP host at `docker run` with stdin kept open:

```json
{
  "mcpServers": {
    "gong": {
      "command": "docker",
      "args": [
        "run",
        "--rm",
        "-i",
        "--network",
        "none",
        "-v",
        "/Users/YOU/gongctl-data:/data:ro",
        "ghcr.io/fyne-coder/gongcli_mcp/gongmcp:v0.3.2",
        "--db",
        "/data/gong.db",
        "--tool-preset",
        "business-pilot"
      ]
    }
  }
}
```

Replace `/Users/YOU/gongctl-data` with the absolute host path that contains `gong.db`.

The MCP container does not need Gong API credentials because it only reads the configured cache store. Use `gongctl sync ...` commands to refresh that cache.

## HTTP MCP For Private Pilots

The same MCP-only image can expose `/mcp` over HTTP when a company wants one
customer-managed endpoint for multiple approved users or MCP hosts:

```bash
docker run --rm \
  -p 127.0.0.1:8080:8080 \
  -e GONGMCP_BEARER_TOKEN_FILE=/run/secrets/gongmcp_token \
  -e GONGMCP_TOOL_PRESET=business-pilot \
  -e GONGMCP_TRANSCRIPT_EVIDENCE_PROVENANCE=redacted \
  -e GONGMCP_ALLOWED_ORIGINS=https://approved-client.example.com \
  -v /srv/gongctl/gong.db:/data/gong.db:ro \
  -v /srv/gongctl/secrets/gongmcp_token:/run/secrets/gongmcp_token:ro \
  ghcr.io/fyne-coder/gongcli_mcp/gongmcp:v0.3.2 \
  --http 0.0.0.0:8080 \
  --auth-mode bearer \
  --allow-open-network \
  --db /data/gong.db
```

HTTP mode does not need Gong credentials and opens the configured cache store
read-only. It is a private-pilot request/response endpoint over one
operator-owned cache, not a tenant router or hosted review application. HTTP
mode always requires an explicit tool preset or allowlist, including loopback
binds behind a proxy. Non-local binds also require `--allow-open-network`, an
explicit Origin allowlist, and TLS termination at a trusted company
proxy/gateway. For Postgres HTTP MCP, omit `--db` and set `GONG_DATABASE_URL`
to the reader role URL. The customer's IT/platform owner manages bearer tokens
outside the repo, image, and cache store. Suitable
places include Docker secrets, mounted secret files, systemd environment files,
Kubernetes Secrets, or a company secret manager.
Use `/healthz` for infrastructure health checks and `/mcp` only for MCP
JSON-RPC traffic.

Use the named tool profiles in
[Customer implementation checklist](implementation-checklist.md#named-tool-profiles)
when deciding `GONGMCP_TOOL_PRESET` or `GONGMCP_TOOL_ALLOWLIST`. The example
above uses `business-pilot`.

The example binds the host port to `127.0.0.1` so only a local customer proxy,
gateway, or tunnel can reach it. Do not change this to `-p 8080:8080` unless
the host firewall, private network, and customer auth boundary are already in
place and approved for a temporary lab bridge.

Unauthenticated HTTP is blocked by default. It is only for explicit local
development with the native binary, not for shared Docker pilots:

```bash
GONGMCP_TOOL_PRESET=operator-smoke \
  gongmcp --http 127.0.0.1:8080 --auth-mode none --dev-allow-no-auth-localhost --db /srv/gongctl/gong.db
```

Non-local unauthenticated HTTP is not supported. Binding HTTP to a non-local
address requires bearer auth and fails unless `--allow-open-network` is set.
Use that override only behind an approved TLS/private-network boundary during a
temporary pilot.

For remote clients that require HTTPS/OAuth, put this HTTP service behind a
customer-managed gateway or broker. See
[Remote MCP auth and connector setup](remote-mcp-auth.md) for ChatGPT, Claude
remote add-by-URL, JumpCloud, Cognito, and other IdP patterns.

## Claude Add-By-URL Requires HTTPS

Claude's UI flow for adding a remote MCP server validates the URL and requires
`https://`. A local endpoint such as `http://127.0.0.1:8080/mcp` is still useful
for curl, MCP Inspector, and desktop JSON bridge testing, but it is not the
right onboarding path for users who add MCP servers through Claude's UI.

For that flow, deploy `gongmcp` behind a company-managed HTTPS endpoint:

```text
Claude UI -> https://gong-mcp.example.com/mcp -> TLS/proxy/gateway -> gongmcp http://127.0.0.1:8080/mcp
```

A minimal reverse-proxy shape looks like:

```text
gong-mcp.example.com {
  reverse_proxy 127.0.0.1:8080
}
```

Keep bearer token values out of Git and out of image layers. Store them in a
secret manager or mounted secret file, pass only the HTTPS `/mcp` URL to users,
and use the UI's authentication/token flow if enabled by the MCP host. For a
quick local demo, a temporary HTTPS tunnel can prove reachability, but it should
not be treated as a production deployment for customer data.

## Publishing Shape

For company-managed use, publish two distinct artifacts: the full image for
operator sync jobs and the MCP-only target for business-user MCP hosts. Do not
point business-user hosts at the full image. Pin immutable tags or digests in
the MCP host config. The expected operational contract is:

- the company controls the image tag and rollout
- each tenant/user controls credentials and local or mounted data
- SQLite/transcript/profile paths are mounted volumes, not image contents
- MCP stays read-only; HTTP mode is a private-pilot transport, not a tenant
  management layer
- shared hosts should avoid long-lived plain environment variables where possible; Docker socket access can expose container environment through inspection
- production rollouts should pin immutable digests and can add image signing outside this repo's local-development defaults
- stdio MCP host configs must use the MCP-only target once published, with no
  Gong credentials, `--network none`, and a read-only SQLite mount
- HTTP MCP host configs must use the MCP-only target with no Gong credentials,
  an explicit tool preset or allowlist, a read-only SQLite mount, and a TLS/private-network
  boundary managed outside the container
