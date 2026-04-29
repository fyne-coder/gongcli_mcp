#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Usage: scripts/docker-smoke.sh [options]

Runs a small real-data Docker smoke:
  1. gongctl auth check
  2. gongctl sync calls --preset minimal --max-pages 1
  3. gongctl sync status
  4. gongmcp tools/list from the MCP-only image over the same mounted SQLite DB

Options:
  --image IMAGE       CLI container image to test (default: GONGCTL_IMAGE or gongctl:local)
  --mcp-image IMAGE   MCP-only image to test (default: GONGCTL_MCP_IMAGE or gongctl:mcp-local)
  --data-dir DIR     Host data directory to mount (default: GONGCTL_DATA_DIR or ~/gongctl-data)
  --db NAME          SQLite filename inside the data dir (default: GONGCTL_DB_NAME or gong-smoke.db)
  --env-file FILE    Optional env file for Gong credentials (default: GONGCTL_ENV_FILE or .env)
  --from DATE        Sync start date, YYYY-MM-DD (default: GONGCTL_FROM or 7 ET days ago)
  --to DATE          Sync end date, YYYY-MM-DD (default: GONGCTL_TO or today in ET)
  -h, --help         Show this help

Credential input:
  If --env-file exists, it is passed to Docker with --env-file.
  Otherwise, GONG_ACCESS_KEY, GONG_ACCESS_KEY_SECRET, and GONG_BASE_URL are passed from the host environment.
USAGE
}

image="${GONGCTL_IMAGE:-gongctl:local}"
mcp_image="${GONGCTL_MCP_IMAGE:-gongctl:mcp-local}"
data_dir="${GONGCTL_DATA_DIR:-$HOME/gongctl-data}"
db_name="${GONGCTL_DB_NAME:-gong-smoke.db}"
env_file="${GONGCTL_ENV_FILE:-.env}"
from="${GONGCTL_FROM:-}"
to="${GONGCTL_TO:-}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --image)
      image="${2:?--image requires a value}"
      shift 2
      ;;
    --mcp-image)
      mcp_image="${2:?--mcp-image requires a value}"
      shift 2
      ;;
    --data-dir)
      data_dir="${2:?--data-dir requires a value}"
      shift 2
      ;;
    --db)
      db_name="${2:?--db requires a value}"
      shift 2
      ;;
    --env-file)
      env_file="${2:?--env-file requires a value}"
      shift 2
      ;;
    --from)
      from="${2:?--from requires a value}"
      shift 2
      ;;
    --to)
      to="${2:?--to requires a value}"
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

et_date() {
  local offset_days="$1"
  if TZ=America/New_York date -d "${offset_days} days" +%F >/dev/null 2>&1; then
    TZ=America/New_York date -d "${offset_days} days" +%F
    return
  fi
  if [[ "$offset_days" == -* ]]; then
    TZ=America/New_York date -v"${offset_days}"d +%F
  else
    TZ=America/New_York date -v+"${offset_days}"d +%F
  fi
}

if [[ -z "$from" ]]; then
  from="$(et_date -7)"
fi
if [[ -z "$to" ]]; then
  to="$(et_date 0)"
fi

mkdir -p "$data_dir"
data_dir="$(cd "$data_dir" && pwd)"
db_path="/data/$db_name"
host_db_path="$data_dir/$db_name"

env_args=()
if [[ -f "$env_file" ]]; then
  env_args=(--env-file "$env_file")
else
  env_args=(-e GONG_ACCESS_KEY -e GONG_ACCESS_KEY_SECRET -e GONG_BASE_URL)
fi

write_user_args=()
if command -v id >/dev/null 2>&1; then
  write_user_args=(--user "$(id -u):$(id -g)")
fi

echo "image: $image"
echo "mcp_image: $mcp_image"
echo "data_dir: $data_dir"
echo "db: $host_db_path"
echo "from: $from"
echo "to: $to"

echo "== auth check =="
docker run --rm \
  "${write_user_args[@]}" \
  "${env_args[@]}" \
  -v "$data_dir:/data" \
  "$image" \
  auth check

echo "== sync calls =="
docker run --rm \
  "${write_user_args[@]}" \
  "${env_args[@]}" \
  -v "$data_dir:/data" \
  "$image" \
  sync calls --db "$db_path" --from "$from" --to "$to" --preset minimal --max-pages 1

echo "== sync status =="
docker run --rm \
  "${write_user_args[@]}" \
  -v "$data_dir:/data" \
  "$image" \
  sync status --db "$db_path"

echo "== mcp tools/list =="
mcp_output="$(mktemp "${TMPDIR:-/tmp}/gongctl-mcp-smoke.XXXXXX")"
trap 'rm -f "$mcp_output"' EXIT

printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"docker-smoke","version":"0"}}}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' \
| docker run --rm -i \
    --network none \
    "${write_user_args[@]}" \
    -v "$data_dir:/data:ro" \
    "$mcp_image" \
    --db "$db_path" \
  > "$mcp_output"

if ! grep -q '"get_sync_status"' "$mcp_output"; then
  echo "MCP tools/list did not include get_sync_status" >&2
  echo "MCP output saved at: $mcp_output" >&2
  trap - EXIT
  exit 1
fi

echo "ok: Docker smoke completed"
