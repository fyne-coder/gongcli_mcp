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
	contractPath := flags.String("contract", "", "Absolute path to the frozen workflow contract JSON")
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
	if strings.TrimSpace(*contractPath) == "" {
		fmt.Fprintln(stderr, "--contract is required")
		return 2
	}
	if !strings.HasPrefix(strings.TrimSpace(*contractPath), "/") {
		fmt.Fprintln(stderr, "--contract must be an absolute path")
		return 2
	}

	contract, err := coworkbridge.LoadContract(*contractPath)
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
