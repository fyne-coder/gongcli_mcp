package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

func (s *Store) UpsertTranscript(ctx context.Context, raw json.RawMessage) (*TranscriptRecord, error) {
	payload, err := decodeTranscript(raw)
	if err != nil {
		return nil, err
	}

	now := nowUTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO transcripts(call_id, raw_json, raw_sha256, segment_count, first_seen_at, updated_at)
		 VALUES(?, ?, ?, ?, ?, ?)
		 ON CONFLICT(call_id) DO UPDATE SET
			raw_json = excluded.raw_json,
			raw_sha256 = excluded.raw_sha256,
			segment_count = excluded.segment_count,
			updated_at = excluded.updated_at
		 WHERE
			transcripts.raw_sha256 IS NOT excluded.raw_sha256 OR
			transcripts.segment_count IS NOT excluded.segment_count`,
		payload.CallID,
		payload.RawJSON,
		payload.RawSHA256,
		len(payload.Segments),
		now,
		now,
	); err != nil {
		return nil, err
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM transcript_segments WHERE call_id = ?`, payload.CallID); err != nil {
		return nil, err
	}

	for _, segment := range payload.Segments {
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO transcript_segments(call_id, segment_index, speaker_id, start_ms, end_ms, text, raw_json)
			 VALUES(?, ?, ?, ?, ?, ?, ?)`,
			segment.CallID,
			segment.SegmentIndex,
			segment.SpeakerID,
			segment.StartMS,
			segment.EndMS,
			segment.Text,
			segment.RawJSON,
		); err != nil {
			return nil, err
		}
	}
	if err := invalidateProfileCallFactCacheTx(ctx, tx); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return s.transcriptByCallID(ctx, payload.CallID)
}
func (s *Store) FindCallsMissingTranscripts(ctx context.Context, limit int) ([]MissingTranscriptCall, error) {
	return s.FindCallsMissingTranscriptsByFilters(ctx, MissingTranscriptSearchParams{Limit: limit})
}
func (s *Store) FindCallsMissingTranscriptsByFilters(ctx context.Context, params MissingTranscriptSearchParams) ([]MissingTranscriptCall, error) {
	limit := boundedLimit(params.Limit, defaultMissingTranscriptsLimit, maxMissingTranscriptsLimit)

	query := `SELECT c.call_id, c.title, c.started_at
		   FROM calls c
		   LEFT JOIN transcripts t ON t.call_id = c.call_id`
	var args []any
	where := []string{`t.call_id IS NULL`}
	factWhere, factArgs, err := callFactFilterWhere("cf", callFactFilterParams{
		FromDate:        params.FromDate,
		ToDate:          params.ToDate,
		LifecycleBucket: params.LifecycleBucket,
		Scope:           params.Scope,
		System:          params.System,
		Direction:       params.Direction,
	}, false)
	if err != nil {
		return nil, err
	}
	if len(factWhere) > 0 {
		where = append(where, `EXISTS (SELECT 1 FROM call_facts cf WHERE cf.call_id = c.call_id AND `+strings.Join(factWhere, ` AND `)+`)`)
		args = append(args, factArgs...)
	}
	objectType := strings.TrimSpace(params.CRMObjectType)
	objectID := strings.TrimSpace(params.CRMObjectID)
	if objectType != "" || objectID != "" {
		subquery := []string{`o.call_id = c.call_id`}
		if objectType != "" {
			subquery = append(subquery, `o.object_type = ?`)
			args = append(args, objectType)
		}
		if objectID != "" {
			subquery = append(subquery, `o.object_id = ?`)
			args = append(args, objectID)
		}
		where = append(where, `EXISTS (SELECT 1 FROM call_context_objects o WHERE `+strings.Join(subquery, ` AND `)+`)`)
	}
	query += ` WHERE ` + strings.Join(where, ` AND `) + `
		  ORDER BY c.started_at DESC, c.call_id
		  LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []MissingTranscriptCall
	for rows.Next() {
		var row MissingTranscriptCall
		if err := rows.Scan(&row.CallID, &row.Title, &row.StartedAt); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
func (s *Store) SearchTranscriptSegments(ctx context.Context, query string, limit int) ([]TranscriptSearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("search query is required")
	}
	limit = boundedLimit(limit, defaultTranscriptSearchLimit, maxTranscriptSearchLimit)

	rows, err := s.db.QueryContext(
		ctx,
		`SELECT
			ts.call_id,
			ts.speaker_id,
				ts.segment_index,
				ts.start_ms,
				ts.end_ms,
				'',
				snippet(transcript_segments_fts, 0, '[', ']', '...', 12)
		FROM transcript_segments_fts
		JOIN transcript_segments ts ON ts.id = transcript_segments_fts.rowid
		WHERE transcript_segments_fts MATCH ?
		ORDER BY bm25(transcript_segments_fts), ts.call_id, ts.segment_index
		LIMIT ?`,
		query,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []TranscriptSearchResult
	for rows.Next() {
		var row TranscriptSearchResult
		if err := rows.Scan(
			&row.CallID,
			&row.SpeakerID,
			&row.SegmentIndex,
			&row.StartMS,
			&row.EndMS,
			&row.Text,
			&row.Snippet,
		); err != nil {
			return nil, err
		}
		results = append(results, row)
	}
	return results, rows.Err()
}
func (s *Store) ListOpportunitiesMissingTranscripts(ctx context.Context, params OpportunityMissingTranscriptParams) ([]OpportunityMissingTranscriptSummary, error) {
	limit := boundedLimit(params.Limit, defaultOpportunitySummaryLimit, maxOpportunitySummaryLimit)
	stageValues := cleanStringList(params.StageValues)

	query := `
WITH opportunities AS (
	SELECT o.call_id,
	       o.object_key,
	       o.object_id,
	       o.object_name
	  FROM call_context_objects o
	 WHERE o.object_type = 'Opportunity'
	   AND TRIM(o.object_id) <> ''
),
stage_fields AS (
	SELECT f.call_id,
	       f.object_key,
	       TRIM(f.field_value_text) AS stage
	  FROM call_context_fields f
	 WHERE f.field_name = 'StageName'
	   AND TRIM(f.field_value_text) <> ''
),
opportunity_calls AS (
	SELECT o.call_id,
	       o.object_key,
	       o.object_id,
	       o.object_name,
	       COALESCE(sf.stage, '') AS stage,
	       c.title,
	       c.started_at,
	       t.call_id AS transcript_call_id
	  FROM opportunities o
	  JOIN calls c
	    ON c.call_id = o.call_id
	  LEFT JOIN stage_fields sf
	    ON sf.call_id = o.call_id
	   AND sf.object_key = o.object_key
	  LEFT JOIN transcripts t
	    ON t.call_id = o.call_id
)`
	args := make([]any, 0, len(stageValues)+1)
	var where []string
	if len(stageValues) > 0 {
		placeholders := strings.TrimRight(strings.Repeat("?,", len(stageValues)), ",")
		where = append(where, `LOWER(TRIM(stage)) IN (`+placeholders+`)`)
		for _, value := range stageValues {
			args = append(args, strings.ToLower(strings.TrimSpace(value)))
		}
	}
	query += `
,
filtered_opportunity_calls AS (
	SELECT *
	  FROM opportunity_calls`
	if len(where) > 0 {
		query += `
	 WHERE ` + strings.Join(where, ` AND `)
	}
	query += `
)
SELECT object_id,
       COALESCE((SELECT oc2.object_name
                   FROM filtered_opportunity_calls oc2
                  WHERE oc2.object_id = foc.object_id
                  ORDER BY oc2.started_at DESC, oc2.call_id
                  LIMIT 1), '') AS object_name,
       COALESCE((SELECT oc2.stage
                   FROM filtered_opportunity_calls oc2
                  WHERE oc2.object_id = foc.object_id
                  ORDER BY oc2.started_at DESC, oc2.call_id
                  LIMIT 1), '') AS stage,
       COUNT(DISTINCT call_id) AS call_count,
       COUNT(DISTINCT CASE WHEN transcript_call_id IS NULL THEN call_id END) AS missing_transcript_count,
       COUNT(DISTINCT CASE WHEN transcript_call_id IS NOT NULL THEN call_id END) AS transcript_count,
       MAX(started_at) AS latest_call_at,
       COALESCE((SELECT oc2.call_id
                   FROM filtered_opportunity_calls oc2
                  WHERE oc2.object_id = foc.object_id
                  ORDER BY oc2.started_at DESC, oc2.call_id
                  LIMIT 1), '') AS latest_call_id
  FROM filtered_opportunity_calls foc
 GROUP BY object_id
HAVING COUNT(DISTINCT CASE WHEN transcript_call_id IS NULL THEN call_id END) > 0
 ORDER BY missing_transcript_count DESC, call_count DESC, latest_call_at DESC
 LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []OpportunityMissingTranscriptSummary
	for rows.Next() {
		var row OpportunityMissingTranscriptSummary
		if err := rows.Scan(
			&row.OpportunityID,
			&row.OpportunityName,
			&row.Stage,
			&row.CallCount,
			&row.MissingTranscriptCount,
			&row.TranscriptCount,
			&row.LatestCallAt,
			&row.LatestCallID,
		); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
func (s *Store) SearchTranscriptSegmentsByCRMContext(ctx context.Context, params TranscriptCRMSearchParams) ([]TranscriptCRMSearchResult, error) {
	queryText := strings.TrimSpace(params.Query)
	if queryText == "" {
		return nil, errors.New("search query is required")
	}
	objectType := strings.TrimSpace(params.ObjectType)
	if objectType == "" {
		return nil, errors.New("object type is required")
	}
	objectID := strings.TrimSpace(params.ObjectID)
	limit := boundedLimit(params.Limit, defaultTranscriptSearchLimit, maxTranscriptSearchLimit)

	query := `
WITH matched_segments AS (
	SELECT ts.id,
	       ts.call_id,
	       ts.speaker_id,
	       ts.segment_index,
	       ts.start_ms,
	       ts.end_ms,
	       snippet(transcript_segments_fts, 0, '[', ']', '...', 12) AS snippet,
	       bm25(transcript_segments_fts) AS rank
	  FROM transcript_segments_fts
	  JOIN transcript_segments ts
	    ON ts.id = transcript_segments_fts.rowid
	 WHERE transcript_segments_fts MATCH ?
),
matching_objects AS (
	SELECT call_id,
	       object_key,
	       object_type,
	       object_id,
	       object_name
	  FROM call_context_objects
	 WHERE object_type = ?
	   AND (? = '' OR object_id = ?)
)
SELECT m.call_id,
       c.title,
       c.started_at,
       COALESCE((SELECT mo.object_type
                   FROM matching_objects mo
                  WHERE mo.call_id = m.call_id
                  ORDER BY mo.object_id, mo.object_key
                  LIMIT 1), '') AS object_type,
       COALESCE((SELECT mo.object_id
                   FROM matching_objects mo
                  WHERE mo.call_id = m.call_id
                  ORDER BY mo.object_id, mo.object_key
                  LIMIT 1), '') AS object_id,
       COALESCE((SELECT mo.object_name
                   FROM matching_objects mo
                  WHERE mo.call_id = m.call_id
                  ORDER BY mo.object_id, mo.object_key
                  LIMIT 1), '') AS object_name,
       COALESCE((SELECT COUNT(DISTINCT mo.object_key)
                   FROM matching_objects mo
                  WHERE mo.call_id = m.call_id), 0) AS matching_object_count,
       m.speaker_id,
       m.segment_index,
       m.start_ms,
       m.end_ms,
       m.snippet
  FROM matched_segments m
  JOIN calls c
    ON c.call_id = m.call_id
 WHERE EXISTS (
	SELECT 1
	  FROM matching_objects mo
	 WHERE mo.call_id = m.call_id
 )
 ORDER BY m.rank, c.started_at DESC, m.call_id, m.segment_index
 LIMIT ?`
	args := []any{queryText, objectType, objectID, objectID, limit}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []TranscriptCRMSearchResult
	for rows.Next() {
		var row TranscriptCRMSearchResult
		if err := rows.Scan(
			&row.CallID,
			&row.Title,
			&row.StartedAt,
			&row.ObjectType,
			&row.ObjectID,
			&row.ObjectName,
			&row.MatchingObjectCount,
			&row.SpeakerID,
			&row.SegmentIndex,
			&row.StartMS,
			&row.EndMS,
			&row.Snippet,
		); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
func (s *Store) SearchTranscriptSegmentsByCallFacts(ctx context.Context, params TranscriptCallFactsSearchParams) ([]TranscriptCallFactsSearchResult, error) {
	queryText := strings.TrimSpace(params.Query)
	if queryText == "" {
		return nil, errors.New("search query is required")
	}
	limit := boundedLimit(params.Limit, defaultTranscriptSearchLimit, maxTranscriptSearchLimit)

	where := []string{`transcript_segments_fts MATCH ?`}
	args := []any{queryText}
	if value := strings.TrimSpace(params.FromDate); value != "" {
		date, err := normalizeDateFilter(value, "from_date")
		if err != nil {
			return nil, err
		}
		where = append(where, `cf.call_date >= ?`)
		args = append(args, date)
	}
	if value := strings.TrimSpace(params.ToDate); value != "" {
		date, err := normalizeDateFilter(value, "to_date")
		if err != nil {
			return nil, err
		}
		where = append(where, `cf.call_date <= ?`)
		args = append(args, date)
	}
	if strings.TrimSpace(params.FromDate) != "" && strings.TrimSpace(params.ToDate) != "" {
		fromDate, _ := normalizeDateFilter(params.FromDate, "from_date")
		toDate, _ := normalizeDateFilter(params.ToDate, "to_date")
		if fromDate > toDate {
			return nil, errors.New("from_date must be on or before to_date")
		}
	}
	if value := strings.TrimSpace(params.LifecycleBucket); value != "" {
		if !isKnownLifecycleBucket(value) {
			return nil, fmt.Errorf("unknown lifecycle bucket %q", value)
		}
		where = append(where, `cf.lifecycle_bucket = ?`)
		args = append(args, strings.ToLower(value))
	}
	if value := strings.TrimSpace(params.Scope); value != "" {
		scope, ok := normalizedScope(value)
		if !ok {
			return nil, errors.New("scope must be one of: External, Internal, Unknown")
		}
		where = append(where, `cf.scope = ?`)
		args = append(args, scope)
	}
	if value := strings.TrimSpace(params.System); value != "" {
		where = append(where, `cf.system = ?`)
		args = append(args, value)
	}
	if value := strings.TrimSpace(params.Direction); value != "" {
		where = append(where, `cf.direction = ?`)
		args = append(args, value)
	}

	query := `
SELECT cf.call_id,
       cf.started_at,
       cf.call_date,
       cf.call_month,
       cf.duration_seconds,
       cf.lifecycle_bucket,
       cf.scope,
       cf.system,
       cf.direction,
       ts.speaker_id,
       ts.segment_index,
       ts.start_ms,
       ts.end_ms,
       snippet(transcript_segments_fts, 0, '', '', '...', 18),
       substr(COALESCE((
	       SELECT group_concat(context_text, ' ')
	         FROM (
		       SELECT ctx.text AS context_text
		         FROM transcript_segments ctx
		        WHERE ctx.call_id = ts.call_id
		          AND ctx.segment_index BETWEEN ts.segment_index - 1 AND ts.segment_index + 1
		        ORDER BY ctx.segment_index
	         )
       ), ''), 1, 800)
  FROM transcript_segments_fts
  JOIN transcript_segments ts
    ON ts.id = transcript_segments_fts.rowid
  JOIN call_facts cf
    ON cf.call_id = ts.call_id
 WHERE ` + strings.Join(where, ` AND `) + `
 ORDER BY bm25(transcript_segments_fts), cf.started_at DESC, ts.segment_index
 LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]TranscriptCallFactsSearchResult, 0)
	for rows.Next() {
		var row TranscriptCallFactsSearchResult
		if err := rows.Scan(
			&row.CallID,
			&row.StartedAt,
			&row.CallDate,
			&row.CallMonth,
			&row.DurationSeconds,
			&row.LifecycleBucket,
			&row.Scope,
			&row.System,
			&row.Direction,
			&row.SpeakerID,
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
func (s *Store) SearchTranscriptQuotesWithAttribution(ctx context.Context, params TranscriptAttributionSearchParams) ([]TranscriptAttributionSearchResult, error) {
	queryText := strings.TrimSpace(params.Query)
	if queryText == "" {
		return nil, errors.New("search query is required")
	}
	limit := boundedLimit(params.Limit, defaultTranscriptSearchLimit, maxTranscriptSearchLimit)

	where := []string{`transcript_segments_fts MATCH ?`}
	args := []any{queryText}
	if value := strings.TrimSpace(params.FromDate); value != "" {
		date, err := normalizeDateFilter(value, "from_date")
		if err != nil {
			return nil, err
		}
		where = append(where, `COALESCE(cf.call_date, substr(c.started_at, 1, 10)) >= ?`)
		args = append(args, date)
	}
	if value := strings.TrimSpace(params.ToDate); value != "" {
		date, err := normalizeDateFilter(value, "to_date")
		if err != nil {
			return nil, err
		}
		where = append(where, `COALESCE(cf.call_date, substr(c.started_at, 1, 10)) <= ?`)
		args = append(args, date)
	}
	if strings.TrimSpace(params.FromDate) != "" && strings.TrimSpace(params.ToDate) != "" {
		fromDate, _ := normalizeDateFilter(params.FromDate, "from_date")
		toDate, _ := normalizeDateFilter(params.ToDate, "to_date")
		if fromDate > toDate {
			return nil, errors.New("from_date must be on or before to_date")
		}
	}
	if value := strings.TrimSpace(params.LifecycleBucket); value != "" {
		if !isKnownLifecycleBucket(value) {
			return nil, fmt.Errorf("unknown lifecycle bucket %q", value)
		}
		where = append(where, `cf.lifecycle_bucket = ?`)
		args = append(args, strings.ToLower(value))
	}
	factWhere, factArgs, err := callFactFilterWhere("cf", callFactFilterParams{
		Scope:            params.Scope,
		System:           params.System,
		Direction:        params.Direction,
		TranscriptStatus: params.TranscriptStatus,
	}, true)
	if err != nil {
		return nil, err
	}
	where = append(where, factWhere...)
	args = append(args, factArgs...)
	if value := strings.TrimSpace(params.Industry); value != "" {
		where = append(where, `EXISTS (
			SELECT 1
			  FROM call_context_objects filter_o
			  JOIN call_context_fields filter_f
			    ON filter_f.call_id = filter_o.call_id AND filter_f.object_key = filter_o.object_key
			 WHERE filter_o.call_id = ts.call_id
			   AND filter_o.object_type = 'Account'
			   AND filter_f.field_name = 'Industry'
			   AND LOWER(TRIM(filter_f.field_value_text)) LIKE '%' || LOWER(?) || '%'
		)`)
		args = append(args, value)
	}
	if value := strings.TrimSpace(params.AccountQuery); value != "" {
		where = append(where, `EXISTS (
			SELECT 1
			  FROM call_context_objects filter_o
			  JOIN call_context_fields filter_f
			    ON filter_f.call_id = filter_o.call_id AND filter_f.object_key = filter_o.object_key
			 WHERE filter_o.call_id = ts.call_id
			   AND filter_o.object_type = 'Account'
			   AND filter_f.field_name = 'Name'
			   AND LOWER(TRIM(filter_f.field_value_text)) LIKE '%' || LOWER(?) || '%'
		)`)
		args = append(args, value)
	}
	if value := strings.TrimSpace(params.OpportunityStage); value != "" {
		where = append(where, `EXISTS (
			SELECT 1
			  FROM call_context_objects filter_o
			  JOIN call_context_fields filter_f
			    ON filter_f.call_id = filter_o.call_id AND filter_f.object_key = filter_o.object_key
			 WHERE filter_o.call_id = ts.call_id
			   AND filter_o.object_type = 'Opportunity'
			   AND filter_f.field_name = 'StageName'
			   AND LOWER(TRIM(filter_f.field_value_text)) LIKE '%' || LOWER(?) || '%'
		)`)
		args = append(args, value)
	}

	selectedArgs := make([]any, 0, 3)
	selectedAccountWhere := []string{`o.object_type = 'Account'`}
	if value := strings.TrimSpace(params.Industry); value != "" {
		selectedAccountWhere = append(selectedAccountWhere, `EXISTS (
			SELECT 1
			  FROM call_context_fields selected_f
			 WHERE selected_f.call_id = o.call_id
			   AND selected_f.object_key = o.object_key
			   AND selected_f.field_name = 'Industry'
			   AND LOWER(TRIM(selected_f.field_value_text)) LIKE '%' || LOWER(?) || '%'
		)`)
		selectedArgs = append(selectedArgs, value)
	}
	if value := strings.TrimSpace(params.AccountQuery); value != "" {
		selectedAccountWhere = append(selectedAccountWhere, `EXISTS (
			SELECT 1
			  FROM call_context_fields selected_f
			 WHERE selected_f.call_id = o.call_id
			   AND selected_f.object_key = o.object_key
			   AND selected_f.field_name = 'Name'
			   AND LOWER(TRIM(selected_f.field_value_text)) LIKE '%' || LOWER(?) || '%'
		)`)
		selectedArgs = append(selectedArgs, value)
	}
	selectedOpportunityWhere := []string{`o.object_type = 'Opportunity'`}
	if value := strings.TrimSpace(params.OpportunityStage); value != "" {
		selectedOpportunityWhere = append(selectedOpportunityWhere, `EXISTS (
			SELECT 1
			  FROM call_context_fields selected_f
			 WHERE selected_f.call_id = o.call_id
			   AND selected_f.object_key = o.object_key
			   AND selected_f.field_name = 'StageName'
			   AND LOWER(TRIM(selected_f.field_value_text)) LIKE '%' || LOWER(?) || '%'
		)`)
		selectedArgs = append(selectedArgs, value)
	}

	query := `
WITH matched_segments AS (
	SELECT ts.call_id,
	       c.title,
	       c.started_at,
	       c.parties_count,
	       COALESCE(cf.call_date, substr(c.started_at, 1, 10)) AS call_date,
	       COALESCE(cf.lifecycle_bucket, '') AS lifecycle_bucket,
	       ts.segment_index,
	       ts.start_ms,
	       ts.end_ms,
	       snippet(transcript_segments_fts, 0, '', '', '...', 18) AS snippet,
	       COALESCE((SELECT COUNT(1) FROM json_each(c.raw_json, '$.parties') p WHERE TRIM(COALESCE(json_extract(p.value, '$.title'), json_extract(p.value, '$.jobTitle'), json_extract(p.value, '$.job_title'), '')) <> ''), 0) +
	       COALESCE((SELECT COUNT(1) FROM json_each(c.raw_json, '$.metaData.parties') p WHERE TRIM(COALESCE(json_extract(p.value, '$.title'), json_extract(p.value, '$.jobTitle'), json_extract(p.value, '$.job_title'), '')) <> ''), 0) AS party_title_count,
	       bm25(transcript_segments_fts) AS rank
	  FROM transcript_segments_fts
	  JOIN transcript_segments ts
	    ON ts.id = transcript_segments_fts.rowid
	  JOIN calls c
	    ON c.call_id = ts.call_id
	  LEFT JOIN call_facts cf
	    ON cf.call_id = ts.call_id
	 WHERE ` + strings.Join(where, ` AND `) + `
	 ORDER BY rank, c.started_at DESC, ts.call_id, ts.segment_index
	 LIMIT ?
),
selected_account AS (
	SELECT call_id, object_key
	  FROM (
		SELECT o.call_id,
		       o.object_key,
		       ROW_NUMBER() OVER (PARTITION BY o.call_id ORDER BY o.object_id, o.object_key) AS rn
		  FROM call_context_objects o
		  JOIN (SELECT DISTINCT call_id FROM matched_segments) m
		    ON m.call_id = o.call_id
		 WHERE ` + strings.Join(selectedAccountWhere, ` AND `) + `
	  )
	 WHERE rn = 1
),
selected_opportunity AS (
	SELECT call_id, object_key
	  FROM (
		SELECT o.call_id,
		       o.object_key,
		       ROW_NUMBER() OVER (PARTITION BY o.call_id ORDER BY o.object_id, o.object_key) AS rn
		  FROM call_context_objects o
		  JOIN (SELECT DISTINCT call_id FROM matched_segments) m
		    ON m.call_id = o.call_id
		 WHERE ` + strings.Join(selectedOpportunityWhere, ` AND `) + `
	  )
	 WHERE rn = 1
)
SELECT m.call_id,
       m.title,
       m.started_at,
       m.call_date,
       m.lifecycle_bucket,
       COALESCE((SELECT TRIM(f.field_value_text) FROM call_context_fields f JOIN selected_account sa ON sa.call_id = f.call_id AND sa.object_key = f.object_key WHERE f.call_id = m.call_id AND f.field_name = 'Name' LIMIT 1), '') AS account_name,
       COALESCE((SELECT TRIM(f.field_value_text) FROM call_context_fields f JOIN selected_account sa ON sa.call_id = f.call_id AND sa.object_key = f.object_key WHERE f.call_id = m.call_id AND f.field_name = 'Industry' LIMIT 1), '') AS account_industry,
       COALESCE((SELECT TRIM(f.field_value_text) FROM call_context_fields f JOIN selected_account sa ON sa.call_id = f.call_id AND sa.object_key = f.object_key WHERE f.call_id = m.call_id AND f.field_name = 'Website' LIMIT 1), '') AS account_website,
       COALESCE((SELECT TRIM(f.field_value_text) FROM call_context_fields f JOIN selected_opportunity so ON so.call_id = f.call_id AND so.object_key = f.object_key WHERE f.call_id = m.call_id AND f.field_name = 'Name' LIMIT 1), '') AS opportunity_name,
       COALESCE((SELECT TRIM(f.field_value_text) FROM call_context_fields f JOIN selected_opportunity so ON so.call_id = f.call_id AND so.object_key = f.object_key WHERE f.call_id = m.call_id AND f.field_name = 'StageName' LIMIT 1), '') AS opportunity_stage,
       COALESCE((SELECT TRIM(f.field_value_text) FROM call_context_fields f JOIN selected_opportunity so ON so.call_id = f.call_id AND so.object_key = f.object_key WHERE f.call_id = m.call_id AND f.field_name = 'Type' LIMIT 1), '') AS opportunity_type,
       COALESCE((SELECT TRIM(f.field_value_text) FROM call_context_fields f JOIN selected_opportunity so ON so.call_id = f.call_id AND so.object_key = f.object_key WHERE f.call_id = m.call_id AND f.field_name = 'CloseDate' LIMIT 1), '') AS opportunity_close_date,
       COALESCE((SELECT TRIM(f.field_value_text) FROM call_context_fields f JOIN selected_opportunity so ON so.call_id = f.call_id AND so.object_key = f.object_key WHERE f.call_id = m.call_id AND f.field_name = 'Probability' LIMIT 1), '') AS opportunity_probability,
       CASE WHEN m.parties_count > 0 THEN 'present' ELSE 'missing_from_cache' END AS participant_status,
       CASE
	       WHEN m.party_title_count > 0 THEN 'available'
	       WHEN EXISTS (
		       SELECT 1
		         FROM call_context_objects po
		        WHERE po.call_id = m.call_id
		          AND po.object_type IN ('Contact', 'Lead')
	       ) THEN 'contact_or_lead_present_title_unverified'
	       WHEN m.parties_count > 0 THEN 'participants_present_check_party_titles'
	       ELSE 'missing_from_cache'
       END AS person_title_status,
       CASE
	       WHEN m.party_title_count > 0 THEN 'call_parties'
	       WHEN EXISTS (
		       SELECT 1
		         FROM call_context_objects po
		        WHERE po.call_id = m.call_id
		          AND po.object_type IN ('Contact', 'Lead')
	       ) THEN 'contact_or_lead_object'
	       ELSE ''
       END AS person_title_source,
       m.segment_index,
       m.start_ms,
       m.end_ms,
       m.snippet,
       substr(COALESCE((
	       SELECT group_concat(context_text, ' ')
	         FROM (
		       SELECT ctx.text AS context_text
		         FROM transcript_segments ctx
		        WHERE ctx.call_id = m.call_id
		          AND ctx.segment_index BETWEEN m.segment_index - 1 AND m.segment_index + 1
		        ORDER BY ctx.segment_index
	         )
       ), ''), 1, 800) AS context_excerpt
  FROM matched_segments m
 ORDER BY m.rank, m.started_at DESC, m.call_id, m.segment_index
 LIMIT ?`
	args = append(args, limit)
	args = append(args, selectedArgs...)
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]TranscriptAttributionSearchResult, 0)
	for rows.Next() {
		var row TranscriptAttributionSearchResult
		if err := rows.Scan(
			&row.CallID,
			&row.Title,
			&row.StartedAt,
			&row.CallDate,
			&row.LifecycleBucket,
			&row.AccountName,
			&row.AccountIndustry,
			&row.AccountWebsite,
			&row.OpportunityName,
			&row.OpportunityStage,
			&row.OpportunityType,
			&row.OpportunityCloseDate,
			&row.OpportunityProbability,
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
func (s *Store) transcriptByCallID(ctx context.Context, callID string) (*TranscriptRecord, error) {
	var record TranscriptRecord
	err := s.db.QueryRowContext(
		ctx,
		`SELECT call_id, segment_count, raw_json, raw_sha256, first_seen_at, updated_at
		   FROM transcripts WHERE call_id = ?`,
		callID,
	).Scan(
		&record.CallID,
		&record.SegmentCount,
		&record.RawJSON,
		&record.RawSHA256,
		&record.FirstSeenAt,
		&record.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &record, nil
}
func decodeTranscript(raw json.RawMessage) (*transcriptPayload, error) {
	normalized, err := normalizeJSON(raw)
	if err != nil {
		return nil, err
	}

	var envelope map[string]any
	if err := json.Unmarshal(normalized, &envelope); err != nil {
		return nil, err
	}

	record := envelope
	if wrapped, ok := envelope["callTranscripts"]; ok {
		items, ok := wrapped.([]any)
		if !ok || len(items) != 1 {
			return nil, errors.New("transcript payload must contain exactly one call transcript")
		}
		itemMap, ok := items[0].(map[string]any)
		if !ok {
			return nil, errors.New("call transcript item must be an object")
		}
		record = itemMap
		normalized, err = normalizeJSONValue(record)
		if err != nil {
			return nil, err
		}
	}

	callID := firstString(record, "callId", "id")
	if callID == "" {
		return nil, errors.New("transcript payload missing callId")
	}

	blocks, ok := record["transcript"].([]any)
	if !ok {
		return nil, errors.New("transcript payload missing transcript array")
	}

	segments := make([]TranscriptSegment, 0)
	index := 0
	for _, block := range blocks {
		blockMap, ok := block.(map[string]any)
		if !ok {
			continue
		}
		speakerID := firstString(blockMap, "speakerId", "speakerID")
		sentences, ok := blockMap["sentences"].([]any)
		if !ok {
			continue
		}
		for _, sentence := range sentences {
			sentenceMap, ok := sentence.(map[string]any)
			if !ok {
				continue
			}
			segmentRaw, err := normalizeJSONValue(map[string]any{
				"speakerId": speakerID,
				"start":     sentenceMap["start"],
				"end":       sentenceMap["end"],
				"text":      sentenceMap["text"],
			})
			if err != nil {
				return nil, err
			}
			segments = append(segments, TranscriptSegment{
				CallID:       callID,
				SegmentIndex: index,
				SpeakerID:    speakerID,
				StartMS:      int64FromAny(sentenceMap["start"]),
				EndMS:        int64FromAny(sentenceMap["end"]),
				Text:         strings.TrimSpace(firstString(sentenceMap, "text")),
				RawJSON:      segmentRaw,
			})
			index++
		}
	}

	return &transcriptPayload{
		CallID:    callID,
		RawJSON:   normalized,
		RawSHA256: sha256Hex(normalized),
		Segments:  segments,
	}, nil
}
