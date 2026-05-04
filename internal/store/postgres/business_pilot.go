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
	defaultLifecycleLimit = 25
	maxLifecycleLimit     = 1000
	defaultCallFactsLimit = 25
	maxCallFactsLimit     = 1000
)

func (s *Store) ListLifecycleBucketDefinitions(ctx context.Context) ([]sqlite.LifecycleBucketDefinition, error) {
	return postgresLifecycleBucketDefinitions(), nil
}

func (s *Store) ListLifecycleBucketDefinitionsWithSource(ctx context.Context, requested string) ([]sqlite.LifecycleBucketDefinition, *sqlite.ProfileQueryInfo, error) {
	info, err := postgresBuiltinLifecycleInfo(requested)
	if err != nil {
		return nil, nil, err
	}
	rows, err := s.ListLifecycleBucketDefinitions(ctx)
	return rows, info, err
}

func (s *Store) SummarizeCallsByLifecycle(ctx context.Context, params sqlite.LifecycleSummaryParams) ([]sqlite.LifecycleBucketSummary, error) {
	bucket := strings.TrimSpace(params.Bucket)
	if bucket != "" && !postgresKnownLifecycleBucket(bucket) {
		return nil, fmt.Errorf("unknown lifecycle bucket %q", bucket)
	}
	args := []any{}
	where := ""
	if bucket != "" {
		where = " WHERE lifecycle_bucket = " + postgresAddArg(&args, bucket)
	}
	rows, err := s.db.QueryContext(ctx, `
WITH facts AS (`+postgresCallFactsSQL()+`)
SELECT lifecycle_bucket,
       COUNT(*) AS call_count,
       COALESCE(SUM(CASE WHEN transcript_present THEN 1 ELSE 0 END), 0) AS transcript_count,
       COALESCE(SUM(CASE WHEN NOT transcript_present THEN 1 ELSE 0 END), 0) AS missing_transcript_count,
       COALESCE(SUM(CASE WHEN opportunity_count > 0 THEN 1 ELSE 0 END), 0) AS opportunity_call_count,
       COALESCE(SUM(CASE WHEN account_count > 0 THEN 1 ELSE 0 END), 0) AS account_call_count,
       COALESCE(SUM(duration_seconds), 0) AS total_duration_seconds,
       COALESCE(MAX(started_at), '') AS latest_call_at,
       '' AS latest_call_id,
       COALESCE(SUM(CASE WHEN lifecycle_confidence = 'high' THEN 1 ELSE 0 END), 0) AS high_confidence_calls,
       COALESCE(SUM(CASE WHEN lifecycle_confidence = 'medium' THEN 1 ELSE 0 END), 0) AS medium_confidence_calls,
       COALESCE(SUM(CASE WHEN lifecycle_confidence NOT IN ('high', 'medium') THEN 1 ELSE 0 END), 0) AS low_confidence_calls
  FROM facts`+where+`
 GROUP BY lifecycle_bucket
 ORDER BY `+postgresLifecycleOrderSQL("lifecycle_bucket")+`, call_count DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []sqlite.LifecycleBucketSummary{}
	for rows.Next() {
		var row sqlite.LifecycleBucketSummary
		if err := rows.Scan(&row.Bucket, &row.CallCount, &row.TranscriptCount, &row.MissingTranscriptCount, &row.OpportunityCallCount, &row.AccountCallCount, &row.TotalDurationSeconds, &row.LatestCallAt, &row.LatestCallID, &row.HighConfidenceCalls, &row.MediumConfidenceCalls, &row.LowConfidenceCalls); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) SummarizeCallsByLifecycleWithSource(ctx context.Context, params sqlite.LifecycleSummaryParams) ([]sqlite.LifecycleBucketSummary, *sqlite.ProfileQueryInfo, error) {
	info, err := postgresBuiltinLifecycleInfo(params.LifecycleSource)
	if err != nil {
		return nil, nil, err
	}
	rows, err := s.SummarizeCallsByLifecycle(ctx, sqlite.LifecycleSummaryParams{Bucket: params.Bucket, LifecycleSource: "builtin"})
	return rows, info, err
}

func (s *Store) SearchCallsByLifecycle(ctx context.Context, params sqlite.LifecycleCallSearchParams) ([]sqlite.LifecycleCallSearchResult, error) {
	bucket := strings.TrimSpace(params.Bucket)
	if bucket != "" && !postgresKnownLifecycleBucket(bucket) {
		return nil, fmt.Errorf("unknown lifecycle bucket %q", bucket)
	}
	limit := boundedLimit(params.Limit, defaultLifecycleLimit, maxLifecycleLimit)
	args := []any{}
	where := []string{}
	if bucket != "" {
		where = append(where, "lifecycle_bucket = "+postgresAddArg(&args, bucket))
	}
	if params.MissingTranscriptsOnly {
		where = append(where, "NOT transcript_present")
	}
	whereSQL := ""
	if len(where) > 0 {
		whereSQL = " WHERE " + strings.Join(where, " AND ")
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, `
WITH facts AS (`+postgresCallFactsSQL()+`)
SELECT call_id, title, started_at, duration_seconds, lifecycle_bucket, lifecycle_confidence, lifecycle_reason, lifecycle_evidence_fields, opportunity_count, account_count, transcript_present
  FROM facts`+whereSQL+`
 ORDER BY started_at DESC, call_id
 LIMIT `+fmt.Sprintf("$%d", len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []sqlite.LifecycleCallSearchResult{}
	for rows.Next() {
		var row sqlite.LifecycleCallSearchResult
		var evidence string
		if err := rows.Scan(&row.CallID, &row.Title, &row.StartedAt, &row.DurationSeconds, &row.Bucket, &row.Confidence, &row.Reason, &evidence, &row.OpportunityCount, &row.AccountCount, &row.TranscriptPresent); err != nil {
			return nil, err
		}
		row.EvidenceFields = splitPostgresEvidenceFields(evidence)
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) SearchCallsByLifecycleWithSource(ctx context.Context, params sqlite.LifecycleCallSearchParams) ([]sqlite.LifecycleCallSearchResult, *sqlite.ProfileQueryInfo, error) {
	info, err := postgresBuiltinLifecycleInfo(params.LifecycleSource)
	if err != nil {
		return nil, nil, err
	}
	rows, err := s.SearchCallsByLifecycle(ctx, params)
	return rows, info, err
}

func (s *Store) PrioritizeTranscriptsByLifecycle(ctx context.Context, params sqlite.LifecycleTranscriptPriorityParams) ([]sqlite.LifecycleTranscriptPriority, error) {
	bucket := strings.TrimSpace(params.Bucket)
	if bucket != "" && !postgresKnownLifecycleBucket(bucket) {
		return nil, fmt.Errorf("unknown lifecycle bucket %q", bucket)
	}
	limit := boundedLimit(params.Limit, defaultLifecycleLimit, maxLifecycleLimit)
	args := []any{}
	where := []string{"NOT transcript_present"}
	if bucket != "" {
		where = append(where, "lifecycle_bucket = "+postgresAddArg(&args, bucket))
	}
	if value := strings.TrimSpace(params.FromDate); value != "" {
		date, err := normalizePostgresDateFilter(value, "from_date")
		if err != nil {
			return nil, err
		}
		where = append(where, "call_date >= "+postgresAddArg(&args, date))
	}
	if value := strings.TrimSpace(params.ToDate); value != "" {
		date, err := normalizePostgresDateFilter(value, "to_date")
		if err != nil {
			return nil, err
		}
		where = append(where, "call_date <= "+postgresAddArg(&args, date))
	}
	if strings.TrimSpace(params.FromDate) != "" && strings.TrimSpace(params.ToDate) != "" && strings.TrimSpace(params.FromDate) > strings.TrimSpace(params.ToDate) {
		return nil, errors.New("from_date must be on or before to_date")
	}
	if value := strings.TrimSpace(params.Scope); value != "" {
		scope, ok := normalizePostgresScope(value)
		if !ok {
			return nil, errors.New("scope must be one of: External, Internal, Unknown")
		}
		where = append(where, "scope = "+postgresAddArg(&args, scope))
	}
	if value := strings.TrimSpace(params.System); value != "" {
		where = append(where, "system = "+postgresAddArg(&args, value))
	}
	if value := strings.TrimSpace(params.Direction); value != "" {
		where = append(where, "direction = "+postgresAddArg(&args, value))
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, `
WITH facts AS (`+postgresCallFactsSQL()+`)
SELECT call_id,
       title,
       started_at,
       duration_seconds,
       system,
       direction,
       scope,
       lifecycle_bucket,
       lifecycle_confidence,
       lifecycle_reason,
       lifecycle_evidence_fields,
       (
         CASE lifecycle_bucket
           WHEN 'late_stage_sales' THEN 100
           WHEN 'renewal' THEN 95
           WHEN 'upsell_expansion' THEN 90
           WHEN 'closed_won_lost_review' THEN 85
           WHEN 'customer_success_account' THEN 75
           WHEN 'active_sales_pipeline' THEN 70
           WHEN 'partner' THEN 60
           WHEN 'outbound_prospecting' THEN 20
           ELSE 10
         END
         + CASE lifecycle_confidence WHEN 'high' THEN 20 WHEN 'medium' THEN 10 ELSE 0 END
         + CASE WHEN scope = 'External' THEN 25 ELSE 0 END
         + CASE WHEN direction = 'Conference' THEN 20 ELSE 0 END
         + CASE WHEN duration_seconds >= 1800 THEN 20 WHEN duration_seconds >= 600 THEN 10 ELSE 0 END
         + CASE WHEN opportunity_count > 0 THEN 10 ELSE 0 END
         - CASE WHEN duration_seconds > 0 AND duration_seconds < 300 AND direction <> 'Conference' THEN 20 ELSE 0 END
       ) AS priority_score
  FROM facts
 WHERE `+strings.Join(where, " AND ")+`
 ORDER BY priority_score DESC, started_at DESC, call_id
 LIMIT `+fmt.Sprintf("$%d", len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []sqlite.LifecycleTranscriptPriority{}
	for rows.Next() {
		var row sqlite.LifecycleTranscriptPriority
		var evidence string
		if err := rows.Scan(&row.CallID, &row.Title, &row.StartedAt, &row.DurationSeconds, &row.System, &row.Direction, &row.Scope, &row.Bucket, &row.Confidence, &row.Reason, &evidence, &row.PriorityScore); err != nil {
			return nil, err
		}
		row.EvidenceFields = splitPostgresEvidenceFields(evidence)
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) PrioritizeTranscriptsByLifecycleWithSource(ctx context.Context, params sqlite.LifecycleTranscriptPriorityParams) ([]sqlite.LifecycleTranscriptPriority, *sqlite.ProfileQueryInfo, error) {
	info, err := postgresBuiltinLifecycleInfo(params.LifecycleSource)
	if err != nil {
		return nil, nil, err
	}
	rows, err := s.PrioritizeTranscriptsByLifecycle(ctx, params)
	return rows, info, err
}

func (s *Store) SummarizeCallFacts(ctx context.Context, params sqlite.CallFactsSummaryParams) ([]sqlite.CallFactsSummaryRow, error) {
	groupBy, column, err := postgresCallFactGroupColumn(params.GroupBy)
	if err != nil {
		return nil, err
	}
	limit := boundedLimit(params.Limit, defaultCallFactsLimit, maxCallFactsLimit)
	where, args, err := postgresCallFactsWhere(params)
	if err != nil {
		return nil, err
	}
	whereSQL := ""
	if len(where) > 0 {
		whereSQL = " WHERE " + strings.Join(where, " AND ")
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, `
WITH facts AS (`+postgresCallFactsSQL()+`)
SELECT '`+groupBy+`' AS group_by,
       COALESCE(NULLIF(TRIM(`+column+`), ''), '<blank>') AS group_value,
       COUNT(*) AS call_count,
       COALESCE(SUM(CASE WHEN transcript_present THEN 1 ELSE 0 END), 0) AS transcript_count,
       COALESCE(SUM(CASE WHEN NOT transcript_present THEN 1 ELSE 0 END), 0) AS missing_transcript_count,
       COALESCE(SUM(CASE WHEN opportunity_count > 0 THEN 1 ELSE 0 END), 0) AS opportunity_call_count,
       COALESCE(SUM(CASE WHEN account_count > 0 THEN 1 ELSE 0 END), 0) AS account_call_count,
       COALESCE(SUM(CASE WHEN scope = 'External' THEN 1 ELSE 0 END), 0) AS external_call_count,
       COALESCE(SUM(CASE WHEN scope = 'Internal' THEN 1 ELSE 0 END), 0) AS internal_call_count,
       COALESCE(SUM(CASE WHEN scope = 'Unknown' THEN 1 ELSE 0 END), 0) AS unknown_scope_call_count,
       COALESCE(SUM(duration_seconds), 0) AS total_duration_seconds,
       COALESCE(AVG(duration_seconds), 0) AS avg_duration_seconds,
       COALESCE(MAX(started_at), '') AS latest_call_at
  FROM facts`+whereSQL+`
 GROUP BY group_value
 ORDER BY call_count DESC, group_value
 LIMIT `+fmt.Sprintf("$%d", len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []sqlite.CallFactsSummaryRow{}
	for rows.Next() {
		var row sqlite.CallFactsSummaryRow
		if err := rows.Scan(&row.GroupBy, &row.GroupValue, &row.CallCount, &row.TranscriptCount, &row.MissingTranscriptCount, &row.OpportunityCallCount, &row.AccountCallCount, &row.ExternalCallCount, &row.InternalCallCount, &row.UnknownScopeCallCount, &row.TotalDurationSeconds, &row.AvgDurationSeconds, &row.LatestCallAt); err != nil {
			return nil, err
		}
		row.TranscriptCoverageRate = postgresRate(row.TranscriptCount, row.CallCount)
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) SummarizeCallFactsWithSource(ctx context.Context, params sqlite.CallFactsSummaryParams) ([]sqlite.CallFactsSummaryRow, *sqlite.ProfileQueryInfo, error) {
	info, err := postgresBuiltinLifecycleInfo(params.LifecycleSource)
	if err != nil {
		return nil, nil, err
	}
	rows, err := s.SummarizeCallFacts(ctx, params)
	return rows, info, err
}

func (s *Store) CallFactsCoverage(ctx context.Context) (*sqlite.CallFactsCoverage, error) {
	var coverage sqlite.CallFactsCoverage
	if err := s.db.QueryRowContext(ctx, `
WITH facts AS (`+postgresCallFactsSQL()+`)
SELECT COUNT(*) AS total_calls,
       COALESCE(SUM(CASE WHEN transcript_present THEN 1 ELSE 0 END), 0) AS transcript_count,
       COALESCE(SUM(CASE WHEN NOT transcript_present THEN 1 ELSE 0 END), 0) AS missing_transcript_count,
       COALESCE(SUM(CASE WHEN opportunity_count > 0 THEN 1 ELSE 0 END), 0) AS opportunity_call_count,
       COALESCE(SUM(CASE WHEN account_count > 0 THEN 1 ELSE 0 END), 0) AS account_call_count,
       COALESCE(SUM(CASE WHEN scope = 'External' THEN 1 ELSE 0 END), 0) AS external_call_count,
       COALESCE(SUM(CASE WHEN scope = 'Internal' THEN 1 ELSE 0 END), 0) AS internal_call_count,
       COALESCE(SUM(CASE WHEN scope = 'Unknown' THEN 1 ELSE 0 END), 0) AS unknown_scope_call_count,
       0 AS purpose_populated_calls,
       0 AS calendar_call_count,
       COALESCE(SUM(duration_seconds), 0) AS total_duration_seconds
  FROM facts`).Scan(&coverage.TotalCalls, &coverage.TranscriptCount, &coverage.MissingTranscriptCount, &coverage.OpportunityCallCount, &coverage.AccountCallCount, &coverage.ExternalCallCount, &coverage.InternalCallCount, &coverage.UnknownScopeCallCount, &coverage.PurposePopulatedCalls, &coverage.CalendarCallCount, &coverage.TotalDurationSeconds); err != nil {
		return nil, err
	}
	coverage.TranscriptCoverageRate = postgresRate(coverage.TranscriptCount, coverage.TotalCalls)
	return &coverage, nil
}

func (s *Store) CallFactsCoverageWithSource(ctx context.Context, sourceArg string) (*sqlite.CallFactsCoverage, []sqlite.CallFactsSummaryRow, *sqlite.ProfileQueryInfo, error) {
	info, err := postgresBuiltinLifecycleInfo(sourceArg)
	if err != nil {
		return nil, nil, nil, err
	}
	coverage, err := s.CallFactsCoverage(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	rows, err := s.SummarizeCallFacts(ctx, sqlite.CallFactsSummaryParams{GroupBy: "lifecycle", LifecycleSource: "builtin", Limit: 50})
	return coverage, rows, info, err
}

func postgresCallFactsSQL() string {
	return `
WITH raw_objects AS (
	SELECT c.call_id,
	       object_item.object_json,
	       source_item.source_name || ':' || object_item.ordinal::text AS object_key,
	       COALESCE(NULLIF(object_item.object_json->>'objectType', ''), NULLIF(object_item.object_json->>'type', ''), '') AS object_type,
	       COALESCE(NULLIF(object_item.object_json->>'id', ''), NULLIF(object_item.object_json->>'objectId', ''), '') AS object_id
	  FROM calls c
	  CROSS JOIN LATERAL (VALUES
		('context', CASE
			WHEN jsonb_typeof(c.raw_json->'context') = 'array' THEN c.raw_json->'context'
			WHEN jsonb_typeof(c.raw_json->'context') = 'object' AND jsonb_typeof(c.raw_json#>'{context,fields}') IN ('array', 'object') THEN jsonb_build_array(c.raw_json->'context')
			WHEN jsonb_typeof(c.raw_json->'context') = 'object' AND jsonb_typeof(c.raw_json#>'{context,properties}') IN ('array', 'object') THEN jsonb_build_array(c.raw_json->'context')
			ELSE NULL
		END),
		('context.crmObjects', CASE WHEN jsonb_typeof(c.raw_json#>'{context,crmObjects}') = 'array' THEN c.raw_json#>'{context,crmObjects}' ELSE NULL END),
		('context.objects', CASE WHEN jsonb_typeof(c.raw_json#>'{context,objects}') = 'array' THEN c.raw_json#>'{context,objects}' ELSE NULL END),
		('crmContext', CASE
			WHEN jsonb_typeof(c.raw_json->'crmContext') = 'array' THEN c.raw_json->'crmContext'
			WHEN jsonb_typeof(c.raw_json->'crmContext') = 'object' AND jsonb_typeof(c.raw_json#>'{crmContext,fields}') IN ('array', 'object') THEN jsonb_build_array(c.raw_json->'crmContext')
			WHEN jsonb_typeof(c.raw_json->'crmContext') = 'object' AND jsonb_typeof(c.raw_json#>'{crmContext,properties}') IN ('array', 'object') THEN jsonb_build_array(c.raw_json->'crmContext')
			ELSE NULL
		END),
		('crmContext.crmObjects', CASE WHEN jsonb_typeof(c.raw_json#>'{crmContext,crmObjects}') = 'array' THEN c.raw_json#>'{crmContext,crmObjects}' ELSE NULL END),
		('crmContext.objects', CASE WHEN jsonb_typeof(c.raw_json#>'{crmContext,objects}') = 'array' THEN c.raw_json#>'{crmContext,objects}' ELSE NULL END),
		('crm', CASE
			WHEN jsonb_typeof(c.raw_json->'crm') = 'array' THEN c.raw_json->'crm'
			WHEN jsonb_typeof(c.raw_json->'crm') = 'object' AND jsonb_typeof(c.raw_json#>'{crm,fields}') IN ('array', 'object') THEN jsonb_build_array(c.raw_json->'crm')
			WHEN jsonb_typeof(c.raw_json->'crm') = 'object' AND jsonb_typeof(c.raw_json#>'{crm,properties}') IN ('array', 'object') THEN jsonb_build_array(c.raw_json->'crm')
			ELSE NULL
		END),
		('crm.crmObjects', CASE WHEN jsonb_typeof(c.raw_json#>'{crm,crmObjects}') = 'array' THEN c.raw_json#>'{crm,crmObjects}' ELSE NULL END),
		('crm.objects', CASE WHEN jsonb_typeof(c.raw_json#>'{crm,objects}') = 'array' THEN c.raw_json#>'{crm,objects}' ELSE NULL END),
		('extendedContext', CASE
			WHEN jsonb_typeof(c.raw_json->'extendedContext') = 'array' THEN c.raw_json->'extendedContext'
			WHEN jsonb_typeof(c.raw_json->'extendedContext') = 'object' AND jsonb_typeof(c.raw_json#>'{extendedContext,fields}') IN ('array', 'object') THEN jsonb_build_array(c.raw_json->'extendedContext')
			WHEN jsonb_typeof(c.raw_json->'extendedContext') = 'object' AND jsonb_typeof(c.raw_json#>'{extendedContext,properties}') IN ('array', 'object') THEN jsonb_build_array(c.raw_json->'extendedContext')
			ELSE NULL
		END),
		('extendedContext.crmObjects', CASE WHEN jsonb_typeof(c.raw_json#>'{extendedContext,crmObjects}') = 'array' THEN c.raw_json#>'{extendedContext,crmObjects}' ELSE NULL END),
		('extendedContext.objects', CASE WHEN jsonb_typeof(c.raw_json#>'{extendedContext,objects}') = 'array' THEN c.raw_json#>'{extendedContext,objects}' ELSE NULL END),
		('crmObjects', CASE WHEN jsonb_typeof(c.raw_json->'crmObjects') = 'array' THEN c.raw_json->'crmObjects' ELSE NULL END),
		('objects', CASE WHEN jsonb_typeof(c.raw_json->'objects') = 'array' THEN c.raw_json->'objects' ELSE NULL END)
	  ) AS source_item(source_name, objects_json)
	  CROSS JOIN LATERAL jsonb_array_elements(
		CASE WHEN source_item.objects_json IS NULL THEN '[]'::jsonb ELSE source_item.objects_json END
	  ) WITH ORDINALITY AS object_item(object_json, ordinal)
),
object_fields AS (
	SELECT ro.call_id,
	       ro.object_key,
	       ro.object_type,
	       ro.object_id,
	       COALESCE(NULLIF(field_item.field_json->>'name', ''), NULLIF(field_item.field_json->>'fieldName', ''), '') AS field_name,
	       COALESCE(NULLIF(field_item.field_json->>'value', ''), NULLIF(field_item.field_json->>'fieldValue', ''), '') AS field_value
	  FROM raw_objects ro
	  CROSS JOIN LATERAL jsonb_array_elements(
		CASE WHEN jsonb_typeof(ro.object_json->'fields') = 'array' THEN ro.object_json->'fields' ELSE '[]'::jsonb END
	  ) AS field_item(field_json)
	UNION ALL
	SELECT ro.call_id,
	       ro.object_key,
	       ro.object_type,
	       ro.object_id,
	       field_item.field_name,
	       CASE
		       WHEN jsonb_typeof(field_item.field_value_json) = 'string' THEN field_item.field_value_json#>>'{}'
		       ELSE field_item.field_value_json::text
	       END AS field_value
	  FROM raw_objects ro
	  CROSS JOIN LATERAL jsonb_each(
		CASE WHEN jsonb_typeof(ro.object_json->'fields') = 'object' THEN ro.object_json->'fields' ELSE '{}'::jsonb END
	  ) AS field_item(field_name, field_value_json)
	UNION ALL
	SELECT ro.call_id,
	       ro.object_key,
	       ro.object_type,
	       ro.object_id,
	       COALESCE(NULLIF(field_item.field_json->>'name', ''), NULLIF(field_item.field_json->>'fieldName', ''), '') AS field_name,
	       COALESCE(NULLIF(field_item.field_json->>'value', ''), NULLIF(field_item.field_json->>'fieldValue', ''), '') AS field_value
	  FROM raw_objects ro
	  CROSS JOIN LATERAL jsonb_array_elements(
		CASE WHEN jsonb_typeof(ro.object_json->'properties') = 'array' THEN ro.object_json->'properties' ELSE '[]'::jsonb END
	  ) AS field_item(field_json)
	UNION ALL
	SELECT ro.call_id,
	       ro.object_key,
	       ro.object_type,
	       ro.object_id,
	       field_item.field_name,
	       CASE
		       WHEN jsonb_typeof(field_item.field_value_json) = 'string' THEN field_item.field_value_json#>>'{}'
		       ELSE field_item.field_value_json::text
	       END AS field_value
	  FROM raw_objects ro
	  CROSS JOIN LATERAL jsonb_each(
		CASE WHEN jsonb_typeof(ro.object_json->'properties') = 'object' THEN ro.object_json->'properties' ELSE '{}'::jsonb END
	  ) AS field_item(field_name, field_value_json)
),
crm AS (
	SELECT c.call_id,
	       COALESCE(MAX(CASE WHEN LOWER(ro.object_type) = 'account' THEN ro.object_id END), '') AS account_id,
	       COALESCE(MAX(CASE WHEN LOWER(ro.object_type) = 'opportunity' THEN ro.object_id END), '') AS opportunity_id,
	       COALESCE(MAX(CASE WHEN LOWER(of.object_type) = 'account' AND of.field_name = 'Account_Type__c' THEN TRIM(of.field_value) END), '') AS account_type,
	       COALESCE(MAX(CASE WHEN LOWER(of.object_type) = 'account' AND of.field_name = 'Industry' THEN TRIM(of.field_value) END), '') AS account_industry,
	       COALESCE(MAX(CASE WHEN LOWER(of.object_type) = 'account' AND of.field_name = 'Revenue_Range_f__c' THEN TRIM(of.field_value) END), '') AS account_revenue_range,
	       COALESCE(MAX(CASE WHEN LOWER(of.object_type) = 'opportunity' AND of.field_name = 'StageName' THEN TRIM(of.field_value) END), '') AS opportunity_stage,
	       COALESCE(MAX(CASE WHEN LOWER(of.object_type) = 'opportunity' AND of.field_name = 'Type' THEN TRIM(of.field_value) END), '') AS opportunity_type,
	       COALESCE(MAX(CASE WHEN LOWER(of.object_type) = 'opportunity' AND of.field_name = 'Primary_Lead_Source__c' THEN TRIM(of.field_value) END), '') AS opportunity_primary_lead_source,
	       COALESCE(MAX(CASE WHEN LOWER(of.object_type) = 'opportunity' AND of.field_name = 'Forecast_Category_VP__c' THEN TRIM(of.field_value) END), '') AS opportunity_forecast_category,
	       COUNT(DISTINCT CASE WHEN LOWER(ro.object_type) = 'opportunity' THEN ro.object_key END) AS opportunity_count,
	       COUNT(DISTINCT CASE WHEN LOWER(ro.object_type) = 'account' THEN ro.object_key END) AS account_count,
	       COALESCE(MAX(CASE WHEN LOWER(of.object_type) = 'opportunity' AND of.field_name = 'Type' AND LOWER(TRIM(of.field_value)) = 'partnership' THEN 1 ELSE 0 END), 0) AS has_partner_opportunity,
	       COALESCE(MAX(CASE WHEN LOWER(of.object_type) = 'account' AND of.field_name = 'Account_Type__c' AND (LOWER(TRIM(of.field_value)) LIKE 'partner%' OR LOWER(TRIM(of.field_value)) LIKE 'technology referral partner%') THEN 1 ELSE 0 END), 0) AS has_partner_account,
	       COALESCE(MAX(CASE WHEN LOWER(of.object_type) = 'opportunity' AND of.field_name = 'Type' AND LOWER(TRIM(of.field_value)) = 'renewal' THEN 1 ELSE 0 END), 0) AS has_renewal_opportunity,
	       COALESCE(MAX(CASE WHEN LOWER(of.object_type) = 'opportunity' AND of.field_name = 'Type' AND LOWER(TRIM(of.field_value)) IN ('upsell', 'existing business', 'year 2 increase', 'year 3 increase') THEN 1 ELSE 0 END), 0) AS has_upsell_opportunity,
	       COALESCE(MAX(CASE WHEN LOWER(of.object_type) = 'opportunity' AND of.field_name IN ('Expansion_Bookings__c', 'One_Year_Upsell__c') AND TRIM(of.field_value) NOT IN ('', '0', '0.0', '0.00') THEN 1 ELSE 0 END), 0) AS has_expansion_signal,
	       COALESCE(MAX(CASE WHEN LOWER(of.object_type) = 'opportunity' AND of.field_name = 'StageName' AND LOWER(TRIM(of.field_value)) IN ('closed won', 'closed lost') THEN 1 ELSE 0 END), 0) AS has_closed_stage,
	       COALESCE(MAX(CASE WHEN LOWER(of.object_type) = 'opportunity' AND of.field_name = 'StageName' AND LOWER(TRIM(of.field_value)) IN ('demo & business case', 'business case', 'sow & proposal', 'contract review', 'contract signing', 'crucible/last mile') THEN 1 ELSE 0 END), 0) AS has_late_stage,
	       COALESCE(MAX(CASE WHEN LOWER(of.object_type) = 'account' AND of.field_name = 'Account_Type__c' AND LOWER(TRIM(of.field_value)) LIKE 'customer%' THEN 1 ELSE 0 END), 0) AS has_customer_account
	  FROM calls c
	  LEFT JOIN raw_objects ro ON ro.call_id = c.call_id
	  LEFT JOIN object_fields of ON of.call_id = ro.call_id AND of.object_key = ro.object_key
	 GROUP BY c.call_id
),
signals AS (
	SELECT c.call_id,
	       c.title,
	       c.started_at,
	       left(c.started_at, 10) AS call_date,
	       left(c.started_at, 7) AS call_month,
	       c.duration_seconds,
	       COALESCE(NULLIF(c.raw_json#>>'{metaData,system}', ''), NULLIF(c.raw_json->>'system', ''), '') AS system,
	       COALESCE(NULLIF(c.raw_json#>>'{metaData,direction}', ''), NULLIF(c.raw_json->>'direction', ''), '') AS direction,
	       COALESCE(NULLIF(TRIM(c.raw_json#>>'{metaData,scope}'), ''), 'Unknown') AS scope,
	       c.raw_json,
	       CASE WHEN t.call_id IS NULL THEN false ELSE true END AS transcript_present,
	       CASE WHEN t.call_id IS NULL THEN 'missing' ELSE 'present' END AS transcript_status,
	       COALESCE(crm.account_id, '') AS account_id,
	       COALESCE(crm.account_type, '') AS account_type,
	       COALESCE(crm.account_industry, '') AS account_industry,
	       COALESCE(crm.account_revenue_range, '') AS account_revenue_range,
	       COALESCE(crm.opportunity_id, '') AS opportunity_id,
	       COALESCE(crm.opportunity_stage, '') AS opportunity_stage,
	       COALESCE(crm.opportunity_type, '') AS opportunity_type,
	       COALESCE(crm.opportunity_primary_lead_source, '') AS opportunity_primary_lead_source,
	       COALESCE(crm.opportunity_forecast_category, '') AS opportunity_forecast_category,
	       COALESCE(crm.opportunity_count, 0) AS opportunity_count,
	       COALESCE(crm.account_count, 0) AS account_count,
	       COALESCE(crm.has_partner_opportunity, 0) AS has_partner_opportunity,
	       COALESCE(crm.has_partner_account, 0) AS has_partner_account,
	       COALESCE(crm.has_renewal_opportunity, 0) AS has_renewal_opportunity,
	       COALESCE(crm.has_upsell_opportunity, 0) AS has_upsell_opportunity,
	       COALESCE(crm.has_expansion_signal, 0) AS has_expansion_signal,
	       COALESCE(crm.has_closed_stage, 0) AS has_closed_stage,
	       COALESCE(crm.has_late_stage, 0) AS has_late_stage,
	       COALESCE(crm.has_customer_account, 0) AS has_customer_account
	  FROM calls c
	  LEFT JOIN transcripts t ON t.call_id = c.call_id
	  LEFT JOIN crm ON crm.call_id = c.call_id
)
SELECT c.call_id,
       c.title,
       c.started_at,
       c.call_date,
       c.call_month,
       c.duration_seconds,
       c.system,
       c.direction,
       c.scope,
       CASE
	       WHEN c.has_partner_opportunity = 1 OR c.has_partner_account = 1 THEN 'partner'
	       WHEN c.has_renewal_opportunity = 1 THEN 'renewal'
	       WHEN c.has_upsell_opportunity = 1 OR c.has_expansion_signal = 1 THEN 'upsell_expansion'
	       WHEN c.has_closed_stage = 1 THEN 'closed_won_lost_review'
	       WHEN c.has_late_stage = 1 THEN 'late_stage_sales'
	       WHEN c.opportunity_count > 0 THEN 'active_sales_pipeline'
	       WHEN c.has_customer_account = 1 THEN 'customer_success_account'
	       WHEN LOWER(c.system) = 'upload api' AND LOWER(c.direction) = 'outbound' THEN 'outbound_prospecting'
	       ELSE 'unknown'
       END AS lifecycle_bucket,
       CASE
	       WHEN c.has_partner_opportunity = 1 OR c.has_partner_account = 1 OR c.has_renewal_opportunity = 1 OR c.has_upsell_opportunity = 1 OR c.has_expansion_signal = 1 OR c.has_closed_stage = 1 OR c.has_late_stage = 1 OR c.has_customer_account = 1 THEN 'high'
	       WHEN c.opportunity_count > 0 OR (LOWER(c.system) = 'upload api' AND LOWER(c.direction) = 'outbound') THEN 'medium'
	       ELSE 'low'
       END AS lifecycle_confidence,
       CASE
	       WHEN c.has_partner_opportunity = 1 THEN 'Opportunity.Type=Partnership'
	       WHEN c.has_partner_account = 1 THEN 'Account.Account_Type__c indicates partner'
	       WHEN c.has_renewal_opportunity = 1 THEN 'Opportunity.Type=Renewal'
	       WHEN c.has_upsell_opportunity = 1 THEN 'Opportunity.Type indicates post-sales expansion'
	       WHEN c.has_expansion_signal = 1 THEN 'Opportunity expansion booking fields are populated'
	       WHEN c.has_closed_stage = 1 THEN 'Opportunity.StageName is closed'
	       WHEN c.has_late_stage = 1 THEN 'Opportunity.StageName is late-stage'
	       WHEN c.opportunity_count > 0 THEN 'Opportunity context is attached'
	       WHEN c.has_customer_account = 1 THEN 'Account.Account_Type__c indicates customer'
	       WHEN LOWER(c.system) = 'upload api' AND LOWER(c.direction) = 'outbound' THEN 'Outbound Upload API call without Opportunity context'
	       ELSE 'No strong lifecycle CRM signal'
       END AS lifecycle_reason,
       CASE
	       WHEN c.has_partner_opportunity = 1 THEN 'Opportunity.Type'
	       WHEN c.has_partner_account = 1 THEN 'Account.Account_Type__c'
	       WHEN c.has_renewal_opportunity = 1 THEN 'Opportunity.Type'
	       WHEN c.has_upsell_opportunity = 1 THEN 'Opportunity.Type'
	       WHEN c.has_expansion_signal = 1 THEN 'Opportunity.Expansion_Bookings__c|Opportunity.One_Year_Upsell__c'
	       WHEN c.has_closed_stage = 1 THEN 'Opportunity.StageName'
	       WHEN c.has_late_stage = 1 THEN 'Opportunity.StageName'
	       WHEN c.opportunity_count > 0 THEN 'Opportunity context'
	       WHEN c.has_customer_account = 1 THEN 'Account.Account_Type__c'
	       WHEN LOWER(c.system) = 'upload api' AND LOWER(c.direction) = 'outbound' THEN 'call.system|call.direction'
	       ELSE ''
       END AS lifecycle_evidence_fields,
       c.opportunity_count,
       c.account_count,
       c.transcript_present,
       c.transcript_status,
       c.account_id,
       c.account_type,
       c.account_industry,
       c.account_revenue_range,
       c.opportunity_id,
       c.opportunity_stage,
       c.opportunity_type,
       c.opportunity_primary_lead_source,
       c.opportunity_forecast_category,
       CASE WHEN c.duration_seconds < 60 THEN 'under_1m' WHEN c.duration_seconds < 300 THEN '1_5m' WHEN c.duration_seconds < 900 THEN '5_15m' WHEN c.duration_seconds < 1800 THEN '15_30m' WHEN c.duration_seconds < 2700 THEN '30_45m' ELSE '45m_plus' END AS duration_bucket
  FROM signals c`
}

func postgresBuiltinLifecycleInfo(requested string) (*sqlite.ProfileQueryInfo, error) {
	switch strings.ToLower(strings.TrimSpace(requested)) {
	case "", "auto", "builtin":
		return &sqlite.ProfileQueryInfo{LifecycleSource: "builtin"}, nil
	case "profile":
		return nil, errors.New("postgres profile lifecycle source is not implemented in this slice")
	default:
		return nil, fmt.Errorf("lifecycle_source must be one of: auto, builtin, profile")
	}
}

func postgresCallFactGroupColumn(groupBy string) (string, string, error) {
	switch strings.ToLower(strings.TrimSpace(groupBy)) {
	case "", "lifecycle", "lifecycle_bucket":
		return "lifecycle", "lifecycle_bucket", nil
	case "opportunity_stage", "stage":
		return "opportunity_stage", "opportunity_stage", nil
	case "opportunity_type":
		return "opportunity_type", "opportunity_type", nil
	case "account_type":
		return "account_type", "account_type", nil
	case "account_industry", "industry":
		return "account_industry", "account_industry", nil
	case "revenue_range", "account_revenue_range":
		return "revenue_range", "account_revenue_range", nil
	case "scope":
		return "scope", "scope", nil
	case "system":
		return "system", "system", nil
	case "direction":
		return "direction", "direction", nil
	case "transcript_status":
		return "transcript_status", "transcript_status", nil
	case "duration_bucket":
		return "duration_bucket", "duration_bucket", nil
	case "month", "call_month":
		return "month", "call_month", nil
	case "lead_source", "primary_lead_source":
		return "lead_source", "opportunity_primary_lead_source", nil
	case "forecast_category":
		return "forecast_category", "opportunity_forecast_category", nil
	default:
		return "", "", fmt.Errorf("unsupported group_by %q in postgres business-pilot slice", groupBy)
	}
}

func postgresCallFactsWhere(params sqlite.CallFactsSummaryParams) ([]string, []any, error) {
	where := []string{}
	args := []any{}
	if value := strings.TrimSpace(params.LifecycleBucket); value != "" {
		if !postgresKnownLifecycleBucket(value) {
			return nil, nil, fmt.Errorf("unknown lifecycle bucket %q", value)
		}
		where = append(where, "lifecycle_bucket = "+postgresAddArg(&args, value))
	}
	if value := strings.TrimSpace(params.Scope); value != "" {
		scope, ok := normalizePostgresScope(value)
		if !ok {
			return nil, nil, errors.New("scope must be one of: External, Internal, Unknown")
		}
		where = append(where, "scope = "+postgresAddArg(&args, scope))
	}
	if value := strings.TrimSpace(params.System); value != "" {
		where = append(where, "system = "+postgresAddArg(&args, value))
	}
	if value := strings.TrimSpace(params.Direction); value != "" {
		where = append(where, "direction = "+postgresAddArg(&args, value))
	}
	if value := strings.TrimSpace(params.TranscriptStatus); value != "" && value != "any" {
		status, ok := normalizePostgresTranscriptStatus(value)
		if !ok {
			return nil, nil, errors.New("transcript_status must be one of: present, missing")
		}
		where = append(where, "transcript_status = "+postgresAddArg(&args, status))
	}
	return where, args, nil
}

func postgresAddArg(args *[]any, value any) string {
	*args = append(*args, value)
	return fmt.Sprintf("$%d", len(*args))
}

func normalizePostgresDateFilter(value string, fieldName string) (string, error) {
	date := strings.TrimSpace(value)
	if date == "" {
		return "", nil
	}
	parsed, err := time.Parse("2006-01-02", date)
	if err != nil {
		return "", fmt.Errorf("%s must be YYYY-MM-DD", fieldName)
	}
	return parsed.Format("2006-01-02"), nil
}

func normalizePostgresScope(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "external":
		return "External", true
	case "internal":
		return "Internal", true
	case "unknown":
		return "Unknown", true
	default:
		return "", false
	}
}

func normalizePostgresTranscriptStatus(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "present", "has_transcript", "with_transcript":
		return "present", true
	case "missing", "missing_transcript", "without_transcript":
		return "missing", true
	default:
		return "", false
	}
}

func postgresLifecycleBucketDefinitions() []sqlite.LifecycleBucketDefinition {
	return []sqlite.LifecycleBucketDefinition{
		{Bucket: "outbound_prospecting", Label: "Outbound Prospecting", Description: "Outbound calls without Opportunity context, typically top-of-funnel prospecting.", PrimarySignals: []string{"call.system=Upload API", "call.direction=Outbound", "no Opportunity context"}},
		{Bucket: "active_sales_pipeline", Label: "Active Sales Pipeline", Description: "Open Opportunity-linked sales conversations before late-stage sales.", PrimarySignals: []string{"Opportunity context", "Opportunity.StageName not closed or late-stage"}},
		{Bucket: "late_stage_sales", Label: "Late-Stage Sales", Description: "Opportunity calls in demo/business-case, proposal, contract review, or signing stages.", PrimarySignals: []string{"Opportunity.StageName"}},
		{Bucket: "closed_won_lost_review", Label: "Closed Won/Lost Review", Description: "Calls tied to closed Opportunities that are useful for win/loss review.", PrimarySignals: []string{"Opportunity.StageName=Closed Won", "Opportunity.StageName=Closed Lost"}},
		{Bucket: "renewal", Label: "Renewal", Description: "Post-sales renewal conversations.", PrimarySignals: []string{"Opportunity.Type=Renewal"}},
		{Bucket: "upsell_expansion", Label: "Upsell / Expansion", Description: "Post-sales expansion conversations such as upsell, existing business, or contract increase calls.", PrimarySignals: []string{"Opportunity.Type", "Opportunity.Expansion_Bookings__c", "Opportunity.One_Year_Upsell__c"}},
		{Bucket: "customer_success_account", Label: "Customer Success / Account", Description: "Account-context calls for active or inactive customers without stronger Opportunity lifecycle signals.", PrimarySignals: []string{"Account.Account_Type__c starts with Customer"}},
		{Bucket: "partner", Label: "Partner", Description: "Partner or referral partner conversations.", PrimarySignals: []string{"Opportunity.Type=Partnership", "Account.Account_Type__c partner values"}},
		{Bucket: "unknown", Label: "Unknown", Description: "Calls without enough CRM or metadata signal for a deterministic lifecycle bucket.", PrimarySignals: []string{}},
	}
}

func postgresKnownLifecycleBucket(bucket string) bool {
	bucket = strings.TrimSpace(bucket)
	for _, definition := range postgresLifecycleBucketDefinitions() {
		if definition.Bucket == bucket {
			return true
		}
	}
	return false
}

func postgresLifecycleOrderSQL(column string) string {
	return `CASE ` + column + `
	WHEN 'outbound_prospecting' THEN 1
	WHEN 'active_sales_pipeline' THEN 2
	WHEN 'late_stage_sales' THEN 3
	WHEN 'closed_won_lost_review' THEN 4
	WHEN 'renewal' THEN 5
	WHEN 'upsell_expansion' THEN 6
	WHEN 'customer_success_account' THEN 7
	WHEN 'partner' THEN 8
	ELSE 99
END`
}

func postgresRate(part int64, total int64) float64 {
	if total <= 0 {
		return 0
	}
	return float64(part) / float64(total)
}

func splitPostgresEvidenceFields(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == '|' || r == ','
	})
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
