#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE_FILE="$ROOT/docker-compose.postgres.yml"
PROJECT="${GONGCTL_POSTGRES_COMPOSE_PROJECT:-gongctl-postgres-smoke}"
PORT="${GONGCTL_POSTGRES_PORT:-55432}"
export GONGCTL_POSTGRES_USER="${GONGCTL_POSTGRES_USER:-gongctl}"
export GONGCTL_POSTGRES_PASSWORD="${GONGCTL_POSTGRES_PASSWORD:-gongctl_dev_password}"
export GONGMCP_READER_PASSWORD="${GONGMCP_READER_PASSWORD:-gongmcp_reader_dev_password}"

urlencode() {
  python3 -c 'from urllib.parse import quote; import sys; print(quote(sys.argv[1], safe=""))' "$1"
}

WRITER_URL="postgres://$(urlencode "$GONGCTL_POSTGRES_USER"):$(urlencode "$GONGCTL_POSTGRES_PASSWORD")@127.0.0.1:${PORT}/gongctl?sslmode=disable"
READER_URL="postgres://gongmcp_reader:$(urlencode "$GONGMCP_READER_PASSWORD")@127.0.0.1:${PORT}/gongctl?sslmode=disable"

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

GONG_DATABASE_URL="$WRITER_URL" go run ./cmd/gongctl sync synthetic >/tmp/gongctl-postgres-sync.json
GONG_DATABASE_URL="$WRITER_URL" go run ./cmd/gongctl sync read-model >/tmp/gongctl-postgres-read-model-state.json
grep -q '"status": "current"' /tmp/gongctl-postgres-read-model-state.json
grep -q '"call_count": 2' /tmp/gongctl-postgres-read-model-state.json
grep -q '"fact_count": 2' /tmp/gongctl-postgres-read-model-state.json
grep -q '"ready": true' /tmp/gongctl-postgres-read-model-state.json
cat >/tmp/gongctl-postgres-profile.yaml <<'YAML'
version: 1
name: Synthetic Postgres profile
objects:
  deal:
    object_types: [Opportunity]
  account:
    object_types: [Account]
lifecycle:
  open:
    order: 10
  closed_won:
    order: 20
  closed_lost:
    order: 30
  post_sales:
    order: 40
  unknown:
    order: 999
methodology:
  pain:
    description: Discovery pain
    aliases: [pain]
YAML
GONG_DATABASE_URL="$WRITER_URL" go run ./cmd/gongctl profile validate --profile /tmp/gongctl-postgres-profile.yaml >/tmp/gongctl-postgres-profile-validate.json
grep -q '"valid": true' /tmp/gongctl-postgres-profile-validate.json
GONG_DATABASE_URL="$WRITER_URL" go run ./cmd/gongctl profile import --profile /tmp/gongctl-postgres-profile.yaml >/tmp/gongctl-postgres-profile-import.json
grep -q '"imported": true' /tmp/gongctl-postgres-profile-import.json
GONG_DATABASE_URL="$WRITER_URL" go run ./cmd/gongctl profile history >/tmp/gongctl-postgres-profile-history.json
grep -q '"name": "Synthetic Postgres profile"' /tmp/gongctl-postgres-profile-history.json
GONG_DATABASE_URL="$WRITER_URL" go run ./cmd/gongctl profile show >/tmp/gongctl-postgres-profile-show.json
grep -q '"name": "Synthetic Postgres profile"' /tmp/gongctl-postgres-profile-show.json
GONG_DATABASE_URL="$READER_URL" go run ./cmd/gongctl sync status >/tmp/gongctl-postgres-status-with-profile.json
grep -q '"cache_status": "not_implemented"' /tmp/gongctl-postgres-status-with-profile.json
GONG_DATABASE_URL="$READER_URL" go run ./cmd/gongctl calls show --call-id synthetic-call-001 --json >/tmp/gongctl-postgres-calls-show.json
grep -q '"call_id": "synthetic-call-001"' /tmp/gongctl-postgres-calls-show.json
grep -q '"title": "Pulsaris implementation kickoff"' /tmp/gongctl-postgres-calls-show.json
if grep -q 'raw_json\|crmObjects\|speaker-1' /tmp/gongctl-postgres-calls-show.json; then
  echo "postgres calls show exposed raw call payload fields" >&2
  exit 1
fi

docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -tA -F '|' -c "SELECT 'call_context_objects', COUNT(*) FROM call_context_objects UNION ALL SELECT 'call_context_fields', COUNT(*) FROM call_context_fields UNION ALL SELECT 'call_facts', COUNT(*) FROM call_facts ORDER BY 1" >/tmp/gongctl-postgres-normalized-counts.txt
grep -q 'call_facts|2' /tmp/gongctl-postgres-normalized-counts.txt
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -tA -F '|' -c "SELECT call_id, transcript_present, transcript_status, lifecycle_bucket FROM call_facts ORDER BY call_id" >/tmp/gongctl-postgres-call-facts.txt
grep -q 'synthetic-call-001|t|present|' /tmp/gongctl-postgres-call-facts.txt
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -tA -F '|' -c "SELECT call_id, object_count, field_count, object_limit_exceeded, field_limit_exceeded, last_error FROM call_read_model_diagnostics ORDER BY call_id" >/tmp/gongctl-postgres-read-model-diagnostics.txt
grep -q 'synthetic-call-001|0|0|f|f|' /tmp/gongctl-postgres-read-model-diagnostics.txt
grep -q 'synthetic-call-002|0|0|f|f|' /tmp/gongctl-postgres-read-model-diagnostics.txt

docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -c "DELETE FROM call_facts WHERE call_id = 'synthetic-call-002'" >/dev/null
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -c "INSERT INTO call_facts(call_id, title, updated_at) VALUES('synthetic-orphan-fact', 'orphan fact', now()::text)" >/dev/null
GONG_DATABASE_URL="$WRITER_URL" go run ./cmd/gongctl sync read-model >/tmp/gongctl-postgres-integrity-gap.json
grep -q '"status": "stale"' /tmp/gongctl-postgres-integrity-gap.json
grep -q '"missing_fact_call_count": 1' /tmp/gongctl-postgres-integrity-gap.json
grep -q '"orphan_fact_count": 1' /tmp/gongctl-postgres-integrity-gap.json
if GONG_DATABASE_URL="$READER_URL" GONGMCP_TOOL_PRESET=business-pilot go run ./cmd/gongmcp </dev/null >/tmp/gongctl-postgres-stale-mcp.txt 2>&1; then
  echo "read-only MCP unexpectedly started with a stale Postgres read model" >&2
  exit 1
fi
grep -q 'postgres read model is missing or stale' /tmp/gongctl-postgres-stale-mcp.txt
GONG_DATABASE_URL="$WRITER_URL" go run ./cmd/gongctl sync read-model --rebuild >/tmp/gongctl-postgres-read-model-rebuild.json
grep -q '"status": "rebuilt"' /tmp/gongctl-postgres-read-model-rebuild.json
grep -q '"ready": true' /tmp/gongctl-postgres-read-model-rebuild.json
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 -c "REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA public FROM gongmcp_reader" >/tmp/gongctl-postgres-reader-regrant.txt
GONG_DATABASE_URL="$WRITER_URL" go run ./cmd/gongctl sync read-model --rebuild >>/tmp/gongctl-postgres-reader-regrant.txt
grep -q '"status": "rebuilt"' /tmp/gongctl-postgres-reader-regrant.txt

if reader_psql -c "SELECT raw_json FROM calls LIMIT 1" >/tmp/gongctl-postgres-reader-raw-read.txt 2>&1; then
  echo "reader role unexpectedly read raw call JSON" >&2
  exit 1
fi
if reader_psql -c "SELECT cursor FROM sync_runs LIMIT 1" >/tmp/gongctl-postgres-reader-sensitive-read.txt 2>&1; then
  echo "reader role unexpectedly read sync cursor" >&2
  exit 1
fi
if reader_psql -c "SELECT field_value_text FROM call_context_fields LIMIT 1" >>/tmp/gongctl-postgres-reader-sensitive-read.txt 2>&1; then
  echo "reader role unexpectedly read normalized CRM field values" >&2
  exit 1
fi
if reader_psql -c "SELECT object_name FROM call_context_objects LIMIT 1" >>/tmp/gongctl-postgres-reader-sensitive-read.txt 2>&1; then
  echo "reader role unexpectedly read normalized CRM object names" >&2
  exit 1
fi
if reader_psql -c "SELECT text FROM transcript_segments LIMIT 1" >>/tmp/gongctl-postgres-reader-sensitive-read.txt 2>&1; then
  echo "reader role unexpectedly read full transcript text" >&2
  exit 1
fi
if reader_psql -c "SELECT search_vector FROM transcript_segments LIMIT 1" >>/tmp/gongctl-postgres-reader-sensitive-read.txt 2>&1; then
  echo "reader role unexpectedly read transcript search vector" >&2
  exit 1
fi
if reader_psql -c "SELECT * FROM gongmcp_transcript_segments LIMIT 1" >>/tmp/gongctl-postgres-reader-sensitive-read.txt 2>&1; then
  echo "reader role unexpectedly read retired transcript segment view" >&2
  exit 1
fi
if reader_psql -c "SELECT opportunity_amount FROM call_facts LIMIT 1" >>/tmp/gongctl-postgres-reader-sensitive-read.txt 2>&1; then
  echo "reader role unexpectedly read sensitive opportunity amount" >&2
  exit 1
fi
if reader_psql -c "SELECT raw_yaml FROM profile_meta LIMIT 1" >>/tmp/gongctl-postgres-reader-sensitive-read.txt 2>&1; then
  echo "reader role unexpectedly read raw profile YAML" >&2
  exit 1
fi
if reader_psql -c "SELECT canonical_json FROM profile_meta LIMIT 1" >>/tmp/gongctl-postgres-reader-sensitive-read.txt 2>&1; then
  echo "reader role unexpectedly read canonical profile JSON" >&2
  exit 1
fi
if reader_psql -c "SELECT * FROM profile_object_alias LIMIT 1" >>/tmp/gongctl-postgres-reader-sensitive-read.txt 2>&1; then
  echo "reader role unexpectedly read profile object projection table" >&2
  exit 1
fi
if reader_psql -c "SELECT * FROM profile_lifecycle_rule LIMIT 1" >>/tmp/gongctl-postgres-reader-sensitive-read.txt 2>&1; then
  echo "reader role unexpectedly read profile lifecycle projection table" >&2
  exit 1
fi
if reader_psql -c "SELECT * FROM profile_validation_warning LIMIT 1" >>/tmp/gongctl-postgres-reader-sensitive-read.txt 2>&1; then
  echo "reader role unexpectedly read profile warning table" >&2
  exit 1
fi

if reader_psql -c "INSERT INTO users(user_id, raw_json, raw_sha256, first_seen_at, updated_at) VALUES('should-fail', '{}'::jsonb, 'x', now()::text, now()::text)" >/tmp/gongctl-postgres-reader-write.txt 2>&1; then
  echo "reader role unexpectedly wrote to Postgres" >&2
  exit 1
fi

{
  printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_sync_status","arguments":{}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"search_calls","arguments":{"limit":5}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"search_transcript_segments","arguments":{"query":"shared Postgres","limit":5}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"get_call","arguments":{"call_id":"synthetic-call-001"}}}'
} | GONG_DATABASE_URL="$READER_URL" GONGMCP_TOOL_PRESET=operator-smoke go run ./cmd/gongmcp > /tmp/gongctl-postgres-mcp.jsonl

grep -q '"get_sync_status"' /tmp/gongctl-postgres-mcp.jsonl
grep -q '"search_calls"' /tmp/gongctl-postgres-mcp.jsonl
grep -q '"search_transcript_segments"' /tmp/gongctl-postgres-mcp.jsonl
grep -q '"get_call"' /tmp/gongctl-postgres-mcp.jsonl
grep -q 'synthetic-call-001' /tmp/gongctl-postgres-mcp.jsonl
grep -q 'shared.*Postgres' /tmp/gongctl-postgres-mcp.jsonl
assert_mcp_success /tmp/gongctl-postgres-mcp.jsonl 3 4 5 6
if grep -q 'raw_json\|crmObjects' /tmp/gongctl-postgres-mcp.jsonl; then
  echo "get_call/search smoke exposed raw call payload fields" >&2
  exit 1
fi

{
  printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"get_sync_status","arguments":{}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"summarize_call_facts","arguments":{"group_by":"transcript_status","limit":5}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"summarize_calls_by_lifecycle","arguments":{}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"rank_transcript_backlog","arguments":{"limit":5}}}'
} | GONG_DATABASE_URL="$READER_URL" GONGMCP_TOOL_PRESET=business-pilot go run ./cmd/gongmcp > /tmp/gongctl-postgres-business-pilot.jsonl

grep -q '"get_sync_status"' /tmp/gongctl-postgres-business-pilot.jsonl
grep -q '"summarize_call_facts"' /tmp/gongctl-postgres-business-pilot.jsonl
grep -q '"summarize_calls_by_lifecycle"' /tmp/gongctl-postgres-business-pilot.jsonl
grep -q '"rank_transcript_backlog"' /tmp/gongctl-postgres-business-pilot.jsonl
grep -q 'transcript_status' /tmp/gongctl-postgres-business-pilot.jsonl
assert_mcp_success /tmp/gongctl-postgres-business-pilot.jsonl 6 7 8 9

{
  printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_business_profile","arguments":{}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"list_business_concepts","arguments":{}}}'
} | GONG_DATABASE_URL="$READER_URL" GONGMCP_TOOL_ALLOWLIST=get_business_profile,list_business_concepts go run ./cmd/gongmcp > /tmp/gongctl-postgres-profile-mcp.jsonl

grep -q '"get_business_profile"' /tmp/gongctl-postgres-profile-mcp.jsonl
grep -q '"list_business_concepts"' /tmp/gongctl-postgres-profile-mcp.jsonl
grep -q 'Synthetic Postgres profile' /tmp/gongctl-postgres-profile-mcp.jsonl
assert_mcp_success /tmp/gongctl-postgres-profile-mcp.jsonl 3 4
if grep -q 'raw_yaml\|canonical_json' /tmp/gongctl-postgres-profile-mcp.jsonl; then
  echo "profile MCP smoke exposed raw profile storage fields" >&2
  exit 1
fi

GONGCTL_TEST_POSTGRES_URL="$WRITER_URL" go test -count=1 ./internal/store/postgres

echo "postgres smoke passed"
echo "sync output: /tmp/gongctl-postgres-sync.json"
echo "mcp output: /tmp/gongctl-postgres-mcp.jsonl"
echo "calls show output: /tmp/gongctl-postgres-calls-show.json"
echo "business-pilot output: /tmp/gongctl-postgres-business-pilot.jsonl"
echo "reader denial output: /tmp/gongctl-postgres-reader-write.txt"
echo "reader raw-read denial output: /tmp/gongctl-postgres-reader-raw-read.txt"
echo "reader sensitive-read denial output: /tmp/gongctl-postgres-reader-sensitive-read.txt"
echo "reader regrant output: /tmp/gongctl-postgres-reader-regrant.txt"
echo "normalized counts output: /tmp/gongctl-postgres-normalized-counts.txt"
echo "call facts output: /tmp/gongctl-postgres-call-facts.txt"
echo "read model state output: /tmp/gongctl-postgres-read-model-state.json"
echo "read model diagnostics output: /tmp/gongctl-postgres-read-model-diagnostics.txt"
echo "integrity gap output: /tmp/gongctl-postgres-integrity-gap.json"
echo "stale MCP denial output: /tmp/gongctl-postgres-stale-mcp.txt"
echo "read model rebuild output: /tmp/gongctl-postgres-read-model-rebuild.json"
echo "profile validate output: /tmp/gongctl-postgres-profile-validate.json"
echo "profile import output: /tmp/gongctl-postgres-profile-import.json"
echo "profile history output: /tmp/gongctl-postgres-profile-history.json"
echo "profile show output: /tmp/gongctl-postgres-profile-show.json"
echo "profile status output: /tmp/gongctl-postgres-status-with-profile.json"
echo "profile mcp output: /tmp/gongctl-postgres-profile-mcp.jsonl"
