package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
)

// fakeHighlightsStore wraps a real *sqlite.Store and overrides ListAIHighlights
// so facade tests can exercise the Postgres-only AI-highlights read model
// without standing up a Postgres instance.
type fakeHighlightsStore struct {
	*sqlite.Store
	rows       []sqlite.AIHighlightRow
	refs       map[string]string
	lastParams sqlite.AIHighlightListParams
	err        error
}

func (f *fakeHighlightsStore) ListAIHighlights(_ context.Context, params sqlite.AIHighlightListParams) ([]sqlite.AIHighlightRow, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.lastParams = params
	allowed := make(map[string]struct{}, len(params.CallIDs))
	for _, id := range params.CallIDs {
		allowed[id] = struct{}{}
	}
	out := make([]sqlite.AIHighlightRow, 0, len(f.rows))
	for _, row := range f.rows {
		if len(allowed) > 0 {
			if _, ok := allowed[row.CallID]; !ok {
				continue
			}
		}
		out = append(out, row)
	}
	return out, nil
}

func (f *fakeHighlightsStore) ResolveCallIDByRef(_ context.Context, ref string) (string, error) {
	normalized, err := sqlite.NormalizeStableCallRef(ref)
	if err != nil {
		return "", err
	}
	if id, ok := f.refs[normalized]; ok {
		return id, nil
	}
	return "", fmt.Errorf("call_ref not found")
}

func TestFacadeToolsExposedInToolCatalog(t *testing.T) {
	t.Parallel()

	names := map[string]struct{}{}
	for _, info := range ToolCatalog() {
		names[info.Name] = struct{}{}
	}
	for _, want := range []string{
		FacadeToolStatus,
		FacadeToolDiscoverCapabilities,
		FacadeToolQuery,
		FacadeToolAnalyze,
		FacadeToolGetEvidence,
		FacadeToolExplainLimitations,
	} {
		if _, ok := names[want]; !ok {
			t.Fatalf("ToolCatalog missing facade tool %q", want)
		}
	}
}

func TestFacadeOperationsRegistryShape(t *testing.T) {
	t.Parallel()

	ops := FacadeOperations()
	if len(ops) == 0 {
		t.Fatal("FacadeOperations returned no operations")
	}

	expected := []string{
		OpAnalyzeCohortBuild,
		OpAnalyzeCohortInspect,
		OpAnalyzeLimitationsExplain,
		OpAnalyzeThemesDiscover,
		OpEvidenceHighlightsList,
		OpEvidenceQuotePackBuild,
		OpEvidenceQuotesSearch,
		OpQueryCalls,
		OpQueryScorecardDetail,
		OpQueryScorecards,
		OpQueryTranscriptSegments,
		OpQuestionAnswer,
		OpStatusSync,
	}
	got := make([]string, 0, len(ops))
	seen := map[string]struct{}{}
	for _, op := range ops {
		if op.Name == "" || op.Version == "" || op.Description == "" || op.FacadeTool == "" || op.RoutedTool == "" || op.ExposureLevel == "" {
			t.Fatalf("operation %+v is missing required metadata", op)
		}
		if _, dup := seen[op.Name]; dup {
			t.Fatalf("operation %q appears more than once", op.Name)
		}
		seen[op.Name] = struct{}{}
		got = append(got, op.Name)
		if !isFacadeTool(op.FacadeTool) {
			t.Fatalf("operation %q references unknown facade tool %q", op.Name, op.FacadeTool)
		}
		if op.InputSchema == nil {
			t.Fatalf("operation %q has nil input schema", op.Name)
		}
	}
	if len(got) != len(expected) {
		t.Fatalf("operation names: got %v want %v", got, expected)
	}
	for i := range expected {
		if got[i] != expected[i] {
			t.Fatalf("operations not sorted: got %v want %v", got, expected)
		}
	}
}

func TestFacadeDiscoverCapabilitiesReportsRoutedAvailability(t *testing.T) {
	t.Parallel()

	server := NewServerWithOptions(nil, "gongmcp", "test", WithToolAllowlist([]string{
		FacadeToolStatus,
		FacadeToolDiscoverCapabilities,
		FacadeToolQuery,
		FacadeToolAnalyze,
		FacadeToolGetEvidence,
		FacadeToolExplainLimitations,
		"get_sync_status",
		"search_transcript_segments",
	}), WithRuntimeInfo(RuntimeInfo{
		Commit:       "abc123",
		BuildDate:    "2026-05-06T00:00:00Z",
		ToolPreset:   "redacted-all-readonly",
		DeploymentID: "lab-20260506",
		StartedAtUTC: "2026-05-06T21:45:00Z",
	}))

	result, err := server.executeFacadeDiscoverCapabilities(nil)
	if err != nil {
		t.Fatalf("discover capabilities: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected isError: %+v", result)
	}
	if len(result.Content) != 1 {
		t.Fatalf("expected one content entry, got %d", len(result.Content))
	}
	var payload struct {
		FacadeVersion string            `json:"facade_version"`
		FacadeTools   []string          `json:"facade_tools"`
		Operations    []map[string]any  `json:"operations"`
		MCPServer     PublicRuntimeInfo `json:"mcp_server"`
		Note          string            `json:"note"`
	}
	if err := json.Unmarshal([]byte(result.Content[0].Text), &payload); err != nil {
		t.Fatalf("decode capability payload: %v", err)
	}
	if payload.FacadeVersion != "v1" {
		t.Fatalf("facade_version=%q want v1", payload.FacadeVersion)
	}
	if len(payload.FacadeTools) != 6 {
		t.Fatalf("facade_tools count=%d want 6", len(payload.FacadeTools))
	}
	if payload.MCPServer.ToolPreset != "redacted-all-readonly" || payload.MCPServer.DeploymentID != "lab-20260506" || payload.MCPServer.Commit != "abc123" {
		t.Fatalf("unexpected mcp_server identity: %+v", payload.MCPServer)
	}
	if payload.MCPServer.ToolCount == 0 {
		t.Fatalf("mcp_server missing tool count: %+v", payload.MCPServer)
	}
	if !strings.Contains(payload.Note, "individual tools") {
		t.Fatalf("note missing top-level-tools statement: %q", payload.Note)
	}
	availability := map[string]bool{}
	for _, op := range payload.Operations {
		name, _ := op["operation"].(string)
		avail, _ := op["routed_tool_available"].(bool)
		availability[name] = avail
	}
	if !availability[OpStatusSync] {
		t.Fatalf("expected status.sync available with get_sync_status in allowlist")
	}
	if !availability[OpQueryTranscriptSegments] {
		t.Fatalf("expected query.transcript_segments available with search_transcript_segments in allowlist")
	}
	if availability[OpAnalyzeCohortBuild] {
		t.Fatalf("did not expect analyze.cohort.build to be available without build_call_cohort in allowlist")
	}

	facadeOnly := NewServerWithOptions(nil, "gongmcp", "test",
		WithToolAllowlist(FacadeToolNames()),
		WithFacadeRoutedToolAllowlist([]string{"build_call_cohort"}),
	)
	result, err = facadeOnly.executeFacadeDiscoverCapabilities(nil)
	if err != nil {
		t.Fatalf("discover capabilities with hidden routed tools: %v", err)
	}
	if err := json.Unmarshal([]byte(result.Content[0].Text), &payload); err != nil {
		t.Fatalf("decode capability payload: %v", err)
	}
	availability = map[string]bool{}
	for _, op := range payload.Operations {
		name, _ := op["operation"].(string)
		avail, _ := op["routed_tool_available"].(bool)
		availability[name] = avail
	}
	if !availability[OpAnalyzeCohortBuild] {
		t.Fatalf("expected analyze.cohort.build available through hidden facade routed allowlist")
	}
	if availability[OpQueryTranscriptSegments] {
		t.Fatalf("did not expect query.transcript_segments available without routed transcript tool")
	}
}

func TestFacadeStatusDispatchesToGetSyncStatus(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	server := NewServerWithOptions(store, "gongmcp", "test", WithToolAllowlist([]string{
		FacadeToolStatus,
	}), WithFacadeRoutedToolAllowlist([]string{"get_sync_status"}), WithRuntimeInfo(RuntimeInfo{
		ToolPreset:   "business-pilot",
		DeploymentID: "stdio-test",
		StartedAtUTC: "2026-05-06T21:46:00Z",
	}))

	result, err := server.executeFacadeStatus(t.Context(), nil)
	if err != nil {
		t.Fatalf("facade status: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected isError: %+v", result)
	}
	wrapper := decodeFacadeWrapper(t, result)
	if wrapper["facade_tool"] != FacadeToolStatus {
		t.Fatalf("facade_tool=%v want %s", wrapper["facade_tool"], FacadeToolStatus)
	}
	if wrapper["operation"] != OpStatusSync {
		t.Fatalf("operation=%v want %s", wrapper["operation"], OpStatusSync)
	}
	if wrapper["routed_tool"] != "get_sync_status" {
		t.Fatalf("routed_tool=%v want get_sync_status", wrapper["routed_tool"])
	}
	inner, ok := wrapper["result"].(map[string]any)
	if !ok {
		t.Fatalf("inner result missing or wrong type: %T", wrapper["result"])
	}
	if _, ok := inner["total_calls"]; !ok {
		t.Fatalf("inner result missing total_calls: %v", inner)
	}
	identity, ok := inner["mcp_server"].(map[string]any)
	if !ok {
		t.Fatalf("inner result missing mcp_server: %v", inner)
	}
	if identity["tool_preset"] != "business-pilot" || identity["deployment_id"] != "stdio-test" {
		t.Fatalf("unexpected mcp_server identity: %v", identity)
	}
}

func TestFacadeQueryRoutesTranscriptSegmentsAndDeniesUnallowedRoutedTool(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	// search_transcript_segments is in the allowlist; search_calls is not.
	server := NewServerWithOptions(store, "gongmcp", "test", WithToolAllowlist([]string{
		FacadeToolQuery,
		"search_transcript_segments",
	}))

	args, err := json.Marshal(map[string]any{
		"operation": OpQueryTranscriptSegments,
		"arguments": map[string]any{"query": "kickoff", "limit": 1},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	allowed, err := server.executeFacadeDispatch(t.Context(), FacadeToolQuery, args)
	if err != nil {
		t.Fatalf("facade dispatch: %v", err)
	}
	if allowed.IsError {
		t.Fatalf("expected dispatch to succeed, got error result: %+v", allowed)
	}
	wrapper := decodeFacadeWrapper(t, allowed)
	if wrapper["routed_tool"] != "search_transcript_segments" {
		t.Fatalf("routed_tool=%v want search_transcript_segments", wrapper["routed_tool"])
	}

	// query.calls -> primary search_calls_by_filters not exposed and the
	// fallback search_calls is also not exposed; expect a useful tool error.
	deniedArgs, err := json.Marshal(map[string]any{
		"operation": OpQueryCalls,
		"arguments": map[string]any{"filter": map[string]any{"limit": 5}},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	_, err = server.executeFacadeDispatch(t.Context(), FacadeToolQuery, deniedArgs)
	if err == nil {
		t.Fatal("expected dispatch error when routed tool is not in allowlist")
	}
	if !strings.Contains(err.Error(), "search_calls_by_filters") || !strings.Contains(err.Error(), "search_calls") {
		t.Fatalf("error did not name routed tool/fallback: %v", err)
	}
	if !strings.Contains(err.Error(), "analyst") {
		t.Fatalf("error did not name a remediation preset: %v", err)
	}
}

func TestFacadeQuestionAnswerReturnsEvidencePackAndCallDurations(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	server := NewServerWithOptions(store, "gongmcp", "test",
		WithToolAllowlist([]string{FacadeToolAnalyze}),
		WithFacadeRoutedToolAllowlist([]string{internalRoutedToolQuestionAnswer}),
	)
	result, err := server.executeFacadeDispatch(t.Context(), FacadeToolAnalyze, mustFacadeArgs(t, OpQuestionAnswer, map[string]any{
		"question":      "What did the external speaker say?",
		"query":         "Synthetic",
		"role_context":  "sales-enablement",
		"output_intent": "brief",
		"filter": map[string]any{
			"query": "Synthetic",
			"limit": 5,
		},
		"limit": 5,
	}))
	if err != nil {
		t.Fatalf("question.answer dispatch: %v", err)
	}
	wrapper := decodeFacadeWrapper(t, result)
	if wrapper["operation"] != OpQuestionAnswer || wrapper["routed_tool"] != internalRoutedToolQuestionAnswer {
		t.Fatalf("unexpected facade wrapper: %v", wrapper)
	}
	inner, ok := wrapper["result"].(map[string]any)
	if !ok {
		t.Fatalf("missing inner result: %T", wrapper["result"])
	}
	if inner["status"] != "evidence_pack_ready" || inner["question"] != "What did the external speaker say?" {
		t.Fatalf("unexpected question payload: %v", inner)
	}
	if count, _ := inner["evidence_count"].(float64); count == 0 {
		t.Fatalf("expected evidence rows, got %v", inner["evidence_count"])
	}
	reviewed, ok := inner["reviewed_calls"].([]any)
	if !ok || len(reviewed) == 0 {
		t.Fatalf("expected reviewed calls, got %T %v", inner["reviewed_calls"], inner["reviewed_calls"])
	}
	first, ok := reviewed[0].(map[string]any)
	if !ok {
		t.Fatalf("first reviewed call wrong type: %T", reviewed[0])
	}
	if _, ok := first["duration_seconds"]; !ok {
		t.Fatalf("reviewed call missing per-call duration: %v", first)
	}
	if _, ok := first["call_title"]; ok {
		t.Fatalf("question.answer exposed call title without include_call_titles: %v", first)
	}
	if _, ok := inner["answer_contract"].([]any); !ok {
		t.Fatalf("missing answer contract: %v", inner["answer_contract"])
	}
}

func TestFacadeRejectsUnknownOperationAndCrossWiredTool(t *testing.T) {
	t.Parallel()

	server := NewServerWithOptions(nil, "gongmcp", "test", WithToolAllowlist([]string{
		FacadeToolQuery,
		FacadeToolAnalyze,
		"search_transcript_segments",
		"build_call_cohort",
	}))

	_, err := server.executeFacadeDispatch(t.Context(), FacadeToolQuery, mustFacadeArgs(t, "", nil))
	if err == nil || !strings.Contains(err.Error(), "requires an operation") {
		t.Fatalf("expected missing operation error, got %v", err)
	}

	_, err = server.executeFacadeDispatch(t.Context(), FacadeToolQuery, mustFacadeArgs(t, "no.such.op", nil))
	if err == nil || !strings.Contains(err.Error(), "unknown facade operation") {
		t.Fatalf("expected unknown operation error, got %v", err)
	}

	// analyze.cohort.build is routed by gong_analyze; trying to dispatch via
	// gong_query must be rejected before invoking the routed tool.
	_, err = server.executeFacadeDispatch(t.Context(), FacadeToolQuery, mustFacadeArgs(t, OpAnalyzeCohortBuild, map[string]any{"filter": map[string]any{}}))
	if err == nil || !strings.Contains(err.Error(), "is routed by facade tool") {
		t.Fatalf("expected cross-wired facade tool error, got %v", err)
	}
}

func TestFacadeExplainLimitationsWithoutOperationReturnsHighLevelSummary(t *testing.T) {
	t.Parallel()

	server := NewServerWithOptions(nil, "gongmcp", "test", WithToolAllowlist([]string{
		FacadeToolExplainLimitations,
		"get_sync_status",
	}))

	result, err := server.executeFacadeExplainLimitations(t.Context(), nil)
	if err != nil {
		t.Fatalf("facade explain limitations: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected isError: %+v", result)
	}
	var payload struct {
		Tool                  string   `json:"tool"`
		FacadeVersion         string   `json:"facade_version"`
		Limitations           []string `json:"limitations"`
		AvailableOperations   []string `json:"available_operations"`
		UnavailableOperations []string `json:"unavailable_operations"`
	}
	if err := json.Unmarshal([]byte(result.Content[0].Text), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.Tool != FacadeToolExplainLimitations {
		t.Fatalf("tool=%q want %s", payload.Tool, FacadeToolExplainLimitations)
	}
	if payload.FacadeVersion != "v1" {
		t.Fatalf("facade_version=%q want v1", payload.FacadeVersion)
	}
	if len(payload.Limitations) == 0 {
		t.Fatalf("limitations should be non-empty")
	}
	if !containsString(payload.AvailableOperations, OpStatusSync) {
		t.Fatalf("available_operations should include status.sync; got %v", payload.AvailableOperations)
	}
	if !containsString(payload.UnavailableOperations, OpAnalyzeCohortBuild) {
		t.Fatalf("unavailable_operations should include analyze.cohort.build; got %v", payload.UnavailableOperations)
	}
}

func TestFacadeRoutedToolMustNotBeAnotherFacadeTool(t *testing.T) {
	t.Parallel()

	server := NewServerWithOptions(nil, "gongmcp", "test", WithToolAllowlist([]string{
		FacadeToolStatus,
		FacadeToolQuery,
	}))
	_, err := server.executeFacadeRouted(t.Context(), FacadeToolQuery, nil)
	if err == nil || !strings.Contains(err.Error(), "must not be another facade tool") {
		t.Fatalf("expected reject of facade->facade routing, got %v", err)
	}
}

func decodeFacadeWrapper(t *testing.T, result toolCallResult) map[string]any {
	t.Helper()
	if len(result.Content) != 1 {
		t.Fatalf("expected one content entry, got %d", len(result.Content))
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &payload); err != nil {
		t.Fatalf("decode wrapper: %v", err)
	}
	return payload
}

func mustFacadeArgs(t *testing.T, op string, arguments map[string]any) json.RawMessage {
	t.Helper()
	body := map[string]any{"operation": op}
	if arguments != nil {
		body["arguments"] = arguments
	}
	out, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal facade args: %v", err)
	}
	return out
}

func containsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func TestFacadeEvidenceHighlightsListReturnsBoundedRedactedRows(t *testing.T) {
	t.Parallel()

	base := openSeededStore(t)
	defer base.Close()

	store := &fakeHighlightsStore{
		Store: base,
		rows: []sqlite.AIHighlightRow{
			{CallID: "call-allow-1", HighlightIndex: 0, HighlightType: "key_point", HighlightText: "Discussed shared Postgres deployment.", SourcePath: "content.highlights", UpdatedAt: "2026-04-12T00:00:00Z"},
			{CallID: "call-allow-1", HighlightIndex: 1, HighlightType: "next_step", HighlightText: "Schedule architecture review next quarter.", SourcePath: "content.highlights", UpdatedAt: "2026-04-12T00:00:00Z"},
			{CallID: "call-allow-2", HighlightIndex: 0, HighlightType: "key_point", HighlightText: "Procurement gating Q2 SOW.", SourcePath: "content.highlights", UpdatedAt: "2026-04-12T00:00:00Z"},
			{CallID: "call-suppressed-1", HighlightIndex: 0, HighlightType: "key_point", HighlightText: "Should be filtered out.", SourcePath: "content.highlights", UpdatedAt: "2026-04-12T00:00:00Z"},
		},
	}

	server := NewServerWithOptions(store, "gongmcp", "test",
		WithToolAllowlist(FacadeToolNames()),
		WithFacadeRoutedToolAllowlist([]string{"list_call_ai_highlights"}),
		WithSuppressedCallIDs([]string{"call-suppressed-1"}),
	)

	args, err := json.Marshal(map[string]any{
		"operation": OpEvidenceHighlightsList,
		"arguments": map[string]any{
			"call_ids": []string{"call-allow-1", "call-allow-2", "call-suppressed-1"},
			"limit":    50,
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	result, err := server.executeFacadeDispatch(t.Context(), FacadeToolGetEvidence, args)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected isError: %+v", result)
	}
	wrapper := decodeFacadeWrapper(t, result)
	if wrapper["operation"] != OpEvidenceHighlightsList {
		t.Fatalf("operation=%v want %s", wrapper["operation"], OpEvidenceHighlightsList)
	}
	if wrapper["routed_tool"] != "list_call_ai_highlights" {
		t.Fatalf("routed_tool=%v want list_call_ai_highlights", wrapper["routed_tool"])
	}
	inner, ok := wrapper["result"].(map[string]any)
	if !ok {
		t.Fatalf("inner result type=%T", wrapper["result"])
	}
	rows, _ := inner["rows"].([]any)
	if len(rows) != 3 {
		t.Fatalf("rows count=%d want 3 (suppressed must be filtered): %+v", len(rows), inner)
	}
	for _, r := range rows {
		row, _ := r.(map[string]any)
		callID, _ := row["call_id"].(string)
		if callID == "call-suppressed-1" {
			t.Fatalf("suppressed call surfaced: %+v", row)
		}
		if _, hasRaw := row["raw_json"]; hasRaw {
			t.Fatalf("row exposes raw_json: %+v", row)
		}
		if _, hasRaw := row["raw_highlight"]; hasRaw {
			t.Fatalf("row exposes raw_highlight: %+v", row)
		}
		if _, hasType := row["highlight_type"]; !hasType {
			t.Fatalf("row missing highlight_type: %+v", row)
		}
		if _, hasText := row["highlight_text"]; !hasText {
			t.Fatalf("row missing highlight_text: %+v", row)
		}
	}
	filtered, _ := inner["suppressed_filtered_count"].(float64)
	if int(filtered) != 1 {
		t.Fatalf("suppressed_filtered_count=%v want 1 (one suppressed row dropped)", inner["suppressed_filtered_count"])
	}

	caveats, _ := inner["caveats"].([]any)
	if len(caveats) == 0 {
		t.Fatalf("expected caveats, got none in %+v", inner)
	}
	joined := ""
	for _, c := range caveats {
		joined += " " + strings.ToLower(fmt.Sprintf("%v", c))
	}
	if !strings.Contains(joined, "ai") || !strings.Contains(joined, "accelerator") {
		t.Fatalf("caveats missing Gong AI accelerator note: %v", caveats)
	}
	if !strings.Contains(joined, "transcript quotes") {
		t.Fatalf("caveats missing transcript quotes primary-evidence note: %v", caveats)
	}
	if !strings.Contains(joined, "account") || !strings.Contains(joined, "no raw") {
		t.Fatalf("caveats missing no-raw-account-enumeration note: %v", caveats)
	}

	limits, _ := inner["limits"].(map[string]any)
	if limits == nil {
		t.Fatalf("expected limits object, got %v", inner["limits"])
	}
	if _, ok := limits["limit"]; !ok {
		t.Fatalf("limits missing limit field: %+v", limits)
	}
	if _, ok := limits["max_limit"]; !ok {
		t.Fatalf("limits missing max_limit field: %+v", limits)
	}
	if _, ok := limits["max_call_ids"]; !ok {
		t.Fatalf("limits missing max_call_ids field: %+v", limits)
	}
}

func TestFacadeEvidenceHighlightsListAcceptsCallRefsWithoutRawIDLeak(t *testing.T) {
	t.Parallel()

	base := openSeededStore(t)
	defer base.Close()

	callID := "call-allow-1"
	callRef := sqlite.StableCallRef(callID)
	store := &fakeHighlightsStore{
		Store: base,
		refs:  map[string]string{callRef: callID},
		rows: []sqlite.AIHighlightRow{
			{CallID: callID, HighlightIndex: 0, HighlightType: "brief", HighlightText: "Brief confirms generated Gong summary exists.", SourcePath: "content.brief", UpdatedAt: "2026-04-12T00:00:00Z"},
		},
	}

	server := NewServerWithOptions(store, "gongmcp", "test",
		WithToolAllowlist(FacadeToolNames()),
		WithFacadeRoutedToolAllowlist([]string{"list_call_ai_highlights"}),
	)

	args, err := json.Marshal(map[string]any{
		"operation": OpEvidenceHighlightsList,
		"arguments": map[string]any{
			"call_refs": []string{callRef},
			"limit":     5,
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	result, err := server.executeFacadeDispatch(t.Context(), FacadeToolGetEvidence, args)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected isError: %+v", result)
	}
	if got := strings.Join(store.lastParams.CallIDs, ","); got != callID {
		t.Fatalf("ListAIHighlights CallIDs=%q want resolved raw call ID", got)
	}

	wrapper := decodeFacadeWrapper(t, result)
	inner, ok := wrapper["result"].(map[string]any)
	if !ok {
		t.Fatalf("inner result type=%T", wrapper["result"])
	}
	rows, _ := inner["rows"].([]any)
	if len(rows) != 1 {
		t.Fatalf("rows count=%d want 1: %+v", len(rows), inner)
	}
	row, _ := rows[0].(map[string]any)
	if got, _ := row["call_ref"].(string); got != callRef {
		t.Fatalf("call_ref=%q want %q in row %+v", got, callRef, row)
	}
	if got, _ := row["call_id"].(string); got != "" {
		t.Fatalf("raw call_id leaked for call_ref lookup: %+v", row)
	}

	requestedRefs, _ := inner["requested_call_refs"].([]any)
	if len(requestedRefs) != 1 || requestedRefs[0] != callRef {
		t.Fatalf("requested_call_refs=%v want [%s]", requestedRefs, callRef)
	}
	missingRefs, _ := inner["call_refs_without_rows"].([]any)
	if len(missingRefs) != 0 {
		t.Fatalf("call_refs_without_rows=%v want empty", missingRefs)
	}
}

func TestFacadeEvidenceHighlightsListResolvesCallRefSuppliedUnderCallIDs(t *testing.T) {
	t.Parallel()

	base := openSeededStore(t)
	defer base.Close()

	callID := "call-allow-1"
	callRef := sqlite.StableCallRef(callID)
	store := &fakeHighlightsStore{
		Store: base,
		refs:  map[string]string{callRef: callID},
		rows: []sqlite.AIHighlightRow{
			{CallID: callID, HighlightIndex: 0, HighlightType: "brief", HighlightText: "Brief confirms generated Gong summary exists.", SourcePath: "content.brief", UpdatedAt: "2026-04-12T00:00:00Z"},
		},
	}

	server := NewServerWithOptions(store, "gongmcp", "test",
		WithToolAllowlist(FacadeToolNames()),
		WithFacadeRoutedToolAllowlist([]string{"list_call_ai_highlights"}),
	)

	args, err := json.Marshal(map[string]any{
		"operation": OpEvidenceHighlightsList,
		"arguments": map[string]any{
			"call_ids": []string{callRef},
			"limit":    5,
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	result, err := server.executeFacadeDispatch(t.Context(), FacadeToolGetEvidence, args)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected isError: %+v", result)
	}
	if got := strings.Join(store.lastParams.CallIDs, ","); got != callID {
		t.Fatalf("ListAIHighlights CallIDs=%q want resolved raw call ID", got)
	}

	wrapper := decodeFacadeWrapper(t, result)
	inner, ok := wrapper["result"].(map[string]any)
	if !ok {
		t.Fatalf("inner result type=%T", wrapper["result"])
	}
	rows, _ := inner["rows"].([]any)
	if len(rows) != 1 {
		t.Fatalf("rows count=%d want 1: %+v", len(rows), inner)
	}
	row, _ := rows[0].(map[string]any)
	if got, _ := row["call_ref"].(string); got != callRef {
		t.Fatalf("call_ref=%q want %q in row %+v", got, callRef, row)
	}
	if got, _ := row["call_id"].(string); got != "" {
		t.Fatalf("raw call_id leaked when call_ref supplied via call_ids: %+v", row)
	}

	requestedRefs, _ := inner["requested_call_refs"].([]any)
	if len(requestedRefs) != 1 || requestedRefs[0] != callRef {
		t.Fatalf("requested_call_refs=%v want [%s]", requestedRefs, callRef)
	}
	requestedIDs, _ := inner["requested_call_ids"].([]any)
	if len(requestedIDs) != 0 {
		t.Fatalf("requested_call_ids=%v want empty (call_ref supplied via legacy field should not appear as raw call_id)", requestedIDs)
	}
	missingRefs, _ := inner["call_refs_without_rows"].([]any)
	if len(missingRefs) != 0 {
		t.Fatalf("call_refs_without_rows=%v want empty", missingRefs)
	}
}

func TestFacadeEvidenceHighlightsListEnforcesInputLimits(t *testing.T) {
	t.Parallel()

	base := openSeededStore(t)
	defer base.Close()

	store := &fakeHighlightsStore{Store: base}
	server := NewServerWithOptions(store, "gongmcp", "test",
		WithToolAllowlist(FacadeToolNames()),
		WithFacadeRoutedToolAllowlist([]string{"list_call_ai_highlights"}),
	)

	// Missing identifiers must be rejected so the operation never enumerates
	// the full Postgres serving DB.
	args, err := json.Marshal(map[string]any{
		"operation": OpEvidenceHighlightsList,
		"arguments": map[string]any{"limit": 5},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	_, err = server.executeFacadeDispatch(t.Context(), FacadeToolGetEvidence, args)
	if err == nil || !strings.Contains(err.Error(), "call_ids or call_refs") {
		t.Fatalf("expected error about missing call_ids/call_refs, got %v", err)
	}

	// Too many call_ids must be rejected so the operation stays bounded.
	tooMany := make([]string, 0, 32)
	for i := 0; i < 32; i++ {
		tooMany = append(tooMany, fmt.Sprintf("call-%d", i))
	}
	args, err = json.Marshal(map[string]any{
		"operation": OpEvidenceHighlightsList,
		"arguments": map[string]any{"call_ids": tooMany},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	_, err = server.executeFacadeDispatch(t.Context(), FacadeToolGetEvidence, args)
	if err == nil || !strings.Contains(err.Error(), "call_ids") {
		t.Fatalf("expected error about call_ids cap, got %v", err)
	}
}

func TestFacadeQueryScorecardInventoryOperationsRegisteredAndRouted(t *testing.T) {
	t.Parallel()

	listOp, ok := FacadeOperationByName(OpQueryScorecards)
	if !ok {
		t.Fatalf("FacadeOperationByName(%q) not registered", OpQueryScorecards)
	}
	if listOp.FacadeTool != FacadeToolQuery {
		t.Fatalf("query.scorecards facade_tool=%q want %s", listOp.FacadeTool, FacadeToolQuery)
	}
	if listOp.RoutedTool != "list_scorecards" {
		t.Fatalf("query.scorecards routed_tool=%q want list_scorecards", listOp.RoutedTool)
	}

	detailOp, ok := FacadeOperationByName(OpQueryScorecardDetail)
	if !ok {
		t.Fatalf("FacadeOperationByName(%q) not registered", OpQueryScorecardDetail)
	}
	if detailOp.FacadeTool != FacadeToolQuery {
		t.Fatalf("query.scorecard_detail facade_tool=%q want %s", detailOp.FacadeTool, FacadeToolQuery)
	}
	if detailOp.RoutedTool != "get_scorecard" {
		t.Fatalf("query.scorecard_detail routed_tool=%q want get_scorecard", detailOp.RoutedTool)
	}

	// gong_query schema must advertise the new operations so analyst-facade
	// clients can discover scorecard inventory without switching presets.
	tools := facadeTools(LimitPolicy{})
	var query tool
	for _, tl := range tools {
		if tl.Name == FacadeToolQuery {
			query = tl
			break
		}
	}
	if query.Name == "" {
		t.Fatalf("facade tools missing %s", FacadeToolQuery)
	}
	props, _ := query.InputSchema["properties"].(map[string]any)
	op, _ := props["operation"].(map[string]any)
	enum, _ := op["enum"].([]string)
	have := map[string]struct{}{}
	for _, name := range enum {
		have[name] = struct{}{}
	}
	for _, want := range []string{OpQueryScorecards, OpQueryScorecardDetail} {
		if _, ok := have[want]; !ok {
			t.Fatalf("gong_query operation enum missing %q: %v", want, enum)
		}
	}

	// Dispatch must route to the existing top-level tools without exposing
	// raw scorecard activity rows or unrestricted scorecard activity.
	store := openSeededStore(t)
	defer store.Close()
	server := NewServerWithOptions(store, "gongmcp", "test", WithToolAllowlist([]string{
		FacadeToolQuery,
		"list_scorecards",
		"get_scorecard",
	}))
	args, err := json.Marshal(map[string]any{
		"operation": OpQueryScorecards,
		"arguments": map[string]any{"limit": 5},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	result, err := server.executeFacadeDispatch(t.Context(), FacadeToolQuery, args)
	if err != nil {
		t.Fatalf("facade dispatch: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected isError: %+v", result)
	}
	wrapper := decodeFacadeWrapper(t, result)
	if wrapper["routed_tool"] != "list_scorecards" {
		t.Fatalf("routed_tool=%v want list_scorecards", wrapper["routed_tool"])
	}
}

func TestFacadeEvidenceHighlightsListAvailableThroughGetEvidenceFacade(t *testing.T) {
	t.Parallel()

	op, ok := FacadeOperationByName(OpEvidenceHighlightsList)
	if !ok {
		t.Fatalf("FacadeOperationByName(%q) not registered", OpEvidenceHighlightsList)
	}
	if op.FacadeTool != FacadeToolGetEvidence {
		t.Fatalf("evidence.highlights.list facade_tool=%q want %s", op.FacadeTool, FacadeToolGetEvidence)
	}
	if op.RoutedTool != "list_call_ai_highlights" {
		t.Fatalf("evidence.highlights.list routed_tool=%q want list_call_ai_highlights", op.RoutedTool)
	}
}
