package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/fyne-coder/gongcli_mcp/internal/mcp"
	"github.com/fyne-coder/gongcli_mcp/internal/store/postgres"
)

func (a *app) mcp(ctx context.Context, args []string) error {
	_ = ctx
	if len(args) == 0 {
		fmt.Fprintln(a.err, "usage: gongctl mcp [tools|tool-info|postgres-reader-sql]")
		return errUsage
	}

	switch args[0] {
	case "tools":
		if len(args) != 1 {
			return errUsage
		}
		policy, err := mcp.LimitPolicyFromEnv(os.Getenv)
		if err != nil {
			return err
		}
		return writeJSONValue(a.out, mcp.ToolCatalogWithLimitPolicy(policy))
	case "tool-info":
		if len(args) != 2 {
			return errUsage
		}
		policy, err := mcp.LimitPolicyFromEnv(os.Getenv)
		if err != nil {
			return err
		}
		tool, ok := mcp.FindToolWithLimitPolicy(strings.TrimSpace(args[1]), policy)
		if !ok {
			return fmt.Errorf("unknown MCP tool %q", args[1])
		}
		return writeJSONValue(a.out, tool)
	case "postgres-reader-sql":
		return a.mcpPostgresReaderSQL(args[1:])
	default:
		fmt.Fprintf(a.err, "unknown mcp command %q\n", args[0])
		return errUsage
	}
}

func (a *app) mcpPostgresReaderSQL(args []string) error {
	flags := flag.NewFlagSet("gongctl mcp postgres-reader-sql", flag.ContinueOnError)
	flags.SetOutput(a.err)
	preset := flags.String("preset", "business-pilot", "MCP tool preset to generate scoped reader grants for")
	roleName := flags.String("role", "gongmcp_business_pilot_reader", "Postgres reader role name")
	databaseName := flags.String("database", "gongctl", "Postgres database name")
	createRole := flags.Bool("create-role", false, "Include a CREATE ROLE LOGIN statement without credentials")
	if err := flags.Parse(args); err != nil {
		return errUsage
	}
	if flags.NArg() != 0 {
		return errUsage
	}
	allowlist, err := mcp.ExpandToolPreset(*preset)
	if err != nil {
		return err
	}
	sql, err := postgres.BuildScopedReaderGrantSQL(postgres.ScopedReaderGrantSQLParams{
		Allowlist:    allowlist,
		RoleName:     *roleName,
		DatabaseName: *databaseName,
		Generator:    "gongctl mcp postgres-reader-sql",
		CreateRole:   *createRole,
	})
	if err != nil {
		return err
	}
	_, err = io.WriteString(a.out, sql)
	return err
}
