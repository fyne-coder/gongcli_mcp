package mcp

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
)

const protocolVersion = "2025-11-25"
const maxFrameBytes = 1 << 20
const maxToolResultBytes = maxFrameBytes - 4096

const (
	maxSearchResults        = 100
	maxCRMFields            = 200
	maxLateStageSignals     = 100
	maxMissingTranscripts   = 500
	maxOpportunitySummaries = 100
	maxCRMMatrixCells       = 200
	maxCallDetailObjects    = 20
	maxCallDetailFieldNames = 50
	maxLifecycleResults     = 100
	maxLifecycleCRMFields   = 200
	maxCallFactGroups       = 200
	maxInventoryResults     = 200
)

type frameMode int

const (
	frameModeHeader frameMode = iota
	frameModeLine
)

type Store interface {
	SyncStatusSummary(ctx context.Context) (*sqlite.SyncStatusSummary, error)
	SearchCallsRaw(ctx context.Context, params sqlite.CallSearchParams) ([]json.RawMessage, error)
	GetCallDetail(ctx context.Context, callID string) (*sqlite.CallDetail, error)
	ListCRMObjectTypes(ctx context.Context) ([]sqlite.CRMObjectTypeSummary, error)
	ListCRMFields(ctx context.Context, objectType string, limit int) ([]sqlite.CRMFieldSummary, error)
	SearchCRMFieldValues(ctx context.Context, params sqlite.CRMFieldValueSearchParams) ([]sqlite.CRMFieldValueMatch, error)
	ListCRMIntegrations(ctx context.Context) ([]sqlite.CRMIntegrationRecord, error)
	ListCRMSchemaObjects(ctx context.Context, integrationID string) ([]sqlite.CRMSchemaObjectRecord, error)
	ListCRMSchemaFields(ctx context.Context, params sqlite.CRMSchemaFieldListParams) ([]sqlite.CRMSchemaFieldRecord, error)
	ListGongSettings(ctx context.Context, params sqlite.GongSettingListParams) ([]sqlite.GongSettingRecord, error)
	ListScorecards(ctx context.Context, params sqlite.ScorecardListParams) ([]sqlite.ScorecardSummary, error)
	GetScorecardDetail(ctx context.Context, scorecardID string) (*sqlite.ScorecardDetail, error)
	ActiveBusinessProfile(ctx context.Context) (*sqlite.BusinessProfile, error)
	ListBusinessConcepts(ctx context.Context) ([]sqlite.BusinessConcept, error)
	ListUnmappedCRMFields(ctx context.Context, params sqlite.UnmappedCRMFieldParams) ([]sqlite.UnmappedCRMField, error)
	AnalyzeLateStageSignals(ctx context.Context, params sqlite.LateStageSignalParams) (*sqlite.LateStageSignalsReport, error)
	ListOpportunitiesMissingTranscripts(ctx context.Context, params sqlite.OpportunityMissingTranscriptParams) ([]sqlite.OpportunityMissingTranscriptSummary, error)
	SearchTranscriptSegmentsByCRMContext(ctx context.Context, params sqlite.TranscriptCRMSearchParams) ([]sqlite.TranscriptCRMSearchResult, error)
	SummarizeOpportunityCalls(ctx context.Context, params sqlite.OpportunityCallSummaryParams) ([]sqlite.OpportunityCallSummary, error)
	CRMFieldPopulationMatrix(ctx context.Context, params sqlite.CRMFieldPopulationMatrixParams) (*sqlite.CRMFieldPopulationMatrix, error)
	ListLifecycleBucketDefinitions(ctx context.Context) ([]sqlite.LifecycleBucketDefinition, error)
	ListLifecycleBucketDefinitionsWithSource(ctx context.Context, requested string) ([]sqlite.LifecycleBucketDefinition, *sqlite.ProfileQueryInfo, error)
	SummarizeCallsByLifecycle(ctx context.Context, params sqlite.LifecycleSummaryParams) ([]sqlite.LifecycleBucketSummary, error)
	SummarizeCallsByLifecycleWithSource(ctx context.Context, params sqlite.LifecycleSummaryParams) ([]sqlite.LifecycleBucketSummary, *sqlite.ProfileQueryInfo, error)
	SearchCallsByLifecycle(ctx context.Context, params sqlite.LifecycleCallSearchParams) ([]sqlite.LifecycleCallSearchResult, error)
	SearchCallsByLifecycleWithSource(ctx context.Context, params sqlite.LifecycleCallSearchParams) ([]sqlite.LifecycleCallSearchResult, *sqlite.ProfileQueryInfo, error)
	PrioritizeTranscriptsByLifecycle(ctx context.Context, params sqlite.LifecycleTranscriptPriorityParams) ([]sqlite.LifecycleTranscriptPriority, error)
	PrioritizeTranscriptsByLifecycleWithSource(ctx context.Context, params sqlite.LifecycleTranscriptPriorityParams) ([]sqlite.LifecycleTranscriptPriority, *sqlite.ProfileQueryInfo, error)
	CompareLifecycleCRMFields(ctx context.Context, params sqlite.LifecycleCRMFieldComparisonParams) (*sqlite.LifecycleCRMFieldComparison, error)
	SummarizeCallFacts(ctx context.Context, params sqlite.CallFactsSummaryParams) ([]sqlite.CallFactsSummaryRow, error)
	SummarizeCallFactsWithSource(ctx context.Context, params sqlite.CallFactsSummaryParams) ([]sqlite.CallFactsSummaryRow, *sqlite.ProfileQueryInfo, error)
	CallFactsCoverage(ctx context.Context) (*sqlite.CallFactsCoverage, error)
	SearchTranscriptSegments(ctx context.Context, query string, limit int) ([]sqlite.TranscriptSearchResult, error)
	SearchTranscriptSegmentsByCallFacts(ctx context.Context, params sqlite.TranscriptCallFactsSearchParams) ([]sqlite.TranscriptCallFactsSearchResult, error)
	SearchTranscriptQuotesWithAttribution(ctx context.Context, params sqlite.TranscriptAttributionSearchParams) ([]sqlite.TranscriptAttributionSearchResult, error)
	FindCallsMissingTranscripts(ctx context.Context, limit int) ([]sqlite.MissingTranscriptCall, error)
}

type Server struct {
	store            Store
	name             string
	version          string
	tools            []tool
	allowedToolNames map[string]struct{}
}

type ServerOption func(*Server)

type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id"`
	Result  any            `json:"result,omitempty"`
	Error   *responseError `json:"error,omitempty"`
}

type responseError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type initializeResult struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    capabilities   `json:"capabilities"`
	ServerInfo      serverIdentity `json:"serverInfo"`
}

type capabilities struct {
	Tools toolsCapability `json:"tools"`
}

type toolsCapability struct {
	ListChanged bool `json:"listChanged"`
}

type serverIdentity struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type toolsListResult struct {
	Tools []tool `json:"tools"`
}

type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type ToolInfo struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

type publicSyncStatus struct {
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
}

type toolsCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

type toolCallResult struct {
	Content []toolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

type toolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type searchCallsArgs struct {
	CRMObjectType string `json:"crm_object_type"`
	CRMObjectID   string `json:"crm_object_id"`
	Limit         int    `json:"limit"`
}

type getCallArgs struct {
	CallID string `json:"call_id"`
}

type listCRMFieldsArgs struct {
	ObjectType string `json:"object_type"`
	Limit      int    `json:"limit"`
}

type listCRMSchemaObjectsArgs struct {
	IntegrationID string `json:"integration_id"`
}

type listCRMSchemaFieldsArgs struct {
	IntegrationID string `json:"integration_id"`
	ObjectType    string `json:"object_type"`
	Limit         int    `json:"limit"`
}

type listGongSettingsArgs struct {
	Kind  string `json:"kind"`
	Limit int    `json:"limit"`
}

type listScorecardsArgs struct {
	ActiveOnly bool `json:"active_only"`
	Limit      int  `json:"limit"`
}

type getScorecardArgs struct {
	ScorecardID string `json:"scorecard_id"`
}

type listUnmappedCRMFieldsArgs struct {
	Limit int `json:"limit"`
}

type searchCRMFieldValuesArgs struct {
	ObjectType           string `json:"object_type"`
	FieldName            string `json:"field_name"`
	ValueQuery           string `json:"value_query"`
	Limit                int    `json:"limit"`
	IncludeValueSnippets bool   `json:"include_value_snippets"`
	IncludeCallIDs       bool   `json:"include_call_ids"`
}

type analyzeLateStageCRMSignalsArgs struct {
	ObjectType          string   `json:"object_type"`
	StageField          string   `json:"stage_field"`
	LateStageValues     []string `json:"late_stage_values"`
	IncludeStageProxies bool     `json:"include_stage_proxies"`
	Limit               int      `json:"limit"`
}

type opportunitiesMissingTranscriptsArgs struct {
	StageValues []string `json:"stage_values"`
	Limit       int      `json:"limit"`
}

type searchTranscriptsByCRMContextArgs struct {
	Query      string `json:"query"`
	ObjectType string `json:"object_type"`
	ObjectID   string `json:"object_id"`
	Limit      int    `json:"limit"`
}

type opportunityCallSummaryArgs struct {
	StageValues []string `json:"stage_values"`
	Limit       int      `json:"limit"`
}

type crmFieldPopulationMatrixArgs struct {
	ObjectType   string `json:"object_type"`
	GroupByField string `json:"group_by_field"`
	Limit        int    `json:"limit"`
}

type summarizeCallsByLifecycleArgs struct {
	Bucket          string `json:"bucket"`
	LifecycleSource string `json:"lifecycle_source"`
}

type listLifecycleBucketsArgs struct {
	LifecycleSource string `json:"lifecycle_source"`
}

type searchCallsByLifecycleArgs struct {
	Bucket                 string `json:"bucket"`
	MissingTranscriptsOnly bool   `json:"missing_transcripts_only"`
	Limit                  int    `json:"limit"`
	LifecycleSource        string `json:"lifecycle_source"`
}

type prioritizeTranscriptsByLifecycleArgs struct {
	Bucket          string `json:"bucket"`
	Limit           int    `json:"limit"`
	LifecycleSource string `json:"lifecycle_source"`
}

type compareLifecycleCRMFieldsArgs struct {
	BucketA    string `json:"bucket_a"`
	BucketB    string `json:"bucket_b"`
	ObjectType string `json:"object_type"`
	Limit      int    `json:"limit"`
}

type summarizeCallFactsArgs struct {
	GroupBy          string `json:"group_by"`
	LifecycleBucket  string `json:"lifecycle_bucket"`
	LifecycleSource  string `json:"lifecycle_source"`
	Scope            string `json:"scope"`
	System           string `json:"system"`
	Direction        string `json:"direction"`
	TranscriptStatus string `json:"transcript_status"`
	Limit            int    `json:"limit"`
}

type searchTranscriptSegmentsArgs struct {
	Query             string `json:"query"`
	Limit             int    `json:"limit"`
	IncludeCallIDs    bool   `json:"include_call_ids"`
	IncludeSpeakerIDs bool   `json:"include_speaker_ids"`
}

type searchTranscriptsByCallFactsArgs struct {
	Query           string `json:"query"`
	FromDate        string `json:"from_date"`
	ToDate          string `json:"to_date"`
	LifecycleBucket string `json:"lifecycle_bucket"`
	Scope           string `json:"scope"`
	System          string `json:"system"`
	Direction       string `json:"direction"`
	Limit           int    `json:"limit"`
}

type searchTranscriptQuotesWithAttributionArgs struct {
	Query                   string `json:"query"`
	FromDate                string `json:"from_date"`
	ToDate                  string `json:"to_date"`
	LifecycleBucket         string `json:"lifecycle_bucket"`
	Industry                string `json:"industry"`
	AccountQuery            string `json:"account_query"`
	OpportunityStage        string `json:"opportunity_stage"`
	Limit                   int    `json:"limit"`
	IncludeCallIDs          bool   `json:"include_call_ids"`
	IncludeCallTitles       bool   `json:"include_call_titles"`
	IncludeAccountNames     bool   `json:"include_account_names"`
	IncludeOpportunityNames bool   `json:"include_opportunity_names"`
}

type missingTranscriptsArgs struct {
	Limit int `json:"limit"`
}

type searchCallSummary struct {
	CallID          string `json:"call_id"`
	Title           string `json:"title"`
	StartedAt       string `json:"started_at"`
	DurationSeconds int64  `json:"duration_seconds"`
	PartiesCount    int    `json:"parties_count"`
}

type callDetail struct {
	CallID              string                `json:"call_id"`
	Title               string                `json:"title"`
	StartedAt           string                `json:"started_at"`
	DurationSeconds     int64                 `json:"duration_seconds"`
	PartiesCount        int                   `json:"parties_count"`
	CRMObjects          []callDetailCRMObject `json:"crm_objects,omitempty"`
	CRMObjectsTruncated bool                  `json:"crm_objects_truncated,omitempty"`
}

type callDetailCRMObject struct {
	ObjectType          string   `json:"object_type"`
	ObjectID            string   `json:"object_id"`
	ObjectName          string   `json:"object_name,omitempty"`
	FieldCount          int      `json:"field_count"`
	PopulatedFieldCount int      `json:"populated_field_count"`
	FieldNames          []string `json:"field_names,omitempty"`
	FieldNamesTruncated bool     `json:"field_names_truncated,omitempty"`
}

type transcriptSnippet struct {
	CallID       string `json:"call_id"`
	SpeakerID    string `json:"speaker_id"`
	SegmentIndex int    `json:"segment_index"`
	StartMS      int64  `json:"start_ms"`
	EndMS        int64  `json:"end_ms"`
	Snippet      string `json:"snippet"`
}

func NewServer(store Store, name, version string) *Server {
	serverName := strings.TrimSpace(name)
	if serverName == "" {
		serverName = "gongmcp"
	}
	serverVersion := strings.TrimSpace(version)
	if serverVersion == "" {
		serverVersion = "dev"
	}

	return NewServerWithOptions(store, serverName, serverVersion)
}

func NewServerWithOptions(store Store, name, version string, opts ...ServerOption) *Server {
	server := &Server{
		store:   store,
		name:    name,
		version: version,
		tools:   defaultTools(),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(server)
		}
	}
	return server
}

func WithToolAllowlist(names []string) ServerOption {
	allowset := make(map[string]struct{}, len(names))
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		allowset[name] = struct{}{}
	}
	if len(allowset) == 0 {
		return nil
	}
	return func(s *Server) {
		filtered := make([]tool, 0, len(s.tools))
		for _, item := range s.tools {
			if _, ok := allowset[item.Name]; ok {
				filtered = append(filtered, item)
			}
		}
		s.tools = filtered
		s.allowedToolNames = allowset
	}
}

func defaultTools() []tool {
	return []tool{
		{
			Name:        "get_sync_status",
			Description: "Return cached sync run metadata and local SQLite record counts.",
			InputSchema: emptyObjectSchema(),
		},
		{
			Name:        "search_calls",
			Description: "Search cached calls by stored CRM context filters and return summarized call metadata.",
			InputSchema: objectSchema(
				map[string]any{
					"crm_object_type": map[string]any{"type": "string"},
					"crm_object_id":   map[string]any{"type": "string"},
					"limit":           map[string]any{"type": "integer", "minimum": 1, "maximum": maxSearchResults},
				},
				nil,
			),
		},
		{
			Name:        "get_call",
			Description: "Return minimized cached call detail for one call_id without raw participant, CRM field value, or transcript payloads.",
			InputSchema: objectSchema(
				map[string]any{
					"call_id": map[string]any{"type": "string"},
				},
				[]string{"call_id"},
			),
		},
		{
			Name:        "list_crm_object_types",
			Description: "List CRM object types cached from Gong context with object, call, and field counts.",
			InputSchema: emptyObjectSchema(),
		},
		{
			Name:        "list_crm_fields",
			Description: "List fields for one cached CRM object type with population counts and no raw example values.",
			InputSchema: objectSchema(
				map[string]any{
					"object_type": map[string]any{"type": "string"},
					"limit":       map[string]any{"type": "integer", "minimum": 1, "maximum": maxCRMFields},
				},
				[]string{"object_type"},
			),
		},
		{
			Name:        "list_crm_integrations",
			Description: "List cached Gong CRM integrations discovered from the CRM integration API.",
			InputSchema: emptyObjectSchema(),
		},
		{
			Name:        "list_cached_crm_schema_objects",
			Description: "List cached CRM schema object types by Gong CRM integration.",
			InputSchema: objectSchema(
				map[string]any{
					"integration_id": map[string]any{"type": "string"},
				},
				nil,
			),
		},
		{
			Name:        "list_cached_crm_schema_fields",
			Description: "List cached CRM schema fields by optional integration ID and object type without returning field values.",
			InputSchema: objectSchema(
				map[string]any{
					"integration_id": map[string]any{"type": "string"},
					"object_type":    map[string]any{"type": "string"},
					"limit":          map[string]any{"type": "integer", "minimum": 1, "maximum": maxInventoryResults},
				},
				nil,
			),
		},
		{
			Name:        "list_gong_settings",
			Description: "List cached Gong settings inventory items such as trackers, scorecards, and workspaces without raw payloads.",
			InputSchema: objectSchema(
				map[string]any{
					"kind":  map[string]any{"type": "string"},
					"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": maxInventoryResults},
				},
				nil,
			),
		},
		{
			Name:        "list_scorecards",
			Description: "List cached Gong scorecards with names, active state, review method, workspace, and question counts.",
			InputSchema: objectSchema(
				map[string]any{
					"active_only": map[string]any{"type": "boolean"},
					"limit":       map[string]any{"type": "integer", "minimum": 1, "maximum": maxInventoryResults},
				},
				nil,
			),
		},
		{
			Name:        "get_scorecard",
			Description: "Return one cached Gong scorecard with question text and safe scoring metadata, without raw settings payloads.",
			InputSchema: objectSchema(
				map[string]any{
					"scorecard_id": map[string]any{"type": "string"},
				},
				[]string{"scorecard_id"},
			),
		},
		{
			Name:        "get_business_profile",
			Description: "Return the active imported business profile provenance, lifecycle core, warnings, and mapped concepts.",
			InputSchema: emptyObjectSchema(),
		},
		{
			Name:        "list_business_concepts",
			Description: "List object, field, lifecycle, and methodology concepts from the active imported business profile.",
			InputSchema: emptyObjectSchema(),
		},
		{
			Name:        "list_unmapped_crm_fields",
			Description: "List cached CRM fields not mapped by the active business profile with redacted aggregate statistics only.",
			InputSchema: objectSchema(
				map[string]any{
					"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": maxInventoryResults},
				},
				nil,
			),
		},
		{
			Name:        "search_crm_field_values",
			Description: "Search cached CRM field values with redacted identifiers by default; call IDs require include_call_ids=true, and value snippets/call titles require include_value_snippets=true.",
			InputSchema: objectSchema(
				map[string]any{
					"object_type":            map[string]any{"type": "string"},
					"field_name":             map[string]any{"type": "string"},
					"value_query":            map[string]any{"type": "string"},
					"include_value_snippets": map[string]any{"type": "boolean"},
					"include_call_ids":       map[string]any{"type": "boolean"},
					"limit":                  map[string]any{"type": "integer", "minimum": 1, "maximum": maxSearchResults},
				},
				[]string{"object_type", "field_name", "value_query"},
			),
		},
		{
			Name:        "analyze_late_stage_crm_signals",
			Description: "Rank CRM fields by how much more often they are populated on late-stage opportunity calls.",
			InputSchema: objectSchema(
				map[string]any{
					"object_type":           map[string]any{"type": "string"},
					"stage_field":           map[string]any{"type": "string"},
					"late_stage_values":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"include_stage_proxies": map[string]any{"type": "boolean"},
					"limit":                 map[string]any{"type": "integer", "minimum": 1, "maximum": maxLateStageSignals},
				},
				nil,
			),
		},
		{
			Name:        "opportunities_missing_transcripts",
			Description: "Rank cached Opportunities by calls that do not have transcripts indexed, with total call and coverage counts.",
			InputSchema: objectSchema(
				map[string]any{
					"stage_values": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"limit":        map[string]any{"type": "integer", "minimum": 1, "maximum": maxOpportunitySummaries},
				},
				nil,
			),
		},
		{
			Name:        "search_transcripts_by_crm_context",
			Description: "Search transcript snippets for calls tied to a CRM object type and optional object ID.",
			InputSchema: objectSchema(
				map[string]any{
					"query":       map[string]any{"type": "string"},
					"object_type": map[string]any{"type": "string"},
					"object_id":   map[string]any{"type": "string"},
					"limit":       map[string]any{"type": "integer", "minimum": 1, "maximum": maxSearchResults},
				},
				[]string{"query", "object_type"},
			),
		},
		{
			Name:        "opportunity_call_summary",
			Description: "Summarize cached calls by Opportunity, including stage, basic CRM fields, transcript coverage, and latest call.",
			InputSchema: objectSchema(
				map[string]any{
					"stage_values": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"limit":        map[string]any{"type": "integer", "minimum": 1, "maximum": maxOpportunitySummaries},
				},
				nil,
			),
		},
		{
			Name:        "crm_field_population_matrix",
			Description: "Return CRM field population rates grouped by a CRM field, defaulting to Opportunity StageName.",
			InputSchema: objectSchema(
				map[string]any{
					"object_type":    map[string]any{"type": "string"},
					"group_by_field": map[string]any{"type": "string"},
					"limit":          map[string]any{"type": "integer", "minimum": 1, "maximum": maxCRMMatrixCells},
				},
				[]string{"object_type"},
			),
		},
		{
			Name:        "list_lifecycle_buckets",
			Description: "List call lifecycle buckets and the CRM/call signals used to classify them; uses imported profile when available unless lifecycle_source=builtin.",
			InputSchema: objectSchema(
				map[string]any{
					"lifecycle_source": map[string]any{"type": "string"},
				},
				nil,
			),
		},
		{
			Name:        "summarize_calls_by_lifecycle",
			Description: "Summarize call volume, transcript coverage, confidence, and recency by lifecycle bucket; uses imported profile when available unless lifecycle_source=builtin.",
			InputSchema: objectSchema(
				map[string]any{
					"bucket":           map[string]any{"type": "string"},
					"lifecycle_source": map[string]any{"type": "string"},
				},
				nil,
			),
		},
		{
			Name:        "search_calls_by_lifecycle",
			Description: "Search minimized cached calls by lifecycle bucket, optionally only missing transcripts; uses imported profile when available unless lifecycle_source=builtin.",
			InputSchema: objectSchema(
				map[string]any{
					"bucket":                   map[string]any{"type": "string"},
					"missing_transcripts_only": map[string]any{"type": "boolean"},
					"limit":                    map[string]any{"type": "integer", "minimum": 1, "maximum": maxLifecycleResults},
					"lifecycle_source":         map[string]any{"type": "string"},
				},
				nil,
			),
		},
		{
			Name:        "prioritize_transcripts_by_lifecycle",
			Description: "Rank calls missing transcripts by lifecycle bucket, confidence, duration, and mapped deal context.",
			InputSchema: objectSchema(
				map[string]any{
					"bucket":           map[string]any{"type": "string"},
					"limit":            map[string]any{"type": "integer", "minimum": 1, "maximum": maxLifecycleResults},
					"lifecycle_source": map[string]any{"type": "string"},
				},
				nil,
			),
		},
		{
			Name:        "compare_lifecycle_crm_fields",
			Description: "Compare CRM field population rates between two lifecycle buckets.",
			InputSchema: objectSchema(
				map[string]any{
					"bucket_a":    map[string]any{"type": "string"},
					"bucket_b":    map[string]any{"type": "string"},
					"object_type": map[string]any{"type": "string"},
					"limit":       map[string]any{"type": "integer", "minimum": 1, "maximum": maxLifecycleCRMFields},
				},
				[]string{"bucket_a", "bucket_b", "object_type"},
			),
		},
		{
			Name:        "summarize_call_facts",
			Description: "Summarize normalized metadata-only call facts by business dimensions such as lifecycle, stage, scope, system, direction, transcript status, industry, or month.",
			InputSchema: objectSchema(
				map[string]any{
					"group_by":          map[string]any{"type": "string"},
					"lifecycle_bucket":  map[string]any{"type": "string"},
					"lifecycle_source":  map[string]any{"type": "string"},
					"scope":             map[string]any{"type": "string"},
					"system":            map[string]any{"type": "string"},
					"direction":         map[string]any{"type": "string"},
					"transcript_status": map[string]any{"type": "string"},
					"limit":             map[string]any{"type": "integer", "minimum": 1, "maximum": maxCallFactGroups},
				},
				nil,
			),
		},
		{
			Name:        "rank_transcript_backlog",
			Description: "Rank calls missing transcripts by lifecycle, confidence, duration, and Opportunity context so transcript sync work starts with the most useful business calls.",
			InputSchema: objectSchema(
				map[string]any{
					"bucket":           map[string]any{"type": "string"},
					"limit":            map[string]any{"type": "integer", "minimum": 1, "maximum": maxLifecycleResults},
					"lifecycle_source": map[string]any{"type": "string"},
				},
				nil,
			),
		},
		{
			Name:        "search_transcript_segments",
			Description: "Search transcript snippets in the local SQLite FTS index with call and speaker IDs redacted by default.",
			InputSchema: objectSchema(
				map[string]any{
					"query":               map[string]any{"type": "string"},
					"limit":               map[string]any{"type": "integer", "minimum": 1, "maximum": maxSearchResults},
					"include_call_ids":    map[string]any{"type": "boolean"},
					"include_speaker_ids": map[string]any{"type": "boolean"},
				},
				[]string{"query"},
			),
		},
		{
			Name:        "search_transcripts_by_call_facts",
			Description: "Search transcript snippets joined to normalized call facts with date, lifecycle, scope, system, and direction filters; returns bounded evidence excerpts without call IDs, titles, speaker IDs, or full transcript text.",
			InputSchema: objectSchema(
				map[string]any{
					"query":            map[string]any{"type": "string"},
					"from_date":        map[string]any{"type": "string", "description": "Inclusive YYYY-MM-DD call date."},
					"to_date":          map[string]any{"type": "string", "description": "Inclusive YYYY-MM-DD call date."},
					"lifecycle_bucket": map[string]any{"type": "string"},
					"scope":            map[string]any{"type": "string"},
					"system":           map[string]any{"type": "string"},
					"direction":        map[string]any{"type": "string"},
					"limit":            map[string]any{"type": "integer", "minimum": 1, "maximum": maxSearchResults},
				},
				[]string{"query"},
			),
		},
		{
			Name:        "search_transcript_quotes_with_attribution",
			Description: "Search bounded transcript quote snippets and join available call, Account, Opportunity, industry, and attribution-readiness metadata; identifiers and names require explicit opt-in flags.",
			InputSchema: objectSchema(
				map[string]any{
					"query":                     map[string]any{"type": "string"},
					"from_date":                 map[string]any{"type": "string", "description": "Inclusive YYYY-MM-DD call date."},
					"to_date":                   map[string]any{"type": "string", "description": "Inclusive YYYY-MM-DD call date."},
					"lifecycle_bucket":          map[string]any{"type": "string"},
					"industry":                  map[string]any{"type": "string"},
					"account_query":             map[string]any{"type": "string"},
					"opportunity_stage":         map[string]any{"type": "string"},
					"limit":                     map[string]any{"type": "integer", "minimum": 1, "maximum": maxSearchResults},
					"include_call_ids":          map[string]any{"type": "boolean"},
					"include_call_titles":       map[string]any{"type": "boolean"},
					"include_account_names":     map[string]any{"type": "boolean"},
					"include_opportunity_names": map[string]any{"type": "boolean"},
				},
				[]string{"query"},
			),
		},
		{
			Name:        "missing_transcripts",
			Description: "List cached calls that do not yet have transcript segments stored in SQLite.",
			InputSchema: objectSchema(
				map[string]any{
					"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": maxMissingTranscripts},
				},
				nil,
			),
		},
	}
}

func ToolCatalog() []ToolInfo {
	server := NewServer(nil, "gongmcp", "dev")
	out := make([]ToolInfo, 0, len(server.tools))
	for _, item := range server.tools {
		out = append(out, ToolInfo(item))
	}
	return out
}

func FindTool(name string) (ToolInfo, bool) {
	for _, tool := range ToolCatalog() {
		if tool.Name == name {
			return tool, true
		}
	}
	return ToolInfo{}, false
}

func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	reader := bufio.NewReader(r)
	writer := bufio.NewWriter(w)
	defer writer.Flush()
	tracef("serve start")

	for {
		tracef("waiting for frame")
		payload, mode, err := readMessage(reader)
		if err != nil {
			if errors.Is(err, io.EOF) {
				tracef("read eof")
				return nil
			}
			tracef("read error: %v", err)
			return err
		}
		tracef("read frame bytes=%d", len(payload))

		resp := s.handlePayload(ctx, payload)
		if resp == nil {
			tracef("handled notification")
			continue
		}
		tracef("writing response")
		if err := writeMessage(writer, resp, mode); err != nil {
			tracef("write error: %v", err)
			return err
		}
		if err := writer.Flush(); err != nil {
			tracef("flush error: %v", err)
			return err
		}
		tracef("response flushed")
	}
}

func (s *Server) Handle(ctx context.Context, req Request) *response {
	switch req.Method {
	case "initialize":
		return s.ok(req.ID, initializeResult{
			ProtocolVersion: protocolVersion,
			Capabilities: capabilities{
				Tools: toolsCapability{ListChanged: false},
			},
			ServerInfo: serverIdentity{
				Name:    s.name,
				Version: s.version,
			},
		})
	case "notifications/initialized":
		return nil
	case "tools/list":
		return s.ok(req.ID, toolsListResult{Tools: s.tools})
	case "tools/call":
		return s.handleToolsCall(ctx, req)
	default:
		return s.err(req.ID, -32601, "method not found")
	}
}

func (s *Server) handlePayload(ctx context.Context, payload []byte) *response {
	var req Request
	if err := json.Unmarshal(payload, &req); err != nil {
		tracef("payload parse error: %v", err)
		return s.err(nil, -32700, "parse error")
	}
	if req.Method == "" {
		tracef("payload missing method")
		return s.err(req.ID, -32600, "method is required")
	}
	tracef("handle method=%s id=%v", req.Method, req.ID)

	resp := s.Handle(ctx, req)
	if req.ID == nil {
		return nil
	}
	return resp
}

func (s *Server) handleToolsCall(ctx context.Context, req Request) *response {
	var params toolsCallParams
	if err := decodeArgs(req.Params, &params); err != nil {
		return s.err(req.ID, -32602, err.Error())
	}
	if strings.TrimSpace(params.Name) == "" {
		return s.err(req.ID, -32602, "tool name is required")
	}
	if !s.isToolAllowed(params.Name) {
		return s.ok(req.ID, toolErrorResult(errors.New("tool is not available")))
	}

	result, err := s.executeTool(ctx, params)
	if err != nil {
		return s.ok(req.ID, toolErrorResult(err))
	}
	if err := ensureToolResultFits(req.ID, result); err != nil {
		return s.ok(req.ID, toolErrorResult(err))
	}
	return s.ok(req.ID, result)
}

func (s *Server) isToolAllowed(name string) bool {
	if len(s.allowedToolNames) == 0 {
		return true
	}
	_, ok := s.allowedToolNames[name]
	return ok
}

func (s *Server) executeTool(ctx context.Context, params toolsCallParams) (toolCallResult, error) {
	switch params.Name {
	case "get_sync_status":
		return s.getSyncStatus(ctx, params.Arguments)
	case "search_calls":
		return s.searchCalls(ctx, params.Arguments)
	case "get_call":
		return s.getCall(ctx, params.Arguments)
	case "list_crm_object_types":
		return s.listCRMObjectTypes(ctx, params.Arguments)
	case "list_crm_fields":
		return s.listCRMFields(ctx, params.Arguments)
	case "list_crm_integrations":
		return s.listCRMIntegrations(ctx, params.Arguments)
	case "list_cached_crm_schema_objects":
		return s.listCachedCRMSchemaObjects(ctx, params.Arguments)
	case "list_cached_crm_schema_fields":
		return s.listCachedCRMSchemaFields(ctx, params.Arguments)
	case "list_gong_settings":
		return s.listGongSettings(ctx, params.Arguments)
	case "list_scorecards":
		return s.listScorecards(ctx, params.Arguments)
	case "get_scorecard":
		return s.getScorecard(ctx, params.Arguments)
	case "get_business_profile":
		return s.getBusinessProfile(ctx, params.Arguments)
	case "list_business_concepts":
		return s.listBusinessConcepts(ctx, params.Arguments)
	case "list_unmapped_crm_fields":
		return s.listUnmappedCRMFields(ctx, params.Arguments)
	case "search_crm_field_values":
		return s.searchCRMFieldValues(ctx, params.Arguments)
	case "analyze_late_stage_crm_signals":
		return s.analyzeLateStageCRMSignals(ctx, params.Arguments)
	case "opportunities_missing_transcripts":
		return s.opportunitiesMissingTranscripts(ctx, params.Arguments)
	case "search_transcripts_by_crm_context":
		return s.searchTranscriptsByCRMContext(ctx, params.Arguments)
	case "opportunity_call_summary":
		return s.opportunityCallSummary(ctx, params.Arguments)
	case "crm_field_population_matrix":
		return s.crmFieldPopulationMatrix(ctx, params.Arguments)
	case "list_lifecycle_buckets":
		return s.listLifecycleBuckets(ctx, params.Arguments)
	case "summarize_calls_by_lifecycle":
		return s.summarizeCallsByLifecycle(ctx, params.Arguments)
	case "search_calls_by_lifecycle":
		return s.searchCallsByLifecycle(ctx, params.Arguments)
	case "prioritize_transcripts_by_lifecycle":
		return s.prioritizeTranscriptsByLifecycle(ctx, params.Arguments)
	case "compare_lifecycle_crm_fields":
		return s.compareLifecycleCRMFields(ctx, params.Arguments)
	case "summarize_call_facts":
		return s.summarizeCallFacts(ctx, params.Arguments)
	case "rank_transcript_backlog":
		return s.rankTranscriptBacklog(ctx, params.Arguments)
	case "search_transcript_segments":
		return s.searchTranscriptSegments(ctx, params.Arguments)
	case "search_transcripts_by_call_facts":
		return s.searchTranscriptsByCallFacts(ctx, params.Arguments)
	case "search_transcript_quotes_with_attribution":
		return s.searchTranscriptQuotesWithAttribution(ctx, params.Arguments)
	case "missing_transcripts":
		return s.missingTranscripts(ctx, params.Arguments)
	default:
		return toolCallResult{}, fmt.Errorf("unknown tool %q", params.Name)
	}
}

func (s *Server) getSyncStatus(ctx context.Context, raw json.RawMessage) (toolCallResult, error) {
	var args struct{}
	if err := decodeArgs(raw, &args); err != nil {
		return toolCallResult{}, err
	}

	summary, err := s.store.SyncStatusSummary(ctx)
	if err != nil {
		return toolCallResult{}, err
	}
	return newToolResult(mcpSyncStatus(summary))
}

func (s *Server) searchCalls(ctx context.Context, raw json.RawMessage) (toolCallResult, error) {
	var args searchCallsArgs
	if err := decodeArgs(raw, &args); err != nil {
		return toolCallResult{}, err
	}

	rows, err := s.store.SearchCallsRaw(ctx, sqlite.CallSearchParams{
		CRMObjectType: args.CRMObjectType,
		CRMObjectID:   args.CRMObjectID,
		Limit:         args.Limit,
	})
	if err != nil {
		return toolCallResult{}, err
	}

	summaries := make([]searchCallSummary, 0, len(rows))
	for _, rawCall := range rows {
		summary, err := summarizeCall(rawCall)
		if err != nil {
			return toolCallResult{}, err
		}
		summaries = append(summaries, summary)
	}
	return newToolResult(summaries)
}

func (s *Server) getCall(ctx context.Context, raw json.RawMessage) (toolCallResult, error) {
	var args getCallArgs
	if err := decodeArgs(raw, &args); err != nil {
		return toolCallResult{}, err
	}

	detail, err := s.store.GetCallDetail(ctx, args.CallID)
	if err != nil {
		return toolCallResult{}, err
	}
	minimizeCallDetail(detail)
	return newToolResult(detail)
}

func (s *Server) listCRMObjectTypes(ctx context.Context, raw json.RawMessage) (toolCallResult, error) {
	var args struct{}
	if err := decodeArgs(raw, &args); err != nil {
		return toolCallResult{}, err
	}

	rows, err := s.store.ListCRMObjectTypes(ctx)
	if err != nil {
		return toolCallResult{}, err
	}
	return newToolResult(rows)
}

func (s *Server) listCRMFields(ctx context.Context, raw json.RawMessage) (toolCallResult, error) {
	var args listCRMFieldsArgs
	if err := decodeArgs(raw, &args); err != nil {
		return toolCallResult{}, err
	}

	rows, err := s.store.ListCRMFields(ctx, args.ObjectType, args.Limit)
	if err != nil {
		return toolCallResult{}, err
	}
	return newToolResult(rows)
}

func (s *Server) listCRMIntegrations(ctx context.Context, raw json.RawMessage) (toolCallResult, error) {
	var args struct{}
	if err := decodeArgs(raw, &args); err != nil {
		return toolCallResult{}, err
	}

	rows, err := s.store.ListCRMIntegrations(ctx)
	if err != nil {
		return toolCallResult{}, err
	}
	return newToolResult(rows)
}

func (s *Server) listCachedCRMSchemaObjects(ctx context.Context, raw json.RawMessage) (toolCallResult, error) {
	var args listCRMSchemaObjectsArgs
	if err := decodeArgs(raw, &args); err != nil {
		return toolCallResult{}, err
	}

	rows, err := s.store.ListCRMSchemaObjects(ctx, args.IntegrationID)
	if err != nil {
		return toolCallResult{}, err
	}
	return newToolResult(rows)
}

func (s *Server) listCachedCRMSchemaFields(ctx context.Context, raw json.RawMessage) (toolCallResult, error) {
	var args listCRMSchemaFieldsArgs
	if err := decodeArgs(raw, &args); err != nil {
		return toolCallResult{}, err
	}

	rows, err := s.store.ListCRMSchemaFields(ctx, sqlite.CRMSchemaFieldListParams{
		IntegrationID: args.IntegrationID,
		ObjectType:    args.ObjectType,
		Limit:         args.Limit,
	})
	if err != nil {
		return toolCallResult{}, err
	}
	return newToolResult(rows)
}

func (s *Server) listGongSettings(ctx context.Context, raw json.RawMessage) (toolCallResult, error) {
	var args listGongSettingsArgs
	if err := decodeArgs(raw, &args); err != nil {
		return toolCallResult{}, err
	}

	rows, err := s.store.ListGongSettings(ctx, sqlite.GongSettingListParams{
		Kind:  args.Kind,
		Limit: args.Limit,
	})
	if err != nil {
		return toolCallResult{}, err
	}
	return newToolResult(rows)
}

func (s *Server) listScorecards(ctx context.Context, raw json.RawMessage) (toolCallResult, error) {
	var args listScorecardsArgs
	if err := decodeArgs(raw, &args); err != nil {
		return toolCallResult{}, err
	}

	rows, err := s.store.ListScorecards(ctx, sqlite.ScorecardListParams{
		ActiveOnly: args.ActiveOnly,
		Limit:      args.Limit,
	})
	if err != nil {
		return toolCallResult{}, err
	}
	return newToolResult(rows)
}

func (s *Server) getScorecard(ctx context.Context, raw json.RawMessage) (toolCallResult, error) {
	var args getScorecardArgs
	if err := decodeArgs(raw, &args); err != nil {
		return toolCallResult{}, err
	}

	row, err := s.store.GetScorecardDetail(ctx, args.ScorecardID)
	if err != nil {
		return toolCallResult{}, err
	}
	return newToolResult(row)
}

func (s *Server) getBusinessProfile(ctx context.Context, raw json.RawMessage) (toolCallResult, error) {
	var args struct{}
	if err := decodeArgs(raw, &args); err != nil {
		return toolCallResult{}, err
	}
	row, err := s.store.ActiveBusinessProfile(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return toolCallResult{}, noActiveProfileError()
		}
		return toolCallResult{}, err
	}
	return newToolResult(mcpBusinessProfile(row))
}

func (s *Server) listBusinessConcepts(ctx context.Context, raw json.RawMessage) (toolCallResult, error) {
	var args struct{}
	if err := decodeArgs(raw, &args); err != nil {
		return toolCallResult{}, err
	}
	rows, err := s.store.ListBusinessConcepts(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return toolCallResult{}, noActiveProfileError()
		}
		return toolCallResult{}, err
	}
	return newToolResult(rows)
}

func (s *Server) listUnmappedCRMFields(ctx context.Context, raw json.RawMessage) (toolCallResult, error) {
	var args listUnmappedCRMFieldsArgs
	if err := decodeArgs(raw, &args); err != nil {
		return toolCallResult{}, err
	}
	rows, err := s.store.ListUnmappedCRMFields(ctx, sqlite.UnmappedCRMFieldParams{Limit: args.Limit})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return toolCallResult{}, noActiveProfileError()
		}
		return toolCallResult{}, err
	}
	return newToolResult(rows)
}

func (s *Server) searchCRMFieldValues(ctx context.Context, raw json.RawMessage) (toolCallResult, error) {
	var args searchCRMFieldValuesArgs
	if err := decodeArgs(raw, &args); err != nil {
		return toolCallResult{}, err
	}

	rows, err := s.store.SearchCRMFieldValues(ctx, sqlite.CRMFieldValueSearchParams{
		ObjectType:          args.ObjectType,
		FieldName:           args.FieldName,
		ValueQuery:          args.ValueQuery,
		Limit:               args.Limit,
		IncludeValueSnippet: args.IncludeValueSnippets,
	})
	if err != nil {
		return toolCallResult{}, err
	}
	for idx := range rows {
		if !args.IncludeCallIDs {
			rows[idx].CallID = ""
		}
		rows[idx].ObjectID = ""
		rows[idx].ObjectName = ""
		if !args.IncludeValueSnippets {
			rows[idx].Title = ""
		}
	}
	return newToolResult(rows)
}

func (s *Server) analyzeLateStageCRMSignals(ctx context.Context, raw json.RawMessage) (toolCallResult, error) {
	var args analyzeLateStageCRMSignalsArgs
	if err := decodeArgs(raw, &args); err != nil {
		return toolCallResult{}, err
	}

	report, err := s.store.AnalyzeLateStageSignals(ctx, sqlite.LateStageSignalParams{
		ObjectType:          args.ObjectType,
		StageField:          args.StageField,
		LateStageValues:     args.LateStageValues,
		IncludeStageProxies: args.IncludeStageProxies,
		Limit:               args.Limit,
	})
	if err != nil {
		return toolCallResult{}, err
	}
	return newToolResult(report)
}

func (s *Server) opportunitiesMissingTranscripts(ctx context.Context, raw json.RawMessage) (toolCallResult, error) {
	var args opportunitiesMissingTranscriptsArgs
	if err := decodeArgs(raw, &args); err != nil {
		return toolCallResult{}, err
	}

	rows, err := s.store.ListOpportunitiesMissingTranscripts(ctx, sqlite.OpportunityMissingTranscriptParams{
		StageValues: args.StageValues,
		Limit:       args.Limit,
	})
	if err != nil {
		return toolCallResult{}, err
	}
	return newToolResult(mcpOpportunityMissingTranscriptSummaries(rows))
}

func (s *Server) searchTranscriptsByCRMContext(ctx context.Context, raw json.RawMessage) (toolCallResult, error) {
	var args searchTranscriptsByCRMContextArgs
	if err := decodeArgs(raw, &args); err != nil {
		return toolCallResult{}, err
	}

	rows, err := s.store.SearchTranscriptSegmentsByCRMContext(ctx, sqlite.TranscriptCRMSearchParams{
		Query:      args.Query,
		ObjectType: args.ObjectType,
		ObjectID:   args.ObjectID,
		Limit:      args.Limit,
	})
	if err != nil {
		return toolCallResult{}, err
	}
	return newToolResult(mcpTranscriptCRMSearchResults(rows))
}

func mcpOpportunityMissingTranscriptSummaries(rows []sqlite.OpportunityMissingTranscriptSummary) []sqlite.OpportunityMissingTranscriptSummary {
	out := make([]sqlite.OpportunityMissingTranscriptSummary, len(rows))
	for i, row := range rows {
		row.OpportunityID = ""
		row.OpportunityName = ""
		row.LatestCallID = ""
		out[i] = row
	}
	return out
}

func mcpOpportunityCallSummaries(rows []sqlite.OpportunityCallSummary) []sqlite.OpportunityCallSummary {
	out := make([]sqlite.OpportunityCallSummary, len(rows))
	for i, row := range rows {
		row.OpportunityID = ""
		row.OpportunityName = ""
		row.Amount = ""
		row.CloseDate = ""
		row.OwnerID = ""
		row.LatestCallID = ""
		out[i] = row
	}
	return out
}

func mcpTranscriptCRMSearchResults(rows []sqlite.TranscriptCRMSearchResult) []sqlite.TranscriptCRMSearchResult {
	out := make([]sqlite.TranscriptCRMSearchResult, len(rows))
	for i, row := range rows {
		row.CallID = ""
		row.Title = ""
		row.ObjectID = ""
		row.ObjectName = ""
		row.SpeakerID = ""
		out[i] = row
	}
	return out
}

func (s *Server) opportunityCallSummary(ctx context.Context, raw json.RawMessage) (toolCallResult, error) {
	var args opportunityCallSummaryArgs
	if err := decodeArgs(raw, &args); err != nil {
		return toolCallResult{}, err
	}

	rows, err := s.store.SummarizeOpportunityCalls(ctx, sqlite.OpportunityCallSummaryParams{
		StageValues: args.StageValues,
		Limit:       args.Limit,
	})
	if err != nil {
		return toolCallResult{}, err
	}
	return newToolResult(mcpOpportunityCallSummaries(rows))
}

func (s *Server) crmFieldPopulationMatrix(ctx context.Context, raw json.RawMessage) (toolCallResult, error) {
	var args crmFieldPopulationMatrixArgs
	if err := decodeArgs(raw, &args); err != nil {
		return toolCallResult{}, err
	}

	report, err := s.store.CRMFieldPopulationMatrix(ctx, sqlite.CRMFieldPopulationMatrixParams{
		ObjectType:   args.ObjectType,
		GroupByField: args.GroupByField,
		Limit:        args.Limit,
	})
	if err != nil {
		return toolCallResult{}, err
	}
	return newToolResult(report)
}

func (s *Server) listLifecycleBuckets(ctx context.Context, raw json.RawMessage) (toolCallResult, error) {
	var args listLifecycleBucketsArgs
	if err := decodeArgs(raw, &args); err != nil {
		return toolCallResult{}, err
	}

	rows, info, err := s.store.ListLifecycleBucketDefinitionsWithSource(ctx, args.LifecycleSource)
	if err != nil {
		return toolCallResult{}, err
	}
	return newLifecycleToolResult(rows, info, args.LifecycleSource)
}

func (s *Server) summarizeCallsByLifecycle(ctx context.Context, raw json.RawMessage) (toolCallResult, error) {
	var args summarizeCallsByLifecycleArgs
	if err := decodeArgs(raw, &args); err != nil {
		return toolCallResult{}, err
	}

	rows, info, err := s.store.SummarizeCallsByLifecycleWithSource(ctx, sqlite.LifecycleSummaryParams{
		Bucket:          args.Bucket,
		LifecycleSource: args.LifecycleSource,
	})
	if err != nil {
		return toolCallResult{}, err
	}
	return newLifecycleToolResult(rows, info, args.LifecycleSource)
}

func (s *Server) searchCallsByLifecycle(ctx context.Context, raw json.RawMessage) (toolCallResult, error) {
	var args searchCallsByLifecycleArgs
	if err := decodeArgs(raw, &args); err != nil {
		return toolCallResult{}, err
	}

	rows, info, err := s.store.SearchCallsByLifecycleWithSource(ctx, sqlite.LifecycleCallSearchParams{
		Bucket:                 args.Bucket,
		MissingTranscriptsOnly: args.MissingTranscriptsOnly,
		Limit:                  args.Limit,
		LifecycleSource:        args.LifecycleSource,
	})
	if err != nil {
		return toolCallResult{}, err
	}
	return newLifecycleToolResult(rows, info, args.LifecycleSource)
}

func (s *Server) prioritizeTranscriptsByLifecycle(ctx context.Context, raw json.RawMessage) (toolCallResult, error) {
	var args prioritizeTranscriptsByLifecycleArgs
	if err := decodeArgs(raw, &args); err != nil {
		return toolCallResult{}, err
	}

	rows, info, err := s.store.PrioritizeTranscriptsByLifecycleWithSource(ctx, sqlite.LifecycleTranscriptPriorityParams{
		Bucket:          args.Bucket,
		Limit:           args.Limit,
		LifecycleSource: args.LifecycleSource,
	})
	if err != nil {
		return toolCallResult{}, err
	}
	return newLifecycleToolResult(mcpTranscriptPriorities(rows), info, args.LifecycleSource)
}

func mcpTranscriptPriorities(rows []sqlite.LifecycleTranscriptPriority) []sqlite.LifecycleTranscriptPriority {
	out := make([]sqlite.LifecycleTranscriptPriority, len(rows))
	for i, row := range rows {
		row.CallID = ""
		row.Title = ""
		out[i] = row
	}
	return out
}

func mcpSyncStatus(summary *sqlite.SyncStatusSummary) publicSyncStatus {
	profile := summary.ProfileReadiness
	profile.Name = ""
	profile.CanonicalSHA256 = ""
	return publicSyncStatus{
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
		ProfileReadiness:             profile,
		PublicReadiness:              summary.PublicReadiness,
		AttributionCoverage:          summary.AttributionCoverage,
	}
}

func (s *Server) compareLifecycleCRMFields(ctx context.Context, raw json.RawMessage) (toolCallResult, error) {
	var args compareLifecycleCRMFieldsArgs
	if err := decodeArgs(raw, &args); err != nil {
		return toolCallResult{}, err
	}

	report, err := s.store.CompareLifecycleCRMFields(ctx, sqlite.LifecycleCRMFieldComparisonParams{
		BucketA:    args.BucketA,
		BucketB:    args.BucketB,
		ObjectType: args.ObjectType,
		Limit:      args.Limit,
	})
	if err != nil {
		return toolCallResult{}, err
	}
	return newToolResult(report)
}

func (s *Server) summarizeCallFacts(ctx context.Context, raw json.RawMessage) (toolCallResult, error) {
	var args summarizeCallFactsArgs
	if err := decodeArgs(raw, &args); err != nil {
		return toolCallResult{}, err
	}
	if err := validateMCPCallFactsGroupBy(args.GroupBy); err != nil {
		return toolCallResult{}, err
	}

	rows, info, err := s.store.SummarizeCallFactsWithSource(ctx, sqlite.CallFactsSummaryParams{
		GroupBy:          args.GroupBy,
		LifecycleBucket:  args.LifecycleBucket,
		LifecycleSource:  args.LifecycleSource,
		Scope:            args.Scope,
		System:           args.System,
		Direction:        args.Direction,
		TranscriptStatus: args.TranscriptStatus,
		Limit:            args.Limit,
	})
	if err != nil {
		return toolCallResult{}, err
	}
	return newLifecycleToolResult(rows, info, args.LifecycleSource)
}

func validateMCPCallFactsGroupBy(groupBy string) error {
	switch strings.ToLower(strings.TrimSpace(groupBy)) {
	case "",
		"lifecycle", "lifecycle_bucket",
		"stage", "deal_stage", "opportunity_stage",
		"deal_type", "opportunity_type",
		"account_type",
		"account_industry", "industry",
		"revenue_range", "account_revenue_range",
		"scope",
		"system",
		"direction",
		"transcript_status",
		"calendar", "calendar_event_status",
		"duration_bucket",
		"month", "call_month",
		"lead_source", "primary_lead_source",
		"forecast_category":
		return nil
	default:
		return fmt.Errorf("unsupported MCP group_by %q; use list_business_concepts for field discovery and search_crm_field_values with explicit opt-in for value lookups", groupBy)
	}
}

func (s *Server) rankTranscriptBacklog(ctx context.Context, raw json.RawMessage) (toolCallResult, error) {
	var args prioritizeTranscriptsByLifecycleArgs
	if err := decodeArgs(raw, &args); err != nil {
		return toolCallResult{}, err
	}

	rows, info, err := s.store.PrioritizeTranscriptsByLifecycleWithSource(ctx, sqlite.LifecycleTranscriptPriorityParams{
		Bucket:          args.Bucket,
		Limit:           args.Limit,
		LifecycleSource: args.LifecycleSource,
	})
	if err != nil {
		return toolCallResult{}, err
	}
	return newLifecycleToolResult(mcpTranscriptPriorities(rows), info, args.LifecycleSource)
}

func (s *Server) searchTranscriptSegments(ctx context.Context, raw json.RawMessage) (toolCallResult, error) {
	var args searchTranscriptSegmentsArgs
	if err := decodeArgs(raw, &args); err != nil {
		return toolCallResult{}, err
	}

	results, err := s.store.SearchTranscriptSegments(ctx, args.Query, args.Limit)
	if err != nil {
		return toolCallResult{}, err
	}

	snippets := make([]transcriptSnippet, 0, len(results))
	for _, row := range results {
		callID := row.CallID
		if !args.IncludeCallIDs {
			callID = ""
		}
		speakerID := row.SpeakerID
		if !args.IncludeSpeakerIDs {
			speakerID = ""
		}
		snippets = append(snippets, transcriptSnippet{
			CallID:       callID,
			SpeakerID:    speakerID,
			SegmentIndex: row.SegmentIndex,
			StartMS:      row.StartMS,
			EndMS:        row.EndMS,
			Snippet:      row.Snippet,
		})
	}
	return newToolResult(snippets)
}

func (s *Server) searchTranscriptsByCallFacts(ctx context.Context, raw json.RawMessage) (toolCallResult, error) {
	var args searchTranscriptsByCallFactsArgs
	if err := decodeArgs(raw, &args); err != nil {
		return toolCallResult{}, err
	}

	results, err := s.store.SearchTranscriptSegmentsByCallFacts(ctx, sqlite.TranscriptCallFactsSearchParams{
		Query:           args.Query,
		FromDate:        args.FromDate,
		ToDate:          args.ToDate,
		LifecycleBucket: args.LifecycleBucket,
		Scope:           args.Scope,
		System:          args.System,
		Direction:       args.Direction,
		Limit:           args.Limit,
	})
	if err != nil {
		return toolCallResult{}, err
	}
	return newToolResult(results)
}

func (s *Server) searchTranscriptQuotesWithAttribution(ctx context.Context, raw json.RawMessage) (toolCallResult, error) {
	var args searchTranscriptQuotesWithAttributionArgs
	if err := decodeArgs(raw, &args); err != nil {
		return toolCallResult{}, err
	}
	if strings.TrimSpace(args.AccountQuery) != "" && !args.IncludeAccountNames {
		return toolCallResult{}, errors.New("account_query requires include_account_names=true because it can probe customer names")
	}

	results, err := s.store.SearchTranscriptQuotesWithAttribution(ctx, sqlite.TranscriptAttributionSearchParams{
		Query:            args.Query,
		FromDate:         args.FromDate,
		ToDate:           args.ToDate,
		LifecycleBucket:  args.LifecycleBucket,
		Industry:         args.Industry,
		AccountQuery:     args.AccountQuery,
		OpportunityStage: args.OpportunityStage,
		Limit:            args.Limit,
	})
	if err != nil {
		return toolCallResult{}, err
	}
	return newToolResult(mcpTranscriptAttributionResults(results, args))
}

func mcpTranscriptAttributionResults(rows []sqlite.TranscriptAttributionSearchResult, args searchTranscriptQuotesWithAttributionArgs) []sqlite.TranscriptAttributionSearchResult {
	out := make([]sqlite.TranscriptAttributionSearchResult, len(rows))
	for i, row := range rows {
		if !args.IncludeCallIDs {
			row.CallID = ""
		}
		if !args.IncludeCallTitles {
			row.Title = ""
		}
		if !args.IncludeAccountNames {
			row.AccountName = ""
			row.AccountWebsite = ""
		}
		if !args.IncludeOpportunityNames {
			row.OpportunityName = ""
			row.OpportunityCloseDate = ""
			row.OpportunityProbability = ""
		}
		out[i] = row
	}
	return out
}

func (s *Server) missingTranscripts(ctx context.Context, raw json.RawMessage) (toolCallResult, error) {
	var args missingTranscriptsArgs
	if err := decodeArgs(raw, &args); err != nil {
		return toolCallResult{}, err
	}

	rows, err := s.store.FindCallsMissingTranscripts(ctx, args.Limit)
	if err != nil {
		return toolCallResult{}, err
	}
	return newToolResult(rows)
}

func (s *Server) ok(id any, result any) *response {
	return &response{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
}

func (s *Server) err(id any, code int, message string) *response {
	return &response{
		JSONRPC: "2.0",
		ID:      id,
		Error: &responseError{
			Code:    code,
			Message: message,
		},
	}
}

func readFrame(r *bufio.Reader) ([]byte, error) {
	payload, _, err := readMessage(r)
	return payload, err
}

func readMessage(r *bufio.Reader) ([]byte, frameMode, error) {
	contentLength := -1
	sawHeader := false

	for {
		line, err := readLineLimited(r, maxFrameBytes)
		if err != nil {
			if errors.Is(err, io.EOF) && !sawHeader && len(line) == 0 {
				return nil, frameModeHeader, io.EOF
			}
			return nil, frameModeHeader, err
		}
		trimmedLine := strings.TrimSpace(line)
		if !sawHeader && strings.HasPrefix(trimmedLine, "{") {
			tracef("json-line frame bytes=%d", len(trimmedLine))
			return []byte(trimmedLine), frameModeLine, nil
		}
		sawHeader = true
		tracef("frame header line=%q", strings.TrimRight(line, "\r\n"))

		if line == "\r\n" || line == "\n" {
			break
		}

		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return nil, frameModeHeader, fmt.Errorf("invalid frame header %q", strings.TrimSpace(line))
		}
		if strings.EqualFold(strings.TrimSpace(key), "Content-Length") {
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || n < 0 {
				return nil, frameModeHeader, fmt.Errorf("invalid Content-Length %q", strings.TrimSpace(value))
			}
			if n > maxFrameBytes {
				return nil, frameModeHeader, fmt.Errorf("Content-Length %d exceeds maximum %d", n, maxFrameBytes)
			}
			contentLength = n
		}
	}

	if contentLength < 0 {
		return nil, frameModeHeader, errors.New("missing Content-Length header")
	}
	tracef("frame content length=%d", contentLength)

	payload := make([]byte, contentLength)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, frameModeHeader, err
	}
	return payload, frameModeHeader, nil
}

func writeFrame(w io.Writer, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(payload) > maxFrameBytes {
		return fmt.Errorf("response frame exceeds maximum %d bytes", maxFrameBytes)
	}
	if _, err := fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(payload)); err != nil {
		return err
	}
	_, err = w.Write(payload)
	return err
}

func writeMessage(w io.Writer, value any, mode frameMode) error {
	if mode != frameModeLine {
		return writeFrame(w, value)
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(payload) > maxFrameBytes {
		return fmt.Errorf("response frame exceeds maximum %d bytes", maxFrameBytes)
	}
	if _, err := w.Write(payload); err != nil {
		return err
	}
	_, err = io.WriteString(w, "\n")
	return err
}

func decodeArgs(raw json.RawMessage, dst any) error {
	payload := bytes.TrimSpace(raw)
	if len(payload) == 0 {
		payload = []byte("{}")
	}

	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	return nil
}

func newToolResult(value any) (toolCallResult, error) {
	text, err := jsonText(value)
	if err != nil {
		return toolCallResult{}, err
	}
	return toolCallResult{
		Content: []toolContent{
			{
				Type: "text",
				Text: text,
			},
		},
	}, nil
}

func newLifecycleToolResult(results any, info *sqlite.ProfileQueryInfo, requested string) (toolCallResult, error) {
	if info == nil || (info.Profile == nil && strings.TrimSpace(requested) == "") {
		return newToolResult(results)
	}
	return newToolResult(map[string]any{
		"lifecycle_source":     info.LifecycleSource,
		"profile":              mcpBusinessProfile(info.Profile),
		"unavailable_concepts": info.UnavailableConcepts,
		"results":              results,
	})
}

func mcpBusinessProfile(profile *sqlite.BusinessProfile) *sqlite.BusinessProfile {
	if profile == nil {
		return nil
	}
	cp := *profile
	if cp.SourcePath != "" {
		cp.SourcePath = ""
	}
	cp.SourceSHA256 = ""
	cp.CanonicalSHA256 = ""
	if cp.ImportedBy != "" {
		cp.ImportedBy = "redacted"
	}
	return &cp
}

func noActiveProfileError() error {
	return errors.New("no active profile; run gongctl profile discover, validate, and import first")
}

func toolErrorResult(err error) toolCallResult {
	text, marshalErr := jsonText(map[string]string{"error": err.Error()})
	if marshalErr != nil {
		text = `{"error":"tool execution failed"}`
	}
	return toolCallResult{
		Content: []toolContent{
			{
				Type: "text",
				Text: text,
			},
		},
		IsError: true,
	}
}

func minimizeCallDetail(detail *sqlite.CallDetail) {
	if detail == nil {
		return
	}
	if len(detail.CRMObjects) > maxCallDetailObjects {
		detail.CRMObjects = detail.CRMObjects[:maxCallDetailObjects]
		detail.CRMObjectsTruncated = true
	}
	for idx := range detail.CRMObjects {
		detail.CRMObjects[idx].ObjectName = ""
		if len(detail.CRMObjects[idx].FieldNames) > maxCallDetailFieldNames {
			detail.CRMObjects[idx].FieldNames = detail.CRMObjects[idx].FieldNames[:maxCallDetailFieldNames]
			detail.CRMObjects[idx].FieldNamesTruncated = true
		}
	}
}

func ensureToolResultFits(id any, result toolCallResult) error {
	payload, err := json.Marshal(response{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	})
	if err != nil {
		return err
	}
	if len(payload) > maxFrameBytes {
		return fmt.Errorf("tool result exceeds maximum %d bytes after MCP framing", maxFrameBytes)
	}
	return nil
}

func jsonText(value any) (string, error) {
	switch raw := value.(type) {
	case json.RawMessage:
		if !json.Valid(raw) {
			return "", errors.New("invalid JSON payload")
		}
		if len(raw) > maxToolResultBytes {
			return "", fmt.Errorf("tool result exceeds maximum %d bytes", maxToolResultBytes)
		}
		return string(raw), nil
	default:
		payload, err := json.Marshal(value)
		if err != nil {
			return "", err
		}
		if len(payload) > maxToolResultBytes {
			return "", fmt.Errorf("tool result exceeds maximum %d bytes", maxToolResultBytes)
		}
		return string(payload), nil
	}
}

func readLineLimited(r *bufio.Reader, maxBytes int) (string, error) {
	var out []byte
	for {
		chunk, err := r.ReadSlice('\n')
		out = append(out, chunk...)
		if len(out) > maxBytes {
			return "", fmt.Errorf("frame line exceeds maximum %d bytes", maxBytes)
		}
		if err == nil {
			return string(out), nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return string(out), err
	}
}

func tracef(format string, args ...any) {
	path := strings.TrimSpace(os.Getenv("GONGMCP_TRACE"))
	if path == "" {
		return
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer file.Close()

	prefix := fmt.Sprintf("%s pid=%d ", time.Now().UTC().Format(time.RFC3339Nano), os.Getpid())
	_, _ = fmt.Fprintf(file, prefix+format+"\n", args...)
}

func summarizeCall(raw json.RawMessage) (searchCallSummary, error) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return searchCallSummary{}, err
	}
	metaData, _ := payload["metaData"].(map[string]any)

	callID := firstJSONString(payload, "id", "callId")
	if callID == "" {
		callID = firstJSONString(metaData, "id", "callId")
	}
	title := firstJSONString(payload, "title")
	if title == "" {
		title = firstJSONString(metaData, "title")
	}
	started := firstJSONString(payload, "started", "startedAt")
	if started == "" {
		started = firstJSONString(metaData, "started", "startedAt")
	}
	duration := int64JSONValue(payload["duration"])
	if duration == 0 {
		duration = int64JSONValue(metaData["duration"])
	}
	partiesCount := jsonArrayLen(payload["parties"])
	if partiesCount == 0 {
		partiesCount = jsonArrayLen(metaData["parties"])
	}

	return searchCallSummary{
		CallID:          callID,
		Title:           title,
		StartedAt:       started,
		DurationSeconds: duration,
		PartiesCount:    partiesCount,
	}, nil
}

func summarizeCallDetail(raw json.RawMessage) (callDetail, error) {
	summary, err := summarizeCall(raw)
	if err != nil {
		return callDetail{}, err
	}

	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return callDetail{}, err
	}
	objects := collectCallDetailCRMObjects(payload)
	truncatedObjects := false
	if len(objects) > maxCallDetailObjects {
		objects = objects[:maxCallDetailObjects]
		truncatedObjects = true
	}

	return callDetail{
		CallID:              summary.CallID,
		Title:               summary.Title,
		StartedAt:           summary.StartedAt,
		DurationSeconds:     summary.DurationSeconds,
		PartiesCount:        summary.PartiesCount,
		CRMObjects:          objects,
		CRMObjectsTruncated: truncatedObjects,
	}, nil
}

func collectCallDetailCRMObjects(root map[string]any) []callDetailCRMObject {
	var out []callDetailCRMObject
	for _, key := range []string{"context", "crmContext", "crm", "extendedContext", "crmObjects", "objects"} {
		value, ok := root[key]
		if !ok {
			continue
		}
		out = append(out, collectCallDetailCRMObjectsFromValue(key, value)...)
	}
	return out
}

func collectCallDetailCRMObjectsFromValue(defaultType string, value any) []callDetailCRMObject {
	switch typed := value.(type) {
	case []any:
		rows := make([]callDetailCRMObject, 0, len(typed))
		for _, item := range typed {
			itemMap, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if row, ok := buildCallDetailCRMObject(defaultType, itemMap); ok {
				rows = append(rows, row)
				continue
			}
			rows = append(rows, collectCallDetailCRMObjectsFromValue(defaultType, itemMap)...)
		}
		return rows
	case map[string]any:
		if row, ok := buildCallDetailCRMObject(defaultType, typed); ok {
			return []callDetailCRMObject{row}
		}
		var rows []callDetailCRMObject
		for key, child := range typed {
			rows = append(rows, collectCallDetailCRMObjectsFromValue(key, child)...)
		}
		return rows
	default:
		return nil
	}
}

func buildCallDetailCRMObject(defaultType string, doc map[string]any) (callDetailCRMObject, bool) {
	fieldsValue, ok := doc["fields"]
	if !ok {
		fieldsValue, ok = doc["properties"]
	}
	if !ok {
		return callDetailCRMObject{}, false
	}

	fieldNames, populatedCount, fieldCount := summarizeCRMFieldNames(fieldsValue)
	fieldNamesTruncated := false
	if len(fieldNames) > maxCallDetailFieldNames {
		fieldNames = fieldNames[:maxCallDetailFieldNames]
		fieldNamesTruncated = true
	}

	objectType := firstJSONString(doc, "objectType", "type", "entityType")
	if objectType == "" {
		objectType = defaultType
	}
	objectName := firstJSONString(doc, "name", "displayName", "label", "title")
	if objectName == "" {
		objectName = crmNameFromFields(fieldsValue)
	}
	return callDetailCRMObject{
		ObjectType:          objectType,
		ObjectID:            firstJSONString(doc, "id", "objectId", "crmId"),
		ObjectName:          objectName,
		FieldCount:          fieldCount,
		PopulatedFieldCount: populatedCount,
		FieldNames:          fieldNames,
		FieldNamesTruncated: fieldNamesTruncated,
	}, true
}

func crmNameFromFields(value any) string {
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			itemMap, ok := item.(map[string]any)
			if !ok {
				continue
			}
			name := firstJSONString(itemMap, "name", "fieldName", "apiName", "label", "displayName")
			if strings.EqualFold(strings.TrimSpace(name), "Name") {
				return strings.TrimSpace(firstJSONString(itemMap, "value"))
			}
		}
	case map[string]any:
		if value, ok := typed["Name"]; ok {
			return strings.TrimSpace(fmt.Sprint(value))
		}
		for key, value := range typed {
			if strings.EqualFold(strings.TrimSpace(key), "Name") {
				return strings.TrimSpace(fmt.Sprint(value))
			}
		}
	}
	return ""
}

func summarizeCRMFieldNames(value any) ([]string, int, int) {
	names := map[string]struct{}{}
	populatedCount := 0
	fieldCount := 0

	add := func(name string, populated bool) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		fieldCount++
		if populated {
			populatedCount++
		}
		names[name] = struct{}{}
	}

	switch typed := value.(type) {
	case []any:
		for idx, item := range typed {
			itemMap, ok := item.(map[string]any)
			if !ok {
				continue
			}
			name := firstJSONString(itemMap, "name", "fieldName", "apiName", "label", "displayName")
			if name == "" {
				name = fmt.Sprintf("field_%d", idx)
			}
			add(name, jsonValuePresent(itemMap["value"]))
		}
	case map[string]any:
		for key, child := range typed {
			add(key, jsonValuePresent(child))
		}
	}

	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, populatedCount, fieldCount
}

func jsonValuePresent(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(typed) != ""
	case []any:
		return len(typed) > 0
	case map[string]any:
		return len(typed) > 0
	default:
		return true
	}
}

func firstJSONString(doc map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := doc[key]
		if !ok {
			continue
		}
		if text, ok := value.(string); ok {
			return strings.TrimSpace(text)
		}
	}
	return ""
}

func int64JSONValue(value any) int64 {
	switch typed := value.(type) {
	case float64:
		return int64(typed)
	case int64:
		return typed
	case int:
		return int64(typed)
	case json.Number:
		n, _ := typed.Int64()
		return n
	case string:
		n, _ := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return n
	default:
		return 0
	}
}

func jsonArrayLen(value any) int {
	if items, ok := value.([]any); ok {
		return len(items)
	}
	return 0
}

func emptyObjectSchema() map[string]any {
	return objectSchema(map[string]any{}, nil)
}

func objectSchema(properties map[string]any, required []string) map[string]any {
	schema := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}
