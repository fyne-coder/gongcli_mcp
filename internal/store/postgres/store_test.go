package postgres

import (
	"context"
	"encoding/json"
	"os"
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
