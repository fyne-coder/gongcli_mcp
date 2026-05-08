# Postgres Client Shareability Audit

## Verdict

Current branch status: prepared for a `v0.4.0` customer-hosted Postgres
business-workbench release after the release tag and image digest publication
complete. Untagged branch builds remain controlled-pilot only. This is not a
production capacity certification.

The safe customer-hosted boundary is:

- SQLite remains complete/default for local and single-host installs.
- Postgres is the shared-deployment path for separate sync and MCP containers.
- Start business users on `business-pilot`.
- Enable reviewed `analyst` or `analyst-expansion` only with scoped reader
  grants and `GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS=1`.
- Keep Postgres `all-readonly`, `all-tools`, and `all` rejected.

## Requirement-To-Evidence Checklist

| Requirement | Current evidence | Status |
| --- | --- | --- |
| Preserve SQLite behavior | Suppression defaults disabled; full `go test -count=1 ./...` includes SQLite packages; docs keep SQLite as default/local workflow | Proven in repo |
| Backend selection for Postgres | Postgres uses `GONG_DATABASE_URL` / `DATABASE_URL` when `--db` is omitted; covered by prior Phase 1+ tests/smokes and docs | Proven in repo |
| Read-only MCP stays read-only | Postgres smokes include reader write-denial, raw-read denial, scoped function-grant validation, and startup fail-closed checks | Proven with synthetic smokes |
| Reviewed Postgres analyst surface | Commits `8113ab9`, `142d53b`, `6372564`, and `6380418` add analyst preset gating, scoped analyst reader, presence-backed dimensions, and small-cell suppression | Proven for reviewed catalog |
| No Postgres `all-readonly` | `postgresToolAllowlist` rejects `all-readonly`, `all-tools`, and `all`; smoke keeps an `all-readonly` rejection artifact | Proven in repo |
| Analyst aggregate privacy guard | Commit `6380418`; `docs/postgres-parity.md`; smoke artifact class proves singleton buckets suppressed and 1,200-call buckets remain visible | Proven as MCP-layer guard |
| Client pilot packet | Commit `01c5b19`; `docs/postgres-client-pilot-release-packet.md` | Complete |
| Client onboarding checklist | Commit `bb2477e`; `docs/postgres-client-onboarding-checklist.md` | Complete |
| Release notes coverage | `CHANGELOG.md` `0.4.0` lists business-workbench, GA acceptance, redaction audit, profile/readiness, and scoped Postgres evidence work | Complete |
| Synthetic capacity evidence | Load/capacity smokes produce reviewed evidence summaries and EXPLAIN artifacts at bounded synthetic sizes | Proven synthetically only |
| Customer-platform capacity/PITR | Packet/checklist require customer dry run on target Postgres service class | Not repo-proven |
| Published release/image digests | `VERSION` is prepared as `0.4.0`; tag/GHCR publish and digest verification are the remaining release actions | Pending tag |
| Native OAuth in `gongmcp` | Docs route OAuth through customer-managed broker; native OAuth remains future | Not done |
| Governed/RLS/materialized analyst aggregates | MCP-layer suppression exists; database-enforced governed aggregate variants remain queued | Not done |
| Full Postgres query parity | Many explicit slices are complete; broad `all-readonly` remains rejected | Not complete |

## Evidence Commands From Final Slices

Recent accepted gates:

```bash
go test -count=1 ./internal/mcp ./cmd/gongmcp
go test -count=1 ./...
go vet ./...
go run honnef.co/go/tools/cmd/staticcheck@v0.7.0 -checks=all,-U1000,-ST1000 ./...
make secret-scan
bash -n scripts/*.sh deploy/lab-auth/scripts/*.sh deploy/postgres/init/*.sh
docker compose -f docker-compose.postgres.yml config --quiet
git diff --check
```

Recent accepted Postgres smoke commands:

```bash
GONGCTL_POSTGRES_COMPOSE_PROJECT=gongctl-postgres-smoke-9x-r1 \
GONGCTL_POSTGRES_PORT=55620 \
./scripts/postgres-smoke.sh

GONGCTL_POSTGRES_LOAD_COMPOSE_PROJECT=gongctl-postgres-load-9x-r1 \
GONGCTL_POSTGRES_LOAD_PORT=55621 \
GONGCTL_POSTGRES_LOAD_CALLS=1200 \
GONGCTL_POSTGRES_LOAD_PROFILE_CACHE_ROWS=1200 \
./scripts/postgres-load-smoke.sh
```

Key smoke evidence classes:

- scoped analyst JSONL with `small_cell_suppression_applied`
- `all-readonly` rejection artifact
- read-only write-denial and raw-read-denial artifacts
- 1,200-call analyst dimensions retaining high-count buckets
- capacity/load summaries with synthetic-only caveats

## Current Client-Shareability Decision

Shareable after tag/digest publication:

- source review of this branch or the `v0.4.0` tag
- controlled customer-hosted Postgres business-workbench deployment planning
- synthetic evidence packet
- operator onboarding checklist
- `business-workbench` first user access
- reviewed analyst sessions only after scoped reader grant enforcement and
  sponsor approval

Do not share as done:

- production capacity benchmark
- customer-platform PITR/restore proof
- broad Postgres `all-readonly` parity
- native OAuth support inside `gongmcp`
- database-enforced governed analyst privacy model

## Next Required Decision

Before release publishing, complete the tagged-release path:

- tag `v0.4.0` from the reviewed commit
- let the publish-images workflow complete
- verify and record GHCR digests for `gongctl` and `gongmcp`
- run the GA acceptance smoke against the customer/lab deployment using the
  released image digest

Without tag, digest verification, and a released-image acceptance smoke, the
repo-side implementation is complete but the overall publish/release goal is
not complete.
