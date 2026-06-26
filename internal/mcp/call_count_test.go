package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFacadeQueryCallCountOperationRegistered(t *testing.T) {
	t.Parallel()

	op, ok := FacadeOperationByName(OpQueryCallCount)
	if !ok {
		t.Fatalf("FacadeOperationByName(%q) not registered", OpQueryCallCount)
	}
	if op.FacadeTool != FacadeToolQuery {
		t.Fatalf("query.call_count facade_tool=%q want %s", op.FacadeTool, FacadeToolQuery)
	}
	if op.RoutedTool != internalRoutedToolCallCount {
		t.Fatalf("query.call_count routed_tool=%q want %s", op.RoutedTool, internalRoutedToolCallCount)
	}
	opText := op.Description
	for _, want := range []string{"filter", "dimension_filters", "duration_seconds", "300"} {
		if !strings.Contains(opText, want) && !strings.Contains(mustJSONForTest(t, op.InputSchema), want) {
			t.Fatalf("query.call_count operation contract missing %q", want)
		}
	}
	props, _ := op.InputSchema["properties"].(map[string]any)
	for _, want := range []string{"filter", "cohort_token", "include_account_names"} {
		if _, ok := props[want]; !ok {
			t.Fatalf("query.call_count schema missing %q", want)
		}
	}
}

func TestFacadeQueryCallCountDispatchUsesSummaryCount(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	server := NewServerWithOptions(store, "gongmcp", "test",
		WithToolAllowlist([]string{FacadeToolQuery}),
		WithFacadeRoutedToolAllowlist([]string{internalRoutedToolCallCount}),
	)
	result, err := server.executeFacadeDispatch(t.Context(), FacadeToolQuery, mustFacadeArgs(t, OpQueryCallCount, map[string]any{
		"filter": map[string]any{
			"dimension_filters": []any{
				map[string]any{
					"dimension": "duration_seconds",
					"operator":  "gte",
					"values":    []string{"300"},
				},
			},
		},
	}))
	if err != nil {
		t.Fatalf("query.call_count dispatch: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected isError: %+v", result)
	}
	wrapper := decodeFacadeWrapper(t, result)
	if wrapper["operation"] != OpQueryCallCount || wrapper["routed_tool"] != internalRoutedToolCallCount {
		t.Fatalf("unexpected facade wrapper: %v", wrapper)
	}
	inner, ok := wrapper["result"].(map[string]any)
	if !ok {
		t.Fatalf("missing inner result: %T", wrapper["result"])
	}
	if count, _ := inner["call_count"].(float64); count != 2 {
		t.Fatalf("duration_seconds>=300 call_count=%v want 2; payload=%v", inner["call_count"], inner)
	}
	if token, _ := inner["cohort_token"].(string); !strings.HasPrefix(token, cohortTokenPrefix) {
		t.Fatalf("missing cohort_token in response: %v", inner["cohort_token"])
	}
	if _, ok := inner["cohort_id"].(string); !ok || inner["cohort_id"] == "" {
		t.Fatalf("missing cohort_id: %v", inner["cohort_id"])
	}
}

func mustJSONForTest(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(raw)
}
