package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFacadeQueryGuidedRepairMalformedArguments(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	server := NewServerWithOptions(store, "gongmcp", "test",
		WithToolAllowlist([]string{FacadeToolQuery}),
		WithFacadeRoutedToolAllowlist([]string{internalRoutedToolCallCount, internalRoutedToolDimensionCounts, "search_calls_by_filters"}),
	)

	tests := []struct {
		name             string
		operation        string
		args             map[string]any
		wantIssue        string
		wantOperation    string
		wantPhrases      []string
		wantOriginal     []string
		wantStrictReject bool
	}{
		{
			name:      "filters on query.calls",
			operation: OpQueryCalls,
			args: map[string]any{
				"filters": []any{map[string]any{"dimension": "duration_seconds", "operator": "gte", "value": 300}},
				"limit":   1,
			},
			wantIssue:     "use_filter_dimension_filters_not_filters",
			wantOperation: OpQueryCallCount,
			wantPhrases: []string{
				"guided_repair",
				"filter.dimension_filters",
				"values",
				OpQueryCallCount,
			},
			wantOriginal:     []string{`unknown field "filters"`},
			wantStrictReject: true,
		},
		{
			name:      "filters on query.call_count",
			operation: OpQueryCallCount,
			args: map[string]any{
				"filters": []any{map[string]any{"dimension": "duration_seconds", "operator": "gte", "value": 300}},
			},
			wantIssue:     "use_filter_dimension_filters_not_filters",
			wantOperation: OpQueryCallCount,
			wantPhrases: []string{
				"guided_repair",
				"filter.dimension_filters",
				"values",
			},
			wantOriginal:     []string{`unknown field "filters"`},
			wantStrictReject: true,
		},
		{
			name:      "top level dimension_filters",
			operation: OpQueryCallCount,
			args: map[string]any{
				"dimension_filters": []any{map[string]any{"dimension": "duration_seconds", "operator": "gte", "values": []string{"300"}}},
			},
			wantIssue:     "dimension_filters_belongs_under_filter",
			wantOperation: OpQueryCallCount,
			wantPhrases: []string{
				"guided_repair",
			},
			wantOriginal:     []string{`unknown field "dimension_filters"`},
			wantStrictReject: true,
		},
		{
			name:      "filters on query.dimension_counts",
			operation: OpQueryDimensionCounts,
			args: map[string]any{
				"filters":   []any{map[string]any{"dimension": "duration_seconds", "operator": "gte", "value": 300}},
				"dimension": "lifecycle",
			},
			wantIssue:     "use_filter_dimension_filters_not_filters",
			wantOperation: OpQueryDimensionCounts,
			wantPhrases: []string{
				"guided_repair",
				"filter.dimension_filters",
				"values",
				OpQueryDimensionCounts,
			},
			wantOriginal:     []string{`unknown field "filters"`},
			wantStrictReject: true,
		},
		{
			name:      "singular value",
			operation: OpQueryCallCount,
			args: map[string]any{
				"filter": map[string]any{
					"dimension_filters": []any{map[string]any{"dimension": "duration_seconds", "operator": "gte", "value": 300}},
				},
			},
			wantIssue:     "use_values_array_not_value",
			wantOperation: OpQueryCallCount,
			wantPhrases: []string{
				"guided_repair",
			},
			wantOriginal:     []string{`unknown field "value"`},
			wantStrictReject: true,
		},
		{
			name:      "numeric values",
			operation: OpQueryCallCount,
			args: map[string]any{
				"filter": map[string]any{
					"dimension_filters": []any{map[string]any{"dimension": "duration_seconds", "operator": "gte", "values": []any{300}}},
				},
			},
			wantIssue:     "values_must_be_string_array",
			wantOperation: OpQueryCallCount,
			wantPhrases: []string{
				"guided_repair",
			},
			wantOriginal:     []string{"cannot unmarshal number"},
			wantStrictReject: true,
		},
		{
			name:             "missing filter",
			operation:        OpQueryCallCount,
			args:             map[string]any{},
			wantIssue:        "missing_filter_object",
			wantOperation:    OpQueryCallCount,
			wantPhrases:      []string{"guided_repair", "filter.dimension_filters", "requires at least one selective filter field"},
			wantStrictReject: false,
		},
		{
			name:             "missing operation",
			operation:        "",
			args:             map[string]any{"filter": map[string]any{}},
			wantIssue:        "missing_operation",
			wantOperation:    OpQueryCallCount,
			wantPhrases:      []string{"guided_repair", "requires an operation", OpQueryCallCount, "suggested_arguments"},
			wantStrictReject: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := server.executeFacadeDispatch(t.Context(), FacadeToolQuery, mustFacadeArgs(t, tt.operation, tt.args))
			if err == nil {
				t.Fatal("expected guided repair error")
			}
			msg := err.Error()
			for _, phrase := range tt.wantPhrases {
				if !strings.Contains(msg, phrase) {
					t.Fatalf("error missing %q: %s", phrase, msg)
				}
			}

			var payload facadeGuidedRepairPayload
			if err := json.Unmarshal([]byte(msg), &payload); err != nil {
				t.Fatalf("guided repair payload is not JSON: %v; msg=%s", err, msg)
			}
			for _, phrase := range tt.wantOriginal {
				if !strings.Contains(payload.Error, phrase) {
					t.Fatalf("original error missing %q: %s", phrase, payload.Error)
				}
			}
			if payload.Repair.Issue != tt.wantIssue {
				t.Fatalf("issue=%q want %q", payload.Repair.Issue, tt.wantIssue)
			}
			if payload.Repair.SuggestedOperation != tt.wantOperation {
				t.Fatalf("suggested_operation=%q want %q", payload.Repair.SuggestedOperation, tt.wantOperation)
			}
			if len(payload.Repair.Guidance) == 0 {
				t.Fatal("missing guidance")
			}
			if payload.Repair.SuggestedArguments == "" {
				t.Fatal("missing suggested_arguments")
			}
			if tt.wantIssue == "use_values_array_not_value" && !strings.Contains(strings.Join(payload.Repair.Guidance, " "), `values":["300"]`) {
				t.Fatalf("guidance missing values example: %v", payload.Repair.Guidance)
			}
			if tt.wantIssue == "dimension_filters_belongs_under_filter" && !strings.Contains(strings.Join(payload.Repair.Guidance, " "), "filter") {
				t.Fatalf("guidance missing filter nesting: %v", payload.Repair.Guidance)
			}
			if !strings.Contains(payload.Repair.SuggestedArguments, "dimension_filters") || !strings.Contains(payload.Repair.SuggestedArguments, "values") {
				t.Fatalf("suggested_arguments missing dimension_filters/values: %s", payload.Repair.SuggestedArguments)
			}

			if tt.wantStrictReject {
				corrected, err := server.executeFacadeDispatch(t.Context(), FacadeToolQuery, json.RawMessage(payload.Repair.SuggestedArguments))
				if err != nil {
					t.Fatalf("corrected dispatch should succeed: %v", err)
				}
				if corrected.IsError {
					t.Fatalf("corrected dispatch returned tool error: %+v", corrected)
				}
			}
		})
	}
}

func TestFacadeQueryGuidedRepairCorrectedCallCountStillWorks(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	server := NewServerWithOptions(store, "gongmcp", "test",
		WithToolAllowlist([]string{FacadeToolQuery}),
		WithFacadeRoutedToolAllowlist([]string{internalRoutedToolCallCount, "search_calls_by_filters"}),
	)

	malformed := mustFacadeArgs(t, OpQueryCalls, map[string]any{
		"filters": []any{map[string]any{"dimension": "duration_seconds", "operator": "gte", "value": 300}},
		"limit":   1,
	})
	_, err := server.executeFacadeDispatch(t.Context(), FacadeToolQuery, malformed)
	if err == nil {
		t.Fatal("expected malformed dispatch error")
	}

	var payload facadeGuidedRepairPayload
	if err := json.Unmarshal([]byte(err.Error()), &payload); err != nil {
		t.Fatalf("decode guided repair payload: %v", err)
	}
	if payload.Repair.SuggestedOperation != OpQueryCallCount {
		t.Fatalf("suggested_operation=%q want %q", payload.Repair.SuggestedOperation, OpQueryCallCount)
	}

	result, err := server.executeFacadeDispatch(t.Context(), FacadeToolQuery, json.RawMessage(payload.Repair.SuggestedArguments))
	if err != nil {
		t.Fatalf("corrected query.call_count dispatch: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected isError: %+v", result)
	}
	wrapper := decodeFacadeWrapper(t, result)
	inner, ok := wrapper["result"].(map[string]any)
	if !ok {
		t.Fatalf("missing inner result: %T", wrapper["result"])
	}
	if count, _ := inner["call_count"].(float64); count != 2 {
		t.Fatalf("duration_seconds>=300 call_count=%v want 2", inner["call_count"])
	}
}
