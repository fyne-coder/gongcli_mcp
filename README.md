# gongctl

`gongctl` is an unofficial Gong API command-line client. It is designed as an open-source wrapper: source code can be public, but every user brings their own Gong credentials and is responsible for consent, data handling, and Gong terms. This project is not affiliated with or endorsed by Gong.

This project starts as a local CLI and keeps the API client boundary narrow. A read-only local MCP server is available over the SQLite cache; it does not expose raw Gong API access. `gongctl` is the ingestion wedge; multi-user transcript review, evidence workflows, artifact generation, and customer-specific customization belong in a separate pipeline/application layer.

## Positioning

`gongctl` complements Gong rather than replacing it. Gong remains the system of
record for calls, transcripts, trackers, scorecards, and Gong-native AI
workflows. `gongctl` gives operators and analysts a local, queryable evidence
workbench for questions that are awkward to answer directly in Gong:

- prototype ad-hoc analyses before turning them into durable Gong trackers,
  scorecards, themes, or Gong AI prompts
- join cached transcript evidence to call facts, lifecycle buckets, scorecard
  configuration, and selected CRM context under operator control
- inspect coverage gaps before trusting AI summaries or coaching conclusions
- keep an open-source/local-first path where every company uses its own Gong
  credentials and controls where cached data lives

The intended flow is: sync an approved local cache, test the business question
with bounded evidence, convert the useful pattern into Gong-native
configuration or a governed analysis workflow, then use Gong AI with a clearer
question, better tracker/theme inputs, and known coverage limits.

## Status

Early public release. `v0.3.0` is enterprise-pilot ready for operator-managed
local sync plus read-only MCP over a reviewed SQLite cache. Public 1.0 still
requires signed/provenance-backed release artifacts, stable deprecation policy,
and the remaining production hardening tracked in [docs/roadmap.md](docs/roadmap.md).

For company evaluation, start with the enterprise pilot packet:

- [Quickstart](docs/quickstart.md)
- [Customer-hosted package](docs/customer-hosted-package.md)
- [Data Boundary Statement](docs/data-boundary-statement.md)
- [Support model](docs/support.md)
- [Enterprise deployment](docs/enterprise-deployment.md)
- [Security model](docs/security-model.md)
- [Remote MCP auth and connector setup](docs/remote-mcp-auth.md)
- [Security questionnaire](docs/security-questionnaire.md)
- [MCP data exposure](docs/mcp-data-exposure.md)
- [Operator sync runbook](docs/runbooks/operator-sync.md)
- [Configuration surfaces](docs/configuration-surfaces.md)
- [Business-user guide](docs/business-user-guide.md)
- [Pilot plan](docs/pilot-plan.md)

Fast paths for evaluation and deployment:

```bash
gongmcp --list-tool-presets
gongctl profile schema
```

Use `business-pilot` for first business-user access and `all-readonly` only for
trusted admin/analyst sessions or a fully reviewed filtered DB.

- Deployment worksheet: [Customer implementation checklist](docs/implementation-checklist.md)
- Local/container start: [Quickstart](docs/quickstart.md)
- HTTPS/auth boundary: [Remote MCP auth and connector setup](docs/remote-mcp-auth.md)
- Image digest verification: [Release process](docs/release.md)
- RevOps profile setup: [Business profiles](docs/profiles.md)

For support intake, generate the local/offline diagnostic bundle before
sharing logs or payloads:

```bash
gongctl support bundle --db "$HOME/gongctl-data/gong-mcp-governed.db" --out "$HOME/gongctl-data/support-bundle"
```

The command opens SQLite read-only, does not need Gong credentials, and does
not make network calls. See [Support model](docs/support.md) for what the
bundle includes and excludes.

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

This repo requires Go 1.22+.

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

Use the published GHCR images after a release is published:

```bash
docker run --rm ghcr.io/fyne-coder/gongcli_mcp/gongctl:v0.3.0 version
docker run --rm -v "$HOME/gongctl-data:/data" ghcr.io/fyne-coder/gongcli_mcp/gongctl:v0.3.0 sync status --db /data/gong.db
```

For read-only MCP, use the MCP-only image:

```bash
docker run --rm -i --network none -v "$HOME/gongctl-data:/data:ro" ghcr.io/fyne-coder/gongcli_mcp/gongmcp:v0.3.0 --db /data/gong.db --tool-preset business-pilot
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
docker run --rm -i --network none -v "$HOME/gongctl-data:/data:ro" gongctl:mcp-local --db /data/gong.db
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

Compose requires `GONGCTL_DATA_DIR` to point at an external data directory so customer SQLite/transcript data does not land under the source checkout.

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
- `gongctl calls show --json`
- `gongctl calls list --context extended ...`
- `gongctl calls export ...`
- `gongctl calls transcript ...`
- `gongctl calls transcript-batch ...`
- `gongctl sync transcripts ...`
- `gongctl sync calls --preset business`
- `gongctl sync calls --preset all`
- `gongctl sync calls --include-parties`

Approved override example:

```bash
GONGCTL_RESTRICTED=1 gongctl calls list --from 2026-04-01 --to 2026-04-24 --context extended --allow-sensitive-export --out calls-with-crm-context.json
GONGCTL_RESTRICTED=1 gongctl sync calls --db ~/gongctl-data/gong.db --from 2026-04-01 --to 2026-04-24 --preset business --allow-sensitive-export
GONGCTL_RESTRICTED=1 gongctl sync calls --db ~/gongctl-data/gong.db --from 2026-04-01 --to 2026-04-24 --preset minimal --include-parties --allow-sensitive-export
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
gongctl diagnose
```

For repeatable operator refreshes, stage the approved steps in YAML and dry-run
them before the scheduled job uses the same file:

```bash
gongctl sync run --config testdata/fixtures/sync-run-minimal.yaml --dry-run
gongctl cache inventory --db ~/gongctl-data/gong.db
gongctl cache purge --db ~/gongctl-data/gong.db --older-than 2026-04-01 --dry-run
```

## Advanced Local Operator Commands

These commands are useful when an operator is working against their own tenant data on their own machine. They can reveal raw call JSON, transcript files, CRM context, profile-derived field values, or tenant-specific configuration, so keep outputs outside the source repo and do not use them in public examples.

```bash
gongctl sync crm-integrations --db ~/gongctl-data/gong.db
gongctl sync crm-schema --db ~/gongctl-data/gong.db --integration-id CRM_INTEGRATION_ID --object-type ACCOUNT --object-type DEAL
gongctl sync settings --db ~/gongctl-data/gong.db --kind trackers
gongctl sync run --config ~/gongctl-data/company-sync.yaml --dry-run
gongctl sync run --config ~/gongctl-data/company-sync.yaml
gongctl cache inventory --db ~/gongctl-data/gong.db
gongctl cache purge --db ~/gongctl-data/gong.db --older-than 2026-04-01 --dry-run
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
gongctl analyze transcript-backlog --db ~/gongctl-data/gong.db --limit 25
gongctl mcp tool-info list_gong_settings
gongctl search calls --db ~/gongctl-data/gong.db --crm-object-type Opportunity --crm-object-id opp_001 --limit 10
gongctl calls show --db ~/gongctl-data/gong.db --call-id CALL_ID --json
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

The Agent E CLI flow is SQLite-backed:

1. `gongctl sync calls --db PATH --from DATE --to DATE --preset ...`
2. `gongctl sync users --db PATH`
3. `gongctl sync transcripts --db PATH --out-dir PATH`
4. `gongctl sync crm-integrations --db PATH`
5. `gongctl sync crm-schema --db PATH --integration-id ID --object-type TYPE`
6. `gongctl sync settings --db PATH --kind trackers|scorecards|workspaces`
7. `gongctl sync run --config PATH [--dry-run]`
8. `gongctl sync status --db PATH`
9. `gongctl cache inventory --db PATH`
10. `gongctl cache purge --db PATH --older-than YYYY-MM-DD [--dry-run|--confirm]`
11. `gongctl profile discover --db PATH --out PATH`
12. Review and edit the YAML profile for tenant-specific CRM objects, fields, lifecycle buckets, and methodology concepts.
13. `gongctl profile validate --db PATH --profile PATH`
14. `gongctl profile import --db PATH --profile PATH`
15. `gongctl analyze calls --db PATH --group-by DIMENSION [--lifecycle-source auto|profile|builtin]`
16. `gongctl analyze coverage --db PATH [--lifecycle-source auto|profile|builtin]`
17. `gongctl analyze crm-schema --db PATH [--integration-id ID] [--object-type TYPE]`
18. `gongctl analyze settings --db PATH [--kind KIND]`
19. `gongctl analyze transcript-backlog --db PATH [--lifecycle-source auto|profile|builtin]`
20. `gongctl search transcripts --db PATH --query TEXT`
21. `gongctl search calls --db PATH [--crm-object-type TYPE] [--crm-object-id ID]`
22. `gongctl calls show --db PATH --call-id ID --json`

Rules:

- `--db` is required for all SQLite-backed `sync`, `search`, and `calls show` commands.
- `sync calls --preset business` requests Gong `Extended` context.
- `sync calls --preset minimal` does not request Gong context.
- `sync calls --preset all` currently maps to `Extended` context as well; it is documented separately so it can diverge later without changing the CLI shape.
- `sync calls --include-parties` requests Gong call participant fields such as names, emails, speaker IDs, and titles. Use it only for approved operator refreshes because returned participant payloads are cached in raw call JSON.
- `sync crm-integrations` caches Gong CRM integration IDs needed by `sync crm-schema`.
- `sync crm-schema` caches selected CRM field metadata by integration/object type; it stores field names and labels, not CRM field values.
- `sync settings` caches read-only Gong inventory for trackers, scorecards, and workspaces.
- `sync run --config PATH` executes a staged refresh plan from YAML. Relative
  `db` and `out_dir` paths resolve from the config file location, and
  `--dry-run` validates the file without calling Gong. Sensitive transcript and
  extended-context steps are flagged in dry-run output, but runtime approval is
  still required through `--allow-sensitive-export` or
  `GONGCTL_ALLOW_SENSITIVE_EXPORT=1` when restricted mode is enabled.
- `sync transcripts` selects calls that do not already have cached transcripts, batches missing call IDs into Gong transcript requests, and writes one normalized transcript JSON file per returned call transcript. The default `--batch-size` is 100, and the CLI caps it at 100.
- Existing cached transcripts are skipped by `sync transcripts`; rerun `sync calls` to refresh call metadata and embedded CRM context. A transcript refresh policy for re-checking already downloaded transcripts is planned separately.
- `sync status` separates embedded CRM context from CRM integration/schema inventory. A cache can contain CRM context from `sync calls --preset business` even when `sync crm-integrations` or `sync crm-schema` has not populated inventory tables.
- `sync status` also returns public business-readiness flags for conversation volume, transcript coverage, scorecard/theme inventory, lifecycle separation, CRM segmentation, and attribution readiness.
- `cache inventory --db PATH` opens the database read-only and reports file
  size, primary table counts, call date range, transcript and CRM-context
  presence, profile status, and last sync metadata with a sensitive-data
  warning.
- `cache purge --db PATH --older-than YYYY-MM-DD` is dry-run by default and
  reports calls, transcripts, transcript segments, CRM context, and profile
  fact-cache rows that would be deleted. Confirmed purges enable SQLite
  `secure_delete`, checkpoint/truncate WAL state, and run `VACUUM`; still add
  `--confirm` only after backup, retention, and legal-hold checks are complete.
- `profile discover` generates an editable YAML profile from cached CRM inventory and includes confidence plus evidence for discovered mappings. Discovery is an English-biased starter and may include CRM evidence values in the YAML, so write real-tenant output to a local file outside git rather than shared logs.
- Discovered profiles are starter drafts, not universal truth. A human should review tenant lifecycle, object, field, and methodology mappings before relying on profile-aware separation of sales and post-sales calls.
- `profile validate` rejects malformed YAML, unsupported profile versions, unsupported rule operators, unsafe regex rules, missing required lifecycle buckets, and mapped fields that do not exist in cached CRM data.
- `profile import` stores the active profile in SQLite in one transaction. Re-importing identical profile content is a no-op; changed source metadata for the same canonical profile updates metadata without changing profile meaning.
- Profile-aware analysis uses a materialized SQLite read model keyed by active profile and canonical hash. Writable CLI sync/profile commands rebuild or warm it; read-only MCP requires that cache to be current and reports a stale-cache error instead of writing to SQLite.
- Profile rules are a closed Go-evaluated grammar: `equals`, `in`, `prefix`, `iprefix`, `regex`, `is_set`, and `is_empty`. Profiles do not run SQL, templates, JSONPath, JMESPath, or arbitrary expressions.
- `analyze scorecards` and `analyze scorecard` expose scorecard names and question text from cached settings without returning raw settings payloads.
- `sync` commands write concise progress summaries to `stderr`.
- `sync status`, `search ...`, and `calls show --json` write JSON to `stdout`.
- `analyze ...` commands write metadata-only JSON summaries to `stdout`.
- `search transcripts` returns segment metadata and snippets only; it does not emit full segment text.
- `search calls` CRM filters only match rows that were synced with stored CRM context, so use `business` or `all` when those searches are needed.
- `analyze calls` groups the normalized `call_facts` view by safe dimensions such as `lifecycle`, `opportunity_stage`, `account_industry`, `scope`, `system`, `direction`, `transcript_status`, `duration_bucket`, `month`, `lead_source`, and `forecast_category`.
- `analyze coverage` includes lifecycle, scope, system, and direction summaries so transcript coverage gaps can be understood by conversation type.
- `analyze transcript-backlog` prioritizes External and Conference-style customer conversations ahead of short dialer-style events by default.
- With an active profile, profile-aware analysis defaults to `lifecycle_source=profile`; use `--lifecycle-source builtin` to force the compatibility lifecycle/read model.

For non-client-specific business prompt examples, see [docs/public-readiness.md](docs/public-readiness.md). Public examples should avoid tenant field names, customer names, raw CRM values, transcripts, call titles, object IDs, and call IDs.

## Deterministic AI Exclusion Filtering

For companies with customer-specific AI-use restrictions, keep the real
restricted-customer list in a private YAML file outside Git and load it with
`gongmcp --ai-governance-config PATH` or `GONGMCP_AI_GOVERNANCE_CONFIG`.
`gongctl governance audit --db PATH --config PATH` verifies deterministic
name/alias matches before MCP use. This is a local exclusion filter, not legal
consent management or contractual enforcement. See
[docs/ai-governance.md](docs/ai-governance.md); the tracked example config uses
synthetic names only. For MCP/LLM use, default to a physically filtered MCP DB:

```bash
gongctl governance export-filtered-db --db ~/gongctl-data/gong.db --config ~/gongctl-data/ai-governance.yaml --out ~/gongctl-data/gong-mcp-governed.db
gongmcp --db ~/gongctl-data/gong-mcp-governed.db --tool-preset business-pilot
```

Raw-DB governance remains available as a fallback; when it is enabled,
`gongmcp` requires an explicit governance-compatible `--tool-preset`,
`--tool-allowlist`, `GONGMCP_TOOL_PRESET`, or `GONGMCP_TOOL_ALLOWLIST`, and
unsupported aggregate/config tools are refused.

## Local MCP (stdio)

`gongmcp --db PATH` serves a read-only stdio MCP adapter over the local SQLite cache.
Use `gongctl mcp tools` or `gongctl mcp tool-info NAME` to inspect the local MCP tool catalog without starting a host app.

By default, `gongmcp` exposes the full read-only MCP catalog to any connected
host. Company pilots should narrow that surface with `gongmcp --tool-preset`
or `--tool-allowlist`, then layer host and filesystem controls around the
approved business-user subset. Trusted single-user analyst workstations may
use `--tool-preset all-readonly` or intentionally skip the allowlist in stdio
mode and turn on per-tool opt-ins
(`include_call_ids`, `include_speaker_ids`, `include_value_snippets`) for
deeper, identifier-bearing questions; see
[docs/mcp-data-exposure.md](docs/mcp-data-exposure.md) for the trade-off and
[docs/mcp-data-exposure.md#mcp-call-volume-and-limits](docs/mcp-data-exposure.md#mcp-call-volume-and-limits)
for how to keep MCP traffic from overwhelming the host context window.

## Private HTTP MCP Pilot

For shared private pilots where users should not run Docker locally, `gongmcp`
can expose the same read-only MCP tools over a minimal HTTP `/mcp` endpoint:

```bash
GONGMCP_BEARER_TOKEN="<customer-managed-token>" \
GONGMCP_ALLOWED_ORIGINS="https://approved-client.example.com" \
GONGMCP_TOOL_PRESET=business-pilot \
  gongmcp --http 127.0.0.1:8080 --auth-mode bearer --db /srv/gongctl/gong.db
```

HTTP mode is a request/response JSON-RPC bridge over one operator-owned SQLite
cache. Put it behind TLS termination at a trusted company proxy/gateway before
non-local use. It is not a hosted SaaS layer, tenant router, browser app, or
full streaming MCP service. Every HTTP mode requires an explicit tool preset or
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
Secret, or company secret manager. Do not commit MCP bearer tokens to Git,
SQLite, docs, images, or shared logs.
Non-local HTTP also requires `GONGMCP_ALLOWED_ORIGINS` or `--allowed-origins`
so the server can reject unexpected browser `Origin` headers.
Use `/healthz` for infrastructure health checks and `/mcp` only for MCP
JSON-RPC traffic.

Tools:

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

The MCP server requires `--db`, reads SQLite only, and intentionally does not expose raw Gong API calls, arbitrary SQL, or full transcript dumps.
`get_call` returns minimized call detail instead of raw cached call JSON. `crm_field_population_matrix` only allows safe categorical group fields such as `StageName`.
Lifecycle tools classify calls through the imported profile when one is active, otherwise through the builtin compatibility view. Profile-aware responses include `lifecycle_source` and profile provenance. Use `lifecycle_source=builtin` to force buckets such as `active_sales_pipeline`, `late_stage_sales`, `renewal`, `upsell_expansion`, and `customer_success_account`.
`summarize_call_facts` reads metadata-only facts for ad-hoc grouping. MCP only allows safe business dimensions there; use `search_crm_field_values` with explicit opt-in for directed value lookups. `rank_transcript_backlog` is the business-facing transcript-sync priority tool; model-facing MCP output redacts call IDs and titles while preserving rank, lifecycle, scope, system, direction, duration, and rationale. `list_unmapped_crm_fields` returns field names, types, cardinality, population/null rates, and length distribution only; it does not return raw example values by default.
`search_crm_field_values` is the narrow MCP exception for value search: it requires an object type, field name, and value query. It redacts call IDs by default unless `include_call_ids=true` is explicitly set, and only returns bounded short value snippets plus call titles when `include_value_snippets=true` is explicitly set.
`search_transcript_segments` returns bounded snippets, but redacts call IDs and speaker IDs by default. Exact identifiers require `include_call_ids=true` and `include_speaker_ids=true`.
`search_transcript_quotes_with_attribution` returns bounded quote snippets joined to available Account/Opportunity metadata for marketing and sales evidence review. Call IDs, call titles, account names/websites, and opportunity names/close dates/probabilities require explicit opt-in flags; the tool also returns participant/person-title status so users can tell when contact title data is missing from the cache rather than inferred.

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
cmd/gongmcp/              read-only local MCP server over SQLite
internal/gong/            typed API client + raw request support
internal/auth/            env/config credential loading
internal/ratelimit/       3 rps limiter + Retry-After-friendly client retries
internal/export/          JSONL writers
internal/checkpoint/      resumable batch checkpoints
internal/redact/          safe logging helpers
internal/profile/         tenant-editable business profile parser, validator, discovery, and rule evaluation
internal/store/sqlite/    local SQLite cache for calls, users, transcripts, CRM schema, settings, profiles, and sync state
internal/syncsvc/         SQLite-backed call/user/inventory sync orchestration
internal/transcripts/     transcript sync/search helpers on top of store + Gong client
internal/mcp/             read-only MCP adapter over SQLite
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
