package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	postgresdeploy "github.com/fyne-coder/gongcli_mcp/internal/deploy/postgres"
	"github.com/fyne-coder/gongcli_mcp/internal/mcp"
	"github.com/fyne-coder/gongcli_mcp/internal/store/postgres"
)

const defaultPostgresDeployMarkerMaxAge = 24 * time.Hour
const deploySensitiveDataWarning = "This output contains sanitized deployment counts, checks, fingerprints, and grant hashes. Protect the underlying source database, serving database, secrets, governance config, and operator logs separately."

var deployRefreshServingDB = postgres.RefreshServingDB
var deployRebuildReadModel = rebuildPostgresReadModel
var deployApplyScopedReaderGrants = postgres.ApplyScopedReaderGrants
var deployOpenPostgresStatus = postgres.OpenStatus

func (a *app) deploy(ctx context.Context, args []string) error {
	if len(args) == 0 {
		fmt.Fprintln(a.err, "usage: gongctl deploy [postgres-refresh]")
		return errUsage
	}
	switch args[0] {
	case "postgres-refresh":
		return a.deployPostgresRefresh(ctx, args[1:])
	default:
		fmt.Fprintf(a.err, "unknown deploy command %q\n", args[0])
		return errUsage
	}
}

func (a *app) deployPostgresRefresh(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("deploy postgres-refresh", flag.ContinueOnError)
	fs.SetOutput(a.err)
	sourceURL := fs.String("source", "", "Postgres source/operator database URL, e.g. $GONGCTL_SOURCE_DATABASE_URL")
	targetURL := fs.String("target", "", "Postgres redacted serving database URL, e.g. $GONGCTL_MCP_DATABASE_URL")
	configPath := fs.String("config", "", "AI governance YAML config path")
	noGovernanceExclusions := fs.Bool("no-governance-exclusions", false, "declare that no customer governance exclusions exist; do not pass --config")
	preset := fs.String("preset", "", "MCP tool preset for reader grant validation")
	roleName := fs.String("role", "", "existing Postgres reader role name")
	databaseName := fs.String("database", "", "Postgres database name for reader grants")
	skipReadModel := fs.Bool("skip-read-model", false, "skip rebuilding the source read model before serving refresh")
	skipGrants := fs.Bool("skip-grants", false, "skip scoped reader grant reconciliation")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	if fs.NArg() != 0 {
		return errUsage
	}

	source := refreshServingDBInput(*sourceURL, "GONGCTL_SOURCE_DATABASE_URL")
	target := refreshServingDBInput(*targetURL, "GONGCTL_MCP_DATABASE_URL")
	selectedPreset := deployInputDefault(*preset, "business-workbench", "GONGMCP_TOOL_PRESET")
	role := deployInputDefault(*roleName, "gongmcp_business_workbench_reader", "GONGMCP_READER_ROLE")
	database := deployInputDefault(*databaseName, "gongctl_mcp", "GONGMCP_DATABASE_NAME", "GONGCTL_MCP_DB")

	if source == "" {
		return errors.New("deploy postgres-refresh failed before connect: --source is required or set GONGCTL_SOURCE_DATABASE_URL")
	}
	if target == "" {
		return errors.New("deploy postgres-refresh failed before connect: --target is required or set GONGCTL_MCP_DATABASE_URL")
	}
	contract, err := resolveGovernanceServingContract(*configPath, *noGovernanceExclusions)
	if err != nil {
		if isGovernanceConfigLoadError(err) {
			return fmt.Errorf("deploy postgres-refresh failed before connect: governance config could not be loaded")
		}
		return fmt.Errorf("deploy postgres-refresh failed before connect: %w", err)
	}
	allowlist, err := mcp.ExpandToolPresetReaderGrantTools(selectedPreset)
	if err != nil {
		return fmt.Errorf("deploy postgres-refresh failed before connect: %w", err)
	}

	response := deployPostgresRefreshResponse{
		Backend:                "postgres",
		Preset:                 selectedPreset,
		Role:                   role,
		Database:               database,
		NoGovernanceExclusions: contract.NoGovernanceExclusions,
		SensitiveDataWarning:   deploySensitiveDataWarning,
		Steps:                  []deployStep{},
	}

	if *skipReadModel {
		response.Steps = append(response.Steps, deployStep{Name: "source_read_model", Status: "skipped", Message: "source read model rebuild skipped by operator flag"})
	} else {
		readModel, err := deployRebuildReadModel(ctx, source)
		if err != nil {
			return a.deployPostgresRefreshStepFailure(response, "source_read_model", "source database unavailable or read model rebuild failed", err)
		}
		response.ReadModel = readModel
		response.Steps = append(response.Steps, deployStep{Name: "source_read_model", Status: "pass", Message: "source read model rebuilt"})
	}

	result, err := deployRefreshServingDB(ctx, postgres.RefreshServingDBOptions{
		SourceURL:              source,
		TargetURL:              target,
		Config:                 contract.Config,
		NoGovernanceExclusions: contract.NoGovernanceExclusions,
	})
	if err != nil {
		return a.deployPostgresRefreshStepFailure(response, "serving_refresh", "serving database refresh failed", err)
	}
	response.Result = result
	response.Steps = append(response.Steps, deployStep{Name: "serving_refresh", Status: "pass", Message: "redacted serving database refreshed"})

	if *skipGrants {
		response.Steps = append(response.Steps, deployStep{Name: "reader_grants", Status: "skipped", Message: "scoped reader grant reconciliation skipped by operator flag"})
	} else {
		params := postgres.ScopedReaderGrantSQLParams{
			Allowlist:    allowlist,
			RoleName:     role,
			DatabaseName: database,
			Generator:    "gongctl deploy postgres-refresh",
		}
		appliedSQL, err := deployApplyScopedReaderGrants(ctx, target, params)
		if err != nil {
			return a.deployPostgresRefreshStepFailure(response, "reader_grants", "scoped reader grants could not be reconciled", err)
		}
		sum := sha256.Sum256([]byte(appliedSQL))
		response.GrantSQLSHA256 = hex.EncodeToString(sum[:])
		response.Steps = append(response.Steps, deployStep{Name: "reader_grants", Status: "pass", Message: "scoped reader grants reconciled"})
	}

	return writeJSONValue(a.out, response)
}

func (a *app) deployPostgresRefreshStepFailure(response deployPostgresRefreshResponse, step, message string, err error) error {
	response.Steps = append(response.Steps, deployFailedStep(step, message, err))
	if writeErr := writeJSONValue(a.out, response); writeErr != nil {
		return writeErr
	}
	return deployPostgresRefreshFailure(step, message, err)
}

func deployFailedStep(step, message string, err error) deployStep {
	failed := deployStep{
		Name:        step,
		Status:      "fail",
		Message:     message,
		Detail:      deployPostgresRefreshFailureDetail(step, err),
		RerunSafe:   true,
		NextActions: deployStepNextActions(step, err),
	}
	var phaseErr *postgres.ServingRefreshPhaseError
	if errors.As(err, &phaseErr) {
		failed.Phase = string(phaseErr.Phase)
		failed.Side = string(phaseErr.Side)
		failed.Object = phaseErr.Object
		failed.Kind = string(phaseErr.Cause)
		failed.ServingDBState = servingRefreshFailureState(phaseErr)
		return failed
	}
	failed.Kind = deployFailureKind(err)
	return failed
}

func deployStepNextActions(step string, err error) []string {
	var phaseErr *postgres.ServingRefreshPhaseError
	if errors.As(err, &phaseErr) {
		switch phaseErr.Cause {
		case postgres.ServingRefreshCauseStatementTimeout:
			return []string{
				"raise statement_timeout for the refresh role or session",
				"rerun gongctl deploy postgres-refresh after the database change",
				"run gongctl doctor postgres-deploy if the rerun fails",
			}
		case postgres.ServingRefreshCausePermissionDenied:
			return []string{
				"grant the refresh role required source and target Postgres privileges",
				"rerun gongctl deploy postgres-refresh after grants are corrected",
				"run gongctl doctor postgres-deploy to verify deployment readiness",
			}
		case postgres.ServingRefreshCauseMigrationMissing:
			return []string{
				"apply the required Postgres migrations to the failing database",
				"rerun gongctl deploy postgres-refresh after migrations complete",
				"run gongctl doctor postgres-deploy to verify deployment readiness",
			}
		case postgres.ServingRefreshCauseConnectionFailed:
			return []string{
				"verify source and target database URLs, network access, credentials, and pg_hba rules",
				"rerun gongctl deploy postgres-refresh after connectivity is fixed",
				"run gongctl doctor postgres-deploy to verify deployment readiness",
			}
		case postgres.ServingRefreshCauseLockTimeout:
			return []string{
				"wait for contending Postgres work to finish or run the refresh during the maintenance window",
				"rerun gongctl deploy postgres-refresh",
				"run gongctl doctor postgres-deploy if the lock failure repeats",
			}
		case postgres.ServingRefreshCauseConfigInvalid:
			return []string{
				"fix the governance configuration",
				"rerun gongctl deploy postgres-refresh after validation passes",
				"run gongctl doctor postgres-deploy to verify deployment readiness",
			}
		case postgres.ServingRefreshCauseValidationFailed:
			return []string{
				"review the refresh validation counts and governance inputs",
				"rerun gongctl deploy postgres-refresh after correcting the mismatch",
				"run gongctl doctor postgres-deploy to verify deployment readiness",
			}
		case postgres.ServingRefreshCauseCanceled:
			return []string{
				"rerun gongctl deploy postgres-refresh when the maintenance window is available",
				"run gongctl doctor postgres-deploy if cancellation repeats",
			}
		}
	}
	switch step {
	case "source_read_model":
		return []string{
			"verify the source database URL, credentials, migrations, and read-model prerequisites",
			"rerun gongctl deploy postgres-refresh after the source read model can rebuild",
			"run gongctl doctor postgres-deploy to verify deployment readiness",
		}
	case "reader_grants":
		return []string{
			"verify the target database URL, reader role, database name, and grant privileges",
			"rerun gongctl deploy postgres-refresh or reapply scoped reader grants after correction",
			"run gongctl doctor postgres-deploy to verify preset and serving database readiness",
		}
	default:
		return []string{
			"review the failed step detail and sanitized operator logs",
			"rerun gongctl deploy postgres-refresh after correcting the issue",
			"run gongctl doctor postgres-deploy to verify deployment readiness",
		}
	}
}

func deployFailureKind(err error) string {
	if err == nil {
		return "unknown"
	}
	detail := sanitizeDeployStepError(err)
	switch detail {
	case "Postgres privileges are insufficient for this step":
		return "permission_denied"
	case "Postgres schema migrations are missing or stale":
		return "migration_missing"
	case "database connection, network path, or credentials are invalid":
		return "connection_failed"
	case "governance config could not be parsed or validated":
		return "config_invalid"
	default:
		return "unknown"
	}
}

func servingRefreshFailureState(err *postgres.ServingRefreshPhaseError) string {
	if err == nil {
		return ""
	}
	switch err.Phase {
	case postgres.ServingRefreshPhaseConnect, postgres.ServingRefreshPhaseAudit, postgres.ServingRefreshPhaseCount:
		return "serving database was not changed by this failing phase"
	case postgres.ServingRefreshPhaseTransaction, postgres.ServingRefreshPhaseLock, postgres.ServingRefreshPhaseTruncate, postgres.ServingRefreshPhaseCopy:
		return "previous serving data should remain available because target copy work rolls back unless the transaction commits"
	case postgres.ServingRefreshPhaseReadModel, postgres.ServingRefreshPhaseGovernancePolicy, postgres.ServingRefreshPhaseValidation, postgres.ServingRefreshPhaseMarker:
		return "serving data may have refreshed, but final validation or marker work did not complete; rerun before treating the deployment as complete"
	default:
		return "rerun the refresh before treating the deployment as complete"
	}
}

func deployPostgresRefreshFailure(step, message string, err error) error {
	return fmt.Errorf("deploy postgres-refresh failed at %s: %s", step, message)
}

func deployPostgresRefreshFailureDetail(step string, err error) string {
	if detail := postgres.ServingRefreshOperatorDetail(err); detail != "" {
		return detail
	}
	if step == "serving_refresh" {
		if sanitized := sanitizeRefreshServingDBError(err); sanitized != nil {
			return servingRefreshSanitizedDetail(sanitized)
		}
	}
	return sanitizeDeployStepError(err)
}

func servingRefreshSanitizedDetail(err error) string {
	if err == nil {
		return "unknown deployment error"
	}
	msg := err.Error()
	const prefix = "refresh serving database failed: "
	if strings.HasPrefix(msg, prefix) {
		return strings.TrimPrefix(msg, prefix)
	}
	if msg == "refresh serving database failed; inspect operator logs for details" {
		return "inspect operator logs for details"
	}
	return msg
}

func sanitizeDeployStepError(err error) string {
	if err == nil {
		return "unknown deployment error"
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "permission denied") || strings.Contains(msg, "privilege"):
		return "Postgres privileges are insufficient for this step"
	case strings.Contains(msg, "schema version") || strings.Contains(msg, "migration") || strings.Contains(msg, "does not exist"):
		return "Postgres schema migrations are missing or stale"
	case strings.Contains(msg, "password authentication failed") || strings.Contains(msg, "pg_hba") || strings.Contains(msg, "no such host") || strings.Contains(msg, "connection refused") || strings.Contains(msg, "dial tcp") || strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline"):
		return "database connection, network path, or credentials are invalid"
	case strings.Contains(msg, "yaml") || strings.Contains(msg, "governance config"):
		return "governance config could not be parsed or validated"
	default:
		return "inspect operator logs for the sanitized underlying error"
	}
}

func deployInputDefault(flagValue, fallback string, envNames ...string) string {
	if value := strings.TrimSpace(flagValue); value != "" {
		return value
	}
	for _, name := range envNames {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return fallback
}

func rebuildPostgresReadModel(ctx context.Context, databaseURL string) (*postgres.ReadModelStatus, error) {
	store, err := postgres.Open(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	defer store.Close()
	return store.RebuildReadModel(ctx)
}

func (a *app) diagnosePostgresDeploy(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("doctor postgres-deploy", flag.ContinueOnError)
	fs.SetOutput(a.err)
	targetURL := fs.String("target", "", "Postgres redacted serving database URL, e.g. $GONGCTL_MCP_DATABASE_URL")
	preset := fs.String("preset", "", "MCP tool preset to validate")
	maxMarkerAge := fs.Duration("max-marker-age", defaultPostgresDeployMarkerMaxAge, "maximum allowed age for the serving refresh marker")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	if fs.NArg() != 0 {
		return errUsage
	}

	selectedPreset := deployInputDefault(*preset, "business-workbench", "GONGMCP_TOOL_PRESET")
	target := refreshServingDBInput(*targetURL, "GONGCTL_MCP_DATABASE_URL", "GONG_DATABASE_URL", "DATABASE_URL")
	checks := []postgresdeploy.Check{
		envPresenceCheck("source_database_url", refreshServingDBInput("", "GONGCTL_SOURCE_DATABASE_URL"), "set GONGCTL_SOURCE_DATABASE_URL or pass --source to gongctl deploy postgres-refresh"),
		envPresenceCheck("serving_database_url", target, "set GONGCTL_MCP_DATABASE_URL or pass --target"),
		envPresenceCheck("governance_config", refreshServingDBInput("", "GONGCTL_AI_GOVERNANCE_CONFIG", "GONGMCP_AI_GOVERNANCE_CONFIG"), "set GONGCTL_AI_GOVERNANCE_CONFIG or GONGMCP_AI_GOVERNANCE_CONFIG"),
	}

	presetCheck, err := presetCatalogCheck(selectedPreset)
	if err != nil {
		checks = append(checks, postgresdeploy.Check{
			Name:        "tool_preset",
			Status:      postgresdeploy.CheckFail,
			ErrorKind:   "unknown_tool_preset",
			Message:     "selected MCP tool preset is not known",
			Remediation: "use a reviewed preset such as business-workbench",
		})
		return writeJSONValue(a.out, diagnosePostgresDeployResponse{
			Backend:              "postgres",
			Preset:               selectedPreset,
			Checks:               checks,
			SensitiveDataWarning: deploySensitiveDataWarning,
		})
	}
	checks = append(checks, presetCheck)

	if target == "" {
		checks = append(checks, postgresdeploy.Check{
			Name:        "serving_refresh_marker",
			Status:      postgresdeploy.CheckFail,
			ErrorKind:   "serving_database_url_missing",
			Message:     "serving refresh marker could not be checked without a serving database URL",
			Remediation: "set GONGCTL_MCP_DATABASE_URL or pass --target",
		})
		return writeJSONValue(a.out, diagnosePostgresDeployResponse{
			Backend:              "postgres",
			Preset:               selectedPreset,
			Checks:               checks,
			SensitiveDataWarning: deploySensitiveDataWarning,
		})
	}

	store, err := deployOpenPostgresStatus(ctx, target)
	if err != nil {
		checks = append(checks, postgresdeploy.Check{
			Name:        "serving_database_connectivity",
			Status:      postgresdeploy.CheckFail,
			ErrorKind:   "serving_database_unavailable",
			Message:     "serving database could not be opened for diagnostics",
			Remediation: "verify the serving database URL, network path, schema migrations, and reader privileges",
		})
	} else {
		defer store.Close()
		checks = append(checks, postgresdeploy.Check{
			Name:    "serving_database_connectivity",
			Status:  postgresdeploy.CheckPass,
			Message: "serving database opened for diagnostics",
		})
		checks = append(checks, postgresdeploy.CheckServingRefreshMarker(ctx, store, postgresdeploy.ServingRefreshMarkerOptions{
			MaxAge: *maxMarkerAge,
		}))
	}

	return writeJSONValue(a.out, diagnosePostgresDeployResponse{
		Backend:              "postgres",
		Preset:               selectedPreset,
		Checks:               checks,
		SensitiveDataWarning: deploySensitiveDataWarning,
	})
}

func envPresenceCheck(name, value, remediation string) postgresdeploy.Check {
	if strings.TrimSpace(value) == "" {
		return postgresdeploy.Check{
			Name:        name,
			Status:      postgresdeploy.CheckFail,
			ErrorKind:   "missing_operator_input",
			Message:     "required operator input is not configured",
			Remediation: remediation,
		}
	}
	return postgresdeploy.Check{
		Name:    name,
		Status:  postgresdeploy.CheckPass,
		Message: "operator input is configured",
	}
}

func presetCatalogCheck(preset string) (postgresdeploy.Check, error) {
	allowlist, err := mcp.ExpandToolPresetReaderGrantTools(preset)
	if err != nil {
		return postgresdeploy.Check{}, err
	}
	return postgresdeploy.Check{
		Name:    "tool_preset",
		Status:  postgresdeploy.CheckPass,
		Message: "MCP tool preset is known and has reviewed reader grant coverage",
		Evidence: []postgresdeploy.Evidence{
			{Key: "preset", Value: preset},
			{Key: "reader_grant_tool_count", Value: fmt.Sprintf("%d", len(allowlist))},
		},
	}, nil
}

type deployPostgresRefreshResponse struct {
	Backend                string                           `json:"backend"`
	Preset                 string                           `json:"preset"`
	Role                   string                           `json:"role"`
	Database               string                           `json:"database"`
	NoGovernanceExclusions bool                             `json:"no_governance_exclusions,omitempty"`
	Steps                  []deployStep                     `json:"steps"`
	ReadModel              *postgres.ReadModelStatus        `json:"read_model,omitempty"`
	Result                 *postgres.ServingDBRefreshResult `json:"result,omitempty"`
	GrantSQLSHA256         string                           `json:"grant_sql_sha256,omitempty"`
	SensitiveDataWarning   string                           `json:"sensitive_data_warning"`
}

type deployStep struct {
	Name           string   `json:"name"`
	Status         string   `json:"status"`
	Message        string   `json:"message"`
	Detail         string   `json:"detail,omitempty"`
	Phase          string   `json:"phase,omitempty"`
	Side           string   `json:"side,omitempty"`
	Object         string   `json:"object,omitempty"`
	Kind           string   `json:"kind,omitempty"`
	NextActions    []string `json:"next_actions,omitempty"`
	RerunSafe      bool     `json:"rerun_safe,omitempty"`
	ServingDBState string   `json:"serving_db_state,omitempty"`
}

type diagnosePostgresDeployResponse struct {
	Backend              string                 `json:"backend"`
	Preset               string                 `json:"preset"`
	Checks               []postgresdeploy.Check `json:"checks"`
	SensitiveDataWarning string                 `json:"sensitive_data_warning"`
}
