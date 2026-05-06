package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

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
		OpEvidenceQuotePackBuild,
		OpEvidenceQuotesSearch,
		OpQueryCalls,
		OpQueryTranscriptSegments,
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
		FacadeVersion string           `json:"facade_version"`
		FacadeTools   []string         `json:"facade_tools"`
		Operations    []map[string]any `json:"operations"`
		Note          string           `json:"note"`
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
	}), WithFacadeRoutedToolAllowlist([]string{"get_sync_status"}))

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
