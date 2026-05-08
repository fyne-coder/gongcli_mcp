package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
)

const maxBusinessSignalTopics = 12

type businessSignalExtractionArgs struct {
	Filter                  callFilter `json:"filter"`
	Topics                  []string   `json:"topics"`
	Query                   string     `json:"query"`
	ThemeQuery              string     `json:"theme_query"`
	Limit                   int        `json:"limit"`
	FieldProfile            string     `json:"field_profile"`
	SpeakerRole             string     `json:"speaker_role"`
	IncludeCallIDs          bool       `json:"include_call_ids"`
	IncludeCallTitles       bool       `json:"include_call_titles"`
	IncludeAccountNames     bool       `json:"include_account_names"`
	IncludeOpportunityNames bool       `json:"include_opportunity_names"`
}

type businessSignalBucket struct {
	Topic         string                  `json:"topic"`
	EvidenceCount int                     `json:"evidence_count"`
	Evidence      []businessAnalysisItem  `json:"evidence"`
	Quotes        []businessAnalysisQuote `json:"quotes"`
}

func businessSignalExtractionSchema() map[string]any {
	return objectSchema(map[string]any{
		"filter":                    map[string]any{"type": "object"},
		"topics":                    map[string]any{"type": "array", "items": map[string]any{"type": "string", "maxLength": maxBusinessAnalysisFTSQueryLength}, "maxItems": maxBusinessSignalTopics},
		"query":                     map[string]any{"type": "string", "maxLength": maxBusinessAnalysisFTSQueryLength, "description": "Optional single topic seed."},
		"theme_query":               map[string]any{"type": "string", "maxLength": maxBusinessAnalysisFTSQueryLength, "description": "Alias for query."},
		"limit":                     map[string]any{"type": "integer", "minimum": 1, "maximum": hardMaxBusinessAnalysisRows},
		"field_profile":             fieldProfileSchema(),
		"speaker_role":              speakerRoleSchema(),
		"include_call_ids":          map[string]any{"type": "boolean"},
		"include_call_titles":       map[string]any{"type": "boolean"},
		"include_account_names":     map[string]any{"type": "boolean"},
		"include_opportunity_names": map[string]any{"type": "boolean"},
	}, nil)
}

func (s *Server) executeBusinessSignalExtraction(ctx context.Context, operation string, raw json.RawMessage) (toolCallResult, error) {
	var args businessSignalExtractionArgs
	if err := decodeArgs(raw, &args); err != nil {
		return toolCallResult{}, err
	}
	if s.governanceActive() {
		return toolCallResult{}, governanceFilteredAggregateError(operation)
	}
	baArgs := businessAnalysisArgs{
		Filter:                  args.Filter,
		Query:                   firstNonBlank(args.Query, args.ThemeQuery),
		ThemeQuery:              firstNonBlank(args.ThemeQuery, args.Query),
		Limit:                   args.Limit,
		FieldProfile:            args.FieldProfile,
		SpeakerRole:             args.SpeakerRole,
		IncludeCallIDs:          args.IncludeCallIDs,
		IncludeCallTitles:       args.IncludeCallTitles,
		IncludeAccountNames:     args.IncludeAccountNames,
		IncludeOpportunityNames: args.IncludeOpportunityNames,
	}
	if err := baArgs.ApplyFieldProfile(); err != nil {
		return toolCallResult{}, err
	}
	speakerRole, err := normalizeSpeakerRoleFilter(firstNonBlank(args.SpeakerRole, speakerRoleExternalOrUnknown))
	if err != nil {
		return toolCallResult{}, err
	}
	baArgs.SpeakerRole = speakerRole
	normalized, err := normalizeCallFilter(args.Filter)
	if err != nil {
		return toolCallResult{}, err
	}
	if !businessAnalysisFilterIsSelective(normalized) {
		return toolCallResult{}, fmt.Errorf("%s requires a selective filter such as date range, quarter, title_query, query, industry, opportunity_stage, crm_object_id, participant_title_query, or lifecycle_bucket", operation)
	}
	if strings.TrimSpace(normalized.AccountQuery) != "" && !baArgs.IncludeAccountNames {
		return toolCallResult{}, fmt.Errorf("account_query requires include_account_names=true because it can probe customer names")
	}
	if s.restrictedAccountQuery(normalized.AccountQuery) {
		return toolCallResult{}, restrictedAccountQueryError()
	}
	baArgs.Filter = normalized
	limit := s.limitPolicy.BusinessAnalysisLimit(args.Limit)
	if normalized.Limit > 0 {
		limit = s.limitPolicy.BusinessAnalysisLimit(normalized.Limit)
		normalized.Limit = limit
		baArgs.Filter = normalized
	}
	topics := businessSignalTopics(operation, args)
	if len(topics) == 0 {
		return toolCallResult{}, fmt.Errorf("%s requires at least one topic seed", operation)
	}
	if s.store == nil {
		return newToolResult(map[string]any{
			"operation": operation,
			"status":    "store_unavailable",
			"warnings":  []string{"store_unavailable"},
		})
	}
	cohort, err := s.store.SearchBusinessAnalysisCalls(ctx, sqlite.BusinessAnalysisCallSearchParams{
		Filter: sqliteBusinessAnalysisFilter(normalized),
		Limit:  limit,
	})
	if err != nil {
		return toolCallResult{}, err
	}
	buckets := make([]businessSignalBucket, 0, len(topics))
	totalEvidence := 0
	perTopicLimit := limit
	if perTopicLimit > 5 {
		perTopicLimit = 5
	}
	for _, topic := range topics {
		items, quotes, err := s.businessAnalysisEvidence(ctx, normalized, topic, perTopicLimit, baArgs)
		if err != nil {
			return toolCallResult{}, err
		}
		bucket := businessSignalBucket{
			Topic:         topic,
			EvidenceCount: len(items),
			Evidence:      items,
			Quotes:        quotes,
		}
		totalEvidence += len(items)
		buckets = append(buckets, bucket)
	}
	warnings := businessAnalysisWarnings(operation, normalized)
	if speakerRole == sqlite.SpeakerRoleExternal {
		warnings = append(warnings, "speaker_role_filter_external: extraction returns buyer/customer/prospect speaker evidence only when cached speaker_role is available")
	} else if speakerRole == speakerRoleExternalOrUnknown {
		warnings = append(warnings, "speaker_role_filter_external_or_unknown: extraction excludes known internal speakers while preserving unattributed speaker rows")
	}
	if totalEvidence == 0 {
		warnings = append(warnings, "no_seeded_signal_evidence_returned: try narrower domain topics such as pricing, implementation, security review, ERP integration, or timeline")
	}
	status := "seeded_extraction_ready"
	if totalEvidence == 0 {
		status = "no_seeded_evidence"
	}
	return newToolResult(map[string]any{
		"operation":           operation,
		"status":              status,
		"searched_scope":      normalized,
		"field_profile":       baArgs.FieldProfile,
		"speaker_role_filter": speakerRole,
		"extraction_mode":     "seeded_topic_evidence",
		"topics":              topics,
		"coverage_summary":    businessAnalysisCoverageFromSummary(cohort.Summary),
		"cohort_summary":      cohort.Summary,
		"buckets":             buckets,
		"evidence_count":      totalEvidence,
		"warnings":            warnings,
		"limitations": append(businessAnalysisLimitations(operation),
			"seeded_extraction_not_full_semantic_classification",
			"question_mark_detection_is_not_required_for_buyer_question_buckets",
		),
		"suggested_followups": []string{
			"Rerun with the strongest topic as theme_query in theme_intelligence_report.",
			"Use evidence.call_drilldown on returned call_ref values for exact call context.",
		},
	})
}

func businessSignalTopics(operation string, args businessSignalExtractionArgs) []string {
	candidates := append([]string{}, args.Topics...)
	if query := firstNonBlank(args.Query, args.ThemeQuery); query != "" {
		candidates = append([]string{query}, candidates...)
	}
	if len(candidates) == 0 {
		switch operation {
		case OpExtractObjectionSignals:
			candidates = []string{"price", "budget", "timeline", "security review", "integration risk", "IT bandwidth", "ROI", "worried", "blocker", "competitor"}
		default:
			candidates = []string{"pricing", "implementation", "integration", "security", "support", "timeline", "data", "ERP", "punchout"}
		}
	}
	seen := make(map[string]struct{}, len(candidates))
	out := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		topic := strings.TrimSpace(candidate)
		if topic == "" {
			continue
		}
		key := strings.ToLower(topic)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, topic)
		if len(out) >= maxBusinessSignalTopics {
			break
		}
	}
	return out
}
