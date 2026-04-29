# Operator Sync Runbook

## Scope

This runbook is for the IT / RevOps operator who refreshes protected Gong cache
data for an enterprise pilot. It assumes the current product boundary:

- `gongctl` performs writable sync and profile operations
- `gongmcp` reads the resulting SQLite cache only
- business users consume approved MCP tools and do not run sync jobs

Do not paste secrets, transcript text, raw payloads, or tenant-specific IDs
into tickets, docs, or chat while executing this runbook.

## Prerequisites

Before running a sync:

- use a managed host or managed workstation approved for customer data
- confirm Gong credentials are available through environment variables or an
  ignored env file
- confirm the protected data root exists outside the source repo
- confirm there is enough free disk for SQLite growth, transcript output, and
  backup copies
- confirm you know which sync scope is approved for this tenant and pilot phase

Recommended protected directories under a generic `<data-root>`:

- `<data-root>/cache/` for SQLite databases
- `<data-root>/transcripts/` for transcript output
- `<data-root>/profiles/` for reviewed profile YAML
- `<data-root>/backups/` for pre-change snapshots

Set the parent `<data-root>` permissions before running syncs. The current
implementation may create subdirectories that are traversable by local users if
the parent directory is broad, so the parent data root must be restricted by the
host or volume ACLs, for example owner-only access on single-user pilots.

## Approved Operating Rules

- Run writable commands only from `gongctl`, never from `gongmcp`.
- Keep real SQLite, transcript, and profile files outside the checkout.
- Use metadata-only logs. Do not log transcript bodies or secret values.
- Do not give business users Gong credentials, writable storage, or transcript
  export commands.
- For containerized MCP, require a read-only mount and `--network none`.

## Preflight

1. Verify the binary or image version you intend to use.
2. Confirm the protected data root is mounted with the expected permissions.
3. Confirm the backup target is available.
4. Run a credential check.

Source build example:

```bash
bin/gongctl version
bin/gongctl auth check
```

Docker example:

```bash
docker run --rm --env-file .env gongctl:local version
docker run --rm --env-file .env -v <data-root>:/data gongctl:local auth check
```

If `auth check` fails, stop here and fix credentials before touching the cache.

For the Docker-based operator path, `scripts/docker-smoke.sh` is the bounded
end-to-end smoke after credentials and `<data-root>` are set. It covers `auth
check`, a one-page call sync, `sync status`, and a read-only `gongmcp`
`tools/list` request.

## Backup Before Refresh

Take a restorable copy before major refreshes, upgrades, or profile changes.

Minimum backup set:

- active SQLite file
- profile YAML files used by this tenant
- transcript directory if transcript sync is enabled for the pilot

The exact copy command depends on the host. The requirement is operational, not
tool-specific: produce a dated backup in protected storage and verify the copy
completed before continuing.

## Writable Refresh Procedure

Run only the commands approved for this tenant. A common pilot refresh sequence
is:

```bash
bin/gongctl sync calls --db <data-root>/cache/gong.db --from YYYY-MM-DD --to YYYY-MM-DD --preset minimal
bin/gongctl sync users --db <data-root>/cache/gong.db
bin/gongctl sync transcripts --db <data-root>/cache/gong.db --out-dir <data-root>/transcripts --limit 50 --batch-size 50
bin/gongctl sync crm-integrations --db <data-root>/cache/gong.db
bin/gongctl sync crm-schema --db <data-root>/cache/gong.db --integration-id <approved-integration> --object-type ACCOUNT --object-type DEAL
bin/gongctl sync settings --db <data-root>/cache/gong.db --kind scorecards
bin/gongctl sync status --db <data-root>/cache/gong.db
```

Notes:

- Use `--preset minimal` unless approved business questions require embedded CRM
  context from `business` or `all`.
- Transcript sync increases the sensitivity of the stored data. Only run it when
  transcript-backed search or analysis is in scope.
- `sync status` is the required post-refresh verification step because it shows
  cache population and readiness state.
- If the pilot uses a reviewed business profile, validate and import it only
  from the protected profile path:

```bash
bin/gongctl profile validate --db <data-root>/cache/gong.db --profile <data-root>/profiles/gongctl-profile.yaml
bin/gongctl profile import --db <data-root>/cache/gong.db --profile <data-root>/profiles/gongctl-profile.yaml
bin/gongctl sync status --db <data-root>/cache/gong.db
```

## Read-Only MCP Verification

After a successful refresh, verify the read-only MCP runtime separately from the
writable sync process.

Docker example:

```bash
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"operator-smoke","version":"0"}}}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' \
| docker run --rm -i \
    --network none \
    -v <data-root>/cache:/data:ro \
    --entrypoint /usr/local/bin/gongmcp \
    gongctl:local \
    --db /data/gong.db
```

Pass criteria:

- `gongmcp` starts without requesting Gong credentials
- the data mount is read-only
- the runtime works without network access
- direct `tools/list` currently shows the full read-only MCP catalog because
  native server-side tool allowlisting is not implemented yet

If you expose MCP through a host app, point that host at the same read-only
database path and expose only the approved tool set. Native `gongmcp`
allowlisting is a planned production-readiness control; until then, enforce the
approved tool set through the MCP host, wrapper configuration, or operator
policy.

## Scheduled Refresh Ownership

If refreshes are scheduled instead of manual:

- assign one named owner for the job and one backup owner
- record the approved cadence and scope
- run scheduled sync where the writable data root and backup target are already
  available
- keep MCP consumer hosts read-only and separate from the refresh job when
  possible

Recommended ownership split:

- operator job owns credentials, sync, backups, and profile imports
- business-user MCP host owns read-only access only

## Backup, Retention, And Restore

Minimum controls:

- retain at least one known-good pre-upgrade SQLite backup
- retain profile backups alongside the active profile
- treat transcript retention separately because transcript volume and
  sensitivity may exceed the cache retention window

Restore test:

1. restore a backup copy into an isolated protected location
2. start `gongmcp` against the restored SQLite file with a read-only mount
3. verify `tools/list` or `get_sync_status`
4. only then treat the backup as usable

## Decommissioning

When the pilot ends or a tenant is removed:

1. stop manual and scheduled sync jobs
2. remove MCP host entries that reference the tenant cache
3. revoke or rotate the Gong credentials used for this pilot
4. archive or destroy SQLite, transcript, profile, and backup data per approved
   retention policy
5. remove stale local copies from operator machines and managed hosts

## Incident Response

### 1. Credential failure

Symptoms:

- `auth check` fails
- sync calls return auth errors

Response:

1. stop sync retries
2. verify the expected credential source
3. rotate or replace the credential if compromise is possible
4. rerun `auth check` before resuming scheduled jobs

### 2. Cache or schema mismatch

Symptoms:

- `gongmcp` fails during startup after a binary upgrade
- the CLI reports an older SQLite schema version

Response:

1. stop the MCP process
2. take a backup copy of the affected SQLite file
3. run a writable CLI command such as `sync status` against the protected cache
   to perform any required cache-side update
4. rerun the read-only MCP smoke
5. restart the MCP host only after the cache is current

Do not try to repair schema drift from the read-only MCP runtime.

### 3. Data exposure incident

Symptoms:

- transcript text appears in the wrong channel
- sensitive files were mounted too broadly
- an export landed in an unapproved location

Response:

1. stop the affected process and remove the bad mount or host config
2. preserve metadata-only evidence
3. remove or quarantine the exposed copy per company policy
4. rotate credentials if secrets were exposed
5. review whether sync scope, logging, or MCP tool exposure needs tightening

### 4. Failed or partial refresh

Symptoms:

- `sync status` shows incomplete readiness
- transcripts or settings are missing unexpectedly

Response:

1. keep the previous approved MCP runtime in place if possible
2. inspect operator-side stderr summaries and job metadata
3. rerun only the failed sync slice once the cause is fixed
4. confirm `sync status` before promoting the refreshed cache

## Verification Checklist

Before closing the change window, confirm:

- pre-refresh backup completed
- required sync commands completed
- `sync status` reflects the expected cache state
- read-only `gongmcp` smoke passed with no network and no credentials
- any scheduled job change has a named owner and documented cadence
