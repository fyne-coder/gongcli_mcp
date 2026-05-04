package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fyne-coder/gongcli_mcp/internal/governance"
	"github.com/fyne-coder/gongcli_mcp/internal/mcp"
	"github.com/fyne-coder/gongcli_mcp/internal/mcphttp"
	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
	"github.com/fyne-coder/gongcli_mcp/internal/version"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("gongmcp", flag.ContinueOnError)
	flags.SetOutput(stderr)

	dbPath := flags.String("db", "", "Path to the local gongctl SQLite cache")
	transcriptEvidenceProvenance := flags.String("transcript-evidence-provenance", envDefault("GONGMCP_TRANSCRIPT_EVIDENCE_PROVENANCE", "redacted"), "Transcript evidence provenance mode: redacted, alias, or raw")
	toolAllowlist := flags.String("tool-allowlist", "", "Comma-separated MCP tool allowlist; defaults to GONGMCP_TOOL_ALLOWLIST when no tool preset is set; one of preset or allowlist is required for HTTP")
	toolPreset := flags.String("tool-preset", "", "Named MCP tool preset: business-pilot, operator-smoke, analyst, governance-search, all-readonly; defaults to GONGMCP_TOOL_PRESET")
	listToolPresets := flags.Bool("list-tool-presets", false, "List built-in MCP tool presets as JSON and exit")
	httpAddr := flags.String("http", "", "Optional HTTP listen address for /mcp; defaults to GONGMCP_HTTP_ADDR")
	forceStdio := flags.Bool("stdio", false, "Force stdio transport and ignore GONGMCP_HTTP_ADDR")
	authMode := flags.String("auth-mode", "", "HTTP auth mode: none or bearer; defaults to GONGMCP_AUTH_MODE or bearer")
	bearerToken := flags.String("bearer-token", "", "Bearer token for HTTP auth; defaults to GONGMCP_BEARER_TOKEN")
	bearerTokenFile := flags.String("bearer-token-file", "", "Path to bearer token file; defaults to GONGMCP_BEARER_TOKEN_FILE")
	bearerTokenPreviousFile := flags.String("bearer-token-previous-file", "", "Optional previous bearer token file accepted during rotation; defaults to GONGMCP_BEARER_TOKEN_PREVIOUS_FILE")
	allowOpenNetwork := flags.Bool("allow-open-network", false, "Allow non-local HTTP bind addresses; can also be enabled with GONGMCP_ALLOW_OPEN_NETWORK=1")
	devAllowNoAuthLocalhost := flags.Bool("dev-allow-no-auth-localhost", false, "Allow unauthenticated HTTP only on localhost for local development")
	allowedOrigins := flags.String("allowed-origins", "", "Comma-separated allowed HTTP Origin values; defaults to GONGMCP_ALLOWED_ORIGINS; required for non-local HTTP")
	aiGovernanceConfig := flags.String("ai-governance-config", "", "AI governance YAML config path; defaults to GONGMCP_AI_GOVERNANCE_CONFIG")
	allowUnmatchedAIGovernance := flags.Bool("allow-unmatched-ai-governance", false, "Allow AI governance config entries that do not match the current cache; defaults to GONGMCP_ALLOW_UNMATCHED_AI_GOVERNANCE when set")
	maxSearchResults := flags.Int("max-search-results", 0, "Maximum rows for MCP search-style tools; defaults to GONGMCP_MAX_SEARCH_RESULTS or 100, hard-capped at 1000")
	maxCRMFields := flags.Int("max-crm-fields", 0, "Maximum rows for MCP CRM field inventory tools; defaults to GONGMCP_MAX_CRM_FIELDS or 200, hard-capped at 1000")
	maxLateStageSignals := flags.Int("max-late-stage-signals", 0, "Maximum rows for late-stage signal analysis; defaults to GONGMCP_MAX_LATE_STAGE_SIGNALS or 100, hard-capped at 500")
	maxMissingTranscripts := flags.Int("max-missing-transcripts", 0, "Maximum rows for missing transcript tools; defaults to GONGMCP_MAX_MISSING_TRANSCRIPTS or 500, hard-capped at 10000")
	maxOpportunitySummaries := flags.Int("max-opportunity-summaries", 0, "Maximum rows for Opportunity summary tools; defaults to GONGMCP_MAX_OPPORTUNITY_SUMMARIES or 100, hard-capped at 1000")
	maxCRMMatrixCells := flags.Int("max-crm-matrix-cells", 0, "Maximum cells for CRM matrix tools; defaults to GONGMCP_MAX_CRM_MATRIX_CELLS or 200, hard-capped at 1000")
	maxLifecycleResults := flags.Int("max-lifecycle-results", 0, "Maximum rows for lifecycle and backlog tools; defaults to GONGMCP_MAX_LIFECYCLE_RESULTS or 100, hard-capped at 1000")
	maxLifecycleCRMFields := flags.Int("max-lifecycle-crm-fields", 0, "Maximum rows for lifecycle CRM comparison tools; defaults to GONGMCP_MAX_LIFECYCLE_CRM_FIELDS or 200, hard-capped at 1000")
	maxCallFactGroups := flags.Int("max-call-fact-groups", 0, "Maximum rows for call-fact aggregate tools; defaults to GONGMCP_MAX_CALL_FACT_GROUPS or 200, hard-capped at 1000")
	maxInventoryResults := flags.Int("max-inventory-results", 0, "Maximum rows for MCP config and inventory tools; defaults to GONGMCP_MAX_INVENTORY_RESULTS or 200, hard-capped at 1000")
	maxBusinessAnalysisResults := flags.Int("max-business-analysis-results", 0, "Maximum rows for business-analysis tools; defaults to GONGMCP_MAX_BUSINESS_ANALYSIS_RESULTS or 100, hard-capped at 1000")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	flagSet := map[string]bool{}
	flags.Visit(func(flag *flag.Flag) {
		flagSet[flag.Name] = true
	})
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "unexpected arguments: %s\n", strings.Join(flags.Args(), " "))
		return 2
	}
	if *listToolPresets {
		if err := mcp.WriteToolPresetCatalog(stdout); err != nil {
			fmt.Fprintf(stderr, "list tool presets: %v\n", err)
			return 1
		}
		return 0
	}

	db := strings.TrimSpace(*dbPath)
	if db == "" {
		fmt.Fprintln(stderr, "--db is required")
		return 2
	}
	if _, err := os.Stat(db); err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(stderr, "db file not found: %s\n", filepath.Clean(db))
			return 2
		}
		fmt.Fprintf(stderr, "stat db: %v\n", err)
		return 1
	}

	allowlist, err := resolveToolAllowlist(toolSelection{
		AllowlistFlag:    *toolAllowlist,
		AllowlistFlagSet: flagSet["tool-allowlist"],
		PresetFlag:       *toolPreset,
		PresetFlagSet:    flagSet["tool-preset"],
		AllowlistEnv:     os.Getenv("GONGMCP_TOOL_ALLOWLIST"),
		PresetEnv:        os.Getenv("GONGMCP_TOOL_PRESET"),
	})
	if err != nil {
		fmt.Fprintf(stderr, "invalid tool selection: %v\n", err)
		return 2
	}
	limitPolicy, err := resolveLimitPolicy(limitSelection{
		SearchResults:              *maxSearchResults,
		SearchResultsSet:           flagSet["max-search-results"],
		CRMFields:                  *maxCRMFields,
		CRMFieldsSet:               flagSet["max-crm-fields"],
		LateStageSignals:           *maxLateStageSignals,
		LateStageSignalsSet:        flagSet["max-late-stage-signals"],
		MissingTranscripts:         *maxMissingTranscripts,
		MissingTranscriptsSet:      flagSet["max-missing-transcripts"],
		OpportunitySummaries:       *maxOpportunitySummaries,
		OpportunitySummariesSet:    flagSet["max-opportunity-summaries"],
		CRMMatrixCells:             *maxCRMMatrixCells,
		CRMMatrixCellsSet:          flagSet["max-crm-matrix-cells"],
		LifecycleResults:           *maxLifecycleResults,
		LifecycleResultsSet:        flagSet["max-lifecycle-results"],
		LifecycleCRMFields:         *maxLifecycleCRMFields,
		LifecycleCRMFieldsSet:      flagSet["max-lifecycle-crm-fields"],
		CallFactGroups:             *maxCallFactGroups,
		CallFactGroupsSet:          flagSet["max-call-fact-groups"],
		InventoryResults:           *maxInventoryResults,
		InventoryResultsSet:        flagSet["max-inventory-results"],
		BusinessAnalysisResults:    *maxBusinessAnalysisResults,
		BusinessAnalysisResultsSet: flagSet["max-business-analysis-results"],
		Getenv:                     os.Getenv,
	})
	if err != nil {
		fmt.Fprintf(stderr, "invalid MCP limit policy: %v\n", err)
		return 2
	}
	provenance, err := mcp.ParseTranscriptEvidenceProvenance(*transcriptEvidenceProvenance)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}

	ctx := context.Background()
	store, err := sqlite.OpenReadOnly(ctx, db)
	if err != nil {
		fmt.Fprintf(stderr, "open db: %v\n", err)
		return 1
	}
	defer store.Close()

	serverOptions := []mcp.ServerOption{mcp.WithToolAllowlist(allowlist), mcp.WithLimitPolicy(limitPolicy), mcp.WithTranscriptEvidenceProvenance(provenance)}
	if configPath := firstNonEmpty(*aiGovernanceConfig, os.Getenv("GONGMCP_AI_GOVERNANCE_CONFIG")); configPath != "" {
		if err := mcp.ValidateGovernanceAllowlist(allowlist); err != nil {
			fmt.Fprintf(stderr, "invalid AI governance MCP allowlist: %v\n", err)
			return 2
		}
		cfg, err := governance.LoadFile(configPath)
		if err != nil {
			fmt.Fprintln(stderr, "load AI governance config: failed")
			return 2
		}
		snapshot, err := governance.Snapshot(ctx, configPath, store)
		if err != nil {
			fmt.Fprintln(stderr, "snapshot AI governance state: failed")
			return 1
		}
		audit, err := governance.BuildAudit(ctx, store, cfg)
		if err != nil {
			fmt.Fprintln(stderr, "audit AI governance config: failed")
			return 1
		}
		if len(audit.UnmatchedEntries) > 0 && !(*allowUnmatchedAIGovernance || truthy(os.Getenv("GONGMCP_ALLOW_UNMATCHED_AI_GOVERNANCE"))) {
			fmt.Fprintf(stderr, "AI governance config has %d unmatched entries; run gongctl governance audit locally, add aliases, or set --allow-unmatched-ai-governance for this cache\n", len(audit.UnmatchedEntries))
			return 2
		}
		serverOptions = append(serverOptions, mcp.WithSuppressedCallIDs(audit.SuppressedCallIDs))
		serverOptions = append(serverOptions, mcp.WithGovernanceCheck(func(checkCtx context.Context) error {
			current, err := governance.Snapshot(checkCtx, configPath, store)
			if err != nil {
				return fmt.Errorf("AI governance state changed or cannot be verified; restart gongmcp")
			}
			if current != snapshot {
				return fmt.Errorf("AI governance state changed; restart gongmcp")
			}
			return nil
		}))
		fmt.Fprintf(stderr, "AI governance active: entries=%d aliases=%d matched=%d unmatched=%d suppressed_calls=%d; restart gongmcp after cache or config changes\n",
			audit.ConfigEntries,
			audit.ConfigAliases,
			len(audit.MatchedEntries),
			len(audit.UnmatchedEntries),
			audit.SuppressedCallCount,
		)
	}

	server := mcp.NewServerWithOptions(store, "gongmcp", version.DisplayVersion(), serverOptions...)

	httpConfig, err := mcphttp.ResolveConfig(mcphttp.ConfigSelection{
		AddrFlag:                *httpAddr,
		ForceStdio:              *forceStdio,
		AuthModeFlag:            *authMode,
		TokenFlag:               *bearerToken,
		TokenFileFlag:           *bearerTokenFile,
		PreviousTokenFileFlag:   *bearerTokenPreviousFile,
		AllowOpenNetworkFlag:    *allowOpenNetwork,
		DevAllowNoAuthLocalhost: *devAllowNoAuthLocalhost,
		AllowedOriginsFlag:      *allowedOrigins,
		Allowlist:               allowlist,
		Getenv:                  os.Getenv,
	})
	if err != nil {
		fmt.Fprintf(stderr, "invalid http config: %v\n", err)
		return 2
	}
	if httpConfig.Enabled {
		if httpConfig.OpenNetworkWarning {
			fmt.Fprintln(stderr, "warning: starting HTTP MCP on a non-local address; terminate TLS at a trusted proxy/gateway and use only for explicit private-network pilots")
		}
		handler := mcphttp.Handler(server, httpConfig, stderr)
		httpServer := &http.Server{
			Addr:              httpConfig.Addr,
			Handler:           handler,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       20 * time.Second,
			WriteTimeout:      90 * time.Second,
			IdleTimeout:       120 * time.Second,
		}
		fmt.Fprintf(stderr, "serving mcp over http on %s path=/mcp auth_mode=%s bearer_token_count=%d\n", httpConfig.Addr, httpConfig.AuthMode, len(httpConfig.BearerTokens))
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(stderr, "serve http mcp: %v\n", err)
			return 1
		}
		return 0
	}

	if err := server.Serve(ctx, stdin, stdout); err != nil {
		fmt.Fprintf(stderr, "serve mcp: %v\n", err)
		return 1
	}
	return 0
}

type toolSelection struct {
	AllowlistFlag    string
	AllowlistFlagSet bool
	PresetFlag       string
	PresetFlagSet    bool
	AllowlistEnv     string
	PresetEnv        string
}

type limitSelection struct {
	SearchResults              int
	SearchResultsSet           bool
	CRMFields                  int
	CRMFieldsSet               bool
	LateStageSignals           int
	LateStageSignalsSet        bool
	MissingTranscripts         int
	MissingTranscriptsSet      bool
	OpportunitySummaries       int
	OpportunitySummariesSet    bool
	CRMMatrixCells             int
	CRMMatrixCellsSet          bool
	LifecycleResults           int
	LifecycleResultsSet        bool
	LifecycleCRMFields         int
	LifecycleCRMFieldsSet      bool
	CallFactGroups             int
	CallFactGroupsSet          bool
	InventoryResults           int
	InventoryResultsSet        bool
	BusinessAnalysisResults    int
	BusinessAnalysisResultsSet bool
	Getenv                     func(string) string
}

func resolveLimitPolicy(selection limitSelection) (mcp.LimitPolicy, error) {
	policy, err := mcp.LimitPolicyFromEnv(selection.Getenv)
	if err != nil {
		return mcp.LimitPolicy{}, err
	}
	overrides := []struct {
		set   bool
		field string
		value int
	}{
		{selection.SearchResultsSet, "search_results", selection.SearchResults},
		{selection.CRMFieldsSet, "crm_fields", selection.CRMFields},
		{selection.LateStageSignalsSet, "late_stage_signals", selection.LateStageSignals},
		{selection.MissingTranscriptsSet, "missing_transcripts", selection.MissingTranscripts},
		{selection.OpportunitySummariesSet, "opportunity_summaries", selection.OpportunitySummaries},
		{selection.CRMMatrixCellsSet, "crm_matrix_cells", selection.CRMMatrixCells},
		{selection.LifecycleResultsSet, "lifecycle_results", selection.LifecycleResults},
		{selection.LifecycleCRMFieldsSet, "lifecycle_crm_fields", selection.LifecycleCRMFields},
		{selection.CallFactGroupsSet, "call_fact_groups", selection.CallFactGroups},
		{selection.InventoryResultsSet, "inventory_results", selection.InventoryResults},
		{selection.BusinessAnalysisResultsSet, "business_analysis_rows", selection.BusinessAnalysisResults},
	}
	for _, override := range overrides {
		if !override.set {
			continue
		}
		policy, err = policy.WithOverride(override.field, override.value)
		if err != nil {
			return mcp.LimitPolicy{}, err
		}
	}
	return policy.Normalize(), nil
}

func resolveToolAllowlist(selection toolSelection) ([]string, error) {
	flagAllowlist := strings.TrimSpace(selection.AllowlistFlag)
	flagPreset := strings.TrimSpace(selection.PresetFlag)
	if flagAllowlist != "" && flagPreset != "" {
		return nil, fmt.Errorf("--tool-allowlist cannot be combined with --tool-preset")
	}
	if flagAllowlist != "" {
		return mcp.ParseToolAllowlist(flagAllowlist)
	}
	if flagPreset != "" {
		return mcp.ExpandToolPreset(flagPreset)
	}

	envAllowlist := strings.TrimSpace(selection.AllowlistEnv)
	envPreset := strings.TrimSpace(selection.PresetEnv)
	if envAllowlist != "" && envPreset != "" {
		return nil, fmt.Errorf("set only one of GONGMCP_TOOL_ALLOWLIST or GONGMCP_TOOL_PRESET")
	}
	if envAllowlist != "" {
		return mcp.ParseToolAllowlist(envAllowlist)
	}
	if envPreset != "" {
		return mcp.ExpandToolPreset(envPreset)
	}

	if selection.AllowlistFlagSet || selection.PresetFlagSet {
		return nil, fmt.Errorf("empty tool selection")
	}
	return nil, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func envDefault(name, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return value
}

func truthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}
