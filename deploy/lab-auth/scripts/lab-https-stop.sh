#!/usr/bin/env bash
set -euo pipefail

LAB_VM="${LAB_VM:-root@192.168.1.205}"
TUNNEL_CONTAINER="${TUNNEL_CONTAINER:-gongctl-lab-quick-tunnel}"

ssh "$LAB_VM" "docker rm -f '$TUNNEL_CONTAINER' >/dev/null 2>&1 || true"
