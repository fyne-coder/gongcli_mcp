package mcp

import (
	"strings"
	"testing"
)

func TestFacadeAnalyzeDiscoverySummaryRegistered(t *testing.T) {
	t.Parallel()

	op, ok := FacadeOperationByName(OpAnalyzeDiscoverySummary)
	if !ok {
		t.Fatalf("operation %q not registered", OpAnalyzeDiscoverySummary)
	}
	if op.FacadeTool != FacadeToolAnalyze {
		t.Fatalf("facade_tool=%q want %s", op.FacadeTool, FacadeToolAnalyze)
	}
	if op.RoutedTool != internalRoutedToolDiscoverySummary {
		t.Fatalf("routed_tool=%q want %s", op.RoutedTool, internalRoutedToolDiscoverySummary)
	}
	exampleText := mustJSONText(t, op.Examples)
	if !strings.Contains(strings.ToLower(exampleText), "business discovery") {
		t.Fatalf("examples should steer Business Discovery cohorts to title_query: %s", exampleText)
	}
}

func TestFacadeAnalyzeDiscoverySummaryBusinessDiscoverySeedless(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	seedBusinessAnalysisMCPFixtures(t, store)

	server := NewServerWithOptions(store, "gongmcp", "test",
		WithToolAllowlist([]string{FacadeToolAnalyze}),
		WithFacadeRoutedToolAllowlist([]string{internalRoutedToolDiscoverySummary}),
	)
	result, err := server.executeFacadeDispatch(t.Context(), FacadeToolAnalyze, mustFacadeArgs(t, OpAnalyzeDiscoverySummary, map[string]any{
		"filter": map[string]any{
			"title_query": "business discovery",
		},
		"limit": 10,
	}))
	if err != nil {
		t.Fatalf("analyze.discovery_summary dispatch: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected isError: %+v", result)
	}
	wrapper := decodeFacadeWrapper(t, result)
	inner, ok := wrapper["result"].(map[string]any)
	if !ok {
		t.Fatalf("missing inner result: %T", wrapper["result"])
	}
	if got, _ := inner["operation"].(string); got != OpAnalyzeDiscoverySummary {
		t.Fatalf("operation=%q want %s", got, OpAnalyzeDiscoverySummary)
	}
	if got, _ := inner["cohort_token"].(string); !strings.HasPrefix(got, cohortTokenPrefix) {
		t.Fatalf("cohort_token=%q want prefix %q", got, cohortTokenPrefix)
	}
	suggested := strings.ToLower(mustJSONText(t, inner["suggested_seed_topics"]))
	if !strings.Contains(suggested, "pricing") || !strings.Contains(suggested, "manual order entry") {
		t.Fatalf("suggested_seed_topics missing expected seeds: %s", suggested)
	}
	selected := mustJSONText(t, inner["selected_seed_topics"])
	if selected == "" || selected == "[]" {
		t.Fatalf("selected_seed_topics should be populated in auto mode: %s", selected)
	}
	coverage, _ := inner["coverage_summary"].(map[string]any)
	if got, _ := coverage["call_count"].(float64); got < 1 {
		t.Fatalf("coverage_summary.call_count=%v want >=1; inner=%v", got, inner)
	}
	contract := strings.ToLower(mustJSONText(t, inner["answer_contract"]))
	if strings.Contains(contract, "final buyer-validated") && !strings.Contains(contract, "not final") {
		t.Fatalf("answer_contract should not present seedless output as final themes: %s", contract)
	}
	if strings.Contains(contract, "directional") || strings.Contains(contract, "customer-facing") {
		// expected strict contract language
	} else {
		t.Fatalf("answer_contract should distinguish directional guidance from customer-facing claims: %s", contract)
	}
	summaries := mustJSONText(t, inner["theme_summaries"])
	preview := mustJSONText(t, inner["seeded_preview"])
	if summaries == "[]" && preview == "[]" {
		t.Fatalf("expected bounded theme_summaries or seeded_preview evidence when fixtures contain transcripts: summaries=%s preview=%s", summaries, preview)
	}
	if summaries != "[]" && !strings.Contains(summaries, "directional_auto_selected_seed_evidence_not_exhaustive_theme_ranking") {
		t.Fatalf("auto-selected theme_summaries should be explicitly directional: %s", summaries)
	}
}

func TestFacadeAnalyzeDiscoverySummaryAcceptsCohortToken(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	seedBusinessAnalysisMCPFixtures(t, store)

	server := NewServerWithOptions(store, "gongmcp", "test",
		WithToolAllowlist([]string{FacadeToolAnalyze}),
		WithFacadeRoutedToolAllowlist([]string{"build_call_cohort", internalRoutedToolDiscoverySummary}),
	)
	filter := map[string]any{"title_query": "business discovery"}
	buildResult, err := server.executeFacadeDispatch(t.Context(), FacadeToolAnalyze, mustFacadeArgs(t, OpAnalyzeCohortBuild, map[string]any{
		"filter": filter,
		"limit":  10,
	}))
	if err != nil {
		t.Fatalf("cohort build: %v", err)
	}
	buildInner, _ := decodeFacadeWrapper(t, buildResult)["result"].(map[string]any)
	token, _ := buildInner["cohort_token"].(string)

	result, err := server.executeFacadeDispatch(t.Context(), FacadeToolAnalyze, mustFacadeArgs(t, OpAnalyzeDiscoverySummary, map[string]any{
		"cohort_token": token,
		"limit":        10,
	}))
	if err != nil {
		t.Fatalf("discovery summary via cohort_token: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected isError: %+v", result)
	}
	inner, _ := decodeFacadeWrapper(t, result)["result"].(map[string]any)
	if got, _ := inner["cohort_token"].(string); !strings.HasPrefix(got, cohortTokenPrefix) {
		t.Fatalf("cohort_token=%q want prefix %q", got, cohortTokenPrefix)
	}
	normalized, _ := inner["normalized_filter"].(map[string]any)
	if got, _ := normalized["title_query"].(string); !strings.EqualFold(got, "business discovery") {
		t.Fatalf("normalized_filter.title_query=%q want business discovery", got)
	}
}

func TestMergeDiscoverySeedTopicNamesDeterministic(t *testing.T) {
	t.Parallel()

	got := mergeDiscoverySeedTopicNames(
		[]string{"pricing", "timeline"},
		[]businessAnalysisTheme{{Theme: "manual order entry", SupportCount: 3}, {Theme: "ERP integration", SupportCount: 3}},
		10,
	)
	wantLead := []string{"ERP integration", "manual order entry", "pricing", "timeline"}
	if len(got) < len(wantLead) {
		t.Fatalf("merge len=%d want at least %d: %v", len(got), len(wantLead), got)
	}
	for i, theme := range wantLead {
		if got[i] != theme {
			t.Fatalf("merge[%d]=%q want %q; got=%v", i, got[i], theme, got)
		}
	}
}
