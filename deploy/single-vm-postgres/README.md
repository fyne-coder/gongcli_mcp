# Single-VM Postgres Starter

This is a simple company-managed VM shape for teams that want everything on one
Linux host before moving to managed container infrastructure:

- one local Postgres container
- one source database for `gongctl` sync jobs
- one MCP serving database rebuilt through governance redaction
- one scoped reader role for `gongmcp`
- one HTTP `gongmcp` endpoint bound to loopback by default

It is still a starter, not a production security architecture. Put a
customer-managed HTTPS/OAuth/SSO gateway, reverse proxy, WAF, VPN, or tunnel in
front of `127.0.0.1:8080` before giving users an MCP URL. Do not expose
Postgres publicly.

## Files

- `docker-compose.yml`: Postgres, operator job profiles, grant reconciliation,
  and read-only `gongmcp`.
- `init/01-bootstrap.sh`: creates the source DB, serving DB, and scoped reader
  role on first Postgres initialization.
- `single-vm.env.example`: required environment shape.

## First-Time Setup

On the VM:

```bash
sudo mkdir -p /srv/gongctl/{config,secrets,transcripts}
sudo cp deploy/single-vm-postgres/single-vm.env.example /srv/gongctl/single-vm.env
sudo install -m 600 /dev/null /srv/gongctl/secrets/gongmcp_token
# Only when customer exclusions exist:
sudo install -m 600 /dev/null /srv/gongctl/secrets/ai-governance.yaml
```

Edit `/srv/gongctl/single-vm.env` and the secret files with customer-approved
values. Keep the env file and secret files out of Git and out of shared support
bundles. Replace the `vX.Y.Z` image placeholders with a pinned release tag or
digest before running `docker compose pull` or starting services.

When the customer has no customer exclusions, omit
`/srv/gongctl/secrets/ai-governance.yaml`, run `gongctl deploy postgres-refresh
--no-governance-exclusions`, and set `GONGMCP_NO_GOVERNANCE_EXCLUSIONS=1` for
the MCP runtime. In `/srv/gongctl/single-vm.env`, leave `AI_GOVERNANCE_CONFIG`
unset and set both `GONGCTL_NO_GOVERNANCE_EXCLUSIONS=1` and
`GONGMCP_NO_GOVERNANCE_EXCLUSIONS=1`.

Render the Compose config before starting containers:

```bash
docker compose \
  --env-file /srv/gongctl/single-vm.env \
  -f deploy/single-vm-postgres/docker-compose.yml \
  config --quiet
```

Start Postgres:

```bash
docker compose \
  --env-file /srv/gongctl/single-vm.env \
  -f deploy/single-vm-postgres/docker-compose.yml \
  up -d postgres
```

## Operator Flow

Run sync commands against the source DB. Override the default command with the
approved sync scope for the customer:

```bash
docker compose \
  --env-file /srv/gongctl/single-vm.env \
  -f deploy/single-vm-postgres/docker-compose.yml \
  --profile operator \
  run --rm gongctl \
  sync calls --from YYYY-MM-DD --to YYYY-MM-DD --preset business
```

Then run the remaining approved sync steps, for example transcripts,
CRM/schema/settings, profile import, and read-model rebuild as described in the
Postgres deployment runbook.

Refresh the governed serving DB:

```bash
docker compose \
  --env-file /srv/gongctl/single-vm.env \
  -f deploy/single-vm-postgres/docker-compose.yml \
  --profile refresh \
  run --rm refresh-serving-db
```

That profile runs the consolidated operator command with the required source
URL, target URL, governance config, preset, role, and database inputs already
wired into the service environment.

Apply scoped reader grants on the serving DB separately only when you did not
use `deploy postgres-refresh` or when repairing grants:

```bash
docker compose \
  --env-file /srv/gongctl/single-vm.env \
  -f deploy/single-vm-postgres/docker-compose.yml \
  --profile grants \
  run --rm apply-reader-grants
```

Start the read-only MCP runtime:

```bash
docker compose \
  --env-file /srv/gongctl/single-vm.env \
  -f deploy/single-vm-postgres/docker-compose.yml \
  up -d gongmcp
```

`gongmcp` receives only the scoped reader URL and bearer token. It does not
receive Gong API credentials or the source DB URL. It receives the private AI
governance YAML read-only when customer exclusions exist; when no exclusions
exist, use `GONGMCP_NO_GOVERNANCE_EXCLUSIONS=1` and `gongctl deploy
postgres-refresh --no-governance-exclusions` instead.

Postgres runs `init/01-bootstrap.sh` only when the named Docker volume is first
created. If you change database names or the scoped reader role after first
boot, apply the equivalent DBA changes manually or recreate the volume only
after taking approved backups.

## Checks Before User Access

Before connecting business users:

- run `gongctl doctor postgres-deploy --preset business-workbench`
- run `gongctl sync status --preset business-workbench` against the scoped
  reader URL
- run the GA acceptance smoke from the Postgres deployment runbook
- verify the HTTPS/OAuth/SSO gateway denies unauthenticated users
- verify forged identity headers are stripped or ignored by the gateway
- keep `POSTGRES_BIND` on `127.0.0.1`
- keep `GONGMCP_BIND` on `127.0.0.1` unless the host firewall and gateway
  boundary are approved
- back up both `gongctl_source` and `gongctl_mcp`
- test restore into an isolated database

For larger or stricter deployments, move from this VM starter to customer
managed Postgres plus the AWS ECS Postgres runtime starter or equivalent
platform-owned infrastructure.
