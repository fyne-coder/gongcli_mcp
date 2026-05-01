# AI Governance Customer Exclusion

`gongctl` can apply a private AI governance YAML file to produce an MCP-facing
SQLite copy that excludes calls linked to configured customer names, aliases,
domains, or email fragments before results reach an MCP host or LLM.

The public repo must not contain a company's real restricted-customer list. Keep
the real file in a private mounted path, secret-managed config volume, or
operator-controlled data directory.

## Config Shape

Use exact customer names and explicit aliases:

```yaml
version: 1
lists:
  no_ai:
    description: Customers requiring per-use consent before AI processing.
    customers:
      - name: "Example Restricted Corp"
        aliases:
          - "Example Restricted Corporation"
  notification_required:
    description: Customers excluded until third-party notification is complete.
    customers:
      - name: "Example Notice Customer"
        aliases:
          - "Example Notice Co"
```

Matching is deterministic and local:

- lowercase
- trim whitespace
- collapse punctuation and whitespace
- normalized token-phrase matching

There is no fuzzy matching and no LLM decision in the enforcement path. If an
audit shows a near variant that should be excluded, add it as an explicit alias.
Short names are matched on token boundaries, so `XYZ` matches `XYZ Process
Automation` but does not match `Xylophone Yard Zone`. Add domains such as
`example.com` as aliases when email-domain matches should also exclude a call.

## Audit Before MCP Use

Run the audit locally before enabling the MCP server:

```bash
gongctl governance audit \
  --db /srv/gongctl/gong.db \
  --config /srv/gongctl/private/ai-governance.yaml
```

The audit is operator-facing and may show configured customer names. Use it to
verify configured entry and alias counts, matched entries, unmatched entries
that may need aliases, and suppressed call count. Do not write audit output to
shared CI logs or public support tickets.

Use JSON for automation:

```bash
gongctl governance audit \
  --db /srv/gongctl/gong.db \
  --config /srv/gongctl/private/ai-governance.yaml \
  --json
```

## Filtered MCP Database

For MCP/LLM use, the preferred deployment is a physically filtered SQLite copy:

```bash
gongctl governance export-filtered-db \
  --db /srv/gongctl/gong.db \
  --config /srv/gongctl/private/ai-governance.yaml \
  --out /srv/gongctl/gong-mcp-governed.db
```

The source DB remains the complete operator cache. The filtered export scans
call titles, raw call payloads including participant emails, embedded CRM object
names, all cached CRM field values, and transcript segment text. It removes
matched call-dependent rows from `calls`, transcripts, transcript FTS, embedded
CRM context, and profile call-fact cache tables, then compacts the copy. Point
MCP hosts at the filtered DB by default whenever a blocklist exists:

```bash
gongmcp --db /srv/gongctl/gong-mcp-governed.db
```

This recovers more MCP tool capability because the server is not reading the raw
restricted call rows. Recreate the filtered DB after every sync or governance
config change. Keep the filtered DB outside Git and treat it as customer data.

The filtered export is call-record filtering. It does not delete unrelated
global configuration rows such as scorecards, trackers, workspaces, CRM schema
metadata, or internal user rows unless they are tied to a suppressed call. If
the policy is "no occurrence anywhere in any configuration metadata," add a
separate settings/schema/profile metadata scan before MCP use.

## Raw-DB MCP Enforcement

Raw-DB governance is a fallback when a filtered DB has not been generated. Start
`gongmcp` with the same private config:

AI governance mode requires an explicit governance-compatible
`--tool-allowlist` or `GONGMCP_TOOL_ALLOWLIST`. Unsupported aggregate/config
tools are refused while the filter is active. Directed CRM value lookup through
`search_crm_field_values` is also refused in governance mode because it can
answer whether a restricted customer name or legal-name variant is present in
cached CRM fields.

```bash
GONGMCP_AI_GOVERNANCE_CONFIG=/srv/gongctl/private/ai-governance.yaml \
GONGMCP_TOOL_ALLOWLIST=search_calls,search_transcript_segments,rank_transcript_backlog,get_call \
  gongmcp --db /srv/gongctl/gong.db
```

HTTP private pilots use the same config variable:

```bash
GONGMCP_AI_GOVERNANCE_CONFIG=/srv/gongctl/private/ai-governance.yaml \
GONGMCP_BEARER_TOKEN_FILE=/run/secrets/gongmcp_token \
GONGMCP_TOOL_ALLOWLIST=search_calls,search_transcript_segments,rank_transcript_backlog,get_call \
  gongmcp --http 127.0.0.1:8080 --auth-mode bearer --db /srv/gongctl/gong.db
```

MCP responses do not include configured restricted customer names or filtered
match counts. Aggregate tools that are not yet recomputed over the filtered call
set fail closed instead of returning counts that include excluded customers.

Restart `gongmcp` after cache refreshes or config changes. This is mandatory:
`gongmcp` fingerprints the config and cache at startup, checks that fingerprint
on each tool call, and fails closed if either changes while the process is
running.

By default, `gongmcp` refuses governance configs with unmatched entries. Use
`--allow-unmatched-ai-governance` only after the local audit confirms the
unmatched entries are expected for the current cache.

## Current Boundary

Filtered export physically removes matched call-dependent rows from the MCP copy
only; it does not delete data from the source operator SQLite cache. Raw-DB
governance remains query/output-time suppression and does not prevent local
operators from running raw CLI cache inspection commands. Environments that
cannot store restricted customer data at all should add sync-time exclusion
before ingesting those records.
