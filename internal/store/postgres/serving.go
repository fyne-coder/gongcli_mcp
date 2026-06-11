package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/fyne-coder/gongcli_mcp/internal/governance"
)

// RefreshServingDBOptions describes a Phase 13e4 redacted MCP serving database
// refresh. Source is the operator cache; target is the physically redacted MCP
// serving cache. Config carries the private governance YAML used to determine
// which call IDs must not appear on the target.
type RefreshServingDBOptions struct {
	SourceURL              string
	TargetURL              string
	Config                 *governance.Config
	NoGovernanceExclusions bool
	// StatementTimeout applies to every source database connection opened for
	// the refresh via libpq startup options on the source URL.
	StatementTimeout time.Duration
}

// ServingDBRefreshResult is the sanitized output of a serving-DB refresh.
//
// It intentionally does NOT include database URLs, customer names/aliases,
// blocklist values, call IDs, or call titles so the structure can be logged
// and shared with reviewers without leaking governance content or operator
// secrets.
type ServingDBRefreshResult struct {
	ServingRefreshID         int64    `json:"serving_refresh_id,omitempty"`
	RefreshedAt              string   `json:"refreshed_at,omitempty"`
	Backend                  string   `json:"backend"`
	SourceCalls              int64    `json:"source_calls"`
	SourceUsers              int64    `json:"source_users"`
	SourceTranscripts        int64    `json:"source_transcripts"`
	SourceTranscriptSegments int64    `json:"source_transcript_segments"`
	SourceContextObjects     int64    `json:"source_context_objects"`
	SourceContextFields      int64    `json:"source_context_fields"`
	SourceGongSettings       int64    `json:"source_gong_settings"`
	SourceScorecards         int64    `json:"source_scorecards"`
	SourceScorecardActivity  int64    `json:"source_scorecard_activity"`
	SourceAIHighlights       int64    `json:"source_ai_highlights"`
	TargetCalls              int64    `json:"target_calls"`
	TargetUsers              int64    `json:"target_users"`
	TargetTranscripts        int64    `json:"target_transcripts"`
	TargetTranscriptSegments int64    `json:"target_transcript_segments"`
	TargetContextObjects     int64    `json:"target_context_objects"`
	TargetContextFields      int64    `json:"target_context_fields"`
	TargetGongSettings       int64    `json:"target_gong_settings"`
	TargetScorecards         int64    `json:"target_scorecards"`
	TargetScorecardActivity  int64    `json:"target_scorecard_activity"`
	TargetAIHighlights       int64    `json:"target_ai_highlights"`
	RemovedCalls             int64    `json:"removed_calls"`
	RemovedScorecardActivity int64    `json:"removed_scorecard_activity"`
	RemovedAIHighlights      int64    `json:"removed_ai_highlights"`
	SuppressedCallCount      int      `json:"suppressed_call_count"`
	NoGovernanceExclusions   bool     `json:"no_governance_exclusions,omitempty"`
	PolicyConfigSHA256       string   `json:"policy_config_sha256"`
	SourceDataFingerprint    string   `json:"source_data_fingerprint"`
	TargetDataFingerprint    string   `json:"target_data_fingerprint"`
	TargetSuppressedRows     int64    `json:"target_suppressed_rows"`
	SkippedTables            []string `json:"skipped_tables,omitempty"`
}

// ServingDBRefreshMarker is the persisted, sanitized proof that a target
// Postgres database was rebuilt through governance refresh. It intentionally
// contains fingerprints and counts only: no URLs, customer names, call IDs,
// call titles, governance terms, or transcript text.
type ServingDBRefreshMarker struct {
	ID                    int64           `json:"id"`
	RefreshedAt           string          `json:"refreshed_at"`
	SourceDataFingerprint string          `json:"source_data_fingerprint"`
	TargetDataFingerprint string          `json:"target_data_fingerprint"`
	PolicyConfigSHA256    string          `json:"policy_config_sha256"`
	SourceCalls           int64           `json:"source_calls"`
	TargetCalls           int64           `json:"target_calls"`
	RemovedCalls          int64           `json:"removed_calls"`
	SuppressedCallCount   int64           `json:"suppressed_call_count"`
	RowCountsJSON         json.RawMessage `json:"row_counts_json"`
}

// servingSkippedTables enumerates tables that the redacted serving database does
// not copy. They are listed in sanitized output so reviewers can see exactly
// what is intentionally absent from the redacted serving database.
//
// Skipped tables either contain operator-only state (sync_runs, sync_state,
// purged_call_ids), data the redacted MCP serving DB does not yet need
// (profile_*, CRM schema metadata), or governance ingest bookkeeping
// (governance_ingest_skipped_calls).
func servingSkippedTables() []string {
	return []string{
		"sync_runs",
		"sync_state",
		"crm_integrations",
		"crm_schema_objects",
		"crm_schema_fields",
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
		"serving_refresh_log",
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
// The serving refresh intentionally does not copy several operator/global metadata
// tables; see servingSkippedTables for the full list. Skipped tables are
// surfaced in the sanitized output so reviewers can confirm the boundary.
func RefreshServingDB(ctx context.Context, opts RefreshServingDBOptions) (*ServingDBRefreshResult, error) {
	if err := validateRefreshServingDBURLs(opts.SourceURL, opts.TargetURL); err != nil {
		return nil, err
	}
	cfg := opts.Config
	if opts.NoGovernanceExclusions {
		if cfg != nil {
			return nil, errors.New("governance config cannot be combined with no-governance-exclusions")
		}
		cfg = governance.NoExclusionsConfig()
	}
	if cfg == nil {
		return nil, errors.New("governance config is required unless no-governance-exclusions is set")
	}

	sourceURL := opts.SourceURL
	if opts.StatementTimeout > 0 {
		var err error
		sourceURL, err = databaseURLWithStatementTimeout(opts.SourceURL, opts.StatementTimeout)
		if err != nil {
			return nil, err
		}
	}
	source, err := Open(ctx, sourceURL)
	if err != nil {
		return nil, wrapServingRefreshPhaseError(ServingRefreshSideSource, "database", ServingRefreshPhaseConnect, err)
	}
	defer source.Close()

	audit, err := governance.BuildAudit(ctx, source, cfg)
	if err != nil {
		return nil, wrapServingRefreshPhaseError(ServingRefreshSideSource, "governance_audit", ServingRefreshPhaseAudit, err)
	}
	suppressed := make(map[string]struct{}, len(audit.SuppressedCallIDs))
	for _, callID := range audit.SuppressedCallIDs {
		suppressed[strings.TrimSpace(callID)] = struct{}{}
	}

	sourceFingerprint, err := source.GovernanceDataFingerprint(ctx)
	if err != nil {
		return nil, wrapServingRefreshPhaseError(ServingRefreshSideSource, "data_fingerprint", ServingRefreshPhaseCount, err)
	}

	target, err := Open(ctx, opts.TargetURL)
	if err != nil {
		return nil, wrapServingRefreshPhaseError(ServingRefreshSideTarget, "database", ServingRefreshPhaseConnect, err)
	}
	defer target.Close()

	counts, err := refreshServingDBCopy(ctx, source.db, target.db, suppressed)
	if err != nil {
		return nil, fmt.Errorf("copy filtered serving data: %w", err)
	}

	if _, err := target.RebuildReadModel(ctx); err != nil {
		return nil, wrapServingRefreshPhaseError(ServingRefreshSideTarget, "read_model", ServingRefreshPhaseReadModel, err)
	}
	if err := readServingTargetCounts(ctx, target.db, &counts); err != nil {
		return nil, wrapServingRefreshPhaseError(ServingRefreshSideTarget, "serving_counts", ServingRefreshPhaseCount, err)
	}
	if err := validateServingCopyCounts(counts); err != nil {
		return nil, wrapServingRefreshPhaseError(ServingRefreshSideServing, "copy_counts", ServingRefreshPhaseValidation, err)
	}

	configSHA := cfg.Fingerprint()
	if opts.NoGovernanceExclusions {
		configSHA = governance.NoExclusionsConfigFingerprint()
	}
	if _, _, err := target.BuildAndSaveGovernancePolicy(ctx, configSHA, cfg); err != nil {
		return nil, wrapServingRefreshPhaseError(ServingRefreshSideTarget, "governance_policy", ServingRefreshPhaseGovernancePolicy, err)
	}

	targetPolicy, err := target.LoadGovernancePolicy(ctx, configSHA)
	if err != nil {
		return nil, wrapServingRefreshPhaseError(ServingRefreshSideTarget, "governance_policy", ServingRefreshPhaseGovernancePolicy, err)
	}
	targetFingerprint, err := target.GovernanceDataFingerprint(ctx)
	if err != nil {
		return nil, wrapServingRefreshPhaseError(ServingRefreshSideTarget, "data_fingerprint", ServingRefreshPhaseCount, err)
	}

	result := &ServingDBRefreshResult{
		Backend:                  "postgres",
		SourceCalls:              counts.sourceCalls,
		SourceUsers:              counts.sourceUsers,
		SourceTranscripts:        counts.sourceTranscripts,
		SourceTranscriptSegments: counts.sourceTranscriptSegments,
		SourceContextObjects:     counts.sourceContextObjects,
		SourceContextFields:      counts.sourceContextFields,
		SourceGongSettings:       counts.sourceGongSettings,
		SourceScorecards:         counts.sourceScorecards,
		SourceScorecardActivity:  counts.sourceScorecardActivity,
		SourceAIHighlights:       counts.sourceAIHighlights,
		TargetCalls:              counts.targetCalls,
		TargetUsers:              counts.targetUsers,
		TargetTranscripts:        counts.targetTranscripts,
		TargetTranscriptSegments: counts.targetTranscriptSegments,
		TargetContextObjects:     counts.targetContextObjects,
		TargetContextFields:      counts.targetContextFields,
		TargetGongSettings:       counts.targetGongSettings,
		TargetScorecards:         counts.targetScorecards,
		TargetScorecardActivity:  counts.targetScorecardActivity,
		TargetAIHighlights:       counts.targetAIHighlights,
		RemovedCalls:             counts.sourceCalls - counts.targetCalls,
		RemovedScorecardActivity: counts.redactedScorecardActivity,
		RemovedAIHighlights:      counts.redactedAIHighlights,
		SuppressedCallCount:      audit.SuppressedCallCount,
		NoGovernanceExclusions:   opts.NoGovernanceExclusions,
		PolicyConfigSHA256:       configSHA,
		SourceDataFingerprint:    sourceFingerprint,
		TargetDataFingerprint:    targetFingerprint,
		TargetSuppressedRows:     int64(targetPolicy.SuppressedCallCount),
		SkippedTables:            servingSkippedTables(),
	}
	marker, err := target.RecordServingRefreshMarker(ctx, result)
	if err != nil {
		return nil, wrapServingRefreshPhaseError(ServingRefreshSideTarget, "serving_refresh_log", ServingRefreshPhaseMarker, err)
	}
	result.ServingRefreshID = marker.ID
	result.RefreshedAt = marker.RefreshedAt
	return result, nil
}

// RecordServingRefreshMarker stores a sanitized proof record for a successful
// serving refresh. Call it only after copy, read-model rebuild, governance
// policy application, and validation have succeeded.
func (s *Store) RecordServingRefreshMarker(ctx context.Context, result *ServingDBRefreshResult) (*ServingDBRefreshMarker, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("postgres store is not open")
	}
	if s.readOnly {
		return nil, errors.New("postgres store is read-only")
	}
	if result == nil {
		return nil, errors.New("serving refresh result is required")
	}
	refreshedAt := nowUTC()
	rowCounts := map[string]int64{
		"source_calls":               result.SourceCalls,
		"source_users":               result.SourceUsers,
		"source_transcripts":         result.SourceTranscripts,
		"source_transcript_segments": result.SourceTranscriptSegments,
		"source_context_objects":     result.SourceContextObjects,
		"source_context_fields":      result.SourceContextFields,
		"source_gong_settings":       result.SourceGongSettings,
		"source_scorecards":          result.SourceScorecards,
		"source_scorecard_activity":  result.SourceScorecardActivity,
		"source_ai_highlights":       result.SourceAIHighlights,
		"target_calls":               result.TargetCalls,
		"target_users":               result.TargetUsers,
		"target_transcripts":         result.TargetTranscripts,
		"target_transcript_segments": result.TargetTranscriptSegments,
		"target_context_objects":     result.TargetContextObjects,
		"target_context_fields":      result.TargetContextFields,
		"target_gong_settings":       result.TargetGongSettings,
		"target_scorecards":          result.TargetScorecards,
		"target_scorecard_activity":  result.TargetScorecardActivity,
		"target_ai_highlights":       result.TargetAIHighlights,
		"removed_scorecard_activity": result.RemovedScorecardActivity,
		"removed_ai_highlights":      result.RemovedAIHighlights,
		"target_suppressed_rows":     result.TargetSuppressedRows,
		"suppressed_call_count":      int64(result.SuppressedCallCount),
	}
	rowCountsJSON, err := json.Marshal(rowCounts)
	if err != nil {
		return nil, err
	}
	var marker ServingDBRefreshMarker
	var rowCountsText []byte
	if err := s.db.QueryRowContext(ctx, `INSERT INTO serving_refresh_log(
	refreshed_at, source_data_fingerprint, target_data_fingerprint,
	policy_config_sha256, source_calls, target_calls, removed_calls,
	suppressed_call_count, row_counts_json
) VALUES($1, $2, $3, $4, $5, $6, $7, $8, $9::jsonb)
RETURNING id, refreshed_at, source_data_fingerprint, target_data_fingerprint,
	policy_config_sha256, source_calls, target_calls, removed_calls,
	suppressed_call_count, row_counts_json::text`,
		refreshedAt,
		result.SourceDataFingerprint,
		result.TargetDataFingerprint,
		result.PolicyConfigSHA256,
		result.SourceCalls,
		result.TargetCalls,
		result.RemovedCalls,
		result.SuppressedCallCount,
		string(rowCountsJSON),
	).Scan(
		&marker.ID,
		&marker.RefreshedAt,
		&marker.SourceDataFingerprint,
		&marker.TargetDataFingerprint,
		&marker.PolicyConfigSHA256,
		&marker.SourceCalls,
		&marker.TargetCalls,
		&marker.RemovedCalls,
		&marker.SuppressedCallCount,
		&rowCountsText,
	); err != nil {
		return nil, err
	}
	marker.RowCountsJSON = json.RawMessage(rowCountsText)
	return &marker, nil
}

// LatestServingRefreshMarker returns the most recent successful serving
// refresh marker for diagnostics. sql.ErrNoRows means the database has no
// durable proof that it was built by governance refresh.
func (s *Store) LatestServingRefreshMarker(ctx context.Context) (*ServingDBRefreshMarker, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("postgres store is not open")
	}
	var marker ServingDBRefreshMarker
	var rowCountsText []byte
	err := s.db.QueryRowContext(ctx, `SELECT id, refreshed_at, source_data_fingerprint,
	target_data_fingerprint, policy_config_sha256, source_calls, target_calls,
	removed_calls, suppressed_call_count, row_counts_json::text
FROM serving_refresh_log
ORDER BY refreshed_at DESC, id DESC
LIMIT 1`).Scan(
		&marker.ID,
		&marker.RefreshedAt,
		&marker.SourceDataFingerprint,
		&marker.TargetDataFingerprint,
		&marker.PolicyConfigSHA256,
		&marker.SourceCalls,
		&marker.TargetCalls,
		&marker.RemovedCalls,
		&marker.SuppressedCallCount,
		&rowCountsText,
	)
	if err != nil {
		return nil, err
	}
	marker.RowCountsJSON = json.RawMessage(rowCountsText)
	return &marker, nil
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
	sourceCalls               int64
	sourceUsers               int64
	sourceTranscripts         int64
	sourceTranscriptSegments  int64
	sourceContextObjects      int64
	sourceContextFields       int64
	sourceGongSettings        int64
	sourceScorecards          int64
	sourceScorecardActivity   int64
	sourceAIHighlights        int64
	redactedCalls             int64
	redactedTranscripts       int64
	redactedTranscriptSegs    int64
	redactedContextObjects    int64
	redactedContextFields     int64
	redactedScorecardActivity int64
	redactedAIHighlights      int64
	targetCalls               int64
	targetUsers               int64
	targetTranscripts         int64
	targetTranscriptSegments  int64
	targetContextObjects      int64
	targetContextFields       int64
	targetGongSettings        int64
	targetScorecards          int64
	targetScorecardActivity   int64
	targetAIHighlights        int64
}

// refreshServingDBCopy performs the actual table-by-table copy from source to
// target inside a single target transaction with the writer lock held. Source
// rows whose call_id is in the suppressed set are skipped at insert time so
// the target physically lacks restricted call rows.
func refreshServingDBCopy(ctx context.Context, sourceDB, targetDB *sql.DB, suppressed map[string]struct{}) (servingCopyCounts, error) {
	var counts servingCopyCounts
	for _, count := range []struct {
		object string
		query  string
		dest   *int64
	}{
		{"calls", `SELECT COUNT(*) FROM calls`, &counts.sourceCalls},
		{"users", `SELECT COUNT(*) FROM users`, &counts.sourceUsers},
		{"transcripts", `SELECT COUNT(*) FROM transcripts`, &counts.sourceTranscripts},
		{"transcript_segments", `SELECT COUNT(*) FROM transcript_segments`, &counts.sourceTranscriptSegments},
		{"call_context_objects", `SELECT COUNT(*) FROM call_context_objects`, &counts.sourceContextObjects},
		{"call_context_fields", `SELECT COUNT(*) FROM call_context_fields`, &counts.sourceContextFields},
		{"gong_settings", `SELECT COUNT(*) FROM gong_settings`, &counts.sourceGongSettings},
		{"scorecards", `SELECT COUNT(*) FROM gong_settings WHERE kind = 'scorecards'`, &counts.sourceScorecards},
		{"scorecard_activity", `SELECT COUNT(*) FROM scorecard_activity`, &counts.sourceScorecardActivity},
	} {
		if err := sourceDB.QueryRowContext(ctx, count.query).Scan(count.dest); err != nil {
			return counts, wrapServingRefreshPhaseError(ServingRefreshSideSource, count.object, ServingRefreshPhaseCount, err)
		}
	}
	var err error
	counts.sourceAIHighlights, err = countServingSourceAIHighlights(ctx, sourceDB, nil)
	if err != nil {
		return counts, wrapServingRefreshPhaseError(ServingRefreshSideSource, "call_ai_highlights", ServingRefreshPhaseCount, err)
	}
	if err := countServingRedactedRows(ctx, sourceDB, suppressed, &counts); err != nil {
		return counts, err
	}

	tx, err := targetDB.BeginTx(ctx, nil)
	if err != nil {
		return counts, wrapServingRefreshPhaseError(ServingRefreshSideTarget, "transaction", ServingRefreshPhaseTransaction, err)
	}
	defer tx.Rollback()
	if err := lockPostgresWriterTx(ctx, tx); err != nil {
		return counts, wrapServingRefreshPhaseError(ServingRefreshSideTarget, "writer_lock", ServingRefreshPhaseLock, err)
	}

	if _, err := tx.ExecContext(ctx, `TRUNCATE TABLE
call_read_model_diagnostics,
call_facts,
call_ai_highlights,
call_context_fields,
call_context_objects,
scorecard_activity,
gong_settings,
transcript_segments,
transcripts,
calls,
users,
governance_policy_state,
governance_suppressed_calls,
governance_ingest_skipped_calls
RESTART IDENTITY CASCADE`); err != nil {
		return counts, wrapServingRefreshPhaseError(ServingRefreshSideTarget, "serving_tables", ServingRefreshPhaseTruncate, err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM serving_refresh_log`); err != nil {
		return counts, wrapServingRefreshPhaseError(ServingRefreshSideTarget, "serving_refresh_log", ServingRefreshPhaseTruncate, err)
	}

	if err := copyServingUsers(ctx, sourceDB, tx, &counts); err != nil {
		return counts, err
	}
	if err := copyServingGongSettings(ctx, sourceDB, tx, &counts); err != nil {
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
	if err := copyServingScorecardActivity(ctx, sourceDB, tx, suppressed, &counts); err != nil {
		return counts, err
	}

	if err := tx.Commit(); err != nil {
		return counts, wrapServingRefreshPhaseError(ServingRefreshSideTarget, "transaction", ServingRefreshPhaseTransaction, err)
	}
	return counts, nil
}

func readServingTargetCounts(ctx context.Context, targetDB *sql.DB, counts *servingCopyCounts) error {
	for _, count := range []struct {
		object string
		query  string
		dest   *int64
	}{
		{"calls", `SELECT COUNT(*) FROM calls`, &counts.targetCalls},
		{"users", `SELECT COUNT(*) FROM users`, &counts.targetUsers},
		{"transcripts", `SELECT COUNT(*) FROM transcripts`, &counts.targetTranscripts},
		{"transcript_segments", `SELECT COUNT(*) FROM transcript_segments`, &counts.targetTranscriptSegments},
		{"call_context_objects", `SELECT COUNT(*) FROM call_context_objects`, &counts.targetContextObjects},
		{"call_context_fields", `SELECT COUNT(*) FROM call_context_fields`, &counts.targetContextFields},
		{"gong_settings", `SELECT COUNT(*) FROM gong_settings`, &counts.targetGongSettings},
		{"scorecards", `SELECT COUNT(*) FROM gong_settings WHERE kind = 'scorecards'`, &counts.targetScorecards},
		{"scorecard_activity", `SELECT COUNT(*) FROM scorecard_activity`, &counts.targetScorecardActivity},
		{"call_ai_highlights", `SELECT COUNT(*) FROM call_ai_highlights`, &counts.targetAIHighlights},
	} {
		if err := targetDB.QueryRowContext(ctx, count.query).Scan(count.dest); err != nil {
			return wrapServingRefreshPhaseError(ServingRefreshSideTarget, count.object, ServingRefreshPhaseCount, err)
		}
	}
	return nil
}

func countServingRedactedRows(ctx context.Context, sourceDB *sql.DB, suppressed map[string]struct{}, counts *servingCopyCounts) error {
	if len(suppressed) == 0 {
		return nil
	}
	for _, count := range []struct {
		table  string
		column string
		dest   *int64
	}{
		{"calls", "call_id", &counts.redactedCalls},
		{"transcripts", "call_id", &counts.redactedTranscripts},
		{"transcript_segments", "call_id", &counts.redactedTranscriptSegs},
		{"call_context_objects", "call_id", &counts.redactedContextObjects},
		{"call_context_fields", "call_id", &counts.redactedContextFields},
		{"scorecard_activity", "call_id", &counts.redactedScorecardActivity},
	} {
		value, err := countServingSuppressedTableRows(ctx, sourceDB, count.table, count.column, suppressed)
		if err != nil {
			return wrapServingRefreshPhaseError(ServingRefreshSideSource, count.table, ServingRefreshPhaseCount, err)
		}
		*count.dest = value
	}
	var err error
	counts.redactedAIHighlights, err = countServingSourceAIHighlights(ctx, sourceDB, suppressed)
	if err != nil {
		return wrapServingRefreshPhaseError(ServingRefreshSideSource, "call_ai_highlights", ServingRefreshPhaseCount, err)
	}
	return nil
}

func countServingSuppressedTableRows(ctx context.Context, sourceDB *sql.DB, table, column string, suppressed map[string]struct{}) (int64, error) {
	idsJSON, err := servingSuppressedIDsJSON(suppressed)
	if err != nil {
		return 0, err
	}
	query := fmt.Sprintf(`
WITH suppressed(call_id) AS (
	SELECT TRIM(value) FROM jsonb_array_elements_text($1::jsonb) AS item(value)
)
SELECT COUNT(*)
  FROM %s t
  JOIN suppressed s
    ON s.call_id = TRIM(t.%s)`, table, column)
	var count int64
	if err := sourceDB.QueryRowContext(ctx, query, string(idsJSON)).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func countServingSourceAIHighlights(ctx context.Context, sourceDB *sql.DB, suppressed map[string]struct{}) (int64, error) {
	args := []any{}
	filter := ""
	if len(suppressed) > 0 {
		idsJSON, err := servingSuppressedIDsJSON(suppressed)
		if err != nil {
			return 0, err
		}
		args = append(args, string(idsJSON))
		filter = `AND EXISTS (
	SELECT 1
	  FROM jsonb_array_elements_text($1::jsonb) AS suppressed(call_id)
	 WHERE TRIM(suppressed.call_id) = TRIM(c.call_id)
)`
	}
	query := `
WITH raw_items AS (
	SELECT c.call_id, highlight_item.value AS raw_item
	  FROM calls c
	  CROSS JOIN LATERAL jsonb_array_elements(
		CASE WHEN jsonb_typeof(c.raw_json#>'{content,highlights}') = 'array' THEN c.raw_json#>'{content,highlights}' ELSE '[]'::jsonb END
	  ) AS highlight_item(value)
	 WHERE 1 = 1 ` + filter + `
	UNION ALL
	SELECT c.call_id, c.raw_json#>'{content,brief}' AS raw_item
	  FROM calls c
	 WHERE jsonb_typeof(c.raw_json#>'{content,brief}') IN ('string', 'object') ` + filter + `
	UNION ALL
	SELECT c.call_id, key_point_item.value AS raw_item
	  FROM calls c
	  CROSS JOIN LATERAL jsonb_array_elements(
		CASE WHEN jsonb_typeof(c.raw_json#>'{content,keyPoints}') = 'array' THEN c.raw_json#>'{content,keyPoints}' ELSE '[]'::jsonb END
	  ) AS key_point_item(value)
	 WHERE 1 = 1 ` + filter + `
	UNION ALL
	SELECT c.call_id, outline_item.value AS raw_item
	  FROM calls c
	  CROSS JOIN LATERAL jsonb_array_elements(
		CASE WHEN jsonb_typeof(c.raw_json#>'{content,outline}') = 'array' THEN c.raw_json#>'{content,outline}' ELSE '[]'::jsonb END
	  ) AS outline_item(value)
	 WHERE 1 = 1 ` + filter + `
	UNION ALL
	SELECT c.call_id, c.raw_json#>'{content,outline}' AS raw_item
	  FROM calls c
	 WHERE jsonb_typeof(c.raw_json#>'{content,outline}') = 'string' ` + filter + `
),
typed_items AS (
	SELECT TRIM(CASE
		       WHEN jsonb_typeof(raw_item) = 'string' THEN raw_item#>>'{}'
		       WHEN jsonb_typeof(raw_item) = 'object' THEN COALESCE(raw_item->>'text', raw_item->>'summary', raw_item->>'description', raw_item->>'value', raw_item->>'title', '')
		       ELSE ''
	       END) AS highlight_text
	  FROM raw_items
)
SELECT COUNT(*) FROM typed_items WHERE highlight_text <> ''`
	var count int64
	if err := sourceDB.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func servingSuppressedIDsJSON(suppressed map[string]struct{}) ([]byte, error) {
	ids := make([]string, 0, len(suppressed))
	for id := range suppressed {
		id = strings.TrimSpace(id)
		if id != "" {
			ids = append(ids, id)
		}
	}
	return json.Marshal(ids)
}

func validateServingCopyCounts(counts servingCopyCounts) error {
	expectations := []struct {
		name string
		got  int64
		want int64
	}{
		{"calls", counts.targetCalls, counts.sourceCalls - counts.redactedCalls},
		{"users", counts.targetUsers, counts.sourceUsers},
		{"transcripts", counts.targetTranscripts, counts.sourceTranscripts - counts.redactedTranscripts},
		{"transcript_segments", counts.targetTranscriptSegments, counts.sourceTranscriptSegments - counts.redactedTranscriptSegs},
		{"call_context_objects", counts.targetContextObjects, counts.sourceContextObjects - counts.redactedContextObjects},
		{"call_context_fields", counts.targetContextFields, counts.sourceContextFields - counts.redactedContextFields},
		{"gong_settings", counts.targetGongSettings, counts.sourceGongSettings},
		{"scorecards", counts.targetScorecards, counts.sourceScorecards},
		{"scorecard_activity", counts.targetScorecardActivity, counts.sourceScorecardActivity - counts.redactedScorecardActivity},
		{"call_ai_highlights", counts.targetAIHighlights, counts.sourceAIHighlights - counts.redactedAIHighlights},
	}
	var mismatches []string
	for _, expectation := range expectations {
		if expectation.want < 0 {
			expectation.want = 0
		}
		if expectation.got != expectation.want {
			mismatches = append(mismatches, fmt.Sprintf("%s target=%d want=%d", expectation.name, expectation.got, expectation.want))
		}
	}
	if len(mismatches) > 0 {
		return fmt.Errorf("redacted serving database validation failed: %s", strings.Join(mismatches, "; "))
	}
	return nil
}

func copyServingGongSettings(ctx context.Context, sourceDB *sql.DB, tx *sql.Tx, counts *servingCopyCounts) error {
	rows, err := sourceDB.QueryContext(ctx, `
SELECT kind, object_id, name, active, raw_json::text, raw_sha256, first_seen_at, updated_at
  FROM gong_settings
 ORDER BY kind, object_id`)
	if err != nil {
		return wrapServingRefreshPhaseError(ServingRefreshSideSource, "gong_settings", ServingRefreshPhaseCopy, err)
	}
	defer rows.Close()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO gong_settings(
	kind, object_id, name, active, raw_json, raw_sha256, first_seen_at, updated_at
) VALUES($1, $2, $3, $4, $5::jsonb, $6, $7, $8)`)
	if err != nil {
		return wrapServingRefreshPhaseError(ServingRefreshSideTarget, "gong_settings", ServingRefreshPhaseCopy, err)
	}
	defer stmt.Close()
	var written int64
	for rows.Next() {
		var kind, objectID, name, rawJSON, rawSHA, firstSeenAt, updatedAt string
		var active bool
		if err := rows.Scan(&kind, &objectID, &name, &active, &rawJSON, &rawSHA, &firstSeenAt, &updatedAt); err != nil {
			return wrapServingRefreshPhaseError(ServingRefreshSideSource, "gong_settings", ServingRefreshPhaseCopy, err)
		}
		if _, err := stmt.ExecContext(ctx, kind, objectID, name, active, rawJSON, rawSHA, firstSeenAt, updatedAt); err != nil {
			return wrapServingRefreshPhaseError(ServingRefreshSideTarget, "gong_settings", ServingRefreshPhaseCopy, err)
		}
		written++
	}
	if err := rows.Err(); err != nil {
		return wrapServingRefreshPhaseError(ServingRefreshSideSource, "gong_settings", ServingRefreshPhaseCopy, err)
	}
	counts.targetGongSettings = written
	return nil
}

func copyServingUsers(ctx context.Context, sourceDB *sql.DB, tx *sql.Tx, counts *servingCopyCounts) error {
	rows, err := sourceDB.QueryContext(ctx, `
SELECT user_id, email, first_name, last_name, display_name, title, active,
       raw_json::text, raw_sha256, first_seen_at, updated_at
  FROM users`)
	if err != nil {
		return wrapServingRefreshPhaseError(ServingRefreshSideSource, "users", ServingRefreshPhaseCopy, err)
	}
	defer rows.Close()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO users(
	user_id, email, first_name, last_name, display_name, title, active,
	raw_json, raw_sha256, first_seen_at, updated_at
) VALUES($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9, $10, $11)`)
	if err != nil {
		return wrapServingRefreshPhaseError(ServingRefreshSideTarget, "users", ServingRefreshPhaseCopy, err)
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
			return wrapServingRefreshPhaseError(ServingRefreshSideSource, "users", ServingRefreshPhaseCopy, err)
		}
		if _, err := stmt.ExecContext(ctx, userID, email, firstName, lastName, displayName, title, active, rawJSON, rawSHA, firstSeenAt, updatedAt); err != nil {
			return wrapServingRefreshPhaseError(ServingRefreshSideTarget, "users", ServingRefreshPhaseCopy, err)
		}
		written++
	}
	if err := rows.Err(); err != nil {
		return wrapServingRefreshPhaseError(ServingRefreshSideSource, "users", ServingRefreshPhaseCopy, err)
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
		return wrapServingRefreshPhaseError(ServingRefreshSideSource, "calls", ServingRefreshPhaseCopy, err)
	}
	defer rows.Close()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO calls(
	call_id, title, started_at, duration_seconds, parties_count, context_present,
	raw_json, raw_sha256, first_seen_at, updated_at
) VALUES($1, $2, $3, $4, $5, $6, $7::jsonb, $8, $9, $10)`)
	if err != nil {
		return wrapServingRefreshPhaseError(ServingRefreshSideTarget, "calls", ServingRefreshPhaseCopy, err)
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
			return wrapServingRefreshPhaseError(ServingRefreshSideSource, "calls", ServingRefreshPhaseCopy, err)
		}
		if _, blocked := suppressed[strings.TrimSpace(callID)]; blocked {
			continue
		}
		if _, err := stmt.ExecContext(ctx, callID, title, startedAt, durationSeconds, partiesCount, contextPresent, rawJSON, rawSHA, firstSeenAt, updatedAt); err != nil {
			return wrapServingRefreshPhaseError(ServingRefreshSideTarget, "calls", ServingRefreshPhaseCopy, err)
		}
		written++
	}
	if err := rows.Err(); err != nil {
		return wrapServingRefreshPhaseError(ServingRefreshSideSource, "calls", ServingRefreshPhaseCopy, err)
	}
	counts.targetCalls = written
	return nil
}

func copyServingTranscripts(ctx context.Context, sourceDB *sql.DB, tx *sql.Tx, suppressed map[string]struct{}, counts *servingCopyCounts) error {
	rows, err := sourceDB.QueryContext(ctx, `
SELECT call_id, raw_json::text, raw_sha256, segment_count, first_seen_at, updated_at
  FROM transcripts`)
	if err != nil {
		return wrapServingRefreshPhaseError(ServingRefreshSideSource, "transcripts", ServingRefreshPhaseCopy, err)
	}
	defer rows.Close()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO transcripts(
	call_id, raw_json, raw_sha256, segment_count, first_seen_at, updated_at
) VALUES($1, $2::jsonb, $3, $4, $5, $6)`)
	if err != nil {
		return wrapServingRefreshPhaseError(ServingRefreshSideTarget, "transcripts", ServingRefreshPhaseCopy, err)
	}
	defer stmt.Close()
	var written int64
	for rows.Next() {
		var (
			callID, rawJSON, rawSHA, firstSeenAt, updatedAt string
			segmentCount                                    int
		)
		if err := rows.Scan(&callID, &rawJSON, &rawSHA, &segmentCount, &firstSeenAt, &updatedAt); err != nil {
			return wrapServingRefreshPhaseError(ServingRefreshSideSource, "transcripts", ServingRefreshPhaseCopy, err)
		}
		if _, blocked := suppressed[strings.TrimSpace(callID)]; blocked {
			continue
		}
		if _, err := stmt.ExecContext(ctx, callID, rawJSON, rawSHA, segmentCount, firstSeenAt, updatedAt); err != nil {
			return wrapServingRefreshPhaseError(ServingRefreshSideTarget, "transcripts", ServingRefreshPhaseCopy, err)
		}
		written++
	}
	if err := rows.Err(); err != nil {
		return wrapServingRefreshPhaseError(ServingRefreshSideSource, "transcripts", ServingRefreshPhaseCopy, err)
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
		return wrapServingRefreshPhaseError(ServingRefreshSideSource, "transcript_segments", ServingRefreshPhaseCopy, err)
	}
	defer rows.Close()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO transcript_segments(
	call_id, segment_index, speaker_id, start_ms, end_ms, text, raw_json
) VALUES($1, $2, $3, $4, $5, $6, $7::jsonb)`)
	if err != nil {
		return wrapServingRefreshPhaseError(ServingRefreshSideTarget, "transcript_segments", ServingRefreshPhaseCopy, err)
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
			return wrapServingRefreshPhaseError(ServingRefreshSideSource, "transcript_segments", ServingRefreshPhaseCopy, err)
		}
		if _, blocked := suppressed[strings.TrimSpace(callID)]; blocked {
			continue
		}
		if _, err := stmt.ExecContext(ctx, callID, segmentIndex, speakerID, startMS, endMS, text, rawJSON); err != nil {
			return wrapServingRefreshPhaseError(ServingRefreshSideTarget, "transcript_segments", ServingRefreshPhaseCopy, err)
		}
		written++
	}
	if err := rows.Err(); err != nil {
		return wrapServingRefreshPhaseError(ServingRefreshSideSource, "transcript_segments", ServingRefreshPhaseCopy, err)
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
		return wrapServingRefreshPhaseError(ServingRefreshSideSource, "call_context_objects", ServingRefreshPhaseCopy, err)
	}
	defer rows.Close()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO call_context_objects(
	call_id, object_key, object_type, object_id, object_name, raw_json
) VALUES($1, $2, $3, $4, $5, $6::jsonb)`)
	if err != nil {
		return wrapServingRefreshPhaseError(ServingRefreshSideTarget, "call_context_objects", ServingRefreshPhaseCopy, err)
	}
	defer stmt.Close()
	var written int64
	for rows.Next() {
		var callID, objectKey, objectType, objectID, objectName, rawJSON string
		if err := rows.Scan(&callID, &objectKey, &objectType, &objectID, &objectName, &rawJSON); err != nil {
			return wrapServingRefreshPhaseError(ServingRefreshSideSource, "call_context_objects", ServingRefreshPhaseCopy, err)
		}
		if _, blocked := suppressed[strings.TrimSpace(callID)]; blocked {
			continue
		}
		if _, err := stmt.ExecContext(ctx, callID, objectKey, objectType, objectID, objectName, rawJSON); err != nil {
			return wrapServingRefreshPhaseError(ServingRefreshSideTarget, "call_context_objects", ServingRefreshPhaseCopy, err)
		}
		written++
	}
	if err := rows.Err(); err != nil {
		return wrapServingRefreshPhaseError(ServingRefreshSideSource, "call_context_objects", ServingRefreshPhaseCopy, err)
	}
	counts.targetContextObjects = written
	return nil
}

func copyServingScorecardActivity(ctx context.Context, sourceDB *sql.DB, tx *sql.Tx, suppressed map[string]struct{}, counts *servingCopyCounts) error {
	rows, err := sourceDB.QueryContext(ctx, `
SELECT answered_scorecard_id, scorecard_id, scorecard_name, call_id, call_started_at,
       reviewed_user_id, reviewer_user_id, editor_user_id, review_method, review_time,
       visibility_type, overall_score, average_score, answer_count,
       raw_json::text, raw_sha256, first_seen_at, updated_at
  FROM scorecard_activity
 ORDER BY answered_scorecard_id`)
	if err != nil {
		return wrapServingRefreshPhaseError(ServingRefreshSideSource, "scorecard_activity", ServingRefreshPhaseCopy, err)
	}
	defer rows.Close()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO scorecard_activity(
	answered_scorecard_id, scorecard_id, scorecard_name, call_id, call_started_at,
	reviewed_user_id, reviewer_user_id, editor_user_id, review_method, review_time,
	visibility_type, overall_score, average_score, answer_count,
	raw_json, raw_sha256, first_seen_at, updated_at
) VALUES($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15::jsonb, $16, $17, $18)`)
	if err != nil {
		return wrapServingRefreshPhaseError(ServingRefreshSideTarget, "scorecard_activity", ServingRefreshPhaseCopy, err)
	}
	defer stmt.Close()
	var written int64
	for rows.Next() {
		var (
			answeredScorecardID, scorecardID, scorecardName, callID, callStartedAt string
			reviewedUserID, reviewerUserID, editorUserID, reviewMethod             string
			reviewTime, visibilityType, rawJSON, rawSHA, firstSeenAt, updatedAt    string
			overallScore, averageScore                                             float64
			answerCount                                                            int64
		)
		if err := rows.Scan(
			&answeredScorecardID, &scorecardID, &scorecardName, &callID, &callStartedAt,
			&reviewedUserID, &reviewerUserID, &editorUserID, &reviewMethod, &reviewTime,
			&visibilityType, &overallScore, &averageScore, &answerCount,
			&rawJSON, &rawSHA, &firstSeenAt, &updatedAt,
		); err != nil {
			return wrapServingRefreshPhaseError(ServingRefreshSideSource, "scorecard_activity", ServingRefreshPhaseCopy, err)
		}
		if _, blocked := suppressed[strings.TrimSpace(callID)]; blocked {
			continue
		}
		if _, err := stmt.ExecContext(
			ctx,
			answeredScorecardID, scorecardID, scorecardName, callID, callStartedAt,
			reviewedUserID, reviewerUserID, editorUserID, reviewMethod, reviewTime,
			visibilityType, overallScore, averageScore, answerCount,
			rawJSON, rawSHA, firstSeenAt, updatedAt,
		); err != nil {
			return wrapServingRefreshPhaseError(ServingRefreshSideTarget, "scorecard_activity", ServingRefreshPhaseCopy, err)
		}
		written++
	}
	if err := rows.Err(); err != nil {
		return wrapServingRefreshPhaseError(ServingRefreshSideSource, "scorecard_activity", ServingRefreshPhaseCopy, err)
	}
	counts.targetScorecardActivity = written
	return nil
}

func copyServingCallContextFields(ctx context.Context, sourceDB *sql.DB, tx *sql.Tx, suppressed map[string]struct{}, counts *servingCopyCounts) error {
	rows, err := sourceDB.QueryContext(ctx, `
SELECT call_id, object_key, field_name, field_label, field_type, field_value_text, raw_json::text
  FROM call_context_fields
 ORDER BY call_id, object_key, field_name`)
	if err != nil {
		return wrapServingRefreshPhaseError(ServingRefreshSideSource, "call_context_fields", ServingRefreshPhaseCopy, err)
	}
	defer rows.Close()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO call_context_fields(
	call_id, object_key, field_name, field_label, field_type, field_value_text, raw_json
) VALUES($1, $2, $3, $4, $5, $6, $7::jsonb)`)
	if err != nil {
		return wrapServingRefreshPhaseError(ServingRefreshSideTarget, "call_context_fields", ServingRefreshPhaseCopy, err)
	}
	defer stmt.Close()
	var written int64
	for rows.Next() {
		var callID, objectKey, fieldName, fieldLabel, fieldType, fieldValue, rawJSON string
		if err := rows.Scan(&callID, &objectKey, &fieldName, &fieldLabel, &fieldType, &fieldValue, &rawJSON); err != nil {
			return wrapServingRefreshPhaseError(ServingRefreshSideSource, "call_context_fields", ServingRefreshPhaseCopy, err)
		}
		if _, blocked := suppressed[strings.TrimSpace(callID)]; blocked {
			continue
		}
		if _, err := stmt.ExecContext(ctx, callID, objectKey, fieldName, fieldLabel, fieldType, fieldValue, rawJSON); err != nil {
			return wrapServingRefreshPhaseError(ServingRefreshSideTarget, "call_context_fields", ServingRefreshPhaseCopy, err)
		}
		written++
	}
	if err := rows.Err(); err != nil {
		return wrapServingRefreshPhaseError(ServingRefreshSideSource, "call_context_fields", ServingRefreshPhaseCopy, err)
	}
	counts.targetContextFields = written
	return nil
}
