package coworkbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/fyne-coder/gongcli_mcp/internal/mcp"
)

const ToolName = "gong_workflow"

type workflowArgs struct {
	Operation  string          `json:"operation"`
	ItemID     string          `json:"item_id,omitempty"`
	Response   json.RawMessage `json:"response,omitempty"`
	AttestedBy string          `json:"attested_by,omitempty"`
}

// Tool returns the single custom MCP tool for gongcowork.
func Tool(runner *Runner) mcp.CustomTool {
	return mcp.CustomTool{
		Name:        ToolName,
		Description: "Fail-closed Gong Quarterly Review workflow operations for Claude Cowork. Operations: preflight, persist_response, validate_item, finalize_run, get_run_status.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"operation": map[string]any{
					"type": "string",
					"enum": []any{"preflight", "persist_response", "validate_item", "finalize_run", "get_run_status"},
				},
				"item_id": map[string]any{
					"type":        "string",
					"description": "Frozen capture item id for persist_response and validate_item",
				},
				"response": map[string]any{
					"description": "Exact JSON response value for persist_response; never augmented",
				},
				"attested_by": map[string]any{
					"type":        "string",
					"description": "Capture session attestation string for persist_response",
				},
			},
			"required":             []any{"operation"},
			"additionalProperties": false,
		},
		Handler: func(ctx context.Context, arguments json.RawMessage) (any, error) {
			return Dispatch(ctx, runner, arguments)
		},
	}
}

// Dispatch executes one gong_workflow operation.
func Dispatch(ctx context.Context, runner *Runner, arguments json.RawMessage) (any, error) {
	var args workflowArgs
	if err := decodeStrict(arguments, &args); err != nil {
		return nil, err
	}
	op := strings.TrimSpace(args.Operation)
	switch op {
	case "preflight":
		if err := rejectUnexpectedFields(args, false, false, false); err != nil {
			return nil, err
		}
		return runner.Preflight(ctx)
	case "persist_response":
		if strings.TrimSpace(args.ItemID) == "" {
			return nil, fmt.Errorf("item_id is required for persist_response")
		}
		if len(args.Response) == 0 {
			return nil, fmt.Errorf("response is required for persist_response")
		}
		if strings.TrimSpace(args.AttestedBy) == "" {
			return nil, fmt.Errorf("attested_by is required for persist_response")
		}
		return runner.PersistResponse(ctx, args.ItemID, args.Response, args.AttestedBy)
	case "validate_item":
		if strings.TrimSpace(args.ItemID) == "" {
			return nil, fmt.Errorf("item_id is required for validate_item")
		}
		if len(args.Response) != 0 || strings.TrimSpace(args.AttestedBy) != "" {
			return nil, fmt.Errorf("validate_item rejects response and attested_by fields")
		}
		return runner.ValidateItem(ctx, args.ItemID)
	case "finalize_run":
		if err := rejectUnexpectedFields(args, false, false, false); err != nil {
			return nil, err
		}
		return runner.FinalizeRun(ctx)
	case "get_run_status":
		if err := rejectUnexpectedFields(args, false, false, false); err != nil {
			return nil, err
		}
		return runner.GetRunStatus(ctx)
	default:
		return nil, fmt.Errorf("unknown operation %q", op)
	}
}

func rejectUnexpectedFields(args workflowArgs, allowItem, allowResponse, allowAttested bool) error {
	if !allowItem && strings.TrimSpace(args.ItemID) != "" {
		return fmt.Errorf("item_id is not allowed for this operation")
	}
	if !allowResponse && len(args.Response) != 0 {
		return fmt.Errorf("response is not allowed for this operation")
	}
	if !allowAttested && strings.TrimSpace(args.AttestedBy) != "" {
		return fmt.Errorf("attested_by is not allowed for this operation")
	}
	return nil
}

func decodeStrict(raw json.RawMessage, dst any) error {
	payload := bytesTrimSpace(raw)
	if len(payload) == 0 {
		payload = []byte("{}")
	}
	dec := json.NewDecoder(strings.NewReader(string(payload)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	return nil
}

func bytesTrimSpace(raw json.RawMessage) []byte {
	return []byte(strings.TrimSpace(string(raw)))
}
