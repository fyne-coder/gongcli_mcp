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
	defaultCRMFieldLimit         = 50
	maxCRMFieldLimit             = 1000
)

const postgresReadModelBackfillMigrationVersion = 2

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
	query.Set("search_path", "public,pg_temp")
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
	if err := store.setReadOnlySearchPath(ctx, "read-only"); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.validateMigrationVersion(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
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
	db, err := sql.Open("pgx", readOnlyDatabaseURL(databaseURL))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(30 * time.Minute)
	store := &Store{db: db, readOnly: true}
	if err := store.setReadOnlySearchPath(ctx, "profile-inventory"); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.validateMigrationVersion(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.validateSchema(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.ExecContext(ctx, `SET default_transaction_read_only = on`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("enable postgres status read-only session mode: %w", err)
	}
	var readOnlyMode string
	if err := db.QueryRowContext(ctx, `SHOW default_transaction_read_only`).Scan(&readOnlyMode); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("verify postgres status read-only session mode: %w", err)
	}
	if strings.ToLower(strings.TrimSpace(readOnlyMode)) != "on" {
		_ = db.Close()
		return nil, fmt.Errorf("postgres status read-only session mode is %q, want on", readOnlyMode)
	}
	return store, nil
}

func OpenProfileInventory(ctx context.Context, databaseURL string) (*Store, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, errors.New("postgres database URL is required")
	}
	db, err := sql.Open("pgx", readOnlyDatabaseURL(databaseURL))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(30 * time.Minute)
	store := &Store{db: db, readOnly: true}
	if err := store.validateMigrationVersion(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.validateSchema(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.ExecContext(ctx, `SET default_transaction_read_only = on`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("enable postgres profile-inventory read-only session mode: %w", err)
	}
	return store, nil
}

func (s *Store) setReadOnlySearchPath(ctx context.Context, purpose string) error {
	if _, err := s.db.ExecContext(ctx, `SET search_path = public, pg_temp`); err != nil {
		return fmt.Errorf("set postgres %s search_path: %w", purpose, err)
	}
	var currentSchema string
	if err := s.db.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&currentSchema); err != nil {
		return fmt.Errorf("verify postgres %s search_path: %w", purpose, err)
	}
	if strings.TrimSpace(currentSchema) != "public" {
		return fmt.Errorf("postgres %s search_path resolved current_schema=%q, want public", purpose, currentSchema)
	}
	return nil
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
	if err := s.reconcileReaderGrants(ctx); err != nil {
		return err
	}
	if shouldBackfillReadModelAfterMigrations(startingVersion) {
		if _, err := s.RebuildReadModel(ctx); err != nil {
			return fmt.Errorf("backfill postgres read model: %w", err)
		}
	}
	return nil
}

func shouldBackfillReadModelAfterMigrations(startingVersion int) bool {
	return startingVersion < postgresReadModelBackfillMigrationVersion
}

func (s *Store) reconcileReaderGrants(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errors.New("postgres store is not open")
	}
	_, err := s.db.ExecContext(ctx, `
CREATE OR REPLACE VIEW gongmcp_sync_runs AS SELECT id, scope, sync_key, ''::text AS cursor, from_value, to_value, ''::text AS request_context, status, started_at, finished_at, records_seen, records_written, ''::text AS error_text FROM sync_runs;
	CREATE OR REPLACE VIEW gongmcp_sync_state AS SELECT sync_key, scope, ''::text AS cursor, last_run_id, last_status, ''::text AS last_error, last_success_at, updated_at FROM sync_state;
	CREATE OR REPLACE VIEW gongmcp_call_context_fields AS SELECT id, call_id, object_key, field_name, field_label, field_type, (TRIM(field_value_text) <> '') AS field_populated FROM call_context_fields;
	DROP VIEW IF EXISTS gongmcp_transcript_segments;
	DROP FUNCTION IF EXISTS gongmcp_crm_field_summary(text, integer);
	`+postgresBusinessAnalysisFunctionsSQL+`
	`+postgresSettingsFunctionsSQL+`
	`+postgresScorecardActivityFunctionsSQL+`
	DROP FUNCTION IF EXISTS gongmcp_profile_call_fact_cache_meta(bigint, text);
CREATE OR REPLACE FUNCTION gongmcp_profile_call_fact_cache_meta(profile_id_arg bigint, canonical_sha_arg text)
RETURNS TABLE(canonical_sha256 text, data_fingerprint text, built_at text, call_count bigint)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = public, pg_temp
AS $function$
SELECT m.canonical_sha256, m.data_fingerprint, m.built_at, m.call_count
  FROM profile_call_fact_cache_meta m
  JOIN profile_meta p
    ON p.id = m.profile_id
 WHERE p.is_active = true
   AND m.profile_id = profile_id_arg
   AND m.canonical_sha256 = canonical_sha_arg
$function$;
REVOKE ALL ON FUNCTION gongmcp_profile_call_fact_cache_meta(bigint, text) FROM PUBLIC;
DROP FUNCTION IF EXISTS gongmcp_profile_data_fingerprint();
CREATE OR REPLACE FUNCTION gongmcp_profile_data_fingerprint()
RETURNS TABLE(fingerprint text)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = public, pg_temp
AS $function$
SELECT 'calls:' || (SELECT COUNT(*) FROM calls)::text || ':' || COALESCE((SELECT MAX(updated_at) FROM calls), '') ||
       '|objects:' || (SELECT COUNT(*) FROM call_context_objects)::text || ':' || COALESCE((SELECT md5(COALESCE(string_agg(call_id || E'\x1f' || object_key || E'\x1f' || object_type || E'\x1f' || object_id || E'\x1f' || object_name, E'\x1e' ORDER BY call_id, object_key), '')) FROM call_context_objects), '') ||
       '|fields:' || (SELECT COUNT(*) FROM call_context_fields)::text || ':' || COALESCE((SELECT md5(COALESCE(string_agg(call_id || E'\x1f' || object_key || E'\x1f' || field_name || E'\x1f' || field_label || E'\x1f' || field_type || E'\x1f' || field_value_text, E'\x1e' ORDER BY call_id, object_key, field_name), '')) FROM call_context_fields), '') ||
       '|transcripts:' || (SELECT COUNT(*) FROM transcripts)::text || ':' || COALESCE((SELECT MAX(updated_at) FROM transcripts), '') AS fingerprint
$function$;
REVOKE ALL ON FUNCTION gongmcp_profile_data_fingerprint() FROM PUBLIC;
DROP FUNCTION IF EXISTS gongmcp_profile_call_fact_cache(bigint, text);
CREATE OR REPLACE FUNCTION gongmcp_profile_call_fact_cache(profile_id_arg bigint, canonical_sha_arg text)
RETURNS TABLE(
	call_id text,
	title text,
	started_at text,
	duration_seconds bigint,
	system text,
	direction text,
	scope text,
	purpose text,
	calendar_event_present boolean,
	transcript_present boolean,
	lifecycle_bucket text,
	lifecycle_confidence text,
	lifecycle_reason text,
	evidence_fields_json jsonb,
	deal_count bigint,
	account_count bigint
)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = public, pg_temp
AS $function$
SELECT c.call_id,
       c.title,
       c.started_at,
       c.duration_seconds,
       c.system,
       c.direction,
       c.scope,
       c.purpose,
       c.calendar_event_present,
       c.transcript_present,
       c.lifecycle_bucket,
       c.lifecycle_confidence,
       c.lifecycle_reason,
       c.evidence_fields_json,
       c.deal_count,
       c.account_count
  FROM profile_call_fact_cache c
  JOIN profile_meta p
    ON p.id = c.profile_id
 WHERE p.is_active = true
   AND c.profile_id = profile_id_arg
   AND c.canonical_sha256 = canonical_sha_arg
$function$;
REVOKE ALL ON FUNCTION gongmcp_profile_call_fact_cache(bigint, text) FROM PUBLIC;

DROP FUNCTION IF EXISTS gongmcp_profile_call_fact_summary(bigint, text, text, text, text, text, text, text, integer);
CREATE OR REPLACE FUNCTION gongmcp_profile_call_fact_summary(profile_id_arg bigint, canonical_sha_arg text, group_by_arg text, lifecycle_bucket_arg text, scope_arg text, system_arg text, direction_arg text, transcript_status_arg text, row_limit integer)
RETURNS TABLE(
	group_by text,
	group_value text,
	call_count bigint,
	transcript_count bigint,
	missing_transcript_count bigint,
	opportunity_call_count bigint,
	account_call_count bigint,
	external_call_count bigint,
	internal_call_count bigint,
	unknown_scope_call_count bigint,
	total_duration_seconds bigint,
	avg_duration_seconds double precision,
	latest_call_at text
)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = public, pg_temp
AS $function$
WITH filtered AS (
	SELECT c.*,
	       CASE group_by_arg
		       WHEN 'lifecycle' THEN COALESCE(NULLIF(c.lifecycle_bucket, ''), '<blank>')
		       WHEN 'scope' THEN COALESCE(NULLIF(c.scope, ''), '<blank>')
		       WHEN 'system' THEN COALESCE(NULLIF(c.system, ''), '<blank>')
		       WHEN 'direction' THEN COALESCE(NULLIF(c.direction, ''), '<blank>')
		       WHEN 'transcript_status' THEN CASE WHEN c.transcript_present THEN 'present' ELSE 'missing' END
		       WHEN 'duration_bucket' THEN CASE WHEN c.duration_seconds < 60 THEN 'under_1m' WHEN c.duration_seconds < 300 THEN '1_5m' WHEN c.duration_seconds < 900 THEN '5_15m' WHEN c.duration_seconds < 1800 THEN '15_30m' WHEN c.duration_seconds < 2700 THEN '30_45m' ELSE '45m_plus' END
		       WHEN 'month' THEN COALESCE(NULLIF(left(c.started_at, 7), ''), '<blank>')
		       WHEN 'calendar' THEN CASE WHEN c.calendar_event_present THEN 'calendar' ELSE 'no_calendar' END
		       ELSE COALESCE(NULLIF(jsonb_extract_path_text(c.field_values_json, group_by_arg, '0'), ''), '<blank>')
	       END AS group_value
	  FROM profile_call_fact_cache c
	  JOIN profile_meta p
	    ON p.id = c.profile_id
	 WHERE p.is_active = true
	   AND c.profile_id = profile_id_arg
	   AND c.canonical_sha256 = canonical_sha_arg
	   AND (lifecycle_bucket_arg = '' OR c.lifecycle_bucket = lifecycle_bucket_arg)
	   AND (scope_arg = '' OR c.scope = scope_arg)
	   AND (system_arg = '' OR c.system = system_arg)
	   AND (direction_arg = '' OR c.direction = direction_arg)
	   AND (transcript_status_arg = '' OR (transcript_status_arg = 'present' AND c.transcript_present) OR (transcript_status_arg = 'missing' AND NOT c.transcript_present))
)
SELECT group_by_arg AS group_by,
       group_value,
       COUNT(*) AS call_count,
       COALESCE(SUM(CASE WHEN transcript_present THEN 1 ELSE 0 END), 0) AS transcript_count,
       COALESCE(SUM(CASE WHEN NOT transcript_present THEN 1 ELSE 0 END), 0) AS missing_transcript_count,
       COALESCE(SUM(CASE WHEN deal_count > 0 THEN 1 ELSE 0 END), 0) AS opportunity_call_count,
       COALESCE(SUM(CASE WHEN account_count > 0 THEN 1 ELSE 0 END), 0) AS account_call_count,
       COALESCE(SUM(CASE WHEN scope = 'External' THEN 1 ELSE 0 END), 0) AS external_call_count,
       COALESCE(SUM(CASE WHEN scope = 'Internal' THEN 1 ELSE 0 END), 0) AS internal_call_count,
       COALESCE(SUM(CASE WHEN scope NOT IN ('External', 'Internal') THEN 1 ELSE 0 END), 0) AS unknown_scope_call_count,
       COALESCE(SUM(duration_seconds), 0) AS total_duration_seconds,
       COALESCE(AVG(duration_seconds), 0) AS avg_duration_seconds,
       COALESCE(MAX(started_at), '') AS latest_call_at
  FROM filtered
 GROUP BY group_value
 ORDER BY call_count DESC, group_value
 LIMIT LEAST(GREATEST(COALESCE(row_limit, 25), 1), 1000)
$function$;

REVOKE ALL ON FUNCTION gongmcp_profile_call_fact_summary(bigint, text, text, text, text, text, text, text, integer) FROM PUBLIC;

CREATE OR REPLACE FUNCTION gongmcp_search_transcript_segments(search_text text, row_limit integer)
RETURNS TABLE(call_id text, speaker_id text, segment_index integer, start_ms bigint, end_ms bigint, text text, snippet text)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = public, pg_temp
AS $function$
WITH q AS (
	SELECT websearch_to_tsquery('simple', search_text) AS query
),
bounded AS (
	SELECT LEAST(GREATEST(COALESCE(row_limit, 20), 1), 1000) AS limit_value
)
SELECT ts.call_id,
       ts.speaker_id,
       ts.segment_index,
       ts.start_ms,
       ts.end_ms,
       ''::text AS text,
       ts_headline('simple', ts.text, q.query, 'StartSel=[, StopSel=], MaxWords=12, MinWords=4, ShortWord=2') AS snippet
  FROM transcript_segments ts, q, bounded
 WHERE ts.search_vector @@ q.query
 ORDER BY ts_rank_cd(ts.search_vector, q.query) DESC, ts.call_id, ts.segment_index
 LIMIT (SELECT limit_value FROM bounded)
$function$;
REVOKE ALL ON FUNCTION gongmcp_search_transcript_segments(text, integer) FROM PUBLIC;
CREATE OR REPLACE FUNCTION gongmcp_active_business_profile()
RETURNS TABLE(profile_json jsonb)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = public, pg_temp
AS $function$
SELECT jsonb_build_object(
	'profile_id', p.id,
	'name', p.name,
	'version', p.version,
	'source_path', p.source_path,
	'source_sha256', p.source_sha256,
	'canonical_sha256', p.canonical_sha256,
	'imported_at', p.imported_at,
	'imported_by', p.imported_by,
	'is_active', p.is_active,
	'profile', jsonb_build_object(
		'version', p.version,
		'name', p.name,
		'objects', COALESCE((
			SELECT jsonb_object_agg(o.concept, jsonb_build_object('object_types', o.object_types) ORDER BY o.concept)
			  FROM (
				SELECT concept, jsonb_agg(object_type ORDER BY object_type) AS object_types
				  FROM profile_object_alias
				 WHERE profile_id = p.id
				 GROUP BY concept
			  ) o
		), '{}'::jsonb),
		'fields', COALESCE((
			SELECT jsonb_object_agg(f.concept, jsonb_build_object('object', f.object_concept, 'names', f.names, 'confidence', f.confidence, 'evidence', f.evidence_json) ORDER BY f.concept)
			  FROM (
				SELECT concept, object_concept, confidence, evidence_json, jsonb_agg(field_name ORDER BY field_name) AS names
				  FROM profile_field_concept
				 WHERE profile_id = p.id
				 GROUP BY concept, object_concept, confidence, evidence_json
			  ) f
		), '{}'::jsonb),
		'lifecycle', COALESCE((
			SELECT jsonb_object_agg(l.bucket, jsonb_build_object('order', l.ordinal, 'label', l.label, 'description', l.description, 'rules', l.rules) ORDER BY l.ordinal, l.bucket)
			  FROM (
				SELECT bucket,
				       MAX(ordinal) AS ordinal,
				       MAX(label) AS label,
				       MAX(description) AS description,
				       COALESCE(jsonb_agg(rule_json ORDER BY rule_index) FILTER (WHERE rule_index >= 0), '[]'::jsonb) AS rules
				  FROM profile_lifecycle_rule
				 WHERE profile_id = p.id
				 GROUP BY bucket
			  ) l
		), '{}'::jsonb),
		'methodology', COALESCE((
			SELECT jsonb_object_agg(m.concept, jsonb_build_object('description', m.description, 'aliases', m.aliases_json, 'fields', m.fields_json, 'tracker_ids', m.tracker_ids_json, 'scorecard_question_ids', m.scorecard_question_ids_json) ORDER BY m.concept)
			  FROM profile_methodology_concept m
			 WHERE m.profile_id = p.id
		), '{}'::jsonb)
	),
	'warnings', COALESCE((
		SELECT jsonb_agg(jsonb_build_object('severity', w.severity, 'code', w.code, 'message', w.message, 'path', w.path)
			ORDER BY CASE w.severity WHEN 'error' THEN 1 WHEN 'warn' THEN 2 ELSE 3 END, w.path, w.code)
		  FROM profile_validation_warning w
		 WHERE w.profile_id = p.id
	), '[]'::jsonb)
) AS profile_json
  FROM profile_meta p
 WHERE p.is_active = true
 ORDER BY p.id DESC
 LIMIT 1
$function$;
REVOKE ALL ON FUNCTION gongmcp_active_business_profile() FROM PUBLIC;
DO $$
BEGIN
	IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'gongmcp_reader') THEN
		REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA public FROM gongmcp_reader;
		ALTER DEFAULT PRIVILEGES IN SCHEMA public REVOKE SELECT ON TABLES FROM gongmcp_reader;
		EXECUTE 'GRANT CONNECT ON DATABASE ' || quote_ident(current_database()) || ' TO gongmcp_reader';
		GRANT USAGE ON SCHEMA public TO gongmcp_reader;
		GRANT SELECT ON TABLE gongctl_schema_migrations TO gongmcp_reader;
		GRANT SELECT ON TABLE gongmcp_sync_runs TO gongmcp_reader;
		GRANT SELECT ON TABLE gongmcp_sync_state TO gongmcp_reader;
		GRANT SELECT (call_id, title, started_at, duration_seconds, parties_count, context_present, first_seen_at, updated_at) ON calls TO gongmcp_reader;
		GRANT SELECT (user_id, title, active, first_seen_at, updated_at) ON users TO gongmcp_reader;
		GRANT SELECT (call_id, segment_count, first_seen_at, updated_at) ON transcripts TO gongmcp_reader;
		GRANT EXECUTE ON FUNCTION gongmcp_search_transcript_segments(text, integer) TO gongmcp_reader;
		GRANT SELECT (id, call_id, object_key, object_type, object_id) ON call_context_objects TO gongmcp_reader;
		GRANT SELECT ON TABLE gongmcp_call_context_fields TO gongmcp_reader;
		GRANT SELECT (call_id, title, started_at, call_date, call_month, duration_seconds, duration_bucket, system, direction, scope, purpose, calendar_event_present, transcript_present, transcript_status, lifecycle_bucket, lifecycle_confidence, lifecycle_reason, lifecycle_evidence_fields, account_type, account_industry, account_revenue_range, opportunity_stage, opportunity_type, opportunity_forecast_category, opportunity_primary_lead_source, opportunity_count, account_count) ON call_facts TO gongmcp_reader;
		GRANT SELECT ON TABLE postgres_read_model_state TO gongmcp_reader;
		GRANT SELECT ON TABLE call_read_model_diagnostics TO gongmcp_reader;
		GRANT EXECUTE ON FUNCTION gongmcp_active_business_profile() TO gongmcp_reader;
		GRANT EXECUTE ON FUNCTION gongmcp_profile_call_fact_cache_meta(bigint, text) TO gongmcp_reader;
			GRANT EXECUTE ON FUNCTION gongmcp_profile_data_fingerprint() TO gongmcp_reader;
			GRANT EXECUTE ON FUNCTION gongmcp_profile_call_fact_cache(bigint, text) TO gongmcp_reader;
			GRANT EXECUTE ON FUNCTION gongmcp_profile_call_fact_summary(bigint, text, text, text, text, text, text, text, integer) TO gongmcp_reader;
			GRANT EXECUTE ON FUNCTION gongmcp_governance_data_fingerprint() TO gongmcp_reader;
			GRANT EXECUTE ON FUNCTION gongmcp_governance_policy_state(text) TO gongmcp_reader;
				GRANT EXECUTE ON FUNCTION gongmcp_governance_suppressed_call_ids(text) TO gongmcp_reader;
	`+postgresBusinessAnalysisReaderGrantStatementsSQL+`
	`+postgresSettingsReaderGrantStatementsSQL+`
	`+postgresScorecardActivityReaderGrantStatementsSQL+`
	`+postgresCRMInventoryReaderGrantStatementsSQL+`
			END IF;
		END;
		$$;`)
	if err != nil {
		return fmt.Errorf("reconcile postgres reader grants: %w", err)
	}
	return nil
}

func (s *Store) validateSchema(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errors.New("postgres store is not open")
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*)
	  FROM unnest(ARRAY[
		'sync_runs',
		'sync_state',
		'calls',
		'users',
		'transcripts',
		'transcript_segments',
		'call_context_objects',
		'call_context_fields',
		'call_facts',
		'postgres_read_model_state',
		'call_read_model_diagnostics',
		'profile_meta',
		'profile_object_alias',
		'profile_field_concept',
		'profile_lifecycle_rule',
		'profile_methodology_concept',
			'profile_validation_warning',
			'profile_call_fact_cache_meta',
			'profile_call_fact_cache',
					'governance_policy_state',
					'governance_suppressed_calls',
					'gong_settings',
					'scorecard_activity',
					'crm_integrations',
					'crm_schema_objects',
					'crm_schema_fields'
				  ]) AS core_table(table_name)
				 WHERE to_regclass(core_table.table_name) IS NOT NULL`).Scan(&count); err != nil {
		return err
	}
	if count != 26 {
		return fmt.Errorf("postgres schema is not initialized: found %d/26 core tables", count)
	}
	return nil
}

func (s *Store) validateMigrationVersion(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errors.New("postgres store is not open")
	}
	var migrationTable any
	if err := s.db.QueryRowContext(ctx, `SELECT to_regclass('gongctl_schema_migrations')`).Scan(&migrationTable); err != nil {
		return fmt.Errorf("read postgres migration table: %w", err)
	}
	if migrationTable == nil {
		return fmt.Errorf("postgres schema is not initialized; run gongctl sync with a writable Postgres URL to migrate to version %d", len(migrations))
	}
	var current int
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM gongctl_schema_migrations`).Scan(&current); err != nil {
		return fmt.Errorf("read postgres migration version: %w", err)
	}
	if current != len(migrations) {
		return fmt.Errorf("postgres schema version %d is not current; run gongctl sync with a writable Postgres URL to migrate to version %d", current, len(migrations))
	}
	return nil
}

func (s *Store) validateReadOnlyPrivileges(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `
SELECT table_name
  FROM information_schema.tables
 WHERE table_schema = 'public'
			   AND table_name IN ('calls', 'users', 'transcripts', 'transcript_segments', 'sync_runs', 'sync_state', 'call_context_objects', 'call_context_fields', 'call_facts', 'postgres_read_model_state', 'call_read_model_diagnostics', 'profile_meta', 'profile_object_alias', 'profile_field_concept', 'profile_lifecycle_rule', 'profile_methodology_concept', 'profile_validation_warning', 'profile_call_fact_cache_meta', 'profile_call_fact_cache', 'governance_policy_state', 'governance_suppressed_calls', 'gong_settings', 'scorecard_activity', 'crm_integrations', 'crm_schema_objects', 'crm_schema_fields')
   AND (
	has_table_privilege(current_user, quote_ident(table_schema) || '.' || quote_ident(table_name), 'INSERT') OR
	has_table_privilege(current_user, quote_ident(table_schema) || '.' || quote_ident(table_name), 'UPDATE') OR
	has_table_privilege(current_user, quote_ident(table_schema) || '.' || quote_ident(table_name), 'DELETE') OR
	has_table_privilege(current_user, quote_ident(table_schema) || '.' || quote_ident(table_name), 'TRUNCATE') OR
	has_any_column_privilege(current_user, quote_ident(table_schema) || '.' || quote_ident(table_name), 'INSERT') OR
	has_any_column_privilege(current_user, quote_ident(table_schema) || '.' || quote_ident(table_name), 'UPDATE')
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
	rows, err = s.db.QueryContext(ctx, `
SELECT table_name
  FROM information_schema.tables
 WHERE table_schema = 'public'
			   AND table_name IN ('transcript_segments', 'sync_runs', 'sync_state', 'call_context_fields', 'profile_meta', 'profile_object_alias', 'profile_field_concept', 'profile_lifecycle_rule', 'profile_methodology_concept', 'profile_validation_warning', 'profile_call_fact_cache_meta', 'profile_call_fact_cache', 'governance_policy_state', 'governance_suppressed_calls', 'scorecard_activity')
   AND (
	has_table_privilege(current_user, quote_ident(table_schema) || '.' || quote_ident(table_name), 'SELECT') OR
	has_any_column_privilege(current_user, quote_ident(table_schema) || '.' || quote_ident(table_name), 'SELECT')
   )
 ORDER BY table_name`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var readableSensitive []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			return err
		}
		readableSensitive = append(readableSensitive, table)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(readableSensitive) > 0 {
		return fmt.Errorf("postgres read-only URL has direct SELECT on sensitive tables: %s", strings.Join(readableSensitive, ", "))
	}
	rows, err = s.db.QueryContext(ctx, `
WITH forbidden(table_name, column_name) AS (
	VALUES
		('calls', 'raw_json'),
		('calls', 'raw_sha256'),
		('users', 'email'),
		('users', 'first_name'),
		('users', 'last_name'),
		('users', 'display_name'),
		('users', 'raw_json'),
		('users', 'raw_sha256'),
		('transcripts', 'raw_json'),
		('transcripts', 'raw_sha256'),
		('transcript_segments', 'text'),
		('transcript_segments', 'raw_json'),
		('transcript_segments', 'search_vector'),
		('call_context_objects', 'object_name'),
		('call_context_objects', 'raw_json'),
		('call_context_fields', 'field_value_text'),
		('call_context_fields', 'raw_json'),
		('call_facts', 'primary_user_id'),
		('call_facts', 'calendar_event_status'),
		('call_facts', 'sdr_disposition'),
		('call_facts', 'account_id'),
		('call_facts', 'account_primary_procurement_system'),
			('call_facts', 'opportunity_id'),
			('call_facts', 'opportunity_amount'),
			('call_facts', 'opportunity_probability'),
				('call_facts', 'opportunity_procurement_system'),
				('gong_settings', 'raw_json'),
				('gong_settings', 'raw_sha256'),
				('scorecard_activity', 'raw_json'),
				('scorecard_activity', 'raw_sha256'),
				('crm_integrations', 'raw_json'),
				('crm_integrations', 'raw_sha256'),
				('crm_schema_objects', 'raw_json'),
				('crm_schema_objects', 'raw_sha256'),
				('crm_schema_fields', 'raw_json'),
				('crm_schema_fields', 'raw_sha256')
)
SELECT c.table_name || '.' || c.column_name
  FROM information_schema.columns c
  JOIN forbidden f
    ON f.table_name = c.table_name
   AND f.column_name = c.column_name
 WHERE c.table_schema = 'public'
   AND has_column_privilege(current_user, quote_ident(c.table_schema) || '.' || quote_ident(c.table_name), c.column_name, 'SELECT')
 ORDER BY c.table_name, c.column_name`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var readableForbiddenColumns []string
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			return err
		}
		readableForbiddenColumns = append(readableForbiddenColumns, column)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(readableForbiddenColumns) > 0 {
		return fmt.Errorf("postgres read-only URL has forbidden column SELECT: %s", strings.Join(readableForbiddenColumns, ", "))
	}
	rows, err = s.db.QueryContext(ctx, `
WITH required(table_name, column_name) AS (
	VALUES
		('gong_settings', 'kind'),
		('gong_settings', 'object_id'),
		('gong_settings', 'name'),
		('gong_settings', 'active'),
		('gong_settings', 'updated_at'),
		('crm_integrations', 'integration_id'),
		('crm_integrations', 'name'),
		('crm_integrations', 'provider'),
		('crm_integrations', 'first_seen_at'),
		('crm_integrations', 'updated_at'),
		('crm_schema_objects', 'integration_id'),
		('crm_schema_objects', 'object_type'),
		('crm_schema_objects', 'display_name'),
		('crm_schema_objects', 'field_count'),
		('crm_schema_objects', 'first_seen_at'),
		('crm_schema_objects', 'updated_at'),
		('crm_schema_fields', 'integration_id'),
		('crm_schema_fields', 'object_type'),
		('crm_schema_fields', 'field_name'),
		('crm_schema_fields', 'field_label'),
		('crm_schema_fields', 'field_type'),
		('crm_schema_fields', 'first_seen_at'),
		('crm_schema_fields', 'updated_at')
)
SELECT r.table_name || '.' || r.column_name
  FROM required r
  JOIN pg_attribute a
    ON a.attrelid = to_regclass('public.' || quote_ident(r.table_name))
   AND a.attname = r.column_name
   AND NOT a.attisdropped
 WHERE NOT has_column_privilege(current_user, 'public.' || quote_ident(r.table_name), r.column_name, 'SELECT')
 ORDER BY r.table_name, r.column_name`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var missingColumnGrants []string
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			return err
		}
		missingColumnGrants = append(missingColumnGrants, column)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(missingColumnGrants) > 0 {
		return fmt.Errorf("postgres read-only URL is missing required column SELECT grants: %s", strings.Join(missingColumnGrants, ", "))
	}
	rows, err = s.db.QueryContext(ctx, `
WITH required_functions(signature) AS (
	VALUES
		('public.gongmcp_scorecard_activity_summary(text, integer)'),
		('public.gongmcp_scorecard_activity_totals()')
)
SELECT signature
  FROM required_functions
 WHERE NOT has_function_privilege(current_user, signature, 'EXECUTE')
 ORDER BY signature`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var missingFunctionGrants []string
	for rows.Next() {
		var signature string
		if err := rows.Scan(&signature); err != nil {
			return err
		}
		missingFunctionGrants = append(missingFunctionGrants, signature)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(missingFunctionGrants) > 0 {
		return fmt.Errorf("postgres read-only URL is missing required function EXECUTE grants: %s", strings.Join(missingFunctionGrants, ", "))
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
	rows, err := s.db.QueryContext(ctx, `SELECT call_id, speaker_id, segment_index, start_ms, end_ms, text, snippet
  FROM gongmcp_search_transcript_segments($1, $2)`, query, limit)
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
	query := `SELECT jsonb_build_object(
		'id', c.call_id,
		'title', c.title,
		'started', c.started_at,
		'duration', c.duration_seconds,
		'parties', COALESCE((SELECT jsonb_agg(jsonb_build_object('id', 'redacted')) FROM generate_series(1::bigint, c.parties_count)), '[]'::jsonb)
	)::text FROM calls c`
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
	       COUNT(CASE WHEN f.field_populated THEN 1 END) AS populated_field_count,
	       COALESCE(string_agg(DISTINCT f.field_name, ',' ORDER BY f.field_name), '') AS field_names
	  FROM call_context_objects o
	  LEFT JOIN gongmcp_call_context_fields f
	    ON f.call_id = o.call_id
	   AND f.object_key = o.object_key
 WHERE o.call_id = $1
	 GROUP BY o.object_key, o.object_type, o.object_id
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
		{`SELECT COALESCE(SUM(segment_count), 0) FROM transcripts`, &summary.TotalTranscriptSegments},
		{`SELECT COUNT(DISTINCT call_id) FROM call_context_objects`, &summary.TotalEmbeddedCRMContextCalls},
		{`SELECT COUNT(*) FROM call_context_objects`, &summary.TotalEmbeddedCRMObjects},
		{`SELECT COUNT(*) FROM gongmcp_call_context_fields`, &summary.TotalEmbeddedCRMFields},
		{`SELECT COUNT(*) FROM crm_integrations`, &summary.TotalCRMIntegrations},
		{`SELECT COUNT(*) FROM crm_schema_objects`, &summary.TotalCRMSchemaObjects},
		{`SELECT COUNT(*) FROM crm_schema_fields`, &summary.TotalCRMSchemaFields},
		{`SELECT COUNT(*) FROM gong_settings`, &summary.TotalGongSettings},
		{`SELECT COUNT(*) FROM gong_settings WHERE kind = 'scorecards'`, &summary.TotalScorecards},
		{`SELECT COUNT(*) FROM calls c LEFT JOIN transcripts t ON t.call_id = c.call_id WHERE t.call_id IS NULL`, &summary.MissingTranscripts},
		{`SELECT COUNT(*) FROM gongmcp_sync_runs WHERE status = 'running'`, &summary.RunningSyncRuns},
		{`SELECT COUNT(*) FROM calls WHERE TRIM(title) <> ''`, &summary.AttributionCoverage.CallsWithTitles},
		{`SELECT COUNT(*) FROM calls WHERE parties_count > 0`, &summary.AttributionCoverage.CallsWithParties},
		{`SELECT COUNT(*) FROM users WHERE TRIM(title) <> ''`, &summary.AttributionCoverage.UsersWithTitles},
	}
	for _, item := range counts {
		if err := s.db.QueryRowContext(ctx, item.query).Scan(item.dest); err != nil {
			return nil, err
		}
	}
	scorecardActivityCountQuery := `SELECT COUNT(*) FROM scorecard_activity`
	if s.readOnly {
		scorecardActivityCountQuery = `SELECT total_answered_scorecards FROM gongmcp_scorecard_activity_totals()`
	}
	if err := s.db.QueryRowContext(ctx, scorecardActivityCountQuery).Scan(&summary.TotalScorecardActivity); err != nil {
		return nil, err
	}
	lastRun, err := s.latestSyncRun(ctx, `SELECT id, scope, sync_key, cursor, from_value, to_value, request_context, status, started_at, COALESCE(finished_at, ''), records_seen, records_written, error_text FROM gongmcp_sync_runs ORDER BY started_at DESC, id DESC LIMIT 1`)
	if err != nil {
		return nil, err
	}
	summary.LastRun = lastRun
	lastSuccess, err := s.latestSyncRun(ctx, `SELECT id, scope, sync_key, cursor, from_value, to_value, request_context, status, started_at, COALESCE(finished_at, ''), records_seen, records_written, error_text FROM gongmcp_sync_runs WHERE status = 'success' ORDER BY finished_at DESC, id DESC LIMIT 1`)
	if err != nil {
		return nil, err
	}
	summary.LastSuccessfulRun = lastSuccess
	states, err := s.syncStates(ctx)
	if err != nil {
		return nil, err
	}
	summary.States = states
	profileReadiness, err := s.profileReadiness(ctx)
	if err != nil {
		return nil, err
	}
	summary.ProfileReadiness = profileReadiness
	if summary.MissingTranscripts == 0 && summary.TotalCalls > 0 {
		summary.PublicReadiness.TranscriptCoverage = sqlite.ReadinessFlag{Ready: true, Status: "ready", Detail: "all cached calls have transcripts"}
	}
	if !profileReadiness.Active {
		summary.PublicReadiness.LifecycleSeparation = sqlite.ReadinessFlag{Ready: false, Status: "needs_profile", Detail: profileReadiness.Detail, Requirements: profileReadiness.Blocking}
	} else if profileReadiness.CacheFresh {
		summary.PublicReadiness.LifecycleSeparation = sqlite.ReadinessFlag{Ready: true, Status: "ready", Detail: "profile lifecycle facts are available"}
	} else {
		summary.PublicReadiness.LifecycleSeparation = sqlite.ReadinessFlag{Ready: false, Status: "needs_action", Detail: profileReadiness.Detail, Requirements: profileReadiness.Blocking}
	}
	return summary, nil
}

func (s *Store) CacheInventory(ctx context.Context) (*sqlite.CacheInventory, error) {
	summary, err := s.SyncStatusSummary(ctx)
	if err != nil {
		return nil, err
	}
	out := &sqlite.CacheInventory{
		Summary:     summary,
		TableCounts: []sqlite.CacheTableCount{},
	}
	fallbackCounts := postgresInventoryFallbackCounts(summary)
	if readModelStatus, err := s.ReadModelStatus(ctx); err == nil {
		fallbackCounts["call_facts"] = readModelStatus.FactCount
		fallbackCounts["postgres_read_model_state"] = 1
		fallbackCounts["call_read_model_diagnostics"] = readModelStatus.DiagnosticsCallCount
	}
	if err := s.db.QueryRowContext(ctx, `
SELECT COALESCE(MIN(started_at), ''), COALESCE(MAX(started_at), '')
  FROM calls
 WHERE TRIM(started_at) <> ''`).Scan(&out.OldestCallStartedAt, &out.NewestCallStartedAt); err != nil {
		return nil, err
	}
	for _, tableName := range postgresInventoryTables() {
		var exists bool
		if err := s.db.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, "public."+tableName).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			continue
		}
		if s.readOnly {
			if fallback, ok := fallbackCounts[tableName]; ok {
				out.TableCounts = append(out.TableCounts, sqlite.CacheTableCount{Table: tableName, Rows: fallback})
				continue
			}
		}
		var count int64
		query := `SELECT COUNT(*) FROM public.` + quotePostgresIdent(tableName)
		if err := s.db.QueryRowContext(ctx, query).Scan(&count); err != nil {
			if fallback, ok := fallbackCounts[tableName]; ok {
				out.TableCounts = append(out.TableCounts, sqlite.CacheTableCount{Table: tableName, Rows: fallback})
				continue
			}
			return nil, fmt.Errorf("count postgres inventory table %s: %w", tableName, err)
		}
		out.TableCounts = append(out.TableCounts, sqlite.CacheTableCount{Table: tableName, Rows: count})
	}
	return out, nil
}

type CacheDiagnostics struct {
	Backend                string `json:"backend"`
	SchemaVersion          int    `json:"schema_version"`
	SupportedSchemaVersion int    `json:"supported_schema_version"`
	ReadModelReady         bool   `json:"read_model_ready"`
	ReadModelStatus        string `json:"read_model_status"`
	ReadModelStaleReason   string `json:"read_model_stale_reason,omitempty"`
	ProfileCacheStatus     string `json:"profile_cache_status"`
	ReaderPrivilegeStatus  string `json:"reader_privilege_status"`
}

func (s *Store) CacheDiagnostics(ctx context.Context) (*CacheDiagnostics, error) {
	diagnostics := &CacheDiagnostics{
		Backend:                "postgres",
		SupportedSchemaVersion: len(migrations),
		ReaderPrivilegeStatus:  "not_checked",
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM gongctl_schema_migrations`).Scan(&diagnostics.SchemaVersion); err != nil {
		return nil, err
	}
	if status, err := s.ReadModelStatus(ctx); err == nil {
		diagnostics.ReadModelReady = status.Ready
		if status.Ready {
			diagnostics.ReadModelStatus = "current"
		} else {
			diagnostics.ReadModelStatus = "stale"
		}
		diagnostics.ReadModelStaleReason = status.StaleReason
	} else {
		diagnostics.ReadModelStatus = "unavailable"
	}
	if readiness, err := s.profileReadiness(ctx); err == nil {
		diagnostics.ProfileCacheStatus = readiness.CacheStatus
	} else {
		diagnostics.ProfileCacheStatus = "unavailable"
	}
	if err := s.validateReadOnlyPrivileges(ctx); err == nil {
		diagnostics.ReaderPrivilegeStatus = "valid_reader"
	} else {
		diagnostics.ReaderPrivilegeStatus = "not_valid_reader"
	}
	return diagnostics, nil
}

func postgresInventoryTables() []string {
	return []string{
		"calls",
		"users",
		"transcripts",
		"transcript_segments",
		"call_context_objects",
		"call_context_fields",
		"call_facts",
		"postgres_read_model_state",
		"call_read_model_diagnostics",
		"gong_settings",
		"scorecard_activity",
		"crm_integrations",
		"crm_schema_objects",
		"crm_schema_fields",
	}
}

func postgresInventoryFallbackCounts(summary *sqlite.SyncStatusSummary) map[string]int64 {
	if summary == nil {
		return map[string]int64{}
	}
	return map[string]int64{
		"calls":                summary.TotalCalls,
		"users":                summary.TotalUsers,
		"transcripts":          summary.TotalTranscripts,
		"transcript_segments":  summary.TotalTranscriptSegments,
		"call_context_objects": summary.TotalEmbeddedCRMObjects,
		"call_context_fields":  summary.TotalEmbeddedCRMFields,
		"crm_integrations":     summary.TotalCRMIntegrations,
		"crm_schema_objects":   summary.TotalCRMSchemaObjects,
		"crm_schema_fields":    summary.TotalCRMSchemaFields,
		"gong_settings":        summary.TotalGongSettings,
		"scorecard_activity":   summary.TotalScorecardActivity,
	}
}

func quotePostgresIdent(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
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
	rows, err := s.db.QueryContext(ctx, `SELECT sync_key, scope, cursor, COALESCE(last_run_id, 0), last_status, last_error, COALESCE(last_success_at, ''), updated_at FROM gongmcp_sync_state ORDER BY scope, sync_key`)
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
