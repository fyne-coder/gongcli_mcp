package mcp

import (
	"strings"
	"testing"

	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
)

func TestNormalizeCallFilterDimensionFilters(t *testing.T) {
	filter, err := normalizeCallFilter(callFilter{
		DimensionFilters: []sqlite.BusinessAnalysisDimensionFilter{
			{Dimension: "revenue_range", Operator: "IN", Values: []string{"D: ENT", "C: MM", "C: MM"}},
			{Dimension: "stage", Operator: "equals", Values: []string{"Discovery"}},
		},
	})
	if err != nil {
		t.Fatalf("normalizeCallFilter dimension filters: %v", err)
	}
	if got := len(filter.DimensionFilters); got != 2 {
		t.Fatalf("dimension filter count=%d want 2: %+v", got, filter.DimensionFilters)
	}
	first := filter.DimensionFilters[0]
	if first.Dimension != "account_revenue_range" || first.Operator != "in" || strings.Join(first.Values, ",") != "C: MM,D: ENT" {
		t.Fatalf("unexpected normalized revenue filter: %+v", first)
	}
	if !businessAnalysisFilterIsSelective(filter) {
		t.Fatalf("dimension filters should make business-analysis filter selective")
	}
}

func TestNormalizeCallFilterDimensionFiltersAcceptsBackedDurationAndEmail(t *testing.T) {
	filter, err := normalizeCallFilter(callFilter{
		DimensionFilters: []sqlite.BusinessAnalysisDimensionFilter{
			{Dimension: "call_length", Operator: "gte", Values: []string{"900"}},
			{Dimension: "participant_email", Operator: "equals", Values: []string{"Buyer@Example.COM"}},
		},
	})
	if err != nil {
		t.Fatalf("normalizeCallFilter backed duration/email filters: %v", err)
	}
	if len(filter.DimensionFilters) != 2 {
		t.Fatalf("dimension filter count=%d want 2: %+v", len(filter.DimensionFilters), filter.DimensionFilters)
	}
	if filter.DimensionFilters[0].Dimension != "duration_seconds" || filter.DimensionFilters[0].Operator != "gte" || filter.DimensionFilters[0].Values[0] != "900" {
		t.Fatalf("unexpected duration filter: %+v", filter.DimensionFilters[0])
	}
	if filter.DimensionFilters[1].Dimension != "participant_email" || filter.DimensionFilters[1].Operator != "equals" || filter.DimensionFilters[1].Values[0] != "buyer@example.com" {
		t.Fatalf("unexpected participant email filter: %+v", filter.DimensionFilters[1])
	}
}

func TestNormalizeCallFilterCanonicalizesSingleQuarterDimensionFilter(t *testing.T) {
	filter, err := normalizeCallFilter(callFilter{
		DimensionFilters: []sqlite.BusinessAnalysisDimensionFilter{
			{Dimension: "quarter", Operator: "in", Values: []string{"2026-Q2"}},
			{Dimension: "duration_seconds", Operator: "gte", Values: []string{"300"}},
		},
	})
	if err != nil {
		t.Fatalf("normalizeCallFilter quarter dimension filter: %v", err)
	}
	if filter.Quarter != "2026-Q2" || filter.FromDate != "2026-04-01" || filter.ToDate != "2026-06-30" {
		t.Fatalf("quarter dimension filter was not canonicalized to top-level dates: %+v", filter)
	}
	if len(filter.DimensionFilters) != 1 {
		t.Fatalf("dimension filter count=%d want only duration filter: %+v", len(filter.DimensionFilters), filter.DimensionFilters)
	}
	if got := filter.DimensionFilters[0]; got.Dimension != "duration_seconds" || got.Operator != "gte" || got.Values[0] != "300" {
		t.Fatalf("unexpected remaining dimension filter: %+v", got)
	}
}

func TestNormalizeCallFilterCanonicalizesContiguousQuarterDimensionFilters(t *testing.T) {
	filter, err := normalizeCallFilter(callFilter{
		DimensionFilters: []sqlite.BusinessAnalysisDimensionFilter{
			{Dimension: "quarter", Operator: "in", Values: []string{"2026-Q2", "2025-Q3", "2026-Q1", "2025-Q4"}},
			{Dimension: "duration_seconds", Operator: "gte", Values: []string{"300"}},
		},
	})
	if err != nil {
		t.Fatalf("normalizeCallFilter multi-quarter dimension filter: %v", err)
	}
	if filter.Quarter != "" || filter.FromDate != "2025-07-01" || filter.ToDate != "2026-06-30" {
		t.Fatalf("contiguous quarter dimension filters were not canonicalized to a date range: %+v", filter)
	}
	if len(filter.DimensionFilters) != 1 {
		t.Fatalf("dimension filter count=%d want only duration filter: %+v", len(filter.DimensionFilters), filter.DimensionFilters)
	}
	if got := filter.DimensionFilters[0]; got.Dimension != "duration_seconds" || got.Operator != "gte" || got.Values[0] != "300" {
		t.Fatalf("unexpected remaining dimension filter: %+v", got)
	}
}

func TestNormalizeCallFilterIntersectsContiguousQuarterDimensionFilters(t *testing.T) {
	tests := []struct {
		name     string
		filter   callFilter
		wantFrom string
		wantTo   string
	}{
		{
			name: "explicit dates narrow quarter",
			filter: callFilter{
				FromDate: "2025-08-01",
				DimensionFilters: []sqlite.BusinessAnalysisDimensionFilter{
					{Dimension: "quarter", Operator: "in", Values: []string{"2025-Q3"}},
				},
			},
			wantFrom: "2025-08-01",
			wantTo:   "2025-09-30",
		},
		{
			name: "top-level quarter narrows quarter range",
			filter: callFilter{
				Quarter: "2025-Q4",
				DimensionFilters: []sqlite.BusinessAnalysisDimensionFilter{
					{Dimension: "quarter", Operator: "in", Values: []string{"2025-Q3", "2025-Q4", "2026-Q1"}},
				},
			},
			wantFrom: "2025-10-01",
			wantTo:   "2025-12-31",
		},
		{
			name: "duplicate mixed-format quarters remain contiguous",
			filter: callFilter{
				DimensionFilters: []sqlite.BusinessAnalysisDimensionFilter{
					{Dimension: "quarter", Operator: "in", Values: []string{"Q3-2025", "2025Q3", "2025-Q4"}},
				},
			},
			wantFrom: "2025-07-01",
			wantTo:   "2025-12-31",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter, err := normalizeCallFilter(tt.filter)
			if err != nil {
				t.Fatalf("normalizeCallFilter: %v", err)
			}
			if filter.FromDate != tt.wantFrom || filter.ToDate != tt.wantTo {
				t.Fatalf("date range=%s..%s want %s..%s filter=%+v", filter.FromDate, filter.ToDate, tt.wantFrom, tt.wantTo, filter)
			}
			for _, dimensionFilter := range filter.DimensionFilters {
				if dimensionFilter.Dimension == "quarter" {
					t.Fatalf("canonicalized filter kept quarter dimension filter: %+v", filter.DimensionFilters)
				}
			}
		})
	}
}

func TestNormalizeCallFilterRejectsNonOverlappingQuarterDimensionFilters(t *testing.T) {
	tests := []struct {
		name   string
		filter callFilter
	}{
		{
			name: "top-level quarter outside dimension range",
			filter: callFilter{
				Quarter: "2025-Q1",
				DimensionFilters: []sqlite.BusinessAnalysisDimensionFilter{
					{Dimension: "quarter", Operator: "in", Values: []string{"2025-Q3", "2025-Q4"}},
				},
			},
		},
		{
			name: "explicit date outside dimension range",
			filter: callFilter{
				FromDate: "2026-01-01",
				DimensionFilters: []sqlite.BusinessAnalysisDimensionFilter{
					{Dimension: "quarter", Operator: "in", Values: []string{"2025-Q3"}},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := normalizeCallFilter(tt.filter); err == nil {
				t.Fatal("normalizeCallFilter accepted non-overlapping quarter filters")
			}
		})
	}
}

func TestNormalizeCallFilterKeepsNonContiguousQuarterDimensionFilters(t *testing.T) {
	filter, err := normalizeCallFilter(callFilter{
		DimensionFilters: []sqlite.BusinessAnalysisDimensionFilter{
			{Dimension: "quarter", Operator: "in", Values: []string{"2025-Q3", "2026-Q2"}},
			{Dimension: "duration_seconds", Operator: "gte", Values: []string{"300"}},
		},
	})
	if err != nil {
		t.Fatalf("normalizeCallFilter non-contiguous quarter dimension filter: %v", err)
	}
	if filter.FromDate != "" || filter.ToDate != "" {
		t.Fatalf("non-contiguous quarter filters should not be broadened into a date range: %+v", filter)
	}
	if len(filter.DimensionFilters) != 2 {
		t.Fatalf("non-contiguous quarter dimension filter should remain: %+v", filter.DimensionFilters)
	}
	got := make(map[string]sqlite.BusinessAnalysisDimensionFilter, len(filter.DimensionFilters))
	for _, dimensionFilter := range filter.DimensionFilters {
		got[dimensionFilter.Dimension] = dimensionFilter
	}
	if _, ok := got["quarter"]; !ok {
		t.Fatalf("non-contiguous quarter dimension filter should remain: %+v", filter.DimensionFilters)
	}
}

func TestNormalizeCallFilterRejectsConflictingQuarterDimensionFilter(t *testing.T) {
	_, err := normalizeCallFilter(callFilter{
		Quarter: "2026-Q1",
		DimensionFilters: []sqlite.BusinessAnalysisDimensionFilter{
			{Dimension: "quarter", Operator: "equals", Values: []string{"2026-Q2"}},
		},
	})
	if err == nil {
		t.Fatal("normalizeCallFilter accepted conflicting top-level and dimension-filter quarters")
	}
}

func TestNormalizeCallFilterDimensionFiltersAcceptsBackedIdentifiersAndDerivedDimensions(t *testing.T) {
	filter, err := normalizeCallFilter(callFilter{
		DimensionFilters: []sqlite.BusinessAnalysisDimensionFilter{
			{Dimension: "account_name", Operator: "equals", Values: []string{"Example Co"}},
			{Dimension: "crm_object_id", Operator: "in", Values: []string{"opp-1", "acct-1"}},
			{Dimension: "loss_reason", Operator: "equals", Values: []string{"price"}},
			{Dimension: "participant_title", Operator: "equals", Values: []string{"procurement"}},
		},
	})
	if err != nil {
		t.Fatalf("normalizeCallFilter backed identifier/derived filters: %v", err)
	}
	got := make(map[string]sqlite.BusinessAnalysisDimensionFilter, len(filter.DimensionFilters))
	for _, dimensionFilter := range filter.DimensionFilters {
		got[dimensionFilter.Dimension] = dimensionFilter
	}
	for _, want := range []string{"account_name", "crm_object_id", "loss_reason", "persona"} {
		if _, ok := got[want]; !ok {
			t.Fatalf("normalized filters missing %q: %+v", want, filter.DimensionFilters)
		}
	}
	if disallowed := sqlite.DisallowedBusinessAnalysisFilterDimensions(); len(disallowed) != 0 {
		t.Fatalf("package-2 disallow list must be empty, got %v", disallowed)
	}
}

func TestNormalizeCallFilterDimensionFiltersRejectsUnsafeInputs(t *testing.T) {
	longValue := strings.Repeat("x", sqlite.MaxBusinessAnalysisDimensionFilterValueLength+1)
	tests := []struct {
		name   string
		filter sqlite.BusinessAnalysisDimensionFilter
	}{
		{name: "unknown dimension", filter: sqlite.BusinessAnalysisDimensionFilter{Dimension: "account_nam", Operator: "equals", Values: []string{"Acme"}}},
		{name: "bad operator", filter: sqlite.BusinessAnalysisDimensionFilter{Dimension: "account_revenue_range", Operator: "contains", Values: []string{"ENT"}}},
		{name: "duration bad operator", filter: sqlite.BusinessAnalysisDimensionFilter{Dimension: "duration_seconds", Operator: "contains", Values: []string{"900"}}},
		{name: "duration non-numeric", filter: sqlite.BusinessAnalysisDimensionFilter{Dimension: "duration_seconds", Operator: "gte", Values: []string{"long"}}},
		{name: "between wrong arity", filter: sqlite.BusinessAnalysisDimensionFilter{Dimension: "duration_seconds", Operator: "between", Values: []string{"600"}}},
		{name: "blank values", filter: sqlite.BusinessAnalysisDimensionFilter{Dimension: "account_revenue_range", Operator: "in", Values: []string{" "}}},
		{name: "equals many values", filter: sqlite.BusinessAnalysisDimensionFilter{Dimension: "account_revenue_range", Operator: "equals", Values: []string{"C: MM", "D: ENT"}}},
		{name: "value too long", filter: sqlite.BusinessAnalysisDimensionFilter{Dimension: "account_revenue_range", Operator: "equals", Values: []string{longValue}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := normalizeCallFilter(callFilter{DimensionFilters: []sqlite.BusinessAnalysisDimensionFilter{tt.filter}}); err == nil {
				t.Fatalf("normalizeCallFilter succeeded for unsafe dimension filter: %+v", tt.filter)
			}
		})
	}
}

func TestFacadeBusinessAnalysisFilterSchemaAdvertisesDimensionFilters(t *testing.T) {
	disallowed := map[string]bool{}
	for _, name := range sqlite.DisallowedBusinessAnalysisFilterDimensions() {
		disallowed[name] = true
	}
	checked := 0
	for _, op := range FacadeOperations() {
		props, ok := op.InputSchema["properties"].(map[string]any)
		if !ok {
			continue
		}
		filterSchema, ok := props["filter"].(map[string]any)
		if !ok {
			continue
		}
		filterProps, ok := filterSchema["properties"].(map[string]any)
		if !ok {
			t.Fatalf("%s filter schema missing properties", op.Name)
		}
		dimensionFilters, ok := filterProps["dimension_filters"].(map[string]any)
		if !ok {
			t.Fatalf("%s filter schema missing dimension_filters", op.Name)
		}
		items, ok := dimensionFilters["items"].(map[string]any)
		if !ok {
			t.Fatalf("%s dimension_filters missing item schema", op.Name)
		}
		itemProps, ok := items["properties"].(map[string]any)
		if !ok {
			t.Fatalf("%s dimension_filters item missing properties", op.Name)
		}
		dimension, ok := itemProps["dimension"].(map[string]any)
		if !ok {
			t.Fatalf("%s dimension_filters item missing dimension schema", op.Name)
		}
		if _, hasEnum := dimension["enum"]; hasEnum {
			t.Fatalf("%s dimension schema must not advertise a closed enum", op.Name)
		}
		operator, ok := itemProps["operator"].(map[string]any)
		if !ok {
			t.Fatalf("%s dimension_filters item missing operator schema", op.Name)
		}
		operatorEnum, ok := operator["enum"].([]string)
		if !ok {
			t.Fatalf("%s operator enum has type %T", op.Name, operator["enum"])
		}
		if !strings.Contains(strings.Join(operatorEnum, ","), "gte") || !strings.Contains(strings.Join(operatorEnum, ","), "between") {
			t.Fatalf("%s operator enum=%v want numeric operators documented", op.Name, operatorEnum)
		}
		for _, name := range sqlite.BackedBusinessAnalysisFilterDimensions() {
			if disallowed[name] {
				t.Fatalf("backed dimension list includes disallowed field %q", name)
			}
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no facade operation filter schemas were checked")
	}
}

func TestCohortIDStableUnderDimensionFilterOrdering(t *testing.T) {
	a, err := normalizeCallFilter(callFilter{
		Quarter: "2026-Q1",
		DimensionFilters: []sqlite.BusinessAnalysisDimensionFilter{
			{Dimension: "stage", Operator: "equals", Values: []string{"Discovery"}},
			{Dimension: "revenue_range", Operator: "in", Values: []string{"D: ENT", "C: MM"}},
		},
	})
	if err != nil {
		t.Fatalf("normalize filter a: %v", err)
	}
	b, err := normalizeCallFilter(callFilter{
		Quarter: "2026-Q1",
		DimensionFilters: []sqlite.BusinessAnalysisDimensionFilter{
			{Dimension: "account_revenue_range", Operator: "in", Values: []string{"C: MM", "D: ENT"}},
			{Dimension: "opportunity_stage", Operator: "equals", Values: []string{"Discovery"}},
		},
	})
	if err != nil {
		t.Fatalf("normalize filter b: %v", err)
	}
	if cohortID(a, "") != cohortID(b, "") {
		t.Fatalf("cohort IDs should be stable after dimension-filter normalization: %s != %s", cohortID(a, ""), cohortID(b, ""))
	}
}
