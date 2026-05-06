# Gong-native enrichment endpoint review (Phase 13k)

This document captures the Phase 13k decision matrix for adding Gong-native
enrichment paths to `gongctl` and `gongmcp`. It is intentionally narrow:
the slice that lands with this document only adds an opt-in raw capture flag
for AI Highlights/brief/next-step content. No MCP tool exposure, no new DB
schema, no new aggregate/dimension tools.

The review is meant to keep follow-up work reviewable. Treat each row as
"approved/queued/deferred for now"; do not add MCP exposure for any row
without a separate slice that includes a typed redacted read model and
governance/serving-DB filtering.

## Decision matrix

| Endpoint | Phase 13k status | What it adds | Why it is gated |
| --- | --- | --- | --- |
| `POST /v2/calls/extensive` with `contentSelector.exposedFields.highlights=true` | Approved as **opt-in raw-capture prototype only**. No MCP exposure yet. Capture goes only through `--include-highlights` + `--allow-sensitive-export`. | Returns the call's AI Highlights / brief / next steps under `content.highlights` (replacing the deprecated `pointsOfInterest` and `actionItems` fields). | Highlights and next steps can include customer-facing summaries and named individuals. They must be treated like participant fields: opt-in, restricted-mode blocked unless explicit, and stored only in raw call payloads until a typed redacted read model exists. |
| `POST /v2/calls/transcript` | Already supported. No change. | Authoritative transcript evidence for analyst tools. | Continues to be the evidence source; review/redaction for MCP exposure already exists. |
| `POST /v2/entities/get-brief` | **Queued as beta / operator-only review.** Not added to `gongctl` or `gongmcp` in Phase 13k. | Returns Gong's CRM-entity brief synthesized across calls, emails, and other signals. | The brief crosses call boundaries and may include AI summaries built from data outside any single call. Plan/scope validation, retention rules, and governance/serving-DB redaction need explicit review before caching or MCP exposure. |
| Topics, trackers, comments, interaction stats, outcomes | **Queued for follow-up review.** Classify likely safe-aggregate vs sensitive record-level exposure before any code work. | Could enable theme/coverage analyst flows, coaching aggregates, and outcome rollups. | Some of these are aggregate-friendly; others surface per-call or per-rep content. The exposure level must be classified per-endpoint, like the existing `internal/mcp` exposure matrix, before any MCP wiring. |

## Implementation note for the next slice

The next code slice after this one should:

1. Extract a typed, redacted `call_ai_highlights` table (or read model) from
   raw call payloads, not a fresh raw column.
2. Apply governance ingest filtering and redacted-serving-DB
   (`gongctl_mcp`) filtering to that read model so highlights for
   blocklisted calls never reach MCP.
3. Only after the read model and filters exist, add a facade operation
   (e.g. `evidence.highlights.list`) routed by the existing Phase 13e2
   facade registry (see `internal/mcp/facade.go` and
   `docs/mcp-data-exposure.md`). Do not expose raw highlight payloads
   directly through MCP.

Until that slice lands, the only sanctioned consumer of highlight content
is operator review of stored raw call JSON.

## Authorization contract for `--include-highlights`

`gongctl sync calls --include-highlights` is gated identically to
`--include-parties`:

- It is rejected when restricted mode is on unless the caller also passes
  `--allow-sensitive-export`.
- It is treated as sensitive in `gongctl sync run` step authorization
  (`syncRunStepRequiresSensitiveExport`), so dry-run output marks the step
  with `requires_sensitive_export: true` and restricted-mode preflight
  blocks the step before the DB is opened.
- It adds `include_highlights_requested=true` and
  `include_highlights_result=request_sent` markers to the sync-run
  request context, mirroring the existing parties markers, so operators
  can audit which sync runs requested AI summaries.

The flag does not introduce a new fallback path. If Gong rejects the
request, the caller must rerun without `--include-highlights`.

## Source notes

- Gong API: `POST /v2/calls/extensive` and `contentSelector.exposedFields`
  are documented at <https://app.gong.io/settings/api/documentation>
  (Gong's authenticated API documentation portal). The exact request shape
  used by this slice is encoded in `internal/gong/calls.go` and the
  `TestListCallsExposesPartiesAndHighlights` /
  `TestListCallsExposesHighlightsOnly` regression tests.
- Gong product docs note that the AI-driven Call Brief / next-step content
  is what `content.highlights` exposes; the older `pointsOfInterest` and
  `actionItems` fields are deprecated and should not be used by new code.
  Refer to Gong help center articles on Call Brief / Highlights for the
  product-side description (do not copy long help-center text into this
  repo).
- This slice does not call `/v2/entities/get-brief` or any of the
  topics/trackers/comments/interaction-stats/outcomes endpoints. They are
  only listed here so the next reviewer knows what is queued and not yet
  approved.
