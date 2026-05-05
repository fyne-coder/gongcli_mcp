package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fyne-coder/gongcli_mcp/internal/store/postgres"
	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
	"gopkg.in/yaml.v3"
)

const cacheSensitiveDataWarning = "This cache may contain raw Gong payloads, transcript text, CRM field values, profile mappings, and sync history. Treat the DB and any copied exports as sensitive."
const cacheRetentionPolicyMaxBytes = 64 * 1024

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
	dbPath := fs.String("db", "", "SQLite database path; omit when using GONG_DATABASE_URL or DATABASE_URL for Postgres")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	store, backend, dbPathForResponse, err := openCacheInventoryStore(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	inventory, err := store.CacheInventory(ctx)
	if err != nil {
		return err
	}

	var dbSize, walSize, shmSize int64
	if backend == "sqlite" {
		dbSize, walSize, shmSize = statSQLiteFiles(dbPathForResponse)
	}
	response := cacheInventoryResponse{
		Backend:              backend,
		DBPath:               dbPathForResponse,
		DBPathPolicy:         cacheInventoryPathPolicy(backend),
		DBSizeBytes:          dbSize,
		WALSizeBytes:         walSize,
		SHMSizeBytes:         shmSize,
		TableCounts:          inventory.TableCounts,
		OldestCallStartedAt:  inventory.OldestCallStartedAt,
		NewestCallStartedAt:  inventory.NewestCallStartedAt,
		TranscriptPresence:   newCacheTranscriptPresence(inventory.Summary),
		CRMContextPresence:   newCacheCRMContextPresence(inventory.Summary),
		ProfileStatus:        cacheInventoryProfileStatus(backend, inventory.Summary.ProfileReadiness),
		LastRun:              newSyncRunJSON(inventory.Summary.LastRun),
		LastSuccessfulRun:    newSyncRunJSON(inventory.Summary.LastSuccessfulRun),
		SyncStates:           []syncStateJSON{},
		SensitiveDataWarning: cacheSensitiveDataWarning,
	}
	if diagnosticsStore, ok := store.(cacheDiagnosticsStore); ok {
		diagnostics, err := diagnosticsStore.CacheDiagnostics(ctx)
		if err != nil {
			return err
		}
		response.Diagnostics = diagnostics
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

type cacheInventoryStore interface {
	CacheInventory(context.Context) (*sqlite.CacheInventory, error)
	Close() error
}

type cacheDiagnosticsStore interface {
	CacheDiagnostics(context.Context) (*postgres.CacheDiagnostics, error)
}

func openCacheInventoryStore(ctx context.Context, path string) (cacheInventoryStore, string, string, error) {
	if strings.TrimSpace(path) != "" {
		absPath, err := filepath.Abs(path)
		if err != nil {
			return nil, "", "", err
		}
		store, err := sqlite.OpenReadOnly(ctx, absPath)
		return store, "sqlite", absPath, err
	}
	databaseURL := postgres.URLFromEnv(os.Getenv)
	if databaseURL == "" {
		return nil, "", "", errors.New("--db is required")
	}
	store, err := postgres.OpenStatus(ctx, databaseURL)
	return store, "postgres", "", err
}

func cacheInventoryPathPolicy(backend string) string {
	if backend == "postgres" {
		return "database_url_not_exported"
	}
	return "local_path_reported_for_operator"
}

func cacheInventoryProfileStatus(backend string, readiness sqlite.ProfileReadiness) any {
	if backend == "postgres" {
		return supportProfileStatus{
			Active:      readiness.Active,
			Status:      readiness.Status,
			CacheFresh:  readiness.CacheFresh,
			CacheStatus: readiness.CacheStatus,
		}
	}
	return readiness
}

func (a *app) cachePurge(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("cache purge", flag.ContinueOnError)
	fs.SetOutput(a.err)
	dbPath := fs.String("db", "", "SQLite database path; omit when using GONG_DATABASE_URL or DATABASE_URL for Postgres")
	olderThan := fs.String("older-than", "", "purge calls started before this YYYY-MM-DD date")
	configPath := fs.String("config", "", "retention policy YAML for scheduled purge jobs")
	dryRun := fs.Bool("dry-run", false, "show purge plan without deleting data")
	confirm := fs.Bool("confirm", false, "delete matching cache rows")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}

	cutoff := strings.TrimSpace(*olderThan)
	var policy *cacheRetentionPolicyMetadata
	if strings.TrimSpace(*configPath) != "" {
		if cutoff != "" {
			return fmt.Errorf("--older-than and --config cannot be used together")
		}
		loaded, err := loadCacheRetentionPolicy(*configPath)
		if err != nil {
			return err
		}
		if *confirm {
			if err := loaded.validateForConfirm(); err != nil {
				return err
			}
		}
		policy = loaded
		cutoff = loaded.OlderThan
	}
	if cutoff == "" {
		return fmt.Errorf("--older-than is required")
	}
	if _, err := time.Parse("2006-01-02", cutoff); err != nil {
		return fmt.Errorf("--older-than must use YYYY-MM-DD: %w", err)
	}
	if *dryRun && *confirm {
		return fmt.Errorf("--dry-run and --confirm cannot be used together")
	}

	var plan *sqlite.CachePurgePlan
	var backend string
	var dbPathForResponse string
	executed := false
	if *confirm {
		store, selectedBackend, selectedPath, err := openCachePurgeWritableStore(ctx, *dbPath)
		if err != nil {
			return err
		}
		defer store.Close()
		backend = selectedBackend
		dbPathForResponse = selectedPath
		plan, err = store.PurgeCacheBefore(ctx, cutoff)
		if err != nil {
			return err
		}
		executed = true
	} else {
		store, selectedBackend, selectedPath, err := openCachePurgePlanStore(ctx, *dbPath, policy != nil)
		if err != nil {
			return err
		}
		defer store.Close()
		backend = selectedBackend
		dbPathForResponse = selectedPath
		plan, err = store.PlanCachePurgeBefore(ctx, cutoff)
		if err != nil {
			return err
		}
	}

	return writeJSONValue(a.out, cachePurgeResponse{
		Backend:                  backend,
		DBPath:                   dbPathForResponse,
		DBPathPolicy:             cacheInventoryPathPolicy(backend),
		DryRun:                   !*confirm || *dryRun,
		Executed:                 executed,
		ConfirmationRequired:     !*confirm,
		Plan:                     plan,
		RetentionPolicy:          policy,
		SensitiveDataWarning:     cacheSensitiveDataWarning,
		ConfirmationInstructions: cachePurgeConfirmationInstructions(!*confirm),
	})
}

type cacheRetentionPolicyConfig struct {
	Version   int                          `yaml:"version"`
	OlderThan string                       `yaml:"older_than"`
	Approval  cacheRetentionPolicyApproval `yaml:"approval"`
}

type cacheRetentionPolicyApproval struct {
	Reference         string `yaml:"reference"`
	ApprovedBy        string `yaml:"approved_by"`
	ApprovedAt        string `yaml:"approved_at"`
	DataOwner         string `yaml:"data_owner"`
	BackupReference   string `yaml:"backup_reference"`
	LegalHoldReviewed bool   `yaml:"legal_hold_reviewed"`
}

type cacheRetentionPolicyMetadata struct {
	Configured        bool   `json:"configured"`
	PolicySHA256      string `json:"policy_sha256"`
	OlderThan         string `json:"older_than"`
	ApprovalReference string `json:"approval_reference,omitempty"`
	ApprovedBy        string `json:"approved_by,omitempty"`
	ApprovedAt        string `json:"approved_at,omitempty"`
	DataOwner         string `json:"data_owner,omitempty"`
	BackupReference   string `json:"backup_reference,omitempty"`
	LegalHoldReviewed bool   `json:"legal_hold_reviewed"`
	ApprovalComplete  bool   `json:"approval_complete"`
}

func loadCacheRetentionPolicy(path string) (*cacheRetentionPolicyMetadata, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return nil, fmt.Errorf("retention policy config path is required")
	}
	info, err := os.Stat(trimmed)
	if err != nil {
		return nil, fmt.Errorf("read retention policy config: unavailable")
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("read retention policy config: must be a regular file")
	}
	if info.Size() > cacheRetentionPolicyMaxBytes {
		return nil, fmt.Errorf("retention policy config exceeds %d byte limit", cacheRetentionPolicyMaxBytes)
	}
	raw, err := os.ReadFile(trimmed)
	if err != nil {
		return nil, fmt.Errorf("read retention policy config: failed")
	}
	var cfg cacheRetentionPolicyConfig
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse retention policy config: %w", err)
	}
	if cfg.Version != 1 {
		return nil, fmt.Errorf("retention policy config version must be 1")
	}
	olderThan := strings.TrimSpace(cfg.OlderThan)
	if olderThan == "" {
		return nil, fmt.Errorf("retention policy config older_than is required")
	}
	sum := sha256.Sum256(raw)
	meta := &cacheRetentionPolicyMetadata{
		Configured:        true,
		PolicySHA256:      hex.EncodeToString(sum[:]),
		OlderThan:         olderThan,
		ApprovalReference: cacheRetentionPolicySafeMetadata(strings.TrimSpace(cfg.Approval.Reference)),
		ApprovedBy:        cacheRetentionPolicySafeMetadata(strings.TrimSpace(cfg.Approval.ApprovedBy)),
		ApprovedAt:        strings.TrimSpace(cfg.Approval.ApprovedAt),
		DataOwner:         cacheRetentionPolicySafeMetadata(strings.TrimSpace(cfg.Approval.DataOwner)),
		BackupReference:   cacheRetentionPolicySafeMetadata(strings.TrimSpace(cfg.Approval.BackupReference)),
		LegalHoldReviewed: cfg.Approval.LegalHoldReviewed,
	}
	meta.ApprovalComplete = meta.approvalComplete()
	return meta, nil
}

func (p *cacheRetentionPolicyMetadata) approvalComplete() bool {
	return p.approvalFieldsPresent() && p.validateApprovedAt() == nil
}

func (p *cacheRetentionPolicyMetadata) approvalFieldsPresent() bool {
	if p == nil {
		return false
	}
	return p.ApprovalReference != "" &&
		p.ApprovedBy != "" &&
		p.ApprovedAt != "" &&
		p.DataOwner != "" &&
		p.BackupReference != "" &&
		p.LegalHoldReviewed
}

func (p *cacheRetentionPolicyMetadata) validateForConfirm() error {
	if p == nil {
		return fmt.Errorf("retention policy config is required for config-driven confirmation")
	}
	if !p.approvalFieldsPresent() {
		return fmt.Errorf("retention policy approval is incomplete; confirmed config purge requires approval.reference, approval.approved_by, approval.approved_at, approval.data_owner, approval.backup_reference, and approval.legal_hold_reviewed=true")
	}
	if err := p.validateApprovedAt(); err != nil {
		return err
	}
	return nil
}

func (p *cacheRetentionPolicyMetadata) validateApprovedAt() error {
	approvedAt, err := time.Parse("2006-01-02", p.ApprovedAt)
	if err != nil {
		return fmt.Errorf("retention policy approval.approved_at must use YYYY-MM-DD: %w", err)
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	if approvedAt.After(today) {
		return fmt.Errorf("retention policy approval.approved_at must not be in the future")
	}
	return nil
}

func cacheRetentionPolicySafeMetadata(value string) string {
	if value == "" {
		return ""
	}
	lowered := strings.ToLower(value)
	if strings.Contains(value, "/") ||
		strings.Contains(value, `\`) ||
		strings.Contains(lowered, "://") ||
		strings.HasPrefix(value, "~") ||
		strings.Contains(value, "@") {
		sum := sha256.Sum256([]byte(value))
		return "redacted:" + hex.EncodeToString(sum[:8])
	}
	return value
}

type cachePurgePlanStore interface {
	PlanCachePurgeBefore(context.Context, string) (*sqlite.CachePurgePlan, error)
	Close() error
}

type cachePurgeWritableStore interface {
	cachePurgePlanStore
	PurgeCacheBefore(context.Context, string) (*sqlite.CachePurgePlan, error)
}

func openCachePurgePlanStore(ctx context.Context, path string, requirePostgresReader bool) (cachePurgePlanStore, string, string, error) {
	if strings.TrimSpace(path) != "" {
		absPath, err := filepath.Abs(path)
		if err != nil {
			return nil, "", "", err
		}
		store, err := sqlite.OpenReadOnly(ctx, absPath)
		return store, "sqlite", absPath, err
	}
	databaseURL := postgres.URLFromEnv(os.Getenv)
	if databaseURL == "" {
		return nil, "", "", errors.New("--db is required")
	}
	var store *postgres.Store
	var err error
	if requirePostgresReader {
		store, err = postgres.OpenReadOnly(ctx, databaseURL)
	} else {
		store, err = postgres.OpenStatus(ctx, databaseURL)
	}
	return store, "postgres", "", err
}

func openCachePurgeWritableStore(ctx context.Context, path string) (cachePurgeWritableStore, string, string, error) {
	if strings.TrimSpace(path) != "" {
		absPath, err := filepath.Abs(path)
		if err != nil {
			return nil, "", "", err
		}
		store, err := openSQLiteStore(ctx, absPath)
		return store, "sqlite", absPath, err
	}
	databaseURL := postgres.URLFromEnv(os.Getenv)
	if databaseURL == "" {
		return nil, "", "", errors.New("--db is required")
	}
	store, err := postgres.Open(ctx, databaseURL)
	if err != nil {
		return nil, "", "", fmt.Errorf("open writable Postgres cache for confirmed purge: %w", err)
	}
	return store, "postgres", "", nil
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
	Backend              string                      `json:"backend"`
	DBPath               string                      `json:"db_path"`
	DBPathPolicy         string                      `json:"db_path_policy"`
	DBSizeBytes          int64                       `json:"db_size_bytes"`
	WALSizeBytes         int64                       `json:"wal_size_bytes,omitempty"`
	SHMSizeBytes         int64                       `json:"shm_size_bytes,omitempty"`
	TableCounts          []sqlite.CacheTableCount    `json:"table_counts"`
	Summary              cacheInventorySummaryJSON   `json:"summary"`
	OldestCallStartedAt  string                      `json:"oldest_call_started_at,omitempty"`
	NewestCallStartedAt  string                      `json:"newest_call_started_at,omitempty"`
	TranscriptPresence   cacheTranscriptPresenceJSON `json:"transcript_presence"`
	CRMContextPresence   cacheCRMContextPresenceJSON `json:"crm_context_presence"`
	ProfileStatus        any                         `json:"profile_status"`
	LastRun              *syncRunJSON                `json:"last_run,omitempty"`
	LastSuccessfulRun    *syncRunJSON                `json:"last_successful_run,omitempty"`
	SyncStates           []syncStateJSON             `json:"sync_states"`
	Diagnostics          any                         `json:"diagnostics,omitempty"`
	SensitiveDataWarning string                      `json:"sensitive_data_warning"`
}

type cachePurgeResponse struct {
	Backend                  string                        `json:"backend"`
	DBPath                   string                        `json:"db_path"`
	DBPathPolicy             string                        `json:"db_path_policy"`
	DryRun                   bool                          `json:"dry_run"`
	Executed                 bool                          `json:"executed"`
	ConfirmationRequired     bool                          `json:"confirmation_required"`
	Plan                     *sqlite.CachePurgePlan        `json:"plan"`
	RetentionPolicy          *cacheRetentionPolicyMetadata `json:"retention_policy,omitempty"`
	SensitiveDataWarning     string                        `json:"sensitive_data_warning"`
	ConfirmationInstructions string                        `json:"confirmation_instructions,omitempty"`
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
