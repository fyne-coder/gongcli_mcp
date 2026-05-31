package mcp

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	defaultSearchResults        = 100
	defaultCRMFields            = 200
	defaultLateStageSignals     = 100
	defaultMissingTranscripts   = 500
	defaultOpportunitySummaries = 100
	defaultCRMMatrixCells       = 200
	defaultCallDetailObjects    = 20
	defaultCallDetailFieldNames = 50
	defaultLifecycleResults     = 100
	defaultLifecycleCRMFields   = 200
	defaultCallFactGroups       = 200
	defaultInventoryResults     = 200
	defaultBusinessAnalysisRows = 100

	defaultSearchRequestLimit             = 20
	defaultCRMFieldRequestLimit           = 50
	defaultCRMFieldValueRequestLimit      = 20
	defaultLateStageSignalRequestLimit    = 25
	defaultMissingTranscriptRequestLimit  = 100
	defaultOpportunitySummaryRequestLimit = 25
	defaultCRMMatrixRequestLimit          = 50
	defaultLifecycleRequestLimit          = 25
	defaultLifecycleCRMFieldRequestLimit  = 50
	defaultCallFactRequestLimit           = 50
	defaultInventoryRequestLimit          = 50

	hardMaxSearchResults        = 1000
	hardMaxCRMFields            = 1000
	hardMaxLateStageSignals     = 500
	hardMaxMissingTranscripts   = 10000
	hardMaxOpportunitySummaries = 1000
	hardMaxCRMMatrixCells       = 1000
	hardMaxLifecycleResults     = 1000
	hardMaxLifecycleCRMFields   = 1000
	hardMaxCallFactGroups       = 1000
	hardMaxInventoryResults     = 1000
	hardMaxBusinessAnalysisRows = 1000
)

type LimitPolicy struct {
	SearchResults        int `json:"search_results"`
	CRMFields            int `json:"crm_fields"`
	LateStageSignals     int `json:"late_stage_signals"`
	MissingTranscripts   int `json:"missing_transcripts"`
	OpportunitySummaries int `json:"opportunity_summaries"`
	CRMMatrixCells       int `json:"crm_matrix_cells"`
	LifecycleResults     int `json:"lifecycle_results"`
	LifecycleCRMFields   int `json:"lifecycle_crm_fields"`
	CallFactGroups       int `json:"call_fact_groups"`
	InventoryResults     int `json:"inventory_results"`
	BusinessAnalysisRows int `json:"business_analysis_rows"`
}

func DefaultLimitPolicy() LimitPolicy {
	return LimitPolicy{
		SearchResults:        defaultSearchResults,
		CRMFields:            defaultCRMFields,
		LateStageSignals:     defaultLateStageSignals,
		MissingTranscripts:   defaultMissingTranscripts,
		OpportunitySummaries: defaultOpportunitySummaries,
		CRMMatrixCells:       defaultCRMMatrixCells,
		LifecycleResults:     defaultLifecycleResults,
		LifecycleCRMFields:   defaultLifecycleCRMFields,
		CallFactGroups:       defaultCallFactGroups,
		InventoryResults:     defaultInventoryResults,
		BusinessAnalysisRows: defaultBusinessAnalysisRows,
	}
}

func LimitPolicyFromEnv(getenv func(string) string) (LimitPolicy, error) {
	if getenv == nil {
		return DefaultLimitPolicy(), nil
	}
	policy := DefaultLimitPolicy()
	var err error
	for _, spec := range limitEnvSpecs() {
		raw := strings.TrimSpace(getenv(spec.Env))
		if raw == "" {
			continue
		}
		value, parseErr := strconv.Atoi(raw)
		if parseErr != nil || value <= 0 {
			return LimitPolicy{}, fmt.Errorf("%s must be a positive integer", spec.Env)
		}
		policy, err = policy.WithOverride(spec.Field, value)
		if err != nil {
			return LimitPolicy{}, fmt.Errorf("%s: %w", spec.Env, err)
		}
	}
	return policy.Normalize(), nil
}

func (p LimitPolicy) Normalize() LimitPolicy {
	def := DefaultLimitPolicy()
	p.SearchResults = normalizeLimit(p.SearchResults, def.SearchResults, hardMaxSearchResults)
	p.CRMFields = normalizeLimit(p.CRMFields, def.CRMFields, hardMaxCRMFields)
	p.LateStageSignals = normalizeLimit(p.LateStageSignals, def.LateStageSignals, hardMaxLateStageSignals)
	p.MissingTranscripts = normalizeLimit(p.MissingTranscripts, def.MissingTranscripts, hardMaxMissingTranscripts)
	p.OpportunitySummaries = normalizeLimit(p.OpportunitySummaries, def.OpportunitySummaries, hardMaxOpportunitySummaries)
	p.CRMMatrixCells = normalizeLimit(p.CRMMatrixCells, def.CRMMatrixCells, hardMaxCRMMatrixCells)
	p.LifecycleResults = normalizeLimit(p.LifecycleResults, def.LifecycleResults, hardMaxLifecycleResults)
	p.LifecycleCRMFields = normalizeLimit(p.LifecycleCRMFields, def.LifecycleCRMFields, hardMaxLifecycleCRMFields)
	p.CallFactGroups = normalizeLimit(p.CallFactGroups, def.CallFactGroups, hardMaxCallFactGroups)
	p.InventoryResults = normalizeLimit(p.InventoryResults, def.InventoryResults, hardMaxInventoryResults)
	p.BusinessAnalysisRows = normalizeLimit(p.BusinessAnalysisRows, def.BusinessAnalysisRows, hardMaxBusinessAnalysisRows)
	return p
}

func (p LimitPolicy) WithOverride(field string, value int) (LimitPolicy, error) {
	if value <= 0 {
		return LimitPolicy{}, fmt.Errorf("%s must be a positive integer", field)
	}
	switch field {
	case "search_results":
		p.SearchResults = min(value, hardMaxSearchResults)
	case "crm_fields":
		p.CRMFields = min(value, hardMaxCRMFields)
	case "late_stage_signals":
		p.LateStageSignals = min(value, hardMaxLateStageSignals)
	case "missing_transcripts":
		p.MissingTranscripts = min(value, hardMaxMissingTranscripts)
	case "opportunity_summaries":
		p.OpportunitySummaries = min(value, hardMaxOpportunitySummaries)
	case "crm_matrix_cells":
		p.CRMMatrixCells = min(value, hardMaxCRMMatrixCells)
	case "lifecycle_results":
		p.LifecycleResults = min(value, hardMaxLifecycleResults)
	case "lifecycle_crm_fields":
		p.LifecycleCRMFields = min(value, hardMaxLifecycleCRMFields)
	case "call_fact_groups":
		p.CallFactGroups = min(value, hardMaxCallFactGroups)
	case "inventory_results":
		p.InventoryResults = min(value, hardMaxInventoryResults)
	case "business_analysis_rows":
		p.BusinessAnalysisRows = min(value, hardMaxBusinessAnalysisRows)
	default:
		return LimitPolicy{}, fmt.Errorf("unknown limit field %q", field)
	}
	return p.Normalize(), nil
}

func (p LimitPolicy) SearchLimit(value int) int {
	return capLimit(value, defaultSearchRequestLimit, p.Normalize().SearchResults)
}

func (p LimitPolicy) MissingTranscriptLimit(value int) int {
	return capLimit(value, defaultMissingTranscriptRequestLimit, p.Normalize().MissingTranscripts)
}

func (p LimitPolicy) LifecycleLimit(value int) int {
	return capLimit(value, defaultLifecycleRequestLimit, p.Normalize().LifecycleResults)
}

func (p LimitPolicy) BusinessAnalysisLimit(value int) int {
	return capLimit(value, defaultBusinessAnalysisLimit, p.Normalize().BusinessAnalysisRows)
}

func normalizeLimit(value, def, max int) int {
	if value <= 0 {
		return def
	}
	if value > max {
		return max
	}
	return value
}

func capLimit(value, def, max int) int {
	if value <= 0 {
		value = def
	}
	if value > max {
		return max
	}
	return value
}

type limitEnvSpec struct {
	Env   string
	Field string
}

func limitEnvSpecs() []limitEnvSpec {
	return []limitEnvSpec{
		{Env: "GONGMCP_MAX_SEARCH_RESULTS", Field: "search_results"},
		{Env: "GONGMCP_MAX_CRM_FIELDS", Field: "crm_fields"},
		{Env: "GONGMCP_MAX_LATE_STAGE_SIGNALS", Field: "late_stage_signals"},
		{Env: "GONGMCP_MAX_MISSING_TRANSCRIPTS", Field: "missing_transcripts"},
		{Env: "GONGMCP_MAX_OPPORTUNITY_SUMMARIES", Field: "opportunity_summaries"},
		{Env: "GONGMCP_MAX_CRM_MATRIX_CELLS", Field: "crm_matrix_cells"},
		{Env: "GONGMCP_MAX_LIFECYCLE_RESULTS", Field: "lifecycle_results"},
		{Env: "GONGMCP_MAX_LIFECYCLE_CRM_FIELDS", Field: "lifecycle_crm_fields"},
		{Env: "GONGMCP_MAX_CALL_FACT_GROUPS", Field: "call_fact_groups"},
		{Env: "GONGMCP_MAX_INVENTORY_RESULTS", Field: "inventory_results"},
		{Env: "GONGMCP_MAX_BUSINESS_ANALYSIS_RESULTS", Field: "business_analysis_rows"},
	}
}
