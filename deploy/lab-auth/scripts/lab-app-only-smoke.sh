#!/usr/bin/env bash
# shellcheck disable=SC2029
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
LAB_VM="${LAB_VM:-}"
LAB_PUBLIC_BASE_URL="${LAB_PUBLIC_BASE_URL:-}"
REMOTE_ROOT="${REMOTE_ROOT:-/srv/gongctl}"
REMOTE_LAB="${REMOTE_LAB:-$REMOTE_ROOT/source/deploy/lab-auth}"

require() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "missing required command: $1" >&2
    exit 1
  }
}

require_env() {
  local name="$1"
  if [[ -z "${!name:-}" ]]; then
    echo "$name is required, see deploy/lab-auth/.env.example" >&2
    exit 1
  fi
}

remote_env() {
  ssh "$LAB_VM" "awk -F= -v key='$1' '\$1 == key {print substr(\$0, length(key) + 2)}' '$REMOTE_LAB/.env'"
}

require ssh
require curl
require jq
require_env LAB_VM
require_env LAB_PUBLIC_BASE_URL

case "$LAB_VM" in
  -*|*[[:space:]]*)
    echo "LAB_VM must be an ssh host target, not an option or whitespace-containing value" >&2
    exit 2
    ;;
esac
case "$REMOTE_ROOT" in
  /*) ;;
  *) echo "REMOTE_ROOT must be an absolute remote path" >&2; exit 2 ;;
esac
case "$REMOTE_ROOT" in
  *\'*|*\"*|*[\;\`\$]*)
    echo "REMOTE_ROOT contains unsupported shell metacharacters" >&2
    exit 2
    ;;
esac

LAB_SECONDARY_EMAIL="$(remote_env LAB_SECONDARY_EMAIL)"
LAB_SECONDARY_PASSWORD="$(remote_env LAB_SECONDARY_PASSWORD)"
GONGMCP_TOOL_PRESET="$(remote_env GONGMCP_TOOL_PRESET)"
GONG_DATABASE_URL="$(remote_env GONG_DATABASE_URL)"
GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS="$(remote_env GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS)"
GONGMCP_POSTGRES_REDACTED_SERVING_DB="$(remote_env GONGMCP_POSTGRES_REDACTED_SERVING_DB)"
GONGMCP_AI_GOVERNANCE_CONFIG_HOST="$(remote_env GONGMCP_AI_GOVERNANCE_CONFIG_HOST)"
GONGMCP_POLICY_SWITCHES="$(remote_env GONGMCP_POLICY_SWITCHES)"
LAB_GONGMCP_IMAGE="$(remote_env LAB_GONGMCP_IMAGE)"
if [[ -z "$LAB_SECONDARY_EMAIL" || -z "$LAB_SECONDARY_PASSWORD" ]]; then
  echo "remote lab .env is missing secondary lab user values; redeploy with lab-up.sh" >&2
  exit 1
fi
if [[ -z "$GONG_DATABASE_URL" && -z "${LAB_DB:-}" ]]; then
  echo "SQLite-backed labs need LAB_DB for app-only smoke so lab-up can refresh the mounted DB" >&2
  exit 1
fi

dcr_response_file="/tmp/gongctl-lab-app-only-dcr.$$"
dcr_body="$(jq -n '{
  client_name: "gongctl-lab-app-only-smoke",
  redirect_uris: ["https://chatgpt.com/aip/gongctl-lab-app-only-smoke/oauth/callback"],
  grant_types: ["authorization_code"],
  response_types: ["code"],
  token_endpoint_auth_method: "none",
  scope: "openid profile email offline_access"
}')"
dcr_status="$(curl -sS -o "$dcr_response_file" -w '%{http_code}' \
  "$LAB_PUBLIC_BASE_URL/realms/gong-lab/clients-registrations/openid-connect" \
  -H "Content-Type: application/json" \
  --data "$dcr_body")"
case "$dcr_status" in
  201) ;;
  *) echo "expected dynamic client registration status=201, got status=$dcr_status" >&2; jq . "$dcr_response_file" >&2 || cat "$dcr_response_file" >&2; rm -f "$dcr_response_file"; exit 1 ;;
esac
dcr_registration_uri="$(jq -r '.registration_client_uri' "$dcr_response_file")"
dcr_registration_token="$(jq -r '.registration_access_token' "$dcr_response_file")"
dcr_client_id="$(jq -r '.client_id' "$dcr_response_file")"
cleanup() {
  if [[ "${dcr_registration_uri:-}" != "null" && -n "${dcr_registration_uri:-}" && "${dcr_registration_token:-}" != "null" && -n "${dcr_registration_token:-}" ]]; then
    curl -fsS -X DELETE "$dcr_registration_uri" -H "Authorization: Bearer $dcr_registration_token" >/dev/null || true
  fi
  rm -f "$dcr_response_file"
}
trap cleanup EXIT

echo "== registered temporary dynamic client $dcr_client_id =="
ssh "$LAB_VM" "
  set -e
  cd '$REMOTE_LAB'
  docker-compose exec -T keycloak /opt/keycloak/bin/kcadm.sh config credentials \
    --server http://127.0.0.1:8080 \
    --realm master \
    --user admin \
    --password \"\$(awk -F= '\$1 == \"KEYCLOAK_ADMIN_PASSWORD\" {print substr(\$0, length(\$1) + 2)}' .env)\" >/dev/null
  client_uuid=\$(docker-compose exec -T keycloak /opt/keycloak/bin/kcadm.sh get clients \
    -r gong-lab \
    -q clientId='$dcr_client_id' \
    --fields id \
    --format csv \
    --noquotes \
    | tail -1 \
    | tr -d '\\r')
  test -n \"\$client_uuid\"
  docker-compose exec -T keycloak /opt/keycloak/bin/kcadm.sh update clients/\$client_uuid \
    -r gong-lab \
    -s directAccessGrantsEnabled=true >/dev/null
"

echo "== dynamic client token before app-only deploy =="
curl -fsS "$LAB_PUBLIC_BASE_URL/realms/gong-lab/protocol/openid-connect/token" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  --data-urlencode "grant_type=password" \
  --data-urlencode "client_id=$dcr_client_id" \
  --data-urlencode "username=$LAB_SECONDARY_EMAIL" \
  --data-urlencode "password=$LAB_SECONDARY_PASSWORD" \
  --data-urlencode "scope=openid profile email offline_access" \
  | jq -e '.access_token' >/dev/null

new_deployment_id="${LAB_GONGMCP_DEPLOYMENT_ID:-lab-app-only-smoke-$(date -u +%Y%m%dT%H%M%SZ)}"
echo "== app-only deploy $new_deployment_id =="
LAB_DEPLOY_MODE=app-only \
LAB_GONGMCP_DEPLOYMENT_ID="$new_deployment_id" \
LAB_GONG_DATABASE_URL="$GONG_DATABASE_URL" \
LAB_TOOL_PRESET="${GONGMCP_TOOL_PRESET:-business-workbench}" \
LAB_GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS="$GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS" \
LAB_GONGMCP_POSTGRES_REDACTED_SERVING_DB="$GONGMCP_POSTGRES_REDACTED_SERVING_DB" \
LAB_GONGMCP_AI_GOVERNANCE_CONFIG_HOST="$GONGMCP_AI_GOVERNANCE_CONFIG_HOST" \
LAB_GONGMCP_POLICY_SWITCHES="$GONGMCP_POLICY_SWITCHES" \
LAB_GONGMCP_IMAGE="$LAB_GONGMCP_IMAGE" \
  "$ROOT/deploy/lab-auth/scripts/lab-up.sh"

echo "== healthz sees new deployment id =="
curl -fsS "$LAB_PUBLIC_BASE_URL/healthz" \
  | jq --arg deployment_id "$new_deployment_id" -e '.mcp_server.deployment_id == $deployment_id' >/dev/null

echo "== same dynamic client token after app-only deploy =="
curl -fsS "$LAB_PUBLIC_BASE_URL/realms/gong-lab/protocol/openid-connect/token" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  --data-urlencode "grant_type=password" \
  --data-urlencode "client_id=$dcr_client_id" \
  --data-urlencode "username=$LAB_SECONDARY_EMAIL" \
  --data-urlencode "password=$LAB_SECONDARY_PASSWORD" \
  --data-urlencode "scope=openid profile email offline_access" \
  | jq -e '.access_token' >/dev/null

echo "ok: app-only deploy preserved dynamic client $dcr_client_id"
