package storeiface

import (
	"context"
	"encoding/json"

	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
)

type Closer interface {
	Close() error
}

type SyncRunStore interface {
	StartSyncRun(ctx context.Context, params sqlite.StartSyncRunParams) (*sqlite.SyncRun, error)
	FinishSyncRun(ctx context.Context, runID int64, params sqlite.FinishSyncRunParams) error
}

type CallWriter interface {
	UpsertCall(ctx context.Context, raw json.RawMessage) (*sqlite.CallRecord, error)
}

type UserWriter interface {
	UpsertUser(ctx context.Context, raw json.RawMessage) (*sqlite.UserRecord, error)
}

type TranscriptWriter interface {
	UpsertTranscript(ctx context.Context, raw json.RawMessage) (*sqlite.TranscriptRecord, error)
}

type GongSettingWriter interface {
	UpsertGongSetting(ctx context.Context, kind string, raw json.RawMessage) (*sqlite.GongSettingRecord, error)
}

type CRMIntegrationWriter interface {
	UpsertCRMIntegration(ctx context.Context, raw json.RawMessage) (*sqlite.CRMIntegrationRecord, error)
}

type CRMSchemaWriter interface {
	UpsertCRMSchema(ctx context.Context, integrationID string, objectType string, raw json.RawMessage) (int64, error)
}

type ScorecardActivityWriter interface {
	UpsertScorecardActivity(ctx context.Context, raw json.RawMessage) (*sqlite.ScorecardActivityRecord, error)
}

type SyncStore interface {
	SyncRunStore
	CallWriter
	UserWriter
}

type SettingsStore interface {
	SyncRunStore
	GongSettingWriter
}

type CRMIntegrationStore interface {
	SyncRunStore
	CRMIntegrationWriter
}

type CRMSchemaStore interface {
	SyncRunStore
	CRMSchemaWriter
}

type ScorecardActivityStore interface {
	SyncRunStore
	ScorecardActivityWriter
}

type SyncStatusReader interface {
	SyncStatusSummary(ctx context.Context) (*sqlite.SyncStatusSummary, error)
}

type CallSearcher interface {
	SearchCallsRaw(ctx context.Context, params sqlite.CallSearchParams) ([]json.RawMessage, error)
}

type TranscriptStore interface {
	SyncRunStore
	TranscriptWriter
	FindCallsMissingTranscripts(ctx context.Context, limit int) ([]sqlite.MissingTranscriptCall, error)
	SearchTranscriptSegments(ctx context.Context, query string, limit int) ([]sqlite.TranscriptSearchResult, error)
}

type CoreReadStore interface {
	SyncStatusReader
	CallSearcher
	SearchTranscriptSegments(ctx context.Context, query string, limit int) ([]sqlite.TranscriptSearchResult, error)
}
