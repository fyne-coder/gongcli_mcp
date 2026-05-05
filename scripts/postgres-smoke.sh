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

cleanup
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" up -d postgres

for _ in $(seq 1 90); do
  if docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -tAc "SELECT 1" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 -tAc "SELECT 1" >/dev/null

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
grep -q '"backend": "postgres"' /tmp/gongctl-postgres-support-bundle/manifest.json
grep -q '"path_policy": "database_url_not_exported"' /tmp/gongctl-postgres-support-bundle/manifest.json
grep -q '"reader_privilege_status": "valid_reader"' /tmp/gongctl-postgres-support-bundle/diagnostics.json
grep -q '"read_model_status": "current"' /tmp/gongctl-postgres-support-bundle/diagnostics.json
grep -q '"schema_version":' /tmp/gongctl-postgres-support-bundle/diagnostics.json
grep -q '"total_crm_schema_fields": 5' /tmp/gongctl-postgres-support-bundle/cache-summary.json
grep -q '"table": "crm_schema_fields"' /tmp/gongctl-postgres-support-bundle/cache-summary.json
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

if GONG_DATABASE_URL="$READER_URL" GONGMCP_TOOL_PRESET=analyst go run ./cmd/gongmcp </dev/null >/tmp/gongctl-postgres-analyst-rejected.txt 2>&1; then
  echo "postgres unexpectedly accepted full analyst preset" >&2
  exit 1
fi
grep -q 'analyst is not supported by the postgres vertical slice' /tmp/gongctl-postgres-analyst-rejected.txt
if GONG_DATABASE_URL="$READER_URL" GONGMCP_TOOL_PRESET=all-readonly go run ./cmd/gongmcp </dev/null >/tmp/gongctl-postgres-all-readonly-rejected.txt 2>&1; then
  echo "postgres unexpectedly accepted all-readonly preset" >&2
  exit 1
fi
grep -q 'all-readonly is not supported by the postgres vertical slice' /tmp/gongctl-postgres-all-readonly-rejected.txt

{
  printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"get_sync_status","arguments":{}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"summarize_call_facts","arguments":{"group_by":"transcript_status","limit":5}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"summarize_calls_by_lifecycle","arguments":{}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"rank_transcript_backlog","arguments":{"limit":5}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"summarize_call_facts","arguments":{"group_by":"deal_stage","lifecycle_source":"profile","limit":5}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"summarize_calls_by_lifecycle","arguments":{"lifecycle_source":"profile"}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":12,"method":"tools/call","params":{"name":"rank_transcript_backlog","arguments":{"lifecycle_source":"profile","limit":5}}}'
} | GONG_DATABASE_URL="$READER_URL" GONGMCP_TOOL_PRESET=business-pilot go run ./cmd/gongmcp > /tmp/gongctl-postgres-business-pilot.jsonl

grep -q '"get_sync_status"' /tmp/gongctl-postgres-business-pilot.jsonl
grep -q '"summarize_call_facts"' /tmp/gongctl-postgres-business-pilot.jsonl
grep -q '"summarize_calls_by_lifecycle"' /tmp/gongctl-postgres-business-pilot.jsonl
grep -q '"rank_transcript_backlog"' /tmp/gongctl-postgres-business-pilot.jsonl
grep -q 'transcript_status' /tmp/gongctl-postgres-business-pilot.jsonl
grep -q 'deal_stage' /tmp/gongctl-postgres-business-pilot.jsonl
grep -q 'Proposal' /tmp/gongctl-postgres-business-pilot.jsonl
grep -q 'lifecycle_source' /tmp/gongctl-postgres-business-pilot.jsonl
grep -q 'profile' /tmp/gongctl-postgres-business-pilot.jsonl
assert_mcp_success /tmp/gongctl-postgres-business-pilot.jsonl 6 7 8 9 10 11 12

docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 -c "UPDATE calls SET updated_at = '2099-01-01T00:00:00Z' WHERE call_id = 'synthetic-profile-call-001'" >/dev/null
{
  printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"summarize_call_facts","arguments":{"group_by":"deal_stage","lifecycle_source":"profile","limit":5}}}'
} | GONG_DATABASE_URL="$READER_URL" GONGMCP_TOOL_PRESET=business-pilot go run ./cmd/gongmcp > /tmp/gongctl-postgres-profile-stale-reader.jsonl
grep -q 'profile read model is missing or stale' /tmp/gongctl-postgres-profile-stale-reader.jsonl
GONG_DATABASE_URL="$WRITER_URL" go run ./cmd/gongctl sync read-model --rebuild >/tmp/gongctl-postgres-profile-cache-rewarm.json
grep -q '"status": "rebuilt"' /tmp/gongctl-postgres-profile-cache-rewarm.json

docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 <<'SQL' >/tmp/gongctl-postgres-governance-fixture.txt
DELETE FROM transcript_segments WHERE call_id IN ('synthetic-governance-blocked', 'synthetic-governance-allowed');
DELETE FROM transcripts WHERE call_id IN ('synthetic-governance-blocked', 'synthetic-governance-allowed');
DELETE FROM calls WHERE call_id IN ('synthetic-governance-blocked', 'synthetic-governance-allowed');
INSERT INTO calls(call_id, title, started_at, duration_seconds, parties_count, context_present, raw_json, raw_sha256, first_seen_at, updated_at)
VALUES
  ('synthetic-governance-blocked', 'Restricted governance customer review', '2026-03-01T15:00:00Z', 1500, 2, true, '{"id":"synthetic-governance-blocked","title":"Restricted governance customer review","started":"2026-03-01T15:00:00Z","duration":1500,"context":{"crmObjects":[{"type":"Account","id":"acct-governance-blocked","name":"Governance NoAI Corp","fields":{"Name":"Governance NoAI Corp"}}]}}'::jsonb, 'governance-blocked-sha', now()::text, now()::text),
  ('synthetic-governance-allowed', 'Allowed governance customer review', '2026-03-01T16:00:00Z', 1200, 2, true, '{"id":"synthetic-governance-allowed","title":"Allowed governance customer review","started":"2026-03-01T16:00:00Z","duration":1200,"context":{"crmObjects":[{"type":"Account","id":"acct-governance-allowed","name":"Governance Allowed Corp","fields":{"Name":"Governance Allowed Corp"}}]}}'::jsonb, 'governance-allowed-sha', now()::text, now()::text);
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
grep -q '"suppressed_call_count": 1' /tmp/gongctl-postgres-governance-audit.json

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

docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 -c "REVOKE EXECUTE ON FUNCTION gongmcp_scorecard_activity_summary(text, integer) FROM gongmcp_reader; REVOKE EXECUTE ON FUNCTION gongmcp_cache_purge_plan(text) FROM gongmcp_reader" >/dev/null
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

docker compose -p "$PROJECT" -f "$COMPOSE_FILE" exec -T postgres psql -U gongctl -d gongctl -v ON_ERROR_STOP=1 <<'SQL' >/dev/null
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

GONGCTL_TEST_POSTGRES_URL="$WRITER_URL" go test -count=1 ./internal/store/postgres

echo "postgres smoke passed"
echo "sync output: /tmp/gongctl-postgres-sync.json"
echo "mcp output: /tmp/gongctl-postgres-mcp.jsonl"
echo "analyst-core output: /tmp/gongctl-postgres-analyst-core.jsonl"
echo "analyst-business-core output: /tmp/gongctl-postgres-analyst-business-core.jsonl"
echo "analyst rejection output: /tmp/gongctl-postgres-analyst-rejected.txt"
echo "all-readonly rejection output: /tmp/gongctl-postgres-all-readonly-rejected.txt"
echo "calls show output: /tmp/gongctl-postgres-calls-show.json"
echo "business-pilot output: /tmp/gongctl-postgres-business-pilot.jsonl"
echo "reader denial output: /tmp/gongctl-postgres-reader-write.txt"
echo "reader raw-read denial output: /tmp/gongctl-postgres-reader-raw-read.txt"
echo "reader settings raw-read denial output: /tmp/gongctl-postgres-reader-settings-raw-read.txt"
echo "reader scorecard activity raw-read denial output: /tmp/gongctl-postgres-reader-scorecard-activity-raw-read.txt"
echo "reader CRM inventory raw-read denial output: /tmp/gongctl-postgres-reader-crm-inventory-raw-read.txt"
echo "reader sensitive-read denial output: /tmp/gongctl-postgres-reader-sensitive-read.txt"
echo "reader regrant output: /tmp/gongctl-postgres-reader-regrant.txt"
echo "reader column-drift denial output: /tmp/gongctl-postgres-reader-column-drift.txt"
echo "reader sensitive-column-drift denial output: /tmp/gongctl-postgres-reader-sensitive-column-drift.txt"
echo "reader function-grant drift denial output: /tmp/gongctl-postgres-reader-function-grant-drift.txt"
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
