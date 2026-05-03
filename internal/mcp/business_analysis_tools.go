package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
)

const (
	defaultBusinessAnalysisLimit      = 25
	maxBusinessAnalysisLimit          = 100
	maxBusinessAnalysisFTSQueryLength = 160
)

var businessAnalysisToolDescriptions = map[string]string{
	"build_call_cohort":                        "Build a reproducible bounded call cohort from an allowlisted filter and return a deterministic cohort_id.",
	"inspect_call_cohort":                      "Inspect the normalized filter, cohort identity, cache coverage, and limitations for a call cohort.",
	"list_call_cohorts":                        "List in-process cohort state when available; cohort filters remain the durable restart-safe handoff.",
	"compare_call_cohorts":                     "Compare two normalized cohort filters and return deterministic cohort identities plus limitation metadata.",
	"search_calls_by_filters":                  "Search cached calls through the shared bounded filter contract without exposing raw SQL or unbounded identifiers.",
	"summarize_calls_by_filters":               "Summarize cached call coverage for a bounded filter and report which dimensions need store-level helpers.",
	"search_transcripts_by_filters":            "Search transcript evidence with bounded excerpts using safe filter fields supported by the current cache helpers.",
	"discover_themes_in_cohort":                "Return deterministic cache-derived theme signals for a cohort; no hidden LLM conclusions are generated.",
	"summarize_themes_by_dimension":            "Prepare bounded theme-by-dimension inputs for an analyst or client LLM to synthesize outside gongmcp.",
	"compare_themes_over_time":                 "Prepare bounded theme-over-time inputs and explicit cache coverage caveats.",
	"compare_themes_by_segment":                "Prepare bounded theme-by-segment inputs and explicit cache coverage caveats.",
	"extract_theme_quotes":                     "Extract bounded quote evidence for a theme query with names and identifiers redacted by default.",
	"search_quotes_in_cohort":                  "Search bounded quote evidence inside a cohort filter with safe default redaction.",
	"rank_quotes_for_sales_use":                "Return bounded quote candidates and scoring-input placeholders for sales-use ranking outside gongmcp.",
	"build_quote_pack":                         "Build a bounded quote pack from cached snippets with explicit attribution and redaction limits.",
	"compare_theme_outcomes":                   "Compare theme/outcome coverage and report when CRM outcome fields are unavailable.",
	"summarize_pipeline_progression_by_theme":  "Summarize pipeline progression coverage for a theme query without inventing attribution.",
	"summarize_loss_reasons_by_theme":          "Summarize loss-reason coverage for a theme query without inventing missing CRM fields.",
	"compare_won_lost_theme_patterns":          "Compare won/lost theme-pattern coverage and report outcome-field limitations.",
	"summarize_themes_by_persona":              "Summarize theme/persona coverage and report missing participant-title coverage.",
	"summarize_themes_by_industry":             "Summarize theme/industry coverage and report missing account-industry coverage.",
	"rank_personas_by_insight_quality":         "Rank persona insight-quality inputs without inferring unavailable participant titles.",
	"diagnose_attribution_coverage":            "Diagnose cache coverage needed for attribution, outcomes, persona, industry, and transcript evidence.",
	"generate_sales_hooks_from_themes":         "Return structured sales-hook inputs from cached theme evidence for downstream synthesis outside gongmcp.",
	"generate_outreach_sequence_inputs":        "Return structured outreach-sequence inputs from cached theme evidence for downstream synthesis outside gongmcp.",
	"recommend_target_personas_and_industries": "Return structured targeting inputs and coverage caveats without inferring unavailable persona or industry data.",
	"build_theme_brief":                        "Build a bounded theme brief with optional cached quote evidence and explicit limitations.",
	"score_cohort_evidence_quality":            "Score evidence-readiness inputs for a cohort based on cache coverage and filter specificity.",
	"explain_analysis_limitations":             "Explain current cache limitations for a requested analysis.",
	"suggest_filter_refinements":               "Suggest safer, narrower filter refinements based on the supplied bounded filter fields.",
}

var businessAnalysisToolNameList = []string{
	"build_call_cohort",
	"inspect_call_cohort",
	"list_call_cohorts",
	"compare_call_cohorts",
	"search_calls_by_filters",
	"summarize_calls_by_filters",
	"search_transcripts_by_filters",
	"discover_themes_in_cohort",
	"summarize_themes_by_dimension",
	"compare_themes_over_time",
	"compare_themes_by_segment",
	"extract_theme_quotes",
	"search_quotes_in_cohort",
	"rank_quotes_for_sales_use",
	"build_quote_pack",
	"compare_theme_outcomes",
	"summarize_pipeline_progression_by_theme",
	"summarize_loss_reasons_by_theme",
	"compare_won_lost_theme_patterns",
	"summarize_themes_by_persona",
	"summarize_themes_by_industry",
	"rank_personas_by_insight_quality",
	"diagnose_attribution_coverage",
	"generate_sales_hooks_from_themes",
	"generate_outreach_sequence_inputs",
	"recommend_target_personas_and_industries",
	"build_theme_brief",
	"score_cohort_evidence_quality",
	"explain_analysis_limitations",
	"suggest_filter_refinements",
}

func BusinessAnalysisToolNames() []string {
	out := make([]string, len(businessAnalysisToolNameList))
	copy(out, businessAnalysisToolNameList)
	return out
}

type callFilter struct {
	TitleQuery            string `json:"title_query"`
	Query                 string `json:"query"`
	FromDate              string `json:"from_date"`
	ToDate                string `json:"to_date"`
	Quarter               string `json:"quarter"`
	LifecycleBucket       string `json:"lifecycle_bucket"`
	Scope                 string `json:"scope"`
	System                string `json:"system"`
	Direction             string `json:"direction"`
	TranscriptStatus      string `json:"transcript_status"`
	Industry              string `json:"industry"`
	AccountQuery          string `json:"account_query"`
	OpportunityStage      string `json:"opportunity_stage"`
	CRMObjectType         string `json:"crm_object_type"`
	CRMObjectID           string `json:"crm_object_id"`
	ParticipantTitleQuery string `json:"participant_title_query"`
	Limit                 int    `json:"limit"`
}

type businessAnalysisArgs struct {
	Filter                  callFilter `json:"filter"`
	FilterA                 callFilter `json:"filter_a"`
	FilterB                 callFilter `json:"filter_b"`
	CohortID                string     `json:"cohort_id"`
	CohortIDA               string     `json:"cohort_id_a"`
	CohortIDB               string     `json:"cohort_id_b"`
	Query                   string     `json:"query"`
	ThemeQuery              string     `json:"theme_query"`
	Theme                   string     `json:"theme"`
	Dimension               string     `json:"dimension"`
	Segment                 string     `json:"segment"`
	SegmentBy               string     `json:"segment_by"`
	TimeGrain               string     `json:"time_grain"`
	Limit                   int        `json:"limit"`
	IncludeCallIDs          bool       `json:"include_call_ids"`
	IncludeCallTitles       bool       `json:"include_call_titles"`
	IncludeAccountNames     bool       `json:"include_account_names"`
	IncludeOpportunityNames bool       `json:"include_opportunity_names"`
}

type businessAnalysisResponse struct {
	Tool              string                                `json:"tool"`
	Status            string                                `json:"status"`
	CohortID          string                                `json:"cohort_id,omitempty"`
	CohortIDA         string                                `json:"cohort_id_a,omitempty"`
	CohortIDB         string                                `json:"cohort_id_b,omitempty"`
	NormalizedFilter  callFilter                            `json:"normalized_filter,omitempty"`
	NormalizedFilterA callFilter                            `json:"normalized_filter_a,omitempty"`
	NormalizedFilterB callFilter                            `json:"normalized_filter_b,omitempty"`
	Query             string                                `json:"query,omitempty"`
	ThemeQuery        string                                `json:"theme_query,omitempty"`
	Dimension         string                                `json:"dimension,omitempty"`
	Segment           string                                `json:"segment,omitempty"`
	TimeGrain         string                                `json:"time_grain,omitempty"`
	Limit             int                                   `json:"limit"`
	Count             int                                   `json:"count"`
	Coverage          map[string]any                        `json:"coverage_summary,omitempty"`
	Summary           *sqlite.BusinessAnalysisCohortSummary `json:"summary,omitempty"`
	Summaries         []sqlite.BusinessAnalysisDimensionRow `json:"summaries,omitempty"`
	Themes            []businessAnalysisTheme               `json:"themes,omitempty"`
	QualityScore      int                                   `json:"quality_score,omitempty"`
	SynthesisInputs   map[string]any                        `json:"synthesis_inputs,omitempty"`
	Warnings          []string                              `json:"warnings"`
	Limitations       []string                              `json:"limitations"`
	Results           []businessAnalysisItem                `json:"results,omitempty"`
	Quotes            []businessAnalysisQuote               `json:"quotes,omitempty"`
	Refinements       []string                              `json:"suggested_refinements,omitempty"`
}

type businessAnalysisItem struct {
	CallRef           string `json:"call_ref,omitempty"`
	CallID            string `json:"call_id,omitempty"`
	CallTitle         string `json:"call_title,omitempty"`
	AccountName       string `json:"account_name,omitempty"`
	OpportunityName   string `json:"opportunity_name,omitempty"`
	StartedAt         string `json:"started_at,omitempty"`
	CallDate          string `json:"call_date,omitempty"`
	CallMonth         string `json:"call_month,omitempty"`
	LifecycleBucket   string `json:"lifecycle_bucket,omitempty"`
	AccountIndustry   string `json:"account_industry,omitempty"`
	OpportunityStage  string `json:"opportunity_stage,omitempty"`
	OpportunityType   string `json:"opportunity_type,omitempty"`
	ParticipantStatus string `json:"participant_status,omitempty"`
	PersonTitleStatus string `json:"person_title_status,omitempty"`
	SegmentIndex      int    `json:"segment_index,omitempty"`
	StartMS           int64  `json:"start_ms,omitempty"`
	EndMS             int64  `json:"end_ms,omitempty"`
	Snippet           string `json:"snippet,omitempty"`
	ContextExcerpt    string `json:"context_excerpt,omitempty"`
}

type businessAnalysisQuote struct {
	CallRef           string `json:"call_ref,omitempty"`
	CallID            string `json:"call_id,omitempty"`
	CallTitle         string `json:"call_title,omitempty"`
	AccountName       string `json:"account_name,omitempty"`
	OpportunityName   string `json:"opportunity_name,omitempty"`
	StartedAt         string `json:"started_at,omitempty"`
	CallDate          string `json:"call_date,omitempty"`
	LifecycleBucket   string `json:"lifecycle_bucket,omitempty"`
	AccountIndustry   string `json:"account_industry,omitempty"`
	OpportunityStage  string `json:"opportunity_stage,omitempty"`
	OpportunityType   string `json:"opportunity_type,omitempty"`
	ParticipantStatus string `json:"participant_status,omitempty"`
	PersonTitleStatus string `json:"person_title_status,omitempty"`
	SegmentIndex      int    `json:"segment_index,omitempty"`
	StartMS           int64  `json:"start_ms,omitempty"`
	EndMS             int64  `json:"end_ms,omitempty"`
	Excerpt           string `json:"excerpt"`
	ContextExcerpt    string `json:"context_excerpt,omitempty"`
}

type businessAnalysisTheme struct {
	Theme        string `json:"theme"`
	SupportCount int    `json:"support_count"`
	EvidenceType string `json:"evidence_type"`
}

func businessAnalysisTools() []tool {
	out := make([]tool, 0, len(businessAnalysisToolNameList))
	for _, name := range businessAnalysisToolNameList {
		out = append(out, tool{
			Name:        name,
			Description: businessAnalysisToolDescriptions[name],
			InputSchema: businessAnalysisInputSchema(),
		})
	}
	return out
}

func isBusinessAnalysisTool(name string) bool {
	_, ok := businessAnalysisToolDescriptions[name]
	return ok
}

func businessAnalysisInputSchema() map[string]any {
	filter := callFilterSchema()
	return objectSchema(map[string]any{
		"filter":                    filter,
		"filter_a":                  filter,
		"filter_b":                  filter,
		"cohort_id":                 map[string]any{"type": "string"},
		"cohort_id_a":               map[string]any{"type": "string"},
		"cohort_id_b":               map[string]any{"type": "string"},
		"query":                     map[string]any{"type": "string", "maxLength": maxBusinessAnalysisFTSQueryLength},
		"theme_query":               map[string]any{"type": "string", "maxLength": maxBusinessAnalysisFTSQueryLength},
		"theme":                     map[string]any{"type": "string", "maxLength": maxBusinessAnalysisFTSQueryLength},
		"dimension":                 map[string]any{"type": "string"},
		"segment":                   map[string]any{"type": "string"},
		"segment_by":                map[string]any{"type": "string"},
		"time_grain":                map[string]any{"type": "string", "enum": []string{"", "month", "quarter"}},
		"limit":                     map[string]any{"type": "integer", "minimum": 1, "maximum": maxBusinessAnalysisLimit},
		"include_call_ids":          map[string]any{"type": "boolean"},
		"include_call_titles":       map[string]any{"type": "boolean"},
		"include_account_names":     map[string]any{"type": "boolean"},
		"include_opportunity_names": map[string]any{"type": "boolean"},
	}, nil)
}

func callFilterSchema() map[string]any {
	return objectSchema(map[string]any{
		"title_query":             map[string]any{"type": "string"},
		"query":                   map[string]any{"type": "string", "maxLength": maxBusinessAnalysisFTSQueryLength},
		"from_date":               map[string]any{"type": "string", "description": "Inclusive YYYY-MM-DD call date."},
		"to_date":                 map[string]any{"type": "string", "description": "Inclusive YYYY-MM-DD call date."},
		"quarter":                 map[string]any{"type": "string", "description": "Calendar quarter such as 2026-Q1."},
		"lifecycle_bucket":        map[string]any{"type": "string"},
		"scope":                   map[string]any{"type": "string"},
		"system":                  map[string]any{"type": "string"},
		"direction":               map[string]any{"type": "string"},
		"transcript_status":       map[string]any{"type": "string", "enum": []string{"", "present", "missing", "any"}},
		"industry":                map[string]any{"type": "string"},
		"account_query":           map[string]any{"type": "string"},
		"opportunity_stage":       map[string]any{"type": "string"},
		"crm_object_type":         map[string]any{"type": "string"},
		"crm_object_id":           map[string]any{"type": "string"},
		"participant_title_query": map[string]any{"type": "string"},
		"limit":                   map[string]any{"type": "integer", "minimum": 1, "maximum": maxBusinessAnalysisLimit},
	}, nil)
}

func (s *Server) executeBusinessAnalysisTool(ctx context.Context, params toolsCallParams) (toolCallResult, error) {
	var args businessAnalysisArgs
	if err := decodeArgs(params.Arguments, &args); err != nil {
		return toolCallResult{}, err
	}
	if s.governanceActive() {
		return toolCallResult{}, governanceFilteredAggregateError(params.Name)
	}
	if strings.TrimSpace(args.AccountQuery()) != "" && !args.IncludeAccountNames {
		return toolCallResult{}, fmt.Errorf("account_query requires include_account_names=true because it can probe customer names")
	}
	normalized, err := normalizeCallFilter(args.Filter)
	if err != nil {
		return toolCallResult{}, err
	}
	filterA, err := normalizeCallFilter(args.FilterA)
	if err != nil {
		return toolCallResult{}, err
	}
	filterB, err := normalizeCallFilter(args.FilterB)
	if err != nil {
		return toolCallResult{}, err
	}

	limit := boundedBusinessAnalysisLimit(args.Limit)
	if normalized.Limit > 0 {
		limit = boundedBusinessAnalysisLimit(normalized.Limit)
		normalized.Limit = limit
	}
	if filterA.Limit > 0 {
		filterA.Limit = boundedBusinessAnalysisLimit(filterA.Limit)
	}
	if filterB.Limit > 0 {
		filterB.Limit = boundedBusinessAnalysisLimit(filterB.Limit)
	}

	query := firstNonBlank(args.Query, normalized.Query, args.ThemeQuery, args.Theme)
	themeQuery := firstNonBlank(args.ThemeQuery, args.Theme, args.Query, normalized.Query)
	if params.Name == "compare_call_cohorts" {
		if !businessAnalysisFilterIsSelective(filterA) || !businessAnalysisFilterIsSelective(filterB) {
			return toolCallResult{}, fmt.Errorf("compare_call_cohorts requires selective filter_a and filter_b fields such as quarter, date range, title_query, query, industry, opportunity_stage, crm_object_id, participant_title_query, or lifecycle_bucket")
		}
	} else if params.Name != "list_call_cohorts" && !businessAnalysisFilterIsSelective(normalized) {
		return toolCallResult{}, fmt.Errorf("%s requires at least one selective filter field such as quarter, date range, title_query, query, industry, opportunity_stage, crm_object_id, participant_title_query, or lifecycle_bucket", params.Name)
	}
	if toolNeedsThemeQuery(params.Name) && strings.TrimSpace(themeQuery) == "" {
		return toolCallResult{}, fmt.Errorf("%s requires theme_query, theme, query, or filter.query before theme-specific analysis", params.Name)
	}
	if toolNeedsEvidenceQuery(params.Name) && strings.TrimSpace(firstNonBlank(query, themeQuery)) == "" {
		return toolCallResult{}, fmt.Errorf("%s requires query, theme_query, theme, or filter.query before returning transcript excerpts", params.Name)
	}
	response := businessAnalysisResponse{
		Tool:             params.Name,
		Status:           "cache_derived",
		CohortID:         cohortID(normalized, args.CohortID),
		NormalizedFilter: normalized,
		Query:            strings.TrimSpace(args.Query),
		ThemeQuery:       firstNonBlank(args.ThemeQuery, args.Theme),
		Dimension:        strings.TrimSpace(args.Dimension),
		Segment:          firstNonBlank(args.SegmentBy, args.Segment),
		TimeGrain:        strings.TrimSpace(args.TimeGrain),
		Limit:            limit,
		Warnings:         businessAnalysisWarnings(params.Name, normalized),
		Limitations:      businessAnalysisLimitations(params.Name),
		Refinements:      suggestedFilterRefinements(normalized, query),
	}
	if params.Name == "list_call_cohorts" {
		response.Status = "stateless"
		response.Warnings = append(response.Warnings, "cohort_state_not_persisted: pass normalized_filter between calls; cohort_id is deterministic convenience only")
		return newToolResult(response)
	}
	if s.store == nil {
		response.Status = "store_unavailable"
		response.Warnings = append(response.Warnings, "store_unavailable")
		return newToolResult(response)
	}

	if params.Name == "compare_call_cohorts" {
		response.CohortIDA = cohortID(filterA, args.CohortIDA)
		response.CohortIDB = cohortID(filterB, args.CohortIDB)
		response.NormalizedFilterA = filterA
		response.NormalizedFilterB = filterB
		a, err := s.store.SearchBusinessAnalysisCalls(ctx, sqlite.BusinessAnalysisCallSearchParams{
			Filter: sqliteBusinessAnalysisFilter(filterA),
			Limit:  limit,
		})
		if err != nil {
			return toolCallResult{}, err
		}
		b, err := s.store.SearchBusinessAnalysisCalls(ctx, sqlite.BusinessAnalysisCallSearchParams{
			Filter: sqliteBusinessAnalysisFilter(filterB),
			Limit:  limit,
		})
		if err != nil {
			return toolCallResult{}, err
		}
		response.Count = int(a.Summary.CallCount + b.Summary.CallCount)
		response.Coverage = map[string]any{
			"filter_a": businessAnalysisCoverageFromSummary(a.Summary),
			"filter_b": businessAnalysisCoverageFromSummary(b.Summary),
		}
		response.SynthesisInputs = map[string]any{
			"filter_a_summary": a.Summary,
			"filter_b_summary": b.Summary,
		}
		return newToolResult(response)
	}

	cohort, err := s.store.SearchBusinessAnalysisCalls(ctx, sqlite.BusinessAnalysisCallSearchParams{
		Filter: sqliteBusinessAnalysisFilter(normalized),
		Limit:  limit,
	})
	if err != nil {
		return toolCallResult{}, err
	}
	response.Count = int(cohort.Summary.CallCount)
	response.Summary = &cohort.Summary
	response.Coverage = businessAnalysisCoverageFromSummary(cohort.Summary)

	switch params.Name {
	case "build_call_cohort", "inspect_call_cohort":
		response.Results = mcpBusinessAnalysisCallRows(cohort.Rows, args)
	case "search_calls_by_filters":
		response.Results = mcpBusinessAnalysisCallRows(cohort.Rows, args)
	case "summarize_calls_by_filters":
		dimension := firstNonBlank(args.Dimension, "lifecycle")
		summaries, err := s.businessAnalysisDimension(ctx, normalized, dimension, "", limit)
		if err != nil {
			return toolCallResult{}, err
		}
		response.Dimension = dimension
		response.Summaries = summaries
	case "search_transcripts_by_filters":
		evidenceQuery := firstNonBlank(args.Query, normalized.Query, args.ThemeQuery, args.Theme)
		items, quotes, err := s.businessAnalysisEvidence(ctx, normalized, evidenceQuery, limit, args)
		if err != nil {
			return toolCallResult{}, err
		}
		response.Results = items
		response.Quotes = quotes
	case "discover_themes_in_cohort":
		items, _, err := s.businessAnalysisEvidence(ctx, normalized, query, limit, args)
		if err != nil {
			return toolCallResult{}, err
		}
		response.Results = items
		response.Themes = discoverBusinessAnalysisThemes(items, limit)
	case "summarize_themes_by_dimension":
		dimension := firstNonBlank(args.Dimension, "industry")
		summaries, err := s.businessAnalysisDimension(ctx, normalized, dimension, themeQuery, limit)
		if err != nil {
			return toolCallResult{}, err
		}
		response.Dimension = dimension
		response.Summaries = summaries
	case "compare_themes_over_time":
		dimension := firstNonBlank(args.TimeGrain, "quarter")
		summaries, err := s.businessAnalysisDimension(ctx, normalized, dimension, themeQuery, limit)
		if err != nil {
			return toolCallResult{}, err
		}
		response.TimeGrain = dimension
		response.Summaries = summaries
	case "compare_themes_by_segment":
		dimension := firstNonBlank(args.SegmentBy, args.Segment, "industry")
		summaries, err := s.businessAnalysisDimension(ctx, normalized, dimension, themeQuery, limit)
		if err != nil {
			return toolCallResult{}, err
		}
		response.Segment = dimension
		response.Summaries = summaries
	case "extract_theme_quotes", "search_quotes_in_cohort", "rank_quotes_for_sales_use", "build_quote_pack":
		items, quotes, err := s.businessAnalysisEvidence(ctx, normalized, themeQuery, limit, args)
		if err != nil {
			return toolCallResult{}, err
		}
		response.Results = items
		response.Quotes = quotes
	case "compare_theme_outcomes":
		summaries, err := s.businessAnalysisDimension(ctx, normalized, "won_lost", themeQuery, limit)
		if err != nil {
			return toolCallResult{}, err
		}
		response.Dimension = "won_lost"
		response.Summaries = summaries
	case "summarize_pipeline_progression_by_theme":
		summaries, err := s.businessAnalysisDimension(ctx, normalized, "opportunity_stage", themeQuery, limit)
		if err != nil {
			return toolCallResult{}, err
		}
		response.Dimension = "opportunity_stage"
		response.Summaries = summaries
	case "summarize_loss_reasons_by_theme":
		summaries, err := s.businessAnalysisDimension(ctx, normalized, "loss_reason", themeQuery, limit)
		if err != nil {
			return toolCallResult{}, err
		}
		response.Dimension = "loss_reason"
		response.Summaries = summaries
	case "compare_won_lost_theme_patterns":
		summaries, err := s.businessAnalysisDimension(ctx, normalized, "won_lost", themeQuery, limit)
		if err != nil {
			return toolCallResult{}, err
		}
		response.Dimension = "won_lost"
		response.Summaries = summaries
	case "summarize_themes_by_persona":
		summaries, err := s.businessAnalysisDimension(ctx, normalized, "persona", themeQuery, limit)
		if err != nil {
			return toolCallResult{}, err
		}
		response.Dimension = "persona"
		response.Summaries = summaries
	case "summarize_themes_by_industry":
		summaries, err := s.businessAnalysisDimension(ctx, normalized, "industry", themeQuery, limit)
		if err != nil {
			return toolCallResult{}, err
		}
		response.Dimension = "industry"
		response.Summaries = summaries
	case "rank_personas_by_insight_quality":
		summaries, err := s.businessAnalysisDimension(ctx, normalized, "persona", themeQuery, limit)
		if err != nil {
			return toolCallResult{}, err
		}
		response.Dimension = "persona"
		response.Summaries = summaries
	case "diagnose_attribution_coverage":
		response.SynthesisInputs = map[string]any{"coverage": response.Coverage}
	case "generate_sales_hooks_from_themes", "generate_outreach_sequence_inputs", "recommend_target_personas_and_industries", "build_theme_brief":
		items, quotes, err := s.businessAnalysisEvidence(ctx, normalized, themeQuery, limit, args)
		if err != nil {
			return toolCallResult{}, err
		}
		response.Results = items
		response.Quotes = quotes
		response.Themes = discoverBusinessAnalysisThemes(items, limit)
		response.SynthesisInputs = map[string]any{
			"cohort_summary": cohort.Summary,
			"themes":         response.Themes,
			"quote_count":    len(quotes),
			"limitations":    response.Limitations,
		}
	case "score_cohort_evidence_quality":
		response.QualityScore = businessAnalysisQualityScore(cohort.Summary)
	case "explain_analysis_limitations":
		response.SynthesisInputs = map[string]any{
			"coverage":    response.Coverage,
			"limitations": response.Limitations,
			"refinements": response.Refinements,
		}
	case "suggest_filter_refinements":
		response.SynthesisInputs = map[string]any{"refinements": response.Refinements}
	default:
		return toolCallResult{}, fmt.Errorf("unknown business-analysis tool %q", params.Name)
	}
	if response.Count == 0 {
		response.Warnings = append(response.Warnings, "empty_cohort: no calls matched the normalized filter")
	}
	if cohort.Summary.ParticipantTitleCallCount == 0 {
		response.Warnings = append(response.Warnings, "participant_title_missing_or_unmapped")
	}
	if cohort.Summary.AccountIndustryCount == 0 {
		response.Warnings = append(response.Warnings, "account_industry_missing_or_unmapped")
	}
	if cohort.Summary.OpportunityStageCount == 0 {
		response.Warnings = append(response.Warnings, "opportunity_stage_missing_or_unmapped")
	}
	if strings.TrimSpace(themeQuery) == "" && toolNeedsThemeQuery(params.Name) {
		response.Warnings = append(response.Warnings, "theme_query_missing: returned cohort-level evidence or summaries without a theme-specific filter")
	}
	return newToolResult(response)
}

func (s *Server) businessAnalysisDimension(ctx context.Context, filter callFilter, dimension string, themeQuery string, limit int) ([]sqlite.BusinessAnalysisDimensionRow, error) {
	return s.store.SummarizeBusinessAnalysisDimension(ctx, sqlite.BusinessAnalysisDimensionSummaryParams{
		Filter:     sqliteBusinessAnalysisFilter(filter),
		Dimension:  dimension,
		ThemeQuery: themeQuery,
		Limit:      limit,
	})
}

func (s *Server) businessAnalysisEvidence(ctx context.Context, filter callFilter, query string, limit int, args businessAnalysisArgs) ([]businessAnalysisItem, []businessAnalysisQuote, error) {
	rows, err := s.store.SearchBusinessAnalysisEvidence(ctx, sqlite.BusinessAnalysisEvidenceSearchParams{
		Filter: sqliteBusinessAnalysisFilter(filter),
		Query:  query,
		Limit:  limit,
	})
	if err != nil {
		return nil, nil, err
	}
	items := make([]businessAnalysisItem, 0, len(rows))
	quotes := make([]businessAnalysisQuote, 0, len(rows))
	for _, row := range rows {
		if s.isSuppressedCall(row.CallID) {
			continue
		}
		item := mcpBusinessAnalysisEvidenceRow(row, args)
		items = append(items, item)
		quotes = append(quotes, businessAnalysisQuote{
			CallRef:           item.CallRef,
			CallID:            item.CallID,
			CallTitle:         item.CallTitle,
			AccountName:       item.AccountName,
			OpportunityName:   item.OpportunityName,
			StartedAt:         item.StartedAt,
			CallDate:          item.CallDate,
			LifecycleBucket:   item.LifecycleBucket,
			AccountIndustry:   item.AccountIndustry,
			OpportunityStage:  item.OpportunityStage,
			OpportunityType:   item.OpportunityType,
			ParticipantStatus: item.ParticipantStatus,
			PersonTitleStatus: item.PersonTitleStatus,
			SegmentIndex:      item.SegmentIndex,
			StartMS:           item.StartMS,
			EndMS:             item.EndMS,
			Excerpt:           item.Snippet,
			ContextExcerpt:    item.ContextExcerpt,
		})
	}
	return items, quotes, nil
}

func (args businessAnalysisArgs) AccountQuery() string {
	return firstNonBlank(args.Filter.AccountQuery, args.FilterA.AccountQuery, args.FilterB.AccountQuery)
}

func normalizeCallFilter(filter callFilter) (callFilter, error) {
	filter.TitleQuery = strings.ToLower(strings.TrimSpace(filter.TitleQuery))
	filter.Query = strings.TrimSpace(filter.Query)
	filter.FromDate = strings.TrimSpace(filter.FromDate)
	filter.ToDate = strings.TrimSpace(filter.ToDate)
	filter.Quarter = strings.TrimSpace(filter.Quarter)
	filter.LifecycleBucket = strings.TrimSpace(filter.LifecycleBucket)
	filter.Scope = strings.TrimSpace(filter.Scope)
	filter.System = strings.TrimSpace(filter.System)
	filter.Direction = strings.TrimSpace(filter.Direction)
	filter.TranscriptStatus = strings.ToLower(strings.TrimSpace(filter.TranscriptStatus))
	filter.Industry = strings.TrimSpace(filter.Industry)
	filter.AccountQuery = strings.TrimSpace(filter.AccountQuery)
	filter.OpportunityStage = strings.TrimSpace(filter.OpportunityStage)
	filter.CRMObjectType = strings.TrimSpace(filter.CRMObjectType)
	filter.CRMObjectID = strings.TrimSpace(filter.CRMObjectID)
	filter.ParticipantTitleQuery = strings.TrimSpace(filter.ParticipantTitleQuery)
	if filter.Quarter != "" {
		canonical, fromDate, toDate, err := normalizeCallFilterQuarter(filter.Quarter)
		if err != nil {
			return callFilter{}, err
		}
		filter.Quarter = canonical
		if filter.FromDate == "" {
			filter.FromDate = fromDate
		}
		if filter.ToDate == "" {
			filter.ToDate = toDate
		}
	}
	if err := validateYYYYMMDD(filter.FromDate, "from_date"); err != nil {
		return callFilter{}, err
	}
	if err := validateYYYYMMDD(filter.ToDate, "to_date"); err != nil {
		return callFilter{}, err
	}
	if filter.FromDate != "" && filter.ToDate != "" && filter.FromDate > filter.ToDate {
		return callFilter{}, fmt.Errorf("from_date must be on or before to_date")
	}
	if filter.TranscriptStatus != "" && filter.TranscriptStatus != "present" && filter.TranscriptStatus != "missing" && filter.TranscriptStatus != "any" {
		return callFilter{}, fmt.Errorf("transcript_status must be present, missing, or any")
	}
	if filter.Limit > 0 {
		filter.Limit = boundedBusinessAnalysisLimit(filter.Limit)
	}
	return filter, nil
}

func normalizeCallFilterQuarter(value string) (string, string, string, error) {
	normalized := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(value), " ", "-"))
	normalized = strings.ReplaceAll(normalized, "_", "-")
	var year, quarter string
	switch {
	case len(normalized) == len("2026-Q1") && normalized[4] == '-' && normalized[5] == 'Q':
		year = normalized[:4]
		quarter = normalized[5:]
	case len(normalized) == len("2026Q1") && normalized[4] == 'Q':
		year = normalized[:4]
		quarter = normalized[4:]
	case len(normalized) == len("Q1-2026") && normalized[0] == 'Q' && normalized[2] == '-':
		quarter = normalized[:2]
		year = normalized[3:]
	default:
		return "", "", "", fmt.Errorf("quarter must look like 2026-Q1")
	}
	if _, err := time.Parse("2006-01-02", year+"-01-01"); err != nil {
		return "", "", "", fmt.Errorf("quarter must include a four-digit year")
	}
	switch quarter {
	case "Q1":
		return year + "-Q1", year + "-01-01", year + "-03-31", nil
	case "Q2":
		return year + "-Q2", year + "-04-01", year + "-06-30", nil
	case "Q3":
		return year + "-Q3", year + "-07-01", year + "-09-30", nil
	case "Q4":
		return year + "-Q4", year + "-10-01", year + "-12-31", nil
	default:
		return "", "", "", fmt.Errorf("quarter must be Q1, Q2, Q3, or Q4")
	}
}

func validateYYYYMMDD(value, field string) error {
	if value == "" {
		return nil
	}
	if _, err := time.Parse("2006-01-02", value); err != nil {
		return fmt.Errorf("%s must be YYYY-MM-DD", field)
	}
	return nil
}

func boundedBusinessAnalysisLimit(value int) int {
	if value <= 0 {
		return defaultBusinessAnalysisLimit
	}
	if value > maxBusinessAnalysisLimit {
		return maxBusinessAnalysisLimit
	}
	return value
}

func cohortID(filter callFilter, explicit string) string {
	if trimmed := strings.TrimSpace(explicit); trimmed != "" {
		return trimmed
	}
	payload, _ := json.Marshal(filter)
	sum := sha256.Sum256(payload)
	return "cohort_" + hex.EncodeToString(sum[:])[:16]
}

func businessAnalysisCoverageFromSummary(summary sqlite.BusinessAnalysisCohortSummary) map[string]any {
	return map[string]any{
		"call_count":                   summary.CallCount,
		"transcript_count":             summary.TranscriptCount,
		"missing_transcript_count":     summary.MissingTranscriptCount,
		"transcript_coverage_rate":     summary.TranscriptCoverageRate,
		"account_industry_count":       summary.AccountIndustryCount,
		"opportunity_stage_count":      summary.OpportunityStageCount,
		"participant_title_call_count": summary.ParticipantTitleCallCount,
		"participant_title_rate":       summary.ParticipantTitleRate,
		"crm_outcome_coverage_hint":    summary.CRMOutcomeCoverageHint,
		"participant_coverage_hint":    summary.ParticipantCoverageHint,
		"industry_coverage_hint":       summary.IndustryCoverageHint,
		"cache_derived_not_llm_claims": true,
	}
}

func toolNeedsThemeQuery(toolName string) bool {
	switch toolName {
	case "summarize_themes_by_dimension", "compare_themes_over_time", "compare_themes_by_segment",
		"extract_theme_quotes", "search_quotes_in_cohort", "rank_quotes_for_sales_use", "build_quote_pack",
		"compare_theme_outcomes", "summarize_pipeline_progression_by_theme", "summarize_loss_reasons_by_theme",
		"compare_won_lost_theme_patterns", "summarize_themes_by_persona", "summarize_themes_by_industry",
		"rank_personas_by_insight_quality", "generate_sales_hooks_from_themes", "generate_outreach_sequence_inputs",
		"recommend_target_personas_and_industries", "build_theme_brief":
		return true
	default:
		return false
	}
}

func toolNeedsEvidenceQuery(toolName string) bool {
	switch toolName {
	case "search_transcripts_by_filters", "discover_themes_in_cohort",
		"extract_theme_quotes", "search_quotes_in_cohort", "rank_quotes_for_sales_use", "build_quote_pack",
		"generate_sales_hooks_from_themes", "generate_outreach_sequence_inputs",
		"recommend_target_personas_and_industries", "build_theme_brief":
		return true
	default:
		return false
	}
}

func businessAnalysisFilterIsSelective(filter callFilter) bool {
	if filter.FromDate != "" && filter.ToDate != "" {
		return true
	}
	return firstNonBlank(
		filter.Quarter,
		filter.TitleQuery,
		filter.Query,
		filter.LifecycleBucket,
		filter.Industry,
		filter.AccountQuery,
		filter.OpportunityStage,
		filter.CRMObjectID,
		filter.ParticipantTitleQuery,
	) != ""
}

func businessAnalysisWarnings(toolName string, filter callFilter) []string {
	var warnings []string
	if filter.ParticipantTitleQuery != "" {
		warnings = append(warnings, "participant_title_query uses cached participant or Contact/Lead title fields only; no persona is inferred")
	}
	if filter.TranscriptStatus == "missing" {
		warnings = append(warnings, "missing transcript cohorts cannot return quote evidence")
	}
	if strings.Contains(toolName, "loss_reason") {
		warnings = append(warnings, "loss_reason depends on cached Opportunity loss-reason fields and may be blank")
	}
	return warnings
}

func businessAnalysisLimitations(toolName string) []string {
	limitations := []string{
		"read_only_sqlite_cache_only",
		"no_raw_sql_or_arbitrary_table_access",
		"bounded_results_and_redacted_identifiers_by_default",
		"cache_derived_signals_not_llm_conclusions",
	}
	switch toolName {
	case "compare_theme_outcomes", "summarize_pipeline_progression_by_theme", "summarize_loss_reasons_by_theme", "compare_won_lost_theme_patterns", "diagnose_attribution_coverage":
		limitations = append(limitations, "outcome_attribution_may_be_missing_or_incomplete", "crm_outcome_fields_may_be_missing_or_sparse")
	case "summarize_themes_by_persona", "rank_personas_by_insight_quality":
		limitations = append(limitations, "participant_titles_may_be_missing_or_unmapped")
	case "summarize_themes_by_industry", "recommend_target_personas_and_industries":
		limitations = append(limitations, "account_industry_may_be_missing_or_unmapped")
	}
	return limitations
}

func suggestedFilterRefinements(filter callFilter, query string) []string {
	var out []string
	if filter.FromDate == "" || filter.ToDate == "" {
		out = append(out, "add from_date and to_date to make the cohort reproducible and bounded by calendar range")
	}
	if filter.TranscriptStatus == "" {
		out = append(out, "set transcript_status=present for quote/theme tools or missing for transcript coverage analysis")
	}
	if strings.TrimSpace(query) == "" {
		out = append(out, "add query or theme_query before requesting transcript evidence or quote packs")
	}
	return out
}

func sqliteBusinessAnalysisFilter(filter callFilter) sqlite.BusinessAnalysisFilter {
	return sqlite.BusinessAnalysisFilter{
		TitleQuery:            filter.TitleQuery,
		Query:                 filter.Query,
		FromDate:              filter.FromDate,
		ToDate:                filter.ToDate,
		Quarter:               filter.Quarter,
		LifecycleBucket:       filter.LifecycleBucket,
		Scope:                 filter.Scope,
		System:                filter.System,
		Direction:             filter.Direction,
		TranscriptStatus:      filter.TranscriptStatus,
		Industry:              filter.Industry,
		AccountQuery:          filter.AccountQuery,
		OpportunityStage:      filter.OpportunityStage,
		CRMObjectType:         filter.CRMObjectType,
		CRMObjectID:           filter.CRMObjectID,
		ParticipantTitleQuery: filter.ParticipantTitleQuery,
		Limit:                 filter.Limit,
	}
}

func mcpBusinessAnalysisCallRows(rows []sqlite.BusinessAnalysisCallRow, args businessAnalysisArgs) []businessAnalysisItem {
	out := make([]businessAnalysisItem, 0, len(rows))
	for _, row := range rows {
		item := businessAnalysisItem{
			CallRef:           callRef(row.CallID),
			StartedAt:         row.StartedAt,
			CallDate:          row.CallDate,
			CallMonth:         row.CallMonth,
			LifecycleBucket:   row.LifecycleBucket,
			AccountIndustry:   row.AccountIndustry,
			OpportunityStage:  row.OpportunityStage,
			OpportunityType:   row.OpportunityType,
			ParticipantStatus: row.ParticipantStatus,
			PersonTitleStatus: row.PersonTitleStatus,
		}
		if args.IncludeCallIDs {
			item.CallID = row.CallID
		}
		if args.IncludeCallTitles {
			item.CallTitle = row.Title
		}
		out = append(out, item)
	}
	return out
}

func mcpBusinessAnalysisEvidenceRow(row sqlite.BusinessAnalysisEvidenceRow, args businessAnalysisArgs) businessAnalysisItem {
	item := businessAnalysisItem{
		CallRef:           callRef(row.CallID),
		StartedAt:         row.StartedAt,
		CallDate:          row.CallDate,
		CallMonth:         row.CallMonth,
		LifecycleBucket:   row.LifecycleBucket,
		AccountIndustry:   row.AccountIndustry,
		OpportunityStage:  row.OpportunityStage,
		OpportunityType:   row.OpportunityType,
		ParticipantStatus: row.ParticipantStatus,
		PersonTitleStatus: row.PersonTitleStatus,
		SegmentIndex:      row.SegmentIndex,
		StartMS:           row.StartMS,
		EndMS:             row.EndMS,
		Snippet:           row.Snippet,
		ContextExcerpt:    row.ContextExcerpt,
	}
	if args.IncludeCallIDs {
		item.CallID = row.CallID
	}
	if args.IncludeCallTitles {
		item.CallTitle = row.Title
	}
	if args.IncludeAccountNames {
		item.AccountName = row.AccountName
	}
	if args.IncludeOpportunityNames {
		item.OpportunityName = row.OpportunityName
	}
	return item
}

func callRef(callID string) string {
	if strings.TrimSpace(callID) == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(callID))
	return "call_ref_" + hex.EncodeToString(sum[:])[:12]
}

func discoverBusinessAnalysisThemes(items []businessAnalysisItem, limit int) []businessAnalysisTheme {
	if limit <= 0 || limit > maxBusinessAnalysisLimit {
		limit = defaultBusinessAnalysisLimit
	}
	stop := map[string]struct{}{
		"able": {}, "about": {}, "after": {}, "again": {}, "also": {}, "because": {}, "been": {}, "being": {}, "call": {},
		"could": {}, "from": {}, "have": {}, "into": {}, "more": {}, "need": {}, "needs": {}, "only": {}, "process": {},
		"really": {}, "some": {}, "that": {}, "their": {}, "them": {}, "then": {}, "there": {}, "they": {}, "this": {},
		"through": {}, "with": {}, "would": {}, "your": {},
	}
	counts := map[string]int{}
	for _, item := range items {
		text := strings.ToLower(item.Snippet + " " + item.ContextExcerpt)
		var token strings.Builder
		flush := func() {
			value := token.String()
			token.Reset()
			if len(value) < 4 {
				return
			}
			if _, ok := stop[value]; ok {
				return
			}
			counts[value]++
		}
		for _, r := range text {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
				token.WriteRune(r)
				continue
			}
			flush()
		}
		flush()
	}
	type pair struct {
		term  string
		count int
	}
	pairs := make([]pair, 0, len(counts))
	for term, count := range counts {
		pairs = append(pairs, pair{term: term, count: count})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].count == pairs[j].count {
			return pairs[i].term < pairs[j].term
		}
		return pairs[i].count > pairs[j].count
	})
	if len(pairs) > limit {
		pairs = pairs[:limit]
	}
	out := make([]businessAnalysisTheme, 0, len(pairs))
	for _, item := range pairs {
		out = append(out, businessAnalysisTheme{
			Theme:        item.term,
			SupportCount: item.count,
			EvidenceType: "deterministic_keyword_signal",
		})
	}
	return out
}

func businessAnalysisQualityScore(summary sqlite.BusinessAnalysisCohortSummary) int {
	if summary.CallCount == 0 {
		return 0
	}
	score := 25
	if summary.TranscriptCoverageRate >= 0.8 {
		score += 25
	} else if summary.TranscriptCoverageRate >= 0.5 {
		score += 15
	} else if summary.TranscriptCoverageRate > 0 {
		score += 5
	}
	if summary.AccountIndustryCount > 0 {
		score += 15
	}
	if summary.OpportunityStageCount > 0 {
		score += 15
	}
	if summary.ParticipantTitleCallCount > 0 {
		score += 10
	}
	if summary.CallCount >= 10 {
		score += 10
	} else if summary.CallCount >= 3 {
		score += 5
	}
	if score > 100 {
		return 100
	}
	return score
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
