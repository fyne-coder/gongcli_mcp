package mcp

import "testing"

func TestToolCatalogInvariants(t *testing.T) {
	t.Parallel()

	catalogNames := ToolCatalogNames()
	if len(catalogNames) == 0 {
		t.Fatal("ToolCatalogNames returned no tools")
	}

	catalog := make(map[string]struct{}, len(catalogNames))
	for _, name := range catalogNames {
		if name == "" {
			t.Fatal("ToolCatalogNames returned an empty tool name")
		}
		if _, ok := catalog[name]; ok {
			t.Fatalf("ToolCatalogNames contains duplicate tool %q", name)
		}
		catalog[name] = struct{}{}
	}

	presets := ToolPresetCatalog()
	if len(presets) == 0 {
		t.Fatal("ToolPresetCatalog returned no presets")
	}

	seenPresetNames := make(map[string]string)
	for _, preset := range presets {
		if preset.Name == "" {
			t.Fatal("ToolPresetCatalog returned a preset with an empty name")
		}
		if preset.ToolCount != len(preset.Tools) {
			t.Fatalf("preset %q tool_count=%d len(tools)=%d", preset.Name, preset.ToolCount, len(preset.Tools))
		}

		registerPresetName(t, seenPresetNames, preset.Name, preset.Name)
		for _, alias := range preset.Aliases {
			registerPresetName(t, seenPresetNames, alias, preset.Name)
		}

		canonicalTools, err := ExpandToolPreset(preset.Name)
		if err != nil {
			t.Fatalf("ExpandToolPreset(%q) returned error: %v", preset.Name, err)
		}
		assertStringSlicesEqual(t, canonicalTools, preset.Tools, "catalog tools for "+preset.Name)

		for _, tool := range preset.Tools {
			if _, ok := catalog[tool]; !ok {
				t.Fatalf("preset %q references unknown tool %q", preset.Name, tool)
			}
		}

		for _, alias := range preset.Aliases {
			aliasTools, err := ExpandToolPreset(alias)
			if err != nil {
				t.Fatalf("ExpandToolPreset(%q) alias for %q returned error: %v", alias, preset.Name, err)
			}
			assertStringSlicesEqual(t, aliasTools, canonicalTools, "alias "+alias+" for "+preset.Name)
		}
	}

	allReadonly, err := ExpandToolPreset("all-readonly")
	if err != nil {
		t.Fatalf("ExpandToolPreset(all-readonly) returned error: %v", err)
	}
	assertStringSlicesEqual(t, allReadonly, catalogNames, "all-readonly catalog")

	governanceTools, err := ExpandToolPreset("governance-search")
	if err != nil {
		t.Fatalf("ExpandToolPreset(governance-search) returned error: %v", err)
	}
	if err := ValidateGovernanceAllowlist(governanceTools); err != nil {
		t.Fatalf("governance-search preset rejected by governance validator: %v", err)
	}

	facadeTools, err := ExpandToolPreset("analyst-facade")
	if err != nil {
		t.Fatalf("ExpandToolPreset(analyst-facade) returned error: %v", err)
	}
	assertStringSlicesEqual(t, facadeTools, FacadeToolNames(), "analyst-facade visible tools")
	facadeRoutedTools, err := ExpandToolPresetFacadeRoutedTools("analyst-facade")
	if err != nil {
		t.Fatalf("ExpandToolPresetFacadeRoutedTools(analyst-facade) returned error: %v", err)
	}
	analystTools, err := ExpandToolPreset("analyst")
	if err != nil {
		t.Fatalf("ExpandToolPreset(analyst) returned error: %v", err)
	}
	wantFacadeRoutedTools := append(copyStrings(analystTools), internalRoutedToolListAIHighlights, internalRoutedToolQuestionAnswer)
	assertStringSlicesEqual(t, facadeRoutedTools, wantFacadeRoutedTools, "analyst-facade hidden routed tools")

	for _, preset := range []string{"analyst-business-core", "analyst", "redacted-all-readonly", "all-readonly"} {
		visibleTools, err := ExpandToolPreset(preset)
		if err != nil {
			t.Fatalf("ExpandToolPreset(%q) returned error: %v", preset, err)
		}
		routedTools, err := ExpandToolPresetFacadeRoutedTools(preset)
		if err != nil {
			t.Fatalf("ExpandToolPresetFacadeRoutedTools(%q) returned error: %v", preset, err)
		}
		want := append(copyStrings(visibleTools), internalRoutedToolListAIHighlights, internalRoutedToolQuestionAnswer)
		assertStringSlicesEqual(t, routedTools, want, preset+" hidden facade routed tools")
	}
}

func TestBusinessWorkbenchPresetExposesOnlyFacadeTools(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"business-workbench", "analyst-facade", "facade-analyst"} {
		tools, err := ExpandToolPreset(name)
		if err != nil {
			t.Fatalf("ExpandToolPreset(%q) returned error: %v", name, err)
		}
		assertStringSlicesEqual(t, tools, FacadeToolNames(), "business-workbench visible tools via "+name)
	}

	routed, err := ExpandToolPresetFacadeRoutedTools("business-workbench")
	if err != nil {
		t.Fatalf("ExpandToolPresetFacadeRoutedTools(business-workbench) returned error: %v", err)
	}
	analyst, err := ExpandToolPreset("analyst")
	if err != nil {
		t.Fatalf("ExpandToolPreset(analyst) returned error: %v", err)
	}
	wantRouted := append(copyStrings(analyst), internalRoutedToolListAIHighlights, internalRoutedToolQuestionAnswer)
	assertStringSlicesEqual(t, routed, wantRouted, "business-workbench hidden facade routed tools")

	grants, err := ExpandToolPresetReaderGrantTools("business-workbench")
	if err != nil {
		t.Fatalf("ExpandToolPresetReaderGrantTools(business-workbench) returned error: %v", err)
	}
	assertStringSlicesEqual(t, grants, wantRouted, "business-workbench reader grant tools")

	presets := ToolPresetCatalog()
	var entry ToolPresetInfo
	for _, info := range presets {
		if info.Name == "business-workbench" {
			entry = info
			break
		}
	}
	if entry.Name == "" {
		t.Fatalf("ToolPresetCatalog missing business-workbench entry; presets=%v", presets)
	}
	if entry.ToolCount != 6 || len(entry.Tools) != 6 {
		t.Fatalf("business-workbench preset tool count=%d want 6 (six facade tools); tools=%v", entry.ToolCount, entry.Tools)
	}
	wantAliases := map[string]struct{}{"analyst-facade": {}, "facade-analyst": {}}
	for _, alias := range entry.Aliases {
		delete(wantAliases, alias)
	}
	if len(wantAliases) != 0 {
		t.Fatalf("business-workbench preset missing analyst-facade/facade-analyst aliases; got %v", entry.Aliases)
	}
}

func TestAnalystPresetExposesScorecardInventoryTools(t *testing.T) {
	t.Parallel()

	for _, preset := range []string{"analyst", "analyst-expansion"} {
		tools, err := ExpandToolPreset(preset)
		if err != nil {
			t.Fatalf("ExpandToolPreset(%q) returned error: %v", preset, err)
		}
		for _, want := range []string{"list_scorecards", "get_scorecard"} {
			found := false
			for _, name := range tools {
				if name == want {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("preset %q missing scorecard inventory tool %q in %v", preset, want, tools)
			}
		}
		for _, blocked := range []string{"summarize_scorecard_activity"} {
			for _, name := range tools {
				if name == blocked {
					t.Fatalf("preset %q must not expose %q (Phase 13g keeps activity aggregates in analyst-core/analyst-business-core)", preset, blocked)
				}
			}
		}
	}
}

func TestRedactedAllReadonlyPresetExposesReviewedPostgresSearchSurface(t *testing.T) {
	t.Parallel()

	tools, err := ExpandToolPreset("redacted-all-readonly")
	if err != nil {
		t.Fatalf("ExpandToolPreset(redacted-all-readonly) returned error: %v", err)
	}
	for _, want := range []string{
		"gong_query",
		"gong_analyze",
		"search_calls",
		"get_call",
		"search_calls_by_lifecycle",
		"search_crm_field_values",
		"search_transcript_segments",
		"search_transcripts_by_call_facts",
		"search_transcripts_by_crm_context",
		"search_transcript_quotes_with_attribution",
		"list_crm_integrations",
		"list_cached_crm_schema_objects",
		"list_cached_crm_schema_fields",
		"list_gong_settings",
		"list_scorecards",
		"get_scorecard",
		"summarize_scorecard_activity",
		"missing_transcripts",
	} {
		if !containsString(tools, want) {
			t.Fatalf("redacted-all-readonly missing reviewed Postgres tool %q in %v", want, tools)
		}
	}
	for _, name := range BusinessAnalysisToolNames() {
		if !containsString(tools, name) {
			t.Fatalf("redacted-all-readonly missing business-analysis tool %q", name)
		}
	}
	if len(tools) == 0 {
		t.Fatal("redacted-all-readonly returned no tools")
	}
}

func registerPresetName(t *testing.T, seen map[string]string, name, preset string) {
	t.Helper()

	normalized := normalizedToolPresetName(name)
	if normalized == "" {
		t.Fatalf("preset %q includes an empty normalized name or alias %q", preset, name)
	}
	if existing, ok := seen[normalized]; ok {
		t.Fatalf("preset name or alias %q for %q duplicates normalized name used by %q", name, preset, existing)
	}
	seen[normalized] = preset
}

func assertStringSlicesEqual(t *testing.T, got, want []string, label string) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("%s len=%d want %d\ngot:  %v\nwant: %v", label, len(got), len(want), got, want)
	}
	for idx := range want {
		if got[idx] != want[idx] {
			t.Fatalf("%s[%d]=%q want %q\ngot:  %v\nwant: %v", label, idx, got[idx], want[idx], got, want)
		}
	}
}
