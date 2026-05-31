package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/fyne-coder/gongcli_mcp/internal/governance"
)

// TestPolicySwitchHideCallTitlesSuppressesGetCallTitle proves that flipping a
// single policy switch (hide_call_titles) suppresses the corresponding field
// in a real MCP get_call response. This is the end-to-end coverage required
// by the broad-public-redacted policy contract.
func TestPolicySwitchHideCallTitlesSuppressesGetCallTitle(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	server := NewServerWithOptions(store, "gongmcp", "test",
		WithPolicySwitches(PolicySwitches{HideCallTitles: true}),
	)
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "policy-hide-title",
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name": "get_call",
			"arguments": map[string]any{
				"call_id": "call_extended_001",
			},
		}),
	}))

	var envelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &envelope); err != nil {
		t.Fatalf("unmarshal tools/call response: %v", err)
	}
	if envelope.Result.IsError {
		t.Fatalf("unexpected tool error: %+v", envelope.Result)
	}
	text := envelope.Result.Content[0].Text
	if strings.Contains(text, "Expansion Q2") {
		t.Fatalf("hide_call_titles policy switch did not suppress call title in %s", text)
	}
	var detail callDetail
	if err := json.Unmarshal([]byte(text), &detail); err != nil {
		t.Fatalf("unmarshal call detail: %v", err)
	}
	if detail.Title != "" {
		t.Fatalf("hide_call_titles failed to clear title: %q", detail.Title)
	}
	if detail.CallID == "" {
		t.Fatal("hide_call_titles must not also clear call_id")
	}
}

// TestPolicySwitchHideRawCallIDsSuppressesSearchCallID proves the
// hide_raw_call_ids switch removes call_id from search_calls rows.
func TestPolicySwitchHideRawCallIDsSuppressesSearchCallID(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	server := NewServerWithOptions(store, "gongmcp", "test",
		WithPolicySwitches(PolicySwitches{HideRawCallIDs: true}),
	)
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "policy-hide-call-id",
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name":      "search_calls",
			"arguments": map[string]any{},
		}),
	}))

	var envelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &envelope); err != nil {
		t.Fatalf("unmarshal tools/call response: %v", err)
	}
	if envelope.Result.IsError {
		t.Fatalf("unexpected tool error: %+v", envelope.Result)
	}
	text := envelope.Result.Content[0].Text
	if strings.Contains(text, "call_extended_001") {
		t.Fatalf("hide_raw_call_ids did not suppress raw call_id in search_calls: %s", text)
	}
}

func TestPolicySwitchesSuppressBusinessAnalysisIdentifiers(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	seedBusinessAnalysisMCPFixtures(t, store)

	server := NewServerWithOptions(store, "gongmcp", "test",
		WithPolicySwitches(PolicySwitches{HideCallTitles: true, HideRawCallIDs: true}),
	)
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "policy-business-analysis",
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name": "search_calls_by_filters",
			"arguments": map[string]any{
				"filter": map[string]any{
					"title_query": "business discovery",
				},
				"include_call_ids":    true,
				"include_call_titles": true,
			},
		}),
	}))

	var envelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &envelope); err != nil {
		t.Fatalf("unmarshal tools/call response: %v", err)
	}
	if envelope.Result.IsError {
		t.Fatalf("unexpected tool error: %+v", envelope.Result)
	}
	text := envelope.Result.Content[0].Text
	if strings.Contains(text, "call_business_discovery_001") || strings.Contains(text, "Business Discovery") {
		t.Fatalf("policy switches did not suppress business-analysis identifiers: %s", text)
	}
	var payload businessAnalysisResponse
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		t.Fatalf("unmarshal business-analysis payload: %v", err)
	}
	if len(payload.Results) == 0 {
		t.Fatalf("expected at least one business-analysis result: %s", text)
	}
	for _, row := range payload.Results {
		if row.CallID != "" || row.CallTitle != "" {
			t.Fatalf("business-analysis row leaked identifiers under policy switches: %+v", row)
		}
	}
}

// TestRuntimeInfoSurfacesPolicySwitchContract proves operators and clients
// can inspect the policy switch contract via the public RuntimeInfo (which
// powers gongmcp /healthz and gong_status).
func TestRuntimeInfoSurfacesPolicySwitchContract(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	server := NewServerWithOptions(store, "gongmcp", "test",
		WithPolicySwitches(PolicySwitches{HideAccountNames: true, HideRawCallIDs: true}),
	)
	info := server.RuntimeInfo()
	if info.PolicySwitchReloadContract != "restart_required" {
		t.Fatalf("RuntimeInfo.PolicySwitchReloadContract=%q want restart_required", info.PolicySwitchReloadContract)
	}
	enabled := map[string]bool{}
	for _, name := range info.PolicySwitchesEnabled {
		enabled[name] = true
	}
	if !enabled["hide_account_names"] || !enabled["hide_raw_call_ids"] {
		t.Fatalf("RuntimeInfo.PolicySwitchesEnabled missing expected switches: %v", info.PolicySwitchesEnabled)
	}
	if !info.PolicySwitches.HideAccountNames || !info.PolicySwitches.HideRawCallIDs {
		t.Fatalf("RuntimeInfo.PolicySwitches struct did not reflect input: %+v", info.PolicySwitches)
	}
}

// TestBlocklistGuardSuppressesGetCallForBlockedAccountName proves the
// emit-time defense-in-depth filter rejects a get_call response whose CRM
// account name matches a blocklisted entity, even when source-to-serving
// redaction missed the row.
func TestBlocklistGuardSuppressesGetCallForBlockedAccountName(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	guard := governance.NewBlocklistGuard([]string{"Acme Corp"})
	server := NewServerWithOptions(store, "gongmcp", "test",
		WithBlocklistGuard(guard),
	)
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "blocklist-block-call",
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name": "get_call",
			"arguments": map[string]any{
				"call_id": "call_extended_001",
			},
		}),
	}))

	var envelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &envelope); err != nil {
		t.Fatalf("unmarshal tools/call response: %v", err)
	}
	if !envelope.Result.IsError {
		t.Fatalf("expected blocklist guard to deny get_call for Acme Corp; got %+v", envelope.Result)
	}
	body := envelope.Result.Content[0].Text
	if !strings.Contains(body, "call not found") {
		t.Fatalf("blocklist denial response should be a generic not-found, got %s", body)
	}
	if strings.Contains(body, "Acme Corp") {
		t.Fatalf("blocklist denial leaked the blocklisted name: %s", body)
	}
}
