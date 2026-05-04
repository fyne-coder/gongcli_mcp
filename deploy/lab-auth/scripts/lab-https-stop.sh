#!/usr/bin/env bash
# shellcheck disable=SC2029
set -euo pipefail

LAB_VM="${LAB_VM:-}"
TUNNEL_CONTAINER="${TUNNEL_CONTAINER:-gongctl-lab-quick-tunnel}"

if [[ -z "$LAB_VM" ]]; then
  echo "LAB_VM is required, for example: LAB_VM=ssh-user@lab-host.example.com" >&2
  exit 1
fi
case "$LAB_VM" in
  -*|*[[:space:]]*)
    echo "LAB_VM must be an ssh host target, not an option or whitespace-containing value" >&2
    exit 2
    ;;
esac
case "$TUNNEL_CONTAINER" in
  *[!A-Za-z0-9_.-]*|"")
    echo "TUNNEL_CONTAINER must contain only letters, numbers, dot, underscore, or dash" >&2
    exit 2
    ;;
esac

ssh "$LAB_VM" "docker rm -f '$TUNNEL_CONTAINER' >/dev/null 2>&1 || true"
