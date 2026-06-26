package mcp

import (
	"strings"
	"testing"
)

func TestCohortTokenRoundTrip(t *testing.T) {
	t.Parallel()

	filter := callFilter{
		FromDate:         "2026-04-01",
		ToDate:           "2026-04-30",
		LifecycleBucket:  "active_sales_pipeline",
		TranscriptStatus: "present",
	}
	normalized, err := normalizeCallFilter(filter)
	if err != nil {
		t.Fatalf("normalizeCallFilter: %v", err)
	}
	token, err := encodeCohortToken(normalized)
	if err != nil {
		t.Fatalf("encodeCohortToken: %v", err)
	}
	if !strings.HasPrefix(token, cohortTokenPrefix) {
		t.Fatalf("token prefix=%q want %q", token, cohortTokenPrefix)
	}
	decoded, err := decodeCohortToken(token)
	if err != nil {
		t.Fatalf("decodeCohortToken: %v", err)
	}
	if !cohortFiltersEquivalent(normalized, decoded) {
		t.Fatalf("decoded filter mismatch: %+v vs %+v", decoded, normalized)
	}
}

func TestResolveCohortFilterTokenOnly(t *testing.T) {
	t.Parallel()

	filter := callFilter{Quarter: "2026-Q1"}
	normalized, err := normalizeCallFilter(filter)
	if err != nil {
		t.Fatalf("normalizeCallFilter: %v", err)
	}
	token, err := encodeCohortToken(normalized)
	if err != nil {
		t.Fatalf("encodeCohortToken: %v", err)
	}
	resolved, err := resolveCohortFilter(callFilter{}, token)
	if err != nil {
		t.Fatalf("resolveCohortFilter: %v", err)
	}
	if !cohortFiltersEquivalent(normalized, resolved) {
		t.Fatalf("resolved filter mismatch")
	}
}

func TestResolveCohortFilterMismatch(t *testing.T) {
	t.Parallel()

	filterA := callFilter{Quarter: "2026-Q1"}
	normalizedA, err := normalizeCallFilter(filterA)
	if err != nil {
		t.Fatalf("normalizeCallFilter: %v", err)
	}
	token, err := encodeCohortToken(normalizedA)
	if err != nil {
		t.Fatalf("encodeCohortToken: %v", err)
	}
	_, err = resolveCohortFilter(callFilter{Quarter: "2026-Q2"}, token)
	if err == nil || !strings.Contains(err.Error(), "cohort_token") {
		t.Fatalf("expected cohort_token mismatch error, got %v", err)
	}
}

func TestDecodeCohortTokenMalformed(t *testing.T) {
	t.Parallel()

	_, err := decodeCohortToken("not-a-token")
	if err == nil || !strings.Contains(err.Error(), "cohort_token") {
		t.Fatalf("expected cohort_token error, got %v", err)
	}
	_, err = decodeCohortToken(cohortTokenPrefix + "!!!")
	if err == nil || !strings.Contains(err.Error(), "cohort_token") {
		t.Fatalf("expected malformed cohort_token error, got %v", err)
	}
}
