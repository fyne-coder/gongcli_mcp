package postgres

import (
	"context"
	"database/sql"
)

func refreshCallReadModelTx(ctx context.Context, tx *sql.Tx, callID string) error {
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, callID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM call_context_fields WHERE call_id = $1`, callID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM call_context_objects WHERE call_id = $1`, callID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, postgresInsertContextObjectsSQL, callID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, postgresInsertContextFieldsSQL, callID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE calls SET context_present = EXISTS (SELECT 1 FROM call_context_objects WHERE call_id = $1) WHERE call_id = $1`, callID); err != nil {
		return err
	}
	return refreshCallFactsTx(ctx, tx, callID)
}

func backfillReadModelTx(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT call_id FROM calls ORDER BY call_id`)
	if err != nil {
		return err
	}
	callIDs := []string{}
	for rows.Next() {
		var callID string
		if err := rows.Scan(&callID); err != nil {
			rows.Close()
			return err
		}
		callIDs = append(callIDs, callID)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, callID := range callIDs {
		if err := refreshCallReadModelTx(ctx, tx, callID); err != nil {
			return err
		}
	}
	return nil
}

func refreshCallFactsTx(ctx context.Context, tx *sql.Tx, callID string) error {
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, callID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM call_facts WHERE call_id = $1`, callID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, postgresInsertCallFactsSQL, callID, nowUTC())
	return err
}

const postgresContextSourcesSQL = `
SELECT c.call_id,
       object_item.object_json,
       source_item.source_name || ':' || object_item.ordinal::text AS object_key,
       COALESCE(NULLIF(object_item.object_json->>'objectType', ''), NULLIF(object_item.object_json->>'type', ''), NULLIF(object_item.object_json->>'entityType', ''), '') AS object_type,
       COALESCE(NULLIF(object_item.object_json->>'id', ''), NULLIF(object_item.object_json->>'objectId', ''), NULLIF(object_item.object_json->>'crmId', ''), '') AS object_id
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
 WHERE c.call_id = $1`

const postgresInsertContextObjectsSQL = `
WITH raw_objects AS (` + postgresContextSourcesSQL + `)
INSERT INTO call_context_objects(call_id, object_key, object_type, object_id, object_name, raw_json)
SELECT call_id,
       object_key,
       object_type,
       object_id,
       COALESCE(NULLIF(object_json->>'name', ''), NULLIF(object_json->>'displayName', ''), NULLIF(object_json->>'label', ''), NULLIF(object_json->>'title', ''), '') AS object_name,
       object_json
  FROM raw_objects
 WHERE TRIM(object_type) <> ''
ON CONFLICT(call_id, object_key) DO UPDATE SET
	object_type = EXCLUDED.object_type,
	object_id = EXCLUDED.object_id,
	object_name = EXCLUDED.object_name,
	raw_json = EXCLUDED.raw_json`

const postgresInsertContextFieldsSQL = `
WITH raw_objects AS (` + postgresContextSourcesSQL + `),
object_fields AS (
	SELECT ro.call_id,
	       ro.object_key,
	       COALESCE(NULLIF(field_item.field_json->>'name', ''), NULLIF(field_item.field_json->>'fieldName', ''), NULLIF(field_item.field_json->>'apiName', ''), NULLIF(field_item.field_json->>'label', ''), NULLIF(field_item.field_json->>'fieldLabel', ''), '') AS field_name,
	       COALESCE(NULLIF(field_item.field_json->>'label', ''), NULLIF(field_item.field_json->>'fieldLabel', ''), NULLIF(field_item.field_json->>'displayName', ''), '') AS field_label,
	       COALESCE(NULLIF(field_item.field_json->>'type', ''), NULLIF(field_item.field_json->>'fieldType', ''), '') AS field_type,
	       COALESCE(NULLIF(field_item.field_json->>'value', ''), NULLIF(field_item.field_json->>'fieldValue', ''), '') AS field_value_text,
	       field_item.field_json AS raw_json
	  FROM raw_objects ro
	  CROSS JOIN LATERAL jsonb_array_elements(
		CASE WHEN jsonb_typeof(ro.object_json->'fields') = 'array' THEN ro.object_json->'fields' ELSE '[]'::jsonb END
	  ) AS field_item(field_json)
	UNION ALL
	SELECT ro.call_id,
	       ro.object_key,
	       field_item.field_name,
	       '',
	       '',
	       CASE
		       WHEN jsonb_typeof(field_item.field_value_json) = 'string' THEN field_item.field_value_json#>>'{}'
		       ELSE field_item.field_value_json::text
	       END AS field_value_text,
	       jsonb_build_object('name', field_item.field_name, 'value', field_item.field_value_json) AS raw_json
	  FROM raw_objects ro
	  CROSS JOIN LATERAL jsonb_each(
		CASE WHEN jsonb_typeof(ro.object_json->'fields') = 'object' THEN ro.object_json->'fields' ELSE '{}'::jsonb END
	  ) AS field_item(field_name, field_value_json)
	UNION ALL
	SELECT ro.call_id,
	       ro.object_key,
	       COALESCE(NULLIF(field_item.field_json->>'name', ''), NULLIF(field_item.field_json->>'fieldName', ''), NULLIF(field_item.field_json->>'apiName', ''), NULLIF(field_item.field_json->>'label', ''), NULLIF(field_item.field_json->>'fieldLabel', ''), '') AS field_name,
	       COALESCE(NULLIF(field_item.field_json->>'label', ''), NULLIF(field_item.field_json->>'fieldLabel', ''), NULLIF(field_item.field_json->>'displayName', ''), '') AS field_label,
	       COALESCE(NULLIF(field_item.field_json->>'type', ''), NULLIF(field_item.field_json->>'fieldType', ''), '') AS field_type,
	       COALESCE(NULLIF(field_item.field_json->>'value', ''), NULLIF(field_item.field_json->>'fieldValue', ''), '') AS field_value_text,
	       field_item.field_json AS raw_json
	  FROM raw_objects ro
	  CROSS JOIN LATERAL jsonb_array_elements(
		CASE WHEN jsonb_typeof(ro.object_json->'properties') = 'array' THEN ro.object_json->'properties' ELSE '[]'::jsonb END
	  ) AS field_item(field_json)
	UNION ALL
	SELECT ro.call_id,
	       ro.object_key,
	       field_item.field_name,
	       '',
	       '',
	       CASE
		       WHEN jsonb_typeof(field_item.field_value_json) = 'string' THEN field_item.field_value_json#>>'{}'
		       ELSE field_item.field_value_json::text
	       END AS field_value_text,
	       jsonb_build_object('name', field_item.field_name, 'value', field_item.field_value_json) AS raw_json
	  FROM raw_objects ro
	  CROSS JOIN LATERAL jsonb_each(
		CASE WHEN jsonb_typeof(ro.object_json->'properties') = 'object' THEN ro.object_json->'properties' ELSE '{}'::jsonb END
	  ) AS field_item(field_name, field_value_json)
)
INSERT INTO call_context_fields(call_id, object_key, field_name, field_label, field_type, field_value_text, raw_json)
SELECT call_id, object_key, field_name, field_label, field_type, field_value_text, raw_json
  FROM object_fields
 WHERE TRIM(field_name) <> ''
ON CONFLICT(call_id, object_key, field_name) DO UPDATE SET
	field_label = EXCLUDED.field_label,
	field_type = EXCLUDED.field_type,
	field_value_text = EXCLUDED.field_value_text,
	raw_json = EXCLUDED.raw_json`

const postgresInsertCallFactsSQL = `
WITH selected_account AS (
	SELECT DISTINCT ON (call_id) call_id, object_key, object_id
	  FROM call_context_objects
	 WHERE LOWER(object_type) = 'account'
	   AND call_id = $1
	 ORDER BY call_id, CASE WHEN TRIM(object_id) <> '' THEN 0 ELSE 1 END, object_key
),
selected_opportunity AS (
	SELECT DISTINCT ON (call_id) call_id, object_key, object_id
	  FROM call_context_objects
	 WHERE LOWER(object_type) = 'opportunity'
	   AND call_id = $1
	 ORDER BY call_id, CASE WHEN TRIM(object_id) <> '' THEN 0 ELSE 1 END, object_key
),
crm AS (
	SELECT c.call_id,
	       COALESCE(MAX(sa.object_id), '') AS account_id,
	       COALESCE(MAX(CASE WHEN f.object_key = sa.object_key AND f.field_name = 'Account_Type__c' THEN TRIM(f.field_value_text) END), '') AS account_type,
	       COALESCE(MAX(CASE WHEN f.object_key = sa.object_key AND f.field_name = 'Industry' THEN TRIM(f.field_value_text) END), '') AS account_industry,
	       COALESCE(MAX(CASE WHEN f.object_key = sa.object_key AND f.field_name = 'Revenue_Range_f__c' THEN TRIM(f.field_value_text) END), '') AS account_revenue_range,
	       COALESCE(MAX(CASE WHEN f.object_key = sa.object_key AND f.field_name = 'Primary_Procurement_System__c' THEN TRIM(f.field_value_text) END), '') AS account_primary_procurement_system,
	       COALESCE(MAX(so.object_id), '') AS opportunity_id,
	       COALESCE(MAX(CASE WHEN f.object_key = so.object_key AND f.field_name = 'StageName' THEN TRIM(f.field_value_text) END), '') AS opportunity_stage,
	       COALESCE(MAX(CASE WHEN f.object_key = so.object_key AND f.field_name = 'Type' THEN TRIM(f.field_value_text) END), '') AS opportunity_type,
	       COALESCE(MAX(CASE WHEN f.object_key = so.object_key AND f.field_name = 'Amount' THEN TRIM(f.field_value_text) END), '') AS opportunity_amount,
	       COALESCE(MAX(CASE WHEN f.object_key = so.object_key AND f.field_name = 'Probability' THEN TRIM(f.field_value_text) END), '') AS opportunity_probability,
	       COALESCE(MAX(CASE WHEN f.object_key = so.object_key AND f.field_name = 'Forecast_Category_VP__c' THEN TRIM(f.field_value_text) END), '') AS opportunity_forecast_category,
	       COALESCE(MAX(CASE WHEN f.object_key = so.object_key AND f.field_name = 'Primary_Lead_Source__c' THEN TRIM(f.field_value_text) END), '') AS opportunity_primary_lead_source,
	       COALESCE(MAX(CASE WHEN f.object_key = so.object_key AND f.field_name = 'Procurement_System__c' THEN TRIM(f.field_value_text) END), '') AS opportunity_procurement_system,
	       COUNT(DISTINCT CASE WHEN LOWER(o.object_type) = 'opportunity' THEN o.object_key END) AS opportunity_count,
	       COUNT(DISTINCT CASE WHEN LOWER(o.object_type) = 'account' THEN o.object_key END) AS account_count,
	       COALESCE(MAX(CASE WHEN LOWER(o.object_type) = 'opportunity' AND f.field_name = 'Type' AND LOWER(TRIM(f.field_value_text)) = 'partnership' THEN 1 ELSE 0 END), 0) AS has_partner_opportunity,
	       COALESCE(MAX(CASE WHEN LOWER(o.object_type) = 'account' AND f.field_name = 'Account_Type__c' AND (LOWER(TRIM(f.field_value_text)) LIKE 'partner%' OR LOWER(TRIM(f.field_value_text)) LIKE 'technology referral partner%') THEN 1 ELSE 0 END), 0) AS has_partner_account,
	       COALESCE(MAX(CASE WHEN LOWER(o.object_type) = 'opportunity' AND f.field_name = 'Type' AND LOWER(TRIM(f.field_value_text)) = 'renewal' THEN 1 ELSE 0 END), 0) AS has_renewal_opportunity,
	       COALESCE(MAX(CASE WHEN LOWER(o.object_type) = 'opportunity' AND f.field_name = 'Type' AND LOWER(TRIM(f.field_value_text)) IN ('upsell', 'existing business', 'year 2 increase', 'year 3 increase') THEN 1 ELSE 0 END), 0) AS has_upsell_opportunity,
	       COALESCE(MAX(CASE WHEN LOWER(o.object_type) = 'opportunity' AND f.field_name IN ('Expansion_Bookings__c', 'One_Year_Upsell__c') AND TRIM(f.field_value_text) NOT IN ('', '0', '0.0', '0.00') THEN 1 ELSE 0 END), 0) AS has_expansion_signal,
	       COALESCE(MAX(CASE WHEN LOWER(o.object_type) = 'opportunity' AND f.field_name = 'StageName' AND LOWER(TRIM(f.field_value_text)) IN ('closed won', 'closed lost') THEN 1 ELSE 0 END), 0) AS has_closed_stage,
	       COALESCE(MAX(CASE WHEN LOWER(o.object_type) = 'opportunity' AND f.field_name = 'StageName' AND LOWER(TRIM(f.field_value_text)) IN ('demo & business case', 'business case', 'sow & proposal', 'contract review', 'contract signing', 'crucible/last mile') THEN 1 ELSE 0 END), 0) AS has_late_stage,
	       COALESCE(MAX(CASE WHEN LOWER(o.object_type) = 'account' AND f.field_name = 'Account_Type__c' AND LOWER(TRIM(f.field_value_text)) LIKE 'customer%' THEN 1 ELSE 0 END), 0) AS has_customer_account
	  FROM calls c
	  LEFT JOIN selected_account sa ON sa.call_id = c.call_id
	  LEFT JOIN selected_opportunity so ON so.call_id = c.call_id
	  LEFT JOIN call_context_objects o ON o.call_id = c.call_id
	  LEFT JOIN call_context_fields f ON f.call_id = o.call_id AND f.object_key = o.object_key
	 WHERE c.call_id = $1
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
	       COALESCE(NULLIF(c.raw_json#>>'{metaData,purpose}', ''), NULLIF(c.raw_json->>'purpose', ''), '') AS purpose,
	       COALESCE(NULLIF(c.raw_json#>>'{metaData,primaryUserId}', ''), NULLIF(c.raw_json->>'primaryUserId', ''), '') AS primary_user_id,
	       CASE WHEN COALESCE(NULLIF(c.raw_json#>>'{metaData,calendarEventId}', ''), NULLIF(c.raw_json->>'calendarEventId', ''), '') <> '' THEN true ELSE false END AS calendar_event_present,
	       CASE WHEN COALESCE(NULLIF(c.raw_json#>>'{metaData,calendarEventId}', ''), NULLIF(c.raw_json->>'calendarEventId', ''), '') <> '' THEN 'calendar' ELSE 'no_calendar' END AS calendar_event_status,
	       COALESCE(NULLIF(c.raw_json#>>'{metaData,sdrDisposition}', ''), NULLIF(c.raw_json->>'sdrDisposition', ''), '') AS sdr_disposition,
	       CASE WHEN t.call_id IS NULL THEN false ELSE true END AS transcript_present,
	       CASE WHEN t.call_id IS NULL THEN 'missing' ELSE 'present' END AS transcript_status,
	       COALESCE(crm.account_id, '') AS account_id,
	       COALESCE(crm.account_type, '') AS account_type,
	       COALESCE(crm.account_industry, '') AS account_industry,
	       COALESCE(crm.account_revenue_range, '') AS account_revenue_range,
	       COALESCE(crm.account_primary_procurement_system, '') AS account_primary_procurement_system,
	       COALESCE(crm.opportunity_id, '') AS opportunity_id,
	       COALESCE(crm.opportunity_stage, '') AS opportunity_stage,
	       COALESCE(crm.opportunity_type, '') AS opportunity_type,
	       COALESCE(crm.opportunity_amount, '') AS opportunity_amount,
	       COALESCE(crm.opportunity_probability, '') AS opportunity_probability,
	       COALESCE(crm.opportunity_forecast_category, '') AS opportunity_forecast_category,
	       COALESCE(crm.opportunity_primary_lead_source, '') AS opportunity_primary_lead_source,
	       COALESCE(crm.opportunity_procurement_system, '') AS opportunity_procurement_system,
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
	 WHERE c.call_id = $1
)
INSERT INTO call_facts(
	call_id, title, started_at, call_date, call_month, duration_seconds, duration_bucket,
	system, direction, scope, purpose, primary_user_id, calendar_event_present, calendar_event_status,
	sdr_disposition, transcript_present, transcript_status, lifecycle_bucket, lifecycle_confidence,
	lifecycle_reason, lifecycle_evidence_fields, account_id, account_type, account_industry,
	account_revenue_range, account_primary_procurement_system, opportunity_id, opportunity_stage,
	opportunity_type, opportunity_amount, opportunity_probability, opportunity_forecast_category,
	opportunity_primary_lead_source, opportunity_procurement_system, opportunity_count, account_count, updated_at
)
SELECT c.call_id,
       c.title,
       c.started_at,
       c.call_date,
       c.call_month,
       c.duration_seconds,
       CASE WHEN c.duration_seconds < 60 THEN 'under_1m' WHEN c.duration_seconds < 300 THEN '1_5m' WHEN c.duration_seconds < 900 THEN '5_15m' WHEN c.duration_seconds < 1800 THEN '15_30m' WHEN c.duration_seconds < 2700 THEN '30_45m' ELSE '45m_plus' END AS duration_bucket,
       c.system,
       c.direction,
       c.scope,
       c.purpose,
       c.primary_user_id,
       c.calendar_event_present,
       c.calendar_event_status,
       c.sdr_disposition,
       c.transcript_present,
       c.transcript_status,
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
       c.account_id,
       c.account_type,
       c.account_industry,
       c.account_revenue_range,
       c.account_primary_procurement_system,
       c.opportunity_id,
       c.opportunity_stage,
       c.opportunity_type,
       c.opportunity_amount,
       c.opportunity_probability,
       c.opportunity_forecast_category,
       c.opportunity_primary_lead_source,
       c.opportunity_procurement_system,
       c.opportunity_count,
       c.account_count,
       $2
  FROM signals c`
