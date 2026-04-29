package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/fyne-coder/gongcli_mcp/internal/mcp"
	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
	"github.com/fyne-coder/gongcli_mcp/internal/version"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("gongmcp", flag.ContinueOnError)
	flags.SetOutput(stderr)

	dbPath := flags.String("db", "", "Path to the local gongctl SQLite cache")
	toolAllowlist := flags.String("tool-allowlist", "", "Comma-separated MCP tool allowlist; defaults to GONGMCP_TOOL_ALLOWLIST when unset")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "unexpected arguments: %s\n", strings.Join(flags.Args(), " "))
		return 2
	}

	db := strings.TrimSpace(*dbPath)
	if db == "" {
		fmt.Fprintln(stderr, "--db is required")
		return 2
	}
	if _, err := os.Stat(db); err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(stderr, "db file not found: %s\n", filepath.Clean(db))
			return 2
		}
		fmt.Fprintf(stderr, "stat db: %v\n", err)
		return 1
	}

	allowlist, err := parseToolAllowlist(*toolAllowlist, os.Getenv("GONGMCP_TOOL_ALLOWLIST"))
	if err != nil {
		fmt.Fprintf(stderr, "invalid tool allowlist: %v\n", err)
		return 2
	}

	ctx := context.Background()
	store, err := sqlite.OpenReadOnly(ctx, db)
	if err != nil {
		fmt.Fprintf(stderr, "open db: %v\n", err)
		return 1
	}
	defer store.Close()

	server := mcp.NewServerWithOptions(store, "gongmcp", version.DisplayVersion(), mcp.WithToolAllowlist(allowlist))
	if err := server.Serve(ctx, stdin, stdout); err != nil {
		fmt.Fprintf(stderr, "serve mcp: %v\n", err)
		return 1
	}
	return 0
}

func parseToolAllowlist(flagValue, envValue string) ([]string, error) {
	raw := strings.TrimSpace(flagValue)
	if raw == "" {
		raw = strings.TrimSpace(envValue)
	}
	if raw == "" {
		return nil, nil
	}

	catalog := make(map[string]struct{}, len(mcp.ToolCatalog()))
	for _, tool := range mcp.ToolCatalog() {
		catalog[tool.Name] = struct{}{}
	}

	seen := make(map[string]struct{})
	names := make([]string, 0)
	for _, piece := range strings.Split(raw, ",") {
		name := strings.TrimSpace(piece)
		if name == "" {
			continue
		}
		if _, ok := catalog[name]; !ok {
			return nil, fmt.Errorf("unknown tool %q", name)
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("no valid tool names provided")
	}
	return names, nil
}
