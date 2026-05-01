package main

import (
	"context"
	"crypto/subtle"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
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
	authMode := flags.String("auth-mode", "", "HTTP auth mode: none or bearer; defaults to GONGMCP_AUTH_MODE or none")
	bearerToken := flags.String("bearer-token", "", "Bearer token for HTTP auth; defaults to GONGMCP_BEARER_TOKEN")
	bearerTokenFile := flags.String("bearer-token-file", "", "Path to bearer token file; defaults to GONGMCP_BEARER_TOKEN_FILE")
	allowOpenNetwork := flags.Bool("allow-open-network", false, "Allow non-local HTTP bind addresses; defaults to GONGMCP_ALLOW_OPEN_NETWORK=1")
	aiGovernanceConfig := flags.String("ai-governance-config", "", "AI governance YAML config path; defaults to GONGMCP_AI_GOVERNANCE_CONFIG")
	allowUnmatchedAIGovernance := flags.Bool("allow-unmatched-ai-governance", false, "Allow AI governance config entries that do not match the current cache; defaults to GONGMCP_ALLOW_UNMATCHED_AI_GOVERNANCE=1")
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

	httpConfig, err := resolveHTTPConfig(*httpAddr, *forceStdio, *authMode, *bearerToken, *bearerTokenFile, *allowOpenNetwork, allowlist, os.Getenv)
	if err != nil {
		fmt.Fprintf(stderr, "invalid http config: %v\n", err)
		return 2
	}
	if httpConfig.Enabled {
		if httpConfig.OpenNetworkWarning {
			fmt.Fprintln(stderr, "warning: starting HTTP MCP on a non-local address; terminate TLS at a trusted proxy/gateway and use only for explicit private-network pilots")
		}
		handler := authMiddleware(server, httpConfig)
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
}

func resolveHTTPConfig(addrFlag string, forceStdio bool, authModeFlag, tokenFlag, tokenFileFlag string, allowOpenNetworkFlag bool, allowlist []string, getenv getenvFunc) (httpConfig, error) {
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
	if authMode == "" {
		if bearerTokenSourceConfigured(tokenFlag, tokenFileFlag, getenv) {
			authMode = "bearer"
		} else {
			authMode = "none"
		}
	}
	if authMode != "none" && authMode != "bearer" {
		return httpConfig{}, fmt.Errorf("auth-mode must be none or bearer")
	}

	localBind := isLocalHTTPAddr(addr)
	allowOpenNetwork := allowOpenNetworkFlag || truthy(getenv("GONGMCP_ALLOW_OPEN_NETWORK"))
	if !localBind {
		if !allowOpenNetwork {
			return httpConfig{}, fmt.Errorf("non-local HTTP address %q requires --allow-open-network and TLS termination at a trusted proxy or gateway", addr)
		}
	}

	token := ""
	if authMode == "bearer" {
		var err error
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
	}, nil
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
