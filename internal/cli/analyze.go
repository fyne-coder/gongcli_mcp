package cli

import (
	"context"
	"flag"
	"fmt"

	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
)

func (a *app) analyze(ctx context.Context, args []string) error {
	if len(args) == 0 {
		fmt.Fprintln(a.err, "usage: gongctl analyze [calls|coverage|transcript-backlog|crm-schema|settings|scorecards|scorecard]")
		return errUsage
	}

	switch args[0] {
	case "calls":
		return a.analyzeCalls(ctx, args[1:])
	case "coverage":
		return a.analyzeCoverage(ctx, args[1:])
	case "transcript-backlog":
		return a.analyzeTranscriptBacklog(ctx, args[1:])
	case "crm-schema":
		return a.analyzeCRMSchema(ctx, args[1:])
	case "settings":
		return a.analyzeSettings(ctx, args[1:])
	case "scorecards":
		return a.analyzeScorecards(ctx, args[1:])
	case "scorecard":
		return a.analyzeScorecard(ctx, args[1:])
	default:
		fmt.Fprintf(a.err, "unknown analyze command %q\n", args[0])
		return errUsage
	}
}

func (a *app) analyzeCalls(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("analyze calls", flag.ContinueOnError)
	fs.SetOutput(a.err)
	dbPath := fs.String("db", "", "SQLite database path")
	groupBy := fs.String("group-by", "lifecycle", "dimension: builtin values include lifecycle, opportunity_stage, account_industry, scope, system, direction, transcript_status, duration_bucket, month; profile source also accepts profile field concepts such as deal_stage")
	lifecycle := fs.String("lifecycle", "", "lifecycle bucket filter")
	scope := fs.String("scope", "", "scope filter: External, Internal, Unknown")
	system := fs.String("system", "", "Gong system filter")
	direction := fs.String("direction", "", "Gong direction filter")
	transcriptStatus := fs.String("transcript-status", "", "transcript filter: present or missing")
	lifecycleSource := fs.String("lifecycle-source", "auto", "lifecycle source: auto, profile, builtin")
	limit := fs.Int("limit", 50, "maximum groups to return")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}

	store, err := openSQLiteReadOnlyStore(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	rows, info, err := store.SummarizeCallFactsWithSource(ctx, sqlite.CallFactsSummaryParams{
		GroupBy:          *groupBy,
		LifecycleBucket:  *lifecycle,
		Scope:            *scope,
		System:           *system,
		Direction:        *direction,
		TranscriptStatus: *transcriptStatus,
		Limit:            *limit,
		LifecycleSource:  *lifecycleSource,
	})
	if err != nil {
		return err
	}
	canonicalGroupBy := *groupBy
	if len(rows) > 0 {
		canonicalGroupBy = rows[0].GroupBy
	} else if info == nil || info.LifecycleSource == sqlite.LifecycleSourceBuiltin {
		var err error
		canonicalGroupBy, err = sqlite.NormalizeCallFactsGroupBy(*groupBy)
		if err != nil {
			return err
		}
	}

	return writeJSONValue(a.out, callFactsSummaryResponse{
		GroupBy:             canonicalGroupBy,
		LifecycleBucket:     *lifecycle,
		LifecycleSource:     profileInfoSource(info),
		Profile:             profileInfoProfile(info),
		UnavailableConcepts: profileInfoUnavailable(info),
		Scope:               *scope,
		System:              *system,
		Direction:           *direction,
		TranscriptStatus:    *transcriptStatus,
		Limit:               normalizeSearchLimit(*limit, 50),
		Results:             rows,
	})
}

func (a *app) analyzeCoverage(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("analyze coverage", flag.ContinueOnError)
	fs.SetOutput(a.err)
	dbPath := fs.String("db", "", "SQLite database path")
	lifecycleSource := fs.String("lifecycle-source", "auto", "lifecycle source: auto, profile, builtin")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}

	store, err := openSQLiteReadOnlyStore(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	coverage, byLifecycle, info, err := store.CallFactsCoverageWithSource(ctx, *lifecycleSource)
	if err != nil {
		return err
	}
	byScope, _, err := store.SummarizeCallFactsWithSource(ctx, sqlite.CallFactsSummaryParams{
		GroupBy:         "scope",
		LifecycleSource: *lifecycleSource,
		Limit:           50,
	})
	if err != nil {
		return err
	}
	bySystem, _, err := store.SummarizeCallFactsWithSource(ctx, sqlite.CallFactsSummaryParams{
		GroupBy:         "system",
		LifecycleSource: *lifecycleSource,
		Limit:           50,
	})
	if err != nil {
		return err
	}
	byDirection, _, err := store.SummarizeCallFactsWithSource(ctx, sqlite.CallFactsSummaryParams{
		GroupBy:         "direction",
		LifecycleSource: *lifecycleSource,
		Limit:           50,
	})
	if err != nil {
		return err
	}

	return writeJSONValue(a.out, callFactsCoverageResponse{
		LifecycleSource:     profileInfoSource(info),
		Profile:             profileInfoProfile(info),
		UnavailableConcepts: profileInfoUnavailable(info),
		Coverage:            coverage,
		ByLifecycle:         byLifecycle,
		ByScope:             byScope,
		BySystem:            bySystem,
		ByDirection:         byDirection,
	})
}

func (a *app) analyzeTranscriptBacklog(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("analyze transcript-backlog", flag.ContinueOnError)
	fs.SetOutput(a.err)
	dbPath := fs.String("db", "", "SQLite database path")
	lifecycle := fs.String("lifecycle", "", "lifecycle bucket filter")
	lifecycleSource := fs.String("lifecycle-source", "auto", "lifecycle source: auto, profile, builtin")
	limit := fs.Int("limit", 25, "maximum calls to return")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}

	store, err := openSQLiteReadOnlyStore(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	rows, info, err := store.PrioritizeTranscriptsByLifecycleWithSource(ctx, sqlite.LifecycleTranscriptPriorityParams{
		Bucket:          *lifecycle,
		Limit:           *limit,
		LifecycleSource: *lifecycleSource,
	})
	if err != nil {
		return err
	}

	return writeJSONValue(a.out, transcriptBacklogResponse{
		LifecycleBucket:     *lifecycle,
		LifecycleSource:     profileInfoSource(info),
		Profile:             profileInfoProfile(info),
		UnavailableConcepts: profileInfoUnavailable(info),
		Limit:               normalizeSearchLimit(*limit, 25),
		Results:             rows,
	})
}

func (a *app) analyzeCRMSchema(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("analyze crm-schema", flag.ContinueOnError)
	fs.SetOutput(a.err)
	dbPath := fs.String("db", "", "SQLite database path")
	integrationID := fs.String("integration-id", "", "optional CRM integration ID filter")
	objectType := fs.String("object-type", "", "optional CRM object type filter")
	limit := fs.Int("limit", 100, "maximum fields to return")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}

	store, err := openSQLiteReadOnlyStore(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	integrations, err := store.ListCRMIntegrations(ctx)
	if err != nil {
		return err
	}
	objects, err := store.ListCRMSchemaObjects(ctx, *integrationID)
	if err != nil {
		return err
	}
	fields, err := store.ListCRMSchemaFields(ctx, sqlite.CRMSchemaFieldListParams{
		IntegrationID: *integrationID,
		ObjectType:    *objectType,
		Limit:         *limit,
	})
	if err != nil {
		return err
	}

	return writeJSONValue(a.out, crmSchemaAnalyzeResponse{
		IntegrationID: *integrationID,
		ObjectType:    *objectType,
		Limit:         normalizeSearchLimit(*limit, 100),
		Integrations:  integrations,
		Objects:       objects,
		Fields:        fields,
	})
}

func (a *app) analyzeSettings(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("analyze settings", flag.ContinueOnError)
	fs.SetOutput(a.err)
	dbPath := fs.String("db", "", "SQLite database path")
	kind := fs.String("kind", "", "settings kind: trackers, scorecards, workspaces")
	limit := fs.Int("limit", 100, "maximum settings items to return")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}

	store, err := openSQLiteReadOnlyStore(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	items, err := store.ListGongSettings(ctx, sqlite.GongSettingListParams{
		Kind:  *kind,
		Limit: *limit,
	})
	if err != nil {
		return err
	}
	return writeJSONValue(a.out, gongSettingsAnalyzeResponse{
		Kind:    *kind,
		Limit:   normalizeSearchLimit(*limit, 100),
		Results: items,
	})
}

func (a *app) analyzeScorecards(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("analyze scorecards", flag.ContinueOnError)
	fs.SetOutput(a.err)
	dbPath := fs.String("db", "", "SQLite database path")
	activeOnly := fs.Bool("active-only", false, "only include active scorecards")
	limit := fs.Int("limit", 100, "maximum scorecards to return")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}

	store, err := openSQLiteReadOnlyStore(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	rows, err := store.ListScorecards(ctx, sqlite.ScorecardListParams{
		ActiveOnly: *activeOnly,
		Limit:      *limit,
	})
	if err != nil {
		return err
	}
	return writeJSONValue(a.out, scorecardsAnalyzeResponse{
		ActiveOnly: *activeOnly,
		Limit:      normalizeSearchLimit(*limit, 100),
		Results:    rows,
	})
}

func (a *app) analyzeScorecard(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("analyze scorecard", flag.ContinueOnError)
	fs.SetOutput(a.err)
	dbPath := fs.String("db", "", "SQLite database path")
	scorecardID := fs.String("scorecard-id", "", "Gong scorecard ID")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}

	store, err := openSQLiteReadOnlyStore(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	detail, err := store.GetScorecardDetail(ctx, *scorecardID)
	if err != nil {
		return err
	}
	return writeJSONValue(a.out, detail)
}

type callFactsSummaryResponse struct {
	GroupBy             string                       `json:"group_by"`
	LifecycleBucket     string                       `json:"lifecycle_bucket,omitempty"`
	LifecycleSource     string                       `json:"lifecycle_source,omitempty"`
	Profile             *sqlite.BusinessProfile      `json:"profile,omitempty"`
	UnavailableConcepts []string                     `json:"unavailable_concepts,omitempty"`
	Scope               string                       `json:"scope,omitempty"`
	System              string                       `json:"system,omitempty"`
	Direction           string                       `json:"direction,omitempty"`
	TranscriptStatus    string                       `json:"transcript_status,omitempty"`
	Limit               int                          `json:"limit"`
	Results             []sqlite.CallFactsSummaryRow `json:"results"`
}

type callFactsCoverageResponse struct {
	LifecycleSource     string                       `json:"lifecycle_source,omitempty"`
	Profile             *sqlite.BusinessProfile      `json:"profile,omitempty"`
	UnavailableConcepts []string                     `json:"unavailable_concepts,omitempty"`
	Coverage            *sqlite.CallFactsCoverage    `json:"coverage"`
	ByLifecycle         []sqlite.CallFactsSummaryRow `json:"by_lifecycle"`
	ByScope             []sqlite.CallFactsSummaryRow `json:"by_scope"`
	BySystem            []sqlite.CallFactsSummaryRow `json:"by_system"`
	ByDirection         []sqlite.CallFactsSummaryRow `json:"by_direction"`
}

type transcriptBacklogResponse struct {
	LifecycleBucket     string                               `json:"lifecycle_bucket,omitempty"`
	LifecycleSource     string                               `json:"lifecycle_source,omitempty"`
	Profile             *sqlite.BusinessProfile              `json:"profile,omitempty"`
	UnavailableConcepts []string                             `json:"unavailable_concepts,omitempty"`
	Limit               int                                  `json:"limit"`
	Results             []sqlite.LifecycleTranscriptPriority `json:"results"`
}

type crmSchemaAnalyzeResponse struct {
	IntegrationID string                         `json:"integration_id,omitempty"`
	ObjectType    string                         `json:"object_type,omitempty"`
	Limit         int                            `json:"limit"`
	Integrations  []sqlite.CRMIntegrationRecord  `json:"integrations"`
	Objects       []sqlite.CRMSchemaObjectRecord `json:"objects"`
	Fields        []sqlite.CRMSchemaFieldRecord  `json:"fields"`
}

type gongSettingsAnalyzeResponse struct {
	Kind    string                     `json:"kind,omitempty"`
	Limit   int                        `json:"limit"`
	Results []sqlite.GongSettingRecord `json:"results"`
}

type scorecardsAnalyzeResponse struct {
	ActiveOnly bool                      `json:"active_only"`
	Limit      int                       `json:"limit"`
	Results    []sqlite.ScorecardSummary `json:"results"`
}

func profileInfoSource(info *sqlite.ProfileQueryInfo) string {
	if info == nil {
		return ""
	}
	return info.LifecycleSource
}

func profileInfoProfile(info *sqlite.ProfileQueryInfo) *sqlite.BusinessProfile {
	if info == nil {
		return nil
	}
	return info.Profile
}

func profileInfoUnavailable(info *sqlite.ProfileQueryInfo) []string {
	if info == nil {
		return nil
	}
	return info.UnavailableConcepts
}
