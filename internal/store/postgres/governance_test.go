package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/fyne-coder/gongcli_mcp/internal/governance"
)

func TestConsumeGovernanceCandidateRowsScanAndConsumerErrors(t *testing.T) {
	ctx := context.Background()
	scanErr := errors.New("scan failed")
	err := consumeGovernanceCandidateRows(ctx, &fakeGovernanceRows{
		scanErr: scanErr,
		values:  [][]string{{"call-1", "call_title", "Acme"}},
	}, func(governance.Candidate) error { return nil })
	if !errors.Is(err, scanErr) {
		t.Fatalf("scan error=%v want %v", err, scanErr)
	}

	consumeErr := errors.New("consume failed")
	var seen int
	err = consumeGovernanceCandidateRows(ctx, &fakeGovernanceRows{
		values: [][]string{
			{"call-1", "call_title", "Acme"},
			{"call-2", "call_title", "Other"},
		},
	}, func(governance.Candidate) error {
		seen++
		return consumeErr
	})
	if !errors.Is(err, consumeErr) {
		t.Fatalf("consume error=%v want %v", err, consumeErr)
	}
	if seen != 1 {
		t.Fatalf("seen=%d want 1 (stop after first consumer error)", seen)
	}

	rowsErr := errors.New("rows iteration failed")
	seen = 0
	err = consumeGovernanceCandidateRows(ctx, &fakeGovernanceRows{
		values: [][]string{{"call-1", "call_title", "Acme"}},
		err:    rowsErr,
	}, func(governance.Candidate) error {
		seen++
		return nil
	})
	if !errors.Is(err, rowsErr) {
		t.Fatalf("rows.Err=%v want %v", err, rowsErr)
	}
	if seen != 1 {
		t.Fatalf("seen=%d want 1 (consume succeeded before terminal rows.Err)", seen)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = consumeGovernanceCandidateRows(ctx, &fakeGovernanceRows{
		values: [][]string{{"call-1", "call_title", "Acme"}},
	}, func(governance.Candidate) error {
		t.Fatal("consume should not run after canceled context")
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error=%v want context.Canceled", err)
	}
}

func TestStreamGovernanceNameCandidatesQueryContextError(t *testing.T) {
	queryErr := errors.New("query failed")
	var seen int
	err := streamGovernanceNameCandidates(context.Background(), &fakeGovernanceQueryer{err: queryErr}, func(governance.Candidate) error {
		seen++
		return nil
	})
	if !errors.Is(err, queryErr) {
		t.Fatalf("QueryContext error=%v want %v", err, queryErr)
	}
	if seen != 0 {
		t.Fatalf("seen=%d want 0 (no consumer call after query error)", seen)
	}
}

func TestConsumeGovernanceCandidateRowsSuccess(t *testing.T) {
	var got []governance.Candidate
	err := consumeGovernanceCandidateRows(context.Background(), &fakeGovernanceRows{
		values: [][]string{
			{"call-1", "call_title", "Acme"},
			{"call-2", "transcript_segment", "Other"},
		},
	}, func(candidate governance.Candidate) error {
		got = append(got, candidate)
		return nil
	})
	if err != nil {
		t.Fatalf("consumeGovernanceCandidateRows returned error: %v", err)
	}
	want := []governance.Candidate{
		{CallID: "call-1", Source: "call_title", Value: "Acme"},
		{CallID: "call-2", Source: "transcript_segment", Value: "Other"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("candidates=%+v want %+v", got, want)
	}
}

func TestPostgresGovernanceStreamingAuditParity(t *testing.T) {
	databaseURL := os.Getenv("GONGCTL_TEST_POSTGRES_URL")
	if databaseURL == "" {
		t.Skip("GONGCTL_TEST_POSTGRES_URL is not set")
	}

	ctx := context.Background()
	store, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()
	resetPostgresTestStore(t, ctx, store)

	if _, err := store.UpsertCall(ctx, json.RawMessage(`{"id":"pg-stream-blocked","title":"Blocked stream account","started":"2026-04-24T12:00:00Z","duration":1200,"context":{"crmObjects":[{"type":"Account","id":"acct-stream-blocked","name":"Postgres Stream NoAI","fields":{"Name":"Postgres Stream NoAI"}}]}}`)); err != nil {
		t.Fatalf("UpsertCall blocked returned error: %v", err)
	}
	if _, err := store.UpsertCall(ctx, json.RawMessage(`{"id":"pg-stream-allowed","title":"Allowed stream account","started":"2026-04-24T13:00:00Z","duration":1200,"context":{"crmObjects":[{"type":"Account","id":"acct-stream-allowed","name":"Postgres Stream Allowed","fields":{"Name":"Postgres Stream Allowed"}}]}}`)); err != nil {
		t.Fatalf("UpsertCall allowed returned error: %v", err)
	}
	if _, err := store.UpsertTranscript(ctx, json.RawMessage(`{"callId":"pg-stream-blocked","transcript":[{"speakerId":"speaker-1","sentences":[{"start":0,"end":3000,"text":"The team named Postgres Stream NoAI in transcript evidence."}]}]}`)); err != nil {
		t.Fatalf("UpsertTranscript blocked returned error: %v", err)
	}

	cfg, err := governance.ParseYAML([]byte(`
version: 1
lists:
  no_ai:
    customers:
      - name: "Postgres Stream NoAI"
`))
	if err != nil {
		t.Fatalf("ParseYAML returned error: %v", err)
	}

	streamAudit, err := governance.BuildAudit(ctx, store, cfg)
	if err != nil {
		t.Fatalf("BuildAudit streaming returned error: %v", err)
	}
	materialized, err := store.GovernanceNameCandidates(ctx)
	if err != nil {
		t.Fatalf("GovernanceNameCandidates returned error: %v", err)
	}
	want := governance.AuditCandidates(materialized, cfg)
	if !reflect.DeepEqual(streamAudit, want) {
		t.Fatalf("streaming BuildAudit mismatch\ngot:  %+v\nwant: %+v", streamAudit, want)
	}

	policyAudit, policy, err := store.BuildAndSaveGovernancePolicy(ctx, cfg.Fingerprint(), cfg)
	if err != nil {
		t.Fatalf("BuildAndSaveGovernancePolicy returned error: %v", err)
	}
	if !reflect.DeepEqual(policyAudit, want) {
		t.Fatalf("policy audit mismatch\ngot:  %+v\nwant: %+v", policyAudit, want)
	}
	if policy.SuppressedCallCount != want.SuppressedCallCount || !reflect.DeepEqual(policy.SuppressedCallIDs, want.SuppressedCallIDs) {
		t.Fatalf("unexpected saved policy: %+v want suppressed %+v", policy, want.SuppressedCallIDs)
	}
}

type fakeGovernanceQueryer struct {
	err error
}

func (q *fakeGovernanceQueryer) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, q.err
}

type fakeGovernanceRows struct {
	values  [][]string
	index   int
	scanErr error
	err     error
}

func (r *fakeGovernanceRows) Next() bool {
	if r.index >= len(r.values) {
		return false
	}
	r.index++
	return true
}

func (r *fakeGovernanceRows) Scan(dest ...any) error {
	if r.scanErr != nil {
		return r.scanErr
	}
	row := r.values[r.index-1]
	for i, value := range row {
		ptr, ok := dest[i].(*string)
		if !ok {
			return errors.New("unexpected scan destination")
		}
		*ptr = value
	}
	return nil
}

func (r *fakeGovernanceRows) Err() error {
	return r.err
}
