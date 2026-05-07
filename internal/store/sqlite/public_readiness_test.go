package sqlite

import (
	"strings"
	"testing"
)

// TestBuildPublicReadinessUsesCallFactsSignals locks in the contract that
// gong_status reports CRMSegmentation/AttributionReadiness as ready when the
// curated call_facts attribution signals are populated even if the upstream
// CRM integration inventory and call_context_fields counts are zero. The
// Postgres redacted serving DB exhibits this exact shape: call_context_fields
// is pruned, crm_integrations is empty, but call_facts is fully materialized.
// Without this fallback the redacted-deployment readiness reads as blocked
// purely because of a CRM integration inventory gap.
func TestBuildPublicReadinessUsesCallFactsSignals(t *testing.T) {
	t.Parallel()

	t.Run("call_facts signals promote readiness when crm inventory is empty", func(t *testing.T) {
		t.Parallel()
		summary := &SyncStatusSummary{
			TotalCalls:             10,
			MissingTranscripts:     0,
			TotalScorecards:        2,
			TotalEmbeddedCRMFields: 0,
			TotalCRMIntegrations:   0,
			TotalCRMSchemaFields:   0,
			CallFactsAttribution: CallFactsAttributionSignals{
				AccountIndustryCallCount:     8,
				OpportunityStageCallCount:    7,
				LifecycleClassifiedCallCount: 6,
				HasAnyAttributionSignal:      true,
			},
		}
		got := BuildPublicReadiness(summary)
		if !got.CRMSegmentation.Ready {
			t.Fatalf("CRMSegmentation.Ready=false, want true when call_facts signals are populated: %+v", got.CRMSegmentation)
		}
		if got.AttributionReadiness.Status == "needs_crm_context" {
			t.Fatalf("AttributionReadiness.Status=needs_crm_context, want partial/ready when call_facts signals are populated: %+v", got.AttributionReadiness)
		}
		if got.CRMInventoryNote == "" || !strings.Contains(got.CRMInventoryNote, "call_facts") {
			t.Fatalf("CRMInventoryNote should explain that call_facts signals are populated, got %q", got.CRMInventoryNote)
		}
	})

	t.Run("blocked when neither embedded crm fields nor call_facts signals exist", func(t *testing.T) {
		t.Parallel()
		summary := &SyncStatusSummary{
			TotalCalls:             10,
			MissingTranscripts:     0,
			TotalScorecards:        2,
			TotalEmbeddedCRMFields: 0,
			CallFactsAttribution:   CallFactsAttributionSignals{HasAnyAttributionSignal: false},
		}
		got := BuildPublicReadiness(summary)
		if got.CRMSegmentation.Ready {
			t.Fatalf("CRMSegmentation.Ready=true, want false when no signals are available: %+v", got.CRMSegmentation)
		}
		if got.CRMSegmentation.Status != "needs_crm_context" {
			t.Fatalf("CRMSegmentation.Status=%q, want needs_crm_context", got.CRMSegmentation.Status)
		}
	})

	t.Run("embedded crm fields keep their original ready note", func(t *testing.T) {
		t.Parallel()
		summary := &SyncStatusSummary{
			TotalCalls:             10,
			MissingTranscripts:     0,
			TotalScorecards:        2,
			TotalEmbeddedCRMFields: 50,
			TotalCRMIntegrations:   0,
			TotalCRMSchemaFields:   0,
			CallFactsAttribution:   CallFactsAttributionSignals{HasAnyAttributionSignal: false},
		}
		got := BuildPublicReadiness(summary)
		if !got.CRMSegmentation.Ready {
			t.Fatalf("CRMSegmentation.Ready=false, want true when call_context_fields are populated: %+v", got.CRMSegmentation)
		}
		if got.CRMInventoryNote == "" || !strings.Contains(got.CRMInventoryNote, "Embedded CRM context") {
			t.Fatalf("CRMInventoryNote should preserve the embedded-context preamble, got %q", got.CRMInventoryNote)
		}
	})

	t.Run("recommended next action skips CRM resync when call_facts signals are populated", func(t *testing.T) {
		t.Parallel()
		summary := &SyncStatusSummary{
			TotalCalls:             10,
			MissingTranscripts:     0,
			TotalScorecards:        2,
			TotalEmbeddedCRMFields: 0,
			CallFactsAttribution: CallFactsAttributionSignals{
				AccountIndustryCallCount:     1,
				OpportunityStageCallCount:    1,
				LifecycleClassifiedCallCount: 1,
				HasAnyAttributionSignal:      true,
			},
		}
		got := BuildPublicReadiness(summary)
		if strings.Contains(got.RecommendedNextAction, "Re-run call sync") {
			t.Fatalf("RecommendedNextAction should not nag about CRM resync when call_facts signals are populated, got %q", got.RecommendedNextAction)
		}
	})
}
