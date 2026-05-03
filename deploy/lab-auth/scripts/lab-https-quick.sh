#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
LAB_VM="${LAB_VM:-root@192.168.1.205}"
TUNNEL_CONTAINER="${TUNNEL_CONTAINER:-gongctl-lab-quick-tunnel}"
CLOUDFLARED_IMAGE="${CLOUDFLARED_IMAGE:-cloudflare/cloudflared:2026.3.0}"

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
LAB_PUBLIC_BASE_URL="$url" "$ROOT/deploy/lab-auth/scripts/lab-up.sh"
LAB_PUBLIC_BASE_URL="$url" "$ROOT/deploy/lab-auth/scripts/lab-smoke.sh"

cat <<EOF

HTTPS lab is live:
  $url/mcp

This is an ephemeral Cloudflare quick tunnel. Use it for ChatGPT/API HTTPS
smoke testing only; use a named Cloudflare Tunnel plus DNS route for a stable
enterprise rehearsal URL.
EOF
