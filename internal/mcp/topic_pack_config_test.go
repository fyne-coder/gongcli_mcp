package mcp

import (
	"strings"
	"testing"
)

func TestParseBusinessTopicPacksYAMLValidProductReadiness(t *testing.T) {
	t.Parallel()

	raw := []byte(`
topic_packs:
  product_readiness:
    description: "Optional local pack for product readiness reviews."
    aliases:
      implementation:
        - launch readiness
        - training plan
    default_topics:
      extract.buyer_questions:
        - implementation
        - launch readiness
      extract.objection_signals:
        - implementation risk
`)
	registry, err := ParseBusinessTopicPacksYAML(raw)
	if err != nil {
		t.Fatalf("ParseBusinessTopicPacksYAML: %v", err)
	}
	if got := registry.CustomPackNames(); len(got) != 1 || got[0] != "product_readiness" {
		t.Fatalf("CustomPackNames=%v", got)
	}
	pack, ok := registry.customPack("product_readiness")
	if !ok {
		t.Fatal("expected product_readiness pack")
	}
	if pack.Description == "" {
		t.Fatal("expected description")
	}
	if got := pack.Aliases["implementation"]; len(got) != 2 {
		t.Fatalf("implementation aliases=%v", got)
	}
	if got := pack.DefaultTopics[OpExtractBuyerQuestions]; len(got) != 2 {
		t.Fatalf("buyer default topics=%v", got)
	}
}

func TestParseBusinessTopicPacksYAMLRejectsReservedBuiltinName(t *testing.T) {
	t.Parallel()

	_, err := ParseBusinessTopicPacksYAML([]byte(`
topic_packs:
  technical_readiness:
    aliases:
      integration:
        - sandbox testing
`))
	if err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("expected reserved built-in error, got %v", err)
	}
}

func TestParseBusinessTopicPacksYAMLRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	_, err := ParseBusinessTopicPacksYAML([]byte(`
topic_packs:
  product_readiness:
    description: "Optional local pack."
    alias:
      implementation:
        - launch readiness
`))
	if err == nil || !strings.Contains(err.Error(), "field alias not found") {
		t.Fatalf("expected strict unknown field error, got %v", err)
	}
}

func TestParseBusinessTopicPacksYAMLRejectsDescriptionOnlyPack(t *testing.T) {
	t.Parallel()

	_, err := ParseBusinessTopicPacksYAML([]byte(`
topic_packs:
  product_readiness:
    description: "Optional local pack."
`))
	if err == nil || !strings.Contains(err.Error(), "must define aliases and/or default_topics") {
		t.Fatalf("expected inert pack error, got %v", err)
	}
}

func TestParseBusinessTopicPacksYAMLRejectsOversizedAliasList(t *testing.T) {
	t.Parallel()

	aliases := make([]string, maxCustomTopicPackAliasesPerKey+1)
	for i := range aliases {
		aliases[i] = "alias term"
	}
	var b strings.Builder
	b.WriteString("topic_packs:\n  integration_review:\n    aliases:\n      integration:\n")
	for _, alias := range aliases {
		b.WriteString("        - ")
		b.WriteString(alias)
		b.WriteByte('\n')
	}
	_, err := ParseBusinessTopicPacksYAML([]byte(b.String()))
	if err == nil || !strings.Contains(err.Error(), "maximum") {
		t.Fatalf("expected maximum alias error, got %v", err)
	}
}

func TestParseBusinessTopicPacksYAMLRejectsFTSUnsafeValues(t *testing.T) {
	t.Parallel()

	_, err := ParseBusinessTopicPacksYAML([]byte(`
topic_packs:
  product_readiness:
    aliases:
      implementation:
        - R&D rollout
`))
	if err == nil || !strings.Contains(err.Error(), "may contain only letters, numbers, spaces, and simple separators") {
		t.Fatalf("expected FTS-safe value error, got %v", err)
	}
}

func TestParseBusinessTopicPacksYAMLRejectsUnknownDefaultOperation(t *testing.T) {
	t.Parallel()

	_, err := ParseBusinessTopicPacksYAML([]byte(`
topic_packs:
  integration_review:
    default_topics:
      analyze.themes.discover:
        - pricing
`))
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("expected unsupported operation error, got %v", err)
	}
}

func TestParseBusinessTopicPacksYAMLRejectsNormalizedAliasKeyDuplicates(t *testing.T) {
	t.Parallel()

	_, err := ParseBusinessTopicPacksYAML([]byte(`
topic_packs:
  product_readiness:
    aliases:
      Implementation:
        - launch readiness
      implementation:
        - training plan
`))
	if err == nil || !strings.Contains(err.Error(), "duplicates alias key") {
		t.Fatalf("expected normalized alias duplicate error, got %v", err)
	}
}

func TestParseBusinessTopicPacksYAMLRejectsNormalizedDefaultOperationDuplicates(t *testing.T) {
	t.Parallel()

	_, err := ParseBusinessTopicPacksYAML([]byte(`
topic_packs:
  product_readiness:
    default_topics:
      Extract.Buyer_Questions:
        - implementation
      extract.buyer_questions:
        - launch readiness
`))
	if err == nil || !strings.Contains(err.Error(), "duplicates operation") {
		t.Fatalf("expected normalized operation duplicate error, got %v", err)
	}
}

func TestCustomTopicPackAliasesAndDefaultsAreAdditive(t *testing.T) {
	t.Parallel()

	registry, err := ParseBusinessTopicPacksYAML([]byte(`
topic_packs:
  product_readiness:
    aliases:
      implementation:
        - launch readiness
    default_topics:
      extract.buyer_questions:
        - launch readiness
`))
	if err != nil {
		t.Fatalf("ParseBusinessTopicPacksYAML: %v", err)
	}
	packs, err := registry.ResolveTopicPacks([]string{"product_readiness"})
	if err != nil {
		t.Fatalf("ResolveTopicPacks: %v", err)
	}
	aliases := businessSignalTopicAliases(OpExtractBuyerQuestions, "implementation", packs)
	joined := strings.ToLower(strings.Join(aliases, ","))
	for _, term := range []string{"rollout", "launch readiness"} {
		if !strings.Contains(joined, term) {
			t.Fatalf("aliases missing %q: %s", term, joined)
		}
	}
	topics := businessSignalTopics(OpExtractBuyerQuestions, businessSignalExtractionArgs{}, packs)
	joinedTopics := strings.ToLower(strings.Join(topics, ","))
	for _, term := range []string{"launch readiness", "pricing", "implementation"} {
		if !strings.Contains(joinedTopics, term) {
			t.Fatalf("default topics missing %q: %v", term, topics)
		}
	}
}

func TestCustomDefaultTopicsSkippedWhenExplicitTopicsProvided(t *testing.T) {
	t.Parallel()

	registry, err := ParseBusinessTopicPacksYAML([]byte(`
topic_packs:
  product_readiness:
    default_topics:
      extract.buyer_questions:
        - launch readiness
`))
	if err != nil {
		t.Fatalf("ParseBusinessTopicPacksYAML: %v", err)
	}
	packs, err := registry.ResolveTopicPacks([]string{"product_readiness"})
	if err != nil {
		t.Fatalf("ResolveTopicPacks: %v", err)
	}
	topics := businessSignalTopics(OpExtractBuyerQuestions, businessSignalExtractionArgs{
		Topics: []string{"pricing"},
	}, packs)
	if len(topics) != 1 || topics[0] != "pricing" {
		t.Fatalf("explicit topics should win: %v", topics)
	}
}

func TestResolveTopicPacksUnknownIncludesConfiguredNames(t *testing.T) {
	t.Parallel()

	registry, err := ParseBusinessTopicPacksYAML([]byte(`
topic_packs:
  product_readiness:
    aliases:
      pricing:
        - budget review
`))
	if err != nil {
		t.Fatalf("ParseBusinessTopicPacksYAML: %v", err)
	}
	_, err = registry.ResolveTopicPacks([]string{"missing_pack"})
	if err == nil {
		t.Fatal("expected unknown topic_pack error")
	}
	msg := err.Error()
	for _, name := range []string{"generic_b2b", "technical_readiness", "product_readiness"} {
		if !strings.Contains(msg, name) {
			t.Fatalf("error missing %q: %s", name, msg)
		}
	}
}

func TestFacadeDiscoverCapabilitiesAdvertisesConfiguredTopicPacks(t *testing.T) {
	t.Parallel()

	registry, err := ParseBusinessTopicPacksYAML([]byte(`
topic_packs:
  product_readiness:
    description: "Local readiness vocabulary."
    aliases:
      implementation:
        - launch readiness
`))
	if err != nil {
		t.Fatalf("ParseBusinessTopicPacksYAML: %v", err)
	}
	server := NewServerWithOptions(nil, "test", "dev", WithBusinessTopicPacks(registry))
	result, err := server.executeFacadeDiscoverCapabilities(mustJSON(t, map[string]any{"detail": "full"}))
	if err != nil {
		t.Fatalf("executeFacadeDiscoverCapabilities: %v", err)
	}
	payload := decodeDiscoverCapabilitiesPayload(t, result)
	meta, _ := payload["business_topic_packs"].(map[string]any)
	if meta == nil {
		t.Fatalf("missing business_topic_packs metadata: %v", payload)
	}
	names, _ := meta["configured_pack_names"].([]any)
	if len(names) != 1 || names[0] != "product_readiness" {
		t.Fatalf("configured_pack_names=%v", names)
	}
	descriptions, _ := meta["configured_pack_descriptions"].(map[string]any)
	if descriptions["product_readiness"] != "Local readiness vocabulary." {
		t.Fatalf("configured_pack_descriptions=%v", descriptions)
	}
	operations, _ := payload["operations"].([]any)
	for _, raw := range operations {
		entry, _ := raw.(map[string]any)
		if entry["operation"] != OpExtractBuyerQuestions {
			continue
		}
		schema, _ := entry["input_schema"].(map[string]any)
		props, _ := schema["properties"].(map[string]any)
		topicPacks, _ := props["topic_packs"].(map[string]any)
		items, _ := topicPacks["items"].(map[string]any)
		enumValues, _ := items["enum"].([]any)
		found := false
		for _, value := range enumValues {
			if value == "product_readiness" {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("extract.buyer_questions schema enum missing product_readiness: %v", enumValues)
		}
		return
	}
	t.Fatal("extract.buyer_questions operation not found in discovery payload")
}
