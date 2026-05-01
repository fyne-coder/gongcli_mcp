# Public-Safe Business Readiness

`gongctl` is meant to be reusable across Gong tenants. The public workflow is:

1. each user brings their own Gong credentials
2. CLI commands sync a local SQLite cache
3. optional local profile YAML maps that tenant's CRM and lifecycle concepts
4. MCP reads aggregate/minimized SQLite data only

Do not ship real tenant profiles, call titles, customer names, CRM values, transcripts, object IDs, or call IDs in public examples.

## Readiness Surfaces

Run:

```bash
gongctl sync status --db ~/gongctl-data/gong.db
```

The response separates several states that are easy to confuse:

- cached calls, users, transcripts, and transcript segments
- embedded CRM context stored from `sync calls --preset business`
- Gong CRM integration inventory from `sync crm-integrations`
- Gong CRM schema inventory from `sync crm-schema`
- Gong settings inventory from `sync settings`
- active business profile status and profile cache freshness
- public business-readiness flags

Embedded CRM context and CRM integration/schema inventory are separate. A tenant can have CRM context attached to synced calls while the CRM integration/schema inventory still reports zero because those inventory endpoints have not been synced or are unavailable.

## Business Questions That Work First

### Sales Leader

Question shape:

```text
Where is customer conversation volume concentrated across the funnel or lifecycle, and where is transcript coverage weakest?
```

Works from metadata when cached calls exist. It improves with `sync calls --preset business`, a reviewed profile, and transcript sync.

Useful commands:

```bash
gongctl analyze coverage --db ~/gongctl-data/gong.db
gongctl analyze calls --db ~/gongctl-data/gong.db --group-by lifecycle
gongctl analyze transcript-backlog --db ~/gongctl-data/gong.db
```

### Sales Enablement

Question shape:

```text
What coaching or QA themes are available from cached scorecards, and which conversation segments should managers sample next?
```

Requires cached settings for scorecard/theme inventory. Transcript sampling requires cached transcripts. Backlog prioritization can still identify the best missing-transcript areas before transcripts are loaded.

Useful commands:

```bash
gongctl sync settings --db ~/gongctl-data/gong.db --kind scorecards
gongctl analyze scorecards --db ~/gongctl-data/gong.db
gongctl analyze transcript-backlog --db ~/gongctl-data/gong.db
```

### RevOps / CS

Question shape:

```text
Can we reliably separate sales funnel calls from post-sales, renewal, upsell, and customer-success calls today?
```

Builtin lifecycle buckets can help with obvious CRM patterns, but reliable tenant-specific separation requires a reviewed imported profile.

Useful commands:

```bash
gongctl profile discover --db ~/gongctl-data/gong.db --out ~/gongctl-data/gongctl-profile.yaml
gongctl profile validate --db ~/gongctl-data/gong.db --profile ~/gongctl-data/gongctl-profile.yaml
gongctl profile import --db ~/gongctl-data/gong.db --profile ~/gongctl-data/gongctl-profile.yaml
gongctl analyze calls --db ~/gongctl-data/gong.db --group-by lifecycle --lifecycle-source profile
```

### Marketing / Demand Gen

Question shape:

```text
What CRM segmentation and attribution questions are viable from cached Gong data today, and what is blocked?
```

Works best with embedded CRM context from `sync calls --preset business`. Attribution-specific concepts usually need profile review because field names and meanings vary by tenant.

Useful commands:

```bash
gongctl analyze calls --db ~/gongctl-data/gong.db --group-by lead_source
gongctl analyze calls --db ~/gongctl-data/gong.db --group-by account_industry
gongctl analyze coverage --db ~/gongctl-data/gong.db
gongctl profile show --db ~/gongctl-data/gong.db
```

## Transcript Backlog Defaults

Transcript prioritization favors business-useful customer conversations:

- `External` scope first
- `Conference` or meeting-like calls before short dialer-style calls
- stronger lifecycle/profile confidence before weak signals
- longer customer conversations before short events

MCP backlog tools keep call IDs and titles out of default model-facing output. Use CLI commands against the local database when an operator needs to act on exact calls.

## MCP Boundary

The local MCP server is aggregate-first:

- reads SQLite only
- does not call Gong live
- does not expose raw transcripts
- does not expose raw CRM values by default
- redacts transcript search call IDs and speaker IDs by default
- does not expose full participant/user records
- does not expose profile import or mutation

Use `gongctl mcp tools` and `gongctl mcp tool-info NAME` to inspect available read-only tools before connecting a host application.

## Public Git And GHCR Checklist

Before making the repository or a release broadly consumable:

1. Run `make secret-scan`, `go test -count=1 ./...`, `go vet ./...`, `make sbom`,
   and `make checksums`.
2. Confirm `git ls-files` does not include `.env`, SQLite databases, transcript
   files, JSONL exports, tenant profile YAML, local logs, or checkpoint files.
3. Confirm examples use synthetic data, role labels, fake object IDs, and
   portable paths such as `$HOME/gongctl-data` or `$PULSARIS_DB`.
4. Publish both GHCR packages from the same tag:
   `ghcr.io/fyne-coder/gongcli_mcp/gongctl:vX.Y.Z` and
   `ghcr.io/fyne-coder/gongcli_mcp/gongmcp:vX.Y.Z`.
5. For MCP consumption, document the MCP-only image, no Gong credentials, and a
   read-only SQLite mount. Use `--network none` for stdio MCP; HTTP MCP needs an
   explicit allowlist and the approved proxy/TLS path.
