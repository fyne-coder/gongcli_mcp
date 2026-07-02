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
- For `GONGMCP_TOOL_PRESET=broad-public-redacted`, also set
  `GONGMCP_AI_GOVERNANCE_CONFIG`, `GONGMCP_POSTGRES_REDACTED_SERVING_DB=1`,
  and `GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS=1`

Do not give Gong API keys to `gongmcp`.

When enabling JumpCloud/OIDC, keep `gongmcp` itself on internal bearer auth.
JumpCloud authenticates the user at the gateway/broker layer; the gateway then
forwards approved MCP traffic to `gongmcp` with the internal bearer token.
Do not switch `gongmcp` to `auth-mode=none` for shared deployments.

## Postgres URL Separation

Treat these as three different secrets, even though each container receives the
one it needs as `GONG_DATABASE_URL`:

| Secret purpose | Example local name | Used by | Privilege |
|---|---|---|---|
| Source writer URL | `GONGCTL_SOURCE_DATABASE_URL` | `gongctl sync ...`, `gongctl sync read-model --rebuild` | Writable operator role on the source cache. |
| Serving writer URL | `GONGCTL_MCP_DATABASE_URL` | `gongctl governance refresh-serving-db`, `gongctl mcp postgres-reader-apply` | Writable operator role on the redacted serving DB. |
| MCP reader URL | `GONGMCP_READER_DATABASE_URL` | `gongmcp`, MCP smoke tests, optional default-surface `gongctl sync status` checks | Scoped read-only role on the redacted serving DB. |

Use explicit placeholders for the grant target:

- `GONGMCP_READER_ROLE`, for example `gongmcp_reader`
- `GONGCTL_MCP_DB`, for example `gongctl_mcp`

Do not reuse the source writer URL for `gongmcp` or reader-side validation.
Do not use the scoped reader URL for migrations, source sync, serving DB
refresh, or grant reconciliation.

When a single wrapper runs several phases, set `GONG_DATABASE_URL` per phase
instead of exporting one global value for the whole Job:

```bash
GONG_DATABASE_URL="$GONGCTL_SOURCE_DATABASE_URL" gongctl sync calls ...
GONG_DATABASE_URL="$GONGCTL_SOURCE_DATABASE_URL" gongctl sync read-model --rebuild

gongctl governance refresh-serving-db \
  --source "$GONGCTL_SOURCE_DATABASE_URL" \
  --target "$GONGCTL_MCP_DATABASE_URL" \
  --config "$GONGCTL_AI_GOVERNANCE_CONFIG"

GONG_DATABASE_URL="$GONGCTL_MCP_DATABASE_URL" \
  gongctl mcp postgres-reader-apply --preset broad-public-redacted ...

# For broad-public-redacted, validate the reader through gongmcp with the same
# preset and call initialize, tools/list, and gong_status.
```

If `GONG_DATABASE_URL` stays set to the serving writer URL for the whole Job,
source sync writes to the wrong database and reader-side validation fails the
read-only posture check.

## Image Invocation

The `gongctl` image has `gongctl` as its entrypoint and `--help` as the default
command. In Kubernetes, override `args` for a single command:

```yaml
containers:
  - name: gongctl
    image: ghcr.io/fyne-coder/gongcli_mcp/gongctl:v0.6.4
    args: ["sync", "status"]
```

If the `v0.6.4` image has not been published yet, pin the latest published tag
or build and push a customer-owned image before applying the Kubernetes
manifests.

Use `command` only when you need a shell wrapper that runs several commands:

```yaml
command: ["/bin/sh", "-lc"]
args:
  - |
    set -eu
    gongctl sync users
    gongctl sync read-model --rebuild
```

Do not run `gongctl sync status` in the same shell after writer steps unless
you first switch `GONG_DATABASE_URL` to a scoped reader URL. In Postgres mode,
`sync status` opens the database through the read-only validation path and will
reject a writer URL.

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
  --limit 1000 \
  --allow-sensitive-export

gongctl sync settings --kind trackers
gongctl sync settings --kind scorecards

gongctl sync read-model --rebuild
```

Notes:

- The schema migration happens when `gongctl` opens Postgres with the writable
  URL.
- Remove the transcript step if transcript search is not approved for the
  pilot.
- `sync transcripts` defaults to `--limit 100`. For first historical backfill,
  set an approved higher `--limit` or run repeated Jobs until an approved
  reader-side smoke shows the expected transcript coverage. Keep daily
  scheduled refreshes smaller.
- In restricted mode, transcript sync requires `--allow-sensitive-export` or
  `GONGCTL_ALLOW_SENSITIVE_EXPORT=1` as explicit operator approval.
- Use `--preset business`, `--include-parties`, or `--include-highlights` only
  after the sponsor approves the additional cached data exposure.
- `sync status` is a default read-only validation command in Postgres mode.
  Run it only with a reader URL, not a writer URL. For scoped MCP presets such
  as `broad-public-redacted`, use the MCP smoke test below instead of generic
  `sync status`.

## Recurring Refresh Cadence

Start with:

- Daily `sync calls` over a rolling recent window.
- Daily `sync users`.
- Daily `sync transcripts` only if transcript search is in scope.
- Daily or weekly `sync settings --kind trackers` and
  `sync settings --kind scorecards`, depending on how often Gong admin
  metadata changes.
- `sync read-model --rebuild` after each refresh.
- If using a redacted serving DB, refresh the serving DB after the writer sync.
- Reconcile scoped reader grants after serving DB refresh, schema changes, or
  image upgrades.
- Reader validation after grants. For default-surface checks this can be
  `sync status` with the scoped reader URL; for `broad-public-redacted`, use
  the MCP smoke test with the same preset.

For Postgres shared deployments, schedule direct `gongctl sync ...` commands or
a reviewed shell wrapper. Do not use `sync run --config` for this path today;
that runner is SQLite-oriented.

## Scoped Reader Grants

Before starting `gongmcp`, reconcile grants on the serving database with the
serving writer URL. Run this after the first migration/read-model rebuild and
repeat it after every serving DB refresh, schema/image upgrade, or preset
change. Manual grant changes are not a durable substitute for this step.

For the default customer-facing Postgres surface:

```bash
GONG_DATABASE_URL="$GONGCTL_MCP_DATABASE_URL" \
gongctl mcp postgres-reader-apply \
  --preset business-workbench \
  --role "$GONGMCP_READER_ROLE" \
  --database "$GONGCTL_MCP_DB" \
  --dry-run
```

Review the dry run, then rerun with `--apply` if approved. Do not rely on the
command defaults here; they target the legacy `business-pilot` surface and a
sample role name.

For a broad redacted customer-test runtime using
`GONGMCP_TOOL_PRESET=broad-public-redacted`, apply matching scoped grants with
`--preset broad-public-redacted`. Older operator notes may refer to
`redacted-all-readonly`; that is the same underlying broad tool surface but an
internal/manual testing posture.

## Operator MCP Smoke Test

An operator does not need to be a business MCP user to prove the deployment
works end to end. Validate the data path first, then the MCP serving path.

Confirm the source-to-serving refresh and grant reconciliation first:

- `governance refresh-serving-db` reports matching approved source/target
  counts and the expected policy fingerprint.
- `postgres-reader-apply` returns `"status": "applied"` for the same preset,
  role, and database used by `gongmcp`.

For `broad-public-redacted`, do not use generic `gongctl sync status` as the
reader acceptance check. It validates the default read-only function set and
can report missing functions that the broad scoped preset intentionally
replaces with sanitized functions. Validate the actual runtime path by starting
`gongmcp` with the scoped reader URL and the same preset, then call
`initialize`, `tools/list`, and `gong_status`.

Use this as the replacement for a wrapper's final `sync status` phase:

1. Confirm `gongmcp` is running with `GONG_DATABASE_URL` set to
   `GONGMCP_READER_DATABASE_URL`.
2. Confirm `GONGMCP_TOOL_PRESET` matches the grant preset, for example
   `broad-public-redacted`.
3. For `broad-public-redacted`, confirm `GONGMCP_AI_GOVERNANCE_CONFIG`,
   `GONGMCP_POSTGRES_REDACTED_SERVING_DB=1`, and
   `GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS=1` are set.
4. Port-forward the internal `gongmcp` service.
5. Send `initialize`, `tools/list`, and `tools/call` for `gong_status`.
6. Treat the smoke as passing only if `tools/list` returns the expected preset
   surface and `gong_status` returns successfully from Postgres mode.

The following pod logs mean `gongmcp` has passed startup validation and is
ready for the JSON-RPC smoke:

- `postgres backend active: read-only MCP exposes ...`
- `AI governance active: backend=postgres_redacted_serving ...`
- `serving mcp over http on ... path=/mcp auth_mode=bearer ...`

The non-local-address warning is expected when the pod listens on `0.0.0.0`
behind a trusted Kubernetes service/proxy. Do not expose that listener directly
to the public internet; test the public JumpCloud/OIDC gateway separately after
the internal bearer-token smoke passes.

Confirm HTTP MCP with the bearer token against the internal `gongmcp` service,
not the public JumpCloud/OIDC gateway. For example, port-forward the service
from a trusted operator workstation:

```bash
kubectl -n gongctl port-forward svc/gongmcp 8080:8080
```

Then send JSON-RPC to the forwarded service:

```bash
curl -sS \
  -H "Authorization: Bearer $GONGMCP_BEARER_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"operator-smoke","version":"0"}}}' \
  http://127.0.0.1:8080/mcp

curl -sS \
  -H "Authorization: Bearer $GONGMCP_BEARER_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' \
  http://127.0.0.1:8080/mcp

curl -sS \
  -H "Authorization: Bearer $GONGMCP_BEARER_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"gong_status","arguments":{}}}' \
  http://127.0.0.1:8080/mcp
```

If a gateway such as JumpCloud OIDC sits in front of `/mcp`, test that layer
separately against the public hostname:

- unauthenticated requests are rejected
- authenticated approved users can reach `/mcp`
- the gateway forwards only reviewed identity and auth context to `gongmcp`
- forged client-supplied identity headers are ignored or overwritten by the
  gateway

The internal bearer-token smoke proves the underlying `gongmcp` runtime works.
The gateway test proves the user access path works. Do not expect the bearer
token smoke to pass through a public JumpCloud/OIDC gateway unless that gateway
is explicitly configured to pass the internal bearer token through unchanged.
The public side can be JumpCloud/OIDC, but the upstream `gongmcp` service should
remain protected by internal bearer auth.

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

`postgres read-only URL has CREATE privilege on public schema`

: A read-only validation command such as `gongctl sync status` is running with
  a writer-privileged Postgres URL, or the scoped reader role still has schema
  creation privileges. For writer jobs, stop before `sync status`; for
  readiness checks, set `GONG_DATABASE_URL` to the scoped reader URL and rerun
  `gongctl mcp postgres-reader-apply` against the serving writer URL if the
  reader role needs repair.
  If the selected URL is unclear, connect with the same secret and verify
  `SELECT current_user, current_database();`.

`postgres read-only URL is missing required column SELECT grants`

: The scoped reader grants are stale for the current schema or selected MCP
  preset. Re-run `gongctl mcp postgres-reader-apply` on the serving database
  with the serving writer URL after migrations, serving DB refresh, and image
  upgrades. For `broad-public-redacted`, use `--preset broad-public-redacted`
  for the grant apply step.
  If manual grant application fixes the error but a later refresh breaks it
  again, the refresh/deployment pipeline is resetting privileges after the
  manual step; move `postgres-reader-apply` later in the automated flow.

`postgres read-only URL is missing required function EXECUTE grants`

: If this appears from `gongctl sync status` after
  `postgres-reader-apply --preset broad-public-redacted` succeeded, the grant
  apply may be correct and the validation command may be wrong. `sync status`
  checks the default read-only function set, while the broad redacted MCP
  preset intentionally grants the reviewed preset surface, including sanitized
  function variants. Start `gongmcp` with the same reader URL and preset, then
  validate `initialize`, `tools/list`, and `gong_status`. Use
  `sync status` only for default-surface reader checks until a preset-aware
  CLI doctor/status command exists.

`gongmcp` starts but MCP tool calls fail readiness checks

: Re-run `gongctl sync read-model --rebuild`, confirm scoped reader grants with
  `gongctl mcp postgres-reader-apply --preset business-workbench --role
  "$GONGMCP_READER_ROLE" --database "$GONGCTL_MCP_DB" --dry-run`, then apply
  approved grants with the writable URL.
