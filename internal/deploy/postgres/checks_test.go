package postgresdeploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	storepostgres "github.com/fyne-coder/gongcli_mcp/internal/store/postgres"
)

type fakeMarkerReader struct {
	marker *storepostgres.ServingDBRefreshMarker
	err    error
}

func (f fakeMarkerReader) LatestServingRefreshMarker(context.Context) (*storepostgres.ServingDBRefreshMarker, error) {
	return f.marker, f.err
}

func TestCheckServingRefreshMarkerMissing(t *testing.T) {
	check := CheckServingRefreshMarker(context.Background(), fakeMarkerReader{err: sql.ErrNoRows}, ServingRefreshMarkerOptions{})
	if check.Status != CheckFail || check.ErrorKind != "serving_refresh_marker_missing" {
		t.Fatalf("unexpected check: %+v", check)
	}
}

func TestCheckServingRefreshMarkerUnavailable(t *testing.T) {
	check := CheckServingRefreshMarker(context.Background(), fakeMarkerReader{err: context.DeadlineExceeded}, ServingRefreshMarkerOptions{})
	if check.Status != CheckFail || check.ErrorKind != "serving_refresh_marker_unavailable" {
		t.Fatalf("unexpected check: %+v", check)
	}
}

func TestCheckServingRefreshMarkerRejectsIncompleteMarker(t *testing.T) {
	check := CheckServingRefreshMarker(context.Background(), fakeMarkerReader{marker: &storepostgres.ServingDBRefreshMarker{
		ID:          7,
		RefreshedAt: time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
	}}, ServingRefreshMarkerOptions{})
	if check.Status != CheckFail || check.ErrorKind != "serving_refresh_marker_incomplete" {
		t.Fatalf("unexpected check: %+v", check)
	}
}

func TestCheckServingRefreshMarkerRejectsInvalidTimestamp(t *testing.T) {
	check := CheckServingRefreshMarker(context.Background(), fakeMarkerReader{marker: &storepostgres.ServingDBRefreshMarker{
		ID:                    7,
		RefreshedAt:           "not-a-timestamp",
		SourceDataFingerprint: "source-fingerprint",
		TargetDataFingerprint: "target-fingerprint",
		PolicyConfigSHA256:    "policy-sha",
	}}, ServingRefreshMarkerOptions{})
	if check.Status != CheckFail || check.ErrorKind != "serving_refresh_marker_invalid_timestamp" {
		t.Fatalf("unexpected check: %+v", check)
	}
}

func TestCheckServingRefreshMarkerPassesWithSanitizedEvidence(t *testing.T) {
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	check := CheckServingRefreshMarker(context.Background(), fakeMarkerReader{marker: &storepostgres.ServingDBRefreshMarker{
		ID:                    7,
		RefreshedAt:           now.Add(-time.Hour).Format(time.RFC3339Nano),
		SourceDataFingerprint: "source-fingerprint",
		TargetDataFingerprint: "target-fingerprint",
		PolicyConfigSHA256:    "policy-sha",
		SourceCalls:           10,
		TargetCalls:           8,
		RemovedCalls:          2,
		SuppressedCallCount:   2,
		RowCountsJSON:         json.RawMessage(`{"source_calls":10,"target_calls":8}`),
	}}, ServingRefreshMarkerOptions{Now: now, MaxAge: 2 * time.Hour})
	if check.Status != CheckPass {
		t.Fatalf("unexpected check: %+v", check)
	}
	body, err := json.Marshal(check)
	if err != nil {
		t.Fatalf("marshal check: %v", err)
	}
	for _, blocked := range []string{
		"postgres://",
		"secret",
		"customer.example",
		"Blocked Synthetic Corp",
		"pg-serving-blocked",
		"Restricted transcript",
	} {
		if strings.Contains(string(body), blocked) {
			t.Fatalf("check leaked blocked value %q: %s", blocked, body)
		}
	}
}

func TestCheckServingRefreshMarkerRejectsStaleMarker(t *testing.T) {
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	check := CheckServingRefreshMarker(context.Background(), fakeMarkerReader{marker: &storepostgres.ServingDBRefreshMarker{
		ID:                    7,
		RefreshedAt:           now.Add(-3 * time.Hour).Format(time.RFC3339Nano),
		SourceDataFingerprint: "source-fingerprint",
		TargetDataFingerprint: "target-fingerprint",
		PolicyConfigSHA256:    "policy-sha",
	}}, ServingRefreshMarkerOptions{Now: now, MaxAge: time.Hour})
	if check.Status != CheckFail || check.ErrorKind != "serving_refresh_marker_stale" {
		t.Fatalf("unexpected check: %+v", check)
	}
}
