#!/usr/bin/env bash
set -euo pipefail

LAB_VM="${LAB_VM:-root@192.168.1.205}"
REMOTE_LAB="${REMOTE_LAB:-/srv/gongctl/source/deploy/lab-auth}"

ssh "$LAB_VM" "if [ -d '$REMOTE_LAB' ]; then cd '$REMOTE_LAB' && docker-compose down; fi"
