package postgres

import (
	"strings"

	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
)

// postgresLossReasonFieldNamesSQLList renders the shared
// sqlite.LossReasonFieldNames list as a Postgres-quoted CSV that can be
// dropped directly into an `IN (...)` clause. Going through the shared
// list keeps the SQLite dimension SQL, the Postgres bucket function, and
// the read-model has_loss_reason flag in lockstep.
func postgresLossReasonFieldNamesSQLList() string {
	names := sqlite.LossReasonFieldNames
	quoted := make([]string, 0, len(names))
	for _, name := range names {
		quoted = append(quoted, "'"+strings.ReplaceAll(name, "'", "''")+"'")
	}
	return strings.Join(quoted, ", ")
}

// postgresBusinessAnalysisLossReasonBucketFunctionSQL is rendered from the
// shared sqlite.LossReasonBucketWhenClauses helper so the deterministic
// SQLite and Postgres bucket mappings stay in lockstep. Raw loss-reason
// text never escapes this function; callers receive only a normalized
// bucket label or, when raw text is blank but the cached
// loss_reason_present flag is true, the legacy `loss_reason_present`
// fallback for coverage purposes.
var postgresBusinessAnalysisLossReasonBucketFunctionSQL = `
CREATE OR REPLACE FUNCTION gongmcp_business_analysis_loss_reason_bucket(call_id_arg text, fact_loss_reason_present boolean)
RETURNS text
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = public, pg_temp
AS $function$
WITH raw AS (
	SELECT TRIM(f.field_value_text) AS r
	  FROM call_context_fields f
	  JOIN call_context_objects o
	    ON o.call_id = f.call_id
	   AND o.object_key = f.object_key
	 WHERE f.call_id = call_id_arg
	   AND o.object_type = 'Opportunity'
	   AND f.field_name IN (` + postgresLossReasonFieldNamesSQLList() + `)
	   AND TRIM(f.field_value_text) <> ''
	 ORDER BY f.id
	 LIMIT 1
), normalized AS (
	SELECT r,
	       ' ' || REPLACE(REPLACE(REPLACE(REPLACE(REPLACE(REPLACE(REPLACE(LOWER(r), ',', ' '), '/', ' '), '-', ' '), '.', ' '), '_', ' '), E'\t', ' '), E'\n', ' ') || ' ' AS norm
	  FROM raw
)
SELECT COALESCE(
	(SELECT CASE` + sqlite.LossReasonBucketWhenClauses("norm") + `
		ELSE 'unknown_other'
	END FROM normalized),
	CASE WHEN COALESCE(fact_loss_reason_present, false) THEN 'loss_reason_present' ELSE '' END
)
$function$;
REVOKE ALL ON FUNCTION gongmcp_business_analysis_loss_reason_bucket(text, boolean) FROM PUBLIC;
`

// postgresBusinessAnalysisFunctionsSQL is the full set of business-analysis
// SECURITY DEFINER functions. It is composed at package init from a base
// template plus the rendered loss-reason-bucket function so the SQLite and
// Postgres bucket mappings are sourced from the same Go rule set.
var postgresBusinessAnalysisFunctionsSQL = strings.Replace(
	postgresBusinessAnalysisFunctionsTemplateSQL,
	"-- __INSERT_LOSS_REASON_BUCKET_FUNCTION_HERE__",
	postgresBusinessAnalysisLossReasonBucketFunctionSQL,
	1,
)

const postgresBusinessAnalysisFunctionsTemplateSQL = `
CREATE OR REPLACE FUNCTION gongmcp_search_transcript_segments_by_call_facts(search_text text, from_date_arg text, to_date_arg text, lifecycle_bucket_arg text, scope_arg text, system_arg text, direction_arg text, row_limit integer)
RETURNS TABLE(call_id text, started_at text, call_date text, call_month text, duration_seconds bigint, lifecycle_bucket text, scope text, system text, direction text, speaker_id text, segment_index integer, start_ms bigint, end_ms bigint, snippet text, context_excerpt text)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = public, pg_temp
AS $function$
WITH q AS (
	SELECT websearch_to_tsquery('simple', left(search_text, 160)) AS query
),
matched AS (
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
	       ts_headline('simple', ts.text, q.query, 'StartSel=, StopSel=, MaxWords=18, MinWords=4, ShortWord=2') AS snippet,
	       ts_rank_cd(ts.search_vector, q.query) AS rank
	  FROM transcript_segments ts
	  JOIN call_facts cf
	    ON cf.call_id = ts.call_id,
	       q
	 WHERE ts.search_vector @@ q.query
	   AND (from_date_arg = '' OR cf.call_date >= from_date_arg)
	   AND (to_date_arg = '' OR cf.call_date <= to_date_arg)
	   AND (lifecycle_bucket_arg = '' OR cf.lifecycle_bucket = lifecycle_bucket_arg)
	   AND (scope_arg = '' OR cf.scope = scope_arg)
	   AND (system_arg = '' OR cf.system = system_arg)
	   AND (direction_arg = '' OR cf.direction = direction_arg)
	 ORDER BY rank DESC, cf.started_at DESC, ts.call_id, ts.segment_index
	 LIMIT LEAST(GREATEST(COALESCE(row_limit, 20), 1), 1000)
)
SELECT m.call_id,
       m.started_at,
       m.call_date,
       m.call_month,
       m.duration_seconds,
       m.lifecycle_bucket,
       m.scope,
       m.system,
       m.direction,
       m.speaker_id,
       m.segment_index,
       m.start_ms,
       m.end_ms,
       m.snippet,
       left(COALESCE((
	       SELECT string_agg(ctx.text, ' ' ORDER BY ctx.segment_index)
	         FROM transcript_segments ctx
	        WHERE ctx.call_id = m.call_id
	          AND ctx.segment_index BETWEEN m.segment_index - 1 AND m.segment_index + 1
       ), ''), 800) AS context_excerpt
  FROM matched m
 ORDER BY m.rank DESC, m.started_at DESC, m.call_id, m.segment_index
$function$;

CREATE OR REPLACE FUNCTION gongmcp_search_transcript_quotes_with_attribution(search_text text, from_date_arg text, to_date_arg text, lifecycle_bucket_arg text, scope_arg text, system_arg text, direction_arg text, transcript_status_arg text, industry_arg text, account_query_arg text, opportunity_stage_arg text, row_limit integer)
RETURNS TABLE(call_id text, title text, started_at text, call_date text, lifecycle_bucket text, account_name text, account_industry text, account_website text, opportunity_name text, opportunity_stage text, opportunity_type text, opportunity_close_date text, opportunity_probability text, participant_status text, person_title_status text, person_title_source text, segment_index integer, start_ms bigint, end_ms bigint, snippet text, context_excerpt text, speaker_role text, speaker_role_status text)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = public, pg_temp
AS $function$
WITH q AS (
	SELECT websearch_to_tsquery('simple', left(search_text, 160)) AS query
),
matched_segments AS (
	SELECT ts.call_id,
	       c.title,
	       c.started_at,
	       c.parties_count,
	       COALESCE(cf.call_date, left(c.started_at, 10)) AS call_date,
	       COALESCE(cf.lifecycle_bucket, '') AS lifecycle_bucket,
	       ts.speaker_id,
	       ts.segment_index,
	       ts.start_ms,
	       ts.end_ms,
	       ts_headline('simple', ts.text, q.query, 'StartSel=, StopSel=, MaxWords=18, MinWords=4, ShortWord=2') AS snippet,
	       COALESCE((SELECT COUNT(1) FROM jsonb_array_elements(CASE WHEN jsonb_typeof(c.raw_json->'parties') = 'array' THEN c.raw_json->'parties' ELSE '[]'::jsonb END) p WHERE TRIM(COALESCE(p.value->>'title', p.value->>'jobTitle', p.value->>'job_title', '')) <> ''), 0) +
	       COALESCE((SELECT COUNT(1) FROM jsonb_array_elements(CASE WHEN jsonb_typeof(c.raw_json->'metaData'->'parties') = 'array' THEN c.raw_json->'metaData'->'parties' ELSE '[]'::jsonb END) p WHERE TRIM(COALESCE(p.value->>'title', p.value->>'jobTitle', p.value->>'job_title', '')) <> ''), 0) AS party_title_count,
	       ts_rank_cd(ts.search_vector, q.query) AS rank
	  FROM transcript_segments ts
	  JOIN calls c
	    ON c.call_id = ts.call_id
	  LEFT JOIN call_facts cf
	    ON cf.call_id = ts.call_id,
	       q
	 WHERE ts.search_vector @@ q.query
	   AND (from_date_arg = '' OR COALESCE(cf.call_date, left(c.started_at, 10)) >= from_date_arg)
	   AND (to_date_arg = '' OR COALESCE(cf.call_date, left(c.started_at, 10)) <= to_date_arg)
	   AND (lifecycle_bucket_arg = '' OR cf.lifecycle_bucket = lifecycle_bucket_arg)
	   AND (scope_arg = '' OR cf.scope = scope_arg)
	   AND (system_arg = '' OR cf.system = system_arg)
	   AND (direction_arg = '' OR cf.direction = direction_arg)
	   AND (transcript_status_arg = '' OR cf.transcript_status = transcript_status_arg)
	   AND (industry_arg = '' OR EXISTS (
		   SELECT 1
		     FROM call_context_objects filter_o
		     JOIN call_context_fields filter_f
		       ON filter_f.call_id = filter_o.call_id
		      AND filter_f.object_key = filter_o.object_key
		    WHERE filter_o.call_id = ts.call_id
		      AND filter_o.object_type = 'Account'
		      AND filter_f.field_name = 'Industry'
		      AND LOWER(TRIM(filter_f.field_value_text)) LIKE '%' || LOWER(left(industry_arg, 160)) || '%'
	   ))
	   AND (account_query_arg = '' OR EXISTS (
		   SELECT 1
		     FROM call_context_objects filter_o
		     LEFT JOIN call_context_fields filter_f
		       ON filter_f.call_id = filter_o.call_id
		      AND filter_f.object_key = filter_o.object_key
		      AND filter_f.field_name = 'Name'
		    WHERE filter_o.call_id = ts.call_id
		      AND filter_o.object_type = 'Account'
		      AND (
		          LOWER(TRIM(filter_o.object_name)) LIKE '%' || LOWER(left(account_query_arg, 160)) || '%'
		       OR LOWER(TRIM(filter_f.field_value_text)) LIKE '%' || LOWER(left(account_query_arg, 160)) || '%'
		      )
	   ))
	   AND (opportunity_stage_arg = '' OR EXISTS (
		   SELECT 1
		     FROM call_context_objects filter_o
		     JOIN call_context_fields filter_f
		       ON filter_f.call_id = filter_o.call_id
		      AND filter_f.object_key = filter_o.object_key
		    WHERE filter_o.call_id = ts.call_id
		      AND filter_o.object_type = 'Opportunity'
		      AND filter_f.field_name = 'StageName'
		      AND LOWER(TRIM(filter_f.field_value_text)) LIKE '%' || LOWER(left(opportunity_stage_arg, 160)) || '%'
	   ))
	 ORDER BY rank DESC, c.started_at DESC, ts.call_id, ts.segment_index
	 LIMIT LEAST(GREATEST(COALESCE(row_limit, 20), 1), 1000)
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
		 WHERE o.object_type = 'Account'
		   AND (industry_arg = '' OR EXISTS (SELECT 1 FROM call_context_fields selected_f WHERE selected_f.call_id = o.call_id AND selected_f.object_key = o.object_key AND selected_f.field_name = 'Industry' AND LOWER(TRIM(selected_f.field_value_text)) LIKE '%' || LOWER(left(industry_arg, 160)) || '%'))
	  ) selected
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
		 WHERE o.object_type = 'Opportunity'
		   AND (opportunity_stage_arg = '' OR EXISTS (SELECT 1 FROM call_context_fields selected_f WHERE selected_f.call_id = o.call_id AND selected_f.object_key = o.object_key AND selected_f.field_name = 'StageName' AND LOWER(TRIM(selected_f.field_value_text)) LIKE '%' || LOWER(left(opportunity_stage_arg, 160)) || '%'))
	  ) selected
	 WHERE rn = 1
),
party_roles AS (
	SELECT DISTINCT c.call_id,
	       NULLIF(TRIM(BOTH '"' FROM COALESCE(p.value->>'speakerId', p.value->>'speaker_id', p.value->>'id', '')), '') AS speaker_key,
	       CASE
		       WHEN LOWER(TRIM(COALESCE(p.value->>'affiliation', p.value->>'Affiliation', p.value->>'partyType', p.value->>'party_type', ''))) = 'internal' THEN 'internal'
		       WHEN LOWER(TRIM(COALESCE(p.value->>'affiliation', p.value->>'Affiliation', p.value->>'partyType', p.value->>'party_type', ''))) = 'external' THEN 'external'
		       WHEN LOWER(TRIM(COALESCE(p.value->>'isInternal', p.value->>'is_internal', ''))) = 'true' THEN 'internal'
		       WHEN LOWER(TRIM(COALESCE(p.value->>'isInternal', p.value->>'is_internal', ''))) = 'false' THEN 'external'
		       ELSE NULL
	       END AS speaker_role
	  FROM calls c
	  JOIN (SELECT DISTINCT call_id FROM matched_segments) m ON m.call_id = c.call_id,
	       LATERAL (
		       SELECT jsonb_array_elements(CASE WHEN jsonb_typeof(c.raw_json->'parties') = 'array' THEN c.raw_json->'parties' ELSE '[]'::jsonb END) AS value
		       UNION ALL
		       SELECT jsonb_array_elements(CASE WHEN jsonb_typeof(c.raw_json->'metaData'->'parties') = 'array' THEN c.raw_json->'metaData'->'parties' ELSE '[]'::jsonb END) AS value
	       ) AS p
),
speaker_roles AS (
	SELECT m.call_id,
	       m.segment_index,
	       m.speaker_id,
	       COUNT(p.speaker_key) FILTER (WHERE p.speaker_key IS NOT NULL AND p.speaker_key = m.speaker_id) AS match_count,
	       MAX(p.speaker_role) FILTER (WHERE p.speaker_key IS NOT NULL AND p.speaker_key = m.speaker_id AND p.speaker_role IS NOT NULL) AS role_when_present,
	       COUNT(p.speaker_role) FILTER (WHERE p.speaker_key IS NOT NULL AND p.speaker_key = m.speaker_id AND p.speaker_role IS NOT NULL) AS role_count,
	       COUNT(DISTINCT p.speaker_role) FILTER (WHERE p.speaker_key IS NOT NULL AND p.speaker_key = m.speaker_id AND p.speaker_role IS NOT NULL) AS role_distinct_count
	  FROM matched_segments m
	  LEFT JOIN party_roles p ON p.call_id = m.call_id
	 GROUP BY m.call_id, m.segment_index, m.speaker_id
)
SELECT m.call_id,
       m.title,
       m.started_at,
       m.call_date,
       m.lifecycle_bucket,
	       ''::text AS account_name,
	       COALESCE((SELECT TRIM(f.field_value_text) FROM call_context_fields f JOIN selected_account sa ON sa.call_id = f.call_id AND sa.object_key = f.object_key WHERE f.call_id = m.call_id AND f.field_name = 'Industry' LIMIT 1), '') AS account_industry,
	       ''::text AS account_website,
	       ''::text AS opportunity_name,
	       COALESCE((SELECT TRIM(f.field_value_text) FROM call_context_fields f JOIN selected_opportunity so ON so.call_id = f.call_id AND so.object_key = f.object_key WHERE f.call_id = m.call_id AND f.field_name = 'StageName' LIMIT 1), '') AS opportunity_stage,
	       COALESCE((SELECT TRIM(f.field_value_text) FROM call_context_fields f JOIN selected_opportunity so ON so.call_id = f.call_id AND so.object_key = f.object_key WHERE f.call_id = m.call_id AND f.field_name = 'Type' LIMIT 1), '') AS opportunity_type,
	       ''::text AS opportunity_close_date,
	       ''::text AS opportunity_probability,
       CASE WHEN m.parties_count > 0 THEN 'present' ELSE 'missing_from_cache' END AS participant_status,
       CASE
	       WHEN m.party_title_count > 0 THEN 'available'
	       WHEN EXISTS (SELECT 1 FROM call_context_objects po WHERE po.call_id = m.call_id AND po.object_type IN ('Contact', 'Lead')) THEN 'contact_or_lead_present_title_unverified'
	       WHEN m.parties_count > 0 THEN 'participants_present_check_party_titles'
	       ELSE 'missing_from_cache'
       END AS person_title_status,
       CASE
	       WHEN m.party_title_count > 0 THEN 'call_parties'
	       WHEN EXISTS (SELECT 1 FROM call_context_objects po WHERE po.call_id = m.call_id AND po.object_type IN ('Contact', 'Lead')) THEN 'contact_or_lead_object'
	       ELSE ''
       END AS person_title_source,
       m.segment_index,
       m.start_ms,
       m.end_ms,
       m.snippet,
       left(COALESCE((SELECT string_agg(ctx.text, ' ' ORDER BY ctx.segment_index) FROM transcript_segments ctx WHERE ctx.call_id = m.call_id AND ctx.segment_index BETWEEN m.segment_index - 1 AND m.segment_index + 1), ''), 800) AS context_excerpt,
       CASE
	       WHEN COALESCE(NULLIF(TRIM(m.speaker_id), ''), '') = '' THEN 'unknown'
	       WHEN sr.match_count IS NULL OR sr.match_count = 0 THEN 'unknown'
	       WHEN sr.match_count > 1 AND (sr.role_count = 0 OR sr.role_distinct_count <> 1) THEN 'unknown'
	       WHEN sr.role_count > 0 THEN sr.role_when_present
	       ELSE 'unknown'
       END AS speaker_role,
       CASE
	       WHEN COALESCE(NULLIF(TRIM(m.speaker_id), ''), '') = '' THEN 'speaker_unmatched'
	       WHEN sr.match_count IS NULL OR sr.match_count = 0 THEN 'speaker_unmatched'
	       WHEN sr.match_count > 1 THEN 'speaker_ambiguous'
	       WHEN sr.role_count > 0 THEN 'available'
	       ELSE 'affiliation_missing'
       END AS speaker_role_status
  FROM matched_segments m
  LEFT JOIN speaker_roles sr ON sr.call_id = m.call_id AND sr.segment_index = m.segment_index AND sr.speaker_id = m.speaker_id
 ORDER BY m.rank DESC, m.started_at DESC, m.call_id, m.segment_index
$function$;

CREATE OR REPLACE FUNCTION gongmcp_business_analysis_calls(title_query_arg text, transcript_query_arg text, from_date_arg text, to_date_arg text, lifecycle_bucket_arg text, scope_arg text, system_arg text, direction_arg text, transcript_status_arg text, industry_arg text, account_query_arg text, opportunity_stage_arg text, crm_object_type_arg text, crm_object_id_arg text, participant_title_query_arg text, exclude_lifecycle_buckets_json text, exclude_likely_voicemail_arg boolean, row_limit integer)
RETURNS TABLE(call_id text, title text, started_at text, call_date text, call_month text, duration_seconds bigint, lifecycle_bucket text, likely_voicemail_or_ivr boolean, scope text, system text, direction text, transcript_status text, account_industry text, opportunity_stage text, opportunity_type text, forecast_category text, opportunity_count bigint, account_count bigint, participant_status text, person_title_status text, person_title_source text)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = public, pg_temp
AS $function$
WITH filtered AS (
	SELECT cf.*,
	       c.parties_count,
	       COALESCE((SELECT COUNT(1) FROM jsonb_array_elements(CASE WHEN jsonb_typeof(c.raw_json->'parties') = 'array' THEN c.raw_json->'parties' ELSE '[]'::jsonb END) p WHERE TRIM(COALESCE(p.value->>'title', p.value->>'jobTitle', p.value->>'job_title', '')) <> ''), 0) +
	       COALESCE((SELECT COUNT(1) FROM jsonb_array_elements(CASE WHEN jsonb_typeof(c.raw_json->'metaData'->'parties') = 'array' THEN c.raw_json->'metaData'->'parties' ELSE '[]'::jsonb END) p WHERE TRIM(COALESCE(p.value->>'title', p.value->>'jobTitle', p.value->>'job_title', '')) <> ''), 0) AS party_title_count
	  FROM call_facts cf
	  JOIN calls c
	    ON c.call_id = cf.call_id
	 WHERE (title_query_arg = '' OR LOWER(cf.title) LIKE '%' || LOWER(left(title_query_arg, 160)) || '%')
	   AND (transcript_query_arg = '' OR EXISTS (SELECT 1 FROM transcript_segments qts WHERE qts.call_id = cf.call_id AND qts.search_vector @@ websearch_to_tsquery('simple', left(transcript_query_arg, 160))))
	   AND (from_date_arg = '' OR cf.call_date >= from_date_arg)
	   AND (to_date_arg = '' OR cf.call_date <= to_date_arg)
	   AND (lifecycle_bucket_arg = '' OR cf.lifecycle_bucket = lifecycle_bucket_arg)
	   AND (COALESCE(NULLIF(exclude_lifecycle_buckets_json, ''), '[]') = '[]' OR NOT EXISTS (SELECT 1 FROM jsonb_array_elements_text(COALESCE(NULLIF(exclude_lifecycle_buckets_json, ''), '[]')::jsonb) excluded(value) WHERE LOWER(TRIM(excluded.value)) = cf.lifecycle_bucket))
	   AND (NOT COALESCE(exclude_likely_voicemail_arg, false) OR NOT cf.likely_voicemail_or_ivr)
	   AND (scope_arg = '' OR cf.scope = scope_arg)
	   AND (system_arg = '' OR cf.system = system_arg)
	   AND (direction_arg = '' OR cf.direction = direction_arg)
	   AND (transcript_status_arg = '' OR cf.transcript_status = transcript_status_arg)
	   AND (industry_arg = '' OR LOWER(cf.account_industry) LIKE '%' || LOWER(left(industry_arg, 160)) || '%')
	   AND (account_query_arg = '' OR EXISTS (
		   SELECT 1
		     FROM call_context_objects filter_o
		     LEFT JOIN call_context_fields filter_f
		       ON filter_f.call_id = filter_o.call_id
		      AND filter_f.object_key = filter_o.object_key
		      AND filter_f.field_name = 'Name'
		    WHERE filter_o.call_id = cf.call_id
		      AND filter_o.object_type = 'Account'
		      AND (
		          LOWER(TRIM(filter_o.object_name)) LIKE '%' || LOWER(left(account_query_arg, 160)) || '%'
		       OR LOWER(TRIM(filter_f.field_value_text)) LIKE '%' || LOWER(left(account_query_arg, 160)) || '%'
		      )
	   ))
	   AND (opportunity_stage_arg = '' OR LOWER(cf.opportunity_stage) LIKE '%' || LOWER(left(opportunity_stage_arg, 160)) || '%')
	   AND ((crm_object_type_arg = '' AND crm_object_id_arg = '') OR EXISTS (SELECT 1 FROM call_context_objects filter_o WHERE filter_o.call_id = cf.call_id AND (crm_object_type_arg = '' OR filter_o.object_type = crm_object_type_arg) AND (crm_object_id_arg = '' OR filter_o.object_id = crm_object_id_arg)))
	   AND (participant_title_query_arg = '' OR
		       EXISTS (SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(c.raw_json->'parties') = 'array' THEN c.raw_json->'parties' ELSE '[]'::jsonb END) p WHERE LOWER(TRIM(COALESCE(p.value->>'title', p.value->>'jobTitle', p.value->>'job_title', ''))) LIKE '%' || LOWER(left(participant_title_query_arg, 160)) || '%') OR
		       EXISTS (SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(c.raw_json->'metaData'->'parties') = 'array' THEN c.raw_json->'metaData'->'parties' ELSE '[]'::jsonb END) p WHERE LOWER(TRIM(COALESCE(p.value->>'title', p.value->>'jobTitle', p.value->>'job_title', ''))) LIKE '%' || LOWER(left(participant_title_query_arg, 160)) || '%') OR
		       EXISTS (SELECT 1 FROM call_context_objects po JOIN call_context_fields pf ON pf.call_id = po.call_id AND pf.object_key = po.object_key WHERE po.call_id = cf.call_id AND po.object_type IN ('Contact', 'Lead') AND pf.field_name IN ('Title', 'JobTitle', 'Job_Title__c', 'JobTitle__c') AND LOWER(TRIM(pf.field_value_text)) LIKE '%' || LOWER(left(participant_title_query_arg, 160)) || '%'))
)
SELECT call_id,
       title,
       started_at,
       call_date,
       call_month,
       duration_seconds,
       lifecycle_bucket,
       likely_voicemail_or_ivr,
       scope,
       system,
       direction,
       transcript_status,
       account_industry,
       opportunity_stage,
       opportunity_type,
       opportunity_forecast_category AS forecast_category,
       opportunity_count,
       account_count,
       CASE WHEN parties_count > 0 THEN 'present' ELSE 'missing_from_cache' END AS participant_status,
       CASE
	       WHEN party_title_count > 0 THEN 'available'
	       WHEN EXISTS (SELECT 1 FROM call_context_objects po WHERE po.call_id = filtered.call_id AND po.object_type IN ('Contact', 'Lead')) THEN 'contact_or_lead_present_title_unverified'
	       WHEN parties_count > 0 THEN 'participants_present_check_party_titles'
	       ELSE 'missing_from_cache'
       END AS person_title_status,
       CASE
	       WHEN party_title_count > 0 THEN 'call_parties'
	       WHEN EXISTS (SELECT 1 FROM call_context_objects po WHERE po.call_id = filtered.call_id AND po.object_type IN ('Contact', 'Lead')) THEN 'contact_or_lead_object'
	       ELSE ''
       END AS person_title_source
  FROM filtered
 ORDER BY started_at DESC, call_id
 LIMIT LEAST(GREATEST(COALESCE(row_limit, 25), 1), 1000)
$function$;

CREATE OR REPLACE FUNCTION gongmcp_business_analysis_summary(title_query_arg text, transcript_query_arg text, from_date_arg text, to_date_arg text, lifecycle_bucket_arg text, scope_arg text, system_arg text, direction_arg text, transcript_status_arg text, industry_arg text, account_query_arg text, opportunity_stage_arg text, crm_object_type_arg text, crm_object_id_arg text, participant_title_query_arg text, exclude_lifecycle_buckets_json text, exclude_likely_voicemail_arg boolean)
RETURNS TABLE(call_count bigint, transcript_count bigint, missing_transcript_count bigint, account_industry_count bigint, opportunity_stage_count bigint, opportunity_call_count bigint, account_call_count bigint, external_call_count bigint, participant_call_count bigint, participant_title_call_count bigint, earliest_call_at text, latest_call_at text, total_duration_seconds bigint, average_duration_seconds double precision)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = public, pg_temp
AS $function$
WITH rows AS (
	SELECT cf.*,
	       c.parties_count,
	       COALESCE((SELECT COUNT(1) FROM jsonb_array_elements(CASE WHEN jsonb_typeof(c.raw_json->'parties') = 'array' THEN c.raw_json->'parties' ELSE '[]'::jsonb END) p WHERE TRIM(COALESCE(p.value->>'title', p.value->>'jobTitle', p.value->>'job_title', '')) <> ''), 0) +
	       COALESCE((SELECT COUNT(1) FROM jsonb_array_elements(CASE WHEN jsonb_typeof(c.raw_json->'metaData'->'parties') = 'array' THEN c.raw_json->'metaData'->'parties' ELSE '[]'::jsonb END) p WHERE TRIM(COALESCE(p.value->>'title', p.value->>'jobTitle', p.value->>'job_title', '')) <> ''), 0) AS party_title_count
	  FROM call_facts cf
	  JOIN calls c
	    ON c.call_id = cf.call_id
	 WHERE (title_query_arg = '' OR LOWER(cf.title) LIKE '%' || LOWER(left(title_query_arg, 160)) || '%')
	   AND (transcript_query_arg = '' OR EXISTS (SELECT 1 FROM transcript_segments qts WHERE qts.call_id = cf.call_id AND qts.search_vector @@ websearch_to_tsquery('simple', left(transcript_query_arg, 160))))
	   AND (from_date_arg = '' OR cf.call_date >= from_date_arg)
	   AND (to_date_arg = '' OR cf.call_date <= to_date_arg)
	   AND (lifecycle_bucket_arg = '' OR cf.lifecycle_bucket = lifecycle_bucket_arg)
	   AND (COALESCE(NULLIF(exclude_lifecycle_buckets_json, ''), '[]') = '[]' OR NOT EXISTS (SELECT 1 FROM jsonb_array_elements_text(COALESCE(NULLIF(exclude_lifecycle_buckets_json, ''), '[]')::jsonb) excluded(value) WHERE LOWER(TRIM(excluded.value)) = cf.lifecycle_bucket))
	   AND (NOT COALESCE(exclude_likely_voicemail_arg, false) OR NOT cf.likely_voicemail_or_ivr)
	   AND (scope_arg = '' OR cf.scope = scope_arg)
	   AND (system_arg = '' OR cf.system = system_arg)
	   AND (direction_arg = '' OR cf.direction = direction_arg)
	   AND (transcript_status_arg = '' OR cf.transcript_status = transcript_status_arg)
	   AND (industry_arg = '' OR LOWER(cf.account_industry) LIKE '%' || LOWER(left(industry_arg, 160)) || '%')
	   AND (account_query_arg = '' OR EXISTS (
		   SELECT 1
		     FROM call_context_objects filter_o
		     LEFT JOIN call_context_fields filter_f
		       ON filter_f.call_id = filter_o.call_id
		      AND filter_f.object_key = filter_o.object_key
		      AND filter_f.field_name = 'Name'
		    WHERE filter_o.call_id = cf.call_id
		      AND filter_o.object_type = 'Account'
		      AND (
		          LOWER(TRIM(filter_o.object_name)) LIKE '%' || LOWER(left(account_query_arg, 160)) || '%'
		       OR LOWER(TRIM(filter_f.field_value_text)) LIKE '%' || LOWER(left(account_query_arg, 160)) || '%'
		      )
	   ))
	   AND (opportunity_stage_arg = '' OR LOWER(cf.opportunity_stage) LIKE '%' || LOWER(left(opportunity_stage_arg, 160)) || '%')
	   AND ((crm_object_type_arg = '' AND crm_object_id_arg = '') OR EXISTS (SELECT 1 FROM call_context_objects filter_o WHERE filter_o.call_id = cf.call_id AND (crm_object_type_arg = '' OR filter_o.object_type = crm_object_type_arg) AND (crm_object_id_arg = '' OR filter_o.object_id = crm_object_id_arg)))
	   AND (participant_title_query_arg = '' OR
		       EXISTS (SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(c.raw_json->'parties') = 'array' THEN c.raw_json->'parties' ELSE '[]'::jsonb END) p WHERE LOWER(TRIM(COALESCE(p.value->>'title', p.value->>'jobTitle', p.value->>'job_title', ''))) LIKE '%' || LOWER(left(participant_title_query_arg, 160)) || '%') OR
		       EXISTS (SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(c.raw_json->'metaData'->'parties') = 'array' THEN c.raw_json->'metaData'->'parties' ELSE '[]'::jsonb END) p WHERE LOWER(TRIM(COALESCE(p.value->>'title', p.value->>'jobTitle', p.value->>'job_title', ''))) LIKE '%' || LOWER(left(participant_title_query_arg, 160)) || '%') OR
		       EXISTS (SELECT 1 FROM call_context_objects po JOIN call_context_fields pf ON pf.call_id = po.call_id AND pf.object_key = po.object_key WHERE po.call_id = cf.call_id AND po.object_type IN ('Contact', 'Lead') AND pf.field_name IN ('Title', 'JobTitle', 'Job_Title__c', 'JobTitle__c') AND LOWER(TRIM(pf.field_value_text)) LIKE '%' || LOWER(left(participant_title_query_arg, 160)) || '%'))
)
SELECT COUNT(*) AS call_count,
       COALESCE(SUM(CASE WHEN transcript_status = 'present' THEN 1 ELSE 0 END), 0) AS transcript_count,
       COALESCE(SUM(CASE WHEN transcript_status = 'missing' THEN 1 ELSE 0 END), 0) AS missing_transcript_count,
       COALESCE(SUM(CASE WHEN TRIM(account_industry) <> '' THEN 1 ELSE 0 END), 0) AS account_industry_count,
       COALESCE(SUM(CASE WHEN TRIM(opportunity_stage) <> '' THEN 1 ELSE 0 END), 0) AS opportunity_stage_count,
       COALESCE(SUM(CASE WHEN opportunity_count > 0 THEN 1 ELSE 0 END), 0) AS opportunity_call_count,
       COALESCE(SUM(CASE WHEN account_count > 0 THEN 1 ELSE 0 END), 0) AS account_call_count,
       COALESCE(SUM(CASE WHEN scope = 'External' THEN 1 ELSE 0 END), 0) AS external_call_count,
       COALESCE(SUM(CASE WHEN parties_count > 0 THEN 1 ELSE 0 END), 0) AS participant_call_count,
       COALESCE(SUM(CASE WHEN party_title_count > 0 THEN 1 ELSE 0 END), 0) AS participant_title_call_count,
       COALESCE(MIN(started_at), '') AS earliest_call_at,
       COALESCE(MAX(started_at), '') AS latest_call_at,
       COALESCE(SUM(duration_seconds), 0) AS total_duration_seconds,
       COALESCE(AVG(duration_seconds), 0) AS average_duration_seconds
  FROM rows
$function$;

CREATE OR REPLACE FUNCTION gongmcp_business_analysis_evidence(search_text text, title_query_arg text, transcript_query_arg text, from_date_arg text, to_date_arg text, lifecycle_bucket_arg text, scope_arg text, system_arg text, direction_arg text, transcript_status_arg text, industry_arg text, account_query_arg text, opportunity_stage_arg text, crm_object_type_arg text, crm_object_id_arg text, participant_title_query_arg text, exclude_lifecycle_buckets_json text, exclude_likely_voicemail_arg boolean, row_limit integer)
RETURNS TABLE(call_id text, title text, started_at text, call_date text, call_month text, lifecycle_bucket text, account_industry text, account_name text, opportunity_name text, opportunity_stage text, opportunity_type text, opportunity_probability text, opportunity_close_date text, participant_status text, person_title_status text, person_title_source text, segment_index integer, start_ms bigint, end_ms bigint, snippet text, context_excerpt text, speaker_role text, speaker_role_status text)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = public, pg_temp
AS $function$
WITH q AS (
	SELECT websearch_to_tsquery('simple', left(search_text, 160)) AS query
),
matched AS (
	SELECT cf.call_id,
	       cf.title,
	       cf.started_at,
	       cf.call_date,
	       cf.call_month,
	       cf.lifecycle_bucket,
	       cf.account_industry,
	       cf.opportunity_stage,
	       cf.opportunity_type,
	       c.parties_count,
	       ts.speaker_id,
	       ts.segment_index,
	       ts.start_ms,
	       ts.end_ms,
	       ts_headline('simple', ts.text, q.query, 'StartSel=, StopSel=, MaxWords=18, MinWords=4, ShortWord=2') AS snippet,
	       COALESCE((SELECT COUNT(1) FROM jsonb_array_elements(CASE WHEN jsonb_typeof(c.raw_json->'parties') = 'array' THEN c.raw_json->'parties' ELSE '[]'::jsonb END) p WHERE TRIM(COALESCE(p.value->>'title', p.value->>'jobTitle', p.value->>'job_title', '')) <> ''), 0) +
	       COALESCE((SELECT COUNT(1) FROM jsonb_array_elements(CASE WHEN jsonb_typeof(c.raw_json->'metaData'->'parties') = 'array' THEN c.raw_json->'metaData'->'parties' ELSE '[]'::jsonb END) p WHERE TRIM(COALESCE(p.value->>'title', p.value->>'jobTitle', p.value->>'job_title', '')) <> ''), 0) AS party_title_count,
	       ts_rank_cd(ts.search_vector, q.query) AS rank
	  FROM transcript_segments ts
	  JOIN call_facts cf
	    ON cf.call_id = ts.call_id
	  JOIN calls c
	    ON c.call_id = ts.call_id,
	       q
	 WHERE ts.search_vector @@ q.query
	   AND (title_query_arg = '' OR LOWER(cf.title) LIKE '%' || LOWER(left(title_query_arg, 160)) || '%')
	   AND (transcript_query_arg = '' OR EXISTS (SELECT 1 FROM transcript_segments qts WHERE qts.call_id = cf.call_id AND qts.search_vector @@ websearch_to_tsquery('simple', left(transcript_query_arg, 160))))
	   AND (from_date_arg = '' OR cf.call_date >= from_date_arg)
	   AND (to_date_arg = '' OR cf.call_date <= to_date_arg)
	   AND (lifecycle_bucket_arg = '' OR cf.lifecycle_bucket = lifecycle_bucket_arg)
	   AND (COALESCE(NULLIF(exclude_lifecycle_buckets_json, ''), '[]') = '[]' OR NOT EXISTS (SELECT 1 FROM jsonb_array_elements_text(COALESCE(NULLIF(exclude_lifecycle_buckets_json, ''), '[]')::jsonb) excluded(value) WHERE LOWER(TRIM(excluded.value)) = cf.lifecycle_bucket))
	   AND (NOT COALESCE(exclude_likely_voicemail_arg, false) OR NOT cf.likely_voicemail_or_ivr)
	   AND (scope_arg = '' OR cf.scope = scope_arg)
	   AND (system_arg = '' OR cf.system = system_arg)
	   AND (direction_arg = '' OR cf.direction = direction_arg)
	   AND (transcript_status_arg = '' OR cf.transcript_status = transcript_status_arg)
	   AND (industry_arg = '' OR LOWER(cf.account_industry) LIKE '%' || LOWER(left(industry_arg, 160)) || '%')
	   AND (account_query_arg = '' OR EXISTS (
		   SELECT 1
		     FROM call_context_objects filter_o
		     LEFT JOIN call_context_fields filter_f
		       ON filter_f.call_id = filter_o.call_id
		      AND filter_f.object_key = filter_o.object_key
		      AND filter_f.field_name = 'Name'
		    WHERE filter_o.call_id = cf.call_id
		      AND filter_o.object_type = 'Account'
		      AND (
		          LOWER(TRIM(filter_o.object_name)) LIKE '%' || LOWER(left(account_query_arg, 160)) || '%'
		       OR LOWER(TRIM(filter_f.field_value_text)) LIKE '%' || LOWER(left(account_query_arg, 160)) || '%'
		      )
	   ))
	   AND (opportunity_stage_arg = '' OR LOWER(cf.opportunity_stage) LIKE '%' || LOWER(left(opportunity_stage_arg, 160)) || '%')
	   AND ((crm_object_type_arg = '' AND crm_object_id_arg = '') OR EXISTS (SELECT 1 FROM call_context_objects filter_o WHERE filter_o.call_id = cf.call_id AND (crm_object_type_arg = '' OR filter_o.object_type = crm_object_type_arg) AND (crm_object_id_arg = '' OR filter_o.object_id = crm_object_id_arg)))
	   AND (participant_title_query_arg = '' OR
		       EXISTS (SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(c.raw_json->'parties') = 'array' THEN c.raw_json->'parties' ELSE '[]'::jsonb END) p WHERE LOWER(TRIM(COALESCE(p.value->>'title', p.value->>'jobTitle', p.value->>'job_title', ''))) LIKE '%' || LOWER(left(participant_title_query_arg, 160)) || '%') OR
		       EXISTS (SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(c.raw_json->'metaData'->'parties') = 'array' THEN c.raw_json->'metaData'->'parties' ELSE '[]'::jsonb END) p WHERE LOWER(TRIM(COALESCE(p.value->>'title', p.value->>'jobTitle', p.value->>'job_title', ''))) LIKE '%' || LOWER(left(participant_title_query_arg, 160)) || '%') OR
		       EXISTS (SELECT 1 FROM call_context_objects po JOIN call_context_fields pf ON pf.call_id = po.call_id AND pf.object_key = po.object_key WHERE po.call_id = cf.call_id AND po.object_type IN ('Contact', 'Lead') AND pf.field_name IN ('Title', 'JobTitle', 'Job_Title__c', 'JobTitle__c') AND LOWER(TRIM(pf.field_value_text)) LIKE '%' || LOWER(left(participant_title_query_arg, 160)) || '%'))
),
party_roles AS (
	SELECT DISTINCT c.call_id,
	       NULLIF(TRIM(BOTH '"' FROM COALESCE(p.value->>'speakerId', p.value->>'speaker_id', p.value->>'id', '')), '') AS speaker_key,
	       CASE
		       WHEN LOWER(TRIM(COALESCE(p.value->>'affiliation', p.value->>'Affiliation', p.value->>'partyType', p.value->>'party_type', ''))) = 'internal' THEN 'internal'
		       WHEN LOWER(TRIM(COALESCE(p.value->>'affiliation', p.value->>'Affiliation', p.value->>'partyType', p.value->>'party_type', ''))) = 'external' THEN 'external'
		       WHEN LOWER(TRIM(COALESCE(p.value->>'isInternal', p.value->>'is_internal', ''))) = 'true' THEN 'internal'
		       WHEN LOWER(TRIM(COALESCE(p.value->>'isInternal', p.value->>'is_internal', ''))) = 'false' THEN 'external'
		       ELSE NULL
	       END AS speaker_role
	  FROM calls c
	  JOIN (SELECT DISTINCT call_id FROM matched) m ON m.call_id = c.call_id,
	       LATERAL (
		       SELECT jsonb_array_elements(CASE WHEN jsonb_typeof(c.raw_json->'parties') = 'array' THEN c.raw_json->'parties' ELSE '[]'::jsonb END) AS value
		       UNION ALL
		       SELECT jsonb_array_elements(CASE WHEN jsonb_typeof(c.raw_json->'metaData'->'parties') = 'array' THEN c.raw_json->'metaData'->'parties' ELSE '[]'::jsonb END) AS value
	       ) AS p
),
speaker_roles AS (
	SELECT m.call_id,
	       m.segment_index,
	       m.speaker_id,
	       COUNT(p.speaker_key) FILTER (WHERE p.speaker_key IS NOT NULL AND p.speaker_key = m.speaker_id) AS match_count,
	       MAX(p.speaker_role) FILTER (WHERE p.speaker_key IS NOT NULL AND p.speaker_key = m.speaker_id AND p.speaker_role IS NOT NULL) AS role_when_present,
	       COUNT(p.speaker_role) FILTER (WHERE p.speaker_key IS NOT NULL AND p.speaker_key = m.speaker_id AND p.speaker_role IS NOT NULL) AS role_count,
	       COUNT(DISTINCT p.speaker_role) FILTER (WHERE p.speaker_key IS NOT NULL AND p.speaker_key = m.speaker_id AND p.speaker_role IS NOT NULL) AS role_distinct_count
	  FROM matched m
	  LEFT JOIN party_roles p ON p.call_id = m.call_id
	 GROUP BY m.call_id, m.segment_index, m.speaker_id
)
SELECT m.call_id,
       m.title,
       m.started_at,
       m.call_date,
       m.call_month,
       m.lifecycle_bucket,
       m.account_industry,
       ''::text AS account_name,
       ''::text AS opportunity_name,
       m.opportunity_stage,
       m.opportunity_type,
       ''::text AS opportunity_probability,
       ''::text AS opportunity_close_date,
       CASE WHEN m.parties_count > 0 THEN 'present' ELSE 'missing_from_cache' END AS participant_status,
       CASE
	       WHEN m.party_title_count > 0 THEN 'available'
	       WHEN EXISTS (SELECT 1 FROM call_context_objects po WHERE po.call_id = m.call_id AND po.object_type IN ('Contact', 'Lead')) THEN 'contact_or_lead_present_title_unverified'
	       WHEN m.parties_count > 0 THEN 'participants_present_check_party_titles'
	       ELSE 'missing_from_cache'
       END AS person_title_status,
       CASE
	       WHEN m.party_title_count > 0 THEN 'call_parties'
	       WHEN EXISTS (SELECT 1 FROM call_context_objects po WHERE po.call_id = m.call_id AND po.object_type IN ('Contact', 'Lead')) THEN 'contact_or_lead_object'
	       ELSE ''
       END AS person_title_source,
       m.segment_index,
       m.start_ms,
       m.end_ms,
       m.snippet,
       left(COALESCE((SELECT string_agg(ctx.text, ' ' ORDER BY ctx.segment_index) FROM transcript_segments ctx WHERE ctx.call_id = m.call_id AND ctx.segment_index BETWEEN m.segment_index - 1 AND m.segment_index + 1), ''), 800) AS context_excerpt,
       CASE
	       WHEN COALESCE(NULLIF(TRIM(m.speaker_id), ''), '') = '' THEN 'unknown'
	       WHEN sr.match_count IS NULL OR sr.match_count = 0 THEN 'unknown'
	       WHEN sr.match_count > 1 AND (sr.role_count = 0 OR sr.role_distinct_count <> 1) THEN 'unknown'
	       WHEN sr.role_count > 0 THEN sr.role_when_present
	       ELSE 'unknown'
       END AS speaker_role,
       CASE
	       WHEN COALESCE(NULLIF(TRIM(m.speaker_id), ''), '') = '' THEN 'speaker_unmatched'
	       WHEN sr.match_count IS NULL OR sr.match_count = 0 THEN 'speaker_unmatched'
	       WHEN sr.match_count > 1 THEN 'speaker_ambiguous'
	       WHEN sr.role_count > 0 THEN 'available'
	       ELSE 'affiliation_missing'
       END AS speaker_role_status
  FROM matched m
  LEFT JOIN speaker_roles sr ON sr.call_id = m.call_id AND sr.segment_index = m.segment_index AND sr.speaker_id = m.speaker_id
 ORDER BY m.rank DESC, m.started_at DESC, m.call_id, m.segment_index
 LIMIT LEAST(GREATEST(COALESCE(row_limit, 25), 1), 1000)
$function$;

CREATE OR REPLACE FUNCTION gongmcp_business_analysis_theme_seed_sample(title_query_arg text, transcript_query_arg text, from_date_arg text, to_date_arg text, lifecycle_bucket_arg text, scope_arg text, system_arg text, direction_arg text, transcript_status_arg text, industry_arg text, account_query_arg text, opportunity_stage_arg text, crm_object_type_arg text, crm_object_id_arg text, participant_title_query_arg text, exclude_lifecycle_buckets_json text, exclude_likely_voicemail_arg boolean, row_limit integer)
RETURNS TABLE(call_id text, title text, started_at text, call_date text, call_month text, lifecycle_bucket text, account_industry text, account_name text, opportunity_name text, opportunity_stage text, opportunity_type text, opportunity_probability text, opportunity_close_date text, participant_status text, person_title_status text, person_title_source text, segment_index integer, start_ms bigint, end_ms bigint, snippet text, context_excerpt text, speaker_role text, speaker_role_status text)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = public, pg_temp
AS $function$
WITH sampled AS (
	SELECT cf.call_id,
	       cf.title,
	       cf.started_at,
	       cf.call_date,
	       cf.call_month,
	       cf.lifecycle_bucket,
	       cf.account_industry,
	       cf.opportunity_stage,
	       cf.opportunity_type,
	       c.parties_count,
	       ts.speaker_id,
	       ts.segment_index,
	       ts.start_ms,
	       ts.end_ms,
	       left(ts.text, 400) AS snippet,
	       COALESCE((SELECT COUNT(1) FROM jsonb_array_elements(CASE WHEN jsonb_typeof(c.raw_json->'parties') = 'array' THEN c.raw_json->'parties' ELSE '[]'::jsonb END) p WHERE TRIM(COALESCE(p.value->>'title', p.value->>'jobTitle', p.value->>'job_title', '')) <> ''), 0) +
	       COALESCE((SELECT COUNT(1) FROM jsonb_array_elements(CASE WHEN jsonb_typeof(c.raw_json->'metaData'->'parties') = 'array' THEN c.raw_json->'metaData'->'parties' ELSE '[]'::jsonb END) p WHERE TRIM(COALESCE(p.value->>'title', p.value->>'jobTitle', p.value->>'job_title', '')) <> ''), 0) AS party_title_count
	  FROM transcript_segments ts
	  JOIN call_facts cf
	    ON cf.call_id = ts.call_id
	  JOIN calls c
	    ON c.call_id = ts.call_id
	 WHERE (title_query_arg = '' OR LOWER(cf.title) LIKE '%' || LOWER(left(title_query_arg, 160)) || '%')
	   AND (transcript_query_arg = '' OR EXISTS (SELECT 1 FROM transcript_segments qts WHERE qts.call_id = cf.call_id AND qts.search_vector @@ websearch_to_tsquery('simple', left(transcript_query_arg, 160))))
	   AND (from_date_arg = '' OR cf.call_date >= from_date_arg)
	   AND (to_date_arg = '' OR cf.call_date <= to_date_arg)
	   AND (lifecycle_bucket_arg = '' OR cf.lifecycle_bucket = lifecycle_bucket_arg)
	   AND (COALESCE(NULLIF(exclude_lifecycle_buckets_json, ''), '[]') = '[]' OR NOT EXISTS (SELECT 1 FROM jsonb_array_elements_text(COALESCE(NULLIF(exclude_lifecycle_buckets_json, ''), '[]')::jsonb) excluded(value) WHERE LOWER(TRIM(excluded.value)) = cf.lifecycle_bucket))
	   AND (NOT COALESCE(exclude_likely_voicemail_arg, false) OR NOT cf.likely_voicemail_or_ivr)
	   AND (scope_arg = '' OR cf.scope = scope_arg)
	   AND (system_arg = '' OR cf.system = system_arg)
	   AND (direction_arg = '' OR cf.direction = direction_arg)
	   AND (transcript_status_arg = '' OR cf.transcript_status = transcript_status_arg)
	   AND (industry_arg = '' OR LOWER(cf.account_industry) LIKE '%' || LOWER(left(industry_arg, 160)) || '%')
	   AND (account_query_arg = '' OR EXISTS (
		   SELECT 1
		     FROM call_context_objects filter_o
		     LEFT JOIN call_context_fields filter_f
		       ON filter_f.call_id = filter_o.call_id
		      AND filter_f.object_key = filter_o.object_key
		      AND filter_f.field_name = 'Name'
		    WHERE filter_o.call_id = cf.call_id
		      AND filter_o.object_type = 'Account'
		      AND (
		          LOWER(TRIM(filter_o.object_name)) LIKE '%' || LOWER(left(account_query_arg, 160)) || '%'
		       OR LOWER(TRIM(filter_f.field_value_text)) LIKE '%' || LOWER(left(account_query_arg, 160)) || '%'
		      )
	   ))
	   AND (opportunity_stage_arg = '' OR LOWER(cf.opportunity_stage) LIKE '%' || LOWER(left(opportunity_stage_arg, 160)) || '%')
	   AND ((crm_object_type_arg = '' AND crm_object_id_arg = '') OR EXISTS (SELECT 1 FROM call_context_objects filter_o WHERE filter_o.call_id = cf.call_id AND (crm_object_type_arg = '' OR filter_o.object_type = crm_object_type_arg) AND (crm_object_id_arg = '' OR filter_o.object_id = crm_object_id_arg)))
	   AND (participant_title_query_arg = '' OR
		       EXISTS (SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(c.raw_json->'parties') = 'array' THEN c.raw_json->'parties' ELSE '[]'::jsonb END) p WHERE LOWER(TRIM(COALESCE(p.value->>'title', p.value->>'jobTitle', p.value->>'job_title', ''))) LIKE '%' || LOWER(left(participant_title_query_arg, 160)) || '%') OR
		       EXISTS (SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(c.raw_json->'metaData'->'parties') = 'array' THEN c.raw_json->'metaData'->'parties' ELSE '[]'::jsonb END) p WHERE LOWER(TRIM(COALESCE(p.value->>'title', p.value->>'jobTitle', p.value->>'job_title', ''))) LIKE '%' || LOWER(left(participant_title_query_arg, 160)) || '%') OR
		       EXISTS (SELECT 1 FROM call_context_objects po JOIN call_context_fields pf ON pf.call_id = po.call_id AND pf.object_key = po.object_key WHERE po.call_id = cf.call_id AND po.object_type IN ('Contact', 'Lead') AND pf.field_name IN ('Title', 'JobTitle', 'Job_Title__c', 'JobTitle__c') AND LOWER(TRIM(pf.field_value_text)) LIKE '%' || LOWER(left(participant_title_query_arg, 160)) || '%'))
),
party_roles AS (
	SELECT DISTINCT c.call_id,
	       NULLIF(TRIM(BOTH '"' FROM COALESCE(p.value->>'speakerId', p.value->>'speaker_id', p.value->>'id', '')), '') AS speaker_key,
	       CASE
		       WHEN LOWER(TRIM(COALESCE(p.value->>'affiliation', p.value->>'Affiliation', p.value->>'partyType', p.value->>'party_type', ''))) = 'internal' THEN 'internal'
		       WHEN LOWER(TRIM(COALESCE(p.value->>'affiliation', p.value->>'Affiliation', p.value->>'partyType', p.value->>'party_type', ''))) = 'external' THEN 'external'
		       WHEN LOWER(TRIM(COALESCE(p.value->>'isInternal', p.value->>'is_internal', ''))) = 'true' THEN 'internal'
		       WHEN LOWER(TRIM(COALESCE(p.value->>'isInternal', p.value->>'is_internal', ''))) = 'false' THEN 'external'
		       ELSE NULL
	       END AS speaker_role
	  FROM calls c
	  JOIN (SELECT DISTINCT call_id FROM sampled) s ON s.call_id = c.call_id,
	       LATERAL (
		       SELECT jsonb_array_elements(CASE WHEN jsonb_typeof(c.raw_json->'parties') = 'array' THEN c.raw_json->'parties' ELSE '[]'::jsonb END) AS value
		       UNION ALL
		       SELECT jsonb_array_elements(CASE WHEN jsonb_typeof(c.raw_json->'metaData'->'parties') = 'array' THEN c.raw_json->'metaData'->'parties' ELSE '[]'::jsonb END) AS value
	       ) AS p
),
speaker_roles AS (
	SELECT s.call_id,
	       s.segment_index,
	       s.speaker_id,
	       COUNT(p.speaker_key) FILTER (WHERE p.speaker_key IS NOT NULL AND p.speaker_key = s.speaker_id) AS match_count,
	       MAX(p.speaker_role) FILTER (WHERE p.speaker_key IS NOT NULL AND p.speaker_key = s.speaker_id AND p.speaker_role IS NOT NULL) AS role_when_present,
	       COUNT(p.speaker_role) FILTER (WHERE p.speaker_key IS NOT NULL AND p.speaker_key = s.speaker_id AND p.speaker_role IS NOT NULL) AS role_count,
	       COUNT(DISTINCT p.speaker_role) FILTER (WHERE p.speaker_key IS NOT NULL AND p.speaker_key = s.speaker_id AND p.speaker_role IS NOT NULL) AS role_distinct_count
	  FROM sampled s
	  LEFT JOIN party_roles p ON p.call_id = s.call_id
	 GROUP BY s.call_id, s.segment_index, s.speaker_id
)
SELECT s.call_id,
       s.title,
       s.started_at,
       s.call_date,
       s.call_month,
       s.lifecycle_bucket,
       s.account_industry,
       ''::text AS account_name,
       ''::text AS opportunity_name,
       s.opportunity_stage,
       s.opportunity_type,
       ''::text AS opportunity_probability,
       ''::text AS opportunity_close_date,
       CASE WHEN s.parties_count > 0 THEN 'present' ELSE 'missing_from_cache' END AS participant_status,
       CASE
	       WHEN s.party_title_count > 0 THEN 'available'
	       WHEN EXISTS (SELECT 1 FROM call_context_objects po WHERE po.call_id = s.call_id AND po.object_type IN ('Contact', 'Lead')) THEN 'contact_or_lead_present_title_unverified'
	       WHEN s.parties_count > 0 THEN 'participants_present_check_party_titles'
	       ELSE 'missing_from_cache'
       END AS person_title_status,
       CASE
	       WHEN s.party_title_count > 0 THEN 'call_parties'
	       WHEN EXISTS (SELECT 1 FROM call_context_objects po WHERE po.call_id = s.call_id AND po.object_type IN ('Contact', 'Lead')) THEN 'contact_or_lead_object'
	       ELSE ''
       END AS person_title_source,
       s.segment_index,
       s.start_ms,
       s.end_ms,
       s.snippet,
       left(COALESCE((SELECT string_agg(ctx.text, ' ' ORDER BY ctx.segment_index) FROM transcript_segments ctx WHERE ctx.call_id = s.call_id AND ctx.segment_index BETWEEN s.segment_index - 1 AND s.segment_index + 1), ''), 800) AS context_excerpt,
       CASE
	       WHEN COALESCE(NULLIF(TRIM(s.speaker_id), ''), '') = '' THEN 'unknown'
	       WHEN sr.match_count IS NULL OR sr.match_count = 0 THEN 'unknown'
	       WHEN sr.match_count > 1 AND (sr.role_count = 0 OR sr.role_distinct_count <> 1) THEN 'unknown'
	       WHEN sr.role_count > 0 THEN sr.role_when_present
	       ELSE 'unknown'
       END AS speaker_role,
       CASE
	       WHEN COALESCE(NULLIF(TRIM(s.speaker_id), ''), '') = '' THEN 'speaker_unmatched'
	       WHEN sr.match_count IS NULL OR sr.match_count = 0 THEN 'speaker_unmatched'
	       WHEN sr.match_count > 1 THEN 'speaker_ambiguous'
	       WHEN sr.role_count > 0 THEN 'available'
	       ELSE 'affiliation_missing'
       END AS speaker_role_status
  FROM sampled s
  LEFT JOIN speaker_roles sr ON sr.call_id = s.call_id AND sr.segment_index = s.segment_index AND sr.speaker_id = s.speaker_id
 ORDER BY s.started_at DESC, s.call_id, s.segment_index
 LIMIT LEAST(GREATEST(COALESCE(row_limit, 25), 1), 1000)
$function$;

CREATE OR REPLACE FUNCTION gongmcp_business_analysis_persona_bucket(call_id_arg text, fact_title_present boolean)
RETURNS text
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = public, pg_temp
AS $function$
WITH titles AS (
	SELECT ' ' || REPLACE(REPLACE(REPLACE(REPLACE(REPLACE(REPLACE(LOWER(TRIM(COALESCE(p.value->>'title', p.value->>'jobTitle', p.value->>'job_title', ''))), ',', ' '), '/', ' '), '-', ' '), '.', ' '), E'\t', ' '), E'\n', ' ') || ' ' AS t
	  FROM calls c
	  CROSS JOIN LATERAL jsonb_array_elements(CASE WHEN jsonb_typeof(c.raw_json->'parties') = 'array' THEN c.raw_json->'parties' ELSE '[]'::jsonb END) AS p
	 WHERE c.call_id = call_id_arg
	   AND COALESCE(p.value->>'title', p.value->>'jobTitle', p.value->>'job_title', '') <> ''
	UNION ALL
	SELECT ' ' || REPLACE(REPLACE(REPLACE(REPLACE(REPLACE(REPLACE(LOWER(TRIM(COALESCE(p.value->>'title', p.value->>'jobTitle', p.value->>'job_title', ''))), ',', ' '), '/', ' '), '-', ' '), '.', ' '), E'\t', ' '), E'\n', ' ') || ' '
	  FROM calls c
	  CROSS JOIN LATERAL jsonb_array_elements(CASE WHEN jsonb_typeof(c.raw_json->'metaData'->'parties') = 'array' THEN c.raw_json->'metaData'->'parties' ELSE '[]'::jsonb END) AS p
	 WHERE c.call_id = call_id_arg
	   AND COALESCE(p.value->>'title', p.value->>'jobTitle', p.value->>'job_title', '') <> ''
	UNION ALL
	SELECT ' ' || REPLACE(REPLACE(REPLACE(REPLACE(REPLACE(REPLACE(LOWER(TRIM(f.field_value_text)), ',', ' '), '/', ' '), '-', ' '), '.', ' '), E'\t', ' '), E'\n', ' ') || ' '
	  FROM call_context_fields f
	  JOIN call_context_objects o
	    ON o.call_id = f.call_id
	   AND o.object_key = f.object_key
	 WHERE f.call_id = call_id_arg
	   AND o.object_type IN ('Contact', 'Lead')
	   AND f.field_name IN ('Title', 'JobTitle', 'Job_Title__c', 'JobTitle__c')
	   AND TRIM(f.field_value_text) <> ''
)
SELECT CASE
	WHEN EXISTS (SELECT 1 FROM titles WHERE t LIKE '%procurement%' OR t LIKE '%purchasing%' OR t LIKE '%sourcing%' OR t LIKE '%buyer%' OR t LIKE '%category manager%') THEN 'procurement'
	WHEN EXISTS (SELECT 1 FROM titles WHERE t LIKE '%supplier%' OR t LIKE '%vendor manager%' OR t LIKE '%vendor management%' OR t LIKE '%vendor enablement%' OR t LIKE '%channel manager%' OR t LIKE '%partner manager%' OR t LIKE '%alliances%') THEN 'supplier_enablement'
	WHEN EXISTS (SELECT 1 FROM titles WHERE t LIKE '% ciso %' OR t LIKE '% cio %' OR t LIKE '% cto %' OR t LIKE '%chief information%' OR t LIKE '%chief technology%' OR t LIKE '%vp it%' OR t LIKE '%vp of it%' OR t LIKE '%head of it%' OR t LIKE '%it director%' OR t LIKE '%it manager%' OR t LIKE '%infrastructure%' OR t LIKE '%security%' OR t LIKE '%infosec%' OR t LIKE '%integration%' OR t LIKE '%architect%' OR t LIKE '%devops%' OR t LIKE '%platform engineer%' OR t LIKE '%site reliability%') THEN 'it_security_integration'
	WHEN EXISTS (SELECT 1 FROM titles WHERE t LIKE '% cfo %' OR t LIKE '%chief financial%' OR t LIKE '%finance%' OR t LIKE '%controller%' OR t LIKE '%treasur%' OR t LIKE '%accounting%') THEN 'finance'
	WHEN EXISTS (SELECT 1 FROM titles WHERE t LIKE '% coo %' OR t LIKE '%chief operating%' OR t LIKE '%operations%' OR t LIKE '%supply chain%' OR t LIKE '%logistics%' OR t LIKE '%manufacturing%' OR t LIKE '%production%' OR t LIKE '%fulfillment%') THEN 'operations'
	WHEN EXISTS (SELECT 1 FROM titles WHERE t LIKE '%sales%' OR t LIKE '%revenue%' OR t LIKE '%account exec%' OR t LIKE '%account manager%' OR t LIKE '% sdr %' OR t LIKE '% bdr %' OR t LIKE '% ae %' OR t LIKE '% csm %' OR t LIKE '%customer success%' OR t LIKE '%go-to-market%' OR t LIKE '%gtm%') THEN 'sales_revenue'
	WHEN EXISTS (SELECT 1 FROM titles WHERE t LIKE '% ceo %' OR t LIKE '%chief executive%' OR t LIKE '%founder%' OR t LIKE '%president%' OR t LIKE '%chair%' OR t LIKE '%general manager%') THEN 'executive'
	WHEN EXISTS (SELECT 1 FROM titles WHERE TRIM(t) <> '') THEN 'other_title_present'
	WHEN COALESCE(fact_title_present, false) THEN 'participant_title_present'
	ELSE ''
END
$function$;
REVOKE ALL ON FUNCTION gongmcp_business_analysis_persona_bucket(text, boolean) FROM PUBLIC;

-- __INSERT_LOSS_REASON_BUCKET_FUNCTION_HERE__

CREATE OR REPLACE FUNCTION gongmcp_business_analysis_dimension(dimension_arg text, theme_query_arg text, title_query_arg text, transcript_query_arg text, from_date_arg text, to_date_arg text, lifecycle_bucket_arg text, scope_arg text, system_arg text, direction_arg text, transcript_status_arg text, industry_arg text, account_query_arg text, opportunity_stage_arg text, crm_object_type_arg text, crm_object_id_arg text, participant_title_query_arg text, exclude_lifecycle_buckets_json text, exclude_likely_voicemail_arg boolean, row_limit integer)
RETURNS TABLE(dimension text, value text, call_count bigint, transcript_count bigint, missing_transcript_count bigint, opportunity_call_count bigint, account_call_count bigint, external_call_count bigint, latest_call_at text)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = public, pg_temp
AS $function$
WITH rows AS (
	SELECT cf.*,
	       CASE lower(trim(dimension_arg))
		       WHEN '' THEN cf.lifecycle_bucket
		       WHEN 'lifecycle' THEN cf.lifecycle_bucket
		       WHEN 'lifecycle_bucket' THEN cf.lifecycle_bucket
		       WHEN 'industry' THEN cf.account_industry
		       WHEN 'account_industry' THEN cf.account_industry
		       WHEN 'persona' THEN gongmcp_business_analysis_persona_bucket(cf.call_id, cf.participant_title_present)
		       WHEN 'participant_title' THEN gongmcp_business_analysis_persona_bucket(cf.call_id, cf.participant_title_present)
		       WHEN 'title' THEN gongmcp_business_analysis_persona_bucket(cf.call_id, cf.participant_title_present)
		       WHEN 'opportunity_stage' THEN cf.opportunity_stage
		       WHEN 'stage' THEN cf.opportunity_stage
		       WHEN 'opportunity_type' THEN cf.opportunity_type
		       WHEN 'forecast_category' THEN cf.opportunity_forecast_category
		       WHEN 'scope' THEN cf.scope
		       WHEN 'system' THEN cf.system
		       WHEN 'direction' THEN cf.direction
		       WHEN 'transcript_status' THEN cf.transcript_status
		       WHEN 'month' THEN cf.call_month
		       WHEN 'call_month' THEN cf.call_month
		       WHEN 'quarter' THEN CASE
			       WHEN substring(cf.call_date from 6 for 2) IN ('01','02','03') THEN left(cf.call_date, 4) || '-Q1'
			       WHEN substring(cf.call_date from 6 for 2) IN ('04','05','06') THEN left(cf.call_date, 4) || '-Q2'
			       WHEN substring(cf.call_date from 6 for 2) IN ('07','08','09') THEN left(cf.call_date, 4) || '-Q3'
			       WHEN substring(cf.call_date from 6 for 2) IN ('10','11','12') THEN left(cf.call_date, 4) || '-Q4'
			       ELSE ''
		       END
		       WHEN 'won_lost' THEN CASE WHEN lower(cf.opportunity_stage) = 'closed won' THEN 'closed_won' WHEN lower(cf.opportunity_stage) = 'closed lost' THEN 'closed_lost' WHEN trim(cf.opportunity_stage) <> '' THEN 'open_or_in_progress' ELSE 'unknown' END
		       WHEN 'outcome' THEN CASE WHEN lower(cf.opportunity_stage) = 'closed won' THEN 'closed_won' WHEN lower(cf.opportunity_stage) = 'closed lost' THEN 'closed_lost' WHEN trim(cf.opportunity_stage) <> '' THEN 'open_or_in_progress' ELSE 'unknown' END
		       WHEN 'loss_reason' THEN gongmcp_business_analysis_loss_reason_bucket(cf.call_id, cf.loss_reason_present)
		       ELSE ''
	       END AS dimension_value
	  FROM call_facts cf
	 WHERE (title_query_arg = '' OR LOWER(cf.title) LIKE '%' || LOWER(left(title_query_arg, 160)) || '%')
	   AND (transcript_query_arg = '' OR EXISTS (SELECT 1 FROM transcript_segments qts WHERE qts.call_id = cf.call_id AND qts.search_vector @@ websearch_to_tsquery('simple', left(transcript_query_arg, 160))))
	   AND (theme_query_arg = '' OR EXISTS (SELECT 1 FROM transcript_segments qts WHERE qts.call_id = cf.call_id AND qts.search_vector @@ websearch_to_tsquery('simple', left(theme_query_arg, 160))))
	   AND (from_date_arg = '' OR cf.call_date >= from_date_arg)
	   AND (to_date_arg = '' OR cf.call_date <= to_date_arg)
	   AND (lifecycle_bucket_arg = '' OR cf.lifecycle_bucket = lifecycle_bucket_arg)
	   AND (COALESCE(NULLIF(exclude_lifecycle_buckets_json, ''), '[]') = '[]' OR NOT EXISTS (SELECT 1 FROM jsonb_array_elements_text(COALESCE(NULLIF(exclude_lifecycle_buckets_json, ''), '[]')::jsonb) excluded(value) WHERE LOWER(TRIM(excluded.value)) = cf.lifecycle_bucket))
	   AND (NOT COALESCE(exclude_likely_voicemail_arg, false) OR NOT cf.likely_voicemail_or_ivr)
	   AND (scope_arg = '' OR cf.scope = scope_arg)
	   AND (system_arg = '' OR cf.system = system_arg)
	   AND (direction_arg = '' OR cf.direction = direction_arg)
	   AND (transcript_status_arg = '' OR cf.transcript_status = transcript_status_arg)
	   AND (industry_arg = '' OR LOWER(cf.account_industry) LIKE '%' || LOWER(left(industry_arg, 160)) || '%')
	   AND (opportunity_stage_arg = '' OR LOWER(cf.opportunity_stage) LIKE '%' || LOWER(left(opportunity_stage_arg, 160)) || '%')
	   AND ((crm_object_type_arg = '' AND crm_object_id_arg = '') OR EXISTS (SELECT 1 FROM call_context_objects filter_o WHERE filter_o.call_id = cf.call_id AND (crm_object_type_arg = '' OR filter_o.object_type = crm_object_type_arg) AND (crm_object_id_arg = '' OR filter_o.object_id = crm_object_id_arg)))
	   AND (participant_title_query_arg = '' OR
		       EXISTS (
			       SELECT 1
			         FROM calls filter_c
			        WHERE filter_c.call_id = cf.call_id
			          AND (
			              EXISTS (SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(filter_c.raw_json->'parties') = 'array' THEN filter_c.raw_json->'parties' ELSE '[]'::jsonb END) p WHERE LOWER(TRIM(COALESCE(p.value->>'title', p.value->>'jobTitle', p.value->>'job_title', ''))) LIKE '%' || LOWER(left(participant_title_query_arg, 160)) || '%')
			           OR EXISTS (SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(filter_c.raw_json->'metaData'->'parties') = 'array' THEN filter_c.raw_json->'metaData'->'parties' ELSE '[]'::jsonb END) p WHERE LOWER(TRIM(COALESCE(p.value->>'title', p.value->>'jobTitle', p.value->>'job_title', ''))) LIKE '%' || LOWER(left(participant_title_query_arg, 160)) || '%')
			          )
		       )
		       OR EXISTS (SELECT 1 FROM call_context_objects po JOIN call_context_fields pf ON pf.call_id = po.call_id AND pf.object_key = po.object_key WHERE po.call_id = cf.call_id AND po.object_type IN ('Contact', 'Lead') AND pf.field_name IN ('Title', 'JobTitle', 'Job_Title__c', 'JobTitle__c') AND LOWER(TRIM(pf.field_value_text)) LIKE '%' || LOWER(left(participant_title_query_arg, 160)) || '%'))
)
SELECT CASE lower(trim(dimension_arg))
	       WHEN '' THEN 'lifecycle'
	       WHEN 'lifecycle_bucket' THEN 'lifecycle'
	       WHEN 'industry' THEN 'industry'
	       WHEN 'account_industry' THEN 'industry'
	       WHEN 'participant_title' THEN 'persona'
	       WHEN 'title' THEN 'persona'
	       WHEN 'stage' THEN 'opportunity_stage'
	       WHEN 'outcome' THEN 'won_lost'
	       ELSE lower(trim(dimension_arg))
       END AS dimension,
       COALESCE(NULLIF(TRIM(dimension_value), ''), '<blank>') AS value,
       COUNT(*) AS call_count,
       COALESCE(SUM(CASE WHEN transcript_status = 'present' THEN 1 ELSE 0 END), 0) AS transcript_count,
       COALESCE(SUM(CASE WHEN transcript_status = 'missing' THEN 1 ELSE 0 END), 0) AS missing_transcript_count,
       COALESCE(SUM(CASE WHEN opportunity_count > 0 THEN 1 ELSE 0 END), 0) AS opportunity_call_count,
       COALESCE(SUM(CASE WHEN account_count > 0 THEN 1 ELSE 0 END), 0) AS account_call_count,
       COALESCE(SUM(CASE WHEN scope = 'External' THEN 1 ELSE 0 END), 0) AS external_call_count,
       COALESCE(MAX(started_at), '') AS latest_call_at
  FROM rows
 GROUP BY value
 ORDER BY call_count DESC, value
 LIMIT LEAST(GREATEST(COALESCE(row_limit, 25), 1), 1000)
$function$;

CREATE OR REPLACE FUNCTION gongmcp_search_transcript_quotes_with_attribution_sanitized(search_text text, from_date_arg text, to_date_arg text, lifecycle_bucket_arg text, scope_arg text, system_arg text, direction_arg text, transcript_status_arg text, industry_arg text, account_query_arg text, opportunity_stage_arg text, row_limit integer)
RETURNS TABLE(call_id text, title text, started_at text, call_date text, lifecycle_bucket text, account_name text, account_industry text, account_website text, opportunity_name text, opportunity_stage text, opportunity_type text, opportunity_close_date text, opportunity_probability text, participant_status text, person_title_status text, person_title_source text, segment_index integer, start_ms bigint, end_ms bigint, snippet text, context_excerpt text, speaker_role text, speaker_role_status text)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = public, pg_temp
AS $function$
SELECT md5(raw.call_id) AS call_id,
       ''::text AS title,
       raw.started_at,
       raw.call_date,
       raw.lifecycle_bucket,
       ''::text AS account_name,
       raw.account_industry,
       ''::text AS account_website,
       ''::text AS opportunity_name,
       raw.opportunity_stage,
       raw.opportunity_type,
       ''::text AS opportunity_close_date,
       ''::text AS opportunity_probability,
       raw.participant_status,
       raw.person_title_status,
       raw.person_title_source,
       raw.segment_index,
       raw.start_ms,
       raw.end_ms,
       raw.snippet,
       raw.context_excerpt,
       raw.speaker_role,
       raw.speaker_role_status
  FROM gongmcp_search_transcript_quotes_with_attribution(search_text, from_date_arg, to_date_arg, lifecycle_bucket_arg, scope_arg, system_arg, direction_arg, transcript_status_arg, industry_arg, account_query_arg, opportunity_stage_arg, row_limit) raw
$function$;

CREATE OR REPLACE FUNCTION gongmcp_business_analysis_calls_sanitized(title_query_arg text, transcript_query_arg text, from_date_arg text, to_date_arg text, lifecycle_bucket_arg text, scope_arg text, system_arg text, direction_arg text, transcript_status_arg text, industry_arg text, account_query_arg text, opportunity_stage_arg text, crm_object_type_arg text, crm_object_id_arg text, participant_title_query_arg text, exclude_lifecycle_buckets_json text, exclude_likely_voicemail_arg boolean, row_limit integer)
RETURNS TABLE(call_id text, title text, started_at text, call_date text, call_month text, duration_seconds bigint, lifecycle_bucket text, likely_voicemail_or_ivr boolean, scope text, system text, direction text, transcript_status text, account_industry text, opportunity_stage text, opportunity_type text, forecast_category text, opportunity_count bigint, account_count bigint, participant_status text, person_title_status text, person_title_source text)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = public, pg_temp
AS $function$
SELECT md5(raw.call_id) AS call_id,
       ''::text AS title,
       raw.started_at,
       raw.call_date,
       raw.call_month,
       raw.duration_seconds,
       raw.lifecycle_bucket,
       raw.likely_voicemail_or_ivr,
       raw.scope,
       raw.system,
       raw.direction,
       raw.transcript_status,
       raw.account_industry,
       raw.opportunity_stage,
       raw.opportunity_type,
       raw.forecast_category,
       raw.opportunity_count,
       raw.account_count,
       raw.participant_status,
       raw.person_title_status,
       raw.person_title_source
  FROM gongmcp_business_analysis_calls(title_query_arg, transcript_query_arg, from_date_arg, to_date_arg, lifecycle_bucket_arg, scope_arg, system_arg, direction_arg, transcript_status_arg, industry_arg, account_query_arg, opportunity_stage_arg, crm_object_type_arg, crm_object_id_arg, participant_title_query_arg, exclude_lifecycle_buckets_json, exclude_likely_voicemail_arg, row_limit) raw
$function$;

CREATE OR REPLACE FUNCTION gongmcp_business_analysis_evidence_sanitized(search_text text, title_query_arg text, transcript_query_arg text, from_date_arg text, to_date_arg text, lifecycle_bucket_arg text, scope_arg text, system_arg text, direction_arg text, transcript_status_arg text, industry_arg text, account_query_arg text, opportunity_stage_arg text, crm_object_type_arg text, crm_object_id_arg text, participant_title_query_arg text, exclude_lifecycle_buckets_json text, exclude_likely_voicemail_arg boolean, row_limit integer)
RETURNS TABLE(call_id text, title text, started_at text, call_date text, call_month text, lifecycle_bucket text, account_industry text, account_name text, opportunity_name text, opportunity_stage text, opportunity_type text, opportunity_probability text, opportunity_close_date text, participant_status text, person_title_status text, person_title_source text, segment_index integer, start_ms bigint, end_ms bigint, snippet text, context_excerpt text, speaker_role text, speaker_role_status text)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = public, pg_temp
AS $function$
SELECT md5(raw.call_id) AS call_id,
       ''::text AS title,
       raw.started_at,
       raw.call_date,
       raw.call_month,
       raw.lifecycle_bucket,
       raw.account_industry,
       ''::text AS account_name,
       ''::text AS opportunity_name,
       raw.opportunity_stage,
       raw.opportunity_type,
       ''::text AS opportunity_probability,
       ''::text AS opportunity_close_date,
       raw.participant_status,
       raw.person_title_status,
       raw.person_title_source,
       raw.segment_index,
       raw.start_ms,
       raw.end_ms,
       raw.snippet,
       raw.context_excerpt,
       raw.speaker_role,
       raw.speaker_role_status
  FROM gongmcp_business_analysis_evidence(search_text, title_query_arg, transcript_query_arg, from_date_arg, to_date_arg, lifecycle_bucket_arg, scope_arg, system_arg, direction_arg, transcript_status_arg, industry_arg, account_query_arg, opportunity_stage_arg, crm_object_type_arg, crm_object_id_arg, participant_title_query_arg, exclude_lifecycle_buckets_json, exclude_likely_voicemail_arg, row_limit) raw
$function$;

CREATE OR REPLACE FUNCTION gongmcp_business_analysis_theme_seed_sample_sanitized(title_query_arg text, transcript_query_arg text, from_date_arg text, to_date_arg text, lifecycle_bucket_arg text, scope_arg text, system_arg text, direction_arg text, transcript_status_arg text, industry_arg text, account_query_arg text, opportunity_stage_arg text, crm_object_type_arg text, crm_object_id_arg text, participant_title_query_arg text, exclude_lifecycle_buckets_json text, exclude_likely_voicemail_arg boolean, row_limit integer)
RETURNS TABLE(call_id text, title text, started_at text, call_date text, call_month text, lifecycle_bucket text, account_industry text, account_name text, opportunity_name text, opportunity_stage text, opportunity_type text, opportunity_probability text, opportunity_close_date text, participant_status text, person_title_status text, person_title_source text, segment_index integer, start_ms bigint, end_ms bigint, snippet text, context_excerpt text, speaker_role text, speaker_role_status text)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = public, pg_temp
AS $function$
SELECT md5(raw.call_id) AS call_id,
       ''::text AS title,
       raw.started_at,
       raw.call_date,
       raw.call_month,
       raw.lifecycle_bucket,
       raw.account_industry,
       ''::text AS account_name,
       ''::text AS opportunity_name,
       raw.opportunity_stage,
       raw.opportunity_type,
       ''::text AS opportunity_probability,
       ''::text AS opportunity_close_date,
       raw.participant_status,
       raw.person_title_status,
       raw.person_title_source,
       raw.segment_index,
       raw.start_ms,
       raw.end_ms,
       raw.snippet,
       raw.context_excerpt,
       raw.speaker_role,
       raw.speaker_role_status
  FROM gongmcp_business_analysis_theme_seed_sample(title_query_arg, transcript_query_arg, from_date_arg, to_date_arg, lifecycle_bucket_arg, scope_arg, system_arg, direction_arg, transcript_status_arg, industry_arg, account_query_arg, opportunity_stage_arg, crm_object_type_arg, crm_object_id_arg, participant_title_query_arg, exclude_lifecycle_buckets_json, exclude_likely_voicemail_arg, row_limit) raw
$function$;

CREATE OR REPLACE FUNCTION gongmcp_search_transcript_segments_sanitized(search_text text, row_limit integer)
RETURNS TABLE(call_id text, speaker_id text, segment_index integer, start_ms bigint, end_ms bigint, text text, snippet text)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = public, pg_temp
AS $function$
SELECT md5(raw.call_id) AS call_id,
       CASE WHEN raw.speaker_id = '' THEN '' ELSE md5(raw.speaker_id) END AS speaker_id,
       raw.segment_index,
       raw.start_ms,
       raw.end_ms,
       ''::text AS text,
       raw.snippet
  FROM gongmcp_search_transcript_segments(search_text, row_limit) raw
$function$;

CREATE OR REPLACE FUNCTION gongmcp_search_transcript_segments_by_call_facts_sanitized(search_text text, from_date_arg text, to_date_arg text, lifecycle_bucket_arg text, scope_arg text, system_arg text, direction_arg text, row_limit integer)
RETURNS TABLE(call_id text, started_at text, call_date text, call_month text, duration_seconds bigint, lifecycle_bucket text, scope text, system text, direction text, speaker_id text, segment_index integer, start_ms bigint, end_ms bigint, snippet text, context_excerpt text)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = public, pg_temp
AS $function$
SELECT md5(raw.call_id) AS call_id,
       raw.started_at,
       raw.call_date,
       raw.call_month,
       raw.duration_seconds,
       raw.lifecycle_bucket,
       raw.scope,
       raw.system,
       raw.direction,
       CASE WHEN raw.speaker_id = '' THEN '' ELSE md5(raw.speaker_id) END AS speaker_id,
       raw.segment_index,
       raw.start_ms,
       raw.end_ms,
       raw.snippet,
       raw.context_excerpt
  FROM gongmcp_search_transcript_segments_by_call_facts(search_text, from_date_arg, to_date_arg, lifecycle_bucket_arg, scope_arg, system_arg, direction_arg, row_limit) raw
$function$;

REVOKE ALL ON FUNCTION gongmcp_search_transcript_segments_by_call_facts(text, text, text, text, text, text, text, integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION gongmcp_search_transcript_quotes_with_attribution(text, text, text, text, text, text, text, text, text, text, text, integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION gongmcp_business_analysis_calls(text, text, text, text, text, text, text, text, text, text, text, text, text, text, text, text, boolean, integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION gongmcp_business_analysis_summary(text, text, text, text, text, text, text, text, text, text, text, text, text, text, text, text, boolean) FROM PUBLIC;
REVOKE ALL ON FUNCTION gongmcp_business_analysis_evidence(text, text, text, text, text, text, text, text, text, text, text, text, text, text, text, text, text, boolean, integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION gongmcp_business_analysis_theme_seed_sample(text, text, text, text, text, text, text, text, text, text, text, text, text, text, text, text, boolean, integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION gongmcp_business_analysis_dimension(text, text, text, text, text, text, text, text, text, text, text, text, text, text, text, text, text, text, boolean, integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION gongmcp_search_transcript_quotes_with_attribution_sanitized(text, text, text, text, text, text, text, text, text, text, text, integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION gongmcp_business_analysis_calls_sanitized(text, text, text, text, text, text, text, text, text, text, text, text, text, text, text, text, boolean, integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION gongmcp_business_analysis_evidence_sanitized(text, text, text, text, text, text, text, text, text, text, text, text, text, text, text, text, text, boolean, integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION gongmcp_business_analysis_theme_seed_sample_sanitized(text, text, text, text, text, text, text, text, text, text, text, text, text, text, text, text, boolean, integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION gongmcp_search_transcript_segments_sanitized(text, integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION gongmcp_search_transcript_segments_by_call_facts_sanitized(text, text, text, text, text, text, text, integer) FROM PUBLIC;
`

const postgresBusinessAnalysisReaderGrantStatementsSQL = `
		EXECUTE 'GRANT EXECUTE ON FUNCTION gongmcp_search_transcript_segments_by_call_facts(text, text, text, text, text, text, text, integer) TO gongmcp_reader';
		EXECUTE 'GRANT EXECUTE ON FUNCTION gongmcp_search_transcript_quotes_with_attribution(text, text, text, text, text, text, text, text, text, text, text, integer) TO gongmcp_reader';
		EXECUTE 'GRANT EXECUTE ON FUNCTION gongmcp_business_analysis_calls(text, text, text, text, text, text, text, text, text, text, text, text, text, text, text, text, boolean, integer) TO gongmcp_reader';
		EXECUTE 'GRANT EXECUTE ON FUNCTION gongmcp_business_analysis_summary(text, text, text, text, text, text, text, text, text, text, text, text, text, text, text, text, boolean) TO gongmcp_reader';
		EXECUTE 'GRANT EXECUTE ON FUNCTION gongmcp_business_analysis_evidence(text, text, text, text, text, text, text, text, text, text, text, text, text, text, text, text, text, boolean, integer) TO gongmcp_reader';
		EXECUTE 'GRANT EXECUTE ON FUNCTION gongmcp_business_analysis_theme_seed_sample(text, text, text, text, text, text, text, text, text, text, text, text, text, text, text, text, boolean, integer) TO gongmcp_reader';
		EXECUTE 'GRANT EXECUTE ON FUNCTION gongmcp_business_analysis_dimension(text, text, text, text, text, text, text, text, text, text, text, text, text, text, text, text, text, text, boolean, integer) TO gongmcp_reader';
`

const postgresBusinessAnalysisReaderGrantsSQL = `
DO $$
BEGIN
	IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'gongmcp_reader') THEN
` + postgresBusinessAnalysisReaderGrantStatementsSQL + `
	END IF;
END;
$$;
`
