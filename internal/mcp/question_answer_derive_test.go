package mcp

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestFacadeQuestionAnswerDerivesBoundedThemeQuery proves the gap-followup
// extension to question.answer: a long, free-form, natural-language
// question is no longer fed verbatim into the FTS query path. Instead the
// operation derives a bounded theme_query (cap matches the underlying FTS
// term limit), surfaces it in `derived_theme_query`, and returns coverage
// metadata plus a non-fatal warning instead of erroring out on the
// "no more than 12 search terms" guard.
func TestFacadeQuestionAnswerDerivesBoundedThemeQuery(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	server := NewServerWithOptions(store, "gongmcp", "test",
		WithToolAllowlist([]string{FacadeToolAnalyze}),
		WithFacadeRoutedToolAllowlist([]string{internalRoutedToolQuestionAnswer}),
	)

	// 20+ tokens — well past the 12-term FTS guard. The extra stop words
	// must be dropped, and the derived query must stay within the limit.
	question := "What are prospects in manufacturing and financial services telling us about manual order entry pain procurement bottlenecks finance close cycle reconciliation pricing concerns and competitor comparisons across recent calls?"

	args, err := json.Marshal(map[string]any{
		"operation": OpQuestionAnswer,
		"arguments": map[string]any{
			"question":      question,
			"role_context":  "sales-enablement",
			"output_intent": "brief",
			"filter": map[string]any{
				"from_date": "2026-04-01",
				"to_date":   "2026-04-30",
				"limit":     5,
			},
			"limit": 5,
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	result, err := server.executeFacadeDispatch(t.Context(), FacadeToolAnalyze, args)
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
	derived, ok := inner["derived_theme_query"].(string)
	if !ok || strings.TrimSpace(derived) == "" {
		t.Fatalf("derived_theme_query must be present and non-empty for a long natural-language question; inner=%v", inner)
	}
	// The derived query must be bounded by the FTS term cap so the
	// underlying evidence search never trips the
	// "no more than 12 search terms" guard.
	tokens := strings.Fields(derived)
	if len(tokens) > 12 {
		t.Fatalf("derived_theme_query has %d tokens; want <=12: %q", len(tokens), derived)
	}
	if len(tokens) == 0 {
		t.Fatalf("derived_theme_query produced zero tokens: %q", derived)
	}
	if len(derived) > maxBusinessAnalysisFTSQueryLength {
		t.Fatalf("derived_theme_query has %d chars; want <=%d: %q", len(derived), maxBusinessAnalysisFTSQueryLength, derived)
	}

	// derivation metadata: status + dropped term count must surface so
	// callers know the question was rewritten.
	derivation, _ := inner["theme_query_derivation"].(map[string]any)
	if derivation == nil {
		t.Fatalf("theme_query_derivation must be present: %v", inner)
	}
	for _, key := range []string{"source", "term_count", "dropped_count", "max_terms"} {
		if _, ok := derivation[key]; !ok {
			t.Fatalf("theme_query_derivation missing %q: %v", key, derivation)
		}
	}
	if src, _ := derivation["source"].(string); src != "derived_from_question" {
		t.Fatalf("theme_query_derivation.source=%q want derived_from_question", src)
	}
	dropped, _ := derivation["dropped_count"].(float64)
	if dropped <= 0 {
		t.Fatalf("expected dropped_count>0 for a long question; got %v", dropped)
	}

	// The operation must NOT have errored: the dispatch was successful
	// and we have an evidence pack (or empty results, but no error).
	if status, _ := inner["status"].(string); status == "" {
		t.Fatalf("question.answer status missing: %v", inner)
	}
}

func TestFacadeQuestionAnswerFallsBackToMatchedEvidenceTerm(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	seedBusinessAnalysisMCPFixtures(t, store)

	server := NewServerWithOptions(store, "gongmcp", "test",
		WithToolAllowlist([]string{FacadeToolAnalyze}),
		WithFacadeRoutedToolAllowlist([]string{internalRoutedToolQuestionAnswer}),
	)

	args, err := json.Marshal(map[string]any{
		"operation": OpQuestionAnswer,
		"arguments": map[string]any{
			"question":      "Which manual order entry industry deals close won versus closed lost?",
			"role_context":  "revops",
			"output_intent": "quotes",
			"filter": map[string]any{
				"from_date":         "2026-02-01",
				"to_date":           "2026-03-31",
				"transcript_status": "present",
				"limit":             5,
			},
			"limit": 5,
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	result, err := server.executeFacadeDispatch(t.Context(), FacadeToolAnalyze, args)
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
	if got, _ := inner["evidence_count"].(float64); got == 0 {
		t.Fatalf("question.answer should retry a derived query with a matched high-signal fallback term; inner=%v", inner)
	}
	if got, _ := inner["evidence_query"].(string); got != "manual" {
		t.Fatalf("evidence_query=%q want matched fallback term manual; inner=%v", got, inner)
	}
	if got, _ := inner["derived_theme_query"].(string); !strings.Contains(got, "manual") || !strings.Contains(got, "order") {
		t.Fatalf("derived_theme_query=%q should preserve the original derived query even when evidence_query falls back", got)
	}
	derivation, _ := inner["theme_query_derivation"].(map[string]any)
	if derivation == nil {
		t.Fatalf("theme_query_derivation missing: %v", inner)
	}
	if got, _ := derivation["initial_query"].(string); !strings.Contains(got, "manual") || !strings.Contains(got, "order") {
		t.Fatalf("theme_query_derivation.initial_query=%q should preserve the original derived query", got)
	}
	if got, _ := derivation["fallback_query"].(string); got != "manual" {
		t.Fatalf("theme_query_derivation.fallback_query=%q want manual; derivation=%v", got, derivation)
	}
	if got, _ := derivation["fallback_reason"].(string); got == "" {
		t.Fatalf("theme_query_derivation.fallback_reason missing: %v", derivation)
	}
	if got, _ := derivation["fallback_trigger_reason"].(string); got != "initial_derived_query_returned_no_evidence" {
		t.Fatalf("theme_query_derivation.fallback_trigger_reason=%q want initial_derived_query_returned_no_evidence; derivation=%v", got, derivation)
	}
	if got, _ := derivation["fallback_outcome"].(string); got != "fallback_query_returned_evidence" {
		t.Fatalf("theme_query_derivation.fallback_outcome=%q want fallback_query_returned_evidence; derivation=%v", got, derivation)
	}
}

func TestQuestionAnswerFallbackQueriesStripsFTSQuotes(t *testing.T) {
	t.Parallel()

	got := questionAnswerFallbackQueries(`"manual" "order" "entry"`)
	want := []string{"manual", "order", "entry"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("questionAnswerFallbackQueries()=%v want %v", got, want)
	}
}

// TestFacadeQuestionAnswerDerivedThemeQueryHonoursExplicitOverride verifies
// that if the caller provides an explicit theme_query / query, the
// derivation step does not silently overwrite it.
func TestFacadeQuestionAnswerDerivedThemeQueryHonoursExplicitOverride(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	server := NewServerWithOptions(store, "gongmcp", "test",
		WithToolAllowlist([]string{FacadeToolAnalyze}),
		WithFacadeRoutedToolAllowlist([]string{internalRoutedToolQuestionAnswer}),
	)

	args, err := json.Marshal(map[string]any{
		"operation": OpQuestionAnswer,
		"arguments": map[string]any{
			"question":     "What are prospects saying about implementation effort and pricing?",
			"theme_query":  "Synthetic",
			"role_context": "sales",
			"filter": map[string]any{
				"query": "Synthetic",
				"limit": 5,
			},
			"limit": 5,
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	result, err := server.executeFacadeDispatch(t.Context(), FacadeToolAnalyze, args)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	wrapper := decodeFacadeWrapper(t, result)
	inner, _ := wrapper["result"].(map[string]any)
	derivation, _ := inner["theme_query_derivation"].(map[string]any)
	if derivation == nil {
		t.Fatalf("theme_query_derivation must be present even when explicit theme_query is set")
	}
	if src, _ := derivation["source"].(string); src != "explicit" {
		t.Fatalf("theme_query_derivation.source=%q want explicit when theme_query is provided", src)
	}
}

func TestQuestionAnswerOperationSchemaAllowsFreeFormQuestionLength(t *testing.T) {
	t.Parallel()

	for _, op := range FacadeOperations() {
		if op.Name != OpQuestionAnswer {
			continue
		}
		properties, _ := op.InputSchema["properties"].(map[string]any)
		question, _ := properties["question"].(map[string]any)
		if got, _ := question["maxLength"].(int); got != maxQuestionAnswerQuestionLength {
			t.Fatalf("question.answer schema question.maxLength=%v want %d", question["maxLength"], maxQuestionAnswerQuestionLength)
		}
		return
	}
	t.Fatalf("%s operation not registered", OpQuestionAnswer)
}
