#!/usr/bin/env sh
set -eu

if [ -z "${GONGMCP_READER_PASSWORD:-}" ]; then
  echo "GONGMCP_READER_PASSWORD is required" >&2
  exit 1
fi

reader_password=$(printf "%s" "$GONGMCP_READER_PASSWORD" | sed "s/'/''/g")

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<SQL
DO \$\$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'gongmcp_reader') THEN
    CREATE ROLE gongmcp_reader LOGIN PASSWORD '${reader_password}';
  ELSE
    ALTER ROLE gongmcp_reader WITH PASSWORD '${reader_password}';
  END IF;
END
\$\$;
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
REVOKE CREATE ON SCHEMA public FROM gongmcp_reader;
GRANT CONNECT ON DATABASE "$POSTGRES_DB" TO gongmcp_reader;
GRANT USAGE ON SCHEMA public TO gongmcp_reader;
SQL
