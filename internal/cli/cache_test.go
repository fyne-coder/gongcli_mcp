package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
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

	t.Setenv("GONG_DATABASE_URL", "postgres://gongctl:secret@127.0.0.1:1/gongctl?sslmode=disable")
	t.Setenv("DATABASE_URL", "")
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
		Backend             string `json:"backend"`
		DBPath              string `json:"db_path"`
		DBPathPolicy        string `json:"db_path_policy"`
		DBSizeBytes         int64  `json:"db_size_bytes"`
		OldestCallStartedAt string `json:"oldest_call_started_at"`
		NewestCallStartedAt string `json:"newest_call_started_at"`
		Summary             struct {
			TotalCalls          int64 `json:"total_calls"`
			TotalUsers          int64 `json:"total_users"`
			TotalTranscripts    int64 `json:"total_transcripts"`
			MissingTranscripts  int64 `json:"missing_transcripts"`
			AttributionCoverage struct {
				CallsWithTitles       int64  `json:"calls_with_titles"`
				CallsWithParties      int64  `json:"calls_with_parties"`
				UsersWithTitles       int64  `json:"users_with_titles"`
				PersonTitleStatus     string `json:"person_title_status"`
				RecommendedNextAction string `json:"recommended_next_action"`
			} `json:"attribution_coverage"`
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

	if resp.Backend != "sqlite" || resp.DBPathPolicy != "local_path_reported_for_operator" {
		t.Fatalf("unexpected backend/path policy: %+v", resp)
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
	if resp.Summary.AttributionCoverage.CallsWithTitles != 2 || resp.Summary.AttributionCoverage.CallsWithParties != 0 || resp.Summary.AttributionCoverage.UsersWithTitles != 0 {
		t.Fatalf("unexpected attribution coverage: %+v", resp.Summary.AttributionCoverage)
	}
	if resp.Summary.AttributionCoverage.PersonTitleStatus != "missing_from_cache" || resp.Summary.AttributionCoverage.RecommendedNextAction == "" {
		t.Fatalf("unexpected title readiness: %+v", resp.Summary.AttributionCoverage)
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

func TestCachePurgePolicyConfigValidatesApprovalAndConfirms(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "gong.db")
	store := openCLITestStore(t, dbPath)

	ctx := context.Background()
	if _, err := store.UpsertCall(ctx, mustMarshalJSON(t, map[string]any{
		"id":       "call-policy-purge-old",
		"title":    "Policy purge old call",
		"started":  "2026-04-20T14:00:00Z",
		"duration": 600,
		"metaData": map[string]any{
			"system":    "Zoom",
			"direction": "Conference",
			"scope":     "External",
		},
		"context": map[string]any{
			"crmObjects": []map[string]any{
				{
					"type": "Opportunity",
					"id":   "opp-policy-purge-old",
					"name": "Policy purge opportunity",
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
		"id":       "call-policy-purge-new",
		"title":    "Policy purge new call",
		"started":  "2026-04-24T14:00:00Z",
		"duration": 600,
		"metaData": map[string]any{
			"system":    "Google Meet",
			"direction": "Conference",
			"scope":     "External",
		},
	})); err != nil {
		t.Fatalf("UpsertCall(new) returned error: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	incompletePolicy := filepath.Join(dir, "retention-incomplete.yaml")
	if err := os.WriteFile(incompletePolicy, []byte(`version: 1
older_than: 2026-04-22
approval:
  reference: CHANGE-123
`), 0o600); err != nil {
		t.Fatalf("write incomplete policy: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"cache", "purge", "--db", dbPath, "--config", incompletePolicy, "--dry-run"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run(config dry-run) code=%d stderr=%q", code, stderr.String())
	}
	var dryRunResp struct {
		DryRun   bool `json:"dry_run"`
		Executed bool `json:"executed"`
		Plan     struct {
			CallCount int64 `json:"call_count"`
		} `json:"plan"`
		RetentionPolicy *cacheRetentionPolicyMetadata `json:"retention_policy"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &dryRunResp); err != nil {
		t.Fatalf("json.Unmarshal dry-run returned error: %v", err)
	}
	if !dryRunResp.DryRun || dryRunResp.Executed || dryRunResp.Plan.CallCount != 1 {
		t.Fatalf("unexpected config dry-run response: %+v body=%s", dryRunResp, stdout.String())
	}
	if dryRunResp.RetentionPolicy == nil || !dryRunResp.RetentionPolicy.Configured || dryRunResp.RetentionPolicy.PolicySHA256 == "" {
		t.Fatalf("missing retention policy metadata: %+v", dryRunResp.RetentionPolicy)
	}
	if dryRunResp.RetentionPolicy.OlderThan != "2026-04-22" || dryRunResp.RetentionPolicy.ApprovalComplete {
		t.Fatalf("unexpected retention policy metadata: %+v", dryRunResp.RetentionPolicy)
	}

	missingPolicy := filepath.Join(dir, "retention-missing.yaml")
	stdout.Reset()
	stderr.Reset()
	code = Run(context.Background(), []string{"cache", "purge", "--db", dbPath, "--config", missingPolicy, "--confirm"}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("Run(config missing file) succeeded unexpectedly: stdout=%q", stdout.String())
	}
	if strings.Contains(stderr.String(), missingPolicy) {
		t.Fatalf("stderr=%q leaked missing retention policy path", stderr.String())
	}

	futurePolicy := filepath.Join(dir, "retention-future-approval.yaml")
	if err := os.WriteFile(futurePolicy, []byte(`version: 1
older_than: 2026-04-22
approval:
  reference: CHANGE-125
  approved_by: revops-retention-reviewer
  approved_at: 2999-01-01
  data_owner: revenue-operations
  backup_reference: backup-20240101-synthetic
  legal_hold_reviewed: true
`), 0o600); err != nil {
		t.Fatalf("write future approval policy: %v", err)
	}
	stdout.Reset()
	stderr.Reset()
	code = Run(context.Background(), []string{"cache", "purge", "--db", dbPath, "--config", futurePolicy, "--dry-run"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run(config dry-run future approval) code=%d stderr=%q", code, stderr.String())
	}
	var futureResp struct {
		RetentionPolicy *cacheRetentionPolicyMetadata `json:"retention_policy"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &futureResp); err != nil {
		t.Fatalf("json.Unmarshal future dry-run returned error: %v", err)
	}
	if futureResp.RetentionPolicy == nil || futureResp.RetentionPolicy.ApprovalComplete {
		t.Fatalf("future approval date should not be complete: %+v", futureResp.RetentionPolicy)
	}
	stdout.Reset()
	stderr.Reset()
	code = Run(context.Background(), []string{"cache", "purge", "--db", dbPath, "--config", futurePolicy, "--confirm"}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("Run(config confirm future approval) succeeded unexpectedly: stdout=%q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "must not be in the future") {
		t.Fatalf("stderr=%q missing future approval validation", stderr.String())
	}

	unsafeMetadataPolicy := filepath.Join(dir, "retention-unsafe-metadata.yaml")
	if err := os.WriteFile(unsafeMetadataPolicy, []byte(`version: 1
older_than: 2026-04-22
approval:
  reference: https://changes.example.invalid/CHANGE-126
  approved_by: reviewer@example.invalid
  approved_at: 2024-01-01
  data_owner: revenue-operations
  backup_reference: /srv/backups/customer-retention.dump
  legal_hold_reviewed: true
`), 0o600); err != nil {
		t.Fatalf("write unsafe metadata policy: %v", err)
	}
	stdout.Reset()
	stderr.Reset()
	code = Run(context.Background(), []string{"cache", "purge", "--db", dbPath, "--config", unsafeMetadataPolicy, "--dry-run"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run(config dry-run unsafe metadata) code=%d stderr=%q", code, stderr.String())
	}
	body := stdout.String()
	for _, leaked := range []string{"https://changes.example.invalid", "reviewer@example.invalid", "/srv/backups/customer-retention.dump"} {
		if strings.Contains(body, leaked) {
			t.Fatalf("config dry-run leaked unsafe metadata %q in body=%s", leaked, body)
		}
	}
	if !strings.Contains(body, "redacted:") {
		t.Fatalf("config dry-run body=%s missing redacted metadata marker", body)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run(context.Background(), []string{"cache", "purge", "--db", dbPath, "--config", incompletePolicy, "--confirm"}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("Run(config confirm missing approval) succeeded unexpectedly: stdout=%q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "approval is incomplete") {
		t.Fatalf("stderr=%q missing approval validation", stderr.String())
	}

	validPolicy := filepath.Join(dir, "retention-valid.yaml")
	if err := os.WriteFile(validPolicy, []byte(`version: 1
older_than: 2026-04-22
approval:
  reference: CHANGE-124
  approved_by: revops-retention-reviewer
  approved_at: 2024-01-01
  data_owner: revenue-operations
  backup_reference: backup-20240101-synthetic
  legal_hold_reviewed: true
`), 0o600); err != nil {
		t.Fatalf("write valid policy: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run(context.Background(), []string{"cache", "purge", "--db", dbPath, "--config", validPolicy, "--confirm"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run(config confirm) code=%d stderr=%q", code, stderr.String())
	}
	var confirmResp struct {
		DryRun   bool `json:"dry_run"`
		Executed bool `json:"executed"`
		Plan     struct {
			CallCount int64 `json:"call_count"`
		} `json:"plan"`
		RetentionPolicy *cacheRetentionPolicyMetadata `json:"retention_policy"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &confirmResp); err != nil {
		t.Fatalf("json.Unmarshal confirm returned error: %v", err)
	}
	if confirmResp.DryRun || !confirmResp.Executed || confirmResp.Plan.CallCount != 1 {
		t.Fatalf("unexpected config confirm response: %+v", confirmResp)
	}
	if confirmResp.RetentionPolicy == nil || !confirmResp.RetentionPolicy.ApprovalComplete || confirmResp.RetentionPolicy.BackupReference != "backup-20240101-synthetic" {
		t.Fatalf("unexpected confirmed retention policy metadata: %+v", confirmResp.RetentionPolicy)
	}

	readOnly, err := sqlite.OpenReadOnly(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("OpenReadOnly after config confirm returned error: %v", err)
	}
	inventory, err := readOnly.CacheInventory(context.Background())
	if err != nil {
		t.Fatalf("CacheInventory after config confirm returned error: %v", err)
	}
	if inventory.Summary.TotalCalls != 1 || inventory.OldestCallStartedAt != "2026-04-24T14:00:00Z" {
		t.Fatalf("config confirm did not purge expected rows: %+v", inventory.Summary)
	}
	if err := readOnly.Close(); err != nil {
		t.Fatalf("Close read-only returned error: %v", err)
	}
}
