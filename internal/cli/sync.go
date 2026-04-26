package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/arthurlee/gongctl/internal/store/sqlite"
	"github.com/arthurlee/gongctl/internal/syncsvc"
	transcriptsync "github.com/arthurlee/gongctl/internal/transcripts"
)

func (a *app) sync(ctx context.Context, args []string) error {
	if len(args) == 0 {
		fmt.Fprintln(a.err, "usage: gongctl sync [calls|users|transcripts|crm-integrations|crm-schema|settings|status]")
		return errUsage
	}

	switch args[0] {
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

func (a *app) syncCalls(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sync calls", flag.ContinueOnError)
	fs.SetOutput(a.err)
	dbPath := fs.String("db", "", "SQLite database path")
	from := fs.String("from", "", "from date, YYYY-MM-DD or RFC3339")
	to := fs.String("to", "", "to date, YYYY-MM-DD or RFC3339")
	preset := fs.String("preset", "", "sync preset: business, minimal, all")
	maxPages := fs.Int("max-pages", 0, "maximum pages to sync; 0 means all pages")
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
		From:     *from,
		To:       *to,
		Context:  requestContext,
		Preset:   presetName,
		MaxPages: *maxPages,
	})
	fmt.Fprintf(
		a.err,
		"synced calls: pages=%d seen=%d written=%d preset=%s context=%s cursor=%s db=%s\n",
		result.Pages,
		result.RecordsSeen,
		result.RecordsWritten,
		presetName,
		displayContext(requestContext),
		displayCursor(result.Cursor),
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
	batchSize := fs.Int("batch-size", 50, "maximum call IDs per Gong transcript request")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	if strings.TrimSpace(*outDir) == "" {
		return fmt.Errorf("--out-dir is required")
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

	store, err := openSQLiteStore(ctx, *dbPath)
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

type syncStatusResponse struct {
	TotalCalls                   int64                   `json:"total_calls"`
	TotalUsers                   int64                   `json:"total_users"`
	TotalTranscripts             int64                   `json:"total_transcripts"`
	TotalTranscriptSegments      int64                   `json:"total_transcript_segments"`
	TotalEmbeddedCRMContextCalls int64                   `json:"total_embedded_crm_context_calls"`
	TotalEmbeddedCRMObjects      int64                   `json:"total_embedded_crm_objects"`
	TotalEmbeddedCRMFields       int64                   `json:"total_embedded_crm_fields"`
	TotalCRMIntegrations         int64                   `json:"total_crm_integrations"`
	TotalCRMSchemaObjects        int64                   `json:"total_crm_schema_objects"`
	TotalCRMSchemaFields         int64                   `json:"total_crm_schema_fields"`
	TotalGongSettings            int64                   `json:"total_gong_settings"`
	TotalScorecards              int64                   `json:"total_scorecards"`
	MissingTranscripts           int64                   `json:"missing_transcripts"`
	RunningSyncRuns              int64                   `json:"running_sync_runs"`
	ProfileReadiness             sqlite.ProfileReadiness `json:"profile_readiness"`
	PublicReadiness              sqlite.PublicReadiness  `json:"public_readiness"`
	LastRun                      *syncRunJSON            `json:"last_run,omitempty"`
	LastSuccessfulRun            *syncRunJSON            `json:"last_successful_run,omitempty"`
	States                       []syncStateJSON         `json:"states"`
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
