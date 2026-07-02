package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fyne-coder/gongcli_mcp/internal/governance"
	"github.com/fyne-coder/gongcli_mcp/internal/mcp"
	"github.com/fyne-coder/gongcli_mcp/internal/store/postgres"
	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
)

type fakeNoGovernanceExclusionsPolicyReader struct {
	state          *postgres.GovernancePolicyState
	loadErr        error
	fingerprint    string
	fingerprintErr error
}

func (f fakeNoGovernanceExclusionsPolicyReader) LoadGovernancePolicy(context.Context, string) (*postgres.GovernancePolicyState, error) {
	if f.loadErr != nil {
		return nil, f.loadErr
	}
	return f.state, nil
}

func (f fakeNoGovernanceExclusionsPolicyReader) GovernanceDataFingerprint(context.Context) (string, error) {
	if f.fingerprintErr != nil {
		return "", f.fingerprintErr
	}
	return f.fingerprint, nil
}

func TestRunRequiresDBFlag(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run(nil, bytes.NewReader(nil), &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code=%d want 2", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout not empty: %q", stdout.String())
	}
	if got := stderr.String(); got == "" || !bytes.Contains([]byte(got), []byte("--db is required")) {
		t.Fatalf("stderr=%q want missing --db message", got)
	}
}

func TestRunRejectsInvalidBusinessTopicPacksConfig(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "topic-packs.yaml")
	if err := os.WriteFile(configPath, []byte("topic_packs:\n  generic_b2b:\n    aliases:\n      pricing:\n        - budget\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"--business-topic-packs-config", configPath}, bytes.NewReader(nil), &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code=%d want 2 stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "invalid business topic packs config") {
		t.Fatalf("stderr=%q want invalid business topic packs config", stderr.String())
	}
}

func TestRunLoadsBusinessTopicPacksConfigFromEnv(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gong.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open returned error: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	dir := t.TempDir()
	configPath := filepath.Join(dir, "topic-packs.yaml")
	if err := os.WriteFile(configPath, []byte(`topic_packs:
  product_readiness:
    description: "Local readiness vocabulary."
    aliases:
      implementation:
        - launch readiness
`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Setenv("GONGMCP_BUSINESS_TOPIC_PACKS_CONFIG", configPath)
	t.Setenv("GONGMCP_TOOL_PRESET", "business-workbench")
	stdin := strings.NewReader(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
	}, "\n") + "\n")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"--db", dbPath, "--stdio"}, stdin, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "business topic packs active") || !strings.Contains(stderr.String(), "product_readiness") {
		t.Fatalf("stderr=%q missing business topic packs startup summary", stderr.String())
	}
}

func TestParseCommaListTrimsSkipsEmptyAndDedupes(t *testing.T) {
	t.Parallel()

	got := parseCommaList(" internal.example, , buyer.example,internal.example ")
	want := []string{"internal.example", "buyer.example"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseCommaList=%v want %v", got, want)
	}
	if got := parseCommaList(" \t "); got != nil {
		t.Fatalf("parseCommaList blank=%v want nil", got)
	}
}

func TestPostgresToolAllowlistDefaultsToSupportedSlice(t *testing.T) {
	allowlist, err := postgresToolAllowlist(nil, false, "")
	if err != nil {
		t.Fatalf("postgresToolAllowlist returned error: %v", err)
	}
	want := []string{"get_sync_status", "search_calls", "search_transcript_segments"}
	if !reflect.DeepEqual(allowlist, want) {
		t.Fatalf("allowlist=%v want %v", allowlist, want)
	}
}

func TestPostgresToolAllowlistAcceptsCoreQueryParityTools(t *testing.T) {
	allowlist, err := postgresToolAllowlist([]string{"get_sync_status", "search_calls", "get_call", "search_transcript_segments"}, false, "")
	if err != nil {
		t.Fatalf("postgresToolAllowlist returned error: %v", err)
	}
	want := []string{"get_sync_status", "search_calls", "get_call", "search_transcript_segments"}
	if !reflect.DeepEqual(allowlist, want) {
		t.Fatalf("allowlist=%v want %v", allowlist, want)
	}
}

func TestPostgresToolAllowlistAcceptsCRMContextTranscriptSearch(t *testing.T) {
	allowlist, err := postgresToolAllowlist([]string{"search_transcripts_by_crm_context"}, false, "")
	if err != nil {
		t.Fatalf("postgresToolAllowlist returned error: %v", err)
	}
	if !reflect.DeepEqual(allowlist, []string{"search_transcripts_by_crm_context"}) {
		t.Fatalf("allowlist=%v want search_transcripts_by_crm_context", allowlist)
	}
}

func TestPostgresToolAllowlistAcceptsLifecycleCRMFieldComparison(t *testing.T) {
	allowlist, err := postgresToolAllowlist([]string{"compare_lifecycle_crm_fields"}, false, "")
	if err != nil {
		t.Fatalf("postgresToolAllowlist returned error: %v", err)
	}
	if !reflect.DeepEqual(allowlist, []string{"compare_lifecycle_crm_fields"}) {
		t.Fatalf("allowlist=%v want compare_lifecycle_crm_fields", allowlist)
	}
}

func TestPostgresToolAllowlistRejectsUnsupportedTools(t *testing.T) {
	if _, err := postgresToolAllowlist([]string{"not_a_tool"}, false, ""); err == nil {
		t.Fatal("postgresToolAllowlist accepted unsupported tool")
	}
}

func TestPostgresToolAllowlistAcceptsOpportunitiesMissingTranscripts(t *testing.T) {
	allowlist, err := postgresToolAllowlist([]string{"opportunities_missing_transcripts"}, false, "")
	if err != nil {
		t.Fatalf("postgresToolAllowlist returned error: %v", err)
	}
	if !reflect.DeepEqual(allowlist, []string{"opportunities_missing_transcripts"}) {
		t.Fatalf("allowlist=%v want opportunities_missing_transcripts", allowlist)
	}
}

func TestPostgresToolAllowlistAcceptsMissingTranscripts(t *testing.T) {
	allowlist, err := postgresToolAllowlist([]string{"missing_transcripts"}, false, "")
	if err != nil {
		t.Fatalf("postgresToolAllowlist returned error: %v", err)
	}
	if !reflect.DeepEqual(allowlist, []string{"missing_transcripts"}) {
		t.Fatalf("allowlist=%v want missing_transcripts", allowlist)
	}
}

func TestPostgresReadOnlyOptionsForBusinessPilotAllowlist(t *testing.T) {
	expanded, err := mcp.ExpandToolPreset("business-pilot")
	if err != nil {
		t.Fatalf("ExpandToolPreset returned error: %v", err)
	}
	allowlist, err := postgresToolAllowlist(expanded, true, "business-pilot")
	if err != nil {
		t.Fatalf("postgresToolAllowlist returned error: %v", err)
	}
	options := postgresReadOnlyOptionsForAllowlist(allowlist)
	if !options.EnforceAllowedFunctionBoundary {
		t.Fatal("expected tool-scoped grant enforcement")
	}
	for _, signature := range options.AllowedFunctionSignatures {
		if strings.Contains(signature, "gongmcp_missing_transcripts") {
			t.Fatalf("business-pilot scoped functions included admin missing_transcripts function: %v", options.AllowedFunctionSignatures)
		}
	}
	if !options.EnforceAllowedColumnBoundary {
		t.Fatal("expected business-pilot scoped column grant enforcement")
	}
	for _, grant := range options.AllowedColumnSelectGrants {
		if grant.Table == "calls" && (grant.Column == "call_id" || grant.Column == "title") {
			t.Fatalf("business-pilot scoped columns included direct calls.%s grant: %v", grant.Column, options.AllowedColumnSelectGrants)
		}
		if grant.Table == "call_facts" && (grant.Column == "call_id" || grant.Column == "title") {
			t.Fatalf("business-pilot scoped columns included direct call_facts.%s grant: %v", grant.Column, options.AllowedColumnSelectGrants)
		}
	}
	for _, want := range []postgres.ColumnSelectGrant{
		{Table: "gongctl_schema_migrations", Column: "version"},
		{Table: "calls", Column: "started_at"},
		{Table: "postgres_read_model_state", Column: "model_name"},
		{Table: "gongmcp_sync_runs", Column: "status"},
		{Table: "gongmcp_sync_state", Column: "sync_key"},
	} {
		if !containsColumnSelectGrant(options.RequiredColumnSelectGrants, want) {
			t.Fatalf("business-pilot required columns=%v missing %s.%s", options.RequiredColumnSelectGrants, want.Table, want.Column)
		}
	}
	for _, want := range []string{
		"public.gongmcp_active_business_profile_sanitized()",
		"public.gongmcp_profile_call_fact_cache_sanitized_limited(bigint, text, integer)",
		"public.gongmcp_profile_call_fact_cache_meta_sanitized(bigint)",
		"public.gongmcp_profile_call_fact_summary_sanitized(bigint, text, text, text, text, text, text, text, integer)",
		"public.gongmcp_profile_data_fingerprint()",
		"public.gongmcp_profile_lifecycle_summary_sanitized(bigint, text, text)",
		"public.gongmcp_profile_transcript_backlog_sanitized(bigint, text, text, text, text, text, text, text, integer)",
		"public.gongmcp_scorecard_activity_totals()",
	} {
		if !containsString(options.RequiredFunctionSignatures, want) {
			t.Fatalf("business-pilot required functions=%v missing %s", options.RequiredFunctionSignatures, want)
		}
	}
	if containsString(options.RequiredFunctionSignatures, "public.gongmcp_profile_call_fact_cache(bigint, text)") {
		t.Fatalf("business-pilot required functions included identifier-bearing profile cache helper: %v", options.RequiredFunctionSignatures)
	}
	if containsString(options.RequiredFunctionSignatures, "public.gongmcp_profile_call_fact_cache_meta(bigint, text)") {
		t.Fatalf("business-pilot required functions included canonical profile cache metadata helper: %v", options.RequiredFunctionSignatures)
	}
	if containsString(options.RequiredFunctionSignatures, "public.gongmcp_profile_call_fact_summary(bigint, text, text, text, text, text, text, text, integer)") {
		t.Fatalf("business-pilot required functions included CRM-value profile summary helper: %v", options.RequiredFunctionSignatures)
	}
	if containsString(options.RequiredFunctionSignatures, "public.gongmcp_active_business_profile()") {
		t.Fatalf("business-pilot required functions included identifier-bearing active profile helper: %v", options.RequiredFunctionSignatures)
	}
}

func TestBuildPostgresReaderGrantSQLBusinessPilot(t *testing.T) {
	expanded, err := mcp.ExpandToolPreset("business-pilot")
	if err != nil {
		t.Fatalf("ExpandToolPreset returned error: %v", err)
	}
	allowlist, err := postgresToolAllowlist(expanded, true, "business-pilot")
	if err != nil {
		t.Fatalf("postgresToolAllowlist returned error: %v", err)
	}
	sql, err := buildPostgresReaderGrantSQL(allowlist, "gongmcp_business_pilot_reader", "gongctl")
	if err != nil {
		t.Fatalf("buildPostgresReaderGrantSQL returned error: %v", err)
	}
	for _, want := range []string{
		`-- Generated by gongmcp --print-postgres-reader-grants.`,
		`-- Role and credentials must already exist; create and rotate passwords outside this grant block using your secret manager.`,
		`-- This block reconciles reviewed grants for the named role by clearing existing public table/function/sequence privileges before regranting the selected surface.`,
		`-- It reconciles current public objects only; gongmcp startup rejects default privileges that would grant future public objects to this service role.`,
		`REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA "public" FROM "gongmcp_business_pilot_reader";`,
		`REVOKE ALL PRIVILEGES ON ALL FUNCTIONS IN SCHEMA "public" FROM "gongmcp_business_pilot_reader";`,
		`reviewed business-workbench/facade, business-pilot, analyst, and redacted-all-readonly scoped readers`,
		`GRANT CONNECT ON DATABASE "gongctl" TO "gongmcp_business_pilot_reader";`,
		`REVOKE CREATE ON SCHEMA "public" FROM PUBLIC;`,
		`GRANT USAGE ON SCHEMA "public" TO "gongmcp_business_pilot_reader";`,
		`REVOKE EXECUTE ON FUNCTION public.gongmcp_active_business_profile() FROM "gongmcp_business_pilot_reader";`,
		`REVOKE EXECUTE ON FUNCTION public.gongmcp_profile_call_fact_cache(bigint, text) FROM "gongmcp_business_pilot_reader";`,
		`REVOKE EXECUTE ON FUNCTION public.gongmcp_profile_call_fact_cache_sanitized(bigint, text) FROM "gongmcp_business_pilot_reader";`,
		`GRANT SELECT ("context_present", "parties_count", "started_at") ON TABLE public."calls" TO "gongmcp_business_pilot_reader";`,
		`GRANT SELECT ("account_count", "account_industry", "duration_seconds", "lifecycle_bucket", "lifecycle_confidence", "likely_voicemail_or_ivr", "opportunity_count", "opportunity_stage", "started_at", "transcript_present", "transcript_status") ON TABLE public."call_facts" TO "gongmcp_business_pilot_reader";`,
		`GRANT EXECUTE ON FUNCTION public.gongmcp_active_business_profile_sanitized() TO "gongmcp_business_pilot_reader";`,
		`GRANT EXECUTE ON FUNCTION public.gongmcp_profile_call_fact_cache_meta_sanitized(bigint) TO "gongmcp_business_pilot_reader";`,
		`GRANT EXECUTE ON FUNCTION public.gongmcp_profile_call_fact_cache_sanitized_limited(bigint, text, integer) TO "gongmcp_business_pilot_reader";`,
		`GRANT EXECUTE ON FUNCTION public.gongmcp_profile_call_fact_summary_sanitized(bigint, text, text, text, text, text, text, text, integer) TO "gongmcp_business_pilot_reader";`,
		`GRANT EXECUTE ON FUNCTION public.gongmcp_profile_lifecycle_summary_sanitized(bigint, text, text) TO "gongmcp_business_pilot_reader";`,
		`GRANT EXECUTE ON FUNCTION public.gongmcp_profile_transcript_backlog_sanitized(bigint, text, text, text, text, text, text, text, integer) TO "gongmcp_business_pilot_reader";`,
		`COMMIT;`,
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("generated SQL missing %q\n%s", want, sql)
		}
	}
	for _, forbidden := range []string{
		`PASSWORD '`,
		`"call_id", "title"`,
		`"call_id"`,
		`"object_key"`,
		`GRANT EXECUTE ON FUNCTION public.gongmcp_active_business_profile()`,
		`GRANT EXECUTE ON FUNCTION public.gongmcp_profile_call_fact_cache(bigint, text)`,
		`GRANT EXECUTE ON FUNCTION public.gongmcp_profile_call_fact_cache_meta(bigint, text)`,
		`GRANT EXECUTE ON FUNCTION public.gongmcp_profile_call_fact_cache_sanitized(bigint, text)`,
		`GRANT EXECUTE ON FUNCTION public.gongmcp_profile_call_fact_summary(bigint, text, text, text, text, text, text, text, integer)`,
		`public.gongmcp_missing_transcripts`,
		`field_values_json`,
	} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("generated SQL included forbidden %q\n%s", forbidden, sql)
		}
	}
	for _, line := range strings.Split(sql, "\n") {
		if strings.Contains(line, `ON TABLE public."calls"`) && strings.Contains(line, `"call_id"`) {
			t.Fatalf("generated SQL included calls.call_id grant\n%s", sql)
		}
		if strings.Contains(line, `ON TABLE public."calls"`) && strings.Contains(line, `"title"`) {
			t.Fatalf("generated SQL included calls.title grant\n%s", sql)
		}
		if strings.Contains(line, `ON TABLE public."call_facts"`) && strings.Contains(line, `"call_id"`) {
			t.Fatalf("generated SQL included call_facts.call_id grant\n%s", sql)
		}
		if strings.Contains(line, `ON TABLE public."call_facts"`) && strings.Contains(line, `"title"`) {
			t.Fatalf("generated SQL included call_facts.title grant\n%s", sql)
		}
	}
}

func TestBuildPostgresReaderGrantSQLRejectsUnsupportedSurface(t *testing.T) {
	if _, err := buildPostgresReaderGrantSQL([]string{"get_sync_status", "search_calls"}, "gongmcp_reader", "gongctl"); err == nil {
		t.Fatal("buildPostgresReaderGrantSQL accepted unsupported non-business-pilot surface")
	}
}

func TestBuildPostgresReaderGrantSQLRejectsUnsafeIdentifiers(t *testing.T) {
	expanded, err := mcp.ExpandToolPreset("business-pilot")
	if err != nil {
		t.Fatalf("ExpandToolPreset returned error: %v", err)
	}
	allowlist, err := postgresToolAllowlist(expanded, true, "business-pilot")
	if err != nil {
		t.Fatalf("postgresToolAllowlist returned error: %v", err)
	}
	for _, tc := range []struct {
		name     string
		roleName string
		dbName   string
	}{
		{name: "role injection", roleName: `reader"; DROP ROLE writer; --`, dbName: "gongctl"},
		{name: "database dash", roleName: "gongmcp_reader", dbName: "gongctl-prod"},
		{name: "role starts digit", roleName: "1_reader", dbName: "gongctl"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := buildPostgresReaderGrantSQL(allowlist, tc.roleName, tc.dbName); err == nil {
				t.Fatal("buildPostgresReaderGrantSQL accepted unsafe identifier")
			}
		})
	}
}

func TestPrintPostgresReaderGrantsForBusinessPilot(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{
		"--print-postgres-reader-grants",
		"--tool-preset", "business-pilot",
		"--postgres-reader-role", "gongmcp_business_pilot_reader",
		"--postgres-database", "gongctl",
	}, bytes.NewReader(nil), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run code=%d stderr=%q", code, stderr.String())
	}
	sql := stdout.String()
	for _, want := range []string{
		`GRANT CONNECT ON DATABASE "gongctl" TO "gongmcp_business_pilot_reader";`,
		`REVOKE CREATE ON SCHEMA "public" FROM PUBLIC;`,
		`REVOKE EXECUTE ON FUNCTION public.gongmcp_active_business_profile() FROM "gongmcp_business_pilot_reader";`,
		`REVOKE EXECUTE ON FUNCTION public.gongmcp_profile_call_fact_cache(bigint, text) FROM "gongmcp_business_pilot_reader";`,
		`REVOKE EXECUTE ON FUNCTION public.gongmcp_profile_call_fact_cache_sanitized(bigint, text) FROM "gongmcp_business_pilot_reader";`,
		`GRANT SELECT ("context_present", "parties_count", "started_at") ON TABLE public."calls" TO "gongmcp_business_pilot_reader";`,
		`GRANT SELECT ("account_count", "account_industry", "duration_seconds", "lifecycle_bucket", "lifecycle_confidence", "likely_voicemail_or_ivr", "opportunity_count", "opportunity_stage", "started_at", "transcript_present", "transcript_status") ON TABLE public."call_facts" TO "gongmcp_business_pilot_reader";`,
		`GRANT EXECUTE ON FUNCTION public.gongmcp_active_business_profile_sanitized() TO "gongmcp_business_pilot_reader";`,
		`GRANT EXECUTE ON FUNCTION public.gongmcp_profile_call_fact_cache_meta_sanitized(bigint) TO "gongmcp_business_pilot_reader";`,
		`GRANT EXECUTE ON FUNCTION public.gongmcp_profile_call_fact_cache_sanitized_limited(bigint, text, integer) TO "gongmcp_business_pilot_reader";`,
		`GRANT EXECUTE ON FUNCTION public.gongmcp_profile_call_fact_summary_sanitized(bigint, text, text, text, text, text, text, text, integer) TO "gongmcp_business_pilot_reader";`,
		`GRANT EXECUTE ON FUNCTION public.gongmcp_profile_lifecycle_summary_sanitized(bigint, text, text) TO "gongmcp_business_pilot_reader";`,
		`GRANT EXECUTE ON FUNCTION public.gongmcp_profile_transcript_backlog_sanitized(bigint, text, text, text, text, text, text, text, integer) TO "gongmcp_business_pilot_reader";`,
		`COMMIT;`,
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("generated SQL missing %q\n%s", want, sql)
		}
	}
	for _, forbidden := range []string{
		`"call_id") ON TABLE public."calls"`,
		`"title") ON TABLE public."calls"`,
		`"call_id") ON TABLE public."call_facts"`,
		`"title") ON TABLE public."call_facts"`,
		`"call_id"`,
		`"object_key"`,
		`GRANT EXECUTE ON FUNCTION public.gongmcp_active_business_profile()`,
		`GRANT EXECUTE ON FUNCTION public.gongmcp_profile_call_fact_cache(bigint, text)`,
		`GRANT EXECUTE ON FUNCTION public.gongmcp_profile_call_fact_cache_meta(bigint, text)`,
		`GRANT EXECUTE ON FUNCTION public.gongmcp_profile_call_fact_cache_sanitized(bigint, text)`,
		`GRANT EXECUTE ON FUNCTION public.gongmcp_profile_call_fact_summary(bigint, text, text, text, text, text, text, text, integer)`,
		`PASSWORD`,
		`postgres://`,
		`GONG_DATABASE_URL`,
	} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("generated SQL contained forbidden %q\n%s", forbidden, sql)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr not empty: %q", stderr.String())
	}
}

func TestPrintPostgresReaderGrantsRejectsUnsupportedPreset(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{
		"--print-postgres-reader-grants",
		"--tool-preset", "operator-smoke",
	}, bytes.NewReader(nil), &stdout, &stderr)
	if code != 2 {
		t.Fatalf("run code=%d want 2 stderr=%q", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout not empty: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "reviewed business-workbench/facade, business-pilot, analyst, and redacted-all-readonly scoped reader surfaces") {
		t.Fatalf("stderr=%q missing unsupported preset message", stderr.String())
	}
}

func TestPrintPostgresReaderGrantsRejectsUnsafeIdentifier(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{
		"--print-postgres-reader-grants",
		"--tool-preset", "business-pilot",
		"--postgres-reader-role", "gongmcp_reader;DROP",
	}, bytes.NewReader(nil), &stdout, &stderr)
	if code != 2 {
		t.Fatalf("run code=%d want 2 stderr=%q", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout not empty: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "invalid --postgres-reader-role") {
		t.Fatalf("stderr=%q missing unsafe identifier message", stderr.String())
	}
}

func TestPostgresReadOnlyOptionsForMissingTranscriptsAllowlist(t *testing.T) {
	options := postgresReadOnlyOptionsForAllowlist([]string{"missing_transcripts"})
	want := "public.gongmcp_missing_transcripts(text, text, text, text, text, text, text, text, integer)"
	if !containsString(options.RequiredFunctionSignatures, want) {
		t.Fatalf("missing_transcripts required functions=%v missing %s", options.RequiredFunctionSignatures, want)
	}
	if !reflect.DeepEqual(options.RequiredFunctionSignatures, options.AllowedFunctionSignatures) {
		t.Fatalf("required=%v allowed=%v", options.RequiredFunctionSignatures, options.AllowedFunctionSignatures)
	}
}

func TestPostgresReadOnlyOptionsAllowFunctionFreeAllowlist(t *testing.T) {
	options := postgresReadOnlyOptionsForAllowlist([]string{"search_calls", "get_call"})
	if !options.EnforceAllowedFunctionBoundary {
		t.Fatal("expected tool-scoped grant enforcement")
	}
	if len(options.RequiredFunctionSignatures) != 0 {
		t.Fatalf("function-free allowlist required functions=%v, want none", options.RequiredFunctionSignatures)
	}
	if len(options.AllowedFunctionSignatures) != 0 {
		t.Fatalf("function-free allowlist allowed functions=%v, want none", options.AllowedFunctionSignatures)
	}
}

func TestPostgresReadOnlyOptionsForFilteredTranscriptSearchIncludesCallFactFunction(t *testing.T) {
	options := postgresReadOnlyOptionsForAllowlist([]string{"search_transcript_segments"})
	want := "public.gongmcp_search_transcript_segments_by_call_facts(text, text, text, text, text, text, text, integer)"
	if !containsString(options.RequiredFunctionSignatures, want) {
		t.Fatalf("search_transcript_segments required functions=%v missing %s", options.RequiredFunctionSignatures, want)
	}
}

func TestPostgresReadOnlyOptionsForBusinessAnalysisToolsIncludeCoreFunctions(t *testing.T) {
	options := postgresReadOnlyOptionsForAllowlist([]string{"extract_theme_quotes"})
	for _, want := range []string{
		"public.gongmcp_business_analysis_calls(text, text, text, text, text, text, text, text, text, text, text, text, text, text, text, text, boolean, integer, text)",
		"public.gongmcp_business_analysis_summary(text, text, text, text, text, text, text, text, text, text, text, text, text, text, text, text, boolean, text)",
		"public.gongmcp_business_analysis_evidence(text, text, text, text, text, text, text, text, text, text, text, text, text, text, text, text, text, boolean, integer, text)",
	} {
		if !containsString(options.RequiredFunctionSignatures, want) {
			t.Fatalf("extract_theme_quotes required functions=%v missing %s", options.RequiredFunctionSignatures, want)
		}
	}
	if containsString(options.RequiredFunctionSignatures, "public.gongmcp_business_analysis_dimension_filters_match(text, text, text, text, text, text, text, text, text, text, text, text, text, text)") {
		t.Fatalf("extract_theme_quotes should not require direct grant on internal dimension filter helper: %v", options.RequiredFunctionSignatures)
	}
}

func TestPostgresToolAllowlistAcceptsOpportunityCallSummary(t *testing.T) {
	allowlist, err := postgresToolAllowlist([]string{"opportunity_call_summary"}, false, "")
	if err != nil {
		t.Fatalf("postgresToolAllowlist returned error: %v", err)
	}
	if !reflect.DeepEqual(allowlist, []string{"opportunity_call_summary"}) {
		t.Fatalf("allowlist=%v want opportunity_call_summary", allowlist)
	}
}

func TestPostgresToolAllowlistAcceptsCRMFieldPopulationMatrix(t *testing.T) {
	allowlist, err := postgresToolAllowlist([]string{"crm_field_population_matrix"}, false, "")
	if err != nil {
		t.Fatalf("postgresToolAllowlist returned error: %v", err)
	}
	if !reflect.DeepEqual(allowlist, []string{"crm_field_population_matrix"}) {
		t.Fatalf("allowlist=%v want crm_field_population_matrix", allowlist)
	}
}

func TestPostgresToolAllowlistAcceptsLateStageCRMSignals(t *testing.T) {
	allowlist, err := postgresToolAllowlist([]string{"analyze_late_stage_crm_signals"}, false, "")
	if err != nil {
		t.Fatalf("postgresToolAllowlist returned error: %v", err)
	}
	if !reflect.DeepEqual(allowlist, []string{"analyze_late_stage_crm_signals"}) {
		t.Fatalf("allowlist=%v want analyze_late_stage_crm_signals", allowlist)
	}
}

func TestPostgresToolAllowlistAcceptsUnmappedCRMFields(t *testing.T) {
	allowlist, err := postgresToolAllowlist([]string{"list_unmapped_crm_fields"}, false, "")
	if err != nil {
		t.Fatalf("postgresToolAllowlist returned error: %v", err)
	}
	if !reflect.DeepEqual(allowlist, []string{"list_unmapped_crm_fields"}) {
		t.Fatalf("allowlist=%v want list_unmapped_crm_fields", allowlist)
	}
}

func TestPostgresToolAllowlistAcceptsCRMFieldValueSearch(t *testing.T) {
	allowlist, err := postgresToolAllowlist([]string{"search_crm_field_values"}, false, "")
	if err != nil {
		t.Fatalf("postgresToolAllowlist returned error: %v", err)
	}
	if !reflect.DeepEqual(allowlist, []string{"search_crm_field_values"}) {
		t.Fatalf("allowlist=%v want search_crm_field_values", allowlist)
	}
}

func TestPostgresToolAllowlistAcceptsAnalystCorePreset(t *testing.T) {
	expanded, err := mcp.ExpandToolPreset("analyst-core")
	if err != nil {
		t.Fatalf("ExpandToolPreset returned error: %v", err)
	}
	allowlist, err := postgresToolAllowlist(expanded, true, "analyst-core")
	if err != nil {
		t.Fatalf("postgresToolAllowlist returned error: %v", err)
	}
	want := []string{"get_sync_status", "search_calls", "get_call", "list_crm_object_types", "list_crm_fields", "list_crm_integrations", "list_cached_crm_schema_objects", "list_cached_crm_schema_fields", "list_gong_settings", "list_scorecards", "get_scorecard", "summarize_scorecard_activity", "get_business_profile", "list_business_concepts", "list_lifecycle_buckets", "summarize_calls_by_lifecycle", "search_calls_by_lifecycle", "prioritize_transcripts_by_lifecycle", "summarize_call_facts", "rank_transcript_backlog", "search_transcript_segments"}
	if !reflect.DeepEqual(allowlist, want) {
		t.Fatalf("allowlist=%v want %v", allowlist, want)
	}
}

func TestPostgresToolAllowlistAcceptsAnalystBusinessCorePreset(t *testing.T) {
	expanded, err := mcp.ExpandToolPreset("analyst-business-core")
	if err != nil {
		t.Fatalf("ExpandToolPreset returned error: %v", err)
	}
	allowlist, err := postgresToolAllowlist(expanded, true, "analyst-business-core")
	if err != nil {
		t.Fatalf("postgresToolAllowlist returned error: %v", err)
	}
	for _, name := range []string{"search_transcripts_by_call_facts", "search_transcript_quotes_with_attribution", "search_transcripts_by_filters", "summarize_themes_by_dimension"} {
		if !containsString(allowlist, name) {
			t.Fatalf("analyst-business-core missing %s: %v", name, allowlist)
		}
	}
}

func TestPostgresToolAllowlistAcceptsAnalystFacadePreset(t *testing.T) {
	expanded, err := mcp.ExpandToolPreset("analyst-facade")
	if err != nil {
		t.Fatalf("ExpandToolPreset returned error: %v", err)
	}
	allowlist, err := postgresToolAllowlist(expanded, true, "analyst-facade")
	if err != nil {
		t.Fatalf("postgresToolAllowlist returned error: %v", err)
	}
	if !reflect.DeepEqual(allowlist, mcp.FacadeToolNames()) {
		t.Fatalf("allowlist=%v want facade tools", allowlist)
	}
	routed, err := mcp.ExpandToolPresetFacadeRoutedTools("analyst-facade")
	if err != nil {
		t.Fatalf("ExpandToolPresetFacadeRoutedTools returned error: %v", err)
	}
	analyst, err := mcp.ExpandToolPreset("analyst")
	if err != nil {
		t.Fatalf("ExpandToolPreset(analyst) returned error: %v", err)
	}
	wantRouted := append(append([]string{}, analyst...), mcp.FacadeHiddenRoutedToolNames()...)
	if !reflect.DeepEqual(routed, wantRouted) {
		t.Fatalf("routed=%v want analyst tools plus internal highlights and question-answer routes %v", routed, wantRouted)
	}
	if min := postgresAnalystSmallCellMin(true, "analyst-facade", true); min != 3 {
		t.Fatalf("postgresAnalystSmallCellMin=%d want 3", min)
	}
	if got := readerGrantAllowlist(allowlist, routed); !reflect.DeepEqual(got, wantRouted) {
		t.Fatalf("readerGrantAllowlist=%v want analyst tools plus internal highlights and question-answer routes %v", got, wantRouted)
	}
}

func TestPostgresToolAllowlistAcceptsBusinessWorkbenchPreset(t *testing.T) {
	expanded, err := mcp.ExpandToolPreset("business-workbench")
	if err != nil {
		t.Fatalf("ExpandToolPreset returned error: %v", err)
	}
	allowlist, err := postgresToolAllowlist(expanded, true, "business-workbench")
	if err != nil {
		t.Fatalf("postgresToolAllowlist returned error: %v", err)
	}
	if !reflect.DeepEqual(allowlist, mcp.FacadeToolNames()) {
		t.Fatalf("allowlist=%v want facade tools", allowlist)
	}
	if len(allowlist) != 6 {
		t.Fatalf("business-workbench visible allowlist len=%d want 6 facade tools", len(allowlist))
	}
	routed, err := mcp.ExpandToolPresetFacadeRoutedTools("business-workbench")
	if err != nil {
		t.Fatalf("ExpandToolPresetFacadeRoutedTools returned error: %v", err)
	}
	analyst, err := mcp.ExpandToolPreset("analyst")
	if err != nil {
		t.Fatalf("ExpandToolPreset(analyst) returned error: %v", err)
	}
	wantRouted := append(append([]string{}, analyst...), mcp.FacadeHiddenRoutedToolNames()...)
	if !reflect.DeepEqual(routed, wantRouted) {
		t.Fatalf("routed=%v want analyst tools plus internal highlights and question-answer routes %v", routed, wantRouted)
	}
	if min := postgresAnalystSmallCellMin(true, "business-workbench", true); min != 3 {
		t.Fatalf("postgresAnalystSmallCellMin(business-workbench)=%d want 3", min)
	}
	if got := readerGrantAllowlist(allowlist, routed); !reflect.DeepEqual(got, wantRouted) {
		t.Fatalf("readerGrantAllowlist=%v want analyst tools plus internal highlights and question-answer routes %v", got, wantRouted)
	}
}

func TestPostgresToolAllowlistAcceptsBusinessPilotPreset(t *testing.T) {
	expanded, err := mcp.ExpandToolPreset("business-pilot")
	if err != nil {
		t.Fatalf("ExpandToolPreset returned error: %v", err)
	}
	allowlist, err := postgresToolAllowlist(expanded, true, "business-pilot")
	if err != nil {
		t.Fatalf("postgresToolAllowlist returned error: %v", err)
	}
	want := []string{"get_sync_status", "summarize_call_facts", "summarize_calls_by_lifecycle", "rank_transcript_backlog"}
	if !reflect.DeepEqual(allowlist, want) {
		t.Fatalf("allowlist=%v want %v", allowlist, want)
	}
}

func TestPostgresToolAllowlistNarrowsGovernanceSearchPreset(t *testing.T) {
	expanded, err := mcp.ExpandToolPreset("governance-search")
	if err != nil {
		t.Fatalf("ExpandToolPreset returned error: %v", err)
	}
	allowlist, err := postgresToolAllowlist(expanded, true, "governance-search")
	if err != nil {
		t.Fatalf("postgresToolAllowlist returned error: %v", err)
	}
	want := []string{"search_calls", "get_call", "search_transcript_segments", "rank_transcript_backlog"}
	if !reflect.DeepEqual(allowlist, want) {
		t.Fatalf("allowlist=%v want %v", allowlist, want)
	}
}

func TestPostgresToolAllowlistNarrowsGovernanceSearchExpandedAllowlist(t *testing.T) {
	expanded, err := mcp.ExpandToolPreset("governance-search")
	if err != nil {
		t.Fatalf("ExpandToolPreset returned error: %v", err)
	}
	allowlist, err := postgresToolAllowlist(expanded, true, "")
	if err != nil {
		t.Fatalf("postgresToolAllowlist returned error: %v", err)
	}
	want := []string{"search_calls", "get_call", "search_transcript_segments", "rank_transcript_backlog"}
	if !reflect.DeepEqual(allowlist, want) {
		t.Fatalf("allowlist=%v want %v", allowlist, want)
	}
}

func TestPostgresToolAllowlistRejectsUnsupportedPostgresPresets(t *testing.T) {
	for _, preset := range []string{"all-readonly", "all-tools", "all"} {
		expanded, err := mcp.ExpandToolPreset(preset)
		if err != nil {
			t.Fatalf("ExpandToolPreset(%q) returned error: %v", preset, err)
		}
		_, err = postgresToolAllowlist(expanded, true, preset)
		if err == nil || !strings.Contains(err.Error(), "not supported by the postgres backend") {
			t.Fatalf("postgresToolAllowlist(%q) error=%v, want unsupported postgres error", preset, err)
		}
	}
}

func TestPostgresToolAllowlistAcceptsAnalystPreset(t *testing.T) {
	for _, preset := range []string{"analyst", "analyst-expansion"} {
		expanded, err := mcp.ExpandToolPreset(preset)
		if err != nil {
			t.Fatalf("ExpandToolPreset(%q) returned error: %v", preset, err)
		}
		allowlist, err := postgresToolAllowlist(expanded, true, preset)
		if err != nil {
			t.Fatalf("postgresToolAllowlist(%q) returned error: %v", preset, err)
		}
		if !reflect.DeepEqual(allowlist, expanded) {
			t.Fatalf("postgresToolAllowlist(%q)=%v want expanded preset %v", preset, allowlist, expanded)
		}
	}
}

func TestPostgresToolAllowlistAcceptsRedactedAllReadonlyPreset(t *testing.T) {
	for _, preset := range []string{"redacted-all-readonly", "redacted-all", "redacted-search-lab"} {
		expanded, err := mcp.ExpandToolPreset(preset)
		if err != nil {
			t.Fatalf("ExpandToolPreset(%q) returned error: %v", preset, err)
		}
		allowlist, err := postgresToolAllowlist(expanded, true, preset)
		if err != nil {
			t.Fatalf("postgresToolAllowlist(%q) returned error: %v", preset, err)
		}
		if !reflect.DeepEqual(allowlist, mcp.PostgresRedactedAllReadonlyToolNames()) {
			t.Fatalf("postgresToolAllowlist(%q)=%v want redacted all readonly tools", preset, allowlist)
		}
		for _, want := range []string{"search_calls", "get_call", "search_crm_field_values", "summarize_scorecard_activity", "gong_query"} {
			if !containsString(allowlist, want) {
				t.Fatalf("postgres redacted all allowlist missing %q in %v", want, allowlist)
			}
		}
	}
}

func TestPostgresToolAllowlistRejectsUnreviewedRedactedAllExpansion(t *testing.T) {
	expanded, err := mcp.ExpandToolPreset("redacted-all-readonly")
	if err != nil {
		t.Fatalf("ExpandToolPreset returned error: %v", err)
	}
	expanded = append(expanded, "not_a_tool")
	_, err = postgresToolAllowlist(expanded, true, "redacted-all-readonly")
	if err == nil || !strings.Contains(err.Error(), "not been reviewed for the postgres redacted all-readonly preset") {
		t.Fatalf("postgresToolAllowlist accepted unreviewed redacted all expansion: %v", err)
	}
}

func TestRunRedactedAllReadonlyAllowsNoGovernanceExclusionsWithoutConfig(t *testing.T) {
	t.Setenv("GONG_DATABASE_URL", "postgres://reader:pw@127.0.0.1:1/gongctl?sslmode=disable")
	t.Setenv("GONGMCP_TOOL_PRESET", "redacted-all-readonly")
	t.Setenv("GONGMCP_HTTP_ADDR", "127.0.0.1:0")
	t.Setenv("GONGMCP_POSTGRES_REDACTED_SERVING_DB", "1")
	t.Setenv("GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS", "1")
	t.Setenv("GONGMCP_NO_GOVERNANCE_EXCLUSIONS", "1")
	t.Setenv("GONGMCP_AI_GOVERNANCE_CONFIG", "")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(nil, bytes.NewReader(nil), &stdout, &stderr)
	if code == 2 {
		if strings.Contains(stderr.String(), "requires --ai-governance-config") {
			t.Fatalf("stderr still requires governance config: %q", stderr.String())
		}
	}
	if !strings.Contains(stderr.String(), "no_governance_exclusions=1") {
		t.Fatalf("stderr=%q missing no_governance_exclusions contract", stderr.String())
	}
}

func TestRunRedactedAllReadonlyRejectsConfigWithNoExclusions(t *testing.T) {
	t.Setenv("GONG_DATABASE_URL", "postgres://reader:pw@127.0.0.1:1/gongctl?sslmode=disable")
	t.Setenv("GONGMCP_TOOL_PRESET", "redacted-all-readonly")
	t.Setenv("GONGMCP_HTTP_ADDR", "127.0.0.1:0")
	t.Setenv("GONGMCP_POSTGRES_REDACTED_SERVING_DB", "1")
	t.Setenv("GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS", "1")
	t.Setenv("GONGMCP_NO_GOVERNANCE_EXCLUSIONS", "1")
	t.Setenv("GONGMCP_AI_GOVERNANCE_CONFIG", "/tmp/ai-governance.yaml")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(nil, bytes.NewReader(nil), &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code=%d want 2 stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "cannot be used together") {
		t.Fatalf("stderr=%q missing conflict message", stderr.String())
	}
}

func TestRunBroadPublicRedactedAllowsNoGovernanceExclusionsWithoutConfig(t *testing.T) {
	t.Setenv("GONG_DATABASE_URL", "postgres://reader:pw@127.0.0.1:1/gongctl?sslmode=disable")
	t.Setenv("GONGMCP_TOOL_PRESET", "broad-public-redacted")
	t.Setenv("GONGMCP_HTTP_ADDR", "127.0.0.1:0")
	t.Setenv("GONGMCP_POSTGRES_REDACTED_SERVING_DB", "1")
	t.Setenv("GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS", "1")
	t.Setenv("GONGMCP_NO_GOVERNANCE_EXCLUSIONS", "1")
	t.Setenv("GONGMCP_AI_GOVERNANCE_CONFIG", "")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(nil, bytes.NewReader(nil), &stdout, &stderr)
	if code == 2 && strings.Contains(stderr.String(), "requires --ai-governance-config") {
		t.Fatalf("stderr still requires governance config: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "no_governance_exclusions=1") {
		t.Fatalf("stderr=%q missing no_governance_exclusions contract", stderr.String())
	}
}

func TestValidateNoGovernanceExclusionsPostgresPolicy(t *testing.T) {
	ctx := context.Background()
	fingerprint := "data-fingerprint"
	freshState := &postgres.GovernancePolicyState{
		ConfigSHA256:    governance.NoExclusionsConfigFingerprint(),
		DataFingerprint: fingerprint,
	}
	tests := []struct {
		name     string
		store    fakeNoGovernanceExclusionsPolicyReader
		wantErr  string
		wantCode int
	}{
		{
			name: "fresh no exclusions policy",
			store: fakeNoGovernanceExclusionsPolicyReader{
				state:       freshState,
				fingerprint: fingerprint,
			},
		},
		{
			name: "missing no exclusions policy",
			store: fakeNoGovernanceExclusionsPolicyReader{
				loadErr: errors.New("not prepared"),
			},
			wantErr:  "load Postgres no-governance-exclusions policy: failed",
			wantCode: 2,
		},
		{
			name: "fingerprint snapshot failure",
			store: fakeNoGovernanceExclusionsPolicyReader{
				state:          freshState,
				fingerprintErr: errors.New("snapshot failed"),
			},
			wantErr:  "snapshot Postgres no-governance-exclusions state: failed",
			wantCode: 1,
		},
		{
			name: "stale no exclusions policy",
			store: fakeNoGovernanceExclusionsPolicyReader{
				state:       freshState,
				fingerprint: "new-data-fingerprint",
			},
			wantErr:  "Postgres no-governance-exclusions policy is stale",
			wantCode: 2,
		},
		{
			name: "suppressed calls are incompatible",
			store: fakeNoGovernanceExclusionsPolicyReader{
				state: &postgres.GovernancePolicyState{
					ConfigSHA256:        governance.NoExclusionsConfigFingerprint(),
					DataFingerprint:     fingerprint,
					SuppressedCallCount: 1,
					SuppressedCallIDs:   []string{"call-1"},
				},
				fingerprint: fingerprint,
			},
			wantErr:  "contains suppressed calls",
			wantCode: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateNoGovernanceExclusionsPostgresPolicy(ctx, tt.store)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateNoGovernanceExclusionsPostgresPolicy returned error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error=%q missing %q", err.Error(), tt.wantErr)
			}
			validationErr, ok := err.(startupValidationError)
			if !ok {
				t.Fatalf("error type=%T want startupValidationError", err)
			}
			if validationErr.Code() != tt.wantCode {
				t.Fatalf("code=%d want %d", validationErr.Code(), tt.wantCode)
			}
		})
	}
}

func TestRunRedactedAllReadonlyRequiresRedactedServingDBGates(t *testing.T) {
	t.Setenv("GONG_DATABASE_URL", "postgres://reader:pw@127.0.0.1:1/gongctl?sslmode=disable")
	t.Setenv("GONGMCP_TOOL_PRESET", "redacted-all-readonly")
	t.Setenv("GONGMCP_HTTP_ADDR", "127.0.0.1:0")

	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{
			name: "missing redacted serving marker",
			env:  map[string]string{"GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS": "1"},
			want: "requires GONGMCP_POSTGRES_REDACTED_SERVING_DB=1",
		},
		{
			name: "missing scoped grants enforcement",
			env:  map[string]string{"GONGMCP_POSTGRES_REDACTED_SERVING_DB": "1"},
			want: "requires GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS=1",
		},
		{
			name: "missing governance config",
			env: map[string]string{
				"GONGMCP_POSTGRES_REDACTED_SERVING_DB":  "1",
				"GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS": "1",
			},
			want: "requires --ai-governance-config or GONGMCP_AI_GOVERNANCE_CONFIG",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("GONGMCP_POSTGRES_REDACTED_SERVING_DB", "")
			t.Setenv("GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS", "")
			for key, value := range tt.env {
				t.Setenv(key, value)
			}

			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := run(nil, bytes.NewReader(nil), &stdout, &stderr)
			if code != 2 {
				t.Fatalf("exit code=%d want 2 stderr=%q", code, stderr.String())
			}
			if !strings.Contains(stderr.String(), tt.want) {
				t.Fatalf("stderr=%q missing %q", stderr.String(), tt.want)
			}
		})
	}
}

func TestRunBroadPublicRedactedRequiresFailClosedGates(t *testing.T) {
	t.Setenv("GONG_DATABASE_URL", "postgres://reader:pw@127.0.0.1:1/gongctl?sslmode=disable")
	t.Setenv("GONGMCP_TOOL_PRESET", "broad-public-redacted")
	t.Setenv("GONGMCP_HTTP_ADDR", "127.0.0.1:0")

	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{
			name: "missing redacted serving marker",
			env:  map[string]string{"GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS": "1"},
			want: "requires GONGMCP_POSTGRES_REDACTED_SERVING_DB=1",
		},
		{
			name: "missing scoped grants enforcement",
			env:  map[string]string{"GONGMCP_POSTGRES_REDACTED_SERVING_DB": "1"},
			want: "requires GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS=1",
		},
		{
			name: "missing governance config",
			env: map[string]string{
				"GONGMCP_POSTGRES_REDACTED_SERVING_DB":  "1",
				"GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS": "1",
			},
			want: "broad-public-redacted requires --ai-governance-config",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("GONGMCP_POSTGRES_REDACTED_SERVING_DB", "")
			t.Setenv("GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS", "")
			for key, value := range tt.env {
				t.Setenv(key, value)
			}

			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := run(nil, bytes.NewReader(nil), &stdout, &stderr)
			if code != 2 {
				t.Fatalf("exit code=%d want 2 stderr=%q", code, stderr.String())
			}
			if !strings.Contains(stderr.String(), tt.want) {
				t.Fatalf("stderr=%q missing %q", stderr.String(), tt.want)
			}
		})
	}
}

func TestRunBroadPublicRedactedRejectsUnknownPolicySwitch(t *testing.T) {
	t.Setenv("GONG_DATABASE_URL", "postgres://reader:pw@127.0.0.1:1/gongctl?sslmode=disable")
	t.Setenv("GONGMCP_TOOL_PRESET", "broad-public-redacted")
	t.Setenv("GONGMCP_HTTP_ADDR", "127.0.0.1:0")
	t.Setenv("GONGMCP_POSTGRES_REDACTED_SERVING_DB", "1")
	t.Setenv("GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS", "1")
	t.Setenv("GONGMCP_POLICY_SWITCHES", "hide_account_names,not_a_real_switch")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(nil, bytes.NewReader(nil), &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code=%d want 2 stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "invalid policy switches") {
		t.Fatalf("stderr=%q missing invalid policy switches message", stderr.String())
	}
}

func TestPostgresAnalystPresetIncludesScorecardInventoryTools(t *testing.T) {
	for _, preset := range []string{"analyst", "analyst-expansion"} {
		expanded, err := mcp.ExpandToolPreset(preset)
		if err != nil {
			t.Fatalf("ExpandToolPreset(%q) returned error: %v", preset, err)
		}
		for _, want := range []string{"list_scorecards", "get_scorecard"} {
			if !containsString(expanded, want) {
				t.Fatalf("preset %q missing scorecard inventory tool %q in %v", preset, want, expanded)
			}
		}
		allowlist, err := postgresToolAllowlist(expanded, true, preset)
		if err != nil {
			t.Fatalf("postgresToolAllowlist(%q) returned error: %v", preset, err)
		}
		for _, want := range []string{"list_scorecards", "get_scorecard"} {
			if !containsString(allowlist, want) {
				t.Fatalf("postgres allowlist for preset %q missing scorecard inventory tool %q in %v", preset, want, allowlist)
			}
		}
		// summarize_scorecard_activity intentionally remains in
		// analyst-core/analyst-business-core; Phase 13g exposes only
		// inventory through analyst/analyst-expansion.
		if containsString(expanded, "summarize_scorecard_activity") {
			t.Fatalf("preset %q must not expose summarize_scorecard_activity through analyst/expansion: %v", preset, expanded)
		}
	}
}

func TestPostgresAnalystSmallCellMinOnlyForEnforcedScopedAnalyst(t *testing.T) {
	tests := []struct {
		name                  string
		postgresMode          bool
		presetName            string
		enforceScopedDBGrants bool
		want                  int
	}{
		{
			name:                  "enforced analyst",
			postgresMode:          true,
			presetName:            "analyst",
			enforceScopedDBGrants: true,
			want:                  3,
		},
		{
			name:                  "enforced analyst expansion",
			postgresMode:          true,
			presetName:            "analyst-expansion",
			enforceScopedDBGrants: true,
			want:                  3,
		},
		{
			name:                  "unenforced analyst",
			postgresMode:          true,
			presetName:            "analyst",
			enforceScopedDBGrants: false,
			want:                  0,
		},
		{
			name:                  "business pilot",
			postgresMode:          true,
			presetName:            "business-pilot",
			enforceScopedDBGrants: true,
			want:                  0,
		},
		{
			name:                  "sqlite analyst",
			postgresMode:          false,
			presetName:            "analyst",
			enforceScopedDBGrants: true,
			want:                  0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := postgresAnalystSmallCellMin(tt.postgresMode, tt.presetName, tt.enforceScopedDBGrants); got != tt.want {
				t.Fatalf("postgresAnalystSmallCellMin()=%d want %d", got, tt.want)
			}
		})
	}
}

func TestPostgresToolAllowlistRejectsUnreviewedAnalystExpansion(t *testing.T) {
	expanded, err := mcp.ExpandToolPreset("analyst")
	if err != nil {
		t.Fatalf("ExpandToolPreset returned error: %v", err)
	}
	expanded = append(expanded, "search_crm_field_values")
	_, err = postgresToolAllowlist(expanded, true, "analyst")
	if err == nil || !strings.Contains(err.Error(), "not been reviewed for the postgres analyst preset") {
		t.Fatalf("postgresToolAllowlist accepted unreviewed analyst expansion: %v", err)
	}
}

func TestPostgresAnalystPresetBusinessAnalysisToolsHaveGrantMappings(t *testing.T) {
	expanded, err := mcp.ExpandToolPreset("analyst")
	if err != nil {
		t.Fatalf("ExpandToolPreset returned error: %v", err)
	}
	allowlist, err := postgresToolAllowlist(expanded, true, "analyst")
	if err != nil {
		t.Fatalf("postgresToolAllowlist returned error: %v", err)
	}
	functionless := map[string]struct{}{
		"list_call_cohorts": {},
	}
	for _, name := range allowlist {
		if !containsString(mcp.BusinessAnalysisToolNames(), name) {
			continue
		}
		if _, ok := functionless[name]; ok {
			continue
		}
		if got := postgres.FunctionSignaturesForTools([]string{name}); len(got) == 0 {
			t.Fatalf("business-analysis tool %q has no Postgres required-function mapping", name)
		}
	}
}

func TestPostgresAnalystPresetBusinessAnalysisToolsRequireExplicitReview(t *testing.T) {
	expanded, err := mcp.ExpandToolPreset("analyst")
	if err != nil {
		t.Fatalf("ExpandToolPreset returned error: %v", err)
	}
	allowlist, err := postgresToolAllowlist(expanded, true, "analyst")
	if err != nil {
		t.Fatalf("postgresToolAllowlist returned error: %v", err)
	}
	for _, name := range mcp.BusinessAnalysisToolNames() {
		if !containsString(allowlist, name) {
			t.Fatalf("business-analysis tool %q is in analyst catalog but not reviewed for Postgres analyst", name)
		}
	}
}

func TestPostgresToolAllowlistRejectsBlockedPresetEvenIfToolsBecomeSupported(t *testing.T) {
	supportedSubset := []string{"get_sync_status", "search_calls", "list_crm_object_types", "list_crm_fields"}
	if _, err := postgresToolAllowlist(supportedSubset, true, "all-readonly"); err == nil {
		t.Fatal("postgresToolAllowlist accepted blocked all-readonly preset by supported subset")
	}
	allowlist, err := postgresToolAllowlist(supportedSubset, true, "")
	if err != nil {
		t.Fatalf("manual supported allowlist returned error: %v", err)
	}
	if !reflect.DeepEqual(allowlist, supportedSubset) {
		t.Fatalf("manual allowlist=%v want %v", allowlist, supportedSubset)
	}
}

func TestPostgresToolAllowlistRequiresExplicitSelectionForHTTP(t *testing.T) {
	if _, err := postgresToolAllowlist(nil, true, ""); err == nil {
		t.Fatal("postgresToolAllowlist accepted implicit HTTP tools")
	}
}

func TestRunHelpExitsZero(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"--help"}, bytes.NewReader(nil), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "-list-tool-presets") {
		t.Fatalf("help output missing list-tool-presets: %s", stderr.String())
	}
}

func TestRunToolAllowlistEnvFiltersCatalog(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gong.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open returned error: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	t.Setenv("GONGMCP_TOOL_ALLOWLIST", "get_sync_status")
	stdin := strings.NewReader(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
	}, "\n") + "\n")
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"--db", dbPath}, stdin, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code=%d stderr=%q", code, stderr.String())
	}
	got := toolNamesFromToolsListOutput(t, stdout.Bytes())
	if !containsString(got, "get_sync_status") || containsString(got, "search_calls") {
		t.Fatalf("tools/list names=%v did not reflect allowlist", got)
	}
}

func TestRunToolPresetEnvFiltersCatalog(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gong.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open returned error: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	t.Setenv("GONGMCP_TOOL_PRESET", "business-pilot")
	stdin := strings.NewReader(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
	}, "\n") + "\n")
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"--db", dbPath}, stdin, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code=%d stderr=%q", code, stderr.String())
	}
	got := toolNamesFromToolsListOutput(t, stdout.Bytes())
	for _, name := range []string{"get_sync_status", "summarize_call_facts", "summarize_calls_by_lifecycle", "rank_transcript_backlog"} {
		if !containsString(got, name) {
			t.Fatalf("tools/list names=%v missing preset tool %q", got, name)
		}
	}
	if containsString(got, "search_calls") {
		t.Fatalf("tools/list names=%v included non-business-pilot tool", got)
	}
}

func TestRunListToolPresetsDoesNotRequireDB(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"--list-tool-presets"}, bytes.NewReader(nil), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code=%d stderr=%q", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr=%q want empty", stderr.String())
	}
	var resp struct {
		Presets []struct {
			Name        string   `json:"name"`
			Aliases     []string `json:"aliases"`
			Purpose     string   `json:"purpose"`
			Tools       []string `json:"tools"`
			ToolCount   int      `json:"tool_count"`
			Recommended string   `json:"recommended_for"`
		} `json:"presets"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	seen := map[string]struct{}{}
	for _, preset := range resp.Presets {
		seen[preset.Name] = struct{}{}
		if preset.Purpose == "" || preset.Recommended == "" || preset.ToolCount != len(preset.Tools) || len(preset.Tools) == 0 {
			t.Fatalf("incomplete preset entry: %+v", preset)
		}
	}
	for _, name := range []string{"business-pilot", "operator-smoke", "analyst-core", "analyst-business-core", "analyst", "governance-search", "all-readonly"} {
		if _, ok := seen[name]; !ok {
			t.Fatalf("missing preset %q in %s", name, stdout.String())
		}
	}
	for _, preset := range resp.Presets {
		if preset.Name != "analyst" && preset.Name != "all-readonly" {
			continue
		}
		for _, name := range mcp.BusinessAnalysisToolNames() {
			if !containsString(preset.Tools, name) {
				t.Fatalf("%s preset missing business-analysis tool %q", preset.Name, name)
			}
		}
	}
}

func TestRunAnalystPresetExposesBusinessAnalysisToolsOverJSONRPC(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gong.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open returned error: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	stdin := strings.NewReader(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
	}, "\n") + "\n")
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"--stdio", "--db", dbPath, "--tool-preset", "analyst"}, stdin, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code=%d stderr=%q", code, stderr.String())
	}
	got := toolNamesFromToolsListOutput(t, stdout.Bytes())
	for _, name := range mcp.BusinessAnalysisToolNames() {
		if !containsString(got, name) {
			t.Fatalf("analyst tools/list output missing %q in %v", name, got)
		}
	}
	for _, name := range []string{"search_calls", "get_call", "list_gong_settings"} {
		if containsString(got, name) {
			t.Fatalf("analyst tools/list output included admin/config-heavy tool %q in %v", name, got)
		}
	}
}

func TestRunStdioFlagOverridesHTTPAddrEnv(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gong.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open returned error: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	t.Setenv("GONGMCP_HTTP_ADDR", "127.0.0.1:0")
	stdin := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}` + "\n")
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"--stdio", "--db", dbPath}, stdin, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code=%d stderr=%q", code, stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, `"protocolVersion"`) {
		t.Fatalf("stdout=%q did not look like stdio initialize response", got)
	}
}

func TestRunLoadsAIGovernanceConfigWithoutLoggingNames(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "gong.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open returned error: %v", err)
	}
	if _, err := store.UpsertCall(context.Background(), mustJSONForMainTest(t, map[string]any{
		"id":       "call-main-governance-blocked",
		"title":    "Blocked governance call",
		"started":  "2026-04-24T12:00:00Z",
		"duration": 1200,
		"context": []any{
			map[string]any{
				"objectType": "Account",
				"id":         "acct-main-governance-blocked",
				"name":       "Main Synthetic Restricted",
				"fields": []any{
					map[string]any{"name": "Name", "value": "Main Synthetic Restricted"},
				},
			},
		},
	})); err != nil {
		t.Fatalf("UpsertCall returned error: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	configPath := filepath.Join(dir, "ai-governance.yaml")
	if err := os.WriteFile(configPath, []byte(`
version: 1
lists:
  no_ai:
    customers:
      - name: "Main Synthetic Restricted"
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	stdin := strings.NewReader(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"search_calls","arguments":{"limit":5}}}`,
	}, "\n") + "\n")
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"--stdio", "--db", dbPath, "--tool-preset", "governance-search", "--ai-governance-config", configPath}, stdin, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code=%d stderr=%q", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "Main Synthetic Restricted") || strings.Contains(stdout.String(), "call-main-governance-blocked") {
		t.Fatalf("stdout leaked governed data: %s", stdout.String())
	}
	if strings.Contains(stderr.String(), "Main Synthetic Restricted") {
		t.Fatalf("stderr leaked governance config name: %s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "AI governance active:") || !strings.Contains(stderr.String(), "suppressed_calls=1") {
		t.Fatalf("stderr missing name-safe governance summary: %s", stderr.String())
	}
}

func TestRunRejectsCRMValueSearchInGovernanceMode(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "gong.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open returned error: %v", err)
	}
	if _, err := store.UpsertCall(context.Background(), mustJSONForMainTest(t, map[string]any{
		"id":      "call-main-governance-blocked",
		"title":   "Blocked governance call",
		"started": "2026-04-24T12:00:00Z",
		"context": []any{
			map[string]any{
				"objectType": "Account",
				"id":         "acct-main-governance-blocked",
				"name":       "Main Synthetic Restricted",
				"fields": []any{
					map[string]any{"name": "Name", "value": "Main Synthetic Restricted"},
				},
			},
		},
	})); err != nil {
		t.Fatalf("UpsertCall returned error: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	configPath := filepath.Join(dir, "ai-governance.yaml")
	if err := os.WriteFile(configPath, []byte(`
version: 1
lists:
  no_ai:
    customers:
      - name: "Main Synthetic Restricted"
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"--stdio", "--db", dbPath, "--tool-allowlist", "search_crm_field_values", "--ai-governance-config", configPath}, bytes.NewReader(nil), &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code=%d want 2", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout=%q want empty", stdout.String())
	}
	if got := stderr.String(); !strings.Contains(got, `tool "search_crm_field_values" is not supported while AI governance filtering is active`) {
		t.Fatalf("stderr=%q missing governance allowlist rejection", got)
	}
}

func TestResolveToolAllowlistPresets(t *testing.T) {
	tests := []struct {
		name string
		in   toolSelection
		want []string
	}{
		{
			name: "business preset",
			in:   toolSelection{PresetEnv: "business-pilot"},
			want: []string{"get_sync_status", "summarize_call_facts", "summarize_calls_by_lifecycle", "rank_transcript_backlog"},
		},
		{
			name: "legacy strict business alias",
			in:   toolSelection{PresetEnv: "strict-business-pilot"},
			want: []string{"get_sync_status", "summarize_call_facts", "summarize_calls_by_lifecycle", "rank_transcript_backlog"},
		},
		{
			name: "operator smoke preset",
			in:   toolSelection{PresetEnv: "operator-smoke"},
			want: []string{"get_sync_status", "search_calls", "search_transcript_segments", "get_call", "rank_transcript_backlog"},
		},
		{
			name: "analyst core preset",
			in:   toolSelection{PresetEnv: "analyst-core"},
			want: []string{"get_sync_status", "search_calls", "get_call", "list_crm_object_types", "list_crm_fields", "list_crm_integrations", "list_cached_crm_schema_objects", "list_cached_crm_schema_fields", "list_gong_settings", "list_scorecards", "get_scorecard", "summarize_scorecard_activity", "get_business_profile", "list_business_concepts", "list_lifecycle_buckets", "summarize_calls_by_lifecycle", "search_calls_by_lifecycle", "prioritize_transcripts_by_lifecycle", "summarize_call_facts", "rank_transcript_backlog", "search_transcript_segments"},
		},
		{
			name: "all readonly expands to catalog",
			in:   toolSelection{PresetEnv: "all-readonly"},
			want: mcp.ToolCatalogNames(),
		},
		{
			name: "governance search preset",
			in:   toolSelection{PresetEnv: "governance-search"},
			want: []string{"search_calls", "get_call", "search_transcripts_by_crm_context", "search_calls_by_lifecycle", "prioritize_transcripts_by_lifecycle", "rank_transcript_backlog", "search_transcript_segments", "search_transcripts_by_call_facts", "search_transcript_quotes_with_attribution", "missing_transcripts"},
		},
		{
			name: "redacted all readonly preset",
			in:   toolSelection{PresetEnv: "redacted-all-readonly"},
			want: mcp.PostgresRedactedAllReadonlyToolNames(),
		},
		{
			name: "flag preset overrides env allowlist",
			in:   toolSelection{PresetFlag: "business-pilot", PresetFlagSet: true, AllowlistEnv: "search_calls"},
			want: []string{"get_sync_status", "summarize_call_facts", "summarize_calls_by_lifecycle", "rank_transcript_backlog"},
		},
		{
			name: "flag allowlist overrides env preset",
			in:   toolSelection{AllowlistFlag: "get_sync_status", AllowlistFlagSet: true, PresetEnv: "all-readonly"},
			want: []string{"get_sync_status"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveToolAllowlist(tt.in)
			if err != nil {
				t.Fatalf("resolveToolAllowlist returned error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("resolveToolAllowlist=%v want %v", got, tt.want)
			}
		})
	}
}

func TestPresetGovernanceCompatibilityAndAnalystScope(t *testing.T) {
	governancePreset, err := mcp.ExpandToolPreset("governance-search")
	if err != nil {
		t.Fatalf("expandToolPreset returned error: %v", err)
	}
	if err := mcp.ValidateGovernanceAllowlist(governancePreset); err != nil {
		t.Fatalf("governance-search preset rejected by governance validator: %v", err)
	}

	analyst, err := mcp.ExpandToolPreset("analyst")
	if err != nil {
		t.Fatalf("expandToolPreset returned error: %v", err)
	}
	for _, denied := range []string{"search_crm_field_values", "list_gong_settings", "get_call", "search_calls", "search_calls_by_lifecycle", "missing_transcripts"} {
		if containsString(analyst, denied) {
			t.Fatalf("analyst preset includes admin/config-heavy tool %q", denied)
		}
	}
	if !containsString(analyst, "search_transcript_quotes_with_attribution") {
		t.Fatalf("analyst preset missing bounded evidence tool")
	}
	for _, name := range mcp.BusinessAnalysisToolNames() {
		if !containsString(analyst, name) {
			t.Fatalf("analyst preset missing business-analysis tool %q", name)
		}
	}
	allReadonly, err := mcp.ExpandToolPreset("all-readonly")
	if err != nil {
		t.Fatalf("expandToolPreset returned error: %v", err)
	}
	for _, name := range mcp.BusinessAnalysisToolNames() {
		if !containsString(allReadonly, name) {
			t.Fatalf("all-readonly preset missing business-analysis tool %q", name)
		}
	}
}

func TestResolveToolAllowlistRejectsAmbiguousSelection(t *testing.T) {
	tests := []struct {
		name string
		in   toolSelection
	}{
		{
			name: "both flags",
			in:   toolSelection{AllowlistFlag: "get_sync_status", AllowlistFlagSet: true, PresetFlag: "business-pilot", PresetFlagSet: true},
		},
		{
			name: "both env vars",
			in:   toolSelection{AllowlistEnv: "get_sync_status", PresetEnv: "business-pilot"},
		},
		{
			name: "unknown preset",
			in:   toolSelection{PresetEnv: "not-a-preset"},
		},
		{
			name: "empty explicit flag",
			in:   toolSelection{PresetFlagSet: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := resolveToolAllowlist(tt.in); err == nil {
				t.Fatal("resolveToolAllowlist returned nil error")
			}
		})
	}
}

func TestResolveLimitPolicyFlagOverridesEnvAndClamps(t *testing.T) {
	policy, err := resolveLimitPolicy(limitSelection{
		SearchResults:    250,
		SearchResultsSet: true,
		Getenv: func(key string) string {
			if key == "GONGMCP_MAX_SEARCH_RESULTS" {
				return "125"
			}
			if key == "GONGMCP_MAX_MISSING_TRANSCRIPTS" {
				return "999999"
			}
			return ""
		},
	})
	if err != nil {
		t.Fatalf("resolveLimitPolicy returned error: %v", err)
	}
	if policy.SearchResults != 250 {
		t.Fatalf("SearchResults=%d want flag override 250", policy.SearchResults)
	}
	if policy.MissingTranscripts != 10000 {
		t.Fatalf("MissingTranscripts=%d want hard cap 10000", policy.MissingTranscripts)
	}
}

func TestResolveLimitPolicyRejectsInvalidValues(t *testing.T) {
	if _, err := resolveLimitPolicy(limitSelection{
		SearchResults:    -1,
		SearchResultsSet: true,
		Getenv:           func(string) string { return "" },
	}); err == nil {
		t.Fatal("resolveLimitPolicy allowed negative flag value")
	}
	if _, err := resolveLimitPolicy(limitSelection{
		Getenv: func(key string) string {
			if key == "GONGMCP_MAX_SEARCH_RESULTS" {
				return "nope"
			}
			return ""
		},
	}); err == nil {
		t.Fatal("resolveLimitPolicy allowed invalid env value")
	}
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func containsColumnSelectGrant(values []postgres.ColumnSelectGrant, needle postgres.ColumnSelectGrant) bool {
	for _, value := range values {
		if value.Table == needle.Table && value.Column == needle.Column {
			return true
		}
	}
	return false
}

func toolNamesFromToolsListOutput(t *testing.T, raw []byte) []string {
	t.Helper()

	decoder := json.NewDecoder(bytes.NewReader(raw))
	for {
		var envelope struct {
			Result struct {
				Tools []struct {
					Name string `json:"name"`
				} `json:"tools"`
			} `json:"result"`
		}
		if err := decoder.Decode(&envelope); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("decode JSON-RPC output: %v\n%s", err, string(raw))
		}
		if len(envelope.Result.Tools) == 0 {
			continue
		}
		names := make([]string, 0, len(envelope.Result.Tools))
		for _, tool := range envelope.Result.Tools {
			names = append(names, tool.Name)
		}
		return names
	}
	t.Fatalf("tools/list response not found in JSON-RPC output:\n%s", string(raw))
	return nil
}

func TestRunToolAllowlistFlagOverridesEnvAndRejectsUnknownTools(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gong.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open returned error: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	t.Setenv("GONGMCP_TOOL_ALLOWLIST", "get_sync_status")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"--db", dbPath, "--tool-allowlist", "does_not_exist"}, bytes.NewReader(nil), &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code=%d want 2", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout=%q want empty", stdout.String())
	}
	if got := stderr.String(); !strings.Contains(got, `unknown tool "does_not_exist"`) {
		t.Fatalf("stderr=%q missing unknown-tool error", got)
	}
}

func TestRunToolAllowlistFlagPrecedenceOverEnv(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gong.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open returned error: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	t.Setenv("GONGMCP_TOOL_ALLOWLIST", "search_calls")
	stdin := strings.NewReader(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
	}, "\n") + "\n")
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"--db", dbPath, "--tool-allowlist", "get_sync_status"}, stdin, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code=%d stderr=%q", code, stderr.String())
	}
	got := toolNamesFromToolsListOutput(t, stdout.Bytes())
	if !containsString(got, "get_sync_status") || containsString(got, "search_calls") {
		t.Fatalf("tools/list names=%v did not prefer flag allowlist", got)
	}
}

const (
	testCurrentBearerToken  = "current-bearer-token-0123456789abcdef"
	testFlagBearerToken     = "flag-bearer-token-0123456789abcdef"
	testEnvBearerToken      = "env-bearer-token-0123456789abcdef"
	testPreviousBearerToken = "previous-bearer-token-0123456789abcdef"
)

func TestResolveHTTPConfigRequiresBearerByDefaultAndNoAuthDevLocalhost(t *testing.T) {
	getenv := func(string) string { return "" }

	if _, err := resolveHTTPConfig("0.0.0.0:8080", false, "none", "", "", "", false, false, "", []string{"get_sync_status"}, getenv); err == nil {
		t.Fatal("resolveHTTPConfig allowed auth-mode=none without dev localhost override")
	}
	if _, err := resolveHTTPConfig("0.0.0.0:8080", false, "none", "", "", "", true, true, "https://app.example.com", []string{"get_sync_status"}, getenv); err == nil {
		t.Fatal("resolveHTTPConfig allowed non-local no-auth HTTP")
	}
	if _, err := resolveHTTPConfig("0.0.0.0:8080", false, "bearer", "", "", "", false, false, "https://app.example.com", []string{"get_sync_status"}, getenv); err == nil {
		t.Fatal("resolveHTTPConfig allowed non-local bind without explicit override")
	}

	cfg, err := resolveHTTPConfig("0.0.0.0:8080", false, "bearer", testCurrentBearerToken, "", "", true, false, "https://app.example.com", nil, getenv)
	if err == nil {
		t.Fatal("resolveHTTPConfig allowed non-local bind without tool allowlist")
	}

	if _, err := resolveHTTPConfig("0.0.0.0:8080", false, "bearer", testCurrentBearerToken, "", "", true, false, "", []string{"get_sync_status"}, getenv); err == nil {
		t.Fatal("resolveHTTPConfig allowed non-local HTTP without allowed origins")
	}

	cfg, err = resolveHTTPConfig("0.0.0.0:8080", false, "bearer", testCurrentBearerToken, "", "", true, false, "https://app.example.com", []string{"get_sync_status"}, getenv)
	if err != nil {
		t.Fatalf("resolveHTTPConfig returned error with override and allowlist: %v", err)
	}
	if !cfg.Enabled || cfg.AuthMode != "bearer" || !cfg.OpenNetworkWarning {
		t.Fatalf("unexpected config: %+v", cfg)
	}

	if _, err := resolveHTTPConfig("127.0.0.1:0", false, "none", "", "", "", false, true, "", nil, getenv); err == nil {
		t.Fatal("resolveHTTPConfig allowed HTTP without tool allowlist")
	}
	if _, err := resolveHTTPConfig("127.0.0.1:0", false, "", "", "", "", false, false, "", []string{"get_sync_status"}, getenv); err == nil {
		t.Fatal("resolveHTTPConfig allowed default bearer mode without token")
	}

	local, err := resolveHTTPConfig("127.0.0.1:0", false, "none", "", "", "", false, true, "", []string{"get_sync_status"}, getenv)
	if err != nil {
		t.Fatalf("resolveHTTPConfig rejected explicit local dev no-auth with allowlist: %v", err)
	}
	if local.AuthMode != "none" || local.OpenNetworkWarning {
		t.Fatalf("local config should not warn: %+v", local)
	}
}

func TestResolveHTTPConfigRejectsUnknownAuthMode(t *testing.T) {
	if _, err := resolveHTTPConfig("127.0.0.1:0", false, "basic", "", "", "", false, false, "", []string{"get_sync_status"}, func(string) string { return "" }); err == nil {
		t.Fatal("resolveHTTPConfig allowed unknown auth mode")
	}
}

func TestResolveHTTPConfigCanForceStdioWithHTTPAddrEnv(t *testing.T) {
	cfg, err := resolveHTTPConfig("", true, "", "", "", "", false, false, "", nil, func(key string) string {
		if key == "GONGMCP_HTTP_ADDR" {
			return "127.0.0.1:0"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("resolveHTTPConfig returned error: %v", err)
	}
	if cfg.Enabled {
		t.Fatalf("force stdio should disable HTTP config: %+v", cfg)
	}

	if _, err := resolveHTTPConfig("127.0.0.1:0", true, "", "", "", "", false, false, "", nil, func(string) string { return "" }); err == nil {
		t.Fatal("resolveHTTPConfig allowed --stdio with --http")
	}
}

func TestResolveHTTPToolCallTimeoutDefaultsEnvAndFlag(t *testing.T) {
	defaultTimeout, err := resolveHTTPToolCallTimeout("", false, func(string) string { return "" })
	if err != nil {
		t.Fatalf("default timeout returned error: %v", err)
	}
	if defaultTimeout != mcp.DefaultHTTPToolCallTimeout {
		t.Fatalf("default timeout=%s want %s", defaultTimeout, mcp.DefaultHTTPToolCallTimeout)
	}

	envTimeout, err := resolveHTTPToolCallTimeout("", false, func(key string) string {
		if key == "GONGMCP_HTTP_TOOL_TIMEOUT" {
			return "2m"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("env timeout returned error: %v", err)
	}
	if envTimeout != 2*time.Minute {
		t.Fatalf("env timeout=%s want 2m", envTimeout)
	}

	flagTimeout, err := resolveHTTPToolCallTimeout("75s", true, func(key string) string {
		if key == "GONGMCP_HTTP_TOOL_TIMEOUT" {
			return "2m"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("flag timeout returned error: %v", err)
	}
	if flagTimeout != 75*time.Second {
		t.Fatalf("flag timeout=%s want 75s", flagTimeout)
	}

	for _, value := range []string{"0", "0s", "-1s", "not-a-duration"} {
		if _, err := resolveHTTPToolCallTimeout(value, true, func(string) string { return "" }); err == nil {
			t.Fatalf("resolveHTTPToolCallTimeout accepted %q", value)
		}
	}
}

func TestResolveHTTPConfigBearerTokenSources(t *testing.T) {
	tokenPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenPath, []byte(" "+testCurrentBearerToken+" \n"), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}
	previousTokenPath := filepath.Join(t.TempDir(), "previous-token")
	if err := os.WriteFile(previousTokenPath, []byte(" "+testPreviousBearerToken+" \n"), 0o600); err != nil {
		t.Fatalf("write previous token file: %v", err)
	}

	getenv := func(key string) string {
		switch key {
		case "GONGMCP_BEARER_TOKEN_FILE":
			return tokenPath
		default:
			return ""
		}
	}
	allowlist := []string{"get_sync_status"}
	cfg, err := resolveHTTPConfig("127.0.0.1:0", false, "", "", "", "", false, false, "", allowlist, getenv)
	if err != nil {
		t.Fatalf("resolveHTTPConfig returned error: %v", err)
	}
	if cfg.AuthMode != "bearer" || len(cfg.BearerTokens) != 1 || cfg.BearerTokens[0] != testCurrentBearerToken {
		t.Fatalf("unexpected bearer config: %+v", cfg)
	}

	envFile := getenv
	cfg, err = resolveHTTPConfig("127.0.0.1:0", false, "", testFlagBearerToken, "", "", false, false, "", allowlist, envFile)
	if err != nil {
		t.Fatalf("resolveHTTPConfig did not let bearer token flag override env file: %v", err)
	}
	if len(cfg.BearerTokens) != 1 || cfg.BearerTokens[0] != testFlagBearerToken {
		t.Fatalf("bearer tokens=%v want flag token", cfg.BearerTokens)
	}

	envToken := func(key string) string {
		if key == "GONGMCP_BEARER_TOKEN" {
			return testEnvBearerToken
		}
		return ""
	}
	cfg, err = resolveHTTPConfig("127.0.0.1:0", false, "", "", tokenPath, "", false, false, "", allowlist, envToken)
	if err != nil {
		t.Fatalf("resolveHTTPConfig did not let bearer token file flag override env token: %v", err)
	}
	if len(cfg.BearerTokens) != 1 || cfg.BearerTokens[0] != testCurrentBearerToken {
		t.Fatalf("bearer tokens=%v want file token", cfg.BearerTokens)
	}

	if _, err := resolveHTTPConfig("127.0.0.1:0", false, "bearer", testFlagBearerToken, tokenPath, "", false, false, "", allowlist, func(string) string { return "" }); err == nil {
		t.Fatal("resolveHTTPConfig allowed both raw token and token file")
	}
	if _, err := resolveHTTPConfig("127.0.0.1:0", false, "bearer", "", "", "", false, false, "", allowlist, func(string) string { return "" }); err == nil {
		t.Fatal("resolveHTTPConfig allowed bearer mode without token")
	}
	if _, err := resolveHTTPConfig("127.0.0.1:0", false, "bearer", "", filepath.Join(t.TempDir(), "missing-token"), "", false, false, "", allowlist, func(string) string { return "" }); err == nil {
		t.Fatal("resolveHTTPConfig allowed unreadable token file")
	}
	if _, err := resolveHTTPConfig("127.0.0.1:0", false, "", "", "", "", false, false, "", allowlist, func(key string) string {
		switch key {
		case "GONGMCP_BEARER_TOKEN":
			return testEnvBearerToken
		case "GONGMCP_BEARER_TOKEN_FILE":
			return tokenPath
		default:
			return ""
		}
	}); err == nil {
		t.Fatal("resolveHTTPConfig allowed both env raw token and env token file")
	}

	cfg, err = resolveHTTPConfig("127.0.0.1:0", false, "bearer", "", tokenPath, previousTokenPath, false, false, "", allowlist, func(string) string { return "" })
	if err != nil {
		t.Fatalf("resolveHTTPConfig rejected previous token file: %v", err)
	}
	if !reflect.DeepEqual(cfg.BearerTokens, []string{testCurrentBearerToken, testPreviousBearerToken}) {
		t.Fatalf("bearer tokens=%v want current and previous", cfg.BearerTokens)
	}
}

func TestResolveHTTPConfigRejectsWeakBearerTokens(t *testing.T) {
	allowlist := []string{"get_sync_status"}
	if _, err := resolveHTTPConfig("127.0.0.1:0", false, "bearer", "short-token", "", "", false, false, "", allowlist, func(string) string { return "" }); err == nil {
		t.Fatal("resolveHTTPConfig allowed weak raw bearer token")
	}

	tokenPath := filepath.Join(t.TempDir(), "weak-token")
	if err := os.WriteFile(tokenPath, []byte("short-token\n"), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}
	if _, err := resolveHTTPConfig("127.0.0.1:0", false, "bearer", "", tokenPath, "", false, false, "", allowlist, func(string) string { return "" }); err == nil {
		t.Fatal("resolveHTTPConfig allowed weak bearer token file")
	}

	if _, err := resolveHTTPConfig("127.0.0.1:0", false, "bearer", "token-with-space 0123456789abcdef0123456789abcdef", "", "", false, false, "", allowlist, func(string) string { return "" }); err == nil {
		t.Fatal("resolveHTTPConfig allowed bearer token with whitespace")
	}
}

func TestAuthMiddlewareRequiresBearerToken(t *testing.T) {
	handler := authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), httpConfig{
		Enabled:      true,
		Addr:         "127.0.0.1:0",
		AuthMode:     "bearer",
		BearerTokens: []string{"expected-token", "previous-token"},
	})

	for _, tc := range []struct {
		name   string
		header string
		want   int
	}{
		{name: "missing", want: http.StatusUnauthorized},
		{name: "wrong", header: "Bearer wrong-token", want: http.StatusUnauthorized},
		{name: "ok", header: "Bearer expected-token", want: http.StatusNoContent},
		{name: "previous", header: "Bearer previous-token", want: http.StatusNoContent},
		{name: "lowercase-scheme", header: "bearer expected-token", want: http.StatusNoContent},
		{name: "extra-fields", header: "Bearer expected-token extra", want: http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)
			if recorder.Code != tc.want {
				t.Fatalf("status=%d want %d body=%q", recorder.Code, tc.want, recorder.Body.String())
			}
		})
	}
}

func TestBearerHTTPStackProtectsMCPRequests(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gong.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open returned error: %v", err)
	}
	defer store.Close()

	server := mcp.NewServer(store, "gongmcp", "test")
	handler := authMiddleware(server, httpConfig{
		Enabled:      true,
		Addr:         "127.0.0.1:0",
		AuthMode:     "bearer",
		BearerTokens: []string{"expected-token", "previous-token"},
	})
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`

	unauthorized := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	unauthorizedRecorder := httptest.NewRecorder()
	handler.ServeHTTP(unauthorizedRecorder, unauthorized)
	if unauthorizedRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d want %d", unauthorizedRecorder.Code, http.StatusUnauthorized)
	}

	authorized := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	authorized.Header.Set("Authorization", "Bearer expected-token")
	authorizedRecorder := httptest.NewRecorder()
	handler.ServeHTTP(authorizedRecorder, authorized)
	if authorizedRecorder.Code != http.StatusOK {
		t.Fatalf("authorized status=%d want %d body=%q", authorizedRecorder.Code, http.StatusOK, authorizedRecorder.Body.String())
	}
	if !strings.Contains(authorizedRecorder.Body.String(), `"protocolVersion"`) {
		t.Fatalf("authorized response=%q missing initialize result", authorizedRecorder.Body.String())
	}
}

func TestHTTPHandlerExposesUnauthenticatedHealthzOnly(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gong.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open returned error: %v", err)
	}
	defer store.Close()

	server := mcp.NewServer(store, "gongmcp", "test")
	var accessLog bytes.Buffer
	handler := httpHandler(server, httpConfig{
		Enabled:      true,
		Addr:         "127.0.0.1:0",
		AuthMode:     "bearer",
		BearerTokens: []string{"expected-token", "previous-token"},
	}, &accessLog)

	health := httptest.NewRecorder()
	handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("health status=%d body=%q", health.Code, health.Body.String())
	}
	if !json.Valid(health.Body.Bytes()) {
		t.Fatalf("health body is not valid JSON: %q", health.Body.String())
	}
	var healthPayload struct {
		Status    string                `json:"status"`
		Service   string                `json:"service"`
		Version   string                `json:"version"`
		MCPServer mcp.PublicRuntimeInfo `json:"mcp_server"`
	}
	if err := json.Unmarshal(health.Body.Bytes(), &healthPayload); err != nil {
		t.Fatalf("unmarshal health JSON: %v", err)
	}
	if healthPayload.Status != "ok" || healthPayload.Service != "gongmcp" || healthPayload.Version == "" {
		t.Fatalf("unexpected health payload: %+v", healthPayload)
	}
	if healthPayload.MCPServer.Name != "gongmcp" || healthPayload.MCPServer.Version == "" {
		t.Fatalf("health payload missing MCP server identity: %+v", healthPayload)
	}

	mcpRecorder := httptest.NewRecorder()
	handler.ServeHTTP(mcpRecorder, httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{}`)))
	if mcpRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("mcp status=%d want unauthorized", mcpRecorder.Code)
	}
	if !strings.Contains(accessLog.String(), `auth_mode="bearer"`) {
		t.Fatalf("access log missing auth mode: %s", accessLog.String())
	}
	if !strings.Contains(accessLog.String(), `decision="auth_missing"`) {
		t.Fatalf("access log missing auth rejection decision: %s", accessLog.String())
	}
	if strings.Contains(accessLog.String(), `{}`) {
		t.Fatalf("access log leaked request payload: %s", accessLog.String())
	}
}

func TestHTTPHandlerValidatesOrigin(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gong.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open returned error: %v", err)
	}
	defer store.Close()

	server := mcp.NewServer(store, "gongmcp", "test")
	var accessLog bytes.Buffer
	handler := httpHandler(server, httpConfig{
		Enabled:      true,
		Addr:         "0.0.0.0:8080",
		AuthMode:     "bearer",
		BearerTokens: []string{"expected-token", "previous-token"},
		AllowedOrigins: map[string]struct{}{
			"https://chatgpt.example.com": {},
		},
	}, &accessLog)
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`

	preflight := httptest.NewRequest(http.MethodOptions, "/mcp", nil)
	preflight.Header.Set("Origin", "https://chatgpt.example.com")
	preflight.Header.Set("Access-Control-Request-Method", "POST")
	preflight.Header.Set("Access-Control-Request-Headers", "authorization,content-type")
	preflightRecorder := httptest.NewRecorder()
	handler.ServeHTTP(preflightRecorder, preflight)
	if preflightRecorder.Code != http.StatusNoContent {
		t.Fatalf("preflight status=%d body=%q", preflightRecorder.Code, preflightRecorder.Body.String())
	}
	if got := preflightRecorder.Header().Get("Access-Control-Allow-Origin"); got != "https://chatgpt.example.com" {
		t.Fatalf("preflight allow origin=%q", got)
	}
	if got := preflightRecorder.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(got, "Authorization") || !strings.Contains(got, "Content-Type") {
		t.Fatalf("preflight allow headers=%q", got)
	}

	badPreflight := httptest.NewRequest(http.MethodOptions, "/mcp", nil)
	badPreflight.Header.Set("Origin", "https://chatgpt.example.com")
	badPreflight.Header.Set("Access-Control-Request-Method", "GET")
	badPreflightRecorder := httptest.NewRecorder()
	handler.ServeHTTP(badPreflightRecorder, badPreflight)
	if badPreflightRecorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("bad preflight status=%d body=%q", badPreflightRecorder.Code, badPreflightRecorder.Body.String())
	}

	allowed := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	allowed.Header.Set("Origin", "https://chatgpt.example.com")
	allowed.Header.Set("Authorization", "Bearer expected-token")
	allowedRecorder := httptest.NewRecorder()
	handler.ServeHTTP(allowedRecorder, allowed)
	if allowedRecorder.Code != http.StatusOK {
		t.Fatalf("allowed status=%d body=%q", allowedRecorder.Code, allowedRecorder.Body.String())
	}

	previous := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	previous.Header.Set("Origin", "https://chatgpt.example.com")
	previous.Header.Set("Authorization", "Bearer previous-token")
	previousRecorder := httptest.NewRecorder()
	handler.ServeHTTP(previousRecorder, previous)
	if previousRecorder.Code != http.StatusOK {
		t.Fatalf("previous status=%d body=%q", previousRecorder.Code, previousRecorder.Body.String())
	}

	blocked := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	blocked.Header.Set("Origin", "https://attacker.example.com")
	blocked.Header.Set("Authorization", "Bearer expected-token")
	blockedRecorder := httptest.NewRecorder()
	handler.ServeHTTP(blockedRecorder, blocked)
	if blockedRecorder.Code != http.StatusForbidden {
		t.Fatalf("blocked status=%d want forbidden body=%q", blockedRecorder.Code, blockedRecorder.Body.String())
	}
	logOutput := accessLog.String()
	if !strings.Contains(logOutput, "status=204") || !strings.Contains(logOutput, "status=200") || !strings.Contains(logOutput, "status=403") {
		t.Fatalf("access log did not record preflight, success, and origin rejection: %s", logOutput)
	}
	for _, slot := range []string{`token_slot="current"`, `token_slot="previous"`} {
		if !strings.Contains(logOutput, slot) {
			t.Fatalf("access log missing %s: %s", slot, logOutput)
		}
	}
	for _, decision := range []string{`decision="cors_preflight_ok"`, `decision="cors_preflight_denied"`, `decision="origin_denied"`} {
		if !strings.Contains(logOutput, decision) {
			t.Fatalf("access log missing %s: %s", decision, logOutput)
		}
	}
}

func TestHTTPHandlerAllowsLoopbackOriginsForLocalBind(t *testing.T) {
	handler := httpHandler(mcp.NewServer(nil, "gongmcp", "test"), httpConfig{
		Enabled:   true,
		Addr:      "127.0.0.1:8080",
		AuthMode:  "none",
		LocalBind: true,
	}, io.Discard)

	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	req.Header.Set("Origin", "http://localhost:3000")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("loopback origin status=%d body=%q", recorder.Code, recorder.Body.String())
	}
}

func mustJSONForMainTest(t *testing.T, value any) []byte {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal JSON: %v", err)
	}
	return payload
}
