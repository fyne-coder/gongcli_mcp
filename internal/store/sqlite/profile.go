package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os/user"
	"sort"
	"strings"

	profilepkg "github.com/arthurlee/gongctl/internal/profile"
)

const (
	LifecycleSourceAuto    = "auto"
	LifecycleSourceBuiltin = "builtin"
	LifecycleSourceProfile = "profile"
)

type ProfileImportParams struct {
	SourcePath      string
	SourceSHA256    string
	CanonicalSHA256 string
	RawYAML         []byte
	CanonicalJSON   []byte
	Profile         *profilepkg.Profile
	Findings        []profilepkg.Finding
	ImportedBy      string
}

type ProfileImportResult struct {
	ProfileID       int64  `json:"profile_id"`
	Imported        bool   `json:"imported"`
	Activated       bool   `json:"activated"`
	SourceSHA256    string `json:"source_sha256"`
	CanonicalSHA256 string `json:"canonical_sha256"`
}

type BusinessProfile struct {
	ProfileID           int64                `json:"profile_id"`
	Name                string               `json:"name,omitempty"`
	Version             int                  `json:"version"`
	SourcePath          string               `json:"source_path"`
	SourceSHA256        string               `json:"source_sha256"`
	CanonicalSHA256     string               `json:"canonical_sha256"`
	ImportedAt          string               `json:"imported_at"`
	ImportedBy          string               `json:"imported_by"`
	IsActive            bool                 `json:"is_active"`
	LifecycleCore       []string             `json:"lifecycle_core"`
	ObjectConcepts      []BusinessConcept    `json:"object_concepts"`
	FieldConcepts       []BusinessConcept    `json:"field_concepts"`
	LifecycleBuckets    []BusinessConcept    `json:"lifecycle_buckets"`
	MethodologyConcepts []BusinessConcept    `json:"methodology_concepts"`
	Warnings            []profilepkg.Finding `json:"warnings,omitempty"`
	UnavailableConcepts []string             `json:"unavailable_concepts,omitempty"`
}

type BusinessConcept struct {
	Kind        string   `json:"kind"`
	Name        string   `json:"name"`
	Label       string   `json:"label,omitempty"`
	Description string   `json:"description,omitempty"`
	Object      string   `json:"object,omitempty"`
	ObjectTypes []string `json:"object_types,omitempty"`
	FieldNames  []string `json:"field_names,omitempty"`
	Aliases     []string `json:"aliases,omitempty"`
}

type ProfileQueryInfo struct {
	LifecycleSource     string           `json:"lifecycle_source"`
	Profile             *BusinessProfile `json:"profile,omitempty"`
	UnavailableConcepts []string         `json:"unavailable_concepts,omitempty"`
}

type UnmappedCRMFieldParams struct {
	Limit int
}

type UnmappedCRMField struct {
	ObjectType         string  `json:"object_type"`
	FieldName          string  `json:"field_name"`
	FieldLabel         string  `json:"field_label,omitempty"`
	FieldType          string  `json:"field_type,omitempty"`
	ObjectCount        int64   `json:"object_count"`
	PopulatedCount     int64   `json:"populated_count"`
	PopulationRate     float64 `json:"population_rate"`
	DistinctValueCount int64   `json:"distinct_value_count"`
	MinValueLength     int64   `json:"min_value_length"`
	MaxValueLength     int64   `json:"max_value_length"`
	AvgValueLength     float64 `json:"avg_value_length"`
}

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

func (s *Store) ProfileInventory(ctx context.Context) (*profilepkg.Inventory, error) {
	objects, err := s.profileInventoryObjects(ctx)
	if err != nil {
		return nil, err
	}
	fields, err := s.profileInventoryFields(ctx)
	if err != nil {
		return nil, err
	}
	return &profilepkg.Inventory{Objects: objects, Fields: fields}, nil
}

func (s *Store) ImportProfile(ctx context.Context, params ProfileImportParams) (*ProfileImportResult, error) {
	if params.Profile == nil {
		return nil, errors.New("profile is required")
	}
	if len(params.RawYAML) == 0 {
		return nil, errors.New("raw profile YAML is required")
	}
	sourceSHA256 := profilepkg.SourceHash(params.RawYAML)
	if strings.TrimSpace(params.SourceSHA256) != "" && params.SourceSHA256 != sourceSHA256 {
		return nil, errors.New("source_sha256 does not match raw profile YAML")
	}
	parsedProfile, err := profilepkg.ParseYAML(params.RawYAML)
	if err != nil {
		return nil, err
	}
	parsedCanonicalJSON, err := profilepkg.CanonicalJSON(parsedProfile)
	if err != nil {
		return nil, err
	}
	canonicalJSON, err := profilepkg.CanonicalJSON(params.Profile)
	if err != nil {
		return nil, err
	}
	if string(parsedCanonicalJSON) != string(canonicalJSON) {
		return nil, errors.New("raw profile YAML does not match profile")
	}
	canonicalSHA256 := profilepkg.SourceHash(canonicalJSON)
	if strings.TrimSpace(params.CanonicalSHA256) != "" && params.CanonicalSHA256 != canonicalSHA256 {
		return nil, errors.New("canonical_sha256 does not match canonical profile JSON")
	}
	if len(params.CanonicalJSON) > 0 && !json.Valid(params.CanonicalJSON) {
		return nil, errors.New("canonical_json is not valid JSON")
	}
	if len(params.CanonicalJSON) > 0 && string(params.CanonicalJSON) != string(canonicalJSON) {
		return nil, errors.New("canonical_json does not match profile")
	}
	inventory, err := s.ProfileInventory(ctx)
	if err != nil {
		return nil, err
	}
	validationFindings := profilepkg.Validate(params.Profile, inventory)
	if !profilepkg.IsValid(validationFindings) {
		return nil, errors.New("profile validation failed; fix error findings before import")
	}
	params.Findings = validationFindings
	params.SourceSHA256 = sourceSHA256
	params.CanonicalSHA256 = canonicalSHA256
	params.CanonicalJSON = canonicalJSON

	importedBy := strings.TrimSpace(params.ImportedBy)
	if importedBy == "" {
		if current, err := user.Current(); err == nil {
			importedBy = current.Username
		}
	}
	now := nowUTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var existing struct {
		ID           int64
		SourcePath   string
		SourceSHA256 string
		ImportedAt   string
		ImportedBy   string
		RawYAML      []byte
		IsActive     int
	}
	err = tx.QueryRowContext(ctx, `
SELECT id, source_path, source_sha256, imported_at, imported_by, raw_yaml, is_active
  FROM profile_meta
 WHERE canonical_sha256 = ?`, params.CanonicalSHA256).Scan(
		&existing.ID,
		&existing.SourcePath,
		&existing.SourceSHA256,
		&existing.ImportedAt,
		&existing.ImportedBy,
		&existing.RawYAML,
		&existing.IsActive,
	)
	switch {
	case err == nil:
		sourceChanged := existing.SourceSHA256 != params.SourceSHA256 || existing.SourcePath != params.SourcePath || !bytes.Equal(existing.RawYAML, params.RawYAML)
		activationChanged := existing.IsActive != 1
		if !sourceChanged && !activationChanged {
			if err := tx.Commit(); err != nil {
				return nil, err
			}
			if err := s.RefreshActiveProfileReadModel(ctx); err != nil {
				return nil, err
			}
			return &ProfileImportResult{ProfileID: existing.ID, Imported: false, Activated: false, SourceSHA256: params.SourceSHA256, CanonicalSHA256: params.CanonicalSHA256}, nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE profile_meta SET is_active = 0 WHERE id <> ?`, existing.ID); err != nil {
			return nil, err
		}
		if sourceChanged {
			if _, err := tx.ExecContext(ctx, `
UPDATE profile_meta
   SET source_path = ?, source_sha256 = ?, imported_at = ?, imported_by = ?, raw_yaml = ?, is_active = 1
 WHERE id = ?`,
				params.SourcePath, params.SourceSHA256, now, importedBy, params.RawYAML, existing.ID); err != nil {
				return nil, err
			}
			if err := replaceProfileWarnings(ctx, tx, existing.ID, params.Findings); err != nil {
				return nil, err
			}
		} else if activationChanged {
			if _, err := tx.ExecContext(ctx, `UPDATE profile_meta SET is_active = 1 WHERE id = ?`, existing.ID); err != nil {
				return nil, err
			}
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		if err := s.RefreshActiveProfileReadModel(ctx); err != nil {
			return nil, err
		}
		return &ProfileImportResult{ProfileID: existing.ID, Imported: false, Activated: activationChanged, SourceSHA256: params.SourceSHA256, CanonicalSHA256: params.CanonicalSHA256}, nil
	case errors.Is(err, sql.ErrNoRows):
	default:
		return nil, err
	}

	if _, err := tx.ExecContext(ctx, `UPDATE profile_meta SET is_active = 0`); err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx, `
INSERT INTO profile_meta(name, version, source_path, source_sha256, canonical_sha256, imported_at, imported_by, is_active, raw_yaml, canonical_json)
VALUES(?, ?, ?, ?, ?, ?, ?, 1, ?, ?)`,
		params.Profile.Name,
		params.Profile.Version,
		params.SourcePath,
		params.SourceSHA256,
		params.CanonicalSHA256,
		now,
		importedBy,
		params.RawYAML,
		params.CanonicalJSON,
	)
	if err != nil {
		return nil, err
	}
	profileID, err := result.LastInsertId()
	if err != nil {
		return nil, err
	}
	if err := insertProfileMappings(ctx, tx, profileID, params.Profile); err != nil {
		return nil, err
	}
	if err := replaceProfileWarnings(ctx, tx, profileID, params.Findings); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if err := s.RefreshActiveProfileReadModel(ctx); err != nil {
		return nil, err
	}
	return &ProfileImportResult{ProfileID: profileID, Imported: true, Activated: true, SourceSHA256: params.SourceSHA256, CanonicalSHA256: params.CanonicalSHA256}, nil
}

func (s *Store) ActiveBusinessProfile(ctx context.Context) (*BusinessProfile, error) {
	meta, p, warnings, err := s.activeProfile(ctx)
	if err != nil {
		return nil, err
	}
	return businessProfileFrom(meta, p, warnings), nil
}

func (s *Store) ActiveProfileDocument(ctx context.Context) (*profilepkg.Profile, error) {
	_, p, _, err := s.activeProfile(ctx)
	return p, err
}

func (s *Store) RefreshActiveProfileReadModel(ctx context.Context) error {
	meta, p, _, err := s.activeProfile(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	fingerprint, err := s.profileDataFingerprint(ctx)
	if err != nil {
		return err
	}
	return s.rebuildProfileCallFactCache(ctx, meta, p, fingerprint)
}

func (s *Store) ListBusinessConcepts(ctx context.Context) ([]BusinessConcept, error) {
	profile, err := s.ActiveBusinessProfile(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]BusinessConcept, 0)
	out = append(out, profile.ObjectConcepts...)
	out = append(out, profile.FieldConcepts...)
	out = append(out, profile.LifecycleBuckets...)
	out = append(out, profile.MethodologyConcepts...)
	return out, nil
}

func (s *Store) ListUnmappedCRMFields(ctx context.Context, params UnmappedCRMFieldParams) ([]UnmappedCRMField, error) {
	_, p, _, err := s.activeProfile(ctx)
	if err != nil {
		return nil, err
	}
	mapped := mappedProfileFields(p)
	limit := boundedLimit(params.Limit, defaultInventoryLimit, maxInventoryLimit)
	scanLimit := limit * 4
	if scanLimit < limit {
		scanLimit = limit
	}
	if scanLimit > maxInventoryLimit*4 {
		scanLimit = maxInventoryLimit * 4
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT o.object_type,
       f.field_name,
       MAX(f.field_label) AS field_label,
       MAX(f.field_type) AS field_type,
       COUNT(DISTINCT o.object_key) AS object_count,
       COUNT(DISTINCT CASE WHEN TRIM(f.field_value_text) <> '' THEN o.object_key END) AS populated_count,
       COUNT(DISTINCT CASE WHEN TRIM(f.field_value_text) <> '' THEN f.field_value_text END) AS distinct_value_count,
       COALESCE(MIN(CASE WHEN TRIM(f.field_value_text) <> '' THEN LENGTH(f.field_value_text) END), 0) AS min_len,
       COALESCE(MAX(CASE WHEN TRIM(f.field_value_text) <> '' THEN LENGTH(f.field_value_text) END), 0) AS max_len,
       COALESCE(AVG(CASE WHEN TRIM(f.field_value_text) <> '' THEN LENGTH(f.field_value_text) END), 0) AS avg_len
  FROM call_context_fields f
  JOIN call_context_objects o
    ON o.call_id = f.call_id
   AND o.object_key = f.object_key
 GROUP BY o.object_type, f.field_name
 ORDER BY populated_count DESC, o.object_type, f.field_name
 LIMIT ?`, scanLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]UnmappedCRMField, 0)
	for rows.Next() {
		var row UnmappedCRMField
		if err := rows.Scan(
			&row.ObjectType,
			&row.FieldName,
			&row.FieldLabel,
			&row.FieldType,
			&row.ObjectCount,
			&row.PopulatedCount,
			&row.DistinctValueCount,
			&row.MinValueLength,
			&row.MaxValueLength,
			&row.AvgValueLength,
		); err != nil {
			return nil, err
		}
		if _, ok := mapped[row.ObjectType+"."+row.FieldName]; ok {
			continue
		}
		row.PopulationRate = rate(row.PopulatedCount, row.ObjectCount)
		out = append(out, row)
		if len(out) >= limit {
			break
		}
	}
	return out, rows.Err()
}

func (s *Store) ResolveLifecycleSource(ctx context.Context, requested string) (string, *BusinessProfile, error) {
	source := strings.ToLower(strings.TrimSpace(requested))
	switch source {
	case "", LifecycleSourceAuto:
		meta, p, warnings, err := s.activeProfile(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return LifecycleSourceBuiltin, nil, nil
		}
		if err != nil {
			return "", nil, err
		}
		return LifecycleSourceProfile, businessProfileFrom(meta, p, warnings), nil
	case LifecycleSourceBuiltin:
		return LifecycleSourceBuiltin, nil, nil
	case LifecycleSourceProfile:
		businessProfile, err := s.ActiveBusinessProfile(ctx)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return "", nil, errors.New("no active profile; run gongctl profile discover, validate, and import first")
			}
			return "", nil, err
		}
		return LifecycleSourceProfile, businessProfile, nil
	default:
		return "", nil, fmt.Errorf("lifecycle_source must be one of: auto, builtin, profile")
	}
}

func (s *Store) SummarizeCallFactsWithSource(ctx context.Context, params CallFactsSummaryParams) ([]CallFactsSummaryRow, *ProfileQueryInfo, error) {
	source, businessProfile, err := s.ResolveLifecycleSource(ctx, params.LifecycleSource)
	if err != nil {
		return nil, nil, err
	}
	info := &ProfileQueryInfo{LifecycleSource: source, Profile: businessProfile}
	if source == LifecycleSourceBuiltin {
		rows, err := s.SummarizeCallFacts(ctx, withBuiltinSource(params))
		return rows, info, err
	}
	rows, unavailable, err := s.summarizeProfileCallFacts(ctx, params)
	info.UnavailableConcepts = unavailable
	return rows, info, err
}

func (s *Store) ListLifecycleBucketDefinitionsWithSource(ctx context.Context, requested string) ([]LifecycleBucketDefinition, *ProfileQueryInfo, error) {
	source, businessProfile, err := s.ResolveLifecycleSource(ctx, requested)
	if err != nil {
		return nil, nil, err
	}
	info := &ProfileQueryInfo{LifecycleSource: source, Profile: businessProfile}
	if source == LifecycleSourceBuiltin {
		rows, err := s.ListLifecycleBucketDefinitions(ctx)
		return rows, info, err
	}
	_, p, _, err := s.activeProfile(ctx)
	if err != nil {
		return nil, nil, err
	}
	info.UnavailableConcepts = profileUnavailableConcepts(p, "")
	out := make([]LifecycleBucketDefinition, 0, len(p.Lifecycle))
	for _, bucket := range orderedLifecycleBuckets(p) {
		definition := p.Lifecycle[bucket]
		out = append(out, LifecycleBucketDefinition{
			Bucket:         bucket,
			Label:          definition.Label,
			Description:    definition.Description,
			PrimarySignals: lifecycleRuleSignals(definition.Rules),
		})
	}
	return out, info, nil
}

func (s *Store) SummarizeCallsByLifecycleWithSource(ctx context.Context, params LifecycleSummaryParams) ([]LifecycleBucketSummary, *ProfileQueryInfo, error) {
	source, businessProfile, err := s.ResolveLifecycleSource(ctx, params.LifecycleSource)
	if err != nil {
		return nil, nil, err
	}
	info := &ProfileQueryInfo{LifecycleSource: source, Profile: businessProfile}
	if source == LifecycleSourceBuiltin {
		rows, err := s.SummarizeCallsByLifecycle(ctx, LifecycleSummaryParams{Bucket: params.Bucket, LifecycleSource: LifecycleSourceBuiltin})
		return rows, info, err
	}
	meta, p, err := s.profileFactsReady(ctx)
	if err != nil {
		return nil, nil, err
	}
	info.UnavailableConcepts = profileUnavailableConcepts(p, "")
	bucketFilter := strings.TrimSpace(params.Bucket)
	where := `WHERE profile_id = ? AND canonical_sha256 = ?`
	args := []any{meta.ID, meta.CanonicalSHA256}
	if bucketFilter != "" {
		where += ` AND lifecycle_bucket = ?`
		args = append(args, bucketFilter)
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT lifecycle_bucket,
       COUNT(*) AS call_count,
       COALESCE(SUM(CASE WHEN transcript_present = 1 THEN 1 ELSE 0 END), 0) AS transcript_count,
       COALESCE(SUM(CASE WHEN transcript_present = 0 THEN 1 ELSE 0 END), 0) AS missing_transcript_count,
       COALESCE(SUM(CASE WHEN deal_count > 0 THEN 1 ELSE 0 END), 0) AS opportunity_call_count,
       COALESCE(SUM(CASE WHEN account_count > 0 THEN 1 ELSE 0 END), 0) AS account_call_count,
       COALESCE(SUM(duration_seconds), 0) AS total_duration_seconds,
       COALESCE(MAX(started_at), '') AS latest_call_at,
       COALESCE(SUM(CASE WHEN lifecycle_confidence = 'high' THEN 1 ELSE 0 END), 0) AS high_confidence_calls,
       COALESCE(SUM(CASE WHEN lifecycle_confidence = 'medium' THEN 1 ELSE 0 END), 0) AS medium_confidence_calls,
       COALESCE(SUM(CASE WHEN lifecycle_confidence NOT IN ('high', 'medium') THEN 1 ELSE 0 END), 0) AS low_confidence_calls
  FROM profile_call_fact_cache
`+where+`
 GROUP BY lifecycle_bucket`, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	out := make([]LifecycleBucketSummary, 0)
	latest := map[string]string{}
	for rows.Next() {
		var row LifecycleBucketSummary
		if err := rows.Scan(
			&row.Bucket,
			&row.CallCount,
			&row.TranscriptCount,
			&row.MissingTranscriptCount,
			&row.OpportunityCallCount,
			&row.AccountCallCount,
			&row.TotalDurationSeconds,
			&row.LatestCallAt,
			&row.HighConfidenceCalls,
			&row.MediumConfidenceCalls,
			&row.LowConfidenceCalls,
		); err != nil {
			return nil, nil, err
		}
		latest[row.Bucket] = row.LatestCallAt
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	for idx := range out {
		_ = s.db.QueryRowContext(ctx, `
SELECT call_id
  FROM profile_call_fact_cache
 WHERE profile_id = ?
   AND canonical_sha256 = ?
   AND lifecycle_bucket = ?
   AND started_at = ?
 ORDER BY call_id
 LIMIT 1`, meta.ID, meta.CanonicalSHA256, out[idx].Bucket, latest[out[idx].Bucket]).Scan(&out[idx].LatestCallID)
	}
	order := lifecycleOrderMap(p)
	sort.Slice(out, func(i, j int) bool {
		if order[out[i].Bucket] != order[out[j].Bucket] {
			return order[out[i].Bucket] < order[out[j].Bucket]
		}
		return out[i].CallCount > out[j].CallCount
	})
	return out, info, nil
}

func (s *Store) CallFactsCoverageWithSource(ctx context.Context, sourceArg string) (*CallFactsCoverage, []CallFactsSummaryRow, *ProfileQueryInfo, error) {
	source, businessProfile, err := s.ResolveLifecycleSource(ctx, sourceArg)
	if err != nil {
		return nil, nil, nil, err
	}
	info := &ProfileQueryInfo{LifecycleSource: source, Profile: businessProfile}
	if source == LifecycleSourceBuiltin {
		coverage, err := s.CallFactsCoverage(ctx)
		if err != nil {
			return nil, nil, nil, err
		}
		rows, err := s.SummarizeCallFacts(ctx, CallFactsSummaryParams{GroupBy: "lifecycle", LifecycleSource: LifecycleSourceBuiltin, Limit: 50})
		return coverage, rows, info, err
	}
	facts, p, err := s.profileCallFacts(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	info.UnavailableConcepts = profileUnavailableConcepts(p, "")
	coverage := coverageFromProfileFacts(facts)
	rows := summarizeProfileFactsRows(facts, "lifecycle", "", "", "", "", "", 50)
	return coverage, rows, info, nil
}

func (s *Store) SearchCallsByLifecycleWithSource(ctx context.Context, params LifecycleCallSearchParams) ([]LifecycleCallSearchResult, *ProfileQueryInfo, error) {
	source, businessProfile, err := s.ResolveLifecycleSource(ctx, params.LifecycleSource)
	if err != nil {
		return nil, nil, err
	}
	info := &ProfileQueryInfo{LifecycleSource: source, Profile: businessProfile}
	if source == LifecycleSourceBuiltin {
		rows, err := s.SearchCallsByLifecycle(ctx, withBuiltinLifecycleSearch(params))
		return rows, info, err
	}
	meta, p, err := s.profileFactsReady(ctx)
	if err != nil {
		return nil, nil, err
	}
	info.UnavailableConcepts = profileUnavailableConcepts(p, "")
	limit := boundedLimit(params.Limit, defaultLifecycleLimit, maxLifecycleLimit)
	bucket := strings.TrimSpace(params.Bucket)
	where := `WHERE profile_id = ? AND canonical_sha256 = ?`
	args := []any{meta.ID, meta.CanonicalSHA256}
	if bucket != "" {
		where += ` AND lifecycle_bucket = ?`
		args = append(args, bucket)
	}
	if params.MissingTranscriptsOnly {
		where += ` AND transcript_present = 0`
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, `
SELECT call_id, title, started_at, duration_seconds, lifecycle_bucket, lifecycle_confidence, lifecycle_reason, evidence_fields_json, deal_count, account_count, transcript_present
  FROM profile_call_fact_cache
`+where+`
 ORDER BY started_at DESC, call_id
 LIMIT ?`, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	out := make([]LifecycleCallSearchResult, 0)
	for rows.Next() {
		var row LifecycleCallSearchResult
		var evidenceJSON []byte
		var transcript int
		if err := rows.Scan(
			&row.CallID,
			&row.Title,
			&row.StartedAt,
			&row.DurationSeconds,
			&row.Bucket,
			&row.Confidence,
			&row.Reason,
			&evidenceJSON,
			&row.OpportunityCount,
			&row.AccountCount,
			&transcript,
		); err != nil {
			return nil, nil, err
		}
		row.TranscriptPresent = transcript == 1
		if len(evidenceJSON) > 0 {
			if err := json.Unmarshal(evidenceJSON, &row.EvidenceFields); err != nil {
				return nil, nil, err
			}
		}
		out = append(out, row)
	}
	return out, info, rows.Err()
}

func (s *Store) PrioritizeTranscriptsByLifecycleWithSource(ctx context.Context, params LifecycleTranscriptPriorityParams) ([]LifecycleTranscriptPriority, *ProfileQueryInfo, error) {
	source, businessProfile, err := s.ResolveLifecycleSource(ctx, params.LifecycleSource)
	if err != nil {
		return nil, nil, err
	}
	info := &ProfileQueryInfo{LifecycleSource: source, Profile: businessProfile}
	if source == LifecycleSourceBuiltin {
		rows, err := s.PrioritizeTranscriptsByLifecycle(ctx, withBuiltinTranscriptPriority(params))
		return rows, info, err
	}
	meta, p, err := s.profileFactsReady(ctx)
	if err != nil {
		return nil, nil, err
	}
	info.UnavailableConcepts = profileUnavailableConcepts(p, "")
	limit := boundedLimit(params.Limit, defaultLifecycleLimit, maxLifecycleLimit)
	bucket := strings.TrimSpace(params.Bucket)
	order := lifecycleOrderMap(p)
	priorityExpr := profilePrioritySQL(order)
	where := `WHERE profile_id = ? AND canonical_sha256 = ? AND transcript_present = 0`
	args := []any{meta.ID, meta.CanonicalSHA256}
	if bucket != "" {
		where += ` AND lifecycle_bucket = ?`
		args = append(args, bucket)
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, `
SELECT call_id, title, started_at, duration_seconds, system, direction, scope, lifecycle_bucket, lifecycle_confidence, lifecycle_reason, evidence_fields_json, `+priorityExpr+` AS priority_score
  FROM profile_call_fact_cache
`+where+`
 ORDER BY priority_score DESC, started_at DESC, call_id
 LIMIT ?`, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	out := make([]LifecycleTranscriptPriority, 0)
	for rows.Next() {
		var row LifecycleTranscriptPriority
		var evidenceJSON []byte
		if err := rows.Scan(&row.CallID, &row.Title, &row.StartedAt, &row.DurationSeconds, &row.System, &row.Direction, &row.Scope, &row.Bucket, &row.Confidence, &row.Reason, &evidenceJSON, &row.PriorityScore); err != nil {
			return nil, nil, err
		}
		if len(evidenceJSON) > 0 {
			if err := json.Unmarshal(evidenceJSON, &row.EvidenceFields); err != nil {
				return nil, nil, err
			}
		}
		out = append(out, row)
	}
	return out, info, rows.Err()
}

func (s *Store) profileInventoryObjects(ctx context.Context) ([]profilepkg.ObjectInventory, error) {
	byType := map[string]*profilepkg.ObjectInventory{}
	rows, err := s.db.QueryContext(ctx, `
SELECT object_type,
       COUNT(DISTINCT object_key) AS object_count,
       COUNT(DISTINCT call_id) AS call_count
  FROM call_context_objects
 GROUP BY object_type
 ORDER BY object_type`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var row profilepkg.ObjectInventory
		if err := rows.Scan(&row.ObjectType, &row.ObjectCount, &row.CallCount); err != nil {
			return nil, err
		}
		cp := row
		byType[row.ObjectType] = &cp
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	schemaRows, err := s.db.QueryContext(ctx, `
SELECT object_type
  FROM crm_schema_objects
 UNION
SELECT object_type
  FROM crm_schema_fields
 ORDER BY object_type`)
	if err != nil {
		return nil, err
	}
	defer schemaRows.Close()
	for schemaRows.Next() {
		var objectType string
		if err := schemaRows.Scan(&objectType); err != nil {
			return nil, err
		}
		if strings.TrimSpace(objectType) == "" {
			continue
		}
		if _, ok := byType[objectType]; !ok {
			byType[objectType] = &profilepkg.ObjectInventory{ObjectType: objectType}
		}
	}
	if err := schemaRows.Err(); err != nil {
		return nil, err
	}
	out := make([]profilepkg.ObjectInventory, 0, len(byType))
	for _, row := range byType {
		out = append(out, *row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ObjectType < out[j].ObjectType })
	return out, nil
}

func (s *Store) profileInventoryFields(ctx context.Context) ([]profilepkg.FieldInventory, error) {
	byField := map[string]*profilepkg.FieldInventory{}
	rows, err := s.db.QueryContext(ctx, `
SELECT o.object_type,
       f.field_name,
       MAX(f.field_label) AS field_label,
       MAX(f.field_type) AS field_type,
       COUNT(DISTINCT o.object_key) AS object_count,
       COUNT(DISTINCT CASE WHEN TRIM(f.field_value_text) <> '' THEN o.object_key END) AS populated_count
  FROM call_context_fields f
  JOIN call_context_objects o
    ON o.call_id = f.call_id
   AND o.object_key = f.object_key
 GROUP BY o.object_type, f.field_name
 ORDER BY o.object_type, f.field_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var row profilepkg.FieldInventory
		if err := rows.Scan(&row.ObjectType, &row.FieldName, &row.FieldLabel, &row.FieldType, &row.ObjectCount, &row.PopulatedCount); err != nil {
			return nil, err
		}
		cp := row
		byField[row.ObjectType+"."+row.FieldName] = &cp
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	schemaRows, err := s.db.QueryContext(ctx, `
SELECT object_type,
       field_name,
       MAX(field_label) AS field_label,
       MAX(field_type) AS field_type
  FROM crm_schema_fields
 GROUP BY object_type, field_name
 ORDER BY object_type, field_name`)
	if err != nil {
		return nil, err
	}
	for schemaRows.Next() {
		var objectType, fieldName, fieldLabel, fieldType string
		if err := schemaRows.Scan(&objectType, &fieldName, &fieldLabel, &fieldType); err != nil {
			_ = schemaRows.Close()
			return nil, err
		}
		if strings.TrimSpace(objectType) == "" || strings.TrimSpace(fieldName) == "" {
			continue
		}
		key := objectType + "." + fieldName
		if existing, ok := byField[key]; ok {
			if existing.FieldLabel == "" {
				existing.FieldLabel = fieldLabel
			}
			if existing.FieldType == "" {
				existing.FieldType = fieldType
			}
			continue
		}
		byField[key] = &profilepkg.FieldInventory{
			ObjectType: objectType,
			FieldName:  fieldName,
			FieldLabel: fieldLabel,
			FieldType:  fieldType,
		}
	}
	if err := schemaRows.Err(); err != nil {
		_ = schemaRows.Close()
		return nil, err
	}
	if err := schemaRows.Close(); err != nil {
		return nil, err
	}
	out := make([]profilepkg.FieldInventory, 0, len(byField))
	for _, row := range byField {
		out = append(out, *row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ObjectType != out[j].ObjectType {
			return out[i].ObjectType < out[j].ObjectType
		}
		return out[i].FieldName < out[j].FieldName
	})
	for idx := range out {
		if out[idx].ObjectCount == 0 {
			continue
		}
		values, err := s.distinctFieldValues(ctx, out[idx].ObjectType, out[idx].FieldName, 50)
		if err != nil {
			return nil, err
		}
		out[idx].DistinctValues = values
	}
	return out, nil
}

func (s *Store) distinctFieldValues(ctx context.Context, objectType string, fieldName string, limit int) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT DISTINCT TRIM(f.field_value_text)
  FROM call_context_fields f
  JOIN call_context_objects o
    ON o.call_id = f.call_id
   AND o.object_key = f.object_key
 WHERE o.object_type = ?
   AND f.field_name = ?
   AND TRIM(f.field_value_text) <> ''
 ORDER BY 1
 LIMIT ?`, objectType, fieldName, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

type profileMetaRecord struct {
	ID              int64
	Name            string
	Version         int
	SourcePath      string
	SourceSHA256    string
	CanonicalSHA256 string
	ImportedAt      string
	ImportedBy      string
	IsActive        bool
	CanonicalJSON   []byte
}

func (s *Store) activeProfile(ctx context.Context) (*profileMetaRecord, *profilepkg.Profile, []profilepkg.Finding, error) {
	var meta profileMetaRecord
	var isActive int
	if err := s.db.QueryRowContext(ctx, `
SELECT id, name, version, source_path, source_sha256, canonical_sha256, imported_at, imported_by, is_active, canonical_json
  FROM profile_meta
 WHERE is_active = 1
 ORDER BY id DESC
 LIMIT 1`).Scan(
		&meta.ID,
		&meta.Name,
		&meta.Version,
		&meta.SourcePath,
		&meta.SourceSHA256,
		&meta.CanonicalSHA256,
		&meta.ImportedAt,
		&meta.ImportedBy,
		&isActive,
		&meta.CanonicalJSON,
	); err != nil {
		return nil, nil, nil, err
	}
	meta.IsActive = isActive == 1
	var p profilepkg.Profile
	if err := json.Unmarshal(meta.CanonicalJSON, &p); err != nil {
		return nil, nil, nil, err
	}
	warnings, err := s.profileWarnings(ctx, meta.ID)
	if err != nil {
		return nil, nil, nil, err
	}
	return &meta, &p, warnings, nil
}

func (s *Store) profileWarnings(ctx context.Context, profileID int64) ([]profilepkg.Finding, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT severity, code, message, path
  FROM profile_validation_warning
 WHERE profile_id = ?
 ORDER BY CASE severity WHEN 'error' THEN 1 WHEN 'warn' THEN 2 ELSE 3 END, path, code`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]profilepkg.Finding, 0)
	for rows.Next() {
		var finding profilepkg.Finding
		if err := rows.Scan(&finding.Severity, &finding.Code, &finding.Message, &finding.Path); err != nil {
			return nil, err
		}
		out = append(out, finding)
	}
	return out, rows.Err()
}

func insertProfileMappings(ctx context.Context, tx *sql.Tx, profileID int64, p *profilepkg.Profile) error {
	for concept, mapping := range p.Objects {
		for _, objectType := range mapping.ObjectTypes {
			if _, err := tx.ExecContext(ctx, `INSERT INTO profile_object_alias(profile_id, concept, object_type) VALUES(?, ?, ?)`, profileID, concept, objectType); err != nil {
				return err
			}
		}
	}
	for concept, mapping := range p.Fields {
		evidence, err := json.Marshal(mapping.Evidence)
		if err != nil {
			return err
		}
		for _, fieldName := range mapping.Names {
			if _, err := tx.ExecContext(ctx, `
INSERT INTO profile_field_concept(profile_id, concept, object_concept, field_name, confidence, evidence_json)
VALUES(?, ?, ?, ?, ?, ?)`, profileID, concept, mapping.Object, fieldName, mapping.Confidence, evidence); err != nil {
				return err
			}
		}
	}
	for bucket, mapping := range p.Lifecycle {
		if len(mapping.Rules) == 0 {
			if _, err := tx.ExecContext(ctx, `
INSERT INTO profile_lifecycle_rule(profile_id, bucket, ordinal, label, description, rule_index, rule_json)
VALUES(?, ?, ?, ?, ?, -1, '{}')`, profileID, bucket, mapping.Order, mapping.Label, mapping.Description); err != nil {
				return err
			}
			continue
		}
		for idx, rule := range mapping.Rules {
			body, err := json.Marshal(rule)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
INSERT INTO profile_lifecycle_rule(profile_id, bucket, ordinal, label, description, rule_index, rule_json)
VALUES(?, ?, ?, ?, ?, ?, ?)`, profileID, bucket, mapping.Order, mapping.Label, mapping.Description, idx, body); err != nil {
				return err
			}
		}
	}
	for concept, mapping := range p.Methodology {
		aliases, err := json.Marshal(mapping.Aliases)
		if err != nil {
			return err
		}
		fields, err := json.Marshal(mapping.Fields)
		if err != nil {
			return err
		}
		trackerIDs, err := json.Marshal(mapping.TrackerIDs)
		if err != nil {
			return err
		}
		scorecardQuestionIDs, err := json.Marshal(mapping.ScorecardQuestionIDs)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO profile_methodology_concept(profile_id, concept, description, aliases_json, fields_json, tracker_ids_json, scorecard_question_ids_json)
VALUES(?, ?, ?, ?, ?, ?, ?)`, profileID, concept, mapping.Description, aliases, fields, trackerIDs, scorecardQuestionIDs); err != nil {
			return err
		}
	}
	return nil
}

func replaceProfileWarnings(ctx context.Context, tx *sql.Tx, profileID int64, findings []profilepkg.Finding) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM profile_validation_warning WHERE profile_id = ?`, profileID); err != nil {
		return err
	}
	seen := map[string]struct{}{}
	for _, finding := range findings {
		key := finding.Severity + "\x00" + finding.Code + "\x00" + finding.Path
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO profile_validation_warning(profile_id, severity, code, message, path)
VALUES(?, ?, ?, ?, ?)`, profileID, finding.Severity, finding.Code, finding.Message, finding.Path); err != nil {
			return err
		}
	}
	return nil
}

func invalidateProfileCallFactCacheTx(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM profile_call_fact_cache_meta`); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM profile_call_fact_cache`)
	return err
}

func businessProfileFrom(meta *profileMetaRecord, p *profilepkg.Profile, warnings []profilepkg.Finding) *BusinessProfile {
	if meta == nil || p == nil {
		return nil
	}
	out := &BusinessProfile{
		ProfileID:           meta.ID,
		Name:                meta.Name,
		Version:             meta.Version,
		SourcePath:          meta.SourcePath,
		SourceSHA256:        meta.SourceSHA256,
		CanonicalSHA256:     meta.CanonicalSHA256,
		ImportedAt:          meta.ImportedAt,
		ImportedBy:          meta.ImportedBy,
		IsActive:            meta.IsActive,
		LifecycleCore:       append([]string(nil), profilepkg.RequiredLifecycleBuckets...),
		Warnings:            warnings,
		UnavailableConcepts: profileUnavailableConcepts(p, ""),
	}
	for concept, mapping := range p.Objects {
		out.ObjectConcepts = append(out.ObjectConcepts, BusinessConcept{Kind: "object", Name: concept, ObjectTypes: append([]string(nil), mapping.ObjectTypes...)})
	}
	for concept, mapping := range p.Fields {
		out.FieldConcepts = append(out.FieldConcepts, BusinessConcept{Kind: "field", Name: concept, Object: mapping.Object, FieldNames: append([]string(nil), mapping.Names...)})
	}
	for bucket, mapping := range p.Lifecycle {
		out.LifecycleBuckets = append(out.LifecycleBuckets, BusinessConcept{Kind: "lifecycle", Name: bucket, Label: mapping.Label, Description: mapping.Description})
	}
	for concept, mapping := range p.Methodology {
		out.MethodologyConcepts = append(out.MethodologyConcepts, BusinessConcept{Kind: "methodology", Name: concept, Description: mapping.Description, Aliases: append([]string(nil), mapping.Aliases...)})
	}
	sortBusinessConcepts(out.ObjectConcepts)
	sortBusinessConcepts(out.FieldConcepts)
	sortBusinessConcepts(out.LifecycleBuckets)
	sortBusinessConcepts(out.MethodologyConcepts)
	return out
}

func sortBusinessConcepts(items []BusinessConcept) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Kind != items[j].Kind {
			return items[i].Kind < items[j].Kind
		}
		return items[i].Name < items[j].Name
	})
}

func mappedProfileFields(p *profilepkg.Profile) map[string]struct{} {
	out := map[string]struct{}{}
	if p == nil {
		return out
	}
	for _, mapping := range p.Fields {
		objectMapping := p.Objects[mapping.Object]
		for _, objectType := range objectMapping.ObjectTypes {
			for _, fieldName := range mapping.Names {
				out[objectType+"."+fieldName] = struct{}{}
			}
		}
	}
	return out
}

func withBuiltinSource(params CallFactsSummaryParams) CallFactsSummaryParams {
	params.LifecycleSource = LifecycleSourceBuiltin
	return params
}

func withBuiltinLifecycleSearch(params LifecycleCallSearchParams) LifecycleCallSearchParams {
	params.LifecycleSource = LifecycleSourceBuiltin
	return params
}

func withBuiltinTranscriptPriority(params LifecycleTranscriptPriorityParams) LifecycleTranscriptPriorityParams {
	params.LifecycleSource = LifecycleSourceBuiltin
	return params
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
	if err := s.db.QueryRowContext(ctx, `
SELECT canonical_sha256, data_fingerprint
  FROM profile_call_fact_cache_meta
 WHERE profile_id = ?`, meta.ID).Scan(&existingCanonical, &existingFingerprint); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	} else if existingCanonical == meta.CanonicalSHA256 && existingFingerprint == fingerprint {
		return nil
	}
	if s.readOnly {
		return errors.New("profile read model is missing or stale; run gongctl sync status --db PATH or a writable profile-aware CLI command before using profile-aware MCP tools")
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
	if _, err := tx.ExecContext(ctx, `DELETE FROM profile_call_fact_cache WHERE profile_id = ?`, meta.ID); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `
INSERT INTO profile_call_fact_cache(
	profile_id, canonical_sha256, call_id, title, started_at, duration_seconds,
	system, direction, scope, purpose, calendar_event_present, transcript_present,
	lifecycle_bucket, lifecycle_confidence, lifecycle_reason, evidence_fields_json,
	deal_count, account_count, field_values_json
) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
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
			boolToInt(call.CalendarEventPresent),
			boolToInt(call.TranscriptPresent),
			call.LifecycleBucket,
			call.LifecycleConfidence,
			call.LifecycleReason,
			evidence,
			call.DealCount,
			call.AccountCount,
			fieldValues,
		); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO profile_call_fact_cache_meta(profile_id, canonical_sha256, data_fingerprint, built_at, call_count)
VALUES(?, ?, ?, ?, ?)
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
	if err := s.db.QueryRowContext(ctx, `
SELECT call_count
  FROM profile_call_fact_cache_meta
 WHERE profile_id = ?
   AND canonical_sha256 = ?`, profileID, canonicalSHA256).Scan(&expectedCount); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
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
       evidence_fields_json,
       deal_count,
       account_count,
       field_values_json
  FROM profile_call_fact_cache
 WHERE profile_id = ?
   AND canonical_sha256 = ?
 ORDER BY started_at DESC, call_id`, profileID, canonicalSHA256)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]profileCallFact, 0)
	for rows.Next() {
		var row profileCallFact
		var calendar int
		var transcript int
		var evidenceJSON []byte
		var fieldValuesJSON []byte
		if err := rows.Scan(
			&row.CallID,
			&row.Title,
			&row.StartedAt,
			&row.DurationSeconds,
			&row.System,
			&row.Direction,
			&row.Scope,
			&row.Purpose,
			&calendar,
			&transcript,
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
		row.CalendarEventPresent = calendar == 1
		row.TranscriptPresent = transcript == 1
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
	var maxCallUpdated, maxTranscriptUpdated string
	if err := s.db.QueryRowContext(ctx, `
SELECT
	(SELECT COUNT(*) FROM calls),
	COALESCE((SELECT MAX(updated_at) FROM calls), ''),
	(SELECT COUNT(*) FROM call_context_objects),
	(SELECT COUNT(*) FROM call_context_fields),
	(SELECT COUNT(*) FROM transcripts),
	COALESCE((SELECT MAX(updated_at) FROM transcripts), '')`).Scan(
		&callCount,
		&maxCallUpdated,
		&objectCount,
		&fieldCount,
		&transcriptCount,
		&maxTranscriptUpdated,
	); err != nil {
		return "", err
	}
	return fmt.Sprintf("calls:%d:%s|objects:%d|fields:%d|transcripts:%d:%s", callCount, maxCallUpdated, objectCount, fieldCount, transcriptCount, maxTranscriptUpdated), nil
}

func (s *Store) profileBaseCalls(ctx context.Context) ([]profileCallFact, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT c.call_id,
       c.title,
       c.started_at,
       c.duration_seconds,
       COALESCE(json_extract(c.raw_json, '$.metaData.system'), json_extract(c.raw_json, '$.system'), '') AS system,
       COALESCE(json_extract(c.raw_json, '$.metaData.direction'), json_extract(c.raw_json, '$.direction'), '') AS direction,
       COALESCE(NULLIF(TRIM(COALESCE(json_extract(c.raw_json, '$.metaData.scope'), json_extract(c.raw_json, '$.scope'), '')), ''), 'Unknown') AS scope,
       COALESCE(json_extract(c.raw_json, '$.metaData.purpose'), json_extract(c.raw_json, '$.purpose'), '') AS purpose,
       CASE WHEN COALESCE(json_extract(c.raw_json, '$.metaData.calendarEventId'), json_extract(c.raw_json, '$.calendarEventId'), '') <> '' THEN 1 ELSE 0 END AS calendar_event_present,
       CASE WHEN t.call_id IS NULL THEN 0 ELSE 1 END AS transcript_present
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
		var calendar int
		var transcript int
		if err := rows.Scan(&row.CallID, &row.Title, &row.StartedAt, &row.DurationSeconds, &row.System, &row.Direction, &row.Scope, &row.Purpose, &calendar, &transcript); err != nil {
			return nil, err
		}
		row.CalendarEventPresent = calendar == 1
		row.TranscriptPresent = transcript == 1
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
		var callID, key, objectType, fieldName, value string
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
		if strings.TrimSpace(fieldName) != "" {
			obj.Fields[fieldName] = append(obj.Fields[fieldName], value)
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

func (s *Store) summarizeProfileCallFacts(ctx context.Context, params CallFactsSummaryParams) ([]CallFactsSummaryRow, []string, error) {
	meta, p, err := s.profileFactsReady(ctx)
	if err != nil {
		return nil, nil, err
	}
	groupBy, unavailable, err := validateProfileGroupBy(p, params.GroupBy)
	if err != nil {
		return nil, nil, err
	}
	unavailable = mergeUnavailableConcepts(unavailable, profileUnavailableConcepts(p, groupBy))
	rows, err := s.summarizeProfileFactsRowsSQL(ctx, meta, groupBy, params)
	if err != nil {
		return nil, nil, err
	}
	return rows, unavailable, nil
}

func (s *Store) summarizeProfileFactsRowsSQL(ctx context.Context, meta *profileMetaRecord, groupBy string, params CallFactsSummaryParams) ([]CallFactsSummaryRow, error) {
	groupExpr := profileGroupSQL(groupBy)
	where := `WHERE profile_id = ? AND canonical_sha256 = ?`
	args := []any{meta.ID, meta.CanonicalSHA256}
	if value := strings.TrimSpace(params.LifecycleBucket); value != "" {
		where += ` AND lifecycle_bucket = ?`
		args = append(args, value)
	}
	if value := strings.TrimSpace(params.Scope); value != "" {
		if normalized, ok := normalizedScope(value); ok {
			where += ` AND scope = ?`
			args = append(args, normalized)
		}
	}
	if value := strings.TrimSpace(params.System); value != "" {
		where += ` AND system = ?`
		args = append(args, value)
	}
	if value := strings.TrimSpace(params.Direction); value != "" {
		where += ` AND direction = ?`
		args = append(args, value)
	}
	if value := strings.TrimSpace(params.TranscriptStatus); value != "" {
		status, ok := normalizedTranscriptStatus(value)
		if ok {
			where += ` AND transcript_present = ?`
			if status == "present" {
				args = append(args, 1)
			} else {
				args = append(args, 0)
			}
		}
	}
	limit := boundedLimit(params.Limit, defaultCallFactsLimit, maxCallFactsLimit)
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, `
SELECT `+groupExpr+` AS group_value,
       COUNT(*) AS call_count,
       COALESCE(SUM(CASE WHEN transcript_present = 1 THEN 1 ELSE 0 END), 0) AS transcript_count,
       COALESCE(SUM(CASE WHEN transcript_present = 0 THEN 1 ELSE 0 END), 0) AS missing_transcript_count,
       COALESCE(SUM(CASE WHEN deal_count > 0 THEN 1 ELSE 0 END), 0) AS opportunity_call_count,
       COALESCE(SUM(CASE WHEN account_count > 0 THEN 1 ELSE 0 END), 0) AS account_call_count,
       COALESCE(SUM(CASE WHEN scope = 'External' THEN 1 ELSE 0 END), 0) AS external_call_count,
       COALESCE(SUM(CASE WHEN scope = 'Internal' THEN 1 ELSE 0 END), 0) AS internal_call_count,
       COALESCE(SUM(CASE WHEN scope NOT IN ('External', 'Internal') THEN 1 ELSE 0 END), 0) AS unknown_scope_call_count,
       COALESCE(SUM(duration_seconds), 0) AS total_duration_seconds,
       COALESCE(MAX(started_at), '') AS latest_call_at
  FROM profile_call_fact_cache
`+where+`
 GROUP BY group_value
 ORDER BY call_count DESC, group_value
 LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]CallFactsSummaryRow, 0)
	for rows.Next() {
		var row CallFactsSummaryRow
		row.GroupBy = groupBy
		if err := rows.Scan(
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
			&row.LatestCallAt,
		); err != nil {
			return nil, err
		}
		row.TranscriptCoverageRate = rate(row.TranscriptCount, row.CallCount)
		if row.CallCount > 0 {
			row.AvgDurationSeconds = float64(row.TotalDurationSeconds) / float64(row.CallCount)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func profileGroupSQL(groupBy string) string {
	switch groupBy {
	case "lifecycle":
		return `COALESCE(NULLIF(lifecycle_bucket, ''), '<blank>')`
	case "scope":
		return `COALESCE(NULLIF(scope, ''), '<blank>')`
	case "system":
		return `COALESCE(NULLIF(system, ''), '<blank>')`
	case "direction":
		return `COALESCE(NULLIF(direction, ''), '<blank>')`
	case "transcript_status":
		return `CASE WHEN transcript_present = 1 THEN 'present' ELSE 'missing' END`
	case "duration_bucket":
		return `CASE WHEN duration_seconds < 60 THEN 'under_1m' WHEN duration_seconds < 300 THEN '1_5m' WHEN duration_seconds < 900 THEN '5_15m' WHEN duration_seconds < 1800 THEN '15_30m' WHEN duration_seconds < 2700 THEN '30_45m' ELSE '45m_plus' END`
	case "month":
		return `COALESCE(NULLIF(substr(started_at, 1, 7), ''), '<blank>')`
	case "calendar":
		return `CASE WHEN calendar_event_present = 1 THEN 'calendar' ELSE 'no_calendar' END`
	default:
		return `COALESCE(NULLIF(json_extract(field_values_json, '$."` + groupBy + `"[0]'), ''), '<blank>')`
	}
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

func summarizeProfileFactsRows(facts []profileCallFact, groupBy string, lifecycle string, scope string, system string, direction string, transcriptStatus string, limitValue int) []CallFactsSummaryRow {
	limit := boundedLimit(limitValue, defaultCallFactsLimit, maxCallFactsLimit)
	groups := map[string]*CallFactsSummaryRow{}
	for _, fact := range facts {
		if !profileFactMatchesFilters(fact, lifecycle, scope, system, direction, transcriptStatus) {
			continue
		}
		groupValue := profileGroupValue(fact, groupBy)
		row := groups[groupValue]
		if row == nil {
			row = &CallFactsSummaryRow{GroupBy: groupBy, GroupValue: groupValue}
			groups[groupValue] = row
		}
		addFactToSummary(row, fact)
	}
	out := make([]CallFactsSummaryRow, 0, len(groups))
	for _, row := range groups {
		row.TranscriptCoverageRate = rate(row.TranscriptCount, row.CallCount)
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

func coverageFromProfileFacts(facts []profileCallFact) *CallFactsCoverage {
	coverage := &CallFactsCoverage{}
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
	coverage.TranscriptCoverageRate = rate(coverage.TranscriptCount, coverage.TotalCalls)
	return coverage
}

func profileFactMatchesFilters(fact profileCallFact, lifecycle string, scope string, system string, direction string, transcriptStatus string) bool {
	if lifecycle != "" && fact.LifecycleBucket != lifecycle {
		return false
	}
	if scope != "" {
		normalized, ok := normalizedScope(scope)
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
		status, ok := normalizedTranscriptStatus(transcriptStatus)
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

func addFactToSummary(row *CallFactsSummaryRow, fact profileCallFact) {
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

func lifecycleSearchResultFromProfileFact(fact profileCallFact) LifecycleCallSearchResult {
	return LifecycleCallSearchResult{
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
	if fact.DurationSeconds >= 1800 {
		score += 20
	} else if fact.DurationSeconds >= 600 {
		score += 10
	}
	if fact.DealCount > 0 {
		score += 10
	}
	return score
}

func profilePrioritySQL(order map[string]int64) string {
	buckets := make([]string, 0, len(order))
	for bucket := range order {
		buckets = append(buckets, bucket)
	}
	sort.Strings(buckets)
	var b strings.Builder
	b.WriteString("(100 - CASE lifecycle_bucket")
	for _, bucket := range buckets {
		b.WriteString(" WHEN ")
		b.WriteString(sqlQuote(bucket))
		b.WriteString(" THEN ")
		b.WriteString(fmt.Sprintf("%d", order[bucket]))
	}
	b.WriteString(" ELSE 100 END)")
	b.WriteString(" + CASE WHEN lifecycle_bucket IN ('closed_won','closed_lost','post_sales') THEN 50 ELSE 0 END")
	b.WriteString(" + CASE WHEN lifecycle_confidence = 'high' THEN 20 ELSE 0 END")
	b.WriteString(" + CASE WHEN scope = 'External' THEN 25 ELSE 0 END")
	b.WriteString(" + CASE WHEN direction = 'Conference' THEN 20 ELSE 0 END")
	b.WriteString(" + CASE WHEN duration_seconds >= 1800 THEN 20 WHEN duration_seconds >= 600 THEN 10 ELSE 0 END")
	b.WriteString(" + CASE WHEN deal_count > 0 THEN 10 ELSE 0 END")
	b.WriteString(" - CASE WHEN duration_seconds > 0 AND duration_seconds < 300 AND direction <> 'Conference' THEN 20 ELSE 0 END")
	return b.String()
}

func sqlQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
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

func profileUnavailableConcepts(p *profilepkg.Profile, groupBy string) []string {
	if p == nil {
		return nil
	}
	missing := map[string]struct{}{}
	if _, ok := p.Objects["deal"]; !ok {
		missing["deal"] = struct{}{}
	}
	if _, ok := p.Objects["account"]; !ok {
		missing["account"] = struct{}{}
	}
	switch normalizeProfileGroupBy(groupBy) {
	case "", "lifecycle", "scope", "system", "direction", "transcript_status", "duration_bucket", "month", "calendar":
	default:
		if _, ok := p.Fields[normalizeProfileGroupBy(groupBy)]; !ok {
			missing[normalizeProfileGroupBy(groupBy)] = struct{}{}
		}
	}
	out := make([]string, 0, len(missing))
	for concept := range missing {
		out = append(out, concept)
	}
	sort.Strings(out)
	return out
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
