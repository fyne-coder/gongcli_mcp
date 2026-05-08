package mcp

import (
	"bufio"
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	profilepkg "github.com/fyne-coder/gongcli_mcp/internal/profile"
	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
)

func TestToolsListOnlyExposesExpectedReadOnlyTools(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	server := NewServerWithOptions(store, "gongmcp", "test", WithTranscriptEvidenceProvenance(TranscriptEvidenceRaw))
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "init",
		Method:  "initialize",
		Params:  mustJSON(t, map[string]any{}),
	})+requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "tools",
		Method:  "tools/list",
	}))

	if len(responses) != 2 {
		t.Fatalf("response count=%d want 2", len(responses))
	}

	var listed struct {
		Result toolsListResult `json:"result"`
	}
	if err := json.Unmarshal(responses[1], &listed); err != nil {
		t.Fatalf("unmarshal tools/list response: %v", err)
	}

	names := make([]string, 0, len(listed.Result.Tools))
	for _, tool := range listed.Result.Tools {
		names = append(names, tool.Name)
	}

	want := ToolCatalogNames()
	if len(names) != len(want) {
		t.Fatalf("tool count=%d want %d (%v)", len(names), len(want), names)
	}
	for idx, name := range want {
		if names[idx] != name {
			t.Fatalf("tool[%d]=%q want %q", idx, names[idx], name)
		}
	}
	for _, blocked := range []string{"api_raw", "raw_api", "sql_query"} {
		for _, name := range names {
			if name == blocked {
				t.Fatalf("unexpected tool %q exposed", blocked)
			}
		}
	}
}

func TestToolsListSchemasUseObjectPropertiesRecord(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	server := NewServerWithOptions(store, "gongmcp", "test", WithToolAllowlist(FacadeToolNames()))
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "init",
		Method:  "initialize",
		Params:  mustJSON(t, map[string]any{}),
	})+requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "tools",
		Method:  "tools/list",
	}))

	if len(responses) != 2 {
		t.Fatalf("response count=%d want 2", len(responses))
	}

	var listed struct {
		Result toolsListResult `json:"result"`
	}
	if err := json.Unmarshal(responses[1], &listed); err != nil {
		t.Fatalf("unmarshal tools/list response: %v", err)
	}

	for _, item := range listed.Result.Tools {
		props, ok := item.InputSchema["properties"].(map[string]any)
		if !ok {
			t.Fatalf("%s inputSchema.properties=%T want object: %+v", item.Name, item.InputSchema["properties"], item.InputSchema)
		}
		if props == nil {
			t.Fatalf("%s inputSchema.properties is nil", item.Name)
		}
	}
}

func TestToolsListOnlyReturnsAllowlistedTools(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	server := NewServerWithOptions(store, "gongmcp", "test", WithToolAllowlist([]string{
		"get_sync_status",
		"list_scorecards",
		"search_transcript_segments",
	}))
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "init",
		Method:  "initialize",
		Params:  mustJSON(t, map[string]any{}),
	})+requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "tools",
		Method:  "tools/list",
	}))

	var listed struct {
		Result toolsListResult `json:"result"`
	}
	if err := json.Unmarshal(responses[1], &listed); err != nil {
		t.Fatalf("unmarshal tools/list response: %v", err)
	}

	got := make([]string, 0, len(listed.Result.Tools))
	for _, item := range listed.Result.Tools {
		got = append(got, item.Name)
	}
	want := []string{"get_sync_status", "list_scorecards", "search_transcript_segments"}
	if len(got) != len(want) {
		t.Fatalf("tool count=%d want %d (%v)", len(got), len(want), got)
	}
	for idx, name := range want {
		if got[idx] != name {
			t.Fatalf("tool[%d]=%q want %q", idx, got[idx], name)
		}
	}
}

func TestToolSchemasReflectConfiguredLimitPolicy(t *testing.T) {
	t.Parallel()

	policy, err := DefaultLimitPolicy().WithOverride("search_results", 250)
	if err != nil {
		t.Fatalf("WithOverride returned error: %v", err)
	}
	policy, err = policy.WithOverride("business_analysis_rows", 300)
	if err != nil {
		t.Fatalf("WithOverride returned error: %v", err)
	}

	segmentTool, ok := FindToolWithLimitPolicy("search_transcript_segments", policy)
	if !ok {
		t.Fatal("search_transcript_segments not found")
	}
	if got := schemaLimitMaximum(t, segmentTool.InputSchema, "limit"); got != 250 {
		t.Fatalf("search_transcript_segments maximum=%d want 250", got)
	}

	cohortTool, ok := FindToolWithLimitPolicy("build_call_cohort", policy)
	if !ok {
		t.Fatal("build_call_cohort not found")
	}
	if got := schemaLimitMaximum(t, cohortTool.InputSchema, "limit"); got != 300 {
		t.Fatalf("build_call_cohort maximum=%d want 300", got)
	}
	filterSchema := mapField(t, mapField(t, cohortTool.InputSchema, "properties"), "filter")
	if got := schemaLimitMaximum(t, filterSchema, "limit"); got != 300 {
		t.Fatalf("build_call_cohort filter maximum=%d want 300", got)
	}
}

func TestLimitPolicyFromEnvClampsHardCeiling(t *testing.T) {
	t.Parallel()

	policy, err := LimitPolicyFromEnv(func(key string) string {
		if key == "GONGMCP_MAX_SEARCH_RESULTS" {
			return "999999"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("LimitPolicyFromEnv returned error: %v", err)
	}
	if policy.SearchResults != hardMaxSearchResults {
		t.Fatalf("SearchResults=%d want hard max %d", policy.SearchResults, hardMaxSearchResults)
	}
}

func TestLimitPolicyAppliesConfiguredCapToOmittedLimit(t *testing.T) {
	t.Parallel()

	policy, err := DefaultLimitPolicy().WithOverride("search_results", 5)
	if err != nil {
		t.Fatalf("WithOverride returned error: %v", err)
	}
	if got := policy.SearchLimit(0); got != 5 {
		t.Fatalf("SearchLimit(0)=%d want configured cap 5", got)
	}
	policy, err = DefaultLimitPolicy().WithOverride("missing_transcripts", 10)
	if err != nil {
		t.Fatalf("WithOverride returned error: %v", err)
	}
	if got := policy.MissingTranscriptLimit(0); got != 10 {
		t.Fatalf("MissingTranscriptLimit(0)=%d want configured cap 10", got)
	}
	policy, err = DefaultLimitPolicy().WithOverride("business_analysis_rows", 10)
	if err != nil {
		t.Fatalf("WithOverride returned error: %v", err)
	}
	if got := policy.BusinessAnalysisLimit(0); got != 10 {
		t.Fatalf("BusinessAnalysisLimit(0)=%d want configured cap 10", got)
	}
}

func TestCapRefinementsOnlySuggestAcceptedFilters(t *testing.T) {
	t.Parallel()

	fieldRefs := crmFieldValueRefinements(searchCRMFieldValuesArgs{
		ObjectType: "Opportunity",
		FieldName:  "StageName",
		ValueQuery: "Discovery",
	})
	for _, disallowed := range []string{"from_date", "lifecycle_bucket", "scope", "system", "direction", "crm_object"} {
		if strings.Contains(strings.Join(fieldRefs, " "), disallowed) {
			t.Fatalf("CRM field value refinements suggested unsupported filter %q: %v", disallowed, fieldRefs)
		}
	}
	if !strings.Contains(strings.Join(fieldRefs, " "), "value_query") {
		t.Fatalf("CRM field value refinements did not mention value_query: %v", fieldRefs)
	}

	crmRefs := crmTranscriptRefinements(searchTranscriptsByCRMContextArgs{
		Query:      "implementation",
		ObjectType: "Opportunity",
	})
	for _, disallowed := range []string{"from_date", "lifecycle_bucket", "scope", "system", "direction"} {
		if strings.Contains(strings.Join(crmRefs, " "), disallowed) {
			t.Fatalf("CRM transcript refinements suggested unsupported filter %q: %v", disallowed, crmRefs)
		}
	}
	if !strings.Contains(strings.Join(crmRefs, " "), "object_id") {
		t.Fatalf("CRM transcript refinements did not mention object_id: %v", crmRefs)
	}
}

func TestToolPresetCatalogAliasesAndGovernanceCompatibility(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		"business-pilot",
		"strict-business-pilot",
		"operator-smoke",
		"analyst-facade",
		"facade-analyst",
		"analyst-core",
		"postgres-analyst-core",
		"analyst",
		"analyst-expansion",
		"governance-search",
		"all-readonly",
		"all-tools",
		"all",
	} {
		if tools, err := ExpandToolPreset(name); err != nil {
			t.Fatalf("ExpandToolPreset(%q) returned error: %v", name, err)
		} else if len(tools) == 0 {
			t.Fatalf("ExpandToolPreset(%q) returned no tools", name)
		}
	}

	governanceTools, err := ExpandToolPreset("governance-search")
	if err != nil {
		t.Fatalf("ExpandToolPreset(governance-search) returned error: %v", err)
	}
	if err := ValidateGovernanceAllowlist(governanceTools); err != nil {
		t.Fatalf("governance-search preset rejected by governance validator: %v", err)
	}
	if err := ValidateGovernanceAllowlist([]string{"search_crm_field_values"}); err == nil {
		t.Fatal("governance validator accepted unsafe tool")
	}
	operatorSmoke, err := ExpandToolPreset("operator-smoke")
	if err != nil {
		t.Fatalf("ExpandToolPreset(operator-smoke) returned error: %v", err)
	}
	hasGetCall := false
	for _, tool := range operatorSmoke {
		if tool == "get_call" {
			hasGetCall = true
			break
		}
	}
	if !hasGetCall {
		t.Fatalf("operator-smoke tools=%v missing get_call", operatorSmoke)
	}

	seen := map[string]ToolPresetInfo{}
	for _, preset := range ToolPresetCatalog() {
		seen[preset.Name] = preset
		if preset.ToolCount != len(preset.Tools) {
			t.Fatalf("preset %q tool_count=%d len(tools)=%d", preset.Name, preset.ToolCount, len(preset.Tools))
		}
	}
	for _, name := range []string{"business-pilot", "operator-smoke", "analyst", "governance-search", "all-readonly"} {
		if _, ok := seen[name]; !ok {
			t.Fatalf("ToolPresetCatalog missing %q", name)
		}
	}
}

func TestBusinessAnalysisToolSetIsExposedWithSafeSchemas(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	server := NewServerWithOptions(store, "gongmcp", "test", WithTranscriptEvidenceProvenance(TranscriptEvidenceRaw))
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "tools",
		Method:  "tools/list",
	}))

	var listed struct {
		Result toolsListResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &listed); err != nil {
		t.Fatalf("unmarshal tools/list response: %v", err)
	}

	byName := make(map[string]tool, len(listed.Result.Tools))
	for _, item := range listed.Result.Tools {
		byName[item.Name] = item
	}
	var missing []string
	for _, name := range BusinessAnalysisToolNames() {
		item, ok := byName[name]
		if !ok {
			missing = append(missing, name)
			continue
		}
		assertBusinessAnalysisSchemaIsSafe(t, item)
	}
	if len(missing) > 0 {
		t.Fatalf("missing business-analysis tools: %v", missing)
	}
}

func TestBusinessAnalysisCohortNormalizesTitleQueryAndBoundsResults(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	seedBusinessAnalysisMCPFixtures(t, store)

	server := NewServer(store, "gongmcp", "test")
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "cohort",
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name": "build_call_cohort",
			"arguments": map[string]any{
				"filter": map[string]any{
					"title_query":       "  Business Discovery  ",
					"from_date":         "2026-01-01",
					"to_date":           "2026-04-01",
					"transcript_status": "present",
				},
				"limit": 10000,
			},
		}),
	}))

	result := decodeBusinessAnalysisResult(t, responses[0])
	if got := nestedString(result, "normalized_filter", "title_query"); !strings.EqualFold(got, "business discovery") {
		t.Fatalf("normalized title_query=%q want trimmed business discovery in %+v", got, result)
	}
	if cohortID := stringField(result, "cohort_id"); cohortID == "" {
		t.Fatalf("missing deterministic cohort_id in %+v", result)
	}
	if count := intField(result, "count"); count == 0 {
		t.Fatalf("missing non-zero filtered cohort count in %+v", result)
	}
	normalizedLimit := intField(result, "limit")
	if normalizedLimit == 0 {
		normalizedLimit = intField(mapField(t, result, "normalized_filter"), "limit")
	}
	if normalizedLimit <= 0 || normalizedLimit > 100 {
		t.Fatalf("limit=%d want normalized bounded limit <= 100 in %+v", normalizedLimit, result)
	}
	if count := intField(result, "count"); count > normalizedLimit {
		t.Fatalf("count=%d exceeded normalized limit=%d in %+v", count, normalizedLimit, result)
	}
	if _, ok := result["coverage_summary"]; !ok {
		t.Fatalf("missing coverage_summary in %+v", result)
	}
	if _, ok := result["warnings"]; !ok {
		t.Fatalf("missing warning flags in %+v", result)
	}
	if strings.Contains(mustJSONText(t, result["warnings"]), "title_query_not_yet_enforced") {
		t.Fatalf("title_query should be enforced, got warnings: %+v", result["warnings"])
	}
}

func TestBusinessAnalysisToolsRedactDefaultsAndReportLimitations(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	seedBusinessAnalysisMCPFixtures(t, store)

	server := NewServer(store, "gongmcp", "test")
	responses := runServer(t, server,
		requestFrame(Request{
			JSONRPC: "2.0",
			ID:      "quotes",
			Method:  "tools/call",
			Params: mustJSON(t, map[string]any{
				"name": "extract_theme_quotes",
				"arguments": map[string]any{
					"filter": map[string]any{
						"title_query": "business discovery",
						"from_date":   "2026-01-01",
						"to_date":     "2026-04-01",
					},
					"theme_query": "manual process",
					"limit":       25,
				},
			}),
		})+
			requestFrame(Request{
				JSONRPC: "2.0",
				ID:      "outcomes",
				Method:  "tools/call",
				Params: mustJSON(t, map[string]any{
					"name": "compare_theme_outcomes",
					"arguments": map[string]any{
						"filter": map[string]any{
							"title_query": "business discovery",
							"from_date":   "2026-01-01",
							"to_date":     "2026-04-01",
						},
						"theme_query": "manual process",
						"limit":       10,
					},
				}),
			}))

	quotes := decodeBusinessAnalysisResult(t, responses[0])
	rows := namedOrResultsArrayField(t, quotes, "quotes")
	if len(rows) == 0 {
		t.Fatalf("expected bounded quote evidence rows in %+v", quotes)
	}
	if len(rows) > 25 {
		t.Fatalf("quote row count=%d exceeded requested limit", len(rows))
	}
	firstQuote := rows[0].(map[string]any)
	for _, leaked := range []string{"call_id", "call_title", "title", "account_name", "opportunity_name", "speaker_id"} {
		if value, ok := firstQuote[leaked]; ok && value != "" {
			t.Fatalf("quote result leaked %s by default: %+v", leaked, firstQuote)
		}
	}
	if excerpt := stringField(firstQuote, "excerpt"); excerpt == "" || strings.Contains(excerpt, "raw transcript") {
		t.Fatalf("missing bounded safe quote excerpt: %+v", firstQuote)
	}

	outcomes := decodeBusinessAnalysisResult(t, responses[1])
	limitations := strings.ToLower(mustJSONText(t, outcomes["limitations"]))
	if !strings.Contains(limitations, "attribution") || !strings.Contains(limitations, "missing") {
		t.Fatalf("outcome result did not report missing attribution limitations: %+v", outcomes)
	}
	if _, ok := outcomes["coverage_summary"]; !ok {
		t.Fatalf("outcome result missing coverage_summary: %+v", outcomes)
	}
}

func TestSummarizeLossReasonsByThemeReturnsNormalizedBucketsAndHidesRaw(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openSeededStore(t)
	defer store.Close()

	type lossFixture struct {
		callID string
		title  string
		raw    string
		bucket string
	}
	fixtures := []lossFixture{
		{callID: "call_loss_price", title: "Loss reason coverage call price", raw: "Price too high", bucket: "price"},
		{callID: "call_loss_timing", title: "Loss reason coverage call timing", raw: "Timeline uncertainty", bucket: "timing"},
		{callID: "call_loss_competitor", title: "Loss reason coverage call competitor", raw: "Lost to Competitor", bucket: "competitor"},
		{callID: "call_loss_no_decision", title: "Loss reason coverage call no decision", raw: "No Decision", bucket: "no_decision"},
		{callID: "call_loss_unknown", title: "Loss reason coverage call unknown", raw: "Internal build on hadoop cluster", bucket: "unknown_other"},
	}
	for _, f := range fixtures {
		raw := mustJSON(t, map[string]any{
			"id":       f.callID,
			"title":    f.title,
			"started":  "2026-02-20T15:00:00Z",
			"duration": 1500,
			"metaData": map[string]any{
				"system":    "Zoom",
				"direction": "Conference",
				"scope":     "External",
			},
			"context": []any{
				map[string]any{
					"objectType": "Opportunity",
					"id":         "opp_" + f.callID,
					"fields": []any{
						map[string]any{"name": "StageName", "value": "Closed Lost"},
						map[string]any{"name": "LossReason", "value": f.raw},
					},
				},
			},
		})
		if _, err := store.UpsertCall(ctx, raw); err != nil {
			t.Fatalf("upsert loss-reason fixture %s: %v", f.callID, err)
		}
		if _, err := store.UpsertTranscript(ctx, mustJSON(t, map[string]any{
			"callTranscripts": []any{
				map[string]any{
					"callId": f.callID,
					"transcript": []any{
						map[string]any{
							"speakerId": "buyer",
							"sentences": []any{
								map[string]any{"start": 1000, "end": 2500, "text": "Loss reason coverage discussion in this fixture."},
							},
						},
					},
				},
			},
		})); err != nil {
			t.Fatalf("upsert loss-reason transcript %s: %v", f.callID, err)
		}
	}

	for _, mode := range []struct {
		name           string
		hideLossReason bool
	}{
		{name: "default", hideLossReason: false},
		{name: "hide_loss_reasons", hideLossReason: true},
	} {
		mode := mode
		t.Run(mode.name, func(t *testing.T) {
			opts := []ServerOption{}
			if mode.hideLossReason {
				opts = append(opts, WithPolicySwitches(PolicySwitches{HideLossReasons: true}))
			}
			server := NewServerWithOptions(store, "gongmcp", "test", opts...)
			responses := runServer(t, server, requestFrame(Request{
				JSONRPC: "2.0",
				ID:      "loss",
				Method:  "tools/call",
				Params: mustJSON(t, map[string]any{
					"name": "summarize_loss_reasons_by_theme",
					"arguments": map[string]any{
						"filter": map[string]any{
							"title_query": "loss reason coverage",
							"from_date":   "2026-01-01",
							"to_date":     "2026-04-01",
						},
						"theme_query": "loss reason",
						"limit":       50,
					},
				}),
			}))

			result := decodeBusinessAnalysisResult(t, responses[0])
			if dim, _ := result["dimension"].(string); dim != "loss_reason" {
				t.Fatalf("expected dimension=loss_reason, got %q in %+v", dim, result)
			}
			summaries := namedOrResultsArrayField(t, result, "summaries")
			gotBuckets := map[string]int{}
			rawText := mustJSONText(t, summaries)
			for _, row := range summaries {
				m, ok := row.(map[string]any)
				if !ok {
					continue
				}
				value := stringField(m, "value")
				gotBuckets[value]++
			}
			for _, want := range []string{"price", "timing", "competitor", "no_decision", "unknown_other"} {
				if gotBuckets[want] == 0 {
					t.Fatalf("expected bucket %q in loss_reason summaries, got %+v", want, summaries)
				}
			}
			for _, leak := range []string{"Price too high", "Timeline uncertainty", "Lost to Competitor", "No Decision", "Internal build on hadoop cluster"} {
				if strings.Contains(rawText, leak) {
					t.Fatalf("loss_reason summaries leaked raw value %q in %s", leak, rawText)
				}
			}
			limitations := strings.ToLower(mustJSONText(t, result["limitations"]))
			if !strings.Contains(limitations, "loss_reason_values_are_normalized_buckets_not_raw_text") {
				t.Fatalf("expected normalized-bucket limitation, got %s", limitations)
			}
			if !strings.Contains(limitations, "raw_loss_reason_text_is_hidden_by_default_and_suppressed_when_hide_loss_reasons_is_enabled") {
				t.Fatalf("expected hide-loss-reasons limitation, got %s", limitations)
			}
			warnings := strings.ToLower(mustJSONText(t, result["warnings"]))
			if !strings.Contains(warnings, "deterministic normalized buckets") {
				t.Fatalf("expected deterministic-buckets warning, got %s", warnings)
			}
			if mode.hideLossReason {
				if !strings.Contains(warnings, "hide_loss_reasons_enforced") {
					t.Fatalf("expected hide_loss_reasons_enforced warning when policy switch is enabled, got %s", warnings)
				}
			} else if strings.Contains(warnings, "hide_loss_reasons_enforced") {
				t.Fatalf("did not expect hide_loss_reasons_enforced warning when policy switch is disabled, got %s", warnings)
			}
		})
	}
}

func TestBusinessAnalysisSmallCellSuppressionOmitsLowCountDimensionBuckets(t *testing.T) {
	t.Parallel()

	server := NewServerWithOptions(nil, "gongmcp", "test", WithBusinessAnalysisSmallCellMin(3))
	response := businessAnalysisResponse{
		Tool:        "summarize_themes_by_persona",
		Status:      "cache_derived",
		Limitations: []string{"participant_title_coverage_limited"},
		Summaries: []sqlite.BusinessAnalysisDimensionRow{
			{Dimension: "persona", Value: "participant_title_present", CallCount: 1},
			{Dimension: "persona", Value: "larger_segment", CallCount: 3},
			{Dimension: "persona", Value: "unknown", CallCount: 0},
		},
	}

	server.applyBusinessAnalysisSmallCellSuppression(&response)

	if len(response.Summaries) != 2 {
		t.Fatalf("summary count=%d want 2 after suppression: %+v", len(response.Summaries), response.Summaries)
	}
	if response.Summaries[0].Value != "larger_segment" || response.Summaries[1].Value != "unknown" {
		t.Fatalf("unexpected kept summaries: %+v", response.Summaries)
	}
	warnings := mustJSONText(t, response.Warnings)
	if !strings.Contains(warnings, "small_cell_suppression_applied") {
		t.Fatalf("missing suppression warning: %+v", response.Warnings)
	}
	limitations := mustJSONText(t, response.Limitations)
	if !strings.Contains(limitations, "small_cell_suppression_min_3") {
		t.Fatalf("missing suppression limitation: %+v", response.Limitations)
	}
	suppression, ok := response.SynthesisInputs["small_cell_suppression"].(map[string]any)
	if !ok {
		t.Fatalf("missing machine-readable suppression inputs: %+v", response.SynthesisInputs)
	}
	if intField(suppression, "minimum_call_count") != 3 || intField(suppression, "suppressed_bucket_count") != 1 || intField(suppression, "suppressed_call_count") != 1 {
		t.Fatalf("unexpected suppression inputs: %+v", suppression)
	}
}

func TestBusinessAnalysisSmallCellSuppressionDefaultsDisabled(t *testing.T) {
	t.Parallel()

	server := NewServer(nil, "gongmcp", "test")
	response := businessAnalysisResponse{
		Summaries: []sqlite.BusinessAnalysisDimensionRow{
			{Dimension: "persona", Value: "participant_title_present", CallCount: 1},
		},
	}

	server.applyBusinessAnalysisSmallCellSuppression(&response)

	if len(response.Summaries) != 1 || response.Summaries[0].Value != "participant_title_present" {
		t.Fatalf("default suppression changed summaries: %+v", response.Summaries)
	}
	if strings.Contains(mustJSONText(t, response.Warnings), "small_cell_suppression") {
		t.Fatalf("default suppression emitted warning: %+v", response.Warnings)
	}
}

func TestBusinessAnalysisEvidenceToolsRequireSearchTerm(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	seedBusinessAnalysisMCPFixtures(t, store)

	server := NewServer(store, "gongmcp", "test")
	// Phase 13e: discover_themes_in_cohort intentionally accepts a selective
	// cohort filter without theme_query/query and returns broad-discovery
	// candidate seed terms. The other transcript-evidence and quote tools
	// must still require a query/theme_query to avoid raw-transcript dumps.
	for _, toolName := range []string{
		"search_transcripts_by_filters",
		"extract_theme_quotes",
		"build_theme_brief",
		"search_quotes_in_cohort",
		"build_quote_pack",
	} {
		responses := runServer(t, server, requestFrame(Request{
			JSONRPC: "2.0",
			ID:      toolName,
			Method:  "tools/call",
			Params: mustJSON(t, map[string]any{
				"name": toolName,
				"arguments": map[string]any{
					"filter": map[string]any{
						"title_query": "business discovery",
						"from_date":   "2026-01-01",
						"to_date":     "2026-04-01",
					},
				},
			}),
		}))

		var envelope struct {
			Result toolCallResult `json:"result"`
		}
		if err := json.Unmarshal(responses[0], &envelope); err != nil {
			t.Fatalf("unmarshal %s response: %v", toolName, err)
		}
		if !envelope.Result.IsError {
			t.Fatalf("%s returned transcript evidence without a query: %+v", toolName, envelope.Result)
		}
		if !strings.Contains(envelope.Result.Content[0].Text, "requires") {
			t.Fatalf("%s error did not explain missing query: %+v", toolName, envelope.Result)
		}
	}
}

func TestBusinessAnalysisDiscoverThemesInCohortSupportsSeedlessBroadDiscovery(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	seedBusinessAnalysisMCPFixtures(t, store)

	server := NewServer(store, "gongmcp", "test")
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "discover-themes-seedless",
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name": "discover_themes_in_cohort",
			"arguments": map[string]any{
				"filter": map[string]any{
					"title_query": "business discovery",
					"from_date":   "2026-01-01",
					"to_date":     "2026-04-01",
				},
				"limit": 10,
			},
		}),
	}))

	result := decodeBusinessAnalysisResult(t, responses[0])
	themes := arrayField(t, result, "themes")
	if len(themes) == 0 {
		t.Fatalf("seedless discover_themes_in_cohort returned no themes in %+v", result)
	}
	first := themes[0].(map[string]any)
	if term := stringField(first, "theme"); term == "" {
		t.Fatalf("first theme missing term: %+v", first)
	}
	warnings := strings.ToLower(mustJSONText(t, result["warnings"]))
	if !strings.Contains(warnings, "broad_discovery") || !strings.Contains(warnings, "seedless") {
		t.Fatalf("expected broad-discovery seedless warning, got warnings=%+v", result["warnings"])
	}
	if !strings.Contains(warnings, "seed") {
		t.Fatalf("expected suggested-seed warning text, got warnings=%+v", result["warnings"])
	}
	limitations := strings.ToLower(mustJSONText(t, result["limitations"]))
	if !strings.Contains(limitations, "broad_discovery") {
		t.Fatalf("expected broad_discovery limitation, got limitations=%+v", result["limitations"])
	}

	rows := namedOrResultsArrayField(t, result, "results")
	if len(rows) > 10 {
		t.Fatalf("seedless discovery returned %d rows; bounded limit was 10", len(rows))
	}
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		if account := stringField(row, "account_name"); account != "" {
			t.Fatalf("seedless discovery surfaced account_name without explicit opt-in: %+v", row)
		}
	}
}

func TestFacadeAnalyzeThemesDiscoverSupportsSeedlessBroadDiscovery(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	seedBusinessAnalysisMCPFixtures(t, store)

	server := NewServerWithOptions(store, "gongmcp", "test", WithToolAllowlist([]string{
		FacadeToolAnalyze,
		"discover_themes_in_cohort",
	}))

	args, err := json.Marshal(map[string]any{
		"operation": OpAnalyzeThemesDiscover,
		"arguments": map[string]any{
			"filter": map[string]any{
				"title_query": "business discovery",
				"from_date":   "2026-01-01",
				"to_date":     "2026-04-01",
			},
			"limit": 8,
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	result, err := server.executeFacadeDispatch(t.Context(), FacadeToolAnalyze, args)
	if err != nil {
		t.Fatalf("dispatch analyze.themes.discover seedless: %v", err)
	}
	if result.IsError {
		t.Fatalf("facade dispatch returned tool error: %+v", result)
	}
	wrapper := decodeFacadeWrapper(t, result)
	if wrapper["operation"] != OpAnalyzeThemesDiscover {
		t.Fatalf("operation=%v want %s", wrapper["operation"], OpAnalyzeThemesDiscover)
	}
	if wrapper["routed_tool"] != "discover_themes_in_cohort" {
		t.Fatalf("routed_tool=%v want discover_themes_in_cohort", wrapper["routed_tool"])
	}
	inner, _ := wrapper["result"].(map[string]any)
	if inner == nil {
		t.Fatalf("missing inner result: %+v", wrapper)
	}
	themesAny, ok := inner["themes"].([]any)
	if !ok || len(themesAny) == 0 {
		t.Fatalf("expected nonempty themes from seedless facade discovery: %+v", inner)
	}
	warnings := strings.ToLower(mustJSONText(t, inner["warnings"]))
	if !strings.Contains(warnings, "broad_discovery_seedless") {
		t.Fatalf("expected broad_discovery_seedless warning in facade response: %+v", inner["warnings"])
	}
}

func TestBusinessAnalysisGovernanceFailsClosed(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	seedBusinessAnalysisMCPFixtures(t, store)

	server := NewServerWithOptions(store, "gongmcp", "test", WithSuppressedCallIDs([]string{"call_business_discovery_001"}))
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "business-analysis-governance",
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name": "build_call_cohort",
			"arguments": map[string]any{
				"filter": map[string]any{
					"title_query": "business discovery",
					"from_date":   "2026-01-01",
					"to_date":     "2026-04-01",
				},
			},
		}),
	}))

	var envelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &envelope); err != nil {
		t.Fatalf("unmarshal governance response: %v", err)
	}
	if !envelope.Result.IsError {
		t.Fatalf("expected business-analysis tool to fail closed while governance active")
	}
	text := envelope.Result.Content[0].Text
	if strings.Contains(text, "call_business_discovery_001") || strings.Contains(text, "Discovery Account") {
		t.Fatalf("business-analysis governance error leaked suppressed data: %s", text)
	}
}

func TestBusinessAnalysisRepresentativeJSONRPCCalls(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	seedBusinessAnalysisMCPFixtures(t, store)

	server := NewServerWithOptions(store, "gongmcp", "test", WithToolAllowlist(BusinessAnalysisToolNames()))
	frames := requestFrame(Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: mustJSON(t, map[string]any{
			"protocolVersion": "2025-11-25",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "codex-smoke", "version": "0"},
		}),
	}) + requestFrame(Request{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/list",
		Params:  mustJSON(t, map[string]any{}),
	}) + requestFrame(Request{
		JSONRPC: "2.0",
		ID:      3,
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name": "search_transcripts_by_filters",
			"arguments": map[string]any{
				"filter": map[string]any{
					"title_query": "business discovery",
					"from_date":   "2026-01-01",
					"to_date":     "2026-04-01",
				},
				"query": "pain point",
				"limit": 5,
			},
		}),
	}) + requestFrame(Request{
		JSONRPC: "2.0",
		ID:      4,
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name": "build_theme_brief",
			"arguments": map[string]any{
				"filter": map[string]any{
					"title_query": "business discovery",
					"from_date":   "2026-01-01",
					"to_date":     "2026-04-01",
				},
				"theme_query": "manual process",
				"limit":       5,
			},
		}),
	}) + requestFrame(Request{
		JSONRPC: "2.0",
		ID:      5,
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name": "explain_analysis_limitations",
			"arguments": map[string]any{
				"filter": map[string]any{"title_query": "business discovery"},
			},
		}),
	})
	responses := runServer(t, server, frames)
	if len(responses) != 5 {
		t.Fatalf("response count=%d want 5", len(responses))
	}
	for idx, raw := range responses[2:] {
		result := decodeBusinessAnalysisResult(t, raw)
		if _, ok := result["normalized_filter"]; !ok {
			t.Fatalf("response %d missing normalized_filter: %+v", idx+3, result)
		}
	}
}

func TestInitializeAdvertisesClaudeDesktopProtocolVersion(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	server := NewServer(store, "gongmcp", "test")
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      0,
		Method:  "initialize",
		Params: mustJSON(t, map[string]any{
			"protocolVersion": "2025-11-25",
			"capabilities": map[string]any{
				"extensions": map[string]any{
					"io.modelcontextprotocol/ui": map[string]any{
						"mimeTypes": []string{"text/html;profile=mcp-app"},
					},
				},
			},
			"clientInfo": map[string]any{
				"name":    "claude-ai",
				"version": "0.1.0",
			},
		}),
	}))

	var envelope struct {
		Result initializeResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &envelope); err != nil {
		t.Fatalf("unmarshal initialize response: %v", err)
	}
	if envelope.Result.ProtocolVersion != "2025-11-25" {
		t.Fatalf("protocolVersion=%q want 2025-11-25", envelope.Result.ProtocolVersion)
	}
}

func TestServerAcceptsJSONLineTransport(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	payload := mustJSON(t, Request{
		JSONRPC: "2.0",
		ID:      0,
		Method:  "initialize",
		Params: mustJSON(t, map[string]any{
			"protocolVersion": "2025-11-25",
			"capabilities": map[string]any{
				"roots":       map[string]any{},
				"elicitation": map[string]any{},
			},
			"clientInfo": map[string]any{
				"name":    "claude-code",
				"version": "2.1.108",
			},
		}),
	})

	server := NewServer(store, "gongmcp", "test")
	var output bytes.Buffer
	if err := server.Serve(context.Background(), bytes.NewReader(append(payload, '\n')), &output); err != nil {
		t.Fatalf("serve JSON line transport: %v", err)
	}

	var envelope struct {
		Result initializeResult `json:"result"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &envelope); err != nil {
		t.Fatalf("unmarshal JSON line response %q: %v", output.String(), err)
	}
	if envelope.Result.ProtocolVersion != "2025-11-25" {
		t.Fatalf("protocolVersion=%q want 2025-11-25", envelope.Result.ProtocolVersion)
	}
}

func TestReadFrameRejectsOversizedJSONLine(t *testing.T) {
	t.Parallel()

	input := "{" + strings.Repeat("x", maxFrameBytes) + "\n"
	_, err := readFrame(bufio.NewReader(strings.NewReader(input)))
	if err == nil {
		t.Fatal("readFrame returned nil error for oversized JSON-line frame")
	}
	if !strings.Contains(err.Error(), "exceeds maximum") {
		t.Fatalf("error=%q want exceeds maximum", err)
	}
}

func TestUnknownToolReturnsToolError(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	server := NewServer(store, "gongmcp", "test")
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "blocked",
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name":      "api_raw",
			"arguments": map[string]any{},
		}),
	}))

	var envelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &envelope); err != nil {
		t.Fatalf("unmarshal tools/call response: %v", err)
	}
	if !envelope.Result.IsError {
		t.Fatalf("expected tool error result, got %+v", envelope.Result)
	}
	if len(envelope.Result.Content) != 1 {
		t.Fatalf("content count=%d want 1", len(envelope.Result.Content))
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(envelope.Result.Content[0].Text), &payload); err != nil {
		t.Fatalf("unmarshal error payload: %v", err)
	}
	if payload["error"] == "" {
		t.Fatalf("expected error message in tool response")
	}
}

func TestToolsCallIgnoresMCPMetaExtensionFields(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	server := NewServerWithOptions(store, "gongmcp", "test", WithToolAllowlist([]string{
		"get_sync_status",
	}))
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "status",
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name": "get_sync_status",
			"arguments": map[string]any{
				"_meta": map[string]any{
					"openai/userAgent": "chatgpt-connector",
				},
			},
			"_meta": map[string]any{
				"progressToken": "chatgpt-status",
			},
		}),
	}))

	if len(responses) != 1 {
		t.Fatalf("response count=%d want 1", len(responses))
	}
	var envelope struct {
		Result toolCallResult `json:"result"`
		Error  *responseError `json:"error"`
	}
	if err := json.Unmarshal(responses[0], &envelope); err != nil {
		t.Fatalf("unmarshal tools/call response: %v", err)
	}
	if envelope.Error != nil {
		t.Fatalf("unexpected JSON-RPC error: %+v", envelope.Error)
	}
	if envelope.Result.IsError {
		t.Fatalf("unexpected tool error: %+v", envelope.Result)
	}
	if len(envelope.Result.Content) != 1 {
		t.Fatalf("content count=%d want 1", len(envelope.Result.Content))
	}
	var status publicSyncStatus
	if err := json.Unmarshal([]byte(envelope.Result.Content[0].Text), &status); err != nil {
		t.Fatalf("unmarshal sync status payload: %v", err)
	}
	if status.TotalCalls == 0 {
		t.Fatalf("TotalCalls=%d want seeded data", status.TotalCalls)
	}
}

func TestAllowlistedServerRejectsNonAllowedToolCallsWithGenericError(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	server := NewServerWithOptions(store, "gongmcp", "test", WithToolAllowlist([]string{"get_sync_status"}))
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "blocked",
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name": "search_calls",
			"arguments": map[string]any{
				"limit": 1,
			},
		}),
	}))

	var envelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &envelope); err != nil {
		t.Fatalf("unmarshal tools/call response: %v", err)
	}
	if !envelope.Result.IsError {
		t.Fatalf("expected tool error result, got %+v", envelope.Result)
	}

	var payload map[string]string
	if err := json.Unmarshal([]byte(envelope.Result.Content[0].Text), &payload); err != nil {
		t.Fatalf("unmarshal error payload: %v", err)
	}
	if got := payload["error"]; got != "tool is not available" {
		t.Fatalf("error=%q want generic hidden-tool message", got)
	}
}

func TestGovernanceSuppressionHidesSearchCallsAndNames(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	ctx := context.Background()
	if _, err := store.UpsertCall(ctx, mustJSON(t, map[string]any{
		"id":       "call-governance-blocked",
		"title":    "Restricted synthetic account call",
		"started":  "2026-04-24T12:00:00Z",
		"duration": 1200,
		"context": []any{
			map[string]any{
				"objectType": "Account",
				"id":         "acct-governance-blocked",
				"name":       "NoAI Synthetic Corp",
				"fields": []any{
					map[string]any{"name": "Name", "value": "NoAI Synthetic Corp"},
				},
			},
		},
	})); err != nil {
		t.Fatalf("upsert blocked call: %v", err)
	}
	if _, err := store.UpsertCall(ctx, mustJSON(t, map[string]any{
		"id":       "call-governance-allowed",
		"title":    "Allowed synthetic account call",
		"started":  "2026-04-25T12:00:00Z",
		"duration": 1200,
		"context": []any{
			map[string]any{
				"objectType": "Account",
				"id":         "acct-governance-allowed",
				"name":       "Allowed Synthetic Corp",
				"fields": []any{
					map[string]any{"name": "Name", "value": "Allowed Synthetic Corp"},
				},
			},
		},
	})); err != nil {
		t.Fatalf("upsert allowed call: %v", err)
	}

	server := NewServerWithOptions(store, "gongmcp", "test", WithSuppressedCallIDs([]string{"call-governance-blocked"}))
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "search",
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name": "search_calls",
			"arguments": map[string]any{
				"limit": 20,
			},
		}),
	}))

	var envelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &envelope); err != nil {
		t.Fatalf("unmarshal tools/call response: %v", err)
	}
	if envelope.Result.IsError {
		t.Fatalf("search_calls returned error: %+v", envelope.Result)
	}
	text := envelope.Result.Content[0].Text
	if strings.Contains(text, "call-governance-blocked") || strings.Contains(text, "NoAI Synthetic Corp") {
		t.Fatalf("governance response leaked blocked data: %s", text)
	}
	if !strings.Contains(text, "call-governance-allowed") {
		t.Fatalf("governance response missing allowed call: %s", text)
	}
	if strings.Contains(text, "filtered_call_count") {
		t.Fatalf("governance response exposed filtered count oracle: %s", text)
	}
}

func TestGovernanceSuppressionSearchCallsOverfetchesBeforeLimit(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	ctx := context.Background()
	if _, err := store.UpsertCall(ctx, mustJSON(t, map[string]any{
		"id":       "call-governance-limit-blocked",
		"title":    "Blocked governance limit call",
		"started":  "2026-05-02T13:00:00Z",
		"duration": 1200,
	})); err != nil {
		t.Fatalf("upsert blocked call: %v", err)
	}
	if _, err := store.UpsertCall(ctx, mustJSON(t, map[string]any{
		"id":       "call-governance-limit-allowed",
		"title":    "Allowed governance limit call",
		"started":  "2026-05-02T12:00:00Z",
		"duration": 1200,
	})); err != nil {
		t.Fatalf("upsert allowed call: %v", err)
	}

	server := NewServerWithOptions(store, "gongmcp", "test", WithSuppressedCallIDs([]string{"call-governance-limit-blocked"}))
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "search",
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name": "search_calls",
			"arguments": map[string]any{
				"from_date": "2026-05-02",
				"to_date":   "2026-05-02",
				"limit":     1,
			},
		}),
	}))

	var envelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &envelope); err != nil {
		t.Fatalf("unmarshal tools/call response: %v", err)
	}
	if envelope.Result.IsError {
		t.Fatalf("search_calls returned error: %+v", envelope.Result)
	}
	text := envelope.Result.Content[0].Text
	if strings.Contains(text, "call-governance-limit-blocked") {
		t.Fatalf("governance response leaked blocked limit row: %s", text)
	}
	if !strings.Contains(text, "call-governance-limit-allowed") {
		t.Fatalf("governance response missed allowed row after filtered limit: %s", text)
	}
}

func TestGovernanceSuppressionCRMContextSearchOverfetchesBeforeLimit(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	ctx := context.Background()
	for _, call := range []map[string]any{
		{
			"id":      "call-governance-crm-blocked",
			"title":   "Blocked governance CRM transcript",
			"started": "2026-05-02T13:00:00Z",
			"context": map[string]any{"crmObjects": []any{map[string]any{
				"type": "Opportunity",
				"id":   "opp-governance-crm",
				"name": "Suppressed CRM Opportunity",
				"fields": map[string]any{
					"StageName": "Proposal",
				},
			}}},
		},
		{
			"id":      "call-governance-crm-allowed",
			"title":   "Allowed governance CRM transcript",
			"started": "2026-05-02T12:00:00Z",
			"context": map[string]any{"crmObjects": []any{map[string]any{
				"type": "Opportunity",
				"id":   "opp-governance-crm",
				"name": "Allowed CRM Opportunity",
				"fields": map[string]any{
					"StageName": "Proposal",
				},
			}}},
		},
	} {
		if _, err := store.UpsertCall(ctx, mustJSON(t, call)); err != nil {
			t.Fatalf("upsert call: %v", err)
		}
	}
	for _, transcript := range []map[string]any{
		{
			"callId": "call-governance-crm-blocked",
			"transcript": []any{map[string]any{
				"speakerId": "blocked-speaker",
				"sentences": []any{map[string]any{"start": 0, "end": 1000, "text": "pricing governance crm limit marker"}},
			}},
		},
		{
			"callId": "call-governance-crm-allowed",
			"transcript": []any{map[string]any{
				"speakerId": "allowed-speaker",
				"sentences": []any{map[string]any{"start": 0, "end": 1000, "text": "pricing governance crm limit marker"}},
			}},
		},
	} {
		if _, err := store.UpsertTranscript(ctx, mustJSON(t, transcript)); err != nil {
			t.Fatalf("upsert transcript: %v", err)
		}
	}

	server := NewServerWithOptions(store, "gongmcp", "test", WithSuppressedCallIDs([]string{"call-governance-crm-blocked"}))
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "search",
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name": "search_transcripts_by_crm_context",
			"arguments": map[string]any{
				"query":       "pricing governance crm",
				"object_type": "Opportunity",
				"object_id":   "opp-governance-crm",
				"limit":       1,
			},
		}),
	}))

	var envelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &envelope); err != nil {
		t.Fatalf("unmarshal tools/call response: %v", err)
	}
	if envelope.Result.IsError {
		t.Fatalf("search_transcripts_by_crm_context returned error: %+v", envelope.Result)
	}
	text := envelope.Result.Content[0].Text
	if strings.Contains(text, "call-governance-crm-blocked") || strings.Contains(text, "Suppressed CRM Opportunity") {
		t.Fatalf("governance CRM-context response leaked blocked row: %s", text)
	}
	if !strings.Contains(text, "pricing") {
		t.Fatalf("governance CRM-context response missed allowed snippet after filtered limit: %s", text)
	}
}

func TestGovernanceSuppressionFailsClosedForAggregateTool(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	server := NewServerWithOptions(store, "gongmcp", "test", WithSuppressedCallIDs([]string{"call-id"}))
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "aggregate",
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name":      "summarize_call_facts",
			"arguments": map[string]any{},
		}),
	}))

	var envelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &envelope); err != nil {
		t.Fatalf("unmarshal tools/call response: %v", err)
	}
	if !envelope.Result.IsError {
		t.Fatalf("expected aggregate to fail closed while governance active")
	}
	if strings.Contains(envelope.Result.Content[0].Text, "NoAI Synthetic Corp") {
		t.Fatalf("aggregate error leaked restricted name: %s", envelope.Result.Content[0].Text)
	}
}

func TestGovernanceSuppressionGetCallDoesNotEchoSuppressedID(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	server := NewServerWithOptions(store, "gongmcp", "test", WithSuppressedCallIDs([]string{"call-id"}))
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "get-call",
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name": "get_call",
			"arguments": map[string]any{
				"call_id": "call-id",
			},
		}),
	}))

	var envelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &envelope); err != nil {
		t.Fatalf("unmarshal get_call response: %v", err)
	}
	if !envelope.Result.IsError {
		t.Fatalf("expected get_call to hide suppressed call")
	}
	if strings.Contains(envelope.Result.Content[0].Text, "call-id") {
		t.Fatalf("get_call error leaked suppressed call id: %s", envelope.Result.Content[0].Text)
	}
}

func TestGovernanceSuppressionHidesLifecycleSearchCalls(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	ctx := context.Background()
	if _, err := store.UpsertCall(ctx, mustJSON(t, map[string]any{
		"id":       "call-governance-lifecycle-blocked",
		"title":    "Blocked lifecycle call",
		"started":  "2026-04-26T12:00:00Z",
		"duration": 1200,
		"context": []any{
			map[string]any{
				"objectType": "Account",
				"id":         "acct-governance-lifecycle-blocked",
				"name":       "NoAI Lifecycle Corp",
				"fields": []any{
					map[string]any{"name": "Name", "value": "NoAI Lifecycle Corp"},
				},
			},
		},
	})); err != nil {
		t.Fatalf("upsert blocked lifecycle call: %v", err)
	}

	server := NewServerWithOptions(store, "gongmcp", "test", WithSuppressedCallIDs([]string{"call-governance-lifecycle-blocked"}))
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "lifecycle",
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name": "search_calls_by_lifecycle",
			"arguments": map[string]any{
				"limit": 20,
			},
		}),
	}))

	var envelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &envelope); err != nil {
		t.Fatalf("unmarshal tools/call response: %v", err)
	}
	if envelope.Result.IsError {
		t.Fatalf("search_calls_by_lifecycle returned error: %+v", envelope.Result)
	}
	text := envelope.Result.Content[0].Text
	if strings.Contains(text, "call-governance-lifecycle-blocked") || strings.Contains(text, "NoAI Lifecycle Corp") {
		t.Fatalf("lifecycle search leaked governed data: %s", text)
	}
}

func TestSearchTranscriptSegmentsReturnsSnippetsWithoutTextField(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	server := NewServer(store, "gongmcp", "test")
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "snippets",
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name": "search_transcript_segments",
			"arguments": map[string]any{
				"query": "external",
				"limit": 5,
			},
		}),
	}))

	var envelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &envelope); err != nil {
		t.Fatalf("unmarshal tools/call response: %v", err)
	}
	if envelope.Result.IsError {
		t.Fatalf("unexpected tool error: %+v", envelope.Result)
	}
	if len(envelope.Result.Content) != 1 {
		t.Fatalf("content count=%d want 1", len(envelope.Result.Content))
	}

	var rows []map[string]any
	if err := json.Unmarshal([]byte(envelope.Result.Content[0].Text), &rows); err != nil {
		t.Fatalf("unmarshal snippet payload: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("snippet count=%d want 1", len(rows))
	}
	if _, ok := rows[0]["snippet"]; !ok {
		t.Fatalf("snippet field missing in %v", rows[0])
	}
	if _, ok := rows[0]["text"]; ok {
		t.Fatalf("unexpected raw text field in %v", rows[0])
	}
	for _, field := range []string{"call_id", "speaker_id"} {
		if _, ok := rows[0][field]; ok {
			t.Fatalf("unexpected redacted field %q in %v", field, rows[0])
		}
	}
}

func TestSummarizeScorecardActivityRedactsIdentifiersByDefault(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	seedScorecardActivityMCPFixtures(t, store)

	server := NewServer(store, "gongmcp", "test")
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "scorecard-activity",
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name":      "summarize_scorecard_activity",
			"arguments": map[string]any{"limit": 10},
		}),
	}))

	var envelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &envelope); err != nil {
		t.Fatalf("unmarshal tools/call response: %v", err)
	}
	if envelope.Result.IsError {
		t.Fatalf("unexpected tool error: %+v", envelope.Result)
	}
	if len(envelope.Result.Content) != 1 {
		t.Fatalf("content count=%d want 1", len(envelope.Result.Content))
	}
	text := envelope.Result.Content[0].Text
	for _, forbidden := range []string{
		"answered-activity-001",
		"call-activity-001",
		"scorecard-activity-001",
		"user-activity-001",
		"reviewer-activity-001",
		"question-activity-001",
		"Confirmed pain",
		"Raw activity call",
		"raw_json",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("MCP scorecard activity response leaked %q: %s", forbidden, text)
		}
	}
	var report sqlite.ScorecardActivityOverview
	if err := json.Unmarshal([]byte(text), &report); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if report.TotalAnsweredScorecards != 1 || report.ManualCount != 1 {
		t.Fatalf("unexpected report counts: %+v", report)
	}
}

func TestSummarizeScorecardActivityUsesMCPGroupLimitPolicy(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	seedScorecardActivityMCPFixtures(t, store)
	seedAdditionalScorecardActivityMCPFixture(t, store)

	policy := DefaultLimitPolicy()
	policy.CallFactGroups = 1
	server := NewServerWithOptions(store, "gongmcp", "test", WithLimitPolicy(policy))
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "scorecard-activity-limit",
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name":      "summarize_scorecard_activity",
			"arguments": map[string]any{"limit": 100},
		}),
	}))

	var envelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &envelope); err != nil {
		t.Fatalf("unmarshal tools/call response: %v", err)
	}
	if envelope.Result.IsError {
		t.Fatalf("unexpected tool error: %+v", envelope.Result)
	}
	var report sqlite.ScorecardActivityOverview
	if err := json.Unmarshal([]byte(envelope.Result.Content[0].Text), &report); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if len(report.ByScorecard) != 1 || len(report.ByReviewMethod) != 1 || len(report.ByTranscriptStatus) != 1 {
		t.Fatalf("scorecard activity report did not honor MCP group limit: %+v", report)
	}
	if report.TotalAnsweredScorecards != 2 {
		t.Fatalf("totals were unexpectedly limited: %+v", report)
	}
}

func TestSearchTranscriptSegmentsCanOptIntoIdentifiers(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	server := NewServer(store, "gongmcp", "test")
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "snippets-with-ids",
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name": "search_transcript_segments",
			"arguments": map[string]any{
				"query":               "external",
				"limit":               5,
				"include_call_ids":    true,
				"include_speaker_ids": true,
			},
		}),
	}))

	var envelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &envelope); err != nil {
		t.Fatalf("unmarshal tools/call response: %v", err)
	}
	if envelope.Result.IsError {
		t.Fatalf("unexpected tool error: %+v", envelope.Result)
	}

	var rows []map[string]any
	if err := json.Unmarshal([]byte(envelope.Result.Content[0].Text), &rows); err != nil {
		t.Fatalf("unmarshal snippet payload: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("snippet count=%d want 1", len(rows))
	}
	if got := rows[0]["call_id"]; got == "" {
		t.Fatalf("call_id was not returned after opt-in: %v", rows[0])
	}
	if got := rows[0]["speaker_id"]; got == "" {
		t.Fatalf("speaker_id was not returned after opt-in: %v", rows[0])
	}
}

func TestSearchTranscriptSegmentsSupportsCallFactFilters(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	seedFilteredSegmentFixture(t, store)

	server := NewServer(store, "gongmcp", "test")
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "filtered-snippets",
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name": "search_transcript_segments",
			"arguments": map[string]any{
				"query":               "implementation",
				"from_date":           "2026-01-01",
				"to_date":             "2026-03-31",
				"scope":               "External",
				"limit":               5,
				"include_call_ids":    true,
				"include_speaker_ids": true,
			},
		}),
	}))

	var envelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &envelope); err != nil {
		t.Fatalf("unmarshal tools/call response: %v", err)
	}
	if envelope.Result.IsError {
		t.Fatalf("unexpected tool error: %+v", envelope.Result)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(envelope.Result.Content[0].Text), &rows); err != nil {
		t.Fatalf("unmarshal filtered snippet payload: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("filtered snippet count=%d want 1: %+v", len(rows), rows)
	}
	if rows[0]["call_id"] == "" || rows[0]["speaker_id"] == "" {
		t.Fatalf("filtered identifier opt-in did not return ids: %+v", rows[0])
	}
	if _, ok := rows[0]["context_excerpt"]; ok {
		t.Fatalf("search_transcript_segments should preserve snippet shape without context_excerpt: %+v", rows[0])
	}
}

func TestSearchTranscriptSegmentsReportsCapHit(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	server := NewServer(store, "gongmcp", "test")
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "capped-snippets",
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name": "search_transcript_segments",
			"arguments": map[string]any{
				"query": "external",
				"limit": 1,
			},
		}),
	}))

	var envelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &envelope); err != nil {
		t.Fatalf("unmarshal tools/call response: %v", err)
	}
	if envelope.Result.IsError {
		t.Fatalf("unexpected tool error: %+v", envelope.Result)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(envelope.Result.Content[0].Text), &payload); err != nil {
		t.Fatalf("unmarshal cap payload: %v", err)
	}
	if capped, _ := payload["capped"].(bool); !capped {
		t.Fatalf("payload did not report cap hit: %+v", payload)
	}
	if intField(payload, "limit") != 1 || intField(payload, "returned") != 1 {
		t.Fatalf("unexpected cap metadata: %+v", payload)
	}
	if rows := arrayField(t, payload, "results"); len(rows) != 1 {
		t.Fatalf("results count=%d want 1 in %+v", len(rows), payload)
	}
	if refs := arrayField(t, payload, "suggested_refinements"); len(refs) == 0 {
		t.Fatalf("missing suggested refinements in %+v", payload)
	}
}

func TestSearchTranscriptSegmentsOmittedLimitHonorsConfiguredCap(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	seedFilteredSegmentFixture(t, store)
	seedTranscriptSegmentFixture(t, store, "call_filtered_segments_2", "buyer-filtered-2", "2026-02-11T15:00:00Z", "The implementation plan needs another external review.")

	policy, err := DefaultLimitPolicy().WithOverride("search_results", 1)
	if err != nil {
		t.Fatalf("WithOverride returned error: %v", err)
	}
	server := NewServerWithOptions(store, "gongmcp", "test", WithLimitPolicy(policy))
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "omitted-limit-cap",
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name": "search_transcript_segments",
			"arguments": map[string]any{
				"query": "implementation",
			},
		}),
	}))

	var envelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &envelope); err != nil {
		t.Fatalf("unmarshal tools/call response: %v", err)
	}
	if envelope.Result.IsError {
		t.Fatalf("unexpected tool error: %+v", envelope.Result)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(envelope.Result.Content[0].Text), &payload); err != nil {
		t.Fatalf("unmarshal cap payload: %v", err)
	}
	if intField(payload, "limit") != 1 || intField(payload, "returned") != 1 {
		t.Fatalf("omitted limit did not honor configured cap: %+v", payload)
	}
}

func TestSearchTranscriptsByCallFactsFiltersAndRedactsIdentifiers(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	ctx := context.Background()

	for _, raw := range []json.RawMessage{
		mustJSON(t, map[string]any{
			"metaData": map[string]any{
				"id":        "call_theme_external",
				"title":     "External theme evidence",
				"started":   "2026-02-10T15:00:00Z",
				"duration":  1800,
				"system":    "Zoom",
				"direction": "Conference",
				"scope":     "External",
			},
		}),
		mustJSON(t, map[string]any{
			"metaData": map[string]any{
				"id":        "call_theme_internal",
				"title":     "Internal theme evidence",
				"started":   "2026-02-11T15:00:00Z",
				"duration":  1800,
				"system":    "Zoom",
				"direction": "Conference",
				"scope":     "Internal",
			},
		}),
	} {
		if _, err := store.UpsertCall(ctx, raw); err != nil {
			t.Fatalf("upsert theme call: %v", err)
		}
	}
	for _, item := range []struct {
		callID    string
		speakerID string
		text      string
	}{
		{callID: "call_theme_external", speakerID: "buyer-external", text: "The implementation timeline is the main objection."},
		{callID: "call_theme_internal", speakerID: "rep-internal", text: "The implementation timeline is the internal concern."},
	} {
		if _, err := store.UpsertTranscript(ctx, mustJSON(t, map[string]any{
			"callTranscripts": []any{
				map[string]any{
					"callId": item.callID,
					"transcript": []any{
						map[string]any{
							"speakerId": item.speakerID,
							"sentences": []any{
								map[string]any{"start": 1000, "end": 2000, "text": item.text},
							},
						},
					},
				},
			},
		})); err != nil {
			t.Fatalf("upsert theme transcript: %v", err)
		}
	}

	server := NewServer(store, "gongmcp", "test")
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "theme-evidence",
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name": "search_transcripts_by_call_facts",
			"arguments": map[string]any{
				"query":     "implementation",
				"from_date": "2026-01-01",
				"to_date":   "2026-03-31",
				"scope":     "External",
				"limit":     5,
			},
		}),
	}))

	var envelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &envelope); err != nil {
		t.Fatalf("unmarshal tools/call response: %v", err)
	}
	if envelope.Result.IsError {
		t.Fatalf("unexpected tool error: %+v", envelope.Result)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(envelope.Result.Content[0].Text), &rows); err != nil {
		t.Fatalf("unmarshal call-facts transcript payload: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("row count=%d want 1: %+v", len(rows), rows)
	}
	if rows[0]["scope"] != "External" || rows[0]["call_date"] != "2026-02-10" {
		t.Fatalf("unexpected filtered row: %+v", rows[0])
	}
	if excerpt, ok := rows[0]["context_excerpt"].(string); !ok || !strings.Contains(excerpt, "main objection") {
		t.Fatalf("missing bounded context excerpt: %+v", rows[0])
	}
	for _, leaked := range []string{"call_id", "title", "speaker_id", "text"} {
		if _, ok := rows[0][leaked]; ok {
			t.Fatalf("call-facts transcript result leaked %s: %+v", leaked, rows[0])
		}
	}
}

func TestSearchTranscriptQuotesWithAttributionRedactsNamesByDefault(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	ctx := context.Background()

	call := mustJSON(t, map[string]any{
		"id":      "call_attribution_mcp",
		"title":   "Attribution MCP call",
		"started": "2026-02-12T15:00:00Z",
		"context": []any{
			map[string]any{
				"objectType": "Account",
				"id":         "acct_attribution_mcp",
				"fields": []any{
					map[string]any{"name": "Name", "value": "Named Account"},
					map[string]any{"name": "Industry", "value": "Manufacturing"},
				},
			},
			map[string]any{
				"objectType": "Opportunity",
				"id":         "opp_attribution_mcp",
				"fields": []any{
					map[string]any{"name": "Name", "value": "Named Opportunity"},
					map[string]any{"name": "StageName", "value": "Discovery"},
				},
			},
		},
	})
	if _, err := store.UpsertCall(ctx, call); err != nil {
		t.Fatalf("upsert attribution call: %v", err)
	}
	if _, err := store.UpsertTranscript(ctx, mustJSON(t, map[string]any{
		"callTranscripts": []any{
			map[string]any{
				"callId": "call_attribution_mcp",
				"transcript": []any{
					map[string]any{
						"speakerId": "buyer",
						"sentences": []any{
							map[string]any{"start": 1000, "end": 2000, "text": "Implementation timeline is the problem."},
						},
					},
				},
			},
		},
	})); err != nil {
		t.Fatalf("upsert attribution transcript: %v", err)
	}

	server := NewServer(store, "gongmcp", "test")
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "attribution",
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name": "search_transcript_quotes_with_attribution",
			"arguments": map[string]any{
				"query":             "implementation",
				"from_date":         "2026-01-01",
				"to_date":           "2026-03-31",
				"industry":          "Manufacturing",
				"opportunity_stage": "Discovery",
				"limit":             5,
			},
		}),
	}))

	var envelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &envelope); err != nil {
		t.Fatalf("unmarshal tools/call response: %v", err)
	}
	if envelope.Result.IsError {
		t.Fatalf("unexpected tool error: %+v", envelope.Result)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(envelope.Result.Content[0].Text), &rows); err != nil {
		t.Fatalf("unmarshal attribution payload: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("row count=%d want 1: %+v", len(rows), rows)
	}
	for _, leaked := range []string{"call_id", "title", "account_name", "account_website", "opportunity_name", "opportunity_close_date", "opportunity_probability"} {
		if _, ok := rows[0][leaked]; ok {
			t.Fatalf("attribution result leaked %s by default: %+v", leaked, rows[0])
		}
	}
	if rows[0]["account_industry"] != "Manufacturing" || rows[0]["opportunity_stage"] != "Discovery" {
		t.Fatalf("missing safe attribution metadata: %+v", rows[0])
	}
	if rows[0]["participant_status"] == "" || rows[0]["person_title_status"] == "" {
		t.Fatalf("missing person/title status: %+v", rows[0])
	}
	if rows[0]["speaker_role"] != sqlite.SpeakerRoleUnknown || rows[0]["speaker_role_status"] != sqlite.SpeakerRoleStatusSpeakerUnmatched {
		t.Fatalf("missing safe speaker role/status defaults: %+v", rows[0])
	}
	if text, ok := rows[0]["context_excerpt"].(string); !ok || !strings.Contains(text, "Implementation timeline") {
		t.Fatalf("missing bounded context excerpt: %+v", rows[0])
	}
}

func TestSearchTranscriptQuotesRejectsMissingTranscriptStatus(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	server := NewServer(store, "gongmcp", "test")
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "missing-status",
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name": "search_transcript_quotes_with_attribution",
			"arguments": map[string]any{
				"query":             "implementation",
				"transcript_status": "missing",
			},
		}),
	}))

	var envelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &envelope); err != nil {
		t.Fatalf("unmarshal tools/call response: %v", err)
	}
	if !envelope.Result.IsError {
		t.Fatalf("expected tool error, got %+v", envelope.Result)
	}
	if len(envelope.Result.Content) != 1 || !strings.Contains(envelope.Result.Content[0].Text, "transcript_status=missing") {
		t.Fatalf("error did not explain invalid missing transcript status: %+v", envelope.Result)
	}
}

func TestSearchTranscriptQuotesExternalOrUnknownPreservesUnmatchedSpeakers(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	ctx := context.Background()
	call := mustJSON(t, map[string]any{
		"id":      "call_attribution_unknown_role",
		"title":   "Manufacturing implementation discussion",
		"started": "2026-02-10T15:00:00Z",
		"parties": []any{
			map[string]any{
				"id":          "buyer",
				"name":        "Buyer",
				"affiliation": "",
				"email":       "buyer@example.com",
			},
		},
		"context": []any{
			map[string]any{
				"objectType": "Opportunity",
				"id":         "opp-unknown-role",
				"fields": []any{
					map[string]any{"name": "StageName", "value": "Discovery"},
					map[string]any{"name": "Industry", "value": "Manufacturing"},
				},
			},
		},
	})
	if _, err := store.UpsertCall(ctx, call); err != nil {
		t.Fatalf("upsert attribution call: %v", err)
	}
	if _, err := store.UpsertTranscript(ctx, mustJSON(t, map[string]any{
		"callTranscripts": []any{
			map[string]any{
				"callId": "call_attribution_unknown_role",
				"transcript": []any{
					map[string]any{
						"speakerId": "buyer",
						"sentences": []any{
							map[string]any{"start": 1000, "end": 2000, "text": "Implementation timeline is the problem."},
						},
					},
				},
			},
		},
	})); err != nil {
		t.Fatalf("upsert attribution transcript: %v", err)
	}

	server := NewServer(store, "gongmcp", "test")
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "external-or-unknown",
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name": "search_transcript_quotes_with_attribution",
			"arguments": map[string]any{
				"query":        "implementation",
				"speaker_role": "external_or_unknown",
				"from_date":    "2026-02-01",
				"to_date":      "2026-02-28",
				"limit":        5,
			},
		}),
	}))

	var envelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &envelope); err != nil {
		t.Fatalf("unmarshal tools/call response: %v", err)
	}
	if envelope.Result.IsError {
		t.Fatalf("unexpected tool error: %+v", envelope.Result)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(envelope.Result.Content[0].Text), &rows); err != nil {
		t.Fatalf("unmarshal attribution payload: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("external_or_unknown row count=%d want 1: %+v", len(rows), rows)
	}
	if rows[0]["speaker_role"] != sqlite.SpeakerRoleUnknown {
		t.Fatalf("external_or_unknown should preserve unknown speaker role row: %+v", rows[0])
	}
}

func TestSearchTranscriptQuotesWithAttributionAccountQueryRequiresNameOptIn(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	server := NewServer(store, "gongmcp", "test")
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "attribution-account-query",
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name": "search_transcript_quotes_with_attribution",
			"arguments": map[string]any{
				"query":         "implementation",
				"account_query": "Named Account",
				"limit":         5,
			},
		}),
	}))

	var envelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &envelope); err != nil {
		t.Fatalf("unmarshal tools/call response: %v", err)
	}
	if !envelope.Result.IsError {
		t.Fatalf("expected account_query tool error, got %+v", envelope.Result)
	}
	if !strings.Contains(envelope.Result.Content[0].Text, "include_account_names") {
		t.Fatalf("tool error missing opt-in guidance: %+v", envelope.Result)
	}
}

func TestSearchTranscriptQuotesWithAttributionRestrictedAccountQueryDenied(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	server := NewServerWithOptions(store, "gongmcp", "test", WithRestrictedAccountQueryTerms([]string{"NoAI Synthetic Corp"}))
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "attribution-restricted-account-query",
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name": "search_transcript_quotes_with_attribution",
			"arguments": map[string]any{
				"query":                 "implementation",
				"account_query":         "NoAI Synthetic Corp",
				"include_account_names": true,
				"limit":                 5,
			},
		}),
	}))

	var envelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &envelope); err != nil {
		t.Fatalf("unmarshal tools/call response: %v", err)
	}
	if !envelope.Result.IsError {
		t.Fatalf("expected restricted account_query tool error, got %+v", envelope.Result)
	}
	text := envelope.Result.Content[0].Text
	if !strings.Contains(text, "account_query is unavailable") {
		t.Fatalf("tool error missing restricted account guidance: %s", text)
	}
	if strings.Contains(text, "NoAI") || strings.Contains(text, "Synthetic Corp") {
		t.Fatalf("restricted account_query error leaked configured term: %s", text)
	}
}

func TestBusinessAnalysisRestrictedAccountQueryDenied(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	server := NewServerWithOptions(store, "gongmcp", "test", WithRestrictedAccountQueryTerms([]string{"NoAI Synthetic Corp"}))
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "business-restricted-account-query",
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name": "build_call_cohort",
			"arguments": map[string]any{
				"filter": map[string]any{
					"account_query": "NoAI Synthetic Corp",
				},
				"include_account_names": true,
			},
		}),
	}))

	var envelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &envelope); err != nil {
		t.Fatalf("unmarshal tools/call response: %v", err)
	}
	if !envelope.Result.IsError {
		t.Fatalf("expected restricted business account_query error, got %+v", envelope.Result)
	}
	text := envelope.Result.Content[0].Text
	if !strings.Contains(text, "account_query is unavailable") {
		t.Fatalf("tool error missing restricted account guidance: %s", text)
	}
	if strings.Contains(text, "NoAI") || strings.Contains(text, "Synthetic Corp") {
		t.Fatalf("restricted business account_query error leaked configured term: %s", text)
	}
}

func TestBusinessAnalysisRestrictedAccountQueryDeniedInSecondaryFilter(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	server := NewServerWithOptions(store, "gongmcp", "test", WithRestrictedAccountQueryTerms([]string{"NoAI Synthetic Corp"}))
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "business-restricted-secondary-account-query",
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name": "compare_call_cohorts",
			"arguments": map[string]any{
				"filter_a": map[string]any{
					"title_query": "discovery",
				},
				"filter_b": map[string]any{
					"account_query": "NoAI Synthetic Corp",
				},
				"include_account_names": true,
			},
		}),
	}))

	var envelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &envelope); err != nil {
		t.Fatalf("unmarshal tools/call response: %v", err)
	}
	if !envelope.Result.IsError {
		t.Fatalf("expected restricted secondary account_query error, got %+v", envelope.Result)
	}
	text := envelope.Result.Content[0].Text
	if !strings.Contains(text, "account_query is unavailable") {
		t.Fatalf("tool error missing restricted account guidance: %s", text)
	}
	if strings.Contains(text, "NoAI") || strings.Contains(text, "Synthetic Corp") {
		t.Fatalf("restricted secondary account_query error leaked configured term: %s", text)
	}
}

func TestMissingTranscriptsOmittedLimitHonorsConfiguredCap(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	ctx := context.Background()
	for _, id := range []string{"call_missing_cap_1", "call_missing_cap_2"} {
		if _, err := store.UpsertCall(ctx, mustJSON(t, map[string]any{
			"metaData": map[string]any{
				"id":      id,
				"title":   "Missing transcript cap call",
				"started": "2026-02-10T15:00:00Z",
			},
		})); err != nil {
			t.Fatalf("upsert missing transcript call: %v", err)
		}
	}
	policy, err := DefaultLimitPolicy().WithOverride("missing_transcripts", 1)
	if err != nil {
		t.Fatalf("WithOverride returned error: %v", err)
	}

	server := NewServerWithOptions(store, "gongmcp", "test", WithLimitPolicy(policy))
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "missing-cap",
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name":      "missing_transcripts",
			"arguments": map[string]any{},
		}),
	}))

	var envelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &envelope); err != nil {
		t.Fatalf("unmarshal tools/call response: %v", err)
	}
	if envelope.Result.IsError {
		t.Fatalf("unexpected tool error: %+v", envelope.Result)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(envelope.Result.Content[0].Text), &payload); err != nil {
		t.Fatalf("unmarshal cap payload: %v", err)
	}
	if intField(payload, "limit") != 1 || intField(payload, "returned") != 1 {
		t.Fatalf("missing transcript omitted limit did not honor configured cap: %+v", payload)
	}
}

func TestMissingTranscriptsGovernanceOverfetchTrimsToRequestedLimit(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	ctx := context.Background()
	for _, call := range []struct {
		id      string
		title   string
		started string
	}{
		{id: "call_missing_governance_blocked", title: "Blocked missing transcript", started: "2026-02-13T15:00:00Z"},
		{id: "call_missing_governance_allowed_1", title: "Allowed missing transcript one", started: "2026-02-12T15:00:00Z"},
		{id: "call_missing_governance_allowed_2", title: "Allowed missing transcript two", started: "2026-02-11T15:00:00Z"},
	} {
		if _, err := store.UpsertCall(ctx, mustJSON(t, map[string]any{
			"metaData": map[string]any{
				"id":      call.id,
				"title":   call.title,
				"started": call.started,
			},
		})); err != nil {
			t.Fatalf("upsert missing transcript call: %v", err)
		}
	}

	server := NewServerWithOptions(store, "gongmcp", "test", WithSuppressedCallIDs([]string{"call_missing_governance_blocked"}))
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "missing-governance-limit",
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name": "missing_transcripts",
			"arguments": map[string]any{
				"from_date": "2026-02-11",
				"to_date":   "2026-02-13",
				"limit":     1,
			},
		}),
	}))

	var envelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &envelope); err != nil {
		t.Fatalf("unmarshal tools/call response: %v", err)
	}
	if envelope.Result.IsError {
		t.Fatalf("unexpected tool error: %+v", envelope.Result)
	}
	text := envelope.Result.Content[0].Text
	if strings.Contains(text, "call_missing_governance_blocked") {
		t.Fatalf("missing transcripts leaked suppressed call: %s", text)
	}
	if strings.Contains(text, "call_missing_governance_allowed_2") {
		t.Fatalf("missing transcripts returned more than requested limit: %s", text)
	}
	if !strings.Contains(text, "call_missing_governance_allowed_1") {
		t.Fatalf("missing transcripts missed first allowed call after governance overfetch: %s", text)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		t.Fatalf("unmarshal capped payload: %v", err)
	}
	if intField(payload, "limit") != 1 || intField(payload, "returned") != 1 {
		t.Fatalf("governed missing transcript limit mismatch: %+v", payload)
	}
}

func TestGetCallReturnsMinimizedDetail(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	server := NewServer(store, "gongmcp", "test")
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "call-detail",
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name": "get_call",
			"arguments": map[string]any{
				"call_id": "call_extended_001",
			},
		}),
	}))

	var envelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &envelope); err != nil {
		t.Fatalf("unmarshal tools/call response: %v", err)
	}
	if envelope.Result.IsError {
		t.Fatalf("unexpected tool error: %+v", envelope.Result)
	}
	text := envelope.Result.Content[0].Text
	for _, leaked := range []string{"internal@example.invalid", "external@example.invalid", "40000", "125000", "Acme Corp", "Expansion Q2"} {
		if strings.Contains(text, leaked) {
			t.Fatalf("minimized call detail leaked %q in %s", leaked, text)
		}
	}

	var detail callDetail
	if err := json.Unmarshal([]byte(text), &detail); err != nil {
		t.Fatalf("unmarshal call detail: %v", err)
	}
	if detail.CallID != "call_extended_001" || detail.DurationSeconds != 2400 || detail.PartiesCount != 2 {
		t.Fatalf("unexpected call detail: %+v", detail)
	}
	if len(detail.CRMObjects) != 2 {
		t.Fatalf("crm object count=%d want 2", len(detail.CRMObjects))
	}
	for _, object := range detail.CRMObjects {
		if object.ObjectName != "" {
			t.Fatalf("object name leaked in minimized call detail: %+v", object)
		}
		if object.FieldCount == 0 || len(object.FieldNames) == 0 {
			t.Fatalf("object missing field metadata: %+v", object)
		}
	}
}

func TestGetCallAcceptsRedactedCallRef(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	callRef := callRef("call_extended_001")
	server := NewServer(store, "gongmcp", "test")
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "call-detail-ref",
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name": "get_call",
			"arguments": map[string]any{
				"call_ref": callRef,
			},
		}),
	}))

	var envelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &envelope); err != nil {
		t.Fatalf("unmarshal tools/call response: %v", err)
	}
	if envelope.Result.IsError {
		t.Fatalf("unexpected tool error: %+v", envelope.Result)
	}
	text := envelope.Result.Content[0].Text
	if strings.Contains(text, "call_extended_001") {
		t.Fatalf("call_ref lookup echoed raw call_id: %s", text)
	}

	var detail struct {
		CallID          string `json:"call_id"`
		CallRef         string `json:"call_ref"`
		StartedAt       string `json:"started_at"`
		DurationSeconds int64  `json:"duration_seconds"`
	}
	if err := json.Unmarshal([]byte(text), &detail); err != nil {
		t.Fatalf("unmarshal call-ref detail: %v", err)
	}
	if detail.CallID != "" || detail.CallRef != callRef || detail.DurationSeconds != 2400 || detail.StartedAt == "" {
		t.Fatalf("unexpected call-ref detail: %+v", detail)
	}
}

func TestGetCallAcceptsSanitizedBusinessCallRef(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	sum := md5.Sum([]byte("call_extended_001"))
	sanitizedToken := hex.EncodeToString(sum[:])
	callRef := callRef(sanitizedToken)
	server := NewServer(store, "gongmcp", "test")
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "call-detail-sanitized-ref",
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name": "get_call",
			"arguments": map[string]any{
				"call_ref": callRef,
			},
		}),
	}))

	var envelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &envelope); err != nil {
		t.Fatalf("unmarshal tools/call response: %v", err)
	}
	if envelope.Result.IsError {
		t.Fatalf("unexpected tool error: %+v", envelope.Result)
	}
	text := envelope.Result.Content[0].Text
	if strings.Contains(text, "call_extended_001") {
		t.Fatalf("sanitized call_ref lookup echoed raw call_id: %s", text)
	}
	var detail struct {
		CallID          string `json:"call_id"`
		CallRef         string `json:"call_ref"`
		DurationSeconds int64  `json:"duration_seconds"`
	}
	if err := json.Unmarshal([]byte(text), &detail); err != nil {
		t.Fatalf("unmarshal sanitized call-ref detail: %v", err)
	}
	if detail.CallID != "" || detail.CallRef != callRef || detail.DurationSeconds != 2400 {
		t.Fatalf("unexpected sanitized call-ref detail: %+v", detail)
	}
}

func TestSearchCallsSummarizesMetaDataEnvelope(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	raw := mustJSON(t, map[string]any{
		"metaData": map[string]any{
			"id":       "call_metadata_mcp_001",
			"title":    "Metadata MCP call",
			"started":  "2026-04-24T15:00:00Z",
			"duration": 900,
		},
		"context": []any{
			map[string]any{
				"objectType": "Opportunity",
				"id":         "opp_metadata_mcp_001",
				"name":       "Metadata opp",
				"fields": []any{
					map[string]any{"name": "StageName", "value": "Contract Signing"},
				},
			},
		},
	})
	if _, err := store.UpsertCall(context.Background(), raw); err != nil {
		t.Fatalf("upsert metadata call: %v", err)
	}

	server := NewServer(store, "gongmcp", "test")
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "search",
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name": "search_calls",
			"arguments": map[string]any{
				"crm_object_type": "Opportunity",
				"crm_object_id":   "opp_metadata_mcp_001",
				"limit":           5,
			},
		}),
	}))

	var envelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &envelope); err != nil {
		t.Fatalf("unmarshal tools/call response: %v", err)
	}
	var rows []searchCallSummary
	if err := json.Unmarshal([]byte(envelope.Result.Content[0].Text), &rows); err != nil {
		t.Fatalf("unmarshal search rows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("row count=%d want 1", len(rows))
	}
	if rows[0].CallID != "call_metadata_mcp_001" || rows[0].Title != "Metadata MCP call" || rows[0].DurationSeconds != 900 {
		t.Fatalf("unexpected summary: %+v", rows[0])
	}
}

func TestCRMToolsReturnAggregates(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	seedLateStageMCPCall(t, store)

	server := NewServer(store, "gongmcp", "test")
	responses := runServer(t, server,
		requestFrame(Request{
			JSONRPC: "2.0",
			ID:      "objects",
			Method:  "tools/call",
			Params: mustJSON(t, map[string]any{
				"name":      "list_crm_object_types",
				"arguments": map[string]any{},
			}),
		})+
			requestFrame(Request{
				JSONRPC: "2.0",
				ID:      "fields",
				Method:  "tools/call",
				Params: mustJSON(t, map[string]any{
					"name": "list_crm_fields",
					"arguments": map[string]any{
						"object_type": "Opportunity",
						"limit":       5,
					},
				}),
			})+
			requestFrame(Request{
				JSONRPC: "2.0",
				ID:      "values_redacted",
				Method:  "tools/call",
				Params: mustJSON(t, map[string]any{
					"name": "search_crm_field_values",
					"arguments": map[string]any{
						"object_type": "Opportunity",
						"field_name":  "amount",
						"value_query": "40000",
						"limit":       5,
					},
				}),
			})+
			requestFrame(Request{
				JSONRPC: "2.0",
				ID:      "values",
				Method:  "tools/call",
				Params: mustJSON(t, map[string]any{
					"name": "search_crm_field_values",
					"arguments": map[string]any{
						"object_type":            "Opportunity",
						"field_name":             "amount",
						"value_query":            "40000",
						"include_value_snippets": true,
						"limit":                  5,
					},
				}),
			})+
			requestFrame(Request{
				JSONRPC: "2.0",
				ID:      "signals",
				Method:  "tools/call",
				Params: mustJSON(t, map[string]any{
					"name": "analyze_late_stage_crm_signals",
					"arguments": map[string]any{
						"object_type":       "Opportunity",
						"late_stage_values": []string{"Contract Signing"},
						"limit":             5,
					},
				}),
			}))

	if len(responses) != 5 {
		t.Fatalf("response count=%d want 5", len(responses))
	}
	var objectsEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &objectsEnvelope); err != nil {
		t.Fatalf("unmarshal objects response: %v", err)
	}
	var objects []sqlite.CRMObjectTypeSummary
	if err := json.Unmarshal([]byte(objectsEnvelope.Result.Content[0].Text), &objects); err != nil {
		t.Fatalf("unmarshal object summaries: %v", err)
	}
	if len(objects) == 0 {
		t.Fatal("object summaries empty")
	}

	var fieldsEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[1], &fieldsEnvelope); err != nil {
		t.Fatalf("unmarshal fields response: %v", err)
	}
	var fields []sqlite.CRMFieldSummary
	if err := json.Unmarshal([]byte(fieldsEnvelope.Result.Content[0].Text), &fields); err != nil {
		t.Fatalf("unmarshal field summaries: %v", err)
	}
	if len(fields) == 0 {
		t.Fatal("field summaries empty")
	}
	for _, field := range fields {
		if len(field.ExampleValues) != 0 {
			t.Fatalf("list_crm_fields leaked example values: %+v", fields)
		}
	}

	var redactedEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[2], &redactedEnvelope); err != nil {
		t.Fatalf("unmarshal redacted values response: %v", err)
	}
	var redactedValues []sqlite.CRMFieldValueMatch
	if err := json.Unmarshal([]byte(redactedEnvelope.Result.Content[0].Text), &redactedValues); err != nil {
		t.Fatalf("unmarshal redacted value matches: %v", err)
	}
	if len(redactedValues) != 1 || redactedValues[0].CallID != "" || redactedValues[0].ValueSnippet != "" || redactedValues[0].ObjectID != "" || redactedValues[0].ObjectName != "" || redactedValues[0].Title != "" {
		t.Fatalf("default value search leaked value/object details: %+v", redactedValues)
	}

	var valuesEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[3], &valuesEnvelope); err != nil {
		t.Fatalf("unmarshal values response: %v", err)
	}
	var values []sqlite.CRMFieldValueMatch
	if err := json.Unmarshal([]byte(valuesEnvelope.Result.Content[0].Text), &values); err != nil {
		t.Fatalf("unmarshal value matches: %v", err)
	}
	if len(values) != 1 || values[0].ValueSnippet != "40000" || values[0].Title == "" || values[0].CallID != "" {
		t.Fatalf("unexpected value matches: %+v", values)
	}
	if values[0].ObjectID != "" || values[0].ObjectName != "" {
		t.Fatalf("value search leaked object details: %+v", values[0])
	}

	withIDsResponses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "values_with_ids",
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name": "search_crm_field_values",
			"arguments": map[string]any{
				"object_type":            "Opportunity",
				"field_name":             "amount",
				"value_query":            "40000",
				"include_value_snippets": true,
				"include_call_ids":       true,
				"limit":                  5,
			},
		}),
	}))

	var withIDsEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(withIDsResponses[0], &withIDsEnvelope); err != nil {
		t.Fatalf("unmarshal values_with_ids response: %v", err)
	}
	var withIDs []sqlite.CRMFieldValueMatch
	if err := json.Unmarshal([]byte(withIDsEnvelope.Result.Content[0].Text), &withIDs); err != nil {
		t.Fatalf("unmarshal value matches with ids: %v", err)
	}
	if len(withIDs) != 1 || withIDs[0].CallID == "" || withIDs[0].ValueSnippet == "" || withIDs[0].Title == "" {
		t.Fatalf("expected opt-in call identifiers and snippets: %+v", withIDs)
	}
	if withIDs[0].ObjectID != "" || withIDs[0].ObjectName != "" {
		t.Fatalf("value search leaked object details with ids: %+v", withIDs[0])
	}

	var signalsEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[4], &signalsEnvelope); err != nil {
		t.Fatalf("unmarshal signals response: %v", err)
	}
	var report sqlite.LateStageSignalsReport
	if err := json.Unmarshal([]byte(signalsEnvelope.Result.Content[0].Text), &report); err != nil {
		t.Fatalf("unmarshal signal report: %v", err)
	}
	if report.ObjectType != "Opportunity" || report.LateCalls == 0 || len(report.Signals) == 0 {
		t.Fatalf("unexpected signal report: %+v", report)
	}
}

func TestInventoryToolsReturnCachedMetadata(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	seedInventoryMCPFixtures(t, store)

	server := NewServer(store, "gongmcp", "test")
	responses := runServer(t, server,
		requestFrame(Request{
			JSONRPC: "2.0",
			ID:      "integrations",
			Method:  "tools/call",
			Params: mustJSON(t, map[string]any{
				"name":      "list_crm_integrations",
				"arguments": map[string]any{},
			}),
		})+
			requestFrame(Request{
				JSONRPC: "2.0",
				ID:      "schema-objects",
				Method:  "tools/call",
				Params: mustJSON(t, map[string]any{
					"name": "list_cached_crm_schema_objects",
					"arguments": map[string]any{
						"integration_id": "crm-int-001",
					},
				}),
			})+
			requestFrame(Request{
				JSONRPC: "2.0",
				ID:      "schema-fields",
				Method:  "tools/call",
				Params: mustJSON(t, map[string]any{
					"name": "list_cached_crm_schema_fields",
					"arguments": map[string]any{
						"integration_id": "crm-int-001",
						"object_type":    "DEAL",
						"limit":          10,
					},
				}),
			})+
			requestFrame(Request{
				JSONRPC: "2.0",
				ID:      "settings",
				Method:  "tools/call",
				Params: mustJSON(t, map[string]any{
					"name": "list_gong_settings",
					"arguments": map[string]any{
						"kind":  "trackers",
						"limit": 10,
					},
				}),
			})+
			requestFrame(Request{
				JSONRPC: "2.0",
				ID:      "scorecards",
				Method:  "tools/call",
				Params: mustJSON(t, map[string]any{
					"name": "list_scorecards",
					"arguments": map[string]any{
						"limit": 10,
					},
				}),
			})+
			requestFrame(Request{
				JSONRPC: "2.0",
				ID:      "scorecard-detail",
				Method:  "tools/call",
				Params: mustJSON(t, map[string]any{
					"name": "get_scorecard",
					"arguments": map[string]any{
						"scorecard_id": "scorecard-001",
					},
				}),
			}))

	if len(responses) != 6 {
		t.Fatalf("response count=%d want 6", len(responses))
	}

	var integrationsEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &integrationsEnvelope); err != nil {
		t.Fatalf("unmarshal integrations response: %v", err)
	}
	var integrations []sqlite.CRMIntegrationRecord
	if err := json.Unmarshal([]byte(integrationsEnvelope.Result.Content[0].Text), &integrations); err != nil {
		t.Fatalf("unmarshal integrations: %v", err)
	}
	if len(integrations) != 1 || integrations[0].IntegrationID != "crm-int-001" {
		t.Fatalf("unexpected integrations: %+v", integrations)
	}

	var objectsEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[1], &objectsEnvelope); err != nil {
		t.Fatalf("unmarshal schema objects response: %v", err)
	}
	var objects []sqlite.CRMSchemaObjectRecord
	if err := json.Unmarshal([]byte(objectsEnvelope.Result.Content[0].Text), &objects); err != nil {
		t.Fatalf("unmarshal schema objects: %v", err)
	}
	if len(objects) != 1 || objects[0].ObjectType != "DEAL" || objects[0].FieldCount != 2 {
		t.Fatalf("unexpected schema objects: %+v", objects)
	}

	var fieldsEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[2], &fieldsEnvelope); err != nil {
		t.Fatalf("unmarshal schema fields response: %v", err)
	}
	var fields []sqlite.CRMSchemaFieldRecord
	if err := json.Unmarshal([]byte(fieldsEnvelope.Result.Content[0].Text), &fields); err != nil {
		t.Fatalf("unmarshal schema fields: %v", err)
	}
	if len(fields) != 2 || fields[0].FieldName != "Amount" {
		t.Fatalf("unexpected schema fields: %+v", fields)
	}
	if strings.Contains(fieldsEnvelope.Result.Content[0].Text, "raw_json") {
		t.Fatalf("schema field tool exposed raw payload: %s", fieldsEnvelope.Result.Content[0].Text)
	}

	var settingsEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[3], &settingsEnvelope); err != nil {
		t.Fatalf("unmarshal settings response: %v", err)
	}
	var settings []sqlite.GongSettingRecord
	if err := json.Unmarshal([]byte(settingsEnvelope.Result.Content[0].Text), &settings); err != nil {
		t.Fatalf("unmarshal settings: %v", err)
	}
	if len(settings) != 1 || settings[0].ObjectID != "tracker-001" || !settings[0].Active {
		t.Fatalf("unexpected settings: %+v", settings)
	}

	var scorecardsEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[4], &scorecardsEnvelope); err != nil {
		t.Fatalf("unmarshal scorecards response: %v", err)
	}
	var scorecards []sqlite.ScorecardSummary
	if err := json.Unmarshal([]byte(scorecardsEnvelope.Result.Content[0].Text), &scorecards); err != nil {
		t.Fatalf("unmarshal scorecards: %v", err)
	}
	if len(scorecards) != 1 || scorecards[0].Name != "Discovery quality" || scorecards[0].QuestionCount != 1 {
		t.Fatalf("unexpected scorecards: %+v", scorecards)
	}

	var detailEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[5], &detailEnvelope); err != nil {
		t.Fatalf("unmarshal scorecard detail response: %v", err)
	}
	var detail sqlite.ScorecardDetail
	if err := json.Unmarshal([]byte(detailEnvelope.Result.Content[0].Text), &detail); err != nil {
		t.Fatalf("unmarshal scorecard detail: %v", err)
	}
	if len(detail.Questions) != 1 || detail.Questions[0].QuestionText != "Did the rep confirm pain?" {
		t.Fatalf("unexpected scorecard detail: %+v", detail)
	}
}

func TestOpportunityAggregateMCPTools(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	seedOpportunityAggregateMCPFixtures(t, store)

	server := NewServer(store, "gongmcp", "test")
	responses := runServer(t, server,
		requestFrame(Request{
			JSONRPC: "2.0",
			ID:      "missing-opps",
			Method:  "tools/call",
			Params: mustJSON(t, map[string]any{
				"name": "opportunities_missing_transcripts",
				"arguments": map[string]any{
					"stage_values": []string{"Contract Signing"},
					"limit":        5,
				},
			}),
		})+
			requestFrame(Request{
				JSONRPC: "2.0",
				ID:      "crm-transcripts",
				Method:  "tools/call",
				Params: mustJSON(t, map[string]any{
					"name": "search_transcripts_by_crm_context",
					"arguments": map[string]any{
						"query":       "pricing",
						"object_type": "Opportunity",
						"object_id":   "opp_mcp_gap_001",
						"limit":       5,
					},
				}),
			})+
			requestFrame(Request{
				JSONRPC: "2.0",
				ID:      "opp-summary",
				Method:  "tools/call",
				Params: mustJSON(t, map[string]any{
					"name": "opportunity_call_summary",
					"arguments": map[string]any{
						"stage_values": []string{"Contract Signing"},
						"limit":        5,
					},
				}),
			})+
			requestFrame(Request{
				JSONRPC: "2.0",
				ID:      "matrix",
				Method:  "tools/call",
				Params: mustJSON(t, map[string]any{
					"name": "crm_field_population_matrix",
					"arguments": map[string]any{
						"object_type":    "Opportunity",
						"group_by_field": "StageName",
						"limit":          20,
					},
				}),
			}))

	if len(responses) != 4 {
		t.Fatalf("response count=%d want 4", len(responses))
	}

	var missingEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &missingEnvelope); err != nil {
		t.Fatalf("unmarshal missing response: %v", err)
	}
	var missing []sqlite.OpportunityMissingTranscriptSummary
	if err := json.Unmarshal([]byte(missingEnvelope.Result.Content[0].Text), &missing); err != nil {
		t.Fatalf("unmarshal missing opportunities: %v", err)
	}
	if len(missing) != 1 || missing[0].OpportunityID != "" || missing[0].OpportunityName != "" || missing[0].LatestCallID != "" || missing[0].CallCount != 2 || missing[0].MissingTranscriptCount != 1 || missing[0].TranscriptCount != 1 {
		t.Fatalf("unexpected missing opportunity rows: %+v", missing)
	}

	var transcriptEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[1], &transcriptEnvelope); err != nil {
		t.Fatalf("unmarshal transcript response: %v", err)
	}
	var transcriptRows []sqlite.TranscriptCRMSearchResult
	if err := json.Unmarshal([]byte(transcriptEnvelope.Result.Content[0].Text), &transcriptRows); err != nil {
		t.Fatalf("unmarshal transcript CRM rows: %v", err)
	}
	if len(transcriptRows) != 1 || transcriptRows[0].CallID != "" || transcriptRows[0].Title != "" || transcriptRows[0].ObjectID != "" || transcriptRows[0].ObjectName != "" || transcriptRows[0].SpeakerID != "" {
		t.Fatalf("unexpected transcript CRM rows: %+v", transcriptRows)
	}
	if _, ok := mapFromJSONText(t, transcriptEnvelope.Result.Content[0].Text, 0)["text"]; ok {
		t.Fatalf("raw text leaked in transcript CRM result: %s", transcriptEnvelope.Result.Content[0].Text)
	}

	var summaryEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[2], &summaryEnvelope); err != nil {
		t.Fatalf("unmarshal opportunity summary response: %v", err)
	}
	var summaries []sqlite.OpportunityCallSummary
	if err := json.Unmarshal([]byte(summaryEnvelope.Result.Content[0].Text), &summaries); err != nil {
		t.Fatalf("unmarshal opportunity summaries: %v", err)
	}
	if len(summaries) != 1 || summaries[0].OpportunityID != "" || summaries[0].OpportunityName != "" || summaries[0].LatestCallID != "" || summaries[0].Amount != "" || summaries[0].CloseDate != "" || summaries[0].OwnerID != "" || summaries[0].TotalDurationSeconds != 1800 {
		t.Fatalf("unexpected opportunity summaries: %+v", summaries)
	}

	var matrixEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[3], &matrixEnvelope); err != nil {
		t.Fatalf("unmarshal matrix response: %v", err)
	}
	var matrix sqlite.CRMFieldPopulationMatrix
	if err := json.Unmarshal([]byte(matrixEnvelope.Result.Content[0].Text), &matrix); err != nil {
		t.Fatalf("unmarshal matrix: %v", err)
	}
	if matrix.ObjectType != "Opportunity" || len(matrix.Cells) == 0 {
		t.Fatalf("unexpected matrix: %+v", matrix)
	}
}

func TestLifecycleMCPTools(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	seedLifecycleMCPFixtures(t, store)

	server := NewServer(store, "gongmcp", "test")
	responses := runServer(t, server,
		requestFrame(Request{
			JSONRPC: "2.0",
			ID:      "buckets",
			Method:  "tools/call",
			Params: mustJSON(t, map[string]any{
				"name":      "list_lifecycle_buckets",
				"arguments": map[string]any{},
			}),
		})+
			requestFrame(Request{
				JSONRPC: "2.0",
				ID:      "summary",
				Method:  "tools/call",
				Params: mustJSON(t, map[string]any{
					"name": "summarize_calls_by_lifecycle",
					"arguments": map[string]any{
						"bucket": "renewal",
					},
				}),
			})+
			requestFrame(Request{
				JSONRPC: "2.0",
				ID:      "search",
				Method:  "tools/call",
				Params: mustJSON(t, map[string]any{
					"name": "search_calls_by_lifecycle",
					"arguments": map[string]any{
						"bucket":                   "upsell_expansion",
						"missing_transcripts_only": true,
						"limit":                    5,
					},
				}),
			})+
			requestFrame(Request{
				JSONRPC: "2.0",
				ID:      "priority",
				Method:  "tools/call",
				Params: mustJSON(t, map[string]any{
					"name": "prioritize_transcripts_by_lifecycle",
					"arguments": map[string]any{
						"limit": 5,
					},
				}),
			})+
			requestFrame(Request{
				JSONRPC: "2.0",
				ID:      "compare",
				Method:  "tools/call",
				Params: mustJSON(t, map[string]any{
					"name": "compare_lifecycle_crm_fields",
					"arguments": map[string]any{
						"bucket_a":    "renewal",
						"bucket_b":    "active_sales_pipeline",
						"object_type": "Opportunity",
						"limit":       10,
					},
				}),
			})+
			requestFrame(Request{
				JSONRPC: "2.0",
				ID:      "facts",
				Method:  "tools/call",
				Params: mustJSON(t, map[string]any{
					"name": "summarize_call_facts",
					"arguments": map[string]any{
						"group_by": "lifecycle",
						"limit":    10,
					},
				}),
			})+
			requestFrame(Request{
				JSONRPC: "2.0",
				ID:      "backlog",
				Method:  "tools/call",
				Params: mustJSON(t, map[string]any{
					"name": "rank_transcript_backlog",
					"arguments": map[string]any{
						"limit": 5,
					},
				}),
			}))

	if len(responses) != 7 {
		t.Fatalf("response count=%d want 7", len(responses))
	}

	var bucketsEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &bucketsEnvelope); err != nil {
		t.Fatalf("unmarshal buckets response: %v", err)
	}
	var buckets []sqlite.LifecycleBucketDefinition
	if err := json.Unmarshal([]byte(bucketsEnvelope.Result.Content[0].Text), &buckets); err != nil {
		t.Fatalf("unmarshal lifecycle buckets: %v", err)
	}
	if len(buckets) == 0 || buckets[0].Bucket != "outbound_prospecting" {
		t.Fatalf("unexpected lifecycle buckets: %+v", buckets)
	}

	var summaryEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[1], &summaryEnvelope); err != nil {
		t.Fatalf("unmarshal summary response: %v", err)
	}
	var summaries []sqlite.LifecycleBucketSummary
	if err := json.Unmarshal([]byte(summaryEnvelope.Result.Content[0].Text), &summaries); err != nil {
		t.Fatalf("unmarshal lifecycle summaries: %v", err)
	}
	if len(summaries) != 1 || summaries[0].Bucket != "renewal" || summaries[0].CallCount != 1 || summaries[0].LatestCallID != "" {
		t.Fatalf("unexpected lifecycle summary: %+v", summaries)
	}

	var searchEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[2], &searchEnvelope); err != nil {
		t.Fatalf("unmarshal search response: %v", err)
	}
	var searchRows []sqlite.LifecycleCallSearchResult
	if err := json.Unmarshal([]byte(searchEnvelope.Result.Content[0].Text), &searchRows); err != nil {
		t.Fatalf("unmarshal lifecycle search rows: %v", err)
	}
	if len(searchRows) != 1 || searchRows[0].CallID != "call_mcp_lifecycle_upsell" || searchRows[0].TranscriptPresent {
		t.Fatalf("unexpected lifecycle search rows: %+v", searchRows)
	}

	var priorityEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[3], &priorityEnvelope); err != nil {
		t.Fatalf("unmarshal priority response: %v", err)
	}
	var priorities []sqlite.LifecycleTranscriptPriority
	if err := json.Unmarshal([]byte(priorityEnvelope.Result.Content[0].Text), &priorities); err != nil {
		t.Fatalf("unmarshal lifecycle priorities: %v", err)
	}
	if len(priorities) == 0 || priorities[0].Bucket == "" || priorities[0].PriorityScore <= 0 {
		t.Fatalf("unexpected lifecycle priorities: %+v", priorities)
	}
	if priorities[0].CallID != "" || priorities[0].Title != "" {
		t.Fatalf("MCP priority rows should redact call IDs/titles by default: %+v", priorities[0])
	}

	var compareEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[4], &compareEnvelope); err != nil {
		t.Fatalf("unmarshal compare response: %v", err)
	}
	var comparison sqlite.LifecycleCRMFieldComparison
	if err := json.Unmarshal([]byte(compareEnvelope.Result.Content[0].Text), &comparison); err != nil {
		t.Fatalf("unmarshal lifecycle comparison: %v", err)
	}
	if comparison.BucketA != "renewal" || comparison.BucketB != "active_sales_pipeline" || len(comparison.Fields) == 0 {
		t.Fatalf("unexpected lifecycle comparison: %+v", comparison)
	}

	var factsEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[5], &factsEnvelope); err != nil {
		t.Fatalf("unmarshal facts response: %v", err)
	}
	var factRows []sqlite.CallFactsSummaryRow
	if err := json.Unmarshal([]byte(factsEnvelope.Result.Content[0].Text), &factRows); err != nil {
		t.Fatalf("unmarshal call facts rows: %v", err)
	}
	if len(factRows) == 0 || factRows[0].GroupBy != "lifecycle" {
		t.Fatalf("unexpected call facts rows: %+v", factRows)
	}

	var backlogEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[6], &backlogEnvelope); err != nil {
		t.Fatalf("unmarshal backlog response: %v", err)
	}
	var backlog []sqlite.LifecycleTranscriptPriority
	if err := json.Unmarshal([]byte(backlogEnvelope.Result.Content[0].Text), &backlog); err != nil {
		t.Fatalf("unmarshal backlog rows: %v", err)
	}
	if len(backlog) == 0 || backlog[0].PriorityScore <= 0 {
		t.Fatalf("unexpected backlog rows: %+v", backlog)
	}
	if backlog[0].CallID != "" || backlog[0].Title != "" {
		t.Fatalf("MCP backlog rows should redact call IDs/titles by default: %+v", backlog[0])
	}
}

func TestBusinessProfileMCPTools(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	seedBusinessProfileMCPFixtures(t, store)

	server := NewServer(store, "gongmcp", "test")
	responses := runServer(t, server,
		requestFrame(Request{
			JSONRPC: "2.0",
			ID:      "profile",
			Method:  "tools/call",
			Params: mustJSON(t, map[string]any{
				"name":      "get_business_profile",
				"arguments": map[string]any{},
			}),
		})+
			requestFrame(Request{
				JSONRPC: "2.0",
				ID:      "concepts",
				Method:  "tools/call",
				Params: mustJSON(t, map[string]any{
					"name":      "list_business_concepts",
					"arguments": map[string]any{},
				}),
			})+
			requestFrame(Request{
				JSONRPC: "2.0",
				ID:      "facts",
				Method:  "tools/call",
				Params: mustJSON(t, map[string]any{
					"name": "summarize_call_facts",
					"arguments": map[string]any{
						"lifecycle_source": "profile",
						"group_by":         "deal_stage",
						"limit":            10,
					},
				}),
			})+
			requestFrame(Request{
				JSONRPC: "2.0",
				ID:      "unmapped",
				Method:  "tools/call",
				Params: mustJSON(t, map[string]any{
					"name": "list_unmapped_crm_fields",
					"arguments": map[string]any{
						"limit": 10,
					},
				}),
			})+
			requestFrame(Request{
				JSONRPC: "2.0",
				ID:      "unsafe_group",
				Method:  "tools/call",
				Params: mustJSON(t, map[string]any{
					"name": "summarize_call_facts",
					"arguments": map[string]any{
						"lifecycle_source": "profile",
						"group_by":         "secret_id",
						"limit":            10,
					},
				}),
			}))

	if len(responses) != 5 {
		t.Fatalf("response count=%d want 5", len(responses))
	}
	var profileEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &profileEnvelope); err != nil {
		t.Fatalf("unmarshal profile response: %v", err)
	}
	var businessProfile sqlite.BusinessProfile
	if err := json.Unmarshal([]byte(profileEnvelope.Result.Content[0].Text), &businessProfile); err != nil {
		t.Fatalf("unmarshal business profile: %v", err)
	}
	if businessProfile.CanonicalSHA256 != "" || businessProfile.SourceSHA256 != "" || businessProfile.SourcePath != "" || businessProfile.ImportedBy != "redacted" {
		t.Fatalf("unexpected business profile: %+v", businessProfile)
	}

	var conceptsEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[1], &conceptsEnvelope); err != nil {
		t.Fatalf("unmarshal concepts response: %v", err)
	}
	var concepts []sqlite.BusinessConcept
	if err := json.Unmarshal([]byte(conceptsEnvelope.Result.Content[0].Text), &concepts); err != nil {
		t.Fatalf("unmarshal concepts: %v", err)
	}
	if len(concepts) == 0 {
		t.Fatal("business concepts empty")
	}

	var factsEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[2], &factsEnvelope); err != nil {
		t.Fatalf("unmarshal facts response: %v", err)
	}
	var factsResponse struct {
		LifecycleSource string                       `json:"lifecycle_source"`
		Profile         *sqlite.BusinessProfile      `json:"profile"`
		Results         []sqlite.CallFactsSummaryRow `json:"results"`
	}
	if err := json.Unmarshal([]byte(factsEnvelope.Result.Content[0].Text), &factsResponse); err != nil {
		t.Fatalf("unmarshal profile facts: %v", err)
	}
	if factsResponse.LifecycleSource != sqlite.LifecycleSourceProfile || factsResponse.Profile == nil {
		t.Fatalf("unexpected facts envelope: %+v", factsResponse)
	}
	foundProposal := false
	for _, row := range factsResponse.Results {
		if row.GroupValue == "Proposal" {
			foundProposal = true
		}
	}
	if !foundProposal {
		t.Fatalf("profile facts missing Proposal group: %+v", factsResponse.Results)
	}

	var unmappedEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[3], &unmappedEnvelope); err != nil {
		t.Fatalf("unmarshal unmapped response: %v", err)
	}
	if strings.Contains(unmappedEnvelope.Result.Content[0].Text, "10000") {
		t.Fatalf("unmapped fields leaked raw value: %s", unmappedEnvelope.Result.Content[0].Text)
	}
	var unmapped []sqlite.UnmappedCRMField
	if err := json.Unmarshal([]byte(unmappedEnvelope.Result.Content[0].Text), &unmapped); err != nil {
		t.Fatalf("unmarshal unmapped fields: %v", err)
	}
	if len(unmapped) == 0 {
		t.Fatal("unmapped fields empty")
	}
	var unsafeEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[4], &unsafeEnvelope); err != nil {
		t.Fatalf("unmarshal unsafe group response: %v", err)
	}
	if !strings.Contains(unsafeEnvelope.Result.Content[0].Text, "unsupported MCP group_by") || strings.Contains(unsafeEnvelope.Result.Content[0].Text, "tenant-secret") {
		t.Fatalf("unexpected unsafe group response: %s", unsafeEnvelope.Result.Content[0].Text)
	}
}

func TestBusinessProfileMCPNoActiveProfileError(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	server := NewServer(store, "gongmcp", "test")
	responses := runServer(t, server,
		requestFrame(Request{
			JSONRPC: "2.0",
			ID:      "profile",
			Method:  "tools/call",
			Params: mustJSON(t, map[string]any{
				"name":      "get_business_profile",
				"arguments": map[string]any{},
			}),
		})+
			requestFrame(Request{
				JSONRPC: "2.0",
				ID:      "concepts",
				Method:  "tools/call",
				Params: mustJSON(t, map[string]any{
					"name":      "list_business_concepts",
					"arguments": map[string]any{},
				}),
			})+
			requestFrame(Request{
				JSONRPC: "2.0",
				ID:      "unmapped",
				Method:  "tools/call",
				Params: mustJSON(t, map[string]any{
					"name": "list_unmapped_crm_fields",
					"arguments": map[string]any{
						"limit": 5,
					},
				}),
			}))
	if len(responses) != 3 {
		t.Fatalf("response count=%d want 3", len(responses))
	}
	for _, response := range responses {
		var envelope struct {
			Result toolCallResult `json:"result"`
		}
		if err := json.Unmarshal(response, &envelope); err != nil {
			t.Fatalf("unmarshal no-profile response: %v", err)
		}
		text := envelope.Result.Content[0].Text
		if !strings.Contains(text, "run gongctl profile discover, validate, and import first") || strings.Contains(text, "sql: no rows") {
			t.Fatalf("unexpected no-profile error: %s", text)
		}
	}
}

func TestReadFrameRejectsOversizedContentLength(t *testing.T) {
	t.Parallel()

	input := "Content-Length: " + strconv.Itoa(maxFrameBytes+1) + "\r\n\r\n"
	_, err := readFrame(bufio.NewReader(strings.NewReader(input)))
	if err == nil {
		t.Fatal("readFrame returned nil error for oversized frame")
	}
	if !strings.Contains(err.Error(), "exceeds maximum") {
		t.Fatalf("error=%q want exceeds maximum", err)
	}
}

func TestJSONTextRejectsOversizedToolResult(t *testing.T) {
	t.Parallel()

	_, err := jsonText(strings.Repeat("x", maxToolResultBytes+1))
	if err == nil {
		t.Fatal("jsonText returned nil error for oversized tool result")
	}
	if !strings.Contains(err.Error(), "tool result exceeds maximum") {
		t.Fatalf("error=%q want tool result exceeds maximum", err)
	}
}

func TestToolResultEnvelopeRejectsDoubleEncodedOverflow(t *testing.T) {
	t.Parallel()

	itemCount := 200000
	largeRaw := json.RawMessage(`{"id":"large-double-encoded","items":[` + strings.TrimRight(strings.Repeat(`"x",`, itemCount), ",") + `]}`)
	if !json.Valid(largeRaw) {
		t.Fatal("test raw JSON is invalid")
	}
	if len(largeRaw) > maxToolResultBytes {
		t.Fatalf("raw JSON size=%d should be under pre-envelope cap %d", len(largeRaw), maxToolResultBytes)
	}
	if _, err := jsonText(largeRaw); err != nil {
		t.Fatalf("jsonText unexpectedly rejected raw JSON before envelope check: %v", err)
	}

	err := ensureToolResultFits("large-call", toolCallResult{
		Content: []toolContent{{Type: "text", Text: string(largeRaw)}},
	})
	if err == nil {
		t.Fatal("ensureToolResultFits allowed double-encoded response overflow")
	}
	if !strings.Contains(err.Error(), "after MCP framing") {
		t.Fatalf("error=%q want after MCP framing", err)
	}
}

func TestWriteFrameRejectsOversizedResponseEnvelope(t *testing.T) {
	t.Parallel()

	err := writeFrame(io.Discard, response{
		JSONRPC: "2.0",
		ID:      "large",
		Result:  strings.Repeat("x", maxFrameBytes),
	})
	if err == nil {
		t.Fatal("writeFrame returned nil error for oversized response")
	}
	if !strings.Contains(err.Error(), "response frame exceeds maximum") {
		t.Fatalf("error=%q want response frame exceeds maximum", err)
	}
}

func TestServeHTTPHandlesInitializeAndNotifications(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	server := NewServer(store, "gongmcp", "test")
	request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content-type=%q want application/json", got)
	}
	if !strings.Contains(recorder.Body.String(), `"protocolVersion":"2025-11-25"`) {
		t.Fatalf("body=%q missing initialize result", recorder.Body.String())
	}

	notification := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`))
	notificationRecorder := httptest.NewRecorder()
	server.ServeHTTP(notificationRecorder, notification)
	if notificationRecorder.Code != http.StatusAccepted {
		t.Fatalf("notification status=%d body=%q", notificationRecorder.Code, notificationRecorder.Body.String())
	}
	if notificationRecorder.Body.Len() != 0 {
		t.Fatalf("notification body=%q want empty", notificationRecorder.Body.String())
	}
}

func TestServeHTTPRejectsUnsupportedMethodPathAndOversizedBody(t *testing.T) {
	t.Parallel()

	server := NewServer(nil, "gongmcp", "test")

	notFound := httptest.NewRecorder()
	server.ServeHTTP(notFound, httptest.NewRequest(http.MethodPost, "/not-mcp", strings.NewReader(`{}`)))
	if notFound.Code != http.StatusNotFound {
		t.Fatalf("not-found status=%d want 404", notFound.Code)
	}

	get := httptest.NewRecorder()
	server.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/mcp", nil))
	if get.Code != http.StatusMethodNotAllowed {
		t.Fatalf("get status=%d want 405", get.Code)
	}

	oversized := httptest.NewRecorder()
	server.ServeHTTP(oversized, httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(strings.Repeat("x", maxFrameBytes+1))))
	if oversized.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized status=%d want 413", oversized.Code)
	}
}

func TestServeHTTPReturnsJSONRPCParseErrorForInvalidJSON(t *testing.T) {
	t.Parallel()

	server := NewServer(nil, "gongmcp", "test")
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{invalid`)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"code":-32700`) {
		t.Fatalf("body=%q missing parse error envelope", recorder.Body.String())
	}
}

func openSeededStore(t *testing.T) *sqlite.Store {
	t.Helper()

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "gongmcp.db")
	store, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}

	callFixture := loadCallFixture(t)
	if _, err := store.UpsertCall(ctx, callFixture); err != nil {
		t.Fatalf("upsert call: %v", err)
	}
	extendedCallFixture := loadFixture(t, "internal/store/sqlite/testdata/call.extended.sample.json")
	if _, err := store.UpsertCall(ctx, extendedCallFixture); err != nil {
		t.Fatalf("upsert extended call: %v", err)
	}

	transcriptFixture := loadFixture(t, "testdata/fixtures/transcript.sample.json")
	if _, err := store.UpsertTranscript(ctx, transcriptFixture); err != nil {
		t.Fatalf("upsert transcript: %v", err)
	}

	return store
}

func assertBusinessAnalysisSchemaIsSafe(t *testing.T, item tool) {
	t.Helper()

	if got := item.InputSchema["additionalProperties"]; got != false {
		t.Fatalf("%s schema additionalProperties=%v want false", item.Name, got)
	}
	raw := strings.ToLower(mustJSONText(t, item.InputSchema))
	for _, unsafe := range []string{"sql", "table_name", "column_name", "raw_identifier"} {
		if strings.Contains(raw, unsafe) {
			t.Fatalf("%s schema exposes unsafe surface %q: %s", item.Name, unsafe, raw)
		}
	}

	filterRequired := map[string]bool{
		"build_call_cohort":                        true,
		"search_calls_by_filters":                  true,
		"summarize_calls_by_filters":               true,
		"search_transcripts_by_filters":            true,
		"extract_theme_quotes":                     true,
		"compare_theme_outcomes":                   true,
		"build_theme_brief":                        true,
		"score_cohort_evidence_quality":            true,
		"explain_analysis_limitations":             true,
		"suggest_filter_refinements":               true,
		"diagnose_attribution_coverage":            true,
		"generate_sales_hooks_from_themes":         true,
		"generate_outreach_sequence_inputs":        true,
		"recommend_target_personas_and_industries": true,
	}
	if !filterRequired[item.Name] {
		return
	}
	props := schemaProperties(t, item.InputSchema)
	filter, ok := props["filter"].(map[string]any)
	if !ok {
		t.Fatalf("%s schema missing filter object in %+v", item.Name, item.InputSchema)
	}
	filterProps := schemaProperties(t, filter)
	for _, field := range []string{
		"title_query",
		"query",
		"from_date",
		"to_date",
		"quarter",
		"lifecycle_bucket",
		"scope",
		"system",
		"direction",
		"transcript_status",
		"industry",
		"account_query",
		"opportunity_stage",
		"crm_object_type",
		"crm_object_id",
		"participant_title_query",
		"limit",
	} {
		if _, ok := filterProps[field]; !ok {
			t.Fatalf("%s filter schema missing %s in %+v", item.Name, field, filterProps)
		}
	}
}

func schemaProperties(t *testing.T, schema map[string]any) map[string]any {
	t.Helper()

	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema missing properties: %+v", schema)
	}
	return props
}

func decodeBusinessAnalysisResult(t *testing.T, raw []byte) map[string]any {
	t.Helper()

	var envelope struct {
		Result toolCallResult `json:"result"`
		Error  *responseError `json:"error"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("unmarshal JSON-RPC response: %v", err)
	}
	if envelope.Error != nil {
		t.Fatalf("unexpected JSON-RPC error: %+v", envelope.Error)
	}
	if envelope.Result.IsError {
		t.Fatalf("unexpected tool error: %+v", envelope.Result)
	}
	if len(envelope.Result.Content) != 1 {
		t.Fatalf("content count=%d want 1", len(envelope.Result.Content))
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(envelope.Result.Content[0].Text), &result); err != nil {
		t.Fatalf("unmarshal business-analysis result %q: %v", envelope.Result.Content[0].Text, err)
	}
	return result
}

func mapField(t *testing.T, values map[string]any, key string) map[string]any {
	t.Helper()

	item, ok := values[key].(map[string]any)
	if !ok {
		t.Fatalf("%s is not an object in %+v", key, values)
	}
	return item
}

func arrayField(t *testing.T, values map[string]any, key string) []any {
	t.Helper()

	item, ok := values[key].([]any)
	if !ok {
		t.Fatalf("%s is not an array in %+v", key, values)
	}
	return item
}

func namedOrResultsArrayField(t *testing.T, values map[string]any, key string) []any {
	t.Helper()

	if item, ok := values[key].([]any); ok {
		return item
	}
	return arrayField(t, values, "results")
}

func schemaLimitMaximum(t *testing.T, schema map[string]any, field string) int {
	t.Helper()

	properties := mapField(t, schema, "properties")
	limitSchema := mapField(t, properties, field)
	return intField(limitSchema, "maximum")
}

func stringField(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return value
}

func nestedString(values map[string]any, objectKey, stringKey string) string {
	object, _ := values[objectKey].(map[string]any)
	value, _ := object[stringKey].(string)
	return value
}

func intField(values map[string]any, key string) int {
	switch value := values[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	default:
		return 0
	}
}

func mustJSONText(t *testing.T, value any) string {
	t.Helper()

	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal JSON text: %v", err)
	}
	return string(raw)
}

func seedBusinessAnalysisMCPFixtures(t *testing.T, store *sqlite.Store) {
	t.Helper()

	ctx := context.Background()
	for _, raw := range []json.RawMessage{
		mustJSON(t, map[string]any{
			"id":        "call_business_discovery_001",
			"title":     "Business Discovery - operations workflow",
			"started":   "2026-02-18T15:00:00Z",
			"duration":  1800,
			"system":    "Zoom",
			"direction": "Conference",
			"scope":     "External",
			"context": []any{
				map[string]any{
					"objectType": "Account",
					"id":         "acct_business_discovery_001",
					"fields": []any{
						map[string]any{"name": "Name", "value": "Discovery Account"},
						map[string]any{"name": "Industry", "value": "Manufacturing"},
					},
				},
				map[string]any{
					"objectType": "Opportunity",
					"id":         "opp_business_discovery_001",
					"fields": []any{
						map[string]any{"name": "Name", "value": "Discovery Opportunity"},
						map[string]any{"name": "StageName", "value": "Discovery"},
					},
				},
			},
		}),
		mustJSON(t, map[string]any{
			"id":        "call_business_discovery_002",
			"title":     "Business Discovery - finance workflow",
			"started":   "2026-03-05T15:00:00Z",
			"duration":  1200,
			"system":    "Zoom",
			"direction": "Conference",
			"scope":     "External",
			"context": []any{
				map[string]any{
					"objectType": "Account",
					"id":         "acct_business_discovery_002",
					"fields": []any{
						map[string]any{"name": "Industry", "value": "Financial Services"},
					},
				},
				map[string]any{
					"objectType": "Opportunity",
					"id":         "opp_business_discovery_002",
					"fields": []any{
						map[string]any{"name": "StageName", "value": "Evaluation"},
					},
				},
			},
		}),
		mustJSON(t, map[string]any{
			"id":        "call_other_001",
			"title":     "Implementation handoff",
			"started":   "2026-03-06T15:00:00Z",
			"duration":  900,
			"system":    "Zoom",
			"direction": "Conference",
			"scope":     "External",
		}),
	} {
		if _, err := store.UpsertCall(ctx, raw); err != nil {
			t.Fatalf("upsert business-analysis call: %v", err)
		}
	}

	for _, item := range []struct {
		callID string
		text   string
	}{
		{callID: "call_business_discovery_001", text: "The pain point is that manual process steps slow the team every week."},
		{callID: "call_business_discovery_002", text: "A manual process creates a reporting bottleneck for finance."},
		{callID: "call_other_001", text: "This implementation call should not match the business discovery title filter."},
	} {
		if _, err := store.UpsertTranscript(ctx, mustJSON(t, map[string]any{
			"callTranscripts": []any{
				map[string]any{
					"callId": item.callID,
					"transcript": []any{
						map[string]any{
							"speakerId": "buyer",
							"sentences": []any{
								map[string]any{"start": 1000, "end": 2500, "text": item.text},
							},
						},
					},
				},
			},
		})); err != nil {
			t.Fatalf("upsert business-analysis transcript: %v", err)
		}
	}
}

func seedLateStageMCPCall(t *testing.T, store *sqlite.Store) {
	t.Helper()

	lateCall := mustJSON(t, map[string]any{
		"id":      "call_late_stage_mcp",
		"title":   "Late stage MCP call",
		"started": "2026-04-24T14:00:00Z",
		"context": []any{
			map[string]any{
				"objectType": "Opportunity",
				"id":         "opp_late_stage_mcp",
				"fields": []any{
					map[string]any{"name": "StageName", "value": "Contract Signing"},
					map[string]any{"name": "ISSUE__c", "value": "Procurement review"},
				},
			},
		},
	})
	if _, err := store.UpsertCall(context.Background(), lateCall); err != nil {
		t.Fatalf("upsert late-stage call: %v", err)
	}
}

func seedInventoryMCPFixtures(t *testing.T, store *sqlite.Store) {
	t.Helper()

	ctx := context.Background()
	if _, err := store.UpsertCRMIntegration(ctx, mustJSON(t, map[string]any{
		"integrationId": "crm-int-001",
		"name":          "Salesforce production",
		"crmType":       "Salesforce",
	})); err != nil {
		t.Fatalf("upsert CRM integration: %v", err)
	}
	if _, err := store.UpsertCRMSchema(ctx, "crm-int-001", "DEAL", mustJSON(t, map[string]any{
		"requestId": "schema-request-001",
		"objectTypeToSelectedFields": map[string]any{
			"DEAL": []map[string]any{
				{"fieldName": "Amount", "label": "Amount", "fieldType": "currency"},
				{"fieldName": "StageName", "label": "Stage", "fieldType": "picklist"},
			},
		},
	})); err != nil {
		t.Fatalf("upsert CRM schema: %v", err)
	}
	if _, err := store.UpsertGongSetting(ctx, "trackers", mustJSON(t, map[string]any{
		"id":      "tracker-001",
		"name":    "Pricing objection",
		"enabled": true,
	})); err != nil {
		t.Fatalf("upsert Gong setting: %v", err)
	}
	if _, err := store.UpsertGongSetting(ctx, "scorecards", mustJSON(t, map[string]any{
		"scorecardId":   "scorecard-001",
		"scorecardName": "Discovery quality",
		"enabled":       true,
		"reviewMethod":  "AUTOMATIC",
		"questions": []map[string]any{
			{
				"questionId":   "question-001",
				"questionText": "Did the rep confirm pain?",
				"questionType": "SCALE",
				"minRange":     1,
				"maxRange":     5,
			},
		},
	})); err != nil {
		t.Fatalf("upsert Gong scorecard: %v", err)
	}
}

func seedScorecardActivityMCPFixtures(t *testing.T, store *sqlite.Store) {
	t.Helper()

	ctx := context.Background()
	if _, err := store.UpsertCall(ctx, mustJSON(t, map[string]any{
		"id":       "call-activity-001",
		"title":    "Raw activity call",
		"started":  "2026-03-01T15:00:00Z",
		"duration": 600,
	})); err != nil {
		t.Fatalf("upsert scorecard activity call: %v", err)
	}
	if _, err := store.UpsertScorecardActivity(ctx, mustJSON(t, map[string]any{
		"answeredScorecardId": "answered-activity-001",
		"scorecardId":         "scorecard-activity-001",
		"scorecardName":       "Discovery QA",
		"callId":              "call-activity-001",
		"callStartTime":       "2026-03-01T15:00:00Z",
		"reviewedUserId":      "user-activity-001",
		"reviewerUserId":      "reviewer-activity-001",
		"reviewMethod":        "MANUAL",
		"reviewTime":          "2026-03-02T15:00:00Z",
		"answers": []map[string]any{
			{
				"questionId":    "question-activity-001",
				"answerText":    "Confirmed pain",
				"isOverall":     true,
				"score":         4,
				"notApplicable": false,
			},
		},
	})); err != nil {
		t.Fatalf("upsert scorecard activity: %v", err)
	}
}

func seedAdditionalScorecardActivityMCPFixture(t *testing.T, store *sqlite.Store) {
	t.Helper()

	ctx := context.Background()
	if _, err := store.UpsertCall(ctx, mustJSON(t, map[string]any{
		"id":       "call-activity-002",
		"title":    "Second raw activity call",
		"started":  "2026-03-03T15:00:00Z",
		"duration": 600,
	})); err != nil {
		t.Fatalf("upsert second scorecard activity call: %v", err)
	}
	if _, err := store.UpsertScorecardActivity(ctx, mustJSON(t, map[string]any{
		"answeredScorecardId": "answered-activity-002",
		"scorecardId":         "scorecard-activity-002",
		"scorecardName":       "Demo QA",
		"callId":              "call-activity-002",
		"callStartTime":       "2026-03-03T15:00:00Z",
		"reviewedUserId":      "user-activity-002",
		"reviewerUserId":      "reviewer-activity-002",
		"reviewMethod":        "AUTOMATIC",
		"reviewTime":          "2026-03-04T15:00:00Z",
		"answers": []map[string]any{
			{
				"questionId":    "question-activity-002",
				"answerText":    "Confirmed value",
				"isOverall":     true,
				"score":         3,
				"notApplicable": false,
			},
		},
	})); err != nil {
		t.Fatalf("upsert second scorecard activity: %v", err)
	}
}

func seedOpportunityAggregateMCPFixtures(t *testing.T, store *sqlite.Store) {
	t.Helper()

	ctx := context.Background()
	calls := []json.RawMessage{
		mustJSON(t, map[string]any{
			"id":       "call_mcp_gap_covered",
			"title":    "Covered MCP contract call",
			"started":  "2026-04-23T12:00:00Z",
			"duration": 600,
			"context": []any{
				map[string]any{
					"objectType": "Opportunity",
					"id":         "opp_mcp_gap_001",
					"name":       "MCP Gap Opportunity",
					"fields": []any{
						map[string]any{"name": "StageName", "value": "Contract Signing"},
						map[string]any{"name": "Amount", "value": "75000"},
						map[string]any{"name": "CloseDate", "value": "2026-05-15"},
					},
				},
			},
		}),
		mustJSON(t, map[string]any{
			"id":       "call_mcp_gap_missing",
			"title":    "Missing MCP transcript call",
			"started":  "2026-04-24T12:00:00Z",
			"duration": 1200,
			"context": []any{
				map[string]any{
					"objectType": "Opportunity",
					"id":         "opp_mcp_gap_001",
					"name":       "MCP Gap Opportunity",
					"fields": []any{
						map[string]any{"name": "StageName", "value": "Contract Signing"},
						map[string]any{"name": "Amount", "value": "75000"},
						map[string]any{"name": "CloseDate", "value": "2026-05-15"},
					},
				},
			},
		}),
	}
	for _, raw := range calls {
		if _, err := store.UpsertCall(ctx, raw); err != nil {
			t.Fatalf("upsert aggregate MCP call: %v", err)
		}
	}

	transcript := mustJSON(t, map[string]any{
		"callTranscripts": []any{
			map[string]any{
				"callId": "call_mcp_gap_covered",
				"transcript": []any{
					map[string]any{
						"speakerId": "buyer",
						"sentences": []any{
							map[string]any{"start": 1000, "end": 3000, "text": "Pricing needs procurement approval."},
						},
					},
				},
			},
		},
	})
	if _, err := store.UpsertTranscript(ctx, transcript); err != nil {
		t.Fatalf("upsert aggregate MCP transcript: %v", err)
	}
}

func seedBusinessProfileMCPFixtures(t *testing.T, store *sqlite.Store) {
	t.Helper()

	ctx := context.Background()
	if _, err := store.UpsertCall(ctx, mustJSON(t, map[string]any{
		"id":        "call_mcp_profile_001",
		"title":     "MCP profile custom deal call",
		"started":   "2026-04-24T16:00:00Z",
		"duration":  1200,
		"system":    "Zoom",
		"direction": "Conference",
		"scope":     "External",
		"context": []any{
			map[string]any{
				"objectType": "Deal",
				"id":         "deal_mcp_profile_001",
				"name":       "MCP Profile Deal",
				"fields": []any{
					map[string]any{"name": "DealPhase__c", "value": "Proposal"},
					map[string]any{"name": "Amount", "value": "10000"},
					map[string]any{"name": "SecretID__c", "value": "tenant-secret-001"},
				},
			},
			map[string]any{
				"objectType": "Company",
				"id":         "company_mcp_profile_001",
				"name":       "MCP Profile Company",
				"fields": []any{
					map[string]any{"name": "Industry", "value": "Manufacturing"},
				},
			},
		},
	})); err != nil {
		t.Fatalf("upsert business profile MCP call: %v", err)
	}

	body := []byte(`
version: 1
name: MCP custom profile
objects:
  deal:
    object_types: [Deal]
  account:
    object_types: [Company]
fields:
  deal_stage:
    object: deal
    names: [DealPhase__c]
  account_industry:
    object: account
    names: [Industry]
  secret_id:
    object: deal
    names: [SecretID__c]
lifecycle:
  open:
    label: Open
    order: 10
    rules:
      - field: deal_stage
        op: in
        values: [Proposal]
  closed_won:
    label: Closed won
    order: 20
  closed_lost:
    label: Closed lost
    order: 30
  post_sales:
    label: Post sales
    order: 40
  unknown:
    label: Unknown
    order: 999
methodology:
  pain:
    description: Pain evidence
    aliases: [pain]
`)
	inventory, err := store.ProfileInventory(ctx)
	if err != nil {
		t.Fatalf("profile inventory: %v", err)
	}
	p, validation, err := profilepkg.ValidateBytes(body, inventory)
	if err != nil {
		t.Fatalf("validate business profile: %v", err)
	}
	if !validation.Valid {
		t.Fatalf("business profile has validation errors: %+v", validation.Findings)
	}
	canonical, err := profilepkg.CanonicalJSON(p)
	if err != nil {
		t.Fatalf("canonical profile json: %v", err)
	}
	if _, err := store.ImportProfile(ctx, sqlite.ProfileImportParams{
		SourcePath:      "/Users/example/private/mcp-profile.yaml",
		SourceSHA256:    validation.SourceSHA256,
		CanonicalSHA256: validation.CanonicalSHA256,
		RawYAML:         body,
		CanonicalJSON:   canonical,
		Profile:         p,
		Findings:        validation.Findings,
		ImportedBy:      "example-user",
	}); err != nil {
		t.Fatalf("import business profile: %v", err)
	}
}

func seedFilteredSegmentFixture(t *testing.T, store *sqlite.Store) {
	t.Helper()

	seedTranscriptSegmentFixture(t, store, "call_filtered_segments", "buyer-filtered", "2026-02-10T15:00:00Z", "The implementation timeline is the main objection.")
}

func seedTranscriptSegmentFixture(t *testing.T, store *sqlite.Store, callID, speakerID, startedAt, text string) {
	t.Helper()

	ctx := context.Background()
	if _, err := store.UpsertCall(ctx, mustJSON(t, map[string]any{
		"metaData": map[string]any{
			"id":        callID,
			"title":     "Filtered segment evidence",
			"started":   startedAt,
			"duration":  1800,
			"system":    "Zoom",
			"direction": "Conference",
			"scope":     "External",
		},
	})); err != nil {
		t.Fatalf("upsert filtered call: %v", err)
	}
	if _, err := store.UpsertTranscript(ctx, mustJSON(t, map[string]any{
		"callTranscripts": []any{
			map[string]any{
				"callId": callID,
				"transcript": []any{
					map[string]any{
						"speakerId": speakerID,
						"sentences": []any{
							map[string]any{"start": 1000, "end": 2000, "text": text},
						},
					},
				},
			},
		},
	})); err != nil {
		t.Fatalf("upsert filtered transcript: %v", err)
	}
}

func seedLifecycleMCPFixtures(t *testing.T, store *sqlite.Store) {
	t.Helper()

	ctx := context.Background()
	calls := []json.RawMessage{
		mustJSON(t, map[string]any{
			"id":       "call_mcp_lifecycle_active",
			"title":    "MCP lifecycle active pipeline call",
			"started":  "2026-04-21T12:00:00Z",
			"duration": 900,
			"context": []any{
				map[string]any{
					"objectType": "Opportunity",
					"id":         "opp_mcp_lifecycle_active",
					"name":       "MCP Active Lifecycle Opportunity",
					"fields": []any{
						map[string]any{"name": "StageName", "value": "MQL"},
						map[string]any{"name": "Type", "value": "New Business"},
						map[string]any{"name": "Primary_Lead_Source__c", "value": "Web"},
					},
				},
			},
		}),
		mustJSON(t, map[string]any{
			"id":       "call_mcp_lifecycle_renewal",
			"title":    "MCP lifecycle renewal call",
			"started":  "2026-04-22T12:00:00Z",
			"duration": 1200,
			"context": []any{
				map[string]any{
					"objectType": "Opportunity",
					"id":         "opp_mcp_lifecycle_renewal",
					"name":       "MCP Renewal Lifecycle Opportunity",
					"fields": []any{
						map[string]any{"name": "StageName", "value": "Discovery & Demo (SQO)"},
						map[string]any{"name": "Type", "value": "Renewal"},
						map[string]any{"name": "Renewal_Process__c", "value": "Standard"},
					},
				},
			},
		}),
		mustJSON(t, map[string]any{
			"id":       "call_mcp_lifecycle_upsell",
			"title":    "MCP lifecycle upsell call",
			"started":  "2026-04-23T12:00:00Z",
			"duration": 2400,
			"context": []any{
				map[string]any{
					"objectType": "Opportunity",
					"id":         "opp_mcp_lifecycle_upsell",
					"name":       "MCP Upsell Lifecycle Opportunity",
					"fields": []any{
						map[string]any{"name": "StageName", "value": "Demo & Business Case"},
						map[string]any{"name": "Type", "value": "Upsell"},
						map[string]any{"name": "One_Year_Upsell__c", "value": "12000"},
					},
				},
			},
		}),
	}
	for _, raw := range calls {
		if _, err := store.UpsertCall(ctx, raw); err != nil {
			t.Fatalf("upsert lifecycle MCP call: %v", err)
		}
	}

	transcript := mustJSON(t, map[string]any{
		"callTranscripts": []any{
			map[string]any{
				"callId": "call_mcp_lifecycle_renewal",
				"transcript": []any{
					map[string]any{
						"speakerId": "speaker",
						"sentences": []any{
							map[string]any{"start": 1000, "end": 2000, "text": "Renewal transcript coverage."},
						},
					},
				},
			},
		},
	})
	if _, err := store.UpsertTranscript(ctx, transcript); err != nil {
		t.Fatalf("upsert lifecycle MCP transcript: %v", err)
	}
}

func loadCallFixture(t *testing.T) json.RawMessage {
	t.Helper()

	var payload struct {
		Calls []json.RawMessage `json:"calls"`
	}
	if err := json.Unmarshal(loadFixture(t, "testdata/fixtures/calls.extensive.sample.json"), &payload); err != nil {
		t.Fatalf("unmarshal call fixture: %v", err)
	}
	if len(payload.Calls) == 0 {
		t.Fatal("calls fixture missing calls")
	}
	return payload.Calls[0]
}

func loadFixture(t *testing.T, rel string) []byte {
	t.Helper()

	path := filepath.Join(repoRoot(t), filepath.FromSlash(rel))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", rel, err)
	}
	return data
}

func repoRoot(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func runServer(t *testing.T, server *Server, input string) [][]byte {
	t.Helper()

	var output bytes.Buffer
	if err := server.Serve(context.Background(), bytes.NewBufferString(input), &output); err != nil {
		t.Fatalf("serve mcp: %v", err)
	}

	reader := bytes.NewReader(output.Bytes())
	bufReader := bufio.NewReader(reader)
	var frames [][]byte
	for {
		payload, err := readFrame(bufReader)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return frames
			}
			t.Fatalf("read response frame: %v", err)
		}
		frames = append(frames, payload)
	}
}

func requestFrame(req Request) string {
	payload, err := json.Marshal(req)
	if err != nil {
		panic(err)
	}
	return "Content-Length: " + strconv.Itoa(len(payload)) + "\r\n\r\n" + string(payload)
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()

	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal JSON: %v", err)
	}
	return payload
}

func mapFromJSONText(t *testing.T, text string, index int) map[string]any {
	t.Helper()

	var rows []map[string]any
	if err := json.Unmarshal([]byte(text), &rows); err != nil {
		t.Fatalf("unmarshal JSON text: %v", err)
	}
	if index >= len(rows) {
		t.Fatalf("row index %d out of range for %d rows", index, len(rows))
	}
	return rows[index]
}
