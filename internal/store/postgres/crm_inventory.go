package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
)

const postgresCRMInventoryMigrationSQL = `
CREATE TABLE IF NOT EXISTS crm_integrations (
	integration_id TEXT PRIMARY KEY,
	name TEXT NOT NULL DEFAULT '',
	provider TEXT NOT NULL DEFAULT '',
	raw_json JSONB NOT NULL,
	raw_sha256 TEXT NOT NULL,
	first_seen_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_pg_crm_integrations_provider_name
	ON crm_integrations(provider, name, integration_id);

CREATE TABLE IF NOT EXISTS crm_schema_objects (
	integration_id TEXT NOT NULL,
	object_type TEXT NOT NULL,
	display_name TEXT NOT NULL DEFAULT '',
	field_count BIGINT NOT NULL DEFAULT 0,
	raw_json JSONB NOT NULL,
	raw_sha256 TEXT NOT NULL,
	first_seen_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	PRIMARY KEY(integration_id, object_type)
);

CREATE INDEX IF NOT EXISTS idx_pg_crm_schema_objects_type
	ON crm_schema_objects(object_type);

CREATE TABLE IF NOT EXISTS crm_schema_fields (
	integration_id TEXT NOT NULL,
	object_type TEXT NOT NULL,
	field_name TEXT NOT NULL,
	field_label TEXT NOT NULL DEFAULT '',
	field_type TEXT NOT NULL DEFAULT '',
	raw_json JSONB NOT NULL,
	raw_sha256 TEXT NOT NULL,
	first_seen_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	PRIMARY KEY(integration_id, object_type, field_name)
);

CREATE INDEX IF NOT EXISTS idx_pg_crm_schema_fields_object_name
	ON crm_schema_fields(object_type, field_name);
`

const postgresCRMInventoryReaderGrantStatementsSQL = `
			GRANT SELECT (integration_id, name, provider, first_seen_at, updated_at) ON crm_integrations TO gongmcp_reader;
			GRANT SELECT (integration_id, object_type, display_name, field_count, first_seen_at, updated_at) ON crm_schema_objects TO gongmcp_reader;
			GRANT SELECT (integration_id, object_type, field_name, field_label, field_type, first_seen_at, updated_at) ON crm_schema_fields TO gongmcp_reader;
`

const postgresCRMInventoryReaderGrantsSQL = `
DO $$
BEGIN
	IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'gongmcp_reader') THEN
` + postgresCRMInventoryReaderGrantStatementsSQL + `
	END IF;
END;
$$;
`

const postgresCRMFieldValueSearchFunctionSQL = `
CREATE OR REPLACE FUNCTION gongmcp_crm_field_value_search(object_type_arg text, field_name_arg text, value_query_arg text, row_limit integer, include_call_ids_arg boolean, include_value_snippets_arg boolean)
RETURNS TABLE(
	call_id text,
	title text,
	started_at text,
	object_type text,
	field_name text,
	field_label text,
	value_snippet text
)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = public, pg_temp
AS $function$
SELECT CASE WHEN COALESCE(include_call_ids_arg, false) THEN c.call_id ELSE ''::text END AS call_id,
       CASE WHEN COALESCE(include_value_snippets_arg, false) THEN c.title ELSE ''::text END AS title,
       c.started_at,
       o.object_type,
       f.field_name,
       f.field_label,
       CASE WHEN COALESCE(include_value_snippets_arg, false) THEN LEFT(TRIM(f.field_value_text), 240) ELSE ''::text END AS value_snippet
  FROM call_context_fields f
  JOIN call_context_objects o
    ON o.call_id = f.call_id
   AND o.object_key = f.object_key
  JOIN calls c
    ON c.call_id = f.call_id
 WHERE o.object_type = object_type_arg
   AND f.field_name = field_name_arg
   AND LOWER(f.field_value_text) LIKE '%' || LOWER(value_query_arg) || '%'
 ORDER BY c.started_at DESC, c.call_id
 LIMIT LEAST(GREATEST(COALESCE(row_limit, 20), 1), 1000)
$function$;

REVOKE ALL ON FUNCTION gongmcp_crm_field_value_search(text, text, text, integer, boolean, boolean) FROM PUBLIC;
`

type postgresCRMIntegrationPayload struct {
	IntegrationID string
	Name          string
	Provider      string
	RawJSON       []byte
	RawSHA256     string
}

type postgresCRMSchemaFieldPayload struct {
	FieldName  string
	FieldLabel string
	FieldType  string
	RawJSON    []byte
	RawSHA256  string
}

func (s *Store) ListCRMObjectTypes(ctx context.Context) ([]sqlite.CRMObjectTypeSummary, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT o.object_type,
       COUNT(DISTINCT o.id) AS object_count,
       COUNT(DISTINCT o.call_id) AS call_count,
       COUNT(f.id) AS field_count,
       COUNT(CASE WHEN f.field_populated THEN 1 END) AS populated_field_count,
       COUNT(DISTINCT NULLIF(TRIM(o.object_id), '')) AS distinct_object_id_count
  FROM call_context_objects o
  LEFT JOIN gongmcp_call_context_fields f
    ON f.call_id = o.call_id
   AND f.object_key = o.object_key
 GROUP BY o.object_type
 ORDER BY object_count DESC, o.object_type`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []sqlite.CRMObjectTypeSummary{}
	for rows.Next() {
		var row sqlite.CRMObjectTypeSummary
		if err := rows.Scan(
			&row.ObjectType,
			&row.ObjectCount,
			&row.CallCount,
			&row.FieldCount,
			&row.PopulatedFieldCount,
			&row.DistinctObjectIDCount,
		); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) ListCRMFields(ctx context.Context, objectType string, limit int) ([]sqlite.CRMFieldSummary, error) {
	objectType = strings.TrimSpace(objectType)
	if objectType == "" {
		return nil, errors.New("object type is required")
	}
	limit = boundedLimit(limit, defaultCRMFieldLimit, maxCRMFieldLimit)

	query := `
SELECT object_type,
       field_name,
       field_label,
       row_count,
       call_count,
       populated_count,
       distinct_value_count
  FROM (
	SELECT o.object_type,
	       f.field_name,
	       MAX(f.field_label) AS field_label,
	       COUNT(*) AS row_count,
	       COUNT(DISTINCT f.call_id) AS call_count,
	       COUNT(CASE WHEN f.field_value_text <> '' THEN 1 END) AS populated_count,
	       COUNT(DISTINCT NULLIF(TRIM(f.field_value_text), '')) AS distinct_value_count
	  FROM call_context_fields f
	  JOIN call_context_objects o
	    ON o.call_id = f.call_id
	   AND o.object_key = f.object_key
	 WHERE o.object_type = $1
	 GROUP BY o.object_type, f.field_name
	 ORDER BY COUNT(*) DESC, f.field_name
	 LIMIT $2
  ) fields`
	if s.readOnly {
		query = `
SELECT object_type,
       field_name,
       field_label,
       row_count,
       call_count,
       populated_count,
       distinct_value_count
  FROM (
	SELECT o.object_type,
	       f.field_name,
	       MAX(f.field_label) AS field_label,
	       COUNT(*) AS row_count,
	       COUNT(DISTINCT f.call_id) AS call_count,
	       COUNT(CASE WHEN f.field_populated THEN 1 END) AS populated_count,
	       0::bigint AS distinct_value_count
	  FROM gongmcp_call_context_fields f
	  JOIN call_context_objects o
	    ON o.call_id = f.call_id
	   AND o.object_key = f.object_key
	 WHERE o.object_type = $1
	 GROUP BY o.object_type, f.field_name
	 ORDER BY COUNT(*) DESC, f.field_name
	 LIMIT $2
  ) fields`
	}
	rows, err := s.db.QueryContext(ctx, query, objectType, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []sqlite.CRMFieldSummary{}
	for rows.Next() {
		var row sqlite.CRMFieldSummary
		if err := rows.Scan(
			&row.ObjectType,
			&row.FieldName,
			&row.FieldLabel,
			&row.RowCount,
			&row.CallCount,
			&row.PopulatedCount,
			&row.DistinctValueCount,
		); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) SearchCRMFieldValues(ctx context.Context, params sqlite.CRMFieldValueSearchParams) ([]sqlite.CRMFieldValueMatch, error) {
	objectType := strings.TrimSpace(params.ObjectType)
	fieldName := strings.TrimSpace(params.FieldName)
	valueQuery := strings.TrimSpace(params.ValueQuery)
	if objectType == "" {
		return nil, errors.New("object type is required")
	}
	if fieldName == "" {
		return nil, errors.New("field name is required")
	}
	if valueQuery == "" {
		return nil, errors.New("value query is required")
	}
	limit := boundedLimit(params.Limit, defaultCRMFieldValueLimit, maxCRMFieldValueLimit)

	rows, err := s.db.QueryContext(ctx, `
SELECT call_id,
       title,
       started_at,
       object_type,
       field_name,
       field_label,
       value_snippet
  FROM gongmcp_crm_field_value_search($1, $2, $3, $4, $5, $6)`, objectType, fieldName, valueQuery, limit, params.IncludeCallIDs, params.IncludeValueSnippet)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []sqlite.CRMFieldValueMatch
	for rows.Next() {
		var row sqlite.CRMFieldValueMatch
		if err := rows.Scan(
			&row.CallID,
			&row.Title,
			&row.StartedAt,
			&row.ObjectType,
			&row.FieldName,
			&row.FieldLabel,
			&row.ValueSnippet,
		); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) UpsertCRMIntegration(ctx context.Context, raw json.RawMessage) (*sqlite.CRMIntegrationRecord, error) {
	if err := s.ensureWritable(); err != nil {
		return nil, err
	}
	payload, err := decodePostgresCRMIntegration(raw)
	if err != nil {
		return nil, err
	}
	now := nowUTC()
	if _, err := s.db.ExecContext(ctx, `INSERT INTO crm_integrations(
	integration_id, name, provider, raw_json, raw_sha256, first_seen_at, updated_at
) VALUES($1, $2, $3, $4::jsonb, $5, $6, $7)
ON CONFLICT(integration_id) DO UPDATE SET
	name = excluded.name,
	provider = excluded.provider,
	raw_json = excluded.raw_json,
	raw_sha256 = excluded.raw_sha256,
	updated_at = excluded.updated_at
WHERE
	crm_integrations.name IS DISTINCT FROM excluded.name OR
	crm_integrations.provider IS DISTINCT FROM excluded.provider OR
	crm_integrations.raw_sha256 IS DISTINCT FROM excluded.raw_sha256`,
		payload.IntegrationID,
		payload.Name,
		payload.Provider,
		string(payload.RawJSON),
		payload.RawSHA256,
		now,
		now,
	); err != nil {
		return nil, err
	}
	return s.crmIntegrationByID(ctx, payload.IntegrationID)
}

func (s *Store) UpsertCRMSchema(ctx context.Context, integrationID string, objectType string, raw json.RawMessage) (int64, error) {
	if err := s.ensureWritable(); err != nil {
		return 0, err
	}
	integrationID = strings.TrimSpace(integrationID)
	objectType = strings.TrimSpace(objectType)
	if integrationID == "" {
		return 0, errors.New("integration id is required")
	}
	if objectType == "" {
		return 0, errors.New("object type is required")
	}

	normalized, err := normalizeJSON(raw)
	if err != nil {
		return 0, err
	}
	var doc map[string]any
	if err := json.Unmarshal(normalized, &doc); err != nil {
		return 0, err
	}
	fields := extractPostgresCRMSchemaFields(doc, objectType)
	displayName := firstString(doc, "displayName", "label", "name")
	if displayName == "" {
		displayName = objectType
	}
	rawSHA256 := sha256Hex(normalized)

	var existingRawSHA256 string
	var existingFieldCount int64
	err = s.db.QueryRowContext(ctx, `
SELECT raw_sha256, field_count
  FROM crm_schema_objects
 WHERE integration_id = $1
   AND object_type = $2`, integrationID, objectType).Scan(&existingRawSHA256, &existingFieldCount)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	if err == nil && existingRawSHA256 == rawSHA256 && existingFieldCount == int64(len(fields)) {
		var actualFieldCount int64
		if err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*)
  FROM crm_schema_fields
 WHERE integration_id = $1
   AND object_type = $2`, integrationID, objectType).Scan(&actualFieldCount); err != nil {
			return 0, err
		}
		if actualFieldCount == int64(len(fields)) {
			return actualFieldCount, nil
		}
	}

	now := nowUTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if err := lockPostgresWriterTx(ctx, tx); err != nil {
		return 0, err
	}

	if _, err := tx.ExecContext(ctx, `INSERT INTO crm_schema_objects(
	integration_id, object_type, display_name, field_count, raw_json, raw_sha256, first_seen_at, updated_at
) VALUES($1, $2, $3, $4, $5::jsonb, $6, $7, $8)
ON CONFLICT(integration_id, object_type) DO UPDATE SET
	display_name = excluded.display_name,
	field_count = excluded.field_count,
	raw_json = excluded.raw_json,
	raw_sha256 = excluded.raw_sha256,
	updated_at = excluded.updated_at
WHERE
	crm_schema_objects.display_name IS DISTINCT FROM excluded.display_name OR
	crm_schema_objects.field_count IS DISTINCT FROM excluded.field_count OR
	crm_schema_objects.raw_sha256 IS DISTINCT FROM excluded.raw_sha256`,
		integrationID,
		objectType,
		displayName,
		int64(len(fields)),
		string(normalized),
		rawSHA256,
		now,
		now,
	); err != nil {
		return 0, err
	}

	for _, field := range fields {
		if _, err := tx.ExecContext(ctx, `INSERT INTO crm_schema_fields(
	integration_id, object_type, field_name, field_label, field_type, raw_json, raw_sha256, first_seen_at, updated_at
) VALUES($1, $2, $3, $4, $5, $6::jsonb, $7, $8, $9)
ON CONFLICT(integration_id, object_type, field_name) DO UPDATE SET
	field_label = excluded.field_label,
	field_type = excluded.field_type,
	raw_json = excluded.raw_json,
	raw_sha256 = excluded.raw_sha256,
	updated_at = excluded.updated_at
WHERE
	crm_schema_fields.field_label IS DISTINCT FROM excluded.field_label OR
	crm_schema_fields.field_type IS DISTINCT FROM excluded.field_type OR
	crm_schema_fields.raw_sha256 IS DISTINCT FROM excluded.raw_sha256`,
			integrationID,
			objectType,
			field.FieldName,
			field.FieldLabel,
			field.FieldType,
			string(field.RawJSON),
			field.RawSHA256,
			now,
			now,
		); err != nil {
			return 0, err
		}
	}
	if len(fields) == 0 {
		if _, err := tx.ExecContext(ctx, `DELETE FROM crm_schema_fields WHERE integration_id = $1 AND object_type = $2`, integrationID, objectType); err != nil {
			return 0, err
		}
	} else {
		args := []any{integrationID, objectType}
		placeholders := make([]string, 0, len(fields))
		for _, field := range fields {
			args = append(args, field.FieldName)
			placeholders = append(placeholders, fmt.Sprintf("$%d", len(args)))
		}
		query := `DELETE FROM crm_schema_fields WHERE integration_id = $1 AND object_type = $2 AND field_name NOT IN (` + strings.Join(placeholders, ", ") + `)`
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return 0, err
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int64(len(fields)), nil
}

func (s *Store) ListCRMIntegrations(ctx context.Context) ([]sqlite.CRMIntegrationRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT integration_id, name, provider, updated_at
  FROM crm_integrations
 ORDER BY provider, name, integration_id
 LIMIT $1`, maxCRMFieldLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []sqlite.CRMIntegrationRecord{}
	for rows.Next() {
		var row sqlite.CRMIntegrationRecord
		if err := rows.Scan(&row.IntegrationID, &row.Name, &row.Provider, &row.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) ListCRMSchemaObjects(ctx context.Context, integrationID string) ([]sqlite.CRMSchemaObjectRecord, error) {
	integrationID = strings.TrimSpace(integrationID)
	query := `SELECT integration_id, object_type, display_name, field_count, updated_at FROM crm_schema_objects`
	args := []any{}
	if integrationID != "" {
		query += ` WHERE integration_id = $1`
		args = append(args, integrationID)
	}
	query += ` ORDER BY integration_id, object_type`
	query += fmt.Sprintf(` LIMIT $%d`, len(args)+1)
	args = append(args, maxCRMFieldLimit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []sqlite.CRMSchemaObjectRecord{}
	for rows.Next() {
		var row sqlite.CRMSchemaObjectRecord
		if err := rows.Scan(&row.IntegrationID, &row.ObjectType, &row.DisplayName, &row.FieldCount, &row.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) ListCRMSchemaFields(ctx context.Context, params sqlite.CRMSchemaFieldListParams) ([]sqlite.CRMSchemaFieldRecord, error) {
	limit := boundedLimit(params.Limit, defaultCRMFieldLimit, maxCRMFieldLimit)
	where := []string{}
	args := []any{}
	if value := strings.TrimSpace(params.IntegrationID); value != "" {
		args = append(args, value)
		where = append(where, fmt.Sprintf(`integration_id = $%d`, len(args)))
	}
	if value := strings.TrimSpace(params.ObjectType); value != "" {
		args = append(args, value)
		where = append(where, fmt.Sprintf(`object_type = $%d`, len(args)))
	}
	query := `SELECT integration_id, object_type, field_name, field_label, field_type, updated_at FROM crm_schema_fields`
	if len(where) > 0 {
		query += ` WHERE ` + strings.Join(where, ` AND `)
	}
	args = append(args, limit)
	query += fmt.Sprintf(` ORDER BY integration_id, object_type, field_name LIMIT $%d`, len(args))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []sqlite.CRMSchemaFieldRecord{}
	for rows.Next() {
		var row sqlite.CRMSchemaFieldRecord
		if err := rows.Scan(&row.IntegrationID, &row.ObjectType, &row.FieldName, &row.FieldLabel, &row.FieldType, &row.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) crmIntegrationByID(ctx context.Context, integrationID string) (*sqlite.CRMIntegrationRecord, error) {
	var row sqlite.CRMIntegrationRecord
	if err := s.db.QueryRowContext(ctx, `
SELECT integration_id, name, provider, updated_at
  FROM crm_integrations
 WHERE integration_id = $1`, integrationID).Scan(
		&row.IntegrationID,
		&row.Name,
		&row.Provider,
		&row.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &row, nil
}

func decodePostgresCRMIntegration(raw json.RawMessage) (*postgresCRMIntegrationPayload, error) {
	normalized, err := normalizeJSON(raw)
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := json.Unmarshal(normalized, &doc); err != nil {
		return nil, err
	}
	integrationID := firstString(doc, "integrationId", "crmIntegrationId", "id")
	if integrationID == "" {
		return nil, errors.New("CRM integration payload missing integration id")
	}
	return &postgresCRMIntegrationPayload{
		IntegrationID: integrationID,
		Name:          firstString(doc, "name", "displayName", "crmName"),
		Provider:      firstString(doc, "provider", "crmType", "type", "integrationType"),
		RawJSON:       normalized,
		RawSHA256:     sha256Hex(normalized),
	}, nil
}

func extractPostgresCRMSchemaFields(doc map[string]any, objectType string) []postgresCRMSchemaFieldPayload {
	if value, ok := lookupPostgresAnyCase(doc, "objectTypeToSelectedFields"); ok {
		if byObject, ok := value.(map[string]any); ok {
			if selected, ok := lookupPostgresAnyCase(byObject, objectType); ok {
				return uniquePostgresCRMSchemaFields(buildPostgresCRMSchemaFields(selected, ""))
			}
		}
	}

	for _, key := range []string{"fields", "selectedFields", "selectedCrmFields", "crmFields"} {
		if value, ok := lookupPostgresAnyCase(doc, key); ok {
			return uniquePostgresCRMSchemaFields(buildPostgresCRMSchemaFields(value, ""))
		}
	}
	return nil
}

func buildPostgresCRMSchemaFields(value any, fallbackName string) []postgresCRMSchemaFieldPayload {
	switch typed := value.(type) {
	case []any:
		rows := make([]postgresCRMSchemaFieldPayload, 0, len(typed))
		for idx, item := range typed {
			itemMap, ok := item.(map[string]any)
			if !ok {
				continue
			}
			rows = append(rows, buildPostgresCRMSchemaField(itemMap, fallbackName, idx))
		}
		return rows
	case map[string]any:
		if fieldName := firstString(typed, "fieldName", "name", "apiName", "id"); fieldName != "" {
			return []postgresCRMSchemaFieldPayload{buildPostgresCRMSchemaField(typed, fallbackName, 0)}
		}
		rows := make([]postgresCRMSchemaFieldPayload, 0, len(typed))
		idx := 0
		for key, item := range typed {
			if itemMap, ok := item.(map[string]any); ok {
				rows = append(rows, buildPostgresCRMSchemaField(itemMap, key, idx))
			} else {
				rawDoc := map[string]any{"name": key, "value": item}
				rows = append(rows, buildPostgresCRMSchemaField(rawDoc, key, idx))
			}
			idx++
		}
		return rows
	default:
		return nil
	}
}

func buildPostgresCRMSchemaField(doc map[string]any, fallbackName string, index int) postgresCRMSchemaFieldPayload {
	fieldName := firstString(doc, "fieldName", "name", "apiName", "id")
	if fieldName == "" {
		fieldName = strings.TrimSpace(fallbackName)
	}
	if fieldName == "" {
		fieldName = fmt.Sprintf("field_%d", index)
	}
	fieldLabel := firstString(doc, "label", "displayName", "fieldLabel")
	if fieldLabel == "" && fieldName != fallbackName {
		fieldLabel = strings.TrimSpace(fallbackName)
	}
	raw, err := normalizeJSONValue(doc)
	if err != nil {
		raw = []byte(`{}`)
	}
	return postgresCRMSchemaFieldPayload{
		FieldName:  fieldName,
		FieldLabel: fieldLabel,
		FieldType:  firstString(doc, "fieldType", "type", "dataType", "valueType"),
		RawJSON:    raw,
		RawSHA256:  sha256Hex(raw),
	}
}

func uniquePostgresCRMSchemaFields(fields []postgresCRMSchemaFieldPayload) []postgresCRMSchemaFieldPayload {
	out := make([]postgresCRMSchemaFieldPayload, 0, len(fields))
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		name := strings.TrimSpace(field.FieldName)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		field.FieldName = name
		out = append(out, field)
	}
	return out
}

func lookupPostgresAnyCase(doc map[string]any, key string) (any, bool) {
	if doc == nil {
		return nil, false
	}
	if value, ok := doc[key]; ok {
		return value, true
	}
	for existing, value := range doc {
		if strings.EqualFold(existing, key) {
			return value, true
		}
	}
	return nil, false
}
