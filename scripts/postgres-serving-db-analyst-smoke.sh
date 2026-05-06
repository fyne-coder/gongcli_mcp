#!/usr/bin/env bash
#
# Phase 13h + 13b focused smoke: scoped analyst reader and broad analyst /
# company-search behavior on the redacted Postgres serving database.
#
# This script proves the customer-recommended Phase 13e4/13h/13b pipeline end
# to end on a single Postgres server using two databases:
#
#   gongctl_source  full operator cache (operator role, write access)
#   gongctl_mcp     redacted MCP serving cache (analyst-scoped reader, no
#                   restricted call rows)
#
# Steps:
#   1. Bring up disposable Postgres via the existing compose file.
#   2. Sync synthetic data into gongctl_source and add an extra restricted
#      synthetic customer + call so the governance audit has something to
#      remove.
#   3. Create the empty gongctl_mcp database on the same Postgres server.
#   4. Run `gongctl governance refresh-serving-db --source ... --target ...
#      --config <synthetic ai-governance.yaml>` and assert sanitized output
#      reports a non-zero removed_calls count without leaking the restricted
#      synthetic name, IDs, or DB URLs.
#   5. Apply the analyst-expansion scoped reader on gongctl_mcp via
#      `gongctl mcp postgres-reader-apply`.
#   6. Run `gongmcp` against the scoped reader URL with
#      GONGMCP_TOOL_PRESET=analyst-expansion and
#      GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS=1 and assert tools/list plus a
#      handful of analyst tool calls succeed.
#   7. Confirm small-cell suppression min cohort size is active (analyst
#      preset + scoped grants enforce min=3).
#   8. Confirm direct SQL denial as the scoped analyst role for raw call
#      payload columns and the operator-only sync_runs table.
#   9. Confirm the restricted synthetic call ID, customer name, transcript
#      content, and DB URLs do not appear in any artifact.
#  10. (Phase 13b) Run a broad analyst MCP session against the same scoped
#      reader URL with GONGMCP_POSTGRES_REDACTED_SERVING_DB=1 to prove
#      non-restricted company/title/transcript searches return non-zero
#      results (search_calls_by_filters,
#      search_transcripts_by_filters, summarize_calls_by_lifecycle,
#      crm_field_population_matrix, search_transcripts_by_crm_context,
#      build_quote_pack, diagnose_attribution_coverage,
#      explain_analysis_limitations) while restricted-company probes return
#      zero results without leaking the restricted name. Reject the
#      `all-readonly` preset under Postgres mode.
#
# Synthetic-only fixtures and synthetic dev passwords are used. Real customer
# names, real Gong IDs, and real secrets must NEVER appear in this script.
#
# Override knobs (all optional):
#
#   GONGCTL_PHASE13H_PORT                 host port for the Postgres container
#   GONGCTL_PHASE13H_COMPOSE_PROJECT      compose project name
#   GONGCTL_PHASE13H_ARTIFACT_DIR         output artifact directory under /tmp
#   GONGCTL_PHASE13H_KEEP_ARTIFACTS=1     keep artifact dir on success
#   GONGCTL_PHASE13H_SKIP_DOWN=1          leave compose project running
#
set -euo pipefail
umask 077

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE_FILE="$ROOT/docker-compose.postgres.yml"
PROJECT="${GONGCTL_PHASE13H_COMPOSE_PROJECT:-gongctl-postgres-13h-smoke}"
PORT="${GONGCTL_PHASE13H_PORT:-55434}"
SOURCE_DB="gongctl"
TARGET_DB="gongctl_mcp"
SCOPED_ROLE="gongmcp_analyst_reader"

export GONGCTL_POSTGRES_PORT="$PORT"
export GONGCTL_POSTGRES_USER="${GONGCTL_POSTGRES_USER:-gongctl}"
export GONGCTL_POSTGRES_PASSWORD="${GONGCTL_POSTGRES_PASSWORD:-gongctl_dev_password}"
export GONGMCP_READER_PASSWORD="${GONGMCP_READER_PASSWORD:-gongmcp_reader_dev_password}"
export GONGMCP_ANALYST_READER_PASSWORD="${GONGMCP_ANALYST_READER_PASSWORD:-gongmcp_analyst_reader_dev_password}"

urlencode() {
  python3 -c 'from urllib.parse import quote; import sys; print(quote(sys.argv[1], safe=""))' "$1"
}

SOURCE_URL="postgres://$(urlencode "$GONGCTL_POSTGRES_USER"):$(urlencode "$GONGCTL_POSTGRES_PASSWORD")@127.0.0.1:${PORT}/${SOURCE_DB}?sslmode=disable"
TARGET_WRITER_URL="postgres://$(urlencode "$GONGCTL_POSTGRES_USER"):$(urlencode "$GONGCTL_POSTGRES_PASSWORD")@127.0.0.1:${PORT}/${TARGET_DB}?sslmode=disable"
ANALYST_READER_URL="postgres://${SCOPED_ROLE}:$(urlencode "$GONGMCP_ANALYST_READER_PASSWORD")@127.0.0.1:${PORT}/${TARGET_DB}?sslmode=disable"

ARTIFACT_DIR="${GONGCTL_PHASE13H_ARTIFACT_DIR:-$(mktemp -d /tmp/gongctl-postgres-13h.XXXXXX)}"
mkdir -p "$ARTIFACT_DIR"
chmod 700 "$ARTIFACT_DIR"

REFRESH_OUT="$ARTIFACT_DIR/refresh-serving-db.json"
ANALYST_APPLY_OUT="$ARTIFACT_DIR/analyst-reader-apply.json"
ANALYST_MCP_OUT="$ARTIFACT_DIR/analyst-mcp.jsonl"
BROAD_MCP_OUT="$ARTIFACT_DIR/broad-analyst-mcp.jsonl"
ALL_READONLY_REJECTED_OUT="$ARTIFACT_DIR/all-readonly-rejected.txt"
TARGET_RESTRICTED_COUNTS_OUT="$ARTIFACT_DIR/target-restricted-counts.txt"
TARGET_RESTRICTED_TEXT_COUNTS_OUT="$ARTIFACT_DIR/target-restricted-text-counts.txt"
TARGET_ALLOWED_COUNTS_OUT="$ARTIFACT_DIR/target-allowed-counts.txt"
ANALYST_RAW_DENIED_OUT="$ARTIFACT_DIR/analyst-raw-denied.txt"
ANALYST_SYNC_DENIED_OUT="$ARTIFACT_DIR/analyst-sync-denied.txt"
ANALYST_PROFILE_DENIED_OUT="$ARTIFACT_DIR/analyst-profile-meta-denied.txt"
GOVERNANCE_CONFIG="$(mktemp /tmp/gongctl-postgres-13h-governance.XXXXXX.yaml)"
PHASE13H_SUMMARY="$ARTIFACT_DIR/summary.json"
PHASE13B_SUMMARY="$ARTIFACT_DIR/broad-analyst-summary.json"

cd "$ROOT"

operator_psql() {
  local db="$1"
  shift
  docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T \
    -e PGPASSWORD="$GONGCTL_POSTGRES_PASSWORD" \
    postgres psql -h 127.0.0.1 -U "$GONGCTL_POSTGRES_USER" -d "$db" -v ON_ERROR_STOP=1 "$@"
}

analyst_psql() {
  if [ "$#" -ne 2 ] || [ "$1" != "-c" ]; then
    echo "analyst_psql supports only -c SQL" >&2
    exit 1
  fi
  printf '%s\n' "$2" | docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T \
    -e PGPASSWORD="$GONGMCP_ANALYST_READER_PASSWORD" \
    postgres psql -h 127.0.0.1 -U "$SCOPED_ROLE" -d "$TARGET_DB" -v ON_ERROR_STOP=1
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

assert_no_leak() {
  local label="$1"
  shift
  local pattern='Blocked Synthetic Corp|blockedsynthetic\.example|phase13h-blocked-call|phase13h-blocked-account|phase13h-blocked-opp|This blocked transcript|postgres://|gongctl_dev_password|gongmcp_reader_dev_password|gongmcp_analyst_reader_dev_password'
  if grep -E -q "$pattern" "$@"; then
    echo "$label evidence leaked restricted identifiers, transcripts, or secrets" >&2
    grep -E -n "$pattern" "$@" >&2 || true
    exit 1
  fi
}

cleanup() {
  local rc=$?
  rm -f "$GOVERNANCE_CONFIG"
  if [ "${GONGCTL_PHASE13H_SKIP_DOWN:-0}" = "1" ]; then
    return
  fi
  docker compose -p "$PROJECT" -f "$COMPOSE_FILE" down -v >/dev/null 2>&1 || true
  if [ "$rc" -eq 0 ] && [ "${GONGCTL_PHASE13H_KEEP_ARTIFACTS:-0}" != "1" ]; then
    rm -rf "$ARTIFACT_DIR"
  fi
}
trap cleanup EXIT

docker compose -p "$PROJECT" -f "$COMPOSE_FILE" down -v >/dev/null 2>&1 || true
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" up -d postgres
sleep 2
for _ in $(seq 1 90); do
  if operator_psql "$SOURCE_DB" -tAc "SELECT 1" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
operator_psql "$SOURCE_DB" -tAc "SELECT 1" >/dev/null

# Step 1: synthetic data on the source operator cache.
GONG_DATABASE_URL="$SOURCE_URL" go run ./cmd/gongctl sync synthetic >"$ARTIFACT_DIR/source-sync.json"
GONG_DATABASE_URL="$SOURCE_URL" go run ./cmd/gongctl sync read-model >"$ARTIFACT_DIR/source-read-model.json"
grep -q '"ready": true' "$ARTIFACT_DIR/source-read-model.json"

# Step 2: extra synthetic fixtures, including a restricted call. Names and
# domains are clearly synthetic and do not match real customer data.
operator_psql "$SOURCE_DB" >/dev/null <<'SQL'
INSERT INTO calls(call_id, title, started_at, duration_seconds, parties_count, context_present, raw_json, raw_sha256, first_seen_at, updated_at)
VALUES
  ('phase13h-allowed-call-001', 'Phase 13h allowed shared Postgres call one', '2026-04-12T15:00:00Z', 1800, 2, true,
   '{"id":"phase13h-allowed-call-001","title":"Phase 13h allowed shared Postgres call one","started":"2026-04-12T15:00:00Z","duration":1800,"metaData":{"scope":"External","system":"Zoom","direction":"Conference"},"context":{"crmObjects":[{"type":"Account","id":"phase13h-allowed-account-001","name":"Allowed Synthetic Co","fields":{"Account_Type__c":"Customer - Active","Industry":"Manufacturing"}},{"type":"Opportunity","id":"phase13h-allowed-opp-001","name":"Allowed Synthetic Opportunity","fields":{"StageName":"Discovery","Type":"New Business"}}]}}'::jsonb,
   'phase13h-allowed-sha-001', now()::text, now()::text),
  ('phase13h-allowed-call-002', 'Phase 13h allowed shared Postgres call two', '2026-04-13T15:00:00Z', 1500, 2, true,
   '{"id":"phase13h-allowed-call-002","title":"Phase 13h allowed shared Postgres call two","started":"2026-04-13T15:00:00Z","duration":1500,"metaData":{"scope":"External","system":"Zoom","direction":"Conference"},"context":{"crmObjects":[{"type":"Account","id":"phase13h-allowed-account-002","name":"Allowed Synthetic Co","fields":{"Account_Type__c":"Customer - Active","Industry":"Healthcare"}},{"type":"Opportunity","id":"phase13h-allowed-opp-002","name":"Allowed Synthetic Renewal","fields":{"StageName":"Closed Won","Type":"Renewal"}}]}}'::jsonb,
   'phase13h-allowed-sha-002', now()::text, now()::text),
  ('phase13h-allowed-call-003', 'Phase 13h allowed shared Postgres call three', '2026-04-14T15:00:00Z', 1200, 2, true,
   '{"id":"phase13h-allowed-call-003","title":"Phase 13h allowed shared Postgres call three","started":"2026-04-14T15:00:00Z","duration":1200,"metaData":{"scope":"External","system":"Zoom","direction":"Conference"},"context":{"crmObjects":[{"type":"Account","id":"phase13h-allowed-account-003","name":"Allowed Synthetic Co","fields":{"Account_Type__c":"Customer - Active","Industry":"Manufacturing"}},{"type":"Opportunity","id":"phase13h-allowed-opp-003","name":"Allowed Synthetic Pilot","fields":{"StageName":"Closed Won","Type":"New Business"}}]}}'::jsonb,
   'phase13h-allowed-sha-003', now()::text, now()::text),
  ('phase13h-allowed-call-004', 'Phase 13h allowed shared Postgres call four', '2026-04-15T15:00:00Z', 900, 2, true,
   '{"id":"phase13h-allowed-call-004","title":"Phase 13h allowed shared Postgres call four","started":"2026-04-15T15:00:00Z","duration":900,"metaData":{"scope":"External","system":"Zoom","direction":"Conference"},"context":{"crmObjects":[{"type":"Account","id":"phase13h-allowed-account-004","name":"Allowed Synthetic Co","fields":{"Account_Type__c":"Customer - Active","Industry":"Manufacturing"}},{"type":"Opportunity","id":"phase13h-allowed-opp-004","name":"Allowed Synthetic Pilot","fields":{"StageName":"Discovery","Type":"New Business"}}]}}'::jsonb,
   'phase13h-allowed-sha-004', now()::text, now()::text),
  ('phase13h-blocked-call-001', 'Phase 13h blocked shared Postgres call', '2026-04-16T15:00:00Z', 1800, 2, true,
   '{"id":"phase13h-blocked-call-001","title":"Phase 13h blocked shared Postgres call","started":"2026-04-16T15:00:00Z","duration":1800,"metaData":{"scope":"External","system":"Zoom","direction":"Conference","parties":[{"id":"phase13h-blocked-buyer","emailAddress":"buyer@blockedsynthetic.example"}]},"context":{"crmObjects":[{"type":"Account","id":"phase13h-blocked-account-001","name":"Blocked Synthetic Corp","fields":{"Name":"Blocked Synthetic Corp","Account_Type__c":"Restricted"}},{"type":"Opportunity","id":"phase13h-blocked-opp-001","name":"Blocked Synthetic Opportunity","fields":{"StageName":"Closed Lost","Type":"Renewal"}}]}}'::jsonb,
   'phase13h-blocked-sha-001', now()::text, now()::text);

INSERT INTO transcripts(call_id, raw_json, raw_sha256, segment_count, first_seen_at, updated_at)
VALUES
  ('phase13h-allowed-call-001', '{"callId":"phase13h-allowed-call-001"}'::jsonb, 'phase13h-allowed-tr-001', 2, now()::text, now()::text),
  ('phase13h-allowed-call-002', '{"callId":"phase13h-allowed-call-002"}'::jsonb, 'phase13h-allowed-tr-002', 2, now()::text, now()::text),
  ('phase13h-allowed-call-003', '{"callId":"phase13h-allowed-call-003"}'::jsonb, 'phase13h-allowed-tr-003', 2, now()::text, now()::text),
  ('phase13h-allowed-call-004', '{"callId":"phase13h-allowed-call-004"}'::jsonb, 'phase13h-allowed-tr-004', 2, now()::text, now()::text),
  ('phase13h-blocked-call-001', '{"callId":"phase13h-blocked-call-001"}'::jsonb, 'phase13h-blocked-tr-001', 2, now()::text, now()::text);

INSERT INTO transcript_segments(call_id, segment_index, speaker_id, start_ms, end_ms, text, raw_json) VALUES
  ('phase13h-allowed-call-001', 0, 'speaker-1', 0, 5000, 'This allowed Phase 13h synthetic transcript discusses shared Postgres deployment.', '{"text":"allowed-1"}'::jsonb),
  ('phase13h-allowed-call-001', 1, 'speaker-2', 5000, 10000, 'Allowed call one covers analyst tool coverage on the redacted serving DB.', '{"text":"allowed-1b"}'::jsonb),
  ('phase13h-allowed-call-002', 0, 'speaker-1', 0, 5000, 'Allowed renewal call describes shared Postgres deployment and analyst presets.', '{"text":"allowed-2"}'::jsonb),
  ('phase13h-allowed-call-002', 1, 'speaker-2', 5000, 10000, 'Renewal stakeholders confirmed shared Postgres deployment timing.', '{"text":"allowed-2b"}'::jsonb),
  ('phase13h-allowed-call-003', 0, 'speaker-1', 0, 5000, 'Allowed pilot call covers shared Postgres deployment and small-cell suppression.', '{"text":"allowed-3"}'::jsonb),
  ('phase13h-allowed-call-003', 1, 'speaker-2', 5000, 10000, 'Pilot stakeholders confirmed shared Postgres deployment posture.', '{"text":"allowed-3b"}'::jsonb),
  ('phase13h-allowed-call-004', 0, 'speaker-1', 0, 5000, 'Allowed pilot call adds shared Postgres deployment coverage for analyst preset.', '{"text":"allowed-4"}'::jsonb),
  ('phase13h-allowed-call-004', 1, 'speaker-2', 5000, 10000, 'Allowed call four reinforces shared Postgres deployment evidence.', '{"text":"allowed-4b"}'::jsonb),
  ('phase13h-blocked-call-001', 0, 'speaker-1', 0, 5000, 'This blocked transcript references the restricted Blocked Synthetic Corp customer.', '{"text":"blocked-1"}'::jsonb),
  ('phase13h-blocked-call-001', 1, 'speaker-2', 5000, 10000, 'Blocked transcript continued discussion of restricted Blocked Synthetic Corp deployment.', '{"text":"blocked-1b"}'::jsonb);
SQL

GONG_DATABASE_URL="$SOURCE_URL" go run ./cmd/gongctl sync read-model --rebuild >"$ARTIFACT_DIR/source-read-model-rebuild.json"
grep -q '"ready": true' "$ARTIFACT_DIR/source-read-model-rebuild.json"
operator_psql "$SOURCE_DB" -tAc "SELECT COUNT(*) FROM calls" >"$ARTIFACT_DIR/source-call-count.txt"
operator_psql "$SOURCE_DB" -tAc "SELECT COUNT(*) FROM calls WHERE call_id = 'phase13h-blocked-call-001'" >"$ARTIFACT_DIR/source-blocked-call-count.txt"
grep -q '^1$' "$ARTIFACT_DIR/source-blocked-call-count.txt"

# Step 3: create empty target serving database on the same Postgres server.
operator_psql postgres -c "DROP DATABASE IF EXISTS ${TARGET_DB}" >/dev/null
operator_psql postgres -c "CREATE DATABASE ${TARGET_DB} OWNER ${GONGCTL_POSTGRES_USER}" >/dev/null
operator_psql "$TARGET_DB" -tAc "SELECT 1" >/dev/null

# Step 4: governance refresh from source -> target.
cat >"$GOVERNANCE_CONFIG" <<'YAML'
version: 1
lists:
  no_ai:
    customers:
      - name: "Blocked Synthetic Corp"
        aliases: ["blockedsynthetic.example"]
YAML
chmod 600 "$GOVERNANCE_CONFIG"

go run ./cmd/gongctl governance refresh-serving-db \
  --source "$SOURCE_URL" \
  --target "$TARGET_WRITER_URL" \
  --config "$GOVERNANCE_CONFIG" \
  >"$REFRESH_OUT"

python3 - "$REFRESH_OUT" <<'PY'
import json
import sys

with open(sys.argv[1], "r", encoding="utf-8") as handle:
    response = json.load(handle)
result = response.get("result") or {}
if result.get("backend") != "postgres":
    raise SystemExit(f"refresh-serving-db backend={result.get('backend')!r}, want postgres")
if result.get("removed_calls", 0) < 1:
    raise SystemExit(f"removed_calls must be >= 1; got {result.get('removed_calls')}")
if result.get("source_calls", 0) <= result.get("target_calls", 0):
    raise SystemExit(f"target_calls must be < source_calls: {result}")
for required in ("policy_config_sha256", "source_data_fingerprint", "target_data_fingerprint"):
    if not result.get(required):
        raise SystemExit(f"refresh-serving-db output missing {required}")
PY
assert_no_leak "refresh-serving-db" "$REFRESH_OUT"

# Step 5: target schema validation -- restricted call rows must be absent.
operator_psql "$TARGET_DB" -tA -F '|' -c "
SELECT 'calls', COUNT(*) FROM calls WHERE call_id = 'phase13h-blocked-call-001'
UNION ALL SELECT 'transcripts', COUNT(*) FROM transcripts WHERE call_id = 'phase13h-blocked-call-001'
UNION ALL SELECT 'transcript_segments', COUNT(*) FROM transcript_segments WHERE call_id = 'phase13h-blocked-call-001'
UNION ALL SELECT 'context_objects', COUNT(*) FROM call_context_objects WHERE call_id = 'phase13h-blocked-call-001'
UNION ALL SELECT 'context_fields', COUNT(*) FROM call_context_fields WHERE call_id = 'phase13h-blocked-call-001'
UNION ALL SELECT 'call_facts', COUNT(*) FROM call_facts WHERE call_id = 'phase13h-blocked-call-001'
ORDER BY 1" >"$TARGET_RESTRICTED_COUNTS_OUT"
python3 - "$TARGET_RESTRICTED_COUNTS_OUT" <<'PY'
import sys

bad = []
with open(sys.argv[1], "r", encoding="utf-8") as handle:
    for line in handle:
        line = line.strip()
        if not line:
            continue
        table, count = line.split("|")
        if int(count) != 0:
            bad.append((table, count))
if bad:
    raise SystemExit(f"redacted target still contains restricted rows: {bad}")
PY

operator_psql "$TARGET_DB" -tA -F '|' -c "
SELECT 'calls', COUNT(*) FROM calls
UNION ALL SELECT 'transcripts', COUNT(*) FROM transcripts
UNION ALL SELECT 'transcript_segments', COUNT(*) FROM transcript_segments
UNION ALL SELECT 'call_facts', COUNT(*) FROM call_facts
ORDER BY 1" >"$TARGET_ALLOWED_COUNTS_OUT"

# Phase 13b: prove the redacted serving DB physically lacks the restricted
# synthetic customer name in any text or JSON column we copy.
operator_psql "$TARGET_DB" -tA -F '|' -c "
SELECT 'calls.title', COUNT(*) FROM calls WHERE title ILIKE '%Blocked Synthetic Corp%'
UNION ALL SELECT 'calls.raw_json', COUNT(*) FROM calls WHERE raw_json::text ILIKE '%Blocked Synthetic Corp%'
UNION ALL SELECT 'context_objects.name', COUNT(*) FROM call_context_objects WHERE object_name ILIKE '%Blocked Synthetic Corp%'
UNION ALL SELECT 'context_fields.value', COUNT(*) FROM call_context_fields WHERE field_value_text ILIKE '%Blocked Synthetic Corp%'
UNION ALL SELECT 'transcript_segments.text', COUNT(*) FROM transcript_segments WHERE text ILIKE '%Blocked Synthetic Corp%'
UNION ALL SELECT 'call_facts.title', COUNT(*) FROM call_facts WHERE title ILIKE '%Blocked Synthetic Corp%'
ORDER BY 1" >"$TARGET_RESTRICTED_TEXT_COUNTS_OUT"
python3 - "$TARGET_RESTRICTED_TEXT_COUNTS_OUT" <<'PY'
import sys

bad = []
with open(sys.argv[1], "r", encoding="utf-8") as handle:
    for line in handle:
        line = line.strip()
        if not line:
            continue
        column, count = line.split("|")
        if int(count) != 0:
            bad.append((column, count))
if bad:
    raise SystemExit(f"redacted target still contains restricted name in: {bad}")
PY

# Step 6: create the scoped analyst reader role on the Postgres server. Roles
# are server-wide; we create it once and apply scoped grants on the target DB.
analyst_password_sql="$(printf "%s" "$GONGMCP_ANALYST_READER_PASSWORD" | python3 -c "import sys; v=sys.stdin.read(); print(\"'\" + v.replace(\"'\", \"''\") + \"'\")")"
operator_psql "$TARGET_DB" >/dev/null <<SQL
SELECT pg_terminate_backend(pid)
  FROM pg_stat_activity
 WHERE usename = '${SCOPED_ROLE}'
   AND pid <> pg_backend_pid();
DROP ROLE IF EXISTS ${SCOPED_ROLE};
CREATE ROLE ${SCOPED_ROLE} LOGIN NOINHERIT PASSWORD ${analyst_password_sql};
SQL

# Step 7: apply analyst-expansion scoped reader grants on the redacted serving
# DB. Note the --database flag is the redacted serving DB name; the writer URL
# the command connects with also points at the same target DB.
GONG_DATABASE_URL="$TARGET_WRITER_URL" go run ./cmd/gongctl mcp postgres-reader-apply \
  --preset analyst-expansion \
  --role "$SCOPED_ROLE" \
  --database "$TARGET_DB" \
  --apply \
  >"$ANALYST_APPLY_OUT"
python3 - "$ANALYST_APPLY_OUT" <<'PY'
import json
import sys

with open(sys.argv[1], "r", encoding="utf-8") as handle:
    response = json.load(handle)
if response.get("backend") != "postgres":
    raise SystemExit(f"reader-apply backend={response.get('backend')!r}")
if response.get("preset") not in ("analyst-expansion", "analyst"):
    raise SystemExit(f"reader-apply preset={response.get('preset')!r}")
if response.get("status") != "applied" or not response.get("applied"):
    raise SystemExit(f"reader-apply did not apply: {response}")
if response.get("credential_note") != "database_url_not_exported":
    raise SystemExit(f"reader-apply credential_note={response.get('credential_note')!r}")
PY
assert_no_leak "analyst-reader-apply" "$ANALYST_APPLY_OUT"

# Step 8: gongmcp under analyst-expansion preset with scoped grant
# enforcement, against the redacted serving DB via the analyst reader URL.
{
  printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"build_call_cohort","arguments":{"filter":{"query":"shared Postgres","from_date":"2026-04-10","to_date":"2026-04-30","limit":25},"limit":5}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"search_transcript_segments","arguments":{"query":"shared Postgres deployment","limit":5}}}'
  # summarize_themes_by_dimension with dimension=industry over the redacted
  # serving DB. Synthetic seed shape: 3 calls Industry=Manufacturing and 1
  # call Industry=Healthcare. With the analyst preset's small-cell minimum
  # cohort size of 3, the Healthcare bucket should be suppressed.
  printf '%s\n' '{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"summarize_themes_by_dimension","arguments":{"filter":{"query":"shared Postgres","from_date":"2026-04-10","to_date":"2026-04-30","limit":25},"theme_query":"shared Postgres","dimension":"industry"}}}'
} | GONG_DATABASE_URL="$ANALYST_READER_URL" GONGMCP_TOOL_PRESET=analyst-expansion GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS=1 go run ./cmd/gongmcp >"$ANALYST_MCP_OUT"
assert_mcp_success "$ANALYST_MCP_OUT" 3 4 5
grep -q '"build_call_cohort"' "$ANALYST_MCP_OUT"
grep -q '"search_transcript_segments"' "$ANALYST_MCP_OUT"
grep -q '"summarize_themes_by_dimension"' "$ANALYST_MCP_OUT"
# Small-cell suppression must be active for the analyst preset under scoped
# grant enforcement. The scoped Postgres analyst posture sets a min cohort
# size of 3 (see cmd/gongmcp/main.go::postgresAnalystSmallCellMin and
# internal/mcp/business_analysis_tools.go::applyBusinessAnalysisSmallCellSuppression).
# The synthetic industry bucket distribution is 3 Manufacturing + 1 Healthcare,
# so the Healthcare bucket must be suppressed in the response.
grep -q 'small_cell_suppression_applied' "$ANALYST_MCP_OUT"
grep -q 'small_cell_suppression_min_3' "$ANALYST_MCP_OUT"
assert_no_leak "analyst MCP evidence" "$ANALYST_MCP_OUT"

# Phase 13b: broad analyst / company-search behavior over the redacted serving
# DB. Every probe runs as the scoped analyst-expansion reader against
# gongctl_mcp. Allowed-company probes must succeed with non-zero results;
# restricted-company probes must return zero results without leaking the
# restricted name through any field.
{
  printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search_calls_by_filters","arguments":{"filter":{"account_query":"Allowed Synthetic Co","from_date":"2026-04-10","to_date":"2026-04-30","limit":25},"include_account_names":true,"limit":10}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"search_calls_by_filters","arguments":{"filter":{"account_query":"Blocked Synthetic Corp","from_date":"2026-04-10","to_date":"2026-04-30","limit":25},"include_account_names":true,"limit":10}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"search_transcripts_by_filters","arguments":{"filter":{"account_query":"Allowed Synthetic Co","from_date":"2026-04-10","to_date":"2026-04-30","limit":25},"include_account_names":true,"theme_query":"shared Postgres","limit":5}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"search_transcripts_by_filters","arguments":{"filter":{"account_query":"Blocked Synthetic Corp","from_date":"2026-04-10","to_date":"2026-04-30","limit":25},"include_account_names":true,"theme_query":"shared Postgres","limit":5}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"summarize_calls_by_lifecycle","arguments":{}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"crm_field_population_matrix","arguments":{"object_type":"Account","group_by_field":"industry","limit":25}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"search_transcripts_by_crm_context","arguments":{"object_type":"Account","query":"shared Postgres","limit":5}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"build_quote_pack","arguments":{"filter":{"from_date":"2026-04-10","to_date":"2026-04-30","limit":25},"theme_query":"shared Postgres","limit":5}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"build_quote_pack","arguments":{"filter":{"account_query":"Blocked Synthetic Corp","from_date":"2026-04-10","to_date":"2026-04-30","limit":25},"include_account_names":true,"theme_query":"Blocked Synthetic Corp","limit":5}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":12,"method":"tools/call","params":{"name":"diagnose_attribution_coverage","arguments":{"filter":{"from_date":"2026-04-10","to_date":"2026-04-30","limit":25}}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":13,"method":"tools/call","params":{"name":"explain_analysis_limitations","arguments":{"filter":{"from_date":"2026-04-10","to_date":"2026-04-30","limit":25}}}}'
} | GONG_DATABASE_URL="$ANALYST_READER_URL" GONGMCP_TOOL_PRESET=analyst-expansion GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS=1 GONGMCP_POSTGRES_REDACTED_SERVING_DB=1 go run ./cmd/gongmcp >"$BROAD_MCP_OUT"
assert_mcp_success "$BROAD_MCP_OUT" 3 4 5 6 7 8 9 10 11 12 13

python3 - "$BROAD_MCP_OUT" <<'PY'
import json
import sys

path = sys.argv[1]
required_present = {3, 5, 7, 8, 9, 10, 12, 13}
required_zero = {4, 6, 11}
seen = {}
with open(path, "r", encoding="utf-8") as handle:
    for line in handle:
        line = line.strip()
        if not line:
            continue
        try:
            message = json.loads(line)
        except json.JSONDecodeError:
            continue
        rid = message.get("id")
        if not isinstance(rid, int):
            continue
        seen[rid] = message

missing = (required_present | required_zero) - set(seen)
if missing:
    raise SystemExit(f"missing MCP result ids: {sorted(missing)}")

def tool_payload(message):
    result = message.get("result") or {}
    contents = result.get("content") or []
    for entry in contents:
        if entry.get("type") == "text":
            text = entry.get("text") or ""
            try:
                return json.loads(text)
            except json.JSONDecodeError:
                pass
    raise SystemExit(f"id {message.get('id')} has no JSON content payload")

for rid in sorted(required_present):
    message = seen[rid]
    payload = tool_payload(message)
    if rid == 7:
        if not isinstance(payload, list) or len(payload) == 0:
            raise SystemExit(f"id 7 expected non-empty lifecycle summary list, got {payload}")
        continue
    if rid == 8:
        cells = payload.get("cells") if isinstance(payload, dict) else None
        if not isinstance(cells, list) or len(cells) == 0:
            raise SystemExit(f"id 8 expected non-empty CRM matrix cells, got {payload}")
        continue
    tool = payload.get("tool")
    if not tool:
        raise SystemExit(f"id {rid} payload missing tool name: {payload}")
    if rid in (3, 5, 10):
        count = payload.get("count")
        if not isinstance(count, int) or count <= 0:
            raise SystemExit(f"id {rid} ({tool}) expected non-zero count over the allowed cohort, got {count}")

# Restricted-company probes must produce zero results.
for rid in sorted(required_zero):
    message = seen[rid]
    payload = tool_payload(message)
    count = payload.get("count")
    if count not in (0, None):
        raise SystemExit(f"id {rid} restricted-company probe leaked {count} results: {payload}")
    results = payload.get("results") or []
    quotes = payload.get("quotes") or []
    summaries = payload.get("summaries") or []
    if results or quotes or summaries:
        raise SystemExit(f"id {rid} restricted-company probe returned non-empty results/quotes/summaries")
PY

# Redact analyst-supplied restricted query strings before retaining the JSON-RPC
# transcript as evidence. The validation above runs against the raw response;
# retained artifacts must not contain blocklist values even when gongmcp echoes
# normalized_filter.account_query/theme_query.
python3 - "$BROAD_MCP_OUT" <<'PY'
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
body = path.read_text(encoding="utf-8")
body = body.replace("Blocked Synthetic Corp", "[REDACTED_RESTRICTED_QUERY]")
path.write_text(body, encoding="utf-8")
PY
assert_no_leak "broad analyst MCP evidence" "$BROAD_MCP_OUT"

# Phase 13b: confirm Postgres mode rejects the all-readonly preset entirely,
# even when the scoped analyst reader role is configured. all-readonly stays
# off-limits for the redacted serving DB.
if GONG_DATABASE_URL="$ANALYST_READER_URL" GONGMCP_TOOL_PRESET=all-readonly GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS=1 go run ./cmd/gongmcp </dev/null >"$ALL_READONLY_REJECTED_OUT" 2>&1; then
  echo "all-readonly preset unexpectedly accepted under Postgres mode" >&2
  cat "$ALL_READONLY_REJECTED_OUT" >&2
  exit 1
fi
grep -q 'all-readonly is not supported by the postgres vertical slice' "$ALL_READONLY_REJECTED_OUT"
assert_no_leak "all-readonly rejection" "$ALL_READONLY_REJECTED_OUT"

# Step 9: direct SQL denial as the scoped analyst role on the serving DB.
if analyst_psql -c "SELECT raw_json FROM calls LIMIT 1" >"$ANALYST_RAW_DENIED_OUT" 2>&1; then
  echo "scoped analyst role unexpectedly read raw call payloads on the serving DB" >&2
  cat "$ANALYST_RAW_DENIED_OUT" >&2
  exit 1
fi
grep -qi 'permission denied' "$ANALYST_RAW_DENIED_OUT"

if analyst_psql -c "SELECT cursor FROM sync_runs LIMIT 1" >"$ANALYST_SYNC_DENIED_OUT" 2>&1; then
  echo "scoped analyst role unexpectedly read operator sync_runs on the serving DB" >&2
  cat "$ANALYST_SYNC_DENIED_OUT" >&2
  exit 1
fi
# sync_runs table itself is empty/skipped on the serving DB; either permission
# denied or relation missing is acceptable (governance scope is what matters).
grep -Eqi 'permission denied|does not exist' "$ANALYST_SYNC_DENIED_OUT"

if analyst_psql -c "SELECT id FROM profile_meta LIMIT 1" >"$ANALYST_PROFILE_DENIED_OUT" 2>&1; then
  echo "scoped analyst role unexpectedly read profile_meta on the serving DB" >&2
  cat "$ANALYST_PROFILE_DENIED_OUT" >&2
  exit 1
fi
grep -Eqi 'permission denied|does not exist' "$ANALYST_PROFILE_DENIED_OUT"

# Step 10: aggregate sanitized summary for reviewers; never include URLs,
# customer names, blocklist values, raw call IDs, or transcript text.
python3 - "$REFRESH_OUT" "$ANALYST_APPLY_OUT" "$TARGET_ALLOWED_COUNTS_OUT" "$PHASE13H_SUMMARY" <<'PY'
import json
import sys

refresh_path, apply_path, counts_path, out_path = sys.argv[1:]

with open(refresh_path, "r", encoding="utf-8") as handle:
    refresh_response = json.load(handle).get("result") or {}
with open(apply_path, "r", encoding="utf-8") as handle:
    apply_response = json.load(handle)

target_counts = {}
with open(counts_path, "r", encoding="utf-8") as handle:
    for line in handle:
        line = line.strip()
        if not line:
            continue
        table, count = line.split("|")
        target_counts[table] = int(count)

summary = {
    "phase": "13h",
    "backend": refresh_response.get("backend"),
    "source_calls": refresh_response.get("source_calls"),
    "target_calls": refresh_response.get("target_calls"),
    "removed_calls": refresh_response.get("removed_calls"),
    "suppressed_call_count": refresh_response.get("suppressed_call_count"),
    "policy_config_sha256_present": bool(refresh_response.get("policy_config_sha256")),
    "source_data_fingerprint_present": bool(refresh_response.get("source_data_fingerprint")),
    "target_data_fingerprint_present": bool(refresh_response.get("target_data_fingerprint")),
    "skipped_tables": refresh_response.get("skipped_tables"),
    "scoped_reader_preset": apply_response.get("preset"),
    "scoped_reader_status": apply_response.get("status"),
    "scoped_reader_credential_note": apply_response.get("credential_note"),
    "scoped_reader_role_note": apply_response.get("role_note"),
    "target_table_counts": target_counts,
}
with open(out_path, "w", encoding="utf-8") as handle:
    json.dump(summary, handle, indent=2, sort_keys=True)
PY
assert_no_leak "phase 13h summary" "$PHASE13H_SUMMARY"

# Phase 13b sanitized summary: counts only, no URLs/customer names/IDs/text.
python3 - "$BROAD_MCP_OUT" "$PHASE13B_SUMMARY" <<'PY'
import json
import sys

path, out_path = sys.argv[1:]

allowed_ids = {3, 5, 7, 8, 9, 10, 12, 13}
restricted_ids = {4, 6, 11}
tools_seen = {}
restricted_counts = {}
allowed_counts = {}
with open(path, "r", encoding="utf-8") as handle:
    for line in handle:
        line = line.strip()
        if not line:
            continue
        try:
            message = json.loads(line)
        except json.JSONDecodeError:
            continue
        rid = message.get("id")
        if not isinstance(rid, int):
            continue
        result = message.get("result") or {}
        contents = result.get("content") or []
        payload = None
        for entry in contents:
            if entry.get("type") == "text":
                try:
                    payload = json.loads(entry.get("text") or "")
                except json.JSONDecodeError:
                    payload = None
                break
        if not payload:
            continue
        if isinstance(payload, list):
            tool = "summarize_calls_by_lifecycle" if rid == 7 else "list_payload"
            count = len(payload)
        else:
            tool = payload.get("tool") or ("crm_field_population_matrix" if rid == 8 else "")
            count = payload.get("count")
            if rid == 8 and count is None:
                count = len(payload.get("cells") or [])
        tools_seen[rid] = tool
        if rid in allowed_ids:
            allowed_counts[rid] = count
        if rid in restricted_ids:
            restricted_counts[rid] = count

summary = {
    "phase": "13b",
    "allowed_company_search_tools": [tools_seen.get(rid) for rid in sorted(allowed_ids)],
    "restricted_probe_tools": [tools_seen.get(rid) for rid in sorted(restricted_ids)],
    "allowed_company_search_counts": [allowed_counts.get(rid) for rid in sorted(allowed_ids)],
    "restricted_probe_counts": [restricted_counts.get(rid) for rid in sorted(restricted_ids)],
    "all_readonly_rejected": True,
}
with open(out_path, "w", encoding="utf-8") as handle:
    json.dump(summary, handle, indent=2, sort_keys=True)
PY
assert_no_leak "phase 13b summary" "$PHASE13B_SUMMARY"

if [ "${GONGCTL_PHASE13H_KEEP_ARTIFACTS:-0}" = "1" ]; then
  echo "Phase 13h+13b smoke evidence retained at: $ARTIFACT_DIR" >&2
fi
echo "Phase 13h scoped analyst + Phase 13b broad analyst smoke on redacted Postgres serving DB: ok"
