#!/usr/bin/env bash
set -euo pipefail

LAB_PUBLIC_BASE_URL="${LAB_PUBLIC_BASE_URL:-}"

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

http_status() {
  curl -sS -o "$body_file" -w '%{http_code}' "$@"
}

require curl
require jq
require rg
require_env LAB_PUBLIC_BASE_URL

body_file="$(mktemp "${TMPDIR:-/tmp}/gongctl-claude-preflight-body.XXXXXX")"
headers_file="$(mktemp "${TMPDIR:-/tmp}/gongctl-claude-preflight-headers.XXXXXX")"
cleanup() {
  rm -f "$body_file" "$headers_file"
}
trap cleanup EXIT

echo "== Claude remote MCP preflight =="
echo "MCP server URL: $LAB_PUBLIC_BASE_URL/mcp"
echo "authentication: OAuth"
echo "static OAuth client ID: claude-remote-mcp"
echo

echo "== protected-resource metadata =="
metadata_response="$(curl -fsS "$LAB_PUBLIC_BASE_URL/.well-known/oauth-protected-resource")"
echo "$metadata_response" | jq .
echo "$metadata_response" | jq -e --arg resource "$LAB_PUBLIC_BASE_URL/mcp" '.resource == $resource' >/dev/null
echo "$metadata_response" | jq -e '.authorization_servers | length >= 1' >/dev/null
echo "ok: protected-resource metadata resolves"

echo "== authorization-server metadata =="
oidc_discovery="$(curl -fsS "$LAB_PUBLIC_BASE_URL/realms/gong-lab/.well-known/openid-configuration")"
echo "$oidc_discovery" | jq -e '.issuer' >/dev/null
if echo "$oidc_discovery" | jq -e 'has("registration_endpoint")' >/dev/null; then
  echo "warning: authorization-server metadata advertises registration_endpoint; Claude web may prefer DCR over pre-registered credentials" >&2
else
  echo "ok: authorization-server metadata omits registration_endpoint for pre-registered clients"
fi
echo "ok: Keycloak OIDC metadata resolves"

echo "== unauthenticated /mcp challenge =="
unauth_status="$(http_status \
  -D "$headers_file" \
  -H "Origin: $LAB_PUBLIC_BASE_URL" \
  -H "Content-Type: application/json" \
  --data '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"claude-preflight","version":"0"}}}' \
  "$LAB_PUBLIC_BASE_URL/mcp")"
case "$unauth_status" in
  401) echo "ok: unauthenticated status=$unauth_status" ;;
  *) echo "expected unauthenticated denial, got status=$unauth_status" >&2; cat "$headers_file" >&2; exit 1 ;;
esac
tr -d '\r' <"$headers_file" | rg -i '^www-authenticate: Bearer ' >/dev/null
tr -d '\r' <"$headers_file" | rg -F "resource_metadata=\"$LAB_PUBLIC_BASE_URL/.well-known/oauth-protected-resource\"" >/dev/null
echo "ok: unauthenticated /mcp returns WWW-Authenticate with resource_metadata"

cat <<EOF

Manual Claude remote test checklist:
1. Add MCP server URL: $LAB_PUBLIC_BASE_URL/mcp
2. Choose authentication: OAuth
3. Open Advanced OAuth settings for the production-like/static-client path:
   - OAuth Client ID: claude-remote-mcp
   - OAuth Client Secret: paste the pre-registered Claude client secret from
     the operator-held lab/IdP secret store. Do not paste it into logs.
   - Redirect/callback URI allowed by the IdP:
     https://claude.ai/api/mcp/auth_callback
4. Sign in with an approved lab user such as LAB_APPROVED_EMAIL
5. First prompt:
   Use the gongctl MCP connector. Call get_sync_status. Then summarize total
   calls, total transcripts, missing transcripts, and which business-pilot
   tools are available next.
6. Capture evidence:
   - this preflight output
   - lab-smoke.sh success
   - Claude OAuth success
   - first get_sync_status result

Failure ladder:
- metadata ok but unauthenticated /mcp not 401/WWW-Authenticate -> fix registration/broker/shim/routing before JumpCloud debugging
- Keycloak says "Invalid parameter: redirect_uri" -> add https://claude.ai/api/mcp/auth_callback to the Claude client's IdP redirect URI allowlist
- Claude shows oauth_error=unauthorized_client or mcp_token_exchange_failed and IdP logs show invalid_client_credentials -> recreate/update the Claude connector with the static OAuth client secret
- login ok but initialize fails -> check audience/group claims, requested scope, resource/audience, and offline_access policy
- initialize ok but first tool call fails -> check _meta tolerance and tool preset

This preflight checks the public protocol surface only. It does not prove the
IdP admin-side client secret, redirect URI allowlist, group mapper, or token
claim policy unless you also run provider admin checks or the full manual Claude
connector flow.

Only move to JumpCloud provider-specific smoke after this Keycloak proof path passes.
EOF

echo
echo "ok: Claude remote MCP preflight passed"
