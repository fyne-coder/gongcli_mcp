package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
)

func TestCacheInventoryReportsSummaryAndWarnings(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gong.db")
	store := openCLITestStore(t, dbPath)

	ctx := context.Background()
	if _, err := store.UpsertCall(ctx, mustMarshalJSON(t, map[string]any{
		"id":       "call-cache-001",
		"title":    "Cache inventory call 1",
		"started":  "2026-04-20T14:00:00Z",
		"duration": 1800,
		"metaData": map[string]any{
			"system":    "Zoom",
			"direction": "Conference",
			"scope":     "External",
		},
		"context": map[string]any{
			"crmObjects": []map[string]any{
				{
					"type": "Opportunity",
					"id":   "opp-cache-001",
					"name": "Cache opportunity",
					"fields": []map[string]any{
						{"name": "StageName", "label": "Stage", "value": "Discovery"},
					},
				},
			},
		},
	})); err != nil {
		t.Fatalf("UpsertCall(first) returned error: %v", err)
	}
	if _, err := store.UpsertCall(ctx, mustMarshalJSON(t, map[string]any{
		"id":       "call-cache-002",
		"title":    "Cache inventory call 2",
		"started":  "2026-04-24T14:00:00Z",
		"duration": 1200,
		"metaData": map[string]any{
			"system":    "Google Meet",
			"direction": "Conference",
			"scope":     "External",
		},
	})); err != nil {
		t.Fatalf("UpsertCall(second) returned error: %v", err)
	}
	if _, err := store.UpsertUser(ctx, mustMarshalJSON(t, map[string]any{
		"id":           "user-cache-001",
		"emailAddress": "owner@example.invalid",
		"firstName":    "Casey",
		"lastName":     "Owner",
		"name":         "Casey Owner",
		"active":       true,
	})); err != nil {
		t.Fatalf("UpsertUser returned error: %v", err)
	}
	if _, err := store.UpsertTranscript(ctx, mustMarshalJSON(t, map[string]any{
		"callId": "call-cache-001",
		"transcript": []any{
			map[string]any{
				"speakerId": "spk-1",
				"sentences": []map[string]any{
					{"start": 0, "end": 1000, "text": "Cached transcript sentence."},
				},
			},
		},
	})); err != nil {
		t.Fatalf("UpsertTranscript returned error: %v", err)
	}

	run, err := store.StartSyncRun(ctx, sqlite.StartSyncRunParams{
		Scope:   "calls",
		SyncKey: "calls:preset=minimal:from=2026-04-20:to=2026-04-24",
		From:    "2026-04-20",
		To:      "2026-04-24",
	})
	if err != nil {
		t.Fatalf("StartSyncRun returned error: %v", err)
	}
	if err := store.FinishSyncRun(ctx, run.ID, sqlite.FinishSyncRunParams{
		Status:         "success",
		RecordsSeen:    2,
		RecordsWritten: 2,
	}); err != nil {
		t.Fatalf("FinishSyncRun returned error: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"cache", "inventory", "--db", dbPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run(cache inventory) code=%d stderr=%q", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr=%q want empty", stderr.String())
	}

	var resp struct {
		DBPath              string `json:"db_path"`
		DBSizeBytes         int64  `json:"db_size_bytes"`
		OldestCallStartedAt string `json:"oldest_call_started_at"`
		NewestCallStartedAt string `json:"newest_call_started_at"`
		Summary             struct {
			TotalCalls         int64 `json:"total_calls"`
			TotalUsers         int64 `json:"total_users"`
			TotalTranscripts   int64 `json:"total_transcripts"`
			MissingTranscripts int64 `json:"missing_transcripts"`
		} `json:"summary"`
		TranscriptPresence struct {
			HasTranscripts     bool  `json:"has_transcripts"`
			CachedTranscripts  int64 `json:"cached_transcripts"`
			MissingTranscripts int64 `json:"missing_transcripts"`
		} `json:"transcript_presence"`
		CRMContextPresence struct {
			HasEmbeddedContext bool  `json:"has_embedded_context"`
			CallsWithContext   int64 `json:"calls_with_context"`
			Fields             int64 `json:"fields"`
		} `json:"crm_context_presence"`
		ProfileStatus struct {
			Status string `json:"status"`
		} `json:"profile_status"`
		TableCounts []struct {
			Table string `json:"table"`
			Rows  int64  `json:"rows"`
		} `json:"table_counts"`
		SensitiveDataWarning string `json:"sensitive_data_warning"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}

	if resp.DBPath == "" || resp.DBSizeBytes <= 0 {
		t.Fatalf("unexpected db metadata: %+v", resp)
	}
	if resp.OldestCallStartedAt != "2026-04-20T14:00:00Z" || resp.NewestCallStartedAt != "2026-04-24T14:00:00Z" {
		t.Fatalf("unexpected call date range: %+v", resp)
	}
	if resp.Summary.TotalCalls != 2 || resp.Summary.TotalUsers != 1 || resp.Summary.TotalTranscripts != 1 || resp.Summary.MissingTranscripts != 1 {
		t.Fatalf("unexpected summary: %+v", resp.Summary)
	}
	if !resp.TranscriptPresence.HasTranscripts || resp.TranscriptPresence.CachedTranscripts != 1 || resp.TranscriptPresence.MissingTranscripts != 1 {
		t.Fatalf("unexpected transcript presence: %+v", resp.TranscriptPresence)
	}
	if !resp.CRMContextPresence.HasEmbeddedContext || resp.CRMContextPresence.CallsWithContext != 1 || resp.CRMContextPresence.Fields != 1 {
		t.Fatalf("unexpected CRM context presence: %+v", resp.CRMContextPresence)
	}
	if resp.ProfileStatus.Status != "not_configured" {
		t.Fatalf("profile status=%q want not_configured", resp.ProfileStatus.Status)
	}
	if !strings.Contains(resp.SensitiveDataWarning, "Treat") {
		t.Fatalf("warning=%q missing handling guidance", resp.SensitiveDataWarning)
	}

	counts := map[string]int64{}
	for _, table := range resp.TableCounts {
		counts[table.Table] = table.Rows
	}
	if counts["calls"] != 2 || counts["transcripts"] != 1 || counts["sync_runs"] != 1 {
		t.Fatalf("unexpected table counts: %+v", counts)
	}
}

func TestCachePurgeDryRunRequiresConfirmation(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gong.db")
	store := openCLITestStore(t, dbPath)

	ctx := context.Background()
	if _, err := store.UpsertCall(ctx, mustMarshalJSON(t, map[string]any{
		"id":       "call-purge-old",
		"title":    "Old purge call",
		"started":  "2026-04-20T14:00:00Z",
		"duration": 1800,
		"metaData": map[string]any{
			"system":    "Zoom",
			"direction": "Conference",
			"scope":     "External",
		},
		"context": map[string]any{
			"crmObjects": []map[string]any{
				{
					"type": "Opportunity",
					"id":   "opp-purge-old",
					"name": "Old opportunity",
					"fields": []map[string]any{
						{"name": "StageName", "label": "Stage", "value": "Discovery"},
					},
				},
			},
		},
	})); err != nil {
		t.Fatalf("UpsertCall(old) returned error: %v", err)
	}
	if _, err := store.UpsertCall(ctx, mustMarshalJSON(t, map[string]any{
		"id":       "call-purge-new",
		"title":    "New purge call",
		"started":  "2026-04-24T14:00:00Z",
		"duration": 1200,
		"metaData": map[string]any{
			"system":    "Google Meet",
			"direction": "Conference",
			"scope":     "External",
		},
	})); err != nil {
		t.Fatalf("UpsertCall(new) returned error: %v", err)
	}
	if _, err := store.UpsertTranscript(ctx, mustMarshalJSON(t, map[string]any{
		"callId": "call-purge-old",
		"transcript": []any{
			map[string]any{
				"speakerId": "spk-1",
				"sentences": []map[string]any{
					{"start": 0, "end": 1000, "text": "Old purge transcript."},
				},
			},
		},
	})); err != nil {
		t.Fatalf("UpsertTranscript returned error: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"cache", "purge", "--db", dbPath, "--older-than", "2026-04-22"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run(cache purge dry-run) code=%d stderr=%q", code, stderr.String())
	}
	var dryRunResp struct {
		DryRun               bool `json:"dry_run"`
		Executed             bool `json:"executed"`
		ConfirmationRequired bool `json:"confirmation_required"`
		Plan                 struct {
			CallCount              int64 `json:"call_count"`
			TranscriptCount        int64 `json:"transcript_count"`
			TranscriptSegmentCount int64 `json:"transcript_segment_count"`
			ContextObjectCount     int64 `json:"context_object_count"`
			ContextFieldCount      int64 `json:"context_field_count"`
		} `json:"plan"`
		ConfirmationInstructions string `json:"confirmation_instructions"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &dryRunResp); err != nil {
		t.Fatalf("json.Unmarshal dry-run returned error: %v", err)
	}
	if !dryRunResp.DryRun || dryRunResp.Executed || !dryRunResp.ConfirmationRequired {
		t.Fatalf("unexpected dry-run flags: %+v", dryRunResp)
	}
	if dryRunResp.Plan.CallCount != 1 || dryRunResp.Plan.TranscriptCount != 1 || dryRunResp.Plan.TranscriptSegmentCount != 1 || dryRunResp.Plan.ContextObjectCount != 1 || dryRunResp.Plan.ContextFieldCount != 1 {
		t.Fatalf("unexpected dry-run plan: %+v", dryRunResp.Plan)
	}
	if !strings.Contains(dryRunResp.ConfirmationInstructions, "--confirm") {
		t.Fatalf("confirmation instructions=%q missing --confirm", dryRunResp.ConfirmationInstructions)
	}

	readOnly, err := sqlite.OpenReadOnly(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("OpenReadOnly returned error: %v", err)
	}
	inventory, err := readOnly.CacheInventory(context.Background())
	if err != nil {
		t.Fatalf("CacheInventory after dry-run returned error: %v", err)
	}
	if inventory.Summary.TotalCalls != 2 || inventory.Summary.TotalTranscripts != 1 {
		t.Fatalf("dry-run mutated cache: %+v", inventory.Summary)
	}
	if err := readOnly.Close(); err != nil {
		t.Fatalf("Close read-only returned error: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run(context.Background(), []string{"cache", "purge", "--db", dbPath, "--older-than", "2026-04-22", "--confirm"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run(cache purge confirm) code=%d stderr=%q", code, stderr.String())
	}
	var confirmResp struct {
		DryRun               bool `json:"dry_run"`
		Executed             bool `json:"executed"`
		ConfirmationRequired bool `json:"confirmation_required"`
		Plan                 struct {
			CallCount int64 `json:"call_count"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &confirmResp); err != nil {
		t.Fatalf("json.Unmarshal confirm returned error: %v", err)
	}
	if confirmResp.DryRun || !confirmResp.Executed || confirmResp.ConfirmationRequired || confirmResp.Plan.CallCount != 1 {
		t.Fatalf("unexpected confirm response: %+v", confirmResp)
	}

	readOnly, err = sqlite.OpenReadOnly(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("OpenReadOnly after confirm returned error: %v", err)
	}
	inventory, err = readOnly.CacheInventory(context.Background())
	if err != nil {
		t.Fatalf("CacheInventory after confirm returned error: %v", err)
	}
	if inventory.Summary.TotalCalls != 1 || inventory.Summary.TotalTranscripts != 0 || inventory.Summary.TotalEmbeddedCRMFields != 0 {
		t.Fatalf("confirm did not purge expected rows: %+v", inventory.Summary)
	}
	if inventory.OldestCallStartedAt != "2026-04-24T14:00:00Z" || inventory.NewestCallStartedAt != "2026-04-24T14:00:00Z" {
		t.Fatalf("unexpected remaining date range: oldest=%q newest=%q", inventory.OldestCallStartedAt, inventory.NewestCallStartedAt)
	}
	if err := readOnly.Close(); err != nil {
		t.Fatalf("Close read-only after confirm returned error: %v", err)
	}
}
