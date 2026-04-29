package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
)

const cacheSensitiveDataWarning = "This cache may contain raw Gong payloads, transcript text, CRM field values, profile mappings, and sync history. Treat the DB and any copied exports as sensitive."

func (a *app) cache(ctx context.Context, args []string) error {
	if len(args) == 0 {
		fmt.Fprintln(a.err, "usage: gongctl cache [inventory|purge]")
		return errUsage
	}

	switch args[0] {
	case "inventory":
		return a.cacheInventory(ctx, args[1:])
	case "purge":
		return a.cachePurge(ctx, args[1:])
	default:
		fmt.Fprintf(a.err, "unknown cache command %q\n", args[0])
		return errUsage
	}
}

func (a *app) cacheInventory(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("cache inventory", flag.ContinueOnError)
	fs.SetOutput(a.err)
	dbPath := fs.String("db", "", "SQLite database path")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	if strings.TrimSpace(*dbPath) == "" {
		return fmt.Errorf("--db is required")
	}

	absPath, err := filepath.Abs(*dbPath)
	if err != nil {
		return err
	}

	store, err := sqlite.OpenReadOnly(ctx, absPath)
	if err != nil {
		return err
	}
	defer store.Close()

	inventory, err := store.CacheInventory(ctx)
	if err != nil {
		return err
	}

	dbSize, walSize, shmSize := statSQLiteFiles(absPath)
	response := cacheInventoryResponse{
		DBPath:               absPath,
		DBSizeBytes:          dbSize,
		WALSizeBytes:         walSize,
		SHMSizeBytes:         shmSize,
		TableCounts:          inventory.TableCounts,
		OldestCallStartedAt:  inventory.OldestCallStartedAt,
		NewestCallStartedAt:  inventory.NewestCallStartedAt,
		TranscriptPresence:   newCacheTranscriptPresence(inventory.Summary),
		CRMContextPresence:   newCacheCRMContextPresence(inventory.Summary),
		ProfileStatus:        inventory.Summary.ProfileReadiness,
		LastRun:              newSyncRunJSON(inventory.Summary.LastRun),
		LastSuccessfulRun:    newSyncRunJSON(inventory.Summary.LastSuccessfulRun),
		SyncStates:           []syncStateJSON{},
		SensitiveDataWarning: cacheSensitiveDataWarning,
	}
	response.Summary = cacheInventorySummaryJSON{
		TotalCalls:                   inventory.Summary.TotalCalls,
		TotalUsers:                   inventory.Summary.TotalUsers,
		TotalTranscripts:             inventory.Summary.TotalTranscripts,
		TotalTranscriptSegments:      inventory.Summary.TotalTranscriptSegments,
		TotalEmbeddedCRMContextCalls: inventory.Summary.TotalEmbeddedCRMContextCalls,
		TotalEmbeddedCRMObjects:      inventory.Summary.TotalEmbeddedCRMObjects,
		TotalEmbeddedCRMFields:       inventory.Summary.TotalEmbeddedCRMFields,
		TotalCRMIntegrations:         inventory.Summary.TotalCRMIntegrations,
		TotalCRMSchemaObjects:        inventory.Summary.TotalCRMSchemaObjects,
		TotalCRMSchemaFields:         inventory.Summary.TotalCRMSchemaFields,
		TotalGongSettings:            inventory.Summary.TotalGongSettings,
		TotalScorecards:              inventory.Summary.TotalScorecards,
		MissingTranscripts:           inventory.Summary.MissingTranscripts,
		RunningSyncRuns:              inventory.Summary.RunningSyncRuns,
		AttributionCoverage:          inventory.Summary.AttributionCoverage,
	}
	for _, state := range inventory.Summary.States {
		response.SyncStates = append(response.SyncStates, syncStateJSON{
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

func (a *app) cachePurge(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("cache purge", flag.ContinueOnError)
	fs.SetOutput(a.err)
	dbPath := fs.String("db", "", "SQLite database path")
	olderThan := fs.String("older-than", "", "purge calls started before this YYYY-MM-DD date")
	dryRun := fs.Bool("dry-run", false, "show purge plan without deleting data")
	confirm := fs.Bool("confirm", false, "delete matching cache rows")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	if strings.TrimSpace(*dbPath) == "" {
		return fmt.Errorf("--db is required")
	}
	cutoff := strings.TrimSpace(*olderThan)
	if cutoff == "" {
		return fmt.Errorf("--older-than is required")
	}
	if _, err := time.Parse("2006-01-02", cutoff); err != nil {
		return fmt.Errorf("--older-than must use YYYY-MM-DD: %w", err)
	}
	if *dryRun && *confirm {
		return fmt.Errorf("--dry-run and --confirm cannot be used together")
	}

	absPath, err := filepath.Abs(*dbPath)
	if err != nil {
		return err
	}

	var plan *sqlite.CachePurgePlan
	executed := false
	if *confirm {
		store, err := openSQLiteStore(ctx, absPath)
		if err != nil {
			return err
		}
		defer store.Close()
		plan, err = store.PurgeCacheBefore(ctx, cutoff)
		if err != nil {
			return err
		}
		executed = true
	} else {
		store, err := sqlite.OpenReadOnly(ctx, absPath)
		if err != nil {
			return err
		}
		defer store.Close()
		plan, err = store.PlanCachePurgeBefore(ctx, cutoff)
		if err != nil {
			return err
		}
	}

	return writeJSONValue(a.out, cachePurgeResponse{
		DBPath:                   absPath,
		DryRun:                   !*confirm || *dryRun,
		Executed:                 executed,
		ConfirmationRequired:     !*confirm,
		Plan:                     plan,
		SensitiveDataWarning:     cacheSensitiveDataWarning,
		ConfirmationInstructions: cachePurgeConfirmationInstructions(!*confirm),
	})
}

func cachePurgeConfirmationInstructions(required bool) string {
	if !required {
		return ""
	}
	return "Re-run with --confirm to delete matching cache rows. Keep --dry-run or omit --confirm for planning only."
}

func statSQLiteFiles(dbPath string) (dbSize int64, walSize int64, shmSize int64) {
	dbSize = statFileSize(dbPath)
	walSize = statFileSize(dbPath + "-wal")
	shmSize = statFileSize(dbPath + "-shm")
	return dbSize, walSize, shmSize
}

func statFileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

func newCacheTranscriptPresence(summary *sqlite.SyncStatusSummary) cacheTranscriptPresenceJSON {
	if summary == nil {
		return cacheTranscriptPresenceJSON{}
	}
	return cacheTranscriptPresenceJSON{
		HasTranscripts:     summary.TotalTranscripts > 0,
		TotalCalls:         summary.TotalCalls,
		CachedTranscripts:  summary.TotalTranscripts,
		MissingTranscripts: summary.MissingTranscripts,
		TranscriptSegments: summary.TotalTranscriptSegments,
	}
}

func newCacheCRMContextPresence(summary *sqlite.SyncStatusSummary) cacheCRMContextPresenceJSON {
	if summary == nil {
		return cacheCRMContextPresenceJSON{}
	}
	return cacheCRMContextPresenceJSON{
		HasEmbeddedContext: summary.TotalEmbeddedCRMFields > 0,
		CallsWithContext:   summary.TotalEmbeddedCRMContextCalls,
		Objects:            summary.TotalEmbeddedCRMObjects,
		Fields:             summary.TotalEmbeddedCRMFields,
	}
}

type cacheInventoryResponse struct {
	DBPath               string                      `json:"db_path"`
	DBSizeBytes          int64                       `json:"db_size_bytes"`
	WALSizeBytes         int64                       `json:"wal_size_bytes,omitempty"`
	SHMSizeBytes         int64                       `json:"shm_size_bytes,omitempty"`
	TableCounts          []sqlite.CacheTableCount    `json:"table_counts"`
	Summary              cacheInventorySummaryJSON   `json:"summary"`
	OldestCallStartedAt  string                      `json:"oldest_call_started_at,omitempty"`
	NewestCallStartedAt  string                      `json:"newest_call_started_at,omitempty"`
	TranscriptPresence   cacheTranscriptPresenceJSON `json:"transcript_presence"`
	CRMContextPresence   cacheCRMContextPresenceJSON `json:"crm_context_presence"`
	ProfileStatus        sqlite.ProfileReadiness     `json:"profile_status"`
	LastRun              *syncRunJSON                `json:"last_run,omitempty"`
	LastSuccessfulRun    *syncRunJSON                `json:"last_successful_run,omitempty"`
	SyncStates           []syncStateJSON             `json:"sync_states"`
	SensitiveDataWarning string                      `json:"sensitive_data_warning"`
}

type cachePurgeResponse struct {
	DBPath                   string                 `json:"db_path"`
	DryRun                   bool                   `json:"dry_run"`
	Executed                 bool                   `json:"executed"`
	ConfirmationRequired     bool                   `json:"confirmation_required"`
	Plan                     *sqlite.CachePurgePlan `json:"plan"`
	SensitiveDataWarning     string                 `json:"sensitive_data_warning"`
	ConfirmationInstructions string                 `json:"confirmation_instructions,omitempty"`
}

type cacheInventorySummaryJSON struct {
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
	AttributionCoverage          sqlite.AttributionCoverage `json:"attribution_coverage"`
}

type cacheTranscriptPresenceJSON struct {
	HasTranscripts     bool  `json:"has_transcripts"`
	TotalCalls         int64 `json:"total_calls"`
	CachedTranscripts  int64 `json:"cached_transcripts"`
	MissingTranscripts int64 `json:"missing_transcripts"`
	TranscriptSegments int64 `json:"transcript_segments"`
}

type cacheCRMContextPresenceJSON struct {
	HasEmbeddedContext bool  `json:"has_embedded_context"`
	CallsWithContext   int64 `json:"calls_with_context"`
	Objects            int64 `json:"objects"`
	Fields             int64 `json:"fields"`
}
