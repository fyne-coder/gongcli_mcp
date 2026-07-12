package coworkbridge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	Module      string `json:"module"`
	ExitCode    int    `json:"exit_code"`
	DurationMS  int64  `json:"duration_ms"`
	StdoutBytes int    `json:"stdout_bytes"`
	StderrBytes int    `json:"stderr_bytes"`
}

// Runner executes fixed gong_quarterly_review module invocations only.
type Runner struct {
	Contract *ResolvedContract
	root     *os.Root
	// execCommand is replaceable in tests; production uses exec.CommandContext.
	execCommand func(ctx context.Context, name string, argv ...string) *exec.Cmd
}

func NewRunner(contract *ResolvedContract) (*Runner, error) {
	if contract == nil {
		return nil, fmt.Errorf("contract is required")
	}
	root, err := os.OpenRoot(contract.ApprovedProjectRoot)
	if err != nil {
		return nil, fmt.Errorf("open approved project root: %w", err)
	}
	return &Runner{
		Contract: contract,
		root:     root,
		execCommand: func(ctx context.Context, name string, argv ...string) *exec.Cmd {
			return exec.CommandContext(ctx, name, argv...)
		},
	}, nil
}

// Close releases the approved-root handle.
func (r *Runner) Close() error {
	if r == nil || r.root == nil {
		return nil
	}
	err := r.root.Close()
	r.root = nil
	return err
}

func (r *Runner) rel(abs string) (string, error) {
	rel, err := filepath.Rel(r.Contract.ApprovedProjectRoot, abs)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes approved root: %s", abs)
	}
	return rel, nil
}

func (r *Runner) lstatContained(abs string) (os.FileInfo, error) {
	rel, err := r.rel(abs)
	if err != nil {
		return nil, err
	}
	return r.root.Lstat(rel)
}

func (r *Runner) runModule(ctx context.Context, module string, args ...string) (CommandResult, []byte, error) {
	if err := r.verifySegmentConfigPinned(); err != nil {
		return CommandResult{Module: module}, nil, err
	}
	resolvedInterpreter, err := filepath.EvalSymlinks(r.Contract.PythonInterpreter)
	if err != nil {
		return CommandResult{Module: module}, nil, fmt.Errorf("resolve python_interpreter before module %s: %w", module, err)
	}
	if resolvedInterpreter != r.Contract.PythonInterpreterResolved {
		return CommandResult{Module: module}, nil, fmt.Errorf("python_interpreter target changed after startup; refusing module %s", module)
	}
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
	err = cmd.Run()
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
			return result, stdout.Bytes(), fmt.Errorf("module %s timed out after %s", module, timeout)
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		if len(msg) > 512 {
			msg = msg[:512] + "…"
		}
		return result, stdout.Bytes(), fmt.Errorf("module %s failed (exit %d): %s", module, result.ExitCode, msg)
	}
	return result, stdout.Bytes(), nil
}

func (r *Runner) verifySegmentConfigPinned() error {
	if r.Contract.SegmentConfigPath == "" {
		return nil
	}
	info, err := r.lstatContained(r.Contract.SegmentConfigPath)
	if err != nil {
		return fmt.Errorf("segment_config_path changed after startup: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("segment_config_path changed after startup: no longer a regular file")
	}
	payload, err := os.ReadFile(r.Contract.SegmentConfigPath)
	if err != nil {
		return fmt.Errorf("read segment_config_path: %w", err)
	}
	sum := sha256.Sum256(payload)
	if hex.EncodeToString(sum[:]) != r.Contract.SegmentConfigSHA256 {
		return fmt.Errorf("segment_config_path bytes changed after startup; refusing module execution")
	}
	return nil
}

// CheckFreshness runs the project's read-only generic segment freshness gate.
// Legacy Slice 4D contracts intentionally omit segment_config_path and cannot
// call this operation.
func (r *Runner) CheckFreshness(ctx context.Context) (map[string]any, error) {
	if r.Contract.SegmentConfigPath == "" {
		return nil, fmt.Errorf("check_freshness requires segment_config_path in the frozen companion contract")
	}
	result, stdout, err := r.runModule(ctx, "gong_quarterly_review.segment_pipeline",
		"check-freshness",
		"--config", r.Contract.SegmentConfigPath,
		"--run-root", r.Contract.RunRoot,
	)
	if err != nil {
		return nil, err
	}
	verdict, err := parseVerifierVerdict(stdout)
	if err != nil {
		return nil, fmt.Errorf("parse freshness verdict: %w", err)
	}
	if ok, _ := verdict["ok"].(bool); !ok {
		return nil, fmt.Errorf("freshness check returned ok other than true")
	}
	return map[string]any{"ok": true, "command": result, "verdict": verdict}, nil
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
	result, stdout, err := r.runModule(ctx, "gong_quarterly_review.local_bridge_readiness",
		"--approved-root", r.Contract.ApprovedProjectRoot,
		"--target-dir", r.Contract.ReadinessTargetDir,
		"--scratch-root", r.Contract.ReadinessScratchRoot,
	)
	if err != nil {
		return nil, err
	}
	verdict, err := parseVerifierVerdict(stdout)
	if err != nil {
		return nil, fmt.Errorf("parse readiness verdict: %w", err)
	}
	if err := requireReadinessAccepted(verdict); err != nil {
		return nil, err
	}
	return map[string]any{
		"ok":      true,
		"command": result,
		"verdict": verdict,
	}, nil
}

// PersistPreflightResponse saves one verbatim facade response in the fixed
// status -> capabilities order. It never interprets or augments the response.
func (r *Runner) PersistPreflightResponse(kind string, response json.RawMessage) (map[string]any, error) {
	kind = strings.TrimSpace(kind)
	if len(bytes.TrimSpace(response)) == 0 {
		return nil, fmt.Errorf("response is required")
	}
	if len(response) > r.Contract.MaxResponseBytes {
		return nil, fmt.Errorf("response exceeds maximum %d bytes", r.Contract.MaxResponseBytes)
	}
	if !json.Valid(response) {
		return nil, fmt.Errorf("response must be valid JSON")
	}
	if _, err := r.lstatContained(r.Contract.PreDrilldownGatePath); err == nil {
		return nil, fmt.Errorf("pre-drilldown gate already exists; preflight responses are immutable")
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	var output string
	switch kind {
	case "status":
		if _, err := r.lstatContained(r.Contract.CapabilitiesResponsePath); err == nil {
			return nil, fmt.Errorf("capabilities response already exists before status persistence")
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		output = r.Contract.StatusResponsePath
	case "capabilities":
		if _, err := r.lstatContained(r.Contract.StatusResponsePath); err != nil {
			if os.IsNotExist(err) {
				return nil, fmt.Errorf("status response must exist before capabilities persistence")
			}
			return nil, err
		}
		output = r.Contract.CapabilitiesResponsePath
	default:
		return nil, fmt.Errorf("unknown preflight response kind %q", kind)
	}
	if err := r.writeJSONExclusive(output, response); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "kind": kind, "path": output}, nil
}

// IssuePreDrilldownGate derives and issues the fixed project gate from the two
// saved preflight responses. Observed MCP fields are derived by Python from the
// saved bytes rather than accepted from the caller.
func (r *Runner) IssuePreDrilldownGate(ctx context.Context, attestedBy, capturedAt string) (map[string]any, error) {
	attestedBy = strings.TrimSpace(attestedBy)
	capturedAt = strings.TrimSpace(capturedAt)
	if attestedBy == "" {
		return nil, fmt.Errorf("attested_by is required")
	}
	if capturedAt == "" {
		return nil, fmt.Errorf("captured_at is required")
	}
	if _, err := r.lstatContained(r.Contract.PreDrilldownGatePath); err == nil {
		return nil, fmt.Errorf("pre-drilldown gate already exists")
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	for label, path := range map[string]string{
		"status":       r.Contract.StatusResponsePath,
		"capabilities": r.Contract.CapabilitiesResponsePath,
	} {
		if _, err := r.lstatContained(path); err != nil {
			if os.IsNotExist(err) {
				return nil, fmt.Errorf("%s response must exist before issuing gate", label)
			}
			return nil, err
		}
	}
	statusRel, err := filepath.Rel(r.Contract.QuarterRoot, r.Contract.StatusResponsePath)
	if err != nil {
		return nil, err
	}
	capabilitiesRel, err := filepath.Rel(r.Contract.QuarterRoot, r.Contract.CapabilitiesResponsePath)
	if err != nil {
		return nil, err
	}
	args := []string{
		"--quarter-root", r.Contract.QuarterRoot,
		"--quarter-id", r.Contract.QuarterID,
		"--version", r.Contract.Version,
		"--segment-id", r.Contract.SegmentID,
		"--status-response", statusRel,
		"--capabilities-response", capabilitiesRel,
		"--contract-model-id", r.Contract.ContractModelID,
		"--cowork-ui-display-name", r.Contract.CoworkUIDisplayName,
		"--attested-by", attestedBy,
		"--captured-at", capturedAt,
	}
	if r.Contract.SegmentConfigPath != "" {
		args = append(args, "--segment-config", r.Contract.SegmentConfigPath)
	}
	result, stdout, err := r.runModule(ctx, "gong_quarterly_review.preflight_gate_cli", args...)
	if err != nil {
		return nil, err
	}
	verdict, err := parseVerifierVerdict(stdout)
	if err != nil {
		return nil, fmt.Errorf("parse pre-drilldown gate verdict: %w", err)
	}
	if ok, _ := verdict["ok"].(bool); !ok {
		return nil, fmt.Errorf("pre-drilldown gate CLI returned ok other than true")
	}
	if _, err := r.lstatContained(r.Contract.PreDrilldownGatePath); err != nil {
		return nil, fmt.Errorf("gate CLI returned accepted verdict but gate is missing: %w", err)
	}
	return map[string]any{
		"ok": true, "command": result, "verdict": verdict,
		"path": r.Contract.PreDrilldownGatePath,
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
	if _, err := r.lstatContained(r.Contract.PreDrilldownGatePath); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("pre-drilldown gate must exist before item persistence")
		}
		return nil, err
	}
	if r.Contract.SegmentConfigPath != "" {
		if _, err := r.CheckFreshness(ctx); err != nil {
			return nil, fmt.Errorf("freshness check failed before response persistence: %w", err)
		}
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
		if _, err := r.lstatContained(prev.RawResponsePath); err != nil {
			return nil, fmt.Errorf("refusing to persist %q until previous item %q raw response exists", itemID, prev.ItemID)
		}
		if _, err := r.ValidateItem(ctx, prev.ItemID); err != nil {
			return nil, fmt.Errorf("refusing to persist %q until previous item %q validates: %w", itemID, prev.ItemID, err)
		}
	}

	if err := r.writeJSONExclusive(item.RawResponsePath, response); err != nil {
		return nil, err
	}

	commands := make([]CommandResult, 0, 3)
	receipt, _, err := r.runModule(ctx, "gong_quarterly_review.response_receipt",
		"--quarter-root", r.Contract.QuarterRoot,
		"--item-id", item.ItemID,
		"--raw-response", item.RawResponsePath,
	)
	commands = append(commands, receipt)
	if err != nil {
		return nil, fmt.Errorf("receipt failed; adapter and stage were not run: %w", err)
	}

	adapter, _, err := r.runModule(ctx, "gong_quarterly_review.response_adapter",
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

	stage, _, err := r.runModule(ctx, "gong_quarterly_review.stage_rehearsal_capture",
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
	status, verdict, err := r.runOrderingVerifier(ctx, false, "")
	if err != nil {
		return nil, err
	}
	if err := requireItemAccepted(verdict, item.ItemID); err != nil {
		return nil, err
	}
	if _, err := r.lstatContained(item.RawResponsePath); err != nil {
		return nil, fmt.Errorf("item %q raw response is not present", itemID)
	}
	if _, err := r.lstatContained(item.StagedInputPath); err != nil {
		return nil, fmt.Errorf("item %q staged input is not present", itemID)
	}
	return map[string]any{
		"ok":      true,
		"item_id": item.ItemID,
		"command": status,
		"verdict": verdict,
		"paths": map[string]string{
			"raw_response_path": item.RawResponsePath,
			"staged_input_path": item.StagedInputPath,
		},
	}, nil
}

func (r *Runner) FinalizeRun(ctx context.Context) (map[string]any, error) {
	if _, err := r.lstatContained(r.Contract.FinalizationResultPath); err == nil {
		return nil, fmt.Errorf("finalization already recorded at %s", r.Contract.FinalizationResultPath)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	for _, marker := range r.Contract.CompletionArtifactPaths {
		if _, err := r.lstatContained(marker); err == nil {
			return nil, fmt.Errorf("completion artifact already exists at %s", marker)
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}

	result, verdict, err := r.runOrderingVerifier(ctx, true, r.Contract.FinalizationResultPath)
	if err != nil {
		return nil, err
	}
	if err := requireFinalizeAccepted(verdict); err != nil {
		return nil, err
	}
	for _, artifact := range r.Contract.CompletionArtifactPaths {
		if _, err := r.lstatContained(artifact); err != nil {
			return nil, fmt.Errorf("finalizer returned accepted verdict but completion artifact is missing at %s: %w", artifact, err)
		}
	}
	return map[string]any{
		"ok":      true,
		"command": result,
		"verdict": verdict,
		"output":  r.Contract.FinalizationResultPath,
	}, nil
}

func (r *Runner) GetRunStatus(ctx context.Context) (map[string]any, error) {
	result, verdict, err := r.runOrderingVerifier(ctx, false, "")
	if err != nil {
		return nil, err
	}
	ok, _ := verdict["ok"].(bool)
	return map[string]any{
		"ok":      ok,
		"command": result,
		"verdict": verdict,
	}, nil
}

func (r *Runner) runOrderingVerifier(ctx context.Context, finalize bool, outputPath string) (CommandResult, map[string]any, error) {
	args := []string{"--run-root", r.Contract.RunRoot}
	if finalize {
		args = append(args, "--finalize", "--output", outputPath)
	}
	result, stdout, err := r.runModule(ctx, "gong_quarterly_review.verify_ordering_rehearsal", args...)
	if err != nil {
		verdict, parseErr := parseVerifierVerdict(stdout)
		if parseErr == nil {
			return result, verdict, err
		}
		return result, nil, err
	}
	verdict, err := parseVerifierVerdict(stdout)
	if err != nil {
		return result, nil, err
	}
	return result, verdict, nil
}

func parseVerifierVerdict(stdout []byte) (map[string]any, error) {
	payload := bytes.TrimSpace(stdout)
	if len(payload) == 0 {
		return nil, fmt.Errorf("verifier produced empty stdout")
	}
	dec := json.NewDecoder(bytes.NewReader(payload))
	var verdict map[string]any
	if err := dec.Decode(&verdict); err != nil {
		return nil, fmt.Errorf("parse verifier verdict: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("verifier stdout contains trailing JSON")
		}
		return nil, fmt.Errorf("verifier stdout contains trailing data: %w", err)
	}
	if verdict == nil {
		return nil, fmt.Errorf("verifier verdict must be a JSON object")
	}
	return verdict, nil
}

// requireItemAccepted encodes the smallest stable acceptance rule confirmed
// against the real verify_ordering_rehearsal JSON: top-level ok:true can coexist
// with stage pending-items, so ok alone is never sufficient.
func requireItemAccepted(verdict map[string]any, itemID string) error {
	ok, _ := verdict["ok"].(bool)
	if !ok {
		stage, _ := verdict["stage"].(string)
		if stage != "" {
			return fmt.Errorf("verifier rejected item %q (ok=false, stage=%s)", itemID, stage)
		}
		return fmt.Errorf("verifier rejected item %q (ok=false)", itemID)
	}
	if rawJournal, present := verdict["ordering_journal"]; present && rawJournal != nil {
		journal, ok := rawJournal.(map[string]any)
		if !ok {
			return fmt.Errorf("verifier ordering_journal must be an object")
		}
		journalOK, _ := journal["ok"].(bool)
		if !journalOK {
			return fmt.Errorf("verifier ordering_journal.ok is not true for item %q", itemID)
		}
	}
	pending, err := pendingItemIDsRequired(verdict)
	if err != nil {
		stage, _ := verdict["stage"].(string)
		if stage == "captured-pending-finalize" || stage == "finalized" {
			return nil
		}
		return err
	}
	for _, pendingID := range pending {
		if pendingID == itemID {
			stage, _ := verdict["stage"].(string)
			return fmt.Errorf("verifier has not accepted item %q (still pending; stage=%s)", itemID, stage)
		}
	}
	return nil
}

func requireFinalizeAccepted(verdict map[string]any) error {
	ok, _ := verdict["ok"].(bool)
	if !ok {
		stage, _ := verdict["stage"].(string)
		if stage != "" {
			return fmt.Errorf("finalize rejected (ok=false, stage=%s)", stage)
		}
		return fmt.Errorf("finalize rejected (ok=false)")
	}
	if rawJournal, present := verdict["ordering_journal"]; present && rawJournal != nil {
		journal, ok := rawJournal.(map[string]any)
		if !ok {
			return fmt.Errorf("finalize ordering_journal must be an object")
		}
		journalOK, _ := journal["ok"].(bool)
		if !journalOK {
			return fmt.Errorf("finalize ordering_journal.ok is not true")
		}
	}
	stage, _ := verdict["stage"].(string)
	if stage != "finalized" {
		return fmt.Errorf("finalize verdict stage=%q, want finalized", stage)
	}
	return nil
}

func requireReadinessAccepted(verdict map[string]any) error {
	ok, _ := verdict["ok"].(bool)
	if !ok {
		stage, _ := verdict["stage"].(string)
		return fmt.Errorf("readiness rejected (ok is not true, stage=%q)", stage)
	}
	rawChecks, present := verdict["checks"]
	if !present || rawChecks == nil {
		// Older/synthetic readiness implementations may expose only top-level ok.
		return nil
	}
	checks, ok := rawChecks.(map[string]any)
	if !ok || len(checks) == 0 {
		return fmt.Errorf("readiness checks must be a non-empty object")
	}
	for name, raw := range checks {
		check, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("readiness check %q must be an object", name)
		}
		checkOK, _ := check["ok"].(bool)
		if !checkOK {
			return fmt.Errorf("readiness check %q is not accepted", name)
		}
	}
	return nil
}

func pendingItemIDsRequired(verdict map[string]any) ([]string, error) {
	raw, present := verdict["pending_item_ids"]
	if !present || raw == nil {
		return nil, fmt.Errorf("verifier verdict is missing pending_item_ids")
	}
	values, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("verifier pending_item_ids must be an array")
	}
	out := make([]string, 0, len(values))
	for idx, value := range values {
		text, ok := value.(string)
		if !ok || strings.TrimSpace(text) == "" {
			return nil, fmt.Errorf("verifier pending_item_ids[%d] must be a non-empty string", idx)
		}
		out = append(out, text)
	}
	return out, nil
}

func pendingItemIDs(verdict map[string]any) []string {
	raw, ok := verdict["pending_item_ids"]
	if !ok || raw == nil {
		return nil
	}
	switch values := raw.(type) {
	case []any:
		out := make([]string, 0, len(values))
		for _, value := range values {
			out = append(out, fmt.Sprint(value))
		}
		return out
	case []string:
		return append([]string(nil), values...)
	default:
		return nil
	}
}

func (r *Runner) writeJSONExclusive(absPath string, response json.RawMessage) error {
	rel, err := r.rel(absPath)
	if err != nil {
		return err
	}
	dir := filepath.Dir(rel)
	if dir != "." && dir != "" {
		if err := r.root.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, response); err != nil {
		return fmt.Errorf("canonicalize response JSON: %w", err)
	}
	compact.WriteByte('\n')

	f, err := r.root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("refusing duplicate persistence: %s already exists", absPath)
		}
		return err
	}
	if _, err := io.Copy(f, &compact); err != nil {
		_ = f.Close()
		_ = r.root.Remove(rel)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = r.root.Remove(rel)
		return err
	}
	if err := f.Close(); err != nil {
		_ = r.root.Remove(rel)
		return err
	}

	if dir == "." || dir == "" {
		dir = "."
	}
	dirFile, err := r.root.Open(dir)
	if err != nil {
		return fmt.Errorf("open parent directory for fsync: %w", err)
	}
	syncErr := dirFile.Sync()
	closeErr := dirFile.Close()
	if syncErr != nil {
		return fmt.Errorf("fsync parent directory: %w", syncErr)
	}
	return closeErr
}
