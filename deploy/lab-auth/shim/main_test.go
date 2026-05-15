package main

import (
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
