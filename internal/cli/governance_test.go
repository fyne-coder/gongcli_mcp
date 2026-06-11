package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fyne-coder/gongcli_mcp/internal/store/postgres"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestGovernanceAuditReportsMatchedAndUnmatchedSyntheticNames(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "gong.db")
	store := openCLITestStore(t, dbPath)

	if _, err := store.UpsertCall(context.Background(), mustMarshalJSON(t, map[string]any{
		"id":       "call-governance-cli-001",
		"title":    "Governance CLI call",
		"started":  "2026-04-24T12:00:00Z",
		"duration": 1200,
		"context": []any{
			map[string]any{
				"objectType": "Account",
				"id":         "acct-governance-cli-001",
				"name":       "Audit Synthetic Corp",
				"fields": []any{
					map[string]any{"name": "Name", "value": "Audit Synthetic Corp"},
				},
			},
		},
	})); err != nil {
		t.Fatalf("upsert governance call: %v", err)
	}
	store.Close()

	configPath := filepath.Join(dir, "ai-governance.yaml")
	if err := os.WriteFile(configPath, []byte(`
version: 1
lists:
  no_ai:
    customers:
      - name: "Audit Synthetic Corp"
  notification_required:
    customers:
      - name: "Missing Synthetic Corp"
        aliases: ["Missing Synthetic Alias"]
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"governance", "audit", "--db", dbPath, "--config", configPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run(governance audit) code=%d stderr=%q", code, stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{
		"matched=1",
		"unmatched=2",
		"suppressed_calls=1",
		"Audit Synthetic Corp",
		"Missing Synthetic Corp",
		"Missing Synthetic Alias",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("audit output missing %q: %s", want, output)
		}
	}
}

func TestGovernanceRefreshServingDBRejectsIdenticalURLs(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "ai-governance.yaml")
	if err := os.WriteFile(configPath, []byte(`
version: 1
lists:
  no_ai:
    customers:
      - name: "Refresh Synthetic Corp"
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	url := "postgres://operator:secret@localhost:5432/gongctl_source"

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{
		"governance", "refresh-serving-db",
		"--source", url,
		"--target", url,
		"--config", configPath,
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("expected non-zero exit when source and target match; stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	combined := stdout.String() + stderr.String()
	if !strings.Contains(combined, "different databases") {
		t.Fatalf("expected message about different databases; got stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	for _, leak := range []string{"operator", "secret"} {
		if strings.Contains(combined, leak) {
			t.Fatalf("error output leaked credentials %q: %s", leak, combined)
		}
	}
}

func TestGovernanceRefreshServingDBRequiresFlags(t *testing.T) {
	clearRefreshServingDBEnv(t)

	dir := t.TempDir()
	configPath := filepath.Join(dir, "ai-governance.yaml")
	if err := os.WriteFile(configPath, []byte(`
version: 1
lists:
  no_ai:
    customers:
      - name: "Refresh Synthetic Corp"
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "missing source",
			args: []string{"governance", "refresh-serving-db", "--target", "postgres://h/db", "--config", configPath},
			want: "--source",
		},
		{
			name: "missing target",
			args: []string{"governance", "refresh-serving-db", "--source", "postgres://h/db", "--config", configPath},
			want: "--target",
		},
		{
			name: "missing config",
			args: []string{"governance", "refresh-serving-db", "--source", "postgres://h/a", "--target", "postgres://h/b"},
			want: "--config",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Run(context.Background(), tc.args, &stdout, &stderr)
			if code == 0 {
				t.Fatalf("expected non-zero exit for %s; stdout=%q stderr=%q", tc.name, stdout.String(), stderr.String())
			}
			combined := stdout.String() + stderr.String()
			if !strings.Contains(combined, tc.want) {
				t.Fatalf("missing flag message for %s did not include %q: %s", tc.name, tc.want, combined)
			}
		})
	}
}

func TestGovernanceRefreshServingDBUsesEnvDefaults(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "ai-governance.yaml")
	if err := os.WriteFile(configPath, []byte(`
version: 1
lists:
  no_ai:
    customers:
      - name: "Refresh Synthetic Corp"
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	sourceURL := "postgres://operator:source-secret@localhost:5432/gongctl_source"
	targetURL := "postgres://operator:target-secret@localhost:5432/gongctl_mcp"
	t.Setenv("GONGCTL_SOURCE_DATABASE_URL", sourceURL)
	t.Setenv("GONGCTL_MCP_DATABASE_URL", targetURL)
	t.Setenv("GONGCTL_AI_GOVERNANCE_CONFIG", configPath)
	t.Setenv("GONGMCP_AI_GOVERNANCE_CONFIG", "")

	var captured postgres.RefreshServingDBOptions
	withRefreshServingDBFake(t, func(ctx context.Context, opts postgres.RefreshServingDBOptions) (*postgres.ServingDBRefreshResult, error) {
		captured = opts
		return &postgres.ServingDBRefreshResult{Backend: "postgres", SourceCalls: 3, TargetCalls: 2, RemovedCalls: 1}, nil
	})

	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"governance", "refresh-serving-db"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run(governance refresh-serving-db) code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if captured.SourceURL != sourceURL {
		t.Fatalf("source URL default mismatch: got %q want %q", captured.SourceURL, sourceURL)
	}
	if captured.TargetURL != targetURL {
		t.Fatalf("target URL default mismatch: got %q want %q", captured.TargetURL, targetURL)
	}
	if captured.Config == nil {
		t.Fatalf("expected governance config to be loaded")
	}
	if !strings.Contains(stdout.String(), `"removed_calls": 1`) {
		t.Fatalf("expected sanitized refresh JSON; got stdout=%q", stdout.String())
	}
	combined := stdout.String() + stderr.String()
	for _, leak := range []string{sourceURL, targetURL, "source-secret", "target-secret", configPath} {
		if strings.Contains(combined, leak) {
			t.Fatalf("refresh output leaked %q: %s", leak, combined)
		}
	}
}

func TestGovernanceRefreshServingDBFlagsOverrideEnv(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "ai-governance.yaml")
	if err := os.WriteFile(configPath, []byte(`
version: 1
lists:
  no_ai:
    customers:
      - name: "Refresh Synthetic Corp"
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	envConfigPath := filepath.Join(dir, "ignored-ai-governance.yaml")
	if err := os.WriteFile(envConfigPath, []byte(`
version: 1
lists:
  no_ai:
    customers:
      - name: "Ignored Synthetic Corp"
`), 0o600); err != nil {
		t.Fatalf("write ignored config: %v", err)
	}

	t.Setenv("GONGCTL_SOURCE_DATABASE_URL", "postgres://env:secret@localhost:5432/env_source")
	t.Setenv("GONGCTL_MCP_DATABASE_URL", "postgres://env:secret@localhost:5432/env_mcp")
	t.Setenv("GONGCTL_AI_GOVERNANCE_CONFIG", envConfigPath)
	t.Setenv("GONGMCP_AI_GOVERNANCE_CONFIG", "")

	sourceURL := "postgres://operator:source-secret@localhost:5432/gongctl_source"
	targetURL := "postgres://operator:target-secret@localhost:5432/gongctl_mcp"

	var captured postgres.RefreshServingDBOptions
	withRefreshServingDBFake(t, func(ctx context.Context, opts postgres.RefreshServingDBOptions) (*postgres.ServingDBRefreshResult, error) {
		captured = opts
		return &postgres.ServingDBRefreshResult{Backend: "postgres"}, nil
	})

	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{
		"governance", "refresh-serving-db",
		"--source", sourceURL,
		"--target", targetURL,
		"--config", configPath,
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run(governance refresh-serving-db) code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if captured.SourceURL != sourceURL {
		t.Fatalf("source URL override mismatch: got %q want %q", captured.SourceURL, sourceURL)
	}
	if captured.TargetURL != targetURL {
		t.Fatalf("target URL override mismatch: got %q want %q", captured.TargetURL, targetURL)
	}
}

func TestGovernanceRefreshServingDBUsesGONGMCPConfigFallback(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "ai-governance.yaml")
	if err := os.WriteFile(configPath, []byte(`
version: 1
lists:
  no_ai:
    customers:
      - name: "Fallback Synthetic Corp"
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Setenv("GONGCTL_SOURCE_DATABASE_URL", "postgres://operator:source-secret@localhost:5432/gongctl_source")
	t.Setenv("GONGCTL_MCP_DATABASE_URL", "postgres://operator:target-secret@localhost:5432/gongctl_mcp")
	t.Setenv("GONGCTL_AI_GOVERNANCE_CONFIG", "")
	t.Setenv("GONGMCP_AI_GOVERNANCE_CONFIG", configPath)

	var captured postgres.RefreshServingDBOptions
	withRefreshServingDBFake(t, func(ctx context.Context, opts postgres.RefreshServingDBOptions) (*postgres.ServingDBRefreshResult, error) {
		captured = opts
		return &postgres.ServingDBRefreshResult{Backend: "postgres"}, nil
	})

	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"governance", "refresh-serving-db"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run(governance refresh-serving-db) code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if captured.Config == nil {
		t.Fatalf("expected fallback governance config to be loaded")
	}
}

func TestGovernanceRefreshServingDBSanitizesConnectionErrors(t *testing.T) {
	clearRefreshServingDBEnv(t)

	dir := t.TempDir()
	configPath := filepath.Join(dir, "ai-governance.yaml")
	if err := os.WriteFile(configPath, []byte(`
version: 1
lists:
  no_ai:
    customers:
      - name: "Refresh Synthetic Corp"
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var stdout, stderr bytes.Buffer
	sourceURL := "postgres://operator:synthetic-secret@127.0.0.1:1/gongctl_source"
	targetURL := "postgres://operator:synthetic-secret@127.0.0.1:1/gongctl_mcp"
	code := Run(context.Background(), []string{
		"governance", "refresh-serving-db",
		"--source", sourceURL,
		"--target", targetURL,
		"--config", configPath,
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("expected non-zero exit for unavailable source; stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	combined := stdout.String() + stderr.String()
	if !strings.Contains(combined, "source database connect failed") ||
		!strings.Contains(combined, "database connection, network path, or credentials are unavailable") {
		t.Fatalf("expected sanitized source database error; got stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	for _, leak := range []string{"operator", "synthetic-secret", "gongctl_source", "gongctl_mcp", sourceURL, targetURL} {
		if strings.Contains(combined, leak) {
			t.Fatalf("error output leaked %q: %s", leak, combined)
		}
	}
}

func TestGovernanceRefreshServingDBReportsSourceTranscriptSegmentTimeout(t *testing.T) {
	clearRefreshServingDBEnv(t)

	dir := t.TempDir()
	configPath := filepath.Join(dir, "ai-governance.yaml")
	if err := os.WriteFile(configPath, []byte(`
version: 1
lists:
  no_ai:
    customers:
      - name: "Refresh Synthetic Corp"
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	withRefreshServingDBFake(t, func(ctx context.Context, opts postgres.RefreshServingDBOptions) (*postgres.ServingDBRefreshResult, error) {
		return nil, servingRefreshTranscriptSegmentTimeoutError()
	})

	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{
		"governance", "refresh-serving-db",
		"--source", "postgres://operator:source-secret@localhost:5432/gongctl_source",
		"--target", "postgres://operator:target-secret@localhost:5432/gongctl_mcp",
		"--config", configPath,
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("expected timeout failure; stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	combined := stdout.String() + stderr.String()
	for _, want := range []string{
		"source transcript_segments copy timed out",
		"raise statement_timeout for the refresh role or session",
	} {
		if !strings.Contains(combined, want) {
			t.Fatalf("expected %q in output; got stdout=%q stderr=%q", want, stdout.String(), stderr.String())
		}
	}
	for _, leak := range []string{
		"canceling statement due to statement timeout",
		"57014",
		"inspect operator logs for details",
		"source-secret",
		"target-secret",
	} {
		if strings.Contains(combined, leak) {
			t.Fatalf("error output leaked or genericized %q: %s", leak, combined)
		}
	}
}

func TestGovernanceRefreshServingDBDoesNotPassThroughRawSourceTargetErrors(t *testing.T) {
	raw := fmt.Errorf("read source transcript_segments: %w", &pgconn.PgError{
		Code:    "57014",
		Message: "canceling statement due to statement timeout",
	})
	err := sanitizeRefreshServingDBError(raw)
	if err == nil {
		t.Fatal("expected sanitized error")
	}
	got := err.Error()
	if strings.Contains(got, "read source transcript_segments") || strings.Contains(got, "57014") || strings.Contains(got, "canceling statement") {
		t.Fatalf("raw source/target error leaked through sanitizer: %q", got)
	}
	if !strings.Contains(got, "inspect operator logs") {
		t.Fatalf("expected generic sanitized fallback, got %q", got)
	}
}

func TestGovernanceRefreshServingDBSanitizesMalformedURLs(t *testing.T) {
	clearRefreshServingDBEnv(t)

	dir := t.TempDir()
	configPath := filepath.Join(dir, "ai-governance.yaml")
	if err := os.WriteFile(configPath, []byte(`
version: 1
lists:
  no_ai:
    customers:
      - name: "Refresh Synthetic Corp"
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var stdout, stderr bytes.Buffer
	sourceURL := "postgres://operator:synthetic-secret@%zz/gongctl_source"
	targetURL := "postgres://operator:synthetic-secret@localhost/gongctl_mcp"
	code := Run(context.Background(), []string{
		"governance", "refresh-serving-db",
		"--source", sourceURL,
		"--target", targetURL,
		"--config", configPath,
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("expected non-zero exit for malformed source; stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	combined := stdout.String() + stderr.String()
	if !strings.Contains(combined, "source database is unavailable or invalid") {
		t.Fatalf("expected sanitized source database error; got stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	for _, leak := range []string{"operator", "synthetic-secret", "gongctl_source", "gongctl_mcp", sourceURL, targetURL} {
		if strings.Contains(combined, leak) {
			t.Fatalf("error output leaked %q: %s", leak, combined)
		}
	}
}

func TestGovernanceRefreshServingDBNoExclusionsWorksWithoutConfig(t *testing.T) {
	clearRefreshServingDBEnv(t)
	sourceURL := "postgres://operator:source-secret@localhost:5432/gongctl_source"
	targetURL := "postgres://operator:target-secret@localhost:5432/gongctl_mcp"
	t.Setenv("GONGCTL_SOURCE_DATABASE_URL", sourceURL)
	t.Setenv("GONGCTL_MCP_DATABASE_URL", targetURL)

	var captured postgres.RefreshServingDBOptions
	withRefreshServingDBFake(t, func(ctx context.Context, opts postgres.RefreshServingDBOptions) (*postgres.ServingDBRefreshResult, error) {
		captured = opts
		return &postgres.ServingDBRefreshResult{
			Backend:                "postgres",
			SuppressedCallCount:    0,
			NoGovernanceExclusions: true,
		}, nil
	})

	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{
		"governance", "refresh-serving-db",
		"--no-governance-exclusions",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run(governance refresh-serving-db --no-governance-exclusions) code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !captured.NoGovernanceExclusions || captured.Config != nil {
		t.Fatalf("unexpected refresh opts: %+v", captured)
	}
	if !strings.Contains(stdout.String(), `"no_governance_exclusions": true`) || !strings.Contains(stdout.String(), `"suppressed_call_count": 0`) {
		t.Fatalf("expected no-exclusions refresh JSON; got stdout=%q", stdout.String())
	}
}

func TestGovernanceRefreshServingDBRejectsConfigWithNoExclusions(t *testing.T) {
	clearRefreshServingDBEnv(t)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "ai-governance.yaml")
	if err := os.WriteFile(configPath, []byte(`
version: 1
lists:
  no_ai:
    customers:
      - name: "Refresh Synthetic Corp"
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{
		"governance", "refresh-serving-db",
		"--source", "postgres://h/a",
		"--target", "postgres://h/b",
		"--config", configPath,
		"--no-governance-exclusions",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("expected conflict failure; stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String()+stderr.String(), "cannot be used together") {
		t.Fatalf("expected conflict message; stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func clearRefreshServingDBEnv(t *testing.T) {
	t.Helper()
	t.Setenv("GONGCTL_SOURCE_DATABASE_URL", "")
	t.Setenv("GONGCTL_MCP_DATABASE_URL", "")
	t.Setenv("GONGCTL_AI_GOVERNANCE_CONFIG", "")
	t.Setenv("GONGMCP_AI_GOVERNANCE_CONFIG", "")
	t.Setenv("GONGCTL_NO_GOVERNANCE_EXCLUSIONS", "")
	t.Setenv("GONGMCP_NO_GOVERNANCE_EXCLUSIONS", "")
}

func withRefreshServingDBFake(t *testing.T, fn func(context.Context, postgres.RefreshServingDBOptions) (*postgres.ServingDBRefreshResult, error)) {
	t.Helper()
	original := refreshServingDB
	refreshServingDB = fn
	t.Cleanup(func() {
		refreshServingDB = original
	})
}

func servingRefreshTranscriptSegmentTimeoutError() error {
	pgErr := &pgconn.PgError{Code: "57014", Message: "canceling statement due to statement timeout"}
	phaseErr := &postgres.ServingRefreshPhaseError{
		Phase:  postgres.ServingRefreshPhaseCopy,
		Side:   postgres.ServingRefreshSideSource,
		Object: "transcript_segments",
		Cause:  postgres.ServingRefreshCauseStatementTimeout,
		Err:    pgErr,
	}
	return fmt.Errorf("copy filtered serving data: %w", phaseErr)
}

func TestGovernanceExportFilteredDBWritesPhysicalMCPDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "gong.db")
	outPath := filepath.Join(dir, "gong-governed.db")
	store := openCLITestStore(t, dbPath)

	if _, err := store.UpsertCall(context.Background(), mustMarshalJSON(t, map[string]any{
		"id":       "call-governance-export-cli-blocked",
		"title":    "Governance export CLI blocked",
		"started":  "2026-04-24T12:00:00Z",
		"duration": 1200,
		"context": []any{
			map[string]any{
				"objectType": "Account",
				"id":         "acct-governance-export-cli-blocked",
				"name":       "Export Synthetic Corp",
				"fields": []any{
					map[string]any{"name": "Name", "value": "Export Synthetic Corp"},
				},
			},
		},
	})); err != nil {
		t.Fatalf("upsert blocked governance call: %v", err)
	}
	if _, err := store.UpsertCall(context.Background(), mustMarshalJSON(t, map[string]any{
		"id":       "call-governance-export-cli-allowed",
		"title":    "Governance export CLI allowed",
		"started":  "2026-04-24T12:30:00Z",
		"duration": 900,
		"context": []any{
			map[string]any{
				"objectType": "Account",
				"id":         "acct-governance-export-cli-allowed",
				"name":       "Allowed Synthetic Corp",
				"fields": []any{
					map[string]any{"name": "Name", "value": "Allowed Synthetic Corp"},
				},
			},
		},
	})); err != nil {
		t.Fatalf("upsert allowed governance call: %v", err)
	}
	if _, err := store.UpsertCall(context.Background(), mustMarshalJSON(t, map[string]any{
		"id":      "call-governance-export-cli-email",
		"title":   "Governance export CLI email domain",
		"started": "2026-04-24T13:00:00Z",
		"parties": []any{
			map[string]any{"speakerId": "buyer-email", "emailAddress": "buyer@exportsynthetic.example"},
		},
		"context": []any{
			map[string]any{
				"objectType": "Account",
				"id":         "acct-governance-export-cli-email",
				"name":       "Allowed Email Corp",
				"fields": []any{
					map[string]any{"name": "Name", "value": "Allowed Email Corp"},
				},
			},
		},
	})); err != nil {
		t.Fatalf("upsert email governance call: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	configPath := filepath.Join(dir, "ai-governance.yaml")
	if err := os.WriteFile(configPath, []byte(`
version: 1
lists:
  no_ai:
    customers:
      - name: "Export Synthetic Corp"
        aliases: ["exportsynthetic.example"]
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"governance", "export-filtered-db", "--db", dbPath, "--config", configPath, "--out", outPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run(governance export-filtered-db) code=%d stderr=%q", code, stderr.String())
	}
	var response struct {
		SuppressedCallCount           int   `json:"suppressed_call_count"`
		DeletedCalls                  int64 `json:"deleted_calls"`
		RemainingSuppressedCandidates int64 `json:"remaining_suppressed_candidates"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("decode export response: %v", err)
	}
	if response.SuppressedCallCount != 2 || response.DeletedCalls != 2 || response.RemainingSuppressedCandidates != 0 {
		t.Fatalf("unexpected export response: %+v stdout=%s", response, stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Run(context.Background(), []string{"governance", "audit", "--db", outPath, "--config", configPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run(governance audit filtered) code=%d stderr=%q", code, stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, "matched=0") || !strings.Contains(got, "suppressed_calls=0") {
		t.Fatalf("filtered audit should not find blocked rows: %s", got)
	}
}
