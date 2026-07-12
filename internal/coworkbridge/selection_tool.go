package coworkbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/fyne-coder/gongcli_mcp/internal/mcp"
)

const SelectionToolName = "gong_candidate_selection"

type selectionArgs struct {
	Operation string          `json:"operation"`
	QueryID   string          `json:"query_id,omitempty"`
	Response  json.RawMessage `json:"response,omitempty"`
}

// SelectionTool returns the single custom MCP tool for selection-contract mode.
func SelectionTool(runner *SelectionRunner) mcp.CustomTool {
	return mcp.CustomTool{
		Name:        SelectionToolName,
		Description: "Fail-closed Gong Quarterly Review candidate-selection operations for Claude Cowork. Operations: preflight, persist_discovery_response, get_next_query, persist_query_response, finalize_selection, get_selection_status.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"operation": map[string]any{
					"type": "string",
					"enum": []any{
						"preflight",
						"persist_discovery_response",
						"get_next_query",
						"persist_query_response",
						"finalize_selection",
						"get_selection_status",
					},
				},
				"query_id": map[string]any{
					"type":        "string",
					"description": "Frozen query id for persist_query_response",
				},
				"response": map[string]any{
					"description": "Exact JSON response value for a persistence operation; forwarded on stdin without argv or temp files",
				},
			},
			"required":             []any{"operation"},
			"additionalProperties": false,
		},
		Handler: func(ctx context.Context, arguments json.RawMessage) (any, error) {
			return DispatchSelection(ctx, runner, arguments)
		},
	}
}

// DispatchSelection executes one gong_candidate_selection operation.
func DispatchSelection(ctx context.Context, runner *SelectionRunner, arguments json.RawMessage) (any, error) {
	var args selectionArgs
	if err := decodeStrict(arguments, &args); err != nil {
		return nil, err
	}
	op := strings.TrimSpace(args.Operation)
	switch op {
	case "preflight":
		if err := rejectUnexpectedSelectionFields(args, false, false); err != nil {
			return nil, err
		}
		return runner.SelectionPreflight(ctx)
	case "persist_discovery_response":
		if len(args.Response) == 0 {
			return nil, fmt.Errorf("response is required for persist_discovery_response")
		}
		if err := rejectUnexpectedSelectionFields(args, false, true); err != nil {
			return nil, err
		}
		return runner.PersistDiscoveryResponse(ctx, args.Response)
	case "get_next_query":
		if err := rejectUnexpectedSelectionFields(args, false, false); err != nil {
			return nil, err
		}
		return runner.GetNextQuery(ctx)
	case "persist_query_response":
		if strings.TrimSpace(args.QueryID) == "" {
			return nil, fmt.Errorf("query_id is required for persist_query_response")
		}
		if len(args.Response) == 0 {
			return nil, fmt.Errorf("response is required for persist_query_response")
		}
		if err := rejectUnexpectedSelectionFields(args, true, true); err != nil {
			return nil, err
		}
		return runner.PersistQueryResponse(ctx, args.QueryID, args.Response)
	case "finalize_selection":
		if err := rejectUnexpectedSelectionFields(args, false, false); err != nil {
			return nil, err
		}
		return runner.FinalizeSelection(ctx)
	case "get_selection_status":
		if err := rejectUnexpectedSelectionFields(args, false, false); err != nil {
			return nil, err
		}
		return runner.GetSelectionStatus(ctx)
	default:
		return nil, fmt.Errorf("unknown operation %q", op)
	}
}

func rejectUnexpectedSelectionFields(args selectionArgs, allowQueryID, allowResponse bool) error {
	if !allowQueryID && strings.TrimSpace(args.QueryID) != "" {
		return fmt.Errorf("query_id is not allowed for this operation")
	}
	if !allowResponse && len(args.Response) != 0 {
		return fmt.Errorf("response is not allowed for this operation")
	}
	return nil
}
