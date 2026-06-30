package mcp

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/fyne-coder/gongcli_mcp/internal/governance"
	"github.com/fyne-coder/gongcli_mcp/internal/store/crmdimensions"
	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
)

const protocolVersion = "2025-11-25"
const maxFrameBytes = 1 << 20
const maxToolResultBytes = maxFrameBytes - 4096
const httpToolCallTimeout = 60 * time.Second

var errHTTPPayloadTooLarge = fmt.Errorf("request body exceeds maximum %d bytes", maxFrameBytes)

type frameMode int

const (
	frameModeHeader frameMode = iota
	frameModeLine
)

type Store interface {
	SyncStatusSummary(ctx context.Context) (*sqlite.SyncStatusSummary, error)
	SearchCallsRaw(ctx context.Context, params sqlite.CallSearchParams) ([]json.RawMessage, error)
	GetCallDetail(ctx context.Context, callID string) (*sqlite.CallDetail, error)
	ResolveCallIDByRef(ctx context.Context, ref string) (string, error)
	ListCRMObjectTypes(ctx context.Context) ([]sqlite.CRMObjectTypeSummary, error)
	ListCRMFields(ctx context.Context, objectType string, limit int) ([]sqlite.CRMFieldSummary, error)
	SearchCRMFieldValues(ctx context.Context, params sqlite.CRMFieldValueSearchParams) ([]sqlite.CRMFieldValueMatch, error)
	ListCRMIntegrations(ctx context.Context) ([]sqlite.CRMIntegrationRecord, error)
	ListCRMSchemaObjects(ctx context.Context, integrationID string) ([]sqlite.CRMSchemaObjectRecord, error)
	ListCRMSchemaFields(ctx context.Context, params sqlite.CRMSchemaFieldListParams) ([]sqlite.CRMSchemaFieldRecord, error)
	ListGongSettings(ctx context.Context, params sqlite.GongSettingListParams) ([]sqlite.GongSettingRecord, error)
	ListScorecards(ctx context.Context, params sqlite.ScorecardListParams) ([]sqlite.ScorecardSummary, error)
	GetScorecardDetail(ctx context.Context, scorecardID string) (*sqlite.ScorecardDetail, error)
	ScorecardActivityOverview(ctx context.Context, limit int) (*sqlite.ScorecardActivityOverview, error)
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
	SearchBusinessAnalysisCalls(ctx context.Context, params sqlite.BusinessAnalysisCallSearchParams) (*sqlite.BusinessAnalysisCallSearchResult, error)
	SearchBusinessAnalysisEvidence(ctx context.Context, params sqlite.BusinessAnalysisEvidenceSearchParams) ([]sqlite.BusinessAnalysisEvidenceRow, error)
	SummarizeBusinessAnalysisDimension(ctx context.Context, params sqlite.BusinessAnalysisDimensionSummaryParams) ([]sqlite.BusinessAnalysisDimensionRow, error)
	FindCallsMissingTranscripts(ctx context.Context, limit int) ([]sqlite.MissingTranscriptCall, error)
	FindCallsMissingTranscriptsByFilters(ctx context.Context, params sqlite.MissingTranscriptSearchParams) ([]sqlite.MissingTranscriptCall, error)
	ListAIHighlights(ctx context.Context, params sqlite.AIHighlightListParams) ([]sqlite.AIHighlightRow, error)
	CallDrilldownEvidence(ctx context.Context, params sqlite.CallDrilldownEvidenceParams) ([]sqlite.CallDrilldownEvidenceRow, error)
}

type Server struct {
	store                        Store
	name                         string
	version                      string
	runtimeInfo                  RuntimeInfo
	tools                        []tool
	limitPolicy                  LimitPolicy
	transcriptEvidenceProvenance TranscriptEvidenceProvenance
	businessAnalysisSmallCellMin int
	internalParticipantDomains   []string
	allowedToolNames             map[string]struct{}
	facadeRoutedToolNames        map[string]struct{}
	suppressedCallIDs            map[string]struct{}
	restrictedAccountQueries     map[string]struct{}
	governanceCheck              func(context.Context) error
	policySwitches               PolicySwitches
	blocklistGuard               *governance.BlocklistGuard
}

// BlocklistGuard is re-exported from the governance package so MCP
// server-option callers do not need to import internal/governance directly.
type BlocklistGuard = governance.BlocklistGuard

type ServerOption func(*Server)

// RuntimeInfo captures non-secret runtime metadata that helps MCP clients and
// manual testers prove which server instance they are connected to.
type RuntimeInfo struct {
	Commit       string
	BuildDate    string
	ToolPreset   string
	DeploymentID string
	StartedAtUTC string
}

type PublicRuntimeInfo struct {
	Name                         string         `json:"name"`
	Version                      string         `json:"version"`
	Commit                       string         `json:"commit,omitempty"`
	BuildDate                    string         `json:"build_date,omitempty"`
	ToolPreset                   string         `json:"tool_preset,omitempty"`
	DeploymentID                 string         `json:"deployment_id,omitempty"`
	StartedAtUTC                 string         `json:"started_at_utc,omitempty"`
	ToolCount                    int            `json:"tool_count"`
	FacadeRoutedToolCount        int            `json:"facade_routed_tool_count"`
	TranscriptEvidenceProvenance string         `json:"transcript_evidence_provenance"`
	PolicySwitches               PolicySwitches `json:"policy_switches"`
	PolicySwitchesEnabled        []string       `json:"policy_switches_enabled,omitempty"`
	PolicySwitchReloadContract   string         `json:"policy_switch_reload_contract"`
}

type TranscriptEvidenceProvenance string

const (
	TranscriptEvidenceRedacted TranscriptEvidenceProvenance = "redacted"
	TranscriptEvidenceAlias    TranscriptEvidenceProvenance = "alias"
	TranscriptEvidenceRaw      TranscriptEvidenceProvenance = "raw"
)

func ParseTranscriptEvidenceProvenance(value string) (TranscriptEvidenceProvenance, error) {
	switch TranscriptEvidenceProvenance(strings.ToLower(strings.TrimSpace(value))) {
	case "", TranscriptEvidenceRedacted:
		return TranscriptEvidenceRedacted, nil
	case TranscriptEvidenceAlias:
		return TranscriptEvidenceAlias, nil
	case TranscriptEvidenceRaw:
		return TranscriptEvidenceRaw, nil
	default:
		return "", fmt.Errorf("transcript evidence provenance must be one of: redacted, alias, raw")
	}
}

func normalizeTranscriptEvidenceProvenance(value TranscriptEvidenceProvenance) TranscriptEvidenceProvenance {
	provenance, err := ParseTranscriptEvidenceProvenance(string(value))
	if err != nil {
		return TranscriptEvidenceRedacted
	}
	return provenance
}

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
	MCPServer                    PublicRuntimeInfo                  `json:"mcp_server"`
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
	CRMObjectType    string `json:"crm_object_type"`
	CRMObjectID      string `json:"crm_object_id"`
	FromDate         string `json:"from_date"`
	ToDate           string `json:"to_date"`
	LifecycleBucket  string `json:"lifecycle_bucket"`
	Scope            string `json:"scope"`
	System           string `json:"system"`
	Direction        string `json:"direction"`
	TranscriptStatus string `json:"transcript_status"`
	Limit            int    `json:"limit"`
}

type getCallArgs struct {
	CallID  string `json:"call_id"`
	CallRef string `json:"call_ref"`
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

type summarizeScorecardActivityArgs struct {
	Limit int `json:"limit"`
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
	FromDate        string `json:"from_date"`
	ToDate          string `json:"to_date"`
	Scope           string `json:"scope"`
	System          string `json:"system"`
	Direction       string `json:"direction"`
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
	FromDate          string `json:"from_date"`
	ToDate            string `json:"to_date"`
	LifecycleBucket   string `json:"lifecycle_bucket"`
	Scope             string `json:"scope"`
	System            string `json:"system"`
	Direction         string `json:"direction"`
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
	Scope                   string `json:"scope"`
	System                  string `json:"system"`
	Direction               string `json:"direction"`
	TranscriptStatus        string `json:"transcript_status"`
	Industry                string `json:"industry"`
	AccountQuery            string `json:"account_query"`
	OpportunityStage        string `json:"opportunity_stage"`
	Limit                   int    `json:"limit"`
	FieldProfile            string `json:"field_profile"`
	SpeakerRole             string `json:"speaker_role"`
	IncludeCallIDs          bool   `json:"include_call_ids"`
	IncludeCallTitles       bool   `json:"include_call_titles"`
	IncludeAccountNames     bool   `json:"include_account_names"`
	IncludeOpportunityNames bool   `json:"include_opportunity_names"`
}

type missingTranscriptsArgs struct {
	FromDate        string `json:"from_date"`
	ToDate          string `json:"to_date"`
	LifecycleBucket string `json:"lifecycle_bucket"`
	Scope           string `json:"scope"`
	System          string `json:"system"`
	Direction       string `json:"direction"`
	CRMObjectType   string `json:"crm_object_type"`
	CRMObjectID     string `json:"crm_object_id"`
	Limit           int    `json:"limit"`
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
	CallID       string `json:"call_id,omitempty"`
	CallRef      string `json:"call_ref,omitempty"`
	SpeakerID    string `json:"speaker_id,omitempty"`
	SpeakerRef   string `json:"speaker_ref,omitempty"`
	SegmentIndex int    `json:"segment_index"`
	StartMS      int64  `json:"start_ms"`
	EndMS        int64  `json:"end_ms"`
	Snippet      string `json:"snippet"`
}

type transcriptCallFactsSnippet struct {
	CallID          string `json:"call_id,omitempty"`
	CallRef         string `json:"call_ref,omitempty"`
	SpeakerID       string `json:"speaker_id,omitempty"`
	SpeakerRef      string `json:"speaker_ref,omitempty"`
	StartedAt       string `json:"started_at"`
	CallDate        string `json:"call_date"`
	CallMonth       string `json:"call_month"`
	DurationSeconds int64  `json:"duration_seconds"`
	LifecycleBucket string `json:"lifecycle_bucket"`
	Scope           string `json:"scope"`
	System          string `json:"system"`
	Direction       string `json:"direction"`
	SegmentIndex    int    `json:"segment_index"`
	StartMS         int64  `json:"start_ms"`
	EndMS           int64  `json:"end_ms"`
	Snippet         string `json:"snippet"`
	ContextExcerpt  string `json:"context_excerpt"`
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
		store:                        store,
		name:                         name,
		version:                      version,
		runtimeInfo:                  RuntimeInfo{StartedAtUTC: time.Now().UTC().Format(time.RFC3339)},
		limitPolicy:                  DefaultLimitPolicy(),
		transcriptEvidenceProvenance: TranscriptEvidenceRedacted,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(server)
		}
	}
	server.limitPolicy = server.limitPolicy.Normalize()
	server.transcriptEvidenceProvenance = normalizeTranscriptEvidenceProvenance(server.transcriptEvidenceProvenance)
	server.tools = defaultTools(server.limitPolicy)
	if len(server.allowedToolNames) > 0 {
		filtered := make([]tool, 0, len(server.tools))
		for _, item := range server.tools {
			if _, ok := server.allowedToolNames[item.Name]; ok {
				filtered = append(filtered, item)
			}
		}
		server.tools = filtered
	}
	return server
}

func WithRuntimeInfo(info RuntimeInfo) ServerOption {
	return func(s *Server) {
		if strings.TrimSpace(info.StartedAtUTC) == "" {
			info.StartedAtUTC = s.runtimeInfo.StartedAtUTC
		}
		s.runtimeInfo = info
	}
}

func (s *Server) publicRuntimeInfo() PublicRuntimeInfo {
	return PublicRuntimeInfo{
		Name:                         s.name,
		Version:                      s.version,
		Commit:                       strings.TrimSpace(s.runtimeInfo.Commit),
		BuildDate:                    strings.TrimSpace(s.runtimeInfo.BuildDate),
		ToolPreset:                   strings.TrimSpace(s.runtimeInfo.ToolPreset),
		DeploymentID:                 strings.TrimSpace(s.runtimeInfo.DeploymentID),
		StartedAtUTC:                 strings.TrimSpace(s.runtimeInfo.StartedAtUTC),
		ToolCount:                    len(s.tools),
		FacadeRoutedToolCount:        s.facadeAvailableOperationCount(),
		TranscriptEvidenceProvenance: string(s.transcriptEvidenceProvenance),
		PolicySwitches:               s.policySwitches,
		PolicySwitchesEnabled:        s.policySwitches.EnabledNames(),
		PolicySwitchReloadContract:   PolicySwitchReloadContract(),
	}
}

func (s *Server) RuntimeInfo() PublicRuntimeInfo {
	return s.publicRuntimeInfo()
}

func (s *Server) facadeAvailableOperationCount() int {
	count := 0
	for _, op := range FacadeOperations() {
		if s.facadeRoutedToolAvailable(op) {
			count++
		}
	}
	return count
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
		s.allowedToolNames = allowset
	}
}

func WithFacadeRoutedToolAllowlist(names []string) ServerOption {
	allowset := make(map[string]struct{}, len(names))
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" || isFacadeTool(name) {
			continue
		}
		allowset[name] = struct{}{}
	}
	if len(allowset) == 0 {
		return nil
	}
	return func(s *Server) {
		s.facadeRoutedToolNames = allowset
	}
}

func WithLimitPolicy(policy LimitPolicy) ServerOption {
	return func(s *Server) {
		s.limitPolicy = policy.Normalize()
	}
}

func WithTranscriptEvidenceProvenance(provenance TranscriptEvidenceProvenance) ServerOption {
	return func(s *Server) {
		s.transcriptEvidenceProvenance = normalizeTranscriptEvidenceProvenance(provenance)
	}
}

func WithBusinessAnalysisSmallCellMin(min int) ServerOption {
	return func(s *Server) {
		if min > 1 {
			s.businessAnalysisSmallCellMin = min
		}
	}
}

func WithSuppressedCallIDs(callIDs []string) ServerOption {
	return func(s *Server) {
		if len(callIDs) == 0 {
			return
		}
		s.suppressedCallIDs = make(map[string]struct{}, len(callIDs))
		for _, callID := range callIDs {
			if trimmed := strings.TrimSpace(callID); trimmed != "" {
				s.suppressedCallIDs[trimmed] = struct{}{}
			}
		}
	}
}

func WithRestrictedAccountQueryTerms(terms []string) ServerOption {
	return func(s *Server) {
		if len(terms) == 0 {
			return
		}
		s.restrictedAccountQueries = make(map[string]struct{}, len(terms))
		for _, term := range terms {
			if normalized := governance.NormalizeName(term); normalized != "" {
				s.restrictedAccountQueries[normalized] = struct{}{}
			}
		}
	}
}

func WithGovernanceCheck(check func(context.Context) error) ServerOption {
	return func(s *Server) {
		s.governanceCheck = check
	}
}

// WithPolicySwitches installs the customer-deployment policy switch contract
// for this MCP process. Switches take effect at startup and require a restart
// to change; see PolicySwitchReloadContract.
func WithPolicySwitches(switches PolicySwitches) ServerOption {
	return func(s *Server) {
		s.policySwitches = switches
	}
}

// WithBlocklistGuard installs an emit-time defense-in-depth filter that
// suppresses MCP tool output rows/fields whenever a blocklisted entity is
// detected. The guard sits in front of MCP serialization on the highest-risk
// paths (search_calls, get_call) and is intended to catch missed joins or
// new columns that bypass the source-to-serving redaction.
func WithBlocklistGuard(guard *BlocklistGuard) ServerOption {
	return func(s *Server) {
		if guard == nil || guard.Empty() {
			return
		}
		s.blocklistGuard = guard
	}
}

func defaultTools(policy LimitPolicy) []tool {
	policy = policy.Normalize()
	tools := []tool{
		{
			Name:        "get_sync_status",
			Description: "Return cached sync run metadata and local store record counts.",
			InputSchema: emptyObjectSchema(),
		},
		{
			Name:        "search_calls",
			Description: "Search cached calls by stored CRM context filters and return summarized call metadata.",
			InputSchema: objectSchema(
				map[string]any{
					"crm_object_type":   map[string]any{"type": "string"},
					"crm_object_id":     map[string]any{"type": "string"},
					"from_date":         map[string]any{"type": "string", "description": "Inclusive YYYY-MM-DD call date."},
					"to_date":           map[string]any{"type": "string", "description": "Inclusive YYYY-MM-DD call date."},
					"lifecycle_bucket":  map[string]any{"type": "string"},
					"scope":             map[string]any{"type": "string"},
					"system":            map[string]any{"type": "string"},
					"direction":         map[string]any{"type": "string"},
					"transcript_status": map[string]any{"type": "string", "enum": []string{"", "present", "missing", "any"}},
					"limit":             map[string]any{"type": "integer", "minimum": 1, "maximum": policy.SearchResults},
				},
				nil,
			),
		},
		{
			Name:        "get_call",
			Description: "Return minimized cached call detail for one call_id or redacted call_ref without raw participant, CRM field value, or transcript payloads.",
			InputSchema: objectSchema(
				map[string]any{
					"call_id":  map[string]any{"type": "string"},
					"call_ref": map[string]any{"type": "string"},
				},
				nil,
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
					"limit":       map[string]any{"type": "integer", "minimum": 1, "maximum": policy.CRMFields},
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
					"limit":          map[string]any{"type": "integer", "minimum": 1, "maximum": policy.InventoryResults},
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
					"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": policy.InventoryResults},
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
					"limit":       map[string]any{"type": "integer", "minimum": 1, "maximum": policy.InventoryResults},
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
			Name:        "summarize_scorecard_activity",
			Description: "Summarize cached answered scorecard activity as aggregate counts and scores without call IDs, user IDs, scorecard IDs, answer text, call titles, transcript snippets, emails, or raw payloads.",
			InputSchema: objectSchema(
				map[string]any{
					"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": policy.CallFactGroups},
				},
				nil,
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
					"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": policy.InventoryResults},
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
					"limit":                  map[string]any{"type": "integer", "minimum": 1, "maximum": policy.SearchResults},
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
					"limit":                 map[string]any{"type": "integer", "minimum": 1, "maximum": policy.LateStageSignals},
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
					"limit":        map[string]any{"type": "integer", "minimum": 1, "maximum": policy.OpportunitySummaries},
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
					"limit":       map[string]any{"type": "integer", "minimum": 1, "maximum": policy.SearchResults},
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
					"limit":        map[string]any{"type": "integer", "minimum": 1, "maximum": policy.OpportunitySummaries},
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
					"limit":          map[string]any{"type": "integer", "minimum": 1, "maximum": policy.CRMMatrixCells},
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
					"limit":                    map[string]any{"type": "integer", "minimum": 1, "maximum": policy.LifecycleResults},
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
					"from_date":        map[string]any{"type": "string", "description": "Inclusive YYYY-MM-DD call date."},
					"to_date":          map[string]any{"type": "string", "description": "Inclusive YYYY-MM-DD call date."},
					"scope":            map[string]any{"type": "string"},
					"system":           map[string]any{"type": "string"},
					"direction":        map[string]any{"type": "string"},
					"limit":            map[string]any{"type": "integer", "minimum": 1, "maximum": policy.LifecycleResults},
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
					"limit":       map[string]any{"type": "integer", "minimum": 1, "maximum": policy.LifecycleCRMFields},
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
					"limit":             map[string]any{"type": "integer", "minimum": 1, "maximum": policy.CallFactGroups},
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
					"from_date":        map[string]any{"type": "string", "description": "Inclusive YYYY-MM-DD call date."},
					"to_date":          map[string]any{"type": "string", "description": "Inclusive YYYY-MM-DD call date."},
					"scope":            map[string]any{"type": "string"},
					"system":           map[string]any{"type": "string"},
					"direction":        map[string]any{"type": "string"},
					"limit":            map[string]any{"type": "integer", "minimum": 1, "maximum": policy.LifecycleResults},
					"lifecycle_source": map[string]any{"type": "string"},
				},
				nil,
			),
		},
		{
			Name:        "search_transcript_segments",
			Description: "Search transcript snippets in the configured local store. Call/speaker provenance is controlled by server transcript-evidence-provenance config: redacted by default, stable aliases in alias mode, raw IDs only in raw mode.",
			InputSchema: objectSchema(
				map[string]any{
					"query":               map[string]any{"type": "string"},
					"from_date":           map[string]any{"type": "string", "description": "Inclusive YYYY-MM-DD call date."},
					"to_date":             map[string]any{"type": "string", "description": "Inclusive YYYY-MM-DD call date."},
					"lifecycle_bucket":    map[string]any{"type": "string"},
					"scope":               map[string]any{"type": "string"},
					"system":              map[string]any{"type": "string"},
					"direction":           map[string]any{"type": "string"},
					"limit":               map[string]any{"type": "integer", "minimum": 1, "maximum": policy.SearchResults},
					"include_call_ids":    map[string]any{"type": "boolean"},
					"include_speaker_ids": map[string]any{"type": "boolean"},
				},
				[]string{"query"},
			),
		},
		{
			Name:        "search_transcripts_by_call_facts",
			Description: "Search transcript snippets joined to normalized call facts with date, lifecycle, scope, system, and direction filters. Returns bounded evidence excerpts; call/speaker provenance follows server transcript-evidence-provenance config.",
			InputSchema: objectSchema(
				map[string]any{
					"query":            map[string]any{"type": "string"},
					"from_date":        map[string]any{"type": "string", "description": "Inclusive YYYY-MM-DD call date."},
					"to_date":          map[string]any{"type": "string", "description": "Inclusive YYYY-MM-DD call date."},
					"lifecycle_bucket": map[string]any{"type": "string"},
					"scope":            map[string]any{"type": "string"},
					"system":           map[string]any{"type": "string"},
					"direction":        map[string]any{"type": "string"},
					"limit":            map[string]any{"type": "integer", "minimum": 1, "maximum": policy.SearchResults},
				},
				[]string{"query"},
			),
		},
		{
			Name:        "search_transcript_quotes_with_attribution",
			Description: "Search bounded transcript quote snippets and join available call, Account, Opportunity, industry, and attribution-readiness metadata; call titles are included by default where policy permits, while raw IDs and names require explicit opt-in flags.",
			InputSchema: objectSchema(
				map[string]any{
					"query":                     map[string]any{"type": "string"},
					"from_date":                 map[string]any{"type": "string", "description": "Inclusive YYYY-MM-DD call date."},
					"to_date":                   map[string]any{"type": "string", "description": "Inclusive YYYY-MM-DD call date."},
					"lifecycle_bucket":          map[string]any{"type": "string"},
					"scope":                     map[string]any{"type": "string"},
					"system":                    map[string]any{"type": "string"},
					"direction":                 map[string]any{"type": "string"},
					"transcript_status":         map[string]any{"type": "string", "enum": []string{"", "present", "any"}},
					"industry":                  map[string]any{"type": "string"},
					"account_query":             map[string]any{"type": "string"},
					"opportunity_stage":         map[string]any{"type": "string"},
					"limit":                     map[string]any{"type": "integer", "minimum": 1, "maximum": policy.SearchResults},
					"field_profile":             fieldProfileSchema(),
					"speaker_role":              speakerRoleSchema(),
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
			Description: "List cached calls that do not yet have transcript segments stored in the configured local store.",
			InputSchema: objectSchema(
				map[string]any{
					"from_date":        map[string]any{"type": "string", "description": "Inclusive YYYY-MM-DD call date."},
					"to_date":          map[string]any{"type": "string", "description": "Inclusive YYYY-MM-DD call date."},
					"lifecycle_bucket": map[string]any{"type": "string"},
					"scope":            map[string]any{"type": "string"},
					"system":           map[string]any{"type": "string"},
					"direction":        map[string]any{"type": "string"},
					"crm_object_type":  map[string]any{"type": "string"},
					"crm_object_id":    map[string]any{"type": "string"},
					"limit":            map[string]any{"type": "integer", "minimum": 1, "maximum": policy.MissingTranscripts},
				},
				nil,
			),
		},
	}
	tools = append(tools, businessAnalysisTools(policy)...)
	tools = append(tools, facadeTools(policy)...)
	return tools
}

func ToolCatalog() []ToolInfo {
	return ToolCatalogWithLimitPolicy(DefaultLimitPolicy())
}

func ToolCatalogWithLimitPolicy(policy LimitPolicy) []ToolInfo {
	server := NewServerWithOptions(nil, "gongmcp", "dev", WithLimitPolicy(policy))
	out := make([]ToolInfo, 0, len(server.tools))
	for _, item := range server.tools {
		out = append(out, ToolInfo(item))
	}
	return out
}

func FindTool(name string) (ToolInfo, bool) {
	return FindToolWithLimitPolicy(name, DefaultLimitPolicy())
}

func FindToolWithLimitPolicy(name string, policy LimitPolicy) (ToolInfo, bool) {
	for _, tool := range ToolCatalogWithLimitPolicy(policy) {
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

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/mcp" {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodGet {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "streaming GET is not implemented by this MCP server", http.StatusMethodNotAllowed)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	payload, err := readHTTPPayload(r.Body)
	if err != nil {
		if errors.Is(err, errHTTPPayloadTooLarge) {
			http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "read request body", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), httpToolCallTimeout)
	defer cancel()

	resp := s.handlePayload(ctx, payload)
	if resp == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	body, err := json.Marshal(resp)
	if err != nil {
		http.Error(w, "encode response", http.StatusInternalServerError)
		return
	}
	if len(body) > maxFrameBytes {
		http.Error(w, fmt.Sprintf("response frame exceeds maximum %d bytes", maxFrameBytes), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
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

func readHTTPPayload(r io.Reader) ([]byte, error) {
	payload, err := io.ReadAll(io.LimitReader(r, maxFrameBytes+1))
	if err != nil {
		return nil, err
	}
	if len(payload) > maxFrameBytes {
		return nil, errHTTPPayloadTooLarge
	}
	return payload, nil
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
	if s.governanceCheck != nil {
		if err := s.governanceCheck(ctx); err != nil {
			return toolCallResult{}, err
		}
	}
	if isFacadeTool(params.Name) {
		return s.executeFacadeTool(ctx, params)
	}
	if isBusinessAnalysisTool(params.Name) {
		return s.executeBusinessAnalysisTool(ctx, params)
	}
	return s.executeNonFacadeTool(ctx, params)
}

// executeNonFacadeTool dispatches the original top-level tool catalog. The
// facade module also calls into this helper so a facade-routed call exercises
// the same handlers as a direct tools/call against the routed tool.
func (s *Server) executeNonFacadeTool(ctx context.Context, params toolsCallParams) (toolCallResult, error) {
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
	case "summarize_scorecard_activity":
		return s.summarizeScorecardActivity(ctx, params.Arguments)
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
	return newToolResult(s.mcpSyncStatus(summary))
}

func (s *Server) searchCalls(ctx context.Context, raw json.RawMessage) (toolCallResult, error) {
	var args searchCallsArgs
	if err := decodeArgs(raw, &args); err != nil {
		return toolCallResult{}, err
	}

	limit := s.limitPolicy.SearchLimit(args.Limit)
	queryLimit := s.governedQueryLimit(limit, s.limitPolicy.Normalize().SearchResults)
	rows, err := s.store.SearchCallsRaw(ctx, sqlite.CallSearchParams{
		CRMObjectType:    args.CRMObjectType,
		CRMObjectID:      args.CRMObjectID,
		FromDate:         args.FromDate,
		ToDate:           args.ToDate,
		LifecycleBucket:  args.LifecycleBucket,
		Scope:            args.Scope,
		System:           args.System,
		Direction:        args.Direction,
		TranscriptStatus: args.TranscriptStatus,
		Limit:            queryLimit,
	})
	if err != nil {
		return toolCallResult{}, err
	}

	summaries := make([]searchCallSummary, 0, len(rows))
	filtered := 0
	for _, rawCall := range rows {
		summary, err := summarizeCall(rawCall)
		if err != nil {
			return toolCallResult{}, err
		}
		if s.isSuppressedCall(summary.CallID) {
			filtered++
			continue
		}
		if s.blocklistMatchesCallSummary(summary) {
			filtered++
			continue
		}
		s.applyPolicySwitchesToSearchSummary(&summary)
		summaries = append(summaries, summary)
		if len(summaries) >= limit {
			break
		}
	}
	return s.newCappedToolResult("search_calls", summaries, len(summaries), limit, filtered, searchCallRefinements(args))
}

func (s *Server) getCall(ctx context.Context, raw json.RawMessage) (toolCallResult, error) {
	var args getCallArgs
	if err := decodeArgs(raw, &args); err != nil {
		return toolCallResult{}, err
	}
	callID := strings.TrimSpace(args.CallID)
	callRef := strings.TrimSpace(args.CallRef)
	if callID != "" && callRef != "" {
		return toolCallResult{}, fmt.Errorf("provide either call_id or call_ref, not both")
	}
	resolvedFromRef := false
	if callRef != "" {
		resolved, err := s.store.ResolveCallIDByRef(ctx, callRef)
		if err != nil {
			return toolCallResult{}, fmt.Errorf("call not found")
		}
		callID = resolved
		resolvedFromRef = true
	}
	if s.isSuppressedCall(callID) {
		return toolCallResult{}, fmt.Errorf("call not found")
	}

	detail, err := s.store.GetCallDetail(ctx, callID)
	if err != nil {
		return toolCallResult{}, err
	}
	// Inspect the un-minimized detail so the blocklist guard sees CRM object
	// names before they are cleared by the minimizer. This is the
	// defense-in-depth layer and must run on the richest representation
	// available.
	if s.blocklistMatchesCallDetail(detail) {
		return toolCallResult{}, fmt.Errorf("call not found")
	}
	minimizeCallDetail(detail)
	if resolvedFromRef {
		detail.CallRef, _ = sqlite.NormalizeStableCallRef(callRef)
		detail.CallID = ""
	}
	s.applyPolicySwitchesToCallDetail(detail)
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

	rows, err := s.store.ListCRMFields(ctx, args.ObjectType, capLimit(args.Limit, defaultCRMFieldRequestLimit, s.limitPolicy.Normalize().CRMFields))
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
		Limit:         capLimit(args.Limit, defaultInventoryRequestLimit, s.limitPolicy.Normalize().InventoryResults),
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
		Limit: capLimit(args.Limit, defaultInventoryRequestLimit, s.limitPolicy.Normalize().InventoryResults),
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
		Limit:      capLimit(args.Limit, defaultInventoryRequestLimit, s.limitPolicy.Normalize().InventoryResults),
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

func (s *Server) summarizeScorecardActivity(ctx context.Context, raw json.RawMessage) (toolCallResult, error) {
	var args summarizeScorecardActivityArgs
	if err := decodeArgs(raw, &args); err != nil {
		return toolCallResult{}, err
	}

	report, err := s.store.ScorecardActivityOverview(ctx, capLimit(args.Limit, defaultCallFactRequestLimit, s.limitPolicy.Normalize().CallFactGroups))
	if err != nil {
		return toolCallResult{}, err
	}
	return newToolResult(report)
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
	rows, err := s.store.ListUnmappedCRMFields(ctx, sqlite.UnmappedCRMFieldParams{Limit: capLimit(args.Limit, defaultInventoryRequestLimit, s.limitPolicy.Normalize().InventoryResults)})
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
		Limit:               capLimit(args.Limit, defaultCRMFieldValueRequestLimit, s.limitPolicy.Normalize().SearchResults),
		IncludeValueSnippet: args.IncludeValueSnippets,
		IncludeCallIDs:      args.IncludeCallIDs,
	})
	if err != nil {
		return toolCallResult{}, err
	}
	filtered := 0
	out := rows[:0]
	for _, row := range rows {
		if s.isSuppressedCall(row.CallID) {
			filtered++
			continue
		}
		if !args.IncludeCallIDs {
			row.CallID = ""
		}
		row.ObjectID = ""
		row.ObjectName = ""
		if !args.IncludeValueSnippets {
			row.Title = ""
		}
		out = append(out, row)
	}
	return s.newCappedToolResult("search_crm_field_values", out, len(out), capLimit(args.Limit, defaultCRMFieldValueRequestLimit, s.limitPolicy.Normalize().SearchResults), filtered, crmFieldValueRefinements(args))
}

func (s *Server) analyzeLateStageCRMSignals(ctx context.Context, raw json.RawMessage) (toolCallResult, error) {
	if s.governanceActive() {
		return toolCallResult{}, governanceFilteredAggregateError("analyze_late_stage_crm_signals")
	}
	var args analyzeLateStageCRMSignalsArgs
	if err := decodeArgs(raw, &args); err != nil {
		return toolCallResult{}, err
	}

	report, err := s.store.AnalyzeLateStageSignals(ctx, sqlite.LateStageSignalParams{
		ObjectType:          args.ObjectType,
		StageField:          args.StageField,
		LateStageValues:     args.LateStageValues,
		IncludeStageProxies: args.IncludeStageProxies,
		Limit:               capLimit(args.Limit, defaultLateStageSignalRequestLimit, s.limitPolicy.Normalize().LateStageSignals),
	})
	if err != nil {
		return toolCallResult{}, err
	}
	return newToolResult(report)
}

func (s *Server) opportunitiesMissingTranscripts(ctx context.Context, raw json.RawMessage) (toolCallResult, error) {
	if s.governanceActive() {
		return toolCallResult{}, governanceFilteredAggregateError("opportunities_missing_transcripts")
	}
	var args opportunitiesMissingTranscriptsArgs
	if err := decodeArgs(raw, &args); err != nil {
		return toolCallResult{}, err
	}

	rows, err := s.store.ListOpportunitiesMissingTranscripts(ctx, sqlite.OpportunityMissingTranscriptParams{
		StageValues: args.StageValues,
		Limit:       capLimit(args.Limit, defaultOpportunitySummaryRequestLimit, s.limitPolicy.Normalize().OpportunitySummaries),
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

	limit := s.limitPolicy.SearchLimit(args.Limit)
	queryLimit := s.governedQueryLimit(limit, s.limitPolicy.Normalize().SearchResults)
	rows, err := s.store.SearchTranscriptSegmentsByCRMContext(ctx, sqlite.TranscriptCRMSearchParams{
		Query:      args.Query,
		ObjectType: args.ObjectType,
		ObjectID:   args.ObjectID,
		Limit:      queryLimit,
	})
	if err != nil {
		return toolCallResult{}, err
	}
	filtered := 0
	out := rows[:0]
	for _, row := range rows {
		if s.isSuppressedCall(row.CallID) {
			filtered++
			continue
		}
		out = append(out, row)
	}
	results := mcpTranscriptCRMSearchResults(out)
	return s.newCappedToolResult("search_transcripts_by_crm_context", results, len(results), limit, filtered, crmTranscriptRefinements(args))
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

func mcpLifecycleBucketSummaries(rows []sqlite.LifecycleBucketSummary) []sqlite.LifecycleBucketSummary {
	out := make([]sqlite.LifecycleBucketSummary, len(rows))
	for i, row := range rows {
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
	if s.governanceActive() {
		return toolCallResult{}, governanceFilteredAggregateError("opportunity_call_summary")
	}
	var args opportunityCallSummaryArgs
	if err := decodeArgs(raw, &args); err != nil {
		return toolCallResult{}, err
	}

	rows, err := s.store.SummarizeOpportunityCalls(ctx, sqlite.OpportunityCallSummaryParams{
		StageValues: args.StageValues,
		Limit:       capLimit(args.Limit, defaultOpportunitySummaryRequestLimit, s.limitPolicy.Normalize().OpportunitySummaries),
	})
	if err != nil {
		return toolCallResult{}, err
	}
	return newToolResult(mcpOpportunityCallSummaries(rows))
}

func (s *Server) crmFieldPopulationMatrix(ctx context.Context, raw json.RawMessage) (toolCallResult, error) {
	if s.governanceActive() {
		return toolCallResult{}, governanceFilteredAggregateError("crm_field_population_matrix")
	}
	var args crmFieldPopulationMatrixArgs
	if err := decodeArgs(raw, &args); err != nil {
		return toolCallResult{}, err
	}

	report, err := s.store.CRMFieldPopulationMatrix(ctx, sqlite.CRMFieldPopulationMatrixParams{
		ObjectType:   args.ObjectType,
		GroupByField: args.GroupByField,
		Limit:        capLimit(args.Limit, defaultCRMMatrixRequestLimit, s.limitPolicy.Normalize().CRMMatrixCells),
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
	if s.governanceActive() {
		return toolCallResult{}, governanceFilteredAggregateError("summarize_calls_by_lifecycle")
	}
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
	return newLifecycleToolResult(mcpLifecycleBucketSummaries(rows), info, args.LifecycleSource)
}

func (s *Server) searchCallsByLifecycle(ctx context.Context, raw json.RawMessage) (toolCallResult, error) {
	var args searchCallsByLifecycleArgs
	if err := decodeArgs(raw, &args); err != nil {
		return toolCallResult{}, err
	}

	rows, info, err := s.store.SearchCallsByLifecycleWithSource(ctx, sqlite.LifecycleCallSearchParams{
		Bucket:                 args.Bucket,
		MissingTranscriptsOnly: args.MissingTranscriptsOnly,
		Limit:                  s.limitPolicy.LifecycleLimit(args.Limit),
		LifecycleSource:        args.LifecycleSource,
	})
	if err != nil {
		return toolCallResult{}, err
	}
	filtered := 0
	out := rows[:0]
	for _, row := range rows {
		if s.isSuppressedCall(row.CallID) {
			filtered++
			continue
		}
		out = append(out, row)
	}
	return s.newGovernedLifecycleToolResult(out, info, args.LifecycleSource, filtered)
}

func (s *Server) prioritizeTranscriptsByLifecycle(ctx context.Context, raw json.RawMessage) (toolCallResult, error) {
	var args prioritizeTranscriptsByLifecycleArgs
	if err := decodeArgs(raw, &args); err != nil {
		return toolCallResult{}, err
	}

	limit := s.limitPolicy.LifecycleLimit(args.Limit)
	queryLimit := s.governedQueryLimit(limit, s.limitPolicy.Normalize().LifecycleResults)
	rows, info, err := s.store.PrioritizeTranscriptsByLifecycleWithSource(ctx, sqlite.LifecycleTranscriptPriorityParams{
		Bucket:          args.Bucket,
		FromDate:        args.FromDate,
		ToDate:          args.ToDate,
		Scope:           args.Scope,
		System:          args.System,
		Direction:       args.Direction,
		Limit:           queryLimit,
		LifecycleSource: args.LifecycleSource,
	})
	if err != nil {
		return toolCallResult{}, err
	}
	filtered := 0
	out := rows[:0]
	for _, row := range rows {
		if s.isSuppressedCall(row.CallID) {
			filtered++
			continue
		}
		out = append(out, row)
		if len(out) >= limit {
			break
		}
	}
	results := mcpTranscriptPriorities(out)
	if shouldReturnCapEnvelope(len(results), limit) {
		envelope := cappedToolPayload("prioritize_transcripts_by_lifecycle", results, len(results), limit, transcriptBacklogRefinements(args))
		if info != nil && (info.Profile != nil || strings.TrimSpace(args.LifecycleSource) != "") {
			envelope["lifecycle_source"] = info.LifecycleSource
			envelope["profile"] = mcpBusinessProfile(info.Profile)
			envelope["unavailable_concepts"] = info.UnavailableConcepts
		}
		return newToolResult(envelope)
	}
	return s.newGovernedLifecycleToolResult(results, info, args.LifecycleSource, filtered)
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

func (s *Server) mcpSyncStatus(summary *sqlite.SyncStatusSummary) publicSyncStatus {
	profile := summary.ProfileReadiness
	profile.Name = ""
	profile.CanonicalSHA256 = ""
	return publicSyncStatus{
		MCPServer:                    s.publicRuntimeInfo(),
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
		ProfileReadiness:             profile,
		PublicReadiness:              summary.PublicReadiness,
		AttributionCoverage:          summary.AttributionCoverage,
	}
}

func (s *Server) compareLifecycleCRMFields(ctx context.Context, raw json.RawMessage) (toolCallResult, error) {
	if s.governanceActive() {
		return toolCallResult{}, governanceFilteredAggregateError("compare_lifecycle_crm_fields")
	}
	var args compareLifecycleCRMFieldsArgs
	if err := decodeArgs(raw, &args); err != nil {
		return toolCallResult{}, err
	}

	report, err := s.store.CompareLifecycleCRMFields(ctx, sqlite.LifecycleCRMFieldComparisonParams{
		BucketA:    args.BucketA,
		BucketB:    args.BucketB,
		ObjectType: args.ObjectType,
		Limit:      capLimit(args.Limit, defaultLifecycleCRMFieldRequestLimit, s.limitPolicy.Normalize().LifecycleCRMFields),
	})
	if err != nil {
		return toolCallResult{}, err
	}
	return newToolResult(report)
}

func (s *Server) summarizeCallFacts(ctx context.Context, raw json.RawMessage) (toolCallResult, error) {
	if s.governanceActive() {
		return toolCallResult{}, governanceFilteredAggregateError("summarize_call_facts")
	}
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
		Limit:            capLimit(args.Limit, defaultCallFactRequestLimit, s.limitPolicy.Normalize().CallFactGroups),
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
		dim := strings.ToLower(strings.TrimSpace(groupBy))
		if field, ok := crmdimensions.LookupPromotedField(dim); ok && (field.Kind == crmdimensions.KindCategorical || field.Kind == crmdimensions.KindBoolean) {
			return nil
		}
		for _, bucket := range crmdimensions.BucketDimensions {
			if bucket.Dimension == dim {
				return nil
			}
		}
		if crmdimensions.IsExcludedFilterDimension(dim) {
			return fmt.Errorf("unsupported MCP group_by %q; excluded CRM field", groupBy)
		}
		return fmt.Errorf("unsupported MCP group_by %q; use list_business_concepts for field discovery and search_crm_field_values with explicit opt-in for value lookups", groupBy)
	}
}

func (s *Server) rankTranscriptBacklog(ctx context.Context, raw json.RawMessage) (toolCallResult, error) {
	var args prioritizeTranscriptsByLifecycleArgs
	if err := decodeArgs(raw, &args); err != nil {
		return toolCallResult{}, err
	}

	limit := s.limitPolicy.LifecycleLimit(args.Limit)
	queryLimit := s.governedQueryLimit(limit, s.limitPolicy.Normalize().LifecycleResults)
	rows, info, err := s.store.PrioritizeTranscriptsByLifecycleWithSource(ctx, sqlite.LifecycleTranscriptPriorityParams{
		Bucket:          args.Bucket,
		FromDate:        args.FromDate,
		ToDate:          args.ToDate,
		Scope:           args.Scope,
		System:          args.System,
		Direction:       args.Direction,
		Limit:           queryLimit,
		LifecycleSource: args.LifecycleSource,
	})
	if err != nil {
		return toolCallResult{}, err
	}
	filtered := 0
	out := rows[:0]
	for _, row := range rows {
		if s.isSuppressedCall(row.CallID) {
			filtered++
			continue
		}
		out = append(out, row)
		if len(out) >= limit {
			break
		}
	}
	results := mcpTranscriptPriorities(out)
	if shouldReturnCapEnvelope(len(results), limit) {
		envelope := cappedToolPayload("rank_transcript_backlog", results, len(results), limit, transcriptBacklogRefinements(args))
		if info != nil && (info.Profile != nil || strings.TrimSpace(args.LifecycleSource) != "") {
			envelope["lifecycle_source"] = info.LifecycleSource
			envelope["profile"] = mcpBusinessProfile(info.Profile)
			envelope["unavailable_concepts"] = info.UnavailableConcepts
		}
		return newToolResult(envelope)
	}
	return s.newGovernedLifecycleToolResult(results, info, args.LifecycleSource, filtered)
}

func (s *Server) searchTranscriptSegments(ctx context.Context, raw json.RawMessage) (toolCallResult, error) {
	var args searchTranscriptSegmentsArgs
	if err := decodeArgs(raw, &args); err != nil {
		return toolCallResult{}, err
	}

	limit := s.limitPolicy.SearchLimit(args.Limit)
	queryLimit := s.governedQueryLimit(limit, s.limitPolicy.Normalize().SearchResults)
	var snippets []transcriptSnippet
	filtered := 0
	if searchTranscriptSegmentsUsesCallFactFilters(args) {
		results, err := s.store.SearchTranscriptSegmentsByCallFacts(ctx, sqlite.TranscriptCallFactsSearchParams{
			Query:           args.Query,
			FromDate:        args.FromDate,
			ToDate:          args.ToDate,
			LifecycleBucket: args.LifecycleBucket,
			Scope:           args.Scope,
			System:          args.System,
			Direction:       args.Direction,
			Limit:           queryLimit,
		})
		if err != nil {
			return toolCallResult{}, err
		}
		snippets = make([]transcriptSnippet, 0, len(results))
		for _, row := range results {
			if s.isSuppressedCall(row.CallID) {
				filtered++
				continue
			}
			callID, callRef, speakerID, speakerRef := s.transcriptEvidenceIdentity(row.CallID, row.SpeakerID)
			if !args.IncludeCallIDs && s.transcriptEvidenceProvenance == TranscriptEvidenceRaw {
				callID = ""
			}
			if !args.IncludeSpeakerIDs && s.transcriptEvidenceProvenance == TranscriptEvidenceRaw {
				speakerID = ""
			}
			snippets = append(snippets, transcriptSnippet{
				CallID:       callID,
				CallRef:      callRef,
				SpeakerID:    speakerID,
				SpeakerRef:   speakerRef,
				SegmentIndex: row.SegmentIndex,
				StartMS:      row.StartMS,
				EndMS:        row.EndMS,
				Snippet:      row.Snippet,
			})
			if len(snippets) >= limit {
				break
			}
		}
	} else {
		results, err := s.store.SearchTranscriptSegments(ctx, args.Query, queryLimit)
		if err != nil {
			return toolCallResult{}, err
		}
		snippets = make([]transcriptSnippet, 0, len(results))
		for _, row := range results {
			if s.isSuppressedCall(row.CallID) {
				filtered++
				continue
			}
			callID, callRef, speakerID, speakerRef := s.transcriptEvidenceIdentity(row.CallID, row.SpeakerID)
			if !args.IncludeCallIDs && s.transcriptEvidenceProvenance == TranscriptEvidenceRaw {
				callID = ""
			}
			if !args.IncludeSpeakerIDs && s.transcriptEvidenceProvenance == TranscriptEvidenceRaw {
				speakerID = ""
			}
			snippets = append(snippets, transcriptSnippet{
				CallID:       callID,
				CallRef:      callRef,
				SpeakerID:    speakerID,
				SpeakerRef:   speakerRef,
				SegmentIndex: row.SegmentIndex,
				StartMS:      row.StartMS,
				EndMS:        row.EndMS,
				Snippet:      row.Snippet,
			})
			if len(snippets) >= limit {
				break
			}
		}
	}
	return s.newCappedToolResult("search_transcript_segments", snippets, len(snippets), limit, filtered, transcriptSearchRefinements(args))
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
		Limit:           s.limitPolicy.SearchLimit(args.Limit),
	})
	if err != nil {
		return toolCallResult{}, err
	}
	filtered := 0
	out := make([]transcriptCallFactsSnippet, 0, len(results))
	for _, row := range results {
		if s.isSuppressedCall(row.CallID) {
			filtered++
			continue
		}
		callID, callRef, speakerID, speakerRef := s.transcriptEvidenceIdentity(row.CallID, row.SpeakerID)
		out = append(out, transcriptCallFactsSnippet{
			CallID:          callID,
			CallRef:         callRef,
			SpeakerID:       speakerID,
			SpeakerRef:      speakerRef,
			StartedAt:       row.StartedAt,
			CallDate:        row.CallDate,
			CallMonth:       row.CallMonth,
			DurationSeconds: row.DurationSeconds,
			LifecycleBucket: row.LifecycleBucket,
			Scope:           row.Scope,
			System:          row.System,
			Direction:       row.Direction,
			SegmentIndex:    row.SegmentIndex,
			StartMS:         row.StartMS,
			EndMS:           row.EndMS,
			Snippet:         row.Snippet,
			ContextExcerpt:  row.ContextExcerpt,
		})
	}
	return s.newCappedToolResult("search_transcripts_by_call_facts", out, len(out), s.limitPolicy.SearchLimit(args.Limit), filtered, callFactTranscriptRefinements(args))
}

func (s *Server) transcriptEvidenceIdentity(callID, speakerID string) (string, string, string, string) {
	switch s.transcriptEvidenceProvenance {
	case TranscriptEvidenceRaw:
		return callID, "", speakerID, ""
	case TranscriptEvidenceAlias:
		callRef := stableEvidenceRef("call", callID)
		speakerRef := stableEvidenceRef("speaker", callID+"\x00"+speakerID)
		return "", callRef, "", speakerRef
	default:
		return "", "", "", ""
	}
}

func stableEvidenceRef(prefix, value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return prefix + "_" + hex.EncodeToString(sum[:])[:12]
}

func (s *Server) searchTranscriptQuotesWithAttribution(ctx context.Context, raw json.RawMessage) (toolCallResult, error) {
	var args searchTranscriptQuotesWithAttributionArgs
	if err := decodeArgs(raw, &args); err != nil {
		return toolCallResult{}, err
	}
	if s.restrictedAccountQuery(args.AccountQuery) {
		return toolCallResult{}, restrictedAccountQueryError()
	}
	profiled, err := applyFieldProfile(args.FieldProfile, fieldProfileApplication{
		IncludeRawIDs:           args.IncludeCallIDs,
		IncludeCallTitles:       true,
		IncludeAccountNames:     args.IncludeAccountNames,
		IncludeOpportunityNames: args.IncludeOpportunityNames,
	})
	if err != nil {
		return toolCallResult{}, err
	}
	args.FieldProfile = profiled.Profile
	args.IncludeCallIDs = profiled.IncludeRawIDs
	args.IncludeCallTitles = profiled.IncludeCallTitles
	args.IncludeAccountNames = profiled.IncludeAccountNames
	args.IncludeOpportunityNames = profiled.IncludeOpportunityNames
	if strings.TrimSpace(args.AccountQuery) != "" && !args.IncludeAccountNames {
		return toolCallResult{}, errors.New("account_query requires include_account_names=true because it can probe customer names")
	}
	speakerRoleFilter, err := normalizeSpeakerRoleFilter(args.SpeakerRole)
	if err != nil {
		return toolCallResult{}, err
	}
	args.SpeakerRole = speakerRoleFilter
	if strings.EqualFold(strings.TrimSpace(args.TranscriptStatus), "missing") {
		return toolCallResult{}, errors.New("transcript_status=missing is not valid for quote search because quote tools require cached transcript segments")
	}

	results, err := s.store.SearchTranscriptQuotesWithAttribution(ctx, sqlite.TranscriptAttributionSearchParams{
		Query:            args.Query,
		FromDate:         args.FromDate,
		ToDate:           args.ToDate,
		LifecycleBucket:  args.LifecycleBucket,
		Scope:            args.Scope,
		System:           args.System,
		Direction:        args.Direction,
		TranscriptStatus: args.TranscriptStatus,
		Industry:         args.Industry,
		AccountQuery:     args.AccountQuery,
		OpportunityStage: args.OpportunityStage,
		Limit:            s.limitPolicy.SearchLimit(args.Limit),
	})
	if err != nil {
		return toolCallResult{}, err
	}
	filtered := 0
	out := results[:0]
	for _, row := range results {
		if !businessAnalysisSpeakerRoleAllowed(row.SpeakerRole, args.SpeakerRole) {
			continue
		}
		if s.isSuppressedCall(row.CallID) {
			filtered++
			continue
		}
		out = append(out, row)
	}
	attributed := mcpTranscriptAttributionResults(out, args)
	for idx := range attributed {
		if s.policySwitches.HideCallTitles {
			attributed[idx].Title = ""
		}
		if s.policySwitches.HideRawCallIDs {
			attributed[idx].CallID = ""
		}
		if s.policySwitches.HideAccountNames {
			attributed[idx].AccountName = ""
			attributed[idx].AccountWebsite = ""
		}
		if s.policySwitches.HideOpportunityNames {
			attributed[idx].OpportunityName = ""
			attributed[idx].OpportunityCloseDate = ""
			attributed[idx].OpportunityProbability = ""
		}
	}
	return s.newCappedToolResult("search_transcript_quotes_with_attribution", attributed, len(attributed), s.limitPolicy.SearchLimit(args.Limit), filtered, attributionRefinements(args))
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

	limit := s.limitPolicy.MissingTranscriptLimit(args.Limit)
	queryLimit := s.governedQueryLimit(limit, s.limitPolicy.Normalize().MissingTranscripts)
	if strings.TrimSpace(args.CRMObjectID) != "" && strings.TrimSpace(args.CRMObjectType) == "" {
		return toolCallResult{}, errors.New("crm_object_type is required when crm_object_id is set")
	}
	rows, err := s.store.FindCallsMissingTranscriptsByFilters(ctx, sqlite.MissingTranscriptSearchParams{
		FromDate:        args.FromDate,
		ToDate:          args.ToDate,
		LifecycleBucket: args.LifecycleBucket,
		Scope:           args.Scope,
		System:          args.System,
		Direction:       args.Direction,
		CRMObjectType:   args.CRMObjectType,
		CRMObjectID:     args.CRMObjectID,
		Limit:           queryLimit,
	})
	if err != nil {
		return toolCallResult{}, err
	}
	filtered := 0
	out := rows[:0]
	for _, row := range rows {
		if s.isSuppressedCall(row.CallID) {
			filtered++
			continue
		}
		out = append(out, row)
		if len(out) >= limit {
			break
		}
	}
	return s.newCappedToolResult("missing_transcripts", out, len(out), limit, filtered, missingTranscriptRefinements(args))
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

	payload, err := stripMCPMeta(payload)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	return nil
}

func stripMCPMeta(payload []byte) ([]byte, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		return nil, err
	}
	if _, ok := fields["_meta"]; !ok {
		return payload, nil
	}
	delete(fields, "_meta")
	return json.Marshal(fields)
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

func (s *Server) newGovernedToolResult(value any, _ int) (toolCallResult, error) {
	return newToolResult(value)
}

func (s *Server) newCappedToolResult(toolName string, value any, returned int, limit int, filtered int, refinements []string) (toolCallResult, error) {
	if !shouldReturnCapEnvelope(returned, limit) {
		return s.newGovernedToolResult(value, filtered)
	}
	return newToolResult(cappedToolPayload(toolName, value, returned, limit, refinements))
}

func shouldReturnCapEnvelope(returned int, limit int) bool {
	return limit > 0 && returned >= limit
}

func cappedToolPayload(toolName string, results any, returned int, limit int, refinements []string) map[string]any {
	if len(refinements) == 0 {
		refinements = []string{"add a narrower date, lifecycle, scope, system, direction, CRM, or text filter before increasing the cap"}
	}
	return map[string]any{
		"results":               results,
		"returned":              returned,
		"limit":                 limit,
		"capped":                true,
		"tool":                  toolName,
		"suggested_refinements": refinements,
	}
}

func searchTranscriptSegmentsUsesCallFactFilters(args searchTranscriptSegmentsArgs) bool {
	return firstNonBlank(args.FromDate, args.ToDate, args.LifecycleBucket, args.Scope, args.System, args.Direction) != ""
}

func searchCallRefinements(args searchCallsArgs) []string {
	var out []string
	if args.FromDate == "" || args.ToDate == "" {
		out = append(out, "add from_date and to_date to bound the call date range")
	}
	if args.LifecycleBucket == "" {
		out = append(out, "add lifecycle_bucket when investigating a specific sales or customer-success motion")
	}
	if args.Scope == "" || args.System == "" || args.Direction == "" {
		out = append(out, "add scope, system, or direction to narrow the normalized call facts")
	}
	if args.CRMObjectType == "" && args.CRMObjectID == "" {
		out = append(out, "add crm_object_type or crm_object_id when following a known Account, Opportunity, Contact, or Lead")
	}
	return out
}

func crmFieldValueRefinements(args searchCRMFieldValuesArgs) []string {
	var out []string
	if strings.TrimSpace(args.ValueQuery) == "" || len(strings.TrimSpace(args.ValueQuery)) < 3 {
		out = append(out, "use a more specific value_query for the CRM value search")
	} else {
		out = append(out, "make value_query more specific before increasing the cap")
	}
	if strings.TrimSpace(args.ObjectType) == "" {
		out = append(out, "set object_type to the CRM object you want to inspect")
	}
	if strings.TrimSpace(args.FieldName) == "" {
		out = append(out, "set field_name to a single CRM field")
	}
	return out
}

func crmTranscriptRefinements(args searchTranscriptsByCRMContextArgs) []string {
	var out []string
	if strings.TrimSpace(args.Query) == "" || len(strings.TrimSpace(args.Query)) < 3 {
		out = append(out, "use a more specific transcript query")
	} else {
		out = append(out, "make query more specific before increasing the cap")
	}
	if strings.TrimSpace(args.ObjectID) == "" {
		out = append(out, "add object_id when following one known CRM record")
	}
	return out
}

func transcriptSearchRefinements(args searchTranscriptSegmentsArgs) []string {
	return transcriptFilterRefinements(args.FromDate, args.ToDate, args.LifecycleBucket, args.Scope, args.System, args.Direction)
}

func callFactTranscriptRefinements(args searchTranscriptsByCallFactsArgs) []string {
	return transcriptFilterRefinements(args.FromDate, args.ToDate, args.LifecycleBucket, args.Scope, args.System, args.Direction)
}

func attributionRefinements(args searchTranscriptQuotesWithAttributionArgs) []string {
	out := transcriptFilterRefinements(args.FromDate, args.ToDate, args.LifecycleBucket, args.Scope, args.System, args.Direction)
	if args.Industry == "" {
		out = append(out, "add industry when preparing segment-specific quote evidence")
	}
	if args.OpportunityStage == "" {
		out = append(out, "add opportunity_stage when comparing pipeline or outcome language")
	}
	return out
}

func missingTranscriptRefinements(args missingTranscriptsArgs) []string {
	out := transcriptFilterRefinements(args.FromDate, args.ToDate, args.LifecycleBucket, args.Scope, args.System, args.Direction)
	if args.CRMObjectType == "" && args.CRMObjectID == "" {
		out = append(out, "add crm_object_type or crm_object_id to inspect a specific CRM slice")
	}
	return out
}

func transcriptBacklogRefinements(args prioritizeTranscriptsByLifecycleArgs) []string {
	out := transcriptFilterRefinements(args.FromDate, args.ToDate, args.Bucket, args.Scope, args.System, args.Direction)
	if args.Bucket == "" {
		out = append(out, "add bucket to focus transcript sync priority on one lifecycle motion")
	}
	return out
}

func transcriptFilterRefinements(fromDate, toDate, lifecycleBucket, scope, system, direction string) []string {
	var out []string
	if fromDate == "" || toDate == "" {
		out = append(out, "add from_date and to_date to bound the call date range")
	}
	if lifecycleBucket == "" {
		out = append(out, "add lifecycle_bucket when the analysis is tied to a known business motion")
	}
	if scope == "" {
		out = append(out, "add scope=External, Internal, or Unknown")
	}
	if system == "" {
		out = append(out, "add system when the dataset spans multiple meeting systems")
	}
	if direction == "" {
		out = append(out, "add direction when inbound, outbound, or conference style matters")
	}
	return out
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

func (s *Server) newGovernedLifecycleToolResult(results any, info *sqlite.ProfileQueryInfo, requested string, _ int) (toolCallResult, error) {
	if len(s.suppressedCallIDs) == 0 {
		return newLifecycleToolResult(results, info, requested)
	}
	envelope := map[string]any{
		"results": results,
	}
	if info != nil && (info.Profile != nil || strings.TrimSpace(requested) != "") {
		envelope["lifecycle_source"] = info.LifecycleSource
		envelope["profile"] = mcpBusinessProfile(info.Profile)
		envelope["unavailable_concepts"] = info.UnavailableConcepts
	}
	return newToolResult(envelope)
}

func (s *Server) governedQueryLimit(requested int, max int) int {
	if len(s.suppressedCallIDs) == 0 {
		return requested
	}
	if max <= 0 {
		return requested
	}
	return max
}

func (s *Server) isSuppressedCall(callID string) bool {
	if len(s.suppressedCallIDs) == 0 {
		return false
	}
	_, ok := s.suppressedCallIDs[strings.TrimSpace(callID)]
	return ok
}

// blocklistMatchesCallSummary applies the emit-time defense-in-depth filter
// to a search-result row. Title is the highest-risk surface that escapes
// scoped-reader column grants, so check it explicitly.
func (s *Server) blocklistMatchesCallSummary(summary searchCallSummary) bool {
	if s.blocklistGuard == nil || s.blocklistGuard.Empty() {
		return false
	}
	return s.blocklistGuard.MatchValue(summary.Title)
}

// blocklistMatchesCallDetail checks every customer-identifying field that
// get_call can return: title and CRM object names.
func (s *Server) blocklistMatchesCallDetail(detail *sqlite.CallDetail) bool {
	if detail == nil || s.blocklistGuard == nil || s.blocklistGuard.Empty() {
		return false
	}
	if s.blocklistGuard.MatchValue(detail.Title) {
		return true
	}
	for _, obj := range detail.CRMObjects {
		if s.blocklistGuard.MatchValue(obj.ObjectName) {
			return true
		}
	}
	return false
}

func (s *Server) blocklistMatchesBusinessAnalysisCall(row sqlite.BusinessAnalysisCallRow) bool {
	if s.blocklistGuard == nil || s.blocklistGuard.Empty() {
		return false
	}
	return s.blocklistGuard.MatchValue(row.Title)
}

func (s *Server) blocklistMatchesBusinessAnalysisEvidence(row sqlite.BusinessAnalysisEvidenceRow) bool {
	if s.blocklistGuard == nil || s.blocklistGuard.Empty() {
		return false
	}
	return s.blocklistGuard.MatchAny([]string{row.Title, row.AccountName, row.OpportunityName})
}

// applyPolicySwitchesToSearchSummary suppresses customer-identifying fields
// in a search-result row according to the active policy switches. The switch
// set is intentionally conservative in this slice: only the switches that map
// directly to existing search summary fields take effect; every other switch
// is parsed and visible in runtime status but is a no-op at this layer.
func (s *Server) applyPolicySwitchesToSearchSummary(summary *searchCallSummary) {
	if summary == nil {
		return
	}
	if s.policySwitches.HideCallTitles {
		summary.Title = ""
	}
	if s.policySwitches.HideRawCallIDs {
		summary.CallID = ""
	}
}

// applyPolicySwitchesToCallDetail applies policy switches to a get_call detail
// payload before serialization.
func (s *Server) applyPolicySwitchesToCallDetail(detail *sqlite.CallDetail) {
	if detail == nil {
		return
	}
	if s.policySwitches.HideCallTitles {
		detail.Title = ""
	}
	if s.policySwitches.HideRawCallIDs {
		detail.CallID = ""
	}
	if s.policySwitches.HideAccountNames {
		for idx := range detail.CRMObjects {
			detail.CRMObjects[idx].ObjectName = ""
		}
	}
}

func (s *Server) applyPolicySwitchesToBusinessAnalysisItem(item *businessAnalysisItem) {
	if item == nil {
		return
	}
	if s.policySwitches.HideCallTitles {
		item.CallTitle = ""
	}
	if s.policySwitches.HideRawCallIDs {
		item.CallID = ""
	}
	if s.policySwitches.HideAccountNames {
		item.AccountName = ""
	}
	if s.policySwitches.HideOpportunityNames {
		item.OpportunityName = ""
	}
}

func (s *Server) governanceActive() bool {
	return len(s.suppressedCallIDs) > 0
}

func (s *Server) restrictedAccountQuery(query string) bool {
	if len(s.restrictedAccountQueries) == 0 {
		return false
	}
	_, ok := s.restrictedAccountQueries[governance.NormalizeName(query)]
	return ok
}

func (s *Server) restrictedAccountQueryAny(queries []string) bool {
	for _, query := range queries {
		if s.restrictedAccountQuery(query) {
			return true
		}
	}
	return false
}

func restrictedAccountQueryError() error {
	return errors.New("account_query is unavailable for this governed term")
}

func governanceFilteredAggregateError(toolName string) error {
	return fmt.Errorf("%s is unavailable while AI governance filtering is active because this aggregate has not been recomputed over the filtered call set", toolName)
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
	if len(detail.CRMObjects) > defaultCallDetailObjects {
		detail.CRMObjects = detail.CRMObjects[:defaultCallDetailObjects]
		detail.CRMObjectsTruncated = true
	}
	for idx := range detail.CRMObjects {
		detail.CRMObjects[idx].ObjectName = ""
		if len(detail.CRMObjects[idx].FieldNames) > defaultCallDetailFieldNames {
			detail.CRMObjects[idx].FieldNames = detail.CRMObjects[idx].FieldNames[:defaultCallDetailFieldNames]
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
	if len(objects) > defaultCallDetailObjects {
		objects = objects[:defaultCallDetailObjects]
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
	if len(fieldNames) > defaultCallDetailFieldNames {
		fieldNames = fieldNames[:defaultCallDetailFieldNames]
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
	if properties == nil {
		properties = map[string]any{}
	}
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
