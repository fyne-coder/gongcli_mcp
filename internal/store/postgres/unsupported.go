package postgres

import (
	"context"
	"errors"
	"strings"

	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
)

var errUnsupported = errors.New("postgres store does not support this tool in the first vertical slice")

func unsupported() error { return errUnsupported }

func (s *Store) CompareLifecycleCRMFields(ctx context.Context, params sqlite.LifecycleCRMFieldComparisonParams) (*sqlite.LifecycleCRMFieldComparison, error) {
	return nil, unsupported()
}
func (s *Store) FindCallsMissingTranscriptsByFilters(ctx context.Context, params sqlite.MissingTranscriptSearchParams) ([]sqlite.MissingTranscriptCall, error) {
	if strings.TrimSpace(params.CRMObjectType) != "" || strings.TrimSpace(params.CRMObjectID) != "" || strings.TrimSpace(params.LifecycleBucket) != "" || strings.TrimSpace(params.Scope) != "" || strings.TrimSpace(params.System) != "" || strings.TrimSpace(params.Direction) != "" {
		return nil, unsupported()
	}
	return s.FindCallsMissingTranscripts(ctx, params.Limit)
}
