package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
)

// themeIntelReportOpName is the public dotted operation name surfaced by the
// stable facade. Tests reference it as a string literal so the file remains
// compile-clean while the implementation is missing during red TDD; the
// matching constant `OpThemeIntelReport` exists once the operation lands.
const themeIntelReportOpName = "theme_intelligence_report"

// seedThemeIntelReportFixtures inserts a synthetic, self-contained cohort that
// exercises every assertion required by the Phase C contract:
//   - two quarters (2026-Q1, 2026-Q2)
//   - two industries (Manufacturing, Financial Services)
//   - two persona buckets (procurement, finance)
//   - one closed-won and one closed-lost call with a populated loss reason
//   - per-call transcripts mentioning the seeded theme query
//   - a unique probe substring on the closed_lost call so the assertion that
//     full transcript text never escapes the bounded excerpt path can fail
//     deterministically if the implementation regresses.
//
// All values are deliberately synthetic and do not include real customer
// names, raw CRM identifiers, or real account/title text.
func seedThemeIntelReportFixtures(t *testing.T, store *sqlite.Store) {
	t.Helper()

	ctx := context.Background()

	// Q1 / Manufacturing / procurement / closed_won
	wonCall := mustJSON(t, map[string]any{
		"id":        "call_theme_q1_won_001",
		"title":     "Theme intel Q1 Manufacturing closed-won discovery",
		"started":   "2026-02-12T15:00:00Z",
		"duration":  1800,
		"system":    "Zoom",
		"direction": "Conference",
		"scope":     "External",
		"parties": []any{
			map[string]any{"speakerId": "speaker_q1_won_buyer", "title": "VP Procurement"},
			map[string]any{"speakerId": "speaker_q1_won_rep", "title": "Account Executive"},
		},
		"context": []any{
			map[string]any{
				"objectType": "Account",
				"id":         "acct_theme_q1_won",
				"fields": []any{
					map[string]any{"name": "Name", "value": "Theme Intel Manufacturing Account"},
					map[string]any{"name": "Industry", "value": "Manufacturing"},
				},
			},
			map[string]any{
				"objectType": "Opportunity",
				"id":         "opp_theme_q1_won",
				"fields": []any{
					map[string]any{"name": "Name", "value": "Theme Intel Q1 Manufacturing Opportunity"},
					map[string]any{"name": "StageName", "value": "Closed Won"},
				},
			},
		},
	})
	// Q1 / Manufacturing / procurement / closed_lost with LossReason
	lostCall := mustJSON(t, map[string]any{
		"id":        "call_theme_q1_lost_002",
		"title":     "Theme intel Q1 Manufacturing closed-lost review",
		"started":   "2026-03-04T15:00:00Z",
		"duration":  1500,
		"system":    "Zoom",
		"direction": "Conference",
		"scope":     "External",
		"parties": []any{
			map[string]any{"speakerId": "speaker_q1_lost_buyer", "title": "Procurement Director"},
			map[string]any{"speakerId": "speaker_q1_lost_rep", "title": "Sales Engineer"},
		},
		"context": []any{
			map[string]any{
				"objectType": "Account",
				"id":         "acct_theme_q1_lost",
				"fields": []any{
					map[string]any{"name": "Name", "value": "Theme Intel Manufacturing Loss Account"},
					map[string]any{"name": "Industry", "value": "Manufacturing"},
				},
			},
			map[string]any{
				"objectType": "Opportunity",
				"id":         "opp_theme_q1_lost",
				"fields": []any{
					map[string]any{"name": "Name", "value": "Theme Intel Q1 Manufacturing Loss Opportunity"},
					map[string]any{"name": "StageName", "value": "Closed Lost"},
					map[string]any{"name": "LossReason", "value": "Pricing too high"},
				},
			},
		},
	})
	// Q2 / Financial Services / finance / Negotiation
	q2NegCall := mustJSON(t, map[string]any{
		"id":        "call_theme_q2_neg_003",
		"title":     "Theme intel Q2 Financial Services negotiation",
		"started":   "2026-04-15T15:00:00Z",
		"duration":  1500,
		"system":    "Zoom",
		"direction": "Conference",
		"scope":     "External",
		"parties": []any{
			map[string]any{"speakerId": "speaker_q2_neg_buyer", "title": "CFO"},
			map[string]any{"speakerId": "speaker_q2_neg_rep", "title": "Account Executive"},
		},
		"context": []any{
			map[string]any{
				"objectType": "Account",
				"id":         "acct_theme_q2_neg",
				"fields": []any{
					map[string]any{"name": "Name", "value": "Theme Intel Financial Account"},
					map[string]any{"name": "Industry", "value": "Financial Services"},
				},
			},
			map[string]any{
				"objectType": "Opportunity",
				"id":         "opp_theme_q2_neg",
				"fields": []any{
					map[string]any{"name": "Name", "value": "Theme Intel Q2 Financial Negotiation"},
					map[string]any{"name": "StageName", "value": "Negotiation"},
				},
			},
		},
	})
	// Q2 / Financial Services / finance / Discovery
	q2DiscCall := mustJSON(t, map[string]any{
		"id":        "call_theme_q2_disc_004",
		"title":     "Theme intel Q2 Financial Services discovery",
		"started":   "2026-05-21T15:00:00Z",
		"duration":  1500,
		"system":    "Zoom",
		"direction": "Conference",
		"scope":     "External",
		"parties": []any{
			map[string]any{"speakerId": "speaker_q2_disc_buyer", "title": "Finance Manager"},
			map[string]any{"speakerId": "speaker_q2_disc_rep", "title": "Sales Engineer"},
		},
		"context": []any{
			map[string]any{
				"objectType": "Account",
				"id":         "acct_theme_q2_disc",
				"fields": []any{
					map[string]any{"name": "Name", "value": "Theme Intel Financial Discovery Account"},
					map[string]any{"name": "Industry", "value": "Financial Services"},
				},
			},
			map[string]any{
				"objectType": "Opportunity",
				"id":         "opp_theme_q2_disc",
				"fields": []any{
					map[string]any{"name": "Name", "value": "Theme Intel Q2 Financial Discovery"},
					map[string]any{"name": "StageName", "value": "Discovery"},
				},
			},
		},
	})
	vmCall := mustJSON(t, map[string]any{
		"id":        "call_theme_q2_vm_005",
		"title":     "Theme intel Q2 outbound voicemail",
		"started":   "2026-05-22T15:00:00Z",
		"duration":  35,
		"system":    "Upload API",
		"direction": "Outbound",
		"scope":     "External",
	})

	for _, raw := range []json.RawMessage{wonCall, lostCall, q2NegCall, q2DiscCall, vmCall} {
		if _, err := store.UpsertCall(ctx, raw); err != nil {
			t.Fatalf("upsert theme intel call: %v", err)
		}
	}

	transcriptItems := []struct {
		callID string
		lines  []string
	}{
		{
			callID: "call_theme_q1_won_001",
			lines: []string{
				"Manual order entry is the biggest pain point for our procurement team.",
				"We spend hours fixing manual order entry mistakes every week.",
			},
		},
		{
			callID: "call_theme_q1_lost_002",
			lines: []string{
				"Manual order entry is painful but pricing was too high to justify.",
				"FULL_TRANSCRIPT_TEXT_LEAK_PROBE_NEVER_RETURN_ENTIRE_BODY (this string must never leak verbatim into the report payload because the report only emits bounded transcript snippets, not full transcripts).",
			},
		},
		{
			callID: "call_theme_q2_neg_003",
			lines: []string{
				"Manual order entry slows our finance close every quarter.",
				"Removing manual order entry from accounts payable is the priority.",
			},
		},
		{
			callID: "call_theme_q2_disc_004",
			lines: []string{
				"Manual order entry creates reconciliation pain in finance.",
			},
		},
		{
			callID: "call_theme_q2_vm_005",
			lines: []string{
				"Please leave a detailed message after the tone.",
			},
		},
	}
	for _, item := range transcriptItems {
		sentences := make([]any, 0, len(item.lines))
		for idx, line := range item.lines {
			sentences = append(sentences, map[string]any{
				"start": int64(1000 + idx*1000),
				"end":   int64(1500 + idx*1000),
				"text":  line,
			})
		}
		raw := mustJSON(t, map[string]any{
			"callTranscripts": []any{
				map[string]any{
					"callId": item.callID,
					"transcript": []any{
						map[string]any{
							"speakerId": "speaker_" + item.callID + "_buyer",
							"sentences": sentences,
						},
					},
				},
			},
		})
		if _, err := store.UpsertTranscript(ctx, raw); err != nil {
			t.Fatalf("upsert theme intel transcript: %v", err)
		}
	}
}

// fakeThemeIntelReportStore wraps the seeded sqlite store so the test can
// supply Gong AI condensed evidence (highlights / brief / keyPoints) without a
// Postgres backend, and so call-drilldown transcript evidence carries
// attribution metadata that real Postgres-only paths would normally provide.
type fakeThemeIntelReportStore struct {
	*sqlite.Store
	highlights         map[string][]sqlite.AIHighlightRow
	transcriptByCallID map[string][]sqlite.CallDrilldownEvidenceRow
}

func (f *fakeThemeIntelReportStore) ListAIHighlights(_ context.Context, params sqlite.AIHighlightListParams) ([]sqlite.AIHighlightRow, error) {
	out := make([]sqlite.AIHighlightRow, 0)
	for _, id := range params.CallIDs {
		out = append(out, f.highlights[id]...)
	}
	return out, nil
}

func (f *fakeThemeIntelReportStore) CallDrilldownEvidence(ctx context.Context, params sqlite.CallDrilldownEvidenceParams) ([]sqlite.CallDrilldownEvidenceRow, error) {
	if rows, ok := f.transcriptByCallID[params.CallID]; ok {
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
	// Fallback to the underlying SQLite path so calls without a stubbed entry
	// still return real FTS-derived rows.
	return f.Store.CallDrilldownEvidence(ctx, params)
}

func newSeededThemeIntelReportStore(t *testing.T) *fakeThemeIntelReportStore {
	t.Helper()
	base := openSeededStore(t)
	t.Cleanup(func() { base.Close() })
	seedThemeIntelReportFixtures(t, base)

	stamp := "2026-04-25T00:00:00Z"
	store := &fakeThemeIntelReportStore{
		Store: base,
		highlights: map[string][]sqlite.AIHighlightRow{
			"call_theme_q1_won_001": {
				{CallID: "call_theme_q1_won_001", HighlightIndex: 0, HighlightType: "brief", HighlightText: "Procurement is anchored on manual order entry pain.", SourcePath: "content.brief", UpdatedAt: stamp},
				{CallID: "call_theme_q1_won_001", HighlightIndex: 1, HighlightType: "key_point", HighlightText: "Manual order entry will be replaced as part of rollout.", SourcePath: "content.keyPoints", UpdatedAt: stamp},
				{CallID: "call_theme_q1_won_001", HighlightIndex: 2, HighlightType: "key_point", HighlightText: "BigCommerce migration and Oracle Fusion storefront readiness are driving the discovery agenda.", SourcePath: "content.keyPoints", UpdatedAt: stamp},
			},
			"call_theme_q1_lost_002": {
				{CallID: "call_theme_q1_lost_002", HighlightIndex: 0, HighlightType: "brief", HighlightText: "Pricing dominated the conversation; pain acknowledged.", SourcePath: "content.brief", UpdatedAt: stamp},
				{CallID: "call_theme_q1_lost_002", HighlightIndex: 1, HighlightType: "highlight", HighlightText: "Manual order entry pain was real but lost to incumbent.", SourcePath: "content.highlights", UpdatedAt: stamp},
			},
			"call_theme_q2_neg_003": {
				{CallID: "call_theme_q2_neg_003", HighlightIndex: 0, HighlightType: "brief", HighlightText: "Finance close cycle blocked by manual order entry.", SourcePath: "content.brief", UpdatedAt: stamp},
			},
			"call_theme_q2_disc_004": {
				{CallID: "call_theme_q2_disc_004", HighlightIndex: 0, HighlightType: "key_point", HighlightText: "Reconciliation pain in finance from manual order entry.", SourcePath: "content.keyPoints", UpdatedAt: stamp},
			},
			"call_theme_q2_vm_005": {
				{CallID: "call_theme_q2_vm_005", HighlightIndex: 0, HighlightType: "brief", HighlightText: "Please leave a detailed message after the tone.", SourcePath: "content.brief", UpdatedAt: stamp},
			},
		},
		transcriptByCallID: map[string][]sqlite.CallDrilldownEvidenceRow{
			"call_theme_q1_won_001": {
				{
					CallID: "call_theme_q1_won_001", SegmentIndex: 0,
					SpeakerID: "speaker_q1_won_buyer", StartMS: 1000, EndMS: 2500,
					Snippet:               "Manual order entry pain point",
					ContextExcerpt:        "Manual order entry is the biggest pain point for our procurement team.",
					PersonTitleStatus:     sqlite.AttributionStatusAvailable,
					PersonTitleSource:     sqlite.AttributionSourceCallParties,
					AttributionSource:     sqlite.AttributionSourceGongParty,
					AttributionConfidence: sqlite.AttributionConfidenceExactSpeakerID,
					PersonTitle:           "VP Procurement",
				},
			},
			"call_theme_q1_lost_002": {
				{
					CallID: "call_theme_q1_lost_002", SegmentIndex: 0,
					SpeakerID: "speaker_q1_lost_buyer", StartMS: 1000, EndMS: 2500,
					Snippet:               "Manual order entry pain but pricing too high",
					ContextExcerpt:        "Manual order entry is painful but pricing was too high.",
					PersonTitleStatus:     sqlite.AttributionStatusAvailable,
					PersonTitleSource:     sqlite.AttributionSourceCallParties,
					AttributionSource:     sqlite.AttributionSourceGongParty,
					AttributionConfidence: sqlite.AttributionConfidenceExactSpeakerID,
					PersonTitle:           "Procurement Director",
				},
			},
			"call_theme_q2_neg_003": {
				{
					CallID: "call_theme_q2_neg_003", SegmentIndex: 0,
					SpeakerID: "speaker_q2_neg_buyer", StartMS: 1000, EndMS: 2500,
					Snippet:               "Manual order entry slows close",
					ContextExcerpt:        "Manual order entry slows our finance close every quarter.",
					PersonTitleStatus:     sqlite.AttributionStatusAvailable,
					PersonTitleSource:     sqlite.AttributionSourceCallParties,
					AttributionSource:     sqlite.AttributionSourceGongParty,
					AttributionConfidence: sqlite.AttributionConfidenceExactSpeakerID,
					PersonTitle:           "CFO",
				},
			},
			"call_theme_q2_disc_004": {
				{
					CallID: "call_theme_q2_disc_004", SegmentIndex: 0,
					SpeakerID: "speaker_q2_disc_buyer", StartMS: 1000, EndMS: 2500,
					Snippet:               "Manual order entry reconciliation pain",
					ContextExcerpt:        "Manual order entry creates reconciliation pain in finance.",
					PersonTitleStatus:     sqlite.AttributionStatusAvailable,
					PersonTitleSource:     sqlite.AttributionSourceCallParties,
					AttributionSource:     sqlite.AttributionSourceGongParty,
					AttributionConfidence: sqlite.AttributionConfidenceExactSpeakerID,
					PersonTitle:           "Finance Manager",
				},
			},
		},
	}
	return store
}

func TestFacadeAnalyzeThemeIntelReportRegisteredAndDispatchesReport(t *testing.T) {
	t.Parallel()

	op, ok := FacadeOperationByName(themeIntelReportOpName)
	if !ok {
		t.Fatalf("FacadeOperationByName(%q) is not registered", themeIntelReportOpName)
	}
	if op.FacadeTool != FacadeToolAnalyze {
		t.Fatalf("theme_intelligence_report facade_tool=%q want %s", op.FacadeTool, FacadeToolAnalyze)
	}
	if op.RoutedTool != themeIntelReportOpName {
		t.Fatalf("theme_intelligence_report routed_tool=%q want %s", op.RoutedTool, themeIntelReportOpName)
	}
	if op.InputSchema == nil {
		t.Fatalf("theme_intelligence_report input_schema must not be nil")
	}
	for _, want := range []string{
		"business-workbench",
		"analyst-facade",
		"analyst-business-core",
		"analyst",
	} {
		if !containsString(op.AllowedPresets, want) {
			t.Fatalf("theme_intelligence_report allowed_presets missing %q: %v", want, op.AllowedPresets)
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
	props, _ := analyze.InputSchema["properties"].(map[string]any)
	opSchema, _ := props["operation"].(map[string]any)
	enum, _ := opSchema["enum"].([]string)
	enumFound := false
	for _, name := range enum {
		if name == themeIntelReportOpName {
			enumFound = true
			break
		}
	}
	if !enumFound {
		t.Fatalf("gong_analyze enum missing %q: %v", themeIntelReportOpName, enum)
	}

	store := newSeededThemeIntelReportStore(t)
	server := NewServerWithOptions(store, "gongmcp", "test",
		WithToolAllowlist(FacadeToolNames()),
		WithFacadeRoutedToolAllowlist([]string{themeIntelReportOpName}),
	)

	args, err := json.Marshal(map[string]any{
		"operation": themeIntelReportOpName,
		"arguments": map[string]any{
			"from_date":             "2026-01-01",
			"to_date":               "2026-06-30",
			"theme_query":           "manual order entry",
			"group_by":              []string{"quarter", "industry", "persona", "won_lost"},
			"top_quotes_per_theme":  50,
			"output_intent":         "full_report",
			"include_call_titles":   true,
			"include_account_names": true,
			"include_speaker_refs":  true,
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
	if wrapper["operation"] != themeIntelReportOpName {
		t.Fatalf("operation=%v want %s", wrapper["operation"], themeIntelReportOpName)
	}
	if wrapper["routed_tool"] != themeIntelReportOpName {
		t.Fatalf("routed_tool=%v want %s", wrapper["routed_tool"], themeIntelReportOpName)
	}
	inner, ok := wrapper["result"].(map[string]any)
	if !ok {
		t.Fatalf("inner result type=%T", wrapper["result"])
	}

	for _, field := range []string{
		"operation",
		"status",
		"searched_scope",
		"coverage_summary",
		"theme_candidates",
		"themes_by_quarter",
		"themes_by_industry",
		"themes_by_persona",
		"top_quotes_by_theme",
		"call_drilldowns",
		"sales_hooks_inputs",
		"outreach_sequence_inputs",
		"pipeline_outcome_summary",
		"loss_reason_summary",
		"evidence_policy",
		"evidence_type",
		"answer_contract",
		"dimension_readiness",
		"data_readiness_caveats",
		"limitations",
		"warnings",
		"suggested_followups",
		"report_truncated",
	} {
		if _, ok := inner[field]; !ok {
			t.Fatalf("report missing required field %q in keys=%v", field, sortedKeys(inner))
		}
	}

	if got, _ := inner["operation"].(string); got != themeIntelReportOpName {
		t.Fatalf("inner.operation=%q want %q", got, themeIntelReportOpName)
	}

	quotesByTheme, _ := inner["top_quotes_by_theme"].(map[string]any)
	if len(quotesByTheme) == 0 {
		t.Fatalf("top_quotes_by_theme empty: %v", inner)
	}
	for theme, list := range quotesByTheme {
		rows, _ := list.([]any)
		if len(rows) > 5 {
			t.Fatalf("theme %q has %d quotes; cap is 5", theme, len(rows))
		}
		if len(rows) == 0 {
			continue
		}
		for _, r := range rows {
			row, _ := r.(map[string]any)
			for _, want := range []string{"snippet", "person_title_status", "attribution_source", "attribution_confidence"} {
				if _, ok := row[want]; !ok {
					t.Fatalf("quote row missing %q in theme %q row %v", want, theme, row)
				}
			}
		}
	}

	drills, _ := inner["call_drilldowns"].([]any)
	if len(drills) == 0 {
		t.Fatalf("call_drilldowns empty: %v", inner)
	}
	for _, d := range drills {
		drill, _ := d.(map[string]any)
		ai, hasAI := drill["ai_condensed_evidence"].([]any)
		if !hasAI {
			t.Fatalf("drilldown missing ai_condensed_evidence (must label AI evidence separately): %v", drill)
		}
		// AI rows must use the highlight_type discriminator and must NOT carry
		// transcript-specific keys like start_ms / segment_index, so callers
		// cannot conflate them with verbatim quotes.
		for _, r := range ai {
			row, _ := r.(map[string]any)
			if _, ok := row["highlight_type"]; !ok {
				t.Fatalf("ai_condensed_evidence row missing highlight_type: %v", row)
			}
			if _, ok := row["snippet"]; ok {
				t.Fatalf("ai_condensed_evidence row exposed transcript-style snippet field; AI evidence must stay typed: %v", row)
			}
		}
		verb, hasVerb := drill["verbatim_transcript_excerpts"].([]any)
		if !hasVerb {
			t.Fatalf("drilldown missing verbatim_transcript_excerpts: %v", drill)
		}
		for _, r := range verb {
			row, _ := r.(map[string]any)
			for _, want := range []string{"snippet", "person_title_status", "attribution_source", "attribution_confidence"} {
				if _, ok := row[want]; !ok {
					t.Fatalf("verbatim row missing attribution field %q: %v", want, row)
				}
			}
		}
	}

	lossSummary, _ := inner["loss_reason_summary"].(map[string]any)
	if lossSummary == nil {
		t.Fatalf("loss_reason_summary must be an object: %v", inner)
	}
	rows, _ := lossSummary["rows"].([]any)
	foundPriceBucket := false
	for _, r := range rows {
		row, _ := r.(map[string]any)
		if v, _ := row["bucket"].(string); v == "price" {
			foundPriceBucket = true
			break
		}
	}
	if !foundPriceBucket {
		t.Fatalf("loss_reason_summary missing 'price' bucket: %v", lossSummary)
	}

	rendered := result.Content[0].Text
	if strings.Contains(rendered, "FULL_TRANSCRIPT_TEXT_LEAK_PROBE_NEVER_RETURN_ENTIRE_BODY") {
		t.Fatalf("report leaked the full-transcript probe text; report must use bounded excerpts only")
	}

	if _, ok := inner["report_truncated"].(bool); !ok {
		t.Fatalf("report_truncated must be a bool: %T", inner["report_truncated"])
	}

	quarterRows, _ := inner["themes_by_quarter"].(map[string]any)
	quarterList, _ := quarterRows["rows"].([]any)
	if len(quarterList) < 2 {
		t.Fatalf("themes_by_quarter expected >=2 quarter buckets, got %v", quarterRows)
	}

	industryRows, _ := inner["themes_by_industry"].(map[string]any)
	industryList, _ := industryRows["rows"].([]any)
	if len(industryList) < 2 {
		t.Fatalf("themes_by_industry expected >=2 industry buckets, got %v", industryRows)
	}

	personaRows, _ := inner["themes_by_persona"].(map[string]any)
	personaList, _ := personaRows["rows"].([]any)
	if len(personaList) < 2 {
		t.Fatalf("themes_by_persona expected >=2 persona buckets, got %v", personaRows)
	}

	// Item E: <blank> rows must not appear in the public dimension `rows`
	// list; they are surfaced only through the `coverage` sub-object so
	// callers do not treat blank as a normal industry/persona/quarter.
	for name, dim := range map[string]map[string]any{
		"themes_by_quarter":  quarterRows,
		"themes_by_industry": industryRows,
		"themes_by_persona":  personaRows,
	} {
		if _, ok := dim["coverage"].(map[string]any); !ok {
			t.Fatalf("%s missing coverage sub-object: %v", name, dim)
		}
		rowsList, _ := dim["rows"].([]any)
		for _, r := range rowsList {
			row, _ := r.(map[string]any)
			if v, _ := row["bucket"].(string); v == "<blank>" || v == "" {
				t.Fatalf("%s rows must exclude <blank> bucket; got %v", name, row)
			}
		}
	}

	// Item B: explicit report-to-drilldown workflow. Each top quote row
	// must carry a `drilldown_term` safe to feed into call_drilldown's
	// theme_query, and the report must expose a deterministic
	// `drilldown_workflow_inputs` array that pairs call_refs with the
	// matched theme term.
	for theme, list := range quotesByTheme {
		rows, _ := list.([]any)
		for _, r := range rows {
			row, _ := r.(map[string]any)
			term, _ := row["drilldown_term"].(string)
			if strings.TrimSpace(term) == "" {
				t.Fatalf("quote row missing drilldown_term in theme %q row %v", theme, row)
			}
		}
	}
	workflow, _ := inner["drilldown_workflow_inputs"].([]any)
	if len(workflow) == 0 {
		t.Fatalf("drilldown_workflow_inputs must be non-empty when quotes exist: %v", inner)
	}
	for _, w := range workflow {
		entry, _ := w.(map[string]any)
		for _, key := range []string{"call_ref", "theme_query", "step", "evidence_source"} {
			if _, ok := entry[key]; !ok {
				t.Fatalf("drilldown_workflow_inputs entry missing %q: %v", key, entry)
			}
		}
		if step, _ := entry["step"].(string); !strings.Contains(step, "call_drilldown") {
			t.Fatalf("drilldown_workflow_inputs.step should reference call_drilldown: %v", entry)
		}
	}

	pipeline, _ := inner["pipeline_outcome_summary"].(map[string]any)
	if pipeline == nil {
		t.Fatalf("pipeline_outcome_summary must be present")
	}
	wonLost, _ := pipeline["won_lost"].([]any)
	hasWon, hasLost := false, false
	for _, r := range wonLost {
		row, _ := r.(map[string]any)
		switch v, _ := row["bucket"].(string); v {
		case "closed_won":
			hasWon = true
		case "closed_lost":
			hasLost = true
		}
	}
	if !hasWon || !hasLost {
		t.Fatalf("pipeline_outcome_summary missing closed_won/closed_lost coverage: %v", pipeline)
	}
}

// TestFacadeAnalyzeThemeIntelReportBlankDimensionMovesIntoCoverage proves
// that a cohort with no industry / persona data emits empty bucket rows
// plus a coverage sub-object that names the blank-call count and
// percentage so Marketing/RevOps prompts cannot misread the <blank>
// segment as a real industry or persona.
func TestFacadeAnalyzeThemeIntelReportBlankDimensionMovesIntoCoverage(t *testing.T) {
	t.Parallel()

	base := openSeededStore(t)
	defer base.Close()

	// Seed a single call with no Industry, no participant titles, no
	// opportunity stage so industry/persona/quarter all collapse to <blank>.
	if _, err := base.UpsertCall(context.Background(), mustJSON(t, map[string]any{
		"id":        "call_theme_blank_dim_001",
		"title":     "Theme intel blank dimension cohort",
		"started":   "2026-02-15T15:00:00Z",
		"duration":  900,
		"system":    "Zoom",
		"direction": "Conference",
		"scope":     "External",
	})); err != nil {
		t.Fatalf("upsert blank-dim call: %v", err)
	}
	if _, err := base.UpsertTranscript(context.Background(), mustJSON(t, map[string]any{
		"callTranscripts": []any{
			map[string]any{
				"callId": "call_theme_blank_dim_001",
				"transcript": []any{
					map[string]any{
						"speakerId": "speaker_blank_dim_buyer",
						"sentences": []any{
							map[string]any{"start": 1000, "end": 2500, "text": "Manual order entry pain across the board."},
						},
					},
				},
			},
		},
	})); err != nil {
		t.Fatalf("upsert blank-dim transcript: %v", err)
	}

	server := NewServerWithOptions(base, "gongmcp", "test",
		WithToolAllowlist(FacadeToolNames()),
		WithFacadeRoutedToolAllowlist([]string{themeIntelReportOpName}),
	)
	args, err := json.Marshal(map[string]any{
		"operation": themeIntelReportOpName,
		"arguments": map[string]any{
			"from_date":   "2026-02-01",
			"to_date":     "2026-02-28",
			"theme_query": "manual order entry",
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
	inner, _ := wrapper["result"].(map[string]any)

	for _, name := range []string{"themes_by_industry", "themes_by_persona"} {
		dim, _ := inner[name].(map[string]any)
		if dim == nil {
			t.Fatalf("%s missing", name)
		}
		coverage, ok := dim["coverage"].(map[string]any)
		if !ok {
			t.Fatalf("%s missing coverage sub-object: %v", name, dim)
		}
		blankCount, _ := coverage["blank_call_count"].(float64)
		if blankCount < 1 {
			t.Fatalf("%s coverage.blank_call_count=%v want >=1", name, blankCount)
		}
		blankRate, _ := coverage["blank_call_rate"].(float64)
		if blankRate <= 0 || blankRate > 1 {
			t.Fatalf("%s coverage.blank_call_rate=%v want (0,1]", name, blankRate)
		}
		rowsList, _ := dim["rows"].([]any)
		for _, r := range rowsList {
			row, _ := r.(map[string]any)
			if v, _ := row["bucket"].(string); v == "<blank>" || v == "" {
				t.Fatalf("%s rows must exclude <blank>: %v", name, row)
			}
		}
	}
}

// TestFacadeAnalyzeThemeIntelReportSurfacesSpeakerRoleAttribution proves
// that the gap-followup `speaker_role` / `speaker_role_status` /
// `is_internal_speaker` fields surface on every drilldown transcript
// row in the report. Internal speakers come back as "internal" with
// status "available"; speakers with no party data come back as
// "unknown" with an explicit status so callers cannot misread the
// silence as a confident answer.
func TestFacadeAnalyzeThemeIntelReportSurfacesSpeakerRoleAttribution(t *testing.T) {
	t.Parallel()

	store := newSeededThemeIntelReportStore(t)
	// Override one drilldown row to carry an explicit role + status so we
	// exercise the available branch; one row stays with empty role to
	// exercise the affiliation_missing fallback.
	store.transcriptByCallID["call_theme_q1_won_001"] = []sqlite.CallDrilldownEvidenceRow{
		{
			CallID: "call_theme_q1_won_001", SegmentIndex: 0,
			SpeakerID: "speaker_q1_won_buyer", StartMS: 1000, EndMS: 2500,
			Snippet:               "Manual order entry pain point",
			ContextExcerpt:        "Manual order entry is the biggest pain point for our procurement team.",
			PersonTitleStatus:     sqlite.AttributionStatusAvailable,
			PersonTitleSource:     sqlite.AttributionSourceCallParties,
			AttributionSource:     sqlite.AttributionSourceGongParty,
			AttributionConfidence: sqlite.AttributionConfidenceExactSpeakerID,
			PersonTitle:           "VP Procurement",
			SpeakerRole:           sqlite.SpeakerRoleExternal,
			SpeakerRoleStatus:     sqlite.SpeakerRoleStatusAvailable,
		},
	}
	store.transcriptByCallID["call_theme_q1_lost_002"] = []sqlite.CallDrilldownEvidenceRow{
		{
			CallID: "call_theme_q1_lost_002", SegmentIndex: 0,
			SpeakerID: "speaker_q1_lost_buyer", StartMS: 1000, EndMS: 2500,
			Snippet:               "Manual order entry pain but pricing too high",
			PersonTitleStatus:     sqlite.AttributionStatusAvailable,
			PersonTitleSource:     sqlite.AttributionSourceCallParties,
			AttributionSource:     sqlite.AttributionSourceGongParty,
			AttributionConfidence: sqlite.AttributionConfidenceExactSpeakerID,
			PersonTitle:           "Procurement Director",
			// SpeakerRole / SpeakerRoleStatus deliberately empty: the
			// underlying party data did not give us an affiliation; the
			// facade must fall back to unknown + affiliation_missing.
		},
	}

	server := NewServerWithOptions(store, "gongmcp", "test",
		WithToolAllowlist(FacadeToolNames()),
		WithFacadeRoutedToolAllowlist([]string{themeIntelReportOpName}),
	)
	// Use a Q1-only date range so the cohort only contains the two
	// synthetic Manufacturing calls. The default seeded fixture calls
	// (call_extended_001, call_sanitized_001) are dated 2026-04-24 and
	// would otherwise crowd the drill cap.
	args, err := json.Marshal(map[string]any{
		"operation": themeIntelReportOpName,
		"arguments": map[string]any{
			"from_date":   "2026-01-01",
			"to_date":     "2026-03-31",
			"theme_query": "manual order entry",
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
	inner, _ := wrapper["result"].(map[string]any)
	drills, _ := inner["call_drilldowns"].([]any)
	if len(drills) == 0 {
		t.Fatalf("expected drilldowns: %v", inner)
	}

	roleByCallRef := map[string]map[string]any{}
	for _, d := range drills {
		drill, _ := d.(map[string]any)
		ref, _ := drill["call_ref"].(string)
		excerpts, _ := drill["verbatim_transcript_excerpts"].([]any)
		if len(excerpts) == 0 {
			continue
		}
		row, _ := excerpts[0].(map[string]any)
		roleByCallRef[ref] = row
	}
	wonRef := sqlite.StableCallRef("call_theme_q1_won_001")
	lostRef := sqlite.StableCallRef("call_theme_q1_lost_002")

	wonRow := roleByCallRef[wonRef]
	if wonRow == nil {
		t.Fatalf("missing won-call drilldown row for ref %s; got %+v", wonRef, roleByCallRef)
	}
	if got, _ := wonRow["speaker_role"].(string); got != sqlite.SpeakerRoleExternal {
		t.Fatalf("won-call speaker_role=%q want %q", got, sqlite.SpeakerRoleExternal)
	}
	if got, _ := wonRow["speaker_role_status"].(string); got != sqlite.SpeakerRoleStatusAvailable {
		t.Fatalf("won-call speaker_role_status=%q want %q", got, sqlite.SpeakerRoleStatusAvailable)
	}
	if got, _ := wonRow["is_internal_speaker"].(bool); got {
		t.Fatalf("won-call is_internal_speaker=true; external speaker should not be flagged internal: %+v", wonRow)
	}

	lostRow := roleByCallRef[lostRef]
	if lostRow == nil {
		t.Fatalf("missing lost-call drilldown row for ref %s; got %+v", lostRef, roleByCallRef)
	}
	if got, _ := lostRow["speaker_role"].(string); got != sqlite.SpeakerRoleUnknown {
		t.Fatalf("lost-call speaker_role=%q want %q (no affiliation data)", got, sqlite.SpeakerRoleUnknown)
	}
	if got, _ := lostRow["speaker_role_status"].(string); got != sqlite.SpeakerRoleStatusAffiliationMissing {
		t.Fatalf("lost-call speaker_role_status=%q want %q", got, sqlite.SpeakerRoleStatusAffiliationMissing)
	}
}

func TestFacadeAnalyzeThemeIntelReportEmptyLossReasonEmitsNotPopulated(t *testing.T) {
	t.Parallel()

	base := openSeededStore(t)
	defer base.Close()
	// Seed a single call without any loss reason field so the loss_reason
	// dimension produces no normalized buckets.
	openCall := mustJSON(t, map[string]any{
		"id":        "call_theme_no_loss_reason",
		"title":     "Theme intel no-loss-reason call",
		"started":   "2026-02-15T15:00:00Z",
		"duration":  900,
		"system":    "Zoom",
		"direction": "Conference",
		"scope":     "External",
		"parties": []any{
			map[string]any{"speakerId": "speaker_no_loss_buyer", "title": "VP Procurement"},
		},
		"context": []any{
			map[string]any{
				"objectType": "Account",
				"id":         "acct_theme_no_loss",
				"fields": []any{
					map[string]any{"name": "Industry", "value": "Manufacturing"},
				},
			},
			map[string]any{
				"objectType": "Opportunity",
				"id":         "opp_theme_no_loss",
				"fields": []any{
					map[string]any{"name": "StageName", "value": "Discovery"},
				},
			},
		},
	})
	if _, err := base.UpsertCall(context.Background(), openCall); err != nil {
		t.Fatalf("upsert no-loss-reason call: %v", err)
	}
	if _, err := base.UpsertTranscript(context.Background(), mustJSON(t, map[string]any{
		"callTranscripts": []any{
			map[string]any{
				"callId": "call_theme_no_loss_reason",
				"transcript": []any{
					map[string]any{
						"speakerId": "speaker_no_loss_buyer",
						"sentences": []any{
							map[string]any{"start": 1000, "end": 2500, "text": "Manual order entry is painful and we still need to evaluate."},
						},
					},
				},
			},
		},
	})); err != nil {
		t.Fatalf("upsert no-loss-reason transcript: %v", err)
	}

	server := NewServerWithOptions(base, "gongmcp", "test",
		WithToolAllowlist(FacadeToolNames()),
		WithFacadeRoutedToolAllowlist([]string{themeIntelReportOpName}),
	)

	args, err := json.Marshal(map[string]any{
		"operation": themeIntelReportOpName,
		"arguments": map[string]any{
			"from_date":   "2026-02-01",
			"to_date":     "2026-02-28",
			"theme_query": "manual order entry",
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
	inner, _ := wrapper["result"].(map[string]any)
	loss, _ := inner["loss_reason_summary"].(map[string]any)
	if got, _ := loss["status"].(string); got != "loss_reason_not_populated" {
		t.Fatalf("loss_reason_summary.status=%q want loss_reason_not_populated; loss=%v", got, loss)
	}
	limitations, _ := inner["limitations"].([]any)
	hasLimitation := false
	for _, l := range limitations {
		if strings.EqualFold(stringValue(l), "loss_reason_not_populated") {
			hasLimitation = true
			break
		}
	}
	if !hasLimitation {
		t.Fatalf("limitations missing loss_reason_not_populated marker: %v", limitations)
	}
}

func TestFacadeAnalyzeThemeIntelReportPolicySwitchesOverrideIncludeFlags(t *testing.T) {
	t.Parallel()

	store := newSeededThemeIntelReportStore(t)
	server := NewServerWithOptions(store, "gongmcp", "test",
		WithToolAllowlist(FacadeToolNames()),
		WithFacadeRoutedToolAllowlist([]string{themeIntelReportOpName}),
		WithPolicySwitches(PolicySwitches{
			HideRawCallIDs:   true,
			HideCallTitles:   true,
			HideAccountNames: true,
		}),
	)

	args, err := json.Marshal(map[string]any{
		"operation": themeIntelReportOpName,
		"arguments": map[string]any{
			"from_date":             "2026-01-01",
			"to_date":               "2026-06-30",
			"theme_query":           "manual order entry",
			"include_call_titles":   true,
			"include_account_names": true,
			"include_raw_ids":       true,
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
	rendered := result.Content[0].Text
	for _, leaked := range []string{
		"call_theme_q1_won_001",
		"call_theme_q1_lost_002",
		"call_theme_q2_neg_003",
		"call_theme_q2_disc_004",
		"Theme intel Q1 Manufacturing closed-won discovery",
		"Theme Intel Manufacturing Account",
	} {
		if strings.Contains(rendered, leaked) {
			t.Fatalf("policy switch did not suppress %q in rendered report:\n%s", leaked, rendered)
		}
	}
}

func TestFacadeAnalyzeThemeIntelReportAccountQueryFailsClosedWhenNamesHidden(t *testing.T) {
	t.Parallel()

	store := newSeededThemeIntelReportStore(t)
	server := NewServerWithOptions(store, "gongmcp", "test",
		WithToolAllowlist(FacadeToolNames()),
		WithFacadeRoutedToolAllowlist([]string{themeIntelReportOpName}),
		WithPolicySwitches(PolicySwitches{HideAccountNames: true}),
	)

	args, err := json.Marshal(map[string]any{
		"operation": themeIntelReportOpName,
		"arguments": map[string]any{
			"filter": map[string]any{
				"from_date":     "2026-01-01",
				"to_date":       "2026-06-30",
				"account_query": "Theme Intel",
			},
			"theme_query":           "manual order entry",
			"include_account_names": true,
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	_, err = server.executeFacadeDispatch(t.Context(), FacadeToolAnalyze, args)
	if err == nil || !strings.Contains(err.Error(), "hide_account_names=false") {
		t.Fatalf("expected account_query to fail closed when hide_account_names is enabled, got %v", err)
	}
}

func TestFacadeAnalyzeThemeIntelReportSeedlessUsesAIBusinessBriefs(t *testing.T) {
	t.Parallel()

	store := newSeededThemeIntelReportStore(t)
	server := NewServerWithOptions(store, "gongmcp", "test",
		WithToolAllowlist(FacadeToolNames()),
		WithFacadeRoutedToolAllowlist([]string{themeIntelReportOpName}),
	)

	args, err := json.Marshal(map[string]any{
		"operation": themeIntelReportOpName,
		"arguments": map[string]any{
			"from_date": "2026-01-01",
			"to_date":   "2026-06-30",
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
	if got, _ := inner["status"].(string); got != "ai_brief_candidate_themes" {
		t.Fatalf("status=%q want ai_brief_candidate_themes; inner=%v", got, inner)
	}
	scope, _ := inner["searched_scope"].(map[string]any)
	if got := mustJSONText(t, scope["exclude_lifecycle_buckets"]); !strings.Contains(got, "outbound_prospecting") {
		t.Fatalf("seedless searched_scope missing default outbound exclusion: %s", got)
	}
	if got, _ := scope["exclude_likely_voicemail"].(bool); !got {
		t.Fatalf("seedless searched_scope exclude_likely_voicemail=false")
	}
	coverage, _ := inner["coverage_summary"].(map[string]any)
	if got, _ := coverage["call_count"].(float64); got != 6 {
		t.Fatalf("coverage_summary.call_count=%v want 6 after default seedless noise exclusions; coverage=%v", got, coverage)
	}
	candidates := mustJSONText(t, inner["theme_candidates"])
	if !strings.Contains(candidates, "manual order entry") {
		t.Fatalf("AI brief candidates missing manual order entry: %s", candidates)
	}
	if !strings.Contains(candidates, "bigcommerce migration") {
		t.Fatalf("AI brief bootstrap should include dynamic non-seed phrase bigcommerce migration: %s", candidates)
	}
	aiEvidence, _ := inner["ai_business_brief_evidence_by_theme"].(map[string]any)
	if len(aiEvidence) == 0 {
		t.Fatalf("ai_business_brief_evidence_by_theme empty: %v", inner)
	}
	rendered := result.Content[0].Text
	if strings.Contains(strings.ToLower(rendered), "please leave a message") || strings.Contains(strings.ToLower(rendered), "after the tone") {
		t.Fatalf("seedless AI brief report included voicemail boilerplate:\n%s", rendered)
	}
}

func TestFacadeAnalyzeThemesDiscoverSeedlessUsesAIBusinessBriefs(t *testing.T) {
	t.Parallel()

	store := newSeededThemeIntelReportStore(t)
	server := NewServerWithOptions(store, "gongmcp", "test",
		WithToolAllowlist(FacadeToolNames()),
		WithFacadeRoutedToolAllowlist([]string{"discover_themes_in_cohort"}),
	)

	args, err := json.Marshal(map[string]any{
		"operation": OpAnalyzeThemesDiscover,
		"arguments": map[string]any{
			"filter": map[string]any{
				"from_date": "2026-01-01",
				"to_date":   "2026-06-30",
			},
			"limit": 10,
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
	if got, _ := inner["status"].(string); got != "ai_brief_candidate_terms" {
		t.Fatalf("status=%q want ai_brief_candidate_terms; inner=%v", got, inner)
	}
	if rows, _ := inner["results"].([]any); len(rows) != 0 {
		t.Fatalf("AI-brief seedless discovery should not include transcript sample rows when brief candidates exist: %v", rows)
	}
	candidates := mustJSONText(t, inner["themes"])
	if !strings.Contains(candidates, "manual order entry") {
		t.Fatalf("AI brief discover candidates missing manual order entry: %s", candidates)
	}
	if !strings.Contains(candidates, "bigcommerce migration") {
		t.Fatalf("AI brief discover should include dynamic non-seed phrase bigcommerce migration: %s", candidates)
	}
	rendered := strings.ToLower(result.Content[0].Text)
	if strings.Contains(rendered, "please leave a detailed message") || strings.Contains(rendered, "after the tone") {
		t.Fatalf("seedless AI brief discovery included voicemail boilerplate:\n%s", result.Content[0].Text)
	}
}

func TestFacadeAnalyzeThemeIntelReportSeedlessLimitDoesNotStarveAIBriefScan(t *testing.T) {
	t.Parallel()

	store := newSeededThemeIntelReportStore(t)
	for day := 5; day <= 31; day++ {
		callID := fmt.Sprintf("call_theme_q1_no_high_%02d", day)
		rawCall := mustJSON(t, map[string]any{
			"id":        callID,
			"title":     "Theme intel Q1 no-highlight business discovery",
			"started":   fmt.Sprintf("2026-03-%02dT15:00:00Z", day),
			"duration":  1800,
			"system":    "Zoom",
			"direction": "Conference",
			"scope":     "External",
			"parties": []any{
				map[string]any{"speakerId": "speaker_no_high_buyer", "title": "Procurement"},
				map[string]any{"speakerId": "speaker_no_high_rep", "title": "Account Executive"},
			},
		})
		if _, err := store.Store.UpsertCall(context.Background(), rawCall); err != nil {
			t.Fatalf("upsert no-highlight call: %v", err)
		}
		rawTranscript := mustJSON(t, map[string]any{
			"callTranscripts": []any{
				map[string]any{
					"callId": callID,
					"transcript": []any{
						map[string]any{
							"speakerId": "speaker_no_high_buyer",
							"sentences": []any{
								map[string]any{
									"start": 1000,
									"end":   2000,
									"text":  "We are here for a discovery conversation.",
								},
							},
						},
					},
				},
			},
		})
		if _, err := store.Store.UpsertTranscript(context.Background(), rawTranscript); err != nil {
			t.Fatalf("upsert no-highlight transcript: %v", err)
		}
	}
	policy, err := DefaultLimitPolicy().WithOverride("business_analysis_rows", 1)
	if err != nil {
		t.Fatalf("limit policy: %v", err)
	}
	server := NewServerWithOptions(store, "gongmcp", "test",
		WithToolAllowlist(FacadeToolNames()),
		WithFacadeRoutedToolAllowlist([]string{themeIntelReportOpName}),
		WithLimitPolicy(policy),
	)

	result, err := server.executeFacadeDispatch(t.Context(), FacadeToolAnalyze, mustFacadeArgs(t, themeIntelReportOpName, map[string]any{
		"from_date": "2026-01-01",
		"to_date":   "2026-06-30",
		"limit":     1,
	}))
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected isError: %+v", result)
	}
	wrapper := decodeFacadeWrapper(t, result)
	inner, _ := wrapper["result"].(map[string]any)
	if got, _ := inner["status"].(string); got != "ai_brief_candidate_themes" {
		t.Fatalf("status=%q want ai_brief_candidate_themes; inner=%v", got, inner)
	}
	source, _ := inner["ai_business_brief_source"].(map[string]any)
	if got, _ := source["source_calls"].(float64); got <= 25 {
		t.Fatalf("seedless bootstrap should scan beyond candidate limit; source_calls=%v source=%v inner=%v", got, source, inner)
	}
	if got, _ := source["source_rows"].(float64); got == 0 {
		t.Fatalf("seedless bootstrap should find AI rows beyond early no-highlight calls; source=%v inner=%v", source, inner)
	}
}

func TestBusinessBriefCandidatePhrasesExtractsNonSeedBusinessThemes(t *testing.T) {
	t.Parallel()

	phrases := businessBriefCandidatePhrases("BigCommerce migration and Oracle Fusion storefront readiness are driving preferred vendor urgency.")
	got := strings.ToLower(mustJSONText(t, phrases))
	for _, want := range []string{"bigcommerce migration", "oracle fusion", "preferred vendor"} {
		if !strings.Contains(got, want) {
			t.Fatalf("candidate phrases missing %q: %s", want, got)
		}
	}
}

func TestAIBusinessBriefSummaryResolvesStableCallRefs(t *testing.T) {
	t.Parallel()

	store := newSeededThemeIntelReportStore(t)
	server := NewServerWithOptions(store, "gongmcp", "test")

	summary, err := server.aiBusinessBriefThemeSummary(t.Context(), []sqlite.BusinessAnalysisCallRow{
		{CallID: sqlite.StableCallRef("call_theme_q1_won_001")},
	}, false, 5)
	if err != nil {
		t.Fatalf("aiBusinessBriefThemeSummary: %v", err)
	}
	if summary.SourceRowCount == 0 {
		t.Fatalf("expected AI brief rows for stable call_ref input; summary=%+v", summary)
	}
	if got := mustJSONText(t, summary.Candidates); !strings.Contains(strings.ToLower(got), "manual order entry") {
		t.Fatalf("expected resolved call_ref to produce manual order entry candidate: %s", got)
	}
}

func TestFacadeAnalyzeThemeIntelReportSeededIncludesAIBriefEvidence(t *testing.T) {
	t.Parallel()

	store := newSeededThemeIntelReportStore(t)
	store.highlights["call_theme_q2_disc_004"] = append(store.highlights["call_theme_q2_disc_004"], sqlite.AIHighlightRow{
		CallID:         "call_theme_q2_disc_004",
		HighlightIndex: 9,
		HighlightType:  "key_point",
		HighlightText:  "ERP integrations with SAP and Oracle are part of the buyer roadmap.",
		SourcePath:     "content.keyPoints",
		UpdatedAt:      "2026-04-25T00:00:00Z",
	})
	server := NewServerWithOptions(store, "gongmcp", "test",
		WithToolAllowlist(FacadeToolNames()),
		WithFacadeRoutedToolAllowlist([]string{themeIntelReportOpName}),
	)

	args, err := json.Marshal(map[string]any{
		"operation": themeIntelReportOpName,
		"arguments": map[string]any{
			"from_date":   "2026-04-01",
			"to_date":     "2026-06-30",
			"theme_query": "ERP integration",
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
	inner, _ := wrapper["result"].(map[string]any)
	aiEvidence, _ := inner["ai_business_brief_evidence_by_theme"].(map[string]any)
	rows, _ := aiEvidence["ERP integration"].([]any)
	if len(rows) == 0 {
		t.Fatalf("seeded ERP integration report missing AI brief evidence: %v", inner)
	}
}

func TestFacadeAnalyzeThemeIntelReportDrilldownsPreferQuoteBackedCalls(t *testing.T) {
	t.Parallel()

	store := newSeededThemeIntelReportStore(t)
	server := NewServerWithOptions(store, "gongmcp", "test",
		WithToolAllowlist(FacadeToolNames()),
		WithFacadeRoutedToolAllowlist([]string{themeIntelReportOpName}),
	)

	args, err := json.Marshal(map[string]any{
		"operation": themeIntelReportOpName,
		"arguments": map[string]any{
			"from_date":   "2026-05-21",
			"to_date":     "2026-05-23",
			"theme_query": "manual order entry",
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
	inner, _ := wrapper["result"].(map[string]any)
	drills, _ := inner["call_drilldowns"].([]any)
	if len(drills) != 1 {
		t.Fatalf("quote-backed drilldowns len=%d want 1 real quote call; inner=%v", len(drills), inner)
	}
	drill, _ := drills[0].(map[string]any)
	if got, _ := drill["call_ref"].(string); got != sqlite.StableCallRef("call_theme_q2_disc_004") {
		t.Fatalf("drilldown call_ref=%q want quote-backed q2 discovery call, drills=%v", got, drills)
	}
	if got, _ := drill["call_ref"].(string); got == sqlite.StableCallRef("call_theme_q2_vm_005") {
		t.Fatalf("drilldown selected voicemail cohort filler call: %v", drills)
	}
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Tests do not need full sorting for diagnostic output, but a stable order
	// makes failures easier to compare across runs.
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	return keys
}

func stringValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
