package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *Store) SummarizeCallFacts(ctx context.Context, params CallFactsSummaryParams) ([]CallFactsSummaryRow, error) {
	groupBy, column, err := callFactGroupColumn(params.GroupBy)
	if err != nil {
		return nil, err
	}
	limit := boundedLimit(params.Limit, defaultCallFactsLimit, maxCallFactsLimit)

	where, args, err := callFactsWhere(params)
	if err != nil {
		return nil, err
	}
	groupExpr := `COALESCE(NULLIF(TRIM(` + column + `), ''), '<blank>')`
	query := `
SELECT '` + groupBy + `' AS group_by,
       ` + groupExpr + ` AS group_value,
       COUNT(*) AS call_count,
       SUM(CASE WHEN transcript_present = 1 THEN 1 ELSE 0 END) AS transcript_count,
       SUM(CASE WHEN transcript_present = 0 THEN 1 ELSE 0 END) AS missing_transcript_count,
       SUM(CASE WHEN opportunity_count > 0 THEN 1 ELSE 0 END) AS opportunity_call_count,
       SUM(CASE WHEN account_count > 0 THEN 1 ELSE 0 END) AS account_call_count,
       SUM(CASE WHEN scope = 'External' THEN 1 ELSE 0 END) AS external_call_count,
       SUM(CASE WHEN scope = 'Internal' THEN 1 ELSE 0 END) AS internal_call_count,
       SUM(CASE WHEN scope = 'Unknown' THEN 1 ELSE 0 END) AS unknown_scope_call_count,
       COALESCE(SUM(duration_seconds), 0) AS total_duration_seconds,
       COALESCE(AVG(duration_seconds), 0) AS avg_duration_seconds,
       COALESCE(MAX(started_at), '') AS latest_call_at
  FROM call_facts`
	if len(where) > 0 {
		query += ` WHERE ` + strings.Join(where, ` AND `)
	}
	query += `
 GROUP BY group_value
 ORDER BY call_count DESC, group_value
 LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]CallFactsSummaryRow, 0)
	for rows.Next() {
		var row CallFactsSummaryRow
		if err := rows.Scan(
			&row.GroupBy,
			&row.GroupValue,
			&row.CallCount,
			&row.TranscriptCount,
			&row.MissingTranscriptCount,
			&row.OpportunityCallCount,
			&row.AccountCallCount,
			&row.ExternalCallCount,
			&row.InternalCallCount,
			&row.UnknownScopeCallCount,
			&row.TotalDurationSeconds,
			&row.AvgDurationSeconds,
			&row.LatestCallAt,
		); err != nil {
			return nil, err
		}
		row.TranscriptCoverageRate = rate(row.TranscriptCount, row.CallCount)
		out = append(out, row)
	}
	return out, rows.Err()
}
func (s *Store) CallFactsCoverage(ctx context.Context) (*CallFactsCoverage, error) {
	var coverage CallFactsCoverage
	if err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*) AS total_calls,
       COALESCE(SUM(CASE WHEN transcript_present = 1 THEN 1 ELSE 0 END), 0) AS transcript_count,
       COALESCE(SUM(CASE WHEN transcript_present = 0 THEN 1 ELSE 0 END), 0) AS missing_transcript_count,
       COALESCE(SUM(CASE WHEN opportunity_count > 0 THEN 1 ELSE 0 END), 0) AS opportunity_call_count,
       COALESCE(SUM(CASE WHEN account_count > 0 THEN 1 ELSE 0 END), 0) AS account_call_count,
       COALESCE(SUM(CASE WHEN scope = 'External' THEN 1 ELSE 0 END), 0) AS external_call_count,
       COALESCE(SUM(CASE WHEN scope = 'Internal' THEN 1 ELSE 0 END), 0) AS internal_call_count,
       COALESCE(SUM(CASE WHEN scope = 'Unknown' THEN 1 ELSE 0 END), 0) AS unknown_scope_call_count,
       COALESCE(SUM(CASE WHEN TRIM(purpose) <> '' THEN 1 ELSE 0 END), 0) AS purpose_populated_calls,
       COALESCE(SUM(CASE WHEN calendar_event_present = 1 THEN 1 ELSE 0 END), 0) AS calendar_call_count,
       COALESCE(SUM(duration_seconds), 0) AS total_duration_seconds
  FROM call_facts`).Scan(
		&coverage.TotalCalls,
		&coverage.TranscriptCount,
		&coverage.MissingTranscriptCount,
		&coverage.OpportunityCallCount,
		&coverage.AccountCallCount,
		&coverage.ExternalCallCount,
		&coverage.InternalCallCount,
		&coverage.UnknownScopeCallCount,
		&coverage.PurposePopulatedCalls,
		&coverage.CalendarCallCount,
		&coverage.TotalDurationSeconds,
	); err != nil {
		return nil, err
	}
	coverage.TranscriptCoverageRate = rate(coverage.TranscriptCount, coverage.TotalCalls)
	return &coverage, nil
}
func NormalizeCallFactsGroupBy(groupBy string) (string, error) {
	canonical, _, err := callFactGroupColumn(groupBy)
	return canonical, err
}
func NormalizeScorecardActivityGroupBy(groupBy string) (string, error) {
	canonical, _, err := scorecardActivityGroupColumn(groupBy)
	return canonical, err
}
func scorecardActivityGroupColumn(groupBy string) (string, string, error) {
	switch strings.ToLower(strings.TrimSpace(groupBy)) {
	case "", "scorecard", "scorecard_name":
		return "scorecard", "sa.scorecard_name", nil
	case "review_method", "method":
		return "review_method", "sa.review_method", nil
	case "reviewed_user", "reviewed_user_id":
		return "reviewed_user", "sa.reviewed_user_id", nil
	case "lifecycle", "lifecycle_bucket":
		return "lifecycle", "cf.lifecycle_bucket", nil
	case "transcript_status":
		return "transcript_status", "cf.transcript_status", nil
	default:
		return "", "", fmt.Errorf("unsupported group_by %q", groupBy)
	}
}
func callFactsWhere(params CallFactsSummaryParams) ([]string, []any, error) {
	var where []string
	var args []any
	if value := strings.TrimSpace(params.LifecycleBucket); value != "" {
		if !isKnownLifecycleBucket(value) {
			return nil, nil, fmt.Errorf("unknown lifecycle bucket %q", value)
		}
		where = append(where, `lifecycle_bucket = ?`)
		args = append(args, value)
	}
	if value := strings.TrimSpace(params.Scope); value != "" {
		scope, ok := normalizedScope(value)
		if !ok {
			return nil, nil, fmt.Errorf("scope must be one of: External, Internal, Unknown")
		}
		where = append(where, `scope = ?`)
		args = append(args, scope)
	}
	if value := strings.TrimSpace(params.System); value != "" {
		where = append(where, `system = ?`)
		args = append(args, value)
	}
	if value := strings.TrimSpace(params.Direction); value != "" {
		where = append(where, `direction = ?`)
		args = append(args, value)
	}
	if value := strings.TrimSpace(params.TranscriptStatus); value != "" {
		status, ok := normalizedTranscriptStatus(value)
		if !ok {
			return nil, nil, fmt.Errorf("transcript_status must be one of: present, missing")
		}
		where = append(where, `transcript_status = ?`)
		args = append(args, status)
	}
	return where, args, nil
}

type callFactFilterParams struct {
	FromDate         string
	ToDate           string
	LifecycleBucket  string
	Scope            string
	System           string
	Direction        string
	TranscriptStatus string
}

func callFactFilterWhere(alias string, params callFactFilterParams, allowTranscriptStatus bool) ([]string, []any, error) {
	if strings.TrimSpace(alias) == "" {
		alias = "cf"
	}
	prefix := alias + "."
	var where []string
	var args []any
	var fromDate, toDate string
	if value := strings.TrimSpace(params.FromDate); value != "" {
		date, err := normalizeDateFilter(value, "from_date")
		if err != nil {
			return nil, nil, err
		}
		fromDate = date
		where = append(where, prefix+`call_date >= ?`)
		args = append(args, date)
	}
	if value := strings.TrimSpace(params.ToDate); value != "" {
		date, err := normalizeDateFilter(value, "to_date")
		if err != nil {
			return nil, nil, err
		}
		toDate = date
		where = append(where, prefix+`call_date <= ?`)
		args = append(args, date)
	}
	if fromDate != "" && toDate != "" && fromDate > toDate {
		return nil, nil, errors.New("from_date must be on or before to_date")
	}
	if value := strings.TrimSpace(params.LifecycleBucket); value != "" {
		if !isKnownLifecycleBucket(value) {
			return nil, nil, fmt.Errorf("unknown lifecycle bucket %q", value)
		}
		where = append(where, prefix+`lifecycle_bucket = ?`)
		args = append(args, strings.ToLower(value))
	}
	if value := strings.TrimSpace(params.Scope); value != "" {
		scope, ok := normalizedScope(value)
		if !ok {
			return nil, nil, errors.New("scope must be one of: External, Internal, Unknown")
		}
		where = append(where, prefix+`scope = ?`)
		args = append(args, scope)
	}
	if value := strings.TrimSpace(params.System); value != "" {
		where = append(where, prefix+`system = ?`)
		args = append(args, value)
	}
	if value := strings.TrimSpace(params.Direction); value != "" {
		where = append(where, prefix+`direction = ?`)
		args = append(args, value)
	}
	if value := strings.TrimSpace(params.TranscriptStatus); value != "" && value != "any" {
		if !allowTranscriptStatus {
			return nil, nil, errors.New("transcript_status is not supported for this query")
		}
		status, ok := normalizedTranscriptStatus(value)
		if !ok {
			return nil, nil, errors.New("transcript_status must be one of: present, missing, any")
		}
		where = append(where, prefix+`transcript_status = ?`)
		args = append(args, status)
	}
	return where, args, nil
}
func callFactGroupColumn(groupBy string) (string, string, error) {
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
	case "calendar", "calendar_event_status":
		return "calendar", "calendar_event_status", nil
	case "duration_bucket":
		return "duration_bucket", "duration_bucket", nil
	case "month", "call_month":
		return "month", "call_month", nil
	case "lead_source", "primary_lead_source":
		return "lead_source", "opportunity_primary_lead_source", nil
	case "forecast_category":
		return "forecast_category", "opportunity_forecast_category", nil
	default:
		return "", "", fmt.Errorf("unsupported group_by %q", groupBy)
	}
}
func normalizeDateFilter(value string, fieldName string) (string, error) {
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
func normalizedScope(value string) (string, bool) {
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
func normalizedTranscriptStatus(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "present", "has_transcript", "with_transcript":
		return "present", true
	case "missing", "missing_transcript", "without_transcript":
		return "missing", true
	default:
		return "", false
	}
}
func (s *Store) SyncStatusSummary(ctx context.Context) (*SyncStatusSummary, error) {
	summary := &SyncStatusSummary{}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM calls`).Scan(&summary.TotalCalls); err != nil {
		return nil, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&summary.TotalUsers); err != nil {
		return nil, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM transcripts`).Scan(&summary.TotalTranscripts); err != nil {
		return nil, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM transcript_segments`).Scan(&summary.TotalTranscriptSegments); err != nil {
		return nil, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT call_id) FROM call_context_objects`).Scan(&summary.TotalEmbeddedCRMContextCalls); err != nil {
		return nil, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM call_context_objects`).Scan(&summary.TotalEmbeddedCRMObjects); err != nil {
		return nil, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM call_context_fields`).Scan(&summary.TotalEmbeddedCRMFields); err != nil {
		return nil, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM crm_integrations`).Scan(&summary.TotalCRMIntegrations); err != nil {
		return nil, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM crm_schema_objects`).Scan(&summary.TotalCRMSchemaObjects); err != nil {
		return nil, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM crm_schema_fields`).Scan(&summary.TotalCRMSchemaFields); err != nil {
		return nil, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM gong_settings`).Scan(&summary.TotalGongSettings); err != nil {
		return nil, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM gong_settings WHERE kind = 'scorecards'`).Scan(&summary.TotalScorecards); err != nil {
		return nil, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM scorecard_activity`).Scan(&summary.TotalScorecardActivity); err != nil {
		return nil, err
	}
	if err := s.db.QueryRowContext(
		ctx,
		`SELECT COUNT(*)
		   FROM calls c
		   LEFT JOIN transcripts t ON t.call_id = c.call_id
		  WHERE t.call_id IS NULL`,
	).Scan(&summary.MissingTranscripts); err != nil {
		return nil, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sync_runs WHERE status = 'running'`).Scan(&summary.RunningSyncRuns); err != nil {
		return nil, err
	}
	attributionCoverage, err := s.attributionCoverage(ctx)
	if err != nil {
		return nil, err
	}
	summary.AttributionCoverage = attributionCoverage

	lastRun, err := s.latestSyncRun(ctx, `SELECT id, scope, sync_key, cursor, from_value, to_value, request_context, status, started_at, finished_at, records_seen, records_written, error_text FROM sync_runs ORDER BY started_at DESC, id DESC LIMIT 1`)
	if err != nil {
		return nil, err
	}
	summary.LastRun = lastRun

	lastSuccess, err := s.latestSyncRun(ctx, `SELECT id, scope, sync_key, cursor, from_value, to_value, request_context, status, started_at, finished_at, records_seen, records_written, error_text FROM sync_runs WHERE status = 'success' ORDER BY finished_at DESC, id DESC LIMIT 1`)
	if err != nil {
		return nil, err
	}
	summary.LastSuccessfulRun = lastSuccess

	stateRows, err := s.db.QueryContext(
		ctx,
		`SELECT sync_key, scope, cursor, COALESCE(last_run_id, 0), last_status, last_error, COALESCE(last_success_at, ''), updated_at
		   FROM sync_state
		  ORDER BY scope, sync_key`,
	)
	if err != nil {
		return nil, err
	}
	defer stateRows.Close()

	for stateRows.Next() {
		var row SyncState
		if err := stateRows.Scan(
			&row.SyncKey,
			&row.Scope,
			&row.Cursor,
			&row.LastRunID,
			&row.LastStatus,
			&row.LastError,
			&row.LastSuccessAt,
			&row.UpdatedAt,
		); err != nil {
			return nil, err
		}
		summary.States = append(summary.States, row)
	}
	if err := stateRows.Err(); err != nil {
		return nil, err
	}
	if summary.States == nil {
		summary.States = []SyncState{}
	}
	profileReadiness, err := s.profileReadiness(ctx)
	if err != nil {
		return nil, err
	}
	summary.ProfileReadiness = profileReadiness
	summary.PublicReadiness = buildPublicReadiness(summary)
	return summary, nil
}
func (s *Store) attributionCoverage(ctx context.Context) (AttributionCoverage, error) {
	var coverage AttributionCoverage
	scalars := []struct {
		target *int64
		query  string
	}{
		{&coverage.CallsWithTitles, `SELECT COUNT(*) FROM calls WHERE TRIM(title) <> ''`},
		{&coverage.CallsWithParties, `SELECT COUNT(*) FROM calls WHERE parties_count > 0`},
		{&coverage.CallsWithPartyTitles, `SELECT COUNT(*) FROM calls WHERE
			COALESCE((SELECT COUNT(1) FROM json_each(calls.raw_json, '$.parties') p WHERE TRIM(COALESCE(json_extract(p.value, '$.title'), json_extract(p.value, '$.jobTitle'), json_extract(p.value, '$.job_title'), '')) <> ''), 0) +
			COALESCE((SELECT COUNT(1) FROM json_each(calls.raw_json, '$.metaData.parties') p WHERE TRIM(COALESCE(json_extract(p.value, '$.title'), json_extract(p.value, '$.jobTitle'), json_extract(p.value, '$.job_title'), '')) <> ''), 0) > 0`},
		{&coverage.UsersWithTitles, `SELECT COUNT(*) FROM users WHERE TRIM(title) <> ''`},
		{&coverage.AccountNameCalls, `SELECT COUNT(DISTINCT o.call_id) FROM call_context_objects o JOIN call_context_fields f ON f.call_id = o.call_id AND f.object_key = o.object_key WHERE o.object_type = 'Account' AND f.field_name = 'Name' AND TRIM(f.field_value_text) <> ''`},
		{&coverage.AccountIndustryCalls, `SELECT COUNT(DISTINCT o.call_id) FROM call_context_objects o JOIN call_context_fields f ON f.call_id = o.call_id AND f.object_key = o.object_key WHERE o.object_type = 'Account' AND f.field_name = 'Industry' AND TRIM(f.field_value_text) <> ''`},
		{&coverage.OpportunityStageCalls, `SELECT COUNT(DISTINCT o.call_id) FROM call_context_objects o JOIN call_context_fields f ON f.call_id = o.call_id AND f.object_key = o.object_key WHERE o.object_type = 'Opportunity' AND f.field_name = 'StageName' AND TRIM(f.field_value_text) <> ''`},
		{&coverage.ContactObjectCalls, `SELECT COUNT(DISTINCT call_id) FROM call_context_objects WHERE object_type = 'Contact'`},
		{&coverage.LeadObjectCalls, `SELECT COUNT(DISTINCT call_id) FROM call_context_objects WHERE object_type = 'Lead'`},
		{&coverage.ObjectsWithNames, `SELECT COUNT(*) FROM call_context_objects WHERE TRIM(object_name) <> ''`},
	}
	for _, scalar := range scalars {
		if err := s.db.QueryRowContext(ctx, scalar.query).Scan(scalar.target); err != nil {
			return AttributionCoverage{}, err
		}
	}
	if coverage.CallsWithParties > 0 {
		coverage.ParticipantStatus = "present"
	} else {
		coverage.ParticipantStatus = "missing_from_cache"
	}
	if coverage.CallsWithPartyTitles > 0 {
		coverage.PersonTitleStatus = "party_titles_available"
	} else if coverage.UsersWithTitles > 0 || coverage.ContactObjectCalls > 0 || coverage.LeadObjectCalls > 0 {
		coverage.PersonTitleStatus = "title_source_present_but_call_titles_unverified"
	} else if coverage.CallsWithParties > 0 {
		coverage.PersonTitleStatus = "participants_present_check_party_titles"
	} else {
		coverage.PersonTitleStatus = "missing_from_cache"
	}
	if coverage.CallsWithParties == 0 {
		coverage.RecommendedNextAction = "Re-sync calls with exposed participant fields enabled; sync users for internal titles and Contact/Lead CRM context for durable external person titles."
	}
	return coverage, nil
}
func (s *Store) CacheInventory(ctx context.Context) (*CacheInventory, error) {
	summary, err := s.SyncStatusSummary(ctx)
	if err != nil {
		return nil, err
	}

	out := &CacheInventory{
		Summary:     summary,
		TableCounts: []CacheTableCount{},
	}
	if err := s.db.QueryRowContext(
		ctx,
		`SELECT COALESCE(MIN(started_at), ''), COALESCE(MAX(started_at), '')
		   FROM calls
		  WHERE TRIM(started_at) <> ''`,
	).Scan(&out.OldestCallStartedAt, &out.NewestCallStartedAt); err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(
		ctx,
		`SELECT name, COALESCE(sql, '')
		   FROM sqlite_master
		  WHERE type = 'table'
		    AND name NOT LIKE 'sqlite_%'
		  ORDER BY name`,
	)
	if err != nil {
		return nil, err
	}

	type inventoryTable struct {
		name string
		sql  string
	}
	var tables []inventoryTable
	for rows.Next() {
		var tableName string
		var tableSQL string
		if err := rows.Scan(&tableName, &tableSQL); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if !inventoryTableVisible(tableName, tableSQL) {
			continue
		}
		tables = append(tables, inventoryTable{name: tableName, sql: tableSQL})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for _, table := range tables {
		query := fmt.Sprintf(`SELECT COUNT(*) FROM "%s"`, strings.ReplaceAll(table.name, `"`, `""`))
		var count int64
		if err := s.db.QueryRowContext(ctx, query).Scan(&count); err != nil {
			return nil, err
		}
		out.TableCounts = append(out.TableCounts, CacheTableCount{Table: table.name, Rows: count})
	}
	return out, nil
}
func (s *Store) PlanCachePurgeBefore(ctx context.Context, startedBefore string) (*CachePurgePlan, error) {
	startedBefore = strings.TrimSpace(startedBefore)
	if startedBefore == "" {
		return nil, errors.New("started_before is required")
	}
	plan := &CachePurgePlan{StartedBefore: startedBefore}
	for _, item := range []struct {
		target *int64
		query  string
	}{
		{&plan.CallCount, `SELECT COUNT(*) FROM calls WHERE TRIM(started_at) <> '' AND started_at < ?`},
		{&plan.TranscriptCount, `SELECT COUNT(*) FROM transcripts WHERE call_id IN (SELECT call_id FROM calls WHERE TRIM(started_at) <> '' AND started_at < ?)`},
		{&plan.TranscriptSegmentCount, `SELECT COUNT(*) FROM transcript_segments WHERE call_id IN (SELECT call_id FROM calls WHERE TRIM(started_at) <> '' AND started_at < ?)`},
		{&plan.ContextObjectCount, `SELECT COUNT(*) FROM call_context_objects WHERE call_id IN (SELECT call_id FROM calls WHERE TRIM(started_at) <> '' AND started_at < ?)`},
		{&plan.ContextFieldCount, `SELECT COUNT(*) FROM call_context_fields WHERE call_id IN (SELECT call_id FROM calls WHERE TRIM(started_at) <> '' AND started_at < ?)`},
		{&plan.ProfileCallFactCount, `SELECT COUNT(*) FROM profile_call_fact_cache WHERE call_id IN (SELECT call_id FROM calls WHERE TRIM(started_at) <> '' AND started_at < ?)`},
	} {
		if err := s.db.QueryRowContext(ctx, item.query, startedBefore).Scan(item.target); err != nil {
			return nil, err
		}
	}
	return plan, nil
}
func (s *Store) PurgeCacheBefore(ctx context.Context, startedBefore string) (*CachePurgePlan, error) {
	plan, err := s.PlanCachePurgeBefore(ctx, startedBefore)
	if err != nil {
		return nil, err
	}
	if plan.CallCount == 0 {
		return plan, nil
	}
	if _, err := s.db.ExecContext(ctx, `PRAGMA secure_delete = ON`); err != nil {
		return nil, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	for _, query := range []string{
		`DELETE FROM profile_call_fact_cache WHERE call_id IN (SELECT call_id FROM calls WHERE TRIM(started_at) <> '' AND started_at < ?)`,
		`DELETE FROM transcript_segments WHERE call_id IN (SELECT call_id FROM calls WHERE TRIM(started_at) <> '' AND started_at < ?)`,
		`DELETE FROM transcripts WHERE call_id IN (SELECT call_id FROM calls WHERE TRIM(started_at) <> '' AND started_at < ?)`,
		`DELETE FROM call_context_fields WHERE call_id IN (SELECT call_id FROM calls WHERE TRIM(started_at) <> '' AND started_at < ?)`,
		`DELETE FROM call_context_objects WHERE call_id IN (SELECT call_id FROM calls WHERE TRIM(started_at) <> '' AND started_at < ?)`,
		`DELETE FROM calls WHERE TRIM(started_at) <> '' AND started_at < ?`,
	} {
		if _, err := tx.ExecContext(ctx, query, plan.StartedBefore); err != nil {
			return nil, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO transcript_segments_fts(transcript_segments_fts) VALUES ('optimize')`); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if err := s.compactAfterPurge(ctx); err != nil {
		return nil, err
	}
	return plan, nil
}
func (s *Store) compactAfterPurge(ctx context.Context) error {
	for _, query := range []string{
		`PRAGMA wal_checkpoint(TRUNCATE)`,
		`VACUUM`,
		`PRAGMA optimize`,
	} {
		if _, err := s.db.ExecContext(ctx, query); err != nil {
			return err
		}
	}
	return nil
}
func inventoryTableVisible(name string, tableSQL string) bool {
	lowerName := strings.ToLower(strings.TrimSpace(name))
	if strings.HasPrefix(lowerName, "transcript_segments_fts_") {
		return false
	}
	if strings.Contains(strings.ToUpper(tableSQL), "VIRTUAL TABLE") {
		return false
	}
	return true
}
func (s *Store) profileReadiness(ctx context.Context) (ProfileReadiness, error) {
	readiness := ProfileReadiness{
		Status:      "not_configured",
		Detail:      "No active business profile is imported. Builtin lifecycle buckets are available, but reliable tenant-specific sales-vs-post-sales separation requires a reviewed profile.",
		CacheStatus: "not_applicable",
		Blocking:    []string{"run gongctl profile discover, review the YAML, then run profile validate and profile import"},
	}
	meta, p, warnings, err := s.activeProfile(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return readiness, nil
	}
	if err != nil {
		return readiness, err
	}
	readiness.Active = true
	readiness.Blocking = nil
	readiness.Name = meta.Name
	readiness.Version = meta.Version
	readiness.CanonicalSHA256 = meta.CanonicalSHA256
	readiness.ObjectConceptCount = len(p.Objects)
	readiness.FieldConceptCount = len(p.Fields)
	readiness.LifecycleBucketCount = len(p.Lifecycle)
	readiness.MethodologyConceptCount = len(p.Methodology)
	readiness.WarningCount = len(warnings)
	readiness.UnavailableConcepts = profileUnavailableConcepts(p, "")

	fingerprint, err := s.profileDataFingerprint(ctx)
	if err != nil {
		return readiness, err
	}
	var cachedCanonical string
	var cachedFingerprint string
	if err := s.db.QueryRowContext(ctx, `
SELECT canonical_sha256, data_fingerprint
  FROM profile_call_fact_cache_meta
 WHERE profile_id = ?`, meta.ID).Scan(&cachedCanonical, &cachedFingerprint); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			readiness.CacheStatus = "missing"
			readiness.Blocking = append(readiness.Blocking, "run gongctl sync status --db PATH or another writable profile-aware CLI command to warm the profile cache")
		} else {
			return readiness, err
		}
	} else if cachedCanonical == meta.CanonicalSHA256 && cachedFingerprint == fingerprint {
		readiness.CacheFresh = true
		readiness.CacheStatus = "fresh"
	} else {
		readiness.CacheStatus = "stale"
		readiness.Blocking = append(readiness.Blocking, "run gongctl sync status --db PATH to refresh the profile cache after sync/profile changes")
	}

	switch {
	case len(readiness.Blocking) > 0:
		readiness.Status = "needs_action"
		readiness.Detail = "An active business profile exists, but profile-aware analysis needs cache or mapping cleanup before broad business use."
	case len(readiness.UnavailableConcepts) > 0 || len(warnings) > 0:
		readiness.Status = "partial"
		readiness.Detail = "An active business profile is available, with warnings or unavailable concepts to review."
	default:
		readiness.Status = "ready"
		readiness.Detail = "An active reviewed business profile and fresh read model are available for profile-aware analysis."
	}
	return readiness, nil
}
func buildPublicReadiness(summary *SyncStatusSummary) PublicReadiness {
	out := PublicReadiness{
		ConversationVolume: readinessFlag(summary.TotalCalls > 0, "ready", "blocked", "Cached call metadata is available for aggregate conversation volume questions.", "Sync calls first with gongctl sync calls --preset business."),
		TranscriptCoverage: transcriptCoverageReadiness(summary),
		ScorecardThemes:    readinessFlag(summary.TotalScorecards > 0, "ready", "needs_settings", "Cached scorecards can support coaching-theme and QA inventory questions.", "Run gongctl sync settings to cache scorecard definitions."),
	}
	if summary.TotalEmbeddedCRMFields > 0 {
		out.CRMSegmentation = ReadinessFlag{
			Ready:  true,
			Status: "ready",
			Detail: "Embedded CRM context from synced calls is available for metadata-only segmentation, even if CRM integration/schema inventory has not been synced.",
		}
		out.AttributionReadiness = ReadinessFlag{
			Ready:  summary.ProfileReadiness.Active && summary.ProfileReadiness.Status == "ready",
			Status: "partial",
			Detail: "Embedded CRM context is available for attribution-readiness checks; tenant-specific field mapping may still be needed for precise attribution concepts.",
			Requirements: []string{
				"review unmapped CRM fields and import a business profile for tenant-specific attribution concepts",
			},
		}
		if out.AttributionReadiness.Ready {
			out.AttributionReadiness.Status = "ready"
			out.AttributionReadiness.Detail = "Embedded CRM context and a ready active profile are available for attribution-readiness checks."
			out.AttributionReadiness.Requirements = nil
		}
	} else {
		out.CRMSegmentation = readinessFlag(false, "ready", "needs_crm_context", "Embedded CRM context is available for metadata-only segmentation.", "Run gongctl sync calls --preset business so call CRM context is cached.")
		out.AttributionReadiness = readinessFlag(false, "ready", "needs_crm_context", "Attribution readiness needs cached CRM context before field availability can be assessed.", "Run gongctl sync calls --preset business and then inspect CRM/profile readiness.")
	}

	if summary.ProfileReadiness.Active && summary.ProfileReadiness.Status == "ready" {
		out.LifecycleSeparation = ReadinessFlag{
			Ready:  true,
			Status: "ready",
			Detail: "A reviewed active profile is available for tenant-specific lifecycle separation.",
		}
	} else if summary.TotalEmbeddedCRMFields > 0 {
		out.LifecycleSeparation = ReadinessFlag{
			Ready:  false,
			Status: "partial",
			Detail: "Builtin lifecycle buckets can separate some sales/post-sales patterns, but reliable tenant-specific separation needs a reviewed active profile.",
			Requirements: []string{
				"run gongctl profile discover",
				"review/edit the generated YAML",
				"run gongctl profile validate and profile import",
			},
		}
	} else {
		out.LifecycleSeparation = readinessFlag(false, "ready", "needs_crm_context", "Lifecycle separation needs CRM context and, for tenant-specific accuracy, an imported profile.", "Sync calls with --preset business and import a reviewed profile.")
	}

	if summary.TotalEmbeddedCRMFields > 0 && (summary.TotalCRMIntegrations == 0 || summary.TotalCRMSchemaFields == 0) {
		out.CRMInventoryNote = "Embedded CRM context from call sync is present separately from CRM integration/schema inventory. Zero CRM integrations or schema fields does not mean call CRM context is missing."
	}
	out.RecommendedNextAction = recommendedNextAction(summary, out)
	return out
}
func readinessFlag(ready bool, readyStatus string, blockedStatus string, readyDetail string, requirement string) ReadinessFlag {
	if ready {
		return ReadinessFlag{Ready: true, Status: readyStatus, Detail: readyDetail}
	}
	return ReadinessFlag{Ready: false, Status: blockedStatus, Detail: requirement, Requirements: []string{requirement}}
}
func transcriptCoverageReadiness(summary *SyncStatusSummary) ReadinessFlag {
	switch {
	case summary.TotalCalls == 0:
		return readinessFlag(false, "ready", "blocked", "Transcript coverage can be analyzed from cached transcripts and missing-transcript counts.", "Sync calls before assessing transcript coverage.")
	case summary.MissingTranscripts == 0:
		return ReadinessFlag{Ready: true, Status: "ready", Detail: "Every cached call has a cached transcript."}
	case summary.TotalTranscripts > 0:
		return ReadinessFlag{
			Ready:  false,
			Status: "partial",
			Detail: "Some transcripts are cached, but transcript coverage is incomplete; use transcript-backlog ranking before content-level analysis.",
			Requirements: []string{
				"run gongctl analyze transcript-backlog",
				"sync the highest-priority missing transcripts",
			},
		}
	default:
		return readinessFlag(false, "ready", "needs_transcripts", "Transcript coverage can be analyzed from cached transcripts and missing-transcript counts.", "Sync transcripts before asking content-level or sampling questions.")
	}
}
func recommendedNextAction(summary *SyncStatusSummary, readiness PublicReadiness) string {
	switch {
	case summary.TotalCalls == 0:
		return "Run gongctl sync calls --preset business to cache call metadata and embedded CRM context."
	case summary.TotalEmbeddedCRMFields == 0:
		return "Re-run call sync with --preset business so embedded CRM context is available for segmentation and lifecycle readiness."
	case !readiness.TranscriptCoverage.Ready:
		return "Use gongctl analyze transcript-backlog to prioritize External/Conference customer conversations before broad transcript sync."
	case summary.TotalScorecards == 0:
		return "Run gongctl sync settings to cache scorecards, trackers, and workspaces for enablement/readiness questions."
	case !summary.ProfileReadiness.Active:
		return "Run gongctl profile discover, review the starter YAML, then validate and import a tenant profile."
	case !summary.ProfileReadiness.CacheFresh:
		return "Run gongctl sync status --db PATH to refresh the active profile read model."
	default:
		return "The cache is ready for aggregate business questions; use analyze coverage, analyze calls, and MCP aggregate tools."
	}
}
