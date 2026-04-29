package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/fyne-coder/gongcli_mcp/internal/auth"
	"github.com/fyne-coder/gongcli_mcp/internal/gong"
	"github.com/fyne-coder/gongcli_mcp/internal/ratelimit"
	"github.com/fyne-coder/gongcli_mcp/internal/redact"
	"github.com/fyne-coder/gongcli_mcp/internal/version"
)

var errUsage = errors.New("usage")

const defaultHTTPTimeout = 30 * time.Second

const (
	restrictedEnvVar           = "GONGCTL_RESTRICTED"
	allowSensitiveExportEnvVar = "GONGCTL_ALLOW_SENSITIVE_EXPORT"
)

type app struct {
	out                  io.Writer
	err                  io.Writer
	restricted           bool
	allowSensitiveExport bool
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
	opts, args := parseGlobalCLIOptions(args)
	a.restricted = envEnabled(restrictedEnvVar) || opts.restricted
	a.allowSensitiveExport = envEnabled(allowSensitiveExportEnvVar) || opts.allowSensitiveExport

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
	case "cache":
		return a.cache(ctx, args[1:])
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
	case "version":
		return a.version(ctx, args[1:])
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
  gongctl [--restricted] [--allow-sensitive-export] COMMAND ...
  gongctl analyze calls --db gong.db --group-by lifecycle [--lifecycle-source auto|profile|builtin] [--limit N]
  gongctl analyze coverage --db gong.db [--lifecycle-source auto|profile|builtin]
  gongctl analyze transcript-backlog --db gong.db [--lifecycle-source auto|profile|builtin] [--lifecycle BUCKET] [--limit N]
  gongctl analyze crm-schema --db gong.db [--integration-id ID] [--object-type TYPE]
  gongctl analyze settings --db gong.db [--kind trackers|scorecards|workspaces]
  gongctl analyze scorecards --db gong.db [--active-only]
  gongctl analyze scorecard --db gong.db --scorecard-id ID
  gongctl auth check
  gongctl cache inventory --db gong.db
  gongctl cache purge --db gong.db --older-than YYYY-MM-DD [--dry-run|--confirm]
  gongctl profile discover --db gong.db --out gongctl-profile.yaml
  gongctl profile validate --db gong.db --profile gongctl-profile.yaml
  gongctl profile import --db gong.db --profile gongctl-profile.yaml
  gongctl profile show --db gong.db [--format json|yaml]
  gongctl sync run --config company-sync.yaml [--dry-run]
  gongctl sync calls --db gong.db --from YYYY-MM-DD --to YYYY-MM-DD --preset business|minimal|all [--max-pages N] [--allow-sensitive-export]
  gongctl sync users --db gong.db [--max-pages N]
  gongctl sync transcripts --db gong.db --out-dir transcripts [--limit N] [--batch-size N] [--allow-sensitive-export]
  gongctl sync crm-integrations --db gong.db
  gongctl sync crm-schema --db gong.db --integration-id ID --object-type ACCOUNT --object-type DEAL
  gongctl sync settings --db gong.db --kind trackers|scorecards|workspaces [--workspace-id ID]
  gongctl sync status --db gong.db
  gongctl mcp tools
  gongctl mcp tool-info NAME
  gongctl search transcripts --db gong.db --query TEXT [--limit N]
  gongctl search calls --db gong.db [--crm-object-type TYPE] [--crm-object-id ID] [--limit N]
  gongctl calls list --from YYYY-MM-DD --to YYYY-MM-DD [--context none|extended] [--out calls.json] [--allow-sensitive-export]
  gongctl calls export --from YYYY-MM-DD --to YYYY-MM-DD --out calls.jsonl [--allow-sensitive-export]
  gongctl calls show --db gong.db --call-id CALL_ID --json [--allow-sensitive-export]
  gongctl calls transcript --call-id CALL_ID [--out transcript.json] [--allow-sensitive-export]
  gongctl calls transcript-batch --ids-file call_ids.txt --out-dir transcripts --resume [--allow-sensitive-export]
  gongctl users list
  gongctl version
  gongctl api raw METHOD PATH [--body body.json] [--out response.json] [--allow-sensitive-export]
  gongctl diagnose [--live]
`)
}

func (a *app) version(ctx context.Context, args []string) error {
	_ = ctx
	if len(args) != 0 {
		return fmt.Errorf("unexpected version arguments: %v", args)
	}
	info := version.Current()
	fmt.Fprintf(a.out, "version: %s\n", info.Version)
	fmt.Fprintf(a.out, "commit: %s\n", info.Commit)
	fmt.Fprintf(a.out, "date: %s\n", info.Date)
	return nil
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

type globalCLIOptions struct {
	restricted           bool
	allowSensitiveExport bool
}

func parseGlobalCLIOptions(args []string) (globalCLIOptions, []string) {
	var opts globalCLIOptions
	for len(args) > 0 {
		switch args[0] {
		case "--restricted":
			opts.restricted = true
			args = args[1:]
		case "--allow-sensitive-export":
			opts.allowSensitiveExport = true
			args = args[1:]
		default:
			return opts, args
		}
	}
	return opts, args
}

func envEnabled(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func (a *app) requireSensitiveExport(command string, localOverride bool, detail string) error {
	if !a.restricted {
		return nil
	}
	if a.allowSensitiveExport || localOverride {
		return nil
	}
	if strings.TrimSpace(detail) == "" {
		detail = "it can expose sensitive tenant data"
	}
	return fmt.Errorf(
		"%s is blocked because restricted mode is enabled; %s. Re-run with --allow-sensitive-export or set %s=1 if you have operator approval",
		command,
		detail,
		allowSensitiveExportEnvVar,
	)
}
