package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/arthurlee/gongctl/internal/auth"
	"github.com/arthurlee/gongctl/internal/gong"
	"github.com/arthurlee/gongctl/internal/ratelimit"
	"github.com/arthurlee/gongctl/internal/redact"
)

var errUsage = errors.New("usage")

const defaultHTTPTimeout = 30 * time.Second

type app struct {
	out io.Writer
	err io.Writer
}

func Run(ctx context.Context, args []string, out io.Writer, errOut io.Writer) int {
	a := &app{out: out, err: errOut}
	if err := a.run(ctx, args); err != nil {
		if errors.Is(err, errUsage) {
			return 2
		}
		fmt.Fprintln(errOut, "error:", err)
		return 1
	}
	return 0
}

func (a *app) run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		a.usage()
		return errUsage
	}

	switch args[0] {
	case "help", "-h", "--help":
		a.usage()
		return nil
	case "analyze":
		return a.analyze(ctx, args[1:])
	case "auth":
		return a.auth(ctx, args[1:])
	case "calls":
		return a.calls(ctx, args[1:])
	case "search":
		return a.search(ctx, args[1:])
	case "sync":
		return a.sync(ctx, args[1:])
	case "mcp":
		return a.mcp(ctx, args[1:])
	case "profile":
		return a.profile(ctx, args[1:])
	case "users":
		return a.users(ctx, args[1:])
	case "api":
		return a.api(ctx, args[1:])
	case "diagnose":
		return a.diagnose(ctx, args[1:])
	default:
		fmt.Fprintf(a.err, "unknown command %q\n", args[0])
		a.usage()
		return errUsage
	}
}

func (a *app) usage() {
	fmt.Fprint(a.err, `Usage:
  gongctl analyze calls --db gong.db --group-by lifecycle [--lifecycle-source auto|profile|builtin] [--limit N]
  gongctl analyze coverage --db gong.db [--lifecycle-source auto|profile|builtin]
  gongctl analyze transcript-backlog --db gong.db [--lifecycle-source auto|profile|builtin] [--lifecycle BUCKET] [--limit N]
  gongctl analyze crm-schema --db gong.db [--integration-id ID] [--object-type TYPE]
  gongctl analyze settings --db gong.db [--kind trackers|scorecards|workspaces]
  gongctl analyze scorecards --db gong.db [--active-only]
  gongctl analyze scorecard --db gong.db --scorecard-id ID
  gongctl auth check
  gongctl profile discover --db gong.db --out gongctl-profile.yaml
  gongctl profile validate --db gong.db --profile gongctl-profile.yaml
  gongctl profile import --db gong.db --profile gongctl-profile.yaml
  gongctl profile show --db gong.db [--format json|yaml]
  gongctl sync calls --db gong.db --from YYYY-MM-DD --to YYYY-MM-DD --preset business|minimal|all [--max-pages N]
  gongctl sync users --db gong.db [--max-pages N]
  gongctl sync transcripts --db gong.db --out-dir transcripts [--limit N]
  gongctl sync crm-integrations --db gong.db
  gongctl sync crm-schema --db gong.db --integration-id ID --object-type ACCOUNT --object-type DEAL
  gongctl sync settings --db gong.db --kind trackers|scorecards|workspaces [--workspace-id ID]
  gongctl sync status --db gong.db
  gongctl mcp tools
  gongctl mcp tool-info NAME
  gongctl search transcripts --db gong.db --query TEXT [--limit N]
  gongctl search calls --db gong.db [--crm-object-type TYPE] [--crm-object-id ID] [--limit N]
  gongctl calls list --from YYYY-MM-DD --to YYYY-MM-DD [--out calls.json]
  gongctl calls export --from YYYY-MM-DD --to YYYY-MM-DD --out calls.jsonl
  gongctl calls show --db gong.db --call-id CALL_ID --json
  gongctl calls transcript --call-id CALL_ID [--out transcript.json]
  gongctl calls transcript-batch --ids-file call_ids.txt --out-dir transcripts --resume
  gongctl users list
  gongctl api raw METHOD PATH [--body body.json] [--out response.json]
  gongctl diagnose [--live]
`)
}

func newClientFromEnv() (*gong.Client, error) {
	creds, err := auth.LoadFromEnv()
	if err != nil {
		return nil, err
	}
	baseURL := os.Getenv("GONG_BASE_URL")
	return gong.NewClient(gong.Options{
		BaseURL:     baseURL,
		Credentials: creds,
		HTTPClient:  newCLIHTTPClient(),
		Limiter:     ratelimit.New(3, time.Second),
		MaxRetries:  4,
	})
}

func (a *app) diagnose(ctx context.Context, args []string) error {
	live, err := parseDiagnoseArgs(args)
	if err != nil {
		return err
	}
	if err := auth.ApplyDotEnv(".env"); err != nil {
		return err
	}

	baseURL := os.Getenv("GONG_BASE_URL")
	if baseURL == "" {
		baseURL = gong.DefaultBaseURL
	}

	key := os.Getenv("GONG_ACCESS_KEY")
	secret := os.Getenv("GONG_ACCESS_KEY_SECRET")

	fmt.Fprintf(a.out, "base_url: %s\n", baseURL)
	fmt.Fprintf(a.out, "access_key: %s\n", redact.Secret(key))
	fmt.Fprintf(a.out, "access_key_secret_present: %t\n", secret != "")
	fmt.Fprintln(a.out, "rate_limit: 3 requests/second")
	fmt.Fprintln(a.out, "retry_policy: Retry-After, then exponential backoff up to 30s")
	fmt.Fprintf(a.out, "go_runtime: %s\n", runtime.Version())

	if live {
		client, err := newClientFromEnv()
		if err != nil {
			return err
		}
		resp, err := client.Raw(ctx, "GET", "/v2/users", nil)
		if err != nil {
			return err
		}
		fmt.Fprintf(a.out, "live_auth_check: status=%d\n", resp.StatusCode)
	}
	return nil
}

func parseDiagnoseArgs(args []string) (bool, error) {
	live := false
	for _, arg := range args {
		switch arg {
		case "--live":
			live = true
		case "-h", "--help":
			return false, errUsage
		default:
			return false, fmt.Errorf("unknown diagnose flag %q", arg)
		}
	}
	return live, nil
}

func newCLIHTTPClient() *http.Client {
	return &http.Client{Timeout: defaultHTTPTimeout}
}
