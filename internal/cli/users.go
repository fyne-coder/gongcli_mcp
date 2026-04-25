package cli

import (
	"context"
	"flag"
	"fmt"

	"github.com/arthurlee/gongctl/internal/gong"
)

func (a *app) users(ctx context.Context, args []string) error {
	if len(args) == 0 {
		fmt.Fprintln(a.err, "usage: gongctl users list")
		return errUsage
	}

	switch args[0] {
	case "list":
		return a.usersList(ctx, args[1:])
	default:
		fmt.Fprintf(a.err, "unknown users command %q\n", args[0])
		return errUsage
	}
}

func (a *app) usersList(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("users list", flag.ContinueOnError)
	fs.SetOutput(a.err)
	cursor := fs.String("cursor", "", "Gong pagination cursor")
	limit := fs.Int("limit", 0, "page size, only sent when non-zero")
	out := fs.String("out", "", "write response JSON to path")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}

	client, err := newClientFromEnv()
	if err != nil {
		return err
	}
	resp, err := client.ListUsers(ctx, gong.UserListParams{Cursor: *cursor, Limit: *limit})
	if err != nil {
		return err
	}
	return writeOutput(*out, a.out, resp.Body)
}
