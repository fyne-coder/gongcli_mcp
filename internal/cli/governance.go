package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/fyne-coder/gongcli_mcp/internal/governance"
	"github.com/fyne-coder/gongcli_mcp/internal/store/postgres"
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
	applyPostgresPolicy := fs.Bool("apply-postgres-policy", false, "persist this audit as the active Postgres MCP governance policy")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	if strings.TrimSpace(*configPath) == "" {
		return fmt.Errorf("--config is required")
	}

	cfg, err := governance.LoadFile(*configPath)
	if err != nil {
		return err
	}

	backend := "sqlite"
	var candidateStore governance.CandidateStore
	var buildAndSavePostgresPolicy func() (*governance.Audit, *postgres.GovernancePolicyState, error)
	var closeStore func() error
	if strings.TrimSpace(*dbPath) != "" {
		store, err := sqlite.OpenReadOnly(ctx, *dbPath)
		if err != nil {
			return err
		}
		candidateStore = store
		closeStore = store.Close
	} else if databaseURL := postgres.URLFromEnv(os.Getenv); strings.TrimSpace(databaseURL) != "" {
		backend = "postgres"
		store, err := postgres.Open(ctx, databaseURL)
		if err != nil {
			return err
		}
		candidateStore = store
		closeStore = store.Close
		buildAndSavePostgresPolicy = func() (*governance.Audit, *postgres.GovernancePolicyState, error) {
			return store.BuildAndSaveGovernancePolicy(ctx, cfg.Fingerprint(), cfg)
		}
	} else {
		return fmt.Errorf("--db is required unless GONG_DATABASE_URL or DATABASE_URL is set")
	}
	defer closeStore()

	var policy *postgres.GovernancePolicyState
	var audit *governance.Audit
	if *applyPostgresPolicy {
		if buildAndSavePostgresPolicy == nil {
			return fmt.Errorf("--apply-postgres-policy requires GONG_DATABASE_URL or DATABASE_URL")
		}
		audit, policy, err = buildAndSavePostgresPolicy()
		if err != nil {
			return err
		}
	} else {
		audit, err = governance.BuildAudit(ctx, candidateStore, cfg)
		if err != nil {
			return err
		}
	}
	if *jsonOutput {
		encoder := json.NewEncoder(a.out)
		encoder.SetIndent("", "  ")
		if policy != nil {
			return encoder.Encode(governanceAuditResponse{
				Audit:                 audit,
				Backend:               backend,
				ConfigSHA256:          cfg.Fingerprint(),
				PostgresPolicyApplied: true,
				PostgresPolicy:        policy,
			})
		}
		return encoder.Encode(audit)
	}

	fmt.Fprintf(a.out, "governance audit: backend=%s entries=%d aliases=%d candidates=%d matched=%d unmatched=%d suppressed_calls=%d\n",
		backend,
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
	if policy != nil {
		fmt.Fprintf(a.out, "postgres policy applied: config_sha256=%s data_fingerprint=%s suppressed_calls=%d\n",
			policy.ConfigSHA256,
			policy.DataFingerprint,
			policy.SuppressedCallCount,
		)
	}
	return nil
}

type governanceAuditResponse struct {
	Audit                 *governance.Audit               `json:"audit"`
	Backend               string                          `json:"backend"`
	ConfigSHA256          string                          `json:"config_sha256"`
	PostgresPolicyApplied bool                            `json:"postgres_policy_applied"`
	PostgresPolicy        *postgres.GovernancePolicyState `json:"postgres_policy,omitempty"`
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
