# JumpCloud Claude Direct MCP Success RCA

Date: 2026-05-31

Audience: operators configuring Claude remote MCP against a customer-hosted
`gongmcp` gateway with JumpCloud as the OIDC provider.

## Summary

The direct JumpCloud + Claude remote MCP path worked after two non-code fixes:

1. Change the active JumpCloud OIDC app's token endpoint client authentication
   method from `Client Secret Basic` to `Client Secret POST`.
2. Delete the stale Claude custom connector and recreate it with the same clean
   `/mcp` URL and the JumpCloud static client ID/secret.

The successful connector was `gong-jumpcloud-post-20260531` against:

```text
https://jc-direct-20260530.transcripts.fyne-llc.com/mcp
```

Claude successfully called `get_sync_status` through the connector and returned
live `gongmcp` status for deployment `lab-20260530T224836Z`, preset
`business-pilot`, with clean sync health and cache counts of `14,311` calls,
`14,311` transcripts, and `1,281,697` transcript segments.

This was not a gateway code fix. The gateway metadata and direct-OIDC token
compatibility were already sufficient for this lab path. The blocker was the
JumpCloud app's client-auth method plus stale Claude connector/resource state.

## Impact

Before the fix, Claude returned generic `mcp_client_invalid` errors and the
gateway saw only unauthenticated `/mcp` attempts. This made the failure look
like a gateway, metadata, DCR, or bearer-validation issue. In reality, Claude
never reached the gateway with a usable bearer until the JumpCloud static-client
settings and Claude connector state were aligned.

## Evidence

Evidence bundle:

```text
tmp/jumpcloud-one-variable-2026-05-31/
```

High-signal files:

```text
tmp/jumpcloud-one-variable-2026-05-31/jumpcloud/token-probe-client_secret_post.json
tmp/jumpcloud-one-variable-2026-05-31/jumpcloud/token-probe-after-change-client_secret_post.json
tmp/jumpcloud-one-variable-2026-05-31/jumpcloud/token-probe-after-change-client_secret_basic.json
tmp/jumpcloud-one-variable-2026-05-31/jumpcloud/local-oauth-replay-after-client-secret-post.json
tmp/jumpcloud-one-variable-2026-05-31/claude/claude-chat-success-evidence.json
tmp/jumpcloud-one-variable-2026-05-31/keycloak/docker-prm-mcp-final.json
tmp/jumpcloud-one-variable-2026-05-31/jumpcloud/fresh-prm-final.json
```

The decisive token-endpoint probes were:

- Before the JumpCloud app change, `client_secret_post` returned `401
  invalid_client` and JumpCloud reported that the client supported
  `client_secret_basic`.
- After the JumpCloud app change, `client_secret_post` reached `400
  invalid_grant` for a fake code, which proves the client authentication method
  was accepted and the request advanced to grant validation.
- After the app change, `client_secret_basic` returned `401 invalid_client`,
  proving the active app had actually flipped to `client_secret_post`.
- A local browser OAuth replay then exchanged a real authorization code
  successfully with HTTP `200`.

The decisive Claude proof was not browser login. It was a tool call:

- Claude loaded the new connector.
- Claude requested `get_sync_status` from `gong-jumpcloud-post-20260531`.
- After approval, Claude returned live `gongmcp` status with deployment,
  preset, sync health, and cache counts.

## Timeline

1. A fresh-host baseline against `jc-direct-20260530.transcripts.fyne-llc.com`
   reproduced `mcp_client_invalid`; no bearer-bearing gateway request appeared.
2. Server metadata and `gongctl doctor mcp-gateway` checks passed, so the
   public MCP resource metadata was not the active blocker.
3. Directory Insights service-account probing failed with `401 invalid_client`,
   so IdP-side event evidence required admin UI/API access.
4. Token endpoint probes isolated the first concrete mismatch: Claude/TC-style
   static-client behavior required `client_secret_post`, while the active
   JumpCloud app was configured for `client_secret_basic`.
5. After admin login, the only JumpCloud variable changed was Client
   Authentication Type to `Client Secret POST`.
6. Token endpoint probes and a local OAuth replay confirmed the app now accepted
   `client_secret_post`.
7. Retrying the existing Claude connector still failed, so stale Claude
   connector state became the next suspect.
8. The old Claude connector was deleted before creating a new one.
9. The fresh Claude connector was created with the same `/mcp` URL and the
   JumpCloud static client ID/secret.
10. The connect flow stopped at JumpCloud developer-user login; Arthur completed
    the password step manually.
11. Claude loaded the connector and successfully called `get_sync_status`.
12. Docker/Keycloak metadata was rechecked and remained intact on
    `docker.transcripts.fyne-llc.com/mcp`.

## Root Cause

The direct root cause was a mismatch between Claude's token-exchange client
authentication method and the active JumpCloud OIDC app configuration.

The active JumpCloud app initially accepted `client_secret_basic`. The working
Claude/TC path required `client_secret_post`. Until the app was changed,
Claude could not complete the token exchange and therefore could not send a
bearer token to `/mcp`.

The second contributing cause was stale Claude connector state. After the
JumpCloud app was corrected, the existing connector still returned
`mcp_client_invalid`. Deleting and recreating the connector was required before
Claude used the corrected static-client configuration cleanly.

## Contributing Factors

- The same troubleshooting session had previously reused hostnames and
  connector state across Keycloak and JumpCloud attempts, which made stale
  client state plausible.
- Gateway containers log denials but do not emit enough positive audit lines
  for accepted MCP requests, so success had to be proven from Claude UI output
  plus absence of new server-side errors.
- `gongctl doctor mcp-gateway` verifies public metadata and gateway contract,
  but it does not currently test the provider's configured static client auth
  method against the real token endpoint.
- JumpCloud admin API/Directory Insights access was not available at first, so
  the investigation had to use browser UI and redacted token endpoint probes.
- The Claude custom connector form required transmitting a client secret; Codex
  correctly paused for explicit action-time confirmation, adding human latency.
- The test-user password was not in a safe secret path, so Arthur had to finish
  the JumpCloud login manually.

## What Worked

- A one-variable loop prevented accidental fixes from being conflated.
- Token endpoint probes with a fake code were useful and safe: `invalid_client`
  versus `invalid_grant` separated client-auth failure from grant failure.
- Deleting old Claude connectors before creating a replacement removed stale
  connector/resource state.
- A live tool call (`get_sync_status`) was the right proof of success.
- Keeping `docker.transcripts.fyne-llc.com/mcp` on Keycloak and testing
  JumpCloud on a separate host prevented the Keycloak lab from being broken
  while debugging JumpCloud.

## What Took Too Long

- Too much early time went into gateway and metadata variants after the
  protected-resource metadata was already passing.
- The JumpCloud app's client-auth method was not checked directly at the token
  endpoint until late.
- The old Claude connector was retried after the JumpCloud app changed; it
  should have been deleted immediately once a client-auth mismatch was fixed.
- Manual access dependencies were discovered incrementally instead of prepared
  up front.
- Positive gateway evidence was weak because accepted requests are not logged
  clearly enough.

## Documentation Improvements

Update operator docs and runbooks to say:

- For Claude custom connectors with JumpCloud static clients, set the JumpCloud
  OIDC app Client Authentication Type to `Client Secret POST`.
- Configure callback URL:
  `https://claude.ai/api/mcp/auth_callback`.
- When changing client ID, secret, redirect URI, scope, or client-auth method,
  delete the old Claude connector and recreate it. Do not assume reconnecting an
  old connector refreshes all static-client state.
- Treat `mcp_client_invalid` with no bearer-bearing gateway request as
  upstream of gateway bearer validation.
- Probe the token endpoint with both `client_secret_post` and
  `client_secret_basic` before changing gateway code.
- Success means Claude can call `get_sync_status`; browser login alone is not
  enough.

## Code Improvements To Consider

No gateway runtime code change was required for this success.

Useful follow-up improvements:

- Add a `gongctl doctor mcp-gateway` optional static-client token-endpoint auth
  probe that redacts secrets and reports whether the provider accepts
  `client_secret_post` or `client_secret_basic`.
- Make `doctor mcp-gateway --timeout` bound the whole command. One Docker
  Keycloak doctor probe hung past its own 15 second timeout and had to be
  terminated manually.
- Add positive gateway audit logs for accepted MCP requests at low sensitivity,
  such as request method, MCP method, auth profile, issuer, subject hash, and
  policy result. Do not log bearer tokens or full claim values.
- Add a runbook script that captures a complete evidence bundle: PRM, challenge,
  discovery, token-auth-method probe, gateway/Caddy logs, and connector
  timestamps.

## What Arthur Could Do To Help Next Time

- Provide the IdP admin session or admin API key before the loop starts.
- Put the test user's username/password in an agreed local secret path or be
  ready to complete the login immediately.
- Confirm up front whether Codex may paste a specific client ID/secret into a
  specific third-party form. Codex still needs action-time confirmation for the
  final secret transmission, but the prior context reduces back-and-forth.
- Delete stale Claude connectors before each materially different static-client
  retry, or explicitly tell Codex to delete them first.
- Keep one canonical comparison table for the known-working customer setup:
  issuer, scopes, redirect URI, client-auth method, client ID, app activation,
  app assignment, and connector URL.
- Share screenshots of the exact IdP fields when API access is unavailable,
  especially client-auth method, grant types, redirect URIs, and app assignment.

## Recommended Next Runbook Order

1. Verify `/mcp` challenge and endpoint protected-resource metadata.
2. Verify IdP discovery and `jwks_uri`.
3. Verify the static client accepts the expected token endpoint auth method.
4. Verify callback URL and scopes.
5. Delete old Claude connector.
6. Create fresh Claude connector.
7. Complete login.
8. Run `get_sync_status`.
9. Capture gateway/Caddy/IdP/Claude evidence.
10. Recheck any preserved parallel path, such as Docker/Keycloak.
