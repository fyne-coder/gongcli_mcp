package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/fyne-coder/gongcli_mcp/internal/governance"
)

// RefreshServingDBOptions describes a Phase 13e4 redacted MCP serving database
// refresh. Source is the operator cache; target is the physically redacted MCP
// serving cache. Config carries the private governance YAML used to determine
// which call IDs must not appear on the target.
type RefreshServingDBOptions struct {
	SourceURL string
	TargetURL string
	Config    *governance.Config
}

// ServingDBRefreshResult is the sanitized output of a serving-DB refresh.
//
// It intentionally does NOT include database URLs, customer names/aliases,
// blocklist values, call IDs, or call titles so the structure can be logged
// and shared with reviewers without leaking governance content or operator
// secrets.
type ServingDBRefreshResult struct {
	Backend                  string   `json:"backend"`
	SourceCalls              int64    `json:"source_calls"`
	SourceUsers              int64    `json:"source_users"`
	SourceTranscripts        int64    `json:"source_transcripts"`
	SourceTranscriptSegments int64    `json:"source_transcript_segments"`
	SourceContextObjects     int64    `json:"source_context_objects"`
	SourceContextFields      int64    `json:"source_context_fields"`
	TargetCalls              int64    `json:"target_calls"`
	TargetUsers              int64    `json:"target_users"`
	TargetTranscripts        int64    `json:"target_transcripts"`
	TargetTranscriptSegments int64    `json:"target_transcript_segments"`
	TargetContextObjects     int64    `json:"target_context_objects"`
	TargetContextFields      int64    `json:"target_context_fields"`
	RemovedCalls             int64    `json:"removed_calls"`
	SuppressedCallCount      int      `json:"suppressed_call_count"`
	PolicyConfigSHA256       string   `json:"policy_config_sha256"`
	SourceDataFingerprint    string   `json:"source_data_fingerprint"`
	TargetDataFingerprint    string   `json:"target_data_fingerprint"`
	TargetSuppressedRows     int64    `json:"target_suppressed_rows"`
	SkippedTables            []string `json:"skipped_tables,omitempty"`
}

// servingSkippedTables enumerates tables that the first vertical slice does
// not copy. They are listed in sanitized output so reviewers can see exactly
// what is intentionally absent from the redacted serving database.
//
// Skipped tables either contain operator-only state (sync_runs, sync_state,
// purged_call_ids), data the MCP smoke does not yet need (profile_*, scorecard
// activity, CRM schema metadata, gong_settings), or governance ingest
// bookkeeping (governance_ingest_skipped_calls).
func servingSkippedTables() []string {
	return []string{
		"sync_runs",
		"sync_state",
		"gong_settings",
		"crm_integrations",
		"crm_schema_objects",
		"crm_schema_fields",
		"scorecard_activity",
		"profile_meta",
		"profile_object_alias",
		"profile_field_concept",
		"profile_lifecycle_rule",
		"profile_methodology_concept",
		"profile_validation_warning",
		"profile_call_fact_cache",
		"profile_call_fact_cache_meta",
		"governance_ingest_skipped_calls",
		"purged_call_ids",
	}
}

// RefreshServingDB rebuilds the redacted MCP serving database (target) from
// the operator cache (source) using the supplied governance config.
//
// Behavior summary:
//   - Validates that source/target URLs are present and refer to different
//     databases (different scheme/host/port/path tuple).
//   - Determines the suppressed call ID set by running the existing governance
//     audit logic against the source.
//   - Truncates the call-scoped tables on the target that this slice rebuilds
//     and copies allowed rows (call_id NOT IN suppressed) from source to
//     target with raw payloads preserved.
//   - Re-runs the Postgres read model rebuild and re-applies the governance
//     policy on the target so MCP can serve sanitized outputs immediately.
//
// The first slice intentionally does not copy several global metadata tables;
// see servingSkippedTables for the full list. Skipped tables are surfaced in
// the sanitized output so reviewers can confirm the boundary.
func RefreshServingDB(ctx context.Context, opts RefreshServingDBOptions) (*ServingDBRefreshResult, error) {
	if err := validateRefreshServingDBURLs(opts.SourceURL, opts.TargetURL); err != nil {
		return nil, err
	}
	if opts.Config == nil {
		return nil, errors.New("governance config is required")
	}

	source, err := Open(ctx, opts.SourceURL)
	if err != nil {
		return nil, fmt.Errorf("open source database: %w", err)
	}
	defer source.Close()

	audit, err := governance.BuildAudit(ctx, source, opts.Config)
	if err != nil {
		return nil, fmt.Errorf("build governance audit on source: %w", err)
	}
	suppressed := make(map[string]struct{}, len(audit.SuppressedCallIDs))
	for _, callID := range audit.SuppressedCallIDs {
		suppressed[strings.TrimSpace(callID)] = struct{}{}
	}

	sourceFingerprint, err := source.GovernanceDataFingerprint(ctx)
	if err != nil {
		return nil, fmt.Errorf("read source data fingerprint: %w", err)
	}

	target, err := Open(ctx, opts.TargetURL)
	if err != nil {
		return nil, fmt.Errorf("open target database: %w", err)
	}
	defer target.Close()

	counts, err := refreshServingDBCopy(ctx, source.db, target.db, suppressed)
	if err != nil {
		return nil, fmt.Errorf("copy filtered serving data: %w", err)
	}

	if _, err := target.RebuildReadModel(ctx); err != nil {
		return nil, fmt.Errorf("rebuild read model on target: %w", err)
	}
	if err := readServingTargetCounts(ctx, target.db, &counts); err != nil {
		return nil, fmt.Errorf("read target serving counts: %w", err)
	}

	configSHA := opts.Config.Fingerprint()
	if _, _, err := target.BuildAndSaveGovernancePolicy(ctx, configSHA, opts.Config); err != nil {
		return nil, fmt.Errorf("apply governance policy on target: %w", err)
	}

	targetPolicy, err := target.LoadGovernancePolicy(ctx, configSHA)
	if err != nil {
		return nil, fmt.Errorf("load governance policy on target: %w", err)
	}
	targetFingerprint, err := target.GovernanceDataFingerprint(ctx)
	if err != nil {
		return nil, fmt.Errorf("read target data fingerprint: %w", err)
	}

	return &ServingDBRefreshResult{
		Backend:                  "postgres",
		SourceCalls:              counts.sourceCalls,
		SourceUsers:              counts.sourceUsers,
		SourceTranscripts:        counts.sourceTranscripts,
		SourceTranscriptSegments: counts.sourceTranscriptSegments,
		SourceContextObjects:     counts.sourceContextObjects,
		SourceContextFields:      counts.sourceContextFields,
		TargetCalls:              counts.targetCalls,
		TargetUsers:              counts.targetUsers,
		TargetTranscripts:        counts.targetTranscripts,
		TargetTranscriptSegments: counts.targetTranscriptSegments,
		TargetContextObjects:     counts.targetContextObjects,
		TargetContextFields:      counts.targetContextFields,
		RemovedCalls:             counts.sourceCalls - counts.targetCalls,
		SuppressedCallCount:      audit.SuppressedCallCount,
		PolicyConfigSHA256:       configSHA,
		SourceDataFingerprint:    sourceFingerprint,
		TargetDataFingerprint:    targetFingerprint,
		TargetSuppressedRows:     int64(targetPolicy.SuppressedCallCount),
		SkippedTables:            servingSkippedTables(),
	}, nil
}

// validateRefreshServingDBURLs requires both URLs to be non-empty and to point
// at distinct databases. Two URLs with the same scheme/host/port/path are
// rejected even if they differ in user, password, or query parameters.
func validateRefreshServingDBURLs(sourceURL, targetURL string) error {
	sourceURL = strings.TrimSpace(sourceURL)
	targetURL = strings.TrimSpace(targetURL)
	if sourceURL == "" {
		return errors.New("--source database URL is required")
	}
	if targetURL == "" {
		return errors.New("--target database URL is required")
	}
	src, err := url.Parse(sourceURL)
	if err != nil {
		return fmt.Errorf("parse source database URL: %w", err)
	}
	tgt, err := url.Parse(targetURL)
	if err != nil {
		return fmt.Errorf("parse target database URL: %w", err)
	}
	if normalizeServingDBURL(src) == normalizeServingDBURL(tgt) {
		return errors.New("source and target database URLs must point to different databases")
	}
	return nil
}

func normalizeServingDBURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(u.Scheme)) + "://" +
		strings.ToLower(strings.TrimSpace(u.Host)) +
		strings.TrimRight(u.Path, "/")
}

type servingCopyCounts struct {
	sourceCalls              int64
	sourceUsers              int64
	sourceTranscripts        int64
	sourceTranscriptSegments int64
	sourceContextObjects     int64
	sourceContextFields      int64
	targetCalls              int64
	targetUsers              int64
	targetTranscripts        int64
	targetTranscriptSegments int64
	targetContextObjects     int64
	targetContextFields      int64
}

// refreshServingDBCopy performs the actual table-by-table copy from source to
// target inside a single target transaction with the writer lock held. Source
// rows whose call_id is in the suppressed set are skipped at insert time so
// the target physically lacks restricted call rows.
func refreshServingDBCopy(ctx context.Context, sourceDB, targetDB *sql.DB, suppressed map[string]struct{}) (servingCopyCounts, error) {
	var counts servingCopyCounts
	for _, count := range []struct {
		query string
		dest  *int64
	}{
		{`SELECT COUNT(*) FROM calls`, &counts.sourceCalls},
		{`SELECT COUNT(*) FROM users`, &counts.sourceUsers},
		{`SELECT COUNT(*) FROM transcripts`, &counts.sourceTranscripts},
		{`SELECT COUNT(*) FROM transcript_segments`, &counts.sourceTranscriptSegments},
		{`SELECT COUNT(*) FROM call_context_objects`, &counts.sourceContextObjects},
		{`SELECT COUNT(*) FROM call_context_fields`, &counts.sourceContextFields},
	} {
		if err := sourceDB.QueryRowContext(ctx, count.query).Scan(count.dest); err != nil {
			return counts, fmt.Errorf("count source: %w", err)
		}
	}

	tx, err := targetDB.BeginTx(ctx, nil)
	if err != nil {
		return counts, err
	}
	defer tx.Rollback()
	if err := lockPostgresWriterTx(ctx, tx); err != nil {
		return counts, err
	}

	if _, err := tx.ExecContext(ctx, `TRUNCATE TABLE
call_read_model_diagnostics,
call_facts,
call_context_fields,
call_context_objects,
transcript_segments,
transcripts,
calls,
users,
governance_policy_state,
governance_suppressed_calls,
governance_ingest_skipped_calls
RESTART IDENTITY CASCADE`); err != nil {
		return counts, fmt.Errorf("truncate target serving tables: %w", err)
	}

	if err := copyServingUsers(ctx, sourceDB, tx, &counts); err != nil {
		return counts, err
	}
	if err := copyServingCalls(ctx, sourceDB, tx, suppressed, &counts); err != nil {
		return counts, err
	}
	if err := copyServingTranscripts(ctx, sourceDB, tx, suppressed, &counts); err != nil {
		return counts, err
	}
	if err := copyServingTranscriptSegments(ctx, sourceDB, tx, suppressed, &counts); err != nil {
		return counts, err
	}
	if err := copyServingCallContextObjects(ctx, sourceDB, tx, suppressed, &counts); err != nil {
		return counts, err
	}
	if err := copyServingCallContextFields(ctx, sourceDB, tx, suppressed, &counts); err != nil {
		return counts, err
	}

	if err := tx.Commit(); err != nil {
		return counts, err
	}
	return counts, nil
}

func readServingTargetCounts(ctx context.Context, targetDB *sql.DB, counts *servingCopyCounts) error {
	for _, count := range []struct {
		query string
		dest  *int64
	}{
		{`SELECT COUNT(*) FROM calls`, &counts.targetCalls},
		{`SELECT COUNT(*) FROM users`, &counts.targetUsers},
		{`SELECT COUNT(*) FROM transcripts`, &counts.targetTranscripts},
		{`SELECT COUNT(*) FROM transcript_segments`, &counts.targetTranscriptSegments},
		{`SELECT COUNT(*) FROM call_context_objects`, &counts.targetContextObjects},
		{`SELECT COUNT(*) FROM call_context_fields`, &counts.targetContextFields},
	} {
		if err := targetDB.QueryRowContext(ctx, count.query).Scan(count.dest); err != nil {
			return fmt.Errorf("count target: %w", err)
		}
	}
	return nil
}

func copyServingUsers(ctx context.Context, sourceDB *sql.DB, tx *sql.Tx, counts *servingCopyCounts) error {
	rows, err := sourceDB.QueryContext(ctx, `
SELECT user_id, email, first_name, last_name, display_name, title, active,
       raw_json::text, raw_sha256, first_seen_at, updated_at
  FROM users`)
	if err != nil {
		return fmt.Errorf("read source users: %w", err)
	}
	defer rows.Close()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO users(
	user_id, email, first_name, last_name, display_name, title, active,
	raw_json, raw_sha256, first_seen_at, updated_at
) VALUES($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9, $10, $11)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	var written int64
	for rows.Next() {
		var (
			userID, email, firstName, lastName, displayName, title string
			active                                                 bool
			rawJSON, rawSHA, firstSeenAt, updatedAt                string
		)
		if err := rows.Scan(&userID, &email, &firstName, &lastName, &displayName, &title, &active, &rawJSON, &rawSHA, &firstSeenAt, &updatedAt); err != nil {
			return fmt.Errorf("scan source user: %w", err)
		}
		if _, err := stmt.ExecContext(ctx, userID, email, firstName, lastName, displayName, title, active, rawJSON, rawSHA, firstSeenAt, updatedAt); err != nil {
			return fmt.Errorf("insert target user: %w", err)
		}
		written++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	counts.targetUsers = written
	return nil
}

func copyServingCalls(ctx context.Context, sourceDB *sql.DB, tx *sql.Tx, suppressed map[string]struct{}, counts *servingCopyCounts) error {
	rows, err := sourceDB.QueryContext(ctx, `
SELECT call_id, title, started_at, duration_seconds, parties_count, context_present,
       raw_json::text, raw_sha256, first_seen_at, updated_at
  FROM calls`)
	if err != nil {
		return fmt.Errorf("read source calls: %w", err)
	}
	defer rows.Close()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO calls(
	call_id, title, started_at, duration_seconds, parties_count, context_present,
	raw_json, raw_sha256, first_seen_at, updated_at
) VALUES($1, $2, $3, $4, $5, $6, $7::jsonb, $8, $9, $10)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	var written int64
	for rows.Next() {
		var (
			callID, title, startedAt                string
			durationSeconds, partiesCount           int64
			contextPresent                          bool
			rawJSON, rawSHA, firstSeenAt, updatedAt string
		)
		if err := rows.Scan(&callID, &title, &startedAt, &durationSeconds, &partiesCount, &contextPresent, &rawJSON, &rawSHA, &firstSeenAt, &updatedAt); err != nil {
			return fmt.Errorf("scan source call: %w", err)
		}
		if _, blocked := suppressed[strings.TrimSpace(callID)]; blocked {
			continue
		}
		if _, err := stmt.ExecContext(ctx, callID, title, startedAt, durationSeconds, partiesCount, contextPresent, rawJSON, rawSHA, firstSeenAt, updatedAt); err != nil {
			return fmt.Errorf("insert target call: %w", err)
		}
		written++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	counts.targetCalls = written
	return nil
}

func copyServingTranscripts(ctx context.Context, sourceDB *sql.DB, tx *sql.Tx, suppressed map[string]struct{}, counts *servingCopyCounts) error {
	rows, err := sourceDB.QueryContext(ctx, `
SELECT call_id, raw_json::text, raw_sha256, segment_count, first_seen_at, updated_at
  FROM transcripts`)
	if err != nil {
		return fmt.Errorf("read source transcripts: %w", err)
	}
	defer rows.Close()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO transcripts(
	call_id, raw_json, raw_sha256, segment_count, first_seen_at, updated_at
) VALUES($1, $2::jsonb, $3, $4, $5, $6)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	var written int64
	for rows.Next() {
		var (
			callID, rawJSON, rawSHA, firstSeenAt, updatedAt string
			segmentCount                                    int
		)
		if err := rows.Scan(&callID, &rawJSON, &rawSHA, &segmentCount, &firstSeenAt, &updatedAt); err != nil {
			return fmt.Errorf("scan source transcript: %w", err)
		}
		if _, blocked := suppressed[strings.TrimSpace(callID)]; blocked {
			continue
		}
		if _, err := stmt.ExecContext(ctx, callID, rawJSON, rawSHA, segmentCount, firstSeenAt, updatedAt); err != nil {
			return fmt.Errorf("insert target transcript: %w", err)
		}
		written++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	counts.targetTranscripts = written
	return nil
}

func copyServingTranscriptSegments(ctx context.Context, sourceDB *sql.DB, tx *sql.Tx, suppressed map[string]struct{}, counts *servingCopyCounts) error {
	rows, err := sourceDB.QueryContext(ctx, `
SELECT call_id, segment_index, speaker_id, start_ms, end_ms, text, raw_json::text
  FROM transcript_segments
 ORDER BY call_id, segment_index`)
	if err != nil {
		return fmt.Errorf("read source transcript_segments: %w", err)
	}
	defer rows.Close()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO transcript_segments(
	call_id, segment_index, speaker_id, start_ms, end_ms, text, raw_json
) VALUES($1, $2, $3, $4, $5, $6, $7::jsonb)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	var written int64
	for rows.Next() {
		var (
			callID, speakerID, text, rawJSON string
			segmentIndex                     int
			startMS, endMS                   int64
		)
		if err := rows.Scan(&callID, &segmentIndex, &speakerID, &startMS, &endMS, &text, &rawJSON); err != nil {
			return fmt.Errorf("scan source transcript_segment: %w", err)
		}
		if _, blocked := suppressed[strings.TrimSpace(callID)]; blocked {
			continue
		}
		if _, err := stmt.ExecContext(ctx, callID, segmentIndex, speakerID, startMS, endMS, text, rawJSON); err != nil {
			return fmt.Errorf("insert target transcript_segment: %w", err)
		}
		written++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	counts.targetTranscriptSegments = written
	return nil
}

func copyServingCallContextObjects(ctx context.Context, sourceDB *sql.DB, tx *sql.Tx, suppressed map[string]struct{}, counts *servingCopyCounts) error {
	rows, err := sourceDB.QueryContext(ctx, `
SELECT call_id, object_key, object_type, object_id, object_name, raw_json::text
  FROM call_context_objects
 ORDER BY call_id, object_key`)
	if err != nil {
		return fmt.Errorf("read source call_context_objects: %w", err)
	}
	defer rows.Close()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO call_context_objects(
	call_id, object_key, object_type, object_id, object_name, raw_json
) VALUES($1, $2, $3, $4, $5, $6::jsonb)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	var written int64
	for rows.Next() {
		var callID, objectKey, objectType, objectID, objectName, rawJSON string
		if err := rows.Scan(&callID, &objectKey, &objectType, &objectID, &objectName, &rawJSON); err != nil {
			return fmt.Errorf("scan source call_context_object: %w", err)
		}
		if _, blocked := suppressed[strings.TrimSpace(callID)]; blocked {
			continue
		}
		if _, err := stmt.ExecContext(ctx, callID, objectKey, objectType, objectID, objectName, rawJSON); err != nil {
			return fmt.Errorf("insert target call_context_object: %w", err)
		}
		written++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	counts.targetContextObjects = written
	return nil
}

func copyServingCallContextFields(ctx context.Context, sourceDB *sql.DB, tx *sql.Tx, suppressed map[string]struct{}, counts *servingCopyCounts) error {
	rows, err := sourceDB.QueryContext(ctx, `
SELECT call_id, object_key, field_name, field_label, field_type, field_value_text, raw_json::text
  FROM call_context_fields
 ORDER BY call_id, object_key, field_name`)
	if err != nil {
		return fmt.Errorf("read source call_context_fields: %w", err)
	}
	defer rows.Close()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO call_context_fields(
	call_id, object_key, field_name, field_label, field_type, field_value_text, raw_json
) VALUES($1, $2, $3, $4, $5, $6, $7::jsonb)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	var written int64
	for rows.Next() {
		var callID, objectKey, fieldName, fieldLabel, fieldType, fieldValue, rawJSON string
		if err := rows.Scan(&callID, &objectKey, &fieldName, &fieldLabel, &fieldType, &fieldValue, &rawJSON); err != nil {
			return fmt.Errorf("scan source call_context_field: %w", err)
		}
		if _, blocked := suppressed[strings.TrimSpace(callID)]; blocked {
			continue
		}
		if _, err := stmt.ExecContext(ctx, callID, objectKey, fieldName, fieldLabel, fieldType, fieldValue, rawJSON); err != nil {
			return fmt.Errorf("insert target call_context_field: %w", err)
		}
		written++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	counts.targetContextFields = written
	return nil
}
