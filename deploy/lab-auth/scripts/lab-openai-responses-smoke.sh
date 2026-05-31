#!/usr/bin/env bash
# shellcheck disable=SC2029
set -euo pipefail

LAB_VM="${LAB_VM:-}"
LAB_PUBLIC_BASE_URL="${LAB_PUBLIC_BASE_URL:-}"
REMOTE_ROOT="${REMOTE_ROOT:-/srv/gongctl}"
REMOTE_LAB="${REMOTE_LAB:-$REMOTE_ROOT/source/deploy/lab-auth}"
OPENAI_MODEL="${OPENAI_MODEL:-gpt-5.5}"

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

token_for() {
  local username="$1"
  local password="$2"
  curl -fsS "$LAB_PUBLIC_BASE_URL/realms/gong-lab/protocol/openid-connect/token" \
    -H "Content-Type: application/x-www-form-urlencoded" \
    --data-urlencode "grant_type=password" \
    --data-urlencode "client_id=gong-lab-proxy" \
    --data-urlencode "client_secret=$OIDC_CLIENT_SECRET" \
    --data-urlencode "username=$username" \
    --data-urlencode "password=$password" \
  | jq -r '.access_token'
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
case "$REMOTE_LAB" in
  /*) ;;
  *) echo "REMOTE_LAB must be an absolute remote path" >&2; exit 2 ;;
esac
case "$REMOTE_LAB" in
  *\'*|*\"*|*[\;\`\$]*)
    echo "REMOTE_LAB contains unsupported shell metacharacters" >&2
    exit 2
    ;;
esac

if [[ -z "${OPENAI_API_KEY:-}" ]]; then
  echo "OPENAI_API_KEY is required for the Responses API smoke." >&2
  exit 1
fi

OIDC_CLIENT_SECRET="$(remote_env OIDC_CLIENT_SECRET)"
LAB_APPROVED_EMAIL="$(remote_env LAB_APPROVED_EMAIL)"
LAB_APPROVED_PASSWORD="$(remote_env LAB_APPROVED_PASSWORD)"
if [[ -z "$OIDC_CLIENT_SECRET" || -z "$LAB_APPROVED_EMAIL" || -z "$LAB_APPROVED_PASSWORD" ]]; then
  echo "remote lab .env is missing required lab auth values; redeploy with lab-up.sh" >&2
  exit 1
fi
approved_token="$(token_for "$LAB_APPROVED_EMAIL" "$LAB_APPROVED_PASSWORD")"
test "$approved_token" != "null"

request_body="$(jq -n \
  --arg model "$OPENAI_MODEL" \
  --arg server_url "$LAB_PUBLIC_BASE_URL/mcp" \
  --arg authorization "$approved_token" \
  '{
    model: $model,
    tools: [{
      type: "mcp",
      server_label: "gongctl_lab",
      server_description: "Synthetic gongctl enterprise auth lab MCP server.",
      server_url: $server_url,
      authorization: $authorization,
      require_approval: "never",
      allowed_tools: ["get_sync_status", "summarize_calls_by_lifecycle", "summarize_call_facts", "rank_transcript_backlog"]
    }],
    input: "Use the gongctl_lab MCP server to call get_sync_status. Keep the answer to one short sentence."
  }')"

response_file="/tmp/gongctl-openai-responses-smoke.$$"
status="$(curl -sS -o "$response_file" -w '%{http_code}' https://api.openai.com/v1/responses \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $OPENAI_API_KEY" \
  --data "$request_body")"

if [[ "$status" != 2* ]]; then
  echo "Responses API request failed with status=$status" >&2
  jq . "$response_file" >&2 || cat "$response_file" >&2
  rm -f "$response_file"
  exit 1
fi

echo "== Responses API output items =="
jq -r '.output[]?.type' "$response_file"

echo "== Imported MCP tools =="
jq -r '.output[]? | select(.type == "mcp_list_tools") | .tools[]?.name' "$response_file"

jq -e '.output[]? | select(.type == "mcp_list_tools") | .tools[]?.name | select(. == "get_sync_status")' "$response_file" >/dev/null

echo "== MCP calls =="
jq -r '.output[]? | select(.type == "mcp_call") | "\(.name) error=\(.error // "null")"' "$response_file"

if jq -e '.output[]? | select(.type == "mcp_call" and .error != null)' "$response_file" >/dev/null; then
  echo "Responses API reported an MCP call error" >&2
  jq '.output[]? | select(.type == "mcp_call")' "$response_file" >&2
  rm -f "$response_file"
  exit 1
fi

echo "== Model text =="
jq -r '.output_text // empty' "$response_file"
rm -f "$response_file"
echo "ok: OpenAI Responses API MCP smoke passed"
