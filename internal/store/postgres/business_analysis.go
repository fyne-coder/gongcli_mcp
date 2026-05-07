package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
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
	defaultAIHighlightsLimit           = 25
	maxAIHighlightsLimit               = 100
)

const postgresAIHighlightsFunctionSQL = `
CREATE OR REPLACE FUNCTION gongmcp_list_call_ai_highlights(call_ids_json text, row_limit integer)
RETURNS TABLE(call_id text, highlight_index integer, highlight_type text, highlight_text text, source_path text, updated_at text)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = public, pg_temp
AS $function$
WITH requested AS (
	SELECT DISTINCT TRIM(value) AS call_id
	  FROM jsonb_array_elements_text(COALESCE(NULLIF(call_ids_json, ''), '[]')::jsonb) AS item(value)
	 WHERE TRIM(value) <> ''
	 LIMIT 25
),
bounded AS (
	SELECT LEAST(GREATEST(COALESCE(row_limit, 25), 1), 100) AS limit_value
)
SELECT h.call_id,
       h.highlight_index,
       h.highlight_type,
       h.highlight_text,
       h.source_path,
       h.updated_at
  FROM call_ai_highlights h
  JOIN requested r
    ON r.call_id = h.call_id
 ORDER BY h.call_id, h.highlight_index
 LIMIT (SELECT limit_value FROM bounded)
$function$;
REVOKE ALL ON FUNCTION gongmcp_list_call_ai_highlights(text, integer) FROM PUBLIC;

CREATE OR REPLACE FUNCTION gongmcp_call_ai_highlights_count()
RETURNS bigint
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = public, pg_temp
AS $function$
SELECT COUNT(*)::bigint FROM call_ai_highlights
$function$;
REVOKE ALL ON FUNCTION gongmcp_call_ai_highlights_count() FROM PUBLIC;

CREATE OR REPLACE FUNCTION gongmcp_call_drilldown_transcript_evidence(call_id_arg text, theme_query_arg text, row_limit integer)
RETURNS TABLE(
        call_id text,
        segment_index integer,
        speaker_id text,
        start_ms bigint,
        end_ms bigint,
        snippet text,
        context_excerpt text,
        person_title_status text,
        person_title_source text,
        attribution_source text,
        attribution_confidence text,
        person_title text
)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = public, pg_temp
AS $function$
WITH q AS (
        SELECT websearch_to_tsquery('simple', left(theme_query_arg, 160)) AS query
),
matched AS (
        SELECT ts.call_id,
               ts.segment_index,
               ts.speaker_id,
               ts.start_ms,
               ts.end_ms,
               ts_headline('simple', ts.text, q.query, 'StartSel=, StopSel=, MaxWords=18, MinWords=4, ShortWord=2') AS snippet,
               ts_rank_cd(ts.search_vector, q.query) AS rank
          FROM transcript_segments ts,
               q
         WHERE ts.call_id = call_id_arg
           AND theme_query_arg <> ''
           AND ts.search_vector @@ q.query
         ORDER BY rank DESC, ts.segment_index
         LIMIT LEAST(GREATEST(COALESCE(row_limit, 10), 1), 25)
),
parties AS (
        SELECT DISTINCT
                NULLIF(TRIM(BOTH '"' FROM COALESCE(p.value->>'speakerId', p.value->>'speaker_id', p.value->>'id', '')), '') AS speaker_key,
                NULLIF(TRIM(COALESCE(p.value->>'title', p.value->>'jobTitle', p.value->>'job_title', '')), '') AS title
          FROM calls c,
               LATERAL (
                        SELECT jsonb_array_elements(CASE WHEN jsonb_typeof(c.raw_json->'parties') = 'array' THEN c.raw_json->'parties' ELSE '[]'::jsonb END) AS value
                        UNION ALL
                        SELECT jsonb_array_elements(CASE WHEN jsonb_typeof(c.raw_json->'metaData'->'parties') = 'array' THEN c.raw_json->'metaData'->'parties' ELSE '[]'::jsonb END) AS value
               ) AS p
         WHERE c.call_id = call_id_arg
),
attribution AS (
        SELECT m.segment_index,
               m.speaker_id,
               COUNT(p.speaker_key) FILTER (WHERE p.speaker_key IS NOT NULL AND p.speaker_key = m.speaker_id) AS match_count,
               MAX(p.title) FILTER (WHERE p.speaker_key IS NOT NULL AND p.speaker_key = m.speaker_id AND p.title IS NOT NULL) AS title_when_present,
               COUNT(p.title) FILTER (WHERE p.speaker_key IS NOT NULL AND p.speaker_key = m.speaker_id AND p.title IS NOT NULL) AS title_count
          FROM matched m
          LEFT JOIN parties p ON TRUE
         GROUP BY m.segment_index, m.speaker_id
)
SELECT m.call_id,
       m.segment_index,
       m.speaker_id,
       m.start_ms,
       m.end_ms,
       m.snippet,
       left(COALESCE((
               SELECT string_agg(ctx.text, ' ' ORDER BY ctx.segment_index)
                 FROM transcript_segments ctx
                WHERE ctx.call_id = m.call_id
                  AND ctx.segment_index BETWEEN m.segment_index - 1 AND m.segment_index + 1
       ), ''), 800) AS context_excerpt,
       CASE
               WHEN COALESCE(NULLIF(TRIM(m.speaker_id), ''), '') = '' THEN 'speaker_unmatched'
               WHEN a.match_count IS NULL OR a.match_count = 0 THEN 'speaker_unmatched'
               WHEN a.match_count > 1 THEN 'speaker_ambiguous'
               WHEN a.title_count > 0 THEN 'available'
               ELSE 'participant_matched_title_missing'
       END AS person_title_status,
       CASE
               WHEN COALESCE(NULLIF(TRIM(m.speaker_id), ''), '') = '' THEN ''
               WHEN a.match_count IS NULL OR a.match_count = 0 THEN ''
               ELSE 'call_parties'
       END AS person_title_source,
       CASE
               WHEN COALESCE(NULLIF(TRIM(m.speaker_id), ''), '') = '' THEN 'unmatched'
               WHEN a.match_count IS NULL OR a.match_count = 0 THEN 'unmatched'
               ELSE 'gong_party'
       END AS attribution_source,
       CASE
               WHEN COALESCE(NULLIF(TRIM(m.speaker_id), ''), '') = '' THEN 'unmatched'
               WHEN a.match_count IS NULL OR a.match_count = 0 THEN 'unmatched'
               WHEN a.match_count > 1 THEN 'ambiguous'
               ELSE 'exact_speaker_id'
       END AS attribution_confidence,
       CASE
               WHEN a.match_count = 1 AND a.title_count > 0 THEN a.title_when_present
               ELSE ''
       END AS person_title
  FROM matched m
  LEFT JOIN attribution a ON a.segment_index = m.segment_index AND a.speaker_id = m.speaker_id
 ORDER BY m.rank DESC, m.segment_index
$function$;
REVOKE ALL ON FUNCTION gongmcp_call_drilldown_transcript_evidence(text, text, integer) FROM PUBLIC;
`

// ListAIHighlights returns bounded, typed Gong AI highlight rows from the
// Postgres call_ai_highlights read model. The serving DB never contains rows
// for governance-restricted calls, so they remain absent without any
// additional filtering here. Raw highlight JSON is intentionally not
// exposed; only the typed text/type/index columns are returned.
func (s *Store) ListAIHighlights(ctx context.Context, params sqlite.AIHighlightListParams) ([]sqlite.AIHighlightRow, error) {
	ids := dedupeNonEmptyStrings(params.CallIDs)
	if len(ids) == 0 {
		return nil, errors.New("call_ids is required for evidence.highlights.list")
	}
	limit := boundedLimit(params.Limit, defaultAIHighlightsLimit, maxAIHighlightsLimit)
	idsJSON, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT call_id, highlight_index, highlight_type, highlight_text, source_path, updated_at
  FROM gongmcp_list_call_ai_highlights($1, $2)`, string(idsJSON), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []sqlite.AIHighlightRow{}
	for rows.Next() {
		var row sqlite.AIHighlightRow
		if err := rows.Scan(&row.CallID, &row.HighlightIndex, &row.HighlightType, &row.HighlightText, &row.SourcePath, &row.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// CallDrilldownEvidence returns bounded transcript excerpts scoped to a single
// call. The Postgres path uses a dedicated SECURITY DEFINER function so
// scoped MCP roles can drill into one call without being granted broad
// transcript_segments search rights.
func (s *Store) CallDrilldownEvidence(ctx context.Context, params sqlite.CallDrilldownEvidenceParams) ([]sqlite.CallDrilldownEvidenceRow, error) {
	callID := strings.TrimSpace(params.CallID)
	if callID == "" {
		return nil, errors.New("call_id is required for call_drilldown evidence")
	}
	queryText := strings.TrimSpace(params.Query)
	if queryText == "" {
		return nil, nil
	}
	if err := validatePostgresBusinessAnalysisSearchText(queryText, "theme_query"); err != nil {
		return nil, err
	}
	limit := params.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > 25 {
		limit = 25
	}
	rows, err := s.db.QueryContext(ctx, `SELECT call_id,
       segment_index,
       speaker_id,
       start_ms,
       end_ms,
       snippet,
       context_excerpt,
       person_title_status,
       person_title_source,
       attribution_source,
       attribution_confidence,
       person_title
  FROM gongmcp_call_drilldown_transcript_evidence($1, $2, $3)`,
		callID,
		queryText,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []sqlite.CallDrilldownEvidenceRow{}
	for rows.Next() {
		var row sqlite.CallDrilldownEvidenceRow
		if err := rows.Scan(
			&row.CallID,
			&row.SegmentIndex,
			&row.SpeakerID,
			&row.StartMS,
			&row.EndMS,
			&row.Snippet,
			&row.ContextExcerpt,
			&row.PersonTitleStatus,
			&row.PersonTitleSource,
			&row.AttributionSource,
			&row.AttributionConfidence,
			&row.PersonTitle,
		); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func dedupeNonEmptyStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, raw := range values {
		v := strings.TrimSpace(raw)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

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
	if queryText == "" && !params.BroadDiscovery {
		return nil, errors.New("query is required for business-analysis evidence searches")
	}
	var rows *sql.Rows
	if params.BroadDiscovery {
		rows, err = s.db.QueryContext(ctx, `SELECT call_id, title, started_at, call_date, call_month, lifecycle_bucket, account_industry, account_name, opportunity_name, opportunity_stage, opportunity_type, opportunity_probability, opportunity_close_date, participant_status, person_title_status, person_title_source, segment_index, start_ms, end_ms, snippet, context_excerpt
  FROM `+s.postgresBusinessAnalysisThemeSeedFunction()+`($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)`,
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
	} else {
		rows, err = s.db.QueryContext(ctx, `SELECT call_id, title, started_at, call_date, call_month, lifecycle_bucket, account_industry, account_name, opportunity_name, opportunity_stage, opportunity_type, opportunity_probability, opportunity_close_date, participant_status, person_title_status, person_title_source, segment_index, start_ms, end_ms, snippet, context_excerpt
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
	}
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
		if s.readOnlyOptions.AllowBusinessAnalysisRawIDs {
			return "gongmcp_business_analysis_calls"
		}
		return "gongmcp_business_analysis_calls_sanitized"
	}
	return "gongmcp_business_analysis_calls"
}

func (s *Store) postgresBusinessAnalysisEvidenceFunction() string {
	if s.readOnlyOptions.EnforceAllowedColumnBoundary {
		if s.readOnlyOptions.AllowBusinessAnalysisRawIDs {
			return "gongmcp_business_analysis_evidence"
		}
		return "gongmcp_business_analysis_evidence_sanitized"
	}
	return "gongmcp_business_analysis_evidence"
}

func (s *Store) postgresBusinessAnalysisThemeSeedFunction() string {
	if s.readOnlyOptions.EnforceAllowedColumnBoundary {
		if s.readOnlyOptions.AllowBusinessAnalysisRawIDs {
			return "gongmcp_business_analysis_theme_seed_sample"
		}
		return "gongmcp_business_analysis_theme_seed_sample_sanitized"
	}
	return "gongmcp_business_analysis_theme_seed_sample"
}

func (s *Store) postgresTranscriptQuoteAttributionFunction() string {
	if s.readOnlyOptions.EnforceAllowedColumnBoundary {
		if s.readOnlyOptions.AllowBusinessAnalysisRawIDs {
			return "gongmcp_search_transcript_quotes_with_attribution"
		}
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
