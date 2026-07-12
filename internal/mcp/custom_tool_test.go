package mcp

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
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

func TestCustomToolNameCollisionsAreRejected(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	defaultName := ToolCatalogNames()[0]
	called := false
	server := NewServerWithOptions(store, "gongmcp", "test", WithCustomTools(
		CustomTool{
			Name:        defaultName,
			Description: "should be rejected",
			Handler: func(ctx context.Context, arguments json.RawMessage) (any, error) {
				called = true
				return map[string]any{"shadow": true}, nil
			},
		},
		CustomTool{
			Name:        "dup_custom",
			Description: "first",
			Handler: func(ctx context.Context, arguments json.RawMessage) (any, error) {
				return map[string]any{"which": "first"}, nil
			},
		},
		CustomTool{
			Name:        "dup_custom",
			Description: "second",
			Handler: func(ctx context.Context, arguments json.RawMessage) (any, error) {
				called = true
				return map[string]any{"which": "second"}, nil
			},
		},
	))

	names := toolNames(server)
	seen := map[string]int{}
	for _, name := range names {
		seen[name]++
	}
	if seen[defaultName] != 1 {
		t.Fatalf("default tool %q count=%d want 1", defaultName, seen[defaultName])
	}
	if seen["dup_custom"] != 1 {
		t.Fatalf("dup_custom count=%d want 1", seen["dup_custom"])
	}

	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "call-default",
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name":      defaultName,
			"arguments": map[string]any{},
		}),
	}))
	if len(responses) != 1 {
		t.Fatalf("response count=%d", len(responses))
	}
	if called {
		t.Fatal("colliding custom handler shadowed default tool")
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

func TestServeContinuesAfterOversizedContentLength(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	server := NewServerWithOptions(store, "gongmcp", "test", WithMaxFrameBytes(1024))

	oversizeBody := strings.Repeat("x", 1100)
	input := "Content-Length: 1100\r\n\r\n" + oversizeBody +
		requestFrame(Request{JSONRPC: "2.0", ID: "1", Method: "initialize", Params: json.RawMessage(`{}`)})

	responses := runServer(t, server, input)
	if len(responses) < 2 {
		t.Fatalf("response count=%d want at least 2 (oversize error + initialize)", len(responses))
	}
	var first struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(responses[0], &first); err != nil {
		t.Fatal(err)
	}
	if first.Error == nil || !strings.Contains(first.Error.Message, "exceeds maximum") {
		t.Fatalf("first response=%s", responses[0])
	}
	var second struct {
		Result *struct {
			ProtocolVersion string `json:"protocolVersion"`
		} `json:"result"`
	}
	if err := json.Unmarshal(responses[1], &second); err != nil {
		t.Fatal(err)
	}
	if second.Result == nil || second.Result.ProtocolVersion == "" {
		t.Fatalf("initialize response=%s", responses[1])
	}
}

func TestCompanionFrameLimitAllowsLargerThanDefault(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	server := NewServerWithOptions(store, "gongcowork", "test", WithMaxFrameBytes(CompanionMaxFrameBytes))
	if got := server.frameLimit(); got != CompanionMaxFrameBytes {
		t.Fatalf("frameLimit=%d want %d", got, CompanionMaxFrameBytes)
	}

	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{` + strings.Repeat(" ", maxFrameBytes) + `}}`
	if len(body) <= maxFrameBytes {
		t.Fatalf("test body size=%d should exceed default maxFrameBytes", len(body))
	}
	if len(body) > CompanionMaxFrameBytes {
		t.Fatalf("test body size=%d exceeds companion cap", len(body))
	}
	frame := "Content-Length: " + strconv.Itoa(len(body)) + "\r\n\r\n" + body
	responses := runServer(t, server, frame)
	if len(responses) != 1 {
		t.Fatalf("response count=%d want 1 (accepted under companion cap)", len(responses))
	}
}

func toolNames(server *Server) []string {
	names := make([]string, 0, len(server.tools))
	for _, item := range server.tools {
		names = append(names, item.Name)
	}
	return names
}
