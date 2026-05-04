import { OAuthProvider } from "@cloudflare/workers-oauth-provider";
import { WorkerEntrypoint } from "cloudflare:workers";

interface Env {
  OAUTH_KV: KVNamespace;
  OAUTH_PROVIDER: {
    parseAuthRequest(request: Request): Promise<OAuthRequestInfo>;
    lookupClient(clientId: string): Promise<unknown>;
    completeAuthorization(input: CompleteAuthorizationInput): Promise<{ redirectTo: string }>;
  };
  PUBLIC_BASE_URL: string;
  GONGMCP_UPSTREAM_URL: string;
  GONGMCP_INTERNAL_BEARER_TOKEN: string;
  GONGMCP_ALLOWED_SCOPES: string;
  PILOT_ALLOWED_EMAILS: string;
}

interface OAuthRequestInfo {
  clientId: string;
  scope?: string[] | string;
}

interface CompleteAuthorizationInput {
  request: OAuthRequestInfo;
  userId: string;
  metadata: Record<string, string>;
  scope: string[];
  props: {
    email: string;
    scopes: string[];
  };
}

function scopes(env: Env): string[] {
  return env.GONGMCP_ALLOWED_SCOPES.split(/\s+/).map((scope) => scope.trim()).filter(Boolean);
}

function allowedEmails(env: Env): Set<string> {
  return new Set(
    env.PILOT_ALLOWED_EMAILS.split(",")
      .map((email) => email.trim().toLowerCase())
      .filter(Boolean),
  );
}

function currentUserEmail(request: Request): string | null {
  const email = request.headers.get("CF-Access-Authenticated-User-Email");
  return email ? email.trim().toLowerCase() : null;
}

function authorizePilotUser(env: Env, email: string): boolean {
  const allowlist = allowedEmails(env);
  return allowlist.size === 0 || allowlist.has(email);
}

const defaultHandler = {
  async fetch(request: Request, env: Env): Promise<Response> {
    const url = new URL(request.url);
    if (url.pathname !== "/authorize") {
      return new Response("Not found", { status: 404 });
    }

    const email = currentUserEmail(request);
    if (!email || !authorizePilotUser(env, email)) {
      return new Response("Forbidden", { status: 403 });
    }

    const oauthRequest = await env.OAUTH_PROVIDER.parseAuthRequest(request);
    await env.OAUTH_PROVIDER.lookupClient(oauthRequest.clientId);
    const grantedScopes = scopes(env);
    const { redirectTo } = await env.OAUTH_PROVIDER.completeAuthorization({
      request: oauthRequest,
      userId: email,
      metadata: { email },
      scope: grantedScopes,
      props: {
        email,
        scopes: grantedScopes,
      },
    });

    return Response.redirect(redirectTo, 302);
  },
};

class GongMcpHandler extends WorkerEntrypoint<Env> {
  async fetch(request: Request): Promise<Response> {
    const inboundURL = new URL(request.url);
    const upstreamURL = new URL(this.env.GONGMCP_UPSTREAM_URL);
    upstreamURL.search = inboundURL.search;
    const props = this.ctx.props as { email?: string };

    const headers = new Headers(request.headers);
    headers.set("Authorization", `Bearer ${this.env.GONGMCP_INTERNAL_BEARER_TOKEN}`);
    headers.set("X-Gongctl-Principal", String(props.email ?? "unknown"));
    headers.delete("Cookie");
    headers.delete("CF-Access-Jwt-Assertion");

    return fetch(upstreamURL.toString(), {
      method: request.method,
      headers,
      body: request.body,
      redirect: "manual",
    });
  }
}

export default new OAuthProvider({
  apiRoute: "/mcp",
  apiHandler: GongMcpHandler,
  defaultHandler,
  authorizeEndpoint: "/authorize",
  tokenEndpoint: "/token",
  clientRegistrationEndpoint: "/register",
  scopesSupported: ["gongmcp:status", "gongmcp:aggregate", "gongmcp:search"],
  allowImplicitFlow: false,
  allowPlainPKCE: false,
  refreshTokenTTL: 2592000,
  accessTokenTTL: 3600,
});
