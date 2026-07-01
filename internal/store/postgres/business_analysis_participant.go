package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
)

func (s *Store) summarizeBusinessAnalysisParticipantDimension(ctx context.Context, filter sqlite.BusinessAnalysisFilter, requestedDimension string, themeQuery string, requestedLimit int, internalDomains []string, affiliationFilter string) ([]sqlite.BusinessAnalysisDimensionRow, error) {
	canonical, ok := sqliteParticipantPolicyDimensionCanonical(requestedDimension)
	if !ok {
		return nil, fmt.Errorf("unsupported participant dimension %q", requestedDimension)
	}
	filter, err := s.normalizePostgresBusinessAnalysisFilter(filter)
	if err != nil {
		return nil, err
	}
	themeQuery = strings.TrimSpace(themeQuery)
	if err := validatePostgresBusinessAnalysisSearchText(themeQuery, "theme_query"); err != nil {
		return nil, err
	}
	excludeBucketsJSON, err := postgresBusinessAnalysisExcludeBucketsArg(filter)
	if err != nil {
		return nil, err
	}
	dimensionFiltersJSON, err := postgresBusinessAnalysisDimensionFiltersArg(filter)
	if err != nil {
		return nil, err
	}
	limit := boundedLimit(firstPositivePostgres(requestedLimit, filter.Limit), defaultBusinessAnalysisLimit, maxBusinessAnalysisLimit)
	internalDomains = sqliteNormalizeInternalParticipantDomains(internalDomains)
	internalDomainsJSON, err := postgresInternalParticipantDomainsArg(internalDomains)
	if err != nil {
		return nil, err
	}
	affiliationClause, err := sqliteParticipantAffiliationFilterClause(affiliationFilter)
	if err != nil {
		return nil, err
	}

	filteredSQL := `
WITH dimension_filters AS MATERIALIZED (
	SELECT dimension, operator, values_json
	  FROM gongmcp_business_analysis_normalized_dimension_filters($19)
),
dimension_filter_mode AS MATERIALIZED (
	SELECT COALESCE(NULLIF($19, ''), '[]') = '[]' AS dimension_filters_empty,
	       NOT EXISTS (SELECT 1 FROM dimension_filters WHERE dimension IS DISTINCT FROM 'duration_seconds') AS duration_filters_only
),
filtered AS (
	SELECT cf.*,
	       c.raw_json AS raw_json
	  FROM call_facts cf
	  JOIN calls c
	    ON c.call_id = cf.call_id
	  CROSS JOIN dimension_filter_mode dfm
	 WHERE ($1 = '' OR LOWER(cf.title) LIKE '%' || LOWER(left($1, 160)) || '%')
	   AND ($2 = '' OR EXISTS (SELECT 1 FROM transcript_segments theme_ts WHERE theme_ts.call_id = cf.call_id AND theme_ts.search_vector @@ websearch_to_tsquery('simple', left($2, 160))))
	   AND ($3 = '' OR EXISTS (SELECT 1 FROM transcript_segments query_ts WHERE query_ts.call_id = cf.call_id AND query_ts.search_vector @@ websearch_to_tsquery('simple', left($3, 160))))
	   AND ($4 = '' OR cf.call_date >= $4)
	   AND ($5 = '' OR cf.call_date <= $5)
	   AND ($6 = '' OR cf.lifecycle_bucket = $6)
	   AND (COALESCE(NULLIF($7, ''), '[]') = '[]' OR NOT EXISTS (SELECT 1 FROM jsonb_array_elements_text(COALESCE(NULLIF($7, ''), '[]')::jsonb) excluded(value) WHERE LOWER(TRIM(excluded.value)) = cf.lifecycle_bucket))
	   AND CASE
		   WHEN dfm.dimension_filters_empty THEN true
		   WHEN dfm.duration_filters_only THEN NOT EXISTS (
			   SELECT 1
			     FROM dimension_filters df
			    WHERE df.values_json IS NULL
			       OR jsonb_typeof(df.values_json) <> 'array'
			       OR jsonb_array_length(df.values_json) = 0
			       OR (df.operator = 'equals' AND jsonb_array_length(df.values_json) <> 1)
			       OR (df.operator = 'between' AND jsonb_array_length(df.values_json) <> 2)
			       OR df.operator NOT IN ('equals', 'in', 'gte', 'lte', 'between')
			       OR cf.duration_seconds IS NULL
			       OR (
				       (df.operator = 'equals' AND cf.duration_seconds <> (df.values_json->>0)::bigint)
				    OR (df.operator = 'gte' AND cf.duration_seconds < (df.values_json->>0)::bigint)
				    OR (df.operator = 'lte' AND cf.duration_seconds > (df.values_json->>0)::bigint)
				    OR (df.operator = 'between' AND (
					       cf.duration_seconds < LEAST((df.values_json->>0)::bigint, (df.values_json->>1)::bigint)
					    OR cf.duration_seconds > GREATEST((df.values_json->>0)::bigint, (df.values_json->>1)::bigint)
				       ))
				    OR (df.operator = 'in' AND NOT EXISTS (
					       SELECT 1
					         FROM jsonb_array_elements_text(df.values_json) AS values(value)
					        WHERE cf.duration_seconds = values.value::bigint
				       ))
			       )
		   )
		   ELSE gongmcp_business_analysis_dimension_filters_match($19, cf.account_revenue_range, cf.account_type, cf.account_industry, cf.opportunity_stage, cf.opportunity_type, cf.opportunity_forecast_category, cf.scope, cf.system, cf.direction, cf.transcript_status, cf.lifecycle_bucket, cf.call_month, cf.call_date, cf.duration_seconds, cf.call_id)
	   END
	   AND (NOT COALESCE($8::boolean, false) OR NOT cf.likely_voicemail_or_ivr)
	   AND ($9 = '' OR cf.scope = $9)
	   AND ($10 = '' OR cf.system = $10)
	   AND ($11 = '' OR cf.direction = $11)
	   AND ($12 = '' OR cf.transcript_status = $12)
	   AND ($13 = '' OR LOWER(cf.account_industry) LIKE '%' || LOWER(left($13, 160)) || '%')
	   AND ($14 = '' OR EXISTS (SELECT 1 FROM call_context_objects account_o WHERE account_o.call_id = cf.call_id AND account_o.object_type = 'Account' AND LOWER(TRIM(account_o.object_name)) LIKE '%' || LOWER(left($14, 160)) || '%'))
	   AND ($15 = '' OR LOWER(cf.opportunity_stage) LIKE '%' || LOWER(left($15, 160)) || '%')
	   AND (($16 = '' AND $17 = '') OR EXISTS (SELECT 1 FROM call_context_objects filter_o WHERE filter_o.call_id = cf.call_id AND ($16 = '' OR filter_o.object_type = $16) AND ($17 = '' OR filter_o.object_id = $17)))
	   AND ($18 = '' OR
		       EXISTS (
			       SELECT 1
			         FROM calls filter_c
			        WHERE filter_c.call_id = cf.call_id
			          AND (
			              EXISTS (SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(filter_c.raw_json->'parties') = 'array' THEN filter_c.raw_json->'parties' ELSE '[]'::jsonb END) p WHERE LOWER(TRIM(COALESCE(p.value->>'title', p.value->>'jobTitle', p.value->>'job_title', ''))) LIKE '%' || LOWER(left($18, 160)) || '%')
			           OR EXISTS (SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(filter_c.raw_json->'metaData'->'parties') = 'array' THEN filter_c.raw_json->'metaData'->'parties' ELSE '[]'::jsonb END) p WHERE LOWER(TRIM(COALESCE(p.value->>'title', p.value->>'jobTitle', p.value->>'job_title', ''))) LIKE '%' || LOWER(left($18, 160)) || '%')
			          )
		       )
		       OR EXISTS (SELECT 1 FROM call_context_objects po JOIN call_context_fields pf ON pf.call_id = po.call_id AND pf.object_key = po.object_key WHERE po.call_id = cf.call_id AND po.object_type IN ('Contact', 'Lead') AND pf.field_name IN ('Title', 'JobTitle', 'Job_Title__c', 'JobTitle__c') AND LOWER(TRIM(pf.field_value_text)) LIKE '%' || LOWER(left($18, 160)) || '%'))
),
party_emails AS (
	SELECT call_id, email
	  FROM (
		SELECT f.call_id AS call_id,
		       LOWER(TRIM(COALESCE(p.value->>'emailAddress', p.value->>'email', ''))) AS email
		  FROM filtered f
		  CROSS JOIN LATERAL jsonb_array_elements(CASE WHEN jsonb_typeof(f.raw_json->'parties') = 'array' THEN f.raw_json->'parties' ELSE '[]'::jsonb END) p(value)
		 UNION
		SELECT f.call_id AS call_id,
		       LOWER(TRIM(COALESCE(p.value->>'emailAddress', p.value->>'email', ''))) AS email
		  FROM filtered f
		  CROSS JOIN LATERAL jsonb_array_elements(CASE WHEN jsonb_typeof(f.raw_json->'metaData'->'parties') = 'array' THEN f.raw_json->'metaData'->'parties' ELSE '[]'::jsonb END) p(value)
	  ) emails
	 WHERE email <> ''
),
party_domains AS (
	SELECT DISTINCT call_id,
	       CASE
		       WHEN position('@' in email) > 0 THEN lower(trim(split_part(email, '@', 2)))
		       ELSE ''
	       END AS domain
	  FROM party_emails
	 WHERE email <> ''
),
distinct_domains AS (
	SELECT DISTINCT call_id, domain
	  FROM party_domains
	 WHERE domain <> ''
),
call_domain_flags AS (
	SELECT call_id,
	       SUM(CASE WHEN EXISTS (
		       SELECT 1
			 FROM jsonb_array_elements_text(COALESCE(NULLIF($20, ''), '[]')::jsonb) internal(value)
			WHERE lower(trim(internal.value)) = domain
	       ) THEN 1 ELSE 0 END) AS internal_domain_count,
	       SUM(CASE WHEN NOT EXISTS (
		       SELECT 1
			 FROM jsonb_array_elements_text(COALESCE(NULLIF($20, ''), '[]')::jsonb) internal(value)
			WHERE lower(trim(internal.value)) = domain
	       ) THEN 1 ELSE 0 END) AS external_domain_count
	  FROM distinct_domains
	 GROUP BY call_id
),
call_affiliation AS (
	SELECT f.call_id AS call_id,
	       CASE
		       WHEN COALESCE(cdf.internal_domain_count, 0) + COALESCE(cdf.external_domain_count, 0) = 0 THEN 'unknown'
		       WHEN COALESCE(cdf.internal_domain_count, 0) > 0 AND COALESCE(cdf.external_domain_count, 0) > 0 THEN 'mixed'
		       WHEN COALESCE(cdf.internal_domain_count, 0) > 0 THEN 'internal'
		       ELSE 'external'
	       END AS affiliation
	  FROM (SELECT DISTINCT call_id FROM filtered) f
	  LEFT JOIN call_domain_flags cdf
	    ON cdf.call_id = f.call_id
)`

	var (
		selectSQL string
		groupBy   string
	)
	switch canonical {
	case "participant_domain":
		selectSQL = `
SELECT '` + canonical + `' AS dimension,
       pd.domain AS value,
       COUNT(DISTINCT pd.call_id) AS call_count,
       COALESCE(SUM(CASE WHEN f.transcript_status = 'present' THEN 1 ELSE 0 END), 0) AS transcript_count,
       COALESCE(SUM(CASE WHEN f.transcript_status = 'missing' THEN 1 ELSE 0 END), 0) AS missing_transcript_count,
       COALESCE(SUM(CASE WHEN f.opportunity_count > 0 THEN 1 ELSE 0 END), 0) AS opportunity_call_count,
       COALESCE(SUM(CASE WHEN f.account_count > 0 THEN 1 ELSE 0 END), 0) AS account_call_count,
       COALESCE(SUM(CASE WHEN f.scope = 'External' THEN 1 ELSE 0 END), 0) AS external_call_count,
       COALESCE(MAX(f.started_at), '') AS latest_call_at
  FROM distinct_domains pd
  JOIN filtered f
    ON f.call_id = pd.call_id
  JOIN call_affiliation ca
    ON ca.call_id = pd.call_id`
		whereParts := []string{}
		if affiliationClause != "" {
			whereParts = append(whereParts, affiliationClause)
		}
		if strings.EqualFold(strings.TrimSpace(affiliationFilter), "external") {
			whereParts = append(whereParts, `NOT EXISTS (SELECT 1 FROM jsonb_array_elements_text(COALESCE(NULLIF($20, ''), '[]')::jsonb) internal(value) WHERE lower(trim(internal.value)) = pd.domain)`)
		}
		if strings.EqualFold(strings.TrimSpace(affiliationFilter), "internal") {
			whereParts = append(whereParts, `EXISTS (SELECT 1 FROM jsonb_array_elements_text(COALESCE(NULLIF($20, ''), '[]')::jsonb) internal(value) WHERE lower(trim(internal.value)) = pd.domain)`)
		}
		if len(whereParts) > 0 {
			selectSQL += `
 WHERE ` + strings.Join(whereParts, ` AND `)
		}
		groupBy = `
 GROUP BY pd.domain
 ORDER BY call_count DESC, value
 LIMIT $21`
	case "participant_email":
		selectSQL = `
SELECT '` + canonical + `' AS dimension,
       pe.email AS value,
       COUNT(DISTINCT pe.call_id) AS call_count,
       COALESCE(SUM(CASE WHEN f.transcript_status = 'present' THEN 1 ELSE 0 END), 0) AS transcript_count,
       COALESCE(SUM(CASE WHEN f.transcript_status = 'missing' THEN 1 ELSE 0 END), 0) AS missing_transcript_count,
       COALESCE(SUM(CASE WHEN f.opportunity_count > 0 THEN 1 ELSE 0 END), 0) AS opportunity_call_count,
       COALESCE(SUM(CASE WHEN f.account_count > 0 THEN 1 ELSE 0 END), 0) AS account_call_count,
       COALESCE(SUM(CASE WHEN f.scope = 'External' THEN 1 ELSE 0 END), 0) AS external_call_count,
       COALESCE(MAX(f.started_at), '') AS latest_call_at
  FROM party_emails pe
  JOIN filtered f
    ON f.call_id = pe.call_id
  JOIN call_affiliation ca
    ON ca.call_id = pe.call_id`
		whereParts := []string{}
		if affiliationClause != "" {
			whereParts = append(whereParts, affiliationClause)
		}
		if strings.EqualFold(strings.TrimSpace(affiliationFilter), "external") {
			whereParts = append(whereParts, `NOT EXISTS (SELECT 1 FROM jsonb_array_elements_text(COALESCE(NULLIF($20, ''), '[]')::jsonb) internal(value) WHERE lower(trim(internal.value)) = CASE WHEN position('@' in pe.email) > 0 THEN lower(trim(split_part(pe.email, '@', 2))) ELSE '' END)`)
		}
		if strings.EqualFold(strings.TrimSpace(affiliationFilter), "internal") {
			whereParts = append(whereParts, `EXISTS (SELECT 1 FROM jsonb_array_elements_text(COALESCE(NULLIF($20, ''), '[]')::jsonb) internal(value) WHERE lower(trim(internal.value)) = CASE WHEN position('@' in pe.email) > 0 THEN lower(trim(split_part(pe.email, '@', 2))) ELSE '' END)`)
		}
		if len(whereParts) > 0 {
			selectSQL += `
 WHERE ` + strings.Join(whereParts, ` AND `)
		}
		groupBy = `
 GROUP BY pe.email
 ORDER BY call_count DESC, value
 LIMIT $21`
	case "participant_affiliation":
		selectSQL = `
SELECT '` + canonical + `' AS dimension,
       ca.affiliation AS value,
       COUNT(*) AS call_count,
       COALESCE(SUM(CASE WHEN f.transcript_status = 'present' THEN 1 ELSE 0 END), 0) AS transcript_count,
       COALESCE(SUM(CASE WHEN f.transcript_status = 'missing' THEN 1 ELSE 0 END), 0) AS missing_transcript_count,
       COALESCE(SUM(CASE WHEN f.opportunity_count > 0 THEN 1 ELSE 0 END), 0) AS opportunity_call_count,
       COALESCE(SUM(CASE WHEN f.account_count > 0 THEN 1 ELSE 0 END), 0) AS account_call_count,
       COALESCE(SUM(CASE WHEN f.scope = 'External' THEN 1 ELSE 0 END), 0) AS external_call_count,
       COALESCE(MAX(f.started_at), '') AS latest_call_at
  FROM call_affiliation ca
  JOIN filtered f
    ON f.call_id = ca.call_id`
		if affiliationClause != "" {
			selectSQL += `
 WHERE ` + affiliationClause
		}
		groupBy = `
 GROUP BY ca.affiliation
 ORDER BY call_count DESC, value
 LIMIT $21`
	default:
		return nil, fmt.Errorf("unsupported participant dimension %q", requestedDimension)
	}

	sql := filteredSQL + selectSQL + groupBy
	queryArgs := []any{
		filter.TitleQuery,
		themeQuery,
		filter.Query,
		filter.FromDate,
		filter.ToDate,
		filter.LifecycleBucket,
		excludeBucketsJSON,
		filter.ExcludeLikelyVoicemail,
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
		dimensionFiltersJSON,
		internalDomainsJSON,
		limit,
	}

	rows, err := s.db.QueryContext(ctx, sql, queryArgs...)
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

func sqliteParticipantPolicyDimensionCanonical(dimension string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(dimension)) {
	case "participant_domain", "domain", "email_domain":
		return "participant_domain", true
	case "participant_email":
		return "participant_email", true
	case "participant_affiliation", "participant_affiliation_class":
		return "participant_affiliation", true
	default:
		return "", false
	}
}

func sqliteNormalizeInternalParticipantDomains(domains []string) []string {
	if len(domains) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(domains))
	out := make([]string, 0, len(domains))
	for _, raw := range domains {
		domain := strings.ToLower(strings.TrimSpace(raw))
		domain = strings.TrimPrefix(domain, "@")
		if domain == "" {
			continue
		}
		if _, ok := seen[domain]; ok {
			continue
		}
		seen[domain] = struct{}{}
		out = append(out, domain)
	}
	return out
}

func sqliteParticipantAffiliationFilterClause(filter string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(filter)) {
	case "", "any":
		return "", nil
	case "external", "buyer", "customer", "prospect", "marketing":
		return `ca.affiliation IN ('external', 'mixed')`, nil
	case "internal", "seller", "rep", "coaching":
		return `ca.affiliation = 'internal'`, nil
	default:
		return "", fmt.Errorf("participant_affiliation_filter must be one of: any, external, internal")
	}
}

func postgresInternalParticipantDomainsArg(domains []string) (string, error) {
	if len(domains) == 0 {
		return "[]", nil
	}
	raw, err := json.Marshal(domains)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
