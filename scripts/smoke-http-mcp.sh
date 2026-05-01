#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Usage: scripts/smoke-http-mcp.sh --url URL --token TOKEN --origin ORIGIN [options]

Validates a customer-hosted HTTP MCP bridge without logging payload contents:
  1. GET /healthz returns valid JSON
  2. allowed OPTIONS /mcp returns 204 with CORS headers
  3. missing bearer token returns 401
  4. bad Origin returns 403
  5. authorized tools/list returns an MCP response

Options:
  --url URL            Base URL or /mcp URL, for example https://mcp.example.com
  --token TOKEN        Bearer token for the internal bridge
  --origin ORIGIN      Approved browser/client Origin
  --bad-origin ORIGIN  Disallowed Origin (default: https://not-approved.example.com)
  -h, --help           Show this help

Environment fallbacks:
  GONGMCP_SMOKE_URL, GONGMCP_SMOKE_TOKEN, GONGMCP_SMOKE_ORIGIN,
  GONGMCP_SMOKE_BAD_ORIGIN
USAGE
}

url="${GONGMCP_SMOKE_URL:-}"
token="${GONGMCP_SMOKE_TOKEN:-}"
origin="${GONGMCP_SMOKE_ORIGIN:-}"
bad_origin="${GONGMCP_SMOKE_BAD_ORIGIN:-https://not-approved.example.com}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --url)
      url="${2:?--url requires a value}"
      shift 2
      ;;
    --token)
      token="${2:?--token requires a value}"
      shift 2
      ;;
    --origin)
      origin="${2:?--origin requires a value}"
      shift 2
      ;;
    --bad-origin)
      bad_origin="${2:?--bad-origin requires a value}"
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

if [[ -z "$url" || -z "$token" || -z "$origin" ]]; then
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
health_url="$base/healthz"
payload='{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}'

tmpdir="$(mktemp -d "${TMPDIR:-/tmp}/gongmcp-http-smoke.XXXXXX")"
trap 'rm -rf "$tmpdir"' EXIT

echo "== healthz =="
curl -fsS "$health_url" -o "$tmpdir/health.json"
python3 - "$tmpdir/health.json" <<'PY'
import json
import sys

with open(sys.argv[1], "r", encoding="utf-8") as f:
    data = json.load(f)
if data.get("status") != "ok" or data.get("service") != "gongmcp":
    raise SystemExit(f"unexpected health payload: {data!r}")
PY

echo "== allowed preflight =="
status="$(curl -sS -D "$tmpdir/preflight.headers" -o "$tmpdir/preflight.out" -w "%{http_code}" -X OPTIONS "$mcp_url" \
  -H "Origin: $origin" \
  -H "Access-Control-Request-Method: POST" \
  -H "Access-Control-Request-Headers: authorization,content-type")"
if [[ "$status" != "204" ]]; then
  echo "allowed preflight returned HTTP $status" >&2
  exit 1
fi
tr -d '\r' < "$tmpdir/preflight.headers" > "$tmpdir/preflight.headers.clean"
if ! grep -Fxq "Access-Control-Allow-Origin: $origin" "$tmpdir/preflight.headers.clean"; then
  echo "allowed preflight did not reflect approved origin" >&2
  exit 1
fi
if ! grep -Fq "Access-Control-Allow-Methods: POST, OPTIONS" "$tmpdir/preflight.headers.clean"; then
  echo "allowed preflight did not advertise POST, OPTIONS" >&2
  exit 1
fi
if ! grep -Fq "Access-Control-Allow-Headers: Authorization, Content-Type" "$tmpdir/preflight.headers.clean"; then
  echo "allowed preflight did not advertise required request headers" >&2
  exit 1
fi

echo "== missing bearer =="
status="$(curl -sS -o "$tmpdir/unauthorized.out" -w "%{http_code}" "$mcp_url" \
  -H "Origin: $origin" \
  -H "Content-Type: application/json" \
  -d "$payload")"
if [[ "$status" != "401" ]]; then
  echo "missing bearer returned HTTP $status" >&2
  exit 1
fi

echo "== bad origin =="
status="$(curl -sS -o "$tmpdir/bad-origin.out" -w "%{http_code}" "$mcp_url" \
  -H "Origin: $bad_origin" \
  -H "Authorization: Bearer $token" \
  -H "Content-Type: application/json" \
  -d "$payload")"
if [[ "$status" != "403" ]]; then
  echo "bad origin returned HTTP $status" >&2
  exit 1
fi

echo "== authorized tools/list =="
status="$(curl -sS -o "$tmpdir/tools.json" -w "%{http_code}" "$mcp_url" \
  -H "Origin: $origin" \
  -H "Authorization: Bearer $token" \
  -H "Content-Type: application/json" \
  -d "$payload")"
if [[ "$status" != "200" ]]; then
  echo "authorized tools/list returned HTTP $status" >&2
  exit 1
fi
if ! grep -q '"tools"' "$tmpdir/tools.json"; then
  echo "authorized response did not include tools" >&2
  exit 1
fi

echo "ok: HTTP MCP smoke completed"
