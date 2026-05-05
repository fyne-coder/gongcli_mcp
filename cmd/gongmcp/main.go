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
	if postgresMode {
		postgresHTTPMode := !*forceStdio && firstNonEmpty(*httpAddr, os.Getenv("GONGMCP_HTTP_ADDR")) != ""
		allowlist, err = postgresToolAllowlist(allowlist, postgresHTTPMode, selectedToolPresetName(toolSelection))
		if err != nil {
			fmt.Fprintf(stderr, "invalid postgres tool selection: %v\n", err)
			return 2
		}
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

	ctx := context.Background()
	var store mcp.Store
	var governanceStore governance.CandidateStore
	var postgresStore *postgres.Store
	var closeStore func() error
	if postgresMode {
		readOnlyOptions := postgres.ReadOnlyOptions{}
		if *enforceToolScopedDBGrants || truthy(os.Getenv("GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS")) {
			readOnlyOptions = postgresReadOnlyOptionsForAllowlist(allowlist)
			if governanceConfigPath != "" {
				readOnlyOptions.RequiredFunctionSignatures = append(readOnlyOptions.RequiredFunctionSignatures, postgresGovernanceFunctionSignatures()...)
				readOnlyOptions.AllowedFunctionSignatures = append(readOnlyOptions.AllowedFunctionSignatures, postgresGovernanceFunctionSignatures()...)
			}
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
	case "analyst", "analyst-expansion", "all-readonly", "all-tools", "all":
		return nil, fmt.Errorf("%s is not supported by the postgres vertical slice", presetName)
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
		"build_call_cohort":                         {},
		"inspect_call_cohort":                       {},
		"search_calls_by_filters":                   {},
		"summarize_calls_by_filters":                {},
		"search_transcripts_by_filters":             {},
		"discover_themes_in_cohort":                 {},
		"summarize_themes_by_dimension":             {},
		"extract_theme_quotes":                      {},
		"search_quotes_in_cohort":                   {},
		"diagnose_attribution_coverage":             {},
		"score_cohort_evidence_quality":             {},
		"explain_analysis_limitations":              {},
		"suggest_filter_refinements":                {},
		"summarize_scorecard_activity":              {},
		"summarize_call_facts":                      {},
		"summarize_calls_by_lifecycle":              {},
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

func postgresReadOnlyOptionsForAllowlist(allowlist []string) postgres.ReadOnlyOptions {
	signatures := postgresFunctionSignaturesForTools(allowlist)
	options := postgres.ReadOnlyOptions{
		RequiredFunctionSignatures:     signatures,
		AllowedFunctionSignatures:      signatures,
		EnforceAllowedFunctionBoundary: true,
	}
	if postgresBusinessPilotScopedColumns(allowlist) {
		columns := postgresBusinessPilotColumnSelectGrants()
		options.RequiredColumnSelectGrants = columns
		options.AllowedColumnSelectGrants = columns
		options.EnforceAllowedColumnBoundary = true
	}
	return options
}

func postgresGovernanceFunctionSignatures() []string {
	return []string{
		"public.gongmcp_governance_data_fingerprint()",
		"public.gongmcp_governance_policy_state(text)",
		"public.gongmcp_governance_suppressed_call_ids(text)",
	}
}

func postgresFunctionSignaturesForTools(allowlist []string) []string {
	profileReadinessFunctions := []string{
		"public.gongmcp_active_business_profile()",
		"public.gongmcp_profile_call_fact_cache_meta(bigint, text)",
		"public.gongmcp_profile_data_fingerprint()",
	}
	profileRowsFunctions := append(copyPostgresFunctionSignatures(profileReadinessFunctions),
		"public.gongmcp_profile_call_fact_cache(bigint, text)",
	)
	profileSummaryFunctions := append(copyPostgresFunctionSignatures(profileReadinessFunctions),
		"public.gongmcp_profile_call_fact_summary(bigint, text, text, text, text, text, text, text, integer)",
	)
	businessAnalysisCoreFunctions := []string{
		"public.gongmcp_business_analysis_calls(text, text, text, text, text, text, text, text, text, text, text, text, text, text, text, integer)",
		"public.gongmcp_business_analysis_summary(text, text, text, text, text, text, text, text, text, text, text, text, text, text, text)",
	}
	businessAnalysisEvidenceFunctions := append(copyPostgresFunctionSignatures(businessAnalysisCoreFunctions),
		"public.gongmcp_business_analysis_evidence(text, text, text, text, text, text, text, text, text, text, text, text, text, text, text, text, integer)",
	)
	businessAnalysisDimensionFunctions := append(copyPostgresFunctionSignatures(businessAnalysisCoreFunctions),
		"public.gongmcp_business_analysis_dimension(text, text, text, text, text, text, text, text, text, text, text, text, text, text, text, text, text, integer)",
	)
	functionsByTool := map[string][]string{
		"analyze_late_stage_crm_signals": {
			"public.gongmcp_late_stage_call_counts(text, text, text)",
			"public.gongmcp_late_stage_stage_counts(text, text, text)",
			"public.gongmcp_late_stage_signal_inventory(text, text, text, integer, boolean)",
		},
		"build_call_cohort": businessAnalysisCoreFunctions,
		"compare_lifecycle_crm_fields": {
			"public.gongmcp_compare_lifecycle_crm_fields(text, text, text, integer)",
		},
		"crm_field_population_matrix": {
			"public.gongmcp_crm_field_population_matrix(text, text, integer)",
		},
		"diagnose_attribution_coverage": businessAnalysisCoreFunctions,
		"discover_themes_in_cohort":     businessAnalysisEvidenceFunctions,
		"explain_analysis_limitations":  businessAnalysisCoreFunctions,
		"extract_theme_quotes":          businessAnalysisEvidenceFunctions,
		"get_sync_status": {
			"public.gongmcp_active_business_profile()",
			"public.gongmcp_profile_call_fact_cache_meta(bigint, text)",
			"public.gongmcp_profile_data_fingerprint()",
			"public.gongmcp_scorecard_activity_totals()",
		},
		"get_business_profile": {
			"public.gongmcp_active_business_profile()",
		},
		"get_scorecard": {
			"public.gongmcp_scorecard_detail(text)",
		},
		"inspect_call_cohort": businessAnalysisCoreFunctions,
		"list_business_concepts": {
			"public.gongmcp_active_business_profile()",
		},
		"list_crm_object_types": {
			"public.gongmcp_crm_object_type_summary()",
		},
		"list_lifecycle_buckets": profileReadinessFunctions,
		"list_scorecards": {
			"public.gongmcp_scorecards(boolean, integer)",
		},
		"list_unmapped_crm_fields": append([]string{
			"public.gongmcp_unmapped_crm_field_inventory(integer)",
		}, profileReadinessFunctions...),
		"missing_transcripts": {
			"public.gongmcp_missing_transcripts(text, text, text, text, text, text, text, text, integer)",
		},
		"opportunities_missing_transcripts": {
			"public.gongmcp_opportunities_missing_transcripts(text, integer)",
		},
		"opportunity_call_summary": {
			"public.gongmcp_opportunity_call_summary(text, integer)",
		},
		"prioritize_transcripts_by_lifecycle": profileRowsFunctions,
		"rank_transcript_backlog":             profileRowsFunctions,
		"score_cohort_evidence_quality":       businessAnalysisCoreFunctions,
		"search_calls_by_filters":             businessAnalysisCoreFunctions,
		"search_calls_by_lifecycle":           profileRowsFunctions,
		"search_crm_field_values": {
			"public.gongmcp_crm_field_value_search(text, text, text, integer, boolean, boolean)",
		},
		"search_quotes_in_cohort": businessAnalysisEvidenceFunctions,
		"search_transcript_quotes_with_attribution": {
			"public.gongmcp_search_transcript_quotes_with_attribution(text, text, text, text, text, text, text, text, text, text, text, integer)",
		},
		"search_transcript_segments": {
			"public.gongmcp_search_transcript_segments(text, integer)",
			"public.gongmcp_search_transcript_segments_by_call_facts(text, text, text, text, text, text, text, integer)",
		},
		"search_transcripts_by_call_facts": {
			"public.gongmcp_search_transcript_segments_by_call_facts(text, text, text, text, text, text, text, integer)",
		},
		"search_transcripts_by_crm_context": {
			"public.gongmcp_search_transcript_segments_by_crm_context(text, text, text, integer)",
		},
		"search_transcripts_by_filters": businessAnalysisEvidenceFunctions,
		"suggest_filter_refinements":    businessAnalysisCoreFunctions,
		"summarize_call_facts":          profileSummaryFunctions,
		"summarize_calls_by_filters":    businessAnalysisDimensionFunctions,
		"summarize_calls_by_lifecycle":  profileRowsFunctions,
		"summarize_scorecard_activity": {
			"public.gongmcp_scorecard_activity_summary(text, integer)",
			"public.gongmcp_scorecard_activity_totals()",
		},
		"summarize_themes_by_dimension": businessAnalysisDimensionFunctions,
	}
	var signatures []string
	for _, name := range allowlist {
		signatures = append(signatures, functionsByTool[name]...)
	}
	return signatures
}

func copyPostgresFunctionSignatures(signatures []string) []string {
	out := make([]string, len(signatures))
	copy(out, signatures)
	return out
}

func postgresBusinessPilotScopedColumns(allowlist []string) bool {
	want := map[string]struct{}{
		"get_sync_status":              {},
		"summarize_call_facts":         {},
		"summarize_calls_by_lifecycle": {},
		"rank_transcript_backlog":      {},
	}
	if len(allowlist) != len(want) {
		return false
	}
	for _, name := range allowlist {
		if _, ok := want[name]; !ok {
			return false
		}
	}
	return true
}

func postgresBusinessPilotColumnSelectGrants() []postgres.ColumnSelectGrant {
	grants := []postgres.ColumnSelectGrant{
		{Table: "gongctl_schema_migrations", Column: "version"},
		{Table: "calls", Column: "started_at"},
		{Table: "calls", Column: "parties_count"},
		{Table: "users", Column: "user_id"},
		{Table: "users", Column: "title"},
		{Table: "transcripts", Column: "segment_count"},
		{Table: "gongmcp_call_context_objects", Column: "id"},
		{Table: "gongmcp_call_context_objects", Column: "call_id"},
		{Table: "gongmcp_call_context_objects", Column: "object_key"},
		{Table: "gongmcp_call_context_objects", Column: "object_type"},
		{Table: "gongmcp_call_context_fields", Column: "id"},
		{Table: "gongmcp_call_context_fields", Column: "call_id"},
		{Table: "gongmcp_call_context_fields", Column: "object_key"},
		{Table: "gongmcp_call_context_fields", Column: "field_name"},
		{Table: "gongmcp_call_context_fields", Column: "field_label"},
		{Table: "gongmcp_call_context_fields", Column: "field_type"},
		{Table: "gongmcp_call_context_fields", Column: "field_populated"},
		{Table: "crm_integrations", Column: "integration_id"},
		{Table: "crm_schema_objects", Column: "object_type"},
		{Table: "crm_schema_fields", Column: "field_name"},
		{Table: "gong_settings", Column: "kind"},
		{Table: "postgres_read_model_state", Column: "model_name"},
		{Table: "postgres_read_model_state", Column: "model_version"},
		{Table: "postgres_read_model_state", Column: "rebuilt_at"},
		{Table: "postgres_read_model_state", Column: "call_count"},
		{Table: "postgres_read_model_state", Column: "fact_count"},
		{Table: "postgres_read_model_state", Column: "missing_fact_call_count"},
		{Table: "postgres_read_model_state", Column: "orphan_fact_count"},
		{Table: "postgres_read_model_state", Column: "stale_reason"},
		{Table: "postgres_read_model_state", Column: "updated_at"},
	}
	for _, column := range []string{"id", "scope", "sync_key", "cursor", "from_value", "to_value", "request_context", "status", "started_at", "finished_at", "records_seen", "records_written", "error_text"} {
		grants = append(grants, postgres.ColumnSelectGrant{Table: "gongmcp_sync_runs", Column: column})
	}
	for _, column := range []string{"sync_key", "scope", "cursor", "last_run_id", "last_status", "last_error", "last_success_at", "updated_at"} {
		grants = append(grants, postgres.ColumnSelectGrant{Table: "gongmcp_sync_state", Column: column})
	}
	return grants
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
