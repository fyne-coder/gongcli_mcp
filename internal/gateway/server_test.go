package gateway

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
	"github.com/golang-jwt/jwt/v5"
)

func TestProtectedResourceMetadata(t *testing.T) {
	srv := NewServer(testConfig(t, nil), testAuthorizer(t, testConfig(t, nil), mustKey(t)))
	req := httptest.NewRequest(http.MethodGet, "https://mcp.example.test/.well-known/oauth-protected-resource/mcp", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`"resource":"https://mcp.example.test/mcp"`,
		`"authorization_servers":["https://cognito-idp.us-east-1.amazonaws.com/us-east-1_pool"]`,
		`"scopes_supported":["gongmcp/read"]`,
		`"bearer_methods_supported":["header"]`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metadata missing %s in %s", want, body)
		}
	}
}

func TestDCRProtectedResourceAdvertisesGatewayAuthorizationServer(t *testing.T) {
	cfg := testConfig(t, nil)
	cfg.DCREnabled = true
	cfg.CognitoDomainURL = "https://customer.auth.us-east-1.amazoncognito.com"
	cfg.DCRAllowedScopes = []string{"openid", "email", "gongmcp/read"}
	srv := NewServerWithDCR(cfg, testAuthorizer(t, cfg, mustKey(t)), &fakeDCRRegistrar{})
	req := httptest.NewRequest(http.MethodGet, "https://mcp.example.test/.well-known/oauth-protected-resource/mcp", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"authorization_servers":["https://mcp.example.test"]`) {
		t.Fatalf("metadata did not advertise gateway auth server: %s", rec.Body.String())
	}
}

func TestDCRAuthorizationServerMetadata(t *testing.T) {
	cfg := testConfig(t, nil)
	cfg.DCREnabled = true
	cfg.CognitoDomainURL = "https://customer.auth.us-east-1.amazoncognito.com"
	cfg.DCRAllowedScopes = []string{"openid", "email", "gongmcp/read"}
	srv := NewServerWithDCR(cfg, testAuthorizer(t, cfg, mustKey(t)), &fakeDCRRegistrar{})
	req := httptest.NewRequest(http.MethodGet, "https://mcp.example.test/.well-known/oauth-authorization-server", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`"issuer":"https://mcp.example.test"`,
		`"authorization_endpoint":"https://customer.auth.us-east-1.amazoncognito.com/oauth2/authorize"`,
		`"token_endpoint":"https://customer.auth.us-east-1.amazoncognito.com/oauth2/token"`,
		`"registration_endpoint":"https://mcp.example.test/register"`,
		`"code_challenge_methods_supported":["S256"]`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metadata missing %s in %s", want, body)
		}
	}
}

func TestDCRRegisterCreatesCognitoBackedPublicPKCEClient(t *testing.T) {
	cfg := testDCRConfig(t)
	registrar := &fakeDCRRegistrar{clientID: "generated-client-id"}
	srv := NewServerWithDCR(cfg, testAuthorizer(t, cfg, mustKey(t)), registrar)
	req := httptest.NewRequest(http.MethodPost, "https://mcp.example.test/register", strings.NewReader(`{
		"redirect_uris":["https://claude.ai/api/mcp/auth_callback"],
		"token_endpoint_auth_method":"none",
		"grant_types":["authorization_code"],
		"response_types":["code"],
		"client_name":"Claude",
		"scope":"openid email gongmcp/read"
	}`))
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got DCRClientRegistrationResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ClientID != "generated-client-id" {
		t.Fatalf("client_id=%q", got.ClientID)
	}
	if got.TokenEndpointAuthMethod != "none" {
		t.Fatalf("token endpoint auth method=%q", got.TokenEndpointAuthMethod)
	}
	if registrar.seen.Scope != "openid email gongmcp/read" {
		t.Fatalf("registered scope=%q", registrar.seen.Scope)
	}
}

func TestDCRRegisterRejectsUnsafeMetadata(t *testing.T) {
	cfg := testDCRConfig(t)
	tests := []struct {
		name string
		body string
	}{
		{
			name: "bad redirect",
			body: `{"redirect_uris":["https://evil.example.test/callback"],"scope":"openid email gongmcp/read"}`,
		},
		{
			name: "secret auth",
			body: `{"redirect_uris":["https://claude.ai/api/mcp/auth_callback"],"token_endpoint_auth_method":"client_secret_basic","scope":"openid email gongmcp/read"}`,
		},
		{
			name: "implicit grant",
			body: `{"redirect_uris":["https://claude.ai/api/mcp/auth_callback"],"grant_types":["implicit"],"scope":"openid email gongmcp/read"}`,
		},
		{
			name: "missing required scope",
			body: `{"redirect_uris":["https://claude.ai/api/mcp/auth_callback"],"scope":"openid email"}`,
		},
		{
			name: "unapproved scope",
			body: `{"redirect_uris":["https://claude.ai/api/mcp/auth_callback"],"scope":"openid email gongmcp/read admin"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := NewServerWithDCR(cfg, testAuthorizer(t, cfg, mustKey(t)), &fakeDCRRegistrar{})
			req := httptest.NewRequest(http.MethodPost, "https://mcp.example.test/register", strings.NewReader(tt.body))
			rec := httptest.NewRecorder()

			srv.Handler().ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d want 400 body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestUnauthenticatedMCPReturnsBearerChallenge(t *testing.T) {
	cfg := testConfig(t, nil)
	srv := NewServer(cfg, testAuthorizer(t, cfg, mustKey(t)))
	req := httptest.NewRequest(http.MethodPost, "https://mcp.example.test/mcp", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401 body=%s", rec.Code, rec.Body.String())
	}
	challenge := rec.Header().Get("WWW-Authenticate")
	if !strings.Contains(challenge, `resource_metadata="https://mcp.example.test/.well-known/oauth-protected-resource/mcp"`) {
		t.Fatalf("challenge missing resource metadata: %q", challenge)
	}
	if !strings.Contains(challenge, `scope="gongmcp/read"`) {
		t.Fatalf("challenge missing scope: %q", challenge)
	}
}

func TestAuthenticateRejectsMalformedOrOversizedAuthorization(t *testing.T) {
	key := mustKey(t)
	cfg := testConfig(t, nil)
	cfg.MaxBearerBytes = 8
	authorizer := testAuthorizer(t, cfg, key)
	for _, authorization := range []string{
		"",
		"Basic abc123",
		"Bearer ",
		"Bearer " + strings.Repeat("x", 9),
	} {
		if principal, err := authorizer.Authenticate(t.Context(), authorization); err == nil {
			t.Fatalf("expected %q to fail, got principal %+v", authorization, principal)
		}
	}
}

func TestMCPValidTokenProxiesWithInternalBearerAndStripsIdentityHeaders(t *testing.T) {
	key := mustKey(t)
	var gotAuth, gotPrincipal, gotForwardedEmail, gotAccessJWT, gotForwardedAccessToken, gotPath, gotSession string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPrincipal = r.Header.Get("X-Gongctl-Principal")
		gotForwardedEmail = r.Header.Get("X-Forwarded-Email")
		gotAccessJWT = r.Header.Get("CF-Access-Jwt-Assertion")
		gotForwardedAccessToken = r.Header.Get("X-Forwarded-Access-Token")
		gotPath = r.URL.Path
		gotSession = r.Header.Get("Mcp-Session-Id")
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)

	cfg := testConfig(t, mustURL(t, upstream.URL))
	srv := NewServer(cfg, testAuthorizer(t, cfg, key))
	token := signToken(t, key, cfg, func(claims jwt.MapClaims) {})
	req := httptest.NewRequest(http.MethodPost, "https://mcp.example.test/mcp", strings.NewReader(`{"jsonrpc":"2.0"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Mcp-Session-Id", "session-1")
	req.Header.Set("X-Forwarded-Email", "attacker@example.test")
	req.Header.Set("CF-Access-Jwt-Assertion", "attacker-jwt")
	req.Header.Set("X-Forwarded-Access-Token", "attacker-access-placeholder")
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status=%d want 204 body=%s", rec.Code, rec.Body.String())
	}
	if gotPath != "/mcp" {
		t.Fatalf("upstream path=%q want /mcp", gotPath)
	}
	if gotAuth != testBearerHeader("internal-upstream-placeholder") {
		t.Fatalf("upstream auth=%q", gotAuth)
	}
	if gotPrincipal != "approved@example.test" {
		t.Fatalf("principal=%q", gotPrincipal)
	}
	if gotSession != "session-1" {
		t.Fatalf("session header=%q", gotSession)
	}
	if gotForwardedEmail != "" || gotAccessJWT != "" || gotForwardedAccessToken != "" {
		t.Fatalf("identity headers leaked forwarded_email=%q access_jwt=%q forwarded_access_token=%q", gotForwardedEmail, gotAccessJWT, gotForwardedAccessToken)
	}
}

func TestMCPGetUsesSameAuthProxyPath(t *testing.T) {
	key := mustKey(t)
	var gotMethod string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)

	cfg := testConfig(t, mustURL(t, upstream.URL))
	srv := NewServer(cfg, testAuthorizer(t, cfg, key))
	token := signToken(t, key, cfg, func(claims jwt.MapClaims) {})
	req := httptest.NewRequest(http.MethodGet, "https://mcp.example.test/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status=%d want 204 body=%s", rec.Code, rec.Body.String())
	}
	if gotMethod != http.MethodGet {
		t.Fatalf("upstream method=%q", gotMethod)
	}
}

func TestMCPDeleteUsesSameAuthProxyPath(t *testing.T) {
	key := mustKey(t)
	var gotMethod string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)

	cfg := testConfig(t, mustURL(t, upstream.URL))
	srv := NewServer(cfg, testAuthorizer(t, cfg, key))
	token := signToken(t, key, cfg, func(claims jwt.MapClaims) {})
	req := httptest.NewRequest(http.MethodDelete, "https://mcp.example.test/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status=%d want 204 body=%s", rec.Code, rec.Body.String())
	}
	if gotMethod != http.MethodDelete {
		t.Fatalf("upstream method=%q", gotMethod)
	}
}

func TestMCPPreflightRequiresAllowedOrigin(t *testing.T) {
	cfg := testConfig(t, nil)
	srv := NewServer(cfg, testAuthorizer(t, cfg, mustKey(t)))

	allowed := httptest.NewRequest(http.MethodOptions, "https://mcp.example.test/mcp", nil)
	allowed.Header.Set("Origin", "https://claude.ai")
	allowedRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(allowedRec, allowed)
	if allowedRec.Code != http.StatusNoContent {
		t.Fatalf("allowed preflight status=%d", allowedRec.Code)
	}
	if got := allowedRec.Header().Get("Access-Control-Allow-Origin"); got != "https://claude.ai" {
		t.Fatalf("allow origin=%q", got)
	}

	blocked := httptest.NewRequest(http.MethodOptions, "https://mcp.example.test/mcp", nil)
	blocked.Header.Set("Origin", "https://evil.example.test")
	blockedRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(blockedRec, blocked)
	if blockedRec.Code != http.StatusForbidden {
		t.Fatalf("blocked preflight status=%d want 403", blockedRec.Code)
	}
}

func TestMCPActualRequestRequiresAllowedOrigin(t *testing.T) {
	key := mustKey(t)
	cfg := testConfig(t, nil)
	srv := NewServer(cfg, testAuthorizer(t, cfg, key))
	token := signToken(t, key, cfg, func(claims jwt.MapClaims) {})
	req := httptest.NewRequest(http.MethodPost, "https://mcp.example.test/mcp", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Origin", "https://evil.example.test")
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d want 403 body=%s", rec.Code, rec.Body.String())
	}
}

func TestMCPRejectsOversizedDeclaredBody(t *testing.T) {
	key := mustKey(t)
	cfg := testConfig(t, nil)
	cfg.MaxRequestBytes = 3
	srv := NewServer(cfg, testAuthorizer(t, cfg, key))
	token := signToken(t, key, cfg, func(claims jwt.MapClaims) {})
	req := httptest.NewRequest(http.MethodPost, "https://mcp.example.test/mcp", strings.NewReader(`{}`))
	req.ContentLength = 4
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d want 413 body=%s", rec.Code, rec.Body.String())
	}
}

func TestVerifyAccessTokenRejectsBadClaims(t *testing.T) {
	key := mustKey(t)
	cfg := testConfig(t, nil)
	authorizer := testAuthorizer(t, cfg, key)
	tests := []struct {
		name   string
		mutate func(jwt.MapClaims)
	}{
		{name: "wrong issuer", mutate: func(claims jwt.MapClaims) { claims["iss"] = "https://issuer.example.test" }},
		{name: "wrong client", mutate: func(claims jwt.MapClaims) { claims["client_id"] = "other-client" }},
		{name: "wrong token use", mutate: func(claims jwt.MapClaims) { claims["token_use"] = "id" }},
		{name: "missing scope", mutate: func(claims jwt.MapClaims) { claims["scope"] = "openid email" }},
		{name: "missing group", mutate: func(claims jwt.MapClaims) { claims["cognito:groups"] = []string{"other"} }},
		{name: "wrong audience", mutate: func(claims jwt.MapClaims) { claims["aud"] = "https://other.example.test/mcp" }},
		{name: "expired", mutate: func(claims jwt.MapClaims) { claims["exp"] = time.Now().Add(-2 * time.Hour).Unix() }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token := signToken(t, key, cfg, tt.mutate)
			if principal, err := authorizer.VerifyAccessToken(t.Context(), token); err == nil {
				t.Fatalf("expected token to fail, got principal %+v", principal)
			}
		})
	}
}

func TestVerifyAccessTokenAcceptsConfiguredGroupClaim(t *testing.T) {
	key := mustKey(t)
	cfg := testConfig(t, nil)
	cfg.GroupClaim = "custom:jumpcloud_groups"
	authorizer := testAuthorizer(t, cfg, key)
	token := signToken(t, key, cfg, func(claims jwt.MapClaims) {
		delete(claims, "cognito:groups")
		claims["custom:jumpcloud_groups"] = []string{"gongmcp-users"}
	})

	principal, err := authorizer.VerifyAccessToken(t.Context(), token)
	if err != nil {
		t.Fatalf("VerifyAccessToken failed: %v", err)
	}
	if !contains(principal.Groups, "gongmcp-users") {
		t.Fatalf("groups=%v", principal.Groups)
	}
}

func TestVerifyAccessTokenAcceptsConfiguredGroupClaimString(t *testing.T) {
	key := mustKey(t)
	cfg := testConfig(t, nil)
	cfg.GroupClaim = "custom:jumpcloud_groups"
	authorizer := testAuthorizer(t, cfg, key)
	token := signToken(t, key, cfg, func(claims jwt.MapClaims) {
		delete(claims, "cognito:groups")
		claims["custom:jumpcloud_groups"] = "other,gongmcp-users"
	})

	principal, err := authorizer.VerifyAccessToken(t.Context(), token)
	if err != nil {
		t.Fatalf("VerifyAccessToken failed: %v", err)
	}
	if !contains(principal.Groups, "gongmcp-users") {
		t.Fatalf("groups=%v", principal.Groups)
	}
}

func TestVerifyAccessTokenRejectsUnexpectedSigningMethods(t *testing.T) {
	key := mustKey(t)
	cfg := testConfig(t, nil)
	authorizer := NewAuthorizer(cfg, func(token *jwt.Token) (any, error) {
		return &key.PublicKey, nil
	})

	noneToken := jwt.NewWithClaims(jwt.SigningMethodNone, baseClaims(cfg))
	unsigned, err := noneToken.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatal(err)
	}
	if principal, err := authorizer.VerifyAccessToken(t.Context(), unsigned); err == nil {
		t.Fatalf("expected alg none to fail, got principal %+v", principal)
	}

	hsToken := jwt.NewWithClaims(jwt.SigningMethodHS256, baseClaims(cfg))
	signed, err := hsToken.SignedString([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	if principal, err := authorizer.VerifyAccessToken(t.Context(), signed); err == nil {
		t.Fatalf("expected HS256 to fail, got principal %+v", principal)
	}
}

func TestLoadConfigRequiresHTTPSPublicBaseURL(t *testing.T) {
	tokenFile := t.TempDir() + "/token"
	if err := osWriteFile(tokenFile, "internal-upstream-placeholder"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GATEWAY_INTERNAL_BEARER_TOKEN_FILE", tokenFile)
	t.Setenv("PUBLIC_BASE_URL", "http://mcp.example.test")
	t.Setenv("COGNITO_ISSUER_URL", "https://cognito-idp.us-east-1.amazonaws.com/us-east-1_pool")
	t.Setenv("COGNITO_CLIENT_ID", "client-id")

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected http public base URL to fail")
	}
	if !strings.Contains(err.Error(), "PUBLIC_BASE_URL") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadConfigRequiresHTTPSIssuerAndJWKS(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{
			name: "issuer",
			env:  map[string]string{"COGNITO_ISSUER_URL": "http://cognito-idp.us-east-1.amazonaws.com/us-east-1_pool"},
			want: "COGNITO_ISSUER_URL",
		},
		{
			name: "jwks",
			env: map[string]string{
				"COGNITO_ISSUER_URL": "https://cognito-idp.us-east-1.amazonaws.com/us-east-1_pool",
				"COGNITO_JWKS_URL":   "http://cognito-idp.us-east-1.amazonaws.com/us-east-1_pool/.well-known/jwks.json",
			},
			want: "COGNITO_JWKS_URL",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setLoadConfigBaseEnv(t)
			for key, value := range tt.env {
				t.Setenv(key, value)
			}

			_, err := LoadConfig()
			if err == nil {
				t.Fatal("expected LoadConfig to fail")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error=%v want %s", err, tt.want)
			}
		})
	}
}

func TestLoadConfigRequiresAccessPolicyGate(t *testing.T) {
	setLoadConfigBaseEnv(t)
	t.Setenv("COGNITO_REQUIRED_GROUP", "")
	t.Setenv("COGNITO_ALLOWED_SUBJECTS", "")
	t.Setenv("COGNITO_ALLOWED_EMAILS", "")

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected missing access policy gate to fail")
	}
	if !strings.Contains(err.Error(), "at least one") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadConfigDCRRequiresExplicitCognitoRegistrationSettings(t *testing.T) {
	setLoadConfigBaseEnv(t)
	t.Setenv("GATEWAY_DCR_ENABLED", "true")
	t.Setenv("COGNITO_DOMAIN_URL", "https://customer.auth.us-east-1.amazoncognito.com")
	t.Setenv("COGNITO_USER_POOL_ID", "us-east-1_pool")
	t.Setenv("COGNITO_DCR_ALLOWED_REDIRECT_URIS", "https://claude.ai/api/mcp/auth_callback")
	t.Setenv("COGNITO_DCR_IDENTITY_PROVIDERS", "JumpCloud")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if !cfg.DCREnabled {
		t.Fatal("DCR should be enabled")
	}
	if cfg.AuthorizationServerURL() != "https://mcp.example.test" {
		t.Fatalf("authorization server=%q", cfg.AuthorizationServerURL())
	}
	if !contains(cfg.DCRAllowedScopes, "gongmcp/read") {
		t.Fatalf("allowed scopes=%v", cfg.DCRAllowedScopes)
	}
}

func TestDynamicClientVerifierAcceptsConfiguredClientAndVerifierApprovedClient(t *testing.T) {
	key := mustKey(t)
	cfg := testDCRConfig(t)
	verifier := &fakeClientVerifier{allowed: map[string]struct{}{
		cfg.ClientID:          {},
		"generated-client-id": {},
	}}
	authorizer := NewAuthorizerWithClientVerifier(cfg, func(token *jwt.Token) (any, error) {
		return &key.PublicKey, nil
	}, verifier)
	token := signToken(t, key, cfg, func(claims jwt.MapClaims) {
		claims["client_id"] = "generated-client-id"
	})

	if _, err := authorizer.VerifyAccessToken(t.Context(), token); err != nil {
		t.Fatalf("VerifyAccessToken failed: %v", err)
	}
}

func TestCognitoClientStoreVerifyClientIDGatesDynamicClients(t *testing.T) {
	cfg := testDCRConfig(t)
	tests := []struct {
		name    string
		client  types.UserPoolClientType
		wantErr string
	}{
		{
			name: "accepts gateway-created client",
			client: testUserPoolClient(cfg, func(client *types.UserPoolClientType) {
			}),
		},
		{
			name: "rejects prefix mismatch",
			client: testUserPoolClient(cfg, func(client *types.UserPoolClientType) {
				client.ClientName = aws.String("manual-client")
			}),
			wantErr: "gateway-created",
		},
		{
			name: "rejects oauth disabled",
			client: testUserPoolClient(cfg, func(client *types.UserPoolClientType) {
				client.AllowedOAuthFlowsUserPoolClient = aws.Bool(false)
			}),
			wantErr: "OAuth flows disabled",
		},
		{
			name: "rejects missing code flow",
			client: testUserPoolClient(cfg, func(client *types.UserPoolClientType) {
				client.AllowedOAuthFlows = []types.OAuthFlowType{types.OAuthFlowTypeImplicit}
			}),
			wantErr: "authorization code",
		},
		{
			name: "rejects missing required scope",
			client: testUserPoolClient(cfg, func(client *types.UserPoolClientType) {
				client.AllowedOAuthScopes = []string{"openid", "email"}
			}),
			wantErr: "required scope",
		},
		{
			name: "rejects missing allowed callback",
			client: testUserPoolClient(cfg, func(client *types.UserPoolClientType) {
				client.CallbackURLs = []string{"https://evil.example.test/callback"}
			}),
			wantErr: "allowed callback",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewCognitoClientStoreWithClient(cfg, &fakeCognitoClient{
				describeOutput: &cognitoidentityprovider.DescribeUserPoolClientOutput{
					UserPoolClient: &tt.client,
				},
			})
			err := store.VerifyClientID(t.Context(), "generated-client-id")
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("VerifyClientID failed: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error=%v want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestCognitoClientStoreVerifyClientIDCacheExpires(t *testing.T) {
	cfg := testDCRConfig(t)
	cfg.DCRClientCacheTTL = time.Minute
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	fake := &fakeCognitoClient{
		describeOutput: &cognitoidentityprovider.DescribeUserPoolClientOutput{
			UserPoolClient: awsUserPoolClientPtr(testUserPoolClient(cfg, func(client *types.UserPoolClientType) {})),
		},
	}
	store := NewCognitoClientStoreWithClient(cfg, fake)
	store.now = func() time.Time { return now }

	if err := store.VerifyClientID(t.Context(), "generated-client-id"); err != nil {
		t.Fatalf("first VerifyClientID failed: %v", err)
	}
	fake.describeErr = errors.New("unexpected describe")
	if err := store.VerifyClientID(t.Context(), "generated-client-id"); err != nil {
		t.Fatalf("cache hit VerifyClientID failed: %v", err)
	}
	now = now.Add(2 * time.Minute)
	if err := store.VerifyClientID(t.Context(), "generated-client-id"); err == nil {
		t.Fatal("expected expired cache to call DescribeUserPoolClient and fail")
	}
	if fake.describeCalls != 2 {
		t.Fatalf("describe calls=%d want 2", fake.describeCalls)
	}
}

func TestCognitoClientStoreVerifyClientIDAcceptsStaticClientWithoutDescribe(t *testing.T) {
	cfg := testDCRConfig(t)
	fake := &fakeCognitoClient{describeErr: errors.New("should not describe static client")}
	store := NewCognitoClientStoreWithClient(cfg, fake)

	if err := store.VerifyClientID(t.Context(), cfg.ClientID); err != nil {
		t.Fatalf("static client VerifyClientID failed: %v", err)
	}
	if fake.describeCalls != 0 {
		t.Fatalf("describe calls=%d want 0", fake.describeCalls)
	}
}

func testBearerHeader(value string) string {
	return "Bear" + "er " + value
}

func testConfig(t *testing.T, upstream *url.URL) Config {
	t.Helper()
	if upstream == nil {
		upstream = mustURL(t, "http://gongmcp:8080")
	}
	return Config{
		Addr:            "127.0.0.1:0",
		Upstream:        upstream,
		InternalToken:   "internal-upstream-placeholder",
		PublicBaseURL:   "https://mcp.example.test",
		Issuer:          "https://cognito-idp.us-east-1.amazonaws.com/us-east-1_pool",
		JWKSURL:         "https://cognito-idp.us-east-1.amazonaws.com/us-east-1_pool/.well-known/jwks.json",
		ClientID:        "client-id",
		RequiredScope:   "gongmcp/read",
		ScopesSupported: []string{"gongmcp/read"},
		RequiredGroup:   "gongmcp-users",
		GroupClaim:      "cognito:groups",
		AllowedEmails:   csvSet("approved@example.test"),
		AllowedOrigins:  []string{"https://claude.ai"},
		AuthLeeway:      time.Second,
		MaxRequestBytes: 1024 * 1024,
		MaxBearerBytes:  8 << 10,
		UpstreamTimeout: 5 * time.Second,
	}
}

func testDCRConfig(t *testing.T) Config {
	t.Helper()
	cfg := testConfig(t, nil)
	cfg.DCREnabled = true
	cfg.CognitoDomainURL = "https://customer.auth.us-east-1.amazoncognito.com"
	cfg.CognitoUserPoolID = "us-east-1_pool"
	cfg.DCRAllowedRedirectURIs = []string{"https://claude.ai/api/mcp/auth_callback"}
	cfg.DCRAllowedScopes = []string{"openid", "email", "gongmcp/read"}
	cfg.DCRIdentityProviders = []string{"JumpCloud"}
	cfg.DCRClientNamePrefix = "gongmcp-dcr"
	cfg.DCRAccessTokenMinutes = 60
	cfg.DCRClientCacheTTL = time.Minute
	return cfg
}

func testAuthorizer(t *testing.T, cfg Config, key *rsa.PrivateKey) *Authorizer {
	t.Helper()
	return NewAuthorizer(cfg, func(token *jwt.Token) (any, error) {
		if token.Method.Alg() != jwt.SigningMethodRS256.Alg() {
			t.Fatalf("unexpected signing method %s", token.Method.Alg())
		}
		return &key.PublicKey, nil
	})
}

type fakeDCRRegistrar struct {
	clientID string
	seen     DCRClientRegistrationRequest
}

func (f *fakeDCRRegistrar) RegisterClient(_ context.Context, req DCRClientRegistrationRequest) (DCRClientRegistrationResponse, error) {
	f.seen = req
	clientID := f.clientID
	if clientID == "" {
		clientID = "generated-client-id"
	}
	return DCRClientRegistrationResponse{
		ClientID:                clientID,
		ClientIDIssuedAt:        123,
		RedirectURIs:            req.RedirectURIs,
		TokenEndpointAuthMethod: req.TokenEndpointAuthMethod,
		GrantTypes:              req.GrantTypes,
		ResponseTypes:           req.ResponseTypes,
		ClientName:              req.ClientName,
		Scope:                   req.Scope,
	}, nil
}

type fakeClientVerifier struct {
	allowed map[string]struct{}
}

type fakeCognitoClient struct {
	createOutput   *cognitoidentityprovider.CreateUserPoolClientOutput
	createErr      error
	describeOutput *cognitoidentityprovider.DescribeUserPoolClientOutput
	describeErr    error
	describeCalls  int
}

func (f *fakeCognitoClient) CreateUserPoolClient(context.Context, *cognitoidentityprovider.CreateUserPoolClientInput, ...func(*cognitoidentityprovider.Options)) (*cognitoidentityprovider.CreateUserPoolClientOutput, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	if f.createOutput != nil {
		return f.createOutput, nil
	}
	return &cognitoidentityprovider.CreateUserPoolClientOutput{
		UserPoolClient: &types.UserPoolClientType{ClientId: aws.String("generated-client-id")},
	}, nil
}

func (f *fakeCognitoClient) DescribeUserPoolClient(context.Context, *cognitoidentityprovider.DescribeUserPoolClientInput, ...func(*cognitoidentityprovider.Options)) (*cognitoidentityprovider.DescribeUserPoolClientOutput, error) {
	f.describeCalls++
	if f.describeErr != nil {
		return nil, f.describeErr
	}
	if f.describeOutput != nil {
		return f.describeOutput, nil
	}
	return &cognitoidentityprovider.DescribeUserPoolClientOutput{}, nil
}

func testUserPoolClient(cfg Config, mutate func(*types.UserPoolClientType)) types.UserPoolClientType {
	client := types.UserPoolClientType{
		ClientId:                        aws.String("generated-client-id"),
		ClientName:                      aws.String(cfg.DCRClientNamePrefix + "-123-abcdef"),
		AllowedOAuthFlowsUserPoolClient: aws.Bool(true),
		AllowedOAuthFlows:               []types.OAuthFlowType{types.OAuthFlowTypeCode},
		AllowedOAuthScopes:              []string{"openid", "email", cfg.RequiredScope},
		CallbackURLs:                    []string{"https://claude.ai/api/mcp/auth_callback"},
	}
	mutate(&client)
	return client
}

func awsUserPoolClientPtr(client types.UserPoolClientType) *types.UserPoolClientType {
	return &client
}

func (f *fakeClientVerifier) VerifyClientID(_ context.Context, clientID string) error {
	if _, ok := f.allowed[clientID]; ok {
		return nil
	}
	return fmt.Errorf("client_id %q not allowed", clientID)
}

func signToken(t *testing.T, key *rsa.PrivateKey, cfg Config, mutate func(jwt.MapClaims)) string {
	t.Helper()
	claims := baseClaims(cfg)
	mutate(claims)
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func baseClaims(cfg Config) jwt.MapClaims {
	now := time.Now()
	claims := jwt.MapClaims{
		"iss":            cfg.Issuer,
		"sub":            "subject-1",
		"email":          "approved@example.test",
		"client_id":      cfg.ClientID,
		"token_use":      "access",
		"scope":          "openid email " + cfg.RequiredScope,
		"cognito:groups": []string{cfg.RequiredGroup},
		"aud":            cfg.ResourceURL(),
		"exp":            now.Add(time.Hour).Unix(),
		"nbf":            now.Add(-time.Minute).Unix(),
		"iat":            now.Add(-time.Minute).Unix(),
	}
	return claims
}

func mustKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func osWriteFile(path, value string) error {
	return os.WriteFile(path, []byte(value), 0o600)
}

func setLoadConfigBaseEnv(t *testing.T) {
	t.Helper()
	tokenFile := fmt.Sprintf("%s/token", t.TempDir())
	if err := osWriteFile(tokenFile, "internal-upstream-placeholder"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GATEWAY_INTERNAL_BEARER_TOKEN", "")
	t.Setenv("GATEWAY_INTERNAL_BEARER_TOKEN_FILE", tokenFile)
	t.Setenv("PUBLIC_BASE_URL", "https://mcp.example.test")
	t.Setenv("COGNITO_ISSUER_URL", "https://cognito-idp.us-east-1.amazonaws.com/us-east-1_pool")
	t.Setenv("COGNITO_JWKS_URL", "")
	t.Setenv("COGNITO_CLIENT_ID", "client-id")
	t.Setenv("COGNITO_REQUIRED_GROUP", "gongmcp-users")
}
