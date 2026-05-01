package sqlite

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fyne-coder/gongcli_mcp/internal/governance"
)

type GovernanceFilteredExportPlan struct {
	SourceDBPath                  string `json:"source_db_path"`
	OutputDBPath                  string `json:"output_db_path"`
	SuppressedCallCount           int    `json:"suppressed_call_count"`
	DeletedCalls                  int64  `json:"deleted_calls"`
	DeletedTranscripts            int64  `json:"deleted_transcripts"`
	DeletedTranscriptSegments     int64  `json:"deleted_transcript_segments"`
	DeletedContextObjects         int64  `json:"deleted_context_objects"`
	DeletedContextFields          int64  `json:"deleted_context_fields"`
	DeletedProfileCallFactRows    int64  `json:"deleted_profile_call_fact_rows"`
	RemainingSuppressedCandidates int64  `json:"remaining_suppressed_candidates"`
}

func (s *Store) GovernanceNameCandidates(ctx context.Context) ([]governance.Candidate, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT call_id, 'call_title' AS source, title AS value
  FROM calls
 WHERE TRIM(title) <> ''
UNION ALL
SELECT call_id, 'call_raw_json' AS source, CAST(raw_json AS TEXT) AS value
  FROM calls
 WHERE TRIM(CAST(raw_json AS TEXT)) <> ''
UNION ALL
SELECT call_id, 'crm_object_name' AS source, object_name AS value
  FROM call_context_objects
 WHERE TRIM(object_name) <> ''
UNION ALL
SELECT f.call_id, 'crm_field:' || f.field_name AS source, f.field_value_text AS value
  FROM call_context_fields f
 WHERE TRIM(f.field_value_text) <> ''
UNION ALL
SELECT call_id, 'transcript_segment' AS source, text AS value
  FROM transcript_segments
 WHERE TRIM(text) <> ''
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]governance.Candidate, 0)
	for rows.Next() {
		var candidate governance.Candidate
		if err := rows.Scan(&candidate.CallID, &candidate.Source, &candidate.Value); err != nil {
			return nil, err
		}
		out = append(out, candidate)
	}
	return out, rows.Err()
}

func (s *Store) GovernanceDataFingerprint(ctx context.Context) (string, error) {
	candidates, err := s.GovernanceNameCandidates(ctx)
	if err != nil {
		return "", err
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].CallID != candidates[j].CallID {
			return candidates[i].CallID < candidates[j].CallID
		}
		if candidates[i].Source != candidates[j].Source {
			return candidates[i].Source < candidates[j].Source
		}
		return candidates[i].Value < candidates[j].Value
	})
	hash := sha256.New()
	for _, candidate := range candidates {
		_, _ = hash.Write([]byte(candidate.CallID))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(candidate.Source))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(strings.TrimSpace(candidate.Value)))
		_, _ = hash.Write([]byte{0})
	}
	return fmt.Sprintf("candidates:%d:%x", len(candidates), hash.Sum(nil)), nil
}

func ExportGovernanceFilteredDB(ctx context.Context, sourcePath, outputPath string, suppressedCallIDs []string, overwrite bool) (*GovernanceFilteredExportPlan, error) {
	sourcePath = strings.TrimSpace(sourcePath)
	outputPath = strings.TrimSpace(outputPath)
	if sourcePath == "" {
		return nil, errors.New("source db path is required")
	}
	if outputPath == "" {
		return nil, errors.New("output db path is required")
	}
	sourceAbs, err := filepath.Abs(sourcePath)
	if err != nil {
		return nil, err
	}
	outputAbs, err := filepath.Abs(outputPath)
	if err != nil {
		return nil, err
	}
	if sourceAbs == outputAbs {
		return nil, errors.New("output db path must be different from source db path")
	}
	if _, err := os.Stat(sourceAbs); err != nil {
		return nil, err
	}
	if _, err := os.Stat(outputAbs); err == nil && !overwrite {
		return nil, fmt.Errorf("output db already exists: %s", outputAbs)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := ensureParentDir(outputAbs); err != nil {
		return nil, err
	}
	if overwrite {
		if err := removeSQLiteFiles(outputAbs); err != nil {
			return nil, err
		}
	}

	source, err := Open(ctx, sourceAbs)
	if err != nil {
		return nil, err
	}
	if _, err := source.db.ExecContext(ctx, `VACUUM main INTO ?`, outputAbs); err != nil {
		_ = source.Close()
		return nil, fmt.Errorf("copy source db: %w", err)
	}
	if err := source.Close(); err != nil {
		return nil, err
	}

	out, err := Open(ctx, outputAbs)
	if err != nil {
		return nil, err
	}
	defer out.Close()

	plan, err := out.DeleteGovernanceSuppressedCalls(ctx, suppressedCallIDs)
	if err != nil {
		return nil, err
	}
	plan.SourceDBPath = sourceAbs
	plan.OutputDBPath = outputAbs
	return plan, nil
}

func (s *Store) DeleteGovernanceSuppressedCalls(ctx context.Context, callIDs []string) (*GovernanceFilteredExportPlan, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("sqlite store is not open")
	}
	normalized := uniqueNonEmpty(callIDs)
	plan := &GovernanceFilteredExportPlan{SuppressedCallCount: len(normalized)}
	if len(normalized) == 0 {
		if err := s.compactAfterPurge(ctx); err != nil {
			return nil, err
		}
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
	if _, err := tx.ExecContext(ctx, `CREATE TEMP TABLE governance_suppressed_call_ids(call_id TEXT PRIMARY KEY)`); err != nil {
		return nil, err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO governance_suppressed_call_ids(call_id) VALUES (?)`)
	if err != nil {
		return nil, err
	}
	for _, callID := range normalized {
		if _, err := stmt.ExecContext(ctx, callID); err != nil {
			_ = stmt.Close()
			return nil, err
		}
	}
	if err := stmt.Close(); err != nil {
		return nil, err
	}
	for _, item := range []struct {
		target *int64
		query  string
	}{
		{&plan.DeletedProfileCallFactRows, `DELETE FROM profile_call_fact_cache WHERE call_id IN (SELECT call_id FROM governance_suppressed_call_ids)`},
		{&plan.DeletedTranscriptSegments, `DELETE FROM transcript_segments WHERE call_id IN (SELECT call_id FROM governance_suppressed_call_ids)`},
		{&plan.DeletedTranscripts, `DELETE FROM transcripts WHERE call_id IN (SELECT call_id FROM governance_suppressed_call_ids)`},
		{&plan.DeletedContextFields, `DELETE FROM call_context_fields WHERE call_id IN (SELECT call_id FROM governance_suppressed_call_ids)`},
		{&plan.DeletedContextObjects, `DELETE FROM call_context_objects WHERE call_id IN (SELECT call_id FROM governance_suppressed_call_ids)`},
		{&plan.DeletedCalls, `DELETE FROM calls WHERE call_id IN (SELECT call_id FROM governance_suppressed_call_ids)`},
	} {
		result, err := tx.ExecContext(ctx, item.query)
		if err != nil {
			return nil, err
		}
		if rows, err := result.RowsAffected(); err == nil {
			*item.target = rows
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO transcript_segments_fts(transcript_segments_fts) VALUES ('rebuild')`); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if err := s.compactAfterPurge(ctx); err != nil {
		return nil, err
	}
	remaining, err := s.remainingCallIDCount(ctx, normalized)
	if err != nil {
		return nil, err
	}
	plan.RemainingSuppressedCandidates = remaining
	return plan, nil
}

func (s *Store) remainingCallIDCount(ctx context.Context, callIDs []string) (int64, error) {
	if len(callIDs) == 0 {
		return 0, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `CREATE TEMP TABLE governance_remaining_call_ids(call_id TEXT PRIMARY KEY)`); err != nil {
		return 0, err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO governance_remaining_call_ids(call_id) VALUES (?)`)
	if err != nil {
		return 0, err
	}
	for _, callID := range callIDs {
		if _, err := stmt.ExecContext(ctx, callID); err != nil {
			_ = stmt.Close()
			return 0, err
		}
	}
	if err := stmt.Close(); err != nil {
		return 0, err
	}
	var count int64
	err = tx.QueryRowContext(ctx, `
SELECT
	(SELECT COUNT(*) FROM calls WHERE call_id IN (SELECT call_id FROM governance_remaining_call_ids)) +
	(SELECT COUNT(*) FROM transcripts WHERE call_id IN (SELECT call_id FROM governance_remaining_call_ids)) +
	(SELECT COUNT(*) FROM transcript_segments WHERE call_id IN (SELECT call_id FROM governance_remaining_call_ids)) +
	(SELECT COUNT(*) FROM call_context_objects WHERE call_id IN (SELECT call_id FROM governance_remaining_call_ids)) +
	(SELECT COUNT(*) FROM call_context_fields WHERE call_id IN (SELECT call_id FROM governance_remaining_call_ids)) +
	(SELECT COUNT(*) FROM profile_call_fact_cache WHERE call_id IN (SELECT call_id FROM governance_remaining_call_ids))
`).Scan(&count)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return count, nil
}

func uniqueNonEmpty(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func removeSQLiteFiles(path string) error {
	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Remove(candidate); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}
