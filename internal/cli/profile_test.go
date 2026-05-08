package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProfileStagedImportHistoryActivateDiffAndSchema(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gong.db")
	store := openCLITestStore(t, dbPath)
	if err := store.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	dir := t.TempDir()
	firstProfile := filepath.Join(dir, "profile-one.yaml")
	secondProfile := filepath.Join(dir, "profile-two.yaml")
	if err := os.WriteFile(firstProfile, []byte(testProfileYAML("Profile One")), 0o600); err != nil {
		t.Fatalf("write first profile: %v", err)
	}
	if err := os.WriteFile(secondProfile, []byte(testProfileYAML("Profile Two")), 0o600); err != nil {
		t.Fatalf("write second profile: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"profile", "import", "--db", dbPath, "--profile", firstProfile}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("import active code=%d stderr=%q", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	code = Run(context.Background(), []string{"profile", "import", "--db", dbPath, "--profile", secondProfile, "--activate=false"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("staged import code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"activated": false`) {
		t.Fatalf("staged import response=%s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Run(context.Background(), []string{"profile", "history", "--db", dbPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("history code=%d stderr=%q", code, stderr.String())
	}
	var history struct {
		Profiles []struct {
			ProfileID       int64  `json:"profile_id"`
			Name            string `json:"name"`
			CanonicalSHA256 string `json:"canonical_sha256"`
			IsActive        bool   `json:"is_active"`
		} `json:"profiles"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &history); err != nil {
		t.Fatalf("unmarshal history: %v", err)
	}
	if len(history.Profiles) != 2 {
		t.Fatalf("profiles=%v want 2", history.Profiles)
	}
	activeName := ""
	for _, profile := range history.Profiles {
		if profile.IsActive {
			activeName = profile.Name
		}
	}
	if activeName != "Profile One" {
		t.Fatalf("unexpected staged history: %+v", history.Profiles)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run(context.Background(), []string{"profile", "diff", "--db", dbPath, "--from", "active", "--to", secondProfile}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("diff code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"changed": true`) || !strings.Contains(stdout.String(), `"name"`) {
		t.Fatalf("diff response=%s", stdout.String())
	}

	stagedSHA := ""
	for _, profile := range history.Profiles {
		if profile.Name == "Profile Two" {
			stagedSHA = profile.CanonicalSHA256
		}
	}
	if stagedSHA == "" {
		t.Fatalf("missing staged SHA in history: %+v", history.Profiles)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run(context.Background(), []string{"profile", "activate", "--db", dbPath, "--canonical-sha", stagedSHA[:12]}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("activate code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"activated": true`) {
		t.Fatalf("activate response=%s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Run(context.Background(), []string{"profile", "show", "--db", dbPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("show code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"name": "Profile Two"`) {
		t.Fatalf("show response=%s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Run(context.Background(), []string{"profile", "schema"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("schema code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"open"`) || !strings.Contains(stdout.String(), `"regex"`) {
		t.Fatalf("schema response=%s", stdout.String())
	}
}

func TestProfileHelpExitsZero(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := Run(context.Background(), []string{"profile", "--help"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "profile history") || !strings.Contains(stderr.String(), "profile schema") {
		t.Fatalf("help output missing new commands: %s", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Run(context.Background(), []string{"profile", "diff", "--help"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("profile diff help code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "-to") {
		t.Fatalf("profile diff help missing flags: %s", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Run(context.Background(), []string{"profile", "activate", "--help"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("profile activate help code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "-canonical-sha") {
		t.Fatalf("profile activate help missing flags: %s", stderr.String())
	}
}

func testProfileYAML(name string) string {
	return `version: 1
name: ` + name + `
lifecycle:
  open:
    label: Open
    order: 10
  closed_won:
    label: Closed Won
    order: 20
  closed_lost:
    label: Closed Lost
    order: 30
  post_sales:
    label: Post Sales
    order: 40
  unknown:
    label: Unknown
    order: 99
`
}

// TestProfileValidateGAReadinessGate proves the GA-readiness gate behavior on
// `gongctl profile validate`:
//
//   - default validate exits success for a syntactically valid but
//     readiness-suspect profile (no methodology, no loss-reason mapping,
//     CreatedDate-only field concept);
//   - --ga-readiness validate fails for the same suspect profile and surfaces
//     readiness findings in the JSON output;
//   - --ga-readiness validate passes for a synthetic profile that satisfies
//     the mechanical readiness checklist.
func TestProfileValidateGAReadinessGate(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gong.db")
	store := openCLITestStore(t, dbPath)
	if _, err := store.UpsertCRMIntegration(context.Background(), mustMarshalJSON(t, map[string]any{
		"integrationId": "crm-profile-ga-001",
		"name":          "Salesforce profile GA",
		"crmType":       "Salesforce",
	})); err != nil {
		t.Fatalf("UpsertCRMIntegration returned error: %v", err)
	}
	if _, err := store.UpsertCRMSchema(context.Background(), "crm-profile-ga-001", "Opportunity", mustMarshalJSON(t, map[string]any{
		"objectTypeToSelectedFields": map[string]any{
			"Opportunity": []map[string]any{
				{"fieldName": "CreatedDate", "label": "Created Date", "fieldType": "datetime"},
				{"fieldName": "StageName", "label": "Stage", "fieldType": "picklist"},
				{"fieldName": "Loss_Reason__c", "label": "Loss Reason", "fieldType": "picklist"},
			},
		},
	})); err != nil {
		t.Fatalf("UpsertCRMSchema returned error: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	dir := t.TempDir()
	suspectPath := filepath.Join(dir, "suspect.yaml")
	if err := os.WriteFile(suspectPath, []byte(suspectReadinessProfileYAML()), 0o600); err != nil {
		t.Fatalf("write suspect profile: %v", err)
	}
	readyPath := filepath.Join(dir, "ready.yaml")
	if err := os.WriteFile(readyPath, []byte(readyReadinessProfileYAML()), 0o600); err != nil {
		t.Fatalf("write ready profile: %v", err)
	}

	// Default validate: must still exit success even though the profile has
	// suspect readiness findings (the YAML is syntactically valid).
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"profile", "validate", "--db", dbPath, "--profile", suspectPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("default validate code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"valid": true`) {
		t.Fatalf("default validate response should report valid=true: %s", stdout.String())
	}

	// --ga-readiness must fail and the JSON output must still be emitted with
	// readiness findings.
	stdout.Reset()
	stderr.Reset()
	code = Run(context.Background(), []string{"profile", "validate", "--db", dbPath, "--profile", suspectPath, "--ga-readiness"}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("ga-readiness validate of suspect profile should fail; stdout=%s", stdout.String())
	}
	if !strings.Contains(strings.ToLower(stderr.String()), "readiness") {
		t.Fatalf("ga-readiness validate stderr should mention readiness: %q", stderr.String())
	}
	body := stdout.String()
	if !strings.Contains(body, `"ga_readiness"`) {
		t.Fatalf("ga-readiness JSON missing ga_readiness block: %s", body)
	}
	if !strings.Contains(body, "loss_reason_mapping_missing") {
		t.Fatalf("ga-readiness JSON missing loss-reason finding: %s", body)
	}
	if !strings.Contains(body, "methodology") {
		t.Fatalf("ga-readiness JSON missing methodology finding: %s", body)
	}
	if !strings.Contains(body, "created_date") {
		t.Fatalf("ga-readiness JSON missing created-date finding: %s", body)
	}

	// --ga-readiness must pass for a profile that satisfies the mechanical
	// checklist (lifecycle present with rules, methodology mapped, loss-reason
	// field referenced, no CreatedDate-only concept).
	stdout.Reset()
	stderr.Reset()
	code = Run(context.Background(), []string{"profile", "validate", "--db", dbPath, "--profile", readyPath, "--ga-readiness"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("ga-readiness validate of ready profile should succeed; stderr=%q stdout=%s", stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), `"ga_readiness"`) {
		t.Fatalf("ga-readiness validate JSON missing ga_readiness block: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"blocking_findings": []`) && !strings.Contains(stdout.String(), `"blocking_findings": null`) {
		t.Fatalf("ga-readiness validate response should have empty blocking_findings: %s", stdout.String())
	}
}

func suspectReadinessProfileYAML() string {
	// Syntactically valid against the synthetic CRM schema seeded above, so
	// default `profile validate` exits success. The readiness checklist still
	// flags it because a field concept maps only to CreatedDate, methodology is
	// unmapped, and no loss-reason field is referenced.
	return `version: 1
name: Suspect Profile
objects:
  deal:
    object_types: [Opportunity]
fields:
  deal_created:
    object: deal
    names: [CreatedDate]
lifecycle:
  open:
    label: Open
    order: 10
  closed_won:
    label: Closed Won
    order: 20
  closed_lost:
    label: Closed Lost
    order: 30
  post_sales:
    label: Post Sales
    order: 40
  unknown:
    label: Unknown
    order: 99
`
}

func readyReadinessProfileYAML() string {
	return `version: 1
name: Ready Profile
objects:
  deal:
    object_types: [Opportunity]
  account:
    object_types: [Account]
fields:
  deal_stage:
    object: deal
    names: [StageName]
  deal_loss_reason:
    object: deal
    names: [Loss_Reason__c]
lifecycle:
  open:
    label: Open
    order: 10
    rules:
      - field: deal_stage
        op: in
        values: [Discovery, Proposal]
  closed_won:
    label: Closed Won
    order: 20
    rules:
      - field: deal_stage
        op: equals
        value: Closed Won
  closed_lost:
    label: Closed Lost
    order: 30
    rules:
      - field: deal_stage
        op: equals
        value: Closed Lost
  post_sales:
    label: Post Sales
    order: 40
    rules:
      - field: deal_stage
        op: equals
        value: Renewal
  unknown:
    label: Unknown
    order: 99
methodology:
  pain:
    description: Customer pain themes captured on calls
    aliases: [pain_points, problem]
`
}
