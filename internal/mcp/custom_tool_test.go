package mcp

import (
	"context"
	"encoding/json"
	"testing"
)

func TestCustomToolRegistrationDoesNotChangeDefaultCatalog(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	baseline := NewServer(store, "gongmcp", "test")
	withCustom := NewServerWithOptions(store, "gongmcp", "test", WithCustomTools(CustomTool{
		Name:        "probe_custom_tool",
		Description: "instance-local custom tool",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		Handler: func(ctx context.Context, arguments json.RawMessage) (any, error) {
			return map[string]any{"ok": true}, nil
		},
	}))

	baselineNames := toolNames(baseline)
	wantDefault := ToolCatalogNames()
	if len(baselineNames) != len(wantDefault) {
		t.Fatalf("baseline tool count=%d want %d", len(baselineNames), len(wantDefault))
	}
	for i, name := range wantDefault {
		if baselineNames[i] != name {
			t.Fatalf("baseline tool[%d]=%q want %q", i, baselineNames[i], name)
		}
	}

	customNames := toolNames(withCustom)
	if len(customNames) != len(wantDefault)+1 {
		t.Fatalf("custom server tool count=%d want %d", len(customNames), len(wantDefault)+1)
	}
	if customNames[len(customNames)-1] != "probe_custom_tool" {
		t.Fatalf("custom tool missing from tools/list: %v", customNames)
	}
	for i, name := range wantDefault {
		if customNames[i] != name {
			t.Fatalf("custom server mutated default catalog at [%d]=%q want %q", i, customNames[i], name)
		}
	}

	catalog := ToolCatalogNames()
	for _, name := range catalog {
		if name == "probe_custom_tool" {
			t.Fatalf("custom tool leaked into shared ToolCatalogNames")
		}
	}
}

func TestCustomToolAllowlistOnlyExposesRegisteredTool(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	server := NewServerWithOptions(
		store,
		"gongcowork-test",
		"test",
		WithToolAllowlist([]string{"probe_custom_tool"}),
		WithCustomTools(CustomTool{
			Name:        "probe_custom_tool",
			Description: "only tool",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"ping": map[string]any{"type": "string"},
				},
				"required":             []any{"ping"},
				"additionalProperties": false,
			},
			Handler: func(ctx context.Context, arguments json.RawMessage) (any, error) {
				var args struct {
					Ping string `json:"ping"`
				}
				if err := decodeArgs(arguments, &args); err != nil {
					return nil, err
				}
				return map[string]any{"pong": args.Ping}, nil
			},
		}),
	)

	names := toolNames(server)
	if len(names) != 1 || names[0] != "probe_custom_tool" {
		t.Fatalf("tools/list=%v want [probe_custom_tool]", names)
	}

	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "call",
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name":      "probe_custom_tool",
			"arguments": map[string]any{"ping": "hello"},
		}),
	}))
	if len(responses) != 1 {
		t.Fatalf("response count=%d want 1", len(responses))
	}
	var envelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &envelope); err != nil {
		t.Fatalf("unmarshal tools/call: %v", err)
	}
	if envelope.Result.IsError {
		t.Fatalf("tools/call error: %s", envelope.Result.Content[0].Text)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(envelope.Result.Content[0].Text), &body); err != nil {
		t.Fatalf("unmarshal tool body: %v", err)
	}
	if body["pong"] != "hello" {
		t.Fatalf("pong=%v want hello", body["pong"])
	}
}

func toolNames(server *Server) []string {
	names := make([]string, 0, len(server.tools))
	for _, item := range server.tools {
		names = append(names, item.Name)
	}
	return names
}
