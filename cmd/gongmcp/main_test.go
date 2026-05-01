package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fyne-coder/gongcli_mcp/internal/mcp"
	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
)

func TestRunRequiresDBFlag(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run(nil, bytes.NewReader(nil), &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code=%d want 2", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout not empty: %q", stdout.String())
	}
	if got := stderr.String(); got == "" || !bytes.Contains([]byte(got), []byte("--db is required")) {
		t.Fatalf("stderr=%q want missing --db message", got)
	}
}

func TestRunToolAllowlistEnvFiltersCatalog(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gong.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open returned error: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	t.Setenv("GONGMCP_TOOL_ALLOWLIST", "get_sync_status")
	stdin := strings.NewReader(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
	}, "\n") + "\n")
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"--db", dbPath}, stdin, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code=%d stderr=%q", code, stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, `"get_sync_status"`) || strings.Contains(got, `"search_calls"`) {
		t.Fatalf("stdout=%q did not reflect allowlist", got)
	}
}

func TestRunStdioFlagOverridesHTTPAddrEnv(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gong.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open returned error: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	t.Setenv("GONGMCP_HTTP_ADDR", "127.0.0.1:0")
	stdin := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}` + "\n")
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"--stdio", "--db", dbPath}, stdin, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code=%d stderr=%q", code, stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, `"protocolVersion"`) {
		t.Fatalf("stdout=%q did not look like stdio initialize response", got)
	}
}

func TestRunLoadsAIGovernanceConfigWithoutLoggingNames(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "gong.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open returned error: %v", err)
	}
	if _, err := store.UpsertCall(context.Background(), mustJSONForMainTest(t, map[string]any{
		"id":       "call-main-governance-blocked",
		"title":    "Blocked governance call",
		"started":  "2026-04-24T12:00:00Z",
		"duration": 1200,
		"context": []any{
			map[string]any{
				"objectType": "Account",
				"id":         "acct-main-governance-blocked",
				"name":       "Main Synthetic Restricted",
				"fields": []any{
					map[string]any{"name": "Name", "value": "Main Synthetic Restricted"},
				},
			},
		},
	})); err != nil {
		t.Fatalf("UpsertCall returned error: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	configPath := filepath.Join(dir, "ai-governance.yaml")
	if err := os.WriteFile(configPath, []byte(`
version: 1
lists:
  no_ai:
    customers:
      - name: "Main Synthetic Restricted"
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	stdin := strings.NewReader(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"search_calls","arguments":{"limit":5}}}`,
	}, "\n") + "\n")
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"--stdio", "--db", dbPath, "--tool-allowlist", "search_calls", "--ai-governance-config", configPath}, stdin, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code=%d stderr=%q", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "Main Synthetic Restricted") || strings.Contains(stdout.String(), "call-main-governance-blocked") {
		t.Fatalf("stdout leaked governed data: %s", stdout.String())
	}
	if strings.Contains(stderr.String(), "Main Synthetic Restricted") {
		t.Fatalf("stderr leaked governance config name: %s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "AI governance active:") || !strings.Contains(stderr.String(), "suppressed_calls=1") {
		t.Fatalf("stderr missing name-safe governance summary: %s", stderr.String())
	}
}

func TestRunRejectsCRMValueSearchInGovernanceMode(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "gong.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open returned error: %v", err)
	}
	if _, err := store.UpsertCall(context.Background(), mustJSONForMainTest(t, map[string]any{
		"id":      "call-main-governance-blocked",
		"title":   "Blocked governance call",
		"started": "2026-04-24T12:00:00Z",
		"context": []any{
			map[string]any{
				"objectType": "Account",
				"id":         "acct-main-governance-blocked",
				"name":       "Main Synthetic Restricted",
				"fields": []any{
					map[string]any{"name": "Name", "value": "Main Synthetic Restricted"},
				},
			},
		},
	})); err != nil {
		t.Fatalf("UpsertCall returned error: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	configPath := filepath.Join(dir, "ai-governance.yaml")
	if err := os.WriteFile(configPath, []byte(`
version: 1
lists:
  no_ai:
    customers:
      - name: "Main Synthetic Restricted"
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"--stdio", "--db", dbPath, "--tool-allowlist", "search_crm_field_values", "--ai-governance-config", configPath}, bytes.NewReader(nil), &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code=%d want 2", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout=%q want empty", stdout.String())
	}
	if got := stderr.String(); !strings.Contains(got, `tool "search_crm_field_values" is not supported while AI governance filtering is active`) {
		t.Fatalf("stderr=%q missing governance allowlist rejection", got)
	}
}

func TestRunToolAllowlistFlagOverridesEnvAndRejectsUnknownTools(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gong.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open returned error: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	t.Setenv("GONGMCP_TOOL_ALLOWLIST", "get_sync_status")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"--db", dbPath, "--tool-allowlist", "does_not_exist"}, bytes.NewReader(nil), &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code=%d want 2", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout=%q want empty", stdout.String())
	}
	if got := stderr.String(); !strings.Contains(got, `unknown tool "does_not_exist"`) {
		t.Fatalf("stderr=%q missing unknown-tool error", got)
	}
}

func TestRunToolAllowlistFlagPrecedenceOverEnv(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gong.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open returned error: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	t.Setenv("GONGMCP_TOOL_ALLOWLIST", "search_calls")
	stdin := strings.NewReader(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
	}, "\n") + "\n")
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"--db", dbPath, "--tool-allowlist", "get_sync_status"}, stdin, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code=%d stderr=%q", code, stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, `"get_sync_status"`) || strings.Contains(got, `"search_calls"`) {
		t.Fatalf("stdout=%q did not prefer flag allowlist", got)
	}
}

func TestResolveHTTPConfigRequiresBearerByDefaultAndNoAuthDevLocalhost(t *testing.T) {
	getenv := func(string) string { return "" }

	if _, err := resolveHTTPConfig("0.0.0.0:8080", false, "none", "", "", false, false, "", []string{"get_sync_status"}, getenv); err == nil {
		t.Fatal("resolveHTTPConfig allowed auth-mode=none without dev localhost override")
	}
	if _, err := resolveHTTPConfig("0.0.0.0:8080", false, "none", "", "", true, true, "https://app.example.com", []string{"get_sync_status"}, getenv); err == nil {
		t.Fatal("resolveHTTPConfig allowed non-local no-auth HTTP")
	}
	if _, err := resolveHTTPConfig("0.0.0.0:8080", false, "bearer", "", "", false, false, "https://app.example.com", []string{"get_sync_status"}, getenv); err == nil {
		t.Fatal("resolveHTTPConfig allowed non-local bind without explicit override")
	}

	cfg, err := resolveHTTPConfig("0.0.0.0:8080", false, "bearer", "token", "", true, false, "https://app.example.com", nil, getenv)
	if err == nil {
		t.Fatal("resolveHTTPConfig allowed non-local bind without tool allowlist")
	}

	if _, err := resolveHTTPConfig("0.0.0.0:8080", false, "bearer", "token", "", true, false, "", []string{"get_sync_status"}, getenv); err == nil {
		t.Fatal("resolveHTTPConfig allowed non-local HTTP without allowed origins")
	}

	cfg, err = resolveHTTPConfig("0.0.0.0:8080", false, "bearer", "token", "", true, false, "https://app.example.com", []string{"get_sync_status"}, getenv)
	if err != nil {
		t.Fatalf("resolveHTTPConfig returned error with override and allowlist: %v", err)
	}
	if !cfg.Enabled || cfg.AuthMode != "bearer" || !cfg.OpenNetworkWarning {
		t.Fatalf("unexpected config: %+v", cfg)
	}

	if _, err := resolveHTTPConfig("127.0.0.1:0", false, "none", "", "", false, true, "", nil, getenv); err == nil {
		t.Fatal("resolveHTTPConfig allowed HTTP without tool allowlist")
	}
	if _, err := resolveHTTPConfig("127.0.0.1:0", false, "", "", "", false, false, "", []string{"get_sync_status"}, getenv); err == nil {
		t.Fatal("resolveHTTPConfig allowed default bearer mode without token")
	}

	local, err := resolveHTTPConfig("127.0.0.1:0", false, "none", "", "", false, true, "", []string{"get_sync_status"}, getenv)
	if err != nil {
		t.Fatalf("resolveHTTPConfig rejected explicit local dev no-auth with allowlist: %v", err)
	}
	if local.AuthMode != "none" || local.OpenNetworkWarning {
		t.Fatalf("local config should not warn: %+v", local)
	}
}

func TestResolveHTTPConfigRejectsUnknownAuthMode(t *testing.T) {
	if _, err := resolveHTTPConfig("127.0.0.1:0", false, "basic", "", "", false, false, "", []string{"get_sync_status"}, func(string) string { return "" }); err == nil {
		t.Fatal("resolveHTTPConfig allowed unknown auth mode")
	}
}

func TestResolveHTTPConfigCanForceStdioWithHTTPAddrEnv(t *testing.T) {
	cfg, err := resolveHTTPConfig("", true, "", "", "", false, false, "", nil, func(key string) string {
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

	if _, err := resolveHTTPConfig("127.0.0.1:0", true, "", "", "", false, false, "", nil, func(string) string { return "" }); err == nil {
		t.Fatal("resolveHTTPConfig allowed --stdio with --http")
	}
}

func TestResolveHTTPConfigBearerTokenSources(t *testing.T) {
	tokenPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenPath, []byte(" file-token \n"), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
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
	cfg, err := resolveHTTPConfig("127.0.0.1:0", false, "", "", "", false, false, "", allowlist, getenv)
	if err != nil {
		t.Fatalf("resolveHTTPConfig returned error: %v", err)
	}
	if cfg.AuthMode != "bearer" || cfg.BearerToken != "file-token" {
		t.Fatalf("unexpected bearer config: %+v", cfg)
	}

	envFile := getenv
	cfg, err = resolveHTTPConfig("127.0.0.1:0", false, "", "flag-token", "", false, false, "", allowlist, envFile)
	if err != nil {
		t.Fatalf("resolveHTTPConfig did not let bearer token flag override env file: %v", err)
	}
	if cfg.BearerToken != "flag-token" {
		t.Fatalf("bearer token=%q want flag-token", cfg.BearerToken)
	}

	envToken := func(key string) string {
		if key == "GONGMCP_BEARER_TOKEN" {
			return "env-token"
		}
		return ""
	}
	cfg, err = resolveHTTPConfig("127.0.0.1:0", false, "", "", tokenPath, false, false, "", allowlist, envToken)
	if err != nil {
		t.Fatalf("resolveHTTPConfig did not let bearer token file flag override env token: %v", err)
	}
	if cfg.BearerToken != "file-token" {
		t.Fatalf("bearer token=%q want file-token", cfg.BearerToken)
	}

	if _, err := resolveHTTPConfig("127.0.0.1:0", false, "bearer", "flag-token", tokenPath, false, false, "", allowlist, func(string) string { return "" }); err == nil {
		t.Fatal("resolveHTTPConfig allowed both raw token and token file")
	}
	if _, err := resolveHTTPConfig("127.0.0.1:0", false, "bearer", "", "", false, false, "", allowlist, func(string) string { return "" }); err == nil {
		t.Fatal("resolveHTTPConfig allowed bearer mode without token")
	}
	if _, err := resolveHTTPConfig("127.0.0.1:0", false, "bearer", "", filepath.Join(t.TempDir(), "missing-token"), false, false, "", allowlist, func(string) string { return "" }); err == nil {
		t.Fatal("resolveHTTPConfig allowed unreadable token file")
	}
	if _, err := resolveHTTPConfig("127.0.0.1:0", false, "", "", "", false, false, "", allowlist, func(key string) string {
		switch key {
		case "GONGMCP_BEARER_TOKEN":
			return "env-token"
		case "GONGMCP_BEARER_TOKEN_FILE":
			return tokenPath
		default:
			return ""
		}
	}); err == nil {
		t.Fatal("resolveHTTPConfig allowed both env raw token and env token file")
	}
}

func TestAuthMiddlewareRequiresBearerToken(t *testing.T) {
	handler := authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), httpConfig{
		Enabled:     true,
		Addr:        "127.0.0.1:0",
		AuthMode:    "bearer",
		BearerToken: "expected-token",
	})

	for _, tc := range []struct {
		name   string
		header string
		want   int
	}{
		{name: "missing", want: http.StatusUnauthorized},
		{name: "wrong", header: "Bearer wrong-token", want: http.StatusUnauthorized},
		{name: "ok", header: "Bearer expected-token", want: http.StatusNoContent},
		{name: "lowercase-scheme", header: "bearer expected-token", want: http.StatusNoContent},
		{name: "extra-fields", header: "Bearer expected-token extra", want: http.StatusUnauthorized},
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
		Enabled:     true,
		Addr:        "127.0.0.1:0",
		AuthMode:    "bearer",
		BearerToken: "expected-token",
	})
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`

	unauthorized := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	unauthorizedRecorder := httptest.NewRecorder()
	handler.ServeHTTP(unauthorizedRecorder, unauthorized)
	if unauthorizedRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d want %d", unauthorizedRecorder.Code, http.StatusUnauthorized)
	}

	authorized := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	authorized.Header.Set("Authorization", "Bearer expected-token")
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
		Enabled:     true,
		Addr:        "127.0.0.1:0",
		AuthMode:    "bearer",
		BearerToken: "expected-token",
	}, &accessLog)

	health := httptest.NewRecorder()
	handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("health status=%d body=%q", health.Code, health.Body.String())
	}
	if !strings.Contains(health.Body.String(), `"status":"ok"`) {
		t.Fatalf("health body=%q missing status", health.Body.String())
	}

	mcpRecorder := httptest.NewRecorder()
	handler.ServeHTTP(mcpRecorder, httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{}`)))
	if mcpRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("mcp status=%d want unauthorized", mcpRecorder.Code)
	}
	if !strings.Contains(accessLog.String(), `auth_mode="bearer"`) {
		t.Fatalf("access log missing auth mode: %s", accessLog.String())
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
	handler := httpHandler(server, httpConfig{
		Enabled:     true,
		Addr:        "0.0.0.0:8080",
		AuthMode:    "bearer",
		BearerToken: "expected-token",
		AllowedOrigins: map[string]struct{}{
			"https://chatgpt.example.com": {},
		},
	}, io.Discard)
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`

	allowed := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	allowed.Header.Set("Origin", "https://chatgpt.example.com")
	allowed.Header.Set("Authorization", "Bearer expected-token")
	allowedRecorder := httptest.NewRecorder()
	handler.ServeHTTP(allowedRecorder, allowed)
	if allowedRecorder.Code != http.StatusOK {
		t.Fatalf("allowed status=%d body=%q", allowedRecorder.Code, allowedRecorder.Body.String())
	}

	blocked := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	blocked.Header.Set("Origin", "https://attacker.example.com")
	blocked.Header.Set("Authorization", "Bearer expected-token")
	blockedRecorder := httptest.NewRecorder()
	handler.ServeHTTP(blockedRecorder, blocked)
	if blockedRecorder.Code != http.StatusForbidden {
		t.Fatalf("blocked status=%d want forbidden body=%q", blockedRecorder.Code, blockedRecorder.Body.String())
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

func mustJSONForMainTest(t *testing.T, value any) []byte {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal JSON: %v", err)
	}
	return payload
}
