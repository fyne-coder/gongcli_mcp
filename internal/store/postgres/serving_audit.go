package postgres

import (
	"fmt"
	"sort"
	"strings"
)

// ServingColumnDecision categorizes a column copied from the operator cache
// into the redacted MCP serving database. Foundation D-1 requires that every
// column carrying customer identity or text/JSON has an explicit policy.
type ServingColumnDecision string

const (
	// ServingColumnAllowed means the column copies its source value verbatim
	// because the column does not carry customer identity (e.g. counts, dates,
	// integration IDs, profile aliases that have already been redacted).
	ServingColumnAllowed ServingColumnDecision = "allowed"

	// ServingColumnRedacted means the column copies a redacted/derived value;
	// the source value never reaches the serving DB unchanged. (Reserved for
	// future slices that introduce sanitization-during-copy.)
	ServingColumnRedacted ServingColumnDecision = "redacted"

	// ServingColumnNotCopied means the column exists in the source schema but
	// is intentionally absent from the serving DB; it must not be copied by
	// any future code path without an audit decision.
	ServingColumnNotCopied ServingColumnDecision = "not_copied"
)

// ServingColumnPolicy is one row of the schema/redaction audit. Each row
// names the table+column and the audit decision plus a one-line justification
// that explains why the decision is safe.
type ServingColumnPolicy struct {
	Table      string
	Column     string
	Decision   ServingColumnDecision
	Reason     string
	CarriesPII bool
}

// ServingSchemaAudit catalogs the operator-cache schema columns that the
// serving-DB refresh slice currently knows about. Foundation D-1 requires
// every column carrying customer identity, text, or JSON to be categorized;
// the linter test ValidateServingSchemaAudit rejects synthetic columns or
// tables that lack a decision.
//
// This is the source of truth for the audit, so adding a new copy in
// serving.go must be paired with a new entry here.
func ServingSchemaAudit() []ServingColumnPolicy {
	return []ServingColumnPolicy{
		// calls
		{Table: "calls", Column: "call_id", Decision: ServingColumnAllowed, Reason: "stable call identifier; redacted at read time via stable refs", CarriesPII: true},
		{Table: "calls", Column: "title", Decision: ServingColumnAllowed, Reason: "broad-public-redacted exposes title only over the physically redacted serving DB; emit-time blocklist guard backs up source-to-serving filtering", CarriesPII: true},
		{Table: "calls", Column: "started_at", Decision: ServingColumnAllowed, Reason: "timestamp; non-PII"},
		{Table: "calls", Column: "duration_seconds", Decision: ServingColumnAllowed, Reason: "duration; non-PII"},
		{Table: "calls", Column: "parties_count", Decision: ServingColumnAllowed, Reason: "count; non-PII"},
		{Table: "calls", Column: "context_present", Decision: ServingColumnAllowed, Reason: "boolean coverage flag; non-PII"},
		{Table: "calls", Column: "raw_json", Decision: ServingColumnAllowed, Reason: "raw call payload; same posture as source over a physically redacted serving DB", CarriesPII: true},
		{Table: "calls", Column: "raw_sha256", Decision: ServingColumnAllowed, Reason: "content hash; non-PII"},
		{Table: "calls", Column: "first_seen_at", Decision: ServingColumnAllowed, Reason: "import timestamp; non-PII"},
		{Table: "calls", Column: "updated_at", Decision: ServingColumnAllowed, Reason: "import timestamp; non-PII"},

		// users
		{Table: "users", Column: "user_id", Decision: ServingColumnAllowed, Reason: "Gong internal user ID; needed for participant correlations"},
		{Table: "users", Column: "email", Decision: ServingColumnAllowed, Reason: "internal-staff email; broad-public-redacted serving DB scopes this to redacted dataset", CarriesPII: true},
		{Table: "users", Column: "first_name", Decision: ServingColumnAllowed, Reason: "internal-staff name; redacted serving DB scope", CarriesPII: true},
		{Table: "users", Column: "last_name", Decision: ServingColumnAllowed, Reason: "internal-staff name; redacted serving DB scope", CarriesPII: true},
		{Table: "users", Column: "display_name", Decision: ServingColumnAllowed, Reason: "internal-staff name; redacted serving DB scope", CarriesPII: true},
		{Table: "users", Column: "title", Decision: ServingColumnAllowed, Reason: "internal-staff title; non-customer PII"},
		{Table: "users", Column: "active", Decision: ServingColumnAllowed, Reason: "boolean; non-PII"},
		{Table: "users", Column: "raw_json", Decision: ServingColumnAllowed, Reason: "raw user payload; redacted serving DB scope", CarriesPII: true},
		{Table: "users", Column: "raw_sha256", Decision: ServingColumnAllowed, Reason: "content hash; non-PII"},
		{Table: "users", Column: "first_seen_at", Decision: ServingColumnAllowed, Reason: "import timestamp; non-PII"},
		{Table: "users", Column: "updated_at", Decision: ServingColumnAllowed, Reason: "import timestamp; non-PII"},

		// transcripts
		{Table: "transcripts", Column: "call_id", Decision: ServingColumnAllowed, Reason: "join key; same posture as calls.call_id", CarriesPII: true},
		{Table: "transcripts", Column: "raw_json", Decision: ServingColumnAllowed, Reason: "transcript payload; redacted serving DB scope", CarriesPII: true},
		{Table: "transcripts", Column: "raw_sha256", Decision: ServingColumnAllowed, Reason: "content hash; non-PII"},
		{Table: "transcripts", Column: "segment_count", Decision: ServingColumnAllowed, Reason: "count; non-PII"},
		{Table: "transcripts", Column: "first_seen_at", Decision: ServingColumnAllowed, Reason: "import timestamp; non-PII"},
		{Table: "transcripts", Column: "updated_at", Decision: ServingColumnAllowed, Reason: "import timestamp; non-PII"},

		// transcript_segments
		{Table: "transcript_segments", Column: "call_id", Decision: ServingColumnAllowed, Reason: "join key; same posture as calls.call_id", CarriesPII: true},
		{Table: "transcript_segments", Column: "segment_index", Decision: ServingColumnAllowed, Reason: "ordering integer; non-PII"},
		{Table: "transcript_segments", Column: "speaker_id", Decision: ServingColumnAllowed, Reason: "Gong-internal speaker handle; redacted serving DB scope", CarriesPII: true},
		{Table: "transcript_segments", Column: "start_ms", Decision: ServingColumnAllowed, Reason: "offset; non-PII"},
		{Table: "transcript_segments", Column: "end_ms", Decision: ServingColumnAllowed, Reason: "offset; non-PII"},
		{Table: "transcript_segments", Column: "text", Decision: ServingColumnAllowed, Reason: "transcript text; redacted serving DB scope", CarriesPII: true},
		{Table: "transcript_segments", Column: "raw_json", Decision: ServingColumnAllowed, Reason: "raw segment payload; redacted serving DB scope", CarriesPII: true},

		// call_context_objects
		{Table: "call_context_objects", Column: "call_id", Decision: ServingColumnAllowed, Reason: "join key; redacted serving DB scope", CarriesPII: true},
		{Table: "call_context_objects", Column: "object_key", Decision: ServingColumnAllowed, Reason: "Gong-internal sequence; non-PII"},
		{Table: "call_context_objects", Column: "object_type", Decision: ServingColumnAllowed, Reason: "object type label; non-PII"},
		{Table: "call_context_objects", Column: "object_id", Decision: ServingColumnAllowed, Reason: "CRM ID; redacted serving DB scope", CarriesPII: true},
		{Table: "call_context_objects", Column: "object_name", Decision: ServingColumnAllowed, Reason: "CRM object name (account/opportunity); broad-public-redacted scope; emit-time blocklist guard backs up redaction", CarriesPII: true},
		{Table: "call_context_objects", Column: "raw_json", Decision: ServingColumnAllowed, Reason: "raw context object payload; redacted serving DB scope", CarriesPII: true},

		// call_context_fields
		{Table: "call_context_fields", Column: "call_id", Decision: ServingColumnAllowed, Reason: "join key; redacted serving DB scope", CarriesPII: true},
		{Table: "call_context_fields", Column: "object_key", Decision: ServingColumnAllowed, Reason: "context object reference; non-PII"},
		{Table: "call_context_fields", Column: "field_name", Decision: ServingColumnAllowed, Reason: "CRM field name; non-PII"},
		{Table: "call_context_fields", Column: "field_label", Decision: ServingColumnAllowed, Reason: "CRM field label; non-PII"},
		{Table: "call_context_fields", Column: "field_type", Decision: ServingColumnAllowed, Reason: "CRM field type; non-PII"},
		{Table: "call_context_fields", Column: "field_value_text", Decision: ServingColumnAllowed, Reason: "CRM field value text; redacted serving DB scope; broad-public-redacted policy can hide CRM value snippets", CarriesPII: true},
		{Table: "call_context_fields", Column: "raw_json", Decision: ServingColumnAllowed, Reason: "raw field payload; redacted serving DB scope", CarriesPII: true},

		// gong_settings
		{Table: "gong_settings", Column: "kind", Decision: ServingColumnAllowed, Reason: "settings category; non-PII"},
		{Table: "gong_settings", Column: "object_id", Decision: ServingColumnAllowed, Reason: "Gong settings ID; non-PII"},
		{Table: "gong_settings", Column: "name", Decision: ServingColumnAllowed, Reason: "scorecard/setting name; non-PII"},
		{Table: "gong_settings", Column: "active", Decision: ServingColumnAllowed, Reason: "boolean; non-PII"},
		{Table: "gong_settings", Column: "raw_json", Decision: ServingColumnAllowed, Reason: "raw settings payload; non-customer PII"},
		{Table: "gong_settings", Column: "raw_sha256", Decision: ServingColumnAllowed, Reason: "content hash; non-PII"},
		{Table: "gong_settings", Column: "first_seen_at", Decision: ServingColumnAllowed, Reason: "import timestamp; non-PII"},
		{Table: "gong_settings", Column: "updated_at", Decision: ServingColumnAllowed, Reason: "import timestamp; non-PII"},

		// scorecard_activity
		{Table: "scorecard_activity", Column: "answered_scorecard_id", Decision: ServingColumnAllowed, Reason: "Gong activity ID; non-customer PII"},
		{Table: "scorecard_activity", Column: "scorecard_id", Decision: ServingColumnAllowed, Reason: "scorecard ID; non-customer PII"},
		{Table: "scorecard_activity", Column: "scorecard_name", Decision: ServingColumnAllowed, Reason: "scorecard label; non-customer PII"},
		{Table: "scorecard_activity", Column: "call_id", Decision: ServingColumnAllowed, Reason: "join key; same posture as calls.call_id", CarriesPII: true},
		{Table: "scorecard_activity", Column: "call_started_at", Decision: ServingColumnAllowed, Reason: "timestamp; non-PII"},
		{Table: "scorecard_activity", Column: "reviewed_user_id", Decision: ServingColumnAllowed, Reason: "internal user reference; non-customer PII"},
		{Table: "scorecard_activity", Column: "reviewer_user_id", Decision: ServingColumnAllowed, Reason: "internal user reference; non-customer PII"},
		{Table: "scorecard_activity", Column: "editor_user_id", Decision: ServingColumnAllowed, Reason: "internal user reference; non-customer PII"},
		{Table: "scorecard_activity", Column: "review_method", Decision: ServingColumnAllowed, Reason: "enum; non-PII"},
		{Table: "scorecard_activity", Column: "review_time", Decision: ServingColumnAllowed, Reason: "timestamp; non-PII"},
		{Table: "scorecard_activity", Column: "visibility_type", Decision: ServingColumnAllowed, Reason: "enum; non-PII"},
		{Table: "scorecard_activity", Column: "overall_score", Decision: ServingColumnAllowed, Reason: "score; non-PII"},
		{Table: "scorecard_activity", Column: "average_score", Decision: ServingColumnAllowed, Reason: "score; non-PII"},
		{Table: "scorecard_activity", Column: "answer_count", Decision: ServingColumnAllowed, Reason: "count; non-PII"},
		{Table: "scorecard_activity", Column: "raw_json", Decision: ServingColumnAllowed, Reason: "raw activity payload; redacted serving DB scope", CarriesPII: true},
		{Table: "scorecard_activity", Column: "raw_sha256", Decision: ServingColumnAllowed, Reason: "content hash; non-PII"},
		{Table: "scorecard_activity", Column: "first_seen_at", Decision: ServingColumnAllowed, Reason: "import timestamp; non-PII"},
		{Table: "scorecard_activity", Column: "updated_at", Decision: ServingColumnAllowed, Reason: "import timestamp; non-PII"},

		// call_facts (rebuilt from calls/transcripts on the target)
		{Table: "call_facts", Column: "call_id", Decision: ServingColumnAllowed, Reason: "join key; rebuilt from filtered calls"},
		{Table: "call_facts", Column: "lifecycle_bucket", Decision: ServingColumnAllowed, Reason: "label; non-PII"},
		{Table: "call_facts", Column: "transcript_present", Decision: ServingColumnAllowed, Reason: "boolean; non-PII"},
		{Table: "call_facts", Column: "transcript_status", Decision: ServingColumnAllowed, Reason: "enum; non-PII"},
		{Table: "call_facts", Column: "duration_seconds", Decision: ServingColumnAllowed, Reason: "duration; non-PII"},
		{Table: "call_facts", Column: "started_at", Decision: ServingColumnAllowed, Reason: "timestamp; non-PII"},
		{Table: "call_facts", Column: "lifecycle_confidence", Decision: ServingColumnAllowed, Reason: "score; non-PII"},
		{Table: "call_facts", Column: "opportunity_count", Decision: ServingColumnAllowed, Reason: "count; non-PII"},
		{Table: "call_facts", Column: "account_count", Decision: ServingColumnAllowed, Reason: "count; non-PII"},

		// call_ai_highlights (read-model derivative)
		{Table: "call_ai_highlights", Column: "call_id", Decision: ServingColumnAllowed, Reason: "join key; rebuilt from filtered calls"},
		{Table: "call_ai_highlights", Column: "highlight_text", Decision: ServingColumnAllowed, Reason: "AI-condensed text; redacted serving DB scope", CarriesPII: true},

		// governance state
		{Table: "governance_policy_state", Column: "config_sha256", Decision: ServingColumnAllowed, Reason: "policy fingerprint; non-PII"},
		{Table: "governance_policy_state", Column: "data_fingerprint", Decision: ServingColumnAllowed, Reason: "data fingerprint; non-PII"},
		{Table: "governance_suppressed_calls", Column: "call_id", Decision: ServingColumnAllowed, Reason: "ledger of suppressed call IDs; never copied from source", CarriesPII: true},
	}
}

// ServingSchemaAuditTables enumerates every table referenced by
// ServingSchemaAudit; useful for quick set-membership tests.
func ServingSchemaAuditTables() []string {
	seen := map[string]struct{}{}
	for _, p := range ServingSchemaAudit() {
		seen[p.Table] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for table := range seen {
		out = append(out, table)
	}
	sort.Strings(out)
	return out
}

// ServingSchemaSnapshot describes the columns observed in some live source
// schema. Tests use it to feed a synthetic snapshot into the validator.
type ServingSchemaSnapshot struct {
	Tables map[string][]string
}

// ValidateServingSchemaAudit fails when a column or table observed in the
// supplied snapshot is not categorized in ServingSchemaAudit. Foundation D-1
// requires that newly introduced text/JSON/ID columns force an explicit
// allow/redact decision before they can be copied to the serving DB.
//
// Tables in skippedTables (operator-only or governance bookkeeping) are
// allowed to exist on the source without an audit row because they are
// intentionally not copied; the snapshot simply confirms they are not
// referenced from the audit either.
func ValidateServingSchemaAudit(snapshot ServingSchemaSnapshot, skippedTables []string) error {
	allowed := map[string]map[string]struct{}{}
	for _, p := range ServingSchemaAudit() {
		if _, ok := allowed[p.Table]; !ok {
			allowed[p.Table] = map[string]struct{}{}
		}
		allowed[p.Table][p.Column] = struct{}{}
	}
	skipped := map[string]struct{}{}
	for _, t := range skippedTables {
		skipped[t] = struct{}{}
	}
	var missing []string
	for table, cols := range snapshot.Tables {
		if _, ok := skipped[table]; ok {
			continue
		}
		columns, ok := allowed[table]
		if !ok {
			missing = append(missing, fmt.Sprintf("table %s lacks an audit decision", table))
			continue
		}
		for _, col := range cols {
			if _, ok := columns[col]; !ok {
				missing = append(missing, fmt.Sprintf("column %s.%s lacks an audit decision", table, col))
			}
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return fmt.Errorf("serving schema audit missing decisions: %s", strings.Join(missing, "; "))
}
