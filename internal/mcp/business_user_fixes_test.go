package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
)

func TestFieldProfilePresetsControlExposureFlags(t *testing.T) {
	t.Parallel()

	limited, err := applyFieldProfile("limited", fieldProfileApplication{
		IncludeRawIDs:           true,
		IncludeCallTitles:       true,
		IncludeAccountNames:     true,
		IncludeOpportunityNames: true,
		IncludeSpeakerRefs:      true,
	})
	if err != nil {
		t.Fatalf("apply limited profile: %v", err)
	}
	if limited.IncludeRawIDs || limited.IncludeCallTitles || limited.IncludeAccountNames || limited.IncludeOpportunityNames || limited.IncludeSpeakerRefs {
		t.Fatalf("limited profile should clear opt-in exposure flags: %+v", limited)
	}

	attribution, err := applyFieldProfile("attribution", fieldProfileApplication{})
	if err != nil {
		t.Fatalf("apply attribution profile: %v", err)
	}
	if attribution.IncludeRawIDs {
		t.Fatalf("attribution profile must not enable raw IDs: %+v", attribution)
	}
	if !attribution.IncludeCallTitles || !attribution.IncludeAccountNames || !attribution.IncludeOpportunityNames || !attribution.IncludeSpeakerRefs {
		t.Fatalf("attribution profile should enable business attribution fields: %+v", attribution)
	}
}

func TestQuestionAnswerFallbackQueriesSkipGenericBusinessWords(t *testing.T) {
	t.Parallel()

	got := questionAnswerFallbackQueries("main themes showing business discovery quarter concerns")
	if len(got) != 0 {
		t.Fatalf("generic business words should not become literal fallback queries: %v", got)
	}
}

func TestFacadeQuestionAnswerGenericThemeQuestionNeedsSeed(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	seedBusinessAnalysisMCPFixtures(t, store)

	server := NewServerWithOptions(store, "gongmcp", "test",
		WithToolAllowlist([]string{FacadeToolAnalyze}),
		WithFacadeRoutedToolAllowlist([]string{internalRoutedToolQuestionAnswer}),
	)
	result, err := server.executeFacadeDispatch(t.Context(), FacadeToolAnalyze, mustFacadeArgs(t, OpQuestionAnswer, map[string]any{
		"question":      "What are the main themes showing up in business discovery this quarter?",
		"role_context":  "sales-manager",
		"output_intent": "themes",
		"filter": map[string]any{
			"from_date":         "2026-04-01",
			"to_date":           "2026-04-30",
			"transcript_status": "present",
			"limit":             5,
		},
		"limit": 5,
	}))
	if err != nil {
		t.Fatalf("generic question.answer dispatch: %v", err)
	}
	if result.IsError {
		t.Fatalf("generic question.answer should return guided payload, not tool error: %+v", result)
	}
	wrapper := decodeFacadeWrapper(t, result)
	inner, ok := wrapper["result"].(map[string]any)
	if !ok {
		t.Fatalf("missing inner result: %T", wrapper["result"])
	}
	if got, _ := inner["status"].(string); got != "needs_theme_seed" {
		t.Fatalf("status=%q want needs_theme_seed; inner=%v", got, inner)
	}
	if got, _ := inner["evidence_count"].(float64); got != 0 {
		t.Fatalf("evidence_count=%v want 0 for seed guidance payload; inner=%v", got, inner)
	}
	if got, _ := inner["evidence_query"].(string); got != "" {
		t.Fatalf("evidence_query=%q want empty for generic prompt guidance; inner=%v", got, inner)
	}
	if got := strings.ToLower(mustJSONText(t, inner["recommended_operations"])); !strings.Contains(got, OpThemeIntelReport) || !strings.Contains(got, OpExtractBuyerQuestions) || !strings.Contains(got, OpExtractObjectionSignals) {
		t.Fatalf("recommended_operations should steer business users to seeded reports and extractors: %s", got)
	}
	if got := strings.ToLower(mustJSONText(t, inner["suggested_seed_topics"])); !strings.Contains(got, "pricing") || !strings.Contains(got, "manual order entry") {
		t.Fatalf("suggested_seed_topics missing expected business seeds: %s", got)
	}
	derivation, _ := inner["theme_query_derivation"].(map[string]any)
	if got, _ := derivation["outcome"].(string); got != "no_specific_theme_seed" {
		t.Fatalf("theme_query_derivation.outcome=%q want no_specific_theme_seed; derivation=%v", got, derivation)
	}
}

func TestFacadeQuestionAnswerEmptyCohortPrecedesSeedGuidance(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	seedBusinessAnalysisMCPFixtures(t, store)

	server := NewServerWithOptions(store, "gongmcp", "test",
		WithToolAllowlist([]string{FacadeToolAnalyze}),
		WithFacadeRoutedToolAllowlist([]string{internalRoutedToolQuestionAnswer}),
	)
	result, err := server.executeFacadeDispatch(t.Context(), FacadeToolAnalyze, mustFacadeArgs(t, OpQuestionAnswer, map[string]any{
		"question":      "What are the main themes showing up in business discovery this quarter?",
		"role_context":  "sales-manager",
		"output_intent": "themes",
		"filter": map[string]any{
			"from_date":         "2030-01-01",
			"to_date":           "2030-01-31",
			"transcript_status": "present",
			"limit":             5,
		},
		"limit": 5,
	}))
	if err != nil {
		t.Fatalf("empty cohort question.answer dispatch: %v", err)
	}
	if result.IsError {
		t.Fatalf("empty cohort question.answer should return structured payload, not tool error: %+v", result)
	}
	wrapper := decodeFacadeWrapper(t, result)
	inner, ok := wrapper["result"].(map[string]any)
	if !ok {
		t.Fatalf("missing inner result: %T", wrapper["result"])
	}
	if got, _ := inner["status"].(string); got != "empty_cohort" {
		t.Fatalf("status=%q want empty_cohort; inner=%v", got, inner)
	}
	if got := strings.ToLower(mustJSONText(t, inner["warnings"])); !strings.Contains(got, "empty_cohort") {
		t.Fatalf("warnings should explain empty cohort before seed guidance: %s", got)
	}
}

func TestDiscoverBusinessAnalysisThemesFiltersSeedlessNoise(t *testing.T) {
	t.Parallel()

	themes := discoverBusinessAnalysisThemes([]businessAnalysisItem{
		{Snippet: "Please press one to leave a message after the tone. John said pricing pricing pricing approval and implementation effort came up."},
		{Snippet: "Please press zero for the main desk. Sarah said pricing review and manual order entry came up."},
	}, 8)
	text := strings.ToLower(mustJSONText(t, themes))
	for _, noisy := range []string{"please", "press", "leave", "message", "tone", "zero", "main", "desk", "john", "sarah"} {
		if strings.Contains(text, `"`+noisy+`"`) {
			t.Fatalf("seedless candidate themes should filter IVR/voicemail noise %q: %s", noisy, text)
		}
	}
	if !strings.Contains(text, `"pricing"`) {
		t.Fatalf("seedless candidate themes should retain business term pricing: %s", text)
	}
	if strings.Contains(text, `"support_count":4`) {
		t.Fatalf("candidate theme support should count per snippet, not repeated tokens inside one snippet: %s", text)
	}
}

func TestFacadeExtractObjectionSignalsDefaultsToExternalOrUnknownEvidenceAndProfiles(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	seedExternalAndInternalObjectionSignals(t, store)

	server := NewServerWithOptions(store, "gongmcp", "test",
		WithToolAllowlist([]string{FacadeToolAnalyze}),
		WithFacadeRoutedToolAllowlist([]string{internalRoutedToolObjectionSignals}),
	)
	result, err := server.executeFacadeDispatch(t.Context(), FacadeToolAnalyze, mustFacadeArgs(t, OpExtractObjectionSignals, map[string]any{
		"filter": map[string]any{
			"from_date":         "2026-04-01",
			"to_date":           "2026-04-30",
			"transcript_status": "present",
		},
		"topics":        []string{"pricing"},
		"field_profile": "attribution",
		"limit":         10,
	}))
	if err != nil {
		t.Fatalf("extract objection signals dispatch: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected isError: %+v", result)
	}
	wrapper := decodeFacadeWrapper(t, result)
	inner, ok := wrapper["result"].(map[string]any)
	if !ok {
		t.Fatalf("missing inner result: %T", wrapper["result"])
	}
	if got, _ := inner["operation"].(string); got != OpExtractObjectionSignals {
		t.Fatalf("operation=%q want %q: %v", got, OpExtractObjectionSignals, inner)
	}
	if got, _ := inner["speaker_role_filter"].(string); got != speakerRoleExternalOrUnknown {
		t.Fatalf("speaker_role_filter=%q want %s: %v", got, speakerRoleExternalOrUnknown, inner)
	}
	buckets, _ := inner["buckets"].([]any)
	if len(buckets) != 1 {
		t.Fatalf("buckets len=%d want 1: %v", len(buckets), inner)
	}
	bucket, _ := buckets[0].(map[string]any)
	evidence, _ := bucket["evidence"].([]any)
	if len(evidence) != 2 {
		t.Fatalf("external-or-unknown evidence len=%d want 2: %v", len(evidence), bucket)
	}
	var externalRow map[string]any
	var unknownRow map[string]any
	for _, raw := range evidence {
		row, _ := raw.(map[string]any)
		switch row["speaker_role"] {
		case sqlite.SpeakerRoleExternal:
			externalRow = row
		case sqlite.SpeakerRoleUnknown:
			unknownRow = row
		}
	}
	if externalRow == nil || unknownRow == nil {
		t.Fatalf("external_or_unknown should retain one external and one unknown row: %v", evidence)
	}
	if got, _ := externalRow["account_name"].(string); got != "BuyerCo" {
		t.Fatalf("field_profile=attribution should expose account_name BuyerCo, got %q row=%v", got, externalRow)
	}
	if text := strings.ToLower(mustJSONText(t, evidence)); strings.Contains(text, "rep script") {
		t.Fatalf("internal seller evidence leaked into external objection extraction: %s", text)
	}
}

func seedExternalAndInternalObjectionSignals(t *testing.T, store *sqlite.Store) {
	t.Helper()
	ctx := context.Background()
	for _, raw := range []json.RawMessage{
		mustJSON(t, map[string]any{
			"id":        "call_external_pricing_objection",
			"title":     "External pricing objection",
			"started":   "2026-04-10T15:00:00Z",
			"duration":  1200,
			"system":    "Zoom",
			"direction": "Conference",
			"scope":     "External",
			"parties": []any{
				map[string]any{"speakerId": "buyer_external_1", "affiliation": "External", "title": "VP Operations"},
			},
			"context": []any{
				map[string]any{
					"objectType": "Account",
					"id":         "acct_buyerco",
					"name":       "BuyerCo",
					"fields": []any{
						map[string]any{"name": "Name", "value": "BuyerCo"},
						map[string]any{"name": "Industry", "value": "Manufacturing"},
					},
				},
			},
		}),
		mustJSON(t, map[string]any{
			"id":        "call_internal_pricing_script",
			"title":     "Internal pricing script",
			"started":   "2026-04-11T15:00:00Z",
			"duration":  900,
			"system":    "Zoom",
			"direction": "Conference",
			"scope":     "External",
			"parties": []any{
				map[string]any{"speakerId": "rep_internal_1", "affiliation": "Internal", "title": "Account Executive"},
			},
		}),
		mustJSON(t, map[string]any{
			"id":        "call_unknown_pricing_question",
			"title":     "Unknown-role pricing question",
			"started":   "2026-04-12T15:00:00Z",
			"duration":  900,
			"system":    "Zoom",
			"direction": "Conference",
			"scope":     "External",
			"parties": []any{
				map[string]any{"speakerId": "unknown_1"},
			},
		}),
	} {
		if _, err := store.UpsertCall(ctx, raw); err != nil {
			t.Fatalf("upsert objection signal call: %v", err)
		}
	}
	for _, item := range []struct {
		callID    string
		speakerID string
		text      string
	}{
		{"call_external_pricing_objection", "buyer_external_1", "Pricing is a blocker for our finance team."},
		{"call_internal_pricing_script", "rep_internal_1", "Pricing objection rep script should not be included."},
		{"call_unknown_pricing_question", "unknown_1", "Pricing is unclear without the implementation plan."},
	} {
		if _, err := store.UpsertTranscript(ctx, mustJSON(t, map[string]any{
			"callTranscripts": []any{
				map[string]any{
					"callId": item.callID,
					"transcript": []any{
						map[string]any{
							"speakerId": item.speakerID,
							"sentences": []any{
								map[string]any{"start": 1000, "end": 2500, "text": item.text},
							},
						},
					},
				},
			},
		})); err != nil {
			t.Fatalf("upsert objection signal transcript: %v", err)
		}
	}
}
