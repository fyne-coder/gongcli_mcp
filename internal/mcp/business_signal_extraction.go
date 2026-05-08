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
	Topic           string                  `json:"topic"`
	ExpandedQueries []string                `json:"expanded_queries,omitempty"`
	EvidenceCount   int                     `json:"evidence_count"`
	Evidence        []businessAnalysisItem  `json:"evidence"`
	Quotes          []businessAnalysisQuote `json:"quotes"`
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
	normalized = applyDefaultBroadThemeQualityFilters(normalized)
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
	allEvidence := make([]businessAnalysisItem, 0)
	perTopicLimit := limit
	if perTopicLimit > 5 {
		perTopicLimit = 5
	}
	for _, topic := range topics {
		expandedQueries := businessSignalTopicQueries(operation, topic)
		items, quotes, err := s.businessAnalysisEvidenceForTopicQueries(ctx, normalized, expandedQueries, perTopicLimit, baArgs)
		if err != nil {
			return toolCallResult{}, err
		}
		bucket := businessSignalBucket{
			Topic:           topic,
			ExpandedQueries: expandedQueries,
			EvidenceCount:   len(items),
			Evidence:        items,
			Quotes:          quotes,
		}
		totalEvidence += len(items)
		allEvidence = append(allEvidence, items...)
		buckets = append(buckets, bucket)
	}
	warnings := businessAnalysisWarnings(operation, normalized)
	warnings = append(warnings, "business_signal_default_quality_filters: extraction defaults exclude outbound_prospecting and likely voicemail/IVR calls; override with an explicit lifecycle_bucket or exclusion list when reviewing prospecting workflows")
	if speakerRole == sqlite.SpeakerRoleExternal {
		warnings = append(warnings, "speaker_role_filter_external: extraction returns buyer/customer/prospect speaker evidence only when cached speaker_role is available")
	} else if speakerRole == speakerRoleExternalOrUnknown {
		warnings = append(warnings, "speaker_role_filter_external_or_unknown: extraction excludes known internal speakers while preserving unattributed speaker rows")
	}
	if totalEvidence == 0 {
		warnings = append(warnings, "no_seeded_signal_evidence_returned: try narrower domain topics such as pricing, implementation, security review, ERP integration, or timeline")
	}
	warnings = append(warnings, "seeded_topic_synonym_expansion: topic buckets transparently try common TC sales/marketing synonyms and expose expanded_queries")
	status := "seeded_extraction_ready"
	if totalEvidence == 0 {
		status = "no_seeded_evidence"
	}
	payload := map[string]any{
		"operation":           operation,
		"status":              status,
		"searched_scope":      normalized,
		"field_profile":       baArgs.FieldProfile,
		"speaker_role_filter": speakerRole,
		"evidence_type":       evidenceTypeKeywordSynonym,
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
	}
	addBusinessEvidenceMetadata(payload, defaultBusinessEvidencePolicy(), evidenceTypeKeywordSynonym, &cohort.Summary, baArgs.FieldProfile, false, speakerAttributionSummaryFromItems(allEvidence))
	return newToolResult(payload)
}

func (s *Server) businessAnalysisEvidenceForTopicQueries(ctx context.Context, filter callFilter, queries []string, limit int, args businessAnalysisArgs) ([]businessAnalysisItem, []businessAnalysisQuote, error) {
	if limit <= 0 {
		limit = 5
	}
	seen := make(map[string]struct{})
	itemsOut := make([]businessAnalysisItem, 0, limit)
	quotesOut := make([]businessAnalysisQuote, 0, limit)
	for _, query := range normalizeBusinessSignalQueries(queries, maxBusinessSignalTopics) {
		if len(itemsOut) >= limit {
			break
		}
		items, quotes, err := s.businessAnalysisEvidence(ctx, filter, query, limit, args)
		if err != nil {
			return nil, nil, err
		}
		for i, item := range items {
			if len(itemsOut) >= limit {
				break
			}
			if filter.ExcludeLikelyVoicemail && businessAnalysisItemLooksLikeVoicemail(item) {
				continue
			}
			key := businessSignalEvidenceKey(item)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			itemsOut = append(itemsOut, item)
			if i < len(quotes) {
				quotesOut = append(quotesOut, quotes[i])
			}
		}
	}
	return itemsOut, quotesOut, nil
}

func businessAnalysisItemLooksLikeVoicemail(item businessAnalysisItem) bool {
	text := strings.ToLower(strings.TrimSpace(item.Snippet + " " + item.ContextExcerpt))
	if text == "" {
		return false
	}
	if businessBriefLooksLikeVoicemail(text) {
		return true
	}
	ivrPhrases := []string{
		"customer support center",
		"support center is open",
		"recorded for quality",
		"quality and training purposes",
		"transferring you to",
		"please hold while",
		"next available representative",
		"next available agent",
	}
	hits := 0
	for _, phrase := range ivrPhrases {
		if strings.Contains(text, phrase) {
			hits++
		}
	}
	return hits >= 2 || (hits >= 1 && strings.Contains(text, "customer support"))
}

func businessSignalEvidenceKey(item businessAnalysisItem) string {
	return strings.Join([]string{
		strings.TrimSpace(item.CallRef),
		strings.TrimSpace(item.CallID),
		fmt.Sprintf("%d", item.SegmentIndex),
		strings.TrimSpace(item.Snippet),
	}, "\x00")
}

func businessSignalTopicQueries(operation string, topic string) []string {
	base := strings.TrimSpace(topic)
	if base == "" {
		return nil
	}
	queries := []string{base}
	queries = append(queries, businessSignalTopicAliases(operation, base)...)
	return normalizeBusinessSignalQueries(queries, maxBusinessSignalTopics)
}

func businessSignalTopicAliases(operation string, topic string) []string {
	key := strings.ToLower(strings.TrimSpace(topic))
	key = strings.Join(strings.Fields(key), " ")
	aliases := map[string][]string{
		"implementation":        {"implementation timeline", "implementation plan", "rollout", "deployment", "go live", "launch"},
		"implementation effort": {"implementation timeline", "implementation plan", "rollout effort", "deployment effort", "IT bandwidth", "resource constraints"},
		"integration":           {"ERP integration", "system integration", "API integration", "punchout integration"},
		"integration risk":      {"ERP integration", "integration timeline", "integration effort", "API support", "technical lift", "IT bandwidth"},
		"erp integration":       {"ERP", "integrate with ERP", "direct ERP", "SAP integration", "Oracle integration", "NetSuite integration"},
		"security":              {"security review", "security questionnaire", "infosec", "information security", "compliance review", "risk review"},
		"security review":       {"security", "security questionnaire", "infosec", "information security", "compliance review", "risk review"},
		"pricing":               {"price", "budget", "cost", "investment", "pricing model", "quote"},
		"price":                 {"pricing", "budget", "cost", "investment", "quote"},
		"budget":                {"pricing", "price", "cost", "investment", "funding"},
		"roi":                   {"ROI", "return on investment", "business case", "value", "payback", "justify"},
		"punchout":              {"punchout integration", "punch out", "eprocurement", "Coupa", "Ariba", "Jaggaer"},
		"supplier onboarding":   {"supplier enablement", "vendor onboarding", "trading relationship", "supplier setup", "supplier adoption"},
		"timeline":              {"implementation timeline", "go live", "launch date", "rollout", "schedule"},
		"support":               {"customer support", "post implementation support", "training", "enablement", "help desk"},
	}
	out := append([]string{}, aliases[key]...)
	if operation == OpExtractObjectionSignals {
		switch key {
		case "implementation", "implementation effort":
			out = append(out, "too much work", "resource constraints", "IT capacity")
		case "integration", "integration risk", "erp integration":
			out = append(out, "concerned about integration", "integration complexity", "technical risk")
		case "security", "security review":
			out = append(out, "security concern", "compliance concern", "review process")
		}
	}
	return out
}

func normalizeBusinessSignalQueries(queries []string, max int) []string {
	if max <= 0 {
		max = maxBusinessSignalTopics
	}
	seen := make(map[string]struct{}, len(queries))
	out := make([]string, 0, len(queries))
	for _, query := range queries {
		value := strings.TrimSpace(query)
		if value == "" {
			continue
		}
		key := strings.ToLower(strings.Join(strings.Fields(value), " "))
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
		if len(out) >= max {
			break
		}
	}
	return out
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
