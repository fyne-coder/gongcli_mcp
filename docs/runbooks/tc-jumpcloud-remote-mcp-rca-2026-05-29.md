# TradeCentric JumpCloud Remote MCP RCA

Date: 2026-05-29

Audience: DevOps and identity operators deploying Claude remote MCP with
JumpCloud in front of a private `gongmcp` service.

## Summary

The working path is direct JumpCloud OIDC through the MCP gateway. Cognito and
Dynamic Client Registration are not required when Claude can use a
pre-registered JumpCloud OIDC client through custom connector Advanced
settings.

The deployment still needs a public MCP gateway. JumpCloud handles human login
and token issuance. The gateway handles MCP protected-resource metadata,
`WWW-Authenticate` challenges, access-token validation, group policy, and
private forwarding to `gongmcp`.

Do not treat browser login success as the final proof. The proof is complete
only when Claude sends a bearer access token to `/mcp`, the gateway accepts the
configured group policy, and a safe tool call such as `get_sync_status`
succeeds.

As of the 2026-05-30 lab replay, the gateway and JumpCloud token-shape work is
proven locally, but the Claude.ai lab connector is still not end-to-end. The
latest clean direct-JumpCloud retry is `gong-jumpcloud-direct-r6` on
`https://docker.transcripts.fyne-llc.com/mcp-jc-8a8eed`, with protected-resource
metadata advertising JumpCloud's exact issuer
`https://oauth.id.jumpcloud.com/`. Claude still returned `mcp_client_invalid`
with reference `ofid_01270f5668fb0fa5`; VM logs for that retry show only
unauthenticated MCP requests and `missing bearer token`. JumpCloud Directory
Insights for the same window shows `sso_auth` with `sso_token_success=true`.
That combination means the current blocker is before gateway bearer validation
and before group/scope policy. Because TC has the same class of Claude
connector working directly with JumpCloud and without DCR, this is not evidence
that direct JumpCloud is impossible. It is evidence that the lab connector,
JumpCloud app, or metadata shape still differs from the TC working setup.

## Root Causes

1. The first deployment put `/mcp` behind browser/session auth. `oauth2-proxy`
   can validate a browser login, but hosted remote MCP clients need a bearer
   access-token flow at `/mcp`.
2. The investigation over-focused on Cognito because Cognito can bridge
   JumpCloud and AWS app clients. That was a fallback, not a requirement for
   the direct JumpCloud path.
3. Keycloak proved the general MCP OAuth shape, but it did not prove
   JumpCloud/Ory access-token claim compatibility. The local gateway code still
   had Cognito-shaped assumptions.
4. JumpCloud/Ory token shape can differ from Cognito:
   - access tokens may omit `token_use`
   - client identity may appear in `aud` instead of top-level `client_id`
   - scopes may appear in `scp` instead of only a string `scope`
   - group and email claims may appear under nested `ext`
5. The production authorization target was group-based access. Email allowlist
   is useful for a narrow diagnostic smoke, but it should not be the final
   access control when the customer wants directory groups.
6. Reusing the same public lab hostname for Keycloak and then JumpCloud caused
   Claude to replay stale Keycloak-issued bearer tokens at the JumpCloud-backed
   gateway. The gateway correctly rejected those tokens because the Keycloak
   signing key was absent from JumpCloud JWKS.
7. A temporary Caddy OAuth facade contributed to the stale-token loop by
   advertising the lab hostname as the authorization server instead of
   JumpCloud. For direct JumpCloud tests, protected-resource metadata must
   advertise `https://oauth.id.jumpcloud.com`.
8. After the stale-token and metadata issues were isolated with an alternate
   resource URL, Claude failed earlier with `mcp_client_invalid` and no bearer
   reached the gateway. This is not evidence for weakening gateway JWT
   validation; it is evidence to inspect JumpCloud Directory Insights and the
   Claude connector's static-client settings.

## Failure Ladder Seen Live

| Stage | Log or user symptom | Meaning | Next check |
| --- | --- | --- | --- |
| Metadata succeeds, then `/mcp` gets `missing bearer token` | `gateway auth denied ... reason=missing bearer token` | Claude reached `/mcp` without a usable bearer token. This is before group policy. | Static client setup, redirect URI, client auth mode, token exchange, and whether `/mcp` routes to the MCP gateway instead of a browser-session proxy. |
| Claude shows authorization failed and IdP logs show `invalid client` | Claude reference such as `ofid_...`; IdP token exchange error | Claude and JumpCloud did not agree on the client registration/authentication method. | Recreate the Claude connector, confirm `https://claude.ai/api/mcp/auth_callback`, client ID, secret, and JumpCloud client authentication method. |
| Gateway receives a Keycloak-shaped bearer while configured for JumpCloud | Bearer shape shows `iss=.../realms/gong-lab`, `aud=gong-lab-proxy`, and Keycloak signing `kid`; gateway logs `key not found` | Claude is replaying cached Keycloak auth state for the same MCP resource hostname. Gateway is failing closed correctly. | Delete connectors for the reused hostname, use a fresh resource URL or canonical JumpCloud metadata, and verify `authorization_servers=["https://oauth.id.jumpcloud.com"]`. |
| Alternate metadata is correct but Claude shows `mcp_client_invalid` and gateway logs only `missing bearer token` | Claude reference `ofid_c0ff7e1c62ac5c1d`; redacted VM logs show unauthenticated POSTs only | OAuth did not produce a stored bearer before Claude retried `/mcp`; the failure is upstream of gateway token validation. | Pull JumpCloud Directory Insights for the same time window; verify Client Secret POST, callback URI, app activation, scopes, and exact client ID/secret used in Claude Advanced settings. |
| Gateway rejects with required group missing | `reason=required group "..." missing` | A bearer token was present and validated far enough to reach authorization policy. | Decode the access token locally and check configured group claim, nested `ext`, and whether the user is in the dedicated JumpCloud MCP group. |
| First safe tool succeeds | `get_sync_status` works through Claude | Remote MCP path is working end to end. | Move from test group to dedicated production group and keep raw `gongmcp` private. |

## Decision Rule After The R6 Replay

Stop running blind direct JumpCloud connector variants when all of these are
true:

- protected-resource metadata `resource` exactly matches the Claude connector
  URL
- `authorization_servers` advertises JumpCloud's issuer with the trailing slash:
  `https://oauth.id.jumpcloud.com/`
- the JumpCloud app has the Claude callback URI:
  `https://claude.ai/api/mcp/auth_callback`
- JumpCloud Directory Insights shows `sso_auth` / `sso_token_success=true`
- the gateway still sees only `missing bearer token`
- JumpCloud discovery has no `registration_endpoint`, but the target TC setup is
  known to work without DCR

At that point, the next non-cycling direct-JumpCloud action is not another URL
or metadata variant. It is an exact comparison against the TC working setup:
Claude connector static-client fields, JumpCloud app client authentication
method, redirect URI list, scopes, app activation/assignment, and token-exchange
audit events. If the lab matches TC's working static-client shape and still
does not emit a bearer, then use a broker fallback that presents an MCP-shaped
OAuth surface to Claude while delegating human login to JumpCloud. The smallest
repo-supported fallback is the Cloudflare Worker OAuth broker in
`deploy/remote-mcp-auth/cloudflare-worker/`, with Cloudflare Access configured
to use JumpCloud as its identity provider. The existing Cognito DCR gateway is
another fallback when the customer explicitly wants Cognito.

## 2026-05-30 Lab Replay RCA

### Timeline

1. The Keycloak lab proved the remote MCP OAuth shape on
   `https://docker.transcripts.fyne-llc.com/mcp`.
2. The JumpCloud admin setup initially failed before any gateway callback
   because the user/app assignment was incomplete.
3. After app activation and assignment, a local direct JumpCloud E2E succeeded:
   authorization code callback, token exchange, gateway `initialize`,
   `tools/list`, and `get_sync_status` all passed. Evidence:
   `tmp/jumpcloud-e2e-result-2026-05-30.json`.
4. Sanitized token evidence showed JumpCloud access tokens use issuer
   `https://oauth.id.jumpcloud.com/`, `scp`, `client_id`, and nested `ext`
   claims, with no Cognito-style `token_use`. Evidence:
   `tmp/jumpcloud-token-claims-2026-05-30.json`.
5. The public Claude retry initially sent stale Keycloak tokens to the
   JumpCloud gateway. Those tokens had the Keycloak realm issuer, client
   audience `gong-lab-proxy`, and Keycloak signing key ID. The gateway rejected
   them against JumpCloud JWKS, which is the correct fail-closed behavior.
6. The VM route was adjusted so an alternate MCP URL,
   `https://docker.transcripts.fyne-llc.com/mcp-jc-8a8eed`, challenged with
   alternate protected-resource metadata, and that metadata advertised
   JumpCloud as the authorization server.
7. Public doctor checks against the JumpCloud issuer then passed 14/14 for
   metadata, CORS, unauthenticated challenge, OIDC discovery, and JWKS.
   Evidence: `tmp/jumpcloud-doctor-after-direct-metadata-2026-05-30.json`.
8. A fresh Claude connector on the alternate URL failed with
   `mcp_client_invalid` (`ofid_c0ff7e1c62ac5c1d`). The VM saw only
   unauthenticated MCP POSTs and `missing bearer token`; no new Keycloak bearer
   and no JumpCloud bearer reached the gateway.

### Current Interpretation

The first public lab issue was stale Claude connector state caused by reusing a
Keycloak-era resource hostname and advertising the wrong authorization server.
That issue appears isolated by the alternate URL and corrected metadata.

The current issue is a different stage. Because no bearer reaches the gateway,
the active failure is before `VerifyBearerToken` can validate issuer, JWKS,
scope, group, or subject policy. The likely causes are:

- Claude Advanced settings do not match the active JumpCloud app's client ID or
  secret.
- JumpCloud app client authentication is not `Client Secret POST`, or Claude is
  sending a token request shape JumpCloud rejects.
- The JumpCloud app does not exactly allow
  `https://claude.ai/api/mcp/auth_callback`.
- The app is inactive, the wrong JumpCloud Custom Application is being used, or
  the test user/group binding is attached to a different app object.
- The scopes/grant types allowed in JumpCloud do not match what Claude requests
  after reading the MCP challenge.

### 2026-05-30 Doctor Fix And Replay

The lab replay exposed a doctor blind spot: `doctor mcp-gateway` treated the
OIDC issuer and the OAuth authorization server as the same URL, and it assumed
the public MCP endpoint was exactly `/mcp`. That missed the failure shape where
JumpCloud OIDC discovery succeeds but OAuth Authorization Server metadata is
missing or served from an alternate gateway-hosted authorization-server path.

The doctor now separates these concepts:

- `--issuer` checks the provider OIDC discovery and JWKS used by the gateway.
- `--authorization-server` checks the authorization server advertised in MCP
  protected-resource metadata.
- alternate MCP paths such as `/mcp-jc-as-*` are checked against their matching
  endpoint protected-resource metadata and `WWW-Authenticate` challenge.
- OAuth Authorization Server metadata is checked for authorization-code shape,
  HTTPS authorization/token endpoints, compatible token endpoint auth methods,
  and PKCE S256 when advertised.

Live replay against the lab alternate path passed after forcing Go to use the Go
DNS resolver on this Mac:

```bash
GODEBUG=netdns=go go run ./cmd/gongctl doctor mcp-gateway \
  --url https://docker.transcripts.fyne-llc.com/mcp-jc-as-1c3d7f \
  --issuer https://oauth.id.jumpcloud.com \
  --authorization-server https://docker.transcripts.fyne-llc.com/jc-as-1c3d7f \
  --profile direct-oidc \
  --required-scope openid \
  --timeout 10s
```

Evidence file: `tmp/jumpcloud-doctor-as-metadata-2026-05-30.json`. The only
non-pass check is the expected warning that root protected-resource metadata
resource validation is skipped for an alternate MCP path; endpoint metadata and
the challenge are the source of truth for the alternate path.

Claude still returned `mcp_client_invalid` on retry, with support reference
`ofid_c1624275a4672ec9`. Gateway/token-shape logs showed another no-bearer
request, so the current failure remains upstream of gateway authorization.

### 2026-05-30 R6 Replay And Review

A final clean direct-JumpCloud connector, `gong-jumpcloud-direct-r6`, was
created after rejecting a malformed `r5` connector whose saved URL contained
the OAuth fields. The r6 connector saved the clean URL
`https://docker.transcripts.fyne-llc.com/mcp-jc-8a8eed`. The Caddy metadata for
that resource was corrected to advertise JumpCloud's exact discovery issuer,
`https://oauth.id.jumpcloud.com/`.

The r6 retry still failed:

- Claude support reference: `ofid_01270f5668fb0fa5`
- gateway log: `2026/05/30 22:15:51 gateway auth denied ... reason=missing bearer token`
- JumpCloud Directory Insights: `2026-05-30T22:15:50Z`, `event_type=sso_auth`,
  `sso_token_success=true`, application `Gong MCP Claude Active`

Claude review artifact:
`/Users/arthurlee/src/agent_tmux/runs/stress/20260530T181724-0400-49380-jumpcloud-r6-no-bearer-review/task-runs/review/artifacts/claude.md`.
Claude recommended a broker path, but that recommendation should be read as a
lab fallback rather than a contradiction of the TC direct-JumpCloud proof.
Cursor produced the same broker recommendation at
`/Users/arthurlee/src/agent_tmux/runs/stress/20260530T181724-0400-49380-jumpcloud-r6-no-bearer-review/task-runs/review/artifacts/cursor.md`.

The stronger conclusion after comparing with TC is narrower: direct JumpCloud
without DCR can work for Claude, so this lab must be brought to parity with the
TC static-client configuration before declaring the direct path exhausted.

### Next Debug Evidence

Do not run another blind Claude connector retry first. Collect one of these
evidence sets during the next controlled attempt:

1. JumpCloud Directory Insights for the `ofid_c1624275a4672ec9` timeframe, plus
   earlier `ofid_229dac049e8782c6` and `ofid_c0ff7e1c62ac5c1d` attempts, and
   the active Custom Application. Capture redacted event type, result, OAuth
   error code, application name, and timestamp only. The important distinction
   is `invalid_client` versus `invalid_grant` versus `invalid_scope` versus no
   JumpCloud event at all.
2. A redacted JumpCloud app configuration snapshot: active status, callback
   URIs, grant types, scopes, Client Authentication Type, and bound groups. Do
   not record the full client secret.
3. A local single-shot OAuth replay using the exact same static-client config
   entered into Claude. If local replay succeeds but Claude still returns
   `mcp_client_invalid`, the remaining evidence points to Claude connector
   compatibility or cached connector state.
4. A Claude connector inventory for the affected account, with only server URL
   and redacted client ID suffixes, to remove duplicate connectors pointing at
   `docker.transcripts.fyne-llc.com`.

### What Not To Change For This Failure

- Do not accept Keycloak and JumpCloud issuers on the same JumpCloud gateway
  profile.
- Do not disable signature, expiry, issuer, JWKS, scope, or forged-header
  checks.
- Do not switch the production gate to subject allowlist. Subject allowlist is
  only a lab diagnostic fallback while JumpCloud group claims are missing from
  the access token.
- Do not treat doctor metadata success as end-to-end success. The end-to-end
  proof still requires a Claude-sent JumpCloud bearer and a safe tool call.

## Decision On Mihai's Local Auth Changes

Mihai's local gateway changes are evidence of the compatibility gap, not a
patch to copy directly into production.

Use the idea as a supported gateway feature in a separate code slice:

- add a provider profile such as `cognito` versus `direct-oidc` or
  `jumpcloud`
- keep Cognito strict by default
- allow JumpCloud/Ory compatibility only when that profile is enabled
- extract `scope` and `scp`
- read configured group/email claims from top-level claims and nested `ext`
- allow client binding through `aud` only for the direct-OIDC profile
- preserve signature, issuer, expiry, algorithm, bearer-size, and forged-header
  protections

Required tests before merging the code slice:

- Cognito still requires `token_use=access` and the expected `client_id`
- JumpCloud-shaped tokens can use `scp` and nested `ext.memberOf` only with the
  JumpCloud/direct-OIDC profile enabled
- wrong issuer, wrong audience/client, missing scope, missing group, expired
  token, and bad signature remain rejected
- forged client-supplied identity headers are ignored or stripped
- email allowlist remains diagnostic, not the preferred production gate

## Operator Guidance

Use one dedicated JumpCloud group for the connector, for example
`GongMCP-Users` or `AI-Data-Readers`. Do not use broad administrative groups
such as `AWS-Admin` for production access.

The 2026-05-30 local JumpCloud token showed group and email details in the ID
token and nested access-token `ext`, not in top-level access-token claims. Before
production, either configure JumpCloud to emit the required group claim in the
bearer access token or use a broker that mints a gateway-facing bearer with the
dedicated MCP group claim. Do not call the path production-ready on subject
allowlist alone.

For a debug token, decode locally only:

```bash
python3 - <<'PY'
import base64, json, os
token = os.environ["ACCESS_TOKEN"]
payload = token.split(".")[1]
payload += "=" * (-len(payload) % 4)
claims = json.loads(base64.urlsafe_b64decode(payload))
for key in ["iss", "sub", "aud", "client_id", "scope", "scp", "email", "groups", "memberOf", "ext"]:
    if key in claims:
        print(key, "=", claims[key])
PY
```

Do not paste customer access tokens into hosted JWT decoder sites or Slack.

## Final Architecture

```text
Claude custom connector
  -> public HTTPS /mcp gateway
  -> JumpCloud OIDC login and access token
  -> gateway validates token, scope, and dedicated group
  -> gateway injects internal bearer token
  -> private gongmcp
  -> governed SQLite cache or Postgres reader role
```
