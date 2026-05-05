package postgres

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os/user"
	"sort"
	"strconv"
	"strings"
	"unicode"

	profilepkg "github.com/fyne-coder/gongcli_mcp/internal/profile"
	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
)

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
 ORDER BY object_type, field_name
 LIMIT $1`, maxCRMFieldLimit)
	if err != nil {
		return nil, err
	}
	defer schemaRows.Close()
	for schemaRows.Next() {
		var objectType, fieldName, fieldLabel, fieldType string
		if err := schemaRows.Scan(&objectType, &fieldName, &fieldLabel, &fieldType); err != nil {
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
		if out[idx].ObjectCount == 0 && out[idx].PopulatedCount == 0 {
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
 WHERE o.object_type = $1
   AND f.field_name = $2
   AND TRIM(f.field_value_text) <> ''
 ORDER BY 1
 LIMIT $3`, objectType, fieldName, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

func (s *Store) ImportProfile(ctx context.Context, params sqlite.ProfileImportParams) (*sqlite.ProfileImportResult, error) {
	if err := s.ensureWritable(); err != nil {
		return nil, err
	}
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
	if !bytes.Equal(parsedCanonicalJSON, canonicalJSON) {
		return nil, errors.New("raw profile YAML does not match profile")
	}
	canonicalSHA256 := profilepkg.SourceHash(canonicalJSON)
	if strings.TrimSpace(params.CanonicalSHA256) != "" && params.CanonicalSHA256 != canonicalSHA256 {
		return nil, errors.New("canonical_sha256 does not match canonical profile JSON")
	}
	if len(params.CanonicalJSON) > 0 && !json.Valid(params.CanonicalJSON) {
		return nil, errors.New("canonical_json is not valid JSON")
	}
	if len(params.CanonicalJSON) > 0 && !bytes.Equal(params.CanonicalJSON, canonicalJSON) {
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
	importedBy := strings.TrimSpace(params.ImportedBy)
	if importedBy == "" {
		if current, err := user.Current(); err == nil {
			importedBy = current.Username
		}
	}
	params.Findings = validationFindings
	now := nowUTC()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := lockPostgresWriterTx(ctx, tx); err != nil {
		return nil, err
	}

	existing, err := profileByCanonicalTx(ctx, tx, canonicalSHA256)
	switch {
	case err == nil:
		sourceChanged := existing.SourceSHA256 != sourceSHA256 || existing.SourcePath != params.SourcePath
		activationChanged := !params.StageOnly && !existing.IsActive
		if !sourceChanged && !activationChanged {
			if err := tx.Commit(); err != nil {
				return nil, err
			}
			result := &sqlite.ProfileImportResult{ProfileID: existing.ID, Imported: false, Activated: false, SourceSHA256: sourceSHA256, CanonicalSHA256: canonicalSHA256}
			if !params.StageOnly {
				if err := s.RefreshActiveProfileReadModel(ctx); err != nil {
					return nil, err
				}
			}
			return result, nil
		}
		if !params.StageOnly {
			if _, err := tx.ExecContext(ctx, `UPDATE profile_meta SET is_active = false WHERE id <> $1`, existing.ID); err != nil {
				return nil, err
			}
		}
		if sourceChanged {
			activeValue := existing.IsActive
			if !params.StageOnly {
				activeValue = true
			}
			if _, err := tx.ExecContext(ctx, `
UPDATE profile_meta
   SET source_path = $1, source_sha256 = $2, imported_at = $3, imported_by = $4, raw_yaml = $5, is_active = $6
 WHERE id = $7`,
				params.SourcePath, sourceSHA256, now, importedBy, params.RawYAML, activeValue, existing.ID); err != nil {
				return nil, err
			}
			if err := replaceProfileWarnings(ctx, tx, existing.ID, params.Findings); err != nil {
				return nil, err
			}
		} else if activationChanged {
			if _, err := tx.ExecContext(ctx, `UPDATE profile_meta SET is_active = true WHERE id = $1`, existing.ID); err != nil {
				return nil, err
			}
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		result := &sqlite.ProfileImportResult{ProfileID: existing.ID, Imported: false, Activated: activationChanged, SourceSHA256: sourceSHA256, CanonicalSHA256: canonicalSHA256}
		if !params.StageOnly {
			if err := s.RefreshActiveProfileReadModel(ctx); err != nil {
				return nil, err
			}
		}
		return result, nil
	case errors.Is(err, sql.ErrNoRows):
	default:
		return nil, err
	}

	if !params.StageOnly {
		if _, err := tx.ExecContext(ctx, `UPDATE profile_meta SET is_active = false`); err != nil {
			return nil, err
		}
	}
	var profileID int64
	if err := tx.QueryRowContext(ctx, `
INSERT INTO profile_meta(name, version, source_path, source_sha256, canonical_sha256, imported_at, imported_by, is_active, raw_yaml, canonical_json)
VALUES($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb)
RETURNING id`,
		params.Profile.Name,
		params.Profile.Version,
		params.SourcePath,
		sourceSHA256,
		canonicalSHA256,
		now,
		importedBy,
		!params.StageOnly,
		params.RawYAML,
		string(canonicalJSON),
	).Scan(&profileID); err != nil {
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
	result := &sqlite.ProfileImportResult{ProfileID: profileID, Imported: true, Activated: !params.StageOnly, SourceSHA256: sourceSHA256, CanonicalSHA256: canonicalSHA256}
	if !params.StageOnly {
		if err := s.RefreshActiveProfileReadModel(ctx); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func profileByCanonicalTx(ctx context.Context, tx *sql.Tx, canonicalSHA256 string) (*profileMetaRecord, error) {
	var meta profileMetaRecord
	if err := tx.QueryRowContext(ctx, `
SELECT id, name, version, source_path, source_sha256, canonical_sha256, imported_at, imported_by, is_active
  FROM profile_meta
 WHERE canonical_sha256 = $1`, canonicalSHA256).Scan(&meta.ID, &meta.Name, &meta.Version, &meta.SourcePath, &meta.SourceSHA256, &meta.CanonicalSHA256, &meta.ImportedAt, &meta.ImportedBy, &meta.IsActive); err != nil {
		return nil, err
	}
	return &meta, nil
}

func (s *Store) ListProfiles(ctx context.Context) ([]sqlite.ProfileHistoryEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT p.id, p.name, p.version, p.source_path, p.source_sha256, p.canonical_sha256,
       p.imported_at, p.imported_by, p.is_active, COUNT(w.profile_id)
  FROM profile_meta p
  LEFT JOIN profile_validation_warning w ON w.profile_id = p.id
 GROUP BY p.id, p.name, p.version, p.source_path, p.source_sha256, p.canonical_sha256,
          p.imported_at, p.imported_by, p.is_active
 ORDER BY p.imported_at DESC, p.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []sqlite.ProfileHistoryEntry{}
	for rows.Next() {
		var row sqlite.ProfileHistoryEntry
		if err := rows.Scan(&row.ProfileID, &row.Name, &row.Version, &row.SourcePath, &row.SourceSHA256, &row.CanonicalSHA256, &row.ImportedAt, &row.ImportedBy, &row.IsActive, &row.WarningCount); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) ProfileDocument(ctx context.Context, ref string) (*sqlite.StoredProfileDocument, error) {
	where, args, err := profileRefWhere(ref)
	if err != nil {
		return nil, err
	}
	if isProfileHashPrefixRef(ref) {
		var matches int64
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM profile_meta p WHERE `+where, args...).Scan(&matches); err != nil {
			return nil, err
		}
		if matches > 1 {
			return nil, fmt.Errorf("profile canonical_sha256 prefix %q matches %d profiles; use a longer prefix or profile id", cleanProfileRef(ref), matches)
		}
	}
	query := `
SELECT p.id, p.name, p.version, p.source_path, p.source_sha256, p.canonical_sha256,
       p.imported_at, p.imported_by, p.is_active, p.canonical_json::text, COUNT(w.profile_id)
  FROM profile_meta p
  LEFT JOIN profile_validation_warning w ON w.profile_id = p.id
 WHERE ` + where + `
 GROUP BY p.id, p.name, p.version, p.source_path, p.source_sha256, p.canonical_sha256,
          p.imported_at, p.imported_by, p.is_active, p.canonical_json
 ORDER BY p.id DESC
 LIMIT 1`
	var meta sqlite.ProfileHistoryEntry
	var canonical string
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&meta.ProfileID, &meta.Name, &meta.Version, &meta.SourcePath, &meta.SourceSHA256, &meta.CanonicalSHA256, &meta.ImportedAt, &meta.ImportedBy, &meta.IsActive, &canonical, &meta.WarningCount); err != nil {
		return nil, err
	}
	var p profilepkg.Profile
	if err := json.Unmarshal([]byte(canonical), &p); err != nil {
		return nil, err
	}
	return &sqlite.StoredProfileDocument{Meta: meta, Profile: &p}, nil
}

func (s *Store) ActivateProfile(ctx context.Context, ref string) (*sqlite.ProfileImportResult, error) {
	if err := s.ensureWritable(); err != nil {
		return nil, err
	}
	doc, err := s.ProfileDocument(ctx, ref)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := lockPostgresWriterTx(ctx, tx); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE profile_meta SET is_active = false WHERE id <> $1`, doc.Meta.ProfileID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE profile_meta SET is_active = true WHERE id = $1`, doc.Meta.ProfileID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if err := s.RefreshActiveProfileReadModel(ctx); err != nil {
		return nil, err
	}
	return &sqlite.ProfileImportResult{
		ProfileID:       doc.Meta.ProfileID,
		Imported:        false,
		Activated:       !doc.Meta.IsActive,
		SourceSHA256:    doc.Meta.SourceSHA256,
		CanonicalSHA256: doc.Meta.CanonicalSHA256,
	}, nil
}

func (s *Store) ActiveBusinessProfile(ctx context.Context) (*sqlite.BusinessProfile, error) {
	meta, p, warnings, err := s.activeProfile(ctx)
	if err != nil {
		return nil, err
	}
	return businessProfileFrom(meta, p, warnings), nil
}

func (s *Store) ActiveProfileDocument(ctx context.Context) (*profilepkg.Profile, error) {
	doc, err := s.ProfileDocument(ctx, "active")
	if err != nil {
		return nil, err
	}
	return doc.Profile, nil
}

func (s *Store) ListBusinessConcepts(ctx context.Context) ([]sqlite.BusinessConcept, error) {
	profile, err := s.ActiveBusinessProfile(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]sqlite.BusinessConcept, 0)
	out = append(out, profile.ObjectConcepts...)
	out = append(out, profile.FieldConcepts...)
	out = append(out, profile.LifecycleBuckets...)
	out = append(out, profile.MethodologyConcepts...)
	return out, nil
}

func (s *Store) activeProfile(ctx context.Context) (*profileMetaRecord, *profilepkg.Profile, []profilepkg.Finding, error) {
	if s.readOnly {
		return s.activeProfileViaMCPFunction(ctx)
	}
	var meta profileMetaRecord
	if err := s.db.QueryRowContext(ctx, `
SELECT id, name, version, source_path, source_sha256, canonical_sha256, imported_at, imported_by, is_active, canonical_json::text
  FROM profile_meta
 WHERE is_active = true
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
		&meta.IsActive,
		&meta.CanonicalJSON,
	); err != nil {
		return nil, nil, nil, err
	}
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

func (s *Store) activeProfileViaMCPFunction(ctx context.Context) (*profileMetaRecord, *profilepkg.Profile, []profilepkg.Finding, error) {
	var payload []byte
	functionName := "gongmcp_active_business_profile"
	if s.readOnlyOptions.EnforceAllowedColumnBoundary {
		functionName = "gongmcp_active_business_profile_sanitized"
	}
	if err := s.db.QueryRowContext(ctx, `SELECT profile_json::text FROM `+functionName+`()`).Scan(&payload); err != nil {
		return nil, nil, nil, err
	}
	var decoded struct {
		ProfileID       int64                `json:"profile_id"`
		Name            string               `json:"name"`
		Version         int                  `json:"version"`
		SourcePath      string               `json:"source_path"`
		SourceSHA256    string               `json:"source_sha256"`
		CanonicalSHA256 string               `json:"canonical_sha256"`
		ImportedAt      string               `json:"imported_at"`
		ImportedBy      string               `json:"imported_by"`
		IsActive        bool                 `json:"is_active"`
		CanonicalJSON   json.RawMessage      `json:"canonical_json"`
		Profile         json.RawMessage      `json:"profile"`
		Warnings        []profilepkg.Finding `json:"warnings"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return nil, nil, nil, err
	}
	profileJSON := decoded.Profile
	if len(profileJSON) == 0 {
		profileJSON = decoded.CanonicalJSON
	}
	meta := &profileMetaRecord{
		ID:              decoded.ProfileID,
		Name:            decoded.Name,
		Version:         decoded.Version,
		SourcePath:      decoded.SourcePath,
		SourceSHA256:    decoded.SourceSHA256,
		CanonicalSHA256: decoded.CanonicalSHA256,
		ImportedAt:      decoded.ImportedAt,
		ImportedBy:      decoded.ImportedBy,
		IsActive:        decoded.IsActive,
		CanonicalJSON:   []byte(profileJSON),
	}
	var p profilepkg.Profile
	if err := json.Unmarshal(profileJSON, &p); err != nil {
		return nil, nil, nil, err
	}
	return meta, &p, decoded.Warnings, nil
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

func (s *Store) profileWarnings(ctx context.Context, profileID int64) ([]profilepkg.Finding, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT severity, code, message, path
  FROM profile_validation_warning
 WHERE profile_id = $1
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
			if _, err := tx.ExecContext(ctx, `INSERT INTO profile_object_alias(profile_id, concept, object_type) VALUES($1, $2, $3)`, profileID, concept, objectType); err != nil {
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
VALUES($1, $2, $3, $4, $5, $6::jsonb)`, profileID, concept, mapping.Object, fieldName, mapping.Confidence, string(evidence)); err != nil {
				return err
			}
		}
	}
	for bucket, mapping := range p.Lifecycle {
		if len(mapping.Rules) == 0 {
			if _, err := tx.ExecContext(ctx, `
INSERT INTO profile_lifecycle_rule(profile_id, bucket, ordinal, label, description, rule_index, rule_json)
VALUES($1, $2, $3, $4, $5, -1, '{}'::jsonb)`, profileID, bucket, mapping.Order, mapping.Label, mapping.Description); err != nil {
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
VALUES($1, $2, $3, $4, $5, $6, $7::jsonb)`, profileID, bucket, mapping.Order, mapping.Label, mapping.Description, idx, string(body)); err != nil {
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
VALUES($1, $2, $3, $4::jsonb, $5::jsonb, $6::jsonb, $7::jsonb)`, profileID, concept, mapping.Description, string(aliases), string(fields), string(trackerIDs), string(scorecardQuestionIDs)); err != nil {
			return err
		}
	}
	return nil
}

func replaceProfileWarnings(ctx context.Context, tx *sql.Tx, profileID int64, findings []profilepkg.Finding) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM profile_validation_warning WHERE profile_id = $1`, profileID); err != nil {
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
VALUES($1, $2, $3, $4, $5)`, profileID, finding.Severity, finding.Code, finding.Message, finding.Path); err != nil {
			return err
		}
	}
	return nil
}

func profileRefWhere(ref string) (string, []any, error) {
	trimmed := strings.TrimSpace(ref)
	if trimmed == "" || strings.EqualFold(trimmed, "active") {
		return "p.is_active = $1", []any{true}, nil
	}
	if strings.HasPrefix(strings.ToLower(trimmed), "sha:") {
		prefix := strings.TrimSpace(trimmed[4:])
		if err := validateProfileHashPrefix(prefix); err != nil {
			return "", nil, err
		}
		return "p.canonical_sha256 LIKE $1", []any{strings.ToLower(prefix) + "%"}, nil
	}
	if id, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
		return "p.id = $1", []any{id}, nil
	}
	if err := validateProfileHashPrefix(trimmed); err != nil {
		return "", nil, err
	}
	return "p.canonical_sha256 LIKE $1", []any{strings.ToLower(trimmed) + "%"}, nil
}

func isProfileHashPrefixRef(ref string) bool {
	trimmed := cleanProfileRef(ref)
	if trimmed == "" || strings.EqualFold(trimmed, "active") {
		return false
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(ref)), "sha:") {
		return true
	}
	if _, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
		return false
	}
	return len(trimmed) >= 12
}

func cleanProfileRef(ref string) string {
	trimmed := strings.TrimSpace(ref)
	if strings.HasPrefix(strings.ToLower(trimmed), "sha:") {
		return strings.TrimSpace(trimmed[4:])
	}
	return trimmed
}

func validateProfileHashPrefix(prefix string) error {
	if len(prefix) < 12 || len(prefix) > 64 {
		return fmt.Errorf("profile canonical_sha256 prefix must be 12 to 64 hex characters")
	}
	for _, r := range prefix {
		if !unicode.Is(unicode.ASCII_Hex_Digit, r) {
			return fmt.Errorf("profile canonical_sha256 prefix must contain only hex characters")
		}
	}
	return nil
}

func sortBusinessConcepts(items []sqlite.BusinessConcept) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Kind != items[j].Kind {
			return items[i].Kind < items[j].Kind
		}
		return items[i].Name < items[j].Name
	})
}

func businessProfileFrom(meta *profileMetaRecord, p *profilepkg.Profile, warnings []profilepkg.Finding) *sqlite.BusinessProfile {
	if meta == nil || p == nil {
		return nil
	}
	out := &sqlite.BusinessProfile{
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
		out.ObjectConcepts = append(out.ObjectConcepts, sqlite.BusinessConcept{Kind: "object", Name: concept, ObjectTypes: append([]string(nil), mapping.ObjectTypes...)})
	}
	for concept, mapping := range p.Fields {
		out.FieldConcepts = append(out.FieldConcepts, sqlite.BusinessConcept{Kind: "field", Name: concept, Object: mapping.Object, FieldNames: append([]string(nil), mapping.Names...)})
	}
	for bucket, mapping := range p.Lifecycle {
		out.LifecycleBuckets = append(out.LifecycleBuckets, sqlite.BusinessConcept{Kind: "lifecycle", Name: bucket, Label: mapping.Label, Description: mapping.Description})
	}
	for concept, mapping := range p.Methodology {
		out.MethodologyConcepts = append(out.MethodologyConcepts, sqlite.BusinessConcept{Kind: "methodology", Name: concept, Description: mapping.Description, Aliases: append([]string(nil), mapping.Aliases...)})
	}
	sortBusinessConcepts(out.ObjectConcepts)
	sortBusinessConcepts(out.FieldConcepts)
	sortBusinessConcepts(out.LifecycleBuckets)
	sortBusinessConcepts(out.MethodologyConcepts)
	return out
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

func (s *Store) profileReadiness(ctx context.Context) (sqlite.ProfileReadiness, error) {
	readiness := sqlite.ProfileReadiness{
		Status:      "not_configured",
		Detail:      "No active business profile is imported. Builtin lifecycle buckets are available, but reliable tenant-specific lifecycle separation requires a reviewed profile.",
		CacheStatus: "not_applicable",
		Blocking:    []string{"run gongctl profile discover, review the YAML, then run profile validate and profile import"},
	}
	profile, err := s.ActiveBusinessProfile(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return readiness, nil
	}
	if err != nil {
		return readiness, err
	}
	readiness.Active = true
	readiness.Status = "ready"
	readiness.Detail = "An active business profile is imported and Postgres profile-derived lifecycle facts are available when the profile read model cache is fresh."
	readiness.Name = profile.Name
	readiness.Version = profile.Version
	readiness.CanonicalSHA256 = profile.CanonicalSHA256
	readiness.ObjectConceptCount = len(profile.ObjectConcepts)
	readiness.FieldConceptCount = len(profile.FieldConcepts)
	readiness.LifecycleBucketCount = len(profile.LifecycleBuckets)
	readiness.MethodologyConceptCount = len(profile.MethodologyConcepts)
	readiness.WarningCount = len(profile.Warnings)
	readiness.UnavailableConcepts = profile.UnavailableConcepts
	fingerprint, err := s.profileDataFingerprint(ctx)
	if err != nil {
		return readiness, err
	}
	var canonicalSHA256, dataFingerprint string
	metaQuery := `
SELECT canonical_sha256, data_fingerprint
  FROM profile_call_fact_cache_meta
 WHERE profile_id = $1`
	metaArgs := []any{profile.ProfileID}
	if s.readOnly {
		metaQuery = `SELECT canonical_sha256, data_fingerprint FROM gongmcp_profile_call_fact_cache_meta($1, $2)`
		metaArgs = []any{profile.ProfileID, profile.CanonicalSHA256}
		if s.readOnlyOptions.EnforceAllowedColumnBoundary {
			metaQuery = `SELECT data_fingerprint FROM gongmcp_profile_call_fact_cache_meta_sanitized($1)`
			metaArgs = []any{profile.ProfileID}
		}
	}
	if s.readOnly && s.readOnlyOptions.EnforceAllowedColumnBoundary {
		err = s.db.QueryRowContext(ctx, metaQuery, metaArgs...).Scan(&dataFingerprint)
	} else {
		err = s.db.QueryRowContext(ctx, metaQuery, metaArgs...).Scan(&canonicalSHA256, &dataFingerprint)
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			readiness.Status = "needs_action"
			readiness.Detail = "An active business profile is imported, but the Postgres profile read model cache has not been warmed."
			readiness.CacheStatus = "missing"
			readiness.CacheFresh = false
			readiness.Blocking = []string{"run gongctl sync read-model --rebuild with a writable Postgres URL"}
			return readiness, nil
		}
		return readiness, err
	}
	readiness.CacheFresh = (profile.CanonicalSHA256 == "" || canonicalSHA256 == profile.CanonicalSHA256) && dataFingerprint == fingerprint
	if readiness.CacheFresh {
		readiness.CacheStatus = "fresh"
		readiness.Blocking = nil
		if len(readiness.UnavailableConcepts) > 0 || len(profile.Warnings) > 0 {
			readiness.Status = "partial"
			readiness.Detail = "An active business profile is available, with warnings or unavailable concepts to review."
			return readiness, nil
		}
		readiness.Status = "ready"
		readiness.Detail = "An active reviewed business profile and fresh read model are available for profile-aware analysis."
		return readiness, nil
	}
	readiness.Status = "needs_action"
	readiness.Detail = "An active business profile is imported, but the Postgres profile read model cache is stale."
	readiness.CacheStatus = "stale"
	readiness.Blocking = []string{"run gongctl sync read-model --rebuild with a writable Postgres URL"}
	return readiness, nil
}
