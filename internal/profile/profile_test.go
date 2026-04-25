package profile

import (
	"strings"
	"testing"
)

func TestValidateRejectsUnsafeRulesAndMissingCore(t *testing.T) {
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
        op: sql
        value: "1=1"
`)
	p, err := ParseYAML(body)
	if err != nil {
		t.Fatalf("ParseYAML returned error: %v", err)
	}
	findings := Validate(p, &Inventory{
		Objects: []ObjectInventory{{ObjectType: "Deal", ObjectCount: 1}},
		Fields:  []FieldInventory{{ObjectType: "Deal", FieldName: "Stage", ObjectCount: 1, PopulatedCount: 1}},
	})
	if IsValid(findings) {
		t.Fatalf("profile unexpectedly valid: %+v", findings)
	}
	if !hasFinding(findings, "unsupported_rule_operator") {
		t.Fatalf("missing unsupported_rule_operator finding: %+v", findings)
	}
	if !hasFinding(findings, "missing_lifecycle_bucket") {
		t.Fatalf("missing required lifecycle finding: %+v", findings)
	}
}

func TestValidateRejectsDirectRuleFieldNotSeen(t *testing.T) {
	body := []byte(`
version: 1
objects:
  deal:
    object_types: [Deal]
lifecycle:
  open:
    rules:
      - object: deal
        field_name: MissingStage
        op: equals
        value: Proposal
  closed_won: {}
  closed_lost: {}
  post_sales: {}
  unknown: {}
`)
	p, err := ParseYAML(body)
	if err != nil {
		t.Fatalf("ParseYAML returned error: %v", err)
	}
	findings := Validate(p, &Inventory{
		Objects: []ObjectInventory{{ObjectType: "Deal", ObjectCount: 1}},
		Fields:  []FieldInventory{{ObjectType: "Deal", FieldName: "Stage", ObjectCount: 1, PopulatedCount: 1}},
	})
	if !hasFinding(findings, "rule_field_not_seen") {
		t.Fatalf("missing direct rule field validation finding: %+v", findings)
	}
}

func TestValidateRejectsFieldlessEmptyRule(t *testing.T) {
	body := []byte(`
version: 1
objects:
  deal:
    object_types: [Deal]
lifecycle:
  open:
    rules:
      - op: is_empty
  closed_won: {}
  closed_lost: {}
  post_sales: {}
  unknown: {}
`)
	p, err := ParseYAML(body)
	if err != nil {
		t.Fatalf("ParseYAML returned error: %v", err)
	}
	findings := Validate(p, nil)
	if !hasFinding(findings, "rule_missing_field") {
		t.Fatalf("missing rule_missing_field finding: %+v", findings)
	}
}

func TestParseYAMLRejectsUnknownKeys(t *testing.T) {
	_, err := ParseYAML([]byte(`
version: 1
objects:
  deal:
    object_types: [Deal]
    sql: "1=1"
lifecycle:
  open: {}
  closed_won: {}
  closed_lost: {}
  post_sales: {}
  unknown: {}
`))
	if err == nil {
		t.Fatal("ParseYAML returned nil error for unknown key")
	}
}

func TestValidateRejectsMissingZeroAndUnsupportedVersion(t *testing.T) {
	cases := []struct {
		name string
		body string
		code string
	}{
		{
			name: "missing",
			body: `
lifecycle:
  open: {}
  closed_won: {}
  closed_lost: {}
  post_sales: {}
  unknown: {}
`,
			code: "missing_version",
		},
		{
			name: "zero",
			body: `
version: 0
lifecycle:
  open: {}
  closed_won: {}
  closed_lost: {}
  post_sales: {}
  unknown: {}
`,
			code: "missing_version",
		},
		{
			name: "unsupported",
			body: `
version: 2
lifecycle:
  open: {}
  closed_won: {}
  closed_lost: {}
  post_sales: {}
  unknown: {}
`,
			code: "unsupported_version",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := ParseYAML([]byte(tc.body))
			if err != nil {
				t.Fatalf("ParseYAML returned error: %v", err)
			}
			findings := Validate(p, nil)
			if IsValid(findings) || !hasFinding(findings, tc.code) {
				t.Fatalf("expected %s validation failure: %+v", tc.code, findings)
			}
		})
	}
}

func TestDiscoverBuildsCustomDealProfile(t *testing.T) {
	p := Discover(&Inventory{
		Objects: []ObjectInventory{
			{ObjectType: "Company", ObjectCount: 5, CallCount: 5},
			{ObjectType: "Deal", ObjectCount: 7, CallCount: 7},
		},
		Fields: []FieldInventory{
			{
				ObjectType:     "Deal",
				FieldName:      "DealPhase__c",
				FieldLabel:     "Stage",
				ObjectCount:    7,
				PopulatedCount: 7,
				DistinctValues: []string{"Discovery", "Closed Won", "Closed Lost"},
			},
			{
				ObjectType:     "Company",
				FieldName:      "Customer_Status__c",
				FieldLabel:     "Customer Status",
				ObjectCount:    5,
				PopulatedCount: 5,
				DistinctValues: []string{"Customer - Active"},
			},
		},
	})
	if p.Objects["deal"].ObjectTypes[0] != "Deal" {
		t.Fatalf("deal object=%+v", p.Objects["deal"])
	}
	if p.Fields["deal_stage"].Names[0] != "DealPhase__c" {
		t.Fatalf("deal_stage=%+v", p.Fields["deal_stage"])
	}
	if !IsValid(Validate(p, nil)) {
		t.Fatalf("discovered profile should be structurally valid: %+v", Validate(p, nil))
	}
	if len(p.Lifecycle["closed_won"].Rules) == 0 {
		t.Fatalf("closed_won lifecycle missing discovered rules: %+v", p.Lifecycle["closed_won"])
	}
}

func TestCanonicalHashIgnoresYAMLFormatting(t *testing.T) {
	left, err := ParseYAML([]byte(`
version: 1
name: Demo
objects:
  deal:
    object_types: [Deal, Opportunity]
lifecycle:
  open: {order: 1}
  closed_won: {order: 2}
  closed_lost: {order: 3}
  post_sales: {order: 4}
  unknown: {order: 99}
`))
	if err != nil {
		t.Fatalf("ParseYAML left: %v", err)
	}
	right, err := ParseYAML([]byte(`version: 1
name: Demo
objects:
  deal:
    object_types:
      - Opportunity
      - Deal
lifecycle:
  unknown:
    order: 99
  post_sales:
    order: 4
  closed_lost:
    order: 3
  closed_won:
    order: 2
  open:
    order: 1
`))
	if err != nil {
		t.Fatalf("ParseYAML right: %v", err)
	}
	leftHash, err := CanonicalHash(left)
	if err != nil {
		t.Fatalf("CanonicalHash left: %v", err)
	}
	rightHash, err := CanonicalHash(right)
	if err != nil {
		t.Fatalf("CanonicalHash right: %v", err)
	}
	if leftHash != rightHash {
		t.Fatalf("canonical hashes differ: %s vs %s", leftHash, rightHash)
	}
}

func TestRuleEval(t *testing.T) {
	cases := []struct {
		name   string
		rule   Rule
		values []string
		want   bool
	}{
		{name: "equals", rule: Rule{Op: "equals", Value: "Closed Won"}, values: []string{"Closed Won"}, want: true},
		{name: "in", rule: Rule{Op: "in", Values: []string{"A", "B"}}, values: []string{"C", "B"}, want: true},
		{name: "iprefix", rule: Rule{Op: "iprefix", Value: "customer"}, values: []string{"Customer - Active"}, want: true},
		{name: "empty", rule: Rule{Op: "is_empty"}, values: nil, want: true},
		{name: "regex", rule: Rule{Op: "regex", Value: "^Q[0-9]"}, values: []string{"Q2 risk"}, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := EvaluateRule(tc.values, tc.rule)
			if err != nil {
				t.Fatalf("EvaluateRule returned error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("EvaluateRule=%t want %t", got, tc.want)
			}
		})
	}
	if _, err := EvaluateRule([]string{"x"}, Rule{Op: "regex", Value: strings.Repeat("x", maxRegexLength+1)}); err == nil {
		t.Fatal("long regex did not fail")
	}
}

func hasFinding(findings []Finding, code string) bool {
	for _, finding := range findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}
