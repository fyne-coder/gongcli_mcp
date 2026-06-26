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
	"time"

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

func TestDeployPostgresRefreshPassesStatementTimeoutFromFlag(t *testing.T) {
	clearDeployEnv(t)
	dir := t.TempDir()
	configPath := writeDeployGovernanceConfig(t, dir)
	sourceURL := "postgres://operator:source-secret@source.internal:5432/gongctl_source"
	targetURL := "postgres://operator:target-secret@target.internal:5432/gongctl_mcp"
	t.Setenv("GONGCTL_SOURCE_DATABASE_URL", sourceURL)
	t.Setenv("GONGCTL_MCP_DATABASE_URL", targetURL)
	t.Setenv("GONGCTL_AI_GOVERNANCE_CONFIG", configPath)

	var captured postgres.RefreshServingDBOptions
	withDeployFakes(t,
		func(ctx context.Context, databaseURL string) (*postgres.ReadModelStatus, error) {
			return &postgres.ReadModelStatus{ModelName: "call_facts", Ready: true}, nil
		},
		func(ctx context.Context, opts postgres.RefreshServingDBOptions) (*postgres.ServingDBRefreshResult, error) {
			captured = opts
			return &postgres.ServingDBRefreshResult{Backend: "postgres"}, nil
		},
		func(ctx context.Context, databaseURL string, params postgres.ScopedReaderGrantSQLParams) (string, error) {
			return "-- grant sql", nil
		},
	)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{
		"deploy", "postgres-refresh",
		"--statement-timeout", "45m",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run(deploy postgres-refresh --statement-timeout) code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if captured.StatementTimeout != 45*time.Minute {
		t.Fatalf("refresh StatementTimeout=%s want 45m", captured.StatementTimeout)
	}
	var response deployPostgresRefreshResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("unmarshal deploy response: %v", err)
	}
	if response.StatementTimeout != "45m" {
		t.Fatalf("response.StatementTimeout=%q want 45m", response.StatementTimeout)
	}
}

func TestDeployPostgresRefreshPassesStatementTimeoutFromEnv(t *testing.T) {
	clearDeployEnv(t)
	dir := t.TempDir()
	configPath := writeDeployGovernanceConfig(t, dir)
	t.Setenv("GONGCTL_SOURCE_DATABASE_URL", "postgres://operator:source-secret@source.internal:5432/gongctl_source")
	t.Setenv("GONGCTL_MCP_DATABASE_URL", "postgres://operator:target-secret@target.internal:5432/gongctl_mcp")
	t.Setenv("GONGCTL_AI_GOVERNANCE_CONFIG", configPath)
	t.Setenv("GONGCTL_REFRESH_STATEMENT_TIMEOUT", "20m")

	var captured postgres.RefreshServingDBOptions
	withDeployFakes(t,
		func(ctx context.Context, databaseURL string) (*postgres.ReadModelStatus, error) {
			return &postgres.ReadModelStatus{ModelName: "call_facts", Ready: true}, nil
		},
		func(ctx context.Context, opts postgres.RefreshServingDBOptions) (*postgres.ServingDBRefreshResult, error) {
			captured = opts
			return &postgres.ServingDBRefreshResult{Backend: "postgres"}, nil
		},
		func(ctx context.Context, databaseURL string, params postgres.ScopedReaderGrantSQLParams) (string, error) {
			return "-- grant sql", nil
		},
	)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"deploy", "postgres-refresh"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run(deploy postgres-refresh) code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if captured.StatementTimeout != 20*time.Minute {
		t.Fatalf("refresh StatementTimeout=%s want 20m", captured.StatementTimeout)
	}
}

func TestDeployPostgresRefreshStatementTimeoutFlagOverridesEnv(t *testing.T) {
	clearDeployEnv(t)
	dir := t.TempDir()
	configPath := writeDeployGovernanceConfig(t, dir)
	t.Setenv("GONGCTL_SOURCE_DATABASE_URL", "postgres://operator:source-secret@source.internal:5432/gongctl_source")
	t.Setenv("GONGCTL_MCP_DATABASE_URL", "postgres://operator:target-secret@target.internal:5432/gongctl_mcp")
	t.Setenv("GONGCTL_AI_GOVERNANCE_CONFIG", configPath)
	t.Setenv("GONGCTL_REFRESH_STATEMENT_TIMEOUT", "20m")

	var captured postgres.RefreshServingDBOptions
	withDeployFakes(t,
		func(ctx context.Context, databaseURL string) (*postgres.ReadModelStatus, error) {
			return &postgres.ReadModelStatus{ModelName: "call_facts", Ready: true}, nil
		},
		func(ctx context.Context, opts postgres.RefreshServingDBOptions) (*postgres.ServingDBRefreshResult, error) {
			captured = opts
			return &postgres.ServingDBRefreshResult{Backend: "postgres"}, nil
		},
		func(ctx context.Context, databaseURL string, params postgres.ScopedReaderGrantSQLParams) (string, error) {
			return "-- grant sql", nil
		},
	)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{
		"deploy", "postgres-refresh",
		"--statement-timeout", "10m",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run(deploy postgres-refresh) code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if captured.StatementTimeout != 10*time.Minute {
		t.Fatalf("refresh StatementTimeout=%s want 10m", captured.StatementTimeout)
	}
}

func TestDeployPostgresRefreshRejectsInvalidStatementTimeoutBeforeRefresh(t *testing.T) {
	clearDeployEnv(t)
	dir := t.TempDir()
	configPath := writeDeployGovernanceConfig(t, dir)
	t.Setenv("GONGCTL_SOURCE_DATABASE_URL", "postgres://operator:source-secret@source.internal:5432/gongctl_source")
	t.Setenv("GONGCTL_MCP_DATABASE_URL", "postgres://operator:target-secret@target.internal:5432/gongctl_mcp")
	t.Setenv("GONGCTL_AI_GOVERNANCE_CONFIG", configPath)

	refreshCalled := false
	withDeployFakes(t,
		func(ctx context.Context, databaseURL string) (*postgres.ReadModelStatus, error) {
			t.Fatal("read model rebuild should not run before timeout validation")
			return nil, nil
		},
		func(ctx context.Context, opts postgres.RefreshServingDBOptions) (*postgres.ServingDBRefreshResult, error) {
			refreshCalled = true
			return nil, nil
		},
		nil,
	)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{
		"deploy", "postgres-refresh",
		"--statement-timeout", "not-a-duration",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("expected invalid timeout failure; stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if refreshCalled {
		t.Fatal("refresh should not run for invalid timeout")
	}
	combined := stdout.String() + stderr.String()
	if !strings.Contains(combined, "failed before connect") || !strings.Contains(combined, "not a valid duration") {
		t.Fatalf("expected sanitized invalid timeout message; got %q", combined)
	}
	for _, leak := range []string{"source-secret", "target-secret", "source.internal", "target.internal", "postgres://"} {
		if strings.Contains(combined, leak) {
			t.Fatalf("invalid timeout error leaked %q: %s", leak, combined)
		}
	}
}

func TestDeployPostgresRefreshRejectsZeroStatementTimeoutBeforeRefresh(t *testing.T) {
	clearDeployEnv(t)
	dir := t.TempDir()
	configPath := writeDeployGovernanceConfig(t, dir)
	t.Setenv("GONGCTL_SOURCE_DATABASE_URL", "postgres://operator:source-secret@source.internal:5432/gongctl_source")
	t.Setenv("GONGCTL_MCP_DATABASE_URL", "postgres://operator:target-secret@target.internal:5432/gongctl_mcp")
	t.Setenv("GONGCTL_AI_GOVERNANCE_CONFIG", configPath)

	refreshCalled := false
	withDeployFakes(t,
		func(ctx context.Context, databaseURL string) (*postgres.ReadModelStatus, error) {
			t.Fatal("read model rebuild should not run before timeout validation")
			return nil, nil
		},
		func(ctx context.Context, opts postgres.RefreshServingDBOptions) (*postgres.ServingDBRefreshResult, error) {
			refreshCalled = true
			return nil, nil
		},
		nil,
	)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{
		"deploy", "postgres-refresh",
		"--statement-timeout", "0",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("expected zero timeout failure; stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if refreshCalled {
		t.Fatal("refresh should not run for zero timeout")
	}
	combined := stdout.String() + stderr.String()
	if !strings.Contains(combined, "greater than zero") {
		t.Fatalf("expected zero timeout rejection; got %q", combined)
	}
	for _, leak := range []string{"source-secret", "target-secret", "source.internal", "postgres://"} {
		if strings.Contains(combined, leak) {
			t.Fatalf("zero timeout error leaked %q: %s", leak, combined)
		}
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
	if response.StatementTimeout != "" {
		t.Fatalf("expected unset statement_timeout in backward-compatible output; got %q", response.StatementTimeout)
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
		"source transcript_segments copy timed out",
		"raise statement_timeout for the refresh role or session",
	} {
		if !strings.Contains(combined, want) {
			t.Fatalf("expected %q in output; got %q", want, combined)
		}
	}
	var response deployPostgresRefreshResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("unmarshal deploy failure response: %v; stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}
	if len(response.Steps) != 2 {
		t.Fatalf("steps=%d want 2: %+v", len(response.Steps), response.Steps)
	}
	failed := response.Steps[1]
	if failed.Name != "serving_refresh" || failed.Status != "fail" || !failed.RerunSafe {
		t.Fatalf("unexpected failed step: %+v", failed)
	}
	if failed.Phase != "copy" || failed.Side != "source" || failed.Object != "transcript_segments" || failed.Kind != "statement_timeout" {
		t.Fatalf("missing serving refresh metadata: %+v", failed)
	}
	if failed.Detail == "" || failed.ServingDBState == "" || len(failed.NextActions) == 0 {
		t.Fatalf("missing operator guidance: %+v", failed)
	}
	if strings.Contains(stderr.String(), failed.Detail) {
		t.Fatalf("stderr should stay short and leave detail in JSON: stderr=%q", stderr.String())
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
	var response deployPostgresRefreshResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("unmarshal deploy failure response: %v; stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}
	if len(response.Steps) != 1 {
		t.Fatalf("steps=%d want 1: %+v", len(response.Steps), response.Steps)
	}
	failed := response.Steps[0]
	if failed.Name != "source_read_model" || failed.Status != "fail" || failed.Kind != "connection_failed" || !failed.RerunSafe {
		t.Fatalf("unexpected source failed step: %+v", failed)
	}
	if failed.Detail == "" || len(failed.NextActions) == 0 {
		t.Fatalf("missing source failure guidance: %+v", failed)
	}
}

func TestDeployPostgresRefreshWritesFailedStepJSONForGrantFailure(t *testing.T) {
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
			return &postgres.ServingDBRefreshResult{
				Backend:               "postgres",
				ServingRefreshID:      12,
				SourceCalls:           4,
				TargetCalls:           4,
				SuppressedCallCount:   0,
				PolicyConfigSHA256:    "policy-sha",
				TargetDataFingerprint: "target-fingerprint",
			}, nil
		},
		func(ctx context.Context, databaseURL string, params postgres.ScopedReaderGrantSQLParams) (string, error) {
			return "", errors.New("permission denied for relation calls with target-secret")
		},
	)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"deploy", "postgres-refresh"}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("expected grant failure; stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	combined := stdout.String() + stderr.String()
	for _, leak := range []string{"source-secret", "target-secret", "source.internal", "target.internal", "postgres://"} {
		if strings.Contains(combined, leak) {
			t.Fatalf("deploy grant failure leaked %q: %s", leak, combined)
		}
	}
	var response deployPostgresRefreshResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("unmarshal deploy failure response: %v; stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}
	if response.Result == nil || len(response.Steps) != 3 {
		t.Fatalf("expected result and 3 steps: %+v", response)
	}
	failed := response.Steps[2]
	if failed.Name != "reader_grants" || failed.Status != "fail" || failed.Kind != "permission_denied" || !failed.RerunSafe {
		t.Fatalf("unexpected grant failed step: %+v", failed)
	}
	if failed.Detail == "" || len(failed.NextActions) == 0 {
		t.Fatalf("missing grant failure guidance: %+v", failed)
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
	if !hasDeployCheck(response.Checks, "source_database_connectivity", postgresdeploy.CheckFail) {
		t.Fatalf("missing source connectivity failure check: %+v", response.Checks)
	}
	if !hasDeployCheck(response.Checks, "serving_database_connectivity", postgresdeploy.CheckFail) {
		t.Fatalf("missing serving connectivity failure check: %+v", response.Checks)
	}
	if !hasDeployCheck(response.Checks, "serving_refresh_marker", postgresdeploy.CheckFail) {
		t.Fatalf("missing marker failure check: %+v", response.Checks)
	}
	if !hasDeployCheck(response.Checks, "statement_timeout", postgresdeploy.CheckPass) {
		t.Fatalf("missing statement_timeout pass check: %+v", response.Checks)
	}
	if !hasDeployCheck(response.Checks, "scoped_reader_role_input", postgresdeploy.CheckPass) {
		t.Fatalf("missing scoped_reader_role_input pass check: %+v", response.Checks)
	}
	if !hasDeployCheck(response.Checks, "scoped_reader_grant_sql", postgresdeploy.CheckPass) {
		t.Fatalf("missing scoped_reader_grant_sql pass check: %+v", response.Checks)
	}
	assertDoctorOutputSanitized(t, stdout.String()+stderr.String())
}

func TestDoctorPostgresDeployValidStatementTimeoutPassesWithoutConnecting(t *testing.T) {
	clearDeployEnv(t)
	t.Setenv("GONGCTL_REFRESH_STATEMENT_TIMEOUT", "45m")

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
	check := findDeployCheck(response.Checks, "statement_timeout")
	if check == nil || check.Status != postgresdeploy.CheckPass {
		t.Fatalf("expected passing statement_timeout check: %+v", check)
	}
	if check.Evidence == nil || len(check.Evidence) != 1 || check.Evidence[0].Value != "45m" {
		t.Fatalf("expected normalized timeout evidence; check=%+v", check)
	}
	assertDoctorOutputSanitized(t, stdout.String()+stderr.String())
}

func TestDoctorPostgresDeployInvalidStatementTimeoutFailsWithoutConnecting(t *testing.T) {
	clearDeployEnv(t)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{
		"doctor", "postgres-deploy",
		"--preset", "business-workbench",
		"--statement-timeout", "0",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run(doctor postgres-deploy) code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var response diagnosePostgresDeployResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("unmarshal doctor response: %v", err)
	}
	check := findDeployCheck(response.Checks, "statement_timeout")
	if check == nil || check.Status != postgresdeploy.CheckFail || check.ErrorKind != "statement_timeout_invalid" {
		t.Fatalf("expected failing statement_timeout check: %+v", check)
	}
	if !hasDeployCheck(response.Checks, "scoped_reader_role_input", postgresdeploy.CheckPass) {
		t.Fatalf("expected role/database checks to continue after timeout failure: %+v", response.Checks)
	}
	assertDoctorOutputSanitized(t, stdout.String()+stderr.String())
}

func TestDoctorPostgresDeployUnknownPresetStillReturnsActionableChecks(t *testing.T) {
	clearDeployEnv(t)
	t.Setenv("GONGCTL_REFRESH_STATEMENT_TIMEOUT", "30m")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{
		"doctor", "postgres-deploy",
		"--preset", "unknown-preset",
		"--role", "gongmcp_business_workbench_reader",
		"--database", "gongctl_mcp",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run(doctor postgres-deploy) code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var response diagnosePostgresDeployResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("unmarshal doctor response: %v", err)
	}
	if !hasDeployCheck(response.Checks, "tool_preset", postgresdeploy.CheckFail) {
		t.Fatalf("missing unknown preset failure: %+v", response.Checks)
	}
	if !hasDeployCheck(response.Checks, "statement_timeout", postgresdeploy.CheckPass) {
		t.Fatalf("missing statement_timeout pass for unknown preset: %+v", response.Checks)
	}
	if !hasDeployCheck(response.Checks, "scoped_reader_role_input", postgresdeploy.CheckPass) {
		t.Fatalf("missing scoped_reader_role_input pass for unknown preset: %+v", response.Checks)
	}
	if !hasDeployCheck(response.Checks, "scoped_reader_grant_sql", postgresdeploy.CheckFail) {
		t.Fatalf("missing scoped_reader_grant_sql failure for unknown preset: %+v", response.Checks)
	}
	assertDoctorOutputSanitized(t, stdout.String()+stderr.String())
}

func TestDoctorPostgresDeployChecksSourceReadModelWhenConnected(t *testing.T) {
	clearDeployEnv(t)
	sourceURL := "postgres://operator:source-secret@source.internal:5432/gongctl_source"
	targetURL := "postgres://operator:target-secret@target.internal:5432/gongctl_mcp"
	t.Setenv("GONGCTL_SOURCE_DATABASE_URL", sourceURL)
	t.Setenv("GONGCTL_MCP_DATABASE_URL", targetURL)

	withDoctorFakes(t, func(ctx context.Context, databaseURL string) (doctorPostgresStatusStore, error) {
		switch databaseURL {
		case sourceURL:
			return &fakeDoctorPostgresStore{
				readModel: &postgres.ReadModelStatus{ModelName: "builtin_call_facts", Ready: true, CallCount: 12},
			}, nil
		case targetURL:
			return &fakeDoctorPostgresStore{markerErr: errors.New("no marker in fake")}, nil
		default:
			return nil, errors.New("unexpected database URL")
		}
	})

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
	if !hasDeployCheck(response.Checks, "source_database_connectivity", postgresdeploy.CheckPass) {
		t.Fatalf("missing source connectivity pass: %+v", response.Checks)
	}
	if !hasDeployCheck(response.Checks, "source_read_model", postgresdeploy.CheckPass) {
		t.Fatalf("missing source read model pass: %+v", response.Checks)
	}
	assertDoctorOutputSanitized(t, stdout.String()+stderr.String())
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
	t.Setenv("GONGCTL_REFRESH_STATEMENT_TIMEOUT", "")
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

func assertDoctorOutputSanitized(t *testing.T, combined string) {
	t.Helper()
	for _, leak := range []string{
		"postgres://",
		"source-secret",
		"target-secret",
		"source.internal",
		"target.internal",
		"GRANT CONNECT",
		"BEGIN;",
	} {
		if strings.Contains(combined, leak) {
			t.Fatalf("doctor output leaked %q: %s", leak, combined)
		}
	}
}

type fakeDoctorPostgresStore struct {
	readModel *postgres.ReadModelStatus
	readErr   error
	marker    *postgres.ServingDBRefreshMarker
	markerErr error
}

func (f *fakeDoctorPostgresStore) Close() error { return nil }

func (f *fakeDoctorPostgresStore) ReadModelStatus(context.Context) (*postgres.ReadModelStatus, error) {
	return f.readModel, f.readErr
}

func (f *fakeDoctorPostgresStore) LatestServingRefreshMarker(context.Context) (*postgres.ServingDBRefreshMarker, error) {
	return f.marker, f.markerErr
}

func withDoctorFakes(t *testing.T, open func(context.Context, string) (doctorPostgresStatusStore, error)) {
	t.Helper()
	originalOpen := deployOpenPostgresStatus
	deployOpenPostgresStatus = open
	t.Cleanup(func() {
		deployOpenPostgresStatus = originalOpen
	})
}
