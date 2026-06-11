#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE_FILE="$ROOT/docker-compose.postgres.yml"
PROJECT="${GONGCTL_POSTGRES_COMPOSE_PROJECT:-gongctl-postgres-smoke}"
PORT="${GONGCTL_POSTGRES_PORT:-55432}"
export GONGCTL_POSTGRES_USER="${GONGCTL_POSTGRES_USER:-gongctl}"
export GONGCTL_POSTGRES_PASSWORD="${GONGCTL_POSTGRES_PASSWORD:-gongctl_dev_password}"
export GONGMCP_READER_PASSWORD="${GONGMCP_READER_PASSWORD:-gongmcp_reader_dev_password}"
export GONGMCP_BUSINESS_PILOT_READER_PASSWORD="${GONGMCP_BUSINESS_PILOT_READER_PASSWORD:-gongmcp_business_pilot_reader_dev_password}"
export GONGMCP_ANALYST_READER_PASSWORD="${GONGMCP_ANALYST_READER_PASSWORD:-gongmcp_analyst_reader_dev_password}"
export GONGMCP_FUNCTION_FREE_READER_PASSWORD="${GONGMCP_FUNCTION_FREE_READER_PASSWORD:-gongmcp_function_free_reader_dev_password}"

urlencode() {
  python3 -c 'from urllib.parse import quote; import sys; print(quote(sys.argv[1], safe=""))' "$1"
}

WRITER_URL="postgres://$(urlencode "$GONGCTL_POSTGRES_USER"):$(urlencode "$GONGCTL_POSTGRES_PASSWORD")@127.0.0.1:${PORT}/gongctl?sslmode=disable"
READER_URL="postgres://gongmcp_reader:$(urlencode "$GONGMCP_READER_PASSWORD")@127.0.0.1:${PORT}/gongctl?sslmode=disable"
BUSINESS_PILOT_READER_URL="postgres://gongmcp_business_pilot_reader:$(urlencode "$GONGMCP_BUSINESS_PILOT_READER_PASSWORD")@127.0.0.1:${PORT}/gongctl?sslmode=disable"
ANALYST_READER_URL="postgres://gongmcp_analyst_reader:$(urlencode "$GONGMCP_ANALYST_READER_PASSWORD")@127.0.0.1:${PORT}/gongctl?sslmode=disable"
FUNCTION_FREE_READER_URL="postgres://gongmcp_function_free_reader:$(urlencode "$GONGMCP_FUNCTION_FREE_READER_PASSWORD")@127.0.0.1:${PORT}/gongctl?sslmode=disable"

cd "$ROOT"

reader_psql() {
  docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres sh -s -- "$@" <<'SH'
set -eu
export PGPASSWORD="${GONGMCP_READER_PASSWORD:?}"
exec psql -h 127.0.0.1 -U gongmcp_reader -d gongctl "$@"
SH
}

business_pilot_reader_psql() {
  docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T -e GONGMCP_BUSINESS_PILOT_READER_PASSWORD="$GONGMCP_BUSINESS_PILOT_READER_PASSWORD" postgres sh -s -- "$@" <<'SH'
set -eu
export PGPASSWORD="${GONGMCP_BUSINESS_PILOT_READER_PASSWORD:?}"
exec psql -h 127.0.0.1 -U gongmcp_business_pilot_reader -d gongctl "$@"
SH
}

analyst_reader_psql() {
  docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T -e GONGMCP_ANALYST_READER_PASSWORD="$GONGMCP_ANALYST_READER_PASSWORD" postgres sh -s -- "$@" <<'SH'
set -eu
export PGPASSWORD="${GONGMCP_ANALYST_READER_PASSWORD:?}"
exec psql -h 127.0.0.1 -U gongmcp_analyst_reader -d gongctl "$@"
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

cleanup
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" up -d postgres
# The official Postgres image briefly accepts local connections during
# initdb before restarting into the long-running server. Avoid racing that
# transient post-init shutdown on fresh volumes.
sleep 2

for _ in $(seq 1 90); do
  if docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -tAc "SELECT 1" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 -tAc "SELECT 1" >/dev/null

for _ in $(seq 1 90); do
  if python3 - "$PORT" >/dev/null 2>&1 <<'PY'
import socket
import sys

with socket.create_connection(("127.0.0.1", int(sys.argv[1])), timeout=1):
    pass
PY
  then
    break
  fi
  sleep 1
done
python3 - "$PORT" >/dev/null <<'PY'
import socket
import sys

with socket.create_connection(("127.0.0.1", int(sys.argv[1])), timeout=1):
    pass
PY

GONG_DATABASE_URL="$WRITER_URL" go run ./cmd/gongctl sync synthetic >/tmp/gongctl-postgres-sync.json
GONG_DATABASE_URL="$WRITER_URL" go run ./cmd/gongctl sync read-model >/tmp/gongctl-postgres-read-model-state.json
grep -q '"status": "current"' /tmp/gongctl-postgres-read-model-state.json
grep -q '"call_count": 2' /tmp/gongctl-postgres-read-model-state.json
grep -q '"fact_count": 2' /tmp/gongctl-postgres-read-model-state.json
grep -q '"ready": true' /tmp/gongctl-postgres-read-model-state.json
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -tAc "SELECT COUNT(*) FROM scorecard_activity" >/tmp/gongctl-postgres-scorecard-activity-count.txt
grep -q '^2$' /tmp/gongctl-postgres-scorecard-activity-count.txt
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -tAc "SELECT COUNT(*) FROM crm_integrations" >/tmp/gongctl-postgres-crm-integrations-count.txt
grep -q '^1$' /tmp/gongctl-postgres-crm-integrations-count.txt
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -tAc "SELECT COUNT(*) FROM crm_schema_objects" >/tmp/gongctl-postgres-crm-schema-objects-count.txt
grep -q '^2$' /tmp/gongctl-postgres-crm-schema-objects-count.txt
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -tAc "SELECT COUNT(*) FROM crm_schema_fields" >/tmp/gongctl-postgres-crm-schema-fields-count.txt
grep -q '^5$' /tmp/gongctl-postgres-crm-schema-fields-count.txt
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 -c "DELETE FROM calls WHERE call_id = 'synthetic-profile-call-001'; INSERT INTO calls(call_id, title, started_at, duration_seconds, parties_count, context_present, raw_json, raw_sha256, first_seen_at, updated_at) VALUES('synthetic-profile-call-001', 'Profile lifecycle proposal review', '2026-02-14T15:00:00Z', 1800, 2, true, '{\"id\":\"synthetic-profile-call-001\",\"title\":\"Profile lifecycle proposal review\",\"started\":\"2026-02-14T15:00:00Z\",\"duration\":1800,\"metaData\":{\"scope\":\"External\",\"system\":\"Zoom\",\"direction\":\"Conference\"},\"context\":{\"crmObjects\":[{\"type\":\"Opportunity\",\"id\":\"opp-profile-001\",\"name\":\"Profile Opportunity\",\"fields\":{\"StageName\":\"Proposal\",\"Type\":\"New Business\"}},{\"type\":\"Account\",\"id\":\"acct-profile-001\",\"name\":\"Profile Account\",\"fields\":{\"Account_Type__c\":\"Prospect\",\"Industry\":\"Manufacturing\"}}]}}'::jsonb, 'synthetic-profile-sha', now()::text, now()::text)" >/dev/null
GONG_DATABASE_URL="$WRITER_URL" go run ./cmd/gongctl sync read-model --rebuild >/tmp/gongctl-postgres-profile-fixture-read-model.json
grep -q '"call_count": 3' /tmp/gongctl-postgres-profile-fixture-read-model.json
grep -q '"fact_count": 3' /tmp/gongctl-postgres-profile-fixture-read-model.json
cat >/tmp/gongctl-postgres-profile.yaml <<'YAML'
version: 1
name: Synthetic Postgres profile
objects:
  deal:
    object_types: [Opportunity]
  account:
    object_types: [Account]
fields:
  deal_stage:
    object: deal
    names: [StageName]
  account_type:
    object: account
    names: [Account_Type__c]
lifecycle:
  open:
    order: 10
    rules:
      - field: deal_stage
        op: equals
        value: Proposal
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
grep -q '"cache_status": "fresh"' /tmp/gongctl-postgres-status-with-profile.json
GONG_DATABASE_URL="$READER_URL" go run ./cmd/gongctl calls show --call-id synthetic-call-001 --json >/tmp/gongctl-postgres-calls-show.json
grep -q '"call_id": "synthetic-call-001"' /tmp/gongctl-postgres-calls-show.json
grep -q '"title": "Pulsaris implementation kickoff"' /tmp/gongctl-postgres-calls-show.json
if grep -q 'raw_json\|crmObjects\|speaker-1' /tmp/gongctl-postgres-calls-show.json; then
  echo "postgres calls show exposed raw call payload fields" >&2
  exit 1
fi

docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -tA -F '|' -c "SELECT 'call_context_objects', COUNT(*) FROM call_context_objects UNION ALL SELECT 'call_context_fields', COUNT(*) FROM call_context_fields UNION ALL SELECT 'call_facts', COUNT(*) FROM call_facts ORDER BY 1" >/tmp/gongctl-postgres-normalized-counts.txt
grep -q 'call_facts|3' /tmp/gongctl-postgres-normalized-counts.txt
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -tA -F '|' -c "SELECT call_id, transcript_present, transcript_status, lifecycle_bucket FROM call_facts ORDER BY call_id" >/tmp/gongctl-postgres-call-facts.txt
grep -q 'synthetic-call-001|t|present|' /tmp/gongctl-postgres-call-facts.txt
grep -q 'synthetic-profile-call-001|f|missing|' /tmp/gongctl-postgres-call-facts.txt
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -tA -F '|' -c "SELECT call_id, object_count, field_count, object_limit_exceeded, field_limit_exceeded, last_error FROM call_read_model_diagnostics ORDER BY call_id" >/tmp/gongctl-postgres-read-model-diagnostics.txt
grep -q 'synthetic-call-001|0|0|f|f|' /tmp/gongctl-postgres-read-model-diagnostics.txt
grep -q 'synthetic-call-002|0|0|f|f|' /tmp/gongctl-postgres-read-model-diagnostics.txt
grep -q 'synthetic-profile-call-001|2|4|f|f|' /tmp/gongctl-postgres-read-model-diagnostics.txt

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
GONG_DATABASE_URL="$READER_URL" go run ./cmd/gongctl analyze crm-schema --integration-id synthetic-crm-integration-001 --object-type Opportunity >/tmp/gongctl-postgres-crm-schema.json
GONG_DATABASE_URL="$READER_URL" go run ./cmd/gongctl analyze settings --kind scorecards >/tmp/gongctl-postgres-scorecard-settings.json
GONG_DATABASE_URL="$READER_URL" go run ./cmd/gongctl analyze scorecards >/tmp/gongctl-postgres-scorecards.json
GONG_DATABASE_URL="$READER_URL" go run ./cmd/gongctl analyze scorecard --scorecard-id synthetic-scorecard-001 >/tmp/gongctl-postgres-scorecard-detail.json
GONG_DATABASE_URL="$READER_URL" go run ./cmd/gongctl analyze scorecard-activity --group-by review_method >/tmp/gongctl-postgres-scorecard-activity-review-method.json
GONG_DATABASE_URL="$READER_URL" go run ./cmd/gongctl analyze scorecard-activity --group-by transcript_status >/tmp/gongctl-postgres-scorecard-activity-transcript-status.json
if GONG_DATABASE_URL="$READER_URL" go run ./cmd/gongctl analyze scorecard-activity --group-by reviewed_user >/tmp/gongctl-postgres-scorecard-activity-reviewed-user-denied.txt 2>&1; then
  echo "postgres read-only scorecard activity unexpectedly exposed reviewed_user grouping" >&2
  exit 1
fi
grep -q '"integration_id": "synthetic-crm-integration-001"' /tmp/gongctl-postgres-crm-schema.json
grep -q '"object_type": "Opportunity"' /tmp/gongctl-postgres-crm-schema.json
grep -q '"field_name": "StageName"' /tmp/gongctl-postgres-crm-schema.json
grep -q '"object_id": "synthetic-generic-setting-id-001"' /tmp/gongctl-postgres-scorecard-settings.json
grep -q '"name": "Synthetic discovery quality"' /tmp/gongctl-postgres-scorecards.json
grep -q '"question_text": "Did the seller confirm the implementation timeline?"' /tmp/gongctl-postgres-scorecard-detail.json
grep -q '"group_value": "MANUAL"' /tmp/gongctl-postgres-scorecard-activity-review-method.json
grep -q '"group_value": "AUTOMATIC"' /tmp/gongctl-postgres-scorecard-activity-review-method.json
grep -q '"group_value": "present"' /tmp/gongctl-postgres-scorecard-activity-transcript-status.json
grep -q 'reviewed_user is not supported' /tmp/gongctl-postgres-scorecard-activity-reviewed-user-denied.txt
grep -q '"min_range": 1' /tmp/gongctl-postgres-scorecard-detail.json
if grep -q '"max_range"' /tmp/gongctl-postgres-scorecard-detail.json; then
  echo "scorecard detail did not tolerate nonnumeric maxRange" >&2
  exit 1
fi
if grep -q 'raw_json\|raw_sha256\|raw_payload\|synthetic-answered-scorecard\|synthetic-user-' /tmp/gongctl-postgres-scorecard-settings.json /tmp/gongctl-postgres-scorecards.json /tmp/gongctl-postgres-scorecard-detail.json /tmp/gongctl-postgres-scorecard-activity-review-method.json /tmp/gongctl-postgres-scorecard-activity-transcript-status.json; then
	echo "scorecard inventory/activity output exposed raw payload fields or identifiers" >&2
	exit 1
fi
if grep -q 'raw_json\|raw_sha256\|raw_payload\|Proposal\|Manufacturing\|Profile Account' /tmp/gongctl-postgres-crm-schema.json; then
  echo "CRM schema inventory output exposed raw payload fields or CRM values" >&2
  exit 1
fi

GONG_DATABASE_URL="$READER_URL" go run ./cmd/gongctl cache inventory >/tmp/gongctl-postgres-cache-inventory.json
grep -q '"backend": "postgres"' /tmp/gongctl-postgres-cache-inventory.json
grep -q '"db_path_policy": "database_url_not_exported"' /tmp/gongctl-postgres-cache-inventory.json
grep -q '"reader_privilege_status": "valid_reader"' /tmp/gongctl-postgres-cache-inventory.json
grep -q '"read_model_status": "current"' /tmp/gongctl-postgres-cache-inventory.json
grep -q '"total_crm_schema_fields": 5' /tmp/gongctl-postgres-cache-inventory.json
grep -q '"table": "crm_schema_fields"' /tmp/gongctl-postgres-cache-inventory.json
if grep -q 'postgres://\|gongctl_dev_password\|gongmcp_reader_dev_password\|127.0.0.1\|raw_json\|raw_sha256\|raw_payload' /tmp/gongctl-postgres-cache-inventory.json; then
  echo "Postgres cache inventory exposed DB URL, secrets, host, or raw storage fields" >&2
  exit 1
fi
if grep -q 'Synthetic Postgres profile\|canonical_sha256\|unavailable_concepts' /tmp/gongctl-postgres-cache-inventory.json; then
  echo "Postgres cache inventory exposed profile identifiers or detailed concepts" >&2
  exit 1
fi

GONG_DATABASE_URL="$WRITER_URL" go run ./cmd/gongctl cache inventory >/tmp/gongctl-postgres-cache-inventory-writer-url.json
grep -q '"backend": "postgres"' /tmp/gongctl-postgres-cache-inventory-writer-url.json
grep -q '"reader_privilege_status": "not_valid_reader"' /tmp/gongctl-postgres-cache-inventory-writer-url.json
if grep -q 'postgres://\|gongctl_dev_password\|gongmcp_reader_dev_password\|127.0.0.1\|Synthetic Postgres profile\|canonical_sha256\|unavailable_concepts' /tmp/gongctl-postgres-cache-inventory-writer-url.json; then
  echo "Postgres cache inventory with writer URL exposed URL, secrets, host, or profile identifiers" >&2
  exit 1
fi

rm -rf /tmp/gongctl-postgres-support-bundle
GONG_DATABASE_URL="$READER_URL" go run ./cmd/gongctl support bundle --out /tmp/gongctl-postgres-support-bundle >/tmp/gongctl-postgres-support-bundle.json
grep -q '"path_policy": "local_path_not_exported"' /tmp/gongctl-postgres-support-bundle.json
grep -q '"contains_raw_customer_data": false' /tmp/gongctl-postgres-support-bundle.json
grep -q '"postgres-deployment.json"' /tmp/gongctl-postgres-support-bundle.json
grep -q '"backend": "postgres"' /tmp/gongctl-postgres-support-bundle/manifest.json
grep -q '"path_policy": "database_url_not_exported"' /tmp/gongctl-postgres-support-bundle/manifest.json
grep -q '"reader_privilege_status": "valid_reader"' /tmp/gongctl-postgres-support-bundle/diagnostics.json
grep -q '"read_model_status": "current"' /tmp/gongctl-postgres-support-bundle/diagnostics.json
grep -q '"schema_version":' /tmp/gongctl-postgres-support-bundle/diagnostics.json
grep -q '"total_crm_schema_fields": 5' /tmp/gongctl-postgres-support-bundle/cache-summary.json
grep -q '"table": "crm_schema_fields"' /tmp/gongctl-postgres-support-bundle/cache-summary.json
grep -q '"refresh_progress"' /tmp/gongctl-postgres-support-bundle/postgres-deployment.json
grep -q '"status": "not_available"' /tmp/gongctl-postgres-support-bundle/postgres-deployment.json
grep -q '"statement_timeout"' /tmp/gongctl-postgres-support-bundle/postgres-deployment.json
grep -q '"scoped_reader_grant_sql"' /tmp/gongctl-postgres-support-bundle/postgres-deployment.json
grep -q '"serving_refresh_marker"' /tmp/gongctl-postgres-support-bundle/postgres-deployment.json
if grep -R -q 'postgres://\|gongctl_dev_password\|gongmcp_reader_dev_password\|127.0.0.1\|/tmp/gongctl-postgres-support-bundle\|synthetic-call-\|speaker-1\|crmObjects\|raw_json\|raw_sha256\|raw_payload\|Proposal\|Manufacturing\|Profile Account\|Synthetic Postgres profile\|canonical_sha256\|unavailable_concepts' /tmp/gongctl-postgres-support-bundle /tmp/gongctl-postgres-support-bundle.json; then
  echo "Postgres support bundle exposed DB URL, paths, secrets, raw fields, or customer-like fixture values" >&2
  exit 1
fi

docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 <<'SQL' >/tmp/gongctl-postgres-purge-fixture.txt
DELETE FROM transcript_segments WHERE call_id = 'synthetic-retention-old';
DELETE FROM transcripts WHERE call_id = 'synthetic-retention-old';
DELETE FROM calls WHERE call_id = 'synthetic-retention-old';
DELETE FROM scorecard_activity WHERE answered_scorecard_id = 'synthetic-retention-scorecard';
INSERT INTO calls(call_id, title, started_at, duration_seconds, parties_count, context_present, raw_json, raw_sha256, first_seen_at, updated_at)
VALUES('synthetic-retention-old', 'Retention cleanup candidate', '2025-12-15T15:00:00Z', 900, 2, true, '{"id":"synthetic-retention-old","title":"Retention cleanup candidate","started":"2025-12-15T15:00:00Z","duration":900,"context":{"crmObjects":[{"type":"Opportunity","id":"opp-retention-old","name":"Retention Opportunity","fields":{"StageName":"Proposal"}}]}}'::jsonb, 'retention-old-sha', now()::text, now()::text);
INSERT INTO transcripts(call_id, raw_json, raw_sha256, segment_count, first_seen_at, updated_at)
VALUES('synthetic-retention-old', '{"callId":"synthetic-retention-old"}'::jsonb, 'retention-old-transcript-sha', 1, now()::text, now()::text);
INSERT INTO transcript_segments(call_id, segment_index, speaker_id, start_ms, end_ms, text, raw_json)
VALUES('synthetic-retention-old', 0, 'speaker-retention-old', 0, 1000, 'retentionpurgeunique transcript text should be removed.', '{"speakerId":"speaker-retention-old"}'::jsonb);
INSERT INTO scorecard_activity(answered_scorecard_id, scorecard_id, scorecard_name, call_id, call_started_at, review_method, review_time, overall_score, average_score, answer_count, raw_json, raw_sha256, first_seen_at, updated_at)
VALUES('synthetic-retention-scorecard', 'synthetic-scorecard-001', 'Synthetic discovery quality', 'synthetic-retention-old', '2025-12-15T15:00:00Z', 'MANUAL', '2025-12-16T15:00:00Z', 4, 4, 1, '{"answeredScorecardId":"synthetic-retention-scorecard"}'::jsonb, 'retention-scorecard-sha', now()::text, now()::text);
SQL
GONG_DATABASE_URL="$WRITER_URL" go run ./cmd/gongctl sync read-model --rebuild >/tmp/gongctl-postgres-purge-fixture-read-model.json
grep -q '"status": "rebuilt"' /tmp/gongctl-postgres-purge-fixture-read-model.json
GONG_DATABASE_URL="$READER_URL" go run ./cmd/gongctl cache purge --older-than 2026-01-01 >/tmp/gongctl-postgres-purge-dry-run.json
grep -q '"backend": "postgres"' /tmp/gongctl-postgres-purge-dry-run.json
grep -q '"dry_run": true' /tmp/gongctl-postgres-purge-dry-run.json
grep -q '"executed": false' /tmp/gongctl-postgres-purge-dry-run.json
grep -q '"call_count": 1' /tmp/gongctl-postgres-purge-dry-run.json
grep -q '"transcript_count": 1' /tmp/gongctl-postgres-purge-dry-run.json
grep -q '"transcript_segment_count": 1' /tmp/gongctl-postgres-purge-dry-run.json
grep -q '"call_fact_count": 1' /tmp/gongctl-postgres-purge-dry-run.json
grep -q '"scorecard_activity_count": 1' /tmp/gongctl-postgres-purge-dry-run.json
if grep -q 'synthetic-retention-old\|retentionpurgeunique\|postgres://\|gongmcp_reader_dev_password\|gongctl_dev_password' /tmp/gongctl-postgres-purge-dry-run.json; then
  echo "Postgres purge dry-run exposed raw identifiers, transcript text, or secrets" >&2
  exit 1
fi

rm -f /tmp/gongctl-postgres-retention-policy-does-not-exist.yaml
if GONG_DATABASE_URL="$WRITER_URL" go run ./cmd/gongctl cache purge --config /tmp/gongctl-postgres-retention-policy-does-not-exist.yaml --confirm >/tmp/gongctl-postgres-purge-policy-missing-config.txt 2>&1; then
  echo "Postgres purge unexpectedly allowed missing retention-policy config" >&2
  exit 1
fi
grep -q 'read retention policy config: unavailable' /tmp/gongctl-postgres-purge-policy-missing-config.txt
if grep -q '/tmp/gongctl-postgres-retention-policy-does-not-exist.yaml\|postgres://\|gongmcp_reader_dev_password\|gongctl_dev_password' /tmp/gongctl-postgres-purge-policy-missing-config.txt; then
  echo "Postgres policy purge missing-config error exposed local paths or secrets" >&2
  exit 1
fi

cat >/tmp/gongctl-postgres-retention-policy-missing-approval.yaml <<'YAML'
version: 1
older_than: 2026-01-01
approval:
  reference: CHANGE-RETENTION-MISSING
YAML
if GONG_DATABASE_URL="$WRITER_URL" go run ./cmd/gongctl cache purge --config /tmp/gongctl-postgres-retention-policy-missing-approval.yaml --confirm >/tmp/gongctl-postgres-purge-policy-missing-approval.txt 2>&1; then
  echo "Postgres purge unexpectedly allowed incomplete retention-policy approval" >&2
  exit 1
fi
grep -q 'approval is incomplete' /tmp/gongctl-postgres-purge-policy-missing-approval.txt

cat >/tmp/gongctl-postgres-retention-policy.yaml <<'YAML'
version: 1
older_than: 2026-01-01
approval:
  reference: CHANGE-RETENTION-SYNTHETIC
  approved_by: revops-retention-reviewer
  approved_at: 2024-01-01
  data_owner: revenue-operations
  backup_reference: backup-20240101-synthetic
  legal_hold_reviewed: true
YAML
GONG_DATABASE_URL="$READER_URL" go run ./cmd/gongctl cache purge --config /tmp/gongctl-postgres-retention-policy.yaml --dry-run >/tmp/gongctl-postgres-purge-policy-dry-run.json
grep -q '"backend": "postgres"' /tmp/gongctl-postgres-purge-policy-dry-run.json
grep -q '"dry_run": true' /tmp/gongctl-postgres-purge-policy-dry-run.json
grep -q '"configured": true' /tmp/gongctl-postgres-purge-policy-dry-run.json
grep -q '"approval_complete": true' /tmp/gongctl-postgres-purge-policy-dry-run.json
grep -q '"backup_reference": "backup-20240101-synthetic"' /tmp/gongctl-postgres-purge-policy-dry-run.json
grep -q '"call_count": 1' /tmp/gongctl-postgres-purge-policy-dry-run.json
if grep -q 'synthetic-retention-old\|retentionpurgeunique\|postgres://\|gongmcp_reader_dev_password\|gongctl_dev_password\|/tmp/gongctl-postgres-retention-policy' /tmp/gongctl-postgres-purge-policy-dry-run.json; then
  echo "Postgres policy purge dry-run exposed raw identifiers, transcript text, secrets, or local policy paths" >&2
  exit 1
fi
cat >/tmp/gongctl-postgres-retention-policy-unsafe-metadata.yaml <<'YAML'
version: 1
older_than: 2026-01-01
approval:
  reference: https://changes.example.invalid/CHANGE-RETENTION-SYNTHETIC
  approved_by: reviewer@example.invalid
  approved_at: 2024-01-01
  data_owner: revenue-operations
  backup_reference: /srv/backups/customer-retention.dump
  legal_hold_reviewed: true
YAML
GONG_DATABASE_URL="$READER_URL" go run ./cmd/gongctl cache purge --config /tmp/gongctl-postgres-retention-policy-unsafe-metadata.yaml --dry-run >/tmp/gongctl-postgres-purge-policy-redacted-metadata.json
grep -q '"approval_complete": true' /tmp/gongctl-postgres-purge-policy-redacted-metadata.json
grep -q 'redacted:' /tmp/gongctl-postgres-purge-policy-redacted-metadata.json
if grep -q 'https://changes.example.invalid\|reviewer@example.invalid\|/srv/backups/customer-retention.dump\|postgres://\|gongmcp_reader_dev_password\|gongctl_dev_password\|/tmp/gongctl-postgres-retention-policy' /tmp/gongctl-postgres-purge-policy-redacted-metadata.json; then
  echo "Postgres policy purge dry-run exposed unsafe approval metadata, secrets, or local policy paths" >&2
  exit 1
fi
if GONG_DATABASE_URL="$WRITER_URL" go run ./cmd/gongctl cache purge --config /tmp/gongctl-postgres-retention-policy.yaml --dry-run >/tmp/gongctl-postgres-purge-writer-policy-dry-run-denied.txt 2>&1; then
  echo "Postgres policy purge unexpectedly allowed writer URL dry-run" >&2
  exit 1
fi
if grep -q 'postgres://\|gongmcp_reader_dev_password\|gongctl_dev_password\|/tmp/gongctl-postgres-retention-policy' /tmp/gongctl-postgres-purge-writer-policy-dry-run-denied.txt; then
  echo "Postgres policy purge writer dry-run denial exposed secrets or local policy paths" >&2
  exit 1
fi
if GONG_DATABASE_URL="$READER_URL" go run ./cmd/gongctl cache purge --config /tmp/gongctl-postgres-retention-policy.yaml --confirm >/tmp/gongctl-postgres-purge-reader-confirm-denied.txt 2>&1; then
  echo "Postgres purge unexpectedly allowed reader URL confirmation" >&2
  exit 1
fi
GONG_DATABASE_URL="$WRITER_URL" go run ./cmd/gongctl cache purge --config /tmp/gongctl-postgres-retention-policy.yaml --confirm >/tmp/gongctl-postgres-purge-confirm.json
grep -q '"backend": "postgres"' /tmp/gongctl-postgres-purge-confirm.json
grep -q '"executed": true' /tmp/gongctl-postgres-purge-confirm.json
grep -q '"configured": true' /tmp/gongctl-postgres-purge-confirm.json
grep -q '"approval_complete": true' /tmp/gongctl-postgres-purge-confirm.json
grep -q '"call_count": 1' /tmp/gongctl-postgres-purge-confirm.json
if grep -q 'synthetic-retention-old\|retentionpurgeunique\|postgres://\|gongctl_dev_password\|/tmp/gongctl-postgres-retention-policy' /tmp/gongctl-postgres-purge-confirm.json; then
  echo "Postgres policy purge confirm exposed raw identifiers, transcript text, secrets, or local policy paths" >&2
  exit 1
fi
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -tA -F '|' -c "SELECT 'calls', COUNT(*) FROM calls WHERE call_id = 'synthetic-retention-old' UNION ALL SELECT 'transcript_segments', COUNT(*) FROM transcript_segments WHERE call_id = 'synthetic-retention-old' UNION ALL SELECT 'call_facts', COUNT(*) FROM call_facts WHERE call_id = 'synthetic-retention-old' UNION ALL SELECT 'profile_call_fact_cache', COUNT(*) FROM profile_call_fact_cache WHERE call_id = 'synthetic-retention-old' UNION ALL SELECT 'scorecard_activity', COUNT(*) FROM scorecard_activity WHERE call_id = 'synthetic-retention-old' ORDER BY 1" >/tmp/gongctl-postgres-purge-post-counts.txt
if grep -v '|0$' /tmp/gongctl-postgres-purge-post-counts.txt >/dev/null; then
  echo "Postgres purge left old call-dependent rows" >&2
  cat /tmp/gongctl-postgres-purge-post-counts.txt >&2
  exit 1
fi
GONG_DATABASE_URL="$READER_URL" go run ./cmd/gongctl search transcripts --query retentionpurgeunique --limit 5 >/tmp/gongctl-postgres-purge-search-after.json
if grep -q 'synthetic-retention-old' /tmp/gongctl-postgres-purge-search-after.json; then
  echo "Postgres purge left searchable transcript evidence" >&2
  exit 1
fi

if reader_psql -c "SELECT raw_json FROM calls LIMIT 1" >/tmp/gongctl-postgres-reader-raw-read.txt 2>&1; then
	echo "reader role unexpectedly read raw call JSON" >&2
  exit 1
fi
if reader_psql -c "SELECT raw_json FROM gong_settings LIMIT 1" >/tmp/gongctl-postgres-reader-settings-raw-read.txt 2>&1; then
  echo "reader role unexpectedly read raw Gong settings JSON" >&2
  exit 1
fi
if reader_psql -c "SELECT raw_sha256 FROM gong_settings LIMIT 1" >>/tmp/gongctl-postgres-reader-settings-raw-read.txt 2>&1; then
  echo "reader role unexpectedly read raw Gong settings hashes" >&2
  exit 1
fi
if reader_psql -c "SELECT raw_json FROM scorecard_activity LIMIT 1" >/tmp/gongctl-postgres-reader-scorecard-activity-raw-read.txt 2>&1; then
  echo "reader role unexpectedly read raw scorecard activity JSON" >&2
  exit 1
fi
if reader_psql -c "SELECT raw_sha256 FROM scorecard_activity LIMIT 1" >>/tmp/gongctl-postgres-reader-scorecard-activity-raw-read.txt 2>&1; then
  echo "reader role unexpectedly read raw scorecard activity hashes" >&2
  exit 1
fi
if reader_psql -c "SELECT raw_json FROM crm_integrations LIMIT 1" >/tmp/gongctl-postgres-reader-crm-inventory-raw-read.txt 2>&1; then
  echo "reader role unexpectedly read raw CRM integration JSON" >&2
  exit 1
fi
if reader_psql -c "SELECT raw_sha256 FROM crm_integrations LIMIT 1" >>/tmp/gongctl-postgres-reader-crm-inventory-raw-read.txt 2>&1; then
  echo "reader role unexpectedly read raw CRM integration hashes" >&2
  exit 1
fi
if reader_psql -c "SELECT raw_json FROM crm_schema_objects LIMIT 1" >>/tmp/gongctl-postgres-reader-crm-inventory-raw-read.txt 2>&1; then
  echo "reader role unexpectedly read raw CRM schema object JSON" >&2
  exit 1
fi
if reader_psql -c "SELECT raw_sha256 FROM crm_schema_objects LIMIT 1" >>/tmp/gongctl-postgres-reader-crm-inventory-raw-read.txt 2>&1; then
  echo "reader role unexpectedly read raw CRM schema object hashes" >&2
  exit 1
fi
if reader_psql -c "SELECT raw_json FROM crm_schema_fields LIMIT 1" >>/tmp/gongctl-postgres-reader-crm-inventory-raw-read.txt 2>&1; then
  echo "reader role unexpectedly read raw CRM schema field JSON" >&2
  exit 1
fi
if reader_psql -c "SELECT raw_sha256 FROM crm_schema_fields LIMIT 1" >>/tmp/gongctl-postgres-reader-crm-inventory-raw-read.txt 2>&1; then
  echo "reader role unexpectedly read raw CRM schema field hashes" >&2
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
if reader_psql -c "SELECT object_id FROM call_context_objects LIMIT 1" >>/tmp/gongctl-postgres-reader-sensitive-read.txt 2>&1; then
  echo "reader role unexpectedly read normalized CRM object IDs" >&2
  exit 1
fi
if reader_psql -c "SELECT object_key FROM call_context_objects LIMIT 1" >/tmp/gongctl-postgres-reader-object-key-read.txt 2>&1; then
  echo "reader role unexpectedly read normalized CRM object keys" >&2
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
if reader_psql -tA -c "SELECT profile_json ? 'canonical_json' FROM gongmcp_active_business_profile()" >>/tmp/gongctl-postgres-reader-sensitive-read.txt 2>&1 && tail -n 1 /tmp/gongctl-postgres-reader-sensitive-read.txt | grep -q '^t$'; then
  echo "reader role unexpectedly read canonical profile JSON from active-profile function" >&2
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
if reader_psql -c "SELECT * FROM profile_call_fact_cache LIMIT 1" >>/tmp/gongctl-postgres-reader-sensitive-read.txt 2>&1; then
  echo "reader role unexpectedly read profile fact-cache table" >&2
  exit 1
fi
if reader_psql -c "SELECT * FROM governance_policy_state LIMIT 1" >>/tmp/gongctl-postgres-reader-sensitive-read.txt 2>&1; then
  echo "reader role unexpectedly read governance policy table directly" >&2
  exit 1
fi
if reader_psql -c "SELECT * FROM governance_suppressed_calls LIMIT 1" >>/tmp/gongctl-postgres-reader-sensitive-read.txt 2>&1; then
  echo "reader role unexpectedly read governance suppressed-call table directly" >&2
  exit 1
fi
if reader_psql -c "SELECT field_values_json FROM gongmcp_profile_call_fact_cache(1, 'not-a-real-sha') LIMIT 1" >>/tmp/gongctl-postgres-reader-sensitive-read.txt 2>&1; then
  echo "reader role unexpectedly read profile field values through cache helper" >&2
  exit 1
fi
if reader_psql -c "SELECT field_values_json FROM gongmcp_profile_call_fact_cache((SELECT id FROM profile_meta WHERE is_active = true LIMIT 1), (SELECT canonical_sha256 FROM profile_meta WHERE is_active = true LIMIT 1)) LIMIT 1" >>/tmp/gongctl-postgres-reader-sensitive-read.txt 2>&1; then
  echo "reader role unexpectedly read mapped CRM values from profile fact-cache function" >&2
  exit 1
fi

if reader_psql -c "INSERT INTO users(user_id, raw_json, raw_sha256, first_seen_at, updated_at) VALUES('should-fail', '{}'::jsonb, 'x', now()::text, now()::text)" >/tmp/gongctl-postgres-reader-write.txt 2>&1; then
  echo "reader role unexpectedly wrote to Postgres" >&2
  exit 1
fi

go run ./cmd/gongmcp --print-postgres-reader-grants --tool-preset business-pilot --postgres-reader-role gongmcp_business_pilot_reader --postgres-database gongctl >/tmp/gongctl-postgres-business-pilot-reader-grants.sql
go run ./cmd/gongctl mcp postgres-reader-sql --preset business-pilot --role gongmcp_business_pilot_reader --database gongctl >/tmp/gongctl-postgres-business-pilot-reader-grants-gongctl.sql
go run ./cmd/gongctl mcp postgres-reader-apply --preset business-pilot --role gongmcp_business_pilot_reader --database gongctl >/tmp/gongctl-postgres-business-pilot-reader-apply-dry-run.sql
if GONG_DATABASE_URL="$WRITER_URL" go run ./cmd/gongctl mcp postgres-reader-apply --preset business-pilot --role gongmcp_business_pilot_reader --database wrong_db --apply >/tmp/gongctl-postgres-business-pilot-reader-apply-wrong-database.txt 2>&1; then
  echo "business-pilot reader apply unexpectedly accepted mismatched database name" >&2
  exit 1
fi
grep -q 'apply scoped Postgres reader grants failed' /tmp/gongctl-postgres-business-pilot-reader-apply-wrong-database.txt
if grep -q 'postgres://\|gongctl_dev_password\|gongmcp_reader_dev_password\|wrong_db' /tmp/gongctl-postgres-business-pilot-reader-apply-wrong-database.txt; then
  echo "business-pilot reader apply wrong-database output unexpectedly exposed URL, password, or database value" >&2
  exit 1
fi
grep -q 'Generated by gongmcp --print-postgres-reader-grants' /tmp/gongctl-postgres-business-pilot-reader-grants.sql
grep -q 'Generated by gongctl mcp postgres-reader-sql' /tmp/gongctl-postgres-business-pilot-reader-grants-gongctl.sql
grep -q 'Generated by gongctl mcp postgres-reader-apply' /tmp/gongctl-postgres-business-pilot-reader-apply-dry-run.sql
grep -q 'GRANT SELECT ("context_present", "parties_count", "started_at") ON TABLE public."calls"' /tmp/gongctl-postgres-business-pilot-reader-grants.sql
grep -q 'GRANT SELECT ("context_present", "parties_count", "started_at") ON TABLE public."calls"' /tmp/gongctl-postgres-business-pilot-reader-grants-gongctl.sql
grep -q 'GRANT SELECT ("context_present", "parties_count", "started_at") ON TABLE public."calls"' /tmp/gongctl-postgres-business-pilot-reader-apply-dry-run.sql
grep -q 'GRANT EXECUTE ON FUNCTION public.gongmcp_active_business_profile_sanitized()' /tmp/gongctl-postgres-business-pilot-reader-grants.sql
grep -q 'GRANT EXECUTE ON FUNCTION public.gongmcp_active_business_profile_sanitized()' /tmp/gongctl-postgres-business-pilot-reader-grants-gongctl.sql
grep -q 'GRANT EXECUTE ON FUNCTION public.gongmcp_active_business_profile_sanitized()' /tmp/gongctl-postgres-business-pilot-reader-apply-dry-run.sql
grep -q 'GRANT EXECUTE ON FUNCTION public.gongmcp_profile_call_fact_cache_sanitized_limited(bigint, text, integer)' /tmp/gongctl-postgres-business-pilot-reader-grants.sql
grep -q 'GRANT EXECUTE ON FUNCTION public.gongmcp_profile_call_fact_cache_sanitized_limited(bigint, text, integer)' /tmp/gongctl-postgres-business-pilot-reader-grants-gongctl.sql
grep -q 'GRANT EXECUTE ON FUNCTION public.gongmcp_profile_call_fact_cache_sanitized_limited(bigint, text, integer)' /tmp/gongctl-postgres-business-pilot-reader-apply-dry-run.sql
grep -q 'GRANT EXECUTE ON FUNCTION public.gongmcp_profile_call_fact_cache_meta_sanitized(bigint)' /tmp/gongctl-postgres-business-pilot-reader-grants.sql
grep -q 'GRANT EXECUTE ON FUNCTION public.gongmcp_profile_call_fact_cache_meta_sanitized(bigint)' /tmp/gongctl-postgres-business-pilot-reader-grants-gongctl.sql
grep -q 'GRANT EXECUTE ON FUNCTION public.gongmcp_profile_call_fact_cache_meta_sanitized(bigint)' /tmp/gongctl-postgres-business-pilot-reader-apply-dry-run.sql
grep -q 'GRANT EXECUTE ON FUNCTION public.gongmcp_profile_call_fact_summary_sanitized' /tmp/gongctl-postgres-business-pilot-reader-grants.sql
grep -q 'GRANT EXECUTE ON FUNCTION public.gongmcp_profile_call_fact_summary_sanitized' /tmp/gongctl-postgres-business-pilot-reader-grants-gongctl.sql
grep -q 'GRANT EXECUTE ON FUNCTION public.gongmcp_profile_call_fact_summary_sanitized' /tmp/gongctl-postgres-business-pilot-reader-apply-dry-run.sql
grep -q 'GRANT EXECUTE ON FUNCTION public.gongmcp_profile_data_fingerprint()' /tmp/gongctl-postgres-business-pilot-reader-grants.sql
grep -q 'GRANT EXECUTE ON FUNCTION public.gongmcp_profile_data_fingerprint()' /tmp/gongctl-postgres-business-pilot-reader-grants-gongctl.sql
grep -q 'GRANT EXECUTE ON FUNCTION public.gongmcp_profile_data_fingerprint()' /tmp/gongctl-postgres-business-pilot-reader-apply-dry-run.sql
grep -q 'GRANT EXECUTE ON FUNCTION public.gongmcp_profile_lifecycle_summary_sanitized(bigint, text, text)' /tmp/gongctl-postgres-business-pilot-reader-grants.sql
grep -q 'GRANT EXECUTE ON FUNCTION public.gongmcp_profile_lifecycle_summary_sanitized(bigint, text, text)' /tmp/gongctl-postgres-business-pilot-reader-grants-gongctl.sql
grep -q 'GRANT EXECUTE ON FUNCTION public.gongmcp_profile_lifecycle_summary_sanitized(bigint, text, text)' /tmp/gongctl-postgres-business-pilot-reader-apply-dry-run.sql
grep -q 'GRANT EXECUTE ON FUNCTION public.gongmcp_profile_transcript_backlog_sanitized(bigint, text, text, text, text, text, text, text, integer)' /tmp/gongctl-postgres-business-pilot-reader-grants.sql
grep -q 'GRANT EXECUTE ON FUNCTION public.gongmcp_profile_transcript_backlog_sanitized(bigint, text, text, text, text, text, text, text, integer)' /tmp/gongctl-postgres-business-pilot-reader-grants-gongctl.sql
grep -q 'GRANT EXECUTE ON FUNCTION public.gongmcp_profile_transcript_backlog_sanitized(bigint, text, text, text, text, text, text, text, integer)' /tmp/gongctl-postgres-business-pilot-reader-apply-dry-run.sql
for generated_sql in /tmp/gongctl-postgres-business-pilot-reader-grants.sql /tmp/gongctl-postgres-business-pilot-reader-grants-gongctl.sql /tmp/gongctl-postgres-business-pilot-reader-apply-dry-run.sql; do
  grep -q 'Role and credentials must already exist' "$generated_sql"
  grep -q 'clearing existing public table/function/sequence privileges' "$generated_sql"
  grep -q 'startup rejects default privileges' "$generated_sql"
  grep -q 'REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA "public"' "$generated_sql"
  grep -q 'REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA "public"' "$generated_sql"
  grep -q 'REVOKE ALL PRIVILEGES ON ALL FUNCTIONS IN SCHEMA "public"' "$generated_sql"
  grep -q 'not an analyst SQL login' "$generated_sql"
  grep -q 'minimized operational metadata' "$generated_sql"
  grep -q 'reviewed business-workbench/facade, business-pilot, analyst, and redacted-all-readonly scoped readers' "$generated_sql"
  if grep -q 'PASSWORD\|postgres://\|GONG_DATABASE_URL' "$generated_sql"; then
    echo "generated business-pilot reader grant SQL unexpectedly contains secret or connection-url marker: $generated_sql" >&2
    exit 1
  fi
  for secret_value in "$GONGCTL_POSTGRES_PASSWORD" "$GONGMCP_READER_PASSWORD" "$GONGMCP_BUSINESS_PILOT_READER_PASSWORD" "$GONGMCP_FUNCTION_FREE_READER_PASSWORD"; do
    if [ -n "$secret_value" ] && grep -Fq "$secret_value" "$generated_sql"; then
      echo "generated business-pilot reader grant SQL unexpectedly contains a configured secret value: $generated_sql" >&2
      exit 1
    fi
  done
  if grep 'ON TABLE public."calls"' "$generated_sql" | grep -q '"call_id"\|"title"'; then
    echo "generated business-pilot reader grant SQL unexpectedly grants calls.call_id or calls.title: $generated_sql" >&2
    exit 1
  fi
  if grep 'ON TABLE public."call_facts"' "$generated_sql" | grep -q '"call_id"\|"title"'; then
    echo "generated business-pilot reader grant SQL unexpectedly grants call_facts.call_id or call_facts.title: $generated_sql" >&2
    exit 1
  fi
  if grep 'ON TABLE public."gongmcp_call_context_objects"' "$generated_sql" | grep -q '"call_id"\|"object_key"'; then
    echo "generated business-pilot reader grant SQL unexpectedly grants context object identifiers: $generated_sql" >&2
    exit 1
  fi
  if grep 'ON TABLE public."gongmcp_call_context_fields"' "$generated_sql" | grep -q '"call_id"\|"object_key"'; then
    echo "generated business-pilot reader grant SQL unexpectedly grants context field identifiers: $generated_sql" >&2
    exit 1
  fi
  if grep -q 'GRANT EXECUTE ON FUNCTION public.gongmcp_profile_call_fact_cache(bigint, text)' "$generated_sql"; then
    echo "generated business-pilot reader grant SQL unexpectedly grants identifier-bearing profile cache helper: $generated_sql" >&2
    exit 1
  fi
  if grep -q 'GRANT EXECUTE ON FUNCTION public.gongmcp_profile_call_fact_cache_meta(bigint, text)' "$generated_sql"; then
    echo "generated business-pilot reader grant SQL unexpectedly grants canonical profile cache metadata helper: $generated_sql" >&2
    exit 1
  fi
  if grep -q 'GRANT EXECUTE ON FUNCTION public.gongmcp_profile_call_fact_cache_sanitized(bigint, text)' "$generated_sql"; then
    echo "generated business-pilot reader grant SQL unexpectedly grants unbounded sanitized profile cache helper: $generated_sql" >&2
    exit 1
  fi
  if grep -q 'GRANT EXECUTE ON FUNCTION public.gongmcp_profile_call_fact_summary(bigint, text, text, text, text, text, text, text, integer)' "$generated_sql"; then
    echo "generated business-pilot reader grant SQL unexpectedly grants CRM-value profile summary helper: $generated_sql" >&2
    exit 1
  fi
  if grep -q 'GRANT EXECUTE ON FUNCTION public.gongmcp_active_business_profile()' "$generated_sql"; then
    echo "generated business-pilot reader grant SQL unexpectedly grants generic active profile helper: $generated_sql" >&2
    exit 1
  fi
  if ! grep -Fq 'DO $gongctl_scoped_reader_reconcile$' "$generated_sql"; then
    echo "generated business-pilot reader grant SQL missing reconcile block: $generated_sql" >&2
    exit 1
  fi
done
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T -e GONGMCP_BUSINESS_PILOT_READER_PASSWORD="$GONGMCP_BUSINESS_PILOT_READER_PASSWORD" postgres sh -s >/tmp/gongctl-postgres-business-pilot-reader-create.txt 2>&1 <<'SH'
set -eu
psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 -v reader_password="$GONGMCP_BUSINESS_PILOT_READER_PASSWORD" <<'SQL'
DROP ROLE IF EXISTS gongmcp_business_pilot_reader;
CREATE ROLE gongmcp_business_pilot_reader LOGIN NOINHERIT PASSWORD :'reader_password';
SQL
SH
GONG_DATABASE_URL="$WRITER_URL" go run ./cmd/gongctl mcp postgres-reader-apply --preset business-pilot --role gongmcp_business_pilot_reader --database gongctl --apply >/tmp/gongctl-postgres-business-pilot-reader-apply.json
grep -q '"status": "applied"' /tmp/gongctl-postgres-business-pilot-reader-apply.json
grep -q '"credential_note": "database_url_not_exported"' /tmp/gongctl-postgres-business-pilot-reader-apply.json
grep -q '"role_note": "existing_role_only_passwords_managed_externally"' /tmp/gongctl-postgres-business-pilot-reader-apply.json
if grep -q 'postgres://\|PASSWORD\|GONG_DATABASE_URL' /tmp/gongctl-postgres-business-pilot-reader-apply.json; then
  echo "business-pilot reader apply JSON unexpectedly contains secret or connection-url marker" >&2
  exit 1
fi
for secret_value in "$GONGCTL_POSTGRES_PASSWORD" "$GONGMCP_READER_PASSWORD" "$GONGMCP_BUSINESS_PILOT_READER_PASSWORD" "$GONGMCP_FUNCTION_FREE_READER_PASSWORD"; do
  if [ -n "$secret_value" ] && grep -Fq "$secret_value" /tmp/gongctl-postgres-business-pilot-reader-apply.json; then
    echo "business-pilot reader apply JSON unexpectedly contains a configured secret value" >&2
    exit 1
  fi
done
GONG_DATABASE_URL="$WRITER_URL" go run ./cmd/gongctl sync read-model >/tmp/gongctl-postgres-business-pilot-reader-post-reconcile-read-model.json

{
  printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_sync_status","arguments":{}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"summarize_call_facts","arguments":{"group_by":"lifecycle","limit":5}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"summarize_calls_by_lifecycle","arguments":{}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"rank_transcript_backlog","arguments":{"limit":5}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"summarize_call_facts","arguments":{"group_by":"scope","lifecycle_source":"profile","limit":5}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"rank_transcript_backlog","arguments":{"lifecycle_source":"profile","from_date":"2026-01-01","to_date":"2026-12-31","limit":5}}}'
} | GONG_DATABASE_URL="$BUSINESS_PILOT_READER_URL" GONGMCP_TOOL_PRESET=business-pilot GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS=1 go run ./cmd/gongmcp > /tmp/gongctl-postgres-business-pilot-scoped-reader.jsonl
grep -q '"get_sync_status"' /tmp/gongctl-postgres-business-pilot-scoped-reader.jsonl
grep -q '"summarize_call_facts"' /tmp/gongctl-postgres-business-pilot-scoped-reader.jsonl
grep -q '"summarize_calls_by_lifecycle"' /tmp/gongctl-postgres-business-pilot-scoped-reader.jsonl
grep -q '"rank_transcript_backlog"' /tmp/gongctl-postgres-business-pilot-scoped-reader.jsonl
grep -q 'priority_score' /tmp/gongctl-postgres-business-pilot-scoped-reader.jsonl
assert_mcp_success /tmp/gongctl-postgres-business-pilot-scoped-reader.jsonl 3 4 5 6 7 8
{
  printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"summarize_call_facts","arguments":{"group_by":"lifecycle","lifecycle_source":"builtin","limit":5}}}'
} | GONG_DATABASE_URL="$BUSINESS_PILOT_READER_URL" GONGMCP_TOOL_PRESET=business-pilot GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS=1 go run ./cmd/gongmcp > /tmp/gongctl-postgres-business-pilot-scoped-reader-builtin-denied.jsonl
if assert_mcp_success /tmp/gongctl-postgres-business-pilot-scoped-reader-builtin-denied.jsonl 2 >/dev/null 2>&1; then
  echo "business-pilot scoped reader unexpectedly served builtin lifecycle_source path" >&2
  exit 1
fi
grep -q 'permission denied' /tmp/gongctl-postgres-business-pilot-scoped-reader-builtin-denied.jsonl
if business_pilot_reader_psql -c "SELECT * FROM gongmcp_missing_transcripts('', '', '', '', '', '', '', '', 1)" >/tmp/gongctl-postgres-business-pilot-reader-missing-transcripts-denied.txt 2>&1; then
  echo "business-pilot scoped reader unexpectedly executed missing_transcripts function" >&2
  exit 1
fi
grep -q 'permission denied' /tmp/gongctl-postgres-business-pilot-reader-missing-transcripts-denied.txt
if business_pilot_reader_psql -c "SELECT call_id FROM calls LIMIT 1" >/tmp/gongctl-postgres-business-pilot-reader-calls-call-id-denied.txt 2>&1; then
  echo "business-pilot scoped reader unexpectedly read calls.call_id directly" >&2
  exit 1
fi
grep -q 'permission denied' /tmp/gongctl-postgres-business-pilot-reader-calls-call-id-denied.txt
if business_pilot_reader_psql -c "SELECT title FROM calls LIMIT 1" >/tmp/gongctl-postgres-business-pilot-reader-calls-title-denied.txt 2>&1; then
  echo "business-pilot scoped reader unexpectedly read calls.title directly" >&2
  exit 1
fi
grep -q 'permission denied' /tmp/gongctl-postgres-business-pilot-reader-calls-title-denied.txt
if business_pilot_reader_psql -c "SELECT call_id FROM call_facts LIMIT 1" >/tmp/gongctl-postgres-business-pilot-reader-call-facts-call-id-denied.txt 2>&1; then
  echo "business-pilot scoped reader unexpectedly read call_facts.call_id directly" >&2
  exit 1
fi
grep -q 'permission denied' /tmp/gongctl-postgres-business-pilot-reader-call-facts-call-id-denied.txt
if business_pilot_reader_psql -c "SELECT title FROM call_facts LIMIT 1" >/tmp/gongctl-postgres-business-pilot-reader-call-facts-title-denied.txt 2>&1; then
  echo "business-pilot scoped reader unexpectedly read call_facts.title directly" >&2
  exit 1
fi
grep -q 'permission denied' /tmp/gongctl-postgres-business-pilot-reader-call-facts-title-denied.txt
if business_pilot_reader_psql -c "SELECT call_id FROM gongmcp_call_context_objects LIMIT 1" >/tmp/gongctl-postgres-business-pilot-reader-context-objects-call-id-denied.txt 2>&1; then
  echo "business-pilot scoped reader unexpectedly read context object call_id directly" >&2
  exit 1
fi
grep -q 'permission denied' /tmp/gongctl-postgres-business-pilot-reader-context-objects-call-id-denied.txt
if business_pilot_reader_psql -c "SELECT object_key FROM gongmcp_call_context_fields LIMIT 1" >/tmp/gongctl-postgres-business-pilot-reader-context-fields-object-key-denied.txt 2>&1; then
  echo "business-pilot scoped reader unexpectedly read context field object_key directly" >&2
  exit 1
fi
grep -q 'permission denied' /tmp/gongctl-postgres-business-pilot-reader-context-fields-object-key-denied.txt
if business_pilot_reader_psql -c "SELECT profile_json FROM gongmcp_active_business_profile()" >/tmp/gongctl-postgres-business-pilot-reader-active-profile-generic-denied.txt 2>&1; then
  echo "business-pilot scoped reader unexpectedly executed generic active-profile helper" >&2
  exit 1
fi
grep -q 'permission denied' /tmp/gongctl-postgres-business-pilot-reader-active-profile-generic-denied.txt
business_pilot_reader_psql -tA -c "SELECT profile_json::text FROM gongmcp_active_business_profile_sanitized()" >/tmp/gongctl-postgres-business-pilot-reader-active-profile-sanitized.json
if grep -q 'source_path\|source_sha256\|canonical_sha256\|imported_by\|canonical_json\|evidence\|tracker_ids\|scorecard_question_ids' /tmp/gongctl-postgres-business-pilot-reader-active-profile-sanitized.json; then
  echo "business-pilot scoped reader sanitized active-profile helper exposed source metadata, canonical hash, canonical JSON, field evidence, or methodology identifiers" >&2
  exit 1
fi
grep -q '"profile_id"' /tmp/gongctl-postgres-business-pilot-reader-active-profile-sanitized.json
grep -q '"profile"' /tmp/gongctl-postgres-business-pilot-reader-active-profile-sanitized.json
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 -c "GRANT SELECT (title) ON calls TO gongmcp_business_pilot_reader" >/dev/null
if GONG_DATABASE_URL="$BUSINESS_PILOT_READER_URL" GONGMCP_TOOL_PRESET=business-pilot GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS=1 go run ./cmd/gongmcp </dev/null >/tmp/gongctl-postgres-business-pilot-reader-extra-column-grant.txt 2>&1; then
  echo "business-pilot scoped reader unexpectedly started with extra calls.title grant" >&2
  exit 1
fi
grep -q 'extra column SELECT grants outside selected MCP tools' /tmp/gongctl-postgres-business-pilot-reader-extra-column-grant.txt
grep -q 'calls.title' /tmp/gongctl-postgres-business-pilot-reader-extra-column-grant.txt
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 -c "REVOKE SELECT (title) ON calls FROM gongmcp_business_pilot_reader; GRANT SELECT ON SEQUENCE profile_meta_id_seq TO gongmcp_business_pilot_reader" >/dev/null
GONG_DATABASE_URL="$WRITER_URL" go run ./cmd/gongctl mcp postgres-reader-apply --preset business-pilot --role gongmcp_business_pilot_reader --database gongctl --apply >/tmp/gongctl-postgres-business-pilot-reader-apply-column-repair.json
grep -q '"status": "applied"' /tmp/gongctl-postgres-business-pilot-reader-apply-column-repair.json
if business_pilot_reader_psql -c "SELECT title FROM calls LIMIT 1" >/tmp/gongctl-postgres-business-pilot-reader-apply-column-repair-calls-title-denied.txt 2>&1; then
  echo "business-pilot scoped reader apply column repair left direct calls.title grant" >&2
  exit 1
fi
grep -q 'permission denied' /tmp/gongctl-postgres-business-pilot-reader-apply-column-repair-calls-title-denied.txt
if business_pilot_reader_psql -c "SELECT last_value FROM profile_meta_id_seq" >/tmp/gongctl-postgres-business-pilot-reader-apply-column-repair-sequence-denied.txt 2>&1; then
  echo "business-pilot scoped reader apply column repair left profile_meta_id_seq grant" >&2
  exit 1
fi
grep -q 'permission denied' /tmp/gongctl-postgres-business-pilot-reader-apply-column-repair-sequence-denied.txt
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 -c "GRANT SELECT ON SEQUENCE profile_meta_id_seq TO PUBLIC" >/dev/null
if GONG_DATABASE_URL="$WRITER_URL" go run ./cmd/gongctl mcp postgres-reader-apply --preset business-pilot --role gongmcp_business_pilot_reader --database gongctl --apply >/tmp/gongctl-postgres-business-pilot-reader-apply-public-sequence-denied.txt 2>&1; then
  echo "business-pilot scoped reader apply unexpectedly accepted PUBLIC sequence grant" >&2
  exit 1
fi
grep -q 'apply scoped Postgres reader grants failed' /tmp/gongctl-postgres-business-pilot-reader-apply-public-sequence-denied.txt
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 -c "REVOKE SELECT ON SEQUENCE profile_meta_id_seq FROM PUBLIC" >/dev/null
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 -c "ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON TABLES TO gongmcp_business_pilot_reader" >/dev/null
if GONG_DATABASE_URL="$BUSINESS_PILOT_READER_URL" GONGMCP_TOOL_PRESET=business-pilot GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS=1 go run ./cmd/gongmcp </dev/null >/tmp/gongctl-postgres-business-pilot-reader-default-privilege-drift.txt 2>&1; then
  echo "business-pilot scoped reader unexpectedly started with default privilege drift" >&2
  exit 1
fi
grep -q 'default privileges for future public objects' /tmp/gongctl-postgres-business-pilot-reader-default-privilege-drift.txt
grep -q 'public:gongmcp_business_pilot_reader:tables:SELECT' /tmp/gongctl-postgres-business-pilot-reader-default-privilege-drift.txt
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 -c "ALTER DEFAULT PRIVILEGES IN SCHEMA public REVOKE SELECT ON TABLES FROM gongmcp_business_pilot_reader" >/dev/null
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 -c "ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON TABLES TO PUBLIC" >/dev/null
if GONG_DATABASE_URL="$BUSINESS_PILOT_READER_URL" GONGMCP_TOOL_PRESET=business-pilot GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS=1 go run ./cmd/gongmcp </dev/null >/tmp/gongctl-postgres-business-pilot-reader-public-default-privilege-drift.txt 2>&1; then
  echo "business-pilot scoped reader unexpectedly started with PUBLIC default privilege drift" >&2
  exit 1
fi
grep -q 'default privileges for future public objects' /tmp/gongctl-postgres-business-pilot-reader-public-default-privilege-drift.txt
grep -q 'public:PUBLIC:tables:SELECT' /tmp/gongctl-postgres-business-pilot-reader-public-default-privilege-drift.txt
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 -c "ALTER DEFAULT PRIVILEGES IN SCHEMA public REVOKE SELECT ON TABLES FROM PUBLIC" >/dev/null
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 -c "REVOKE SELECT (title) ON calls FROM gongmcp_business_pilot_reader" >/dev/null
business_pilot_profile_identity="$(docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -tA -F '|' -c "SELECT id, canonical_sha256 FROM profile_meta WHERE is_active = true LIMIT 1")"
IFS='|' read -r business_pilot_profile_id business_pilot_profile_sha <<EOF
$business_pilot_profile_identity
EOF
if business_pilot_reader_psql -c "SELECT * FROM gongmcp_profile_call_fact_cache_meta($business_pilot_profile_id, '$business_pilot_profile_sha')" >/tmp/gongctl-postgres-business-pilot-reader-profile-cache-meta-denied.txt 2>&1; then
  echo "business-pilot scoped reader unexpectedly executed canonical profile cache metadata helper" >&2
  exit 1
fi
grep -q 'permission denied' /tmp/gongctl-postgres-business-pilot-reader-profile-cache-meta-denied.txt
business_pilot_reader_psql -tA -c "SELECT row_to_json(m)::text FROM gongmcp_profile_call_fact_cache_meta_sanitized($business_pilot_profile_id) m" >/tmp/gongctl-postgres-business-pilot-reader-profile-cache-meta-sanitized.json
if grep -q 'canonical_sha256' /tmp/gongctl-postgres-business-pilot-reader-profile-cache-meta-sanitized.json; then
  echo "business-pilot scoped reader sanitized profile cache metadata exposed canonical hash" >&2
  exit 1
fi
grep -q 'data_fingerprint' /tmp/gongctl-postgres-business-pilot-reader-profile-cache-meta-sanitized.json
if business_pilot_reader_psql -c "SELECT COUNT(*) FROM gongmcp_profile_call_fact_summary($business_pilot_profile_id, '$business_pilot_profile_sha', 'deal_stage', '', '', '', '', '', 5)" >/tmp/gongctl-postgres-business-pilot-reader-profile-summary-generic-denied.txt 2>&1; then
  echo "business-pilot scoped reader unexpectedly executed CRM-value profile summary helper" >&2
  exit 1
fi
grep -q 'permission denied' /tmp/gongctl-postgres-business-pilot-reader-profile-summary-generic-denied.txt
if business_pilot_reader_psql -c "SELECT COUNT(*) FROM gongmcp_profile_call_fact_summary_sanitized($business_pilot_profile_id, '$business_pilot_profile_sha', 'deal_stage', '', '', '', '', '', 5)" >/tmp/gongctl-postgres-business-pilot-reader-profile-summary-unsupported-group-denied.txt 2>&1; then
  echo "business-pilot scoped reader sanitized profile summary unexpectedly accepted CRM-value grouping" >&2
  exit 1
fi
grep -q 'not supported by the business-pilot scoped Postgres reader' /tmp/gongctl-postgres-business-pilot-reader-profile-summary-unsupported-group-denied.txt
business_pilot_safe_group_count="$(business_pilot_reader_psql -At -c "SELECT COUNT(*) FROM gongmcp_profile_call_fact_summary_sanitized($business_pilot_profile_id, '$business_pilot_profile_sha', 'lifecycle', '', '', '', '', '', 5)" | tee /tmp/gongctl-postgres-business-pilot-reader-profile-summary-sanitized.txt | tr -d '[:space:]')"
if [ "$business_pilot_safe_group_count" -lt 1 ]; then
  echo "business-pilot scoped reader sanitized profile summary returned no safe lifecycle groups" >&2
  exit 1
fi
business_pilot_lifecycle_summary_count="$(business_pilot_reader_psql -At -c "SELECT COUNT(*) FROM gongmcp_profile_lifecycle_summary_sanitized($business_pilot_profile_id, '$business_pilot_profile_sha', '')" | tee /tmp/gongctl-postgres-business-pilot-reader-profile-lifecycle-summary-sanitized.txt | tr -d '[:space:]')"
if [ "$business_pilot_lifecycle_summary_count" -lt 1 ]; then
  echo "business-pilot scoped reader sanitized lifecycle summary returned no rows" >&2
  exit 1
fi
business_pilot_backlog_identifier_count="$(business_pilot_reader_psql -At -c "SELECT COUNT(*) FROM gongmcp_profile_transcript_backlog_sanitized($business_pilot_profile_id, '$business_pilot_profile_sha', '', '', '', '', '', '', 5) WHERE call_id <> '' OR title <> ''" | tee /tmp/gongctl-postgres-business-pilot-reader-profile-backlog-sanitized.txt | tr -d '[:space:]')"
if [ "$business_pilot_backlog_identifier_count" != "0" ]; then
  echo "business-pilot scoped reader sanitized transcript backlog exposed identifiers" >&2
  exit 1
fi
business_pilot_backlog_fact_count="$(business_pilot_reader_psql -At -c "SELECT COUNT(*) FROM gongmcp_profile_transcript_backlog_sanitized($business_pilot_profile_id, '$business_pilot_profile_sha', '', '', '', '', '', '', 5) WHERE started_at <> '' AND bucket <> '' AND priority_score > 0" | tee -a /tmp/gongctl-postgres-business-pilot-reader-profile-backlog-sanitized.txt | tr -d '[:space:]')"
if [ "$business_pilot_backlog_fact_count" -lt 1 ]; then
  echo "business-pilot scoped reader sanitized transcript backlog returned no ranked backlog rows" >&2
  exit 1
fi
business_pilot_dated_backlog_fact_count="$(business_pilot_reader_psql -At -c "SELECT COUNT(*) FROM gongmcp_profile_transcript_backlog_sanitized($business_pilot_profile_id, '$business_pilot_profile_sha', '', '2026-01-01', '2026-12-31', '', '', '', 5) WHERE started_at <> '' AND bucket <> '' AND priority_score > 0" | tee -a /tmp/gongctl-postgres-business-pilot-reader-profile-backlog-sanitized.txt | tr -d '[:space:]')"
if [ "$business_pilot_dated_backlog_fact_count" -lt 1 ]; then
  echo "business-pilot scoped reader sanitized transcript backlog date filter returned no ranked backlog rows" >&2
  exit 1
fi
if business_pilot_reader_psql -c "SELECT COUNT(*) FROM gongmcp_profile_call_fact_cache($business_pilot_profile_id, '$business_pilot_profile_sha')" >/tmp/gongctl-postgres-business-pilot-reader-profile-cache-identifier-helper-denied.txt 2>&1; then
  echo "business-pilot scoped reader unexpectedly executed identifier-bearing profile cache helper" >&2
  exit 1
fi
grep -q 'permission denied' /tmp/gongctl-postgres-business-pilot-reader-profile-cache-identifier-helper-denied.txt
if business_pilot_reader_psql -c "SELECT COUNT(*) FROM gongmcp_profile_call_fact_cache_sanitized($business_pilot_profile_id, '$business_pilot_profile_sha')" >/tmp/gongctl-postgres-business-pilot-reader-profile-cache-unbounded-sanitized-denied.txt 2>&1; then
  echo "business-pilot scoped reader unexpectedly executed unbounded sanitized profile cache helper" >&2
  exit 1
fi
grep -q 'permission denied' /tmp/gongctl-postgres-business-pilot-reader-profile-cache-unbounded-sanitized-denied.txt
business_pilot_direct_profile_identifier_count="$(business_pilot_reader_psql -At -c "SELECT COUNT(*) FROM gongmcp_profile_call_fact_cache_sanitized_limited($business_pilot_profile_id, '$business_pilot_profile_sha', 1000) WHERE call_id <> '' OR title <> ''" | tee /tmp/gongctl-postgres-business-pilot-reader-profile-cache-sanitized-limited.txt | tr -d '[:space:]')"
if [ "$business_pilot_direct_profile_identifier_count" != "0" ]; then
  echo "business-pilot scoped reader limited sanitized profile cache returned identifier-bearing rows" >&2
  exit 1
fi
business_pilot_direct_profile_fact_count="$(business_pilot_reader_psql -At -c "SELECT COUNT(*) FROM gongmcp_profile_call_fact_cache_sanitized_limited($business_pilot_profile_id, '$business_pilot_profile_sha', 1000) WHERE started_at <> '' AND lifecycle_bucket <> ''" | tee -a /tmp/gongctl-postgres-business-pilot-reader-profile-cache-sanitized-limited.txt | tr -d '[:space:]')"
if [ "$business_pilot_direct_profile_fact_count" -lt 1 ]; then
  echo "business-pilot scoped reader limited sanitized profile cache returned no non-identifier fact rows" >&2
  exit 1
fi
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 -c "REVOKE EXECUTE ON FUNCTION gongmcp_profile_call_fact_summary_sanitized(bigint, text, text, text, text, text, text, text, integer) FROM gongmcp_business_pilot_reader" >/dev/null
if GONG_DATABASE_URL="$BUSINESS_PILOT_READER_URL" GONGMCP_TOOL_PRESET=business-pilot GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS=1 go run ./cmd/gongmcp </dev/null >/tmp/gongctl-postgres-business-pilot-reader-missing-grant.txt 2>&1; then
  echo "business-pilot scoped reader unexpectedly started without required function grant" >&2
  exit 1
fi
grep -q 'missing required function EXECUTE grants' /tmp/gongctl-postgres-business-pilot-reader-missing-grant.txt
grep -q 'gongmcp_profile_call_fact_summary' /tmp/gongctl-postgres-business-pilot-reader-missing-grant.txt
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 -c "GRANT SELECT (title) ON calls TO gongmcp_business_pilot_reader; GRANT EXECUTE ON FUNCTION gongmcp_profile_call_fact_summary_sanitized(bigint, text, text, text, text, text, text, text, integer) TO gongmcp_business_pilot_reader; GRANT EXECUTE ON FUNCTION gongmcp_profile_call_fact_summary(bigint, text, text, text, text, text, text, text, integer) TO gongmcp_business_pilot_reader; GRANT EXECUTE ON FUNCTION gongmcp_profile_call_fact_cache(bigint, text) TO gongmcp_business_pilot_reader; GRANT EXECUTE ON FUNCTION gongmcp_profile_call_fact_cache_meta(bigint, text) TO gongmcp_business_pilot_reader; GRANT EXECUTE ON FUNCTION gongmcp_profile_call_fact_cache_sanitized(bigint, text) TO gongmcp_business_pilot_reader; GRANT EXECUTE ON FUNCTION gongmcp_active_business_profile() TO gongmcp_business_pilot_reader; GRANT EXECUTE ON FUNCTION gongmcp_missing_transcripts(text, text, text, text, text, text, text, text, integer) TO gongmcp_business_pilot_reader" >/dev/null
GONG_DATABASE_URL="$WRITER_URL" go run ./cmd/gongctl mcp postgres-reader-apply --preset business-pilot --role gongmcp_business_pilot_reader --database gongctl --apply >/tmp/gongctl-postgres-business-pilot-reader-apply-repair.json
grep -q '"status": "applied"' /tmp/gongctl-postgres-business-pilot-reader-apply-repair.json
if business_pilot_reader_psql -c "SELECT COUNT(*) FROM gongmcp_profile_call_fact_cache($business_pilot_profile_id, '$business_pilot_profile_sha')" >/tmp/gongctl-postgres-business-pilot-reader-apply-repair-identifier-helper-denied.txt 2>&1; then
  echo "business-pilot scoped reader apply repair left identifier-bearing profile cache helper executable" >&2
  exit 1
fi
grep -q 'permission denied' /tmp/gongctl-postgres-business-pilot-reader-apply-repair-identifier-helper-denied.txt
if business_pilot_reader_psql -c "SELECT profile_json FROM gongmcp_active_business_profile()" >/tmp/gongctl-postgres-business-pilot-reader-apply-repair-active-profile-denied.txt 2>&1; then
  echo "business-pilot scoped reader apply repair left generic active-profile helper executable" >&2
  exit 1
fi
grep -q 'permission denied' /tmp/gongctl-postgres-business-pilot-reader-apply-repair-active-profile-denied.txt
if business_pilot_reader_psql -c "SELECT * FROM gongmcp_missing_transcripts('', '', '', '', '', '', '', '', 1)" >/tmp/gongctl-postgres-business-pilot-reader-apply-repair-missing-transcripts-denied.txt 2>&1; then
  echo "business-pilot scoped reader apply repair left missing_transcripts helper executable" >&2
  exit 1
fi
grep -q 'permission denied' /tmp/gongctl-postgres-business-pilot-reader-apply-repair-missing-transcripts-denied.txt
{
  printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_sync_status","arguments":{}}}'
} | GONG_DATABASE_URL="$BUSINESS_PILOT_READER_URL" GONGMCP_TOOL_PRESET=business-pilot GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS=1 go run ./cmd/gongmcp > /tmp/gongctl-postgres-business-pilot-reader-apply-repair-mcp.jsonl
assert_mcp_success /tmp/gongctl-postgres-business-pilot-reader-apply-repair-mcp.jsonl 3
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 -c "DROP ROLE IF EXISTS gongmcp_leaky_parent; CREATE ROLE gongmcp_leaky_parent NOLOGIN; GRANT SELECT (title) ON calls TO gongmcp_leaky_parent; GRANT gongmcp_leaky_parent TO gongmcp_business_pilot_reader" >/dev/null
if GONG_DATABASE_URL="$WRITER_URL" go run ./cmd/gongctl mcp postgres-reader-apply --preset business-pilot --role gongmcp_business_pilot_reader --database gongctl --apply >/tmp/gongctl-postgres-business-pilot-reader-apply-membership-denied.txt 2>&1; then
  echo "business-pilot scoped reader apply unexpectedly accepted inherited role membership" >&2
  exit 1
fi
grep -q 'apply scoped Postgres reader grants failed' /tmp/gongctl-postgres-business-pilot-reader-apply-membership-denied.txt
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 -c "REVOKE gongmcp_leaky_parent FROM gongmcp_business_pilot_reader; REVOKE SELECT (title) ON calls FROM gongmcp_leaky_parent; DROP ROLE gongmcp_leaky_parent" >/dev/null
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T -e GONGMCP_FUNCTION_FREE_READER_PASSWORD="$GONGMCP_FUNCTION_FREE_READER_PASSWORD" postgres sh -s >/tmp/gongctl-postgres-function-free-reader-create.txt 2>&1 <<'SH'
set -eu
psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 -v reader_password="$GONGMCP_FUNCTION_FREE_READER_PASSWORD" <<'SQL'
DROP ROLE IF EXISTS gongmcp_function_free_reader;
CREATE ROLE gongmcp_function_free_reader LOGIN PASSWORD :'reader_password';
REVOKE CREATE ON SCHEMA public FROM gongmcp_function_free_reader;
GRANT CONNECT ON DATABASE gongctl TO gongmcp_function_free_reader;
GRANT USAGE ON SCHEMA public TO gongmcp_function_free_reader;
GRANT SELECT ON TABLE gongctl_schema_migrations TO gongmcp_function_free_reader;
GRANT SELECT ON TABLE gongmcp_sync_runs TO gongmcp_function_free_reader;
GRANT SELECT ON TABLE gongmcp_sync_state TO gongmcp_function_free_reader;
	GRANT SELECT (call_id, title, started_at, duration_seconds, parties_count, context_present, first_seen_at, updated_at) ON calls TO gongmcp_function_free_reader;
	GRANT SELECT (user_id, title, active, first_seen_at, updated_at) ON users TO gongmcp_function_free_reader;
	GRANT SELECT (call_id, segment_count, first_seen_at, updated_at) ON transcripts TO gongmcp_function_free_reader;
	GRANT SELECT (id, call_id, object_type) ON call_context_objects TO gongmcp_function_free_reader;
	GRANT SELECT ON TABLE gongmcp_call_context_objects TO gongmcp_function_free_reader;
	GRANT SELECT ON TABLE gongmcp_call_context_fields TO gongmcp_function_free_reader;
	GRANT SELECT ON TABLE postgres_read_model_state TO gongmcp_function_free_reader;
GRANT SELECT (kind, object_id, name, active, updated_at) ON gong_settings TO gongmcp_function_free_reader;
GRANT SELECT (integration_id, name, provider, first_seen_at, updated_at) ON crm_integrations TO gongmcp_function_free_reader;
GRANT SELECT (integration_id, object_type, display_name, field_count, first_seen_at, updated_at) ON crm_schema_objects TO gongmcp_function_free_reader;
GRANT SELECT (integration_id, object_type, field_name, field_label, field_type, first_seen_at, updated_at) ON crm_schema_fields TO gongmcp_function_free_reader;
SQL
SH
{
  printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search_calls","arguments":{"limit":5}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"get_call","arguments":{"call_id":"synthetic-call-001"}}}'
} | GONG_DATABASE_URL="$FUNCTION_FREE_READER_URL" GONGMCP_TOOL_ALLOWLIST=search_calls,get_call GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS=1 go run ./cmd/gongmcp > /tmp/gongctl-postgres-function-free-scoped-reader.jsonl
grep -q '"search_calls"' /tmp/gongctl-postgres-function-free-scoped-reader.jsonl
grep -q '"get_call"' /tmp/gongctl-postgres-function-free-scoped-reader.jsonl
grep -q 'synthetic-call-001' /tmp/gongctl-postgres-function-free-scoped-reader.jsonl
assert_mcp_success /tmp/gongctl-postgres-function-free-scoped-reader.jsonl 3 4

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
  printf '%s\n' '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list_crm_object_types","arguments":{}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"list_crm_fields","arguments":{"object_type":"Opportunity","limit":10}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"list_crm_integrations","arguments":{}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"list_cached_crm_schema_objects","arguments":{"integration_id":"synthetic-crm-integration-001"}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"list_cached_crm_schema_fields","arguments":{"integration_id":"synthetic-crm-integration-001","object_type":"Opportunity","limit":10}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"list_gong_settings","arguments":{"kind":"scorecards","limit":10}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"list_scorecards","arguments":{"limit":10}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"get_scorecard","arguments":{"scorecard_id":"synthetic-scorecard-001"}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"summarize_scorecard_activity","arguments":{"limit":10}}}'
} | GONG_DATABASE_URL="$READER_URL" GONGMCP_TOOL_PRESET=analyst-core go run ./cmd/gongmcp > /tmp/gongctl-postgres-analyst-core.jsonl

grep -q '"list_crm_object_types"' /tmp/gongctl-postgres-analyst-core.jsonl
grep -q '"list_crm_fields"' /tmp/gongctl-postgres-analyst-core.jsonl
grep -q '"list_crm_integrations"' /tmp/gongctl-postgres-analyst-core.jsonl
grep -q '"list_cached_crm_schema_objects"' /tmp/gongctl-postgres-analyst-core.jsonl
grep -q '"list_cached_crm_schema_fields"' /tmp/gongctl-postgres-analyst-core.jsonl
grep -q '"list_gong_settings"' /tmp/gongctl-postgres-analyst-core.jsonl
grep -q '"list_scorecards"' /tmp/gongctl-postgres-analyst-core.jsonl
grep -q '"get_scorecard"' /tmp/gongctl-postgres-analyst-core.jsonl
grep -q '"summarize_scorecard_activity"' /tmp/gongctl-postgres-analyst-core.jsonl
grep -q 'Opportunity' /tmp/gongctl-postgres-analyst-core.jsonl
grep -q 'StageName' /tmp/gongctl-postgres-analyst-core.jsonl
grep -q 'Synthetic Salesforce' /tmp/gongctl-postgres-analyst-core.jsonl
grep -q 'Synthetic discovery quality' /tmp/gongctl-postgres-analyst-core.jsonl
grep -q 'Did the seller confirm the implementation timeline?' /tmp/gongctl-postgres-analyst-core.jsonl
grep -q 'total_answered_scorecards.*2' /tmp/gongctl-postgres-analyst-core.jsonl
grep -q 'manual_count.*1' /tmp/gongctl-postgres-analyst-core.jsonl
grep -q 'automatic_count.*1' /tmp/gongctl-postgres-analyst-core.jsonl
assert_mcp_success /tmp/gongctl-postgres-analyst-core.jsonl 3 4 5 6 7 8 9 10 11
if grep -q 'Proposal\|Manufacturing\|Profile Account\|raw_json\|field_value_text\|raw_sha256\|raw_payload\|synthetic-answered-scorecard\|synthetic-user-' /tmp/gongctl-postgres-analyst-core.jsonl; then
  echo "analyst-core inventory exposed raw CRM/settings/activity values" >&2
  exit 1
fi

{
  printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search_transcripts_by_call_facts","arguments":{"query":"shared Postgres","limit":5}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"search_transcript_quotes_with_attribution","arguments":{"query":"shared Postgres","limit":5}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"search_transcripts_by_filters","arguments":{"filter":{"query":"shared Postgres"},"limit":5}}}'
} | GONG_DATABASE_URL="$READER_URL" GONGMCP_TOOL_PRESET=analyst-business-core go run ./cmd/gongmcp > /tmp/gongctl-postgres-analyst-business-core.jsonl

grep -q '"search_transcripts_by_call_facts"' /tmp/gongctl-postgres-analyst-business-core.jsonl
grep -q '"search_transcript_quotes_with_attribution"' /tmp/gongctl-postgres-analyst-business-core.jsonl
grep -q '"search_transcripts_by_filters"' /tmp/gongctl-postgres-analyst-business-core.jsonl
grep -q 'shared.*Postgres' /tmp/gongctl-postgres-analyst-business-core.jsonl
assert_mcp_success /tmp/gongctl-postgres-analyst-business-core.jsonl 3 4 5
if grep -q 'raw_json\|field_value_text' /tmp/gongctl-postgres-analyst-business-core.jsonl; then
  echo "analyst-business-core output exposed raw storage fields" >&2
  exit 1
fi

docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 >/tmp/gongctl-postgres-crm-context-fixture.txt <<'SQL'
INSERT INTO call_context_objects(call_id, object_key, object_type, object_id, object_name, raw_json)
VALUES ('synthetic-call-001', 'Opportunity:opp-crm-context-private', 'Opportunity', 'opp-crm-context-private', 'CRM Context Private Opportunity', '{"type":"Opportunity"}'::jsonb)
ON CONFLICT (call_id, object_key) DO UPDATE
   SET object_type = EXCLUDED.object_type,
       object_id = EXCLUDED.object_id,
       object_name = EXCLUDED.object_name,
       raw_json = EXCLUDED.raw_json;
INSERT INTO call_context_fields(call_id, object_key, field_name, field_label, field_type, field_value_text, raw_json)
VALUES ('synthetic-call-001', 'Opportunity:opp-crm-context-private', 'Loss_Reason__c', 'Loss Reason', 'string', 'Timeline uncertainty', '{"name":"Loss_Reason__c","value":"Timeline uncertainty"}'::jsonb)
ON CONFLICT (call_id, object_key, field_name) DO UPDATE
   SET field_label = EXCLUDED.field_label,
       field_type = EXCLUDED.field_type,
       field_value_text = EXCLUDED.field_value_text,
       raw_json = EXCLUDED.raw_json;
SQL
reader_psql -tA -F '|' -c "SELECT started_at, object_type, matching_object_count, segment_index, start_ms, end_ms, snippet FROM gongmcp_search_transcript_segments_by_crm_context('shared Postgres', 'Opportunity', 'opp-crm-context-private', 5)" >/tmp/gongctl-postgres-reader-search-transcripts-by-crm-context.txt
{
  printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search_transcripts_by_crm_context","arguments":{"query":"shared Postgres","object_type":"Opportunity","object_id":"opp-crm-context-private","limit":5}}}'
} | GONG_DATABASE_URL="$READER_URL" GONGMCP_TOOL_ALLOWLIST=search_transcripts_by_crm_context go run ./cmd/gongmcp > /tmp/gongctl-postgres-search-transcripts-by-crm-context.jsonl
grep -q '"search_transcripts_by_crm_context"' /tmp/gongctl-postgres-search-transcripts-by-crm-context.jsonl
grep -q 'shared.*Postgres' /tmp/gongctl-postgres-search-transcripts-by-crm-context.jsonl
grep -q 'object_type.*Opportunity' /tmp/gongctl-postgres-search-transcripts-by-crm-context.jsonl
grep -q 'matching_object_count.*1' /tmp/gongctl-postgres-search-transcripts-by-crm-context.jsonl
assert_mcp_success /tmp/gongctl-postgres-search-transcripts-by-crm-context.jsonl 3
if grep -q 'synthetic-call-001\|Pulsaris implementation kickoff\|opp-crm-context-private\|CRM Context Private Opportunity\|speaker-1\|raw_json\|raw_sha256\|search_vector\|field_value_text' /tmp/gongctl-postgres-search-transcripts-by-crm-context.jsonl; then
  echo "Postgres CRM-context transcript MCP output exposed identifiers, titles, speakers, or raw storage fields" >&2
  exit 1
fi
grep -q '2026-01-15T15:00:00Z|Opportunity|1|0|0|5000|' /tmp/gongctl-postgres-reader-search-transcripts-by-crm-context.txt
grep -q 'shared.*Postgres' /tmp/gongctl-postgres-reader-search-transcripts-by-crm-context.txt
if grep -q 'synthetic-call-001\|speaker-1\|Pulsaris implementation kickoff\|opp-crm-context-private\|CRM Context Private Opportunity\|raw_json\|raw_sha256\|search_vector\|field_value_text' /tmp/gongctl-postgres-reader-search-transcripts-by-crm-context.txt; then
  echo "Postgres CRM-context transcript function exposed title, object identifiers, or raw storage fields" >&2
  exit 1
fi
for column in call_id title object_id object_name object_key speaker_id field_value_text raw_json raw_sha256 text; do
  if reader_psql -c "SELECT ${column} FROM gongmcp_search_transcript_segments_by_crm_context('shared Postgres', 'Opportunity', 'opp-crm-context-private', 5)" >/tmp/gongctl-postgres-reader-search-transcripts-by-crm-context-${column}.txt 2>&1; then
    echo "Postgres CRM-context transcript function exposed ${column} column" >&2
    exit 1
  fi
done
if reader_psql -c "SELECT text FROM transcript_segments LIMIT 1" >/tmp/gongctl-postgres-reader-search-transcripts-by-crm-context-transcript-text.txt 2>&1; then
  echo "Postgres reader can directly select transcript segment text" >&2
  exit 1
fi
if reader_psql -c "SELECT object_id FROM call_context_objects LIMIT 1" >/tmp/gongctl-postgres-reader-search-transcripts-by-crm-context-object-id.txt 2>&1; then
  echo "Postgres reader can directly select CRM object IDs" >&2
  exit 1
fi
if reader_psql -c "SELECT field_value_text FROM call_context_fields LIMIT 1" >/tmp/gongctl-postgres-reader-search-transcripts-by-crm-context-field-value-text.txt 2>&1; then
  echo "Postgres reader can directly select CRM field values" >&2
  exit 1
fi

{
  printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list_unmapped_crm_fields","arguments":{"limit":10}}}'
} | GONG_DATABASE_URL="$READER_URL" GONGMCP_TOOL_ALLOWLIST=list_unmapped_crm_fields go run ./cmd/gongmcp > /tmp/gongctl-postgres-unmapped-crm-fields.jsonl
grep -q '"list_unmapped_crm_fields"' /tmp/gongctl-postgres-unmapped-crm-fields.jsonl
grep -q 'field_name.*Industry' /tmp/gongctl-postgres-unmapped-crm-fields.jsonl
grep -q 'field_name.*Type' /tmp/gongctl-postgres-unmapped-crm-fields.jsonl
assert_mcp_success /tmp/gongctl-postgres-unmapped-crm-fields.jsonl 3
if grep -q 'Proposal\|Manufacturing\|New Business\|Prospect\|synthetic-profile-call-001\|opp-profile-001\|acct-profile-001\|Profile Opportunity\|Profile Account\|raw_json\|raw_sha256\|field_value_text\|canonical_sha256\|Synthetic Postgres profile' /tmp/gongctl-postgres-unmapped-crm-fields.jsonl; then
  echo "Postgres unmapped CRM fields output exposed raw values, identifiers, profile details, or raw storage fields" >&2
  exit 1
fi
reader_psql -tAc "SELECT field_name FROM gongmcp_unmapped_crm_field_inventory(10) ORDER BY field_name" >/tmp/gongctl-postgres-reader-unmapped-crm-function-fields.txt
grep -q 'Industry' /tmp/gongctl-postgres-reader-unmapped-crm-function-fields.txt
grep -q 'Type' /tmp/gongctl-postgres-reader-unmapped-crm-function-fields.txt
if grep -q 'StageName\|Account_Type__c' /tmp/gongctl-postgres-reader-unmapped-crm-function-fields.txt; then
  echo "Postgres unmapped CRM function returned mapped profile fields" >&2
  exit 1
fi
if reader_psql -c "SELECT field_value_text FROM gongmcp_unmapped_crm_field_inventory(10)" >/tmp/gongctl-postgres-reader-unmapped-crm-function-value-column.txt 2>&1; then
  echo "Postgres unmapped CRM function exposed field_value_text column" >&2
  exit 1
fi

{
  printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search_crm_field_values","arguments":{"object_type":"Opportunity","field_name":"StageName","value_query":"Proposal","limit":5}}}'
} | GONG_DATABASE_URL="$READER_URL" GONGMCP_TOOL_ALLOWLIST=search_crm_field_values go run ./cmd/gongmcp > /tmp/gongctl-postgres-crm-field-values-redacted.jsonl
grep -q '"search_crm_field_values"' /tmp/gongctl-postgres-crm-field-values-redacted.jsonl
grep -q 'field_name.*StageName' /tmp/gongctl-postgres-crm-field-values-redacted.jsonl
assert_mcp_success /tmp/gongctl-postgres-crm-field-values-redacted.jsonl 3
if grep -q 'Proposal\|synthetic-profile-call-001\|Profile lifecycle proposal review\|opp-profile-001\|Profile Opportunity\|field_value_text\|raw_json\|raw_sha256' /tmp/gongctl-postgres-crm-field-values-redacted.jsonl; then
  echo "Postgres CRM field-value default output exposed identifiers, titles, values, or raw fields" >&2
  exit 1
fi

{
  printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search_crm_field_values","arguments":{"object_type":"Opportunity","field_name":"StageName","value_query":"Proposal","limit":5,"include_call_ids":true,"include_value_snippets":true}}}'
} | env GONG_DATABASE_URL="$READER_URL" GONGMCP_TOOL_ALLOWLIST=search_crm_field_values go run ./cmd/gongmcp > /tmp/gongctl-postgres-crm-field-values-optin.jsonl
grep -q '"search_crm_field_values"' /tmp/gongctl-postgres-crm-field-values-optin.jsonl
grep -q 'synthetic-profile-call-001' /tmp/gongctl-postgres-crm-field-values-optin.jsonl
grep -q 'Proposal' /tmp/gongctl-postgres-crm-field-values-optin.jsonl
grep -q 'Profile lifecycle proposal review' /tmp/gongctl-postgres-crm-field-values-optin.jsonl
assert_mcp_success /tmp/gongctl-postgres-crm-field-values-optin.jsonl 3
if grep -q 'opp-profile-001\|Profile Opportunity\|field_value_text\|raw_json\|raw_sha256' /tmp/gongctl-postgres-crm-field-values-optin.jsonl; then
  echo "Postgres CRM field-value opt-in output exposed object identifiers/names or raw fields" >&2
  exit 1
fi
reader_psql -tAc "SELECT call_id || '|' || title || '|' || value_snippet FROM gongmcp_crm_field_value_search('Opportunity', 'StageName', 'Proposal', 5, false, false)" >/tmp/gongctl-postgres-reader-crm-field-value-function-redacted.txt
if grep -q 'Proposal\|synthetic-profile-call-001\|Profile lifecycle proposal review\|opp-profile-001\|Profile Opportunity' /tmp/gongctl-postgres-reader-crm-field-value-function-redacted.txt; then
  echo "Postgres CRM field-value reader function default exposed identifiers, titles, or values" >&2
  exit 1
fi
if reader_psql -c "SELECT object_name FROM gongmcp_crm_field_value_search('Opportunity', 'StageName', 'Proposal', 5, true, true)" >/tmp/gongctl-postgres-reader-crm-field-value-function-object-name.txt 2>&1; then
  echo "Postgres CRM field-value reader function exposed object_name column" >&2
  exit 1
fi

{
  printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"analyze_late_stage_crm_signals","arguments":{"object_type":"Opportunity","late_stage_values":["Proposal"],"limit":10}}}'
} | GONG_DATABASE_URL="$READER_URL" GONGMCP_TOOL_ALLOWLIST=analyze_late_stage_crm_signals go run ./cmd/gongmcp > /tmp/gongctl-postgres-late-stage-crm-signals.jsonl
grep -q '"analyze_late_stage_crm_signals"' /tmp/gongctl-postgres-late-stage-crm-signals.jsonl
grep -q 'late_calls.*1' /tmp/gongctl-postgres-late-stage-crm-signals.jsonl
grep -q 'field_name.*Type' /tmp/gongctl-postgres-late-stage-crm-signals.jsonl
assert_mcp_success /tmp/gongctl-postgres-late-stage-crm-signals.jsonl 3
if grep -q 'New Business\|synthetic-profile-call-001\|Profile lifecycle proposal review\|opp-profile-001\|acct-profile-001\|Profile Opportunity\|Profile Account\|field_value_text\|raw_json\|raw_sha256\|canonical_sha256\|Synthetic Postgres profile' /tmp/gongctl-postgres-late-stage-crm-signals.jsonl; then
  echo "Postgres late-stage CRM signals output exposed raw values, identifiers, profile details, or raw storage fields" >&2
  exit 1
fi
reader_psql -tAc "SELECT field_name || '|' || late_populated_calls || '|' || non_late_populated_calls FROM gongmcp_late_stage_signal_inventory('Opportunity', 'StageName', '[\"Proposal\"]', 10, false) ORDER BY field_name" >/tmp/gongctl-postgres-reader-late-stage-function-fields.txt
grep -q 'Type|1|0' /tmp/gongctl-postgres-reader-late-stage-function-fields.txt
if grep -q 'StageName\|Probability\|New Business\|synthetic-profile-call-001\|opp-profile-001\|Profile Opportunity' /tmp/gongctl-postgres-reader-late-stage-function-fields.txt; then
  echo "Postgres late-stage function returned proxy fields, raw values, or identifiers" >&2
  exit 1
fi
if reader_psql -c "SELECT field_value_text FROM gongmcp_late_stage_signal_inventory('Opportunity', 'StageName', '[\"Proposal\"]', 10, false)" >/tmp/gongctl-postgres-reader-late-stage-function-value-column.txt 2>&1; then
  echo "Postgres late-stage function exposed field_value_text column" >&2
  exit 1
fi
reader_psql -tAc "SELECT COUNT(*) FROM gongmcp_late_stage_stage_counts('Opportunity', 'Type', '[\"New Business\"]')" >/tmp/gongctl-postgres-reader-late-stage-custom-stage-denial.txt
grep -q '^0$' /tmp/gongctl-postgres-reader-late-stage-custom-stage-denial.txt
{
  printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"analyze_late_stage_crm_signals","arguments":{"object_type":"Opportunity","stage_field":"Type","late_stage_values":["New Business"],"limit":10}}}'
} | GONG_DATABASE_URL="$READER_URL" GONGMCP_TOOL_ALLOWLIST=analyze_late_stage_crm_signals go run ./cmd/gongmcp > /tmp/gongctl-postgres-late-stage-custom-stage-denial.jsonl
grep -q 'Opportunity.StageName only' /tmp/gongctl-postgres-late-stage-custom-stage-denial.jsonl
if grep -q 'New Business\|synthetic-profile-call-001\|Profile lifecycle proposal review\|opp-profile-001\|Profile Opportunity' /tmp/gongctl-postgres-late-stage-custom-stage-denial.jsonl /tmp/gongctl-postgres-reader-late-stage-custom-stage-denial.txt; then
  echo "Postgres late-stage custom stage denial exposed raw CRM values or identifiers" >&2
  exit 1
fi

{
  printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"opportunities_missing_transcripts","arguments":{"stage_values":["Proposal"],"limit":5}}}'
} | GONG_DATABASE_URL="$READER_URL" GONGMCP_TOOL_ALLOWLIST=opportunities_missing_transcripts go run ./cmd/gongmcp > /tmp/gongctl-postgres-opportunities-missing-transcripts.jsonl
grep -q '"opportunities_missing_transcripts"' /tmp/gongctl-postgres-opportunities-missing-transcripts.jsonl
grep -q 'missing_transcript_count.*1' /tmp/gongctl-postgres-opportunities-missing-transcripts.jsonl
grep -q 'stage.*Proposal' /tmp/gongctl-postgres-opportunities-missing-transcripts.jsonl
assert_mcp_success /tmp/gongctl-postgres-opportunities-missing-transcripts.jsonl 3
if grep -q 'synthetic-profile-call-001\|Profile lifecycle proposal review\|opp-profile-001\|Profile Opportunity\|field_value_text\|raw_json\|raw_sha256\|canonical_sha256\|Synthetic Postgres profile\|New Business' /tmp/gongctl-postgres-opportunities-missing-transcripts.jsonl; then
  echo "Postgres opportunities missing transcripts output exposed identifiers, raw values, profile details, or raw storage fields" >&2
  exit 1
fi
reader_psql -tAc "SELECT stage || '|' || call_count || '|' || missing_transcript_count || '|' || transcript_count || '|' || latest_call_at FROM gongmcp_opportunities_missing_transcripts('[\"Proposal\"]', 5)" >/tmp/gongctl-postgres-reader-opportunities-missing-transcripts.txt
grep -q 'Proposal|1|1|0|' /tmp/gongctl-postgres-reader-opportunities-missing-transcripts.txt
if grep -q 'synthetic-profile-call-001\|Profile lifecycle proposal review\|opp-profile-001\|Profile Opportunity\|New Business' /tmp/gongctl-postgres-reader-opportunities-missing-transcripts.txt; then
  echo "Postgres opportunities missing transcripts function exposed identifiers or raw values" >&2
  exit 1
fi
if reader_psql -c "SELECT object_id FROM gongmcp_opportunities_missing_transcripts('[\"Proposal\"]', 5)" >/tmp/gongctl-postgres-reader-opportunities-missing-object-id.txt 2>&1; then
  echo "Postgres opportunities missing transcripts function exposed object_id column" >&2
  exit 1
fi
if reader_psql -c "SELECT object_name FROM gongmcp_opportunities_missing_transcripts('[\"Proposal\"]', 5)" >/tmp/gongctl-postgres-reader-opportunities-missing-object-name.txt 2>&1; then
  echo "Postgres opportunities missing transcripts function exposed object_name column" >&2
  exit 1
fi
if reader_psql -c "SELECT latest_call_id FROM gongmcp_opportunities_missing_transcripts('[\"Proposal\"]', 5)" >/tmp/gongctl-postgres-reader-opportunities-missing-latest-call-id.txt 2>&1; then
  echo "Postgres opportunities missing transcripts function exposed latest_call_id column" >&2
  exit 1
fi
if reader_psql -c "SELECT owner_id FROM gongmcp_opportunities_missing_transcripts('[\"Proposal\"]', 5)" >/tmp/gongctl-postgres-reader-opportunities-missing-owner-id.txt 2>&1; then
  echo "Postgres opportunities missing transcripts function exposed owner_id column" >&2
  exit 1
fi
if reader_psql -c "SELECT amount FROM gongmcp_opportunities_missing_transcripts('[\"Proposal\"]', 5)" >/tmp/gongctl-postgres-reader-opportunities-missing-amount.txt 2>&1; then
  echo "Postgres opportunities missing transcripts function exposed amount column" >&2
  exit 1
fi
if reader_psql -c "SELECT close_date FROM gongmcp_opportunities_missing_transcripts('[\"Proposal\"]', 5)" >/tmp/gongctl-postgres-reader-opportunities-missing-close-date.txt 2>&1; then
  echo "Postgres opportunities missing transcripts function exposed close_date column" >&2
  exit 1
fi

{
  printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"opportunity_call_summary","arguments":{"stage_values":["Proposal"],"limit":5}}}'
} | GONG_DATABASE_URL="$READER_URL" GONGMCP_TOOL_ALLOWLIST=opportunity_call_summary go run ./cmd/gongmcp > /tmp/gongctl-postgres-opportunity-call-summary.jsonl
grep -q '"opportunity_call_summary"' /tmp/gongctl-postgres-opportunity-call-summary.jsonl
grep -q 'call_count.*1' /tmp/gongctl-postgres-opportunity-call-summary.jsonl
grep -q 'missing_transcript_count.*1' /tmp/gongctl-postgres-opportunity-call-summary.jsonl
grep -q 'stage.*Proposal' /tmp/gongctl-postgres-opportunity-call-summary.jsonl
assert_mcp_success /tmp/gongctl-postgres-opportunity-call-summary.jsonl 3
if grep -q 'synthetic-profile-call-001\|Profile lifecycle proposal review\|opp-profile-001\|Profile Opportunity\|owner-\|field_value_text\|raw_json\|raw_sha256\|canonical_sha256\|Synthetic Postgres profile\|New Business\|10000' /tmp/gongctl-postgres-opportunity-call-summary.jsonl; then
  echo "Postgres opportunity call summary output exposed identifiers, raw values, profile details, or raw storage fields" >&2
  exit 1
fi
reader_psql -tAc "SELECT stage || '|' || call_count || '|' || transcript_count || '|' || missing_transcript_count || '|' || total_duration_seconds || '|' || latest_call_at FROM gongmcp_opportunity_call_summary('[\"Proposal\"]', 5)" >/tmp/gongctl-postgres-reader-opportunity-call-summary.txt
grep -q 'Proposal|1|0|1|' /tmp/gongctl-postgres-reader-opportunity-call-summary.txt
if grep -q 'synthetic-profile-call-001\|Profile lifecycle proposal review\|opp-profile-001\|Profile Opportunity\|owner-\|New Business\|10000' /tmp/gongctl-postgres-reader-opportunity-call-summary.txt; then
  echo "Postgres opportunity call summary function exposed identifiers or raw values" >&2
  exit 1
fi
if reader_psql -c "SELECT object_id FROM gongmcp_opportunity_call_summary('[\"Proposal\"]', 5)" >/tmp/gongctl-postgres-reader-opportunity-summary-object-id.txt 2>&1; then
  echo "Postgres opportunity call summary function exposed object_id column" >&2
  exit 1
fi
if reader_psql -c "SELECT object_name FROM gongmcp_opportunity_call_summary('[\"Proposal\"]', 5)" >/tmp/gongctl-postgres-reader-opportunity-summary-object-name.txt 2>&1; then
  echo "Postgres opportunity call summary function exposed object_name column" >&2
  exit 1
fi
if reader_psql -c "SELECT latest_call_id FROM gongmcp_opportunity_call_summary('[\"Proposal\"]', 5)" >/tmp/gongctl-postgres-reader-opportunity-summary-latest-call-id.txt 2>&1; then
  echo "Postgres opportunity call summary function exposed latest_call_id column" >&2
  exit 1
fi
if reader_psql -c "SELECT owner_id FROM gongmcp_opportunity_call_summary('[\"Proposal\"]', 5)" >/tmp/gongctl-postgres-reader-opportunity-summary-owner-id.txt 2>&1; then
  echo "Postgres opportunity call summary function exposed owner_id column" >&2
  exit 1
fi
if reader_psql -c "SELECT amount FROM gongmcp_opportunity_call_summary('[\"Proposal\"]', 5)" >/tmp/gongctl-postgres-reader-opportunity-summary-amount.txt 2>&1; then
  echo "Postgres opportunity call summary function exposed amount column" >&2
  exit 1
fi
if reader_psql -c "SELECT close_date FROM gongmcp_opportunity_call_summary('[\"Proposal\"]', 5)" >/tmp/gongctl-postgres-reader-opportunity-summary-close-date.txt 2>&1; then
  echo "Postgres opportunity call summary function exposed close_date column" >&2
  exit 1
fi
if reader_psql -c "SELECT field_value_text FROM gongmcp_opportunity_call_summary('[\"Proposal\"]', 5)" >/tmp/gongctl-postgres-reader-opportunity-summary-field-value-text.txt 2>&1; then
  echo "Postgres opportunity call summary function exposed field_value_text column" >&2
  exit 1
fi

{
  printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"crm_field_population_matrix","arguments":{"object_type":"Opportunity","group_by_field":"StageName","limit":5}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"crm_field_population_matrix","arguments":{"object_type":"Opportunity","group_by_field":"OwnerId","limit":5}}}'
} | GONG_DATABASE_URL="$READER_URL" GONGMCP_TOOL_ALLOWLIST=crm_field_population_matrix go run ./cmd/gongmcp > /tmp/gongctl-postgres-crm-field-population-matrix.jsonl
grep -q '"crm_field_population_matrix"' /tmp/gongctl-postgres-crm-field-population-matrix.jsonl
grep -q 'group_by_field.*StageName' /tmp/gongctl-postgres-crm-field-population-matrix.jsonl
grep -q 'group_value.*Proposal' /tmp/gongctl-postgres-crm-field-population-matrix.jsonl
grep -q 'field_name.*Type' /tmp/gongctl-postgres-crm-field-population-matrix.jsonl
grep -q 'group_by_field.*OwnerId.*not allowed' /tmp/gongctl-postgres-crm-field-population-matrix.jsonl
assert_mcp_success /tmp/gongctl-postgres-crm-field-population-matrix.jsonl 3
if grep -q 'synthetic-profile-call-001\|Profile lifecycle proposal review\|opp-profile-001\|Profile Opportunity\|owner-\|field_value_text\|raw_json\|raw_sha256\|canonical_sha256\|Synthetic Postgres profile\|New Business\|10000' /tmp/gongctl-postgres-crm-field-population-matrix.jsonl; then
  echo "Postgres CRM field population matrix output exposed identifiers, raw values, profile details, or raw storage fields" >&2
  exit 1
fi
reader_psql -tAc "SELECT group_value || '|' || field_name || '|' || object_count || '|' || call_count || '|' || populated_count FROM gongmcp_crm_field_population_matrix('Opportunity', 'StageName', 5)" >/tmp/gongctl-postgres-reader-crm-field-population-matrix.txt
grep -q 'Proposal|Type|1|1|1' /tmp/gongctl-postgres-reader-crm-field-population-matrix.txt
if grep -q 'synthetic-profile-call-001\|Profile lifecycle proposal review\|opp-profile-001\|Profile Opportunity\|owner-\|New Business\|10000' /tmp/gongctl-postgres-reader-crm-field-population-matrix.txt; then
  echo "Postgres CRM field population matrix function exposed identifiers or raw values" >&2
  exit 1
fi
if reader_psql -c "SELECT COUNT(*) FROM gongmcp_crm_field_population_matrix('Opportunity', 'OwnerId', 5)" >/tmp/gongctl-postgres-reader-crm-field-population-matrix-unsafe-group.txt 2>&1; then
  echo "Postgres CRM field population matrix function accepted unsafe group_by_field" >&2
  exit 1
fi
grep -q 'not allowed for MCP-safe aggregate grouping' /tmp/gongctl-postgres-reader-crm-field-population-matrix-unsafe-group.txt
if reader_psql -c "SELECT object_id FROM gongmcp_crm_field_population_matrix('Opportunity', 'StageName', 5)" >/tmp/gongctl-postgres-reader-crm-field-population-matrix-object-id.txt 2>&1; then
  echo "Postgres CRM field population matrix function exposed object_id column" >&2
  exit 1
fi
if reader_psql -c "SELECT object_name FROM gongmcp_crm_field_population_matrix('Opportunity', 'StageName', 5)" >/tmp/gongctl-postgres-reader-crm-field-population-matrix-object-name.txt 2>&1; then
  echo "Postgres CRM field population matrix function exposed object_name column" >&2
  exit 1
fi
if reader_psql -c "SELECT object_key FROM gongmcp_crm_field_population_matrix('Opportunity', 'StageName', 5)" >/tmp/gongctl-postgres-reader-crm-field-population-matrix-object-key.txt 2>&1; then
  echo "Postgres CRM field population matrix function exposed object_key column" >&2
  exit 1
fi
if reader_psql -c "SELECT call_id FROM gongmcp_crm_field_population_matrix('Opportunity', 'StageName', 5)" >/tmp/gongctl-postgres-reader-crm-field-population-matrix-call-id.txt 2>&1; then
  echo "Postgres CRM field population matrix function exposed call_id column" >&2
  exit 1
fi
if reader_psql -c "SELECT field_value_text FROM gongmcp_crm_field_population_matrix('Opportunity', 'StageName', 5)" >/tmp/gongctl-postgres-reader-crm-field-population-matrix-field-value-text.txt 2>&1; then
  echo "Postgres CRM field population matrix function exposed field_value_text column" >&2
  exit 1
fi
if reader_psql -c "SELECT raw_json FROM gongmcp_crm_field_population_matrix('Opportunity', 'StageName', 5)" >/tmp/gongctl-postgres-reader-crm-field-population-matrix-raw-json.txt 2>&1; then
  echo "Postgres CRM field population matrix function exposed raw_json column" >&2
  exit 1
fi
if reader_psql -c "SELECT raw_sha256 FROM gongmcp_crm_field_population_matrix('Opportunity', 'StageName', 5)" >/tmp/gongctl-postgres-reader-crm-field-population-matrix-raw-sha256.txt 2>&1; then
  echo "Postgres CRM field population matrix function exposed raw_sha256 column" >&2
  exit 1
fi

docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 -c "DELETE FROM calls WHERE call_id IN ('synthetic-lifecycle-crm-001','synthetic-lifecycle-crm-002','synthetic-lifecycle-crm-003'); INSERT INTO calls(call_id, title, started_at, duration_seconds, parties_count, context_present, raw_json, raw_sha256, first_seen_at, updated_at) VALUES('synthetic-lifecycle-crm-001', 'Lifecycle CRM renewal process', '2026-02-15T15:00:00Z', 1800, 2, true, '{\"id\":\"synthetic-lifecycle-crm-001\",\"title\":\"Lifecycle CRM renewal process\",\"started\":\"2026-02-15T15:00:00Z\",\"duration\":1800,\"metaData\":{\"scope\":\"External\",\"system\":\"Zoom\",\"direction\":\"Conference\"},\"context\":{\"crmObjects\":[{\"type\":\"Opportunity\",\"id\":\"opp-lifecycle-crm-001\",\"name\":\"Lifecycle Renewal One\",\"fields\":{\"StageName\":\"Renewal Discussion\",\"Type\":\"Renewal\",\"Renewal_Process__c\":\"Formal\",\"Procurement_System__c\":\"Coupa\"}}]}}'::jsonb, 'synthetic-lifecycle-crm-sha-001', now()::text, now()::text),('synthetic-lifecycle-crm-002', 'Lifecycle CRM renewal blank process', '2026-02-16T15:00:00Z', 1200, 2, true, '{\"id\":\"synthetic-lifecycle-crm-002\",\"title\":\"Lifecycle CRM renewal blank process\",\"started\":\"2026-02-16T15:00:00Z\",\"duration\":1200,\"metaData\":{\"scope\":\"External\",\"system\":\"Zoom\",\"direction\":\"Conference\"},\"context\":{\"crmObjects\":[{\"type\":\"Opportunity\",\"id\":\"opp-lifecycle-crm-002\",\"name\":\"Lifecycle Renewal Two\",\"fields\":{\"StageName\":\"Renewal Discussion\",\"Type\":\"Renewal\",\"Renewal_Process__c\":\"\",\"Procurement_System__c\":\"Ariba\"}}]}}'::jsonb, 'synthetic-lifecycle-crm-sha-002', now()::text, now()::text),('synthetic-lifecycle-crm-003', 'Lifecycle CRM active pipeline', '2026-02-17T15:00:00Z', 1500, 2, true, '{\"id\":\"synthetic-lifecycle-crm-003\",\"title\":\"Lifecycle CRM active pipeline\",\"started\":\"2026-02-17T15:00:00Z\",\"duration\":1500,\"metaData\":{\"scope\":\"External\",\"system\":\"Zoom\",\"direction\":\"Conference\"},\"context\":{\"crmObjects\":[{\"type\":\"Opportunity\",\"id\":\"opp-lifecycle-crm-003\",\"name\":\"Lifecycle Pipeline\",\"fields\":{\"StageName\":\"Discovery\",\"Type\":\"New Business\",\"Renewal_Process__c\":\"\",\"Procurement_System__c\":\"Coupa\"}}]}}'::jsonb, 'synthetic-lifecycle-crm-sha-003', now()::text, now()::text)" >/dev/null
GONG_DATABASE_URL="$WRITER_URL" go run ./cmd/gongctl sync read-model --rebuild >/tmp/gongctl-postgres-lifecycle-crm-fixture-read-model.json
grep -q '"status": "rebuilt"' /tmp/gongctl-postgres-lifecycle-crm-fixture-read-model.json
{
  printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"compare_lifecycle_crm_fields","arguments":{"bucket_a":"renewal","bucket_b":"active_sales_pipeline","object_type":"Opportunity","limit":5}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"compare_lifecycle_crm_fields","arguments":{"bucket_a":"renewal","bucket_b":"renewal","object_type":"Opportunity","limit":5}}}'
} | GONG_DATABASE_URL="$READER_URL" GONGMCP_TOOL_ALLOWLIST=compare_lifecycle_crm_fields go run ./cmd/gongmcp > /tmp/gongctl-postgres-compare-lifecycle-crm-fields.jsonl
grep -q '"compare_lifecycle_crm_fields"' /tmp/gongctl-postgres-compare-lifecycle-crm-fields.jsonl
grep -q 'bucket_a.*renewal' /tmp/gongctl-postgres-compare-lifecycle-crm-fields.jsonl
grep -q 'bucket_b.*active_sales_pipeline' /tmp/gongctl-postgres-compare-lifecycle-crm-fields.jsonl
grep -q 'field_name.*Renewal_Process__c' /tmp/gongctl-postgres-compare-lifecycle-crm-fields.jsonl
grep -q 'bucket_a.*bucket_b must be different' /tmp/gongctl-postgres-compare-lifecycle-crm-fields.jsonl
assert_mcp_success /tmp/gongctl-postgres-compare-lifecycle-crm-fields.jsonl 3
if grep -q 'synthetic-lifecycle-crm\|Lifecycle CRM\|opp-lifecycle-crm\|Lifecycle Renewal\|Lifecycle Pipeline\|Coupa\|Ariba\|New Business\|Formal\|field_value_text\|raw_json\|raw_sha256\|canonical_sha256' /tmp/gongctl-postgres-compare-lifecycle-crm-fields.jsonl; then
  echo "Postgres lifecycle CRM comparison output exposed identifiers, raw values, or raw storage fields" >&2
  exit 1
fi
reader_psql -tAc "SELECT object_type || '|' || field_name || '|' || bucket_a_call_count || '|' || bucket_b_call_count || '|' || bucket_a_populated || '|' || bucket_b_populated FROM gongmcp_compare_lifecycle_crm_fields('renewal', 'active_sales_pipeline', 'Opportunity', 5)" >/tmp/gongctl-postgres-reader-compare-lifecycle-crm-fields.txt
grep -q 'Opportunity|Renewal_Process__c|2|2|1|0' /tmp/gongctl-postgres-reader-compare-lifecycle-crm-fields.txt
if grep -q 'synthetic-lifecycle-crm\|Lifecycle CRM\|opp-lifecycle-crm\|Lifecycle Renewal\|Lifecycle Pipeline\|Coupa\|Ariba\|New Business\|Formal' /tmp/gongctl-postgres-reader-compare-lifecycle-crm-fields.txt; then
  echo "Postgres lifecycle CRM comparison function exposed identifiers or raw values" >&2
  exit 1
fi
for column in call_id title object_id object_name object_key field_value_text raw_json raw_sha256 text; do
  if reader_psql -c "SELECT $column FROM gongmcp_compare_lifecycle_crm_fields('renewal', 'active_sales_pipeline', 'Opportunity', 5)" >/tmp/gongctl-postgres-reader-compare-lifecycle-crm-fields-"$column".txt 2>&1; then
    echo "Postgres lifecycle CRM comparison function exposed $column column" >&2
    exit 1
  fi
done
if reader_psql -c "SELECT COUNT(*) FROM gongmcp_compare_lifecycle_crm_fields('renewal', 'not-real', 'Opportunity', 5)" >/tmp/gongctl-postgres-reader-compare-lifecycle-crm-fields-bad-bucket.txt 2>&1; then
  echo "Postgres lifecycle CRM comparison function accepted bad lifecycle bucket" >&2
  exit 1
fi
grep -q 'unknown lifecycle bucket' /tmp/gongctl-postgres-reader-compare-lifecycle-crm-fields-bad-bucket.txt
if reader_psql -c "SELECT COUNT(*) FROM gongmcp_compare_lifecycle_crm_fields('renewal', 'active_sales_pipeline', 'Account', 5)" >/tmp/gongctl-postgres-reader-compare-lifecycle-crm-fields-bad-object.txt 2>&1; then
  echo "Postgres lifecycle CRM comparison function accepted unsafe object_type" >&2
  exit 1
fi
grep -q 'not allowed for MCP-safe lifecycle CRM field comparison' /tmp/gongctl-postgres-reader-compare-lifecycle-crm-fields-bad-object.txt
if reader_psql -c "SELECT COUNT(*) FROM gongmcp_compare_lifecycle_crm_fields(NULL, 'active_sales_pipeline', 'Opportunity', 5)" >/tmp/gongctl-postgres-reader-compare-lifecycle-crm-fields-null-arg.txt 2>&1; then
  echo "Postgres lifecycle CRM comparison function accepted NULL bucket" >&2
  exit 1
fi
grep -q 'bucket_a and bucket_b are required' /tmp/gongctl-postgres-reader-compare-lifecycle-crm-fields-null-arg.txt

docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 -c "DELETE FROM transcript_segments WHERE call_id IN ('synthetic-missing-filter-001','synthetic-missing-filter-002','synthetic-missing-filter-003'); DELETE FROM transcripts WHERE call_id IN ('synthetic-missing-filter-001','synthetic-missing-filter-002','synthetic-missing-filter-003'); DELETE FROM calls WHERE call_id IN ('synthetic-missing-filter-001','synthetic-missing-filter-002','synthetic-missing-filter-003'); INSERT INTO calls(call_id, title, started_at, duration_seconds, parties_count, context_present, raw_json, raw_sha256, first_seen_at, updated_at) VALUES('synthetic-missing-filter-001', 'Missing renewal external smoke', '2026-02-20T15:00:00Z', 1800, 2, true, '{\"id\":\"synthetic-missing-filter-001\",\"title\":\"Missing renewal external smoke\",\"started\":\"2026-02-20T15:00:00Z\",\"duration\":1800,\"metaData\":{\"scope\":\"External\",\"system\":\"Zoom\",\"direction\":\"Conference\"},\"context\":{\"crmObjects\":[{\"type\":\"Opportunity\",\"id\":\"opp-missing-filter-smoke-001\",\"name\":\"Missing Filter Opportunity\",\"fields\":{\"StageName\":\"Renewal Discussion\",\"Type\":\"Renewal\"}}]}}'::jsonb, 'synthetic-missing-filter-sha-001', now()::text, now()::text),('synthetic-missing-filter-002', 'Missing active internal smoke', '2026-02-21T15:00:00Z', 1200, 2, true, '{\"id\":\"synthetic-missing-filter-002\",\"title\":\"Missing active internal smoke\",\"started\":\"2026-02-21T15:00:00Z\",\"duration\":1200,\"metaData\":{\"scope\":\"Internal\",\"system\":\"Upload API\",\"direction\":\"Inbound\"},\"context\":{\"crmObjects\":[{\"type\":\"Opportunity\",\"id\":\"opp-missing-filter-smoke-002\",\"name\":\"Missing Filter Active Opportunity\",\"fields\":{\"StageName\":\"Discovery\",\"Type\":\"New Business\"}}]}}'::jsonb, 'synthetic-missing-filter-sha-002', now()::text, now()::text),('synthetic-missing-filter-003', 'Present renewal external smoke', '2026-02-22T15:00:00Z', 1500, 2, true, '{\"id\":\"synthetic-missing-filter-003\",\"title\":\"Present renewal external smoke\",\"started\":\"2026-02-22T15:00:00Z\",\"duration\":1500,\"metaData\":{\"scope\":\"External\",\"system\":\"Zoom\",\"direction\":\"Conference\"},\"context\":{\"crmObjects\":[{\"type\":\"Opportunity\",\"id\":\"opp-missing-filter-smoke-003\",\"name\":\"Present Filter Opportunity\",\"fields\":{\"StageName\":\"Renewal Discussion\",\"Type\":\"Renewal\"}}]}}'::jsonb, 'synthetic-missing-filter-sha-003', now()::text, now()::text); INSERT INTO transcripts(call_id, raw_json, raw_sha256, segment_count, first_seen_at, updated_at) VALUES('synthetic-missing-filter-003', '{\"callId\":\"synthetic-missing-filter-003\"}'::jsonb, 'synthetic-missing-filter-transcript-sha-003', 1, now()::text, now()::text); INSERT INTO transcript_segments(call_id, segment_index, speaker_id, start_ms, end_ms, text, raw_json) VALUES('synthetic-missing-filter-003', 0, 'speaker-missing-filter-present', 0, 1000, 'present transcript for missing filter smoke', '{\"speakerId\":\"speaker-missing-filter-present\"}'::jsonb)" >/dev/null
GONG_DATABASE_URL="$WRITER_URL" go run ./cmd/gongctl sync read-model --rebuild >/tmp/gongctl-postgres-missing-transcripts-fixture-read-model.json
grep -q '"status": "rebuilt"' /tmp/gongctl-postgres-missing-transcripts-fixture-read-model.json
{
  printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"missing_transcripts","arguments":{"from_date":"2026-02-20","to_date":"2026-02-20","lifecycle_bucket":"renewal","scope":"External","system":"Zoom","direction":"Conference","crm_object_type":"Opportunity","crm_object_id":"opp-missing-filter-smoke-001","limit":5}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"missing_transcripts","arguments":{"lifecycle_bucket":"not-real","limit":5}}}'
} | GONG_DATABASE_URL="$READER_URL" GONGMCP_TOOL_ALLOWLIST=missing_transcripts go run ./cmd/gongmcp > /tmp/gongctl-postgres-missing-transcripts-filtered.jsonl
grep -q '"missing_transcripts"' /tmp/gongctl-postgres-missing-transcripts-filtered.jsonl
grep -q 'synthetic-missing-filter-001' /tmp/gongctl-postgres-missing-transcripts-filtered.jsonl
grep -q 'Missing renewal external smoke' /tmp/gongctl-postgres-missing-transcripts-filtered.jsonl
grep -q 'unknown lifecycle bucket' /tmp/gongctl-postgres-missing-transcripts-filtered.jsonl
assert_mcp_success /tmp/gongctl-postgres-missing-transcripts-filtered.jsonl 3
if grep -q 'synthetic-missing-filter-002\|synthetic-missing-filter-003\|opp-missing-filter-smoke\|field_value_text\|raw_json\|raw_sha256\|present transcript' /tmp/gongctl-postgres-missing-transcripts-filtered.jsonl; then
  echo "Postgres missing_transcripts filtered MCP output exposed nonmatching calls, raw CRM identifiers, raw storage fields, or transcript text" >&2
  exit 1
fi
reader_psql -tAc "SELECT call_id || '|' || title || '|' || started_at FROM calls WHERE call_id = 'synthetic-missing-filter-001'" >/tmp/gongctl-postgres-reader-missing-transcripts-record-reference.txt
grep -q 'synthetic-missing-filter-001|Missing renewal external smoke|2026-02-20T15:00:00Z' /tmp/gongctl-postgres-reader-missing-transcripts-record-reference.txt

docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 -c "REVOKE EXECUTE ON FUNCTION gongmcp_missing_transcripts(text, text, text, text, text, text, text, text, integer) FROM gongmcp_reader" >/dev/null
if GONG_DATABASE_URL="$READER_URL" GONGMCP_TOOL_ALLOWLIST=missing_transcripts go run ./cmd/gongmcp </dev/null >/tmp/gongctl-postgres-reader-missing-transcripts-function-grant-drift.txt 2>&1; then
  echo "read-only MCP unexpectedly started without missing_transcripts function grant" >&2
  exit 1
fi
grep -q 'missing required function EXECUTE grants' /tmp/gongctl-postgres-reader-missing-transcripts-function-grant-drift.txt
grep -q 'gongmcp_missing_transcripts' /tmp/gongctl-postgres-reader-missing-transcripts-function-grant-drift.txt
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 -c "GRANT EXECUTE ON FUNCTION gongmcp_missing_transcripts(text, text, text, text, text, text, text, text, integer) TO gongmcp_reader; GRANT EXECUTE ON FUNCTION gongmcp_missing_transcript_count() TO gongmcp_reader" >/tmp/gongctl-postgres-reader-missing-transcripts-function-grant-drift-repaired.txt

docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 -c "REVOKE EXECUTE ON FUNCTION gongmcp_opportunities_missing_transcripts(text, integer) FROM gongmcp_reader" >/dev/null
if GONG_DATABASE_URL="$READER_URL" GONGMCP_TOOL_ALLOWLIST=opportunities_missing_transcripts go run ./cmd/gongmcp </dev/null >/tmp/gongctl-postgres-reader-opportunities-missing-function-grant-drift.txt 2>&1; then
  echo "read-only MCP unexpectedly started without opportunities_missing_transcripts function grant" >&2
  exit 1
fi
grep -q 'missing required function EXECUTE grants' /tmp/gongctl-postgres-reader-opportunities-missing-function-grant-drift.txt
grep -q 'gongmcp_opportunities_missing_transcripts' /tmp/gongctl-postgres-reader-opportunities-missing-function-grant-drift.txt
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 -c "GRANT EXECUTE ON FUNCTION gongmcp_opportunities_missing_transcripts(text, integer) TO gongmcp_reader" >/tmp/gongctl-postgres-reader-opportunities-missing-function-grant-drift-repaired.txt
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 -c "REVOKE EXECUTE ON FUNCTION gongmcp_opportunity_call_summary(text, integer) FROM gongmcp_reader" >/dev/null
if GONG_DATABASE_URL="$READER_URL" GONGMCP_TOOL_ALLOWLIST=opportunity_call_summary go run ./cmd/gongmcp </dev/null >/tmp/gongctl-postgres-reader-opportunity-summary-function-grant-drift.txt 2>&1; then
  echo "read-only MCP unexpectedly started without opportunity_call_summary function grant" >&2
  exit 1
fi
grep -q 'missing required function EXECUTE grants' /tmp/gongctl-postgres-reader-opportunity-summary-function-grant-drift.txt
grep -q 'gongmcp_opportunity_call_summary' /tmp/gongctl-postgres-reader-opportunity-summary-function-grant-drift.txt
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 -c "GRANT EXECUTE ON FUNCTION gongmcp_opportunity_call_summary(text, integer) TO gongmcp_reader" >/tmp/gongctl-postgres-reader-opportunity-summary-function-grant-drift-repaired.txt
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 -c "REVOKE EXECUTE ON FUNCTION gongmcp_crm_field_population_matrix(text, text, integer) FROM gongmcp_reader" >/dev/null
if GONG_DATABASE_URL="$READER_URL" GONGMCP_TOOL_ALLOWLIST=crm_field_population_matrix go run ./cmd/gongmcp </dev/null >/tmp/gongctl-postgres-reader-crm-field-population-matrix-function-grant-drift.txt 2>&1; then
  echo "read-only MCP unexpectedly started without crm_field_population_matrix function grant" >&2
  exit 1
fi
grep -q 'missing required function EXECUTE grants' /tmp/gongctl-postgres-reader-crm-field-population-matrix-function-grant-drift.txt
grep -q 'gongmcp_crm_field_population_matrix' /tmp/gongctl-postgres-reader-crm-field-population-matrix-function-grant-drift.txt
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 -c "GRANT EXECUTE ON FUNCTION gongmcp_crm_field_population_matrix(text, text, integer) TO gongmcp_reader" >/tmp/gongctl-postgres-reader-crm-field-population-matrix-function-grant-drift-repaired.txt
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 -c "REVOKE EXECUTE ON FUNCTION gongmcp_compare_lifecycle_crm_fields(text, text, text, integer) FROM gongmcp_reader" >/dev/null
if GONG_DATABASE_URL="$READER_URL" GONGMCP_TOOL_ALLOWLIST=compare_lifecycle_crm_fields go run ./cmd/gongmcp </dev/null >/tmp/gongctl-postgres-reader-compare-lifecycle-crm-fields-function-grant-drift.txt 2>&1; then
  echo "read-only MCP unexpectedly started without compare_lifecycle_crm_fields function grant" >&2
  exit 1
fi
grep -q 'missing required function EXECUTE grants' /tmp/gongctl-postgres-reader-compare-lifecycle-crm-fields-function-grant-drift.txt
grep -q 'gongmcp_compare_lifecycle_crm_fields' /tmp/gongctl-postgres-reader-compare-lifecycle-crm-fields-function-grant-drift.txt
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 -c "GRANT EXECUTE ON FUNCTION gongmcp_compare_lifecycle_crm_fields(text, text, text, integer) TO gongmcp_reader" >/tmp/gongctl-postgres-reader-compare-lifecycle-crm-fields-function-grant-drift-repaired.txt
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 -c "REVOKE EXECUTE ON FUNCTION gongmcp_search_transcript_segments_by_crm_context(text, text, text, integer) FROM gongmcp_reader" >/dev/null
if GONG_DATABASE_URL="$READER_URL" GONGMCP_TOOL_ALLOWLIST=search_transcripts_by_crm_context go run ./cmd/gongmcp </dev/null >/tmp/gongctl-postgres-reader-search-transcripts-by-crm-context-function-grant-drift.txt 2>&1; then
  echo "read-only MCP unexpectedly started without search_transcripts_by_crm_context function grant" >&2
  exit 1
fi
grep -q 'missing required function EXECUTE grants' /tmp/gongctl-postgres-reader-search-transcripts-by-crm-context-function-grant-drift.txt
grep -q 'gongmcp_search_transcript_segments_by_crm_context' /tmp/gongctl-postgres-reader-search-transcripts-by-crm-context-function-grant-drift.txt
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 -c "GRANT EXECUTE ON FUNCTION gongmcp_search_transcript_segments_by_crm_context(text, text, text, integer) TO gongmcp_reader" >/tmp/gongctl-postgres-reader-search-transcripts-by-crm-context-function-grant-drift-repaired.txt
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 -c "GRANT EXECUTE ON FUNCTION gongmcp_opportunities_missing_transcripts(text, integer) TO PUBLIC; GRANT EXECUTE ON FUNCTION gongmcp_opportunity_call_summary(text, integer) TO PUBLIC; GRANT EXECUTE ON FUNCTION gongmcp_crm_field_population_matrix(text, text, integer) TO PUBLIC; GRANT EXECUTE ON FUNCTION gongmcp_compare_lifecycle_crm_fields(text, text, text, integer) TO PUBLIC; GRANT EXECUTE ON FUNCTION gongmcp_search_transcript_segments_by_crm_context(text, text, text, integer) TO PUBLIC; GRANT EXECUTE ON FUNCTION gongmcp_missing_transcripts(text, text, text, text, text, text, text, text, integer) TO PUBLIC; GRANT EXECUTE ON FUNCTION gongmcp_missing_transcript_count() TO PUBLIC; GRANT EXECUTE ON FUNCTION gongmcp_crm_object_type_summary() TO PUBLIC" >/dev/null
if env GONG_DATABASE_URL="$READER_URL" GONGMCP_TOOL_ALLOWLIST="opportunities_missing_transcripts,missing_transcripts" go run ./cmd/gongmcp </dev/null >/tmp/gongctl-postgres-reader-function-public-drift.txt 2>&1; then
  echo "read-only MCP unexpectedly started with public function EXECUTE drift" >&2
  exit 1
fi
grep -Eq 'over-broad function EXECUTE grants|extra function EXECUTE grants outside selected MCP tools' /tmp/gongctl-postgres-reader-function-public-drift.txt
grep -q 'gongmcp_opportunities_missing_transcripts' /tmp/gongctl-postgres-reader-function-public-drift.txt
grep -q 'gongmcp_opportunity_call_summary' /tmp/gongctl-postgres-reader-function-public-drift.txt
grep -q 'gongmcp_crm_field_population_matrix' /tmp/gongctl-postgres-reader-function-public-drift.txt
grep -q 'gongmcp_compare_lifecycle_crm_fields' /tmp/gongctl-postgres-reader-function-public-drift.txt
grep -q 'gongmcp_search_transcript_segments_by_crm_context' /tmp/gongctl-postgres-reader-function-public-drift.txt
grep -q 'gongmcp_missing_transcripts' /tmp/gongctl-postgres-reader-function-public-drift.txt
grep -q 'gongmcp_missing_transcript_count' /tmp/gongctl-postgres-reader-function-public-drift.txt
grep -q 'gongmcp_crm_object_type_summary' /tmp/gongctl-postgres-reader-function-public-drift.txt
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 -c "REVOKE EXECUTE ON FUNCTION gongmcp_opportunities_missing_transcripts(text, integer) FROM PUBLIC; REVOKE EXECUTE ON FUNCTION gongmcp_opportunity_call_summary(text, integer) FROM PUBLIC; REVOKE EXECUTE ON FUNCTION gongmcp_crm_field_population_matrix(text, text, integer) FROM PUBLIC; REVOKE EXECUTE ON FUNCTION gongmcp_compare_lifecycle_crm_fields(text, text, text, integer) FROM PUBLIC; REVOKE EXECUTE ON FUNCTION gongmcp_search_transcript_segments_by_crm_context(text, text, text, integer) FROM PUBLIC; REVOKE EXECUTE ON FUNCTION gongmcp_missing_transcripts(text, text, text, text, text, text, text, text, integer) FROM PUBLIC; REVOKE EXECUTE ON FUNCTION gongmcp_missing_transcript_count() FROM PUBLIC; REVOKE EXECUTE ON FUNCTION gongmcp_crm_object_type_summary() FROM PUBLIC; GRANT EXECUTE ON FUNCTION gongmcp_opportunities_missing_transcripts(text, integer) TO gongmcp_reader; GRANT EXECUTE ON FUNCTION gongmcp_opportunity_call_summary(text, integer) TO gongmcp_reader; GRANT EXECUTE ON FUNCTION gongmcp_crm_field_population_matrix(text, text, integer) TO gongmcp_reader; GRANT EXECUTE ON FUNCTION gongmcp_compare_lifecycle_crm_fields(text, text, text, integer) TO gongmcp_reader; GRANT EXECUTE ON FUNCTION gongmcp_search_transcript_segments_by_crm_context(text, text, text, integer) TO gongmcp_reader; GRANT EXECUTE ON FUNCTION gongmcp_missing_transcripts(text, text, text, text, text, text, text, text, integer) TO gongmcp_reader; GRANT EXECUTE ON FUNCTION gongmcp_missing_transcript_count() TO gongmcp_reader; GRANT EXECUTE ON FUNCTION gongmcp_crm_object_type_summary() TO gongmcp_reader; GRANT EXECUTE ON FUNCTION gongmcp_call_ai_highlights_count() TO gongmcp_reader" >/tmp/gongctl-postgres-reader-function-public-drift-repaired.txt
analyst_generated_sql="/tmp/gongctl-postgres-analyst-reader-grants.sql"
GONG_DATABASE_URL="$WRITER_URL" go run ./cmd/gongctl mcp postgres-reader-sql --preset analyst --role gongmcp_analyst_reader --database gongctl > "$analyst_generated_sql"
for required_grant in \
  'GRANT EXECUTE ON FUNCTION public.gongmcp_business_analysis_calls_sanitized' \
  'GRANT EXECUTE ON FUNCTION public.gongmcp_business_analysis_evidence_sanitized' \
  'GRANT EXECUTE ON FUNCTION public.gongmcp_search_transcript_quotes_with_attribution_sanitized' \
  'GRANT EXECUTE ON FUNCTION public.gongmcp_search_transcript_segments_sanitized' \
  'GRANT EXECUTE ON FUNCTION public.gongmcp_search_transcript_segments_by_call_facts_sanitized' \
  'GRANT EXECUTE ON FUNCTION public.gongmcp_crm_field_summary_sanitized' \
  'GRANT EXECUTE ON FUNCTION public.gongmcp_business_analysis_dimension'
do
  if ! grep -q "$required_grant" "$analyst_generated_sql"; then
    echo "generated analyst reader grant SQL missing required grant marker $required_grant: $analyst_generated_sql" >&2
    exit 1
  fi
done
for forbidden_grant in \
  'GRANT EXECUTE ON FUNCTION public.gongmcp_business_analysis_calls(text' \
  'GRANT EXECUTE ON FUNCTION public.gongmcp_business_analysis_evidence(text' \
  'GRANT EXECUTE ON FUNCTION public.gongmcp_search_transcript_quotes_with_attribution(text' \
  'GRANT EXECUTE ON FUNCTION public.gongmcp_search_transcript_segments(text' \
  'GRANT EXECUTE ON FUNCTION public.gongmcp_search_transcript_segments_by_call_facts(text'
do
  if grep -q "$forbidden_grant" "$analyst_generated_sql"; then
    echo "generated analyst reader grant SQL unexpectedly grants raw identifier-bearing helper $forbidden_grant: $analyst_generated_sql" >&2
    exit 1
  fi
done
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T -e GONGMCP_ANALYST_READER_PASSWORD="$GONGMCP_ANALYST_READER_PASSWORD" postgres sh -s >/tmp/gongctl-postgres-analyst-reader-create.txt 2>&1 <<'SH'
set -eu
psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 -v reader_password="$GONGMCP_ANALYST_READER_PASSWORD" <<'SQL'
DROP ROLE IF EXISTS gongmcp_analyst_reader;
CREATE ROLE gongmcp_analyst_reader LOGIN NOINHERIT PASSWORD :'reader_password';
SQL
SH
GONG_DATABASE_URL="$WRITER_URL" go run ./cmd/gongctl mcp postgres-reader-apply --preset analyst --role gongmcp_analyst_reader --database gongctl --apply >/tmp/gongctl-postgres-analyst-reader-apply.json
grep -q '"status": "applied"' /tmp/gongctl-postgres-analyst-reader-apply.json
if grep -q 'postgres://\|PASSWORD\|GONG_DATABASE_URL' /tmp/gongctl-postgres-analyst-reader-apply.json; then
  echo "analyst reader apply JSON unexpectedly contains secret or connection-url marker" >&2
  exit 1
fi
for secret_value in "$GONGCTL_POSTGRES_PASSWORD" "$GONGMCP_READER_PASSWORD" "$GONGMCP_BUSINESS_PILOT_READER_PASSWORD" "$GONGMCP_ANALYST_READER_PASSWORD" "$GONGMCP_FUNCTION_FREE_READER_PASSWORD"; do
  if [ -n "$secret_value" ] && grep -Fq "$secret_value" /tmp/gongctl-postgres-analyst-reader-apply.json; then
    echo "analyst reader apply JSON unexpectedly contains a configured secret value" >&2
    exit 1
  fi
done
if analyst_reader_psql -c "SELECT call_id FROM calls LIMIT 1" >/tmp/gongctl-postgres-analyst-reader-calls-call-id-denied.txt 2>&1; then
  echo "analyst scoped reader unexpectedly read calls.call_id directly" >&2
  exit 1
fi
grep -q 'permission denied' /tmp/gongctl-postgres-analyst-reader-calls-call-id-denied.txt
if analyst_reader_psql -c "SELECT * FROM gongmcp_business_analysis_calls('', 'shared Postgres', '', '', '', '', '', '', '', '', '', '', '', '', '', '[]', false, 1, '[]')" >/tmp/gongctl-postgres-analyst-reader-raw-business-calls-denied.txt 2>&1; then
  echo "analyst scoped reader unexpectedly executed raw business-analysis call helper" >&2
  exit 1
fi
grep -q 'permission denied' /tmp/gongctl-postgres-analyst-reader-raw-business-calls-denied.txt
if analyst_reader_psql -c "SELECT * FROM gongmcp_search_transcript_segments('shared Postgres', 1)" >/tmp/gongctl-postgres-analyst-reader-raw-transcript-search-denied.txt 2>&1; then
  echo "analyst scoped reader unexpectedly executed raw transcript search helper" >&2
  exit 1
fi
grep -q 'permission denied' /tmp/gongctl-postgres-analyst-reader-raw-transcript-search-denied.txt
analyst_reader_psql -tA -c "SELECT row_to_json(r)::text FROM gongmcp_business_analysis_calls_sanitized('', 'shared Postgres', '', '', '', '', '', '', '', '', '', '', '', '', '', '[]', false, 1, '[]') r" >/tmp/gongctl-postgres-analyst-reader-business-calls-sanitized.json
if grep -q 'synthetic-call-001\|CustomerA\|Shared Postgres pilot kickoff' /tmp/gongctl-postgres-analyst-reader-business-calls-sanitized.json; then
  echo "analyst scoped reader sanitized business-analysis helper exposed raw call identifier or title" >&2
  exit 1
fi
analyst_reader_psql -tA -c "SELECT row_to_json(r)::text FROM gongmcp_search_transcript_segments_sanitized('shared Postgres', 1) r" >/tmp/gongctl-postgres-analyst-reader-transcript-search-sanitized.json
if grep -q 'synthetic-call-001\|speaker-1\|We need a shared Postgres deployment path' /tmp/gongctl-postgres-analyst-reader-transcript-search-sanitized.json; then
  echo "analyst scoped reader sanitized transcript search helper exposed raw call/speaker identifier or full transcript text" >&2
  exit 1
fi
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 >/tmp/gongctl-postgres-analyst-loss-reason-fixture.txt <<'SQL'
INSERT INTO call_context_objects(call_id, object_key, object_type, object_id, object_name, raw_json)
VALUES ('synthetic-call-001', 'Opportunity:opp-crm-context-private', 'Opportunity', 'opp-crm-context-private', 'CRM Context Private Opportunity', '{"type":"Opportunity"}'::jsonb)
ON CONFLICT (call_id, object_key) DO UPDATE
   SET object_type = EXCLUDED.object_type,
       object_id = EXCLUDED.object_id,
       object_name = EXCLUDED.object_name,
       raw_json = EXCLUDED.raw_json;
INSERT INTO call_context_fields(call_id, object_key, field_name, field_label, field_type, field_value_text, raw_json)
VALUES ('synthetic-call-001', 'Opportunity:opp-crm-context-private', 'Loss_Reason__c', 'Loss Reason', 'string', 'LossReason raw sentinel', '{"name":"Loss_Reason__c","value":"LossReason raw sentinel"}'::jsonb)
ON CONFLICT (call_id, object_key, field_name) DO UPDATE
   SET field_label = EXCLUDED.field_label,
       field_type = EXCLUDED.field_type,
       field_value_text = EXCLUDED.field_value_text,
       raw_json = EXCLUDED.raw_json;
INSERT INTO call_context_objects(call_id, object_key, object_type, object_id, object_name, raw_json)
VALUES ('synthetic-call-001', 'Contact:contact-analyst-private', 'Contact', 'contact-analyst-private', 'Analyst Private Contact', '{"type":"Contact"}'::jsonb)
ON CONFLICT (call_id, object_key) DO UPDATE
   SET object_type = EXCLUDED.object_type,
       object_id = EXCLUDED.object_id,
       object_name = EXCLUDED.object_name,
       raw_json = EXCLUDED.raw_json;
INSERT INTO call_context_fields(call_id, object_key, field_name, field_label, field_type, field_value_text, raw_json)
VALUES ('synthetic-call-001', 'Contact:contact-analyst-private', 'Title', 'Title', 'string', 'VP Operations', '{"name":"Title","value":"VP Operations"}'::jsonb)
ON CONFLICT (call_id, object_key, field_name) DO UPDATE
   SET field_label = EXCLUDED.field_label,
       field_type = EXCLUDED.field_type,
       field_value_text = EXCLUDED.field_value_text,
       raw_json = EXCLUDED.raw_json;
SQL

{
  printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_sync_status","arguments":{}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"list_crm_fields","arguments":{"object_type":"Opportunity","limit":5}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"search_transcripts_by_crm_context","arguments":{"query":"shared Postgres","object_type":"Opportunity","limit":5}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"build_call_cohort","arguments":{"filter":{"query":"shared Postgres"},"limit":5}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"compare_theme_outcomes","arguments":{"filter":{"query":"shared Postgres","limit":5},"theme_query":"Postgres"}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"summarize_loss_reasons_by_theme","arguments":{"filter":{"query":"shared Postgres","limit":5},"theme_query":"Postgres"}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"summarize_themes_by_persona","arguments":{"filter":{"query":"shared Postgres","limit":5},"theme_query":"Postgres"}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"rank_personas_by_insight_quality","arguments":{"filter":{"query":"shared Postgres","limit":5},"theme_query":"Postgres"}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"build_quote_pack","arguments":{"filter":{"query":"shared Postgres","limit":5},"theme_query":"Postgres"}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":12,"method":"tools/call","params":{"name":"generate_sales_hooks_from_themes","arguments":{"filter":{"query":"shared Postgres","limit":5},"theme_query":"Postgres"}}}'
} | GONG_DATABASE_URL="$ANALYST_READER_URL" GONGMCP_TOOL_PRESET=analyst GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS=1 go run ./cmd/gongmcp > /tmp/gongctl-postgres-analyst.jsonl
grep -q '"get_sync_status"' /tmp/gongctl-postgres-analyst.jsonl
grep -q '"list_crm_fields"' /tmp/gongctl-postgres-analyst.jsonl
grep -q '"summarize_calls_by_lifecycle"' /tmp/gongctl-postgres-analyst.jsonl
grep -q '"search_transcripts_by_crm_context"' /tmp/gongctl-postgres-analyst.jsonl
grep -q '"build_call_cohort"' /tmp/gongctl-postgres-analyst.jsonl
grep -q '"list_call_cohorts"' /tmp/gongctl-postgres-analyst.jsonl
grep -q '"compare_theme_outcomes"' /tmp/gongctl-postgres-analyst.jsonl
grep -q '"summarize_loss_reasons_by_theme"' /tmp/gongctl-postgres-analyst.jsonl
grep -q '"summarize_themes_by_persona"' /tmp/gongctl-postgres-analyst.jsonl
grep -q '"rank_personas_by_insight_quality"' /tmp/gongctl-postgres-analyst.jsonl
grep -q '"build_theme_brief"' /tmp/gongctl-postgres-analyst.jsonl
grep -q '"build_quote_pack"' /tmp/gongctl-postgres-analyst.jsonl
grep -q '"generate_sales_hooks_from_themes"' /tmp/gongctl-postgres-analyst.jsonl
grep -q '"compare_lifecycle_crm_fields"' /tmp/gongctl-postgres-analyst.jsonl
grep -q 'lifecycle_source' /tmp/gongctl-postgres-analyst.jsonl
grep -q 'cohort_id' /tmp/gongctl-postgres-analyst.jsonl
grep -q 'shared.*Postgres' /tmp/gongctl-postgres-analyst.jsonl
grep -q 'small_cell_suppression_applied' /tmp/gongctl-postgres-analyst.jsonl
grep -q 'small_cell_suppression_min_3' /tmp/gongctl-postgres-analyst.jsonl
assert_mcp_success /tmp/gongctl-postgres-analyst.jsonl 3 4 5 6 7 8 9 10 11 12
if grep -q 'postgres://\|gongctl_dev_password\|gongmcp_reader_dev_password\|raw_json\|raw_sha256\|field_value_text\|VP Operations\|LossReason raw sentinel\|synthetic-call-001\|speaker-1\|contact-analyst-private\|Analyst Private Contact\|synthetic-profile-call-001\|Profile lifecycle proposal review\|opp-profile-001\|Profile Opportunity\|opp-crm-context-private\|CRM Context Private Opportunity\|This synthetic segment validates' /tmp/gongctl-postgres-analyst.jsonl; then
  echo "postgres analyst preset output exposed connection, raw storage, identifier, or synthetic transcript markers" >&2
  exit 1
fi
if ! {
  printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
} | GONG_DATABASE_URL="$ANALYST_READER_URL" GONGMCP_TOOL_PRESET=analyst-expansion GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS=1 go run ./cmd/gongmcp > /tmp/gongctl-postgres-analyst-expansion.jsonl 2>/tmp/gongctl-postgres-analyst-expansion.stderr; then
  cat /tmp/gongctl-postgres-analyst-expansion.stderr >&2
  exit 1
fi
grep -q '"build_call_cohort"' /tmp/gongctl-postgres-analyst-expansion.jsonl
grep -q '"summarize_loss_reasons_by_theme"' /tmp/gongctl-postgres-analyst-expansion.jsonl
grep -q '"generate_sales_hooks_from_themes"' /tmp/gongctl-postgres-analyst-expansion.jsonl
if GONG_DATABASE_URL="$READER_URL" GONGMCP_TOOL_PRESET=all-readonly go run ./cmd/gongmcp </dev/null >/tmp/gongctl-postgres-all-readonly-rejected.txt 2>&1; then
  echo "postgres unexpectedly accepted all-readonly preset" >&2
  exit 1
fi
grep -q 'all-readonly is not supported by the postgres backend' /tmp/gongctl-postgres-all-readonly-rejected.txt
GONG_DATABASE_URL="$WRITER_URL" go run ./cmd/gongctl sync read-model --rebuild >/tmp/gongctl-postgres-post-analyst-read-model-rebuild.json
grep -q '"status": "rebuilt"' /tmp/gongctl-postgres-post-analyst-read-model-rebuild.json
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 -c "GRANT EXECUTE ON FUNCTION gongmcp_call_ai_highlights_count() TO gongmcp_reader" >/tmp/gongctl-postgres-post-analyst-reader-repair.txt

{
  printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"get_sync_status","arguments":{}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"summarize_call_facts","arguments":{"group_by":"transcript_status","limit":5}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"summarize_calls_by_lifecycle","arguments":{}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"rank_transcript_backlog","arguments":{"limit":5}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"summarize_call_facts","arguments":{"group_by":"scope","lifecycle_source":"profile","limit":5}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"summarize_calls_by_lifecycle","arguments":{"lifecycle_source":"profile"}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":12,"method":"tools/call","params":{"name":"rank_transcript_backlog","arguments":{"lifecycle_source":"profile","limit":5}}}'
} | GONG_DATABASE_URL="$READER_URL" GONGMCP_TOOL_PRESET=business-pilot go run ./cmd/gongmcp > /tmp/gongctl-postgres-business-pilot.jsonl

grep -q '"get_sync_status"' /tmp/gongctl-postgres-business-pilot.jsonl
grep -q '"summarize_call_facts"' /tmp/gongctl-postgres-business-pilot.jsonl
grep -q '"summarize_calls_by_lifecycle"' /tmp/gongctl-postgres-business-pilot.jsonl
grep -q '"rank_transcript_backlog"' /tmp/gongctl-postgres-business-pilot.jsonl
grep -q 'transcript_status' /tmp/gongctl-postgres-business-pilot.jsonl
grep -q 'scope' /tmp/gongctl-postgres-business-pilot.jsonl
grep -q 'External' /tmp/gongctl-postgres-business-pilot.jsonl
grep -q 'lifecycle_source' /tmp/gongctl-postgres-business-pilot.jsonl
grep -q 'profile' /tmp/gongctl-postgres-business-pilot.jsonl
assert_mcp_success /tmp/gongctl-postgres-business-pilot.jsonl 6 7 8 9 10 11 12

docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 -c "UPDATE calls SET updated_at = '2099-01-01T00:00:00Z' WHERE call_id = 'synthetic-profile-call-001'" >/dev/null
{
  printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"summarize_call_facts","arguments":{"group_by":"scope","lifecycle_source":"profile","limit":5}}}'
} | GONG_DATABASE_URL="$READER_URL" GONGMCP_TOOL_PRESET=business-pilot go run ./cmd/gongmcp > /tmp/gongctl-postgres-profile-stale-reader.jsonl
grep -q 'profile read model is missing or stale' /tmp/gongctl-postgres-profile-stale-reader.jsonl
GONG_DATABASE_URL="$WRITER_URL" go run ./cmd/gongctl sync read-model --rebuild >/tmp/gongctl-postgres-profile-cache-rewarm.json
grep -q '"status": "rebuilt"' /tmp/gongctl-postgres-profile-cache-rewarm.json
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 -c "GRANT EXECUTE ON FUNCTION gongmcp_call_ai_highlights_count() TO gongmcp_business_pilot_reader" >/tmp/gongctl-postgres-business-pilot-reader-profile-repair.txt
{
  printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"summarize_call_facts","arguments":{"group_by":"scope","lifecycle_source":"profile","limit":5}}}'
} | GONG_DATABASE_URL="$BUSINESS_PILOT_READER_URL" GONGMCP_TOOL_PRESET=business-pilot GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS=1 go run ./cmd/gongmcp > /tmp/gongctl-postgres-business-pilot-scoped-reader-after-rebuild.jsonl
assert_mcp_success /tmp/gongctl-postgres-business-pilot-scoped-reader-after-rebuild.jsonl 2

docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 <<'SQL' >/tmp/gongctl-postgres-governance-fixture.txt
DELETE FROM transcript_segments WHERE call_id IN ('synthetic-governance-blocked', 'synthetic-governance-allowed');
DELETE FROM transcripts WHERE call_id IN ('synthetic-governance-blocked', 'synthetic-governance-allowed');
DELETE FROM calls WHERE call_id IN ('synthetic-governance-blocked', 'synthetic-governance-allowed', 'synthetic-governance-missing-blocked', 'synthetic-governance-missing-allowed');
INSERT INTO calls(call_id, title, started_at, duration_seconds, parties_count, context_present, raw_json, raw_sha256, first_seen_at, updated_at)
VALUES
  ('synthetic-governance-blocked', 'Restricted governance customer review', '2026-03-01T15:00:00Z', 1500, 2, true, '{"id":"synthetic-governance-blocked","title":"Restricted governance customer review","started":"2026-03-01T15:00:00Z","duration":1500,"context":{"crmObjects":[{"type":"Account","id":"acct-governance-blocked","name":"Governance NoAI Corp","fields":{"Name":"Governance NoAI Corp"}}]}}'::jsonb, 'governance-blocked-sha', now()::text, now()::text),
  ('synthetic-governance-allowed', 'Allowed governance customer review', '2026-03-01T16:00:00Z', 1200, 2, true, '{"id":"synthetic-governance-allowed","title":"Allowed governance customer review","started":"2026-03-01T16:00:00Z","duration":1200,"context":{"crmObjects":[{"type":"Account","id":"acct-governance-allowed","name":"Governance Allowed Corp","fields":{"Name":"Governance Allowed Corp"}}]}}'::jsonb, 'governance-allowed-sha', now()::text, now()::text),
  ('synthetic-governance-missing-blocked', 'Restricted governance missing transcript', '2026-03-01T17:00:00Z', 900, 2, true, '{"id":"synthetic-governance-missing-blocked","title":"Restricted governance missing transcript","started":"2026-03-01T17:00:00Z","duration":900,"context":{"crmObjects":[{"type":"Account","id":"acct-governance-missing-blocked","name":"Governance NoAI Corp","fields":{"Name":"Governance NoAI Corp"}}]}}'::jsonb, 'governance-missing-blocked-sha', now()::text, now()::text),
  ('synthetic-governance-missing-allowed', 'Allowed governance missing transcript', '2026-03-01T16:30:00Z', 900, 2, true, '{"id":"synthetic-governance-missing-allowed","title":"Allowed governance missing transcript","started":"2026-03-01T16:30:00Z","duration":900,"context":{"crmObjects":[{"type":"Account","id":"acct-governance-missing-allowed","name":"Governance Allowed Corp","fields":{"Name":"Governance Allowed Corp"}}]}}'::jsonb, 'governance-missing-allowed-sha', now()::text, now()::text);
INSERT INTO transcripts(call_id, raw_json, raw_sha256, segment_count, first_seen_at, updated_at)
VALUES
  ('synthetic-governance-blocked', '{"callId":"synthetic-governance-blocked"}'::jsonb, 'governance-blocked-transcript-sha', 1, now()::text, now()::text),
  ('synthetic-governance-allowed', '{"callId":"synthetic-governance-allowed"}'::jsonb, 'governance-allowed-transcript-sha', 1, now()::text, now()::text);
INSERT INTO transcript_segments(call_id, segment_index, speaker_id, start_ms, end_ms, text, raw_json)
VALUES
  ('synthetic-governance-blocked', 0, 'speaker-governance-blocked', 0, 3000, 'Governance NoAI Corp should never appear in governed MCP transcript snippets.', '{"speakerId":"speaker-governance-blocked"}'::jsonb),
  ('synthetic-governance-allowed', 0, 'speaker-governance-allowed', 0, 3000, 'Governance allowed search evidence should remain visible.', '{"speakerId":"speaker-governance-allowed"}'::jsonb);
SQL
GONG_DATABASE_URL="$WRITER_URL" go run ./cmd/gongctl sync read-model --rebuild >/tmp/gongctl-postgres-governance-read-model.json
grep -q '"status": "rebuilt"' /tmp/gongctl-postgres-governance-read-model.json
cat >/tmp/gongctl-postgres-governance.yaml <<'YAML'
version: 1
lists:
  no_ai:
    customers:
      - name: "Governance NoAI Corp"
YAML
GONG_DATABASE_URL="$WRITER_URL" go run ./cmd/gongctl governance audit --config /tmp/gongctl-postgres-governance.yaml --apply-postgres-policy --json >/tmp/gongctl-postgres-governance-audit.json
grep -q '"backend": "postgres"' /tmp/gongctl-postgres-governance-audit.json
grep -q '"postgres_policy_applied": true' /tmp/gongctl-postgres-governance-audit.json
grep -q '"suppressed_call_count": 2' /tmp/gongctl-postgres-governance-audit.json

{
  printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search_calls","arguments":{"limit":10}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"search_transcript_segments","arguments":{"query":"Governance","limit":10}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"get_call","arguments":{"call_id":"synthetic-governance-blocked"}}}'
} | GONG_DATABASE_URL="$READER_URL" GONGMCP_TOOL_PRESET=governance-search GONGMCP_AI_GOVERNANCE_CONFIG=/tmp/gongctl-postgres-governance.yaml go run ./cmd/gongmcp > /tmp/gongctl-postgres-governance-mcp.jsonl 2>/tmp/gongctl-postgres-governance-mcp.stderr

grep -q '"search_calls"' /tmp/gongctl-postgres-governance-mcp.jsonl
grep -q '"search_transcript_segments"' /tmp/gongctl-postgres-governance-mcp.jsonl
grep -q 'synthetic-governance-allowed' /tmp/gongctl-postgres-governance-mcp.jsonl
grep -q 'allowed search evidence' /tmp/gongctl-postgres-governance-mcp.jsonl
grep -q 'AI governance active: backend=postgres' /tmp/gongctl-postgres-governance-mcp.stderr
assert_mcp_success /tmp/gongctl-postgres-governance-mcp.jsonl 3 4
python3 - /tmp/gongctl-postgres-governance-mcp.jsonl <<'PY'
import json
import sys

path = sys.argv[1]
for line in open(path, "r", encoding="utf-8"):
    line = line.strip()
    if not line:
        continue
    try:
        message = json.loads(line)
    except json.JSONDecodeError:
        continue
    if message.get("id") != 5:
        continue
    result = message.get("result")
    if not isinstance(result, dict) or result.get("isError") is not True:
        raise SystemExit(f"blocked get_call did not fail closed: {message}")
    text = json.dumps(result)
    if "synthetic-governance-blocked" in text or "Governance NoAI Corp" in text:
        raise SystemExit(f"blocked get_call leaked restricted data: {text}")
    break
else:
    raise SystemExit("missing blocked get_call MCP result id 5")
PY
if grep -q 'Governance NoAI Corp\|synthetic-governance-blocked' /tmp/gongctl-postgres-governance-mcp.jsonl /tmp/gongctl-postgres-governance-mcp.stderr; then
  echo "Postgres governed MCP output leaked restricted governance data" >&2
  exit 1
fi
if grep -q 'search_transcripts_by_call_facts\|missing_transcripts\|search_transcript_quotes_with_attribution' /tmp/gongctl-postgres-governance-mcp.jsonl; then
  echo "Postgres governance-search exposed unsupported Postgres tools" >&2
  exit 1
fi
{
  printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"missing_transcripts","arguments":{"from_date":"2026-03-01","to_date":"2026-03-01","limit":1}}}'
} | GONG_DATABASE_URL="$READER_URL" GONGMCP_TOOL_ALLOWLIST=missing_transcripts GONGMCP_AI_GOVERNANCE_CONFIG=/tmp/gongctl-postgres-governance.yaml go run ./cmd/gongmcp > /tmp/gongctl-postgres-governance-missing-transcripts.jsonl 2>/tmp/gongctl-postgres-governance-missing-transcripts.stderr
grep -q '"missing_transcripts"' /tmp/gongctl-postgres-governance-missing-transcripts.jsonl
grep -q 'synthetic-governance-missing-allowed' /tmp/gongctl-postgres-governance-missing-transcripts.jsonl
assert_mcp_success /tmp/gongctl-postgres-governance-missing-transcripts.jsonl 3
if grep -q 'synthetic-governance-missing-blocked\|Restricted governance missing transcript\|Governance NoAI Corp' /tmp/gongctl-postgres-governance-missing-transcripts.jsonl /tmp/gongctl-postgres-governance-missing-transcripts.stderr; then
  echo "Postgres governed missing_transcripts output leaked restricted missing call" >&2
  exit 1
fi
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 -c "UPDATE calls SET title = 'Allowed governance customer changed', updated_at = '2099-02-01T00:00:00Z' WHERE call_id = 'synthetic-governance-allowed'" >/dev/null
if {
  printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"search_calls","arguments":{"limit":5}}}'
} | GONG_DATABASE_URL="$READER_URL" GONGMCP_TOOL_PRESET=governance-search GONGMCP_AI_GOVERNANCE_CONFIG=/tmp/gongctl-postgres-governance.yaml go run ./cmd/gongmcp > /tmp/gongctl-postgres-governance-stale-mcp.jsonl 2>/tmp/gongctl-postgres-governance-stale-mcp.stderr; then
  echo "Postgres governed MCP unexpectedly started with a stale governance policy" >&2
  exit 1
fi
grep -q 'Postgres AI governance policy is stale' /tmp/gongctl-postgres-governance-stale-mcp.stderr
GONG_DATABASE_URL="$WRITER_URL" go run ./cmd/gongctl governance audit --config /tmp/gongctl-postgres-governance.yaml --apply-postgres-policy --json >/tmp/gongctl-postgres-governance-audit-reapplied.json
grep -q '"postgres_policy_applied": true' /tmp/gongctl-postgres-governance-audit-reapplied.json

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

docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 -c "GRANT SELECT ON profile_call_fact_cache TO gongmcp_reader; GRANT SELECT ON purged_call_ids TO gongmcp_reader" >/dev/null
if GONG_DATABASE_URL="$READER_URL" GONGMCP_TOOL_PRESET=business-pilot go run ./cmd/gongmcp </dev/null >/tmp/gongctl-postgres-reader-select-drift.txt 2>&1; then
  echo "read-only MCP unexpectedly started with direct SELECT on sensitive tables" >&2
  exit 1
fi
grep -q 'direct SELECT on sensitive tables' /tmp/gongctl-postgres-reader-select-drift.txt
GONG_DATABASE_URL="$WRITER_URL" go run ./cmd/gongctl sync read-model --rebuild >/tmp/gongctl-postgres-reader-select-drift-repaired.json
grep -q '"status": "rebuilt"' /tmp/gongctl-postgres-reader-select-drift-repaired.json

docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 -c "GRANT SELECT (raw_json) ON calls TO gongmcp_reader" >/dev/null
if GONG_DATABASE_URL="$READER_URL" GONGMCP_TOOL_PRESET=business-pilot go run ./cmd/gongmcp </dev/null >/tmp/gongctl-postgres-reader-column-drift.txt 2>&1; then
  echo "read-only MCP unexpectedly started with raw column SELECT drift" >&2
  exit 1
fi
grep -q 'forbidden column SELECT' /tmp/gongctl-postgres-reader-column-drift.txt
GONG_DATABASE_URL="$WRITER_URL" go run ./cmd/gongctl sync read-model --rebuild >/tmp/gongctl-postgres-reader-column-drift-repaired.json
grep -q '"status": "rebuilt"' /tmp/gongctl-postgres-reader-column-drift-repaired.json

docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 -c "GRANT SELECT (field_values_json) ON profile_call_fact_cache TO gongmcp_reader" >/dev/null
if GONG_DATABASE_URL="${READER_URL}&search_path=pg_catalog,public" GONGMCP_TOOL_PRESET=business-pilot go run ./cmd/gongmcp </dev/null >/tmp/gongctl-postgres-reader-sensitive-column-drift.txt 2>&1; then
  echo "read-only MCP unexpectedly started with sensitive-table column SELECT drift" >&2
  exit 1
fi
grep -q 'direct SELECT on sensitive tables' /tmp/gongctl-postgres-reader-sensitive-column-drift.txt
GONG_DATABASE_URL="$WRITER_URL" go run ./cmd/gongctl sync read-model --rebuild >/tmp/gongctl-postgres-reader-sensitive-column-drift-repaired.json
grep -q '"status": "rebuilt"' /tmp/gongctl-postgres-reader-sensitive-column-drift-repaired.json

docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 -c "REVOKE EXECUTE ON FUNCTION gongmcp_scorecard_activity_summary(text, integer) FROM gongmcp_reader; REVOKE EXECUTE ON FUNCTION gongmcp_cache_purge_plan(text) FROM gongmcp_reader; REVOKE EXECUTE ON FUNCTION gongmcp_crm_object_type_summary() FROM gongmcp_reader; REVOKE EXECUTE ON FUNCTION gongmcp_crm_field_value_search(text, text, text, integer, boolean, boolean) FROM gongmcp_reader; REVOKE EXECUTE ON FUNCTION gongmcp_unmapped_crm_field_inventory(integer) FROM gongmcp_reader; REVOKE EXECUTE ON FUNCTION gongmcp_late_stage_call_counts(text, text, text) FROM gongmcp_reader; REVOKE EXECUTE ON FUNCTION gongmcp_late_stage_stage_counts(text, text, text) FROM gongmcp_reader; REVOKE EXECUTE ON FUNCTION gongmcp_late_stage_signal_inventory(text, text, text, integer, boolean) FROM gongmcp_reader; REVOKE EXECUTE ON FUNCTION gongmcp_opportunities_missing_transcripts(text, integer) FROM gongmcp_reader; REVOKE EXECUTE ON FUNCTION gongmcp_opportunity_call_summary(text, integer) FROM gongmcp_reader; REVOKE EXECUTE ON FUNCTION gongmcp_crm_field_population_matrix(text, text, integer) FROM gongmcp_reader; REVOKE EXECUTE ON FUNCTION gongmcp_compare_lifecycle_crm_fields(text, text, text, integer) FROM gongmcp_reader; REVOKE EXECUTE ON FUNCTION gongmcp_search_transcript_segments_by_crm_context(text, text, text, integer) FROM gongmcp_reader; REVOKE EXECUTE ON FUNCTION gongmcp_missing_transcripts(text, text, text, text, text, text, text, text, integer) FROM gongmcp_reader" >/dev/null
if GONG_DATABASE_URL="$READER_URL" GONGMCP_TOOL_PRESET=analyst-core go run ./cmd/gongmcp </dev/null >/tmp/gongctl-postgres-reader-function-grant-drift.txt 2>&1; then
  echo "read-only MCP unexpectedly started without required function grants" >&2
  exit 1
fi
grep -q 'missing required function EXECUTE grants' /tmp/gongctl-postgres-reader-function-grant-drift.txt
GONG_DATABASE_URL="$WRITER_URL" go run ./cmd/gongctl sync read-model --rebuild >/tmp/gongctl-postgres-reader-function-grant-drift-repaired.json
grep -q '"status": "rebuilt"' /tmp/gongctl-postgres-reader-function-grant-drift-repaired.json

docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 -c "REVOKE ALL PRIVILEGES ON crm_schema_fields FROM gongmcp_reader; GRANT SELECT (integration_id, object_type, field_name, field_type, first_seen_at, updated_at) ON crm_schema_fields TO gongmcp_reader" >/dev/null
if GONG_DATABASE_URL="$READER_URL" GONGMCP_TOOL_PRESET=analyst-core go run ./cmd/gongmcp </dev/null >/tmp/gongctl-postgres-reader-crm-grant-drift.txt 2>&1; then
  echo "read-only MCP unexpectedly started without CRM schema field_label grant" >&2
  exit 1
fi
grep -q 'missing required column SELECT grants' /tmp/gongctl-postgres-reader-crm-grant-drift.txt
GONG_DATABASE_URL="$WRITER_URL" go run ./cmd/gongctl sync read-model --rebuild >/tmp/gongctl-postgres-reader-crm-grant-drift-repaired.json
grep -q '"status": "rebuilt"' /tmp/gongctl-postgres-reader-crm-grant-drift-repaired.json

docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 >/dev/null <<'SQL'
CREATE OR REPLACE FUNCTION gongmcp_crm_field_summary(object_type_arg text, row_limit integer)
RETURNS TABLE(
	object_type text,
	field_name text,
	field_label text,
	row_count bigint,
	call_count bigint,
	populated_count bigint,
	distinct_value_count bigint
)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = public, pg_temp
AS $function$
SELECT ''::text, ''::text, ''::text, 0::bigint, 0::bigint, 0::bigint, 0::bigint
 WHERE false
$function$;
GRANT EXECUTE ON FUNCTION gongmcp_crm_field_summary(text, integer) TO gongmcp_reader;
SQL
GONG_DATABASE_URL="$WRITER_URL" go run ./cmd/gongctl sync read-model --rebuild >/tmp/gongctl-postgres-retired-function-repaired.json
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -tAc "SELECT COALESCE(to_regprocedure('gongmcp_crm_field_summary(text, integer)')::text, '')" >/tmp/gongctl-postgres-retired-function.txt
if grep -q 'gongmcp_crm_field_summary' /tmp/gongctl-postgres-retired-function.txt; then
  echo "retired CRM field summary function still exists after reconcile" >&2
  exit 1
fi

for _ in $(seq 1 30); do
  if docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -tAc "SELECT 1" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 -tAc "SELECT 1" >/dev/null
GONGCTL_TEST_POSTGRES_URL="$WRITER_URL" go test -count=1 ./internal/store/postgres

echo "postgres smoke passed"
echo "sync output: /tmp/gongctl-postgres-sync.json"
echo "mcp output: /tmp/gongctl-postgres-mcp.jsonl"
echo "analyst-core output: /tmp/gongctl-postgres-analyst-core.jsonl"
echo "analyst-business-core output: /tmp/gongctl-postgres-analyst-business-core.jsonl"
echo "CRM-context transcript fixture output: /tmp/gongctl-postgres-crm-context-fixture.txt"
echo "CRM-context transcript MCP output: /tmp/gongctl-postgres-search-transcripts-by-crm-context.jsonl"
echo "CRM-context transcript reader output: /tmp/gongctl-postgres-reader-search-transcripts-by-crm-context.txt"
echo "CRM-context transcript text denial output: /tmp/gongctl-postgres-reader-search-transcripts-by-crm-context-transcript-text.txt"
echo "CRM-context object-id denial output: /tmp/gongctl-postgres-reader-search-transcripts-by-crm-context-object-id.txt"
echo "CRM-context field-value denial output: /tmp/gongctl-postgres-reader-search-transcripts-by-crm-context-field-value-text.txt"
echo "CRM-context function-grant drift denial output: /tmp/gongctl-postgres-reader-search-transcripts-by-crm-context-function-grant-drift.txt"
echo "CRM-context function-grant drift repair output: /tmp/gongctl-postgres-reader-search-transcripts-by-crm-context-function-grant-drift-repaired.txt"
echo "unmapped CRM fields output: /tmp/gongctl-postgres-unmapped-crm-fields.jsonl"
echo "reader unmapped CRM function fields output: /tmp/gongctl-postgres-reader-unmapped-crm-function-fields.txt"
echo "reader unmapped CRM function value-column denial output: /tmp/gongctl-postgres-reader-unmapped-crm-function-value-column.txt"
echo "CRM field values redacted output: /tmp/gongctl-postgres-crm-field-values-redacted.jsonl"
echo "CRM field values opt-in output: /tmp/gongctl-postgres-crm-field-values-optin.jsonl"
echo "late-stage CRM signals output: /tmp/gongctl-postgres-late-stage-crm-signals.jsonl"
echo "reader late-stage function fields output: /tmp/gongctl-postgres-reader-late-stage-function-fields.txt"
echo "reader late-stage function value-column denial output: /tmp/gongctl-postgres-reader-late-stage-function-value-column.txt"
echo "reader late-stage custom-stage denial output: /tmp/gongctl-postgres-reader-late-stage-custom-stage-denial.txt"
echo "MCP late-stage custom-stage denial output: /tmp/gongctl-postgres-late-stage-custom-stage-denial.jsonl"
echo "analyst output: /tmp/gongctl-postgres-analyst.jsonl"
echo "analyst scoped reader grant SQL: /tmp/gongctl-postgres-analyst-reader-grants.sql"
echo "analyst scoped reader apply JSON: /tmp/gongctl-postgres-analyst-reader-apply.json"
echo "analyst scoped reader direct calls.call_id denial output: /tmp/gongctl-postgres-analyst-reader-calls-call-id-denied.txt"
echo "analyst scoped reader raw business-analysis helper denial output: /tmp/gongctl-postgres-analyst-reader-raw-business-calls-denied.txt"
echo "analyst scoped reader raw transcript search helper denial output: /tmp/gongctl-postgres-analyst-reader-raw-transcript-search-denied.txt"
echo "analyst scoped reader sanitized business-analysis helper output: /tmp/gongctl-postgres-analyst-reader-business-calls-sanitized.json"
echo "analyst scoped reader sanitized transcript search helper output: /tmp/gongctl-postgres-analyst-reader-transcript-search-sanitized.json"
echo "all-readonly rejection output: /tmp/gongctl-postgres-all-readonly-rejected.txt"
echo "calls show output: /tmp/gongctl-postgres-calls-show.json"
echo "compatibility reader business-pilot MCP output: /tmp/gongctl-postgres-business-pilot.jsonl"
echo "synthetic business-pilot scoped reader grant SQL: /tmp/gongctl-postgres-business-pilot-reader-grants.sql"
echo "synthetic business-pilot scoped reader grant SQL via gongctl wrapper: /tmp/gongctl-postgres-business-pilot-reader-grants-gongctl.sql"
echo "synthetic business-pilot scoped reader apply dry-run SQL: /tmp/gongctl-postgres-business-pilot-reader-apply-dry-run.sql"
echo "synthetic business-pilot scoped reader apply JSON: /tmp/gongctl-postgres-business-pilot-reader-apply.json"
echo "synthetic business-pilot scoped reader apply repair JSON: /tmp/gongctl-postgres-business-pilot-reader-apply-repair.json"
echo "synthetic business-pilot scoped reader apply repair MCP output: /tmp/gongctl-postgres-business-pilot-reader-apply-repair-mcp.jsonl"
echo "synthetic business-pilot scoped reader apply wrong-database denial output: /tmp/gongctl-postgres-business-pilot-reader-apply-wrong-database.txt"
echo "synthetic business-pilot scoped reader apply column repair JSON: /tmp/gongctl-postgres-business-pilot-reader-apply-column-repair.json"
echo "synthetic business-pilot scoped reader apply column repair calls.title denial output: /tmp/gongctl-postgres-business-pilot-reader-apply-column-repair-calls-title-denied.txt"
echo "synthetic business-pilot scoped reader apply column repair sequence denial output: /tmp/gongctl-postgres-business-pilot-reader-apply-column-repair-sequence-denied.txt"
echo "synthetic business-pilot scoped reader apply PUBLIC sequence denial output: /tmp/gongctl-postgres-business-pilot-reader-apply-public-sequence-denied.txt"
echo "synthetic business-pilot scoped reader default privilege drift denial output: /tmp/gongctl-postgres-business-pilot-reader-default-privilege-drift.txt"
echo "synthetic business-pilot scoped reader apply repair identifier-helper denial output: /tmp/gongctl-postgres-business-pilot-reader-apply-repair-identifier-helper-denied.txt"
echo "synthetic business-pilot scoped reader apply repair active-profile denial output: /tmp/gongctl-postgres-business-pilot-reader-apply-repair-active-profile-denied.txt"
echo "synthetic business-pilot scoped reader apply repair missing-transcripts denial output: /tmp/gongctl-postgres-business-pilot-reader-apply-repair-missing-transcripts-denied.txt"
echo "synthetic business-pilot scoped reader apply role-membership denial output: /tmp/gongctl-postgres-business-pilot-reader-apply-membership-denied.txt"
echo "business-pilot grant SQL checks passed: no PASSWORD/URL markers, no configured secret values, no direct calls/call_facts call_id/title grants"
echo "synthetic business-pilot scoped reader create output: /tmp/gongctl-postgres-business-pilot-reader-create.txt"
echo "synthetic business-pilot scoped reader post-reconcile read-model output: /tmp/gongctl-postgres-business-pilot-reader-post-reconcile-read-model.json"
echo "synthetic business-pilot scoped reader MCP output: /tmp/gongctl-postgres-business-pilot-scoped-reader.jsonl"
echo "synthetic business-pilot scoped reader builtin lifecycle denial output: /tmp/gongctl-postgres-business-pilot-scoped-reader-builtin-denied.jsonl"
echo "synthetic business-pilot scoped reader missing-transcripts denial output: /tmp/gongctl-postgres-business-pilot-reader-missing-transcripts-denied.txt"
echo "synthetic business-pilot scoped reader calls.call_id denial output: /tmp/gongctl-postgres-business-pilot-reader-calls-call-id-denied.txt"
echo "synthetic business-pilot scoped reader calls.title denial output: /tmp/gongctl-postgres-business-pilot-reader-calls-title-denied.txt"
echo "synthetic business-pilot scoped reader call_facts.call_id denial output: /tmp/gongctl-postgres-business-pilot-reader-call-facts-call-id-denied.txt"
echo "synthetic business-pilot scoped reader call_facts.title denial output: /tmp/gongctl-postgres-business-pilot-reader-call-facts-title-denied.txt"
echo "synthetic business-pilot scoped reader context object call_id denial output: /tmp/gongctl-postgres-business-pilot-reader-context-objects-call-id-denied.txt"
echo "synthetic business-pilot scoped reader context field object_key denial output: /tmp/gongctl-postgres-business-pilot-reader-context-fields-object-key-denied.txt"
echo "synthetic business-pilot scoped reader generic active-profile denial output: /tmp/gongctl-postgres-business-pilot-reader-active-profile-generic-denied.txt"
echo "synthetic business-pilot scoped reader sanitized active-profile output: /tmp/gongctl-postgres-business-pilot-reader-active-profile-sanitized.json"
echo "synthetic business-pilot scoped reader profile-cache metadata denial output: /tmp/gongctl-postgres-business-pilot-reader-profile-cache-meta-denied.txt"
echo "synthetic business-pilot scoped reader sanitized profile-cache metadata output: /tmp/gongctl-postgres-business-pilot-reader-profile-cache-meta-sanitized.json"
echo "synthetic business-pilot scoped reader generic profile summary denial output: /tmp/gongctl-postgres-business-pilot-reader-profile-summary-generic-denied.txt"
echo "synthetic business-pilot scoped reader unsupported profile summary grouping denial output: /tmp/gongctl-postgres-business-pilot-reader-profile-summary-unsupported-group-denied.txt"
echo "synthetic business-pilot scoped reader sanitized profile summary output: /tmp/gongctl-postgres-business-pilot-reader-profile-summary-sanitized.txt"
echo "synthetic business-pilot scoped reader sanitized lifecycle summary output: /tmp/gongctl-postgres-business-pilot-reader-profile-lifecycle-summary-sanitized.txt"
echo "synthetic business-pilot scoped reader sanitized transcript backlog output: /tmp/gongctl-postgres-business-pilot-reader-profile-backlog-sanitized.txt"
echo "synthetic business-pilot scoped reader extra column grant denial output: /tmp/gongctl-postgres-business-pilot-reader-extra-column-grant.txt"
echo "synthetic business-pilot scoped reader extra sequence grant denial output: /tmp/gongctl-postgres-business-pilot-reader-extra-sequence-grant.txt"
echo "synthetic business-pilot scoped reader default privilege drift output: /tmp/gongctl-postgres-business-pilot-reader-default-privilege-drift.txt"
echo "synthetic business-pilot scoped reader PUBLIC default privilege drift output: /tmp/gongctl-postgres-business-pilot-reader-public-default-privilege-drift.txt"
echo "synthetic business-pilot scoped reader identifier-bearing profile-cache helper denial output: /tmp/gongctl-postgres-business-pilot-reader-profile-cache-identifier-helper-denied.txt"
echo "synthetic business-pilot scoped reader unbounded sanitized profile-cache denial output: /tmp/gongctl-postgres-business-pilot-reader-profile-cache-unbounded-sanitized-denied.txt"
echo "synthetic business-pilot scoped reader limited sanitized profile-cache output: /tmp/gongctl-postgres-business-pilot-reader-profile-cache-sanitized-limited.txt"
echo "synthetic business-pilot scoped reader limited sanitized profile-cache identifier-bearing row count: ${business_pilot_direct_profile_identifier_count:-unknown}"
echo "synthetic business-pilot scoped reader limited sanitized profile-cache non-identifier fact row count: ${business_pilot_direct_profile_fact_count:-unknown}"
echo "synthetic business-pilot scoped reader missing grant denial output: /tmp/gongctl-postgres-business-pilot-reader-missing-grant.txt"
echo "synthetic function-free scoped reader create output: /tmp/gongctl-postgres-function-free-reader-create.txt"
echo "synthetic function-free scoped reader MCP output: /tmp/gongctl-postgres-function-free-scoped-reader.jsonl"
echo "reader denial output: /tmp/gongctl-postgres-reader-write.txt"
echo "reader raw-read denial output: /tmp/gongctl-postgres-reader-raw-read.txt"
echo "reader settings raw-read denial output: /tmp/gongctl-postgres-reader-settings-raw-read.txt"
echo "reader scorecard activity raw-read denial output: /tmp/gongctl-postgres-reader-scorecard-activity-raw-read.txt"
echo "reader CRM inventory raw-read denial output: /tmp/gongctl-postgres-reader-crm-inventory-raw-read.txt"
echo "reader sensitive-read denial output: /tmp/gongctl-postgres-reader-sensitive-read.txt"
echo "reader object-key denial output: /tmp/gongctl-postgres-reader-object-key-read.txt"
echo "reader regrant output: /tmp/gongctl-postgres-reader-regrant.txt"
echo "reader column-drift denial output: /tmp/gongctl-postgres-reader-column-drift.txt"
echo "reader sensitive-column-drift denial output: /tmp/gongctl-postgres-reader-sensitive-column-drift.txt"
echo "reader function-grant drift denial output: /tmp/gongctl-postgres-reader-function-grant-drift.txt"
echo "reader function-grant drift repair output: /tmp/gongctl-postgres-reader-function-grant-drift-repaired.json"
echo "reader public function-grant drift denial output: /tmp/gongctl-postgres-reader-function-public-drift.txt"
echo "reader public function-grant drift repair output: /tmp/gongctl-postgres-reader-function-public-drift-repaired.txt"
echo "reader CRM grant-drift denial output: /tmp/gongctl-postgres-reader-crm-grant-drift.txt"
echo "retired function output: /tmp/gongctl-postgres-retired-function.txt"
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
echo "profile fixture read model output: /tmp/gongctl-postgres-profile-fixture-read-model.json"
echo "profile stale reader output: /tmp/gongctl-postgres-profile-stale-reader.jsonl"
echo "profile cache rewarm output: /tmp/gongctl-postgres-profile-cache-rewarm.json"
echo "business-pilot scoped reader after writer rebuild output: /tmp/gongctl-postgres-business-pilot-scoped-reader-after-rebuild.jsonl"
echo "scorecard settings output: /tmp/gongctl-postgres-scorecard-settings.json"
echo "scorecards output: /tmp/gongctl-postgres-scorecards.json"
echo "scorecard detail output: /tmp/gongctl-postgres-scorecard-detail.json"
echo "scorecard activity count output: /tmp/gongctl-postgres-scorecard-activity-count.txt"
echo "scorecard activity review-method output: /tmp/gongctl-postgres-scorecard-activity-review-method.json"
echo "scorecard activity transcript-status output: /tmp/gongctl-postgres-scorecard-activity-transcript-status.json"
echo "scorecard activity reviewed-user denial output: /tmp/gongctl-postgres-scorecard-activity-reviewed-user-denied.txt"
echo "CRM integrations count output: /tmp/gongctl-postgres-crm-integrations-count.txt"
echo "CRM schema objects count output: /tmp/gongctl-postgres-crm-schema-objects-count.txt"
echo "CRM schema fields count output: /tmp/gongctl-postgres-crm-schema-fields-count.txt"
echo "CRM schema analysis output: /tmp/gongctl-postgres-crm-schema.json"
echo "opportunities missing transcripts MCP output: /tmp/gongctl-postgres-opportunities-missing-transcripts.jsonl"
echo "opportunities missing transcripts reader output: /tmp/gongctl-postgres-reader-opportunities-missing-transcripts.txt"
echo "opportunities missing transcripts object-id denial output: /tmp/gongctl-postgres-reader-opportunities-missing-object-id.txt"
echo "opportunities missing transcripts object-name denial output: /tmp/gongctl-postgres-reader-opportunities-missing-object-name.txt"
echo "opportunities missing transcripts latest-call-id denial output: /tmp/gongctl-postgres-reader-opportunities-missing-latest-call-id.txt"
echo "opportunities missing transcripts owner-id denial output: /tmp/gongctl-postgres-reader-opportunities-missing-owner-id.txt"
echo "opportunities missing transcripts amount denial output: /tmp/gongctl-postgres-reader-opportunities-missing-amount.txt"
echo "opportunities missing transcripts close-date denial output: /tmp/gongctl-postgres-reader-opportunities-missing-close-date.txt"
echo "opportunities missing transcripts function-grant drift denial output: /tmp/gongctl-postgres-reader-opportunities-missing-function-grant-drift.txt"
echo "opportunities missing transcripts function-grant drift repair output: /tmp/gongctl-postgres-reader-opportunities-missing-function-grant-drift-repaired.txt"
echo "opportunity call summary MCP output: /tmp/gongctl-postgres-opportunity-call-summary.jsonl"
echo "opportunity call summary reader output: /tmp/gongctl-postgres-reader-opportunity-call-summary.txt"
echo "opportunity call summary object-id denial output: /tmp/gongctl-postgres-reader-opportunity-summary-object-id.txt"
echo "opportunity call summary object-name denial output: /tmp/gongctl-postgres-reader-opportunity-summary-object-name.txt"
echo "opportunity call summary latest-call-id denial output: /tmp/gongctl-postgres-reader-opportunity-summary-latest-call-id.txt"
echo "opportunity call summary owner-id denial output: /tmp/gongctl-postgres-reader-opportunity-summary-owner-id.txt"
echo "opportunity call summary amount denial output: /tmp/gongctl-postgres-reader-opportunity-summary-amount.txt"
echo "opportunity call summary close-date denial output: /tmp/gongctl-postgres-reader-opportunity-summary-close-date.txt"
echo "opportunity call summary raw-value column denial output: /tmp/gongctl-postgres-reader-opportunity-summary-field-value-text.txt"
echo "opportunity call summary direct sensitive-table denial output: /tmp/gongctl-postgres-reader-crm-inventory-raw-read.txt"
echo "opportunity call summary function-grant drift denial output: /tmp/gongctl-postgres-reader-opportunity-summary-function-grant-drift.txt"
echo "opportunity call summary function-grant drift repair output: /tmp/gongctl-postgres-reader-opportunity-summary-function-grant-drift-repaired.txt"
echo "CRM field population matrix MCP output: /tmp/gongctl-postgres-crm-field-population-matrix.jsonl"
echo "CRM field population matrix reader output: /tmp/gongctl-postgres-reader-crm-field-population-matrix.txt"
echo "CRM field population matrix unsafe group output: /tmp/gongctl-postgres-reader-crm-field-population-matrix-unsafe-group.txt"
echo "CRM field population matrix object-id denial output: /tmp/gongctl-postgres-reader-crm-field-population-matrix-object-id.txt"
echo "CRM field population matrix object-name denial output: /tmp/gongctl-postgres-reader-crm-field-population-matrix-object-name.txt"
echo "CRM field population matrix object-key denial output: /tmp/gongctl-postgres-reader-crm-field-population-matrix-object-key.txt"
echo "CRM field population matrix call-id denial output: /tmp/gongctl-postgres-reader-crm-field-population-matrix-call-id.txt"
echo "CRM field population matrix field-value denial output: /tmp/gongctl-postgres-reader-crm-field-population-matrix-field-value-text.txt"
echo "CRM field population matrix raw-json denial output: /tmp/gongctl-postgres-reader-crm-field-population-matrix-raw-json.txt"
echo "CRM field population matrix raw-sha denial output: /tmp/gongctl-postgres-reader-crm-field-population-matrix-raw-sha256.txt"
echo "CRM field population matrix function-grant drift denial output: /tmp/gongctl-postgres-reader-crm-field-population-matrix-function-grant-drift.txt"
echo "CRM field population matrix function-grant drift repair output: /tmp/gongctl-postgres-reader-crm-field-population-matrix-function-grant-drift-repaired.txt"
echo "lifecycle CRM comparison fixture output: /tmp/gongctl-postgres-lifecycle-crm-fixture-read-model.json"
echo "lifecycle CRM comparison MCP output: /tmp/gongctl-postgres-compare-lifecycle-crm-fields.jsonl"
echo "lifecycle CRM comparison reader output: /tmp/gongctl-postgres-reader-compare-lifecycle-crm-fields.txt"
echo "lifecycle CRM comparison bad-bucket denial output: /tmp/gongctl-postgres-reader-compare-lifecycle-crm-fields-bad-bucket.txt"
echo "lifecycle CRM comparison bad-object denial output: /tmp/gongctl-postgres-reader-compare-lifecycle-crm-fields-bad-object.txt"
echo "lifecycle CRM comparison null-argument denial output: /tmp/gongctl-postgres-reader-compare-lifecycle-crm-fields-null-arg.txt"
echo "lifecycle CRM comparison function-grant drift denial output: /tmp/gongctl-postgres-reader-compare-lifecycle-crm-fields-function-grant-drift.txt"
echo "lifecycle CRM comparison function-grant drift repair output: /tmp/gongctl-postgres-reader-compare-lifecycle-crm-fields-function-grant-drift-repaired.txt"
echo "missing transcripts fixture read model output: /tmp/gongctl-postgres-missing-transcripts-fixture-read-model.json"
echo "missing transcripts filtered MCP output: /tmp/gongctl-postgres-missing-transcripts-filtered.jsonl"
echo "missing transcripts reader record-reference output: /tmp/gongctl-postgres-reader-missing-transcripts-record-reference.txt"
echo "missing transcripts function-grant drift denial output: /tmp/gongctl-postgres-reader-missing-transcripts-function-grant-drift.txt"
echo "missing transcripts function-grant drift repair output: /tmp/gongctl-postgres-reader-missing-transcripts-function-grant-drift-repaired.json"
echo "cache inventory output: /tmp/gongctl-postgres-cache-inventory.json"
echo "cache inventory writer URL output: /tmp/gongctl-postgres-cache-inventory-writer-url.json"
echo "support bundle response output: /tmp/gongctl-postgres-support-bundle.json"
echo "support bundle directory: /tmp/gongctl-postgres-support-bundle"
echo "purge fixture output: /tmp/gongctl-postgres-purge-fixture.txt"
echo "purge fixture read model output: /tmp/gongctl-postgres-purge-fixture-read-model.json"
echo "purge dry-run output: /tmp/gongctl-postgres-purge-dry-run.json"
echo "purge policy missing-config output: /tmp/gongctl-postgres-purge-policy-missing-config.txt"
echo "purge policy missing-approval output: /tmp/gongctl-postgres-purge-policy-missing-approval.txt"
echo "purge policy dry-run output: /tmp/gongctl-postgres-purge-policy-dry-run.json"
echo "purge policy redacted-metadata output: /tmp/gongctl-postgres-purge-policy-redacted-metadata.json"
echo "purge policy writer dry-run denial output: /tmp/gongctl-postgres-purge-writer-policy-dry-run-denied.txt"
echo "purge reader confirm denial output: /tmp/gongctl-postgres-purge-reader-confirm-denied.txt"
echo "purge confirm output: /tmp/gongctl-postgres-purge-confirm.json"
echo "purge post-counts output: /tmp/gongctl-postgres-purge-post-counts.txt"
echo "purge post-search output: /tmp/gongctl-postgres-purge-search-after.json"
echo "governance fixture output: /tmp/gongctl-postgres-governance-fixture.txt"
echo "governance read model output: /tmp/gongctl-postgres-governance-read-model.json"
echo "governance audit output: /tmp/gongctl-postgres-governance-audit.json"
echo "governance audit reapplied output: /tmp/gongctl-postgres-governance-audit-reapplied.json"
echo "governance mcp output: /tmp/gongctl-postgres-governance-mcp.jsonl"
echo "governance mcp stderr output: /tmp/gongctl-postgres-governance-mcp.stderr"
echo "governance stale mcp output: /tmp/gongctl-postgres-governance-stale-mcp.jsonl"
echo "governance stale mcp stderr output: /tmp/gongctl-postgres-governance-stale-mcp.stderr"
echo "reader select-drift denial output: /tmp/gongctl-postgres-reader-select-drift.txt"
echo "reader select-drift repair output: /tmp/gongctl-postgres-reader-select-drift-repaired.json"
