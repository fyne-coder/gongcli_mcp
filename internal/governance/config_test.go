package governance

import "testing"

func TestParseYAMLNormalizesListsAndAliases(t *testing.T) {
	cfg, err := ParseYAML([]byte(`
version: 1
lists:
  no_ai:
    customers:
      - name: "Acme, Inc."
        aliases: ["ACME"]
  notification_required:
    customers:
      - name: "Globex LLC"
        aliases: ["Globex Corporation"]
`))
	if err != nil {
		t.Fatalf("ParseYAML returned error: %v", err)
	}
	targets := cfg.Targets()
	got := map[string]Target{}
	for _, target := range targets {
		got[target.List+"|"+target.Normalized] = target
	}
	if got[ListNoAI+"|acme inc"].Name != "Acme, Inc." {
		t.Fatalf("missing normalized Acme target: %+v", got)
	}
	if got[ListNoAI+"|acme"].Alias != "ACME" {
		t.Fatalf("missing normalized Acme alias: %+v", got)
	}
	if got[ListNotificationRequired+"|globex corporation"].Alias != "Globex Corporation" {
		t.Fatalf("missing notification alias: %+v", got)
	}
}

func TestAuditCandidatesUsesNormalizedTokenPhraseMatch(t *testing.T) {
	cfg, err := ParseYAML([]byte(`
version: 1
lists:
  no_ai:
    customers:
      - name: "Acme, Inc."
  notification_required:
    customers:
      - name: "Globex"
        aliases: ["Globex Corporation"]
      - name: "XYZ"
`))
	if err != nil {
		t.Fatalf("ParseYAML returned error: %v", err)
	}
	audit := AuditCandidates([]Candidate{
		{CallID: "call-acme", Value: "acme inc"},
		{CallID: "call-acme-title", Value: "Acme Inc quarterly review"},
		{CallID: "call-partial", Value: "Acme Partners"},
		{CallID: "call-globex", Value: "Globex Corporation"},
		{CallID: "call-xyz", Value: "XYZ Process Automation overview"},
		{CallID: "call-xylophone", Value: "Xylophone Yard Zone overview"},
	}, cfg)
	if audit.SuppressedCallCount != 4 {
		t.Fatalf("suppressed calls=%d want 4: %+v", audit.SuppressedCallCount, audit)
	}
	for _, callID := range audit.SuppressedCallIDs {
		if callID == "call-partial" || callID == "call-xylophone" {
			t.Fatalf("false-positive match was suppressed: %+v", audit)
		}
	}
	if len(audit.MatchedEntries) != 4 {
		t.Fatalf("matched entries=%d want 4: %+v", len(audit.MatchedEntries), audit)
	}
}

func TestParseYAMLRejectsUnknownListAndEmptyTargets(t *testing.T) {
	if _, err := ParseYAML([]byte(`
version: 1
lists:
  noai:
    customers:
      - name: "Typo Corp"
`)); err == nil {
		t.Fatal("ParseYAML allowed unknown list key")
	}
	if _, err := ParseYAML([]byte(`
version: 1
lists:
  no_ai:
    customers: []
`)); err == nil {
		t.Fatal("ParseYAML allowed empty governance target set")
	}
}
