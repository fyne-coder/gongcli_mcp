package contextmodel

import (
	"encoding/json"
	"testing"
)

func TestExtractUsesSQLiteCompatibleFallbacks(t *testing.T) {
	objects, hasContext, err := Extract(json.RawMessage(`{
		"context": {
			"crmObjects": [
				{
					"type": "Opportunity",
					"id": "opp-001",
					"fields": [
						{"label": "Name", "value": "Renewal Deal"},
						{"apiName": "StageName", "displayName": "Stage", "type": "picklist", "value": "Contract Review"},
						{"value": "fallback value"}
					]
				}
			]
		},
		"crmContext": {
			"objects": [
				{
					"entityType": "Account",
					"crmId": "acct-001",
					"properties": {
						"Industry": "Healthcare",
						"Active__c": true,
						"Nested__c": {"tier":"gold"}
					}
				}
			]
		}
	}`))
	if err != nil {
		t.Fatalf("Extract returned error: %v", err)
	}
	if !hasContext {
		t.Fatal("Extract hasContext=false, want true")
	}
	if len(objects) != 2 {
		t.Fatalf("objects=%d want 2: %+v", len(objects), objects)
	}
	if objects[0].ObjectKey != "Opportunity:opp-001" || objects[0].ObjectName != "Renewal Deal" {
		t.Fatalf("unexpected opportunity object: %+v", objects[0])
	}
	if got := objects[0].Fields[0]; got.FieldName != "Name" || got.FieldLabel != "Name" || got.ValueText != "Renewal Deal" {
		t.Fatalf("unexpected label fallback field: %+v", got)
	}
	if got := objects[0].Fields[1]; got.FieldName != "StageName" || got.FieldLabel != "Stage" || got.FieldType != "picklist" || got.ValueText != "Contract Review" {
		t.Fatalf("unexpected apiName field: %+v", got)
	}
	if got := objects[0].Fields[2]; got.FieldName != "field_2" || got.ValueText != "fallback value" {
		t.Fatalf("unexpected field_N fallback: %+v", got)
	}
	if objects[1].ObjectKey != "Account:acct-001" || len(objects[1].Fields) != 3 {
		t.Fatalf("unexpected account object: %+v", objects[1])
	}
	values := map[string]string{}
	for _, field := range objects[1].Fields {
		values[field.FieldName] = field.ValueText
	}
	if values["Industry"] != "Healthcare" || values["Active__c"] != "true" || values["Nested__c"] != `{"tier":"gold"}` {
		t.Fatalf("unexpected map field values: %+v", values)
	}
}
