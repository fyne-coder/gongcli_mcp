package postgres

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
)

func TestStoreSyntheticVerticalSlice(t *testing.T) {
	databaseURL := os.Getenv("GONGCTL_TEST_POSTGRES_URL")
	if databaseURL == "" {
		t.Skip("GONGCTL_TEST_POSTGRES_URL is not set")
	}

	ctx := context.Background()
	store, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()
	resetPostgresTestStore(t, ctx, store)

	run, err := store.StartSyncRun(ctx, sqlite.StartSyncRunParams{Scope: "synthetic", SyncKey: "synthetic:test"})
	if err != nil {
		t.Fatalf("StartSyncRun returned error: %v", err)
	}
	if _, err := store.UpsertCall(ctx, json.RawMessage(`{"id":"pg-call-001","title":"Postgres shared MCP kickoff","started":"2026-01-15T15:00:00Z","duration":1200,"parties":[{"id":"speaker-1"}]}`)); err != nil {
		t.Fatalf("UpsertCall returned error: %v", err)
	}
	enrichedCall, err := store.UpsertCall(ctx, json.RawMessage(`{"id":"pg-call-002","title":"Preserve enriched call","started":"2026-01-15T18:00:00Z","duration":600,"parties":[{"id":"speaker-1"},{"id":"speaker-2"}],"context":{"crmObjects":[{"type":"account","id":"acct-1"}]}}`))
	if err != nil {
		t.Fatalf("UpsertCall enriched returned error: %v", err)
	}
	if !enrichedCall.ContextPresent || enrichedCall.PartiesCount != 2 {
		t.Fatalf("enriched call did not capture context/parties: %+v", enrichedCall)
	}
	downgradedCall, err := store.UpsertCall(ctx, json.RawMessage(`{"id":"pg-call-002","title":"Preserve enriched call","started":"2026-01-15T18:00:00Z","duration":600}`))
	if err != nil {
		t.Fatalf("UpsertCall minimal returned error: %v", err)
	}
	if !downgradedCall.ContextPresent || downgradedCall.PartiesCount != 2 {
		t.Fatalf("minimal update downgraded enriched call: %+v", downgradedCall)
	}
	if !jsonContainsKey(downgradedCall.RawJSON, "context") {
		t.Fatalf("minimal update replaced enriched raw JSON: %s", string(downgradedCall.RawJSON))
	}
	if _, err := store.UpsertUser(ctx, json.RawMessage(`{"id":"pg-user-001","emailAddress":"operator@example.invalid","firstName":"Op","lastName":"Erator","title":"RevOps Lead","active":true}`)); err != nil {
		t.Fatalf("UpsertUser returned error: %v", err)
	}
	if _, err := store.UpsertTranscript(ctx, json.RawMessage(`{"callId":"pg-call-001","transcript":[{"speakerId":"speaker-1","sentences":[{"start":0,"end":3000,"text":"Postgres gives the sync job and MCP server one shared database."}]}]}`)); err != nil {
		t.Fatalf("UpsertTranscript returned error: %v", err)
	}
	if err := store.FinishSyncRun(ctx, run.ID, sqlite.FinishSyncRunParams{Status: "success", RecordsSeen: 3, RecordsWritten: 3}); err != nil {
		t.Fatalf("FinishSyncRun returned error: %v", err)
	}

	summary, err := store.SyncStatusSummary(ctx)
	if err != nil {
		t.Fatalf("SyncStatusSummary returned error: %v", err)
	}
	if summary.TotalCalls != 2 || summary.TotalUsers != 1 || summary.TotalTranscripts != 1 || summary.TotalTranscriptSegments != 1 || summary.MissingTranscripts != 1 {
		t.Fatalf("unexpected summary: %+v", summary)
	}

	calls, err := store.SearchCallsRaw(ctx, sqlite.CallSearchParams{Limit: 10})
	if err != nil {
		t.Fatalf("SearchCallsRaw returned error: %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("SearchCallsRaw returned %d calls, want 2", len(calls))
	}
	callsOnDay, err := store.SearchCallsRaw(ctx, sqlite.CallSearchParams{ToDate: "2026-01-15", Limit: 10})
	if err != nil {
		t.Fatalf("SearchCallsRaw with inclusive to_date returned error: %v", err)
	}
	if len(callsOnDay) != 2 {
		t.Fatalf("SearchCallsRaw with inclusive to_date returned %d calls, want 2", len(callsOnDay))
	}

	segments, err := store.SearchTranscriptSegments(ctx, "shared database", 10)
	if err != nil {
		t.Fatalf("SearchTranscriptSegments returned error: %v", err)
	}
	if len(segments) != 1 || segments[0].CallID != "pg-call-001" || segments[0].Snippet == "" {
		t.Fatalf("unexpected transcript search results: %+v", segments)
	}

	readOnly, err := OpenReadOnly(ctx, databaseURL)
	if err != nil {
		t.Fatalf("OpenReadOnly returned error: %v", err)
	}
	defer readOnly.Close()
	if _, err := readOnly.SyncStatusSummary(ctx); err != nil {
		t.Fatalf("read-only SyncStatusSummary returned error: %v", err)
	}
	if _, err := readOnly.StartSyncRun(ctx, sqlite.StartSyncRunParams{Scope: "should-fail", SyncKey: "readonly"}); err == nil {
		t.Fatal("read-only StartSyncRun unexpectedly succeeded")
	}
	if _, err := readOnly.UpsertCall(ctx, json.RawMessage(`{"id":"should-fail"}`)); err == nil {
		t.Fatal("read-only UpsertCall unexpectedly succeeded")
	}
}

func TestBusinessPilotAggregatesMatchSQLite(t *testing.T) {
	databaseURL := os.Getenv("GONGCTL_TEST_POSTGRES_URL")
	if databaseURL == "" {
		t.Skip("GONGCTL_TEST_POSTGRES_URL is not set")
	}

	ctx := context.Background()
	pgStore, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer pgStore.Close()
	resetPostgresTestStore(t, ctx, pgStore)

	sqliteStore, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "gong.db"))
	if err != nil {
		t.Fatalf("sqlite.Open returned error: %v", err)
	}
	defer sqliteStore.Close()

	for _, raw := range businessPilotCallPayloads() {
		if _, err := pgStore.UpsertCall(ctx, raw); err != nil {
			t.Fatalf("postgres UpsertCall returned error: %v", err)
		}
		if _, err := sqliteStore.UpsertCall(ctx, raw); err != nil {
			t.Fatalf("sqlite UpsertCall returned error: %v", err)
		}
	}
	for _, raw := range businessPilotTranscriptPayloads() {
		if _, err := pgStore.UpsertTranscript(ctx, raw); err != nil {
			t.Fatalf("postgres UpsertTranscript returned error: %v", err)
		}
		if _, err := sqliteStore.UpsertTranscript(ctx, raw); err != nil {
			t.Fatalf("sqlite UpsertTranscript returned error: %v", err)
		}
	}

	sqliteLifecycle, err := sqliteStore.SummarizeCallsByLifecycle(ctx, sqlite.LifecycleSummaryParams{})
	if err != nil {
		t.Fatalf("sqlite SummarizeCallsByLifecycle returned error: %v", err)
	}
	postgresLifecycle, err := pgStore.SummarizeCallsByLifecycle(ctx, sqlite.LifecycleSummaryParams{})
	if err != nil {
		t.Fatalf("postgres SummarizeCallsByLifecycle returned error: %v", err)
	}
	if got, want := lifecycleSummaryByBucket(postgresLifecycle), lifecycleSummaryByBucket(sqliteLifecycle); !equalLifecycleSummaries(got, want) {
		t.Fatalf("postgres lifecycle summaries=%+v want sqlite %+v", got, want)
	}

	renewal, err := pgStore.SummarizeCallsByLifecycle(ctx, sqlite.LifecycleSummaryParams{Bucket: "renewal"})
	if err != nil {
		t.Fatalf("postgres renewal lifecycle summary returned error: %v", err)
	}
	if len(renewal) != 1 || renewal[0].CallCount != 1 || renewal[0].MissingTranscriptCount != 1 || renewal[0].HighConfidenceCalls != 1 {
		t.Fatalf("unexpected renewal summary: %+v", renewal)
	}

	for _, groupBy := range []string{"lifecycle", "transcript_status"} {
		sqliteFacts, err := sqliteStore.SummarizeCallFacts(ctx, sqlite.CallFactsSummaryParams{GroupBy: groupBy, Limit: 10})
		if err != nil {
			t.Fatalf("sqlite SummarizeCallFacts(%s) returned error: %v", groupBy, err)
		}
		postgresFacts, err := pgStore.SummarizeCallFacts(ctx, sqlite.CallFactsSummaryParams{GroupBy: groupBy, Limit: 10})
		if err != nil {
			t.Fatalf("postgres SummarizeCallFacts(%s) returned error: %v", groupBy, err)
		}
		if got, want := factSummaryByValue(postgresFacts), factSummaryByValue(sqliteFacts); !equalFactSummaries(got, want) {
			t.Fatalf("postgres facts group_by=%s got %+v want sqlite %+v", groupBy, got, want)
		}
	}
}

func TestPostgresPrioritizeTranscriptsByLifecycle(t *testing.T) {
	databaseURL := os.Getenv("GONGCTL_TEST_POSTGRES_URL")
	if databaseURL == "" {
		t.Skip("GONGCTL_TEST_POSTGRES_URL is not set")
	}

	ctx := context.Background()
	store, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()
	resetPostgresTestStore(t, ctx, store)
	for _, raw := range businessPilotCallPayloads() {
		if _, err := store.UpsertCall(ctx, raw); err != nil {
			t.Fatalf("UpsertCall returned error: %v", err)
		}
	}
	for _, raw := range businessPilotTranscriptPayloads() {
		if _, err := store.UpsertTranscript(ctx, raw); err != nil {
			t.Fatalf("UpsertTranscript returned error: %v", err)
		}
	}

	rows, err := store.PrioritizeTranscriptsByLifecycle(ctx, sqlite.LifecycleTranscriptPriorityParams{Limit: 10})
	if err != nil {
		t.Fatalf("PrioritizeTranscriptsByLifecycle returned error: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("priority rows=%d want 3: %+v", len(rows), rows)
	}
	if rows[0].CallID != "bp-renewal-missing" || rows[0].Bucket != "renewal" || rows[0].Confidence != "high" {
		t.Fatalf("unexpected top priority row: %+v", rows[0])
	}
	if rows[0].PriorityScore <= rows[1].PriorityScore {
		t.Fatalf("top priority score=%d should exceed second=%d rows=%+v", rows[0].PriorityScore, rows[1].PriorityScore, rows)
	}

	filtered, err := store.PrioritizeTranscriptsByLifecycle(ctx, sqlite.LifecycleTranscriptPriorityParams{
		Bucket:    "renewal",
		Scope:     "External",
		Direction: "Conference",
		FromDate:  "2026-02-01",
		ToDate:    "2026-02-28",
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("filtered PrioritizeTranscriptsByLifecycle returned error: %v", err)
	}
	if len(filtered) != 1 || filtered[0].CallID != "bp-renewal-missing" {
		t.Fatalf("unexpected filtered rows: %+v", filtered)
	}
	upsellRows, err := store.PrioritizeTranscriptsByLifecycle(ctx, sqlite.LifecycleTranscriptPriorityParams{
		Bucket: "upsell_expansion",
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("upsell PrioritizeTranscriptsByLifecycle returned error: %v", err)
	}
	if len(upsellRows) != 1 || upsellRows[0].CallID != "bp-upsell-missing" {
		t.Fatalf("unexpected upsell rows: %+v", upsellRows)
	}
	if got, want := upsellRows[0].EvidenceFields, []string{"Opportunity.Expansion_Bookings__c", "Opportunity.One_Year_Upsell__c"}; !stringSlicesEqual(got, want) {
		t.Fatalf("upsell evidence fields=%+v want %+v", got, want)
	}
	if _, err := store.PrioritizeTranscriptsByLifecycle(ctx, sqlite.LifecycleTranscriptPriorityParams{FromDate: "2026-02-99"}); err == nil {
		t.Fatal("bad date unexpectedly succeeded")
	}
	if _, err := store.PrioritizeTranscriptsByLifecycle(ctx, sqlite.LifecycleTranscriptPriorityParams{Bucket: "bogus"}); err == nil {
		t.Fatal("bad bucket unexpectedly succeeded")
	}
	if _, err := store.PrioritizeTranscriptsByLifecycle(ctx, sqlite.LifecycleTranscriptPriorityParams{FromDate: "2026-03-01", ToDate: "2026-02-01"}); err == nil {
		t.Fatal("inverted date range unexpectedly succeeded")
	}
}

func resetPostgresTestStore(t *testing.T, ctx context.Context, store *Store) {
	t.Helper()
	_, err := store.DB().ExecContext(ctx, `TRUNCATE transcript_segments, transcripts, calls, users, sync_state, sync_runs RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatalf("reset postgres test store: %v", err)
	}
}

func jsonContainsKey(raw json.RawMessage, key string) bool {
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return false
	}
	_, ok := doc[key]
	return ok
}

func businessPilotCallPayloads() []json.RawMessage {
	return []json.RawMessage{
		json.RawMessage(`{"id":"bp-late-present","title":"Late present call","started":"2026-02-10T15:00:00Z","duration":1800,"metaData":{"scope":"External","system":"Zoom","direction":"Conference"},"context":[{"objectType":"Opportunity","id":"opp-late","name":"Late Opportunity","fields":[{"name":"StageName","value":"Contract Review"},{"name":"Type","value":"New Business"}]},{"objectType":"Account","id":"acct-late","name":"Late Account","fields":[{"name":"Account_Type__c","value":"Prospect"},{"name":"Industry","value":"Manufacturing"}]}]}`),
		json.RawMessage(`{"id":"bp-renewal-missing","title":"Renewal missing call","started":"2026-02-12T15:00:00Z","duration":2400,"metaData":{"scope":"External","system":"Zoom","direction":"Conference"},"context":{"crmObjects":[{"type":"Opportunity","id":"opp-renewal","name":"Renewal Opportunity","fields":{"StageName":"Discovery & Demo (SQO)","Type":"Renewal"}},{"type":"Account","id":"acct-renewal","name":"Renewal Account","fields":{"Account_Type__c":"Customer - Active","Industry":"Healthcare"}}]}}`),
		json.RawMessage(`{"id":"bp-upsell-missing","title":"Upsell missing call","started":"2026-02-13T15:00:00Z","duration":1200,"metaData":{"scope":"External","system":"Zoom","direction":"Conference"},"crmObjects":[{"type":"Opportunity","id":"opp-upsell","name":"Upsell Opportunity","fields":{"StageName":"Demo & Business Case","Expansion_Bookings__c":"24000","One_Year_Upsell__c":"12000"}}]}`),
		json.RawMessage(`{"id":"bp-outbound-missing","title":"Outbound missing call","started":"2026-02-14T15:00:00Z","duration":45,"metaData":{"system":"Upload API","direction":"Outbound"}}`),
	}
}

func businessPilotTranscriptPayloads() []json.RawMessage {
	return []json.RawMessage{
		json.RawMessage(`{"callId":"bp-late-present","transcript":[{"speakerId":"speaker-1","sentences":[{"start":0,"end":3000,"text":"The contract review depends on security approval."}]}]}`),
	}
}

func lifecycleSummaryByBucket(rows []sqlite.LifecycleBucketSummary) map[string]sqlite.LifecycleBucketSummary {
	out := make(map[string]sqlite.LifecycleBucketSummary, len(rows))
	for _, row := range rows {
		if row.Bucket == "" {
			continue
		}
		row.LatestCallID = ""
		out[row.Bucket] = row
	}
	return out
}

func factSummaryByValue(rows []sqlite.CallFactsSummaryRow) map[string]sqlite.CallFactsSummaryRow {
	out := make(map[string]sqlite.CallFactsSummaryRow, len(rows))
	for _, row := range rows {
		out[row.GroupValue] = row
	}
	return out
}

func equalLifecycleSummaries(got map[string]sqlite.LifecycleBucketSummary, want map[string]sqlite.LifecycleBucketSummary) bool {
	keys := []string{"late_stage_sales", "renewal", "upsell_expansion", "outbound_prospecting"}
	for _, key := range keys {
		left, leftOK := got[key]
		right, rightOK := want[key]
		if !leftOK || !rightOK {
			return false
		}
		if left.CallCount != right.CallCount || left.TranscriptCount != right.TranscriptCount || left.MissingTranscriptCount != right.MissingTranscriptCount || left.OpportunityCallCount != right.OpportunityCallCount || left.AccountCallCount != right.AccountCallCount || left.TotalDurationSeconds != right.TotalDurationSeconds || left.LatestCallAt != right.LatestCallAt || left.HighConfidenceCalls != right.HighConfidenceCalls || left.MediumConfidenceCalls != right.MediumConfidenceCalls || left.LowConfidenceCalls != right.LowConfidenceCalls {
			return false
		}
	}
	return true
}

func equalFactSummaries(got map[string]sqlite.CallFactsSummaryRow, want map[string]sqlite.CallFactsSummaryRow) bool {
	if len(got) != len(want) {
		return false
	}
	keys := make([]string, 0, len(want))
	for key := range want {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		left, leftOK := got[key]
		right := want[key]
		if !leftOK {
			return false
		}
		if left.GroupBy != right.GroupBy || left.CallCount != right.CallCount || left.TranscriptCount != right.TranscriptCount || left.MissingTranscriptCount != right.MissingTranscriptCount || left.OpportunityCallCount != right.OpportunityCallCount || left.AccountCallCount != right.AccountCallCount || left.ExternalCallCount != right.ExternalCallCount || left.InternalCallCount != right.InternalCallCount || left.UnknownScopeCallCount != right.UnknownScopeCallCount || left.TotalDurationSeconds != right.TotalDurationSeconds || left.AvgDurationSeconds != right.AvgDurationSeconds || left.LatestCallAt != right.LatestCallAt || left.TranscriptCoverageRate != right.TranscriptCoverageRate {
			return false
		}
	}
	return true
}

func stringSlicesEqual(got []string, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
