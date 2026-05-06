package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fyne-coder/gongcli_mcp/internal/governance"
	"github.com/fyne-coder/gongcli_mcp/internal/mcp"
	"github.com/fyne-coder/gongcli_mcp/internal/store/postgres"
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
	toolPreset := flags.String("tool-preset", "", "Named MCP tool preset: business-pilot, operator-smoke, analyst-core, analyst-business-core, analyst, governance-search, all-readonly; defaults to GONGMCP_TOOL_PRESET")
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
	enforceToolScopedDBGrants := flags.Bool("enforce-tool-scoped-db-grants", false, "For Postgres MCP, validate reader function EXECUTE grants against the selected tool preset/allowlist; defaults to GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS")
	printPostgresReaderGrants := flags.Bool("print-postgres-reader-grants", false, "Compatibility helper: print reviewed Postgres reader grant SQL for --tool-preset business-pilot and exit; canonical operator command is gongctl mcp postgres-reader-sql")
	postgresReaderRole := flags.String("postgres-reader-role", "gongmcp_business_pilot_reader", "Postgres role name used by --print-postgres-reader-grants")
	postgresDatabase := flags.String("postgres-database", "gongctl", "Postgres database name used by --print-postgres-reader-grants")
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
	postgresURL := postgres.URLFromEnv(os.Getenv)
	postgresMode := db == "" && postgresURL != ""

	toolSelection := toolSelection{
		AllowlistFlag:    *toolAllowlist,
		AllowlistFlagSet: flagSet["tool-allowlist"],
		PresetFlag:       *toolPreset,
		PresetFlagSet:    flagSet["tool-preset"],
		AllowlistEnv:     os.Getenv("GONGMCP_TOOL_ALLOWLIST"),
		PresetEnv:        os.Getenv("GONGMCP_TOOL_PRESET"),
	}
	allowlist, err := resolveToolAllowlist(toolSelection)
	if err != nil {
		fmt.Fprintf(stderr, "invalid tool selection: %v\n", err)
		return 2
	}
	selectedPreset := selectedToolPresetName(toolSelection)
	if postgresMode || *printPostgresReaderGrants {
		postgresHTTPMode := !*forceStdio && firstNonEmpty(*httpAddr, os.Getenv("GONGMCP_HTTP_ADDR")) != ""
		allowlist, err = postgresToolAllowlist(allowlist, postgresHTTPMode, selectedPreset)
		if err != nil {
			fmt.Fprintf(stderr, "invalid postgres tool selection: %v\n", err)
			return 2
		}
	}
	if *printPostgresReaderGrants {
		sql, err := buildPostgresReaderGrantSQL(allowlist, *postgresReaderRole, *postgresDatabase)
		if err != nil {
			fmt.Fprintf(stderr, "print postgres reader grants: %v\n", err)
			return 2
		}
		if _, err := io.WriteString(stdout, sql); err != nil {
			fmt.Fprintf(stderr, "print postgres reader grants: %v\n", err)
			return 1
		}
		return 0
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
	governanceConfigPath := firstNonEmpty(*aiGovernanceConfig, os.Getenv("GONGMCP_AI_GOVERNANCE_CONFIG"))
	enforceScopedDBGrants := *enforceToolScopedDBGrants || truthy(os.Getenv("GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS"))

	ctx := context.Background()
	var store mcp.Store
	var governanceStore governance.CandidateStore
	var postgresStore *postgres.Store
	var closeStore func() error
	if postgresMode {
		readOnlyOptions := postgres.ReadOnlyOptions{}
		if enforceScopedDBGrants {
			readOnlyOptions = postgresReadOnlyOptionsForAllowlist(allowlist)
			if governanceConfigPath != "" {
				readOnlyOptions.RequiredFunctionSignatures = append(readOnlyOptions.RequiredFunctionSignatures, postgresGovernanceFunctionSignatures()...)
				readOnlyOptions.AllowedFunctionSignatures = append(readOnlyOptions.AllowedFunctionSignatures, postgresGovernanceFunctionSignatures()...)
			}
			readOnlyOptions.AllowAccountQuery = truthy(os.Getenv("GONGMCP_POSTGRES_REDACTED_SERVING_DB"))
		}
		pgStore, err := postgres.OpenReadOnlyWithOptions(ctx, postgresURL, readOnlyOptions)
		if err != nil {
			fmt.Fprintf(stderr, "open postgres db: %v\n", err)
			return 1
		}
		store = pgStore
		postgresStore = pgStore
		closeStore = pgStore.Close
		fmt.Fprintf(stderr, "postgres backend active: read-only MCP exposes %s\n", strings.Join(allowlist, ","))
	} else {
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
		sqliteStore, err := sqlite.OpenReadOnly(ctx, db)
		if err != nil {
			fmt.Fprintf(stderr, "open db: %v\n", err)
			return 1
		}
		store = sqliteStore
		governanceStore = sqliteStore
		closeStore = sqliteStore.Close
	}
	defer closeStore()

	serverOptions := []mcp.ServerOption{mcp.WithToolAllowlist(allowlist), mcp.WithLimitPolicy(limitPolicy), mcp.WithTranscriptEvidenceProvenance(provenance)}
	if min := postgresAnalystSmallCellMin(postgresMode, selectedPreset, enforceScopedDBGrants); min > 1 {
		serverOptions = append(serverOptions, mcp.WithBusinessAnalysisSmallCellMin(min))
	}
	if governanceConfigPath != "" {
		configPath := governanceConfigPath
		if err := mcp.ValidateGovernanceAllowlist(allowlist); err != nil {
			fmt.Fprintf(stderr, "invalid AI governance MCP allowlist: %v\n", err)
			return 2
		}
		cfg, err := governance.LoadFile(configPath)
		if err != nil {
			fmt.Fprintln(stderr, "load AI governance config: failed")
			return 2
		}
		if postgresMode {
			if postgresStore == nil {
				fmt.Fprintln(stderr, "open postgres governance state: failed")
				return 1
			}
			state, err := postgresStore.LoadGovernancePolicy(ctx, cfg.Fingerprint())
			if err != nil {
				fmt.Fprintln(stderr, "load Postgres AI governance policy: failed; run GONG_DATABASE_URL=<writer-url> gongctl governance audit --config <same-ai-governance.yaml> --apply-postgres-policy, then restart gongmcp")
				return 2
			}
			currentFingerprint, err := postgresStore.GovernanceDataFingerprint(ctx)
			if err != nil {
				fmt.Fprintln(stderr, "snapshot Postgres AI governance state: failed")
				return 1
			}
			if currentFingerprint != state.DataFingerprint {
				fmt.Fprintln(stderr, "Postgres AI governance policy is stale; run GONG_DATABASE_URL=<writer-url> gongctl governance audit --config <same-ai-governance.yaml> --apply-postgres-policy, then restart gongmcp")
				return 2
			}
			if state.UnmatchedEntries > 0 && !(*allowUnmatchedAIGovernance || truthy(os.Getenv("GONGMCP_ALLOW_UNMATCHED_AI_GOVERNANCE"))) {
				fmt.Fprintf(stderr, "AI governance config has %d unmatched entries; run gongctl governance audit locally, add aliases, or set --allow-unmatched-ai-governance for this cache\n", state.UnmatchedEntries)
				return 2
			}
			serverOptions = append(serverOptions, mcp.WithSuppressedCallIDs(state.SuppressedCallIDs))
			serverOptions = append(serverOptions, mcp.WithGovernanceCheck(func(checkCtx context.Context) error {
				current, err := postgresStore.GovernanceDataFingerprint(checkCtx)
				if err != nil {
					return fmt.Errorf("AI governance state changed or cannot be verified; restart gongmcp")
				}
				nextState, err := postgresStore.LoadGovernancePolicy(checkCtx, cfg.Fingerprint())
				if err != nil {
					return fmt.Errorf("AI governance state changed or cannot be verified; restart gongmcp")
				}
				if current != state.DataFingerprint || nextState.DataFingerprint != state.DataFingerprint || nextState.UpdatedAt != state.UpdatedAt || nextState.SuppressedCallCount != state.SuppressedCallCount {
					return fmt.Errorf("AI governance state changed; restart gongmcp")
				}
				return nil
			}))
			fmt.Fprintf(stderr, "AI governance active: backend=postgres entries=%d aliases=%d matched=%d unmatched=%d suppressed_calls=%d; restart gongmcp after cache, policy, or config changes\n",
				state.ConfigEntries,
				state.ConfigAliases,
				state.MatchedEntries,
				state.UnmatchedEntries,
				state.SuppressedCallCount,
			)
		} else {
			if governanceStore == nil {
				fmt.Fprintln(stderr, "AI governance config is not supported without a governance-capable store")
				return 2
			}
			snapshot, err := governance.Snapshot(ctx, configPath, governanceStore)
			if err != nil {
				fmt.Fprintln(stderr, "snapshot AI governance state: failed")
				return 1
			}
			audit, err := governance.BuildAudit(ctx, governanceStore, cfg)
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
				current, err := governance.Snapshot(checkCtx, configPath, governanceStore)
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
	}

	server := mcp.NewServerWithOptions(store, "gongmcp", version.DisplayVersion(), serverOptions...)

	httpConfig, err := resolveHTTPConfig(*httpAddr, *forceStdio, *authMode, *bearerToken, *bearerTokenFile, *bearerTokenPreviousFile, *allowOpenNetwork, *devAllowNoAuthLocalhost, *allowedOrigins, allowlist, os.Getenv)
	if err != nil {
		fmt.Fprintf(stderr, "invalid http config: %v\n", err)
		return 2
	}
	if httpConfig.Enabled {
		if httpConfig.OpenNetworkWarning {
			fmt.Fprintln(stderr, "warning: starting HTTP MCP on a non-local address; terminate TLS at a trusted proxy/gateway and use only for explicit private-network pilots")
		}
		handler := httpHandler(server, httpConfig, stderr)
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

func postgresToolAllowlist(allowlist []string, httpMode bool, presetName string) ([]string, error) {
	switch strings.ToLower(strings.TrimSpace(presetName)) {
	case "all-readonly", "all-tools", "all":
		return nil, fmt.Errorf("%s is not supported by the postgres vertical slice", presetName)
	case "analyst", "analyst-expansion":
		reviewed := postgresReviewedAnalystTools()
		if !sameStringSet(allowlist, reviewed) {
			return nil, fmt.Errorf("%s includes tools that have not been reviewed for the postgres analyst preset", presetName)
		}
		return reviewed, nil
	case "governance-search":
		return []string{"search_calls", "get_call", "search_transcript_segments", "rank_transcript_backlog"}, nil
	}
	if postgresGovernanceSearchPreset(allowlist) {
		return []string{"search_calls", "get_call", "search_transcript_segments", "rank_transcript_backlog"}, nil
	}
	supported := map[string]struct{}{
		"analyze_late_stage_crm_signals":            {},
		"get_sync_status":                           {},
		"get_business_profile":                      {},
		"get_scorecard":                             {},
		"get_call":                                  {},
		"list_business_concepts":                    {},
		"list_cached_crm_schema_fields":             {},
		"list_cached_crm_schema_objects":            {},
		"list_crm_fields":                           {},
		"list_crm_integrations":                     {},
		"list_crm_object_types":                     {},
		"list_gong_settings":                        {},
		"list_lifecycle_buckets":                    {},
		"list_scorecards":                           {},
		"list_unmapped_crm_fields":                  {},
		"missing_transcripts":                       {},
		"crm_field_population_matrix":               {},
		"compare_lifecycle_crm_fields":              {},
		"opportunity_call_summary":                  {},
		"opportunities_missing_transcripts":         {},
		"prioritize_transcripts_by_lifecycle":       {},
		"rank_transcript_backlog":                   {},
		"search_calls":                              {},
		"search_calls_by_lifecycle":                 {},
		"search_crm_field_values":                   {},
		"search_transcript_segments":                {},
		"search_transcript_quotes_with_attribution": {},
		"search_transcripts_by_call_facts":          {},
		"search_transcripts_by_crm_context":         {},
		"summarize_scorecard_activity":              {},
		"summarize_call_facts":                      {},
		"summarize_calls_by_lifecycle":              {},
		"build_call_cohort":                         {},
		"inspect_call_cohort":                       {},
		"list_call_cohorts":                         {},
		"compare_call_cohorts":                      {},
		"search_calls_by_filters":                   {},
		"summarize_calls_by_filters":                {},
		"search_transcripts_by_filters":             {},
		"discover_themes_in_cohort":                 {},
		"summarize_themes_by_dimension":             {},
		"compare_themes_over_time":                  {},
		"compare_themes_by_segment":                 {},
		"extract_theme_quotes":                      {},
		"search_quotes_in_cohort":                   {},
		"rank_quotes_for_sales_use":                 {},
		"build_quote_pack":                          {},
		"compare_theme_outcomes":                    {},
		"summarize_pipeline_progression_by_theme":   {},
		"summarize_loss_reasons_by_theme":           {},
		"compare_won_lost_theme_patterns":           {},
		"summarize_themes_by_persona":               {},
		"summarize_themes_by_industry":              {},
		"rank_personas_by_insight_quality":          {},
		"diagnose_attribution_coverage":             {},
		"generate_sales_hooks_from_themes":          {},
		"generate_outreach_sequence_inputs":         {},
		"recommend_target_personas_and_industries":  {},
		"build_theme_brief":                         {},
		"score_cohort_evidence_quality":             {},
		"explain_analysis_limitations":              {},
		"suggest_filter_refinements":                {},
	}
	if len(allowlist) == 0 {
		if httpMode {
			return nil, fmt.Errorf("postgres HTTP mode requires explicit --tool-preset, --tool-allowlist, GONGMCP_TOOL_PRESET, or GONGMCP_TOOL_ALLOWLIST")
		}
		return []string{"get_sync_status", "search_calls", "search_transcript_segments"}, nil
	}
	for _, name := range allowlist {
		if _, ok := supported[name]; !ok {
			return nil, fmt.Errorf("%s is not supported by the postgres vertical slice", name)
		}
	}
	return allowlist, nil
}

func postgresReviewedAnalystTools() []string {
	return []string{
		"get_sync_status",
		"list_crm_object_types",
		"list_crm_fields",
		"get_business_profile",
		"list_business_concepts",
		"list_unmapped_crm_fields",
		"analyze_late_stage_crm_signals",
		"opportunities_missing_transcripts",
		"search_transcripts_by_crm_context",
		"opportunity_call_summary",
		"crm_field_population_matrix",
		"list_lifecycle_buckets",
		"summarize_calls_by_lifecycle",
		"prioritize_transcripts_by_lifecycle",
		"compare_lifecycle_crm_fields",
		"summarize_call_facts",
		"rank_transcript_backlog",
		"search_transcript_segments",
		"search_transcripts_by_call_facts",
		"search_transcript_quotes_with_attribution",
		"build_call_cohort",
		"inspect_call_cohort",
		"list_call_cohorts",
		"compare_call_cohorts",
		"search_calls_by_filters",
		"summarize_calls_by_filters",
		"search_transcripts_by_filters",
		"discover_themes_in_cohort",
		"summarize_themes_by_dimension",
		"compare_themes_over_time",
		"compare_themes_by_segment",
		"extract_theme_quotes",
		"search_quotes_in_cohort",
		"rank_quotes_for_sales_use",
		"build_quote_pack",
		"compare_theme_outcomes",
		"summarize_pipeline_progression_by_theme",
		"summarize_loss_reasons_by_theme",
		"compare_won_lost_theme_patterns",
		"summarize_themes_by_persona",
		"summarize_themes_by_industry",
		"rank_personas_by_insight_quality",
		"diagnose_attribution_coverage",
		"generate_sales_hooks_from_themes",
		"generate_outreach_sequence_inputs",
		"recommend_target_personas_and_industries",
		"build_theme_brief",
		"score_cohort_evidence_quality",
		"explain_analysis_limitations",
		"suggest_filter_refinements",
	}
}

func postgresGovernanceSearchPreset(allowlist []string) bool {
	if len(allowlist) == 0 {
		return false
	}
	governanceTools, err := mcp.ExpandToolPreset("governance-search")
	if err != nil || len(governanceTools) != len(allowlist) {
		return false
	}
	seen := make(map[string]int, len(governanceTools))
	for _, name := range governanceTools {
		seen[name]++
	}
	for _, name := range allowlist {
		seen[name]--
		if seen[name] < 0 {
			return false
		}
	}
	return true
}

func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]int, len(a))
	for _, item := range a {
		seen[item]++
	}
	for _, item := range b {
		seen[item]--
		if seen[item] < 0 {
			return false
		}
	}
	return true
}

func postgresReadOnlyOptionsForAllowlist(allowlist []string) postgres.ReadOnlyOptions {
	return postgres.ReadOnlyOptionsForToolAllowlist(allowlist)
}

func buildPostgresReaderGrantSQL(allowlist []string, roleName, databaseName string) (string, error) {
	sql, err := postgres.BuildScopedReaderGrantSQL(postgres.ScopedReaderGrantSQLParams{
		Allowlist:    allowlist,
		RoleName:     roleName,
		DatabaseName: databaseName,
		Generator:    "gongmcp --print-postgres-reader-grants",
	})
	if err != nil {
		errText := err.Error()
		errText = strings.Replace(errText, "scoped reader grant SQL", "--print-postgres-reader-grants", 1)
		errText = strings.Replace(errText, "invalid role name", "invalid --postgres-reader-role", 1)
		errText = strings.Replace(errText, "invalid database name", "invalid --postgres-database", 1)
		return "", fmt.Errorf("%s", errText)
	}
	return sql, nil
}

func postgresGovernanceFunctionSignatures() []string {
	return postgres.GovernanceFunctionSignatures()
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

func selectedToolPresetName(selection toolSelection) string {
	if preset := strings.TrimSpace(selection.PresetFlag); preset != "" {
		return preset
	}
	if strings.TrimSpace(selection.AllowlistFlag) != "" {
		return ""
	}
	if preset := strings.TrimSpace(selection.PresetEnv); preset != "" && strings.TrimSpace(selection.AllowlistEnv) == "" {
		return preset
	}
	return ""
}

func postgresAnalystSmallCellMin(postgresMode bool, presetName string, enforceScopedDBGrants bool) int {
	if !postgresMode || !enforceScopedDBGrants {
		return 0
	}
	switch strings.ToLower(strings.TrimSpace(presetName)) {
	case "analyst", "analyst-expansion":
		return 3
	default:
		return 0
	}
}

type getenvFunc func(string) string

type httpConfig struct {
	Enabled            bool
	Addr               string
	AuthMode           string
	BearerTokens       []string
	OpenNetworkWarning bool
	LocalBind          bool
	AllowedOrigins     map[string]struct{}
}

func resolveHTTPConfig(addrFlag string, forceStdio bool, authModeFlag, tokenFlag, tokenFileFlag, previousTokenFileFlag string, allowOpenNetworkFlag, devAllowNoAuthLocalhost bool, allowedOriginsFlag string, allowlist []string, getenv getenvFunc) (httpConfig, error) {
	if forceStdio {
		if strings.TrimSpace(addrFlag) != "" {
			return httpConfig{}, fmt.Errorf("--stdio cannot be combined with --http")
		}
		return httpConfig{}, nil
	}

	addr := firstNonEmpty(addrFlag, getenv("GONGMCP_HTTP_ADDR"))
	if addr == "" {
		return httpConfig{}, nil
	}
	if len(allowlist) == 0 {
		return httpConfig{}, fmt.Errorf("HTTP mode requires --tool-preset, --tool-allowlist, GONGMCP_TOOL_PRESET, or GONGMCP_TOOL_ALLOWLIST")
	}

	authModeSource := firstNonEmpty(authModeFlag, getenv("GONGMCP_AUTH_MODE"))
	authMode := strings.ToLower(authModeSource)
	localBind := isLocalHTTPAddr(addr)
	if authMode == "" {
		if bearerTokenSourceConfigured(tokenFlag, tokenFileFlag, getenv) {
			authMode = "bearer"
		} else if devAllowNoAuthLocalhost && localBind {
			authMode = "none"
		} else {
			authMode = "bearer"
		}
	}
	if authMode != "none" && authMode != "bearer" {
		return httpConfig{}, fmt.Errorf("auth-mode must be none or bearer")
	}
	if authMode == "none" {
		if !devAllowNoAuthLocalhost {
			return httpConfig{}, fmt.Errorf("auth-mode=none requires --dev-allow-no-auth-localhost")
		}
		if !localBind {
			return httpConfig{}, fmt.Errorf("auth-mode=none is only allowed for localhost development")
		}
	}

	allowOpenNetwork := allowOpenNetworkFlag || truthy(getenv("GONGMCP_ALLOW_OPEN_NETWORK"))
	if !localBind {
		if !allowOpenNetwork {
			return httpConfig{}, fmt.Errorf("non-local HTTP address %q requires --allow-open-network and TLS termination at a trusted proxy or gateway", addr)
		}
	}
	allowedOrigins, err := parseAllowedOrigins(allowedOriginsFlag, getenv("GONGMCP_ALLOWED_ORIGINS"))
	if err != nil {
		return httpConfig{}, err
	}
	if !localBind && len(allowedOrigins) == 0 {
		return httpConfig{}, fmt.Errorf("non-local HTTP address %q requires --allowed-origins or GONGMCP_ALLOWED_ORIGINS for Origin validation", addr)
	}

	tokens := []string(nil)
	if authMode == "bearer" {
		tokens, err = resolveBearerTokens(tokenFlag, tokenFileFlag, previousTokenFileFlag, getenv)
		if err != nil {
			return httpConfig{}, err
		}
	}

	return httpConfig{
		Enabled:            true,
		Addr:               addr,
		AuthMode:           authMode,
		BearerTokens:       tokens,
		OpenNetworkWarning: !localBind,
		LocalBind:          localBind,
		AllowedOrigins:     allowedOrigins,
	}, nil
}

func parseAllowedOrigins(flagValue, envValue string) (map[string]struct{}, error) {
	raw := strings.TrimSpace(flagValue)
	if raw == "" {
		raw = strings.TrimSpace(envValue)
	}
	out := map[string]struct{}{}
	if raw == "" {
		return out, nil
	}
	for _, piece := range strings.Split(raw, ",") {
		origin := strings.TrimSpace(piece)
		if origin == "" {
			continue
		}
		normalized, err := normalizeOrigin(origin)
		if err != nil {
			return nil, err
		}
		out[normalized] = struct{}{}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no valid allowed origins provided")
	}
	return out, nil
}

func normalizeOrigin(origin string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(origin))
	if err != nil {
		return "", fmt.Errorf("invalid allowed origin %q", origin)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("invalid allowed origin %q: scheme must be http or https", origin)
	}
	if parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("invalid allowed origin %q: use scheme://host[:port] only", origin)
	}
	return strings.ToLower(parsed.Scheme) + "://" + strings.ToLower(parsed.Host), nil
}

func resolveBearerTokens(tokenFlag, tokenFileFlag, previousTokenFileFlag string, getenv getenvFunc) ([]string, error) {
	token := strings.TrimSpace(tokenFlag)
	tokenFile := strings.TrimSpace(tokenFileFlag)
	previousTokenFile := strings.TrimSpace(previousTokenFileFlag)
	if token != "" && tokenFile != "" {
		return nil, fmt.Errorf("set either bearer token or bearer token file, not both")
	}
	if tokenFile != "" {
		read, err := readBearerTokenFile(tokenFile)
		if err != nil {
			return nil, err
		}
		token = read
	} else if token == "" {
		token = strings.TrimSpace(getenv("GONGMCP_BEARER_TOKEN"))
		tokenFile = strings.TrimSpace(getenv("GONGMCP_BEARER_TOKEN_FILE"))
		if token != "" && tokenFile != "" {
			return nil, fmt.Errorf("set either bearer token or bearer token file, not both")
		}
		if tokenFile != "" {
			read, err := readBearerTokenFile(tokenFile)
			if err != nil {
				return nil, err
			}
			token = read
		}
	}
	if token == "" {
		return nil, fmt.Errorf("auth-mode=bearer requires bearer token or bearer token file")
	}
	tokens := []string{token}
	if previousTokenFile == "" {
		previousTokenFile = strings.TrimSpace(getenv("GONGMCP_BEARER_TOKEN_PREVIOUS_FILE"))
	}
	if previousTokenFile != "" {
		previous, err := readBearerTokenFile(previousTokenFile)
		if err != nil {
			return nil, err
		}
		if previous != token {
			tokens = append(tokens, previous)
		}
	}
	return tokens, nil
}

func bearerTokenSourceConfigured(tokenFlag, tokenFileFlag string, getenv getenvFunc) bool {
	if firstNonEmpty(tokenFlag, tokenFileFlag) != "" {
		return true
	}
	return firstNonEmpty(getenv("GONGMCP_BEARER_TOKEN"), getenv("GONGMCP_BEARER_TOKEN_FILE")) != ""
}

func readBearerTokenFile(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read bearer token file: %w", err)
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", fmt.Errorf("auth-mode=bearer requires bearer token or bearer token file")
	}
	return token, nil
}

func httpHandler(server *mcp.Server, cfg httpConfig, accessLog io.Writer) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/mcp", accessLogMiddleware(originMiddleware(authMiddleware(server, cfg), cfg), cfg, accessLog))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodHead {
			return
		}
		_ = json.NewEncoder(w).Encode(struct {
			Status  string `json:"status"`
			Service string `json:"service"`
			Version string `json:"version"`
		}{
			Status:  "ok",
			Service: "gongmcp",
			Version: version.DisplayVersion(),
		})
	})
	return mux
}

func originMiddleware(next http.Handler, cfg httpConfig) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin == "" {
			next.ServeHTTP(w, r)
			return
		}
		normalized, err := normalizeOrigin(origin)
		if err != nil || !originAllowed(normalized, cfg) {
			setAccessDecision(r, "origin_denied")
			http.Error(w, "invalid origin", http.StatusForbidden)
			return
		}
		setCORSHeaders(w, normalized)
		if r.Method == http.MethodOptions {
			if !validPreflightRequest(r) {
				setAccessDecision(r, "cors_preflight_denied")
				w.Header().Set("Allow", "POST, OPTIONS")
				http.Error(w, "invalid preflight", http.StatusMethodNotAllowed)
				return
			}
			setAccessDecision(r, "cors_preflight_ok")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func setCORSHeaders(w http.ResponseWriter, origin string) {
	w.Header().Add("Vary", "Origin")
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, MCP-Protocol-Version, Mcp-Session-Id")
	w.Header().Set("Access-Control-Max-Age", "600")
}

func validPreflightRequest(r *http.Request) bool {
	if !strings.EqualFold(strings.TrimSpace(r.Header.Get("Access-Control-Request-Method")), http.MethodPost) {
		return false
	}
	allowedHeaders := map[string]struct{}{
		"authorization":        {},
		"content-type":         {},
		"mcp-protocol-version": {},
		"mcp-session-id":       {},
	}
	for _, header := range strings.Split(r.Header.Get("Access-Control-Request-Headers"), ",") {
		normalized := strings.ToLower(strings.TrimSpace(header))
		if normalized == "" {
			continue
		}
		if _, ok := allowedHeaders[normalized]; !ok {
			return false
		}
	}
	return true
}

func originAllowed(origin string, cfg httpConfig) bool {
	if _, ok := cfg.AllowedOrigins[origin]; ok {
		return true
	}
	return cfg.LocalBind && isLocalOrigin(origin)
}

func isLocalOrigin(origin string) bool {
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := strings.Trim(strings.ToLower(parsed.Hostname()), "[]")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func accessLogMiddleware(next http.Handler, cfg httpConfig, out io.Writer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		requestID := fmt.Sprintf("%d", start.UnixNano())
		decision := &accessDecision{Reason: "ok"}
		r = r.WithContext(context.WithValue(r.Context(), accessDecisionContextKey{}, decision))
		var body bytes.Buffer
		if r.Body != nil {
			r.Body = io.NopCloser(io.TeeReader(r.Body, &body))
		}
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		method, toolName := mcpHTTPAccessInfo(body.Bytes())
		fmt.Fprintf(out, "mcp_http_access request_id=%s method=%q tool=%q status=%d decision=%q duration_ms=%d remote_addr=%q auth_mode=%q token_slot=%q\n",
			requestID,
			method,
			toolName,
			recorder.status,
			decision.Reason,
			time.Since(start).Milliseconds(),
			r.RemoteAddr,
			cfg.AuthMode,
			decision.TokenSlot,
		)
	})
}

type accessDecision struct {
	Reason    string
	TokenSlot string
}

type accessDecisionContextKey struct{}

func setAccessDecision(r *http.Request, reason string) {
	if decision, ok := r.Context().Value(accessDecisionContextKey{}).(*accessDecision); ok && decision != nil {
		decision.Reason = reason
	}
}

func setAccessTokenSlot(r *http.Request, slot string) {
	if decision, ok := r.Context().Value(accessDecisionContextKey{}).(*accessDecision); ok && decision != nil {
		decision.TokenSlot = slot
	}
}

func mcpHTTPAccessInfo(payload []byte) (string, string) {
	var req struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(payload, &req); err != nil {
		return "", ""
	}
	if req.Method != "tools/call" {
		return req.Method, ""
	}
	var params struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return req.Method, ""
	}
	return req.Method, strings.TrimSpace(params.Name)
}

func authMiddleware(next http.Handler, cfg httpConfig) http.Handler {
	if cfg.AuthMode != "bearer" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Fields(r.Header.Get("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			setAccessDecision(r, "auth_missing")
			w.Header().Set("WWW-Authenticate", `Bearer realm="gongmcp"`)
			http.Error(w, "missing bearer token", http.StatusUnauthorized)
			return
		}
		token := parts[1]
		slot, ok := bearerTokenSlot(token, cfg.BearerTokens)
		if !ok {
			setAccessDecision(r, "auth_invalid")
			w.Header().Set("WWW-Authenticate", `Bearer realm="gongmcp"`)
			http.Error(w, "invalid bearer token", http.StatusUnauthorized)
			return
		}
		setAccessTokenSlot(r, slot)
		next.ServeHTTP(w, r)
	})
}

func validBearerToken(token string, expected []string) bool {
	_, ok := bearerTokenSlot(token, expected)
	return ok
}

func bearerTokenSlot(token string, expected []string) (string, bool) {
	tokenSum := sha256.Sum256([]byte(token))
	for idx, value := range expected {
		valueSum := sha256.Sum256([]byte(value))
		if subtle.ConstantTimeCompare(tokenSum[:], valueSum[:]) == 1 {
			if idx == 0 {
				return "current", true
			}
			return "previous", true
		}
	}
	return "", false
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

func isLocalHTTPAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	host = strings.Trim(strings.ToLower(strings.TrimSpace(host)), "[]")
	if host == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
