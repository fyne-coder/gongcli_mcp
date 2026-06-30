package crmdimensions

// SQLiteCallFactsViewMigrationSQL recreates call_facts with business-safe CRM
// promoted columns while preserving the existing account/opportunity selection
// logic.
var SQLiteCallFactsViewMigrationSQL = `
DROP VIEW IF EXISTS call_facts;

CREATE VIEW IF NOT EXISTS call_facts AS
WITH object_fields AS (
	SELECT c.call_id,
	       o.object_key,
	       o.object_type,
	       o.object_id,
	       COALESCE(MAX(CASE WHEN f.field_name = 'Account_Type__c' THEN TRIM(f.field_value_text) END), '') AS account_type,
	       COALESCE(MAX(CASE WHEN f.field_name = 'Industry' THEN TRIM(f.field_value_text) END), '') AS account_industry,
	       COALESCE(MAX(CASE WHEN f.field_name = 'Revenue_Range_f__c' THEN TRIM(f.field_value_text) END), '') AS account_revenue_range,
	       COALESCE(MAX(CASE WHEN f.field_name = 'Primary_Procurement_System__c' THEN TRIM(f.field_value_text) END), '') AS account_primary_procurement_system,
` + SQLiteObjectFieldExtractLines() + `
	       COALESCE(MAX(CASE WHEN f.field_name = 'StageName' THEN TRIM(f.field_value_text) END), '') AS opportunity_stage,
	       COALESCE(MAX(CASE WHEN f.field_name = 'Type' THEN TRIM(f.field_value_text) END), '') AS opportunity_type,
	       COALESCE(MAX(CASE WHEN f.field_name = 'Amount' THEN TRIM(f.field_value_text) END), '') AS opportunity_amount,
	       COALESCE(MAX(CASE WHEN f.field_name = 'Probability' THEN TRIM(f.field_value_text) END), '') AS opportunity_probability,
	       COALESCE(MAX(CASE WHEN f.field_name = 'Forecast_Category_VP__c' THEN TRIM(f.field_value_text) END), '') AS opportunity_forecast_category,
	       COALESCE(MAX(CASE WHEN f.field_name = 'Primary_Lead_Source__c' THEN TRIM(f.field_value_text) END), '') AS opportunity_primary_lead_source,
	       COALESCE(MAX(CASE WHEN f.field_name = 'Procurement_System__c' THEN TRIM(f.field_value_text) END), '') AS opportunity_procurement_system
	  FROM calls c
	  JOIN call_context_objects o
	    ON o.call_id = c.call_id
	  LEFT JOIN call_context_fields f
	    ON f.call_id = o.call_id
	   AND f.object_key = o.object_key
	 GROUP BY c.call_id, o.object_key, o.object_type, o.object_id
),
object_counts AS (
	SELECT call_id,
	       COUNT(DISTINCT CASE WHEN object_type = 'Opportunity' THEN object_key END) AS opportunity_count,
	       COUNT(DISTINCT CASE WHEN object_type = 'Account' THEN object_key END) AS account_count
	  FROM object_fields
	 GROUP BY call_id
),
account_choice AS (
	SELECT *
	  FROM (
		SELECT object_fields.*,
		       ROW_NUMBER() OVER (
			       PARTITION BY call_id
			       ORDER BY
				       CASE
					       WHEN LOWER(account_type) LIKE 'customer%' THEN 1
					       WHEN LOWER(account_type) LIKE 'partner%' THEN 2
					       WHEN LOWER(account_type) LIKE 'technology referral partner%' THEN 2
					       WHEN TRIM(account_type) <> '' THEN 3
					       ELSE 9
				       END,
				       object_key
		       ) AS rn
		  FROM object_fields
		 WHERE object_type = 'Account'
	  )
	 WHERE rn = 1
),
opportunity_choice AS (
	SELECT *
	  FROM (
		SELECT object_fields.*,
		       ROW_NUMBER() OVER (
			       PARTITION BY call_id
			       ORDER BY
				       CASE
					       WHEN LOWER(opportunity_type) = 'partnership' THEN 1
					       WHEN LOWER(opportunity_type) = 'renewal' THEN 2
					       WHEN LOWER(opportunity_type) IN ('upsell', 'existing business', 'year 2 increase', 'year 3 increase') THEN 3
					       WHEN LOWER(opportunity_stage) IN ('closed won', 'closed lost') THEN 4
					       WHEN LOWER(opportunity_stage) IN ('demo & business case', 'business case', 'sow & proposal', 'contract review', 'contract signing', 'crucible/last mile') THEN 5
					       WHEN TRIM(opportunity_stage) <> '' THEN 6
					       ELSE 9
				       END,
				       object_key
		       ) AS rn
		  FROM object_fields
		 WHERE object_type = 'Opportunity'
	  )
	 WHERE rn = 1
)
SELECT c.call_id,
       c.title,
       c.started_at,
       substr(c.started_at, 1, 10) AS call_date,
       substr(c.started_at, 1, 7) AS call_month,
       c.duration_seconds,
       CASE
	       WHEN c.duration_seconds < 60 THEN 'under_1m'
	       WHEN c.duration_seconds < 300 THEN '1_5m'
	       WHEN c.duration_seconds < 900 THEN '5_15m'
	       WHEN c.duration_seconds < 1800 THEN '15_30m'
	       WHEN c.duration_seconds < 2700 THEN '30_45m'
	       ELSE '45m_plus'
       END AS duration_bucket,
       COALESCE(json_extract(c.raw_json, '$.metaData.system'), json_extract(c.raw_json, '$.system'), '') AS system,
       COALESCE(json_extract(c.raw_json, '$.metaData.direction'), json_extract(c.raw_json, '$.direction'), '') AS direction,
       COALESCE(NULLIF(TRIM(json_extract(c.raw_json, '$.metaData.scope')), ''), 'Unknown') AS scope,
       COALESCE(json_extract(c.raw_json, '$.metaData.purpose'), json_extract(c.raw_json, '$.purpose'), '') AS purpose,
       COALESCE(json_extract(c.raw_json, '$.metaData.primaryUserId'), json_extract(c.raw_json, '$.primaryUserId'), '') AS primary_user_id,
       CASE
	       WHEN COALESCE(json_extract(c.raw_json, '$.metaData.calendarEventId'), json_extract(c.raw_json, '$.calendarEventId'), '') <> '' THEN 1
	       ELSE 0
       END AS calendar_event_present,
       CASE
	       WHEN COALESCE(json_extract(c.raw_json, '$.metaData.calendarEventId'), json_extract(c.raw_json, '$.calendarEventId'), '') <> '' THEN 'calendar'
	       ELSE 'no_calendar'
       END AS calendar_event_status,
       COALESCE(json_extract(c.raw_json, '$.metaData.sdrDisposition'), json_extract(c.raw_json, '$.sdrDisposition'), '') AS sdr_disposition,
       CASE WHEN t.call_id IS NULL THEN 0 ELSE 1 END AS transcript_present,
       CASE WHEN t.call_id IS NULL THEN 'missing' ELSE 'present' END AS transcript_status,
       COALESCE(l.bucket, 'unknown') AS lifecycle_bucket,
       COALESCE(l.confidence, 'low') AS lifecycle_confidence,
       COALESCE(l.reason, '') AS lifecycle_reason,
       COALESCE(l.evidence_fields, '') AS lifecycle_evidence_fields,
       COALESCE(a.object_id, '') AS account_id,
       COALESCE(a.account_type, '') AS account_type,
       COALESCE(a.account_industry, '') AS account_industry,
       COALESCE(a.account_revenue_range, '') AS account_revenue_range,
       COALESCE(a.account_primary_procurement_system, '') AS account_primary_procurement_system,
` + SQLiteCallFactsAccountSelectLines() + `
       COALESCE(o.object_id, '') AS opportunity_id,
       COALESCE(o.opportunity_stage, '') AS opportunity_stage,
       COALESCE(o.opportunity_type, '') AS opportunity_type,
       COALESCE(o.opportunity_amount, '') AS opportunity_amount,
       COALESCE(o.opportunity_probability, '') AS opportunity_probability,
       COALESCE(o.opportunity_forecast_category, '') AS opportunity_forecast_category,
       COALESCE(o.opportunity_primary_lead_source, '') AS opportunity_primary_lead_source,
       COALESCE(o.opportunity_procurement_system, '') AS opportunity_procurement_system,
` + SQLiteCallFactsOpportunitySelectLines() + `
       COALESCE(oc.opportunity_count, 0) AS opportunity_count,
       COALESCE(oc.account_count, 0) AS account_count
  FROM calls c
  LEFT JOIN transcripts t
    ON t.call_id = c.call_id
  LEFT JOIN call_lifecycle l
    ON l.call_id = c.call_id
  LEFT JOIN object_counts oc
    ON oc.call_id = c.call_id
  LEFT JOIN account_choice a
    ON a.call_id = c.call_id
  LEFT JOIN opportunity_choice o
    ON o.call_id = c.call_id;
`
