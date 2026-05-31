#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Usage: scripts/smoke-mcp-gateway.sh --url URL [options]

Validates a public gongmcp-gateway deployment without printing tokens:
  1. protected-resource metadata is reachable
  2. endpoint-scoped protected-resource metadata is reachable
  3. optional CORS preflight allows the expected Origin
  4. unauthenticated GET and POST /mcp return 401 with resource_metadata and scope
  5. optional authenticated tools/list reaches the private upstream

Options:
  --url URL          Public gateway base URL or /mcp URL
  --issuer URL       Optional expected Cognito issuer URL in metadata
  --expect-dcr       Expect gateway-advertised auth metadata with registration_endpoint
  --token TOKEN      Optional Cognito access token for authenticated tools/list
  --origin ORIGIN    Optional Origin header for CORS/client-origin smoke
  -h, --help         Show this help

Environment fallbacks:
  GONGMCP_GATEWAY_SMOKE_URL
  GONGMCP_GATEWAY_SMOKE_ISSUER
  GONGMCP_GATEWAY_SMOKE_TOKEN
  GONGMCP_GATEWAY_SMOKE_ORIGIN

Never paste customer tokens into public JWT decoder sites. This script does
not echo token values.
USAGE
}

url="${GONGMCP_GATEWAY_SMOKE_URL:-}"
issuer="${GONGMCP_GATEWAY_SMOKE_ISSUER:-}"
token="${GONGMCP_GATEWAY_SMOKE_TOKEN:-}"
origin="${GONGMCP_GATEWAY_SMOKE_ORIGIN:-}"
expect_dcr="${GONGMCP_GATEWAY_SMOKE_EXPECT_DCR:-}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --url)
      url="${2:?--url requires a value}"
      shift 2
      ;;
    --issuer)
      issuer="${2:?--issuer requires a value}"
      shift 2
      ;;
    --expect-dcr)
      expect_dcr="1"
      shift
      ;;
    --token)
      token="${2:?--token requires a value}"
      shift 2
      ;;
    --origin)
      origin="${2:?--origin requires a value}"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown option: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [[ -z "$url" ]]; then
  usage >&2
  exit 2
fi

base="${url%/}"
if [[ "$base" == */mcp ]]; then
  mcp_url="$base"
  base="${base%/mcp}"
else
  mcp_url="$base/mcp"
fi

metadata_url="$base/.well-known/oauth-protected-resource"
metadata_mcp_url="$base/.well-known/oauth-protected-resource/mcp"
payload='{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}'

tmpdir="$(mktemp -d "${TMPDIR:-/tmp}/gongmcp-gateway-smoke.XXXXXX")"
trap 'rm -rf "$tmpdir"' EXIT

curl_headers=()
if [[ -n "$origin" ]]; then
  curl_headers+=(-H "Origin: $origin")
fi

echo "== protected-resource metadata =="
curl -fsS "$metadata_url" -o "$tmpdir/metadata.json"
python3 - "$tmpdir/metadata.json" "$mcp_url" "$issuer" <<'PY'
import json
import sys
from urllib.parse import urlparse

with open(sys.argv[1], "r", encoding="utf-8") as f:
    data = json.load(f)
expected_resource = sys.argv[2]
expected_issuer = sys.argv[3]
if data.get("resource") != expected_resource:
    raise SystemExit(f"resource={data.get('resource')!r}, want {expected_resource!r}")
servers = data.get("authorization_servers")
if not isinstance(servers, list) or not servers:
    raise SystemExit("authorization_servers must be a non-empty list")
server = servers[0]
if expected_issuer and server != expected_issuer.rstrip("/"):
    raise SystemExit(f"authorization_servers[0]={server!r}, want {expected_issuer!r}")
parsed = urlparse(server)
if parsed.scheme != "https" or not parsed.netloc:
    raise SystemExit(f"authorization server must be an absolute https URL, got {server!r}")
if "header" not in data.get("bearer_methods_supported", []):
    raise SystemExit("bearer_methods_supported must include header")
scopes = data.get("scopes_supported")
if not isinstance(scopes, list) or not scopes:
    raise SystemExit("scopes_supported must be a non-empty list")
PY

if [[ -n "$expect_dcr" ]]; then
  echo "== authorization-server metadata with DCR =="
  auth_server="$(python3 - "$tmpdir/metadata.json" <<'PY'
import json
import sys

with open(sys.argv[1], "r", encoding="utf-8") as f:
    data = json.load(f)
print(data["authorization_servers"][0].rstrip("/"))
PY
)"
  if [[ "$auth_server" != "$base" ]]; then
    echo "DCR mode expected authorization_servers[0] to be $base, got $auth_server" >&2
    exit 1
  fi
  curl -fsS "$base/.well-known/oauth-authorization-server" -o "$tmpdir/auth-metadata.json"
  python3 - "$tmpdir/auth-metadata.json" "$base/register" <<'PY'
import json
import sys

with open(sys.argv[1], "r", encoding="utf-8") as f:
    data = json.load(f)
if data.get("registration_endpoint") != sys.argv[2]:
    raise SystemExit(f"registration_endpoint={data.get('registration_endpoint')!r}")
if "S256" not in data.get("code_challenge_methods_supported", []):
    raise SystemExit("code_challenge_methods_supported must include S256")
if "none" not in data.get("token_endpoint_auth_methods_supported", []):
    raise SystemExit("token_endpoint_auth_methods_supported must include none")
if data.get("authorization_endpoint", "").endswith("/oauth2/authorize") is False:
    raise SystemExit("authorization_endpoint must point at Cognito /oauth2/authorize")
if data.get("token_endpoint", "").endswith("/oauth2/token") is False:
    raise SystemExit("token_endpoint must point at Cognito /oauth2/token")
PY
fi

echo "== endpoint-scoped protected-resource metadata =="
curl -fsS "$metadata_mcp_url" -o "$tmpdir/metadata-mcp.json"
python3 - "$tmpdir/metadata-mcp.json" "$mcp_url" <<'PY'
import json
import sys

with open(sys.argv[1], "r", encoding="utf-8") as f:
    data = json.load(f)
if data.get("resource") != sys.argv[2]:
    raise SystemExit("endpoint-scoped metadata resource mismatch")
PY

if [[ -n "$origin" ]]; then
  echo "== CORS preflight =="
  status="$(curl -sS -D "$tmpdir/preflight.headers" -o "$tmpdir/preflight.out" -w "%{http_code}" \
    -X OPTIONS "$mcp_url" \
    -H "Origin: $origin" \
    -H "Access-Control-Request-Method: POST" \
    -H "Access-Control-Request-Headers: Authorization, Content-Type")"
  if [[ "$status" != "204" ]]; then
    echo "preflight returned HTTP $status, expected 204" >&2
    exit 1
  fi
  tr -d '\r' < "$tmpdir/preflight.headers" > "$tmpdir/preflight.headers.clean"
  if ! grep -Fqi "Access-Control-Allow-Origin: $origin" "$tmpdir/preflight.headers.clean"; then
    echo "preflight did not allow expected origin $origin" >&2
    exit 1
  fi
fi

echo "== unauthenticated GET challenge =="
status="$(curl -sS -D "$tmpdir/unauth-get.headers" -o "$tmpdir/unauth-get.out" -w "%{http_code}" \
  -X GET "$mcp_url" \
  "${curl_headers[@]}")"
if [[ "$status" != "401" ]]; then
  echo "unauthenticated GET /mcp returned HTTP $status, expected 401" >&2
  exit 1
fi
tr -d '\r' < "$tmpdir/unauth-get.headers" > "$tmpdir/unauth-get.headers.clean"
if ! grep -iq '^WWW-Authenticate: Bearer .*resource_metadata=' "$tmpdir/unauth-get.headers.clean"; then
  echo "missing GET WWW-Authenticate Bearer resource_metadata challenge" >&2
  exit 1
fi
if ! grep -iq '^WWW-Authenticate: Bearer .*scope=' "$tmpdir/unauth-get.headers.clean"; then
  echo "missing GET WWW-Authenticate scope" >&2
  exit 1
fi

echo "== unauthenticated POST challenge =="
status="$(curl -sS -D "$tmpdir/unauth-post.headers" -o "$tmpdir/unauth-post.out" -w "%{http_code}" \
  -X POST "$mcp_url" \
  "${curl_headers[@]}" \
  -H "Content-Type: application/json" \
  -d "$payload")"
if [[ "$status" != "401" ]]; then
  echo "unauthenticated POST /mcp returned HTTP $status, expected 401" >&2
  exit 1
fi
tr -d '\r' < "$tmpdir/unauth-post.headers" > "$tmpdir/unauth-post.headers.clean"
if ! grep -iq '^WWW-Authenticate: Bearer .*resource_metadata=' "$tmpdir/unauth-post.headers.clean"; then
  echo "missing WWW-Authenticate Bearer resource_metadata challenge" >&2
  exit 1
fi
if ! grep -iq '^WWW-Authenticate: Bearer .*scope=' "$tmpdir/unauth-post.headers.clean"; then
  echo "missing WWW-Authenticate scope" >&2
  exit 1
fi

if [[ -z "$token" ]]; then
  echo "ok: metadata and unauthenticated OAuth challenge passed"
  echo "note: pass --token or GONGMCP_GATEWAY_SMOKE_TOKEN to test authenticated tools/list"
  exit 0
fi

echo "== authenticated tools/list =="
status="$(curl -sS -o "$tmpdir/tools.json" -w "%{http_code}" \
  -X POST "$mcp_url" \
  "${curl_headers[@]}" \
  -H "Authorization: Bearer ${token}" \
  -H "Content-Type: application/json" \
  -d "$payload")"
if [[ "$status" != "200" ]]; then
  echo "authenticated tools/list returned HTTP $status" >&2
  exit 1
fi
if ! grep -q '"tools"' "$tmpdir/tools.json"; then
  echo "authenticated response did not include tools" >&2
  exit 1
fi

echo "ok: gateway smoke completed"
