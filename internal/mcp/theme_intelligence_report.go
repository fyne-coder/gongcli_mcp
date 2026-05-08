package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
)

// Theme intelligence report bounded payload constants. These keep the
// composed report deterministic and prevent unbounded fan-out across the
// underlying primitives.
const (
	defaultThemeIntelReportQuotesPerTheme = 5
	maxThemeIntelReportQuotesPerTheme     = 5
	maxThemeIntelReportThemes             = 5
	maxThemeIntelReportCallDrilldowns     = 5
	maxThemeIntelReportAICondensedRows    = 12
	maxThemeIntelReportSalesHooks         = 8
	maxThemeIntelReportOutreachInputs     = 8
	maxThemeIntelReportDrilldownExcerpts  = 3
	maxThemeIntelReportDimensionRows      = 10
)

type themeIntelReportArgs struct {
	Filter              callFilter `json:"filter"`
	FromDate            string     `json:"from_date"`
	ToDate              string     `json:"to_date"`
	Quarter             string     `json:"quarter"`
	ThemeQuery          string     `json:"theme_query"`
	OutputIntent        string     `json:"output_intent"`
	GroupBy             []string   `json:"group_by"`
	TopQuotesPerTheme   int        `json:"top_quotes_per_theme"`
	FieldProfile        string     `json:"field_profile"`
	SpeakerRole         string     `json:"speaker_role"`
	IncludeCallTitles   bool       `json:"include_call_titles"`
	IncludeAccountNames bool       `json:"include_account_names"`
	IncludeSpeakerRefs  bool       `json:"include_speaker_refs"`
	IncludeRawIDs       bool       `json:"include_raw_ids"`
	Limit               int        `json:"limit"`
}

type themeIntelReportQuoteRow struct {
	CallRef               string `json:"call_ref,omitempty"`
	CallID                string `json:"call_id,omitempty"`
	CallTitle             string `json:"call_title,omitempty"`
	AccountName           string `json:"account_name,omitempty"`
	StartedAt             string `json:"started_at,omitempty"`
	CallDate              string `json:"call_date,omitempty"`
	LifecycleBucket       string `json:"lifecycle_bucket,omitempty"`
	OpportunityStage      string `json:"opportunity_stage,omitempty"`
	AccountIndustry       string `json:"account_industry,omitempty"`
	SpeakerRef            string `json:"speaker_ref,omitempty"`
	SegmentIndex          int    `json:"segment_index,omitempty"`
	StartMS               int64  `json:"start_ms,omitempty"`
	EndMS                 int64  `json:"end_ms,omitempty"`
	Snippet               string `json:"snippet"`
	ContextExcerpt        string `json:"context_excerpt,omitempty"`
	PersonTitleStatus     string `json:"person_title_status"`
	AttributionSource     string `json:"attribution_source"`
	AttributionConfidence string `json:"attribution_confidence"`
	SpeakerRole           string `json:"speaker_role"`
	SpeakerRoleStatus     string `json:"speaker_role_status"`
	// DrilldownTerm is the exact theme term/phrase that callers should pass
	// back into evidence.call_drilldown's theme_query so the drill-down hits
	// the same matched segment. Phase C deliberately defers fuzzy/synonym
	// matching; the explicit workflow uses these literal terms instead.
	DrilldownTerm string `json:"drilldown_term"`

	callIDForDrilldown string
}

type themeIntelReportAIRow struct {
	CallRef        string `json:"call_ref,omitempty"`
	CallID         string `json:"call_id,omitempty"`
	HighlightIndex int    `json:"highlight_index"`
	HighlightType  string `json:"highlight_type"`
	HighlightText  string `json:"highlight_text"`
	SourcePath     string `json:"source_path,omitempty"`
	UpdatedAt      string `json:"updated_at,omitempty"`
}

type aiBusinessBriefThemeSummary struct {
	Candidates      []businessAnalysisTheme
	EvidenceByTheme map[string][]themeIntelReportAIRow
	SourceCallCount int
	SourceRowCount  int
}

type themeIntelReportTranscriptRow struct {
	CallRef               string `json:"call_ref,omitempty"`
	CallID                string `json:"call_id,omitempty"`
	SegmentIndex          int    `json:"segment_index"`
	SpeakerRef            string `json:"speaker_ref,omitempty"`
	StartMS               int64  `json:"start_ms"`
	EndMS                 int64  `json:"end_ms"`
	Snippet               string `json:"snippet,omitempty"`
	PersonTitleStatus     string `json:"person_title_status"`
	AttributionSource     string `json:"attribution_source"`
	AttributionConfidence string `json:"attribution_confidence"`
	SpeakerRole           string `json:"speaker_role"`
	SpeakerRoleStatus     string `json:"speaker_role_status"`
	IsInternalSpeaker     bool   `json:"is_internal_speaker"`
}

type themeIntelReportDrilldown struct {
	CallRef                    string                          `json:"call_ref,omitempty"`
	CallID                     string                          `json:"call_id,omitempty"`
	CallTitle                  string                          `json:"call_title,omitempty"`
	AccountName                string                          `json:"account_name,omitempty"`
	StartedAt                  string                          `json:"started_at,omitempty"`
	OpportunityStage           string                          `json:"opportunity_stage,omitempty"`
	AccountIndustry            string                          `json:"account_industry,omitempty"`
	AICondensedEvidence        []themeIntelReportAIRow         `json:"ai_condensed_evidence"`
	VerbatimTranscriptExcerpts []themeIntelReportTranscriptRow `json:"verbatim_transcript_excerpts"`
}

func (s *Server) executeThemeIntelReport(ctx context.Context, raw json.RawMessage) (toolCallResult, error) {
	var args themeIntelReportArgs
	if err := decodeArgs(raw, &args); err != nil {
		return toolCallResult{}, err
	}
	if s.governanceActive() {
		return toolCallResult{}, governanceFilteredAggregateError(OpThemeIntelReport)
	}

	// Normalize incoming aliases onto the existing call filter so the rest
	// of the operation reuses the existing primitives without any new
	// parsing surface.
	if args.FromDate != "" && strings.TrimSpace(args.Filter.FromDate) == "" {
		args.Filter.FromDate = args.FromDate
	}
	if args.ToDate != "" && strings.TrimSpace(args.Filter.ToDate) == "" {
		args.Filter.ToDate = args.ToDate
	}
	if args.Quarter != "" && strings.TrimSpace(args.Filter.Quarter) == "" {
		args.Filter.Quarter = args.Quarter
	}
	if s.restrictedAccountQuery(args.Filter.AccountQuery) {
		return toolCallResult{}, restrictedAccountQueryError()
	}
	profiled, err := applyFieldProfile(args.FieldProfile, fieldProfileApplication{
		IncludeRawIDs:       args.IncludeRawIDs,
		IncludeCallTitles:   args.IncludeCallTitles,
		IncludeAccountNames: args.IncludeAccountNames,
		IncludeSpeakerRefs:  args.IncludeSpeakerRefs,
	})
	if err != nil {
		return toolCallResult{}, err
	}
	args.FieldProfile = profiled.Profile
	args.IncludeRawIDs = profiled.IncludeRawIDs
	args.IncludeCallTitles = profiled.IncludeCallTitles
	args.IncludeAccountNames = profiled.IncludeAccountNames
	args.IncludeSpeakerRefs = profiled.IncludeSpeakerRefs
	if strings.TrimSpace(args.Filter.AccountQuery) != "" && (!args.IncludeAccountNames || s.policySwitches.HideAccountNames) {
		return toolCallResult{}, fmt.Errorf("account_query requires include_account_names=true and hide_account_names=false because it can probe customer names")
	}
	speakerRoleFilter, err := normalizeSpeakerRoleFilter(firstNonBlank(args.SpeakerRole, speakerRoleExternalOrUnknown))
	if err != nil {
		return toolCallResult{}, err
	}

	normalized, err := normalizeCallFilter(args.Filter)
	if err != nil {
		return toolCallResult{}, err
	}
	policy := defaultBusinessEvidencePolicy()
	normalized = policy.applyFilter(normalized)
	if !businessAnalysisFilterIsSelective(normalized) {
		return toolCallResult{}, fmt.Errorf("%s requires a selective filter such as date range, quarter, title_query, query, industry, opportunity_stage, crm_object_id, participant_title_query, or lifecycle_bucket", OpThemeIntelReport)
	}

	limit := s.limitPolicy.BusinessAnalysisLimit(args.Limit)
	if normalized.Limit > 0 {
		limit = s.limitPolicy.BusinessAnalysisLimit(normalized.Limit)
		normalized.Limit = limit
	}

	topQuotes := args.TopQuotesPerTheme
	if topQuotes <= 0 {
		topQuotes = defaultThemeIntelReportQuotesPerTheme
	}
	if topQuotes > maxThemeIntelReportQuotesPerTheme {
		topQuotes = maxThemeIntelReportQuotesPerTheme
	}

	themeQuery := strings.TrimSpace(args.ThemeQuery)
	if themeQuery == "" {
		themeQuery = strings.TrimSpace(normalized.Query)
	}

	// Policy switches always win over caller include flags. Compute the
	// effective include flags up-front so every downstream payload mirrors
	// the same posture.
	includeRaw := args.IncludeRawIDs && !s.policySwitches.HideRawCallIDs
	includeTitles := args.IncludeCallTitles && !s.policySwitches.HideCallTitles
	includeAccounts := args.IncludeAccountNames && !s.policySwitches.HideAccountNames
	includeSpeakerRefs := args.IncludeSpeakerRefs

	if s.store == nil {
		payload := map[string]any{
			"operation":                OpThemeIntelReport,
			"status":                   "store_unavailable",
			"searched_scope":           normalized,
			"theme_query":              themeQuery,
			"coverage_summary":         map[string]any{},
			"theme_candidates":         []any{},
			"themes_by_quarter":        map[string]any{"rows": []any{}},
			"themes_by_industry":       map[string]any{"rows": []any{}},
			"themes_by_persona":        map[string]any{"rows": []any{}},
			"top_quotes_by_theme":      map[string]any{},
			"call_drilldowns":          []any{},
			"sales_hooks_inputs":       []any{},
			"outreach_sequence_inputs": []any{},
			"pipeline_outcome_summary": map[string]any{},
			"loss_reason_summary":      map[string]any{"status": "store_unavailable", "rows": []any{}},
			"limitations":              themeIntelReportLimitations(),
			"warnings":                 []string{"store_unavailable"},
			"suggested_followups":      themeIntelReportFollowups(args.OutputIntent),
			"report_truncated":         false,
		}
		return newToolResult(payload)
	}

	cohort, err := s.store.SearchBusinessAnalysisCalls(ctx, sqlite.BusinessAnalysisCallSearchParams{
		Filter: sqliteBusinessAnalysisFilter(normalized),
		Limit:  limit,
	})
	if err != nil {
		return toolCallResult{}, err
	}

	baArgs := businessAnalysisArgs{
		Filter:              normalized,
		Query:               themeQuery,
		ThemeQuery:          themeQuery,
		Limit:               limit,
		IncludeCallIDs:      includeRaw,
		IncludeCallTitles:   includeTitles,
		IncludeAccountNames: includeAccounts,
		FieldProfile:        args.FieldProfile,
		SpeakerRole:         speakerRoleFilter,
	}

	// Evidence: when theme_query is supplied we treat it as the primary
	// theme and run the targeted evidence path. With no theme_query we fall
	// back to broad seedless discovery to surface candidate terms only —
	// the operation never invents a final theme from zero evidence.
	var (
		evidenceItems    []businessAnalysisItem
		evidenceQuotes   []businessAnalysisQuote
		broadDiscovery   bool
		themeCandidates  []businessAnalysisTheme
		primaryThemeName string
	)
	var aiBriefSummary aiBusinessBriefThemeSummary
	if themeQuery == "" {
		broadDiscovery = true
		normalized = applyDefaultBroadThemeQualityFilters(normalized)
		cohort, err = s.store.SearchBusinessAnalysisCalls(ctx, sqlite.BusinessAnalysisCallSearchParams{
			Filter: sqliteBusinessAnalysisFilter(normalized),
			Limit:  limit,
		})
		if err != nil {
			return toolCallResult{}, err
		}
		cohort.Rows = filterThemeIntelCohortRows(cohort.Rows, normalized)
		aiBriefSummary, err = s.aiBusinessBriefThemeSummary(ctx, cohort.Rows, includeRaw, maxThemeIntelReportThemes)
		if err != nil {
			return toolCallResult{}, err
		}
		themeCandidates = aiBriefSummary.Candidates
	} else {
		evidenceItems, evidenceQuotes, err = s.businessAnalysisEvidence(ctx, normalized, themeQuery, limit, baArgs)
		if err != nil {
			return toolCallResult{}, err
		}
		aiBriefSummary, err = s.aiBusinessBriefThemeSummaryForSeeds(ctx, cohort.Rows, includeRaw, maxThemeIntelReportThemes, []string{themeQuery})
		if err != nil {
			return toolCallResult{}, err
		}
		primaryThemeName = themeQuery
		themeCandidates = discoverBusinessAnalysisThemes(evidenceItems, maxThemeIntelReportThemes)
	}

	// Dimension rollups. compare_themes_over_time / summarize_themes_by_industry
	// / summarize_themes_by_persona each route through the existing
	// SummarizeBusinessAnalysisDimension helper so SQLite/Postgres parity is
	// preserved without any new SQL. won_lost / opportunity_stage / loss_reason
	// follow the same path so the report stays cache-derived.
	quarterRows, err := s.businessAnalysisDimension(ctx, normalized, "quarter", themeQuery, maxThemeIntelReportDimensionRows)
	if err != nil {
		return toolCallResult{}, err
	}
	industryRows, err := s.businessAnalysisDimension(ctx, normalized, "industry", themeQuery, maxThemeIntelReportDimensionRows)
	if err != nil {
		return toolCallResult{}, err
	}
	personaRows, err := s.businessAnalysisDimension(ctx, normalized, "persona", themeQuery, maxThemeIntelReportDimensionRows)
	if err != nil {
		return toolCallResult{}, err
	}
	wonLostRows, err := s.businessAnalysisDimension(ctx, normalized, "won_lost", themeQuery, maxThemeIntelReportDimensionRows)
	if err != nil {
		return toolCallResult{}, err
	}
	stageRows, err := s.businessAnalysisDimension(ctx, normalized, "opportunity_stage", themeQuery, maxThemeIntelReportDimensionRows)
	if err != nil {
		return toolCallResult{}, err
	}
	lossReasonRows, err := s.businessAnalysisDimension(ctx, normalized, "loss_reason", themeQuery, maxThemeIntelReportDimensionRows)
	if err != nil {
		return toolCallResult{}, err
	}

	// Build top-quote map. With a primary theme everything is pinned to
	// that single bucket. With broad discovery we partition the same
	// bounded sample across the discovered candidate terms by snippet
	// match so the caller sees per-candidate evidence.
	quoteRowsByTheme := buildThemeIntelQuotePartitions(themeCandidates, evidenceItems, evidenceQuotes, primaryThemeName, topQuotes, includeRaw, includeTitles, includeAccounts, includeSpeakerRefs)

	// Pick representative calls for drilldown — at most maxThemeIntelReportCallDrilldowns.
	drillCalls := selectThemeIntelQuoteBackedDrilldownCalls(quoteRowsByTheme, maxThemeIntelReportCallDrilldowns)
	if len(drillCalls) == 0 {
		drillCalls = selectThemeIntelDrilldownCalls(cohort.Rows, evidenceItems, maxThemeIntelReportCallDrilldowns)
	}
	drilldowns := make([]themeIntelReportDrilldown, 0, len(drillCalls))
	for _, callID := range drillCalls {
		drill, err := s.themeIntelReportBuildDrilldown(ctx, callID, themeQuery, includeRaw, includeTitles, includeAccounts, includeSpeakerRefs)
		if err != nil {
			return toolCallResult{}, err
		}
		drilldowns = append(drilldowns, drill)
	}

	// Sales hook / outreach inputs are deterministic projections of the
	// quote evidence; they are *inputs* for downstream synthesis, not
	// finished copy. The caps here keep the payload bounded.
	salesHooks := buildThemeIntelSalesHookInputs(quoteRowsByTheme, themeCandidates, primaryThemeName, maxThemeIntelReportSalesHooks)
	outreachInputs := buildThemeIntelOutreachInputs(quoteRowsByTheme, themeCandidates, primaryThemeName, maxThemeIntelReportOutreachInputs)

	pipelineSummary := map[string]any{
		"won_lost":          themeIntelReportDimensionRowsAsPayload(wonLostRows),
		"opportunity_stage": themeIntelReportDimensionRowsAsPayload(stageRows),
	}
	lossSummary := buildThemeIntelLossReasonSummary(lossReasonRows)

	warnings := businessAnalysisWarnings(OpThemeIntelReport, normalized)
	limitations := themeIntelReportLimitations()
	if cohort.Summary.ParticipantTitleCallCount == 0 {
		warnings = append(warnings, "participant_title_missing_or_unmapped")
	}
	if cohort.Summary.AccountIndustryCount == 0 {
		warnings = append(warnings, "account_industry_missing_or_unmapped")
	}
	if cohort.Summary.OpportunityStageCount == 0 {
		warnings = append(warnings, "opportunity_stage_missing_or_unmapped")
	}
	if broadDiscovery {
		warnings = append(warnings,
			"broad_discovery_ai_brief_first: no theme_query was provided, so candidate themes were ranked from Gong AI brief/keyPoint/highlight rows after excluding noisy lifecycle/voicemail calls. Rerun theme_intelligence_report with a chosen theme_query for buyer transcript quotes.")
		if len(themeCandidates) == 0 {
			warnings = append(warnings, "broad_discovery_no_business_like_candidates: no Gong AI brief/keyPoint/highlight candidate themes survived the business evidence policy; rerun with a suggested theme_query")
		}
	}
	if loss, ok := lossSummary["status"].(string); ok && loss == "loss_reason_not_populated" {
		limitations = append(limitations, "loss_reason_not_populated")
	}
	if s.policySwitches.HideLossReasons {
		warnings = append(warnings, "hide_loss_reasons_enforced: bucket coverage emitted; raw loss-reason text is not exposed by this tool")
	}

	status := "ready"
	if cohort.Summary.CallCount == 0 {
		status = "empty_cohort"
		warnings = append(warnings, "empty_cohort: no calls matched the normalized filter")
	} else if broadDiscovery {
		status = "ai_brief_candidate_themes"
		if len(themeCandidates) == 0 {
			status = "needs_theme_seed"
		}
	}

	// Truncation accounting. The dimension paths apply their own caps; we
	// surface a single deterministic flag so callers know to ask for a
	// narrower filter when content was clipped.
	truncated := args.TopQuotesPerTheme > maxThemeIntelReportQuotesPerTheme
	for _, list := range quoteRowsByTheme {
		if len(list) >= topQuotes && len(evidenceQuotes) > topQuotes {
			truncated = true
		}
	}
	if len(themeCandidates) >= maxThemeIntelReportThemes {
		truncated = true
	}
	if len(drillCalls) >= maxThemeIntelReportCallDrilldowns && len(cohort.Rows) > maxThemeIntelReportCallDrilldowns {
		truncated = true
	}
	if len(quarterRows) >= maxThemeIntelReportDimensionRows ||
		len(industryRows) >= maxThemeIntelReportDimensionRows ||
		len(personaRows) >= maxThemeIntelReportDimensionRows {
		truncated = true
	}

	payload := map[string]any{
		"operation":                           OpThemeIntelReport,
		"status":                              status,
		"searched_scope":                      normalized,
		"field_profile":                       args.FieldProfile,
		"speaker_role_filter":                 speakerRoleFilter,
		"theme_query":                         themeQuery,
		"primary_theme":                       primaryThemeName,
		"coverage_summary":                    businessAnalysisCoverageFromSummary(cohort.Summary),
		"theme_candidates":                    themeCandidates,
		"ai_business_brief_evidence_by_theme": themeIntelReportAIMapAsPayload(aiBriefSummary.EvidenceByTheme),
		"ai_business_brief_source": map[string]any{
			"source":          "gong_ai_brief_keypoints_highlights",
			"source_calls":    aiBriefSummary.SourceCallCount,
			"source_rows":     aiBriefSummary.SourceRowCount,
			"filters_applied": []string{"exclude_lifecycle_buckets", "exclude_likely_voicemail"},
		},
		"themes_by_quarter":         themeIntelReportDimensionPayload("quarter", quarterRows),
		"themes_by_industry":        themeIntelReportDimensionPayload("industry", industryRows),
		"themes_by_persona":         themeIntelReportDimensionPayload("persona", personaRows),
		"top_quotes_by_theme":       themeIntelReportQuoteMapAsPayload(quoteRowsByTheme),
		"drilldown_workflow_inputs": buildThemeIntelDrilldownWorkflow(quoteRowsByTheme, maxThemeIntelReportCallDrilldowns*maxThemeIntelReportQuotesPerTheme),
		"call_drilldowns":           drilldowns,
		"sales_hooks_inputs":        salesHooks,
		"outreach_sequence_inputs":  outreachInputs,
		"pipeline_outcome_summary":  pipelineSummary,
		"loss_reason_summary":       lossSummary,
		"limitations":               limitations,
		"warnings":                  warnings,
		"suggested_followups":       themeIntelReportFollowups(args.OutputIntent),
		"report_truncated":          truncated,
		"limits": map[string]any{
			"top_quotes_per_theme":    topQuotes,
			"max_quotes_per_theme":    maxThemeIntelReportQuotesPerTheme,
			"max_themes":              maxThemeIntelReportThemes,
			"max_call_drilldowns":     maxThemeIntelReportCallDrilldowns,
			"max_ai_condensed_rows":   maxThemeIntelReportAICondensedRows,
			"max_drilldown_excerpts":  maxThemeIntelReportDrilldownExcerpts,
			"max_dimension_rows":      maxThemeIntelReportDimensionRows,
			"max_sales_hook_inputs":   maxThemeIntelReportSalesHooks,
			"max_outreach_seq_inputs": maxThemeIntelReportOutreachInputs,
		},
	}
	evidenceType := evidenceTypeTranscriptQuote
	if broadDiscovery {
		evidenceType = evidenceTypeGongAICondensedCandidate
	}
	allQuoteRows := make([]themeIntelReportQuoteRow, 0)
	for _, rows := range quoteRowsByTheme {
		allQuoteRows = append(allQuoteRows, rows...)
	}
	addBusinessEvidenceMetadata(payload, policy, evidenceType, &cohort.Summary, args.FieldProfile, len(aiBriefSummary.EvidenceByTheme) > 0, speakerAttributionSummaryFromThemeQuotes(allQuoteRows))
	return newToolResult(payload)
}

func (s *Server) themeIntelReportBuildDrilldown(ctx context.Context, callID, themeQuery string, includeRaw, includeTitles, includeAccounts, includeSpeakerRefs bool) (themeIntelReportDrilldown, error) {
	out := themeIntelReportDrilldown{
		CallRef:                    sqlite.StableCallRef(callID),
		AICondensedEvidence:        []themeIntelReportAIRow{},
		VerbatimTranscriptExcerpts: []themeIntelReportTranscriptRow{},
	}
	if includeRaw {
		out.CallID = callID
	}
	detail, err := s.store.GetCallDetail(ctx, callID)
	if err == nil && detail != nil {
		if !s.blocklistMatchesCallDetail(detail) {
			out.StartedAt = detail.StartedAt
			if includeTitles {
				out.CallTitle = detail.Title
			}
			for _, obj := range detail.CRMObjects {
				switch strings.TrimSpace(obj.ObjectType) {
				case "Account":
					if includeAccounts && out.AccountName == "" {
						out.AccountName = strings.TrimSpace(obj.ObjectName)
					}
				case "Opportunity":
					// Opportunity stage on call detail can be derived from
					// call_facts; the dimension rollups already surface
					// stage coverage, so a per-call value is best-effort
					// and stays empty when not provided.
				}
			}
		}
	}

	highlights, err := s.store.ListAIHighlights(ctx, sqlite.AIHighlightListParams{CallIDs: []string{callID}, Limit: maxThemeIntelReportAICondensedRows})
	if err != nil {
		return out, err
	}
	for _, row := range highlights {
		ai := themeIntelReportAIRow{
			HighlightIndex: row.HighlightIndex,
			HighlightType:  row.HighlightType,
			HighlightText:  row.HighlightText,
			SourcePath:     row.SourcePath,
			UpdatedAt:      row.UpdatedAt,
			CallRef:        sqlite.StableCallRef(callID),
		}
		if includeRaw {
			ai.CallID = callID
		}
		out.AICondensedEvidence = append(out.AICondensedEvidence, ai)
	}

	if strings.TrimSpace(themeQuery) != "" {
		transcripts, err := s.store.CallDrilldownEvidence(ctx, sqlite.CallDrilldownEvidenceParams{
			CallID: callID,
			Query:  themeQuery,
			Limit:  maxThemeIntelReportDrilldownExcerpts,
		})
		if err != nil {
			return out, err
		}
		for i, row := range transcripts {
			if i >= maxThemeIntelReportDrilldownExcerpts {
				break
			}
			tr := themeIntelReportTranscriptRow{
				CallRef:               sqlite.StableCallRef(callID),
				SegmentIndex:          row.SegmentIndex,
				StartMS:               row.StartMS,
				EndMS:                 row.EndMS,
				Snippet:               row.Snippet,
				PersonTitleStatus:     row.PersonTitleStatus,
				AttributionSource:     row.AttributionSource,
				AttributionConfidence: row.AttributionConfidence,
				SpeakerRole:           row.SpeakerRole,
				SpeakerRoleStatus:     row.SpeakerRoleStatus,
			}
			if tr.PersonTitleStatus == "" {
				tr.PersonTitleStatus = sqlite.AttributionStatusSpeakerUnmatched
			}
			if tr.AttributionSource == "" {
				tr.AttributionSource = sqlite.AttributionSourceUnmatched
			}
			if tr.AttributionConfidence == "" {
				tr.AttributionConfidence = sqlite.AttributionConfidenceUnmatched
			}
			if tr.SpeakerRole == "" {
				tr.SpeakerRole = sqlite.SpeakerRoleUnknown
			}
			if tr.SpeakerRoleStatus == "" {
				tr.SpeakerRoleStatus = sqlite.SpeakerRoleStatusAffiliationMissing
			}
			tr.IsInternalSpeaker = tr.SpeakerRole == sqlite.SpeakerRoleInternal
			if includeRaw {
				tr.CallID = callID
			}
			if includeSpeakerRefs && strings.TrimSpace(row.SpeakerID) != "" {
				tr.SpeakerRef = stableEvidenceRef("speaker", callID+"\x00"+row.SpeakerID)
			}
			out.VerbatimTranscriptExcerpts = append(out.VerbatimTranscriptExcerpts, tr)
		}
	}
	return out, nil
}

func selectThemeIntelDrilldownCalls(cohortRows []sqlite.BusinessAnalysisCallRow, evidence []businessAnalysisItem, max int) []string {
	seen := make(map[string]struct{}, max)
	out := make([]string, 0, max)
	// Prefer calls that produced evidence — those carry the strongest
	// theme signal. The cohort rows fill in any remaining slots so the
	// drilldown sample is still bounded when no evidence exists.
	for _, item := range evidence {
		if len(out) >= max {
			break
		}
		// Evidence rows may carry the redacted call_ref under CallID when
		// include_call_ids is off; in that case fall back to the
		// CallRef-derived id-less path. Drilldown lookups need a real
		// call_id, so we only use evidence rows that exposed one.
		if item.CallID == "" {
			continue
		}
		if _, ok := seen[item.CallID]; ok {
			continue
		}
		seen[item.CallID] = struct{}{}
		out = append(out, item.CallID)
	}
	for _, row := range cohortRows {
		if len(out) >= max {
			break
		}
		if row.CallID == "" {
			continue
		}
		if _, ok := seen[row.CallID]; ok {
			continue
		}
		seen[row.CallID] = struct{}{}
		out = append(out, row.CallID)
	}
	return out
}

func selectThemeIntelQuoteBackedDrilldownCalls(quotes map[string][]themeIntelReportQuoteRow, max int) []string {
	if max <= 0 {
		return nil
	}
	keys := make([]string, 0, len(quotes))
	for theme := range quotes {
		keys = append(keys, theme)
	}
	sort.Strings(keys)
	seen := make(map[string]struct{}, max)
	out := make([]string, 0, max)
	for _, theme := range keys {
		for _, row := range quotes[theme] {
			if len(out) >= max {
				return out
			}
			callID := strings.TrimSpace(row.callIDForDrilldown)
			if callID == "" {
				continue
			}
			if _, ok := seen[callID]; ok {
				continue
			}
			seen[callID] = struct{}{}
			out = append(out, callID)
		}
	}
	return out
}

func filterThemeIntelCohortRows(rows []sqlite.BusinessAnalysisCallRow, filter callFilter) []sqlite.BusinessAnalysisCallRow {
	if len(rows) == 0 {
		return rows
	}
	out := make([]sqlite.BusinessAnalysisCallRow, 0, len(rows))
	for _, row := range rows {
		if businessAnalysisFilterExcludesRow(filter, row.LifecycleBucket, row.LikelyVoicemailOrIVR) {
			continue
		}
		out = append(out, row)
	}
	return out
}

func (s *Server) aiBusinessBriefThemeSummary(ctx context.Context, rows []sqlite.BusinessAnalysisCallRow, includeRaw bool, limit int) (aiBusinessBriefThemeSummary, error) {
	return s.aiBusinessBriefThemeSummaryForSeeds(ctx, rows, includeRaw, limit, questionAnswerSuggestedSeedTopics())
}

func (s *Server) aiBusinessBriefThemeSummaryForSeeds(ctx context.Context, rows []sqlite.BusinessAnalysisCallRow, includeRaw bool, limit int, seedTopics []string) (aiBusinessBriefThemeSummary, error) {
	if limit <= 0 {
		limit = maxThemeIntelReportThemes
	}
	seedTopics = normalizeBusinessSignalQueries(seedTopics, maxBusinessSignalTopics)
	if len(seedTopics) == 0 {
		return aiBusinessBriefThemeSummary{EvidenceByTheme: map[string][]themeIntelReportAIRow{}}, nil
	}
	callIDs := make([]string, 0, len(rows))
	for _, row := range rows {
		if strings.TrimSpace(row.CallID) == "" || s.isSuppressedCall(row.CallID) {
			continue
		}
		callIDs = append(callIDs, row.CallID)
		if len(callIDs) >= 25 {
			break
		}
	}
	if len(callIDs) == 0 {
		return aiBusinessBriefThemeSummary{EvidenceByTheme: map[string][]themeIntelReportAIRow{}}, nil
	}
	highlights, err := s.store.ListAIHighlights(ctx, sqlite.AIHighlightListParams{CallIDs: callIDs, Limit: maxThemeIntelReportAICondensedRows * len(callIDs)})
	if err != nil {
		return aiBusinessBriefThemeSummary{}, err
	}
	callSupport := make(map[string]map[string]struct{}, len(seedTopics))
	evidence := make(map[string][]themeIntelReportAIRow, len(seedTopics))
	for _, row := range highlights {
		text := strings.ToLower(row.HighlightText)
		if strings.TrimSpace(text) == "" || businessBriefLooksLikeVoicemail(text) {
			continue
		}
		for _, seed := range seedTopics {
			if !businessBriefMatchesTopic(text, seed) {
				continue
			}
			if callSupport[seed] == nil {
				callSupport[seed] = map[string]struct{}{}
			}
			callSupport[seed][row.CallID] = struct{}{}
			ai := themeIntelReportAIRow{
				CallRef:        sqlite.StableCallRef(row.CallID),
				HighlightIndex: row.HighlightIndex,
				HighlightType:  row.HighlightType,
				HighlightText:  row.HighlightText,
				SourcePath:     row.SourcePath,
				UpdatedAt:      row.UpdatedAt,
			}
			if includeRaw {
				ai.CallID = row.CallID
			}
			if len(evidence[seed]) < maxThemeIntelReportQuotesPerTheme {
				evidence[seed] = append(evidence[seed], ai)
			}
		}
	}
	type scored struct {
		theme string
		count int
	}
	scoredThemes := make([]scored, 0, len(callSupport))
	for theme, calls := range callSupport {
		if len(calls) == 0 {
			continue
		}
		scoredThemes = append(scoredThemes, scored{theme: theme, count: len(calls)})
	}
	sort.Slice(scoredThemes, func(i, j int) bool {
		if scoredThemes[i].count == scoredThemes[j].count {
			return scoredThemes[i].theme < scoredThemes[j].theme
		}
		return scoredThemes[i].count > scoredThemes[j].count
	})
	if len(scoredThemes) > limit {
		scoredThemes = scoredThemes[:limit]
	}
	candidates := make([]businessAnalysisTheme, 0, len(scoredThemes))
	filteredEvidence := make(map[string][]themeIntelReportAIRow, len(scoredThemes))
	for _, item := range scoredThemes {
		candidates = append(candidates, businessAnalysisTheme{
			Theme:        item.theme,
			SupportCount: item.count,
			EvidenceType: "gong_ai_business_brief_signal",
		})
		filteredEvidence[item.theme] = evidence[item.theme]
	}
	return aiBusinessBriefThemeSummary{
		Candidates:      candidates,
		EvidenceByTheme: filteredEvidence,
		SourceCallCount: len(callIDs),
		SourceRowCount:  len(highlights),
	}, nil
}

func businessBriefMatchesTopic(text string, seed string) bool {
	if businessBriefMatchesSeed(text, seed) {
		return true
	}
	for _, alias := range businessSignalTopicAliases("", seed) {
		if businessBriefMatchesSeed(text, alias) {
			return true
		}
	}
	return false
}

func businessBriefMatchesSeed(text string, seed string) bool {
	for _, term := range strings.Fields(strings.ToLower(seed)) {
		term = strings.Trim(term, " -_/")
		if len(term) <= 2 {
			continue
		}
		if !strings.Contains(text, term) {
			return false
		}
	}
	return true
}

func businessBriefLooksLikeVoicemail(text string) bool {
	for _, phrase := range []string{"please leave", "leave a message", "after the tone", "voicemail", "not available", "unable to take", "customer care team", "your call is important"} {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
}

func themeIntelReportAIMapAsPayload(rows map[string][]themeIntelReportAIRow) map[string]any {
	out := make(map[string]any, len(rows))
	for theme, list := range rows {
		if len(list) == 0 {
			out[theme] = []any{}
			continue
		}
		out[theme] = list
	}
	return out
}

// buildThemeIntelQuotePartitions returns top-N quotes per theme. For a
// single primary theme the entire bounded evidence set is pinned to that
// bucket. With broad discovery, the same evidence is partitioned across
// candidate terms by snippet match so each candidate carries its own
// evidence rows.
func buildThemeIntelQuotePartitions(candidates []businessAnalysisTheme, items []businessAnalysisItem, quotes []businessAnalysisQuote, primary string, topN int, includeRaw, includeTitles, includeAccounts, includeSpeakerRefs bool) map[string][]themeIntelReportQuoteRow {
	out := make(map[string][]themeIntelReportQuoteRow)
	if topN <= 0 {
		return out
	}
	convert := func(quote businessAnalysisQuote, item businessAnalysisItem, themeName string) themeIntelReportQuoteRow {
		speakerRole := strings.TrimSpace(quote.SpeakerRole)
		speakerStatus := strings.TrimSpace(quote.SpeakerRoleStatus)
		if speakerRole == "" {
			speakerRole = sqlite.SpeakerRoleUnknown
		}
		if speakerStatus == "" {
			speakerStatus = sqlite.SpeakerRoleStatusAffiliationMissing
		}
		row := themeIntelReportQuoteRow{
			CallRef:               quote.CallRef,
			SegmentIndex:          quote.SegmentIndex,
			StartMS:               quote.StartMS,
			EndMS:                 quote.EndMS,
			Snippet:               quote.Excerpt,
			ContextExcerpt:        quote.ContextExcerpt,
			LifecycleBucket:       quote.LifecycleBucket,
			OpportunityStage:      quote.OpportunityStage,
			AccountIndustry:       quote.AccountIndustry,
			StartedAt:             quote.StartedAt,
			CallDate:              quote.CallDate,
			PersonTitleStatus:     quote.PersonTitleStatus,
			AttributionSource:     defaultAttribution(quote.PersonTitleStatus),
			AttributionConfidence: defaultAttributionConfidence(quote.PersonTitleStatus),
			SpeakerRole:           speakerRole,
			SpeakerRoleStatus:     speakerStatus,
			DrilldownTerm:         themeName,
			callIDForDrilldown:    strings.TrimSpace(firstNonBlank(quote.callIDForDrilldown, quote.CallID, item.CallID)),
		}
		if includeRaw {
			row.CallID = quote.CallID
		}
		if includeTitles {
			row.CallTitle = quote.CallTitle
		}
		if includeAccounts {
			row.AccountName = quote.AccountName
		}
		if includeSpeakerRefs && item.CallRef != "" {
			row.SpeakerRef = stableEvidenceRef("speaker", item.CallRef+":"+fmtSegment(item.SegmentIndex))
		}
		return row
	}
	if strings.TrimSpace(primary) != "" {
		bucket := make([]themeIntelReportQuoteRow, 0, topN)
		for i, quote := range quotes {
			if len(bucket) >= topN {
				break
			}
			var item businessAnalysisItem
			if i < len(items) {
				item = items[i]
			}
			bucket = append(bucket, convert(quote, item, primary))
		}
		out[primary] = bucket
		return out
	}
	if len(candidates) == 0 {
		return out
	}
	for _, c := range candidates {
		theme := c.Theme
		if strings.TrimSpace(theme) == "" {
			continue
		}
		needle := strings.ToLower(theme)
		bucket := make([]themeIntelReportQuoteRow, 0, topN)
		for i, quote := range quotes {
			if len(bucket) >= topN {
				break
			}
			text := strings.ToLower(quote.Excerpt + " " + quote.ContextExcerpt)
			if !strings.Contains(text, needle) {
				continue
			}
			var item businessAnalysisItem
			if i < len(items) {
				item = items[i]
			}
			bucket = append(bucket, convert(quote, item, theme))
		}
		out[theme] = bucket
	}
	return out
}

func defaultAttribution(status string) string {
	if strings.TrimSpace(status) == "" {
		return sqlite.AttributionSourceUnmatched
	}
	if status == sqlite.AttributionStatusSpeakerUnmatched {
		return sqlite.AttributionSourceUnmatched
	}
	return sqlite.AttributionSourceCallParties
}

func defaultAttributionConfidence(status string) string {
	switch status {
	case "":
		return sqlite.AttributionConfidenceUnmatched
	case sqlite.AttributionStatusSpeakerUnmatched:
		return sqlite.AttributionConfidenceUnmatched
	case sqlite.AttributionStatusSpeakerAmbiguous:
		return sqlite.AttributionConfidenceAmbiguous
	default:
		return sqlite.AttributionConfidenceExactSpeakerID
	}
}

func fmtSegment(idx int) string {
	return fmt.Sprintf("%d", idx)
}

func buildThemeIntelSalesHookInputs(quotes map[string][]themeIntelReportQuoteRow, candidates []businessAnalysisTheme, primary string, max int) []map[string]any {
	out := make([]map[string]any, 0, max)
	add := func(theme string, support int) {
		rows := quotes[theme]
		if len(rows) == 0 {
			return
		}
		entry := map[string]any{
			"theme":               theme,
			"support_count":       support,
			"evidence_quote_refs": themeIntelReportQuoteRefs(rows),
			"evidence_type":       "deterministic_keyword_signal",
			"hook_inputs": []string{
				"problem_statement_seed: rephrase the highest-support snippet as the customer's problem statement",
				"persona_seed: pair the snippet with the matched person_title_status to ground persona language",
				"outcome_seed: cite the won/lost or opportunity_stage bucket from pipeline_outcome_summary",
			},
		}
		out = append(out, entry)
	}
	if strings.TrimSpace(primary) != "" {
		support := 0
		for _, c := range candidates {
			if c.Theme == primary {
				support = c.SupportCount
				break
			}
		}
		add(primary, support)
		return out
	}
	for _, c := range candidates {
		if len(out) >= max {
			break
		}
		add(c.Theme, c.SupportCount)
	}
	return out
}

func buildThemeIntelOutreachInputs(quotes map[string][]themeIntelReportQuoteRow, candidates []businessAnalysisTheme, primary string, max int) []map[string]any {
	out := make([]map[string]any, 0, max)
	add := func(theme string, support int) {
		rows := quotes[theme]
		if len(rows) == 0 {
			return
		}
		entry := map[string]any{
			"theme":               theme,
			"support_count":       support,
			"evidence_quote_refs": themeIntelReportQuoteRefs(rows),
			"sequence_inputs": []string{
				"opening_seed: lead with a procurement/finance pain phrasing drawn from the snippet",
				"value_seed: pair the snippet with the matched coverage_summary participant title rate",
				"call_to_action_seed: reference the closed_won bucket from pipeline_outcome_summary when present",
			},
		}
		out = append(out, entry)
	}
	if strings.TrimSpace(primary) != "" {
		support := 0
		for _, c := range candidates {
			if c.Theme == primary {
				support = c.SupportCount
				break
			}
		}
		add(primary, support)
		return out
	}
	for _, c := range candidates {
		if len(out) >= max {
			break
		}
		add(c.Theme, c.SupportCount)
	}
	return out
}

func themeIntelReportQuoteRefs(rows []themeIntelReportQuoteRow) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		entry := map[string]any{
			"call_ref":               row.CallRef,
			"segment_index":          row.SegmentIndex,
			"person_title_status":    row.PersonTitleStatus,
			"attribution_source":     row.AttributionSource,
			"attribution_confidence": row.AttributionConfidence,
		}
		if row.CallID != "" {
			entry["call_id"] = row.CallID
		}
		out = append(out, entry)
	}
	return out
}

// themeIntelReportDimensionPayload splits a dimension rollup into a
// public payload that hides `<blank>` rows from the main `rows` array
// and surfaces blank-coverage counts/percentages in a `coverage`
// sub-object instead. Marketing/RevOps prompts can then count real
// industry/persona/quarter buckets in `rows` without misreading
// `<blank>` as a normal segment.
func themeIntelReportDimensionPayload(dimension string, rows []sqlite.BusinessAnalysisDimensionRow) map[string]any {
	publicRows := make([]sqlite.BusinessAnalysisDimensionRow, 0, len(rows))
	var blankRow *sqlite.BusinessAnalysisDimensionRow
	var totalCalls int64
	for i := range rows {
		row := rows[i]
		totalCalls += row.CallCount
		if v := strings.TrimSpace(row.Value); v == "" || v == "<blank>" {
			blank := row
			blankRow = &blank
			continue
		}
		publicRows = append(publicRows, row)
	}
	coverage := map[string]any{
		"total_call_count":          totalCalls,
		"populated_bucket_count":    len(publicRows),
		"blank_call_count":          int64(0),
		"blank_call_rate":           0.0,
		"latest_blank_call_at":      "",
		"blank_treated_as_unmapped": true,
	}
	if blankRow != nil {
		coverage["blank_call_count"] = blankRow.CallCount
		coverage["latest_blank_call_at"] = blankRow.LatestCallAt
		if totalCalls > 0 {
			coverage["blank_call_rate"] = float64(blankRow.CallCount) / float64(totalCalls)
		}
	}
	return map[string]any{
		"dimension": dimension,
		"rows":      themeIntelReportDimensionRowsAsPayload(publicRows),
		"coverage":  coverage,
	}
}

func themeIntelReportDimensionRowsAsPayload(rows []sqlite.BusinessAnalysisDimensionRow) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, map[string]any{
			"dimension":                row.Dimension,
			"bucket":                   row.Value,
			"call_count":               row.CallCount,
			"transcript_count":         row.TranscriptCount,
			"missing_transcript_count": row.MissingTranscriptCount,
			"transcript_coverage_rate": row.TranscriptCoverageRate,
			"latest_call_at":           row.LatestCallAt,
		})
	}
	return out
}

// buildThemeIntelDrilldownWorkflow returns a deterministic, machine-readable
// workflow plan: each entry pairs a stable call_ref with the exact theme
// term it matched, plus a step instruction telling the calling model to
// pass the same term back into evidence.call_drilldown's theme_query. This
// is the explicit substitute for fuzzy/synonym matching across the
// report-to-drilldown boundary.
func buildThemeIntelDrilldownWorkflow(quotes map[string][]themeIntelReportQuoteRow, max int) []map[string]any {
	if max <= 0 {
		return []map[string]any{}
	}
	keys := make([]string, 0, len(quotes))
	for k := range quotes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	seen := make(map[string]struct{})
	out := make([]map[string]any, 0, max)
	for _, theme := range keys {
		for _, row := range quotes[theme] {
			if len(out) >= max {
				return out
			}
			if strings.TrimSpace(row.CallRef) == "" || strings.TrimSpace(row.DrilldownTerm) == "" {
				continue
			}
			key := row.CallRef + "\x00" + row.DrilldownTerm
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, map[string]any{
				"call_ref":        row.CallRef,
				"theme_query":     row.DrilldownTerm,
				"theme":           theme,
				"step":            "evidence.call_drilldown { call_ref, theme_query: <drilldown_term> }",
				"evidence_source": "theme_intelligence_report.top_quotes_by_theme",
				"segment_index":   row.SegmentIndex,
			})
		}
	}
	return out
}

func themeIntelReportQuoteMapAsPayload(quotes map[string][]themeIntelReportQuoteRow) map[string]any {
	out := make(map[string]any, len(quotes))
	keys := make([]string, 0, len(quotes))
	for k := range quotes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		rows := quotes[k]
		conv := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			payload := map[string]any{
				"call_ref":               row.CallRef,
				"segment_index":          row.SegmentIndex,
				"start_ms":               row.StartMS,
				"end_ms":                 row.EndMS,
				"snippet":                row.Snippet,
				"person_title_status":    row.PersonTitleStatus,
				"attribution_source":     row.AttributionSource,
				"attribution_confidence": row.AttributionConfidence,
			}
			if row.CallID != "" {
				payload["call_id"] = row.CallID
			}
			if row.CallTitle != "" {
				payload["call_title"] = row.CallTitle
			}
			if row.AccountName != "" {
				payload["account_name"] = row.AccountName
			}
			if row.SpeakerRef != "" {
				payload["speaker_ref"] = row.SpeakerRef
			}
			if row.LifecycleBucket != "" {
				payload["lifecycle_bucket"] = row.LifecycleBucket
			}
			if row.OpportunityStage != "" {
				payload["opportunity_stage"] = row.OpportunityStage
			}
			if row.AccountIndustry != "" {
				payload["account_industry"] = row.AccountIndustry
			}
			// context_excerpt is intentionally not surfaced: it joins ±1
			// neighboring transcript segments and would expand the report
			// beyond bounded snippet evidence. The matched snippet alone
			// keeps the payload aligned with the "no raw transcripts" rule.
			if row.StartedAt != "" {
				payload["started_at"] = row.StartedAt
			}
			if row.CallDate != "" {
				payload["call_date"] = row.CallDate
			}
			// drilldown_term is the exact theme phrase callers should pass
			// back into evidence.call_drilldown's theme_query so the drill
			// hits the same matched segment without fuzzy/synonym handling.
			payload["drilldown_term"] = row.DrilldownTerm
			conv = append(conv, payload)
		}
		out[k] = conv
	}
	return out
}

func buildThemeIntelLossReasonSummary(rows []sqlite.BusinessAnalysisDimensionRow) map[string]any {
	if !businessAnalysisHasLossReasonBuckets(rows) {
		return map[string]any{
			"status": "loss_reason_not_populated",
			"rows":   []any{},
			"note":   "no normalized loss-reason buckets present; CRM loss-reason fields are blank or unmapped for this cohort",
		}
	}
	out := map[string]any{
		"status": "ready",
		"rows":   themeIntelReportDimensionRowsAsPayload(rows),
		"note":   "values are deterministic normalized buckets (price, no_decision, competitor, timing, feature_gap, budget, relationship, unknown_other); raw CRM loss-reason text is not exposed",
	}
	return out
}

func themeIntelReportLimitations() []string {
	return []string{
		"read_only_cache_only",
		"bounded_results_and_redacted_identifiers_by_default",
		"cache_derived_signals_not_llm_conclusions",
		"top_quotes_per_theme_hard_capped",
		"call_drilldown_excerpts_are_bounded_not_full_transcripts",
		"ai_condensed_evidence_is_gong_generated_accelerator_text_not_verbatim_buyer_quotes",
		"speaker_attribution_is_exact_gong_party_only_no_crm_contact_or_lead_matching_in_this_phase",
		"sales_hooks_and_outreach_inputs_are_synthesis_inputs_not_finished_copy",
		"raw_crm_loss_reason_text_and_account_enumeration_are_not_supported_through_this_report",
	}
}

func themeIntelReportFollowups(intent string) []string {
	switch strings.ToLower(strings.TrimSpace(intent)) {
	case "themes_only":
		return []string{
			"Run discover_themes_in_cohort with the highest-support candidate term for stronger evidence.",
			"Compare the top theme across persona/industry by calling summarize_themes_by_persona and summarize_themes_by_industry.",
		}
	case "outreach_only":
		return []string{
			"Pull a quote pack with build_quote_pack for the chosen theme before drafting outreach copy.",
			"Confirm pipeline outcome alignment by checking pipeline_outcome_summary's closed_won bucket.",
		}
	default:
		return []string{
			"Narrow the date range or industry to deepen evidence for the strongest theme.",
			"Walk through drilldown_workflow_inputs and pass each {call_ref, theme_query} pair into evidence.call_drilldown verbatim — the same exact term from this report keeps the drill hitting the matched segment without fuzzy matching.",
			"Use evidence.quote_pack.build for a sales-ready quote bundle once the strongest theme is selected.",
		}
	}
}
