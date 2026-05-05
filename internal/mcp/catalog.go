package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type ToolPresetInfo struct {
	Name        string   `json:"name"`
	Aliases     []string `json:"aliases,omitempty"`
	Purpose     string   `json:"purpose"`
	Tools       []string `json:"tools"`
	ToolCount   int      `json:"tool_count"`
	Recommended string   `json:"recommended_for"`
}

func ParseToolAllowlist(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	catalog := make(map[string]struct{}, len(ToolCatalog()))
	for _, tool := range ToolCatalog() {
		catalog[tool.Name] = struct{}{}
	}

	seen := make(map[string]struct{})
	names := make([]string, 0)
	for _, piece := range strings.Split(raw, ",") {
		name := strings.TrimSpace(piece)
		if name == "" {
			continue
		}
		if _, ok := catalog[name]; !ok {
			return nil, fmt.Errorf("unknown tool %q", name)
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("no valid tool names provided")
	}
	return names, nil
}

func ExpandToolPreset(name string) ([]string, error) {
	switch normalizedToolPresetName(name) {
	case "business-pilot", "strict-business-pilot":
		return copyStrings([]string{
			"get_sync_status",
			"summarize_call_facts",
			"summarize_calls_by_lifecycle",
			"rank_transcript_backlog",
		}), nil
	case "operator-smoke":
		return copyStrings([]string{
			"get_sync_status",
			"search_calls",
			"search_transcript_segments",
			"get_call",
			"rank_transcript_backlog",
		}), nil
	case "analyst-core", "postgres-analyst-core":
		return copyStrings([]string{
			"get_sync_status",
			"search_calls",
			"get_call",
			"list_crm_object_types",
			"list_crm_fields",
			"list_crm_integrations",
			"list_cached_crm_schema_objects",
			"list_cached_crm_schema_fields",
			"list_gong_settings",
			"list_scorecards",
			"get_scorecard",
			"summarize_scorecard_activity",
			"get_business_profile",
			"list_business_concepts",
			"list_lifecycle_buckets",
			"summarize_calls_by_lifecycle",
			"search_calls_by_lifecycle",
			"prioritize_transcripts_by_lifecycle",
			"summarize_call_facts",
			"rank_transcript_backlog",
			"search_transcript_segments",
		}), nil
	case "analyst-business-core", "postgres-analyst-business-core":
		return copyStrings([]string{
			"get_sync_status",
			"search_calls",
			"get_call",
			"list_crm_object_types",
			"list_crm_fields",
			"list_crm_integrations",
			"list_cached_crm_schema_objects",
			"list_cached_crm_schema_fields",
			"list_gong_settings",
			"list_scorecards",
			"get_scorecard",
			"summarize_scorecard_activity",
			"get_business_profile",
			"list_business_concepts",
			"list_lifecycle_buckets",
			"summarize_calls_by_lifecycle",
			"search_calls_by_lifecycle",
			"prioritize_transcripts_by_lifecycle",
			"summarize_call_facts",
			"rank_transcript_backlog",
			"search_transcript_segments",
			"search_transcripts_by_call_facts",
			"search_transcript_quotes_with_attribution",
			"build_call_cohort",
			"inspect_call_cohort",
			"search_calls_by_filters",
			"summarize_calls_by_filters",
			"search_transcripts_by_filters",
			"discover_themes_in_cohort",
			"summarize_themes_by_dimension",
			"extract_theme_quotes",
			"search_quotes_in_cohort",
			"diagnose_attribution_coverage",
			"score_cohort_evidence_quality",
			"explain_analysis_limitations",
			"suggest_filter_refinements",
		}), nil
	case "analyst", "analyst-expansion":
		tools := []string{
			"get_sync_status",
			"list_crm_object_types",
			"list_crm_fields",
			"get_business_profile",
			"list_business_concepts",
			"list_unmapped_crm_fields",
			"analyze_late_stage_crm_signals",
			"opportunities_missing_transcripts",
			"search_transcripts_by_crm_context",
			"opportunity_call_summary",
			"crm_field_population_matrix",
			"list_lifecycle_buckets",
			"summarize_calls_by_lifecycle",
			"prioritize_transcripts_by_lifecycle",
			"compare_lifecycle_crm_fields",
			"summarize_call_facts",
			"rank_transcript_backlog",
			"search_transcript_segments",
			"search_transcripts_by_call_facts",
			"search_transcript_quotes_with_attribution",
		}
		tools = append(tools, BusinessAnalysisToolNames()...)
		return copyStrings(tools), nil
	case "governance-search":
		return copyStrings(governanceCompatibleToolNames), nil
	case "all-readonly", "all-tools", "all":
		return ToolCatalogNames(), nil
	default:
		return nil, fmt.Errorf("unknown tool preset %q; available presets: business-pilot, strict-business-pilot, operator-smoke, analyst-core, analyst-business-core, analyst, analyst-expansion, governance-search, all-readonly", strings.TrimSpace(name))
	}
}

func ToolPresetCatalog() []ToolPresetInfo {
	defs := []struct {
		name        string
		aliases     []string
		purpose     string
		recommended string
	}{
		{
			name:        "business-pilot",
			aliases:     []string{"strict-business-pilot"},
			purpose:     "Narrow status and aggregate tools for first business-user pilots.",
			recommended: "business users after operator setup",
		},
		{
			name:        "operator-smoke",
			purpose:     "Minimal search/status set for deployment smoke tests.",
			recommended: "IT and platform operators validating connectivity",
		},
		{
			name:        "analyst-core",
			aliases:     []string{"postgres-analyst-core"},
			purpose:     "Postgres-supported analyst starter surface over core call, CRM context, profile, lifecycle, and transcript search tools.",
			recommended: "approved analysts validating shared Postgres deployments before full analyst parity",
		},
		{
			name:        "analyst-business-core",
			aliases:     []string{"postgres-analyst-business-core"},
			purpose:     "Postgres-supported analyst business-analysis starter surface over bounded cohort, transcript evidence, and dimension tools.",
			recommended: "approved analysts validating shared Postgres business-analysis workflows before full analyst parity",
		},
		{
			name:        "analyst",
			aliases:     []string{"analyst-expansion"},
			purpose:     "Broader bounded evidence and profile-aware analysis without admin/config-heavy tools.",
			recommended: "SQLite analyst sessions, or Postgres only after full analyst parity is complete",
		},
		{
			name:        "governance-search",
			purpose:     "Raw-DB AI-governance-compatible search tools only.",
			recommended: "operator testing with GONGMCP_AI_GOVERNANCE_CONFIG",
		},
		{
			name:        "all-readonly",
			aliases:     []string{"all", "all-tools"},
			purpose:     "Full read-only MCP catalog.",
			recommended: "trusted SQLite admin/analyst sessions; Postgres only after full read-only parity is complete",
		},
	}
	out := make([]ToolPresetInfo, 0, len(defs))
	for _, def := range defs {
		tools, err := ExpandToolPreset(def.name)
		if err != nil {
			continue
		}
		out = append(out, ToolPresetInfo{
			Name:        def.name,
			Aliases:     copyStrings(def.aliases),
			Purpose:     def.purpose,
			Tools:       tools,
			ToolCount:   len(tools),
			Recommended: def.recommended,
		})
	}
	return out
}

func WriteToolPresetCatalog(w io.Writer) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(struct {
		Presets []ToolPresetInfo `json:"presets"`
	}{
		Presets: ToolPresetCatalog(),
	})
}

func ToolCatalogNames() []string {
	catalog := ToolCatalog()
	names := make([]string, 0, len(catalog))
	for _, tool := range catalog {
		names = append(names, tool.Name)
	}
	return names
}

func ValidateGovernanceAllowlist(allowlist []string) error {
	if len(allowlist) == 0 {
		return fmt.Errorf("AI governance requires an explicit MCP tool preset or allowlist")
	}
	safe := make(map[string]struct{}, len(governanceCompatibleToolNames))
	for _, name := range governanceCompatibleToolNames {
		safe[name] = struct{}{}
	}
	for _, name := range allowlist {
		if _, ok := safe[name]; !ok {
			return fmt.Errorf("tool %q is not supported while AI governance filtering is active", name)
		}
	}
	return nil
}

var governanceCompatibleToolNames = []string{
	"search_calls",
	"get_call",
	"search_transcripts_by_crm_context",
	"search_calls_by_lifecycle",
	"prioritize_transcripts_by_lifecycle",
	"rank_transcript_backlog",
	"search_transcript_segments",
	"search_transcripts_by_call_facts",
	"search_transcript_quotes_with_attribution",
	"missing_transcripts",
}

func normalizedToolPresetName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func copyStrings(values []string) []string {
	out := make([]string, len(values))
	copy(out, values)
	return out
}
