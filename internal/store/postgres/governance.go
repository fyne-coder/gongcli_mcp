package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/fyne-coder/gongcli_mcp/internal/governance"
)

type GovernancePolicyState struct {
	ConfigSHA256        string   `json:"config_sha256"`
	DataFingerprint     string   `json:"data_fingerprint"`
	ConfigEntries       int      `json:"config_entries"`
	ConfigAliases       int      `json:"config_aliases"`
	MatchedEntries      int      `json:"matched_entries"`
	UnmatchedEntries    int      `json:"unmatched_entries"`
	SuppressedCallCount int      `json:"suppressed_call_count"`
	SuppressedCallIDs   []string `json:"suppressed_call_ids,omitempty"`
	UpdatedAt           string   `json:"updated_at"`
}

func (s *Store) GovernanceNameCandidates(ctx context.Context) ([]governance.Candidate, error) {
	return governanceNameCandidates(ctx, s.db)
}

func governanceNameCandidates(ctx context.Context, queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}) ([]governance.Candidate, error) {
	rows, err := queryer.QueryContext(ctx, `
SELECT call_id, 'call_title' AS source, title AS value
  FROM calls
 WHERE TRIM(title) <> ''
UNION ALL
SELECT call_id, 'call_raw_json' AS source, raw_json::text AS value
  FROM calls
 WHERE TRIM(raw_json::text) <> ''
UNION ALL
SELECT call_id, 'crm_object_name' AS source, object_name AS value
  FROM call_context_objects
 WHERE TRIM(object_name) <> ''
UNION ALL
SELECT call_id, 'crm_field:' || field_name AS source, field_value_text AS value
  FROM call_context_fields
 WHERE TRIM(field_value_text) <> ''
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
	return governanceDataFingerprint(ctx, s.db)
}

func governanceDataFingerprint(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (string, error) {
	var fingerprint string
	if err := queryer.QueryRowContext(ctx, `SELECT gongmcp_governance_data_fingerprint()`).Scan(&fingerprint); err != nil {
		return "", err
	}
	return fingerprint, nil
}

func (s *Store) BuildAndSaveGovernancePolicy(ctx context.Context, configSHA256 string, cfg *governance.Config) (*governance.Audit, *GovernancePolicyState, error) {
	if s.readOnly {
		return nil, nil, errors.New("postgres store is read-only")
	}
	configSHA256 = strings.TrimSpace(configSHA256)
	if configSHA256 == "" {
		return nil, nil, errors.New("governance config fingerprint is required")
	}
	if cfg == nil {
		return nil, nil, errors.New("governance config is required")
	}
	now := nowUTC()
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()
	candidates, err := governanceNameCandidates(ctx, tx)
	if err != nil {
		return nil, nil, err
	}
	dataFingerprint, err := governanceDataFingerprint(ctx, tx)
	if err != nil {
		return nil, nil, err
	}
	audit := governance.AuditCandidates(candidates, cfg)
	if _, err := tx.ExecContext(ctx, `INSERT INTO governance_policy_state(
	config_sha256, data_fingerprint, config_entries, config_aliases,
	matched_entries, unmatched_entries, suppressed_call_count, updated_at
) VALUES($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT(config_sha256) DO UPDATE SET
	data_fingerprint = EXCLUDED.data_fingerprint,
	config_entries = EXCLUDED.config_entries,
	config_aliases = EXCLUDED.config_aliases,
	matched_entries = EXCLUDED.matched_entries,
	unmatched_entries = EXCLUDED.unmatched_entries,
	suppressed_call_count = EXCLUDED.suppressed_call_count,
	updated_at = EXCLUDED.updated_at`,
		configSHA256,
		dataFingerprint,
		audit.ConfigEntries,
		audit.ConfigAliases,
		len(audit.MatchedEntries),
		len(audit.UnmatchedEntries),
		audit.SuppressedCallCount,
		now,
	); err != nil {
		return nil, nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM governance_suppressed_calls WHERE config_sha256 = $1`, configSHA256); err != nil {
		return nil, nil, err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO governance_suppressed_calls(config_sha256, call_id) VALUES($1, $2) ON CONFLICT DO NOTHING`)
	if err != nil {
		return nil, nil, err
	}
	for _, callID := range audit.SuppressedCallIDs {
		if trimmed := strings.TrimSpace(callID); trimmed != "" {
			if _, err := stmt.ExecContext(ctx, configSHA256, trimmed); err != nil {
				_ = stmt.Close()
				return nil, nil, err
			}
		}
	}
	if err := stmt.Close(); err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	policy, err := s.LoadGovernancePolicy(ctx, configSHA256)
	if err != nil {
		return nil, nil, err
	}
	return audit, policy, nil
}

func (s *Store) LoadGovernancePolicy(ctx context.Context, configSHA256 string) (*GovernancePolicyState, error) {
	configSHA256 = strings.TrimSpace(configSHA256)
	if configSHA256 == "" {
		return nil, errors.New("governance config fingerprint is required")
	}
	state := &GovernancePolicyState{}
	if err := s.db.QueryRowContext(ctx, `
SELECT config_sha256, data_fingerprint, config_entries, config_aliases,
       matched_entries, unmatched_entries, suppressed_call_count, updated_at
  FROM gongmcp_governance_policy_state($1)`, configSHA256).Scan(
		&state.ConfigSHA256,
		&state.DataFingerprint,
		&state.ConfigEntries,
		&state.ConfigAliases,
		&state.MatchedEntries,
		&state.UnmatchedEntries,
		&state.SuppressedCallCount,
		&state.UpdatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("postgres AI governance policy is not prepared for this config; run gongctl governance audit --apply-postgres-policy with the writable Postgres URL")
		}
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx, `SELECT call_id FROM gongmcp_governance_suppressed_call_ids($1) ORDER BY call_id`, configSHA256)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var callID string
		if err := rows.Scan(&callID); err != nil {
			return nil, err
		}
		state.SuppressedCallIDs = append(state.SuppressedCallIDs, callID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return state, nil
}
