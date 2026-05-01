# Enterprise Deployment

## Purpose

This document defines the current enterprise pilot deployment shape for
`gongctl`. The core boundary is unchanged:

- `gongctl` is the writable operator tool that authenticates to Gong and
  refreshes local cache state.
- `gongmcp` is a read-only MCP server over an existing SQLite cache. It supports
  local stdio and a minimal HTTP `/mcp` private-pilot mode.
- Business users should consume only the approved MCP tool set through host or
  wrapper policy. They do not run live syncs, handle Gong credentials, or write
  to SQLite.

This is a pilot-candidate operating model, not a hosted service design.
Customer identity, raw transcripts, secrets, and tenant-specific filesystem
details should stay outside shared docs and outside the source repo.

Security-review readers should also use
[Data Boundary Statement](data-boundary-statement.md) for the customer-hosted
data boundary and [Support](support.md) for sanitized diagnostic-bundle and
support-access policy. The complete customer-hosted review packet is indexed in
[Customer-hosted package](customer-hosted-package.md). Remote HTTPS/OAuth and
ChatGPT connector setup are covered in
[Remote MCP auth and connector setup](remote-mcp-auth.md).

Current limitations still matter for deployment approval: `gongmcp` can narrow
its tool surface with an allowlist and can require bearer tokens for HTTP mode,
and `gongctl` now has a restricted company mode for high-risk CLI commands, but
neither control replaces operator ownership of storage, host policy, and
process access.

## Roles And Ownership

### IT / RevOps operator

- owns Gong credentials and sync scope
- runs or schedules `gongctl` refresh jobs
- controls SQLite, transcript, and profile storage
- validates cache freshness before exposing MCP to business users
- manages backup, retention, and decommissioning

### Business user

- uses an approved MCP host connected to `gongmcp`
- reads aggregate or bounded cached data only
- does not receive Gong credentials
- does not run writable CLI sync or profile-import commands

### Platform / security owner

- approves host, filesystem, backup, and monitoring controls
- approves the MCP tool set exposed to business users and configures the
  `gongmcp` allowlist for that deployment
- owns incident escalation and credential rotation policy

## Supported Deployment Modes

### 1. Admin workstation pilot

Use when one operator runs syncs on a managed workstation and optionally exposes
local MCP to a small reviewed pilot.

- `gongctl` runs with network access and operator-managed credentials.
- SQLite, transcript files, and profile YAML stay on protected local storage
  outside the repo.
- `gongmcp` reads the same cache through a read-only path.
- Best for initial validation, limited concurrency, and short pilot windows.

### 2. Company-managed container or VM

Use when the company wants a repeatable managed runtime without changing the
product boundary.

- Run `gongctl` in Docker or on a managed host for writable sync jobs.
- Mount a protected external data directory for the SQLite cache, transcript
  output, profiles, and backups.
- Keep Gong credentials in approved secret storage, not in the image.
- Run `gongmcp` as a separate read-only process or container against the same
  mounted cache.

### 3. MCP-only consumer host

Use when business-user access must be separated from the writable sync runtime.

- Build and publish the Docker `mcp` target, not the default full CLI image.
- Stdio `gongmcp` runs with `--network none` when containerized.
- Mount the SQLite cache read-only.
- Do not provide Gong credentials to the MCP process.
- Refresh happens upstream through operator-owned `gongctl` jobs only.

### 4. Private HTTP MCP pilot

Use when a company wants approved users or MCP hosts to connect to one
customer-managed endpoint instead of launching a local subprocess/container on
each workstation.

- Run `gongmcp --http ADDR --auth-mode bearer --tool-preset business-pilot --db PATH`
  or use a reviewed custom `--tool-allowlist`.
- Expose `/mcp` to approved clients through TLS termination at a trusted
  company proxy/gateway or equivalent private-network boundary. Use `/healthz`
  for infrastructure health checks; do not use MCP JSON-RPC as the health
  probe.
- Set `--allowed-origins` or `GONGMCP_ALLOWED_ORIGINS` for every non-local HTTP
  deployment so browser-capable MCP clients cannot bypass the proxy boundary
  through DNS rebinding.
- Keep `gongctl` sync jobs separate from this read-only process.
- Store bearer tokens outside Git, SQLite, images, docs, and shared logs.
- Treat `--auth-mode none --dev-allow-no-auth-localhost` as local developer
  scaffolding only. Non-local unauthenticated HTTP is not supported.

The initial HTTP mode is intentionally small: POST JSON-RPC requests to `/mcp`;
GET streaming/SSE, user management, OIDC, tenant routing, and hosted transcript
review are not implemented here. Non-local HTTP binds require
`--allow-open-network` so operators make that deployment decision deliberately.
Every HTTP mode requires an explicit tool preset or allowlist, including loopback binds
behind a proxy/gateway.

## Storage Classes And Protection

Treat these artifacts as customer data:

- SQLite cache files
- transcript output directories
- tenant profile YAML files

Required controls:

- store them outside the source checkout
- limit access to named operators and approved service accounts
- use host or volume encryption where company policy requires it
- keep backups and restore copies in the same protected data class
- keep logs and review artifacts metadata-only; do not copy transcript text,
  secrets, raw payloads, or tenant-specific IDs into shared docs

Storage-specific guidance:

- SQLite contains cached call, user, transcript, CRM, settings, and sync state
  data, so it should be treated like a protected local database, not a scratch
  file.
- Transcript output contains raw normalized transcript JSON and should be kept
  at least as restricted as the SQLite cache.
- Profile YAML can encode tenant CRM object names, field names, lifecycle
  mappings, tracker names, or scorecard references and should be protected even
  when it does not contain transcript text.

## Network And Credential Boundary

`gongctl` and `gongmcp` have different trust assumptions:

- `gongctl` needs network access to Gong and valid credentials for `auth check`
  and `sync ...` commands.
- `gongmcp` reads SQLite only and should not receive Gong credentials.
- For containerized stdio MCP, prefer `docker run --network none` with a
  read-only data mount.
- For HTTP MCP, require bearer auth, an explicit tool preset or allowlist, and TLS
  termination at a trusted proxy/gateway for shared access.
- For customer-specific AI-use restrictions, mount a private AI governance
  config, run `gongctl governance audit`, and start `gongmcp` with
  `--ai-governance-config` or `GONGMCP_AI_GOVERNANCE_CONFIG`.
  Restart is mandatory after cache/config changes because `gongmcp`
  fingerprints both and fails closed if either changes while running.
- For shared environments, separate the writable sync runtime from the
  business-user MCP runtime even if both read the same protected data root.

## HTTP MCP Token Ownership

Bearer tokens are deployment secrets owned by the customer IT/platform owner.
The repo supports supplying them through:

- `GONGMCP_BEARER_TOKEN`
- `GONGMCP_BEARER_TOKEN_FILE`
- `--bearer-token`
- `--bearer-token-file`
- `GONGMCP_BEARER_TOKEN_PREVIOUS_FILE`
- `--bearer-token-previous-file`

Prefer a secret file or managed secret store over long-lived shell history or
shared `.env` files. Rotate tokens when a pilot participant leaves, when logs or
configs are exposed, or before widening access. For production-grade enterprise
access, put a company-managed gateway or OIDC layer in front of the service
rather than treating a shared static token as durable identity.

Current/previous-token private-bridge rotation:

1. Write the new token to the current token file and the old token to a
   previous-token file.
2. Restart `gongmcp` with `--bearer-token-file CURRENT` and
   `--bearer-token-previous-file PREVIOUS`, or the equivalent environment
   variables.
3. Move approved clients to the new token.
4. Watch payload-free access logs for `token_slot="previous"` on successful
   requests.
5. Remove the previous-token file and restart `gongmcp` after no approved
   clients are using the previous token.

Zero downtime requires rolling or redundant `gongmcp` instances behind the
customer gateway. A single-instance restart interrupts in-flight requests.

For remote MCP clients that expect OAuth, separate human SSO from MCP-compatible
authorization. A customer IdP such as JumpCloud, Cognito, Okta, Entra, or
Cloudflare Access can handle login, but a broker or future native OAuth layer
must still expose MCP-compatible metadata, PKCE-capable authorization, scoped
tokens, and token validation. See
[Remote MCP auth and connector setup](remote-mcp-auth.md).

## AI Provider Boundary

When an MCP host or downstream analyst sends tool results to an AI provider, the
recipient sees whatever `gongmcp` returned: aggregate metadata, configuration,
record references, snippets, or opt-in attribution depending on the tool
surface. That provider path should be reviewed separately from Gong API access.

Deployment approval should answer these questions before business-user access:

- Which model host receives prompts and MCP tool results?
- Is that host approved for Gong-derived transcript and CRM data?
- Are OpenAI or any other model providers covered by the customer's DPA,
  vendor review, and subprocessor approval process?
- Are model logs, traces, file uploads, and support access disabled, minimized,
  or retained according to policy?
- Is the pilot using a customer-managed AI account/gateway or a vendor-managed
  account, and who owns deletion and incident response?

For the cleanest enterprise posture, keep the cache and MCP host inside the
customer's approved environment, use a reviewed AI account or gateway, and send
the minimum tool output needed for the question.

## Restricted CLI Mode

For company-managed operator jobs, enable restricted mode by default with
`GONGCTL_RESTRICTED=1` or `gongctl --restricted ...`.

In restricted mode, these commands require an explicit override
(`--allow-sensitive-export` or `GONGCTL_ALLOW_SENSITIVE_EXPORT=1`):

- `api raw`
- `calls list --context extended`
- `calls show --json`
- `calls export`
- `calls transcript`
- `calls transcript-batch`
- `sync transcripts`
- `sync calls --preset business`
- `sync calls --preset all`

This keeps the default lane safe for `sync status`, `sync users`, minimal call
syncs, schema/settings inventory, and read-model analysis while forcing an
affirmative operator decision for transcript, raw-payload, and extended
CRM-context flows.

The reviewed YAML for `sync run --config ...` cannot self-authorize sensitive
steps. In restricted mode, sensitive transcript or extended-context steps still
require the operator to pass `--allow-sensitive-export` or set
`GONGCTL_ALLOW_SENSITIVE_EXPORT=1` at runtime.

## Config-Driven Refresh Jobs

For recurring refreshes, prefer a reviewed YAML config and run the same file in
dry-run mode before enabling the scheduler:

```bash
bin/gongctl sync run --config /srv/gongctl/company-sync.yaml --dry-run
bin/gongctl sync run --config /srv/gongctl/company-sync.yaml
bin/gongctl cache inventory --db /srv/gongctl/cache/gong.db
bin/gongctl cache purge --db /srv/gongctl/cache/gong.db --older-than 2026-04-01 --dry-run
```

The config runner resolves relative `db` and transcript `out_dir` paths from
the config location, so one reviewed file can travel with the operator-managed
job definition. `cache inventory` is the companion read-only governance check
for DB size, primary table counts, date range, transcript/CRM-context presence,
profile status, and last sync metadata.

`cache purge` is dry-run by default. Use it to preview retention cleanup, then
run the same command with `--confirm` only after backup, legal-hold, and owner
approval checks are complete.

## Admin-Run Sync Contract

The pilot operating contract is admin-run refresh, then read-only consumption.

1. An operator decides the approved sync scope and cadence.
2. `gongctl` runs sync commands against protected writable storage.
3. The operator reviews `sync status` and any required readiness signals.
4. `gongmcp` is started or restarted against the refreshed cache with read-only
   access.
5. Business users connect through an approved MCP host configuration.

Business users should not trigger live refreshes, schema sync, transcript
downloads, raw API passthrough, or profile changes from their MCP workflow.

## Scheduled Refresh Ownership

The scheduler is an operator concern, not an end-user concern.

- The owner should be a named IT/RevOps operator or managed service account.
- The schedule should be documented with scope, time window, and escalation
  contact.
- The scheduled job should point at a reviewed `sync run --config ...` file
  rather than re-encoding flags in multiple places.
- Writable jobs should run where protected storage is already mounted.
- Read-only MCP hosts should consume the latest approved cache; they should not
  mutate it.

An acceptable pilot pattern is:

- calls and users refreshed on a regular business cadence
- transcripts refreshed on a reviewed cadence because they increase data
  sensitivity and storage volume
- CRM schema/settings/profile work refreshed only when needed for approved
  business questions

## Backup, Retention, And Decommissioning

Backup policy should be owned by the company operating the pilot:

- back up the SQLite cache before upgrades, major sync-scope changes, and
  profile changes
- include transcript and profile storage in the same backup plan
- review `cache inventory` output alongside backup logs so unusual DB growth or
  missing sync metadata is caught early
- verify that restores can be mounted back into a read-only MCP runtime before
  treating the backup as valid

Retention policy should define:

- how long SQLite snapshots are kept
- how long transcript files are kept
- when stale profiles and exports are removed
- who approves retention exceptions

For approved retention cleanup, run `cache purge --older-than YYYY-MM-DD`
without `--confirm` first and keep the JSON plan with the change record. The
confirmed purge deletes matching calls plus dependent transcripts, transcript
segments, embedded CRM context, and profile call-fact cache rows. It enables
SQLite `secure_delete`, checkpoints/truncates WAL state, and runs `VACUUM` to
reduce retained bytes in the active database file. It does not delete sync-run
history, profile definitions, CRM schema inventory, settings inventory,
transcript JSON files outside SQLite, snapshots, or backups; handle those
through the company retention workflow.

Decommissioning should include:

1. disable scheduled sync jobs
2. remove MCP host configs that point at the cache
3. revoke or rotate Gong credentials used for the pilot
4. archive or destroy retained SQLite, transcript, and profile data per company
   policy
5. remove container images, volumes, and local working copies that are no
   longer approved

## Incident Response

Treat the following as incidents:

- unexpected exposure of transcript text, raw CRM values, or secrets
- unauthorized write access to SQLite, transcript, or profile storage
- MCP serving stale or unreviewed data after a failed sync
- schema/version mismatch that prevents `gongmcp` from starting cleanly
- failed backups or unverified restore paths

Initial response:

1. stop or isolate the affected sync/MCP process
2. preserve metadata-only logs and error output for review
3. revoke or rotate exposed credentials if secrets may be involved
4. confirm whether protected storage was modified, copied, or mounted too
   broadly
5. restore service only after the cache, runtime version, and mount mode are
   revalidated

For a binary-versus-cache mismatch, repair the writable cache first and only
then restart `gongmcp`. Read-only MCP should not be used as the migration path.

## Pilot Limits

This repo currently documents a conservative deployment shape:

- local or company-managed writable sync
- local or company-managed read-only stdio MCP
- private-pilot HTTP MCP with bearer-token support
- no live Gong API access from MCP
- no shared hosted control plane or tenant/user-management layer in this repo

If the company needs multi-tenant hosting, remote auth, browser-facing APIs, or
centralized transcript review workflows, those belong in a separate application
layer around or in front of `gongmcp`, not in the read-only cache adapter itself.

The conservative defaults documented here are not the only supported posture.
A trusted single-user analyst workstation using stdio can skip the tool
allowlist and enable per-tool opt-ins to surface exact identifiers, bounded
snippets, and attribution joined to Account/Opportunity context for deeper
questions. See
[mcp-data-exposure.md](mcp-data-exposure.md) for the trade-off framing and
[mcp-data-exposure.md#mcp-call-volume-and-limits](mcp-data-exposure.md#mcp-call-volume-and-limits)
for the per-call cost model and recommended limits.
