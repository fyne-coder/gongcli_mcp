package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
)

const (
	defaultBusinessAnalysisLimit       = 25
	maxBusinessAnalysisLimit           = 1000
	maxBusinessAnalysisFTSQueryLength  = 160
	maxBusinessAnalysisFTSQueryTerms   = 12
	maxBusinessAnalysisFTSQueryTermLen = 48
)

func (s *Store) SearchTranscriptSegmentsByCallFacts(ctx context.Context, params sqlite.TranscriptCallFactsSearchParams) ([]sqlite.TranscriptCallFactsSearchResult, error) {
	queryText := strings.TrimSpace(params.Query)
	if queryText == "" {
		return nil, errors.New("search query is required")
	}
	if err := validatePostgresBusinessAnalysisSearchText(queryText, "query"); err != nil {
		return nil, err
	}
	fromDate, toDate, err := postgresNormalizeDateRange(params.FromDate, params.ToDate)
	if err != nil {
		return nil, err
	}
	lifecycleBucket, err := postgresNormalizeLifecycleBucket(params.LifecycleBucket)
	if err != nil {
		return nil, err
	}
	scope, err := postgresNormalizeOptionalScope(params.Scope)
	if err != nil {
		return nil, err
	}
	limit := boundedLimit(params.Limit, defaultTranscriptSearchLimit, maxTranscriptSearchLimit)
	rows, err := s.db.QueryContext(ctx, `SELECT call_id, started_at, call_date, call_month, duration_seconds, lifecycle_bucket, scope, system, direction, speaker_id, segment_index, start_ms, end_ms, snippet, context_excerpt
  FROM `+s.postgresTranscriptSegmentsByCallFactsFunction()+`($1, $2, $3, $4, $5, $6, $7, $8)`,
		queryText,
		fromDate,
		toDate,
		lifecycleBucket,
		scope,
		strings.TrimSpace(params.System),
		strings.TrimSpace(params.Direction),
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []sqlite.TranscriptCallFactsSearchResult{}
	for rows.Next() {
		var row sqlite.TranscriptCallFactsSearchResult
		if err := rows.Scan(&row.CallID, &row.StartedAt, &row.CallDate, &row.CallMonth, &row.DurationSeconds, &row.LifecycleBucket, &row.Scope, &row.System, &row.Direction, &row.SpeakerID, &row.SegmentIndex, &row.StartMS, &row.EndMS, &row.Snippet, &row.ContextExcerpt); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) SearchTranscriptQuotesWithAttribution(ctx context.Context, params sqlite.TranscriptAttributionSearchParams) ([]sqlite.TranscriptAttributionSearchResult, error) {
	queryText := strings.TrimSpace(params.Query)
	if queryText == "" {
		return nil, errors.New("search query is required")
	}
	if err := validatePostgresBusinessAnalysisSearchText(queryText, "query"); err != nil {
		return nil, err
	}
	fromDate, toDate, err := postgresNormalizeDateRange(params.FromDate, params.ToDate)
	if err != nil {
		return nil, err
	}
	lifecycleBucket, err := postgresNormalizeLifecycleBucket(params.LifecycleBucket)
	if err != nil {
		return nil, err
	}
	scope, err := postgresNormalizeOptionalScope(params.Scope)
	if err != nil {
		return nil, err
	}
	transcriptStatus, err := postgresNormalizeOptionalTranscriptStatus(params.TranscriptStatus)
	if err != nil {
		return nil, err
	}
	if transcriptStatus == "missing" {
		return nil, errors.New("transcript_status=missing is not supported for transcript quote search")
	}
	if strings.TrimSpace(params.AccountQuery) != "" && !s.readOnlyOptions.AllowAccountQuery {
		return nil, errors.New("account_query is not supported by the Postgres analyst-business-core reader because it can probe customer names")
	}
	limit := boundedLimit(params.Limit, defaultTranscriptSearchLimit, maxTranscriptSearchLimit)
	rows, err := s.db.QueryContext(ctx, `SELECT call_id, title, started_at, call_date, lifecycle_bucket, account_name, account_industry, account_website, opportunity_name, opportunity_stage, opportunity_type, opportunity_close_date, opportunity_probability, participant_status, person_title_status, person_title_source, segment_index, start_ms, end_ms, snippet, context_excerpt
  FROM `+s.postgresTranscriptQuoteAttributionFunction()+`($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		queryText,
		fromDate,
		toDate,
		lifecycleBucket,
		scope,
		strings.TrimSpace(params.System),
		strings.TrimSpace(params.Direction),
		transcriptStatus,
		strings.TrimSpace(params.Industry),
		strings.TrimSpace(params.AccountQuery),
		strings.TrimSpace(params.OpportunityStage),
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []sqlite.TranscriptAttributionSearchResult{}
	for rows.Next() {
		var row sqlite.TranscriptAttributionSearchResult
		if err := rows.Scan(&row.CallID, &row.Title, &row.StartedAt, &row.CallDate, &row.LifecycleBucket, &row.AccountName, &row.AccountIndustry, &row.AccountWebsite, &row.OpportunityName, &row.OpportunityStage, &row.OpportunityType, &row.OpportunityCloseDate, &row.OpportunityProbability, &row.ParticipantStatus, &row.PersonTitleStatus, &row.PersonTitleSource, &row.SegmentIndex, &row.StartMS, &row.EndMS, &row.Snippet, &row.ContextExcerpt); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) SearchBusinessAnalysisCalls(ctx context.Context, params sqlite.BusinessAnalysisCallSearchParams) (*sqlite.BusinessAnalysisCallSearchResult, error) {
	filter, err := s.normalizePostgresBusinessAnalysisFilter(params.Filter)
	if err != nil {
		return nil, err
	}
	limit := boundedLimit(firstPositivePostgres(params.Limit, filter.Limit), defaultBusinessAnalysisLimit, maxBusinessAnalysisLimit)
	filter.Limit = limit
	summary, err := s.postgresBusinessAnalysisSummary(ctx, filter)
	if err != nil {
		return nil, err
	}
	rows, err := s.postgresBusinessAnalysisCallRows(ctx, filter, limit)
	if err != nil {
		return nil, err
	}
	return &sqlite.BusinessAnalysisCallSearchResult{Filter: filter, Summary: summary, Rows: rows}, nil
}

func (s *Store) SearchBusinessAnalysisEvidence(ctx context.Context, params sqlite.BusinessAnalysisEvidenceSearchParams) ([]sqlite.BusinessAnalysisEvidenceRow, error) {
	filter, err := s.normalizePostgresBusinessAnalysisFilter(params.Filter)
	if err != nil {
		return nil, err
	}
	limit := boundedLimit(firstPositivePostgres(params.Limit, filter.Limit), defaultBusinessAnalysisLimit, maxBusinessAnalysisLimit)
	queryText := strings.TrimSpace(firstNonEmptyPostgres(params.Query, filter.Query))
	if err := validatePostgresBusinessAnalysisSearchText(queryText, "query"); err != nil {
		return nil, err
	}
	if queryText == "" {
		return nil, errors.New("query is required for business-analysis evidence searches")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT call_id, title, started_at, call_date, call_month, lifecycle_bucket, account_industry, account_name, opportunity_name, opportunity_stage, opportunity_type, opportunity_probability, opportunity_close_date, participant_status, person_title_status, person_title_source, segment_index, start_ms, end_ms, snippet, context_excerpt
  FROM `+s.postgresBusinessAnalysisEvidenceFunction()+`($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)`,
		queryText,
		filter.TitleQuery,
		filter.Query,
		filter.FromDate,
		filter.ToDate,
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
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []sqlite.BusinessAnalysisEvidenceRow{}
	for rows.Next() {
		var row sqlite.BusinessAnalysisEvidenceRow
		if err := rows.Scan(&row.CallID, &row.Title, &row.StartedAt, &row.CallDate, &row.CallMonth, &row.LifecycleBucket, &row.AccountIndustry, &row.AccountName, &row.OpportunityName, &row.OpportunityStage, &row.OpportunityType, &row.OpportunityProbability, &row.OpportunityCloseDate, &row.ParticipantStatus, &row.PersonTitleStatus, &row.PersonTitleSource, &row.SegmentIndex, &row.StartMS, &row.EndMS, &row.Snippet, &row.ContextExcerpt); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) SummarizeBusinessAnalysisDimension(ctx context.Context, params sqlite.BusinessAnalysisDimensionSummaryParams) ([]sqlite.BusinessAnalysisDimensionRow, error) {
	filter, err := s.normalizePostgresBusinessAnalysisFilter(params.Filter)
	if err != nil {
		return nil, err
	}
	dimension, err := postgresBusinessAnalysisDimension(params.Dimension)
	if err != nil {
		return nil, err
	}
	themeQuery := strings.TrimSpace(params.ThemeQuery)
	if err := validatePostgresBusinessAnalysisSearchText(themeQuery, "theme_query"); err != nil {
		return nil, err
	}
	limit := boundedLimit(firstPositivePostgres(params.Limit, filter.Limit), defaultBusinessAnalysisLimit, maxBusinessAnalysisLimit)
	rows, err := s.db.QueryContext(ctx, `SELECT dimension, value, call_count, transcript_count, missing_transcript_count, opportunity_call_count, account_call_count, external_call_count, latest_call_at
  FROM gongmcp_business_analysis_dimension($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)`,
		dimension,
		themeQuery,
		filter.TitleQuery,
		filter.Query,
		filter.FromDate,
		filter.ToDate,
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
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []sqlite.BusinessAnalysisDimensionRow{}
	for rows.Next() {
		var row sqlite.BusinessAnalysisDimensionRow
		if err := rows.Scan(&row.Dimension, &row.Value, &row.CallCount, &row.TranscriptCount, &row.MissingTranscriptCount, &row.OpportunityCallCount, &row.AccountCallCount, &row.ExternalCallCount, &row.LatestCallAt); err != nil {
			return nil, err
		}
		row.TranscriptCoverageRate = postgresRate(row.TranscriptCount, row.CallCount)
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) postgresBusinessAnalysisCallRows(ctx context.Context, filter sqlite.BusinessAnalysisFilter, limit int) ([]sqlite.BusinessAnalysisCallRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT call_id, title, started_at, call_date, call_month, duration_seconds, lifecycle_bucket, scope, system, direction, transcript_status, account_industry, opportunity_stage, opportunity_type, forecast_category, opportunity_count, account_count, participant_status, person_title_status, person_title_source
  FROM `+s.postgresBusinessAnalysisCallsFunction()+`($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)`,
		filter.TitleQuery,
		filter.Query,
		filter.FromDate,
		filter.ToDate,
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
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []sqlite.BusinessAnalysisCallRow{}
	for rows.Next() {
		var row sqlite.BusinessAnalysisCallRow
		if err := rows.Scan(&row.CallID, &row.Title, &row.StartedAt, &row.CallDate, &row.CallMonth, &row.DurationSeconds, &row.LifecycleBucket, &row.Scope, &row.System, &row.Direction, &row.TranscriptStatus, &row.AccountIndustry, &row.OpportunityStage, &row.OpportunityType, &row.ForecastCategory, &row.OpportunityCount, &row.AccountCount, &row.ParticipantStatus, &row.PersonTitleStatus, &row.PersonTitleSource); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) postgresBusinessAnalysisCallsFunction() string {
	if s.readOnlyOptions.EnforceAllowedColumnBoundary {
		return "gongmcp_business_analysis_calls_sanitized"
	}
	return "gongmcp_business_analysis_calls"
}

func (s *Store) postgresBusinessAnalysisEvidenceFunction() string {
	if s.readOnlyOptions.EnforceAllowedColumnBoundary {
		return "gongmcp_business_analysis_evidence_sanitized"
	}
	return "gongmcp_business_analysis_evidence"
}

func (s *Store) postgresTranscriptQuoteAttributionFunction() string {
	if s.readOnlyOptions.EnforceAllowedColumnBoundary {
		return "gongmcp_search_transcript_quotes_with_attribution_sanitized"
	}
	return "gongmcp_search_transcript_quotes_with_attribution"
}

func (s *Store) postgresTranscriptSegmentsByCallFactsFunction() string {
	if s.readOnlyOptions.EnforceAllowedColumnBoundary {
		return "gongmcp_search_transcript_segments_by_call_facts_sanitized"
	}
	return "gongmcp_search_transcript_segments_by_call_facts"
}

func (s *Store) postgresBusinessAnalysisSummary(ctx context.Context, filter sqlite.BusinessAnalysisFilter) (sqlite.BusinessAnalysisCohortSummary, error) {
	var summary sqlite.BusinessAnalysisCohortSummary
	if err := s.db.QueryRowContext(ctx, `SELECT call_count, transcript_count, missing_transcript_count, account_industry_count, opportunity_stage_count, opportunity_call_count, account_call_count, external_call_count, participant_call_count, participant_title_call_count, earliest_call_at, latest_call_at, total_duration_seconds, average_duration_seconds
  FROM gongmcp_business_analysis_summary($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`,
		filter.TitleQuery,
		filter.Query,
		filter.FromDate,
		filter.ToDate,
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
	).Scan(
		&summary.CallCount,
		&summary.TranscriptCount,
		&summary.MissingTranscriptCount,
		&summary.AccountIndustryCount,
		&summary.OpportunityStageCount,
		&summary.OpportunityCallCount,
		&summary.AccountCallCount,
		&summary.ExternalCallCount,
		&summary.ParticipantCallCount,
		&summary.ParticipantTitleCallCount,
		&summary.EarliestCallAt,
		&summary.LatestCallAt,
		&summary.TotalDurationSeconds,
		&summary.AverageDurationSeconds,
	); err != nil {
		return sqlite.BusinessAnalysisCohortSummary{}, err
	}
	summary.TranscriptCoverageRate = postgresRate(summary.TranscriptCount, summary.CallCount)
	summary.ParticipantTitleRate = postgresRate(summary.ParticipantTitleCallCount, summary.CallCount)
	summary.CRMOutcomeCoverageHint = postgresCoverageHint(summary.OpportunityStageCount, summary.CallCount, "opportunity stage")
	summary.ParticipantCoverageHint = postgresCoverageHint(summary.ParticipantTitleCallCount, summary.CallCount, "participant title")
	summary.IndustryCoverageHint = postgresCoverageHint(summary.AccountIndustryCount, summary.CallCount, "account industry")
	summary.CacheDerivedNotLLMClaims = true
	return summary, nil
}

func (s *Store) normalizePostgresBusinessAnalysisFilter(filter sqlite.BusinessAnalysisFilter) (sqlite.BusinessAnalysisFilter, error) {
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
		canonical, from, to, err := normalizePostgresBusinessAnalysisQuarter(filter.Quarter)
		if err != nil {
			return sqlite.BusinessAnalysisFilter{}, err
		}
		filter.Quarter = canonical
		if filter.FromDate == "" {
			filter.FromDate = from
		}
		if filter.ToDate == "" {
			filter.ToDate = to
		}
	}
	var err error
	if filter.FromDate, err = normalizePostgresDateFilter(filter.FromDate, "from_date"); err != nil {
		return sqlite.BusinessAnalysisFilter{}, err
	}
	if filter.ToDate, err = normalizePostgresDateFilter(filter.ToDate, "to_date"); err != nil {
		return sqlite.BusinessAnalysisFilter{}, err
	}
	if filter.FromDate != "" && filter.ToDate != "" && filter.FromDate > filter.ToDate {
		return sqlite.BusinessAnalysisFilter{}, errors.New("from_date must be on or before to_date")
	}
	if filter.LifecycleBucket, err = postgresNormalizeLifecycleBucket(filter.LifecycleBucket); err != nil {
		return sqlite.BusinessAnalysisFilter{}, err
	}
	if filter.Scope, err = postgresNormalizeOptionalScope(filter.Scope); err != nil {
		return sqlite.BusinessAnalysisFilter{}, err
	}
	if filter.AccountQuery != "" && !s.readOnlyOptions.AllowAccountQuery {
		return sqlite.BusinessAnalysisFilter{}, errors.New("account_query is not supported by the Postgres analyst-business-core reader because it can probe customer names")
	}
	if filter.TranscriptStatus, err = postgresNormalizeOptionalTranscriptStatus(filter.TranscriptStatus); err != nil {
		return sqlite.BusinessAnalysisFilter{}, err
	}
	if err := validatePostgresBusinessAnalysisSearchText(filter.Query, "filter.query"); err != nil {
		return sqlite.BusinessAnalysisFilter{}, err
	}
	if err := validatePostgresBusinessAnalysisSearchText(filter.ParticipantTitleQuery, "participant_title_query"); err != nil {
		return sqlite.BusinessAnalysisFilter{}, err
	}
	if filter.Limit > 0 {
		filter.Limit = boundedLimit(filter.Limit, defaultBusinessAnalysisLimit, maxBusinessAnalysisLimit)
	}
	return filter, nil
}

func postgresNormalizeDateRange(rawFrom string, rawTo string) (string, string, error) {
	fromDate, err := normalizePostgresDateFilter(rawFrom, "from_date")
	if err != nil {
		return "", "", err
	}
	toDate, err := normalizePostgresDateFilter(rawTo, "to_date")
	if err != nil {
		return "", "", err
	}
	if fromDate != "" && toDate != "" && fromDate > toDate {
		return "", "", errors.New("from_date must be on or before to_date")
	}
	return fromDate, toDate, nil
}

func postgresNormalizeLifecycleBucket(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if !postgresKnownLifecycleBucket(value) {
		return "", fmt.Errorf("unknown lifecycle bucket %q", value)
	}
	return strings.ToLower(value), nil
}

func postgresNormalizeOptionalScope(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	scope, ok := normalizePostgresScope(value)
	if !ok {
		return "", errors.New("scope must be one of: External, Internal, Unknown")
	}
	return scope, nil
}

func postgresNormalizeOptionalTranscriptStatus(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "any" {
		return "", nil
	}
	status, ok := normalizePostgresTranscriptStatus(value)
	if !ok {
		return "", errors.New("transcript_status must be one of: present, missing, any")
	}
	return status, nil
}

func normalizePostgresBusinessAnalysisQuarter(value string) (string, string, string, error) {
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

func validatePostgresBusinessAnalysisSearchText(value string, field string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if len(value) > maxBusinessAnalysisFTSQueryLength {
		return fmt.Errorf("%s must be %d characters or fewer", field, maxBusinessAnalysisFTSQueryLength)
	}
	var tokens []string
	var token strings.Builder
	flush := func() error {
		if token.Len() == 0 {
			return nil
		}
		text := token.String()
		token.Reset()
		if len(text) > maxBusinessAnalysisFTSQueryTermLen {
			return fmt.Errorf("%s terms must be %d characters or fewer", field, maxBusinessAnalysisFTSQueryTermLen)
		}
		tokens = append(tokens, text)
		if len(tokens) > maxBusinessAnalysisFTSQueryTerms {
			return fmt.Errorf("%s must include no more than %d search terms", field, maxBusinessAnalysisFTSQueryTerms)
		}
		return nil
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			token.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			token.WriteRune(r)
		case r >= '0' && r <= '9':
			token.WriteRune(r)
		case r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '-' || r == '_' || r == '\'' || r == '"' || r == '/' || r == '.' || r == ',' || r == ';' || r == ':':
			if err := flush(); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%s may contain only letters, numbers, spaces, and simple separators", field)
		}
	}
	if err := flush(); err != nil {
		return err
	}
	if len(tokens) == 0 {
		return fmt.Errorf("%s must include at least one search term", field)
	}
	return nil
}

func postgresBusinessAnalysisDimension(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "lifecycle", "lifecycle_bucket":
		return "lifecycle", nil
	case "industry", "account_industry":
		return "industry", nil
	case "persona", "participant_title", "title":
		return "persona", nil
	case "opportunity_stage", "stage":
		return "opportunity_stage", nil
	case "opportunity_type":
		return "opportunity_type", nil
	case "forecast_category":
		return "forecast_category", nil
	case "scope":
		return "scope", nil
	case "system":
		return "system", nil
	case "direction":
		return "direction", nil
	case "transcript_status":
		return "transcript_status", nil
	case "month", "call_month":
		return "month", nil
	case "quarter":
		return "quarter", nil
	case "won_lost", "outcome":
		return "won_lost", nil
	case "loss_reason":
		return "loss_reason", nil
	default:
		return "", fmt.Errorf("unsupported business-analysis dimension %q", value)
	}
}

func postgresCoverageHint(populated int64, total int64, label string) string {
	if total == 0 {
		return "no calls matched the filter"
	}
	ratio := postgresRate(populated, total)
	switch {
	case ratio >= 0.8:
		return label + " coverage is high"
	case ratio >= 0.4:
		return label + " coverage is partial"
	default:
		return label + " coverage is sparse"
	}
}

func firstPositivePostgres(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func firstNonEmptyPostgres(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
