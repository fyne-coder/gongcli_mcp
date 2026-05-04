package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
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
	got := toolNamesFromToolsListOutput(t, stdout.Bytes())
	if !containsString(got, "get_sync_status") || containsString(got, "search_calls") {
		t.Fatalf("tools/list names=%v did not reflect allowlist", got)
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
	got := toolNamesFromToolsListOutput(t, stdout.Bytes())
	for _, name := range []string{"get_sync_status", "summarize_call_facts", "summarize_calls_by_lifecycle", "rank_transcript_backlog"} {
		if !containsString(got, name) {
			t.Fatalf("tools/list names=%v missing preset tool %q", got, name)
		}
	}
	if containsString(got, "search_calls") {
		t.Fatalf("tools/list names=%v included non-business-pilot tool", got)
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
	for _, preset := range resp.Presets {
		if preset.Name != "analyst" && preset.Name != "all-readonly" {
			continue
		}
		for _, name := range mcp.BusinessAnalysisToolNames() {
			if !containsString(preset.Tools, name) {
				t.Fatalf("%s preset missing business-analysis tool %q", preset.Name, name)
			}
		}
	}
}

func TestRunAnalystPresetExposesBusinessAnalysisToolsOverJSONRPC(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gong.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open returned error: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	stdin := strings.NewReader(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
	}, "\n") + "\n")
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"--stdio", "--db", dbPath, "--tool-preset", "analyst"}, stdin, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code=%d stderr=%q", code, stderr.String())
	}
	got := toolNamesFromToolsListOutput(t, stdout.Bytes())
	for _, name := range mcp.BusinessAnalysisToolNames() {
		if !containsString(got, name) {
			t.Fatalf("analyst tools/list output missing %q in %v", name, got)
		}
	}
	for _, name := range []string{"search_calls", "get_call", "list_gong_settings"} {
		if containsString(got, name) {
			t.Fatalf("analyst tools/list output included admin/config-heavy tool %q in %v", name, got)
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

	code := run([]string{"--stdio", "--db", dbPath, "--tool-preset", "governance-search", "--ai-governance-config", configPath}, stdin, &stdout, &stderr)
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
			want: mcp.ToolCatalogNames(),
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
	governancePreset, err := mcp.ExpandToolPreset("governance-search")
	if err != nil {
		t.Fatalf("expandToolPreset returned error: %v", err)
	}
	if err := mcp.ValidateGovernanceAllowlist(governancePreset); err != nil {
		t.Fatalf("governance-search preset rejected by governance validator: %v", err)
	}

	analyst, err := mcp.ExpandToolPreset("analyst")
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
	for _, name := range mcp.BusinessAnalysisToolNames() {
		if !containsString(analyst, name) {
			t.Fatalf("analyst preset missing business-analysis tool %q", name)
		}
	}
	allReadonly, err := mcp.ExpandToolPreset("all-readonly")
	if err != nil {
		t.Fatalf("expandToolPreset returned error: %v", err)
	}
	for _, name := range mcp.BusinessAnalysisToolNames() {
		if !containsString(allReadonly, name) {
			t.Fatalf("all-readonly preset missing business-analysis tool %q", name)
		}
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

func TestResolveLimitPolicyFlagOverridesEnvAndClamps(t *testing.T) {
	policy, err := resolveLimitPolicy(limitSelection{
		SearchResults:    250,
		SearchResultsSet: true,
		Getenv: func(key string) string {
			if key == "GONGMCP_MAX_SEARCH_RESULTS" {
				return "125"
			}
			if key == "GONGMCP_MAX_MISSING_TRANSCRIPTS" {
				return "999999"
			}
			return ""
		},
	})
	if err != nil {
		t.Fatalf("resolveLimitPolicy returned error: %v", err)
	}
	if policy.SearchResults != 250 {
		t.Fatalf("SearchResults=%d want flag override 250", policy.SearchResults)
	}
	if policy.MissingTranscripts != 10000 {
		t.Fatalf("MissingTranscripts=%d want hard cap 10000", policy.MissingTranscripts)
	}
}

func TestResolveLimitPolicyRejectsInvalidValues(t *testing.T) {
	if _, err := resolveLimitPolicy(limitSelection{
		SearchResults:    -1,
		SearchResultsSet: true,
		Getenv:           func(string) string { return "" },
	}); err == nil {
		t.Fatal("resolveLimitPolicy allowed negative flag value")
	}
	if _, err := resolveLimitPolicy(limitSelection{
		Getenv: func(key string) string {
			if key == "GONGMCP_MAX_SEARCH_RESULTS" {
				return "nope"
			}
			return ""
		},
	}); err == nil {
		t.Fatal("resolveLimitPolicy allowed invalid env value")
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

func toolNamesFromToolsListOutput(t *testing.T, raw []byte) []string {
	t.Helper()

	decoder := json.NewDecoder(bytes.NewReader(raw))
	for {
		var envelope struct {
			Result struct {
				Tools []struct {
					Name string `json:"name"`
				} `json:"tools"`
			} `json:"result"`
		}
		if err := decoder.Decode(&envelope); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("decode JSON-RPC output: %v\n%s", err, string(raw))
		}
		if len(envelope.Result.Tools) == 0 {
			continue
		}
		names := make([]string, 0, len(envelope.Result.Tools))
		for _, tool := range envelope.Result.Tools {
			names = append(names, tool.Name)
		}
		return names
	}
	t.Fatalf("tools/list response not found in JSON-RPC output:\n%s", string(raw))
	return nil
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
	got := toolNamesFromToolsListOutput(t, stdout.Bytes())
	if !containsString(got, "get_sync_status") || containsString(got, "search_calls") {
		t.Fatalf("tools/list names=%v did not prefer flag allowlist", got)
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
