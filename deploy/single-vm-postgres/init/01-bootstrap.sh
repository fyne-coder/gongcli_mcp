#!/usr/bin/env sh
set -eu

for name in GONGCTL_SOURCE_DB GONGCTL_MCP_DB GONGMCP_READER_ROLE; do
  value="$(eval "printf '%s' \"\${$name:-}\"")"
  case "$value" in
    ''|*[!A-Za-z0-9_]*)
      echo "$name must contain only letters, numbers, and underscores" >&2
      exit 1
      ;;
  esac
done

if [ -z "${GONGMCP_READER_PASSWORD:-}" ]; then
  echo "GONGMCP_READER_PASSWORD is required" >&2
  exit 1
fi

create_database_if_missing() {
  db_name="$1"
  if ! psql -v ON_ERROR_STOP=1 --set=db_name="$db_name" --username "$POSTGRES_USER" --dbname postgres -tAc "SELECT 1 FROM pg_database WHERE datname = :'db_name'" | grep -qx 1; then
    createdb --username "$POSTGRES_USER" "$db_name"
  fi
}

create_database_if_missing "$GONGCTL_SOURCE_DB"
create_database_if_missing "$GONGCTL_MCP_DB"

psql -v ON_ERROR_STOP=1 \
  --set=reader_role="$GONGMCP_READER_ROLE" \
  --set=reader_password="$GONGMCP_READER_PASSWORD" \
  --set=mcp_db="$GONGCTL_MCP_DB" \
  --username "$POSTGRES_USER" \
  --dbname postgres <<'SQL'
DO $$
DECLARE
  target_role text := :'reader_role';
  target_password text := :'reader_password';
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = target_role) THEN
    EXECUTE format('CREATE ROLE %I LOGIN NOINHERIT PASSWORD %L', target_role, target_password);
  ELSE
    EXECUTE format('ALTER ROLE %I WITH LOGIN NOINHERIT PASSWORD %L', target_role, target_password);
  END IF;
END
$$;
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
GRANT CONNECT ON DATABASE :"mcp_db" TO :"reader_role";
SQL
