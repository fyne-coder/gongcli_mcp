package postgresdeploy

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	storepostgres "github.com/fyne-coder/gongcli_mcp/internal/store/postgres"
)

type CheckStatus string

const (
	CheckPass CheckStatus = "pass"
	CheckWarn CheckStatus = "warn"
	CheckFail CheckStatus = "fail"
)

type Check struct {
	Name        string      `json:"name"`
	Status      CheckStatus `json:"status"`
	ErrorKind   string      `json:"error_kind,omitempty"`
	Message     string      `json:"message"`
	Remediation string      `json:"remediation,omitempty"`
	Evidence    []Evidence  `json:"evidence,omitempty"`
}

type Evidence struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type ServingRefreshMarkerReader interface {
	LatestServingRefreshMarker(context.Context) (*storepostgres.ServingDBRefreshMarker, error)
}

type ServingRefreshMarkerOptions struct {
	Now    time.Time
	MaxAge time.Duration
}

func CheckServingRefreshMarker(ctx context.Context, reader ServingRefreshMarkerReader, opts ServingRefreshMarkerOptions) Check {
	if reader == nil {
		return Check{
			Name:        "serving_refresh_marker",
			Status:      CheckFail,
			ErrorKind:   "serving_refresh_marker_reader_missing",
			Message:     "serving refresh marker could not be checked",
			Remediation: "run diagnostics with access to the Postgres serving database",
		}
	}
	marker, err := reader.LatestServingRefreshMarker(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return Check{
			Name:        "serving_refresh_marker",
			Status:      CheckFail,
			ErrorKind:   "serving_refresh_marker_missing",
			Message:     "serving database has no recorded governance refresh marker",
			Remediation: "run gongctl deploy postgres-refresh or gongctl governance refresh-serving-db against the serving database",
		}
	}
	if err != nil {
		return Check{
			Name:        "serving_refresh_marker",
			Status:      CheckFail,
			ErrorKind:   "serving_refresh_marker_unavailable",
			Message:     "serving refresh marker could not be read",
			Remediation: "verify the serving database URL, schema migration state, and reader privileges",
		}
	}
	if marker == nil ||
		marker.ID == 0 ||
		marker.RefreshedAt == "" ||
		marker.SourceDataFingerprint == "" ||
		marker.TargetDataFingerprint == "" ||
		marker.PolicyConfigSHA256 == "" {
		return Check{
			Name:        "serving_refresh_marker",
			Status:      CheckFail,
			ErrorKind:   "serving_refresh_marker_incomplete",
			Message:     "serving refresh marker is incomplete",
			Remediation: "rerun the serving database refresh after upgrading gongctl",
		}
	}

	refreshedAt, err := time.Parse(time.RFC3339Nano, marker.RefreshedAt)
	if err != nil {
		return Check{
			Name:        "serving_refresh_marker",
			Status:      CheckFail,
			ErrorKind:   "serving_refresh_marker_invalid_timestamp",
			Message:     "serving refresh marker timestamp is invalid",
			Remediation: "rerun the serving database refresh after verifying schema migrations",
		}
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if opts.MaxAge > 0 && now.Sub(refreshedAt) > opts.MaxAge {
		return Check{
			Name:        "serving_refresh_marker",
			Status:      CheckFail,
			ErrorKind:   "serving_refresh_marker_stale",
			Message:     "serving database refresh marker is older than the allowed freshness window",
			Remediation: "rerun gongctl deploy postgres-refresh before exposing this preset",
			Evidence: []Evidence{
				{Key: "refreshed_at", Value: marker.RefreshedAt},
				{Key: "max_age", Value: opts.MaxAge.String()},
			},
		}
	}

	return Check{
		Name:    "serving_refresh_marker",
		Status:  CheckPass,
		Message: "serving database has a recorded governance refresh marker",
		Evidence: []Evidence{
			{Key: "refreshed_at", Value: marker.RefreshedAt},
			{Key: "policy_config_sha256", Value: marker.PolicyConfigSHA256},
			{Key: "source_data_fingerprint", Value: marker.SourceDataFingerprint},
			{Key: "target_data_fingerprint", Value: marker.TargetDataFingerprint},
		},
	}
}

func CheckStatementTimeout(raw string) Check {
	if strings.TrimSpace(raw) == "" {
		return Check{
			Name:    "statement_timeout",
			Status:  CheckPass,
			Message: "no custom statement_timeout configured; deploy will use the source session default",
		}
	}
	parsed, err := storepostgres.ParseRefreshStatementTimeout(raw)
	if err != nil {
		return Check{
			Name:        "statement_timeout",
			Status:      CheckFail,
			ErrorKind:   "statement_timeout_invalid",
			Message:     "statement_timeout operator input is invalid",
			Remediation: "set --statement-timeout or GONGCTL_REFRESH_STATEMENT_TIMEOUT to a positive duration such as 30m",
		}
	}
	return Check{
		Name:    "statement_timeout",
		Status:  CheckPass,
		Message: "statement_timeout operator input is valid for deploy postgres-refresh",
		Evidence: []Evidence{
			{Key: "statement_timeout", Value: storepostgres.FormatRefreshStatementTimeout(parsed)},
		},
	}
}

func CheckScopedReaderRoleInput(role, database string) Check {
	evidence := []Evidence{
		{Key: "role", Value: role},
		{Key: "database", Value: database},
	}
	if err := storepostgres.ValidateScopedReaderIdentifiers(role, database); err != nil {
		return Check{
			Name:        "scoped_reader_role_input",
			Status:      CheckFail,
			ErrorKind:   "scoped_reader_role_input_invalid",
			Message:     "scoped reader role or database input is invalid",
			Remediation: "set --role and --database to reviewed Postgres identifiers or matching env defaults",
			Evidence:    evidence,
		}
	}
	return Check{
		Name:     "scoped_reader_role_input",
		Status:   CheckPass,
		Message:  "scoped reader role and database inputs are valid Postgres identifiers",
		Evidence: evidence,
	}
}

func CheckScopedReaderGrantSQL(params storepostgres.ScopedReaderGrantSQLParams) Check {
	evidence := []Evidence{
		{Key: "role", Value: params.RoleName},
		{Key: "database", Value: params.DatabaseName},
	}
	sql, err := storepostgres.BuildScopedReaderGrantSQL(params)
	if err != nil {
		return Check{
			Name:        "scoped_reader_grant_sql",
			Status:      CheckFail,
			ErrorKind:   "scoped_reader_grant_sql_unavailable",
			Message:     "scoped reader grant SQL could not be generated for the selected preset and role inputs",
			Remediation: "use a reviewed preset and valid role/database inputs before deploy postgres-refresh",
			Evidence:    evidence,
		}
	}
	sum := sha256.Sum256([]byte(sql))
	evidence = append(evidence,
		Evidence{Key: "grant_sql_sha256", Value: hex.EncodeToString(sum[:])},
		Evidence{Key: "grant_sql_bytes", Value: fmt.Sprintf("%d", len(sql))},
	)
	return Check{
		Name:     "scoped_reader_grant_sql",
		Status:   CheckPass,
		Message:  "scoped reader grant SQL can be generated without applying changes",
		Evidence: evidence,
	}
}

type ReadModelStatusReader interface {
	ReadModelStatus(context.Context) (*storepostgres.ReadModelStatus, error)
}

func CheckSourceDatabaseConnectivity(connectErr error) Check {
	if connectErr != nil {
		return Check{
			Name:        "source_database_connectivity",
			Status:      CheckFail,
			ErrorKind:   "source_database_unavailable",
			Message:     "source database could not be opened for diagnostics",
			Remediation: "verify the source database URL, network path, schema migrations, and operator privileges",
		}
	}
	return Check{
		Name:    "source_database_connectivity",
		Status:  CheckPass,
		Message: "source database opened for diagnostics",
	}
}

func CheckSourceDatabaseConnectivityMissing() Check {
	return Check{
		Name:        "source_database_connectivity",
		Status:      CheckFail,
		ErrorKind:   "source_database_url_missing",
		Message:     "source database could not be checked without a source database URL",
		Remediation: "set GONGCTL_SOURCE_DATABASE_URL or pass --source",
	}
}

func CheckServingDatabaseConnectivityMissing() Check {
	return Check{
		Name:        "serving_database_connectivity",
		Status:      CheckFail,
		ErrorKind:   "serving_database_url_missing",
		Message:     "serving database could not be checked without a serving database URL",
		Remediation: "set GONGCTL_MCP_DATABASE_URL or pass --target",
	}
}

func CheckSourceReadModel(ctx context.Context, reader ReadModelStatusReader) Check {
	if reader == nil {
		return Check{
			Name:        "source_read_model",
			Status:      CheckFail,
			ErrorKind:   "source_read_model_reader_missing",
			Message:     "source read model could not be checked",
			Remediation: "verify source database connectivity before checking read model readiness",
		}
	}
	status, err := reader.ReadModelStatus(ctx)
	if err != nil {
		return Check{
			Name:        "source_read_model",
			Status:      CheckFail,
			ErrorKind:   "source_read_model_unavailable",
			Message:     "source read model status could not be read",
			Remediation: "verify source database URL, schema migrations, and operator privileges",
		}
	}
	evidence := []Evidence{
		{Key: "model_name", Value: status.ModelName},
		{Key: "ready", Value: fmt.Sprintf("%t", status.Ready)},
	}
	if status.CallCount > 0 {
		evidence = append(evidence, Evidence{Key: "call_count", Value: fmt.Sprintf("%d", status.CallCount)})
	}
	if !status.Ready {
		if status.StaleReason != "" {
			evidence = append(evidence, Evidence{Key: "stale_reason", Value: status.StaleReason})
		}
		return Check{
			Name:        "source_read_model",
			Status:      CheckFail,
			ErrorKind:   "source_read_model_not_ready",
			Message:     "source read model is not ready for serving refresh",
			Remediation: "rebuild the source read model with gongctl sync read-model --rebuild or rerun deploy postgres-refresh",
			Evidence:    evidence,
		}
	}
	return Check{
		Name:     "source_read_model",
		Status:   CheckPass,
		Message:  "source read model is ready",
		Evidence: evidence,
	}
}

func CheckSourceReadModelUnavailable(errorKind string) Check {
	check := Check{
		Name:        "source_read_model",
		Status:      CheckFail,
		Message:     "source read model could not be checked without source database connectivity",
		Remediation: "set GONGCTL_SOURCE_DATABASE_URL or pass --source and verify connectivity",
	}
	switch errorKind {
	case "source_database_url_missing":
		check.ErrorKind = "source_database_url_missing"
	case "source_database_unavailable":
		check.ErrorKind = "source_database_unavailable"
	default:
		check.ErrorKind = "source_read_model_unavailable"
	}
	return check
}

func CheckServingRefreshMarkerUnavailable(errorKind string) Check {
	check := Check{
		Name:        "serving_refresh_marker",
		Status:      CheckFail,
		Message:     "serving refresh marker could not be checked without serving database connectivity",
		Remediation: "set GONGCTL_MCP_DATABASE_URL or pass --target and verify connectivity",
	}
	switch errorKind {
	case "serving_database_url_missing":
		check.ErrorKind = "serving_database_url_missing"
		check.Message = "serving refresh marker could not be checked without a serving database URL"
		check.Remediation = "set GONGCTL_MCP_DATABASE_URL or pass --target"
	case "serving_database_unavailable":
		check.ErrorKind = "serving_database_unavailable"
	default:
		check.ErrorKind = "serving_refresh_marker_unavailable"
	}
	return check
}
