package mcp

import (
	"reflect"
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

func TestNormalizeCallFilterDimensionFiltersRejectsUnsafeInputs(t *testing.T) {
	longValue := strings.Repeat("x", sqlite.MaxBusinessAnalysisDimensionFilterValueLength+1)
	tests := []struct {
		name   string
		filter sqlite.BusinessAnalysisDimensionFilter
	}{
		{name: "unknown dimension", filter: sqlite.BusinessAnalysisDimensionFilter{Dimension: "account_name", Operator: "equals", Values: []string{"Acme"}}},
		{name: "computed bucket", filter: sqlite.BusinessAnalysisDimensionFilter{Dimension: "loss_reason", Operator: "equals", Values: []string{"price"}}},
		{name: "bad operator", filter: sqlite.BusinessAnalysisDimensionFilter{Dimension: "account_revenue_range", Operator: "contains", Values: []string{"ENT"}}},
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
	allowed := sqlite.AllowedBusinessAnalysisFilterDimensions()
	highRisk := map[string]bool{
		"account_name":  true,
		"crm_object_id": true,
		"loss_reason":   true,
		"persona":       true,
		"won_lost":      true,
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
		dimensionEnum, ok := dimension["enum"].([]string)
		if !ok {
			t.Fatalf("%s dimension enum has type %T", op.Name, dimension["enum"])
		}
		if !reflect.DeepEqual(dimensionEnum, allowed) {
			t.Fatalf("%s dimension enum=%v want %v", op.Name, dimensionEnum, allowed)
		}
		for _, name := range dimensionEnum {
			if highRisk[name] {
				t.Fatalf("%s dimension enum exposes high-risk field %q", op.Name, name)
			}
		}
		operator, ok := itemProps["operator"].(map[string]any)
		if !ok {
			t.Fatalf("%s dimension_filters item missing operator schema", op.Name)
		}
		operatorEnum, ok := operator["enum"].([]string)
		if !ok {
			t.Fatalf("%s operator enum has type %T", op.Name, operator["enum"])
		}
		if !reflect.DeepEqual(operatorEnum, []string{"equals", "in"}) {
			t.Fatalf("%s operator enum=%v want [equals in]", op.Name, operatorEnum)
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
