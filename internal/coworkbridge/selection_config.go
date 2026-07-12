package coworkbridge

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const SelectionContractSchemaVersion = "1.0"

// ResolvedSelectionContract is the startup-validated absolute path view of a
// candidate-selection contract. It is distinct from the capture ResolvedContract.
type ResolvedSelectionContract struct {
	SchemaVersion              string
	ContractPath               string
	ApprovedProjectRoot        string
	PythonInterpreter          string
	PythonInterpreterResolved  string
	SelectionConfigPath        string
	SelectionConfigSHA256      string
	SelectionStatePath         string
	SelectionOutputPath        string
	ReadinessTargetDir         string
	ReadinessScratchRoot       string
	ContractModelID            string
	CoworkUIDisplayName        string
	CommandTimeout             time.Duration
	MaxResponseBytes           int
	ContractInsideApprovedRoot bool
}

type selectionContractFile struct {
	SchemaVersion        string `json:"schema_version"`
	ApprovedProjectRoot  string `json:"approved_project_root"`
	PythonInterpreter    string `json:"python_interpreter"`
	SelectionConfigPath  string `json:"selection_config_path"`
	SelectionStatePath   string `json:"selection_state_path"`
	SelectionOutputPath  string `json:"selection_output_path"`
	ReadinessTargetDir   string `json:"readiness_target_dir"`
	ReadinessScratchRoot string `json:"readiness_scratch_root"`
	ContractModelID      string `json:"contract_model_id"`
	CoworkUIDisplayName  string `json:"cowork_ui_display_name"`
}

// LoadSelectionContract loads and validates a frozen selection contract.
func LoadSelectionContract(contractPath string) (*ResolvedSelectionContract, error) {
	if !filepath.IsAbs(strings.TrimSpace(contractPath)) {
		return nil, fmt.Errorf("selection contract path must be absolute")
	}
	absContract, err := canonicalizeExisting(contractPath)
	if err != nil {
		return nil, fmt.Errorf("selection contract path: %w", err)
	}

	raw, err := os.ReadFile(absContract)
	if err != nil {
		return nil, fmt.Errorf("read selection contract: %w", err)
	}
	var parsed selectionContractFile
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&parsed); err != nil {
		return nil, fmt.Errorf("parse selection contract: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("parse selection contract: trailing JSON after contract document")
		}
		return nil, fmt.Errorf("parse selection contract: trailing data after contract document: %w", err)
	}
	if strings.TrimSpace(parsed.SchemaVersion) != SelectionContractSchemaVersion {
		return nil, fmt.Errorf("unsupported selection contract schema_version %q (want %q)", parsed.SchemaVersion, SelectionContractSchemaVersion)
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
	interpreterAbs, interpreterResolved, err := resolveInterpreterPath(approvedRoot, interpreterRel)
	if err != nil {
		return nil, fmt.Errorf("python_interpreter: %w", err)
	}
	info, err := os.Stat(interpreterResolved)
	if err != nil {
		return nil, fmt.Errorf("python_interpreter: %w", err)
	}
	if info.IsDir() || info.Mode()&0o111 == 0 {
		return nil, fmt.Errorf("python_interpreter must be an executable file under the approved root")
	}

	configRel := strings.TrimSpace(parsed.SelectionConfigPath)
	stateRel := strings.TrimSpace(parsed.SelectionStatePath)
	outputRel := strings.TrimSpace(parsed.SelectionOutputPath)
	targetRel := strings.TrimSpace(parsed.ReadinessTargetDir)
	scratchRel := strings.TrimSpace(parsed.ReadinessScratchRoot)
	for _, pair := range []struct {
		label string
		value string
	}{
		{"selection_config_path", configRel},
		{"selection_state_path", stateRel},
		{"selection_output_path", outputRel},
		{"readiness_target_dir", targetRel},
		{"readiness_scratch_root", scratchRel},
	} {
		if err := requireRelativePath(pair.value, pair.label); err != nil {
			return nil, err
		}
	}
	for label, value := range map[string]string{
		"contract_model_id":      parsed.ContractModelID,
		"cowork_ui_display_name": parsed.CoworkUIDisplayName,
	} {
		if strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("%s is required", label)
		}
	}

	configAbs, err := resolveContainedPath(approvedRoot, configRel, true)
	if err != nil {
		return nil, fmt.Errorf("selection_config_path: %w", err)
	}
	configInfo, err := os.Stat(configAbs)
	if err != nil {
		return nil, fmt.Errorf("selection_config_path: %w", err)
	}
	if !configInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("selection_config_path must be a regular file")
	}
	configPayload, err := os.ReadFile(configAbs)
	if err != nil {
		return nil, fmt.Errorf("read selection_config_path: %w", err)
	}
	sum := sha256.Sum256(configPayload)
	configSHA := hex.EncodeToString(sum[:])

	stateAbs, err := resolveContainedPath(approvedRoot, stateRel, false)
	if err != nil {
		return nil, fmt.Errorf("selection_state_path: %w", err)
	}
	outputAbs, err := resolveContainedPath(approvedRoot, outputRel, false)
	if err != nil {
		return nil, fmt.Errorf("selection_output_path: %w", err)
	}
	targetAbs, err := resolveContainedPath(approvedRoot, targetRel, false)
	if err != nil {
		return nil, fmt.Errorf("readiness_target_dir: %w", err)
	}
	scratchAbs, err := resolveContainedPath(approvedRoot, scratchRel, false)
	if err != nil {
		return nil, fmt.Errorf("readiness_scratch_root: %w", err)
	}

	seenPaths := map[string]string{
		configAbs:  "selection_config_path",
		stateAbs:   "selection_state_path",
		outputAbs:  "selection_output_path",
		targetAbs:  "readiness_target_dir",
		scratchAbs: "readiness_scratch_root",
	}
	if len(seenPaths) != 5 {
		// Collision among the five resolved paths.
		owners := make(map[string][]string)
		for path, label := range map[string]string{
			configAbs:  "selection_config_path",
			stateAbs:   "selection_state_path",
			outputAbs:  "selection_output_path",
			targetAbs:  "readiness_target_dir",
			scratchAbs: "readiness_scratch_root",
		} {
			owners[path] = append(owners[path], label)
		}
		for path, labels := range owners {
			if len(labels) > 1 {
				return nil, fmt.Errorf("duplicate selection path %q used by %s", path, strings.Join(labels, " and "))
			}
		}
		return nil, fmt.Errorf("selection contract paths must be unique")
	}
	// Interpreter must not collide with state/output/config/readiness paths.
	if owner, ok := seenPaths[interpreterAbs]; ok {
		return nil, fmt.Errorf("python_interpreter collides with %s", owner)
	}
	if owner, ok := seenPaths[interpreterResolved]; ok {
		return nil, fmt.Errorf("python_interpreter resolved target collides with %s", owner)
	}

	return &ResolvedSelectionContract{
		SchemaVersion:              SelectionContractSchemaVersion,
		ContractPath:               absContract,
		ApprovedProjectRoot:        approvedRoot,
		PythonInterpreter:          interpreterAbs,
		PythonInterpreterResolved:  interpreterResolved,
		SelectionConfigPath:        configAbs,
		SelectionConfigSHA256:      configSHA,
		SelectionStatePath:         stateAbs,
		SelectionOutputPath:        outputAbs,
		ReadinessTargetDir:         targetAbs,
		ReadinessScratchRoot:       scratchAbs,
		ContractModelID:            strings.TrimSpace(parsed.ContractModelID),
		CoworkUIDisplayName:        strings.TrimSpace(parsed.CoworkUIDisplayName),
		CommandTimeout:             DefaultCommandTimeout,
		MaxResponseBytes:           MaxResponseBytes,
		ContractInsideApprovedRoot: contractInsideRoot,
	}, nil
}
