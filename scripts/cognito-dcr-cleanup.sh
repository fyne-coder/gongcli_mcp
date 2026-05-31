#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Usage: scripts/cognito-dcr-cleanup.sh --user-pool-id POOL_ID [options]

Lists Cognito app clients created by gongmcp-gateway DCR by client-name prefix.
Deletion requires an explicit confirmation flag.

Options:
  --user-pool-id ID   Cognito user pool ID (or set COGNITO_USER_POOL_ID)
  --region REGION     AWS region (default: AWS_REGION or AWS_DEFAULT_REGION)
  --prefix PREFIX     Client name prefix (default: gongmcp-dcr)
  --confirm-delete    Delete matched clients (default: list only)
  -h, --help          Show this help

Environment fallbacks:
  COGNITO_USER_POOL_ID
  COGNITO_DCR_CLIENT_NAME_PREFIX
  AWS_REGION
  AWS_DEFAULT_REGION

This script uses aws cognito-idp only. It does not print tokens or secrets.
USAGE
}

user_pool_id="${COGNITO_USER_POOL_ID:-}"
region="${AWS_REGION:-${AWS_DEFAULT_REGION:-}}"
prefix="${COGNITO_DCR_CLIENT_NAME_PREFIX:-gongmcp-dcr}"
confirm_delete=""

while [[ $# -gt 0 ]]; do
  case "$1" in
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
    --confirm-delete)
      confirm_delete="1"
      shift
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

if [[ -z "$user_pool_id" ]]; then
  usage >&2
  exit 2
fi
if [[ -z "$region" ]]; then
  echo "AWS region is required via --region, AWS_REGION, or AWS_DEFAULT_REGION" >&2
  exit 2
fi
if [[ -z "$prefix" ]]; then
  echo "prefix must not be empty" >&2
  exit 2
fi

if ! command -v aws >/dev/null 2>&1; then
  echo "aws CLI is required" >&2
  exit 1
fi

tmpdir="$(mktemp -d "${TMPDIR:-/tmp}/cognito-dcr-cleanup.XXXXXX")"
trap 'rm -rf "$tmpdir"' EXIT

matched="$tmpdir/matched.tsv"
: > "$matched"

next_token=""
while :; do
  args=(cognito-idp list-user-pool-clients --region "$region" --user-pool-id "$user_pool_id" --max-results 60 --output json)
  if [[ -n "$next_token" ]]; then
    args+=(--next-token "$next_token")
  fi
  aws "${args[@]}" > "$tmpdir/clients.json"
  next_token="$(python3 - "$tmpdir/clients.json" "$prefix" "$matched" <<'PY'
import json
import sys

with open(sys.argv[1], "r", encoding="utf-8") as handle:
    data = json.load(handle)
prefix = sys.argv[2]
out_path = sys.argv[3]
needle = prefix + "-"
with open(out_path, "a", encoding="utf-8") as out:
    for item in data.get("UserPoolClients") or []:
        name = item.get("ClientName") or ""
        client_id = item.get("ClientId") or ""
        if name.startswith(needle):
            out.write(f"{client_id}\t{name}\n")
print(data.get("NextToken") or "")
PY
)"
  if [[ -z "$next_token" ]]; then
    break
  fi
done

if [[ ! -s "$matched" ]]; then
  echo "no Cognito app clients matched prefix ${prefix}-"
  exit 0
fi

echo "matched Cognito app clients (prefix ${prefix}-):"
while IFS=$'\t' read -r client_id client_name; do
  echo "  ${client_name} (${client_id})"
done < "$matched"

if [[ -z "$confirm_delete" ]]; then
  echo "list-only mode: pass --confirm-delete to delete matched clients"
  exit 0
fi

deleted=0
while IFS=$'\t' read -r client_id client_name; do
  aws cognito-idp delete-user-pool-client \
    --region "$region" \
    --user-pool-id "$user_pool_id" \
    --client-id "$client_id" >/dev/null
  echo "deleted ${client_name}"
  deleted=$((deleted + 1))
done < "$matched"

echo "deleted ${deleted} Cognito app client(s)"
