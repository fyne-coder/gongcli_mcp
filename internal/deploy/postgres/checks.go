package postgresdeploy

import (
	"context"
	"database/sql"
	"errors"
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
