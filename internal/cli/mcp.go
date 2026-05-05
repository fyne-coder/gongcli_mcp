package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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
		fmt.Fprintln(a.err, "usage: gongctl mcp [tools|tool-info|postgres-reader-sql|postgres-reader-apply]")
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
	case "postgres-reader-apply":
		return a.mcpPostgresReaderApply(ctx, args[1:])
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
	})
	if err != nil {
		return err
	}
	_, err = io.WriteString(a.out, sql)
	return err
}

var mcpApplyScopedReaderGrants = postgres.ApplyScopedReaderGrants

type postgresReaderApplyResponse struct {
	Backend        string `json:"backend"`
	Preset         string `json:"preset"`
	Role           string `json:"role"`
	Database       string `json:"database"`
	Status         string `json:"status"`
	Applied        bool   `json:"applied"`
	SQLSHA256      string `json:"sql_sha256"`
	CredentialNote string `json:"credential_note"`
	RoleNote       string `json:"role_note"`
}

func (a *app) mcpPostgresReaderApply(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("gongctl mcp postgres-reader-apply", flag.ContinueOnError)
	flags.SetOutput(a.err)
	preset := flags.String("preset", "business-pilot", "MCP tool preset to reconcile scoped reader grants for")
	roleName := flags.String("role", "gongmcp_business_pilot_reader", "existing Postgres reader role name")
	databaseName := flags.String("database", "gongctl", "Postgres database name")
	apply := flags.Bool("apply", false, "apply grants using GONG_DATABASE_URL or DATABASE_URL")
	dryRun := flags.Bool("dry-run", false, "print grant SQL without connecting to Postgres")
	if err := flags.Parse(args); err != nil {
		return errUsage
	}
	if flags.NArg() != 0 {
		return errUsage
	}
	if *apply && *dryRun {
		return errors.New("--apply and --dry-run cannot be used together")
	}
	allowlist, err := mcp.ExpandToolPreset(*preset)
	if err != nil {
		return err
	}
	params := postgres.ScopedReaderGrantSQLParams{
		Allowlist:    allowlist,
		RoleName:     *roleName,
		DatabaseName: *databaseName,
		Generator:    "gongctl mcp postgres-reader-apply",
	}
	sql, err := postgres.BuildScopedReaderGrantSQL(params)
	if err != nil {
		return err
	}
	if !*apply {
		_, err := io.WriteString(a.out, sql)
		return err
	}
	databaseURL := postgres.URLFromEnv(os.Getenv)
	if strings.TrimSpace(databaseURL) == "" {
		return errors.New("GONG_DATABASE_URL or DATABASE_URL is required with --apply")
	}
	appliedSQL, err := mcpApplyScopedReaderGrants(ctx, databaseURL, params)
	if err != nil {
		return fmt.Errorf("apply scoped Postgres reader grants failed; verify the writable Postgres URL, network path, role/database names, and operator privileges")
	}
	sum := sha256.Sum256([]byte(appliedSQL))
	return writeJSONValue(a.out, postgresReaderApplyResponse{
		Backend:        "postgres",
		Preset:         *preset,
		Role:           *roleName,
		Database:       *databaseName,
		Status:         "applied",
		Applied:        true,
		SQLSHA256:      hex.EncodeToString(sum[:]),
		CredentialNote: "database_url_not_exported",
		RoleNote:       "existing_role_only_passwords_managed_externally",
	})
}
