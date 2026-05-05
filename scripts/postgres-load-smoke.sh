#!/usr/bin/env bash
set -euo pipefail
umask 077

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE_FILE="$ROOT/docker-compose.postgres.yml"
PROJECT="${GONGCTL_POSTGRES_LOAD_COMPOSE_PROJECT:-gongctl-postgres-load-smoke}"
PORT="${GONGCTL_POSTGRES_LOAD_PORT:-55433}"
CALL_COUNT="${GONGCTL_POSTGRES_LOAD_CALLS:-750}"
case "$CALL_COUNT" in
  ''|*[!0-9]*)
    echo "GONGCTL_POSTGRES_LOAD_CALLS must be an integer" >&2
    exit 1
    ;;
esac
if [ "$CALL_COUNT" -lt 100 ]; then
  echo "GONGCTL_POSTGRES_LOAD_CALLS must be at least 100 for fixed probe call coverage" >&2
  exit 1
fi
export GONGCTL_POSTGRES_PORT="$PORT"
export GONGCTL_POSTGRES_PASSWORD="${GONGCTL_POSTGRES_PASSWORD:-gongctl_dev_password}"
export GONGMCP_READER_PASSWORD="${GONGMCP_READER_PASSWORD:-gongmcp_reader_dev_password}"
WRITER_URL="postgres://gongctl:${GONGCTL_POSTGRES_PASSWORD}@127.0.0.1:${PORT}/gongctl?sslmode=disable"
READER_URL="postgres://gongmcp_reader:${GONGMCP_READER_PASSWORD}@127.0.0.1:${PORT}/gongctl?sslmode=disable"

ARTIFACT_DIR="${GONGCTL_POSTGRES_LOAD_ARTIFACT_DIR:-$(mktemp -d /tmp/gongctl-postgres-load.XXXXXX)}"
mkdir -p "$ARTIFACT_DIR"
chmod 700 "$ARTIFACT_DIR"

SUMMARY_OUT="$ARTIFACT_DIR/summary.json"
COUNTS_OUT="$ARTIFACT_DIR/counts.txt"
REBUILD_OUT="$ARTIFACT_DIR/read-model-rebuild.json"
EXPLAIN_SEARCH_CALLS_OUT="$ARTIFACT_DIR/explain-search-calls.json"
EXPLAIN_GET_CALL_OUT="$ARTIFACT_DIR/explain-get-call.json"
EXPLAIN_TRANSCRIPT_OUT="$ARTIFACT_DIR/explain-transcript-search.json"
EXPLAIN_BUSINESS_OUT="$ARTIFACT_DIR/explain-business-pilot.json"
MCP_OUT="$ARTIFACT_DIR/mcp.jsonl"
OPERATOR_SMOKE_OUT="$ARTIFACT_DIR/operator-smoke.jsonl"
BUSINESS_PILOT_OUT="$ARTIFACT_DIR/business-pilot.jsonl"
STALE_MCP_OUT="$ARTIFACT_DIR/stale-mcp.txt"
READER_WRITE_OUT="$ARTIFACT_DIR/reader-write.txt"
READER_RAW_READ_OUT="$ARTIFACT_DIR/reader-raw-read.txt"
READER_SENSITIVE_READ_OUT="$ARTIFACT_DIR/reader-sensitive-read.txt"
READER_REGRANT_OUT="$ARTIFACT_DIR/reader-regrant.txt"
READER_NO_ROLE_VIEW_OUT="$ARTIFACT_DIR/reader-no-role-views.txt"

cd "$ROOT"

reader_psql() {
  docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres sh -s -- "$@" <<'SH'
set -eu
export PGPASSWORD="${GONGMCP_READER_PASSWORD:?}"
exec psql -h 127.0.0.1 -U gongmcp_reader -d gongctl "$@"
SH
}

assert_mcp_success() {
  local file="$1"
  shift
  python3 - "$file" "$@" <<'PY'
import json
import sys

path = sys.argv[1]
required = {int(value) for value in sys.argv[2:]}
seen = set()
with open(path, "r", encoding="utf-8") as handle:
    for line in handle:
        line = line.strip()
        if not line:
            continue
        try:
            message = json.loads(line)
        except json.JSONDecodeError:
            continue
        request_id = message.get("id")
        if request_id not in required:
            continue
        seen.add(request_id)
        if "error" in message:
            raise SystemExit(f"MCP id {request_id} returned JSON-RPC error: {message['error']}")
        result = message.get("result")
        if isinstance(result, dict) and result.get("isError") is True:
            raise SystemExit(f"MCP id {request_id} returned tool isError=true: {result}")

missing = required - seen
if missing:
    raise SystemExit(f"missing MCP result ids: {sorted(missing)}")
PY
}

cleanup() {
  docker compose -p "$PROJECT" -f "$COMPOSE_FILE" down -v >/dev/null 2>&1 || true
}
trap cleanup EXIT

docker compose -p "$PROJECT" -f "$COMPOSE_FILE" up -d postgres

for _ in $(seq 1 60); do
  if docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres pg_isready -U gongctl -d gongctl >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

GONG_DATABASE_URL="$WRITER_URL" go run ./cmd/gongctl sync synthetic >"$ARTIFACT_DIR/bootstrap.json"
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 -c "TRUNCATE call_read_model_diagnostics, postgres_read_model_state, call_facts, call_context_fields, call_context_objects, transcript_segments, transcripts, calls, users, sync_state, sync_runs RESTART IDENTITY CASCADE" >/dev/null
GONG_DATABASE_URL="$WRITER_URL" go run ./cmd/gongctl sync read-model --rebuild >"$ARTIFACT_DIR/initial-read-model.json"

load_start_seconds=$(date +%s)
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 -v call_count="$CALL_COUNT" <<'SQL'
WITH synthetic AS (
	SELECT gs AS n,
	       'load-call-' || lpad(gs::text, 6, '0') AS call_id,
	       to_char(timestamp '2026-02-01 12:00:00' + (gs * interval '1 minute'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"') AS started_at,
	       CASE gs % 5
		       WHEN 0 THEN 'Renewal'
		       WHEN 1 THEN 'Upsell'
		       WHEN 2 THEN 'New Business'
		       WHEN 3 THEN 'Partnership'
		       ELSE 'Customer Success'
	       END AS opportunity_type,
	       CASE gs % 4
		       WHEN 0 THEN 'Contract Review'
		       WHEN 1 THEN 'Discovery & Demo (SQO)'
		       WHEN 2 THEN 'Closed Won'
		       ELSE 'Business Case'
	       END AS opportunity_stage
	  FROM generate_series(1, :call_count) AS gs
)
INSERT INTO calls(call_id, title, started_at, duration_seconds, parties_count, context_present, raw_json, raw_sha256, first_seen_at, updated_at)
SELECT call_id,
       'Synthetic load call ' || n,
       started_at,
       600 + ((n % 8) * 300),
       2,
       true,
       jsonb_build_object(
	       'id', call_id,
	       'title', 'Synthetic load call ' || n,
	       'started', started_at,
	       'duration', 600 + ((n % 8) * 300),
	       'metaData', jsonb_build_object(
		       'scope', CASE WHEN n % 3 = 0 THEN 'Internal' ELSE 'External' END,
		       'system', CASE WHEN n % 2 = 0 THEN 'Zoom' ELSE 'Gong Connect' END,
		       'direction', CASE WHEN n % 4 = 0 THEN 'Outbound' ELSE 'Conference' END,
		       'purpose', 'Synthetic load performance validation',
		       'calendarEventId', 'synthetic-cal-' || n
	       ),
	       'context', jsonb_build_object(
		       'crmObjects', jsonb_build_array(
			       jsonb_build_object(
				       'type', 'Opportunity',
				       'id', 'synthetic-load-opp-' || n,
				       'name', 'Synthetic Load Opportunity ' || n,
				       'fields', jsonb_build_object(
					       'StageName', opportunity_stage,
					       'Type', opportunity_type,
					       'Forecast_Category_VP__c', 'Pipeline',
					       'Primary_Lead_Source__c', 'Synthetic Load'
				       )
			       ),
			       jsonb_build_object(
				       'type', 'Account',
				       'id', 'synthetic-load-account-' || ((n % 25) + 1),
				       'name', 'Synthetic Load Account ' || ((n % 25) + 1),
				       'fields', jsonb_build_object(
					       'Account_Type__c', CASE WHEN n % 5 = 0 THEN 'Partner' ELSE 'Customer - Active' END,
					       'Industry', CASE WHEN n % 2 = 0 THEN 'Healthcare' ELSE 'Manufacturing' END
				       )
			       )
		       )
	       )
       ),
       'synthetic-load-sha-' || n,
       started_at,
       started_at
  FROM synthetic;

WITH synthetic AS (
	SELECT gs AS n,
	       'load-call-' || lpad(gs::text, 6, '0') AS call_id,
	       to_char(timestamp '2026-02-01 12:00:00' + (gs * interval '1 minute'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"') AS started_at
	  FROM generate_series(1, :call_count) AS gs
)
INSERT INTO transcripts(call_id, raw_json, raw_sha256, segment_count, first_seen_at, updated_at)
SELECT call_id,
       jsonb_build_object('callId', call_id, 'synthetic', true),
       'synthetic-load-transcript-sha-' || n,
       2,
       started_at,
       started_at
  FROM synthetic;

WITH synthetic AS (
	SELECT gs AS n,
	       'load-call-' || lpad(gs::text, 6, '0') AS call_id
	  FROM generate_series(1, :call_count) AS gs
),
segments AS (
	SELECT n, call_id, 0 AS segment_index,
	       'speaker-1' AS speaker_id,
	       'This synthetic segment validates shared Postgres deployment search and read model performance for call ' || n AS text
	  FROM synthetic
	UNION ALL
	SELECT n, call_id, 1 AS segment_index,
	       'speaker-2' AS speaker_id,
	       'Renewal implementation evidence and transcript coverage are represented without customer data for call ' || n AS text
	  FROM synthetic
)
INSERT INTO transcript_segments(call_id, segment_index, speaker_id, start_ms, end_ms, text, raw_json)
SELECT call_id,
       segment_index,
       speaker_id,
       segment_index * 5000,
       (segment_index + 1) * 5000,
       text,
       jsonb_build_object('speakerId', speaker_id, 'text', text)
 FROM segments;
SQL
load_end_seconds=$(date +%s)
load_insert_seconds=$((load_end_seconds - load_start_seconds))

start_seconds=$(date +%s)
GONG_DATABASE_URL="$WRITER_URL" go run ./cmd/gongctl sync read-model --rebuild >"$REBUILD_OUT"
end_seconds=$(date +%s)
rebuild_command_seconds=$((end_seconds - start_seconds))

grep -q '"status": "rebuilt"' "$REBUILD_OUT"
grep -q '"ready": true' "$REBUILD_OUT"
grep -q "\"call_count\": $CALL_COUNT" "$REBUILD_OUT"
grep -q "\"fact_count\": $CALL_COUNT" "$REBUILD_OUT"

docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -tA -F '|' -c "
SELECT 'calls', COUNT(*) FROM calls
UNION ALL SELECT 'transcripts', COUNT(*) FROM transcripts
UNION ALL SELECT 'transcript_segments', COUNT(*) FROM transcript_segments
UNION ALL SELECT 'call_context_objects', COUNT(*) FROM call_context_objects
UNION ALL SELECT 'call_context_fields', COUNT(*) FROM call_context_fields
UNION ALL SELECT 'call_facts', COUNT(*) FROM call_facts
UNION ALL SELECT 'lifecycle_partner', COUNT(*) FROM call_facts WHERE lifecycle_bucket = 'partner'
UNION ALL SELECT 'lifecycle_upsell_expansion', COUNT(*) FROM call_facts WHERE lifecycle_bucket = 'upsell_expansion'
UNION ALL SELECT 'lifecycle_closed_won_lost_review', COUNT(*) FROM call_facts WHERE lifecycle_bucket = 'closed_won_lost_review'
UNION ALL SELECT 'lifecycle_active_sales_pipeline', COUNT(*) FROM call_facts WHERE lifecycle_bucket = 'active_sales_pipeline'
UNION ALL SELECT 'lifecycle_unknown', COUNT(*) FROM call_facts WHERE lifecycle_bucket = 'unknown'
UNION ALL SELECT 'read_model_missing', missing_fact_call_count FROM postgres_read_model_state WHERE model_name = 'builtin_call_facts'
UNION ALL SELECT 'read_model_orphan', orphan_fact_count FROM postgres_read_model_state WHERE model_name = 'builtin_call_facts'
ORDER BY 1" >"$COUNTS_OUT"
grep -q "calls|$CALL_COUNT" "$COUNTS_OUT"
grep -q "call_facts|$CALL_COUNT" "$COUNTS_OUT"
grep -q "call_context_objects|$((CALL_COUNT * 2))" "$COUNTS_OUT"
grep -q "call_context_fields|$((CALL_COUNT * 6))" "$COUNTS_OUT"
grep -q "transcript_segments|$((CALL_COUNT * 2))" "$COUNTS_OUT"
grep -Eq "lifecycle_partner\\|[1-9][0-9]*" "$COUNTS_OUT"
grep -Eq "lifecycle_upsell_expansion\\|[1-9][0-9]*" "$COUNTS_OUT"
grep -Eq "lifecycle_closed_won_lost_review\\|[1-9][0-9]*" "$COUNTS_OUT"
grep -Eq "lifecycle_active_sales_pipeline\\|[1-9][0-9]*" "$COUNTS_OUT"
grep -q "lifecycle_unknown|0" "$COUNTS_OUT"
grep -q "read_model_missing|0" "$COUNTS_OUT"
grep -q "read_model_orphan|0" "$COUNTS_OUT"

docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -tA -c "
EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)
SELECT jsonb_build_object(
	'id', c.call_id,
	'title', c.title,
	'started', c.started_at,
	'duration', c.duration_seconds,
	'parties', COALESCE((SELECT jsonb_agg(jsonb_build_object('id', 'redacted')) FROM generate_series(1::bigint, c.parties_count)), '[]'::jsonb)
)::text
  FROM calls c
 WHERE EXISTS (
       SELECT 1
         FROM call_facts cf
        WHERE cf.call_id = c.call_id
          AND cf.lifecycle_bucket = 'partner'
          AND cf.transcript_status = 'present'
 )
 ORDER BY c.started_at DESC, c.call_id
 LIMIT 20" >"$EXPLAIN_SEARCH_CALLS_OUT"

docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -tA -c "
EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)
SELECT o.object_type,
       o.object_id,
       COUNT(f.id) AS field_count,
       COUNT(CASE WHEN f.field_populated THEN 1 END) AS populated_field_count,
       COALESCE(string_agg(DISTINCT f.field_name, ',' ORDER BY f.field_name), '') AS field_names
  FROM call_context_objects o
  LEFT JOIN gongmcp_call_context_fields f
    ON f.call_id = o.call_id
   AND f.object_key = o.object_key
 WHERE o.call_id = 'load-call-000100'
 GROUP BY o.object_key, o.object_type, o.object_id
 ORDER BY o.object_type, o.object_id, o.object_key" >"$EXPLAIN_GET_CALL_OUT"

docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -tA -c "
EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)
SELECT ts.call_id,
       ts.speaker_id,
       ts.segment_index,
       ts.snippet
  FROM gongmcp_search_transcript_segments('shared Postgres deployment', 20) ts" >"$EXPLAIN_TRANSCRIPT_OUT"

docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -tA -c "
SELECT COUNT(*)
  FROM gongmcp_search_transcript_segments('shared Postgres deployment', 1000)" >"$ARTIFACT_DIR/transcript-search-limit-count.txt"
EXPECTED_TRANSCRIPT_SEARCH_LIMIT_COUNT=$CALL_COUNT
if [ "$EXPECTED_TRANSCRIPT_SEARCH_LIMIT_COUNT" -gt 1000 ]; then
  EXPECTED_TRANSCRIPT_SEARCH_LIMIT_COUNT=1000
fi
grep -q "^${EXPECTED_TRANSCRIPT_SEARCH_LIMIT_COUNT}$" "$ARTIFACT_DIR/transcript-search-limit-count.txt"

docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -tA -c "
EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)
SELECT transcript_status,
       COUNT(*) AS call_count,
       COALESCE(SUM(CASE WHEN lifecycle_bucket = 'renewal' THEN 1 ELSE 0 END), 0) AS renewal_count
  FROM call_facts
 GROUP BY transcript_status
 ORDER BY call_count DESC, transcript_status
 LIMIT 20" >"$EXPLAIN_BUSINESS_OUT"

for explain_file in "$EXPLAIN_SEARCH_CALLS_OUT" "$EXPLAIN_GET_CALL_OUT" "$EXPLAIN_TRANSCRIPT_OUT" "$EXPLAIN_BUSINESS_OUT"; do
  grep -q '"Plan"' "$explain_file"
  grep -q '"Execution Time"' "$explain_file"
  grep -q '"Actual Rows"' "$explain_file"
done
grep -Eq '"Index Name": "(idx_pg_call_facts_search_filters|idx_pg_call_facts_transcript_status|idx_pg_call_facts_lifecycle)"' "$EXPLAIN_SEARCH_CALLS_OUT"
grep -q '"Index Name": "idx_pg_call_context_objects_call_id"' "$EXPLAIN_GET_CALL_OUT"
grep -Eq '"Actual Rows": 20' "$EXPLAIN_SEARCH_CALLS_OUT"
grep -Eq '"Actual Rows": 20' "$EXPLAIN_TRANSCRIPT_OUT"
grep -Eq '"Actual Rows": [1-9][0-9]*' "$EXPLAIN_BUSINESS_OUT"
# Keep transcript and aggregate plan artifacts as evidence, but do not force a
# specific index shape at this bounded synthetic size. PostgreSQL can correctly
# choose sequential scans for small tables and all-row aggregates.

docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 >"$READER_NO_ROLE_VIEW_OUT" <<'SQL'
DROP VIEW IF EXISTS gongmcp_transcript_segments;
DROP FUNCTION IF EXISTS gongmcp_search_transcript_segments(text, integer);
DROP VIEW IF EXISTS gongmcp_call_context_fields;
DROP VIEW IF EXISTS gongmcp_sync_state;
DROP VIEW IF EXISTS gongmcp_sync_runs;
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'gongmcp_reader') THEN
    DROP OWNED BY gongmcp_reader;
    DROP ROLE gongmcp_reader;
  END IF;
END;
$$;
SQL
GONG_DATABASE_URL="$WRITER_URL" go run ./cmd/gongctl sync read-model --rebuild >>"$READER_NO_ROLE_VIEW_OUT"
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -tA >>"$READER_NO_ROLE_VIEW_OUT" <<'SQL'
SELECT COUNT(*)::int
  FROM (
    SELECT to_regclass('public.gongmcp_sync_runs') IS NOT NULL AS found
    UNION ALL SELECT to_regclass('public.gongmcp_sync_state') IS NOT NULL
    UNION ALL SELECT to_regclass('public.gongmcp_call_context_fields') IS NOT NULL
    UNION ALL SELECT to_regprocedure('public.gongmcp_search_transcript_segments(text, integer)') IS NOT NULL
  ) expected
 WHERE found;
SQL
grep -q '^4$' "$READER_NO_ROLE_VIEW_OUT"
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres sh -s >>"$READER_NO_ROLE_VIEW_OUT" <<'SH'
set -eu
psql -U gongctl -d gongctl -v reader_password="$GONGMCP_READER_PASSWORD" <<'SQL'
CREATE ROLE gongmcp_reader LOGIN PASSWORD :'reader_password';
SQL
SH

docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 -c "REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA public FROM gongmcp_reader" >"$READER_REGRANT_OUT"
GONG_DATABASE_URL="$WRITER_URL" go run ./cmd/gongctl sync read-model --rebuild >>"$READER_REGRANT_OUT"
grep -q '"status": "rebuilt"' "$READER_REGRANT_OUT"

if reader_psql -c "SELECT raw_json FROM calls LIMIT 1" >"$READER_RAW_READ_OUT" 2>&1; then
  echo "reader role unexpectedly read raw call JSON during load smoke" >&2
  exit 1
fi
if reader_psql -c "SELECT cursor FROM sync_runs LIMIT 1" >"$READER_SENSITIVE_READ_OUT" 2>&1; then
  echo "reader role unexpectedly read sync cursor during load smoke" >&2
  exit 1
fi
if reader_psql -c "SELECT field_value_text FROM call_context_fields LIMIT 1" >>"$READER_SENSITIVE_READ_OUT" 2>&1; then
  echo "reader role unexpectedly read normalized CRM field values during load smoke" >&2
  exit 1
fi
if reader_psql -c "SELECT object_name FROM call_context_objects LIMIT 1" >>"$READER_SENSITIVE_READ_OUT" 2>&1; then
  echo "reader role unexpectedly read normalized CRM object names during load smoke" >&2
  exit 1
fi
if reader_psql -c "SELECT text FROM transcript_segments LIMIT 1" >>"$READER_SENSITIVE_READ_OUT" 2>&1; then
  echo "reader role unexpectedly read full transcript text during load smoke" >&2
  exit 1
fi
if reader_psql -c "SELECT search_vector FROM transcript_segments LIMIT 1" >>"$READER_SENSITIVE_READ_OUT" 2>&1; then
  echo "reader role unexpectedly read transcript search vector during load smoke" >&2
  exit 1
fi
if reader_psql -c "SELECT * FROM gongmcp_transcript_segments LIMIT 1" >>"$READER_SENSITIVE_READ_OUT" 2>&1; then
  echo "reader role unexpectedly read retired transcript segment view during load smoke" >&2
  exit 1
fi
if reader_psql -c "SELECT opportunity_amount FROM call_facts LIMIT 1" >>"$READER_SENSITIVE_READ_OUT" 2>&1; then
  echo "reader role unexpectedly read sensitive opportunity amount during load smoke" >&2
  exit 1
fi

{
  printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_sync_status","arguments":{}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"search_calls","arguments":{"limit":5,"transcript_status":"present"}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"search_transcript_segments","arguments":{"query":"shared Postgres deployment","limit":5}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"get_call","arguments":{"call_id":"load-call-000100"}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"summarize_call_facts","arguments":{"group_by":"transcript_status","limit":5}}}'
} | GONG_DATABASE_URL="$READER_URL" GONGMCP_TOOL_ALLOWLIST=get_sync_status,search_calls,search_transcript_segments,get_call,summarize_call_facts go run ./cmd/gongmcp >"$MCP_OUT"

grep -q '"search_calls"' "$MCP_OUT"
assert_mcp_success "$MCP_OUT" 3 4 5 6 7
grep -q 'load-call-000100' "$MCP_OUT"
grep -q 'Synthetic load call' "$MCP_OUT"
grep -q 'shared.*Postgres.*deployment' "$MCP_OUT"
if grep -q 'raw_json\|crmObjects' "$MCP_OUT"; then
  echo "load MCP smoke exposed raw call payload fields" >&2
  exit 1
fi

{
  printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_sync_status","arguments":{}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"search_calls","arguments":{"limit":5,"transcript_status":"present"}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"search_transcript_segments","arguments":{"query":"shared Postgres deployment","limit":5}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"rank_transcript_backlog","arguments":{"limit":5}}}'
} | GONG_DATABASE_URL="$READER_URL" GONGMCP_TOOL_PRESET=operator-smoke go run ./cmd/gongmcp >"$OPERATOR_SMOKE_OUT"

grep -q '"get_sync_status"' "$OPERATOR_SMOKE_OUT"
grep -q '"search_calls"' "$OPERATOR_SMOKE_OUT"
grep -q '"search_transcript_segments"' "$OPERATOR_SMOKE_OUT"
grep -q '"rank_transcript_backlog"' "$OPERATOR_SMOKE_OUT"
assert_mcp_success "$OPERATOR_SMOKE_OUT" 3 4 5 6
if grep -q 'raw_json\|crmObjects' "$OPERATOR_SMOKE_OUT"; then
  echo "operator-smoke load MCP smoke exposed raw call payload fields" >&2
  exit 1
fi

{
  printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_sync_status","arguments":{}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"summarize_call_facts","arguments":{"group_by":"transcript_status","limit":5}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"summarize_calls_by_lifecycle","arguments":{}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"rank_transcript_backlog","arguments":{"limit":5}}}'
} | GONG_DATABASE_URL="$READER_URL" GONGMCP_TOOL_PRESET=business-pilot go run ./cmd/gongmcp >"$BUSINESS_PILOT_OUT"

grep -q '"summarize_call_facts"' "$BUSINESS_PILOT_OUT"
grep -q '"summarize_calls_by_lifecycle"' "$BUSINESS_PILOT_OUT"
grep -q '"rank_transcript_backlog"' "$BUSINESS_PILOT_OUT"
assert_mcp_success "$BUSINESS_PILOT_OUT" 3 4 5 6

if reader_psql -c "INSERT INTO users(user_id, raw_json, raw_sha256, first_seen_at, updated_at) VALUES('load-should-fail', '{}'::jsonb, 'x', now()::text, now()::text)" >"$READER_WRITE_OUT" 2>&1; then
  echo "reader role unexpectedly wrote to Postgres during load smoke" >&2
  exit 1
fi

docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -c "DELETE FROM call_facts WHERE call_id = 'load-call-000100'" >/dev/null
if GONG_DATABASE_URL="$READER_URL" GONGMCP_TOOL_PRESET=business-pilot go run ./cmd/gongmcp </dev/null >"$STALE_MCP_OUT" 2>&1; then
  echo "read-only MCP unexpectedly started with a stale load read model" >&2
  exit 1
fi
grep -q 'postgres read model is missing or stale' "$STALE_MCP_OUT"

cat >"$SUMMARY_OUT" <<JSON
{
  "status": "ok",
  "artifact_dir": "$ARTIFACT_DIR",
  "synthetic_calls": $CALL_COUNT,
  "synthetic_transcript_segments": $((CALL_COUNT * 2)),
  "bulk_insert_command_seconds": $load_insert_seconds,
  "read_model_rebuild_command_seconds": $rebuild_command_seconds,
  "decision": "Bounded serial rebuild and read-path smoke passed at this synthetic size. EXPLAIN artifacts prove analyzed execution and expected selective index use where stable at this scale; this is not a concurrent contention benchmark, so keep larger customer-scale benchmarking queued before GA."
}
JSON

grep -q '"status": "ok"' "$SUMMARY_OUT"
grep -q "\"synthetic_calls\": $CALL_COUNT" "$SUMMARY_OUT"

echo "postgres load smoke passed"
echo "artifact directory: $ARTIFACT_DIR"
echo "summary output: $SUMMARY_OUT"
echo "counts output: $COUNTS_OUT"
echo "read model rebuild output: $REBUILD_OUT"
echo "search_calls explain output: $EXPLAIN_SEARCH_CALLS_OUT"
echo "get_call explain output: $EXPLAIN_GET_CALL_OUT"
echo "transcript search explain output: $EXPLAIN_TRANSCRIPT_OUT"
echo "business-pilot explain output: $EXPLAIN_BUSINESS_OUT"
echo "mcp output: $MCP_OUT"
echo "operator-smoke output: $OPERATOR_SMOKE_OUT"
echo "business-pilot output: $BUSINESS_PILOT_OUT"
echo "reader denial output: $READER_WRITE_OUT"
echo "reader raw-read denial output: $READER_RAW_READ_OUT"
echo "reader sensitive-read denial output: $READER_SENSITIVE_READ_OUT"
echo "reader regrant output: $READER_REGRANT_OUT"
echo "reader no-role view output: $READER_NO_ROLE_VIEW_OUT"
echo "stale MCP denial output: $STALE_MCP_OUT"
