package coworkbridge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	ContractSchemaVersion = "1.0"
	DefaultCommandTimeout = 60 * time.Second
	// MaxResponseBytes is kept comfortably below the gongcowork MCP frame cap
	// (4 MiB) so the tool-layer limit binds before transport framing.
	MaxResponseBytes      = 3 << 20 // 3 MiB
	MaxCommandOutputBytes = 256 << 10
)

// ContractItem freezes one capture item and its exclusive output paths.
type ContractItem struct {
	ItemID          string `json:"item_id"`
	RawResponsePath string `json:"raw_response_path"`
	StagedInputPath string `json:"staged_input_path"`
}

// ResolvedContract is the startup-validated absolute path view of a contract.
type ResolvedContract struct {
	SchemaVersion              string
	ContractPath               string
	ApprovedProjectRoot        string
	PythonInterpreter          string
	RunRoot                    string
	QuarterRoot                string
	StatusResponsePath         string
	CapabilitiesResponsePath   string
	PreDrilldownGatePath       string
	QuarterID                  string
	Version                    string
	SegmentID                  string
	ContractModelID            string
	CoworkUIDisplayName        string
	ReadinessTargetDir         string
	ReadinessScratchRoot       string
	FinalizationResultPath     string
	CompletionArtifactPaths    []string
	Items                      []ResolvedItem
	CommandTimeout             time.Duration
	MaxResponseBytes           int
	ContractInsideApprovedRoot bool
}

// ResolvedItem holds absolute paths for one frozen item.
type ResolvedItem struct {
	ItemID          string
	RawResponsePath string
	StagedInputPath string
}

type contractFile struct {
	SchemaVersion            string         `json:"schema_version"`
	ApprovedProjectRoot      string         `json:"approved_project_root"`
	PythonInterpreter        string         `json:"python_interpreter"`
	RunRoot                  string         `json:"run_root"`
	QuarterRoot              string         `json:"quarter_root"`
	StatusResponsePath       string         `json:"status_response_path"`
	CapabilitiesResponsePath string         `json:"capabilities_response_path"`
	PreDrilldownGatePath     string         `json:"pre_drilldown_gate_path"`
	QuarterID                string         `json:"quarter_id"`
	Version                  string         `json:"version"`
	SegmentID                string         `json:"segment_id"`
	ContractModelID          string         `json:"contract_model_id"`
	CoworkUIDisplayName      string         `json:"cowork_ui_display_name"`
	ReadinessTargetDir       string         `json:"readiness_target_dir"`
	ReadinessScratchRoot     string         `json:"readiness_scratch_root"`
	FinalizationResultPath   string         `json:"finalization_result_path"`
	CompletionMarkerPaths    []string       `json:"completion_marker_paths"`
	CompletionPinPath        string         `json:"completion_pin_path"`
	Items                    []ContractItem `json:"items"`
}

// LoadContract loads and validates a frozen contract from an absolute path.
func LoadContract(contractPath string) (*ResolvedContract, error) {
	if !filepath.IsAbs(strings.TrimSpace(contractPath)) {
		return nil, fmt.Errorf("contract path must be absolute")
	}
	absContract, err := canonicalizeExisting(contractPath)
	if err != nil {
		return nil, fmt.Errorf("contract path: %w", err)
	}

	raw, err := os.ReadFile(absContract)
	if err != nil {
		return nil, fmt.Errorf("read contract: %w", err)
	}
	var parsed contractFile
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&parsed); err != nil {
		return nil, fmt.Errorf("parse contract: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("parse contract: trailing JSON after contract document")
		}
		return nil, fmt.Errorf("parse contract: trailing data after contract document: %w", err)
	}
	if strings.TrimSpace(parsed.SchemaVersion) != ContractSchemaVersion {
		return nil, fmt.Errorf("unsupported contract schema_version %q (want %q)", parsed.SchemaVersion, ContractSchemaVersion)
	}
	if len(parsed.Items) == 0 {
		return nil, fmt.Errorf("contract items must not be empty")
	}
	if !filepath.IsAbs(strings.TrimSpace(parsed.ApprovedProjectRoot)) {
		return nil, fmt.Errorf("approved_project_root must be absolute")
	}

	approvedRoot, err := canonicalizeExistingDir(parsed.ApprovedProjectRoot)
	if err != nil {
		return nil, fmt.Errorf("approved_project_root: %w", err)
	}
	contractInsideRoot := underRoot(approvedRoot, absContract)

	interpreterRel := strings.TrimSpace(parsed.PythonInterpreter)
	if err := requireRelativePath(interpreterRel, "python_interpreter"); err != nil {
		return nil, err
	}
	interpreterAbs, err := resolveContainedPath(approvedRoot, interpreterRel, true)
	if err != nil {
		return nil, fmt.Errorf("python_interpreter: %w", err)
	}
	info, err := os.Stat(interpreterAbs)
	if err != nil {
		return nil, fmt.Errorf("python_interpreter: %w", err)
	}
	if info.IsDir() || info.Mode()&0o111 == 0 {
		return nil, fmt.Errorf("python_interpreter must be an executable file under the approved root")
	}

	runRootRel := strings.TrimSpace(parsed.RunRoot)
	quarterRootRel := strings.TrimSpace(parsed.QuarterRoot)
	statusRel := strings.TrimSpace(parsed.StatusResponsePath)
	capabilitiesRel := strings.TrimSpace(parsed.CapabilitiesResponsePath)
	gateRel := strings.TrimSpace(parsed.PreDrilldownGatePath)
	targetRel := strings.TrimSpace(parsed.ReadinessTargetDir)
	scratchRel := strings.TrimSpace(parsed.ReadinessScratchRoot)
	finalRel := strings.TrimSpace(parsed.FinalizationResultPath)
	for _, pair := range []struct {
		label string
		value string
	}{
		{"run_root", runRootRel},
		{"quarter_root", quarterRootRel},
		{"status_response_path", statusRel},
		{"capabilities_response_path", capabilitiesRel},
		{"pre_drilldown_gate_path", gateRel},
		{"readiness_target_dir", targetRel},
		{"readiness_scratch_root", scratchRel},
		{"finalization_result_path", finalRel},
	} {
		if err := requireRelativePath(pair.value, pair.label); err != nil {
			return nil, err
		}
	}

	runRootAbs, err := resolveContainedPath(approvedRoot, runRootRel, false)
	if err != nil {
		return nil, fmt.Errorf("run_root: %w", err)
	}
	quarterRootAbs, err := resolveContainedPath(approvedRoot, quarterRootRel, false)
	if err != nil {
		return nil, fmt.Errorf("quarter_root: %w", err)
	}
	statusAbs, err := resolveContainedPath(approvedRoot, statusRel, false)
	if err != nil {
		return nil, fmt.Errorf("status_response_path: %w", err)
	}
	capabilitiesAbs, err := resolveContainedPath(approvedRoot, capabilitiesRel, false)
	if err != nil {
		return nil, fmt.Errorf("capabilities_response_path: %w", err)
	}
	gateAbs, err := resolveContainedPath(approvedRoot, gateRel, false)
	if err != nil {
		return nil, fmt.Errorf("pre_drilldown_gate_path: %w", err)
	}
	for label, path := range map[string]string{
		"status_response_path":       statusAbs,
		"capabilities_response_path": capabilitiesAbs,
		"pre_drilldown_gate_path":    gateAbs,
	} {
		if !underRoot(quarterRootAbs, path) {
			return nil, fmt.Errorf("%s must be under quarter_root", label)
		}
	}
	if gateAbs != filepath.Join(quarterRootAbs, "pre-drilldown-gate.json") {
		return nil, fmt.Errorf("pre_drilldown_gate_path must be quarter_root/pre-drilldown-gate.json")
	}
	for label, value := range map[string]string{
		"quarter_id":             parsed.QuarterID,
		"version":                parsed.Version,
		"segment_id":             parsed.SegmentID,
		"contract_model_id":      parsed.ContractModelID,
		"cowork_ui_display_name": parsed.CoworkUIDisplayName,
	} {
		if strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("%s is required", label)
		}
	}
	targetAbs, err := resolveContainedPath(approvedRoot, targetRel, false)
	if err != nil {
		return nil, fmt.Errorf("readiness_target_dir: %w", err)
	}
	scratchAbs, err := resolveContainedPath(approvedRoot, scratchRel, false)
	if err != nil {
		return nil, fmt.Errorf("readiness_scratch_root: %w", err)
	}
	finalAbs, err := resolveContainedPath(approvedRoot, finalRel, false)
	if err != nil {
		return nil, fmt.Errorf("finalization_result_path: %w", err)
	}

	seenIDs := make(map[string]struct{}, len(parsed.Items))
	seenPaths := make(map[string]string)
	for label, path := range map[string]string{
		"finalization_result_path":   finalAbs,
		"status_response_path":       statusAbs,
		"capabilities_response_path": capabilitiesAbs,
		"pre_drilldown_gate_path":    gateAbs,
	} {
		if owner, ok := seenPaths[path]; ok {
			return nil, fmt.Errorf("duplicate output path %q used by %s and %s", path, owner, label)
		}
		seenPaths[path] = label
	}
	completionArtifacts := make([]string, 0, len(parsed.CompletionMarkerPaths)+1)
	if len(parsed.CompletionMarkerPaths) == 0 {
		return nil, fmt.Errorf("completion_marker_paths must contain at least one path")
	}
	for idx, markerRel := range parsed.CompletionMarkerPaths {
		markerRel = strings.TrimSpace(markerRel)
		if err := requireRelativePath(markerRel, fmt.Sprintf("completion_marker_paths[%d]", idx)); err != nil {
			return nil, err
		}
		markerAbs, err := resolveContainedPath(approvedRoot, markerRel, false)
		if err != nil {
			return nil, fmt.Errorf("completion_marker_paths[%d]: %w", idx, err)
		}
		if owner, ok := seenPaths[markerAbs]; ok {
			return nil, fmt.Errorf("duplicate completion artifact path %q used by %s", markerAbs, owner)
		}
		seenPaths[markerAbs] = fmt.Sprintf("completion_marker_paths[%d]", idx)
		completionArtifacts = append(completionArtifacts, markerAbs)
	}
	pinRel := strings.TrimSpace(parsed.CompletionPinPath)
	if pinRel == "" {
		return nil, fmt.Errorf("completion_pin_path is required")
	}
	{
		if err := requireRelativePath(pinRel, "completion_pin_path"); err != nil {
			return nil, err
		}
		pinAbs, err := resolveContainedPath(approvedRoot, pinRel, false)
		if err != nil {
			return nil, fmt.Errorf("completion_pin_path: %w", err)
		}
		if owner, ok := seenPaths[pinAbs]; ok {
			return nil, fmt.Errorf("duplicate completion artifact path %q used by %s", pinAbs, owner)
		}
		seenPaths[pinAbs] = "completion_pin_path"
		completionArtifacts = append(completionArtifacts, pinAbs)
	}

	items := make([]ResolvedItem, 0, len(parsed.Items))
	for idx, item := range parsed.Items {
		id := strings.TrimSpace(item.ItemID)
		if id == "" {
			return nil, fmt.Errorf("items[%d].item_id is required", idx)
		}
		if _, dup := seenIDs[id]; dup {
			return nil, fmt.Errorf("duplicate item_id %q", id)
		}
		seenIDs[id] = struct{}{}

		rawRel := strings.TrimSpace(item.RawResponsePath)
		stagedRel := strings.TrimSpace(item.StagedInputPath)
		if err := requireRelativePath(rawRel, "raw_response_path"); err != nil {
			return nil, fmt.Errorf("item %q: %w", id, err)
		}
		if err := requireRelativePath(stagedRel, "staged_input_path"); err != nil {
			return nil, fmt.Errorf("item %q: %w", id, err)
		}
		rawAbs, err := resolveContainedPath(approvedRoot, rawRel, false)
		if err != nil {
			return nil, fmt.Errorf("item %q raw_response_path: %w", id, err)
		}
		stagedAbs, err := resolveContainedPath(approvedRoot, stagedRel, false)
		if err != nil {
			return nil, fmt.Errorf("item %q staged_input_path: %w", id, err)
		}
		for _, path := range []string{rawAbs, stagedAbs} {
			if owner, ok := seenPaths[path]; ok {
				return nil, fmt.Errorf("duplicate output path %q used by %q and %q", path, owner, id)
			}
		}
		seenPaths[rawAbs] = id + ":raw"
		seenPaths[stagedAbs] = id + ":staged"
		items = append(items, ResolvedItem{
			ItemID:          id,
			RawResponsePath: rawAbs,
			StagedInputPath: stagedAbs,
		})
	}

	return &ResolvedContract{
		SchemaVersion:              ContractSchemaVersion,
		ContractPath:               absContract,
		ApprovedProjectRoot:        approvedRoot,
		PythonInterpreter:          interpreterAbs,
		RunRoot:                    runRootAbs,
		QuarterRoot:                quarterRootAbs,
		StatusResponsePath:         statusAbs,
		CapabilitiesResponsePath:   capabilitiesAbs,
		PreDrilldownGatePath:       gateAbs,
		QuarterID:                  strings.TrimSpace(parsed.QuarterID),
		Version:                    strings.TrimSpace(parsed.Version),
		SegmentID:                  strings.TrimSpace(parsed.SegmentID),
		ContractModelID:            strings.TrimSpace(parsed.ContractModelID),
		CoworkUIDisplayName:        strings.TrimSpace(parsed.CoworkUIDisplayName),
		ReadinessTargetDir:         targetAbs,
		ReadinessScratchRoot:       scratchAbs,
		FinalizationResultPath:     finalAbs,
		CompletionArtifactPaths:    completionArtifacts,
		Items:                      items,
		CommandTimeout:             DefaultCommandTimeout,
		MaxResponseBytes:           MaxResponseBytes,
		ContractInsideApprovedRoot: contractInsideRoot,
	}, nil
}

func (c *ResolvedContract) Item(itemID string) (ResolvedItem, error) {
	for _, item := range c.Items {
		if item.ItemID == itemID {
			return item, nil
		}
	}
	return ResolvedItem{}, fmt.Errorf("unknown item_id %q", itemID)
}

func (c *ResolvedContract) ItemIndex(itemID string) (int, error) {
	for idx, item := range c.Items {
		if item.ItemID == itemID {
			return idx, nil
		}
	}
	return -1, fmt.Errorf("unknown item_id %q", itemID)
}

func requireRelativePath(value, label string) error {
	if value == "" {
		return fmt.Errorf("%s is required", label)
	}
	if filepath.IsAbs(value) {
		return fmt.Errorf("%s must be project-relative, got absolute path", label)
	}
	clean := filepath.Clean(value)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%s must not escape the approved root", label)
	}
	if strings.Contains(value, "\x00") {
		return fmt.Errorf("%s contains NUL", label)
	}
	return nil
}

func canonicalizeExisting(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("path is required")
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("path must be absolute")
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	return resolved, nil
}

func canonicalizeExistingDir(path string) (string, error) {
	resolved, err := canonicalizeExisting(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("not a directory: %s", resolved)
	}
	return resolved, nil
}

// resolveContainedPath joins root/rel, canonicalizes through any existing
// symlink prefix, and refuses results that escape root.
func resolveContainedPath(root, rel string, mustExist bool) (string, error) {
	if err := requireRelativePath(rel, "path"); err != nil {
		return "", err
	}
	candidate := filepath.Join(root, filepath.Clean(rel))
	resolved, err := resolveThroughSymlinks(candidate)
	if err != nil {
		return "", err
	}
	if !underRoot(root, resolved) {
		return "", fmt.Errorf("path escapes approved root via symlink or traversal")
	}
	if mustExist {
		if _, err := os.Stat(resolved); err != nil {
			return "", err
		}
	}
	return resolved, nil
}

func resolveThroughSymlinks(path string) (string, error) {
	cleaned := filepath.Clean(path)
	if !filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("path must be absolute")
	}
	if _, err := os.Lstat(cleaned); err == nil {
		resolved, err := filepath.EvalSymlinks(cleaned)
		if err != nil {
			return "", fmt.Errorf("symlink path rejected: %w", err)
		}
		return resolved, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}

	// Path does not exist yet: resolve the longest existing prefix, then append
	// the remaining lexical components.
	parent := filepath.Dir(cleaned)
	leaf := filepath.Base(cleaned)
	if parent == cleaned {
		return cleaned, nil
	}
	resolvedParent, err := resolveThroughSymlinks(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolvedParent, leaf), nil
}

func underRoot(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
