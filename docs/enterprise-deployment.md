# Enterprise Deployment

## Purpose

This document defines the current enterprise pilot deployment shape for
`gongctl`. The core boundary is unchanged:

- `gongctl` is the writable operator tool that authenticates to Gong and
  refreshes local cache state.
- `gongmcp` is a read-only stdio MCP server over an existing SQLite cache.
- Business users should consume only the approved MCP tool set through host or
  wrapper policy. They do not run live syncs, handle Gong credentials, or write
  to SQLite.

This is a pilot-candidate operating model, not a hosted service design and not
yet a software-enforced enterprise mode. Customer identity, raw transcripts,
secrets, and tenant-specific filesystem details should stay outside shared docs
and outside the source repo.

Current limitations matter for deployment approval: `gongmcp` does not yet have
server-side tool allowlisting, and `gongctl` does not yet have a restricted
company mode. Until those controls are implemented, tool access and CLI access
are enforced by host configuration, wrapper configuration, filesystem access,
and operator process.

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
- approves the MCP tool set exposed to business users. Native `gongmcp`
  allowlisting is a planned production-readiness control; until then, restrict
  tool access through the MCP host, wrapper configuration, or operator policy.
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

- `gongmcp` runs with `--network none` when containerized.
- Mount the SQLite cache read-only.
- Do not provide Gong credentials to the MCP process.
- Refresh happens upstream through operator-owned `gongctl` jobs only.

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
- For containerized MCP, prefer `docker run --network none` with a read-only
  data mount.
- For shared environments, separate the writable sync runtime from the
  business-user MCP runtime even if both read the same protected data root.

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
- verify that restores can be mounted back into a read-only MCP runtime before
  treating the backup as valid

Retention policy should define:

- how long SQLite snapshots are kept
- how long transcript files are kept
- when stale profiles and exports are removed
- who approves retention exceptions

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
- no live Gong API access from MCP
- no shared hosted control plane in this repo

If the company needs multi-tenant hosting, remote auth, browser-facing APIs, or
centralized transcript review workflows, those belong in a separate application
layer rather than widening `gongmcp`.
