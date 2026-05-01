# Docker Deployment

`gongctl` can run as a local container for three current use cases:

- one-shot CLI sync, search, and analysis commands
- read-only stdio MCP over a mounted SQLite cache
- read-only HTTP MCP private pilots over a mounted SQLite cache

HTTP mode is explicit via `gongmcp --http ...`; the default MCP path remains
stdio. In both modes, `gongmcp` reads SQLite only. Keep credentials and customer
data outside the image.

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
docker pull ghcr.io/fyne-coder/gongcli_mcp/gongctl:v0.2.0
docker pull ghcr.io/fyne-coder/gongcli_mcp/gongmcp:v0.2.0
```

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
        "ghcr.io/fyne-coder/gongcli_mcp/gongmcp:v0.2.0",
        "--db",
        "/data/gong.db"
      ]
    }
  }
}
```

Replace `/Users/YOU/gongctl-data` with the absolute host path that contains `gong.db`.

The MCP container does not need Gong API credentials because it only reads the SQLite cache. Use `gongctl sync ...` commands to refresh that cache.

## HTTP MCP For Private Pilots

The same MCP-only image can expose `/mcp` over HTTP when a company wants one
customer-managed endpoint for multiple approved users or MCP hosts:

```bash
docker run --rm \
  -p 127.0.0.1:8080:8080 \
  -e GONGMCP_BEARER_TOKEN_FILE=/run/secrets/gongmcp_token \
  -e GONGMCP_TOOL_PRESET=business-pilot \
  -e GONGMCP_ALLOWED_ORIGINS=https://approved-client.example.com \
  -v /srv/gongctl/gong.db:/data/gong.db:ro \
  -v /srv/gongctl/secrets/gongmcp_token:/run/secrets/gongmcp_token:ro \
  ghcr.io/fyne-coder/gongcli_mcp/gongmcp:v0.2.0 \
  --http 0.0.0.0:8080 \
  --auth-mode bearer \
  --allow-open-network \
  --db /data/gong.db
```

HTTP mode does not need Gong credentials and still opens SQLite read-only. It is
a private-pilot request/response endpoint over one operator-owned cache, not a
tenant router or hosted review application. HTTP mode always requires an explicit
tool preset or allowlist, including loopback binds behind a proxy. Non-local binds also
require `--allow-open-network`, an explicit Origin allowlist, and TLS
termination at a trusted company proxy/gateway. The customer's IT/platform
owner manages bearer tokens outside the repo, image, and SQLite cache. Suitable
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
