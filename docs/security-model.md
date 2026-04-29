# Security Model

## Scope

This document covers the enterprise-pilot security posture of the local `gongctl` CLI and the local `gongmcp` stdio server as implemented in this worktree on 2026-04-29.

The current design is intentionally local-first:

- `gongctl` is the live Gong API client and the only surface that should handle Gong credentials.
- `gongmcp` reads a previously synced SQLite cache and does not call Gong directly.
- The repository can be public; tenant data, credentials, and local cache files cannot.

Current enforcement limits are important: `gongmcp` does not yet implement
server-side tool allowlisting, and `gongctl` does not yet implement a
restricted/company mode. Business-user tool subsets and keeping users off the
CLI are currently host, wrapper, filesystem, and operator-process controls.

## Trust Boundaries

| Boundary | Components | Allowed data | Primary controls |
| --- | --- | --- | --- |
| Gong API boundary | `internal/gong`, `internal/auth`, `gongctl sync ...`, `gongctl calls ...`, `gongctl api raw ...` | Live Gong API responses, credentials in process memory | HTTPS to Gong, documented rate limiting, operator-supplied credentials |
| Local operator boundary | shell, `.env`, exported env vars, CLI stdout/stderr, local files | Full tenant data when the operator runs live or export commands | User/workstation access control, keep secrets out of git, keep outputs outside the repo |
| Local data-store boundary | SQLite cache, transcript JSON files, tenant profile YAML | Cached call metadata, CRM context, transcript snippets/full transcripts, settings inventory, profile state | External data directory, read-only mount for MCP, repo ignores local data as a safety net |
| MCP boundary | `gongmcp`, `internal/mcp`, connected host app/model | Read-only SQLite query results only | SQLite opened read-only, no live Gong credentials required, no raw API passthrough, no write tools |
| Public source boundary | repo source, docs, tests, examples | Sanitized code and docs only | No live tenant names, transcripts, IDs, secrets, or private local paths in tracked files |

## Credential Flow

1. The operator provides Gong credentials through exported environment variables or an ignored `.env` file.
2. `gongctl` reads those credentials and uses them only for live API calls.
3. Sync commands write cached results to a local SQLite database and optional local transcript/profile files.
4. `gongmcp` starts later with `--db PATH`, opens that SQLite file read-only, and serves stdio MCP requests without Gong credentials.

Operational implications:

- `gongmcp` should not be given Gong API secrets.
- Docker MCP runs should use a read-only data mount and `--network none`.
- Shared hosts should avoid long-lived plaintext environment variables when possible because container inspection can expose them.

## Data Classification

| Class | Examples in this repo shape | Default surfaces | Handling expectation |
| --- | --- | --- | --- |
| Restricted secrets | Gong access key, Gong access key secret, future OAuth secrets or refresh tokens | CLI environment only | Never commit, never place in docs, keep out of MCP |
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

## High-Risk CLI Commands

These commands are valid operator tools, but they should be treated as restricted during an enterprise pilot because they can surface or create sensitive tenant data:

| Command family | Why it is high risk | Control expectation |
| --- | --- | --- |
| `gongctl api raw ...` | Bypasses typed minimization and can return raw Gong payloads | Use only for operator debugging; keep output local |
| `gongctl calls show --json` | Returns raw cached call detail | Do not paste into tickets, docs, or prompts |
| `gongctl calls list/export --context extended ...` | Can emit embedded CRM context and customer identifiers at scale | Write to external files only; review before sharing |
| `gongctl calls transcript ...` and `gongctl calls transcript-batch ...` | Produces transcript payloads and transcript files | Store outside the repo and outside shared logs |
| `gongctl sync transcripts ...` | Pulls transcript content into the local cache/filesystem | Use least-privilege data location and operator-owned storage |
| `gongctl sync calls --preset business` | Caches embedded CRM context that may include sensitive field values | Use only when that context is needed |
| `gongctl profile discover/show/import ...` | Discovery output and active profile state can expose tenant CRM field names, lifecycle terms, tracker/scorecard references, and evidence values | Keep profile files outside git; review before sharing |

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
- The repo documents safe storage patterns, but it does not currently add encryption-at-rest, host-level DLP, remote revocation, centralized audit logging, or MCP-side authentication/RBAC.
- The repo does not yet enforce an MCP tool allowlist or restricted CLI mode in
  the binaries, so company pilots rely on trusted host configuration and
  operator access control.
- Docker hardening helps only if the host itself is trusted; Docker socket or host compromise bypasses container-level isolation.
