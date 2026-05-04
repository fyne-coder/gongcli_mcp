package contextmodel

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type ObjectRow struct {
	ObjectKey  string
	ObjectType string
	ObjectID   string
	ObjectName string
	RawJSON    []byte
	Fields     []FieldRow
}

type FieldRow struct {
	FieldName  string
	FieldLabel string
	FieldType  string
	ValueText  string
	RawJSON    []byte
}

func Extract(raw json.RawMessage) ([]ObjectRow, bool, error) {
	normalized, err := normalizeJSON(raw)
	if err != nil {
		return nil, false, err
	}

	var root map[string]any
	if err := json.Unmarshal(normalized, &root); err != nil {
		return nil, false, err
	}

	type candidate struct {
		name  string
		value any
	}

	var candidates []candidate
	for _, key := range []string{"context", "crmContext", "crm", "extendedContext", "crmObjects", "objects"} {
		value, ok := root[key]
		if !ok {
			continue
		}
		candidates = append(candidates, candidate{name: key, value: value})
	}
	if len(candidates) == 0 {
		return nil, false, nil
	}

	var objects []ObjectRow
	for _, candidate := range candidates {
		objects = append(objects, collectContextObjects(candidate.name, candidate.value)...)
	}
	return objects, true, nil
}

func collectContextObjects(defaultType string, value any) []ObjectRow {
	switch typed := value.(type) {
	case []any:
		rows := make([]ObjectRow, 0, len(typed))
		for idx, item := range typed {
			itemMap, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if row, ok := buildContextObject(defaultType, itemMap, idx); ok {
				rows = append(rows, row)
				continue
			}
			rows = append(rows, collectContextObjects(defaultType, itemMap)...)
		}
		return rows
	case map[string]any:
		if row, ok := buildContextObject(defaultType, typed, 0); ok {
			return []ObjectRow{row}
		}
		var rows []ObjectRow
		for _, key := range sortedKeys(typed) {
			child := typed[key]
			rows = append(rows, collectContextObjects(key, child)...)
		}
		return rows
	default:
		return nil
	}
}

func buildContextObject(defaultType string, doc map[string]any, index int) (ObjectRow, bool) {
	fieldsValue, ok := doc["fields"]
	if !ok {
		fieldsValue, ok = doc["properties"]
	}
	if !ok {
		return ObjectRow{}, false
	}

	objectType := firstString(doc, "objectType", "type", "entityType")
	if objectType == "" {
		objectType = defaultType
	}
	objectID := firstString(doc, "id", "objectId", "crmId")
	objectName := firstString(doc, "name", "displayName", "label", "title")
	fields := extractContextFields(fieldsValue)
	if objectName == "" {
		objectName = contextObjectNameFromFields(fields)
	}
	rawJSON, err := normalizeJSONValue(doc)
	if err != nil {
		return ObjectRow{}, false
	}

	return ObjectRow{
		ObjectKey:  contextObjectKey(objectType, objectID, objectName, index),
		ObjectType: objectType,
		ObjectID:   objectID,
		ObjectName: objectName,
		RawJSON:    rawJSON,
		Fields:     fields,
	}, true
}

func contextObjectNameFromFields(fields []FieldRow) string {
	for _, field := range fields {
		if strings.EqualFold(strings.TrimSpace(field.FieldName), "Name") && strings.TrimSpace(field.ValueText) != "" {
			return strings.TrimSpace(field.ValueText)
		}
	}
	for _, field := range fields {
		if strings.EqualFold(strings.TrimSpace(field.FieldLabel), "Name") && strings.TrimSpace(field.ValueText) != "" {
			return strings.TrimSpace(field.ValueText)
		}
	}
	return ""
}

func extractContextFields(value any) []FieldRow {
	switch typed := value.(type) {
	case []any:
		rows := make([]FieldRow, 0, len(typed))
		for idx, item := range typed {
			itemMap, ok := item.(map[string]any)
			if !ok {
				continue
			}
			fieldName := firstString(itemMap, "name", "fieldName", "apiName")
			fieldLabel := firstString(itemMap, "label", "displayName")
			if fieldName == "" {
				fieldName = fieldLabel
			}
			if fieldName == "" {
				fieldName = fmt.Sprintf("field_%d", idx)
			}
			rawJSON, err := normalizeJSONValue(itemMap)
			if err != nil {
				continue
			}
			rows = append(rows, FieldRow{
				FieldName:  fieldName,
				FieldLabel: fieldLabel,
				FieldType:  firstString(itemMap, "type", "valueType"),
				ValueText:  stringifyValue(itemMap["value"]),
				RawJSON:    rawJSON,
			})
		}
		return rows
	case map[string]any:
		rows := make([]FieldRow, 0, len(typed))
		for _, key := range sortedKeys(typed) {
			item := typed[key]
			rawJSON, err := normalizeJSONValue(map[string]any{
				"name":  key,
				"value": item,
			})
			if err != nil {
				continue
			}
			rows = append(rows, FieldRow{
				FieldName: key,
				ValueText: stringifyValue(item),
				RawJSON:   rawJSON,
			})
		}
		return rows
	default:
		return nil
	}
}

func sortedKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func contextObjectKey(objectType string, objectID string, objectName string, index int) string {
	objectType = strings.TrimSpace(objectType)
	switch {
	case objectType != "" && strings.TrimSpace(objectID) != "":
		return objectType + ":" + strings.TrimSpace(objectID)
	case objectType != "" && strings.TrimSpace(objectName) != "":
		return objectType + ":" + strings.TrimSpace(objectName)
	case objectType != "":
		return objectType + ":" + strconv.Itoa(index)
	case strings.TrimSpace(objectID) != "":
		return "object:" + strings.TrimSpace(objectID)
	default:
		return "object:" + strconv.Itoa(index)
	}
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

func stringifyValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(typed), 'f', -1, 64)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case json.Number:
		return typed.String()
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return ""
		}
		return string(encoded)
	}
}
