package postgres

import (
	"context"
	"encoding/json"
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
	for _, table := range []string{"call_context_objects", "call_context_fields", "call_facts", "profile_meta", "profile_object_alias", "profile_field_concept", "profile_lifecycle_rule", "profile_methodology_concept", "profile_validation_warning", "profile_call_fact_cache_meta", "profile_call_fact_cache", "governance_policy_state", "governance_suppressed_calls"} {
		var exists bool
		if err := store.DB().QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = $1)`, table).Scan(&exists); err != nil {
			t.Fatalf("check table %s: %v", table, err)
		}
		if !exists {
			t.Fatalf("table %s does not exist", table)
		}
	}
	for _, index := range []string{"idx_pg_call_facts_lifecycle", "idx_pg_call_facts_transcript_status", "idx_pg_call_facts_search_filters", "idx_pg_calls_started_call_id", "idx_pg_call_context_objects_type_object_call", "idx_pg_call_context_fields_name_call_key_value", "idx_pg_profile_call_fact_cache_bucket", "idx_pg_profile_call_fact_cache_started"} {
		var exists bool
		if err := store.DB().QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname = current_schema() AND indexname = $1)`, index).Scan(&exists); err != nil {
			t.Fatalf("check index %s: %v", index, err)
		}
		if !exists {
			t.Fatalf("index %s does not exist", index)
		}
	}
	for _, function := range []string{"gongmcp_profile_call_fact_cache", "gongmcp_profile_call_fact_cache_meta", "gongmcp_profile_call_fact_summary", "gongmcp_profile_data_fingerprint", "gongmcp_governance_data_fingerprint", "gongmcp_governance_policy_state", "gongmcp_governance_suppressed_call_ids"} {
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

	if _, err := store.DB().ExecContext(ctx, `DELETE FROM gongctl_schema_migrations WHERE version = $1`, len(migrations)); err != nil {
		t.Fatalf("delete latest migration version: %v", err)
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

	if _, err := store.UpsertCall(ctx, json.RawMessage(`{"id":"pg-readmodel-001","title":"Renewal read model","started":"2026-02-12T15:00:00Z","duration":2400,"metaData":{"scope":"External","system":"Zoom","direction":"Conference","purpose":"Renewal review","calendarEventId":"cal-001"},"context":{"crmObjects":[{"type":"Opportunity","id":"opp-readmodel","name":"Renewal Opportunity","fields":{"StageName":"Discovery & Demo (SQO)","Type":"Renewal","Forecast_Category_VP__c":"Pipeline","Primary_Lead_Source__c":"Customer Success"}},{"type":"Account","id":"acct-readmodel","name":"Renewal Account","fields":{"Account_Type__c":"Customer - Active","Industry":"Healthcare"}}]}}`)); err != nil {
		t.Fatalf("UpsertCall returned error: %v", err)
	}
	assertPostgresCount(t, ctx, store, `SELECT COUNT(*) FROM call_context_objects WHERE call_id = 'pg-readmodel-001'`, 2)
	assertPostgresCount(t, ctx, store, `SELECT COUNT(*) FROM call_context_fields WHERE call_id = 'pg-readmodel-001'`, 6)

	var callDate, callMonth, durationBucket, scope, system, direction, transcriptStatus, lifecycleBucket, lifecycleConfidence, opportunityID, opportunityType, accountIndustry string
	var transcriptPresent bool
	var opportunityCount, accountCount int64
	if err := store.DB().QueryRowContext(ctx, `SELECT call_date, call_month, duration_bucket, scope, system, direction, transcript_present, transcript_status, lifecycle_bucket, lifecycle_confidence, opportunity_id, opportunity_type, account_industry, opportunity_count, account_count FROM call_facts WHERE call_id = $1`, "pg-readmodel-001").Scan(&callDate, &callMonth, &durationBucket, &scope, &system, &direction, &transcriptPresent, &transcriptStatus, &lifecycleBucket, &lifecycleConfidence, &opportunityID, &opportunityType, &accountIndustry, &opportunityCount, &accountCount); err != nil {
		t.Fatalf("read call_facts: %v", err)
	}
	if callDate != "2026-02-12" || callMonth != "2026-02" || durationBucket != "30_45m" || scope != "External" || system != "Zoom" || direction != "Conference" {
		t.Fatalf("unexpected call fact dimensions: date=%s month=%s duration=%s scope=%s system=%s direction=%s", callDate, callMonth, durationBucket, scope, system, direction)
	}
	if transcriptPresent || transcriptStatus != "missing" || lifecycleBucket != "renewal" || lifecycleConfidence != "high" || opportunityID != "opp-readmodel" || opportunityType != "Renewal" || accountIndustry != "Healthcare" || opportunityCount != 1 || accountCount != 1 {
		t.Fatalf("unexpected CRM/lifecycle facts: present=%v status=%s bucket=%s confidence=%s opp=%s type=%s industry=%s opp_count=%d acct_count=%d", transcriptPresent, transcriptStatus, lifecycleBucket, lifecycleConfidence, opportunityID, opportunityType, accountIndustry, opportunityCount, accountCount)
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
	if err := store.DB().QueryRowContext(ctx, `SELECT transcript_present, transcript_status, opportunity_id, lifecycle_bucket FROM call_facts WHERE call_id = $1`, "pg-readmodel-001").Scan(&transcriptPresent, &transcriptStatus, &opportunityID, &lifecycleBucket); err != nil {
		t.Fatalf("read empty-context facts: %v", err)
	}
	if !transcriptPresent || transcriptStatus != "present" || opportunityID != "" || lifecycleBucket != "unknown" {
		t.Fatalf("empty context facts not cleared/preserved correctly: present=%v status=%s opp=%s bucket=%s", transcriptPresent, transcriptStatus, opportunityID, lifecycleBucket)
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
	_, err := store.DB().ExecContext(ctx, `TRUNCATE governance_suppressed_calls, governance_policy_state, profile_call_fact_cache, profile_call_fact_cache_meta, profile_validation_warning, profile_methodology_concept, profile_lifecycle_rule, profile_field_concept, profile_object_alias, profile_meta, call_read_model_diagnostics, postgres_read_model_state, call_facts, call_context_fields, call_context_objects, transcript_segments, transcripts, calls, users, sync_state, sync_runs RESTART IDENTITY CASCADE`)
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
