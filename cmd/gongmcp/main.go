package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fyne-coder/gongcli_mcp/internal/governance"
	"github.com/fyne-coder/gongcli_mcp/internal/mcp"
	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
	"github.com/fyne-coder/gongcli_mcp/internal/version"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("gongmcp", flag.ContinueOnError)
	flags.SetOutput(stderr)

	dbPath := flags.String("db", "", "Path to the local gongctl SQLite cache")
	toolAllowlist := flags.String("tool-allowlist", "", "Comma-separated MCP tool allowlist; defaults to GONGMCP_TOOL_ALLOWLIST when unset; required for HTTP")
	httpAddr := flags.String("http", "", "Optional HTTP listen address for /mcp; defaults to GONGMCP_HTTP_ADDR")
	forceStdio := flags.Bool("stdio", false, "Force stdio transport and ignore GONGMCP_HTTP_ADDR")
	authMode := flags.String("auth-mode", "", "HTTP auth mode: none or bearer; defaults to GONGMCP_AUTH_MODE or bearer")
	bearerToken := flags.String("bearer-token", "", "Bearer token for HTTP auth; defaults to GONGMCP_BEARER_TOKEN")
	bearerTokenFile := flags.String("bearer-token-file", "", "Path to bearer token file; defaults to GONGMCP_BEARER_TOKEN_FILE")
	allowOpenNetwork := flags.Bool("allow-open-network", false, "Allow non-local HTTP bind addresses; defaults to GONGMCP_ALLOW_OPEN_NETWORK=1")
	devAllowNoAuthLocalhost := flags.Bool("dev-allow-no-auth-localhost", false, "Allow unauthenticated HTTP only on localhost for local development")
	allowedOrigins := flags.String("allowed-origins", "", "Comma-separated allowed HTTP Origin values; defaults to GONGMCP_ALLOWED_ORIGINS; required for non-local HTTP")
	aiGovernanceConfig := flags.String("ai-governance-config", "", "AI governance YAML config path; defaults to GONGMCP_AI_GOVERNANCE_CONFIG")
	allowUnmatchedAIGovernance := flags.Bool("allow-unmatched-ai-governance", false, "Allow AI governance config entries that do not match the current cache; defaults to GONGMCP_ALLOW_UNMATCHED_AI_GOVERNANCE when set")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "unexpected arguments: %s\n", strings.Join(flags.Args(), " "))
		return 2
	}

	db := strings.TrimSpace(*dbPath)
	if db == "" {
		fmt.Fprintln(stderr, "--db is required")
		return 2
	}
	if _, err := os.Stat(db); err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(stderr, "db file not found: %s\n", filepath.Clean(db))
			return 2
		}
		fmt.Fprintf(stderr, "stat db: %v\n", err)
		return 1
	}

	allowlist, err := parseToolAllowlist(*toolAllowlist, os.Getenv("GONGMCP_TOOL_ALLOWLIST"))
	if err != nil {
		fmt.Fprintf(stderr, "invalid tool allowlist: %v\n", err)
		return 2
	}

	ctx := context.Background()
	store, err := sqlite.OpenReadOnly(ctx, db)
	if err != nil {
		fmt.Fprintf(stderr, "open db: %v\n", err)
		return 1
	}
	defer store.Close()

	serverOptions := []mcp.ServerOption{mcp.WithToolAllowlist(allowlist)}
	if configPath := firstNonEmpty(*aiGovernanceConfig, os.Getenv("GONGMCP_AI_GOVERNANCE_CONFIG")); configPath != "" {
		if err := validateGovernanceAllowlist(allowlist); err != nil {
			fmt.Fprintf(stderr, "invalid AI governance MCP allowlist: %v\n", err)
			return 2
		}
		cfg, err := governance.LoadFile(configPath)
		if err != nil {
			fmt.Fprintln(stderr, "load AI governance config: failed")
			return 2
		}
		snapshot, err := governance.Snapshot(ctx, configPath, store)
		if err != nil {
			fmt.Fprintln(stderr, "snapshot AI governance state: failed")
			return 1
		}
		audit, err := governance.BuildAudit(ctx, store, cfg)
		if err != nil {
			fmt.Fprintln(stderr, "audit AI governance config: failed")
			return 1
		}
		if len(audit.UnmatchedEntries) > 0 && !(*allowUnmatchedAIGovernance || truthy(os.Getenv("GONGMCP_ALLOW_UNMATCHED_AI_GOVERNANCE"))) {
			fmt.Fprintf(stderr, "AI governance config has %d unmatched entries; run gongctl governance audit locally, add aliases, or set --allow-unmatched-ai-governance for this cache\n", len(audit.UnmatchedEntries))
			return 2
		}
		serverOptions = append(serverOptions, mcp.WithSuppressedCallIDs(audit.SuppressedCallIDs))
		serverOptions = append(serverOptions, mcp.WithGovernanceCheck(func(checkCtx context.Context) error {
			current, err := governance.Snapshot(checkCtx, configPath, store)
			if err != nil {
				return fmt.Errorf("AI governance state changed or cannot be verified; restart gongmcp")
			}
			if current != snapshot {
				return fmt.Errorf("AI governance state changed; restart gongmcp")
			}
			return nil
		}))
		fmt.Fprintf(stderr, "AI governance active: entries=%d aliases=%d matched=%d unmatched=%d suppressed_calls=%d; restart gongmcp after cache or config changes\n",
			audit.ConfigEntries,
			audit.ConfigAliases,
			len(audit.MatchedEntries),
			len(audit.UnmatchedEntries),
			audit.SuppressedCallCount,
		)
	}

	server := mcp.NewServerWithOptions(store, "gongmcp", version.DisplayVersion(), serverOptions...)

	httpConfig, err := resolveHTTPConfig(*httpAddr, *forceStdio, *authMode, *bearerToken, *bearerTokenFile, *allowOpenNetwork, *devAllowNoAuthLocalhost, *allowedOrigins, allowlist, os.Getenv)
	if err != nil {
		fmt.Fprintf(stderr, "invalid http config: %v\n", err)
		return 2
	}
	if httpConfig.Enabled {
		if httpConfig.OpenNetworkWarning {
			fmt.Fprintln(stderr, "warning: starting HTTP MCP on a non-local address; terminate TLS at a trusted proxy/gateway and use only for explicit private-network pilots")
		}
		handler := httpHandler(server, httpConfig, stderr)
		httpServer := &http.Server{
			Addr:              httpConfig.Addr,
			Handler:           handler,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       20 * time.Second,
			WriteTimeout:      90 * time.Second,
			IdleTimeout:       120 * time.Second,
		}
		fmt.Fprintf(stderr, "serving mcp over http on %s path=/mcp auth_mode=%s\n", httpConfig.Addr, httpConfig.AuthMode)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(stderr, "serve http mcp: %v\n", err)
			return 1
		}
		return 0
	}

	if err := server.Serve(ctx, stdin, stdout); err != nil {
		fmt.Fprintf(stderr, "serve mcp: %v\n", err)
		return 1
	}
	return 0
}

func validateGovernanceAllowlist(allowlist []string) error {
	if len(allowlist) == 0 {
		return fmt.Errorf("AI governance requires an explicit MCP tool allowlist")
	}
	safe := map[string]struct{}{
		"search_calls":                              {},
		"get_call":                                  {},
		"search_transcripts_by_crm_context":         {},
		"search_calls_by_lifecycle":                 {},
		"prioritize_transcripts_by_lifecycle":       {},
		"rank_transcript_backlog":                   {},
		"search_transcript_segments":                {},
		"search_transcripts_by_call_facts":          {},
		"search_transcript_quotes_with_attribution": {},
		"missing_transcripts":                       {},
	}
	for _, name := range allowlist {
		if _, ok := safe[name]; !ok {
			return fmt.Errorf("tool %q is not supported while AI governance filtering is active", name)
		}
	}
	return nil
}

func parseToolAllowlist(flagValue, envValue string) ([]string, error) {
	raw := strings.TrimSpace(flagValue)
	if raw == "" {
		raw = strings.TrimSpace(envValue)
	}
	if raw == "" {
		return nil, nil
	}

	catalog := make(map[string]struct{}, len(mcp.ToolCatalog()))
	for _, tool := range mcp.ToolCatalog() {
		catalog[tool.Name] = struct{}{}
	}

	seen := make(map[string]struct{})
	names := make([]string, 0)
	for _, piece := range strings.Split(raw, ",") {
		name := strings.TrimSpace(piece)
		if name == "" {
			continue
		}
		if _, ok := catalog[name]; !ok {
			return nil, fmt.Errorf("unknown tool %q", name)
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("no valid tool names provided")
	}
	return names, nil
}

type getenvFunc func(string) string

type httpConfig struct {
	Enabled            bool
	Addr               string
	AuthMode           string
	BearerToken        string
	OpenNetworkWarning bool
	LocalBind          bool
	AllowedOrigins     map[string]struct{}
}

func resolveHTTPConfig(addrFlag string, forceStdio bool, authModeFlag, tokenFlag, tokenFileFlag string, allowOpenNetworkFlag, devAllowNoAuthLocalhost bool, allowedOriginsFlag string, allowlist []string, getenv getenvFunc) (httpConfig, error) {
	if forceStdio {
		if strings.TrimSpace(addrFlag) != "" {
			return httpConfig{}, fmt.Errorf("--stdio cannot be combined with --http")
		}
		return httpConfig{}, nil
	}

	addr := firstNonEmpty(addrFlag, getenv("GONGMCP_HTTP_ADDR"))
	if addr == "" {
		return httpConfig{}, nil
	}
	if len(allowlist) == 0 {
		return httpConfig{}, fmt.Errorf("HTTP mode requires --tool-allowlist or GONGMCP_TOOL_ALLOWLIST")
	}

	authModeSource := firstNonEmpty(authModeFlag, getenv("GONGMCP_AUTH_MODE"))
	authMode := strings.ToLower(authModeSource)
	localBind := isLocalHTTPAddr(addr)
	if authMode == "" {
		if bearerTokenSourceConfigured(tokenFlag, tokenFileFlag, getenv) {
			authMode = "bearer"
		} else if devAllowNoAuthLocalhost && localBind {
			authMode = "none"
		} else {
			authMode = "bearer"
		}
	}
	if authMode != "none" && authMode != "bearer" {
		return httpConfig{}, fmt.Errorf("auth-mode must be none or bearer")
	}
	if authMode == "none" {
		if !devAllowNoAuthLocalhost {
			return httpConfig{}, fmt.Errorf("auth-mode=none requires --dev-allow-no-auth-localhost")
		}
		if !localBind {
			return httpConfig{}, fmt.Errorf("auth-mode=none is only allowed for localhost development")
		}
	}

	allowOpenNetwork := allowOpenNetworkFlag || truthy(getenv("GONGMCP_ALLOW_OPEN_NETWORK"))
	if !localBind {
		if !allowOpenNetwork {
			return httpConfig{}, fmt.Errorf("non-local HTTP address %q requires --allow-open-network and TLS termination at a trusted proxy or gateway", addr)
		}
	}
	allowedOrigins, err := parseAllowedOrigins(allowedOriginsFlag, getenv("GONGMCP_ALLOWED_ORIGINS"))
	if err != nil {
		return httpConfig{}, err
	}
	if !localBind && len(allowedOrigins) == 0 {
		return httpConfig{}, fmt.Errorf("non-local HTTP address %q requires --allowed-origins or GONGMCP_ALLOWED_ORIGINS for Origin validation", addr)
	}

	token := ""
	if authMode == "bearer" {
		token, err = resolveBearerToken(tokenFlag, tokenFileFlag, getenv)
		if err != nil {
			return httpConfig{}, err
		}
	}

	return httpConfig{
		Enabled:            true,
		Addr:               addr,
		AuthMode:           authMode,
		BearerToken:        token,
		OpenNetworkWarning: !localBind,
		LocalBind:          localBind,
		AllowedOrigins:     allowedOrigins,
	}, nil
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

func resolveBearerToken(tokenFlag, tokenFileFlag string, getenv getenvFunc) (string, error) {
	token := strings.TrimSpace(tokenFlag)
	tokenFile := strings.TrimSpace(tokenFileFlag)
	if token != "" && tokenFile != "" {
		return "", fmt.Errorf("set either bearer token or bearer token file, not both")
	}
	if token != "" {
		return token, nil
	}
	if tokenFile != "" {
		return readBearerTokenFile(tokenFile)
	}

	token = strings.TrimSpace(getenv("GONGMCP_BEARER_TOKEN"))
	tokenFile = strings.TrimSpace(getenv("GONGMCP_BEARER_TOKEN_FILE"))
	if token != "" && tokenFile != "" {
		return "", fmt.Errorf("set either bearer token or bearer token file, not both")
	}
	if tokenFile != "" {
		return readBearerTokenFile(tokenFile)
	}
	if token == "" {
		return "", fmt.Errorf("auth-mode=bearer requires bearer token or bearer token file")
	}
	return token, nil
}

func bearerTokenSourceConfigured(tokenFlag, tokenFileFlag string, getenv getenvFunc) bool {
	if firstNonEmpty(tokenFlag, tokenFileFlag) != "" {
		return true
	}
	return firstNonEmpty(getenv("GONGMCP_BEARER_TOKEN"), getenv("GONGMCP_BEARER_TOKEN_FILE")) != ""
}

func readBearerTokenFile(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read bearer token file: %w", err)
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", fmt.Errorf("auth-mode=bearer requires bearer token or bearer token file")
	}
	return token, nil
}

func httpHandler(server *mcp.Server, cfg httpConfig, accessLog io.Writer) http.Handler {
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

func originMiddleware(next http.Handler, cfg httpConfig) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin == "" {
			next.ServeHTTP(w, r)
			return
		}
		normalized, err := normalizeOrigin(origin)
		if err != nil || !originAllowed(normalized, cfg) {
			http.Error(w, "invalid origin", http.StatusForbidden)
			return
		}
		setCORSHeaders(w, normalized)
		if r.Method == http.MethodOptions {
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

func originAllowed(origin string, cfg httpConfig) bool {
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

func accessLogMiddleware(next http.Handler, cfg httpConfig, out io.Writer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		requestID := fmt.Sprintf("%d", start.UnixNano())
		var body bytes.Buffer
		if r.Body != nil {
			r.Body = io.NopCloser(io.TeeReader(r.Body, &body))
		}
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		method, toolName := mcpHTTPAccessInfo(body.Bytes())
		fmt.Fprintf(out, "mcp_http_access request_id=%s method=%q tool=%q status=%d duration_ms=%d remote_addr=%q auth_mode=%q\n",
			requestID,
			method,
			toolName,
			recorder.status,
			time.Since(start).Milliseconds(),
			r.RemoteAddr,
			cfg.AuthMode,
		)
	})
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

func authMiddleware(next http.Handler, cfg httpConfig) http.Handler {
	if cfg.AuthMode != "bearer" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Fields(r.Header.Get("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			w.Header().Set("WWW-Authenticate", `Bearer realm="gongmcp"`)
			http.Error(w, "missing bearer token", http.StatusUnauthorized)
			return
		}
		token := parts[1]
		if subtle.ConstantTimeCompare([]byte(token), []byte(cfg.BearerToken)) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="gongmcp"`)
			http.Error(w, "invalid bearer token", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
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
