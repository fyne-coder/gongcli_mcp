# Gong-native enrichment endpoint review (Phase 13k)

This document captures the Phase 13k decision matrix for adding Gong-native
enrichment paths to `gongctl` and `gongmcp`. Phase 13k added an opt-in raw
capture flag for AI Highlights/brief/next-step content. The follow-up
read-model slice adds a typed Postgres `call_ai_highlights` table populated
from `content.highlights`. Phase 13k2 wires that read model behind the
existing stable facade as the reviewed `evidence.highlights.list` operation
routed through `gong_get_evidence`; no new top-level MCP tool was added and
no aggregate/dimension tools were introduced.

The review is meant to keep follow-up work reviewable. Treat each row as
"approved/queued/deferred for now"; do not add MCP exposure for any row
without a separate slice that includes a typed redacted read model and
governance/serving-DB filtering.

## Decision matrix

| Endpoint | Phase 13k status | What it adds | Why it is gated |
| --- | --- | --- | --- |
| `POST /v2/calls/extensive` with `contentSelector.exposedFields.content.{brief,highlights,keyPoints,outline,callOutcome}=true` | Approved as **opt-in raw capture plus typed Postgres read model plus reviewed facade operation**. The MCP surface is the `evidence.highlights.list` operation routed through `gong_get_evidence` (no new top-level tool). Capture still goes through `--include-highlights` + `--allow-sensitive-export`; `call_ai_highlights` is populated during Postgres read-model refresh. | Returns Gong Spotlight/generated sections under `content.brief`, `content.highlights`, `content.keyPoints`, `content.outline`, and sometimes `content.callOutcome` (replacing the deprecated `pointsOfInterest` and `actionItems` fields). | Highlights, briefs, key points, outlines, and next steps can include customer-facing summaries and named individuals. They must be treated like participant fields: opt-in, restricted-mode blocked unless explicit, excluded for governed/skipped calls, and copied into the redacted serving DB only for non-restricted calls. The facade operation requires explicit `call_ids`, returns only typed columns (no raw JSON), and never enumerates the full call set. |
| `POST /v2/calls/transcript` | Already supported. No change. | Authoritative transcript evidence for analyst tools. | Continues to be the evidence source; review/redaction for MCP exposure already exists. |
| `POST /v2/entities/get-brief` | **Queued as beta / operator-only review.** Not added to `gongctl` or `gongmcp` in Phase 13k. | Returns Gong's CRM-entity brief synthesized across calls, emails, and other signals. | The brief crosses call boundaries and may include AI summaries built from data outside any single call. Plan/scope validation, retention rules, and governance/serving-DB redaction need explicit review before caching or MCP exposure. |
| Topics, trackers, comments, interaction stats, outcomes | **Queued for follow-up review.** Classify likely safe-aggregate vs sensitive record-level exposure before any code work. | Could enable theme/coverage analyst flows, coaching aggregates, and outcome rollups. | Some of these are aggregate-friendly; others surface per-call or per-rep content. The exposure level must be classified per-endpoint, like the existing `internal/mcp` exposure matrix, before any MCP wiring. |

## Implementation status and next slice

Implemented in the read-model follow-up:

1. Extracted a typed `call_ai_highlights` Postgres read model from raw call
   payloads, not a fresh raw column.
2. Kept `call_ai_highlights` out of generic read-only grants; read-only
   validation treats it as sensitive until a reviewed facade operation exists.
3. Applied redacted-serving-DB filtering by rebuilding the table only from
   allowed calls in `gongctl_mcp`; restricted calls have no highlight rows.

Implemented in the Phase 13k2 facade slice:

1. Added a reviewed `evidence.highlights.list` facade operation routed by
   the existing Phase 13e2 facade registry (`internal/mcp/facade.go`) and
   surfaced only through `gong_get_evidence`. The operation has an internal
   handler — there is no new top-level MCP tool.
2. Bounded the surface: `call_ids` is required and capped, `limit` is
   bounded, returned columns are restricted to typed `call_id`,
   `highlight_index`, `highlight_type`, `highlight_text`, `source_path`,
   and `updated_at`, and raw highlight JSON is never returned.
3. Reused the existing governance/serving-DB boundary: restricted calls
   remain absent because the redacted Postgres serving DB has no rows for
   them, and runtime-suppressed call IDs are filtered before rows leave
   the server.
4. Result envelope carries explicit caveats stating that highlights are
   Gong AI-generated accelerators and that transcript quotes remain primary
   evidence.

Future slices may consider:

1. Highlight-specific aggregate/dimension queries (deferred until a need
   arises; transcript quotes remain the stronger evidentiary layer).
2. Adding a SECURITY DEFINER Postgres function around `call_ai_highlights`
   if/when read-only column-grant boundaries are tightened further.

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
