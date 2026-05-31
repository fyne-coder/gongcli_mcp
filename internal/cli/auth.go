package cli

import (
	"context"
	"fmt"
)

func (a *app) auth(ctx context.Context, args []string) error {
	if len(args) == 0 {
		fmt.Fprintln(a.err, "usage: gongctl auth check")
		return errUsage
	}

	switch args[0] {
	case "check":
		if len(args) != 1 {
			return fmt.Errorf("auth check does not accept arguments")
		}
		client, err := newClientFromEnv()
		if err != nil {
			return err
		}
		resp, err := client.Raw(ctx, "GET", "/v2/users", nil)
		if err != nil {
			return err
		}
		fmt.Fprintf(a.out, "ok: Gong API accepted credentials, status=%d\n", resp.StatusCode)
		return nil
	default:
		fmt.Fprintf(a.err, "unknown auth command %q\n", args[0])
		return errUsage
	}
}
