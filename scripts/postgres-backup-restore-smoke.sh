#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE_FILE="$ROOT/docker-compose.postgres.yml"
DEFAULT_PROJECT="gongctl-postgres-restore-smoke"
PROJECT="${GONGCTL_POSTGRES_RESTORE_COMPOSE_PROJECT:-$DEFAULT_PROJECT}"
PORT="${GONGCTL_POSTGRES_PORT:-55433}"
RESTORE_DB="${GONGCTL_POSTGRES_RESTORE_DB:-gongctl_restore}"
export GONGCTL_POSTGRES_PORT="$PORT"
export GONGCTL_POSTGRES_USER="${GONGCTL_POSTGRES_USER:-gongctl}"
export GONGCTL_POSTGRES_PASSWORD="${GONGCTL_POSTGRES_PASSWORD:-gongctl_dev_password}"
export GONGMCP_READER_PASSWORD="${GONGMCP_READER_PASSWORD:-gongmcp_reader_dev_password}"

urlencode() {
  python3 -c 'from urllib.parse import quote; import sys; print(quote(sys.argv[1], safe=""))' "$1"
}

WRITER_URL="postgres://$(urlencode "$GONGCTL_POSTGRES_USER"):$(urlencode "$GONGCTL_POSTGRES_PASSWORD")@127.0.0.1:${PORT}/gongctl?sslmode=disable"
RESTORE_WRITER_URL="postgres://$(urlencode "$GONGCTL_POSTGRES_USER"):$(urlencode "$GONGCTL_POSTGRES_PASSWORD")@127.0.0.1:${PORT}/${RESTORE_DB}?sslmode=disable"
RESTORE_READER_URL="postgres://gongmcp_reader:$(urlencode "$GONGMCP_READER_PASSWORD")@127.0.0.1:${PORT}/${RESTORE_DB}?sslmode=disable"
DUMP_PATH="${GONGCTL_POSTGRES_RESTORE_DUMP_PATH:-/tmp/gongctl-postgres-backup-restore.dump}"

cd "$ROOT"

if [[ ! "$PROJECT" =~ ^gongctl-postgres-restore-smoke[-a-zA-Z0-9_]*$ ]]; then
  echo "refusing unsafe Compose project for destructive restore smoke cleanup: $PROJECT" >&2
  echo "use a project name that starts with ${DEFAULT_PROJECT}" >&2
  exit 1
fi

cleanup() {
  docker compose -p "$PROJECT" -f "$COMPOSE_FILE" down -v >/dev/null 2>&1 || true
}
trap cleanup EXIT

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

reader_restore_psql() {
  docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres sh -s -- "$RESTORE_DB" "$@" <<'SH'
set -eu
db="$1"
shift
export PGPASSWORD="${GONGMCP_READER_PASSWORD:?}"
exec psql -h 127.0.0.1 -U gongmcp_reader -d "$db" "$@"
SH
}

counts_sql() {
  cat <<'SQL'
SELECT 'call_facts', COUNT(*) FROM call_facts
UNION ALL SELECT 'calls', COUNT(*) FROM calls
UNION ALL SELECT 'crm_integrations', COUNT(*) FROM crm_integrations
UNION ALL SELECT 'crm_schema_fields', COUNT(*) FROM crm_schema_fields
UNION ALL SELECT 'scorecard_activity', COUNT(*) FROM scorecard_activity
UNION ALL SELECT 'transcript_segments', COUNT(*) FROM transcript_segments
UNION ALL SELECT 'transcripts', COUNT(*) FROM transcripts
ORDER BY 1
SQL
}

cleanup
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" up -d postgres

for _ in $(seq 1 90); do
  if docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U "$GONGCTL_POSTGRES_USER" -d gongctl -tAc "SELECT 1" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U "$GONGCTL_POSTGRES_USER" -d gongctl -v ON_ERROR_STOP=1 -tAc "SELECT 1" >/dev/null

GONG_DATABASE_URL="$WRITER_URL" go run ./cmd/gongctl sync synthetic >/tmp/gongctl-postgres-restore-source-sync.json
GONG_DATABASE_URL="$WRITER_URL" go run ./cmd/gongctl sync read-model --rebuild >/tmp/gongctl-postgres-restore-source-read-model.json
grep -q '"status": "rebuilt"' /tmp/gongctl-postgres-restore-source-read-model.json
grep -q '"ready": true' /tmp/gongctl-postgres-restore-source-read-model.json

counts_sql | docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U "$GONGCTL_POSTGRES_USER" -d gongctl -tA -F '|' >/tmp/gongctl-postgres-restore-source-counts.txt
grep -q 'calls|2' /tmp/gongctl-postgres-restore-source-counts.txt
grep -q 'transcript_segments|3' /tmp/gongctl-postgres-restore-source-counts.txt

rm -f "$DUMP_PATH"
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres pg_dump -U "$GONGCTL_POSTGRES_USER" -d gongctl --format=custom --no-owner --no-acl >"$DUMP_PATH"
test -s "$DUMP_PATH"
shasum -a 256 "$DUMP_PATH" >/tmp/gongctl-postgres-restore-dump-sha256.txt
stat -f 'dump_bytes|%z' "$DUMP_PATH" >/tmp/gongctl-postgres-restore-dump-size.txt 2>/dev/null || stat -c 'dump_bytes|%s' "$DUMP_PATH" >/tmp/gongctl-postgres-restore-dump-size.txt

docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres dropdb -U "$GONGCTL_POSTGRES_USER" --if-exists "$RESTORE_DB" >/dev/null
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres createdb -U "$GONGCTL_POSTGRES_USER" "$RESTORE_DB"
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres pg_restore -U "$GONGCTL_POSTGRES_USER" -d "$RESTORE_DB" --no-owner --no-acl <"$DUMP_PATH"

GONG_DATABASE_URL="$RESTORE_WRITER_URL" go run ./cmd/gongctl sync read-model --rebuild >/tmp/gongctl-postgres-restore-read-model.json
grep -q '"status": "rebuilt"' /tmp/gongctl-postgres-restore-read-model.json
grep -q '"ready": true' /tmp/gongctl-postgres-restore-read-model.json

counts_sql | docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U "$GONGCTL_POSTGRES_USER" -d "$RESTORE_DB" -tA -F '|' >/tmp/gongctl-postgres-restore-counts.txt
diff -u /tmp/gongctl-postgres-restore-source-counts.txt /tmp/gongctl-postgres-restore-counts.txt >/tmp/gongctl-postgres-restore-counts.diff

GONG_DATABASE_URL="$RESTORE_READER_URL" go run ./cmd/gongctl sync status >/tmp/gongctl-postgres-restore-status.json
grep -q '"total_calls": 2' /tmp/gongctl-postgres-restore-status.json
grep -q '"total_transcript_segments": 3' /tmp/gongctl-postgres-restore-status.json
grep -q '"postgres_read_model":' /tmp/gongctl-postgres-restore-status.json
grep -q '"ready": true' /tmp/gongctl-postgres-restore-status.json

{
  printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_sync_status","arguments":{}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"search_calls","arguments":{"limit":5,"transcript_status":"present"}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"search_transcript_segments","arguments":{"query":"implementation kickoff","limit":5}}}'
} | GONG_DATABASE_URL="$RESTORE_READER_URL" GONGMCP_TOOL_PRESET=operator-smoke go run ./cmd/gongmcp >/tmp/gongctl-postgres-restore-mcp.jsonl
assert_mcp_success /tmp/gongctl-postgres-restore-mcp.jsonl 3 4 5
grep -q '"search_calls"' /tmp/gongctl-postgres-restore-mcp.jsonl
grep -q '"search_transcript_segments"' /tmp/gongctl-postgres-restore-mcp.jsonl

if reader_restore_psql -c "CREATE TABLE reader_should_not_write(id int)" >/tmp/gongctl-postgres-restore-reader-write-denied.txt 2>&1; then
  echo "restored reader role unexpectedly wrote to restored database" >&2
  exit 1
fi
if reader_restore_psql -c "SELECT raw_json FROM calls LIMIT 1" >/tmp/gongctl-postgres-restore-reader-raw-read-denied.txt 2>&1; then
  echo "restored reader role unexpectedly read raw call JSON" >&2
  exit 1
fi

if grep -q 'postgres://\|gongctl_dev_password\|gongmcp_reader_dev_password\|127.0.0.1\|raw_json\|raw_sha256\|crmObjects' \
  /tmp/gongctl-postgres-restore-status.json \
  /tmp/gongctl-postgres-restore-mcp.jsonl \
  /tmp/gongctl-postgres-restore-source-counts.txt \
  /tmp/gongctl-postgres-restore-counts.txt \
  /tmp/gongctl-postgres-restore-dump-size.txt \
  /tmp/gongctl-postgres-restore-dump-sha256.txt; then
  echo "Postgres backup/restore smoke evidence exposed URL, secrets, host, or raw payload markers" >&2
  exit 1
fi

echo "postgres backup/restore smoke passed"
echo "source counts: /tmp/gongctl-postgres-restore-source-counts.txt"
echo "restore counts: /tmp/gongctl-postgres-restore-counts.txt"
echo "restore counts diff: /tmp/gongctl-postgres-restore-counts.diff"
echo "dump size: /tmp/gongctl-postgres-restore-dump-size.txt"
echo "dump sha256: /tmp/gongctl-postgres-restore-dump-sha256.txt"
echo "restore status: /tmp/gongctl-postgres-restore-status.json"
echo "restore mcp: /tmp/gongctl-postgres-restore-mcp.jsonl"
echo "restore reader write denial: /tmp/gongctl-postgres-restore-reader-write-denied.txt"
echo "restore reader raw-read denial: /tmp/gongctl-postgres-restore-reader-raw-read-denied.txt"
