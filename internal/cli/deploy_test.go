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
