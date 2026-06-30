package sqlite

import (
	"context"
	"encoding/json"
	"testing"
)

func TestBusinessSafeCRMDimensionsCustomerSegmentFilterAndGroup(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	raw := map[string]any{
		"id":       "call-crm-segment-001",
		"title":    "CRM segment filter test",
		"started":  "2026-03-01T12:00:00Z",
		"duration": 900,
		"metaData": map[string]any{
			"system":    "Zoom",
			"direction": "Conference",
			"scope":     "External",
		},
		"context": []any{
			map[string]any{
				"objectType": "Account",
				"id":         "acct-segment-001",
				"fields": []any{
					map[string]any{"name": "Customer_Segment_Type__c", "value": "Enterprise"},
					map[string]any{"name": "AnnualRevenue", "value": 25000000},
				},
			},
			map[string]any{
				"objectType": "Opportunity",
				"id":         "opp-segment-001",
				"fields": []any{
					map[string]any{"name": "CloseDate", "value": "2026-06-15"},
					map[string]any{"name": "Amount", "value": 120000},
				},
			},
		},
	}
	body, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	if _, err := store.UpsertCall(ctx, body); err != nil {
		t.Fatalf("upsert call: %v", err)
	}

	var segment string
	if err := store.DB().QueryRowContext(ctx, `SELECT account_customer_segment_type FROM call_facts WHERE call_id = ?`, "call-crm-segment-001").Scan(&segment); err != nil {
		t.Fatalf("read promoted segment: %v", err)
	}
	if segment != "Enterprise" {
		t.Fatalf("account_customer_segment_type = %q, want Enterprise", segment)
	}

	filtered, err := store.SearchBusinessAnalysisCalls(ctx, BusinessAnalysisCallSearchParams{
		Filter: BusinessAnalysisFilter{
			DimensionFilters: []BusinessAnalysisDimensionFilter{
				{Dimension: "account_customer_segment_type", Operator: "equals", Values: []string{"Enterprise"}},
			},
		},
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("filter search: %v", err)
	}
	if filtered.Summary.CallCount != 1 {
		t.Fatalf("expected 1 filtered call, got %d", filtered.Summary.CallCount)
	}

	dateFiltered, err := store.SearchBusinessAnalysisCalls(ctx, BusinessAnalysisCallSearchParams{
		Filter: BusinessAnalysisFilter{
			DimensionFilters: []BusinessAnalysisDimensionFilter{
				{Dimension: "opportunity_close_date", Operator: "gte", Values: []string{"2026-06-01"}},
			},
		},
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("date range filter search: %v", err)
	}
	if dateFiltered.Summary.CallCount != 1 {
		t.Fatalf("expected 1 date-filtered call, got %d", dateFiltered.Summary.CallCount)
	}

	grouped, err := store.SummarizeBusinessAnalysisDimension(ctx, BusinessAnalysisDimensionSummaryParams{
		Dimension: "account_customer_segment_type",
		Filter:    BusinessAnalysisFilter{},
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("dimension summary: %v", err)
	}
	found := false
	for _, row := range grouped {
		if row.Value == "Enterprise" && row.CallCount == 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected Enterprise bucket in summary, got %+v", grouped)
	}

	revenueGrouped, err := store.SummarizeBusinessAnalysisDimension(ctx, BusinessAnalysisDimensionSummaryParams{
		Dimension: "account_annual_revenue_bucket",
		Filter:    BusinessAnalysisFilter{},
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("revenue bucket summary: %v", err)
	}
	revenueFound := false
	for _, row := range revenueGrouped {
		if row.Value == "10m_100m" && row.CallCount >= 1 {
			revenueFound = true
		}
	}
	if !revenueFound {
		t.Fatalf("expected 10m_100m revenue bucket, got %+v", revenueGrouped)
	}

	closeMonthGrouped, err := store.SummarizeBusinessAnalysisDimension(ctx, BusinessAnalysisDimensionSummaryParams{
		Dimension: "opportunity_close_month",
		Filter:    BusinessAnalysisFilter{},
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("close month summary: %v", err)
	}
	monthFound := false
	for _, row := range closeMonthGrouped {
		if row.Value == "2026-06" && row.CallCount >= 1 {
			monthFound = true
		}
	}
	if !monthFound {
		t.Fatalf("expected 2026-06 close month bucket, got %+v", closeMonthGrouped)
	}

	callFactGrouped, err := store.SummarizeCallFacts(ctx, CallFactsSummaryParams{
		GroupBy: "opportunity_close_month",
		Limit:   10,
	})
	if err != nil {
		t.Fatalf("summarize call facts by close month: %v", err)
	}
	callFactMonthFound := false
	for _, row := range callFactGrouped {
		if row.GroupValue == "2026-06" && row.CallCount >= 1 {
			callFactMonthFound = true
		}
	}
	if !callFactMonthFound {
		t.Fatalf("expected 2026-06 call-fact close month bucket, got %+v", callFactGrouped)
	}
}

func TestExcludedCRMDimensionsRejectedFromFilters(t *testing.T) {
	t.Parallel()

	for _, dim := range []string{"website", "owner_id", "marketing_notes"} {
		_, err := BusinessAnalysisFilterDimensionCanonical(dim)
		if err == nil {
			t.Fatalf("expected %q rejection, got nil", dim)
		}
	}
}
