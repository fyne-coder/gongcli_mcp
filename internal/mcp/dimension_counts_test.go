package mcp

import (
	"strings"
	"testing"
)

func TestFacadeQueryDimensionCountsOperationRegistered(t *testing.T) {
	t.Parallel()

	op, ok := FacadeOperationByName(OpQueryDimensionCounts)
	if !ok {
		t.Fatalf("FacadeOperationByName(%q) not registered", OpQueryDimensionCounts)
	}
	if op.FacadeTool != FacadeToolQuery {
		t.Fatalf("query.dimension_counts facade_tool=%q want %s", op.FacadeTool, FacadeToolQuery)
	}
	if op.RoutedTool != internalRoutedToolDimensionCounts {
		t.Fatalf("query.dimension_counts routed_tool=%q want %s", op.RoutedTool, internalRoutedToolDimensionCounts)
	}
	props, _ := op.InputSchema["properties"].(map[string]any)
	for _, want := range []string{"filter", "cohort_token", "dimension", "theme_query", "limit", "include_account_names", "internal_domains", "participant_affiliation_filter"} {
		if _, ok := props[want]; !ok {
			t.Fatalf("query.dimension_counts schema missing %q", want)
		}
	}
	required := schemaRequiredFields(t, op.InputSchema)
	if len(required) != 1 || required[0] != "dimension" {
		t.Fatalf("query.dimension_counts required=%v want [dimension]", required)
	}
}

func schemaRequiredFields(t *testing.T, schema map[string]any) []string {
	t.Helper()
	switch raw := schema["required"].(type) {
	case []string:
		return raw
	case []any:
		out := make([]string, 0, len(raw))
		for _, item := range raw {
			field, _ := item.(string)
			out = append(out, field)
		}
		return out
	default:
		return nil
	}
}

func TestFacadeQueryDimensionCountsDiscoverable(t *testing.T) {
	t.Parallel()

	server := NewServerWithOptions(nil, "gongmcp", "test",
		WithToolAllowlist([]string{FacadeToolDiscoverCapabilities}),
		WithFacadeRoutedToolAllowlist([]string{internalRoutedToolDimensionCounts}),
	)
	result, err := server.executeFacadeDiscoverCapabilities(nil)
	if err != nil {
		t.Fatalf("discover capabilities: %v", err)
	}
	payload := decodeDiscoverCapabilitiesPayload(t, result)
	availability := map[string]bool{}
	for _, op := range payload["operations"].([]any) {
		entry, _ := op.(map[string]any)
		name, _ := entry["operation"].(string)
		avail, _ := entry["routed_tool_available"].(bool)
		availability[name] = avail
	}
	if !availability[OpQueryDimensionCounts] {
		t.Fatalf("expected %s to be discoverable with routed tool allowlisted", OpQueryDimensionCounts)
	}
}

func TestFacadeQueryDimensionCountsDispatchOpportunityStageRanking(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	seedBusinessAnalysisMCPFixtures(t, store)

	server := NewServerWithOptions(store, "gongmcp", "test",
		WithToolAllowlist([]string{FacadeToolQuery}),
		WithFacadeRoutedToolAllowlist([]string{internalRoutedToolDimensionCounts}),
	)
	result, err := server.executeFacadeDispatch(t.Context(), FacadeToolQuery, mustFacadeArgs(t, OpQueryDimensionCounts, map[string]any{
		"filter": map[string]any{
			"title_query": "business discovery",
		},
		"dimension": "opportunity_stage",
		"limit":     10,
	}))
	if err != nil {
		t.Fatalf("query.dimension_counts dispatch: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected isError: %+v", result)
	}
	wrapper := decodeFacadeWrapper(t, result)
	if wrapper["operation"] != OpQueryDimensionCounts || wrapper["routed_tool"] != internalRoutedToolDimensionCounts {
		t.Fatalf("unexpected facade wrapper: %v", wrapper)
	}
	inner, ok := wrapper["result"].(map[string]any)
	if !ok {
		t.Fatalf("missing inner result: %T", wrapper["result"])
	}
	if got, _ := inner["dimension"].(string); got != "opportunity_stage" {
		t.Fatalf("dimension=%q want opportunity_stage", got)
	}
	rows, ok := inner["rows"].([]any)
	if !ok || len(rows) < 2 {
		t.Fatalf("expected at least two opportunity_stage buckets, got %T %v", inner["rows"], inner["rows"])
	}
	buckets := map[string]float64{}
	for _, item := range rows {
		row, _ := item.(map[string]any)
		bucket, _ := row["bucket"].(string)
		count, _ := row["call_count"].(float64)
		buckets[bucket] = count
	}
	if buckets["Discovery"] != 1 || buckets["Evaluation"] != 1 {
		t.Fatalf("unexpected stage buckets: %v", buckets)
	}
	if token, _ := inner["cohort_token"].(string); !strings.HasPrefix(token, cohortTokenPrefix) {
		t.Fatalf("missing cohort_token: %v", inner["cohort_token"])
	}
	if _, ok := inner["coverage_summary"].(map[string]any); !ok {
		t.Fatalf("missing coverage_summary: %v", inner["coverage_summary"])
	}
	if _, ok := inner["answer_contract"].([]any); !ok {
		t.Fatalf("missing answer_contract: %v", inner["answer_contract"])
	}
}

func TestFacadeQueryDimensionCountsDispatchDurationFilterLifecycleRanking(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	server := NewServerWithOptions(store, "gongmcp", "test",
		WithToolAllowlist([]string{FacadeToolQuery}),
		WithFacadeRoutedToolAllowlist([]string{internalRoutedToolDimensionCounts}),
	)
	result, err := server.executeFacadeDispatch(t.Context(), FacadeToolQuery, mustFacadeArgs(t, OpQueryDimensionCounts, map[string]any{
		"filter": map[string]any{
			"dimension_filters": []any{
				map[string]any{
					"dimension": "duration_seconds",
					"operator":  "gte",
					"values":    []string{"300"},
				},
			},
		},
		"dimension": "lifecycle",
		"limit":     10,
	}))
	if err != nil {
		t.Fatalf("query.dimension_counts dispatch: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected isError: %+v", result)
	}
	inner, _ := decodeFacadeWrapper(t, result)["result"].(map[string]any)
	rows, ok := inner["rows"].([]any)
	if !ok || len(rows) == 0 {
		t.Fatalf("expected lifecycle rows, got %v", inner["rows"])
	}
	coverage, _ := inner["coverage_summary"].(map[string]any)
	if count, _ := coverage["call_count"].(float64); count != 2 {
		t.Fatalf("coverage_summary.call_count=%v want 2", count)
	}
}

func TestFacadeQueryDimensionCountsRejectsNonSelectiveFilter(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	server := NewServerWithOptions(store, "gongmcp", "test",
		WithToolAllowlist([]string{FacadeToolQuery}),
		WithFacadeRoutedToolAllowlist([]string{internalRoutedToolDimensionCounts}),
	)
	_, err := server.executeFacadeDispatch(t.Context(), FacadeToolQuery, mustFacadeArgs(t, OpQueryDimensionCounts, map[string]any{
		"filter":    map[string]any{},
		"dimension": "lifecycle",
	}))
	if err == nil {
		t.Fatal("expected non-selective filter rejection")
	}
	if !strings.Contains(err.Error(), OpQueryDimensionCounts) || !strings.Contains(err.Error(), "selective filter") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFacadeQueryDimensionCountsRejectsGovernanceActiveAggregates(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	server := NewServerWithOptions(store, "gongmcp", "test",
		WithToolAllowlist([]string{FacadeToolQuery}),
		WithFacadeRoutedToolAllowlist([]string{internalRoutedToolDimensionCounts}),
		WithSuppressedCallIDs([]string{"synthetic-call-1"}),
	)
	_, err := server.executeFacadeDispatch(t.Context(), FacadeToolQuery, mustFacadeArgs(t, OpQueryDimensionCounts, map[string]any{
		"filter": map[string]any{
			"dimension_filters": []any{
				map[string]any{
					"dimension": "duration_seconds",
					"operator":  "gte",
					"values":    []string{"300"},
				},
			},
		},
		"dimension": "participant_email",
		"limit":     10,
	}))
	if err == nil {
		t.Fatal("expected governance-active aggregate rejection")
	}
	if !strings.Contains(err.Error(), OpQueryDimensionCounts) || !strings.Contains(err.Error(), "AI governance filtering is active") {
		t.Fatalf("unexpected governance error: %v", err)
	}
}

func TestFacadeQueryDimensionCountsSparseReadinessWarning(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	server := NewServerWithOptions(store, "gongmcp", "test",
		WithToolAllowlist([]string{FacadeToolQuery}),
		WithFacadeRoutedToolAllowlist([]string{internalRoutedToolDimensionCounts}),
	)
	result, err := server.executeFacadeDispatch(t.Context(), FacadeToolQuery, mustFacadeArgs(t, OpQueryDimensionCounts, map[string]any{
		"filter": map[string]any{
			"dimension_filters": []any{
				map[string]any{
					"dimension": "duration_seconds",
					"operator":  "gte",
					"values":    []string{"300"},
				},
			},
		},
		"dimension": "opportunity_stage",
		"limit":     10,
	}))
	if err != nil {
		t.Fatalf("query.dimension_counts dispatch: %v", err)
	}
	inner, _ := decodeFacadeWrapper(t, result)["result"].(map[string]any)
	warnings := strings.ToLower(mustJSONForTest(t, inner["warnings"]))
	if !strings.Contains(warnings, "opportunity_stage_missing_or_unmapped") {
		t.Fatalf("expected opportunity_stage readiness warning, got %s", warnings)
	}
}

func TestFacadeQueryDimensionCountsSmallCellSuppressionWarning(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	seedBusinessAnalysisMCPFixtures(t, store)

	server := NewServerWithOptions(store, "gongmcp", "test",
		WithToolAllowlist([]string{FacadeToolQuery}),
		WithFacadeRoutedToolAllowlist([]string{internalRoutedToolDimensionCounts}),
		WithBusinessAnalysisSmallCellMin(3),
	)
	result, err := server.executeFacadeDispatch(t.Context(), FacadeToolQuery, mustFacadeArgs(t, OpQueryDimensionCounts, map[string]any{
		"filter": map[string]any{
			"title_query": "business discovery",
		},
		"dimension": "opportunity_stage",
		"limit":     10,
	}))
	if err != nil {
		t.Fatalf("query.dimension_counts dispatch: %v", err)
	}
	inner, _ := decodeFacadeWrapper(t, result)["result"].(map[string]any)
	warnings := strings.ToLower(mustJSONForTest(t, inner["warnings"]))
	if !strings.Contains(warnings, "small_cell_suppression_applied") {
		t.Fatalf("expected small_cell_suppression warning, got %s", warnings)
	}
	limitations := strings.ToLower(mustJSONForTest(t, inner["limitations"]))
	if !strings.Contains(limitations, "small_cell_suppression_min_3") {
		t.Fatalf("expected small_cell_suppression limitation, got %s", limitations)
	}
	rows, _ := inner["rows"].([]any)
	if len(rows) != 0 {
		t.Fatalf("expected all sub-minimum buckets suppressed, got %d rows: %v", len(rows), mustJSONForTest(t, rows))
	}
}
