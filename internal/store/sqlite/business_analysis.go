package sqlite

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

const (
	defaultBusinessAnalysisLimit       = 25
	maxBusinessAnalysisLimit           = 100
	maxBusinessAnalysisFTSQueryLength  = 160
	maxBusinessAnalysisFTSQueryTerms   = 12
	maxBusinessAnalysisFTSQueryTermLen = 48
)

type BusinessAnalysisFilter struct {
	TitleQuery            string `json:"title_query,omitempty"`
	Query                 string `json:"query,omitempty"`
	FromDate              string `json:"from_date,omitempty"`
	ToDate                string `json:"to_date,omitempty"`
	Quarter               string `json:"quarter,omitempty"`
	LifecycleBucket       string `json:"lifecycle_bucket,omitempty"`
	Scope                 string `json:"scope,omitempty"`
	System                string `json:"system,omitempty"`
	Direction             string `json:"direction,omitempty"`
	TranscriptStatus      string `json:"transcript_status,omitempty"`
	Industry              string `json:"industry,omitempty"`
	AccountQuery          string `json:"account_query,omitempty"`
	OpportunityStage      string `json:"opportunity_stage,omitempty"`
	CRMObjectType         string `json:"crm_object_type,omitempty"`
	CRMObjectID           string `json:"crm_object_id,omitempty"`
	ParticipantTitleQuery string `json:"participant_title_query,omitempty"`
	Limit                 int    `json:"limit,omitempty"`
}

type BusinessAnalysisCallSearchParams struct {
	Filter BusinessAnalysisFilter
	Limit  int
}

type BusinessAnalysisCallSearchResult struct {
	Filter  BusinessAnalysisFilter        `json:"filter"`
	Summary BusinessAnalysisCohortSummary `json:"summary"`
	Rows    []BusinessAnalysisCallRow     `json:"rows"`
}

type BusinessAnalysisCohortSummary struct {
	CallCount                 int64   `json:"call_count"`
	TranscriptCount           int64   `json:"transcript_count"`
	MissingTranscriptCount    int64   `json:"missing_transcript_count"`
	TranscriptCoverageRate    float64 `json:"transcript_coverage_rate"`
	AccountIndustryCount      int64   `json:"account_industry_count"`
	OpportunityStageCount     int64   `json:"opportunity_stage_count"`
	OpportunityCallCount      int64   `json:"opportunity_call_count"`
	AccountCallCount          int64   `json:"account_call_count"`
	ExternalCallCount         int64   `json:"external_call_count"`
	ParticipantCallCount      int64   `json:"participant_call_count"`
	ParticipantTitleCallCount int64   `json:"participant_title_call_count"`
	ParticipantTitleRate      float64 `json:"participant_title_rate"`
	EarliestCallAt            string  `json:"earliest_call_at,omitempty"`
	LatestCallAt              string  `json:"latest_call_at,omitempty"`
	TotalDurationSeconds      int64   `json:"total_duration_seconds"`
	AverageDurationSeconds    float64 `json:"average_duration_seconds"`
	CRMOutcomeCoverageHint    string  `json:"crm_outcome_coverage_hint,omitempty"`
	ParticipantCoverageHint   string  `json:"participant_coverage_hint,omitempty"`
	IndustryCoverageHint      string  `json:"industry_coverage_hint,omitempty"`
	CacheDerivedNotLLMClaims  bool    `json:"cache_derived_not_llm_claims"`
}

type BusinessAnalysisCallRow struct {
	CallID            string `json:"call_id,omitempty"`
	Title             string `json:"title,omitempty"`
	StartedAt         string `json:"started_at,omitempty"`
	CallDate          string `json:"call_date,omitempty"`
	CallMonth         string `json:"call_month,omitempty"`
	DurationSeconds   int64  `json:"duration_seconds,omitempty"`
	LifecycleBucket   string `json:"lifecycle_bucket,omitempty"`
	Scope             string `json:"scope,omitempty"`
	System            string `json:"system,omitempty"`
	Direction         string `json:"direction,omitempty"`
	TranscriptStatus  string `json:"transcript_status,omitempty"`
	AccountIndustry   string `json:"account_industry,omitempty"`
	OpportunityStage  string `json:"opportunity_stage,omitempty"`
	OpportunityType   string `json:"opportunity_type,omitempty"`
	ForecastCategory  string `json:"forecast_category,omitempty"`
	OpportunityCount  int64  `json:"opportunity_count,omitempty"`
	AccountCount      int64  `json:"account_count,omitempty"`
	ParticipantStatus string `json:"participant_status,omitempty"`
	PersonTitleStatus string `json:"person_title_status,omitempty"`
	PersonTitleSource string `json:"person_title_source,omitempty"`
}

type BusinessAnalysisEvidenceSearchParams struct {
	Filter BusinessAnalysisFilter
	Query  string
	Limit  int
}

type BusinessAnalysisEvidenceRow struct {
	CallID                 string `json:"call_id,omitempty"`
	Title                  string `json:"title,omitempty"`
	StartedAt              string `json:"started_at,omitempty"`
	CallDate               string `json:"call_date,omitempty"`
	CallMonth              string `json:"call_month,omitempty"`
	LifecycleBucket        string `json:"lifecycle_bucket,omitempty"`
	AccountIndustry        string `json:"account_industry,omitempty"`
	AccountName            string `json:"account_name,omitempty"`
	OpportunityName        string `json:"opportunity_name,omitempty"`
	OpportunityStage       string `json:"opportunity_stage,omitempty"`
	OpportunityType        string `json:"opportunity_type,omitempty"`
	OpportunityProbability string `json:"opportunity_probability,omitempty"`
	OpportunityCloseDate   string `json:"opportunity_close_date,omitempty"`
	ParticipantStatus      string `json:"participant_status,omitempty"`
	PersonTitleStatus      string `json:"person_title_status,omitempty"`
	PersonTitleSource      string `json:"person_title_source,omitempty"`
	SegmentIndex           int    `json:"segment_index,omitempty"`
	StartMS                int64  `json:"start_ms,omitempty"`
	EndMS                  int64  `json:"end_ms,omitempty"`
	Snippet                string `json:"snippet,omitempty"`
	ContextExcerpt         string `json:"context_excerpt,omitempty"`
}

type BusinessAnalysisDimensionSummaryParams struct {
	Filter     BusinessAnalysisFilter
	Dimension  string
	ThemeQuery string
	Limit      int
}

type BusinessAnalysisDimensionRow struct {
	Dimension              string  `json:"dimension"`
	Value                  string  `json:"value"`
	CallCount              int64   `json:"call_count"`
	TranscriptCount        int64   `json:"transcript_count"`
	MissingTranscriptCount int64   `json:"missing_transcript_count"`
	TranscriptCoverageRate float64 `json:"transcript_coverage_rate"`
	OpportunityCallCount   int64   `json:"opportunity_call_count"`
	AccountCallCount       int64   `json:"account_call_count"`
	ExternalCallCount      int64   `json:"external_call_count"`
	LatestCallAt           string  `json:"latest_call_at,omitempty"`
}

func (s *Store) SearchBusinessAnalysisCalls(ctx context.Context, params BusinessAnalysisCallSearchParams) (*BusinessAnalysisCallSearchResult, error) {
	filter, err := normalizeBusinessAnalysisFilter(params.Filter)
	if err != nil {
		return nil, err
	}
	limit := boundedLimit(firstPositiveInt(params.Limit, filter.Limit), defaultBusinessAnalysisLimit, maxBusinessAnalysisLimit)
	filter.Limit = limit

	from, where, args, err := businessAnalysisCallFromWhere(filter, true)
	if err != nil {
		return nil, err
	}
	summary, err := s.businessAnalysisCohortSummary(ctx, from, where, args)
	if err != nil {
		return nil, err
	}

	query := `
SELECT cf.call_id,
       cf.title,
       cf.started_at,
       cf.call_date,
       cf.call_month,
       cf.duration_seconds,
       cf.lifecycle_bucket,
       cf.scope,
       cf.system,
       cf.direction,
       cf.transcript_status,
       cf.account_industry,
       cf.opportunity_stage,
       cf.opportunity_type,
       cf.opportunity_forecast_category,
       cf.opportunity_count,
       cf.account_count,
       CASE WHEN c.parties_count > 0 THEN 'present' ELSE 'missing_from_cache' END AS participant_status,
       ` + businessAnalysisPersonTitleStatusSQL("c") + ` AS person_title_status,
       ` + businessAnalysisPersonTitleSourceSQL("c") + ` AS person_title_source
` + from
	if len(where) > 0 {
		query += `
 WHERE ` + strings.Join(where, ` AND `)
	}
	query += `
 ORDER BY cf.started_at DESC, cf.call_id
 LIMIT ?`
	rowArgs := append(append([]any{}, args...), limit)
	rows, err := s.db.QueryContext(ctx, query, rowArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]BusinessAnalysisCallRow, 0)
	for rows.Next() {
		var row BusinessAnalysisCallRow
		if err := rows.Scan(
			&row.CallID,
			&row.Title,
			&row.StartedAt,
			&row.CallDate,
			&row.CallMonth,
			&row.DurationSeconds,
			&row.LifecycleBucket,
			&row.Scope,
			&row.System,
			&row.Direction,
			&row.TranscriptStatus,
			&row.AccountIndustry,
			&row.OpportunityStage,
			&row.OpportunityType,
			&row.ForecastCategory,
			&row.OpportunityCount,
			&row.AccountCount,
			&row.ParticipantStatus,
			&row.PersonTitleStatus,
			&row.PersonTitleSource,
		); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return &BusinessAnalysisCallSearchResult{
		Filter:  filter,
		Summary: summary,
		Rows:    out,
	}, nil
}

func (s *Store) SearchBusinessAnalysisEvidence(ctx context.Context, params BusinessAnalysisEvidenceSearchParams) ([]BusinessAnalysisEvidenceRow, error) {
	filter, err := normalizeBusinessAnalysisFilter(params.Filter)
	if err != nil {
		return nil, err
	}
	limit := boundedLimit(firstPositiveInt(params.Limit, filter.Limit), defaultBusinessAnalysisLimit, maxBusinessAnalysisLimit)
	queryText := strings.TrimSpace(firstNonEmptyString(params.Query, filter.Query))
	queryMatch, err := businessAnalysisFTSQuery(queryText, "query")
	if err != nil {
		return nil, err
	}
	if queryMatch == "" {
		return nil, errors.New("query is required for business-analysis evidence searches")
	}

	from, where, args, err := businessAnalysisCallFromWhere(filter, true)
	if err != nil {
		return nil, err
	}
	selectSnippet := `substr(ts.text, 1, 400) AS snippet`
	orderBy := `cf.started_at DESC, ts.call_id, ts.segment_index`
	from = `
  FROM transcript_segments_fts
  JOIN transcript_segments ts
    ON ts.id = transcript_segments_fts.rowid
  JOIN call_facts cf
    ON cf.call_id = ts.call_id
  JOIN calls c
    ON c.call_id = cf.call_id`
	where = append([]string{`transcript_segments_fts MATCH ?`}, where...)
	args = append([]any{queryMatch}, args...)
	selectSnippet = `snippet(transcript_segments_fts, 0, '', '', '...', 18) AS snippet`
	orderBy = `bm25(transcript_segments_fts), cf.started_at DESC, ts.call_id, ts.segment_index`

	sql := `
SELECT cf.call_id,
       cf.title,
       cf.started_at,
       cf.call_date,
       cf.call_month,
       cf.lifecycle_bucket,
       cf.account_industry,
       COALESCE((SELECT TRIM(o.object_name) FROM call_context_objects o WHERE o.call_id = cf.call_id AND o.object_type = 'Account' AND TRIM(o.object_name) <> '' ORDER BY o.object_key LIMIT 1), '') AS account_name,
       COALESCE((SELECT TRIM(o.object_name) FROM call_context_objects o WHERE o.call_id = cf.call_id AND o.object_type = 'Opportunity' AND TRIM(o.object_name) <> '' ORDER BY o.object_key LIMIT 1), '') AS opportunity_name,
       cf.opportunity_stage,
       cf.opportunity_type,
       cf.opportunity_probability,
       COALESCE((SELECT TRIM(f.field_value_text) FROM call_context_fields f JOIN call_context_objects o ON o.call_id = f.call_id AND o.object_key = f.object_key WHERE f.call_id = cf.call_id AND o.object_type = 'Opportunity' AND f.field_name IN ('CloseDate', 'closeDate', 'Close_Date__c') AND TRIM(f.field_value_text) <> '' ORDER BY f.id LIMIT 1), '') AS opportunity_close_date,
       CASE WHEN c.parties_count > 0 THEN 'present' ELSE 'missing_from_cache' END AS participant_status,
       ` + businessAnalysisPersonTitleStatusSQL("c") + ` AS person_title_status,
       ` + businessAnalysisPersonTitleSourceSQL("c") + ` AS person_title_source,
       ts.segment_index,
       ts.start_ms,
       ts.end_ms,
       ` + selectSnippet + `,
       substr(COALESCE((
	       SELECT group_concat(context_text, ' ')
	         FROM (
		       SELECT ctx.text AS context_text
		         FROM transcript_segments ctx
		        WHERE ctx.call_id = ts.call_id
		          AND ctx.segment_index BETWEEN ts.segment_index - 1 AND ts.segment_index + 1
		        ORDER BY ctx.segment_index
	         )
       ), ''), 1, 800) AS context_excerpt
` + from
	if len(where) > 0 {
		sql += `
 WHERE ` + strings.Join(where, ` AND `)
	}
	sql += `
 ORDER BY ` + orderBy + `
 LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]BusinessAnalysisEvidenceRow, 0)
	for rows.Next() {
		var row BusinessAnalysisEvidenceRow
		if err := rows.Scan(
			&row.CallID,
			&row.Title,
			&row.StartedAt,
			&row.CallDate,
			&row.CallMonth,
			&row.LifecycleBucket,
			&row.AccountIndustry,
			&row.AccountName,
			&row.OpportunityName,
			&row.OpportunityStage,
			&row.OpportunityType,
			&row.OpportunityProbability,
			&row.OpportunityCloseDate,
			&row.ParticipantStatus,
			&row.PersonTitleStatus,
			&row.PersonTitleSource,
			&row.SegmentIndex,
			&row.StartMS,
			&row.EndMS,
			&row.Snippet,
			&row.ContextExcerpt,
		); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) SummarizeBusinessAnalysisDimension(ctx context.Context, params BusinessAnalysisDimensionSummaryParams) ([]BusinessAnalysisDimensionRow, error) {
	filter, err := normalizeBusinessAnalysisFilter(params.Filter)
	if err != nil {
		return nil, err
	}
	if themeQuery := strings.TrimSpace(params.ThemeQuery); themeQuery != "" {
		var err error
		from, where, args, err := businessAnalysisCallFromWhere(filter, true)
		if err != nil {
			return nil, err
		}
		where, args, err = businessAnalysisAppendTranscriptQueryFilter(where, args, themeQuery, "theme_query")
		if err != nil {
			return nil, err
		}
		return s.summarizeBusinessAnalysisDimension(ctx, filter, params.Dimension, params.Limit, from, where, args)
	}
	from, where, args, err := businessAnalysisCallFromWhere(filter, true)
	if err != nil {
		return nil, err
	}
	return s.summarizeBusinessAnalysisDimension(ctx, filter, params.Dimension, params.Limit, from, where, args)
}

func (s *Store) summarizeBusinessAnalysisDimension(ctx context.Context, filter BusinessAnalysisFilter, requestedDimension string, requestedLimit int, from string, where []string, args []any) ([]BusinessAnalysisDimensionRow, error) {
	limit := boundedLimit(firstPositiveInt(requestedLimit, filter.Limit), defaultBusinessAnalysisLimit, maxBusinessAnalysisLimit)
	dimension, expr, err := businessAnalysisDimensionExpr(requestedDimension)
	if err != nil {
		return nil, err
	}
	sql := `
WITH filtered AS (
	SELECT cf.*,
	       ` + expr + ` AS dimension_value
` + from
	if len(where) > 0 {
		sql += `
 WHERE ` + strings.Join(where, ` AND `)
	}
	sql += `
)
SELECT ? AS dimension,
       COALESCE(NULLIF(TRIM(dimension_value), ''), '<blank>') AS value,
       COUNT(*) AS call_count,
       SUM(CASE WHEN transcript_present = 1 THEN 1 ELSE 0 END) AS transcript_count,
       SUM(CASE WHEN transcript_present = 0 THEN 1 ELSE 0 END) AS missing_transcript_count,
       SUM(CASE WHEN opportunity_count > 0 THEN 1 ELSE 0 END) AS opportunity_call_count,
       SUM(CASE WHEN account_count > 0 THEN 1 ELSE 0 END) AS account_call_count,
       SUM(CASE WHEN scope = 'External' THEN 1 ELSE 0 END) AS external_call_count,
       COALESCE(MAX(started_at), '') AS latest_call_at
  FROM filtered
 GROUP BY value
 ORDER BY call_count DESC, value
 LIMIT ?`
	args = append(args, dimension, limit)

	rows, err := s.db.QueryContext(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]BusinessAnalysisDimensionRow, 0)
	for rows.Next() {
		var row BusinessAnalysisDimensionRow
		if err := rows.Scan(
			&row.Dimension,
			&row.Value,
			&row.CallCount,
			&row.TranscriptCount,
			&row.MissingTranscriptCount,
			&row.OpportunityCallCount,
			&row.AccountCallCount,
			&row.ExternalCallCount,
			&row.LatestCallAt,
		); err != nil {
			return nil, err
		}
		row.TranscriptCoverageRate = rate(row.TranscriptCount, row.CallCount)
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) businessAnalysisCohortSummary(ctx context.Context, from string, where []string, args []any) (BusinessAnalysisCohortSummary, error) {
	query := `
SELECT COUNT(*) AS call_count,
       COALESCE(SUM(CASE WHEN cf.transcript_present = 1 THEN 1 ELSE 0 END), 0) AS transcript_count,
       COALESCE(SUM(CASE WHEN cf.transcript_present = 0 THEN 1 ELSE 0 END), 0) AS missing_transcript_count,
       COALESCE(SUM(CASE WHEN TRIM(cf.account_industry) <> '' THEN 1 ELSE 0 END), 0) AS account_industry_count,
       COALESCE(SUM(CASE WHEN TRIM(cf.opportunity_stage) <> '' THEN 1 ELSE 0 END), 0) AS opportunity_stage_count,
       COALESCE(SUM(CASE WHEN cf.opportunity_count > 0 THEN 1 ELSE 0 END), 0) AS opportunity_call_count,
       COALESCE(SUM(CASE WHEN cf.account_count > 0 THEN 1 ELSE 0 END), 0) AS account_call_count,
       COALESCE(SUM(CASE WHEN cf.scope = 'External' THEN 1 ELSE 0 END), 0) AS external_call_count,
       COALESCE(SUM(CASE WHEN c.parties_count > 0 THEN 1 ELSE 0 END), 0) AS participant_call_count,
       COALESCE(SUM(CASE WHEN ` + businessAnalysisPartyTitleCountSQL("c") + ` > 0 THEN 1 ELSE 0 END), 0) AS participant_title_call_count,
       COALESCE(MIN(cf.started_at), '') AS earliest_call_at,
       COALESCE(MAX(cf.started_at), '') AS latest_call_at,
       COALESCE(SUM(cf.duration_seconds), 0) AS total_duration_seconds,
       COALESCE(AVG(cf.duration_seconds), 0) AS average_duration_seconds
` + from
	if len(where) > 0 {
		query += `
 WHERE ` + strings.Join(where, ` AND `)
	}
	var summary BusinessAnalysisCohortSummary
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(
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
		return BusinessAnalysisCohortSummary{}, err
	}
	summary.TranscriptCoverageRate = rate(summary.TranscriptCount, summary.CallCount)
	summary.ParticipantTitleRate = rate(summary.ParticipantTitleCallCount, summary.CallCount)
	summary.CRMOutcomeCoverageHint = coverageHint(summary.OpportunityStageCount, summary.CallCount, "opportunity stage")
	summary.ParticipantCoverageHint = coverageHint(summary.ParticipantTitleCallCount, summary.CallCount, "participant title")
	summary.IndustryCoverageHint = coverageHint(summary.AccountIndustryCount, summary.CallCount, "account industry")
	summary.CacheDerivedNotLLMClaims = true
	return summary, nil
}

func businessAnalysisCallFromWhere(filter BusinessAnalysisFilter, includeFilterQuery bool) (string, []string, []any, error) {
	from := `
  FROM call_facts cf
  JOIN calls c
    ON c.call_id = cf.call_id`
	var where []string
	var args []any

	if value := strings.TrimSpace(filter.TitleQuery); value != "" {
		where = append(where, `LOWER(cf.title) LIKE '%' || LOWER(?) || '%'`)
		args = append(args, value)
	}
	if includeFilterQuery {
		var err error
		where, args, err = businessAnalysisAppendTranscriptQueryFilter(where, args, filter.Query, "filter.query")
		if err != nil {
			return "", nil, nil, err
		}
	}
	if value := strings.TrimSpace(filter.FromDate); value != "" {
		where = append(where, `cf.call_date >= ?`)
		args = append(args, value)
	}
	if value := strings.TrimSpace(filter.ToDate); value != "" {
		where = append(where, `cf.call_date <= ?`)
		args = append(args, value)
	}
	if value := strings.TrimSpace(filter.LifecycleBucket); value != "" {
		where = append(where, `cf.lifecycle_bucket = ?`)
		args = append(args, strings.ToLower(value))
	}
	if value := strings.TrimSpace(filter.Scope); value != "" {
		where = append(where, `cf.scope = ?`)
		args = append(args, value)
	}
	if value := strings.TrimSpace(filter.System); value != "" {
		where = append(where, `cf.system = ?`)
		args = append(args, value)
	}
	if value := strings.TrimSpace(filter.Direction); value != "" {
		where = append(where, `cf.direction = ?`)
		args = append(args, value)
	}
	if value := strings.TrimSpace(filter.TranscriptStatus); value != "" && value != "any" {
		where = append(where, `cf.transcript_status = ?`)
		args = append(args, value)
	}
	if value := strings.TrimSpace(filter.Industry); value != "" {
		where = append(where, `LOWER(cf.account_industry) LIKE '%' || LOWER(?) || '%'`)
		args = append(args, value)
	}
	if value := strings.TrimSpace(filter.AccountQuery); value != "" {
		where = append(where, `EXISTS (
			SELECT 1
			  FROM call_context_objects account_o
			 WHERE account_o.call_id = cf.call_id
			   AND account_o.object_type = 'Account'
			   AND LOWER(TRIM(account_o.object_name)) LIKE '%' || LOWER(?) || '%'
		)`)
		args = append(args, value)
	}
	if value := strings.TrimSpace(filter.OpportunityStage); value != "" {
		where = append(where, `LOWER(cf.opportunity_stage) LIKE '%' || LOWER(?) || '%'`)
		args = append(args, value)
	}
	if strings.TrimSpace(filter.CRMObjectType) != "" || strings.TrimSpace(filter.CRMObjectID) != "" {
		subquery := []string{`filter_o.call_id = cf.call_id`}
		if value := strings.TrimSpace(filter.CRMObjectType); value != "" {
			subquery = append(subquery, `filter_o.object_type = ?`)
			args = append(args, value)
		}
		if value := strings.TrimSpace(filter.CRMObjectID); value != "" {
			subquery = append(subquery, `filter_o.object_id = ?`)
			args = append(args, value)
		}
		where = append(where, `EXISTS (SELECT 1 FROM call_context_objects filter_o WHERE `+strings.Join(subquery, ` AND `)+`)`)
	}
	if value := strings.TrimSpace(filter.ParticipantTitleQuery); value != "" {
		where = append(where, businessAnalysisParticipantTitleLikeSQL("c", "?"))
		args = append(args, value, value, value)
	}
	return from, where, args, nil
}

func businessAnalysisAppendTranscriptQueryFilter(where []string, args []any, rawQuery string, field string) ([]string, []any, error) {
	query, err := businessAnalysisFTSQuery(rawQuery, field)
	if err != nil {
		return nil, nil, err
	}
	if query == "" {
		return where, args, nil
	}
	where = append(where, `cf.call_id IN (
		SELECT query_ts.call_id
		  FROM transcript_segments_fts
		  JOIN transcript_segments query_ts
		    ON query_ts.id = transcript_segments_fts.rowid
		 WHERE transcript_segments_fts MATCH ?
	)`)
	args = append(args, query)
	return where, args, nil
}

func businessAnalysisFTSQuery(value string, field string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if len(value) > maxBusinessAnalysisFTSQueryLength {
		return "", fmt.Errorf("%s must be %d characters or fewer", field, maxBusinessAnalysisFTSQueryLength)
	}
	var tokens []string
	var token strings.Builder
	flush := func() error {
		if token.Len() == 0 {
			return nil
		}
		text := strings.ToLower(token.String())
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
				return "", err
			}
		default:
			return "", fmt.Errorf("%s may contain only letters, numbers, spaces, and simple separators", field)
		}
	}
	if err := flush(); err != nil {
		return "", err
	}
	if len(tokens) == 0 {
		return "", fmt.Errorf("%s must include at least one search term", field)
	}
	quoted := make([]string, 0, len(tokens))
	for _, item := range tokens {
		quoted = append(quoted, `"`+item+`"`)
	}
	return strings.Join(quoted, " "), nil
}

func normalizeBusinessAnalysisFilter(filter BusinessAnalysisFilter) (BusinessAnalysisFilter, error) {
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
		canonical, from, to, err := normalizeBusinessAnalysisQuarter(filter.Quarter)
		if err != nil {
			return BusinessAnalysisFilter{}, err
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
	if filter.FromDate, err = normalizeDateFilter(filter.FromDate, "from_date"); err != nil {
		return BusinessAnalysisFilter{}, err
	}
	if filter.ToDate, err = normalizeDateFilter(filter.ToDate, "to_date"); err != nil {
		return BusinessAnalysisFilter{}, err
	}
	if filter.FromDate != "" && filter.ToDate != "" && filter.FromDate > filter.ToDate {
		return BusinessAnalysisFilter{}, errors.New("from_date must be on or before to_date")
	}
	if filter.LifecycleBucket != "" && !isKnownLifecycleBucket(filter.LifecycleBucket) {
		return BusinessAnalysisFilter{}, fmt.Errorf("unknown lifecycle bucket %q", filter.LifecycleBucket)
	}
	if filter.Scope != "" {
		scope, ok := normalizedScope(filter.Scope)
		if !ok {
			return BusinessAnalysisFilter{}, errors.New("scope must be one of: External, Internal, Unknown")
		}
		filter.Scope = scope
	}
	if filter.TranscriptStatus != "" && filter.TranscriptStatus != "any" {
		status, ok := normalizedTranscriptStatus(filter.TranscriptStatus)
		if !ok {
			return BusinessAnalysisFilter{}, errors.New("transcript_status must be one of: present, missing, any")
		}
		filter.TranscriptStatus = status
	}
	if filter.Limit > 0 {
		filter.Limit = boundedLimit(filter.Limit, defaultBusinessAnalysisLimit, maxBusinessAnalysisLimit)
	}
	return filter, nil
}

func normalizeBusinessAnalysisQuarter(value string) (string, string, string, error) {
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
	if _, err := normalizeDateFilter(year+"-01-01", "quarter year"); err != nil {
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

func businessAnalysisDimensionExpr(dimension string) (string, string, error) {
	switch strings.ToLower(strings.TrimSpace(dimension)) {
	case "", "lifecycle", "lifecycle_bucket":
		return "lifecycle", "cf.lifecycle_bucket", nil
	case "industry", "account_industry":
		return "industry", "cf.account_industry", nil
	case "persona", "participant_title", "title":
		return "persona", businessAnalysisParticipantTitleLabelSQL("c"), nil
	case "opportunity_stage", "stage":
		return "opportunity_stage", "cf.opportunity_stage", nil
	case "opportunity_type":
		return "opportunity_type", "cf.opportunity_type", nil
	case "forecast_category":
		return "forecast_category", "cf.opportunity_forecast_category", nil
	case "scope":
		return "scope", "cf.scope", nil
	case "system":
		return "system", "cf.system", nil
	case "direction":
		return "direction", "cf.direction", nil
	case "transcript_status":
		return "transcript_status", "cf.transcript_status", nil
	case "month", "call_month":
		return "month", "cf.call_month", nil
	case "quarter":
		return "quarter", businessAnalysisQuarterExpr("cf.call_date"), nil
	case "won_lost", "outcome":
		return "won_lost", `CASE
			WHEN LOWER(cf.opportunity_stage) = 'closed won' THEN 'closed_won'
			WHEN LOWER(cf.opportunity_stage) = 'closed lost' THEN 'closed_lost'
			WHEN TRIM(cf.opportunity_stage) <> '' THEN 'open_or_in_progress'
			ELSE 'unknown'
		END`, nil
	case "loss_reason":
		return "loss_reason", businessAnalysisOpportunityFieldExpr([]string{"LossReason", "Loss_Reason__c", "Closed_Lost_Reason__c", "Closed_Lost_Reason_Detail__c"}), nil
	default:
		return "", "", fmt.Errorf("unsupported business-analysis dimension %q", dimension)
	}
}

func businessAnalysisQuarterExpr(callDateExpr string) string {
	return `CASE
		WHEN substr(` + callDateExpr + `, 6, 2) IN ('01','02','03') THEN substr(` + callDateExpr + `, 1, 4) || '-Q1'
		WHEN substr(` + callDateExpr + `, 6, 2) IN ('04','05','06') THEN substr(` + callDateExpr + `, 1, 4) || '-Q2'
		WHEN substr(` + callDateExpr + `, 6, 2) IN ('07','08','09') THEN substr(` + callDateExpr + `, 1, 4) || '-Q3'
		WHEN substr(` + callDateExpr + `, 6, 2) IN ('10','11','12') THEN substr(` + callDateExpr + `, 1, 4) || '-Q4'
		ELSE ''
	END`
}

func businessAnalysisOpportunityFieldExpr(fieldNames []string) string {
	quoted := make([]string, 0, len(fieldNames))
	for _, name := range fieldNames {
		quoted = append(quoted, "'"+strings.ReplaceAll(name, "'", "''")+"'")
	}
	return `COALESCE((
		SELECT TRIM(f.field_value_text)
		  FROM call_context_fields f
		  JOIN call_context_objects o
		    ON o.call_id = f.call_id
		   AND o.object_key = f.object_key
		 WHERE f.call_id = cf.call_id
		   AND o.object_type = 'Opportunity'
		   AND f.field_name IN (` + strings.Join(quoted, ",") + `)
		   AND TRIM(f.field_value_text) <> ''
		 ORDER BY f.id
		 LIMIT 1
	), '')`
}

func businessAnalysisPartyTitleCountSQL(callAlias string) string {
	return `COALESCE((SELECT COUNT(1) FROM json_each(` + callAlias + `.raw_json, '$.parties') p WHERE TRIM(COALESCE(json_extract(p.value, '$.title'), json_extract(p.value, '$.jobTitle'), json_extract(p.value, '$.job_title'), '')) <> ''), 0) +
	       COALESCE((SELECT COUNT(1) FROM json_each(` + callAlias + `.raw_json, '$.metaData.parties') p WHERE TRIM(COALESCE(json_extract(p.value, '$.title'), json_extract(p.value, '$.jobTitle'), json_extract(p.value, '$.job_title'), '')) <> ''), 0)`
}

func businessAnalysisPersonTitleStatusSQL(callAlias string) string {
	return `CASE
		WHEN ` + businessAnalysisPartyTitleCountSQL(callAlias) + ` > 0 THEN 'available'
		WHEN EXISTS (SELECT 1 FROM call_context_objects po WHERE po.call_id = ` + callAlias + `.call_id AND po.object_type IN ('Contact', 'Lead')) THEN 'contact_or_lead_present_title_unverified'
		WHEN ` + callAlias + `.parties_count > 0 THEN 'participants_present_check_party_titles'
		ELSE 'missing_from_cache'
	END`
}

func businessAnalysisPersonTitleSourceSQL(callAlias string) string {
	return `CASE
		WHEN ` + businessAnalysisPartyTitleCountSQL(callAlias) + ` > 0 THEN 'call_parties'
		WHEN EXISTS (SELECT 1 FROM call_context_objects po WHERE po.call_id = ` + callAlias + `.call_id AND po.object_type IN ('Contact', 'Lead')) THEN 'contact_or_lead_object'
		ELSE ''
	END`
}

func businessAnalysisParticipantTitleLabelSQL(callAlias string) string {
	return `COALESCE(NULLIF(TRIM((
		SELECT COALESCE(json_extract(p.value, '$.title'), json_extract(p.value, '$.jobTitle'), json_extract(p.value, '$.job_title'), '')
		  FROM json_each(` + callAlias + `.raw_json, '$.parties') p
		 WHERE TRIM(COALESCE(json_extract(p.value, '$.title'), json_extract(p.value, '$.jobTitle'), json_extract(p.value, '$.job_title'), '')) <> ''
		 LIMIT 1
	)), ''), NULLIF(TRIM((
		SELECT COALESCE(json_extract(p.value, '$.title'), json_extract(p.value, '$.jobTitle'), json_extract(p.value, '$.job_title'), '')
		  FROM json_each(` + callAlias + `.raw_json, '$.metaData.parties') p
		 WHERE TRIM(COALESCE(json_extract(p.value, '$.title'), json_extract(p.value, '$.jobTitle'), json_extract(p.value, '$.job_title'), '')) <> ''
		 LIMIT 1
	)), ''), '')`
}

func businessAnalysisParticipantTitleLikeSQL(callAlias, placeholder string) string {
	return `(
		EXISTS (
			SELECT 1
			  FROM json_each(` + callAlias + `.raw_json, '$.parties') p
			 WHERE LOWER(TRIM(COALESCE(json_extract(p.value, '$.title'), json_extract(p.value, '$.jobTitle'), json_extract(p.value, '$.job_title'), ''))) LIKE '%' || LOWER(` + placeholder + `) || '%'
		)
		OR EXISTS (
			SELECT 1
			  FROM json_each(` + callAlias + `.raw_json, '$.metaData.parties') p
			 WHERE LOWER(TRIM(COALESCE(json_extract(p.value, '$.title'), json_extract(p.value, '$.jobTitle'), json_extract(p.value, '$.job_title'), ''))) LIKE '%' || LOWER(` + placeholder + `) || '%'
		)
		OR EXISTS (
			SELECT 1
			  FROM call_context_objects po
			  JOIN call_context_fields pf
			    ON pf.call_id = po.call_id
			   AND pf.object_key = po.object_key
			 WHERE po.call_id = ` + callAlias + `.call_id
			   AND po.object_type IN ('Contact', 'Lead')
			   AND pf.field_name IN ('Title', 'JobTitle', 'Job_Title__c', 'JobTitle__c')
			   AND LOWER(TRIM(pf.field_value_text)) LIKE '%' || LOWER(` + placeholder + `) || '%'
		)
	)`
}

func coverageHint(populated, total int64, label string) string {
	if total == 0 {
		return "no calls matched the filter"
	}
	if populated == 0 {
		return label + " missing from matched cohort"
	}
	if populated < total {
		return fmt.Sprintf("%s partially populated: %d/%d calls", label, populated, total)
	}
	return label + " populated for matched cohort"
}

func firstPositiveInt(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
