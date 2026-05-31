package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"strings"
	"time"
)

type Server struct {
	cfg        Config
	authorizer *Authorizer
	registrar  DCRRegistrar
}

func NewServer(cfg Config, authorizer *Authorizer) *Server {
	return &Server{cfg: cfg, authorizer: authorizer}
}

func NewServerWithDCR(cfg Config, authorizer *Authorizer, registrar DCRRegistrar) *Server {
	return &Server{cfg: cfg, authorizer: authorizer, registrar: registrar}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.healthz)
	mux.HandleFunc("/.well-known/oauth-protected-resource", s.oauthProtectedResource)
	mux.HandleFunc("/.well-known/oauth-protected-resource/mcp", s.oauthProtectedResource)
	if s.cfg.DCREnabled {
		mux.HandleFunc("/.well-known/oauth-authorization-server", s.oauthAuthorizationServer)
		mux.HandleFunc("/.well-known/openid-configuration", s.oauthAuthorizationServer)
		mux.HandleFunc("/register", s.registerClient)
	}
	mux.HandleFunc("/mcp", s.mcp)
	return mux
}

func NewHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) oauthProtectedResource(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, map[string]any{
		"resource":                 s.cfg.ResourceURL(),
		"authorization_servers":    []string{s.cfg.AuthorizationServerURL()},
		"scopes_supported":         s.cfg.ScopesSupported,
		"bearer_methods_supported": []string{"header"},
	})
}

func (s *Server) oauthAuthorizationServer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, map[string]any{
		"issuer":                                s.cfg.PublicBaseURL,
		"authorization_endpoint":                s.cfg.CognitoDomainURL + "/oauth2/authorize",
		"token_endpoint":                        s.cfg.CognitoDomainURL + "/oauth2/token",
		"registration_endpoint":                 s.cfg.PublicBaseURL + "/register",
		"jwks_uri":                              s.cfg.JWKSURL,
		"scopes_supported":                      s.cfg.DCRAllowedScopes,
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code"},
		"token_endpoint_auth_methods_supported": []string{"none"},
		"code_challenge_methods_supported":      []string{"S256"},
	})
}

func (s *Server) registerClient(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.registrar == nil {
		dcrError(w, http.StatusServiceUnavailable, "temporarily_unavailable", "dynamic client registration is not configured")
		return
	}
	if r.ContentLength > 64<<10 {
		dcrError(w, http.StatusRequestEntityTooLarge, "invalid_client_metadata", "registration request body is too large")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	defer r.Body.Close()

	var req DCRClientRegistrationRequest
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&req); err != nil {
		dcrError(w, http.StatusBadRequest, "invalid_client_metadata", "registration request must be valid JSON")
		return
	}
	var extra any
	if err := dec.Decode(&extra); err == nil || !errors.Is(err, io.EOF) {
		dcrError(w, http.StatusBadRequest, "invalid_client_metadata", "registration request must contain one JSON object")
		return
	}
	normalized, err := s.validateDCRRequest(req)
	if err != nil {
		dcrError(w, http.StatusBadRequest, "invalid_client_metadata", err.Error())
		return
	}
	resp, err := s.registrar.RegisterClient(r.Context(), normalized)
	if err != nil {
		log.Printf("gateway dcr registration failed remote=%s err=%v", r.RemoteAddr, err)
		dcrError(w, http.StatusBadGateway, "server_error", "client registration failed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("gateway write dcr json failed: %v", err)
	}
}

func (s *Server) validateDCRRequest(req DCRClientRegistrationRequest) (DCRClientRegistrationRequest, error) {
	if len(req.RedirectURIs) == 0 {
		return DCRClientRegistrationRequest{}, errors.New("redirect_uris is required")
	}
	for _, redirectURI := range req.RedirectURIs {
		if !contains(s.cfg.DCRAllowedRedirectURIs, redirectURI) {
			return DCRClientRegistrationRequest{}, fmt.Errorf("redirect_uri %q is not allowed", redirectURI)
		}
	}
	authMethod := strings.TrimSpace(req.TokenEndpointAuthMethod)
	if authMethod == "" {
		authMethod = "none"
	}
	if authMethod != "none" {
		return DCRClientRegistrationRequest{}, errors.New("only token_endpoint_auth_method none is supported")
	}
	grantTypes := req.GrantTypes
	if len(grantTypes) == 0 {
		grantTypes = []string{"authorization_code"}
	}
	if len(grantTypes) != 1 || grantTypes[0] != "authorization_code" {
		return DCRClientRegistrationRequest{}, errors.New("only authorization_code grant is supported")
	}
	responseTypes := req.ResponseTypes
	if len(responseTypes) == 0 {
		responseTypes = []string{"code"}
	}
	if len(responseTypes) != 1 || responseTypes[0] != "code" {
		return DCRClientRegistrationRequest{}, errors.New("only code response_type is supported")
	}
	scopes := strings.Fields(req.Scope)
	if len(scopes) == 0 {
		scopes = append([]string(nil), s.cfg.DCRAllowedScopes...)
	}
	for _, scope := range scopes {
		if !contains(s.cfg.DCRAllowedScopes, scope) {
			return DCRClientRegistrationRequest{}, fmt.Errorf("scope %q is not allowed", scope)
		}
	}
	if !contains(scopes, s.cfg.RequiredScope) {
		return DCRClientRegistrationRequest{}, fmt.Errorf("required scope %q missing", s.cfg.RequiredScope)
	}
	return DCRClientRegistrationRequest{
		RedirectURIs:            append([]string(nil), req.RedirectURIs...),
		TokenEndpointAuthMethod: authMethod,
		GrantTypes:              grantTypes,
		ResponseTypes:           responseTypes,
		ClientName:              req.ClientName,
		Scope:                   strings.Join(scopes, " "),
	}, nil
}

func (s *Server) mcp(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/mcp" {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodOptions {
		s.writeCORSPreflight(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPost && r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.ContentLength > s.cfg.MaxRequestBytes {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}
	if !s.writeCORSHeaders(w, r) && r.Header.Get("Origin") != "" {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}
	if r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxRequestBytes)
	}
	principal, err := s.authorizer.Authenticate(r.Context(), r.Header.Get("Authorization"))
	if err != nil {
		log.Printf("gateway auth denied remote=%s reason=%v", r.RemoteAddr, err)
		w.Header().Set("WWW-Authenticate", s.cfg.WWWAuthenticateChallenge())
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	s.proxyMCP(w, r, principal)
}

func (s *Server) writeCORSPreflight(w http.ResponseWriter, r *http.Request) {
	if !s.writeCORSHeaders(w, r) && r.Header.Get("Origin") != "" {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, MCP-Protocol-Version, Mcp-Session-Id")
	w.Header().Set("Access-Control-Max-Age", "600")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) writeCORSHeaders(w http.ResponseWriter, r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	for _, allowed := range s.cfg.AllowedOrigins {
		if origin == allowed {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Add("Vary", "Origin")
			return true
		}
	}
	return false
}

func (s *Server) proxyMCP(w http.ResponseWriter, r *http.Request, principal Principal) {
	proxy := &httputil.ReverseProxy{
		Rewrite: func(req *httputil.ProxyRequest) {
			req.SetURL(s.cfg.Upstream)
			req.Out.URL.Path = "/mcp"
			req.Out.URL.RawQuery = req.In.URL.RawQuery
			req.Out.Host = s.cfg.Upstream.Host
			req.Out.Header = allowedUpstreamHeaders(req.In.Header)
			req.Out.Header.Set("Authorization", "Bearer "+s.cfg.InternalToken)
			req.Out.Header.Set("X-Gongctl-Principal", principalLabel(principal))
			// Set forwarded headers only after the external header allowlist strips client-supplied values.
			req.SetXForwarded()
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("gateway upstream failed principal=%s err=%v", principalLabel(principal), err)
			http.Error(w, "upstream failed", http.StatusBadGateway)
		},
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.UpstreamTimeout)
	defer cancel()
	proxy.ServeHTTP(w, r.WithContext(ctx))
}

func allowedUpstreamHeaders(in http.Header) http.Header {
	out := http.Header{}
	for _, name := range []string{
		"Accept",
		"Accept-Encoding",
		"Content-Type",
		"MCP-Protocol-Version",
		"Mcp-Session-Id",
		"User-Agent",
	} {
		for _, value := range in.Values(name) {
			out.Add(name, value)
		}
	}
	return out
}

func principalLabel(principal Principal) string {
	if principal.Email != "" {
		return principal.Email
	}
	if principal.Subject != "" {
		return principal.Subject
	}
	return "unknown"
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("gateway write json failed: %v", err)
	}
}

func (s *Server) LogConfig() string {
	return fmt.Sprintf("addr=%s upstream=%s auth_profile=%s issuer=%s client_id=%s required_scope=%s required_group=%s group_claim=%s allowed_subjects=%d allowed_emails=%d dcr_enabled=%t dcr_redirect_uris=%d dcr_scopes=%d dcr_identity_providers=%d",
		s.cfg.Addr,
		s.cfg.Upstream.Redacted(),
		s.cfg.AuthProfile,
		s.cfg.Issuer,
		s.cfg.ClientID,
		s.cfg.RequiredScope,
		s.cfg.RequiredGroupLogValue(),
		s.cfg.GroupClaim,
		len(s.cfg.AllowedSubjects),
		len(s.cfg.AllowedEmails),
		s.cfg.DCREnabled,
		len(s.cfg.DCRAllowedRedirectURIs),
		len(s.cfg.DCRAllowedScopes),
		len(s.cfg.DCRIdentityProviders),
	)
}

func IsBearerToken(value string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(value)), "bearer ")
}
