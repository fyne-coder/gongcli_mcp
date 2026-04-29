# Docker Deployment

`gongctl` can run as a local container for two current use cases:

- one-shot CLI sync, search, and analysis commands
- read-only stdio MCP over a mounted SQLite cache

The container does not turn `gongmcp` into an HTTP service. MCP remains a stdio process that reads SQLite only. Keep credentials and customer data outside the image.

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
	        "gongctl:mcp-local",
	        "--db",
	        "/data/gong.db"
      ]
    }
  }
}
```

Replace `/Users/YOU/gongctl-data` with the absolute host path that contains `gong.db`.

The MCP container does not need Gong API credentials because it only reads the SQLite cache. Use `gongctl sync ...` commands to refresh that cache.

## Publishing Shape

For company-managed use, publish two distinct artifacts: the full image for
operator sync jobs and the MCP-only target for business-user MCP hosts. Do not
point business-user hosts at the full image. Pin immutable tags or digests in
the MCP host config. The expected operational contract is:

- the company controls the image tag and rollout
- each tenant/user controls credentials and local or mounted data
- SQLite/transcript/profile paths are mounted volumes, not image contents
- MCP stays read-only until an explicit remote-auth design exists
- shared hosts should avoid long-lived plain environment variables where possible; Docker socket access can expose container environment through inspection
- production rollouts should pin immutable digests and can add image signing outside this repo's local-development defaults
- MCP host configs must use the MCP-only target once published, with no Gong
  credentials, `--network none`, and a read-only SQLite mount
