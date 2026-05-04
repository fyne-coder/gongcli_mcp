#!/usr/bin/env bash
# shellcheck disable=SC2029
set -euo pipefail

LAB_VM="${LAB_VM:-}"
REMOTE_ROOT="${REMOTE_ROOT:-/srv/gongctl}"
REMOTE_LAB="${REMOTE_LAB:-$REMOTE_ROOT/source/deploy/lab-auth}"

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
case "$REMOTE_LAB" in
  /*) ;;
  *) echo "REMOTE_LAB must be an absolute remote path" >&2; exit 2 ;;
esac
case "$REMOTE_LAB" in
  *\'*|*\"*|*[\;\`\$]*)
    echo "REMOTE_LAB contains unsupported shell metacharacters" >&2
    exit 2
    ;;
esac

ssh "$LAB_VM" "if [ -d '$REMOTE_LAB' ]; then cd '$REMOTE_LAB' && docker-compose down; fi"
