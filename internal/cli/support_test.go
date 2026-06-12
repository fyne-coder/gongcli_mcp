package cli

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fyne-coder/gongcli_mcp/internal/store/postgres"
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

	t.Setenv("GONG_DATABASE_URL", "postgres://gongctl:secret@127.0.0.1:1/gongctl?sslmode=disable")
	t.Setenv("DATABASE_URL", "")
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
	if _, err := os.Stat(filepath.Join(outDir, "postgres-deployment.json")); !os.IsNotExist(err) {
		t.Fatalf("sqlite bundle should not write postgres-deployment.json: %v", err)
	}
	for _, name := range response.Files {
		if name == "postgres-deployment.json" {
			t.Fatalf("sqlite bundle response listed postgres-deployment.json: %+v", response.Files)
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
	if !strings.Contains(body, `"backend": "sqlite"`) || !strings.Contains(body, `"path_policy": "no_path_or_filename_exported"`) {
		t.Fatalf("support bundle missing SQLite backend/path policy:\n%s", body)
	}
	if !strings.Contains(body, `"GONG_ACCESS_KEY_SECRET": true`) {
		t.Fatalf("support bundle missing env presence boolean:\n%s", body)
	}
	if !strings.Contains(body, `"contains_customer_operational_metadata": true`) {
		t.Fatalf("support bundle missing operational metadata warning:\n%s", body)
	}
	if strings.Contains(body, "no_identifiers") {
		t.Fatalf("support bundle overstates identifier exclusion:\n%s", body)
	}
}

func TestSupportBundleRequiresEmptyOutputDirectory(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gong.db")
	outDir := filepath.Join(t.TempDir(), "support-bundle")
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "old-debug.txt"), []byte("stale raw debug output"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	store := openCLITestStore(t, dbPath)
	if err := store.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"support", "bundle", "--db", dbPath, "--out", outDir}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("Run(support bundle) unexpectedly succeeded stdout=%q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "empty directory") {
		t.Fatalf("stderr=%q want empty-directory guidance", stderr.String())
	}
}

func TestSupportBundlePostgresWritesDeploymentArtifact(t *testing.T) {
	clearDeployEnv(t)
	outDir := filepath.Join(t.TempDir(), "support-bundle")
	readerURL := "postgres://gongmcp_reader:reader-secret@127.0.0.1:5432/gongctl?sslmode=disable"
	sourceURL := "postgres://gongctl:source-secret@source.internal:5432/gongctl_source?sslmode=disable"
	t.Setenv("GONG_DATABASE_URL", readerURL)
	t.Setenv("DATABASE_URL", "")
	t.Setenv("GONGCTL_SOURCE_DATABASE_URL", sourceURL)
	t.Setenv("GONGCTL_REFRESH_STATEMENT_TIMEOUT", "45m")
	t.Setenv("GONGMCP_TOOL_PRESET", "business-workbench")

	markerTime := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339Nano)
	withSupportBundleStoreFakes(t, func(ctx context.Context, path string) (cacheInventoryStore, string, string, error) {
		if strings.TrimSpace(path) != "" {
			return defaultOpenCacheInventoryStore(ctx, path)
		}
		return &fakeSupportPostgresStore{
			inventory: &sqlite.CacheInventory{
				Summary: &sqlite.SyncStatusSummary{},
			},
			diagnostics: &postgres.CacheDiagnostics{
				Backend:                "postgres",
				SchemaVersion:          1,
				SupportedSchemaVersion: 1,
				ReadModelReady:         true,
				ReadModelStatus:        "current",
				ProfileCacheStatus:     "fresh",
				ReaderPrivilegeStatus:  "valid_reader",
			},
			marker: &postgres.ServingDBRefreshMarker{
				ID:                    7,
				RefreshedAt:           markerTime,
				SourceDataFingerprint: "source-fingerprint-sha256",
				TargetDataFingerprint: "target-fingerprint-sha256",
				PolicyConfigSHA256:    "policy-config-sha256",
			},
		}, "postgres", "", nil
	})
	withDoctorFakes(t, func(ctx context.Context, databaseURL string) (doctorPostgresStatusStore, error) {
		if databaseURL != sourceURL {
			return nil, errors.New("unexpected source database URL")
		}
		return &fakeDoctorPostgresStore{
			readModel: &postgres.ReadModelStatus{ModelName: "builtin_call_facts", Ready: true, CallCount: 3},
		}, nil
	})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"support", "bundle", "--out", outDir}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run(support bundle) code=%d stderr=%q", code, stderr.String())
	}

	var response struct {
		Files []string `json:"files"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !supportBundleFilesContains(response.Files, "postgres-deployment.json") {
		t.Fatalf("postgres bundle missing postgres-deployment.json in files: %+v", response.Files)
	}
	if !supportBundleFilesContains(response.Files, "diagnostics.json") {
		t.Fatalf("postgres bundle missing diagnostics.json in files: %+v", response.Files)
	}
	if _, err := os.Stat(filepath.Join(outDir, "postgres-deployment.json")); err != nil {
		t.Fatalf("expected postgres-deployment.json: %v", err)
	}

	body := readSupportBundleBody(t, outDir)
	if !strings.Contains(body, `"status": "pass"`) || !strings.Contains(body, `"name": "serving_refresh_marker"`) {
		t.Fatalf("postgres deployment artifact missing serving refresh marker pass:\n%s", body)
	}
	if !strings.Contains(body, `"status": "not_available"`) || !strings.Contains(body, `"refresh_progress"`) {
		t.Fatalf("postgres deployment artifact missing deferred refresh progress:\n%s", body)
	}
	if !strings.Contains(body, `"GONGCTL_REFRESH_STATEMENT_TIMEOUT": true`) || !strings.Contains(body, `"GONGCTL_SOURCE_DATABASE_URL": true`) {
		t.Fatalf("postgres deployment artifact missing deployment config posture:\n%s", body)
	}
	if !strings.Contains(body, `"statement_timeout"`) || !strings.Contains(body, `"45m"`) {
		t.Fatalf("postgres deployment artifact missing statement timeout evidence:\n%s", body)
	}
	assertSupportBundleSanitized(t, body, []string{
		readerURL,
		sourceURL,
		"reader-secret",
		"source-secret",
		"127.0.0.1",
		"source.internal",
		"postgres://",
		"GRANT CONNECT",
		"BEGIN;",
		outDir,
	})
}

func TestSupportBundlePostgresDeploymentOmitsForbiddenValues(t *testing.T) {
	clearDeployEnv(t)
	outDir := filepath.Join(t.TempDir(), "support-bundle")
	t.Setenv("GONG_DATABASE_URL", "postgres://gongmcp_reader:bundle-secret@db.example:5432/gongctl?sslmode=require")

	withSupportBundleStoreFakes(t, func(ctx context.Context, path string) (cacheInventoryStore, string, string, error) {
		return &fakeSupportPostgresStore{
			inventory: &sqlite.CacheInventory{Summary: &sqlite.SyncStatusSummary{}},
			diagnostics: &postgres.CacheDiagnostics{
				Backend:         "postgres",
				ReadModelStatus: "current",
			},
			markerErr: sql.ErrNoRows,
		}, "postgres", "", nil
	})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"support", "bundle", "--out", outDir}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run(support bundle) code=%d stderr=%q", code, stderr.String())
	}

	body := readSupportBundleBody(t, outDir) + stdout.String()
	assertSupportBundleSanitized(t, body, []string{
		"postgres://",
		"bundle-secret",
		"db.example",
		"GRANT ",
		"CREATE ROLE",
	})
}

func supportBundleFilesContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func assertSupportBundleSanitized(t *testing.T, body string, forbidden []string) {
	t.Helper()
	for _, leak := range forbidden {
		if strings.Contains(body, leak) {
			t.Fatalf("support bundle leaked %q in:\n%s", leak, body)
		}
	}
}

type fakeSupportPostgresStore struct {
	inventory   *sqlite.CacheInventory
	diagnostics *postgres.CacheDiagnostics
	marker      *postgres.ServingDBRefreshMarker
	markerErr   error
}

func (f *fakeSupportPostgresStore) Close() error { return nil }

func (f *fakeSupportPostgresStore) CacheInventory(context.Context) (*sqlite.CacheInventory, error) {
	return f.inventory, nil
}

func (f *fakeSupportPostgresStore) CacheDiagnostics(context.Context) (*postgres.CacheDiagnostics, error) {
	return f.diagnostics, nil
}

func (f *fakeSupportPostgresStore) LatestServingRefreshMarker(context.Context) (*postgres.ServingDBRefreshMarker, error) {
	if f.markerErr != nil {
		return nil, f.markerErr
	}
	return f.marker, nil
}

func withSupportBundleStoreFakes(t *testing.T, open func(context.Context, string) (cacheInventoryStore, string, string, error)) {
	t.Helper()
	original := openCacheInventoryStoreFn
	openCacheInventoryStoreFn = open
	t.Cleanup(func() {
		openCacheInventoryStoreFn = original
	})
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
