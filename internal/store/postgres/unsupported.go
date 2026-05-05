package postgres

import (
	"context"
	"errors"
	"strings"

	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
)

var errUnsupported = errors.New("postgres store does not support this tool in the first vertical slice")

func unsupported() error { return errUnsupported }

func (s *Store) SearchCRMFieldValues(ctx context.Context, params sqlite.CRMFieldValueSearchParams) ([]sqlite.CRMFieldValueMatch, error) {
	return nil, unsupported()
}
func (s *Store) ListCRMIntegrations(ctx context.Context) ([]sqlite.CRMIntegrationRecord, error) {
	return nil, unsupported()
}
func (s *Store) ListCRMSchemaObjects(ctx context.Context, integrationID string) ([]sqlite.CRMSchemaObjectRecord, error) {
	return nil, unsupported()
}
func (s *Store) ListCRMSchemaFields(ctx context.Context, params sqlite.CRMSchemaFieldListParams) ([]sqlite.CRMSchemaFieldRecord, error) {
	return nil, unsupported()
}
func (s *Store) ListGongSettings(ctx context.Context, params sqlite.GongSettingListParams) ([]sqlite.GongSettingRecord, error) {
	return nil, unsupported()
}
func (s *Store) ListScorecards(ctx context.Context, params sqlite.ScorecardListParams) ([]sqlite.ScorecardSummary, error) {
	return nil, unsupported()
}
func (s *Store) GetScorecardDetail(ctx context.Context, scorecardID string) (*sqlite.ScorecardDetail, error) {
	return nil, unsupported()
}
func (s *Store) ScorecardActivityOverview(ctx context.Context, limit int) (*sqlite.ScorecardActivityOverview, error) {
	return nil, unsupported()
}
func (s *Store) ListUnmappedCRMFields(ctx context.Context, params sqlite.UnmappedCRMFieldParams) ([]sqlite.UnmappedCRMField, error) {
	return nil, unsupported()
}
func (s *Store) AnalyzeLateStageSignals(ctx context.Context, params sqlite.LateStageSignalParams) (*sqlite.LateStageSignalsReport, error) {
	return nil, unsupported()
}
func (s *Store) ListOpportunitiesMissingTranscripts(ctx context.Context, params sqlite.OpportunityMissingTranscriptParams) ([]sqlite.OpportunityMissingTranscriptSummary, error) {
	return nil, unsupported()
}
func (s *Store) SearchTranscriptSegmentsByCRMContext(ctx context.Context, params sqlite.TranscriptCRMSearchParams) ([]sqlite.TranscriptCRMSearchResult, error) {
	return nil, unsupported()
}
func (s *Store) SummarizeOpportunityCalls(ctx context.Context, params sqlite.OpportunityCallSummaryParams) ([]sqlite.OpportunityCallSummary, error) {
	return nil, unsupported()
}
func (s *Store) CRMFieldPopulationMatrix(ctx context.Context, params sqlite.CRMFieldPopulationMatrixParams) (*sqlite.CRMFieldPopulationMatrix, error) {
	return nil, unsupported()
}
func (s *Store) CompareLifecycleCRMFields(ctx context.Context, params sqlite.LifecycleCRMFieldComparisonParams) (*sqlite.LifecycleCRMFieldComparison, error) {
	return nil, unsupported()
}
func (s *Store) FindCallsMissingTranscriptsByFilters(ctx context.Context, params sqlite.MissingTranscriptSearchParams) ([]sqlite.MissingTranscriptCall, error) {
	if strings.TrimSpace(params.CRMObjectType) != "" || strings.TrimSpace(params.CRMObjectID) != "" || strings.TrimSpace(params.LifecycleBucket) != "" || strings.TrimSpace(params.Scope) != "" || strings.TrimSpace(params.System) != "" || strings.TrimSpace(params.Direction) != "" {
		return nil, unsupported()
	}
	return s.FindCallsMissingTranscripts(ctx, params.Limit)
}
