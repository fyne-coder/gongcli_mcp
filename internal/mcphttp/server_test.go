package mcphttp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/fyne-coder/gongcli_mcp/internal/mcp"
	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
)

type httpConfig = Config

const (
	currentBearerToken  = "0123456789abcdef0123456789abcdef"
	previousBearerToken = "abcdef0123456789abcdef0123456789"
	envBearerToken      = "fedcba9876543210fedcba9876543210"
	wrongBearerToken    = "ffffffffffffffffffffffffffffffff"
)

func resolveHTTPConfig(addrFlag string, forceStdio bool, authModeFlag, tokenFlag, tokenFileFlag, previousTokenFileFlag string, allowOpenNetworkFlag, devAllowNoAuthLocalhost bool, allowedOriginsFlag string, allowlist []string, getenv GetenvFunc) (Config, error) {
	return ResolveConfig(ConfigSelection{
		AddrFlag:                addrFlag,
		ForceStdio:              forceStdio,
		AuthModeFlag:            authModeFlag,
		TokenFlag:               tokenFlag,
		TokenFileFlag:           tokenFileFlag,
		PreviousTokenFileFlag:   previousTokenFileFlag,
		AllowOpenNetworkFlag:    allowOpenNetworkFlag,
		DevAllowNoAuthLocalhost: devAllowNoAuthLocalhost,
		AllowedOriginsFlag:      allowedOriginsFlag,
		Allowlist:               allowlist,
		Getenv:                  getenv,
	})
}

func httpHandler(server *mcp.Server, cfg Config, accessLog io.Writer) http.Handler {
	return Handler(server, cfg, accessLog)
}

func TestResolveHTTPConfigRequiresBearerByDefaultAndNoAuthDevLocalhost(t *testing.T) {
	getenv := func(string) string { return "" }

	if _, err := resolveHTTPConfig("0.0.0.0:8080", false, "none", "", "", "", false, false, "", []string{"get_sync_status"}, getenv); err == nil {
		t.Fatal("resolveHTTPConfig allowed auth-mode=none without dev localhost override")
	}
	if _, err := resolveHTTPConfig("0.0.0.0:8080", false, "none", "", "", "", true, true, "https://app.example.com", []string{"get_sync_status"}, getenv); err == nil {
		t.Fatal("resolveHTTPConfig allowed non-local no-auth HTTP")
	}
	if _, err := resolveHTTPConfig("0.0.0.0:8080", false, "bearer", "", "", "", false, false, "https://app.example.com", []string{"get_sync_status"}, getenv); err == nil {
		t.Fatal("resolveHTTPConfig allowed non-local bind without explicit override")
	}

	cfg, err := resolveHTTPConfig("0.0.0.0:8080", false, "bearer", currentBearerToken, "", "", true, false, "https://app.example.com", nil, getenv)
	if err == nil {
		t.Fatal("resolveHTTPConfig allowed non-local bind without tool allowlist")
	}

	if _, err := resolveHTTPConfig("0.0.0.0:8080", false, "bearer", currentBearerToken, "", "", true, false, "", []string{"get_sync_status"}, getenv); err == nil {
		t.Fatal("resolveHTTPConfig allowed non-local HTTP without allowed origins")
	}

	cfg, err = resolveHTTPConfig("0.0.0.0:8080", false, "bearer", currentBearerToken, "", "", true, false, "https://app.example.com", []string{"get_sync_status"}, getenv)
	if err != nil {
		t.Fatalf("resolveHTTPConfig returned error with override and allowlist: %v", err)
	}
	if !cfg.Enabled || cfg.AuthMode != "bearer" || !cfg.OpenNetworkWarning {
		t.Fatalf("unexpected config: %+v", cfg)
	}

	if _, err := resolveHTTPConfig("127.0.0.1:0", false, "none", "", "", "", false, true, "", nil, getenv); err == nil {
		t.Fatal("resolveHTTPConfig allowed HTTP without tool allowlist")
	}
	if _, err := resolveHTTPConfig("127.0.0.1:0", false, "", "", "", "", false, false, "", []string{"get_sync_status"}, getenv); err == nil {
		t.Fatal("resolveHTTPConfig allowed default bearer mode without token")
	}

	local, err := resolveHTTPConfig("127.0.0.1:0", false, "none", "", "", "", false, true, "", []string{"get_sync_status"}, getenv)
	if err != nil {
		t.Fatalf("resolveHTTPConfig rejected explicit local dev no-auth with allowlist: %v", err)
	}
	if local.AuthMode != "none" || local.OpenNetworkWarning {
		t.Fatalf("local config should not warn: %+v", local)
	}
}

func TestResolveHTTPConfigRejectsUnknownAuthMode(t *testing.T) {
	if _, err := resolveHTTPConfig("127.0.0.1:0", false, "basic", "", "", "", false, false, "", []string{"get_sync_status"}, func(string) string { return "" }); err == nil {
		t.Fatal("resolveHTTPConfig allowed unknown auth mode")
	}
}

func TestResolveHTTPConfigCanForceStdioWithHTTPAddrEnv(t *testing.T) {
	cfg, err := resolveHTTPConfig("", true, "", "", "", "", false, false, "", nil, func(key string) string {
		if key == "GONGMCP_HTTP_ADDR" {
			return "127.0.0.1:0"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("resolveHTTPConfig returned error: %v", err)
	}
	if cfg.Enabled {
		t.Fatalf("force stdio should disable HTTP config: %+v", cfg)
	}

	if _, err := resolveHTTPConfig("127.0.0.1:0", true, "", "", "", "", false, false, "", nil, func(string) string { return "" }); err == nil {
		t.Fatal("resolveHTTPConfig allowed --stdio with --http")
	}
}

func TestResolveHTTPConfigBearerTokenSources(t *testing.T) {
	tokenPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenPath, []byte(" "+currentBearerToken+" \n"), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}
	previousTokenPath := filepath.Join(t.TempDir(), "previous-token")
	if err := os.WriteFile(previousTokenPath, []byte(" "+previousBearerToken+" \n"), 0o600); err != nil {
		t.Fatalf("write previous token file: %v", err)
	}

	getenv := func(key string) string {
		switch key {
		case "GONGMCP_BEARER_TOKEN_FILE":
			return tokenPath
		default:
			return ""
		}
	}
	allowlist := []string{"get_sync_status"}
	cfg, err := resolveHTTPConfig("127.0.0.1:0", false, "", "", "", "", false, false, "", allowlist, getenv)
	if err != nil {
		t.Fatalf("resolveHTTPConfig returned error: %v", err)
	}
	if cfg.AuthMode != "bearer" || len(cfg.BearerTokens) != 1 || cfg.BearerTokens[0] != currentBearerToken {
		t.Fatalf("unexpected bearer config: %+v", cfg)
	}

	envFile := getenv
	cfg, err = resolveHTTPConfig("127.0.0.1:0", false, "", previousBearerToken, "", "", false, false, "", allowlist, envFile)
	if err != nil {
		t.Fatalf("resolveHTTPConfig did not let bearer token flag override env file: %v", err)
	}
	if len(cfg.BearerTokens) != 1 || cfg.BearerTokens[0] != previousBearerToken {
		t.Fatalf("bearer tokens=%v want flag token", cfg.BearerTokens)
	}

	envToken := func(key string) string {
		if key == "GONGMCP_BEARER_TOKEN" {
			return envBearerToken
		}
		return ""
	}
	cfg, err = resolveHTTPConfig("127.0.0.1:0", false, "", "", tokenPath, "", false, false, "", allowlist, envToken)
	if err != nil {
		t.Fatalf("resolveHTTPConfig did not let bearer token file flag override env token: %v", err)
	}
	if len(cfg.BearerTokens) != 1 || cfg.BearerTokens[0] != currentBearerToken {
		t.Fatalf("bearer tokens=%v want file token", cfg.BearerTokens)
	}

	if _, err := resolveHTTPConfig("127.0.0.1:0", false, "bearer", previousBearerToken, tokenPath, "", false, false, "", allowlist, func(string) string { return "" }); err == nil {
		t.Fatal("resolveHTTPConfig allowed both raw token and token file")
	}
	if _, err := resolveHTTPConfig("127.0.0.1:0", false, "bearer", "", "", "", false, false, "", allowlist, func(string) string { return "" }); err == nil {
		t.Fatal("resolveHTTPConfig allowed bearer mode without token")
	}
	if _, err := resolveHTTPConfig("127.0.0.1:0", false, "bearer", "", filepath.Join(t.TempDir(), "missing-token"), "", false, false, "", allowlist, func(string) string { return "" }); err == nil {
		t.Fatal("resolveHTTPConfig allowed unreadable token file")
	}
	if _, err := resolveHTTPConfig("127.0.0.1:0", false, "", "", "", "", false, false, "", allowlist, func(key string) string {
		switch key {
		case "GONGMCP_BEARER_TOKEN":
			return envBearerToken
		case "GONGMCP_BEARER_TOKEN_FILE":
			return tokenPath
		default:
			return ""
		}
	}); err == nil {
		t.Fatal("resolveHTTPConfig allowed both env raw token and env token file")
	}

	cfg, err = resolveHTTPConfig("127.0.0.1:0", false, "bearer", "", tokenPath, previousTokenPath, false, false, "", allowlist, func(string) string { return "" })
	if err != nil {
		t.Fatalf("resolveHTTPConfig rejected previous token file: %v", err)
	}
	if !reflect.DeepEqual(cfg.BearerTokens, []string{currentBearerToken, previousBearerToken}) {
		t.Fatalf("bearer tokens=%v want current and previous", cfg.BearerTokens)
	}
}

func TestResolveHTTPConfigRejectsWeakBearerTokens(t *testing.T) {
	allowlist := []string{"get_sync_status"}
	if _, err := resolveHTTPConfig("127.0.0.1:0", false, "bearer", "token", "", "", false, false, "", allowlist, func(string) string { return "" }); err == nil {
		t.Fatal("resolveHTTPConfig allowed placeholder bearer token")
	}
	if _, err := resolveHTTPConfig("127.0.0.1:0", false, "bearer", "short-token", "", "", false, false, "", allowlist, func(string) string { return "" }); err == nil {
		t.Fatal("resolveHTTPConfig allowed short bearer token")
	}
	if _, err := resolveHTTPConfig("127.0.0.1:0", false, "bearer", strings.Repeat("a", 32), "", "", false, false, "", allowlist, func(string) string { return "" }); err == nil {
		t.Fatal("resolveHTTPConfig allowed repeated-character bearer token")
	}
}

func TestResolveHTTPConfigRejectsLooseTokenFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission checks do not apply on Windows")
	}
	tokenPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenPath, []byte(currentBearerToken), 0o644); err != nil {
		t.Fatalf("write token file: %v", err)
	}
	if _, err := resolveHTTPConfig("127.0.0.1:0", false, "bearer", "", tokenPath, "", false, false, "", []string{"get_sync_status"}, func(string) string { return "" }); err == nil {
		t.Fatal("resolveHTTPConfig allowed loose bearer token file permissions")
	}
}

func TestAuthMiddlewareRequiresBearerToken(t *testing.T) {
	handler := authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), httpConfig{
		Enabled:      true,
		Addr:         "127.0.0.1:0",
		AuthMode:     "bearer",
		BearerTokens: []string{currentBearerToken, previousBearerToken},
	})

	for _, tc := range []struct {
		name   string
		header string
		want   int
	}{
		{name: "missing", want: http.StatusUnauthorized},
		{name: "wrong", header: "Bearer " + wrongBearerToken, want: http.StatusUnauthorized},
		{name: "ok", header: "Bearer " + currentBearerToken, want: http.StatusNoContent},
		{name: "previous", header: "Bearer " + previousBearerToken, want: http.StatusNoContent},
		{name: "lowercase-scheme", header: "bearer " + currentBearerToken, want: http.StatusNoContent},
		{name: "extra-fields", header: "Bearer " + currentBearerToken + " extra", want: http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)
			if recorder.Code != tc.want {
				t.Fatalf("status=%d want %d body=%q", recorder.Code, tc.want, recorder.Body.String())
			}
		})
	}
}

func TestBearerHTTPStackProtectsMCPRequests(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gong.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open returned error: %v", err)
	}
	defer store.Close()

	server := mcp.NewServer(store, "gongmcp", "test")
	handler := authMiddleware(server, httpConfig{
		Enabled:      true,
		Addr:         "127.0.0.1:0",
		AuthMode:     "bearer",
		BearerTokens: []string{currentBearerToken, previousBearerToken},
	})
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`

	unauthorized := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	unauthorizedRecorder := httptest.NewRecorder()
	handler.ServeHTTP(unauthorizedRecorder, unauthorized)
	if unauthorizedRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d want %d", unauthorizedRecorder.Code, http.StatusUnauthorized)
	}

	authorized := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	authorized.Header.Set("Authorization", "Bearer "+currentBearerToken)
	authorizedRecorder := httptest.NewRecorder()
	handler.ServeHTTP(authorizedRecorder, authorized)
	if authorizedRecorder.Code != http.StatusOK {
		t.Fatalf("authorized status=%d want %d body=%q", authorizedRecorder.Code, http.StatusOK, authorizedRecorder.Body.String())
	}
	if !strings.Contains(authorizedRecorder.Body.String(), `"protocolVersion"`) {
		t.Fatalf("authorized response=%q missing initialize result", authorizedRecorder.Body.String())
	}
}

func TestHTTPHandlerExposesUnauthenticatedHealthzOnly(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gong.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open returned error: %v", err)
	}
	defer store.Close()

	server := mcp.NewServer(store, "gongmcp", "test")
	var accessLog bytes.Buffer
	handler := httpHandler(server, httpConfig{
		Enabled:      true,
		Addr:         "127.0.0.1:0",
		AuthMode:     "bearer",
		BearerTokens: []string{currentBearerToken, previousBearerToken},
	}, &accessLog)

	health := httptest.NewRecorder()
	handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("health status=%d body=%q", health.Code, health.Body.String())
	}
	if !json.Valid(health.Body.Bytes()) {
		t.Fatalf("health body is not valid JSON: %q", health.Body.String())
	}
	var healthPayload struct {
		Status  string `json:"status"`
		Service string `json:"service"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(health.Body.Bytes(), &healthPayload); err != nil {
		t.Fatalf("unmarshal health JSON: %v", err)
	}
	if healthPayload.Status != "ok" || healthPayload.Service != "gongmcp" || healthPayload.Version == "" {
		t.Fatalf("unexpected health payload: %+v", healthPayload)
	}

	mcpRecorder := httptest.NewRecorder()
	handler.ServeHTTP(mcpRecorder, httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{}`)))
	if mcpRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("mcp status=%d want unauthorized", mcpRecorder.Code)
	}
	if !strings.Contains(accessLog.String(), `auth_mode="bearer"`) {
		t.Fatalf("access log missing auth mode: %s", accessLog.String())
	}
	if !strings.Contains(accessLog.String(), `decision="auth_missing"`) {
		t.Fatalf("access log missing auth rejection decision: %s", accessLog.String())
	}
	if strings.Contains(accessLog.String(), `{}`) {
		t.Fatalf("access log leaked request payload: %s", accessLog.String())
	}
}

func TestHTTPHandlerValidatesOrigin(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gong.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open returned error: %v", err)
	}
	defer store.Close()

	server := mcp.NewServer(store, "gongmcp", "test")
	var accessLog bytes.Buffer
	handler := httpHandler(server, httpConfig{
		Enabled:      true,
		Addr:         "0.0.0.0:8080",
		AuthMode:     "bearer",
		BearerTokens: []string{currentBearerToken, previousBearerToken},
		AllowedOrigins: map[string]struct{}{
			"https://chatgpt.example.com": {},
		},
	}, &accessLog)
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`

	preflight := httptest.NewRequest(http.MethodOptions, "/mcp", nil)
	preflight.Header.Set("Origin", "https://chatgpt.example.com")
	preflight.Header.Set("Access-Control-Request-Method", "POST")
	preflight.Header.Set("Access-Control-Request-Headers", "authorization,content-type")
	preflightRecorder := httptest.NewRecorder()
	handler.ServeHTTP(preflightRecorder, preflight)
	if preflightRecorder.Code != http.StatusNoContent {
		t.Fatalf("preflight status=%d body=%q", preflightRecorder.Code, preflightRecorder.Body.String())
	}
	if got := preflightRecorder.Header().Get("Access-Control-Allow-Origin"); got != "https://chatgpt.example.com" {
		t.Fatalf("preflight allow origin=%q", got)
	}
	if got := preflightRecorder.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(got, "Authorization") || !strings.Contains(got, "Content-Type") {
		t.Fatalf("preflight allow headers=%q", got)
	}

	badPreflight := httptest.NewRequest(http.MethodOptions, "/mcp", nil)
	badPreflight.Header.Set("Origin", "https://chatgpt.example.com")
	badPreflight.Header.Set("Access-Control-Request-Method", "GET")
	badPreflightRecorder := httptest.NewRecorder()
	handler.ServeHTTP(badPreflightRecorder, badPreflight)
	if badPreflightRecorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("bad preflight status=%d body=%q", badPreflightRecorder.Code, badPreflightRecorder.Body.String())
	}

	allowed := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	allowed.Header.Set("Origin", "https://chatgpt.example.com")
	allowed.Header.Set("Authorization", "Bearer "+currentBearerToken)
	allowedRecorder := httptest.NewRecorder()
	handler.ServeHTTP(allowedRecorder, allowed)
	if allowedRecorder.Code != http.StatusOK {
		t.Fatalf("allowed status=%d body=%q", allowedRecorder.Code, allowedRecorder.Body.String())
	}

	previous := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	previous.Header.Set("Origin", "https://chatgpt.example.com")
	previous.Header.Set("Authorization", "Bearer "+previousBearerToken)
	previousRecorder := httptest.NewRecorder()
	handler.ServeHTTP(previousRecorder, previous)
	if previousRecorder.Code != http.StatusOK {
		t.Fatalf("previous status=%d body=%q", previousRecorder.Code, previousRecorder.Body.String())
	}

	blocked := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	blocked.Header.Set("Origin", "https://attacker.example.com")
	blocked.Header.Set("Authorization", "Bearer "+currentBearerToken)
	blockedRecorder := httptest.NewRecorder()
	handler.ServeHTTP(blockedRecorder, blocked)
	if blockedRecorder.Code != http.StatusForbidden {
		t.Fatalf("blocked status=%d want forbidden body=%q", blockedRecorder.Code, blockedRecorder.Body.String())
	}
	logOutput := accessLog.String()
	if !strings.Contains(logOutput, "status=204") || !strings.Contains(logOutput, "status=200") || !strings.Contains(logOutput, "status=403") {
		t.Fatalf("access log did not record preflight, success, and origin rejection: %s", logOutput)
	}
	for _, slot := range []string{`token_slot="current"`, `token_slot="previous"`} {
		if !strings.Contains(logOutput, slot) {
			t.Fatalf("access log missing %s: %s", slot, logOutput)
		}
	}
	for _, decision := range []string{`decision="cors_preflight_ok"`, `decision="cors_preflight_denied"`, `decision="origin_denied"`} {
		if !strings.Contains(logOutput, decision) {
			t.Fatalf("access log missing %s: %s", decision, logOutput)
		}
	}
}

func TestHTTPHandlerAllowsLoopbackOriginsForLocalBind(t *testing.T) {
	handler := httpHandler(mcp.NewServer(nil, "gongmcp", "test"), httpConfig{
		Enabled:   true,
		Addr:      "127.0.0.1:8080",
		AuthMode:  "none",
		LocalBind: true,
	}, io.Discard)

	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	req.Header.Set("Origin", "http://localhost:3000")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("loopback origin status=%d body=%q", recorder.Code, recorder.Body.String())
	}
}
