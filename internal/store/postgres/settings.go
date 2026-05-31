package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
)

const postgresSettingsMigrationSQL = `
CREATE TABLE IF NOT EXISTS gong_settings (
	kind TEXT NOT NULL,
	object_id TEXT NOT NULL,
	name TEXT NOT NULL DEFAULT '',
	active BOOLEAN NOT NULL DEFAULT false,
	raw_json JSONB NOT NULL,
	raw_sha256 TEXT NOT NULL,
	first_seen_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	PRIMARY KEY(kind, object_id)
);

CREATE INDEX IF NOT EXISTS idx_pg_gong_settings_kind_name ON gong_settings(kind, name);
`

const postgresSettingsFunctionsSQL = `
CREATE OR REPLACE FUNCTION gongmcp_scorecards(active_only_arg boolean, row_limit integer)
RETURNS TABLE(scorecard_id text, name text, active boolean, review_method text, workspace_id text, question_count bigint, source_created_at text, source_updated_at text, cached_updated_at text)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = public, pg_temp
AS $function$
SELECT COALESCE(NULLIF(raw_json->>'scorecardId', ''), NULLIF(raw_json->>'id', ''), object_id) AS scorecard_id,
       COALESCE(NULLIF(raw_json->>'scorecardName', ''), NULLIF(raw_json->>'name', ''), NULLIF(raw_json->>'title', ''), NULLIF(raw_json->>'displayName', ''), name) AS name,
       active,
       COALESCE(raw_json->>'reviewMethod', '') AS review_method,
       COALESCE(raw_json->>'workspaceId', '') AS workspace_id,
       jsonb_array_length(CASE WHEN jsonb_typeof(raw_json->'questions') = 'array' THEN raw_json->'questions' ELSE '[]'::jsonb END)::bigint AS question_count,
       COALESCE(raw_json->>'created', raw_json->>'createdAt', '') AS source_created_at,
       COALESCE(raw_json->>'updated', raw_json->>'updatedAt', '') AS source_updated_at,
       updated_at AS cached_updated_at
  FROM gong_settings
 WHERE kind = 'scorecards'
   AND (NOT COALESCE(active_only_arg, false) OR active)
 ORDER BY active DESC, name, object_id
 LIMIT LEAST(GREATEST(COALESCE(row_limit, 100), 1), 1000)
$function$;

CREATE OR REPLACE FUNCTION gongmcp_scorecard_detail(scorecard_id_arg text)
RETURNS TABLE(scorecard_json jsonb)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = public, pg_temp
AS $function$
SELECT jsonb_build_object(
	'scorecard_id', COALESCE(NULLIF(s.raw_json->>'scorecardId', ''), NULLIF(s.raw_json->>'id', ''), s.object_id),
	'name', COALESCE(NULLIF(s.raw_json->>'scorecardName', ''), NULLIF(s.raw_json->>'name', ''), NULLIF(s.raw_json->>'title', ''), NULLIF(s.raw_json->>'displayName', ''), s.name),
	'active', s.active,
	'review_method', COALESCE(s.raw_json->>'reviewMethod', ''),
	'workspace_id', COALESCE(s.raw_json->>'workspaceId', ''),
	'question_count', jsonb_array_length(CASE WHEN jsonb_typeof(s.raw_json->'questions') = 'array' THEN s.raw_json->'questions' ELSE '[]'::jsonb END),
	'source_created_at', COALESCE(s.raw_json->>'created', s.raw_json->>'createdAt', ''),
	'source_updated_at', COALESCE(s.raw_json->>'updated', s.raw_json->>'updatedAt', ''),
	'cached_updated_at', s.updated_at,
	'questions', COALESCE((
		SELECT jsonb_agg(jsonb_build_object(
			'question_id', COALESCE(q.value->>'questionId', q.value->>'id', ''),
			'question_text', COALESCE(q.value->>'questionText', q.value->>'text', q.value->>'name', q.value->>'label', ''),
			'question_type', COALESCE(q.value->>'questionType', q.value->>'type', ''),
			'is_overall', CASE WHEN lower(COALESCE(q.value->>'isOverall', '')) IN ('true', 't', 'yes', 'y', '1', 'on') THEN true ELSE false END,
			'min_range', CASE WHEN jsonb_typeof(q.value->'minRange') = 'number' AND (q.value->>'minRange') ~ '^-?[0-9]+(\.[0-9]+)?$' THEN trunc((q.value->>'minRange')::numeric)::bigint ELSE 0 END,
			'max_range', CASE WHEN jsonb_typeof(q.value->'maxRange') = 'number' AND (q.value->>'maxRange') ~ '^-?[0-9]+(\.[0-9]+)?$' THEN trunc((q.value->>'maxRange')::numeric)::bigint ELSE 0 END,
			'answer_guide', left(COALESCE(q.value->>'answerGuide', q.value->>'guide', ''), 500),
			'options', COALESCE((
				SELECT jsonb_agg(left(option_text, 160) ORDER BY option_ordinality)
				  FROM (
					SELECT DISTINCT ON (lower(option_text)) option_text, option_ordinality
					  FROM (
						SELECT NULLIF(btrim(CASE
							WHEN jsonb_typeof(opt.value) = 'string' THEN opt.value #>> '{}'
							WHEN jsonb_typeof(opt.value) = 'object' THEN COALESCE(opt.value->>'text', opt.value->>'label', opt.value->>'answer', opt.value->>'name', opt.value->>'value', '')
							ELSE opt.value::text
						END), '') AS option_text,
						opt.ordinality AS option_ordinality
						  FROM jsonb_array_elements(CASE WHEN jsonb_typeof(q.value->'answerOptions') = 'array' THEN q.value->'answerOptions' ELSE '[]'::jsonb END) WITH ORDINALITY AS opt(value, ordinality)
						 WHERE opt.ordinality <= 50
					  ) extracted
					 WHERE option_text <> ''
					 ORDER BY lower(option_text), option_ordinality
				  ) deduped_options
			), '[]'::jsonb)
		) ORDER BY ordinality)
		  FROM jsonb_array_elements(CASE WHEN jsonb_typeof(s.raw_json->'questions') = 'array' THEN s.raw_json->'questions' ELSE '[]'::jsonb END) WITH ORDINALITY AS q(value, ordinality)
		 WHERE COALESCE(q.value->>'questionText', q.value->>'text', q.value->>'name', q.value->>'label', '') <> ''
		   AND q.ordinality <= 200
	), '[]'::jsonb)
) AS scorecard_json
  FROM gong_settings s
 WHERE s.kind = 'scorecards'
   AND (
		s.object_id = scorecard_id_arg OR
		s.raw_json->>'scorecardId' = scorecard_id_arg OR
		s.raw_json->>'id' = scorecard_id_arg
   )
 ORDER BY CASE
	WHEN s.object_id = scorecard_id_arg THEN 0
	WHEN s.raw_json->>'scorecardId' = scorecard_id_arg THEN 1
	ELSE 2
 END
 LIMIT 1
$function$;

REVOKE ALL ON FUNCTION gongmcp_scorecards(boolean, integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION gongmcp_scorecard_detail(text) FROM PUBLIC;
`

const postgresSettingsReaderGrantStatementsSQL = `
			GRANT SELECT (kind, object_id, name, active, first_seen_at, updated_at) ON gong_settings TO gongmcp_reader;
			GRANT EXECUTE ON FUNCTION gongmcp_scorecards(boolean, integer) TO gongmcp_reader;
			GRANT EXECUTE ON FUNCTION gongmcp_scorecard_detail(text) TO gongmcp_reader;
`

const postgresSettingsReaderGrantsSQL = `
DO $$
BEGIN
	IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'gongmcp_reader') THEN
` + postgresSettingsReaderGrantStatementsSQL + `
	END IF;
END;
$$;
`

type postgresGongSettingPayload struct {
	Kind      string
	ObjectID  string
	Name      string
	Active    bool
	RawJSON   []byte
	RawSHA256 string
}

func (s *Store) UpsertGongSetting(ctx context.Context, kind string, raw json.RawMessage) (*sqlite.GongSettingRecord, error) {
	if err := s.ensureWritable(); err != nil {
		return nil, err
	}
	payload, err := decodePostgresGongSetting(kind, raw)
	if err != nil {
		return nil, err
	}
	now := nowUTC()
	if _, err := s.db.ExecContext(ctx, `INSERT INTO gong_settings(
	kind, object_id, name, active, raw_json, raw_sha256, first_seen_at, updated_at
) VALUES($1, $2, $3, $4, $5::jsonb, $6, $7, $8)
ON CONFLICT(kind, object_id) DO UPDATE SET
	name = excluded.name,
	active = excluded.active,
	raw_json = excluded.raw_json,
	raw_sha256 = excluded.raw_sha256,
	updated_at = excluded.updated_at
WHERE
	gong_settings.name IS DISTINCT FROM excluded.name OR
	gong_settings.active IS DISTINCT FROM excluded.active OR
	gong_settings.raw_sha256 IS DISTINCT FROM excluded.raw_sha256`,
		payload.Kind,
		payload.ObjectID,
		payload.Name,
		payload.Active,
		string(payload.RawJSON),
		payload.RawSHA256,
		now,
		now,
	); err != nil {
		return nil, err
	}
	return s.gongSettingByID(ctx, payload.Kind, payload.ObjectID)
}

func (s *Store) ListGongSettings(ctx context.Context, params sqlite.GongSettingListParams) ([]sqlite.GongSettingRecord, error) {
	limit := boundedLimit(params.Limit, defaultCRMFieldLimit, maxCRMFieldLimit)
	kind := normalizePostgresGongSettingKind(params.Kind)
	query := `SELECT kind, object_id, name, active, updated_at FROM gong_settings`
	args := []any{}
	if kind != "" {
		query += ` WHERE kind = $1`
		args = append(args, kind)
	}
	query += fmt.Sprintf(` ORDER BY kind, name, object_id LIMIT $%d`, len(args)+1)
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []sqlite.GongSettingRecord{}
	for rows.Next() {
		var row sqlite.GongSettingRecord
		if err := rows.Scan(&row.Kind, &row.ObjectID, &row.Name, &row.Active, &row.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) ListScorecards(ctx context.Context, params sqlite.ScorecardListParams) ([]sqlite.ScorecardSummary, error) {
	limit := boundedLimit(params.Limit, defaultCRMFieldLimit, maxCRMFieldLimit)
	if s.readOnly {
		rows, err := s.db.QueryContext(ctx, `SELECT scorecard_id, name, active, review_method, workspace_id, question_count, source_created_at, source_updated_at, cached_updated_at FROM gongmcp_scorecards($1, $2)`, params.ActiveOnly, limit)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		return scanPostgresScorecardSummaries(rows)
	}
	query := `SELECT object_id, name, active, raw_json, updated_at FROM gong_settings WHERE kind = 'scorecards'`
	args := []any{}
	if params.ActiveOnly {
		query += ` AND active = true`
	}
	query += ` ORDER BY active DESC, name, object_id LIMIT $1`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []sqlite.ScorecardSummary{}
	for rows.Next() {
		var objectID, name, cachedUpdatedAt string
		var active bool
		var raw []byte
		if err := rows.Scan(&objectID, &name, &active, &raw, &cachedUpdatedAt); err != nil {
			return nil, err
		}
		summary, err := decodePostgresScorecardSummary(raw, cachedUpdatedAt)
		if err != nil {
			return nil, err
		}
		if summary.ScorecardID == "" {
			summary.ScorecardID = objectID
		}
		if summary.Name == "" {
			summary.Name = name
		}
		if !summary.Active {
			summary.Active = active
		}
		out = append(out, summary)
	}
	return out, rows.Err()
}

func (s *Store) GetScorecardDetail(ctx context.Context, scorecardID string) (*sqlite.ScorecardDetail, error) {
	scorecardID = strings.TrimSpace(scorecardID)
	if scorecardID == "" {
		return nil, errors.New("scorecard id is required")
	}
	if s.readOnly {
		var encoded []byte
		err := s.db.QueryRowContext(ctx, `SELECT scorecard_json FROM gongmcp_scorecard_detail($1)`, scorecardID).Scan(&encoded)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("scorecard %q not found", scorecardID)
		}
		if err != nil {
			return nil, err
		}
		var detail sqlite.ScorecardDetail
		if err := json.Unmarshal(encoded, &detail); err != nil {
			return nil, err
		}
		return &detail, nil
	}
	var raw []byte
	var cachedUpdatedAt string
	err := s.db.QueryRowContext(ctx, `SELECT raw_json, updated_at
	  FROM gong_settings
	 WHERE kind = 'scorecards'
	   AND (object_id = $1 OR raw_json->>'scorecardId' = $1 OR raw_json->>'id' = $1)
	 ORDER BY CASE
		WHEN object_id = $1 THEN 0
		WHEN raw_json->>'scorecardId' = $1 THEN 1
		ELSE 2
	 END
	 LIMIT 1`, scorecardID).Scan(&raw, &cachedUpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("scorecard %q not found", scorecardID)
	}
	if err != nil {
		return nil, err
	}
	return decodePostgresScorecardDetail(raw, cachedUpdatedAt)
}

func (s *Store) gongSettingByID(ctx context.Context, kind string, objectID string) (*sqlite.GongSettingRecord, error) {
	var row sqlite.GongSettingRecord
	if err := s.db.QueryRowContext(ctx, `SELECT kind, object_id, name, active, updated_at FROM gong_settings WHERE kind = $1 AND object_id = $2`, kind, objectID).Scan(&row.Kind, &row.ObjectID, &row.Name, &row.Active, &row.UpdatedAt); err != nil {
		return nil, err
	}
	return &row, nil
}

func scanPostgresScorecardSummaries(rows *sql.Rows) ([]sqlite.ScorecardSummary, error) {
	out := []sqlite.ScorecardSummary{}
	for rows.Next() {
		var row sqlite.ScorecardSummary
		if err := rows.Scan(&row.ScorecardID, &row.Name, &row.Active, &row.ReviewMethod, &row.WorkspaceID, &row.QuestionCount, &row.SourceCreatedAt, &row.SourceUpdatedAt, &row.CachedUpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func decodePostgresGongSetting(kind string, raw json.RawMessage) (*postgresGongSettingPayload, error) {
	normalized, err := normalizeJSON(raw)
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := json.Unmarshal(normalized, &doc); err != nil {
		return nil, err
	}
	kind = normalizePostgresGongSettingKind(kind)
	if kind == "" {
		return nil, errors.New("settings kind is required")
	}
	name := firstString(doc, "name", "title", "displayName", "label", "scorecardName", "trackerName", "workspaceName")
	objectID := firstString(doc, "id", "trackerId", "keywordTrackerId", "scorecardId", "workspaceId")
	if objectID == "" {
		if name != "" {
			objectID = kind + ":" + name
		} else {
			objectID = kind + ":sha256:" + sha256Hex(normalized)[:16]
		}
	}
	active := false
	for _, key := range []string{"active", "enabled", "isActive", "status"} {
		if value, ok := doc[key]; ok {
			active = boolFromAny(value)
			break
		}
	}
	return &postgresGongSettingPayload{Kind: kind, ObjectID: objectID, Name: name, Active: active, RawJSON: normalized, RawSHA256: sha256Hex(normalized)}, nil
}

func decodePostgresScorecardSummary(raw json.RawMessage, cachedUpdatedAt string) (sqlite.ScorecardSummary, error) {
	normalized, err := normalizeJSON(raw)
	if err != nil {
		return sqlite.ScorecardSummary{}, err
	}
	var doc map[string]any
	if err := json.Unmarshal(normalized, &doc); err != nil {
		return sqlite.ScorecardSummary{}, err
	}
	return sqlite.ScorecardSummary{
		ScorecardID:     firstString(doc, "scorecardId", "id"),
		Name:            firstString(doc, "scorecardName", "name", "title", "displayName"),
		Active:          boolFromAny(doc["enabled"]) || boolFromAny(doc["active"]),
		ReviewMethod:    firstString(doc, "reviewMethod"),
		WorkspaceID:     firstString(doc, "workspaceId"),
		QuestionCount:   int64(len(arrayFromAny(doc["questions"]))),
		SourceCreatedAt: firstString(doc, "created", "createdAt"),
		SourceUpdatedAt: firstString(doc, "updated", "updatedAt"),
		CachedUpdatedAt: cachedUpdatedAt,
	}, nil
}

func decodePostgresScorecardDetail(raw json.RawMessage, cachedUpdatedAt string) (*sqlite.ScorecardDetail, error) {
	normalized, err := normalizeJSON(raw)
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := json.Unmarshal(normalized, &doc); err != nil {
		return nil, err
	}
	summary, err := decodePostgresScorecardSummary(normalized, cachedUpdatedAt)
	if err != nil {
		return nil, err
	}
	detail := &sqlite.ScorecardDetail{ScorecardSummary: summary}
	for _, item := range arrayFromAny(doc["questions"]) {
		questionMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		questionText := firstString(questionMap, "questionText", "text", "name", "label")
		if questionText == "" {
			continue
		}
		detail.Questions = append(detail.Questions, sqlite.ScorecardQuestion{
			QuestionID:   firstString(questionMap, "questionId", "id"),
			QuestionText: questionText,
			QuestionType: firstString(questionMap, "questionType", "type"),
			IsOverall:    boolFromAny(questionMap["isOverall"]),
			MinRange:     int64FromAny(questionMap["minRange"]),
			MaxRange:     int64FromAny(questionMap["maxRange"]),
			AnswerGuide:  truncateString(firstString(questionMap, "answerGuide", "guide"), 500),
			Options:      postgresScorecardAnswerOptions(questionMap["answerOptions"]),
		})
	}
	return detail, nil
}

func normalizePostgresGongSettingKind(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "tracker", "trackers", "keywordtracker", "keywordtrackers", "keyword_trackers":
		return "trackers"
	case "scorecard", "scorecards":
		return "scorecards"
	case "workspace", "workspaces":
		return "workspaces"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func arrayFromAny(value any) []any {
	typed, ok := value.([]any)
	if !ok {
		return nil
	}
	return typed
}

func truncateString(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 || len(value) <= max {
		return value
	}
	return value[:max]
}

func postgresScorecardAnswerOptions(value any) []string {
	items := arrayFromAny(value)
	if len(items) == 0 {
		return nil
	}
	out := make([]string, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		var text string
		switch typed := item.(type) {
		case string:
			text = typed
		case map[string]any:
			text = firstString(typed, "text", "label", "answer", "name", "value")
		default:
			text = stringifyValue(item)
		}
		text = truncateString(text, 160)
		if text == "" {
			continue
		}
		key := strings.ToLower(text)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, text)
	}
	return out
}

func stringifyValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case json.Number:
		return typed.String()
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return ""
		}
		return string(encoded)
	}
}
