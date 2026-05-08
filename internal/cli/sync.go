package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fyne-coder/gongcli_mcp/internal/gong"
	"github.com/fyne-coder/gongcli_mcp/internal/governance"
	"github.com/fyne-coder/gongcli_mcp/internal/store/postgres"
	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
	"github.com/fyne-coder/gongcli_mcp/internal/store/storeiface"
	"github.com/fyne-coder/gongcli_mcp/internal/syncsvc"
	transcriptsync "github.com/fyne-coder/gongcli_mcp/internal/transcripts"
	"gopkg.in/yaml.v3"
)

func (a *app) sync(ctx context.Context, args []string) error {
	if len(args) == 0 {
		fmt.Fprintln(a.err, "usage: gongctl sync [run|calls|users|transcripts|crm-integrations|crm-schema|settings|scorecard-activity|status|read-model|synthetic]")
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
	case "scorecard-activity":
		return a.syncScorecardActivity(ctx, args[1:])
	case "status":
		return a.syncStatus(ctx, args[1:])
	case "read-model":
		return a.syncReadModel(ctx, args[1:])
	case "synthetic":
		return a.syncSynthetic(ctx, args[1:])
	default:
		fmt.Fprintf(a.err, "unknown sync command %q\n", args[0])
		return errUsage
	}
}

func (a *app) syncReadModel(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sync read-model", flag.ContinueOnError)
	fs.SetOutput(a.err)
	dbPath := fs.String("db", "", "SQLite database path; read-model checks are Postgres-only when omitted")
	rebuild := fs.Bool("rebuild", false, "rebuild the Postgres read model using a writable database URL")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	if strings.TrimSpace(*dbPath) != "" {
		return errors.New("sync read-model is only supported for Postgres; omit --db and set GONG_DATABASE_URL or DATABASE_URL")
	}
	databaseURL := postgres.URLFromEnv(os.Getenv)
	if databaseURL == "" {
		return errors.New("GONG_DATABASE_URL or DATABASE_URL is required for sync read-model")
	}

	var status *postgres.ReadModelStatus
	var err error
	action := "check"
	if *rebuild {
		action = "rebuild"
		store, openErr := postgres.Open(ctx, databaseURL)
		if openErr != nil {
			return openErr
		}
		defer store.Close()
		status, err = store.RebuildReadModel(ctx)
	} else {
		store, openErr := postgres.OpenStatus(ctx, databaseURL)
		if openErr != nil {
			return openErr
		}
		defer store.Close()
		status, err = store.ReadModelStatus(ctx)
	}
	if err != nil {
		return err
	}
	state := "stale"
	if status.Ready {
		if *rebuild {
			state = "rebuilt"
		} else {
			state = "current"
		}
	}
	return writeJSONValue(a.out, map[string]any{
		"backend":    "postgres",
		"action":     action,
		"status":     state,
		"read_model": status,
	})
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

func (a *app) syncScorecardActivity(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sync scorecard-activity", flag.ContinueOnError)
	fs.SetOutput(a.err)
	dbPath := fs.String("db", "", "SQLite database path; omit when using GONG_DATABASE_URL or DATABASE_URL for Postgres")
	callFrom := fs.String("call-from", "", "inclusive call from date, YYYY-MM-DD in Gong company timezone")
	callTo := fs.String("call-to", "", "exclusive call to date, YYYY-MM-DD in Gong company timezone")
	reviewFrom := fs.String("review-from", "", "optional inclusive review from date, YYYY-MM-DD in Gong company timezone")
	reviewTo := fs.String("review-to", "", "optional exclusive review to date, YYYY-MM-DD in Gong company timezone")
	reviewMethod := fs.String("review-method", "BOTH", "review method: AUTOMATIC, MANUAL, BOTH")
	maxPages := fs.Int("max-pages", 0, "maximum pages to sync; 0 means all pages")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	if strings.TrimSpace(*callFrom) == "" {
		return fmt.Errorf("--call-from is required")
	}
	if strings.TrimSpace(*callTo) == "" {
		return fmt.Errorf("--call-to is required")
	}

	store, err := openWritableScorecardActivityStore(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	client, err := newClientFromEnv()
	if err != nil {
		return err
	}

	result, err := syncsvc.SyncScorecardActivity(ctx, client, store, syncsvc.ScorecardActivityParams{
		CallFrom:     *callFrom,
		CallTo:       *callTo,
		ReviewFrom:   *reviewFrom,
		ReviewTo:     *reviewTo,
		ReviewMethod: *reviewMethod,
		MaxPages:     *maxPages,
	})
	fmt.Fprintf(
		a.err,
		"synced scorecard activity: pages=%d seen=%d written=%d review_method=%s cursor=%s db=%s\n",
		result.Pages,
		result.RecordsSeen,
		result.RecordsWritten,
		strings.ToUpper(strings.TrimSpace(*reviewMethod)),
		displayCursor(result.Cursor),
		displayStoreTarget(*dbPath),
	)
	return err
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
	includeHighlights := fs.Bool("include-highlights", false, "request Gong AI Highlights/brief/next-step fields and store them in raw call payloads when Gong returns them; replaces the deprecated pointsOfInterest/actionItems contract")
	governanceConfig := fs.String("governance-config", "", "private AI governance YAML config for ingest-time call exclusion")
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
	if *includeHighlights {
		if err := a.requireSensitiveExport(
			"sync calls --include-highlights",
			*allowSensitiveExport,
			"Gong AI Highlights/brief/next-step fields can include customer-facing summaries and are stored in raw call payloads",
		); err != nil {
			return err
		}
	}
	var ingestGovernance *governance.Config
	if strings.TrimSpace(*governanceConfig) != "" {
		var err error
		ingestGovernance, err = governance.LoadFile(*governanceConfig)
		if err != nil {
			return err
		}
	}

	store, err := openWritableCoreStore(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	client, err := newClientFromEnv()
	if err != nil {
		return err
	}

	result, err := syncsvc.SyncCalls(ctx, client, store, syncsvc.CallsParams{
		From:             *from,
		To:               *to,
		Context:          requestContext,
		Preset:           presetName,
		MaxPages:         *maxPages,
		ExposeParties:    *includeParties,
		ExposeHighlights: *includeHighlights,
		Governance:       ingestGovernance,
	})
	fmt.Fprintf(
		a.err,
		"synced calls: pages=%d seen=%d written=%d skipped=%d preset=%s context=%s cursor=%s%s db=%s\n",
		result.Pages,
		result.RecordsSeen,
		result.RecordsWritten,
		result.RecordsSkipped,
		presetName,
		displayContext(requestContext),
		displayCursor(result.Cursor),
		displayParticipantCaptureStatus(result.ParticipantCaptureStatus),
		displayStoreTarget(*dbPath),
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

	store, err := openWritableCoreStore(ctx, *dbPath)
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
		displayStoreTarget(*dbPath),
	)
	return err
}

func (a *app) syncCRMIntegrations(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sync crm-integrations", flag.ContinueOnError)
	fs.SetOutput(a.err)
	dbPath := fs.String("db", "", "SQLite database path; omit when using GONG_DATABASE_URL or DATABASE_URL for Postgres")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}

	store, err := openWritableCRMIntegrationStore(ctx, *dbPath)
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
		displayStoreTarget(*dbPath),
	)
	return err
}

func (a *app) syncCRMSchema(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sync crm-schema", flag.ContinueOnError)
	fs.SetOutput(a.err)
	dbPath := fs.String("db", "", "SQLite database path; omit when using GONG_DATABASE_URL or DATABASE_URL for Postgres")
	integrationID := fs.String("integration-id", "", "Gong CRM integration ID")
	var objectTypes repeatedStringFlag
	fs.Var(&objectTypes, "object-type", "CRM object type; repeat or pass comma-separated values such as ACCOUNT,DEAL")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}

	store, err := openWritableCRMSchemaStore(ctx, *dbPath)
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
		displayStoreTarget(*dbPath),
	)
	return err
}

func (a *app) syncSettings(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sync settings", flag.ContinueOnError)
	fs.SetOutput(a.err)
	dbPath := fs.String("db", "", "SQLite database path; omit when using GONG_DATABASE_URL or DATABASE_URL for Postgres")
	kind := fs.String("kind", "", "settings kind: trackers, scorecards, workspaces")
	workspaceID := fs.String("workspace-id", "", "optional Gong workspace ID for tracker settings")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}

	store, err := openWritableSettingsStore(ctx, *dbPath)
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
		displayStoreTarget(*dbPath),
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
	governanceConfig := fs.String("governance-config", "", "private AI governance YAML config; transcript sync skips calls ledgered by governed call sync")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	var ingestGovernance *governance.Config
	if strings.TrimSpace(*governanceConfig) != "" {
		loadedGovernance, err := governance.LoadFile(*governanceConfig)
		if err != nil {
			return err
		}
		ingestGovernance = loadedGovernance
	}
	if strings.TrimSpace(*outDir) == "" {
		return fmt.Errorf("--out-dir is required")
	}
	if err := a.requireSensitiveExport("sync transcripts", *allowSensitiveExport, "it downloads and stores raw transcript payloads"); err != nil {
		return err
	}

	store, err := openWritableTranscriptStore(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	client, err := newClientFromEnv()
	if err != nil {
		return err
	}

	result, err := transcriptsync.SyncMissingWithBatchGoverned(ctx, client, store, *outDir, *limit, *batchSize, ingestGovernance)
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
		displayStoreTarget(*dbPath),
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

	store, err := openReadOnlyStatusStore(ctx, *dbPath)
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
		TotalScorecardActivity:       summary.TotalScorecardActivity,
		TotalAIHighlights:            summary.TotalAIHighlights,
		MissingTranscripts:           summary.MissingTranscripts,
		RunningSyncRuns:              summary.RunningSyncRuns,
		CallFactsAttribution:         summary.CallFactsAttribution,
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
	if readModelStore, ok := store.(interface {
		ReadModelStatus(context.Context) (*postgres.ReadModelStatus, error)
	}); ok {
		readModel, err := readModelStore.ReadModelStatus(ctx)
		if err != nil {
			return err
		}
		response.PostgresReadModel = readModel
	}
	return writeJSONValue(a.out, response)
}

func (a *app) syncSynthetic(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sync synthetic", flag.ContinueOnError)
	fs.SetOutput(a.err)
	dbPath := fs.String("db", "", "SQLite database path; omit when using GONG_DATABASE_URL or DATABASE_URL for Postgres")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}

	store, err := openWritableCoreStore(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	run, err := store.StartSyncRun(ctx, sqlite.StartSyncRunParams{
		Scope:          "synthetic",
		SyncKey:        "synthetic:postgres-vertical-slice",
		RequestContext: "synthetic_fixture=true",
	})
	if err != nil {
		return err
	}
	written := int64(0)
	finishStatus := "success"
	finishError := ""
	defer func() {
		_ = store.FinishSyncRun(ctx, run.ID, sqlite.FinishSyncRunParams{
			Status:         finishStatus,
			RecordsSeen:    written,
			RecordsWritten: written,
			ErrorText:      finishError,
			RequestContext: "synthetic_fixture=true",
		})
	}()

	for _, raw := range syntheticCallPayloads() {
		if _, err := store.UpsertCall(ctx, raw); err != nil {
			finishStatus = "error"
			finishError = err.Error()
			return err
		}
		written++
	}
	for _, raw := range syntheticUserPayloads() {
		if _, err := store.UpsertUser(ctx, raw); err != nil {
			finishStatus = "error"
			finishError = err.Error()
			return err
		}
		written++
	}
	transcriptStore, ok := store.(interface {
		UpsertTranscript(context.Context, json.RawMessage) (*sqlite.TranscriptRecord, error)
	})
	if !ok {
		return errors.New("selected store cannot write transcripts")
	}
	for _, raw := range syntheticTranscriptPayloads() {
		if _, err := transcriptStore.UpsertTranscript(ctx, raw); err != nil {
			finishStatus = "error"
			finishError = err.Error()
			return err
		}
		written++
	}
	if settingsStore, ok := store.(interface {
		UpsertGongSetting(context.Context, string, json.RawMessage) (*sqlite.GongSettingRecord, error)
	}); ok {
		for _, raw := range syntheticScorecardSettingPayloads() {
			if _, err := settingsStore.UpsertGongSetting(ctx, "scorecards", raw); err != nil {
				finishStatus = "error"
				finishError = err.Error()
				return err
			}
			written++
		}
	}
	if activityStore, ok := store.(interface {
		UpsertScorecardActivity(context.Context, json.RawMessage) (*sqlite.ScorecardActivityRecord, error)
	}); ok {
		for _, raw := range syntheticScorecardActivityPayloads() {
			if _, err := activityStore.UpsertScorecardActivity(ctx, raw); err != nil {
				finishStatus = "error"
				finishError = err.Error()
				return err
			}
			written++
		}
	}
	if crmIntegrationStore, ok := store.(interface {
		UpsertCRMIntegration(context.Context, json.RawMessage) (*sqlite.CRMIntegrationRecord, error)
	}); ok {
		for _, raw := range syntheticCRMIntegrationPayloads() {
			if _, err := crmIntegrationStore.UpsertCRMIntegration(ctx, raw); err != nil {
				finishStatus = "error"
				finishError = err.Error()
				return err
			}
			written++
		}
	}
	if crmSchemaStore, ok := store.(interface {
		UpsertCRMSchema(context.Context, string, string, json.RawMessage) (int64, error)
	}); ok {
		for _, fixture := range syntheticCRMSchemaPayloads() {
			fieldCount, err := crmSchemaStore.UpsertCRMSchema(ctx, fixture.integrationID, fixture.objectType, fixture.raw)
			if err != nil {
				finishStatus = "error"
				finishError = err.Error()
				return err
			}
			written += fieldCount
		}
	}

	fmt.Fprintf(a.err, "synced synthetic fixture: records_written=%d db=%s\n", written, displayStoreTarget(*dbPath))
	return writeJSONValue(a.out, map[string]any{
		"status":          "ok",
		"records_written": written,
		"db":              displayStoreTarget(*dbPath),
	})
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

type writableCoreStore interface {
	storeiface.SyncStore
	storeiface.Closer
}

type writableTranscriptStore interface {
	storeiface.TranscriptStore
	storeiface.Closer
}

type writableSettingsStore interface {
	storeiface.SettingsStore
	storeiface.Closer
}

type writableCRMIntegrationStore interface {
	storeiface.CRMIntegrationStore
	storeiface.Closer
}

type writableCRMSchemaStore interface {
	storeiface.CRMSchemaStore
	storeiface.Closer
}

type writableScorecardActivityStore interface {
	storeiface.ScorecardActivityStore
	storeiface.Closer
}

type readOnlyStatusStore interface {
	storeiface.SyncStatusReader
	storeiface.Closer
}

type readOnlyTranscriptSearchStore interface {
	SearchTranscriptSegments(context.Context, string, int) ([]sqlite.TranscriptSearchResult, error)
	storeiface.Closer
}

type readOnlyCallSearchStore interface {
	storeiface.CallSearcher
	storeiface.Closer
}

type readOnlyCallDetailStore interface {
	GetCallDetail(context.Context, string) (*sqlite.CallDetail, error)
	storeiface.Closer
}

func openWritableCoreStore(ctx context.Context, path string) (writableCoreStore, error) {
	if strings.TrimSpace(path) != "" {
		return sqlite.Open(ctx, path)
	}
	databaseURL := postgres.URLFromEnv(os.Getenv)
	if databaseURL == "" {
		return nil, errors.New("--db is required")
	}
	return postgres.Open(ctx, databaseURL)
}

func openWritableTranscriptStore(ctx context.Context, path string) (writableTranscriptStore, error) {
	if strings.TrimSpace(path) != "" {
		return sqlite.Open(ctx, path)
	}
	databaseURL := postgres.URLFromEnv(os.Getenv)
	if databaseURL == "" {
		return nil, errors.New("--db is required")
	}
	return postgres.Open(ctx, databaseURL)
}

func openWritableSettingsStore(ctx context.Context, path string) (writableSettingsStore, error) {
	if strings.TrimSpace(path) != "" {
		return sqlite.Open(ctx, path)
	}
	databaseURL := postgres.URLFromEnv(os.Getenv)
	if databaseURL == "" {
		return nil, errors.New("--db is required")
	}
	return postgres.Open(ctx, databaseURL)
}

func openWritableCRMIntegrationStore(ctx context.Context, path string) (writableCRMIntegrationStore, error) {
	if strings.TrimSpace(path) != "" {
		return sqlite.Open(ctx, path)
	}
	databaseURL := postgres.URLFromEnv(os.Getenv)
	if databaseURL == "" {
		return nil, errors.New("--db is required")
	}
	return postgres.Open(ctx, databaseURL)
}

func openWritableCRMSchemaStore(ctx context.Context, path string) (writableCRMSchemaStore, error) {
	if strings.TrimSpace(path) != "" {
		return sqlite.Open(ctx, path)
	}
	databaseURL := postgres.URLFromEnv(os.Getenv)
	if databaseURL == "" {
		return nil, errors.New("--db is required")
	}
	return postgres.Open(ctx, databaseURL)
}

func openWritableScorecardActivityStore(ctx context.Context, path string) (writableScorecardActivityStore, error) {
	if strings.TrimSpace(path) != "" {
		return sqlite.Open(ctx, path)
	}
	databaseURL := postgres.URLFromEnv(os.Getenv)
	if databaseURL == "" {
		return nil, errors.New("--db is required")
	}
	return postgres.Open(ctx, databaseURL)
}

func openReadOnlyStatusStore(ctx context.Context, path string) (readOnlyStatusStore, error) {
	if strings.TrimSpace(path) != "" {
		return sqlite.OpenReadOnly(ctx, path)
	}
	databaseURL := postgres.URLFromEnv(os.Getenv)
	if databaseURL == "" {
		return nil, errors.New("--db is required")
	}
	return postgres.OpenReadOnly(ctx, databaseURL)
}

func openReadOnlyTranscriptSearchStore(ctx context.Context, path string) (readOnlyTranscriptSearchStore, error) {
	if strings.TrimSpace(path) != "" {
		return sqlite.OpenReadOnly(ctx, path)
	}
	databaseURL := postgres.URLFromEnv(os.Getenv)
	if databaseURL == "" {
		return nil, errors.New("--db is required")
	}
	return postgres.OpenReadOnly(ctx, databaseURL)
}

func openReadOnlyCallSearchStore(ctx context.Context, path string) (readOnlyCallSearchStore, error) {
	if strings.TrimSpace(path) != "" {
		return sqlite.OpenReadOnly(ctx, path)
	}
	databaseURL := postgres.URLFromEnv(os.Getenv)
	if databaseURL == "" {
		return nil, errors.New("--db is required")
	}
	return postgres.OpenReadOnly(ctx, databaseURL)
}

func openReadOnlyCallDetailStore(ctx context.Context, path string) (readOnlyCallDetailStore, error) {
	if strings.TrimSpace(path) != "" {
		return sqlite.OpenReadOnly(ctx, path)
	}
	databaseURL := postgres.URLFromEnv(os.Getenv)
	if databaseURL == "" {
		return nil, errors.New("--db is required")
	}
	return postgres.OpenReadOnly(ctx, databaseURL)
}

func displayStoreTarget(path string) string {
	if strings.TrimSpace(path) != "" {
		return path
	}
	if postgres.URLFromEnv(os.Getenv) != "" {
		return "postgres"
	}
	return ""
}

func syntheticCallPayloads() []json.RawMessage {
	return []json.RawMessage{
		json.RawMessage(`{"id":"synthetic-call-001","title":"Pulsaris implementation kickoff","started":"2026-01-15T15:00:00Z","duration":1800,"parties":[{"id":"speaker-1"}]}`),
		json.RawMessage(`{"id":"synthetic-call-002","title":"Pulsaris renewal risk review","started":"2026-01-20T16:00:00Z","duration":2400,"parties":[{"id":"speaker-1"},{"id":"speaker-2"}]}`),
	}
}

func syntheticUserPayloads() []json.RawMessage {
	return []json.RawMessage{
		json.RawMessage(`{"id":"synthetic-user-001","emailAddress":"alex.operator@example.invalid","firstName":"Alex","lastName":"Operator","title":"RevOps Lead","active":true}`),
	}
}

func syntheticTranscriptPayloads() []json.RawMessage {
	return []json.RawMessage{
		json.RawMessage(`{"callId":"synthetic-call-001","transcript":[{"speakerId":"speaker-1","sentences":[{"start":0,"end":5000,"text":"We need a shared Postgres deployment path for the MCP server and sync job."},{"start":5000,"end":9000,"text":"SQLite remains useful for local pilots."}]}]}`),
		json.RawMessage(`{"callId":"synthetic-call-002","transcript":[{"speakerId":"speaker-2","sentences":[{"start":0,"end":7000,"text":"The renewal risk is tied to transcript coverage and implementation evidence."}]}]}`),
	}
}

func syntheticScorecardSettingPayloads() []json.RawMessage {
	return []json.RawMessage{
		json.RawMessage(`{"id":"synthetic-generic-setting-id-001","scorecardId":"synthetic-scorecard-001","scorecardName":"Synthetic discovery quality","enabled":true,"reviewMethod":"MANUAL","workspaceId":"synthetic-workspace-001","created":"2026-01-01T00:00:00Z","updated":"2026-01-02T00:00:00Z","questions":[{"questionId":"synthetic-question-001","questionText":"Did the seller confirm the implementation timeline?","questionType":"SCALE","minRange":1.5,"maxRange":"N/A","answerGuide":"Synthetic scoring guidance only."}]}`),
	}
}

func syntheticScorecardActivityPayloads() []json.RawMessage {
	return []json.RawMessage{
		json.RawMessage(`{"answeredScorecardId":"synthetic-answered-scorecard-001","scorecardId":"synthetic-scorecard-001","scorecardName":"Synthetic discovery quality","callId":"synthetic-call-001","callStartTime":"2026-01-15T15:00:00Z","reviewedUserId":"synthetic-user-001","reviewerUserId":"synthetic-reviewer-001","reviewMethod":"MANUAL","reviewTime":"2026-01-16T15:00:00Z","visibilityType":"PUBLIC","answers":[{"questionId":"synthetic-question-001","isOverall":true,"score":4,"notApplicable":false},{"questionId":"synthetic-question-002","score":5,"notApplicable":false}]}`),
		json.RawMessage(`{"answeredScorecardId":"synthetic-answered-scorecard-002","scorecardId":"synthetic-scorecard-001","scorecardName":"Synthetic discovery quality","callId":"synthetic-call-002","callStartTime":"2026-01-16T15:00:00Z","reviewedUserId":"synthetic-user-002","reviewerUserId":"synthetic-reviewer-001","reviewMethod":"AUTOMATIC","reviewTime":"2026-01-17T15:00:00Z","visibilityType":"PUBLIC","answers":[{"questionId":"synthetic-question-001","isOverall":true,"score":3,"notApplicable":false},{"questionId":"synthetic-question-002","score":3,"notApplicable":false}]}`),
	}
}

func syntheticCRMIntegrationPayloads() []json.RawMessage {
	return []json.RawMessage{
		json.RawMessage(`{"integrationId":"synthetic-crm-integration-001","name":"Synthetic Salesforce","crmType":"Salesforce","updated":"2026-01-03T00:00:00Z"}`),
	}
}

type syntheticCRMSchemaPayload struct {
	integrationID string
	objectType    string
	raw           json.RawMessage
}

func syntheticCRMSchemaPayloads() []syntheticCRMSchemaPayload {
	return []syntheticCRMSchemaPayload{
		{
			integrationID: "synthetic-crm-integration-001",
			objectType:    "Opportunity",
			raw:           json.RawMessage(`{"displayName":"Opportunity","objectTypeToSelectedFields":{"Opportunity":[{"fieldName":"StageName","label":"Stage","fieldType":"picklist"},{"fieldName":"CloseDate","label":"Close Date","fieldType":"date"},{"fieldName":"Amount","label":"Amount","fieldType":"currency"}]}}`),
		},
		{
			integrationID: "synthetic-crm-integration-001",
			objectType:    "Account",
			raw:           json.RawMessage(`{"displayName":"Account","objectTypeToSelectedFields":{"Account":[{"fieldName":"Industry","label":"Industry","fieldType":"picklist"},{"fieldName":"Account_Type__c","label":"Account Type","fieldType":"string"}]}}`),
		},
	}
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
		step.GovernanceConfig = resolveConfigPath(baseDir, step.GovernanceConfig)
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
		step.GovernanceConfig = resolveConfigPath(baseDir, step.GovernanceConfig)
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
	case "scorecard-activity":
		if strings.TrimSpace(step.CallFrom) == "" {
			step.CallFrom = strings.TrimSpace(step.From)
		}
		if strings.TrimSpace(step.CallTo) == "" {
			step.CallTo = strings.TrimSpace(step.To)
		}
		if strings.TrimSpace(step.CallFrom) == "" {
			return fmt.Errorf("sync-run step %q requires call_from", step.Name)
		}
		if strings.TrimSpace(step.CallTo) == "" {
			return fmt.Errorf("sync-run step %q requires call_to", step.Name)
		}
		step.ReviewFrom = strings.TrimSpace(step.ReviewFrom)
		step.ReviewTo = strings.TrimSpace(step.ReviewTo)
		if step.MaxPages < 0 {
			return fmt.Errorf("sync-run step %q max_pages must be >= 0", step.Name)
		}
		reviewMethod, err := normalizeSyncRunReviewMethod(step.ReviewMethod)
		if err != nil {
			return fmt.Errorf("sync-run step %q: %w", step.Name, err)
		}
		step.ReviewMethod = reviewMethod
	default:
		return fmt.Errorf("sync-run step %q action must be one of: calls, users, transcripts, crm-integrations, crm-schema, settings, scorecard-activity", step.Name)
	}
	return nil
}

func normalizeSyncRunReviewMethod(value string) (string, error) {
	method := strings.ToUpper(strings.TrimSpace(value))
	if method == "" {
		return "BOTH", nil
	}
	switch method {
	case "AUTOMATIC", "MANUAL", "BOTH":
		return method, nil
	default:
		return "", fmt.Errorf("review_method must be one of: AUTOMATIC, MANUAL, BOTH")
	}
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
			IncludeHighlights:       step.IncludeHighlights,
			GovernanceConfig:        step.GovernanceConfig,
			SettingsKind:            step.SettingsKind,
			WorkspaceID:             step.WorkspaceID,
			CallFrom:                step.CallFrom,
			CallTo:                  step.CallTo,
			ReviewFrom:              step.ReviewFrom,
			ReviewTo:                step.ReviewTo,
			ReviewMethod:            step.ReviewMethod,
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
		return step.Preset == "business" || step.Preset == "all" || step.IncludeParties || step.IncludeHighlights
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
		if step.IncludeHighlights {
			return a.requireSensitiveExport(
				"sync run calls step include_highlights",
				false,
				"Gong AI Highlights/brief/next-step fields can include customer-facing summaries and are stored in raw call payloads",
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
		ingestGovernance, err := loadOptionalGovernanceConfig(step.GovernanceConfig)
		if err != nil {
			return err
		}
		syncResult, err := syncsvc.SyncCalls(ctx, client, store, syncsvc.CallsParams{
			From:             step.From,
			To:               step.To,
			Context:          requestContext,
			Preset:           presetName,
			MaxPages:         step.MaxPages,
			ExposeParties:    step.IncludeParties,
			ExposeHighlights: step.IncludeHighlights,
			Governance:       ingestGovernance,
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
	case "scorecard-activity":
		syncResult, err := syncsvc.SyncScorecardActivity(ctx, client, store, syncsvc.ScorecardActivityParams{
			CallFrom:     step.CallFrom,
			CallTo:       step.CallTo,
			ReviewFrom:   step.ReviewFrom,
			ReviewTo:     step.ReviewTo,
			ReviewMethod: step.ReviewMethod,
			MaxPages:     step.MaxPages,
		})
		if err != nil {
			return err
		}
		populateSyncRunServiceResult(result, syncResult)
	case "transcripts":
		ingestGovernance, err := loadOptionalGovernanceConfig(step.GovernanceConfig)
		if err != nil {
			return err
		}
		syncResult, err := transcriptsync.SyncMissingWithBatchGoverned(ctx, client, store, step.OutDir, step.Limit, step.BatchSize, ingestGovernance)
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
			Skipped:    syncResult.Skipped,
		}
	default:
		return fmt.Errorf("unsupported sync-run action %q", step.Action)
	}
	return nil
}

func loadOptionalGovernanceConfig(path string) (*governance.Config, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	return governance.LoadFile(path)
}

func populateSyncRunServiceResult(target *syncRunStepResult, source syncsvc.Result) {
	target.Scope = source.Scope
	target.SyncKey = source.SyncKey
	target.RunID = source.RunID
	target.Cursor = source.Cursor
	target.Pages = source.Pages
	target.RecordsSeen = source.RecordsSeen
	target.RecordsWritten = source.RecordsWritten
	target.RecordsSkipped = source.RecordsSkipped
}

type syncStatusResponse struct {
	TotalCalls                   int64                              `json:"total_calls"`
	TotalUsers                   int64                              `json:"total_users"`
	TotalTranscripts             int64                              `json:"total_transcripts"`
	TotalTranscriptSegments      int64                              `json:"total_transcript_segments"`
	TotalEmbeddedCRMContextCalls int64                              `json:"total_embedded_crm_context_calls"`
	TotalEmbeddedCRMObjects      int64                              `json:"total_embedded_crm_objects"`
	TotalEmbeddedCRMFields       int64                              `json:"total_embedded_crm_fields"`
	TotalCRMIntegrations         int64                              `json:"total_crm_integrations"`
	TotalCRMSchemaObjects        int64                              `json:"total_crm_schema_objects"`
	TotalCRMSchemaFields         int64                              `json:"total_crm_schema_fields"`
	TotalGongSettings            int64                              `json:"total_gong_settings"`
	TotalScorecards              int64                              `json:"total_scorecards"`
	TotalScorecardActivity       int64                              `json:"total_scorecard_activity"`
	TotalAIHighlights            int64                              `json:"total_ai_highlights"`
	MissingTranscripts           int64                              `json:"missing_transcripts"`
	RunningSyncRuns              int64                              `json:"running_sync_runs"`
	CallFactsAttribution         sqlite.CallFactsAttributionSignals `json:"call_facts_attribution"`
	ProfileReadiness             sqlite.ProfileReadiness            `json:"profile_readiness"`
	PublicReadiness              sqlite.PublicReadiness             `json:"public_readiness"`
	AttributionCoverage          sqlite.AttributionCoverage         `json:"attribution_coverage"`
	PostgresReadModel            *postgres.ReadModelStatus          `json:"postgres_read_model,omitempty"`
	LastRun                      *syncRunJSON                       `json:"last_run,omitempty"`
	LastSuccessfulRun            *syncRunJSON                       `json:"last_successful_run,omitempty"`
	States                       []syncStateJSON                    `json:"states"`
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
	Name              string   `yaml:"name,omitempty" json:"name,omitempty"`
	Action            string   `yaml:"action" json:"action"`
	From              string   `yaml:"from,omitempty" json:"from,omitempty"`
	To                string   `yaml:"to,omitempty" json:"to,omitempty"`
	Preset            string   `yaml:"preset,omitempty" json:"preset,omitempty"`
	MaxPages          int      `yaml:"max_pages,omitempty" json:"max_pages,omitempty"`
	CallFrom          string   `yaml:"call_from,omitempty" json:"call_from,omitempty"`
	CallTo            string   `yaml:"call_to,omitempty" json:"call_to,omitempty"`
	ReviewFrom        string   `yaml:"review_from,omitempty" json:"review_from,omitempty"`
	ReviewTo          string   `yaml:"review_to,omitempty" json:"review_to,omitempty"`
	ReviewMethod      string   `yaml:"review_method,omitempty" json:"review_method,omitempty"`
	OutDir            string   `yaml:"out_dir,omitempty" json:"out_dir,omitempty"`
	Limit             int      `yaml:"limit,omitempty" json:"limit,omitempty"`
	BatchSize         int      `yaml:"batch_size,omitempty" json:"batch_size,omitempty"`
	IncludeParties    bool     `yaml:"include_parties,omitempty" json:"include_parties,omitempty"`
	IncludeHighlights bool     `yaml:"include_highlights,omitempty" json:"include_highlights,omitempty"`
	IntegrationID     string   `yaml:"integration_id,omitempty" json:"integration_id,omitempty"`
	ObjectTypes       []string `yaml:"object_types,omitempty" json:"object_types,omitempty"`
	SettingsKind      string   `yaml:"settings_kind,omitempty" json:"settings_kind,omitempty"`
	WorkspaceID       string   `yaml:"workspace_id,omitempty" json:"workspace_id,omitempty"`
	GovernanceConfig  string   `yaml:"governance_config,omitempty" json:"governance_config,omitempty"`
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
	CallFrom                string                  `json:"call_from,omitempty"`
	CallTo                  string                  `json:"call_to,omitempty"`
	ReviewFrom              string                  `json:"review_from,omitempty"`
	ReviewTo                string                  `json:"review_to,omitempty"`
	ReviewMethod            string                  `json:"review_method,omitempty"`
	SettingsKind            string                  `json:"settings_kind,omitempty"`
	WorkspaceID             string                  `json:"workspace_id,omitempty"`
	IntegrationID           string                  `json:"integration_id,omitempty"`
	ObjectTypes             []string                `json:"object_types,omitempty"`
	OutDir                  string                  `json:"out_dir,omitempty"`
	Limit                   int                     `json:"limit,omitempty"`
	BatchSize               int                     `json:"batch_size,omitempty"`
	IncludeParties          bool                    `json:"include_parties,omitempty"`
	IncludeHighlights       bool                    `json:"include_highlights,omitempty"`
	GovernanceConfig        string                  `json:"governance_config,omitempty"`
	Scope                   string                  `json:"scope,omitempty"`
	SyncKey                 string                  `json:"sync_key,omitempty"`
	RunID                   int64                   `json:"run_id,omitempty"`
	Cursor                  string                  `json:"cursor,omitempty"`
	Pages                   int                     `json:"pages,omitempty"`
	RecordsSeen             int64                   `json:"records_seen,omitempty"`
	RecordsWritten          int64                   `json:"records_written,omitempty"`
	RecordsSkipped          int64                   `json:"records_skipped,omitempty"`
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
	Skipped    int `json:"skipped,omitempty"`
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
