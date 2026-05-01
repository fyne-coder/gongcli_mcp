# Security Model

## Scope

This document covers the enterprise-pilot security posture of the local
`gongctl` CLI and the read-only `gongmcp` MCP server. `gongmcp` supports stdio
for local/Desktop hosts and HTTP `/mcp` for private company pilots.

For customer-hosted security review, pair this with
[Data Boundary Statement](data-boundary-statement.md) and
[Support](support.md). The broader review package and example questionnaire
answers live in [Customer-hosted package](customer-hosted-package.md) and
[Security questionnaire](security-questionnaire.md).

The current design is intentionally local-first:

- `gongctl` is the live Gong API client and the only surface that should handle Gong credentials.
- `gongmcp` reads a previously synced SQLite cache and does not call Gong directly.
- The repository can be public; tenant data, credentials, and local cache files cannot.

Current enforcement limits are important: `gongctl` now has a
restricted/company mode for high-risk CLI commands, and `gongmcp` can enforce a
server-side tool allowlist. Business-user deployments still need approved host,
filesystem, network, token, and operator-process controls because allowlisting
and bearer auth narrow access but do not make returned Gong-derived data
non-sensitive.

## Trust Boundaries

| Boundary | Components | Allowed data | Primary controls |
| --- | --- | --- | --- |
| Gong API boundary | `internal/gong`, `internal/auth`, `gongctl sync ...`, `gongctl calls ...`, `gongctl api raw ...` | Live Gong API responses, credentials in process memory | HTTPS to Gong, documented rate limiting, operator-supplied credentials |
| Local operator boundary | shell, `.env`, exported env vars, CLI stdout/stderr, local files | Full tenant data when the operator runs live or export commands | User/workstation access control, keep secrets out of git, keep outputs outside the repo |
| Local data-store boundary | SQLite cache, transcript JSON files, tenant profile YAML | Cached call metadata, CRM context, transcript snippets/full transcripts, settings inventory, profile state | External data directory, read-only mount for MCP, repo ignores local data as a safety net |
| MCP boundary | `gongmcp`, `internal/mcp`, connected host app/model | Read-only SQLite query results only | SQLite opened read-only, no live Gong credentials required, no raw API passthrough, no write tools, optional bearer auth for HTTP |
| Public source boundary | repo source, docs, tests, examples | Sanitized code and docs only | No live tenant names, transcripts, IDs, secrets, or private local paths in tracked files |

## Credential Flow

1. The operator provides Gong credentials through exported environment variables or an ignored `.env` file.
2. `gongctl` reads those credentials and uses them only for live API calls.
3. Sync commands write cached results to a local SQLite database and optional local transcript/profile files.
4. `gongmcp` starts later with `--db PATH`, opens that SQLite file read-only, and serves stdio or HTTP MCP requests without Gong credentials.

Operational implications:

- `gongmcp` should not be given Gong API secrets.
- Stdio Docker MCP runs should use a read-only data mount and `--network none`;
  HTTP MCP runs need only the MCP port exposed through the approved proxy path.
- Shared hosts should avoid long-lived plaintext environment variables when possible because container inspection can expose them.
- HTTP MCP bearer tokens are customer-managed deployment secrets. Prefer mounted
  secret files or platform secret managers over raw command-line flags.

## Data Classification

| Class | Examples in this repo shape | Default surfaces | Handling expectation |
| --- | --- | --- | --- |
| Restricted secrets | Gong access key, Gong access key secret, future OAuth secrets or refresh tokens | CLI environment only | Never commit, never place in docs, keep out of MCP |
| MCP access secrets | HTTP bearer token for `gongmcp` private-pilot mode | MCP process environment or mounted secret file | Customer-managed; never commit, never bake into images, rotate on access changes |
| AI governance config | Restricted customer names and aliases for MCP filtering against cached CRM account/customer identity fields | Private operator config path or mounted config volume | Never commit real lists, never bake into images, audit before MCP use |
| Restricted tenant content | raw call JSON, transcript JSON/text, embedded CRM field values, profile discovery evidence | local SQLite, local transcript/profile files, selected CLI commands | Keep outside git and outside shared logs; only operator-controlled local storage |
| Sensitive tenant metadata | call titles, call IDs, object IDs, scorecard IDs, workspace IDs, tracker names, question text, lifecycle/profile concepts | selected CLI commands and selected MCP tools | Treat as customer data even when not full transcript content |
| Reduced business metadata | counts, coverage, lifecycle summaries, field population rates, readiness flags | safe CLI summaries and many MCP tools | Prefer for model-facing analysis and pilot review materials |
| Public repo content | code, synthetic fixtures, sanitized docs | tracked source files | Must stay tenant-free and secret-free |

## Capability Model

| Capability | `gongctl` CLI | `gongmcp` MCP |
| --- | --- | --- |
| Live Gong API access | Yes | No |
| Requires Gong credentials | Yes for live commands | No |
| SQLite writes | Yes | No |
| Profile import/mutation | Yes | No |
| Raw export path | Yes, operator-directed | No |
| Transcript search | Yes | Yes, bounded/query-only |
| Raw transcript dump | Yes through CLI transcript/export flows | No |
| Arbitrary API passthrough | Yes via `api raw` | No |
| Arbitrary SQL | No public surface | No |

Implementation controls on the MCP side:

- `cmd/gongmcp/main.go` requires an explicit `--db PATH`.
- `gongmcp` opens SQLite through `sqlite.OpenReadOnly(...)`.
- `internal/mcp/server.go` enforces bounded result counts and a maximum MCP frame size of about 1 MiB.
- Profile-aware reads refuse stale-cache rebuilds instead of mutating SQLite from MCP.
- HTTP mode exposes only `/mcp`, supports `--auth-mode none|bearer`, requires
  an explicit tool allowlist, and blocks non-local binds unless
  `--allow-open-network` is set.
- AI governance mode suppresses configured customer-name/alias matches from
  supported MCP record/snippet tools and fails closed for unsupported aggregate
  tools while the filter is active. It does not return filtered-call counts over
  MCP because those counts could become a match oracle.

Implementation controls on the CLI side:

- `GONGCTL_RESTRICTED=1` or `gongctl --restricted ...` turns on restricted/company mode.
- In restricted mode, `api raw`, `calls show --json`, `calls export`,
  `calls list --context extended`, `calls transcript`, `calls transcript-batch`,
  `sync transcripts`, `sync calls --preset business|all`, and
  `sync calls --include-parties` are blocked unless the operator adds
  `--allow-sensitive-export` or sets
  `GONGCTL_ALLOW_SENSITIVE_EXPORT=1`.
- `gongctl sync run --config ... --dry-run` validates staged operator refresh
  configs without calling Gong so reviewed schedules can be checked before
  execution. The config file cannot self-authorize sensitive steps; runtime
  approval must come from the operator flag or environment variable.
- Approved `--include-parties` syncs record the requested participant mode in
  sync history. If Gong rejects that selector and the job retries without
  parties, the run records `include_parties_result=omitted_fallback`.
- `gongctl cache inventory --db ...` is read-only and returns cache metadata,
  sync history, profile status, and a sensitive-data warning. Even though it
  avoids transcript bodies and raw payload dumps, its output should still be
  handled as tenant operational metadata.
- `gongctl cache purge --db ... --older-than YYYY-MM-DD` is dry-run by default.
  Confirmed purges delete matching call rows and dependent cached transcripts,
  transcript segments, embedded CRM context, and profile call-fact cache rows,
  then use SQLite `secure_delete`, WAL checkpoint/truncation, and `VACUUM`.
  Operators must still handle transcript files, snapshots, backups, sync
  history, profile definitions, CRM schema inventory, and settings inventory
  through company retention controls.

## High-Risk CLI Commands

These commands are valid operator tools, but they should be treated as restricted during an enterprise pilot because they can surface or create sensitive tenant data:

| Command family | Why it is high risk | Control expectation |
| --- | --- | --- |
| `gongctl api raw ...` | Bypasses typed minimization and can return raw Gong payloads | Use only for operator debugging; keep output local |
| `gongctl calls show --json` | Returns raw cached call detail | Do not paste into tickets, docs, or prompts |
| `gongctl calls list/export --context extended ...` | Can emit embedded CRM context and customer identifiers at scale | Restricted mode requires explicit override; write to external files only and review before sharing |
| `gongctl calls transcript ...` and `gongctl calls transcript-batch ...` | Produces transcript payloads and transcript files | Store outside the repo and outside shared logs |
| `gongctl sync transcripts ...` | Pulls transcript content into the local cache/filesystem | Use least-privilege data location and operator-owned storage |
| `gongctl sync calls --preset business` | Caches embedded CRM context that may include sensitive field values | Use only when that context is needed |
| `gongctl profile discover/show/import ...` | Discovery output and active profile state can expose tenant CRM field names, lifecycle terms, tracker/scorecard references, and evidence values | Keep profile files outside git; review before sharing |

In restricted/company mode, raw API, raw call JSON, extended call-context,
transcript/export, and extended sync command families are blocked by default and
require the explicit sensitive-export override. Profile commands remain
operator-only by policy because they can expose tenant metadata, but they are
not currently blocked by the restricted-mode gate.

Cache-retention delete commands now use a reviewed dry-run/confirmation shape.
They remain operator-only and should be paired with backup, legal-hold, and
data-owner approval records.

## Recommended Pilot Controls

- Run sync and export commands only on operator-controlled machines.
- Keep SQLite, transcript, and profile files in an external data directory, not under the checkout.
- Mount the MCP database read-only.
- Prefer the Docker MCP pattern with no network access.
- Treat MCP host apps as trusted recipients for any tool output they can request.
- Use aggregate-first MCP tools unless a user explicitly needs identifier-bearing or snippet-bearing results.

## Residual Risks

- The model is local-trust, not multi-tenant SaaS isolation. Anyone who can run the process or read the cache files can access tenant data allowed by the chosen tool.
- Read-only MCP prevents new writes, but it does not prevent exfiltration of already cached data.
- Some MCP tools still expose sensitive metadata such as call titles, call IDs, object IDs, scorecard IDs, workspace IDs, and question text.
- Transcript snippet tools and explicit CRM value lookups can still reveal sensitive content in bounded excerpts.
- The repo documents safe storage patterns, but it does not currently add encryption-at-rest, host-level DLP, remote revocation, centralized audit logging, OIDC, or per-user MCP RBAC.
- The MCP binary enforces a tool allowlist when configured, but MCP output still
  inherits the sensitivity of the cached dataset and the approved host.
- Docker hardening helps only if the host itself is trusted; Docker socket or host compromise bypasses container-level isolation.
