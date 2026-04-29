package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSyncRunDryRunUsesFixtureAndResolvesRelativePaths(t *testing.T) {
	fixturePath := filepath.Join("..", "..", "testdata", "fixtures", "sync-run-minimal.yaml")
	absFixture, err := filepath.Abs(fixturePath)
	if err != nil {
		t.Fatalf("filepath.Abs returned error: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"sync", "run", "--config", fixturePath, "--dry-run"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run(sync run dry-run) code=%d stderr=%q", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr=%q want empty", stderr.String())
	}

	var resp struct {
		ConfigPath string `json:"config_path"`
		Version    int    `json:"version"`
		DBPath     string `json:"db_path"`
		DryRun     bool   `json:"dry_run"`
		Steps      []struct {
			Name                    string `json:"name"`
			Action                  string `json:"action"`
			Status                  string `json:"status"`
			Preset                  string `json:"preset"`
			MaxPages                int    `json:"max_pages"`
			SettingsKind            string `json:"settings_kind"`
			RequiresSensitiveExport bool   `json:"requires_sensitive_export"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}

	if resp.ConfigPath != absFixture {
		t.Fatalf("config_path=%q want %q", resp.ConfigPath, absFixture)
	}
	if resp.Version != 1 || !resp.DryRun {
		t.Fatalf("unexpected run header: %+v", resp)
	}
	wantDBPath := filepath.Join(filepath.Dir(absFixture), "run-cache", "gong.db")
	if resp.DBPath != wantDBPath {
		t.Fatalf("db_path=%q want %q", resp.DBPath, wantDBPath)
	}
	if len(resp.Steps) != 4 {
		t.Fatalf("step count=%d want 4", len(resp.Steps))
	}
	if resp.Steps[0].Name != "daily_calls" || resp.Steps[0].Action != "calls" || resp.Steps[0].Preset != "minimal" || resp.Steps[0].MaxPages != 1 || resp.Steps[0].Status != "planned" {
		t.Fatalf("unexpected first step: %+v", resp.Steps[0])
	}
	for _, step := range resp.Steps {
		if step.RequiresSensitiveExport {
			t.Fatalf("fixture step %q unexpectedly marked sensitive: %+v", step.Name, step)
		}
	}
}

func TestSyncRunDryRunFlagsSensitiveSteps(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "sync-run.yaml")
	body := []byte(`version: 1
db: ./cache/gong.db
steps:
  - name: business_calls
    action: calls
    from: 2026-04-01
    to: 2026-04-02
    preset: business
  - name: transcript_backfill
    action: transcripts
    out_dir: ./transcripts
    limit: 25
    batch_size: 10
`)
	if err := os.WriteFile(configPath, body, 0o600); err != nil {
		t.Fatalf("os.WriteFile returned error: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"sync", "run", "--config", configPath, "--dry-run"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run(sync run sensitive dry-run) code=%d stderr=%q", code, stderr.String())
	}

	var resp struct {
		DBPath string `json:"db_path"`
		Steps  []struct {
			Name                    string `json:"name"`
			Action                  string `json:"action"`
			OutDir                  string `json:"out_dir"`
			Limit                   int    `json:"limit"`
			BatchSize               int    `json:"batch_size"`
			RequiresSensitiveExport bool   `json:"requires_sensitive_export"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}

	if resp.DBPath != filepath.Join(dir, "cache", "gong.db") {
		t.Fatalf("db_path=%q want %q", resp.DBPath, filepath.Join(dir, "cache", "gong.db"))
	}
	if len(resp.Steps) != 2 {
		t.Fatalf("step count=%d want 2", len(resp.Steps))
	}
	if !resp.Steps[0].RequiresSensitiveExport || resp.Steps[0].Action != "calls" {
		t.Fatalf("unexpected first sensitive step: %+v", resp.Steps[0])
	}
	if !resp.Steps[1].RequiresSensitiveExport || resp.Steps[1].OutDir != filepath.Join(dir, "transcripts") || resp.Steps[1].Limit != 25 || resp.Steps[1].BatchSize != 10 {
		t.Fatalf("unexpected transcript sensitive step: %+v", resp.Steps[1])
	}
}

func TestSyncRunRestrictedModeBlocksSensitiveStepsBeforeOpeningDB(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "sync-run.yaml")
	dbPath := filepath.Join(dir, "cache", "gong.db")
	body := []byte(`version: 1
db: ./cache/gong.db
steps:
  - name: business_calls
    action: calls
    from: 2026-04-01
    to: 2026-04-02
    preset: business
`)
	if err := os.WriteFile(configPath, body, 0o600); err != nil {
		t.Fatalf("os.WriteFile returned error: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"--restricted", "sync", "run", "--config", configPath}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("Run(sync run restricted) code=%d stderr=%q", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout=%q want empty", stdout.String())
	}
	if got := stderr.String(); !strings.Contains(got, `sync run calls step "business" is blocked because restricted mode is enabled`) {
		t.Fatalf("stderr=%q missing restricted sync-run guidance", got)
	}
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatalf("db path exists after preflight block: err=%v", err)
	}
}

func TestSyncRunConfigRejectsSensitiveExportField(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "sync-run.yaml")
	body := []byte(`version: 1
db: ./cache/gong.db
steps:
  - name: transcript_backfill
    action: transcripts
    out_dir: ./transcripts
    allow_sensitive_export: true
`)
	if err := os.WriteFile(configPath, body, 0o600); err != nil {
		t.Fatalf("os.WriteFile returned error: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"sync", "run", "--config", configPath, "--dry-run"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("Run(sync run invalid config) code=%d stderr=%q", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout=%q want empty", stdout.String())
	}
	if got := stderr.String(); !strings.Contains(got, "field allow_sensitive_export not found") {
		t.Fatalf("stderr=%q missing unknown-field error", got)
	}
}
