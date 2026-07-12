package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/fyne-coder/gongcli_mcp/internal/coworkbridge"
	"github.com/fyne-coder/gongcli_mcp/internal/mcp"
	"github.com/fyne-coder/gongcli_mcp/internal/version"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("gongcowork", flag.ContinueOnError)
	flags.SetOutput(stderr)
	contractPath := flags.String("contract", "", "Absolute path to the frozen capture workflow contract JSON")
	selectionContractPath := flags.String("selection-contract", "", "Absolute path to the frozen candidate-selection contract JSON")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "unexpected arguments: %s\n", strings.Join(flags.Args(), " "))
		return 2
	}

	captureSet := strings.TrimSpace(*contractPath) != ""
	selectionSet := strings.TrimSpace(*selectionContractPath) != ""
	switch {
	case captureSet && selectionSet:
		fmt.Fprintln(stderr, "--contract and --selection-contract are mutually exclusive")
		return 2
	case !captureSet && !selectionSet:
		fmt.Fprintln(stderr, "exactly one of --contract or --selection-contract is required")
		return 2
	case selectionSet:
		return runSelectionMode(*selectionContractPath, stdin, stdout, stderr)
	default:
		return runCaptureMode(*contractPath, stdin, stdout, stderr)
	}
}

func requireAbsoluteFlag(stderr io.Writer, label, value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		fmt.Fprintf(stderr, "%s is required\n", label)
		return false
	}
	if !strings.HasPrefix(trimmed, "/") {
		fmt.Fprintf(stderr, "%s must be an absolute path\n", label)
		return false
	}
	return true
}

func runCaptureMode(contractPath string, stdin io.Reader, stdout, stderr io.Writer) int {
	if !requireAbsoluteFlag(stderr, "--contract", contractPath) {
		return 2
	}
	contract, err := coworkbridge.LoadContract(contractPath)
	if err != nil {
		fmt.Fprintf(stderr, "load contract: %v\n", err)
		return 2
	}
	if contract.ContractInsideApprovedRoot {
		fmt.Fprintf(stderr, "warning: contract path %s resolves inside approved_project_root; prefer an operator-owned path outside any Cowork-writable directory\n", contract.ContractPath)
	}
	runner, err := coworkbridge.NewRunner(contract)
	if err != nil {
		fmt.Fprintf(stderr, "open runner: %v\n", err)
		return 2
	}
	defer runner.Close()
	server := mcp.NewServerWithOptions(
		nil,
		"gongcowork",
		version.Version,
		mcp.WithToolAllowlist([]string{coworkbridge.ToolName}),
		mcp.WithCustomTools(coworkbridge.Tool(runner)),
		mcp.WithMaxFrameBytes(mcp.CompanionMaxFrameBytes),
		mcp.WithRuntimeInfo(mcp.RuntimeInfo{
			Commit:     version.Commit,
			BuildDate:  version.Date,
			ToolPreset: "cowork-workflow",
		}),
	)
	if err := server.Serve(context.Background(), stdin, stdout); err != nil {
		fmt.Fprintf(stderr, "serve: %v\n", err)
		return 1
	}
	return 0
}

func runSelectionMode(contractPath string, stdin io.Reader, stdout, stderr io.Writer) int {
	if !requireAbsoluteFlag(stderr, "--selection-contract", contractPath) {
		return 2
	}
	contract, err := coworkbridge.LoadSelectionContract(contractPath)
	if err != nil {
		fmt.Fprintf(stderr, "load selection contract: %v\n", err)
		return 2
	}
	if contract.ContractInsideApprovedRoot {
		fmt.Fprintf(stderr, "warning: selection contract path %s resolves inside approved_project_root; prefer an operator-owned path outside any Cowork-writable directory\n", contract.ContractPath)
	}
	runner, err := coworkbridge.NewSelectionRunner(contract)
	if err != nil {
		fmt.Fprintf(stderr, "open selection runner: %v\n", err)
		return 2
	}
	defer runner.Close()
	server := mcp.NewServerWithOptions(
		nil,
		"gongcowork",
		version.Version,
		mcp.WithToolAllowlist([]string{coworkbridge.SelectionToolName}),
		mcp.WithCustomTools(coworkbridge.SelectionTool(runner)),
		mcp.WithMaxFrameBytes(mcp.CompanionMaxFrameBytes),
		mcp.WithRuntimeInfo(mcp.RuntimeInfo{
			Commit:     version.Commit,
			BuildDate:  version.Date,
			ToolPreset: "cowork-candidate-selection",
		}),
	)
	if err := server.Serve(context.Background(), stdin, stdout); err != nil {
		fmt.Fprintf(stderr, "serve: %v\n", err)
		return 1
	}
	return 0
}
