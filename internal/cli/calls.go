package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	checkpointstore "github.com/fyne-coder/gongcli_mcp/internal/checkpoint"
	exportjsonl "github.com/fyne-coder/gongcli_mcp/internal/export"
	"github.com/fyne-coder/gongcli_mcp/internal/gong"
)

func (a *app) calls(ctx context.Context, args []string) error {
	if len(args) == 0 {
		fmt.Fprintln(a.err, "usage: gongctl calls [list|export|show|transcript|transcript-batch]")
		return errUsage
	}

	switch args[0] {
	case "list":
		return a.callsList(ctx, args[1:])
	case "export":
		return a.callsExport(ctx, args[1:])
	case "show":
		return a.callsShow(ctx, args[1:])
	case "transcript":
		return a.callsTranscript(ctx, args[1:])
	case "transcript-batch":
		return a.callsTranscriptBatch(ctx, args[1:])
	default:
		fmt.Fprintf(a.err, "unknown calls command %q\n", args[0])
		return errUsage
	}
}

func (a *app) callsList(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("calls list", flag.ContinueOnError)
	fs.SetOutput(a.err)
	from := fs.String("from", "", "from date, YYYY-MM-DD or RFC3339")
	to := fs.String("to", "", "to date, YYYY-MM-DD or RFC3339")
	cursor := fs.String("cursor", "", "Gong pagination cursor")
	contextMode := fs.String("context", "none", "call context to include: none, extended")
	out := fs.String("out", "", "write response JSON to path")
	allowSensitiveExport := fs.Bool("allow-sensitive-export", false, "allow sensitive extended context in restricted mode")
	_ = fs.Bool("json", false, "print raw JSON response")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	contextSelector, err := parseCallContext(*contextMode)
	if err != nil {
		return err
	}
	if contextSelector != "" {
		if err := a.requireSensitiveExport("calls list --context extended", *allowSensitiveExport, "it can return live Gong call payloads with embedded CRM context"); err != nil {
			return err
		}
	}

	client, err := newClientFromEnv()
	if err != nil {
		return err
	}
	resp, err := client.ListCalls(ctx, gong.CallListParams{From: *from, To: *to, Cursor: *cursor, Context: contextSelector})
	if err != nil {
		return err
	}
	return writeOutput(*out, a.out, resp.Body)
}

func (a *app) callsExport(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("calls export", flag.ContinueOnError)
	fs.SetOutput(a.err)
	from := fs.String("from", "", "from date, YYYY-MM-DD or RFC3339")
	to := fs.String("to", "", "to date, YYYY-MM-DD or RFC3339")
	cursor := fs.String("cursor", "", "Gong pagination cursor")
	contextMode := fs.String("context", "none", "call context to include: none, extended")
	maxPages := fs.Int("max-pages", 0, "maximum pages to export; 0 means all pages")
	out := fs.String("out", "", "write JSONL to path or - for stdout")
	allowSensitiveExport := fs.Bool("allow-sensitive-export", false, "allow sensitive export in restricted mode")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	contextSelector, err := parseCallContext(*contextMode)
	if err != nil {
		return err
	}
	if *out == "" {
		return fmt.Errorf("--out is required")
	}
	if *maxPages < 0 {
		return fmt.Errorf("--max-pages must be >= 0")
	}
	if err := a.requireSensitiveExport("calls export", *allowSensitiveExport, "it can export raw call JSON and CRM context at scale"); err != nil {
		return err
	}

	client, err := newClientFromEnv()
	if err != nil {
		return err
	}

	writer, closeFn, err := outputWriter(*out, a.out)
	if err != nil {
		return err
	}
	defer closeFn()

	params := gong.CallListParams{From: *from, To: *to, Cursor: *cursor, Context: contextSelector}
	seenCursors := map[string]bool{}
	if params.Cursor != "" {
		seenCursors[params.Cursor] = true
	}

	total := 0
	pages := 0
	for {
		resp, err := client.ListCalls(ctx, params)
		if err != nil {
			return err
		}

		count, err := exportjsonl.WritePayloadAsJSONL(writer, resp.Body)
		if err != nil {
			return err
		}
		total += count
		pages++

		records, err := gong.PageRecordsFromBody(resp.Body)
		if err != nil {
			return err
		}
		if records.Cursor == "" {
			break
		}
		if *maxPages > 0 && pages >= *maxPages {
			fmt.Fprintf(a.err, "stopped after %d page(s) due to --max-pages\n", pages)
			break
		}
		if seenCursors[records.Cursor] {
			return fmt.Errorf("pagination cursor repeated after %d page(s)", pages)
		}
		seenCursors[records.Cursor] = true
		params.Cursor = records.Cursor
	}

	fmt.Fprintf(a.err, "wrote %d JSONL records from %d page(s) to %s\n", total, pages, *out)
	return nil
}

func (a *app) callsTranscript(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("calls transcript", flag.ContinueOnError)
	fs.SetOutput(a.err)
	callID := fs.String("call-id", "", "Gong call ID")
	out := fs.String("out", "", "write response JSON to path")
	allowSensitiveExport := fs.Bool("allow-sensitive-export", false, "allow transcript export in restricted mode")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	if *callID == "" {
		return fmt.Errorf("--call-id is required")
	}
	if err := a.requireSensitiveExport("calls transcript", *allowSensitiveExport, "it can return raw transcript payloads"); err != nil {
		return err
	}

	client, err := newClientFromEnv()
	if err != nil {
		return err
	}
	resp, err := client.GetTranscript(ctx, gong.TranscriptParams{CallIDs: []string{*callID}})
	if err != nil {
		return err
	}
	return writeOutput(*out, a.out, resp.Body)
}

func (a *app) callsShow(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("calls show", flag.ContinueOnError)
	fs.SetOutput(a.err)
	dbPath := fs.String("db", "", "SQLite database path")
	callID := fs.String("call-id", "", "stored Gong call ID")
	asJSON := fs.Bool("json", false, "print stored call JSON to stdout")
	allowSensitiveExport := fs.Bool("allow-sensitive-export", false, "allow raw call JSON in restricted mode")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	if *callID == "" {
		return fmt.Errorf("--call-id is required")
	}
	if !*asJSON {
		return fmt.Errorf("--json is required")
	}
	if err := a.requireSensitiveExport("calls show --json", *allowSensitiveExport, "it can print raw cached call JSON"); err != nil {
		return err
	}

	store, err := openSQLiteReadOnlyStore(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	raw, err := store.GetCallRaw(ctx, *callID)
	if err != nil {
		return err
	}
	return writeOutput("", a.out, raw)
}

func (a *app) callsTranscriptBatch(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("calls transcript-batch", flag.ContinueOnError)
	fs.SetOutput(a.err)
	idsFile := fs.String("ids-file", "", "file with one call ID per line")
	outDir := fs.String("out-dir", "", "directory for transcript JSON files")
	resume := fs.Bool("resume", false, "skip completed calls from checkpoint or existing files")
	allowSensitiveExport := fs.Bool("allow-sensitive-export", false, "allow transcript export in restricted mode")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	if *idsFile == "" {
		return fmt.Errorf("--ids-file is required")
	}
	if *outDir == "" {
		return fmt.Errorf("--out-dir is required")
	}
	if err := a.requireSensitiveExport("calls transcript-batch", *allowSensitiveExport, "it can write raw transcript files in bulk"); err != nil {
		return err
	}

	ids, err := readIDs(*idsFile)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		return err
	}

	store, err := checkpointstore.Open(filepath.Join(*outDir, ".gongctl-checkpoint.jsonl"))
	if err != nil {
		return err
	}
	client, err := newClientFromEnv()
	if err != nil {
		return err
	}

	failures := 0
	for _, id := range ids {
		path := filepath.Join(*outDir, safeFilename(id)+".json")
		if *resume {
			skip, err := canSkipTranscriptResume(store, id, path)
			if err != nil {
				return err
			}
			if skip {
				fmt.Fprintf(a.err, "skip %s\n", id)
				continue
			}
		}

		resp, err := client.GetTranscript(ctx, gong.TranscriptParams{CallIDs: []string{id}})
		if err != nil {
			failures++
			_ = store.Mark(checkpointstore.Entry{ID: id, Status: "error", Error: err.Error()})
			fmt.Fprintf(a.err, "error %s: %v\n", id, err)
			continue
		}
		if err := writeJSONFileAtomic(path, resp.Body); err != nil {
			failures++
			_ = store.Mark(checkpointstore.Entry{ID: id, Status: "error", Error: err.Error()})
			fmt.Fprintf(a.err, "error %s: %v\n", id, err)
			continue
		}
		if err := store.Mark(checkpointstore.Entry{ID: id, Status: "done", Path: path}); err != nil {
			return err
		}
		fmt.Fprintf(a.err, "done %s -> %s\n", id, path)
	}

	if failures > 0 {
		return fmt.Errorf("transcript batch completed with %d failures", failures)
	}
	return nil
}

func canSkipTranscriptResume(store *checkpointstore.Store, id string, path string) (bool, error) {
	valid, err := validJSONFile(path)
	if err != nil {
		return false, err
	}
	if !valid {
		return false, nil
	}
	return store.Done(id) || valid, nil
}

func readIDs(path string) ([]string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		ids = append(ids, line)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("%s did not contain any call IDs", path)
	}
	return ids, nil
}

func parseCallContext(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "none":
		return "", nil
	case "extended":
		return "Extended", nil
	default:
		return "", fmt.Errorf("--context must be one of: none, extended")
	}
}

func safeFilename(value string) string {
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_")
	return replacer.Replace(value)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
