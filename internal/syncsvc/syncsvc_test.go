package syncsvc

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fyne-coder/gongcli_mcp/internal/auth"
	"github.com/fyne-coder/gongcli_mcp/internal/gong"
	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
)

func TestSyncCallsPaginatesAndStoresContext(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/calls/extensive" {
			t.Fatalf("path=%q want /v2/calls/extensive", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("method=%q want POST", r.Method)
		}

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}

		filter, ok := body["filter"].(map[string]any)
		if !ok {
			t.Fatalf("filter missing: %#v", body)
		}
		if got := filter["fromDateTime"]; got != "2026-04-20T00:00:00-04:00" {
			t.Fatalf("fromDateTime=%v want 2026-04-20T00:00:00-04:00", got)
		}
		if got := filter["toDateTime"]; got != "2026-04-24T23:59:59-04:00" {
			t.Fatalf("toDateTime=%v want 2026-04-24T23:59:59-04:00", got)
		}
		contentSelector, ok := body["contentSelector"].(map[string]any)
		if !ok || contentSelector["context"] != "Extended" {
			t.Fatalf("contentSelector=%#v want Extended", body["contentSelector"])
		}

		requests++
		switch requests {
		case 1:
			if got := strings.TrimSpace(stringValue(body["cursor"])); got != "" {
				t.Fatalf("page1 cursor=%q want empty", got)
			}
			writeJSON(t, w, map[string]any{
				"records": map[string]any{
					"currentPageSize":   1,
					"currentPageNumber": 0,
					"cursor":            "page-2",
				},
				"calls": []map[string]any{
					{
						"id":       "call-001",
						"title":    "Discovery",
						"started":  "2026-04-20T14:00:00Z",
						"duration": 120,
						"parties":  []map[string]any{{"id": "user-001"}},
						"context": map[string]any{
							"crm": []map[string]any{
								{
									"id":   "opp-001",
									"type": "Opportunity",
									"name": "ACME renewal",
									"fields": []map[string]any{
										{"name": "amount", "value": 40000},
										{"name": "stage", "value": "Negotiation"},
									},
								},
							},
						},
					},
				},
			})
		case 2:
			if got := stringValue(body["cursor"]); got != "page-2" {
				t.Fatalf("page2 cursor=%q want page-2", got)
			}
			writeJSON(t, w, map[string]any{
				"records": map[string]any{
					"currentPageSize":   1,
					"currentPageNumber": 1,
				},
				"calls": []map[string]any{
					{
						"id":       "call-002",
						"title":    "Demo",
						"started":  "2026-04-21T15:00:00Z",
						"duration": 300,
						"parties":  []map[string]any{{"id": "user-002"}},
					},
				},
			})
		default:
			t.Fatalf("unexpected request %d", requests)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server)
	result, err := SyncCalls(ctx, client, store, CallsParams{
		From:    "2026-04-20",
		To:      "2026-04-24",
		Context: "Extended",
		Preset:  "daily",
	})
	if err != nil {
		t.Fatalf("SyncCalls returned error: %v", err)
	}
	if result.Pages != 2 {
		t.Fatalf("pages=%d want 2", result.Pages)
	}
	if result.RecordsSeen != 2 {
		t.Fatalf("records seen=%d want 2", result.RecordsSeen)
	}
	if result.RecordsWritten != 2 {
		t.Fatalf("records written=%d want 2", result.RecordsWritten)
	}
	if result.Cursor != "page-2" {
		t.Fatalf("cursor=%q want page-2", result.Cursor)
	}

	assertCount(t, store.DB(), "calls", 2)
	assertCount(t, store.DB(), "call_context_objects", 1)
	assertCount(t, store.DB(), "call_context_fields", 2)
	assertCount(t, store.DB(), "sync_runs", 1)

	var scope, syncKey, requestContext, status string
	var seen, written int64
	if err := store.DB().QueryRowContext(
		ctx,
		`SELECT scope, sync_key, request_context, status, records_seen, records_written
		   FROM sync_runs
		  ORDER BY id DESC
		  LIMIT 1`,
	).Scan(&scope, &syncKey, &requestContext, &status, &seen, &written); err != nil {
		t.Fatalf("query sync run: %v", err)
	}
	if scope != scopeCalls {
		t.Fatalf("scope=%q want %q", scope, scopeCalls)
	}
	if !strings.Contains(syncKey, "preset=daily") || !strings.Contains(syncKey, "context=Extended") {
		t.Fatalf("syncKey=%q missing preset/context", syncKey)
	}
	if requestContext != "preset=daily,context=Extended" {
		t.Fatalf("requestContext=%q want preset=daily,context=Extended", requestContext)
	}
	if status != "success" {
		t.Fatalf("status=%q want success", status)
	}
	if seen != 2 || written != 2 {
		t.Fatalf("seen/written=%d/%d want 2/2", seen, written)
	}
}

func TestSyncCallsRejectsRepeatedCursorAndFinishesRun(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{
			"records": map[string]any{
				"currentPageSize":   1,
				"currentPageNumber": 0,
				"cursor":            "loop",
			},
			"calls": []map[string]any{
				{
					"id":       "call-loop",
					"title":    "Loop",
					"started":  "2026-04-22T10:00:00Z",
					"duration": 60,
				},
			},
		})
	}))
	defer server.Close()

	client := newTestClient(t, server)
	_, err := SyncCalls(ctx, client, store, CallsParams{Cursor: "loop"})
	if err == nil {
		t.Fatal("SyncCalls returned nil error, want repeated cursor error")
	}
	if !strings.Contains(err.Error(), "pagination cursor repeated") {
		t.Fatalf("error=%q want repeated cursor message", err)
	}

	assertCount(t, store.DB(), "calls", 1)
	assertCount(t, store.DB(), "sync_runs", 1)

	var status, errorText string
	var seen, written int64
	if err := store.DB().QueryRowContext(
		ctx,
		`SELECT status, error_text, records_seen, records_written
		   FROM sync_runs
		  ORDER BY id DESC
		  LIMIT 1`,
	).Scan(&status, &errorText, &seen, &written); err != nil {
		t.Fatalf("query failed sync run: %v", err)
	}
	if status != "error" {
		t.Fatalf("status=%q want error", status)
	}
	if !strings.Contains(errorText, "pagination cursor repeated") {
		t.Fatalf("error_text=%q want repeated cursor message", errorText)
	}
	if seen != 1 || written != 1 {
		t.Fatalf("seen/written=%d/%d want 1/1", seen, written)
	}
}

func TestSyncCallsRerunIsIdempotentOnRowCounts(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{
			"records": map[string]any{
				"currentPageSize":   1,
				"currentPageNumber": 0,
			},
			"calls": []map[string]any{
				{
					"id":       "call-constant",
					"title":    "Constant",
					"started":  "2026-04-23T10:00:00Z",
					"duration": 90,
					"context": map[string]any{
						"crm": []map[string]any{
							{
								"id":   "opp-constant",
								"type": "Opportunity",
								"fields": []map[string]any{
									{"name": "amount", "value": 123},
								},
							},
						},
					},
				},
			},
		})
	}))
	defer server.Close()

	client := newTestClient(t, server)
	for i := 0; i < 2; i++ {
		result, err := SyncCalls(ctx, client, store, CallsParams{Preset: "nightly"})
		if err != nil {
			t.Fatalf("SyncCalls run %d returned error: %v", i+1, err)
		}
		if result.RecordsSeen != 1 || result.RecordsWritten != 1 {
			t.Fatalf("run %d seen/written=%d/%d want 1/1", i+1, result.RecordsSeen, result.RecordsWritten)
		}
	}

	assertCount(t, store.DB(), "calls", 1)
	assertCount(t, store.DB(), "call_context_objects", 1)
	assertCount(t, store.DB(), "call_context_fields", 1)
	assertCount(t, store.DB(), "sync_runs", 2)

	var rawSHA string
	if err := store.DB().QueryRowContext(ctx, `SELECT raw_sha256 FROM calls WHERE call_id = ?`, "call-constant").Scan(&rawSHA); err != nil {
		t.Fatalf("query raw sha: %v", err)
	}
	if len(rawSHA) != 64 {
		t.Fatalf("raw_sha256=%q want 64-char sha256", rawSHA)
	}
}

func TestSyncCRMInventoryAndSettings(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/crm/integrations":
			if r.Method != http.MethodGet {
				t.Fatalf("crm integrations method=%q want GET", r.Method)
			}
			writeJSON(t, w, map[string]any{
				"integrations": []map[string]any{
					{
						"integrationId": "crm-int-001",
						"name":          "Salesforce production",
						"crmType":       "Salesforce",
					},
				},
			})
		case "/v2/crm/entity-schema":
			if r.Method != http.MethodGet {
				t.Fatalf("crm schema method=%q want GET", r.Method)
			}
			if got := r.URL.Query().Get("integrationId"); got != "crm-int-001" {
				t.Fatalf("integrationId=%q want crm-int-001", got)
			}
			if got := r.URL.Query().Get("objectType"); got != "DEAL" {
				t.Fatalf("objectType=%q want DEAL", got)
			}
			writeJSON(t, w, map[string]any{
				"requestId": "schema-request-001",
				"objectTypeToSelectedFields": map[string]any{
					"DEAL": []map[string]any{
						{"fieldName": "Amount", "label": "Amount", "fieldType": "currency"},
						{"fieldName": "StageName", "label": "Stage", "fieldType": "picklist"},
					},
				},
			})
		case "/v2/settings/trackers":
			if got := r.URL.Query().Get("workspaceId"); got != "workspace-001" {
				t.Fatalf("workspaceId=%q want workspace-001", got)
			}
			writeJSON(t, w, map[string]any{
				"keywordTrackers": []map[string]any{
					{
						"id":      "tracker-001",
						"name":    "Pricing objection",
						"enabled": true,
					},
				},
			})
		case "/v2/settings/scorecards":
			writeJSON(t, w, map[string]any{
				"scorecards": []map[string]any{
					{
						"scorecardId":   "scorecard-001",
						"scorecardName": "Discovery quality",
						"enabled":       true,
					},
				},
			})
		default:
			t.Fatalf("unexpected path=%q", r.URL.Path)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server)
	integrationsResult, err := SyncCRMIntegrations(ctx, client, store, CRMIntegrationsParams{})
	if err != nil {
		t.Fatalf("SyncCRMIntegrations returned error: %v", err)
	}
	if integrationsResult.RecordsWritten != 1 {
		t.Fatalf("integrations written=%d want 1", integrationsResult.RecordsWritten)
	}

	schemaResult, err := SyncCRMSchema(ctx, client, store, CRMSchemaParams{
		IntegrationID: "crm-int-001",
		ObjectTypes:   []string{"DEAL"},
	})
	if err != nil {
		t.Fatalf("SyncCRMSchema returned error: %v", err)
	}
	if schemaResult.RecordsSeen != 1 || schemaResult.RecordsWritten != 2 {
		t.Fatalf("schema seen/written=%d/%d want 1/2", schemaResult.RecordsSeen, schemaResult.RecordsWritten)
	}

	settingsResult, err := SyncSettings(ctx, client, store, SettingsParams{
		Kind:        "trackers",
		WorkspaceID: "workspace-001",
	})
	if err != nil {
		t.Fatalf("SyncSettings returned error: %v", err)
	}
	if settingsResult.RecordsSeen != 1 || settingsResult.RecordsWritten != 1 {
		t.Fatalf("settings seen/written=%d/%d want 1/1", settingsResult.RecordsSeen, settingsResult.RecordsWritten)
	}
	scorecardsResult, err := SyncSettings(ctx, client, store, SettingsParams{Kind: "scorecards"})
	if err != nil {
		t.Fatalf("SyncSettings scorecards returned error: %v", err)
	}
	if scorecardsResult.RecordsSeen != 1 || scorecardsResult.RecordsWritten != 1 {
		t.Fatalf("scorecards seen/written=%d/%d want 1/1", scorecardsResult.RecordsSeen, scorecardsResult.RecordsWritten)
	}

	assertCount(t, store.DB(), "crm_integrations", 1)
	assertCount(t, store.DB(), "crm_schema_fields", 2)
	assertCount(t, store.DB(), "gong_settings", 2)
	assertCount(t, store.DB(), "sync_runs", 4)
}

func TestSyncUsersPaginatesAndOmitsDefaultLimit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/users" {
			t.Fatalf("path=%q want /v2/users", r.URL.Path)
		}
		if got := r.URL.Query().Get("limit"); got != "" {
			t.Fatalf("limit query=%q want omitted", got)
		}

		requests++
		switch requests {
		case 1:
			if got := r.URL.Query().Get("cursor"); got != "" {
				t.Fatalf("page1 cursor=%q want empty", got)
			}
			writeJSON(t, w, map[string]any{
				"records": map[string]any{
					"currentPageSize":   1,
					"currentPageNumber": 0,
					"cursor":            "users-2",
				},
				"users": []map[string]any{
					{
						"id":           "user-001",
						"emailAddress": "one@example.invalid",
						"firstName":    "One",
						"lastName":     "User",
						"name":         "One User",
						"title":        "AE",
						"active":       true,
					},
				},
			})
		case 2:
			if got := r.URL.Query().Get("cursor"); got != "users-2" {
				t.Fatalf("page2 cursor=%q want users-2", got)
			}
			writeJSON(t, w, map[string]any{
				"records": map[string]any{
					"currentPageSize":   1,
					"currentPageNumber": 1,
				},
				"users": []map[string]any{
					{
						"id":           "user-002",
						"emailAddress": "two@example.invalid",
						"firstName":    "Two",
						"lastName":     "User",
						"name":         "Two User",
						"title":        "CSM",
						"active":       false,
					},
				},
			})
		default:
			t.Fatalf("unexpected request %d", requests)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server)
	result, err := SyncUsers(ctx, client, store, UsersParams{Preset: "full"})
	if err != nil {
		t.Fatalf("SyncUsers returned error: %v", err)
	}
	if result.Pages != 2 {
		t.Fatalf("pages=%d want 2", result.Pages)
	}
	if result.RecordsSeen != 2 || result.RecordsWritten != 2 {
		t.Fatalf("seen/written=%d/%d want 2/2", result.RecordsSeen, result.RecordsWritten)
	}

	assertCount(t, store.DB(), "users", 2)
	assertCount(t, store.DB(), "sync_runs", 1)

	var scope, syncKey, requestContext, status string
	if err := store.DB().QueryRowContext(
		ctx,
		`SELECT scope, sync_key, request_context, status
		   FROM sync_runs
		  ORDER BY id DESC
		  LIMIT 1`,
	).Scan(&scope, &syncKey, &requestContext, &status); err != nil {
		t.Fatalf("query users sync run: %v", err)
	}
	if scope != scopeUsers {
		t.Fatalf("scope=%q want %q", scope, scopeUsers)
	}
	if syncKey != "users:preset=full" {
		t.Fatalf("syncKey=%q want users:preset=full", syncKey)
	}
	if requestContext != "preset=full" {
		t.Fatalf("requestContext=%q want preset=full", requestContext)
	}
	if status != "success" {
		t.Fatalf("status=%q want success", status)
	}
}

func newTestClient(t *testing.T, server *httptest.Server) *gong.Client {
	t.Helper()

	client, err := gong.NewClient(gong.Options{
		BaseURL: server.URL,
		Credentials: auth.Credentials{
			AccessKey:       "key",
			AccessKeySecret: "secret",
		},
		MaxRetries: 0,
	})
	if err != nil {
		t.Fatalf("new test client: %v", err)
	}
	return client
}

func openTestStore(t *testing.T) *sqlite.Store {
	t.Helper()

	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "syncsvc.db"))
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	return store
}

func assertCount(t *testing.T, db *sql.DB, table string, want int) {
	t.Helper()

	var got int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&got); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if got != want {
		t.Fatalf("%s count=%d want %d", table, got, want)
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}

func stringValue(value any) string {
	typed, _ := value.(string)
	return typed
}
