package governance

import (
	"encoding/json"
	"testing"
)

func TestEvaluateCallPayloadAcceptsMetaDataCallID(t *testing.T) {
	cfg, err := ParseYAML([]byte(`
version: 1
lists:
  no_ai:
    customers:
      - name: "Acme, Inc."
`))
	if err != nil {
		t.Fatalf("ParseYAML returned error: %v", err)
	}

	decision, err := EvaluateCallPayload(json.RawMessage(`{
		"metaData": {
			"id": "call-meta-id",
			"title": "Quarterly sync"
		}
	}`), cfg)
	if err != nil {
		t.Fatalf("EvaluateCallPayload returned error: %v", err)
	}
	if decision.CallID != "call-meta-id" {
		t.Fatalf("call_id=%q want call-meta-id", decision.CallID)
	}
	if decision.Skip {
		t.Fatal("unmatched metadata-only call was skipped")
	}
}
