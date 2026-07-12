package coworkbridge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const selectionWorkflowModule = "gong_quarterly_review.candidate_selection_workflow"

// SelectionRunner executes fixed candidate-selection module invocations only.
type SelectionRunner struct {
	Contract *ResolvedSelectionContract
	root     *os.Root
	// execCommand is replaceable in tests; production uses exec.CommandContext.
	execCommand func(ctx context.Context, name string, argv ...string) *exec.Cmd
}

func NewSelectionRunner(contract *ResolvedSelectionContract) (*SelectionRunner, error) {
	if contract == nil {
		return nil, fmt.Errorf("selection contract is required")
	}
	root, err := os.OpenRoot(contract.ApprovedProjectRoot)
	if err != nil {
		return nil, fmt.Errorf("open approved project root: %w", err)
	}
	return &SelectionRunner{
		Contract: contract,
		root:     root,
		execCommand: func(ctx context.Context, name string, argv ...string) *exec.Cmd {
			return exec.CommandContext(ctx, name, argv...)
		},
	}, nil
}

// Close releases the approved-root handle.
func (r *SelectionRunner) Close() error {
	if r == nil || r.root == nil {
		return nil
	}
	err := r.root.Close()
	r.root = nil
	return err
}

func (r *SelectionRunner) fixedPathArgs() []string {
	return []string{
		"--config", r.Contract.SelectionConfigPath,
		"--state", r.Contract.SelectionStatePath,
		"--output", r.Contract.SelectionOutputPath,
	}
}

func (r *SelectionRunner) rel(abs string) (string, error) {
	rel, err := filepath.Rel(r.Contract.ApprovedProjectRoot, abs)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes approved root: %s", abs)
	}
	return rel, nil
}

func (r *SelectionRunner) verifySelectionConfigPinned() error {
	rel, err := r.rel(r.Contract.SelectionConfigPath)
	if err != nil {
		return err
	}
	info, err := r.root.Lstat(rel)
	if err != nil {
		return fmt.Errorf("selection_config_path changed after startup: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("selection_config_path changed after startup: no longer a regular file")
	}
	payload, err := os.ReadFile(r.Contract.SelectionConfigPath)
	if err != nil {
		return fmt.Errorf("read selection_config_path: %w", err)
	}
	sum := sha256.Sum256(payload)
	if hex.EncodeToString(sum[:]) != r.Contract.SelectionConfigSHA256 {
		return fmt.Errorf("selection_config_path bytes changed after startup; refusing module execution")
	}
	return nil
}

func (r *SelectionRunner) runSelectionModule(ctx context.Context, subcommand string, stdin []byte, extraArgs ...string) (CommandResult, []byte, error) {
	if err := r.verifySelectionConfigPinned(); err != nil {
		return CommandResult{Module: selectionWorkflowModule}, nil, err
	}
	resolvedInterpreter, err := filepath.EvalSymlinks(r.Contract.PythonInterpreter)
	if err != nil {
		return CommandResult{Module: selectionWorkflowModule}, nil, fmt.Errorf("resolve python_interpreter before %s: %w", subcommand, err)
	}
	if resolvedInterpreter != r.Contract.PythonInterpreterResolved {
		return CommandResult{Module: selectionWorkflowModule}, nil, fmt.Errorf("python_interpreter target changed after startup; refusing %s", subcommand)
	}

	timeout := r.Contract.CommandTimeout
	if timeout <= 0 {
		timeout = DefaultCommandTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	argv := append([]string{"-m", selectionWorkflowModule, subcommand}, r.fixedPathArgs()...)
	argv = append(argv, extraArgs...)
	cmd := r.execCommand(runCtx, r.Contract.PythonInterpreter, argv...)
	cmd.Dir = r.Contract.ApprovedProjectRoot
	cmd.Env = []string{
		"PYTHONDONTWRITEBYTECODE=1",
		"PYTHONPATH=" + filepath.Join(r.Contract.ApprovedProjectRoot, "src"),
	}
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &limitedWriter{limit: MaxCommandOutputBytes, buf: &stdout}
	cmd.Stderr = &limitedWriter{limit: MaxCommandOutputBytes, buf: &stderr}

	start := time.Now()
	err = cmd.Run()
	result := CommandResult{
		Module:      selectionWorkflowModule + " " + subcommand,
		DurationMS:  time.Since(start).Milliseconds(),
		StdoutBytes: stdout.Len(),
		StderrBytes: stderr.Len(),
	}
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}
	if err != nil {
		if runCtx.Err() != nil {
			return result, stdout.Bytes(), fmt.Errorf("module %s %s timed out after %s", selectionWorkflowModule, subcommand, timeout)
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		if len(msg) > 512 {
			msg = msg[:512] + "…"
		}
		return result, stdout.Bytes(), fmt.Errorf("module %s %s failed (exit %d): %s", selectionWorkflowModule, subcommand, result.ExitCode, msg)
	}
	return result, stdout.Bytes(), nil
}

func (r *SelectionRunner) wrapModuleResult(result CommandResult, stdout []byte) (map[string]any, error) {
	out := map[string]any{"ok": true, "command": result}
	if len(bytes.TrimSpace(stdout)) == 0 {
		return out, nil
	}
	verdict, err := parseVerifierVerdict(stdout)
	if err != nil {
		return nil, fmt.Errorf("parse selection module stdout: %w", err)
	}
	if ok, _ := verdict["ok"].(bool); !ok {
		stage, _ := verdict["stage"].(string)
		if stage != "" {
			return nil, fmt.Errorf("selection module returned ok other than true (stage=%s)", stage)
		}
		return nil, fmt.Errorf("selection module returned ok other than true")
	}
	out["verdict"] = verdict
	return out, nil
}

// SelectionPreflight runs the fixed selection preflight subcommand.
func (r *SelectionRunner) SelectionPreflight(ctx context.Context) (map[string]any, error) {
	extra := []string{
		"--approved-root", r.Contract.ApprovedProjectRoot,
		"--target-dir", r.Contract.ReadinessTargetDir,
		"--scratch-root", r.Contract.ReadinessScratchRoot,
		"--contract-model-id", r.Contract.ContractModelID,
		"--cowork-ui-display-name", r.Contract.CoworkUIDisplayName,
	}
	result, stdout, err := r.runSelectionModule(ctx, "preflight", nil, extra...)
	if err != nil {
		return nil, err
	}
	return r.wrapModuleResult(result, stdout)
}

// PersistDiscoveryResponse forwards the exact JSON response on stdin.
func (r *SelectionRunner) PersistDiscoveryResponse(ctx context.Context, response json.RawMessage) (map[string]any, error) {
	payload, err := canonicalizeResponseJSON(response, r.Contract.MaxResponseBytes)
	if err != nil {
		return nil, err
	}
	result, stdout, err := r.runSelectionModule(ctx, "persist_discovery_response", payload)
	if err != nil {
		return nil, err
	}
	return r.wrapModuleResult(result, stdout)
}

// GetNextQuery runs the read-only next-query subcommand.
func (r *SelectionRunner) GetNextQuery(ctx context.Context) (map[string]any, error) {
	result, stdout, err := r.runSelectionModule(ctx, "get_next_query", nil)
	if err != nil {
		return nil, err
	}
	return r.wrapModuleResult(result, stdout)
}

// PersistQueryResponse forwards a small JSON envelope on stdin.
func (r *SelectionRunner) PersistQueryResponse(ctx context.Context, queryID string, response json.RawMessage) (map[string]any, error) {
	queryID = strings.TrimSpace(queryID)
	if queryID == "" {
		return nil, fmt.Errorf("query_id is required")
	}
	if len(bytes.TrimSpace(response)) == 0 {
		return nil, fmt.Errorf("response is required")
	}
	if len(response) > r.Contract.MaxResponseBytes {
		return nil, fmt.Errorf("response exceeds maximum %d bytes", r.Contract.MaxResponseBytes)
	}
	if !json.Valid(response) {
		return nil, fmt.Errorf("response must be valid JSON")
	}
	var responseValue any
	if err := json.Unmarshal(response, &responseValue); err != nil {
		return nil, fmt.Errorf("response must be valid JSON: %w", err)
	}
	envelope, err := json.Marshal(map[string]any{
		"query_id": queryID,
		"response": responseValue,
	})
	if err != nil {
		return nil, fmt.Errorf("encode query response envelope: %w", err)
	}
	if len(envelope) > r.Contract.MaxResponseBytes {
		return nil, fmt.Errorf("query response envelope exceeds maximum %d bytes", r.Contract.MaxResponseBytes)
	}
	result, stdout, err := r.runSelectionModule(ctx, "persist_query_response", envelope)
	if err != nil {
		return nil, err
	}
	return r.wrapModuleResult(result, stdout)
}

// FinalizeSelection runs the fixed finalize subcommand.
func (r *SelectionRunner) FinalizeSelection(ctx context.Context) (map[string]any, error) {
	result, stdout, err := r.runSelectionModule(ctx, "finalize_selection", nil)
	if err != nil {
		return nil, err
	}
	return r.wrapModuleResult(result, stdout)
}

// GetSelectionStatus runs the read-only status subcommand.
func (r *SelectionRunner) GetSelectionStatus(ctx context.Context) (map[string]any, error) {
	result, stdout, err := r.runSelectionModule(ctx, "get_selection_status", nil)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"command": result}
	if len(bytes.TrimSpace(stdout)) == 0 {
		out["ok"] = true
		return out, nil
	}
	verdict, err := parseVerifierVerdict(stdout)
	if err != nil {
		return nil, fmt.Errorf("parse selection status stdout: %w", err)
	}
	ok, _ := verdict["ok"].(bool)
	out["ok"] = ok
	out["verdict"] = verdict
	return out, nil
}

func canonicalizeResponseJSON(response json.RawMessage, maxBytes int) ([]byte, error) {
	if len(bytes.TrimSpace(response)) == 0 {
		return nil, fmt.Errorf("response is required")
	}
	if len(response) > maxBytes {
		return nil, fmt.Errorf("response exceeds maximum %d bytes", maxBytes)
	}
	if !json.Valid(response) {
		return nil, fmt.Errorf("response must be valid JSON")
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, response); err != nil {
		return nil, fmt.Errorf("canonicalize response JSON: %w", err)
	}
	return compact.Bytes(), nil
}
