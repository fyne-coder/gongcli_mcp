# TC/Lab JumpCloud Direct OIDC Review

Date: 2026-05-30 ET

Branch reviewed: `codex/tc-jumpcloud-rca-docs`

Commit reviewed: `402314b4d88374572aff653fd6f2a7af8932f675`

2026-05-31 update: this review captured the pre-success state and recommended a
fresh direct-JumpCloud experiment. That experiment succeeded after changing the
JumpCloud app to `Client Secret POST`, deleting the stale Claude connector, and
creating a fresh connector against the canonical `/mcp` URL. Use
[JumpCloud Claude Direct MCP Success RCA](jumpcloud-claude-direct-success-rca-2026-05-31.md)
as the current operator checklist.

## Executive Summary

The most likely current lab blocker is before gateway JWT validation: Claude is
not storing or sending a JumpCloud bearer token for the lab MCP resource. The
gateway branch can validate JumpCloud/Ory-shaped access tokens when a bearer is
actually supplied, and the direct-OIDC compatibility changes are mostly scoped
to `OIDC_AUTH_PROFILE=direct-oidc`.

Keycloak worked in the lab because it provided a clean, MCP-shaped auth surface
with working registration/metadata for the same host at the time. TradeCentric
can work with direct JumpCloud because Claude eventually used a static
JumpCloud client and sent a bearer token to canonical `/mcp`. The lab has a
different failure boundary: repeated connector attempts on a reused resource
identity, prior stale Keycloak bearer attempts, alternate path experiments, and
`mcp_client_invalid` attempts that leave `/mcp` with no bearer.

The next useful direct-JumpCloud test is a truly fresh hostname with canonical
`/mcp`, not another variant on `docker.transcripts.fyne-llc.com`. If that fresh
host still produces JumpCloud `sso_token_success=true` but gateway logs only
`missing bearer token`, stop direct lab retries and use a broker fallback while
escalating the `ofid_*` packet to Anthropic and the token-exchange evidence to
JumpCloud.

Independent-agent note: the tmux Claude harness run
`/Users/arthurlee/src/agent_tmux/runs/stress/20260530T202738-0400-62548-jumpcloud-direct-review`
stalled before producing an artifact. Two `claude -p` retries also produced an
empty artifact and were stopped. This report is based on direct repo review,
local verification, current JumpCloud discovery, official Claude/MCP/JumpCloud
docs, public Anthropic issue evidence, and prior repo-local evidence.

## Evidence Table

| Evidence | Signal | Interpretation |
| --- | --- | --- |
| Local JumpCloud E2E in the RCA runbook | browser auth, code callback, token exchange, and gateway `initialize` / `tools/list` / `get_sync_status` succeeded with a JumpCloud bearer | gateway validation is not the primary blocker when a bearer is supplied |
| TC reported ladder | `missing bearer token` -> `invalid client` -> `required group missing` -> safe tool success | TC crossed the key boundary from no bearer to bearer-present, then fixed claim/policy shape |
| Lab r6 replay in `docs/runbooks/tc-jumpcloud-remote-mcp-rca-2026-05-29.md` | `mcp_client_invalid`, `ofid_01270f5668fb0fa5`, gateway `missing bearer token`, JumpCloud `sso_token_success=true` | OAuth/login reached JumpCloud, but Claude did not send a bearer to `/mcp` |
| Current JumpCloud discovery | OIDC discovery exists; auth-code, refresh-token, PKCE S256, `client_secret_post`, `client_secret_basic`, and `none` are advertised; no `registration_endpoint` or CIMD flag | direct Claude requires static/custom credentials or a broker; JumpCloud itself is not advertising DCR/CIMD |
| Claude connector docs | custom connectors can use supplied client credentials; protected-resource `resource` must exactly match the MCP URL; hosted callback is `https://claude.ai/api/mcp/auth_callback` | exact resource identity and static-client settings are material |
| MCP authorization spec | clients must keep credentials/tokens bound to the authorization server issuer and must not reuse credentials across issuers | stale Keycloak state on a reused resource is a plausible client-side/resource-state failure |
| Gateway code/tests | direct-OIDC accepts `scp`, absent `token_use`, nested `ext`, and client binding via `client_id`/`aud`; Cognito tests reject missing `token_use`, `scp`-only scope, and nested direct-OIDC fallback leakage | direct-OIDC allowances are profile-gated enough for this branch |
| Go gateway proxy tests | forged identity headers are stripped and replaced before forwarding | Go gateway handles the trusted-header risk called out in repo memory |

## Root Cause Ranking

1. Claude cached or polluted resource state on the lab hostname: high
   confidence. The reused `https://docker.transcripts.fyne-llc.com/mcp` resource
   saw Keycloak, JumpCloud, stale bearer, and alternate-path experiments. The MCP
   spec requires clients to bind credentials to the authorization server issuer,
   and stale Keycloak bearer attempts were observed in the RCA.
2. JumpCloud app or Claude static-client mismatch: medium-high confidence.
   `mcp_client_invalid` plus no bearer at `/mcp` is consistent with token
   exchange failure, client authentication method mismatch, wrong active app,
   wrong redirect URI, stale secret, or scopes not matching what Claude requests.
3. Metadata/challenge mismatch: medium confidence historically, lower
   confidence for the latest doctor-passing replay. Claude and MCP docs make
   exact protected-resource `resource`, first `authorization_servers` entry, and
   401 `resource_metadata` challenge material.
4. Claude custom connector limitation or bug: medium-low confidence. Public
   `anthropics/claude-ai-mcp` issues show similar `ofid_*` and no-bearer
   symptoms, but TC reportedly working with direct JumpCloud prevents treating
   this as the default conclusion.
5. Gateway code blocker: low confidence. The local JumpCloud E2E and tests prove
   the gateway can validate JumpCloud-shaped access tokens when present.

## Keycloak Vs JumpCloud

| Dimension | Keycloak lab | JumpCloud lab |
| --- | --- | --- |
| Auth server metadata | lab-controlled and MCP-shaped | provider OIDC discovery exists, but no DCR/CIMD metadata |
| Registration/client | DCR or lab-managed client path was under our control | static JumpCloud client must match Claude Advanced settings |
| Token endpoint auth | lab-compatible public/DCR path | JumpCloud supports `client_secret_post`, `client_secret_basic`, and `none`; the active app must match what Claude sends |
| Redirect | lab setup was built around Claude callback | JumpCloud app must exactly allow `https://claude.ai/api/mcp/auth_callback`; landing in the User Portal is not proof of RP-initiated OIDC completion |
| Claims | Keycloak carried lab audience/group shapes | JumpCloud/Ory may use `scp`, omit `token_use`, bind client through `client_id` or `aud`, and nest email/groups under `ext` |
| Bearer outcome | Claude sent usable bearer in the lab proof | latest lab JumpCloud attempts show no bearer reaching `/mcp` |

## TC Vs Lab

Material likely differences:

- TC used a clean canonical MCP URL ending in `/mcp`.
- TC used direct JumpCloud with `GATEWAY_DCR_ENABLED=0`.
- TC used a static JumpCloud OAuth client and reportedly `Client Secret POST`.
- TC protected-resource metadata advertised JumpCloud as the authorization
  server and used standard scopes such as `openid`, `email`, `profile`, and
  `offline_access`.
- TC crossed into the `required group missing` stage, proving bearer-present
  gateway validation occurred.

Likely noise:

- The specific public hostname is not material except for freshness and exact
  resource identity.
- `AWS-Admin` as a group name is not material; it is not suitable as a final
  production gate.
- Cognito is not material to the direct path; it is a fallback when Claude cannot
  use the direct/static-client provider surface.

## Recommended Next Experiment

Run exactly one fresh-host direct JumpCloud experiment.

Required shape:

- DNS/tunnel: create a new hostname such as
  `jumpcloud-clean.transcripts.fyne-llc.com` pointing to the same lab edge or
  tunnel, with no prior Claude connector records.
- Public MCP URL entered in Claude:
  `https://jumpcloud-clean.transcripts.fyne-llc.com/mcp`.
- Caddy/edge: route only that host's `/mcp` and
  `/.well-known/oauth-protected-resource/mcp` to the JumpCloud MCP gateway path.
  Do not route it through Keycloak, oauth2-proxy browser sessions, or an
  alternate `/mcp-jc-*` path.
- Protected-resource metadata:
  `resource=https://jumpcloud-clean.transcripts.fyne-llc.com/mcp`,
  `authorization_servers=["https://oauth.id.jumpcloud.com/"]`,
  `scopes_supported=["openid","email","profile","offline_access"]`, and
  `bearer_methods_supported=["header"]`.
- 401 challenge:
  `WWW-Authenticate: Bearer resource_metadata="https://jumpcloud-clean.transcripts.fyne-llc.com/.well-known/oauth-protected-resource/mcp", scope="openid"`.
- Gateway env: `PUBLIC_BASE_URL=https://jumpcloud-clean.transcripts.fyne-llc.com`,
  `OIDC_AUTH_PROFILE=direct-oidc`,
  `OIDC_ISSUER_URL=https://oauth.id.jumpcloud.com/`,
  `OIDC_JWKS_URL=https://oauth.id.jumpcloud.com/.well-known/jwks.json`,
  `OIDC_CLIENT_ID=<active JumpCloud app client ID>`,
  `OIDC_REQUIRED_SCOPE=openid` unless a real custom JumpCloud scope is known to
  be issued in the access token,
  `OIDC_SCOPES_SUPPORTED=openid,email,profile,offline_access`,
  `OIDC_GROUP_CLAIM=<actual access-token claim, e.g. memberOf or ext.memberOf>`,
  and a dedicated non-admin MCP group or temporary subject allowlist.
- Claude connector: create one new connector, with the clean `/mcp` URL, the
  exact active JumpCloud client ID, the current client secret, and no duplicate
  connector records for that host.

Do not count browser login as success. Success requires Claude to send a
JumpCloud bearer to `/mcp` and complete a safe tool call.

## Evidence To Collect

- Gateway logs around the attempt, including missing bearer versus invalid token
  versus required group failure.
- Exact Claude `ofid_*` reference and UTC timestamp.
- JumpCloud Directory Insights rows for the same timestamp: event type,
  application name, result, OAuth error code if present, and whether
  `sso_token_success` is true.
- Browser HAR for the visible JumpCloud/Claude redirects, with secrets redacted.
- Exact protected-resource metadata JSON for the clean host.
- Exact unauthenticated 401 `WWW-Authenticate` response for `GET` and `POST
  /mcp`.
- Current JumpCloud app config snapshot: active app object, redirect URI,
  client authentication type, scopes, access-token format, group claim mapping,
  and bound users/groups.
- Local single-shot OAuth replay using the same JumpCloud client ID/secret
  entered in Claude.

## Code Review Findings

### P2: Cloudflare Worker fallback forwards untrusted identity headers

`deploy/remote-mcp-auth/cloudflare-worker/src/index.ts:93` copies all inbound
request headers, then only overwrites `Authorization` and
`X-Gongctl-Principal` and deletes `Cookie` and `CF-Access-Jwt-Assertion`.
Other client-supplied identity headers such as `X-Forwarded-Email`,
`X-Forwarded-User`, `X-Forwarded-Access-Token`, or `X-Auth-Request-Email` can
still reach the private upstream.

That is weaker than the Go gateway, whose proxy allowlist strips forged identity
headers and has a regression test in `internal/gateway/server_test.go:207`.
Before recommending the Worker as a production fallback, make its forwarding
logic use an explicit header allowlist and add tests for forged identity header
stripping.

### P2: Cloudflare Worker advertised scopes are hardcoded and can diverge from docs/env

`deploy/remote-mcp-auth/cloudflare-worker/src/index.ts:115` hardcodes
`scopesSupported` to `gongmcp:status`, `gongmcp:aggregate`, and
`gongmcp:search`, while the README documents `GONGMCP_ALLOWED_SCOPES` and the
rest of the gateway docs commonly use `gongmcp/read` or JumpCloud standard
scopes. The authorization handler grants `env.GONGMCP_ALLOWED_SCOPES`, so the
metadata and issued scopes can diverge.

Before using the Worker fallback for Claude, make metadata derive from the same
configured scopes that authorization grants, and align the docs with the final
scope names.

### P2: Direct JumpCloud docs over-default to `gongmcp/read`

The TC working shape in the prompt and the latest JumpCloud discovery support
standard scopes such as `openid`, `email`, `profile`, and `offline_access`.
However, `deploy/remote-mcp-auth/tradecentric-jumpcloud/gateway.env.example:18`
and the README examples default to `OIDC_REQUIRED_SCOPE=gongmcp/read`, and the
doctor command uses `--required-scope gongmcp/read`.

This is safe when JumpCloud is configured to issue that custom scope, but it is
misleading for the observed TC/lab shape where the challenge scope was
`openid`. Update the docs to say: use `openid` for the direct JumpCloud smoke
unless the JumpCloud app is known to issue a custom MCP read scope in the access
token.

### No high-severity Go gateway issue found

The direct-OIDC token validation path is opt-in through `OIDC_AUTH_PROFILE` /
`GATEWAY_AUTH_PROFILE`. Cognito remains strict for `token_use=access`,
`client_id`, `scope`, and nested-claim behavior. Direct-OIDC accepts the observed
JumpCloud/Ory shapes without accepting wrong issuer, wrong client binding,
wrong audience, missing scope, missing group, expired tokens, or forged
identity headers.

Residual test gaps are mostly around the fallback Worker, not the Go gateway.

## Support Packet

### Anthropic

Send:

- MCP URL and exact protected-resource metadata JSON for the fresh host.
- Exact unauthenticated 401 response headers for `GET` and `POST /mcp`.
- Claude `ofid_*` references and UTC timestamps.
- Confirmation that the connector used static JumpCloud client ID/secret in
  Advanced settings.
- Gateway logs showing whether Claude sent no bearer, stale bearer, or
  JumpCloud bearer.
- JumpCloud Directory Insights row proving whether token exchange succeeded.
- Statement that local OAuth replay with the same JumpCloud app succeeds or
  fails.

Ask Anthropic to confirm whether the connector token exchange succeeded, which
authorization server issuer and client ID were bound to the connector, and why
the stored bearer was not attached to `/mcp` when JumpCloud shows success.

### JumpCloud

Send:

- Active custom OIDC app name/object ID.
- Redirect URI list, client authentication type, access-token format, scopes,
  app activation status, and assigned groups/users.
- Directory Insights rows for the Claude attempt.
- Whether the token endpoint saw `client_secret_post`, `client_secret_basic`, or
  public PKCE.
- Whether the token request failed as `invalid_client`, `invalid_grant`,
  `invalid_scope`, redirect mismatch, app assignment/policy failure, or
  succeeded.

Ask JumpCloud to confirm that the auth flow is RP-initiated OIDC for the
registered Claude callback and not a User Portal launch.

## Stop Condition

Stop direct JumpCloud lab retries when all are true:

- fresh hostname, canonical `/mcp`, and no duplicate Claude connectors
- protected-resource `resource` exactly equals the URL entered in Claude
- first `authorization_servers` entry is `https://oauth.id.jumpcloud.com/`
- 401 challenge points at the clean host's endpoint metadata and uses the
  expected scope
- JumpCloud app exactly matches Claude callback, client ID, secret, client auth
  type, scopes, and assignment
- JumpCloud Directory Insights shows token success or a specific token-exchange
  result for the attempt
- gateway still logs only `missing bearer token`

At that point, do not weaken gateway validation. Use the broker fallback:
Cloudflare Worker OAuth broker plus Cloudflare Access backed by JumpCloud, after
fixing the Worker header/scope issues above. Use Cognito/DCR only if the
customer explicitly wants AWS-managed Cognito or if the broker decision favors
that operational model.

## Verification

Commands run from `/Users/arthurlee/src/gongctl`:

```bash
git fetch origin codex/tc-jumpcloud-rca-docs
curl -fsSL https://oauth.id.jumpcloud.com/.well-known/openid-configuration | jq '{issuer, authorization_endpoint, token_endpoint, jwks_uri, response_types_supported, grant_types_supported, token_endpoint_auth_methods_supported, code_challenge_methods_supported, registration_endpoint, client_id_metadata_document_supported, scopes_supported}'
curl -fsSI https://oauth.id.jumpcloud.com/.well-known/oauth-authorization-server
curl -fsSL https://oauth.id.jumpcloud.com/.well-known/jwks.json | jq '.keys | length'
go test -count=1 ./internal/gateway ./internal/cli
npm run typecheck
git diff --check -- internal/gateway internal/cli deploy/remote-mcp-auth docs
go test -count=1 ./...
make secret-scan
```

Results:

- Branch fetch matched `origin/codex/tc-jumpcloud-rca-docs` at
  `402314b4d88374572aff653fd6f2a7af8932f675`.
- JumpCloud OIDC discovery returned issuer
  `https://oauth.id.jumpcloud.com/`, JWKS URI, auth-code support, PKCE S256,
  `client_secret_post`, `client_secret_basic`, and `none`; no DCR/CIMD fields.
- JumpCloud OAuth Authorization Server metadata endpoint returned `404`.
- JumpCloud JWKS returned 6 keys.
- Go gateway/CLI tests passed.
- Cloudflare Worker TypeScript typecheck passed.
- Full Go test suite passed.
- Secret scan passed.
- `git diff --check` passed.
