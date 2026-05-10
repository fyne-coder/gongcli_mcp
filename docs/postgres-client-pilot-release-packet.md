# Postgres Client Pilot Release Packet

## Purpose

Use this packet when preparing a controlled customer-hosted Postgres pilot where
sync and MCP run in separate containers or hosts. It is a client-sharing
checklist, not a GA claim.

For the short operator sequence, use the
[Postgres client onboarding checklist](postgres-client-onboarding-checklist.md)
after this packet is reviewed. For business-user acceptance prompts and
pass/fail recording, use the
[Postgres client manual-test checklist](postgres-client-manual-test-checklist.md).
For the customer-hosted deployment sequence, use the
[Postgres client deployment runbook](runbooks/postgres-client-deployment.md).
For client-facing question scope, use the
[Postgres question-parity matrix](postgres-question-parity.md).

SQLite remains the default local workflow with the broadest coverage. Postgres is the shared
deployment path for multi-container pilots that cannot rely on a shared
filesystem. For small company installs that want one hardened VM first, use
[`deploy/single-vm-postgres`](../deploy/single-vm-postgres/README.md) as the
Compose scaffold for the same source DB, serving DB, scoped reader, and
read-only MCP boundary.

## Shareable Pilot Surface

Supported for a controlled pilot:

- SQLite local/single-host cache with the existing `--db PATH` workflow.
- Postgres shared cache with `GONG_DATABASE_URL` or `DATABASE_URL` for writable
  `gongctl` operator commands.
- Read-only Postgres `gongmcp` with `business-pilot`.
- Reviewed Postgres `analyst`, `analyst-expansion`, `analyst-core`, and
  `analyst-business-core` surfaces for approved analyst sessions.
- Scoped Postgres reader roles for `business-pilot` and reviewed analyst
  sessions, reconciled with `gongctl mcp postgres-reader-apply`.
- MCP-layer small-cell suppression for enforced scoped Postgres `analyst` and
  `analyst-expansion` sessions. Dimension buckets below 3 calls are omitted and
  responses include `small_cell_suppression_applied` plus
  `small_cell_suppression_min_3`.

Not shareable as supported Postgres pilot scope:

- Postgres `all-readonly`, `all-tools`, or `all`.
- Native OAuth inside `gongmcp`.
- Vendor-hosted SaaS, multi-tenant routing, or browser transcript review.
- Customer production capacity claims from repo synthetic smoke alone.
- Database-enforced governed analyst aggregates, RLS, materialized governed
  snapshots, or differential privacy claims.

## Required Runtime Boundary

- `gongctl` gets Gong credentials and the writable Postgres URL.
- `gongmcp` gets only a read-only Postgres URL and the approved tool preset.
- Business users do not receive Gong credentials, writable DB URLs, local DB
  files, transcript exports, or raw cached payloads.
- For shared analyst sessions, use a scoped analyst reader role and set:

```bash
GONGMCP_TOOL_PRESET=analyst
GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS=1
```

Use `analyst-expansion` only after the same scoped-reader and sponsor-review
checks pass.

## Repo Synthetic Evidence

Before sharing a pilot packet, run and archive the command outputs listed by
the scripts. Do not archive whole temp directories unless they have been
reviewed for local paths and debug-only material.

```bash
go test -count=1 ./...
go vet ./...
make secret-scan
docker compose -f docker-compose.postgres.yml config --quiet
docker compose --env-file deploy/single-vm-postgres/single-vm.env.example \
  -f deploy/single-vm-postgres/docker-compose.yml config --quiet
GONGCTL_POSTGRES_COMPOSE_PROJECT=gongctl-postgres-smoke-client \
GONGCTL_POSTGRES_PORT=55630 \
./scripts/postgres-smoke.sh
GONGCTL_POSTGRES_LOAD_COMPOSE_PROJECT=gongctl-postgres-load-client \
GONGCTL_POSTGRES_LOAD_PORT=55631 \
GONGCTL_POSTGRES_LOAD_CALLS=1200 \
GONGCTL_POSTGRES_LOAD_PROFILE_CACHE_ROWS=1200 \
./scripts/postgres-load-smoke.sh
```

Minimum evidence to retain from synthetic runs:

- `postgres-smoke.sh` analyst JSONL proving scoped analyst startup and
  `small_cell_suppression_applied` on a singleton bucket.
- `postgres-smoke.sh` `all-readonly` rejection artifact.
- `postgres-load-smoke.sh` `summary.json`, `counts.txt`, and
  `analyst-dimensions.jsonl` proving high-count analyst buckets remain visible.
- Read-only write-denial and raw-read-denial artifacts.
- Secret-scan output.

## Customer-Platform Dry Run

Synthetic repo evidence is not enough for client rollout. Before real business
users connect, the customer platform owner should run a dry run on the target
Postgres class with synthetic or approved non-production data and record:

- selected Postgres service class, version, storage class, and network boundary
- restore/PITR setting and retention window
- backup restore into an isolated database
- `gongctl sync read-model --rebuild` after restore
- read-only `gongmcp` smoke against the restored database
- scoped reader grant reconciliation output for each enabled preset
- read-only write-denial and raw-read-denial checks
- expected concurrency target and observed startup/query timings for the
  enabled presets
- statement-timeout and connection-limit settings owned by the customer
  platform
- rollback test using the prior image digest and prior restored cache/config

Do not introduce real customer data until the dry run is reviewed and the pilot
sponsor approves the exact sync scope.

## Image And Release Pinning

For client pilots, use immutable image tags or digests. Record both the tag and
the resolved digest before deployment:

```bash
docker buildx imagetools inspect ghcr.io/fyne-coder/gongcli_mcp/gongctl:vX.Y.Z
docker buildx imagetools inspect ghcr.io/fyne-coder/gongcli_mcp/gongmcp:vX.Y.Z
```

Deploy the MCP-only image for business-user MCP hosts. Keep the full `gongctl`
image restricted to operator sync jobs.

If the required changes are not in a published tag yet, share source plus local
build instructions as a development-branch pilot, and state that image tags are
not available until the release workflow publishes them.

## Rollback Checklist

Before promotion:

- record the current image digest and candidate image digest
- back up SQLite or Postgres plus transcript/profile/governance config
- verify a restore in isolation
- run MCP `tools/list` and at least one approved `tools/call`
- record scoped reader grant state

Rollback means restoring the prior image digest and the prior verified
cache/config backup. Postgres PITR, replica rewind, backup encryption, and
backup retention are customer-platform controls.

## Non-GA Follow-Ups

Queue these before broad client or GA sharing:

- full Postgres `all-readonly` query parity
- database-enforced governed analyst aggregate variants
- RLS or materialized governed snapshots where customer policy requires them
- tenant-scale statement-timeout/index profiling on the customer Postgres class
- production backup/PITR/replica restore drill owned by the customer platform
- native OAuth in `gongmcp` or a customer-managed OAuth broker with completed
  client interoperability tests
- signed/provenance-backed release artifacts if required by the customer
- support-bundle parity for the exact deployment topology
