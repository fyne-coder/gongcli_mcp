#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
LAB_VM="${LAB_VM:-}"
REMOTE_ROOT="${REMOTE_ROOT:-/srv/gongctl}"
LAB_POSTGRES_PORT="${LAB_POSTGRES_PORT:-55432}"
LAB_POSTGRES_CONTAINER="${LAB_POSTGRES_CONTAINER:-gongctl-lab-postgres}"
LAB_POSTGRES_IMAGE="${LAB_POSTGRES_IMAGE:-postgres:16-alpine@sha256:4e6e670bb069649261c9c18031f0aded7bb249a5b6664ddec29c013a89310d50}"
LAB_POSTGRES_DB="${LAB_POSTGRES_DB:-gongctl}"
LAB_POSTGRES_USER="${LAB_POSTGRES_USER:-gongctl}"
REMOTE_SOURCE="$REMOTE_ROOT/source"

require_env() {
  local name="$1"
  if [[ -z "${!name:-}" ]]; then
    echo "$name is required" >&2
    exit 1
  fi
}

case "$LAB_VM" in
  -*|*[[:space:]]*)
    echo "LAB_VM must be an ssh host target, not an option or whitespace-containing value" >&2
    exit 2
    ;;
esac
case "$REMOTE_ROOT" in
  /*) ;;
  *) echo "REMOTE_ROOT must be an absolute remote path" >&2; exit 2 ;;
esac
case "$LAB_POSTGRES_PORT" in
  ''|*[!0-9]*) echo "LAB_POSTGRES_PORT must be numeric" >&2; exit 2 ;;
esac
case "$LAB_POSTGRES_CONTAINER" in
  ''|*[!a-zA-Z0-9_.-]*) echo "LAB_POSTGRES_CONTAINER contains unsupported characters" >&2; exit 2 ;;
esac

require_env LAB_VM

ssh "$LAB_VM" "set -euo pipefail
mkdir -p '$REMOTE_ROOT/postgres-data' '$REMOTE_ROOT/secrets'
if [ ! -f '$REMOTE_ROOT/secrets/lab_postgres_writer_password' ]; then
  openssl rand -hex 24 | tr -d '\n' > '$REMOTE_ROOT/secrets/lab_postgres_writer_password'
fi
if [ ! -f '$REMOTE_ROOT/secrets/lab_postgres_reader_password' ]; then
  openssl rand -hex 24 | tr -d '\n' > '$REMOTE_ROOT/secrets/lab_postgres_reader_password'
fi
chmod 600 '$REMOTE_ROOT/secrets/lab_postgres_writer_password' '$REMOTE_ROOT/secrets/lab_postgres_reader_password'
writer_password=\$(cat '$REMOTE_ROOT/secrets/lab_postgres_writer_password')
reader_password=\$(cat '$REMOTE_ROOT/secrets/lab_postgres_reader_password')
if ! docker network inspect lab-auth_default >/dev/null 2>&1; then
  docker network create lab-auth_default >/dev/null
fi
if docker ps -a --format '{{.Names}}' | grep -qx '$LAB_POSTGRES_CONTAINER'; then
  docker rm -f '$LAB_POSTGRES_CONTAINER' >/dev/null
fi
docker pull '$LAB_POSTGRES_IMAGE' >/dev/null
docker run -d \
  --name '$LAB_POSTGRES_CONTAINER' \
  --restart unless-stopped \
  --network lab-auth_default \
  -p 127.0.0.1:$LAB_POSTGRES_PORT:5432 \
  -e POSTGRES_DB='$LAB_POSTGRES_DB' \
  -e POSTGRES_USER='$LAB_POSTGRES_USER' \
  -e POSTGRES_PASSWORD=\"\$writer_password\" \
  -e GONGMCP_READER_PASSWORD=\"\$reader_password\" \
  -v '$REMOTE_ROOT/postgres-data:/var/lib/postgresql/data' \
  -v '$REMOTE_SOURCE/deploy/postgres/init:/docker-entrypoint-initdb.d:ro' \
  '$LAB_POSTGRES_IMAGE' >/dev/null
for _ in \$(seq 1 90); do
  if docker exec '$LAB_POSTGRES_CONTAINER' pg_isready -U '$LAB_POSTGRES_USER' -d '$LAB_POSTGRES_DB' >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
docker exec '$LAB_POSTGRES_CONTAINER' pg_isready -U '$LAB_POSTGRES_USER' -d '$LAB_POSTGRES_DB' >/dev/null
cat > '$REMOTE_ROOT/secrets/lab_postgres_urls.env' <<EOF
GONGCTL_WRITER_DATABASE_URL=postgres://$LAB_POSTGRES_USER:\$writer_password@$LAB_POSTGRES_CONTAINER:5432/$LAB_POSTGRES_DB?sslmode=disable
GONGMCP_READER_DATABASE_URL=postgres://gongmcp_reader:\$reader_password@$LAB_POSTGRES_CONTAINER:5432/$LAB_POSTGRES_DB?sslmode=disable
GONGCTL_TUNNEL_WRITER_DATABASE_URL=postgres://$LAB_POSTGRES_USER:\$writer_password@127.0.0.1:$LAB_POSTGRES_PORT/$LAB_POSTGRES_DB?sslmode=disable
EOF
chmod 600 '$REMOTE_ROOT/secrets/lab_postgres_urls.env'
"

echo "lab postgres is running on $LAB_VM as $LAB_POSTGRES_CONTAINER"
echo "for local sync, open an SSH tunnel with:"
echo "ssh -N -L 127.0.0.1:$LAB_POSTGRES_PORT:127.0.0.1:$LAB_POSTGRES_PORT $LAB_VM"
echo "retrieve the writer URL from $REMOTE_ROOT/secrets/lab_postgres_urls.env on the VM"
