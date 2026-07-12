package coworkbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/fyne-coder/gongcli_mcp/internal/mcp"
)

const ToolName = "gong_workflow"

type workflowArgs struct {
	Operation  string          `json:"operation"`
	Kind       string          `json:"kind,omitempty"`
	ItemID     string          `json:"item_id,omitempty"`
	Response   json.RawMessage `json:"response,omitempty"`
	AttestedBy string          `json:"attested_by,omitempty"`
	CapturedAt string          `json:"captured_at,omitempty"`
}

// Tool returns the single custom MCP tool for gongcowork.
func Tool(runner *Runner) mcp.CustomTool {
	return mcp.CustomTool{
		Name:        ToolName,
		Description: "Fail-closed Gong Quarterly Review workflow operations for Claude Cowork. Operations: preflight, persist_preflight_response, issue_pre_drilldown_gate, persist_response, validate_item, finalize_run, get_run_status.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"operation": map[string]any{
					"type": "string",
					"enum": []any{"preflight", "persist_preflight_response", "issue_pre_drilldown_gate", "persist_response", "validate_item", "finalize_run", "get_run_status"},
				},
				"kind": map[string]any{
					"type": "string", "enum": []any{"status", "capabilities"},
					"description": "Frozen response kind for persist_preflight_response",
				},
				"item_id": map[string]any{
					"type":        "string",
					"description": "Frozen capture item id for persist_response and validate_item",
				},
				"response": map[string]any{
					"description": "Exact JSON response value for a persistence operation; never augmented",
				},
				"attested_by": map[string]any{
					"type":        "string",
					"description": "Capture session attestation string for gate issuance or persist_response",
				},
				"captured_at": map[string]any{
					"type": "string", "description": "UTC capture timestamp for issue_pre_drilldown_gate",
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
		if err := rejectUnexpectedFields(args, false, false, false, false, false); err != nil {
			return nil, err
		}
		return runner.Preflight(ctx)
	case "persist_preflight_response":
		if strings.TrimSpace(args.Kind) == "" {
			return nil, fmt.Errorf("kind is required for persist_preflight_response")
		}
		if len(args.Response) == 0 {
			return nil, fmt.Errorf("response is required for persist_preflight_response")
		}
		if err := rejectUnexpectedFields(args, true, false, true, false, false); err != nil {
			return nil, err
		}
		return runner.PersistPreflightResponse(args.Kind, args.Response)
	case "issue_pre_drilldown_gate":
		if strings.TrimSpace(args.AttestedBy) == "" {
			return nil, fmt.Errorf("attested_by is required for issue_pre_drilldown_gate")
		}
		if strings.TrimSpace(args.CapturedAt) == "" {
			return nil, fmt.Errorf("captured_at is required for issue_pre_drilldown_gate")
		}
		if err := rejectUnexpectedFields(args, false, false, false, true, true); err != nil {
			return nil, err
		}
		return runner.IssuePreDrilldownGate(ctx, args.AttestedBy, args.CapturedAt)
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
		if err := rejectUnexpectedFields(args, false, true, true, true, false); err != nil {
			return nil, err
		}
		return runner.PersistResponse(ctx, args.ItemID, args.Response, args.AttestedBy)
	case "validate_item":
		if strings.TrimSpace(args.ItemID) == "" {
			return nil, fmt.Errorf("item_id is required for validate_item")
		}
		if err := rejectUnexpectedFields(args, false, true, false, false, false); err != nil {
			return nil, err
		}
		return runner.ValidateItem(ctx, args.ItemID)
	case "finalize_run":
		if err := rejectUnexpectedFields(args, false, false, false, false, false); err != nil {
			return nil, err
		}
		return runner.FinalizeRun(ctx)
	case "get_run_status":
		if err := rejectUnexpectedFields(args, false, false, false, false, false); err != nil {
			return nil, err
		}
		return runner.GetRunStatus(ctx)
	default:
		return nil, fmt.Errorf("unknown operation %q", op)
	}
}

func rejectUnexpectedFields(args workflowArgs, allowKind, allowItem, allowResponse, allowAttested, allowCaptured bool) error {
	if !allowKind && strings.TrimSpace(args.Kind) != "" {
		return fmt.Errorf("kind is not allowed for this operation")
	}
	if !allowItem && strings.TrimSpace(args.ItemID) != "" {
		return fmt.Errorf("item_id is not allowed for this operation")
	}
	if !allowResponse && len(args.Response) != 0 {
		return fmt.Errorf("response is not allowed for this operation")
	}
	if !allowAttested && strings.TrimSpace(args.AttestedBy) != "" {
		return fmt.Errorf("attested_by is not allowed for this operation")
	}
	if !allowCaptured && strings.TrimSpace(args.CapturedAt) != "" {
		return fmt.Errorf("captured_at is not allowed for this operation")
	}
	return nil
}

func decodeStrict(raw json.RawMessage, dst any) error {
	payload := bytes.TrimSpace(raw)
	if len(payload) == 0 {
		payload = []byte("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("trailing JSON after arguments document")
		}
		return fmt.Errorf("trailing data after arguments document: %w", err)
	}
	return nil
}
