#!/usr/bin/env bash
set -euo pipefail

LAB_VM="${LAB_VM:-root@192.168.1.205}"
LAB_PUBLIC_BASE_URL="${LAB_PUBLIC_BASE_URL:-http://192.168.1.205}"
REMOTE_LAB="${REMOTE_LAB:-/srv/gongctl/source/deploy/lab-auth}"

require() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "missing required command: $1" >&2
    exit 1
  }
}

remote_env() {
  ssh "$LAB_VM" "awk -F= -v key='$1' '\$1 == key {print substr(\$0, length(key) + 2)}' '$REMOTE_LAB/.env'"
}

token_for() {
  local username="$1"
  local password="$2"
  local response

  for _ in $(seq 1 30); do
    response="$(curl -fsS "$LAB_PUBLIC_BASE_URL/realms/gong-lab/protocol/openid-connect/token" \
      -H "Content-Type: application/x-www-form-urlencoded" \
      --data-urlencode "grant_type=password" \
      --data-urlencode "client_id=gong-lab-proxy" \
      --data-urlencode "client_secret=$OIDC_CLIENT_SECRET" \
      --data-urlencode "username=$username" \
      --data-urlencode "password=$password" 2>/dev/null || true)"
    if [[ -n "$response" ]] && jq -e '.access_token' >/dev/null 2>&1 <<<"$response"; then
      jq -r '.access_token' <<<"$response"
      return 0
    fi
    sleep 2
  done

  echo "failed to obtain token for $username" >&2
  if [[ -n "${response:-}" ]]; then
    jq . <<<"$response" >&2 || printf '%s\n' "$response" >&2
  fi
  return 1
}

http_status() {
  curl -sS -o /tmp/gongctl-lab-smoke-body.$$ -w '%{http_code}' "$@"
}

wait_for_url() {
  local url="$1"
  for _ in $(seq 1 30); do
    if curl -fsS "$url" >/dev/null 2>&1; then
      return 0
    fi
    sleep 2
  done

  echo "timed out waiting for $url" >&2
  return 1
}

require ssh
require curl
require jq
require python3
require rg

OIDC_CLIENT_SECRET="$(remote_env OIDC_CLIENT_SECRET)"
PILOT_PASSWORD="$(remote_env PILOT_PASSWORD)"
BLOCKED_PASSWORD="$(remote_env BLOCKED_PASSWORD)"
TEST_PASSWORD="$(remote_env TEST_PASSWORD)"

wait_for_url "$LAB_PUBLIC_BASE_URL/healthz"
wait_for_url "$LAB_PUBLIC_BASE_URL/realms/gong-lab/.well-known/openid-configuration"

echo "== healthz =="
curl -fsS "$LAB_PUBLIC_BASE_URL/healthz" | jq .

echo "== OAuth protected resource metadata =="
metadata_response="$(curl -fsS "$LAB_PUBLIC_BASE_URL/.well-known/oauth-protected-resource")"
echo "$metadata_response" | jq .
echo "$metadata_response" | jq -e --arg resource "$LAB_PUBLIC_BASE_URL/mcp" '.resource == $resource' >/dev/null
echo "$metadata_response" | jq -e --arg issuer "$LAB_PUBLIC_BASE_URL/realms/gong-lab" '.authorization_servers[] == $issuer' >/dev/null
echo "$metadata_response" | jq -e '.scopes_supported | index("openid")' >/dev/null
echo "$metadata_response" | jq -e '.scopes_supported | index("offline_access")' >/dev/null
echo "$metadata_response" | jq -e '.audiences_supported | index("gong-lab-proxy")' >/dev/null

echo "== anonymous dynamic client registration is accepted =="
dcr_body="$(jq -n '{
  client_name: "gongctl-lab-smoke",
  redirect_uris: ["https://chatgpt.com/aip/gongctl-lab-smoke/oauth/callback"],
  grant_types: ["authorization_code"],
  response_types: ["code"],
  token_endpoint_auth_method: "none",
  scope: "openid profile email offline_access"
}')"
dcr_response_file="/tmp/gongctl-lab-dcr-response.$$"
dcr_status="$(curl -sS -o "$dcr_response_file" -w '%{http_code}' \
  "$LAB_PUBLIC_BASE_URL/realms/gong-lab/clients-registrations/openid-connect" \
  -H "Content-Type: application/json" \
  --data "$dcr_body")"
case "$dcr_status" in
  201) echo "ok: dynamic client registration status=$dcr_status" ;;
  *) echo "expected dynamic client registration status=201, got status=$dcr_status" >&2; jq . "$dcr_response_file" >&2 || cat "$dcr_response_file" >&2; rm -f "$dcr_response_file"; exit 1 ;;
esac
dcr_registration_uri="$(jq -r '.registration_client_uri' "$dcr_response_file")"
dcr_registration_token="$(jq -r '.registration_access_token' "$dcr_response_file")"
dcr_client_id="$(jq -r '.client_id' "$dcr_response_file")"
echo "== dynamic client basic scope carries MCP audience and group mappers =="
basic_mapper_count="$(ssh "$LAB_VM" "
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
  docker-compose exec -T keycloak /opt/keycloak/bin/kcadm.sh update clients/\$client_uuid \
    -r gong-lab \
    -s directAccessGrantsEnabled=true >/dev/null
  basic_scope_id=\$(docker-compose exec -T keycloak /opt/keycloak/bin/kcadm.sh get client-scopes \
    -r gong-lab \
    --fields id,name \
    --format csv \
    --noquotes \
    | awk -F, '\$2 == \"basic\" {print \$1}' \
    | tr -d '\\r')
  docker-compose exec -T keycloak /opt/keycloak/bin/kcadm.sh get client-scopes/\$basic_scope_id/protocol-mappers/models \
    -r gong-lab \
    --fields name \
    --format json \
    | jq '[.[].name | select(. == \"audience-gong-lab-proxy\" or . == \"groups\")] | unique | length'
")"
if [[ "$basic_mapper_count" != "2" ]]; then
  echo "Keycloak basic client scope is missing MCP audience/group mappers" >&2
  exit 1
fi
dynamic_token_response="$(curl -fsS "$LAB_PUBLIC_BASE_URL/realms/gong-lab/protocol/openid-connect/token" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  --data-urlencode "grant_type=password" \
  --data-urlencode "client_id=$dcr_client_id" \
  --data-urlencode "username=test@fyne-llc.com" \
  --data-urlencode "password=$TEST_PASSWORD" \
  --data-urlencode "scope=openid profile email offline_access")"
dynamic_access_token="$(jq -r '.access_token' <<<"$dynamic_token_response")"
dynamic_claims="$(python3 - "$dynamic_access_token" <<'PY'
import base64
import sys

payload = sys.argv[1].split(".")[1]
payload += "=" * (-len(payload) % 4)
print(base64.urlsafe_b64decode(payload).decode())
PY
)"
echo "$dynamic_claims" | jq -e '.aud | if type == "array" then index("gong-lab-proxy") else . == "gong-lab-proxy" end' >/dev/null
echo "$dynamic_claims" | jq -e '.groups | index("/gong-mcp-users")' >/dev/null
echo "ok: dynamic client token has MCP audience and group claims"
if [[ "$dcr_registration_uri" != "null" && "$dcr_registration_token" != "null" ]]; then
  curl -fsS -X DELETE "$dcr_registration_uri" -H "Authorization: Bearer $dcr_registration_token" >/dev/null || true
fi
rm -f "$dcr_response_file"

echo "== unauthenticated /mcp is denied =="
headers_file="/tmp/gongctl-lab-smoke-headers.$$"
unauth_status="$(http_status \
  -D "$headers_file" \
  -H "Origin: $LAB_PUBLIC_BASE_URL" \
  -H "Content-Type: application/json" \
  --data '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"lab-smoke","version":"0"}}}' \
  "$LAB_PUBLIC_BASE_URL/mcp")"
rm -f /tmp/gongctl-lab-smoke-body.$$
case "$unauth_status" in
  401) echo "ok: unauthenticated status=$unauth_status" ;;
  *) echo "expected unauthenticated denial, got status=$unauth_status" >&2; exit 1 ;;
esac
tr -d '\r' <"$headers_file" | rg -i '^www-authenticate: Bearer ' >/dev/null
tr -d '\r' <"$headers_file" | rg -F "resource_metadata=\"$LAB_PUBLIC_BASE_URL/.well-known/oauth-protected-resource\"" >/dev/null
rm -f "$headers_file"

echo "== obtain lab OIDC tokens =="
pilot_token="$(token_for pilot@example.com "$PILOT_PASSWORD")"
blocked_token="$(token_for blocked@example.com "$BLOCKED_PASSWORD")"
test_token="$(token_for test@fyne-llc.com "$TEST_PASSWORD")"
test "$pilot_token" != "null"
test "$blocked_token" != "null"
test "$test_token" != "null"
echo "ok: received pilot, test, and blocked-user tokens"

echo "== approved users can request offline_access =="
offline_response="$(curl -fsS "$LAB_PUBLIC_BASE_URL/realms/gong-lab/protocol/openid-connect/token" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  --data-urlencode "grant_type=password" \
  --data-urlencode "client_id=gong-lab-proxy" \
  --data-urlencode "client_secret=$OIDC_CLIENT_SECRET" \
  --data-urlencode "username=test@fyne-llc.com" \
  --data-urlencode "password=$TEST_PASSWORD" \
  --data-urlencode "scope=openid profile email offline_access")"
echo "$offline_response" | jq -e '.access_token' >/dev/null
echo "$offline_response" | jq -e '.refresh_token' >/dev/null
echo "ok: test@fyne-llc.com can receive an offline-capable token response"

echo "== blocked user token is denied =="
blocked_status="$(http_status \
  -H "Authorization: Bearer $blocked_token" \
  -H "Origin: $LAB_PUBLIC_BASE_URL" \
  -H "Content-Type: application/json" \
  --data '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"lab-smoke","version":"0"}}}' \
  "$LAB_PUBLIC_BASE_URL/mcp")"
rm -f /tmp/gongctl-lab-smoke-body.$$
case "$blocked_status" in
  401|403) echo "ok: blocked-user status=$blocked_status" ;;
  *) echo "expected blocked user denial, got status=$blocked_status" >&2; exit 1 ;;
esac

echo "== bad Origin is denied =="
bad_origin_status="$(http_status \
  -H "Authorization: Bearer $pilot_token" \
  -H "Origin: http://evil.example.test" \
  -H "Content-Type: application/json" \
  --data '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"lab-smoke","version":"0"}}}' \
  "$LAB_PUBLIC_BASE_URL/mcp")"
rm -f /tmp/gongctl-lab-smoke-body.$$
case "$bad_origin_status" in
  403) echo "ok: bad origin status=$bad_origin_status" ;;
  *) echo "expected bad Origin denial, got status=$bad_origin_status" >&2; exit 1 ;;
esac

echo "== MCP initialize through OIDC token path =="
initialize_response="$(curl -fsS \
  -H "Authorization: Bearer $pilot_token" \
  -H "Origin: $LAB_PUBLIC_BASE_URL" \
  -H "Content-Type: application/json" \
  --data '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"lab-smoke","version":"0"}}}' \
  "$LAB_PUBLIC_BASE_URL/mcp")"
echo "$initialize_response" | jq .
echo "$initialize_response" | jq -e '.result.protocolVersion' >/dev/null

echo "== MCP initialize through test@fyne-llc.com token path =="
test_initialize_response="$(curl -fsS \
  -H "Authorization: Bearer $test_token" \
  -H "Origin: $LAB_PUBLIC_BASE_URL" \
  -H "Content-Type: application/json" \
  --data '{"jsonrpc":"2.0","id":11,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"lab-smoke-test-user","version":"0"}}}' \
  "$LAB_PUBLIC_BASE_URL/mcp")"
echo "$test_initialize_response" | jq .
echo "$test_initialize_response" | jq -e '.result.protocolVersion' >/dev/null

echo "== business-pilot tools/list =="
tools_response="$(curl -fsS \
  -H "Authorization: Bearer $pilot_token" \
  -H "Origin: $LAB_PUBLIC_BASE_URL" \
  -H "Content-Type: application/json" \
  --data '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' \
  "$LAB_PUBLIC_BASE_URL/mcp")"
echo "$tools_response" | jq '.result.tools[].name'
echo "$tools_response" | jq -e '.result.tools[].name | select(. == "get_sync_status")' >/dev/null
if echo "$tools_response" | jq -e '.result.tools[].name | select(. == "search_transcript_segments")' >/dev/null; then
  echo "unexpected non-business-pilot tool exposed: search_transcript_segments" >&2
  exit 1
fi

echo "== ChatGPT-style tools/call _meta is accepted =="
meta_status_response="$(curl -fsS \
  -H "Authorization: Bearer $pilot_token" \
  -H "Origin: $LAB_PUBLIC_BASE_URL" \
  -H "Content-Type: application/json" \
  --data '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_sync_status","arguments":{"_meta":{"openai/userAgent":"chatgpt-connector"}},"_meta":{"progressToken":"lab-smoke"}}}' \
  "$LAB_PUBLIC_BASE_URL/mcp")"
echo "$meta_status_response" | jq '.result.content[0].text | fromjson | {total_calls,total_transcripts,missing_transcripts}'
echo "$meta_status_response" | jq -e '.error == null and (.result.isError // false | not)' >/dev/null
echo "$meta_status_response" | jq -e '.result.content[0].text | fromjson | has("total_calls")' >/dev/null

echo "== MCP container has no Gong credentials =="
ssh "$LAB_VM" "cd '$REMOTE_LAB' && docker-compose exec -T gongmcp env" \
| { if rg '^GONG_ACCESS_KEY|^GONG_ACCESS_KEY_SECRET|^GONG_BASE_URL'; then exit 1; else exit 0; fi; }
echo "ok: no Gong credential env vars in gongmcp"

echo "== DB mount is read-only =="
ssh "$LAB_VM" "docker run --rm -v /srv/gongctl/runtime/gong-mcp-governed.db:/data/gong.db:ro alpine:3.22 sh -c 'echo x >> /data/gong.db'" >/tmp/gongctl-lab-ro.$$ 2>&1 && {
  cat /tmp/gongctl-lab-ro.$$
  rm -f /tmp/gongctl-lab-ro.$$
  echo "expected read-only DB write to fail" >&2
  exit 1
}
rm -f /tmp/gongctl-lab-ro.$$
echo "ok: DB mount rejects writes"

echo "ok: lab auth smoke passed"
