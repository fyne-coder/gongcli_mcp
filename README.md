# gongctl

`gongctl` turns approved Gong data into a local, queryable evidence workbench
for RevOps, sales, marketing, product, and governance teams. It helps teams
answer questions that are awkward to answer directly in Gong while keeping
credentials, cached data, and tenant-specific configuration under operator
control.

Use it to:

- ask governed business questions over bounded transcript evidence instead of
  pasting calls into a hosted assistant
- join call and transcript evidence with lifecycle buckets, scorecard
  configuration, selected CRM context, and local profile rules
- inspect coverage gaps before trusting AI summaries, coaching conclusions, or
  executive readouts
- expose a read-only MCP workbench to Claude Desktop, Cursor, Codex, or private
  HTTP pilots without giving those hosts Gong API credentials
- prototype ad-hoc analyses before converting useful patterns into Gong-native
  trackers, scorecards, themes, Gong AI prompts, or governed refresh jobs
- keep an open-source/local-first path where every company brings its own Gong
  credentials and controls where cached data lives

`gongctl` is unofficial, not affiliated with or endorsed by Gong. Source code is
open; every user brings their own Gong credentials and is responsible for
consent, data handling, and Gong terms.

**Start here:**

- **Business end-user** (you ask the assistant questions): →
  [Business-user quickstart](docs/business-user-quickstart.md)
- **Power analyst** (you design cohorts, profiles, tool sequences): →
  [Analyst orientation](docs/analyst-orientation.md)
- **IT / DevOps** (you install, secure, schedule, maintain): →
  [Quickstart](docs/quickstart.md), then
  [Enterprise deployment](docs/enterprise-deployment.md) and
  [Scheduling cache refreshes](docs/scheduling.md)
- **Security reviewer**: →
  [Security checklist](docs/security-checklist.md)
- **Anyone else / full doc index**: → [docs/README.md](docs/README.md)

Power users, analysts, and IT / DevOps teams will move faster with a coding
agent (Claude Code, Codex, Cursor, or similar) on hand to scaffold cron
units, K8s manifests, profile YAML edits, and analyst tool sequences from
the templates under `docs/` and `docs/examples/`. Do not paste real Gong
credentials, real customer profile YAML, or real transcript output into a
hosted agent unless your company has approved that data path.

## How It Is Packaged

Two binaries:

- **`gongctl`** — the CLI. Reads from Gong, writes to a local SQLite cache
  or a shared Postgres database. The only surface that handles Gong API
  credentials.
- **`gongmcp`** — read-only MCP server over that cache. Serves stdio
  (Claude Desktop, Cursor, …) or HTTP (private pilots). Never calls Gong
  directly.

Two cache backends:

- **SQLite** — default for local / single-host use. Full feature parity.
- **Postgres** — for shared multi-container deployments. Reviewed analyst,
  business, and governance surfaces; see
  [docs/postgres-parity.md](docs/postgres-parity.md) and
  [docs/postgres-question-parity.md](docs/postgres-question-parity.md).

## How It Fits With Gong

`gongctl` complements Gong rather than replacing it. Gong remains the system of
record for calls, transcripts, trackers, scorecards, and Gong-native AI
workflows.

The intended flow is: sync an approved local cache, test the business question
with bounded evidence, convert the useful pattern into Gong-native
configuration or a governed analysis workflow, then use Gong AI with a clearer
question, better tracker/theme inputs, and known coverage limits.

## Status

Early public release. `v0.4.0` is the first customer-hosted Postgres
`business-workbench` release candidate for operator-managed sync plus
read-only MCP over a reviewed SQLite cache or scoped Postgres serving database.
Public 1.0 still requires signed/provenance-backed release artifacts, stable
deprecation policy, and the remaining production hardening tracked in
[docs/roadmap.md](docs/roadmap.md).

Postgres analyst preset support and explicit allowlist additions called out
above require a tagged `v0.4.0` or later release before customer deployment.

For company evaluation, start with the enterprise pilot packet:

- [Developer orientation](docs/developer-orientation.md)
- [Quickstart](docs/quickstart.md)
- [Customer-hosted package](docs/customer-hosted-package.md)
- [Postgres client pilot release packet](docs/postgres-client-pilot-release-packet.md)
- [Postgres client onboarding checklist](docs/postgres-client-onboarding-checklist.md)
- [Postgres client manual-test checklist](docs/postgres-client-manual-test-checklist.md)
- [Postgres client deployment runbook](docs/runbooks/postgres-client-deployment.md)
- [Single-VM Postgres starter](deploy/single-vm-postgres/README.md)
- [Postgres question-parity matrix](docs/postgres-question-parity.md)
- [Data Boundary Statement](docs/data-boundary-statement.md)
- [Support model](docs/support.md)
- [Enterprise deployment](docs/enterprise-deployment.md)
- [Security model](docs/security-model.md)
- [Remote MCP auth and connector setup](docs/remote-mcp-auth.md)
- [Remote MCP OAuth troubleshooting](docs/runbooks/remote-mcp-oauth-troubleshooting.md)
- [Security questionnaire](docs/security-questionnaire.md)
- [MCP data exposure](docs/mcp-data-exposure.md)
- [Operator sync runbook](docs/runbooks/operator-sync.md)
- [Scheduling cache refreshes (cron / systemd / launchd / K8s)](docs/scheduling.md)
- [Configuration surfaces](docs/configuration-surfaces.md)
- [Analyst orientation](docs/analyst-orientation.md)
- [Business-user quickstart](docs/business-user-quickstart.md)
- [Pilot sponsor and operator guide](docs/pilot-sponsor-and-operator-guide.md)
- [Pilot plan](docs/pilot-plan.md)

Inspect what's available in your build:

```bash
gongmcp --list-tool-presets
gongctl profile schema
```

### MCP tool presets at a glance

`gongmcp --list-tool-presets` is the source of truth. Common presets:

| Preset | Surface | When to use |
|---|---|---|
| `business-workbench` | 6 stable facade tools (`gong_status`, `gong_discover_capabilities`, `gong_query`, `gong_analyze`, `gong_get_evidence`, `gong_explain_limitations`) | **Recommended default** for client business-user MCP hosts. Routes internally to reviewed analyst operations. Aliases: `analyst-facade`, `facade-analyst`. |
| `business-pilot` | Narrow status + aggregate tools (`get_sync_status`, `summarize_call_facts`, `summarize_calls_by_lifecycle`, `rank_transcript_backlog`) | Older first-pilot business-user lane. |
| `analyst-core` (Postgres) | Core call, CRM context, profile, lifecycle, transcript-search tools (incl. cached CRM schema / settings inventory, scorecard inventory + activity aggregates) | First analyst surface on Postgres reader role. |
| `analyst-business-core` (Postgres) | `analyst-core` + bounded transcript-evidence and business-analysis tools | Quote packs, theme intelligence, pipeline outcomes against Postgres. |
| `analyst` (Postgres) | Full reviewed analyst catalog | Approved analyst sessions over the reviewed Postgres surface. |
| `governance-search` (Postgres) | Narrowed governance-search slice | AI governance prepared private policy. |
| `all-readonly` (SQLite) | Full read-only catalog | Trusted SQLite admin / analyst sessions. |
| `redacted-all-readonly` (Postgres) | Broad surface over a **physically redacted** serving DB with scoped reader grants | Internal manual testing only. Call titles are visible by default unless policy suppresses them; explicit include flags can expose raw call IDs from the redacted DB. |
| `broad-public-redacted` (Postgres) | Same surface as `redacted-all-readonly` with a hardened customer-deployment startup contract | Customer-test broad surface over a redacted serving DB. |

For ad-hoc business questions, prefer `business-workbench` and call
`gong_analyze` operation `question.answer`. It returns a governed evidence
pack (interpreted question, scope, coverage, bounded evidence/quotes,
limitations, follow-ups, per-call duration); the host model synthesizes the
final answer. Call titles are shown by default in title-bearing MCP surfaces
when the backend has a title and policy allows it. Suppress them with
`field_profile=limited` for a request or `hide_call_titles` in
`GONGMCP_POLICY_SWITCHES` / `--policy-switches` at launch. This is a
launch/runtime policy, not an AI governance YAML toggle; see
[MCP data exposure](docs/mcp-data-exposure.md#call-title-exposure).

Postgres explicit-allowlist tools (require `v0.4.0`+ for customer
deployment): `list_unmapped_crm_fields`, `search_crm_field_values`,
`analyze_late_stage_crm_signals`, `opportunities_missing_transcripts`,
`opportunity_call_summary`, `crm_field_population_matrix` (approved
object/field pairs only — see [docs/mcp-data-exposure.md](docs/mcp-data-exposure.md)),
`compare_lifecycle_crm_fields` (Opportunity), `search_transcripts_by_crm_context`
(default-redacted snippet search), `missing_transcripts` (admin
transcript-backfill).

For per-tool exposure detail, see
[docs/mcp-data-exposure.md](docs/mcp-data-exposure.md). For analyst tool
selection by analytic intent, see
[docs/analyst-tool-reference.md](docs/analyst-tool-reference.md).

- Deployment worksheet: [Customer implementation checklist](docs/implementation-checklist.md)
- Local/container start: [Quickstart](docs/quickstart.md)
- HTTPS/auth boundary: [Remote MCP auth and connector setup](docs/remote-mcp-auth.md)
- Remote connector failures: [Remote MCP OAuth troubleshooting](docs/runbooks/remote-mcp-oauth-troubleshooting.md)
- Image digest verification: [Release process](docs/release.md)
- RevOps profile setup: [Business profiles](docs/profiles.md)

For support intake, generate the local/offline diagnostic bundle before
sharing logs or payloads:

```bash
gongctl support bundle --db "$HOME/gongctl-data/gong-mcp-governed.db" --out "$HOME/gongctl-data/support-bundle"
# Postgres shared deployment:
GONG_DATABASE_URL="$GONGMCP_READER_DATABASE_URL" gongctl support bundle --out "$HOME/gongctl-data/support-bundle"
```

The command opens the configured cache read-only, does not need Gong
credentials, and does not make network calls. SQLite uses `--db PATH`;
Postgres uses `GONG_DATABASE_URL` or `DATABASE_URL` and does not export the
database URL. The `--out` directory must be new or empty so stale files are not
included by accident. See [Support model](docs/support.md) for what the bundle
includes and excludes.

Current pilot hardening in this worktree includes a restricted/company CLI mode
for high-risk commands. Enable it with `GONGCTL_RESTRICTED=1` or
`gongctl --restricted ...`, then use `--allow-sensitive-export` or
`GONGCTL_ALLOW_SENSITIVE_EXPORT=1` only for approved operator actions.

Current defaults are based on Gong public help-center guidance available on 2026-04-24:

- Gong API supports retrieving analyzed call data, call transcripts, users, activity, and related stats.
- Public limits are documented as 3 API calls per second and 10,000 calls per day; `429` responses include `Retry-After`.
- Customer/admin API credentials use access key and access key secret as HTTP Basic auth.
- Gong Collective-style apps should use OAuth; Gong notes OAuth is global/account-level rather than user-level.

Primary references:

- [What the Gong API provides](https://help.gong.io/docs/what-the-gong-api-provides)
- [Receive access to the API](https://help.gong.io/docs/receive-access-to-the-api)
- [Create an OAuth app for Gong](https://help.gong.io/docs/create-an-app-for-gong)
- [Gong Collective developer terms](https://www.gong.io/legal/gong-collective-developer-terms)

## Install From Source

This repo requires Go 1.26.3 or newer for local builds and release artifacts.

```bash
go test -count=1 ./...
go build -o bin/gongctl ./cmd/gongctl
```

Or use the Makefile to inject local version metadata:

```bash
make build
bin/gongctl version
```

## Versioning

Release versions follow SemVer-style `vX.Y.Z` tags. The next release version lives in [VERSION](VERSION), and release changes are summarized in [CHANGELOG.md](CHANGELOG.md).

GoReleaser and Docker builds inject `version`, `commit`, and `date` into both `gongctl` and `gongmcp`. See [docs/release.md](docs/release.md) for the release candidate flow.

## Run With Docker

For the fastest end-to-end path, use the [Quickstart](docs/quickstart.md).

For shared deployments where the sync job and `gongmcp` run in separate
containers without a shared filesystem, use Postgres instead of a mounted
SQLite cache. See [Docker Deployment](docs/docker.md#postgres-shared-deployment)
and [Enterprise Deployment](docs/enterprise-deployment.md#2b-postgres-shared-container-deployment).

Use the published GHCR images after a release is published:

```bash
docker run --rm ghcr.io/fyne-coder/gongcli_mcp/gongctl:v0.4.5 version
docker run --rm -v "$HOME/gongctl-data:/data" ghcr.io/fyne-coder/gongcli_mcp/gongctl:v0.4.5 sync status --db /data/gong.db
```

The `v0.4.5` image references require the `v0.4.5` tag workflow to have
completed successfully. If the GHCR manifest is not available yet, build and
use the local images below.

For read-only MCP, use the MCP-only image:

```bash
docker run --rm -i --network none -v "$HOME/gongctl-data:/data:ro" ghcr.io/fyne-coder/gongcli_mcp/gongmcp:v0.4.5 --db /data/gong.db --tool-preset business-pilot
```

Build the local image:

```bash
docker build -t gongctl:local .
```

Run CLI commands with credentials and a mounted data directory:

```bash
docker run --rm --env-file .env -v "$HOME/gongctl-data:/data" gongctl:local sync status --db /data/gong.db
```

Run the read-only stdio MCP server from the MCP-only image:

```bash
docker build --target mcp -t gongctl:mcp-local .
docker run --rm -i --network none -v "$HOME/gongctl-data:/data:ro" gongctl:mcp-local --db /data/gong.db --tool-preset business-pilot
```

Generate or install a Claude Desktop stdio MCP config entry:

```bash
scripts/install-claude-stdio-mcp.sh --db "$HOME/gongctl-data/gong.db"
scripts/install-claude-stdio-mcp.sh --db "$HOME/gongctl-data/gong.db" --install
```

Run the real-data Docker smoke after credentials are configured:

```bash
scripts/docker-smoke.sh
```

See [docs/docker.md](docs/docker.md) for Compose usage, MCP host configuration, and OCI registry deployment notes.

Run the synthetic Postgres shared-deployment smoke:

```bash
scripts/postgres-smoke.sh
```

The smoke uses synthetic data only. It initializes Postgres, writes the
synthetic cache through `gongctl sync synthetic`, checks Postgres read-model
state with `gongctl sync read-model`, validates cached CRM schema/settings and
scorecard inventory through the read-only role, starts `gongmcp`, calls the
supported MCP tools, and proves the reader role cannot write or directly read
raw/sensitive tables. It also prepares a synthetic Postgres AI governance
policy and proves restricted synthetic records are absent from governed MCP
search outputs.
The smoke also creates a synthetic `business-pilot` scoped reader role, proves
the selected business-pilot MCP calls work, denies direct reads of
`calls.call_id`, `calls.title`, `call_facts.call_id`, and `call_facts.title`,
verifies startup fails on an extra direct column grant, and applies that role
from the generated `gongctl mcp postgres-reader-sql` SQL artifact.

Compose requires `GONGCTL_DATA_DIR` to point at an external data directory so customer SQLite/transcript data does not land under the source checkout.

For a simple company-managed VM where Postgres, operator jobs, serving-DB
refresh, scoped grants, and read-only HTTP `gongmcp` all run on one host, use
the [Single-VM Postgres starter](deploy/single-vm-postgres/README.md). It is
the practical small-IT setup path; the root `docker-compose.postgres.yml`
remains the synthetic/dev smoke shape.

## Configure

Use environment variables for the MVP:

```bash
export GONG_ACCESS_KEY="..."
export GONG_ACCESS_KEY_SECRET="..."
export GONG_BASE_URL="https://api.gong.io"
```

Or copy `.env.example` to `.env` and fill in the same keys. `.env` is gitignored and loaded from the current working directory; exported environment variables take precedence over `.env` values.

`GONG_BASE_URL` is optional and defaults to `https://api.gong.io`. OAuth customer installs may need to use the customer-specific API base URL returned by Gong.

Keep real SQLite databases, transcript files, and tenant profile YAML outside the source repo, for example under `~/gongctl-data/`. The repo ignores `data/` as a last-resort safety net, but public docs use an external path so copy/paste examples do not encourage committing tenant data.

## Restricted Company Mode

Use restricted mode for company-managed CLI runs:

```bash
export GONGCTL_RESTRICTED=1
```

Or prefix an individual command:

```bash
gongctl --restricted sync status --db ~/gongctl-data/gong.db
```

When restricted mode is enabled, these commands are blocked unless you add
`--allow-sensitive-export` or set `GONGCTL_ALLOW_SENSITIVE_EXPORT=1`:

- `gongctl api raw ...`
- `gongctl calls show --db PATH --json` for raw SQLite cached call JSON
- `gongctl calls list --context extended ...`
- `gongctl calls export ...`
- `gongctl calls transcript ...`
- `gongctl calls transcript-batch ...`
- `gongctl sync transcripts ...`
- `gongctl sync calls --preset business`
- `gongctl sync calls --preset all`
- `gongctl sync calls --include-parties`
- `gongctl sync calls --include-highlights`

Approved override example:

```bash
GONGCTL_RESTRICTED=1 gongctl calls list --from 2026-04-01 --to 2026-04-24 --context extended --allow-sensitive-export --out calls-with-crm-context.json
GONGCTL_RESTRICTED=1 gongctl sync calls --db ~/gongctl-data/gong.db --from 2026-04-01 --to 2026-04-24 --preset business --allow-sensitive-export
GONGCTL_RESTRICTED=1 gongctl sync calls --db ~/gongctl-data/gong.db --from 2026-04-01 --to 2026-04-24 --preset minimal --include-parties --allow-sensitive-export
GONGCTL_RESTRICTED=1 gongctl sync calls --db ~/gongctl-data/gong.db --from 2026-04-01 --to 2026-04-24 --preset minimal --include-highlights --allow-sensitive-export
```

## Public Quickstart Shape

Use [docs/quickstart.md](docs/quickstart.md) for the copy/paste Docker path
from credentials to local MCP host config. The public path stays local:
authenticate, sync a local SQLite cache, inspect readiness, search bounded
snippets, and optionally expose the cache through the read-only MCP server.

```bash
gongctl auth check
gongctl sync calls --db ~/gongctl-data/gong.db --from 2026-04-01 --to 2026-04-24 --preset business
gongctl sync users --db ~/gongctl-data/gong.db
gongctl sync transcripts --db ~/gongctl-data/gong.db --out-dir ~/gongctl-data/transcripts --limit 50 --batch-size 100
gongctl sync status --db ~/gongctl-data/gong.db
gongctl analyze coverage --db ~/gongctl-data/gong.db
gongctl search transcripts --db ~/gongctl-data/gong.db --query "pricing objection" --limit 10
gongctl mcp tools
gongmcp --db ~/gongctl-data/gong.db --tool-preset business-pilot
gongmcp --list-tool-presets
gongctl diagnose
```

For repeatable operator refreshes, stage the approved steps in YAML and dry-run
them before the scheduled job uses the same file:

```bash
gongctl sync run --config testdata/fixtures/sync-run-minimal.yaml --dry-run
gongctl cache inventory --db ~/gongctl-data/gong.db
GONG_DATABASE_URL="$GONGMCP_READER_DATABASE_URL" gongctl cache inventory
gongctl cache purge --db ~/gongctl-data/gong.db --older-than 2026-04-01 --dry-run
GONG_DATABASE_URL="$GONGMCP_READER_DATABASE_URL" gongctl cache purge --older-than 2026-04-01
GONG_DATABASE_URL="$GONGCTL_WRITER_DATABASE_URL" gongctl cache purge --older-than 2026-04-01 --confirm
GONG_DATABASE_URL="$GONGMCP_READER_DATABASE_URL" gongctl cache purge --config ~/gongctl-data/retention-policy.yaml --dry-run
GONG_DATABASE_URL="$GONGCTL_WRITER_DATABASE_URL" gongctl cache purge --config ~/gongctl-data/retention-policy.yaml --confirm
```

## Advanced Local Operator Commands

These commands are useful when an operator is working against their own tenant data on their own machine. They can reveal raw call JSON, transcript files, CRM context, profile-derived field values, or tenant-specific configuration, so keep outputs outside the source repo and do not use them in public examples.
When using Postgres, `gongctl search calls` and `gongctl calls show --json`
return minimized read-model metadata. Use explicit SQLite/operator raw-export
commands such as `calls export` with `--allow-sensitive-export` for
operator-controlled raw payload access.
With `GONG_DATABASE_URL` set, `gongctl profile discover/validate/import/show`
can operate on the shared Postgres cache when `--db` is omitted; keep generated
profile YAML outside git because discovery evidence can contain tenant CRM
field names and values.

```bash
gongctl sync crm-integrations --db ~/gongctl-data/gong.db
gongctl sync crm-schema --db ~/gongctl-data/gong.db --integration-id CRM_INTEGRATION_ID --object-type ACCOUNT --object-type DEAL
gongctl sync settings --db ~/gongctl-data/gong.db --kind trackers
gongctl sync scorecard-activity --db ~/gongctl-data/gong.db --call-from 2026-01-01 --call-to 2026-04-01 --review-method BOTH
gongctl sync run --config ~/gongctl-data/company-sync.yaml --dry-run
gongctl sync run --config ~/gongctl-data/company-sync.yaml
gongctl sync status --db ~/gongctl-data/gong.db
gongctl cache inventory --db ~/gongctl-data/gong.db
gongctl cache purge --db ~/gongctl-data/gong.db --older-than 2026-04-01 --dry-run
GONG_DATABASE_URL="$GONGMCP_READER_DATABASE_URL" gongctl cache purge --older-than 2026-04-01
GONG_DATABASE_URL="$GONGCTL_WRITER_DATABASE_URL" gongctl cache purge --older-than 2026-04-01 --confirm
GONG_DATABASE_URL="$GONGMCP_READER_DATABASE_URL" gongctl cache purge --config ~/gongctl-data/retention-policy.yaml --dry-run
GONG_DATABASE_URL="$GONGCTL_WRITER_DATABASE_URL" gongctl cache purge --config ~/gongctl-data/retention-policy.yaml --confirm
gongctl profile discover --db ~/gongctl-data/gong.db --out ~/gongctl-data/gongctl-profile.yaml
gongctl profile validate --db ~/gongctl-data/gong.db --profile ~/gongctl-data/gongctl-profile.yaml
gongctl profile import --db ~/gongctl-data/gong.db --profile ~/gongctl-data/gongctl-profile.yaml
gongctl profile show --db ~/gongctl-data/gong.db
gongctl analyze calls --db ~/gongctl-data/gong.db --group-by lifecycle
gongctl analyze calls --db ~/gongctl-data/gong.db --group-by deal_stage --lifecycle-source profile
gongctl analyze crm-schema --db ~/gongctl-data/gong.db --object-type DEAL
gongctl analyze settings --db ~/gongctl-data/gong.db --kind trackers
gongctl analyze scorecards --db ~/gongctl-data/gong.db
gongctl analyze scorecard --db ~/gongctl-data/gong.db --scorecard-id SCORECARD_ID
gongctl analyze scorecard-activity --db ~/gongctl-data/gong.db --group-by review_method
gongctl analyze transcript-backlog --db ~/gongctl-data/gong.db --limit 25
gongctl mcp tool-info list_gong_settings
gongctl search calls --db ~/gongctl-data/gong.db --crm-object-type Opportunity --crm-object-id opp_001 --limit 10
gongctl calls show --db ~/gongctl-data/gong.db --call-id CALL_ID --json
# With GONG_DATABASE_URL set, Postgres returns minimized call detail without --db.
gongctl calls show --call-id CALL_ID --json
gongctl sync crm-integrations
gongctl sync crm-schema --integration-id CRM_INTEGRATION_ID --object-type Opportunity
gongctl sync settings --kind scorecards
gongctl analyze crm-schema --integration-id CRM_INTEGRATION_ID --object-type Opportunity
gongctl analyze scorecards
gongctl analyze scorecard --scorecard-id SCORECARD_ID
gongctl calls list --from 2026-04-01 --to 2026-04-24 --json
gongctl calls list --from 2026-04-01 --to 2026-04-24 --context extended --out calls-with-crm-context.json --allow-sensitive-export
gongctl calls export --from 2026-04-01 --to 2026-04-24 --out calls.jsonl
gongctl calls export --from 2026-04-01 --to 2026-04-24 --context extended --out calls.jsonl --max-pages 2
gongctl calls transcript --call-id CALL_ID --out transcript.json
gongctl calls transcript-batch --ids-file call_ids.txt --out-dir transcripts --resume
gongctl users list
gongctl api raw POST /v2/calls/transcript --body body.json
```

The typed commands are intentionally thin wrappers over `internal/gong.Client`. If Gong changes an endpoint contract, the fallback is `gongctl api raw ...` while the typed wrapper is updated.

In company mode, run these commands with `GONGCTL_RESTRICTED=1` and add
`--allow-sensitive-export` only when the operator has explicit approval for the
extra data exposure. `sync run --config` does not support a per-step
`allow_sensitive_export` YAML bypass; restricted-mode approval must be supplied
at runtime with the global flag or environment variable so reviewed schedules
cannot silently self-authorize sensitive steps.

`calls export` follows Gong's `records.cursor` pagination and drains all pages by default. Use `--max-pages N` for bounded smoke tests.

CRM/account/opportunity context is not requested by default because it can include customer CRM values. Use `--context extended` on `calls list` or `calls export` when those fields are intentionally needed.

## SQLite Sync/Search Flow

The local CLI sync/search flow is SQLite-backed:

1. `gongctl sync calls --db PATH --from DATE --to DATE --preset ...`
2. `gongctl sync users --db PATH`
3. `gongctl sync transcripts --db PATH --out-dir PATH`
4. `gongctl sync crm-integrations --db PATH`
5. `gongctl sync crm-schema --db PATH --integration-id ID --object-type TYPE`
6. `gongctl sync settings --db PATH --kind trackers|scorecards|workspaces`
7. `gongctl sync scorecard-activity --db PATH --call-from DATE --call-to DATE [--review-method AUTOMATIC|MANUAL|BOTH]`
8. `gongctl sync run --config PATH [--dry-run]`
9. `gongctl sync status --db PATH`
10. `gongctl cache inventory --db PATH` for SQLite, or omit `--db` with `GONG_DATABASE_URL` / `DATABASE_URL` for Postgres
11. `gongctl cache purge --db PATH --older-than YYYY-MM-DD [--dry-run|--confirm]`, or omit `--db` with `GONG_DATABASE_URL` / `DATABASE_URL` for Postgres; scheduled retention jobs can use `gongctl cache purge --config PATH [--dry-run|--confirm]`
12. `gongctl profile discover --db PATH --out PATH`
13. Review and edit the YAML profile for tenant-specific CRM objects, fields, lifecycle buckets, and methodology concepts.
14. `gongctl profile validate --db PATH --profile PATH`
15. `gongctl profile import --db PATH --profile PATH`
16. `gongctl analyze calls --db PATH --group-by DIMENSION [--lifecycle-source auto|profile|builtin]`
17. `gongctl analyze coverage --db PATH [--lifecycle-source auto|profile|builtin]`
18. `gongctl analyze crm-schema --db PATH [--integration-id ID] [--object-type TYPE]`
19. `gongctl analyze settings --db PATH [--kind KIND]`
20. `gongctl analyze scorecard-activity --db PATH [--group-by scorecard|review_method|reviewed_user|lifecycle|transcript_status]`
21. `gongctl analyze transcript-backlog --db PATH [--lifecycle-source auto|profile|builtin]`
22. `gongctl search transcripts --db PATH --query TEXT`
23. `gongctl search calls --db PATH [--crm-object-type TYPE] [--crm-object-id ID]`
24. `gongctl calls show --db PATH --call-id ID --json`

Rules:

- `--db` is required for all SQLite-backed `sync`, `search`, and `calls show` commands. With `GONG_DATABASE_URL` or `DATABASE_URL`, supported Postgres read-only commands use the shared database instead.
- `sync calls --preset business` requests Gong `Extended` context.
- `sync calls --preset minimal` does not request Gong context.
- `sync calls --preset all` currently maps to `Extended` context as well; it is documented separately so it can diverge later without changing the CLI shape.
- `sync calls --include-parties` requests Gong call participant fields such as names, emails, speaker IDs, and titles. Use it only for approved operator refreshes because returned participant payloads are cached in raw call JSON.
- `sync calls --include-highlights` requests Gong AI Highlights / brief /
  next-step fields. Use it only for approved operator refreshes because
  returned summary/next-step text is cached in raw call JSON. No MCP tool reads
  these fields until a typed redacted read model is added.
- `sync crm-integrations` caches Gong CRM integration IDs needed by
  `sync crm-schema`. With `GONG_DATABASE_URL` set and `--db` omitted, this
  inventory can be written to Postgres for shared deployments.
- `sync crm-schema` caches selected CRM field metadata by integration/object
  type; it stores field names and labels, not CRM field values. With
  `GONG_DATABASE_URL` set and `--db` omitted, this metadata can be written to
  Postgres and read through `analyze crm-schema` or the reviewed MCP inventory
  tools.
- `sync settings` caches read-only Gong inventory for trackers, scorecards, and workspaces. With `GONG_DATABASE_URL` set and `--db` omitted, this cache can be written to Postgres for shared deployments.
- `sync scorecard-activity` caches answered scorecard activity from Gong's
  scorecard activity stats endpoint. It stores local raw JSON for operator
  audit, but public summaries should use aggregate analyze/MCP surfaces. With
  `GONG_DATABASE_URL` set and `--db` omitted, supported writes use Postgres for
  shared deployments.
- `sync run --config PATH` executes a staged refresh plan from YAML. Relative
  `db` and `out_dir` paths resolve from the config file location, and
  `--dry-run` validates the file without calling Gong. Supported stages include
  calls, users, transcripts, CRM integrations/schema, settings, and
  scorecard-activity. Sensitive transcript and extended-context steps are
  flagged in dry-run output, but runtime approval is still required through
  `--allow-sensitive-export` or
  `GONGCTL_ALLOW_SENSITIVE_EXPORT=1` when restricted mode is enabled.
- `sync transcripts` selects calls that do not already have cached transcripts, batches missing call IDs into Gong transcript requests, and writes one normalized transcript JSON file per returned call transcript. The default `--batch-size` is 100, and the CLI caps it at 100.
- Existing cached transcripts are skipped by `sync transcripts`; rerun `sync calls` to refresh call metadata and embedded CRM context. A transcript refresh policy for re-checking already downloaded transcripts is planned separately.
- `sync status` separates embedded CRM context from CRM integration/schema inventory. A cache can contain CRM context from `sync calls --preset business` even when `sync crm-integrations` or `sync crm-schema` has not populated inventory tables.
- `sync status` also returns public business-readiness flags for conversation volume, transcript coverage, scorecard/theme inventory, lifecycle separation, CRM segmentation, and attribution readiness.
- `cache inventory --db PATH` opens SQLite read-only and reports file size,
  primary table counts, call date range, transcript and CRM-context presence,
  profile status, and last sync metadata with a sensitive-data warning. With
  `GONG_DATABASE_URL` or `DATABASE_URL` and no `--db`, it opens Postgres
  read-only, reports safe table/version/readiness diagnostics, and never
  exports the database URL.
- `cache purge --db PATH --older-than YYYY-MM-DD` is dry-run by default for
  SQLite. Omit `--db` and set `GONG_DATABASE_URL` or `DATABASE_URL` for
  Postgres. A Postgres reader URL can preview metadata-only counts through the
  read-only role; confirmed Postgres purges require a writable URL and delete
  matching calls plus dependent transcript, CRM-context, read-model, profile
  fact-cache, scorecard-activity, and governance-suppression rows. SQLite
  confirmed purges additionally enable `secure_delete`, checkpoint/truncate WAL
  state, and run `VACUUM`. Postgres WAL, replicas, snapshots, backups, transcript
  files, sync history, profiles, CRM schema inventory, and settings inventory
  remain operator-owned retention surfaces. Postgres keeps call-ID tombstones as
  operational metadata to prevent later sync steps from accidentally
  rehydrating purged call-scoped rows; add `--confirm` only after backup,
  retention, and legal-hold checks are complete.
- `cache purge --config PATH` reads a retention-policy YAML file for scheduled
  purge jobs. The policy records `version: 1`, `older_than`, and approval
  metadata for the change reference, approver, approval date, data owner, backup
  reference, and legal-hold review. Dry-run output includes the policy SHA-256
  and sanitized metadata, while confirmed config-driven purge fails closed until
  required approval fields are present and `approval.approved_at` is a
  non-future `YYYY-MM-DD` date. The YAML does not install a scheduler or
  self-authorize deletion; `--confirm` and the writable URL are still required.
- `profile discover` generates an editable YAML profile from cached CRM inventory and includes confidence plus evidence for discovered mappings. Discovery is an English-biased starter and may include CRM evidence values in the YAML, so write real-tenant output to a local file outside git rather than shared logs.
- Discovered profiles are starter drafts, not universal truth. A human should review tenant lifecycle, object, field, and methodology mappings before relying on profile-aware separation of sales and post-sales calls.
- `profile validate` reports malformed YAML, unsupported profile versions,
  unsupported rule operators, unsafe regex rules, missing required lifecycle
  buckets, and mapped fields that do not exist in cached CRM data. It writes a
  JSON validation report; for semantic validity gates, inspect the report's
  `valid` field. `profile import` is the command that refuses `valid:false`
  profiles before writing.
- `profile import` stores the active profile in SQLite or Postgres in one transaction. Re-importing identical profile content is a no-op; changed source metadata for the same canonical profile updates metadata without changing profile meaning.
- Profile-aware analysis uses a materialized read model keyed by active profile
  and canonical hash. Writable SQLite/Postgres CLI sync/profile commands rebuild
  or warm it; read-only MCP requires that cache to be current and reports a
  stale-cache error instead of writing to the cache.
- Profile rules are a closed Go-evaluated grammar: `equals`, `in`, `prefix`, `iprefix`, `regex`, `is_set`, and `is_empty`. Profiles do not run SQL, templates, JSONPath, JMESPath, or arbitrary expressions.
- `analyze scorecards` and `analyze scorecard` expose scorecard names and question text from cached settings without returning raw settings payloads. With `GONG_DATABASE_URL` set and `--db` omitted, these read from Postgres through the read-only role.
- `analyze scorecards` is scorecard inventory. `analyze scorecard-activity` is
  answered-scorecard activity and supports grouping by scorecard, review
  method, reviewed user, lifecycle, or transcript status. With
  `GONG_DATABASE_URL` set and `--db` omitted, Postgres reads use the read-only
  role and aggregate functions instead of raw table access; the Postgres
  read-only path rejects `reviewed_user` grouping so it does not emit user IDs.
- After upgrading to a build with new SQLite migrations, run a writable `gongctl sync ...` command before starting `gongmcp`; read-only MCP refuses older schema versions and tells the operator to run a sync command first.
- `sync` commands write concise progress summaries to `stderr`.
- `sync status`, `search ...`, and `calls show --json` write JSON to `stdout`. Postgres `calls show --json` returns minimized call detail rather than raw cached JSON.
- `analyze ...` commands write metadata-only JSON summaries to `stdout`.
- `search transcripts` returns segment metadata and snippets only; it does not emit full segment text.
- `search calls` CRM filters only match rows that were synced with stored CRM context, so use `business` or `all` when those searches are needed.
- `analyze calls` groups the normalized `call_facts` view by safe dimensions such as `lifecycle`, `opportunity_stage`, `account_industry`, `scope`, `system`, `direction`, `transcript_status`, `duration_bucket`, `month`, `lead_source`, and `forecast_category`.
- `analyze coverage` includes lifecycle, scope, system, and direction summaries so transcript coverage gaps can be understood by conversation type.
- `analyze transcript-backlog` prioritizes External and Conference-style customer conversations ahead of short dialer-style events by default.
- With an active profile, profile-aware analysis defaults to `lifecycle_source=profile`; use `--lifecycle-source builtin` to force the compatibility lifecycle/read model. In Postgres, `auto` falls back to builtin only when no active profile exists; when a profile is active, read-only Postgres requires a fresh profile fact cache and fails closed until a writable `gongctl sync read-model --rebuild`, `profile import`, or `profile activate` warms it.

Public examples should avoid tenant field names, customer names, raw CRM values, transcripts, call titles, object IDs, and call IDs. See [docs/business-user-quickstart.md](docs/business-user-quickstart.md) for prompt examples shaped for sales, marketing, enablement, and RevOps users.

## Deterministic AI Exclusion Filtering

For companies with customer-specific AI-use restrictions, keep the real
restricted-customer list in a private YAML file outside Git and load it with
`gongmcp --ai-governance-config PATH` or `GONGMCP_AI_GOVERNANCE_CONFIG`.
`gongctl governance audit --db PATH --config PATH` verifies deterministic
SQLite name/alias matches before MCP use. For Postgres, omit `--db`, set a
writable `GONG_DATABASE_URL`, and run `gongctl governance audit --config PATH
--apply-postgres-policy` before starting read-only `gongmcp`. This is a local
exclusion filter, not legal consent management or contractual enforcement. See
[docs/ai-governance.md](docs/ai-governance.md); the tracked example config uses
synthetic names only. For MCP/LLM use, default to a physically filtered MCP DB:

```bash
gongctl governance export-filtered-db --db ~/gongctl-data/gong.db --config ~/gongctl-data/ai-governance.yaml --out ~/gongctl-data/gong-mcp-governed.db
gongmcp --db ~/gongctl-data/gong-mcp-governed.db --tool-preset business-pilot
```

Raw-DB governance remains available as a fallback. SQLite computes suppression at
`gongmcp` startup. Postgres requires a previously prepared policy because the
MCP container should use the read-only role and should not receive raw candidate
value access. In both modes, `gongmcp` requires an explicit
governance-compatible `--tool-preset`, `--tool-allowlist`,
`GONGMCP_TOOL_PRESET`, or `GONGMCP_TOOL_ALLOWLIST`, and unsupported
aggregate/config tools are refused.

## Local MCP (stdio)

`gongmcp --db PATH` serves a read-only stdio MCP adapter over the local SQLite cache. For reviewed Postgres shared-deployment surfaces, omit `--db` and set `GONG_DATABASE_URL` or `DATABASE_URL` to a read-only database role.
Use `gongctl mcp tools` or `gongctl mcp tool-info NAME` to inspect the local MCP tool catalog without starting a host app.

For SQLite stdio, `gongmcp` exposes the full read-only MCP catalog by default
to any connected host. Company pilots should narrow that surface with
`gongmcp --tool-preset` or `--tool-allowlist`, then layer host and filesystem
controls around the approved business-user subset. Trusted single-user analyst
workstations may use `--tool-preset all-readonly` or intentionally skip the
allowlist in stdio mode and turn on per-tool opt-ins
(`include_call_ids`, `include_speaker_ids`, `include_value_snippets`) for
deeper, identifier-bearing questions; see
[docs/mcp-data-exposure.md](docs/mcp-data-exposure.md) for the trade-off and
[docs/mcp-data-exposure.md#mcp-call-volume-and-limits](docs/mcp-data-exposure.md#mcp-call-volume-and-limits)
for configurable row caps, cap-hit feedback, and filter-first guidance that
keeps MCP traffic from overwhelming the host context window.

For Postgres stdio, the no-allowlist default is the bounded shared-deployment
surface (`get_sync_status`, `search_calls`, and `search_transcript_segments`).
Broader Postgres tools require a supported preset such as `business-pilot`,
`analyst-core`, `analyst-business-core`, or `analyst`, or an explicit
`GONGMCP_TOOL_ALLOWLIST`; `all-readonly` remains rejected until full-catalog
query parity is complete.
The default Postgres reader contract preserves the broad `gongmcp_reader`
service role for compatibility. Operators who create a narrower function-scoped
role for a specific preset or allowlist can start `gongmcp` with
`--enforce-tool-scoped-db-grants` or `GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS=1`;
startup then requires exactly the reviewed `gongmcp_*` function grants and
reviewed table/column grants for the selected scoped surface and fails closed on
extra function grants such as admin-only `missing_transcripts` helpers outside
the selected preset. The reviewed scoped surfaces are currently
`business-pilot` and `analyst`. They deny direct base-table identifiers such as
`calls.call_id`, `calls.title`, `call_facts.call_id`, and `call_facts.title`;
the analyst surface also grants sanitized business-analysis wrapper functions
instead of the raw identifier-bearing SQL helpers. The scoped reader URL is
still a service secret because selected functions and sanitized views can expose
minimized call metadata, snippets, timings, counts, and tenant terminology.
To generate a reviewed grant block without copying smoke script internals, use
the `gongctl` operator command. The `gongmcp` flag is a compatibility path for
MCP-only images:

```bash
gongctl mcp postgres-reader-sql --preset business-pilot \
  --role gongmcp_business_pilot_reader --database gongctl

gongctl mcp postgres-reader-apply --preset business-pilot \
  --role gongmcp_business_pilot_reader --database gongctl --dry-run

GONG_DATABASE_URL="$WRITER_URL" \
  gongctl mcp postgres-reader-apply --preset business-pilot \
  --role gongmcp_business_pilot_reader --database gongctl --apply

gongmcp --print-postgres-reader-grants \
  --tool-preset business-pilot \
  --postgres-reader-role gongmcp_business_pilot_reader \
  --postgres-database gongctl
```

Use `--preset analyst` and a separate role such as
`gongmcp_analyst_reader` for approved analyst sessions that need the broader
reviewed catalog.

The generated SQL and apply JSON do not create credentials or print database
URLs. Create a standalone `LOGIN NOINHERIT` role and password through your
normal secret management process; do not grant that role to/from other roles.
Then dry-run/review the grant block and use `--apply` with a writable operator
URL to reconcile grants for that existing role. The apply command is safe to
rerun to clear stale current public table/function grants and regrant the
reviewed surface, and it validates the role posture plus final effective grants
for current public objects. It also rejects effective sequence grants after
apply. It is not a password or role-creation manager and it cannot clear
default privileges created by other grantors for future objects. Avoid default
grants to this scoped service role or to `PUBLIC`; MCP startup detects explicit
default privileges targeting the role or `PUBLIC`, but PostgreSQL still grants
EXECUTE on newly-created functions to `PUBLIC` by default, so keep public
function grant drift checks and MCP startup privilege enforcement enabled. Run
`gongmcp` with the scoped reader URL and
`GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS=1`. This is a `gongmcp`
service credential, not an analyst SQL login. The scoped active-profile and
profile-cache helpers redact source metadata and call IDs/titles, and scoped
analyst business-analysis helpers return redacted call references/titles through
sanitized SQL wrappers, but selected functions still expose minimized
operational metadata, snippets, timings, counts, and tenant terminology. MCP
result limits are still enforced above those SQL helpers. The scoped
`business-pilot` role is profile-backed: warm/import an active profile and use
the default/profile lifecycle path. Explicit `lifecycle_source=builtin` still
requires the broader compatibility reader role until a sanitized builtin SQL
surface exists.

For approved analyst sessions, the full cohort workflow is documented
in [Pilot sponsor and operator guide](docs/pilot-sponsor-and-operator-guide.md#analyst-cohort-workflow),
[MCP data exposure](docs/mcp-data-exposure.md#analyst-cohort-tool-exposure),
and
[Customer implementation checklist](docs/implementation-checklist.md#analyst-json-rpc-smoke-commands).
That workflow is filter -> cohort -> inspect -> analyze -> quotes ->
limitations, and it is intended for ChatGPT, Claude, or another reviewed MCP
host after the operator confirms that `tools/list` exposes the requested
cohort, theme, quote, pipeline, persona/industry, synthesis, and limitation
tools under `analyst` / `analyst-expansion` for approved Postgres sessions, or
`analyst` / `all-readonly` for trusted SQLite sessions.

## Private HTTP MCP Pilot

For shared private pilots where users should not run Docker locally, `gongmcp`
can expose the same read-only MCP tools over a minimal HTTP `/mcp` endpoint:

```bash
GONGMCP_BEARER_TOKEN="<customer-managed-token>" \
GONGMCP_ALLOWED_ORIGINS="https://approved-client.example.com" \
GONGMCP_TOOL_PRESET=business-pilot \
GONGMCP_MAX_SEARCH_RESULTS=100 \
  gongmcp --http 127.0.0.1:8080 --auth-mode bearer --db /srv/gongctl/gong.db
```

HTTP mode is a request/response JSON-RPC bridge over one operator-owned cache
store: SQLite for local/single-host pilots, or the Postgres shared-deployment
slice for approved presets such as `business-pilot`, narrower analyst presets,
and `analyst`. Put it behind TLS termination at a trusted company
proxy/gateway before non-local use. It is not a hosted SaaS layer, tenant
router, browser app, or full streaming MCP service. Every HTTP mode requires an
explicit tool preset or
allowlist, including loopback binds behind a proxy; the preferred shape is a
loopback bind behind the TLS gateway. Any non-loopback `--http` bind also requires
`--allow-open-network`. Unauthenticated HTTP is blocked by default and is
available only for explicit localhost development with
`--dev-allow-no-auth-localhost`:

```bash
GONGMCP_TOOL_PRESET=operator-smoke \
  gongmcp --http 127.0.0.1:8080 --auth-mode none --dev-allow-no-auth-localhost --db /srv/gongctl/gong.db
```

Non-local unauthenticated HTTP is not supported. Binding HTTP to a non-local
address requires bearer auth plus `--allow-open-network`; use that override
only behind an approved TLS/private-network boundary. Bearer tokens
are owned by the customer/operator and should come from an environment
variable, secret file, systemd environment file, Docker secret, Kubernetes
Secret, or company secret manager. Tokens must be at least 32 characters and
must not contain whitespace or control characters; generate a random token, for
example with `openssl rand -base64 32`. Do not commit MCP bearer tokens to Git,
SQLite, docs, images, or shared logs.
Non-local HTTP also requires `GONGMCP_ALLOWED_ORIGINS` or `--allowed-origins`
so the server can reject unexpected browser `Origin` headers.
Use `/healthz` for infrastructure health checks and `/mcp` only for MCP
JSON-RPC traffic.

Full SQLite catalog tools. Postgres availability is limited to the supported
presets and explicit allowlists described above; unsupported Postgres tools and
the full Postgres `all-readonly` preset fail closed.

- `get_sync_status`
- `search_calls`
- `get_call`
- `list_crm_object_types`
- `list_crm_fields`
- `list_crm_integrations`
- `list_cached_crm_schema_objects`
- `list_cached_crm_schema_fields`
- `list_gong_settings`
- `list_scorecards`
- `get_scorecard`
- `summarize_scorecard_activity`
- `get_business_profile`
- `list_business_concepts`
- `list_unmapped_crm_fields`
- `search_crm_field_values`
- `analyze_late_stage_crm_signals`
- `opportunities_missing_transcripts`
- `search_transcripts_by_crm_context`
- `opportunity_call_summary`
- `crm_field_population_matrix`
- `list_lifecycle_buckets`
- `summarize_calls_by_lifecycle`
- `search_calls_by_lifecycle`
- `prioritize_transcripts_by_lifecycle`
- `compare_lifecycle_crm_fields`
- `summarize_call_facts`
- `rank_transcript_backlog`
- `search_transcript_segments`
- `search_transcripts_by_call_facts`
- `search_transcript_quotes_with_attribution`
- `missing_transcripts`

The SQLite MCP path requires `--db`; the Postgres path omits `--db` and uses `GONG_DATABASE_URL` or `DATABASE_URL`. In both modes, the MCP server intentionally does not expose raw Gong API calls, arbitrary SQL, or full transcript dumps.
`get_call` returns minimized call detail instead of raw cached call JSON. It accepts either a raw `call_id` or a stable `call_ref`; when called with `call_ref`, the response keeps `call_ref` and omits the raw `call_id`. `crm_field_population_matrix` only allows safe categorical object/field pairs: `Opportunity.StageName`, `Opportunity.Forecast_Category_VP__c`, `Opportunity.Forecast_Category_AE__c`, `Account.Industry`, `Account.Account_Type__c`, and `Account.Revenue_Range_f__c`.
Lifecycle tools classify calls through the imported profile when one is active, otherwise through the builtin compatibility view. Profile-aware responses include `lifecycle_source` and profile provenance. Use `lifecycle_source=builtin` to force buckets such as `active_sales_pipeline`, `late_stage_sales`, `renewal`, `upsell_expansion`, and `customer_success_account`.
`summarize_call_facts` reads metadata-only facts for ad-hoc grouping. MCP only allows safe business dimensions there; use `list_unmapped_crm_fields` for profile field-discovery gaps and `search_crm_field_values` with explicit opt-in for directed value lookups. `rank_transcript_backlog` is the business-facing transcript-sync priority tool; model-facing MCP output redacts call IDs and titles while preserving rank, lifecycle, scope, system, direction, duration, and rationale. `list_unmapped_crm_fields` returns field names, types, cardinality, population/null rates, and length distribution only; it does not return raw example values by default.
`summarize_scorecard_activity` returns aggregate answered-scorecard counts and scores only. By default it does not return call IDs, scorecard IDs, user IDs, answer text, call titles, transcript snippets, emails, raw JSON, or raw scorecard activity payloads.
`search_crm_field_values` is the narrow MCP exception for value search: it requires an object type, field name, and value query. It redacts call IDs by default unless `include_call_ids=true` is explicitly set, and only returns bounded short value snippets plus call titles when `include_value_snippets=true` is explicitly set.
`analyze_late_stage_crm_signals` returns aggregate stage counts, field names, population rates, and lift only. In the current Postgres slice it is limited to `Opportunity.StageName` so custom field selection cannot be used as a raw value-distribution path. It does not return raw arbitrary CRM values, CRM object IDs/names, call IDs, call titles, transcript text, or profile payloads.
`opportunities_missing_transcripts` returns redacted per-Opportunity transcript coverage metadata. The Postgres reader function groups by Opportunity internally but returns only stage, call counts, missing/present transcript counts, and latest-call timing; Opportunity IDs/names, latest call IDs, raw CRM values, and raw storage fields are not returned.
`opportunity_call_summary` returns redacted per-Opportunity call aggregate metadata. The Postgres reader function groups by Opportunity internally but returns only stage, call count, transcript/missing-transcript counts, total duration, and latest-call timing; Opportunity IDs/names, latest call IDs, owner IDs, amount/close date, raw CRM values, and raw storage fields are not returned.
`crm_field_population_matrix` returns aggregate field-population cells grouped by approved categorical fields. Approved group values are intentionally exposed as aggregate labels. The Postgres reader function returns only group value, field name/label, object count, call count, and populated count; MCP/store output derives `population_rate` from those counts. Object IDs/names, object keys, call IDs, non-group raw CRM values, raw JSON, raw hashes, titles, and transcript text are not returned.
`compare_lifecycle_crm_fields` returns aggregate CRM field-population differences between two lifecycle buckets for the reviewed `Opportunity` object type. The Postgres reader function returns only object type, field name/label, bucket call counts, bucket populated counts, rates, and rate delta; it omits call IDs, call titles, CRM object IDs/names/keys, raw CRM values, raw JSON, raw hashes, and transcript text, rejects unreviewed object types, and excludes governance-suppressed calls inside SQL. Customer deployments require a tagged `v0.4.0` or later release.
`search_transcripts_by_crm_context` returns CRM-constrained transcript snippets. MCP output redacts call IDs, call titles, speaker IDs, CRM object IDs/names, and object keys. The Postgres reader function filters governance-suppressed calls inside SQL and returns only started time, object type, matching-object count, segment timing, and snippet; it omits call IDs, call titles, speaker IDs, CRM object IDs/names, object keys, raw CRM values, raw JSON, raw hashes, and full transcript text.
`missing_transcripts` returns direct missing-transcript call references for admin transcript-backfill workflows. Postgres supports the reviewed filter set: date range, lifecycle bucket, scope, system, direction, CRM object type, and CRM object ID; `crm_object_id` requires `crm_object_type` to avoid cross-object probing. It returns call IDs, titles, and start times, but not raw cached JSON, transcript text, CRM field values, raw JSON, or raw hashes. Use explicit allowlists; do not put it in business-user presets.
The generic Postgres reader role can execute the reviewed bounded reader
functions, so treat that database URL as a service secret rather than a
general-purpose SQL login; direct table reads of raw CRM values, object names,
and transcript text remain denied. For narrower deployments, use a tool-scoped
reader role plus `GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS=1`; startup rejects a
role that can currently execute functions outside the selected preset/allowlist.
For `business-pilot`, startup also validates the first reviewed table/column
boundary and rejects direct `calls.call_id`, `calls.title`,
`call_facts.call_id`, and `call_facts.title` grants. The scoped reader URL
remains a service secret because selected functions and sanitized views can
still expose minimized call metadata, timings, counts, and tenant terminology.
`search_transcript_segments` returns bounded snippets. Call and speaker provenance is controlled by the `gongmcp` server setting `--transcript-evidence-provenance` / `GONGMCP_TRANSCRIPT_EVIDENCE_PROVENANCE`: `redacted` by default, stable `call_ref` / `speaker_ref` aliases in `alias` mode, and raw IDs only in `raw` mode with the per-call include flags.
`search_transcript_quotes_with_attribution` returns bounded quote snippets joined to available Account/Opportunity metadata for marketing and sales evidence review. Call titles are returned by default where policy permits; raw call IDs, account names/websites, and opportunity names/close dates/probabilities require explicit opt-in flags. The tool also returns participant/person-title status so users can tell when contact title data is missing from the cache rather than inferred.
When serving a physically redacted Postgres MCP database with account-name search enabled, run `gongmcp` with `GONGMCP_AI_GOVERNANCE_CONFIG` or `--ai-governance-config` pointing at the same private policy used to build the serving DB. This lets MCP deny configured restricted names and aliases before query execution instead of returning row counts for restricted-name probes.

## Data Handling Rules

- Do not commit Gong credentials, transcripts, recordings, or real payload fixtures.
- Keep fixtures sanitized and small.
- Log call IDs, counts, paths, and status; do not log transcript body text.
- Use `--out`, `--out-dir`, and SQLite `--db` paths that live outside the source repo for real customer data.
- Respect `429 Retry-After` and keep the built-in 3 requests/second limiter enabled.
- Avoid undocumented endpoints.

## Project Layout

```text
cmd/gongctl/              CLI entrypoint
cmd/gongmcp/              read-only MCP server over cache stores
internal/gong/            typed API client + raw request support
internal/auth/            env/config credential loading
internal/ratelimit/       3 rps limiter + Retry-After-friendly client retries
internal/export/          JSONL writers
internal/checkpoint/      resumable batch checkpoints
internal/redact/          safe logging helpers
internal/profile/         tenant-editable business profile parser, validator, discovery, and rule evaluation
internal/store/sqlite/    local SQLite cache for calls, users, transcripts, CRM schema, settings, profiles, and sync state
internal/store/postgres/  shared Postgres cache backend, migrations, read models, and scoped reader helpers
internal/syncsvc/         call/user/inventory sync orchestration over the store/cache layer
internal/transcripts/     transcript sync/search helpers on top of store + Gong client
internal/mcp/             read-only MCP adapter over the store interface
testdata/fixtures/        sanitized sample payloads only
docs/                     architecture, data handling, MCP phase plan
```

## Transcript Insights Boundary

For Transcript Insights, treat `gongctl` as an ingestion/export utility:

1. Pull call metadata and transcripts from Gong with customer credentials.
2. Cache call/user/transcript state in a local SQLite database.
3. Write transcript files to a local data directory outside this repo when needed.
4. Let the analysis pipeline consume the SQLite-backed exports/files.

Keep the CLI boring, auditable, restartable, and separate from the analysis layer.
