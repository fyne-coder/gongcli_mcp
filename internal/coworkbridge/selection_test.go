package coworkbridge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadSelectionContractRejectsEscapesAndCollisions(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mustWriteExecutable(t, filepath.Join(root, ".venv-host", "bin", "python"), "#!/bin/sh\nexit 0\n")
	configPath := filepath.Join(root, "selection", "config.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(`{"schema_version":"1.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	writeContract := func(name string, mutate func(map[string]any)) string {
		t.Helper()
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
		if mutate != nil {
			mutate(doc)
		}
		path := filepath.Join(root, name)
		raw, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	contract, err := LoadSelectionContract(writeContract("ok.json", nil))
	if err != nil {
		t.Fatalf("valid selection contract: %v", err)
	}
	if contract.SelectionConfigPath == "" || len(contract.SelectionConfigSHA256) != 64 {
		t.Fatalf("selection config was not pinned: %+v", contract)
	}

	if _, err := LoadSelectionContract(writeContract("abs.json", func(doc map[string]any) {
		doc["selection_state_path"] = "/tmp/escape"
	})); err == nil {
		t.Fatal("expected absolute child path rejection")
	}
	if _, err := LoadSelectionContract(writeContract("dotdot.json", func(doc map[string]any) {
		doc["selection_output_path"] = "../outside"
	})); err == nil {
		t.Fatal("expected .. rejection")
	}
	if _, err := LoadSelectionContract(writeContract("collision.json", func(doc map[string]any) {
		doc["selection_state_path"] = "selection/config.json"
	})); err == nil {
		t.Fatal("expected state/config collision rejection")
	}
	if _, err := LoadSelectionContract(writeContract("missing-config.json", func(doc map[string]any) {
		doc["selection_config_path"] = "selection/missing.json"
	})); err == nil {
		t.Fatal("expected missing config rejection")
	}
	if _, err := LoadSelectionContract(writeContract("empty-model.json", func(doc map[string]any) {
		doc["contract_model_id"] = "  "
	})); err == nil {
		t.Fatal("expected empty model id rejection")
	}

	outside := t.TempDir()
	link := filepath.Join(root, "link-escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSelectionContract(writeContract("symlink.json", func(doc map[string]any) {
		doc["selection_state_path"] = "link-escape/state.json"
	})); err == nil {
		t.Fatal("expected symlink rejection")
	}

	trailing := writeContract("trailing.json", nil)
	raw, err := os.ReadFile(trailing)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(trailing, append(raw, []byte(`{"schema_version":"evil"}`)...), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSelectionContract(trailing); err == nil {
		t.Fatal("expected trailing JSON rejection")
	}
	if _, err := LoadSelectionContract(writeContract("unknown.json", func(doc map[string]any) {
		doc["items"] = []any{}
	})); err == nil {
		t.Fatal("expected unknown field rejection")
	}
}

func TestSelectionRunnerStdinForwardingAndPinnedPaths(t *testing.T) {
	t.Parallel()
	env := newSyntheticSelectionEnv(t)
	runner := mustNewSelectionRunner(t, env.contract)
	defer runner.Close()
	ctx := context.Background()

	if _, err := runner.SelectionPreflight(ctx); err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if _, err := runner.PersistDiscoveryResponse(ctx, json.RawMessage(`{"discovery":true,"n":1}`)); err != nil {
		t.Fatalf("persist discovery: %v", err)
	}
	if _, err := runner.GetNextQuery(ctx); err != nil {
		t.Fatalf("get next query: %v", err)
	}
	if _, err := runner.PersistQueryResponse(ctx, "q-1", json.RawMessage(`{"hits":[]}`)); err != nil {
		t.Fatalf("persist query: %v", err)
	}
	if _, err := runner.FinalizeSelection(ctx); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if _, err := runner.GetSelectionStatus(ctx); err != nil {
		t.Fatalf("status: %v", err)
	}

	argv := readArgvLog(t, env.root)
	for _, line := range argv {
		if strings.HasPrefix(line, "ARGS\t") && strings.Contains(line, "candidate_selection_workflow") {
			if !strings.Contains(line, "\t--config\t"+env.contract.SelectionConfigPath) ||
				!strings.Contains(line, "\t--state\t"+env.contract.SelectionStatePath) ||
				!strings.Contains(line, "\t--output\t"+env.contract.SelectionOutputPath) {
				t.Fatalf("frozen paths missing from argv: %s", line)
			}
			if strings.Contains(line, `{"discovery"`) || strings.Contains(line, `"hits"`) || strings.Contains(line, "query_id") {
				t.Fatalf("response body leaked into argv: %s", line)
			}
		}
	}
	stdinLines := 0
	for _, line := range argv {
		if strings.HasPrefix(line, "STDIN\t") {
			stdinLines++
		}
	}
	if stdinLines != 2 {
		t.Fatalf("stdin forwarding markers=%d want 2: %v", stdinLines, argv)
	}
	discoveryBody, err := os.ReadFile(env.contract.SelectionStatePath + ".discovery.json")
	if err != nil {
		t.Fatal(err)
	}
	if string(discoveryBody) != "{\"discovery\":true,\"n\":1}\n" {
		t.Fatalf("discovery body=%q", discoveryBody)
	}
	queryBody, err := os.ReadFile(env.contract.SelectionStatePath + ".query.json")
	if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]any
	if err := json.Unmarshal(queryBody, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope["query_id"] != "q-1" {
		t.Fatalf("envelope=%v", envelope)
	}
	// No raw temp body files under approved root from the Go bridge.
	err = filepath.Walk(env.root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		name := info.Name()
		if strings.HasPrefix(name, "tmp") || strings.HasSuffix(name, ".tmp") || strings.Contains(name, "raw-response") {
			t.Fatalf("unexpected temp/raw path created by bridge: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSelectionConfigAndInterpreterDriftRefused(t *testing.T) {
	t.Parallel()
	env := newSyntheticSelectionEnv(t)
	runner := mustNewSelectionRunner(t, env.contract)
	defer runner.Close()

	if err := os.WriteFile(env.contract.SelectionConfigPath, []byte(`{"changed":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.SelectionPreflight(context.Background()); err == nil || !strings.Contains(err.Error(), "changed after startup") {
		t.Fatalf("expected config drift refusal, got %v", err)
	}

	env2 := newSyntheticSelectionEnv(t)
	runner2 := mustNewSelectionRunner(t, env2.contract)
	defer runner2.Close()
	replacement := filepath.Join(t.TempDir(), "python-replacement")
	mustWriteExecutable(t, replacement, "#!/bin/sh\nexit 0\n")
	if err := os.Remove(env2.contract.PythonInterpreter); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(replacement, env2.contract.PythonInterpreter); err != nil {
		t.Fatal(err)
	}
	if _, err := runner2.GetNextQuery(context.Background()); err == nil || !strings.Contains(err.Error(), "target changed") {
		t.Fatalf("expected interpreter drift refusal, got %v", err)
	}
}

func TestSelectionFailureStopsWithoutAdvance(t *testing.T) {
	t.Parallel()
	env := newSyntheticSelectionEnv(t)
	runner := mustNewSelectionRunner(t, env.contract)
	defer runner.Close()
	if err := os.WriteFile(filepath.Join(env.root, "fail-selection"), []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.PersistDiscoveryResponse(context.Background(), json.RawMessage(`{"n":1}`)); err == nil {
		t.Fatal("expected module failure")
	}
	if _, err := os.Stat(env.contract.SelectionStatePath + ".discovery.json"); !os.IsNotExist(err) {
		t.Fatalf("discovery advanced after failure, err=%v", err)
	}
}

func TestSelectionOversizedResponseRejected(t *testing.T) {
	t.Parallel()
	env := newSyntheticSelectionEnv(t)
	env.contract.MaxResponseBytes = 32
	runner := mustNewSelectionRunner(t, env.contract)
	defer runner.Close()
	big := json.RawMessage(`{"pad":"` + strings.Repeat("x", 64) + `"}`)
	if _, err := runner.PersistDiscoveryResponse(context.Background(), big); err == nil {
		t.Fatal("expected oversized discovery rejection")
	}
	if _, err := runner.PersistQueryResponse(context.Background(), "q-1", big); err == nil {
		t.Fatal("expected oversized query rejection")
	}
}

func TestDispatchSelectionAllowlistAndFields(t *testing.T) {
	t.Parallel()
	env := newSyntheticSelectionEnv(t)
	runner := mustNewSelectionRunner(t, env.contract)
	defer runner.Close()
	ctx := context.Background()

	if _, err := DispatchSelection(ctx, runner, json.RawMessage(`{"operation":"shell"}`)); err == nil {
		t.Fatal("expected unknown operation rejection")
	}
	if _, err := DispatchSelection(ctx, runner, json.RawMessage(`{"operation":"preflight","query_id":"x"}`)); err == nil {
		t.Fatal("expected extra query_id rejection")
	}
	if _, err := DispatchSelection(ctx, runner, json.RawMessage(`{"operation":"preflight","path":"/tmp"}`)); err == nil {
		t.Fatal("expected unknown field rejection")
	}
	if _, err := DispatchSelection(ctx, runner, json.RawMessage(`{"operation":"persist_discovery_response"}`)); err == nil {
		t.Fatal("expected missing response rejection")
	}
	if _, err := DispatchSelection(ctx, runner, json.RawMessage(`{"operation":"persist_query_response","response":{}}`)); err == nil {
		t.Fatal("expected missing query_id rejection")
	}
	if _, err := DispatchSelection(ctx, runner, json.RawMessage(`{"operation":"get_next_query","response":{}}`)); err == nil {
		t.Fatal("expected response rejection for get_next_query")
	}
	if _, err := DispatchSelection(ctx, runner, json.RawMessage(`{"operation":"preflight"}{"operation":"evil"}`)); err == nil {
		t.Fatal("expected trailing JSON rejection")
	}
	if _, err := DispatchSelection(ctx, runner, json.RawMessage(`{"operation":"preflight"}`)); err != nil {
		t.Fatalf("preflight dispatch: %v", err)
	}
}

func TestSelectionNotOKVerdictRefused(t *testing.T) {
	t.Parallel()
	env := newSyntheticSelectionEnv(t)
	runner := mustNewSelectionRunner(t, env.contract)
	defer runner.Close()
	if err := os.WriteFile(filepath.Join(env.root, "selection-not-ok"), []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.SelectionPreflight(context.Background()); err == nil {
		t.Fatal("expected ok:false refusal")
	}
}

type syntheticSelectionEnv struct {
	root     string
	contract *ResolvedSelectionContract
}

func newSyntheticSelectionEnv(t *testing.T) syntheticSelectionEnv {
	t.Helper()
	root := t.TempDir()
	fakeSrc := filepath.Join("testdata", "synthetic", "fake-python")
	fakeDst := filepath.Join(root, ".venv-host", "bin", "python")
	mustCopyExecutable(t, fakeSrc, fakeDst)
	for _, dir := range []string{
		filepath.Join(root, "src"),
		filepath.Join(root, "selection", "target"),
		filepath.Join(root, "selection", "scratch"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	configPath := filepath.Join(root, "selection", "config.json")
	if err := os.WriteFile(configPath, []byte(`{"schema_version":"1.0","synthetic":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
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
	contractPath := filepath.Join(root, "selection-contract.json")
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(contractPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	contract, err := LoadSelectionContract(contractPath)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(`{"schema_version":"1.0","synthetic":true}`))
	if contract.SelectionConfigSHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("unexpected pinned sha %s", contract.SelectionConfigSHA256)
	}
	return syntheticSelectionEnv{root: root, contract: contract}
}

func mustNewSelectionRunner(t *testing.T, contract *ResolvedSelectionContract) *SelectionRunner {
	t.Helper()
	runner, err := NewSelectionRunner(contract)
	if err != nil {
		t.Fatal(err)
	}
	return runner
}
