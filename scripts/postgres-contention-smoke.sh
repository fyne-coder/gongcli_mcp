#!/usr/bin/env bash
set -euo pipefail
umask 077

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE_FILE="$ROOT/docker-compose.postgres.yml"
PROJECT="${GONGCTL_POSTGRES_CONTENTION_COMPOSE_PROJECT:-gongctl-postgres-contention-smoke-$$}"
PORT="${GONGCTL_POSTGRES_CONTENTION_PORT:-55434}"
CALL_COUNT="${GONGCTL_POSTGRES_CONTENTION_CALLS:-1200}"
PURGE_COUNT="${GONGCTL_POSTGRES_CONTENTION_PURGE_CALLS:-120}"
LOCK_HOLD_SECONDS="${GONGCTL_POSTGRES_CONTENTION_LOCK_HOLD_SECONDS:-1.5}"
OPERATION_TIMEOUT_SECONDS="${GONGCTL_POSTGRES_CONTENTION_OPERATION_TIMEOUT_SECONDS:-90}"

case "$CALL_COUNT" in
  ''|*[!0-9]*)
    echo "GONGCTL_POSTGRES_CONTENTION_CALLS must be an integer" >&2
    exit 1
    ;;
esac
case "$PURGE_COUNT" in
  ''|*[!0-9]*)
    echo "GONGCTL_POSTGRES_CONTENTION_PURGE_CALLS must be an integer" >&2
    exit 1
    ;;
esac
if [ "$CALL_COUNT" -lt 300 ]; then
  echo "GONGCTL_POSTGRES_CONTENTION_CALLS must be at least 300" >&2
  exit 1
fi
if [ "$PURGE_COUNT" -lt 1 ] || [ "$PURGE_COUNT" -ge "$CALL_COUNT" ]; then
  echo "GONGCTL_POSTGRES_CONTENTION_PURGE_CALLS must be greater than 0 and less than call count" >&2
  exit 1
fi
case "$PROJECT" in
  ''|*[!a-zA-Z0-9_-]*)
    echo "GONGCTL_POSTGRES_CONTENTION_COMPOSE_PROJECT must contain only letters, numbers, underscores, or dashes" >&2
    exit 1
    ;;
  gongctl-postgres-contention*)
    ;;
  *)
    echo "GONGCTL_POSTGRES_CONTENTION_COMPOSE_PROJECT must start with gongctl-postgres-contention" >&2
    exit 1
    ;;
esac
if ! python3 - "$LOCK_HOLD_SECONDS" <<'PY'
import sys

try:
    value = float(sys.argv[1])
except ValueError:
    raise SystemExit(1)
if not (0.25 <= value <= 10):
    raise SystemExit(1)
PY
then
  echo "GONGCTL_POSTGRES_CONTENTION_LOCK_HOLD_SECONDS must be numeric between 0.25 and 10" >&2
  exit 1
fi
case "$OPERATION_TIMEOUT_SECONDS" in
  ''|*[!0-9]*)
    echo "GONGCTL_POSTGRES_CONTENTION_OPERATION_TIMEOUT_SECONDS must be an integer" >&2
    exit 1
    ;;
esac
if [ "$OPERATION_TIMEOUT_SECONDS" -lt 15 ] || [ "$OPERATION_TIMEOUT_SECONDS" -gt 600 ]; then
  echo "GONGCTL_POSTGRES_CONTENTION_OPERATION_TIMEOUT_SECONDS must be between 15 and 600" >&2
  exit 1
fi

export GONGCTL_POSTGRES_PORT="$PORT"
export GONGCTL_POSTGRES_USER="${GONGCTL_POSTGRES_USER:-gongctl}"
export GONGCTL_POSTGRES_PASSWORD="${GONGCTL_POSTGRES_PASSWORD:-gongctl_dev_password}"
export GONGMCP_READER_PASSWORD="${GONGMCP_READER_PASSWORD:-gongmcp_reader_dev_password}"

urlencode() {
  python3 -c 'from urllib.parse import quote; import sys; print(quote(sys.argv[1], safe=""))' "$1"
}

WRITER_URL="postgres://$(urlencode "$GONGCTL_POSTGRES_USER"):$(urlencode "$GONGCTL_POSTGRES_PASSWORD")@127.0.0.1:${PORT}/gongctl?sslmode=disable"
READER_URL="postgres://gongmcp_reader:$(urlencode "$GONGMCP_READER_PASSWORD")@127.0.0.1:${PORT}/gongctl?sslmode=disable"

url_with_app() {
  python3 - "$1" "$2" <<'PY'
from urllib.parse import parse_qsl, urlencode, urlsplit, urlunsplit
import sys

url, app = sys.argv[1:]
parts = urlsplit(url)
query = dict(parse_qsl(parts.query, keep_blank_values=True))
query["application_name"] = app
print(urlunsplit((parts.scheme, parts.netloc, parts.path, urlencode(query), parts.fragment)))
PY
}

WRITER_REBUILD_URL="$(url_with_app "$WRITER_URL" "contention_read_model_rebuild")"
WRITER_PROFILE_URL="$(url_with_app "$WRITER_URL" "contention_profile_import_refresh")"
WRITER_PURGE_URL="$(url_with_app "$WRITER_URL" "contention_purge_confirm")"
READER_STATUS_URL="$(url_with_app "$READER_URL" "contention_reader_sync_status")"

if [ -n "${GONGCTL_POSTGRES_CONTENTION_ARTIFACT_DIR:-}" ]; then
  ARTIFACT_DIR="$GONGCTL_POSTGRES_CONTENTION_ARTIFACT_DIR"
  case "$ARTIFACT_DIR" in
    /tmp/gongctl-postgres-contention.*)
      ;;
    *)
      echo "GONGCTL_POSTGRES_CONTENTION_ARTIFACT_DIR must be under /tmp/gongctl-postgres-contention.*" >&2
      exit 1
      ;;
  esac
  if [ -L "$ARTIFACT_DIR" ]; then
    echo "GONGCTL_POSTGRES_CONTENTION_ARTIFACT_DIR must not be a symlink" >&2
    exit 1
  fi
  mkdir -p "$ARTIFACT_DIR"
else
  ARTIFACT_DIR="$(mktemp -d /tmp/gongctl-postgres-contention.XXXXXX)"
fi
chmod 700 "$ARTIFACT_DIR"

GONGCTL_BIN="$ARTIFACT_DIR/gongctl"
GONGMCP_BIN="$ARTIFACT_DIR/gongmcp"
PROFILE_FILE="$ARTIFACT_DIR/business-profile.yaml"
POLICY_FILE="$ARTIFACT_DIR/retention-policy.yaml"
SUMMARY_OUT="$ARTIFACT_DIR/summary.json"
COUNTS_BEFORE_OUT="$ARTIFACT_DIR/counts-before.txt"
COUNTS_AFTER_OUT="$ARTIFACT_DIR/counts-after.txt"
PROFILE_CACHE_BEFORE_OUT="$ARTIFACT_DIR/profile-cache-before.txt"
PROFILE_CACHE_AFTER_OUT="$ARTIFACT_DIR/profile-cache-after.txt"
PROFILE_IMPORT_BEFORE_OUT="$ARTIFACT_DIR/profile-import-before.json"
READ_MODEL_BEFORE_OUT="$ARTIFACT_DIR/read-model-before.json"
READ_MODEL_AFTER_OUT="$ARTIFACT_DIR/read-model-after.json"
OP_RESULTS_OUT="$ARTIFACT_DIR/operation-results.jsonl"
LOCK_SAMPLES_OUT="$ARTIFACT_DIR/lock-samples.txt"
MCP_AFTER_OUT="$ARTIFACT_DIR/mcp-after.jsonl"
PURGE_DRY_RUN_OUT="$ARTIFACT_DIR/purge-policy-dry-run.json"
PURGE_CONFIRM_OUT="$ARTIFACT_DIR/purge-confirm.json"
READER_WRITE_DENIED_OUT="$ARTIFACT_DIR/reader-write-denied.txt"
READER_RAW_READ_DENIED_OUT="$ARTIFACT_DIR/reader-raw-read-denied.txt"
READER_TRANSCRIPT_RAW_DENIED_OUT="$ARTIFACT_DIR/reader-transcript-raw-denied.txt"
READER_TRANSCRIPT_TEXT_DENIED_OUT="$ARTIFACT_DIR/reader-transcript-text-denied.txt"
READER_TRANSCRIPT_VECTOR_DENIED_OUT="$ARTIFACT_DIR/reader-transcript-vector-denied.txt"
READER_FIELD_VALUE_DENIED_OUT="$ARTIFACT_DIR/reader-field-value-denied.txt"
READER_PROFILE_TABLE_DENIED_OUT="$ARTIFACT_DIR/reader-profile-table-denied.txt"
LOCK_HOLDER_OUT="$ARTIFACT_DIR/writer-lock-holder.txt"
SAMPLER_DONE="$ARTIFACT_DIR/.sampler-done"

rm -f \
  "$SUMMARY_OUT" \
  "$COUNTS_BEFORE_OUT" \
  "$COUNTS_AFTER_OUT" \
  "$PROFILE_CACHE_BEFORE_OUT" \
  "$PROFILE_CACHE_AFTER_OUT" \
  "$PROFILE_IMPORT_BEFORE_OUT" \
  "$READ_MODEL_BEFORE_OUT" \
  "$READ_MODEL_AFTER_OUT" \
  "$OP_RESULTS_OUT" \
  "$LOCK_SAMPLES_OUT" \
  "$MCP_AFTER_OUT" \
  "$PURGE_DRY_RUN_OUT" \
  "$PURGE_CONFIRM_OUT" \
  "$PURGE_CONFIRM_OUT.stderr" \
  "$READER_WRITE_DENIED_OUT" \
  "$READER_RAW_READ_DENIED_OUT" \
  "$READER_TRANSCRIPT_RAW_DENIED_OUT" \
  "$READER_TRANSCRIPT_TEXT_DENIED_OUT" \
  "$READER_TRANSCRIPT_VECTOR_DENIED_OUT" \
  "$READER_FIELD_VALUE_DENIED_OUT" \
  "$READER_PROFILE_TABLE_DENIED_OUT" \
  "$LOCK_HOLDER_OUT" \
  "$SAMPLER_DONE" \
  "$ARTIFACT_DIR"/op-*.json \
  "$ARTIFACT_DIR"/op-*.json.stderr

cd "$ROOT"

writer_psql() {
  docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl "$@"
}

reader_psql() {
  docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres sh -s -- "$@" <<'SH'
set -eu
export PGPASSWORD="${GONGMCP_READER_PASSWORD:?}"
exec psql -h 127.0.0.1 -U gongmcp_reader -d gongctl "$@"
SH
}

cleanup() {
  docker compose -p "$PROJECT" -f "$COMPOSE_FILE" down -v >/dev/null 2>&1 || true
}

assert_compose_project_unused() {
  if [ -n "$(docker compose -p "$PROJECT" -f "$COMPOSE_FILE" ps -q 2>/dev/null)" ]; then
    echo "Compose project $PROJECT already has containers; choose an unused smoke project name" >&2
    exit 1
  fi
  if docker volume inspect "${PROJECT}_gongctl-postgres-data" >/dev/null 2>&1; then
    echo "Compose project $PROJECT already has a Postgres volume; choose an unused smoke project name" >&2
    exit 1
  fi
}

trap cleanup EXIT

record_operation() {
  local name="$1"
  local code="$2"
  local start_seconds="$3"
  local end_seconds="$4"
  local stdout_path="$5"
  local stderr_path="$6"
  python3 - "$OP_RESULTS_OUT" "$name" "$code" "$start_seconds" "$end_seconds" "$(basename "$stdout_path")" "$(basename "$stderr_path")" <<'PY'
import json
import sys

path, name, code, start, end, stdout_name, stderr_name = sys.argv[1:]
start_i = int(start)
end_i = int(end)
record = {
    "name": name,
    "status": "ok" if int(code) == 0 else "failed",
    "exit_code": int(code),
    "duration_seconds": max(0, end_i - start_i),
    "stdout": stdout_name,
    "stderr": stderr_name,
}
with open(path, "a", encoding="utf-8") as handle:
    handle.write(json.dumps(record, sort_keys=True) + "\n")
PY
}

run_operation() {
  local name="$1"
  local stdout_path="$2"
  shift 2
  local stderr_path="${stdout_path}.stderr"
  local start_seconds
  local end_seconds
  local code
  start_seconds="$(date +%s)"
  set +e
  python3 - "$OPERATION_TIMEOUT_SECONDS" "$stdout_path" "$stderr_path" "$@" <<'PY'
import subprocess
import sys

timeout_seconds = int(sys.argv[1])
stdout_path = sys.argv[2]
stderr_path = sys.argv[3]
command = sys.argv[4:]

with open(stdout_path, "wb") as stdout, open(stderr_path, "wb") as stderr:
    try:
        completed = subprocess.run(command, stdout=stdout, stderr=stderr, timeout=timeout_seconds)
        raise SystemExit(completed.returncode)
    except subprocess.TimeoutExpired:
        stderr.write(f"\noperation timed out after {timeout_seconds} seconds\n".encode("utf-8"))
        raise SystemExit(124)
PY
  code=$?
  set -e
  end_seconds="$(date +%s)"
  record_operation "$name" "$code" "$start_seconds" "$end_seconds" "$stdout_path" "$stderr_path"
  return 0
}

assert_operation_results() {
  python3 - "$OP_RESULTS_OUT" "$@" <<'PY'
import json
import sys

path = sys.argv[1]
expected = set(sys.argv[2:])
seen = set()
failed = []
with open(path, "r", encoding="utf-8") as handle:
    for line in handle:
        if not line.strip():
            continue
        record = json.loads(line)
        seen.add(record["name"])
        if record["status"] != "ok":
            failed.append(record)
missing = expected - seen
if missing or failed:
    raise SystemExit(f"operation result failure missing={sorted(missing)} failed={failed}")
PY
}

assert_operation_durations() {
  python3 - "$OP_RESULTS_OUT" <<'PY'
import json
import sys

path = sys.argv[1]
limits = {
    "reader_sync_status": 10,
    "profile_import_refresh": 30,
    "purge_confirm": 30,
    "read_model_rebuild": 60,
}
too_slow = []
with open(path, "r", encoding="utf-8") as handle:
    for line in handle:
        if not line.strip():
            continue
        record = json.loads(line)
        limit = limits.get(record["name"])
        if limit is not None and record["duration_seconds"] > limit:
            too_slow.append({"name": record["name"], "duration_seconds": record["duration_seconds"], "limit": limit})
if too_slow:
    raise SystemExit(f"operation duration exceeded limits: {too_slow}")
PY
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

write_counts() {
  local out="$1"
  writer_psql -tA -F '|' -v purge_count="$PURGE_COUNT" -c "
SELECT 'calls', COUNT(*) FROM calls
UNION ALL SELECT 'old_calls', COUNT(*) FROM calls WHERE started_at < '2026-01-01'
UNION ALL SELECT 'transcripts', COUNT(*) FROM transcripts
UNION ALL SELECT 'transcript_segments', COUNT(*) FROM transcript_segments
UNION ALL SELECT 'call_context_objects', COUNT(*) FROM call_context_objects
UNION ALL SELECT 'call_context_fields', COUNT(*) FROM call_context_fields
UNION ALL SELECT 'call_facts', COUNT(*) FROM call_facts
UNION ALL SELECT 'profile_call_fact_cache', COUNT(*) FROM profile_call_fact_cache
UNION ALL SELECT 'old_profile_call_fact_cache', COUNT(*) FROM profile_call_fact_cache WHERE call_id IN (SELECT call_id FROM purged_call_ids)
UNION ALL SELECT 'purged_call_ids', COUNT(*) FROM purged_call_ids
UNION ALL SELECT 'read_model_missing', missing_fact_call_count FROM postgres_read_model_state WHERE model_name = 'builtin_call_facts'
UNION ALL SELECT 'read_model_orphan', orphan_fact_count FROM postgres_read_model_state WHERE model_name = 'builtin_call_facts'
ORDER BY 1" >"$out"
}

write_profile_cache_counts() {
  local out="$1"
  writer_psql -tA -F '|' -c "
SELECT 'profile_cache_rows', COUNT(*) FROM profile_call_fact_cache
UNION ALL SELECT 'profile_cache_open', COUNT(*) FROM profile_call_fact_cache WHERE lifecycle_bucket = 'open'
UNION ALL SELECT 'profile_cache_closed_won', COUNT(*) FROM profile_call_fact_cache WHERE lifecycle_bucket = 'closed_won'
UNION ALL SELECT 'profile_cache_post_sales', COUNT(*) FROM profile_call_fact_cache WHERE lifecycle_bucket = 'post_sales'
UNION ALL SELECT 'profile_cache_meta_rows', COUNT(*) FROM profile_call_fact_cache_meta
UNION ALL SELECT 'active_profiles', COUNT(*) FROM profile_meta WHERE is_active = true
ORDER BY 1" >"$out"
}

assert_counts_before() {
  grep -q "calls|$CALL_COUNT" "$COUNTS_BEFORE_OUT"
  grep -q "old_calls|$PURGE_COUNT" "$COUNTS_BEFORE_OUT"
  grep -q "call_facts|$CALL_COUNT" "$COUNTS_BEFORE_OUT"
  grep -q "profile_call_fact_cache|$CALL_COUNT" "$COUNTS_BEFORE_OUT"
  grep -q "read_model_missing|0" "$COUNTS_BEFORE_OUT"
  grep -q "read_model_orphan|0" "$COUNTS_BEFORE_OUT"
  grep -q "profile_cache_rows|$CALL_COUNT" "$PROFILE_CACHE_BEFORE_OUT"
  grep -Eq "profile_cache_(open|closed_won|post_sales)\\|[1-9][0-9]*" "$PROFILE_CACHE_BEFORE_OUT"
}

assert_counts_after() {
  local remaining=$((CALL_COUNT - PURGE_COUNT))
  grep -q "calls|$remaining" "$COUNTS_AFTER_OUT"
  grep -q "old_calls|0" "$COUNTS_AFTER_OUT"
  grep -q "call_facts|$remaining" "$COUNTS_AFTER_OUT"
  grep -q "profile_call_fact_cache|$remaining" "$COUNTS_AFTER_OUT"
  grep -q "old_profile_call_fact_cache|0" "$COUNTS_AFTER_OUT"
  grep -q "purged_call_ids|$PURGE_COUNT" "$COUNTS_AFTER_OUT"
  grep -q "read_model_missing|0" "$COUNTS_AFTER_OUT"
  grep -q "read_model_orphan|0" "$COUNTS_AFTER_OUT"
  grep -q "profile_cache_rows|$remaining" "$PROFILE_CACHE_AFTER_OUT"
}

sample_locks() {
  while [ ! -f "$SAMPLER_DONE" ]; do
    {
      printf 'sample_at|%s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
      writer_psql -tA -F '|' -c "
SELECT pid,
       COALESCE(application_name, ''),
       COALESCE(wait_event_type, ''),
       COALESCE(wait_event, ''),
       state,
       CASE
         WHEN application_name LIKE 'contention_%' THEN application_name
         WHEN query ILIKE '%pg_advisory%' THEN 'writer_advisory_lock'
         WHEN query ILIKE '%profile_call_fact_cache%' THEN 'profile_cache_refresh'
         WHEN query ILIKE '%call_facts%' OR query ILIKE '%call_context_%' THEN 'read_model_refresh'
         WHEN query ILIKE '%gongmcp_cache_purge_plan%' THEN 'purge_plan'
         WHEN query ILIKE '%gongctl_purge_call_ids%' THEN 'purge_confirm'
         ELSE 'other'
       END
  FROM pg_stat_activity
 WHERE datname = 'gongctl'
   AND pid <> pg_backend_pid()
   AND (application_name LIKE 'contention_%' OR wait_event_type IS NOT NULL OR query ILIKE '%advisory%' OR query ILIKE '%profile_call_fact_cache%' OR query ILIKE '%call_facts%')
 ORDER BY pid" || true
    } >>"$LOCK_SAMPLES_OUT"
    sleep 0.25
  done
}

wait_for_writer_lock_holder() {
  for _ in $(seq 1 40); do
    if writer_psql -tA -c "
SELECT COUNT(*)
  FROM pg_locks AS l
  JOIN pg_stat_activity AS a ON a.pid = l.pid
 WHERE a.application_name = 'contention_lock_holder'
   AND l.locktype = 'advisory'
   AND l.granted" | grep -q '^1$'; then
      return 0
    fi
    sleep 0.1
  done
  echo "contention lock holder did not acquire advisory writer lock" >&2
  exit 1
}

assert_lock_contention_observed() {
  python3 - "$LOCK_SAMPLES_OUT" <<'PY'
import sys

path = sys.argv[1]
required = {
    "contention_read_model_rebuild",
    "contention_profile_import_refresh",
    "contention_purge_confirm",
}
seen = set()
with open(path, "r", encoding="utf-8") as handle:
    for raw in handle:
        line = raw.strip()
        if not line or line.startswith("sample_at|"):
            continue
        parts = line.split("|")
        if len(parts) < 6:
            continue
        _, application_name, wait_type, wait_event, _, operation = parts[:6]
        label = application_name or operation
        if label in required and wait_type == "Lock" and wait_event == "advisory":
            seen.add(label)
missing = required - seen
if missing:
    raise SystemExit(f"missing advisory lock-wait samples for: {sorted(missing)}")
PY
}

assert_purge_artifacts() {
  python3 - "$PURGE_DRY_RUN_OUT" "$PURGE_CONFIRM_OUT" "$PURGE_COUNT" <<'PY'
import json
import sys

dry_path, confirm_path, purge_count = sys.argv[1:]
purge_count = int(purge_count)

def load(path):
    with open(path, "r", encoding="utf-8") as handle:
        return json.load(handle)

dry = load(dry_path)
confirm = load(confirm_path)
for name, payload in {"dry_run": dry, "confirm": confirm}.items():
    plan = payload.get("plan") or {}
    if plan.get("call_count") != purge_count:
        raise SystemExit(f"{name} call_count={plan.get('call_count')} want {purge_count}")
    if (payload.get("retention_policy") or {}).get("approval_complete") is not True:
        raise SystemExit(f"{name} retention policy approval_complete is not true")
dry_plan = dry.get("plan") or {}
if dry_plan.get("context_object_count", 0) <= 0 or dry_plan.get("context_field_count", 0) <= 0 or dry_plan.get("call_fact_count", 0) <= 0:
    raise SystemExit(f"dry-run plan did not capture dependent read-model rows: {dry_plan}")
PY
}

assert_compose_project_unused
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" up -d postgres

for _ in $(seq 1 60); do
  if docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres pg_isready -U gongctl -d gongctl >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

go build -o "$GONGCTL_BIN" ./cmd/gongctl
go build -o "$GONGMCP_BIN" ./cmd/gongmcp
cp docs/examples/business-profile.example.yaml "$PROFILE_FILE"

GONG_DATABASE_URL="$WRITER_URL" "$GONGCTL_BIN" sync synthetic >/dev/null
writer_psql -v ON_ERROR_STOP=1 -c "TRUNCATE call_read_model_diagnostics, postgres_read_model_state, call_facts, call_context_fields, call_context_objects, transcript_segments, transcripts, calls, users, sync_state, sync_runs, profile_call_fact_cache, profile_call_fact_cache_meta, profile_validation_warning, profile_methodology_concept, profile_lifecycle_rule, profile_field_concept, profile_object_alias, profile_meta, purged_call_ids RESTART IDENTITY CASCADE" >/dev/null

load_start_seconds="$(date +%s)"
writer_psql -v ON_ERROR_STOP=1 -v call_count="$CALL_COUNT" -v purge_count="$PURGE_COUNT" <<'SQL' >/dev/null
WITH synthetic AS (
	SELECT gs AS n,
	       'contention-call-' || lpad(gs::text, 6, '0') AS call_id,
	       CASE WHEN gs <= :purge_count
	            THEN to_char(timestamp '2025-12-01 12:00:00' + (gs * interval '1 minute'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
	            ELSE to_char(timestamp '2026-02-01 12:00:00' + (gs * interval '1 minute'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
	       END AS started_at,
	       CASE gs % 5
		       WHEN 0 THEN 'Discovery'
		       WHEN 1 THEN 'Proposal'
		       WHEN 2 THEN 'Closed Won'
		       WHEN 3 THEN 'Closed Lost'
		       ELSE 'Evaluation'
	       END AS opportunity_stage,
	       CASE gs % 4
		       WHEN 0 THEN 'Renewal'
		       WHEN 1 THEN 'Upsell'
		       WHEN 2 THEN 'New Business'
		       ELSE 'Expansion'
	       END AS opportunity_type,
	       CASE gs % 6
		       WHEN 0 THEN 'Active Customer'
		       WHEN 1 THEN 'Renewal'
		       ELSE 'Prospect'
	       END AS customer_status
	  FROM generate_series(1, :call_count) AS gs
)
INSERT INTO calls(call_id, title, started_at, duration_seconds, parties_count, context_present, raw_json, raw_sha256, first_seen_at, updated_at)
SELECT call_id,
       'Synthetic contention call ' || n,
       started_at,
       600 + ((n % 8) * 300),
       2,
       true,
       jsonb_build_object(
	       'id', call_id,
	       'title', 'Synthetic contention call ' || n,
	       'started', started_at,
	       'duration', 600 + ((n % 8) * 300),
	       'metaData', jsonb_build_object(
		       'scope', CASE WHEN n % 3 = 0 THEN 'Internal' ELSE 'External' END,
		       'system', CASE WHEN n % 2 = 0 THEN 'Zoom' ELSE 'Gong Connect' END,
		       'direction', CASE WHEN n % 4 = 0 THEN 'Outbound' ELSE 'Conference' END,
		       'purpose', 'Synthetic contention validation',
		       'calendarEventId', 'synthetic-contention-cal-' || n
	       ),
	       'context', jsonb_build_object(
		       'crmObjects', jsonb_build_array(
			       jsonb_build_object(
				       'type', 'Opportunity',
				       'id', 'synthetic-contention-opp-' || n,
				       'name', 'Synthetic Contention Opportunity ' || n,
				       'fields', jsonb_build_object(
					       'StageName', opportunity_stage,
					       'Type', opportunity_type,
					       'ForecastCategoryName', 'Pipeline'
				       )
			       ),
			       jsonb_build_object(
				       'type', 'Account',
				       'id', 'synthetic-contention-account-' || ((n % 40) + 1),
				       'name', 'Synthetic Contention Account ' || ((n % 40) + 1),
				       'fields', jsonb_build_object(
					       'Customer_Status__c', customer_status,
					       'Industry', CASE WHEN n % 2 = 0 THEN 'Healthcare' ELSE 'Manufacturing' END
				       )
			       )
		       )
	       )
       ),
       'synthetic-contention-sha-' || n,
       started_at,
       started_at
  FROM synthetic;

WITH synthetic AS (
	SELECT gs AS n,
	       'contention-call-' || lpad(gs::text, 6, '0') AS call_id,
	       CASE WHEN gs <= :purge_count
	            THEN to_char(timestamp '2025-12-01 12:00:00' + (gs * interval '1 minute'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
	            ELSE to_char(timestamp '2026-02-01 12:00:00' + (gs * interval '1 minute'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
	       END AS started_at
	  FROM generate_series(1, :call_count) AS gs
)
INSERT INTO transcripts(call_id, raw_json, raw_sha256, segment_count, first_seen_at, updated_at)
SELECT call_id,
       jsonb_build_object('callId', call_id, 'synthetic', true),
       'synthetic-contention-transcript-sha-' || n,
       2,
       started_at,
       started_at
  FROM synthetic;

WITH synthetic AS (
	SELECT gs AS n,
	       'contention-call-' || lpad(gs::text, 6, '0') AS call_id
	  FROM generate_series(1, :call_count) AS gs
),
segments AS (
	SELECT n, call_id, 0 AS segment_index,
	       'speaker-1' AS speaker_id,
	       'Synthetic contention transcript segment for shared Postgres validation call ' || n AS text
	  FROM synthetic
	UNION ALL
	SELECT n, call_id, 1 AS segment_index,
	       'speaker-2' AS speaker_id,
	       'Retention and profile-cache benchmark evidence without customer data for call ' || n AS text
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
load_end_seconds="$(date +%s)"
load_insert_seconds=$((load_end_seconds - load_start_seconds))

GONG_DATABASE_URL="$WRITER_URL" "$GONGCTL_BIN" sync read-model --rebuild >"$READ_MODEL_BEFORE_OUT"
grep -q '"ready": true' "$READ_MODEL_BEFORE_OUT"
grep -q "\"call_count\": $CALL_COUNT" "$READ_MODEL_BEFORE_OUT"
GONG_DATABASE_URL="$WRITER_URL" "$GONGCTL_BIN" profile import --profile "$PROFILE_FILE" >"$PROFILE_IMPORT_BEFORE_OUT"
write_counts "$COUNTS_BEFORE_OUT"
write_profile_cache_counts "$PROFILE_CACHE_BEFORE_OUT"
assert_counts_before

cat >"$POLICY_FILE" <<'YAML'
version: 1
older_than: 2026-01-01
approval:
  reference: CHANGE-CONTENTION-SYNTHETIC
  approved_by: revops-retention-reviewer
  approved_at: 2024-01-01
  data_owner: revenue-operations
  backup_reference: backup-20240101-contention
  legal_hold_reviewed: true
YAML

GONG_DATABASE_URL="$READER_URL" "$GONGCTL_BIN" cache purge --config "$POLICY_FILE" --dry-run >"$PURGE_DRY_RUN_OUT"
grep -q '"call_count": '"$PURGE_COUNT" "$PURGE_DRY_RUN_OUT"
if grep -q 'contention-call-\|Synthetic contention transcript\|postgres://\|gongctl_dev_password\|gongmcp_reader_dev_password\|retention-policy.yaml' "$PURGE_DRY_RUN_OUT"; then
  echo "contention purge dry-run exposed raw identifiers, transcript text, secrets, or local policy path" >&2
  exit 1
fi

writer_psql -v ON_ERROR_STOP=1 -c "SET application_name = 'contention_lock_holder'; SELECT pg_advisory_lock(hashtext('gongctl_postgres_writer_v1')); SELECT pg_sleep($LOCK_HOLD_SECONDS::double precision); SELECT pg_advisory_unlock(hashtext('gongctl_postgres_writer_v1'));" >"$LOCK_HOLDER_OUT" &
lock_holder_pid=$!
wait_for_writer_lock_holder

sample_locks &
sampler_pid=$!

run_operation "read_model_rebuild" "$ARTIFACT_DIR/op-read-model-rebuild.json" env GONG_DATABASE_URL="$WRITER_REBUILD_URL" "$GONGCTL_BIN" sync read-model --rebuild &
pid_rebuild=$!
run_operation "profile_import_refresh" "$ARTIFACT_DIR/op-profile-import.json" env GONG_DATABASE_URL="$WRITER_PROFILE_URL" "$GONGCTL_BIN" profile import --profile "$PROFILE_FILE" &
pid_profile=$!
run_operation "purge_confirm" "$PURGE_CONFIRM_OUT" env GONG_DATABASE_URL="$WRITER_PURGE_URL" "$GONGCTL_BIN" cache purge --config "$POLICY_FILE" --confirm &
pid_purge=$!
run_operation "reader_sync_status" "$ARTIFACT_DIR/op-reader-sync-status.json" env GONG_DATABASE_URL="$READER_STATUS_URL" "$GONGCTL_BIN" sync status &
pid_status=$!

wait "$lock_holder_pid"
wait "$pid_rebuild" "$pid_profile" "$pid_purge" "$pid_status"
touch "$SAMPLER_DONE"
wait "$sampler_pid" || true

assert_operation_results read_model_rebuild profile_import_refresh purge_confirm reader_sync_status
assert_operation_durations
assert_lock_contention_observed
assert_purge_artifacts
grep -q '"call_count": '"$PURGE_COUNT" "$PURGE_CONFIRM_OUT"
if grep -q 'contention-call-\|Synthetic contention transcript\|postgres://\|gongctl_dev_password\|gongmcp_reader_dev_password\|retention-policy.yaml' "$PURGE_CONFIRM_OUT"; then
  echo "contention purge confirm exposed raw identifiers, transcript text, secrets, or local policy path" >&2
  exit 1
fi
GONG_DATABASE_URL="$WRITER_URL" "$GONGCTL_BIN" sync read-model >"$READ_MODEL_AFTER_OUT"
grep -q '"ready": true' "$READ_MODEL_AFTER_OUT"
grep -q "\"call_count\": $((CALL_COUNT - PURGE_COUNT))" "$READ_MODEL_AFTER_OUT"
write_counts "$COUNTS_AFTER_OUT"
write_profile_cache_counts "$PROFILE_CACHE_AFTER_OUT"
assert_counts_after

if reader_psql -c "INSERT INTO users(user_id, raw_json, raw_sha256, first_seen_at, updated_at) VALUES('contention-reader-write', '{}'::jsonb, 'sha', now()::text, now()::text)" >"$READER_WRITE_DENIED_OUT" 2>&1; then
  echo "contention reader unexpectedly wrote to users" >&2
  exit 1
fi
if grep -q 'postgres://\|gongctl_dev_password\|gongmcp_reader_dev_password' "$READER_WRITE_DENIED_OUT"; then
  echo "contention reader denial exposed secrets" >&2
  exit 1
fi
if reader_psql -c "SELECT raw_json FROM calls LIMIT 1" >"$READER_RAW_READ_DENIED_OUT" 2>&1; then
  echo "contention reader unexpectedly read raw call JSON" >&2
  exit 1
fi
if reader_psql -c "SELECT raw_json FROM transcripts LIMIT 1" >"$READER_TRANSCRIPT_RAW_DENIED_OUT" 2>&1; then
  echo "contention reader unexpectedly read raw transcript JSON" >&2
  exit 1
fi
if reader_psql -c "SELECT text FROM transcript_segments LIMIT 1" >"$READER_TRANSCRIPT_TEXT_DENIED_OUT" 2>&1; then
  echo "contention reader unexpectedly read transcript text" >&2
  exit 1
fi
if reader_psql -c "SELECT search_vector FROM transcript_segments LIMIT 1" >"$READER_TRANSCRIPT_VECTOR_DENIED_OUT" 2>&1; then
  echo "contention reader unexpectedly read transcript search vector" >&2
  exit 1
fi
if reader_psql -c "SELECT field_value_text FROM call_context_fields LIMIT 1" >"$READER_FIELD_VALUE_DENIED_OUT" 2>&1; then
  echo "contention reader unexpectedly read CRM field values" >&2
  exit 1
fi
if reader_psql -c "SELECT * FROM profile_call_fact_cache LIMIT 1" >"$READER_PROFILE_TABLE_DENIED_OUT" 2>&1; then
  echo "contention reader unexpectedly read profile fact-cache table" >&2
  exit 1
fi
for denial in "$READER_WRITE_DENIED_OUT" "$READER_RAW_READ_DENIED_OUT" "$READER_TRANSCRIPT_RAW_DENIED_OUT" "$READER_TRANSCRIPT_TEXT_DENIED_OUT" "$READER_TRANSCRIPT_VECTOR_DENIED_OUT" "$READER_FIELD_VALUE_DENIED_OUT" "$READER_PROFILE_TABLE_DENIED_OUT"; do
  if grep -q 'postgres://\|gongctl_dev_password\|gongmcp_reader_dev_password' "$denial"; then
    echo "contention reader denial exposed secrets: $denial" >&2
    exit 1
  fi
done

{
  printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"postgres-contention-smoke","version":"0"}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_sync_status","arguments":{}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"rank_transcript_backlog","arguments":{"limit":5}}}'
} | GONG_DATABASE_URL="$READER_URL" GONGMCP_TOOL_PRESET=operator-smoke "$GONGMCP_BIN" >"$MCP_AFTER_OUT"
assert_mcp_success "$MCP_AFTER_OUT" 1 2 3 4
if grep -q 'postgres://\|gongctl_dev_password\|gongmcp_reader_dev_password\|Synthetic contention transcript' "$MCP_AFTER_OUT"; then
  echo "contention MCP output exposed secrets or transcript text" >&2
  exit 1
fi

python3 - "$SUMMARY_OUT" "$CALL_COUNT" "$PURGE_COUNT" "$load_insert_seconds" "$OP_RESULTS_OUT" "$OPERATION_TIMEOUT_SECONDS" <<'PY'
import json
import sys

out, call_count, purge_count, load_seconds, operation_results_path, operation_timeout_seconds = sys.argv[1:]
operation_results = []
with open(operation_results_path, "r", encoding="utf-8") as handle:
    for line in handle:
        if line.strip():
            operation_results.append(json.loads(line))
summary = {
    "call_count": int(call_count),
    "purge_count": int(purge_count),
    "remaining_call_count": int(call_count) - int(purge_count),
    "load_insert_seconds": int(load_seconds),
    "decision": "repo-local synthetic contention smoke passed for the shipped writer-lock behavior at this configured size; customer-scale production benchmarking remains deployment-owned before broad or high-volume rollout",
    "contention_assertions": {
        "required_advisory_waits": [
            "contention_read_model_rebuild",
            "contention_profile_import_refresh",
            "contention_purge_confirm",
        ],
        "operation_duration_limits_seconds": {
            "reader_sync_status": 10,
            "profile_import_refresh": 30,
            "purge_confirm": 30,
            "read_model_rebuild": 60,
        },
        "operation_timeout_seconds": int(operation_timeout_seconds),
    },
    "operation_results": operation_results,
    "artifacts": {
        "counts_before": "counts-before.txt",
        "counts_after": "counts-after.txt",
        "profile_cache_before": "profile-cache-before.txt",
        "profile_cache_after": "profile-cache-after.txt",
        "read_model_before": "read-model-before.json",
        "read_model_after": "read-model-after.json",
        "operation_results": "operation-results.jsonl",
        "lock_samples": "lock-samples.txt",
        "lock_holder": "writer-lock-holder.txt",
        "purge_dry_run": "purge-policy-dry-run.json",
        "purge_confirm": "purge-confirm.json",
        "mcp_after": "mcp-after.jsonl",
        "reader_write_denied": "reader-write-denied.txt",
        "reader_raw_read_denied": "reader-raw-read-denied.txt",
        "reader_transcript_raw_denied": "reader-transcript-raw-denied.txt",
        "reader_transcript_text_denied": "reader-transcript-text-denied.txt",
        "reader_transcript_vector_denied": "reader-transcript-vector-denied.txt",
        "reader_field_value_denied": "reader-field-value-denied.txt",
        "reader_profile_table_denied": "reader-profile-table-denied.txt",
    },
}
with open(out, "w", encoding="utf-8") as handle:
    json.dump(summary, handle, indent=2, sort_keys=True)
    handle.write("\n")
PY

if grep -q 'postgres://\|gongctl_dev_password\|gongmcp_reader_dev_password\|Synthetic contention transcript\|contention-call-\|retention-policy.yaml\|raw_json\|raw_sha256\|crmObjects\|field_value_text' \
  "$SUMMARY_OUT" \
  "$COUNTS_BEFORE_OUT" \
  "$COUNTS_AFTER_OUT" \
  "$PROFILE_CACHE_BEFORE_OUT" \
  "$PROFILE_CACHE_AFTER_OUT" \
  "$PROFILE_IMPORT_BEFORE_OUT" \
  "$READ_MODEL_BEFORE_OUT" \
  "$READ_MODEL_AFTER_OUT" \
  "$OP_RESULTS_OUT" \
  "$LOCK_SAMPLES_OUT" \
  "$MCP_AFTER_OUT" \
  "$PURGE_DRY_RUN_OUT" \
  "$PURGE_CONFIRM_OUT" \
  "$PURGE_CONFIRM_OUT.stderr" \
  "$READER_WRITE_DENIED_OUT" \
  "$READER_RAW_READ_DENIED_OUT" \
  "$READER_TRANSCRIPT_RAW_DENIED_OUT" \
  "$READER_TRANSCRIPT_TEXT_DENIED_OUT" \
  "$READER_TRANSCRIPT_VECTOR_DENIED_OUT" \
  "$READER_FIELD_VALUE_DENIED_OUT" \
  "$READER_PROFILE_TABLE_DENIED_OUT" \
  "$ARTIFACT_DIR"/op-*.json \
  "$ARTIFACT_DIR"/op-*.json.stderr; then
  echo "contention public artifacts exposed raw identifiers, sensitive SQL names, transcript text, secrets, or local paths" >&2
  exit 1
fi

echo "postgres contention smoke passed"
echo "artifact directory: $ARTIFACT_DIR"
echo "summary output: $SUMMARY_OUT"
echo "counts before output: $COUNTS_BEFORE_OUT"
echo "counts after output: $COUNTS_AFTER_OUT"
echo "profile cache before output: $PROFILE_CACHE_BEFORE_OUT"
echo "profile cache after output: $PROFILE_CACHE_AFTER_OUT"
echo "read model before output: $READ_MODEL_BEFORE_OUT"
echo "read model after output: $READ_MODEL_AFTER_OUT"
echo "operation results output: $OP_RESULTS_OUT"
echo "lock samples output: $LOCK_SAMPLES_OUT"
echo "MCP after output: $MCP_AFTER_OUT"
echo "purge dry-run output: $PURGE_DRY_RUN_OUT"
echo "purge confirm output: $PURGE_CONFIRM_OUT"
echo "reader write denial output: $READER_WRITE_DENIED_OUT"
echo "reader raw-read denial output: $READER_RAW_READ_DENIED_OUT"
echo "reader transcript raw-read denial output: $READER_TRANSCRIPT_RAW_DENIED_OUT"
echo "reader transcript text denial output: $READER_TRANSCRIPT_TEXT_DENIED_OUT"
echo "reader transcript vector denial output: $READER_TRANSCRIPT_VECTOR_DENIED_OUT"
echo "reader field-value denial output: $READER_FIELD_VALUE_DENIED_OUT"
echo "reader profile table denial output: $READER_PROFILE_TABLE_DENIED_OUT"
