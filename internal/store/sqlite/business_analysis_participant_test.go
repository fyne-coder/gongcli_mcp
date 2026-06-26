package sqlite

import (
	"context"
	"encoding/json"
	"testing"
)

func TestSummarizeBusinessAnalysisParticipantDimensions(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	for _, raw := range []map[string]any{
		{
			"id": "pg-participant-external", "title": "Participant external", "started": "2026-02-18T15:00:00Z", "duration": 1800,
			"parties": []any{map[string]any{"emailAddress": "buyer@acme.example"}},
		},
		{
			"id": "pg-participant-internal", "title": "Participant internal", "started": "2026-02-19T15:00:00Z", "duration": 1800,
			"parties": []any{map[string]any{"emailAddress": "rep@tradecentric.com"}},
		},
	} {
		payload, err := json.Marshal(raw)
		if err != nil {
			t.Fatalf("marshal call: %v", err)
		}
		if _, err := store.UpsertCall(ctx, payload); err != nil {
			t.Fatalf("upsert call: %v", err)
		}
	}

	filter := BusinessAnalysisFilter{TitleQuery: "Participant"}
	rows, err := store.SummarizeBusinessAnalysisDimension(ctx, BusinessAnalysisDimensionSummaryParams{
		Filter:          filter,
		Dimension:       "participant_affiliation",
		Limit:           10,
		InternalDomains: []string{"tradecentric.com"},
	})
	if err != nil {
		t.Fatalf("SummarizeBusinessAnalysisDimension participant_affiliation: %v", err)
	}
	buckets := map[string]int64{}
	for _, row := range rows {
		buckets[row.Value] = row.CallCount
	}
	if buckets["internal"] != 1 || buckets["external"] != 1 {
		t.Fatalf("participant_affiliation buckets=%v want internal=1 external=1", buckets)
	}

	domainRows, err := store.SummarizeBusinessAnalysisDimension(ctx, BusinessAnalysisDimensionSummaryParams{
		Filter:                       filter,
		Dimension:                    "participant_domain",
		Limit:                        10,
		InternalDomains:              []string{"tradecentric.com"},
		ParticipantAffiliationFilter: ParticipantAffiliationExternal,
	})
	if err != nil {
		t.Fatalf("SummarizeBusinessAnalysisDimension participant_domain: %v", err)
	}
	if len(domainRows) != 1 || domainRows[0].Value != "acme.example" {
		t.Fatalf("external participant_domain rows=%+v want acme.example", domainRows)
	}
}
