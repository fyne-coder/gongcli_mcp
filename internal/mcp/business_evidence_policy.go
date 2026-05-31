package mcp

import (
	"strings"

	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
)

const (
	evidenceTypeGongAICondensedCandidate = "gong_ai_condensed_candidate"
	evidenceTypeKeywordSynonym           = "deterministic_keyword_synonym"
	evidenceTypeTranscriptQuote          = "verbatim_transcript_quote"
	evidenceTypeDimensionRollup          = "dimension_rollup"
)

type businessEvidencePolicy struct {
	Name                    string   `json:"name"`
	ExcludeLifecycleBuckets []string `json:"exclude_lifecycle_buckets"`
	ExcludeLikelyVoicemail  bool     `json:"exclude_likely_voicemail"`
	DefaultSpeakerRole      string   `json:"default_speaker_role"`
	SeedlessPerCallCap      int      `json:"seedless_per_call_cap"`
	BroadThemeEvidenceFirst string   `json:"broad_theme_evidence_first"`
	TranscriptQuoteUse      string   `json:"transcript_quote_use"`
}

func defaultBusinessEvidencePolicy() businessEvidencePolicy {
	return businessEvidencePolicy{
		Name:                    "business_workbench_default",
		ExcludeLifecycleBuckets: []string{"outbound_prospecting"},
		ExcludeLikelyVoicemail:  true,
		DefaultSpeakerRole:      speakerRoleExternalOrUnknown,
		SeedlessPerCallCap:      3,
		BroadThemeEvidenceFirst: "gong_ai_brief_keypoints_highlights",
		TranscriptQuoteUse:      "customer_facing_claim_support",
	}
}

func businessEvidenceTypeForOperation(operation string, broadDiscovery bool) string {
	if broadDiscovery {
		return evidenceTypeGongAICondensedCandidate
	}
	switch operation {
	case OpExtractBuyerQuestions, OpExtractObjectionSignals:
		return evidenceTypeKeywordSynonym
	case "summarize_calls_by_filters", "summarize_themes_by_dimension", "compare_themes_over_time",
		"compare_themes_by_segment", "compare_theme_outcomes", "summarize_pipeline_progression_by_theme",
		"summarize_loss_reasons_by_theme", "compare_won_lost_theme_patterns", "summarize_themes_by_persona",
		"summarize_themes_by_industry", "rank_personas_by_insight_quality", "diagnose_attribution_coverage":
		return evidenceTypeDimensionRollup
	case "extract_theme_quotes", "search_quotes_in_cohort", "rank_quotes_for_sales_use", "build_quote_pack",
		"generate_sales_hooks_from_themes", "generate_outreach_sequence_inputs", "build_theme_brief",
		OpQuestionAnswer, OpProspectQuestionAnswer, OpThemeIntelReport, OpEvidenceQuotePackBuild, OpEvidenceQuotesSearch, OpEvidenceCallDrilldown:
		return evidenceTypeTranscriptQuote
	default:
		return ""
	}
}

func businessEvidenceAnswerContract(evidenceType string) []string {
	switch evidenceType {
	case evidenceTypeGongAICondensedCandidate:
		return []string{
			"Treat these as directional candidate themes from Gong AI condensed evidence, not final buyer-validated conclusions.",
			"Run a seeded theme_intelligence_report before making customer-facing claims.",
		}
	case evidenceTypeKeywordSynonym:
		return []string{
			"Treat rows as deterministic keyword/synonym matches, not full semantic classification.",
			"Unknown or affiliation_missing speaker evidence is unattributed and must not be phrased as buyer speech.",
		}
	case evidenceTypeTranscriptQuote:
		return []string{
			"Use returned bounded transcript quote evidence for customer-facing claims.",
			"Unknown or affiliation_missing speaker evidence is unattributed and must not be phrased as buyer speech.",
		}
	case evidenceTypeDimensionRollup:
		return []string{
			"Use dimension_readiness and data_readiness_caveats before making segment, stage, won/lost, loss-reason, or methodology claims.",
			"Do not infer unmapped loss reasons or methodology concepts.",
		}
	default:
		return []string{
			"Use only returned evidence and caveats; do not infer unavailable CRM or speaker attribution.",
		}
	}
}

func (p businessEvidencePolicy) applyFilter(filter callFilter) callFilter {
	if len(filter.ExcludeLifecycleBuckets) == 0 && strings.TrimSpace(filter.LifecycleBucket) == "" {
		filter.ExcludeLifecycleBuckets = append([]string(nil), p.ExcludeLifecycleBuckets...)
	}
	if p.ExcludeLikelyVoicemail {
		filter.ExcludeLikelyVoicemail = true
	}
	return filter
}

func (p businessEvidencePolicy) payload() map[string]any {
	return map[string]any{
		"name":                       p.Name,
		"exclude_lifecycle_buckets":  p.ExcludeLifecycleBuckets,
		"exclude_likely_voicemail":   p.ExcludeLikelyVoicemail,
		"default_speaker_role":       p.DefaultSpeakerRole,
		"seedless_per_call_cap":      p.SeedlessPerCallCap,
		"broad_theme_evidence_first": p.BroadThemeEvidenceFirst,
		"transcript_quote_use":       p.TranscriptQuoteUse,
		"unknown_speaker_contract":   "unknown or affiliation_missing evidence is unattributed evidence, not buyer-attributed speech",
		"host_display_policy": map[string]any{
			"default_mode":                 "business_summary",
			"tool_trace":                   "omit_unless_requested",
			"include_exact_mcp_operations": false,
			"include_runtime_identity":     "only_when_relevant_or_requested",
			"delta_language":               "show before/after counts and positive excluded_or_added counts; avoid negative deltas in business-user prose unless the metric is inherently signed",
		},
	}
}

func applyDefaultBroadThemeQualityFilters(filter callFilter) callFilter {
	return defaultBusinessEvidencePolicy().applyFilter(filter)
}

func addBusinessEvidenceMetadata(payload map[string]any, policy businessEvidencePolicy, evidenceType string, summary *sqlite.BusinessAnalysisCohortSummary, fieldProfile string, hasAIText bool, speakerSummary map[string]int) map[string]any {
	payload["evidence_policy"] = policy.payload()
	if evidenceType != "" {
		payload["evidence_type"] = evidenceType
		if _, exists := payload["answer_contract"]; !exists {
			payload["answer_contract"] = businessEvidenceAnswerContract(evidenceType)
		}
	}
	if len(speakerSummary) > 0 {
		payload["speaker_attribution_summary"] = speakerSummary
		if speakerSummary["affiliation_missing"] > 0 || speakerSummary["unknown"] > 0 {
			payload = appendPayloadWarning(payload, "speaker_attribution_unknown_present: unknown or affiliation_missing evidence must be described as unattributed evidence, not buyer speech")
		}
	}
	if summary != nil {
		payload["data_readiness_caveats"] = businessEvidenceDataReadinessCaveats(*summary)
		payload["dimension_readiness"] = businessEvidenceDimensionReadiness(*summary)
		for _, caveat := range businessEvidenceDataReadinessCaveats(*summary) {
			payload = appendPayloadWarning(payload, caveat)
		}
	}
	if fieldProfile == fieldProfileLimited && hasAIText {
		payload = appendPayloadWarning(payload, "limited_field_profile_does_not_redact_ai_condensed_evidence_text: field_profile=limited suppresses structured metadata, but Gong AI brief/keyPoint/highlight text may still contain names or customer terms")
	}
	return payload
}

func appendPayloadWarning(payload map[string]any, warning string) map[string]any {
	existing, _ := payload["warnings"].([]string)
	if existing == nil {
		if anyList, ok := payload["warnings"].([]any); ok {
			for _, item := range anyList {
				if s, ok := item.(string); ok {
					existing = append(existing, s)
				}
			}
		}
	}
	for _, current := range existing {
		if current == warning {
			payload["warnings"] = existing
			return payload
		}
	}
	payload["warnings"] = append(existing, warning)
	return payload
}

func businessEvidenceDataReadinessCaveats(summary sqlite.BusinessAnalysisCohortSummary) []string {
	var caveats []string
	if summary.CallCount > 0 && summary.CallCount < 5 {
		caveats = append(caveats, "sparse_cohort_directional_only: fewer than 5 calls matched; treat patterns as directional")
	}
	if summary.AccountIndustryCount == 0 {
		caveats = append(caveats, "account_industry_unavailable_or_unmapped")
	} else if ratio(summary.AccountIndustryCount, summary.CallCount) < 0.3 {
		caveats = append(caveats, "account_industry_degraded_sparse: fewer than 30 percent of calls have industry attribution")
	}
	if summary.ParticipantTitleCallCount == 0 {
		caveats = append(caveats, "participant_title_unavailable_or_unmapped")
	} else if ratio(summary.ParticipantTitleCallCount, summary.CallCount) < 0.3 {
		caveats = append(caveats, "participant_title_degraded_sparse: fewer than 30 percent of calls have participant title/persona attribution")
	}
	if summary.OpportunityStageCount == 0 {
		caveats = append(caveats, "opportunity_stage_unavailable_or_unmapped")
	} else if ratio(summary.OpportunityStageCount, summary.CallCount) < 0.3 {
		caveats = append(caveats, "opportunity_stage_degraded_sparse: fewer than 30 percent of calls have opportunity stage attribution")
	}
	caveats = append(caveats,
		"loss_reason_profile_dependent: do not infer loss reasons unless the customer profile maps and populates loss-reason fields",
		"methodology_profile_dependent: do not infer methodology concepts such as MEDDICC, pain, champion, or next steps unless mapped")
	return caveats
}

func businessEvidenceDimensionReadiness(summary sqlite.BusinessAnalysisCohortSummary) map[string]string {
	return map[string]string{
		"industry":          readinessFromCount(summary.AccountIndustryCount, summary.CallCount),
		"persona":           readinessFromCount(summary.ParticipantTitleCallCount, summary.CallCount),
		"opportunity_stage": readinessFromCount(summary.OpportunityStageCount, summary.CallCount),
		"won_lost":          readinessFromCount(summary.OpportunityStageCount, summary.CallCount),
		"loss_reason":       "unavailable_unmapped",
		"methodology":       "unavailable_unmapped",
	}
}

func readinessFromCount(count int64, total int64) string {
	if total <= 0 || count <= 0 {
		return "unavailable_unmapped"
	}
	if ratio(count, total) < 0.3 {
		return "degraded_sparse"
	}
	return "ready"
}

func ratio(count int64, total int64) float64 {
	if total <= 0 {
		return 0
	}
	return float64(count) / float64(total)
}

func speakerAttributionSummaryFromItems(items []businessAnalysisItem) map[string]int {
	summary := newSpeakerAttributionSummary()
	seenAttributionFields := false
	for _, item := range items {
		if strings.TrimSpace(item.SpeakerRole) == "" && strings.TrimSpace(item.SpeakerRoleStatus) == "" {
			continue
		}
		seenAttributionFields = true
		addSpeakerAttribution(summary, item.SpeakerRole, item.SpeakerRoleStatus)
	}
	if !seenAttributionFields {
		return nil
	}
	return summary
}

func speakerAttributionSummaryFromQuotes(quotes []businessAnalysisQuote) map[string]int {
	summary := newSpeakerAttributionSummary()
	for _, quote := range quotes {
		addSpeakerAttribution(summary, quote.SpeakerRole, quote.SpeakerRoleStatus)
	}
	return summary
}

func speakerAttributionSummaryFromThemeQuotes(quotes []themeIntelReportQuoteRow) map[string]int {
	summary := newSpeakerAttributionSummary()
	for _, quote := range quotes {
		addSpeakerAttribution(summary, quote.SpeakerRole, quote.SpeakerRoleStatus)
	}
	return summary
}

func speakerAttributionSummaryFromCallDrilldown(rows []callDrilldownTranscriptRow) map[string]int {
	summary := newSpeakerAttributionSummary()
	for _, row := range rows {
		addSpeakerAttribution(summary, row.SpeakerRole, row.SpeakerRoleStatus)
	}
	return summary
}

func speakerAttributionSummaryFromThemeDrilldown(rows []themeIntelReportTranscriptRow) map[string]int {
	summary := newSpeakerAttributionSummary()
	for _, row := range rows {
		addSpeakerAttribution(summary, row.SpeakerRole, row.SpeakerRoleStatus)
	}
	return summary
}

func newSpeakerAttributionSummary() map[string]int {
	return map[string]int{
		"external":            0,
		"internal":            0,
		"unknown":             0,
		"affiliation_missing": 0,
	}
}

func addSpeakerAttribution(summary map[string]int, role string, status string) {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case sqlite.SpeakerRoleExternal:
		summary["external"]++
	case sqlite.SpeakerRoleInternal:
		summary["internal"]++
	default:
		summary["unknown"]++
	}
	if strings.EqualFold(strings.TrimSpace(status), sqlite.SpeakerRoleStatusAffiliationMissing) {
		summary["affiliation_missing"]++
	}
}
