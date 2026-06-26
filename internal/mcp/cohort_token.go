package mcp

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

const cohortTokenPrefix = "cohort_v1_"

type cohortTokenPayload struct {
	V      int        `json:"v"`
	Filter callFilter `json:"filter"`
}

func cohortTokenSchemaField() map[string]any {
	return map[string]any{
		"type":        "string",
		"description": "Stateless reusable cohort handoff from analyze.cohort.build or a prior cohort response. When supplied, filter is optional if the token decodes to the same normalized filter.",
	}
}

func encodeCohortToken(filter callFilter) (string, error) {
	payload := cohortTokenPayload{V: 1, Filter: filter}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return cohortTokenPrefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeCohortToken(token string) (callFilter, error) {
	trimmed := strings.TrimSpace(token)
	if trimmed == "" {
		return callFilter{}, fmt.Errorf("cohort_token is required")
	}
	if !strings.HasPrefix(trimmed, cohortTokenPrefix) {
		return callFilter{}, fmt.Errorf("cohort_token must start with %q", cohortTokenPrefix)
	}
	encoded := strings.TrimPrefix(trimmed, cohortTokenPrefix)
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return callFilter{}, fmt.Errorf("cohort_token is malformed: invalid base64url encoding")
	}
	var payload cohortTokenPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return callFilter{}, fmt.Errorf("cohort_token is malformed: invalid JSON payload")
	}
	if payload.V != 1 {
		return callFilter{}, fmt.Errorf("cohort_token version %d is unsupported; expected cohort_token v1", payload.V)
	}
	normalized, err := normalizeCallFilter(payload.Filter)
	if err != nil {
		return callFilter{}, fmt.Errorf("cohort_token filter failed validation: %w", err)
	}
	return normalized, nil
}

func resolveCohortFilter(explicit callFilter, cohortToken string) (callFilter, error) {
	token := strings.TrimSpace(cohortToken)
	if token == "" {
		return normalizeCallFilter(explicit)
	}
	fromToken, err := decodeCohortToken(token)
	if err != nil {
		return callFilter{}, err
	}
	if !cohortFilterProvided(explicit) {
		return fromToken, nil
	}
	explicitNorm, err := normalizeCallFilter(explicit)
	if err != nil {
		return callFilter{}, err
	}
	if !cohortFiltersEquivalent(explicitNorm, fromToken) {
		return callFilter{}, fmt.Errorf("cohort_token does not match the supplied filter; omit filter or resend the same normalized filter")
	}
	return explicitNorm, nil
}

func cohortFilterProvided(filter callFilter) bool {
	if len(filter.DimensionFilters) > 0 || len(filter.ExcludeLifecycleBuckets) > 0 || filter.ExcludeLikelyVoicemail {
		return true
	}
	if filter.Limit > 0 {
		return true
	}
	return firstNonBlank(
		filter.TitleQuery,
		filter.Query,
		filter.FromDate,
		filter.ToDate,
		filter.Quarter,
		filter.LifecycleBucket,
		filter.Scope,
		filter.System,
		filter.Direction,
		filter.TranscriptStatus,
		filter.Industry,
		filter.AccountQuery,
		filter.OpportunityStage,
		filter.CRMObjectType,
		filter.CRMObjectID,
		filter.ParticipantTitleQuery,
	) != ""
}

func cohortFiltersEquivalent(a, b callFilter) bool {
	aJSON, err := json.Marshal(a)
	if err != nil {
		return false
	}
	bJSON, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return string(aJSON) == string(bJSON)
}

func attachCohortHandoff(normalized callFilter, explicitCohortID string) (string, string, error) {
	token, err := encodeCohortToken(normalized)
	if err != nil {
		return "", "", err
	}
	return cohortID(normalized, explicitCohortID), token, nil
}

func withCohortTokenField(schema map[string]any) map[string]any {
	props, _ := schema["properties"].(map[string]any)
	if props == nil {
		return schema
	}
	props["cohort_token"] = cohortTokenSchemaField()
	return schema
}

func routedToolAcceptsCohortToken(name string) bool {
	switch name {
	case internalRoutedToolCallCount:
		return false
	case internalRoutedToolQuestionAnswer,
		internalRoutedToolThemeIntelReport,
		internalRoutedToolBuyerQuestions,
		internalRoutedToolObjectionSignals:
		return true
	default:
		return isBusinessAnalysisTool(name) && name != "compare_call_cohorts" && name != "list_call_cohorts"
	}
}

func applyCohortTokenToRawArgs(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return raw, nil
	}
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil {
		return raw, err
	}
	cohortToken, _ := args["cohort_token"].(string)
	if strings.TrimSpace(cohortToken) == "" {
		return raw, nil
	}
	var explicit callFilter
	if filterRaw, ok := args["filter"]; ok && filterRaw != nil {
		filterBytes, err := json.Marshal(filterRaw)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(filterBytes, &explicit); err != nil {
			return nil, err
		}
	}
	resolved, err := resolveCohortFilter(explicit, cohortToken)
	if err != nil {
		return nil, err
	}
	args["filter"] = resolved
	out, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	return out, nil
}
