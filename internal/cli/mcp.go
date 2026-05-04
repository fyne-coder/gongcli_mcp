package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/fyne-coder/gongcli_mcp/internal/mcp"
)

func (a *app) mcp(ctx context.Context, args []string) error {
	_ = ctx
	if len(args) == 0 {
		fmt.Fprintln(a.err, "usage: gongctl mcp [tools|tool-info]")
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
	default:
		fmt.Fprintf(a.err, "unknown mcp command %q\n", args[0])
		return errUsage
	}
}
