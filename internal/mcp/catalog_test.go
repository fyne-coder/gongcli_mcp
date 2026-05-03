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
