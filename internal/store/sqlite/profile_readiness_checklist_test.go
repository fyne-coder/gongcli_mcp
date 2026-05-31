package sqlite

import (
	"sort"
	"strings"
	"testing"

	profilepkg "github.com/fyne-coder/gongcli_mcp/internal/profile"
)

// TestEvaluateProfileReadinessChecklist exercises the deterministic suspect
// finding rules: a CreatedDate-only field concept, missing required lifecycle
// buckets, an empty methodology section, and the absence of any loss-reason
// field mapping. These are intentionally mechanical so the failure modes
// stay reviewable without per-tenant heuristics.
func TestEvaluateProfileReadinessChecklist(t *testing.T) {
	t.Parallel()

	t.Run("nil profile returns empty checklist not computed", func(t *testing.T) {
		t.Parallel()
		got := EvaluateProfileReadinessChecklist(nil)
		if got.Computed {
			t.Fatalf("Computed=true for nil profile, want false")
		}
		if len(got.SuspectFindings) != 0 || len(got.CreatedDateOnlyConcepts) != 0 || len(got.MissingLifecycleBuckets) != 0 {
			t.Fatalf("checklist for nil profile should be empty, got %+v", got)
		}
	})

	t.Run("flags created_date_only field concepts", func(t *testing.T) {
		t.Parallel()
		p := &profilepkg.Profile{
			Fields: map[string]profilepkg.FieldMapping{
				"deal_stage":    {Names: []string{"CreatedDate"}},
				"deal_started":  {Names: []string{"created_date"}},
				"deal_amount":   {Names: []string{"Amount"}},
				"deal_loss":     {Names: []string{"LossReason"}},
				"deal_combined": {Names: []string{"CreatedDate", "StageName"}},
			},
			Lifecycle: map[string]profilepkg.LifecycleBucket{
				"open":        {Rules: []profilepkg.Rule{{Op: "any"}}},
				"closed_won":  {Rules: []profilepkg.Rule{{Op: "any"}}},
				"closed_lost": {Rules: []profilepkg.Rule{{Op: "any"}}},
				"post_sales":  {Rules: []profilepkg.Rule{{Op: "any"}}},
				"unknown":     {Rules: []profilepkg.Rule{{Op: "any"}}},
			},
			Methodology: map[string]profilepkg.MethodologyConcept{
				"pain": {Description: "pain"},
			},
		}
		got := EvaluateProfileReadinessChecklist(p)
		if !got.Computed {
			t.Fatalf("Computed=false, want true for parsed profile")
		}
		want := []string{"deal_stage", "deal_started"}
		sort.Strings(want)
		if !equalStringSlices(got.CreatedDateOnlyConcepts, want) {
			t.Fatalf("CreatedDateOnlyConcepts=%v want %v", got.CreatedDateOnlyConcepts, want)
		}
		if !findingsContains(got.SuspectFindings, "created_date_only_field_concepts") {
			t.Fatalf("findings should call out created_date_only mapping, got %v", got.SuspectFindings)
		}
		if got.LossReasonMappingMissing {
			t.Fatalf("LossReasonMappingMissing=true, but profile maps LossReason")
		}
		if got.MethodologyUnmapped {
			t.Fatalf("MethodologyUnmapped=true, but profile defines pain")
		}
		if got.LifecycleRulesMissing {
			t.Fatalf("LifecycleRulesMissing=true, but every bucket has rules")
		}
		if len(got.MissingLifecycleBuckets) != 0 {
			t.Fatalf("MissingLifecycleBuckets=%v want empty", got.MissingLifecycleBuckets)
		}
	})

	t.Run("flags missing lifecycle buckets and missing rules", func(t *testing.T) {
		t.Parallel()
		p := &profilepkg.Profile{
			Lifecycle: map[string]profilepkg.LifecycleBucket{
				"open":       {},
				"closed_won": {},
			},
		}
		got := EvaluateProfileReadinessChecklist(p)
		want := []string{"closed_lost", "post_sales", "unknown"}
		if !equalStringSlices(got.MissingLifecycleBuckets, want) {
			t.Fatalf("MissingLifecycleBuckets=%v want %v", got.MissingLifecycleBuckets, want)
		}
		if !got.LifecycleRulesMissing {
			t.Fatalf("LifecycleRulesMissing=false, want true when every defined bucket has zero rules")
		}
		if !findingsContains(got.SuspectFindings, "missing_lifecycle_buckets") {
			t.Fatalf("findings missing lifecycle buckets entry: %v", got.SuspectFindings)
		}
		if !findingsContains(got.SuspectFindings, "lifecycle_rules_missing") {
			t.Fatalf("findings missing lifecycle_rules_missing entry: %v", got.SuspectFindings)
		}
	})

	t.Run("flags methodology and loss reason mapping gaps", func(t *testing.T) {
		t.Parallel()
		p := &profilepkg.Profile{
			Fields: map[string]profilepkg.FieldMapping{
				"deal_stage": {Names: []string{"StageName"}},
			},
			Lifecycle: map[string]profilepkg.LifecycleBucket{
				"open":        {Rules: []profilepkg.Rule{{Op: "any"}}},
				"closed_won":  {Rules: []profilepkg.Rule{{Op: "any"}}},
				"closed_lost": {Rules: []profilepkg.Rule{{Op: "any"}}},
				"post_sales":  {Rules: []profilepkg.Rule{{Op: "any"}}},
				"unknown":     {Rules: []profilepkg.Rule{{Op: "any"}}},
			},
		}
		got := EvaluateProfileReadinessChecklist(p)
		if !got.MethodologyUnmapped {
			t.Fatalf("MethodologyUnmapped=false, want true for empty methodology")
		}
		if !got.LossReasonMappingMissing {
			t.Fatalf("LossReasonMappingMissing=false, want true with no loss-reason field")
		}
		if !findingsContains(got.SuspectFindings, "methodology_concepts_unmapped") {
			t.Fatalf("findings should call out methodology_concepts_unmapped: %v", got.SuspectFindings)
		}
		if !findingsContains(got.SuspectFindings, "loss_reason_mapping_missing") {
			t.Fatalf("findings should call out loss_reason_mapping_missing: %v", got.SuspectFindings)
		}
	})

	t.Run("loss reason mapping detected via methodology field reference", func(t *testing.T) {
		t.Parallel()
		p := &profilepkg.Profile{
			Methodology: map[string]profilepkg.MethodologyConcept{
				"loss_reason": {Fields: []profilepkg.FieldRef{{Object: "deal", Name: "LossReason"}}},
			},
		}
		got := EvaluateProfileReadinessChecklist(p)
		if got.LossReasonMappingMissing {
			t.Fatalf("LossReasonMappingMissing=true, but methodology references LossReason")
		}
	})
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func findingsContains(findings []string, prefix string) bool {
	for _, finding := range findings {
		if strings.HasPrefix(finding, prefix) {
			return true
		}
	}
	return false
}
