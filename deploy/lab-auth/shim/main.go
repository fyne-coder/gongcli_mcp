package main

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
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
}

type app struct {
	cfg    config
	jwksMu sync.Mutex
	jwks   map[string]*rsa.PublicKey
}

type claims struct {
	Issuer        string          `json:"iss"`
	Subject       string          `json:"sub"`
	Email         string          `json:"email"`
	PreferredName string          `json:"preferred_username"`
	Authorized    string          `json:"azp"`
	Audience      json.RawMessage `json:"aud"`
	ExpiresAt     int64           `json:"exp"`
	NotBefore     int64           `json:"nbf"`
	Groups        []string        `json:"groups"`
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
	if err := http.ListenAndServe(cfg.addr, mux); err != nil {
		log.Fatal(err)
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
	return config{
		addr:              envDefault("SHIM_ADDR", ":8090"),
		upstream:          upstream,
		internalToken:     token,
		publicBaseURL:     publicBaseURL,
		issuer:            issuer,
		clientID:          clientID,
		requiredGroup:     strings.TrimSpace(os.Getenv("REQUIRED_GROUP")),
		allowedEmails:     csvSet(os.Getenv("ALLOWED_EMAILS")),
		trustProxyHeaders: truthy(os.Getenv("TRUST_PROXY_HEADERS")),
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
	proxy := httputil.NewSingleHostReverseProxy(a.cfg.upstream)
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("upstream error principal=%s err=%v", principal, err)
		http.Error(w, "upstream failed", http.StatusBadGateway)
	}
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.URL.Path = "/mcp"
		req.Host = a.cfg.upstream.Host
		req.Header.Set("Authorization", "Bearer "+a.cfg.internalToken)
		req.Header.Set("X-Gongctl-Lab-Principal", principal)
	}
	log.Printf("auth ok principal=%s method=%s", principal, r.Method)
	proxy.ServeHTTP(w, r)
}

func (a *app) authenticate(r *http.Request) (string, error) {
	if a.cfg.trustProxyHeaders {
		if principal, ok := a.authenticateProxyHeaders(r); ok {
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

func (a *app) authenticateProxyHeaders(r *http.Request) (string, bool) {
	email := firstHeader(r, "X-Auth-Request-Email", "X-Forwarded-Email", "X-Forwarded-User")
	if email == "" {
		return "", false
	}
	groups := splitGroups(firstHeader(r, "X-Auth-Request-Groups", "X-Forwarded-Groups"))
	if err := a.authorize(email, groups); err != nil {
		return "", false
	}
	return email, true
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

func (a *app) verifyJWT(ctx context.Context, token string) (claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return claims{}, errors.New("token is not a JWT")
	}
	headerRaw, err := decodeSegment(parts[0])
	if err != nil {
		return claims{}, err
	}
	var header struct {
		Algorithm string `json:"alg"`
		KeyID     string `json:"kid"`
	}
	if err := json.Unmarshal(headerRaw, &header); err != nil {
		return claims{}, err
	}
	if header.Algorithm != "RS256" || header.KeyID == "" {
		return claims{}, errors.New("unsupported JWT header")
	}
	key, err := a.publicKey(ctx, header.KeyID)
	if err != nil {
		return claims{}, err
	}
	signed := []byte(parts[0] + "." + parts[1])
	digest := sha256.Sum256(signed)
	sig, err := decodeSegment(parts[2])
	if err != nil {
		return claims{}, err
	}
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], sig); err != nil {
		return claims{}, errors.New("invalid token signature")
	}
	claimsRaw, err := decodeSegment(parts[1])
	if err != nil {
		return claims{}, err
	}
	var claim claims
	if err := json.Unmarshal(claimsRaw, &claim); err != nil {
		return claims{}, err
	}
	now := time.Now().Unix()
	if claim.Issuer != a.cfg.issuer {
		return claims{}, fmt.Errorf("issuer mismatch: %s", claim.Issuer)
	}
	if claim.ExpiresAt <= now {
		return claims{}, errors.New("token expired")
	}
	if claim.NotBefore > 0 && claim.NotBefore > now+60 {
		return claims{}, errors.New("token not yet valid")
	}
	if claim.Authorized != a.cfg.clientID && !audienceContains(claim.Audience, a.cfg.clientID) {
		return claims{}, errors.New("token audience/client mismatch")
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

func (a *app) publicKey(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	a.jwksMu.Lock()
	defer a.jwksMu.Unlock()
	if a.jwks != nil {
		if key := a.jwks[kid]; key != nil {
			return key, nil
		}
	}
	keys, err := fetchJWKS(ctx, a.cfg.issuer+"/protocol/openid-connect/certs")
	if err != nil {
		return nil, err
	}
	a.jwks = keys
	if key := a.jwks[kid]; key != nil {
		return key, nil
	}
	return nil, fmt.Errorf("jwks key %q not found", kid)
}

func fetchJWKS(ctx context.Context, endpoint string) (map[string]*rsa.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jwks status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var jwks struct {
		Keys []struct {
			KeyID     string `json:"kid"`
			KeyType   string `json:"kty"`
			Algorithm string `json:"alg"`
			Use       string `json:"use"`
			N         string `json:"n"`
			E         string `json:"e"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(body, &jwks); err != nil {
		return nil, err
	}
	keys := map[string]*rsa.PublicKey{}
	for _, item := range jwks.Keys {
		if item.KeyID == "" || item.KeyType != "RSA" || item.N == "" || item.E == "" {
			continue
		}
		nBytes, err := decodeSegment(item.N)
		if err != nil {
			continue
		}
		eBytes, err := decodeSegment(item.E)
		if err != nil {
			continue
		}
		e := 0
		for _, b := range eBytes {
			e = e<<8 + int(b)
		}
		if e == 0 {
			continue
		}
		keys[item.KeyID] = &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: e}
	}
	if len(keys) == 0 {
		return nil, errors.New("jwks had no RSA keys")
	}
	return keys, nil
}

func decodeSegment(value string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(value)
}

func audienceContains(raw json.RawMessage, want string) bool {
	if len(raw) == 0 {
		return false
	}
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		return one == want
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err != nil {
		return false
	}
	for _, item := range many {
		if item == want {
			return true
		}
	}
	return false
}

func firstHeader(r *http.Request, names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(r.Header.Get(name)); value != "" {
			return value
		}
	}
	return ""
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
