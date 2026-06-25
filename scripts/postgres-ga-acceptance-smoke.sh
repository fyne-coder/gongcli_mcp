#!/usr/bin/env bash
#
# GA customer-acceptance smoke for a customer-hosted Postgres
# `business-workbench` MCP deployment.
#
# This script drives a deployed MCP endpoint (HTTPS JSON-RPC) and an optional
# scoped Postgres reader URL, collects a non-secret probe bundle, and runs the
# bundle through `gongctl mcp ga-acceptance` to produce a pass/degraded/fail
# report plus an operator-facing Markdown summary.
#
# It is read-only against the MCP endpoint and the supplied DB URL; it never
# writes secrets to the artifact directory.
#
# Required env:
#   MCP_URL            HTTPS JSON-RPC URL of the deployed MCP, e.g.
#                      https://mcp.example.com/mcp
#
# Optional env:
#   MCP_BEARER_TOKEN   bearer token sent as Authorization: Bearer ...
#   MCP_ORIGIN         optional Origin header for gateways/CORS.
#   READER_DB_URL      scoped Postgres reader URL for the read-only posture
#                      probe; when omitted the read_only_posture check is
#                      reported as degraded with reason "DB inputs not
#                      supplied".
#   RAW_TABLE_PROBE    raw source-only table name to probe denial against;
#                      defaults to calls_raw.
#   ARTIFACT_DIR       output directory; defaults to a private mktemp dir.
#   SAMPLE_QUESTION    synthetic non-customer-specific question used for the
#                      question.answer probe; defaults to a synthetic prompt.
#   SAMPLE_THEME_QUERY synthetic theme query used for evidence.call_drilldown;
#                      defaults to a synthetic generic term.
#   SAMPLE_FILTER_QUERY selective filter query used for question.answer;
#                      defaults to SAMPLE_THEME_QUERY.
#   GONGCTL_BIN        path to a gongctl binary; defaults to "gongctl" on PATH.
#   KEEP_ARTIFACTS=1   leave the artifact directory in place on success.
#
# Redaction audit (source-minus-redacted) evidence -- supply ONE of the
# following so the GA smoke records audit evidence in the probe bundle. When
# none of these is set the redaction_audit field is reported as
# {available:false} and the governance_redaction check is degraded:
#   REDACTION_AUDIT_JSON
#       Path to a JSON document with the shape
#         {"available": true,
#          "source_minus_redacted_rows": <int>,
#          "generated_at": "<RFC3339 timestamp>",
#          "evidence_path": "<non-secret pointer or summary>"}
#       The file is read from the operator's filesystem; nothing in it is
#       written to ARTIFACT_DIR beyond the assembled probe bundle.
#   REDACTION_AUDIT_SOURCE_MINUS_REDACTED_ROWS
#   REDACTION_AUDIT_GENERATED_AT
#   REDACTION_AUDIT_EVIDENCE_PATH
#       Compact direct env contract; supplying any of these flips
#       redaction_audit.available=true. SOURCE_MINUS_REDACTED_ROWS must parse
#       as a non-negative integer; if both REDACTION_AUDIT_JSON and the
#       direct env vars are set, REDACTION_AUDIT_JSON wins.
#
# Synthetic prompt and theme defaults are generic; they must not contain
# customer names, products, or restricted vocabulary.
#
# Exit codes:
#   0   pass or degraded (status field carries the distinction)
#   1   fail
#   2   environmental misconfiguration
#
set -euo pipefail
umask 077

require() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "missing required command: $1" >&2
    exit 2
  }
}

require curl
require jq

if [[ -z "${MCP_URL:-}" ]]; then
  echo "MCP_URL is required" >&2
  exit 2
fi

GONGCTL_BIN="${GONGCTL_BIN:-gongctl}"
if ! command -v "$GONGCTL_BIN" >/dev/null 2>&1 && [[ ! -x "$GONGCTL_BIN" ]]; then
  echo "gongctl binary not found at $GONGCTL_BIN" >&2
  exit 2
fi

ARTIFACT_DIR="${ARTIFACT_DIR:-$(mktemp -d /tmp/gongctl-ga-acceptance.XXXXXX)}"
mkdir -p "$ARTIFACT_DIR"
chmod 700 "$ARTIFACT_DIR"

PROBES_PATH="$ARTIFACT_DIR/probes.json"
REPORT_PATH="$ARTIFACT_DIR/report.json"
SUMMARY_PATH="$ARTIFACT_DIR/summary.md"

SAMPLE_QUESTION="${SAMPLE_QUESTION:-What pain themes did pilot accounts mention this quarter?}"
SAMPLE_THEME_QUERY="${SAMPLE_THEME_QUERY:-implementation effort}"
SAMPLE_FILTER_QUERY="${SAMPLE_FILTER_QUERY:-$SAMPLE_THEME_QUERY}"

cleanup() {
  if [[ "${KEEP_ARTIFACTS:-0}" != "1" ]]; then
    rm -rf "$ARTIFACT_DIR"
  fi
}
trap cleanup EXIT

mcp_call() {
  local method="$1"
  local params_json="${2:-}"
  if [[ -z "$params_json" ]]; then
    params_json='{}'
  fi
  local body
  body="$(jq -n \
    --arg method "$method" \
    --argjson params "$params_json" \
    '{jsonrpc:"2.0", id:1, method:$method, params:$params}')"
  local headers=(-H 'Content-Type: application/json')
  if [[ -n "${MCP_BEARER_TOKEN:-}" ]]; then
    headers+=(-H "Authorization: Bearer ${MCP_BEARER_TOKEN}")
  fi
  if [[ -n "${MCP_ORIGIN:-}" ]]; then
    headers+=(-H "Origin: ${MCP_ORIGIN}")
  fi
  curl -fsS "${headers[@]}" -d "$body" "$MCP_URL"
}

call_tool() {
  local name="$1"
  local args_json="$2"
  mcp_call "tools/call" "$(jq -n --arg name "$name" --argjson args "$args_json" '{name:$name, arguments:$args}')"
}

extract_text_payload() {
  jq -r '.result.content[0].text // ""'
}

build_redaction_audit_probe() {
  if [[ -n "${REDACTION_AUDIT_JSON:-}" ]]; then
    if [[ ! -r "$REDACTION_AUDIT_JSON" ]]; then
      echo "REDACTION_AUDIT_JSON is not readable: $REDACTION_AUDIT_JSON" >&2
      exit 2
    fi
    jq '
      {
        available: (.available // true),
        source_minus_redacted_rows: (.source_minus_redacted_rows // .sourceMinusRedactedRows // 0),
        generated_at: (.generated_at // .generatedAt // ""),
        evidence_path: (.evidence_path // .evidencePath // "")
      }
      | if (.source_minus_redacted_rows | type) != "number" or .source_minus_redacted_rows < 0 then
          error("source_minus_redacted_rows must be a non-negative number")
        else
          .
        end
    ' "$REDACTION_AUDIT_JSON"
    return
  fi

  if [[ -n "${REDACTION_AUDIT_SOURCE_MINUS_REDACTED_ROWS:-}" || -n "${REDACTION_AUDIT_GENERATED_AT:-}" || -n "${REDACTION_AUDIT_EVIDENCE_PATH:-}" ]]; then
    local rows="${REDACTION_AUDIT_SOURCE_MINUS_REDACTED_ROWS:-0}"
    if ! [[ "$rows" =~ ^[0-9]+$ ]]; then
      echo "REDACTION_AUDIT_SOURCE_MINUS_REDACTED_ROWS must be a non-negative integer" >&2
      exit 2
    fi
    jq -n \
      --argjson rows "$rows" \
      --arg generated_at "${REDACTION_AUDIT_GENERATED_AT:-}" \
      --arg evidence_path "${REDACTION_AUDIT_EVIDENCE_PATH:-}" \
      '{
        available: true,
        source_minus_redacted_rows: $rows
      }
      + (if $generated_at != "" then {generated_at: $generated_at} else {} end)
      + (if $evidence_path != "" then {evidence_path: $evidence_path} else {} end)'
    return
  fi

  jq -n '{available: false}'
}

echo "[ga-acceptance] artifact dir: $ARTIFACT_DIR" >&2

# 1. Initialize is optional for smokes that target a hosted MCP gateway. We
# rely on tools/list and tools/call directly; gongmcp accepts these without an
# explicit initialize handshake.

# 2. tools/list to determine the visible tool surface.
TOOLS_LIST_RAW="$ARTIFACT_DIR/tools-list.json"
mcp_call "tools/list" '{}' >"$TOOLS_LIST_RAW"
TOOL_NAMES_JSON="$(jq '[.result.tools[].name]' "$TOOLS_LIST_RAW")"

# 3. gong_status (status.sync) for runtime identity + readiness signals.
STATUS_RAW="$ARTIFACT_DIR/status.json"
call_tool "gong_status" '{}' >"$STATUS_RAW"
STATUS_JSON="$(extract_text_payload <"$STATUS_RAW" | jq 'if has("facade_status") and has("result") then .result else . end')"
if [[ -z "$STATUS_JSON" || "$STATUS_JSON" == "null" ]]; then
  echo "[ga-acceptance] gong_status returned no payload; aborting" >&2
  exit 2
fi

# 4. gong_discover_capabilities to enumerate routed operations.
CAPS_RAW="$ARTIFACT_DIR/capabilities.json"
call_tool "gong_discover_capabilities" '{}' >"$CAPS_RAW"
CAPS_JSON="$(extract_text_payload <"$CAPS_RAW" | jq '.')"
if [[ -z "$CAPS_JSON" || "$CAPS_JSON" == "null" ]]; then
  CAPS_JSON='{}'
fi
FACADE_OPERATIONS="$(jq '[.operations[]? | {operation:.operation, facade_tool:.facade_tool, routed_tool:.routed_tool}]' <<<"$CAPS_JSON")"
if [[ -z "$FACADE_OPERATIONS" || "$FACADE_OPERATIONS" == "null" ]]; then
  FACADE_OPERATIONS="[]"
fi

# 5. question.answer for the evidence-pack probe.
QA_RAW="$ARTIFACT_DIR/question-answer.json"
QA_ARGS="$(jq -n \
  --arg op "question.answer" \
  --arg q "$SAMPLE_QUESTION" \
  --arg filter_query "$SAMPLE_FILTER_QUERY" \
  --arg theme "$SAMPLE_THEME_QUERY" \
  '{operation:$op, arguments:{question:$q, filter:{query:$filter_query, transcript_status:"present", limit:25}, theme_query:$theme, limit:5}}')"
call_tool "gong_analyze" "$QA_ARGS" >"$QA_RAW" || true
QA_JSON="$(extract_text_payload <"$QA_RAW" | jq 'if has("facade_status") and has("result") then .result else . end' 2>/dev/null || echo 'null')"
qa_json_payload="${QA_JSON:-null}"
QA_HAS_PACK="$(jq -r '
  def count_items:
    ((.items? // []) | length)
    + ((.evidence_pack?.items? // []) | length)
    + ((.evidence? // []) | length)
    + ((.quotes? // []) | length)
    + ((.rows? // []) | length);
  if (.status? == "evidence_pack_ready") or ((.evidence_count? // 0) > 0) or (count_items > 0) then true else false end
' <<<"$qa_json_payload")"
QA_ITEM_COUNT="$(jq -r '[
  ((.items? // []) | length),
  ((.evidence_pack?.items? // []) | length),
  ((.evidence? // []) | length),
  ((.quotes? // []) | length),
  ((.rows? // []) | length),
  (.evidence_count? // 0)
] | max // 0' <<<"$qa_json_payload")"
QA_CALL_REFS="$(jq '[
  .items[]?.call_ref,
  .evidence_pack?.items[]?.call_ref,
  .evidence[]?.call_ref,
  .quotes[]?.call_ref,
  .rows[]?.call_ref
] | map(select(type == "string" and length > 0)) | unique' <<<"$qa_json_payload")"
if [[ -z "$QA_CALL_REFS" || "$QA_CALL_REFS" == "null" ]]; then
  QA_CALL_REFS="[]"
fi
QA_FIRST_CALL_REF="$(jq -r '.[0] // ""' <<<"$QA_CALL_REFS")"

# 6. evidence.call_drilldown using the first call_ref returned by
# question.answer (when one exists). The drill-down must be scoped to the same
# call.
DRILLDOWN_JSON='null'
if [[ -n "$QA_FIRST_CALL_REF" && "$QA_FIRST_CALL_REF" != "null" ]]; then
  DD_RAW="$ARTIFACT_DIR/call-drilldown.json"
  DD_ARGS="$(jq -n \
    --arg op "evidence.call_drilldown" \
    --arg ref "$QA_FIRST_CALL_REF" \
    --arg theme "$SAMPLE_THEME_QUERY" \
    '{operation:$op, arguments:{call_ref:$ref, theme_query:$theme, limit:5}}')"
  call_tool "gong_get_evidence" "$DD_ARGS" >"$DD_RAW" || true
  DRILLDOWN_JSON="$(extract_text_payload <"$DD_RAW" | jq 'if has("facade_status") and has("result") then .result else . end' 2>/dev/null || echo 'null')"
fi
drilldown_json_payload="${DRILLDOWN_JSON:-null}"
DRILLDOWN_PROBE='null'
if [[ "$DRILLDOWN_JSON" != "null" ]]; then
  DRILLDOWN_PROBE="$(jq --arg ref "$QA_FIRST_CALL_REF" '{
    call_ref: ($ref),
    bounded_snippet_count: ((.transcript_excerpts? // .verbatim_transcript_excerpts? // .snippets? // []) | length),
    gong_ai_source_path_count: ([
      .ai_condensed_evidence[]?.source_path,
      .gong_ai[]?.source_path,
      .ai_source_paths[]?,
      .source_paths[]?
    ] | map(select(type == "string" and length > 0)) | unique | length),
    snippets_scoped_to_call: (((.transcript_excerpts? // .verbatim_transcript_excerpts? // .snippets? // []) | all(.call_ref == $ref or .call_ref == null or $ref == "")) // true)
  }' <<<"$DRILLDOWN_JSON")"
fi

# 7. account_query without and with include_account_names. The fail-closed
# gate is part of the documented governance posture.
ACCT_NO_OPTIN_RAW="$ARTIFACT_DIR/account-query-no-optin.json"
ACCT_NO_OPTIN_ARGS='{"operation":"query.calls","arguments":{"filter":{"account_query":"placeholder_synthetic"},"include_account_names":false,"limit":5}}'
call_tool "gong_query" "$ACCT_NO_OPTIN_ARGS" >"$ACCT_NO_OPTIN_RAW" || true
ACCT_NO_OPTIN_IS_ERROR="$(jq -r '.result.isError // false' "$ACCT_NO_OPTIN_RAW")"
ACCT_NO_OPTIN_ERR="$(jq -r '.result.content[0].text // ""' "$ACCT_NO_OPTIN_RAW")"
ACCT_NO_OPTIN_PROBE="$(jq -n \
  --argjson is_error "$ACCT_NO_OPTIN_IS_ERROR" \
  --arg msg "$ACCT_NO_OPTIN_ERR" \
  '{is_error:$is_error, error:$msg}')"

ACCT_OPTIN_RAW="$ARTIFACT_DIR/account-query-optin.json"
ACCT_OPTIN_ARGS='{"operation":"query.calls","arguments":{"filter":{"account_query":"placeholder_synthetic"},"include_account_names":true,"limit":5}}'
call_tool "gong_query" "$ACCT_OPTIN_ARGS" >"$ACCT_OPTIN_RAW" || true
ACCT_OPTIN_IS_ERROR="$(jq -r '.result.isError // false' "$ACCT_OPTIN_RAW")"
ACCT_OPTIN_ROW_COUNT="$(extract_text_payload <"$ACCT_OPTIN_RAW" | jq -r '(if has("facade_status") and has("result") then .result else . end) | ((.items? | length) // (.calls? | length) // (.rows? | length) // 0)' 2>/dev/null || echo 0)"
ACCT_OPTIN_PROBE="$(jq -n \
  --argjson is_error "$ACCT_OPTIN_IS_ERROR" \
  --argjson row_count "$ACCT_OPTIN_ROW_COUNT" \
  '{is_error:$is_error, row_count:$row_count}')"

# 8. Raw call ID visibility heuristic. Scan question.answer + drill-down JSON
# for any field literally named "call_id". Because business-workbench is
# expected to redact raw call IDs, the heuristic flags any non-empty value.
RAW_ID_HIDDEN="true"
if jq -e '.. | objects | .call_id? | select((type == "string" and length > 0) or (type == "number"))' <<<"$qa_json_payload" >/dev/null 2>&1; then
  RAW_ID_HIDDEN="false"
fi
if jq -e '.. | objects | .call_id? | select((type == "string" and length > 0) or (type == "number"))' <<<"$drilldown_json_payload" >/dev/null 2>&1; then
  RAW_ID_HIDDEN="false"
fi

# 9. Optional read-only posture probe via psql. We attempt a write and a raw
# table read; both must be denied for a passing posture.
RO_PROBE='{"provided": false}'
if [[ -n "${READER_DB_URL:-}" ]]; then
  require psql
  reader_db_url="$READER_DB_URL"
  RAW_TABLE="${RAW_TABLE_PROBE:-calls_raw}"
  case "$RAW_TABLE" in
    ''|*[!A-Za-z0-9_]*)
      echo "RAW_TABLE_PROBE must contain only letters, numbers, and underscores" >&2
      exit 1
      ;;
  esac
  WRITE_DETAIL="$(psql "$reader_db_url" -v ON_ERROR_STOP=0 -X -tAc "UPDATE public.calls SET call_id = call_id WHERE false;" 2>&1 || true)"
  WRITE_DENIED="false"
  if grep -qiE 'permission denied|read.only|cannot execute UPDATE|must be owner|relation .* does not exist' <<<"$WRITE_DETAIL"; then
    WRITE_DENIED="true"
  fi
  RAW_DETAIL="$(psql "$reader_db_url" -v ON_ERROR_STOP=0 -X --set=raw_table="$RAW_TABLE" -tAc 'SELECT 1 FROM public.:"raw_table" LIMIT 1;' 2>&1 || true)"
  RAW_DENIED="false"
  if grep -qiE 'permission denied|relation .* does not exist' <<<"$RAW_DETAIL"; then
    RAW_DENIED="true"
  fi
  RO_PROBE="$(jq -n \
    --argjson write_denied "$WRITE_DENIED" \
    --arg write_detail "$WRITE_DETAIL" \
    --argjson raw_table_read_denied "$RAW_DENIED" \
    --arg raw_table_read_detail "$RAW_DETAIL" \
    '{provided:true, write_denied:$write_denied, write_denial_detail:$write_detail, raw_table_read_denied:$raw_table_read_denied, raw_table_read_detail:$raw_table_read_detail}')"
fi

REDACTION_AUDIT_PROBE="$(build_redaction_audit_probe)"

# 10. Assemble the probe bundle and hand to the evaluator. The evaluator
# emits the JSON report on stdout and the operator Markdown summary at
# --summary.
jq -n \
  --argjson status "$STATUS_JSON" \
  --argjson tools_list "$TOOL_NAMES_JSON" \
  --argjson facade_operations "$FACADE_OPERATIONS" \
  --arg sample_question "$SAMPLE_QUESTION" \
  --argjson qa_pack_present "$QA_HAS_PACK" \
  --argjson qa_call_refs "$QA_CALL_REFS" \
  --argjson qa_item_count "$QA_ITEM_COUNT" \
  --argjson drilldown "$DRILLDOWN_PROBE" \
  --argjson acct_no_optin "$ACCT_NO_OPTIN_PROBE" \
  --argjson acct_optin "$ACCT_OPTIN_PROBE" \
  --argjson raw_id_hidden "$RAW_ID_HIDDEN" \
  --argjson redaction_audit "$REDACTION_AUDIT_PROBE" \
  --argjson read_only "$RO_PROBE" \
  '{
    status: $status,
    tools_list: $tools_list,
    facade_operations: $facade_operations,
    question_answer: {
      question: $sample_question,
      evidence_pack_present: $qa_pack_present,
      call_refs: $qa_call_refs,
      item_count: $qa_item_count
    },
    call_drilldown: $drilldown,
    account_query_without_opt_in: $acct_no_optin,
    account_query_with_opt_in: $acct_optin,
    raw_call_ids_hidden: $raw_id_hidden,
    redaction_audit: $redaction_audit,
    read_only_posture: $read_only
  }' >"$PROBES_PATH"

set +e
"$GONGCTL_BIN" mcp ga-acceptance --probes "$PROBES_PATH" --summary "$SUMMARY_PATH" >"$REPORT_PATH"
EVAL_STATUS=$?
set -e

echo "[ga-acceptance] report: $REPORT_PATH" >&2
echo "[ga-acceptance] summary: $SUMMARY_PATH" >&2
if [[ -s "$SUMMARY_PATH" ]]; then
  cat "$SUMMARY_PATH"
fi

case "$EVAL_STATUS" in
  0) ;;
  1) exit 1 ;;
  *) exit 2 ;;
esac
