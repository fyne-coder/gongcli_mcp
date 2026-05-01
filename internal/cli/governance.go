package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"strings"

	"github.com/fyne-coder/gongcli_mcp/internal/governance"
	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
)

func (a *app) governance(ctx context.Context, args []string) error {
	if len(args) == 0 {
		fmt.Fprintln(a.err, "usage: gongctl governance [audit|export-filtered-db]")
		return errUsage
	}
	switch args[0] {
	case "audit":
		return a.governanceAudit(ctx, args[1:])
	case "export-filtered-db":
		return a.governanceExportFilteredDB(ctx, args[1:])
	default:
		fmt.Fprintf(a.err, "unknown governance command %q\n", args[0])
		return errUsage
	}
}

func (a *app) governanceAudit(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("governance audit", flag.ContinueOnError)
	fs.SetOutput(a.err)
	dbPath := fs.String("db", "", "Path to local gongctl SQLite cache")
	configPath := fs.String("config", "", "AI governance YAML config path")
	jsonOutput := fs.Bool("json", false, "write JSON audit output")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	if strings.TrimSpace(*dbPath) == "" {
		return fmt.Errorf("--db is required")
	}
	if strings.TrimSpace(*configPath) == "" {
		return fmt.Errorf("--config is required")
	}

	cfg, err := governance.LoadFile(*configPath)
	if err != nil {
		return err
	}
	store, err := sqlite.OpenReadOnly(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	audit, err := governance.BuildAudit(ctx, store, cfg)
	if err != nil {
		return err
	}
	if *jsonOutput {
		encoder := json.NewEncoder(a.out)
		encoder.SetIndent("", "  ")
		return encoder.Encode(audit)
	}

	fmt.Fprintf(a.out, "governance audit: entries=%d aliases=%d candidates=%d matched=%d unmatched=%d suppressed_calls=%d\n",
		audit.ConfigEntries,
		audit.ConfigAliases,
		audit.CandidateValues,
		len(audit.MatchedEntries),
		len(audit.UnmatchedEntries),
		audit.SuppressedCallCount,
	)
	if len(audit.MatchedEntries) > 0 {
		fmt.Fprintln(a.out, "matched entries:")
		for _, match := range audit.MatchedEntries {
			label := match.Name
			if strings.TrimSpace(match.Alias) != "" {
				label += " alias=" + match.Alias
			}
			fmt.Fprintf(a.out, "- list=%s name=%s normalized=%s calls=%d\n", match.List, label, match.Normalized, match.CallCount)
		}
	}
	if len(audit.UnmatchedEntries) > 0 {
		fmt.Fprintln(a.out, "unmatched entries:")
		for _, target := range audit.UnmatchedEntries {
			label := target.Name
			if strings.TrimSpace(target.Alias) != "" {
				label += " alias=" + target.Alias
			}
			fmt.Fprintf(a.out, "- list=%s name=%s normalized=%s\n", target.List, label, target.Normalized)
		}
	}
	return nil
}

func (a *app) governanceExportFilteredDB(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("governance export-filtered-db", flag.ContinueOnError)
	fs.SetOutput(a.err)
	dbPath := fs.String("db", "", "Path to source gongctl SQLite cache")
	configPath := fs.String("config", "", "AI governance YAML config path")
	outPath := fs.String("out", "", "Path to write the filtered MCP SQLite cache")
	overwrite := fs.Bool("overwrite", false, "replace an existing output DB")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	if strings.TrimSpace(*dbPath) == "" {
		return fmt.Errorf("--db is required")
	}
	if strings.TrimSpace(*configPath) == "" {
		return fmt.Errorf("--config is required")
	}
	if strings.TrimSpace(*outPath) == "" {
		return fmt.Errorf("--out is required")
	}

	cfg, err := governance.LoadFile(*configPath)
	if err != nil {
		return err
	}
	store, err := sqlite.OpenReadOnly(ctx, *dbPath)
	if err != nil {
		return err
	}
	audit, err := governance.BuildAudit(ctx, store, cfg)
	closeErr := store.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}

	plan, err := sqlite.ExportGovernanceFilteredDB(ctx, *dbPath, *outPath, audit.SuppressedCallIDs, *overwrite)
	if err != nil {
		return err
	}
	response := governanceExportFilteredDBResponse{
		SourceDBPath:                  plan.SourceDBPath,
		OutputDBPath:                  plan.OutputDBPath,
		ConfigEntries:                 audit.ConfigEntries,
		ConfigAliases:                 audit.ConfigAliases,
		MatchedEntries:                len(audit.MatchedEntries),
		UnmatchedEntries:              len(audit.UnmatchedEntries),
		SuppressedCallCount:           audit.SuppressedCallCount,
		DeletedCalls:                  plan.DeletedCalls,
		DeletedTranscripts:            plan.DeletedTranscripts,
		DeletedTranscriptSegments:     plan.DeletedTranscriptSegments,
		DeletedContextObjects:         plan.DeletedContextObjects,
		DeletedContextFields:          plan.DeletedContextFields,
		DeletedProfileCallFactRows:    plan.DeletedProfileCallFactRows,
		RemainingSuppressedCandidates: plan.RemainingSuppressedCandidates,
		SensitiveDataWarning:          cacheSensitiveDataWarning,
	}
	return writeJSONValue(a.out, response)
}

type governanceExportFilteredDBResponse struct {
	SourceDBPath                  string `json:"source_db_path"`
	OutputDBPath                  string `json:"output_db_path"`
	ConfigEntries                 int    `json:"config_entries"`
	ConfigAliases                 int    `json:"config_aliases"`
	MatchedEntries                int    `json:"matched_entries"`
	UnmatchedEntries              int    `json:"unmatched_entries"`
	SuppressedCallCount           int    `json:"suppressed_call_count"`
	DeletedCalls                  int64  `json:"deleted_calls"`
	DeletedTranscripts            int64  `json:"deleted_transcripts"`
	DeletedTranscriptSegments     int64  `json:"deleted_transcript_segments"`
	DeletedContextObjects         int64  `json:"deleted_context_objects"`
	DeletedContextFields          int64  `json:"deleted_context_fields"`
	DeletedProfileCallFactRows    int64  `json:"deleted_profile_call_fact_rows"`
	RemainingSuppressedCandidates int64  `json:"remaining_suppressed_candidates"`
	SensitiveDataWarning          string `json:"sensitive_data_warning"`
}
