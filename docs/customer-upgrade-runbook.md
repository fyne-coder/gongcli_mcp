# Customer Upgrade Runbook

This runbook consolidates the customer-hosted upgrade path for `gongctl`,
`gongmcp`, and `gongmcp-gateway`. Use it when promoting from one tagged
release to another, including 0.5.x patch upgrades.

The important boundary is operational: `gongctl` owns writable sync,
migrations, read-model rebuilds, serving refreshes, and reader-grant
reconciliation. `gongmcp` stays read-only and should never be used as the
migration or repair path.

## Upgrade Unit

For hosted remote MCP deployments, treat the upgrade as a stack promotion:

```text
public HTTPS /mcp gateway
  -> private bearer-authenticated gongmcp
  -> read-only SQLite cache or scoped Postgres reader
```

The writable sync path remains separate:

```text
operator or scheduler
  -> gongctl sync and deploy commands
  -> source cache and optional governed serving DB
```

Do not expose raw `gongmcp` publicly during an upgrade. Hosted Claude, ChatGPT,
or custom remote MCP clients should continue to reach only the gateway or
customer OAuth broker.

## Preflight

Before changing production:

1. Record the current release tag, image digests, and runtime command lines for
   `gongctl`, `gongmcp`, and `gongmcp-gateway`.
2. Record the current public MCP URL, OAuth/OIDC client settings, redirect URI,
   required scope, access group, tool preset or allowlist, and internal bearer
   token secret location. Do not record secret values.
3. Back up the SQLite file or Postgres databases, transcript files, profile
   YAML, AI governance YAML, and MCP host or connector config.
4. Restore the backup into an isolated staging location and prove the restore
   can be read before running the candidate image against production data.
5. Pull candidate images by immutable tag or digest. Prefer digest-pinned
   deployment manifests.

For Postgres deployments, keep these URLs separate:

| URL | Used by | Purpose |
| --- | --- | --- |
| Source writer URL | `gongctl sync ...`, `gongctl sync read-model --rebuild` | Writable source cache and migrations |
| Serving writer URL | `gongctl deploy postgres-refresh`, `gongctl mcp postgres-reader-apply` | Governed serving DB refresh and grants |
| Scoped reader URL | `gongmcp`, MCP smoke tests, optional reader-side `sync status` | Read-only MCP serving |

A reader URL in a writer command cannot migrate, refresh, or reconcile grants.
A writer URL in `gongmcp` or reader-side validation weakens the deployment
boundary and should fail read-only posture checks.

## SQLite Upgrade

Use this path for local or single-file cache deployments:

1. Stop the MCP host or disconnect the business-user MCP config.
2. Copy the current SQLite cache and related profile/transcript files to a
   protected staging path.
3. Run the candidate `gongctl` image or binary against the copy:

   ```bash
   gongctl sync status --db /protected-copy/gong.db
   ```

   Opening the cache through the writable operator path applies required
   cache-side updates. Do not attempt schema repair from read-only `gongmcp`.
4. Start candidate `gongmcp` against the copied cache with a read-only mount
   and the intended preset or allowlist.
5. Run `initialize`, `tools/list`, and `get_sync_status`.
6. Promote by replacing the production binary/image and cache together during
   the maintenance window.

Rollback means restoring the prior image digest and the prior verified cache
copy. If the candidate modified the SQLite schema, do not roll back only the
binary while leaving the upgraded cache in place.

## Postgres Upgrade

Use this path for shared or customer-hosted Postgres deployments:

1. Restore a recent production backup into isolated staging.
2. Run the candidate `gongctl` image with the source writer URL. Postgres
   schema migrations run when `gongctl` opens Postgres with a writable URL.
3. Rebuild the source read model:

   ```bash
   GONG_DATABASE_URL="$GONGCTL_SOURCE_DATABASE_URL" \
     gongctl sync read-model --rebuild
   ```

4. Refresh the governed serving DB and reconcile reader grants in one operator
   command:

   ```bash
   gongctl deploy postgres-refresh \
     --source "$GONGCTL_SOURCE_DATABASE_URL" \
     --target "$GONGCTL_MCP_DATABASE_URL" \
     --preset business-workbench \
     --role "$GONGMCP_READER_ROLE" \
     --database "$GONGCTL_MCP_DB" \
     > refresh-serving-db.json
   ```

   Add either `--config /path/to/ai-governance.yaml` or
   `--no-governance-exclusions`, matching the deployment's approved governance
   posture.
5. Review `refresh-serving-db.json`. It should contain sanitized step results,
   counts, fingerprints, marker IDs, and grant hashes. It must not contain
   database URLs, credentials, customer names, call IDs, call titles, or raw
   transcript text.
6. Run deploy doctor with the same deploy-parity inputs:

   ```bash
   gongctl doctor postgres-deploy \
     --source "$GONGCTL_SOURCE_DATABASE_URL" \
     --target "$GONGCTL_MCP_DATABASE_URL" \
     --preset business-workbench \
     --role "$GONGMCP_READER_ROLE" \
     --database "$GONGCTL_MCP_DB"
   ```

7. Start candidate `gongmcp` with only the scoped reader URL:

   ```bash
   GONG_DATABASE_URL="$GONGMCP_ANALYST_READER_URL" \
   GONGMCP_TOOL_PRESET=business-workbench \
     gongmcp
   ```

8. Run `initialize`, `tools/list`, and `get_sync_status` through the candidate
   MCP process before exposing the gateway or hosted connector.

If using lower-level `gongctl governance refresh-serving-db` for debugging,
run `gongctl mcp postgres-reader-apply` separately afterward. Manual grants are
not durable across schema changes, serving refreshes, image upgrades, or preset
changes.

## Gateway And Hosted Connector Upgrade

For hosted remote MCP, keep staging and production connector registrations
separate when possible:

```text
staging: https://mcp-staging.company.example.com/mcp
prod:    https://mcp.company.example.com/mcp
```

Run the candidate gateway against staging first:

```bash
gongctl doctor mcp-gateway \
  --url https://mcp-staging.company.example.com/mcp \
  --issuer https://issuer.company.example.com \
  --profile direct-oidc \
  --origin https://claude.ai \
  --required-scope gongmcp/read \
  --group-claim memberOf \
  --client-id "$OIDC_CLIENT_ID" \
  --required-group "$OIDC_REQUIRED_GROUP" \
  --token-env GONGMCP_TEST_ACCESS_TOKEN
```

Production promotion is usually a URL and image-digest change only when the
public URL, client ID, secret, redirect URI, scopes, and token-auth method stay
stable. If any of those OAuth inputs change, expect the hosted connector to
need reauthorization or recreation.

Before broad access, verify:

- unauthenticated `/mcp` returns `401` with `WWW-Authenticate`
- authenticated `tools/list` succeeds
- the first safe tool call, usually `get_sync_status`, succeeds
- blocked users are denied
- forged client-supplied identity headers are ignored or stripped
- no public route reaches raw `gongmcp`

## 0.5.x Upgrade Notes

The 0.5.x line introduced several operational surfaces that affect upgrades:

| Release | Upgrade consideration |
| --- | --- |
| 0.5.0 | Added `deploy postgres-refresh`, `doctor postgres-deploy`, serving-refresh markers, Kubernetes smoke jobs, and preset-aware status checks. Prefer the consolidated deploy command over manual serving refreshes. |
| 0.5.1 | Clarified hosted remote MCP deployment requirements and the gateway/broker boundary. Hosted connectors should not point at raw `gongmcp`. |
| 0.5.2 | Added `gongmcp-gateway`, `doctor mcp-gateway`, direct OIDC compatibility, and remote MCP smoke examples. Gateway and MCP image versions should be promoted together. |
| 0.5.3 | Removed internal debug artifacts from public docs and hardened secret scanning. Public release docs should stay operator-facing. |
| 0.5.4 | Added explicit `--no-governance-exclusions` / `GONGMCP_NO_GOVERNANCE_EXCLUSIONS=1`. No-exclusions deployments must refresh the serving DB with the matching policy fingerprint before `gongmcp` starts. |
| 0.5.5 | Added business-analysis function migrations, dimension filters, participant rollups, serving-refresh timeout controls, failed-step JSON, and expanded deploy support diagnostics. Deploy matching `gongctl` and `gongmcp` binaries with the migrated database. |

The main 0.5.x schema risk is Postgres SECURITY DEFINER function signature
drift. Candidate `gongctl` migrations drop and recreate superseded
business-analysis helper signatures. After migration, run the matching
`gongmcp` image and reapply scoped grants through the deploy command or
`postgres-reader-apply`.

## Rollback

Rollback order:

1. Disable hosted connector or gateway access if user traffic is active.
2. Restore the prior `gongmcp` and gateway image digests, or repoint to the
   prior reviewed serving DB and scoped reader URL.
3. Restore the prior cache or database backup if the candidate ran schema
   migrations, serving refresh, or cache-side updates.
4. Restart only the components that changed.
5. Run `/healthz`, `initialize`, `tools/list`, `get_sync_status`, blocked-user
   denial, and direct DB denial checks.
6. Record the failed image digest or commit, affected preset, sanitized
   artifact paths, and operator action taken.

Do not start rollback with a fresh Gong sync. Restore the last known-good MCP
serving path first, then diagnose sync, governance refresh, or connector auth
separately.

## Acceptance Evidence

Archive non-secret evidence for every customer upgrade:

- current and candidate image digests
- backup or restore artifact reference
- candidate `sync status` or Postgres read-model rebuild output
- `refresh-serving-db.json` when using a governed serving DB
- `doctor postgres-deploy` output for Postgres
- `doctor mcp-gateway` output for hosted remote MCP
- MCP smoke output proving `initialize`, `tools/list`, and `get_sync_status`
- denial evidence for blocked user, raw `gongmcp` public reachability, and
  direct DB writes by the scoped reader

Evidence must use paths, hashes, counts, and pass/fail summaries. Do not store
database URLs, credentials, real call IDs, customer names, raw CRM values, or
transcript text in upgrade artifacts.
