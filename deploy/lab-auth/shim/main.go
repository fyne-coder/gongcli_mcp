package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

type config struct {
	addr              string
	upstream          *url.URL
	internalToken     string
	publicBaseURL     string
	issuer            string
	clientID          string
	requiredGroup     string
	allowedEmails     map[string]struct{}
	trustProxyHeaders bool
	trustedProxyCIDRs []netip.Prefix
}

type app struct {
	cfg config

	verifierMu sync.Mutex
	verifier   *oidc.IDTokenVerifier
}

type claims struct {
	Issuer        string   `json:"iss"`
	Subject       string   `json:"sub"`
	Email         string   `json:"email"`
	PreferredName string   `json:"preferred_username"`
	Groups        []string `json:"groups"`
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	a := &app{cfg: cfg}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", a.healthz)
	mux.HandleFunc("/.well-known/oauth-protected-resource", a.oauthProtectedResource)
	mux.HandleFunc("/.well-known/oauth-protected-resource/mcp", a.oauthProtectedResource)
	mux.HandleFunc("/.well-known/oauth-authorization-server", a.oauthAuthorizationServer)
	mux.HandleFunc("/mcp", a.mcp)
	log.Printf("lab auth shim listening addr=%s upstream=%s issuer=%s", cfg.addr, cfg.upstream, cfg.issuer)
	if err := newHTTPServer(cfg.addr, mux).ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       20 * time.Second,
		WriteTimeout:      90 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
}

func loadConfig() (config, error) {
	upstreamRaw := envDefault("UPSTREAM_URL", "http://gongmcp:8080")
	upstream, err := url.Parse(upstreamRaw)
	if err != nil {
		return config{}, err
	}
	tokenFile := strings.TrimSpace(os.Getenv("INTERNAL_BEARER_TOKEN_FILE"))
	if tokenFile == "" {
		return config{}, errors.New("INTERNAL_BEARER_TOKEN_FILE is required")
	}
	tokenRaw, err := os.ReadFile(tokenFile)
	if err != nil {
		return config{}, fmt.Errorf("read internal bearer token: %w", err)
	}
	token := strings.TrimSpace(string(tokenRaw))
	if token == "" {
		return config{}, errors.New("internal bearer token is empty")
	}
	issuer := strings.TrimRight(strings.TrimSpace(os.Getenv("OIDC_ISSUER_URL")), "/")
	if issuer == "" {
		return config{}, errors.New("OIDC_ISSUER_URL is required")
	}
	publicBaseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("PUBLIC_BASE_URL")), "/")
	if publicBaseURL == "" {
		publicBaseURL = issuer
		if idx := strings.Index(publicBaseURL, "/realms/"); idx >= 0 {
			publicBaseURL = publicBaseURL[:idx]
		}
	}
	clientID := strings.TrimSpace(os.Getenv("OIDC_CLIENT_ID"))
	if clientID == "" {
		return config{}, errors.New("OIDC_CLIENT_ID is required")
	}
	trustProxyHeaders := truthy(os.Getenv("TRUST_PROXY_HEADERS"))
	trustedProxyCIDRs, err := parseCIDRList(os.Getenv("TRUST_PROXY_CIDRS"))
	if err != nil {
		return config{}, fmt.Errorf("TRUST_PROXY_CIDRS: %w", err)
	}
	if trustProxyHeaders && len(trustedProxyCIDRs) == 0 {
		return config{}, errors.New("TRUST_PROXY_CIDRS is required when TRUST_PROXY_HEADERS is enabled")
	}
	return config{
		addr:              envDefault("SHIM_ADDR", ":8090"),
		upstream:          upstream,
		internalToken:     token,
		publicBaseURL:     publicBaseURL,
		issuer:            issuer,
		clientID:          clientID,
		requiredGroup:     strings.TrimSpace(os.Getenv("REQUIRED_GROUP")),
		allowedEmails:     csvSet(os.Getenv("ALLOWED_EMAILS")),
		trustProxyHeaders: trustProxyHeaders,
		trustedProxyCIDRs: trustedProxyCIDRs,
	}, nil
}

func (a *app) oauthProtectedResource(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, map[string]any{
		"resource":                 a.resourceURL(),
		"authorization_servers":    []string{a.cfg.issuer},
		"scopes_supported":         []string{"openid", "profile", "email", "offline_access"},
		"bearer_methods_supported": []string{"header"},
		"resource_documentation":   a.cfg.publicBaseURL + "/healthz",
		"audiences_supported":      []string{a.cfg.clientID},
	})
}

func (a *app) oauthAuthorizationServer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	http.Redirect(w, r, a.cfg.issuer+"/.well-known/openid-configuration", http.StatusFound)
}

func (a *app) healthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	proxy := httputil.NewSingleHostReverseProxy(a.cfg.upstream)
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, "upstream health failed", http.StatusBadGateway)
	}
	clone := r.Clone(r.Context())
	clone.URL.Path = "/healthz"
	clone.URL.RawQuery = ""
	proxy.ServeHTTP(w, clone)
}

func (a *app) mcp(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/mcp" {
		http.NotFound(w, r)
		return
	}
	principal, err := a.authenticate(r)
	if err != nil {
		log.Printf("auth denied remote=%s reason=%v", r.RemoteAddr, err)
		w.Header().Set("WWW-Authenticate", a.wwwAuthenticateHeader())
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(req *httputil.ProxyRequest) {
			req.SetURL(a.cfg.upstream)
			req.Out.URL.Path = "/mcp"
			req.Out.URL.RawQuery = req.In.URL.RawQuery
			req.Out.Host = a.cfg.upstream.Host
			req.Out.Header.Set("Authorization", "Bearer "+a.cfg.internalToken)
			req.Out.Header.Set("X-Gongctl-Lab-Principal", principal)
			req.SetXForwarded()
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("upstream error principal=%s err=%v", principal, err)
			http.Error(w, "upstream failed", http.StatusBadGateway)
		},
	}
	log.Printf("auth ok principal=%s method=%s", principal, r.Method)
	proxy.ServeHTTP(w, r)
}

func (a *app) authenticate(r *http.Request) (string, error) {
	if a.cfg.trustProxyHeaders {
		if hasProxyIdentityHeader(r) {
			if !remoteAddrAllowed(r.RemoteAddr, a.cfg.trustedProxyCIDRs) {
				return "", fmt.Errorf("trusted proxy header from untrusted remote %q", r.RemoteAddr)
			}
		}
		if principal, ok, err := a.authenticateProxyHeaders(r); ok {
			if err != nil {
				return "", err
			}
			return principal, nil
		}
	}
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		return "", errors.New("missing bearer token or trusted proxy headers")
	}
	claim, err := a.verifyJWT(r.Context(), strings.TrimSpace(auth[len("Bearer "):]))
	if err != nil {
		return "", err
	}
	if err := a.authorize(claim.Email, claim.Groups); err != nil {
		return "", err
	}
	if claim.Email != "" {
		return claim.Email, nil
	}
	return claim.Subject, nil
}

func (a *app) authenticateProxyHeaders(r *http.Request) (string, bool, error) {
	email := firstHeader(r, "X-Auth-Request-Email", "X-Forwarded-Email", "X-Forwarded-User")
	if email == "" {
		return "", false, nil
	}
	groups := splitGroups(firstHeader(r, "X-Auth-Request-Groups", "X-Forwarded-Groups"))
	if err := a.authorize(email, groups); err != nil {
		return "", true, err
	}
	return email, true, nil
}

func (a *app) authorize(email string, groups []string) error {
	if len(a.cfg.allowedEmails) > 0 {
		if _, ok := a.cfg.allowedEmails[strings.ToLower(email)]; !ok {
			return fmt.Errorf("email %q is not allowed", email)
		}
	}
	if a.cfg.requiredGroup != "" {
		for _, group := range groups {
			if group == a.cfg.requiredGroup || strings.TrimPrefix(group, "/") == strings.TrimPrefix(a.cfg.requiredGroup, "/") {
				return nil
			}
		}
		return fmt.Errorf("required group %q missing", a.cfg.requiredGroup)
	}
	return nil
}

func (a *app) tokenVerifier(ctx context.Context) (*oidc.IDTokenVerifier, error) {
	a.verifierMu.Lock()
	defer a.verifierMu.Unlock()
	if a.verifier != nil {
		return a.verifier, nil
	}
	provider, err := oidc.NewProvider(oidc.ClientContext(ctx, http.DefaultClient), a.cfg.issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc provider discovery: %w", err)
	}
	a.verifier = provider.Verifier(&oidc.Config{ClientID: a.cfg.clientID})
	return a.verifier, nil
}

func (a *app) verifyJWT(ctx context.Context, token string) (claims, error) {
	verifier, err := a.tokenVerifier(ctx)
	if err != nil {
		return claims{}, err
	}
	idToken, err := verifier.Verify(ctx, token)
	if err != nil {
		return claims{}, err
	}
	var claim claims
	if err := idToken.Claims(&claim); err != nil {
		return claims{}, err
	}
	return claim, nil
}

func (a *app) resourceURL() string {
	return a.cfg.publicBaseURL + "/mcp"
}

func (a *app) resourceMetadataURL() string {
	return a.cfg.publicBaseURL + "/.well-known/oauth-protected-resource"
}

func (a *app) wwwAuthenticateHeader() string {
	return fmt.Sprintf(`Bearer realm="gongctl-lab", resource_metadata="%s", authorization_uri="%s", scope="openid profile email offline_access"`, a.resourceMetadataURL(), a.cfg.issuer)
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("write json failed: %v", err)
	}
}

func firstHeader(r *http.Request, names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(r.Header.Get(name)); value != "" {
			return value
		}
	}
	return ""
}

func hasProxyIdentityHeader(r *http.Request) bool {
	return firstHeader(r,
		"X-Auth-Request-Email",
		"X-Forwarded-Email",
		"X-Forwarded-User",
	) != ""
}

func parseCIDRList(value string) ([]netip.Prefix, error) {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == ' '
	})
	out := make([]netip.Prefix, 0, len(fields))
	for _, field := range fields {
		trimmed := strings.TrimSpace(field)
		if trimmed == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(trimmed)
		if err != nil {
			return nil, err
		}
		out = append(out, prefix)
	}
	return out, nil
}

func remoteAddrAllowed(remoteAddr string, allowed []netip.Prefix) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	addr, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return false
	}
	for _, prefix := range allowed {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func splitGroups(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == ' '
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if trimmed := strings.TrimSpace(field); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func csvSet(value string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, field := range strings.Split(value, ",") {
		if trimmed := strings.ToLower(strings.TrimSpace(field)); trimmed != "" {
			out[trimmed] = struct{}{}
		}
	}
	return out
}

func envDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func truthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}
