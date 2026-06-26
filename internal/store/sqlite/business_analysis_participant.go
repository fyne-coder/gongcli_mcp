package sqlite

import (
	"context"
	"fmt"
	"strings"
)

const (
	ParticipantAffiliationInternal = "internal"
	ParticipantAffiliationExternal = "external"
	ParticipantAffiliationUnknown  = "unknown"
	ParticipantAffiliationMixed    = "mixed"
)

func participantPolicyDimensionCanonical(dimension string) (string, bool) {
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

func normalizeParticipantAffiliationFilter(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "any":
		return "", nil
	case "external", "buyer", "customer", "prospect", "marketing":
		return ParticipantAffiliationExternal, nil
	case "internal", "seller", "rep", "coaching":
		return ParticipantAffiliationInternal, nil
	default:
		return "", fmt.Errorf("participant_affiliation_filter must be one of: any, external, internal")
	}
}

func normalizeInternalParticipantDomains(domains []string) []string {
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

func businessAnalysisParticipantPartyEmailsCTE() string {
	return `,
party_emails AS (
	SELECT call_id, email
	  FROM (
		SELECT f.call_id AS call_id,
		       LOWER(TRIM(COALESCE(json_extract(p.value, '$.emailAddress'), json_extract(p.value, '$.email'), ''))) AS email
		  FROM filtered f
		  CROSS JOIN json_each(f.raw_json, '$.parties') p
		 UNION
		SELECT f.call_id AS call_id,
		       LOWER(TRIM(COALESCE(json_extract(p.value, '$.emailAddress'), json_extract(p.value, '$.email'), ''))) AS email
		  FROM filtered f
		  CROSS JOIN json_each(f.raw_json, '$.metaData.parties') p
	  )
	 WHERE email <> ''
),
party_domains AS (
	SELECT DISTINCT call_id,
	       CASE
		       WHEN instr(email, '@') > 0 THEN lower(trim(substr(email, instr(email, '@') + 1)))
		       ELSE ''
	       END AS domain
	  FROM party_emails
	 WHERE email <> ''
),
distinct_domains AS (
	SELECT DISTINCT call_id, domain
	  FROM party_domains
	 WHERE domain <> ''
)`
}

func businessAnalysisParticipantInternalDomainPlaceholders(domains []string) (string, []any) {
	if len(domains) == 0 {
		return "'__no_internal_domains__'", nil
	}
	placeholders := make([]string, 0, len(domains))
	args := make([]any, 0, len(domains))
	for _, domain := range domains {
		placeholders = append(placeholders, "?")
		args = append(args, domain)
	}
	return strings.Join(placeholders, ","), args
}

func businessAnalysisParticipantCallAffiliationCTE(internalPlaceholders string) string {
	return `,
call_domain_flags AS (
	SELECT call_id,
	       SUM(CASE WHEN domain IN (` + internalPlaceholders + `) THEN 1 ELSE 0 END) AS internal_domain_count,
	       SUM(CASE WHEN domain NOT IN (` + internalPlaceholders + `) THEN 1 ELSE 0 END) AS external_domain_count
	  FROM distinct_domains
	 GROUP BY call_id
),
call_affiliation AS (
	SELECT f.call_id AS call_id,
	       CASE
		       WHEN COALESCE(cdf.internal_domain_count, 0) + COALESCE(cdf.external_domain_count, 0) = 0 THEN '` + ParticipantAffiliationUnknown + `'
		       WHEN COALESCE(cdf.internal_domain_count, 0) > 0 AND COALESCE(cdf.external_domain_count, 0) > 0 THEN '` + ParticipantAffiliationMixed + `'
		       WHEN COALESCE(cdf.internal_domain_count, 0) > 0 THEN '` + ParticipantAffiliationInternal + `'
		       ELSE '` + ParticipantAffiliationExternal + `'
	       END AS affiliation
	  FROM (SELECT DISTINCT call_id FROM filtered) f
	  LEFT JOIN call_domain_flags cdf
	    ON cdf.call_id = f.call_id
)`
}

func businessAnalysisParticipantAffiliationFilterClause(filter string) (string, error) {
	normalized, err := normalizeParticipantAffiliationFilter(filter)
	if err != nil {
		return "", err
	}
	switch normalized {
	case "":
		return "", nil
	case ParticipantAffiliationExternal:
		return `ca.affiliation IN ('` + ParticipantAffiliationExternal + `', '` + ParticipantAffiliationMixed + `')`, nil
	case ParticipantAffiliationInternal:
		return `ca.affiliation = '` + ParticipantAffiliationInternal + `'`, nil
	default:
		return "", fmt.Errorf("unsupported participant affiliation filter %q", filter)
	}
}

func (s *Store) summarizeBusinessAnalysisParticipantDimension(ctx context.Context, filter BusinessAnalysisFilter, requestedDimension string, requestedLimit int, internalDomains []string, affiliationFilter string, from string, where []string, args []any) ([]BusinessAnalysisDimensionRow, error) {
	canonical, ok := participantPolicyDimensionCanonical(requestedDimension)
	if !ok {
		return nil, fmt.Errorf("unsupported participant dimension %q", requestedDimension)
	}
	limit := boundedLimit(firstPositiveInt(requestedLimit, filter.Limit), defaultBusinessAnalysisLimit, maxBusinessAnalysisLimit)
	internalDomains = normalizeInternalParticipantDomains(internalDomains)
	internalPlaceholders, internalArgs := businessAnalysisParticipantInternalDomainPlaceholders(internalDomains)
	affiliationClause, err := businessAnalysisParticipantAffiliationFilterClause(affiliationFilter)
	if err != nil {
		return nil, err
	}

	filteredSQL := `
WITH filtered AS (
	SELECT cf.*,
	       c.raw_json AS raw_json
` + from
	if len(where) > 0 {
		filteredSQL += `
	 WHERE ` + strings.Join(where, ` AND `)
	}
	filteredSQL += `
)` + businessAnalysisParticipantPartyEmailsCTE() + businessAnalysisParticipantCallAffiliationCTE(internalPlaceholders)

	queryArgs := append(append([]any{}, args...), internalArgs...)
	queryArgs = append(queryArgs, internalArgs...)

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
       COALESCE(SUM(CASE WHEN f.transcript_present = 1 THEN 1 ELSE 0 END), 0) AS transcript_count,
       COALESCE(SUM(CASE WHEN f.transcript_present = 0 THEN 1 ELSE 0 END), 0) AS missing_transcript_count,
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
		if strings.EqualFold(strings.TrimSpace(affiliationFilter), ParticipantAffiliationExternal) {
			whereParts = append(whereParts, `pd.domain NOT IN (`+internalPlaceholders+`)`)
			queryArgs = append(queryArgs, internalArgs...)
		}
		if strings.EqualFold(strings.TrimSpace(affiliationFilter), ParticipantAffiliationInternal) {
			whereParts = append(whereParts, `pd.domain IN (`+internalPlaceholders+`)`)
			queryArgs = append(queryArgs, internalArgs...)
		}
		if len(whereParts) > 0 {
			selectSQL += `
 WHERE ` + strings.Join(whereParts, ` AND `)
		}
		groupBy = `
 GROUP BY pd.domain
 ORDER BY call_count DESC, value
 LIMIT ?`
	case "participant_email":
		selectSQL = `
SELECT '` + canonical + `' AS dimension,
       pe.email AS value,
       COUNT(DISTINCT pe.call_id) AS call_count,
       COALESCE(SUM(CASE WHEN f.transcript_present = 1 THEN 1 ELSE 0 END), 0) AS transcript_count,
       COALESCE(SUM(CASE WHEN f.transcript_present = 0 THEN 1 ELSE 0 END), 0) AS missing_transcript_count,
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
		if strings.EqualFold(strings.TrimSpace(affiliationFilter), ParticipantAffiliationExternal) {
			whereParts = append(whereParts, `CASE WHEN instr(pe.email, '@') > 0 THEN lower(trim(substr(pe.email, instr(pe.email, '@') + 1))) ELSE '' END NOT IN (`+internalPlaceholders+`)`)
			queryArgs = append(queryArgs, internalArgs...)
		}
		if strings.EqualFold(strings.TrimSpace(affiliationFilter), ParticipantAffiliationInternal) {
			whereParts = append(whereParts, `CASE WHEN instr(pe.email, '@') > 0 THEN lower(trim(substr(pe.email, instr(pe.email, '@') + 1))) ELSE '' END IN (`+internalPlaceholders+`)`)
			queryArgs = append(queryArgs, internalArgs...)
		}
		if len(whereParts) > 0 {
			selectSQL += `
 WHERE ` + strings.Join(whereParts, ` AND `)
		}
		groupBy = `
 GROUP BY pe.email
 ORDER BY call_count DESC, value
 LIMIT ?`
	case "participant_affiliation":
		selectSQL = `
SELECT '` + canonical + `' AS dimension,
       ca.affiliation AS value,
       COUNT(*) AS call_count,
       COALESCE(SUM(CASE WHEN f.transcript_present = 1 THEN 1 ELSE 0 END), 0) AS transcript_count,
       COALESCE(SUM(CASE WHEN f.transcript_present = 0 THEN 1 ELSE 0 END), 0) AS missing_transcript_count,
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
 LIMIT ?`
	default:
		return nil, fmt.Errorf("unsupported participant dimension %q", requestedDimension)
	}

	sql := filteredSQL + selectSQL + groupBy
	queryArgs = append(queryArgs, limit)

	rows, err := s.db.QueryContext(ctx, sql, queryArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]BusinessAnalysisDimensionRow, 0)
	for rows.Next() {
		var row BusinessAnalysisDimensionRow
		if err := rows.Scan(
			&row.Dimension,
			&row.Value,
			&row.CallCount,
			&row.TranscriptCount,
			&row.MissingTranscriptCount,
			&row.OpportunityCallCount,
			&row.AccountCallCount,
			&row.ExternalCallCount,
			&row.LatestCallAt,
		); err != nil {
			return nil, err
		}
		row.TranscriptCoverageRate = rate(row.TranscriptCount, row.CallCount)
		out = append(out, row)
	}
	return out, rows.Err()
}
