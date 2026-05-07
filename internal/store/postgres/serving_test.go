package postgres

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fyne-coder/gongcli_mcp/internal/governance"
)

func TestValidateRefreshServingDBURLs(t *testing.T) {
	cases := []struct {
		name    string
		source  string
		target  string
		wantErr string
	}{
		{name: "missing source", source: "", target: "postgres://h/b", wantErr: "--source"},
		{name: "missing target", source: "postgres://h/a", target: "", wantErr: "--target"},
		{
			name:    "identical URLs rejected",
			source:  "postgres://op:secret@host:5432/gongctl_source",
			target:  "postgres://op:secret@host:5432/gongctl_source",
			wantErr: "different databases",
		},
		{
			name:    "same db differing creds rejected",
			source:  "postgres://op:s@host:5432/gongctl_source",
			target:  "postgres://reader:r@host:5432/gongctl_source",
			wantErr: "different databases",
		},
		{
			name:    "different paths accepted",
			source:  "postgres://op:s@host:5432/gongctl_source",
			target:  "postgres://op:s@host:5432/gongctl_mcp",
			wantErr: "",
		},
		{
			name:    "different hosts accepted",
			source:  "postgres://op:s@host-a:5432/gongctl",
			target:  "postgres://op:s@host-b:5432/gongctl",
			wantErr: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRefreshServingDBURLs(tc.source, tc.target)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q did not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestRefreshServingDBRequiresConfig(t *testing.T) {
	_, err := RefreshServingDB(context.Background(), RefreshServingDBOptions{
		SourceURL: "postgres://h/a",
		TargetURL: "postgres://h/b",
		Config:    nil,
	})
	if err == nil || !strings.Contains(err.Error(), "governance config") {
		t.Fatalf("expected governance config error, got %v", err)
	}
}

// TestRefreshServingDBVerticalSliceCopiesAllowedRows is the integration test
// for Phase 13e4. It requires two synthetic Postgres databases:
//
//   - GONGCTL_TEST_POSTGRES_URL: source operator cache (gongctl_source).
//   - GONGCTL_TEST_POSTGRES_TARGET_URL: redacted MCP serving DB (gongctl_mcp).
//
// Both databases must be empty/synthetic; the test resets their state.
func TestRefreshServingDBVerticalSliceCopiesAllowedRows(t *testing.T) {
	sourceURL := os.Getenv("GONGCTL_TEST_POSTGRES_URL")
	targetURL := os.Getenv("GONGCTL_TEST_POSTGRES_TARGET_URL")
	if sourceURL == "" || targetURL == "" {
		t.Skip("GONGCTL_TEST_POSTGRES_URL and GONGCTL_TEST_POSTGRES_TARGET_URL must both be set")
	}
	if sourceURL == targetURL {
		t.Skip("GONGCTL_TEST_POSTGRES_URL and GONGCTL_TEST_POSTGRES_TARGET_URL must point to different databases")
	}

	ctx := context.Background()

	source, err := Open(ctx, sourceURL)
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	defer source.Close()
	resetPostgresTestStore(t, ctx, source)

	target, err := Open(ctx, targetURL)
	if err != nil {
		t.Fatalf("open target: %v", err)
	}
	resetPostgresTestStore(t, ctx, target)
	target.Close()

	allowedCalls := []map[string]any{
		{
			"id":       "pg-serving-allowed-001",
			"title":    "Allowed serving call one",
			"started":  "2026-04-24T12:00:00Z",
			"duration": 900,
			"content": map[string]any{
				"brief": "Allowed synthetic brief.",
				"highlights": []map[string]any{
					{"type": "summary", "text": "Allowed synthetic highlight."},
				},
				"keyPoints": []map[string]any{
					{"text": "Allowed synthetic key point."},
				},
			},
			"context": []any{
				map[string]any{
					"objectType": "Account",
					"id":         "acct-allowed-001",
					"name":       "Allowed Synthetic Co",
					"fields": []any{
						map[string]any{"name": "Name", "value": "Allowed Synthetic Co"},
					},
				},
			},
		},
		{
			"id":       "pg-serving-allowed-002",
			"title":    "Allowed serving call two",
			"started":  "2026-04-24T13:00:00Z",
			"duration": 600,
		},
	}
	restrictedCalls := []map[string]any{
		{
			"id":       "pg-serving-blocked-001",
			"title":    "Blocked serving call one",
			"started":  "2026-04-24T14:00:00Z",
			"duration": 700,
			"content": map[string]any{
				"highlights": []map[string]any{
					{"type": "summary", "text": "Restricted synthetic highlight."},
				},
			},
			"context": []any{
				map[string]any{
					"objectType": "Account",
					"id":         "acct-blocked-001",
					"name":       "Blocked Synthetic Corp",
					"fields": []any{
						map[string]any{"name": "Name", "value": "Blocked Synthetic Corp"},
					},
				},
			},
		},
	}
	allCalls := append(append([]map[string]any{}, allowedCalls...), restrictedCalls...)
	for _, payload := range allCalls {
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal call: %v", err)
		}
		if _, err := source.UpsertCall(ctx, raw); err != nil {
			t.Fatalf("upsert source call %s: %v", payload["id"], err)
		}
	}

	allowedTranscript := json.RawMessage(`{"callId":"pg-serving-allowed-001","transcript":[{"speakerId":"speaker-1","sentences":[{"start":0,"end":3000,"text":"Allowed transcript content."}]}]}`)
	if _, err := source.UpsertTranscript(ctx, allowedTranscript); err != nil {
		t.Fatalf("upsert allowed transcript: %v", err)
	}
	blockedTranscript := json.RawMessage(`{"callId":"pg-serving-blocked-001","transcript":[{"speakerId":"speaker-1","sentences":[{"start":0,"end":3000,"text":"Restricted transcript content."}]}]}`)
	if _, err := source.UpsertTranscript(ctx, blockedTranscript); err != nil {
		t.Fatalf("upsert blocked transcript: %v", err)
	}

	if _, err := source.UpsertUser(ctx, json.RawMessage(`{"id":"pg-user-serving-001","emailAddress":"op@example.invalid","firstName":"Op","lastName":"One","title":"RevOps","active":true}`)); err != nil {
		t.Fatalf("upsert user: %v", err)
	}
	if _, err := source.UpsertGongSetting(ctx, "scorecards", json.RawMessage(`{"scorecardId":"pg-serving-scorecard-001","scorecardName":"Synthetic Scorecard","active":true,"reviewMethod":"MANUAL","questions":[{"questionId":"q1","questionText":"Did discovery happen?"}]}`)); err != nil {
		t.Fatalf("upsert scorecard setting: %v", err)
	}
	if _, err := source.UpsertScorecardActivity(ctx, json.RawMessage(`{"answeredScorecardId":"pg-serving-activity-allowed","scorecardId":"pg-serving-scorecard-001","scorecardName":"Synthetic Scorecard","callId":"pg-serving-allowed-001","callStartTime":"2026-04-24T12:00:00Z","reviewMethod":"MANUAL","reviewTime":"2026-04-24T15:00:00Z","answers":[{"questionId":"q1","score":4}]}`)); err != nil {
		t.Fatalf("upsert allowed scorecard activity: %v", err)
	}
	if _, err := source.UpsertScorecardActivity(ctx, json.RawMessage(`{"answeredScorecardId":"pg-serving-activity-blocked","scorecardId":"pg-serving-scorecard-001","scorecardName":"Synthetic Scorecard","callId":"pg-serving-blocked-001","callStartTime":"2026-04-24T14:00:00Z","reviewMethod":"MANUAL","reviewTime":"2026-04-24T16:00:00Z","answers":[{"questionId":"q1","score":1}]}`)); err != nil {
		t.Fatalf("upsert blocked scorecard activity: %v", err)
	}

	configPath := filepath.Join(t.TempDir(), "ai-governance.yaml")
	if err := os.WriteFile(configPath, []byte(`
version: 1
lists:
  no_ai:
    customers:
      - name: "Blocked Synthetic Corp"
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := governance.LoadFile(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	result, err := RefreshServingDB(ctx, RefreshServingDBOptions{
		SourceURL: sourceURL,
		TargetURL: targetURL,
		Config:    cfg,
	})
	if err != nil {
		t.Fatalf("RefreshServingDB: %v", err)
	}

	if result.Backend != "postgres" {
		t.Fatalf("backend = %q want postgres", result.Backend)
	}
	if result.SourceCalls != 3 {
		t.Fatalf("source_calls = %d want 3", result.SourceCalls)
	}
	if result.TargetCalls != 2 {
		t.Fatalf("target_calls = %d want 2", result.TargetCalls)
	}
	if result.RemovedCalls != 1 {
		t.Fatalf("removed_calls = %d want 1", result.RemovedCalls)
	}
	if result.SuppressedCallCount != 1 {
		t.Fatalf("suppressed_call_count = %d want 1", result.SuppressedCallCount)
	}
	if result.TargetTranscripts != 1 {
		t.Fatalf("target_transcripts = %d want 1", result.TargetTranscripts)
	}
	if result.TargetTranscriptSegments != 1 {
		t.Fatalf("target_transcript_segments = %d want 1", result.TargetTranscriptSegments)
	}
	if result.TargetUsers != 1 {
		t.Fatalf("target_users = %d want 1", result.TargetUsers)
	}
	if result.SourceScorecards != 1 || result.TargetScorecards != 1 {
		t.Fatalf("scorecard counts source=%d target=%d want 1/1", result.SourceScorecards, result.TargetScorecards)
	}
	if result.SourceScorecardActivity != 2 || result.TargetScorecardActivity != 1 {
		t.Fatalf("scorecard activity counts source=%d target=%d want 2/1", result.SourceScorecardActivity, result.TargetScorecardActivity)
	}
	if result.SourceAIHighlights != 4 || result.RemovedAIHighlights != 1 {
		t.Fatalf("AI highlight counts source=%d removed=%d want 4/1", result.SourceAIHighlights, result.RemovedAIHighlights)
	}
	if result.TargetAIHighlights != 3 {
		t.Fatalf("target_ai_highlights = %d want 3", result.TargetAIHighlights)
	}
	if result.PolicyConfigSHA256 == "" {
		t.Fatalf("policy_config_sha256 should be set")
	}
	if result.SourceDataFingerprint == "" || result.TargetDataFingerprint == "" {
		t.Fatalf("data fingerprints should be set: %+v", result)
	}

	body, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	for _, leak := range []string{
		"Blocked Synthetic Corp",
		"Restricted transcript content",
		"pg-serving-blocked-001",
		"Blocked serving call one",
		sourceURL,
		targetURL,
	} {
		if strings.Contains(string(body), leak) {
			t.Fatalf("sanitized output leaked %q: %s", leak, body)
		}
	}

	target, err = Open(ctx, targetURL)
	if err != nil {
		t.Fatalf("reopen target: %v", err)
	}
	defer target.Close()

	assertPostgresCount(t, ctx, target, `SELECT COUNT(*) FROM calls WHERE call_id = 'pg-serving-blocked-001'`, 0)
	assertPostgresCount(t, ctx, target, `SELECT COUNT(*) FROM transcripts WHERE call_id = 'pg-serving-blocked-001'`, 0)
	assertPostgresCount(t, ctx, target, `SELECT COUNT(*) FROM transcript_segments WHERE call_id = 'pg-serving-blocked-001'`, 0)
	assertPostgresCount(t, ctx, target, `SELECT COUNT(*) FROM call_context_objects WHERE call_id = 'pg-serving-blocked-001'`, 0)
	assertPostgresCount(t, ctx, target, `SELECT COUNT(*) FROM call_context_fields WHERE call_id = 'pg-serving-blocked-001'`, 0)
	assertPostgresCount(t, ctx, target, `SELECT COUNT(*) FROM call_ai_highlights WHERE call_id = 'pg-serving-blocked-001'`, 0)
	assertPostgresCount(t, ctx, target, `SELECT COUNT(*) FROM scorecard_activity WHERE call_id = 'pg-serving-blocked-001'`, 0)
	assertPostgresCount(t, ctx, target, `SELECT COUNT(*) FROM calls`, 2)
	assertPostgresCount(t, ctx, target, `SELECT COUNT(*) FROM transcripts WHERE call_id = 'pg-serving-allowed-001'`, 1)
	assertPostgresCount(t, ctx, target, `SELECT COUNT(*) FROM call_ai_highlights WHERE call_id = 'pg-serving-allowed-001'`, 3)
	assertPostgresCount(t, ctx, target, `SELECT COUNT(*) FROM gong_settings WHERE kind = 'scorecards'`, 1)
	assertPostgresCount(t, ctx, target, `SELECT COUNT(*) FROM scorecard_activity WHERE call_id = 'pg-serving-allowed-001'`, 1)
	// call_facts should be rebuilt for allowed calls.
	assertPostgresCount(t, ctx, target, `SELECT COUNT(*) FROM call_facts WHERE call_id = 'pg-serving-blocked-001'`, 0)
	assertPostgresCount(t, ctx, target, `SELECT COUNT(*) FROM call_facts`, 2)
	// Re-running governance audit on the target should find zero candidates
	// for the blocked customer name.
	targetAudit, err := governance.BuildAudit(ctx, target, cfg)
	if err != nil {
		t.Fatalf("rebuild audit on target: %v", err)
	}
	if targetAudit.SuppressedCallCount != 0 {
		t.Fatalf("target audit still has %d suppressed candidates after redaction; expected 0", targetAudit.SuppressedCallCount)
	}
}

func TestValidateServingCopyCountsRejectsMissingNonRedactedRows(t *testing.T) {
	counts := servingCopyCounts{
		sourceCalls:               10,
		sourceUsers:               3,
		sourceGongSettings:        2,
		sourceScorecards:          1,
		sourceScorecardActivity:   5,
		sourceAIHighlights:        7,
		redactedCalls:             2,
		redactedScorecardActivity: 1,
		redactedAIHighlights:      2,
		targetCalls:               8,
		targetUsers:               0,
		targetGongSettings:        0,
		targetScorecards:          0,
		targetScorecardActivity:   4,
		targetAIHighlights:        0,
	}
	err := validateServingCopyCounts(counts)
	if err == nil {
		t.Fatal("expected validation error")
	}
	got := err.Error()
	for _, want := range []string{
		"users target=0 want=3",
		"gong_settings target=0 want=2",
		"scorecards target=0 want=1",
		"call_ai_highlights target=0 want=5",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("validation error %q missing %q", got, want)
		}
	}
	if strings.Contains(got, "Blocked Synthetic Corp") || strings.Contains(got, "pg-serving-blocked") {
		t.Fatalf("validation error leaked restricted details: %v", err)
	}
}

// TestRefreshServingDBRejectsSameURL ensures Open* paths are not invoked when
// validation fails.
func TestRefreshServingDBRejectsSameURL(t *testing.T) {
	sameURL := "postgres://op:secret@example.invalid:5432/gongctl_source"
	cfg, err := governance.ParseYAML([]byte(`
version: 1
lists:
  no_ai:
    customers:
      - name: "Refresh Synthetic Corp"
`))
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	_, err = RefreshServingDB(context.Background(), RefreshServingDBOptions{
		SourceURL: sameURL,
		TargetURL: sameURL,
		Config:    cfg,
	})
	if err == nil {
		t.Fatal("expected error when source and target URLs match")
	}
	if !strings.Contains(err.Error(), "different databases") {
		t.Fatalf("error %q did not mention different databases", err.Error())
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), sameURL) {
		t.Fatalf("error leaked credentials/URL: %v", err)
	}
}
