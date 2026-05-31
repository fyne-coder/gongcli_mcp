# Research And Code Review Prompt: JumpCloud vs Keycloak Remote MCP

Use this prompt with an independent review agent against branch
`codex/tc-jumpcloud-rca-docs` in the public repo
`https://github.com/fyne-coder/gongcli_mcp`.

## Goal

Determine why the lab Claude.ai custom connector works with Keycloak, and
TradeCentric reportedly has direct JumpCloud working, but the lab JumpCloud
direct-OIDC connector loops:

```text
Claude Connect -> JumpCloud -> Claude -> connector still not connected
```

The gateway logs show no `Authorization: Bearer` header on `/mcp`.

## Review Target

- Repo: `fyne-coder/gongcli_mcp`
- Branch: `codex/tc-jumpcloud-rca-docs`
- Review the branch code and docs exactly as pushed.
- Treat this as both a research task and a code/security review task.

Important paths:

- `internal/gateway/auth.go`
- `internal/gateway/config.go`
- `internal/gateway/server.go`
- `internal/gateway/server_test.go`
- `internal/cli/mcp_gateway_doctor.go`
- `internal/cli/mcp_gateway_doctor_test.go`
- `deploy/remote-mcp-auth/tradecentric-jumpcloud/`
- `deploy/remote-mcp-auth/cloudflare-worker/`
- `docs/remote-mcp-auth.md`
- `docs/remote-mcp-deployment-requirements.md`
- `docs/runbooks/remote-mcp-oauth-troubleshooting.md`
- `docs/runbooks/tc-jumpcloud-remote-mcp-rca-2026-05-29.md`

## Known Facts

### Local JumpCloud E2E Works

A local direct JumpCloud E2E succeeded:

- browser auth completed
- callback received an auth code
- token exchange returned HTTP 200
- using the JumpCloud access token against the gateway succeeded for
  `initialize`, `tools/list`, and `get_sync_status`

This proves the gateway can validate a JumpCloud bearer token when one is
actually supplied.

### Current Branch Supports JumpCloud/Ory Token Shapes

The branch is intended to support:

- issuer trailing slash tolerance
- `scp` scopes
- absent Cognito `token_use`
- empty or missing `aud` when top-level `client_id` is valid
- nested `ext.email`
- nested or dotted `ext.memberOf`

Check whether those allowances are correctly limited to the intended
`direct-oidc` profile and do not weaken Cognito behavior.

### Reported TradeCentric Working Shape

TradeCentric reportedly has Claude direct JumpCloud working without Cognito/DCR.

Reported shape:

- `GATEWAY_DCR_ENABLED=0`
- `OIDC_ISSUER_URL=https://oauth.id.jumpcloud.com/`
- static JumpCloud OAuth client
- JumpCloud client auth type: `Client Secret POST`
- canonical public MCP URL ending in `/mcp`
- protected-resource metadata:
  - `resource=https://gongmcp.internal.tradecentric.com/mcp`
  - `authorization_servers=["https://oauth.id.jumpcloud.com/"]`
  - `scopes_supported=["openid","email","profile","offline_access"]`
  - `bearer_methods_supported=["header"]`
- 401 challenge:
  - `WWW-Authenticate: Bearer resource_metadata=".../.well-known/oauth-protected-resource/mcp", scope="openid"`

Reported failure ladder:

1. missing bearer token
2. invalid client
3. required group missing
4. success after fixing nested JumpCloud/Ory claims

That means TC eventually got Claude to send a bearer token.

### Lab Failure Boundary

Repeated lab Claude connector attempts show:

- Claude starts the connect flow.
- Browser reaches JumpCloud.
- Browser returns to Claude.
- Connector remains disconnected or shows `mcp_client_invalid`.
- Gateway sees only unauthenticated MCP requests:
  - `reason=missing bearer token`
- Earlier JumpCloud Directory Insights showed `sso_token_success=true`.
- After duplicate JumpCloud app cleanup, Directory Insights showed
  `user_login_attempt`, not fresh `sso_auth`, suggesting the flow may have
  landed in the JumpCloud User Portal instead of completing RP-initiated OIDC.

Important suspicion: `https://docker.transcripts.fyne-llc.com/mcp` is not a
clean resource identity anymore. It has been used for Keycloak, stale Keycloak
bearer attempts against the JumpCloud gateway, multiple JumpCloud alternate
route experiments, and multiple failed Claude connector records.

## Research Questions

Use official documentation, public issue trackers, user forums, and this branch.
Prefer primary sources:

- Claude custom connector / remote MCP docs
- MCP authorization spec
- JumpCloud OIDC docs
- Anthropic `claude-ai-mcp` GitHub issues
- relevant Reddit/forum reports only as supporting evidence
- this branch's code, tests, docs, and examples

Answer:

1. What is the most plausible root cause category?
   - Claude cached/polluted resource state?
   - JumpCloud app config or RP-initiated-login misconfiguration?
   - Claude custom connector limitation/bug?
   - metadata/challenge mismatch?
   - redirect/host issue?
   - something else?
2. Why does Keycloak work in the lab but JumpCloud does not?
   - Compare auth server metadata shape.
   - Compare DCR vs static client.
   - Compare token endpoint auth method.
   - Compare redirect behavior.
   - Compare token claims.
   - Compare whether Claude actually receives/stores/uses a token.
3. Why might TC JumpCloud work while the lab JumpCloud flow does not?
   - Identify likely config differences.
   - Distinguish material differences from noise.
   - Include the possibility that TC used a clean host/resource identity and
     the lab did not.
4. Is the correct next test a truly fresh hostname with canonical `/mcp`?
   - If yes, specify exactly what DNS/tunnel/Caddy/gateway metadata must look
     like.
   - If no, specify a better next test and why.
5. What evidence should be collected in the next attempt?
   - gateway logs
   - JumpCloud Directory Insights rows
   - Claude `ofid_*`
   - browser HAR
   - exact protected-resource metadata JSON
   - exact `WWW-Authenticate`
   - token endpoint evidence if visible
6. If fresh-host direct JumpCloud still fails with no bearer, what is the
   recommended fallback?
   - Cloudflare Worker OAuth broker + Cloudflare Access backed by JumpCloud?
   - Cognito/DCR?
   - another auth proxy?

## Code Review Questions

1. Does direct-OIDC validation correctly accept observed JumpCloud/TC token
   shapes without weakening Cognito behavior?
2. Does issuer/client binding validation remain defensible?
3. Are `scp`, `aud`, `client_id`, `token_use`, nested `ext.email`, and
   nested/dotted groups handled only in the intended auth profile?
4. Are required-group and allowed-subject fallbacks documented and safe for lab
   vs production?
5. Does `gongctl doctor mcp-gateway` correctly diagnose static-client
   JumpCloud, alternate paths, canonical `/mcp`, protected-resource metadata,
   AS/OIDC metadata, JWKS, and 401 challenges?
6. Do deployment examples and docs match the code's actual config names and
   behavior?
7. Are there missing negative tests, especially for forged headers, Cognito
   regression, wrong issuer, wrong client, missing scope, wrong group, and
   nested-claim leakage across profiles?
8. Are there any security regressions, overly broad accepted token shapes, or
   docs that overstate production readiness?

## Required Output

Write a concise but rigorous report with:

1. Executive summary
2. Evidence table
3. Root cause ranking with confidence
4. Keycloak vs JumpCloud comparison
5. TC vs lab comparison
6. Recommended next experiment
7. Code review findings for branch `codex/tc-jumpcloud-rca-docs`, ordered by
   severity with file/line references
8. Support/escalation packet for Anthropic and JumpCloud
9. Clear stop condition for direct JumpCloud retries

Do not propose code changes unless the evidence specifically shows code is
still the blocker. The current working assumption is that gateway code is not
the primary issue because local bearer-token E2E passes, but the branch still
needs auth/security/test/doc review.
