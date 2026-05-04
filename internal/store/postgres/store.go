package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
	_ "github.com/jackc/pgx/v5/stdlib"
)

const (
	defaultTranscriptSearchLimit = 20
	maxTranscriptSearchLimit     = 1000
	defaultCallSearchLimit       = 20
	maxCallSearchLimit           = 1000
)

type Store struct {
	db       *sql.DB
	readOnly bool
}

func URLFromEnv(getenv func(string) string) string {
	if getenv == nil {
		getenv = os.Getenv
	}
	if value := strings.TrimSpace(getenv("GONG_DATABASE_URL")); value != "" {
		return value
	}
	return strings.TrimSpace(getenv("DATABASE_URL"))
}

func Open(ctx context.Context, databaseURL string) (*Store, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, errors.New("postgres database URL is required")
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(30 * time.Minute)
	store := &Store{db: db}
	if err := store.Migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func readOnlyDatabaseURL(databaseURL string) string {
	parsed, err := url.Parse(databaseURL)
	if err != nil || parsed.Scheme == "" {
		return databaseURL
	}
	query := parsed.Query()
	query.Set("default_transaction_read_only", "on")
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func OpenReadOnly(ctx context.Context, databaseURL string) (*Store, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, errors.New("postgres database URL is required")
	}
	db, err := sql.Open("pgx", readOnlyDatabaseURL(databaseURL))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(30 * time.Minute)
	store := &Store{db: db, readOnly: true}
	if err := store.validateSchema(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.validateReadOnlyPrivileges(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.validateReadModelReady(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.ExecContext(ctx, `SET default_transaction_read_only = on`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("enable postgres read-only session mode: %w", err)
	}
	var readOnlyMode string
	if err := db.QueryRowContext(ctx, `SHOW default_transaction_read_only`).Scan(&readOnlyMode); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("verify postgres read-only session mode: %w", err)
	}
	if strings.ToLower(strings.TrimSpace(readOnlyMode)) != "on" {
		_ = db.Close()
		return nil, fmt.Errorf("postgres read-only session mode is %q, want on", readOnlyMode)
	}
	return store, nil
}

func OpenStatus(ctx context.Context, databaseURL string) (*Store, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, errors.New("postgres database URL is required")
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(30 * time.Minute)
	store := &Store{db: db, readOnly: true}
	if err := store.validateSchema(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) DB() *sql.DB {
	if s == nil {
		return nil
	}
	return s.db
}

func (s *Store) Migrate(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errors.New("postgres store is not open")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS gongctl_schema_migrations (
	version INTEGER PRIMARY KEY,
	applied_at TEXT NOT NULL
)`); err != nil {
		return fmt.Errorf("ensure postgres migration table: %w", err)
	}
	var current int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM gongctl_schema_migrations`).Scan(&current); err != nil {
		return fmt.Errorf("read postgres migration version: %w", err)
	}
	if current > len(migrations) {
		return fmt.Errorf("postgres schema version %d is newer than supported version %d", current, len(migrations))
	}
	startingVersion := current
	for idx := current; idx < len(migrations); idx++ {
		statement := migrations[idx]
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply postgres migration %d: %w", idx+1, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO gongctl_schema_migrations(version, applied_at)
VALUES($1, $2)
ON CONFLICT(version) DO NOTHING`, idx+1, nowUTC()); err != nil {
			return fmt.Errorf("record postgres migration %d: %w", idx+1, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if startingVersion < len(migrations) {
		if _, err := s.RebuildReadModel(ctx); err != nil {
			return fmt.Errorf("backfill postgres read model: %w", err)
		}
	}
	return nil
}

func (s *Store) validateSchema(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errors.New("postgres store is not open")
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name IN ('sync_runs', 'sync_state', 'calls', 'users', 'transcripts', 'transcript_segments', 'call_context_objects', 'call_context_fields', 'call_facts', 'postgres_read_model_state', 'call_read_model_diagnostics')`).Scan(&count); err != nil {
		return err
	}
	if count != 11 {
		return fmt.Errorf("postgres schema is not initialized: found %d/11 core tables", count)
	}
	return nil
}

func (s *Store) validateReadOnlyPrivileges(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `
SELECT table_name
  FROM information_schema.tables
 WHERE table_schema = current_schema()
   AND table_name IN ('calls', 'users', 'transcripts', 'transcript_segments', 'sync_runs', 'sync_state', 'call_context_objects', 'call_context_fields', 'call_facts', 'postgres_read_model_state', 'call_read_model_diagnostics')
   AND (
	has_table_privilege(current_user, quote_ident(table_schema) || '.' || quote_ident(table_name), 'INSERT') OR
	has_table_privilege(current_user, quote_ident(table_schema) || '.' || quote_ident(table_name), 'UPDATE') OR
	has_table_privilege(current_user, quote_ident(table_schema) || '.' || quote_ident(table_name), 'DELETE') OR
	has_table_privilege(current_user, quote_ident(table_schema) || '.' || quote_ident(table_name), 'TRUNCATE')
   )
 ORDER BY table_name`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var writable []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			return err
		}
		writable = append(writable, table)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(writable) > 0 {
		return fmt.Errorf("postgres read-only URL has write privileges on tables: %s", strings.Join(writable, ", "))
	}
	return nil
}

func (s *Store) StartSyncRun(ctx context.Context, params sqlite.StartSyncRunParams) (*sqlite.SyncRun, error) {
	if err := s.ensureWritable(); err != nil {
		return nil, err
	}
	scope := strings.TrimSpace(params.Scope)
	if scope == "" {
		return nil, errors.New("sync run scope is required")
	}
	now := nowUTC()
	row := s.db.QueryRowContext(ctx, `INSERT INTO sync_runs(scope, sync_key, cursor, from_value, to_value, request_context, status, started_at)
VALUES($1, $2, $3, $4, $5, $6, 'running', $7)
RETURNING id`,
		scope,
		strings.TrimSpace(params.SyncKey),
		strings.TrimSpace(params.Cursor),
		strings.TrimSpace(params.From),
		strings.TrimSpace(params.To),
		strings.TrimSpace(params.RequestContext),
		now,
	)
	var id int64
	if err := row.Scan(&id); err != nil {
		return nil, err
	}
	return &sqlite.SyncRun{
		ID:             id,
		Scope:          scope,
		SyncKey:        strings.TrimSpace(params.SyncKey),
		Cursor:         strings.TrimSpace(params.Cursor),
		From:           strings.TrimSpace(params.From),
		To:             strings.TrimSpace(params.To),
		RequestContext: strings.TrimSpace(params.RequestContext),
		Status:         "running",
		StartedAt:      now,
	}, nil
}

func (s *Store) FinishSyncRun(ctx context.Context, runID int64, params sqlite.FinishSyncRunParams) error {
	if err := s.ensureWritable(); err != nil {
		return err
	}
	if runID <= 0 {
		return errors.New("run id must be positive")
	}
	status := strings.TrimSpace(params.Status)
	if status == "" {
		return errors.New("sync run status is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var scope, syncKey string
	if err := tx.QueryRowContext(ctx, `SELECT scope, sync_key FROM sync_runs WHERE id = $1`, runID).Scan(&scope, &syncKey); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("sync run %d not found", runID)
		}
		return err
	}
	finishedAt := nowUTC()
	if _, err := tx.ExecContext(ctx, `UPDATE sync_runs
SET finished_at = $1,
    status = $2,
    cursor = $3,
    records_seen = $4,
    records_written = $5,
    error_text = $6,
    request_context = CASE WHEN $7 <> '' THEN $8 ELSE request_context END
WHERE id = $9`,
		finishedAt,
		status,
		strings.TrimSpace(params.Cursor),
		params.RecordsSeen,
		params.RecordsWritten,
		strings.TrimSpace(params.ErrorText),
		strings.TrimSpace(params.RequestContext),
		strings.TrimSpace(params.RequestContext),
		runID,
	); err != nil {
		return err
	}
	lastSuccessAt := ""
	if status == "success" {
		lastSuccessAt = finishedAt
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sync_state(sync_key, scope, cursor, last_run_id, last_status, last_error, last_success_at, updated_at)
VALUES($1, $2, $3, $4, $5, $6, NULLIF($7, ''), $8)
ON CONFLICT(sync_key) DO UPDATE SET
	scope = EXCLUDED.scope,
	cursor = EXCLUDED.cursor,
	last_run_id = EXCLUDED.last_run_id,
	last_status = EXCLUDED.last_status,
	last_error = EXCLUDED.last_error,
	last_success_at = COALESCE(EXCLUDED.last_success_at, sync_state.last_success_at),
	updated_at = EXCLUDED.updated_at`,
		syncKey,
		scope,
		strings.TrimSpace(params.Cursor),
		runID,
		status,
		strings.TrimSpace(params.ErrorText),
		lastSuccessAt,
		finishedAt,
	); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) UpsertCall(ctx context.Context, raw json.RawMessage) (*sqlite.CallRecord, error) {
	if err := s.ensureWritable(); err != nil {
		return nil, err
	}
	payload, err := decodeCall(raw)
	if err != nil {
		return nil, err
	}
	now := nowUTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO calls(
	call_id, title, started_at, duration_seconds, parties_count, context_present, raw_json, raw_sha256, first_seen_at, updated_at
) VALUES($1, $2, $3, $4, $5, $6, $7::jsonb, $8, $9, $10)
ON CONFLICT(call_id) DO UPDATE SET
	title = EXCLUDED.title,
	started_at = EXCLUDED.started_at,
	duration_seconds = EXCLUDED.duration_seconds,
	parties_count = CASE WHEN EXCLUDED.parties_count > 0 THEN EXCLUDED.parties_count ELSE calls.parties_count END,
	context_present = CASE WHEN $11 THEN EXCLUDED.context_present ELSE calls.context_present END,
	raw_json = CASE WHEN $11 OR calls.context_present = false THEN EXCLUDED.raw_json ELSE calls.raw_json END,
	raw_sha256 = CASE WHEN $11 OR calls.context_present = false THEN EXCLUDED.raw_sha256 ELSE calls.raw_sha256 END,
	updated_at = EXCLUDED.updated_at`,
		payload.CallID,
		payload.Title,
		payload.StartedAt,
		payload.DurationSeconds,
		payload.PartiesCount,
		payload.ContextPresent,
		string(payload.RawJSON),
		payload.RawSHA256,
		now,
		now,
		payload.HasContextBlock,
	)
	if err != nil {
		return nil, err
	}
	if err := refreshCallReadModelTx(ctx, tx, payload.CallID); err != nil {
		return nil, err
	}
	if err := updateReadModelStateTx(ctx, tx, nowUTC(), "", false); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.callByID(ctx, payload.CallID)
}

func (s *Store) UpsertUser(ctx context.Context, raw json.RawMessage) (*sqlite.UserRecord, error) {
	if err := s.ensureWritable(); err != nil {
		return nil, err
	}
	payload, err := decodeUser(raw)
	if err != nil {
		return nil, err
	}
	now := nowUTC()
	_, err = s.db.ExecContext(ctx, `INSERT INTO users(
	user_id, email, first_name, last_name, display_name, title, active, raw_json, raw_sha256, first_seen_at, updated_at
) VALUES($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9, $10, $11)
ON CONFLICT(user_id) DO UPDATE SET
	email = EXCLUDED.email,
	first_name = EXCLUDED.first_name,
	last_name = EXCLUDED.last_name,
	display_name = EXCLUDED.display_name,
	title = EXCLUDED.title,
	active = EXCLUDED.active,
	raw_json = EXCLUDED.raw_json,
	raw_sha256 = EXCLUDED.raw_sha256,
	updated_at = EXCLUDED.updated_at`,
		payload.UserID,
		payload.Email,
		payload.FirstName,
		payload.LastName,
		payload.DisplayName,
		payload.Title,
		payload.Active,
		string(payload.RawJSON),
		payload.RawSHA256,
		now,
		now,
	)
	if err != nil {
		return nil, err
	}
	return s.userByID(ctx, payload.UserID)
}

func (s *Store) UpsertTranscript(ctx context.Context, raw json.RawMessage) (*sqlite.TranscriptRecord, error) {
	if err := s.ensureWritable(); err != nil {
		return nil, err
	}
	payload, err := decodeTranscript(raw)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := nowUTC()
	if _, err := tx.ExecContext(ctx, `INSERT INTO transcripts(call_id, raw_json, raw_sha256, segment_count, first_seen_at, updated_at)
VALUES($1, $2::jsonb, $3, $4, $5, $6)
ON CONFLICT(call_id) DO UPDATE SET
	raw_json = EXCLUDED.raw_json,
	raw_sha256 = EXCLUDED.raw_sha256,
	segment_count = EXCLUDED.segment_count,
	updated_at = EXCLUDED.updated_at`,
		payload.CallID,
		string(payload.RawJSON),
		payload.RawSHA256,
		len(payload.Segments),
		now,
		now,
	); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM transcript_segments WHERE call_id = $1`, payload.CallID); err != nil {
		return nil, err
	}
	for _, segment := range payload.Segments {
		if _, err := tx.ExecContext(ctx, `INSERT INTO transcript_segments(call_id, segment_index, speaker_id, start_ms, end_ms, text, raw_json)
VALUES($1, $2, $3, $4, $5, $6, $7::jsonb)`,
			segment.CallID,
			segment.SegmentIndex,
			segment.SpeakerID,
			segment.StartMS,
			segment.EndMS,
			segment.Text,
			string(segment.RawJSON),
		); err != nil {
			return nil, err
		}
	}
	if err := refreshCallFactsTx(ctx, tx, payload.CallID); err != nil {
		return nil, err
	}
	if err := updateReadModelStateTx(ctx, tx, nowUTC(), "", false); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.transcriptByCallID(ctx, payload.CallID)
}

func (s *Store) FindCallsMissingTranscripts(ctx context.Context, limit int) ([]sqlite.MissingTranscriptCall, error) {
	limit = boundedLimit(limit, 100, 10000)
	rows, err := s.db.QueryContext(ctx, `SELECT c.call_id, c.title, c.started_at
FROM calls c
LEFT JOIN transcripts t ON t.call_id = c.call_id
WHERE t.call_id IS NULL
ORDER BY c.started_at DESC, c.call_id
LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []sqlite.MissingTranscriptCall
	for rows.Next() {
		var row sqlite.MissingTranscriptCall
		if err := rows.Scan(&row.CallID, &row.Title, &row.StartedAt); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) SearchTranscriptSegments(ctx context.Context, query string, limit int) ([]sqlite.TranscriptSearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("search query is required")
	}
	limit = boundedLimit(limit, defaultTranscriptSearchLimit, maxTranscriptSearchLimit)
	rows, err := s.db.QueryContext(ctx, `WITH q AS (
	SELECT websearch_to_tsquery('simple', $1) AS query
)
SELECT ts.call_id,
       ts.speaker_id,
       ts.segment_index,
       ts.start_ms,
       ts.end_ms,
       ts.text,
       ts_headline('simple', ts.text, q.query, 'StartSel=[, StopSel=], MaxWords=12, MinWords=4, ShortWord=2')
  FROM transcript_segments ts, q
 WHERE ts.search_vector @@ q.query
 ORDER BY ts_rank_cd(ts.search_vector, q.query) DESC, ts.call_id, ts.segment_index
 LIMIT $2`, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []sqlite.TranscriptSearchResult
	for rows.Next() {
		var row sqlite.TranscriptSearchResult
		if err := rows.Scan(&row.CallID, &row.SpeakerID, &row.SegmentIndex, &row.StartMS, &row.EndMS, &row.Text, &row.Snippet); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) SearchCallsRaw(ctx context.Context, params sqlite.CallSearchParams) ([]json.RawMessage, error) {
	if err := s.validateReadModelReady(ctx); err != nil {
		return nil, err
	}
	limit := boundedLimit(params.Limit, defaultCallSearchLimit, maxCallSearchLimit)
	query := `SELECT c.raw_json::text FROM calls c`
	where := []string{}
	args := []any{}
	addArg := func(value any) string {
		args = append(args, value)
		return fmt.Sprintf("$%d", len(args))
	}

	objectType := strings.TrimSpace(params.CRMObjectType)
	objectID := strings.TrimSpace(params.CRMObjectID)
	if objectType != "" || objectID != "" {
		subquery := []string{`o.call_id = c.call_id`}
		if objectType != "" {
			subquery = append(subquery, `o.object_type = `+addArg(objectType))
		}
		if objectID != "" {
			subquery = append(subquery, `o.object_id = `+addArg(objectID))
		}
		where = append(where, `EXISTS (SELECT 1 FROM call_context_objects o WHERE `+strings.Join(subquery, ` AND `)+`)`)
	}

	factWhere := []string{`cf.call_id = c.call_id`}
	var fromDate, toDate string
	if value := strings.TrimSpace(params.FromDate); value != "" {
		date, err := normalizePostgresDateFilter(value, "from_date")
		if err != nil {
			return nil, err
		}
		fromDate = date
		factWhere = append(factWhere, `cf.call_date >= `+addArg(date))
	}
	if value := strings.TrimSpace(params.ToDate); value != "" {
		date, err := normalizePostgresDateFilter(value, "to_date")
		if err != nil {
			return nil, err
		}
		toDate = date
		factWhere = append(factWhere, `cf.call_date <= `+addArg(date))
	}
	if fromDate != "" && toDate != "" && fromDate > toDate {
		return nil, errors.New("from_date must be on or before to_date")
	}
	if value := strings.TrimSpace(params.LifecycleBucket); value != "" {
		if !postgresKnownLifecycleBucket(value) {
			return nil, fmt.Errorf("unknown lifecycle bucket %q", value)
		}
		factWhere = append(factWhere, `cf.lifecycle_bucket = `+addArg(value))
	}
	if value := strings.TrimSpace(params.Scope); value != "" {
		scope, ok := normalizePostgresScope(value)
		if !ok {
			return nil, errors.New("scope must be one of: External, Internal, Unknown")
		}
		factWhere = append(factWhere, `cf.scope = `+addArg(scope))
	}
	if value := strings.TrimSpace(params.System); value != "" {
		factWhere = append(factWhere, `cf.system = `+addArg(value))
	}
	if value := strings.TrimSpace(params.Direction); value != "" {
		factWhere = append(factWhere, `cf.direction = `+addArg(value))
	}
	if value := strings.TrimSpace(params.TranscriptStatus); value != "" && value != "any" {
		status, ok := normalizePostgresTranscriptStatus(value)
		if !ok {
			return nil, errors.New("transcript_status must be one of: present, missing, any")
		}
		factWhere = append(factWhere, `cf.transcript_status = `+addArg(status))
	}
	if len(factWhere) > 1 {
		where = append(where, `EXISTS (SELECT 1 FROM call_facts cf WHERE `+strings.Join(factWhere, ` AND `)+`)`)
	}
	if len(where) > 0 {
		query += ` WHERE ` + strings.Join(where, ` AND `)
	}
	query += ` ORDER BY c.started_at DESC, c.call_id LIMIT ` + addArg(limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []json.RawMessage
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		out = append(out, json.RawMessage(raw))
	}
	return out, rows.Err()
}

func (s *Store) GetCallDetail(ctx context.Context, callID string) (*sqlite.CallDetail, error) {
	if err := s.validateReadModelReady(ctx); err != nil {
		return nil, err
	}
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return nil, errors.New("call id is required")
	}

	var detail sqlite.CallDetail
	if err := s.db.QueryRowContext(ctx, `SELECT call_id, title, started_at, duration_seconds, parties_count FROM calls WHERE call_id = $1`, callID).Scan(
		&detail.CallID,
		&detail.Title,
		&detail.StartedAt,
		&detail.DurationSeconds,
		&detail.PartiesCount,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("call %q not found", callID)
		}
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx, `
SELECT o.object_type,
       o.object_id,
       '' AS object_name,
       COUNT(f.id) AS field_count,
       COUNT(NULLIF(TRIM(f.field_value_text), '')) AS populated_field_count,
       COALESCE(string_agg(DISTINCT f.field_name, ',' ORDER BY f.field_name), '') AS field_names
  FROM call_context_objects o
  LEFT JOIN call_context_fields f
    ON f.call_id = o.call_id
   AND f.object_key = o.object_key
 WHERE o.call_id = $1
 GROUP BY o.object_key, o.object_type, o.object_id, o.object_name
 ORDER BY o.object_type, o.object_id, o.object_key`, callID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var object sqlite.CallDetailCRMObject
		var fieldNames string
		if err := rows.Scan(
			&object.ObjectType,
			&object.ObjectID,
			&object.ObjectName,
			&object.FieldCount,
			&object.PopulatedFieldCount,
			&fieldNames,
		); err != nil {
			return nil, err
		}
		object.FieldNames = splitPostgresCommaList(fieldNames)
		detail.CRMObjects = append(detail.CRMObjects, object)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return &detail, nil
}

func (s *Store) SyncStatusSummary(ctx context.Context) (*sqlite.SyncStatusSummary, error) {
	summary := &sqlite.SyncStatusSummary{}
	counts := []struct {
		query string
		dest  *int64
	}{
		{`SELECT COUNT(*) FROM calls`, &summary.TotalCalls},
		{`SELECT COUNT(*) FROM users`, &summary.TotalUsers},
		{`SELECT COUNT(*) FROM transcripts`, &summary.TotalTranscripts},
		{`SELECT COUNT(*) FROM transcript_segments`, &summary.TotalTranscriptSegments},
		{`SELECT COUNT(DISTINCT call_id) FROM call_context_objects`, &summary.TotalEmbeddedCRMContextCalls},
		{`SELECT COUNT(*) FROM call_context_objects`, &summary.TotalEmbeddedCRMObjects},
		{`SELECT COUNT(*) FROM call_context_fields`, &summary.TotalEmbeddedCRMFields},
		{`SELECT COUNT(*) FROM calls c LEFT JOIN transcripts t ON t.call_id = c.call_id WHERE t.call_id IS NULL`, &summary.MissingTranscripts},
		{`SELECT COUNT(*) FROM sync_runs WHERE status = 'running'`, &summary.RunningSyncRuns},
		{`SELECT COUNT(*) FROM calls WHERE TRIM(title) <> ''`, &summary.AttributionCoverage.CallsWithTitles},
		{`SELECT COUNT(*) FROM calls WHERE parties_count > 0`, &summary.AttributionCoverage.CallsWithParties},
		{`SELECT COUNT(*) FROM users WHERE TRIM(title) <> ''`, &summary.AttributionCoverage.UsersWithTitles},
	}
	for _, item := range counts {
		if err := s.db.QueryRowContext(ctx, item.query).Scan(item.dest); err != nil {
			return nil, err
		}
	}
	lastRun, err := s.latestSyncRun(ctx, `SELECT id, scope, sync_key, cursor, from_value, to_value, request_context, status, started_at, COALESCE(finished_at, ''), records_seen, records_written, error_text FROM sync_runs ORDER BY started_at DESC, id DESC LIMIT 1`)
	if err != nil {
		return nil, err
	}
	summary.LastRun = lastRun
	lastSuccess, err := s.latestSyncRun(ctx, `SELECT id, scope, sync_key, cursor, from_value, to_value, request_context, status, started_at, COALESCE(finished_at, ''), records_seen, records_written, error_text FROM sync_runs WHERE status = 'success' ORDER BY finished_at DESC, id DESC LIMIT 1`)
	if err != nil {
		return nil, err
	}
	summary.LastSuccessfulRun = lastSuccess
	states, err := s.syncStates(ctx)
	if err != nil {
		return nil, err
	}
	summary.States = states
	if summary.MissingTranscripts == 0 && summary.TotalCalls > 0 {
		summary.PublicReadiness.TranscriptCoverage = sqlite.ReadinessFlag{Ready: true, Status: "ready", Detail: "all cached calls have transcripts"}
	}
	return summary, nil
}

func (s *Store) callByID(ctx context.Context, callID string) (*sqlite.CallRecord, error) {
	var record sqlite.CallRecord
	if err := s.db.QueryRowContext(ctx, `SELECT call_id, title, started_at, duration_seconds, parties_count, context_present, raw_json::text, raw_sha256, first_seen_at, updated_at FROM calls WHERE call_id = $1`, callID).Scan(
		&record.CallID, &record.Title, &record.StartedAt, &record.DurationSeconds, &record.PartiesCount, &record.ContextPresent, &record.RawJSON, &record.RawSHA256, &record.FirstSeenAt, &record.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &record, nil
}

func (s *Store) userByID(ctx context.Context, userID string) (*sqlite.UserRecord, error) {
	var record sqlite.UserRecord
	if err := s.db.QueryRowContext(ctx, `SELECT user_id, email, first_name, last_name, display_name, title, active, raw_json::text, raw_sha256, first_seen_at, updated_at FROM users WHERE user_id = $1`, userID).Scan(
		&record.UserID, &record.Email, &record.FirstName, &record.LastName, &record.DisplayName, &record.Title, &record.Active, &record.RawJSON, &record.RawSHA256, &record.FirstSeenAt, &record.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &record, nil
}

func (s *Store) transcriptByCallID(ctx context.Context, callID string) (*sqlite.TranscriptRecord, error) {
	var record sqlite.TranscriptRecord
	if err := s.db.QueryRowContext(ctx, `SELECT call_id, segment_count, raw_json::text, raw_sha256, first_seen_at, updated_at FROM transcripts WHERE call_id = $1`, callID).Scan(
		&record.CallID, &record.SegmentCount, &record.RawJSON, &record.RawSHA256, &record.FirstSeenAt, &record.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &record, nil
}

func (s *Store) latestSyncRun(ctx context.Context, query string) (*sqlite.SyncRun, error) {
	var row sqlite.SyncRun
	if err := s.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.Scope, &row.SyncKey, &row.Cursor, &row.From, &row.To, &row.RequestContext, &row.Status, &row.StartedAt, &row.FinishedAt, &row.RecordsSeen, &row.RecordsWritten, &row.ErrorText); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

func (s *Store) syncStates(ctx context.Context) ([]sqlite.SyncState, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT sync_key, scope, cursor, COALESCE(last_run_id, 0), last_status, last_error, COALESCE(last_success_at, ''), updated_at FROM sync_state ORDER BY scope, sync_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []sqlite.SyncState
	for rows.Next() {
		var row sqlite.SyncState
		if err := rows.Scan(&row.SyncKey, &row.Scope, &row.Cursor, &row.LastRunID, &row.LastStatus, &row.LastError, &row.LastSuccessAt, &row.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

type callPayload struct {
	CallID          string
	Title           string
	StartedAt       string
	DurationSeconds int64
	PartiesCount    int64
	ContextPresent  bool
	HasContextBlock bool
	RawJSON         []byte
	RawSHA256       string
}

type userPayload struct {
	UserID      string
	Email       string
	FirstName   string
	LastName    string
	DisplayName string
	Title       string
	Active      bool
	RawJSON     []byte
	RawSHA256   string
}

type transcriptPayload struct {
	CallID    string
	RawJSON   []byte
	RawSHA256 string
	Segments  []sqlite.TranscriptSegment
}

func decodeCall(raw json.RawMessage) (*callPayload, error) {
	normalized, err := normalizeJSON(raw)
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := json.Unmarshal(normalized, &doc); err != nil {
		return nil, err
	}
	metaData := mapFromAny(doc["metaData"])
	callID := firstString(doc, "id", "callId")
	if callID == "" {
		callID = firstString(metaData, "id", "callId")
	}
	if callID == "" {
		return nil, errors.New("call payload missing id")
	}
	partiesCount := int64(0)
	if parties, ok := doc["parties"].([]any); ok {
		partiesCount = int64(len(parties))
	}
	if partiesCount == 0 {
		if parties, ok := metaData["parties"].([]any); ok {
			partiesCount = int64(len(parties))
		}
	}
	title := firstString(doc, "title")
	if title == "" {
		title = firstString(metaData, "title")
	}
	startedAt := firstString(doc, "started", "startedAt")
	if startedAt == "" {
		startedAt = firstString(metaData, "started", "startedAt")
	}
	durationSeconds := int64FromAny(doc["duration"])
	if durationSeconds == 0 {
		durationSeconds = int64FromAny(metaData["duration"])
	}
	hasContextBlock := hasAnyKey(doc, "context", "crmContext", "crm", "extendedContext", "crmObjects", "objects")
	return &callPayload{
		CallID:          callID,
		Title:           title,
		StartedAt:       startedAt,
		DurationSeconds: durationSeconds,
		PartiesCount:    partiesCount,
		ContextPresent:  hasContextBlock,
		HasContextBlock: hasContextBlock,
		RawJSON:         normalized,
		RawSHA256:       sha256Hex(normalized),
	}, nil
}

func (s *Store) ensureWritable() error {
	if s == nil || s.db == nil {
		return errors.New("postgres store is not open")
	}
	if s.readOnly {
		return errors.New("postgres store is read-only")
	}
	return nil
}

func callSearchExclusiveToDate(value string) (string, bool) {
	if len(value) != len("2006-01-02") {
		return "", false
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return "", false
	}
	return parsed.AddDate(0, 0, 1).Format("2006-01-02"), true
}

func decodeUser(raw json.RawMessage) (*userPayload, error) {
	normalized, err := normalizeJSON(raw)
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := json.Unmarshal(normalized, &doc); err != nil {
		return nil, err
	}
	userID := firstString(doc, "id", "userId")
	if userID == "" {
		return nil, errors.New("user payload missing id")
	}
	firstName := firstString(doc, "firstName", "first_name")
	lastName := firstString(doc, "lastName", "last_name")
	displayName := firstString(doc, "name", "displayName", "display_name")
	if displayName == "" {
		displayName = strings.TrimSpace(strings.TrimSpace(firstName) + " " + strings.TrimSpace(lastName))
	}
	return &userPayload{
		UserID:      userID,
		Email:       firstString(doc, "emailAddress", "email"),
		FirstName:   firstName,
		LastName:    lastName,
		DisplayName: displayName,
		Title:       firstString(doc, "title"),
		Active:      boolFromAny(doc["active"]),
		RawJSON:     normalized,
		RawSHA256:   sha256Hex(normalized),
	}, nil
}

func decodeTranscript(raw json.RawMessage) (*transcriptPayload, error) {
	normalized, err := normalizeJSON(raw)
	if err != nil {
		return nil, err
	}
	var envelope map[string]any
	if err := json.Unmarshal(normalized, &envelope); err != nil {
		return nil, err
	}
	record := envelope
	if wrapped, ok := envelope["callTranscripts"]; ok {
		items, ok := wrapped.([]any)
		if !ok || len(items) != 1 {
			return nil, errors.New("transcript payload must contain exactly one call transcript")
		}
		itemMap, ok := items[0].(map[string]any)
		if !ok {
			return nil, errors.New("call transcript item must be an object")
		}
		record = itemMap
		normalized, err = normalizeJSONValue(record)
		if err != nil {
			return nil, err
		}
	}
	callID := firstString(record, "callId", "id")
	if callID == "" {
		return nil, errors.New("transcript payload missing callId")
	}
	blocks, ok := record["transcript"].([]any)
	if !ok {
		return nil, errors.New("transcript payload missing transcript array")
	}
	segments := make([]sqlite.TranscriptSegment, 0)
	index := 0
	for _, block := range blocks {
		blockMap, ok := block.(map[string]any)
		if !ok {
			continue
		}
		speakerID := firstString(blockMap, "speakerId", "speakerID")
		sentences, ok := blockMap["sentences"].([]any)
		if !ok {
			continue
		}
		for _, sentence := range sentences {
			sentenceMap, ok := sentence.(map[string]any)
			if !ok {
				continue
			}
			segmentRaw, err := normalizeJSONValue(map[string]any{"speakerId": speakerID, "start": sentenceMap["start"], "end": sentenceMap["end"], "text": sentenceMap["text"]})
			if err != nil {
				return nil, err
			}
			segments = append(segments, sqlite.TranscriptSegment{
				CallID:       callID,
				SegmentIndex: index,
				SpeakerID:    speakerID,
				StartMS:      int64FromAny(sentenceMap["start"]),
				EndMS:        int64FromAny(sentenceMap["end"]),
				Text:         strings.TrimSpace(firstString(sentenceMap, "text")),
				RawJSON:      segmentRaw,
			})
			index++
		}
	}
	return &transcriptPayload{CallID: callID, RawJSON: normalized, RawSHA256: sha256Hex(normalized), Segments: segments}, nil
}

func normalizeJSON(raw json.RawMessage) ([]byte, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, errors.New("json payload is required")
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, trimmed); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func normalizeJSONValue(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return normalizeJSON(encoded)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func nowUTC() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func boundedLimit(value int, defaultValue int, maxValue int) int {
	if value <= 0 {
		return defaultValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func firstString(doc map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := doc[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			return strings.TrimSpace(typed)
		case fmt.Stringer:
			return strings.TrimSpace(typed.String())
		}
	}
	return ""
}

func int64FromAny(value any) int64 {
	switch typed := value.(type) {
	case float64:
		return int64(typed)
	case int:
		return int64(typed)
	case int64:
		return typed
	case json.Number:
		parsed, _ := typed.Int64()
		return parsed
	}
	return 0
}

func boolFromAny(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true", "1", "yes", "active", "enabled":
			return true
		}
	case float64:
		return typed != 0
	}
	return false
}

func mapFromAny(value any) map[string]any {
	if typed, ok := value.(map[string]any); ok {
		return typed
	}
	return nil
}

func hasAnyKey(doc map[string]any, keys ...string) bool {
	for _, key := range keys {
		if _, ok := doc[key]; ok {
			return true
		}
	}
	return false
}
