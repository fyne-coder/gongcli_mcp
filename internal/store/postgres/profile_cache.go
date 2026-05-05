package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	profilepkg "github.com/fyne-coder/gongcli_mcp/internal/profile"
	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
)

type profileCallFact struct {
	CallID               string
	Title                string
	StartedAt            string
	DurationSeconds      int64
	System               string
	Direction            string
	Scope                string
	Purpose              string
	CalendarEventPresent bool
	TranscriptPresent    bool
	LifecycleBucket      string
	LifecycleConfidence  string
	LifecycleReason      string
	EvidenceFields       []string
	DealCount            int64
	AccountCount         int64
	Objects              []profileCallObject
	FieldValues          map[string][]string
}

type profileCallObject struct {
	ObjectType string
	Fields     map[string][]string
}

func (s *Store) RefreshActiveProfileReadModel(ctx context.Context) error {
	if err := s.ensureWritable(); err != nil {
		return err
	}
	meta, p, _, err := s.activeProfile(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	fingerprint, err := s.profileDataFingerprint(ctx)
	if err != nil {
		return err
	}
	return s.rebuildProfileCallFactCache(ctx, meta, p, fingerprint)
}

func (s *Store) ResolveLifecycleSource(ctx context.Context, requested string) (string, *sqlite.BusinessProfile, error) {
	source := strings.ToLower(strings.TrimSpace(requested))
	switch source {
	case "", sqlite.LifecycleSourceAuto:
		meta, p, warnings, err := s.activeProfile(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return sqlite.LifecycleSourceBuiltin, nil, nil
		}
		if err != nil {
			return "", nil, err
		}
		fingerprint, err := s.profileDataFingerprint(ctx)
		if err != nil {
			return "", nil, err
		}
		if err := s.ensureProfileCallFactCache(ctx, meta, p, fingerprint); err != nil {
			return "", nil, err
		}
		return sqlite.LifecycleSourceProfile, businessProfileFrom(meta, p, warnings), nil
	case sqlite.LifecycleSourceBuiltin:
		return sqlite.LifecycleSourceBuiltin, nil, nil
	case sqlite.LifecycleSourceProfile:
		meta, p, warnings, err := s.activeProfile(ctx)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return "", nil, errors.New("no active profile; run gongctl profile discover, validate, and import first")
			}
			return "", nil, err
		}
		fingerprint, err := s.profileDataFingerprint(ctx)
		if err != nil {
			return "", nil, err
		}
		if err := s.ensureProfileCallFactCache(ctx, meta, p, fingerprint); err != nil {
			return "", nil, err
		}
		return sqlite.LifecycleSourceProfile, businessProfileFrom(meta, p, warnings), nil
	default:
		return "", nil, fmt.Errorf("lifecycle_source must be one of: auto, builtin, profile")
	}
}

func (s *Store) profileCallFacts(ctx context.Context) ([]profileCallFact, *profilepkg.Profile, error) {
	meta, p, err := s.profileFactsReady(ctx)
	if err != nil {
		return nil, nil, err
	}
	calls, err := s.loadProfileCallFactCache(ctx, meta.ID, meta.CanonicalSHA256)
	if err != nil {
		return nil, nil, err
	}
	return calls, p, nil
}

func (s *Store) profileFactsReady(ctx context.Context) (*profileMetaRecord, *profilepkg.Profile, error) {
	meta, p, _, err := s.activeProfile(ctx)
	if err != nil {
		return nil, nil, err
	}
	fingerprint, err := s.profileDataFingerprint(ctx)
	if err != nil {
		return nil, nil, err
	}
	if err := s.ensureProfileCallFactCache(ctx, meta, p, fingerprint); err != nil {
		return nil, nil, err
	}
	return meta, p, nil
}

func (s *Store) ensureProfileCallFactCache(ctx context.Context, meta *profileMetaRecord, p *profilepkg.Profile, fingerprint string) error {
	var existingFingerprint string
	var existingCanonical string
	query := `SELECT canonical_sha256, data_fingerprint FROM profile_call_fact_cache_meta WHERE profile_id = $1`
	if s.readOnly {
		query = `SELECT canonical_sha256, data_fingerprint FROM gongmcp_profile_call_fact_cache_meta($1, $2)`
	}
	var err error
	if s.readOnly {
		err = s.db.QueryRowContext(ctx, query, meta.ID, meta.CanonicalSHA256).Scan(&existingCanonical, &existingFingerprint)
	} else {
		err = s.db.QueryRowContext(ctx, query, meta.ID).Scan(&existingCanonical, &existingFingerprint)
	}
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	} else if existingCanonical == meta.CanonicalSHA256 && existingFingerprint == fingerprint {
		return nil
	}
	if s.readOnly {
		return errors.New("profile read model is missing or stale; run gongctl sync read-model --rebuild with a writable Postgres URL or run a writable profile-aware CLI command before using profile-aware MCP tools")
	}
	return s.rebuildProfileCallFactCache(ctx, meta, p, fingerprint)
}

func (s *Store) rebuildProfileCallFactCache(ctx context.Context, meta *profileMetaRecord, p *profilepkg.Profile, fingerprint string) error {
	calls, err := s.profileBaseCalls(ctx)
	if err != nil {
		return err
	}
	objects, err := s.profileContextObjects(ctx)
	if err != nil {
		return err
	}
	for idx := range calls {
		callObjects := objects[calls[idx].CallID]
		calls[idx].Objects = callObjects
		calls[idx].DealCount = int64(countObjectsForConcept(p, callObjects, "deal"))
		calls[idx].AccountCount = int64(countObjectsForConcept(p, callObjects, "account"))
		calls[idx].FieldValues = fieldConceptValues(p, callObjects)
		classifyProfileFact(p, &calls[idx])
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM profile_call_fact_cache WHERE profile_id = $1`, meta.ID); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `
INSERT INTO profile_call_fact_cache(
	profile_id, canonical_sha256, call_id, title, started_at, duration_seconds,
	system, direction, scope, purpose, calendar_event_present, transcript_present,
	lifecycle_bucket, lifecycle_confidence, lifecycle_reason, evidence_fields_json,
	deal_count, account_count, field_values_json
) VALUES($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16::jsonb, $17, $18, $19::jsonb)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, call := range calls {
		evidence, err := json.Marshal(call.EvidenceFields)
		if err != nil {
			return err
		}
		fieldValues, err := json.Marshal(call.FieldValues)
		if err != nil {
			return err
		}
		if _, err := stmt.ExecContext(
			ctx,
			meta.ID,
			meta.CanonicalSHA256,
			call.CallID,
			call.Title,
			call.StartedAt,
			call.DurationSeconds,
			call.System,
			call.Direction,
			call.Scope,
			call.Purpose,
			call.CalendarEventPresent,
			call.TranscriptPresent,
			call.LifecycleBucket,
			call.LifecycleConfidence,
			call.LifecycleReason,
			string(evidence),
			call.DealCount,
			call.AccountCount,
			string(fieldValues),
		); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO profile_call_fact_cache_meta(profile_id, canonical_sha256, data_fingerprint, built_at, call_count)
VALUES($1, $2, $3, $4, $5)
ON CONFLICT(profile_id) DO UPDATE SET
	canonical_sha256 = excluded.canonical_sha256,
	data_fingerprint = excluded.data_fingerprint,
	built_at = excluded.built_at,
	call_count = excluded.call_count`,
		meta.ID, meta.CanonicalSHA256, fingerprint, nowUTC(), len(calls)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) loadProfileCallFactCache(ctx context.Context, profileID int64, canonicalSHA256 string) ([]profileCallFact, error) {
	var expectedCount int
	metaQuery := `SELECT call_count FROM profile_call_fact_cache_meta WHERE profile_id = $1 AND canonical_sha256 = $2`
	if s.readOnly {
		metaQuery = `SELECT call_count FROM gongmcp_profile_call_fact_cache_meta($1, $2)`
	}
	if err := s.db.QueryRowContext(ctx, metaQuery, profileID, canonicalSHA256).Scan(&expectedCount); err != nil {
		return nil, err
	}
	rowsQuery := `
SELECT call_id,
       title,
       started_at,
       duration_seconds,
       system,
       direction,
       scope,
       purpose,
       calendar_event_present,
       transcript_present,
       lifecycle_bucket,
       lifecycle_confidence,
       lifecycle_reason,
       evidence_fields_json::text,
       deal_count,
       account_count,
       field_values_json::text
  FROM profile_call_fact_cache
 WHERE profile_id = $1
   AND canonical_sha256 = $2
 ORDER BY started_at DESC, call_id`
	if s.readOnly {
		rowsQuery = `
SELECT call_id,
       title,
       started_at,
       duration_seconds,
       system,
       direction,
       scope,
       purpose,
       calendar_event_present,
       transcript_present,
       lifecycle_bucket,
       lifecycle_confidence,
       lifecycle_reason,
       evidence_fields_json::text,
       deal_count,
       account_count
  FROM gongmcp_profile_call_fact_cache($1, $2)
 ORDER BY started_at DESC, call_id`
	}
	rows, err := s.db.QueryContext(ctx, rowsQuery, profileID, canonicalSHA256)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]profileCallFact, 0)
	for rows.Next() {
		var row profileCallFact
		var evidenceJSON []byte
		var fieldValuesJSON []byte
		if s.readOnly {
			if err := rows.Scan(
				&row.CallID,
				&row.Title,
				&row.StartedAt,
				&row.DurationSeconds,
				&row.System,
				&row.Direction,
				&row.Scope,
				&row.Purpose,
				&row.CalendarEventPresent,
				&row.TranscriptPresent,
				&row.LifecycleBucket,
				&row.LifecycleConfidence,
				&row.LifecycleReason,
				&evidenceJSON,
				&row.DealCount,
				&row.AccountCount,
			); err != nil {
				return nil, err
			}
		} else {
			if err := rows.Scan(
				&row.CallID,
				&row.Title,
				&row.StartedAt,
				&row.DurationSeconds,
				&row.System,
				&row.Direction,
				&row.Scope,
				&row.Purpose,
				&row.CalendarEventPresent,
				&row.TranscriptPresent,
				&row.LifecycleBucket,
				&row.LifecycleConfidence,
				&row.LifecycleReason,
				&evidenceJSON,
				&row.DealCount,
				&row.AccountCount,
				&fieldValuesJSON,
			); err != nil {
				return nil, err
			}
		}
		if len(evidenceJSON) > 0 {
			if err := json.Unmarshal(evidenceJSON, &row.EvidenceFields); err != nil {
				return nil, err
			}
		}
		if len(fieldValuesJSON) > 0 {
			if err := json.Unmarshal(fieldValuesJSON, &row.FieldValues); err != nil {
				return nil, err
			}
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) != expectedCount {
		return nil, errors.New("profile read model changed during query; retry after sync completes")
	}
	return out, nil
}

func (s *Store) profileDataFingerprint(ctx context.Context) (string, error) {
	var callCount, objectCount, fieldCount, transcriptCount int64
	var maxCallUpdated, maxTranscriptUpdated, objectFingerprint, fieldFingerprint string
	if s.readOnly {
		var fingerprint string
		if err := s.db.QueryRowContext(ctx, `SELECT fingerprint FROM gongmcp_profile_data_fingerprint()`).Scan(&fingerprint); err != nil {
			return "", err
		}
		return fingerprint, nil
	}
	if err := s.db.QueryRowContext(ctx, `
SELECT
	(SELECT COUNT(*) FROM calls),
	COALESCE((SELECT MAX(updated_at) FROM calls), ''),
	(SELECT COUNT(*) FROM call_context_objects),
	COALESCE((SELECT md5(COALESCE(string_agg(call_id || E'\x1f' || object_key || E'\x1f' || object_type || E'\x1f' || object_id || E'\x1f' || object_name, E'\x1e' ORDER BY call_id, object_key), '')) FROM call_context_objects), ''),
	(SELECT COUNT(*) FROM call_context_fields),
	COALESCE((SELECT md5(COALESCE(string_agg(call_id || E'\x1f' || object_key || E'\x1f' || field_name || E'\x1f' || field_label || E'\x1f' || field_type || E'\x1f' || field_value_text, E'\x1e' ORDER BY call_id, object_key, field_name), '')) FROM call_context_fields), ''),
	(SELECT COUNT(*) FROM transcripts),
	COALESCE((SELECT MAX(updated_at) FROM transcripts), '')`).Scan(
		&callCount,
		&maxCallUpdated,
		&objectCount,
		&objectFingerprint,
		&fieldCount,
		&fieldFingerprint,
		&transcriptCount,
		&maxTranscriptUpdated,
	); err != nil {
		return "", err
	}
	return fmt.Sprintf("calls:%d:%s|objects:%d:%s|fields:%d:%s|transcripts:%d:%s", callCount, maxCallUpdated, objectCount, objectFingerprint, fieldCount, fieldFingerprint, transcriptCount, maxTranscriptUpdated), nil
}

func (s *Store) profileBaseCalls(ctx context.Context) ([]profileCallFact, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT c.call_id,
       c.title,
       c.started_at,
       c.duration_seconds,
       COALESCE(NULLIF(c.raw_json#>>'{metaData,system}', ''), NULLIF(c.raw_json->>'system', ''), '') AS system,
       COALESCE(NULLIF(c.raw_json#>>'{metaData,direction}', ''), NULLIF(c.raw_json->>'direction', ''), '') AS direction,
       COALESCE(NULLIF(TRIM(COALESCE(c.raw_json#>>'{metaData,scope}', c.raw_json->>'scope', '')), ''), 'Unknown') AS scope,
       COALESCE(NULLIF(c.raw_json#>>'{metaData,purpose}', ''), NULLIF(c.raw_json->>'purpose', ''), '') AS purpose,
       CASE WHEN COALESCE(NULLIF(c.raw_json#>>'{metaData,calendarEventId}', ''), NULLIF(c.raw_json->>'calendarEventId', ''), '') <> '' THEN true ELSE false END AS calendar_event_present,
       CASE WHEN t.call_id IS NULL THEN false ELSE true END AS transcript_present
  FROM calls c
  LEFT JOIN transcripts t
    ON t.call_id = c.call_id
 ORDER BY c.started_at DESC, c.call_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]profileCallFact, 0)
	for rows.Next() {
		var row profileCallFact
		if err := rows.Scan(&row.CallID, &row.Title, &row.StartedAt, &row.DurationSeconds, &row.System, &row.Direction, &row.Scope, &row.Purpose, &row.CalendarEventPresent, &row.TranscriptPresent); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) profileContextObjects(ctx context.Context) (map[string][]profileCallObject, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT o.call_id,
       o.object_key,
       o.object_type,
       f.field_name,
       COALESCE(f.field_value_text, '') AS field_value_text
  FROM call_context_objects o
  LEFT JOIN call_context_fields f
    ON f.call_id = o.call_id
   AND f.object_key = o.object_key
 ORDER BY o.call_id, o.object_key, f.field_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type objectKey struct {
		CallID string
		Key    string
	}
	byKey := map[objectKey]*profileCallObject{}
	order := map[string][]objectKey{}
	for rows.Next() {
		var callID, key, objectType string
		var fieldName sql.NullString
		var value sql.NullString
		if err := rows.Scan(&callID, &key, &objectType, &fieldName, &value); err != nil {
			return nil, err
		}
		k := objectKey{CallID: callID, Key: key}
		obj, ok := byKey[k]
		if !ok {
			obj = &profileCallObject{ObjectType: objectType, Fields: map[string][]string{}}
			byKey[k] = obj
			order[callID] = append(order[callID], k)
		}
		if strings.TrimSpace(fieldName.String) != "" {
			obj.Fields[fieldName.String] = append(obj.Fields[fieldName.String], value.String)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := map[string][]profileCallObject{}
	for callID, keys := range order {
		for _, key := range keys {
			out[callID] = append(out[callID], *byKey[key])
		}
	}
	return out, nil
}

func fieldConceptValues(p *profilepkg.Profile, objects []profileCallObject) map[string][]string {
	out := map[string][]string{}
	for concept, mapping := range p.Fields {
		objectTypes := p.Objects[mapping.Object].ObjectTypes
		for _, obj := range objects {
			if !containsString(objectTypes, obj.ObjectType) {
				continue
			}
			for _, fieldName := range mapping.Names {
				for _, value := range obj.Fields[fieldName] {
					if strings.TrimSpace(value) != "" {
						out[concept] = []string{value}
						goto nextConcept
					}
				}
			}
		}
	nextConcept:
	}
	return out
}

func countObjectsForConcept(p *profilepkg.Profile, objects []profileCallObject, concept string) int {
	mapping, ok := p.Objects[concept]
	if !ok {
		return 0
	}
	count := 0
	for _, obj := range objects {
		if containsString(mapping.ObjectTypes, obj.ObjectType) {
			count++
		}
	}
	return count
}

func classifyProfileFact(p *profilepkg.Profile, fact *profileCallFact) {
	fact.LifecycleBucket = "unknown"
	fact.LifecycleConfidence = "low"
	fact.LifecycleReason = "No profile lifecycle rule matched"
	for _, bucket := range orderedLifecycleBuckets(p) {
		if bucket == "unknown" {
			continue
		}
		definition := p.Lifecycle[bucket]
		for _, rule := range definition.Rules {
			values := valuesForRule(p, fact, rule)
			matched, err := profilepkg.EvaluateRule(values, rule)
			if err != nil || !matched {
				continue
			}
			fact.LifecycleBucket = bucket
			fact.LifecycleConfidence = "high"
			fact.LifecycleReason = "Matched profile lifecycle rule"
			if rule.Field != "" {
				fact.EvidenceFields = []string{rule.Field}
			} else if rule.Object != "" && rule.FieldName != "" {
				fact.EvidenceFields = []string{rule.Object + "." + rule.FieldName}
			}
			return
		}
	}
}

func valuesForRule(p *profilepkg.Profile, fact *profileCallFact, rule profilepkg.Rule) []string {
	if rule.Field != "" {
		return fact.FieldValues[rule.Field]
	}
	if rule.Object == "" || rule.FieldName == "" {
		return nil
	}
	objectTypes := p.Objects[rule.Object].ObjectTypes
	var out []string
	for _, obj := range fact.Objects {
		if !containsString(objectTypes, obj.ObjectType) {
			continue
		}
		for _, value := range obj.Fields[rule.FieldName] {
			if strings.TrimSpace(value) != "" {
				out = append(out, value)
			}
		}
	}
	return uniqueSorted(out)
}

func orderedLifecycleBuckets(p *profilepkg.Profile) []string {
	buckets := make([]string, 0, len(p.Lifecycle))
	for bucket := range p.Lifecycle {
		buckets = append(buckets, bucket)
	}
	order := lifecycleOrderMap(p)
	sort.Slice(buckets, func(i, j int) bool {
		if order[buckets[i]] != order[buckets[j]] {
			return order[buckets[i]] < order[buckets[j]]
		}
		return buckets[i] < buckets[j]
	})
	return buckets
}

func lifecycleOrderMap(p *profilepkg.Profile) map[string]int64 {
	out := map[string]int64{}
	for bucket, mapping := range p.Lifecycle {
		order := mapping.Order
		if order == 0 {
			order = 500
		}
		out[bucket] = int64(order)
	}
	return out
}

func (s *Store) summarizeProfileCallFacts(ctx context.Context, params sqlite.CallFactsSummaryParams) ([]sqlite.CallFactsSummaryRow, []string, error) {
	meta, p, err := s.profileFactsReady(ctx)
	if err != nil {
		return nil, nil, err
	}
	groupBy, unavailable, err := validateProfileGroupBy(p, params.GroupBy)
	if err != nil {
		return nil, nil, err
	}
	if err := validateProfileSummaryFilters(params); err != nil {
		return nil, nil, err
	}
	unavailable = mergeUnavailableConcepts(unavailable, profileUnavailableConcepts(p, groupBy))
	if s.readOnly {
		rows, err := s.summarizeProfileFactsRowsSQL(ctx, meta.ID, meta.CanonicalSHA256, groupBy, params)
		return rows, unavailable, err
	}
	facts, err := s.loadProfileCallFactCache(ctx, meta.ID, meta.CanonicalSHA256)
	if err != nil {
		return nil, nil, err
	}
	rows := summarizeProfileFactsRows(facts, groupBy, params.LifecycleBucket, params.Scope, params.System, params.Direction, params.TranscriptStatus, params.Limit)
	return rows, unavailable, nil
}

func (s *Store) summarizeProfileFactsRowsSQL(ctx context.Context, profileID int64, canonicalSHA256 string, groupBy string, params sqlite.CallFactsSummaryParams) ([]sqlite.CallFactsSummaryRow, error) {
	scope := ""
	if value := strings.TrimSpace(params.Scope); value != "" {
		if normalized, ok := normalizePostgresScope(value); ok {
			scope = normalized
		}
	}
	transcriptStatus := ""
	if value := strings.TrimSpace(params.TranscriptStatus); value != "" {
		if normalized, ok := normalizePostgresTranscriptStatus(value); ok {
			transcriptStatus = normalized
		}
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT group_by,
       group_value,
       call_count,
       transcript_count,
       missing_transcript_count,
       opportunity_call_count,
       account_call_count,
       external_call_count,
       internal_call_count,
       unknown_scope_call_count,
       total_duration_seconds,
       avg_duration_seconds,
       latest_call_at
  FROM gongmcp_profile_call_fact_summary($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		profileID,
		canonicalSHA256,
		groupBy,
		strings.TrimSpace(params.LifecycleBucket),
		scope,
		strings.TrimSpace(params.System),
		strings.TrimSpace(params.Direction),
		transcriptStatus,
		boundedLimit(params.Limit, defaultCallFactsLimit, maxCallFactsLimit),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]sqlite.CallFactsSummaryRow, 0)
	for rows.Next() {
		var row sqlite.CallFactsSummaryRow
		if err := rows.Scan(&row.GroupBy, &row.GroupValue, &row.CallCount, &row.TranscriptCount, &row.MissingTranscriptCount, &row.OpportunityCallCount, &row.AccountCallCount, &row.ExternalCallCount, &row.InternalCallCount, &row.UnknownScopeCallCount, &row.TotalDurationSeconds, &row.AvgDurationSeconds, &row.LatestCallAt); err != nil {
			return nil, err
		}
		row.TranscriptCoverageRate = postgresRate(row.TranscriptCount, row.CallCount)
		out = append(out, row)
	}
	return out, rows.Err()
}

func validateProfileSummaryFilters(params sqlite.CallFactsSummaryParams) error {
	if value := strings.TrimSpace(params.Scope); value != "" {
		if _, ok := normalizePostgresScope(value); !ok {
			return errors.New("scope must be one of: External, Internal, Unknown")
		}
	}
	if value := strings.TrimSpace(params.TranscriptStatus); value != "" && value != "any" {
		if _, ok := normalizePostgresTranscriptStatus(value); !ok {
			return errors.New("transcript_status must be one of: present, missing")
		}
	}
	return nil
}

func validateProfileGroupBy(p *profilepkg.Profile, groupBy string) (string, []string, error) {
	normalized := normalizeProfileGroupBy(groupBy)
	switch normalized {
	case "", "lifecycle", "scope", "system", "direction", "transcript_status", "duration_bucket", "month", "calendar":
		if normalized == "" {
			return "lifecycle", nil, nil
		}
		return normalized, nil, nil
	}
	if _, ok := p.Fields[normalized]; ok {
		return normalized, nil, nil
	}
	switch normalized {
	case "deal_stage", "deal_type", "account_type", "account_industry", "revenue_range", "account_revenue_range", "lead_source", "forecast_category":
		return normalized, []string{normalized}, nil
	default:
		return "", nil, fmt.Errorf("unsupported profile group_by %q", groupBy)
	}
}

func mergeUnavailableConcepts(left []string, right []string) []string {
	if len(left) == 0 {
		return right
	}
	if len(right) == 0 {
		return left
	}
	seen := map[string]struct{}{}
	for _, value := range left {
		seen[value] = struct{}{}
	}
	for _, value := range right {
		seen[value] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for value := range seen {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func summarizeProfileFactsRows(facts []profileCallFact, groupBy string, lifecycle string, scope string, system string, direction string, transcriptStatus string, limitValue int) []sqlite.CallFactsSummaryRow {
	limit := boundedLimit(limitValue, defaultCallFactsLimit, maxCallFactsLimit)
	groups := map[string]*sqlite.CallFactsSummaryRow{}
	for _, fact := range facts {
		if !profileFactMatchesFilters(fact, lifecycle, scope, system, direction, transcriptStatus) {
			continue
		}
		groupValue := profileGroupValue(fact, groupBy)
		row := groups[groupValue]
		if row == nil {
			row = &sqlite.CallFactsSummaryRow{GroupBy: groupBy, GroupValue: groupValue}
			groups[groupValue] = row
		}
		addFactToSummary(row, fact)
	}
	out := make([]sqlite.CallFactsSummaryRow, 0, len(groups))
	for _, row := range groups {
		row.TranscriptCoverageRate = postgresRate(row.TranscriptCount, row.CallCount)
		out = append(out, *row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CallCount != out[j].CallCount {
			return out[i].CallCount > out[j].CallCount
		}
		return out[i].GroupValue < out[j].GroupValue
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func coverageFromProfileFacts(facts []profileCallFact) *sqlite.CallFactsCoverage {
	coverage := &sqlite.CallFactsCoverage{}
	for _, fact := range facts {
		coverage.TotalCalls++
		if fact.TranscriptPresent {
			coverage.TranscriptCount++
		} else {
			coverage.MissingTranscriptCount++
		}
		if fact.DealCount > 0 {
			coverage.OpportunityCallCount++
		}
		if fact.AccountCount > 0 {
			coverage.AccountCallCount++
		}
		switch fact.Scope {
		case "External":
			coverage.ExternalCallCount++
		case "Internal":
			coverage.InternalCallCount++
		default:
			coverage.UnknownScopeCallCount++
		}
		if strings.TrimSpace(fact.Purpose) != "" {
			coverage.PurposePopulatedCalls++
		}
		if fact.CalendarEventPresent {
			coverage.CalendarCallCount++
		}
		coverage.TotalDurationSeconds += fact.DurationSeconds
	}
	coverage.TranscriptCoverageRate = postgresRate(coverage.TranscriptCount, coverage.TotalCalls)
	return coverage
}

func profileFactMatchesFilters(fact profileCallFact, lifecycle string, scope string, system string, direction string, transcriptStatus string) bool {
	if lifecycle != "" && fact.LifecycleBucket != lifecycle {
		return false
	}
	if scope != "" {
		normalized, ok := normalizePostgresScope(scope)
		if ok && fact.Scope != normalized {
			return false
		}
	}
	if system != "" && fact.System != system {
		return false
	}
	if direction != "" && fact.Direction != direction {
		return false
	}
	if transcriptStatus != "" {
		status, ok := normalizePostgresTranscriptStatus(transcriptStatus)
		if ok {
			if status == "present" && !fact.TranscriptPresent {
				return false
			}
			if status == "missing" && fact.TranscriptPresent {
				return false
			}
		}
	}
	return true
}

func profileGroupValue(fact profileCallFact, groupBy string) string {
	switch groupBy {
	case "lifecycle":
		return blankGroup(fact.LifecycleBucket)
	case "scope":
		return blankGroup(fact.Scope)
	case "system":
		return blankGroup(fact.System)
	case "direction":
		return blankGroup(fact.Direction)
	case "transcript_status":
		if fact.TranscriptPresent {
			return "present"
		}
		return "missing"
	case "duration_bucket":
		return durationBucket(fact.DurationSeconds)
	case "month":
		if len(fact.StartedAt) >= 7 {
			return fact.StartedAt[:7]
		}
		return "<blank>"
	case "calendar":
		if fact.CalendarEventPresent {
			return "calendar"
		}
		return "no_calendar"
	default:
		values := fact.FieldValues[groupBy]
		if len(values) == 0 {
			return "<blank>"
		}
		return blankGroup(values[0])
	}
}

func addFactToSummary(row *sqlite.CallFactsSummaryRow, fact profileCallFact) {
	row.CallCount++
	if fact.TranscriptPresent {
		row.TranscriptCount++
	} else {
		row.MissingTranscriptCount++
	}
	if fact.DealCount > 0 {
		row.OpportunityCallCount++
	}
	if fact.AccountCount > 0 {
		row.AccountCallCount++
	}
	switch fact.Scope {
	case "External":
		row.ExternalCallCount++
	case "Internal":
		row.InternalCallCount++
	default:
		row.UnknownScopeCallCount++
	}
	row.TotalDurationSeconds += fact.DurationSeconds
	row.AvgDurationSeconds = float64(row.TotalDurationSeconds) / float64(row.CallCount)
	if fact.StartedAt > row.LatestCallAt {
		row.LatestCallAt = fact.StartedAt
	}
}

func lifecycleSearchResultFromProfileFact(fact profileCallFact) sqlite.LifecycleCallSearchResult {
	return sqlite.LifecycleCallSearchResult{
		CallID:            fact.CallID,
		Title:             fact.Title,
		StartedAt:         fact.StartedAt,
		DurationSeconds:   fact.DurationSeconds,
		Bucket:            fact.LifecycleBucket,
		Confidence:        fact.LifecycleConfidence,
		Reason:            fact.LifecycleReason,
		EvidenceFields:    fact.EvidenceFields,
		OpportunityCount:  fact.DealCount,
		AccountCount:      fact.AccountCount,
		TranscriptPresent: fact.TranscriptPresent,
	}
}

func lifecycleRuleSignals(rules []profilepkg.Rule) []string {
	out := make([]string, 0, len(rules))
	for _, rule := range rules {
		if rule.Field != "" {
			out = append(out, rule.Field+" "+rule.Op)
			continue
		}
		if rule.Object != "" && rule.FieldName != "" {
			out = append(out, rule.Object+"."+rule.FieldName+" "+rule.Op)
		}
	}
	return out
}

func profilePriorityScore(fact profileCallFact, order map[string]int64) int64 {
	score := int64(100)
	if value, ok := order[fact.LifecycleBucket]; ok {
		score = 100 - value
	}
	if fact.LifecycleBucket == "closed_won" || fact.LifecycleBucket == "closed_lost" || fact.LifecycleBucket == "post_sales" {
		score += 50
	}
	if fact.LifecycleConfidence == "high" {
		score += 20
	}
	if fact.Scope == "External" {
		score += 25
	}
	if fact.Direction == "Conference" {
		score += 20
	}
	if fact.DurationSeconds >= 1800 {
		score += 20
	} else if fact.DurationSeconds >= 600 {
		score += 10
	}
	if fact.DealCount > 0 {
		score += 10
	}
	if fact.DurationSeconds > 0 && fact.DurationSeconds < 300 && fact.Direction != "Conference" {
		score -= 20
	}
	return score
}

func normalizeProfileGroupBy(groupBy string) string {
	switch strings.ToLower(strings.TrimSpace(groupBy)) {
	case "", "lifecycle", "lifecycle_bucket":
		return "lifecycle"
	case "stage", "deal_stage", "opportunity_stage":
		return "deal_stage"
	case "deal_type", "opportunity_type":
		return "deal_type"
	case "account_industry", "industry":
		return "account_industry"
	case "account_type":
		return "account_type"
	case "revenue_range", "account_revenue_range":
		return "account_revenue_range"
	case "scope":
		return "scope"
	case "system":
		return "system"
	case "direction":
		return "direction"
	case "transcript_status":
		return "transcript_status"
	case "calendar", "calendar_event_status":
		return "calendar"
	case "duration_bucket":
		return "duration_bucket"
	case "month", "call_month":
		return "month"
	case "lead_source", "primary_lead_source":
		return "lead_source"
	case "forecast_category":
		return "forecast_category"
	default:
		return strings.ToLower(strings.TrimSpace(groupBy))
	}
}

func durationBucket(seconds int64) string {
	switch {
	case seconds < 60:
		return "under_1m"
	case seconds < 300:
		return "1_5m"
	case seconds < 900:
		return "5_15m"
	case seconds < 1800:
		return "15_30m"
	case seconds < 2700:
		return "30_45m"
	default:
		return "45m_plus"
	}
}

func blankGroup(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "<blank>"
	}
	return value
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func uniqueSorted(values []string) []string {
	set := map[string]struct{}{}
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			set[value] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
