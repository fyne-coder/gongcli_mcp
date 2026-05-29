package main

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestNewHTTPServerSetsTimeouts(t *testing.T) {
	server := newHTTPServer("127.0.0.1:0", http.NewServeMux())
	if server.ReadHeaderTimeout != 10*time.Second {
		t.Fatalf("ReadHeaderTimeout=%s want 10s", server.ReadHeaderTimeout)
	}
	if server.ReadTimeout != 20*time.Second {
		t.Fatalf("ReadTimeout=%s want 20s", server.ReadTimeout)
	}
	if server.WriteTimeout != 90*time.Second {
		t.Fatalf("WriteTimeout=%s want 90s", server.WriteTimeout)
	}
	if server.IdleTimeout != 120*time.Second {
		t.Fatalf("IdleTimeout=%s want 120s", server.IdleTimeout)
	}
}

func TestMCPRewritePreservesInternalBearerAgainstHopByHopHeader(t *testing.T) {
	var gotAuth, gotPrincipal, gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPrincipal = r.Header.Get("X-Gongctl-Lab-Principal")
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)
	upstreamURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	app := &app{cfg: config{
		upstream:          upstreamURL,
		internalToken:     "internal-token-0123456789abcdef",
		allowedEmails:     csvSet("approved@example.test"),
		trustProxyHeaders: true,
		trustedProxyCIDRs: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")},
	}}
	req := httptest.NewRequest(http.MethodPost, "http://example.test/mcp", strings.NewReader("{}"))
	req.RemoteAddr = "127.0.0.1:54321"
	req.Header.Set("X-Auth-Request-Email", "approved@example.test")
	req.Header.Set("Connection", "Authorization")
	req.Header.Set("Authorization", "Bearer attacker-controlled")
	recorder := httptest.NewRecorder()

	app.mcp(recorder, req)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status=%d want %d body=%q", recorder.Code, http.StatusNoContent, recorder.Body.String())
	}
	if gotPath != "/mcp" {
		t.Fatalf("upstream path=%q want /mcp", gotPath)
	}
	if gotAuth != "Bearer internal-token-0123456789abcdef" {
		t.Fatalf("upstream auth=%q", gotAuth)
	}
	if gotPrincipal != "approved@example.test" {
		t.Fatalf("upstream principal=%q", gotPrincipal)
	}
}

func TestAuthenticateRejectsForgedProxyHeaderFromUntrustedRemote(t *testing.T) {
	app := &app{cfg: config{
		allowedEmails:     csvSet("approved@example.test"),
		trustProxyHeaders: true,
		trustedProxyCIDRs: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")},
	}}
	req, err := http.NewRequest(http.MethodPost, "http://example.test/mcp", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.RemoteAddr = "203.0.113.10:54321"
	req.Header.Set("X-Auth-Request-Email", "approved@example.test")

	principal, err := app.authenticate(req)
	if err == nil {
		t.Fatalf("expected forged proxy header to be rejected, got principal %q", principal)
	}
	if want := "trusted proxy header from untrusted remote"; !strings.Contains(err.Error(), want) {
		t.Fatalf("expected untrusted remote error, got %v", err)
	}
}

func TestAuthenticateAcceptsProxyHeaderFromTrustedRemote(t *testing.T) {
	app := &app{cfg: config{
		allowedEmails:     csvSet("approved@example.test"),
		trustProxyHeaders: true,
		trustedProxyCIDRs: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")},
	}}
	req, err := http.NewRequest(http.MethodPost, "http://example.test/mcp", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.RemoteAddr = "127.0.0.1:54321"
	req.Header.Set("X-Auth-Request-Email", "approved@example.test")

	principal, err := app.authenticate(req)
	if err != nil {
		t.Fatalf("expected trusted proxy header to authenticate, got %v", err)
	}
	if principal != "approved@example.test" {
		t.Fatalf("unexpected principal %q", principal)
	}
}

func TestAuthenticateIgnoresProxyHeaderWhenTrustDisabled(t *testing.T) {
	app := &app{cfg: config{
		allowedEmails: csvSet("approved@example.test"),
	}}
	req, err := http.NewRequest(http.MethodPost, "http://example.test/mcp", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.RemoteAddr = "127.0.0.1:54321"
	req.Header.Set("X-Auth-Request-Email", "approved@example.test")

	principal, err := app.authenticate(req)
	if err == nil {
		t.Fatalf("expected proxy header to be ignored when trust is disabled, got principal %q", principal)
	}
	if want := "missing bearer token"; !strings.Contains(err.Error(), want) {
		t.Fatalf("expected missing bearer error, got %v", err)
	}
}

func TestLoadConfigRequiresTrustedProxyCIDRWhenProxyHeadersEnabled(t *testing.T) {
	tokenFile := t.TempDir() + "/token"
	if err := os.WriteFile(tokenFile, []byte("internal-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("UPSTREAM_URL", "http://gongmcp:8080")
	t.Setenv("INTERNAL_BEARER_TOKEN_FILE", tokenFile)
	t.Setenv("OIDC_ISSUER_URL", "https://issuer.example.test")
	t.Setenv("OIDC_CLIENT_ID", "client")
	t.Setenv("TRUST_PROXY_HEADERS", "1")

	_, err := loadConfig()
	if err == nil {
		t.Fatal("expected missing TRUST_PROXY_CIDRS to fail closed")
	}
	if want := "TRUST_PROXY_CIDRS is required"; !strings.Contains(err.Error(), want) {
		t.Fatalf("expected missing CIDR error, got %v", err)
	}
}

func TestOAuthProtectedResourceAdvertisesConfiguredAudience(t *testing.T) {
	app := &app{cfg: config{
		publicBaseURL: "https://mcp.example.test",
		issuer:        "https://issuer.example.test/realms/gong-lab",
		clientID:      "gong-lab-proxy",
	}}
	req := httptest.NewRequest(http.MethodGet, "http://example.test/.well-known/oauth-protected-resource", nil)
	recorder := httptest.NewRecorder()

	app.oauthProtectedResource(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	var metadata struct {
		AudiencesSupported []string `json:"audiences_supported"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &metadata); err != nil {
		t.Fatal(err)
	}
	if len(metadata.AudiencesSupported) != 1 || metadata.AudiencesSupported[0] != app.cfg.clientID {
		t.Fatalf("audiences_supported=%v want [%q]", metadata.AudiencesSupported, app.cfg.clientID)
	}
}

func TestRemoteAddrAllowedParsesIPv4AndIPv6(t *testing.T) {
	allowed := []netip.Prefix{
		netip.MustParsePrefix("127.0.0.0/8"),
		netip.MustParsePrefix("::1/128"),
	}
	if !remoteAddrAllowed("127.0.0.1:1234", allowed) {
		t.Fatal("expected IPv4 loopback with port to be allowed")
	}
	if !remoteAddrAllowed("[::1]:1234", allowed) {
		t.Fatal("expected IPv6 loopback with port to be allowed")
	}
	if remoteAddrAllowed("203.0.113.10:1234", allowed) {
		t.Fatal("expected public test address to be denied")
	}
}

func TestVerifyJWTUsesOIDCDiscoveryJWKSURIAndStrictAudience(t *testing.T) {
	fixture := newOIDCFixture(t)
	app := &app{cfg: config{
		issuer:   fixture.issuer,
		clientID: "gong-lab-proxy",
	}}
	token := fixture.token(t, map[string]any{
		"sub":    "user-1",
		"email":  "approved@example.test",
		"groups": []string{"/gong-mcp-users"},
		"aud":    []string{"gong-lab-proxy"},
		"exp":    time.Now().Add(time.Hour).Unix(),
	})

	claim, err := app.verifyJWT(context.Background(), token)
	if err != nil {
		t.Fatalf("verifyJWT returned error: %v", err)
	}
	if claim.Email != "approved@example.test" {
		t.Fatalf("email=%q", claim.Email)
	}
	if fixture.discoveryHits == 0 {
		t.Fatal("expected OIDC discovery endpoint to be used")
	}
	if fixture.jwksHits == 0 {
		t.Fatal("expected discovered jwks_uri to be used")
	}

	if _, err := app.verifyJWT(context.Background(), token); err != nil {
		t.Fatalf("second verifyJWT returned error: %v", err)
	}
	if fixture.discoveryHits != 1 {
		t.Fatalf("discovery hits=%d want 1 cached verifier", fixture.discoveryHits)
	}
}

func TestVerifyJWTRejectsInvalidTokens(t *testing.T) {
	fixture := newOIDCFixture(t)
	validClaims := func() map[string]any {
		return map[string]any{
			"sub":    "user-1",
			"email":  "approved@example.test",
			"groups": []string{"/gong-mcp-users"},
			"aud":    []string{"gong-lab-proxy"},
			"exp":    time.Now().Add(time.Hour).Unix(),
		}
	}
	tests := []struct {
		name  string
		token string
	}{
		{
			name: "wrong issuer",
			token: fixture.token(t, withClaim(validClaims(),
				"iss", "https://issuer.example.invalid",
			)),
		},
		{
			name: "wrong audience",
			token: fixture.token(t, withClaim(validClaims(),
				"aud", []string{"other-client"},
			)),
		},
		{
			name: "azp without audience",
			token: fixture.token(t, withoutClaim(withClaim(validClaims(),
				"azp", "gong-lab-proxy",
			), "aud")),
		},
		{
			name: "azp with wrong audience",
			token: fixture.token(t, withClaim(validClaims(),
				"azp", "gong-lab-proxy",
				"aud", []string{"other-client"},
			)),
		},
		{
			name: "expired",
			token: fixture.token(t, withClaim(validClaims(),
				"exp", time.Now().Add(-time.Hour).Unix(),
			)),
		},
		{
			name:  "alg none",
			token: fixture.unsignedToken(t, validClaims()),
		},
		{
			name:  "wrong kid",
			token: fixture.tokenWithKid(t, "missing-key", validClaims()),
		},
		{
			name:  "malformed jwt",
			token: "not-a-jwt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := &app{cfg: config{
				issuer:   fixture.issuer,
				clientID: "gong-lab-proxy",
			}}
			if _, err := app.verifyJWT(context.Background(), tt.token); err == nil {
				t.Fatal("expected token to be rejected")
			}
		})
	}
}

func TestAuthenticateRejectsVerifiedTokenMissingRequiredGroup(t *testing.T) {
	fixture := newOIDCFixture(t)
	app := &app{cfg: config{
		issuer:        fixture.issuer,
		clientID:      "gong-lab-proxy",
		requiredGroup: "/gong-mcp-users",
		allowedEmails: csvSet("approved@example.test"),
	}}
	token := fixture.token(t, map[string]any{
		"sub":    "user-1",
		"email":  "approved@example.test",
		"groups": []string{"/other-group"},
		"aud":    []string{"gong-lab-proxy"},
		"exp":    time.Now().Add(time.Hour).Unix(),
	})
	req := httptest.NewRequest(http.MethodPost, "http://example.test/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	principal, err := app.authenticate(req)
	if err == nil {
		t.Fatalf("expected missing group to be rejected, got principal %q", principal)
	}
	if want := "required group"; !strings.Contains(err.Error(), want) {
		t.Fatalf("expected required group error, got %v", err)
	}
}

func TestAuthenticateRejectsVerifiedTokenBlockedEmail(t *testing.T) {
	fixture := newOIDCFixture(t)
	app := &app{cfg: config{
		issuer:        fixture.issuer,
		clientID:      "gong-lab-proxy",
		requiredGroup: "/gong-mcp-users",
		allowedEmails: csvSet("approved@example.test"),
	}}
	token := fixture.token(t, map[string]any{
		"sub":    "user-1",
		"email":  "blocked@example.test",
		"groups": []string{"/gong-mcp-users"},
		"aud":    []string{"gong-lab-proxy"},
		"exp":    time.Now().Add(time.Hour).Unix(),
	})
	req := httptest.NewRequest(http.MethodPost, "http://example.test/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	principal, err := app.authenticate(req)
	if err == nil {
		t.Fatalf("expected blocked email to be rejected, got principal %q", principal)
	}
	if want := "is not allowed"; !strings.Contains(err.Error(), want) {
		t.Fatalf("expected allowlist error, got %v", err)
	}
}

type oidcFixture struct {
	issuer        string
	privateKey    *rsa.PrivateKey
	kid           string
	discoveryHits int
	jwksHits      int
}

func newOIDCFixture(t *testing.T) *oidcFixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &oidcFixture{
		privateKey: key,
		kid:        "test-key-1",
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			fixture.discoveryHits++
			writeTestJSON(t, w, map[string]any{
				"issuer":                 fixture.issuer,
				"jwks_uri":               fixture.issuer + "/discovered-jwks",
				"authorization_endpoint": fixture.issuer + "/auth",
				"token_endpoint":         fixture.issuer + "/token",
			})
		case "/discovered-jwks":
			fixture.jwksHits++
			writeTestJSON(t, w, map[string]any{
				"keys": []map[string]any{{
					"kty": "RSA",
					"use": "sig",
					"kid": fixture.kid,
					"alg": "RS256",
					"n":   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
					"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	fixture.issuer = server.URL
	return fixture
}

func (f *oidcFixture) token(t *testing.T, claims map[string]any) string {
	t.Helper()
	return f.tokenWithKid(t, f.kid, claims)
}

func (f *oidcFixture) tokenWithKid(t *testing.T, kid string, claims map[string]any) string {
	t.Helper()
	return f.sign(t, map[string]any{"alg": "RS256", "kid": kid, "typ": "JWT"}, claims)
}

func (f *oidcFixture) unsignedToken(t *testing.T, claims map[string]any) string {
	t.Helper()
	header := encodeTestJSON(t, map[string]any{"alg": "none", "typ": "JWT"})
	payload := encodeTestJSON(t, withDefaultClaim(claims, "iss", f.issuer))
	return header + "." + payload + "."
}

func (f *oidcFixture) sign(t *testing.T, header map[string]any, claims map[string]any) string {
	t.Helper()
	headerSegment := encodeTestJSON(t, header)
	payloadSegment := encodeTestJSON(t, withDefaultClaim(claims, "iss", f.issuer))
	signed := headerSegment + "." + payloadSegment
	digest := sha256.Sum256([]byte(signed))
	signature, err := rsa.SignPKCS1v15(rand.Reader, f.privateKey, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return signed + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func withClaim(claims map[string]any, kv ...any) map[string]any {
	out := map[string]any{}
	for key, value := range claims {
		out[key] = value
	}
	for i := 0; i < len(kv); i += 2 {
		out[kv[i].(string)] = kv[i+1]
	}
	return out
}

func withDefaultClaim(claims map[string]any, key string, value any) map[string]any {
	out := withClaim(claims)
	if _, ok := out[key]; !ok {
		out[key] = value
	}
	return out
}

func withoutClaim(claims map[string]any, keys ...string) map[string]any {
	out := map[string]any{}
	for key, value := range claims {
		out[key] = value
	}
	for _, key := range keys {
		delete(out, key)
	}
	return out
}

func encodeTestJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func writeTestJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatal(err)
	}
}
