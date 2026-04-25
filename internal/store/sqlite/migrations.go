package sqlite

var migrations = []string{
	`
CREATE TABLE IF NOT EXISTS sync_runs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	scope TEXT NOT NULL,
	sync_key TEXT NOT NULL DEFAULT '',
	cursor TEXT NOT NULL DEFAULT '',
	from_value TEXT NOT NULL DEFAULT '',
	to_value TEXT NOT NULL DEFAULT '',
	request_context TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL,
	started_at TEXT NOT NULL,
	finished_at TEXT,
	records_seen INTEGER NOT NULL DEFAULT 0,
	records_written INTEGER NOT NULL DEFAULT 0,
	error_text TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_sync_runs_scope_started_at ON sync_runs(scope, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_sync_runs_status ON sync_runs(status);

CREATE TABLE IF NOT EXISTS sync_state (
	sync_key TEXT PRIMARY KEY,
	scope TEXT NOT NULL,
	cursor TEXT NOT NULL DEFAULT '',
	last_run_id INTEGER,
	last_status TEXT NOT NULL,
	last_error TEXT NOT NULL DEFAULT '',
	last_success_at TEXT,
	updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_sync_state_scope ON sync_state(scope);

CREATE TABLE IF NOT EXISTS calls (
	call_id TEXT PRIMARY KEY,
	title TEXT NOT NULL DEFAULT '',
	started_at TEXT NOT NULL DEFAULT '',
	duration_seconds INTEGER NOT NULL DEFAULT 0,
	parties_count INTEGER NOT NULL DEFAULT 0,
	context_present INTEGER NOT NULL DEFAULT 0,
	raw_json BLOB NOT NULL,
	raw_sha256 TEXT NOT NULL,
	first_seen_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_calls_started_at ON calls(started_at DESC);

CREATE TABLE IF NOT EXISTS call_context_objects (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	call_id TEXT NOT NULL,
	object_key TEXT NOT NULL,
	object_type TEXT NOT NULL,
	object_id TEXT NOT NULL DEFAULT '',
	object_name TEXT NOT NULL DEFAULT '',
	raw_json BLOB NOT NULL,
	UNIQUE(call_id, object_key)
);

CREATE INDEX IF NOT EXISTS idx_call_context_objects_call_id ON call_context_objects(call_id);
CREATE INDEX IF NOT EXISTS idx_call_context_objects_type ON call_context_objects(object_type);

CREATE TABLE IF NOT EXISTS call_context_fields (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	call_id TEXT NOT NULL,
	object_key TEXT NOT NULL,
	field_name TEXT NOT NULL,
	field_label TEXT NOT NULL DEFAULT '',
	field_type TEXT NOT NULL DEFAULT '',
	field_value_text TEXT NOT NULL DEFAULT '',
	raw_json BLOB NOT NULL,
	UNIQUE(call_id, object_key, field_name)
);

CREATE INDEX IF NOT EXISTS idx_call_context_fields_call_id ON call_context_fields(call_id);
CREATE INDEX IF NOT EXISTS idx_call_context_fields_name ON call_context_fields(field_name);

CREATE TABLE IF NOT EXISTS users (
	user_id TEXT PRIMARY KEY,
	email TEXT NOT NULL DEFAULT '',
	first_name TEXT NOT NULL DEFAULT '',
	last_name TEXT NOT NULL DEFAULT '',
	display_name TEXT NOT NULL DEFAULT '',
	title TEXT NOT NULL DEFAULT '',
	active INTEGER NOT NULL DEFAULT 0,
	raw_json BLOB NOT NULL,
	raw_sha256 TEXT NOT NULL,
	first_seen_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_users_email ON users(email);

CREATE TABLE IF NOT EXISTS transcripts (
	call_id TEXT PRIMARY KEY,
	raw_json BLOB NOT NULL,
	raw_sha256 TEXT NOT NULL,
	segment_count INTEGER NOT NULL DEFAULT 0,
	first_seen_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS transcript_segments (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	call_id TEXT NOT NULL,
	segment_index INTEGER NOT NULL,
	speaker_id TEXT NOT NULL DEFAULT '',
	start_ms INTEGER NOT NULL DEFAULT 0,
	end_ms INTEGER NOT NULL DEFAULT 0,
	text TEXT NOT NULL,
	raw_json BLOB NOT NULL,
	UNIQUE(call_id, segment_index)
);

CREATE INDEX IF NOT EXISTS idx_transcript_segments_call_id ON transcript_segments(call_id, segment_index);

CREATE VIRTUAL TABLE IF NOT EXISTS transcript_segments_fts USING fts5(
	text,
	call_id UNINDEXED,
	speaker_id UNINDEXED,
	content='transcript_segments',
	content_rowid='id',
	tokenize='unicode61 remove_diacritics 2'
);

CREATE TRIGGER IF NOT EXISTS transcript_segments_ai AFTER INSERT ON transcript_segments BEGIN
	INSERT INTO transcript_segments_fts(rowid, text, call_id, speaker_id)
	VALUES (new.id, new.text, new.call_id, new.speaker_id);
END;

CREATE TRIGGER IF NOT EXISTS transcript_segments_ad AFTER DELETE ON transcript_segments BEGIN
	INSERT INTO transcript_segments_fts(transcript_segments_fts, rowid, text, call_id, speaker_id)
	VALUES ('delete', old.id, old.text, old.call_id, old.speaker_id);
END;

CREATE TRIGGER IF NOT EXISTS transcript_segments_au AFTER UPDATE ON transcript_segments BEGIN
	INSERT INTO transcript_segments_fts(transcript_segments_fts, rowid, text, call_id, speaker_id)
	VALUES ('delete', old.id, old.text, old.call_id, old.speaker_id);
	INSERT INTO transcript_segments_fts(rowid, text, call_id, speaker_id)
	VALUES (new.id, new.text, new.call_id, new.speaker_id);
END;

INSERT INTO transcript_segments_fts(transcript_segments_fts) VALUES ('rebuild');
`,
	`
CREATE INDEX IF NOT EXISTS idx_call_context_objects_type_object_call
	ON call_context_objects(object_type, object_id, call_id, object_key);

CREATE INDEX IF NOT EXISTS idx_call_context_objects_type_call_key
	ON call_context_objects(object_type, call_id, object_key);

CREATE INDEX IF NOT EXISTS idx_call_context_fields_name_call_key_value
	ON call_context_fields(field_name, call_id, object_key, field_value_text);
`,
	`
CREATE VIEW IF NOT EXISTS call_lifecycle AS
WITH signals AS (
	SELECT c.call_id,
	       c.title,
	       c.started_at,
	       c.duration_seconds,
	       COALESCE(json_extract(c.raw_json, '$.metaData.system'), json_extract(c.raw_json, '$.system'), '') AS system,
	       COALESCE(json_extract(c.raw_json, '$.metaData.direction'), json_extract(c.raw_json, '$.direction'), '') AS direction,
	       COALESCE(MAX(CASE WHEN o.object_type = 'Account' AND f.field_name = 'Account_Type__c' THEN TRIM(f.field_value_text) END), '') AS account_type,
	       COALESCE(MAX(CASE WHEN o.object_type = 'Opportunity' AND f.field_name = 'StageName' THEN TRIM(f.field_value_text) END), '') AS opportunity_stage,
	       COALESCE(MAX(CASE WHEN o.object_type = 'Opportunity' AND f.field_name = 'Type' THEN TRIM(f.field_value_text) END), '') AS opportunity_type,
	       MAX(CASE
		       WHEN o.object_type = 'Opportunity'
		        AND f.field_name IN ('Expansion_Bookings__c', 'One_Year_Upsell__c')
		        AND TRIM(f.field_value_text) NOT IN ('', '0', '0.0', '0.00')
		       THEN 1 ELSE 0
	       END) AS has_expansion_signal,
	       COUNT(DISTINCT CASE WHEN o.object_type = 'Opportunity' THEN o.object_key END) AS opportunity_count,
	       COUNT(DISTINCT CASE WHEN o.object_type = 'Account' THEN o.object_key END) AS account_count,
	       CASE WHEN t.call_id IS NULL THEN 0 ELSE 1 END AS transcript_present
	  FROM calls c
	  LEFT JOIN call_context_objects o
	    ON o.call_id = c.call_id
	  LEFT JOIN call_context_fields f
	    ON f.call_id = o.call_id
	   AND f.object_key = o.object_key
	  LEFT JOIN transcripts t
	    ON t.call_id = c.call_id
	 GROUP BY c.call_id
)
SELECT call_id,
       title,
       started_at,
       duration_seconds,
       system,
       direction,
       account_type,
       opportunity_stage,
       opportunity_type,
       has_expansion_signal,
       opportunity_count,
       account_count,
       transcript_present,
       CASE
	       WHEN LOWER(opportunity_type) = 'partnership'
	         OR LOWER(account_type) LIKE 'partner%'
	         OR LOWER(account_type) LIKE 'technology referral partner%'
	         THEN 'partner'
	       WHEN LOWER(opportunity_type) = 'renewal'
	         THEN 'renewal'
	       WHEN LOWER(opportunity_type) IN ('upsell', 'existing business', 'year 2 increase', 'year 3 increase')
	         OR has_expansion_signal = 1
	         THEN 'upsell_expansion'
	       WHEN LOWER(opportunity_stage) IN ('closed won', 'closed lost')
	         THEN 'closed_won_lost_review'
	       WHEN LOWER(opportunity_stage) IN ('demo & business case', 'business case', 'sow & proposal', 'contract review', 'contract signing', 'crucible/last mile')
	         THEN 'late_stage_sales'
	       WHEN opportunity_count > 0
	         THEN 'active_sales_pipeline'
	       WHEN LOWER(account_type) LIKE 'customer%'
	         THEN 'customer_success_account'
	       WHEN LOWER(system) = 'upload api' AND LOWER(direction) = 'outbound'
	         THEN 'outbound_prospecting'
	       ELSE 'unknown'
       END AS bucket,
       CASE
	       WHEN LOWER(opportunity_type) IN ('partnership', 'renewal', 'upsell', 'existing business', 'year 2 increase', 'year 3 increase')
	         OR has_expansion_signal = 1
	         OR LOWER(opportunity_stage) IN ('closed won', 'closed lost', 'demo & business case', 'business case', 'sow & proposal', 'contract review', 'contract signing', 'crucible/last mile')
	         OR LOWER(account_type) LIKE 'customer%'
	         OR LOWER(account_type) LIKE 'partner%'
	         OR LOWER(account_type) LIKE 'technology referral partner%'
	         THEN 'high'
	       WHEN opportunity_count > 0
	         OR (LOWER(system) = 'upload api' AND LOWER(direction) = 'outbound')
	         THEN 'medium'
	       ELSE 'low'
       END AS confidence,
       CASE
	       WHEN LOWER(opportunity_type) = 'partnership'
	         THEN 'Opportunity.Type=Partnership'
	       WHEN LOWER(account_type) LIKE 'partner%' OR LOWER(account_type) LIKE 'technology referral partner%'
	         THEN 'Account.Account_Type__c indicates partner'
	       WHEN LOWER(opportunity_type) = 'renewal'
	         THEN 'Opportunity.Type=Renewal'
	       WHEN LOWER(opportunity_type) IN ('upsell', 'existing business', 'year 2 increase', 'year 3 increase')
	         THEN 'Opportunity.Type indicates post-sales expansion'
	       WHEN has_expansion_signal = 1
	         THEN 'Opportunity expansion booking fields are populated'
	       WHEN LOWER(opportunity_stage) IN ('closed won', 'closed lost')
	         THEN 'Opportunity.StageName is closed'
	       WHEN LOWER(opportunity_stage) IN ('demo & business case', 'business case', 'sow & proposal', 'contract review', 'contract signing', 'crucible/last mile')
	         THEN 'Opportunity.StageName is late-stage'
	       WHEN opportunity_count > 0
	         THEN 'Opportunity context is attached'
	       WHEN LOWER(account_type) LIKE 'customer%'
	         THEN 'Account.Account_Type__c indicates customer'
	       WHEN LOWER(system) = 'upload api' AND LOWER(direction) = 'outbound'
	         THEN 'Outbound Upload API call without Opportunity context'
	       ELSE 'No strong lifecycle CRM signal'
       END AS reason,
       CASE
	       WHEN LOWER(opportunity_type) = 'partnership'
	         THEN 'Opportunity.Type'
	       WHEN LOWER(account_type) LIKE 'partner%' OR LOWER(account_type) LIKE 'technology referral partner%'
	         THEN 'Account.Account_Type__c'
	       WHEN LOWER(opportunity_type) = 'renewal'
	         THEN 'Opportunity.Type'
	       WHEN LOWER(opportunity_type) IN ('upsell', 'existing business', 'year 2 increase', 'year 3 increase')
	         THEN 'Opportunity.Type'
	       WHEN has_expansion_signal = 1
	         THEN 'Opportunity.Expansion_Bookings__c|Opportunity.One_Year_Upsell__c'
	       WHEN LOWER(opportunity_stage) IN ('closed won', 'closed lost')
	         THEN 'Opportunity.StageName'
	       WHEN LOWER(opportunity_stage) IN ('demo & business case', 'business case', 'sow & proposal', 'contract review', 'contract signing', 'crucible/last mile')
	         THEN 'Opportunity.StageName'
	       WHEN opportunity_count > 0
	         THEN 'Opportunity context'
	       WHEN LOWER(account_type) LIKE 'customer%'
	         THEN 'Account.Account_Type__c'
	       WHEN LOWER(system) = 'upload api' AND LOWER(direction) = 'outbound'
	         THEN 'call.system|call.direction'
	       ELSE ''
       END AS evidence_fields
  FROM signals;
`,
	`
DROP VIEW IF EXISTS call_lifecycle;

CREATE VIEW IF NOT EXISTS call_lifecycle AS
WITH signals AS (
	SELECT c.call_id,
	       c.title,
	       c.started_at,
	       c.duration_seconds,
	       COALESCE(json_extract(c.raw_json, '$.metaData.system'), json_extract(c.raw_json, '$.system'), '') AS system,
	       COALESCE(json_extract(c.raw_json, '$.metaData.direction'), json_extract(c.raw_json, '$.direction'), '') AS direction,
	       COALESCE(MAX(CASE WHEN o.object_type = 'Account' AND f.field_name = 'Account_Type__c' THEN TRIM(f.field_value_text) END), '') AS account_type,
	       COALESCE(MAX(CASE WHEN o.object_type = 'Opportunity' AND f.field_name = 'StageName' THEN TRIM(f.field_value_text) END), '') AS opportunity_stage,
	       COALESCE(MAX(CASE WHEN o.object_type = 'Opportunity' AND f.field_name = 'Type' THEN TRIM(f.field_value_text) END), '') AS opportunity_type,
	       MAX(CASE WHEN o.object_type = 'Opportunity' AND f.field_name = 'Type' AND LOWER(TRIM(f.field_value_text)) = 'partnership' THEN 1 ELSE 0 END) AS has_partner_opportunity,
	       MAX(CASE WHEN o.object_type = 'Account' AND f.field_name = 'Account_Type__c' AND (LOWER(TRIM(f.field_value_text)) LIKE 'partner%' OR LOWER(TRIM(f.field_value_text)) LIKE 'technology referral partner%') THEN 1 ELSE 0 END) AS has_partner_account,
	       MAX(CASE WHEN o.object_type = 'Opportunity' AND f.field_name = 'Type' AND LOWER(TRIM(f.field_value_text)) = 'renewal' THEN 1 ELSE 0 END) AS has_renewal_opportunity,
	       MAX(CASE WHEN o.object_type = 'Opportunity' AND f.field_name = 'Type' AND LOWER(TRIM(f.field_value_text)) IN ('upsell', 'existing business', 'year 2 increase', 'year 3 increase') THEN 1 ELSE 0 END) AS has_upsell_opportunity,
	       MAX(CASE
		       WHEN o.object_type = 'Opportunity'
		        AND f.field_name IN ('Expansion_Bookings__c', 'One_Year_Upsell__c')
		        AND TRIM(f.field_value_text) NOT IN ('', '0', '0.0', '0.00')
		       THEN 1 ELSE 0
	       END) AS has_expansion_signal,
	       MAX(CASE WHEN o.object_type = 'Opportunity' AND f.field_name = 'StageName' AND LOWER(TRIM(f.field_value_text)) IN ('closed won', 'closed lost') THEN 1 ELSE 0 END) AS has_closed_stage,
	       MAX(CASE WHEN o.object_type = 'Opportunity' AND f.field_name = 'StageName' AND LOWER(TRIM(f.field_value_text)) IN ('demo & business case', 'business case', 'sow & proposal', 'contract review', 'contract signing', 'crucible/last mile') THEN 1 ELSE 0 END) AS has_late_stage,
	       MAX(CASE WHEN o.object_type = 'Account' AND f.field_name = 'Account_Type__c' AND LOWER(TRIM(f.field_value_text)) LIKE 'customer%' THEN 1 ELSE 0 END) AS has_customer_account,
	       COUNT(DISTINCT CASE WHEN o.object_type = 'Opportunity' THEN o.object_key END) AS opportunity_count,
	       COUNT(DISTINCT CASE WHEN o.object_type = 'Account' THEN o.object_key END) AS account_count,
	       CASE WHEN t.call_id IS NULL THEN 0 ELSE 1 END AS transcript_present
	  FROM calls c
	  LEFT JOIN call_context_objects o
	    ON o.call_id = c.call_id
	  LEFT JOIN call_context_fields f
	    ON f.call_id = o.call_id
	   AND f.object_key = o.object_key
	  LEFT JOIN transcripts t
	    ON t.call_id = c.call_id
	 GROUP BY c.call_id
)
SELECT call_id,
       title,
       started_at,
       duration_seconds,
       system,
       direction,
       account_type,
       opportunity_stage,
       opportunity_type,
       has_expansion_signal,
       opportunity_count,
       account_count,
       transcript_present,
       CASE
	       WHEN has_partner_opportunity = 1 OR has_partner_account = 1
	         THEN 'partner'
	       WHEN has_renewal_opportunity = 1
	         THEN 'renewal'
	       WHEN has_upsell_opportunity = 1 OR has_expansion_signal = 1
	         THEN 'upsell_expansion'
	       WHEN has_closed_stage = 1
	         THEN 'closed_won_lost_review'
	       WHEN has_late_stage = 1
	         THEN 'late_stage_sales'
	       WHEN opportunity_count > 0
	         THEN 'active_sales_pipeline'
	       WHEN has_customer_account = 1
	         THEN 'customer_success_account'
	       WHEN LOWER(system) = 'upload api' AND LOWER(direction) = 'outbound'
	         THEN 'outbound_prospecting'
	       ELSE 'unknown'
       END AS bucket,
       CASE
	       WHEN has_partner_opportunity = 1
	         OR has_partner_account = 1
	         OR has_renewal_opportunity = 1
	         OR has_upsell_opportunity = 1
	         OR has_expansion_signal = 1
	         OR has_closed_stage = 1
	         OR has_late_stage = 1
	         OR has_customer_account = 1
	         THEN 'high'
	       WHEN opportunity_count > 0
	         OR (LOWER(system) = 'upload api' AND LOWER(direction) = 'outbound')
	         THEN 'medium'
	       ELSE 'low'
       END AS confidence,
       CASE
	       WHEN has_partner_opportunity = 1
	         THEN 'Opportunity.Type=Partnership'
	       WHEN has_partner_account = 1
	         THEN 'Account.Account_Type__c indicates partner'
	       WHEN has_renewal_opportunity = 1
	         THEN 'Opportunity.Type=Renewal'
	       WHEN has_upsell_opportunity = 1
	         THEN 'Opportunity.Type indicates post-sales expansion'
	       WHEN has_expansion_signal = 1
	         THEN 'Opportunity expansion booking fields are populated'
	       WHEN has_closed_stage = 1
	         THEN 'Opportunity.StageName is closed'
	       WHEN has_late_stage = 1
	         THEN 'Opportunity.StageName is late-stage'
	       WHEN opportunity_count > 0
	         THEN 'Opportunity context is attached'
	       WHEN has_customer_account = 1
	         THEN 'Account.Account_Type__c indicates customer'
	       WHEN LOWER(system) = 'upload api' AND LOWER(direction) = 'outbound'
	         THEN 'Outbound Upload API call without Opportunity context'
	       ELSE 'No strong lifecycle CRM signal'
       END AS reason,
       CASE
	       WHEN has_partner_opportunity = 1
	         THEN 'Opportunity.Type'
	       WHEN has_partner_account = 1
	         THEN 'Account.Account_Type__c'
	       WHEN has_renewal_opportunity = 1
	         THEN 'Opportunity.Type'
	       WHEN has_upsell_opportunity = 1
	         THEN 'Opportunity.Type'
	       WHEN has_expansion_signal = 1
	         THEN 'Opportunity.Expansion_Bookings__c|Opportunity.One_Year_Upsell__c'
	       WHEN has_closed_stage = 1
	         THEN 'Opportunity.StageName'
	       WHEN has_late_stage = 1
	         THEN 'Opportunity.StageName'
	       WHEN opportunity_count > 0
	         THEN 'Opportunity context'
	       WHEN has_customer_account = 1
	         THEN 'Account.Account_Type__c'
	       WHEN LOWER(system) = 'upload api' AND LOWER(direction) = 'outbound'
	         THEN 'call.system|call.direction'
	       ELSE ''
       END AS evidence_fields
	FROM signals;
`,
	`
CREATE VIEW IF NOT EXISTS call_facts AS
WITH crm AS (
	SELECT c.call_id,
	       COALESCE(MAX(CASE WHEN o.object_type = 'Account' THEN o.object_id END), '') AS account_id,
	       COALESCE(MAX(CASE WHEN o.object_type = 'Account' AND f.field_name = 'Account_Type__c' THEN TRIM(f.field_value_text) END), '') AS account_type,
	       COALESCE(MAX(CASE WHEN o.object_type = 'Account' AND f.field_name = 'Industry' THEN TRIM(f.field_value_text) END), '') AS account_industry,
	       COALESCE(MAX(CASE WHEN o.object_type = 'Account' AND f.field_name = 'Revenue_Range_f__c' THEN TRIM(f.field_value_text) END), '') AS account_revenue_range,
	       COALESCE(MAX(CASE WHEN o.object_type = 'Account' AND f.field_name = 'Primary_Procurement_System__c' THEN TRIM(f.field_value_text) END), '') AS account_primary_procurement_system,
	       COALESCE(MAX(CASE WHEN o.object_type = 'Opportunity' THEN o.object_id END), '') AS opportunity_id,
	       COALESCE(MAX(CASE WHEN o.object_type = 'Opportunity' AND f.field_name = 'StageName' THEN TRIM(f.field_value_text) END), '') AS opportunity_stage,
	       COALESCE(MAX(CASE WHEN o.object_type = 'Opportunity' AND f.field_name = 'Type' THEN TRIM(f.field_value_text) END), '') AS opportunity_type,
	       COALESCE(MAX(CASE WHEN o.object_type = 'Opportunity' AND f.field_name = 'Amount' THEN TRIM(f.field_value_text) END), '') AS opportunity_amount,
	       COALESCE(MAX(CASE WHEN o.object_type = 'Opportunity' AND f.field_name = 'Probability' THEN TRIM(f.field_value_text) END), '') AS opportunity_probability,
	       COALESCE(MAX(CASE WHEN o.object_type = 'Opportunity' AND f.field_name = 'Forecast_Category_VP__c' THEN TRIM(f.field_value_text) END), '') AS opportunity_forecast_category,
	       COALESCE(MAX(CASE WHEN o.object_type = 'Opportunity' AND f.field_name = 'Primary_Lead_Source__c' THEN TRIM(f.field_value_text) END), '') AS opportunity_primary_lead_source,
	       COALESCE(MAX(CASE WHEN o.object_type = 'Opportunity' AND f.field_name = 'Procurement_System__c' THEN TRIM(f.field_value_text) END), '') AS opportunity_procurement_system,
	       COUNT(DISTINCT CASE WHEN o.object_type = 'Opportunity' THEN o.object_key END) AS opportunity_count,
	       COUNT(DISTINCT CASE WHEN o.object_type = 'Account' THEN o.object_key END) AS account_count
	  FROM calls c
	  LEFT JOIN call_context_objects o
	    ON o.call_id = c.call_id
	  LEFT JOIN call_context_fields f
	    ON f.call_id = o.call_id
	   AND f.object_key = o.object_key
	 GROUP BY c.call_id
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
       COALESCE(crm.account_count, 0) AS account_count
	FROM calls c
  LEFT JOIN transcripts t
    ON t.call_id = c.call_id
  LEFT JOIN call_lifecycle l
    ON l.call_id = c.call_id
  LEFT JOIN crm
    ON crm.call_id = c.call_id;
`,
	`
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
       COALESCE(o.object_id, '') AS opportunity_id,
       COALESCE(o.opportunity_stage, '') AS opportunity_stage,
       COALESCE(o.opportunity_type, '') AS opportunity_type,
       COALESCE(o.opportunity_amount, '') AS opportunity_amount,
       COALESCE(o.opportunity_probability, '') AS opportunity_probability,
       COALESCE(o.opportunity_forecast_category, '') AS opportunity_forecast_category,
       COALESCE(o.opportunity_primary_lead_source, '') AS opportunity_primary_lead_source,
       COALESCE(o.opportunity_procurement_system, '') AS opportunity_procurement_system,
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
`,
	`
CREATE TABLE IF NOT EXISTS crm_integrations (
	integration_id TEXT PRIMARY KEY,
	name TEXT NOT NULL DEFAULT '',
	provider TEXT NOT NULL DEFAULT '',
	raw_json BLOB NOT NULL,
	raw_sha256 TEXT NOT NULL,
	first_seen_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_crm_integrations_provider ON crm_integrations(provider);

CREATE TABLE IF NOT EXISTS crm_schema_objects (
	integration_id TEXT NOT NULL,
	object_type TEXT NOT NULL,
	display_name TEXT NOT NULL DEFAULT '',
	field_count INTEGER NOT NULL DEFAULT 0,
	raw_json BLOB NOT NULL,
	raw_sha256 TEXT NOT NULL,
	first_seen_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	PRIMARY KEY(integration_id, object_type)
);

CREATE INDEX IF NOT EXISTS idx_crm_schema_objects_type ON crm_schema_objects(object_type);

CREATE TABLE IF NOT EXISTS crm_schema_fields (
	integration_id TEXT NOT NULL,
	object_type TEXT NOT NULL,
	field_name TEXT NOT NULL,
	field_label TEXT NOT NULL DEFAULT '',
	field_type TEXT NOT NULL DEFAULT '',
	raw_json BLOB NOT NULL,
	raw_sha256 TEXT NOT NULL,
	first_seen_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	PRIMARY KEY(integration_id, object_type, field_name)
);

CREATE INDEX IF NOT EXISTS idx_crm_schema_fields_object ON crm_schema_fields(object_type, field_name);

CREATE TABLE IF NOT EXISTS gong_settings (
	kind TEXT NOT NULL,
	object_id TEXT NOT NULL,
	name TEXT NOT NULL DEFAULT '',
	active INTEGER NOT NULL DEFAULT 0,
	raw_json BLOB NOT NULL,
	raw_sha256 TEXT NOT NULL,
	first_seen_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	PRIMARY KEY(kind, object_id)
);

CREATE INDEX IF NOT EXISTS idx_gong_settings_kind_name ON gong_settings(kind, name);
`,
	`
CREATE TABLE IF NOT EXISTS profile_meta (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL DEFAULT '',
	version INTEGER NOT NULL,
	source_path TEXT NOT NULL DEFAULT '',
	source_sha256 TEXT NOT NULL,
	canonical_sha256 TEXT NOT NULL,
	imported_at TEXT NOT NULL,
	imported_by TEXT NOT NULL DEFAULT '',
	is_active INTEGER NOT NULL DEFAULT 0,
	raw_yaml BLOB NOT NULL,
	canonical_json BLOB NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_profile_meta_canonical_sha
	ON profile_meta(canonical_sha256);

CREATE INDEX IF NOT EXISTS idx_profile_meta_active
	ON profile_meta(is_active);

CREATE TABLE IF NOT EXISTS profile_object_alias (
	profile_id INTEGER NOT NULL,
	concept TEXT NOT NULL,
	object_type TEXT NOT NULL,
	PRIMARY KEY(profile_id, concept, object_type)
);

CREATE TABLE IF NOT EXISTS profile_field_concept (
	profile_id INTEGER NOT NULL,
	concept TEXT NOT NULL,
	object_concept TEXT NOT NULL,
	field_name TEXT NOT NULL,
	confidence REAL NOT NULL DEFAULT 0,
	evidence_json BLOB NOT NULL DEFAULT '{}',
	PRIMARY KEY(profile_id, concept, object_concept, field_name)
);

CREATE TABLE IF NOT EXISTS profile_lifecycle_rule (
	profile_id INTEGER NOT NULL,
	bucket TEXT NOT NULL,
	ordinal INTEGER NOT NULL DEFAULT 0,
	label TEXT NOT NULL DEFAULT '',
	description TEXT NOT NULL DEFAULT '',
	rule_index INTEGER NOT NULL DEFAULT 0,
	rule_json BLOB NOT NULL DEFAULT '{}',
	PRIMARY KEY(profile_id, bucket, rule_index)
);

CREATE TABLE IF NOT EXISTS profile_methodology_concept (
	profile_id INTEGER NOT NULL,
	concept TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	aliases_json BLOB NOT NULL DEFAULT '[]',
	fields_json BLOB NOT NULL DEFAULT '[]',
	tracker_ids_json BLOB NOT NULL DEFAULT '[]',
	scorecard_question_ids_json BLOB NOT NULL DEFAULT '[]',
	PRIMARY KEY(profile_id, concept)
);

CREATE TABLE IF NOT EXISTS profile_validation_warning (
	profile_id INTEGER NOT NULL,
	severity TEXT NOT NULL,
	code TEXT NOT NULL,
	message TEXT NOT NULL,
	path TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_profile_validation_warning_profile
	ON profile_validation_warning(profile_id, severity);
`,
	`
DELETE FROM profile_validation_warning
 WHERE rowid NOT IN (
	SELECT MIN(rowid)
	  FROM profile_validation_warning
	 GROUP BY profile_id, severity, code, path
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_profile_validation_warning_unique
	ON profile_validation_warning(profile_id, severity, code, path);

CREATE TABLE IF NOT EXISTS profile_call_fact_cache_meta (
	profile_id INTEGER PRIMARY KEY,
	canonical_sha256 TEXT NOT NULL,
	data_fingerprint TEXT NOT NULL,
	built_at TEXT NOT NULL,
	call_count INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS profile_call_fact_cache (
	profile_id INTEGER NOT NULL,
	canonical_sha256 TEXT NOT NULL,
	call_id TEXT NOT NULL,
	title TEXT NOT NULL DEFAULT '',
	started_at TEXT NOT NULL DEFAULT '',
	duration_seconds INTEGER NOT NULL DEFAULT 0,
	system TEXT NOT NULL DEFAULT '',
	direction TEXT NOT NULL DEFAULT '',
	scope TEXT NOT NULL DEFAULT '',
	purpose TEXT NOT NULL DEFAULT '',
	calendar_event_present INTEGER NOT NULL DEFAULT 0,
	transcript_present INTEGER NOT NULL DEFAULT 0,
	lifecycle_bucket TEXT NOT NULL DEFAULT 'unknown',
	lifecycle_confidence TEXT NOT NULL DEFAULT 'low',
	lifecycle_reason TEXT NOT NULL DEFAULT '',
	evidence_fields_json BLOB NOT NULL DEFAULT '[]',
	deal_count INTEGER NOT NULL DEFAULT 0,
	account_count INTEGER NOT NULL DEFAULT 0,
	field_values_json BLOB NOT NULL DEFAULT '{}',
	PRIMARY KEY(profile_id, canonical_sha256, call_id)
);

CREATE INDEX IF NOT EXISTS idx_profile_call_fact_cache_bucket
	ON profile_call_fact_cache(profile_id, canonical_sha256, lifecycle_bucket, transcript_present);

CREATE INDEX IF NOT EXISTS idx_profile_call_fact_cache_started
	ON profile_call_fact_cache(profile_id, canonical_sha256, started_at DESC);
`,
}
