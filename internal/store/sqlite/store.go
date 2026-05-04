package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db       *sql.DB
	readOnly bool
}

const (
	defaultMissingTranscriptsLimit = 100
	maxMissingTranscriptsLimit     = 10000
	defaultTranscriptSearchLimit   = 20
	maxTranscriptSearchLimit       = 1000
	defaultCallSearchLimit         = 20
	maxCallSearchLimit             = 1000
	defaultCRMFieldLimit           = 50
	maxCRMFieldLimit               = 1000
	defaultCRMFieldValueLimit      = 20
	maxCRMFieldValueLimit          = 1000
	defaultLateStageSignalLimit    = 25
	maxLateStageSignalLimit        = 500
	defaultOpportunitySummaryLimit = 25
	maxOpportunitySummaryLimit     = 1000
	defaultCRMMatrixLimit          = 50
	maxCRMMatrixLimit              = 1000
	defaultLifecycleLimit          = 25
	maxLifecycleLimit              = 1000
	defaultLifecycleCRMFieldLimit  = 50
	maxLifecycleCRMFieldLimit      = 1000
	defaultCallFactsLimit          = 50
	maxCallFactsLimit              = 1000
	defaultInventoryLimit          = 50
	maxInventoryLimit              = 1000
)

type StartSyncRunParams struct {
	Scope          string
	SyncKey        string
	Cursor         string
	From           string
	To             string
	RequestContext string
}

type FinishSyncRunParams struct {
	Status         string
	Cursor         string
	RecordsSeen    int64
	RecordsWritten int64
	ErrorText      string
	RequestContext string
}

type SyncRun struct {
	ID             int64
	Scope          string
	SyncKey        string
	Cursor         string
	From           string
	To             string
	RequestContext string
	Status         string
	StartedAt      string
	FinishedAt     string
	RecordsSeen    int64
	RecordsWritten int64
	ErrorText      string
}

type SyncState struct {
	SyncKey       string
	Scope         string
	Cursor        string
	LastRunID     int64
	LastStatus    string
	LastError     string
	LastSuccessAt string
	UpdatedAt     string
}

type CallRecord struct {
	CallID          string
	Title           string
	StartedAt       string
	DurationSeconds int64
	PartiesCount    int64
	ContextPresent  bool
	RawJSON         []byte
	RawSHA256       string
	FirstSeenAt     string
	UpdatedAt       string
}

type CallDetail struct {
	CallID              string                `json:"call_id"`
	Title               string                `json:"title"`
	StartedAt           string                `json:"started_at"`
	DurationSeconds     int64                 `json:"duration_seconds"`
	PartiesCount        int64                 `json:"parties_count"`
	CRMObjects          []CallDetailCRMObject `json:"crm_objects,omitempty"`
	CRMObjectsTruncated bool                  `json:"crm_objects_truncated,omitempty"`
}

type CallDetailCRMObject struct {
	ObjectType          string   `json:"object_type"`
	ObjectID            string   `json:"object_id"`
	ObjectName          string   `json:"object_name,omitempty"`
	FieldCount          int64    `json:"field_count"`
	PopulatedFieldCount int64    `json:"populated_field_count"`
	FieldNames          []string `json:"field_names,omitempty"`
	FieldNamesTruncated bool     `json:"field_names_truncated,omitempty"`
}

type ContextCounts struct {
	Objects int
	Fields  int
}

type UserRecord struct {
	UserID      string
	Email       string
	FirstName   string
	LastName    string
	DisplayName string
	Title       string
	Active      bool
	RawJSON     []byte
	RawSHA256   string
	FirstSeenAt string
	UpdatedAt   string
}

type TranscriptRecord struct {
	CallID       string
	SegmentCount int
	RawJSON      []byte
	RawSHA256    string
	FirstSeenAt  string
	UpdatedAt    string
}

type TranscriptSegment struct {
	ID           int64
	CallID       string
	SegmentIndex int
	SpeakerID    string
	StartMS      int64
	EndMS        int64
	Text         string
	RawJSON      []byte
}

type MissingTranscriptCall struct {
	CallID    string
	Title     string
	StartedAt string
}

type TranscriptSearchResult struct {
	CallID       string
	SpeakerID    string
	SegmentIndex int
	StartMS      int64
	EndMS        int64
	Text         string
	Snippet      string
}

type CallSearchParams struct {
	CRMObjectType    string
	CRMObjectID      string
	FromDate         string
	ToDate           string
	LifecycleBucket  string
	Scope            string
	System           string
	Direction        string
	TranscriptStatus string
	Limit            int
}

type CRMObjectTypeSummary struct {
	ObjectType            string `json:"object_type"`
	ObjectCount           int64  `json:"object_count"`
	CallCount             int64  `json:"call_count"`
	FieldCount            int64  `json:"field_count"`
	PopulatedFieldCount   int64  `json:"populated_field_count"`
	DistinctObjectIDCount int64  `json:"distinct_object_id_count"`
}

type CRMFieldSummary struct {
	ObjectType         string   `json:"object_type"`
	FieldName          string   `json:"field_name"`
	FieldLabel         string   `json:"field_label"`
	RowCount           int64    `json:"row_count"`
	CallCount          int64    `json:"call_count"`
	PopulatedCount     int64    `json:"populated_count"`
	DistinctValueCount int64    `json:"distinct_value_count"`
	ExampleValues      []string `json:"example_values,omitempty"`
}

type CRMFieldValueSearchParams struct {
	ObjectType          string
	FieldName           string
	ValueQuery          string
	Limit               int
	IncludeValueSnippet bool
}

type CRMFieldValueMatch struct {
	CallID       string `json:"call_id"`
	Title        string `json:"title"`
	StartedAt    string `json:"started_at"`
	ObjectType   string `json:"object_type"`
	ObjectID     string `json:"object_id,omitempty"`
	ObjectName   string `json:"object_name,omitempty"`
	FieldName    string `json:"field_name"`
	FieldLabel   string `json:"field_label"`
	ValueSnippet string `json:"value_snippet,omitempty"`
}

type CRMIntegrationRecord struct {
	IntegrationID string `json:"integration_id"`
	Name          string `json:"name,omitempty"`
	Provider      string `json:"provider,omitempty"`
	UpdatedAt     string `json:"updated_at,omitempty"`
}

type CRMSchemaObjectRecord struct {
	IntegrationID string `json:"integration_id"`
	ObjectType    string `json:"object_type"`
	DisplayName   string `json:"display_name,omitempty"`
	FieldCount    int64  `json:"field_count"`
	UpdatedAt     string `json:"updated_at,omitempty"`
}

type CRMSchemaFieldListParams struct {
	IntegrationID string
	ObjectType    string
	Limit         int
}

type CRMSchemaFieldRecord struct {
	IntegrationID string `json:"integration_id"`
	ObjectType    string `json:"object_type"`
	FieldName     string `json:"field_name"`
	FieldLabel    string `json:"field_label,omitempty"`
	FieldType     string `json:"field_type,omitempty"`
	UpdatedAt     string `json:"updated_at,omitempty"`
}

type GongSettingListParams struct {
	Kind  string
	Limit int
}

type GongSettingRecord struct {
	Kind      string `json:"kind"`
	ObjectID  string `json:"object_id"`
	Name      string `json:"name,omitempty"`
	Active    bool   `json:"active"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

type ScorecardListParams struct {
	ActiveOnly bool
	Limit      int
}

type ScorecardSummary struct {
	ScorecardID     string `json:"scorecard_id"`
	Name            string `json:"name"`
	Active          bool   `json:"active"`
	ReviewMethod    string `json:"review_method,omitempty"`
	WorkspaceID     string `json:"workspace_id,omitempty"`
	QuestionCount   int64  `json:"question_count"`
	SourceCreatedAt string `json:"source_created_at,omitempty"`
	SourceUpdatedAt string `json:"source_updated_at,omitempty"`
	CachedUpdatedAt string `json:"cached_updated_at,omitempty"`
}

type ScorecardDetail struct {
	ScorecardSummary
	Questions []ScorecardQuestion `json:"questions"`
}

type ScorecardQuestion struct {
	QuestionID   string   `json:"question_id,omitempty"`
	QuestionText string   `json:"question_text"`
	QuestionType string   `json:"question_type,omitempty"`
	IsOverall    bool     `json:"is_overall,omitempty"`
	MinRange     int64    `json:"min_range,omitempty"`
	MaxRange     int64    `json:"max_range,omitempty"`
	AnswerGuide  string   `json:"answer_guide,omitempty"`
	Options      []string `json:"options,omitempty"`
}

type ScorecardActivityRecord struct {
	AnsweredScorecardID string  `json:"answered_scorecard_id"`
	ScorecardID         string  `json:"scorecard_id,omitempty"`
	ScorecardName       string  `json:"scorecard_name,omitempty"`
	CallID              string  `json:"call_id,omitempty"`
	CallStartedAt       string  `json:"call_started_at,omitempty"`
	ReviewedUserID      string  `json:"reviewed_user_id,omitempty"`
	ReviewerUserID      string  `json:"reviewer_user_id,omitempty"`
	EditorUserID        string  `json:"editor_user_id,omitempty"`
	ReviewMethod        string  `json:"review_method,omitempty"`
	ReviewTime          string  `json:"review_time,omitempty"`
	VisibilityType      string  `json:"visibility_type,omitempty"`
	OverallScore        float64 `json:"overall_score,omitempty"`
	AverageScore        float64 `json:"average_score,omitempty"`
	AnswerCount         int64   `json:"answer_count"`
	RawSHA256           string  `json:"raw_sha256,omitempty"`
	UpdatedAt           string  `json:"updated_at,omitempty"`
}

type ScorecardActivitySummaryParams struct {
	GroupBy string
	Limit   int
}

type ScorecardActivitySummaryRow struct {
	GroupBy                string  `json:"group_by"`
	GroupValue             string  `json:"group_value"`
	AnsweredScorecardCount int64   `json:"answered_scorecard_count"`
	DistinctCallCount      int64   `json:"distinct_call_count"`
	LinkedCallCount        int64   `json:"linked_call_count"`
	ReviewedUserCount      int64   `json:"reviewed_user_count"`
	ManualCount            int64   `json:"manual_count"`
	AutomaticCount         int64   `json:"automatic_count"`
	TranscriptCount        int64   `json:"transcript_count"`
	MissingTranscriptCount int64   `json:"missing_transcript_count"`
	AverageOverallScore    float64 `json:"average_overall_score"`
	AverageAnswerScore     float64 `json:"average_answer_score"`
	LatestReviewTime       string  `json:"latest_review_time"`
}

type ScorecardActivityOverview struct {
	TotalAnsweredScorecards int64                         `json:"total_answered_scorecards"`
	DistinctScorecards      int64                         `json:"distinct_scorecards"`
	DistinctCalls           int64                         `json:"distinct_calls"`
	LinkedCallCount         int64                         `json:"linked_call_count"`
	ReviewedUserCount       int64                         `json:"reviewed_user_count"`
	ManualCount             int64                         `json:"manual_count"`
	AutomaticCount          int64                         `json:"automatic_count"`
	TranscriptCount         int64                         `json:"transcript_count"`
	MissingTranscriptCount  int64                         `json:"missing_transcript_count"`
	AverageOverallScore     float64                       `json:"average_overall_score"`
	AverageAnswerScore      float64                       `json:"average_answer_score"`
	LatestReviewTime        string                        `json:"latest_review_time"`
	ByScorecard             []ScorecardActivitySummaryRow `json:"by_scorecard"`
	ByReviewMethod          []ScorecardActivitySummaryRow `json:"by_review_method"`
	ByLifecycle             []ScorecardActivitySummaryRow `json:"by_lifecycle"`
	ByTranscriptStatus      []ScorecardActivitySummaryRow `json:"by_transcript_status"`
}

type LateStageSignalParams struct {
	ObjectType          string
	StageField          string
	LateStageValues     []string
	IncludeStageProxies bool
	Limit               int
}

type LateStageSignalsReport struct {
	ObjectType      string            `json:"object_type"`
	StageField      string            `json:"stage_field"`
	LateStageValues []string          `json:"late_stage_values"`
	TotalCalls      int64             `json:"total_calls"`
	LateCalls       int64             `json:"late_calls"`
	NonLateCalls    int64             `json:"non_late_calls"`
	StageCounts     map[string]int64  `json:"stage_counts"`
	Signals         []LateStageSignal `json:"signals"`
}

type LateStageSignal struct {
	FieldName             string  `json:"field_name"`
	FieldLabel            string  `json:"field_label"`
	LatePopulatedCalls    int64   `json:"late_populated_calls"`
	NonLatePopulatedCalls int64   `json:"non_late_populated_calls"`
	LateRate              float64 `json:"late_rate"`
	NonLateRate           float64 `json:"non_late_rate"`
	Lift                  float64 `json:"lift"`
}

type OpportunityMissingTranscriptParams struct {
	StageValues []string
	Limit       int
}

type OpportunityMissingTranscriptSummary struct {
	OpportunityID          string `json:"opportunity_id"`
	OpportunityName        string `json:"opportunity_name"`
	Stage                  string `json:"stage,omitempty"`
	CallCount              int64  `json:"call_count"`
	MissingTranscriptCount int64  `json:"missing_transcript_count"`
	TranscriptCount        int64  `json:"transcript_count"`
	LatestCallID           string `json:"latest_call_id"`
	LatestCallAt           string `json:"latest_call_at"`
}

type TranscriptCRMSearchParams struct {
	Query      string
	ObjectType string
	ObjectID   string
	Limit      int
}

type TranscriptCallFactsSearchParams struct {
	Query           string
	FromDate        string
	ToDate          string
	LifecycleBucket string
	Scope           string
	System          string
	Direction       string
	Limit           int
}

type TranscriptAttributionSearchParams struct {
	Query            string
	FromDate         string
	ToDate           string
	LifecycleBucket  string
	Scope            string
	System           string
	Direction        string
	TranscriptStatus string
	Industry         string
	AccountQuery     string
	OpportunityStage string
	Limit            int
}

type MissingTranscriptSearchParams struct {
	FromDate        string
	ToDate          string
	LifecycleBucket string
	Scope           string
	System          string
	Direction       string
	CRMObjectType   string
	CRMObjectID     string
	Limit           int
}

type TranscriptCRMSearchResult struct {
	CallID              string `json:"call_id"`
	Title               string `json:"title"`
	StartedAt           string `json:"started_at"`
	ObjectType          string `json:"object_type"`
	ObjectID            string `json:"object_id"`
	ObjectName          string `json:"object_name"`
	MatchingObjectCount int64  `json:"matching_object_count"`
	SpeakerID           string `json:"speaker_id"`
	SegmentIndex        int    `json:"segment_index"`
	StartMS             int64  `json:"start_ms"`
	EndMS               int64  `json:"end_ms"`
	Snippet             string `json:"snippet"`
}

type TranscriptCallFactsSearchResult struct {
	CallID          string `json:"-"`
	SpeakerID       string `json:"-"`
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

type TranscriptAttributionSearchResult struct {
	CallID                 string `json:"call_id,omitempty"`
	Title                  string `json:"title,omitempty"`
	StartedAt              string `json:"started_at"`
	CallDate               string `json:"call_date,omitempty"`
	LifecycleBucket        string `json:"lifecycle_bucket,omitempty"`
	AccountName            string `json:"account_name,omitempty"`
	AccountIndustry        string `json:"account_industry,omitempty"`
	AccountWebsite         string `json:"account_website,omitempty"`
	OpportunityName        string `json:"opportunity_name,omitempty"`
	OpportunityStage       string `json:"opportunity_stage,omitempty"`
	OpportunityType        string `json:"opportunity_type,omitempty"`
	OpportunityCloseDate   string `json:"opportunity_close_date,omitempty"`
	OpportunityProbability string `json:"opportunity_probability,omitempty"`
	ParticipantStatus      string `json:"participant_status"`
	PersonTitleStatus      string `json:"person_title_status"`
	PersonTitleSource      string `json:"person_title_source,omitempty"`
	SegmentIndex           int    `json:"segment_index"`
	StartMS                int64  `json:"start_ms"`
	EndMS                  int64  `json:"end_ms"`
	Snippet                string `json:"snippet"`
	ContextExcerpt         string `json:"context_excerpt"`
}

type OpportunityCallSummaryParams struct {
	StageValues []string
	Limit       int
}

type OpportunityCallSummary struct {
	OpportunityID          string `json:"opportunity_id"`
	OpportunityName        string `json:"opportunity_name"`
	Stage                  string `json:"stage,omitempty"`
	Amount                 string `json:"amount,omitempty"`
	CloseDate              string `json:"close_date,omitempty"`
	OwnerID                string `json:"owner_id,omitempty"`
	CallCount              int64  `json:"call_count"`
	TranscriptCount        int64  `json:"transcript_count"`
	MissingTranscriptCount int64  `json:"missing_transcript_count"`
	TotalDurationSeconds   int64  `json:"total_duration_seconds"`
	LatestCallID           string `json:"latest_call_id"`
	LatestCallAt           string `json:"latest_call_at"`
}

type CRMFieldPopulationMatrixParams struct {
	ObjectType   string
	GroupByField string
	Limit        int
}

type CRMFieldPopulationMatrix struct {
	ObjectType   string                   `json:"object_type"`
	GroupByField string                   `json:"group_by_field"`
	Cells        []CRMFieldPopulationCell `json:"cells"`
}

type CRMFieldPopulationCell struct {
	GroupValue     string  `json:"group_value"`
	FieldName      string  `json:"field_name"`
	FieldLabel     string  `json:"field_label"`
	ObjectCount    int64   `json:"object_count"`
	CallCount      int64   `json:"call_count"`
	PopulatedCount int64   `json:"populated_count"`
	PopulationRate float64 `json:"population_rate"`
}

type LifecycleBucketDefinition struct {
	Bucket         string   `json:"bucket"`
	Label          string   `json:"label"`
	Description    string   `json:"description"`
	PrimarySignals []string `json:"primary_signals"`
}

type LifecycleSummaryParams struct {
	Bucket          string
	LifecycleSource string
}

type LifecycleBucketSummary struct {
	Bucket                 string `json:"bucket"`
	CallCount              int64  `json:"call_count"`
	TranscriptCount        int64  `json:"transcript_count"`
	MissingTranscriptCount int64  `json:"missing_transcript_count"`
	OpportunityCallCount   int64  `json:"opportunity_call_count"`
	AccountCallCount       int64  `json:"account_call_count"`
	TotalDurationSeconds   int64  `json:"total_duration_seconds"`
	LatestCallID           string `json:"latest_call_id"`
	LatestCallAt           string `json:"latest_call_at"`
	HighConfidenceCalls    int64  `json:"high_confidence_calls"`
	MediumConfidenceCalls  int64  `json:"medium_confidence_calls"`
	LowConfidenceCalls     int64  `json:"low_confidence_calls"`
}

type LifecycleCallSearchParams struct {
	Bucket                 string
	MissingTranscriptsOnly bool
	Limit                  int
	LifecycleSource        string
}

type LifecycleCallSearchResult struct {
	CallID            string   `json:"call_id"`
	Title             string   `json:"title"`
	StartedAt         string   `json:"started_at"`
	DurationSeconds   int64    `json:"duration_seconds"`
	Bucket            string   `json:"bucket"`
	Confidence        string   `json:"confidence"`
	Reason            string   `json:"reason"`
	EvidenceFields    []string `json:"evidence_fields,omitempty"`
	OpportunityCount  int64    `json:"opportunity_count"`
	AccountCount      int64    `json:"account_count"`
	TranscriptPresent bool     `json:"transcript_present"`
}

type LifecycleTranscriptPriorityParams struct {
	Bucket          string
	FromDate        string
	ToDate          string
	Scope           string
	System          string
	Direction       string
	Limit           int
	LifecycleSource string
}

type LifecycleTranscriptPriority struct {
	CallID          string   `json:"call_id"`
	Title           string   `json:"title"`
	StartedAt       string   `json:"started_at"`
	DurationSeconds int64    `json:"duration_seconds"`
	System          string   `json:"system,omitempty"`
	Direction       string   `json:"direction,omitempty"`
	Scope           string   `json:"scope,omitempty"`
	Bucket          string   `json:"bucket"`
	Confidence      string   `json:"confidence"`
	PriorityScore   int64    `json:"priority_score"`
	Reason          string   `json:"reason"`
	EvidenceFields  []string `json:"evidence_fields,omitempty"`
}

type LifecycleCRMFieldComparisonParams struct {
	BucketA    string
	BucketB    string
	ObjectType string
	Limit      int
}

type LifecycleCRMFieldComparison struct {
	BucketA    string                           `json:"bucket_a"`
	BucketB    string                           `json:"bucket_b"`
	ObjectType string                           `json:"object_type,omitempty"`
	Fields     []LifecycleCRMFieldComparisonRow `json:"fields"`
}

type LifecycleCRMFieldComparisonRow struct {
	ObjectType       string  `json:"object_type"`
	FieldName        string  `json:"field_name"`
	FieldLabel       string  `json:"field_label"`
	BucketACallCount int64   `json:"bucket_a_call_count"`
	BucketBCallCount int64   `json:"bucket_b_call_count"`
	BucketAPopulated int64   `json:"bucket_a_populated"`
	BucketBPopulated int64   `json:"bucket_b_populated"`
	BucketARate      float64 `json:"bucket_a_rate"`
	BucketBRate      float64 `json:"bucket_b_rate"`
	RateDelta        float64 `json:"rate_delta"`
}

type CallFactsSummaryParams struct {
	GroupBy          string
	LifecycleBucket  string
	Scope            string
	System           string
	Direction        string
	TranscriptStatus string
	Limit            int
	LifecycleSource  string
}

type CallFactsSummaryRow struct {
	GroupBy                string  `json:"group_by"`
	GroupValue             string  `json:"group_value"`
	CallCount              int64   `json:"call_count"`
	TranscriptCount        int64   `json:"transcript_count"`
	MissingTranscriptCount int64   `json:"missing_transcript_count"`
	TranscriptCoverageRate float64 `json:"transcript_coverage_rate"`
	OpportunityCallCount   int64   `json:"opportunity_call_count"`
	AccountCallCount       int64   `json:"account_call_count"`
	ExternalCallCount      int64   `json:"external_call_count"`
	InternalCallCount      int64   `json:"internal_call_count"`
	UnknownScopeCallCount  int64   `json:"unknown_scope_call_count"`
	TotalDurationSeconds   int64   `json:"total_duration_seconds"`
	AvgDurationSeconds     float64 `json:"avg_duration_seconds"`
	LatestCallAt           string  `json:"latest_call_at"`
}

type CallFactsCoverage struct {
	TotalCalls             int64   `json:"total_calls"`
	TranscriptCount        int64   `json:"transcript_count"`
	MissingTranscriptCount int64   `json:"missing_transcript_count"`
	TranscriptCoverageRate float64 `json:"transcript_coverage_rate"`
	OpportunityCallCount   int64   `json:"opportunity_call_count"`
	AccountCallCount       int64   `json:"account_call_count"`
	ExternalCallCount      int64   `json:"external_call_count"`
	InternalCallCount      int64   `json:"internal_call_count"`
	UnknownScopeCallCount  int64   `json:"unknown_scope_call_count"`
	PurposePopulatedCalls  int64   `json:"purpose_populated_calls"`
	CalendarCallCount      int64   `json:"calendar_call_count"`
	TotalDurationSeconds   int64   `json:"total_duration_seconds"`
}

type SyncStatusSummary struct {
	TotalCalls                   int64               `json:"total_calls"`
	TotalUsers                   int64               `json:"total_users"`
	TotalTranscripts             int64               `json:"total_transcripts"`
	TotalTranscriptSegments      int64               `json:"total_transcript_segments"`
	TotalEmbeddedCRMContextCalls int64               `json:"total_embedded_crm_context_calls"`
	TotalEmbeddedCRMObjects      int64               `json:"total_embedded_crm_objects"`
	TotalEmbeddedCRMFields       int64               `json:"total_embedded_crm_fields"`
	TotalCRMIntegrations         int64               `json:"total_crm_integrations"`
	TotalCRMSchemaObjects        int64               `json:"total_crm_schema_objects"`
	TotalCRMSchemaFields         int64               `json:"total_crm_schema_fields"`
	TotalGongSettings            int64               `json:"total_gong_settings"`
	TotalScorecards              int64               `json:"total_scorecards"`
	TotalScorecardActivity       int64               `json:"total_scorecard_activity"`
	MissingTranscripts           int64               `json:"missing_transcripts"`
	RunningSyncRuns              int64               `json:"running_sync_runs"`
	ProfileReadiness             ProfileReadiness    `json:"profile_readiness"`
	PublicReadiness              PublicReadiness     `json:"public_readiness"`
	AttributionCoverage          AttributionCoverage `json:"attribution_coverage"`
	LastRun                      *SyncRun            `json:"last_run,omitempty"`
	LastSuccessfulRun            *SyncRun            `json:"last_successful_run,omitempty"`
	States                       []SyncState         `json:"states"`
}

type AttributionCoverage struct {
	CallsWithTitles       int64  `json:"calls_with_titles"`
	CallsWithParties      int64  `json:"calls_with_parties"`
	CallsWithPartyTitles  int64  `json:"calls_with_party_titles"`
	UsersWithTitles       int64  `json:"users_with_titles"`
	AccountNameCalls      int64  `json:"account_name_calls"`
	AccountIndustryCalls  int64  `json:"account_industry_calls"`
	OpportunityStageCalls int64  `json:"opportunity_stage_calls"`
	ContactObjectCalls    int64  `json:"contact_object_calls"`
	LeadObjectCalls       int64  `json:"lead_object_calls"`
	ObjectsWithNames      int64  `json:"objects_with_names"`
	ParticipantStatus     string `json:"participant_status"`
	PersonTitleStatus     string `json:"person_title_status"`
	RecommendedNextAction string `json:"recommended_next_action,omitempty"`
}

type CacheInventory struct {
	TableCounts         []CacheTableCount  `json:"table_counts"`
	OldestCallStartedAt string             `json:"oldest_call_started_at,omitempty"`
	NewestCallStartedAt string             `json:"newest_call_started_at,omitempty"`
	Summary             *SyncStatusSummary `json:"summary"`
}

type CacheTableCount struct {
	Table string `json:"table"`
	Rows  int64  `json:"rows"`
}

type CachePurgePlan struct {
	StartedBefore          string `json:"started_before"`
	CallCount              int64  `json:"call_count"`
	TranscriptCount        int64  `json:"transcript_count"`
	TranscriptSegmentCount int64  `json:"transcript_segment_count"`
	ContextObjectCount     int64  `json:"context_object_count"`
	ContextFieldCount      int64  `json:"context_field_count"`
	ProfileCallFactCount   int64  `json:"profile_call_fact_count"`
}

type ProfileReadiness struct {
	Active                  bool     `json:"active"`
	Status                  string   `json:"status"`
	Detail                  string   `json:"detail"`
	Name                    string   `json:"name,omitempty"`
	Version                 int      `json:"version,omitempty"`
	CanonicalSHA256         string   `json:"canonical_sha256,omitempty"`
	ObjectConceptCount      int      `json:"object_concept_count,omitempty"`
	FieldConceptCount       int      `json:"field_concept_count,omitempty"`
	LifecycleBucketCount    int      `json:"lifecycle_bucket_count,omitempty"`
	MethodologyConceptCount int      `json:"methodology_concept_count,omitempty"`
	WarningCount            int      `json:"warning_count,omitempty"`
	UnavailableConcepts     []string `json:"unavailable_concepts,omitempty"`
	CacheFresh              bool     `json:"cache_fresh"`
	CacheStatus             string   `json:"cache_status"`
	Blocking                []string `json:"blocking,omitempty"`
}

type PublicReadiness struct {
	ConversationVolume    ReadinessFlag `json:"conversation_volume"`
	TranscriptCoverage    ReadinessFlag `json:"transcript_coverage"`
	ScorecardThemes       ReadinessFlag `json:"scorecard_themes"`
	LifecycleSeparation   ReadinessFlag `json:"lifecycle_separation"`
	CRMSegmentation       ReadinessFlag `json:"crm_segmentation"`
	AttributionReadiness  ReadinessFlag `json:"attribution_readiness"`
	CRMInventoryNote      string        `json:"crm_inventory_note,omitempty"`
	RecommendedNextAction string        `json:"recommended_next_action,omitempty"`
}

type ReadinessFlag struct {
	Ready        bool     `json:"ready"`
	Status       string   `json:"status"`
	Detail       string   `json:"detail"`
	Requirements []string `json:"requirements,omitempty"`
}

type callPayload struct {
	CallID          string
	Title           string
	StartedAt       string
	DurationSeconds int64
	PartiesCount    int64
	ContextPresent  bool
	RawJSON         []byte
	RawSHA256       string
	ContextObjects  []contextObjectRow
	HasContextBlock bool
}

type userPayload struct {
	UserID      string
	Email       string
	FirstName   string
	LastName    string
	DisplayName string
	Title       string
	Active      bool
	RawJSON     []byte
	RawSHA256   string
}

type transcriptPayload struct {
	CallID    string
	RawJSON   []byte
	RawSHA256 string
	Segments  []TranscriptSegment
}

type crmIntegrationPayload struct {
	IntegrationID string
	Name          string
	Provider      string
	RawJSON       []byte
	RawSHA256     string
}

type crmSchemaFieldPayload struct {
	FieldName  string
	FieldLabel string
	FieldType  string
	RawJSON    []byte
	RawSHA256  string
}

type gongSettingPayload struct {
	Kind      string
	ObjectID  string
	Name      string
	Active    bool
	RawJSON   []byte
	RawSHA256 string
}

type scorecardActivityPayload struct {
	AnsweredScorecardID string
	ScorecardID         string
	ScorecardName       string
	CallID              string
	CallStartedAt       string
	ReviewedUserID      string
	ReviewerUserID      string
	EditorUserID        string
	ReviewMethod        string
	ReviewTime          string
	VisibilityType      string
	OverallScore        float64
	AverageScore        float64
	AnswerCount         int64
	RawJSON             []byte
	RawSHA256           string
}

type contextObjectRow struct {
	ObjectKey  string
	ObjectType string
	ObjectID   string
	ObjectName string
	RawJSON    []byte
	Fields     []contextFieldRow
}

type contextFieldRow struct {
	FieldName  string
	FieldLabel string
	FieldType  string
	ValueText  string
	RawJSON    []byte
}

func Open(ctx context.Context, path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("sqlite path is required")
	}
	if err := ensureParentDir(path); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	store := &Store{db: db}
	if err := store.configure(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.Migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func OpenReadOnly(ctx context.Context, path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("sqlite path is required")
	}
	cleanPath := filepath.Clean(path)
	if _, err := os.Stat(cleanPath); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", sqliteFileURI(cleanPath, url.Values{"mode": []string{"ro"}}))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)

	store := &Store{db: db, readOnly: true}
	if err := store.configureReadOnly(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.validateSchema(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) DB() *sql.DB {
	if s == nil {
		return nil
	}
	return s.db
}

func (s *Store) Migrate(ctx context.Context) error {
	current, err := s.userVersion(ctx)
	if err != nil {
		return err
	}

	for idx := current; idx < len(migrations); idx++ {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, migrations[idx]); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %d: %w", idx+1, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", idx+1)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("set user_version %d: %w", idx+1, err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) StartSyncRun(ctx context.Context, params StartSyncRunParams) (*SyncRun, error) {
	scope := strings.TrimSpace(params.Scope)
	if scope == "" {
		return nil, errors.New("sync run scope is required")
	}

	now := nowUTC()
	result, err := s.db.ExecContext(
		ctx,
		`INSERT INTO sync_runs(scope, sync_key, cursor, from_value, to_value, request_context, status, started_at)
		 VALUES(?, ?, ?, ?, ?, ?, 'running', ?)`,
		scope,
		strings.TrimSpace(params.SyncKey),
		strings.TrimSpace(params.Cursor),
		strings.TrimSpace(params.From),
		strings.TrimSpace(params.To),
		strings.TrimSpace(params.RequestContext),
		now,
	)
	if err != nil {
		return nil, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return nil, err
	}
	return &SyncRun{
		ID:             id,
		Scope:          scope,
		SyncKey:        strings.TrimSpace(params.SyncKey),
		Cursor:         strings.TrimSpace(params.Cursor),
		From:           strings.TrimSpace(params.From),
		To:             strings.TrimSpace(params.To),
		RequestContext: strings.TrimSpace(params.RequestContext),
		Status:         "running",
		StartedAt:      now,
	}, nil
}

func (s *Store) FinishSyncRun(ctx context.Context, runID int64, params FinishSyncRunParams) error {
	if runID <= 0 {
		return errors.New("run id must be positive")
	}

	status := strings.TrimSpace(params.Status)
	if status == "" {
		return errors.New("sync run status is required")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var scope string
	var syncKey string
	if err := tx.QueryRowContext(ctx, `SELECT scope, sync_key FROM sync_runs WHERE id = ?`, runID).Scan(&scope, &syncKey); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("sync run %d not found", runID)
		}
		return err
	}

	finishedAt := nowUTC()
	if _, err := tx.ExecContext(
		ctx,
		`UPDATE sync_runs
		    SET finished_at = ?,
		        status = ?,
		        cursor = ?,
		        records_seen = ?,
		        records_written = ?,
		        error_text = ?,
		        request_context = CASE WHEN ? <> '' THEN ? ELSE request_context END
		  WHERE id = ?`,
		finishedAt,
		status,
		strings.TrimSpace(params.Cursor),
		params.RecordsSeen,
		params.RecordsWritten,
		strings.TrimSpace(params.ErrorText),
		strings.TrimSpace(params.RequestContext),
		strings.TrimSpace(params.RequestContext),
		runID,
	); err != nil {
		return err
	}

	if syncKey != "" {
		lastSuccessAt := any(nil)
		if status == "success" {
			lastSuccessAt = finishedAt
		}
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO sync_state(sync_key, scope, cursor, last_run_id, last_status, last_error, last_success_at, updated_at)
			 VALUES(?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(sync_key) DO UPDATE SET
				scope = excluded.scope,
				cursor = CASE WHEN excluded.cursor <> '' THEN excluded.cursor ELSE sync_state.cursor END,
				last_run_id = excluded.last_run_id,
				last_status = excluded.last_status,
				last_error = excluded.last_error,
				last_success_at = CASE
					WHEN excluded.last_success_at IS NOT NULL THEN excluded.last_success_at
					ELSE sync_state.last_success_at
				END,
				updated_at = excluded.updated_at`,
			syncKey,
			scope,
			strings.TrimSpace(params.Cursor),
			runID,
			status,
			strings.TrimSpace(params.ErrorText),
			lastSuccessAt,
			finishedAt,
		); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	if status == "success" {
		if err := s.RefreshActiveProfileReadModel(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) UpsertCall(ctx context.Context, raw json.RawMessage) (*CallRecord, error) {
	payload, err := decodeCall(raw)
	if err != nil {
		return nil, err
	}

	now := nowUTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	query := `INSERT INTO calls(
				call_id, title, started_at, duration_seconds, parties_count, context_present,
				raw_json, raw_sha256, first_seen_at, updated_at
			)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(call_id) DO UPDATE SET
			title = excluded.title,
			started_at = excluded.started_at,
			duration_seconds = excluded.duration_seconds,
			parties_count = excluded.parties_count,
			context_present = excluded.context_present,
			raw_json = excluded.raw_json,
			raw_sha256 = excluded.raw_sha256,
			updated_at = excluded.updated_at
		WHERE
			calls.title IS NOT excluded.title OR
			calls.started_at IS NOT excluded.started_at OR
				calls.duration_seconds IS NOT excluded.duration_seconds OR
				calls.parties_count IS NOT excluded.parties_count OR
				calls.context_present IS NOT excluded.context_present OR
				calls.raw_sha256 IS NOT excluded.raw_sha256`
	if !payload.HasContextBlock {
		query = `INSERT INTO calls(
				call_id, title, started_at, duration_seconds, parties_count, context_present,
				raw_json, raw_sha256, first_seen_at, updated_at
			)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(call_id) DO UPDATE SET
				title = excluded.title,
				started_at = excluded.started_at,
				duration_seconds = excluded.duration_seconds,
				parties_count = CASE WHEN excluded.parties_count > 0 THEN excluded.parties_count ELSE calls.parties_count END,
				raw_json = CASE WHEN calls.context_present = 1 THEN calls.raw_json ELSE excluded.raw_json END,
				raw_sha256 = CASE WHEN calls.context_present = 1 THEN calls.raw_sha256 ELSE excluded.raw_sha256 END,
				updated_at = excluded.updated_at
			WHERE
				calls.title IS NOT excluded.title OR
				calls.started_at IS NOT excluded.started_at OR
				calls.duration_seconds IS NOT excluded.duration_seconds OR
				(excluded.parties_count > 0 AND calls.parties_count IS NOT excluded.parties_count) OR
				(calls.context_present = 0 AND calls.raw_sha256 IS NOT excluded.raw_sha256)`
	}
	if _, err := tx.ExecContext(
		ctx,
		query,
		payload.CallID,
		payload.Title,
		payload.StartedAt,
		payload.DurationSeconds,
		payload.PartiesCount,
		boolToInt(payload.ContextPresent),
		payload.RawJSON,
		payload.RawSHA256,
		now,
		now,
	); err != nil {
		return nil, err
	}

	if payload.HasContextBlock {
		if _, err := replaceContextRowsTx(ctx, tx, payload.CallID, payload.ContextObjects); err != nil {
			return nil, err
		}
	}
	if err := invalidateProfileCallFactCacheTx(ctx, tx); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	record, err := s.callByID(ctx, payload.CallID)
	if err != nil {
		return nil, err
	}
	return record, nil
}

func (s *Store) UpsertCallContext(ctx context.Context, callID string, raw json.RawMessage) (ContextCounts, error) {
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return ContextCounts{}, errors.New("call id is required")
	}

	objects, hasContext, err := extractContextObjects(raw)
	if err != nil {
		return ContextCounts{}, err
	}
	if !hasContext {
		return ContextCounts{}, errors.New("payload did not contain an extended context block")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ContextCounts{}, err
	}
	defer tx.Rollback()

	counts, err := replaceContextRowsTx(ctx, tx, callID, objects)
	if err != nil {
		return ContextCounts{}, err
	}
	if _, err := tx.ExecContext(
		ctx,
		`UPDATE calls SET context_present = ?, updated_at = ?
		  WHERE call_id = ?`,
		boolToInt(counts.Objects > 0),
		nowUTC(),
		callID,
	); err != nil {
		return ContextCounts{}, err
	}
	if err := invalidateProfileCallFactCacheTx(ctx, tx); err != nil {
		return ContextCounts{}, err
	}
	if err := tx.Commit(); err != nil {
		return ContextCounts{}, err
	}
	return counts, nil
}

func (s *Store) UpsertUser(ctx context.Context, raw json.RawMessage) (*UserRecord, error) {
	payload, err := decodeUser(raw)
	if err != nil {
		return nil, err
	}

	now := nowUTC()
	if _, err := s.db.ExecContext(
		ctx,
		`INSERT INTO users(
			user_id, email, first_name, last_name, display_name, title, active,
			raw_json, raw_sha256, first_seen_at, updated_at
		)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET
			email = excluded.email,
			first_name = excluded.first_name,
			last_name = excluded.last_name,
			display_name = excluded.display_name,
			title = excluded.title,
			active = excluded.active,
			raw_json = excluded.raw_json,
			raw_sha256 = excluded.raw_sha256,
			updated_at = excluded.updated_at
		WHERE
			users.email IS NOT excluded.email OR
			users.first_name IS NOT excluded.first_name OR
			users.last_name IS NOT excluded.last_name OR
			users.display_name IS NOT excluded.display_name OR
			users.title IS NOT excluded.title OR
			users.active IS NOT excluded.active OR
			users.raw_sha256 IS NOT excluded.raw_sha256`,
		payload.UserID,
		payload.Email,
		payload.FirstName,
		payload.LastName,
		payload.DisplayName,
		payload.Title,
		boolToInt(payload.Active),
		payload.RawJSON,
		payload.RawSHA256,
		now,
		now,
	); err != nil {
		return nil, err
	}

	record, err := s.userByID(ctx, payload.UserID)
	if err != nil {
		return nil, err
	}
	return record, nil
}

func (s *Store) UpsertCRMIntegration(ctx context.Context, raw json.RawMessage) (*CRMIntegrationRecord, error) {
	payload, err := decodeCRMIntegration(raw)
	if err != nil {
		return nil, err
	}

	now := nowUTC()
	if _, err := s.db.ExecContext(
		ctx,
		`INSERT INTO crm_integrations(
			integration_id, name, provider, raw_json, raw_sha256, first_seen_at, updated_at
		)
		VALUES(?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(integration_id) DO UPDATE SET
			name = excluded.name,
			provider = excluded.provider,
			raw_json = excluded.raw_json,
			raw_sha256 = excluded.raw_sha256,
			updated_at = excluded.updated_at
		WHERE
			crm_integrations.name IS NOT excluded.name OR
			crm_integrations.provider IS NOT excluded.provider OR
			crm_integrations.raw_sha256 IS NOT excluded.raw_sha256`,
		payload.IntegrationID,
		payload.Name,
		payload.Provider,
		payload.RawJSON,
		payload.RawSHA256,
		now,
		now,
	); err != nil {
		return nil, err
	}

	return s.crmIntegrationByID(ctx, payload.IntegrationID)
}

func (s *Store) UpsertCRMSchema(ctx context.Context, integrationID string, objectType string, raw json.RawMessage) (int64, error) {
	integrationID = strings.TrimSpace(integrationID)
	objectType = strings.TrimSpace(objectType)
	if integrationID == "" {
		return 0, errors.New("integration id is required")
	}
	if objectType == "" {
		return 0, errors.New("object type is required")
	}

	normalized, err := normalizeJSON(raw)
	if err != nil {
		return 0, err
	}
	var doc map[string]any
	if err := json.Unmarshal(normalized, &doc); err != nil {
		return 0, err
	}
	fields := extractCRMSchemaFields(doc, objectType)
	displayName := firstString(doc, "displayName", "label", "name")
	if displayName == "" {
		displayName = objectType
	}

	now := nowUTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO crm_schema_objects(
			integration_id, object_type, display_name, field_count,
			raw_json, raw_sha256, first_seen_at, updated_at
		)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(integration_id, object_type) DO UPDATE SET
			display_name = excluded.display_name,
			field_count = excluded.field_count,
			raw_json = excluded.raw_json,
			raw_sha256 = excluded.raw_sha256,
			updated_at = excluded.updated_at
		WHERE
			crm_schema_objects.display_name IS NOT excluded.display_name OR
			crm_schema_objects.field_count IS NOT excluded.field_count OR
			crm_schema_objects.raw_sha256 IS NOT excluded.raw_sha256`,
		integrationID,
		objectType,
		displayName,
		len(fields),
		normalized,
		sha256Hex(normalized),
		now,
		now,
	); err != nil {
		return 0, err
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM crm_schema_fields WHERE integration_id = ? AND object_type = ?`, integrationID, objectType); err != nil {
		return 0, err
	}
	for _, field := range fields {
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO crm_schema_fields(
				integration_id, object_type, field_name, field_label, field_type,
				raw_json, raw_sha256, first_seen_at, updated_at
			)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			integrationID,
			objectType,
			field.FieldName,
			field.FieldLabel,
			field.FieldType,
			field.RawJSON,
			field.RawSHA256,
			now,
			now,
		); err != nil {
			return 0, err
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int64(len(fields)), nil
}

func (s *Store) UpsertGongSetting(ctx context.Context, kind string, raw json.RawMessage) (*GongSettingRecord, error) {
	payload, err := decodeGongSetting(kind, raw)
	if err != nil {
		return nil, err
	}

	now := nowUTC()
	if _, err := s.db.ExecContext(
		ctx,
		`INSERT INTO gong_settings(
			kind, object_id, name, active, raw_json, raw_sha256, first_seen_at, updated_at
		)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(kind, object_id) DO UPDATE SET
			name = excluded.name,
			active = excluded.active,
			raw_json = excluded.raw_json,
			raw_sha256 = excluded.raw_sha256,
			updated_at = excluded.updated_at
		WHERE
			gong_settings.name IS NOT excluded.name OR
			gong_settings.active IS NOT excluded.active OR
			gong_settings.raw_sha256 IS NOT excluded.raw_sha256`,
		payload.Kind,
		payload.ObjectID,
		payload.Name,
		boolToInt(payload.Active),
		payload.RawJSON,
		payload.RawSHA256,
		now,
		now,
	); err != nil {
		return nil, err
	}

	return s.gongSettingByID(ctx, payload.Kind, payload.ObjectID)
}

func (s *Store) UpsertScorecardActivity(ctx context.Context, raw json.RawMessage) (*ScorecardActivityRecord, error) {
	payload, err := decodeScorecardActivity(raw)
	if err != nil {
		return nil, err
	}

	now := nowUTC()
	if _, err := s.db.ExecContext(
		ctx,
		`INSERT INTO scorecard_activity(
			answered_scorecard_id, scorecard_id, scorecard_name, call_id, call_started_at,
			reviewed_user_id, reviewer_user_id, editor_user_id, review_method, review_time,
			visibility_type, overall_score, average_score, answer_count,
			raw_json, raw_sha256, first_seen_at, updated_at
		)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(answered_scorecard_id) DO UPDATE SET
			scorecard_id = excluded.scorecard_id,
			scorecard_name = excluded.scorecard_name,
			call_id = excluded.call_id,
			call_started_at = excluded.call_started_at,
			reviewed_user_id = excluded.reviewed_user_id,
			reviewer_user_id = excluded.reviewer_user_id,
			editor_user_id = excluded.editor_user_id,
			review_method = excluded.review_method,
			review_time = excluded.review_time,
			visibility_type = excluded.visibility_type,
			overall_score = excluded.overall_score,
			average_score = excluded.average_score,
			answer_count = excluded.answer_count,
			raw_json = excluded.raw_json,
			raw_sha256 = excluded.raw_sha256,
			updated_at = excluded.updated_at
		WHERE
			scorecard_activity.scorecard_id IS NOT excluded.scorecard_id OR
			scorecard_activity.scorecard_name IS NOT excluded.scorecard_name OR
			scorecard_activity.call_id IS NOT excluded.call_id OR
			scorecard_activity.call_started_at IS NOT excluded.call_started_at OR
			scorecard_activity.reviewed_user_id IS NOT excluded.reviewed_user_id OR
			scorecard_activity.reviewer_user_id IS NOT excluded.reviewer_user_id OR
			scorecard_activity.editor_user_id IS NOT excluded.editor_user_id OR
			scorecard_activity.review_method IS NOT excluded.review_method OR
			scorecard_activity.review_time IS NOT excluded.review_time OR
			scorecard_activity.visibility_type IS NOT excluded.visibility_type OR
			scorecard_activity.overall_score IS NOT excluded.overall_score OR
			scorecard_activity.average_score IS NOT excluded.average_score OR
			scorecard_activity.answer_count IS NOT excluded.answer_count OR
			scorecard_activity.raw_sha256 IS NOT excluded.raw_sha256`,
		payload.AnsweredScorecardID,
		payload.ScorecardID,
		payload.ScorecardName,
		payload.CallID,
		payload.CallStartedAt,
		payload.ReviewedUserID,
		payload.ReviewerUserID,
		payload.EditorUserID,
		payload.ReviewMethod,
		payload.ReviewTime,
		payload.VisibilityType,
		payload.OverallScore,
		payload.AverageScore,
		payload.AnswerCount,
		payload.RawJSON,
		payload.RawSHA256,
		now,
		now,
	); err != nil {
		return nil, err
	}

	return s.scorecardActivityByID(ctx, payload.AnsweredScorecardID)
}

func (s *Store) GetCallRaw(ctx context.Context, callID string) (json.RawMessage, error) {
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return nil, errors.New("call id is required")
	}

	var raw []byte
	if err := s.db.QueryRowContext(ctx, `SELECT raw_json FROM calls WHERE call_id = ?`, callID).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("call %q not found", callID)
		}
		return nil, err
	}
	return json.RawMessage(raw), nil
}

func (s *Store) GetCallDetail(ctx context.Context, callID string) (*CallDetail, error) {
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return nil, errors.New("call id is required")
	}

	record, err := s.callByID(ctx, callID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("call %q not found", callID)
		}
		return nil, err
	}
	detail := &CallDetail{
		CallID:          record.CallID,
		Title:           record.Title,
		StartedAt:       record.StartedAt,
		DurationSeconds: record.DurationSeconds,
		PartiesCount:    record.PartiesCount,
	}

	rows, err := s.db.QueryContext(ctx, `
SELECT o.object_type,
       o.object_id,
       o.object_name,
       COUNT(f.id) AS field_count,
       COUNT(NULLIF(TRIM(f.field_value_text), '')) AS populated_field_count,
       COALESCE(GROUP_CONCAT(DISTINCT f.field_name), '') AS field_names
  FROM call_context_objects o
  LEFT JOIN call_context_fields f
    ON f.call_id = o.call_id
   AND f.object_key = o.object_key
 WHERE o.call_id = ?
 GROUP BY o.object_key, o.object_type, o.object_id, o.object_name
 ORDER BY o.object_type, o.object_id, o.object_key`, callID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var object CallDetailCRMObject
		var fieldNames string
		if err := rows.Scan(
			&object.ObjectType,
			&object.ObjectID,
			&object.ObjectName,
			&object.FieldCount,
			&object.PopulatedFieldCount,
			&fieldNames,
		); err != nil {
			return nil, err
		}
		object.FieldNames = splitAndSortCommaList(fieldNames)
		detail.CRMObjects = append(detail.CRMObjects, object)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return detail, nil
}

func (s *Store) SearchCallsRaw(ctx context.Context, params CallSearchParams) ([]json.RawMessage, error) {
	limit := boundedLimit(params.Limit, defaultCallSearchLimit, maxCallSearchLimit)

	query := `SELECT c.raw_json FROM calls c`
	var args []any
	var where []string

	objectType := strings.TrimSpace(params.CRMObjectType)
	objectID := strings.TrimSpace(params.CRMObjectID)
	if objectType != "" || objectID != "" {
		subquery := []string{`o.call_id = c.call_id`}
		if objectType != "" {
			subquery = append(subquery, `o.object_type = ?`)
			args = append(args, objectType)
		}
		if objectID != "" {
			subquery = append(subquery, `o.object_id = ?`)
			args = append(args, objectID)
		}
		where = append(where, `EXISTS (SELECT 1 FROM call_context_objects o WHERE `+strings.Join(subquery, ` AND `)+`)`)
	}
	factWhere, factArgs, err := callFactFilterWhere("cf", callFactFilterParams{
		FromDate:         params.FromDate,
		ToDate:           params.ToDate,
		LifecycleBucket:  params.LifecycleBucket,
		Scope:            params.Scope,
		System:           params.System,
		Direction:        params.Direction,
		TranscriptStatus: params.TranscriptStatus,
	}, true)
	if err != nil {
		return nil, err
	}
	if len(factWhere) > 0 {
		where = append(where, `EXISTS (SELECT 1 FROM call_facts cf WHERE cf.call_id = c.call_id AND `+strings.Join(factWhere, ` AND `)+`)`)
		args = append(args, factArgs...)
	}
	if len(where) > 0 {
		query += ` WHERE ` + strings.Join(where, ` AND `)
	}
	query += ` ORDER BY c.started_at DESC, c.call_id LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []json.RawMessage
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		results = append(results, json.RawMessage(raw))
	}
	return results, rows.Err()
}

func (s *Store) ListCRMObjectTypes(ctx context.Context) ([]CRMObjectTypeSummary, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT o.object_type,
       COUNT(DISTINCT o.id) AS object_count,
       COUNT(DISTINCT o.call_id) AS call_count,
       COUNT(f.id) AS field_count,
       COUNT(NULLIF(TRIM(f.field_value_text), '')) AS populated_field_count,
       COUNT(DISTINCT NULLIF(TRIM(o.object_id), '')) AS distinct_object_id_count
  FROM call_context_objects o
  LEFT JOIN call_context_fields f
    ON f.call_id = o.call_id
   AND f.object_key = o.object_key
 GROUP BY o.object_type
 ORDER BY object_count DESC, o.object_type`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []CRMObjectTypeSummary
	for rows.Next() {
		var row CRMObjectTypeSummary
		if err := rows.Scan(
			&row.ObjectType,
			&row.ObjectCount,
			&row.CallCount,
			&row.FieldCount,
			&row.PopulatedFieldCount,
			&row.DistinctObjectIDCount,
		); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) ListCRMFields(ctx context.Context, objectType string, limit int) ([]CRMFieldSummary, error) {
	objectType = strings.TrimSpace(objectType)
	if objectType == "" {
		return nil, errors.New("object type is required")
	}
	limit = boundedLimit(limit, defaultCRMFieldLimit, maxCRMFieldLimit)

	rows, err := s.db.QueryContext(ctx, `
SELECT o.object_type,
       f.field_name,
       MAX(f.field_label) AS field_label,
       COUNT(*) AS row_count,
       COUNT(DISTINCT f.call_id) AS call_count,
       COUNT(NULLIF(TRIM(f.field_value_text), '')) AS populated_count,
       COUNT(DISTINCT NULLIF(TRIM(f.field_value_text), '')) AS distinct_value_count
  FROM call_context_fields f
  JOIN call_context_objects o
    ON o.call_id = f.call_id
   AND o.object_key = f.object_key
 WHERE o.object_type = ?
 GROUP BY o.object_type, f.field_name
 ORDER BY row_count DESC, f.field_name
 LIMIT ?`, objectType, limit)
	if err != nil {
		return nil, err
	}

	var out []CRMFieldSummary
	for rows.Next() {
		var row CRMFieldSummary
		if err := rows.Scan(
			&row.ObjectType,
			&row.FieldName,
			&row.FieldLabel,
			&row.RowCount,
			&row.CallCount,
			&row.PopulatedCount,
			&row.DistinctValueCount,
		); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	return out, nil
}

func (s *Store) SearchCRMFieldValues(ctx context.Context, params CRMFieldValueSearchParams) ([]CRMFieldValueMatch, error) {
	objectType := strings.TrimSpace(params.ObjectType)
	fieldName := strings.TrimSpace(params.FieldName)
	valueQuery := strings.TrimSpace(params.ValueQuery)
	if objectType == "" {
		return nil, errors.New("object type is required")
	}
	if fieldName == "" {
		return nil, errors.New("field name is required")
	}
	if valueQuery == "" {
		return nil, errors.New("value query is required")
	}
	limit := boundedLimit(params.Limit, defaultCRMFieldValueLimit, maxCRMFieldValueLimit)

	rows, err := s.db.QueryContext(ctx, `
SELECT c.call_id,
       c.title,
       c.started_at,
       o.object_type,
       o.object_id,
       o.object_name,
       f.field_name,
       f.field_label,
       f.field_value_text
  FROM call_context_fields f
  JOIN call_context_objects o
    ON o.call_id = f.call_id
   AND o.object_key = f.object_key
  JOIN calls c
    ON c.call_id = f.call_id
 WHERE o.object_type = ?
   AND f.field_name = ?
   AND LOWER(f.field_value_text) LIKE '%' || LOWER(?) || '%'
 ORDER BY c.started_at DESC, c.call_id
 LIMIT ?`, objectType, fieldName, valueQuery, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []CRMFieldValueMatch
	for rows.Next() {
		var row CRMFieldValueMatch
		var value string
		if err := rows.Scan(
			&row.CallID,
			&row.Title,
			&row.StartedAt,
			&row.ObjectType,
			&row.ObjectID,
			&row.ObjectName,
			&row.FieldName,
			&row.FieldLabel,
			&value,
		); err != nil {
			return nil, err
		}
		if params.IncludeValueSnippet {
			row.ValueSnippet = truncateString(value, 240)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) ListCRMIntegrations(ctx context.Context) ([]CRMIntegrationRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT integration_id, name, provider, updated_at
  FROM crm_integrations
 ORDER BY provider, name, integration_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []CRMIntegrationRecord
	for rows.Next() {
		var row CRMIntegrationRecord
		if err := rows.Scan(&row.IntegrationID, &row.Name, &row.Provider, &row.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) ListCRMSchemaObjects(ctx context.Context, integrationID string) ([]CRMSchemaObjectRecord, error) {
	integrationID = strings.TrimSpace(integrationID)
	query := `SELECT integration_id, object_type, display_name, field_count, updated_at FROM crm_schema_objects`
	var args []any
	if integrationID != "" {
		query += ` WHERE integration_id = ?`
		args = append(args, integrationID)
	}
	query += ` ORDER BY integration_id, object_type`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []CRMSchemaObjectRecord
	for rows.Next() {
		var row CRMSchemaObjectRecord
		if err := rows.Scan(&row.IntegrationID, &row.ObjectType, &row.DisplayName, &row.FieldCount, &row.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) ListCRMSchemaFields(ctx context.Context, params CRMSchemaFieldListParams) ([]CRMSchemaFieldRecord, error) {
	limit := boundedLimit(params.Limit, defaultInventoryLimit, maxInventoryLimit)
	where := []string{}
	args := []any{}
	if value := strings.TrimSpace(params.IntegrationID); value != "" {
		where = append(where, `integration_id = ?`)
		args = append(args, value)
	}
	if value := strings.TrimSpace(params.ObjectType); value != "" {
		where = append(where, `object_type = ?`)
		args = append(args, value)
	}

	query := `SELECT integration_id, object_type, field_name, field_label, field_type, updated_at FROM crm_schema_fields`
	if len(where) > 0 {
		query += ` WHERE ` + strings.Join(where, ` AND `)
	}
	query += ` ORDER BY integration_id, object_type, field_name LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []CRMSchemaFieldRecord
	for rows.Next() {
		var row CRMSchemaFieldRecord
		if err := rows.Scan(&row.IntegrationID, &row.ObjectType, &row.FieldName, &row.FieldLabel, &row.FieldType, &row.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) ListGongSettings(ctx context.Context, params GongSettingListParams) ([]GongSettingRecord, error) {
	limit := boundedLimit(params.Limit, defaultInventoryLimit, maxInventoryLimit)
	query := `SELECT kind, object_id, name, active, updated_at FROM gong_settings`
	args := []any{}
	if value := strings.TrimSpace(params.Kind); value != "" {
		query += ` WHERE kind = ?`
		args = append(args, normalizeGongSettingKind(value))
	}
	query += ` ORDER BY kind, name, object_id LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []GongSettingRecord
	for rows.Next() {
		var row GongSettingRecord
		var active int
		if err := rows.Scan(&row.Kind, &row.ObjectID, &row.Name, &active, &row.UpdatedAt); err != nil {
			return nil, err
		}
		row.Active = active == 1
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) ListScorecards(ctx context.Context, params ScorecardListParams) ([]ScorecardSummary, error) {
	limit := boundedLimit(params.Limit, defaultInventoryLimit, maxInventoryLimit)
	query := `SELECT object_id, name, active, raw_json, updated_at FROM gong_settings WHERE kind = 'scorecards'`
	args := []any{}
	if params.ActiveOnly {
		query += ` AND active = 1`
	}
	query += ` ORDER BY active DESC, name, object_id LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ScorecardSummary
	for rows.Next() {
		var objectID, name, cachedUpdatedAt string
		var active int
		var raw []byte
		if err := rows.Scan(&objectID, &name, &active, &raw, &cachedUpdatedAt); err != nil {
			return nil, err
		}
		summary, err := decodeScorecardSummary(raw, cachedUpdatedAt)
		if err != nil {
			return nil, err
		}
		if summary.ScorecardID == "" {
			summary.ScorecardID = objectID
		}
		if summary.Name == "" {
			summary.Name = name
		}
		if !summary.Active {
			summary.Active = active == 1
		}
		out = append(out, summary)
	}
	return out, rows.Err()
}

func (s *Store) GetScorecardDetail(ctx context.Context, scorecardID string) (*ScorecardDetail, error) {
	scorecardID = strings.TrimSpace(scorecardID)
	if scorecardID == "" {
		return nil, errors.New("scorecard id is required")
	}

	var raw []byte
	var cachedUpdatedAt string
	err := s.db.QueryRowContext(
		ctx,
		`SELECT raw_json, updated_at
		   FROM gong_settings
		  WHERE kind = 'scorecards' AND object_id = ?`,
		scorecardID,
	).Scan(&raw, &cachedUpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("scorecard %q not found", scorecardID)
	}
	if err != nil {
		return nil, err
	}
	return decodeScorecardDetail(raw, cachedUpdatedAt)
}

func (s *Store) scorecardActivityByID(ctx context.Context, answeredScorecardID string) (*ScorecardActivityRecord, error) {
	var row ScorecardActivityRecord
	if err := s.db.QueryRowContext(ctx, `
SELECT answered_scorecard_id, scorecard_id, scorecard_name, call_id, call_started_at,
       reviewed_user_id, reviewer_user_id, editor_user_id, review_method, review_time,
       visibility_type, overall_score, average_score, answer_count, raw_sha256, updated_at
  FROM scorecard_activity
 WHERE answered_scorecard_id = ?`, answeredScorecardID).Scan(
		&row.AnsweredScorecardID,
		&row.ScorecardID,
		&row.ScorecardName,
		&row.CallID,
		&row.CallStartedAt,
		&row.ReviewedUserID,
		&row.ReviewerUserID,
		&row.EditorUserID,
		&row.ReviewMethod,
		&row.ReviewTime,
		&row.VisibilityType,
		&row.OverallScore,
		&row.AverageScore,
		&row.AnswerCount,
		&row.RawSHA256,
		&row.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &row, nil
}

func (s *Store) SummarizeScorecardActivity(ctx context.Context, params ScorecardActivitySummaryParams) ([]ScorecardActivitySummaryRow, error) {
	groupBy, column, err := scorecardActivityGroupColumn(params.GroupBy)
	if err != nil {
		return nil, err
	}
	limit := boundedLimit(params.Limit, defaultCallFactsLimit, maxCallFactsLimit)
	groupExpr := `COALESCE(NULLIF(TRIM(` + column + `), ''), 'unknown')`
	if groupBy == "scorecard" {
		groupExpr = `COALESCE(NULLIF(TRIM(sa.scorecard_name), ''), 'unknown')`
	}

	query := `
SELECT '` + groupBy + `' AS group_by,
       ` + groupExpr + ` AS group_value,
       COUNT(*) AS answered_scorecard_count,
       COUNT(DISTINCT NULLIF(sa.call_id, '')) AS distinct_call_count,
       COUNT(DISTINCT c.call_id) AS linked_call_count,
       COUNT(DISTINCT NULLIF(sa.reviewed_user_id, '')) AS reviewed_user_count,
       COALESCE(SUM(CASE WHEN sa.review_method = 'MANUAL' THEN 1 ELSE 0 END), 0) AS manual_count,
       COALESCE(SUM(CASE WHEN sa.review_method = 'AUTOMATIC' THEN 1 ELSE 0 END), 0) AS automatic_count,
       COUNT(DISTINCT CASE WHEN cf.transcript_status = 'present' THEN sa.call_id END) AS transcript_count,
       COUNT(DISTINCT CASE WHEN cf.transcript_status = 'missing' THEN sa.call_id END) AS missing_transcript_count,
       COALESCE(AVG(NULLIF(sa.overall_score, 0)), 0) AS average_overall_score,
       COALESCE(AVG(NULLIF(sa.average_score, 0)), 0) AS average_answer_score,
       COALESCE(MAX(sa.review_time), '') AS latest_review_time
  FROM scorecard_activity sa
  LEFT JOIN calls c
    ON c.call_id = sa.call_id
  LEFT JOIN call_facts cf
    ON cf.call_id = sa.call_id
 GROUP BY group_value
 ORDER BY answered_scorecard_count DESC, group_value
 LIMIT ?`

	rows, err := s.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ScorecardActivitySummaryRow
	for rows.Next() {
		var row ScorecardActivitySummaryRow
		if err := rows.Scan(
			&row.GroupBy,
			&row.GroupValue,
			&row.AnsweredScorecardCount,
			&row.DistinctCallCount,
			&row.LinkedCallCount,
			&row.ReviewedUserCount,
			&row.ManualCount,
			&row.AutomaticCount,
			&row.TranscriptCount,
			&row.MissingTranscriptCount,
			&row.AverageOverallScore,
			&row.AverageAnswerScore,
			&row.LatestReviewTime,
		); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) ScorecardActivityOverview(ctx context.Context, limit int) (*ScorecardActivityOverview, error) {
	limit = boundedLimit(limit, defaultCallFactsLimit, maxCallFactsLimit)
	report := &ScorecardActivityOverview{}
	if err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*) AS total_answered_scorecards,
       COUNT(DISTINCT NULLIF(sa.scorecard_id, '')) AS distinct_scorecards,
       COUNT(DISTINCT NULLIF(sa.call_id, '')) AS distinct_calls,
       COUNT(DISTINCT c.call_id) AS linked_call_count,
       COUNT(DISTINCT NULLIF(sa.reviewed_user_id, '')) AS reviewed_user_count,
       COALESCE(SUM(CASE WHEN sa.review_method = 'MANUAL' THEN 1 ELSE 0 END), 0) AS manual_count,
       COALESCE(SUM(CASE WHEN sa.review_method = 'AUTOMATIC' THEN 1 ELSE 0 END), 0) AS automatic_count,
       COUNT(DISTINCT CASE WHEN cf.transcript_status = 'present' THEN sa.call_id END) AS transcript_count,
       COUNT(DISTINCT CASE WHEN cf.transcript_status = 'missing' THEN sa.call_id END) AS missing_transcript_count,
       COALESCE(AVG(NULLIF(sa.overall_score, 0)), 0) AS average_overall_score,
       COALESCE(AVG(NULLIF(sa.average_score, 0)), 0) AS average_answer_score,
       COALESCE(MAX(sa.review_time), '') AS latest_review_time
  FROM scorecard_activity sa
  LEFT JOIN calls c
    ON c.call_id = sa.call_id
  LEFT JOIN call_facts cf
    ON cf.call_id = sa.call_id`).Scan(
		&report.TotalAnsweredScorecards,
		&report.DistinctScorecards,
		&report.DistinctCalls,
		&report.LinkedCallCount,
		&report.ReviewedUserCount,
		&report.ManualCount,
		&report.AutomaticCount,
		&report.TranscriptCount,
		&report.MissingTranscriptCount,
		&report.AverageOverallScore,
		&report.AverageAnswerScore,
		&report.LatestReviewTime,
	); err != nil {
		return nil, err
	}

	var err error
	if report.ByScorecard, err = s.SummarizeScorecardActivity(ctx, ScorecardActivitySummaryParams{GroupBy: "scorecard", Limit: limit}); err != nil {
		return nil, err
	}
	if report.ByReviewMethod, err = s.SummarizeScorecardActivity(ctx, ScorecardActivitySummaryParams{GroupBy: "review_method", Limit: limit}); err != nil {
		return nil, err
	}
	if report.ByLifecycle, err = s.SummarizeScorecardActivity(ctx, ScorecardActivitySummaryParams{GroupBy: "lifecycle", Limit: limit}); err != nil {
		return nil, err
	}
	if report.ByTranscriptStatus, err = s.SummarizeScorecardActivity(ctx, ScorecardActivitySummaryParams{GroupBy: "transcript_status", Limit: limit}); err != nil {
		return nil, err
	}
	return report, nil
}

func (s *Store) AnalyzeLateStageSignals(ctx context.Context, params LateStageSignalParams) (*LateStageSignalsReport, error) {
	objectType := strings.TrimSpace(params.ObjectType)
	if objectType == "" {
		objectType = "Opportunity"
	}
	stageField := strings.TrimSpace(params.StageField)
	if stageField == "" {
		stageField = "StageName"
	}
	lateValues := cleanStringList(params.LateStageValues)
	if len(lateValues) == 0 {
		lateValues = []string{"Demo & Business Case", "Business Case", "SOW & Proposal", "Contract Review", "Contract Signing", "Crucible/Last Mile", "Closed Won"}
	}
	limit := boundedLimit(params.Limit, defaultLateStageSignalLimit, maxLateStageSignalLimit)

	stageCounts, lateCalls, nonLateCalls, err := s.stageDistribution(ctx, objectType, stageField, lateValues)
	if err != nil {
		return nil, err
	}
	totalCalls := lateCalls + nonLateCalls
	if totalCalls == 0 {
		return &LateStageSignalsReport{
			ObjectType:      objectType,
			StageField:      stageField,
			LateStageValues: lateValues,
			StageCounts:     stageCounts,
		}, nil
	}

	proxies := map[string]struct{}{
		strings.ToLower(stageField):       {},
		"probability":                     {},
		"forecast_category_vp__c":         {},
		"forecast_category_ae__c":         {},
		"forecastcategory":                {},
		"forecast_category":               {},
		"forecast_category_name":          {},
		"forecast_category_name__c":       {},
		"forecastcategoryname":            {},
		"forecast_category_vp_formula__c": {},
		"forecast_category_ae_formula__c": {},
		"forecast_category_formula__c":    {},
	}

	lateSet := normalizedStringSet(lateValues)
	normalizedLateValues := make([]string, 0, len(lateSet))
	for value := range lateSet {
		normalizedLateValues = append(normalizedLateValues, value)
	}
	sort.Strings(normalizedLateValues)

	placeholders := strings.TrimRight(strings.Repeat("?,", len(normalizedLateValues)), ",")
	args := make([]any, 0, len(normalizedLateValues)+3)
	for _, value := range normalizedLateValues {
		args = append(args, value)
	}
	args = append(args, objectType, stageField, objectType)

	rows, err := s.db.QueryContext(ctx, `
	WITH classified AS (
		SELECT f.call_id,
		       MAX(CASE WHEN LOWER(TRIM(f.field_value_text)) IN (`+placeholders+`) THEN 1 ELSE 0 END) AS is_late
		  FROM call_context_fields f
		  JOIN call_context_objects o
		    ON o.call_id = f.call_id
		   AND o.object_key = f.object_key
		 WHERE o.object_type = ?
		   AND f.field_name = ?
		   AND TRIM(f.field_value_text) <> ''
		 GROUP BY f.call_id
	),
field_presence AS (
	SELECT DISTINCT f.call_id,
	       c.is_late,
	       f.field_name,
	       f.field_label
	  FROM call_context_fields f
	  JOIN call_context_objects o
	    ON o.call_id = f.call_id
	   AND o.object_key = f.object_key
	  JOIN classified c
	    ON c.call_id = f.call_id
	 WHERE o.object_type = ?
	   AND TRIM(f.field_value_text) <> ''
)
SELECT field_name,
       MAX(field_label) AS field_label,
       COUNT(DISTINCT CASE WHEN is_late = 1 THEN call_id END) AS late_calls,
       COUNT(DISTINCT CASE WHEN is_late = 0 THEN call_id END) AS non_late_calls
  FROM field_presence
 GROUP BY field_name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	signals := make([]LateStageSignal, 0)
	for rows.Next() {
		var row LateStageSignal
		if err := rows.Scan(&row.FieldName, &row.FieldLabel, &row.LatePopulatedCalls, &row.NonLatePopulatedCalls); err != nil {
			return nil, err
		}
		if !params.IncludeStageProxies {
			if _, ok := proxies[strings.ToLower(row.FieldName)]; ok {
				continue
			}
		}
		row.LateRate = rate(row.LatePopulatedCalls, lateCalls)
		row.NonLateRate = rate(row.NonLatePopulatedCalls, nonLateCalls)
		row.Lift = row.LateRate - row.NonLateRate
		signals = append(signals, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sortLateStageSignals(signals)
	if limit < len(signals) {
		signals = signals[:limit]
	}

	return &LateStageSignalsReport{
		ObjectType:      objectType,
		StageField:      stageField,
		LateStageValues: lateValues,
		TotalCalls:      totalCalls,
		LateCalls:       lateCalls,
		NonLateCalls:    nonLateCalls,
		StageCounts:     stageCounts,
		Signals:         signals,
	}, nil
}

func (s *Store) SummarizeOpportunityCalls(ctx context.Context, params OpportunityCallSummaryParams) ([]OpportunityCallSummary, error) {
	limit := boundedLimit(params.Limit, defaultOpportunitySummaryLimit, maxOpportunitySummaryLimit)
	stageValues := cleanStringList(params.StageValues)

	query := `
WITH opportunity_calls AS (
	SELECT o.call_id,
	       o.object_key,
	       o.object_id,
	       o.object_name,
	       c.started_at,
	       c.duration_seconds,
	       t.call_id AS transcript_call_id,
	       MAX(CASE WHEN f.field_name = 'StageName' THEN TRIM(f.field_value_text) ELSE '' END) AS stage,
	       MAX(CASE WHEN f.field_name IN ('Amount', 'amount') THEN TRIM(f.field_value_text) ELSE '' END) AS amount,
	       MAX(CASE WHEN f.field_name IN ('CloseDate', 'closeDate', 'Close_Date__c') THEN TRIM(f.field_value_text) ELSE '' END) AS close_date,
	       MAX(CASE WHEN f.field_name IN ('OwnerId', 'ownerId', 'OwnerID') THEN TRIM(f.field_value_text) ELSE '' END) AS owner_id
	  FROM call_context_objects o
	  JOIN calls c
	    ON c.call_id = o.call_id
	  LEFT JOIN transcripts t
	    ON t.call_id = o.call_id
	  LEFT JOIN call_context_fields f
	    ON f.call_id = o.call_id
	   AND f.object_key = o.object_key
	 WHERE o.object_type = 'Opportunity'
	   AND TRIM(o.object_id) <> ''
	 GROUP BY o.call_id, o.object_key, o.object_id, o.object_name, c.started_at, c.duration_seconds, t.call_id
)`
	args := make([]any, 0, len(stageValues)+1)
	var where []string
	if len(stageValues) > 0 {
		placeholders := strings.TrimRight(strings.Repeat("?,", len(stageValues)), ",")
		where = append(where, `LOWER(TRIM(stage)) IN (`+placeholders+`)`)
		for _, value := range stageValues {
			args = append(args, strings.ToLower(strings.TrimSpace(value)))
		}
	}
	query += `
,
filtered_opportunity_calls AS (
	SELECT *
	  FROM opportunity_calls`
	if len(where) > 0 {
		query += `
	 WHERE ` + strings.Join(where, ` AND `)
	}
	query += `
)
SELECT object_id,
       COALESCE((SELECT oc2.object_name
                   FROM filtered_opportunity_calls oc2
                  WHERE oc2.object_id = foc.object_id
                  ORDER BY oc2.started_at DESC, oc2.call_id
                  LIMIT 1), '') AS object_name,
       COALESCE((SELECT oc2.stage
                   FROM filtered_opportunity_calls oc2
                  WHERE oc2.object_id = foc.object_id
                  ORDER BY oc2.started_at DESC, oc2.call_id
                  LIMIT 1), '') AS stage,
       COALESCE((SELECT oc2.amount
                   FROM filtered_opportunity_calls oc2
                  WHERE oc2.object_id = foc.object_id
                  ORDER BY oc2.started_at DESC, oc2.call_id
                  LIMIT 1), '') AS amount,
       COALESCE((SELECT oc2.close_date
                   FROM filtered_opportunity_calls oc2
                  WHERE oc2.object_id = foc.object_id
                  ORDER BY oc2.started_at DESC, oc2.call_id
                  LIMIT 1), '') AS close_date,
       COALESCE((SELECT oc2.owner_id
                   FROM filtered_opportunity_calls oc2
                  WHERE oc2.object_id = foc.object_id
                  ORDER BY oc2.started_at DESC, oc2.call_id
                  LIMIT 1), '') AS owner_id,
       COUNT(DISTINCT call_id) AS call_count,
       COUNT(DISTINCT CASE WHEN transcript_call_id IS NOT NULL THEN call_id END) AS transcript_count,
       COUNT(DISTINCT CASE WHEN transcript_call_id IS NULL THEN call_id END) AS missing_transcript_count,
       SUM(duration_seconds) AS total_duration_seconds,
       MAX(started_at) AS latest_call_at,
       COALESCE((SELECT oc2.call_id
                   FROM filtered_opportunity_calls oc2
                  WHERE oc2.object_id = foc.object_id
                  ORDER BY oc2.started_at DESC, oc2.call_id
                  LIMIT 1), '') AS latest_call_id
  FROM filtered_opportunity_calls foc
 GROUP BY object_id
 ORDER BY call_count DESC, latest_call_at DESC
 LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []OpportunityCallSummary
	for rows.Next() {
		var row OpportunityCallSummary
		if err := rows.Scan(
			&row.OpportunityID,
			&row.OpportunityName,
			&row.Stage,
			&row.Amount,
			&row.CloseDate,
			&row.OwnerID,
			&row.CallCount,
			&row.TranscriptCount,
			&row.MissingTranscriptCount,
			&row.TotalDurationSeconds,
			&row.LatestCallAt,
			&row.LatestCallID,
		); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) CRMFieldPopulationMatrix(ctx context.Context, params CRMFieldPopulationMatrixParams) (*CRMFieldPopulationMatrix, error) {
	objectType := strings.TrimSpace(params.ObjectType)
	if objectType == "" {
		return nil, errors.New("object type is required")
	}
	groupByField := strings.TrimSpace(params.GroupByField)
	if groupByField == "" {
		groupByField = "StageName"
	}
	canonicalGroupByField, ok := safeCRMMatrixGroupField(groupByField)
	if !ok {
		return nil, fmt.Errorf("group_by_field %q is not allowed for MCP-safe aggregate grouping", groupByField)
	}
	groupByField = canonicalGroupByField
	limit := boundedLimit(params.Limit, defaultCRMMatrixLimit, maxCRMMatrixLimit)

	rows, err := s.db.QueryContext(ctx, `
WITH groups AS (
	SELECT o.call_id,
	       o.object_key,
	       TRIM(g.field_value_text) AS group_value
	  FROM call_context_objects o
	  JOIN call_context_fields g
	    ON g.call_id = o.call_id
	   AND g.object_key = o.object_key
	   AND g.field_name = ?
	 WHERE o.object_type = ?
	   AND TRIM(g.field_value_text) <> ''
),
group_sizes AS (
	SELECT group_value,
	       COUNT(DISTINCT call_id || ':' || object_key) AS object_count,
	       COUNT(DISTINCT call_id) AS call_count
	  FROM groups
	 GROUP BY group_value
),
field_counts AS (
	SELECT g.group_value,
	       f.field_name,
	       MAX(f.field_label) AS field_label,
	       COUNT(DISTINCT g.call_id || ':' || g.object_key) AS object_count,
	       COUNT(DISTINCT g.call_id) AS call_count,
	       COUNT(DISTINCT CASE WHEN TRIM(f.field_value_text) <> '' THEN g.call_id || ':' || g.object_key END) AS populated_count
	  FROM groups g
	  JOIN call_context_fields f
	    ON f.call_id = g.call_id
	   AND f.object_key = g.object_key
	 WHERE f.field_name <> ?
	 GROUP BY g.group_value, f.field_name
)
SELECT fc.group_value,
       fc.field_name,
       fc.field_label,
       gs.object_count,
       gs.call_count,
       fc.populated_count
  FROM field_counts fc
  JOIN group_sizes gs
    ON gs.group_value = fc.group_value
 ORDER BY fc.populated_count DESC, fc.group_value, fc.field_name
 LIMIT ?`, groupByField, objectType, groupByField, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	report := &CRMFieldPopulationMatrix{
		ObjectType:   objectType,
		GroupByField: groupByField,
	}
	for rows.Next() {
		var cell CRMFieldPopulationCell
		if err := rows.Scan(
			&cell.GroupValue,
			&cell.FieldName,
			&cell.FieldLabel,
			&cell.ObjectCount,
			&cell.CallCount,
			&cell.PopulatedCount,
		); err != nil {
			return nil, err
		}
		cell.PopulationRate = rate(cell.PopulatedCount, cell.ObjectCount)
		report.Cells = append(report.Cells, cell)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return report, nil
}

func safeCRMMatrixGroupField(fieldName string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(fieldName)) {
	case "stagename":
		return "StageName", true
	case "account_type__c":
		return "Account_Type__c", true
	case "industry":
		return "Industry", true
	case "forecast_category_vp__c":
		return "Forecast_Category_VP__c", true
	case "forecast_category_ae__c":
		return "Forecast_Category_AE__c", true
	case "revenue_range_f__c":
		return "Revenue_Range_f__c", true
	default:
		return "", false
	}
}

func splitAndSortCommaList(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		out = append(out, part)
	}
	sort.Strings(out)
	return out
}

func (s *Store) configure(ctx context.Context) error {
	pragmas := []string{
		`PRAGMA foreign_keys = ON`,
		`PRAGMA busy_timeout = 5000`,
	}
	for _, pragma := range pragmas {
		if _, err := s.db.ExecContext(ctx, pragma); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) configureReadOnly(ctx context.Context) error {
	pragmas := []string{
		`PRAGMA foreign_keys = ON`,
		`PRAGMA busy_timeout = 5000`,
		`PRAGMA query_only = ON`,
	}
	for _, pragma := range pragmas {
		if _, err := s.db.ExecContext(ctx, pragma); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) validateSchema(ctx context.Context) error {
	version, err := s.userVersion(ctx)
	if err != nil {
		return err
	}
	if version < len(migrations) {
		return fmt.Errorf("sqlite schema version %d is older than required version %d; run a sync command with gongctl first", version, len(migrations))
	}
	return nil
}

func sqliteFileURI(path string, values url.Values) string {
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		absolutePath = path
	}
	uri := url.URL{Scheme: "file", Path: absolutePath}
	uri.RawQuery = values.Encode()
	return uri.String()
}

func (s *Store) userVersion(ctx context.Context) (int, error) {
	var version int
	if err := s.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return 0, err
	}
	return version, nil
}

func (s *Store) latestSyncRun(ctx context.Context, query string) (*SyncRun, error) {
	row := s.db.QueryRowContext(ctx, query)
	var run SyncRun
	var finishedAt sql.NullString
	err := row.Scan(
		&run.ID,
		&run.Scope,
		&run.SyncKey,
		&run.Cursor,
		&run.From,
		&run.To,
		&run.RequestContext,
		&run.Status,
		&run.StartedAt,
		&finishedAt,
		&run.RecordsSeen,
		&run.RecordsWritten,
		&run.ErrorText,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if finishedAt.Valid {
		run.FinishedAt = finishedAt.String
	}
	return &run, nil
}

func (s *Store) callByID(ctx context.Context, callID string) (*CallRecord, error) {
	var record CallRecord
	var contextPresent int
	err := s.db.QueryRowContext(
		ctx,
		`SELECT call_id, title, started_at, duration_seconds, parties_count, context_present, raw_json, raw_sha256, first_seen_at, updated_at
		   FROM calls WHERE call_id = ?`,
		callID,
	).Scan(
		&record.CallID,
		&record.Title,
		&record.StartedAt,
		&record.DurationSeconds,
		&record.PartiesCount,
		&contextPresent,
		&record.RawJSON,
		&record.RawSHA256,
		&record.FirstSeenAt,
		&record.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	record.ContextPresent = contextPresent == 1
	return &record, nil
}

func (s *Store) userByID(ctx context.Context, userID string) (*UserRecord, error) {
	var record UserRecord
	var active int
	err := s.db.QueryRowContext(
		ctx,
		`SELECT user_id, email, first_name, last_name, display_name, title, active, raw_json, raw_sha256, first_seen_at, updated_at
		   FROM users WHERE user_id = ?`,
		userID,
	).Scan(
		&record.UserID,
		&record.Email,
		&record.FirstName,
		&record.LastName,
		&record.DisplayName,
		&record.Title,
		&active,
		&record.RawJSON,
		&record.RawSHA256,
		&record.FirstSeenAt,
		&record.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	record.Active = active == 1
	return &record, nil
}

func (s *Store) crmIntegrationByID(ctx context.Context, integrationID string) (*CRMIntegrationRecord, error) {
	var record CRMIntegrationRecord
	err := s.db.QueryRowContext(
		ctx,
		`SELECT integration_id, name, provider, updated_at
		   FROM crm_integrations
		  WHERE integration_id = ?`,
		integrationID,
	).Scan(
		&record.IntegrationID,
		&record.Name,
		&record.Provider,
		&record.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &record, nil
}

func (s *Store) gongSettingByID(ctx context.Context, kind string, objectID string) (*GongSettingRecord, error) {
	var record GongSettingRecord
	var active int
	err := s.db.QueryRowContext(
		ctx,
		`SELECT kind, object_id, name, active, updated_at
		   FROM gong_settings
		  WHERE kind = ? AND object_id = ?`,
		normalizeGongSettingKind(kind),
		objectID,
	).Scan(
		&record.Kind,
		&record.ObjectID,
		&record.Name,
		&active,
		&record.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	record.Active = active == 1
	return &record, nil
}

func replaceContextRowsTx(ctx context.Context, tx *sql.Tx, callID string, objects []contextObjectRow) (ContextCounts, error) {
	if _, err := tx.ExecContext(ctx, `DELETE FROM call_context_fields WHERE call_id = ?`, callID); err != nil {
		return ContextCounts{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM call_context_objects WHERE call_id = ?`, callID); err != nil {
		return ContextCounts{}, err
	}

	counts := ContextCounts{}
	for _, object := range objects {
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO call_context_objects(call_id, object_key, object_type, object_id, object_name, raw_json)
			 VALUES(?, ?, ?, ?, ?, ?)`,
			callID,
			object.ObjectKey,
			object.ObjectType,
			object.ObjectID,
			object.ObjectName,
			object.RawJSON,
		); err != nil {
			return ContextCounts{}, err
		}
		counts.Objects++
		for _, field := range object.Fields {
			if _, err := tx.ExecContext(
				ctx,
				`INSERT INTO call_context_fields(call_id, object_key, field_name, field_label, field_type, field_value_text, raw_json)
				 VALUES(?, ?, ?, ?, ?, ?, ?)`,
				callID,
				object.ObjectKey,
				field.FieldName,
				field.FieldLabel,
				field.FieldType,
				field.ValueText,
				field.RawJSON,
			); err != nil {
				return ContextCounts{}, err
			}
			counts.Fields++
		}
	}
	return counts, nil
}

func decodeCall(raw json.RawMessage) (*callPayload, error) {
	normalized, err := normalizeJSON(raw)
	if err != nil {
		return nil, err
	}

	var doc map[string]any
	if err := json.Unmarshal(normalized, &doc); err != nil {
		return nil, err
	}

	metaData := mapFromAny(doc["metaData"])
	callID := firstString(doc, "id", "callId")
	if callID == "" {
		callID = firstString(metaData, "id", "callId")
	}
	if callID == "" {
		return nil, errors.New("call payload missing id")
	}

	objects, hasContext, err := extractContextObjects(normalized)
	if err != nil {
		return nil, err
	}

	partiesCount := int64(0)
	if parties, ok := doc["parties"].([]any); ok {
		partiesCount = int64(len(parties))
	}
	if partiesCount == 0 {
		if parties, ok := metaData["parties"].([]any); ok {
			partiesCount = int64(len(parties))
		}
	}

	title := firstString(doc, "title")
	if title == "" {
		title = firstString(metaData, "title")
	}
	startedAt := firstString(doc, "started", "startedAt")
	if startedAt == "" {
		startedAt = firstString(metaData, "started", "startedAt")
	}
	durationSeconds := int64FromAny(doc["duration"])
	if durationSeconds == 0 {
		durationSeconds = int64FromAny(metaData["duration"])
	}

	return &callPayload{
		CallID:          callID,
		Title:           title,
		StartedAt:       startedAt,
		DurationSeconds: durationSeconds,
		PartiesCount:    partiesCount,
		ContextPresent:  len(objects) > 0,
		RawJSON:         normalized,
		RawSHA256:       sha256Hex(normalized),
		ContextObjects:  objects,
		HasContextBlock: hasContext,
	}, nil
}

func decodeUser(raw json.RawMessage) (*userPayload, error) {
	normalized, err := normalizeJSON(raw)
	if err != nil {
		return nil, err
	}

	var doc map[string]any
	if err := json.Unmarshal(normalized, &doc); err != nil {
		return nil, err
	}

	userID := firstString(doc, "id", "userId")
	if userID == "" {
		return nil, errors.New("user payload missing id")
	}

	firstName := firstString(doc, "firstName", "first_name")
	lastName := firstString(doc, "lastName", "last_name")
	displayName := firstString(doc, "name", "displayName", "display_name")
	if displayName == "" {
		displayName = strings.TrimSpace(strings.TrimSpace(firstName) + " " + strings.TrimSpace(lastName))
	}

	return &userPayload{
		UserID:      userID,
		Email:       firstString(doc, "emailAddress", "email"),
		FirstName:   firstName,
		LastName:    lastName,
		DisplayName: displayName,
		Title:       firstString(doc, "title"),
		Active:      boolFromAny(doc["active"]),
		RawJSON:     normalized,
		RawSHA256:   sha256Hex(normalized),
	}, nil
}

func decodeCRMIntegration(raw json.RawMessage) (*crmIntegrationPayload, error) {
	normalized, err := normalizeJSON(raw)
	if err != nil {
		return nil, err
	}

	var doc map[string]any
	if err := json.Unmarshal(normalized, &doc); err != nil {
		return nil, err
	}

	integrationID := firstString(doc, "integrationId", "crmIntegrationId", "id")
	if integrationID == "" {
		return nil, errors.New("CRM integration payload missing integration id")
	}

	return &crmIntegrationPayload{
		IntegrationID: integrationID,
		Name:          firstString(doc, "name", "displayName", "crmName"),
		Provider:      firstString(doc, "provider", "crmType", "type", "integrationType"),
		RawJSON:       normalized,
		RawSHA256:     sha256Hex(normalized),
	}, nil
}

func decodeGongSetting(kind string, raw json.RawMessage) (*gongSettingPayload, error) {
	normalized, err := normalizeJSON(raw)
	if err != nil {
		return nil, err
	}

	var doc map[string]any
	if err := json.Unmarshal(normalized, &doc); err != nil {
		return nil, err
	}

	kind = normalizeGongSettingKind(kind)
	if kind == "" {
		return nil, errors.New("settings kind is required")
	}

	name := firstString(doc, "name", "title", "displayName", "label", "scorecardName", "trackerName", "workspaceName")
	objectID := firstString(doc, "id", "trackerId", "keywordTrackerId", "scorecardId", "workspaceId")
	if objectID == "" {
		if name != "" {
			objectID = kind + ":" + name
		} else {
			objectID = kind + ":sha256:" + sha256Hex(normalized)[:16]
		}
	}

	active := false
	for _, key := range []string{"active", "enabled", "isActive", "status"} {
		if value, ok := doc[key]; ok {
			active = boolFromAny(value)
			break
		}
	}

	return &gongSettingPayload{
		Kind:      kind,
		ObjectID:  objectID,
		Name:      name,
		Active:    active,
		RawJSON:   normalized,
		RawSHA256: sha256Hex(normalized),
	}, nil
}

func decodeScorecardSummary(raw json.RawMessage, cachedUpdatedAt string) (ScorecardSummary, error) {
	normalized, err := normalizeJSON(raw)
	if err != nil {
		return ScorecardSummary{}, err
	}

	var doc map[string]any
	if err := json.Unmarshal(normalized, &doc); err != nil {
		return ScorecardSummary{}, err
	}

	questions := arrayFromAny(doc["questions"])
	return ScorecardSummary{
		ScorecardID:     firstString(doc, "scorecardId", "id"),
		Name:            firstString(doc, "scorecardName", "name", "title", "displayName"),
		Active:          boolFromAny(doc["enabled"]) || boolFromAny(doc["active"]),
		ReviewMethod:    firstString(doc, "reviewMethod"),
		WorkspaceID:     firstString(doc, "workspaceId"),
		QuestionCount:   int64(len(questions)),
		SourceCreatedAt: firstString(doc, "created", "createdAt"),
		SourceUpdatedAt: firstString(doc, "updated", "updatedAt"),
		CachedUpdatedAt: cachedUpdatedAt,
	}, nil
}

func decodeScorecardDetail(raw json.RawMessage, cachedUpdatedAt string) (*ScorecardDetail, error) {
	normalized, err := normalizeJSON(raw)
	if err != nil {
		return nil, err
	}

	var doc map[string]any
	if err := json.Unmarshal(normalized, &doc); err != nil {
		return nil, err
	}

	summary, err := decodeScorecardSummary(normalized, cachedUpdatedAt)
	if err != nil {
		return nil, err
	}

	detail := &ScorecardDetail{ScorecardSummary: summary}
	for _, item := range arrayFromAny(doc["questions"]) {
		questionMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		questionText := firstString(questionMap, "questionText", "text", "name", "label")
		if questionText == "" {
			continue
		}
		detail.Questions = append(detail.Questions, ScorecardQuestion{
			QuestionID:   firstString(questionMap, "questionId", "id"),
			QuestionText: questionText,
			QuestionType: firstString(questionMap, "questionType", "type"),
			IsOverall:    boolFromAny(questionMap["isOverall"]),
			MinRange:     int64FromAny(questionMap["minRange"]),
			MaxRange:     int64FromAny(questionMap["maxRange"]),
			AnswerGuide:  truncateString(firstString(questionMap, "answerGuide", "guide"), 500),
			Options:      scorecardAnswerOptions(questionMap["answerOptions"]),
		})
	}
	return detail, nil
}

func decodeScorecardActivity(raw json.RawMessage) (*scorecardActivityPayload, error) {
	normalized, err := normalizeJSON(raw)
	if err != nil {
		return nil, err
	}

	var doc map[string]any
	decoder := json.NewDecoder(bytes.NewReader(normalized))
	decoder.UseNumber()
	if err := decoder.Decode(&doc); err != nil {
		return nil, err
	}

	answeredScorecardID := firstIDString(doc, "answeredScorecardId", "id")
	if answeredScorecardID == "" {
		return nil, errors.New("scorecard activity payload missing answered scorecard id")
	}

	overallScore, averageScore, answerCount := scorecardActivityScores(doc["answers"])
	return &scorecardActivityPayload{
		AnsweredScorecardID: answeredScorecardID,
		ScorecardID:         firstIDString(doc, "scorecardId"),
		ScorecardName:       firstString(doc, "scorecardName", "name"),
		CallID:              firstIDString(doc, "callId"),
		CallStartedAt:       firstString(doc, "callStartTime", "callStartedAt", "started"),
		ReviewedUserID:      firstIDString(doc, "reviewedUserId"),
		ReviewerUserID:      firstIDString(doc, "reviewerUserId"),
		EditorUserID:        firstIDString(doc, "editorUserId"),
		ReviewMethod:        strings.ToUpper(firstString(doc, "reviewMethod")),
		ReviewTime:          firstString(doc, "reviewTime", "reviewedAt"),
		VisibilityType:      firstString(doc, "visibilityType"),
		OverallScore:        overallScore,
		AverageScore:        averageScore,
		AnswerCount:         answerCount,
		RawJSON:             normalized,
		RawSHA256:           sha256Hex(normalized),
	}, nil
}

func extractContextObjects(raw json.RawMessage) ([]contextObjectRow, bool, error) {
	normalized, err := normalizeJSON(raw)
	if err != nil {
		return nil, false, err
	}

	var root map[string]any
	if err := json.Unmarshal(normalized, &root); err != nil {
		return nil, false, err
	}

	type candidate struct {
		name  string
		value any
	}

	var candidates []candidate
	for _, key := range []string{"context", "crmContext", "crm", "extendedContext", "crmObjects", "objects"} {
		value, ok := root[key]
		if !ok {
			continue
		}
		candidates = append(candidates, candidate{name: key, value: value})
	}
	if len(candidates) == 0 {
		return nil, false, nil
	}

	var objects []contextObjectRow
	for _, candidate := range candidates {
		objects = append(objects, collectContextObjects(candidate.name, candidate.value)...)
	}
	return objects, true, nil
}

func collectContextObjects(defaultType string, value any) []contextObjectRow {
	switch typed := value.(type) {
	case []any:
		rows := make([]contextObjectRow, 0, len(typed))
		for idx, item := range typed {
			itemMap, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if row, ok := buildContextObject(defaultType, itemMap, idx); ok {
				rows = append(rows, row)
				continue
			}
			rows = append(rows, collectContextObjects(defaultType, itemMap)...)
		}
		return rows
	case map[string]any:
		if row, ok := buildContextObject(defaultType, typed, 0); ok {
			return []contextObjectRow{row}
		}
		var rows []contextObjectRow
		for key, child := range typed {
			rows = append(rows, collectContextObjects(key, child)...)
		}
		return rows
	default:
		return nil
	}
}

func buildContextObject(defaultType string, doc map[string]any, index int) (contextObjectRow, bool) {
	fieldsValue, ok := doc["fields"]
	if !ok {
		fieldsValue, ok = doc["properties"]
	}
	if !ok {
		return contextObjectRow{}, false
	}

	objectType := firstString(doc, "objectType", "type", "entityType")
	if objectType == "" {
		objectType = defaultType
	}
	objectID := firstString(doc, "id", "objectId", "crmId")
	objectName := firstString(doc, "name", "displayName", "label", "title")
	fields := extractContextFields(fieldsValue)
	if objectName == "" {
		objectName = contextObjectNameFromFields(fields)
	}
	rawJSON, err := normalizeJSONValue(doc)
	if err != nil {
		return contextObjectRow{}, false
	}

	return contextObjectRow{
		ObjectKey:  contextObjectKey(objectType, objectID, objectName, index),
		ObjectType: objectType,
		ObjectID:   objectID,
		ObjectName: objectName,
		RawJSON:    rawJSON,
		Fields:     fields,
	}, true
}

func contextObjectNameFromFields(fields []contextFieldRow) string {
	for _, field := range fields {
		if strings.EqualFold(strings.TrimSpace(field.FieldName), "Name") && strings.TrimSpace(field.ValueText) != "" {
			return strings.TrimSpace(field.ValueText)
		}
	}
	for _, field := range fields {
		if strings.EqualFold(strings.TrimSpace(field.FieldLabel), "Name") && strings.TrimSpace(field.ValueText) != "" {
			return strings.TrimSpace(field.ValueText)
		}
	}
	return ""
}

func extractContextFields(value any) []contextFieldRow {
	switch typed := value.(type) {
	case []any:
		rows := make([]contextFieldRow, 0, len(typed))
		for idx, item := range typed {
			itemMap, ok := item.(map[string]any)
			if !ok {
				continue
			}
			fieldName := firstString(itemMap, "name", "fieldName", "apiName")
			fieldLabel := firstString(itemMap, "label", "displayName")
			if fieldName == "" {
				fieldName = fieldLabel
			}
			if fieldName == "" {
				fieldName = fmt.Sprintf("field_%d", idx)
			}
			rawJSON, err := normalizeJSONValue(itemMap)
			if err != nil {
				continue
			}
			rows = append(rows, contextFieldRow{
				FieldName:  fieldName,
				FieldLabel: fieldLabel,
				FieldType:  firstString(itemMap, "type", "valueType"),
				ValueText:  stringifyValue(itemMap["value"]),
				RawJSON:    rawJSON,
			})
		}
		return rows
	case map[string]any:
		rows := make([]contextFieldRow, 0, len(typed))
		for key, item := range typed {
			rawJSON, err := normalizeJSONValue(map[string]any{
				"name":  key,
				"value": item,
			})
			if err != nil {
				continue
			}
			rows = append(rows, contextFieldRow{
				FieldName: key,
				ValueText: stringifyValue(item),
				RawJSON:   rawJSON,
			})
		}
		return rows
	default:
		return nil
	}
}

func extractCRMSchemaFields(doc map[string]any, objectType string) []crmSchemaFieldPayload {
	if value, ok := lookupAnyCase(doc, "objectTypeToSelectedFields"); ok {
		if byObject, ok := value.(map[string]any); ok {
			if selected, ok := lookupAnyCase(byObject, objectType); ok {
				return uniqueCRMSchemaFields(buildCRMSchemaFields(selected, ""))
			}
		}
	}

	for _, key := range []string{"fields", "selectedFields", "selectedCrmFields", "crmFields"} {
		if value, ok := lookupAnyCase(doc, key); ok {
			return uniqueCRMSchemaFields(buildCRMSchemaFields(value, ""))
		}
	}
	return nil
}

func buildCRMSchemaFields(value any, fallbackName string) []crmSchemaFieldPayload {
	switch typed := value.(type) {
	case []any:
		rows := make([]crmSchemaFieldPayload, 0, len(typed))
		for idx, item := range typed {
			itemMap, ok := item.(map[string]any)
			if !ok {
				continue
			}
			rows = append(rows, buildCRMSchemaField(itemMap, fallbackName, idx))
		}
		return rows
	case map[string]any:
		if fieldName := firstString(typed, "fieldName", "name", "apiName", "id"); fieldName != "" {
			return []crmSchemaFieldPayload{buildCRMSchemaField(typed, fallbackName, 0)}
		}
		rows := make([]crmSchemaFieldPayload, 0, len(typed))
		idx := 0
		for key, item := range typed {
			if itemMap, ok := item.(map[string]any); ok {
				rows = append(rows, buildCRMSchemaField(itemMap, key, idx))
			} else {
				rawDoc := map[string]any{"name": key, "value": item}
				rows = append(rows, buildCRMSchemaField(rawDoc, key, idx))
			}
			idx++
		}
		return rows
	default:
		return nil
	}
}

func buildCRMSchemaField(doc map[string]any, fallbackName string, index int) crmSchemaFieldPayload {
	fieldName := firstString(doc, "fieldName", "name", "apiName", "id")
	if fieldName == "" {
		fieldName = strings.TrimSpace(fallbackName)
	}
	if fieldName == "" {
		fieldName = fmt.Sprintf("field_%d", index)
	}
	fieldLabel := firstString(doc, "label", "displayName", "fieldLabel")
	if fieldLabel == "" && fieldName != fallbackName {
		fieldLabel = strings.TrimSpace(fallbackName)
	}
	raw, err := normalizeJSONValue(doc)
	if err != nil {
		raw = []byte(`{}`)
	}
	return crmSchemaFieldPayload{
		FieldName:  fieldName,
		FieldLabel: fieldLabel,
		FieldType:  firstString(doc, "fieldType", "type", "dataType", "valueType"),
		RawJSON:    raw,
		RawSHA256:  sha256Hex(raw),
	}
}

func uniqueCRMSchemaFields(fields []crmSchemaFieldPayload) []crmSchemaFieldPayload {
	out := make([]crmSchemaFieldPayload, 0, len(fields))
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		name := strings.TrimSpace(field.FieldName)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		field.FieldName = name
		out = append(out, field)
	}
	return out
}

func contextObjectKey(objectType string, objectID string, objectName string, index int) string {
	objectType = strings.TrimSpace(objectType)
	switch {
	case objectType != "" && strings.TrimSpace(objectID) != "":
		return objectType + ":" + strings.TrimSpace(objectID)
	case objectType != "" && strings.TrimSpace(objectName) != "":
		return objectType + ":" + strings.TrimSpace(objectName)
	case objectType != "":
		return objectType + ":" + strconv.Itoa(index)
	case strings.TrimSpace(objectID) != "":
		return "object:" + strings.TrimSpace(objectID)
	default:
		return "object:" + strconv.Itoa(index)
	}
}

func (s *Store) stageDistribution(ctx context.Context, objectType string, stageField string, lateValues []string) (map[string]int64, int64, int64, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT f.call_id,
       f.field_value_text
  FROM call_context_fields f
  JOIN call_context_objects o
    ON o.call_id = f.call_id
   AND o.object_key = f.object_key
 WHERE o.object_type = ?
   AND f.field_name = ?
   AND TRIM(f.field_value_text) <> ''`, objectType, stageField)
	if err != nil {
		return nil, 0, 0, err
	}
	defer rows.Close()

	lateSet := normalizedStringSet(lateValues)
	stageLabels := make(map[string]string)
	for _, value := range lateValues {
		clean := strings.TrimSpace(value)
		if clean != "" {
			stageLabels[strings.ToLower(clean)] = clean
		}
	}
	type callStages struct {
		hasLate bool
		stages  map[string]struct{}
	}
	byCall := make(map[string]*callStages)
	stageCounts := make(map[string]int64)
	for rows.Next() {
		var callID string
		var stage string
		if err := rows.Scan(&callID, &stage); err != nil {
			return nil, 0, 0, err
		}
		stage = strings.TrimSpace(stage)
		if stage == "" {
			continue
		}
		stageKey := strings.ToLower(stage)
		stageLabel, ok := stageLabels[stageKey]
		if !ok {
			stageLabel = stage
			stageLabels[stageKey] = stage
		}
		info := byCall[callID]
		if info == nil {
			info = &callStages{stages: make(map[string]struct{})}
			byCall[callID] = info
		}
		info.stages[stageLabel] = struct{}{}
		if _, ok := lateSet[stageKey]; ok {
			info.hasLate = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, 0, 0, err
	}
	var lateCalls int64
	var nonLateCalls int64
	for _, info := range byCall {
		if info.hasLate {
			lateCalls++
		} else {
			nonLateCalls++
		}
		for stage := range info.stages {
			stageCounts[stage]++
		}
	}
	return stageCounts, lateCalls, nonLateCalls, nil
}

func cleanStringList(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		clean := strings.TrimSpace(value)
		if clean == "" {
			continue
		}
		key := strings.ToLower(clean)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, clean)
	}
	return out
}

func normalizedStringSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		key := strings.ToLower(strings.TrimSpace(value))
		if key != "" {
			set[key] = struct{}{}
		}
	}
	return set
}

func boundedLimit(value int, defaultValue int, maxValue int) int {
	if value <= 0 {
		return defaultValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func sortLateStageSignals(signals []LateStageSignal) {
	sort.Slice(signals, func(i, j int) bool {
		left := signals[i]
		right := signals[j]
		if left.Lift != right.Lift {
			return left.Lift > right.Lift
		}
		if left.LateRate != right.LateRate {
			return left.LateRate > right.LateRate
		}
		if left.LatePopulatedCalls != right.LatePopulatedCalls {
			return left.LatePopulatedCalls > right.LatePopulatedCalls
		}
		return left.FieldName < right.FieldName
	})
}

func rate(part int64, total int64) float64 {
	if total <= 0 {
		return 0
	}
	return float64(part) / float64(total)
}

func truncateString(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 || len(value) <= max {
		return value
	}
	return value[:max]
}

func ensureParentDir(path string) error {
	if path == ":memory:" || strings.HasPrefix(path, "file:") {
		return nil
	}
	dir := filepath.Dir(path)
	if dir == "." || dir == "" {
		return nil
	}
	return os.MkdirAll(dir, 0o755)
}

func normalizeJSON(raw json.RawMessage) ([]byte, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, errors.New("json payload is required")
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, trimmed); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func normalizeJSONValue(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return normalizeJSON(encoded)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func nowUTC() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func firstString(doc map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := doc[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			return strings.TrimSpace(typed)
		case fmt.Stringer:
			return strings.TrimSpace(typed.String())
		}
	}
	return ""
}

func firstIDString(doc map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := doc[key]
		if !ok || value == nil {
			continue
		}
		switch typed := value.(type) {
		case string:
			return strings.TrimSpace(typed)
		case float64:
			return strconv.FormatInt(int64(typed), 10)
		case float32:
			return strconv.FormatInt(int64(typed), 10)
		case int:
			return strconv.FormatInt(int64(typed), 10)
		case int64:
			return strconv.FormatInt(typed, 10)
		case json.Number:
			return strings.TrimSpace(typed.String())
		case fmt.Stringer:
			return strings.TrimSpace(typed.String())
		}
	}
	return ""
}

func lookupAnyCase(doc map[string]any, key string) (any, bool) {
	if value, ok := doc[key]; ok {
		return value, true
	}
	for existing, value := range doc {
		if strings.EqualFold(existing, key) {
			return value, true
		}
	}
	return nil, false
}

func arrayFromAny(value any) []any {
	typed, ok := value.([]any)
	if !ok {
		return nil
	}
	return typed
}

func int64FromAny(value any) int64 {
	switch typed := value.(type) {
	case float64:
		return int64(typed)
	case float32:
		return int64(typed)
	case int:
		return int64(typed)
	case int64:
		return typed
	case json.Number:
		out, _ := typed.Int64()
		return out
	case string:
		out, _ := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return out
	default:
		return 0
	}
}

func float64FromAny(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case json.Number:
		out, _ := typed.Float64()
		return out
	case string:
		out, _ := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return out
	default:
		return 0
	}
}

func scorecardActivityScores(value any) (float64, float64, int64) {
	answers := arrayFromAny(value)
	if len(answers) == 0 {
		return 0, 0, 0
	}
	var total float64
	var count int64
	var overall float64
	var hasOverall bool
	for _, item := range answers {
		answer, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if boolFromAny(answer["notApplicable"]) {
			continue
		}
		if _, ok := answer["score"]; !ok {
			continue
		}
		score := float64FromAny(answer["score"])
		total += score
		count++
		if boolFromAny(answer["isOverall"]) {
			overall = score
			hasOverall = true
		}
	}
	if count == 0 {
		return 0, 0, 0
	}
	average := total / float64(count)
	if !hasOverall {
		overall = average
	}
	return overall, average, count
}

func scorecardAnswerOptions(value any) []string {
	items := arrayFromAny(value)
	if len(items) == 0 {
		return nil
	}
	out := make([]string, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		var text string
		switch typed := item.(type) {
		case string:
			text = typed
		case map[string]any:
			text = firstString(typed, "text", "label", "answer", "name", "value")
		default:
			text = stringifyValue(item)
		}
		text = truncateString(text, 160)
		if text == "" {
			continue
		}
		key := strings.ToLower(text)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, text)
	}
	return out
}

func boolFromAny(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "1", "true", "yes", "active":
			return true
		}
	case float64:
		return typed != 0
	case int:
		return typed != 0
	case int64:
		return typed != 0
	}
	return false
}

func normalizeGongSettingKind(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "tracker", "trackers", "keywordtracker", "keywordtrackers", "keyword_trackers":
		return "trackers"
	case "scorecard", "scorecards":
		return "scorecards"
	case "workspace", "workspaces":
		return "workspaces"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func mapFromAny(value any) map[string]any {
	typed, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	return typed
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func stringifyValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(typed), 'f', -1, 64)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case json.Number:
		return typed.String()
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return ""
		}
		return string(encoded)
	}
}
