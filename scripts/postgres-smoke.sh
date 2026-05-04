#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE_FILE="$ROOT/docker-compose.postgres.yml"
PROJECT="${GONGCTL_POSTGRES_COMPOSE_PROJECT:-gongctl-postgres-smoke}"
PORT="${GONGCTL_POSTGRES_PORT:-55432}"
export GONGCTL_POSTGRES_PASSWORD="${GONGCTL_POSTGRES_PASSWORD:-gongctl_dev_password}"
export GONGMCP_READER_PASSWORD="${GONGMCP_READER_PASSWORD:-gongmcp_reader_dev_password}"
WRITER_URL="postgres://gongctl:${GONGCTL_POSTGRES_PASSWORD}@127.0.0.1:${PORT}/gongctl?sslmode=disable"
READER_URL="postgres://gongmcp_reader:${GONGMCP_READER_PASSWORD}@127.0.0.1:${PORT}/gongctl?sslmode=disable"
READER_CONTAINER_URL="postgres://gongmcp_reader:${GONGMCP_READER_PASSWORD}@127.0.0.1:5432/gongctl?sslmode=disable"

cd "$ROOT"

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

docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -tA -F '|' -c "SELECT 'call_context_objects', COUNT(*) FROM call_context_objects UNION ALL SELECT 'call_context_fields', COUNT(*) FROM call_context_fields UNION ALL SELECT 'call_facts', COUNT(*) FROM call_facts ORDER BY 1" >/tmp/gongctl-postgres-normalized-counts.txt
grep -q 'call_facts|2' /tmp/gongctl-postgres-normalized-counts.txt
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -tA -F '|' -c "SELECT call_id, transcript_present, transcript_status, lifecycle_bucket FROM call_facts ORDER BY call_id" >/tmp/gongctl-postgres-call-facts.txt
grep -q 'synthetic-call-001|t|present|' /tmp/gongctl-postgres-call-facts.txt

docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -c "GRANT SELECT ON ALL TABLES IN SCHEMA public TO gongmcp_reader" >/dev/null

if docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql "$READER_CONTAINER_URL" -c "INSERT INTO users(user_id, raw_json, raw_sha256, first_seen_at, updated_at) VALUES('should-fail', '{}'::jsonb, 'x', now()::text, now()::text)" >/tmp/gongctl-postgres-reader-write.txt 2>&1; then
  echo "reader role unexpectedly wrote to Postgres" >&2
  exit 1
fi

{
  printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_sync_status","arguments":{}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"search_calls","arguments":{"limit":5}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"search_transcript_segments","arguments":{"query":"shared Postgres","limit":5}}}'
} | GONG_DATABASE_URL="$READER_URL" GONGMCP_TOOL_ALLOWLIST=get_sync_status,search_calls,search_transcript_segments go run ./cmd/gongmcp > /tmp/gongctl-postgres-mcp.jsonl

grep -q '"get_sync_status"' /tmp/gongctl-postgres-mcp.jsonl
grep -q '"search_calls"' /tmp/gongctl-postgres-mcp.jsonl
grep -q '"search_transcript_segments"' /tmp/gongctl-postgres-mcp.jsonl
grep -q 'synthetic-call-001' /tmp/gongctl-postgres-mcp.jsonl
grep -q 'shared.*Postgres' /tmp/gongctl-postgres-mcp.jsonl

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
grep -q '"id":6,"result"' /tmp/gongctl-postgres-business-pilot.jsonl
grep -q '"id":7,"result"' /tmp/gongctl-postgres-business-pilot.jsonl
grep -q '"id":8,"result"' /tmp/gongctl-postgres-business-pilot.jsonl
grep -q '"id":9,"result"' /tmp/gongctl-postgres-business-pilot.jsonl
if grep -q '"id":[6789],"error"' /tmp/gongctl-postgres-business-pilot.jsonl; then
  echo "business-pilot MCP call returned an error" >&2
  exit 1
fi

GONGCTL_TEST_POSTGRES_URL="$WRITER_URL" go test -count=1 ./internal/store/postgres

echo "postgres smoke passed"
echo "sync output: /tmp/gongctl-postgres-sync.json"
echo "mcp output: /tmp/gongctl-postgres-mcp.jsonl"
echo "business-pilot output: /tmp/gongctl-postgres-business-pilot.jsonl"
echo "reader denial output: /tmp/gongctl-postgres-reader-write.txt"
echo "normalized counts output: /tmp/gongctl-postgres-normalized-counts.txt"
echo "call facts output: /tmp/gongctl-postgres-call-facts.txt"
