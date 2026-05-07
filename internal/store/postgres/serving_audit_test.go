package postgres

import (
	"strings"
	"testing"
)

// TestServingSchemaAuditCoversCopiedTables proves the audit table covers every
// copied table referenced from serving.go and that each row has a
// non-empty justification.
func TestServingSchemaAuditCoversCopiedTables(t *testing.T) {
	t.Parallel()

	rows := ServingSchemaAudit()
	if len(rows) == 0 {
		t.Fatal("ServingSchemaAudit returned no rows")
	}
	required := []string{
		"calls",
		"users",
		"transcripts",
		"transcript_segments",
		"call_context_objects",
		"call_context_fields",
		"call_facts",
		"call_ai_highlights",
		"scorecard_activity",
		"gong_settings",
		"governance_policy_state",
		"governance_suppressed_calls",
	}
	tables := map[string]struct{}{}
	for _, p := range rows {
		if strings.TrimSpace(p.Table) == "" || strings.TrimSpace(p.Column) == "" {
			t.Fatalf("audit entry has empty table/column: %+v", p)
		}
		if strings.TrimSpace(p.Reason) == "" {
			t.Fatalf("audit entry %s.%s missing justification", p.Table, p.Column)
		}
		switch p.Decision {
		case ServingColumnAllowed, ServingColumnRedacted, ServingColumnNotCopied:
		default:
			t.Fatalf("audit entry %s.%s has unknown decision %q", p.Table, p.Column, p.Decision)
		}
		tables[p.Table] = struct{}{}
	}
	for _, want := range required {
		if _, ok := tables[want]; !ok {
			t.Fatalf("ServingSchemaAudit is missing required table %q", want)
		}
	}
}

// TestValidateServingSchemaAuditFailsForUncategorizedColumn proves the audit
// linter rejects a synthetic column that no one categorized.
//
// Red/green evidence note: this test is the schema-audit linter and
// fails-loud-on-drift requirement from Foundation D-1.
func TestValidateServingSchemaAuditFailsForUncategorizedColumn(t *testing.T) {
	t.Parallel()

	snapshot := ServingSchemaSnapshot{
		Tables: map[string][]string{
			"calls":                       {"call_id", "title", "started_at", "synthetic_new_pii_column"},
			"transcripts":                 {"call_id", "raw_json", "raw_sha256", "segment_count", "first_seen_at", "updated_at"},
			"governance_policy_state":     {"config_sha256", "data_fingerprint"},
			"governance_suppressed_calls": {"call_id"},
		},
	}
	err := ValidateServingSchemaAudit(snapshot, nil)
	if err == nil {
		t.Fatal("ValidateServingSchemaAudit accepted uncategorized column")
	}
	if !strings.Contains(err.Error(), "calls.synthetic_new_pii_column") {
		t.Fatalf("error %q must name the offending column", err.Error())
	}
}

// TestValidateServingSchemaAuditFailsForUncategorizedTable proves the audit
// linter also rejects entire new tables.
func TestValidateServingSchemaAuditFailsForUncategorizedTable(t *testing.T) {
	t.Parallel()

	snapshot := ServingSchemaSnapshot{
		Tables: map[string][]string{
			"calls":                   {"call_id"},
			"synthetic_new_table":     {"id", "raw_json"},
			"governance_policy_state": {"config_sha256", "data_fingerprint"},
		},
	}
	err := ValidateServingSchemaAudit(snapshot, nil)
	if err == nil {
		t.Fatal("ValidateServingSchemaAudit accepted uncategorized table")
	}
	if !strings.Contains(err.Error(), "synthetic_new_table") {
		t.Fatalf("error %q must name the offending table", err.Error())
	}
}

// TestValidateServingSchemaAuditAcceptsCategorizedSnapshot proves the audit
// linter passes when every column is categorized.
func TestValidateServingSchemaAuditAcceptsCategorizedSnapshot(t *testing.T) {
	t.Parallel()

	snapshot := ServingSchemaSnapshot{
		Tables: map[string][]string{
			"calls":                       {"call_id", "title", "started_at"},
			"call_ai_highlights":          {"call_id", "highlight_text"},
			"call_facts":                  {"call_id", "lifecycle_bucket"},
			"governance_policy_state":     {"config_sha256", "data_fingerprint"},
			"governance_suppressed_calls": {"call_id"},
		},
	}
	if err := ValidateServingSchemaAudit(snapshot, nil); err != nil {
		t.Fatalf("ValidateServingSchemaAudit rejected categorized snapshot: %v", err)
	}
}

// TestValidateServingSchemaAuditSkipsSkippedTables proves the audit linter
// allows tables in the skipped list (operator-only state, profile cache,
// governance bookkeeping) to exist on the source without being categorized.
func TestValidateServingSchemaAuditSkipsSkippedTables(t *testing.T) {
	t.Parallel()

	snapshot := ServingSchemaSnapshot{
		Tables: map[string][]string{
			"calls":                           {"call_id"},
			"sync_runs":                       {"id", "status"},
			"profile_call_fact_cache":         {"profile_id", "call_id"},
			"governance_ingest_skipped_calls": {"call_id", "reason"},
		},
	}
	skipped := servingSkippedTables()
	if err := ValidateServingSchemaAudit(snapshot, skipped); err != nil {
		t.Fatalf("ValidateServingSchemaAudit failed for skipped tables: %v", err)
	}
}
