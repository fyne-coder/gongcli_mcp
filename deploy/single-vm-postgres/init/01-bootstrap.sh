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
  if ! psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname postgres -tAc "SELECT 1 FROM pg_database WHERE datname = '$db_name'" | grep -qx 1; then
    createdb --username "$POSTGRES_USER" "$db_name"
  fi
}

create_database_if_missing "$GONGCTL_SOURCE_DB"
create_database_if_missing "$GONGCTL_MCP_DB"

reader_password=$(printf "%s" "$GONGMCP_READER_PASSWORD" | sed "s/'/''/g")

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname postgres <<SQL
DO \$\$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '${GONGMCP_READER_ROLE}') THEN
    CREATE ROLE "${GONGMCP_READER_ROLE}" LOGIN NOINHERIT PASSWORD '${reader_password}';
  ELSE
    ALTER ROLE "${GONGMCP_READER_ROLE}" WITH LOGIN NOINHERIT PASSWORD '${reader_password}';
  END IF;
END
\$\$;
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
GRANT CONNECT ON DATABASE "${GONGCTL_MCP_DB}" TO "${GONGMCP_READER_ROLE}";
SQL
