package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
)

// FacadeOperation describes one logical MCP operation routed by the stable
// facade tools (Phase 13e2). Each operation pins a small, reviewable contract:
// a name, a human-readable description, the facade tool that surfaces it, the
// existing internal tool the facade dispatches to, an exposure level, and the
// presets in which the routed tool is currently exposed by default.
//
// The facade is additive: top-level individual tools (search_calls,
// get_sync_status, build_call_cohort, etc.) remain available for operator and
// analyst testing. The facade lets MCP clients depend on a smaller, more
// stable surface while operations grow underneath.
type FacadeOperation struct {
	Name           string         `json:"operation"`
	Version        string         `json:"version"`
	Description    string         `json:"description"`
	FacadeTool     string         `json:"facade_tool"`
	RoutedTool     string         `json:"routed_tool"`
	RoutedFallback string         `json:"routed_tool_fallback,omitempty"`
	ExposureLevel  string         `json:"exposure_level"`
	AllowedPresets []string       `json:"allowed_presets"`
	InputSchema    map[string]any `json:"input_schema"`
	Examples       []any          `json:"examples,omitempty"`
}

// Stable facade tool names. They must remain stable across versions even as
// operations are added or evolve.
const (
	FacadeToolStatus               = "gong_status"
	FacadeToolDiscoverCapabilities = "gong_discover_capabilities"
	FacadeToolQuery                = "gong_query"
	FacadeToolAnalyze              = "gong_analyze"
	FacadeToolGetEvidence          = "gong_get_evidence"
	FacadeToolExplainLimitations   = "gong_explain_limitations"
)

// Operation names are dotted, lowercase, and stable across versions.
const (
	OpStatusSync                = "status.sync"
	OpQueryCalls                = "query.calls"
	OpQueryScorecards           = "query.scorecards"
	OpQueryScorecardDetail      = "query.scorecard_detail"
	OpQueryTranscriptSegments   = "query.transcript_segments"
	OpAnalyzeCohortBuild        = "analyze.cohort.build"
	OpAnalyzeCohortInspect      = "analyze.cohort.inspect"
	OpAnalyzeThemesDiscover     = "analyze.themes.discover"
	OpAnalyzeLimitationsExplain = "analyze.limitations.explain"
	OpEvidenceQuotesSearch      = "evidence.quotes.search"
	OpEvidenceQuotePackBuild    = "evidence.quote_pack.build"
	OpEvidenceHighlightsList    = "evidence.highlights.list"
)

// internalRoutedToolListAIHighlights is the internal routed-tool name used by
// evidence.highlights.list. It is intentionally not exposed as a top-level
// MCP tool — the facade is the only supported entry point — and the Postgres
// store is the only backend that can return rows.
const internalRoutedToolListAIHighlights = "list_call_ai_highlights"

// FacadeOperations returns the registry of all known facade operations. The
// list is sorted by operation name for stable output.
func FacadeOperations() []FacadeOperation {
	ops := []FacadeOperation{
		{
			Name:           OpStatusSync,
			Version:        "v1",
			Description:    "Cache and sync run status. Routed to the existing get_sync_status tool.",
			FacadeTool:     FacadeToolStatus,
			RoutedTool:     "get_sync_status",
			ExposureLevel:  "operator-status",
			AllowedPresets: []string{"business-pilot", "operator-smoke", "analyst-core", "analyst-business-core", "analyst", "all-readonly"},
			InputSchema:    objectSchema(nil, nil),
			Examples:       []any{map[string]any{}},
		},
		{
			Name:           OpQueryCalls,
			Version:        "v1",
			Description:    "Bounded call search. Routed to search_calls_by_filters when available, otherwise search_calls.",
			FacadeTool:     FacadeToolQuery,
			RoutedTool:     "search_calls_by_filters",
			RoutedFallback: "search_calls",
			ExposureLevel:  "scoped-analyst",
			AllowedPresets: []string{"analyst-facade", "analyst-business-core", "analyst", "all-readonly"},
			InputSchema: objectSchema(map[string]any{
				"filter":                map[string]any{"type": "object"},
				"limit":                 map[string]any{"type": "integer"},
				"include_account_names": map[string]any{"type": "boolean"},
			}, nil),
			Examples: []any{
				map[string]any{
					"filter": map[string]any{
						"from_date": "2026-04-01",
						"to_date":   "2026-04-30",
						"limit":     10,
					},
				},
			},
		},
		{
			Name:           OpQueryScorecards,
			Version:        "v1",
			Description:    "List scorecard inventory rows from the cached gong_settings model. Routed to list_scorecards. Exposes only stable scorecard, workspace, and review-method metadata; no raw settings payloads, scorecard activity, answer text, user IDs, or call IDs.",
			FacadeTool:     FacadeToolQuery,
			RoutedTool:     "list_scorecards",
			ExposureLevel:  "scoped-analyst",
			AllowedPresets: []string{"analyst-facade", "analyst-core", "analyst-business-core", "analyst", "all-readonly"},
			InputSchema: objectSchema(map[string]any{
				"active_only": map[string]any{"type": "boolean"},
				"limit":       map[string]any{"type": "integer"},
			}, nil),
			Examples: []any{
				map[string]any{"active_only": true, "limit": 25},
			},
		},
		{
			Name:           OpQueryScorecardDetail,
			Version:        "v1",
			Description:    "Fetch one cached scorecard's question inventory by scorecard_id. Routed to get_scorecard. Returns scorecard metadata and question text without scorecard activity, answer text, reviewer IDs, or call IDs.",
			FacadeTool:     FacadeToolQuery,
			RoutedTool:     "get_scorecard",
			ExposureLevel:  "scoped-analyst",
			AllowedPresets: []string{"analyst-facade", "analyst-core", "analyst-business-core", "analyst", "all-readonly"},
			InputSchema: objectSchema(map[string]any{
				"scorecard_id": map[string]any{"type": "string"},
			}, []string{"scorecard_id"}),
			Examples: []any{
				map[string]any{"scorecard_id": "scorecard-001"},
			},
		},
		{
			Name:           OpQueryTranscriptSegments,
			Version:        "v1",
			Description:    "Bounded transcript-segment search. Routed to search_transcript_segments when available, otherwise search_transcripts_by_filters.",
			FacadeTool:     FacadeToolQuery,
			RoutedTool:     "search_transcript_segments",
			RoutedFallback: "search_transcripts_by_filters",
			ExposureLevel:  "scoped-analyst",
			AllowedPresets: []string{"operator-smoke", "analyst-core", "analyst-business-core", "analyst", "governance-search", "all-readonly"},
			InputSchema: objectSchema(map[string]any{
				"query": map[string]any{"type": "string"},
				"limit": map[string]any{"type": "integer"},
			}, []string{"query"}),
			Examples: []any{
				map[string]any{"query": "shared Postgres deployment", "limit": 5},
			},
		},
		{
			Name:           OpAnalyzeCohortBuild,
			Version:        "v1",
			Description:    "Build a reproducible bounded call cohort and return a deterministic cohort_id.",
			FacadeTool:     FacadeToolAnalyze,
			RoutedTool:     "build_call_cohort",
			ExposureLevel:  "scoped-analyst",
			AllowedPresets: []string{"analyst-business-core", "analyst", "all-readonly"},
			InputSchema: objectSchema(map[string]any{
				"filter": map[string]any{"type": "object"},
				"limit":  map[string]any{"type": "integer"},
			}, nil),
			Examples: []any{
				map[string]any{
					"filter": map[string]any{
						"query":     "shared Postgres",
						"from_date": "2026-04-01",
						"to_date":   "2026-04-30",
						"limit":     25,
					},
					"limit": 5,
				},
			},
		},
		{
			Name:           OpAnalyzeCohortInspect,
			Version:        "v1",
			Description:    "Inspect cache coverage and limitations for a reproducible call cohort filter.",
			FacadeTool:     FacadeToolAnalyze,
			RoutedTool:     "inspect_call_cohort",
			ExposureLevel:  "scoped-analyst",
			AllowedPresets: []string{"analyst-business-core", "analyst", "all-readonly"},
			InputSchema: objectSchema(map[string]any{
				"filter": map[string]any{"type": "object"},
				"limit":  map[string]any{"type": "integer"},
			}, nil),
		},
		{
			Name:           OpAnalyzeThemesDiscover,
			Version:        "v1",
			Description:    "Discover bounded themes within a cohort filter.",
			FacadeTool:     FacadeToolAnalyze,
			RoutedTool:     "discover_themes_in_cohort",
			ExposureLevel:  "scoped-analyst",
			AllowedPresets: []string{"analyst-business-core", "analyst", "all-readonly"},
			InputSchema: objectSchema(map[string]any{
				"filter":      map[string]any{"type": "object"},
				"theme_query": map[string]any{"type": "string"},
				"limit":       map[string]any{"type": "integer"},
			}, nil),
		},
		{
			Name:           OpAnalyzeLimitationsExplain,
			Version:        "v1",
			Description:    "Explain current cache limitations for a requested analysis.",
			FacadeTool:     FacadeToolAnalyze,
			RoutedTool:     "explain_analysis_limitations",
			ExposureLevel:  "scoped-analyst",
			AllowedPresets: []string{"analyst-business-core", "analyst", "all-readonly"},
			InputSchema: objectSchema(map[string]any{
				"filter": map[string]any{"type": "object"},
			}, nil),
		},
		{
			Name:           OpEvidenceQuotesSearch,
			Version:        "v1",
			Description:    "Search bounded quote evidence inside a cohort filter. Routed to search_quotes_in_cohort when available, otherwise search_transcript_quotes_with_attribution.",
			FacadeTool:     FacadeToolGetEvidence,
			RoutedTool:     "search_quotes_in_cohort",
			RoutedFallback: "search_transcript_quotes_with_attribution",
			ExposureLevel:  "scoped-analyst",
			AllowedPresets: []string{"analyst-business-core", "analyst", "all-readonly"},
			InputSchema: objectSchema(map[string]any{
				"filter":      map[string]any{"type": "object"},
				"theme_query": map[string]any{"type": "string"},
				"limit":       map[string]any{"type": "integer"},
			}, nil),
		},
		{
			Name:           OpEvidenceQuotePackBuild,
			Version:        "v1",
			Description:    "Build a bounded quote pack with explicit attribution and redaction limits.",
			FacadeTool:     FacadeToolGetEvidence,
			RoutedTool:     "build_quote_pack",
			ExposureLevel:  "scoped-analyst",
			AllowedPresets: []string{"analyst-business-core", "analyst", "all-readonly"},
			InputSchema: objectSchema(map[string]any{
				"filter":      map[string]any{"type": "object"},
				"theme_query": map[string]any{"type": "string"},
				"limit":       map[string]any{"type": "integer"},
			}, nil),
		},
		{
			Name:           OpEvidenceHighlightsList,
			Version:        "v1",
			Description:    "List bounded, redacted Gong AI highlight rows from the Postgres call_ai_highlights read model. Highlights are Gong AI accelerators; transcript quotes remain primary evidence. Raw highlight JSON and account/customer enumeration are not exposed.",
			FacadeTool:     FacadeToolGetEvidence,
			RoutedTool:     internalRoutedToolListAIHighlights,
			ExposureLevel:  "scoped-analyst",
			AllowedPresets: []string{"analyst-business-core", "analyst", "all-readonly"},
			InputSchema: objectSchema(map[string]any{
				"call_ids": map[string]any{
					"type":        "array",
					"description": "Bounded list of cached call_ids to look up highlights for.",
					"items":       map[string]any{"type": "string"},
					"minItems":    1,
					"maxItems":    maxAIHighlightCallIDs,
				},
				"limit": map[string]any{
					"type":    "integer",
					"minimum": 1,
					"maximum": maxAIHighlightLimit,
				},
			}, []string{"call_ids"}),
			Examples: []any{
				map[string]any{
					"call_ids": []string{"call-allow-1", "call-allow-2"},
					"limit":    25,
				},
			},
		},
	}
	sort.SliceStable(ops, func(i, j int) bool { return ops[i].Name < ops[j].Name })
	return ops
}

// FacadeOperationByName returns the operation registered under the given
// dotted name, if any.
func FacadeOperationByName(name string) (FacadeOperation, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return FacadeOperation{}, false
	}
	for _, op := range FacadeOperations() {
		if op.Name == name {
			return op, true
		}
	}
	return FacadeOperation{}, false
}

// facadeOperationsForTool filters the registry to operations dispatched
// through the named facade tool.
func facadeOperationsForTool(facadeTool string) []FacadeOperation {
	out := make([]FacadeOperation, 0)
	for _, op := range FacadeOperations() {
		if op.FacadeTool == facadeTool {
			out = append(out, op)
		}
	}
	return out
}

func facadeTools(_ LimitPolicy) []tool {
	return []tool{
		{
			Name:        FacadeToolStatus,
			Description: "Stable facade for cache and sync status. Routes the status.sync operation to the underlying status tool. The top-level get_sync_status tool remains available for operator/analyst testing.",
			InputSchema: objectSchema(nil, nil),
		},
		{
			Name:        FacadeToolDiscoverCapabilities,
			Description: "Return the stable MCP facade operation registry: each operation's name, version, description, facade tool, routed tool, exposure level, allowed presets, input schema, and examples. Top-level individual tools remain for operator/analyst testing.",
			InputSchema: objectSchema(nil, nil),
		},
		{
			Name:        FacadeToolQuery,
			Description: "Stable facade for bounded query operations. Pass {\"operation\": \"query.calls\" | \"query.transcript_segments\" | \"query.scorecards\" | \"query.scorecard_detail\", \"arguments\": {...}}.",
			InputSchema: facadeDispatchSchema([]string{OpQueryCalls, OpQueryTranscriptSegments, OpQueryScorecards, OpQueryScorecardDetail}),
		},
		{
			Name:        FacadeToolAnalyze,
			Description: "Stable facade for bounded analysis operations: cohort build/inspect, theme discovery, and limitations explanation.",
			InputSchema: facadeDispatchSchema([]string{OpAnalyzeCohortBuild, OpAnalyzeCohortInspect, OpAnalyzeThemesDiscover, OpAnalyzeLimitationsExplain}),
		},
		{
			Name:        FacadeToolGetEvidence,
			Description: "Stable facade for bounded evidence operations: quote search inside a cohort, quote-pack assembly, and typed AI highlights lookup.",
			InputSchema: facadeDispatchSchema([]string{OpEvidenceQuotesSearch, OpEvidenceQuotePackBuild, OpEvidenceHighlightsList}),
		},
		{
			Name:        FacadeToolExplainLimitations,
			Description: "Stable facade for explaining cache, governance, and facade limitations. Pass an explicit operation to route to a tool, or call with no operation to get a high-level facade limitations summary.",
			InputSchema: facadeDispatchSchema([]string{OpAnalyzeLimitationsExplain}),
		},
	}
}

func facadeDispatchSchema(operations []string) map[string]any {
	return objectSchema(map[string]any{
		"operation": map[string]any{
			"type":        "string",
			"description": "Operation name routed by this facade tool. See gong_discover_capabilities for the full registry.",
			"enum":        operations,
		},
		"arguments": map[string]any{
			"type":        "object",
			"description": "Arguments forwarded to the routed tool. Schema follows the routed tool's input schema.",
		},
	}, nil)
}

// facadeDispatchArgs is the wire shape every dispatching facade tool accepts:
// `{operation: "...", arguments: {...}}`. The arguments are forwarded
// verbatim to the routed tool.
type facadeDispatchArgs struct {
	Operation string          `json:"operation"`
	Arguments json.RawMessage `json:"arguments"`
}

// isFacadeTool reports whether name is one of the stable facade tool names.
func isFacadeTool(name string) bool {
	switch name {
	case FacadeToolStatus,
		FacadeToolDiscoverCapabilities,
		FacadeToolQuery,
		FacadeToolAnalyze,
		FacadeToolGetEvidence,
		FacadeToolExplainLimitations:
		return true
	}
	return false
}

// executeFacadeTool dispatches one of the six facade tools.
func (s *Server) executeFacadeTool(ctx context.Context, params toolsCallParams) (toolCallResult, error) {
	switch params.Name {
	case FacadeToolStatus:
		return s.executeFacadeStatus(ctx, params.Arguments)
	case FacadeToolDiscoverCapabilities:
		return s.executeFacadeDiscoverCapabilities(params.Arguments)
	case FacadeToolQuery:
		return s.executeFacadeDispatch(ctx, FacadeToolQuery, params.Arguments)
	case FacadeToolAnalyze:
		return s.executeFacadeDispatch(ctx, FacadeToolAnalyze, params.Arguments)
	case FacadeToolGetEvidence:
		return s.executeFacadeDispatch(ctx, FacadeToolGetEvidence, params.Arguments)
	case FacadeToolExplainLimitations:
		return s.executeFacadeExplainLimitations(ctx, params.Arguments)
	}
	return toolCallResult{}, fmt.Errorf("unknown facade tool %q", params.Name)
}

func (s *Server) executeFacadeStatus(ctx context.Context, raw json.RawMessage) (toolCallResult, error) {
	var args struct{}
	if err := decodeArgs(raw, &args); err != nil {
		return toolCallResult{}, err
	}
	op, ok := FacadeOperationByName(OpStatusSync)
	if !ok {
		return toolCallResult{}, fmt.Errorf("facade operation %q is not registered", OpStatusSync)
	}
	routed, err := s.resolveFacadeRoutedTool(op)
	if err != nil {
		return toolCallResult{}, err
	}
	inner, err := s.executeFacadeRouted(ctx, routed, nil)
	if err != nil {
		return toolCallResult{}, err
	}
	return wrapFacadeResult(FacadeToolStatus, op, routed, inner)
}

func (s *Server) executeFacadeDiscoverCapabilities(raw json.RawMessage) (toolCallResult, error) {
	var args struct{}
	if err := decodeArgs(raw, &args); err != nil {
		return toolCallResult{}, err
	}
	ops := FacadeOperations()
	enriched := make([]map[string]any, 0, len(ops))
	for _, op := range ops {
		entry := map[string]any{
			"operation":             op.Name,
			"version":               op.Version,
			"description":           op.Description,
			"facade_tool":           op.FacadeTool,
			"routed_tool":           op.RoutedTool,
			"exposure_level":        op.ExposureLevel,
			"allowed_presets":       op.AllowedPresets,
			"input_schema":          op.InputSchema,
			"routed_tool_available": s.facadeRoutedToolAvailable(op),
		}
		if op.RoutedFallback != "" {
			entry["routed_tool_fallback"] = op.RoutedFallback
		}
		if len(op.Examples) > 0 {
			entry["examples"] = op.Examples
		}
		enriched = append(enriched, entry)
	}
	payload := map[string]any{
		"facade_version": "v1",
		"operations":     enriched,
		"facade_tools": []string{
			FacadeToolStatus,
			FacadeToolDiscoverCapabilities,
			FacadeToolQuery,
			FacadeToolAnalyze,
			FacadeToolGetEvidence,
			FacadeToolExplainLimitations,
		},
		"note": "Top-level individual tools (search_calls, build_call_cohort, search_transcript_segments, build_quote_pack, etc.) remain available for operator and analyst testing alongside this facade.",
	}
	return newToolResult(payload)
}

func (s *Server) executeFacadeDispatch(ctx context.Context, facadeTool string, raw json.RawMessage) (toolCallResult, error) {
	var args facadeDispatchArgs
	if err := decodeArgs(raw, &args); err != nil {
		return toolCallResult{}, err
	}
	if strings.TrimSpace(args.Operation) == "" {
		allowed := operationNames(facadeOperationsForTool(facadeTool))
		return toolCallResult{}, fmt.Errorf("facade tool %q requires an operation; allowed: %s", facadeTool, strings.Join(allowed, ", "))
	}
	op, ok := FacadeOperationByName(args.Operation)
	if !ok {
		allowed := operationNames(facadeOperationsForTool(facadeTool))
		return toolCallResult{}, fmt.Errorf("unknown facade operation %q for %s; allowed: %s", args.Operation, facadeTool, strings.Join(allowed, ", "))
	}
	if op.FacadeTool != facadeTool {
		return toolCallResult{}, fmt.Errorf("operation %q is routed by facade tool %s, not %s", op.Name, op.FacadeTool, facadeTool)
	}
	routed, err := s.resolveFacadeRoutedTool(op)
	if err != nil {
		return toolCallResult{}, err
	}
	inner, err := s.executeFacadeRouted(ctx, routed, args.Arguments)
	if err != nil {
		return toolCallResult{}, err
	}
	return wrapFacadeResult(facadeTool, op, routed, inner)
}

func (s *Server) executeFacadeExplainLimitations(ctx context.Context, raw json.RawMessage) (toolCallResult, error) {
	var args facadeDispatchArgs
	if err := decodeArgs(raw, &args); err != nil {
		return toolCallResult{}, err
	}
	if strings.TrimSpace(args.Operation) == "" {
		return s.facadeHighLevelLimitations()
	}
	op, ok := FacadeOperationByName(args.Operation)
	if !ok {
		return toolCallResult{}, fmt.Errorf("unknown facade operation %q for %s; allowed: %s", args.Operation, FacadeToolExplainLimitations, OpAnalyzeLimitationsExplain)
	}
	if op.FacadeTool != FacadeToolExplainLimitations && op.Name != OpAnalyzeLimitationsExplain {
		return toolCallResult{}, fmt.Errorf("operation %q is not supported by %s", op.Name, FacadeToolExplainLimitations)
	}
	routed, err := s.resolveFacadeRoutedTool(op)
	if err != nil {
		return toolCallResult{}, err
	}
	inner, err := s.executeFacadeRouted(ctx, routed, args.Arguments)
	if err != nil {
		return toolCallResult{}, err
	}
	return wrapFacadeResult(FacadeToolExplainLimitations, op, routed, inner)
}

// facadeHighLevelLimitations returns a static facade-level summary describing
// the current cache, governance, and facade boundaries so a caller invoking
// gong_explain_limitations with no operation gets useful output.
func (s *Server) facadeHighLevelLimitations() (toolCallResult, error) {
	available := make([]string, 0)
	unavailable := make([]string, 0)
	for _, op := range FacadeOperations() {
		if s.facadeRoutedToolAvailable(op) {
			available = append(available, op.Name)
		} else {
			unavailable = append(unavailable, op.Name)
		}
	}
	payload := map[string]any{
		"tool":           FacadeToolExplainLimitations,
		"facade_version": "v1",
		"limitations": []string{
			"The MCP facade routes a stable surface to existing internal tools; it does not introduce new data paths.",
			"Routed tools must be present in the active tool allowlist or preset; otherwise the facade returns a tool error naming the missing routed tool and preset.",
			"AI governance (when configured) and the redacted Postgres serving DB still apply on every routed call; the facade does not bypass either layer.",
			"The facade does not extend Postgres `all-readonly`; broad read-only Postgres exposure remains gated by the existing slice rules.",
			"Top-level individual tools (search_calls, build_call_cohort, search_transcript_segments, build_quote_pack, etc.) remain available for operator and analyst testing.",
		},
		"available_operations":   available,
		"unavailable_operations": unavailable,
	}
	return newToolResult(payload)
}

// resolveFacadeRoutedTool returns the routed tool name for the operation that
// is currently exposed by the server's allowlist. If neither the primary nor
// the fallback is available, it returns a useful error naming both options
// and the active allowlist size so operators can correct the preset.
func (s *Server) resolveFacadeRoutedTool(op FacadeOperation) (string, error) {
	if s.isFacadeRoutedToolAllowed(op.RoutedTool) {
		return op.RoutedTool, nil
	}
	if op.RoutedFallback != "" && s.isFacadeRoutedToolAllowed(op.RoutedFallback) {
		return op.RoutedFallback, nil
	}
	missing := op.RoutedTool
	if op.RoutedFallback != "" {
		missing = fmt.Sprintf("%s (fallback: %s)", op.RoutedTool, op.RoutedFallback)
	}
	allowed := strings.Join(op.AllowedPresets, ", ")
	return "", fmt.Errorf("facade operation %q is not available: routed tool %s is not in the active allowlist; expose it via one of: %s", op.Name, missing, allowed)
}

// facadeRoutedToolAvailable reports whether the routed tool (or its fallback)
// is currently exposed.
func (s *Server) facadeRoutedToolAvailable(op FacadeOperation) bool {
	if s.isFacadeRoutedToolAllowed(op.RoutedTool) {
		return true
	}
	if op.RoutedFallback != "" && s.isFacadeRoutedToolAllowed(op.RoutedFallback) {
		return true
	}
	return false
}

func (s *Server) isFacadeRoutedToolAllowed(name string) bool {
	if s.isToolAllowed(name) {
		return true
	}
	if len(s.facadeRoutedToolNames) == 0 {
		return false
	}
	_, ok := s.facadeRoutedToolNames[name]
	return ok
}

// executeFacadeRouted invokes an existing tool by name, reusing the same
// dispatch path (and governance/business-analysis routing) the server uses
// for direct tools/call requests.
func (s *Server) executeFacadeRouted(ctx context.Context, name string, args json.RawMessage) (toolCallResult, error) {
	if isFacadeTool(name) {
		return toolCallResult{}, fmt.Errorf("facade routed tool %q must not be another facade tool", name)
	}
	if name == internalRoutedToolListAIHighlights {
		return s.executeListCallAIHighlights(ctx, args)
	}
	if isBusinessAnalysisTool(name) {
		return s.executeBusinessAnalysisTool(ctx, toolsCallParams{Name: name, Arguments: args})
	}
	return s.executeNonFacadeTool(ctx, toolsCallParams{Name: name, Arguments: args})
}

// AI-highlights operation limits. These are intentionally low: highlights are
// a narrow accelerator surface, not a primary evidence path.
const (
	maxAIHighlightCallIDs   = 25
	defaultAIHighlightLimit = 25
	maxAIHighlightLimit     = 100
	maxAIHighlightCallIDLen = 200
)

type listCallAIHighlightsArgs struct {
	CallIDs []string `json:"call_ids"`
	Limit   int      `json:"limit"`
}

func (s *Server) executeListCallAIHighlights(ctx context.Context, raw json.RawMessage) (toolCallResult, error) {
	var args listCallAIHighlightsArgs
	if err := decodeArgs(raw, &args); err != nil {
		return toolCallResult{}, err
	}
	ids := make([]string, 0, len(args.CallIDs))
	seen := make(map[string]struct{}, len(args.CallIDs))
	for _, raw := range args.CallIDs {
		v := strings.TrimSpace(raw)
		if v == "" {
			continue
		}
		if len(v) > maxAIHighlightCallIDLen {
			return toolCallResult{}, fmt.Errorf("call_ids entries must be %d characters or fewer", maxAIHighlightCallIDLen)
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		ids = append(ids, v)
	}
	if len(ids) == 0 {
		return toolCallResult{}, fmt.Errorf("call_ids is required and must contain at least one non-empty id")
	}
	if len(ids) > maxAIHighlightCallIDs {
		return toolCallResult{}, fmt.Errorf("call_ids must include no more than %d ids; got %d", maxAIHighlightCallIDs, len(ids))
	}

	limit := args.Limit
	if limit <= 0 {
		limit = defaultAIHighlightLimit
	}
	if limit > maxAIHighlightLimit {
		limit = maxAIHighlightLimit
	}

	rows, err := s.store.ListAIHighlights(ctx, sqlite.AIHighlightListParams{CallIDs: ids, Limit: limit})
	if err != nil {
		return toolCallResult{}, err
	}

	suppressedFiltered := 0
	cleaned := make([]sqlite.AIHighlightRow, 0, len(rows))
	for _, row := range rows {
		if s.isSuppressedCall(row.CallID) {
			suppressedFiltered++
			continue
		}
		cleaned = append(cleaned, row)
		if len(cleaned) >= limit {
			break
		}
	}

	requestedSet := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		requestedSet[id] = struct{}{}
	}
	withRows := make(map[string]struct{}, len(cleaned))
	for _, row := range cleaned {
		withRows[row.CallID] = struct{}{}
	}
	missing := make([]string, 0)
	for _, id := range ids {
		if _, ok := withRows[id]; ok {
			continue
		}
		if s.isSuppressedCall(id) {
			continue
		}
		missing = append(missing, id)
	}

	payload := map[string]any{
		"rows":                      cleaned,
		"count":                     len(cleaned),
		"requested_call_ids":        ids,
		"call_ids_without_rows":     missing,
		"suppressed_filtered_count": suppressedFiltered,
		"limits": map[string]any{
			"limit":        limit,
			"max_limit":    maxAIHighlightLimit,
			"max_call_ids": maxAIHighlightCallIDs,
		},
		"caveats": []string{
			"Highlights are Gong AI-generated accelerators; transcript quotes remain primary evidence.",
			"Rows return only the typed call_id, highlight_index, highlight_type, highlight_text, source_path, and updated_at columns; raw highlight JSON is not exposed.",
			"Lookups require explicit call_ids; this operation performs no raw account or customer enumeration and does not list the full call set.",
			"Restricted calls remain absent because the redacted Postgres serving DB has no rows for them; runtime-suppressed calls are filtered before rows leave the server.",
			"call_ai_highlights only exists in the Postgres serving DB; SQLite-backed deployments will return zero rows.",
		},
	}
	return newToolResult(payload)
}

// wrapFacadeResult tags the inner tool result with facade metadata so callers
// can confirm which operation/routed tool answered without re-parsing the
// inner payload. If the inner result was already an isError envelope, we
// preserve that and surface the error text in the wrapper.
func wrapFacadeResult(facadeTool string, op FacadeOperation, routed string, inner toolCallResult) (toolCallResult, error) {
	wrapper := map[string]any{
		"facade_tool":   facadeTool,
		"operation":     op.Name,
		"version":       op.Version,
		"routed_tool":   routed,
		"facade_status": "ok",
	}
	if inner.IsError {
		wrapper["facade_status"] = "tool_error"
	}
	if len(inner.Content) > 0 {
		first := inner.Content[0]
		if first.Type == "text" {
			var parsed any
			if err := json.Unmarshal([]byte(first.Text), &parsed); err == nil {
				wrapper["result"] = parsed
			} else {
				wrapper["result_text"] = first.Text
			}
		}
	}
	out, err := newToolResult(wrapper)
	if err != nil {
		return toolCallResult{}, err
	}
	if inner.IsError {
		out.IsError = true
	}
	return out, nil
}

func operationNames(ops []FacadeOperation) []string {
	out := make([]string, 0, len(ops))
	for _, op := range ops {
		out = append(out, op.Name)
	}
	return out
}
