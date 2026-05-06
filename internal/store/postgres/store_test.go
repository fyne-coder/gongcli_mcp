package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/fyne-coder/gongcli_mcp/internal/governance"
	profilepkg "github.com/fyne-coder/gongcli_mcp/internal/profile"
	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
)

func TestStoreSyntheticVerticalSlice(t *testing.T) {
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

	run, err := store.StartSyncRun(ctx, sqlite.StartSyncRunParams{Scope: "synthetic", SyncKey: "synthetic:test"})
	if err != nil {
		t.Fatalf("StartSyncRun returned error: %v", err)
	}
	if _, err := store.UpsertCall(ctx, json.RawMessage(`{"id":"pg-call-001","title":"Postgres shared MCP kickoff","started":"2026-01-15T15:00:00Z","duration":1200,"parties":[{"id":"speaker-1"}]}`)); err != nil {
		t.Fatalf("UpsertCall returned error: %v", err)
	}
	enrichedCall, err := store.UpsertCall(ctx, json.RawMessage(`{"id":"pg-call-002","title":"Preserve enriched call","started":"2026-01-15T18:00:00Z","duration":600,"parties":[{"id":"speaker-1"},{"id":"speaker-2"}],"context":{"crmObjects":[{"type":"account","id":"acct-1","fields":{"Name":"Acme"}}]}}`))
	if err != nil {
		t.Fatalf("UpsertCall enriched returned error: %v", err)
	}
	if !enrichedCall.ContextPresent || enrichedCall.PartiesCount != 2 {
		t.Fatalf("enriched call did not capture context/parties: %+v", enrichedCall)
	}
	downgradedCall, err := store.UpsertCall(ctx, json.RawMessage(`{"id":"pg-call-002","title":"Preserve enriched call","started":"2026-01-15T18:00:00Z","duration":600}`))
	if err != nil {
		t.Fatalf("UpsertCall minimal returned error: %v", err)
	}
	if !downgradedCall.ContextPresent || downgradedCall.PartiesCount != 2 {
		t.Fatalf("minimal update downgraded enriched call: %+v", downgradedCall)
	}
	if !jsonContainsKey(downgradedCall.RawJSON, "context") {
		t.Fatalf("minimal update replaced enriched raw JSON: %s", string(downgradedCall.RawJSON))
	}
	if _, err := store.UpsertUser(ctx, json.RawMessage(`{"id":"pg-user-001","emailAddress":"operator@example.invalid","firstName":"Op","lastName":"Erator","title":"RevOps Lead","active":true}`)); err != nil {
		t.Fatalf("UpsertUser returned error: %v", err)
	}
	if _, err := store.UpsertTranscript(ctx, json.RawMessage(`{"callId":"pg-call-001","transcript":[{"speakerId":"speaker-1","sentences":[{"start":0,"end":3000,"text":"Postgres gives the sync job and MCP server one shared database."}]}]}`)); err != nil {
		t.Fatalf("UpsertTranscript returned error: %v", err)
	}
	if err := store.FinishSyncRun(ctx, run.ID, sqlite.FinishSyncRunParams{Status: "success", RecordsSeen: 3, RecordsWritten: 3}); err != nil {
		t.Fatalf("FinishSyncRun returned error: %v", err)
	}

	summary, err := store.SyncStatusSummary(ctx)
	if err != nil {
		t.Fatalf("SyncStatusSummary returned error: %v", err)
	}
	if summary.TotalCalls != 2 || summary.TotalUsers != 1 || summary.TotalTranscripts != 1 || summary.TotalTranscriptSegments != 1 || summary.MissingTranscripts != 1 {
		t.Fatalf("unexpected summary: %+v", summary)
	}

	calls, err := store.SearchCallsRaw(ctx, sqlite.CallSearchParams{Limit: 10})
	if err != nil {
		t.Fatalf("SearchCallsRaw returned error: %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("SearchCallsRaw returned %d calls, want 2", len(calls))
	}
	callsOnDay, err := store.SearchCallsRaw(ctx, sqlite.CallSearchParams{ToDate: "2026-01-15", Limit: 10})
	if err != nil {
		t.Fatalf("SearchCallsRaw with inclusive to_date returned error: %v", err)
	}
	if len(callsOnDay) != 2 {
		t.Fatalf("SearchCallsRaw with inclusive to_date returned %d calls, want 2", len(callsOnDay))
	}

	segments, err := store.SearchTranscriptSegments(ctx, "shared database", 10)
	if err != nil {
		t.Fatalf("SearchTranscriptSegments returned error: %v", err)
	}
	if len(segments) != 1 || segments[0].CallID != "pg-call-001" || segments[0].Snippet == "" {
		t.Fatalf("unexpected transcript search results: %+v", segments)
	}

	if _, err := OpenReadOnly(ctx, databaseURL); err == nil {
		t.Fatal("OpenReadOnly accepted writer-privileged URL")
	}
}

func TestPostgresCacheInventoryAndDiagnostics(t *testing.T) {
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

	run, err := store.StartSyncRun(ctx, sqlite.StartSyncRunParams{Scope: "synthetic", SyncKey: "synthetic:cache-inventory"})
	if err != nil {
		t.Fatalf("StartSyncRun returned error: %v", err)
	}
	if _, err := store.UpsertCall(ctx, json.RawMessage(`{"id":"pg-cache-001","title":"Postgres cache inventory","started":"2026-04-20T14:00:00Z","duration":1200,"parties":[{"id":"seller-1"}]}`)); err != nil {
		t.Fatalf("UpsertCall returned error: %v", err)
	}
	if _, err := store.UpsertTranscript(ctx, json.RawMessage(`{"callId":"pg-cache-001","transcript":[{"speakerId":"seller-1","sentences":[{"start":0,"end":1000,"text":"Inventory diagnostics should count transcript segments."}]}]}`)); err != nil {
		t.Fatalf("UpsertTranscript returned error: %v", err)
	}
	if _, err := store.UpsertUser(ctx, json.RawMessage(`{"id":"pg-cache-user-001","emailAddress":"cache@example.invalid","name":"Cache User","title":"Operator","active":true}`)); err != nil {
		t.Fatalf("UpsertUser returned error: %v", err)
	}
	if _, err := store.UpsertCRMIntegration(ctx, json.RawMessage(`{"integrationId":"pg-cache-crm-001","name":"Cache CRM","crmType":"Salesforce"}`)); err != nil {
		t.Fatalf("UpsertCRMIntegration returned error: %v", err)
	}
	if count, err := store.UpsertCRMSchema(ctx, "pg-cache-crm-001", "Opportunity", json.RawMessage(`{"objectTypeToSelectedFields":{"Opportunity":[{"fieldName":"StageName","label":"Stage","fieldType":"picklist"},{"fieldName":"Amount","label":"Amount","fieldType":"currency"}]}}`)); err != nil {
		t.Fatalf("UpsertCRMSchema returned error: %v", err)
	} else if count != 2 {
		t.Fatalf("UpsertCRMSchema count=%d want 2", count)
	}
	if _, err := store.UpsertGongSetting(ctx, "scorecards", json.RawMessage(`{"scorecardId":"pg-cache-scorecard-001","scorecardName":"Cache scorecard","enabled":true,"questions":[{"questionId":"question-001","questionText":"Was the cache checked?","minRange":1,"maxRange":5}]}`)); err != nil {
		t.Fatalf("UpsertGongSetting returned error: %v", err)
	}
	if _, err := store.UpsertScorecardActivity(ctx, json.RawMessage(`{"answeredScorecardId":"pg-cache-answered-001","scorecardId":"pg-cache-scorecard-001","scorecardName":"Cache scorecard","callId":"pg-cache-001","callStartTime":"2026-04-20T14:00:00Z","reviewMethod":"MANUAL","reviewTime":"2026-04-21T14:00:00Z","answers":[{"isOverall":true,"score":4,"notApplicable":false}]}`)); err != nil {
		t.Fatalf("UpsertScorecardActivity returned error: %v", err)
	}
	if err := store.FinishSyncRun(ctx, run.ID, sqlite.FinishSyncRunParams{Status: "success", RecordsSeen: 1, RecordsWritten: 1}); err != nil {
		t.Fatalf("FinishSyncRun returned error: %v", err)
	}

	inventory, err := store.CacheInventory(ctx)
	if err != nil {
		t.Fatalf("CacheInventory returned error: %v", err)
	}
	if inventory.Summary.TotalCalls != 1 || inventory.Summary.TotalUsers != 1 || inventory.Summary.TotalTranscripts != 1 || inventory.Summary.TotalTranscriptSegments != 1 {
		t.Fatalf("unexpected summary counts: %+v", inventory.Summary)
	}
	if inventory.Summary.TotalCRMIntegrations != 1 || inventory.Summary.TotalCRMSchemaObjects != 1 || inventory.Summary.TotalCRMSchemaFields != 2 || inventory.Summary.TotalGongSettings != 1 || inventory.Summary.TotalScorecardActivity != 1 {
		t.Fatalf("unexpected inventory extension counts: %+v", inventory.Summary)
	}
	counts := postgresTableCounts(inventory.TableCounts)
	if counts["calls"] != 1 || counts["users"] != 1 || counts["transcript_segments"] != 1 || counts["crm_schema_fields"] != 2 || counts["gong_settings"] != 1 || counts["scorecard_activity"] != 1 {
		t.Fatalf("unexpected table counts: %+v", counts)
	}

	diagnostics, err := store.CacheDiagnostics(ctx)
	if err != nil {
		t.Fatalf("CacheDiagnostics returned error: %v", err)
	}
	if diagnostics.Backend != "postgres" ||
		diagnostics.SchemaVersion != len(migrations) ||
		diagnostics.SupportedSchemaVersion != len(migrations) ||
		!diagnostics.ReadModelReady ||
		diagnostics.ReadModelStatus != "current" ||
		diagnostics.ProfileCacheStatus != "not_applicable" ||
		diagnostics.ReaderPrivilegeStatus != "not_valid_reader" {
		t.Fatalf("unexpected diagnostics: %+v", diagnostics)
	}

	statusStore, err := OpenStatus(ctx, databaseURL)
	if err != nil {
		t.Fatalf("OpenStatus returned error: %v", err)
	}
	defer statusStore.Close()
	if _, err := statusStore.DB().ExecContext(ctx, `INSERT INTO users(user_id, raw_json, raw_sha256, first_seen_at, updated_at) VALUES('pg-cache-should-not-write', '{}'::jsonb, 'x', now()::text, now()::text)`); err == nil {
		t.Fatal("OpenStatus allowed a write with a writer URL")
	}
}

func TestPostgresCachePurgeBeforePlansAndDeletesCacheRows(t *testing.T) {
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

	oldCall := json.RawMessage(`{"id":"pg-purge-old","title":"Old purge call","started":"2026-04-20T14:00:00Z","duration":1200,"context":{"crmObjects":[{"type":"Opportunity","id":"opp-purge-old","name":"Old purge opportunity","fields":{"StageName":"Discovery"}}]}}`)
	newCall := json.RawMessage(`{"id":"pg-purge-new","title":"New retained call","started":"2026-04-24T14:00:00Z","duration":900}`)
	if _, err := store.UpsertCall(ctx, oldCall); err != nil {
		t.Fatalf("UpsertCall old returned error: %v", err)
	}
	if _, err := store.UpsertCall(ctx, newCall); err != nil {
		t.Fatalf("UpsertCall new returned error: %v", err)
	}
	if _, err := store.UpsertTranscript(ctx, json.RawMessage(`{"callId":"pg-purge-old","transcript":[{"speakerId":"speaker-old","sentences":[{"start":0,"end":1000,"text":"old purge transcript needle"}]}]}`)); err != nil {
		t.Fatalf("UpsertTranscript returned error: %v", err)
	}
	if _, err := store.UpsertScorecardActivity(ctx, json.RawMessage(`{"answeredScorecardId":"pg-purge-scorecard","scorecardId":"pg-purge-scorecard-template","scorecardName":"Retention QA","callId":"pg-purge-old","callStartTime":"2026-04-20T14:00:00Z","reviewMethod":"MANUAL","reviewTime":"2026-04-21T14:00:00Z","answers":[{"isOverall":true,"score":4,"notApplicable":false}]}`)); err != nil {
		t.Fatalf("UpsertScorecardActivity returned error: %v", err)
	}
	if _, err := importSyntheticPostgresProfile(t, ctx, store); err != nil {
		t.Fatalf("import synthetic profile: %v", err)
	}
	assertPostgresCount(t, ctx, store, `SELECT COUNT(*) FROM profile_call_fact_cache WHERE call_id = 'pg-purge-old'`, 1)
	if _, err := store.DB().ExecContext(ctx, `INSERT INTO governance_policy_state(config_sha256, data_fingerprint, suppressed_call_count, updated_at) VALUES('purge-policy-sha', 'fingerprint-before-purge', 1, now()::text)`); err != nil {
		t.Fatalf("insert governance policy row: %v", err)
	}
	if _, err := store.DB().ExecContext(ctx, `INSERT INTO governance_suppressed_calls(config_sha256, call_id) VALUES('purge-policy-sha', 'pg-purge-old')`); err != nil {
		t.Fatalf("insert governance suppressed call row: %v", err)
	}

	readOnlyView := &Store{db: store.DB(), readOnly: true}
	plan, err := readOnlyView.PlanCachePurgeBefore(ctx, "2026-04-22")
	if err != nil {
		t.Fatalf("read-only PlanCachePurgeBefore returned error: %v", err)
	}
	if plan.CallCount != 1 ||
		plan.TranscriptCount != 1 ||
		plan.TranscriptSegmentCount != 1 ||
		plan.ContextObjectCount != 1 ||
		plan.ContextFieldCount != 1 ||
		plan.CallFactCount != 1 ||
		plan.ReadModelDiagnosticCount != 1 ||
		plan.ProfileCallFactCount != 1 ||
		plan.ScorecardActivityCount != 1 ||
		plan.GovernanceSuppressedCallCount != 1 {
		t.Fatalf("unexpected purge plan: %+v", plan)
	}
	if _, err := readOnlyView.PurgeCacheBefore(ctx, "2026-04-22"); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("read-only purge error=%v, want read-only failure", err)
	}

	results, err := store.SearchTranscriptSegments(ctx, "needle", 10)
	if err != nil {
		t.Fatalf("SearchTranscriptSegments before purge returned error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("search results before purge=%d want 1", len(results))
	}

	executed, err := store.PurgeCacheBefore(ctx, "2026-04-22")
	if err != nil {
		t.Fatalf("PurgeCacheBefore returned error: %v", err)
	}
	if executed.CallCount != 1 || executed.ScorecardActivityCount != 1 || executed.GovernanceSuppressedCallCount != 1 {
		t.Fatalf("unexpected executed purge plan: %+v", executed)
	}

	for _, check := range []struct {
		name  string
		query string
	}{
		{"old calls", `SELECT COUNT(*) FROM calls WHERE call_id = 'pg-purge-old'`},
		{"old transcripts", `SELECT COUNT(*) FROM transcripts WHERE call_id = 'pg-purge-old'`},
		{"old transcript segments", `SELECT COUNT(*) FROM transcript_segments WHERE call_id = 'pg-purge-old'`},
		{"old context objects", `SELECT COUNT(*) FROM call_context_objects WHERE call_id = 'pg-purge-old'`},
		{"old context fields", `SELECT COUNT(*) FROM call_context_fields WHERE call_id = 'pg-purge-old'`},
		{"old call facts", `SELECT COUNT(*) FROM call_facts WHERE call_id = 'pg-purge-old'`},
		{"old read-model diagnostics", `SELECT COUNT(*) FROM call_read_model_diagnostics WHERE call_id = 'pg-purge-old'`},
		{"old profile cache", `SELECT COUNT(*) FROM profile_call_fact_cache WHERE call_id = 'pg-purge-old'`},
		{"old scorecard activity", `SELECT COUNT(*) FROM scorecard_activity WHERE call_id = 'pg-purge-old'`},
		{"old governance suppressed calls", `SELECT COUNT(*) FROM governance_suppressed_calls WHERE call_id = 'pg-purge-old'`},
	} {
		assertPostgresCount(t, ctx, store, check.query, 0)
	}
	assertPostgresCount(t, ctx, store, `SELECT COUNT(*) FROM calls WHERE call_id = 'pg-purge-new'`, 1)
	assertPostgresCount(t, ctx, store, `SELECT COUNT(*) FROM purged_call_ids WHERE call_id = 'pg-purge-old'`, 1)
	if _, err := store.UpsertTranscript(ctx, json.RawMessage(`{"callId":"pg-purge-old","transcript":[{"speakerId":"speaker-old","sentences":[{"start":0,"end":1000,"text":"should not be recreated"}]}]}`)); err == nil || !strings.Contains(err.Error(), "purged by retention policy") {
		t.Fatalf("UpsertTranscript after purge error=%v, want retention tombstone failure", err)
	}
	if _, err := store.UpsertScorecardActivity(ctx, json.RawMessage(`{"answeredScorecardId":"pg-purge-scorecard-after","scorecardId":"pg-purge-scorecard-template","scorecardName":"Retention QA","callId":"pg-purge-old","callStartTime":"2026-04-20T14:00:00Z","reviewMethod":"MANUAL","reviewTime":"2026-04-21T14:00:00Z","answers":[{"isOverall":true,"score":4,"notApplicable":false}]}`)); err == nil || !strings.Contains(err.Error(), "purged by retention policy") {
		t.Fatalf("UpsertScorecardActivity after purge error=%v, want retention tombstone failure", err)
	}
	if _, err := store.UpsertCall(ctx, oldCall); err == nil || !strings.Contains(err.Error(), "purged by retention policy") {
		t.Fatalf("UpsertCall after purge error=%v, want retention tombstone failure", err)
	}
	if err := store.RefreshActiveProfileReadModel(ctx); err != nil {
		t.Fatalf("RefreshActiveProfileReadModel after purge returned error: %v", err)
	}
	assertPostgresCount(t, ctx, store, `SELECT COUNT(*) FROM profile_call_fact_cache WHERE call_id = 'pg-purge-old'`, 0)
	assertPostgresCount(t, ctx, store, `SELECT COUNT(*) FROM profile_call_fact_cache WHERE call_id = 'pg-purge-new'`, 1)
	status, err := store.ReadModelStatus(ctx)
	if err != nil {
		t.Fatalf("ReadModelStatus returned error: %v", err)
	}
	if !status.Ready || status.CallCount != 1 || status.FactCount != 1 || status.StaleReason != "" {
		t.Fatalf("unexpected read model status after purge: %+v", status)
	}
	results, err = store.SearchTranscriptSegments(ctx, "needle", 10)
	if err != nil {
		t.Fatalf("SearchTranscriptSegments after purge returned error: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("search results after purge=%d want 0", len(results))
	}
}

func importSyntheticPostgresProfile(t *testing.T, ctx context.Context, store *Store) (*sqlite.ProfileImportResult, error) {
	t.Helper()
	profileBody := []byte(`
version: 1
name: Synthetic purge profile
objects:
  deal:
    object_types: [Opportunity]
fields:
  deal_stage:
    object: deal
    names: [StageName]
lifecycle:
  open:
    order: 10
    label: Open
    rules:
      - object: deal
        field_name: StageName
        op: equals
        value: Discovery
  closed_won:
    order: 20
  closed_lost:
    order: 30
  post_sales:
    order: 40
  unknown:
    order: 999
`)
	inventory, err := store.ProfileInventory(ctx)
	if err != nil {
		return nil, err
	}
	p, validation, err := profilepkg.ValidateBytes(profileBody, inventory)
	if err != nil {
		return nil, err
	}
	if !validation.Valid {
		return nil, fmt.Errorf("profile validation failed: %+v", validation.Findings)
	}
	canonical, err := profilepkg.CanonicalJSON(p)
	if err != nil {
		return nil, err
	}
	return store.ImportProfile(ctx, sqlite.ProfileImportParams{
		SourcePath:      "synthetic-purge-profile.yaml",
		SourceSHA256:    validation.SourceSHA256,
		CanonicalSHA256: validation.CanonicalSHA256,
		RawYAML:         profileBody,
		CanonicalJSON:   canonical,
		Profile:         p,
		Findings:        validation.Findings,
		ImportedBy:      "test",
	})
}

func TestBusinessPilotAggregatesMatchSQLite(t *testing.T) {
	databaseURL := os.Getenv("GONGCTL_TEST_POSTGRES_URL")
	if databaseURL == "" {
		t.Skip("GONGCTL_TEST_POSTGRES_URL is not set")
	}

	ctx := context.Background()
	pgStore, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer pgStore.Close()
	resetPostgresTestStore(t, ctx, pgStore)

	sqliteStore, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "gong.db"))
	if err != nil {
		t.Fatalf("sqlite.Open returned error: %v", err)
	}
	defer sqliteStore.Close()

	for _, raw := range businessPilotCallPayloads() {
		if _, err := pgStore.UpsertCall(ctx, raw); err != nil {
			t.Fatalf("postgres UpsertCall returned error: %v", err)
		}
		if _, err := sqliteStore.UpsertCall(ctx, raw); err != nil {
			t.Fatalf("sqlite UpsertCall returned error: %v", err)
		}
	}
	for _, raw := range businessPilotTranscriptPayloads() {
		if _, err := pgStore.UpsertTranscript(ctx, raw); err != nil {
			t.Fatalf("postgres UpsertTranscript returned error: %v", err)
		}
		if _, err := sqliteStore.UpsertTranscript(ctx, raw); err != nil {
			t.Fatalf("sqlite UpsertTranscript returned error: %v", err)
		}
	}

	sqliteLifecycle, err := sqliteStore.SummarizeCallsByLifecycle(ctx, sqlite.LifecycleSummaryParams{})
	if err != nil {
		t.Fatalf("sqlite SummarizeCallsByLifecycle returned error: %v", err)
	}
	postgresLifecycle, err := pgStore.SummarizeCallsByLifecycle(ctx, sqlite.LifecycleSummaryParams{})
	if err != nil {
		t.Fatalf("postgres SummarizeCallsByLifecycle returned error: %v", err)
	}
	if got, want := lifecycleSummaryByBucket(postgresLifecycle), lifecycleSummaryByBucket(sqliteLifecycle); !equalLifecycleSummaries(got, want) {
		t.Fatalf("postgres lifecycle summaries=%+v want sqlite %+v", got, want)
	}

	renewal, err := pgStore.SummarizeCallsByLifecycle(ctx, sqlite.LifecycleSummaryParams{Bucket: "renewal"})
	if err != nil {
		t.Fatalf("postgres renewal lifecycle summary returned error: %v", err)
	}
	if len(renewal) != 1 || renewal[0].CallCount != 1 || renewal[0].MissingTranscriptCount != 1 || renewal[0].HighConfidenceCalls != 1 {
		t.Fatalf("unexpected renewal summary: %+v", renewal)
	}

	for _, groupBy := range []string{"lifecycle", "transcript_status"} {
		sqliteFacts, err := sqliteStore.SummarizeCallFacts(ctx, sqlite.CallFactsSummaryParams{GroupBy: groupBy, Limit: 10})
		if err != nil {
			t.Fatalf("sqlite SummarizeCallFacts(%s) returned error: %v", groupBy, err)
		}
		postgresFacts, err := pgStore.SummarizeCallFacts(ctx, sqlite.CallFactsSummaryParams{GroupBy: groupBy, Limit: 10})
		if err != nil {
			t.Fatalf("postgres SummarizeCallFacts(%s) returned error: %v", groupBy, err)
		}
		if got, want := factSummaryByValue(postgresFacts), factSummaryByValue(sqliteFacts); !equalFactSummaries(got, want) {
			t.Fatalf("postgres facts group_by=%s got %+v want sqlite %+v", groupBy, got, want)
		}
	}
}

func TestPostgresGetCallDetailMatchesSQLiteForNormalizedContext(t *testing.T) {
	databaseURL := os.Getenv("GONGCTL_TEST_POSTGRES_URL")
	if databaseURL == "" {
		t.Skip("GONGCTL_TEST_POSTGRES_URL is not set")
	}

	ctx := context.Background()
	pgStore, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer pgStore.Close()
	resetPostgresTestStore(t, ctx, pgStore)

	sqliteStore, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "gong.db"))
	if err != nil {
		t.Fatalf("sqlite.Open returned error: %v", err)
	}
	defer sqliteStore.Close()

	for _, raw := range businessPilotCallPayloads() {
		if _, err := pgStore.UpsertCall(ctx, raw); err != nil {
			t.Fatalf("postgres UpsertCall returned error: %v", err)
		}
		if _, err := sqliteStore.UpsertCall(ctx, raw); err != nil {
			t.Fatalf("sqlite UpsertCall returned error: %v", err)
		}
	}

	got, err := pgStore.GetCallDetail(ctx, "bp-renewal-missing")
	if err != nil {
		t.Fatalf("postgres GetCallDetail returned error: %v", err)
	}
	want, err := sqliteStore.GetCallDetail(ctx, "bp-renewal-missing")
	if err != nil {
		t.Fatalf("sqlite GetCallDetail returned error: %v", err)
	}
	blankCallDetailObjectNames(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("postgres detail=%+v want sqlite %+v", got, want)
	}
	if len(got.CRMObjects) != 2 {
		t.Fatalf("detail CRM object count=%d want 2: %+v", len(got.CRMObjects), got.CRMObjects)
	}
	if _, err := pgStore.GetCallDetail(ctx, "missing-call"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing GetCallDetail error=%v, want not found", err)
	}
	if _, err := pgStore.GetCallDetail(ctx, ""); err == nil || !strings.Contains(err.Error(), "call id is required") {
		t.Fatalf("empty GetCallDetail error=%v, want required", err)
	}
}

func TestPostgresCRMContextInventoryMatchesSQLiteAggregateContract(t *testing.T) {
	databaseURL := os.Getenv("GONGCTL_TEST_POSTGRES_URL")
	if databaseURL == "" {
		t.Skip("GONGCTL_TEST_POSTGRES_URL is not set")
	}

	ctx := context.Background()
	pgStore, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer pgStore.Close()
	resetPostgresTestStore(t, ctx, pgStore)

	sqliteStore, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "gong.db"))
	if err != nil {
		t.Fatalf("sqlite.Open returned error: %v", err)
	}
	defer sqliteStore.Close()

	for _, raw := range businessPilotCallPayloads() {
		if _, err := pgStore.UpsertCall(ctx, raw); err != nil {
			t.Fatalf("postgres UpsertCall returned error: %v", err)
		}
		if _, err := sqliteStore.UpsertCall(ctx, raw); err != nil {
			t.Fatalf("sqlite UpsertCall returned error: %v", err)
		}
	}

	pgObjects, err := pgStore.ListCRMObjectTypes(ctx)
	if err != nil {
		t.Fatalf("postgres ListCRMObjectTypes returned error: %v", err)
	}
	sqliteObjects, err := sqliteStore.ListCRMObjectTypes(ctx)
	if err != nil {
		t.Fatalf("sqlite ListCRMObjectTypes returned error: %v", err)
	}
	if got, want := crmObjectSummaryByType(pgObjects), crmObjectSummaryByType(sqliteObjects); !reflect.DeepEqual(got, want) {
		t.Fatalf("postgres CRM object summaries=%+v want sqlite %+v", got, want)
	}

	pgFields, err := pgStore.ListCRMFields(ctx, "Opportunity", 10)
	if err != nil {
		t.Fatalf("postgres ListCRMFields returned error: %v", err)
	}
	sqliteFields, err := sqliteStore.ListCRMFields(ctx, "Opportunity", 10)
	if err != nil {
		t.Fatalf("sqlite ListCRMFields returned error: %v", err)
	}
	if got, want := crmFieldSummaryByName(pgFields), crmFieldSummaryByName(sqliteFields); !reflect.DeepEqual(got, want) {
		t.Fatalf("postgres CRM field summaries=%+v want sqlite %+v", got, want)
	}
	if _, err := pgStore.ListCRMFields(ctx, "", 10); err == nil || !strings.Contains(err.Error(), "object type is required") {
		t.Fatalf("empty object type error=%v, want required", err)
	}
}

func TestPostgresTranscriptSearchUserVisibleFieldsMatchSQLite(t *testing.T) {
	databaseURL := os.Getenv("GONGCTL_TEST_POSTGRES_URL")
	if databaseURL == "" {
		t.Skip("GONGCTL_TEST_POSTGRES_URL is not set")
	}

	ctx := context.Background()
	pgStore, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer pgStore.Close()
	resetPostgresTestStore(t, ctx, pgStore)

	sqliteStore, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "gong.db"))
	if err != nil {
		t.Fatalf("sqlite.Open returned error: %v", err)
	}
	defer sqliteStore.Close()

	for _, raw := range businessPilotCallPayloads() {
		if _, err := pgStore.UpsertCall(ctx, raw); err != nil {
			t.Fatalf("postgres UpsertCall returned error: %v", err)
		}
		if _, err := sqliteStore.UpsertCall(ctx, raw); err != nil {
			t.Fatalf("sqlite UpsertCall returned error: %v", err)
		}
	}
	for _, raw := range businessPilotTranscriptPayloads() {
		if _, err := pgStore.UpsertTranscript(ctx, raw); err != nil {
			t.Fatalf("postgres UpsertTranscript returned error: %v", err)
		}
		if _, err := sqliteStore.UpsertTranscript(ctx, raw); err != nil {
			t.Fatalf("sqlite UpsertTranscript returned error: %v", err)
		}
	}

	got, err := pgStore.SearchTranscriptSegments(ctx, "contract review", 10)
	if err != nil {
		t.Fatalf("postgres SearchTranscriptSegments returned error: %v", err)
	}
	want, err := sqliteStore.SearchTranscriptSegments(ctx, "contract review", 10)
	if err != nil {
		t.Fatalf("sqlite SearchTranscriptSegments returned error: %v", err)
	}
	if len(got) != 1 || len(want) != 1 {
		t.Fatalf("postgres rows=%+v sqlite rows=%+v want one row each", got, want)
	}
	if got[0].CallID != want[0].CallID || got[0].SpeakerID != want[0].SpeakerID || got[0].SegmentIndex != want[0].SegmentIndex || got[0].StartMS != want[0].StartMS || got[0].EndMS != want[0].EndMS {
		t.Fatalf("postgres transcript metadata=%+v want sqlite %+v", got[0], want[0])
	}
	if got[0].Text != "" || want[0].Text != "" {
		t.Fatalf("raw text leaked: postgres=%q sqlite=%q", got[0].Text, want[0].Text)
	}
	if got[0].Snippet == "" || want[0].Snippet == "" {
		t.Fatalf("empty snippet: postgres=%+v sqlite=%+v", got[0], want[0])
	}
}

func TestPostgresSearchTranscriptSegmentsByCRMContextMatchesSQLiteAfterRedaction(t *testing.T) {
	databaseURL := os.Getenv("GONGCTL_TEST_POSTGRES_URL")
	if databaseURL == "" {
		t.Skip("GONGCTL_TEST_POSTGRES_URL is not set")
	}

	ctx := context.Background()
	pgStore, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer pgStore.Close()
	resetPostgresTestStore(t, ctx, pgStore)

	sqliteStore, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "gong.db"))
	if err != nil {
		t.Fatalf("sqlite.Open returned error: %v", err)
	}
	defer sqliteStore.Close()

	calls := []json.RawMessage{
		json.RawMessage(`{"id":"pg-crm-transcript-001","title":"CRM context target call","started":"2026-04-24T12:00:00Z","duration":900,"context":[{"objectType":"Opportunity","id":"opp-crm-context-a","name":"CRM Context A","fields":[{"name":"StageName","value":"Contract Review"}]},{"objectType":"Opportunity","id":"opp-crm-context-b","name":"CRM Context B","fields":[{"name":"StageName","value":"Proposal"}]},{"objectType":"Account","id":"acct-crm-context","name":"CRM Context Account","fields":[{"name":"Industry","value":"Manufacturing"}]}]}`),
		json.RawMessage(`{"id":"pg-crm-transcript-002","title":"CRM context other call","started":"2026-04-25T12:00:00Z","duration":600,"context":{"crmObjects":[{"type":"Opportunity","id":"opp-crm-context-other","name":"CRM Context Other","fields":{"StageName":"Discovery"}}]}}`),
	}
	transcripts := []json.RawMessage{
		json.RawMessage(`{"callId":"pg-crm-transcript-001","transcript":[{"speakerId":"buyer-crm","sentences":[{"start":1000,"end":3000,"text":"Pricing appears once in the CRM context target."}]}]}`),
		json.RawMessage(`{"callId":"pg-crm-transcript-002","transcript":[{"speakerId":"seller-crm","sentences":[{"start":1000,"end":3000,"text":"Pricing appears in the other opportunity."}]}]}`),
	}
	for _, store := range []interface {
		UpsertCall(context.Context, json.RawMessage) (*sqlite.CallRecord, error)
		UpsertTranscript(context.Context, json.RawMessage) (*sqlite.TranscriptRecord, error)
	}{pgStore, sqliteStore} {
		for _, raw := range calls {
			if _, err := store.UpsertCall(ctx, raw); err != nil {
				t.Fatalf("UpsertCall returned error: %v", err)
			}
		}
		for _, raw := range transcripts {
			if _, err := store.UpsertTranscript(ctx, raw); err != nil {
				t.Fatalf("UpsertTranscript returned error: %v", err)
			}
		}
	}

	params := sqlite.TranscriptCRMSearchParams{Query: "pricing", ObjectType: "Opportunity", ObjectID: "opp-crm-context-a", Limit: 10}
	got, err := pgStore.SearchTranscriptSegmentsByCRMContext(ctx, params)
	if err != nil {
		t.Fatalf("postgres SearchTranscriptSegmentsByCRMContext returned error: %v", err)
	}
	want, err := sqliteStore.SearchTranscriptSegmentsByCRMContext(ctx, params)
	if err != nil {
		t.Fatalf("sqlite SearchTranscriptSegmentsByCRMContext returned error: %v", err)
	}
	if len(got) != 1 || got[0].ObjectType != "Opportunity" || got[0].MatchingObjectCount != 1 {
		t.Fatalf("unexpected CRM transcript result: %+v", got)
	}
	if got[0].Title != "" || got[0].ObjectID != "" || got[0].ObjectName != "" {
		t.Fatalf("postgres function leaked object/title fields before MCP redaction: %+v", got[0])
	}
	redactTranscriptCRMSearchResults(got)
	redactTranscriptCRMSearchResults(want)
	if len(got) != len(want) {
		t.Fatalf("postgres CRM transcript row count=%d want sqlite %d: got=%+v want=%+v", len(got), len(want), got, want)
	}
	for idx := range got {
		gotSnippet := got[idx].Snippet
		wantSnippet := want[idx].Snippet
		got[idx].Snippet = ""
		want[idx].Snippet = ""
		if !reflect.DeepEqual(got[idx], want[idx]) {
			t.Fatalf("postgres CRM transcript row=%+v want sqlite redacted %+v", got[idx], want[idx])
		}
		if gotSnippet == "" || wantSnippet == "" {
			t.Fatalf("empty CRM transcript snippet: postgres=%q sqlite=%q", gotSnippet, wantSnippet)
		}
	}
	if len(got) != 1 || got[0].ObjectType != "Opportunity" || got[0].MatchingObjectCount != 1 {
		t.Fatalf("unexpected CRM transcript result: %+v", got)
	}

	allObjects, err := pgStore.SearchTranscriptSegmentsByCRMContext(ctx, sqlite.TranscriptCRMSearchParams{Query: "pricing", ObjectType: "Opportunity", Limit: 10})
	if err != nil {
		t.Fatalf("postgres all-object CRM transcript search returned error: %v", err)
	}
	if len(allObjects) != 2 {
		t.Fatalf("all-object result count=%d want 2: %+v", len(allObjects), allObjects)
	}
	for _, row := range allObjects {
		if row.CallID == "pg-crm-transcript-001" && row.MatchingObjectCount != 2 {
			t.Fatalf("multi-object matching count=%d want 2: %+v", row.MatchingObjectCount, row)
		}
	}

	if _, err := pgStore.SearchTranscriptSegmentsByCRMContext(ctx, sqlite.TranscriptCRMSearchParams{Query: "", ObjectType: "Opportunity", Limit: 10}); err == nil {
		t.Fatal("expected missing query to fail")
	}
	if _, err := pgStore.SearchTranscriptSegmentsByCRMContext(ctx, sqlite.TranscriptCRMSearchParams{Query: "pricing", ObjectType: "", Limit: 10}); err == nil {
		t.Fatal("expected missing object type to fail")
	}

	for _, column := range []string{"call_id", "title", "object_id", "object_name", "object_key", "speaker_id", "field_value_text", "raw_json", "raw_sha256", "text"} {
		if _, err := pgStore.DB().ExecContext(ctx, `SELECT `+column+` FROM gongmcp_search_transcript_segments_by_crm_context('pricing', 'Opportunity', 'opp-crm-context-a', 10)`); err == nil {
			t.Fatalf("CRM transcript search function exposed %s column", column)
		}
	}

	directRows, err := pgStore.DB().QueryContext(ctx, `SELECT started_at, object_type, matching_object_count, segment_index, start_ms, end_ms, snippet FROM gongmcp_search_transcript_segments_by_crm_context('pricing', 'Opportunity', 'opp-crm-context-a', 10)`)
	if err != nil {
		t.Fatalf("direct CRM transcript search function returned error: %v", err)
	}
	defer directRows.Close()
	var directText strings.Builder
	for directRows.Next() {
		var startedAt, objectType, snippet string
		var matchingObjectCount, startMS, endMS int64
		var segmentIndex int
		if err := directRows.Scan(&startedAt, &objectType, &matchingObjectCount, &segmentIndex, &startMS, &endMS, &snippet); err != nil {
			t.Fatalf("scan direct CRM transcript row: %v", err)
		}
		directText.WriteString(fmt.Sprintf("%s|%s|%d|%d|%d|%d|%s", startedAt, objectType, matchingObjectCount, segmentIndex, startMS, endMS, snippet))
	}
	if err := directRows.Err(); err != nil {
		t.Fatalf("direct CRM transcript rows error: %v", err)
	}
	if strings.Contains(directText.String(), "buyer-crm") || strings.Contains(directText.String(), "CRM context target call") || strings.Contains(directText.String(), "opp-crm-context") || strings.Contains(directText.String(), "CRM Context A") {
		t.Fatalf("direct CRM transcript function leaked title/object fields: %s", directText.String())
	}
}

func TestPostgresProfileImportShowAndReadinessMatchesSQLiteMetadata(t *testing.T) {
	databaseURL := os.Getenv("GONGCTL_TEST_POSTGRES_URL")
	if databaseURL == "" {
		t.Skip("GONGCTL_TEST_POSTGRES_URL is not set")
	}

	ctx := context.Background()
	pgStore, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer pgStore.Close()
	resetPostgresTestStore(t, ctx, pgStore)

	sqliteStore, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "gong.db"))
	if err != nil {
		t.Fatalf("sqlite.Open returned error: %v", err)
	}
	defer sqliteStore.Close()

	for _, raw := range businessPilotCallPayloads() {
		if _, err := pgStore.UpsertCall(ctx, raw); err != nil {
			t.Fatalf("postgres UpsertCall returned error: %v", err)
		}
		if _, err := sqliteStore.UpsertCall(ctx, raw); err != nil {
			t.Fatalf("sqlite UpsertCall returned error: %v", err)
		}
	}

	profileBody := []byte(`
version: 1
name: Synthetic profile parity
objects:
  deal:
    object_types: [Opportunity]
  account:
    object_types: [Account]
fields:
  deal_stage:
    object: deal
    names: [StageName]
  account_type:
    object: account
    names: [Account_Type__c]
lifecycle:
  open:
    order: 10
    label: Open
    rules:
      - object: deal
        field_name: StageName
        op: in
        values: ["Discovery & Demo (SQO)", "Demo & Business Case"]
  closed_won:
    order: 20
  closed_lost:
    order: 30
  post_sales:
    order: 40
    rules:
      - field: account_type
        op: iprefix
        value: customer
  unknown:
    order: 999
methodology:
  pain:
    description: Discovery pain
    aliases: [pain]
  empty_methodology:
    description: Empty methodology stays visible
`)
	pgInventory, err := pgStore.ProfileInventory(ctx)
	if err != nil {
		t.Fatalf("postgres ProfileInventory returned error: %v", err)
	}
	p, validation, err := profilepkg.ValidateBytes(profileBody, pgInventory)
	if err != nil {
		t.Fatalf("ValidateBytes returned error: %v", err)
	}
	if !validation.Valid {
		t.Fatalf("profile validation failed: %+v", validation.Findings)
	}
	canonical, err := profilepkg.CanonicalJSON(p)
	if err != nil {
		t.Fatalf("CanonicalJSON returned error: %v", err)
	}
	params := sqlite.ProfileImportParams{
		SourcePath:      "synthetic-profile.yaml",
		SourceSHA256:    validation.SourceSHA256,
		CanonicalSHA256: validation.CanonicalSHA256,
		RawYAML:         profileBody,
		CanonicalJSON:   canonical,
		Profile:         p,
		Findings:        validation.Findings,
		ImportedBy:      "test",
	}
	badParams := params
	badParams.CanonicalSHA256 = strings.Repeat("0", 64)
	if _, err := pgStore.ImportProfile(ctx, badParams); err == nil || !strings.Contains(err.Error(), "canonical_sha256 does not match") {
		t.Fatalf("bad canonical_sha256 error=%v, want mismatch", err)
	}
	pgImport, err := pgStore.ImportProfile(ctx, params)
	if err != nil {
		t.Fatalf("postgres ImportProfile returned error: %v", err)
	}
	sqliteImport, err := sqliteStore.ImportProfile(ctx, params)
	if err != nil {
		t.Fatalf("sqlite ImportProfile returned error: %v", err)
	}
	if !pgImport.Imported || !pgImport.Activated || !sqliteImport.Imported || !sqliteImport.Activated {
		t.Fatalf("unexpected import results: postgres=%+v sqlite=%+v", pgImport, sqliteImport)
	}

	pgProfile, err := pgStore.ActiveBusinessProfile(ctx)
	if err != nil {
		t.Fatalf("postgres ActiveBusinessProfile returned error: %v", err)
	}
	sqliteProfile, err := sqliteStore.ActiveBusinessProfile(ctx)
	if err != nil {
		t.Fatalf("sqlite ActiveBusinessProfile returned error: %v", err)
	}
	if pgProfile.Name != sqliteProfile.Name || pgProfile.CanonicalSHA256 != sqliteProfile.CanonicalSHA256 || len(pgProfile.ObjectConcepts) != len(sqliteProfile.ObjectConcepts) || len(pgProfile.FieldConcepts) != len(sqliteProfile.FieldConcepts) || len(pgProfile.LifecycleBuckets) != len(sqliteProfile.LifecycleBuckets) || len(pgProfile.MethodologyConcepts) != len(sqliteProfile.MethodologyConcepts) {
		t.Fatalf("postgres profile=%+v want sqlite metadata %+v", pgProfile, sqliteProfile)
	}
	if !hasBusinessConcept(pgProfile.MethodologyConcepts, "empty_methodology") {
		t.Fatalf("postgres profile dropped empty-but-valid methodology concept: %+v", pgProfile.MethodologyConcepts)
	}
	doc, err := pgStore.ProfileDocument(ctx, "active")
	if err != nil {
		t.Fatalf("postgres ProfileDocument returned error: %v", err)
	}
	if doc.Meta.CanonicalSHA256 != validation.CanonicalSHA256 || doc.Profile.Name != "Synthetic profile parity" {
		t.Fatalf("unexpected postgres profile document: %+v", doc)
	}
	concepts, err := pgStore.ListBusinessConcepts(ctx)
	if err != nil {
		t.Fatalf("postgres ListBusinessConcepts returned error: %v", err)
	}
	if len(concepts) != len(pgProfile.ObjectConcepts)+len(pgProfile.FieldConcepts)+len(pgProfile.LifecycleBuckets)+len(pgProfile.MethodologyConcepts) {
		t.Fatalf("business concept count=%d profile=%+v", len(concepts), pgProfile)
	}
	summary, err := pgStore.SyncStatusSummary(ctx)
	if err != nil {
		t.Fatalf("postgres SyncStatusSummary returned error: %v", err)
	}
	if !summary.ProfileReadiness.Active || summary.ProfileReadiness.CacheStatus != "fresh" || summary.ProfileReadiness.Status != "partial" {
		t.Fatalf("unexpected postgres profile readiness: %+v", summary.ProfileReadiness)
	}
	if rows, info, err := pgStore.SummarizeCallFactsWithSource(ctx, sqlite.CallFactsSummaryParams{LifecycleSource: sqlite.LifecycleSourceProfile}); err != nil || info == nil || info.LifecycleSource != sqlite.LifecycleSourceProfile || len(rows) == 0 {
		t.Fatalf("profile lifecycle source rows=%+v info=%+v err=%v, want profile-backed result", rows, info, err)
	}
	pgRows, pgInfo, err := pgStore.SummarizeCallFactsWithSource(ctx, sqlite.CallFactsSummaryParams{GroupBy: "deal_stage", LifecycleSource: sqlite.LifecycleSourceProfile, Limit: 10})
	if err != nil {
		t.Fatalf("postgres profile deal_stage summary returned error: %v", err)
	}
	sqliteRows, sqliteInfo, err := sqliteStore.SummarizeCallFactsWithSource(ctx, sqlite.CallFactsSummaryParams{GroupBy: "deal_stage", LifecycleSource: sqlite.LifecycleSourceProfile, Limit: 10})
	if err != nil {
		t.Fatalf("sqlite profile deal_stage summary returned error: %v", err)
	}
	if pgInfo.LifecycleSource != sqlite.LifecycleSourceProfile || sqliteInfo.LifecycleSource != sqlite.LifecycleSourceProfile || len(pgRows) != len(sqliteRows) {
		t.Fatalf("profile source/row count mismatch: postgres rows=%+v info=%+v sqlite rows=%+v info=%+v", pgRows, pgInfo, sqliteRows, sqliteInfo)
	}
	if !reflect.DeepEqual(pgRows, sqliteRows) {
		t.Fatalf("postgres profile deal_stage rows=%+v want sqlite %+v", pgRows, sqliteRows)
	}
	readOnlyView := &Store{db: pgStore.DB(), readOnly: true}
	readOnlyRows, readOnlyInfo, err := readOnlyView.SummarizeCallFactsWithSource(ctx, sqlite.CallFactsSummaryParams{GroupBy: "deal_stage", LifecycleSource: sqlite.LifecycleSourceProfile, Limit: 10})
	if err != nil {
		t.Fatalf("read-only postgres profile deal_stage summary returned error: %v", err)
	}
	if readOnlyInfo.LifecycleSource != sqlite.LifecycleSourceProfile || !reflect.DeepEqual(readOnlyRows, sqliteRows) {
		t.Fatalf("read-only postgres profile rows=%+v info=%+v want sqlite rows %+v", readOnlyRows, readOnlyInfo, sqliteRows)
	}
	var directIdentifierRows int64
	if err := pgStore.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM gongmcp_profile_call_fact_cache($1, $2) WHERE call_id <> '' OR title <> ''`, pgImport.ProfileID, validation.CanonicalSHA256).Scan(&directIdentifierRows); err != nil {
		t.Fatalf("read generic profile cache function identifiers: %v", err)
	}
	if directIdentifierRows == 0 {
		t.Fatal("generic profile cache function returned no identifier-bearing rows")
	}
	var sanitizedIdentifierRows int64
	if err := pgStore.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM gongmcp_profile_call_fact_cache_sanitized($1, $2) WHERE call_id <> '' OR title <> ''`, pgImport.ProfileID, validation.CanonicalSHA256).Scan(&sanitizedIdentifierRows); err != nil {
		t.Fatalf("read sanitized profile cache function identifiers: %v", err)
	}
	if sanitizedIdentifierRows != 0 {
		t.Fatalf("sanitized profile cache function returned %d identifier-bearing rows, want 0", sanitizedIdentifierRows)
	}
	var limitedIdentifierRows int64
	if err := pgStore.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM gongmcp_profile_call_fact_cache_sanitized_limited($1, $2, 1) WHERE call_id <> '' OR title <> ''`, pgImport.ProfileID, validation.CanonicalSHA256).Scan(&limitedIdentifierRows); err != nil {
		t.Fatalf("read limited sanitized profile cache function identifiers: %v", err)
	}
	if limitedIdentifierRows != 0 {
		t.Fatalf("limited sanitized profile cache function returned %d identifier-bearing rows, want 0", limitedIdentifierRows)
	}
	var limitedFactRows int64
	if err := pgStore.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM gongmcp_profile_call_fact_cache_sanitized_limited($1, $2, 1) WHERE started_at <> '' AND lifecycle_bucket <> ''`, pgImport.ProfileID, validation.CanonicalSHA256).Scan(&limitedFactRows); err != nil {
		t.Fatalf("read limited sanitized profile cache function facts: %v", err)
	}
	if limitedFactRows != 1 {
		t.Fatalf("limited sanitized profile cache function returned %d fact rows, want capped 1", limitedFactRows)
	}
	if _, err := pgStore.DB().ExecContext(ctx, `DELETE FROM profile_call_fact_cache WHERE profile_id = $1 AND call_id LIKE 'pg-profile-cap-%'`, pgImport.ProfileID); err != nil {
		t.Fatalf("clear profile cap regression rows: %v", err)
	}
	if _, err := pgStore.DB().ExecContext(ctx, `DELETE FROM profile_lifecycle_rule WHERE profile_id = $1 AND bucket IN ('cap_regression_low', 'cap_regression_high')`, pgImport.ProfileID); err != nil {
		t.Fatalf("clear profile cap regression rules: %v", err)
	}
	if _, err := pgStore.DB().ExecContext(ctx, `
INSERT INTO profile_lifecycle_rule(profile_id, bucket, ordinal, label, description, rule_index, rule_json)
VALUES($1, 'cap_regression_low', 500, 'Cap regression low', '', 0, '{}'::jsonb),
      ($1, 'cap_regression_high', -100, 'Cap regression high', '', 0, '{}'::jsonb)`, pgImport.ProfileID); err != nil {
		t.Fatalf("insert profile cap regression rules: %v", err)
	}
	if _, err := pgStore.DB().ExecContext(ctx, `
INSERT INTO profile_call_fact_cache(
	profile_id, canonical_sha256, call_id, title, started_at, duration_seconds,
	system, direction, scope, purpose, calendar_event_present, transcript_present,
	lifecycle_bucket, lifecycle_confidence, lifecycle_reason, evidence_fields_json,
	deal_count, account_count, field_values_json
)
	SELECT $1::bigint,
	       $2::text,
       'pg-profile-cap-low-' || lpad(gs::text, 4, '0'),
       'Profile cap low ' || gs::text,
       to_char(timestamp '2026-04-01 00:00:00' + (gs || ' minutes')::interval, 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
       120,
       'Upload API',
       'Inbound',
       'Internal',
       '',
       false,
       false,
       'cap_regression_low',
       'low',
       '',
       '[]'::jsonb,
       0,
       0,
       '{}'::jsonb
  FROM generate_series(1, 1000) gs
UNION ALL
	SELECT $1::bigint,
	       $2::text,
       'pg-profile-cap-high-' || lpad(gs::text, 4, '0'),
       'Profile cap high ' || gs::text,
       to_char(timestamp '2020-01-01 00:00:00' + (gs || ' minutes')::interval, 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
       3600,
       'Zoom',
       'Conference',
       'External',
       '',
       false,
       false,
       'cap_regression_high',
       'high',
       'high priority older row outside newest capped helper window',
       '["cap_regression"]'::jsonb,
       1,
       1,
       '{}'::jsonb
  FROM generate_series(1, 5) gs`, pgImport.ProfileID, validation.CanonicalSHA256); err != nil {
		t.Fatalf("insert profile cap regression rows: %v", err)
	}
	if _, err := pgStore.DB().ExecContext(ctx, `UPDATE profile_call_fact_cache_meta SET call_count = (SELECT COUNT(*) FROM profile_call_fact_cache WHERE profile_id = $1 AND canonical_sha256 = $2) WHERE profile_id = $1`, pgImport.ProfileID, validation.CanonicalSHA256); err != nil {
		t.Fatalf("update profile cap regression metadata: %v", err)
	}
	var cappedHighRows int64
	if err := pgStore.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM gongmcp_profile_call_fact_cache_sanitized_limited($1, $2, 1000) WHERE lifecycle_bucket = 'cap_regression_high'`, pgImport.ProfileID, validation.CanonicalSHA256).Scan(&cappedHighRows); err != nil {
		t.Fatalf("read capped helper cap regression high rows: %v", err)
	}
	if cappedHighRows != 0 {
		t.Fatalf("capped helper returned %d old high-priority cap regression rows, want 0", cappedHighRows)
	}
	readOnlyScopedView := &Store{db: pgStore.DB(), readOnly: true, readOnlyOptions: ReadOnlyOptions{EnforceAllowedColumnBoundary: true}}
	capSummary, _, err := readOnlyScopedView.SummarizeCallsByLifecycleWithSource(ctx, sqlite.LifecycleSummaryParams{Bucket: "cap_regression_low", LifecycleSource: sqlite.LifecycleSourceProfile})
	if err != nil {
		t.Fatalf("scoped read-only lifecycle summary cap regression returned error: %v", err)
	}
	if len(capSummary) != 1 || capSummary[0].CallCount != 1000 || capSummary[0].MissingTranscriptCount != 1000 {
		t.Fatalf("scoped read-only lifecycle summary cap regression rows=%+v, want full 1000 low rows", capSummary)
	}
	capBacklog, _, err := readOnlyScopedView.PrioritizeTranscriptsByLifecycleWithSource(ctx, sqlite.LifecycleTranscriptPriorityParams{LifecycleSource: sqlite.LifecycleSourceProfile, Limit: 1})
	if err != nil {
		t.Fatalf("scoped read-only transcript backlog cap regression returned error: %v", err)
	}
	if len(capBacklog) != 1 || capBacklog[0].Bucket != "cap_regression_high" || capBacklog[0].CallID != "" || capBacklog[0].Title != "" {
		t.Fatalf("scoped read-only transcript backlog cap regression rows=%+v, want redacted older high-priority row outside capped helper window", capBacklog)
	}
	var sanitizedProfileJSON string
	if err := pgStore.DB().QueryRowContext(ctx, `SELECT profile_json::text FROM gongmcp_active_business_profile_sanitized()`).Scan(&sanitizedProfileJSON); err != nil {
		t.Fatalf("read sanitized active profile function: %v", err)
	}
	for _, forbidden := range []string{"source_path", "source_sha256", "canonical_sha256", "imported_by", "canonical_json", "evidence", "tracker_ids", "scorecard_question_ids"} {
		if strings.Contains(sanitizedProfileJSON, forbidden) {
			t.Fatalf("sanitized active profile exposed %q: %s", forbidden, sanitizedProfileJSON)
		}
	}
	var directFactRows int64
	if err := pgStore.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM gongmcp_profile_call_fact_cache_sanitized($1, $2) WHERE started_at <> '' AND lifecycle_bucket <> ''`, pgImport.ProfileID, validation.CanonicalSHA256).Scan(&directFactRows); err != nil {
		t.Fatalf("read sanitized profile cache function facts: %v", err)
	}
	if directFactRows == 0 {
		t.Fatal("profile cache function returned no non-identifier fact rows")
	}
	pgOpen, pgOpenInfo, err := pgStore.SearchCallsByLifecycleWithSource(ctx, sqlite.LifecycleCallSearchParams{Bucket: "open", LifecycleSource: sqlite.LifecycleSourceAuto, Limit: 10})
	if err != nil {
		t.Fatalf("postgres auto profile lifecycle search returned error: %v", err)
	}
	if pgOpenInfo.LifecycleSource != sqlite.LifecycleSourceProfile || len(pgOpen) == 0 {
		t.Fatalf("postgres auto profile lifecycle rows=%+v info=%+v, want profile-backed rows", pgOpen, pgOpenInfo)
	}
	if _, err := pgStore.DB().ExecContext(ctx, `UPDATE profile_call_fact_cache SET lifecycle_bucket = 'unknown', lifecycle_confidence = 'low' WHERE call_id = $1`, pgOpen[0].CallID); err != nil {
		t.Fatalf("corrupt profile cache row: %v", err)
	}
	if _, err := pgStore.RebuildReadModel(ctx); err != nil {
		t.Fatalf("RebuildReadModel after profile cache corruption returned error: %v", err)
	}
	pgOpenAfterRebuild, _, err := pgStore.SearchCallsByLifecycleWithSource(ctx, sqlite.LifecycleCallSearchParams{Bucket: "open", LifecycleSource: sqlite.LifecycleSourceProfile, Limit: 10})
	if err != nil {
		t.Fatalf("profile lifecycle search after read-model rebuild returned error: %v", err)
	}
	if len(pgOpenAfterRebuild) == 0 || pgOpenAfterRebuild[0].CallID != pgOpen[0].CallID {
		t.Fatalf("profile cache was not force-rebuilt: before=%+v after=%+v", pgOpen, pgOpenAfterRebuild)
	}
	if _, _, err := pgStore.SummarizeCallFactsWithSource(ctx, sqlite.CallFactsSummaryParams{LifecycleSource: sqlite.LifecycleSourceProfile, Scope: "bad-scope"}); err == nil || !strings.Contains(err.Error(), "scope must be one of") {
		t.Fatalf("bad profile scope error=%v, want validation error", err)
	}
	if _, _, err := pgStore.SummarizeCallFactsWithSource(ctx, sqlite.CallFactsSummaryParams{LifecycleSource: sqlite.LifecycleSourceProfile, TranscriptStatus: "all"}); err == nil || !strings.Contains(err.Error(), "transcript_status must be one of") {
		t.Fatalf("bad profile transcript_status error=%v, want validation error", err)
	}
	if _, err := pgStore.UpsertCall(ctx, json.RawMessage(`{"id":"pg-profile-stale-001","title":"Stale profile cache","started":"2026-03-01T12:00:00Z","duration":120}`)); err != nil {
		t.Fatalf("postgres stale UpsertCall returned error: %v", err)
	}
	if _, _, err := readOnlyView.SearchCallsByLifecycleWithSource(ctx, sqlite.LifecycleCallSearchParams{LifecycleSource: sqlite.LifecycleSourceProfile, Limit: 10}); err == nil || !strings.Contains(err.Error(), "profile read model is missing or stale") {
		t.Fatalf("read-only stale profile cache error=%v, want fail-closed stale cache", err)
	}
}

func hasBusinessConcept(concepts []sqlite.BusinessConcept, name string) bool {
	for _, concept := range concepts {
		if concept.Name == name {
			return true
		}
	}
	return false
}

func TestPostgresSearchCallsRawSafeFiltersMatchSQLite(t *testing.T) {
	databaseURL := os.Getenv("GONGCTL_TEST_POSTGRES_URL")
	if databaseURL == "" {
		t.Skip("GONGCTL_TEST_POSTGRES_URL is not set")
	}

	ctx := context.Background()
	pgStore, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer pgStore.Close()
	resetPostgresTestStore(t, ctx, pgStore)

	sqliteStore, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "gong.db"))
	if err != nil {
		t.Fatalf("sqlite.Open returned error: %v", err)
	}
	defer sqliteStore.Close()

	for _, raw := range businessPilotCallPayloads() {
		if _, err := pgStore.UpsertCall(ctx, raw); err != nil {
			t.Fatalf("postgres UpsertCall returned error: %v", err)
		}
		if _, err := sqliteStore.UpsertCall(ctx, raw); err != nil {
			t.Fatalf("sqlite UpsertCall returned error: %v", err)
		}
	}
	for _, raw := range businessPilotTranscriptPayloads() {
		if _, err := pgStore.UpsertTranscript(ctx, raw); err != nil {
			t.Fatalf("postgres UpsertTranscript returned error: %v", err)
		}
		if _, err := sqliteStore.UpsertTranscript(ctx, raw); err != nil {
			t.Fatalf("sqlite UpsertTranscript returned error: %v", err)
		}
	}

	cases := []struct {
		name   string
		params sqlite.CallSearchParams
	}{
		{name: "crm object type", params: sqlite.CallSearchParams{CRMObjectType: "Opportunity", Limit: 10}},
		{name: "crm object id", params: sqlite.CallSearchParams{CRMObjectID: "opp-renewal", Limit: 10}},
		{name: "combined crm object", params: sqlite.CallSearchParams{CRMObjectType: "Opportunity", CRMObjectID: "opp-renewal", Limit: 10}},
		{name: "from date", params: sqlite.CallSearchParams{FromDate: "2026-02-12", Limit: 10}},
		{name: "inclusive to date", params: sqlite.CallSearchParams{ToDate: "2026-02-12", Limit: 10}},
		{name: "lifecycle", params: sqlite.CallSearchParams{LifecycleBucket: "renewal", Limit: 10}},
		{name: "scope", params: sqlite.CallSearchParams{Scope: "External", Limit: 10}},
		{name: "system", params: sqlite.CallSearchParams{System: "Zoom", Limit: 10}},
		{name: "direction", params: sqlite.CallSearchParams{Direction: "Conference", Limit: 10}},
		{name: "transcript present", params: sqlite.CallSearchParams{TranscriptStatus: "present", Limit: 10}},
		{name: "transcript missing", params: sqlite.CallSearchParams{TranscriptStatus: "missing", Limit: 10}},
		{name: "combined business filter", params: sqlite.CallSearchParams{CRMObjectType: "Opportunity", LifecycleBucket: "renewal", Scope: "External", System: "Zoom", Direction: "Conference", TranscriptStatus: "missing", FromDate: "2026-02-01", ToDate: "2026-02-28", Limit: 10}},
		{name: "injection shaped object id", params: sqlite.CallSearchParams{CRMObjectID: "opp-core' OR 1=1 --", Limit: 10}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sqliteRows, err := sqliteStore.SearchCallsRaw(ctx, tc.params)
			if err != nil {
				t.Fatalf("sqlite SearchCallsRaw returned error: %v", err)
			}
			postgresRows, err := pgStore.SearchCallsRaw(ctx, tc.params)
			if err != nil {
				t.Fatalf("postgres SearchCallsRaw returned error: %v", err)
			}
			got := rawCallIDs(t, postgresRows)
			want := rawCallIDs(t, sqliteRows)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("postgres call IDs=%v want sqlite %v", got, want)
			}
		})
	}
	if _, err := pgStore.SearchCallsRaw(ctx, sqlite.CallSearchParams{FromDate: "2026-02-99"}); err == nil {
		t.Fatal("bad from_date unexpectedly succeeded")
	}
	if _, err := pgStore.SearchCallsRaw(ctx, sqlite.CallSearchParams{FromDate: "2026-03-01", ToDate: "2026-02-01"}); err == nil {
		t.Fatal("inverted date range unexpectedly succeeded")
	}
	if _, err := pgStore.SearchCallsRaw(ctx, sqlite.CallSearchParams{LifecycleBucket: "not_real"}); err == nil {
		t.Fatal("bad lifecycle bucket unexpectedly succeeded")
	}
	if _, err := pgStore.SearchCallsRaw(ctx, sqlite.CallSearchParams{TranscriptStatus: "all"}); err == nil {
		t.Fatal("transcript_status=all unexpectedly succeeded")
	}
}

func TestPostgresMigrationCreatesNormalizedReadModelTables(t *testing.T) {
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

	var version int
	if err := store.DB().QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM gongctl_schema_migrations`).Scan(&version); err != nil {
		t.Fatalf("read migration version: %v", err)
	}
	if version < 2 {
		t.Fatalf("postgres migration version=%d want at least 2", version)
	}
	for _, table := range []string{"call_context_objects", "call_context_fields", "call_facts", "profile_meta", "profile_object_alias", "profile_field_concept", "profile_lifecycle_rule", "profile_methodology_concept", "profile_validation_warning", "profile_call_fact_cache_meta", "profile_call_fact_cache", "purged_call_ids", "governance_policy_state", "governance_suppressed_calls", "governance_ingest_skipped_calls", "gong_settings", "scorecard_activity", "crm_integrations", "crm_schema_objects", "crm_schema_fields"} {
		var exists bool
		if err := store.DB().QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = $1)`, table).Scan(&exists); err != nil {
			t.Fatalf("check table %s: %v", table, err)
		}
		if !exists {
			t.Fatalf("table %s does not exist", table)
		}
	}
	for _, index := range []string{"idx_pg_call_facts_lifecycle", "idx_pg_call_facts_transcript_status", "idx_pg_call_facts_search_filters", "idx_pg_calls_started_call_id", "idx_pg_call_context_objects_type_object_call", "idx_pg_call_context_fields_name_call_key_value", "idx_pg_profile_call_fact_cache_bucket", "idx_pg_profile_call_fact_cache_started", "idx_pg_profile_call_fact_cache_call", "idx_pg_crm_integrations_provider_name", "idx_pg_crm_schema_objects_type", "idx_pg_crm_schema_fields_object_name"} {
		var exists bool
		if err := store.DB().QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname = current_schema() AND indexname = $1)`, index).Scan(&exists); err != nil {
			t.Fatalf("check index %s: %v", index, err)
		}
		if !exists {
			t.Fatalf("index %s does not exist", index)
		}
	}
	for _, column := range []string{"participant_title_present", "loss_reason_present"} {
		var dataType string
		var nullable string
		if err := store.DB().QueryRowContext(ctx, `SELECT data_type, is_nullable FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'call_facts' AND column_name = $1`, column).Scan(&dataType, &nullable); err != nil {
			t.Fatalf("check call_facts.%s: %v", column, err)
		}
		if dataType != "boolean" || nullable != "NO" {
			t.Fatalf("call_facts.%s type/nullability=%s/%s, want boolean/NO", column, dataType, nullable)
		}
	}
	for _, function := range []string{"gongmcp_active_business_profile_sanitized", "gongmcp_profile_call_fact_cache", "gongmcp_profile_call_fact_cache_sanitized", "gongmcp_profile_call_fact_cache_sanitized_limited", "gongmcp_profile_call_fact_cache_meta", "gongmcp_profile_call_fact_cache_meta_sanitized", "gongmcp_profile_call_fact_summary", "gongmcp_profile_call_fact_summary_sanitized", "gongmcp_profile_lifecycle_summary_sanitized", "gongmcp_profile_transcript_backlog_sanitized", "gongmcp_profile_data_fingerprint", "gongmcp_governance_data_fingerprint", "gongmcp_governance_policy_state", "gongmcp_governance_suppressed_call_ids", "gongmcp_scorecard_activity_summary", "gongmcp_scorecard_activity_totals", "gongmcp_crm_object_type_summary", "gongmcp_crm_field_summary_sanitized", "gongmcp_search_transcript_segments_by_crm_context", "gongmcp_crm_field_value_search", "gongmcp_unmapped_crm_field_inventory", "gongmcp_late_stage_call_counts", "gongmcp_late_stage_stage_counts", "gongmcp_late_stage_signal_inventory", "gongmcp_opportunities_missing_transcripts", "gongmcp_opportunity_call_summary", "gongmcp_crm_field_population_matrix", "gongmcp_compare_lifecycle_crm_fields", "gongmcp_missing_transcripts", "gongmcp_missing_transcript_count", "gongmcp_search_transcript_segments_sanitized", "gongmcp_search_transcript_segments_by_call_facts_sanitized", "gongmcp_search_transcript_quotes_with_attribution_sanitized", "gongmcp_business_analysis_calls_sanitized", "gongmcp_business_analysis_evidence_sanitized", "gongmcp_cache_purge_plan"} {
		var exists bool
		if err := store.DB().QueryRowContext(ctx, `SELECT EXISTS (
	SELECT 1
	  FROM pg_proc p
	  JOIN pg_namespace n ON n.oid = p.pronamespace
	 WHERE n.nspname = current_schema()
	   AND p.proname = $1
)`, function).Scan(&exists); err != nil {
			t.Fatalf("check function %s: %v", function, err)
		}
		if !exists {
			t.Fatalf("function %s does not exist", function)
		}
	}
	for _, column := range []string{"missing_fact_call_count", "orphan_fact_count"} {
		var exists bool
		if err := store.DB().QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'postgres_read_model_state' AND column_name = $1)`, column).Scan(&exists); err != nil {
			t.Fatalf("check read model column %s: %v", column, err)
		}
		if !exists {
			t.Fatalf("read model state column %s does not exist", column)
		}
	}
	for _, trigger := range []string{
		"gongctl_read_model_calls_counter",
		"gongctl_read_model_calls_update_resync",
		"gongctl_read_model_calls_truncate_resync",
		"gongctl_read_model_facts_counter",
		"gongctl_read_model_facts_update_resync",
		"gongctl_read_model_facts_truncate_resync",
	} {
		var exists bool
		if err := store.DB().QueryRowContext(ctx, `SELECT EXISTS (
	SELECT 1
	  FROM pg_trigger t
	  JOIN pg_class c ON c.oid = t.tgrelid
	  JOIN pg_namespace n ON n.oid = c.relnamespace
	 WHERE n.nspname = current_schema()
	   AND t.tgname = $1
	   AND NOT t.tgisinternal
)`, trigger).Scan(&exists); err != nil {
			t.Fatalf("check read model trigger %s: %v", trigger, err)
		}
		if !exists {
			t.Fatalf("read model trigger %s does not exist", trigger)
		}
	}
}

func TestPostgresMissingTranscriptFunctionsReferenceGovernanceIngestLedger(t *testing.T) {
	for name, sqlText := range map[string]string{
		"missing_transcripts":      postgresMissingTranscriptsFunctionSQL,
		"missing_transcript_count": postgresMissingTranscriptCountFunctionSQL,
	} {
		if !strings.Contains(sqlText, "governance_ingest_skipped_calls") {
			t.Fatalf("%s SQL does not reference governance ingest ledger", name)
		}
		if !strings.Contains(sqlText, "gisc.call_id IS NULL") {
			t.Fatalf("%s SQL does not exclude ledgered calls", name)
		}
	}
}

func TestPostgresReadOnlyOpenRejectsStaleMigrationVersion(t *testing.T) {
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

	governanceMigrationVersion := 0
	for idx, migration := range migrations {
		if strings.Contains(migration, "CREATE TABLE IF NOT EXISTS governance_policy_state") {
			governanceMigrationVersion = idx + 1
			break
		}
	}
	if governanceMigrationVersion == 0 {
		t.Fatal("governance migration not found")
	}
	if _, err := store.DB().ExecContext(ctx, `DELETE FROM gongctl_schema_migrations WHERE version >= $1`, governanceMigrationVersion); err != nil {
		t.Fatalf("delete governance-and-later migration versions: %v", err)
	}
	if _, err := store.DB().ExecContext(ctx, `DROP TABLE IF EXISTS governance_suppressed_calls, governance_policy_state`); err != nil {
		t.Fatalf("drop latest governance tables: %v", err)
	}
	defer func() {
		if err := store.Migrate(ctx); err != nil {
			t.Fatalf("restore migrations after stale-version test: %v", err)
		}
	}()
	if _, err := OpenStatus(ctx, databaseURL); err == nil || !strings.Contains(err.Error(), "schema version") {
		t.Fatalf("OpenStatus stale migration error=%v, want schema version guidance", err)
	}
}

func TestPostgresMigrationAppendsBusinessPilotScopedHelpers(t *testing.T) {
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

	helperMigrationVersion := 0
	for idx, migration := range migrations {
		if strings.Contains(migration, "gongmcp_profile_transcript_backlog_sanitized") {
			helperMigrationVersion = idx + 1
		}
	}
	if helperMigrationVersion == 0 {
		t.Fatal("business-pilot scoped helper migration not found")
	}
	if _, err := store.DB().ExecContext(ctx, `DELETE FROM gongctl_schema_migrations WHERE version >= $1`, helperMigrationVersion); err != nil {
		t.Fatalf("delete helper migration versions: %v", err)
	}
	if _, err := store.DB().ExecContext(ctx, `
DROP FUNCTION IF EXISTS gongmcp_profile_call_fact_summary_sanitized(bigint, text, text, text, text, text, text, text, integer);
DROP FUNCTION IF EXISTS gongmcp_profile_lifecycle_summary_sanitized(bigint, text, text);
DROP FUNCTION IF EXISTS gongmcp_profile_transcript_backlog_sanitized(bigint, text, text, text, text, text, text, text, integer);
`); err != nil {
		t.Fatalf("drop scoped helper functions: %v", err)
	}
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("rerun migrations returned error: %v", err)
	}
	for _, function := range []string{"gongmcp_profile_call_fact_summary_sanitized", "gongmcp_profile_lifecycle_summary_sanitized", "gongmcp_profile_transcript_backlog_sanitized"} {
		var exists bool
		if err := store.DB().QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1
		  FROM pg_proc p
		  JOIN pg_namespace n ON n.oid = p.pronamespace
		 WHERE n.nspname = current_schema()
		   AND p.proname = $1
	)`, function).Scan(&exists); err != nil {
			t.Fatalf("check function %s: %v", function, err)
		}
		if !exists {
			t.Fatalf("function %s does not exist after appended migration", function)
		}
	}
}

func TestPostgresBusinessAnalysisPhase5BMatchesSQLiteRepresentativeSlice(t *testing.T) {
	databaseURL := os.Getenv("GONGCTL_TEST_POSTGRES_URL")
	if databaseURL == "" {
		t.Skip("GONGCTL_TEST_POSTGRES_URL is not set")
	}

	ctx := context.Background()
	pgStore, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer pgStore.Close()
	resetPostgresTestStore(t, ctx, pgStore)

	sqlitePath := filepath.Join(t.TempDir(), "gongctl.sqlite")
	sqliteStore, err := sqlite.Open(ctx, sqlitePath)
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer sqliteStore.Close()

	seedPhase5BBusinessAnalysisFixtures(t, ctx, pgStore, sqliteStore)

	callFactsParams := sqlite.TranscriptCallFactsSearchParams{
		Query:    "implementation",
		FromDate: "2026-01-01",
		ToDate:   "2026-03-31",
		Scope:    "External",
		Limit:    10,
	}
	pgCallFacts, err := pgStore.SearchTranscriptSegmentsByCallFacts(ctx, callFactsParams)
	if err != nil {
		t.Fatalf("postgres SearchTranscriptSegmentsByCallFacts: %v", err)
	}
	sqliteCallFacts, err := sqliteStore.SearchTranscriptSegmentsByCallFacts(ctx, callFactsParams)
	if err != nil {
		t.Fatalf("sqlite SearchTranscriptSegmentsByCallFacts: %v", err)
	}
	if got, want := transcriptCallFactIDs(pgCallFacts), transcriptCallFactIDs(sqliteCallFacts); !reflect.DeepEqual(got, want) {
		t.Fatalf("postgres call-fact transcript ids=%v want sqlite %v", got, want)
	}
	if len(pgCallFacts) != 1 || !strings.Contains(strings.ToLower(pgCallFacts[0].ContextExcerpt), "implementation timeline") {
		t.Fatalf("unexpected postgres call-fact transcript result: %+v", pgCallFacts)
	}

	attributionParams := sqlite.TranscriptAttributionSearchParams{
		Query:            "implementation",
		FromDate:         "2026-01-01",
		ToDate:           "2026-03-31",
		Industry:         "manufacturing",
		OpportunityStage: "Discovery",
		Limit:            10,
	}
	pgAttributed, err := pgStore.SearchTranscriptQuotesWithAttribution(ctx, attributionParams)
	if err != nil {
		t.Fatalf("postgres SearchTranscriptQuotesWithAttribution: %v", err)
	}
	sqliteAttributed, err := sqliteStore.SearchTranscriptQuotesWithAttribution(ctx, attributionParams)
	if err != nil {
		t.Fatalf("sqlite SearchTranscriptQuotesWithAttribution: %v", err)
	}
	if got, want := transcriptAttributionIDs(pgAttributed), transcriptAttributionIDs(sqliteAttributed); !reflect.DeepEqual(got, want) {
		t.Fatalf("postgres attributed ids=%v want sqlite %v", got, want)
	}
	if len(pgAttributed) != 1 || pgAttributed[0].AccountName != "" || pgAttributed[0].OpportunityName != "" || pgAttributed[0].AccountIndustry != "Manufacturing" || pgAttributed[0].OpportunityStage != "Discovery" || pgAttributed[0].OpportunityType != "New Business" {
		t.Fatalf("unexpected postgres attribution result: %+v", pgAttributed)
	}
	if _, err := pgStore.SearchTranscriptQuotesWithAttribution(ctx, sqlite.TranscriptAttributionSearchParams{Query: "implementation", AccountQuery: "Example"}); err == nil || !strings.Contains(err.Error(), "account_query is not supported") {
		t.Fatalf("postgres attribution account_query error=%v, want unsupported account_query error", err)
	}
	pgServingView := &Store{db: pgStore.DB(), readOnly: true, readOnlyOptions: ReadOnlyOptions{EnforceAllowedColumnBoundary: true, AllowAccountQuery: true}}
	pgServingAttributed, err := pgServingView.SearchTranscriptQuotesWithAttribution(ctx, sqlite.TranscriptAttributionSearchParams{Query: "implementation", AccountQuery: "Example", Limit: 10})
	if err != nil {
		t.Fatalf("redacted serving Postgres SearchTranscriptQuotesWithAttribution account_query: %v", err)
	}
	if len(pgServingAttributed) != 1 || pgServingAttributed[0].AccountName != "" || pgServingAttributed[0].CallID != "" || pgServingAttributed[0].Title != "" {
		t.Fatalf("redacted serving Postgres account_query attribution result not sanitized: %+v", pgServingAttributed)
	}

	filter := sqlite.BusinessAnalysisFilter{Quarter: "2026-Q1", Industry: "manufacturing", Limit: 10}
	pgCalls, err := pgStore.SearchBusinessAnalysisCalls(ctx, sqlite.BusinessAnalysisCallSearchParams{Filter: filter, Limit: 10})
	if err != nil {
		t.Fatalf("postgres SearchBusinessAnalysisCalls: %v", err)
	}
	sqliteCalls, err := sqliteStore.SearchBusinessAnalysisCalls(ctx, sqlite.BusinessAnalysisCallSearchParams{Filter: filter, Limit: 10})
	if err != nil {
		t.Fatalf("sqlite SearchBusinessAnalysisCalls: %v", err)
	}
	if pgCalls.Summary.CallCount != sqliteCalls.Summary.CallCount || pgCalls.Summary.TranscriptCount != sqliteCalls.Summary.TranscriptCount {
		t.Fatalf("postgres summary=%+v want sqlite %+v", pgCalls.Summary, sqliteCalls.Summary)
	}
	if got, want := businessAnalysisCallIDs(pgCalls.Rows), businessAnalysisCallIDs(sqliteCalls.Rows); !reflect.DeepEqual(got, want) {
		t.Fatalf("postgres business call ids=%v want sqlite %v", got, want)
	}
	titleFilter := sqlite.BusinessAnalysisFilter{Quarter: "2026-Q1", ParticipantTitleQuery: "VP Operations", Limit: 10}
	pgTitleCalls, err := pgStore.SearchBusinessAnalysisCalls(ctx, sqlite.BusinessAnalysisCallSearchParams{Filter: titleFilter, Limit: 10})
	if err != nil {
		t.Fatalf("postgres SearchBusinessAnalysisCalls participant_title_query: %v", err)
	}
	sqliteTitleCalls, err := sqliteStore.SearchBusinessAnalysisCalls(ctx, sqlite.BusinessAnalysisCallSearchParams{Filter: titleFilter, Limit: 10})
	if err != nil {
		t.Fatalf("sqlite SearchBusinessAnalysisCalls participant_title_query: %v", err)
	}
	if got, want := businessAnalysisCallIDs(pgTitleCalls.Rows), businessAnalysisCallIDs(sqliteTitleCalls.Rows); !reflect.DeepEqual(got, want) {
		t.Fatalf("postgres participant_title_query business call ids=%v want sqlite %v", got, want)
	}
	missingTitleFilter := sqlite.BusinessAnalysisFilter{Quarter: "2026-Q1", ParticipantTitleQuery: "Finance", Limit: 10}
	pgMissingTitleCalls, err := pgStore.SearchBusinessAnalysisCalls(ctx, sqlite.BusinessAnalysisCallSearchParams{Filter: missingTitleFilter, Limit: 10})
	if err != nil {
		t.Fatalf("postgres SearchBusinessAnalysisCalls missing participant_title_query: %v", err)
	}
	if pgMissingTitleCalls.Summary.CallCount != 0 || len(pgMissingTitleCalls.Rows) != 0 {
		t.Fatalf("postgres participant_title_query matched by presence instead of title text: summary=%+v rows=%+v", pgMissingTitleCalls.Summary, pgMissingTitleCalls.Rows)
	}
	if _, err := pgStore.SearchBusinessAnalysisCalls(ctx, sqlite.BusinessAnalysisCallSearchParams{Filter: sqlite.BusinessAnalysisFilter{AccountQuery: "Example"}, Limit: 10}); err == nil || !strings.Contains(err.Error(), "account_query is not supported") {
		t.Fatalf("postgres business account_query error=%v, want unsupported account_query error", err)
	}
	pgServingAccountCalls, err := pgServingView.SearchBusinessAnalysisCalls(ctx, sqlite.BusinessAnalysisCallSearchParams{Filter: sqlite.BusinessAnalysisFilter{AccountQuery: "Example", Limit: 10}, Limit: 10})
	if err != nil {
		t.Fatalf("redacted serving Postgres SearchBusinessAnalysisCalls account_query: %v", err)
	}
	if pgServingAccountCalls.Summary.CallCount != 1 || len(pgServingAccountCalls.Rows) != 1 || pgServingAccountCalls.Rows[0].CallID != "" || pgServingAccountCalls.Rows[0].Title != "" {
		t.Fatalf("redacted serving Postgres account_query calls not sanitized: summary=%+v rows=%+v", pgServingAccountCalls.Summary, pgServingAccountCalls.Rows)
	}

	pgEvidence, err := pgStore.SearchBusinessAnalysisEvidence(ctx, sqlite.BusinessAnalysisEvidenceSearchParams{Filter: filter, Query: "implementation", Limit: 10})
	if err != nil {
		t.Fatalf("postgres SearchBusinessAnalysisEvidence: %v", err)
	}
	if len(pgEvidence) != 1 || pgEvidence[0].CallID != "pg-ba-001" || !strings.Contains(strings.ToLower(pgEvidence[0].ContextExcerpt), "implementation timeline") {
		t.Fatalf("unexpected postgres evidence: %+v", pgEvidence)
	}
	if pgEvidence[0].AccountName != "" || pgEvidence[0].OpportunityName != "" || pgEvidence[0].OpportunityProbability != "" || pgEvidence[0].OpportunityCloseDate != "" {
		t.Fatalf("postgres evidence exposed raw CRM values: %+v", pgEvidence[0])
	}

	pgDimension, err := pgStore.SummarizeBusinessAnalysisDimension(ctx, sqlite.BusinessAnalysisDimensionSummaryParams{Filter: filter, Dimension: "industry", Limit: 10})
	if err != nil {
		t.Fatalf("postgres SummarizeBusinessAnalysisDimension: %v", err)
	}
	if len(pgDimension) != 1 || pgDimension[0].Value != "Manufacturing" || pgDimension[0].CallCount != 1 {
		t.Fatalf("unexpected postgres dimension summary: %+v", pgDimension)
	}
	pgPersonaDimension, err := pgStore.SummarizeBusinessAnalysisDimension(ctx, sqlite.BusinessAnalysisDimensionSummaryParams{Filter: filter, Dimension: "persona", Limit: 10})
	if err != nil {
		t.Fatalf("postgres persona SummarizeBusinessAnalysisDimension: %v", err)
	}
	if len(pgPersonaDimension) != 1 || pgPersonaDimension[0].Value != "participant_title_present" || pgPersonaDimension[0].CallCount != 1 {
		t.Fatalf("unexpected postgres persona dimension summary: %+v", pgPersonaDimension)
	}
	if strings.Contains(pgPersonaDimension[0].Value, "VP Operations") {
		t.Fatalf("postgres persona dimension exposed raw title value: %+v", pgPersonaDimension)
	}
	pgLossReasonDimension, err := pgStore.SummarizeBusinessAnalysisDimension(ctx, sqlite.BusinessAnalysisDimensionSummaryParams{Filter: filter, Dimension: "loss_reason", Limit: 10})
	if err != nil {
		t.Fatalf("postgres loss_reason SummarizeBusinessAnalysisDimension: %v", err)
	}
	if len(pgLossReasonDimension) != 1 || pgLossReasonDimension[0].Value != "loss_reason_present" || pgLossReasonDimension[0].CallCount != 1 {
		t.Fatalf("unexpected postgres loss_reason dimension summary: %+v", pgLossReasonDimension)
	}
	if strings.Contains(pgLossReasonDimension[0].Value, "Timeline uncertainty") {
		t.Fatalf("postgres loss_reason dimension exposed raw loss reason value: %+v", pgLossReasonDimension)
	}
	if _, err := pgStore.DB().ExecContext(ctx, `UPDATE calls SET raw_json = jsonb_set(raw_json, '{metaData,parties}', '[]'::jsonb, true) WHERE call_id = 'pg-ba-001'`); err != nil {
		t.Fatalf("blank raw party titles: %v", err)
	}
	if _, err := pgStore.DB().ExecContext(ctx, `UPDATE call_context_fields SET field_value_text = '' WHERE call_id = 'pg-ba-001' AND field_name IN ('LossReason', 'Title', 'JobTitle', 'Job_Title__c', 'JobTitle__c')`); err != nil {
		t.Fatalf("blank raw loss/title fields: %v", err)
	}
	pgPersonaDimension, err = pgStore.SummarizeBusinessAnalysisDimension(ctx, sqlite.BusinessAnalysisDimensionSummaryParams{Filter: filter, Dimension: "persona", Limit: 10})
	if err != nil {
		t.Fatalf("postgres persona SummarizeBusinessAnalysisDimension after raw blanking: %v", err)
	}
	if len(pgPersonaDimension) != 1 || pgPersonaDimension[0].Value != "participant_title_present" || pgPersonaDimension[0].CallCount != 1 {
		t.Fatalf("postgres persona dimension did not use materialized call_facts flag after raw blanking: %+v", pgPersonaDimension)
	}
	pgLossReasonDimension, err = pgStore.SummarizeBusinessAnalysisDimension(ctx, sqlite.BusinessAnalysisDimensionSummaryParams{Filter: filter, Dimension: "loss_reason", Limit: 10})
	if err != nil {
		t.Fatalf("postgres loss_reason SummarizeBusinessAnalysisDimension after raw blanking: %v", err)
	}
	if len(pgLossReasonDimension) != 1 || pgLossReasonDimension[0].Value != "loss_reason_present" || pgLossReasonDimension[0].CallCount != 1 {
		t.Fatalf("postgres loss_reason dimension did not use materialized call_facts flag after raw blanking: %+v", pgLossReasonDimension)
	}
}

func TestPostgresScorecardSettingsInventoryMatchesSQLiteRepresentativeSlice(t *testing.T) {
	databaseURL := os.Getenv("GONGCTL_TEST_POSTGRES_URL")
	if databaseURL == "" {
		t.Skip("GONGCTL_TEST_POSTGRES_URL is not set")
	}

	ctx := context.Background()
	pgStore, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer pgStore.Close()
	resetPostgresTestStore(t, ctx, pgStore)

	sqliteStore, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "gongctl.sqlite"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer sqliteStore.Close()

	raw := json.RawMessage(`{"scorecardId":"pg-scorecard-001","scorecardName":"Discovery quality","enabled":true,"reviewMethod":"MANUAL","workspaceId":"workspace-001","created":"2026-01-01T00:00:00Z","updated":"2026-01-02T00:00:00Z","questions":[{"questionId":"question-001","questionText":"Did the rep confirm pain?","questionType":"SCALE","isOverall":true,"minRange":1,"maxRange":5,"answerGuide":"Synthetic guidance","answerOptions":["No","Yes"]}]}`)
	if _, err := pgStore.UpsertGongSetting(ctx, "scorecards", raw); err != nil {
		t.Fatalf("postgres UpsertGongSetting returned error: %v", err)
	}
	if _, err := sqliteStore.UpsertGongSetting(ctx, "scorecards", raw); err != nil {
		t.Fatalf("sqlite UpsertGongSetting returned error: %v", err)
	}

	pgScorecards, err := pgStore.ListScorecards(ctx, sqlite.ScorecardListParams{ActiveOnly: true, Limit: 10})
	if err != nil {
		t.Fatalf("postgres ListScorecards returned error: %v", err)
	}
	sqliteScorecards, err := sqliteStore.ListScorecards(ctx, sqlite.ScorecardListParams{ActiveOnly: true, Limit: 10})
	if err != nil {
		t.Fatalf("sqlite ListScorecards returned error: %v", err)
	}
	if len(pgScorecards) != 1 || pgScorecards[0].CachedUpdatedAt == "" {
		t.Fatalf("postgres scorecards missing cached timestamp: %+v", pgScorecards)
	}
	if len(sqliteScorecards) != 1 || sqliteScorecards[0].CachedUpdatedAt == "" {
		t.Fatalf("sqlite scorecards missing cached timestamp: %+v", sqliteScorecards)
	}
	pgScorecards[0].CachedUpdatedAt = ""
	sqliteScorecards[0].CachedUpdatedAt = ""
	if !reflect.DeepEqual(pgScorecards, sqliteScorecards) {
		t.Fatalf("postgres scorecards=%+v want sqlite %+v", pgScorecards, sqliteScorecards)
	}

	pgDetail, err := pgStore.GetScorecardDetail(ctx, "pg-scorecard-001")
	if err != nil {
		t.Fatalf("postgres GetScorecardDetail returned error: %v", err)
	}
	sqliteDetail, err := sqliteStore.GetScorecardDetail(ctx, "pg-scorecard-001")
	if err != nil {
		t.Fatalf("sqlite GetScorecardDetail returned error: %v", err)
	}
	if pgDetail.CachedUpdatedAt == "" || sqliteDetail.CachedUpdatedAt == "" {
		t.Fatalf("scorecard detail missing cached timestamp: postgres=%+v sqlite=%+v", pgDetail, sqliteDetail)
	}
	pgDetail.CachedUpdatedAt = ""
	sqliteDetail.CachedUpdatedAt = ""
	if !reflect.DeepEqual(pgDetail, sqliteDetail) {
		t.Fatalf("postgres scorecard detail=%+v want sqlite %+v", pgDetail, sqliteDetail)
	}
	if len(pgDetail.Questions) != 1 || pgDetail.Questions[0].QuestionText != "Did the rep confirm pain?" {
		t.Fatalf("unexpected scorecard questions: %+v", pgDetail.Questions)
	}

	mixedIDRaw := json.RawMessage(`{"id":"generic-setting-id-002","scorecardId":"pg-scorecard-lookup-002","scorecardName":"Lookup scorecard","enabled":true,"questions":[{"questionId":"question-lookup","questionText":"Can list_scorecards ID be fetched?","minRange":1.5,"maxRange":"N/A"}]}`)
	if _, err := pgStore.UpsertGongSetting(ctx, "scorecards", mixedIDRaw); err != nil {
		t.Fatalf("postgres mixed-id UpsertGongSetting returned error: %v", err)
	}
	mixedSummaries, err := pgStore.ListScorecards(ctx, sqlite.ScorecardListParams{ActiveOnly: true, Limit: 10})
	if err != nil {
		t.Fatalf("postgres mixed-id ListScorecards returned error: %v", err)
	}
	var listedScorecardID string
	for _, summary := range mixedSummaries {
		if summary.Name == "Lookup scorecard" {
			listedScorecardID = summary.ScorecardID
			break
		}
	}
	if listedScorecardID != "pg-scorecard-lookup-002" {
		t.Fatalf("mixed-id scorecard listed id=%q, want scorecardId", listedScorecardID)
	}
	mixedDetail, err := pgStore.GetScorecardDetail(ctx, listedScorecardID)
	if err != nil {
		t.Fatalf("postgres mixed-id GetScorecardDetail(%q) returned error: %v", listedScorecardID, err)
	}
	if len(mixedDetail.Questions) != 1 || mixedDetail.Questions[0].MinRange != 1 || mixedDetail.Questions[0].MaxRange != 0 {
		t.Fatalf("mixed-id scorecard detail did not tolerate numeric variants: %+v", mixedDetail.Questions)
	}
}

func TestPostgresScorecardActivityAggregatesMatchSQLiteRepresentativeSlice(t *testing.T) {
	databaseURL := os.Getenv("GONGCTL_TEST_POSTGRES_URL")
	if databaseURL == "" {
		t.Skip("GONGCTL_TEST_POSTGRES_URL is not set")
	}

	ctx := context.Background()
	pgStore, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer pgStore.Close()
	resetPostgresTestStore(t, ctx, pgStore)

	sqliteStore, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "gongctl.sqlite"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer sqliteStore.Close()

	callPresent := json.RawMessage(`{"id":"scorecard-call-present","title":"Scorecard present transcript","started":"2026-03-01T15:00:00Z","duration":900}`)
	callMissing := json.RawMessage(`{"id":"scorecard-call-missing","title":"Scorecard missing transcript","started":"2026-03-02T15:00:00Z","duration":1200}`)
	transcript := json.RawMessage(`{"callId":"scorecard-call-present","transcript":[{"speakerId":"speaker-1","sentences":[{"start":0,"end":1000,"text":"Synthetic transcript for scorecard activity."}]}]}`)
	activities := []json.RawMessage{
		json.RawMessage(`{"answeredScorecardId":"answered-scorecard-present","scorecardId":"scorecard-activity-001","scorecardName":"Discovery QA","callId":"scorecard-call-present","callStartTime":"2026-03-01T15:00:00Z","reviewedUserId":"seller-001","reviewMethod":"MANUAL","reviewTime":"2026-03-03T15:00:00Z","answers":[{"isOverall":true,"score":4,"notApplicable":false},{"score":5,"notApplicable":false}]}`),
		json.RawMessage(`{"answeredScorecardId":"answered-scorecard-missing","scorecardId":"scorecard-activity-001","scorecardName":"Discovery QA","callId":"scorecard-call-missing","callStartTime":"2026-03-02T15:00:00Z","reviewedUserId":"seller-002","reviewMethod":"AUTOMATIC","reviewTime":"2026-03-04T15:00:00Z","answers":[{"isOverall":true,"score":3,"notApplicable":false},{"score":3,"notApplicable":false}]}`),
	}
	for _, store := range []interface {
		UpsertCall(context.Context, json.RawMessage) (*sqlite.CallRecord, error)
		UpsertTranscript(context.Context, json.RawMessage) (*sqlite.TranscriptRecord, error)
		UpsertScorecardActivity(context.Context, json.RawMessage) (*sqlite.ScorecardActivityRecord, error)
	}{pgStore, sqliteStore} {
		if _, err := store.UpsertCall(ctx, callPresent); err != nil {
			t.Fatalf("UpsertCall present returned error: %v", err)
		}
		if _, err := store.UpsertCall(ctx, callMissing); err != nil {
			t.Fatalf("UpsertCall missing returned error: %v", err)
		}
		if _, err := store.UpsertTranscript(ctx, transcript); err != nil {
			t.Fatalf("UpsertTranscript returned error: %v", err)
		}
		for _, raw := range activities {
			if _, err := store.UpsertScorecardActivity(ctx, raw); err != nil {
				t.Fatalf("UpsertScorecardActivity returned error: %v", err)
			}
		}
	}

	for _, groupBy := range []string{"scorecard", "review_method", "lifecycle", "transcript_status"} {
		pgRows, err := pgStore.SummarizeScorecardActivity(ctx, sqlite.ScorecardActivitySummaryParams{GroupBy: groupBy, Limit: 10})
		if err != nil {
			t.Fatalf("postgres SummarizeScorecardActivity(%s) returned error: %v", groupBy, err)
		}
		sqliteRows, err := sqliteStore.SummarizeScorecardActivity(ctx, sqlite.ScorecardActivitySummaryParams{GroupBy: groupBy, Limit: 10})
		if err != nil {
			t.Fatalf("sqlite SummarizeScorecardActivity(%s) returned error: %v", groupBy, err)
		}
		if !reflect.DeepEqual(pgRows, sqliteRows) {
			t.Fatalf("postgres scorecard activity %s rows=%+v want sqlite %+v", groupBy, pgRows, sqliteRows)
		}
	}

	pgOverview, err := pgStore.ScorecardActivityOverview(ctx, 10)
	if err != nil {
		t.Fatalf("postgres ScorecardActivityOverview returned error: %v", err)
	}
	sqliteOverview, err := sqliteStore.ScorecardActivityOverview(ctx, 10)
	if err != nil {
		t.Fatalf("sqlite ScorecardActivityOverview returned error: %v", err)
	}
	if !reflect.DeepEqual(pgOverview, sqliteOverview) {
		t.Fatalf("postgres overview=%+v want sqlite %+v", pgOverview, sqliteOverview)
	}
}

func TestPostgresCRMInventoryMatchesSQLiteRepresentativeSlice(t *testing.T) {
	databaseURL := os.Getenv("GONGCTL_TEST_POSTGRES_URL")
	if databaseURL == "" {
		t.Skip("GONGCTL_TEST_POSTGRES_URL is not set")
	}

	ctx := context.Background()
	pgStore, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer pgStore.Close()
	resetPostgresTestStore(t, ctx, pgStore)

	sqliteStore, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "gong.db"))
	if err != nil {
		t.Fatalf("sqlite.Open returned error: %v", err)
	}
	defer sqliteStore.Close()

	integrationRaw := json.RawMessage(`{"integrationId":"crm-int-parity-001","name":"Salesforce production","crmType":"Salesforce"}`)
	schemaRaw := json.RawMessage(`{"requestId":"request-001","objectTypeToSelectedFields":{"DEAL":[{"fieldName":"Amount","label":"Amount","fieldType":"currency"},{"fieldName":"StageName","label":"Stage","fieldType":"picklist"}]}}`)
	for _, store := range []interface {
		UpsertCRMIntegration(context.Context, json.RawMessage) (*sqlite.CRMIntegrationRecord, error)
		UpsertCRMSchema(context.Context, string, string, json.RawMessage) (int64, error)
	}{pgStore, sqliteStore} {
		if _, err := store.UpsertCRMIntegration(ctx, integrationRaw); err != nil {
			t.Fatalf("UpsertCRMIntegration returned error: %v", err)
		}
		count, err := store.UpsertCRMSchema(ctx, "crm-int-parity-001", "DEAL", schemaRaw)
		if err != nil {
			t.Fatalf("UpsertCRMSchema returned error: %v", err)
		}
		if count != 2 {
			t.Fatalf("schema field count=%d want 2", count)
		}
	}

	pgIntegrations, err := pgStore.ListCRMIntegrations(ctx)
	if err != nil {
		t.Fatalf("postgres ListCRMIntegrations returned error: %v", err)
	}
	sqliteIntegrations, err := sqliteStore.ListCRMIntegrations(ctx)
	if err != nil {
		t.Fatalf("sqlite ListCRMIntegrations returned error: %v", err)
	}
	stripCRMIntegrationTimestamps(pgIntegrations)
	stripCRMIntegrationTimestamps(sqliteIntegrations)
	if !reflect.DeepEqual(pgIntegrations, sqliteIntegrations) {
		t.Fatalf("postgres integrations=%+v want sqlite %+v", pgIntegrations, sqliteIntegrations)
	}

	pgObjects, err := pgStore.ListCRMSchemaObjects(ctx, "crm-int-parity-001")
	if err != nil {
		t.Fatalf("postgres ListCRMSchemaObjects returned error: %v", err)
	}
	sqliteObjects, err := sqliteStore.ListCRMSchemaObjects(ctx, "crm-int-parity-001")
	if err != nil {
		t.Fatalf("sqlite ListCRMSchemaObjects returned error: %v", err)
	}
	stripCRMSchemaObjectTimestamps(pgObjects)
	stripCRMSchemaObjectTimestamps(sqliteObjects)
	if !reflect.DeepEqual(pgObjects, sqliteObjects) {
		t.Fatalf("postgres schema objects=%+v want sqlite %+v", pgObjects, sqliteObjects)
	}

	params := sqlite.CRMSchemaFieldListParams{IntegrationID: "crm-int-parity-001", ObjectType: "DEAL", Limit: 10}
	pgFields, err := pgStore.ListCRMSchemaFields(ctx, params)
	if err != nil {
		t.Fatalf("postgres ListCRMSchemaFields returned error: %v", err)
	}
	sqliteFields, err := sqliteStore.ListCRMSchemaFields(ctx, params)
	if err != nil {
		t.Fatalf("sqlite ListCRMSchemaFields returned error: %v", err)
	}
	stripCRMSchemaFieldTimestamps(pgFields)
	stripCRMSchemaFieldTimestamps(sqliteFields)
	if !reflect.DeepEqual(pgFields, sqliteFields) {
		t.Fatalf("postgres schema fields=%+v want sqlite %+v", pgFields, sqliteFields)
	}

	summary, err := pgStore.SyncStatusSummary(ctx)
	if err != nil {
		t.Fatalf("postgres SyncStatusSummary returned error: %v", err)
	}
	if summary.TotalCRMIntegrations != 1 || summary.TotalCRMSchemaObjects != 1 || summary.TotalCRMSchemaFields != 2 {
		t.Fatalf("unexpected CRM inventory counts: %+v", summary)
	}
}

func TestPostgresProfileInventoryIncludesCachedCRMSchema(t *testing.T) {
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

	if _, err := store.UpsertCRMIntegration(ctx, postgresTestRaw(t, map[string]any{
		"integrationId": "crm-schema-only",
		"name":          "Schema Only",
		"crmType":       "CustomCRM",
	})); err != nil {
		t.Fatalf("UpsertCRMIntegration returned error: %v", err)
	}
	if _, err := store.UpsertCRMSchema(ctx, "crm-schema-only", "Deal", postgresTestRaw(t, map[string]any{
		"objectTypeToSelectedFields": map[string]any{
			"Deal": []map[string]any{
				{"fieldName": "DealStage", "label": "Stage", "fieldType": "picklist"},
				{"fieldName": "Amount", "label": "Amount", "fieldType": "currency"},
			},
		},
	})); err != nil {
		t.Fatalf("UpsertCRMSchema returned error: %v", err)
	}

	inventory, err := store.ProfileInventory(ctx)
	if err != nil {
		t.Fatalf("ProfileInventory returned error: %v", err)
	}
	if !postgresInventoryHasObject(inventory, "Deal") || !postgresInventoryHasField(inventory, "Deal", "DealStage") {
		t.Fatalf("schema-only inventory missing Deal/DealStage: %+v", inventory)
	}
}

func TestShouldBackfillReadModelAfterMigrations(t *testing.T) {
	if !shouldBackfillReadModelAfterMigrations(0) {
		t.Fatalf("new database should get an initial read-model backfill")
	}
	if !shouldBackfillReadModelAfterMigrations(postgresReadModelBackfillMigrationVersion - 1) {
		t.Fatalf("database before read-model backfill migration should rebuild")
	}
	if !shouldBackfillReadModelAfterMigrations(postgresReadModelPresenceMigrationVersion - 1) {
		t.Fatalf("database before read-model presence migration should rebuild")
	}
	if shouldBackfillReadModelAfterMigrations(len(migrations)) {
		t.Fatalf("current migration version should not force unrelated read-model rebuild")
	}
}

func stripCRMIntegrationTimestamps(rows []sqlite.CRMIntegrationRecord) {
	for idx := range rows {
		rows[idx].UpdatedAt = ""
	}
}

func stripCRMSchemaObjectTimestamps(rows []sqlite.CRMSchemaObjectRecord) {
	for idx := range rows {
		rows[idx].UpdatedAt = ""
	}
}

func stripCRMSchemaFieldTimestamps(rows []sqlite.CRMSchemaFieldRecord) {
	for idx := range rows {
		rows[idx].UpdatedAt = ""
	}
}

func postgresInventoryHasObject(inventory *profilepkg.Inventory, objectType string) bool {
	if inventory == nil {
		return false
	}
	for _, row := range inventory.Objects {
		if row.ObjectType == objectType {
			return true
		}
	}
	return false
}

func postgresInventoryHasField(inventory *profilepkg.Inventory, objectType string, fieldName string) bool {
	if inventory == nil {
		return false
	}
	for _, row := range inventory.Fields {
		if row.ObjectType == objectType && row.FieldName == fieldName {
			return true
		}
	}
	return false
}

func TestPostgresUpsertRefreshesNormalizedReadModel(t *testing.T) {
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

	if _, err := store.UpsertCall(ctx, json.RawMessage(`{"id":"pg-readmodel-001","title":"Renewal read model","started":"2026-02-12T15:00:00Z","duration":2400,"metaData":{"scope":"External","system":"Zoom","direction":"Conference","purpose":"Renewal review","calendarEventId":"cal-001","parties":[{"id":"buyer","title":"VP Operations"}]},"context":{"crmObjects":[{"type":"Opportunity","id":"opp-readmodel","name":"Renewal Opportunity","fields":{"StageName":"Discovery & Demo (SQO)","Type":"Renewal","Forecast_Category_VP__c":"Pipeline","Primary_Lead_Source__c":"Customer Success","LossReason":"Timeline uncertainty"}},{"type":"Account","id":"acct-readmodel","name":"Renewal Account","fields":{"Account_Type__c":"Customer - Active","Industry":"Healthcare"}}]}}`)); err != nil {
		t.Fatalf("UpsertCall returned error: %v", err)
	}
	assertPostgresCount(t, ctx, store, `SELECT COUNT(*) FROM call_context_objects WHERE call_id = 'pg-readmodel-001'`, 2)
	assertPostgresCount(t, ctx, store, `SELECT COUNT(*) FROM call_context_fields WHERE call_id = 'pg-readmodel-001'`, 7)

	var callDate, callMonth, durationBucket, scope, system, direction, transcriptStatus, lifecycleBucket, lifecycleConfidence, opportunityID, opportunityType, accountIndustry string
	var transcriptPresent, participantTitlePresent, lossReasonPresent bool
	var opportunityCount, accountCount int64
	if err := store.DB().QueryRowContext(ctx, `SELECT call_date, call_month, duration_bucket, scope, system, direction, transcript_present, transcript_status, lifecycle_bucket, lifecycle_confidence, opportunity_id, opportunity_type, account_industry, opportunity_count, account_count, participant_title_present, loss_reason_present FROM call_facts WHERE call_id = $1`, "pg-readmodel-001").Scan(&callDate, &callMonth, &durationBucket, &scope, &system, &direction, &transcriptPresent, &transcriptStatus, &lifecycleBucket, &lifecycleConfidence, &opportunityID, &opportunityType, &accountIndustry, &opportunityCount, &accountCount, &participantTitlePresent, &lossReasonPresent); err != nil {
		t.Fatalf("read call_facts: %v", err)
	}
	if callDate != "2026-02-12" || callMonth != "2026-02" || durationBucket != "30_45m" || scope != "External" || system != "Zoom" || direction != "Conference" {
		t.Fatalf("unexpected call fact dimensions: date=%s month=%s duration=%s scope=%s system=%s direction=%s", callDate, callMonth, durationBucket, scope, system, direction)
	}
	if transcriptPresent || transcriptStatus != "missing" || lifecycleBucket != "renewal" || lifecycleConfidence != "high" || opportunityID != "opp-readmodel" || opportunityType != "Renewal" || accountIndustry != "Healthcare" || opportunityCount != 1 || accountCount != 1 {
		t.Fatalf("unexpected CRM/lifecycle facts: present=%v status=%s bucket=%s confidence=%s opp=%s type=%s industry=%s opp_count=%d acct_count=%d", transcriptPresent, transcriptStatus, lifecycleBucket, lifecycleConfidence, opportunityID, opportunityType, accountIndustry, opportunityCount, accountCount)
	}
	if !participantTitlePresent || !lossReasonPresent {
		t.Fatalf("expected materialized participant/loss presence flags, got title=%v loss=%v", participantTitlePresent, lossReasonPresent)
	}
	coverage, err := store.CallFactsCoverage(ctx)
	if err != nil {
		t.Fatalf("CallFactsCoverage returned error: %v", err)
	}
	if coverage.PurposePopulatedCalls != 1 || coverage.CalendarCallCount != 1 {
		t.Fatalf("coverage purpose/calendar counts = %d/%d, want 1/1", coverage.PurposePopulatedCalls, coverage.CalendarCallCount)
	}

	if _, err := store.UpsertCall(ctx, json.RawMessage(`{"id":"pg-readmodel-001","title":"Renewal read model minimal","started":"2026-02-13T15:00:00Z","duration":600}`)); err != nil {
		t.Fatalf("minimal UpsertCall returned error: %v", err)
	}
	assertPostgresCount(t, ctx, store, `SELECT COUNT(*) FROM call_context_objects WHERE call_id = 'pg-readmodel-001'`, 2)
	if err := store.DB().QueryRowContext(ctx, `SELECT title, call_date, duration_bucket, opportunity_id, lifecycle_bucket FROM call_facts WHERE call_id = $1`, "pg-readmodel-001").Scan(&system, &callDate, &durationBucket, &opportunityID, &lifecycleBucket); err != nil {
		t.Fatalf("read minimal refreshed facts: %v", err)
	}
	if system != "Renewal read model minimal" || callDate != "2026-02-13" || durationBucket != "5_15m" || opportunityID != "opp-readmodel" || lifecycleBucket != "renewal" {
		t.Fatalf("minimal update facts not preserved/refreshed: title=%s date=%s duration=%s opp=%s bucket=%s", system, callDate, durationBucket, opportunityID, lifecycleBucket)
	}

	if _, err := store.UpsertTranscript(ctx, json.RawMessage(`{"callId":"pg-readmodel-001","transcript":[{"speakerId":"speaker-1","sentences":[{"start":0,"end":3000,"text":"Renewal read model transcript."}]}]}`)); err != nil {
		t.Fatalf("UpsertTranscript returned error: %v", err)
	}
	if err := store.DB().QueryRowContext(ctx, `SELECT transcript_present, transcript_status FROM call_facts WHERE call_id = $1`, "pg-readmodel-001").Scan(&transcriptPresent, &transcriptStatus); err != nil {
		t.Fatalf("read transcript refreshed facts: %v", err)
	}
	if !transcriptPresent || transcriptStatus != "present" {
		t.Fatalf("transcript facts not refreshed: present=%v status=%s", transcriptPresent, transcriptStatus)
	}

	if _, err := store.UpsertCall(ctx, json.RawMessage(`{"id":"pg-readmodel-001","title":"Explicit empty context","started":"2026-02-14T15:00:00Z","duration":600,"context":[]}`)); err != nil {
		t.Fatalf("empty context UpsertCall returned error: %v", err)
	}
	assertPostgresCount(t, ctx, store, `SELECT COUNT(*) FROM call_context_objects WHERE call_id = 'pg-readmodel-001'`, 0)
	assertPostgresCount(t, ctx, store, `SELECT COUNT(*) FROM call_context_fields WHERE call_id = 'pg-readmodel-001'`, 0)
	if err := store.DB().QueryRowContext(ctx, `SELECT transcript_present, transcript_status, opportunity_id, lifecycle_bucket, participant_title_present, loss_reason_present FROM call_facts WHERE call_id = $1`, "pg-readmodel-001").Scan(&transcriptPresent, &transcriptStatus, &opportunityID, &lifecycleBucket, &participantTitlePresent, &lossReasonPresent); err != nil {
		t.Fatalf("read empty-context facts: %v", err)
	}
	if !transcriptPresent || transcriptStatus != "present" || opportunityID != "" || lifecycleBucket != "unknown" {
		t.Fatalf("empty context facts not cleared/preserved correctly: present=%v status=%s opp=%s bucket=%s", transcriptPresent, transcriptStatus, opportunityID, lifecycleBucket)
	}
	if participantTitlePresent || lossReasonPresent {
		t.Fatalf("empty context facts did not clear materialized presence flags: title=%v loss=%v", participantTitlePresent, lossReasonPresent)
	}
	summary, err := store.SyncStatusSummary(ctx)
	if err != nil {
		t.Fatalf("SyncStatusSummary returned error: %v", err)
	}
	if summary.TotalEmbeddedCRMContextCalls != 0 || summary.TotalEmbeddedCRMObjects != 0 || summary.TotalEmbeddedCRMFields != 0 {
		t.Fatalf("empty context summary counts not cleared: calls=%d objects=%d fields=%d", summary.TotalEmbeddedCRMContextCalls, summary.TotalEmbeddedCRMObjects, summary.TotalEmbeddedCRMFields)
	}
}

func TestPostgresBackfillReadModelFromExistingCoreRows(t *testing.T) {
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

	raw := `{"id":"pg-backfill-001","title":"Backfill renewal","started":"2026-02-12T15:00:00Z","duration":2400,"metaData":{"scope":"External","system":"Zoom","direction":"Conference"},"context":{"crmObjects":[{"type":"Opportunity","id":"opp-backfill","fields":{"Type":"Renewal","StageName":"Discovery & Demo (SQO)"}}]}}`
	if _, err := store.DB().ExecContext(ctx, `INSERT INTO calls(call_id, title, started_at, duration_seconds, context_present, raw_json, raw_sha256, first_seen_at, updated_at) VALUES('pg-backfill-001', 'Backfill renewal', '2026-02-12T15:00:00Z', 2400, true, $1::jsonb, 'sha', '2026-02-12T15:00:00Z', '2026-02-12T15:00:00Z')`, raw); err != nil {
		t.Fatalf("insert core call row: %v", err)
	}
	if _, err := store.DB().ExecContext(ctx, `INSERT INTO transcripts(call_id, raw_json, raw_sha256, segment_count, first_seen_at, updated_at) VALUES('pg-backfill-001', '{"callId":"pg-backfill-001","transcript":[]}'::jsonb, 'sha', 0, '2026-02-12T15:00:00Z', '2026-02-12T15:00:00Z')`); err != nil {
		t.Fatalf("insert core transcript row: %v", err)
	}
	assertPostgresCount(t, ctx, store, `SELECT COUNT(*) FROM call_facts WHERE call_id = 'pg-backfill-001'`, 0)

	tx, err := store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx returned error: %v", err)
	}
	if err := backfillReadModelTx(ctx, tx); err != nil {
		tx.Rollback()
		t.Fatalf("backfillReadModelTx returned error: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("backfill tx commit returned error: %v", err)
	}
	assertPostgresCount(t, ctx, store, `SELECT COUNT(*) FROM call_context_objects WHERE call_id = 'pg-backfill-001'`, 1)
	var bucket, status string
	if err := store.DB().QueryRowContext(ctx, `SELECT lifecycle_bucket, transcript_status FROM call_facts WHERE call_id = $1`, "pg-backfill-001").Scan(&bucket, &status); err != nil {
		t.Fatalf("read backfilled facts: %v", err)
	}
	if bucket != "renewal" || status != "present" {
		t.Fatalf("backfilled facts bucket/status=%s/%s want renewal/present", bucket, status)
	}
}

func TestPostgresCallFactsSelectsFieldsFromSameCRMObject(t *testing.T) {
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

	raw := json.RawMessage(`{"id":"pg-multi-opp-001","title":"Multiple opportunities","started":"2026-02-12T15:00:00Z","duration":1800,"context":{"crmObjects":[{"type":"Opportunity","id":"opp-a","fields":{"StageName":"Discovery & Demo (SQO)","Type":"Renewal"}},{"type":"Opportunity","id":"opp-b","fields":{"StageName":"Contract Review","Type":"New Business"}}]}}`)
	if _, err := store.UpsertCall(ctx, raw); err != nil {
		t.Fatalf("UpsertCall returned error: %v", err)
	}
	var opportunityID, opportunityStage, opportunityType string
	if err := store.DB().QueryRowContext(ctx, `SELECT opportunity_id, opportunity_stage, opportunity_type FROM call_facts WHERE call_id = $1`, "pg-multi-opp-001").Scan(&opportunityID, &opportunityStage, &opportunityType); err != nil {
		t.Fatalf("read call_facts returned error: %v", err)
	}
	if opportunityID != "opp-a" || opportunityStage != "Discovery & Demo (SQO)" || opportunityType != "Renewal" {
		t.Fatalf("selected opportunity fields mixed across objects: id=%s stage=%s type=%s", opportunityID, opportunityStage, opportunityType)
	}
}

func TestPostgresNormalizedRowsMatchSQLiteForRepresentativeContextShapes(t *testing.T) {
	databaseURL := os.Getenv("GONGCTL_TEST_POSTGRES_URL")
	if databaseURL == "" {
		t.Skip("GONGCTL_TEST_POSTGRES_URL is not set")
	}

	ctx := context.Background()
	pgStore, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer pgStore.Close()
	resetPostgresTestStore(t, ctx, pgStore)

	sqlitePath := filepath.Join(t.TempDir(), "gong.db")
	sqliteStore, err := sqlite.Open(ctx, sqlitePath)
	if err != nil {
		t.Fatalf("sqlite Open returned error: %v", err)
	}
	defer sqliteStore.Close()

	fixtures := []json.RawMessage{
		json.RawMessage(`{"id":"pg-parity-001","title":"Array context","started":"2026-02-01T12:00:00Z","duration":1200,"context":[{"objectType":"Opportunity","id":"opp-array","name":"Array Opp","fields":[{"name":"StageName","label":"Stage","type":"picklist","value":"Contract Review"}]}]}`),
		json.RawMessage(`{"id":"pg-parity-002","title":"Object fields","started":"2026-02-02T12:00:00Z","duration":1200,"context":{"crmObjects":[{"type":"Account","id":"acct-map","fields":{"Name":"Map Account","Industry":"Healthcare"}}]}}`),
		json.RawMessage(`{"id":"pg-parity-003","title":"Nested properties","started":"2026-02-03T12:00:00Z","duration":1200,"crmContext":{"objects":[{"entityType":"Opportunity","crmId":"opp-properties","properties":{"Name":"Properties Opp","Type":"Renewal","Forecast_Category_VP__c":"Pipeline"}}]}}`),
		json.RawMessage(`{"id":"pg-parity-004","title":"Fallbacks","started":"2026-02-04T12:00:00Z","duration":1200,"extendedContext":{"wrapper":{"items":[{"type":"Account","id":"acct-fallback","fields":[{"label":"Name","value":"Fallback Account"},{"apiName":"Account_Type__c","displayName":"Type","value":"Customer - Active"},{"value":"unnamed"}]}]}}}`),
	}
	for _, raw := range fixtures {
		if _, err := pgStore.UpsertCall(ctx, raw); err != nil {
			t.Fatalf("postgres UpsertCall returned error: %v", err)
		}
		if _, err := sqliteStore.UpsertCall(ctx, raw); err != nil {
			t.Fatalf("sqlite UpsertCall returned error: %v", err)
		}
	}

	if got, want := postgresNormalizedRows(t, ctx, pgStore), sqliteNormalizedRows(t, ctx, sqliteStore); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("postgres normalized rows differ\npostgres:\n%s\nsqlite:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestPostgresSearchCRMFieldValuesMatchesSQLite(t *testing.T) {
	databaseURL := os.Getenv("GONGCTL_TEST_POSTGRES_URL")
	if databaseURL == "" {
		t.Skip("GONGCTL_TEST_POSTGRES_URL is not set")
	}

	ctx := context.Background()
	pgStore, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer pgStore.Close()
	resetPostgresTestStore(t, ctx, pgStore)

	sqliteStore, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "gong.db"))
	if err != nil {
		t.Fatalf("sqlite.Open returned error: %v", err)
	}
	defer sqliteStore.Close()

	longValue := strings.Repeat("procurement approval ", 20)
	fixtures := []json.RawMessage{
		json.RawMessage(`{"id":"pg-crm-value-001","title":"Latest procurement review","started":"2026-03-02T12:00:00Z","duration":1200,"context":{"crmObjects":[{"type":"Opportunity","id":"opp-value-1","name":"Procurement Opp","fields":{"ISSUE__c":"Need procurement approval","StageName":"Contract Review"}}]}}`),
		json.RawMessage(fmt.Sprintf(`{"id":"pg-crm-value-002","title":"Long procurement review","started":"2026-03-03T12:00:00Z","duration":1200,"context":{"crmObjects":[{"type":"Opportunity","id":"opp-value-2","name":"Long Procurement Opp","fields":{"ISSUE__c":%q,"StageName":"Proposal"}}]}}`, longValue)),
		json.RawMessage(`{"id":"pg-crm-value-003","title":"Pricing review","started":"2026-03-01T12:00:00Z","duration":1200,"context":{"crmObjects":[{"type":"Opportunity","id":"opp-value-3","name":"Pricing Opp","fields":{"ISSUE__c":"Pricing approval","StageName":"Proposal"}}]}}`),
	}
	for _, raw := range fixtures {
		if _, err := pgStore.UpsertCall(ctx, raw); err != nil {
			t.Fatalf("postgres UpsertCall returned error: %v", err)
		}
		if _, err := sqliteStore.UpsertCall(ctx, raw); err != nil {
			t.Fatalf("sqlite UpsertCall returned error: %v", err)
		}
	}

	params := sqlite.CRMFieldValueSearchParams{
		ObjectType:          "Opportunity",
		FieldName:           "ISSUE__c",
		ValueQuery:          "procurement",
		Limit:               10,
		IncludeValueSnippet: true,
		IncludeCallIDs:      true,
	}
	got, err := pgStore.SearchCRMFieldValues(ctx, params)
	if err != nil {
		t.Fatalf("postgres SearchCRMFieldValues returned error: %v", err)
	}
	want, err := sqliteStore.SearchCRMFieldValues(ctx, params)
	if err != nil {
		t.Fatalf("sqlite SearchCRMFieldValues returned error: %v", err)
	}
	for idx := range want {
		want[idx].ObjectID = ""
		want[idx].ObjectName = ""
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("postgres CRM field value matches=%+v want sqlite %+v", got, want)
	}
	if len(got) != 2 || got[0].CallID != "pg-crm-value-002" || got[1].CallID != "pg-crm-value-001" {
		t.Fatalf("unexpected Postgres ordering/results: %+v", got)
	}
	if len(got[0].ValueSnippet) != 240 {
		t.Fatalf("value snippet length=%d want 240", len(got[0].ValueSnippet))
	}

	withoutSnippet, err := pgStore.SearchCRMFieldValues(ctx, sqlite.CRMFieldValueSearchParams{
		ObjectType:     "Opportunity",
		FieldName:      "ISSUE__c",
		ValueQuery:     "procurement",
		Limit:          1,
		IncludeCallIDs: true,
	})
	if err != nil {
		t.Fatalf("postgres SearchCRMFieldValues without snippets returned error: %v", err)
	}
	if len(withoutSnippet) != 1 || withoutSnippet[0].ValueSnippet != "" || withoutSnippet[0].CallID != "pg-crm-value-002" {
		t.Fatalf("unexpected no-snippet result: %+v", withoutSnippet)
	}

	noMatches, err := pgStore.SearchCRMFieldValues(ctx, sqlite.CRMFieldValueSearchParams{
		ObjectType:     "Opportunity",
		FieldName:      "ISSUE__c",
		ValueQuery:     "not-present",
		IncludeCallIDs: true,
	})
	if err != nil {
		t.Fatalf("postgres SearchCRMFieldValues no-match returned error: %v", err)
	}
	wantNoMatches, err := sqliteStore.SearchCRMFieldValues(ctx, sqlite.CRMFieldValueSearchParams{
		ObjectType:          "Opportunity",
		FieldName:           "ISSUE__c",
		ValueQuery:          "not-present",
		IncludeValueSnippet: true,
		IncludeCallIDs:      true,
	})
	if err != nil {
		t.Fatalf("sqlite SearchCRMFieldValues no-match returned error: %v", err)
	}
	if !reflect.DeepEqual(noMatches, wantNoMatches) {
		t.Fatalf("postgres no-match result=%+v want sqlite %+v", noMatches, wantNoMatches)
	}

	for name, params := range map[string]sqlite.CRMFieldValueSearchParams{
		"missing object": {FieldName: "ISSUE__c", ValueQuery: "procurement"},
		"missing field":  {ObjectType: "Opportunity", ValueQuery: "procurement"},
		"missing value":  {ObjectType: "Opportunity", FieldName: "ISSUE__c"},
	} {
		if _, err := pgStore.SearchCRMFieldValues(ctx, params); err == nil {
			t.Fatalf("%s: SearchCRMFieldValues returned nil error", name)
		}
	}
}

func TestPostgresListUnmappedCRMFieldsMatchesSQLite(t *testing.T) {
	databaseURL := os.Getenv("GONGCTL_TEST_POSTGRES_URL")
	if databaseURL == "" {
		t.Skip("GONGCTL_TEST_POSTGRES_URL is not set")
	}

	ctx := context.Background()
	pgStore, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer pgStore.Close()
	resetPostgresTestStore(t, ctx, pgStore)

	if _, err := pgStore.ListUnmappedCRMFields(ctx, sqlite.UnmappedCRMFieldParams{Limit: 10}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("postgres ListUnmappedCRMFields without profile error=%v, want sql.ErrNoRows", err)
	}
	var directWithoutProfile int
	if err := pgStore.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM gongmcp_unmapped_crm_field_inventory(10)`).Scan(&directWithoutProfile); err != nil {
		t.Fatalf("direct function without profile returned error: %v", err)
	}
	if directWithoutProfile != 0 {
		t.Fatalf("direct function without profile returned %d rows, want 0", directWithoutProfile)
	}

	sqliteStore, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "gong.db"))
	if err != nil {
		t.Fatalf("sqlite.Open returned error: %v", err)
	}
	defer sqliteStore.Close()

	fixtures := []json.RawMessage{
		json.RawMessage(`{"id":"pg-unmapped-001","title":"Unmapped CRM review","started":"2026-03-02T12:00:00Z","duration":1200,"context":{"crmObjects":[{"type":"Opportunity","id":"opp-unmapped-1","name":"Unmapped Opp","fields":{"StageName":"Proposal","Type":"New Business","Procurement_System__c":"SAP Ariba"}},{"type":"Account","id":"acct-unmapped-1","name":"Unmapped Account","fields":{"Account_Type__c":"Prospect","Industry":"Manufacturing"}}]}}`),
		json.RawMessage(`{"id":"pg-unmapped-002","title":"Second unmapped CRM review","started":"2026-03-03T12:00:00Z","duration":1200,"context":{"crmObjects":[{"type":"Opportunity","id":"opp-unmapped-2","name":"Second Unmapped Opp","fields":{"StageName":"Discovery","Type":"Renewal","Procurement_System__c":"Coupa"}}]}}`),
	}
	for _, raw := range fixtures {
		if _, err := pgStore.UpsertCall(ctx, raw); err != nil {
			t.Fatalf("postgres UpsertCall returned error: %v", err)
		}
		if _, err := sqliteStore.UpsertCall(ctx, raw); err != nil {
			t.Fatalf("sqlite UpsertCall returned error: %v", err)
		}
	}

	profileBody := []byte(`
version: 1
name: Unmapped CRM test profile
objects:
  deal:
    object_types: [Opportunity]
  account:
    object_types: [Account]
fields:
  deal_stage:
    object: deal
    names: [StageName]
  account_type:
    object: account
    names: [Account_Type__c]
lifecycle:
  open:
    order: 10
    rules:
      - field: deal_stage
        op: in
        values: [Proposal, Discovery]
  closed_won:
    order: 20
  closed_lost:
    order: 30
  post_sales:
    order: 40
  unknown:
    order: 999
`)
	inventory, err := pgStore.ProfileInventory(ctx)
	if err != nil {
		t.Fatalf("postgres ProfileInventory returned error: %v", err)
	}
	p, validation, err := profilepkg.ValidateBytes(profileBody, inventory)
	if err != nil {
		t.Fatalf("ValidateBytes returned error: %v", err)
	}
	if !validation.Valid {
		t.Fatalf("profile validation failed: %+v", validation.Findings)
	}
	canonical, err := profilepkg.CanonicalJSON(p)
	if err != nil {
		t.Fatalf("CanonicalJSON returned error: %v", err)
	}
	params := sqlite.ProfileImportParams{
		SourcePath:      "unmapped-crm-profile.yaml",
		SourceSHA256:    validation.SourceSHA256,
		CanonicalSHA256: validation.CanonicalSHA256,
		RawYAML:         profileBody,
		CanonicalJSON:   canonical,
		Profile:         p,
		Findings:        validation.Findings,
		ImportedBy:      "test",
	}
	if _, err := pgStore.ImportProfile(ctx, params); err != nil {
		t.Fatalf("postgres ImportProfile returned error: %v", err)
	}
	if _, err := sqliteStore.ImportProfile(ctx, params); err != nil {
		t.Fatalf("sqlite ImportProfile returned error: %v", err)
	}

	got, err := pgStore.ListUnmappedCRMFields(ctx, sqlite.UnmappedCRMFieldParams{Limit: 10})
	if err != nil {
		t.Fatalf("postgres ListUnmappedCRMFields returned error: %v", err)
	}
	want, err := sqliteStore.ListUnmappedCRMFields(ctx, sqlite.UnmappedCRMFieldParams{Limit: 10})
	if err != nil {
		t.Fatalf("sqlite ListUnmappedCRMFields returned error: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("postgres unmapped CRM fields=%+v want sqlite %+v", got, want)
	}
	directRows, err := pgStore.DB().QueryContext(ctx, `SELECT object_type, field_name FROM gongmcp_unmapped_crm_field_inventory(10) ORDER BY object_type, field_name`)
	if err != nil {
		t.Fatalf("direct function after profile returned error: %v", err)
	}
	defer directRows.Close()
	var directFields []string
	for directRows.Next() {
		var objectType, fieldName string
		if err := directRows.Scan(&objectType, &fieldName); err != nil {
			t.Fatalf("scan direct function row: %v", err)
		}
		directFields = append(directFields, objectType+"."+fieldName)
	}
	if err := directRows.Err(); err != nil {
		t.Fatalf("direct function rows error: %v", err)
	}
	for _, mapped := range []string{"Opportunity.StageName", "Account.Account_Type__c"} {
		if stringSliceContains(directFields, mapped) {
			t.Fatalf("direct function returned mapped field %q: %+v", mapped, directFields)
		}
	}
	for _, row := range got {
		if row.FieldName == "StageName" || row.FieldName == "Account_Type__c" {
			t.Fatalf("mapped field was not filtered: %+v", got)
		}
		if row.FieldName == "Procurement_System__c" && (row.DistinctValueCount != 2 || row.PopulatedCount != 2 || row.MaxValueLength == 0) {
			t.Fatalf("unexpected procurement field stats: %+v", row)
		}
	}
	limited, err := pgStore.ListUnmappedCRMFields(ctx, sqlite.UnmappedCRMFieldParams{Limit: 1})
	if err != nil {
		t.Fatalf("postgres limited ListUnmappedCRMFields returned error: %v", err)
	}
	if len(limited) != 1 {
		t.Fatalf("limited unmapped len=%d want 1: %+v", len(limited), limited)
	}
}

func TestPostgresAnalyzeLateStageSignalsMatchesSQLite(t *testing.T) {
	databaseURL := os.Getenv("GONGCTL_TEST_POSTGRES_URL")
	if databaseURL == "" {
		t.Skip("GONGCTL_TEST_POSTGRES_URL is not set")
	}

	ctx := context.Background()
	pgStore, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer pgStore.Close()
	resetPostgresTestStore(t, ctx, pgStore)

	sqliteStore, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "gong.db"))
	if err != nil {
		t.Fatalf("sqlite.Open returned error: %v", err)
	}
	defer sqliteStore.Close()

	fixtures := []json.RawMessage{
		json.RawMessage(`{"id":"pg-late-signal-001","title":"Late stage contract call","started":"2026-04-24T12:00:00Z","context":{"crmObjects":[{"type":"Opportunity","id":"opp-late-signal-001","name":"Late signal opportunity","fields":{"StageName":" contract signing ","ISSUE__c":"Need procurement approval","Amount":"50000","Probability":"90","COMMON__c":"present"}}]}}`),
		json.RawMessage(`{"id":"pg-late-signal-002","title":"Early discovery call","started":"2026-04-23T12:00:00Z","context":{"crmObjects":[{"type":"Opportunity","id":"opp-late-signal-002","name":"Early signal opportunity","fields":{"StageName":"MQL","Amount":"0","COMMON__c":"present"}}]}}`),
		json.RawMessage(`{"id":"pg-late-signal-003","title":"Multi opportunity call","started":"2026-04-25T12:00:00Z","context":{"crmObjects":[{"type":"Opportunity","id":"opp-late-signal-early","fields":{"StageName":"MQL","MULTI__c":"yes","COMMON__c":"present"}},{"type":"Opportunity","id":"opp-late-signal-late","fields":{"StageName":" Contract Signing ","MULTI__c":"yes","COMMON__c":"present"}}]}}`),
	}
	for _, raw := range fixtures {
		if _, err := pgStore.UpsertCall(ctx, raw); err != nil {
			t.Fatalf("postgres UpsertCall returned error: %v", err)
		}
		if _, err := sqliteStore.UpsertCall(ctx, raw); err != nil {
			t.Fatalf("sqlite UpsertCall returned error: %v", err)
		}
	}

	params := sqlite.LateStageSignalParams{
		ObjectType:      "Opportunity",
		LateStageValues: []string{"Contract Signing"},
		Limit:           10,
	}
	got, err := pgStore.AnalyzeLateStageSignals(ctx, params)
	if err != nil {
		t.Fatalf("postgres AnalyzeLateStageSignals returned error: %v", err)
	}
	want, err := sqliteStore.AnalyzeLateStageSignals(ctx, params)
	if err != nil {
		t.Fatalf("sqlite AnalyzeLateStageSignals returned error: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("postgres late-stage report=%+v want sqlite %+v", got, want)
	}
	for _, signal := range got.Signals {
		if signal.FieldName == "StageName" || signal.FieldName == "Probability" {
			t.Fatalf("stage proxy leaked into default signals: %+v", got.Signals)
		}
		if signal.FieldName == "MULTI__c" && (signal.LatePopulatedCalls != 1 || signal.NonLatePopulatedCalls != 0) {
			t.Fatalf("multi-opportunity field was counted in both buckets: %+v", signal)
		}
	}
	if got.LateCalls != 2 || got.NonLateCalls != 1 || got.StageCounts["Contract Signing"] != 2 || got.StageCounts["MQL"] != 2 {
		t.Fatalf("unexpected late-stage counts: %+v", got)
	}
	limited, err := pgStore.AnalyzeLateStageSignals(ctx, sqlite.LateStageSignalParams{
		ObjectType:      "Opportunity",
		LateStageValues: []string{"Contract Signing"},
		Limit:           1,
	})
	if err != nil {
		t.Fatalf("postgres limited AnalyzeLateStageSignals returned error: %v", err)
	}
	if len(limited.Signals) != 1 || limited.Signals[0].FieldName == "COMMON__c" {
		t.Fatalf("limit was applied before lift sort: %+v", limited.Signals)
	}

	withProxies, err := pgStore.AnalyzeLateStageSignals(ctx, sqlite.LateStageSignalParams{
		ObjectType:          "Opportunity",
		LateStageValues:     []string{"Contract Signing"},
		IncludeStageProxies: true,
		Limit:               10,
	})
	if err != nil {
		t.Fatalf("postgres AnalyzeLateStageSignals with proxies returned error: %v", err)
	}
	signalNames := lateStageSignalNames(withProxies.Signals)
	for _, name := range []string{"StageName", "Probability"} {
		if _, ok := signalNames[name]; !ok {
			t.Fatalf("include_stage_proxies=true missing %s in %+v", name, withProxies.Signals)
		}
	}

	if _, err := pgStore.DB().ExecContext(ctx, `SELECT field_value_text FROM gongmcp_late_stage_signal_inventory('Opportunity', 'StageName', '["Contract Signing"]', 10, false)`); err == nil {
		t.Fatal("late-stage signal inventory exposed field_value_text column")
	}
	if _, err := pgStore.AnalyzeLateStageSignals(ctx, sqlite.LateStageSignalParams{
		ObjectType:      "Opportunity",
		StageField:      "Type",
		LateStageValues: []string{"New Business"},
	}); err == nil || !strings.Contains(err.Error(), "Opportunity.StageName only") {
		t.Fatalf("custom stage_field error=%v, want Opportunity.StageName-only rejection", err)
	}
	var directTypeStageCount int64
	if err := pgStore.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM gongmcp_late_stage_stage_counts('Opportunity', 'Type', '["New Business"]')`).Scan(&directTypeStageCount); err != nil {
		t.Fatalf("direct Type stage count query returned error: %v", err)
	}
	if directTypeStageCount != 0 {
		t.Fatalf("direct Type stage count returned %d rows, want 0", directTypeStageCount)
	}
}

func TestPostgresListOpportunitiesMissingTranscriptsMatchesSQLiteRedacted(t *testing.T) {
	databaseURL := os.Getenv("GONGCTL_TEST_POSTGRES_URL")
	if databaseURL == "" {
		t.Skip("GONGCTL_TEST_POSTGRES_URL is not set")
	}

	ctx := context.Background()
	pgStore, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer pgStore.Close()
	resetPostgresTestStore(t, ctx, pgStore)

	sqliteStore, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "gong.db"))
	if err != nil {
		t.Fatalf("sqlite.Open returned error: %v", err)
	}
	defer sqliteStore.Close()

	calls := []json.RawMessage{
		json.RawMessage(`{"id":"pg-opp-missing-001","title":"Latest covered gap call","started":"2026-04-20T12:00:00Z","duration":1200,"context":{"crmObjects":[{"type":"Opportunity","id":"opp-missing-1","name":"Coverage Gap One","fields":{"StageName":"Contract Signing","Amount":"50000"}}]}}`),
		json.RawMessage(`{"id":"pg-opp-missing-002","title":"Earlier covered transcript call","started":"2026-04-19T12:00:00Z","duration":1200,"context":{"crmObjects":[{"type":"Opportunity","id":"opp-missing-1","name":"Coverage Gap One","fields":{"StageName":"Contract Signing","Amount":"50000"}}]}}`),
		json.RawMessage(`{"id":"pg-opp-missing-003","title":"Latest two gap call","started":"2026-04-21T12:00:00Z","duration":1200,"context":{"crmObjects":[{"type":"Opportunity","id":"opp-missing-2","name":"Coverage Gap Two","fields":{"StageName":"Contract Signing","Amount":"75000"}}]}}`),
		json.RawMessage(`{"id":"pg-opp-missing-004","title":"Earlier two gap call","started":"2026-04-18T12:00:00Z","duration":1200,"context":{"crmObjects":[{"type":"Opportunity","id":"opp-missing-2","name":"Coverage Gap Two","fields":{"StageName":"Contract Signing","Amount":"75000"}}]}}`),
		json.RawMessage(`{"id":"pg-opp-missing-005","title":"Proposal stage gap call","started":"2026-04-22T12:00:00Z","duration":1200,"context":{"crmObjects":[{"type":"Opportunity","id":"opp-missing-proposal","name":"Proposal Gap","fields":{"StageName":"Proposal","Amount":"10000"}}]}}`),
	}
	transcripts := []json.RawMessage{
		json.RawMessage(`{"callId":"pg-opp-missing-002","transcript":[{"speakerId":"speaker-1","sentences":[{"start":0,"end":1000,"text":"Synthetic transcript for covered opportunity call."}]}]}`),
	}
	for _, store := range []interface {
		UpsertCall(context.Context, json.RawMessage) (*sqlite.CallRecord, error)
		UpsertTranscript(context.Context, json.RawMessage) (*sqlite.TranscriptRecord, error)
	}{pgStore, sqliteStore} {
		for _, raw := range calls {
			if _, err := store.UpsertCall(ctx, raw); err != nil {
				t.Fatalf("UpsertCall returned error: %v", err)
			}
		}
		for _, raw := range transcripts {
			if _, err := store.UpsertTranscript(ctx, raw); err != nil {
				t.Fatalf("UpsertTranscript returned error: %v", err)
			}
		}
	}

	params := sqlite.OpportunityMissingTranscriptParams{StageValues: []string{"Contract Signing"}, Limit: 10}
	got, err := pgStore.ListOpportunitiesMissingTranscripts(ctx, params)
	if err != nil {
		t.Fatalf("postgres ListOpportunitiesMissingTranscripts returned error: %v", err)
	}
	want, err := sqliteStore.ListOpportunitiesMissingTranscripts(ctx, params)
	if err != nil {
		t.Fatalf("sqlite ListOpportunitiesMissingTranscripts returned error: %v", err)
	}
	redactOpportunityMissingTranscriptSummaries(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("postgres opportunity gaps=%+v want sqlite %+v", got, want)
	}
	if len(got) != 2 || got[0].MissingTranscriptCount != 2 || got[1].MissingTranscriptCount != 1 {
		t.Fatalf("unexpected ordering/counts: %+v", got)
	}
	if got[0].OpportunityID != "" || got[0].OpportunityName != "" || got[0].LatestCallID != "" {
		t.Fatalf("postgres exposed opportunity identifiers: %+v", got[0])
	}

	limited, err := pgStore.ListOpportunitiesMissingTranscripts(ctx, sqlite.OpportunityMissingTranscriptParams{StageValues: []string{"Contract Signing"}, Limit: 1})
	if err != nil {
		t.Fatalf("postgres limited ListOpportunitiesMissingTranscripts returned error: %v", err)
	}
	if len(limited) != 1 || limited[0].MissingTranscriptCount != 2 {
		t.Fatalf("unexpected limited result: %+v", limited)
	}

	proposalOnly, err := pgStore.ListOpportunitiesMissingTranscripts(ctx, sqlite.OpportunityMissingTranscriptParams{StageValues: []string{"Proposal"}, Limit: 10})
	if err != nil {
		t.Fatalf("postgres Proposal ListOpportunitiesMissingTranscripts returned error: %v", err)
	}
	if len(proposalOnly) != 1 || proposalOnly[0].Stage != "Proposal" || proposalOnly[0].MissingTranscriptCount != 1 {
		t.Fatalf("unexpected proposal-only result: %+v", proposalOnly)
	}

	if _, err := pgStore.DB().ExecContext(ctx, `SELECT object_id FROM gongmcp_opportunities_missing_transcripts('["Contract Signing"]', 10)`); err == nil {
		t.Fatal("opportunities missing transcripts function exposed object_id column")
	}
	if _, err := pgStore.DB().ExecContext(ctx, `SELECT object_name FROM gongmcp_opportunities_missing_transcripts('["Contract Signing"]', 10)`); err == nil {
		t.Fatal("opportunities missing transcripts function exposed object_name column")
	}
	if _, err := pgStore.DB().ExecContext(ctx, `SELECT latest_call_id FROM gongmcp_opportunities_missing_transcripts('["Contract Signing"]', 10)`); err == nil {
		t.Fatal("opportunities missing transcripts function exposed latest_call_id column")
	}
	directRows, err := pgStore.DB().QueryContext(ctx, `SELECT stage, call_count, missing_transcript_count, transcript_count, latest_call_at FROM gongmcp_opportunities_missing_transcripts('["Contract Signing"]', 10)`)
	if err != nil {
		t.Fatalf("direct safe function returned error: %v", err)
	}
	defer directRows.Close()
	var directText strings.Builder
	for directRows.Next() {
		var stage, latestCallAt string
		var callCount, missingCount, transcriptCount int64
		if err := directRows.Scan(&stage, &callCount, &missingCount, &transcriptCount, &latestCallAt); err != nil {
			t.Fatalf("scan direct opportunity function row: %v", err)
		}
		directText.WriteString(fmt.Sprintf("%s|%d|%d|%d|%s\n", stage, callCount, missingCount, transcriptCount, latestCallAt))
	}
	if err := directRows.Err(); err != nil {
		t.Fatalf("direct safe function rows error: %v", err)
	}
	if strings.Contains(directText.String(), "opp-missing") || strings.Contains(directText.String(), "Coverage Gap") || strings.Contains(directText.String(), "pg-opp-missing") || strings.Contains(directText.String(), "50000") || strings.Contains(directText.String(), "75000") {
		t.Fatalf("direct safe function leaked identifiers or raw values: %s", directText.String())
	}

	wideStageValues := []string{"Contract Signing"}
	for len(wideStageValues) < maxOpportunityStageValueCount {
		wideStageValues = append(wideStageValues, strings.Repeat("&", maxOpportunityStageValueLength))
	}
	wide, err := pgStore.ListOpportunitiesMissingTranscripts(ctx, sqlite.OpportunityMissingTranscriptParams{StageValues: wideStageValues, Limit: 10})
	if err != nil {
		t.Fatalf("postgres wide stage_values ListOpportunitiesMissingTranscripts returned error: %v", err)
	}
	if !reflect.DeepEqual(wide, got) {
		t.Fatalf("wide accepted stage_values returned %+v want %+v", wide, got)
	}

	tooManyStages := make([]string, maxOpportunityStageValueCount+1)
	for i := range tooManyStages {
		tooManyStages[i] = fmt.Sprintf("Stage %d", i)
	}
	if _, err := pgStore.ListOpportunitiesMissingTranscripts(ctx, sqlite.OpportunityMissingTranscriptParams{StageValues: tooManyStages}); err == nil {
		t.Fatal("expected too many stage_values to fail")
	}
}

func TestPostgresSummarizeOpportunityCallsMatchesSQLiteRedacted(t *testing.T) {
	databaseURL := os.Getenv("GONGCTL_TEST_POSTGRES_URL")
	if databaseURL == "" {
		t.Skip("GONGCTL_TEST_POSTGRES_URL is not set")
	}

	ctx := context.Background()
	pgStore, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer pgStore.Close()
	resetPostgresTestStore(t, ctx, pgStore)

	sqliteStore, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "gong.db"))
	if err != nil {
		t.Fatalf("sqlite.Open returned error: %v", err)
	}
	defer sqliteStore.Close()

	calls := []json.RawMessage{
		json.RawMessage(`{"id":"pg-opp-summary-001","title":"Summary latest covered call","started":"2026-04-20T12:00:00Z","duration":1200,"context":{"crmObjects":[{"type":"Opportunity","id":"opp-summary-1","name":"Summary One","fields":{"StageName":"Contract Signing","Amount":"50000","CloseDate":"2026-06-01","OwnerId":"owner-001"}}]}}`),
		json.RawMessage(`{"id":"pg-opp-summary-002","title":"Summary earlier transcript call","started":"2026-04-19T12:00:00Z","duration":600,"context":{"crmObjects":[{"type":"Opportunity","id":"opp-summary-1","name":"Summary One","fields":{"StageName":"Contract Signing","Amount":"50000","CloseDate":"2026-06-01","OwnerId":"owner-001"}}]}}`),
		json.RawMessage(`{"id":"pg-opp-summary-003","title":"Summary second latest call","started":"2026-04-21T12:00:00Z","duration":900,"context":{"crmObjects":[{"type":"Opportunity","id":"opp-summary-2","name":"Summary Two","fields":{"StageName":"Contract Signing","Amount":"75000","CloseDate":"2026-06-15","OwnerId":"owner-002"}}]}}`),
		json.RawMessage(`{"id":"pg-opp-summary-004","title":"Summary second earlier call","started":"2026-04-18T12:00:00Z","duration":300,"context":{"crmObjects":[{"type":"Opportunity","id":"opp-summary-2","name":"Summary Two","fields":{"StageName":"Contract Signing","Amount":"75000","CloseDate":"2026-06-15","OwnerId":"owner-002"}}]}}`),
		json.RawMessage(`{"id":"pg-opp-summary-005","title":"Summary proposal call","started":"2026-04-22T12:00:00Z","duration":1500,"context":{"crmObjects":[{"type":"Opportunity","id":"opp-summary-proposal","name":"Proposal Summary","fields":{"StageName":"Proposal","Amount":"10000","CloseDate":"2026-05-20","OwnerId":"owner-003"}}]}}`),
	}
	transcripts := []json.RawMessage{
		json.RawMessage(`{"callId":"pg-opp-summary-002","transcript":[{"speakerId":"speaker-1","sentences":[{"start":0,"end":1000,"text":"Synthetic transcript for covered opportunity summary call."}]}]}`),
	}
	for _, store := range []interface {
		UpsertCall(context.Context, json.RawMessage) (*sqlite.CallRecord, error)
		UpsertTranscript(context.Context, json.RawMessage) (*sqlite.TranscriptRecord, error)
	}{pgStore, sqliteStore} {
		for _, raw := range calls {
			if _, err := store.UpsertCall(ctx, raw); err != nil {
				t.Fatalf("UpsertCall returned error: %v", err)
			}
		}
		for _, raw := range transcripts {
			if _, err := store.UpsertTranscript(ctx, raw); err != nil {
				t.Fatalf("UpsertTranscript returned error: %v", err)
			}
		}
	}

	params := sqlite.OpportunityCallSummaryParams{StageValues: []string{"Contract Signing"}, Limit: 10}
	got, err := pgStore.SummarizeOpportunityCalls(ctx, params)
	if err != nil {
		t.Fatalf("postgres SummarizeOpportunityCalls returned error: %v", err)
	}
	want, err := sqliteStore.SummarizeOpportunityCalls(ctx, params)
	if err != nil {
		t.Fatalf("sqlite SummarizeOpportunityCalls returned error: %v", err)
	}
	redactOpportunityCallSummaries(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("postgres opportunity summaries=%+v want sqlite %+v", got, want)
	}
	if len(got) != 2 || got[0].CallCount != 2 || got[0].LatestCallAt != "2026-04-21T12:00:00Z" || got[0].TotalDurationSeconds != 1200 {
		t.Fatalf("unexpected ordering/counts/duration: %+v", got)
	}
	if got[0].OpportunityID != "" || got[0].OpportunityName != "" || got[0].LatestCallID != "" || got[0].Amount != "" || got[0].CloseDate != "" || got[0].OwnerID != "" {
		t.Fatalf("postgres exposed opportunity sensitive fields: %+v", got[0])
	}

	limited, err := pgStore.SummarizeOpportunityCalls(ctx, sqlite.OpportunityCallSummaryParams{StageValues: []string{"Contract Signing"}, Limit: 1})
	if err != nil {
		t.Fatalf("postgres limited SummarizeOpportunityCalls returned error: %v", err)
	}
	if len(limited) != 1 || limited[0].LatestCallAt != "2026-04-21T12:00:00Z" {
		t.Fatalf("unexpected limited summary: %+v", limited)
	}

	proposalOnly, err := pgStore.SummarizeOpportunityCalls(ctx, sqlite.OpportunityCallSummaryParams{StageValues: []string{"Proposal"}, Limit: 10})
	if err != nil {
		t.Fatalf("postgres Proposal SummarizeOpportunityCalls returned error: %v", err)
	}
	if len(proposalOnly) != 1 || proposalOnly[0].Stage != "Proposal" || proposalOnly[0].CallCount != 1 || proposalOnly[0].TotalDurationSeconds != 1500 {
		t.Fatalf("unexpected proposal-only summary: %+v", proposalOnly)
	}

	for _, column := range []string{"object_id", "object_name", "opportunity_id", "opportunity_name", "owner_id", "amount", "close_date", "latest_call_id", "field_value_text", "raw_json"} {
		if _, err := pgStore.DB().ExecContext(ctx, `SELECT `+column+` FROM gongmcp_opportunity_call_summary('["Contract Signing"]', 10)`); err == nil {
			t.Fatalf("opportunity call summary function exposed %s column", column)
		}
	}
	directRows, err := pgStore.DB().QueryContext(ctx, `SELECT stage, call_count, transcript_count, missing_transcript_count, total_duration_seconds, latest_call_at FROM gongmcp_opportunity_call_summary('["Contract Signing"]', 10)`)
	if err != nil {
		t.Fatalf("direct safe opportunity summary function returned error: %v", err)
	}
	defer directRows.Close()
	var directText strings.Builder
	for directRows.Next() {
		var stage, latestCallAt string
		var callCount, transcriptCount, missingCount, totalDuration int64
		if err := directRows.Scan(&stage, &callCount, &transcriptCount, &missingCount, &totalDuration, &latestCallAt); err != nil {
			t.Fatalf("scan direct opportunity summary row: %v", err)
		}
		directText.WriteString(fmt.Sprintf("%s|%d|%d|%d|%d|%s\n", stage, callCount, transcriptCount, missingCount, totalDuration, latestCallAt))
	}
	if err := directRows.Err(); err != nil {
		t.Fatalf("direct opportunity summary rows error: %v", err)
	}
	if strings.Contains(directText.String(), "opp-summary") || strings.Contains(directText.String(), "Summary One") || strings.Contains(directText.String(), "pg-opp-summary") || strings.Contains(directText.String(), "owner-") || strings.Contains(directText.String(), "50000") || strings.Contains(directText.String(), "2026-06") {
		t.Fatalf("direct safe opportunity summary function leaked identifiers or values: %s", directText.String())
	}

	wideStageValues := []string{"Contract Signing"}
	for len(wideStageValues) < maxOpportunityStageValueCount {
		wideStageValues = append(wideStageValues, strings.Repeat("&", maxOpportunityStageValueLength))
	}
	wide, err := pgStore.SummarizeOpportunityCalls(ctx, sqlite.OpportunityCallSummaryParams{StageValues: wideStageValues, Limit: 10})
	if err != nil {
		t.Fatalf("postgres wide stage_values SummarizeOpportunityCalls returned error: %v", err)
	}
	if !reflect.DeepEqual(wide, got) {
		t.Fatalf("wide accepted stage_values returned %+v want %+v", wide, got)
	}

	tooManyStages := make([]string, maxOpportunityStageValueCount+1)
	for i := range tooManyStages {
		tooManyStages[i] = fmt.Sprintf("Stage %d", i)
	}
	if _, err := pgStore.SummarizeOpportunityCalls(ctx, sqlite.OpportunityCallSummaryParams{StageValues: tooManyStages}); err == nil {
		t.Fatal("expected too many stage_values to fail")
	}
	if _, err := pgStore.SummarizeOpportunityCalls(ctx, sqlite.OpportunityCallSummaryParams{StageValues: []string{strings.Repeat("x", maxOpportunityStageValueLength+1)}}); err == nil {
		t.Fatal("expected too-long stage_values entry to fail")
	}
}

func TestPostgresCRMFieldPopulationMatrixMatchesSQLite(t *testing.T) {
	databaseURL := os.Getenv("GONGCTL_TEST_POSTGRES_URL")
	if databaseURL == "" {
		t.Skip("GONGCTL_TEST_POSTGRES_URL is not set")
	}

	ctx := context.Background()
	pgStore, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer pgStore.Close()
	resetPostgresTestStore(t, ctx, pgStore)

	sqliteStore, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "gong.db"))
	if err != nil {
		t.Fatalf("sqlite.Open returned error: %v", err)
	}
	defer sqliteStore.Close()

	calls := []json.RawMessage{
		json.RawMessage(`{"id":"pg-crm-matrix-001","title":"Matrix latest contract call","started":"2026-04-20T12:00:00Z","duration":1200,"context":{"crmObjects":[{"type":"Opportunity","id":"opp-matrix-1","name":"Matrix One","fields":{"StageName":"Contract Signing","Amount":"50000","CloseDate":"2026-06-01","OwnerId":"owner-001","Forecast_Category_VP__c":"Commit"}}]}}`),
		json.RawMessage(`{"id":"pg-crm-matrix-002","title":"Matrix earlier contract call","started":"2026-04-19T12:00:00Z","duration":600,"context":{"crmObjects":[{"type":"Opportunity","id":"opp-matrix-2","name":"Matrix Two","fields":{"StageName":"Contract Signing","Amount":"","CloseDate":"2026-06-15","OwnerId":"owner-002","Forecast_Category_VP__c":"Best Case"}}]}}`),
		json.RawMessage(`{"id":"pg-crm-matrix-003","title":"Matrix proposal call","started":"2026-04-22T12:00:00Z","duration":1500,"context":{"crmObjects":[{"type":"Opportunity","id":"opp-matrix-proposal","name":"Matrix Proposal","fields":{"StageName":"Proposal","Amount":"10000","CloseDate":"2026-05-20","OwnerId":"owner-003","Forecast_Category_VP__c":"Pipeline"}}]}}`),
	}
	for _, store := range []interface {
		UpsertCall(context.Context, json.RawMessage) (*sqlite.CallRecord, error)
	}{pgStore, sqliteStore} {
		for _, raw := range calls {
			if _, err := store.UpsertCall(ctx, raw); err != nil {
				t.Fatalf("UpsertCall returned error: %v", err)
			}
		}
	}

	params := sqlite.CRMFieldPopulationMatrixParams{ObjectType: "Opportunity", GroupByField: "stagename", Limit: 20}
	got, err := pgStore.CRMFieldPopulationMatrix(ctx, params)
	if err != nil {
		t.Fatalf("postgres CRMFieldPopulationMatrix returned error: %v", err)
	}
	want, err := sqliteStore.CRMFieldPopulationMatrix(ctx, params)
	if err != nil {
		t.Fatalf("sqlite CRMFieldPopulationMatrix returned error: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("postgres CRM matrix=%+v want sqlite %+v", got, want)
	}
	if got.ObjectType != "Opportunity" || got.GroupByField != "StageName" {
		t.Fatalf("unexpected matrix header: %+v", got)
	}
	if len(got.Cells) == 0 {
		t.Fatalf("expected matrix cells")
	}
	var foundAmount bool
	for _, cell := range got.Cells {
		if cell.FieldName == "StageName" {
			t.Fatalf("group-by field leaked into matrix cells: %+v", cell)
		}
		if cell.GroupValue == "Contract Signing" && cell.FieldName == "Amount" {
			foundAmount = true
			if cell.ObjectCount != 2 || cell.CallCount != 2 || cell.PopulatedCount != 1 || cell.PopulationRate != 0.5 {
				t.Fatalf("unexpected amount population cell: %+v", cell)
			}
		}
	}
	if !foundAmount {
		t.Fatalf("amount cell missing from matrix: %+v", got.Cells)
	}

	limited, err := pgStore.CRMFieldPopulationMatrix(ctx, sqlite.CRMFieldPopulationMatrixParams{ObjectType: "Opportunity", GroupByField: "StageName", Limit: 1})
	if err != nil {
		t.Fatalf("postgres limited CRMFieldPopulationMatrix returned error: %v", err)
	}
	if len(limited.Cells) != 1 || limited.Cells[0].FieldName != got.Cells[0].FieldName {
		t.Fatalf("unexpected limited matrix: %+v", limited)
	}

	if _, err := pgStore.CRMFieldPopulationMatrix(ctx, sqlite.CRMFieldPopulationMatrixParams{ObjectType: "Opportunity", GroupByField: "OwnerId", Limit: 20}); err == nil {
		t.Fatal("expected unsafe group_by_field OwnerId to fail")
	}
	forecast, err := pgStore.CRMFieldPopulationMatrix(ctx, sqlite.CRMFieldPopulationMatrixParams{ObjectType: "Opportunity", GroupByField: "forecast_category_vp__c", Limit: 20})
	if err != nil {
		t.Fatalf("expected allowed Opportunity forecast group field to pass: %v", err)
	}
	if forecast.GroupByField != "Forecast_Category_VP__c" || len(forecast.Cells) == 0 {
		t.Fatalf("unexpected forecast matrix: %+v", forecast)
	}
	if _, err := pgStore.CRMFieldPopulationMatrix(ctx, sqlite.CRMFieldPopulationMatrixParams{ObjectType: "Account", GroupByField: "StageName", Limit: 20}); err == nil {
		t.Fatal("expected unsafe Account StageName pair to fail")
	}

	for _, column := range []string{"object_id", "object_name", "object_key", "call_id", "field_value_text", "raw_json", "raw_sha256", "canonical_sha256"} {
		if _, err := pgStore.DB().ExecContext(ctx, `SELECT `+column+` FROM gongmcp_crm_field_population_matrix('Opportunity', 'StageName', 10)`); err == nil {
			t.Fatalf("CRM field population matrix function exposed %s column", column)
		}
	}
	directRows, err := pgStore.DB().QueryContext(ctx, `SELECT group_value, field_name, field_label, object_count, call_count, populated_count FROM gongmcp_crm_field_population_matrix('Opportunity', 'StageName', 10)`)
	if err != nil {
		t.Fatalf("direct safe CRM matrix function returned error: %v", err)
	}
	defer directRows.Close()
	var directText strings.Builder
	for directRows.Next() {
		var groupValue, fieldName, fieldLabel string
		var objectCount, callCount, populatedCount int64
		if err := directRows.Scan(&groupValue, &fieldName, &fieldLabel, &objectCount, &callCount, &populatedCount); err != nil {
			t.Fatalf("scan direct CRM matrix row: %v", err)
		}
		directText.WriteString(fmt.Sprintf("%s|%s|%s|%d|%d|%d\n", groupValue, fieldName, fieldLabel, objectCount, callCount, populatedCount))
	}
	if err := directRows.Err(); err != nil {
		t.Fatalf("direct CRM matrix rows error: %v", err)
	}
	if strings.Contains(directText.String(), "opp-matrix") || strings.Contains(directText.String(), "Matrix One") || strings.Contains(directText.String(), "pg-crm-matrix") || strings.Contains(directText.String(), "owner-") || strings.Contains(directText.String(), "50000") || strings.Contains(directText.String(), "2026-06") || strings.Contains(directText.String(), "Commit") {
		t.Fatalf("direct safe CRM matrix function leaked identifiers or values: %s", directText.String())
	}

	if _, err := pgStore.DB().ExecContext(ctx, `SELECT COUNT(*) FROM gongmcp_crm_field_population_matrix('Opportunity', 'OwnerId', 10)`); err == nil {
		t.Fatal("direct unsafe group_by_field did not fail")
	}
}

func TestPostgresCompareLifecycleCRMFieldsMatchesSQLite(t *testing.T) {
	databaseURL := os.Getenv("GONGCTL_TEST_POSTGRES_URL")
	if databaseURL == "" {
		t.Skip("GONGCTL_TEST_POSTGRES_URL is not set")
	}

	ctx := context.Background()
	pgStore, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer pgStore.Close()
	resetPostgresTestStore(t, ctx, pgStore)

	sqliteStore, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "gong.db"))
	if err != nil {
		t.Fatalf("sqlite.Open returned error: %v", err)
	}
	defer sqliteStore.Close()

	calls := []json.RawMessage{
		json.RawMessage(`{"id":"pg-lifecycle-crm-001","title":"Renewal with process","started":"2026-04-20T12:00:00Z","duration":1200,"context":{"crmObjects":[{"type":"Opportunity","id":"opp-lifecycle-crm-1","name":"Renewal Opp One","fields":{"StageName":"Renewal Discussion","Type":"Renewal","Renewal_Process__c":"Formal","Procurement_System__c":"Coupa"}}]}}`),
		json.RawMessage(`{"id":"pg-lifecycle-crm-002","title":"Renewal without process","started":"2026-04-21T12:00:00Z","duration":900,"context":{"crmObjects":[{"type":"Opportunity","id":"opp-lifecycle-crm-2","name":"Renewal Opp Two","fields":{"StageName":"Renewal Discussion","Type":"Renewal","Renewal_Process__c":"","Procurement_System__c":"Ariba"}}]}}`),
		json.RawMessage(`{"id":"pg-lifecycle-crm-003","title":"Active pipeline call","started":"2026-04-22T12:00:00Z","duration":1500,"context":{"crmObjects":[{"type":"Opportunity","id":"opp-lifecycle-crm-3","name":"Pipeline Opp","fields":{"StageName":"Discovery","Type":"New Business","Renewal_Process__c":"","Procurement_System__c":"Coupa"}}]}}`),
	}
	for _, store := range []interface {
		UpsertCall(context.Context, json.RawMessage) (*sqlite.CallRecord, error)
	}{pgStore, sqliteStore} {
		for _, raw := range calls {
			if _, err := store.UpsertCall(ctx, raw); err != nil {
				t.Fatalf("UpsertCall returned error: %v", err)
			}
		}
	}

	params := sqlite.LifecycleCRMFieldComparisonParams{
		BucketA:    "renewal",
		BucketB:    "active_sales_pipeline",
		ObjectType: "Opportunity",
		Limit:      10,
	}
	got, err := pgStore.CompareLifecycleCRMFields(ctx, params)
	if err != nil {
		t.Fatalf("postgres CompareLifecycleCRMFields returned error: %v", err)
	}
	want, err := sqliteStore.CompareLifecycleCRMFields(ctx, params)
	if err != nil {
		t.Fatalf("sqlite CompareLifecycleCRMFields returned error: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("postgres lifecycle CRM comparison=%+v want sqlite %+v", got, want)
	}

	var foundRenewalProcess bool
	for _, row := range got.Fields {
		if row.FieldName == "Renewal_Process__c" {
			foundRenewalProcess = true
			if row.BucketACallCount != 2 || row.BucketBCallCount != 1 || row.BucketAPopulated != 1 || row.BucketBPopulated != 0 || row.BucketARate != 0.5 || row.BucketBRate != 0 || row.RateDelta != 0.5 {
				t.Fatalf("unexpected Renewal_Process__c row: %+v", row)
			}
		}
	}
	if !foundRenewalProcess {
		t.Fatalf("Renewal_Process__c missing from lifecycle CRM comparison: %+v", got.Fields)
	}

	limited, err := pgStore.CompareLifecycleCRMFields(ctx, sqlite.LifecycleCRMFieldComparisonParams{
		BucketA:    "renewal",
		BucketB:    "active_sales_pipeline",
		ObjectType: "Opportunity",
		Limit:      1,
	})
	if err != nil {
		t.Fatalf("postgres limited CompareLifecycleCRMFields returned error: %v", err)
	}
	if len(limited.Fields) != 1 || limited.Fields[0].FieldName != got.Fields[0].FieldName {
		t.Fatalf("unexpected limited lifecycle CRM comparison: %+v", limited)
	}

	for name, params := range map[string]sqlite.LifecycleCRMFieldComparisonParams{
		"missing bucket": {BucketA: "renewal", ObjectType: "Opportunity"},
		"same bucket":    {BucketA: "renewal", BucketB: "renewal", ObjectType: "Opportunity"},
		"bad bucket":     {BucketA: "renewal", BucketB: "not-real", ObjectType: "Opportunity"},
		"missing object": {BucketA: "renewal", BucketB: "active_sales_pipeline"},
		"unsafe object":  {BucketA: "renewal", BucketB: "active_sales_pipeline", ObjectType: "Account"},
	} {
		if _, err := pgStore.CompareLifecycleCRMFields(ctx, params); err == nil {
			t.Fatalf("%s: CompareLifecycleCRMFields returned nil error", name)
		}
	}

	for _, column := range []string{"call_id", "title", "object_id", "object_name", "object_key", "field_value_text", "raw_json", "raw_sha256", "text"} {
		if _, err := pgStore.DB().ExecContext(ctx, `SELECT `+column+` FROM gongmcp_compare_lifecycle_crm_fields('renewal', 'active_sales_pipeline', 'Opportunity', 10)`); err == nil {
			t.Fatalf("lifecycle CRM comparison function exposed %s column", column)
		}
	}
	directRows, err := pgStore.DB().QueryContext(ctx, `SELECT object_type, field_name, field_label, bucket_a_call_count, bucket_b_call_count, bucket_a_populated, bucket_b_populated, bucket_a_rate, bucket_b_rate, rate_delta FROM gongmcp_compare_lifecycle_crm_fields('renewal', 'active_sales_pipeline', 'Opportunity', 10)`)
	if err != nil {
		t.Fatalf("direct safe lifecycle CRM comparison function returned error: %v", err)
	}
	defer directRows.Close()
	var directText strings.Builder
	for directRows.Next() {
		var objectType, fieldName, fieldLabel string
		var bucketACallCount, bucketBCallCount, bucketAPopulated, bucketBPopulated int64
		var bucketARate, bucketBRate, rateDelta float64
		if err := directRows.Scan(&objectType, &fieldName, &fieldLabel, &bucketACallCount, &bucketBCallCount, &bucketAPopulated, &bucketBPopulated, &bucketARate, &bucketBRate, &rateDelta); err != nil {
			t.Fatalf("scan direct lifecycle CRM comparison row: %v", err)
		}
		directText.WriteString(fmt.Sprintf("%s|%s|%s|%d|%d|%d|%d|%.2f|%.2f|%.2f\n", objectType, fieldName, fieldLabel, bucketACallCount, bucketBCallCount, bucketAPopulated, bucketBPopulated, bucketARate, bucketBRate, rateDelta))
	}
	if err := directRows.Err(); err != nil {
		t.Fatalf("direct lifecycle CRM comparison rows error: %v", err)
	}
	if strings.Contains(directText.String(), "pg-lifecycle-crm") || strings.Contains(directText.String(), "opp-lifecycle-crm") || strings.Contains(directText.String(), "Renewal Opp") || strings.Contains(directText.String(), "Pipeline Opp") || strings.Contains(directText.String(), "Coupa") || strings.Contains(directText.String(), "Ariba") {
		t.Fatalf("direct safe lifecycle CRM comparison leaked identifiers or raw values: %s", directText.String())
	}

	if _, err := pgStore.DB().ExecContext(ctx, `SELECT COUNT(*) FROM gongmcp_compare_lifecycle_crm_fields('renewal', 'not-real', 'Opportunity', 10)`); err == nil {
		t.Fatal("direct bad lifecycle bucket did not fail")
	}
	if _, err := pgStore.DB().ExecContext(ctx, `SELECT COUNT(*) FROM gongmcp_compare_lifecycle_crm_fields('renewal', 'active_sales_pipeline', 'Account', 10)`); err == nil {
		t.Fatal("direct unsafe object_type did not fail")
	}
	for name, query := range map[string]string{
		"null bucket_a":    `SELECT COUNT(*) FROM gongmcp_compare_lifecycle_crm_fields(NULL, 'active_sales_pipeline', 'Opportunity', 10)`,
		"null bucket_b":    `SELECT COUNT(*) FROM gongmcp_compare_lifecycle_crm_fields('renewal', NULL, 'Opportunity', 10)`,
		"null object_type": `SELECT COUNT(*) FROM gongmcp_compare_lifecycle_crm_fields('renewal', 'active_sales_pipeline', NULL, 10)`,
	} {
		if _, err := pgStore.DB().ExecContext(ctx, query); err == nil {
			t.Fatalf("%s: direct NULL argument did not fail", name)
		}
	}

	if _, err := pgStore.DB().ExecContext(ctx, `INSERT INTO governance_policy_state(config_sha256, data_fingerprint, suppressed_call_count, updated_at) VALUES('lifecycle-crm-test-sha', 'fingerprint', 1, $1)`, nowUTC()); err != nil {
		t.Fatalf("insert governance policy state: %v", err)
	}
	if _, err := pgStore.DB().ExecContext(ctx, `INSERT INTO governance_suppressed_calls(config_sha256, call_id) VALUES('lifecycle-crm-test-sha', 'pg-lifecycle-crm-001')`); err != nil {
		t.Fatalf("insert governance suppressed call: %v", err)
	}
	governed, err := pgStore.CompareLifecycleCRMFields(ctx, params)
	if err != nil {
		t.Fatalf("governed CompareLifecycleCRMFields returned error: %v", err)
	}
	for _, row := range governed.Fields {
		if row.FieldName == "Renewal_Process__c" && row.BucketAPopulated != 0 {
			t.Fatalf("governed lifecycle CRM comparison included suppressed call: %+v", row)
		}
	}
}

func TestPostgresFindCallsMissingTranscriptsByFiltersMatchesSQLite(t *testing.T) {
	databaseURL := os.Getenv("GONGCTL_TEST_POSTGRES_URL")
	if databaseURL == "" {
		t.Skip("GONGCTL_TEST_POSTGRES_URL is not set")
	}

	ctx := context.Background()
	pgStore, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer pgStore.Close()
	resetPostgresTestStore(t, ctx, pgStore)

	sqliteStore, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "gong.db"))
	if err != nil {
		t.Fatalf("sqlite.Open returned error: %v", err)
	}
	defer sqliteStore.Close()

	calls := []json.RawMessage{
		json.RawMessage(`{"id":"pg-missing-filter-001","title":"Missing renewal external","started":"2026-04-20T12:00:00Z","duration":1200,"metaData":{"scope":"External","system":"Zoom","direction":"Conference"},"context":{"crmObjects":[{"type":"Opportunity","id":"opp-missing-filter-1","fields":{"StageName":"Renewal Discussion","Type":"Renewal"}}]}}`),
		json.RawMessage(`{"id":"pg-missing-filter-002","title":"Missing active internal","started":"2026-04-21T12:00:00Z","duration":900,"metaData":{"scope":"Internal","system":"Upload API","direction":"Inbound"},"context":{"crmObjects":[{"type":"Opportunity","id":"opp-missing-filter-2","fields":{"StageName":"Discovery","Type":"New Business"}}]}}`),
		json.RawMessage(`{"id":"pg-missing-filter-003","title":"Present renewal external","started":"2026-04-22T12:00:00Z","duration":1500,"metaData":{"scope":"External","system":"Zoom","direction":"Conference"},"context":{"crmObjects":[{"type":"Opportunity","id":"opp-missing-filter-3","fields":{"StageName":"Renewal Discussion","Type":"Renewal"}}]}}`),
	}
	for _, store := range []interface {
		UpsertCall(context.Context, json.RawMessage) (*sqlite.CallRecord, error)
		UpsertTranscript(context.Context, json.RawMessage) (*sqlite.TranscriptRecord, error)
	}{pgStore, sqliteStore} {
		for _, raw := range calls {
			if _, err := store.UpsertCall(ctx, raw); err != nil {
				t.Fatalf("UpsertCall returned error: %v", err)
			}
		}
		if _, err := store.UpsertTranscript(ctx, json.RawMessage(`{"callId":"pg-missing-filter-003","transcript":[{"speakerId":"speaker-1","sentences":[{"start":0,"end":1000,"text":"present transcript"}]}]}`)); err != nil {
			t.Fatalf("UpsertTranscript returned error: %v", err)
		}
	}

	for name, params := range map[string]sqlite.MissingTranscriptSearchParams{
		"unfiltered": {Limit: 10},
		"date":       {FromDate: "2026-04-20", ToDate: "2026-04-20", Limit: 10},
		"lifecycle":  {LifecycleBucket: "renewal", Limit: 10},
		"scope":      {Scope: "external", Limit: 10},
		"system":     {System: "Zoom", Limit: 10},
		"direction":  {Direction: "Conference", Limit: 10},
		"crm type":   {CRMObjectType: "Opportunity", Limit: 10},
		"crm object": {CRMObjectType: "Opportunity", CRMObjectID: "opp-missing-filter-1", Limit: 10},
		"combined":   {LifecycleBucket: "renewal", Scope: "External", System: "Zoom", Direction: "Conference", CRMObjectType: "Opportunity", Limit: 10},
		"limit":      {Limit: 1},
	} {
		got, err := pgStore.FindCallsMissingTranscriptsByFilters(ctx, params)
		if err != nil {
			t.Fatalf("%s: postgres FindCallsMissingTranscriptsByFilters returned error: %v", name, err)
		}
		want, err := sqliteStore.FindCallsMissingTranscriptsByFilters(ctx, params)
		if err != nil {
			t.Fatalf("%s: sqlite FindCallsMissingTranscriptsByFilters returned error: %v", name, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: postgres missing=%+v want sqlite %+v", name, got, want)
		}
	}

	for name, params := range map[string]sqlite.MissingTranscriptSearchParams{
		"bad date":   {FromDate: "2026-02-99"},
		"bad range":  {FromDate: "2026-04-22", ToDate: "2026-04-20"},
		"bad bucket": {LifecycleBucket: "not-real"},
		"bad scope":  {Scope: "field"},
		"id only":    {CRMObjectID: "opp-missing-filter-1"},
	} {
		if _, err := pgStore.FindCallsMissingTranscriptsByFilters(ctx, params); err == nil {
			t.Fatalf("%s: expected postgres filter error", name)
		}
	}

	directRows, err := pgStore.DB().QueryContext(ctx, `SELECT call_id, title, started_at FROM gongmcp_missing_transcripts('2026-04-20', '2026-04-20', 'renewal', 'External', 'Zoom', 'Conference', 'Opportunity', 'opp-missing-filter-1', 10)`)
	if err != nil {
		t.Fatalf("missing_transcripts function returned error: %v", err)
	}
	defer directRows.Close()
	var direct []sqlite.MissingTranscriptCall
	for directRows.Next() {
		var row sqlite.MissingTranscriptCall
		if err := directRows.Scan(&row.CallID, &row.Title, &row.StartedAt); err != nil {
			t.Fatalf("scan missing_transcripts function row: %v", err)
		}
		direct = append(direct, row)
	}
	if err := directRows.Err(); err != nil {
		t.Fatalf("missing_transcripts function rows: %v", err)
	}
	if !reflect.DeepEqual(direct, []sqlite.MissingTranscriptCall{{CallID: "pg-missing-filter-001", Title: "Missing renewal external", StartedAt: "2026-04-20T12:00:00Z"}}) {
		t.Fatalf("missing_transcripts function direct=%+v", direct)
	}
	var idOnlyCount int
	if err := pgStore.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM gongmcp_missing_transcripts('', '', '', '', '', '', '', 'opp-missing-filter-1', 10)`).Scan(&idOnlyCount); err != nil {
		t.Fatalf("missing_transcripts function id-only query returned error: %v", err)
	}
	if idOnlyCount != 0 {
		t.Fatalf("missing_transcripts function accepted crm_object_id without crm_object_type: %d", idOnlyCount)
	}
	for _, column := range []string{"object_id", "object_name", "object_key", "field_value_text", "raw_json", "raw_sha256", "text"} {
		if _, err := pgStore.DB().ExecContext(ctx, `SELECT `+column+` FROM gongmcp_missing_transcripts('2026-04-20', '2026-04-20', 'renewal', 'External', 'Zoom', 'Conference', 'Opportunity', 'opp-missing-filter-1', 10)`); err == nil {
			t.Fatalf("missing_transcripts function exposed %s column", column)
		}
	}

	if _, err := pgStore.DB().ExecContext(ctx, `INSERT INTO governance_policy_state(config_sha256, data_fingerprint, suppressed_call_count, updated_at) VALUES('stale-missing-transcripts-policy', 'stale-fingerprint', 1, $1)`, nowUTC()); err != nil {
		t.Fatalf("insert stale governance policy: %v", err)
	}
	if _, err := pgStore.DB().ExecContext(ctx, `INSERT INTO governance_suppressed_calls(config_sha256, call_id) VALUES('stale-missing-transcripts-policy', 'pg-missing-filter-001')`); err != nil {
		t.Fatalf("insert stale suppressed call: %v", err)
	}
	staleRows, err := pgStore.DB().QueryContext(ctx, `SELECT call_id, title, started_at FROM gongmcp_missing_transcripts('2026-04-20', '2026-04-20', 'renewal', 'External', 'Zoom', 'Conference', 'Opportunity', 'opp-missing-filter-1', 10)`)
	if err != nil {
		t.Fatalf("missing_transcripts function with stale governance policy returned error: %v", err)
	}
	defer staleRows.Close()
	direct = direct[:0]
	for staleRows.Next() {
		var row sqlite.MissingTranscriptCall
		if err := staleRows.Scan(&row.CallID, &row.Title, &row.StartedAt); err != nil {
			t.Fatalf("scan missing_transcripts stale policy row: %v", err)
		}
		direct = append(direct, row)
	}
	if err := staleRows.Err(); err != nil {
		t.Fatalf("missing_transcripts stale policy rows: %v", err)
	}
	if !reflect.DeepEqual(direct, []sqlite.MissingTranscriptCall{{CallID: "pg-missing-filter-001", Title: "Missing renewal external", StartedAt: "2026-04-20T12:00:00Z"}}) {
		t.Fatalf("missing_transcripts function applied stale governance policy: %+v", direct)
	}
}

func TestPostgresReadModelExtractionCapsRecordDiagnostics(t *testing.T) {
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

	var objects []string
	for i := 0; i < maxPostgresContextObjectsPerCall+1; i++ {
		var fields []string
		for j := 0; j < 11; j++ {
			fields = append(fields, fmt.Sprintf(`"Field_%03d":"value-%03d-%03d"`, j, i, j))
		}
		objects = append(objects, fmt.Sprintf(`{"type":"Opportunity","id":"opp-%03d","fields":{%s}}`, i, strings.Join(fields, ",")))
	}
	raw := json.RawMessage(`{"id":"pg-cap-001","title":"Capped context","started":"2026-02-12T15:00:00Z","duration":1200,"context":{"crmObjects":[` + strings.Join(objects, ",") + `]}}`)
	if _, err := store.UpsertCall(ctx, raw); err != nil {
		t.Fatalf("UpsertCall returned error: %v", err)
	}
	assertPostgresCount(t, ctx, store, `SELECT COUNT(*) FROM call_context_objects WHERE call_id = 'pg-cap-001'`, maxPostgresContextObjectsPerCall)
	assertPostgresCount(t, ctx, store, `SELECT COUNT(*) FROM call_context_fields WHERE call_id = 'pg-cap-001'`, maxPostgresContextFieldsPerCall)

	var objectCount, fieldCount, rawObjectCount, rawFieldCount int64
	var objectLimitExceeded, fieldLimitExceeded bool
	var lastError string
	if err := store.DB().QueryRowContext(ctx, `SELECT object_count, field_count, raw_object_count, raw_field_count, object_limit_exceeded, field_limit_exceeded, last_error FROM call_read_model_diagnostics WHERE call_id = $1`, "pg-cap-001").Scan(&objectCount, &fieldCount, &rawObjectCount, &rawFieldCount, &objectLimitExceeded, &fieldLimitExceeded, &lastError); err != nil {
		t.Fatalf("read diagnostics: %v", err)
	}
	if objectCount != maxPostgresContextObjectsPerCall || fieldCount != maxPostgresContextFieldsPerCall || rawObjectCount != maxPostgresContextObjectsPerCall+1 || rawFieldCount != int64((maxPostgresContextObjectsPerCall+1)*11) || !objectLimitExceeded || !fieldLimitExceeded || lastError != "" {
		t.Fatalf("unexpected diagnostics: objects=%d/%d fields=%d/%d limits=%v/%v err=%q", objectCount, rawObjectCount, fieldCount, rawFieldCount, objectLimitExceeded, fieldLimitExceeded, lastError)
	}
}

func TestPostgresReadModelStateDetectsDeletedFactRowsAsStaleAndRebuildRepairs(t *testing.T) {
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

	if _, err := store.UpsertCall(ctx, json.RawMessage(`{"id":"pg-stale-001","title":"Stale state","started":"2026-02-12T15:00:00Z","duration":1200}`)); err != nil {
		t.Fatalf("UpsertCall returned error: %v", err)
	}
	status, err := store.ReadModelStatus(ctx)
	if err != nil {
		t.Fatalf("ReadModelStatus returned error: %v", err)
	}
	if !status.Ready || status.ModelVersion != postgresReadModelVersion || status.CallCount != 1 || status.FactCount != 1 {
		t.Fatalf("initial read model status unexpected: %+v", status)
	}
	if _, err := store.DB().ExecContext(ctx, `DELETE FROM call_facts WHERE call_id = $1`, "pg-stale-001"); err != nil {
		t.Fatalf("delete call fact: %v", err)
	}
	status, err = store.ReadModelStatus(ctx)
	if err != nil {
		t.Fatalf("ReadModelStatus stale returned error: %v", err)
	}
	if status.Ready || !strings.Contains(status.StaleReason, "call_id set mismatch") {
		t.Fatalf("stale status not detected: %+v", status)
	}
	if err := store.validateReadModelReady(ctx); err == nil || !strings.Contains(err.Error(), "missing or stale") {
		t.Fatalf("cheap readiness did not reject deleted fact row: %v", err)
	}
	if _, err := store.RebuildReadModel(ctx); err != nil {
		t.Fatalf("RebuildReadModel returned error: %v", err)
	}
	status, err = store.ReadModelStatus(ctx)
	if err != nil {
		t.Fatalf("ReadModelStatus rebuilt returned error: %v", err)
	}
	if !status.Ready || status.CallCount != 1 || status.FactCount != 1 || status.StaleReason != "" {
		t.Fatalf("rebuilt status unexpected: %+v", status)
	}

	if _, err := store.DB().ExecContext(ctx, `DELETE FROM call_facts WHERE call_id = $1`, "pg-stale-001"); err != nil {
		t.Fatalf("delete rebuilt fact for mixed mismatch: %v", err)
	}
	if _, err := store.DB().ExecContext(ctx, `INSERT INTO call_facts(call_id, title, updated_at) VALUES('pg-orphan-001', 'orphan fact', $1)`, nowUTC()); err != nil {
		t.Fatalf("insert orphan fact: %v", err)
	}
	status, err = store.ReadModelStatus(ctx)
	if err != nil {
		t.Fatalf("ReadModelStatus orphan returned error: %v", err)
	}
	if status.Ready || status.FactCount != 1 || status.CallCount != 1 || status.MissingFactCallCount != 1 || status.OrphanFactCount != 1 || !strings.Contains(status.StaleReason, "call_id set mismatch") {
		t.Fatalf("mixed missing/orphan fact did not make state stale: %+v", status)
	}
	if err := store.validateReadModelReady(ctx); err == nil || !strings.Contains(err.Error(), "call_id set mismatch") {
		t.Fatalf("cheap readiness did not reject mixed missing/orphan rows: %v", err)
	}
	if _, err := store.RebuildReadModel(ctx); err != nil {
		t.Fatalf("RebuildReadModel orphan returned error: %v", err)
	}
	assertPostgresCount(t, ctx, store, `SELECT COUNT(*) FROM call_facts WHERE call_id = 'pg-orphan-001'`, 0)
	status, err = store.ReadModelStatus(ctx)
	if err != nil {
		t.Fatalf("ReadModelStatus orphan rebuilt returned error: %v", err)
	}
	if !status.Ready || status.CallCount != 1 || status.FactCount != 1 {
		t.Fatalf("orphan rebuild status unexpected: %+v", status)
	}
}

func TestPostgresReadModelCounterResyncHandlesUpdateAndTruncate(t *testing.T) {
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

	if _, err := store.UpsertCall(ctx, json.RawMessage(`{"id":"pg-resync-001","title":"Counter resync","started":"2026-02-12T15:00:00Z","duration":1200}`)); err != nil {
		t.Fatalf("UpsertCall returned error: %v", err)
	}
	if _, err := store.DB().ExecContext(ctx, `UPDATE call_facts SET call_id = 'pg-resync-orphan' WHERE call_id = 'pg-resync-001'`); err != nil {
		t.Fatalf("update fact call_id: %v", err)
	}
	status, err := store.ReadModelStatus(ctx)
	if err != nil {
		t.Fatalf("ReadModelStatus after update returned error: %v", err)
	}
	if status.Ready || status.MissingFactCallCount != 1 || status.OrphanFactCount != 1 || !strings.Contains(status.StaleReason, "call_id set mismatch") {
		t.Fatalf("update resync did not mark read model stale: %+v", status)
	}
	if _, err := store.RebuildReadModel(ctx); err != nil {
		t.Fatalf("RebuildReadModel after update drift returned error: %v", err)
	}
	status, err = store.ReadModelStatus(ctx)
	if err != nil {
		t.Fatalf("ReadModelStatus rebuilt returned error: %v", err)
	}
	if !status.Ready || status.MissingFactCallCount != 0 || status.OrphanFactCount != 0 {
		t.Fatalf("rebuild did not repair update drift: %+v", status)
	}
	if _, err := store.DB().ExecContext(ctx, `TRUNCATE call_facts`); err != nil {
		t.Fatalf("truncate facts: %v", err)
	}
	status, err = store.ReadModelStatus(ctx)
	if err != nil {
		t.Fatalf("ReadModelStatus after truncate returned error: %v", err)
	}
	if status.Ready || status.CallCount != 1 || status.FactCount != 0 || status.MissingFactCallCount != 1 || status.OrphanFactCount != 0 {
		t.Fatalf("truncate resync did not mark read model stale: %+v", status)
	}
}

func TestPostgresReadModelReadinessRejectsMissingState(t *testing.T) {
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

	if _, err := store.DB().ExecContext(ctx, `DELETE FROM postgres_read_model_state WHERE model_name = $1`, postgresReadModelName); err != nil {
		t.Fatalf("delete read model state: %v", err)
	}
	status, err := store.ReadModelStatus(ctx)
	if err != nil {
		t.Fatalf("ReadModelStatus returned error: %v", err)
	}
	if status.Ready || !strings.Contains(status.StaleReason, "state is missing") {
		t.Fatalf("missing state not reported stale: %+v", status)
	}
	if err := store.validateReadModelReady(ctx); err == nil || !strings.Contains(err.Error(), "state is missing") {
		t.Fatalf("cheap readiness did not reject missing state: %v", err)
	}
}

func TestPostgresReadModelReadinessRejectsRebuildInProgressState(t *testing.T) {
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

	if _, err := store.UpsertCall(ctx, json.RawMessage(`{"id":"pg-rebuild-progress-001","title":"Rebuild progress","started":"2026-02-12T15:00:00Z","duration":1200}`)); err != nil {
		t.Fatalf("UpsertCall returned error: %v", err)
	}
	if _, err := store.DB().ExecContext(ctx, `UPDATE postgres_read_model_state SET stale_reason = 'rebuild in progress' WHERE model_name = $1`, postgresReadModelName); err != nil {
		t.Fatalf("mark rebuild in progress: %v", err)
	}
	status, err := store.ReadModelStatus(ctx)
	if err != nil {
		t.Fatalf("ReadModelStatus returned error: %v", err)
	}
	if status.Ready || status.StaleReason != "rebuild in progress" {
		t.Fatalf("rebuild in progress state not preserved: %+v", status)
	}
	if err := store.validateReadModelReady(ctx); err == nil || !strings.Contains(err.Error(), "rebuild in progress") {
		t.Fatalf("cheap readiness did not reject rebuild in progress: %v", err)
	}
	if _, err := store.UpsertCall(ctx, json.RawMessage(`{"id":"pg-rebuild-progress-002","title":"Ordinary write","started":"2026-02-13T15:00:00Z","duration":1200}`)); err != nil {
		t.Fatalf("ordinary UpsertCall returned error: %v", err)
	}
	status, err = store.ReadModelStatus(ctx)
	if err != nil {
		t.Fatalf("ReadModelStatus after ordinary write returned error: %v", err)
	}
	if status.Ready || status.StaleReason != "rebuild in progress" {
		t.Fatalf("ordinary write cleared explicit stale reason: %+v", status)
	}
	if _, err := store.RebuildReadModel(ctx); err != nil {
		t.Fatalf("RebuildReadModel returned error: %v", err)
	}
	if err := store.validateReadModelReady(ctx); err != nil {
		t.Fatalf("cheap readiness rejected rebuilt state: %v", err)
	}
}

func TestPostgresWritableOpenDoesNotMarkOldReadModelVersionCurrent(t *testing.T) {
	databaseURL := os.Getenv("GONGCTL_TEST_POSTGRES_URL")
	if databaseURL == "" {
		t.Skip("GONGCTL_TEST_POSTGRES_URL is not set")
	}

	ctx := context.Background()
	store, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	resetPostgresTestStore(t, ctx, store)
	if _, err := store.UpsertCall(ctx, json.RawMessage(`{"id":"pg-old-model-001","title":"Old model","started":"2026-02-12T15:00:00Z","duration":1200}`)); err != nil {
		t.Fatalf("UpsertCall returned error: %v", err)
	}
	if _, err := store.DB().ExecContext(ctx, `UPDATE postgres_read_model_state SET model_version = $1, stale_reason = 'old version test' WHERE model_name = $2`, postgresReadModelVersion-1, postgresReadModelName); err != nil {
		t.Fatalf("downgrade read model state: %v", err)
	}
	store.Close()

	reopened, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("reopen writable store returned error: %v", err)
	}
	defer reopened.Close()
	status, err := reopened.ReadModelStatus(ctx)
	if err != nil {
		t.Fatalf("ReadModelStatus returned error: %v", err)
	}
	if status.Ready || status.ModelVersion != postgresReadModelVersion-1 || !strings.Contains(status.StaleReason, "older than supported") {
		t.Fatalf("writable open marked old model current: %+v", status)
	}
	if _, err := reopened.RebuildReadModel(ctx); err != nil {
		t.Fatalf("RebuildReadModel returned error: %v", err)
	}
	status, err = reopened.ReadModelStatus(ctx)
	if err != nil {
		t.Fatalf("ReadModelStatus rebuilt returned error: %v", err)
	}
	if !status.Ready || status.ModelVersion != postgresReadModelVersion {
		t.Fatalf("rebuild did not mark current: %+v", status)
	}
}

func TestPostgresBusinessPilotMethodsReadMaterializedCallFacts(t *testing.T) {
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

	if _, err := store.DB().ExecContext(ctx, `INSERT INTO call_facts(call_id, title, started_at, call_date, call_month, duration_seconds, duration_bucket, system, direction, scope, transcript_present, transcript_status, lifecycle_bucket, lifecycle_confidence, lifecycle_reason, lifecycle_evidence_fields, opportunity_count, account_count, updated_at)
VALUES('direct-fact-001', 'Direct fact', '2026-03-01T15:00:00Z', '2026-03-01', '2026-03', 1800, '30_45m', 'Zoom', 'Conference', 'External', false, 'missing', 'renewal', 'high', 'direct materialized fact', 'Opportunity.Type', 1, 1, '2026-03-01T15:00:00Z')`); err != nil {
		t.Fatalf("insert direct call_facts row: %v", err)
	}
	if _, err := store.DB().ExecContext(ctx, `INSERT INTO calls(call_id, title, started_at, duration_seconds, raw_json, raw_sha256, first_seen_at, updated_at)
VALUES('direct-fact-001', 'Direct fact core row', '2026-03-01T15:00:00Z', 1800, '{}'::jsonb, 'sha', '2026-03-01T15:00:00Z', '2026-03-01T15:00:00Z')`); err != nil {
		t.Fatalf("insert direct call core row: %v", err)
	}
	tx, err := store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin state tx: %v", err)
	}
	if err := updateReadModelStateTx(ctx, tx, nowUTC(), "", true); err != nil {
		tx.Rollback()
		t.Fatalf("update read model state: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit state tx: %v", err)
	}

	facts, err := store.SummarizeCallFacts(ctx, sqlite.CallFactsSummaryParams{GroupBy: "lifecycle", Limit: 10})
	if err != nil {
		t.Fatalf("SummarizeCallFacts returned error: %v", err)
	}
	if len(facts) != 1 || facts[0].GroupValue != "renewal" || facts[0].CallCount != 1 {
		t.Fatalf("SummarizeCallFacts did not read direct materialized fact: %+v", facts)
	}
	lifecycle, err := store.SummarizeCallsByLifecycle(ctx, sqlite.LifecycleSummaryParams{Bucket: "renewal"})
	if err != nil {
		t.Fatalf("SummarizeCallsByLifecycle returned error: %v", err)
	}
	if len(lifecycle) != 1 || lifecycle[0].Bucket != "renewal" || lifecycle[0].MissingTranscriptCount != 1 {
		t.Fatalf("SummarizeCallsByLifecycle did not read direct materialized fact: %+v", lifecycle)
	}
	backlog, err := store.PrioritizeTranscriptsByLifecycle(ctx, sqlite.LifecycleTranscriptPriorityParams{Bucket: "renewal", Limit: 10})
	if err != nil {
		t.Fatalf("PrioritizeTranscriptsByLifecycle returned error: %v", err)
	}
	if len(backlog) != 1 || backlog[0].CallID != "direct-fact-001" || backlog[0].Bucket != "renewal" {
		t.Fatalf("PrioritizeTranscriptsByLifecycle did not read direct materialized fact: %+v", backlog)
	}
}

func TestPostgresPrioritizeTranscriptsByLifecycle(t *testing.T) {
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
	for _, raw := range businessPilotCallPayloads() {
		if _, err := store.UpsertCall(ctx, raw); err != nil {
			t.Fatalf("UpsertCall returned error: %v", err)
		}
	}
	for _, raw := range businessPilotTranscriptPayloads() {
		if _, err := store.UpsertTranscript(ctx, raw); err != nil {
			t.Fatalf("UpsertTranscript returned error: %v", err)
		}
	}

	rows, err := store.PrioritizeTranscriptsByLifecycle(ctx, sqlite.LifecycleTranscriptPriorityParams{Limit: 10})
	if err != nil {
		t.Fatalf("PrioritizeTranscriptsByLifecycle returned error: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("priority rows=%d want 3: %+v", len(rows), rows)
	}
	if rows[0].CallID != "bp-renewal-missing" || rows[0].Bucket != "renewal" || rows[0].Confidence != "high" {
		t.Fatalf("unexpected top priority row: %+v", rows[0])
	}
	if rows[0].PriorityScore <= rows[1].PriorityScore {
		t.Fatalf("top priority score=%d should exceed second=%d rows=%+v", rows[0].PriorityScore, rows[1].PriorityScore, rows)
	}

	filtered, err := store.PrioritizeTranscriptsByLifecycle(ctx, sqlite.LifecycleTranscriptPriorityParams{
		Bucket:    "renewal",
		Scope:     "External",
		Direction: "Conference",
		FromDate:  "2026-02-01",
		ToDate:    "2026-02-28",
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("filtered PrioritizeTranscriptsByLifecycle returned error: %v", err)
	}
	if len(filtered) != 1 || filtered[0].CallID != "bp-renewal-missing" {
		t.Fatalf("unexpected filtered rows: %+v", filtered)
	}
	upsellRows, err := store.PrioritizeTranscriptsByLifecycle(ctx, sqlite.LifecycleTranscriptPriorityParams{
		Bucket: "upsell_expansion",
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("upsell PrioritizeTranscriptsByLifecycle returned error: %v", err)
	}
	if len(upsellRows) != 1 || upsellRows[0].CallID != "bp-upsell-missing" {
		t.Fatalf("unexpected upsell rows: %+v", upsellRows)
	}
	if got, want := upsellRows[0].EvidenceFields, []string{"Opportunity.Expansion_Bookings__c", "Opportunity.One_Year_Upsell__c"}; !stringSlicesEqual(got, want) {
		t.Fatalf("upsell evidence fields=%+v want %+v", got, want)
	}
	if _, err := store.PrioritizeTranscriptsByLifecycle(ctx, sqlite.LifecycleTranscriptPriorityParams{FromDate: "2026-02-99"}); err == nil {
		t.Fatal("bad date unexpectedly succeeded")
	}
	if _, err := store.PrioritizeTranscriptsByLifecycle(ctx, sqlite.LifecycleTranscriptPriorityParams{Bucket: "bogus"}); err == nil {
		t.Fatal("bad bucket unexpectedly succeeded")
	}
	if _, err := store.PrioritizeTranscriptsByLifecycle(ctx, sqlite.LifecycleTranscriptPriorityParams{FromDate: "2026-03-01", ToDate: "2026-02-01"}); err == nil {
		t.Fatal("inverted date range unexpectedly succeeded")
	}
}

func TestPostgresGovernanceAuditAndPolicy(t *testing.T) {
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

	if _, err := store.UpsertCall(ctx, json.RawMessage(`{"id":"pg-governance-blocked","title":"Blocked governance account","started":"2026-04-24T12:00:00Z","duration":1200,"context":{"crmObjects":[{"type":"Account","id":"acct-governance-blocked","name":"Postgres NoAI Corp","fields":{"Name":"Postgres NoAI Corp"}}]}}`)); err != nil {
		t.Fatalf("UpsertCall blocked returned error: %v", err)
	}
	if _, err := store.UpsertCall(ctx, json.RawMessage(`{"id":"pg-governance-allowed","title":"Allowed governance account","started":"2026-04-24T13:00:00Z","duration":1200,"context":{"crmObjects":[{"type":"Account","id":"acct-governance-allowed","name":"Postgres Allowed Corp","fields":{"Name":"Postgres Allowed Corp"}}]}}`)); err != nil {
		t.Fatalf("UpsertCall allowed returned error: %v", err)
	}
	if _, err := store.UpsertTranscript(ctx, json.RawMessage(`{"callId":"pg-governance-blocked","transcript":[{"speakerId":"speaker-1","sentences":[{"start":0,"end":3000,"text":"The team named Postgres NoAI Corp in transcript evidence."}]}]}`)); err != nil {
		t.Fatalf("UpsertTranscript blocked returned error: %v", err)
	}

	cfg, err := governance.ParseYAML([]byte(`
version: 1
lists:
  no_ai:
    customers:
      - name: "Postgres NoAI Corp"
`))
	if err != nil {
		t.Fatalf("ParseYAML returned error: %v", err)
	}
	audit, policy, err := store.BuildAndSaveGovernancePolicy(ctx, cfg.Fingerprint(), cfg)
	if err != nil {
		t.Fatalf("BuildAndSaveGovernancePolicy returned error: %v", err)
	}
	if audit.SuppressedCallCount != 1 || len(audit.SuppressedCallIDs) != 1 || audit.SuppressedCallIDs[0] != "pg-governance-blocked" {
		t.Fatalf("unexpected governance audit: %+v", audit)
	}
	if policy.ConfigSHA256 != cfg.Fingerprint() || policy.SuppressedCallCount != 1 || len(policy.SuppressedCallIDs) != 1 {
		t.Fatalf("unexpected saved policy: %+v", policy)
	}
	loaded, err := store.LoadGovernancePolicy(ctx, cfg.Fingerprint())
	if err != nil {
		t.Fatalf("LoadGovernancePolicy returned error: %v", err)
	}
	if !reflect.DeepEqual(loaded.SuppressedCallIDs, []string{"pg-governance-blocked"}) || loaded.DataFingerprint == "" {
		t.Fatalf("unexpected loaded policy: %+v", loaded)
	}
	if _, err := store.UpsertCall(ctx, json.RawMessage(`{"id":"pg-governance-blocked-late","title":"Late Postgres NoAI Corp","started":"2026-04-24T14:00:00Z","duration":1200}`)); err != nil {
		t.Fatalf("UpsertCall late blocked returned error: %v", err)
	}
	currentFingerprint, err := store.GovernanceDataFingerprint(ctx)
	if err != nil {
		t.Fatalf("GovernanceDataFingerprint returned error: %v", err)
	}
	if currentFingerprint == loaded.DataFingerprint {
		t.Fatalf("governance policy fingerprint did not detect candidate mutation")
	}
}

func resetPostgresTestStore(t *testing.T, ctx context.Context, store *Store) {
	t.Helper()
	_, err := store.DB().ExecContext(ctx, `TRUNCATE crm_schema_fields, crm_schema_objects, crm_integrations, scorecard_activity, gong_settings, governance_suppressed_calls, governance_policy_state, profile_call_fact_cache, profile_call_fact_cache_meta, profile_validation_warning, profile_methodology_concept, profile_lifecycle_rule, profile_field_concept, profile_object_alias, profile_meta, purged_call_ids, call_read_model_diagnostics, postgres_read_model_state, call_facts, call_context_fields, call_context_objects, transcript_segments, transcripts, calls, users, sync_state, sync_runs RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatalf("reset postgres test store: %v", err)
	}
	tx, err := store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("reset postgres begin tx: %v", err)
	}
	if err := updateReadModelStateTx(ctx, tx, nowUTC(), "", true); err != nil {
		tx.Rollback()
		t.Fatalf("reset postgres read model state: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("reset postgres commit tx: %v", err)
	}
}

func assertPostgresCount(t *testing.T, ctx context.Context, store *Store, query string, want int64) {
	t.Helper()
	var got int64
	if err := store.DB().QueryRowContext(ctx, query).Scan(&got); err != nil {
		t.Fatalf("count query failed: %v", err)
	}
	if got != want {
		t.Fatalf("count query got %d want %d: %s", got, want, query)
	}
}

func stringSliceContains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func lateStageSignalNames(signals []sqlite.LateStageSignal) map[string]struct{} {
	out := make(map[string]struct{}, len(signals))
	for _, signal := range signals {
		out[signal.FieldName] = struct{}{}
	}
	return out
}

func postgresTableCounts(rows []sqlite.CacheTableCount) map[string]int64 {
	out := make(map[string]int64, len(rows))
	for _, row := range rows {
		out[row.Table] = row.Rows
	}
	return out
}

type phase5BStore interface {
	UpsertCall(context.Context, json.RawMessage) (*sqlite.CallRecord, error)
	UpsertTranscript(context.Context, json.RawMessage) (*sqlite.TranscriptRecord, error)
}

func seedPhase5BBusinessAnalysisFixtures(t *testing.T, ctx context.Context, stores ...phase5BStore) {
	t.Helper()
	calls := []json.RawMessage{
		postgresTestRaw(t, map[string]any{
			"id":       "pg-ba-001",
			"title":    "Implementation evidence call",
			"started":  "2026-02-12T15:00:00Z",
			"duration": 1800,
			"metaData": map[string]any{
				"system":    "Zoom",
				"direction": "Conference",
				"scope":     "External",
				"parties": []any{
					map[string]any{"id": "buyer", "title": "VP Operations"},
				},
			},
			"context": []any{
				map[string]any{
					"objectType": "Account",
					"id":         "acct-ba",
					"fields": []any{
						map[string]any{"name": "Name", "value": "Example Manufacturing"},
						map[string]any{"name": "Industry", "value": "Manufacturing"},
						map[string]any{"name": "Website", "value": "https://example.invalid"},
					},
				},
				map[string]any{
					"objectType": "Opportunity",
					"id":         "opp-ba",
					"fields": []any{
						map[string]any{"name": "Name", "value": "Example Deal"},
						map[string]any{"name": "StageName", "value": "Discovery"},
						map[string]any{"name": "Type", "value": "New Business"},
						map[string]any{"name": "LossReason", "value": "Timeline uncertainty"},
						map[string]any{"name": "CloseDate", "value": "2026-03-31"},
						map[string]any{"name": "Probability", "value": "25"},
					},
				},
			},
		}),
		postgresTestRaw(t, map[string]any{
			"id":       "pg-ba-002",
			"title":    "Out of filter implementation call",
			"started":  "2026-04-15T15:00:00Z",
			"duration": 900,
			"metaData": map[string]any{
				"system":    "Zoom",
				"direction": "Conference",
				"scope":     "External",
			},
		}),
	}
	transcripts := []json.RawMessage{
		postgresTestRaw(t, map[string]any{
			"callTranscripts": []any{
				map[string]any{
					"callId": "pg-ba-001",
					"transcript": []any{
						map[string]any{
							"speakerId": "buyer",
							"sentences": []any{
								map[string]any{"start": 1000, "end": 2000, "text": "Implementation timeline is our biggest question."},
							},
						},
					},
				},
			},
		}),
		postgresTestRaw(t, map[string]any{
			"callTranscripts": []any{
				map[string]any{
					"callId": "pg-ba-002",
					"transcript": []any{
						map[string]any{
							"speakerId": "buyer",
							"sentences": []any{
								map[string]any{"start": 1000, "end": 2000, "text": "Implementation timeline is outside the quarter."},
							},
						},
					},
				},
			},
		}),
	}
	for _, store := range stores {
		for _, raw := range calls {
			if _, err := store.UpsertCall(ctx, raw); err != nil {
				t.Fatalf("upsert phase 5b call: %v", err)
			}
		}
		for _, raw := range transcripts {
			if _, err := store.UpsertTranscript(ctx, raw); err != nil {
				t.Fatalf("upsert phase 5b transcript: %v", err)
			}
		}
	}
}

func postgresTestRaw(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := normalizeJSONValue(value)
	if err != nil {
		t.Fatalf("normalize JSON value: %v", err)
	}
	return raw
}

func transcriptCallFactIDs(rows []sqlite.TranscriptCallFactsSearchResult) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.CallID)
	}
	sort.Strings(out)
	return out
}

func transcriptAttributionIDs(rows []sqlite.TranscriptAttributionSearchResult) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.CallID)
	}
	sort.Strings(out)
	return out
}

func businessAnalysisCallIDs(rows []sqlite.BusinessAnalysisCallRow) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.CallID)
	}
	sort.Strings(out)
	return out
}

func postgresNormalizedRows(t *testing.T, ctx context.Context, store *Store) []string {
	t.Helper()
	rows, err := store.DB().QueryContext(ctx, `
SELECT 'object|' || call_id || '|' || object_key || '|' || object_type || '|' || object_id || '|' || object_name
  FROM call_context_objects
UNION ALL
SELECT 'field|' || f.call_id || '|' || f.object_key || '|' || o.object_type || '|' || o.object_id || '|' || f.field_name || '|' || f.field_label || '|' || f.field_type || '|' || f.field_value_text
  FROM call_context_fields f
  JOIN call_context_objects o ON o.call_id = f.call_id AND o.object_key = f.object_key
 ORDER BY 1`)
	if err != nil {
		t.Fatalf("query postgres normalized rows: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var row string
		if err := rows.Scan(&row); err != nil {
			t.Fatalf("scan postgres normalized rows: %v", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate postgres normalized rows: %v", err)
	}
	return out
}

func sqliteNormalizedRows(t *testing.T, ctx context.Context, store *sqlite.Store) []string {
	t.Helper()
	rows, err := store.DB().QueryContext(ctx, `
SELECT 'object|' || call_id || '|' || object_key || '|' || object_type || '|' || object_id || '|' || object_name
  FROM call_context_objects
UNION ALL
SELECT 'field|' || f.call_id || '|' || f.object_key || '|' || o.object_type || '|' || o.object_id || '|' || f.field_name || '|' || f.field_label || '|' || f.field_type || '|' || f.field_value_text
  FROM call_context_fields f
  JOIN call_context_objects o ON o.call_id = f.call_id AND o.object_key = f.object_key
 ORDER BY 1`)
	if err != nil {
		t.Fatalf("query sqlite normalized rows: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var row string
		if err := rows.Scan(&row); err != nil {
			t.Fatalf("scan sqlite normalized rows: %v", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate sqlite normalized rows: %v", err)
	}
	return out
}

func rawCallIDs(t *testing.T, rows []json.RawMessage) []string {
	t.Helper()
	out := make([]string, 0, len(rows))
	for _, raw := range rows {
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("unmarshal raw call: %v", err)
		}
		id, _ := doc["id"].(string)
		if id == "" {
			if meta, ok := doc["metaData"].(map[string]any); ok {
				id, _ = meta["id"].(string)
			}
		}
		out = append(out, id)
	}
	return out
}

func blankCallDetailObjectNames(detail *sqlite.CallDetail) {
	if detail == nil {
		return
	}
	for idx := range detail.CRMObjects {
		detail.CRMObjects[idx].ObjectName = ""
	}
}

func jsonContainsKey(raw json.RawMessage, key string) bool {
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return false
	}
	_, ok := doc[key]
	return ok
}

func businessPilotCallPayloads() []json.RawMessage {
	return []json.RawMessage{
		json.RawMessage(`{"id":"bp-late-present","title":"Late present call","started":"2026-02-10T15:00:00Z","duration":1800,"metaData":{"scope":"External","system":"Zoom","direction":"Conference"},"context":[{"objectType":"Opportunity","id":"opp-late","name":"Late Opportunity","fields":[{"name":"StageName","value":"Contract Review"},{"name":"Type","value":"New Business"}]},{"objectType":"Account","id":"acct-late","name":"Late Account","fields":[{"name":"Account_Type__c","value":"Prospect"},{"name":"Industry","value":"Manufacturing"}]}]}`),
		json.RawMessage(`{"id":"bp-renewal-missing","title":"Renewal missing call","started":"2026-02-12T15:00:00Z","duration":2400,"metaData":{"scope":"External","system":"Zoom","direction":"Conference"},"context":{"crmObjects":[{"type":"Opportunity","id":"opp-renewal","name":"Renewal Opportunity","fields":{"StageName":"Discovery & Demo (SQO)","Type":"Renewal"}},{"type":"Account","id":"acct-renewal","name":"Renewal Account","fields":{"Account_Type__c":"Customer - Active","Industry":"Healthcare"}}]}}`),
		json.RawMessage(`{"id":"bp-upsell-missing","title":"Upsell missing call","started":"2026-02-13T15:00:00Z","duration":1200,"metaData":{"scope":"External","system":"Zoom","direction":"Conference"},"crmObjects":[{"type":"Opportunity","id":"opp-upsell","name":"Upsell Opportunity","fields":{"StageName":"Demo & Business Case","Expansion_Bookings__c":"24000","One_Year_Upsell__c":"12000"}}]}`),
		json.RawMessage(`{"id":"bp-outbound-missing","title":"Outbound missing call","started":"2026-02-14T15:00:00Z","duration":45,"metaData":{"system":"Upload API","direction":"Outbound"}}`),
	}
}

func businessPilotTranscriptPayloads() []json.RawMessage {
	return []json.RawMessage{
		json.RawMessage(`{"callId":"bp-late-present","transcript":[{"speakerId":"speaker-1","sentences":[{"start":0,"end":3000,"text":"The contract review depends on security approval."}]}]}`),
	}
}

func lifecycleSummaryByBucket(rows []sqlite.LifecycleBucketSummary) map[string]sqlite.LifecycleBucketSummary {
	out := make(map[string]sqlite.LifecycleBucketSummary, len(rows))
	for _, row := range rows {
		if row.Bucket == "" {
			continue
		}
		row.LatestCallID = ""
		out[row.Bucket] = row
	}
	return out
}

func factSummaryByValue(rows []sqlite.CallFactsSummaryRow) map[string]sqlite.CallFactsSummaryRow {
	out := make(map[string]sqlite.CallFactsSummaryRow, len(rows))
	for _, row := range rows {
		out[row.GroupValue] = row
	}
	return out
}

func crmObjectSummaryByType(rows []sqlite.CRMObjectTypeSummary) map[string]sqlite.CRMObjectTypeSummary {
	out := make(map[string]sqlite.CRMObjectTypeSummary, len(rows))
	for _, row := range rows {
		out[row.ObjectType] = row
	}
	return out
}

func crmFieldSummaryByName(rows []sqlite.CRMFieldSummary) map[string]sqlite.CRMFieldSummary {
	out := make(map[string]sqlite.CRMFieldSummary, len(rows))
	for _, row := range rows {
		row.ExampleValues = nil
		out[row.FieldName] = row
	}
	return out
}

func redactOpportunityMissingTranscriptSummaries(rows []sqlite.OpportunityMissingTranscriptSummary) {
	for idx := range rows {
		rows[idx].OpportunityID = ""
		rows[idx].OpportunityName = ""
		rows[idx].LatestCallID = ""
	}
}

func redactOpportunityCallSummaries(rows []sqlite.OpportunityCallSummary) {
	for idx := range rows {
		rows[idx].OpportunityID = ""
		rows[idx].OpportunityName = ""
		rows[idx].Amount = ""
		rows[idx].CloseDate = ""
		rows[idx].OwnerID = ""
		rows[idx].LatestCallID = ""
	}
}

func redactTranscriptCRMSearchResults(rows []sqlite.TranscriptCRMSearchResult) {
	for idx := range rows {
		rows[idx].CallID = ""
		rows[idx].Title = ""
		rows[idx].ObjectID = ""
		rows[idx].ObjectName = ""
		rows[idx].SpeakerID = ""
	}
}

func equalLifecycleSummaries(got map[string]sqlite.LifecycleBucketSummary, want map[string]sqlite.LifecycleBucketSummary) bool {
	keys := []string{"late_stage_sales", "renewal", "upsell_expansion", "outbound_prospecting"}
	for _, key := range keys {
		left, leftOK := got[key]
		right, rightOK := want[key]
		if !leftOK || !rightOK {
			return false
		}
		if left.CallCount != right.CallCount || left.TranscriptCount != right.TranscriptCount || left.MissingTranscriptCount != right.MissingTranscriptCount || left.OpportunityCallCount != right.OpportunityCallCount || left.AccountCallCount != right.AccountCallCount || left.TotalDurationSeconds != right.TotalDurationSeconds || left.LatestCallAt != right.LatestCallAt || left.HighConfidenceCalls != right.HighConfidenceCalls || left.MediumConfidenceCalls != right.MediumConfidenceCalls || left.LowConfidenceCalls != right.LowConfidenceCalls {
			return false
		}
	}
	return true
}

func equalFactSummaries(got map[string]sqlite.CallFactsSummaryRow, want map[string]sqlite.CallFactsSummaryRow) bool {
	if len(got) != len(want) {
		return false
	}
	keys := make([]string, 0, len(want))
	for key := range want {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		left, leftOK := got[key]
		right := want[key]
		if !leftOK {
			return false
		}
		if left.GroupBy != right.GroupBy || left.CallCount != right.CallCount || left.TranscriptCount != right.TranscriptCount || left.MissingTranscriptCount != right.MissingTranscriptCount || left.OpportunityCallCount != right.OpportunityCallCount || left.AccountCallCount != right.AccountCallCount || left.ExternalCallCount != right.ExternalCallCount || left.InternalCallCount != right.InternalCallCount || left.UnknownScopeCallCount != right.UnknownScopeCallCount || left.TotalDurationSeconds != right.TotalDurationSeconds || left.AvgDurationSeconds != right.AvgDurationSeconds || left.LatestCallAt != right.LatestCallAt || left.TranscriptCoverageRate != right.TranscriptCoverageRate {
			return false
		}
	}
	return true
}

func stringSlicesEqual(got []string, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
