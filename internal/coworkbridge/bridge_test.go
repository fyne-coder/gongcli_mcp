package coworkbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadContractRejectsEscapesAndDuplicates(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mustWriteExecutable(t, filepath.Join(root, ".venv-host", "bin", "python"), "#!/bin/sh\nexit 0\n")

	writeContract := func(name string, mutate func(map[string]any)) string {
		t.Helper()
		doc := map[string]any{
			"schema_version":           "1.0",
			"approved_project_root":    root,
			"python_interpreter":       ".venv-host/bin/python",
			"run_root":                 "runs/demo",
			"quarter_root":             "runs/demo/q",
			"readiness_target_dir":     "runs/demo/target",
			"readiness_scratch_root":   "runs/demo/scratch",
			"finalization_result_path": "runs/demo/final.json",
			"completion_marker_paths":  []any{"runs/demo/q/complete.marker.json"},
			"completion_pin_path":      "runs/demo/completion.pin.json",
			"items": []any{
				map[string]any{
					"item_id":           "item-1",
					"raw_response_path": "runs/demo/out/item-1.json",
					"staged_input_path": "runs/demo/out/item-1.staged.json",
				},
			},
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

	if _, err := LoadContract(writeContract("ok.json", nil)); err != nil {
		t.Fatalf("valid contract: %v", err)
	}

	if _, err := LoadContract(writeContract("abs.json", func(doc map[string]any) {
		doc["run_root"] = "/tmp/escape"
	})); err == nil {
		t.Fatal("expected absolute child path rejection")
	}

	if _, err := LoadContract(writeContract("dotdot.json", func(doc map[string]any) {
		doc["run_root"] = "../outside"
	})); err == nil {
		t.Fatal("expected .. rejection")
	}

	if _, err := LoadContract(writeContract("dup-id.json", func(doc map[string]any) {
		doc["items"] = []any{
			map[string]any{"item_id": "item-1", "raw_response_path": "a.json", "staged_input_path": "b.json"},
			map[string]any{"item_id": "item-1", "raw_response_path": "c.json", "staged_input_path": "d.json"},
		}
	})); err == nil {
		t.Fatal("expected duplicate item_id rejection")
	}

	if _, err := LoadContract(writeContract("dup-path.json", func(doc map[string]any) {
		doc["items"] = []any{
			map[string]any{"item_id": "item-1", "raw_response_path": "same.json", "staged_input_path": "b.json"},
			map[string]any{"item_id": "item-2", "raw_response_path": "same.json", "staged_input_path": "d.json"},
		}
	})); err == nil {
		t.Fatal("expected duplicate path rejection")
	}

	outside := t.TempDir()
	link := filepath.Join(root, "link-escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadContract(writeContract("symlink.json", func(doc map[string]any) {
		doc["run_root"] = "link-escape/run"
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
	if _, err := LoadContract(trailing); err == nil {
		t.Fatal("expected trailing JSON rejection")
	}
}

func TestPersistResponseOrderingAndStopAfterFailure(t *testing.T) {
	t.Parallel()
	env := newSyntheticEnv(t)
	runner := mustNewRunner(t, env.contract)
	defer runner.Close()

	ctx := context.Background()
	if _, err := runner.PersistResponse(ctx, "item-2", json.RawMessage(`{"n":2}`), "tester"); err == nil {
		t.Fatal("expected item-2 refuse before item-1")
	}

	first, err := runner.PersistResponse(ctx, "item-1", json.RawMessage(`{"n":1,"keep":true}`), "tester")
	if err != nil {
		t.Fatalf("persist item-1: %v", err)
	}
	if first["ok"] != true {
		t.Fatalf("unexpected result: %#v", first)
	}
	raw1, err := os.ReadFile(env.contract.Items[0].RawResponsePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw1) != "{\"n\":1,\"keep\":true}\n" {
		t.Fatalf("raw bytes=%q", raw1)
	}
	if _, err := runner.PersistResponse(ctx, "item-1", json.RawMessage(`{"n":1}`), "tester"); err == nil {
		t.Fatal("expected duplicate persistence refusal")
	}

	argv := readArgvLog(t, env.root)
	assertModuleOrder(t, argv, []string{
		"gong_quarterly_review.response_receipt",
		"gong_quarterly_review.response_adapter",
		"gong_quarterly_review.stage_rehearsal_capture",
	})

	if err := os.WriteFile(filepath.Join(env.root, "fail-stage"), []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := readArgvLog(t, env.root)
	_, err = runner.PersistResponse(ctx, "item-2", json.RawMessage(`{"n":2}`), "tester")
	if err == nil {
		t.Fatal("expected stage failure")
	}
	after := readArgvLog(t, env.root)
	added := after[len(before):]
	modules := modulesFromArgv(added)
	stageIdx := indexOfModule(modules, "gong_quarterly_review.stage_rehearsal_capture")
	if stageIdx < 0 {
		t.Fatalf("expected failed stage invocation, modules=%v", modules)
	}
	if stageIdx != len(modules)-1 {
		t.Fatalf("commands continued after stage: %v", modules)
	}
	if !strings.Contains(err.Error(), "stage failed") {
		t.Fatalf("error=%v", err)
	}
}

func TestStopAfterReceiptFailure(t *testing.T) {
	t.Parallel()
	env := newSyntheticEnv(t)
	runner := mustNewRunner(t, env.contract)
	defer runner.Close()
	if err := os.WriteFile(filepath.Join(env.root, "fail-receipt"), []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := runner.PersistResponse(context.Background(), "item-1", json.RawMessage(`{"n":1}`), "tester")
	if err == nil {
		t.Fatal("expected receipt failure")
	}
	modules := modulesFromArgv(readArgvLog(t, env.root))
	if containsModule(modules, "gong_quarterly_review.response_adapter") ||
		containsModule(modules, "gong_quarterly_review.stage_rehearsal_capture") {
		t.Fatalf("later commands ran after receipt failure: %v", modules)
	}
}

func TestPreflightRejectsExitZeroNotOK(t *testing.T) {
	t.Parallel()
	env := newSyntheticEnv(t)
	runner := mustNewRunner(t, env.contract)
	defer runner.Close()
	if err := os.WriteFile(filepath.Join(env.root, "readiness-not-ok"), []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Preflight(context.Background()); err == nil {
		t.Fatal("expected readiness ok:false refusal even with exit code zero")
	}
}

func TestFinalizeOnceAndStatusReadOnly(t *testing.T) {
	t.Parallel()
	env := newSyntheticEnv(t)
	runner := mustNewRunner(t, env.contract)
	defer runner.Close()
	ctx := context.Background()

	if _, err := runner.PersistResponse(ctx, "item-1", json.RawMessage(`{"n":1}`), "tester"); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.PersistResponse(ctx, "item-2", json.RawMessage(`{"n":2}`), "tester"); err != nil {
		t.Fatal(err)
	}

	before := readArgvLog(t, env.root)
	status, err := runner.GetRunStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status["verdict"] == nil {
		t.Fatalf("status missing verdict: %#v", status)
	}
	statusArgv := readArgvLog(t, env.root)[len(before):]
	for _, line := range statusArgv {
		if strings.Contains(line, "--finalize") || strings.Contains(line, "--recover-pin") {
			t.Fatalf("get_run_status mutated finalize flags: %s", line)
		}
	}

	if _, err := runner.FinalizeRun(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(env.contract.FinalizationResultPath); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.FinalizeRun(ctx); err == nil {
		t.Fatal("expected one-time finalization refusal")
	}
}

func TestFinalizeRequiresFinalizedVerdict(t *testing.T) {
	t.Parallel()
	env := newSyntheticEnv(t)
	runner := mustNewRunner(t, env.contract)
	defer runner.Close()
	ctx := context.Background()
	if _, err := runner.PersistResponse(ctx, "item-1", json.RawMessage(`{"n":1}`), "tester"); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.PersistResponse(ctx, "item-2", json.RawMessage(`{"n":2}`), "tester"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(env.root, "verifier-wrong-final-stage"), []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.FinalizeRun(ctx); err == nil {
		t.Fatal("expected finalize refusal when verifier stage is not finalized")
	}
}

func TestFinalizeRefusesConfiguredCompletionArtifacts(t *testing.T) {
	t.Parallel()
	env := newSyntheticEnv(t)
	env.contract.CompletionArtifactPaths = []string{
		filepath.Join(env.root, "runs", "demo", "markers", "capture-complete.marker.json"),
		filepath.Join(env.root, "runs", "demo", "completion.pin.json"),
	}
	runner := mustNewRunner(t, env.contract)
	defer runner.Close()
	ctx := context.Background()
	if _, err := runner.PersistResponse(ctx, "item-1", json.RawMessage(`{"n":1}`), "tester"); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.PersistResponse(ctx, "item-2", json.RawMessage(`{"n":2}`), "tester"); err != nil {
		t.Fatal(err)
	}

	for _, artifact := range env.contract.CompletionArtifactPaths {
		if err := os.MkdirAll(filepath.Dir(artifact), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(artifact, []byte(`{"ok":true}`), 0o644); err != nil {
			t.Fatal(err)
		}
		before := readArgvLog(t, env.root)
		_, err := runner.FinalizeRun(ctx)
		if err == nil {
			t.Fatalf("expected refusal for artifact %s", artifact)
		}
		after := readArgvLog(t, env.root)
		for _, line := range after[len(before):] {
			if strings.Contains(line, "verify_ordering_rehearsal") {
				t.Fatalf("finalize invoked verifier despite artifact %s: %s", artifact, line)
			}
		}
		if err := os.Remove(artifact); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDispatchRejectsUnknownOperationAndExtraFields(t *testing.T) {
	t.Parallel()
	env := newSyntheticEnv(t)
	runner := mustNewRunner(t, env.contract)
	defer runner.Close()
	if _, err := Dispatch(context.Background(), runner, json.RawMessage(`{"operation":"shell"}`)); err == nil {
		t.Fatal("expected unknown operation rejection")
	}
	if _, err := Dispatch(context.Background(), runner, json.RawMessage(`{"operation":"preflight","item_id":"x"}`)); err == nil {
		t.Fatal("expected extra field rejection")
	}
	if _, err := Dispatch(context.Background(), runner, json.RawMessage(`{"operation":"preflight","command":"rm -rf /"}`)); err == nil {
		t.Fatal("expected unknown field rejection")
	}
	if _, err := Dispatch(context.Background(), runner, json.RawMessage(`{"operation":"preflight"}{"operation":"evil"}`)); err == nil {
		t.Fatal("expected trailing JSON rejection")
	}
}

func TestOversizedResponseRejected(t *testing.T) {
	t.Parallel()
	env := newSyntheticEnv(t)
	env.contract.MaxResponseBytes = 32
	runner := mustNewRunner(t, env.contract)
	defer runner.Close()
	big := json.RawMessage(`{"pad":"` + strings.Repeat("x", 64) + `"}`)
	if _, err := runner.PersistResponse(context.Background(), "item-1", big, "tester"); err == nil {
		t.Fatal("expected oversized rejection")
	}
	if _, err := os.Stat(env.contract.Items[0].RawResponsePath); !os.IsNotExist(err) {
		t.Fatalf("raw file should not exist, err=%v", err)
	}
}

func TestPostStartupSymlinkCannotRedirectWrite(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	outside := t.TempDir()
	fakeSrc := filepath.Join("testdata", "synthetic", "fake-python")
	fakeDst := filepath.Join(root, ".venv-host", "bin", "python")
	mustCopyExecutable(t, fakeSrc, fakeDst)
	for _, dir := range []string{
		filepath.Join(root, "src"),
		filepath.Join(root, "runs", "demo", "target"),
		filepath.Join(root, "runs", "demo", "scratch"),
		filepath.Join(root, "runs", "demo", "q"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Deliberately do not create runs/demo/out so LoadContract appends it lexically.
	doc := map[string]any{
		"schema_version":           "1.0",
		"approved_project_root":    root,
		"python_interpreter":       ".venv-host/bin/python",
		"run_root":                 "runs/demo",
		"quarter_root":             "runs/demo/q",
		"readiness_target_dir":     "runs/demo/target",
		"readiness_scratch_root":   "runs/demo/scratch",
		"finalization_result_path": "runs/demo/final.json",
		"completion_marker_paths":  []any{"runs/demo/q/complete.marker.json"},
		"completion_pin_path":      "runs/demo/completion.pin.json",
		"items": []any{
			map[string]any{
				"item_id":           "item-1",
				"raw_response_path": "runs/demo/out/item-1.json",
				"staged_input_path": "runs/demo/out/item-1.staged.json",
			},
		},
	}
	contractPath := filepath.Join(root, "contract.json")
	raw, _ := json.Marshal(doc)
	if err := os.WriteFile(contractPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	contract, err := LoadContract(contractPath)
	if err != nil {
		t.Fatal(err)
	}
	runner := mustNewRunner(t, contract)
	defer runner.Close()

	outParent := filepath.Join(root, "runs", "demo", "out")
	if err := os.Symlink(outside, outParent); err != nil {
		t.Fatal(err)
	}
	_, err = runner.PersistResponse(context.Background(), "item-1", json.RawMessage(`{"n":1}`), "tester")
	if err == nil {
		t.Fatal("expected persist refusal after post-startup symlink")
	}
	entries, _ := os.ReadDir(outside)
	if len(entries) != 0 {
		t.Fatalf("wrote outside approved root: %v", entries)
	}
	if _, err := os.Lstat(filepath.Join(outside, "item-1.json")); !os.IsNotExist(err) {
		t.Fatalf("outside raw file exists, err=%v", err)
	}
}

func TestVerifierVerdictGate(t *testing.T) {
	t.Parallel()
	env := newSyntheticEnv(t)
	runner := mustNewRunner(t, env.contract)
	defer runner.Close()
	ctx := context.Background()

	if _, err := runner.PersistResponse(ctx, "item-1", json.RawMessage(`{"n":1}`), "tester"); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(env.root, "verifier-not-accepted"), []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.PersistResponse(ctx, "item-2", json.RawMessage(`{"n":2}`), "tester"); err == nil {
		t.Fatal("expected exit-0 not-accepted refusal")
	}
	if _, err := os.Stat(env.contract.Items[1].RawResponsePath); !os.IsNotExist(err) {
		t.Fatal("item-2 should not be persisted when previous verdict is not accepted")
	}

	if err := os.Remove(filepath.Join(env.root, "verifier-not-accepted")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(env.root, "verifier-missing-pending"), []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.PersistResponse(ctx, "item-2", json.RawMessage(`{"n":2}`), "tester"); err == nil {
		t.Fatal("expected refusal when verifier omits pending_item_ids")
	}
	if err := os.Remove(filepath.Join(env.root, "verifier-missing-pending")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(env.root, "verifier-refuse"), []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.PersistResponse(ctx, "item-2", json.RawMessage(`{"n":2}`), "tester"); err == nil {
		t.Fatal("expected nonzero verifier refusal")
	}

	status, err := runner.GetRunStatus(ctx)
	if err == nil {
		t.Fatal("expected get_run_status to surface nonzero verifier failure")
	}
	_ = status
}

type syntheticEnv struct {
	root     string
	contract *ResolvedContract
}

func newSyntheticEnv(t *testing.T) syntheticEnv {
	t.Helper()
	root := t.TempDir()
	fakeSrc := filepath.Join("testdata", "synthetic", "fake-python")
	fakeDst := filepath.Join(root, ".venv-host", "bin", "python")
	mustCopyExecutable(t, fakeSrc, fakeDst)
	_ = os.MkdirAll(filepath.Join(root, "src"), 0o755)
	_ = os.MkdirAll(filepath.Join(root, "runs", "demo", "out"), 0o755)
	_ = os.MkdirAll(filepath.Join(root, "runs", "demo", "target"), 0o755)
	_ = os.MkdirAll(filepath.Join(root, "runs", "demo", "scratch"), 0o755)

	doc := map[string]any{
		"schema_version":           "1.0",
		"approved_project_root":    root,
		"python_interpreter":       ".venv-host/bin/python",
		"run_root":                 "runs/demo",
		"quarter_root":             "runs/demo/q",
		"readiness_target_dir":     "runs/demo/target",
		"readiness_scratch_root":   "runs/demo/scratch",
		"finalization_result_path": "runs/demo/final.json",
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
	contractPath := filepath.Join(root, "contract.json")
	raw, _ := json.Marshal(doc)
	if err := os.WriteFile(contractPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	contract, err := LoadContract(contractPath)
	if err != nil {
		t.Fatalf("LoadContract: %v", err)
	}
	return syntheticEnv{root: root, contract: contract}
}

func mustNewRunner(t *testing.T, contract *ResolvedContract) *Runner {
	t.Helper()
	runner, err := NewRunner(contract)
	if err != nil {
		t.Fatal(err)
	}
	return runner
}

func mustWriteExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustCopyExecutable(t *testing.T, srcRel, dst string) {
	t.Helper()
	srcBytes, err := os.ReadFile(srcRel)
	if err != nil {
		t.Fatal(err)
	}
	mustWriteExecutable(t, dst, string(srcBytes))
}

func readArgvLog(t *testing.T, root string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "argv.log"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}

func modulesFromArgv(lines []string) []string {
	var modules []string
	for _, line := range lines {
		parts := strings.Split(line, "\t")
		for i := 0; i < len(parts)-1; i++ {
			if parts[i] == "-m" {
				modules = append(modules, parts[i+1])
			}
		}
	}
	return modules
}

func assertModuleOrder(t *testing.T, lines []string, want []string) {
	t.Helper()
	got := modulesFromArgv(lines)
	idx := 0
	for _, module := range got {
		if idx < len(want) && module == want[idx] {
			idx++
		}
	}
	if idx != len(want) {
		t.Fatalf("module order=%v want subsequence %v", got, want)
	}
}

func containsModule(modules []string, name string) bool {
	return indexOfModule(modules, name) >= 0
}

func indexOfModule(modules []string, name string) int {
	for i, module := range modules {
		if module == name {
			return i
		}
	}
	return -1
}

func TestWriteJSONExclusivePreservesBytes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	contract := &ResolvedContract{ApprovedProjectRoot: dir, MaxResponseBytes: MaxResponseBytes}
	runner := &Runner{Contract: contract, root: root}
	input := json.RawMessage("{\n  \"b\": 2, \"a\": 1\n}")
	if err := runner.writeJSONExclusive(filepath.Join(dir, "resp.json"), input); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "resp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var compact bytes.Buffer
	_ = json.Compact(&compact, input)
	compact.WriteByte('\n')
	if !bytes.Equal(got, compact.Bytes()) {
		t.Fatalf("got %q want %q", got, compact.Bytes())
	}
}

func TestInstallScriptRefusesWithoutMutatingConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "claude_desktop_config.json")
	original := []byte(`{"mcpServers":{"keep":{"command":"/bin/true"}}}`)
	if err := os.WriteFile(configPath, original, 0o644); err != nil {
		t.Fatal(err)
	}
	contract := filepath.Join(dir, "contract.json")
	binary := filepath.Join(dir, "gongcowork")
	if err := os.WriteFile(contract, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join("..", "..", "scripts", "install-claude-cowork-bridge.sh")
	cmd := exec.Command("bash", script, "--contract", contract, "--binary", binary, "--config", configPath, "--install")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected --install refusal, output=%s", out)
	}
	if code := cmd.ProcessState.ExitCode(); code != 2 {
		t.Fatalf("exit=%d want 2 output=%s", code, out)
	}
	got, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, original) {
		t.Fatalf("config mutated: %s", got)
	}
}
