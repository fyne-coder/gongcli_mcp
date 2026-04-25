package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
)

func (a *app) api(ctx context.Context, args []string) error {
	if len(args) == 0 {
		fmt.Fprintln(a.err, "usage: gongctl api raw METHOD PATH [--body body.json] [--out response.json]")
		return errUsage
	}

	switch args[0] {
	case "raw":
		return a.apiRaw(ctx, args[1:])
	default:
		fmt.Fprintf(a.err, "unknown api command %q\n", args[0])
		return errUsage
	}
}

func (a *app) apiRaw(ctx context.Context, args []string) error {
	method, path, bodySpec, out, err := parseRawArgs(args)
	if err != nil {
		return err
	}

	var body []byte
	if bodySpec != "" {
		body, err = readBody(bodySpec)
		if err != nil {
			return err
		}
	}

	client, err := newClientFromEnv()
	if err != nil {
		return err
	}
	resp, err := client.Raw(ctx, method, path, body)
	if err != nil {
		return err
	}
	return writeOutput(out, a.out, resp.Body)
}

func parseRawArgs(args []string) (string, string, string, string, error) {
	var positionals []string
	var body string
	var out string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--body":
			i++
			if i >= len(args) {
				return "", "", "", "", fmt.Errorf("--body requires a value")
			}
			body = args[i]
		case "--out":
			i++
			if i >= len(args) {
				return "", "", "", "", fmt.Errorf("--out requires a value")
			}
			out = args[i]
		case "-h", "--help":
			return "", "", "", "", errUsage
		default:
			if strings.HasPrefix(arg, "-") {
				return "", "", "", "", fmt.Errorf("unknown api raw flag %q", arg)
			}
			positionals = append(positionals, arg)
		}
	}

	if len(positionals) != 2 {
		return "", "", "", "", fmt.Errorf("api raw requires METHOD and PATH")
	}
	return positionals[0], positionals[1], body, out, nil
}

func readBody(spec string) ([]byte, error) {
	if spec == "-" {
		return os.ReadFile("/dev/stdin")
	}
	if strings.HasPrefix(spec, "@") {
		return os.ReadFile(strings.TrimPrefix(spec, "@"))
	}
	if strings.HasPrefix(strings.TrimSpace(spec), "{") || strings.HasPrefix(strings.TrimSpace(spec), "[") {
		return []byte(spec), nil
	}
	return os.ReadFile(spec)
}
