#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Usage: scripts/smoke-cognito-dcr.sh --url URL [options]

Verifies a DCR-enabled gongmcp-gateway without printing tokens or secrets:
  1. protected-resource metadata points at the gateway auth server
  2. authorization-server metadata exposes /register and Cognito endpoints
  3. optional POST /register creates a Cognito app client only when
     --create-client is passed explicitly
  4. optional Cognito describe/delete checks use aws cognito-idp only

Options:
  --url URL                 Public gateway base URL
  --user-pool-id ID         Cognito user pool ID for CLI verification
  --region REGION           AWS region (default: AWS_REGION or AWS_DEFAULT_REGION)
  --prefix PREFIX           Expected DCR client name prefix (default: gongmcp-dcr)
  --redirect-uri URI        Redirect URI for registration; first comma/semicolon/space entry is used
  --scope SCOPE             Scopes for registration; commas are normalized to spaces
  --create-client           POST /register and verify the client in Cognito
  --delete-created-client   Delete the created client after verification
  --origin ORIGIN           Optional Origin header for metadata/CORS checks
  -h, --help                Show this help

Environment fallbacks:
  GONGMCP_GATEWAY_SMOKE_URL
  GONGMCP_GATEWAY_SMOKE_ORIGIN
  COGNITO_USER_POOL_ID
  COGNITO_DCR_ALLOWED_REDIRECT_URIS
  COGNITO_DCR_ALLOWED_SCOPES
  COGNITO_DCR_CLIENT_NAME_PREFIX
  AWS_REGION
  AWS_DEFAULT_REGION

Never paste customer tokens into public JWT decoder sites. This script does
not echo token values or client secrets.
USAGE
}

url="${GONGMCP_GATEWAY_SMOKE_URL:-}"
user_pool_id="${COGNITO_USER_POOL_ID:-}"
region="${AWS_REGION:-${AWS_DEFAULT_REGION:-}}"
prefix="${COGNITO_DCR_CLIENT_NAME_PREFIX:-gongmcp-dcr}"
redirect_uri="${COGNITO_DCR_ALLOWED_REDIRECT_URIS:-https://claude.ai/api/mcp/auth_callback}"
scope="${COGNITO_DCR_ALLOWED_SCOPES:-openid email gongmcp/read}"
origin="${GONGMCP_GATEWAY_SMOKE_ORIGIN:-}"
create_client=""
delete_created_client=""
created_client_id=""

cleanup() {
  local status="$?"
  if [[ "$status" != "0" && -n "$delete_created_client" && -n "$created_client_id" ]]; then
    aws cognito-idp delete-user-pool-client \
      --region "$region" \
      --user-pool-id "$user_pool_id" \
      --client-id "$created_client_id" >/dev/null || true
    echo "deleted registered smoke client after failure" >&2
  fi
  rm -rf "$tmpdir"
  exit "$status"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --url)
      url="${2:?--url requires a value}"
      shift 2
      ;;
    --user-pool-id)
      user_pool_id="${2:?--user-pool-id requires a value}"
      shift 2
      ;;
    --region)
      region="${2:?--region requires a value}"
      shift 2
      ;;
    --prefix)
      prefix="${2:?--prefix requires a value}"
      shift 2
      ;;
    --redirect-uri)
      redirect_uri="${2:?--redirect-uri requires a value}"
      shift 2
      ;;
    --scope)
      scope="${2:?--scope requires a value}"
      shift 2
      ;;
    --create-client)
      create_client="1"
      shift
      ;;
    --delete-created-client)
      delete_created_client="1"
      shift
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
scope="${scope//,/ }"
redirect_uri="${redirect_uri%%,*}"
redirect_uri="${redirect_uri%%;*}"
redirect_uri="${redirect_uri%% *}"
if [[ -z "$redirect_uri" ]]; then
  echo "redirect URI must not be empty" >&2
  exit 2
fi

base="${url%/}"
if [[ "$base" == */mcp ]]; then
  base="${base%/mcp}"
fi

tmpdir="$(mktemp -d "${TMPDIR:-/tmp}/smoke-cognito-dcr.XXXXXX")"
trap cleanup EXIT

curl_headers=()
if [[ -n "$origin" ]]; then
  curl_headers+=(-H "Origin: $origin")
fi

echo "== protected-resource metadata =="
curl -fsS "${curl_headers[@]}" "$base/.well-known/oauth-protected-resource" -o "$tmpdir/metadata.json"
python3 - "$tmpdir/metadata.json" "$base" <<'PY'
import json
import sys

with open(sys.argv[1], "r", encoding="utf-8") as handle:
    data = json.load(handle)
expected_base = sys.argv[2].rstrip("/")
servers = data.get("authorization_servers") or []
if not servers:
    raise SystemExit("authorization_servers must be non-empty")
if servers[0].rstrip("/") != expected_base:
    raise SystemExit(
        f"DCR mode expected authorization_servers[0]={expected_base!r}, got {servers[0]!r}"
    )
PY

echo "== authorization-server metadata =="
curl -fsS "${curl_headers[@]}" "$base/.well-known/oauth-authorization-server" -o "$tmpdir/auth-metadata.json"
python3 - "$tmpdir/auth-metadata.json" "$base/register" <<'PY'
import json
import sys

with open(sys.argv[1], "r", encoding="utf-8") as handle:
    data = json.load(handle)
if data.get("registration_endpoint") != sys.argv[2]:
    raise SystemExit(f"registration_endpoint={data.get('registration_endpoint')!r}")
if "S256" not in (data.get("code_challenge_methods_supported") or []):
    raise SystemExit("code_challenge_methods_supported must include S256")
if "none" not in (data.get("token_endpoint_auth_methods_supported") or []):
    raise SystemExit("token_endpoint_auth_methods_supported must include none")
if not str(data.get("authorization_endpoint", "")).endswith("/oauth2/authorize"):
    raise SystemExit("authorization_endpoint must point at Cognito /oauth2/authorize")
if not str(data.get("token_endpoint", "")).endswith("/oauth2/token"):
    raise SystemExit("token_endpoint must point at Cognito /oauth2/token")
PY

if [[ -z "$create_client" ]]; then
  echo "ok: DCR metadata smoke passed"
  echo "note: pass --create-client to exercise POST /register against live Cognito"
  exit 0
fi

if [[ -z "$user_pool_id" ]]; then
  echo "--create-client requires --user-pool-id or COGNITO_USER_POOL_ID" >&2
  exit 2
fi
if [[ -z "$region" ]]; then
  echo "--create-client requires --region, AWS_REGION, or AWS_DEFAULT_REGION" >&2
  exit 2
fi
if ! command -v aws >/dev/null 2>&1; then
  echo "aws CLI is required for --create-client" >&2
  exit 1
fi

register_payload="$(python3 - "$redirect_uri" "$scope" <<'PY'
import json
import sys

print(json.dumps({
    "redirect_uris": [sys.argv[1]],
    "token_endpoint_auth_method": "none",
    "grant_types": ["authorization_code"],
    "response_types": ["code"],
    "client_name": "smoke-cognito-dcr",
    "scope": sys.argv[2],
}, separators=(",", ":")))
PY
)"

echo "== POST /register =="
status="$(curl -sS "${curl_headers[@]}" -o "$tmpdir/register.json" -w "%{http_code}" \
  -X POST "$base/register" \
  -H "Content-Type: application/json" \
  -d "$register_payload")"
if [[ "$status" != "201" ]]; then
  echo "POST /register returned HTTP $status" >&2
  python3 - "$tmpdir/register.json" <<'PY'
import json
import sys

with open(sys.argv[1], "r", encoding="utf-8") as handle:
    try:
        data = json.load(handle)
    except json.JSONDecodeError:
        raise SystemExit("registration response was not JSON")
for key in ("error", "error_description"):
    if key in data:
        print(f"{key}: {data[key]}", file=sys.stderr)
PY
  exit 1
fi

read -r created_client_id created_client_name <<<"$(python3 - "$tmpdir/register.json" "$prefix" <<'PY'
import json
import sys

with open(sys.argv[1], "r", encoding="utf-8") as handle:
    data = json.load(handle)
client_id = (data.get("client_id") or "").strip()
client_name = (data.get("client_name") or "").strip()
prefix = sys.argv[2]
if not client_id:
    raise SystemExit("registration response missing client_id")
if client_name and not client_name.startswith(prefix + "-"):
    raise SystemExit(f"registered client_name={client_name!r} does not match prefix {prefix!r}")
print(client_id, client_name)
PY
)"

echo "registered Cognito app client ${created_client_name:-<unnamed>} (${created_client_id})"

echo "== describe-user-pool-client =="
aws cognito-idp describe-user-pool-client \
  --region "$region" \
  --user-pool-id "$user_pool_id" \
  --client-id "$created_client_id" \
  --output json > "$tmpdir/describe.json"

python3 - "$tmpdir/describe.json" "$prefix" "$redirect_uri" <<'PY'
import json
import sys

with open(sys.argv[1], "r", encoding="utf-8") as handle:
    data = json.load(handle)
client = data.get("UserPoolClient") or {}
name = client.get("ClientName") or ""
prefix = sys.argv[2]
redirect_uri = sys.argv[3]
if not name.startswith(prefix + "-"):
    raise SystemExit(f"describe client_name={name!r} does not match prefix {prefix!r}")
callbacks = client.get("CallbackURLs") or []
if redirect_uri not in callbacks:
    raise SystemExit(f"describe missing redirect URI {redirect_uri!r}")
if client.get("GenerateSecret") is True:
    raise SystemExit("DCR client must not have a generated secret")
PY

echo "verified Cognito app client via aws cognito-idp describe-user-pool-client"

if [[ -n "$delete_created_client" ]]; then
  echo "== delete created client =="
  aws cognito-idp delete-user-pool-client \
    --region "$region" \
    --user-pool-id "$user_pool_id" \
    --client-id "$created_client_id" >/dev/null
  echo "deleted registered smoke client"
  created_client_id=""
fi

echo "ok: Cognito DCR smoke completed"
