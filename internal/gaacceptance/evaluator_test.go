package gaacceptance

import (
	"encoding/json"
	"strings"
	"testing"
)

// loadPassBundle returns a synthetic happy-path ProbeBundle used as the
// baseline for red/green tests. Each test mutates a single field to assert one
// degradation/failure mode in isolation. Values are entirely synthetic; no
// real customer or tenant data appears.
func loadPassBundle(t *testing.T) ProbeBundle {
	t.Helper()
	statusJSON := `{
  "mcp_server": {
    "name": "gongmcp",
    "version": "0.3.4",
    "commit": "abcdef0",
    "build_date": "2026-05-07T00:00:00Z",
    "tool_preset": "business-workbench",
    "deployment_id": "ga-acceptance-smoke-fixture",
    "started_at_utc": "2026-05-07T20:00:00Z",
    "tool_count": 6,
    "facade_routed_tool_count": 30,
    "transcript_evidence_provenance": "redacted",
    "policy_switches": {
      "hide_account_names": false,
      "hide_call_titles": true,
      "hide_raw_call_ids": true,
      "hide_contact_names": true
    },
    "policy_switches_enabled": ["hide_call_titles", "hide_raw_call_ids", "hide_contact_names"],
    "policy_switch_reload_contract": "restart-required"
  },
  "total_calls": 4803,
  "total_users": 312,
  "total_transcripts": 4803,
  "total_transcript_segments": 1100000,
  "total_scorecards": 4,
  "total_scorecard_activity": 1200,
  "total_ai_highlights": 4500,
  "missing_transcripts": 0,
  "running_sync_runs": 0,
  "call_facts_attribution": {
    "account_industry_call_count": 4500,
    "opportunity_stage_call_count": 4400,
    "lifecycle_classified_call_count": 4500,
    "opportunity_call_count": 4400,
    "account_call_count": 4500,
    "has_any_attribution_signal": true
  },
  "profile_readiness": {
    "active": true,
    "status": "ready",
    "detail": "synthetic profile ready",
    "lifecycle_bucket_count": 4,
    "methodology_concept_count": 6,
    "checklist": {
      "computed": true,
      "lifecycle_rules_missing": false,
      "methodology_unmapped": false,
      "loss_reason_mapping_missing": false
    }
  },
  "public_readiness": {
    "transcript_coverage": {"ready": true, "status": "ready", "detail": "synthetic"},
    "scorecard_themes": {"ready": true, "status": "ready", "detail": "synthetic"},
    "lifecycle_separation": {"ready": true, "status": "ready", "detail": "synthetic"},
    "crm_segmentation": {"ready": true, "status": "ready", "detail": "synthetic"},
    "attribution_readiness": {"ready": true, "status": "ready", "detail": "synthetic"},
    "conversation_volume": {"ready": true, "status": "ready", "detail": "synthetic"}
  },
  "attribution_coverage": {
    "calls_with_titles": 4803,
    "calls_with_parties": 4803,
    "calls_with_party_titles": 4500,
    "users_with_titles": 312,
    "account_name_calls": 4500,
    "account_industry_calls": 4500,
    "opportunity_stage_calls": 4400,
    "objects_with_names": 4500,
    "participant_status": "available",
    "person_title_status": "available"
  }
}`
	return ProbeBundle{
		Status:    json.RawMessage(statusJSON),
		ToolsList: []string{"gong_status", "gong_discover_capabilities", "gong_query", "gong_analyze", "gong_get_evidence", "gong_explain_limitations"},
		FacadeOperations: []FacadeOperationProbe{
			{Operation: "status.sync", FacadeTool: "gong_status", RoutedTool: "get_sync_status"},
			{Operation: "question.answer", FacadeTool: "gong_analyze", RoutedTool: "question_answer"},
			{Operation: "theme_intelligence_report", FacadeTool: "gong_analyze", RoutedTool: "theme_intelligence_report"},
			{Operation: "evidence.quotes.search", FacadeTool: "gong_get_evidence", RoutedTool: "search_quotes_in_cohort"},
			{Operation: "evidence.highlights.list", FacadeTool: "gong_get_evidence", RoutedTool: "list_call_ai_highlights"},
			{Operation: "evidence.call_drilldown", FacadeTool: "gong_get_evidence", RoutedTool: "call_drilldown"},
		},
		QuestionAnswer: &QuestionAnswerProbe{
			Question:            "What pain themes did pilot accounts mention this quarter?",
			EvidencePackPresent: true,
			CallRefs:            []string{"call_ref_synthetic_aaa1"},
			ItemCount:           5,
		},
		CallDrilldown: &CallDrilldownProbe{
			CallRef:               "call_ref_synthetic_aaa1",
			BoundedSnippetCount:   4,
			GongAISourcePathCount: 2,
			SnippetsScopedToCall:  true,
		},
		AccountQueryWithoutOptIn: &AccountQueryProbe{
			IsError:  true,
			ErrorMsg: "account_query requires include_account_names=true because it can probe customer names",
		},
		AccountQueryWithOptIn: &AccountQueryProbe{
			IsError:  false,
			RowCount: 3,
		},
		RawCallIDsHidden: boolPtr(true),
		RedactionAudit: &RedactionAuditProbe{
			Available:               true,
			SourceMinusRedactedRows: 0,
			GeneratedAt:             "2026-05-07T18:00:00Z",
		},
		ReadOnlyPosture: &ReadOnlyPostureProbe{
			Provided:           true,
			WriteDenied:        true,
			WriteDenialDetail:  "permission denied for relation calls",
			RawTableReadDenied: true,
			RawTableReadDetail: "permission denied for relation calls_raw",
		},
	}
}

func boolPtr(v bool) *bool { return &v }

func mustEvaluate(t *testing.T, bundle ProbeBundle) Report {
	t.Helper()
	rep, err := Evaluate(bundle)
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	return rep
}

func findCheck(t *testing.T, rep Report, id string) Check {
	t.Helper()
	for _, c := range rep.Checks {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("check %q not found in report (have: %v)", id, checkIDs(rep))
	return Check{}
}

func checkIDs(rep Report) []string {
	ids := make([]string, 0, len(rep.Checks))
	for _, c := range rep.Checks {
		ids = append(ids, c.ID)
	}
	return ids
}

func TestEvaluatePassBundle(t *testing.T) {
	bundle := loadPassBundle(t)
	rep := mustEvaluate(t, bundle)
	if rep.Status != StatusPass {
		t.Fatalf("overall status=%q want pass; checks=%+v", rep.Status, rep.Checks)
	}
	wantIDs := []string{
		CheckRuntimeIdentity,
		CheckToolSurface,
		CheckRoutedOperations,
		CheckDataReadiness,
		CheckGovernanceRedaction,
		CheckEvidenceWorkflow,
		CheckReadOnlyPosture,
	}
	for _, id := range wantIDs {
		c := findCheck(t, rep, id)
		if c.Status != StatusPass {
			t.Errorf("check %s status=%q reason=%q want pass", id, c.Status, c.Reason)
		}
	}
	if rep.Summary.DeploymentID == "" {
		t.Errorf("Summary.DeploymentID empty; want fixture value")
	}
	if rep.Summary.ToolPreset != "business-workbench" {
		t.Errorf("Summary.ToolPreset=%q want business-workbench", rep.Summary.ToolPreset)
	}
	if rep.Summary.VisibleToolCount != 6 {
		t.Errorf("Summary.VisibleToolCount=%d want 6", rep.Summary.VisibleToolCount)
	}
}

func TestEvaluateMissingToolSurface(t *testing.T) {
	bundle := loadPassBundle(t)
	// Drop one of the six required facade tools.
	bundle.ToolsList = []string{"gong_status", "gong_discover_capabilities", "gong_query", "gong_analyze", "gong_get_evidence"}
	rep := mustEvaluate(t, bundle)
	if rep.Status != StatusFail {
		t.Fatalf("overall=%q want fail when tool surface incomplete", rep.Status)
	}
	c := findCheck(t, rep, CheckToolSurface)
	if c.Status != StatusFail {
		t.Errorf("tool_surface status=%q want fail", c.Status)
	}
	if !strings.Contains(c.Reason, "gong_explain_limitations") {
		t.Errorf("tool_surface reason should name the missing tool, got %q", c.Reason)
	}
}

func TestEvaluateExtraToolSurfaceFails(t *testing.T) {
	bundle := loadPassBundle(t)
	bundle.ToolsList = append(bundle.ToolsList, "search_calls")
	rep := mustEvaluate(t, bundle)
	c := findCheck(t, rep, CheckToolSurface)
	if c.Status != StatusFail {
		t.Errorf("tool_surface status=%q want fail when business-workbench exposes >6 tools", c.Status)
	}
	if rep.Status != StatusFail {
		t.Errorf("overall=%q want fail", rep.Status)
	}
}

func TestEvaluateMissingRoutedOperation(t *testing.T) {
	bundle := loadPassBundle(t)
	// Drop theme_intelligence_report from routed operations.
	ops := make([]FacadeOperationProbe, 0, len(bundle.FacadeOperations))
	for _, op := range bundle.FacadeOperations {
		if op.Operation == "theme_intelligence_report" {
			continue
		}
		ops = append(ops, op)
	}
	bundle.FacadeOperations = ops
	rep := mustEvaluate(t, bundle)
	c := findCheck(t, rep, CheckRoutedOperations)
	if c.Status != StatusFail {
		t.Fatalf("routed_operations status=%q want fail", c.Status)
	}
	if !strings.Contains(c.Reason, "theme_intelligence_report") {
		t.Errorf("routed_operations reason should name missing op, got %q", c.Reason)
	}
}

func TestEvaluateDegradedProfileReadiness(t *testing.T) {
	bundle := loadPassBundle(t)
	// Mutate status to simulate a profile that imported but mapped concepts
	// only onto CreatedDate and left methodology unmapped.
	var status map[string]any
	if err := json.Unmarshal(bundle.Status, &status); err != nil {
		t.Fatalf("unmarshal status: %v", err)
	}
	pr := status["profile_readiness"].(map[string]any)
	checklist := pr["checklist"].(map[string]any)
	checklist["methodology_unmapped"] = true
	checklist["loss_reason_mapping_missing"] = true
	checklist["created_date_only_concepts"] = []any{"opportunity_close_date"}
	checklist["suspect_findings"] = []any{
		"field concept opportunity_close_date is mapped only to CreatedDate",
		"loss reason field is not mapped",
		"methodology section is empty",
	}
	raw, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("re-marshal status: %v", err)
	}
	bundle.Status = raw

	rep := mustEvaluate(t, bundle)
	c := findCheck(t, rep, CheckDataReadiness)
	if c.Status != StatusDegraded {
		t.Fatalf("data_readiness status=%q want degraded; reason=%q", c.Status, c.Reason)
	}
	if !strings.Contains(c.Reason, "methodology") {
		t.Errorf("data_readiness reason should call out methodology gap, got %q", c.Reason)
	}
	if rep.Status != StatusDegraded {
		t.Errorf("overall=%q want degraded", rep.Status)
	}
}

func TestEvaluateMissingAIHighlights(t *testing.T) {
	bundle := loadPassBundle(t)
	var status map[string]any
	if err := json.Unmarshal(bundle.Status, &status); err != nil {
		t.Fatalf("unmarshal status: %v", err)
	}
	status["total_ai_highlights"] = float64(0)
	raw, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("re-marshal status: %v", err)
	}
	bundle.Status = raw

	rep := mustEvaluate(t, bundle)
	c := findCheck(t, rep, CheckDataReadiness)
	if c.Status != StatusDegraded {
		t.Fatalf("data_readiness status=%q want degraded", c.Status)
	}
	if !strings.Contains(strings.ToLower(c.Reason), "ai highlight") {
		t.Errorf("data_readiness reason should mention AI highlight inventory, got %q", c.Reason)
	}
}

func TestEvaluateAccountQueryOptInDenialMissing(t *testing.T) {
	bundle := loadPassBundle(t)
	// Simulate account_query without opt-in NOT failing closed.
	bundle.AccountQueryWithoutOptIn = &AccountQueryProbe{IsError: false, RowCount: 7}
	rep := mustEvaluate(t, bundle)
	c := findCheck(t, rep, CheckGovernanceRedaction)
	if c.Status != StatusFail {
		t.Fatalf("governance_redaction status=%q want fail", c.Status)
	}
	if !strings.Contains(c.Reason, "account_query") {
		t.Errorf("governance_redaction reason should mention account_query, got %q", c.Reason)
	}
	if rep.Status != StatusFail {
		t.Errorf("overall=%q want fail", rep.Status)
	}
}

func TestEvaluateRawCallIDsVisibleFails(t *testing.T) {
	bundle := loadPassBundle(t)
	bundle.RawCallIDsHidden = boolPtr(false)
	rep := mustEvaluate(t, bundle)
	c := findCheck(t, rep, CheckGovernanceRedaction)
	if c.Status != StatusFail {
		t.Fatalf("governance_redaction status=%q want fail when raw IDs visible", c.Status)
	}
	if !strings.Contains(strings.ToLower(c.Reason), "raw call id") {
		t.Errorf("reason should mention raw call IDs, got %q", c.Reason)
	}
}

func TestEvaluateEvidenceWorkflowDrilldownMismatch(t *testing.T) {
	bundle := loadPassBundle(t)
	// Simulate the drill-down probe targeting a different call_ref than what
	// question.answer produced; this breaks the documented workflow chain.
	bundle.CallDrilldown.CallRef = "call_ref_synthetic_other_zzz"
	rep := mustEvaluate(t, bundle)
	c := findCheck(t, rep, CheckEvidenceWorkflow)
	if c.Status != StatusFail {
		t.Fatalf("evidence_workflow status=%q want fail", c.Status)
	}
	if !strings.Contains(c.Reason, "call_ref") {
		t.Errorf("reason should mention call_ref mismatch, got %q", c.Reason)
	}
}

func TestEvaluateEvidenceWorkflowEmptyEvidencePack(t *testing.T) {
	bundle := loadPassBundle(t)
	bundle.QuestionAnswer.EvidencePackPresent = false
	bundle.QuestionAnswer.ItemCount = 0
	bundle.QuestionAnswer.CallRefs = nil
	rep := mustEvaluate(t, bundle)
	c := findCheck(t, rep, CheckEvidenceWorkflow)
	if c.Status != StatusFail {
		t.Fatalf("evidence_workflow status=%q want fail", c.Status)
	}
}

func TestEvaluateEvidenceWorkflowMissingGongAISourcePath(t *testing.T) {
	bundle := loadPassBundle(t)
	bundle.CallDrilldown.GongAISourcePathCount = 0
	rep := mustEvaluate(t, bundle)
	c := findCheck(t, rep, CheckEvidenceWorkflow)
	if c.Status != StatusFail {
		t.Fatalf("evidence_workflow status=%q want fail", c.Status)
	}
	if !strings.Contains(strings.ToLower(c.Reason), "gong ai") {
		t.Errorf("reason should mention Gong AI source paths, got %q", c.Reason)
	}
}

func TestEvaluateReadOnlyPostureNotProvided(t *testing.T) {
	bundle := loadPassBundle(t)
	bundle.ReadOnlyPosture = &ReadOnlyPostureProbe{Provided: false}
	rep := mustEvaluate(t, bundle)
	c := findCheck(t, rep, CheckReadOnlyPosture)
	if c.Status != StatusDegraded {
		t.Fatalf("read_only_posture status=%q want degraded when DB inputs not supplied", c.Status)
	}
	if !strings.Contains(strings.ToLower(c.Reason), "not supplied") {
		t.Errorf("reason should explain DB inputs were not supplied, got %q", c.Reason)
	}
}

func TestEvaluateReadOnlyPostureWriteSucceeded(t *testing.T) {
	bundle := loadPassBundle(t)
	bundle.ReadOnlyPosture.WriteDenied = false
	bundle.ReadOnlyPosture.WriteDenialDetail = "INSERT 0 1"
	rep := mustEvaluate(t, bundle)
	c := findCheck(t, rep, CheckReadOnlyPosture)
	if c.Status != StatusFail {
		t.Fatalf("read_only_posture status=%q want fail when write succeeded", c.Status)
	}
}

func TestEvaluateReadOnlyPostureRawTableSelectAllowed(t *testing.T) {
	bundle := loadPassBundle(t)
	bundle.ReadOnlyPosture.RawTableReadDenied = false
	bundle.ReadOnlyPosture.RawTableReadDetail = "1 row"
	rep := mustEvaluate(t, bundle)
	c := findCheck(t, rep, CheckReadOnlyPosture)
	if c.Status != StatusFail {
		t.Fatalf("read_only_posture status=%q want fail when raw read allowed", c.Status)
	}
}

func TestRenderOperatorMarkdownContainsSummary(t *testing.T) {
	rep := mustEvaluate(t, loadPassBundle(t))
	md := RenderOperatorMarkdown(rep)
	for _, want := range []string{
		"GA Customer Acceptance",
		"ga-acceptance-smoke-fixture",
		"business-workbench",
		"runtime_identity",
		"tool_surface",
		"routed_operations",
		"data_readiness",
		"governance_redaction",
		"evidence_workflow",
		"read_only_posture",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("operator markdown missing %q; got:\n%s", want, md)
		}
	}
}
