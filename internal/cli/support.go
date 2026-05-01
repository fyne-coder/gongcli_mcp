package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/fyne-coder/gongcli_mcp/internal/mcp"
	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
	"github.com/fyne-coder/gongcli_mcp/internal/version"
)

const supportBundleSchemaVersion = 1

func (a *app) support(ctx context.Context, args []string) error {
	if len(args) == 0 {
		fmt.Fprintln(a.err, "usage: gongctl support [bundle]")
		return errUsage
	}

	switch args[0] {
	case "bundle":
		return a.supportBundle(ctx, args[1:])
	default:
		fmt.Fprintf(a.err, "unknown support command %q\n", args[0])
		return errUsage
	}
}

func (a *app) supportBundle(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("support bundle", flag.ContinueOnError)
	fs.SetOutput(a.err)
	dbPath := fs.String("db", "", "SQLite database path")
	outDir := fs.String("out", "", "output directory for sanitized diagnostic bundle")
	includeEnv := fs.Bool("include-env", false, "include presence booleans for known environment variables")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	if strings.TrimSpace(*dbPath) == "" {
		return fmt.Errorf("--db is required")
	}
	if strings.TrimSpace(*outDir) == "" {
		return fmt.Errorf("--out is required")
	}

	absDBPath, err := filepath.Abs(*dbPath)
	if err != nil {
		return err
	}
	absOutDir, err := filepath.Abs(*outDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(absOutDir, 0o700); err != nil {
		return err
	}

	store, err := sqlite.OpenReadOnly(ctx, absDBPath)
	if err != nil {
		return err
	}
	defer store.Close()

	inventory, err := store.CacheInventory(ctx)
	if err != nil {
		return err
	}

	manifest := newSupportBundleManifest(absDBPath, inventory)
	if err := writeSupportBundleJSON(filepath.Join(absOutDir, "manifest.json"), manifest); err != nil {
		return err
	}
	if err := writeSupportBundleJSON(filepath.Join(absOutDir, "cache-summary.json"), newSupportCacheSummary(inventory)); err != nil {
		return err
	}
	if err := writeSupportBundleJSON(filepath.Join(absOutDir, "mcp-tools.json"), newSupportMCPTools()); err != nil {
		return err
	}
	if err := writeSupportBundleJSON(filepath.Join(absOutDir, "redaction-policy.json"), supportRedactionPolicy()); err != nil {
		return err
	}
	if *includeEnv {
		if err := writeSupportBundleJSON(filepath.Join(absOutDir, "environment.json"), supportEnvironmentPresence()); err != nil {
			return err
		}
	}

	return writeJSONValue(a.out, supportBundleResponse{
		OutputDirectory: supportOutputDirectoryInfo{
			PathPolicy: "local_path_not_exported",
		},
		SchemaVersion:                       supportBundleSchemaVersion,
		Files:                               supportBundleFiles(*includeEnv),
		ContainsRawCustomerData:             false,
		ContainsCustomerOperationalMetadata: true,
		Sensitivity:                         "customer_operational_metadata",
		RedactionPolicy:                     "metadata_only_no_payloads_no_transcripts_no_direct_customer_content_identifiers_no_secrets_no_paths",
		SharePolicy:                         "Share under the customer's support policy; this bundle is not public-safe.",
	})
}

func writeSupportBundleJSON(path string, value any) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func supportBundleFiles(includeEnv bool) []string {
	files := []string{
		"manifest.json",
		"cache-summary.json",
		"mcp-tools.json",
		"redaction-policy.json",
	}
	if includeEnv {
		files = append(files, "environment.json")
	}
	return files
}

func newSupportBundleManifest(dbPath string, inventory *sqlite.CacheInventory) supportBundleManifest {
	info := version.Current()
	dbSize, walSize, shmSize := statSQLiteFiles(dbPath)
	var dbModTime string
	if stat, err := os.Stat(dbPath); err == nil {
		dbModTime = stat.ModTime().UTC().Format(time.RFC3339)
	}
	return supportBundleManifest{
		SchemaVersion: supportBundleSchemaVersion,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		Product: supportProductInfo{
			Name:    "gongctl",
			Version: info.Version,
			Commit:  info.Commit,
			Date:    info.Date,
		},
		Runtime: supportRuntimeInfo{
			GOOS:      runtime.GOOS,
			GOARCH:    runtime.GOARCH,
			GoVersion: runtime.Version(),
		},
		Database: supportDatabaseInfo{
			PathClass:        "customer_sqlite_cache",
			PathPolicy:       "no_path_or_filename_exported",
			SizeBytes:        dbSize,
			WALSizeBytes:     walSize,
			SHMSizeBytes:     shmSize,
			ModifiedAtUTC:    dbModTime,
			OldestCallTime:   inventory.OldestCallStartedAt,
			NewestCallTime:   inventory.NewestCallStartedAt,
			OpenMode:         "read_only",
			CustomerDataNote: "SQLite cache remains customer data; bundle exports metadata only.",
		},
	}
}

func newSupportCacheSummary(inventory *sqlite.CacheInventory) supportCacheSummary {
	summary := inventory.Summary
	out := supportCacheSummary{
		TableCounts:        inventory.TableCounts,
		TranscriptPresence: newCacheTranscriptPresence(summary),
		CRMContextPresence: newCacheCRMContextPresence(summary),
		ProfileStatus: supportProfileStatus{
			Active:      summary.ProfileReadiness.Active,
			Status:      summary.ProfileReadiness.Status,
			CacheFresh:  summary.ProfileReadiness.CacheFresh,
			CacheStatus: summary.ProfileReadiness.CacheStatus,
		},
		PublicReadiness: summary.PublicReadiness,
	}
	out.Summary = cacheInventorySummaryJSON{
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
		AttributionCoverage:          summary.AttributionCoverage,
	}
	if summary.LastRun != nil {
		out.LastRun = &supportSyncRunSummary{
			Scope:          summary.LastRun.Scope,
			Status:         summary.LastRun.Status,
			StartedAt:      summary.LastRun.StartedAt,
			FinishedAt:     summary.LastRun.FinishedAt,
			RecordsSeen:    summary.LastRun.RecordsSeen,
			RecordsWritten: summary.LastRun.RecordsWritten,
		}
	}
	if summary.LastSuccessfulRun != nil {
		out.LastSuccessfulRun = &supportSyncRunSummary{
			Scope:          summary.LastSuccessfulRun.Scope,
			Status:         summary.LastSuccessfulRun.Status,
			StartedAt:      summary.LastSuccessfulRun.StartedAt,
			FinishedAt:     summary.LastSuccessfulRun.FinishedAt,
			RecordsSeen:    summary.LastSuccessfulRun.RecordsSeen,
			RecordsWritten: summary.LastSuccessfulRun.RecordsWritten,
		}
	}
	for _, state := range summary.States {
		out.SyncStates = append(out.SyncStates, supportSyncStateSummary{
			Scope:         state.Scope,
			LastStatus:    state.LastStatus,
			LastSuccessAt: state.LastSuccessAt,
			UpdatedAt:     state.UpdatedAt,
		})
	}
	if out.SyncStates == nil {
		out.SyncStates = []supportSyncStateSummary{}
	}
	return out
}

func newSupportMCPTools() supportMCPTools {
	catalog := mcp.ToolCatalog()
	tools := make([]supportMCPTool, 0, len(catalog))
	for _, tool := range catalog {
		tools = append(tools, supportMCPTool{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: tool.InputSchema,
		})
	}
	return supportMCPTools{
		ToolCount: len(tools),
		Tools:     tools,
	}
}

func supportRedactionPolicy() supportRedactionPolicyJSON {
	return supportRedactionPolicyJSON{
		Mode:                                "metadata_only",
		ContainsCustomerOperationalMetadata: true,
		Sensitivity:                         "customer_operational_metadata",
		ExcludedByDefault: []string{
			"Gong credentials and MCP bearer tokens",
			"raw Gong API payloads",
			"transcript text and transcript raw JSON",
			"CRM field values and raw CRM context payloads",
			"account, opportunity, contact, lead, and user names",
			"call titles, call IDs, object IDs, speaker IDs, and user IDs",
			"local .env contents and full local filesystem paths",
		},
		SupportPolicy: "Share this bundle before sharing logs or payloads. It excludes direct customer content by design but still contains customer operational metadata. Raw customer data requires explicit customer approval and a time-bound support exception.",
	}
}

func supportEnvironmentPresence() supportEnvironmentPresenceJSON {
	keys := []string{
		"GONG_ACCESS_KEY",
		"GONG_ACCESS_KEY_SECRET",
		"GONG_BASE_URL",
		"GONGCTL_RESTRICTED",
		"GONGCTL_ALLOW_SENSITIVE_EXPORT",
		"GONGMCP_HTTP_ADDR",
		"GONGMCP_AUTH_MODE",
		"GONGMCP_TOOL_ALLOWLIST",
		"GONGMCP_ALLOWED_ORIGINS",
		"GONGMCP_AI_GOVERNANCE_CONFIG",
		"GONGMCP_BEARER_TOKEN",
		"GONGMCP_BEARER_TOKEN_FILE",
		"GONGMCP_ALLOW_OPEN_NETWORK",
	}
	values := make(map[string]bool, len(keys))
	for _, key := range keys {
		values[key] = os.Getenv(key) != ""
	}
	return supportEnvironmentPresenceJSON{
		Policy:  "presence_only_values_not_exported",
		Present: values,
	}
}

type supportBundleResponse struct {
	OutputDirectory                     supportOutputDirectoryInfo `json:"output_directory"`
	SchemaVersion                       int                        `json:"schema_version"`
	Files                               []string                   `json:"files"`
	ContainsRawCustomerData             bool                       `json:"contains_raw_customer_data"`
	ContainsCustomerOperationalMetadata bool                       `json:"contains_customer_operational_metadata"`
	Sensitivity                         string                     `json:"sensitivity"`
	RedactionPolicy                     string                     `json:"redaction_policy"`
	SharePolicy                         string                     `json:"share_policy"`
}

type supportOutputDirectoryInfo struct {
	PathPolicy string `json:"path_policy"`
}

type supportBundleManifest struct {
	SchemaVersion int                 `json:"schema_version"`
	GeneratedAt   string              `json:"generated_at"`
	Product       supportProductInfo  `json:"product"`
	Runtime       supportRuntimeInfo  `json:"runtime"`
	Database      supportDatabaseInfo `json:"database"`
}

type supportProductInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
}

type supportRuntimeInfo struct {
	GOOS      string `json:"goos"`
	GOARCH    string `json:"goarch"`
	GoVersion string `json:"go_version"`
}

type supportDatabaseInfo struct {
	PathClass        string `json:"path_class"`
	PathPolicy       string `json:"path_policy"`
	SizeBytes        int64  `json:"size_bytes"`
	WALSizeBytes     int64  `json:"wal_size_bytes,omitempty"`
	SHMSizeBytes     int64  `json:"shm_size_bytes,omitempty"`
	ModifiedAtUTC    string `json:"modified_at_utc,omitempty"`
	OldestCallTime   string `json:"oldest_call_started_at,omitempty"`
	NewestCallTime   string `json:"newest_call_started_at,omitempty"`
	OpenMode         string `json:"open_mode"`
	CustomerDataNote string `json:"customer_data_note"`
}

type supportCacheSummary struct {
	TableCounts        []sqlite.CacheTableCount    `json:"table_counts"`
	Summary            cacheInventorySummaryJSON   `json:"summary"`
	TranscriptPresence cacheTranscriptPresenceJSON `json:"transcript_presence"`
	CRMContextPresence cacheCRMContextPresenceJSON `json:"crm_context_presence"`
	ProfileStatus      supportProfileStatus        `json:"profile_status"`
	PublicReadiness    sqlite.PublicReadiness      `json:"public_readiness"`
	LastRun            *supportSyncRunSummary      `json:"last_run,omitempty"`
	LastSuccessfulRun  *supportSyncRunSummary      `json:"last_successful_run,omitempty"`
	SyncStates         []supportSyncStateSummary   `json:"sync_states"`
}

type supportProfileStatus struct {
	Active      bool   `json:"active"`
	Status      string `json:"status"`
	CacheFresh  bool   `json:"cache_fresh"`
	CacheStatus string `json:"cache_status"`
}

type supportSyncRunSummary struct {
	Scope          string `json:"scope"`
	Status         string `json:"status"`
	StartedAt      string `json:"started_at"`
	FinishedAt     string `json:"finished_at"`
	RecordsSeen    int64  `json:"records_seen"`
	RecordsWritten int64  `json:"records_written"`
}

type supportSyncStateSummary struct {
	Scope         string `json:"scope"`
	LastStatus    string `json:"last_status"`
	LastSuccessAt string `json:"last_success_at,omitempty"`
	UpdatedAt     string `json:"updated_at"`
}

type supportMCPTools struct {
	ToolCount int              `json:"tool_count"`
	Tools     []supportMCPTool `json:"tools"`
}

type supportMCPTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

type supportRedactionPolicyJSON struct {
	Mode                                string   `json:"mode"`
	ContainsCustomerOperationalMetadata bool     `json:"contains_customer_operational_metadata"`
	Sensitivity                         string   `json:"sensitivity"`
	ExcludedByDefault                   []string `json:"excluded_by_default"`
	SupportPolicy                       string   `json:"support_policy"`
}

type supportEnvironmentPresenceJSON struct {
	Policy  string          `json:"policy"`
	Present map[string]bool `json:"present"`
}
