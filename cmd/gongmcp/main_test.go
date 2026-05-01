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
	"reflect"
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

func TestRunHelpExitsZero(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"--help"}, bytes.NewReader(nil), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "-list-tool-presets") {
		t.Fatalf("help output missing list-tool-presets: %s", stderr.String())
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

func TestRunToolPresetEnvFiltersCatalog(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gong.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open returned error: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	t.Setenv("GONGMCP_TOOL_PRESET", "business-pilot")
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
	got := stdout.String()
	for _, name := range []string{"get_sync_status", "summarize_call_facts", "summarize_calls_by_lifecycle", "rank_transcript_backlog"} {
		if !strings.Contains(got, `"`+name+`"`) {
			t.Fatalf("stdout=%q missing preset tool %q", got, name)
		}
	}
	if strings.Contains(got, `"search_calls"`) {
		t.Fatalf("stdout=%q included non-business-pilot tool", got)
	}
}

func TestRunListToolPresetsDoesNotRequireDB(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"--list-tool-presets"}, bytes.NewReader(nil), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code=%d stderr=%q", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr=%q want empty", stderr.String())
	}
	var resp struct {
		Presets []struct {
			Name        string   `json:"name"`
			Aliases     []string `json:"aliases"`
			Purpose     string   `json:"purpose"`
			Tools       []string `json:"tools"`
			ToolCount   int      `json:"tool_count"`
			Recommended string   `json:"recommended_for"`
		} `json:"presets"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	seen := map[string]struct{}{}
	for _, preset := range resp.Presets {
		seen[preset.Name] = struct{}{}
		if preset.Purpose == "" || preset.Recommended == "" || preset.ToolCount != len(preset.Tools) || len(preset.Tools) == 0 {
			t.Fatalf("incomplete preset entry: %+v", preset)
		}
	}
	for _, name := range []string{"business-pilot", "operator-smoke", "analyst", "governance-search", "all-readonly"} {
		if _, ok := seen[name]; !ok {
			t.Fatalf("missing preset %q in %s", name, stdout.String())
		}
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

func TestResolveToolAllowlistPresets(t *testing.T) {
	tests := []struct {
		name string
		in   toolSelection
		want []string
	}{
		{
			name: "business preset",
			in:   toolSelection{PresetEnv: "business-pilot"},
			want: []string{"get_sync_status", "summarize_call_facts", "summarize_calls_by_lifecycle", "rank_transcript_backlog"},
		},
		{
			name: "legacy strict business alias",
			in:   toolSelection{PresetEnv: "strict-business-pilot"},
			want: []string{"get_sync_status", "summarize_call_facts", "summarize_calls_by_lifecycle", "rank_transcript_backlog"},
		},
		{
			name: "operator smoke preset",
			in:   toolSelection{PresetEnv: "operator-smoke"},
			want: []string{"get_sync_status", "search_calls", "search_transcript_segments", "rank_transcript_backlog"},
		},
		{
			name: "all readonly expands to catalog",
			in:   toolSelection{PresetEnv: "all-readonly"},
			want: toolCatalogNames(),
		},
		{
			name: "governance search preset",
			in:   toolSelection{PresetEnv: "governance-search"},
			want: []string{"search_calls", "get_call", "search_transcripts_by_crm_context", "search_calls_by_lifecycle", "prioritize_transcripts_by_lifecycle", "rank_transcript_backlog", "search_transcript_segments", "search_transcripts_by_call_facts", "search_transcript_quotes_with_attribution", "missing_transcripts"},
		},
		{
			name: "flag preset overrides env allowlist",
			in:   toolSelection{PresetFlag: "business-pilot", PresetFlagSet: true, AllowlistEnv: "search_calls"},
			want: []string{"get_sync_status", "summarize_call_facts", "summarize_calls_by_lifecycle", "rank_transcript_backlog"},
		},
		{
			name: "flag allowlist overrides env preset",
			in:   toolSelection{AllowlistFlag: "get_sync_status", AllowlistFlagSet: true, PresetEnv: "all-readonly"},
			want: []string{"get_sync_status"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveToolAllowlist(tt.in)
			if err != nil {
				t.Fatalf("resolveToolAllowlist returned error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("resolveToolAllowlist=%v want %v", got, tt.want)
			}
		})
	}
}

func TestPresetGovernanceCompatibilityAndAnalystScope(t *testing.T) {
	governancePreset, err := expandToolPreset("governance-search")
	if err != nil {
		t.Fatalf("expandToolPreset returned error: %v", err)
	}
	if err := validateGovernanceAllowlist(governancePreset); err != nil {
		t.Fatalf("governance-search preset rejected by governance validator: %v", err)
	}

	analyst, err := expandToolPreset("analyst")
	if err != nil {
		t.Fatalf("expandToolPreset returned error: %v", err)
	}
	for _, denied := range []string{"search_crm_field_values", "list_gong_settings", "get_call", "search_calls", "search_calls_by_lifecycle", "missing_transcripts"} {
		if containsString(analyst, denied) {
			t.Fatalf("analyst preset includes admin/config-heavy tool %q", denied)
		}
	}
	if !containsString(analyst, "search_transcript_quotes_with_attribution") {
		t.Fatalf("analyst preset missing bounded evidence tool")
	}
}

func TestResolveToolAllowlistRejectsAmbiguousSelection(t *testing.T) {
	tests := []struct {
		name string
		in   toolSelection
	}{
		{
			name: "both flags",
			in:   toolSelection{AllowlistFlag: "get_sync_status", AllowlistFlagSet: true, PresetFlag: "business-pilot", PresetFlagSet: true},
		},
		{
			name: "both env vars",
			in:   toolSelection{AllowlistEnv: "get_sync_status", PresetEnv: "business-pilot"},
		},
		{
			name: "unknown preset",
			in:   toolSelection{PresetEnv: "not-a-preset"},
		},
		{
			name: "empty explicit flag",
			in:   toolSelection{PresetFlagSet: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := resolveToolAllowlist(tt.in); err == nil {
				t.Fatal("resolveToolAllowlist returned nil error")
			}
		})
	}
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
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

	if _, err := resolveHTTPConfig("0.0.0.0:8080", false, "none", "", "", "", false, false, "", []string{"get_sync_status"}, getenv); err == nil {
		t.Fatal("resolveHTTPConfig allowed auth-mode=none without dev localhost override")
	}
	if _, err := resolveHTTPConfig("0.0.0.0:8080", false, "none", "", "", "", true, true, "https://app.example.com", []string{"get_sync_status"}, getenv); err == nil {
		t.Fatal("resolveHTTPConfig allowed non-local no-auth HTTP")
	}
	if _, err := resolveHTTPConfig("0.0.0.0:8080", false, "bearer", "", "", "", false, false, "https://app.example.com", []string{"get_sync_status"}, getenv); err == nil {
		t.Fatal("resolveHTTPConfig allowed non-local bind without explicit override")
	}

	cfg, err := resolveHTTPConfig("0.0.0.0:8080", false, "bearer", "token", "", "", true, false, "https://app.example.com", nil, getenv)
	if err == nil {
		t.Fatal("resolveHTTPConfig allowed non-local bind without tool allowlist")
	}

	if _, err := resolveHTTPConfig("0.0.0.0:8080", false, "bearer", "token", "", "", true, false, "", []string{"get_sync_status"}, getenv); err == nil {
		t.Fatal("resolveHTTPConfig allowed non-local HTTP without allowed origins")
	}

	cfg, err = resolveHTTPConfig("0.0.0.0:8080", false, "bearer", "token", "", "", true, false, "https://app.example.com", []string{"get_sync_status"}, getenv)
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
	if err := os.WriteFile(tokenPath, []byte(" file-token \n"), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}
	previousTokenPath := filepath.Join(t.TempDir(), "previous-token")
	if err := os.WriteFile(previousTokenPath, []byte(" previous-token \n"), 0o600); err != nil {
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
	if cfg.AuthMode != "bearer" || len(cfg.BearerTokens) != 1 || cfg.BearerTokens[0] != "file-token" {
		t.Fatalf("unexpected bearer config: %+v", cfg)
	}

	envFile := getenv
	cfg, err = resolveHTTPConfig("127.0.0.1:0", false, "", "flag-token", "", "", false, false, "", allowlist, envFile)
	if err != nil {
		t.Fatalf("resolveHTTPConfig did not let bearer token flag override env file: %v", err)
	}
	if len(cfg.BearerTokens) != 1 || cfg.BearerTokens[0] != "flag-token" {
		t.Fatalf("bearer tokens=%v want flag-token", cfg.BearerTokens)
	}

	envToken := func(key string) string {
		if key == "GONGMCP_BEARER_TOKEN" {
			return "env-token"
		}
		return ""
	}
	cfg, err = resolveHTTPConfig("127.0.0.1:0", false, "", "", tokenPath, "", false, false, "", allowlist, envToken)
	if err != nil {
		t.Fatalf("resolveHTTPConfig did not let bearer token file flag override env token: %v", err)
	}
	if len(cfg.BearerTokens) != 1 || cfg.BearerTokens[0] != "file-token" {
		t.Fatalf("bearer tokens=%v want file-token", cfg.BearerTokens)
	}

	if _, err := resolveHTTPConfig("127.0.0.1:0", false, "bearer", "flag-token", tokenPath, "", false, false, "", allowlist, func(string) string { return "" }); err == nil {
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
			return "env-token"
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
	if !reflect.DeepEqual(cfg.BearerTokens, []string{"file-token", "previous-token"}) {
		t.Fatalf("bearer tokens=%v want current and previous", cfg.BearerTokens)
	}
}

func TestAuthMiddlewareRequiresBearerToken(t *testing.T) {
	handler := authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), httpConfig{
		Enabled:      true,
		Addr:         "127.0.0.1:0",
		AuthMode:     "bearer",
		BearerTokens: []string{"expected-token", "previous-token"},
	})

	for _, tc := range []struct {
		name   string
		header string
		want   int
	}{
		{name: "missing", want: http.StatusUnauthorized},
		{name: "wrong", header: "Bearer wrong-token", want: http.StatusUnauthorized},
		{name: "ok", header: "Bearer expected-token", want: http.StatusNoContent},
		{name: "previous", header: "Bearer previous-token", want: http.StatusNoContent},
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
		Enabled:      true,
		Addr:         "127.0.0.1:0",
		AuthMode:     "bearer",
		BearerTokens: []string{"expected-token", "previous-token"},
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
		Enabled:      true,
		Addr:         "127.0.0.1:0",
		AuthMode:     "bearer",
		BearerTokens: []string{"expected-token", "previous-token"},
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
		BearerTokens: []string{"expected-token", "previous-token"},
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
	allowed.Header.Set("Authorization", "Bearer expected-token")
	allowedRecorder := httptest.NewRecorder()
	handler.ServeHTTP(allowedRecorder, allowed)
	if allowedRecorder.Code != http.StatusOK {
		t.Fatalf("allowed status=%d body=%q", allowedRecorder.Code, allowedRecorder.Body.String())
	}

	previous := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	previous.Header.Set("Origin", "https://chatgpt.example.com")
	previous.Header.Set("Authorization", "Bearer previous-token")
	previousRecorder := httptest.NewRecorder()
	handler.ServeHTTP(previousRecorder, previous)
	if previousRecorder.Code != http.StatusOK {
		t.Fatalf("previous status=%d body=%q", previousRecorder.Code, previousRecorder.Body.String())
	}

	blocked := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	blocked.Header.Set("Origin", "https://attacker.example.com")
	blocked.Header.Set("Authorization", "Bearer expected-token")
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

func mustJSONForMainTest(t *testing.T, value any) []byte {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal JSON: %v", err)
	}
	return payload
}
