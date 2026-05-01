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

func TestSupportBundleWritesMetadataOnlyBundle(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gong.db")
	outDir := filepath.Join(t.TempDir(), "support-bundle")
	store := openCLITestStore(t, dbPath)

	ctx := context.Background()
	if _, err := store.UpsertCall(ctx, mustMarshalJSON(t, map[string]any{
		"id":      "support-call-secret",
		"title":   "Sensitive Support Account renewal call",
		"started": "2026-04-20T14:00:00Z",
		"context": map[string]any{
			"crmObjects": []map[string]any{
				{
					"type": "Account",
					"id":   "support-object-secret",
					"name": "Sensitive Support Account",
					"fields": []map[string]any{
						{"name": "Name", "value": "Sensitive Support Account"},
					},
				},
			},
		},
	})); err != nil {
		t.Fatalf("UpsertCall returned error: %v", err)
	}
	if _, err := store.UpsertTranscript(ctx, mustMarshalJSON(t, map[string]any{
		"callId": "support-call-secret",
		"transcript": []any{
			map[string]any{
				"speakerId": "support-speaker-secret",
				"sentences": []map[string]any{
					{"start": 0, "end": 1000, "text": "Sensitive Support Transcript should not appear."},
				},
			},
		},
	})); err != nil {
		t.Fatalf("UpsertTranscript returned error: %v", err)
	}
	run, err := store.StartSyncRun(ctx, sqlite.StartSyncRunParams{
		Scope:   "calls",
		SyncKey: "calls:support-call-secret",
		From:    "2026-04-20",
		To:      "2026-04-21",
	})
	if err != nil {
		t.Fatalf("StartSyncRun returned error: %v", err)
	}
	if err := store.FinishSyncRun(ctx, run.ID, sqlite.FinishSyncRunParams{
		Status:         "success",
		RecordsSeen:    1,
		RecordsWritten: 1,
		ErrorText:      "Sensitive Support Account raw error should not appear",
	}); err != nil {
		t.Fatalf("FinishSyncRun returned error: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	t.Setenv("GONG_ACCESS_KEY_SECRET", "support-secret-value")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"support", "bundle", "--db", dbPath, "--out", outDir, "--include-env"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run(support bundle) code=%d stderr=%q", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr=%q want empty", stderr.String())
	}

	var response struct {
		OutputDirectory struct {
			PathPolicy string `json:"path_policy"`
		} `json:"output_directory"`
		SchemaVersion                       int      `json:"schema_version"`
		Files                               []string `json:"files"`
		ContainsRawCustomerData             bool     `json:"contains_raw_customer_data"`
		ContainsCustomerOperationalMetadata bool     `json:"contains_customer_operational_metadata"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.OutputDirectory.PathPolicy != "local_path_not_exported" ||
		response.SchemaVersion != 1 ||
		response.ContainsRawCustomerData ||
		!response.ContainsCustomerOperationalMetadata {
		t.Fatalf("unexpected response: %+v", response)
	}

	for _, name := range []string{"manifest.json", "cache-summary.json", "mcp-tools.json", "redaction-policy.json", "environment.json"} {
		if _, err := os.Stat(filepath.Join(outDir, name)); err != nil {
			t.Fatalf("expected bundle file %s: %v", name, err)
		}
	}

	body := readSupportBundleBody(t, outDir)
	for _, forbidden := range []string{
		"Sensitive Support Account",
		"Sensitive Support Transcript",
		"support-call-secret",
		"support-object-secret",
		"support-speaker-secret",
		"support-secret-value",
		dbPath,
		filepath.Base(dbPath),
		outDir,
		filepath.Base(outDir),
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("support bundle leaked %q in:\n%s", forbidden, body)
		}
	}
	if !strings.Contains(body, `"total_calls": 1`) || !strings.Contains(body, `"total_transcripts": 1`) {
		t.Fatalf("support bundle missing expected counts:\n%s", body)
	}
	if !strings.Contains(body, `"GONG_ACCESS_KEY_SECRET": true`) {
		t.Fatalf("support bundle missing env presence boolean:\n%s", body)
	}
}

func readSupportBundleBody(t *testing.T, dir string) string {
	t.Helper()
	var builder strings.Builder
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir returned error: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatalf("ReadFile(%s) returned error: %v", entry.Name(), err)
		}
		builder.Write(body)
		builder.WriteByte('\n')
	}
	return builder.String()
}
