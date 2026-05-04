#!/usr/bin/env bash
# shellcheck disable=SC2029
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
LAB_VM="${LAB_VM:-}"
LAB_DB="${LAB_DB:-}"
REMOTE_ROOT="${REMOTE_ROOT:-/srv/gongctl}"
TUNNEL_CONTAINER="${TUNNEL_CONTAINER:-gongctl-lab-quick-tunnel}"
CLOUDFLARED_IMAGE="${CLOUDFLARED_IMAGE:-cloudflare/cloudflared:2026.3.0}"

if [[ -z "$LAB_VM" ]]; then
  echo "LAB_VM is required, for example: LAB_VM=ssh-user@lab-host.example.com" >&2
  exit 1
fi
if [[ -z "$LAB_DB" ]]; then
  echo "LAB_DB is required, see deploy/lab-auth/.env.example" >&2
  exit 1
fi
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
case "$TUNNEL_CONTAINER" in
  *[!A-Za-z0-9_.-]*|"")
    echo "TUNNEL_CONTAINER must contain only letters, numbers, dot, underscore, or dash" >&2
    exit 2
    ;;
esac
case "$LAB_VM" in
  -*|*[[:space:]]*)
    echo "LAB_VM must be an ssh host target, not an option or whitespace-containing value" >&2
    exit 2
    ;;
esac
case "$CLOUDFLARED_IMAGE" in
  *[!A-Za-z0-9._:/@+-]*|"")
    echo "CLOUDFLARED_IMAGE must be a conservative Docker image reference" >&2
    exit 2
    ;;
esac

ssh "$LAB_VM" "
set -euo pipefail
docker rm -f '$TUNNEL_CONTAINER' >/dev/null 2>&1 || true
docker run -d \
  --name '$TUNNEL_CONTAINER' \
  --network host \
  --restart unless-stopped \
  '$CLOUDFLARED_IMAGE' \
  tunnel --no-autoupdate --url http://127.0.0.1:80 >/dev/null
"

url=""
for _ in $(seq 1 60); do
  logs="$(ssh "$LAB_VM" "docker logs '$TUNNEL_CONTAINER' 2>&1" || true)"
  url="$(printf '%s\n' "$logs" | grep -Eo 'https://[a-zA-Z0-9.-]+\.trycloudflare\.com' | tail -1 || true)"
  if [[ -n "$url" ]]; then
    break
  fi
  sleep 2
done

if [[ -z "$url" ]]; then
  echo "failed to discover trycloudflare URL" >&2
  ssh "$LAB_VM" "docker logs '$TUNNEL_CONTAINER' 2>&1 | tail -80" >&2 || true
  exit 1
fi

echo "quick HTTPS URL: $url"
LAB_VM="$LAB_VM" LAB_DB="$LAB_DB" REMOTE_ROOT="$REMOTE_ROOT" LAB_PUBLIC_BASE_URL="$url" "$ROOT/deploy/lab-auth/scripts/lab-up.sh"
LAB_VM="$LAB_VM" REMOTE_ROOT="$REMOTE_ROOT" LAB_PUBLIC_BASE_URL="$url" "$ROOT/deploy/lab-auth/scripts/lab-smoke.sh"

cat <<EOF

HTTPS lab is live:
  $url/mcp

This is an ephemeral Cloudflare quick tunnel. Use it for ChatGPT/API HTTPS
smoke testing only; use a named Cloudflare Tunnel plus DNS route for a stable
enterprise rehearsal URL.
EOF
