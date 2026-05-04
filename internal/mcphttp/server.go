package mcphttp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/fyne-coder/gongcli_mcp/internal/mcp"
	"github.com/fyne-coder/gongcli_mcp/internal/version"
)

type GetenvFunc func(string) string

const minBearerTokenBytes = 32

type ConfigSelection struct {
	AddrFlag                string
	ForceStdio              bool
	AuthModeFlag            string
	TokenFlag               string
	TokenFileFlag           string
	PreviousTokenFileFlag   string
	AllowOpenNetworkFlag    bool
	DevAllowNoAuthLocalhost bool
	AllowedOriginsFlag      string
	Allowlist               []string
	Getenv                  GetenvFunc
}

type Config struct {
	Enabled            bool
	Addr               string
	AuthMode           string
	BearerTokens       []string
	OpenNetworkWarning bool
	LocalBind          bool
	AllowedOrigins     map[string]struct{}
}

func ResolveConfig(selection ConfigSelection) (Config, error) {
	getenv := selection.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	if selection.ForceStdio {
		if strings.TrimSpace(selection.AddrFlag) != "" {
			return Config{}, fmt.Errorf("--stdio cannot be combined with --http")
		}
		return Config{}, nil
	}

	addr := firstNonEmpty(selection.AddrFlag, getenv("GONGMCP_HTTP_ADDR"))
	if addr == "" {
		return Config{}, nil
	}
	if len(selection.Allowlist) == 0 {
		return Config{}, fmt.Errorf("HTTP mode requires --tool-preset, --tool-allowlist, GONGMCP_TOOL_PRESET, or GONGMCP_TOOL_ALLOWLIST")
	}

	authModeSource := firstNonEmpty(selection.AuthModeFlag, getenv("GONGMCP_AUTH_MODE"))
	authMode := strings.ToLower(authModeSource)
	localBind := isLocalHTTPAddr(addr)
	if authMode == "" {
		if bearerTokenSourceConfigured(selection.TokenFlag, selection.TokenFileFlag, getenv) {
			authMode = "bearer"
		} else if selection.DevAllowNoAuthLocalhost && localBind {
			authMode = "none"
		} else {
			authMode = "bearer"
		}
	}
	if authMode != "none" && authMode != "bearer" {
		return Config{}, fmt.Errorf("auth-mode must be none or bearer")
	}
	if authMode == "none" {
		if !selection.DevAllowNoAuthLocalhost {
			return Config{}, fmt.Errorf("auth-mode=none requires --dev-allow-no-auth-localhost")
		}
		if !localBind {
			return Config{}, fmt.Errorf("auth-mode=none is only allowed for localhost development")
		}
	}

	allowOpenNetwork := selection.AllowOpenNetworkFlag || truthy(getenv("GONGMCP_ALLOW_OPEN_NETWORK"))
	if !localBind {
		if !allowOpenNetwork {
			return Config{}, fmt.Errorf("non-local HTTP address %q requires --allow-open-network and TLS termination at a trusted proxy or gateway", addr)
		}
	}
	allowedOrigins, err := parseAllowedOrigins(selection.AllowedOriginsFlag, getenv("GONGMCP_ALLOWED_ORIGINS"))
	if err != nil {
		return Config{}, err
	}
	if !localBind && len(allowedOrigins) == 0 {
		return Config{}, fmt.Errorf("non-local HTTP address %q requires --allowed-origins or GONGMCP_ALLOWED_ORIGINS for Origin validation", addr)
	}

	tokens := []string(nil)
	if authMode == "bearer" {
		tokens, err = resolveBearerTokens(selection.TokenFlag, selection.TokenFileFlag, selection.PreviousTokenFileFlag, getenv)
		if err != nil {
			return Config{}, err
		}
	}

	return Config{
		Enabled:            true,
		Addr:               addr,
		AuthMode:           authMode,
		BearerTokens:       tokens,
		OpenNetworkWarning: !localBind,
		LocalBind:          localBind,
		AllowedOrigins:     allowedOrigins,
	}, nil
}

func Handler(server *mcp.Server, cfg Config, accessLog io.Writer) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/mcp", accessLogMiddleware(originMiddleware(authMiddleware(server, cfg), cfg), cfg, accessLog))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodHead {
			return
		}
		_ = json.NewEncoder(w).Encode(struct {
			Status  string `json:"status"`
			Service string `json:"service"`
			Version string `json:"version"`
		}{
			Status:  "ok",
			Service: "gongmcp",
			Version: version.DisplayVersion(),
		})
	})
	return mux
}

func parseAllowedOrigins(flagValue, envValue string) (map[string]struct{}, error) {
	raw := strings.TrimSpace(flagValue)
	if raw == "" {
		raw = strings.TrimSpace(envValue)
	}
	out := map[string]struct{}{}
	if raw == "" {
		return out, nil
	}
	for _, piece := range strings.Split(raw, ",") {
		origin := strings.TrimSpace(piece)
		if origin == "" {
			continue
		}
		normalized, err := normalizeOrigin(origin)
		if err != nil {
			return nil, err
		}
		out[normalized] = struct{}{}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no valid allowed origins provided")
	}
	return out, nil
}

func normalizeOrigin(origin string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(origin))
	if err != nil {
		return "", fmt.Errorf("invalid allowed origin %q", origin)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("invalid allowed origin %q: scheme must be http or https", origin)
	}
	if parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("invalid allowed origin %q: use scheme://host[:port] only", origin)
	}
	return strings.ToLower(parsed.Scheme) + "://" + strings.ToLower(parsed.Host), nil
}

func resolveBearerTokens(tokenFlag, tokenFileFlag, previousTokenFileFlag string, getenv GetenvFunc) ([]string, error) {
	token := strings.TrimSpace(tokenFlag)
	tokenFile := strings.TrimSpace(tokenFileFlag)
	previousTokenFile := strings.TrimSpace(previousTokenFileFlag)
	if token != "" && tokenFile != "" {
		return nil, fmt.Errorf("set either bearer token or bearer token file, not both")
	}
	if tokenFile != "" {
		read, err := readBearerTokenFile(tokenFile)
		if err != nil {
			return nil, err
		}
		token = read
	} else if token == "" {
		token = strings.TrimSpace(getenv("GONGMCP_BEARER_TOKEN"))
		tokenFile = strings.TrimSpace(getenv("GONGMCP_BEARER_TOKEN_FILE"))
		if token != "" && tokenFile != "" {
			return nil, fmt.Errorf("set either bearer token or bearer token file, not both")
		}
		if tokenFile != "" {
			read, err := readBearerTokenFile(tokenFile)
			if err != nil {
				return nil, err
			}
			token = read
		}
	}
	if token == "" {
		return nil, fmt.Errorf("auth-mode=bearer requires bearer token or bearer token file")
	}
	if err := validateBearerToken(token); err != nil {
		return nil, err
	}
	tokens := []string{token}
	if previousTokenFile == "" {
		previousTokenFile = strings.TrimSpace(getenv("GONGMCP_BEARER_TOKEN_PREVIOUS_FILE"))
	}
	if previousTokenFile != "" {
		previous, err := readBearerTokenFile(previousTokenFile)
		if err != nil {
			return nil, err
		}
		if previous != token {
			tokens = append(tokens, previous)
		}
	}
	return tokens, nil
}

func bearerTokenSourceConfigured(tokenFlag, tokenFileFlag string, getenv GetenvFunc) bool {
	if firstNonEmpty(tokenFlag, tokenFileFlag) != "" {
		return true
	}
	return firstNonEmpty(getenv("GONGMCP_BEARER_TOKEN"), getenv("GONGMCP_BEARER_TOKEN_FILE")) != ""
}

func readBearerTokenFile(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("read bearer token file: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("read bearer token file: %s is a directory", path)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("bearer token file permissions must not allow group or other access: %s", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read bearer token file: %w", err)
	}
	token := strings.TrimSpace(string(raw))
	if err := validateBearerToken(token); err != nil {
		return "", err
	}
	return token, nil
}

func validateBearerToken(token string) error {
	trimmed := strings.TrimSpace(token)
	if trimmed == "" {
		return fmt.Errorf("auth-mode=bearer requires bearer token or bearer token file")
	}
	normalized := strings.ToLower(strings.ReplaceAll(trimmed, "_", "-"))
	switch normalized {
	case "token", "bearer-token", "secret", "password", "test", "example", "example-token", "changeme", "change-me", "replace-me", "your-token":
		return fmt.Errorf("bearer token must be random token material, not a placeholder")
	}
	if len([]byte(trimmed)) < minBearerTokenBytes {
		return fmt.Errorf("bearer token must be at least %d bytes", minBearerTokenBytes)
	}
	distinct := map[rune]struct{}{}
	for _, value := range trimmed {
		distinct[value] = struct{}{}
	}
	if len(distinct) < 8 {
		return fmt.Errorf("bearer token must contain varied random token material")
	}
	return nil
}

func originMiddleware(next http.Handler, cfg Config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin == "" {
			next.ServeHTTP(w, r)
			return
		}
		normalized, err := normalizeOrigin(origin)
		if err != nil || !originAllowed(normalized, cfg) {
			setAccessDecision(r, "origin_denied")
			http.Error(w, "invalid origin", http.StatusForbidden)
			return
		}
		setCORSHeaders(w, normalized)
		if r.Method == http.MethodOptions {
			if !validPreflightRequest(r) {
				setAccessDecision(r, "cors_preflight_denied")
				w.Header().Set("Allow", "POST, OPTIONS")
				http.Error(w, "invalid preflight", http.StatusMethodNotAllowed)
				return
			}
			setAccessDecision(r, "cors_preflight_ok")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func setCORSHeaders(w http.ResponseWriter, origin string) {
	w.Header().Add("Vary", "Origin")
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, MCP-Protocol-Version, Mcp-Session-Id")
	w.Header().Set("Access-Control-Max-Age", "600")
}

func validPreflightRequest(r *http.Request) bool {
	if !strings.EqualFold(strings.TrimSpace(r.Header.Get("Access-Control-Request-Method")), http.MethodPost) {
		return false
	}
	allowedHeaders := map[string]struct{}{
		"authorization":        {},
		"content-type":         {},
		"mcp-protocol-version": {},
		"mcp-session-id":       {},
	}
	for _, header := range strings.Split(r.Header.Get("Access-Control-Request-Headers"), ",") {
		normalized := strings.ToLower(strings.TrimSpace(header))
		if normalized == "" {
			continue
		}
		if _, ok := allowedHeaders[normalized]; !ok {
			return false
		}
	}
	return true
}

func originAllowed(origin string, cfg Config) bool {
	if _, ok := cfg.AllowedOrigins[origin]; ok {
		return true
	}
	return cfg.LocalBind && isLocalOrigin(origin)
}

func isLocalOrigin(origin string) bool {
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := strings.Trim(strings.ToLower(parsed.Hostname()), "[]")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func accessLogMiddleware(next http.Handler, cfg Config, out io.Writer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		requestID := fmt.Sprintf("%d", start.UnixNano())
		decision := &accessDecision{Reason: "ok"}
		r = r.WithContext(context.WithValue(r.Context(), accessDecisionContextKey{}, decision))
		var body bytes.Buffer
		if r.Body != nil {
			r.Body = io.NopCloser(io.TeeReader(r.Body, &body))
		}
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		method, toolName := mcpHTTPAccessInfo(body.Bytes())
		fmt.Fprintf(out, "mcp_http_access request_id=%s method=%q tool=%q status=%d decision=%q duration_ms=%d remote_addr=%q auth_mode=%q token_slot=%q\n",
			requestID,
			method,
			toolName,
			recorder.status,
			decision.Reason,
			time.Since(start).Milliseconds(),
			r.RemoteAddr,
			cfg.AuthMode,
			decision.TokenSlot,
		)
	})
}

type accessDecision struct {
	Reason    string
	TokenSlot string
}

type accessDecisionContextKey struct{}

func setAccessDecision(r *http.Request, reason string) {
	if decision, ok := r.Context().Value(accessDecisionContextKey{}).(*accessDecision); ok && decision != nil {
		decision.Reason = reason
	}
}

func setAccessTokenSlot(r *http.Request, slot string) {
	if decision, ok := r.Context().Value(accessDecisionContextKey{}).(*accessDecision); ok && decision != nil {
		decision.TokenSlot = slot
	}
}

func mcpHTTPAccessInfo(payload []byte) (string, string) {
	var req struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(payload, &req); err != nil {
		return "", ""
	}
	if req.Method != "tools/call" {
		return req.Method, ""
	}
	var params struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return req.Method, ""
	}
	return req.Method, strings.TrimSpace(params.Name)
}

func authMiddleware(next http.Handler, cfg Config) http.Handler {
	if cfg.AuthMode != "bearer" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Fields(r.Header.Get("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			setAccessDecision(r, "auth_missing")
			w.Header().Set("WWW-Authenticate", `Bearer realm="gongmcp"`)
			http.Error(w, "missing bearer token", http.StatusUnauthorized)
			return
		}
		token := parts[1]
		slot, ok := bearerTokenSlot(token, cfg.BearerTokens)
		if !ok {
			setAccessDecision(r, "auth_invalid")
			w.Header().Set("WWW-Authenticate", `Bearer realm="gongmcp"`)
			http.Error(w, "invalid bearer token", http.StatusUnauthorized)
			return
		}
		setAccessTokenSlot(r, slot)
		next.ServeHTTP(w, r)
	})
}

func bearerTokenSlot(token string, expected []string) (string, bool) {
	tokenSum := sha256.Sum256([]byte(token))
	for idx, value := range expected {
		valueSum := sha256.Sum256([]byte(value))
		if subtle.ConstantTimeCompare(tokenSum[:], valueSum[:]) == 1 {
			if idx == 0 {
				return "current", true
			}
			return "previous", true
		}
	}
	return "", false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func truthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func isLocalHTTPAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	host = strings.Trim(strings.ToLower(strings.TrimSpace(host)), "[]")
	if host == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
