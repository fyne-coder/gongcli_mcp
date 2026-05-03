package cli

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fyne-coder/gongcli_mcp/internal/gong"
	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
	"github.com/fyne-coder/gongcli_mcp/internal/syncsvc"
	transcriptsync "github.com/fyne-coder/gongcli_mcp/internal/transcripts"
	"gopkg.in/yaml.v3"
)

func (a *app) sync(ctx context.Context, args []string) error {
	if len(args) == 0 {
		fmt.Fprintln(a.err, "usage: gongctl sync [run|calls|users|transcripts|crm-integrations|crm-schema|settings|status]")
		return errUsage
	}

	switch args[0] {
	case "run":
		return a.syncRun(ctx, args[1:])
	case "calls":
		return a.syncCalls(ctx, args[1:])
	case "users":
		return a.syncUsers(ctx, args[1:])
	case "transcripts":
		return a.syncTranscripts(ctx, args[1:])
	case "crm-integrations":
		return a.syncCRMIntegrations(ctx, args[1:])
	case "crm-schema":
		return a.syncCRMSchema(ctx, args[1:])
	case "settings":
		return a.syncSettings(ctx, args[1:])
	case "status":
		return a.syncStatus(ctx, args[1:])
	default:
		fmt.Fprintf(a.err, "unknown sync command %q\n", args[0])
		return errUsage
	}
}

func (a *app) syncRun(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sync run", flag.ContinueOnError)
	fs.SetOutput(a.err)
	configPath := fs.String("config", "", "YAML sync-run config path")
	dryRun := fs.Bool("dry-run", false, "validate config and print the planned stages without calling Gong")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}

	config, err := loadSyncRunConfig(*configPath)
	if err != nil {
		return err
	}

	response := newSyncRunResponse(config, *dryRun)
	if *dryRun {
		return writeJSONValue(a.out, response)
	}

	for idx := range config.Steps {
		step := &config.Steps[idx]
		stepResult := &response.Steps[idx]
		if err := a.authorizeSyncRunStep(step); err != nil {
			stepResult.Status = "blocked"
			stepResult.Error = err.Error()
			return fmt.Errorf("sync run step %d (%s): %w", idx+1, step.Name, err)
		}
	}

	store, err := openSQLiteStore(ctx, config.DB)
	if err != nil {
		return err
	}
	defer store.Close()

	client, err := newClientFromEnv()
	if err != nil {
		return err
	}

	for idx := range config.Steps {
		step := &config.Steps[idx]
		stepResult := &response.Steps[idx]
		fmt.Fprintf(a.err, "sync run step %d/%d: %s (%s)\n", idx+1, len(config.Steps), step.Name, step.Action)
		if err := a.executeSyncRunStep(ctx, store, client, step, stepResult); err != nil {
			stepResult.Status = "error"
			stepResult.Error = err.Error()
			return fmt.Errorf("sync run step %d (%s): %w", idx+1, step.Name, err)
		}
	}

	return writeJSONValue(a.out, response)
}

func (a *app) syncCalls(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sync calls", flag.ContinueOnError)
	fs.SetOutput(a.err)
	dbPath := fs.String("db", "", "SQLite database path")
	from := fs.String("from", "", "from date, YYYY-MM-DD or RFC3339")
	to := fs.String("to", "", "to date, YYYY-MM-DD or RFC3339")
	preset := fs.String("preset", "", "sync preset: business, minimal, all")
	maxPages := fs.Int("max-pages", 0, "maximum pages to sync; 0 means all pages")
	allowSensitiveExport := fs.Bool("allow-sensitive-export", false, "allow extended CRM-context sync in restricted mode")
	includeParties := fs.Bool("include-parties", false, "request Gong call participant fields such as names, emails, and titles")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	if *from == "" {
		return fmt.Errorf("--from is required")
	}
	if *to == "" {
		return fmt.Errorf("--to is required")
	}

	presetName, requestContext, err := parseSyncCallsPreset(*preset)
	if err != nil {
		return err
	}
	if presetName == "business" || presetName == "all" {
		if err := a.requireSensitiveExport(
			fmt.Sprintf("sync calls --preset %s", presetName),
			*allowSensitiveExport,
			"these presets request Extended Gong context and can cache CRM field values",
		); err != nil {
			return err
		}
	}
	if *includeParties {
		if err := a.requireSensitiveExport(
			"sync calls --include-parties",
			*allowSensitiveExport,
			"participant fields can include names, emails, speaker IDs, and titles and are stored in raw call payloads",
		); err != nil {
			return err
		}
	}

	store, err := openSQLiteStore(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	client, err := newClientFromEnv()
	if err != nil {
		return err
	}

	result, err := syncsvc.SyncCalls(ctx, client, store, syncsvc.CallsParams{
		From:          *from,
		To:            *to,
		Context:       requestContext,
		Preset:        presetName,
		MaxPages:      *maxPages,
		ExposeParties: *includeParties,
	})
	fmt.Fprintf(
		a.err,
		"synced calls: pages=%d seen=%d written=%d preset=%s context=%s cursor=%s%s db=%s\n",
		result.Pages,
		result.RecordsSeen,
		result.RecordsWritten,
		presetName,
		displayContext(requestContext),
		displayCursor(result.Cursor),
		displayParticipantCaptureStatus(result.ParticipantCaptureStatus),
		*dbPath,
	)
	return err
}

func (a *app) syncUsers(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sync users", flag.ContinueOnError)
	fs.SetOutput(a.err)
	dbPath := fs.String("db", "", "SQLite database path")
	maxPages := fs.Int("max-pages", 0, "maximum pages to sync; 0 means all pages")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}

	store, err := openSQLiteStore(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	client, err := newClientFromEnv()
	if err != nil {
		return err
	}

	result, err := syncsvc.SyncUsers(ctx, client, store, syncsvc.UsersParams{
		Preset:   "full",
		MaxPages: *maxPages,
	})
	fmt.Fprintf(
		a.err,
		"synced users: pages=%d seen=%d written=%d cursor=%s db=%s\n",
		result.Pages,
		result.RecordsSeen,
		result.RecordsWritten,
		displayCursor(result.Cursor),
		*dbPath,
	)
	return err
}

func (a *app) syncCRMIntegrations(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sync crm-integrations", flag.ContinueOnError)
	fs.SetOutput(a.err)
	dbPath := fs.String("db", "", "SQLite database path")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}

	store, err := openSQLiteStore(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	client, err := newClientFromEnv()
	if err != nil {
		return err
	}

	result, err := syncsvc.SyncCRMIntegrations(ctx, client, store, syncsvc.CRMIntegrationsParams{})
	fmt.Fprintf(
		a.err,
		"synced crm integrations: seen=%d written=%d db=%s\n",
		result.RecordsSeen,
		result.RecordsWritten,
		*dbPath,
	)
	return err
}

func (a *app) syncCRMSchema(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sync crm-schema", flag.ContinueOnError)
	fs.SetOutput(a.err)
	dbPath := fs.String("db", "", "SQLite database path")
	integrationID := fs.String("integration-id", "", "Gong CRM integration ID")
	var objectTypes repeatedStringFlag
	fs.Var(&objectTypes, "object-type", "CRM object type; repeat or pass comma-separated values such as ACCOUNT,DEAL")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}

	store, err := openSQLiteStore(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	client, err := newClientFromEnv()
	if err != nil {
		return err
	}

	result, err := syncsvc.SyncCRMSchema(ctx, client, store, syncsvc.CRMSchemaParams{
		IntegrationID: *integrationID,
		ObjectTypes:   objectTypes,
	})
	fmt.Fprintf(
		a.err,
		"synced crm schema: objects=%d fields=%d integration_id=%s db=%s\n",
		result.RecordsSeen,
		result.RecordsWritten,
		*integrationID,
		*dbPath,
	)
	return err
}

func (a *app) syncSettings(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sync settings", flag.ContinueOnError)
	fs.SetOutput(a.err)
	dbPath := fs.String("db", "", "SQLite database path")
	kind := fs.String("kind", "", "settings kind: trackers, scorecards, workspaces")
	workspaceID := fs.String("workspace-id", "", "optional Gong workspace ID for tracker settings")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}

	store, err := openSQLiteStore(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	client, err := newClientFromEnv()
	if err != nil {
		return err
	}

	result, err := syncsvc.SyncSettings(ctx, client, store, syncsvc.SettingsParams{
		Kind:        *kind,
		WorkspaceID: *workspaceID,
	})
	fmt.Fprintf(
		a.err,
		"synced settings: kind=%s seen=%d written=%d db=%s\n",
		*kind,
		result.RecordsSeen,
		result.RecordsWritten,
		*dbPath,
	)
	return err
}

func (a *app) syncTranscripts(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sync transcripts", flag.ContinueOnError)
	fs.SetOutput(a.err)
	dbPath := fs.String("db", "", "SQLite database path")
	outDir := fs.String("out-dir", "", "directory for transcript JSON files")
	limit := fs.Int("limit", 100, "maximum missing transcripts to fetch")
	batchSize := fs.Int("batch-size", 100, "maximum call IDs per Gong transcript request")
	allowSensitiveExport := fs.Bool("allow-sensitive-export", false, "allow transcript sync in restricted mode")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	if strings.TrimSpace(*outDir) == "" {
		return fmt.Errorf("--out-dir is required")
	}
	if err := a.requireSensitiveExport("sync transcripts", *allowSensitiveExport, "it downloads and stores raw transcript payloads"); err != nil {
		return err
	}

	store, err := openSQLiteStore(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	client, err := newClientFromEnv()
	if err != nil {
		return err
	}

	result, err := transcriptsync.SyncMissingWithBatch(ctx, client, store, *outDir, *limit, *batchSize)
	fmt.Fprintf(
		a.err,
		"synced transcripts: considered=%d downloaded=%d stored=%d failed=%d requests=%d batch_size=%d out_dir=%s db=%s\n",
		result.Considered,
		result.Downloaded,
		result.Stored,
		result.Failed,
		result.Requests,
		result.BatchSize,
		*outDir,
		*dbPath,
	)
	return err
}

func (a *app) syncStatus(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sync status", flag.ContinueOnError)
	fs.SetOutput(a.err)
	dbPath := fs.String("db", "", "SQLite database path")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}

	store, err := openSQLiteReadOnlyStore(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	summary, err := syncsvc.Status(ctx, store)
	if err != nil {
		return err
	}

	response := syncStatusResponse{
		TotalCalls:                   summary.TotalCalls,
		TotalUsers:                   summary.TotalUsers,
		TotalTranscripts:             summary.TotalTranscripts,
		TotalTranscriptSegments:      summary.TotalTranscriptSegments,
		TotalEmbeddedCRMContextCalls: summary.TotalEmbeddedCRMContextCalls,
		TotalEmbeddedCRMObjects:      summary.TotalEmbeddedCRMObjects,
		TotalEmbeddedCRMFields:       summary.TotalEmbeddedCRMFields,
		TotalCRMIntegrations:         summary.TotalCRMIntegrations,
		TotalCRMSchemaObjects:        summary.TotalCRMSchemaObjects,
		TotalCRMSchemaFields:         summary.TotalCRMSchemaFields,
		TotalGongSettings:            summary.TotalGongSettings,
		TotalScorecards:              summary.TotalScorecards,
		MissingTranscripts:           summary.MissingTranscripts,
		RunningSyncRuns:              summary.RunningSyncRuns,
		ProfileReadiness:             summary.ProfileReadiness,
		PublicReadiness:              summary.PublicReadiness,
		AttributionCoverage:          summary.AttributionCoverage,
		LastRun:                      newSyncRunJSON(summary.LastRun),
		LastSuccessfulRun:            newSyncRunJSON(summary.LastSuccessfulRun),
		States:                       []syncStateJSON{},
	}
	for _, state := range summary.States {
		response.States = append(response.States, syncStateJSON{
			SyncKey:       state.SyncKey,
			Scope:         state.Scope,
			Cursor:        state.Cursor,
			LastRunID:     state.LastRunID,
			LastStatus:    state.LastStatus,
			LastError:     state.LastError,
			LastSuccessAt: state.LastSuccessAt,
			UpdatedAt:     state.UpdatedAt,
		})
	}
	return writeJSONValue(a.out, response)
}

func openSQLiteStore(ctx context.Context, path string) (*sqlite.Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("--db is required")
	}
	return sqlite.Open(ctx, path)
}

func openSQLiteReadOnlyStore(ctx context.Context, path string) (*sqlite.Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("--db is required")
	}
	return sqlite.OpenReadOnly(ctx, path)
}

func parseSyncCallsPreset(value string) (string, string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "business":
		return "business", "Extended", nil
	case "minimal":
		return "minimal", "", nil
	case "all":
		return "all", "Extended", nil
	default:
		return "", "", fmt.Errorf("--preset must be one of: business, minimal, all")
	}
}

func displayCursor(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func displayContext(value string) string {
	if strings.TrimSpace(value) == "" {
		return "none"
	}
	return value
}

func displayParticipantCaptureStatus(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return " participant_capture=" + strings.TrimSpace(value)
}

func loadSyncRunConfig(path string) (*syncRunConfig, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("--config is required")
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	body, err := os.ReadFile(absPath)
	if err != nil {
		return nil, err
	}

	var cfg syncRunConfig
	decoder := yaml.NewDecoder(bytes.NewReader(body))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decode sync-run config: %w", err)
	}

	cfg.configPath = absPath
	if cfg.Version == 0 {
		cfg.Version = 1
	}
	if cfg.Version != 1 {
		return nil, fmt.Errorf("sync-run config version %d is not supported", cfg.Version)
	}

	baseDir := filepath.Dir(absPath)
	cfg.DB = resolveConfigPath(baseDir, cfg.DB)
	if strings.TrimSpace(cfg.DB) == "" {
		return nil, fmt.Errorf("sync-run config db is required")
	}
	if len(cfg.Steps) == 0 {
		return nil, fmt.Errorf("sync-run config must contain at least one step")
	}

	for idx := range cfg.Steps {
		if err := normalizeSyncRunStep(baseDir, idx, &cfg.Steps[idx]); err != nil {
			return nil, err
		}
	}
	return &cfg, nil
}

func resolveConfigPath(baseDir string, value string) string {
	clean := strings.TrimSpace(value)
	if clean == "" {
		return ""
	}
	if filepath.IsAbs(clean) {
		return filepath.Clean(clean)
	}
	return filepath.Clean(filepath.Join(baseDir, clean))
}

func normalizeSyncRunStep(baseDir string, idx int, step *syncRunStepConfig) error {
	step.Name = strings.TrimSpace(step.Name)
	step.Action = strings.ToLower(strings.TrimSpace(step.Action))
	if step.Name == "" {
		step.Name = fmt.Sprintf("%s-%02d", step.Action, idx+1)
	}

	switch step.Action {
	case "calls":
		if strings.TrimSpace(step.From) == "" {
			return fmt.Errorf("sync-run step %q requires from", step.Name)
		}
		if strings.TrimSpace(step.To) == "" {
			return fmt.Errorf("sync-run step %q requires to", step.Name)
		}
		if step.MaxPages < 0 {
			return fmt.Errorf("sync-run step %q max_pages must be >= 0", step.Name)
		}
		preset := strings.TrimSpace(step.Preset)
		if preset == "" {
			preset = "minimal"
		}
		presetName, _, err := parseSyncCallsPreset(preset)
		if err != nil {
			return fmt.Errorf("sync-run step %q: %w", step.Name, err)
		}
		step.Preset = presetName
	case "users":
		if step.MaxPages < 0 {
			return fmt.Errorf("sync-run step %q max_pages must be >= 0", step.Name)
		}
	case "transcripts":
		step.OutDir = resolveConfigPath(baseDir, step.OutDir)
		if strings.TrimSpace(step.OutDir) == "" {
			return fmt.Errorf("sync-run step %q requires out_dir", step.Name)
		}
		if step.Limit < 0 {
			return fmt.Errorf("sync-run step %q limit must be >= 0", step.Name)
		}
		if step.BatchSize < 0 {
			return fmt.Errorf("sync-run step %q batch_size must be >= 0", step.Name)
		}
		if step.Limit == 0 {
			step.Limit = 100
		}
		if step.BatchSize == 0 {
			step.BatchSize = 100
		}
	case "crm-integrations":
	case "crm-schema":
		step.IntegrationID = strings.TrimSpace(step.IntegrationID)
		if step.IntegrationID == "" {
			return fmt.Errorf("sync-run step %q requires integration_id", step.Name)
		}
		step.ObjectTypes = cleanSyncRunStringList(step.ObjectTypes)
		if len(step.ObjectTypes) == 0 {
			return fmt.Errorf("sync-run step %q requires at least one object_types entry", step.Name)
		}
	case "settings":
		kind, err := normalizeSyncSettingsKind(step.SettingsKind)
		if err != nil {
			return fmt.Errorf("sync-run step %q: %w", step.Name, err)
		}
		step.SettingsKind = kind
		step.WorkspaceID = strings.TrimSpace(step.WorkspaceID)
	default:
		return fmt.Errorf("sync-run step %q action must be one of: calls, users, transcripts, crm-integrations, crm-schema, settings", step.Name)
	}
	return nil
}

func normalizeSyncSettingsKind(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "tracker", "trackers", "keywordtracker", "keywordtrackers", "keyword_trackers":
		return "trackers", nil
	case "scorecard", "scorecards":
		return "scorecards", nil
	case "workspace", "workspaces":
		return "workspaces", nil
	default:
		return "", fmt.Errorf("settings_kind must be one of: trackers, scorecards, workspaces")
	}
}

func cleanSyncRunStringList(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			clean := strings.TrimSpace(part)
			if clean == "" {
				continue
			}
			key := strings.ToUpper(clean)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, clean)
		}
	}
	return out
}

func newSyncRunResponse(config *syncRunConfig, dryRun bool) syncRunResponse {
	response := syncRunResponse{
		ConfigPath:  config.configPath,
		Version:     config.Version,
		Name:        config.Name,
		Description: config.Description,
		DBPath:      config.DB,
		DryRun:      dryRun,
		Steps:       make([]syncRunStepResult, 0, len(config.Steps)),
	}
	for idx, step := range config.Steps {
		response.Steps = append(response.Steps, syncRunStepResult{
			Index:                   idx + 1,
			Name:                    step.Name,
			Action:                  step.Action,
			Status:                  "planned",
			RequiresSensitiveExport: syncRunStepRequiresSensitiveExport(step),
			From:                    step.From,
			To:                      step.To,
			Preset:                  step.Preset,
			MaxPages:                step.MaxPages,
			IncludeParties:          step.IncludeParties,
			SettingsKind:            step.SettingsKind,
			WorkspaceID:             step.WorkspaceID,
			IntegrationID:           step.IntegrationID,
			ObjectTypes:             append([]string(nil), step.ObjectTypes...),
			OutDir:                  step.OutDir,
			Limit:                   step.Limit,
			BatchSize:               step.BatchSize,
		})
	}
	return response
}

func syncRunStepRequiresSensitiveExport(step syncRunStepConfig) bool {
	switch step.Action {
	case "transcripts":
		return true
	case "calls":
		return step.Preset == "business" || step.Preset == "all" || step.IncludeParties
	default:
		return false
	}
}

func (a *app) authorizeSyncRunStep(step *syncRunStepConfig) error {
	switch step.Action {
	case "transcripts":
		return a.requireSensitiveExport("sync run transcripts step", false, "it downloads and stores raw transcript payloads")
	case "calls":
		if step.Preset == "business" || step.Preset == "all" {
			return a.requireSensitiveExport(
				fmt.Sprintf("sync run calls step %q", step.Preset),
				false,
				"these presets request Extended Gong context and can cache CRM field values",
			)
		}
		if step.IncludeParties {
			return a.requireSensitiveExport(
				"sync run calls step include_parties",
				false,
				"participant fields can include names, emails, speaker IDs, and titles and are stored in raw call payloads",
			)
		}
	}
	return nil
}

func (a *app) executeSyncRunStep(ctx context.Context, store *sqlite.Store, client *gong.Client, step *syncRunStepConfig, result *syncRunStepResult) error {
	result.Status = "success"

	switch step.Action {
	case "calls":
		presetName, requestContext, err := parseSyncCallsPreset(step.Preset)
		if err != nil {
			return err
		}
		syncResult, err := syncsvc.SyncCalls(ctx, client, store, syncsvc.CallsParams{
			From:          step.From,
			To:            step.To,
			Context:       requestContext,
			Preset:        presetName,
			MaxPages:      step.MaxPages,
			ExposeParties: step.IncludeParties,
		})
		if err != nil {
			return err
		}
		populateSyncRunServiceResult(result, syncResult)
	case "users":
		syncResult, err := syncsvc.SyncUsers(ctx, client, store, syncsvc.UsersParams{
			Preset:   "full",
			MaxPages: step.MaxPages,
		})
		if err != nil {
			return err
		}
		populateSyncRunServiceResult(result, syncResult)
	case "crm-integrations":
		syncResult, err := syncsvc.SyncCRMIntegrations(ctx, client, store, syncsvc.CRMIntegrationsParams{})
		if err != nil {
			return err
		}
		populateSyncRunServiceResult(result, syncResult)
	case "crm-schema":
		syncResult, err := syncsvc.SyncCRMSchema(ctx, client, store, syncsvc.CRMSchemaParams{
			IntegrationID: step.IntegrationID,
			ObjectTypes:   step.ObjectTypes,
		})
		if err != nil {
			return err
		}
		populateSyncRunServiceResult(result, syncResult)
	case "settings":
		syncResult, err := syncsvc.SyncSettings(ctx, client, store, syncsvc.SettingsParams{
			Kind:        step.SettingsKind,
			WorkspaceID: step.WorkspaceID,
		})
		if err != nil {
			return err
		}
		populateSyncRunServiceResult(result, syncResult)
	case "transcripts":
		syncResult, err := transcriptsync.SyncMissingWithBatch(ctx, client, store, step.OutDir, step.Limit, step.BatchSize)
		if err != nil {
			return err
		}
		result.Scope = "transcripts"
		result.RunID = syncResult.RunID
		result.RecordsSeen = int64(syncResult.Considered)
		result.RecordsWritten = int64(syncResult.Stored)
		result.TranscriptResult = &syncRunTranscriptStats{
			Considered: syncResult.Considered,
			Downloaded: syncResult.Downloaded,
			Stored:     syncResult.Stored,
			Failed:     syncResult.Failed,
			Requests:   syncResult.Requests,
			BatchSize:  syncResult.BatchSize,
		}
	default:
		return fmt.Errorf("unsupported sync-run action %q", step.Action)
	}
	return nil
}

func populateSyncRunServiceResult(target *syncRunStepResult, source syncsvc.Result) {
	target.Scope = source.Scope
	target.SyncKey = source.SyncKey
	target.RunID = source.RunID
	target.Cursor = source.Cursor
	target.Pages = source.Pages
	target.RecordsSeen = source.RecordsSeen
	target.RecordsWritten = source.RecordsWritten
}

type syncStatusResponse struct {
	TotalCalls                   int64                      `json:"total_calls"`
	TotalUsers                   int64                      `json:"total_users"`
	TotalTranscripts             int64                      `json:"total_transcripts"`
	TotalTranscriptSegments      int64                      `json:"total_transcript_segments"`
	TotalEmbeddedCRMContextCalls int64                      `json:"total_embedded_crm_context_calls"`
	TotalEmbeddedCRMObjects      int64                      `json:"total_embedded_crm_objects"`
	TotalEmbeddedCRMFields       int64                      `json:"total_embedded_crm_fields"`
	TotalCRMIntegrations         int64                      `json:"total_crm_integrations"`
	TotalCRMSchemaObjects        int64                      `json:"total_crm_schema_objects"`
	TotalCRMSchemaFields         int64                      `json:"total_crm_schema_fields"`
	TotalGongSettings            int64                      `json:"total_gong_settings"`
	TotalScorecards              int64                      `json:"total_scorecards"`
	MissingTranscripts           int64                      `json:"missing_transcripts"`
	RunningSyncRuns              int64                      `json:"running_sync_runs"`
	ProfileReadiness             sqlite.ProfileReadiness    `json:"profile_readiness"`
	PublicReadiness              sqlite.PublicReadiness     `json:"public_readiness"`
	AttributionCoverage          sqlite.AttributionCoverage `json:"attribution_coverage"`
	LastRun                      *syncRunJSON               `json:"last_run,omitempty"`
	LastSuccessfulRun            *syncRunJSON               `json:"last_successful_run,omitempty"`
	States                       []syncStateJSON            `json:"states"`
}

type syncRunJSON struct {
	ID             int64  `json:"id"`
	Scope          string `json:"scope"`
	SyncKey        string `json:"sync_key"`
	Cursor         string `json:"cursor"`
	From           string `json:"from"`
	To             string `json:"to"`
	RequestContext string `json:"request_context"`
	Status         string `json:"status"`
	StartedAt      string `json:"started_at"`
	FinishedAt     string `json:"finished_at,omitempty"`
	RecordsSeen    int64  `json:"records_seen"`
	RecordsWritten int64  `json:"records_written"`
	ErrorText      string `json:"error_text,omitempty"`
}

type syncStateJSON struct {
	SyncKey       string `json:"sync_key"`
	Scope         string `json:"scope"`
	Cursor        string `json:"cursor"`
	LastRunID     int64  `json:"last_run_id"`
	LastStatus    string `json:"last_status"`
	LastError     string `json:"last_error,omitempty"`
	LastSuccessAt string `json:"last_success_at,omitempty"`
	UpdatedAt     string `json:"updated_at"`
}

type syncRunConfig struct {
	Version     int                 `yaml:"version" json:"version"`
	Name        string              `yaml:"name,omitempty" json:"name,omitempty"`
	Description string              `yaml:"description,omitempty" json:"description,omitempty"`
	DB          string              `yaml:"db" json:"db"`
	Steps       []syncRunStepConfig `yaml:"steps" json:"steps"`
	configPath  string
}

type syncRunStepConfig struct {
	Name           string   `yaml:"name,omitempty" json:"name,omitempty"`
	Action         string   `yaml:"action" json:"action"`
	From           string   `yaml:"from,omitempty" json:"from,omitempty"`
	To             string   `yaml:"to,omitempty" json:"to,omitempty"`
	Preset         string   `yaml:"preset,omitempty" json:"preset,omitempty"`
	MaxPages       int      `yaml:"max_pages,omitempty" json:"max_pages,omitempty"`
	OutDir         string   `yaml:"out_dir,omitempty" json:"out_dir,omitempty"`
	Limit          int      `yaml:"limit,omitempty" json:"limit,omitempty"`
	BatchSize      int      `yaml:"batch_size,omitempty" json:"batch_size,omitempty"`
	IncludeParties bool     `yaml:"include_parties,omitempty" json:"include_parties,omitempty"`
	IntegrationID  string   `yaml:"integration_id,omitempty" json:"integration_id,omitempty"`
	ObjectTypes    []string `yaml:"object_types,omitempty" json:"object_types,omitempty"`
	SettingsKind   string   `yaml:"settings_kind,omitempty" json:"settings_kind,omitempty"`
	WorkspaceID    string   `yaml:"workspace_id,omitempty" json:"workspace_id,omitempty"`
}

type syncRunResponse struct {
	ConfigPath  string              `json:"config_path"`
	Version     int                 `json:"version"`
	Name        string              `json:"name,omitempty"`
	Description string              `json:"description,omitempty"`
	DBPath      string              `json:"db_path"`
	DryRun      bool                `json:"dry_run"`
	Steps       []syncRunStepResult `json:"steps"`
}

type syncRunStepResult struct {
	Index                   int                     `json:"index"`
	Name                    string                  `json:"name"`
	Action                  string                  `json:"action"`
	Status                  string                  `json:"status"`
	RequiresSensitiveExport bool                    `json:"requires_sensitive_export"`
	From                    string                  `json:"from,omitempty"`
	To                      string                  `json:"to,omitempty"`
	Preset                  string                  `json:"preset,omitempty"`
	MaxPages                int                     `json:"max_pages,omitempty"`
	SettingsKind            string                  `json:"settings_kind,omitempty"`
	WorkspaceID             string                  `json:"workspace_id,omitempty"`
	IntegrationID           string                  `json:"integration_id,omitempty"`
	ObjectTypes             []string                `json:"object_types,omitempty"`
	OutDir                  string                  `json:"out_dir,omitempty"`
	Limit                   int                     `json:"limit,omitempty"`
	BatchSize               int                     `json:"batch_size,omitempty"`
	IncludeParties          bool                    `json:"include_parties,omitempty"`
	Scope                   string                  `json:"scope,omitempty"`
	SyncKey                 string                  `json:"sync_key,omitempty"`
	RunID                   int64                   `json:"run_id,omitempty"`
	Cursor                  string                  `json:"cursor,omitempty"`
	Pages                   int                     `json:"pages,omitempty"`
	RecordsSeen             int64                   `json:"records_seen,omitempty"`
	RecordsWritten          int64                   `json:"records_written,omitempty"`
	TranscriptResult        *syncRunTranscriptStats `json:"transcript_result,omitempty"`
	Error                   string                  `json:"error,omitempty"`
}

type syncRunTranscriptStats struct {
	Considered int `json:"considered"`
	Downloaded int `json:"downloaded"`
	Stored     int `json:"stored"`
	Failed     int `json:"failed"`
	Requests   int `json:"requests"`
	BatchSize  int `json:"batch_size"`
}

type repeatedStringFlag []string

func (f *repeatedStringFlag) String() string {
	if f == nil {
		return ""
	}
	return strings.Join(*f, ",")
}

func (f *repeatedStringFlag) Set(value string) error {
	for _, part := range strings.Split(value, ",") {
		clean := strings.TrimSpace(part)
		if clean != "" {
			*f = append(*f, clean)
		}
	}
	return nil
}

func newSyncRunJSON(run *sqlite.SyncRun) *syncRunJSON {
	if run == nil {
		return nil
	}
	return &syncRunJSON{
		ID:             run.ID,
		Scope:          run.Scope,
		SyncKey:        run.SyncKey,
		Cursor:         run.Cursor,
		From:           run.From,
		To:             run.To,
		RequestContext: run.RequestContext,
		Status:         run.Status,
		StartedAt:      run.StartedAt,
		FinishedAt:     run.FinishedAt,
		RecordsSeen:    run.RecordsSeen,
		RecordsWritten: run.RecordsWritten,
		ErrorText:      run.ErrorText,
	}
}
