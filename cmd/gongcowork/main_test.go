package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/fyne-coder/gongcli_mcp/internal/coworkbridge"
	"github.com/fyne-coder/gongcli_mcp/internal/mcp"
)

func TestGongcoworkToolsListOnlyWorkflowTool(t *testing.T) {
	env := newMainSyntheticEnv(t)
	var stdout, stderr bytes.Buffer
	stdin := bytes.NewBufferString(
		mcpFrame(mcp.Request{JSONRPC: "2.0", ID: "1", Method: "initialize", Params: json.RawMessage(`{}`)}) +
			mcpFrame(mcp.Request{JSONRPC: "2.0", Method: "notifications/initialized"}) +
			mcpFrame(mcp.Request{JSONRPC: "2.0", ID: "2", Method: "tools/list"}),
	)
	code := run([]string{"--contract", env.contractPath}, stdin, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	frames := readFrames(t, stdout.Bytes())
	if len(frames) != 2 {
		t.Fatalf("frame count=%d want 2 (init+tools/list)", len(frames))
	}
	var listed struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(frames[1], &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Result.Tools) != 1 || listed.Result.Tools[0].Name != "gong_workflow" {
		t.Fatalf("tools=%v want [gong_workflow]", listed.Result.Tools)
	}
}

func TestGongcoworkSelectionToolsListOnlySelectionTool(t *testing.T) {
	env := newMainSyntheticSelectionEnv(t)
	var stdout, stderr bytes.Buffer
	stdin := bytes.NewBufferString(
		mcpFrame(mcp.Request{JSONRPC: "2.0", ID: "1", Method: "initialize", Params: json.RawMessage(`{}`)}) +
			mcpFrame(mcp.Request{JSONRPC: "2.0", Method: "notifications/initialized"}) +
			mcpFrame(mcp.Request{JSONRPC: "2.0", ID: "2", Method: "tools/list"}),
	)
	code := run([]string{"--selection-contract", env.contractPath}, stdin, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	frames := readFrames(t, stdout.Bytes())
	if len(frames) != 2 {
		t.Fatalf("frame count=%d want 2", len(frames))
	}
	var listed struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(frames[1], &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Result.Tools) != 1 || listed.Result.Tools[0].Name != "gong_candidate_selection" {
		t.Fatalf("tools=%v want [gong_candidate_selection]", listed.Result.Tools)
	}
}

func TestGongcoworkMutuallyExclusiveContracts(t *testing.T) {
	env := newMainSyntheticEnv(t)
	sel := newMainSyntheticSelectionEnv(t)
	var stderr bytes.Buffer
	code := run([]string{"--contract", env.contractPath, "--selection-contract", sel.contractPath}, bytes.NewReader(nil), io.Discard, &stderr)
	if code != 2 {
		t.Fatalf("exit=%d want 2 stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "mutually exclusive") {
		t.Fatalf("stderr=%q", stderr.String())
	}
}

func TestGongcoworkRequiresOneContractFlag(t *testing.T) {
	var stderr bytes.Buffer
	code := run(nil, bytes.NewReader(nil), io.Discard, &stderr)
	if code != 2 {
		t.Fatalf("exit=%d want 2", code)
	}
}

func TestGongcoworkSelectionStdioStdinForwarding(t *testing.T) {
	env := newMainSyntheticSelectionEnv(t)
	contract, err := coworkbridge.LoadSelectionContract(env.contractPath)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	stdin := bytes.NewBufferString(
		mcpFrame(mcp.Request{JSONRPC: "2.0", ID: "1", Method: "initialize", Params: json.RawMessage(`{}`)}) +
			mcpFrame(mcp.Request{JSONRPC: "2.0", ID: "2", Method: "tools/call", Params: mustRaw(t, map[string]any{
				"name": "gong_candidate_selection",
				"arguments": map[string]any{
					"operation": "preflight",
				},
			})}) +
			mcpFrame(mcp.Request{JSONRPC: "2.0", ID: "3", Method: "tools/call", Params: mustRaw(t, map[string]any{
				"name": "gong_candidate_selection",
				"arguments": map[string]any{
					"operation": "persist_discovery_response",
					"response":  map[string]any{"discovery": true},
				},
			})}) +
			mcpFrame(mcp.Request{JSONRPC: "2.0", ID: "4", Method: "tools/call", Params: mustRaw(t, map[string]any{
				"name": "gong_candidate_selection",
				"arguments": map[string]any{
					"operation": "persist_query_response",
					"query_id":  "q-1",
					"response":  map[string]any{"hits": []any{}},
				},
			})}),
	)
	code := run([]string{"--selection-contract", env.contractPath}, stdin, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	argvLog, err := os.ReadFile(filepath.Join(env.root, "argv.log"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(argvLog)
	if !strings.Contains(text, "gong_quarterly_review.candidate_selection_workflow") {
		t.Fatalf("missing selection module:\n%s", text)
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "ARGS\t") && (strings.Contains(line, `"discovery"`) || strings.Contains(line, `"hits"`)) {
			t.Fatalf("body leaked into argv: %s", line)
		}
	}
	if !strings.Contains(text, "STDIN\tpersist_discovery_response\t") || !strings.Contains(text, "STDIN\tpersist_query_response\t") {
		t.Fatalf("missing stdin markers:\n%s", text)
	}
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		if !strings.HasPrefix(line, "ARGS\t") {
			continue
		}
		if !strings.HasPrefix(line, "ARGS\t"+contract.PythonInterpreter+"\t-m\tgong_quarterly_review.candidate_selection_workflow\t") {
			t.Fatalf("argv not pinned: %s", line)
		}
	}
}

func TestGongcoworkPreflightAndPersistViaStdio(t *testing.T) {
	env := newMainSyntheticEnv(t)
	contract, err := coworkbridge.LoadContract(env.contractPath)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	stdin := bytes.NewBufferString(
		mcpFrame(mcp.Request{JSONRPC: "2.0", ID: "1", Method: "initialize", Params: json.RawMessage(`{}`)}) +
			mcpFrame(mcp.Request{JSONRPC: "2.0", ID: "2", Method: "tools/call", Params: mustRaw(t, map[string]any{
				"name": "gong_workflow",
				"arguments": map[string]any{
					"operation": "preflight",
				},
			})}) +
			mcpFrame(mcp.Request{JSONRPC: "2.0", ID: "3", Method: "tools/call", Params: mustRaw(t, map[string]any{
				"name": "gong_workflow",
				"arguments": map[string]any{
					"operation": "persist_preflight_response",
					"kind":      "status",
					"response":  map[string]any{"facade_status": "ok"},
				},
			})}) +
			mcpFrame(mcp.Request{JSONRPC: "2.0", ID: "4", Method: "tools/call", Params: mustRaw(t, map[string]any{
				"name": "gong_workflow",
				"arguments": map[string]any{
					"operation": "persist_preflight_response",
					"kind":      "capabilities",
					"response":  map[string]any{"operations": []any{map[string]any{"operation": "evidence.call_drilldown"}}},
				},
			})}) +
			mcpFrame(mcp.Request{JSONRPC: "2.0", ID: "5", Method: "tools/call", Params: mustRaw(t, map[string]any{
				"name": "gong_workflow",
				"arguments": map[string]any{
					"operation":   "issue_pre_drilldown_gate",
					"attested_by": "capture-session:stdio-test",
					"captured_at": "2099-01-01T00:00:00Z",
				},
			})}) +
			mcpFrame(mcp.Request{JSONRPC: "2.0", ID: "6", Method: "tools/call", Params: mustRaw(t, map[string]any{
				"name": "gong_workflow",
				"arguments": map[string]any{
					"operation":   "persist_response",
					"item_id":     "item-1",
					"response":    map[string]any{"hello": "world"},
					"attested_by": "stdio-test",
				},
			})}),
	)
	code := run([]string{"--contract", env.contractPath}, stdin, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	frames := readFrames(t, stdout.Bytes())
	if len(frames) != 6 {
		t.Fatalf("frame count=%d want 6", len(frames))
	}
	rawPath := filepath.Join(env.root, "runs", "demo", "out", "item-1.json")
	body, err := os.ReadFile(rawPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "{\"hello\":\"world\"}\n" {
		t.Fatalf("persisted body=%q", body)
	}
	argvLog, err := os.ReadFile(filepath.Join(env.root, "argv.log"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(argvLog)
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !strings.HasPrefix(line, "ARGS\t"+contract.PythonInterpreter+"\t-m\tgong_quarterly_review.") {
			t.Fatalf("argv line not pinned to fake interpreter/module: %s", line)
		}
	}
	for _, module := range []string{
		"gong_quarterly_review.local_bridge_readiness",
		"gong_quarterly_review.preflight_gate_cli",
		"gong_quarterly_review.response_receipt",
		"gong_quarterly_review.response_adapter",
		"gong_quarterly_review.stage_rehearsal_capture",
	} {
		if !strings.Contains(text, module) {
			t.Fatalf("argv log missing %s:\n%s", module, text)
		}
	}
}

func TestGongcoworkRequiresAbsoluteContract(t *testing.T) {
	var stderr bytes.Buffer
	code := run([]string{"--contract", "relative.json"}, bytes.NewReader(nil), io.Discard, &stderr)
	if code != 2 {
		t.Fatalf("exit=%d want 2", code)
	}
}

func TestGongcoworkRequiresAbsoluteSelectionContract(t *testing.T) {
	var stderr bytes.Buffer
	code := run([]string{"--selection-contract", "relative.json"}, bytes.NewReader(nil), io.Discard, &stderr)
	if code != 2 {
		t.Fatalf("exit=%d want 2", code)
	}
}

type mainEnv struct {
	root         string
	contractPath string
}

func newMainSyntheticEnv(t *testing.T) mainEnv {
	t.Helper()
	root := t.TempDir()
	src := filepath.Join("..", "..", "internal", "coworkbridge", "testdata", "synthetic", "fake-python")
	dst := filepath.Join(root, ".venv-host", "bin", "python")
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, raw, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{
		filepath.Join(root, "src"),
		filepath.Join(root, "runs", "demo", "out"),
		filepath.Join(root, "runs", "demo", "target"),
		filepath.Join(root, "runs", "demo", "scratch"),
		filepath.Join(root, "runs", "demo", "q"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	contractPath := filepath.Join(root, "contract.json")
	doc := map[string]any{
		"schema_version":             "1.0",
		"approved_project_root":      root,
		"python_interpreter":         ".venv-host/bin/python",
		"run_root":                   "runs/demo",
		"quarter_root":               "runs/demo/q",
		"status_response_path":       "runs/demo/q/preflight/status.json",
		"capabilities_response_path": "runs/demo/q/preflight/capabilities.json",
		"pre_drilldown_gate_path":    "runs/demo/q/pre-drilldown-gate.json",
		"quarter_id":                 "2099-q1",
		"version":                    "v1",
		"segment_id":                 "segment-test",
		"contract_model_id":          "claude-haiku-4-5-20251001",
		"cowork_ui_display_name":     "Claude Haiku 4.5",
		"readiness_target_dir":       "runs/demo/target",
		"readiness_scratch_root":     "runs/demo/scratch",
		"finalization_result_path":   "runs/demo/final.json",
		"completion_marker_paths": []any{
			"runs/demo/markers/capture-complete.marker.json",
		},
		"completion_pin_path": "runs/demo/completion.pin.json",
		"items": []any{
			map[string]any{
				"item_id":           "item-1",
				"raw_response_path": "runs/demo/out/item-1.json",
				"staged_input_path": "runs/demo/out/item-1.staged.json",
			},
			map[string]any{
				"item_id":           "item-2",
				"raw_response_path": "runs/demo/out/item-2.json",
				"staged_input_path": "runs/demo/out/item-2.staged.json",
			},
		},
	}
	payload, _ := json.Marshal(doc)
	if err := os.WriteFile(contractPath, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	return mainEnv{root: root, contractPath: contractPath}
}

func newMainSyntheticSelectionEnv(t *testing.T) mainEnv {
	t.Helper()
	root := t.TempDir()
	src := filepath.Join("..", "..", "internal", "coworkbridge", "testdata", "synthetic", "fake-python")
	dst := filepath.Join(root, ".venv-host", "bin", "python")
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, raw, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{
		filepath.Join(root, "src"),
		filepath.Join(root, "selection", "target"),
		filepath.Join(root, "selection", "scratch"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "selection", "config.json"), []byte(`{"schema_version":"1.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	contractPath := filepath.Join(root, "selection-contract.json")
	doc := map[string]any{
		"schema_version":         "1.0",
		"approved_project_root":  root,
		"python_interpreter":     ".venv-host/bin/python",
		"selection_config_path":  "selection/config.json",
		"selection_state_path":   "selection/state.json",
		"selection_output_path":  "selection/output.json",
		"readiness_target_dir":   "selection/target",
		"readiness_scratch_root": "selection/scratch",
		"contract_model_id":      "claude-haiku-4-5-20251001",
		"cowork_ui_display_name": "Claude Haiku 4.5",
	}
	payload, _ := json.Marshal(doc)
	if err := os.WriteFile(contractPath, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	return mainEnv{root: root, contractPath: contractPath}
}

func mcpFrame(req mcp.Request) string {
	payload, err := json.Marshal(req)
	if err != nil {
		panic(err)
	}
	return "Content-Length: " + strconv.Itoa(len(payload)) + "\r\n\r\n" + string(payload)
}

func mustRaw(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func readFrames(t *testing.T, raw []byte) [][]byte {
	t.Helper()
	reader := bufio.NewReader(bytes.NewReader(raw))
	var frames [][]byte
	for {
		payload, err := readContentLengthFrame(reader)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return frames
			}
			t.Fatal(err)
		}
		frames = append(frames, payload)
	}
}

func readContentLengthFrame(r *bufio.Reader) ([]byte, error) {
	var contentLength int
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		const prefix = "Content-Length:"
		if strings.HasPrefix(strings.ToLower(line), strings.ToLower(prefix)) {
			n, err := strconv.Atoi(strings.TrimSpace(line[len(prefix):]))
			if err != nil {
				return nil, err
			}
			contentLength = n
		}
	}
	if contentLength <= 0 {
		return nil, io.EOF
	}
	payload := make([]byte, contentLength)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	return payload, nil
}
