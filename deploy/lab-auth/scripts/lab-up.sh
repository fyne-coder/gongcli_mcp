#!/usr/bin/env bash
# shellcheck disable=SC2029
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
umask 077
LAB_VM="${LAB_VM:-}"
LAB_PUBLIC_BASE_URL="${LAB_PUBLIC_BASE_URL:-}"
LAB_DB="${LAB_DB:-}"
LAB_TOOL_PRESET="${LAB_TOOL_PRESET:-${GONGMCP_TOOL_PRESET:-business-pilot}}"
LAB_APPROVED_EMAIL="${LAB_APPROVED_EMAIL:-approved-user@example.test}"
LAB_SECONDARY_EMAIL="${LAB_SECONDARY_EMAIL:-secondary-user@example.test}"
LAB_BLOCKED_EMAIL="${LAB_BLOCKED_EMAIL:-blocked-user@example.test}"
REMOTE_ROOT="${REMOTE_ROOT:-/srv/gongctl}"
REMOTE_SOURCE="$REMOTE_ROOT/source"
REMOTE_LAB="$REMOTE_SOURCE/deploy/lab-auth"

require() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "missing required command: $1" >&2
    exit 1
  }
}

require_env() {
  local name="$1"
  if [[ -z "${!name:-}" ]]; then
    echo "$name is required, see deploy/lab-auth/.env.example" >&2
    exit 1
  fi
}

rand_b64() {
  openssl rand -base64 "${1:-32}" | tr -d '\n'
}

rand_hex() {
  openssl rand -hex "${1:-16}" | tr -d '\n'
}

sed_escape() {
  printf '%s' "$1" | sed 's/[\/&]/\\&/g'
}

read_remote_env() {
  local file="$1"
  local key="$2"
  awk -F= -v key="$key" '$1 == key {print substr($0, length(key) + 2)}' "$file" 2>/dev/null || true
}

set_env_value() {
  local file="$1"
  local key="$2"
  local value="$3"
  if grep -q "^${key}=" "$file" 2>/dev/null; then
    awk -F= -v key="$key" -v value="$value" 'BEGIN {OFS = FS} $1 == key {$2 = value} {print}' "$file" >"$file.next"
    mv "$file.next" "$file"
  else
    printf '%s=%s\n' "$key" "$value" >>"$file"
  fi
}

require ssh
require tar
require scp
require openssl
require_env LAB_VM
require_env LAB_PUBLIC_BASE_URL
require_env LAB_DB

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
case "$REMOTE_ROOT" in
  *\'*|*\"*|*[\;\`\$]*)
    echo "REMOTE_ROOT contains unsupported shell metacharacters" >&2
    exit 2
    ;;
esac

if [[ ! -f "$LAB_DB" ]]; then
  echo "lab DB not found: $LAB_DB" >&2
  exit 1
fi

tmpdir="$(mktemp -d "${TMPDIR:-/tmp}/gongctl-lab-auth.XXXXXX")"
trap 'rm -rf "$tmpdir"' EXIT

ssh "$LAB_VM" "set -e; apt-get update >/dev/null; DEBIAN_FRONTEND=noninteractive apt-get install -y rsync curl jq >/dev/null; mkdir -p '$REMOTE_ROOT'/runtime '$REMOTE_ROOT'/secrets '$REMOTE_ROOT'/logs '$REMOTE_ROOT'/compose"
ssh "$LAB_VM" "ufw allow from 172.16.0.0/12 to any port 80 proto tcp >/dev/null || true"

COPYFILE_DISABLE=1 tar --no-xattrs -C "$ROOT" \
  --exclude .git \
  --exclude .env \
  --exclude .DS_Store \
  --exclude bin \
  --exclude dist \
  --exclude tmp \
  --exclude data \
  -czf - . \
| ssh "$LAB_VM" "rm -rf '$REMOTE_SOURCE' && mkdir -p '$REMOTE_SOURCE' && tar -xzf - -C '$REMOTE_SOURCE'"

scp "$LAB_DB" "$LAB_VM:$REMOTE_ROOT/runtime/gong-mcp-governed.db" >/dev/null

if ssh "$LAB_VM" "test -f '$REMOTE_LAB/.env'"; then
  scp "$LAB_VM:$REMOTE_LAB/.env" "$tmpdir/.env" >/dev/null
else
  cat >"$tmpdir/.env" <<EOF
LAB_PUBLIC_BASE_URL=$LAB_PUBLIC_BASE_URL
REMOTE_ROOT=$REMOTE_ROOT
KEYCLOAK_ADMIN_PASSWORD=$(rand_b64 24)
OIDC_CLIENT_SECRET=$(rand_b64 24)
OAUTH2_PROXY_COOKIE_SECRET=$(rand_hex 16)
LAB_APPROVED_EMAIL=$LAB_APPROVED_EMAIL
LAB_SECONDARY_EMAIL=$LAB_SECONDARY_EMAIL
LAB_BLOCKED_EMAIL=$LAB_BLOCKED_EMAIL
LAB_ALLOWED_EMAILS=$LAB_APPROVED_EMAIL,$LAB_SECONDARY_EMAIL
LAB_APPROVED_PASSWORD=$(rand_b64 18)
LAB_BLOCKED_PASSWORD=$(rand_b64 18)
LAB_SECONDARY_PASSWORD=$(rand_b64 18)
EOF
fi
set_env_value "$tmpdir/.env" GONGMCP_TOOL_PRESET "$LAB_TOOL_PRESET"
set_env_value "$tmpdir/.env" LAB_PUBLIC_BASE_URL "$LAB_PUBLIC_BASE_URL"
set_env_value "$tmpdir/.env" REMOTE_ROOT "$REMOTE_ROOT"
set_env_value "$tmpdir/.env" LAB_APPROVED_EMAIL "$LAB_APPROVED_EMAIL"
set_env_value "$tmpdir/.env" LAB_SECONDARY_EMAIL "$LAB_SECONDARY_EMAIL"
set_env_value "$tmpdir/.env" LAB_BLOCKED_EMAIL "$LAB_BLOCKED_EMAIL"
set_env_value "$tmpdir/.env" LAB_ALLOWED_EMAILS "$LAB_APPROVED_EMAIL,$LAB_SECONDARY_EMAIL"

cookie_secret="$(read_remote_env "$tmpdir/.env" OAUTH2_PROXY_COOKIE_SECRET)"
case "${#cookie_secret}" in
  16|24|32) ;;
  *)
    replacement_cookie_secret="$(rand_hex 16)"
    awk -F= -v replacement="$replacement_cookie_secret" 'BEGIN {OFS = FS} $1 == "OAUTH2_PROXY_COOKIE_SECRET" {$2 = replacement} {print}' "$tmpdir/.env" >"$tmpdir/.env.next"
    mv "$tmpdir/.env.next" "$tmpdir/.env"
    ;;
esac

oidc_secret="$(read_remote_env "$tmpdir/.env" OIDC_CLIENT_SECRET)"
approved_password="$(read_remote_env "$tmpdir/.env" LAB_APPROVED_PASSWORD)"
blocked_password="$(read_remote_env "$tmpdir/.env" LAB_BLOCKED_PASSWORD)"
secondary_password="$(read_remote_env "$tmpdir/.env" LAB_SECONDARY_PASSWORD)"
if [[ -z "$approved_password" ]]; then
  approved_password="$(rand_b64 18)"
fi
if [[ -z "$blocked_password" ]]; then
  blocked_password="$(rand_b64 18)"
fi
if [[ -z "$secondary_password" ]]; then
  secondary_password="$(rand_b64 18)"
fi
set_env_value "$tmpdir/.env" LAB_APPROVED_PASSWORD "$approved_password"
set_env_value "$tmpdir/.env" LAB_BLOCKED_PASSWORD "$blocked_password"
set_env_value "$tmpdir/.env" LAB_SECONDARY_PASSWORD "$secondary_password"
if [[ -z "$oidc_secret" || -z "$approved_password" || -z "$blocked_password" || -z "$secondary_password" ]]; then
  echo "lab env is missing required secrets" >&2
  exit 1
fi

mkdir -p "$tmpdir/import"
sed \
  -e "s/__LAB_PUBLIC_BASE_URL__/$(sed_escape "$LAB_PUBLIC_BASE_URL")/g" \
  -e "s/__LAB_APPROVED_EMAIL__/$(sed_escape "$LAB_APPROVED_EMAIL")/g" \
  -e "s/__LAB_SECONDARY_EMAIL__/$(sed_escape "$LAB_SECONDARY_EMAIL")/g" \
  -e "s/__LAB_BLOCKED_EMAIL__/$(sed_escape "$LAB_BLOCKED_EMAIL")/g" \
  -e "s/__OIDC_CLIENT_SECRET__/$(sed_escape "$oidc_secret")/g" \
  -e "s/__LAB_APPROVED_PASSWORD__/$(sed_escape "$approved_password")/g" \
  -e "s/__LAB_BLOCKED_PASSWORD__/$(sed_escape "$blocked_password")/g" \
  -e "s/__LAB_SECONDARY_PASSWORD__/$(sed_escape "$secondary_password")/g" \
  "$ROOT/deploy/lab-auth/keycloak/realm.template.json" >"$tmpdir/import/realm.json"

ssh "$LAB_VM" "mkdir -p '$REMOTE_LAB/keycloak/import'"
scp "$tmpdir/.env" "$LAB_VM:$REMOTE_LAB/.env" >/dev/null
scp "$tmpdir/import/realm.json" "$LAB_VM:$REMOTE_LAB/keycloak/import/realm.json" >/dev/null
ssh "$LAB_VM" "chmod 600 '$REMOTE_LAB/.env'"

ssh "$LAB_VM" "
set -euo pipefail
if [ ! -f '$REMOTE_ROOT/secrets/gongmcp_token' ]; then
  openssl rand -hex 32 > '$REMOTE_ROOT/secrets/gongmcp_token'
fi
chown 65532:65532 '$REMOTE_ROOT/secrets/gongmcp_token'
chmod 400 '$REMOTE_ROOT/secrets/gongmcp_token'
DOCKER_BUILDKIT=1 docker build --target mcp -t gongctl:mcp-local '$REMOTE_SOURCE'
cd '$REMOTE_LAB'
docker-compose down -v >/dev/null
docker-compose up -d --build
for _ in \$(seq 1 60); do
  if docker-compose exec -T keycloak /opt/keycloak/bin/kcadm.sh config credentials \
    --server http://127.0.0.1:8080 \
    --realm master \
    --user admin \
    --password \"\$(awk -F= '\$1 == \"KEYCLOAK_ADMIN_PASSWORD\" {print substr(\$0, length(\$1) + 2)}' .env)\" >/dev/null 2>&1; then
    break
  fi
  sleep 2
done
for name in 'Trusted Hosts' 'Allowed Client Scopes' 'Allowed Protocol Mapper Types' 'Consent Required' 'Full Scope Disabled'; do
  ids=\$(docker-compose exec -T keycloak /opt/keycloak/bin/kcadm.sh get components \
    -r gong-lab \
    -q name=\"\$name\" \
    --fields id,subType \
    --format csv \
    --noquotes \
    | awk -F, '\$2 == \"anonymous\" {print \$1}' \
    | tr -d '\\r')
  for id in \$ids; do
    docker-compose exec -T keycloak /opt/keycloak/bin/kcadm.sh delete components/\$id -r gong-lab >/dev/null
  done
done
audience_scope_id=\$(docker-compose exec -T keycloak /opt/keycloak/bin/kcadm.sh get client-scopes \
  -r gong-lab \
  --fields id,name \
  --format csv \
  --noquotes \
  | awk -F, '\$2 == \"gong-lab-mcp-audience\" {print \$1}' \
  | tr -d '\\r')
if [ -z \"\$audience_scope_id\" ]; then
  audience_scope_id=\$(docker-compose exec -T keycloak /opt/keycloak/bin/kcadm.sh create client-scopes \
    -r gong-lab \
    -s name=gong-lab-mcp-audience \
    -s protocol=openid-connect \
    -s 'attributes.\"include.in.token.scope\"=false' \
    -i \
    | tr -d '\\r')
  docker-compose exec -T keycloak /opt/keycloak/bin/kcadm.sh create client-scopes/\$audience_scope_id/protocol-mappers/models \
    -r gong-lab \
    -s name=audience-gong-lab-proxy \
    -s protocol=openid-connect \
    -s protocolMapper=oidc-audience-mapper \
    -s 'config.\"included.client.audience\"=gong-lab-proxy' \
    -s 'config.\"access.token.claim\"=true' \
    -s 'config.\"id.token.claim\"=false' \
    -s 'config.\"introspection.token.claim\"=true' >/dev/null
fi
docker-compose exec -T keycloak /opt/keycloak/bin/kcadm.sh update realms/gong-lab/default-default-client-scopes/\$audience_scope_id \
  -r gong-lab \
  -n >/dev/null || true
basic_scope_id=\$(docker-compose exec -T keycloak /opt/keycloak/bin/kcadm.sh get client-scopes \
  -r gong-lab \
  --fields id,name \
  --format csv \
  --noquotes \
  | awk -F, '\$2 == \"basic\" {print \$1}' \
  | tr -d '\\r')
if [ -n \"\$basic_scope_id\" ]; then
  if ! docker-compose exec -T keycloak /opt/keycloak/bin/kcadm.sh get client-scopes/\$basic_scope_id/protocol-mappers/models \
    -r gong-lab \
    --fields name \
    --format csv \
    --noquotes \
    | tr -d '\\r' \
    | grep -qx 'audience-gong-lab-proxy'; then
    docker-compose exec -T keycloak /opt/keycloak/bin/kcadm.sh create client-scopes/\$basic_scope_id/protocol-mappers/models \
      -r gong-lab \
      -s name=audience-gong-lab-proxy \
      -s protocol=openid-connect \
      -s protocolMapper=oidc-audience-mapper \
      -s 'config.\"included.client.audience\"=gong-lab-proxy' \
      -s 'config.\"access.token.claim\"=true' \
      -s 'config.\"id.token.claim\"=false' \
      -s 'config.\"introspection.token.claim\"=true' >/dev/null
  fi
  if ! docker-compose exec -T keycloak /opt/keycloak/bin/kcadm.sh get client-scopes/\$basic_scope_id/protocol-mappers/models \
    -r gong-lab \
    --fields name \
    --format csv \
    --noquotes \
    | tr -d '\\r' \
    | grep -qx 'groups'; then
    docker-compose exec -T keycloak /opt/keycloak/bin/kcadm.sh create client-scopes/\$basic_scope_id/protocol-mappers/models \
      -r gong-lab \
      -s name=groups \
      -s protocol=openid-connect \
      -s protocolMapper=oidc-group-membership-mapper \
      -s 'config.\"full.path\"=true' \
      -s 'config.\"id.token.claim\"=true' \
      -s 'config.\"access.token.claim\"=true' \
      -s 'config.\"claim.name\"=groups' \
      -s 'config.\"userinfo.token.claim\"=true' >/dev/null
  fi
fi
docker-compose exec -T keycloak /opt/keycloak/bin/kcadm.sh get clients \
  -r gong-lab \
  --fields id,clientId,defaultClientScopes \
  --format json \
  | jq -r '.[] | select(.clientId != \"realm-management\" and .clientId != \"security-admin-console\" and .clientId != \"admin-cli\" and .clientId != \"account\" and .clientId != \"account-console\" and .clientId != \"broker\") | [.id, ((.defaultClientScopes // []) | join(\";\"))] | @tsv' \
  | while IFS=\$(printf '\\t') read -r client_id scopes; do
      case \";\$scopes;\" in
        *';gong-lab-mcp-audience;'*) ;;
        *) docker-compose exec -T keycloak /opt/keycloak/bin/kcadm.sh update clients/\$client_id/default-client-scopes/\$audience_scope_id -r gong-lab -n >/dev/null || true ;;
      esac
    done
docker-compose restart caddy >/dev/null
"

echo "lab deployed to $LAB_VM ($LAB_PUBLIC_BASE_URL)"
echo "run: deploy/lab-auth/scripts/lab-smoke.sh"
