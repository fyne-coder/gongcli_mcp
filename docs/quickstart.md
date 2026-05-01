# Quickstart

This guide gets one operator from a clean machine to a local Gong SQLite cache
and a read-only MCP server. It uses Docker because that is the easiest path for
people who do not have Go installed.

For production policy, security, HTTP, and release details, use the links in
[Advanced Configuration](#advanced-configuration).

## What You Need

- Docker Desktop or another Docker runtime.
- Gong API credentials with permission to read the data you plan to cache.
- A data directory outside this Git checkout, for example `~/gongctl-data`.
- An MCP host such as Claude Desktop, Cursor, or another client that can launch
  a stdio MCP server.

`gongctl` is not affiliated with Gong. Every company is responsible for its own
Gong access, consent, data-handling policy, and AI/subprocessor review.

## 1. Prepare Local Data And Credentials

Create a data directory outside the repo:

```bash
mkdir -p "$HOME/gongctl-data"
```

Create a local `.env` file in the repo checkout or another private operator
folder:

```bash
cp .env.example .env
```

Fill in:

```bash
GONG_ACCESS_KEY="..."
GONG_ACCESS_KEY_SECRET="..."
GONG_BASE_URL="https://api.gong.io"
```

`GONG_BASE_URL` is optional for most installs. Keep `.env`, SQLite databases,
transcript files, JSON exports, and governance config files out of Git.

## 2. Confirm The Image Runs

Use the published image for released versions:

```bash
docker run --rm ghcr.io/fyne-coder/gongcli_mcp/gongctl:v0.2.0 version
```

If you are testing local source changes instead of a release, build the local
images:

```bash
docker build -t gongctl:local .
docker build --target mcp -t gongctl:mcp-local .
```

In the remaining examples, replace the image names with either:

- `ghcr.io/fyne-coder/gongcli_mcp/gongctl:v0.2.0`
- `ghcr.io/fyne-coder/gongcli_mcp/gongmcp:v0.2.0`
- `gongctl:local`
- `gongctl:mcp-local`

## 3. Check Gong Authentication

```bash
docker run --rm \
  --env-file .env \
  -v "$HOME/gongctl-data:/data" \
  ghcr.io/fyne-coder/gongcli_mcp/gongctl:v0.2.0 \
  auth check
```

If this fails, fix credentials before syncing data.

## 4. Create A Small Local Cache

Start with a bounded sync so the first run is fast:

```bash
docker run --rm \
  --env-file .env \
  -v "$HOME/gongctl-data:/data" \
  ghcr.io/fyne-coder/gongcli_mcp/gongctl:v0.2.0 \
  sync calls --db /data/gong.db --from 2026-04-01 --to 2026-04-30 --preset minimal --max-pages 1
```

Check cache status:

```bash
docker run --rm \
  --env-file .env \
  -v "$HOME/gongctl-data:/data" \
  ghcr.io/fyne-coder/gongcli_mcp/gongctl:v0.2.0 \
  sync status --db /data/gong.db
```

Read the readiness section before deciding what to sync or configure next. A
typical early cache may look like:

```text
Ready: conversation volume, transcript coverage, scorecard themes, CRM segmentation
Partial: lifecycle separation, attribution readiness
Not configured: business profile
Recommended next action: run profile discover, review the generated YAML, then
profile validate and profile import to enable tenant-specific lifecycle
separation and attribution mapping.
```

You can continue without a business profile. Search, basic summaries, sync
status, CRM inspection, and many MCP tools still work. The tradeoff is that
sales-vs-post-sales lifecycle separation and attribution mapping remain generic
or partial until a reviewed tenant profile is imported.

If lifecycle separation or attribution readiness is partial, generate and review
a tenant business profile. For better RevOps results, sync CRM schema and
settings before discovery as shown in [Business Profiles](profiles.md#flow);
the quick path below is only a starter.

```bash
docker run --rm \
  --env-file .env \
  -v "$HOME/gongctl-data:/data" \
  ghcr.io/fyne-coder/gongcli_mcp/gongctl:v0.2.0 \
  profile discover --db /data/gong.db --out /data/gongctl-profile.yaml
docker run --rm \
  --env-file .env \
  -v "$HOME/gongctl-data:/data" \
  ghcr.io/fyne-coder/gongcli_mcp/gongctl:v0.2.0 \
  profile validate --db /data/gong.db --profile /data/gongctl-profile.yaml
docker run --rm \
  --env-file .env \
  -v "$HOME/gongctl-data:/data" \
  ghcr.io/fyne-coder/gongcli_mcp/gongctl:v0.2.0 \
  profile import --db /data/gong.db --profile /data/gongctl-profile.yaml
```

Review `gongctl-profile.yaml` before importing it. It can contain tenant CRM
field names and should stay outside Git. For the profile shape, compare against
the synthetic example in
[docs/examples/business-profile.example.yaml](examples/business-profile.example.yaml).

Add transcripts when the call cache looks right:

```bash
docker run --rm \
  --env-file .env \
  -v "$HOME/gongctl-data:/data" \
  ghcr.io/fyne-coder/gongcli_mcp/gongctl:v0.2.0 \
  sync transcripts --db /data/gong.db --out-dir /data/transcripts --limit 100 --batch-size 100
```

Run a quick local search:

```bash
docker run --rm \
  --env-file .env \
  -v "$HOME/gongctl-data:/data" \
  ghcr.io/fyne-coder/gongcli_mcp/gongctl:v0.2.0 \
  search transcripts --db /data/gong.db --query "pricing objection" --limit 5
```

## 5. If You Have AI Governance Exclusions

If your company has customers or accounts that must not be exposed to AI tools,
create a private config outside Git, for example
`$HOME/gongctl-data/private/ai-governance.yaml`:

```yaml
version: 1
lists:
  no_ai:
    description: Customers requiring approval before AI processing.
    customers:
      - name: "Example Restricted Corp"
        aliases:
          - "Example Restricted Corporation"
          - "example-restricted.com"
  notification_required:
    description: Customers excluded until notification requirements are met.
    customers:
      - name: "Example Notice Customer"
        aliases:
          - "Example Notice Co"
```

Audit the cache:

```bash
docker run --rm \
  -v "$HOME/gongctl-data:/data" \
  ghcr.io/fyne-coder/gongcli_mcp/gongctl:v0.2.0 \
  governance audit --db /data/gong.db --config /data/private/ai-governance.yaml
```

Create a filtered MCP-facing copy:

```bash
docker run --rm \
  -v "$HOME/gongctl-data:/data" \
  ghcr.io/fyne-coder/gongcli_mcp/gongctl:v0.2.0 \
  governance export-filtered-db \
    --db /data/gong.db \
    --config /data/private/ai-governance.yaml \
    --out /data/gong-mcp-governed.db \
    --overwrite
```

Use `/data/gong-mcp-governed.db` for MCP when a blocklist exists. Recreate it
after every sync or governance config change.

More detail: [AI governance exclusions](ai-governance.md).

## 6. Run The MCP Server Locally

For a single-user local MCP host, run the MCP-only image with no network and a
read-only data mount:

```bash
docker run --rm -i \
  --network none \
  -v "$HOME/gongctl-data:/data:ro" \
  ghcr.io/fyne-coder/gongcli_mcp/gongmcp:v0.2.0 \
  --db /data/gong.db
```

If you created a filtered database, use:

```bash
docker run --rm -i \
  --network none \
  -v "$HOME/gongctl-data:/data:ro" \
  ghcr.io/fyne-coder/gongcli_mcp/gongmcp:v0.2.0 \
  --db /data/gong-mcp-governed.db
```

This is stdio MCP. It is local to the MCP host process and does not require
Gong credentials because it only reads the SQLite cache.

## 7. Optional: Test HTTP MCP Locally

The default local path is stdio because it is simplest and can run with
`--network none`. Use HTTP when you are testing a private shared endpoint, a
proxy/TLS boundary, or a client that expects Streamable HTTP.

Start a localhost-only HTTP MCP endpoint:

```bash
printf '%s\n' 'replace-with-a-long-random-local-test-token' > "$HOME/gongctl-data/gongmcp-token"

docker run --rm \
  -p 127.0.0.1:8080:8080 \
  -e GONGMCP_BEARER_TOKEN_FILE=/run/secrets/gongmcp_token \
  -e GONGMCP_TOOL_ALLOWLIST=get_sync_status,search_calls,search_transcript_segments,rank_transcript_backlog \
  -e GONGMCP_ALLOWED_ORIGINS=http://127.0.0.1:8080,http://localhost:8080 \
  -v "$HOME/gongctl-data:/data:ro" \
  -v "$HOME/gongctl-data/gongmcp-token:/run/secrets/gongmcp_token:ro" \
  ghcr.io/fyne-coder/gongcli_mcp/gongmcp:v0.2.0 \
  --http 0.0.0.0:8080 \
  --auth-mode bearer \
  --allow-open-network \
  --db /data/gong.db
```

The container binds internally to `0.0.0.0`, but Docker publishes it only on
`127.0.0.1`. Non-local/private-pilot HTTP should use bearer auth, an explicit
tool allowlist, an explicit Origin allowlist, and TLS termination at a trusted
company proxy or gateway.

The allowlist above is the `operator-smoke` profile from
[Customer implementation checklist](implementation-checklist.md#named-tool-profiles).
It is for install validation only. Use `strict-business-pilot` before giving
business users access.

Some desktop MCP clients still launch local stdio processes from their config
file. For those clients, use a bridge such as `mcp-remote`:

```json
{
  "mcpServers": {
    "gong-http": {
      "command": "npx",
      "args": [
        "-y",
        "mcp-remote@<reviewed-version>",
        "http://127.0.0.1:8080/mcp",
        "--allow-http",
        "--transport",
        "http-only",
        "--silent"
      ]
    }
  }
}
```

Replace `<reviewed-version>` with a pinned package version approved by the
customer's normal dependency review process. Do not use `@latest` in a
customer-hosted deployment path.

Do not use this localhost URL in Claude's add-by-URL UI. That UI is for remote
MCP servers and requires an `https://` URL. For local testing in Claude Desktop,
use the stdio Docker config in the next section or a local bridge configured in
Claude's desktop JSON file. For users who expect to paste a URL into Claude's
UI, deploy `gongmcp` behind a company-managed HTTPS proxy and give them the
public `/mcp` endpoint, for example:

```text
https://gong-mcp.example.com/mcp
```

`gongmcp` itself speaks HTTP. TLS certificates, DNS, network access, and user
authentication are owned by the company proxy/gateway in front of the container.

The same boundary applies to ChatGPT Apps/connectors: ChatGPT expects a remote
HTTPS `/mcp` URL. The local stdio setup below is useful for desktop MCP hosts
that can launch a local process, but it is not a ChatGPT connector install path.

## 8. Add Stdio MCP To An MCP Host

For Claude Desktop on macOS, the helper script can generate the config entry
for you:

```bash
scripts/install-claude-stdio-mcp.sh --db "$HOME/gongctl-data/gong-mcp-governed.db"
```

Review the printed JSON, then install it with a config backup:

```bash
scripts/install-claude-stdio-mcp.sh --db "$HOME/gongctl-data/gong-mcp-governed.db" --install
```

Pass `--db "$HOME/gongctl-data/gong.db"` instead if you did not create a
governed filtered database. Add `--image gongctl:mcp-local` when testing a
local image you built yourself. The generated entry launches Docker in stdio
mode, mounts the data directory read-only, uses `--network none`, and does not
include Gong credentials.

Example host config:

```json
{
  "mcpServers": {
    "gong": {
      "command": "docker",
      "args": [
        "run",
        "--rm",
        "-i",
        "--network",
        "none",
        "-v",
        "/Users/YOU/gongctl-data:/data:ro",
        "ghcr.io/fyne-coder/gongcli_mcp/gongmcp:v0.2.0",
        "--db",
        "/data/gong.db"
      ]
    }
  }
}
```

Replace `/Users/YOU/gongctl-data` with the absolute path on that machine. If
you use a governed database, change the final value to
`/data/gong-mcp-governed.db`.

Restart the MCP host after changing the config.

## 9. Smoke-Test From Your MCP Host

Use a prompt that asks for bounded evidence instead of broad summarization:

```text
Use the Gong MCP server. First check sync status. Then search transcript
segments for "pricing objection" with a limit of 5. Return only short snippets
and explain whether the cache looks ready for broader analysis.
```

For governed deployments, also test that configured restricted names do not
appear in MCP answers. Do not paste real restricted-customer lists into public
issues, public docs, or shared LLM prompts.

Generate a sanitized local support bundle if you need to share deployment
evidence with support:

```bash
mkdir -p "$HOME/gongctl-data/support-bundle"

docker run --rm \
  -v "$HOME/gongctl-data:/data:ro" \
  -v "$HOME/gongctl-data/support-bundle:/support" \
  ghcr.io/fyne-coder/gongcli_mcp/gongctl:v0.2.0 \
  support bundle --db /data/gong-mcp-governed.db --out /support
```

This command opens the SQLite cache read-only, does not need Gong credentials,
and does not make network calls. Add `--include-env` only when support needs
presence booleans for known environment variables.

## Advanced Configuration

- Docker, Compose, GHCR, and MCP host config:
  [Docker deployment](docker.md)
- AI customer exclusion and filtered MCP databases:
  [AI governance exclusions](ai-governance.md)
- What MCP can expose and how to widen the tool surface:
  [MCP data exposure](mcp-data-exposure.md)
- Company-managed deployment shape and multi-user/private HTTP boundary:
  [Enterprise deployment](enterprise-deployment.md)
- Customer-hosted package index, data-flow diagram, and review checklist:
  [Customer-hosted package](customer-hosted-package.md)
- Remote HTTPS/OAuth, ChatGPT connector, and SSO broker setup:
  [Remote MCP auth](remote-mcp-auth.md)
- Operator refresh jobs and repeatable sync plans:
  [Operator sync runbook](runbooks/operator-sync.md)
- Data handling and AI provider/subprocessor review:
  [Data handling](data-handling.md)
- Security assumptions and deployment controls:
  [Security model](security-model.md)
- Example customer security-review answers:
  [Security questionnaire](security-questionnaire.md)
- Customer-hosted data boundary and support intake:
  [Data Boundary Statement](data-boundary-statement.md) and
  [Support](support.md)
- Terraform starter examples:
  [`deploy/terraform`](../deploy/terraform/README.md)
- CRM/profile configuration:
  [Profiles](profiles.md)
- Release, tags, and GHCR publishing:
  [Release versioning](release.md)
