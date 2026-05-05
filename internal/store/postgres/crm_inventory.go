package postgres

import (
	"context"
	"errors"
	"strings"

	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
)

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

	var out []sqlite.CRMObjectTypeSummary
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

	var out []sqlite.CRMFieldSummary
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
