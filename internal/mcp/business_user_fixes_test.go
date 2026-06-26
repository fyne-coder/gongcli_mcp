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

func TestFieldProfileQuoteCompactAliasesToLimited(t *testing.T) {
	t.Parallel()

	got, err := normalizeFieldProfile("quote_compact")
	if err != nil {
		t.Fatalf("normalize quote_compact: %v", err)
	}
	if got != fieldProfileLimited {
		t.Fatalf("quote_compact=%q want %s", got, fieldProfileLimited)
	}
}

func TestFieldProfileSchemaMentionsLimitedEvidenceTextCaveat(t *testing.T) {
	t.Parallel()

	desc, _ := fieldProfileSchema()["description"].(string)
	if !strings.Contains(desc, "does not redact") || !strings.Contains(desc, "transcript") {
		t.Fatalf("field_profile schema missing limited evidence-text caveat: %q", desc)
	}
}

func TestBusinessEvidencePolicyIncludesHostDisplayDefaults(t *testing.T) {
	t.Parallel()

	payload := defaultBusinessEvidencePolicy().payload()
	display, ok := payload["host_display_policy"].(map[string]any)
	if !ok {
		t.Fatalf("missing host_display_policy: %v", payload)
	}
	if got, _ := display["tool_trace"].(string); got != "omit_unless_requested" {
		t.Fatalf("tool_trace=%q want omit_unless_requested: %v", got, display)
	}
	if got, _ := display["include_exact_mcp_operations"].(bool); got {
		t.Fatalf("include_exact_mcp_operations should default false: %v", display)
	}
	if got, _ := display["delta_language"].(string); !strings.Contains(got, "avoid negative deltas") {
		t.Fatalf("delta_language should tell hosts to avoid negative business deltas: %v", display)
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
	policy, _ := inner["evidence_policy"].(map[string]any)
	if got, _ := policy["default_speaker_role"].(string); got != speakerRoleExternalOrUnknown {
		t.Fatalf("evidence_policy.default_speaker_role=%q want %s: %v", got, speakerRoleExternalOrUnknown, policy)
	}
	if got, _ := inner["evidence_type"].(string); got != evidenceTypeGongAICondensedCandidate {
		t.Fatalf("evidence_type=%q want %s: %v", got, evidenceTypeGongAICondensedCandidate, inner)
	}
	readiness, _ := inner["dimension_readiness"].(map[string]any)
	if readiness["loss_reason"] != "unavailable_unmapped" || readiness["methodology"] != "unavailable_unmapped" {
		t.Fatalf("dimension_readiness should flag unmapped loss_reason/methodology: %v", readiness)
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

func TestBusinessWorkbenchRoutesSurfaceDataReadinessCaveats(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	seedBusinessAnalysisMCPFixtures(t, store)

	server := NewServerWithOptions(store, "gongmcp", "test",
		WithToolAllowlist([]string{FacadeToolAnalyze}),
		WithFacadeRoutedToolAllowlist([]string{
			internalRoutedToolThemeIntelReport,
			internalRoutedToolBuyerQuestions,
			internalRoutedToolObjectionSignals,
		}),
	)
	tests := []struct {
		name      string
		operation string
		args      map[string]any
	}{
		{
			name:      "theme report",
			operation: OpThemeIntelReport,
			args: map[string]any{
				"theme_query": "pricing",
				"filter": map[string]any{
					"from_date":         "2026-04-01",
					"to_date":           "2026-04-30",
					"transcript_status": "present",
				},
				"limit": 5,
			},
		},
		{
			name:      "buyer questions",
			operation: OpExtractBuyerQuestions,
			args: map[string]any{
				"topics": []string{"pricing"},
				"filter": map[string]any{
					"from_date":         "2026-04-01",
					"to_date":           "2026-04-30",
					"transcript_status": "present",
				},
				"limit": 5,
			},
		},
		{
			name:      "objection signals",
			operation: OpExtractObjectionSignals,
			args: map[string]any{
				"topics": []string{"pricing"},
				"filter": map[string]any{
					"from_date":         "2026-04-01",
					"to_date":           "2026-04-30",
					"transcript_status": "present",
				},
				"limit": 5,
			},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			result, err := server.executeFacadeDispatch(t.Context(), FacadeToolAnalyze, mustFacadeArgs(t, tt.operation, tt.args))
			if err != nil {
				t.Fatalf("%s dispatch: %v", tt.operation, err)
			}
			if result.IsError {
				t.Fatalf("%s returned isError: %+v", tt.operation, result)
			}
			wrapper := decodeFacadeWrapper(t, result)
			inner, _ := wrapper["result"].(map[string]any)
			if _, ok := inner["data_readiness_caveats"]; !ok {
				t.Fatalf("%s missing data_readiness_caveats: %+v", tt.operation, inner)
			}
			readiness, _ := inner["dimension_readiness"].(map[string]any)
			if readiness["loss_reason"] != "unavailable_unmapped" || readiness["methodology"] != "unavailable_unmapped" {
				t.Fatalf("%s missing unmapped dimension readiness: %v", tt.operation, readiness)
			}
			rendered := mustJSONText(t, inner)
			if !strings.Contains(rendered, "loss_reason_profile_dependent") || !strings.Contains(rendered, "methodology_profile_dependent") {
				t.Fatalf("%s should surface profile-dependent caveats in payload/warnings: %s", tt.operation, rendered)
			}
		})
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
	for _, noisy := range []string{"please", "press", "leave", "message", "tone", "zero", "main", "desk", "john", "sarah", "just", "gonna", "guys", "anybody", "depends"} {
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

func TestDiversifyBusinessAnalysisItemsByCallCapsSingleCallDominance(t *testing.T) {
	t.Parallel()

	items := []businessAnalysisItem{
		{CallRef: "call_ref_one", SegmentIndex: 1, Snippet: "pricing"},
		{CallRef: "call_ref_one", SegmentIndex: 2, Snippet: "pricing"},
		{CallRef: "call_ref_one", SegmentIndex: 3, Snippet: "pricing"},
		{CallRef: "call_ref_one", SegmentIndex: 4, Snippet: "pricing"},
		{CallRef: "call_ref_two", SegmentIndex: 1, Snippet: "implementation"},
	}
	got := diversifyBusinessAnalysisItemsByCall(items, 2, 10)
	if len(got) != 3 {
		t.Fatalf("diversified len=%d want 3: %v", len(got), got)
	}
	seenOne := 0
	for _, item := range got {
		if item.CallRef == "call_ref_one" {
			seenOne++
		}
	}
	if seenOne != 2 {
		t.Fatalf("call_ref_one count=%d want 2: %v", seenOne, got)
	}
}

func TestSpeakerAttributionSummarySkipsCallRowsWithoutSpeakerFields(t *testing.T) {
	t.Parallel()

	if got := speakerAttributionSummaryFromItems([]businessAnalysisItem{
		{CallRef: "call_ref_one", AccountIndustry: "Manufacturing"},
		{CallRef: "call_ref_two", AccountIndustry: "Retail"},
	}); got != nil {
		t.Fatalf("cohort call rows without speaker fields should not be summarized as unknown speaker evidence: %v", got)
	}

	got := speakerAttributionSummaryFromItems([]businessAnalysisItem{
		{CallRef: "call_ref_one", SpeakerRole: sqlite.SpeakerRoleUnknown, SpeakerRoleStatus: sqlite.SpeakerRoleStatusAffiliationMissing},
		{CallRef: "call_ref_two", SpeakerRole: sqlite.SpeakerRoleExternal, SpeakerRoleStatus: sqlite.SpeakerRoleStatusAvailable},
	})
	if got["unknown"] != 1 || got["affiliation_missing"] != 1 || got["external"] != 1 {
		t.Fatalf("evidence rows with speaker fields should still summarize attribution: %v", got)
	}
}

func TestFacadeAnalyzeThemesDiscoverSeedlessSuppressesFillerRowsWhenNoBusinessCandidates(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	ctx := context.Background()
	if _, err := store.UpsertCall(ctx, mustJSON(t, map[string]any{
		"id":        "call_seedless_filler_only",
		"title":     "Business discovery filler only",
		"started":   "2026-04-18T15:00:00Z",
		"duration":  1200,
		"system":    "Zoom",
		"direction": "Conference",
		"scope":     "External",
		"parties": []any{
			map[string]any{"speakerId": "buyer_filler", "affiliation": "External", "title": "VP Operations"},
		},
	})); err != nil {
		t.Fatalf("upsert filler call: %v", err)
	}
	if _, err := store.UpsertTranscript(ctx, mustJSON(t, map[string]any{
		"callTranscripts": []any{
			map[string]any{
				"callId": "call_seedless_filler_only",
				"transcript": []any{
					map[string]any{
						"speakerId": "buyer_filler",
						"sentences": []any{
							map[string]any{"start": 1000, "end": 2000, "text": "Hey Amanda, sorry I am a little late here."},
							map[string]any{"start": 2200, "end": 3500, "text": "No, that is fine. How are you doing today?"},
						},
					},
				},
			},
		},
	})); err != nil {
		t.Fatalf("upsert filler transcript: %v", err)
	}

	server := NewServerWithOptions(store, "gongmcp", "test",
		WithToolAllowlist([]string{FacadeToolAnalyze}),
		WithFacadeRoutedToolAllowlist([]string{"discover_themes_in_cohort"}),
	)
	result, err := server.executeFacadeDispatch(t.Context(), FacadeToolAnalyze, mustFacadeArgs(t, OpAnalyzeThemesDiscover, map[string]any{
		"filter": map[string]any{
			"title_query": "Business discovery filler only",
			"from_date":   "2026-04-01",
			"to_date":     "2026-04-30",
		},
		"limit": 5,
	}))
	if err != nil {
		t.Fatalf("analyze themes discover dispatch: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected isError: %+v", result)
	}
	wrapper := decodeFacadeWrapper(t, result)
	inner, _ := wrapper["result"].(map[string]any)
	if got, _ := inner["status"].(string); got != "needs_theme_seed" {
		t.Fatalf("status=%q want needs_theme_seed when only filler remains: %v", got, inner)
	}
	if rows, _ := inner["results"].([]any); len(rows) != 0 {
		t.Fatalf("seedless filler-only path should not emit raw transcript sample rows: %v", rows)
	}
	text := strings.ToLower(result.Content[0].Text)
	for _, filler := range []string{"little late", "doing today"} {
		if strings.Contains(text, filler) {
			t.Fatalf("seedless filler-only response leaked filler snippet %q: %s", filler, text)
		}
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
	if got, _ := inner["evidence_type"].(string); got != evidenceTypeKeywordSynonym {
		t.Fatalf("evidence_type=%q want %s: %v", got, evidenceTypeKeywordSynonym, inner)
	}
	if got := strings.ToLower(mustJSONText(t, inner["answer_contract"])); !strings.Contains(got, "keyword/synonym") || !strings.Contains(got, "unattributed") {
		t.Fatalf("answer_contract should tell hosts how to describe deterministic and unknown evidence: %s", got)
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
	speakerSummary, _ := inner["speaker_attribution_summary"].(map[string]any)
	if speakerSummary["unknown"] == nil || speakerSummary["affiliation_missing"] == nil {
		t.Fatalf("speaker_attribution_summary should expose unknown/affiliation_missing counts: %v", speakerSummary)
	}
	if warnings := strings.ToLower(mustJSONText(t, inner["warnings"])); !strings.Contains(warnings, "unattributed evidence") {
		t.Fatalf("warnings should tell hosts not to call unknown rows buyer speech: %s", warnings)
	}
}

func TestFacadeExtractBuyerQuestionsUsesTCSpecificSynonymExpansion(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	ctx := context.Background()
	if _, err := store.UpsertCall(ctx, mustJSON(t, map[string]any{
		"id":        "call_rollout_synonym_001",
		"title":     "External rollout discussion",
		"started":   "2026-04-14T15:00:00Z",
		"duration":  1200,
		"system":    "Zoom",
		"direction": "Conference",
		"scope":     "External",
		"parties": []any{
			map[string]any{"speakerId": "buyer_rollout_1", "affiliation": "External", "title": "Director of IT"},
		},
	})); err != nil {
		t.Fatalf("upsert rollout call: %v", err)
	}
	if _, err := store.UpsertTranscript(ctx, mustJSON(t, map[string]any{
		"callTranscripts": []any{
			map[string]any{
				"callId": "call_rollout_synonym_001",
				"transcript": []any{
					map[string]any{
						"speakerId": "buyer_rollout_1",
						"sentences": []any{
							map[string]any{"start": 1000, "end": 2500, "text": "The rollout plan will need IT bandwidth before launch."},
						},
					},
				},
			},
		},
	})); err != nil {
		t.Fatalf("upsert rollout transcript: %v", err)
	}

	server := NewServerWithOptions(store, "gongmcp", "test",
		WithToolAllowlist([]string{FacadeToolAnalyze}),
		WithFacadeRoutedToolAllowlist([]string{internalRoutedToolBuyerQuestions}),
	)
	result, err := server.executeFacadeDispatch(t.Context(), FacadeToolAnalyze, mustFacadeArgs(t, OpExtractBuyerQuestions, map[string]any{
		"filter": map[string]any{
			"from_date":         "2026-04-01",
			"to_date":           "2026-04-30",
			"transcript_status": "present",
		},
		"topics": []string{"implementation"},
		"limit":  10,
	}))
	if err != nil {
		t.Fatalf("extract buyer questions dispatch: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected isError: %+v", result)
	}
	wrapper := decodeFacadeWrapper(t, result)
	inner, _ := wrapper["result"].(map[string]any)
	buckets, _ := inner["buckets"].([]any)
	if len(buckets) != 1 {
		t.Fatalf("buckets len=%d want 1: %v", len(buckets), inner)
	}
	bucket, _ := buckets[0].(map[string]any)
	if got, _ := bucket["evidence_count"].(float64); got < 1 {
		t.Fatalf("implementation synonym expansion returned no evidence: %v", bucket)
	}
	expanded := strings.ToLower(mustJSONText(t, bucket["expanded_queries"]))
	if !strings.Contains(expanded, "rollout") {
		t.Fatalf("expanded_queries missing rollout synonym: %s", expanded)
	}
	evidence := strings.ToLower(mustJSONText(t, bucket["evidence"]))
	if !strings.Contains(evidence, "rollout plan") {
		t.Fatalf("expected rollout evidence from implementation topic: %s", evidence)
	}
	securityExpanded := strings.ToLower(mustJSONText(t, businessSignalTopicQueries(OpExtractBuyerQuestions, "security")))
	if !strings.Contains(securityExpanded, "infosec") || !strings.Contains(securityExpanded, "compliance review") {
		t.Fatalf("buyer-question security aliases should match objection alias breadth: %s", securityExpanded)
	}
}

func TestFacadeExtractBuyerQuestionsDefaultsExcludeVoicemailAndIVRSupportRows(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	ctx := context.Background()
	for _, raw := range []json.RawMessage{
		mustJSON(t, map[string]any{
			"id":        "call_support_outbound_ivr",
			"title":     "Outbound support IVR",
			"started":   "2026-04-20T15:00:00Z",
			"duration":  45,
			"system":    "Telephony",
			"direction": "Outbound",
			"scope":     "External",
			"parties": []any{
				map[string]any{"speakerId": "ivr_support_1", "affiliation": "External", "title": "Customer Support"},
			},
		}),
		mustJSON(t, map[string]any{
			"id":        "call_support_closed_transfer",
			"title":     "Closed review support transfer",
			"started":   "2026-04-21T15:00:00Z",
			"duration":  1200,
			"system":    "Zoom",
			"direction": "Conference",
			"scope":     "External",
			"parties": []any{
				map[string]any{"speakerId": "ivr_support_2", "affiliation": "External", "title": "Customer Support"},
			},
			"context": []any{
				map[string]any{
					"objectType": "Opportunity",
					"id":         "opp_support_closed",
					"name":       "Support Closed Lost",
					"fields": []any{
						map[string]any{"name": "StageName", "value": "Closed Lost"},
					},
				},
			},
		}),
		mustJSON(t, map[string]any{
			"id":        "call_support_real_buyer",
			"title":     "Business discovery support question",
			"started":   "2026-04-22T15:00:00Z",
			"duration":  1200,
			"system":    "Zoom",
			"direction": "Conference",
			"scope":     "External",
			"parties": []any{
				map[string]any{"speakerId": "buyer_support_1", "affiliation": "External", "title": "VP Operations"},
			},
		}),
	} {
		if _, err := store.UpsertCall(ctx, raw); err != nil {
			t.Fatalf("upsert support fixture call: %v", err)
		}
	}
	for _, raw := range []json.RawMessage{
		mustJSON(t, map[string]any{
			"callTranscripts": []any{map[string]any{
				"callId": "call_support_outbound_ivr",
				"transcript": []any{
					map[string]any{
						"speakerId": "ivr_support_1",
						"sentences": []any{
							map[string]any{"start": 1000, "end": 2500, "text": "Our customer support center is open from eight AM to five PM central."},
						},
					},
				},
			}},
		}),
		mustJSON(t, map[string]any{
			"callTranscripts": []any{map[string]any{
				"callId": "call_support_closed_transfer",
				"transcript": []any{
					map[string]any{
						"speakerId": "ivr_support_2",
						"sentences": []any{
							map[string]any{"start": 1000, "end": 2500, "text": "This call will be recorded for quality and training purposes. Transferring you to customer support."},
						},
					},
				},
			}},
		}),
		mustJSON(t, map[string]any{
			"callTranscripts": []any{map[string]any{
				"callId": "call_support_real_buyer",
				"transcript": []any{
					map[string]any{
						"speakerId": "buyer_support_1",
						"sentences": []any{
							map[string]any{"start": 1000, "end": 2500, "text": "What post implementation support do you provide after go live?"},
						},
					},
				},
			}},
		}),
	} {
		if _, err := store.UpsertTranscript(ctx, raw); err != nil {
			t.Fatalf("upsert support transcripts: %v", err)
		}
	}

	server := NewServerWithOptions(store, "gongmcp", "test",
		WithToolAllowlist([]string{FacadeToolAnalyze}),
		WithFacadeRoutedToolAllowlist([]string{internalRoutedToolBuyerQuestions}),
	)
	result, err := server.executeFacadeDispatch(t.Context(), FacadeToolAnalyze, mustFacadeArgs(t, OpExtractBuyerQuestions, map[string]any{
		"filter": map[string]any{
			"from_date": "2026-04-01",
			"to_date":   "2026-04-30",
		},
		"topics": []string{"support"},
		"limit":  10,
	}))
	if err != nil {
		t.Fatalf("extract buyer questions support dispatch: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected isError: %+v", result)
	}
	wrapper := decodeFacadeWrapper(t, result)
	inner, _ := wrapper["result"].(map[string]any)
	scope, _ := inner["searched_scope"].(map[string]any)
	if got := strings.ToLower(mustJSONText(t, scope["exclude_lifecycle_buckets"])); !strings.Contains(got, "outbound_prospecting") {
		t.Fatalf("support extraction should default exclude outbound_prospecting: %v", scope)
	}
	if got, _ := scope["exclude_likely_voicemail"].(bool); !got {
		t.Fatalf("support extraction should default exclude likely voicemail: %v", scope)
	}
	rendered := strings.ToLower(mustJSONText(t, inner["buckets"]))
	if !strings.Contains(rendered, "post implementation support") {
		t.Fatalf("support extraction should keep real buyer support question: %s", rendered)
	}
	for _, noisy := range []string{"customer support center", "transferring you to customer support", "quality and training purposes"} {
		if strings.Contains(rendered, noisy) {
			t.Fatalf("support extraction included IVR row %q: %s", noisy, rendered)
		}
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

func TestFacadeCohortTokenHandoffBuildToInspect(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	seedBusinessAnalysisMCPFixtures(t, store)

	server := NewServerWithOptions(store, "gongmcp", "test",
		WithToolAllowlist([]string{FacadeToolAnalyze}),
		WithFacadeRoutedToolAllowlist([]string{"build_call_cohort", "inspect_call_cohort"}),
	)
	filter := map[string]any{
		"from_date":         "2026-04-01",
		"to_date":           "2026-04-30",
		"transcript_status": "present",
	}
	buildResult, err := server.executeFacadeDispatch(t.Context(), FacadeToolAnalyze, mustFacadeArgs(t, OpAnalyzeCohortBuild, map[string]any{
		"filter": filter,
		"limit":  5,
	}))
	if err != nil {
		t.Fatalf("analyze.cohort.build dispatch: %v", err)
	}
	if buildResult.IsError {
		t.Fatalf("unexpected build isError: %+v", buildResult)
	}
	buildWrapper := decodeFacadeWrapper(t, buildResult)
	buildInner, _ := buildWrapper["result"].(map[string]any)
	token, _ := buildInner["cohort_token"].(string)
	if !strings.HasPrefix(token, cohortTokenPrefix) {
		t.Fatalf("build missing cohort_token: %v", buildInner["cohort_token"])
	}

	inspectResult, err := server.executeFacadeDispatch(t.Context(), FacadeToolAnalyze, mustFacadeArgs(t, OpAnalyzeCohortInspect, map[string]any{
		"cohort_token": token,
		"limit":        5,
	}))
	if err != nil {
		t.Fatalf("analyze.cohort.inspect dispatch: %v", err)
	}
	if inspectResult.IsError {
		t.Fatalf("unexpected inspect isError: %+v", inspectResult)
	}
	inspectWrapper := decodeFacadeWrapper(t, inspectResult)
	inspectInner, _ := inspectWrapper["result"].(map[string]any)
	if inspectInner["cohort_token"] != token {
		t.Fatalf("inspect cohort_token=%v want %q", inspectInner["cohort_token"], token)
	}
	if buildInner["cohort_id"] != inspectInner["cohort_id"] {
		t.Fatalf("cohort_id mismatch build=%v inspect=%v", buildInner["cohort_id"], inspectInner["cohort_id"])
	}

	_, err = server.executeFacadeDispatch(t.Context(), FacadeToolAnalyze, mustFacadeArgs(t, OpAnalyzeCohortInspect, map[string]any{
		"cohort_token": token,
		"filter": map[string]any{
			"from_date": "2026-01-01",
			"to_date":   "2026-01-31",
		},
	}))
	if err == nil || !strings.Contains(err.Error(), "cohort_token") {
		t.Fatalf("expected cohort_token mismatch error, got %v", err)
	}
}
