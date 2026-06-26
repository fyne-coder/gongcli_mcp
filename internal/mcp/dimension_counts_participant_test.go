package mcp

import (
	"strings"
	"testing"

	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
)

func seedParticipantPolicyFixtures(t *testing.T, store *sqlite.Store) {
	t.Helper()
	ctx := t.Context()

	for _, raw := range []map[string]any{
		{
			"id": "call_participant_external", "title": "Participant policy buyer domain", "started": "2026-02-18T15:00:00Z", "duration": 1800,
			"parties": []any{map[string]any{"id": "buyer", "emailAddress": "buyer@acme.example"}},
		},
		{
			"id": "call_participant_internal", "title": "Participant policy seller coaching", "started": "2026-02-19T15:00:00Z", "duration": 1800,
			"parties": []any{map[string]any{"id": "rep", "emailAddress": "rep@internal.example"}},
		},
		{
			"id": "call_participant_mixed", "title": "Participant policy mixed domains", "started": "2026-02-20T15:00:00Z", "duration": 1800,
			"parties": []any{
				map[string]any{"id": "buyer", "emailAddress": "buyer@acme.example"},
				map[string]any{"id": "rep", "emailAddress": "coach@internal.example"},
			},
		},
		{
			"id": "call_participant_unknown", "title": "Participant policy no email", "started": "2026-02-21T15:00:00Z", "duration": 1800,
			"parties": []any{map[string]any{"id": "buyer", "title": "VP Operations"}},
		},
	} {
		if _, err := store.UpsertCall(ctx, mustJSON(t, raw)); err != nil {
			t.Fatalf("upsert participant policy call: %v", err)
		}
	}
}

func TestFacadeQueryDimensionCountsParticipantDomainExternalRanking(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	seedParticipantPolicyFixtures(t, store)

	server := NewServerWithOptions(store, "gongmcp", "test",
		WithToolAllowlist([]string{FacadeToolQuery}),
		WithFacadeRoutedToolAllowlist([]string{internalRoutedToolDimensionCounts}),
	)
	result, err := server.executeFacadeDispatch(t.Context(), FacadeToolQuery, mustFacadeArgs(t, OpQueryDimensionCounts, map[string]any{
		"filter": map[string]any{
			"title_query": "participant policy",
		},
		"dimension":                      "participant_domain",
		"participant_affiliation_filter": "external",
		"limit":                          10,
	}))
	if err != nil {
		t.Fatalf("query.dimension_counts participant_domain dispatch: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected isError: %+v", result)
	}
	inner, _ := decodeFacadeWrapper(t, result)["result"].(map[string]any)
	if got, _ := inner["dimension"].(string); got != "participant_domain" {
		t.Fatalf("dimension=%q want participant_domain", got)
	}
	coverage, _ := inner["coverage_summary"].(map[string]any)
	if domains, _ := coverage["internal_domains"].([]any); len(domains) != 1 || domains[0] != "internal.example" {
		t.Fatalf("internal_domains=%v want [internal.example]", coverage["internal_domains"])
	}
	summary, _ := coverage["participant_affiliation_summary"].(map[string]any)
	if summary["external"] != float64(1) || summary["mixed"] != float64(1) || summary["internal"] != float64(1) || summary["unknown"] != float64(1) {
		t.Fatalf("participant_affiliation_summary=%v want full cohort external=1 mixed=1 internal=1 unknown=1", summary)
	}
	if rate, _ := coverage["resolvable_domain_rate"].(float64); rate != 0.75 {
		t.Fatalf("resolvable_domain_rate=%v want 0.75 from full cohort coverage", rate)
	}
	rows, _ := inner["rows"].([]any)
	if len(rows) != 1 {
		t.Fatalf("expected one external domain bucket, got %v", mustJSONForTest(t, rows))
	}
	row, _ := rows[0].(map[string]any)
	if bucket, _ := row["bucket"].(string); bucket != "acme.example" {
		t.Fatalf("bucket=%q want acme.example", bucket)
	}
	if count, _ := row["call_count"].(float64); count != 2 {
		t.Fatalf("call_count=%v want 2", row["call_count"])
	}
}

func TestFacadeQueryDimensionCountsParticipantAffiliationClassification(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	seedParticipantPolicyFixtures(t, store)

	server := NewServerWithOptions(store, "gongmcp", "test",
		WithToolAllowlist([]string{FacadeToolQuery}),
		WithFacadeRoutedToolAllowlist([]string{internalRoutedToolDimensionCounts}),
	)
	result, err := server.executeFacadeDispatch(t.Context(), FacadeToolQuery, mustFacadeArgs(t, OpQueryDimensionCounts, map[string]any{
		"filter": map[string]any{
			"title_query": "participant policy",
		},
		"dimension": "participant_affiliation",
		"limit":     10,
	}))
	if err != nil {
		t.Fatalf("query.dimension_counts participant_affiliation dispatch: %v", err)
	}
	inner, _ := decodeFacadeWrapper(t, result)["result"].(map[string]any)
	rows, _ := inner["rows"].([]any)
	buckets := map[string]float64{}
	for _, item := range rows {
		row, _ := item.(map[string]any)
		buckets[row["bucket"].(string)] = row["call_count"].(float64)
	}
	if buckets["internal"] != 1 || buckets["external"] != 1 || buckets["mixed"] != 1 || buckets["unknown"] != 1 {
		t.Fatalf("unexpected affiliation buckets: %v", buckets)
	}
	coverage, _ := inner["coverage_summary"].(map[string]any)
	if rate, _ := coverage["resolvable_domain_rate"].(float64); rate != 0.75 {
		t.Fatalf("resolvable_domain_rate=%v want 0.75", rate)
	}
}

func TestFacadeQueryDimensionCountsParticipantEmailRankingAllowed(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	seedParticipantPolicyFixtures(t, store)

	server := NewServerWithOptions(store, "gongmcp", "test",
		WithToolAllowlist([]string{FacadeToolQuery}),
		WithFacadeRoutedToolAllowlist([]string{internalRoutedToolDimensionCounts}),
	)
	result, err := server.executeFacadeDispatch(t.Context(), FacadeToolQuery, mustFacadeArgs(t, OpQueryDimensionCounts, map[string]any{
		"filter": map[string]any{
			"title_query": "participant policy seller",
		},
		"dimension":                      "participant_email",
		"include_participant_emails":     true,
		"participant_affiliation_filter": "internal",
		"limit":                          10,
	}))
	if err != nil {
		t.Fatalf("query.dimension_counts participant_email dispatch: %v", err)
	}
	inner, _ := decodeFacadeWrapper(t, result)["result"].(map[string]any)
	rows, _ := inner["rows"].([]any)
	if len(rows) != 1 {
		t.Fatalf("expected one internal participant email bucket, got %v", mustJSONForTest(t, rows))
	}
	row, _ := rows[0].(map[string]any)
	if email, _ := row["bucket"].(string); email != "rep@internal.example" {
		t.Fatalf("bucket=%q want rep@internal.example", email)
	}
}

func TestFacadeQueryDimensionCountsParticipantEmailRankingAllowedByDefault(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	seedParticipantPolicyFixtures(t, store)

	server := NewServerWithOptions(store, "gongmcp", "test",
		WithToolAllowlist([]string{FacadeToolQuery}),
		WithFacadeRoutedToolAllowlist([]string{internalRoutedToolDimensionCounts}),
	)
	_, err := server.executeFacadeDispatch(t.Context(), FacadeToolQuery, mustFacadeArgs(t, OpQueryDimensionCounts, map[string]any{
		"filter": map[string]any{
			"title_query": "participant policy seller",
		},
		"dimension": "participant_email",
		"limit":     10,
	}))
	if err != nil {
		t.Fatalf("participant_email ranking should be allowed by default: %v", err)
	}
}

func TestFacadeQueryDimensionCountsParticipantEmailPolicySwitchBlocksRanking(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	seedParticipantPolicyFixtures(t, store)

	server := NewServerWithOptions(store, "gongmcp", "test",
		WithToolAllowlist([]string{FacadeToolQuery}),
		WithFacadeRoutedToolAllowlist([]string{internalRoutedToolDimensionCounts}),
		WithPolicySwitches(PolicySwitches{HideContactEmails: true}),
	)
	_, err := server.executeFacadeDispatch(t.Context(), FacadeToolQuery, mustFacadeArgs(t, OpQueryDimensionCounts, map[string]any{
		"filter": map[string]any{
			"title_query": "participant policy seller",
		},
		"dimension": "participant_email",
		"limit":     10,
	}))
	if err == nil {
		t.Fatal("expected participant_email policy-switch error")
	}
	if !strings.Contains(err.Error(), "hide_contact_emails") {
		t.Fatalf("error=%v want hide_contact_emails guidance", err)
	}
}

func TestDirectDimensionSummaryParticipantEmailPolicySwitch(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	seedParticipantPolicyFixtures(t, store)

	server := NewServerWithOptions(store, "gongmcp", "test")
	result, err := server.executeBusinessAnalysisTool(t.Context(), toolsCallParams{
		Name: "summarize_calls_by_filters",
		Arguments: mustJSON(t, map[string]any{
			"filter": map[string]any{
				"title_query": "participant policy",
			},
			"dimension": "participant_email",
			"limit":     10,
		}),
	})
	if err != nil {
		t.Fatalf("participant_email dimension should be allowed by default: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected isError: %+v", result)
	}

	privacyServer := NewServerWithOptions(store, "gongmcp", "test",
		WithPolicySwitches(PolicySwitches{HideContactEmails: true}),
	)
	_, err = privacyServer.executeBusinessAnalysisTool(t.Context(), toolsCallParams{
		Name: "summarize_calls_by_filters",
		Arguments: mustJSON(t, map[string]any{
			"filter": map[string]any{
				"title_query": "participant policy",
			},
			"dimension": "participant_email",
			"limit":     10,
		}),
	})
	if err == nil {
		t.Fatal("expected direct participant_email dimension summary to be rejected when hide_contact_emails is enabled")
	}
	if !strings.Contains(err.Error(), "hide_contact_emails") {
		t.Fatalf("error=%v want hide_contact_emails guidance", err)
	}
}

func TestFacadeQueryDimensionCountsCustomInternalDomains(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	seedParticipantPolicyFixtures(t, store)

	server := NewServerWithOptions(store, "gongmcp", "test",
		WithToolAllowlist([]string{FacadeToolQuery}),
		WithFacadeRoutedToolAllowlist([]string{internalRoutedToolDimensionCounts}),
		WithInternalParticipantDomains([]string{"acme.example"}),
	)
	result, err := server.executeFacadeDispatch(t.Context(), FacadeToolQuery, mustFacadeArgs(t, OpQueryDimensionCounts, map[string]any{
		"filter": map[string]any{
			"title_query": "participant policy buyer",
		},
		"dimension": "participant_affiliation",
		"limit":     10,
	}))
	if err != nil {
		t.Fatalf("query.dimension_counts custom internal domains: %v", err)
	}
	inner, _ := decodeFacadeWrapper(t, result)["result"].(map[string]any)
	rows, _ := inner["rows"].([]any)
	for _, item := range rows {
		row, _ := item.(map[string]any)
		if bucket, _ := row["bucket"].(string); bucket == "external" {
			t.Fatalf("acme.example should classify as internal via server option, got external bucket")
		}
	}
	coverage, _ := inner["coverage_summary"].(map[string]any)
	domainsJSON := strings.ToLower(mustJSONForTest(t, coverage["internal_domains"]))
	if !strings.Contains(domainsJSON, "acme.example") {
		t.Fatalf("internal_domains=%v want acme.example", coverage["internal_domains"])
	}
}

func TestNormalizeParticipantAffiliationFilterAliases(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		in   string
		want string
	}{
		{"marketing", "external"},
		{"coaching", "internal"},
		{"any", ""},
	} {
		got, err := normalizeParticipantAffiliationFilter(tc.in)
		if err != nil {
			t.Fatalf("normalizeParticipantAffiliationFilter(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("normalizeParticipantAffiliationFilter(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}
