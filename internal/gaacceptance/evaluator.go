// Package gaacceptance evaluates probe bundles collected from a customer-hosted
// `business-workbench` Postgres MCP deployment and produces a non-secret
// pass/degraded/fail acceptance report. The evaluator is pure: it consumes a
// pre-collected ProbeBundle (so tests can use synthetic JSON fixtures) and
// emits a Report plus an operator-facing Markdown summary. A separate driver
// (the `scripts/postgres-ga-acceptance-smoke.sh` shell wrapper, or the
// `gongctl mcp ga-acceptance` CLI) is responsible for assembling the bundle
// from a live MCP endpoint and optional Postgres reader credentials.
package gaacceptance

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Status values used both for the overall report and for individual checks.
const (
	StatusPass     = "pass"
	StatusDegraded = "degraded"
	StatusFail     = "fail"
)

// Stable check identifiers. They are part of the operator-visible contract
// surfaced in the JSON report and Markdown summary.
const (
	CheckRuntimeIdentity     = "runtime_identity"
	CheckToolSurface         = "tool_surface"
	CheckRoutedOperations    = "routed_operations"
	CheckDataReadiness       = "data_readiness"
	CheckGovernanceRedaction = "governance_redaction"
	CheckEvidenceWorkflow    = "evidence_workflow"
	CheckReadOnlyPosture     = "read_only_posture"
)

// expectedFacadeTools is the stable six-tool surface for the
// `business-workbench` preset. Mirrored from internal/mcp/facade.go so the
// evaluator does not depend on the mcp package (the smoke can be run against
// a deployed binary that has its own pinned tool surface).
var expectedFacadeTools = []string{
	"gong_status",
	"gong_discover_capabilities",
	"gong_query",
	"gong_analyze",
	"gong_get_evidence",
	"gong_explain_limitations",
}

// requiredRoutedOperations are the operation names that must be reachable via
// the facade for a business-workbench deployment to pass acceptance. Mirrored
// from internal/mcp/facade.go.
var requiredRoutedOperations = []string{
	"question.answer",
	"theme_intelligence_report",
	"evidence.quotes.search",
	"evidence.highlights.list",
	"evidence.call_drilldown",
}

// ProbeBundle is the pre-collected input for Evaluate. Each field corresponds
// to one MCP probe or DB probe captured by the driver. Missing optional probes
// are degraded checks rather than failures.
type ProbeBundle struct {
	// Status is the JSON body returned by the gong_status / status.sync
	// operation. Only the public mcp_server, totals, profile_readiness,
	// public_readiness, attribution_coverage, call_facts_attribution
	// fields are read.
	Status json.RawMessage `json:"status"`

	// ToolsList is the list of tool names returned by tools/list under the
	// business-workbench preset.
	ToolsList []string `json:"tools_list"`

	// FacadeOperations is the list of routed facade operations advertised by
	// gong_discover_capabilities.
	FacadeOperations []FacadeOperationProbe `json:"facade_operations"`

	// QuestionAnswer summarizes a synthetic question.answer call. CallRefs
	// must include the call_ref later passed to evidence.call_drilldown.
	QuestionAnswer *QuestionAnswerProbe `json:"question_answer,omitempty"`

	// CallDrilldown summarizes the evidence.call_drilldown call that
	// consumed one of QuestionAnswer.CallRefs.
	CallDrilldown *CallDrilldownProbe `json:"call_drilldown,omitempty"`

	// AccountQueryWithoutOptIn captures the response when calling a
	// query.calls operation with account_query set but include_account_names
	// omitted; it must fail closed.
	AccountQueryWithoutOptIn *AccountQueryProbe `json:"account_query_without_opt_in,omitempty"`

	// AccountQueryWithOptIn captures the response when calling the same
	// query.calls operation with include_account_names=true; it should
	// succeed (or return no rows) but not error on missing opt-in.
	AccountQueryWithOptIn *AccountQueryProbe `json:"account_query_with_opt_in,omitempty"`

	// RawCallIDsHidden is true when the driver verified that no raw call IDs
	// (numeric Gong call IDs) appear in business-workbench tool outputs. nil
	// means the driver did not check; the evaluator treats nil as degraded.
	RawCallIDsHidden *bool `json:"raw_call_ids_hidden,omitempty"`

	// RedactionAudit summarizes any source-minus-redacted validation evidence.
	RedactionAudit *RedactionAuditProbe `json:"redaction_audit,omitempty"`

	// ReadOnlyPosture summarizes the optional Postgres scoped-reader probe.
	ReadOnlyPosture *ReadOnlyPostureProbe `json:"read_only_posture,omitempty"`
}

// FacadeOperationProbe mirrors a single FacadeOperation entry surfaced by
// gong_discover_capabilities. Only the fields the evaluator inspects are
// required.
type FacadeOperationProbe struct {
	Operation  string `json:"operation"`
	FacadeTool string `json:"facade_tool"`
	RoutedTool string `json:"routed_tool"`
}

// QuestionAnswerProbe summarizes the result of one synthetic question.answer
// call. CallRefs is the list of call_ref values returned in the evidence
// pack.
type QuestionAnswerProbe struct {
	Question            string   `json:"question"`
	EvidencePackPresent bool     `json:"evidence_pack_present"`
	CallRefs            []string `json:"call_refs,omitempty"`
	ItemCount           int      `json:"item_count"`
	Notes               string   `json:"notes,omitempty"`
}

// CallDrilldownProbe summarizes evidence.call_drilldown for one call_ref
// produced by QuestionAnswer.
type CallDrilldownProbe struct {
	CallRef               string `json:"call_ref"`
	BoundedSnippetCount   int    `json:"bounded_snippet_count"`
	GongAISourcePathCount int    `json:"gong_ai_source_path_count"`
	SnippetsScopedToCall  bool   `json:"snippets_scoped_to_call"`
	Notes                 string `json:"notes,omitempty"`
}

// AccountQueryProbe summarizes one query.calls invocation for the opt-in gate.
type AccountQueryProbe struct {
	IsError  bool   `json:"is_error"`
	ErrorMsg string `json:"error,omitempty"`
	RowCount int    `json:"row_count,omitempty"`
}

// RedactionAuditProbe summarizes a serving-DB source-minus-redacted check.
// SourceMinusRedactedRows is the number of rows present in the source DB but
// absent from the redacted serving DB; values >0 indicate the redaction
// reduced the visible row set. Available=false means no audit was supplied.
type RedactionAuditProbe struct {
	Available               bool   `json:"available"`
	SourceMinusRedactedRows int64  `json:"source_minus_redacted_rows"`
	GeneratedAt             string `json:"generated_at,omitempty"`
	EvidencePath            string `json:"evidence_path,omitempty"`
}

// ReadOnlyPostureProbe summarizes the Postgres scoped-reader posture check.
// Provided=false means the operator did not supply DB URL inputs and the
// check is degraded (operator-supplied evidence still required for closeout).
type ReadOnlyPostureProbe struct {
	Provided           bool   `json:"provided"`
	WriteDenied        bool   `json:"write_denied"`
	WriteDenialDetail  string `json:"write_denial_detail,omitempty"`
	RawTableReadDenied bool   `json:"raw_table_read_denied"`
	RawTableReadDetail string `json:"raw_table_read_detail,omitempty"`
}

// Report is the machine-readable evaluator output.
type Report struct {
	Status      string        `json:"status"`
	GeneratedAt string        `json:"generated_at"`
	Summary     ReportSummary `json:"summary"`
	Checks      []Check       `json:"checks"`
}

// ReportSummary is the small operator header copied from runtime identity for
// quick scan in the JSON output.
type ReportSummary struct {
	DeploymentID                 string `json:"deployment_id,omitempty"`
	Version                      string `json:"version,omitempty"`
	Commit                       string `json:"commit,omitempty"`
	StartedAtUTC                 string `json:"started_at_utc,omitempty"`
	ToolPreset                   string `json:"tool_preset,omitempty"`
	VisibleToolCount             int    `json:"visible_tool_count"`
	FacadeRoutedToolCount        int    `json:"facade_routed_tool_count"`
	TranscriptEvidenceProvenance string `json:"transcript_evidence_provenance,omitempty"`
}

// Check is one named acceptance check.
type Check struct {
	ID      string         `json:"id"`
	Name    string         `json:"name"`
	Status  string         `json:"status"`
	Reason  string         `json:"reason,omitempty"`
	Details map[string]any `json:"details,omitempty"`
}

// Evaluate runs all required acceptance checks against the given probe bundle.
// It returns a Report plus an error only when the bundle is structurally
// invalid (e.g. unparseable status JSON). A failed check produces
// Report.Status=fail; degraded checks produce Report.Status=degraded; a
// fully-passing bundle produces Report.Status=pass.
func Evaluate(b ProbeBundle) (Report, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	rep := Report{GeneratedAt: now}

	statusView, err := parseStatus(b.Status)
	if err != nil {
		return Report{}, fmt.Errorf("parse status probe: %w", err)
	}
	rep.Summary = ReportSummary{
		DeploymentID:                 statusView.deploymentID,
		Version:                      statusView.version,
		Commit:                       statusView.commit,
		StartedAtUTC:                 statusView.startedAtUTC,
		ToolPreset:                   statusView.toolPreset,
		VisibleToolCount:             statusView.toolCount,
		FacadeRoutedToolCount:        statusView.facadeRoutedToolCount,
		TranscriptEvidenceProvenance: statusView.transcriptEvidenceProvenance,
	}

	rep.Checks = []Check{
		evaluateRuntimeIdentity(statusView),
		evaluateToolSurface(b, statusView),
		evaluateRoutedOperations(b),
		evaluateDataReadiness(statusView),
		evaluateGovernanceRedaction(b),
		evaluateEvidenceWorkflow(b),
		evaluateReadOnlyPosture(b),
	}

	rep.Status = aggregateStatus(rep.Checks)
	return rep, nil
}

// statusView is the small subset of gong_status fields the evaluator reads.
type statusView struct {
	deploymentID                 string
	version                      string
	commit                       string
	startedAtUTC                 string
	toolPreset                   string
	toolCount                    int
	facadeRoutedToolCount        int
	transcriptEvidenceProvenance string

	totalCalls             int64
	totalTranscripts       int64
	missingTranscripts     int64
	totalScorecards        int64
	totalAIHighlights      int64
	totalScorecardActivity int64

	transcriptCoverageReady   bool
	transcriptCoverageDetail  string
	attributionReadinessReady bool
	scorecardThemesReady      bool
	crmSegmentationReady      bool
	lifecycleSeparationReady  bool

	hasAnyAttributionSignal bool

	profileActive              bool
	profileStatus              string
	profileMethodologyUnmapped bool
	profileLossReasonMissing   bool
	profileLifecycleMissing    bool
	profileLifecycleBuckets    []any
	profileSuspectFindings     []string
}

func parseStatus(raw json.RawMessage) (statusView, error) {
	if len(raw) == 0 {
		return statusView{}, fmt.Errorf("status probe is empty")
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return statusView{}, err
	}
	view := statusView{}
	if mcp, ok := doc["mcp_server"].(map[string]any); ok {
		view.deploymentID = stringField(mcp, "deployment_id")
		view.version = stringField(mcp, "version")
		view.commit = stringField(mcp, "commit")
		view.startedAtUTC = stringField(mcp, "started_at_utc")
		view.toolPreset = stringField(mcp, "tool_preset")
		view.toolCount = intField(mcp, "tool_count")
		view.facadeRoutedToolCount = intField(mcp, "facade_routed_tool_count")
		view.transcriptEvidenceProvenance = stringField(mcp, "transcript_evidence_provenance")
	}
	view.totalCalls = int64Field(doc, "total_calls")
	view.totalTranscripts = int64Field(doc, "total_transcripts")
	view.missingTranscripts = int64Field(doc, "missing_transcripts")
	view.totalScorecards = int64Field(doc, "total_scorecards")
	view.totalAIHighlights = int64Field(doc, "total_ai_highlights")
	view.totalScorecardActivity = int64Field(doc, "total_scorecard_activity")

	if pr, ok := doc["public_readiness"].(map[string]any); ok {
		view.transcriptCoverageReady, view.transcriptCoverageDetail = readinessField(pr, "transcript_coverage")
		view.attributionReadinessReady, _ = readinessField(pr, "attribution_readiness")
		view.scorecardThemesReady, _ = readinessField(pr, "scorecard_themes")
		view.crmSegmentationReady, _ = readinessField(pr, "crm_segmentation")
		view.lifecycleSeparationReady, _ = readinessField(pr, "lifecycle_separation")
	}
	if cf, ok := doc["call_facts_attribution"].(map[string]any); ok {
		if v, ok := cf["has_any_attribution_signal"].(bool); ok {
			view.hasAnyAttributionSignal = v
		}
	}
	if pr, ok := doc["profile_readiness"].(map[string]any); ok {
		if v, ok := pr["active"].(bool); ok {
			view.profileActive = v
		}
		view.profileStatus = stringField(pr, "status")
		if buckets, ok := pr["lifecycle_bucket_count"].(float64); ok && buckets > 0 {
			view.profileLifecycleBuckets = make([]any, int(buckets))
		}
		if checklist, ok := pr["checklist"].(map[string]any); ok {
			if v, ok := checklist["methodology_unmapped"].(bool); ok {
				view.profileMethodologyUnmapped = v
			}
			if v, ok := checklist["loss_reason_mapping_missing"].(bool); ok {
				view.profileLossReasonMissing = v
			}
			if v, ok := checklist["lifecycle_rules_missing"].(bool); ok {
				view.profileLifecycleMissing = v
			}
			if findings, ok := checklist["suspect_findings"].([]any); ok {
				for _, item := range findings {
					if s, ok := item.(string); ok {
						view.profileSuspectFindings = append(view.profileSuspectFindings, s)
					}
				}
			}
		}
	}
	return view, nil
}

func stringField(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func intField(m map[string]any, key string) int {
	if v, ok := m[key].(float64); ok {
		return int(v)
	}
	return 0
}

func int64Field(m map[string]any, key string) int64 {
	if v, ok := m[key].(float64); ok {
		return int64(v)
	}
	return 0
}

func readinessField(parent map[string]any, key string) (bool, string) {
	v, ok := parent[key].(map[string]any)
	if !ok {
		return false, ""
	}
	ready, _ := v["ready"].(bool)
	detail := stringField(v, "detail")
	return ready, detail
}

func evaluateRuntimeIdentity(view statusView) Check {
	c := Check{ID: CheckRuntimeIdentity, Name: "Runtime identity"}
	missing := []string{}
	if view.deploymentID == "" {
		missing = append(missing, "deployment_id")
	}
	if view.version == "" {
		missing = append(missing, "version")
	}
	if view.startedAtUTC == "" {
		missing = append(missing, "started_at_utc")
	}
	if view.toolPreset == "" {
		missing = append(missing, "tool_preset")
	}
	if view.toolCount == 0 {
		missing = append(missing, "tool_count")
	}
	c.Details = map[string]any{
		"deployment_id":            view.deploymentID,
		"version":                  view.version,
		"commit":                   view.commit,
		"started_at_utc":           view.startedAtUTC,
		"tool_preset":              view.toolPreset,
		"visible_tool_count":       view.toolCount,
		"facade_routed_tool_count": view.facadeRoutedToolCount,
	}
	if len(missing) > 0 {
		c.Status = StatusFail
		c.Reason = fmt.Sprintf("runtime identity missing fields: %s", strings.Join(missing, ", "))
		return c
	}
	c.Status = StatusPass
	c.Reason = fmt.Sprintf("deployment_id=%s preset=%s version=%s tool_count=%d", view.deploymentID, view.toolPreset, view.version, view.toolCount)
	return c
}

func evaluateToolSurface(b ProbeBundle, view statusView) Check {
	c := Check{ID: CheckToolSurface, Name: "Visible tool surface"}
	got := normalizeStringSet(b.ToolsList)
	want := normalizeStringSet(expectedFacadeTools)
	missing := setDifference(want, got)
	extra := setDifference(got, want)
	c.Details = map[string]any{
		"expected_tool_count":        len(expectedFacadeTools),
		"observed_tool_count":        len(b.ToolsList),
		"observed_tools":             append([]string(nil), b.ToolsList...),
		"missing_required_tools":     missing,
		"unexpected_extra_tools":     extra,
		"status_reported_tool_count": view.toolCount,
	}
	if len(missing) > 0 {
		c.Status = StatusFail
		c.Reason = fmt.Sprintf("business-workbench is missing required facade tools: %s", strings.Join(missing, ", "))
		return c
	}
	if len(extra) > 0 {
		c.Status = StatusFail
		c.Reason = fmt.Sprintf("business-workbench exposes unexpected tools beyond the six-tool surface: %s", strings.Join(extra, ", "))
		return c
	}
	if view.toolCount != 0 && view.toolCount != len(expectedFacadeTools) {
		c.Status = StatusFail
		c.Reason = fmt.Sprintf("status.tool_count=%d disagrees with expected six-tool business-workbench surface", view.toolCount)
		return c
	}
	c.Status = StatusPass
	c.Reason = "all six business-workbench facade tools are visible"
	return c
}

func evaluateRoutedOperations(b ProbeBundle) Check {
	c := Check{ID: CheckRoutedOperations, Name: "Routed facade operations"}
	have := map[string]struct{}{}
	for _, op := range b.FacadeOperations {
		have[op.Operation] = struct{}{}
	}
	missing := []string{}
	for _, want := range requiredRoutedOperations {
		if _, ok := have[want]; !ok {
			missing = append(missing, want)
		}
	}
	c.Details = map[string]any{
		"required_operations":      append([]string(nil), requiredRoutedOperations...),
		"missing_operations":       missing,
		"observed_operation_count": len(b.FacadeOperations),
	}
	if len(missing) > 0 {
		c.Status = StatusFail
		c.Reason = fmt.Sprintf("required routed operations not advertised: %s", strings.Join(missing, ", "))
		return c
	}
	c.Status = StatusPass
	c.Reason = "all required routed operations are advertised by the facade"
	return c
}

func evaluateDataReadiness(view statusView) Check {
	c := Check{ID: CheckDataReadiness, Name: "Data readiness"}
	failures := []string{}
	degradations := []string{}

	// Transcript coverage: missing > 5% of total calls is degraded; status
	// flag false is degraded; missing == total calls (none synced) is fail.
	if view.totalCalls == 0 {
		degradations = append(degradations, "no calls synced yet")
	} else if !view.transcriptCoverageReady {
		degradations = append(degradations, "transcript coverage not ready: "+view.transcriptCoverageDetail)
	}
	if view.totalCalls > 0 && view.totalTranscripts == 0 {
		failures = append(failures, "no transcripts synced")
	}

	// Profile readiness checklist.
	if !view.profileActive {
		degradations = append(degradations, "no active customer profile")
	}
	if view.profileLifecycleMissing {
		degradations = append(degradations, "profile lifecycle rules missing")
	}
	if view.profileMethodologyUnmapped {
		degradations = append(degradations, "profile methodology unmapped")
	}
	if view.profileLossReasonMissing {
		degradations = append(degradations, "profile loss-reason field not mapped")
	}
	if len(view.profileSuspectFindings) > 0 {
		degradations = append(degradations, fmt.Sprintf("profile checklist suspect findings: %d", len(view.profileSuspectFindings)))
	}

	// Call-facts attribution signal.
	if !view.hasAnyAttributionSignal {
		degradations = append(degradations, "call_facts attribution signals absent")
	}

	// AI highlight inventory.
	if view.totalAIHighlights == 0 {
		degradations = append(degradations, "AI highlight inventory is empty (Gong AI accelerators not yet captured)")
	}

	// Scorecard availability.
	if view.totalScorecards == 0 {
		degradations = append(degradations, "no scorecards synced")
	}

	// Public readiness composite hints.
	if !view.scorecardThemesReady {
		degradations = append(degradations, "scorecard_themes readiness flag not ready")
	}
	if !view.crmSegmentationReady {
		degradations = append(degradations, "crm_segmentation readiness flag not ready")
	}
	if !view.attributionReadinessReady {
		degradations = append(degradations, "attribution_readiness flag not ready")
	}
	if !view.lifecycleSeparationReady {
		degradations = append(degradations, "lifecycle_separation flag not ready")
	}

	c.Details = map[string]any{
		"total_calls":                           view.totalCalls,
		"total_transcripts":                     view.totalTranscripts,
		"missing_transcripts":                   view.missingTranscripts,
		"total_scorecards":                      view.totalScorecards,
		"total_scorecard_activity":              view.totalScorecardActivity,
		"total_ai_highlights":                   view.totalAIHighlights,
		"profile_active":                        view.profileActive,
		"profile_status":                        view.profileStatus,
		"profile_methodology_unmapped":          view.profileMethodologyUnmapped,
		"profile_loss_reason_mapping_missing":   view.profileLossReasonMissing,
		"profile_lifecycle_rules_missing":       view.profileLifecycleMissing,
		"call_facts_has_any_attribution_signal": view.hasAnyAttributionSignal,
		"degradations":                          degradations,
		"failures":                              failures,
	}
	if len(failures) > 0 {
		c.Status = StatusFail
		c.Reason = strings.Join(failures, "; ")
		return c
	}
	if len(degradations) > 0 {
		c.Status = StatusDegraded
		c.Reason = strings.Join(degradations, "; ")
		return c
	}
	c.Status = StatusPass
	c.Reason = "transcript coverage, profile readiness, attribution, AI highlights, and scorecard inventory all ready"
	return c
}

func evaluateGovernanceRedaction(b ProbeBundle) Check {
	c := Check{ID: CheckGovernanceRedaction, Name: "Governance and redaction"}
	failures := []string{}
	degradations := []string{}
	details := map[string]any{}

	switch {
	case b.RawCallIDsHidden == nil:
		degradations = append(degradations, "raw-call-id visibility was not probed")
	case !*b.RawCallIDsHidden:
		failures = append(failures, "raw call IDs are visible in business-workbench tool output (hide_raw_call_ids policy not enforced)")
	}
	details["raw_call_ids_hidden"] = b.RawCallIDsHidden

	if b.AccountQueryWithoutOptIn == nil {
		degradations = append(degradations, "account_query without include_account_names was not probed")
	} else if !b.AccountQueryWithoutOptIn.IsError {
		failures = append(failures, "account_query without include_account_names=true did not fail closed; restricted-account probing is reachable")
	} else if !strings.Contains(strings.ToLower(b.AccountQueryWithoutOptIn.ErrorMsg), "include_account_names") {
		degradations = append(degradations, "account_query no-opt-in error message does not mention include_account_names")
	}
	details["account_query_without_opt_in_error"] = accountQueryDetail(b.AccountQueryWithoutOptIn)

	if b.AccountQueryWithOptIn == nil {
		degradations = append(degradations, "account_query with include_account_names was not probed")
	} else if b.AccountQueryWithOptIn.IsError {
		degradations = append(degradations, "account_query with include_account_names=true returned an error: "+b.AccountQueryWithOptIn.ErrorMsg)
	}
	details["account_query_with_opt_in"] = accountQueryDetail(b.AccountQueryWithOptIn)

	if b.RedactionAudit == nil || !b.RedactionAudit.Available {
		degradations = append(degradations, "no source-minus-redacted audit evidence supplied")
	} else {
		details["redaction_audit_generated_at"] = b.RedactionAudit.GeneratedAt
		details["redaction_audit_source_minus_redacted_rows"] = b.RedactionAudit.SourceMinusRedactedRows
	}

	if len(failures) > 0 {
		c.Status = StatusFail
		c.Reason = strings.Join(failures, "; ")
		c.Details = details
		return c
	}
	if len(degradations) > 0 {
		c.Status = StatusDegraded
		c.Reason = strings.Join(degradations, "; ")
		c.Details = details
		return c
	}
	c.Status = StatusPass
	c.Reason = "raw call IDs hidden, account_query opt-in enforced, redaction audit evidence present"
	c.Details = details
	return c
}

func accountQueryDetail(p *AccountQueryProbe) map[string]any {
	if p == nil {
		return map[string]any{"probed": false}
	}
	return map[string]any{
		"probed":    true,
		"is_error":  p.IsError,
		"error":     p.ErrorMsg,
		"row_count": p.RowCount,
	}
}

func evaluateEvidenceWorkflow(b ProbeBundle) Check {
	c := Check{ID: CheckEvidenceWorkflow, Name: "Evidence workflow"}
	failures := []string{}
	details := map[string]any{}

	if b.QuestionAnswer == nil {
		failures = append(failures, "no question.answer probe supplied")
	} else {
		details["question_answer_question"] = b.QuestionAnswer.Question
		details["question_answer_evidence_pack_present"] = b.QuestionAnswer.EvidencePackPresent
		details["question_answer_item_count"] = b.QuestionAnswer.ItemCount
		details["question_answer_call_refs"] = append([]string(nil), b.QuestionAnswer.CallRefs...)
		if !b.QuestionAnswer.EvidencePackPresent {
			failures = append(failures, "question.answer did not return an evidence pack")
		}
		if len(b.QuestionAnswer.CallRefs) == 0 {
			failures = append(failures, "question.answer returned no call_ref values for drilldown")
		}
		if b.QuestionAnswer.ItemCount == 0 {
			failures = append(failures, "question.answer evidence pack is empty")
		}
	}

	if b.CallDrilldown == nil {
		failures = append(failures, "no evidence.call_drilldown probe supplied")
	} else {
		details["call_drilldown_call_ref"] = b.CallDrilldown.CallRef
		details["call_drilldown_bounded_snippet_count"] = b.CallDrilldown.BoundedSnippetCount
		details["call_drilldown_gong_ai_source_path_count"] = b.CallDrilldown.GongAISourcePathCount
		details["call_drilldown_snippets_scoped_to_call"] = b.CallDrilldown.SnippetsScopedToCall

		if b.QuestionAnswer != nil && len(b.QuestionAnswer.CallRefs) > 0 {
			matched := false
			for _, ref := range b.QuestionAnswer.CallRefs {
				if ref == b.CallDrilldown.CallRef {
					matched = true
					break
				}
			}
			if !matched {
				failures = append(failures, fmt.Sprintf("call_drilldown.call_ref %q does not match any call_ref returned by question.answer", b.CallDrilldown.CallRef))
			}
		}
		if b.CallDrilldown.BoundedSnippetCount == 0 {
			failures = append(failures, "call_drilldown returned no bounded transcript snippets")
		}
		if b.CallDrilldown.GongAISourcePathCount == 0 {
			failures = append(failures, "call_drilldown returned no Gong AI source paths")
		}
		if !b.CallDrilldown.SnippetsScopedToCall {
			failures = append(failures, "call_drilldown snippets are not scoped to the requested call_ref")
		}
	}

	if len(failures) > 0 {
		c.Status = StatusFail
		c.Reason = strings.Join(failures, "; ")
		c.Details = details
		return c
	}
	c.Status = StatusPass
	c.Reason = "question.answer evidence pack flows into evidence.call_drilldown for the same call_ref"
	c.Details = details
	return c
}

func evaluateReadOnlyPosture(b ProbeBundle) Check {
	c := Check{ID: CheckReadOnlyPosture, Name: "Read-only posture"}
	if b.ReadOnlyPosture == nil || !b.ReadOnlyPosture.Provided {
		c.Status = StatusDegraded
		c.Reason = "DB URL inputs were not supplied; scoped-reader read-only posture not verified"
		c.Details = map[string]any{"provided": false}
		return c
	}
	failures := []string{}
	details := map[string]any{
		"provided":              true,
		"write_denied":          b.ReadOnlyPosture.WriteDenied,
		"write_detail":          b.ReadOnlyPosture.WriteDenialDetail,
		"raw_table_read_denied": b.ReadOnlyPosture.RawTableReadDenied,
		"raw_table_read_detail": b.ReadOnlyPosture.RawTableReadDetail,
	}
	if !b.ReadOnlyPosture.WriteDenied {
		failures = append(failures, "scoped MCP role is able to write: "+b.ReadOnlyPosture.WriteDenialDetail)
	}
	if !b.ReadOnlyPosture.RawTableReadDenied {
		failures = append(failures, "scoped MCP role can read raw source-only tables: "+b.ReadOnlyPosture.RawTableReadDetail)
	}
	if len(failures) > 0 {
		c.Status = StatusFail
		c.Reason = strings.Join(failures, "; ")
		c.Details = details
		return c
	}
	c.Status = StatusPass
	c.Reason = "scoped MCP role cannot write and cannot read raw source-only tables"
	c.Details = details
	return c
}

func aggregateStatus(checks []Check) string {
	hasFail := false
	hasDegraded := false
	for _, c := range checks {
		switch c.Status {
		case StatusFail:
			hasFail = true
		case StatusDegraded:
			hasDegraded = true
		}
	}
	switch {
	case hasFail:
		return StatusFail
	case hasDegraded:
		return StatusDegraded
	default:
		return StatusPass
	}
}

func normalizeStringSet(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]struct{}{}
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func setDifference(a, b []string) []string {
	bm := map[string]struct{}{}
	for _, v := range b {
		bm[v] = struct{}{}
	}
	var out []string
	for _, v := range a {
		if _, ok := bm[v]; !ok {
			out = append(out, v)
		}
	}
	return out
}

// RenderOperatorMarkdown formats a concise human-readable operator summary
// from the report. The markdown intentionally avoids emitting per-check
// detail blobs that may contain noisy values; it surfaces overall status, the
// runtime identity header, and one line per check.
func RenderOperatorMarkdown(rep Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# GA Customer Acceptance Smoke\n\n")
	fmt.Fprintf(&b, "- overall_status: **%s**\n", rep.Status)
	fmt.Fprintf(&b, "- generated_at: %s\n", rep.GeneratedAt)
	fmt.Fprintf(&b, "- deployment_id: %s\n", rep.Summary.DeploymentID)
	fmt.Fprintf(&b, "- tool_preset: %s\n", rep.Summary.ToolPreset)
	fmt.Fprintf(&b, "- version: %s\n", rep.Summary.Version)
	if rep.Summary.Commit != "" {
		fmt.Fprintf(&b, "- commit: %s\n", rep.Summary.Commit)
	}
	fmt.Fprintf(&b, "- started_at_utc: %s\n", rep.Summary.StartedAtUTC)
	fmt.Fprintf(&b, "- visible_tool_count: %d\n", rep.Summary.VisibleToolCount)
	fmt.Fprintf(&b, "- facade_routed_tool_count: %d\n", rep.Summary.FacadeRoutedToolCount)
	if rep.Summary.TranscriptEvidenceProvenance != "" {
		fmt.Fprintf(&b, "- transcript_evidence_provenance: %s\n", rep.Summary.TranscriptEvidenceProvenance)
	}
	b.WriteString("\n## Checks\n\n")
	b.WriteString("| Check | Status | Reason |\n")
	b.WriteString("| --- | --- | --- |\n")
	for _, c := range rep.Checks {
		reason := c.Reason
		if reason == "" {
			reason = "-"
		}
		fmt.Fprintf(&b, "| %s | %s | %s |\n", c.ID, c.Status, reason)
	}
	b.WriteString("\n## Status legend\n\n")
	b.WriteString("- **pass** — the deployment satisfies this contract.\n")
	b.WriteString("- **degraded** — the deployment is usable but has gaps the operator should record before connecting business users (missing optional probes, partial readiness, missing inventory).\n")
	b.WriteString("- **fail** — the deployment is not ready for business-user testing; rerun after the failing check is remediated.\n")
	return b.String()
}
