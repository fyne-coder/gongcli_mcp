package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type facadeGuidedRepairPayload struct {
	Error  string                  `json:"error"`
	Repair facadeGuidedRepairHints `json:"guided_repair"`
}

type facadeGuidedRepairHints struct {
	Issue              string   `json:"issue"`
	SuggestedOperation string   `json:"suggested_operation,omitempty"`
	SuggestedArguments string   `json:"suggested_arguments,omitempty"`
	Guidance           []string `json:"guidance"`
}

func augmentFacadeQueryMissingOperationError(base error) error {
	hints := facadeGuidedRepairHints{
		Issue:              "missing_operation",
		SuggestedOperation: OpQueryCallCount,
		Guidance: []string{
			fmt.Sprintf(`Set "operation" to a registered gong_query operation such as %q`, OpQueryCallCount),
			`Pass operation-specific fields under "arguments"`,
		},
		SuggestedArguments: marshalFacadeDispatchExample(OpQueryCallCount, queryCallCountExampleArgs()),
	}
	return facadeGuidedRepairError(base, hints)
}

func augmentFacadeQueryOperationError(operation string, rawArgs json.RawMessage, err error) error {
	if err == nil || !isQueryCallFilterOperation(operation) {
		return err
	}
	if hints, ok := guidedRepairHintsForQueryOperation(operation, rawArgs, err); ok {
		return facadeGuidedRepairError(err, hints)
	}
	return err
}

func guidedRepairHintsForQueryOperation(operation string, rawArgs json.RawMessage, err error) (facadeGuidedRepairHints, bool) {
	if isFacadeGuidedRepairError(err) {
		return facadeGuidedRepairHints{}, false
	}
	msg := err.Error()
	hints := inspectFacadeQueryArgumentShape(operation, rawArgs)

	switch {
	case strings.Contains(msg, `unknown field "filters"`):
		hints.Issue = "use_filter_dimension_filters_not_filters"
		hints.Guidance = appendUniqueGuidance(hints.Guidance,
			`Replace top-level "filters" with "filter":{"dimension_filters":[...]}`,
			`Use filter.dimension_filters[].values as a string array, for example ["300"]`,
		)
		if operation == OpQueryCalls {
			hints.SuggestedOperation = OpQueryCallCount
			hints.Guidance = appendUniqueGuidance(hints.Guidance,
				fmt.Sprintf(`For a count without call rows, use operation %q instead of %q`, OpQueryCallCount, OpQueryCalls),
			)
		}
	case strings.Contains(msg, `unknown field "dimension_filters"`):
		hints.Issue = "dimension_filters_belongs_under_filter"
		hints.Guidance = appendUniqueGuidance(hints.Guidance,
			`Move "dimension_filters" under "filter": {"filter":{"dimension_filters":[...]}}`,
			`Each entry needs "dimension", "operator", and "values" as a string array`,
		)
	case strings.Contains(msg, `unknown field "value"`):
		hints.Issue = "use_values_array_not_value"
		hints.Guidance = appendUniqueGuidance(hints.Guidance,
			`Use "values" as a string array under each dimension filter entry, for example "values":["300"]`,
			`Do not use a singular "value" field`,
		)
	case strings.Contains(msg, "cannot unmarshal number") && strings.Contains(msg, ".values"):
		hints.Issue = "values_must_be_string_array"
		hints.Guidance = appendUniqueGuidance(hints.Guidance,
			`Wrap numeric filter values in strings, for example "values":["300"] not "values":[300]`,
		)
	case strings.Contains(msg, "cannot unmarshal bool") && strings.Contains(msg, ".values"):
		hints.Issue = "values_must_be_string_array"
		hints.Guidance = appendUniqueGuidance(hints.Guidance,
			`Wrap boolean filter values in strings if needed, for example "values":["true"]`,
		)
	case strings.Contains(msg, "requires at least one selective filter field") && rawArgsMissingFilterKey(rawArgs):
		hints.Issue = "missing_filter_object"
		hints.Guidance = appendUniqueGuidance(hints.Guidance,
			`Add a "filter" object; put dimension filters under filter.dimension_filters`,
			`Each dimension filter needs "dimension", "operator", and "values" as a string array`,
		)
		if hints.SuggestedOperation == "" {
			hints.SuggestedOperation = operation
		}
	default:
		if hints.Issue == "" && len(hints.Guidance) == 0 {
			return facadeGuidedRepairHints{}, false
		}
	}

	if hints.SuggestedOperation == "" {
		hints.SuggestedOperation = firstNonBlank(operation, OpQueryCallCount)
	}
	if hints.SuggestedArguments == "" {
		hints.SuggestedArguments = marshalFacadeDispatchExample(hints.SuggestedOperation, queryOperationExampleArgs(hints.SuggestedOperation))
	}
	if hints.Issue == "" {
		return facadeGuidedRepairHints{}, false
	}
	return hints, true
}

func inspectFacadeQueryArgumentShape(operation string, rawArgs json.RawMessage) facadeGuidedRepairHints {
	fields := looseJSONObject(rawArgs)
	if fields == nil {
		return facadeGuidedRepairHints{}
	}
	hints := facadeGuidedRepairHints{}
	if _, ok := fields["filters"]; ok {
		hints.Issue = "use_filter_dimension_filters_not_filters"
		if operation == OpQueryCalls {
			hints.SuggestedOperation = OpQueryCallCount
		}
	}
	if _, ok := fields["dimension_filters"]; ok {
		if _, hasFilter := fields["filter"]; !hasFilter {
			hints.Issue = "dimension_filters_belongs_under_filter"
		}
	}
	return hints
}

func facadeGuidedRepairError(original error, hints facadeGuidedRepairHints) error {
	payload := facadeGuidedRepairPayload{
		Error:  original.Error(),
		Repair: hints,
	}
	text, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("%s; guided_repair: %s", original.Error(), strings.Join(hints.Guidance, "; "))
	}
	return errors.New(string(text))
}

func isFacadeGuidedRepairError(err error) bool {
	if err == nil {
		return false
	}
	var payload facadeGuidedRepairPayload
	return json.Unmarshal([]byte(err.Error()), &payload) == nil && payload.Repair.Issue != ""
}

func isQueryCallFilterOperation(operation string) bool {
	return operation == OpQueryCallCount || operation == OpQueryCalls || operation == OpQueryDimensionCounts
}

func rawArgsMissingFilterKey(rawArgs json.RawMessage) bool {
	fields := looseJSONObject(rawArgs)
	if fields == nil {
		return true
	}
	_, ok := fields["filter"]
	return !ok
}

func looseJSONObject(raw json.RawMessage) map[string]json.RawMessage {
	payload := bytes.TrimSpace(raw)
	if len(payload) == 0 {
		return map[string]json.RawMessage{}
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		return nil
	}
	return fields
}

func queryCallCountExampleArgs() map[string]any {
	op, ok := FacadeOperationByName(OpQueryCallCount)
	if ok && len(op.Examples) > 0 {
		if ex, ok := op.Examples[0].(map[string]any); ok {
			return ex
		}
	}
	return map[string]any{
		"filter": map[string]any{
			"dimension_filters": []any{
				map[string]any{
					"dimension": "duration_seconds",
					"operator":  "gte",
					"values":    []string{"300"},
				},
			},
		},
	}
}

func queryOperationExampleArgs(operation string) map[string]any {
	op, ok := FacadeOperationByName(operation)
	if ok && len(op.Examples) > 0 {
		if ex, ok := op.Examples[0].(map[string]any); ok {
			return ex
		}
	}
	return queryCallCountExampleArgs()
}

func marshalFacadeDispatchExample(operation string, args map[string]any) string {
	body := map[string]any{
		"operation": operation,
		"arguments": args,
	}
	text, err := json.Marshal(body)
	if err != nil {
		return ""
	}
	return string(text)
}

func appendUniqueGuidance(existing []string, items ...string) []string {
	seen := make(map[string]struct{}, len(existing))
	for _, item := range existing {
		seen[item] = struct{}{}
	}
	for _, item := range items {
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		existing = append(existing, item)
	}
	return existing
}
