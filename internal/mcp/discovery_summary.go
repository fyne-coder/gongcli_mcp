package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
)

const (
	maxDiscoverySummarySuggestedSeeds = 10
	maxDiscoverySummarySelectedSeeds  = 5
	maxDiscoverySummaryPreviewSeeds   = 3
	maxDiscoverySummaryQuotesPerTheme = 3
)

type discoverySummaryArgs struct {
	Filter              callFilter `json:"filter"`
	CohortToken         string     `json:"cohort_token"`
	FromDate            string     `json:"from_date"`
	ToDate              string     `json:"to_date"`
	Quarter             string     `json:"quarter"`
	TitleQuery          string     `json:"title_query"`
	OpportunityStage    string     `json:"opportunity_stage"`
	ThemeQueries        []string   `json:"theme_queries"`
	ThemeQuery          string     `json:"theme_query"`
	OutputIntent        string     `json:"output_intent"`
	FieldProfile        string     `json:"field_profile"`
	SpeakerRole         string     `json:"speaker_role"`
	Limit               int        `json:"limit"`
	IncludeCallIDs      bool       `json:"include_call_ids"`
	IncludeCallTitles   bool       `json:"include_call_titles"`
	IncludeAccountNames bool       `json:"include_account_names"`
	IncludeSpeakerRefs  bool       `json:"include_speaker_refs"`
	IncludeRawIDs       bool       `json:"include_raw_ids"`
}

type discoveryThemeSummary struct {
	Theme          string           `json:"theme"`
	EvidenceCount  int              `json:"evidence_count"`
	Quotes         []map[string]any `json:"quotes"`
	PreviewLabel   string           `json:"preview_label,omitempty"`
	CohortDerived  bool             `json:"cohort_derived,omitempty"`
	SupportCount   int              `json:"support_count,omitempty"`
	EvidenceSource string           `json:"evidence_source,omitempty"`
}

func discoverySummaryNormalizeAliases(args *discoverySummaryArgs) {
	if args.FromDate != "" && strings.TrimSpace(args.Filter.FromDate) == "" {
		args.Filter.FromDate = args.FromDate
	}
	if args.ToDate != "" && strings.TrimSpace(args.Filter.ToDate) == "" {
		args.Filter.ToDate = args.ToDate
	}
	if args.Quarter != "" && strings.TrimSpace(args.Filter.Quarter) == "" {
		args.Filter.Quarter = args.Quarter
	}
	if args.TitleQuery != "" && strings.TrimSpace(args.Filter.TitleQuery) == "" {
		args.Filter.TitleQuery = args.TitleQuery
	}
	if args.OpportunityStage != "" && strings.TrimSpace(args.Filter.OpportunityStage) == "" {
		args.Filter.OpportunityStage = args.OpportunityStage
	}
}

func mergeDiscoverySeedTopicNames(staticSeeds []string, cohortCandidates []businessAnalysisTheme, max int) []string {
	if max <= 0 {
		max = maxDiscoverySummarySuggestedSeeds
	}
	seen := make(map[string]struct{}, max)
	out := make([]string, 0, max)
	add := func(theme string) {
		theme = strings.TrimSpace(theme)
		if theme == "" {
			return
		}
		key := strings.ToLower(theme)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, theme)
	}
	sortedCandidates := append([]businessAnalysisTheme(nil), cohortCandidates...)
	sort.SliceStable(sortedCandidates, func(i, j int) bool {
		if sortedCandidates[i].SupportCount == sortedCandidates[j].SupportCount {
			return sortedCandidates[i].Theme < sortedCandidates[j].Theme
		}
		return sortedCandidates[i].SupportCount > sortedCandidates[j].SupportCount
	})
	for _, candidate := range sortedCandidates {
		add(candidate.Theme)
		if len(out) >= max {
			return out
		}
	}
	for _, seed := range staticSeeds {
		add(seed)
		if len(out) >= max {
			break
		}
	}
	return out
}

func pickDiscoveryPreviewSeeds(suggested []string, cohortCandidates []businessAnalysisTheme, max int) []string {
	if max <= 0 {
		max = maxDiscoverySummaryPreviewSeeds
	}
	supportByTheme := make(map[string]int, len(cohortCandidates))
	for _, candidate := range cohortCandidates {
		supportByTheme[strings.ToLower(strings.TrimSpace(candidate.Theme))] = candidate.SupportCount
	}
	staticSafe := make(map[string]struct{}, len(questionAnswerSuggestedSeedTopics()))
	for _, seed := range questionAnswerSuggestedSeedTopics() {
		staticSafe[strings.ToLower(strings.TrimSpace(seed))] = struct{}{}
	}
	type ranked struct {
		theme   string
		support int
		static  bool
	}
	rankedSeeds := make([]ranked, 0, len(suggested))
	for _, theme := range suggested {
		theme = strings.TrimSpace(theme)
		if theme == "" {
			continue
		}
		key := strings.ToLower(theme)
		_, isStatic := staticSafe[key]
		if !isStatic && supportByTheme[key] == 0 {
			continue
		}
		rankedSeeds = append(rankedSeeds, ranked{
			theme:   theme,
			support: supportByTheme[key],
			static:  isStatic,
		})
	}
	sort.SliceStable(rankedSeeds, func(i, j int) bool {
		if rankedSeeds[i].support == rankedSeeds[j].support {
			if rankedSeeds[i].static == rankedSeeds[j].static {
				return rankedSeeds[i].theme < rankedSeeds[j].theme
			}
			return rankedSeeds[i].static
		}
		return rankedSeeds[i].support > rankedSeeds[j].support
	})
	out := make([]string, 0, max)
	seen := make(map[string]struct{}, max)
	for _, item := range rankedSeeds {
		key := strings.ToLower(item.theme)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item.theme)
		if len(out) >= max {
			break
		}
	}
	if len(out) > 0 {
		return out
	}
	for _, seed := range questionAnswerSuggestedSeedTopics() {
		key := strings.ToLower(strings.TrimSpace(seed))
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, seed)
		if len(out) >= max {
			break
		}
	}
	return out
}

func discoverySummaryExplicitSeeds(args discoverySummaryArgs) []string {
	seeds := append([]string{}, args.ThemeQueries...)
	if query := strings.TrimSpace(args.ThemeQuery); query != "" {
		seeds = append([]string{query}, seeds...)
	}
	return normalizeBusinessSignalQueries(seeds, maxDiscoverySummarySelectedSeeds)
}

func discoverySummaryQuoteRows(quotes []businessAnalysisQuote, items []businessAnalysisItem, includeRaw, includeTitles, includeAccounts, includeSpeakerRefs bool) []map[string]any {
	out := make([]map[string]any, 0, len(quotes))
	for i, quote := range quotes {
		var item businessAnalysisItem
		if i < len(items) {
			item = items[i]
		}
		row := map[string]any{
			"call_ref":               quote.CallRef,
			"segment_index":          quote.SegmentIndex,
			"snippet":                quote.Excerpt,
			"person_title_status":    quote.PersonTitleStatus,
			"attribution_source":     defaultAttribution(quote.PersonTitleStatus),
			"attribution_confidence": defaultAttributionConfidence(quote.PersonTitleStatus),
			"speaker_role":           firstNonBlank(quote.SpeakerRole, sqlite.SpeakerRoleUnknown),
			"speaker_role_status":    firstNonBlank(quote.SpeakerRoleStatus, sqlite.SpeakerRoleStatusAffiliationMissing),
		}
		if includeRaw && quote.CallID != "" {
			row["call_id"] = quote.CallID
		}
		if includeTitles && quote.CallTitle != "" {
			row["call_title"] = quote.CallTitle
		}
		if includeAccounts && quote.AccountName != "" {
			row["account_name"] = quote.AccountName
		}
		if includeSpeakerRefs && item.CallRef != "" {
			row["speaker_ref"] = stableEvidenceRef("speaker", item.CallRef+":"+fmtSegment(item.SegmentIndex))
		}
		out = append(out, row)
	}
	return out
}

func (s *Server) buildBoundedSeededThemeSummaries(ctx context.Context, filter callFilter, seeds []string, perThemeLimit int, baArgs businessAnalysisArgs, includeRaw, includeTitles, includeAccounts, includeSpeakerRefs bool, previewLabel string) ([]discoveryThemeSummary, []businessAnalysisItem, error) {
	if perThemeLimit <= 0 {
		perThemeLimit = maxDiscoverySummaryQuotesPerTheme
	}
	if perThemeLimit > maxDiscoverySummaryQuotesPerTheme {
		perThemeLimit = maxDiscoverySummaryQuotesPerTheme
	}
	summaries := make([]discoveryThemeSummary, 0, len(seeds))
	allItems := make([]businessAnalysisItem, 0)
	for _, seed := range seeds {
		expandedQueries := businessSignalTopicQueries(OpAnalyzeDiscoverySummary, seed, defaultTopicPackSet())
		items, quotes, err := s.businessAnalysisEvidenceForTopicQueries(ctx, filter, expandedQueries, perThemeLimit, baArgs)
		if err != nil {
			return nil, nil, err
		}
		if len(items) == 0 {
			continue
		}
		summary := discoveryThemeSummary{
			Theme:          seed,
			EvidenceCount:  len(items),
			Quotes:         discoverySummaryQuoteRows(quotes, items, includeRaw, includeTitles, includeAccounts, includeSpeakerRefs),
			EvidenceSource: evidenceTypeTranscriptQuote,
		}
		if previewLabel != "" {
			summary.PreviewLabel = previewLabel
		}
		summaries = append(summaries, summary)
		allItems = append(allItems, items...)
	}
	return summaries, allItems, nil
}

func (s *Server) executeDiscoverySummary(ctx context.Context, raw json.RawMessage) (toolCallResult, error) {
	var args discoverySummaryArgs
	if err := decodeArgs(raw, &args); err != nil {
		return toolCallResult{}, err
	}
	if s.governanceActive() {
		return toolCallResult{}, governanceFilteredAggregateError(OpAnalyzeDiscoverySummary)
	}
	discoverySummaryNormalizeAliases(&args)
	if s.restrictedAccountQuery(args.Filter.AccountQuery) {
		return toolCallResult{}, restrictedAccountQueryError()
	}
	profiled, err := applyFieldProfile(args.FieldProfile, fieldProfileApplication{
		IncludeRawIDs:       args.IncludeRawIDs || args.IncludeCallIDs,
		IncludeCallTitles:   true,
		IncludeAccountNames: args.IncludeAccountNames,
		IncludeSpeakerRefs:  args.IncludeSpeakerRefs,
	})
	if err != nil {
		return toolCallResult{}, err
	}
	args.FieldProfile = profiled.Profile
	args.IncludeRawIDs = profiled.IncludeRawIDs
	args.IncludeCallIDs = profiled.IncludeRawIDs
	args.IncludeCallTitles = profiled.IncludeCallTitles
	args.IncludeAccountNames = profiled.IncludeAccountNames
	args.IncludeSpeakerRefs = profiled.IncludeSpeakerRefs
	if strings.TrimSpace(args.Filter.AccountQuery) != "" && !args.IncludeAccountNames {
		return toolCallResult{}, fmt.Errorf("account_query requires include_account_names=true because it can probe customer names")
	}
	speakerRoleFilter, err := normalizeSpeakerRoleFilter(firstNonBlank(args.SpeakerRole, speakerRoleExternalOrUnknown))
	if err != nil {
		return toolCallResult{}, err
	}
	normalized, err := resolveCohortFilter(args.Filter, args.CohortToken)
	if err != nil {
		return toolCallResult{}, err
	}
	policy := defaultBusinessEvidencePolicy()
	normalized = policy.applyFilter(normalized)
	if !businessAnalysisFilterIsSelective(normalized) {
		return toolCallResult{}, fmt.Errorf("%s requires a selective filter such as date range, quarter, title_query, query, industry, opportunity_stage, crm_object_id, participant_title_query, or lifecycle_bucket", OpAnalyzeDiscoverySummary)
	}
	limit := s.limitPolicy.BusinessAnalysisLimit(args.Limit)
	if normalized.Limit > 0 {
		limit = s.limitPolicy.BusinessAnalysisLimit(normalized.Limit)
		normalized.Limit = limit
	}
	cohortIDValue, cohortTokenValue, err := attachCohortHandoff(normalized, "")
	if err != nil {
		return toolCallResult{}, err
	}
	includeRaw := args.IncludeRawIDs && !s.policySwitches.HideRawCallIDs
	includeTitles := args.IncludeCallTitles && !s.policySwitches.HideCallTitles
	includeAccounts := args.IncludeAccountNames && !s.policySwitches.HideAccountNames

	warnings := businessAnalysisWarnings(OpAnalyzeDiscoverySummary, normalized)
	limitations := append(businessAnalysisLimitations(OpAnalyzeDiscoverySummary),
		"discovery_summary_not_exhaustive_theme_ranking",
		"auto_selected_seeds_and_seeded_preview_are_directional_until_quote_evidence_is_returned",
	)
	answerContract := []string{
		"Use bounded quote evidence for customer-facing claims.",
		"Treat suggested_seed_topics, cohort-derived candidates, auto-selected theme_summaries, and seeded_preview as directional guidance, not final buyer-validated themes.",
		"Unknown or affiliation_missing speaker evidence is unattributed and must not be phrased as buyer speech.",
	}

	if s.store == nil {
		return newToolResult(map[string]any{
			"operation":         OpAnalyzeDiscoverySummary,
			"status":            "store_unavailable",
			"normalized_filter": normalized,
			"cohort_id":         cohortIDValue,
			"cohort_token":      cohortTokenValue,
			"coverage_summary":  map[string]any{},
			"warnings":          append(warnings, "store_unavailable"),
			"limitations":       limitations,
			"answer_contract":   answerContract,
		})
	}

	cohort, err := s.store.SearchBusinessAnalysisCalls(ctx, sqlite.BusinessAnalysisCallSearchParams{
		Filter: sqliteBusinessAnalysisFilter(normalized),
		Limit:  limit,
	})
	if err != nil {
		return toolCallResult{}, err
	}
	filteredRows := filterThemeIntelCohortRows(cohort.Rows, normalized)
	briefSummary, err := s.aiBusinessBriefThemeSummaryForSeeds(ctx, filteredRows, includeRaw, maxThemeIntelReportThemes, questionAnswerSuggestedSeedTopics())
	if err != nil {
		return toolCallResult{}, err
	}

	explicitSeeds := discoverySummaryExplicitSeeds(args)
	autoMode := len(explicitSeeds) == 0
	suggestedSeeds := mergeDiscoverySeedTopicNames(questionAnswerSuggestedSeedTopics(), briefSummary.Candidates, maxDiscoverySummarySuggestedSeeds)
	selectedSeeds := explicitSeeds
	if autoMode {
		selectedSeeds = pickDiscoveryPreviewSeeds(suggestedSeeds, briefSummary.Candidates, maxDiscoverySummarySelectedSeeds)
	} else {
		selectedSeeds = normalizeBusinessSignalQueries(selectedSeeds, maxDiscoverySummarySelectedSeeds)
	}

	baArgs := businessAnalysisArgs{
		Filter:              normalized,
		Limit:               limit,
		IncludeCallIDs:      includeRaw,
		IncludeCallTitles:   includeTitles,
		IncludeAccountNames: includeAccounts,
		FieldProfile:        args.FieldProfile,
		SpeakerRole:         speakerRoleFilter,
	}

	previewSeeds := selectedSeeds
	if autoMode {
		previewSeeds = pickDiscoveryPreviewSeeds(suggestedSeeds, briefSummary.Candidates, maxDiscoverySummaryPreviewSeeds)
	}
	previewLabel := ""
	summaryLabel := ""
	if autoMode {
		summaryLabel = "directional_auto_selected_seed_evidence_not_exhaustive_theme_ranking"
		previewLabel = "directional_seeded_preview_not_exhaustive_theme_ranking"
		warnings = append(warnings, "discovery_summary_auto_seeded_preview: selected seeds, theme_summaries, and preview are directional; they are not an exhaustive or frequency-ranked theme analysis")
	}
	themeSummaries, evidenceItems, err := s.buildBoundedSeededThemeSummaries(ctx, normalized, selectedSeeds, maxDiscoverySummaryQuotesPerTheme, baArgs, includeRaw, includeTitles, includeAccounts, args.IncludeSpeakerRefs, summaryLabel)
	if err != nil {
		return toolCallResult{}, err
	}
	seededPreview, previewItems, err := s.buildBoundedSeededThemeSummaries(ctx, normalized, previewSeeds, maxDiscoverySummaryQuotesPerTheme, baArgs, includeRaw, includeTitles, includeAccounts, args.IncludeSpeakerRefs, previewLabel)
	if err != nil {
		return toolCallResult{}, err
	}
	evidenceItems = append(evidenceItems, previewItems...)

	if autoMode && len(briefSummary.Candidates) > 0 {
		warnings = append(warnings, "discovery_summary_cohort_derived_candidates: cohort-derived seed suggestions come from Gong AI brief/keyPoint/highlight rows and are directional until rerun with explicit theme_queries")
	}
	if cohort.Summary.ParticipantTitleCallCount == 0 {
		warnings = append(warnings, "participant_title_missing_or_unmapped")
	}
	if cohort.Summary.AccountIndustryCount == 0 {
		warnings = append(warnings, "account_industry_missing_or_unmapped")
	}
	if cohort.Summary.OpportunityStageCount == 0 {
		warnings = append(warnings, "opportunity_stage_missing_or_unmapped")
	}

	status := "discovery_summary_ready"
	evidenceType := evidenceTypeTranscriptQuote
	if cohort.Summary.CallCount == 0 {
		status = "empty_cohort"
		warnings = append(warnings, "empty_cohort: no calls matched the normalized filter")
	} else if autoMode {
		if len(themeSummaries) == 0 && len(briefSummary.Candidates) == 0 {
			status = "needs_theme_seed"
			evidenceType = evidenceTypeGongAICondensedCandidate
			warnings = append(warnings, "discovery_summary_needs_theme_seed: no cohort-derived candidates or quote evidence were found; choose an explicit theme_query/theme_queries seed")
		} else if len(themeSummaries) == 0 {
			status = "directional_seed_guidance"
			evidenceType = evidenceTypeGongAICondensedCandidate
		} else {
			status = "directional_discovery_preview"
		}
	} else if len(themeSummaries) == 0 {
		status = "no_seeded_evidence"
	}

	payload := map[string]any{
		"operation":             OpAnalyzeDiscoverySummary,
		"status":                status,
		"output_intent":         strings.TrimSpace(args.OutputIntent),
		"normalized_filter":     normalized,
		"cohort_id":             cohortIDValue,
		"cohort_token":          cohortTokenValue,
		"field_profile":         args.FieldProfile,
		"speaker_role_filter":   speakerRoleFilter,
		"coverage_summary":      businessAnalysisCoverageFromSummary(cohort.Summary),
		"cohort_summary":        cohort.Summary,
		"suggested_seed_topics": suggestedSeeds,
		"selected_seed_topics":  selectedSeeds,
		"theme_summaries":       themeSummaries,
		"seeded_preview":        seededPreview,
		"cohort_derived_seeds":  briefSummary.Candidates,
		"warnings":              warnings,
		"limitations":           limitations,
		"answer_contract":       answerContract,
		"suggested_followups":   discoverySummaryFollowups(args.OutputIntent, autoMode),
	}
	addBusinessEvidenceMetadata(payload, policy, evidenceType, &cohort.Summary, args.FieldProfile, len(briefSummary.EvidenceByTheme) > 0, speakerAttributionSummaryFromItems(evidenceItems))
	return newToolResult(payload)
}

func discoverySummaryFollowups(outputIntent string, autoMode bool) []string {
	if autoMode {
		return []string{
			"Rerun analyze.discovery_summary with theme_queries for the strongest suggested_seed_topics to deepen quote evidence.",
			"Run theme_intelligence_report with one chosen theme_query for rollups and drilldown workflow inputs.",
		}
	}
	switch strings.ToLower(strings.TrimSpace(outputIntent)) {
	case "themes_only":
		return []string{
			"Compare the strongest theme across persona or industry using theme_intelligence_report dimension rollups.",
		}
	default:
		return []string{
			"Use evidence.call_drilldown on a returned call_ref for call-level context.",
			"Build a quote pack with evidence.quote_pack.build once the strongest theme is confirmed.",
		}
	}
}
