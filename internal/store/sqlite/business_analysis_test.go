package sqlite

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizeLossReasonBucket(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  string
		want string
	}{
		{name: "blank", raw: "", want: ""},
		{name: "whitespace", raw: "   ", want: ""},
		{name: "price_too_high", raw: "Price too high", want: "price"},
		{name: "pricing_concerns", raw: "Pricing concerns", want: "price"},
		{name: "no_decision_phrase", raw: "No Decision", want: "no_decision"},
		{name: "no_decision_underscored", raw: "no_decision", want: "no_decision"},
		{name: "competitor", raw: "Lost to Competitor", want: "competitor"},
		{name: "competition", raw: "Competitive displacement", want: "competitor"},
		{name: "timing_phrase", raw: "Timeline uncertainty", want: "timing"},
		{name: "timing_postponed", raw: "Project Postponed - Timing", want: "timing"},
		{name: "feature_gap_phrase", raw: "Feature Gap", want: "feature_gap"},
		{name: "missing_feature", raw: "Missing feature for reporting", want: "feature_gap"},
		{name: "budget_phrase", raw: "Budget cuts", want: "budget"},
		{name: "no_funding", raw: "No funding available", want: "budget"},
		{name: "relationship_phrase", raw: "Lost champion / sponsor left", want: "relationship"},
		{name: "stakeholder_change", raw: "Stakeholder change", want: "relationship"},
		{name: "unknown_other", raw: "Account decided to build internally on hadoop", want: "unknown_other"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := NormalizeLossReasonBucket(tc.raw)
			if got != tc.want {
				t.Fatalf("NormalizeLossReasonBucket(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestSummarizeBusinessAnalysisDimensionLossReasonBuckets(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	type lossCase struct {
		callID string
		title  string
		raw    string
		bucket string
	}
	cases := []lossCase{
		{callID: "call-loss-price", title: "loss reason buckets price", raw: "Price too high", bucket: "price"},
		{callID: "call-loss-no-decision", title: "loss reason buckets no decision", raw: "No Decision", bucket: "no_decision"},
		{callID: "call-loss-competitor", title: "loss reason buckets competitor", raw: "Lost to Competitor", bucket: "competitor"},
		{callID: "call-loss-timing", title: "loss reason buckets timing", raw: "Timeline uncertainty", bucket: "timing"},
		{callID: "call-loss-feature-gap", title: "loss reason buckets feature gap", raw: "Feature Gap", bucket: "feature_gap"},
		{callID: "call-loss-budget", title: "loss reason buckets budget", raw: "Budget cuts", bucket: "budget"},
		{callID: "call-loss-relationship", title: "loss reason buckets relationship", raw: "Lost champion / sponsor left", bucket: "relationship"},
		{callID: "call-loss-other", title: "loss reason buckets other", raw: "Decided to build internally on hadoop", bucket: "unknown_other"},
		{callID: "call-loss-blank", title: "loss reason buckets blank", raw: "", bucket: ""},
	}
	for _, c := range cases {
		raw := map[string]any{
			"id":       c.callID,
			"title":    c.title,
			"started":  "2026-02-14T15:00:00Z",
			"duration": 1200,
			"metaData": map[string]any{
				"system":    "Zoom",
				"direction": "Conference",
				"scope":     "External",
			},
		}
		if c.raw != "" {
			raw["context"] = []any{
				map[string]any{
					"objectType": "Opportunity",
					"id":         "opp-" + c.callID,
					"fields": []any{
						map[string]any{"name": "StageName", "value": "Closed Lost"},
						map[string]any{"name": "LossReason", "value": c.raw},
					},
				},
			}
		}
		body, err := json.Marshal(raw)
		if err != nil {
			t.Fatalf("marshal fixture %s: %v", c.callID, err)
		}
		if _, err := store.UpsertCall(ctx, body); err != nil {
			t.Fatalf("upsert fixture %s: %v", c.callID, err)
		}
	}

	filter := BusinessAnalysisFilter{TitleQuery: "loss reason buckets"}
	rows, err := store.SummarizeBusinessAnalysisDimension(ctx, BusinessAnalysisDimensionSummaryParams{
		Filter:    filter,
		Dimension: "loss_reason",
		Limit:     50,
	})
	if err != nil {
		t.Fatalf("SummarizeBusinessAnalysisDimension loss_reason: %v", err)
	}

	got := map[string]int64{}
	for _, row := range rows {
		got[row.Value] = row.CallCount
	}
	for _, bucket := range []string{
		"price", "no_decision", "competitor", "timing", "feature_gap", "budget", "relationship", "unknown_other",
	} {
		if got[bucket] == 0 {
			t.Fatalf("loss_reason bucket %q missing or zero in dimension summary: %+v", bucket, rows)
		}
	}
	if _, ok := got["<blank>"]; !ok {
		t.Fatalf("expected <blank> coverage row for the no-loss-reason fixture: %+v", rows)
	}

	// Privacy boundary: raw loss-reason strings must never appear as
	// dimension values.
	for _, leak := range []string{"Price too high", "Timeline uncertainty", "Lost to Competitor", "Budget cuts", "Decided to build internally"} {
		for _, row := range rows {
			if strings.Contains(row.Value, leak) {
				t.Fatalf("loss_reason dimension exposed raw value %q in row %+v", leak, row)
			}
		}
	}
}

// TestSummarizeBusinessAnalysisDimensionLossReasonAliasFields proves the gap-
// follow-up extension: common Opportunity loss-reason field aliases beyond
// the original Salesforce-style four (LossReason, Loss_Reason__c,
// Closed_Lost_Reason__c, Closed_Lost_Reason_Detail__c) also feed the
// normalized loss_reason bucket dimension. Raw values still never escape.
func TestSummarizeBusinessAnalysisDimensionLossReasonAliasFields(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	// Each fixture uses a different alias field name and a raw loss-reason
	// value that resolves to a deterministic normalized bucket.
	type aliasCase struct {
		callID    string
		fieldName string
		raw       string
		bucket    string
	}
	cases := []aliasCase{
		{callID: "call-loss-alias-001", fieldName: "Reason_Lost__c", raw: "Pricing too high", bucket: "price"},
		{callID: "call-loss-alias-002", fieldName: "Lost_Reason__c", raw: "Project Postponed", bucket: "timing"},
		{callID: "call-loss-alias-003", fieldName: "lossReason", raw: "Feature Gap", bucket: "feature_gap"},
		{callID: "call-loss-alias-004", fieldName: "OpportunityLossReason", raw: "Competitor displacement", bucket: "competitor"},
	}
	for _, c := range cases {
		raw := map[string]any{
			"id":       c.callID,
			"title":    "loss reason aliases " + c.callID,
			"started":  "2026-02-14T15:00:00Z",
			"duration": 1200,
			"metaData": map[string]any{
				"system":    "Zoom",
				"direction": "Conference",
				"scope":     "External",
			},
			"context": []any{
				map[string]any{
					"objectType": "Opportunity",
					"id":         "opp-" + c.callID,
					"fields": []any{
						map[string]any{"name": "StageName", "value": "Closed Lost"},
						map[string]any{"name": c.fieldName, "value": c.raw},
					},
				},
			},
		}
		body, err := json.Marshal(raw)
		if err != nil {
			t.Fatalf("marshal alias fixture %s: %v", c.callID, err)
		}
		if _, err := store.UpsertCall(ctx, body); err != nil {
			t.Fatalf("upsert alias fixture %s: %v", c.callID, err)
		}
	}

	rows, err := store.SummarizeBusinessAnalysisDimension(ctx, BusinessAnalysisDimensionSummaryParams{
		Filter:    BusinessAnalysisFilter{TitleQuery: "loss reason aliases"},
		Dimension: "loss_reason",
		Limit:     50,
	})
	if err != nil {
		t.Fatalf("SummarizeBusinessAnalysisDimension loss_reason: %v", err)
	}

	got := map[string]int64{}
	for _, row := range rows {
		got[row.Value] = row.CallCount
	}
	for _, c := range cases {
		if got[c.bucket] == 0 {
			t.Fatalf("expected alias field %q -> bucket %q to populate the dimension; rows=%+v", c.fieldName, c.bucket, rows)
		}
	}
	for _, leak := range []string{"Pricing too high", "Project Postponed", "Feature Gap", "Competitor displacement"} {
		for _, row := range rows {
			if strings.Contains(row.Value, leak) {
				t.Fatalf("loss_reason dimension exposed raw value %q via alias field in row %+v", leak, row)
			}
		}
	}
}
