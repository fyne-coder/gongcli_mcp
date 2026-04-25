package transcripts

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/arthurlee/gongctl/internal/auth"
	"github.com/arthurlee/gongctl/internal/gong"
	"github.com/arthurlee/gongctl/internal/store/sqlite"
)

func TestSyncMissingTranscriptsSelectsOnlyMissingCalls(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	mustUpsertCall(t, ctx, store, map[string]any{
		"id":      "call-existing",
		"title":   "Existing transcript",
		"started": "2026-04-24T09:00:00Z",
	})
	mustUpsertCall(t, ctx, store, map[string]any{
		"id":      "call-missing-2",
		"title":   "Missing transcript 2",
		"started": "2026-04-24T10:00:00Z",
	})
	mustUpsertCall(t, ctx, store, map[string]any{
		"id":      "call-missing-1",
		"title":   "Missing transcript 1",
		"started": "2026-04-24T11:00:00Z",
	})
	mustUpsertTranscript(t, ctx, store, wrappedTranscriptPayload("call-existing", "speaker-a", "Already synced."))

	client, requested := newFakeTranscriptClient(t, map[string]string{
		"call-missing-1": wrappedTranscriptPayload("call-missing-1", "speaker-b", "First missing transcript."),
		"call-missing-2": wrappedTranscriptPayload("call-missing-2", "speaker-c", "Second missing transcript."),
	})

	result, err := SyncMissingTranscripts(ctx, client, store, SyncMissingParams{})
	if err != nil {
		t.Fatalf("SyncMissingTranscripts returned error: %v", err)
	}
	if result.CallsSeen != 2 {
		t.Fatalf("CallsSeen=%d want 2", result.CallsSeen)
	}
	if result.TranscriptsSynced != 2 {
		t.Fatalf("TranscriptsSynced=%d want 2", result.TranscriptsSynced)
	}

	gotIDs := requested()
	slices.Sort(gotIDs)
	wantIDs := []string{"call-missing-1", "call-missing-2"}
	if !slices.Equal(gotIDs, wantIDs) {
		t.Fatalf("requested call ids=%v want %v", gotIDs, wantIDs)
	}

	summary, err := store.SyncStatusSummary(ctx)
	if err != nil {
		t.Fatalf("SyncStatusSummary returned error: %v", err)
	}
	if summary.MissingTranscripts != 0 {
		t.Fatalf("MissingTranscripts=%d want 0", summary.MissingTranscripts)
	}
	if summary.LastRun == nil {
		t.Fatal("LastRun is nil")
	}
	if summary.LastRun.Scope != "transcripts" || summary.LastRun.Status != "success" {
		t.Fatalf("LastRun=%+v want scope=transcripts status=success", summary.LastRun)
	}
	if summary.LastRun.RecordsSeen != 2 || summary.LastRun.RecordsWritten != 2 {
		t.Fatalf("LastRun counts=%d/%d want 2/2", summary.LastRun.RecordsSeen, summary.LastRun.RecordsWritten)
	}
}

func TestSyncMissingTranscriptsNormalizesJSONAndSearchesFTS(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	mustUpsertCall(t, ctx, store, map[string]any{
		"id":      "call-normalized",
		"title":   "Normalization test",
		"started": "2026-04-24T11:15:00Z",
	})

	rawPayload := "{\n  \"callTranscripts\": [\n    {\n      \"transcript\": [\n        {\n          \"sentences\": [\n            {\"text\": \"Budget discussion with external team.\", \"end\": 2000, \"start\": 1000}\n          ],\n          \"speakerId\": \"speaker-ext\"\n        }\n      ],\n      \"callId\": \"call-normalized\"\n    }\n  ]\n}\n"
	client, _ := newFakeTranscriptClient(t, map[string]string{
		"call-normalized": rawPayload,
	})

	if _, err := SyncMissingTranscripts(ctx, client, store, SyncMissingParams{}); err != nil {
		t.Fatalf("SyncMissingTranscripts returned error: %v", err)
	}

	var rawStored string
	if err := store.DB().QueryRowContext(ctx, `SELECT raw_json FROM transcripts WHERE call_id = ?`, "call-normalized").Scan(&rawStored); err != nil {
		t.Fatalf("query raw_json: %v", err)
	}
	wantNormalized := compactJSON(t, map[string]any{
		"callId": "call-normalized",
		"transcript": []any{
			map[string]any{
				"speakerId": "speaker-ext",
				"sentences": []any{
					map[string]any{
						"end":   float64(2000),
						"start": float64(1000),
						"text":  "Budget discussion with external team.",
					},
				},
			},
		},
	})
	if rawStored != wantNormalized {
		t.Fatalf("raw_json=%s want %s", rawStored, wantNormalized)
	}

	results, err := SearchTranscripts(ctx, store, "external", 10)
	if err != nil {
		t.Fatalf("SearchTranscripts returned error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results)=%d want 1", len(results))
	}
	if results[0].CallID != "call-normalized" {
		t.Fatalf("CallID=%q want call-normalized", results[0].CallID)
	}
	if !strings.Contains(strings.ToLower(results[0].Snippet), "[external]") {
		t.Fatalf("Snippet=%q missing highlighted term", results[0].Snippet)
	}
}

func TestSearchTranscriptsReturnsBoundedSnippetsOnly(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	mustUpsertCall(t, ctx, store, map[string]any{
		"id":      "call-snippet",
		"title":   "Snippet safety",
		"started": "2026-04-24T12:00:00Z",
	})

	longText := "alpha " + strings.Repeat("verylongsegment ", 80) + "tail-marker-should-not-leak"
	mustUpsertTranscript(t, ctx, store, directTranscriptPayload("call-snippet", "speaker-long", longText))

	results, err := SearchTranscripts(ctx, store, "alpha", 10)
	if err != nil {
		t.Fatalf("SearchTranscripts returned error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results)=%d want 1", len(results))
	}
	if len(results[0].Snippet) > maxSnippetChars {
		t.Fatalf("Snippet length=%d want <= %d", len(results[0].Snippet), maxSnippetChars)
	}
	if strings.Contains(results[0].Snippet, "tail-marker-should-not-leak") {
		t.Fatalf("Snippet leaked full segment tail: %q", results[0].Snippet)
	}

	body, err := json.Marshal(results[0])
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	if strings.Contains(string(body), `"text"`) {
		t.Fatalf("search result leaked raw text field: %s", body)
	}
	if strings.Contains(string(body), longText) {
		t.Fatalf("search result leaked full transcript text: %s", body)
	}
}

func TestSyncMissingTranscriptsWritesAtomicJSONFiles(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	mustUpsertCall(t, ctx, store, map[string]any{
		"id":      "call/file:one",
		"title":   "Atomic write",
		"started": "2026-04-24T12:30:00Z",
	})

	outDir := filepath.Join(t.TempDir(), "transcripts")
	finalPath := filepath.Join(outDir, "call_file_one.json")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.WriteFile(finalPath, []byte(`{"broken"`), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	client, _ := newFakeTranscriptClient(t, map[string]string{
		"call/file:one": wrappedTranscriptPayload("call/file:one", "speaker-file", "Atomic file output."),
	})

	result, err := SyncMissingTranscripts(ctx, client, store, SyncMissingParams{OutDir: outDir})
	if err != nil {
		t.Fatalf("SyncMissingTranscripts returned error: %v", err)
	}
	if result.FilesWritten != 1 {
		t.Fatalf("FilesWritten=%d want 1", result.FilesWritten)
	}

	body, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if !json.Valid(body) {
		t.Fatalf("file %s is not valid JSON: %s", finalPath, body)
	}
	if strings.Contains(string(body), "callTranscripts") {
		t.Fatalf("file %s did not contain normalized transcript JSON: %s", finalPath, body)
	}

	matches, err := filepath.Glob(filepath.Join(outDir, "*.tmp-*"))
	if err != nil {
		t.Fatalf("Glob returned error: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary files left behind: %v", matches)
	}
}

func TestSyncMissingDoesNotWriteFileWhenStoreRejectsTranscript(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	mustUpsertCall(t, ctx, store, map[string]any{
		"id":      "call-invalid-schema",
		"title":   "Invalid transcript schema",
		"started": "2026-04-24T13:00:00Z",
	})

	outDir := filepath.Join(t.TempDir(), "transcripts")
	client, _ := newFakeTranscriptClient(t, map[string]string{
		"call-invalid-schema": `{"callId":"call-invalid-schema","notTranscript":[]}`,
	})

	result, err := SyncMissing(ctx, client, store, outDir, 1)
	if err == nil {
		t.Fatal("SyncMissing returned nil error, want schema failure")
	}
	if result.Downloaded != 1 || result.Stored != 0 || result.Failed != 1 {
		t.Fatalf("result=%+v want downloaded=1 stored=0 failed=1", result)
	}

	matches, globErr := filepath.Glob(filepath.Join(outDir, "*.json"))
	if globErr != nil {
		t.Fatalf("Glob returned error: %v", globErr)
	}
	if len(matches) != 0 {
		t.Fatalf("transcript files written despite store rejection: %v", matches)
	}
}

func openTestStore(t *testing.T) *sqlite.Store {
	t.Helper()

	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "gongctl.db"))
	if err != nil {
		t.Fatalf("sqlite.Open returned error: %v", err)
	}
	return store
}

func mustUpsertCall(t *testing.T, ctx context.Context, store *sqlite.Store, payload map[string]any) {
	t.Helper()

	if _, err := store.UpsertCall(ctx, compactJSONBytes(t, payload)); err != nil {
		t.Fatalf("UpsertCall returned error: %v", err)
	}
}

func mustUpsertTranscript(t *testing.T, ctx context.Context, store *sqlite.Store, raw string) {
	t.Helper()

	if _, err := store.UpsertTranscript(ctx, json.RawMessage(raw)); err != nil {
		t.Fatalf("UpsertTranscript returned error: %v", err)
	}
}

func compactJSON(t *testing.T, value any) string {
	t.Helper()
	return string(compactJSONBytes(t, value))
}

func compactJSONBytes(t *testing.T, value any) []byte {
	t.Helper()

	body, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}
	return body
}

func wrappedTranscriptPayload(callID string, speakerID string, text string) string {
	return directTranscriptEnvelope(callID, speakerID, text, true)
}

func directTranscriptPayload(callID string, speakerID string, text string) string {
	return directTranscriptEnvelope(callID, speakerID, text, false)
}

func directTranscriptEnvelope(callID string, speakerID string, text string, wrapped bool) string {
	record := map[string]any{
		"callId": callID,
		"transcript": []any{
			map[string]any{
				"speakerId": speakerID,
				"sentences": []any{
					map[string]any{
						"start": 1000,
						"end":   2500,
						"text":  text,
					},
				},
			},
		},
	}
	if !wrapped {
		body, _ := json.Marshal(record)
		return string(body)
	}
	body, _ := json.Marshal(map[string]any{"callTranscripts": []any{record}})
	return string(body)
}

func newFakeTranscriptClient(t *testing.T, responses map[string]string) (*gong.Client, func() []string) {
	t.Helper()

	var (
		mu        sync.Mutex
		requested []string
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method=%s want POST", r.Method)
		}
		if r.URL.Path != "/v2/calls/transcript" {
			t.Fatalf("path=%s want /v2/calls/transcript", r.URL.Path)
		}

		var body struct {
			Filter struct {
				CallIDs []string `json:"callIds"`
			} `json:"filter"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("Decode returned error: %v", err)
		}
		if len(body.Filter.CallIDs) != 1 {
			t.Fatalf("callIds=%v want exactly one id", body.Filter.CallIDs)
		}
		callID := body.Filter.CallIDs[0]

		mu.Lock()
		requested = append(requested, callID)
		mu.Unlock()

		payload, ok := responses[callID]
		if !ok {
			http.Error(w, `{"error":"missing fixture"}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(payload))
	}))
	t.Cleanup(server.Close)

	client, err := gong.NewClient(gong.Options{
		BaseURL: server.URL,
		Credentials: auth.Credentials{
			AccessKey:       "key",
			AccessKeySecret: "secret",
		},
		MaxRetries: 0,
	})
	if err != nil {
		t.Fatalf("gong.NewClient returned error: %v", err)
	}

	return client, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), requested...)
	}
}
