package postgres

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
)

const postgresScorecardActivityMigrationSQL = `
CREATE TABLE IF NOT EXISTS scorecard_activity (
	answered_scorecard_id TEXT PRIMARY KEY,
	scorecard_id TEXT NOT NULL DEFAULT '',
	scorecard_name TEXT NOT NULL DEFAULT '',
	call_id TEXT NOT NULL DEFAULT '',
	call_started_at TEXT NOT NULL DEFAULT '',
	reviewed_user_id TEXT NOT NULL DEFAULT '',
	reviewer_user_id TEXT NOT NULL DEFAULT '',
	editor_user_id TEXT NOT NULL DEFAULT '',
	review_method TEXT NOT NULL DEFAULT '',
	review_time TEXT NOT NULL DEFAULT '',
	visibility_type TEXT NOT NULL DEFAULT '',
	overall_score DOUBLE PRECISION NOT NULL DEFAULT 0,
	average_score DOUBLE PRECISION NOT NULL DEFAULT 0,
	answer_count BIGINT NOT NULL DEFAULT 0,
	raw_json JSONB NOT NULL,
	raw_sha256 TEXT NOT NULL,
	first_seen_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_pg_scorecard_activity_scorecard
	ON scorecard_activity(scorecard_id, review_time DESC);
CREATE INDEX IF NOT EXISTS idx_pg_scorecard_activity_call
	ON scorecard_activity(call_id);
CREATE INDEX IF NOT EXISTS idx_pg_scorecard_activity_review_method
	ON scorecard_activity(review_method);
CREATE INDEX IF NOT EXISTS idx_pg_scorecard_activity_reviewed_user
	ON scorecard_activity(reviewed_user_id);
`

const postgresScorecardActivityFunctionsSQL = `
CREATE OR REPLACE FUNCTION gongmcp_scorecard_activity_summary(group_by_arg text, row_limit integer)
RETURNS TABLE(
	group_by text,
	group_value text,
	answered_scorecard_count bigint,
	distinct_call_count bigint,
	linked_call_count bigint,
	reviewed_user_count bigint,
	manual_count bigint,
	automatic_count bigint,
	transcript_count bigint,
	missing_transcript_count bigint,
	average_overall_score double precision,
	average_answer_score double precision,
	latest_review_time text
)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = public, pg_temp
AS $function$
WITH params AS (
	SELECT CASE lower(trim(COALESCE(group_by_arg, '')))
		WHEN '' THEN 'scorecard'
		WHEN 'scorecard_name' THEN 'scorecard'
		WHEN 'scorecard' THEN 'scorecard'
		WHEN 'method' THEN 'review_method'
		WHEN 'review_method' THEN 'review_method'
		WHEN 'lifecycle_bucket' THEN 'lifecycle'
		WHEN 'lifecycle' THEN 'lifecycle'
		WHEN 'transcript_status' THEN 'transcript_status'
		ELSE '__unsupported__'
	END AS group_by
),
base AS (
	SELECT p.group_by,
	       CASE p.group_by
			WHEN 'scorecard' THEN sa.scorecard_name
			WHEN 'review_method' THEN sa.review_method
			WHEN 'lifecycle' THEN cf.lifecycle_bucket
			WHEN 'transcript_status' THEN cf.transcript_status
			ELSE ''
	       END AS raw_group_value,
	       sa.answered_scorecard_id,
	       sa.call_id,
	       c.call_id AS linked_call_id,
	       sa.reviewed_user_id,
	       sa.review_method,
	       cf.transcript_status,
	       sa.overall_score,
	       sa.average_score,
	       sa.review_time
	  FROM scorecard_activity sa
	  CROSS JOIN params p
	  LEFT JOIN calls c
	    ON c.call_id = sa.call_id
	  LEFT JOIN call_facts cf
	    ON cf.call_id = sa.call_id
	 WHERE p.group_by <> '__unsupported__'
)
SELECT group_by,
       COALESCE(NULLIF(TRIM(raw_group_value), ''), 'unknown') AS group_value,
       COUNT(*) AS answered_scorecard_count,
       COUNT(DISTINCT NULLIF(call_id, '')) AS distinct_call_count,
       COUNT(DISTINCT linked_call_id) AS linked_call_count,
       COUNT(DISTINCT NULLIF(reviewed_user_id, '')) AS reviewed_user_count,
       COALESCE(SUM(CASE WHEN review_method = 'MANUAL' THEN 1 ELSE 0 END), 0) AS manual_count,
       COALESCE(SUM(CASE WHEN review_method = 'AUTOMATIC' THEN 1 ELSE 0 END), 0) AS automatic_count,
       COUNT(DISTINCT CASE WHEN transcript_status = 'present' THEN call_id END) AS transcript_count,
       COUNT(DISTINCT CASE WHEN transcript_status = 'missing' THEN call_id END) AS missing_transcript_count,
       COALESCE(AVG(NULLIF(overall_score, 0)), 0) AS average_overall_score,
       COALESCE(AVG(NULLIF(average_score, 0)), 0) AS average_answer_score,
       COALESCE(MAX(review_time), '') AS latest_review_time
  FROM base
 GROUP BY group_by, group_value
 ORDER BY answered_scorecard_count DESC, group_value
 LIMIT LEAST(GREATEST(COALESCE(row_limit, 50), 1), 1000)
$function$;

CREATE OR REPLACE FUNCTION gongmcp_scorecard_activity_totals()
RETURNS TABLE(
	total_answered_scorecards bigint,
	distinct_scorecards bigint,
	distinct_calls bigint,
	linked_call_count bigint,
	reviewed_user_count bigint,
	manual_count bigint,
	automatic_count bigint,
	transcript_count bigint,
	missing_transcript_count bigint,
	average_overall_score double precision,
	average_answer_score double precision,
	latest_review_time text
)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = public, pg_temp
AS $function$
SELECT COUNT(*) AS total_answered_scorecards,
       COUNT(DISTINCT NULLIF(sa.scorecard_id, '')) AS distinct_scorecards,
       COUNT(DISTINCT NULLIF(sa.call_id, '')) AS distinct_calls,
       COUNT(DISTINCT c.call_id) AS linked_call_count,
       COUNT(DISTINCT NULLIF(sa.reviewed_user_id, '')) AS reviewed_user_count,
       COALESCE(SUM(CASE WHEN sa.review_method = 'MANUAL' THEN 1 ELSE 0 END), 0) AS manual_count,
       COALESCE(SUM(CASE WHEN sa.review_method = 'AUTOMATIC' THEN 1 ELSE 0 END), 0) AS automatic_count,
       COUNT(DISTINCT CASE WHEN cf.transcript_status = 'present' THEN sa.call_id END) AS transcript_count,
       COUNT(DISTINCT CASE WHEN cf.transcript_status = 'missing' THEN sa.call_id END) AS missing_transcript_count,
       COALESCE(AVG(NULLIF(sa.overall_score, 0)), 0) AS average_overall_score,
       COALESCE(AVG(NULLIF(sa.average_score, 0)), 0) AS average_answer_score,
       COALESCE(MAX(sa.review_time), '') AS latest_review_time
  FROM scorecard_activity sa
  LEFT JOIN calls c
    ON c.call_id = sa.call_id
  LEFT JOIN call_facts cf
    ON cf.call_id = sa.call_id
$function$;

REVOKE ALL ON FUNCTION gongmcp_scorecard_activity_summary(text, integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION gongmcp_scorecard_activity_totals() FROM PUBLIC;
`

const postgresScorecardActivityReaderGrantStatementsSQL = `
			REVOKE ALL PRIVILEGES ON TABLE scorecard_activity FROM gongmcp_reader;
			GRANT EXECUTE ON FUNCTION gongmcp_scorecard_activity_summary(text, integer) TO gongmcp_reader;
			GRANT EXECUTE ON FUNCTION gongmcp_scorecard_activity_totals() TO gongmcp_reader;
`

const postgresScorecardActivityReaderGrantsSQL = `
DO $$
BEGIN
	IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'gongmcp_reader') THEN
` + postgresScorecardActivityReaderGrantStatementsSQL + `
	END IF;
END;
$$;
`

type postgresScorecardActivityPayload struct {
	AnsweredScorecardID string
	ScorecardID         string
	ScorecardName       string
	CallID              string
	CallStartedAt       string
	ReviewedUserID      string
	ReviewerUserID      string
	EditorUserID        string
	ReviewMethod        string
	ReviewTime          string
	VisibilityType      string
	OverallScore        float64
	AverageScore        float64
	AnswerCount         int64
	RawJSON             []byte
	RawSHA256           string
}

func (s *Store) UpsertScorecardActivity(ctx context.Context, raw json.RawMessage) (*sqlite.ScorecardActivityRecord, error) {
	if err := s.ensureWritable(); err != nil {
		return nil, err
	}
	payload, err := decodePostgresScorecardActivity(raw)
	if err != nil {
		return nil, err
	}
	now := nowUTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := lockPostgresWriterTx(ctx, tx); err != nil {
		return nil, err
	}
	if err := ensurePostgresCallNotPurgedTx(ctx, tx, payload.CallID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO scorecard_activity(
	answered_scorecard_id, scorecard_id, scorecard_name, call_id, call_started_at,
	reviewed_user_id, reviewer_user_id, editor_user_id, review_method, review_time,
	visibility_type, overall_score, average_score, answer_count,
	raw_json, raw_sha256, first_seen_at, updated_at
) VALUES($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15::jsonb, $16, $17, $18)
ON CONFLICT(answered_scorecard_id) DO UPDATE SET
	scorecard_id = excluded.scorecard_id,
	scorecard_name = excluded.scorecard_name,
	call_id = excluded.call_id,
	call_started_at = excluded.call_started_at,
	reviewed_user_id = excluded.reviewed_user_id,
	reviewer_user_id = excluded.reviewer_user_id,
	editor_user_id = excluded.editor_user_id,
	review_method = excluded.review_method,
	review_time = excluded.review_time,
	visibility_type = excluded.visibility_type,
	overall_score = excluded.overall_score,
	average_score = excluded.average_score,
	answer_count = excluded.answer_count,
	raw_json = excluded.raw_json,
	raw_sha256 = excluded.raw_sha256,
	updated_at = excluded.updated_at
WHERE
	scorecard_activity.scorecard_id IS DISTINCT FROM excluded.scorecard_id OR
	scorecard_activity.scorecard_name IS DISTINCT FROM excluded.scorecard_name OR
	scorecard_activity.call_id IS DISTINCT FROM excluded.call_id OR
	scorecard_activity.call_started_at IS DISTINCT FROM excluded.call_started_at OR
	scorecard_activity.reviewed_user_id IS DISTINCT FROM excluded.reviewed_user_id OR
	scorecard_activity.reviewer_user_id IS DISTINCT FROM excluded.reviewer_user_id OR
	scorecard_activity.editor_user_id IS DISTINCT FROM excluded.editor_user_id OR
	scorecard_activity.review_method IS DISTINCT FROM excluded.review_method OR
	scorecard_activity.review_time IS DISTINCT FROM excluded.review_time OR
	scorecard_activity.visibility_type IS DISTINCT FROM excluded.visibility_type OR
	scorecard_activity.overall_score IS DISTINCT FROM excluded.overall_score OR
	scorecard_activity.average_score IS DISTINCT FROM excluded.average_score OR
	scorecard_activity.answer_count IS DISTINCT FROM excluded.answer_count OR
	scorecard_activity.raw_sha256 IS DISTINCT FROM excluded.raw_sha256`,
		payload.AnsweredScorecardID,
		payload.ScorecardID,
		payload.ScorecardName,
		payload.CallID,
		payload.CallStartedAt,
		payload.ReviewedUserID,
		payload.ReviewerUserID,
		payload.EditorUserID,
		payload.ReviewMethod,
		payload.ReviewTime,
		payload.VisibilityType,
		payload.OverallScore,
		payload.AverageScore,
		payload.AnswerCount,
		string(payload.RawJSON),
		payload.RawSHA256,
		now,
		now,
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.scorecardActivityByID(ctx, payload.AnsweredScorecardID)
}

func (s *Store) SummarizeScorecardActivity(ctx context.Context, params sqlite.ScorecardActivitySummaryParams) ([]sqlite.ScorecardActivitySummaryRow, error) {
	groupBy, column, err := postgresScorecardActivityGroupColumn(params.GroupBy)
	if err != nil {
		return nil, err
	}
	if s.readOnly && groupBy == "reviewed_user" {
		return nil, errors.New("postgres read-only scorecard activity grouping by reviewed_user is not supported; use aggregate groupings that do not expose user IDs")
	}
	limit := boundedLimit(params.Limit, defaultCallFactsLimit, maxCallFactsLimit)
	if s.readOnly {
		rows, err := s.db.QueryContext(ctx, `SELECT group_by, group_value, answered_scorecard_count, distinct_call_count, linked_call_count, reviewed_user_count, manual_count, automatic_count, transcript_count, missing_transcript_count, average_overall_score, average_answer_score, latest_review_time FROM gongmcp_scorecard_activity_summary($1, $2)`, groupBy, limit)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		return scanPostgresScorecardActivitySummaryRows(rows)
	}

	groupExpr := `COALESCE(NULLIF(TRIM(` + column + `), ''), 'unknown')`
	if groupBy == "scorecard" {
		groupExpr = `COALESCE(NULLIF(TRIM(sa.scorecard_name), ''), 'unknown')`
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT '`+groupBy+`' AS group_by,
       `+groupExpr+` AS group_value,
       COUNT(*) AS answered_scorecard_count,
       COUNT(DISTINCT NULLIF(sa.call_id, '')) AS distinct_call_count,
       COUNT(DISTINCT c.call_id) AS linked_call_count,
       COUNT(DISTINCT NULLIF(sa.reviewed_user_id, '')) AS reviewed_user_count,
       COALESCE(SUM(CASE WHEN sa.review_method = 'MANUAL' THEN 1 ELSE 0 END), 0) AS manual_count,
       COALESCE(SUM(CASE WHEN sa.review_method = 'AUTOMATIC' THEN 1 ELSE 0 END), 0) AS automatic_count,
       COUNT(DISTINCT CASE WHEN cf.transcript_status = 'present' THEN sa.call_id END) AS transcript_count,
       COUNT(DISTINCT CASE WHEN cf.transcript_status = 'missing' THEN sa.call_id END) AS missing_transcript_count,
       COALESCE(AVG(NULLIF(sa.overall_score, 0)), 0) AS average_overall_score,
       COALESCE(AVG(NULLIF(sa.average_score, 0)), 0) AS average_answer_score,
       COALESCE(MAX(sa.review_time), '') AS latest_review_time
  FROM scorecard_activity sa
  LEFT JOIN calls c
    ON c.call_id = sa.call_id
  LEFT JOIN call_facts cf
    ON cf.call_id = sa.call_id
 GROUP BY group_value
 ORDER BY answered_scorecard_count DESC, group_value
 LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPostgresScorecardActivitySummaryRows(rows)
}

func (s *Store) ScorecardActivityOverview(ctx context.Context, limit int) (*sqlite.ScorecardActivityOverview, error) {
	limit = boundedLimit(limit, defaultCallFactsLimit, maxCallFactsLimit)
	report := &sqlite.ScorecardActivityOverview{}
	var err error
	if s.readOnly {
		err = s.db.QueryRowContext(ctx, `SELECT total_answered_scorecards, distinct_scorecards, distinct_calls, linked_call_count, reviewed_user_count, manual_count, automatic_count, transcript_count, missing_transcript_count, average_overall_score, average_answer_score, latest_review_time FROM gongmcp_scorecard_activity_totals()`).Scan(
			&report.TotalAnsweredScorecards,
			&report.DistinctScorecards,
			&report.DistinctCalls,
			&report.LinkedCallCount,
			&report.ReviewedUserCount,
			&report.ManualCount,
			&report.AutomaticCount,
			&report.TranscriptCount,
			&report.MissingTranscriptCount,
			&report.AverageOverallScore,
			&report.AverageAnswerScore,
			&report.LatestReviewTime,
		)
	} else {
		err = s.db.QueryRowContext(ctx, `
SELECT COUNT(*) AS total_answered_scorecards,
       COUNT(DISTINCT NULLIF(sa.scorecard_id, '')) AS distinct_scorecards,
       COUNT(DISTINCT NULLIF(sa.call_id, '')) AS distinct_calls,
       COUNT(DISTINCT c.call_id) AS linked_call_count,
       COUNT(DISTINCT NULLIF(sa.reviewed_user_id, '')) AS reviewed_user_count,
       COALESCE(SUM(CASE WHEN sa.review_method = 'MANUAL' THEN 1 ELSE 0 END), 0) AS manual_count,
       COALESCE(SUM(CASE WHEN sa.review_method = 'AUTOMATIC' THEN 1 ELSE 0 END), 0) AS automatic_count,
       COUNT(DISTINCT CASE WHEN cf.transcript_status = 'present' THEN sa.call_id END) AS transcript_count,
       COUNT(DISTINCT CASE WHEN cf.transcript_status = 'missing' THEN sa.call_id END) AS missing_transcript_count,
       COALESCE(AVG(NULLIF(sa.overall_score, 0)), 0) AS average_overall_score,
       COALESCE(AVG(NULLIF(sa.average_score, 0)), 0) AS average_answer_score,
       COALESCE(MAX(sa.review_time), '') AS latest_review_time
  FROM scorecard_activity sa
  LEFT JOIN calls c
    ON c.call_id = sa.call_id
  LEFT JOIN call_facts cf
    ON cf.call_id = sa.call_id`).Scan(
			&report.TotalAnsweredScorecards,
			&report.DistinctScorecards,
			&report.DistinctCalls,
			&report.LinkedCallCount,
			&report.ReviewedUserCount,
			&report.ManualCount,
			&report.AutomaticCount,
			&report.TranscriptCount,
			&report.MissingTranscriptCount,
			&report.AverageOverallScore,
			&report.AverageAnswerScore,
			&report.LatestReviewTime,
		)
	}
	if err != nil {
		return nil, err
	}
	if report.ByScorecard, err = s.SummarizeScorecardActivity(ctx, sqlite.ScorecardActivitySummaryParams{GroupBy: "scorecard", Limit: limit}); err != nil {
		return nil, err
	}
	if report.ByReviewMethod, err = s.SummarizeScorecardActivity(ctx, sqlite.ScorecardActivitySummaryParams{GroupBy: "review_method", Limit: limit}); err != nil {
		return nil, err
	}
	if report.ByLifecycle, err = s.SummarizeScorecardActivity(ctx, sqlite.ScorecardActivitySummaryParams{GroupBy: "lifecycle", Limit: limit}); err != nil {
		return nil, err
	}
	if report.ByTranscriptStatus, err = s.SummarizeScorecardActivity(ctx, sqlite.ScorecardActivitySummaryParams{GroupBy: "transcript_status", Limit: limit}); err != nil {
		return nil, err
	}
	return report, nil
}

func (s *Store) scorecardActivityByID(ctx context.Context, answeredScorecardID string) (*sqlite.ScorecardActivityRecord, error) {
	var row sqlite.ScorecardActivityRecord
	if err := s.db.QueryRowContext(ctx, `
SELECT answered_scorecard_id, scorecard_id, scorecard_name, call_id, call_started_at,
       reviewed_user_id, reviewer_user_id, editor_user_id, review_method, review_time,
       visibility_type, overall_score, average_score, answer_count, raw_sha256, updated_at
  FROM scorecard_activity
 WHERE answered_scorecard_id = $1`, answeredScorecardID).Scan(
		&row.AnsweredScorecardID,
		&row.ScorecardID,
		&row.ScorecardName,
		&row.CallID,
		&row.CallStartedAt,
		&row.ReviewedUserID,
		&row.ReviewerUserID,
		&row.EditorUserID,
		&row.ReviewMethod,
		&row.ReviewTime,
		&row.VisibilityType,
		&row.OverallScore,
		&row.AverageScore,
		&row.AnswerCount,
		&row.RawSHA256,
		&row.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &row, nil
}

func scanPostgresScorecardActivitySummaryRows(rows *sql.Rows) ([]sqlite.ScorecardActivitySummaryRow, error) {
	out := []sqlite.ScorecardActivitySummaryRow{}
	for rows.Next() {
		var row sqlite.ScorecardActivitySummaryRow
		if err := rows.Scan(
			&row.GroupBy,
			&row.GroupValue,
			&row.AnsweredScorecardCount,
			&row.DistinctCallCount,
			&row.LinkedCallCount,
			&row.ReviewedUserCount,
			&row.ManualCount,
			&row.AutomaticCount,
			&row.TranscriptCount,
			&row.MissingTranscriptCount,
			&row.AverageOverallScore,
			&row.AverageAnswerScore,
			&row.LatestReviewTime,
		); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func decodePostgresScorecardActivity(raw json.RawMessage) (*postgresScorecardActivityPayload, error) {
	normalized, err := normalizeJSON(raw)
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	decoder := json.NewDecoder(bytes.NewReader(normalized))
	decoder.UseNumber()
	if err := decoder.Decode(&doc); err != nil {
		return nil, err
	}
	answeredScorecardID := postgresFirstIDString(doc, "answeredScorecardId", "id")
	if answeredScorecardID == "" {
		return nil, errors.New("scorecard activity payload missing answered scorecard id")
	}
	overallScore, averageScore, answerCount := postgresScorecardActivityScores(doc["answers"])
	return &postgresScorecardActivityPayload{
		AnsweredScorecardID: answeredScorecardID,
		ScorecardID:         postgresFirstIDString(doc, "scorecardId"),
		ScorecardName:       firstString(doc, "scorecardName", "name"),
		CallID:              postgresFirstIDString(doc, "callId"),
		CallStartedAt:       firstString(doc, "callStartTime", "callStartedAt", "started"),
		ReviewedUserID:      postgresFirstIDString(doc, "reviewedUserId"),
		ReviewerUserID:      postgresFirstIDString(doc, "reviewerUserId"),
		EditorUserID:        postgresFirstIDString(doc, "editorUserId"),
		ReviewMethod:        strings.ToUpper(firstString(doc, "reviewMethod")),
		ReviewTime:          firstString(doc, "reviewTime", "reviewedAt"),
		VisibilityType:      firstString(doc, "visibilityType"),
		OverallScore:        overallScore,
		AverageScore:        averageScore,
		AnswerCount:         answerCount,
		RawJSON:             normalized,
		RawSHA256:           sha256Hex(normalized),
	}, nil
}

func postgresScorecardActivityGroupColumn(groupBy string) (string, string, error) {
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

func postgresFirstIDString(doc map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := doc[key]
		if !ok || value == nil {
			continue
		}
		switch typed := value.(type) {
		case string:
			return strings.TrimSpace(typed)
		case float64:
			return strconv.FormatInt(int64(typed), 10)
		case float32:
			return strconv.FormatInt(int64(typed), 10)
		case int:
			return strconv.FormatInt(int64(typed), 10)
		case int64:
			return strconv.FormatInt(typed, 10)
		case json.Number:
			return strings.TrimSpace(typed.String())
		case fmt.Stringer:
			return strings.TrimSpace(typed.String())
		}
	}
	return ""
}

func postgresScorecardActivityScores(value any) (float64, float64, int64) {
	answers := arrayFromAny(value)
	if len(answers) == 0 {
		return 0, 0, 0
	}
	var total float64
	var count int64
	var overall float64
	var hasOverall bool
	for _, item := range answers {
		answer, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if boolFromAny(answer["notApplicable"]) {
			continue
		}
		if _, ok := answer["score"]; !ok {
			continue
		}
		score := postgresFloat64FromAny(answer["score"])
		total += score
		count++
		if boolFromAny(answer["isOverall"]) {
			overall = score
			hasOverall = true
		}
	}
	if count == 0 {
		return 0, 0, 0
	}
	average := total / float64(count)
	if !hasOverall {
		overall = average
	}
	return overall, average, count
}

func postgresFloat64FromAny(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case json.Number:
		out, _ := typed.Float64()
		return out
	case string:
		out, _ := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return out
	default:
		return 0
	}
}
