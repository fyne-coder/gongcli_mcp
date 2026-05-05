package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
)

const postgresCRMInventoryMigrationSQL = `
CREATE TABLE IF NOT EXISTS crm_integrations (
	integration_id TEXT PRIMARY KEY,
	name TEXT NOT NULL DEFAULT '',
	provider TEXT NOT NULL DEFAULT '',
	raw_json JSONB NOT NULL,
	raw_sha256 TEXT NOT NULL,
	first_seen_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_pg_crm_integrations_provider_name
	ON crm_integrations(provider, name, integration_id);

CREATE TABLE IF NOT EXISTS crm_schema_objects (
	integration_id TEXT NOT NULL,
	object_type TEXT NOT NULL,
	display_name TEXT NOT NULL DEFAULT '',
	field_count BIGINT NOT NULL DEFAULT 0,
	raw_json JSONB NOT NULL,
	raw_sha256 TEXT NOT NULL,
	first_seen_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	PRIMARY KEY(integration_id, object_type)
);

CREATE INDEX IF NOT EXISTS idx_pg_crm_schema_objects_type
	ON crm_schema_objects(object_type);

CREATE TABLE IF NOT EXISTS crm_schema_fields (
	integration_id TEXT NOT NULL,
	object_type TEXT NOT NULL,
	field_name TEXT NOT NULL,
	field_label TEXT NOT NULL DEFAULT '',
	field_type TEXT NOT NULL DEFAULT '',
	raw_json JSONB NOT NULL,
	raw_sha256 TEXT NOT NULL,
	first_seen_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	PRIMARY KEY(integration_id, object_type, field_name)
);

CREATE INDEX IF NOT EXISTS idx_pg_crm_schema_fields_object_name
	ON crm_schema_fields(object_type, field_name);
`

const postgresCRMInventoryReaderGrantStatementsSQL = `
			GRANT SELECT (integration_id, name, provider, first_seen_at, updated_at) ON crm_integrations TO gongmcp_reader;
			GRANT SELECT (integration_id, object_type, display_name, field_count, first_seen_at, updated_at) ON crm_schema_objects TO gongmcp_reader;
			GRANT SELECT (integration_id, object_type, field_name, field_label, field_type, first_seen_at, updated_at) ON crm_schema_fields TO gongmcp_reader;
`

const postgresCRMInventoryReaderGrantsSQL = `
DO $$
BEGIN
	IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'gongmcp_reader') THEN
` + postgresCRMInventoryReaderGrantStatementsSQL + `
	END IF;
END;
$$;
`

const postgresCRMObjectTypeSummaryFunctionSQL = `
CREATE OR REPLACE FUNCTION gongmcp_crm_object_type_summary()
RETURNS TABLE(
	object_type text,
	object_count bigint,
	call_count bigint,
	field_count bigint,
	populated_field_count bigint,
	distinct_object_id_count bigint
)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = public, pg_temp
AS $function$
SELECT o.object_type,
       COUNT(DISTINCT o.id)::bigint AS object_count,
       COUNT(DISTINCT o.call_id)::bigint AS call_count,
       COUNT(f.id)::bigint AS field_count,
       COUNT(CASE WHEN TRIM(f.field_value_text) <> '' THEN 1 END)::bigint AS populated_field_count,
       COUNT(DISTINCT NULLIF(TRIM(o.object_id), ''))::bigint AS distinct_object_id_count
  FROM call_context_objects o
  LEFT JOIN call_context_fields f
    ON f.call_id = o.call_id
   AND f.object_key = o.object_key
 GROUP BY o.object_type
 ORDER BY object_count DESC, o.object_type
$function$;

REVOKE ALL ON FUNCTION gongmcp_crm_object_type_summary() FROM PUBLIC;
`

const postgresTranscriptCRMContextSearchFunctionSQL = `
CREATE OR REPLACE FUNCTION gongmcp_search_transcript_segments_by_crm_context(search_text text, object_type_arg text, object_id_arg text, row_limit integer)
RETURNS TABLE(
	started_at text,
	object_type text,
	matching_object_count bigint,
	segment_index integer,
	start_ms bigint,
	end_ms bigint,
	snippet text
)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = public, pg_temp
AS $function$
WITH input AS (
	SELECT TRIM(COALESCE(search_text, '')) AS search_value,
	       TRIM(COALESCE(object_type_arg, '')) AS object_type_value,
	       TRIM(COALESCE(object_id_arg, '')) AS object_id_value
),
q AS (
	SELECT websearch_to_tsquery('simple', left(search_value, 160)) AS query
	  FROM input
	 WHERE search_value <> ''
	   AND object_type_value <> ''
	   AND length(search_value) <= 160
	   AND search_value ~ '^[[:alnum:][:space:]_''"/.,;:-]+$'
),
bounded AS (
	SELECT LEAST(GREATEST(COALESCE(row_limit, 20), 1), 1000) AS limit_value
),
matching_objects AS (
	SELECT o.call_id,
	       o.object_key,
	       o.object_type
	  FROM call_context_objects
	       o
	  CROSS JOIN input
	 WHERE o.object_type = input.object_type_value
	   AND (input.object_id_value = '' OR o.object_id = input.object_id_value)
),
matched_segments AS (
	SELECT ts.call_id,
	       c.started_at,
	       ts.segment_index,
	       ts.start_ms,
	       ts.end_ms,
	       ts_rank_cd(ts.search_vector, q.query) AS rank
	  FROM transcript_segments ts
	  JOIN calls c
	    ON c.call_id = ts.call_id,
	       q
	WHERE ts.search_vector @@ q.query
	   AND EXISTS (
		SELECT 1
		  FROM matching_objects mo
		 WHERE mo.call_id = ts.call_id
	   )
	   AND NOT EXISTS (
		SELECT 1
		  FROM governance_suppressed_calls suppressed
		 WHERE suppressed.call_id = ts.call_id
	   )
	 ORDER BY rank DESC, c.started_at DESC, ts.call_id, ts.segment_index
	 LIMIT (SELECT limit_value FROM bounded)
)
SELECT m.started_at,
       COALESCE((SELECT mo.object_type
                   FROM matching_objects mo
                  WHERE mo.call_id = m.call_id
                  ORDER BY mo.object_key
                  LIMIT 1), '') AS object_type,
       COALESCE((SELECT COUNT(DISTINCT mo.object_key)
                   FROM matching_objects mo
                  WHERE mo.call_id = m.call_id), 0)::bigint AS matching_object_count,
       m.segment_index,
       m.start_ms,
       m.end_ms,
       COALESCE(NULLIF(ts_headline('simple', ts.text, q.query, 'StartSel=[, StopSel=], MaxWords=12, MinWords=4, ShortWord=2'), ''), LEFT(ts.text, 240)) AS snippet
  FROM matched_segments m
  JOIN transcript_segments ts
    ON ts.call_id = m.call_id
   AND ts.segment_index = m.segment_index,
       q
 ORDER BY m.rank DESC, m.started_at DESC, m.call_id, m.segment_index
$function$;

REVOKE ALL ON FUNCTION gongmcp_search_transcript_segments_by_crm_context(text, text, text, integer) FROM PUBLIC;
`

const postgresMissingTranscriptsFunctionSQL = `
CREATE OR REPLACE FUNCTION gongmcp_missing_transcripts(from_date_arg text, to_date_arg text, lifecycle_bucket_arg text, scope_arg text, system_arg text, direction_arg text, crm_object_type_arg text, crm_object_id_arg text, row_limit integer)
RETURNS TABLE(
	call_id text,
	title text,
	started_at text
)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = public, pg_temp
AS $function$
WITH input AS (
	SELECT TRIM(COALESCE(from_date_arg, '')) AS from_date_value,
	       TRIM(COALESCE(to_date_arg, '')) AS to_date_value,
	       TRIM(COALESCE(lifecycle_bucket_arg, '')) AS lifecycle_bucket_value,
	       TRIM(COALESCE(scope_arg, '')) AS scope_value,
	       TRIM(COALESCE(system_arg, '')) AS system_value,
	       TRIM(COALESCE(direction_arg, '')) AS direction_value,
	       TRIM(COALESCE(crm_object_type_arg, '')) AS crm_object_type_value,
	       TRIM(COALESCE(crm_object_id_arg, '')) AS crm_object_id_value
),
bounded AS (
	SELECT LEAST(GREATEST(COALESCE(row_limit, 100), 1), 10000) AS limit_value
)
SELECT c.call_id,
       c.title,
       c.started_at
  FROM calls c
  CROSS JOIN input
  LEFT JOIN transcripts t
    ON t.call_id = c.call_id
 WHERE t.call_id IS NULL
   AND (
		(input.crm_object_type_value = '' AND input.crm_object_id_value = '')
		OR (input.crm_object_type_value <> '' AND EXISTS (
			SELECT 1
			  FROM call_context_objects o
			 WHERE o.call_id = c.call_id
			   AND o.object_type = input.crm_object_type_value
			   AND (input.crm_object_id_value = '' OR o.object_id = input.crm_object_id_value)
		))
   )
   AND (
		(input.from_date_value = '' AND input.to_date_value = '' AND input.lifecycle_bucket_value = '' AND input.scope_value = '' AND input.system_value = '' AND input.direction_value = '')
		OR EXISTS (
			SELECT 1
			  FROM call_facts cf
			 WHERE cf.call_id = c.call_id
			   AND (input.from_date_value = '' OR cf.call_date >= input.from_date_value)
			   AND (input.to_date_value = '' OR cf.call_date <= input.to_date_value)
			   AND (input.lifecycle_bucket_value = '' OR cf.lifecycle_bucket = input.lifecycle_bucket_value)
			   AND (input.scope_value = '' OR cf.scope = input.scope_value)
			   AND (input.system_value = '' OR cf.system = input.system_value)
			   AND (input.direction_value = '' OR cf.direction = input.direction_value)
		)
   )
 ORDER BY c.started_at DESC, c.call_id
 LIMIT (SELECT limit_value FROM bounded)
$function$;

REVOKE ALL ON FUNCTION gongmcp_missing_transcripts(text, text, text, text, text, text, text, text, integer) FROM PUBLIC;
`

const postgresCRMFieldValueSearchFunctionSQL = `
CREATE OR REPLACE FUNCTION gongmcp_crm_field_value_search(object_type_arg text, field_name_arg text, value_query_arg text, row_limit integer, include_call_ids_arg boolean, include_value_snippets_arg boolean)
RETURNS TABLE(
	call_id text,
	title text,
	started_at text,
	object_type text,
	field_name text,
	field_label text,
	value_snippet text
)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = public, pg_temp
AS $function$
SELECT CASE WHEN COALESCE(include_call_ids_arg, false) THEN c.call_id ELSE ''::text END AS call_id,
       CASE WHEN COALESCE(include_value_snippets_arg, false) THEN c.title ELSE ''::text END AS title,
       c.started_at,
       o.object_type,
       f.field_name,
       f.field_label,
       CASE WHEN COALESCE(include_value_snippets_arg, false) THEN LEFT(TRIM(f.field_value_text), 240) ELSE ''::text END AS value_snippet
  FROM call_context_fields f
  JOIN call_context_objects o
    ON o.call_id = f.call_id
   AND o.object_key = f.object_key
  JOIN calls c
    ON c.call_id = f.call_id
 WHERE o.object_type = object_type_arg
   AND f.field_name = field_name_arg
   AND LOWER(f.field_value_text) LIKE '%' || LOWER(value_query_arg) || '%'
 ORDER BY c.started_at DESC, c.call_id
 LIMIT LEAST(GREATEST(COALESCE(row_limit, 20), 1), 1000)
$function$;

REVOKE ALL ON FUNCTION gongmcp_crm_field_value_search(text, text, text, integer, boolean, boolean) FROM PUBLIC;
`

const postgresUnmappedCRMFieldInventoryFunctionSQL = `
CREATE OR REPLACE FUNCTION gongmcp_unmapped_crm_field_inventory(row_limit integer)
RETURNS TABLE(
	object_type text,
	field_name text,
	field_label text,
	field_type text,
	object_count bigint,
	populated_count bigint,
	distinct_value_count bigint,
	min_value_length bigint,
	max_value_length bigint,
	avg_value_length double precision
)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = public, pg_temp
AS $function$
WITH active_profile AS (
	SELECT id
	  FROM profile_meta
	 WHERE is_active = true
	 ORDER BY id DESC
	 LIMIT 1
),
mapped_fields AS (
	SELECT DISTINCT oa.object_type, fc.field_name
	  FROM active_profile ap
	  JOIN profile_field_concept fc
	    ON fc.profile_id = ap.id
	  JOIN profile_object_alias oa
	    ON oa.profile_id = ap.id
	   AND oa.concept = fc.object_concept
),
candidate_fields AS (
	SELECT o.object_type,
	       f.field_name,
	       f.field_label,
	       f.field_type,
	       o.object_key,
	       f.field_value_text
	  FROM active_profile ap
	  JOIN call_context_fields f
	    ON true
	  JOIN call_context_objects o
	    ON o.call_id = f.call_id
	   AND o.object_key = f.object_key
	 WHERE NOT EXISTS (
		SELECT 1
		  FROM mapped_fields mf
		 WHERE mf.object_type = o.object_type
		   AND mf.field_name = f.field_name
	 )
)
SELECT object_type,
       field_name,
       MAX(field_label) AS field_label,
       MAX(field_type) AS field_type,
       COUNT(DISTINCT object_key) AS object_count,
       COUNT(DISTINCT CASE WHEN TRIM(field_value_text) <> '' THEN object_key END) AS populated_count,
       COUNT(DISTINCT CASE WHEN TRIM(field_value_text) <> '' THEN field_value_text END) AS distinct_value_count,
       COALESCE(MIN(CASE WHEN TRIM(field_value_text) <> '' THEN char_length(field_value_text) END), 0)::bigint AS min_value_length,
       COALESCE(MAX(CASE WHEN TRIM(field_value_text) <> '' THEN char_length(field_value_text) END), 0)::bigint AS max_value_length,
       COALESCE(AVG(CASE WHEN TRIM(field_value_text) <> '' THEN char_length(field_value_text) END), 0)::double precision AS avg_value_length
  FROM candidate_fields
 GROUP BY object_type, field_name
 ORDER BY populated_count DESC, object_type, field_name
 LIMIT LEAST(GREATEST(COALESCE(row_limit, 50), 1), 4000)
$function$;

REVOKE ALL ON FUNCTION gongmcp_unmapped_crm_field_inventory(integer) FROM PUBLIC;
`

const postgresLateStageSignalFunctionsSQL = `
CREATE OR REPLACE FUNCTION gongmcp_late_stage_call_counts(object_type_arg text, stage_field_arg text, late_values_json_arg text)
RETURNS TABLE(late_calls bigint, non_late_calls bigint)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = public, pg_temp
AS $function$
WITH late_values_json AS (
	SELECT COALESCE(NULLIF(late_values_json_arg, ''), '[]') AS raw_json
	 WHERE CHAR_LENGTH(COALESCE(late_values_json_arg, '')) <= 4096
),
late_values AS (
	SELECT LOWER(TRIM(value)) AS value
	  FROM late_values_json,
	       jsonb_array_elements_text(raw_json::jsonb) AS value
	 WHERE TRIM(value) <> ''
	 LIMIT 25
),
classified AS (
	SELECT f.call_id,
	       MAX(CASE WHEN LOWER(TRIM(f.field_value_text)) IN (SELECT value FROM late_values) THEN 1 ELSE 0 END) AS is_late
	  FROM call_context_fields f
	  JOIN call_context_objects o
	    ON o.call_id = f.call_id
	   AND o.object_key = f.object_key
	 WHERE object_type_arg = 'Opportunity'
	   AND stage_field_arg = 'StageName'
	   AND o.object_type = 'Opportunity'
	   AND f.field_name = 'StageName'
	   AND TRIM(f.field_value_text) <> ''
	 GROUP BY f.call_id
)
SELECT COUNT(*) FILTER (WHERE is_late = 1)::bigint AS late_calls,
       COUNT(*) FILTER (WHERE is_late = 0)::bigint AS non_late_calls
  FROM classified
$function$;

REVOKE ALL ON FUNCTION gongmcp_late_stage_call_counts(text, text, text) FROM PUBLIC;

CREATE OR REPLACE FUNCTION gongmcp_late_stage_stage_counts(object_type_arg text, stage_field_arg text, late_values_json_arg text)
RETURNS TABLE(stage_value text, call_count bigint)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = public, pg_temp
AS $function$
WITH stage_rows AS (
	SELECT f.call_id,
	       LOWER(TRIM(f.field_value_text)) AS stage_key,
	       MIN(TRIM(f.field_value_text)) AS stage_value
	  FROM call_context_fields f
	  JOIN call_context_objects o
	    ON o.call_id = f.call_id
	   AND o.object_key = f.object_key
	 WHERE object_type_arg = 'Opportunity'
	   AND stage_field_arg = 'StageName'
	   AND CHAR_LENGTH(COALESCE(late_values_json_arg, '')) <= 4096
	   AND o.object_type = 'Opportunity'
	   AND f.field_name = 'StageName'
	   AND TRIM(f.field_value_text) <> ''
	 GROUP BY f.call_id, LOWER(TRIM(f.field_value_text))
)
SELECT MIN(stage_value) AS stage_value,
       COUNT(DISTINCT call_id)::bigint AS call_count
  FROM stage_rows
 GROUP BY stage_key
 ORDER BY stage_value
$function$;

REVOKE ALL ON FUNCTION gongmcp_late_stage_stage_counts(text, text, text) FROM PUBLIC;

CREATE OR REPLACE FUNCTION gongmcp_late_stage_signal_inventory(object_type_arg text, stage_field_arg text, late_values_json_arg text, row_limit integer, include_stage_proxies_arg boolean)
RETURNS TABLE(field_name text, field_label text, late_populated_calls bigint, non_late_populated_calls bigint)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = public, pg_temp
AS $function$
WITH late_values_json AS (
	SELECT COALESCE(NULLIF(late_values_json_arg, ''), '[]') AS raw_json
	 WHERE CHAR_LENGTH(COALESCE(late_values_json_arg, '')) <= 4096
),
late_values AS (
	SELECT LOWER(TRIM(value)) AS value
	  FROM late_values_json,
	       jsonb_array_elements_text(raw_json::jsonb) AS value
	 WHERE TRIM(value) <> ''
	 LIMIT 25
),
classified AS (
	SELECT f.call_id,
	       MAX(CASE WHEN LOWER(TRIM(f.field_value_text)) IN (SELECT value FROM late_values) THEN 1 ELSE 0 END) AS is_late
	  FROM call_context_fields f
	  JOIN call_context_objects o
	    ON o.call_id = f.call_id
	   AND o.object_key = f.object_key
	 WHERE object_type_arg = 'Opportunity'
	   AND stage_field_arg = 'StageName'
	   AND o.object_type = 'Opportunity'
	   AND f.field_name = 'StageName'
	   AND TRIM(f.field_value_text) <> ''
	 GROUP BY f.call_id
),
field_presence AS (
	SELECT DISTINCT f.call_id,
	       c.is_late,
	       f.field_name,
	       f.field_label
	  FROM call_context_fields f
	  JOIN call_context_objects o
	    ON o.call_id = f.call_id
	   AND o.object_key = f.object_key
	  JOIN classified c
	    ON c.call_id = f.call_id
	 WHERE object_type_arg = 'Opportunity'
	   AND stage_field_arg = 'StageName'
	   AND o.object_type = 'Opportunity'
	   AND TRIM(f.field_value_text) <> ''
	   AND (
		COALESCE(include_stage_proxies_arg, false)
		OR LOWER(f.field_name) NOT IN (
			LOWER(stage_field_arg),
			'probability',
			'forecast_category_vp__c',
			'forecast_category_ae__c',
			'forecastcategory',
			'forecast_category',
			'forecast_category_name',
			'forecast_category_name__c',
			'forecastcategoryname',
			'forecast_category_vp_formula__c',
			'forecast_category_ae_formula__c',
			'forecast_category_formula__c'
		)
	   )
)
SELECT field_name,
       MAX(field_label) AS field_label,
       COUNT(DISTINCT CASE WHEN is_late = 1 THEN call_id END)::bigint AS late_populated_calls,
       COUNT(DISTINCT CASE WHEN is_late = 0 THEN call_id END)::bigint AS non_late_populated_calls
  FROM field_presence
 GROUP BY field_name
$function$;

REVOKE ALL ON FUNCTION gongmcp_late_stage_signal_inventory(text, text, text, integer, boolean) FROM PUBLIC;
`

const postgresOpportunitiesMissingTranscriptsFunctionSQL = `
CREATE OR REPLACE FUNCTION gongmcp_opportunities_missing_transcripts(stage_values_json_arg text, row_limit integer)
RETURNS TABLE(
	stage text,
	call_count bigint,
	missing_transcript_count bigint,
	transcript_count bigint,
	latest_call_at text
)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = public, pg_temp
AS $function$
WITH stage_values_json AS (
	SELECT COALESCE(NULLIF(stage_values_json_arg, ''), '[]') AS raw_json
	 WHERE CHAR_LENGTH(COALESCE(stage_values_json_arg, '')) <= 65536
),
stage_values AS (
	SELECT LOWER(TRIM(value)) AS value
	  FROM stage_values_json,
	       jsonb_array_elements_text(raw_json::jsonb) AS value
	 WHERE TRIM(value) <> ''
	 LIMIT 50
),
opportunities AS (
	SELECT o.call_id,
	       o.object_key,
	       o.object_id
	  FROM call_context_objects o
	 WHERE o.object_type = 'Opportunity'
	   AND TRIM(o.object_id) <> ''
),
stage_fields AS (
	SELECT f.call_id,
	       f.object_key,
	       MAX(TRIM(f.field_value_text)) AS stage
	  FROM call_context_fields f
	 WHERE f.field_name = 'StageName'
	   AND TRIM(f.field_value_text) <> ''
	 GROUP BY f.call_id, f.object_key
),
opportunity_calls AS (
	SELECT o.call_id,
	       o.object_key,
	       o.object_id,
	       COALESCE(sf.stage, '') AS stage,
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
),
filtered_opportunity_calls AS (
	SELECT *
	  FROM opportunity_calls
	 WHERE COALESCE(NULLIF(TRIM(stage_values_json_arg), ''), '[]') = '[]'
	    OR LOWER(TRIM(stage)) IN (SELECT value FROM stage_values)
),
ranked AS (
	SELECT *,
	       ROW_NUMBER() OVER (PARTITION BY object_id ORDER BY started_at DESC, call_id) AS latest_rank
	  FROM filtered_opportunity_calls
),
summaries AS (
	SELECT COALESCE(MAX(stage) FILTER (WHERE latest_rank = 1), '') AS stage,
	       COUNT(DISTINCT call_id)::bigint AS call_count,
	       COUNT(DISTINCT CASE WHEN transcript_call_id IS NULL THEN call_id END)::bigint AS missing_transcript_count,
	       COUNT(DISTINCT CASE WHEN transcript_call_id IS NOT NULL THEN call_id END)::bigint AS transcript_count,
	       COALESCE(MAX(started_at), '') AS latest_call_at
	  FROM ranked
	 GROUP BY object_id
	HAVING COUNT(DISTINCT CASE WHEN transcript_call_id IS NULL THEN call_id END) > 0
)
SELECT stage,
       call_count,
       missing_transcript_count,
       transcript_count,
       latest_call_at
  FROM summaries
 ORDER BY missing_transcript_count DESC, call_count DESC, latest_call_at DESC
 LIMIT LEAST(GREATEST(COALESCE(row_limit, 25), 1), 1000)
$function$;

REVOKE ALL ON FUNCTION gongmcp_opportunities_missing_transcripts(text, integer) FROM PUBLIC;
`

const postgresOpportunityCallSummaryFunctionSQL = `
CREATE OR REPLACE FUNCTION gongmcp_opportunity_call_summary(stage_values_json_arg text, row_limit integer)
RETURNS TABLE(
	stage text,
	call_count bigint,
	transcript_count bigint,
	missing_transcript_count bigint,
	total_duration_seconds bigint,
	latest_call_at text
)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = public, pg_temp
AS $function$
WITH stage_values_json AS (
	SELECT COALESCE(NULLIF(stage_values_json_arg, ''), '[]') AS raw_json
	 WHERE CHAR_LENGTH(COALESCE(stage_values_json_arg, '')) <= 65536
),
stage_values AS (
	SELECT LOWER(TRIM(value)) AS value
	  FROM stage_values_json,
	       jsonb_array_elements_text(raw_json::jsonb) AS value
	 WHERE TRIM(value) <> ''
	 LIMIT 50
),
opportunities AS (
	SELECT o.call_id,
	       o.object_key,
	       o.object_id
	  FROM call_context_objects o
	 WHERE o.object_type = 'Opportunity'
	   AND TRIM(o.object_id) <> ''
),
stage_fields AS (
	SELECT f.call_id,
	       f.object_key,
	       MAX(TRIM(f.field_value_text)) AS stage
	  FROM call_context_fields f
	 WHERE f.field_name = 'StageName'
	   AND TRIM(f.field_value_text) <> ''
	 GROUP BY f.call_id, f.object_key
),
opportunity_calls AS (
	SELECT o.call_id,
	       o.object_key,
	       o.object_id,
	       COALESCE(sf.stage, '') AS stage,
	       c.started_at,
	       c.duration_seconds,
	       t.call_id AS transcript_call_id
	  FROM opportunities o
	  JOIN calls c
	    ON c.call_id = o.call_id
	  LEFT JOIN stage_fields sf
	    ON sf.call_id = o.call_id
	   AND sf.object_key = o.object_key
	  LEFT JOIN transcripts t
	    ON t.call_id = o.call_id
),
filtered_opportunity_calls AS (
	SELECT *
	  FROM opportunity_calls
	 WHERE COALESCE(NULLIF(TRIM(stage_values_json_arg), ''), '[]') = '[]'
	    OR LOWER(TRIM(stage)) IN (SELECT value FROM stage_values)
),
ranked AS (
	SELECT *,
	       ROW_NUMBER() OVER (PARTITION BY object_id ORDER BY started_at DESC, call_id) AS latest_rank
	  FROM filtered_opportunity_calls
),
summaries AS (
	SELECT COALESCE(MAX(stage) FILTER (WHERE latest_rank = 1), '') AS stage,
	       COUNT(DISTINCT call_id)::bigint AS call_count,
	       COUNT(DISTINCT CASE WHEN transcript_call_id IS NOT NULL THEN call_id END)::bigint AS transcript_count,
	       COUNT(DISTINCT CASE WHEN transcript_call_id IS NULL THEN call_id END)::bigint AS missing_transcript_count,
	       COALESCE(SUM(duration_seconds), 0)::bigint AS total_duration_seconds,
	       COALESCE(MAX(started_at), '') AS latest_call_at
	  FROM ranked
	 GROUP BY object_id
)
SELECT stage,
       call_count,
       transcript_count,
       missing_transcript_count,
       total_duration_seconds,
       latest_call_at
  FROM summaries
 ORDER BY call_count DESC, latest_call_at DESC
 LIMIT LEAST(GREATEST(COALESCE(row_limit, 25), 1), 1000)
$function$;

REVOKE ALL ON FUNCTION gongmcp_opportunity_call_summary(text, integer) FROM PUBLIC;
`

const postgresCRMFieldPopulationMatrixFunctionSQL = `
CREATE OR REPLACE FUNCTION gongmcp_crm_field_population_matrix(object_type_arg text, group_by_field_arg text, row_limit integer)
RETURNS TABLE(
	group_value text,
	field_name text,
	field_label text,
	object_count bigint,
	call_count bigint,
	populated_count bigint
)
LANGUAGE plpgsql
STABLE
SECURITY DEFINER
SET search_path = public, pg_temp
AS $function$
DECLARE
	canonical_object_type text := TRIM(object_type_arg);
	canonical_group_by_field text := '';
BEGIN
	IF canonical_object_type = '' THEN
		RAISE EXCEPTION 'object_type is required' USING ERRCODE = '22023';
	END IF;
	CASE LOWER(TRIM(COALESCE(NULLIF(group_by_field_arg, ''), 'StageName')))
		WHEN 'stagename' THEN
			IF canonical_object_type = 'Opportunity' THEN
				canonical_group_by_field := 'StageName';
			END IF;
		WHEN 'forecast_category_vp__c' THEN
			IF canonical_object_type = 'Opportunity' THEN
				canonical_group_by_field := 'Forecast_Category_VP__c';
			END IF;
		WHEN 'forecast_category_ae__c' THEN
			IF canonical_object_type = 'Opportunity' THEN
				canonical_group_by_field := 'Forecast_Category_AE__c';
			END IF;
		WHEN 'industry' THEN
			IF canonical_object_type = 'Account' THEN
				canonical_group_by_field := 'Industry';
			END IF;
		WHEN 'account_type__c' THEN
			IF canonical_object_type = 'Account' THEN
				canonical_group_by_field := 'Account_Type__c';
			END IF;
		WHEN 'revenue_range_f__c' THEN
			IF canonical_object_type = 'Account' THEN
				canonical_group_by_field := 'Revenue_Range_f__c';
			END IF;
		ELSE
			canonical_group_by_field := '';
	END CASE;
	IF canonical_group_by_field = '' THEN
		RAISE EXCEPTION 'object_type % with group_by_field % is not allowed for MCP-safe aggregate grouping', canonical_object_type, COALESCE(NULLIF(TRIM(group_by_field_arg), ''), 'StageName') USING ERRCODE = '22023';
	END IF;

	RETURN QUERY
	WITH
groups AS (
	SELECT o.call_id,
	       o.object_key,
	       MAX(TRIM(g.field_value_text)) AS group_value
	  FROM call_context_objects o
	  JOIN call_context_fields g
	    ON g.call_id = o.call_id
	   AND g.object_key = o.object_key
	   AND g.field_name = canonical_group_by_field
	 WHERE o.object_type = canonical_object_type
	   AND TRIM(g.field_value_text) <> ''
	 GROUP BY o.call_id, o.object_key
),
group_sizes AS (
	SELECT groups.group_value,
	       COUNT(DISTINCT (call_id, object_key))::bigint AS object_count,
	       COUNT(DISTINCT call_id)::bigint AS call_count
	  FROM groups
	 GROUP BY groups.group_value
),
field_counts AS (
	SELECT g.group_value,
	       f.field_name,
	       MAX(f.field_label) AS field_label,
	       COUNT(DISTINCT (g.call_id, g.object_key))::bigint AS object_count,
	       COUNT(DISTINCT g.call_id)::bigint AS call_count,
	       COUNT(DISTINCT (g.call_id, g.object_key)) FILTER (WHERE TRIM(f.field_value_text) <> '') AS populated_count
	  FROM groups g
	  JOIN call_context_fields f
	    ON f.call_id = g.call_id
	   AND f.object_key = g.object_key
	 WHERE f.field_name <> canonical_group_by_field
	 GROUP BY g.group_value, f.field_name
)
SELECT fc.group_value,
       fc.field_name,
       fc.field_label,
       gs.object_count,
       gs.call_count,
       fc.populated_count
  FROM field_counts fc
  JOIN group_sizes gs
    ON gs.group_value = fc.group_value
 ORDER BY fc.populated_count DESC, fc.group_value, fc.field_name
 LIMIT LEAST(GREATEST(COALESCE(row_limit, 50), 1), 1000);
END;
$function$;

REVOKE ALL ON FUNCTION gongmcp_crm_field_population_matrix(text, text, integer) FROM PUBLIC;
`

const postgresLifecycleCRMFieldComparisonFunctionSQL = `
CREATE OR REPLACE FUNCTION gongmcp_compare_lifecycle_crm_fields(bucket_a_arg text, bucket_b_arg text, object_type_arg text, row_limit integer)
RETURNS TABLE(
	object_type text,
	field_name text,
	field_label text,
	bucket_a_call_count bigint,
	bucket_b_call_count bigint,
	bucket_a_populated bigint,
	bucket_b_populated bigint,
	bucket_a_rate double precision,
	bucket_b_rate double precision,
	rate_delta double precision
)
LANGUAGE plpgsql
STABLE
SECURITY DEFINER
SET search_path = public, pg_temp
AS $function$
DECLARE
	canonical_bucket_a text := TRIM(COALESCE(bucket_a_arg, ''));
	canonical_bucket_b text := TRIM(COALESCE(bucket_b_arg, ''));
	canonical_object_type text := TRIM(COALESCE(object_type_arg, ''));
BEGIN
	IF canonical_bucket_a = '' OR canonical_bucket_b = '' THEN
		RAISE EXCEPTION 'bucket_a and bucket_b are required' USING ERRCODE = '22023';
	END IF;
	IF canonical_bucket_a = canonical_bucket_b THEN
		RAISE EXCEPTION 'bucket_a and bucket_b must be different' USING ERRCODE = '22023';
	END IF;
	IF canonical_object_type = '' THEN
		RAISE EXCEPTION 'object_type is required' USING ERRCODE = '22023';
	END IF;
	IF canonical_object_type <> 'Opportunity' THEN
		RAISE EXCEPTION 'object_type % is not allowed for MCP-safe lifecycle CRM field comparison', canonical_object_type USING ERRCODE = '22023';
	END IF;
	IF canonical_bucket_a NOT IN ('outbound_prospecting', 'active_sales_pipeline', 'late_stage_sales', 'closed_won_lost_review', 'renewal', 'upsell_expansion', 'customer_success_account', 'partner', 'unknown') THEN
		RAISE EXCEPTION 'unknown lifecycle bucket %', canonical_bucket_a USING ERRCODE = '22023';
	END IF;
	IF canonical_bucket_b NOT IN ('outbound_prospecting', 'active_sales_pipeline', 'late_stage_sales', 'closed_won_lost_review', 'renewal', 'upsell_expansion', 'customer_success_account', 'partner', 'unknown') THEN
		RAISE EXCEPTION 'unknown lifecycle bucket %', canonical_bucket_b USING ERRCODE = '22023';
	END IF;

	RETURN QUERY
	WITH selected_calls AS (
		SELECT call_id, lifecycle_bucket AS bucket
		  FROM call_facts
		 WHERE lifecycle_bucket IN (canonical_bucket_a, canonical_bucket_b)
		   AND NOT EXISTS (
			SELECT 1
			  FROM governance_suppressed_calls suppressed
			 WHERE suppressed.call_id = call_facts.call_id
		   )
	),
	bucket_totals AS (
		SELECT bucket, COUNT(DISTINCT call_id)::bigint AS call_count
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
		   AND o.object_type = canonical_object_type
	),
	field_counts AS (
		SELECT fp.object_type,
		       fp.field_name,
		       MAX(fp.field_label) AS field_label,
		       COALESCE(MAX(CASE WHEN bt.bucket = canonical_bucket_a THEN bt.call_count END), 0)::bigint AS bucket_a_call_count,
		       COALESCE(MAX(CASE WHEN bt.bucket = canonical_bucket_b THEN bt.call_count END), 0)::bigint AS bucket_b_call_count,
		       COUNT(DISTINCT CASE WHEN fp.bucket = canonical_bucket_a THEN fp.call_id END)::bigint AS bucket_a_populated,
		       COUNT(DISTINCT CASE WHEN fp.bucket = canonical_bucket_b THEN fp.call_id END)::bigint AS bucket_b_populated
		  FROM field_presence fp
		 CROSS JOIN bucket_totals bt
		 GROUP BY fp.object_type, fp.field_name
	),
	rated AS (
		SELECT field_counts.*,
		       CASE WHEN field_counts.bucket_a_call_count <= 0 THEN 0::double precision ELSE field_counts.bucket_a_populated::double precision / field_counts.bucket_a_call_count::double precision END AS bucket_a_rate,
		       CASE WHEN field_counts.bucket_b_call_count <= 0 THEN 0::double precision ELSE field_counts.bucket_b_populated::double precision / field_counts.bucket_b_call_count::double precision END AS bucket_b_rate
		  FROM field_counts
	)
	SELECT rated.object_type,
	       rated.field_name,
	       rated.field_label,
	       rated.bucket_a_call_count,
	       rated.bucket_b_call_count,
	       rated.bucket_a_populated,
	       rated.bucket_b_populated,
	       rated.bucket_a_rate,
	       rated.bucket_b_rate,
	       (rated.bucket_a_rate - rated.bucket_b_rate) AS rate_delta
	  FROM rated
	 ORDER BY abs(rated.bucket_a_rate - rated.bucket_b_rate) DESC,
	          (rated.bucket_a_populated + rated.bucket_b_populated) DESC,
	          rated.field_name
	 LIMIT LEAST(GREATEST(COALESCE(row_limit, 50), 1), 1000);
END;
$function$;

REVOKE ALL ON FUNCTION gongmcp_compare_lifecycle_crm_fields(text, text, text, integer) FROM PUBLIC;
`

type postgresCRMIntegrationPayload struct {
	IntegrationID string
	Name          string
	Provider      string
	RawJSON       []byte
	RawSHA256     string
}

type postgresCRMSchemaFieldPayload struct {
	FieldName  string
	FieldLabel string
	FieldType  string
	RawJSON    []byte
	RawSHA256  string
}

func (s *Store) SearchTranscriptSegmentsByCRMContext(ctx context.Context, params sqlite.TranscriptCRMSearchParams) ([]sqlite.TranscriptCRMSearchResult, error) {
	queryText := strings.TrimSpace(params.Query)
	if queryText == "" {
		return nil, errors.New("search query is required")
	}
	if err := validatePostgresBusinessAnalysisSearchText(queryText, "query"); err != nil {
		return nil, err
	}
	objectType := strings.TrimSpace(params.ObjectType)
	if objectType == "" {
		return nil, errors.New("object type is required")
	}
	objectID := strings.TrimSpace(params.ObjectID)
	limit := boundedLimit(params.Limit, defaultTranscriptSearchLimit, maxTranscriptSearchLimit)

	rows, err := s.db.QueryContext(ctx, `SELECT ''::text AS call_id, ''::text AS title, started_at, object_type, ''::text AS object_id, ''::text AS object_name, matching_object_count, ''::text AS speaker_id, segment_index, start_ms, end_ms, snippet
  FROM gongmcp_search_transcript_segments_by_crm_context($1, $2, $3, $4)`, queryText, objectType, objectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []sqlite.TranscriptCRMSearchResult{}
	for rows.Next() {
		var row sqlite.TranscriptCRMSearchResult
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

func (s *Store) ListCRMObjectTypes(ctx context.Context) ([]sqlite.CRMObjectTypeSummary, error) {
	query := `
SELECT o.object_type,
       COUNT(DISTINCT o.id) AS object_count,
       COUNT(DISTINCT o.call_id) AS call_count,
       COUNT(f.id) AS field_count,
       COUNT(CASE WHEN TRIM(f.field_value_text) <> '' THEN 1 END) AS populated_field_count,
       COUNT(DISTINCT NULLIF(TRIM(o.object_id), '')) AS distinct_object_id_count
  FROM call_context_objects o
  LEFT JOIN call_context_fields f
    ON f.call_id = o.call_id
   AND f.object_key = o.object_key
 GROUP BY o.object_type
 ORDER BY object_count DESC, o.object_type`
	if s.readOnly {
		query = `
SELECT object_type,
       object_count,
       call_count,
       field_count,
       populated_field_count,
       distinct_object_id_count
  FROM gongmcp_crm_object_type_summary()`
	}
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []sqlite.CRMObjectTypeSummary{}
	for rows.Next() {
		var row sqlite.CRMObjectTypeSummary
		if err := rows.Scan(
			&row.ObjectType,
			&row.ObjectCount,
			&row.CallCount,
			&row.FieldCount,
			&row.PopulatedFieldCount,
			&row.DistinctObjectIDCount,
		); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) ListCRMFields(ctx context.Context, objectType string, limit int) ([]sqlite.CRMFieldSummary, error) {
	objectType = strings.TrimSpace(objectType)
	if objectType == "" {
		return nil, errors.New("object type is required")
	}
	limit = boundedLimit(limit, defaultCRMFieldLimit, maxCRMFieldLimit)

	query := `
SELECT object_type,
       field_name,
       field_label,
       row_count,
       call_count,
       populated_count,
       distinct_value_count
  FROM (
	SELECT o.object_type,
	       f.field_name,
	       MAX(f.field_label) AS field_label,
	       COUNT(*) AS row_count,
	       COUNT(DISTINCT f.call_id) AS call_count,
	       COUNT(CASE WHEN f.field_value_text <> '' THEN 1 END) AS populated_count,
	       COUNT(DISTINCT NULLIF(TRIM(f.field_value_text), '')) AS distinct_value_count
	  FROM call_context_fields f
	  JOIN call_context_objects o
	    ON o.call_id = f.call_id
	   AND o.object_key = f.object_key
	 WHERE o.object_type = $1
	 GROUP BY o.object_type, f.field_name
	 ORDER BY COUNT(*) DESC, f.field_name
	 LIMIT $2
  ) fields`
	if s.readOnly {
		query = `
SELECT object_type,
       field_name,
       field_label,
       row_count,
       call_count,
       populated_count,
       distinct_value_count
  FROM (
	SELECT o.object_type,
	       f.field_name,
	       MAX(f.field_label) AS field_label,
	       COUNT(*) AS row_count,
	       COUNT(DISTINCT f.call_id) AS call_count,
	       COUNT(CASE WHEN f.field_populated THEN 1 END) AS populated_count,
	       0::bigint AS distinct_value_count
	  FROM gongmcp_call_context_fields f
	  JOIN gongmcp_call_context_objects o
	    ON o.call_id = f.call_id
	   AND o.object_key = f.object_key
	 WHERE o.object_type = $1
	 GROUP BY o.object_type, f.field_name
	 ORDER BY COUNT(*) DESC, f.field_name
	 LIMIT $2
  ) fields`
	}
	rows, err := s.db.QueryContext(ctx, query, objectType, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []sqlite.CRMFieldSummary{}
	for rows.Next() {
		var row sqlite.CRMFieldSummary
		if err := rows.Scan(
			&row.ObjectType,
			&row.FieldName,
			&row.FieldLabel,
			&row.RowCount,
			&row.CallCount,
			&row.PopulatedCount,
			&row.DistinctValueCount,
		); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) SearchCRMFieldValues(ctx context.Context, params sqlite.CRMFieldValueSearchParams) ([]sqlite.CRMFieldValueMatch, error) {
	objectType := strings.TrimSpace(params.ObjectType)
	fieldName := strings.TrimSpace(params.FieldName)
	valueQuery := strings.TrimSpace(params.ValueQuery)
	if objectType == "" {
		return nil, errors.New("object type is required")
	}
	if fieldName == "" {
		return nil, errors.New("field name is required")
	}
	if valueQuery == "" {
		return nil, errors.New("value query is required")
	}
	limit := boundedLimit(params.Limit, defaultCRMFieldValueLimit, maxCRMFieldValueLimit)

	rows, err := s.db.QueryContext(ctx, `
SELECT call_id,
       title,
       started_at,
       object_type,
       field_name,
       field_label,
       value_snippet
  FROM gongmcp_crm_field_value_search($1, $2, $3, $4, $5, $6)`, objectType, fieldName, valueQuery, limit, params.IncludeCallIDs, params.IncludeValueSnippet)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []sqlite.CRMFieldValueMatch
	for rows.Next() {
		var row sqlite.CRMFieldValueMatch
		if err := rows.Scan(
			&row.CallID,
			&row.Title,
			&row.StartedAt,
			&row.ObjectType,
			&row.FieldName,
			&row.FieldLabel,
			&row.ValueSnippet,
		); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) ListUnmappedCRMFields(ctx context.Context, params sqlite.UnmappedCRMFieldParams) ([]sqlite.UnmappedCRMField, error) {
	_, p, _, err := s.activeProfile(ctx)
	if err != nil {
		return nil, err
	}
	mapped := mappedProfileFields(p)
	limit := boundedLimit(params.Limit, defaultCRMFieldLimit, maxCRMFieldLimit)
	scanLimit := limit * 4
	if scanLimit < limit {
		scanLimit = limit
	}
	if scanLimit > maxCRMFieldLimit*4 {
		scanLimit = maxCRMFieldLimit * 4
	}

	rows, err := s.db.QueryContext(ctx, `
SELECT object_type,
       field_name,
       field_label,
       field_type,
       object_count,
       populated_count,
       distinct_value_count,
       min_value_length,
       max_value_length,
       avg_value_length
  FROM gongmcp_unmapped_crm_field_inventory($1)`, scanLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]sqlite.UnmappedCRMField, 0)
	for rows.Next() {
		var row sqlite.UnmappedCRMField
		if err := rows.Scan(
			&row.ObjectType,
			&row.FieldName,
			&row.FieldLabel,
			&row.FieldType,
			&row.ObjectCount,
			&row.PopulatedCount,
			&row.DistinctValueCount,
			&row.MinValueLength,
			&row.MaxValueLength,
			&row.AvgValueLength,
		); err != nil {
			return nil, err
		}
		if _, ok := mapped[row.ObjectType+"."+row.FieldName]; ok {
			continue
		}
		row.PopulationRate = rate(row.PopulatedCount, row.ObjectCount)
		out = append(out, row)
		if len(out) >= limit {
			break
		}
	}
	return out, rows.Err()
}

func (s *Store) AnalyzeLateStageSignals(ctx context.Context, params sqlite.LateStageSignalParams) (*sqlite.LateStageSignalsReport, error) {
	objectType := strings.TrimSpace(params.ObjectType)
	if objectType == "" {
		objectType = "Opportunity"
	}
	stageField := strings.TrimSpace(params.StageField)
	if stageField == "" {
		stageField = "StageName"
	}
	if objectType != "Opportunity" || stageField != "StageName" {
		return nil, errors.New("postgres late-stage CRM signal analysis supports Opportunity.StageName only")
	}
	lateValues := cleanStringList(params.LateStageValues)
	if len(lateValues) == 0 {
		lateValues = []string{"Demo & Business Case", "Business Case", "SOW & Proposal", "Contract Review", "Contract Signing", "Crucible/Last Mile", "Closed Won"}
	}
	if len(lateValues) > maxLateStageValueCount {
		return nil, fmt.Errorf("late_stage_values supports at most %d entries", maxLateStageValueCount)
	}
	for _, value := range lateValues {
		if len(value) > maxLateStageValueLength {
			return nil, fmt.Errorf("late_stage_values entries must be at most %d bytes", maxLateStageValueLength)
		}
	}
	limit := boundedLimit(params.Limit, defaultLateStageSignalLimit, maxLateStageSignalLimit)
	lateValuesJSON, err := json.Marshal(lateValues)
	if err != nil {
		return nil, err
	}
	lateValuesArg := string(lateValuesJSON)

	var lateCalls int64
	var nonLateCalls int64
	if err := s.db.QueryRowContext(ctx, `
SELECT late_calls,
       non_late_calls
  FROM gongmcp_late_stage_call_counts($1, $2, $3)`, objectType, stageField, lateValuesArg).Scan(&lateCalls, &nonLateCalls); err != nil {
		return nil, err
	}

	stageLabels := make(map[string]string, len(lateValues))
	for _, value := range lateValues {
		clean := strings.TrimSpace(value)
		if clean != "" {
			stageLabels[strings.ToLower(clean)] = clean
		}
	}
	stageCounts := make(map[string]int64)
	stageRows, err := s.db.QueryContext(ctx, `
SELECT stage_value,
       call_count
  FROM gongmcp_late_stage_stage_counts($1, $2, $3)`, objectType, stageField, lateValuesArg)
	if err != nil {
		return nil, err
	}
	defer stageRows.Close()
	for stageRows.Next() {
		var stageValue string
		var callCount int64
		if err := stageRows.Scan(&stageValue, &callCount); err != nil {
			return nil, err
		}
		stageValue = strings.TrimSpace(stageValue)
		if stageValue == "" {
			continue
		}
		stageKey := strings.ToLower(stageValue)
		stageLabel, ok := stageLabels[stageKey]
		if !ok {
			stageLabel = stageValue
			stageLabels[stageKey] = stageValue
		}
		stageCounts[stageLabel] += callCount
	}
	if err := stageRows.Err(); err != nil {
		return nil, err
	}

	totalCalls := lateCalls + nonLateCalls
	if totalCalls == 0 {
		return &sqlite.LateStageSignalsReport{
			ObjectType:      objectType,
			StageField:      stageField,
			LateStageValues: lateValues,
			StageCounts:     stageCounts,
		}, nil
	}

	rows, err := s.db.QueryContext(ctx, `
SELECT field_name,
       field_label,
       late_populated_calls,
       non_late_populated_calls
  FROM gongmcp_late_stage_signal_inventory($1, $2, $3, $4, $5)`, objectType, stageField, lateValuesArg, limit, params.IncludeStageProxies)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	signals := make([]sqlite.LateStageSignal, 0)
	for rows.Next() {
		var row sqlite.LateStageSignal
		if err := rows.Scan(&row.FieldName, &row.FieldLabel, &row.LatePopulatedCalls, &row.NonLatePopulatedCalls); err != nil {
			return nil, err
		}
		row.LateRate = rate(row.LatePopulatedCalls, lateCalls)
		row.NonLateRate = rate(row.NonLatePopulatedCalls, nonLateCalls)
		row.Lift = row.LateRate - row.NonLateRate
		signals = append(signals, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sortLateStageSignals(signals)
	if len(signals) > limit {
		signals = signals[:limit]
	}

	return &sqlite.LateStageSignalsReport{
		ObjectType:      objectType,
		StageField:      stageField,
		LateStageValues: lateValues,
		TotalCalls:      totalCalls,
		LateCalls:       lateCalls,
		NonLateCalls:    nonLateCalls,
		StageCounts:     stageCounts,
		Signals:         signals,
	}, nil
}

func (s *Store) ListOpportunitiesMissingTranscripts(ctx context.Context, params sqlite.OpportunityMissingTranscriptParams) ([]sqlite.OpportunityMissingTranscriptSummary, error) {
	stageValues := cleanStringList(params.StageValues)
	if len(stageValues) > maxOpportunityStageValueCount {
		return nil, fmt.Errorf("stage_values supports at most %d entries", maxOpportunityStageValueCount)
	}
	for _, value := range stageValues {
		if len(value) > maxOpportunityStageValueLength {
			return nil, fmt.Errorf("stage_values entries must be at most %d bytes", maxOpportunityStageValueLength)
		}
	}
	limit := boundedLimit(params.Limit, defaultOpportunitySummaryLimit, maxOpportunitySummaryLimit)
	stageValuesJSON, err := json.Marshal(stageValues)
	if err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx, `
SELECT stage,
       call_count,
       missing_transcript_count,
       transcript_count,
       latest_call_at
  FROM gongmcp_opportunities_missing_transcripts($1, $2)`, string(stageValuesJSON), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]sqlite.OpportunityMissingTranscriptSummary, 0)
	for rows.Next() {
		var row sqlite.OpportunityMissingTranscriptSummary
		if err := rows.Scan(
			&row.Stage,
			&row.CallCount,
			&row.MissingTranscriptCount,
			&row.TranscriptCount,
			&row.LatestCallAt,
		); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) SummarizeOpportunityCalls(ctx context.Context, params sqlite.OpportunityCallSummaryParams) ([]sqlite.OpportunityCallSummary, error) {
	stageValues := cleanStringList(params.StageValues)
	if len(stageValues) > maxOpportunityStageValueCount {
		return nil, fmt.Errorf("stage_values supports at most %d entries", maxOpportunityStageValueCount)
	}
	for _, value := range stageValues {
		if len(value) > maxOpportunityStageValueLength {
			return nil, fmt.Errorf("stage_values entries must be at most %d bytes", maxOpportunityStageValueLength)
		}
	}
	limit := boundedLimit(params.Limit, defaultOpportunitySummaryLimit, maxOpportunitySummaryLimit)
	stageValuesJSON, err := json.Marshal(stageValues)
	if err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx, `
SELECT stage,
       call_count,
       transcript_count,
       missing_transcript_count,
       total_duration_seconds,
       latest_call_at
  FROM gongmcp_opportunity_call_summary($1, $2)`, string(stageValuesJSON), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]sqlite.OpportunityCallSummary, 0)
	for rows.Next() {
		var row sqlite.OpportunityCallSummary
		if err := rows.Scan(
			&row.Stage,
			&row.CallCount,
			&row.TranscriptCount,
			&row.MissingTranscriptCount,
			&row.TotalDurationSeconds,
			&row.LatestCallAt,
		); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) CRMFieldPopulationMatrix(ctx context.Context, params sqlite.CRMFieldPopulationMatrixParams) (*sqlite.CRMFieldPopulationMatrix, error) {
	objectType := strings.TrimSpace(params.ObjectType)
	if objectType == "" {
		return nil, errors.New("object type is required")
	}
	groupByField := strings.TrimSpace(params.GroupByField)
	if groupByField == "" {
		groupByField = "StageName"
	}
	canonicalGroupByField, ok := safePostgresCRMMatrixGroupField(objectType, groupByField)
	if !ok {
		return nil, fmt.Errorf("object_type %q with group_by_field %q is not allowed for MCP-safe aggregate grouping", objectType, groupByField)
	}
	groupByField = canonicalGroupByField
	limit := boundedLimit(params.Limit, defaultCRMMatrixLimit, maxCRMMatrixLimit)

	rows, err := s.db.QueryContext(ctx, `
SELECT group_value,
       field_name,
       field_label,
       object_count,
       call_count,
       populated_count
  FROM gongmcp_crm_field_population_matrix($1, $2, $3)`, objectType, groupByField, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	report := &sqlite.CRMFieldPopulationMatrix{
		ObjectType:   objectType,
		GroupByField: groupByField,
	}
	for rows.Next() {
		var cell sqlite.CRMFieldPopulationCell
		if err := rows.Scan(
			&cell.GroupValue,
			&cell.FieldName,
			&cell.FieldLabel,
			&cell.ObjectCount,
			&cell.CallCount,
			&cell.PopulatedCount,
		); err != nil {
			return nil, err
		}
		cell.PopulationRate = rate(cell.PopulatedCount, cell.ObjectCount)
		report.Cells = append(report.Cells, cell)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return report, nil
}

func (s *Store) CompareLifecycleCRMFields(ctx context.Context, params sqlite.LifecycleCRMFieldComparisonParams) (*sqlite.LifecycleCRMFieldComparison, error) {
	bucketA := strings.TrimSpace(params.BucketA)
	bucketB := strings.TrimSpace(params.BucketB)
	if bucketA == "" || bucketB == "" {
		return nil, errors.New("bucket_a and bucket_b are required")
	}
	if !postgresKnownLifecycleBucket(bucketA) {
		return nil, fmt.Errorf("unknown lifecycle bucket %q", bucketA)
	}
	if !postgresKnownLifecycleBucket(bucketB) {
		return nil, fmt.Errorf("unknown lifecycle bucket %q", bucketB)
	}
	if bucketA == bucketB {
		return nil, errors.New("bucket_a and bucket_b must be different")
	}
	objectType := strings.TrimSpace(params.ObjectType)
	if objectType == "" {
		return nil, errors.New("object_type is required")
	}
	if objectType != "Opportunity" {
		return nil, fmt.Errorf("object_type %q is not allowed for MCP-safe lifecycle CRM field comparison", objectType)
	}
	limit := boundedLimit(params.Limit, defaultLifecycleCRMFieldLimit, maxLifecycleCRMFieldLimit)

	rows, err := s.db.QueryContext(ctx, `SELECT object_type, field_name, field_label, bucket_a_call_count, bucket_b_call_count, bucket_a_populated, bucket_b_populated, bucket_a_rate, bucket_b_rate, rate_delta
  FROM gongmcp_compare_lifecycle_crm_fields($1, $2, $3, $4)`, bucketA, bucketB, objectType, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	report := &sqlite.LifecycleCRMFieldComparison{
		BucketA:    bucketA,
		BucketB:    bucketB,
		ObjectType: objectType,
	}
	for rows.Next() {
		var row sqlite.LifecycleCRMFieldComparisonRow
		if err := rows.Scan(
			&row.ObjectType,
			&row.FieldName,
			&row.FieldLabel,
			&row.BucketACallCount,
			&row.BucketBCallCount,
			&row.BucketAPopulated,
			&row.BucketBPopulated,
			&row.BucketARate,
			&row.BucketBRate,
			&row.RateDelta,
		); err != nil {
			return nil, err
		}
		report.Fields = append(report.Fields, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return report, nil
}

func safePostgresCRMMatrixGroupField(objectType string, fieldName string) (string, bool) {
	switch strings.TrimSpace(objectType) {
	case "Opportunity":
		switch strings.ToLower(strings.TrimSpace(fieldName)) {
		case "stagename":
			return "StageName", true
		case "forecast_category_vp__c":
			return "Forecast_Category_VP__c", true
		case "forecast_category_ae__c":
			return "Forecast_Category_AE__c", true
		}
	case "Account":
		switch strings.ToLower(strings.TrimSpace(fieldName)) {
		case "account_type__c":
			return "Account_Type__c", true
		case "industry":
			return "Industry", true
		case "revenue_range_f__c":
			return "Revenue_Range_f__c", true
		}
	}
	return "", false
}

func (s *Store) UpsertCRMIntegration(ctx context.Context, raw json.RawMessage) (*sqlite.CRMIntegrationRecord, error) {
	if err := s.ensureWritable(); err != nil {
		return nil, err
	}
	payload, err := decodePostgresCRMIntegration(raw)
	if err != nil {
		return nil, err
	}
	now := nowUTC()
	if _, err := s.db.ExecContext(ctx, `INSERT INTO crm_integrations(
	integration_id, name, provider, raw_json, raw_sha256, first_seen_at, updated_at
) VALUES($1, $2, $3, $4::jsonb, $5, $6, $7)
ON CONFLICT(integration_id) DO UPDATE SET
	name = excluded.name,
	provider = excluded.provider,
	raw_json = excluded.raw_json,
	raw_sha256 = excluded.raw_sha256,
	updated_at = excluded.updated_at
WHERE
	crm_integrations.name IS DISTINCT FROM excluded.name OR
	crm_integrations.provider IS DISTINCT FROM excluded.provider OR
	crm_integrations.raw_sha256 IS DISTINCT FROM excluded.raw_sha256`,
		payload.IntegrationID,
		payload.Name,
		payload.Provider,
		string(payload.RawJSON),
		payload.RawSHA256,
		now,
		now,
	); err != nil {
		return nil, err
	}
	return s.crmIntegrationByID(ctx, payload.IntegrationID)
}

func (s *Store) UpsertCRMSchema(ctx context.Context, integrationID string, objectType string, raw json.RawMessage) (int64, error) {
	if err := s.ensureWritable(); err != nil {
		return 0, err
	}
	integrationID = strings.TrimSpace(integrationID)
	objectType = strings.TrimSpace(objectType)
	if integrationID == "" {
		return 0, errors.New("integration id is required")
	}
	if objectType == "" {
		return 0, errors.New("object type is required")
	}

	normalized, err := normalizeJSON(raw)
	if err != nil {
		return 0, err
	}
	var doc map[string]any
	if err := json.Unmarshal(normalized, &doc); err != nil {
		return 0, err
	}
	fields := extractPostgresCRMSchemaFields(doc, objectType)
	displayName := firstString(doc, "displayName", "label", "name")
	if displayName == "" {
		displayName = objectType
	}
	rawSHA256 := sha256Hex(normalized)

	var existingRawSHA256 string
	var existingFieldCount int64
	err = s.db.QueryRowContext(ctx, `
SELECT raw_sha256, field_count
  FROM crm_schema_objects
 WHERE integration_id = $1
   AND object_type = $2`, integrationID, objectType).Scan(&existingRawSHA256, &existingFieldCount)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	if err == nil && existingRawSHA256 == rawSHA256 && existingFieldCount == int64(len(fields)) {
		var actualFieldCount int64
		if err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*)
  FROM crm_schema_fields
 WHERE integration_id = $1
   AND object_type = $2`, integrationID, objectType).Scan(&actualFieldCount); err != nil {
			return 0, err
		}
		if actualFieldCount == int64(len(fields)) {
			return actualFieldCount, nil
		}
	}

	now := nowUTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if err := lockPostgresWriterTx(ctx, tx); err != nil {
		return 0, err
	}

	if _, err := tx.ExecContext(ctx, `INSERT INTO crm_schema_objects(
	integration_id, object_type, display_name, field_count, raw_json, raw_sha256, first_seen_at, updated_at
) VALUES($1, $2, $3, $4, $5::jsonb, $6, $7, $8)
ON CONFLICT(integration_id, object_type) DO UPDATE SET
	display_name = excluded.display_name,
	field_count = excluded.field_count,
	raw_json = excluded.raw_json,
	raw_sha256 = excluded.raw_sha256,
	updated_at = excluded.updated_at
WHERE
	crm_schema_objects.display_name IS DISTINCT FROM excluded.display_name OR
	crm_schema_objects.field_count IS DISTINCT FROM excluded.field_count OR
	crm_schema_objects.raw_sha256 IS DISTINCT FROM excluded.raw_sha256`,
		integrationID,
		objectType,
		displayName,
		int64(len(fields)),
		string(normalized),
		rawSHA256,
		now,
		now,
	); err != nil {
		return 0, err
	}

	for _, field := range fields {
		if _, err := tx.ExecContext(ctx, `INSERT INTO crm_schema_fields(
	integration_id, object_type, field_name, field_label, field_type, raw_json, raw_sha256, first_seen_at, updated_at
) VALUES($1, $2, $3, $4, $5, $6::jsonb, $7, $8, $9)
ON CONFLICT(integration_id, object_type, field_name) DO UPDATE SET
	field_label = excluded.field_label,
	field_type = excluded.field_type,
	raw_json = excluded.raw_json,
	raw_sha256 = excluded.raw_sha256,
	updated_at = excluded.updated_at
WHERE
	crm_schema_fields.field_label IS DISTINCT FROM excluded.field_label OR
	crm_schema_fields.field_type IS DISTINCT FROM excluded.field_type OR
	crm_schema_fields.raw_sha256 IS DISTINCT FROM excluded.raw_sha256`,
			integrationID,
			objectType,
			field.FieldName,
			field.FieldLabel,
			field.FieldType,
			string(field.RawJSON),
			field.RawSHA256,
			now,
			now,
		); err != nil {
			return 0, err
		}
	}
	if len(fields) == 0 {
		if _, err := tx.ExecContext(ctx, `DELETE FROM crm_schema_fields WHERE integration_id = $1 AND object_type = $2`, integrationID, objectType); err != nil {
			return 0, err
		}
	} else {
		args := []any{integrationID, objectType}
		placeholders := make([]string, 0, len(fields))
		for _, field := range fields {
			args = append(args, field.FieldName)
			placeholders = append(placeholders, fmt.Sprintf("$%d", len(args)))
		}
		query := `DELETE FROM crm_schema_fields WHERE integration_id = $1 AND object_type = $2 AND field_name NOT IN (` + strings.Join(placeholders, ", ") + `)`
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return 0, err
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int64(len(fields)), nil
}

func (s *Store) ListCRMIntegrations(ctx context.Context) ([]sqlite.CRMIntegrationRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT integration_id, name, provider, updated_at
  FROM crm_integrations
 ORDER BY provider, name, integration_id
 LIMIT $1`, maxCRMFieldLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []sqlite.CRMIntegrationRecord{}
	for rows.Next() {
		var row sqlite.CRMIntegrationRecord
		if err := rows.Scan(&row.IntegrationID, &row.Name, &row.Provider, &row.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) ListCRMSchemaObjects(ctx context.Context, integrationID string) ([]sqlite.CRMSchemaObjectRecord, error) {
	integrationID = strings.TrimSpace(integrationID)
	query := `SELECT integration_id, object_type, display_name, field_count, updated_at FROM crm_schema_objects`
	args := []any{}
	if integrationID != "" {
		query += ` WHERE integration_id = $1`
		args = append(args, integrationID)
	}
	query += ` ORDER BY integration_id, object_type`
	query += fmt.Sprintf(` LIMIT $%d`, len(args)+1)
	args = append(args, maxCRMFieldLimit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []sqlite.CRMSchemaObjectRecord{}
	for rows.Next() {
		var row sqlite.CRMSchemaObjectRecord
		if err := rows.Scan(&row.IntegrationID, &row.ObjectType, &row.DisplayName, &row.FieldCount, &row.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) ListCRMSchemaFields(ctx context.Context, params sqlite.CRMSchemaFieldListParams) ([]sqlite.CRMSchemaFieldRecord, error) {
	limit := boundedLimit(params.Limit, defaultCRMFieldLimit, maxCRMFieldLimit)
	where := []string{}
	args := []any{}
	if value := strings.TrimSpace(params.IntegrationID); value != "" {
		args = append(args, value)
		where = append(where, fmt.Sprintf(`integration_id = $%d`, len(args)))
	}
	if value := strings.TrimSpace(params.ObjectType); value != "" {
		args = append(args, value)
		where = append(where, fmt.Sprintf(`object_type = $%d`, len(args)))
	}
	query := `SELECT integration_id, object_type, field_name, field_label, field_type, updated_at FROM crm_schema_fields`
	if len(where) > 0 {
		query += ` WHERE ` + strings.Join(where, ` AND `)
	}
	args = append(args, limit)
	query += fmt.Sprintf(` ORDER BY integration_id, object_type, field_name LIMIT $%d`, len(args))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []sqlite.CRMSchemaFieldRecord{}
	for rows.Next() {
		var row sqlite.CRMSchemaFieldRecord
		if err := rows.Scan(&row.IntegrationID, &row.ObjectType, &row.FieldName, &row.FieldLabel, &row.FieldType, &row.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) crmIntegrationByID(ctx context.Context, integrationID string) (*sqlite.CRMIntegrationRecord, error) {
	var row sqlite.CRMIntegrationRecord
	if err := s.db.QueryRowContext(ctx, `
SELECT integration_id, name, provider, updated_at
  FROM crm_integrations
 WHERE integration_id = $1`, integrationID).Scan(
		&row.IntegrationID,
		&row.Name,
		&row.Provider,
		&row.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &row, nil
}

func decodePostgresCRMIntegration(raw json.RawMessage) (*postgresCRMIntegrationPayload, error) {
	normalized, err := normalizeJSON(raw)
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := json.Unmarshal(normalized, &doc); err != nil {
		return nil, err
	}
	integrationID := firstString(doc, "integrationId", "crmIntegrationId", "id")
	if integrationID == "" {
		return nil, errors.New("CRM integration payload missing integration id")
	}
	return &postgresCRMIntegrationPayload{
		IntegrationID: integrationID,
		Name:          firstString(doc, "name", "displayName", "crmName"),
		Provider:      firstString(doc, "provider", "crmType", "type", "integrationType"),
		RawJSON:       normalized,
		RawSHA256:     sha256Hex(normalized),
	}, nil
}

func extractPostgresCRMSchemaFields(doc map[string]any, objectType string) []postgresCRMSchemaFieldPayload {
	if value, ok := lookupPostgresAnyCase(doc, "objectTypeToSelectedFields"); ok {
		if byObject, ok := value.(map[string]any); ok {
			if selected, ok := lookupPostgresAnyCase(byObject, objectType); ok {
				return uniquePostgresCRMSchemaFields(buildPostgresCRMSchemaFields(selected, ""))
			}
		}
	}

	for _, key := range []string{"fields", "selectedFields", "selectedCrmFields", "crmFields"} {
		if value, ok := lookupPostgresAnyCase(doc, key); ok {
			return uniquePostgresCRMSchemaFields(buildPostgresCRMSchemaFields(value, ""))
		}
	}
	return nil
}

func buildPostgresCRMSchemaFields(value any, fallbackName string) []postgresCRMSchemaFieldPayload {
	switch typed := value.(type) {
	case []any:
		rows := make([]postgresCRMSchemaFieldPayload, 0, len(typed))
		for idx, item := range typed {
			itemMap, ok := item.(map[string]any)
			if !ok {
				continue
			}
			rows = append(rows, buildPostgresCRMSchemaField(itemMap, fallbackName, idx))
		}
		return rows
	case map[string]any:
		if fieldName := firstString(typed, "fieldName", "name", "apiName", "id"); fieldName != "" {
			return []postgresCRMSchemaFieldPayload{buildPostgresCRMSchemaField(typed, fallbackName, 0)}
		}
		rows := make([]postgresCRMSchemaFieldPayload, 0, len(typed))
		idx := 0
		for key, item := range typed {
			if itemMap, ok := item.(map[string]any); ok {
				rows = append(rows, buildPostgresCRMSchemaField(itemMap, key, idx))
			} else {
				rawDoc := map[string]any{"name": key, "value": item}
				rows = append(rows, buildPostgresCRMSchemaField(rawDoc, key, idx))
			}
			idx++
		}
		return rows
	default:
		return nil
	}
}

func buildPostgresCRMSchemaField(doc map[string]any, fallbackName string, index int) postgresCRMSchemaFieldPayload {
	fieldName := firstString(doc, "fieldName", "name", "apiName", "id")
	if fieldName == "" {
		fieldName = strings.TrimSpace(fallbackName)
	}
	if fieldName == "" {
		fieldName = fmt.Sprintf("field_%d", index)
	}
	fieldLabel := firstString(doc, "label", "displayName", "fieldLabel")
	if fieldLabel == "" && fieldName != fallbackName {
		fieldLabel = strings.TrimSpace(fallbackName)
	}
	raw, err := normalizeJSONValue(doc)
	if err != nil {
		raw = []byte(`{}`)
	}
	return postgresCRMSchemaFieldPayload{
		FieldName:  fieldName,
		FieldLabel: fieldLabel,
		FieldType:  firstString(doc, "fieldType", "type", "dataType", "valueType"),
		RawJSON:    raw,
		RawSHA256:  sha256Hex(raw),
	}
}

func uniquePostgresCRMSchemaFields(fields []postgresCRMSchemaFieldPayload) []postgresCRMSchemaFieldPayload {
	out := make([]postgresCRMSchemaFieldPayload, 0, len(fields))
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		name := strings.TrimSpace(field.FieldName)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		field.FieldName = name
		out = append(out, field)
	}
	return out
}

func lookupPostgresAnyCase(doc map[string]any, key string) (any, bool) {
	if doc == nil {
		return nil, false
	}
	if value, ok := doc[key]; ok {
		return value, true
	}
	for existing, value := range doc {
		if strings.EqualFold(existing, key) {
			return value, true
		}
	}
	return nil, false
}
