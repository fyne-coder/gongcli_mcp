package sqlite

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

func (s *Store) ListLifecycleBucketDefinitions(ctx context.Context) ([]LifecycleBucketDefinition, error) {
	return lifecycleBucketDefinitions(), nil
}
func (s *Store) SummarizeCallsByLifecycle(ctx context.Context, params LifecycleSummaryParams) ([]LifecycleBucketSummary, error) {
	bucket := strings.TrimSpace(params.Bucket)
	if bucket != "" && !isKnownLifecycleBucket(bucket) {
		return nil, fmt.Errorf("unknown lifecycle bucket %q", bucket)
	}

	query := `
SELECT bucket,
       COUNT(*) AS call_count,
       SUM(CASE WHEN transcript_present = 1 THEN 1 ELSE 0 END) AS transcript_count,
       SUM(CASE WHEN transcript_present = 0 THEN 1 ELSE 0 END) AS missing_transcript_count,
       SUM(CASE WHEN opportunity_count > 0 THEN 1 ELSE 0 END) AS opportunity_call_count,
       SUM(CASE WHEN account_count > 0 THEN 1 ELSE 0 END) AS account_call_count,
       SUM(duration_seconds) AS total_duration_seconds,
       MAX(started_at) AS latest_call_at,
       COALESCE((SELECT cl2.call_id
                   FROM call_lifecycle cl2
                  WHERE cl2.bucket = cl.bucket
                  ORDER BY cl2.started_at DESC, cl2.call_id
                  LIMIT 1), '') AS latest_call_id,
       SUM(CASE WHEN confidence = 'high' THEN 1 ELSE 0 END) AS high_confidence_calls,
       SUM(CASE WHEN confidence = 'medium' THEN 1 ELSE 0 END) AS medium_confidence_calls,
       SUM(CASE WHEN confidence = 'low' THEN 1 ELSE 0 END) AS low_confidence_calls
  FROM call_lifecycle cl`
	args := []any{}
	if bucket != "" {
		query += ` WHERE bucket = ?`
		args = append(args, bucket)
	}
	query += `
 GROUP BY bucket
 ORDER BY ` + lifecycleBucketOrderSQL("bucket") + `, call_count DESC`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]LifecycleBucketSummary, 0)
	for rows.Next() {
		var row LifecycleBucketSummary
		if err := rows.Scan(
			&row.Bucket,
			&row.CallCount,
			&row.TranscriptCount,
			&row.MissingTranscriptCount,
			&row.OpportunityCallCount,
			&row.AccountCallCount,
			&row.TotalDurationSeconds,
			&row.LatestCallAt,
			&row.LatestCallID,
			&row.HighConfidenceCalls,
			&row.MediumConfidenceCalls,
			&row.LowConfidenceCalls,
		); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
func (s *Store) SearchCallsByLifecycle(ctx context.Context, params LifecycleCallSearchParams) ([]LifecycleCallSearchResult, error) {
	bucket := strings.TrimSpace(params.Bucket)
	if bucket != "" && !isKnownLifecycleBucket(bucket) {
		return nil, fmt.Errorf("unknown lifecycle bucket %q", bucket)
	}
	limit := boundedLimit(params.Limit, defaultLifecycleLimit, maxLifecycleLimit)

	query := `
SELECT call_id,
       title,
       started_at,
       duration_seconds,
       bucket,
       confidence,
       reason,
       evidence_fields,
       opportunity_count,
       account_count,
       transcript_present
  FROM call_lifecycle`
	var where []string
	args := []any{}
	if bucket != "" {
		where = append(where, `bucket = ?`)
		args = append(args, bucket)
	}
	if params.MissingTranscriptsOnly {
		where = append(where, `transcript_present = 0`)
	}
	if len(where) > 0 {
		query += ` WHERE ` + strings.Join(where, ` AND `)
	}
	query += `
 ORDER BY started_at DESC, call_id
 LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]LifecycleCallSearchResult, 0)
	for rows.Next() {
		var row LifecycleCallSearchResult
		var evidence string
		var transcriptPresent int
		if err := rows.Scan(
			&row.CallID,
			&row.Title,
			&row.StartedAt,
			&row.DurationSeconds,
			&row.Bucket,
			&row.Confidence,
			&row.Reason,
			&evidence,
			&row.OpportunityCount,
			&row.AccountCount,
			&transcriptPresent,
		); err != nil {
			return nil, err
		}
		row.EvidenceFields = splitEvidenceFields(evidence)
		row.TranscriptPresent = transcriptPresent == 1
		out = append(out, row)
	}
	return out, rows.Err()
}
func (s *Store) PrioritizeTranscriptsByLifecycle(ctx context.Context, params LifecycleTranscriptPriorityParams) ([]LifecycleTranscriptPriority, error) {
	bucket := strings.TrimSpace(params.Bucket)
	if bucket != "" && !isKnownLifecycleBucket(bucket) {
		return nil, fmt.Errorf("unknown lifecycle bucket %q", bucket)
	}
	limit := boundedLimit(params.Limit, defaultLifecycleLimit, maxLifecycleLimit)

	query := `
WITH candidates AS (
	SELECT l.call_id,
	       l.title,
	       l.started_at,
	       l.duration_seconds,
	       l.system,
	       l.direction,
	       COALESCE(
	         NULLIF(TRIM(json_extract(c.raw_json, '$.metaData.scope')), ''),
	         NULLIF(TRIM(json_extract(c.raw_json, '$.scope')), ''),
	         'Unknown'
	       ) AS scope,
	       l.bucket,
	       l.confidence,
	       l.reason,
	       l.evidence_fields,
	       l.opportunity_count
	  FROM call_lifecycle l
	  JOIN calls c
	    ON c.call_id = l.call_id
	 WHERE l.transcript_present = 0`
	args := []any{}
	if bucket != "" {
		query += ` AND l.bucket = ?`
		args = append(args, bucket)
	}
	var fromDate, toDate string
	if value := strings.TrimSpace(params.FromDate); value != "" {
		date, err := normalizeDateFilter(value, "from_date")
		if err != nil {
			return nil, err
		}
		fromDate = date
		query += ` AND substr(l.started_at, 1, 10) >= ?`
		args = append(args, date)
	}
	if value := strings.TrimSpace(params.ToDate); value != "" {
		date, err := normalizeDateFilter(value, "to_date")
		if err != nil {
			return nil, err
		}
		toDate = date
		query += ` AND substr(l.started_at, 1, 10) <= ?`
		args = append(args, date)
	}
	if fromDate != "" && toDate != "" && fromDate > toDate {
		return nil, errors.New("from_date must be on or before to_date")
	}
	if value := strings.TrimSpace(params.Scope); value != "" {
		scope, ok := normalizedScope(value)
		if !ok {
			return nil, errors.New("scope must be one of: External, Internal, Unknown")
		}
		query += ` AND COALESCE(NULLIF(TRIM(json_extract(c.raw_json, '$.metaData.scope')), ''), NULLIF(TRIM(json_extract(c.raw_json, '$.scope')), ''), 'Unknown') = ?`
		args = append(args, scope)
	}
	if value := strings.TrimSpace(params.System); value != "" {
		query += ` AND l.system = ?`
		args = append(args, value)
	}
	if value := strings.TrimSpace(params.Direction); value != "" {
		query += ` AND l.direction = ?`
		args = append(args, value)
	}
	query += `
)
SELECT call_id,
       title,
       started_at,
       duration_seconds,
       system,
       direction,
       scope,
       bucket,
       confidence,
       reason,
       evidence_fields,
       (
         CASE bucket
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
         + CASE confidence WHEN 'high' THEN 20 WHEN 'medium' THEN 10 ELSE 0 END
         + CASE WHEN scope = 'External' THEN 25 ELSE 0 END
         + CASE WHEN direction = 'Conference' THEN 20 ELSE 0 END
         + CASE WHEN duration_seconds >= 1800 THEN 20 WHEN duration_seconds >= 600 THEN 10 ELSE 0 END
         + CASE WHEN opportunity_count > 0 THEN 10 ELSE 0 END
         - CASE WHEN duration_seconds > 0 AND duration_seconds < 300 AND direction <> 'Conference' THEN 20 ELSE 0 END
       ) AS priority_score
  FROM candidates
 ORDER BY priority_score DESC, started_at DESC, call_id
 LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]LifecycleTranscriptPriority, 0)
	for rows.Next() {
		var row LifecycleTranscriptPriority
		var evidence string
		if err := rows.Scan(
			&row.CallID,
			&row.Title,
			&row.StartedAt,
			&row.DurationSeconds,
			&row.System,
			&row.Direction,
			&row.Scope,
			&row.Bucket,
			&row.Confidence,
			&row.Reason,
			&evidence,
			&row.PriorityScore,
		); err != nil {
			return nil, err
		}
		row.EvidenceFields = splitEvidenceFields(evidence)
		out = append(out, row)
	}
	return out, rows.Err()
}
func (s *Store) CompareLifecycleCRMFields(ctx context.Context, params LifecycleCRMFieldComparisonParams) (*LifecycleCRMFieldComparison, error) {
	bucketA := strings.TrimSpace(params.BucketA)
	bucketB := strings.TrimSpace(params.BucketB)
	if bucketA == "" || bucketB == "" {
		return nil, errors.New("bucket_a and bucket_b are required")
	}
	if !isKnownLifecycleBucket(bucketA) {
		return nil, fmt.Errorf("unknown lifecycle bucket %q", bucketA)
	}
	if !isKnownLifecycleBucket(bucketB) {
		return nil, fmt.Errorf("unknown lifecycle bucket %q", bucketB)
	}
	if bucketA == bucketB {
		return nil, errors.New("bucket_a and bucket_b must be different")
	}
	objectType := strings.TrimSpace(params.ObjectType)
	if objectType == "" {
		return nil, errors.New("object_type is required")
	}
	limit := boundedLimit(params.Limit, defaultLifecycleCRMFieldLimit, maxLifecycleCRMFieldLimit)

	rows, err := s.db.QueryContext(ctx, `
WITH selected_calls AS (
	SELECT call_id, bucket
	  FROM call_lifecycle
	 WHERE bucket IN (?, ?)
),
bucket_totals AS (
	SELECT bucket, COUNT(DISTINCT call_id) AS call_count
	  FROM selected_calls
	 GROUP BY bucket
),
field_presence AS (
	SELECT DISTINCT sc.bucket,
	       f.call_id,
	       o.object_type,
	       f.field_name,
	       f.field_label
	  FROM selected_calls sc
	  JOIN call_context_fields f
	    ON f.call_id = sc.call_id
	  JOIN call_context_objects o
	    ON o.call_id = f.call_id
	   AND o.object_key = f.object_key
	 WHERE TRIM(f.field_value_text) <> ''
	   AND o.object_type = ?
)
SELECT fp.object_type,
       fp.field_name,
       MAX(fp.field_label) AS field_label,
       COALESCE(MAX(CASE WHEN bt.bucket = ? THEN bt.call_count END), 0) AS bucket_a_call_count,
       COALESCE(MAX(CASE WHEN bt.bucket = ? THEN bt.call_count END), 0) AS bucket_b_call_count,
       COUNT(DISTINCT CASE WHEN fp.bucket = ? THEN fp.call_id END) AS bucket_a_populated,
       COUNT(DISTINCT CASE WHEN fp.bucket = ? THEN fp.call_id END) AS bucket_b_populated
  FROM field_presence fp
 CROSS JOIN bucket_totals bt
 GROUP BY fp.object_type, fp.field_name
 ORDER BY fp.object_type, fp.field_name`,
		bucketA,
		bucketB,
		objectType,
		bucketA,
		bucketB,
		bucketA,
		bucketB,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	report := &LifecycleCRMFieldComparison{
		BucketA:    bucketA,
		BucketB:    bucketB,
		ObjectType: objectType,
	}
	for rows.Next() {
		var row LifecycleCRMFieldComparisonRow
		if err := rows.Scan(
			&row.ObjectType,
			&row.FieldName,
			&row.FieldLabel,
			&row.BucketACallCount,
			&row.BucketBCallCount,
			&row.BucketAPopulated,
			&row.BucketBPopulated,
		); err != nil {
			return nil, err
		}
		row.BucketARate = rate(row.BucketAPopulated, row.BucketACallCount)
		row.BucketBRate = rate(row.BucketBPopulated, row.BucketBCallCount)
		row.RateDelta = row.BucketARate - row.BucketBRate
		report.Fields = append(report.Fields, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(report.Fields, func(i, j int) bool {
		left := absoluteFloat(report.Fields[i].RateDelta)
		right := absoluteFloat(report.Fields[j].RateDelta)
		if left != right {
			return left > right
		}
		leftPopulated := report.Fields[i].BucketAPopulated + report.Fields[i].BucketBPopulated
		rightPopulated := report.Fields[j].BucketAPopulated + report.Fields[j].BucketBPopulated
		if leftPopulated != rightPopulated {
			return leftPopulated > rightPopulated
		}
		return report.Fields[i].FieldName < report.Fields[j].FieldName
	})
	if len(report.Fields) > limit {
		report.Fields = report.Fields[:limit]
	}
	return report, nil
}
func lifecycleBucketDefinitions() []LifecycleBucketDefinition {
	return []LifecycleBucketDefinition{
		{
			Bucket:         "outbound_prospecting",
			Label:          "Outbound Prospecting",
			Description:    "Outbound calls without Opportunity context, typically top-of-funnel prospecting.",
			PrimarySignals: []string{"call.system=Upload API", "call.direction=Outbound", "no Opportunity context"},
		},
		{
			Bucket:         "active_sales_pipeline",
			Label:          "Active Sales Pipeline",
			Description:    "Open Opportunity-linked sales conversations before late-stage sales.",
			PrimarySignals: []string{"Opportunity context", "Opportunity.StageName not closed or late-stage"},
		},
		{
			Bucket:         "late_stage_sales",
			Label:          "Late-Stage Sales",
			Description:    "Opportunity calls in demo/business-case, proposal, contract review, or signing stages.",
			PrimarySignals: []string{"Opportunity.StageName"},
		},
		{
			Bucket:         "closed_won_lost_review",
			Label:          "Closed Won/Lost Review",
			Description:    "Calls tied to closed Opportunities that are useful for win/loss review.",
			PrimarySignals: []string{"Opportunity.StageName=Closed Won", "Opportunity.StageName=Closed Lost"},
		},
		{
			Bucket:         "renewal",
			Label:          "Renewal",
			Description:    "Post-sales renewal conversations.",
			PrimarySignals: []string{"Opportunity.Type=Renewal"},
		},
		{
			Bucket:         "upsell_expansion",
			Label:          "Upsell / Expansion",
			Description:    "Post-sales expansion conversations such as upsell, existing business, or contract increase calls.",
			PrimarySignals: []string{"Opportunity.Type", "Opportunity.Expansion_Bookings__c", "Opportunity.One_Year_Upsell__c"},
		},
		{
			Bucket:         "customer_success_account",
			Label:          "Customer Success / Account",
			Description:    "Account-context calls for active or inactive customers without stronger Opportunity lifecycle signals.",
			PrimarySignals: []string{"Account.Account_Type__c starts with Customer"},
		},
		{
			Bucket:         "partner",
			Label:          "Partner",
			Description:    "Partner or referral partner conversations.",
			PrimarySignals: []string{"Opportunity.Type=Partnership", "Account.Account_Type__c partner values"},
		},
		{
			Bucket:         "unknown",
			Label:          "Unknown",
			Description:    "Calls without enough CRM or metadata signal for a deterministic lifecycle bucket.",
			PrimarySignals: []string{},
		},
	}
}
func isKnownLifecycleBucket(bucket string) bool {
	bucket = strings.TrimSpace(bucket)
	for _, definition := range lifecycleBucketDefinitions() {
		if definition.Bucket == bucket {
			return true
		}
	}
	return false
}
func lifecycleBucketOrderSQL(column string) string {
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
func splitEvidenceFields(value string) []string {
	parts := strings.Split(value, "|")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
func absoluteFloat(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}
