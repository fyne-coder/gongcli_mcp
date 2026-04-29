package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
)

func TestSyncCallsBusinessPresetAndStatusJSONWithSensitiveOverride(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gong.db")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/calls/extensive" {
			t.Fatalf("path=%q want /v2/calls/extensive", r.URL.Path)
		}

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		filter := body["filter"].(map[string]any)
		if got := filter["fromDateTime"]; got != "2026-04-20T00:00:00-04:00" {
			t.Fatalf("fromDateTime=%v want 2026-04-20T00:00:00-04:00", got)
		}
		if got := filter["toDateTime"]; got != "2026-04-24T23:59:59-04:00" {
			t.Fatalf("toDateTime=%v want 2026-04-24T23:59:59-04:00", got)
		}
		selector := body["contentSelector"].(map[string]any)
		if selector["context"] != "Extended" {
			t.Fatalf("contentSelector=%v want Extended", selector)
		}

		writeCLIJSON(t, w, map[string]any{
			"records": map[string]any{
				"currentPageSize":   1,
				"currentPageNumber": 0,
			},
			"calls": []map[string]any{
				{
					"id":       "call-sync-001",
					"title":    "Discovery",
					"started":  "2026-04-20T14:00:00Z",
					"duration": 120,
					"parties":  []map[string]any{{"id": "user-001"}},
					"context": map[string]any{
						"crmObjects": []map[string]any{
							{
								"type": "Opportunity",
								"id":   "opp-sync-001",
								"name": "Expansion",
								"fields": []map[string]any{
									{"name": "stage", "value": "Qualified"},
								},
							},
						},
					},
				},
			},
		})
	}))
	defer server.Close()

	setTestEnv(t, server.URL)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	a := &app{out: &stdout, err: &stderr, restricted: true}

	err := a.sync(context.Background(), []string{
		"calls",
		"--db", dbPath,
		"--from", "2026-04-20",
		"--to", "2026-04-24",
		"--preset", "business",
		"--allow-sensitive-export",
		"--max-pages", "1",
	})
	if err != nil {
		t.Fatalf("sync calls returned error: %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout=%q want empty", stdout.String())
	}
	if got := stderr.String(); !strings.Contains(got, "synced calls:") || !strings.Contains(got, "preset=business") || !strings.Contains(got, "context=Extended") {
		t.Fatalf("stderr=%q missing sync summary", got)
	}

	store := openCLITestStore(t, dbPath)
	raw, err := store.GetCallRaw(context.Background(), "call-sync-001")
	if err != nil {
		t.Fatalf("GetCallRaw returned error: %v", err)
	}
	var callDoc map[string]any
	if err := json.Unmarshal(raw, &callDoc); err != nil {
		t.Fatalf("decode stored call: %v", err)
	}
	if callDoc["id"] != "call-sync-001" {
		t.Fatalf("stored call id=%v want call-sync-001", callDoc["id"])
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	a = &app{out: &stdout, err: &stderr}
	err = a.sync(context.Background(), []string{"status", "--db", dbPath})
	if err != nil {
		t.Fatalf("sync status returned error: %v", err)
	}

	var status struct {
		TotalCalls                   int64 `json:"total_calls"`
		TotalEmbeddedCRMContextCalls int64 `json:"total_embedded_crm_context_calls"`
		TotalEmbeddedCRMFields       int64 `json:"total_embedded_crm_fields"`
		ProfileReadiness             struct {
			Active      bool   `json:"active"`
			Status      string `json:"status"`
			CacheStatus string `json:"cache_status"`
		} `json:"profile_readiness"`
		PublicReadiness struct {
			ConversationVolume struct {
				Ready bool `json:"ready"`
			} `json:"conversation_volume"`
			CRMSegmentation struct {
				Ready bool `json:"ready"`
			} `json:"crm_segmentation"`
			CRMInventoryNote string `json:"crm_inventory_note"`
		} `json:"public_readiness"`
		LastRun struct {
			Scope string `json:"scope"`
		} `json:"last_run"`
		States []struct {
			Scope string `json:"scope"`
		} `json:"states"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &status); err != nil {
		t.Fatalf("decode status JSON: %v", err)
	}
	if status.TotalCalls != 1 {
		t.Fatalf("total_calls=%d want 1", status.TotalCalls)
	}
	if status.TotalEmbeddedCRMContextCalls != 1 || status.TotalEmbeddedCRMFields != 1 {
		t.Fatalf("embedded CRM counts calls=%d fields=%d want 1/1", status.TotalEmbeddedCRMContextCalls, status.TotalEmbeddedCRMFields)
	}
	if status.ProfileReadiness.Active || status.ProfileReadiness.Status != "not_configured" || status.ProfileReadiness.CacheStatus != "not_applicable" {
		t.Fatalf("unexpected profile readiness: %+v", status.ProfileReadiness)
	}
	if !status.PublicReadiness.ConversationVolume.Ready || !status.PublicReadiness.CRMSegmentation.Ready {
		t.Fatalf("unexpected public readiness: %+v", status.PublicReadiness)
	}
	if !strings.Contains(status.PublicReadiness.CRMInventoryNote, "Embedded CRM context") {
		t.Fatalf("missing CRM inventory note: %+v", status.PublicReadiness)
	}
	if status.LastRun.Scope != "calls" {
		t.Fatalf("last_run.scope=%q want calls", status.LastRun.Scope)
	}
	if len(status.States) != 1 || status.States[0].Scope != "calls" {
		t.Fatalf("states=%+v want one calls state", status.States)
	}
	if stderr.Len() != 0 {
		t.Fatalf("status stderr=%q want empty", stderr.String())
	}
}

func TestSyncRestrictedModeBlocksSensitiveSubcommands(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "gong.db")
	outDir := filepath.Join(dir, "transcripts")

	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "sync-calls-business",
			args: []string{"calls", "--db", dbPath, "--from", "2026-04-20", "--to", "2026-04-24", "--preset", "business"},
			want: "sync calls --preset business is blocked because restricted mode is enabled",
		},
		{
			name: "sync-calls-all",
			args: []string{"calls", "--db", dbPath, "--from", "2026-04-20", "--to", "2026-04-24", "--preset", "all"},
			want: "sync calls --preset all is blocked because restricted mode is enabled",
		},
		{
			name: "sync-transcripts",
			args: []string{"transcripts", "--db", dbPath, "--out-dir", outDir},
			want: "sync transcripts is blocked because restricted mode is enabled",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			a := &app{out: &stdout, err: &stderr, restricted: true}

			err := a.sync(context.Background(), tc.args)
			if err == nil {
				t.Fatal("sync returned nil error, want restricted-mode failure")
			}
			if got := err.Error(); !strings.Contains(got, tc.want) || !strings.Contains(got, "--allow-sensitive-export") {
				t.Fatalf("err=%q missing restricted-mode guidance", got)
			}
		})
	}
}

func TestSyncUsersWritesSummaryAndStoresRows(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gong.db")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/users" {
			t.Fatalf("path=%q want /v2/users", r.URL.Path)
		}
		if got := r.URL.Query().Get("limit"); got != "" {
			t.Fatalf("limit=%q want omitted", got)
		}
		writeCLIJSON(t, w, map[string]any{
			"records": map[string]any{
				"currentPageSize":   1,
				"currentPageNumber": 0,
			},
			"users": []map[string]any{
				{
					"id":           "user-sync-001",
					"emailAddress": "user@example.invalid",
					"firstName":    "Taylor",
					"lastName":     "Seller",
					"name":         "Taylor Seller",
					"title":        "AE",
					"active":       true,
				},
			},
		})
	}))
	defer server.Close()

	setTestEnv(t, server.URL)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	a := &app{out: &stdout, err: &stderr}
	err := a.sync(context.Background(), []string{"users", "--db", dbPath, "--max-pages", "1"})
	if err != nil {
		t.Fatalf("sync users returned error: %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout=%q want empty", stdout.String())
	}
	if got := stderr.String(); !strings.Contains(got, "synced users:") || !strings.Contains(got, "written=1") {
		t.Fatalf("stderr=%q missing users summary", got)
	}

	store := openCLITestStore(t, dbPath)
	defer store.Close()
	summary, err := store.SyncStatusSummary(context.Background())
	if err != nil {
		t.Fatalf("SyncStatusSummary returned error: %v", err)
	}
	if summary.TotalUsers != 1 {
		t.Fatalf("total users=%d want 1", summary.TotalUsers)
	}
}

func TestSyncInventoryAnalyzeAndMCPDiscoveryCommands(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gong.db")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/crm/integrations":
			writeCLIJSON(t, w, map[string]any{
				"integrations": []map[string]any{
					{
						"integrationId": "crm-int-cli-001",
						"name":          "Salesforce production",
						"crmType":       "Salesforce",
					},
				},
			})
		case "/v2/crm/entity-schema":
			if got := r.URL.Query().Get("integrationId"); got != "crm-int-cli-001" {
				t.Fatalf("integrationId=%q want crm-int-cli-001", got)
			}
			if got := r.URL.Query().Get("objectType"); got != "DEAL" {
				t.Fatalf("objectType=%q want DEAL", got)
			}
			writeCLIJSON(t, w, map[string]any{
				"objectTypeToSelectedFields": map[string]any{
					"DEAL": []map[string]any{
						{"fieldName": "Amount", "label": "Amount", "fieldType": "currency"},
						{"fieldName": "StageName", "label": "Stage", "fieldType": "picklist"},
					},
				},
			})
		case "/v2/settings/scorecards":
			writeCLIJSON(t, w, map[string]any{
				"scorecards": []map[string]any{
					{
						"scorecardId":   "scorecard-cli-001",
						"scorecardName": "Discovery quality",
						"enabled":       true,
						"reviewMethod":  "AUTOMATIC",
						"questions": []map[string]any{
							{
								"questionId":   "question-cli-001",
								"questionText": "Did the rep confirm pain?",
								"questionType": "SCALE",
								"minRange":     1,
								"maxRange":     5,
							},
						},
					},
				},
			})
		default:
			t.Fatalf("unexpected path=%q", r.URL.Path)
		}
	}))
	defer server.Close()
	setTestEnv(t, server.URL)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	a := &app{out: &stdout, err: &stderr}
	if err := a.sync(context.Background(), []string{"crm-integrations", "--db", dbPath}); err != nil {
		t.Fatalf("sync crm-integrations returned error: %v", err)
	}
	if !strings.Contains(stderr.String(), "synced crm integrations") {
		t.Fatalf("crm integrations stderr=%q missing summary", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if err := a.sync(context.Background(), []string{
		"crm-schema",
		"--db", dbPath,
		"--integration-id", "crm-int-cli-001",
		"--object-type", "DEAL",
	}); err != nil {
		t.Fatalf("sync crm-schema returned error: %v", err)
	}
	if !strings.Contains(stderr.String(), "fields=2") {
		t.Fatalf("crm schema stderr=%q missing fields=2", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if err := a.sync(context.Background(), []string{"settings", "--db", dbPath, "--kind", "scorecards"}); err != nil {
		t.Fatalf("sync settings returned error: %v", err)
	}
	if !strings.Contains(stderr.String(), "synced settings") {
		t.Fatalf("settings stderr=%q missing summary", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if err := a.analyze(context.Background(), []string{
		"crm-schema",
		"--db", dbPath,
		"--integration-id", "crm-int-cli-001",
		"--object-type", "DEAL",
	}); err != nil {
		t.Fatalf("analyze crm-schema returned error: %v", err)
	}
	var schemaResp struct {
		Fields []sqlite.CRMSchemaFieldRecord `json:"fields"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &schemaResp); err != nil {
		t.Fatalf("decode crm schema response: %v", err)
	}
	if len(schemaResp.Fields) != 2 || schemaResp.Fields[0].FieldName != "Amount" {
		t.Fatalf("unexpected schema response: %+v", schemaResp)
	}

	stdout.Reset()
	stderr.Reset()
	if err := a.analyze(context.Background(), []string{"settings", "--db", dbPath, "--kind", "scorecards"}); err != nil {
		t.Fatalf("analyze settings returned error: %v", err)
	}
	var settingsResp struct {
		Results []sqlite.GongSettingRecord `json:"results"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &settingsResp); err != nil {
		t.Fatalf("decode settings response: %v", err)
	}
	if len(settingsResp.Results) != 1 || settingsResp.Results[0].ObjectID != "scorecard-cli-001" {
		t.Fatalf("unexpected settings response: %+v", settingsResp)
	}
	if settingsResp.Results[0].Name != "Discovery quality" {
		t.Fatalf("settings name=%q want Discovery quality", settingsResp.Results[0].Name)
	}

	stdout.Reset()
	stderr.Reset()
	if err := a.analyze(context.Background(), []string{"scorecards", "--db", dbPath}); err != nil {
		t.Fatalf("analyze scorecards returned error: %v", err)
	}
	var scorecardsResp struct {
		Results []sqlite.ScorecardSummary `json:"results"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &scorecardsResp); err != nil {
		t.Fatalf("decode scorecards response: %v", err)
	}
	if len(scorecardsResp.Results) != 1 || scorecardsResp.Results[0].Name != "Discovery quality" || scorecardsResp.Results[0].QuestionCount != 1 {
		t.Fatalf("unexpected scorecards response: %+v", scorecardsResp)
	}

	stdout.Reset()
	stderr.Reset()
	if err := a.analyze(context.Background(), []string{"scorecard", "--db", dbPath, "--scorecard-id", "scorecard-cli-001"}); err != nil {
		t.Fatalf("analyze scorecard returned error: %v", err)
	}
	var scorecardDetail sqlite.ScorecardDetail
	if err := json.Unmarshal(stdout.Bytes(), &scorecardDetail); err != nil {
		t.Fatalf("decode scorecard detail: %v", err)
	}
	if len(scorecardDetail.Questions) != 1 || scorecardDetail.Questions[0].QuestionText != "Did the rep confirm pain?" {
		t.Fatalf("unexpected scorecard detail: %+v", scorecardDetail)
	}

	stdout.Reset()
	stderr.Reset()
	if err := a.mcp(context.Background(), []string{"tools"}); err != nil {
		t.Fatalf("mcp tools returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "list_gong_settings") {
		t.Fatalf("mcp tools output missing list_gong_settings: %s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if err := a.mcp(context.Background(), []string{"tool-info", "list_cached_crm_schema_fields"}); err != nil {
		t.Fatalf("mcp tool-info returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "input_schema") {
		t.Fatalf("mcp tool-info output missing input_schema: %s", stdout.String())
	}
}

func TestSyncTranscriptsDownloadsMissingCalls(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gong.db")
	outDir := filepath.Join(t.TempDir(), "transcripts")
	store := openCLITestStore(t, dbPath)
	if _, err := store.UpsertCall(context.Background(), mustMarshalJSON(t, map[string]any{
		"id":       "call-transcript-001",
		"title":    "Transcript target",
		"started":  "2026-04-21T14:00:00Z",
		"duration": 90,
		"parties":  []map[string]any{{"id": "user-001"}},
	})); err != nil {
		t.Fatalf("UpsertCall returned error: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/calls/transcript" {
			t.Fatalf("path=%q want /v2/calls/transcript", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		filter := body["filter"].(map[string]any)
		callIDs := filter["callIds"].([]any)
		if len(callIDs) != 1 || callIDs[0] != "call-transcript-001" {
			t.Fatalf("callIds=%v want [call-transcript-001]", callIDs)
		}
		writeCLIJSON(t, w, map[string]any{
			"callTranscripts": []map[string]any{
				{
					"callId": "call-transcript-001",
					"transcript": []map[string]any{
						{
							"speakerId": "speaker-001",
							"sentences": []map[string]any{
								{"start": 0, "end": 1000, "text": "First transcript sentence."},
							},
						},
					},
				},
			},
		})
	}))
	defer server.Close()

	setTestEnv(t, server.URL)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	a := &app{out: &stdout, err: &stderr}
	err := a.sync(context.Background(), []string{
		"transcripts",
		"--db", dbPath,
		"--out-dir", outDir,
		"--limit", "1",
	})
	if err != nil {
		t.Fatalf("sync transcripts returned error: %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout=%q want empty", stdout.String())
	}
	if got := stderr.String(); !strings.Contains(got, "synced transcripts:") || !strings.Contains(got, "stored=1") {
		t.Fatalf("stderr=%q missing transcript summary", got)
	}

	body, err := os.ReadFile(filepath.Join(outDir, "call-transcript-001.json"))
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	var transcriptDoc map[string]any
	if err := json.Unmarshal(body, &transcriptDoc); err != nil {
		t.Fatalf("decode transcript file: %v", err)
	}
	if got := transcriptDoc["callId"]; got != "call-transcript-001" {
		t.Fatalf("transcript file callId=%v want call-transcript-001: %+v", got, transcriptDoc)
	}
	if _, ok := transcriptDoc["callTranscripts"]; ok {
		t.Fatalf("transcript file should contain a normalized single transcript, got envelope: %+v", transcriptDoc)
	}

	store = openCLITestStore(t, dbPath)
	defer store.Close()
	summary, err := store.SyncStatusSummary(context.Background())
	if err != nil {
		t.Fatalf("SyncStatusSummary returned error: %v", err)
	}
	if summary.TotalTranscripts != 1 {
		t.Fatalf("total_transcripts=%d want 1", summary.TotalTranscripts)
	}
	if summary.MissingTranscripts != 0 {
		t.Fatalf("missing_transcripts=%d want 0", summary.MissingTranscripts)
	}
}

func TestSearchCommandsAndCallsShowJSON(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gong.db")
	store := openCLITestStore(t, dbPath)
	if _, err := store.UpsertCall(context.Background(), mustMarshalJSON(t, map[string]any{
		"id":       "call-search-001",
		"title":    "CRM Match",
		"started":  "2026-04-24T15:30:00Z",
		"duration": 2400,
		"parties":  []map[string]any{{"id": "speaker_internal_001"}},
		"context": map[string]any{
			"crmObjects": []map[string]any{
				{
					"type": "Opportunity",
					"id":   "opp_001",
					"name": "Expansion Q2",
					"fields": map[string]any{
						"amount": 40000,
					},
				},
			},
		},
	})); err != nil {
		t.Fatalf("UpsertCall returned error: %v", err)
	}
	if _, err := store.UpsertTranscript(context.Background(), mustMarshalJSON(t, map[string]any{
		"callTranscripts": []map[string]any{
			{
				"callId": "call-search-001",
				"transcript": []map[string]any{
					{
						"speakerId": "speaker_internal_001",
						"sentences": []map[string]any{
							{"start": 1000, "end": 2000, "text": "Synthetic external expansion mention."},
						},
					},
				},
			},
		},
	})); err != nil {
		t.Fatalf("UpsertTranscript returned error: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	a := &app{}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	a.out = &stdout
	a.err = &stderr

	err := a.search(context.Background(), []string{
		"transcripts",
		"--db", dbPath,
		"--query", "external",
		"--limit", "5",
	})
	if err != nil {
		t.Fatalf("search transcripts returned error: %v", err)
	}
	var transcriptResp struct {
		Query   string `json:"query"`
		Results []struct {
			CallID  string `json:"call_id"`
			Snippet string `json:"snippet"`
		} `json:"results"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &transcriptResp); err != nil {
		t.Fatalf("decode transcript search JSON: %v", err)
	}
	if transcriptResp.Query != "external" || len(transcriptResp.Results) != 1 || transcriptResp.Results[0].CallID != "call-search-001" {
		t.Fatalf("transcript search response=%+v want one call-search-001 result", transcriptResp)
	}
	if stderr.Len() != 0 {
		t.Fatalf("transcript search stderr=%q want empty", stderr.String())
	}
	if strings.Contains(stdout.String(), `"text"`) {
		t.Fatalf("transcript search leaked raw text field: %s", stdout.String())
	}
	if strings.Contains(stdout.String(), "Synthetic external expansion mention.") {
		t.Fatalf("transcript search leaked full segment text: %s", stdout.String())
	}
	if transcriptResp.Results[0].Snippet == "" {
		t.Fatal("transcript search snippet was empty")
	}

	stdout.Reset()
	stderr.Reset()
	err = a.search(context.Background(), []string{
		"calls",
		"--db", dbPath,
		"--crm-object-type", "Opportunity",
		"--crm-object-id", "opp_001",
		"--limit", "5",
	})
	if err != nil {
		t.Fatalf("search calls returned error: %v", err)
	}
	var callResp struct {
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &callResp); err != nil {
		t.Fatalf("decode call search JSON: %v", err)
	}
	if len(callResp.Results) != 1 || callResp.Results[0]["id"] != "call-search-001" {
		t.Fatalf("call search response=%+v want one call-search-001 result", callResp)
	}

	stdout.Reset()
	stderr.Reset()
	err = a.callsShow(context.Background(), []string{
		"--db", dbPath,
		"--call-id", "call-search-001",
		"--json",
	})
	if err != nil {
		t.Fatalf("callsShow returned error: %v", err)
	}
	var callDoc map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &callDoc); err != nil {
		t.Fatalf("decode calls show JSON: %v", err)
	}
	if callDoc["id"] != "call-search-001" {
		t.Fatalf("calls show id=%v want call-search-001", callDoc["id"])
	}
	if stderr.Len() != 0 {
		t.Fatalf("calls show stderr=%q want empty", stderr.String())
	}
}

func TestAnalyzeCommands(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gong.db")
	store := openCLITestStore(t, dbPath)
	if _, err := store.UpsertCall(context.Background(), mustMarshalJSON(t, map[string]any{
		"metaData": map[string]any{
			"id":              "call-analyze-001",
			"title":           "Analyze contract call",
			"started":         "2026-04-24T15:30:00Z",
			"duration":        1800,
			"system":          "Zoom",
			"direction":       "Conference",
			"scope":           "External",
			"calendarEventId": "calendar-analyze-001",
		},
		"context": []any{
			map[string]any{
				"objectType": "Opportunity",
				"id":         "opp_analyze_001",
				"fields": []map[string]any{
					{"name": "StageName", "value": "Contract Signing"},
					{"name": "Type", "value": "New Business"},
				},
			},
		},
	})); err != nil {
		t.Fatalf("UpsertCall returned error: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	a := &app{}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	a.out = &stdout
	a.err = &stderr

	err := a.analyze(context.Background(), []string{
		"calls",
		"--db", dbPath,
		"--group-by", "stage",
		"--limit", "5",
	})
	if err != nil {
		t.Fatalf("analyze calls returned error: %v", err)
	}
	var callsResp struct {
		GroupBy string                       `json:"group_by"`
		Results []sqlite.CallFactsSummaryRow `json:"results"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &callsResp); err != nil {
		t.Fatalf("decode analyze calls JSON: %v", err)
	}
	if callsResp.GroupBy != "opportunity_stage" {
		t.Fatalf("top-level group_by=%q want opportunity_stage", callsResp.GroupBy)
	}
	if len(callsResp.Results) != 1 || callsResp.Results[0].GroupValue != "Contract Signing" || callsResp.Results[0].CallCount != 1 {
		t.Fatalf("unexpected analyze calls response: %+v", callsResp)
	}
	if stderr.Len() != 0 {
		t.Fatalf("analyze calls stderr=%q want empty", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	err = a.analyze(context.Background(), []string{"coverage", "--db", dbPath})
	if err != nil {
		t.Fatalf("analyze coverage returned error: %v", err)
	}
	var coverageResp struct {
		Coverage sqlite.CallFactsCoverage `json:"coverage"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &coverageResp); err != nil {
		t.Fatalf("decode coverage JSON: %v", err)
	}
	if coverageResp.Coverage.TotalCalls != 1 || coverageResp.Coverage.MissingTranscriptCount != 1 {
		t.Fatalf("unexpected coverage response: %+v", coverageResp)
	}

	stdout.Reset()
	stderr.Reset()
	err = a.analyze(context.Background(), []string{
		"transcript-backlog",
		"--db", dbPath,
		"--limit", "5",
	})
	if err != nil {
		t.Fatalf("analyze transcript-backlog returned error: %v", err)
	}
	var backlogResp struct {
		Results []sqlite.LifecycleTranscriptPriority `json:"results"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &backlogResp); err != nil {
		t.Fatalf("decode backlog JSON: %v", err)
	}
	if len(backlogResp.Results) != 1 || backlogResp.Results[0].CallID != "call-analyze-001" {
		t.Fatalf("unexpected backlog response: %+v", backlogResp)
	}
}

func TestProfileCLIFlowWithCustomCRM(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gong.db")
	profilePath := filepath.Join(t.TempDir(), "gongctl-profile.yaml")
	store := openCLITestStore(t, dbPath)
	if _, err := store.UpsertCall(context.Background(), mustMarshalJSON(t, map[string]any{
		"id":       "call-cli-profile-001",
		"title":    "Custom profile call",
		"started":  "2026-04-24T15:00:00Z",
		"duration": 900,
		"metaData": map[string]any{
			"system":    "Google Meet",
			"direction": "Conference",
			"scope":     "External",
		},
		"context": map[string]any{
			"crmObjects": []map[string]any{
				{
					"type": "Deal",
					"id":   "deal-cli-001",
					"fields": []map[string]any{
						{"name": "DealPhase__c", "label": "Stage", "value": "Proposal"},
					},
				},
				{
					"type": "Company",
					"id":   "company-cli-001",
					"fields": []map[string]any{
						{"name": "Industry", "label": "Industry", "value": "Manufacturing"},
					},
				},
			},
		},
	})); err != nil {
		t.Fatalf("UpsertCall returned error: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	a := &app{out: &stdout, err: &stderr}
	if err := a.profile(context.Background(), []string{"discover", "--db", dbPath, "--out", profilePath}); err != nil {
		t.Fatalf("profile discover returned error: %v", err)
	}
	body, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatalf("read discovered profile: %v", err)
	}
	if !strings.Contains(string(body), "DealPhase__c") {
		t.Fatalf("discovered profile missing custom stage field:\n%s", string(body))
	}

	stdout.Reset()
	stderr.Reset()
	if err := a.profile(context.Background(), []string{"validate", "--db", dbPath, "--profile", profilePath}); err != nil {
		t.Fatalf("profile validate returned error: %v", err)
	}
	var validation struct {
		Valid bool `json:"valid"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &validation); err != nil {
		t.Fatalf("decode validation: %v", err)
	}
	if !validation.Valid {
		t.Fatalf("validation response not valid: %s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if err := a.profile(context.Background(), []string{"import", "--db", dbPath, "--profile", profilePath}); err != nil {
		t.Fatalf("profile import returned error: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	if err := a.analyze(context.Background(), []string{
		"calls",
		"--db", dbPath,
		"--lifecycle-source", "profile",
		"--group-by", "deal_stage",
		"--limit", "5",
	}); err != nil {
		t.Fatalf("profile-aware analyze calls returned error: %v", err)
	}
	var callsResp struct {
		LifecycleSource string                       `json:"lifecycle_source"`
		Results         []sqlite.CallFactsSummaryRow `json:"results"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &callsResp); err != nil {
		t.Fatalf("decode calls response: %v", err)
	}
	if callsResp.LifecycleSource != sqlite.LifecycleSourceProfile || len(callsResp.Results) != 1 || callsResp.Results[0].GroupValue != "Proposal" {
		t.Fatalf("unexpected profile-aware calls response: %+v body=%s", callsResp, stdout.String())
	}
}

func openCLITestStore(t *testing.T, dbPath string) *sqlite.Store {
	t.Helper()

	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open returned error: %v", err)
	}
	return store
}

func setTestEnv(t *testing.T, baseURL string) {
	t.Helper()
	t.Setenv("GONG_ACCESS_KEY", "key")
	t.Setenv("GONG_ACCESS_KEY_SECRET", "secret")
	t.Setenv("GONG_BASE_URL", baseURL)
}

func mustMarshalJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}
	return json.RawMessage(body)
}

func writeCLIJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("json.NewEncoder returned error: %v", err)
	}
}
