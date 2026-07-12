package coworkbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// CommandResult is metadata about one fixed module invocation.
type CommandResult struct {
	Module     string `json:"module"`
	ExitCode   int    `json:"exit_code"`
	DurationMS int64  `json:"duration_ms"`
	StdoutBytes int   `json:"stdout_bytes"`
	StderrBytes int   `json:"stderr_bytes"`
}

// Runner executes fixed gong_quarterly_review module invocations only.
type Runner struct {
	Contract *ResolvedContract
	// execCommand is replaceable in tests; production uses exec.CommandContext.
	execCommand func(ctx context.Context, name string, argv ...string) *exec.Cmd
}

func NewRunner(contract *ResolvedContract) *Runner {
	return &Runner{
		Contract: contract,
		execCommand: func(ctx context.Context, name string, argv ...string) *exec.Cmd {
			return exec.CommandContext(ctx, name, argv...)
		},
	}
}

func (r *Runner) runModule(ctx context.Context, module string, args ...string) (CommandResult, error) {
	timeout := r.Contract.CommandTimeout
	if timeout <= 0 {
		timeout = DefaultCommandTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	argv := append([]string{"-m", module}, args...)
	cmd := r.execCommand(runCtx, r.Contract.PythonInterpreter, argv...)
	cmd.Dir = r.Contract.ApprovedProjectRoot
	cmd.Env = []string{
		"PYTHONDONTWRITEBYTECODE=1",
		"PYTHONPATH=" + filepath.Join(r.Contract.ApprovedProjectRoot, "src"),
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &limitedWriter{limit: MaxCommandOutputBytes, buf: &stdout}
	cmd.Stderr = &limitedWriter{limit: MaxCommandOutputBytes, buf: &stderr}

	start := time.Now()
	err := cmd.Run()
	result := CommandResult{
		Module:      module,
		DurationMS:  time.Since(start).Milliseconds(),
		StdoutBytes: stdout.Len(),
		StderrBytes: stderr.Len(),
	}
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}
	if err != nil {
		if runCtx.Err() != nil {
			return result, fmt.Errorf("module %s timed out after %s", module, timeout)
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		if len(msg) > 512 {
			msg = msg[:512] + "…"
		}
		return result, fmt.Errorf("module %s failed (exit %d): %s", module, result.ExitCode, msg)
	}
	return result, nil
}

type limitedWriter struct {
	limit int
	buf   *bytes.Buffer
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	remaining := w.limit - w.buf.Len()
	if remaining <= 0 {
		return len(p), nil
	}
	if len(p) > remaining {
		_, _ = w.buf.Write(p[:remaining])
		return len(p), nil
	}
	return w.buf.Write(p)
}

func (r *Runner) Preflight(ctx context.Context) (map[string]any, error) {
	result, err := r.runModule(ctx, "gong_quarterly_review.local_bridge_readiness",
		"--approved-root", r.Contract.ApprovedProjectRoot,
		"--target-dir", r.Contract.ReadinessTargetDir,
		"--scratch-root", r.Contract.ReadinessScratchRoot,
	)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"ok":      true,
		"command": result,
	}, nil
}

func (r *Runner) PersistResponse(ctx context.Context, itemID string, response json.RawMessage, attestedBy string) (map[string]any, error) {
	itemID = strings.TrimSpace(itemID)
	attestedBy = strings.TrimSpace(attestedBy)
	if itemID == "" {
		return nil, fmt.Errorf("item_id is required")
	}
	if attestedBy == "" {
		return nil, fmt.Errorf("attested_by is required")
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

	item, err := r.Contract.Item(itemID)
	if err != nil {
		return nil, err
	}
	idx, err := r.Contract.ItemIndex(itemID)
	if err != nil {
		return nil, err
	}
	if idx > 0 {
		prev := r.Contract.Items[idx-1]
		if _, err := os.Lstat(prev.RawResponsePath); err != nil {
			return nil, fmt.Errorf("refusing to persist %q until previous item %q raw response exists", itemID, prev.ItemID)
		}
		if _, err := r.ValidateItem(ctx, prev.ItemID); err != nil {
			return nil, fmt.Errorf("refusing to persist %q until previous item %q validates: %w", itemID, prev.ItemID, err)
		}
	}

	if err := writeJSONExclusive(item.RawResponsePath, response); err != nil {
		return nil, err
	}

	commands := make([]CommandResult, 0, 3)
	receipt, err := r.runModule(ctx, "gong_quarterly_review.response_receipt",
		"--quarter-root", r.Contract.QuarterRoot,
		"--item-id", item.ItemID,
		"--raw-response", item.RawResponsePath,
	)
	commands = append(commands, receipt)
	if err != nil {
		return nil, fmt.Errorf("receipt failed; adapter and stage were not run: %w", err)
	}

	adapter, err := r.runModule(ctx, "gong_quarterly_review.response_adapter",
		"--quarter-root", r.Contract.QuarterRoot,
		"--item-id", item.ItemID,
		"--raw-response", item.RawResponsePath,
		"--attested-by", attestedBy,
		"--output", item.StagedInputPath,
	)
	commands = append(commands, adapter)
	if err != nil {
		return nil, fmt.Errorf("adapter failed; stage was not run: %w", err)
	}

	stage, err := r.runModule(ctx, "gong_quarterly_review.stage_rehearsal_capture",
		"--run-root", r.Contract.RunRoot,
		"--input", item.StagedInputPath,
	)
	commands = append(commands, stage)
	if err != nil {
		return nil, fmt.Errorf("stage failed: %w", err)
	}

	return map[string]any{
		"ok":       true,
		"item_id":  item.ItemID,
		"raw_path": item.RawResponsePath,
		"commands": commands,
	}, nil
}

func (r *Runner) ValidateItem(ctx context.Context, itemID string) (map[string]any, error) {
	itemID = strings.TrimSpace(itemID)
	item, err := r.Contract.Item(itemID)
	if err != nil {
		return nil, err
	}
	status, err := r.runModule(ctx, "gong_quarterly_review.verify_ordering_rehearsal",
		"--run-root", r.Contract.RunRoot,
	)
	if err != nil {
		return nil, err
	}
	if _, err := os.Lstat(item.RawResponsePath); err != nil {
		return nil, fmt.Errorf("item %q raw response is not present", itemID)
	}
	if _, err := os.Lstat(item.StagedInputPath); err != nil {
		return nil, fmt.Errorf("item %q staged input is not present", itemID)
	}
	return map[string]any{
		"ok":      true,
		"item_id": item.ItemID,
		"command": status,
		"paths": map[string]string{
			"raw_response_path": item.RawResponsePath,
			"staged_input_path": item.StagedInputPath,
		},
	}, nil
}

func (r *Runner) FinalizeRun(ctx context.Context) (map[string]any, error) {
	if _, err := os.Lstat(r.Contract.FinalizationResultPath); err == nil {
		return nil, fmt.Errorf("finalization already recorded at %s", r.Contract.FinalizationResultPath)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	for _, marker := range completionArtifactCandidates(r.Contract.QuarterRoot, r.Contract.RunRoot) {
		if _, err := os.Lstat(marker); err == nil {
			return nil, fmt.Errorf("completion artifact already exists at %s", marker)
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}

	result, err := r.runModule(ctx, "gong_quarterly_review.verify_ordering_rehearsal",
		"--run-root", r.Contract.RunRoot,
		"--finalize",
		"--output", r.Contract.FinalizationResultPath,
	)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"ok":      true,
		"command": result,
		"output":  r.Contract.FinalizationResultPath,
	}, nil
}

func (r *Runner) GetRunStatus(ctx context.Context) (map[string]any, error) {
	result, err := r.runModule(ctx, "gong_quarterly_review.verify_ordering_rehearsal",
		"--run-root", r.Contract.RunRoot,
	)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"ok":      true,
		"command": result,
	}, nil
}

func completionArtifactCandidates(quarterRoot, runRoot string) []string {
	return []string{
		filepath.Join(quarterRoot, "markers", "capture-complete.marker.json"),
		filepath.Join(quarterRoot, "capture-complete.marker.json"),
		filepath.Join(runRoot, "markers", "capture-complete.marker.json"),
		filepath.Join(runRoot, "capture-complete.marker.json"),
	}
}

func writeJSONExclusive(path string, response json.RawMessage) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, response); err != nil {
		return fmt.Errorf("canonicalize response JSON: %w", err)
	}
	compact.WriteByte('\n')

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("refusing duplicate persistence: %s already exists", path)
		}
		return err
	}
	defer f.Close()
	if _, err := io.Copy(f, &compact); err != nil {
		_ = os.Remove(path)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = os.Remove(path)
		return err
	}
	return f.Close()
}
