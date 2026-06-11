package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	postgresdeploy "github.com/fyne-coder/gongcli_mcp/internal/deploy/postgres"
	"github.com/fyne-coder/gongcli_mcp/internal/store/postgres"
)

func TestDeployPostgresRefreshNoExclusionsWorksWithoutConfig(t *testing.T) {
	clearDeployEnv(t)
	sourceURL := "postgres://operator:source-secret@source.internal:5432/gongctl_source"
	targetURL := "postgres://operator:target-secret@target.internal:5432/gongctl_mcp"
	t.Setenv("GONGCTL_SOURCE_DATABASE_URL", sourceURL)
	t.Setenv("GONGCTL_MCP_DATABASE_URL", targetURL)

	var captured postgres.RefreshServingDBOptions
	withDeployFakes(t,
		func(ctx context.Context, databaseURL string) (*postgres.ReadModelStatus, error) {
			return &postgres.ReadModelStatus{ModelName: "call_facts", Ready: true}, nil
		},
		func(ctx context.Context, opts postgres.RefreshServingDBOptions) (*postgres.ServingDBRefreshResult, error) {
			captured = opts
			return &postgres.ServingDBRefreshResult{
				Backend:                "postgres",
				SuppressedCallCount:    0,
				NoGovernanceExclusions: true,
			}, nil
		},
		func(ctx context.Context, databaseURL string, params postgres.ScopedReaderGrantSQLParams) (string, error) {
			return "-- grant sql", nil
		},
	)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"deploy", "postgres-refresh", "--no-governance-exclusions"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run(deploy postgres-refresh --no-governance-exclusions) code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !captured.NoGovernanceExclusions || captured.Config != nil {
		t.Fatalf("unexpected refresh opts: %+v", captured)
	}
	if !strings.Contains(stdout.String(), `"no_governance_exclusions": true`) || !strings.Contains(stdout.String(), `"suppressed_call_count": 0`) {
		t.Fatalf("expected no-exclusions deploy JSON; got stdout=%q", stdout.String())
	}
}

func TestDeployPostgresRefreshRejectsConfigWithNoExclusions(t *testing.T) {
	clearDeployEnv(t)
	dir := t.TempDir()
	configPath := writeDeployGovernanceConfig(t, dir)
	t.Setenv("GONGCTL_SOURCE_DATABASE_URL", "postgres://operator:source-secret@source.internal:5432/gongctl_source")
	t.Setenv("GONGCTL_MCP_DATABASE_URL", "postgres://operator:target-secret@target.internal:5432/gongctl_mcp")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{
		"deploy", "postgres-refresh",
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

func TestDeployPostgresRefreshSanitizesConfigLoadFailure(t *testing.T) {
	clearDeployEnv(t)
	configPath := filepath.Join(t.TempDir(), "missing-ai-governance.yaml")
	t.Setenv("GONGCTL_SOURCE_DATABASE_URL", "postgres://operator:source-secret@source.internal:5432/gongctl_source")
	t.Setenv("GONGCTL_MCP_DATABASE_URL", "postgres://operator:target-secret@target.internal:5432/gongctl_mcp")
	t.Setenv("GONGCTL_AI_GOVERNANCE_CONFIG", configPath)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"deploy", "postgres-refresh"}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("expected config load failure; stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	combined := stdout.String() + stderr.String()
	if !strings.Contains(combined, "governance config could not be loaded") {
		t.Fatalf("expected sanitized config load message; stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if strings.Contains(combined, configPath) || strings.Contains(combined, "source-secret") || strings.Contains(combined, "target-secret") {
		t.Fatalf("config load failure leaked sensitive values: %s", combined)
	}
}

func TestDeployPostgresRefreshUsesEnvDefaultsAndOmitsURLs(t *testing.T) {
	clearDeployEnv(t)
	dir := t.TempDir()
	configPath := writeDeployGovernanceConfig(t, dir)
	sourceURL := "postgres://operator:source-secret@source.internal:5432/gongctl_source"
	targetURL := "postgres://operator:target-secret@target.internal:5432/gongctl_mcp"
	t.Setenv("GONGCTL_SOURCE_DATABASE_URL", sourceURL)
	t.Setenv("GONGCTL_MCP_DATABASE_URL", targetURL)
	t.Setenv("GONGCTL_AI_GOVERNANCE_CONFIG", configPath)

	var rebuildSource string
	withDeployFakes(t,
		func(ctx context.Context, databaseURL string) (*postgres.ReadModelStatus, error) {
			rebuildSource = databaseURL
			return &postgres.ReadModelStatus{ModelName: "call_facts", Ready: true, CallCount: 7}, nil
		},
		func(ctx context.Context, opts postgres.RefreshServingDBOptions) (*postgres.ServingDBRefreshResult, error) {
			if opts.SourceURL != sourceURL || opts.TargetURL != targetURL || opts.Config == nil {
				t.Fatalf("unexpected refresh opts: %+v", opts)
			}
			return &postgres.ServingDBRefreshResult{
				Backend:               "postgres",
				ServingRefreshID:      11,
				RefreshedAt:           "2026-05-23T12:00:00Z",
				SourceCalls:           7,
				TargetCalls:           6,
				SuppressedCallCount:   1,
				PolicyConfigSHA256:    "policy-sha",
				SourceDataFingerprint: "source-fingerprint",
				TargetDataFingerprint: "target-fingerprint",
			}, nil
		},
		func(ctx context.Context, databaseURL string, params postgres.ScopedReaderGrantSQLParams) (string, error) {
			if databaseURL != targetURL {
				t.Fatalf("grant apply URL=%q want target URL", databaseURL)
			}
			if params.RoleName != "gongmcp_business_workbench_reader" || params.DatabaseName != "gongctl_mcp" {
				t.Fatalf("unexpected grant params: %+v", params)
			}
			if len(params.Allowlist) == 0 {
				t.Fatal("expected non-empty allowlist")
			}
			return "-- grant sql", nil
		},
	)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"deploy", "postgres-refresh"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run(deploy postgres-refresh) code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if rebuildSource != sourceURL {
		t.Fatalf("rebuild source URL=%q want source URL", rebuildSource)
	}
	output := stdout.String()
	for _, leak := range []string{"source-secret", "target-secret", "source.internal", "target.internal", "postgres://"} {
		if strings.Contains(output+stderr.String(), leak) {
			t.Fatalf("deploy output leaked %q: stdout=%q stderr=%q", leak, stdout.String(), stderr.String())
		}
	}
	var response deployPostgresRefreshResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("unmarshal deploy response: %v", err)
	}
	if response.Preset != "business-workbench" || response.Result.ServingRefreshID != 11 || response.GrantSQLSHA256 == "" {
		t.Fatalf("unexpected deploy response: %+v", response)
	}
	if len(response.Steps) != 3 {
		t.Fatalf("steps=%d want 3: %+v", len(response.Steps), response.Steps)
	}
}

func TestDeployPostgresRefreshReportsSourceTranscriptSegmentTimeout(t *testing.T) {
	clearDeployEnv(t)
	dir := t.TempDir()
	configPath := writeDeployGovernanceConfig(t, dir)
	t.Setenv("GONGCTL_SOURCE_DATABASE_URL", "postgres://operator:source-secret@source.internal:5432/gongctl_source")
	t.Setenv("GONGCTL_MCP_DATABASE_URL", "postgres://operator:target-secret@target.internal:5432/gongctl_mcp")
	t.Setenv("GONGCTL_AI_GOVERNANCE_CONFIG", configPath)
	withDeployFakes(t,
		func(ctx context.Context, databaseURL string) (*postgres.ReadModelStatus, error) {
			return &postgres.ReadModelStatus{ModelName: "call_facts", Ready: true}, nil
		},
		func(ctx context.Context, opts postgres.RefreshServingDBOptions) (*postgres.ServingDBRefreshResult, error) {
			return nil, servingRefreshTranscriptSegmentTimeoutError()
		},
		nil,
	)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"deploy", "postgres-refresh"}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("expected timeout failure; stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	combined := stdout.String() + stderr.String()
	for _, want := range []string{
		"deploy postgres-refresh failed at serving_refresh",
		"source transcript_segments copy exceeded the Postgres statement_timeout",
		"raise statement_timeout for the refresh role or session",
	} {
		if !strings.Contains(combined, want) {
			t.Fatalf("expected %q in output; got %q", want, combined)
		}
	}
	for _, leak := range []string{
		"canceling statement due to statement timeout",
		"57014",
		"inspect operator logs for the sanitized underlying error",
		"database connection, network path, or credentials are invalid",
		"source-secret",
		"target-secret",
	} {
		if strings.Contains(combined, leak) {
			t.Fatalf("error output leaked or genericized %q: %s", leak, combined)
		}
	}
}

func TestDeployPostgresRefreshSanitizesFailures(t *testing.T) {
	clearDeployEnv(t)
	dir := t.TempDir()
	configPath := writeDeployGovernanceConfig(t, dir)
	t.Setenv("GONGCTL_SOURCE_DATABASE_URL", "postgres://operator:source-secret@source.internal:5432/gongctl_source")
	t.Setenv("GONGCTL_MCP_DATABASE_URL", "postgres://operator:target-secret@target.internal:5432/gongctl_mcp")
	t.Setenv("GONGCTL_AI_GOVERNANCE_CONFIG", configPath)
	withDeployFakes(t,
		func(ctx context.Context, databaseURL string) (*postgres.ReadModelStatus, error) {
			return nil, errors.New("dial tcp target.internal source-secret target-secret")
		},
		nil,
		nil,
	)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"deploy", "postgres-refresh"}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("expected failure; stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	combined := stdout.String() + stderr.String()
	for _, leak := range []string{"source-secret", "target-secret", "source.internal", "target.internal", "postgres://"} {
		if strings.Contains(combined, leak) {
			t.Fatalf("deploy failure leaked %q: %s", leak, combined)
		}
	}
	if !strings.Contains(combined, "deploy postgres-refresh failed at source_read_model") {
		t.Fatalf("expected step-specific failure, got %q", combined)
	}
}

func TestDoctorPostgresDeployReportsPresetAndMissingMarkerInputs(t *testing.T) {
	clearDeployEnv(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"doctor", "postgres-deploy", "--preset", "business-workbench"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run(doctor postgres-deploy) code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var response diagnosePostgresDeployResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("unmarshal doctor response: %v", err)
	}
	if response.Preset != "business-workbench" {
		t.Fatalf("preset=%q", response.Preset)
	}
	if !hasDeployCheck(response.Checks, "tool_preset", postgresdeploy.CheckPass) {
		t.Fatalf("missing passing tool_preset check: %+v", response.Checks)
	}
	if !hasDeployCheck(response.Checks, "serving_refresh_marker", postgresdeploy.CheckFail) {
		t.Fatalf("missing marker failure check: %+v", response.Checks)
	}
}

func TestSyncStatusPresetValidationForSQLiteWarns(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "gong.db")
	store := openCLITestStore(t, dbPath)
	store.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"sync", "status", "--db", dbPath, "--preset", "business-workbench"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run(sync status --preset) code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var response syncStatusResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("unmarshal sync status: %v", err)
	}
	if response.PresetValidation == nil || response.PresetValidation.Status != postgresdeploy.CheckWarn {
		t.Fatalf("unexpected preset validation: %+v", response.PresetValidation)
	}
}

func writeDeployGovernanceConfig(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "ai-governance.yaml")
	if err := os.WriteFile(path, []byte(`
version: 1
lists:
  no_ai:
    customers:
      - name: "Deploy Synthetic Corp"
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func clearDeployEnv(t *testing.T) {
	t.Helper()
	t.Setenv("GONGCTL_SOURCE_DATABASE_URL", "")
	t.Setenv("GONGCTL_MCP_DATABASE_URL", "")
	t.Setenv("GONGCTL_AI_GOVERNANCE_CONFIG", "")
	t.Setenv("GONGMCP_AI_GOVERNANCE_CONFIG", "")
	t.Setenv("GONGCTL_NO_GOVERNANCE_EXCLUSIONS", "")
	t.Setenv("GONGMCP_NO_GOVERNANCE_EXCLUSIONS", "")
	t.Setenv("GONGMCP_TOOL_PRESET", "")
	t.Setenv("GONGMCP_READER_ROLE", "")
	t.Setenv("GONGMCP_DATABASE_NAME", "")
	t.Setenv("GONGCTL_MCP_DB", "")
	t.Setenv("GONG_DATABASE_URL", "")
	t.Setenv("DATABASE_URL", "")
}

func withDeployFakes(
	t *testing.T,
	rebuild func(context.Context, string) (*postgres.ReadModelStatus, error),
	refresh func(context.Context, postgres.RefreshServingDBOptions) (*postgres.ServingDBRefreshResult, error),
	applyGrants func(context.Context, string, postgres.ScopedReaderGrantSQLParams) (string, error),
) {
	t.Helper()
	originalRebuild := deployRebuildReadModel
	originalRefresh := deployRefreshServingDB
	originalApply := deployApplyScopedReaderGrants
	if rebuild != nil {
		deployRebuildReadModel = rebuild
	}
	if refresh != nil {
		deployRefreshServingDB = refresh
	}
	if applyGrants != nil {
		deployApplyScopedReaderGrants = applyGrants
	}
	t.Cleanup(func() {
		deployRebuildReadModel = originalRebuild
		deployRefreshServingDB = originalRefresh
		deployApplyScopedReaderGrants = originalApply
	})
}

func hasDeployCheck(checks []postgresdeploy.Check, name string, status postgresdeploy.CheckStatus) bool {
	for _, check := range checks {
		if check.Name == name && check.Status == status {
			return true
		}
	}
	return false
}
