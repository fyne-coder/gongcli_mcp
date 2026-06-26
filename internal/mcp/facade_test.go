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
		OpEvidenceCallDrilldown,
		OpEvidenceHighlightsList,
		OpEvidenceQuotePackBuild,
		OpEvidenceQuotesSearch,
		OpExtractBuyerQuestions,
		OpExtractObjectionSignals,
		OpProspectQuestionAnswer,
		OpQueryCalls,
		OpQueryScorecardDetail,
		OpQueryScorecards,
		OpQueryTranscriptSegments,
		OpQuestionAnswer,
		OpStatusSync,
		OpThemeIntelReport,
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

func TestFacadeDispatchSchemasAdvertiseRegisteredOperations(t *testing.T) {
	t.Parallel()

	dispatchEnums := map[string][]string{}
	for _, tl := range facadeTools(LimitPolicy{}) {
		props, _ := tl.InputSchema["properties"].(map[string]any)
		opSchema, _ := props["operation"].(map[string]any)
		enum, _ := opSchema["enum"].([]string)
		if len(enum) > 0 {
			dispatchEnums[tl.Name] = enum
		}
	}

	for _, op := range FacadeOperations() {
		enum, ok := dispatchEnums[op.FacadeTool]
		if !ok {
			continue
		}
		if !containsString(enum, op.Name) {
			t.Fatalf("%s schema enum missing registered operation %q: %v", op.FacadeTool, op.Name, enum)
		}
	}
}

func TestFacadeProspectQuestionAnswerOperationRegistered(t *testing.T) {
	t.Parallel()

	op, ok := FacadeOperationByName(OpProspectQuestionAnswer)
	if !ok {
		t.Fatalf("FacadeOperationByName(%q) not registered", OpProspectQuestionAnswer)
	}
	if op.FacadeTool != FacadeToolAnalyze {
		t.Fatalf("prospect question facade_tool=%q want %s", op.FacadeTool, FacadeToolAnalyze)
	}
	if op.RoutedTool != internalRoutedToolProspectQuestionAnswer {
		t.Fatalf("prospect question routed_tool=%q want %s", op.RoutedTool, internalRoutedToolProspectQuestionAnswer)
	}
	props, _ := op.InputSchema["properties"].(map[string]any)
	for _, want := range []string{"question", "filter", "include_account_names"} {
		if _, ok := props[want]; !ok {
			t.Fatalf("prospect question schema missing %q in %+v", want, props)
		}
	}
	tools := facadeTools(LimitPolicy{})
	var analyze tool
	for _, tl := range tools {
		if tl.Name == FacadeToolAnalyze {
			analyze = tl
			break
		}
	}
	if analyze.Name == "" {
		t.Fatalf("facade tools missing %s", FacadeToolAnalyze)
	}
	enumProps, _ := analyze.InputSchema["properties"].(map[string]any)
	opSchema, _ := enumProps["operation"].(map[string]any)
	enum, _ := opSchema["enum"].([]string)
	have := false
	for _, name := range enum {
		if name == OpProspectQuestionAnswer {
			have = true
			break
		}
	}
	if !have {
		t.Fatalf("gong_analyze operation enum missing %q: %v", OpProspectQuestionAnswer, enum)
	}
}

func TestFacadeQuotePackSchemaAdvertisesFieldProfile(t *testing.T) {
	t.Parallel()

	op, ok := FacadeOperationByName(OpEvidenceQuotePackBuild)
	if !ok {
		t.Fatalf("missing operation %s", OpEvidenceQuotePackBuild)
	}
	props, _ := op.InputSchema["properties"].(map[string]any)
	if _, ok := props["field_profile"]; !ok {
		t.Fatalf("quote_pack schema should advertise field_profile; props=%v", props)
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
		FacadeVersion    string            `json:"facade_version"`
		FacadeTools      []string          `json:"facade_tools"`
		Operations       []map[string]any  `json:"operations"`
		MCPServer        PublicRuntimeInfo `json:"mcp_server"`
		DimensionFilters struct {
			SupportedDimensions      []string            `json:"supported_dimensions"`
			OperatorsByDimension     map[string][]string `json:"operators_by_dimension"`
			DisallowedDimensions     []string            `json:"disallowed_dimensions"`
			DefaultDisallowListEmpty bool                `json:"default_disallow_list_empty"`
		} `json:"dimension_filters"`
		Note string `json:"note"`
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
	if !payload.DimensionFilters.DefaultDisallowListEmpty || len(payload.DimensionFilters.DisallowedDimensions) != 0 {
		t.Fatalf("dimension filter contract should advertise empty default disallow list: %+v", payload.DimensionFilters)
	}
	if !containsString(payload.DimensionFilters.SupportedDimensions, "participant_email") || !containsString(payload.DimensionFilters.SupportedDimensions, "duration_seconds") {
		t.Fatalf("dimension filter contract missing expected dimensions: %+v", payload.DimensionFilters.SupportedDimensions)
	}
	if operators := strings.Join(payload.DimensionFilters.OperatorsByDimension["duration_seconds"], ","); !strings.Contains(operators, "between") {
		t.Fatalf("duration_seconds operators should include between: %v", payload.DimensionFilters.OperatorsByDimension["duration_seconds"])
	}
	if operators := strings.Join(payload.DimensionFilters.OperatorsByDimension["participant_email"], ","); operators != "equals,in" {
		t.Fatalf("participant_email operators=%q want equals,in", operators)
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
	if _, ok := inner["call_facts_attribution"]; !ok {
		t.Fatalf("inner result missing call_facts_attribution: %v", inner)
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
	if got, _ := first["call_title"].(string); got == "" {
		t.Fatalf("question.answer should expose call title by default: %v", first)
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

// fakeDrilldownStore wraps the seeded sqlite store and overrides AI highlight
// and call-scoped transcript evidence lookups so call-drilldown facade tests
// can exercise both branches without standing up Postgres.
type fakeDrilldownStore struct {
	*sqlite.Store
	highlights         map[string][]sqlite.AIHighlightRow
	transcriptByCallID map[string][]sqlite.CallDrilldownEvidenceRow
	refs               map[string]string
	lastTranscript     sqlite.CallDrilldownEvidenceParams
}

func (f *fakeDrilldownStore) ListAIHighlights(_ context.Context, params sqlite.AIHighlightListParams) ([]sqlite.AIHighlightRow, error) {
	out := make([]sqlite.AIHighlightRow, 0)
	for _, id := range params.CallIDs {
		out = append(out, f.highlights[id]...)
	}
	return out, nil
}

func (f *fakeDrilldownStore) CallDrilldownEvidence(_ context.Context, params sqlite.CallDrilldownEvidenceParams) ([]sqlite.CallDrilldownEvidenceRow, error) {
	f.lastTranscript = params
	rows := f.transcriptByCallID[params.CallID]
	if strings.TrimSpace(params.Query) == "" {
		return nil, nil
	}
	out := make([]sqlite.CallDrilldownEvidenceRow, 0, len(rows))
	q := strings.ToLower(strings.TrimSpace(params.Query))
	for _, row := range rows {
		if q == "" || strings.Contains(strings.ToLower(row.Snippet), q) {
			out = append(out, row)
		}
	}
	return out, nil
}

func (f *fakeDrilldownStore) ResolveCallIDByRef(ctx context.Context, ref string) (string, error) {
	normalized, err := sqlite.NormalizeStableCallRef(ref)
	if err != nil {
		return "", err
	}
	if id, ok := f.refs[normalized]; ok {
		return id, nil
	}
	return f.Store.ResolveCallIDByRef(ctx, ref)
}

func TestFacadeProspectQuestionAnswerBriefsFirstThenTranscriptEvidence(t *testing.T) {
	t.Parallel()

	base := openSeededStore(t)
	defer base.Close()
	ctx := context.Background()

	const callID = "call_prospect_question_001"
	if _, err := base.UpsertCall(ctx, mustJSON(t, map[string]any{
		"id":        callID,
		"title":     "Business Discovery prospect question",
		"started":   "2026-02-18T15:00:00Z",
		"duration":  1800,
		"system":    "Zoom",
		"direction": "Conference",
		"scope":     "External",
		"context": []any{
			map[string]any{
				"objectType": "Account",
				"id":         "acct_prospect_question",
				"name":       "Prospect Question Account",
				"fields": []any{
					map[string]any{"name": "Industry", "value": "Manufacturing"},
				},
			},
			map[string]any{
				"objectType": "Opportunity",
				"id":         "opp_prospect_question",
				"name":       "Prospect Question Opportunity",
				"fields": []any{
					map[string]any{"name": "StageName", "value": "Discovery"},
					map[string]any{"name": "Type", "value": "New Business"},
				},
			},
		},
	})); err != nil {
		t.Fatalf("upsert prospect call: %v", err)
	}
	if _, err := base.UpsertTranscript(ctx, mustJSON(t, map[string]any{
		"callTranscripts": []any{
			map[string]any{
				"callId": callID,
				"transcript": []any{
					map[string]any{
						"speakerId": "buyer",
						"sentences": []any{
							map[string]any{"start": 1000, "end": 2500, "text": "The manual process creates implementation risk for this prospect."},
						},
					},
				},
			},
		},
	})); err != nil {
		t.Fatalf("upsert prospect transcript: %v", err)
	}
	callRef := sqlite.StableCallRef(callID)
	store := &fakeDrilldownStore{
		Store: base,
		refs:  map[string]string{callRef: callID},
		highlights: map[string][]sqlite.AIHighlightRow{
			callID: {
				{CallID: callID, HighlightIndex: 0, HighlightType: "brief", HighlightText: "Gong AI brief: manual process and implementation effort were central.", SourcePath: "content.brief", UpdatedAt: "2026-02-18T16:00:00Z"},
			},
		},
	}
	server := NewServerWithOptions(store, "gongmcp", "test",
		WithToolAllowlist(FacadeToolNames()),
		WithFacadeRoutedToolAllowlist([]string{internalRoutedToolProspectQuestionAnswer}),
		WithPolicySwitches(PolicySwitches{HideRawCallIDs: true}),
	)

	args, err := json.Marshal(map[string]any{
		"operation": OpProspectQuestionAnswer,
		"arguments": map[string]any{
			"question":              "What is this prospect saying about manual process and implementation?",
			"query":                 "manual process",
			"filter":                map[string]any{"account_query": "Prospect Question Account", "quarter": "2026-Q1", "transcript_status": "present"},
			"include_account_names": true,
			"field_profile":         "limited",
			"limit":                 5,
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	result, err := server.executeFacadeDispatch(ctx, FacadeToolAnalyze, args)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected isError: %+v", result)
	}
	wrapper := decodeFacadeWrapper(t, result)
	inner, _ := wrapper["result"].(map[string]any)
	if inner["status"] != "prospect_evidence_ready" {
		t.Fatalf("status=%v inner=%+v", inner["status"], inner)
	}
	if got, _ := inner["ai_condensed_evidence_count"].(float64); got != 1 {
		t.Fatalf("ai_condensed_evidence_count=%v want 1 inner=%+v", got, inner)
	}
	if got, _ := inner["transcript_evidence_count"].(float64); got != 1 {
		t.Fatalf("transcript_evidence_count=%v want 1 inner=%+v", got, inner)
	}
	rendered := mustJSONText(t, inner)
	if strings.Contains(rendered, callID) {
		t.Fatalf("prospect question leaked raw call id under HideRawCallIDs: %s", rendered)
	}
	for _, want := range []string{"prospect_question_briefs_first", "account_query_explicit_opt_in", "limited_field_profile_does_not_redact_ai_condensed_evidence_text"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("prospect question missing warning %q: %s", want, rendered)
		}
	}
}

func TestFacadeProspectQuestionAnswerRequiresAccountOptIn(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	server := NewServerWithOptions(store, "gongmcp", "test",
		WithToolAllowlist(FacadeToolNames()),
		WithFacadeRoutedToolAllowlist([]string{internalRoutedToolProspectQuestionAnswer}),
	)
	args, err := json.Marshal(map[string]any{
		"operation": OpProspectQuestionAnswer,
		"arguments": map[string]any{
			"question": "What is this prospect asking about?",
			"filter":   map[string]any{"account_query": "Prospect Question Account", "quarter": "2026-Q1"},
			"limit":    5,
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	_, err = server.executeFacadeDispatch(t.Context(), FacadeToolAnalyze, args)
	if err == nil || !strings.Contains(err.Error(), "include_account_names=true") {
		t.Fatalf("expected include_account_names account_query error, got %v", err)
	}
}

func TestFacadeEvidenceCallDrilldownOperationRegistered(t *testing.T) {
	t.Parallel()

	op, ok := FacadeOperationByName(OpEvidenceCallDrilldown)
	if !ok {
		t.Fatalf("FacadeOperationByName(%q) not registered", OpEvidenceCallDrilldown)
	}
	if op.FacadeTool != FacadeToolGetEvidence {
		t.Fatalf("evidence.call_drilldown facade_tool=%q want %s", op.FacadeTool, FacadeToolGetEvidence)
	}
	if op.RoutedTool != "call_drilldown" {
		t.Fatalf("evidence.call_drilldown routed_tool=%q want call_drilldown", op.RoutedTool)
	}
	if op.InputSchema == nil {
		t.Fatalf("evidence.call_drilldown missing input schema")
	}
	props, _ := op.InputSchema["properties"].(map[string]any)
	for _, want := range []string{"call_ref", "theme_query", "limit"} {
		if _, ok := props[want]; !ok {
			t.Fatalf("evidence.call_drilldown schema missing %q in %+v", want, props)
		}
	}

	tools := facadeTools(LimitPolicy{})
	var evidence tool
	for _, tl := range tools {
		if tl.Name == FacadeToolGetEvidence {
			evidence = tl
			break
		}
	}
	if evidence.Name == "" {
		t.Fatalf("facade tools missing %s", FacadeToolGetEvidence)
	}
	enumProps, _ := evidence.InputSchema["properties"].(map[string]any)
	opSchema, _ := enumProps["operation"].(map[string]any)
	enum, _ := opSchema["enum"].([]string)
	have := false
	for _, name := range enum {
		if name == OpEvidenceCallDrilldown {
			have = true
			break
		}
	}
	if !have {
		t.Fatalf("gong_get_evidence operation enum missing %q: %v", OpEvidenceCallDrilldown, enum)
	}
}

func TestFacadeEvidenceCallDrilldownReturnsHighlightsWithoutTranscriptWhenNoQuery(t *testing.T) {
	t.Parallel()

	base := openSeededStore(t)
	defer base.Close()

	const callID = "call_sanitized_001"
	callRef := sqlite.StableCallRef(callID)
	store := &fakeDrilldownStore{
		Store: base,
		refs:  map[string]string{callRef: callID},
		highlights: map[string][]sqlite.AIHighlightRow{
			callID: {
				{CallID: callID, HighlightIndex: 0, HighlightType: "brief", HighlightText: "Brief generated by Gong AI.", SourcePath: "content.brief", UpdatedAt: "2026-04-25T00:00:00Z"},
				{CallID: callID, HighlightIndex: 1, HighlightType: "key_point", HighlightText: "Key procurement gating point.", SourcePath: "content.keyPoints", UpdatedAt: "2026-04-25T00:00:00Z"},
			},
		},
	}

	server := NewServerWithOptions(store, "gongmcp", "test",
		WithToolAllowlist(FacadeToolNames()),
		WithFacadeRoutedToolAllowlist([]string{"call_drilldown"}),
	)

	args, err := json.Marshal(map[string]any{
		"operation": OpEvidenceCallDrilldown,
		"arguments": map[string]any{
			"call_ref": callRef,
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
	wrapper := decodeFacadeWrapper(t, result)
	if wrapper["operation"] != OpEvidenceCallDrilldown || wrapper["routed_tool"] != "call_drilldown" {
		t.Fatalf("unexpected facade wrapper: %v", wrapper)
	}
	inner, ok := wrapper["result"].(map[string]any)
	if !ok {
		t.Fatalf("inner result type=%T", wrapper["result"])
	}
	if inner["operation"] != OpEvidenceCallDrilldown {
		t.Fatalf("inner operation=%v want %s", inner["operation"], OpEvidenceCallDrilldown)
	}
	status, _ := inner["drilldown_status"].(string)
	if status != "ready" {
		t.Fatalf("drilldown_status=%q want ready: %v", status, inner)
	}
	call, _ := inner["call"].(map[string]any)
	if call == nil {
		t.Fatalf("missing call object: %v", inner)
	}
	if got, _ := call["call_ref"].(string); got != callRef {
		t.Fatalf("call_ref=%q want %q", got, callRef)
	}
	if _, hasRaw := call["call_id"]; hasRaw {
		t.Fatalf("call exposed raw call_id without include_raw_ids: %v", call)
	}
	ai, _ := inner["ai_condensed_evidence"].([]any)
	if len(ai) != 2 {
		t.Fatalf("ai_condensed_evidence len=%d want 2: %v", len(ai), inner)
	}
	first, _ := ai[0].(map[string]any)
	for _, want := range []string{"highlight_type", "highlight_text", "highlight_index", "source_path"} {
		if _, ok := first[want]; !ok {
			t.Fatalf("ai_condensed_evidence row missing %q: %v", want, first)
		}
	}
	verbatim, _ := inner["verbatim_transcript_excerpts"].([]any)
	if len(verbatim) != 0 {
		t.Fatalf("verbatim_transcript_excerpts must be empty without theme_query: %v", verbatim)
	}
	warnings, _ := inner["warnings"].([]any)
	hasNoQuoteWarning := false
	for _, w := range warnings {
		if strings.Contains(fmt.Sprintf("%v", w), "no_transcript_quotes") || strings.Contains(fmt.Sprintf("%v", w), "no_theme_query") {
			hasNoQuoteWarning = true
		}
	}
	if !hasNoQuoteWarning {
		t.Fatalf("expected warning about absent theme_query/quote evidence, got %v", warnings)
	}
	limitations, _ := inner["limitations"].([]any)
	hasInstructionsCaveat := false
	for _, l := range limitations {
		if strings.Contains(strings.ToLower(fmt.Sprintf("%v", l)), "instruction") || strings.Contains(strings.ToLower(fmt.Sprintf("%v", l)), "evidence text") {
			hasInstructionsCaveat = true
		}
	}
	if !hasInstructionsCaveat {
		t.Fatalf("limitations missing evidence-text/not-instructions caveat: %v", limitations)
	}
}

func TestFacadeEvidenceCallDrilldownReturnsHighlightsAndTranscriptForTheme(t *testing.T) {
	t.Parallel()

	base := openSeededStore(t)
	defer base.Close()

	const callID = "call_sanitized_001"
	callRef := sqlite.StableCallRef(callID)
	store := &fakeDrilldownStore{
		Store: base,
		refs:  map[string]string{callRef: callID},
		highlights: map[string][]sqlite.AIHighlightRow{
			callID: {
				{CallID: callID, HighlightIndex: 0, HighlightType: "brief", HighlightText: "Synthetic brief.", SourcePath: "content.brief", UpdatedAt: "2026-04-25T00:00:00Z"},
			},
		},
		transcriptByCallID: map[string][]sqlite.CallDrilldownEvidenceRow{
			callID: {
				{CallID: callID, SegmentIndex: 0, SpeakerID: "speaker_internal_001", StartMS: 1000, EndMS: 3500, Snippet: "Synthetic internal speaker sentence.", ContextExcerpt: "Synthetic internal speaker sentence."},
				{CallID: callID, SegmentIndex: 1, SpeakerID: "speaker_external_001", StartMS: 3600, EndMS: 7000, Snippet: "Synthetic external speaker sentence.", ContextExcerpt: "Synthetic external speaker sentence."},
			},
		},
	}

	server := NewServerWithOptions(store, "gongmcp", "test",
		WithToolAllowlist(FacadeToolNames()),
		WithFacadeRoutedToolAllowlist([]string{"call_drilldown"}),
	)

	args, err := json.Marshal(map[string]any{
		"operation": OpEvidenceCallDrilldown,
		"arguments": map[string]any{
			"call_ref":    callRef,
			"theme_query": "Synthetic",
			"limit":       5,
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
	inner, ok := wrapper["result"].(map[string]any)
	if !ok {
		t.Fatalf("inner result type=%T", wrapper["result"])
	}
	if inner["drilldown_status"] != "ready" {
		t.Fatalf("drilldown_status=%v want ready: %v", inner["drilldown_status"], inner)
	}
	if store.lastTranscript.CallID != callID {
		t.Fatalf("CallDrilldownEvidence not scoped to callID; got %q want %q", store.lastTranscript.CallID, callID)
	}
	if strings.TrimSpace(store.lastTranscript.Query) == "" {
		t.Fatalf("CallDrilldownEvidence query empty; want propagated theme_query")
	}
	verbatim, _ := inner["verbatim_transcript_excerpts"].([]any)
	if len(verbatim) == 0 {
		t.Fatalf("expected verbatim_transcript_excerpts to be populated: %v", inner)
	}
	for _, row := range verbatim {
		r, _ := row.(map[string]any)
		if got, _ := r["call_ref"].(string); got != callRef {
			t.Fatalf("verbatim row call_ref=%q want %q: %v", got, callRef, r)
		}
		if _, ok := r["call_id"]; ok {
			t.Fatalf("verbatim row leaked raw call_id: %v", r)
		}
		for _, key := range []string{"snippet", "segment_index", "start_ms", "end_ms"} {
			if _, ok := r[key]; !ok {
				t.Fatalf("verbatim row missing %q: %v", key, r)
			}
		}
	}
	ai, _ := inner["ai_condensed_evidence"].([]any)
	if len(ai) == 0 {
		t.Fatalf("expected ai_condensed_evidence: %v", inner)
	}
	coverage, _ := inner["coverage_markers"].(map[string]any)
	if coverage == nil {
		t.Fatalf("missing coverage_markers: %v", inner)
	}
}

func TestFacadeEvidenceCallDrilldownUnknownCallRefReturnsTypedNotFound(t *testing.T) {
	t.Parallel()

	base := openSeededStore(t)
	defer base.Close()

	store := &fakeDrilldownStore{Store: base}
	server := NewServerWithOptions(store, "gongmcp", "test",
		WithToolAllowlist(FacadeToolNames()),
		WithFacadeRoutedToolAllowlist([]string{"call_drilldown"}),
	)

	args, err := json.Marshal(map[string]any{
		"operation": OpEvidenceCallDrilldown,
		"arguments": map[string]any{
			"call_ref": "call_ref_doesnotexist0123456789ab",
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
		t.Fatalf("expected non-error result with typed call_not_found, got %+v", result)
	}
	wrapper := decodeFacadeWrapper(t, result)
	inner, ok := wrapper["result"].(map[string]any)
	if !ok {
		t.Fatalf("inner result type=%T", wrapper["result"])
	}
	if inner["drilldown_status"] != "call_not_found" {
		t.Fatalf("drilldown_status=%v want call_not_found: %v", inner["drilldown_status"], inner)
	}
	rendered := strings.ToLower(result.Content[0].Text)
	for _, leaked := range []string{"call_sanitized_001", "call_extended_001"} {
		if strings.Contains(rendered, strings.ToLower(leaked)) {
			t.Fatalf("call_not_found response leaked existing call identifier %q: %s", leaked, rendered)
		}
	}
	if _, hasCall := inner["call"]; hasCall {
		t.Fatalf("call object must be omitted/empty for call_not_found: %v", inner)
	}
}

func TestFacadeEvidenceCallDrilldownPolicySwitchesHideRawIDsAndTitles(t *testing.T) {
	t.Parallel()

	base := openSeededStore(t)
	defer base.Close()

	const callID = "call_sanitized_001"
	callRef := sqlite.StableCallRef(callID)
	store := &fakeDrilldownStore{
		Store: base,
		refs:  map[string]string{callRef: callID},
		highlights: map[string][]sqlite.AIHighlightRow{
			callID: {
				{CallID: callID, HighlightIndex: 0, HighlightType: "brief", HighlightText: "Brief.", SourcePath: "content.brief", UpdatedAt: "2026-04-25T00:00:00Z"},
			},
		},
		transcriptByCallID: map[string][]sqlite.CallDrilldownEvidenceRow{
			callID: {
				{CallID: callID, SegmentIndex: 0, SpeakerID: "speaker_internal_001", StartMS: 1000, EndMS: 3500, Snippet: "Synthetic internal speaker sentence.", ContextExcerpt: "Synthetic internal speaker sentence."},
			},
		},
	}

	server := NewServerWithOptions(store, "gongmcp", "test",
		WithToolAllowlist(FacadeToolNames()),
		WithFacadeRoutedToolAllowlist([]string{"call_drilldown"}),
		WithPolicySwitches(PolicySwitches{HideRawCallIDs: true, HideCallTitles: true}),
	)

	args, err := json.Marshal(map[string]any{
		"operation": OpEvidenceCallDrilldown,
		"arguments": map[string]any{
			"call_ref":            callRef,
			"theme_query":         "Synthetic",
			"include_raw_ids":     true,
			"include_call_titles": true,
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
	rendered := result.Content[0].Text
	if strings.Contains(rendered, callID) {
		t.Fatalf("HideRawCallIDs policy did not suppress raw call_id; rendered output contained %q: %s", callID, rendered)
	}
	if strings.Contains(rendered, "Sanitized discovery call") {
		t.Fatalf("HideCallTitles policy did not suppress raw call title; rendered output contained title: %s", rendered)
	}
	wrapper := decodeFacadeWrapper(t, result)
	inner, _ := wrapper["result"].(map[string]any)
	call, _ := inner["call"].(map[string]any)
	if got, _ := call["call_id"].(string); got != "" {
		t.Fatalf("call.call_id=%q want suppressed by HideRawCallIDs", got)
	}
	if got, _ := call["call_title"].(string); got != "" {
		t.Fatalf("call.call_title=%q want suppressed by HideCallTitles", got)
	}
}

// TestFacadeEvidenceCallDrilldownEmitsSpeakerAttribution asserts the Phase
// B-1 read-time speaker/title attribution contract: every drilldown
// transcript excerpt must carry a stable speaker_ref alias (even when raw
// speaker IDs are hidden by policy), an explicit person_title_status,
// person_title_source, attribution_source and attribution_confidence string,
// and the limitations block must mention that attribution is exact
// Gong-party only. Raw person_title text must NOT leak by default.
func TestFacadeEvidenceCallDrilldownEmitsSpeakerAttribution(t *testing.T) {
	t.Parallel()

	base := openSeededStore(t)
	defer base.Close()

	const callID = "call_sanitized_001"
	callRef := sqlite.StableCallRef(callID)
	store := &fakeDrilldownStore{
		Store: base,
		refs:  map[string]string{callRef: callID},
		highlights: map[string][]sqlite.AIHighlightRow{
			callID: {
				{CallID: callID, HighlightIndex: 0, HighlightType: "brief", HighlightText: "Brief.", SourcePath: "content.brief", UpdatedAt: "2026-04-25T00:00:00Z"},
			},
		},
		transcriptByCallID: map[string][]sqlite.CallDrilldownEvidenceRow{
			callID: {
				{
					CallID:                callID,
					SegmentIndex:          0,
					SpeakerID:             "speaker_internal_001",
					StartMS:               1000,
					EndMS:                 3500,
					Snippet:               "Synthetic internal speaker sentence.",
					ContextExcerpt:        "Synthetic internal speaker sentence.",
					PersonTitleStatus:     "available",
					PersonTitleSource:     "call_parties",
					AttributionSource:     "gong_party",
					AttributionConfidence: "exact_speaker_id",
					PersonTitle:           "VP Procurement",
				},
				{
					CallID:                callID,
					SegmentIndex:          1,
					SpeakerID:             "speaker_unknown_999",
					StartMS:               4000,
					EndMS:                 5500,
					Snippet:               "Synthetic unknown speaker sentence.",
					ContextExcerpt:        "Synthetic unknown speaker sentence.",
					PersonTitleStatus:     "speaker_unmatched",
					PersonTitleSource:     "",
					AttributionSource:     "unmatched",
					AttributionConfidence: "unmatched",
				},
			},
		},
	}

	server := NewServerWithOptions(store, "gongmcp", "test",
		WithToolAllowlist(FacadeToolNames()),
		WithFacadeRoutedToolAllowlist([]string{"call_drilldown"}),
	)

	args, err := json.Marshal(map[string]any{
		"operation": OpEvidenceCallDrilldown,
		"arguments": map[string]any{
			"call_ref":    callRef,
			"theme_query": "Synthetic",
			"limit":       5,
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
	inner, _ := wrapper["result"].(map[string]any)
	verbatim, _ := inner["verbatim_transcript_excerpts"].([]any)
	if len(verbatim) != 2 {
		t.Fatalf("verbatim_transcript_excerpts len=%d want 2: %v", len(verbatim), inner)
	}

	matched, _ := verbatim[0].(map[string]any)
	for _, want := range []string{"speaker_ref", "person_title_status", "person_title_source", "attribution_source", "attribution_confidence"} {
		if _, ok := matched[want]; !ok {
			t.Fatalf("transcript row missing attribution field %q: %v", want, matched)
		}
	}
	if got, _ := matched["person_title_status"].(string); got != "available" {
		t.Fatalf("matched person_title_status=%q want available", got)
	}
	if got, _ := matched["person_title_source"].(string); got != "call_parties" {
		t.Fatalf("matched person_title_source=%q want call_parties", got)
	}
	if got, _ := matched["attribution_source"].(string); got != "gong_party" {
		t.Fatalf("matched attribution_source=%q want gong_party", got)
	}
	if got, _ := matched["attribution_confidence"].(string); got != "exact_speaker_id" {
		t.Fatalf("matched attribution_confidence=%q want exact_speaker_id", got)
	}
	if got, _ := matched["speaker_ref"].(string); !strings.HasPrefix(got, "speaker_") || len(got) < len("speaker_")+8 {
		t.Fatalf("matched speaker_ref=%q expected stable speaker_<hex> alias", got)
	}
	if got, ok := matched["person_title"].(string); ok && got != "" {
		t.Fatalf("matched row leaked raw person_title=%q with default include flag off", got)
	}

	unmatched, _ := verbatim[1].(map[string]any)
	if got, _ := unmatched["person_title_status"].(string); got != "speaker_unmatched" {
		t.Fatalf("unmatched person_title_status=%q want speaker_unmatched", got)
	}
	if got, _ := unmatched["attribution_source"].(string); got != "unmatched" {
		t.Fatalf("unmatched attribution_source=%q want unmatched", got)
	}
	if got, _ := unmatched["attribution_confidence"].(string); got != "unmatched" {
		t.Fatalf("unmatched attribution_confidence=%q want unmatched", got)
	}

	limitations, _ := inner["limitations"].([]any)
	hasGongPartyOnly := false
	hasUnprovenRole := false
	for _, l := range limitations {
		text := strings.ToLower(fmt.Sprintf("%v", l))
		if strings.Contains(text, "gong_party") || strings.Contains(text, "exact_gong_party") || strings.Contains(text, "exact_speaker_id") {
			hasGongPartyOnly = true
		}
		if strings.Contains(text, "buyer_versus_rep_role_is_not_proven") {
			hasUnprovenRole = true
		}
	}
	if !hasGongPartyOnly {
		t.Fatalf("limitations missing exact-Gong-party caveat: %v", limitations)
	}
	if !hasUnprovenRole {
		t.Fatalf("limitations should retain buyer-vs-rep caveat when any transcript row lacks speaker_role_status=available: %v", limitations)
	}
}

// TestFacadeEvidenceCallDrilldownAttributionRespectsHideSpeakerIDs verifies
// that when HideSpeakerIDs is enabled the raw speaker_id is suppressed but
// the stable speaker_ref alias and attribution status fields still surface.
func TestFacadeEvidenceCallDrilldownAttributionRespectsHideSpeakerIDs(t *testing.T) {
	t.Parallel()

	base := openSeededStore(t)
	defer base.Close()

	const callID = "call_sanitized_001"
	callRef := sqlite.StableCallRef(callID)
	store := &fakeDrilldownStore{
		Store: base,
		refs:  map[string]string{callRef: callID},
		transcriptByCallID: map[string][]sqlite.CallDrilldownEvidenceRow{
			callID: {
				{
					CallID:                callID,
					SegmentIndex:          0,
					SpeakerID:             "speaker_internal_001",
					StartMS:               1000,
					EndMS:                 3500,
					Snippet:               "Synthetic internal speaker sentence.",
					ContextExcerpt:        "Synthetic internal speaker sentence.",
					PersonTitleStatus:     "participant_matched_title_missing",
					PersonTitleSource:     "call_parties",
					AttributionSource:     "gong_party",
					AttributionConfidence: "exact_speaker_id",
				},
			},
		},
	}

	server := NewServerWithOptions(store, "gongmcp", "test",
		WithToolAllowlist(FacadeToolNames()),
		WithFacadeRoutedToolAllowlist([]string{"call_drilldown"}),
		WithPolicySwitches(PolicySwitches{HideSpeakerIDs: true}),
	)

	args, err := json.Marshal(map[string]any{
		"operation": OpEvidenceCallDrilldown,
		"arguments": map[string]any{
			"call_ref":    callRef,
			"theme_query": "Synthetic",
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

	rendered := result.Content[0].Text
	if strings.Contains(rendered, "speaker_internal_001") {
		t.Fatalf("HideSpeakerIDs failed to suppress raw speaker_id: %s", rendered)
	}

	wrapper := decodeFacadeWrapper(t, result)
	inner, _ := wrapper["result"].(map[string]any)
	verbatim, _ := inner["verbatim_transcript_excerpts"].([]any)
	if len(verbatim) != 1 {
		t.Fatalf("verbatim len=%d want 1: %v", len(verbatim), inner)
	}
	row, _ := verbatim[0].(map[string]any)
	if got, _ := row["speaker_id"].(string); got != "" {
		t.Fatalf("row leaked raw speaker_id=%q under HideSpeakerIDs", got)
	}
	speakerRef, _ := row["speaker_ref"].(string)
	if !strings.HasPrefix(speakerRef, "speaker_") || len(speakerRef) < len("speaker_")+8 {
		t.Fatalf("row missing stable speaker_ref under HideSpeakerIDs: %v", row)
	}
	if got, _ := row["person_title_status"].(string); got != "participant_matched_title_missing" {
		t.Fatalf("person_title_status=%q want participant_matched_title_missing", got)
	}
}

func TestFacadeEvidenceCallDrilldownLimitedProfileSuppressesSpeakerRef(t *testing.T) {
	t.Parallel()

	base := openSeededStore(t)
	defer base.Close()

	const callID = "call_sanitized_001"
	callRef := sqlite.StableCallRef(callID)
	store := &fakeDrilldownStore{
		Store: base,
		refs:  map[string]string{callRef: callID},
		highlights: map[string][]sqlite.AIHighlightRow{
			callID: {
				{CallID: callID, HighlightIndex: 0, HighlightType: "brief", HighlightText: "Brief names Buyer Co and Hannah.", SourcePath: "content.brief", UpdatedAt: "2026-04-25T00:00:00Z"},
			},
		},
		transcriptByCallID: map[string][]sqlite.CallDrilldownEvidenceRow{
			callID: {
				{
					CallID:                callID,
					SegmentIndex:          0,
					SpeakerID:             "speaker_internal_001",
					StartMS:               1000,
					EndMS:                 3500,
					Snippet:               "Synthetic internal speaker sentence.",
					ContextExcerpt:        "Synthetic internal speaker sentence.",
					PersonTitleStatus:     "available",
					PersonTitleSource:     "call_parties",
					AttributionSource:     "gong_party",
					AttributionConfidence: "exact_speaker_id",
				},
			},
		},
	}

	server := NewServerWithOptions(store, "gongmcp", "test",
		WithToolAllowlist(FacadeToolNames()),
		WithFacadeRoutedToolAllowlist([]string{"call_drilldown"}),
	)

	args, err := json.Marshal(map[string]any{
		"operation": OpEvidenceCallDrilldown,
		"arguments": map[string]any{
			"call_ref":      callRef,
			"theme_query":   "Synthetic",
			"field_profile": "limited",
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
	inner, _ := wrapper["result"].(map[string]any)
	verbatim, _ := inner["verbatim_transcript_excerpts"].([]any)
	if len(verbatim) != 1 {
		t.Fatalf("verbatim len=%d want 1: %v", len(verbatim), inner)
	}
	row, _ := verbatim[0].(map[string]any)
	if got, _ := row["speaker_ref"].(string); got != "" {
		t.Fatalf("limited field_profile leaked speaker_ref=%q row=%v", got, row)
	}
	warnings := strings.ToLower(mustJSONText(t, inner["warnings"]))
	if !strings.Contains(warnings, "limited_field_profile_does_not_redact_ai_condensed_evidence_text") {
		t.Fatalf("limited field_profile should warn AI evidence text is not redacted: %s", warnings)
	}
	if got, _ := inner["evidence_type"].(string); got != evidenceTypeTranscriptQuote {
		t.Fatalf("evidence_type=%q want %s: %v", got, evidenceTypeTranscriptQuote, inner)
	}
	if _, ok := inner["evidence_policy"].(map[string]any); !ok {
		t.Fatalf("call drilldown should include evidence_policy: %v", inner)
	}
	if summary, _ := inner["speaker_attribution_summary"].(map[string]any); summary["external"] == nil {
		t.Fatalf("call drilldown should include speaker_attribution_summary: %v", summary)
	}
}

func TestFacadeEvidenceCallDrilldownFieldProfileSpeakerIDPolicyMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		fieldProfile     string
		hideSpeakerIDs   bool
		wantSpeakerRef   bool
		wantRawSpeakerID bool
	}{
		{name: "limited policy allows raw speaker id", fieldProfile: "limited", wantRawSpeakerID: true},
		{name: "limited policy hides raw speaker id", fieldProfile: "limited", hideSpeakerIDs: true},
		{name: "attribution policy allows ref and raw speaker id", fieldProfile: "attribution", wantSpeakerRef: true, wantRawSpeakerID: true},
		{name: "attribution policy hides raw speaker id but keeps ref", fieldProfile: "attribution", hideSpeakerIDs: true, wantSpeakerRef: true},
		{name: "full policy allows ref and raw speaker id", fieldProfile: "full", wantSpeakerRef: true, wantRawSpeakerID: true},
		{name: "full policy hides raw speaker id but keeps ref", fieldProfile: "full", hideSpeakerIDs: true, wantSpeakerRef: true},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			base := openSeededStore(t)
			defer base.Close()

			const callID = "call_sanitized_001"
			callRef := sqlite.StableCallRef(callID)
			store := &fakeDrilldownStore{
				Store: base,
				refs:  map[string]string{callRef: callID},
				transcriptByCallID: map[string][]sqlite.CallDrilldownEvidenceRow{
					callID: {
						{
							CallID:                callID,
							SegmentIndex:          0,
							SpeakerID:             "speaker_policy_001",
							StartMS:               1000,
							EndMS:                 3500,
							Snippet:               "Synthetic external speaker policy sentence.",
							ContextExcerpt:        "Synthetic external speaker policy sentence.",
							SpeakerRole:           sqlite.SpeakerRoleExternal,
							SpeakerRoleStatus:     sqlite.SpeakerRoleStatusAvailable,
							PersonTitleStatus:     "available",
							PersonTitleSource:     "call_parties",
							AttributionSource:     "gong_party",
							AttributionConfidence: "exact_speaker_id",
						},
					},
				},
			}

			server := NewServerWithOptions(store, "gongmcp", "test",
				WithToolAllowlist(FacadeToolNames()),
				WithFacadeRoutedToolAllowlist([]string{"call_drilldown"}),
				WithPolicySwitches(PolicySwitches{HideSpeakerIDs: tc.hideSpeakerIDs}),
			)

			args, err := json.Marshal(map[string]any{
				"operation": OpEvidenceCallDrilldown,
				"arguments": map[string]any{
					"call_ref":      callRef,
					"theme_query":   "Synthetic",
					"field_profile": tc.fieldProfile,
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
			inner, _ := wrapper["result"].(map[string]any)
			verbatim, _ := inner["verbatim_transcript_excerpts"].([]any)
			if len(verbatim) != 1 {
				t.Fatalf("verbatim len=%d want 1: %v", len(verbatim), inner)
			}
			row, _ := verbatim[0].(map[string]any)

			speakerRef, _ := row["speaker_ref"].(string)
			if tc.wantSpeakerRef && !strings.HasPrefix(speakerRef, "speaker_") {
				t.Fatalf("speaker_ref=%q want stable ref for profile %s row=%v", speakerRef, tc.fieldProfile, row)
			}
			if !tc.wantSpeakerRef && speakerRef != "" {
				t.Fatalf("speaker_ref=%q should be suppressed for profile %s row=%v", speakerRef, tc.fieldProfile, row)
			}

			speakerID, _ := row["speaker_id"].(string)
			if tc.wantRawSpeakerID && speakerID != "speaker_policy_001" {
				t.Fatalf("speaker_id=%q want raw speaker id when policy allows it row=%v", speakerID, row)
			}
			if !tc.wantRawSpeakerID && speakerID != "" {
				t.Fatalf("speaker_id=%q should be hidden by policy row=%v", speakerID, row)
			}
		})
	}
}

func TestFacadeEvidenceCallDrilldownOmitsUnprovenRoleLimitationWhenSpeakerRoleAvailable(t *testing.T) {
	t.Parallel()

	base := openSeededStore(t)
	defer base.Close()

	const callID = "call_sanitized_001"
	callRef := sqlite.StableCallRef(callID)
	store := &fakeDrilldownStore{
		Store: base,
		refs:  map[string]string{callRef: callID},
		transcriptByCallID: map[string][]sqlite.CallDrilldownEvidenceRow{
			callID: {
				{
					CallID:                callID,
					SegmentIndex:          0,
					SpeakerID:             "speaker_external_001",
					StartMS:               1000,
					EndMS:                 3500,
					Snippet:               "Synthetic external speaker sentence.",
					ContextExcerpt:        "Synthetic external speaker sentence.",
					PersonTitleStatus:     "available",
					PersonTitleSource:     "call_parties",
					AttributionSource:     "gong_party",
					AttributionConfidence: "exact_speaker_id",
					SpeakerRole:           sqlite.SpeakerRoleExternal,
					SpeakerRoleStatus:     sqlite.SpeakerRoleStatusAvailable,
				},
			},
		},
	}

	server := NewServerWithOptions(store, "gongmcp", "test",
		WithToolAllowlist(FacadeToolNames()),
		WithFacadeRoutedToolAllowlist([]string{"call_drilldown"}),
	)

	args, err := json.Marshal(map[string]any{
		"operation": OpEvidenceCallDrilldown,
		"arguments": map[string]any{
			"call_ref":    callRef,
			"theme_query": "Synthetic",
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
	inner, _ := wrapper["result"].(map[string]any)
	verbatim, _ := inner["verbatim_transcript_excerpts"].([]any)
	if len(verbatim) != 1 {
		t.Fatalf("verbatim len=%d want 1: %v", len(verbatim), inner)
	}
	row, _ := verbatim[0].(map[string]any)
	if got, _ := row["speaker_role"].(string); got != sqlite.SpeakerRoleExternal {
		t.Fatalf("speaker_role=%q want external; row=%v", got, row)
	}
	if got, _ := row["speaker_role_status"].(string); got != sqlite.SpeakerRoleStatusAvailable {
		t.Fatalf("speaker_role_status=%q want available; row=%v", got, row)
	}
	if got, _ := row["is_internal_speaker"].(bool); got {
		t.Fatalf("is_internal_speaker=true want false for external row: %v", row)
	}
	limitations, _ := inner["limitations"].([]any)
	for _, l := range limitations {
		if strings.Contains(fmt.Sprintf("%v", l), "buyer_versus_rep_role_is_not_proven") {
			t.Fatalf("limitations should not claim buyer-vs-rep role is unproven when speaker_role_status is available: %v", limitations)
		}
	}
}

func TestFacadeEvidenceCallDrilldownPersonTitleRequiresExplicitOptInAndPolicy(t *testing.T) {
	t.Parallel()

	base := openSeededStore(t)
	defer base.Close()

	const callID = "call_sanitized_001"
	callRef := sqlite.StableCallRef(callID)
	store := &fakeDrilldownStore{
		Store: base,
		refs:  map[string]string{callRef: callID},
		transcriptByCallID: map[string][]sqlite.CallDrilldownEvidenceRow{
			callID: {
				{
					CallID:                callID,
					SegmentIndex:          0,
					SpeakerID:             "speaker_internal_001",
					StartMS:               1000,
					EndMS:                 3500,
					Snippet:               "Synthetic internal speaker sentence.",
					ContextExcerpt:        "Synthetic internal speaker sentence.",
					PersonTitleStatus:     "available",
					PersonTitleSource:     "call_parties",
					AttributionSource:     "gong_party",
					AttributionConfidence: "exact_speaker_id",
					PersonTitle:           "VP Procurement",
				},
			},
		},
	}

	call := func(t *testing.T, switches PolicySwitches) map[string]any {
		t.Helper()
		server := NewServerWithOptions(store, "gongmcp", "test",
			WithToolAllowlist(FacadeToolNames()),
			WithFacadeRoutedToolAllowlist([]string{"call_drilldown"}),
			WithPolicySwitches(switches),
		)
		args, err := json.Marshal(map[string]any{
			"operation": OpEvidenceCallDrilldown,
			"arguments": map[string]any{
				"call_ref":              callRef,
				"theme_query":           "Synthetic",
				"include_person_titles": true,
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
		inner, _ := wrapper["result"].(map[string]any)
		verbatim, _ := inner["verbatim_transcript_excerpts"].([]any)
		if len(verbatim) != 1 {
			t.Fatalf("verbatim len=%d want 1: %v", len(verbatim), inner)
		}
		row, _ := verbatim[0].(map[string]any)
		return row
	}

	allowed := call(t, PolicySwitches{})
	if got, _ := allowed["person_title"].(string); got != "VP Procurement" {
		t.Fatalf("person_title=%q want VP Procurement when explicitly included and policy allows it; row=%v", got, allowed)
	}

	blocked := call(t, PolicySwitches{HideContactNames: true})
	if got, ok := blocked["person_title"].(string); ok && got != "" {
		t.Fatalf("person_title leaked under HideContactNames policy: %q row=%v", got, blocked)
	}
	if got, _ := blocked["person_title_status"].(string); got != "available" {
		t.Fatalf("person_title_status=%q want available even when raw title is hidden", got)
	}
}
