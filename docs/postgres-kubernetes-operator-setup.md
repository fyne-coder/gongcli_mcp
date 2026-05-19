# Postgres Kubernetes Operator Setup

Use this guide when deploying `gongctl` and `gongmcp` into Kubernetes with a
customer-managed Postgres database.

This is the operator path, not the business-user MCP path. The operator runs
`gongctl` Jobs to initialize and refresh data. Business users connect later
through the approved MCP host or SSO gateway.

## Runtime Boundary

- `gongctl` is the writable operator CLI. It receives Gong API credentials and
  a writable Postgres URL.
- `gongmcp` is the read-only MCP runtime. It receives only the scoped reader
  Postgres URL and the MCP bearer token or gateway-protected traffic.
- `gongmcp` does not create, migrate, or populate the database. If it starts
  against a blank Postgres DB, it fails closed with a schema initialization
  error.

## Required Secrets

Operator `gongctl` Jobs need:

- `GONG_ACCESS_KEY`
- `GONG_ACCESS_KEY_SECRET`
- `GONG_BASE_URL`, usually `https://api.gong.io`
- `GONG_DATABASE_URL`, pointing at a writable Postgres operator role

The `gongmcp` Deployment needs:

- `GONG_DATABASE_URL`, pointing at the scoped read-only MCP role
- `GONGMCP_BEARER_TOKEN_FILE` or an equivalent gateway/bearer-token setup
- `GONGMCP_TOOL_PRESET`, usually `business-workbench` for the current
  customer-facing Postgres surface

Do not give Gong API keys to `gongmcp`.

## Image Invocation

The `gongctl` image has `gongctl` as its entrypoint and `--help` as the default
command. In Kubernetes, override `args` for a single command:

```yaml
containers:
  - name: gongctl
    image: ghcr.io/fyne-coder/gongcli_mcp/gongctl:v0.4.6
    args: ["sync", "status"]
```

Use `command` only when you need a shell wrapper that runs several commands:

```yaml
command: ["/bin/sh", "-lc"]
args:
  - |
    set -eu
    gongctl sync users
    gongctl sync read-model --rebuild
    gongctl sync status
```

## First Database Initialization

Run a one-off Kubernetes Job with the `gongctl` image, the writable
`GONG_DATABASE_URL`, and Gong API credentials. Start with a bounded date window
that matches the approved pilot scope.

Recommended first-run sequence:

```bash
gongctl auth check

gongctl sync calls \
  --from YYYY-MM-DD \
  --to YYYY-MM-DD \
  --preset minimal

gongctl sync users

gongctl sync transcripts \
  --out-dir /transcripts \
  --batch-size 100 \
  --allow-sensitive-export

gongctl sync settings --kind trackers
gongctl sync settings --kind scorecards

gongctl sync read-model --rebuild
gongctl sync status
```

Notes:

- The schema migration happens when `gongctl` opens Postgres with the writable
  URL.
- Remove the transcript step if transcript search is not approved for the
  pilot.
- In restricted mode, transcript sync requires `--allow-sensitive-export` or
  `GONGCTL_ALLOW_SENSITIVE_EXPORT=1` as explicit operator approval.
- Use `--preset business`, `--include-parties`, or `--include-highlights` only
  after the sponsor approves the additional cached data exposure.

## Recurring Refresh Cadence

Start with:

- Daily `sync calls` over a rolling recent window.
- Daily `sync users`.
- Daily `sync transcripts` only if transcript search is in scope.
- Daily or weekly `sync settings --kind trackers` and
  `sync settings --kind scorecards`, depending on how often Gong admin
  metadata changes.
- `sync read-model --rebuild` after each refresh.
- `sync status` after each refresh for monitoring.

For Postgres shared deployments, schedule direct `gongctl sync ...` commands or
a reviewed shell wrapper. Do not use `sync run --config` for this path today;
that runner is SQLite-oriented.

## Operator MCP Smoke Test

An operator does not need to be a business MCP user to prove the deployment
works end to end. Validate the data path first, then the MCP serving path.

Confirm data/readiness:

```bash
GONG_DATABASE_URL="$READER_OR_WRITER_URL" gongctl sync status
```

Confirm HTTP MCP with the bearer token:

```bash
curl -sS \
  -H "Authorization: Bearer $GONGMCP_BEARER_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"operator-smoke","version":"0"}}}' \
  https://MCP_HOST/mcp

curl -sS \
  -H "Authorization: Bearer $GONGMCP_BEARER_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' \
  https://MCP_HOST/mcp

curl -sS \
  -H "Authorization: Bearer $GONGMCP_BEARER_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"gong_status","arguments":{}}}' \
  https://MCP_HOST/mcp
```

If a gateway such as JumpCloud OIDC sits in front of `/mcp`, test that layer
separately:

- unauthenticated requests are rejected
- authenticated approved users can reach `/mcp`
- forged client-supplied identity headers are ignored or overwritten by the
  gateway

The bearer-token smoke proves the underlying `gongmcp` runtime works. The
gateway test proves the user access path works.

## Common Errors

`postgres schema is not initialized; run gongctl sync with a writable Postgres URL`

: `gongmcp` started against a blank or unmigrated DB. Run a writable
  `gongctl` Job first with `GONG_DATABASE_URL` set to the operator/writer URL.

`--db is required`

: The command did not find either `--db` for SQLite or `GONG_DATABASE_URL` /
  `DATABASE_URL` for Postgres. For Kubernetes Postgres jobs, set
  `GONG_DATABASE_URL` in the Job environment.

Restricted-mode transcript or extended-context command is blocked

: The command can cache sensitive data. Rerun only after approval with
  `--allow-sensitive-export` or `GONGCTL_ALLOW_SENSITIVE_EXPORT=1`.

`gongmcp` starts but MCP tool calls fail readiness checks

: Re-run `gongctl sync read-model --rebuild`, confirm scoped reader grants with
  `gongctl mcp postgres-reader-apply --dry-run`, then apply approved grants with
  the writable URL.
