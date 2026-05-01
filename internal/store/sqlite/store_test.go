package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	profilepkg "github.com/fyne-coder/gongcli_mcp/internal/profile"
)

func TestMigrateIdempotent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("second migrate: %v", err)
	}

	var version int
	if err := store.DB().QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatalf("query user_version: %v", err)
	}
	if version != len(migrations) {
		t.Fatalf("user_version=%d want %d", version, len(migrations))
	}

	var name string
	if err := store.DB().QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'calls'`).Scan(&name); err != nil {
		t.Fatalf("calls table missing: %v", err)
	}
	if name != "calls" {
		t.Fatalf("calls table name=%q", name)
	}
}

func TestUpsertIdempotency(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	callRaw := fixtureObject(t, "testdata/fixtures/calls.extensive.sample.json", "calls")
	callRecord, err := store.UpsertCall(ctx, callRaw)
	if err != nil {
		t.Fatalf("first call upsert: %v", err)
	}
	secondCall, err := store.UpsertCall(ctx, callRaw)
	if err != nil {
		t.Fatalf("second call upsert: %v", err)
	}
	if secondCall.CallID != callRecord.CallID {
		t.Fatalf("call id mismatch: %q vs %q", secondCall.CallID, callRecord.CallID)
	}

	userRaw := mustNormalizeValue(t, map[string]any{
		"id":           "user_sanitized_001",
		"emailAddress": "seller@example.invalid",
		"firstName":    "Taylor",
		"lastName":     "Seller",
		"name":         "Taylor Seller",
		"title":        "Account Executive",
		"active":       true,
	})
	userRecord, err := store.UpsertUser(ctx, userRaw)
	if err != nil {
		t.Fatalf("first user upsert: %v", err)
	}
	secondUser, err := store.UpsertUser(ctx, userRaw)
	if err != nil {
		t.Fatalf("second user upsert: %v", err)
	}
	if secondUser.UserID != userRecord.UserID {
		t.Fatalf("user id mismatch: %q vs %q", secondUser.UserID, userRecord.UserID)
	}

	transcriptRaw := readFixture(t, "testdata/fixtures/transcript.sample.json")
	transcriptRecord, err := store.UpsertTranscript(ctx, transcriptRaw)
	if err != nil {
		t.Fatalf("first transcript upsert: %v", err)
	}
	secondTranscript, err := store.UpsertTranscript(ctx, transcriptRaw)
	if err != nil {
		t.Fatalf("second transcript upsert: %v", err)
	}
	if secondTranscript.CallID != transcriptRecord.CallID {
		t.Fatalf("transcript call id mismatch: %q vs %q", secondTranscript.CallID, transcriptRecord.CallID)
	}

	assertCount(t, store.DB(), "calls", 1)
	assertCount(t, store.DB(), "users", 1)
	assertCount(t, store.DB(), "transcripts", 1)
	assertCount(t, store.DB(), "transcript_segments", 2)
	assertCount(t, store.DB(), "transcript_segments_fts", 2)

	missing, err := store.FindCallsMissingTranscripts(ctx, 10)
	if err != nil {
		t.Fatalf("find missing transcripts: %v", err)
	}
	if len(missing) != 0 {
		t.Fatalf("missing transcript count=%d want 0", len(missing))
	}
}

func TestUpsertCallContextObjectNameFallsBackToNameField(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	raw := mustNormalizeValue(t, map[string]any{
		"id":      "call-name-fallback",
		"title":   "Name fallback call",
		"started": "2026-04-29T12:00:00Z",
		"context": []any{
			map[string]any{
				"objectType": "Account",
				"id":         "acct-name-fallback",
				"fields": []any{
					map[string]any{"name": "Name", "value": "Example Account"},
					map[string]any{"name": "Industry", "value": "Manufacturing"},
				},
			},
			map[string]any{
				"objectType": "Opportunity",
				"id":         "opp-name-fallback",
				"fields": []any{
					map[string]any{"name": "Name", "value": "Example Opportunity"},
					map[string]any{"name": "StageName", "value": "Discovery"},
				},
			},
		},
	})
	if _, err := store.UpsertCall(ctx, raw); err != nil {
		t.Fatalf("upsert call: %v", err)
	}

	rows, err := store.DB().QueryContext(ctx, `SELECT object_type, object_name FROM call_context_objects ORDER BY object_type`)
	if err != nil {
		t.Fatalf("query context objects: %v", err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var objectType, objectName string
		if err := rows.Scan(&objectType, &objectName); err != nil {
			t.Fatalf("scan context object: %v", err)
		}
		got[objectType] = objectName
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate context objects: %v", err)
	}
	if got["Account"] != "Example Account" || got["Opportunity"] != "Example Opportunity" {
		t.Fatalf("unexpected object names: %+v", got)
	}
}

func TestInventoryUpsertsAndLists(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	integrationRaw := mustNormalizeValue(t, map[string]any{
		"integrationId": "crm-int-001",
		"name":          "Salesforce production",
		"crmType":       "Salesforce",
	})
	if _, err := store.UpsertCRMIntegration(ctx, integrationRaw); err != nil {
		t.Fatalf("first CRM integration upsert: %v", err)
	}
	if _, err := store.UpsertCRMIntegration(ctx, integrationRaw); err != nil {
		t.Fatalf("second CRM integration upsert: %v", err)
	}

	schemaRaw := mustNormalizeValue(t, map[string]any{
		"requestId": "request-001",
		"objectTypeToSelectedFields": map[string]any{
			"DEAL": []map[string]any{
				{"fieldName": "Amount", "label": "Amount", "fieldType": "currency"},
				{"fieldName": "StageName", "label": "Stage", "fieldType": "picklist"},
			},
		},
	})
	fieldCount, err := store.UpsertCRMSchema(ctx, "crm-int-001", "DEAL", schemaRaw)
	if err != nil {
		t.Fatalf("first CRM schema upsert: %v", err)
	}
	if fieldCount != 2 {
		t.Fatalf("field count=%d want 2", fieldCount)
	}
	if _, err := store.UpsertCRMSchema(ctx, "crm-int-001", "DEAL", schemaRaw); err != nil {
		t.Fatalf("second CRM schema upsert: %v", err)
	}

	settingRaw := mustNormalizeValue(t, map[string]any{
		"trackerId": "tracker-001",
		"name":      "Pricing objection",
		"active":    true,
	})
	if _, err := store.UpsertGongSetting(ctx, "tracker", settingRaw); err != nil {
		t.Fatalf("first setting upsert: %v", err)
	}
	if _, err := store.UpsertGongSetting(ctx, "trackers", settingRaw); err != nil {
		t.Fatalf("second setting upsert: %v", err)
	}
	scorecardRaw := mustNormalizeValue(t, map[string]any{
		"scorecardId":   "scorecard-001",
		"scorecardName": "Discovery quality",
		"enabled":       true,
		"reviewMethod":  "AUTOMATIC",
		"workspaceId":   "workspace-001",
		"questions": []map[string]any{
			{
				"questionId":   "question-001",
				"questionText": "Did the rep confirm pain?",
				"questionType": "SCALE",
				"minRange":     1,
				"maxRange":     5,
			},
		},
	})
	if _, err := store.UpsertGongSetting(ctx, "scorecards", scorecardRaw); err != nil {
		t.Fatalf("scorecard setting upsert: %v", err)
	}

	assertCount(t, store.DB(), "crm_integrations", 1)
	assertCount(t, store.DB(), "crm_schema_objects", 1)
	assertCount(t, store.DB(), "crm_schema_fields", 2)
	assertCount(t, store.DB(), "gong_settings", 2)

	integrations, err := store.ListCRMIntegrations(ctx)
	if err != nil {
		t.Fatalf("ListCRMIntegrations returned error: %v", err)
	}
	if len(integrations) != 1 || integrations[0].IntegrationID != "crm-int-001" || integrations[0].Provider != "Salesforce" {
		t.Fatalf("unexpected integrations: %+v", integrations)
	}

	fields, err := store.ListCRMSchemaFields(ctx, CRMSchemaFieldListParams{
		IntegrationID: "crm-int-001",
		ObjectType:    "DEAL",
		Limit:         10,
	})
	if err != nil {
		t.Fatalf("ListCRMSchemaFields returned error: %v", err)
	}
	if len(fields) != 2 || fields[0].FieldName != "Amount" || fields[1].FieldName != "StageName" {
		t.Fatalf("unexpected schema fields: %+v", fields)
	}

	settings, err := store.ListGongSettings(ctx, GongSettingListParams{Kind: "tracker", Limit: 10})
	if err != nil {
		t.Fatalf("ListGongSettings returned error: %v", err)
	}
	if len(settings) != 1 || settings[0].ObjectID != "tracker-001" || !settings[0].Active {
		t.Fatalf("unexpected settings: %+v", settings)
	}

	scorecards, err := store.ListScorecards(ctx, ScorecardListParams{Limit: 10})
	if err != nil {
		t.Fatalf("ListScorecards returned error: %v", err)
	}
	if len(scorecards) != 1 || scorecards[0].Name != "Discovery quality" || scorecards[0].QuestionCount != 1 {
		t.Fatalf("unexpected scorecards: %+v", scorecards)
	}
	detail, err := store.GetScorecardDetail(ctx, "scorecard-001")
	if err != nil {
		t.Fatalf("GetScorecardDetail returned error: %v", err)
	}
	if len(detail.Questions) != 1 || detail.Questions[0].QuestionText != "Did the rep confirm pain?" {
		t.Fatalf("unexpected scorecard detail: %+v", detail)
	}
}

func TestUpsertCallSupportsGongMetaDataEnvelope(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	callRaw := mustNormalizeValue(t, map[string]any{
		"metaData": map[string]any{
			"id":       "call_metadata_001",
			"title":    "Metadata envelope call",
			"started":  "2026-04-24T14:00:00Z",
			"duration": 1800,
		},
		"context": []any{
			map[string]any{
				"objectType": "Opportunity",
				"id":         "opp_metadata_001",
				"name":       "Metadata opportunity",
				"fields": []any{
					map[string]any{"name": "stage", "value": "Discovery"},
				},
			},
		},
	})

	record, err := store.UpsertCall(ctx, callRaw)
	if err != nil {
		t.Fatalf("metadata envelope call upsert: %v", err)
	}
	if record.CallID != "call_metadata_001" {
		t.Fatalf("CallID=%q want call_metadata_001", record.CallID)
	}
	if record.Title != "Metadata envelope call" {
		t.Fatalf("Title=%q want Metadata envelope call", record.Title)
	}
	if record.StartedAt != "2026-04-24T14:00:00Z" {
		t.Fatalf("StartedAt=%q want 2026-04-24T14:00:00Z", record.StartedAt)
	}
	if record.DurationSeconds != 1800 {
		t.Fatalf("DurationSeconds=%d want 1800", record.DurationSeconds)
	}

	results, err := store.SearchCallsRaw(ctx, CallSearchParams{
		CRMObjectType: "Opportunity",
		CRMObjectID:   "opp_metadata_001",
		Limit:         10,
	})
	if err != nil {
		t.Fatalf("SearchCallsRaw returned error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("CRM search returned %d calls, want 1", len(results))
	}

}

func TestUpsertCallContextExtractsCRM(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	extendedRaw := readFixture(t, "internal/store/sqlite/testdata/call.extended.sample.json")
	withoutContext := removeJSONKey(t, extendedRaw, "context")

	callRecord, err := store.UpsertCall(ctx, withoutContext)
	if err != nil {
		t.Fatalf("base call upsert: %v", err)
	}
	if callRecord.ContextPresent {
		t.Fatalf("context unexpectedly present on base call")
	}

	counts, err := store.UpsertCallContext(ctx, callRecord.CallID, extendedRaw)
	if err != nil {
		t.Fatalf("upsert call context: %v", err)
	}
	if counts.Objects != 2 || counts.Fields != 4 {
		t.Fatalf("context counts=%+v want objects=2 fields=4", counts)
	}

	assertCount(t, store.DB(), "call_context_objects", 2)
	assertCount(t, store.DB(), "call_context_fields", 4)

	var amount string
	if err := store.DB().QueryRowContext(
		ctx,
		`SELECT field_value_text
		   FROM call_context_fields
		  WHERE call_id = ? AND object_key = ? AND field_name = ?`,
		callRecord.CallID,
		"Opportunity:opp_001",
		"amount",
	).Scan(&amount); err != nil {
		t.Fatalf("query CRM amount field: %v", err)
	}
	if amount != "40000" {
		t.Fatalf("amount field=%q want 40000", amount)
	}

	var contextPresent int
	if err := store.DB().QueryRowContext(ctx, `SELECT context_present FROM calls WHERE call_id = ?`, callRecord.CallID).Scan(&contextPresent); err != nil {
		t.Fatalf("query calls.context_present: %v", err)
	}
	if contextPresent != 1 {
		t.Fatalf("context_present=%d want 1", contextPresent)
	}

	detail, err := store.GetCallDetail(ctx, callRecord.CallID)
	if err != nil {
		t.Fatalf("GetCallDetail returned error: %v", err)
	}
	if len(detail.CRMObjects) != 2 {
		t.Fatalf("detail CRM object count=%d want 2: %+v", len(detail.CRMObjects), detail.CRMObjects)
	}
	for _, object := range detail.CRMObjects {
		if object.ObjectType == "Opportunity" {
			if object.FieldCount != 2 || object.PopulatedFieldCount != 2 {
				t.Fatalf("unexpected Opportunity detail object: %+v", object)
			}
			if len(object.FieldNames) == 0 {
				t.Fatalf("Opportunity detail field names missing: %+v", object)
			}
			return
		}
	}
	t.Fatalf("Opportunity detail object missing: %+v", detail.CRMObjects)
}

func TestUpsertCallWithoutContextPreservesCachedCRMRows(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	extendedRaw := readFixture(t, "internal/store/sqlite/testdata/call.extended.sample.json")
	withoutContext := removeJSONKey(t, extendedRaw, "context")

	if _, err := store.UpsertCall(ctx, extendedRaw); err != nil {
		t.Fatalf("extended call upsert: %v", err)
	}
	assertCount(t, store.DB(), "call_context_objects", 2)
	assertCount(t, store.DB(), "call_context_fields", 4)

	callRecord, err := store.UpsertCall(ctx, withoutContext)
	if err != nil {
		t.Fatalf("minimal call upsert: %v", err)
	}
	if !callRecord.ContextPresent {
		t.Fatal("context_present was cleared by context-free upsert")
	}
	assertCount(t, store.DB(), "call_context_objects", 2)
	assertCount(t, store.DB(), "call_context_fields", 4)

	results, err := store.SearchCallsRaw(ctx, CallSearchParams{
		CRMObjectType: "Opportunity",
		CRMObjectID:   "opp_001",
		Limit:         10,
	})
	if err != nil {
		t.Fatalf("SearchCallsRaw returned error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("CRM search returned %d calls, want 1", len(results))
	}

	storedRaw, err := store.GetCallRaw(ctx, "call_extended_001")
	if err != nil {
		t.Fatalf("GetCallRaw returned error: %v", err)
	}
	if !strings.Contains(string(storedRaw), `"context"`) {
		t.Fatalf("context-free upsert replaced richer raw payload: %s", storedRaw)
	}
}

func TestUpsertCallExplicitEmptyContextClearsCachedCRMRows(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	extendedRaw := readFixture(t, "internal/store/sqlite/testdata/call.extended.sample.json")
	if _, err := store.UpsertCall(ctx, extendedRaw); err != nil {
		t.Fatalf("extended call upsert: %v", err)
	}
	assertCount(t, store.DB(), "call_context_objects", 2)
	assertCount(t, store.DB(), "call_context_fields", 4)

	emptyContext := mustNormalizeValue(t, map[string]any{
		"id":      "call_extended_001",
		"title":   "Extended context sample",
		"started": "2026-04-24T12:00:00Z",
		"context": []any{},
	})
	callRecord, err := store.UpsertCall(ctx, emptyContext)
	if err != nil {
		t.Fatalf("empty-context call upsert: %v", err)
	}
	if callRecord.ContextPresent {
		t.Fatal("context_present remained true after explicit empty-context upsert")
	}
	assertCount(t, store.DB(), "call_context_objects", 0)
	assertCount(t, store.DB(), "call_context_fields", 0)
}

func TestCRMDiscoveryAndLateStageSignals(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	fixtures := []json.RawMessage{
		mustNormalizeValue(t, map[string]any{
			"id":      "call-late-001",
			"title":   "Late stage contract call",
			"started": "2026-04-24T12:00:00Z",
			"context": []any{
				map[string]any{
					"objectType": "Opportunity",
					"id":         "opp-late-001",
					"name":       "Late opp",
					"fields": []any{
						map[string]any{"name": "StageName", "value": " contract signing "},
						map[string]any{"name": "ISSUE__c", "value": "Need procurement approval"},
						map[string]any{"name": "Amount", "value": "50000"},
					},
				},
			},
		}),
		mustNormalizeValue(t, map[string]any{
			"id":      "call-early-001",
			"title":   "Early discovery call",
			"started": "2026-04-23T12:00:00Z",
			"context": []any{
				map[string]any{
					"objectType": "Opportunity",
					"id":         "opp-early-001",
					"name":       "Early opp",
					"fields": []any{
						map[string]any{"name": "StageName", "value": "MQL"},
						map[string]any{"name": "Amount", "value": "0"},
					},
				},
			},
		}),
	}
	for _, raw := range fixtures {
		if _, err := store.UpsertCall(ctx, raw); err != nil {
			t.Fatalf("upsert call: %v", err)
		}
	}

	objects, err := store.ListCRMObjectTypes(ctx)
	if err != nil {
		t.Fatalf("ListCRMObjectTypes returned error: %v", err)
	}
	if len(objects) != 1 || objects[0].ObjectType != "Opportunity" || objects[0].ObjectCount != 2 || objects[0].CallCount != 2 {
		t.Fatalf("unexpected object summaries: %+v", objects)
	}

	fields, err := store.ListCRMFields(ctx, "Opportunity", 10)
	if err != nil {
		t.Fatalf("ListCRMFields returned error: %v", err)
	}
	if len(fields) == 0 {
		t.Fatal("field summaries empty")
	}

	matches, err := store.SearchCRMFieldValues(ctx, CRMFieldValueSearchParams{
		ObjectType: "Opportunity",
		FieldName:  "ISSUE__c",
		ValueQuery: "procurement",
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("SearchCRMFieldValues returned error: %v", err)
	}
	if len(matches) != 1 || matches[0].CallID != "call-late-001" {
		t.Fatalf("unexpected field value matches: %+v", matches)
	}

	report, err := store.AnalyzeLateStageSignals(ctx, LateStageSignalParams{
		ObjectType:      "Opportunity",
		LateStageValues: []string{"Contract Signing"},
		Limit:           10,
	})
	if err != nil {
		t.Fatalf("AnalyzeLateStageSignals returned error: %v", err)
	}
	if report.LateCalls != 1 || report.NonLateCalls != 1 {
		t.Fatalf("late/non-late counts=%d/%d want 1/1", report.LateCalls, report.NonLateCalls)
	}
	if report.StageCounts["Contract Signing"] != 1 {
		t.Fatalf("Contract Signing stage count=%d want 1 in %+v", report.StageCounts["Contract Signing"], report.StageCounts)
	}
	if _, ok := report.StageCounts["contract signing"]; ok {
		t.Fatalf("stage counts kept duplicate normalized stage bucket: %+v", report.StageCounts)
	}
	var foundIssue bool
	for _, signal := range report.Signals {
		if signal.FieldName == "ISSUE__c" {
			foundIssue = true
			if signal.LateRate != 1 || signal.NonLateRate != 0 || signal.Lift != 1 {
				t.Fatalf("unexpected ISSUE__c signal: %+v", signal)
			}
		}
		if signal.FieldName == "StageName" {
			t.Fatalf("stage proxy leaked into default signals: %+v", signal)
		}
	}
	if !foundIssue {
		t.Fatalf("ISSUE__c signal missing from %+v", report.Signals)
	}
}

func TestOpportunityAggregateQueries(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	calls := []json.RawMessage{
		mustNormalizeValue(t, map[string]any{
			"id":       "call-opp-covered",
			"title":    "Covered contract call",
			"started":  "2026-04-23T12:00:00Z",
			"duration": 600,
			"context": []any{
				map[string]any{
					"objectType": "Opportunity",
					"id":         "opp-gap-001",
					"name":       "Z Old Gap Opportunity",
					"fields": []any{
						map[string]any{"name": "StageName", "value": "Contract Signing"},
						map[string]any{"name": "Amount", "value": "90000"},
						map[string]any{"name": "CloseDate", "value": "2026-12-31"},
						map[string]any{"name": "OwnerId", "value": "owner-z"},
						map[string]any{"name": "ISSUE__c", "value": "Pricing approval"},
					},
				},
			},
		}),
		mustNormalizeValue(t, map[string]any{
			"id":       "call-opp-missing",
			"title":    "Missing transcript contract call",
			"started":  "2026-04-24T12:00:00Z",
			"duration": 1200,
			"context": []any{
				map[string]any{
					"objectType": "Opportunity",
					"id":         "opp-gap-001",
					"name":       "A Current Gap Opportunity",
					"fields": []any{
						map[string]any{"name": "StageName", "value": "Contract Signing"},
						map[string]any{"name": "Amount", "value": "50000"},
						map[string]any{"name": "CloseDate", "value": "2026-05-15"},
						map[string]any{"name": "OwnerId", "value": "owner-001"},
					},
				},
			},
		}),
		mustNormalizeValue(t, map[string]any{
			"id":       "call-opp-early",
			"title":    "Early opportunity call",
			"started":  "2026-04-22T12:00:00Z",
			"duration": 300,
			"context": []any{
				map[string]any{
					"objectType": "Opportunity",
					"id":         "opp-early-002",
					"name":       "Early Opportunity",
					"fields": []any{
						map[string]any{"name": "StageName", "value": "MQL"},
						map[string]any{"name": "Amount", "value": "0"},
					},
				},
			},
		}),
	}
	for _, raw := range calls {
		if _, err := store.UpsertCall(ctx, raw); err != nil {
			t.Fatalf("upsert call: %v", err)
		}
	}
	transcripts := []json.RawMessage{
		mustNormalizeValue(t, map[string]any{
			"callTranscripts": []any{
				map[string]any{
					"callId": "call-opp-covered",
					"transcript": []any{
						map[string]any{
							"speakerId": "buyer",
							"sentences": []any{
								map[string]any{"start": 1000, "end": 3000, "text": "The pricing objection is tied to procurement timing."},
							},
						},
					},
				},
			},
		}),
		mustNormalizeValue(t, map[string]any{
			"callTranscripts": []any{
				map[string]any{
					"callId": "call-opp-early",
					"transcript": []any{
						map[string]any{
							"speakerId": "buyer",
							"sentences": []any{
								map[string]any{"start": 1000, "end": 3000, "text": "Early discovery notes."},
							},
						},
					},
				},
			},
		}),
	}
	for _, raw := range transcripts {
		if _, err := store.UpsertTranscript(ctx, raw); err != nil {
			t.Fatalf("upsert transcript: %v", err)
		}
	}

	missing, err := store.ListOpportunitiesMissingTranscripts(ctx, OpportunityMissingTranscriptParams{
		StageValues: []string{"Contract Signing"},
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("ListOpportunitiesMissingTranscripts returned error: %v", err)
	}
	if len(missing) != 1 {
		t.Fatalf("missing opportunity count=%d want 1: %+v", len(missing), missing)
	}
	if missing[0].OpportunityID != "opp-gap-001" || missing[0].CallCount != 2 || missing[0].MissingTranscriptCount != 1 || missing[0].TranscriptCount != 1 {
		t.Fatalf("unexpected missing opportunity summary: %+v", missing[0])
	}
	if missing[0].OpportunityName != "A Current Gap Opportunity" {
		t.Fatalf("opportunity name=%q want latest call snapshot", missing[0].OpportunityName)
	}
	if missing[0].LatestCallID != "call-opp-missing" {
		t.Fatalf("latest call id=%q want call-opp-missing", missing[0].LatestCallID)
	}

	transcriptMatches, err := store.SearchTranscriptSegmentsByCRMContext(ctx, TranscriptCRMSearchParams{
		Query:      "pricing",
		ObjectType: "Opportunity",
		ObjectID:   "opp-gap-001",
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("SearchTranscriptSegmentsByCRMContext returned error: %v", err)
	}
	if len(transcriptMatches) != 1 || transcriptMatches[0].CallID != "call-opp-covered" || transcriptMatches[0].ObjectID != "opp-gap-001" {
		t.Fatalf("unexpected transcript CRM matches: %+v", transcriptMatches)
	}
	if transcriptMatches[0].MatchingObjectCount != 1 {
		t.Fatalf("matching object count=%d want 1", transcriptMatches[0].MatchingObjectCount)
	}
	if !strings.Contains(strings.ToLower(transcriptMatches[0].Snippet), "[pricing]") {
		t.Fatalf("snippet=%q missing highlighted term", transcriptMatches[0].Snippet)
	}

	summaries, err := store.SummarizeOpportunityCalls(ctx, OpportunityCallSummaryParams{
		StageValues: []string{"Contract Signing"},
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("SummarizeOpportunityCalls returned error: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("opportunity summary count=%d want 1: %+v", len(summaries), summaries)
	}
	if summaries[0].OpportunityID != "opp-gap-001" || summaries[0].CallCount != 2 || summaries[0].TranscriptCount != 1 || summaries[0].MissingTranscriptCount != 1 || summaries[0].TotalDurationSeconds != 1800 {
		t.Fatalf("unexpected opportunity call summary: %+v", summaries[0])
	}
	if summaries[0].Amount != "50000" || summaries[0].CloseDate != "2026-05-15" || summaries[0].OwnerID != "owner-001" {
		t.Fatalf("unexpected opportunity field summary: %+v", summaries[0])
	}

	matrix, err := store.CRMFieldPopulationMatrix(ctx, CRMFieldPopulationMatrixParams{
		ObjectType:   "Opportunity",
		GroupByField: "StageName",
		Limit:        20,
	})
	if err != nil {
		t.Fatalf("CRMFieldPopulationMatrix returned error: %v", err)
	}
	if matrix.ObjectType != "Opportunity" || matrix.GroupByField != "StageName" {
		t.Fatalf("unexpected matrix header: %+v", matrix)
	}
	var foundAmount bool
	for _, cell := range matrix.Cells {
		if cell.GroupValue == "Contract Signing" && cell.FieldName == "Amount" {
			foundAmount = true
			if cell.ObjectCount != 2 || cell.CallCount != 2 || cell.PopulatedCount != 2 || cell.PopulationRate != 1 {
				t.Fatalf("unexpected amount population cell: %+v", cell)
			}
		}
		if cell.FieldName == "StageName" {
			t.Fatalf("group-by field leaked into matrix cells: %+v", cell)
		}
	}
	if !foundAmount {
		t.Fatalf("amount cell missing from matrix: %+v", matrix.Cells)
	}

	if _, err := store.CRMFieldPopulationMatrix(ctx, CRMFieldPopulationMatrixParams{
		ObjectType:   "Opportunity",
		GroupByField: "OwnerId",
		Limit:        20,
	}); err == nil {
		t.Fatal("CRMFieldPopulationMatrix allowed unsafe group_by_field OwnerId")
	}
}

func TestSearchTranscriptSegmentsByCRMContextDedupesMultiObjectCalls(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	call := mustNormalizeValue(t, map[string]any{
		"id":       "call-multi-object-transcript",
		"title":    "Multi object transcript call",
		"started":  "2026-04-24T12:00:00Z",
		"duration": 900,
		"context": []any{
			map[string]any{
				"objectType": "Opportunity",
				"id":         "opp-multi-a",
				"name":       "Multi A",
				"fields": []any{
					map[string]any{"name": "StageName", "value": "MQL"},
				},
			},
			map[string]any{
				"objectType": "Opportunity",
				"id":         "opp-multi-b",
				"name":       "Multi B",
				"fields": []any{
					map[string]any{"name": "StageName", "value": "Contract Signing"},
				},
			},
		},
	})
	if _, err := store.UpsertCall(ctx, call); err != nil {
		t.Fatalf("upsert call: %v", err)
	}
	transcript := mustNormalizeValue(t, map[string]any{
		"callTranscripts": []any{
			map[string]any{
				"callId": "call-multi-object-transcript",
				"transcript": []any{
					map[string]any{
						"speakerId": "buyer",
						"sentences": []any{
							map[string]any{"start": 1000, "end": 3000, "text": "Pricing appears once."},
						},
					},
				},
			},
		},
	})
	if _, err := store.UpsertTranscript(ctx, transcript); err != nil {
		t.Fatalf("upsert transcript: %v", err)
	}

	results, err := store.SearchTranscriptSegmentsByCRMContext(ctx, TranscriptCRMSearchParams{
		Query:      "pricing",
		ObjectType: "Opportunity",
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("SearchTranscriptSegmentsByCRMContext returned error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("result count=%d want deduped 1: %+v", len(results), results)
	}
	if results[0].MatchingObjectCount != 2 {
		t.Fatalf("matching object count=%d want 2", results[0].MatchingObjectCount)
	}
}

func TestLifecycleQueries(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	calls := []json.RawMessage{
		mustNormalizeValue(t, map[string]any{
			"metaData": map[string]any{
				"id":        "call-life-outbound",
				"title":     "Outbound prospecting call",
				"started":   "2026-04-20T12:00:00Z",
				"duration":  45,
				"system":    "Upload API",
				"direction": "Outbound",
			},
		}),
		mustNormalizeValue(t, map[string]any{
			"id":       "call-life-active",
			"title":    "Active pipeline call",
			"started":  "2026-04-21T12:00:00Z",
			"duration": 900,
			"context": []any{
				map[string]any{
					"objectType": "Opportunity",
					"id":         "opp-life-active",
					"name":       "Active Lifecycle Opportunity",
					"fields": []any{
						map[string]any{"name": "StageName", "value": "MQL"},
						map[string]any{"name": "Type", "value": "New Business"},
						map[string]any{"name": "Primary_Lead_Source__c", "value": "Web"},
					},
				},
			},
		}),
		mustNormalizeValue(t, map[string]any{
			"id":        "call-life-late",
			"title":     "Late lifecycle call",
			"started":   "2026-04-22T12:00:00Z",
			"duration":  1800,
			"system":    "Gong",
			"direction": "Conference",
			"scope":     "External",
			"context": []any{
				map[string]any{
					"objectType": "Opportunity",
					"id":         "opp-life-late",
					"name":       "Late Lifecycle Opportunity",
					"fields": []any{
						map[string]any{"name": "StageName", "value": "Contract Signing"},
						map[string]any{"name": "Type", "value": "New Business"},
						map[string]any{"name": "Procurement_System__c", "value": "Coupa"},
					},
				},
			},
		}),
		mustNormalizeValue(t, map[string]any{
			"id":       "call-life-renewal",
			"title":    "Renewal lifecycle call",
			"started":  "2026-04-23T12:00:00Z",
			"duration": 1200,
			"context": []any{
				map[string]any{
					"objectType": "Opportunity",
					"id":         "opp-life-renewal",
					"name":       "Renewal Lifecycle Opportunity",
					"fields": []any{
						map[string]any{"name": "StageName", "value": "Discovery & Demo (SQO)"},
						map[string]any{"name": "Type", "value": "Renewal"},
						map[string]any{"name": "Renewal_Process__c", "value": "Standard"},
					},
				},
			},
		}),
		mustNormalizeValue(t, map[string]any{
			"id":       "call-life-upsell",
			"title":    "Upsell lifecycle call",
			"started":  "2026-04-24T12:00:00Z",
			"duration": 2400,
			"context": []any{
				map[string]any{
					"objectType": "Opportunity",
					"id":         "opp-life-upsell",
					"name":       "Upsell Lifecycle Opportunity",
					"fields": []any{
						map[string]any{"name": "StageName", "value": "Demo & Business Case"},
						map[string]any{"name": "Type", "value": "Upsell"},
						map[string]any{"name": "One_Year_Upsell__c", "value": "12000"},
					},
				},
			},
		}),
		mustNormalizeValue(t, map[string]any{
			"id":       "call-life-customer",
			"title":    "Customer success lifecycle call",
			"started":  "2026-04-25T12:00:00Z",
			"duration": 1500,
			"context": []any{
				map[string]any{
					"objectType": "Account",
					"id":         "acct-life-customer",
					"name":       "Customer Lifecycle Account",
					"fields": []any{
						map[string]any{"name": "Account_Type__c", "value": "Customer - Active"},
						map[string]any{"name": "Industry", "value": "Manufacturing"},
					},
				},
			},
		}),
	}
	for _, raw := range calls {
		if _, err := store.UpsertCall(ctx, raw); err != nil {
			t.Fatalf("upsert lifecycle call: %v", err)
		}
	}
	if _, err := store.UpsertTranscript(ctx, mustNormalizeValue(t, map[string]any{
		"callTranscripts": []any{
			map[string]any{
				"callId": "call-life-customer",
				"transcript": []any{
					map[string]any{
						"speakerId": "speaker",
						"sentences": []any{
							map[string]any{"start": 1000, "end": 2000, "text": "Customer success notes."},
						},
					},
				},
			},
		},
	})); err != nil {
		t.Fatalf("upsert lifecycle transcript: %v", err)
	}

	definitions, err := store.ListLifecycleBucketDefinitions(ctx)
	if err != nil {
		t.Fatalf("ListLifecycleBucketDefinitions returned error: %v", err)
	}
	if len(definitions) == 0 || definitions[0].Bucket != "outbound_prospecting" {
		t.Fatalf("unexpected lifecycle definitions: %+v", definitions)
	}

	summaries, err := store.SummarizeCallsByLifecycle(ctx, LifecycleSummaryParams{})
	if err != nil {
		t.Fatalf("SummarizeCallsByLifecycle returned error: %v", err)
	}
	counts := map[string]int64{}
	missing := map[string]int64{}
	for _, summary := range summaries {
		counts[summary.Bucket] = summary.CallCount
		missing[summary.Bucket] = summary.MissingTranscriptCount
	}
	for _, bucket := range []string{"outbound_prospecting", "active_sales_pipeline", "late_stage_sales", "renewal", "upsell_expansion", "customer_success_account"} {
		if counts[bucket] != 1 {
			t.Fatalf("bucket %s count=%d want 1 in %+v", bucket, counts[bucket], summaries)
		}
	}
	if missing["customer_success_account"] != 0 {
		t.Fatalf("customer success missing transcript count=%d want 0", missing["customer_success_account"])
	}

	searchRows, err := store.SearchCallsByLifecycle(ctx, LifecycleCallSearchParams{
		Bucket:                 "renewal",
		MissingTranscriptsOnly: true,
		Limit:                  10,
	})
	if err != nil {
		t.Fatalf("SearchCallsByLifecycle returned error: %v", err)
	}
	if len(searchRows) != 1 || searchRows[0].CallID != "call-life-renewal" || searchRows[0].Reason != "Opportunity.Type=Renewal" {
		t.Fatalf("unexpected lifecycle search rows: %+v", searchRows)
	}
	if len(searchRows[0].EvidenceFields) != 1 || searchRows[0].EvidenceFields[0] != "Opportunity.Type" {
		t.Fatalf("unexpected evidence fields: %+v", searchRows[0].EvidenceFields)
	}

	priorities, err := store.PrioritizeTranscriptsByLifecycle(ctx, LifecycleTranscriptPriorityParams{Limit: 10})
	if err != nil {
		t.Fatalf("PrioritizeTranscriptsByLifecycle returned error: %v", err)
	}
	if len(priorities) == 0 || priorities[0].CallID != "call-life-late" {
		t.Fatalf("top transcript priority=%+v want late-stage call", priorities)
	}
	if priorities[0].Scope != "External" || priorities[0].Direction != "Conference" {
		t.Fatalf("priority customer-meeting fields=%+v want External/Conference", priorities[0])
	}

	comparison, err := store.CompareLifecycleCRMFields(ctx, LifecycleCRMFieldComparisonParams{
		BucketA:    "renewal",
		BucketB:    "active_sales_pipeline",
		ObjectType: "Opportunity",
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("CompareLifecycleCRMFields returned error: %v", err)
	}
	var foundRenewalProcess bool
	for _, row := range comparison.Fields {
		if row.FieldName == "Renewal_Process__c" {
			foundRenewalProcess = true
			if row.BucketAPopulated != 1 || row.BucketBPopulated != 0 || row.RateDelta != 1 {
				t.Fatalf("unexpected lifecycle field comparison row: %+v", row)
			}
		}
	}
	if !foundRenewalProcess {
		t.Fatalf("Renewal_Process__c missing from lifecycle comparison: %+v", comparison.Fields)
	}

	if _, err := store.SearchCallsByLifecycle(ctx, LifecycleCallSearchParams{Bucket: "not_real"}); err == nil {
		t.Fatal("SearchCallsByLifecycle allowed unknown bucket")
	}
}

func TestSyncStatusPublicReadinessIsConservative(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	if _, err := store.UpsertCall(ctx, mustNormalizeValue(t, map[string]any{
		"id":        "call-readiness-with-transcript",
		"title":     "Readiness covered call",
		"started":   "2026-04-24T12:00:00Z",
		"duration":  1800,
		"direction": "Conference",
		"scope":     "External",
		"context": []any{
			map[string]any{
				"objectType": "Opportunity",
				"id":         "opp-readiness",
				"fields": []any{
					map[string]any{"name": "Primary_Lead_Source__c", "value": "Web"},
				},
			},
		},
	})); err != nil {
		t.Fatalf("upsert covered call: %v", err)
	}
	if _, err := store.UpsertCall(ctx, mustNormalizeValue(t, map[string]any{
		"id":        "call-readiness-missing-transcript",
		"title":     "Readiness missing call",
		"started":   "2026-04-25T12:00:00Z",
		"duration":  1200,
		"direction": "Conference",
		"scope":     "External",
	})); err != nil {
		t.Fatalf("upsert missing call: %v", err)
	}
	if _, err := store.UpsertTranscript(ctx, mustNormalizeValue(t, map[string]any{
		"callTranscripts": []any{
			map[string]any{
				"callId": "call-readiness-with-transcript",
				"transcript": []any{
					map[string]any{
						"speakerId": "speaker",
						"sentences": []any{
							map[string]any{"start": 1000, "end": 2000, "text": "Readiness transcript."},
						},
					},
				},
			},
		},
	})); err != nil {
		t.Fatalf("upsert transcript: %v", err)
	}

	summary, err := store.SyncStatusSummary(ctx)
	if err != nil {
		t.Fatalf("SyncStatusSummary returned error: %v", err)
	}
	if summary.PublicReadiness.TranscriptCoverage.Ready || summary.PublicReadiness.TranscriptCoverage.Status != "partial" {
		t.Fatalf("transcript readiness=%+v want partial not ready", summary.PublicReadiness.TranscriptCoverage)
	}
	if summary.PublicReadiness.AttributionReadiness.Ready || summary.PublicReadiness.AttributionReadiness.Status != "partial" {
		t.Fatalf("attribution readiness=%+v want partial not ready without profile", summary.PublicReadiness.AttributionReadiness)
	}
	if !strings.Contains(summary.PublicReadiness.RecommendedNextAction, "transcript-backlog") {
		t.Fatalf("recommended next action=%q want transcript backlog", summary.PublicReadiness.RecommendedNextAction)
	}
}

func TestCallFactsSummariesAndCoverage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	calls := []json.RawMessage{
		mustNormalizeValue(t, map[string]any{
			"metaData": map[string]any{
				"id":              "call-facts-external",
				"title":           "External sales call",
				"started":         "2026-04-24T12:00:00Z",
				"duration":        1800,
				"system":          "Zoom",
				"direction":       "Conference",
				"scope":           "External",
				"primaryUserId":   "user-001",
				"calendarEventId": "calendar-001",
			},
			"context": []any{
				map[string]any{
					"objectType": "Account",
					"id":         "acct-facts-001",
					"fields": []any{
						map[string]any{"name": "Account_Type__c", "value": "Prospect - Active"},
						map[string]any{"name": "Industry", "value": "Manufacturing"},
					},
				},
				map[string]any{
					"objectType": "Opportunity",
					"id":         "opp-facts-001",
					"fields": []any{
						map[string]any{"name": "StageName", "value": "Contract Signing"},
						map[string]any{"name": "Type", "value": "New Business"},
						map[string]any{"name": "Primary_Lead_Source__c", "value": "Web"},
					},
				},
			},
		}),
		mustNormalizeValue(t, map[string]any{
			"metaData": map[string]any{
				"id":        "call-facts-internal",
				"title":     "Internal prep call",
				"started":   "2026-04-23T12:00:00Z",
				"duration":  600,
				"system":    "Google Meet",
				"direction": "Conference",
				"scope":     "Internal",
			},
		}),
	}
	for _, raw := range calls {
		if _, err := store.UpsertCall(ctx, raw); err != nil {
			t.Fatalf("upsert call fact: %v", err)
		}
	}
	if _, err := store.UpsertTranscript(ctx, mustNormalizeValue(t, map[string]any{
		"callTranscripts": []any{
			map[string]any{
				"callId": "call-facts-external",
				"transcript": []any{
					map[string]any{
						"speakerId": "speaker",
						"sentences": []any{
							map[string]any{"start": 1000, "end": 2000, "text": "Fact transcript."},
						},
					},
				},
			},
		},
	})); err != nil {
		t.Fatalf("upsert fact transcript: %v", err)
	}

	coverage, err := store.CallFactsCoverage(ctx)
	if err != nil {
		t.Fatalf("CallFactsCoverage returned error: %v", err)
	}
	if coverage.TotalCalls != 2 || coverage.TranscriptCount != 1 || coverage.MissingTranscriptCount != 1 || coverage.ExternalCallCount != 1 || coverage.InternalCallCount != 1 {
		t.Fatalf("unexpected coverage: %+v", coverage)
	}
	if coverage.CalendarCallCount != 1 || coverage.TranscriptCoverageRate != 0.5 {
		t.Fatalf("unexpected coverage rates: %+v", coverage)
	}

	rows, err := store.SummarizeCallFacts(ctx, CallFactsSummaryParams{
		GroupBy: "scope",
		Limit:   10,
	})
	if err != nil {
		t.Fatalf("SummarizeCallFacts returned error: %v", err)
	}
	counts := map[string]int64{}
	for _, row := range rows {
		counts[row.GroupValue] = row.CallCount
		if row.GroupBy != "scope" {
			t.Fatalf("group_by=%q want scope", row.GroupBy)
		}
	}
	if counts["External"] != 1 || counts["Internal"] != 1 {
		t.Fatalf("unexpected scope counts: %+v", rows)
	}

	leadSourceRows, err := store.SummarizeCallFacts(ctx, CallFactsSummaryParams{
		GroupBy:          "lead_source",
		LifecycleBucket:  "late_stage_sales",
		TranscriptStatus: "present",
		Limit:            10,
	})
	if err != nil {
		t.Fatalf("filtered SummarizeCallFacts returned error: %v", err)
	}
	if len(leadSourceRows) != 1 || leadSourceRows[0].GroupValue != "Web" || leadSourceRows[0].CallCount != 1 {
		t.Fatalf("unexpected lead source rows: %+v", leadSourceRows)
	}

	if _, err := store.SummarizeCallFacts(ctx, CallFactsSummaryParams{GroupBy: "raw_sql"}); err == nil {
		t.Fatal("SummarizeCallFacts allowed unsafe group_by")
	}
}

func TestCallFactsUsesOneConsistentRepresentativeCRMObject(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	if _, err := store.UpsertCall(ctx, mustNormalizeValue(t, map[string]any{
		"id":       "call-facts-multi-opportunity",
		"title":    "Multi opportunity fact call",
		"started":  "2026-04-24T12:00:00Z",
		"duration": 1200,
		"context": []any{
			map[string]any{
				"objectType": "Opportunity",
				"id":         "opp-facts-active",
				"fields": []any{
					map[string]any{"name": "StageName", "value": "MQL"},
					map[string]any{"name": "Type", "value": "New Business"},
					map[string]any{"name": "Primary_Lead_Source__c", "value": "Web"},
				},
			},
			map[string]any{
				"objectType": "Opportunity",
				"id":         "opp-facts-late",
				"fields": []any{
					map[string]any{"name": "StageName", "value": "Contract Signing"},
					map[string]any{"name": "Type", "value": "Renewal"},
					map[string]any{"name": "Primary_Lead_Source__c", "value": "Referral"},
				},
			},
		},
	})); err != nil {
		t.Fatalf("upsert multi-opportunity call fact: %v", err)
	}

	var opportunityID, opportunityStage, opportunityType, leadSource string
	if err := store.DB().QueryRowContext(ctx, `
SELECT opportunity_id, opportunity_stage, opportunity_type, opportunity_primary_lead_source
  FROM call_facts
 WHERE call_id = ?`, "call-facts-multi-opportunity").Scan(
		&opportunityID,
		&opportunityStage,
		&opportunityType,
		&leadSource,
	); err != nil {
		t.Fatalf("query call_facts: %v", err)
	}
	if opportunityID != "opp-facts-late" || opportunityStage != "Contract Signing" || opportunityType != "Renewal" || leadSource != "Referral" {
		t.Fatalf("call_facts mixed representative opportunity fields: id=%q stage=%q type=%q lead_source=%q", opportunityID, opportunityStage, opportunityType, leadSource)
	}

	rows, err := store.SummarizeCallFacts(ctx, CallFactsSummaryParams{
		GroupBy: "opportunity_stage",
		Limit:   10,
	})
	if err != nil {
		t.Fatalf("SummarizeCallFacts returned error: %v", err)
	}
	if len(rows) != 1 || rows[0].GroupValue != "Contract Signing" || rows[0].CallCount != 1 {
		t.Fatalf("unexpected stage facts for multi-opportunity call: %+v", rows)
	}
}

func TestLifecycleClassifiesMultiOpportunityCallsByPrecedence(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	raw := mustNormalizeValue(t, map[string]any{
		"id":       "call-life-multi-opportunity",
		"title":    "Multi opportunity lifecycle call",
		"started":  "2026-04-24T12:00:00Z",
		"duration": 1200,
		"context": []any{
			map[string]any{
				"objectType": "Opportunity",
				"id":         "opp-life-active-on-multi",
				"fields": []any{
					map[string]any{"name": "StageName", "value": "MQL"},
					map[string]any{"name": "Type", "value": "New Business"},
				},
			},
			map[string]any{
				"objectType": "Opportunity",
				"id":         "opp-life-late-on-multi",
				"fields": []any{
					map[string]any{"name": "StageName", "value": "Contract Signing"},
					map[string]any{"name": "Type", "value": "New Business"},
				},
			},
		},
	})
	if _, err := store.UpsertCall(ctx, raw); err != nil {
		t.Fatalf("upsert multi-opportunity lifecycle call: %v", err)
	}

	results, err := store.SearchCallsByLifecycle(ctx, LifecycleCallSearchParams{
		Bucket: "late_stage_sales",
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("SearchCallsByLifecycle returned error: %v", err)
	}
	if len(results) != 1 || results[0].CallID != "call-life-multi-opportunity" {
		t.Fatalf("multi-opportunity call was not classified late-stage: %+v", results)
	}
	if results[0].Reason != "Opportunity.StageName is late-stage" {
		t.Fatalf("reason=%q want late-stage reason", results[0].Reason)
	}
}

func TestLateStageSignalsClassifyMultiOpportunityCallsOnce(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	fixtures := []json.RawMessage{
		mustNormalizeValue(t, map[string]any{
			"id":      "call-multi-opp",
			"title":   "Multi opportunity call",
			"started": "2026-04-24T12:00:00Z",
			"context": []any{
				map[string]any{
					"objectType": "Opportunity",
					"id":         "opp-early-on-multi",
					"fields": []any{
						map[string]any{"name": "StageName", "value": "MQL"},
						map[string]any{"name": "MULTI__c", "value": "yes"},
					},
				},
				map[string]any{
					"objectType": "Opportunity",
					"id":         "opp-late-on-multi",
					"fields": []any{
						map[string]any{"name": "StageName", "value": " Contract Signing "},
						map[string]any{"name": "MULTI__c", "value": "yes"},
					},
				},
			},
		}),
		mustNormalizeValue(t, map[string]any{
			"id":      "call-early-only",
			"title":   "Early-only call",
			"started": "2026-04-23T12:00:00Z",
			"context": []any{
				map[string]any{
					"objectType": "Opportunity",
					"id":         "opp-early-only",
					"fields": []any{
						map[string]any{"name": "StageName", "value": "MQL"},
					},
				},
			},
		}),
	}
	for _, raw := range fixtures {
		if _, err := store.UpsertCall(ctx, raw); err != nil {
			t.Fatalf("upsert call: %v", err)
		}
	}

	report, err := store.AnalyzeLateStageSignals(ctx, LateStageSignalParams{
		ObjectType:      "Opportunity",
		LateStageValues: []string{"contract signing"},
		Limit:           10,
	})
	if err != nil {
		t.Fatalf("AnalyzeLateStageSignals returned error: %v", err)
	}
	if report.LateCalls != 1 || report.NonLateCalls != 1 {
		t.Fatalf("late/non-late counts=%d/%d want 1/1", report.LateCalls, report.NonLateCalls)
	}
	for _, signal := range report.Signals {
		if signal.FieldName == "MULTI__c" {
			if signal.LatePopulatedCalls != 1 || signal.NonLatePopulatedCalls != 0 {
				t.Fatalf("multi-object field was counted in both buckets: %+v", signal)
			}
			return
		}
	}
	t.Fatalf("MULTI__c signal missing from %+v", report.Signals)
}

func TestZeroSegmentTranscriptIsNotMissing(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	callRaw := mustNormalizeValue(t, map[string]any{
		"id":      "call-empty-transcript",
		"title":   "Empty transcript",
		"started": "2026-04-24T12:00:00Z",
	})
	if _, err := store.UpsertCall(ctx, callRaw); err != nil {
		t.Fatalf("call upsert: %v", err)
	}
	transcriptRaw := mustNormalizeValue(t, map[string]any{
		"callId":     "call-empty-transcript",
		"transcript": []any{},
	})
	record, err := store.UpsertTranscript(ctx, transcriptRaw)
	if err != nil {
		t.Fatalf("zero segment transcript upsert: %v", err)
	}
	if record.SegmentCount != 0 {
		t.Fatalf("SegmentCount=%d want 0", record.SegmentCount)
	}

	missing, err := store.FindCallsMissingTranscripts(ctx, 10)
	if err != nil {
		t.Fatalf("FindCallsMissingTranscripts returned error: %v", err)
	}
	if len(missing) != 0 {
		t.Fatalf("missing transcript count=%d want 0", len(missing))
	}

	summary, err := store.SyncStatusSummary(ctx)
	if err != nil {
		t.Fatalf("SyncStatusSummary returned error: %v", err)
	}
	if summary.MissingTranscripts != 0 {
		t.Fatalf("MissingTranscripts=%d want 0", summary.MissingTranscripts)
	}
}

func TestSearchTranscriptSegmentsFTS(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	transcriptRaw := readFixture(t, "testdata/fixtures/transcript.sample.json")
	if _, err := store.UpsertTranscript(ctx, transcriptRaw); err != nil {
		t.Fatalf("upsert transcript: %v", err)
	}

	results, err := store.SearchTranscriptSegments(ctx, "external", 10)
	if err != nil {
		t.Fatalf("fts search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("fts result count=%d want 1", len(results))
	}
	if results[0].CallID != "call_sanitized_001" {
		t.Fatalf("fts call id=%q want call_sanitized_001", results[0].CallID)
	}
	if !strings.Contains(strings.ToLower(results[0].Snippet), "[external]") {
		t.Fatalf("fts snippet=%q missing highlighted term", results[0].Snippet)
	}
}

func TestSearchTranscriptSegmentsByCallFactsFiltersDateAndScope(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	for _, raw := range []json.RawMessage{
		mustNormalizeValue(t, map[string]any{
			"metaData": map[string]any{
				"id":        "call-theme-q1-external",
				"title":     "Q1 external theme call",
				"started":   "2026-03-15T15:00:00Z",
				"duration":  1200,
				"system":    "Zoom",
				"direction": "Conference",
				"scope":     "External",
			},
		}),
		mustNormalizeValue(t, map[string]any{
			"metaData": map[string]any{
				"id":        "call-theme-q2-external",
				"title":     "Q2 external theme call",
				"started":   "2026-04-15T15:00:00Z",
				"duration":  1200,
				"system":    "Zoom",
				"direction": "Conference",
				"scope":     "External",
			},
		}),
		mustNormalizeValue(t, map[string]any{
			"metaData": map[string]any{
				"id":        "call-theme-q1-internal",
				"title":     "Q1 internal theme call",
				"started":   "2026-03-16T15:00:00Z",
				"duration":  1200,
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
	for _, callID := range []string{"call-theme-q1-external", "call-theme-q2-external", "call-theme-q1-internal"} {
		if _, err := store.UpsertTranscript(ctx, mustNormalizeValue(t, map[string]any{
			"callTranscripts": []any{
				map[string]any{
					"callId": callID,
					"transcript": []any{
						map[string]any{
							"speakerId": "speaker",
							"sentences": []any{
								map[string]any{"start": 1000, "end": 2000, "text": "Security review is a blocker."},
							},
						},
					},
				},
			},
		})); err != nil {
			t.Fatalf("upsert theme transcript: %v", err)
		}
	}

	results, err := store.SearchTranscriptSegmentsByCallFacts(ctx, TranscriptCallFactsSearchParams{
		Query:    "security",
		FromDate: "2026-01-01",
		ToDate:   "2026-03-31",
		Scope:    "External",
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("SearchTranscriptSegmentsByCallFacts returned error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("result count=%d want 1: %+v", len(results), results)
	}
	if results[0].CallDate != "2026-03-15" || results[0].Scope != "External" {
		t.Fatalf("unexpected filtered result: %+v", results[0])
	}
	if !strings.Contains(strings.ToLower(results[0].Snippet), "security") {
		t.Fatalf("snippet=%q missing highlighted query", results[0].Snippet)
	}
	if !strings.Contains(strings.ToLower(results[0].ContextExcerpt), "security review is a blocker") {
		t.Fatalf("context excerpt=%q missing bounded transcript context", results[0].ContextExcerpt)
	}
}

func TestSearchTranscriptQuotesWithAttributionJoinsCRMMetadata(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	callRaw := mustNormalizeValue(t, map[string]any{
		"id":       "call-attribution",
		"title":    "Attribution evidence call",
		"started":  "2026-02-12T15:00:00Z",
		"duration": 1800,
		"context": []any{
			map[string]any{
				"objectType": "Account",
				"id":         "acct-attribution",
				"fields": []any{
					map[string]any{"name": "Name", "value": "Example Manufacturing"},
					map[string]any{"name": "Industry", "value": "Manufacturing"},
					map[string]any{"name": "Website", "value": "https://example.invalid"},
				},
			},
			map[string]any{
				"objectType": "Opportunity",
				"id":         "opp-attribution",
				"fields": []any{
					map[string]any{"name": "Name", "value": "Example Deal"},
					map[string]any{"name": "StageName", "value": "Discovery"},
					map[string]any{"name": "Type", "value": "New Business"},
					map[string]any{"name": "CloseDate", "value": "2026-03-31"},
					map[string]any{"name": "Probability", "value": "25"},
				},
			},
		},
	})
	if _, err := store.UpsertCall(ctx, callRaw); err != nil {
		t.Fatalf("upsert attribution call: %v", err)
	}
	if _, err := store.UpsertCall(ctx, mustNormalizeValue(t, map[string]any{
		"id":      "call-attribution-with-parties",
		"title":   "Different call with parties",
		"started": "2026-02-13T15:00:00Z",
		"parties": []any{
			map[string]any{"speakerId": "rep", "title": "Account Executive"},
		},
	})); err != nil {
		t.Fatalf("upsert attribution parties call: %v", err)
	}
	transcriptRaw := mustNormalizeValue(t, map[string]any{
		"callTranscripts": []any{
			map[string]any{
				"callId": "call-attribution",
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
	})
	if _, err := store.UpsertTranscript(ctx, transcriptRaw); err != nil {
		t.Fatalf("upsert attribution transcript: %v", err)
	}

	results, err := store.SearchTranscriptQuotesWithAttribution(ctx, TranscriptAttributionSearchParams{
		Query:            "implementation",
		FromDate:         "2026-01-01",
		ToDate:           "2026-03-31",
		Industry:         "manufacturing",
		OpportunityStage: "Discovery",
		Limit:            10,
	})
	if err != nil {
		t.Fatalf("SearchTranscriptQuotesWithAttribution returned error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("result count=%d want 1: %+v", len(results), results)
	}
	row := results[0]
	if row.CallID != "call-attribution" || row.Title != "Attribution evidence call" || row.AccountName != "Example Manufacturing" || row.AccountIndustry != "Manufacturing" || row.OpportunityName != "Example Deal" || row.OpportunityStage != "Discovery" {
		t.Fatalf("unexpected attribution row: %+v", row)
	}
	if row.ParticipantStatus != "missing_from_cache" || row.PersonTitleStatus != "missing_from_cache" {
		t.Fatalf("unexpected person/title status: %+v", row)
	}
	if !strings.Contains(strings.ToLower(row.ContextExcerpt), "implementation timeline") {
		t.Fatalf("context excerpt=%q missing evidence", row.ContextExcerpt)
	}
}

func TestSearchTranscriptQuotesWithAttributionKeepsMultiObjectFieldsTogether(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	if _, err := store.UpsertCall(ctx, mustNormalizeValue(t, map[string]any{
		"id":      "call-multi-attribution",
		"title":   "Multi object attribution",
		"started": "2026-02-14T15:00:00Z",
		"context": []any{
			map[string]any{
				"objectType": "Opportunity",
				"id":         "opp-a",
				"fields": []any{
					map[string]any{"name": "Name", "value": "Opportunity A"},
					map[string]any{"name": "StageName", "value": "Stage A"},
				},
			},
			map[string]any{
				"objectType": "Opportunity",
				"id":         "opp-b",
				"fields": []any{
					map[string]any{"name": "Name", "value": "Opportunity B"},
					map[string]any{"name": "StageName", "value": "Stage B"},
				},
			},
		},
	})); err != nil {
		t.Fatalf("upsert multi attribution call: %v", err)
	}
	if _, err := store.UpsertTranscript(ctx, mustNormalizeValue(t, map[string]any{
		"callTranscripts": []any{
			map[string]any{
				"callId": "call-multi-attribution",
				"transcript": []any{
					map[string]any{
						"speakerId": "buyer",
						"sentences": []any{
							map[string]any{"start": 1000, "end": 2000, "text": "Implementation detail matters."},
						},
					},
				},
			},
		},
	})); err != nil {
		t.Fatalf("upsert multi attribution transcript: %v", err)
	}

	results, err := store.SearchTranscriptQuotesWithAttribution(ctx, TranscriptAttributionSearchParams{
		Query: "implementation",
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("SearchTranscriptQuotesWithAttribution returned error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("result count=%d want 1: %+v", len(results), results)
	}
	if results[0].OpportunityName != "Opportunity A" || results[0].OpportunityStage != "Stage A" {
		t.Fatalf("opportunity fields were mixed across objects: %+v", results[0])
	}

	filtered, err := store.SearchTranscriptQuotesWithAttribution(ctx, TranscriptAttributionSearchParams{
		Query:            "implementation",
		OpportunityStage: "Stage B",
		Limit:            10,
	})
	if err != nil {
		t.Fatalf("filtered SearchTranscriptQuotesWithAttribution returned error: %v", err)
	}
	if len(filtered) != 1 {
		t.Fatalf("filtered result count=%d want 1: %+v", len(filtered), filtered)
	}
	if filtered[0].OpportunityName != "Opportunity B" || filtered[0].OpportunityStage != "Stage B" {
		t.Fatalf("filtered opportunity fields did not come from matched object: %+v", filtered[0])
	}
}

func TestUpsertCallWithoutPartiesDoesNotErasePriorPartyCount(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	if _, err := store.UpsertCall(ctx, mustNormalizeValue(t, map[string]any{
		"id":      "call-party-preserve",
		"title":   "Party preserve",
		"started": "2026-02-15T15:00:00Z",
		"parties": []any{
			map[string]any{"speakerId": "buyer", "title": "VP Operations"},
		},
		"context": []any{
			map[string]any{
				"objectType": "Account",
				"id":         "acct-party-preserve",
				"fields": []any{
					map[string]any{"name": "Name", "value": "Party Account"},
				},
			},
		},
	})); err != nil {
		t.Fatalf("upsert full party call: %v", err)
	}
	if _, err := store.UpsertCall(ctx, mustNormalizeValue(t, map[string]any{
		"id":      "call-party-preserve",
		"title":   "Party preserve",
		"started": "2026-02-15T15:00:00Z",
	})); err != nil {
		t.Fatalf("upsert reduced party call: %v", err)
	}

	detail, err := store.GetCallDetail(ctx, "call-party-preserve")
	if err != nil {
		t.Fatalf("GetCallDetail returned error: %v", err)
	}
	if detail.PartiesCount != 1 {
		t.Fatalf("PartiesCount=%d want preserved 1", detail.PartiesCount)
	}
}

func TestSearchTranscriptSegmentsCapsLimit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	transcriptRaw := readFixture(t, "testdata/fixtures/transcript.sample.json")
	if _, err := store.UpsertTranscript(ctx, transcriptRaw); err != nil {
		t.Fatalf("upsert transcript: %v", err)
	}

	results, err := store.SearchTranscriptSegments(ctx, "external", maxTranscriptSearchLimit+1)
	if err != nil {
		t.Fatalf("fts search: %v", err)
	}
	if len(results) > maxTranscriptSearchLimit {
		t.Fatalf("result count=%d want <= %d", len(results), maxTranscriptSearchLimit)
	}
}

func TestOpenReadOnlyRejectsWrites(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "readonly.db")
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("open writable store: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close writable store: %v", err)
	}

	readOnly, err := OpenReadOnly(ctx, path)
	if err != nil {
		t.Fatalf("open read-only store: %v", err)
	}
	defer readOnly.Close()

	_, err = readOnly.DB().ExecContext(ctx, `INSERT INTO calls(call_id, raw_json, raw_sha256, first_seen_at, updated_at) VALUES('write-test', '{}', '', '', '')`)
	if err == nil {
		t.Fatal("read-only store allowed write")
	}
}

func TestSyncRunLifecycleAndSummary(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	run, err := store.StartSyncRun(ctx, StartSyncRunParams{
		Scope:          "calls",
		SyncKey:        "calls:extended",
		From:           "2026-04-01",
		To:             "2026-04-24",
		RequestContext: "extended",
	})
	if err != nil {
		t.Fatalf("start sync run: %v", err)
	}

	if err := store.FinishSyncRun(ctx, run.ID, FinishSyncRunParams{
		Status:         "success",
		Cursor:         "cursor_002",
		RecordsSeen:    12,
		RecordsWritten: 11,
	}); err != nil {
		t.Fatalf("finish sync run: %v", err)
	}

	summary, err := store.SyncStatusSummary(ctx)
	if err != nil {
		t.Fatalf("sync status summary: %v", err)
	}
	if summary.RunningSyncRuns != 0 {
		t.Fatalf("running sync runs=%d want 0", summary.RunningSyncRuns)
	}
	if summary.LastRun == nil || summary.LastRun.Status != "success" {
		t.Fatalf("last run=%+v want successful run", summary.LastRun)
	}
	if summary.LastSuccessfulRun == nil || summary.LastSuccessfulRun.ID != run.ID {
		t.Fatalf("last successful run=%+v want run id %d", summary.LastSuccessfulRun, run.ID)
	}
	if len(summary.States) != 1 {
		t.Fatalf("sync state count=%d want 1", len(summary.States))
	}
	if summary.States[0].Cursor != "cursor_002" {
		t.Fatalf("sync state cursor=%q want cursor_002", summary.States[0].Cursor)
	}
}

func TestCacheInventoryReportsTableCountsAndDates(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	if _, err := store.UpsertCall(ctx, mustNormalizeValue(t, map[string]any{
		"id":       "call-inventory-001",
		"title":    "Inventory first call",
		"started":  "2026-04-20T14:00:00Z",
		"duration": 1800,
		"metaData": map[string]any{
			"system":    "Zoom",
			"direction": "Conference",
			"scope":     "External",
		},
		"context": map[string]any{
			"crmObjects": []map[string]any{
				{
					"type": "Opportunity",
					"id":   "opp-inventory-001",
					"name": "Inventory opp",
					"fields": []map[string]any{
						{"name": "StageName", "label": "Stage", "value": "Proposal"},
					},
				},
			},
		},
	})); err != nil {
		t.Fatalf("UpsertCall(first) returned error: %v", err)
	}
	if _, err := store.UpsertCall(ctx, mustNormalizeValue(t, map[string]any{
		"id":       "call-inventory-002",
		"title":    "Inventory second call",
		"started":  "2026-04-24T14:00:00Z",
		"duration": 900,
		"metaData": map[string]any{
			"system":    "Zoom",
			"direction": "Conference",
			"scope":     "External",
		},
	})); err != nil {
		t.Fatalf("UpsertCall(second) returned error: %v", err)
	}
	if _, err := store.UpsertTranscript(ctx, mustNormalizeValue(t, map[string]any{
		"callId": "call-inventory-001",
		"transcript": []any{
			map[string]any{
				"speakerId": "spk-1",
				"sentences": []map[string]any{
					{"start": 0, "end": 1000, "text": "Inventory transcript sentence."},
				},
			},
		},
	})); err != nil {
		t.Fatalf("UpsertTranscript returned error: %v", err)
	}
	run, err := store.StartSyncRun(ctx, StartSyncRunParams{
		Scope:   "calls",
		SyncKey: "calls:preset=minimal",
		From:    "2026-04-20",
		To:      "2026-04-24",
	})
	if err != nil {
		t.Fatalf("StartSyncRun returned error: %v", err)
	}
	if err := store.FinishSyncRun(ctx, run.ID, FinishSyncRunParams{
		Status:         "success",
		RecordsSeen:    2,
		RecordsWritten: 2,
	}); err != nil {
		t.Fatalf("FinishSyncRun returned error: %v", err)
	}

	inventory, err := store.CacheInventory(ctx)
	if err != nil {
		t.Fatalf("CacheInventory returned error: %v", err)
	}
	if inventory.Summary == nil {
		t.Fatal("CacheInventory summary was nil")
	}
	if inventory.OldestCallStartedAt != "2026-04-20T14:00:00Z" || inventory.NewestCallStartedAt != "2026-04-24T14:00:00Z" {
		t.Fatalf("unexpected call range: oldest=%q newest=%q", inventory.OldestCallStartedAt, inventory.NewestCallStartedAt)
	}
	if inventory.Summary.TotalCalls != 2 || inventory.Summary.TotalTranscripts != 1 || inventory.Summary.MissingTranscripts != 1 {
		t.Fatalf("unexpected inventory summary: %+v", inventory.Summary)
	}
	if inventory.Summary.ProfileReadiness.Status != "not_configured" {
		t.Fatalf("profile status=%q want not_configured", inventory.Summary.ProfileReadiness.Status)
	}

	tableCounts := map[string]int64{}
	for _, table := range inventory.TableCounts {
		tableCounts[table.Table] = table.Rows
	}
	if tableCounts["calls"] != 2 || tableCounts["call_context_fields"] != 1 || tableCounts["transcripts"] != 1 || tableCounts["sync_runs"] != 1 {
		t.Fatalf("unexpected inventory table counts: %+v", tableCounts)
	}
	if _, ok := tableCounts["transcript_segments_fts"]; ok {
		t.Fatalf("inventory unexpectedly included FTS virtual table: %+v", tableCounts)
	}
}

func TestCachePurgeBeforePlansAndDeletesCacheRows(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	if _, err := store.UpsertCall(ctx, mustNormalizeValue(t, map[string]any{
		"id":       "call-purge-store-old",
		"title":    "Store purge old call",
		"started":  "2026-04-20T14:00:00Z",
		"duration": 1800,
		"metaData": map[string]any{
			"system":    "Zoom",
			"direction": "Conference",
			"scope":     "External",
		},
		"context": map[string]any{
			"crmObjects": []map[string]any{
				{
					"type": "Opportunity",
					"id":   "opp-purge-store-old",
					"name": "Old purge opportunity",
					"fields": []map[string]any{
						{"name": "StageName", "label": "Stage", "value": "Discovery"},
					},
				},
			},
		},
	})); err != nil {
		t.Fatalf("UpsertCall(old) returned error: %v", err)
	}
	if _, err := store.UpsertCall(ctx, mustNormalizeValue(t, map[string]any{
		"id":       "call-purge-store-new",
		"title":    "Store purge new call",
		"started":  "2026-04-24T14:00:00Z",
		"duration": 900,
		"metaData": map[string]any{
			"system":    "Zoom",
			"direction": "Conference",
			"scope":     "External",
		},
	})); err != nil {
		t.Fatalf("UpsertCall(new) returned error: %v", err)
	}
	if _, err := store.UpsertTranscript(ctx, mustNormalizeValue(t, map[string]any{
		"callId": "call-purge-store-old",
		"transcript": []any{
			map[string]any{
				"speakerId": "spk-1",
				"sentences": []map[string]any{
					{"start": 0, "end": 1000, "text": "needle old purge transcript"},
				},
			},
		},
	})); err != nil {
		t.Fatalf("UpsertTranscript returned error: %v", err)
	}

	plan, err := store.PlanCachePurgeBefore(ctx, "2026-04-22")
	if err != nil {
		t.Fatalf("PlanCachePurgeBefore returned error: %v", err)
	}
	if plan.CallCount != 1 || plan.TranscriptCount != 1 || plan.TranscriptSegmentCount != 1 || plan.ContextObjectCount != 1 || plan.ContextFieldCount != 1 {
		t.Fatalf("unexpected purge plan: %+v", plan)
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
	if executed.CallCount != 1 || executed.TranscriptSegmentCount != 1 {
		t.Fatalf("unexpected executed purge plan: %+v", executed)
	}
	var secureDelete int
	if err := store.DB().QueryRowContext(ctx, `PRAGMA secure_delete`).Scan(&secureDelete); err != nil {
		t.Fatalf("query secure_delete returned error: %v", err)
	}
	if secureDelete == 0 {
		t.Fatal("secure_delete pragma was not enabled during purge")
	}

	inventory, err := store.CacheInventory(ctx)
	if err != nil {
		t.Fatalf("CacheInventory after purge returned error: %v", err)
	}
	if inventory.Summary.TotalCalls != 1 || inventory.Summary.TotalTranscripts != 0 || inventory.Summary.TotalEmbeddedCRMFields != 0 {
		t.Fatalf("unexpected summary after purge: %+v", inventory.Summary)
	}
	if inventory.OldestCallStartedAt != "2026-04-24T14:00:00Z" || inventory.NewestCallStartedAt != "2026-04-24T14:00:00Z" {
		t.Fatalf("unexpected remaining date range: oldest=%q newest=%q", inventory.OldestCallStartedAt, inventory.NewestCallStartedAt)
	}
	results, err = store.SearchTranscriptSegments(ctx, "needle", 10)
	if err != nil {
		t.Fatalf("SearchTranscriptSegments after purge returned error: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("search results after purge=%d want 0", len(results))
	}
}

func TestExportGovernanceFilteredDBPhysicallyRemovesSuppressedRows(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "source.db")
	outputPath := filepath.Join(dir, "governed.db")
	store, err := Open(ctx, sourcePath)
	if err != nil {
		t.Fatalf("Open source returned error: %v", err)
	}
	if _, err := store.UpsertCall(ctx, mustNormalizeValue(t, map[string]any{
		"id":       "call-governance-export-blocked",
		"title":    "Blocked export call",
		"started":  "2026-04-20T14:00:00Z",
		"duration": 1800,
		"context": map[string]any{
			"crmObjects": []map[string]any{
				{
					"type": "Account",
					"id":   "acct-governance-export-blocked",
					"name": "Blocked Export Corp",
					"fields": []map[string]any{
						{"name": "Name", "label": "Name", "value": "Blocked Export Corp"},
					},
				},
			},
		},
	})); err != nil {
		t.Fatalf("UpsertCall blocked returned error: %v", err)
	}
	if _, err := store.UpsertTranscript(ctx, mustNormalizeValue(t, map[string]any{
		"callId": "call-governance-export-blocked",
		"transcript": []any{
			map[string]any{
				"speakerId": "speaker-blocked",
				"sentences": []map[string]any{
					{"start": 0, "end": 1000, "text": "blocked export transcript needle"},
				},
			},
		},
	})); err != nil {
		t.Fatalf("UpsertTranscript blocked returned error: %v", err)
	}
	if _, err := store.UpsertCall(ctx, mustNormalizeValue(t, map[string]any{
		"id":       "call-governance-export-allowed",
		"title":    "Allowed export call",
		"started":  "2026-04-24T14:00:00Z",
		"duration": 900,
		"context": map[string]any{
			"crmObjects": []map[string]any{
				{
					"type": "Account",
					"id":   "acct-governance-export-allowed",
					"name": "Allowed Export Corp",
					"fields": []map[string]any{
						{"name": "Name", "label": "Name", "value": "Allowed Export Corp"},
					},
				},
			},
		},
	})); err != nil {
		t.Fatalf("UpsertCall allowed returned error: %v", err)
	}
	if _, err := store.UpsertCall(ctx, mustNormalizeValue(t, map[string]any{
		"id":      "call-governance-export-email",
		"title":   "Allowed account with restricted email domain",
		"started": "2026-04-24T15:00:00Z",
		"parties": []any{
			map[string]any{"speakerId": "buyer-email", "emailAddress": "buyer@blockedexport.example"},
		},
		"context": map[string]any{
			"crmObjects": []map[string]any{
				{
					"type": "Account",
					"id":   "acct-governance-export-email",
					"name": "Allowed Email Corp",
					"fields": []map[string]any{
						{"name": "Name", "label": "Name", "value": "Allowed Email Corp"},
					},
				},
			},
		},
	})); err != nil {
		t.Fatalf("UpsertCall email returned error: %v", err)
	}
	if _, err := store.UpsertCall(ctx, mustNormalizeValue(t, map[string]any{
		"id":       "call-governance-export-mentioned",
		"title":    "Allowed account mentions restricted company",
		"started":  "2026-04-25T14:00:00Z",
		"duration": 600,
		"context": map[string]any{
			"crmObjects": []map[string]any{
				{
					"type": "Account",
					"id":   "acct-governance-export-mentioned",
					"name": "Allowed Mention Corp",
					"fields": []map[string]any{
						{"name": "Name", "label": "Name", "value": "Allowed Mention Corp"},
					},
				},
			},
		},
	})); err != nil {
		t.Fatalf("UpsertCall mentioned returned error: %v", err)
	}
	if _, err := store.UpsertTranscript(ctx, mustNormalizeValue(t, map[string]any{
		"callId": "call-governance-export-mentioned",
		"transcript": []any{
			map[string]any{
				"speakerId": "speaker-mentioned",
				"sentences": []map[string]any{
					{"start": 0, "end": 1000, "text": "the team compared this to Blocked Export Corp"},
				},
			},
		},
	})); err != nil {
		t.Fatalf("UpsertTranscript mentioned returned error: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close source returned error: %v", err)
	}
	if err := os.Chmod(sourcePath, 0400); err != nil {
		t.Fatalf("chmod source read-only: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(sourcePath, 0600)
	})

	plan, err := ExportGovernanceFilteredDB(ctx, sourcePath, outputPath, []string{"call-governance-export-blocked", "call-governance-export-mentioned", "call-governance-export-email"}, false)
	if err != nil {
		t.Fatalf("ExportGovernanceFilteredDB returned error: %v", err)
	}
	if plan.DeletedCalls != 3 || plan.DeletedTranscripts != 2 || plan.DeletedTranscriptSegments != 2 || plan.RemainingSuppressedCandidates != 0 {
		t.Fatalf("unexpected export plan: %+v", plan)
	}

	filtered, err := OpenReadOnly(ctx, outputPath)
	if err != nil {
		t.Fatalf("OpenReadOnly filtered returned error: %v", err)
	}
	defer filtered.Close()
	inventory, err := filtered.CacheInventory(ctx)
	if err != nil {
		t.Fatalf("CacheInventory filtered returned error: %v", err)
	}
	if inventory.Summary.TotalCalls != 1 || inventory.Summary.TotalTranscripts != 0 || inventory.Summary.TotalEmbeddedCRMContextCalls != 1 {
		t.Fatalf("unexpected filtered summary: %+v", inventory.Summary)
	}
	results, err := filtered.SearchTranscriptSegments(ctx, "blocked", 10)
	if err != nil {
		t.Fatalf("SearchTranscriptSegments filtered returned error: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("filtered DB still returned blocked transcript results: %+v", results)
	}
	candidates, err := filtered.GovernanceNameCandidates(ctx)
	if err != nil {
		t.Fatalf("GovernanceNameCandidates filtered returned error: %v", err)
	}
	for _, candidate := range candidates {
		if strings.Contains(candidate.Value, "Blocked Export Corp") || candidate.CallID == "call-governance-export-blocked" {
			t.Fatalf("filtered DB retained blocked governance candidate: %+v", candidate)
		}
	}
}

func TestGovernanceSuppressedCallIDTableRegistryCoversSchema(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	tables, err := schemaTablesWithColumn(ctx, store, "call_id")
	if err != nil {
		t.Fatalf("schemaTablesWithColumn returned error: %v", err)
	}
	registry := governanceCallIDTableRegistry()
	var missing []string
	for _, table := range tables {
		if _, ok := registry[table]; !ok {
			missing = append(missing, table)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("governance call_id registry missing tables: %s", strings.Join(missing, ", "))
	}
	var extra []string
	tableSet := make(map[string]struct{}, len(tables))
	for _, table := range tables {
		tableSet[table] = struct{}{}
	}
	for table := range registry {
		if _, ok := tableSet[table]; !ok {
			extra = append(extra, table)
		}
	}
	sort.Strings(extra)
	if len(extra) > 0 {
		t.Fatalf("governance call_id registry includes tables not present in schema: %s", strings.Join(extra, ", "))
	}
}

func schemaTablesWithColumn(ctx context.Context, store *Store, column string) ([]string, error) {
	rows, err := store.DB().QueryContext(ctx, `SELECT name FROM sqlite_schema WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, err
	}

	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			_ = rows.Close()
			return nil, err
		}
		tables = append(tables, table)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []string
	for _, table := range tables {
		infoRows, err := store.DB().QueryContext(ctx, `PRAGMA table_info(`+quoteSQLiteIdentForTest(table)+`)`)
		if err != nil {
			return nil, err
		}
		hasColumn := false
		for infoRows.Next() {
			var cid int
			var name, typeName string
			var notNull int
			var defaultValue any
			var pk int
			if err := infoRows.Scan(&cid, &name, &typeName, &notNull, &defaultValue, &pk); err != nil {
				_ = infoRows.Close()
				return nil, err
			}
			if name == column {
				hasColumn = true
			}
		}
		if err := infoRows.Close(); err != nil {
			return nil, err
		}
		if hasColumn {
			out = append(out, table)
		}
	}
	return out, nil
}

func quoteSQLiteIdentForTest(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func TestProfileImportAndProfileAwareFacts(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	if _, err := store.UpsertCall(ctx, mustNormalizeValue(t, map[string]any{
		"id":       "call_profile_custom_deal",
		"title":    "Custom CRM profile call",
		"started":  "2026-04-24T14:00:00Z",
		"duration": 1800,
		"metaData": map[string]any{
			"system":    "Zoom",
			"direction": "Conference",
			"scope":     "External",
		},
		"context": map[string]any{
			"crmObjects": []map[string]any{
				{
					"type": "Deal",
					"id":   "deal-profile-001",
					"name": "Profile deal",
					"fields": []map[string]any{
						{"name": "DealPhase__c", "label": "Stage", "value": "Negotiation"},
						{"name": "Amount", "label": "Amount", "value": "10000"},
					},
				},
				{
					"type": "Company",
					"id":   "company-profile-001",
					"name": "Profile company",
					"fields": []map[string]any{
						{"name": "Customer_Status__c", "label": "Customer Status", "value": "Customer - Active"},
					},
				},
			},
		},
	})); err != nil {
		t.Fatalf("UpsertCall returned error: %v", err)
	}

	body := []byte(`
version: 1
name: Custom test profile
objects:
  deal:
    object_types: [Deal]
  account:
    object_types: [Company]
fields:
  deal_stage:
    object: deal
    names: [DealPhase__c]
  account_type:
    object: account
    names: [Customer_Status__c]
lifecycle:
  open:
    order: 10
    rules:
      - object: deal
        field_name: DealPhase__c
        op: in
        values: [Negotiation]
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
    fields:
      - object: deal
        name: DealPhase__c
`)
	inventory, err := store.ProfileInventory(ctx)
	if err != nil {
		t.Fatalf("ProfileInventory returned error: %v", err)
	}
	p, validation, err := profilepkg.ValidateBytes(body, inventory)
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
	result, err := store.ImportProfile(ctx, ProfileImportParams{
		SourcePath:      "gongctl-profile.yaml",
		SourceSHA256:    validation.SourceSHA256,
		CanonicalSHA256: validation.CanonicalSHA256,
		RawYAML:         body,
		CanonicalJSON:   canonical,
		Profile:         p,
		Findings:        validation.Findings,
		ImportedBy:      "test",
	})
	if err != nil {
		t.Fatalf("ImportProfile returned error: %v", err)
	}
	if !result.Imported || result.ProfileID == 0 {
		t.Fatalf("unexpected import result: %+v", result)
	}
	reformattedBody := []byte("version: 1\nname: Custom test profile\nobjects: {deal: {object_types: [Deal]}, account: {object_types: [Company]}}\nfields: {deal_stage: {object: deal, names: [DealPhase__c]}, account_type: {object: account, names: [Customer_Status__c]}}\nlifecycle: {open: {order: 10, rules: [{object: deal, field_name: DealPhase__c, op: in, values: [Negotiation]}]}, closed_won: {order: 20}, closed_lost: {order: 30}, post_sales: {order: 40, rules: [{field: account_type, op: iprefix, value: customer}]}, unknown: {order: 999}}\nmethodology: {pain: {description: Discovery pain, fields: [{object: deal, name: DealPhase__c}]}}\n")
	second, err := store.ImportProfile(ctx, ProfileImportParams{
		SourcePath:      "profile-reformatted.yaml",
		SourceSHA256:    profilepkg.SourceHash(reformattedBody),
		CanonicalSHA256: validation.CanonicalSHA256,
		RawYAML:         reformattedBody,
		CanonicalJSON:   canonical,
		Profile:         p,
		Findings:        validation.Findings,
		ImportedBy:      "test",
	})
	if err != nil {
		t.Fatalf("second ImportProfile returned error: %v", err)
	}
	if second.Imported || second.ProfileID != result.ProfileID {
		t.Fatalf("idempotent import result=%+v want existing id %d", second, result.ProfileID)
	}
	var importedAt string
	if err := store.DB().QueryRowContext(ctx, `SELECT imported_at FROM profile_meta WHERE id = ?`, result.ProfileID).Scan(&importedAt); err != nil {
		t.Fatalf("query imported_at: %v", err)
	}
	third, err := store.ImportProfile(ctx, ProfileImportParams{
		SourcePath:      "profile-reformatted.yaml",
		SourceSHA256:    profilepkg.SourceHash(reformattedBody),
		CanonicalSHA256: validation.CanonicalSHA256,
		RawYAML:         reformattedBody,
		CanonicalJSON:   canonical,
		Profile:         p,
		Findings:        validation.Findings,
		ImportedBy:      "test",
	})
	if err != nil {
		t.Fatalf("third ImportProfile returned error: %v", err)
	}
	if third.Imported || third.Activated {
		t.Fatalf("byte-identical re-import should be a no-op: %+v", third)
	}
	var importedAtAfter string
	if err := store.DB().QueryRowContext(ctx, `SELECT imported_at FROM profile_meta WHERE id = ?`, result.ProfileID).Scan(&importedAtAfter); err != nil {
		t.Fatalf("query imported_at after no-op: %v", err)
	}
	if importedAtAfter != importedAt {
		t.Fatalf("no-op import changed imported_at: before=%q after=%q", importedAt, importedAtAfter)
	}

	businessProfile, err := store.ActiveBusinessProfile(ctx)
	if err != nil {
		t.Fatalf("ActiveBusinessProfile returned error: %v", err)
	}
	if businessProfile.CanonicalSHA256 != validation.CanonicalSHA256 || businessProfile.SourceSHA256 != profilepkg.SourceHash(reformattedBody) || len(businessProfile.LifecycleCore) != len(profilepkg.RequiredLifecycleBuckets) {
		t.Fatalf("unexpected business profile: %+v", businessProfile)
	}

	rows, info, err := store.SummarizeCallFactsWithSource(ctx, CallFactsSummaryParams{
		GroupBy:         "deal_stage",
		LifecycleSource: LifecycleSourceProfile,
		Limit:           10,
	})
	if err != nil {
		t.Fatalf("SummarizeCallFactsWithSource returned error: %v", err)
	}
	if info.LifecycleSource != LifecycleSourceProfile || info.Profile == nil {
		t.Fatalf("unexpected profile query info: %+v", info)
	}
	if len(rows) != 1 || rows[0].GroupValue != "Negotiation" || rows[0].OpportunityCallCount != 1 {
		t.Fatalf("unexpected profile fact rows: %+v", rows)
	}
	if _, _, err := store.SummarizeCallFactsWithSource(ctx, CallFactsSummaryParams{
		GroupBy:         "primary_user",
		LifecycleSource: LifecycleSourceProfile,
		Limit:           10,
	}); err == nil {
		t.Fatal("unknown profile group_by returned nil error")
	}

	searchRows, _, err := store.SearchCallsByLifecycleWithSource(ctx, LifecycleCallSearchParams{
		Bucket:          "open",
		LifecycleSource: LifecycleSourceProfile,
		Limit:           10,
	})
	if err != nil {
		t.Fatalf("SearchCallsByLifecycleWithSource returned error: %v", err)
	}
	if len(searchRows) != 1 || searchRows[0].CallID != "call_profile_custom_deal" || searchRows[0].Bucket != "open" {
		t.Fatalf("unexpected lifecycle search rows: %+v", searchRows)
	}

	unmapped, err := store.ListUnmappedCRMFields(ctx, UnmappedCRMFieldParams{Limit: 10})
	if err != nil {
		t.Fatalf("ListUnmappedCRMFields returned error: %v", err)
	}
	if len(unmapped) != 1 || unmapped[0].FieldName != "Amount" || unmapped[0].MaxValueLength == 0 {
		t.Fatalf("unexpected unmapped fields: %+v", unmapped)
	}
}

func TestProfileReadModelCachesAndInvalidates(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	if _, err := store.UpsertCall(ctx, mustNormalizeValue(t, map[string]any{
		"id":       "call_profile_cache_1",
		"title":    "Profile cache first call",
		"started":  "2026-04-24T14:00:00Z",
		"duration": 900,
		"metaData": map[string]any{
			"scope": "External",
		},
		"context": map[string]any{
			"crmObjects": []map[string]any{
				{
					"type": "Deal",
					"id":   "deal-cache-1",
					"name": "Cache deal",
					"fields": []map[string]any{
						{"name": "Stage", "value": "Proposal"},
					},
				},
			},
		},
	})); err != nil {
		t.Fatalf("UpsertCall returned error: %v", err)
	}
	body := []byte(`
version: 1
objects:
  deal:
    object_types: [Deal]
fields:
  deal_stage:
    object: deal
    names: [Stage]
lifecycle:
  open:
    rules:
      - field: deal_stage
        op: equals
        value: Proposal
  closed_won: {}
  closed_lost: {}
  post_sales: {}
  unknown: {}
`)
	p, validation, err := profilepkg.ValidateBytes(body, nil)
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
	result, err := store.ImportProfile(ctx, ProfileImportParams{
		SourcePath:      "cache.yaml",
		SourceSHA256:    validation.SourceSHA256,
		CanonicalSHA256: validation.CanonicalSHA256,
		RawYAML:         body,
		CanonicalJSON:   canonical,
		Profile:         p,
		Findings:        validation.Findings,
		ImportedBy:      "test",
	})
	if err != nil {
		t.Fatalf("ImportProfile returned error: %v", err)
	}
	if _, _, err := store.SearchCallsByLifecycleWithSource(ctx, LifecycleCallSearchParams{LifecycleSource: LifecycleSourceProfile, Limit: 10}); err != nil {
		t.Fatalf("SearchCallsByLifecycleWithSource returned error: %v", err)
	}
	var builtAt string
	var callCount int
	if err := store.DB().QueryRowContext(ctx, `SELECT built_at, call_count FROM profile_call_fact_cache_meta WHERE profile_id = ?`, result.ProfileID).Scan(&builtAt, &callCount); err != nil {
		t.Fatalf("query cache meta: %v", err)
	}
	if callCount != 1 {
		t.Fatalf("cache call_count=%d want 1", callCount)
	}
	if _, _, err := store.SearchCallsByLifecycleWithSource(ctx, LifecycleCallSearchParams{LifecycleSource: LifecycleSourceProfile, Limit: 10}); err != nil {
		t.Fatalf("second SearchCallsByLifecycleWithSource returned error: %v", err)
	}
	var builtAtAgain string
	if err := store.DB().QueryRowContext(ctx, `SELECT built_at FROM profile_call_fact_cache_meta WHERE profile_id = ?`, result.ProfileID).Scan(&builtAtAgain); err != nil {
		t.Fatalf("query cache meta after second read: %v", err)
	}
	if builtAtAgain != builtAt {
		t.Fatalf("cache rebuilt without data change: before=%q after=%q", builtAt, builtAtAgain)
	}
	if _, err := store.UpsertCall(ctx, mustNormalizeValue(t, map[string]any{
		"id":       "call_profile_cache_2",
		"title":    "Profile cache second call",
		"started":  "2026-04-25T14:00:00Z",
		"duration": 120,
	})); err != nil {
		t.Fatalf("second UpsertCall returned error: %v", err)
	}
	var metaRows int
	if err := store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM profile_call_fact_cache_meta`).Scan(&metaRows); err != nil {
		t.Fatalf("query stale cache meta count: %v", err)
	}
	if metaRows != 0 {
		t.Fatalf("profile cache was not invalidated; meta rows=%d", metaRows)
	}
	if _, _, err := store.SearchCallsByLifecycleWithSource(ctx, LifecycleCallSearchParams{LifecycleSource: LifecycleSourceProfile, Limit: 10}); err != nil {
		t.Fatalf("post-invalidation SearchCallsByLifecycleWithSource returned error: %v", err)
	}
	if err := store.DB().QueryRowContext(ctx, `SELECT call_count FROM profile_call_fact_cache_meta WHERE profile_id = ?`, result.ProfileID).Scan(&callCount); err != nil {
		t.Fatalf("query rebuilt cache meta: %v", err)
	}
	if callCount != 2 {
		t.Fatalf("rebuilt cache call_count=%d want 2", callCount)
	}
}

func TestProfileReadModelSupportsReadOnlyAfterWarm(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "profile-readonly.db")
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	if _, err := store.UpsertCall(ctx, mustNormalizeValue(t, map[string]any{
		"id":       "call_profile_readonly_1",
		"title":    "Profile readonly first call",
		"started":  "2026-04-24T14:00:00Z",
		"duration": 900,
		"context": map[string]any{
			"crmObjects": []map[string]any{
				{
					"type": "Deal",
					"id":   "deal-readonly-1",
					"fields": []map[string]any{
						{"name": "Stage", "value": "Proposal"},
					},
				},
			},
		},
	})); err != nil {
		t.Fatalf("UpsertCall returned error: %v", err)
	}
	body := []byte(`
version: 1
objects:
  deal:
    object_types: [Deal]
fields:
  deal_stage:
    object: deal
    names: [Stage]
lifecycle:
  open:
    rules:
      - field: deal_stage
        op: equals
        value: Proposal
  closed_won: {}
  closed_lost: {}
  post_sales: {}
  unknown: {}
`)
	p, validation, err := profilepkg.ValidateBytes(body, nil)
	if err != nil {
		t.Fatalf("ValidateBytes returned error: %v", err)
	}
	canonical, err := profilepkg.CanonicalJSON(p)
	if err != nil {
		t.Fatalf("CanonicalJSON returned error: %v", err)
	}
	if _, err := store.ImportProfile(ctx, ProfileImportParams{
		SourcePath:      "readonly.yaml",
		SourceSHA256:    validation.SourceSHA256,
		CanonicalSHA256: validation.CanonicalSHA256,
		RawYAML:         body,
		CanonicalJSON:   canonical,
		Profile:         p,
		Findings:        validation.Findings,
		ImportedBy:      "test",
	}); err != nil {
		t.Fatalf("ImportProfile returned error: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close writable store: %v", err)
	}

	readOnly, err := OpenReadOnly(ctx, path)
	if err != nil {
		t.Fatalf("OpenReadOnly returned error: %v", err)
	}
	if rows, _, err := readOnly.SearchCallsByLifecycleWithSource(ctx, LifecycleCallSearchParams{LifecycleSource: LifecycleSourceProfile, Limit: 10}); err != nil || len(rows) != 1 {
		t.Fatalf("read-only profile search rows=%+v err=%v", rows, err)
	}
	if err := readOnly.Close(); err != nil {
		t.Fatalf("close read-only store: %v", err)
	}

	store, err = Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen writable store: %v", err)
	}
	if _, err := store.UpsertCall(ctx, mustNormalizeValue(t, map[string]any{
		"id":       "call_profile_readonly_2",
		"title":    "Profile readonly second call",
		"started":  "2026-04-25T14:00:00Z",
		"duration": 120,
	})); err != nil {
		t.Fatalf("second UpsertCall returned error: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close writable stale store: %v", err)
	}
	readOnly, err = OpenReadOnly(ctx, path)
	if err != nil {
		t.Fatalf("OpenReadOnly stale returned error: %v", err)
	}
	defer readOnly.Close()
	if _, _, err := readOnly.SearchCallsByLifecycleWithSource(ctx, LifecycleCallSearchParams{LifecycleSource: LifecycleSourceProfile, Limit: 10}); err == nil || !strings.Contains(err.Error(), "profile read model is missing or stale") {
		t.Fatalf("read-only stale cache error=%v", err)
	}
}

func TestLifecycleEmptyRuleBucketRoundTrip(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	body := []byte(`
version: 1
lifecycle:
  open:
    label: Open Pipeline
    order: 10
  closed_won:
    label: Won
    order: 20
  closed_lost:
    label: Lost
    order: 30
  post_sales:
    label: Customer Success
    order: 40
  unknown:
    label: Unknown
    order: 99
`)
	p, validation, err := profilepkg.ValidateBytes(body, nil)
	if err != nil {
		t.Fatalf("ValidateBytes returned error: %v", err)
	}
	if !validation.Valid {
		t.Fatalf("empty-rule profile should import with warnings only: %+v", validation.Findings)
	}
	canonical, err := profilepkg.CanonicalJSON(p)
	if err != nil {
		t.Fatalf("CanonicalJSON returned error: %v", err)
	}
	if _, err := store.ImportProfile(ctx, ProfileImportParams{
		SourcePath:      "empty-rules.yaml",
		SourceSHA256:    validation.SourceSHA256,
		CanonicalSHA256: validation.CanonicalSHA256,
		RawYAML:         body,
		CanonicalJSON:   canonical,
		Profile:         p,
		Findings:        validation.Findings,
		ImportedBy:      "test",
	}); err != nil {
		t.Fatalf("ImportProfile returned error: %v", err)
	}
	active, err := store.ActiveProfileDocument(ctx)
	if err != nil {
		t.Fatalf("ActiveProfileDocument returned error: %v", err)
	}
	if active.Lifecycle["open"].Label != "Open Pipeline" || active.Lifecycle["open"].Order != 10 {
		t.Fatalf("empty-rule lifecycle bucket did not round-trip: %+v", active.Lifecycle["open"])
	}
}

func TestProfileInventoryIncludesCachedCRMSchema(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	if _, err := store.UpsertCRMIntegration(ctx, mustNormalizeValue(t, map[string]any{
		"integrationId": "crm-schema-only",
		"name":          "Schema Only",
		"crmType":       "CustomCRM",
	})); err != nil {
		t.Fatalf("UpsertCRMIntegration returned error: %v", err)
	}
	if _, err := store.UpsertCRMSchema(ctx, "crm-schema-only", "Deal", mustNormalizeValue(t, map[string]any{
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
	if !inventoryHasObject(inventory, "Deal") || !inventoryHasField(inventory, "Deal", "DealStage") {
		t.Fatalf("schema-only inventory missing Deal/DealStage: %+v", inventory)
	}
	body := []byte(`
version: 1
objects:
  deal:
    object_types: [Deal]
fields:
  deal_stage:
    object: deal
    names: [DealStage]
lifecycle:
  open:
    rules:
      - field: deal_stage
        op: equals
        value: Proposal
  closed_won: {}
  closed_lost: {}
  post_sales: {}
  unknown: {}
`)
	_, validation, err := profilepkg.ValidateBytes(body, inventory)
	if err != nil {
		t.Fatalf("ValidateBytes returned error: %v", err)
	}
	if !validation.Valid {
		t.Fatalf("schema-only profile should validate: %+v", validation.Findings)
	}
}

func TestProfileImportRejectsInvalidProfile(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	body := []byte(`
version: 0
lifecycle:
  open: {}
  closed_won: {}
  closed_lost: {}
  post_sales: {}
  unknown: {}
`)
	p, err := profilepkg.ParseYAML(body)
	if err != nil {
		t.Fatalf("ParseYAML returned error: %v", err)
	}
	canonical, err := profilepkg.CanonicalJSON(p)
	if err != nil {
		t.Fatalf("CanonicalJSON returned error: %v", err)
	}
	if _, err := store.ImportProfile(ctx, ProfileImportParams{
		SourcePath:      "invalid.yaml",
		SourceSHA256:    profilepkg.SourceHash(body),
		CanonicalSHA256: profilepkg.SourceHash(canonical),
		RawYAML:         body,
		CanonicalJSON:   canonical,
		Profile:         p,
	}); err == nil {
		t.Fatal("ImportProfile returned nil error for invalid profile")
	}
}

func TestPartialProfileReportsUnavailableConcepts(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	if _, err := store.UpsertCall(ctx, mustNormalizeValue(t, map[string]any{
		"id":       "call_partial_profile",
		"title":    "Partial profile call",
		"started":  "2026-04-24T14:00:00Z",
		"duration": 300,
	})); err != nil {
		t.Fatalf("UpsertCall returned error: %v", err)
	}
	body := []byte(`
version: 1
lifecycle:
  open: {}
  closed_won: {}
  closed_lost: {}
  post_sales: {}
  unknown: {}
`)
	p, validation, err := profilepkg.ValidateBytes(body, nil)
	if err != nil {
		t.Fatalf("ValidateBytes returned error: %v", err)
	}
	if !validation.Valid {
		t.Fatalf("partial profile should import with warnings only: %+v", validation.Findings)
	}
	canonical, err := profilepkg.CanonicalJSON(p)
	if err != nil {
		t.Fatalf("CanonicalJSON returned error: %v", err)
	}
	if _, err := store.ImportProfile(ctx, ProfileImportParams{
		SourcePath:      "partial.yaml",
		SourceSHA256:    validation.SourceSHA256,
		CanonicalSHA256: validation.CanonicalSHA256,
		RawYAML:         body,
		CanonicalJSON:   canonical,
		Profile:         p,
		Findings:        validation.Findings,
		ImportedBy:      "test",
	}); err != nil {
		t.Fatalf("ImportProfile returned error: %v", err)
	}

	_, info, err := store.SummarizeCallFactsWithSource(ctx, CallFactsSummaryParams{
		GroupBy:         "deal_stage",
		LifecycleSource: LifecycleSourceProfile,
	})
	if err != nil {
		t.Fatalf("SummarizeCallFactsWithSource returned error: %v", err)
	}
	if !stringSliceContains(info.UnavailableConcepts, "deal") || !stringSliceContains(info.UnavailableConcepts, "account") || !stringSliceContains(info.UnavailableConcepts, "deal_stage") {
		t.Fatalf("unexpected unavailable concepts: %+v", info)
	}
	_, _, coverageInfo, err := store.CallFactsCoverageWithSource(ctx, LifecycleSourceProfile)
	if err != nil {
		t.Fatalf("CallFactsCoverageWithSource returned error: %v", err)
	}
	if !stringSliceContains(coverageInfo.UnavailableConcepts, "deal") || !stringSliceContains(coverageInfo.UnavailableConcepts, "account") {
		t.Fatalf("coverage missing unavailable concepts: %+v", coverageInfo)
	}
	_, backlogInfo, err := store.PrioritizeTranscriptsByLifecycleWithSource(ctx, LifecycleTranscriptPriorityParams{LifecycleSource: LifecycleSourceProfile})
	if err != nil {
		t.Fatalf("PrioritizeTranscriptsByLifecycleWithSource returned error: %v", err)
	}
	if !stringSliceContains(backlogInfo.UnavailableConcepts, "deal") || !stringSliceContains(backlogInfo.UnavailableConcepts, "account") {
		t.Fatalf("backlog missing unavailable concepts: %+v", backlogInfo)
	}
}

func inventoryHasObject(inventory *profilepkg.Inventory, objectType string) bool {
	for _, object := range inventory.Objects {
		if object.ObjectType == objectType {
			return true
		}
	}
	return false
}

func inventoryHasField(inventory *profilepkg.Inventory, objectType string, fieldName string) bool {
	for _, field := range inventory.Fields {
		if field.ObjectType == objectType && field.FieldName == fieldName {
			return true
		}
	}
	return false
}

func stringSliceContains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func openTestStore(t *testing.T) *Store {
	t.Helper()

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	return store
}

func readFixture(t *testing.T, rel string) json.RawMessage {
	t.Helper()

	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read fixture %s: %v", rel, err)
	}
	return json.RawMessage(data)
}

func fixtureObject(t *testing.T, rel string, key string) json.RawMessage {
	t.Helper()

	var doc map[string]any
	if err := json.Unmarshal(readFixture(t, rel), &doc); err != nil {
		t.Fatalf("decode fixture %s: %v", rel, err)
	}
	values, ok := doc[key].([]any)
	if !ok || len(values) != 1 {
		t.Fatalf("fixture %s key %s did not contain exactly one object", rel, key)
	}
	raw, err := normalizeJSONValue(values[0])
	if err != nil {
		t.Fatalf("normalize fixture object %s[%s]: %v", rel, key, err)
	}
	return raw
}

func removeJSONKey(t *testing.T, raw json.RawMessage, key string) json.RawMessage {
	t.Helper()

	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode JSON for key removal: %v", err)
	}
	delete(doc, key)
	return mustNormalizeValue(t, doc)
}

func mustNormalizeValue(t *testing.T, value any) json.RawMessage {
	t.Helper()

	raw, err := normalizeJSONValue(value)
	if err != nil {
		t.Fatalf("normalize JSON value: %v", err)
	}
	return raw
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

func repoRoot(t *testing.T) string {
	t.Helper()

	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	return root
}
