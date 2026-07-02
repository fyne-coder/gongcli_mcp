package mcp

import (
	"strings"
	"testing"
)

func TestBusinessSignalTopicAliasesDefaultIntegrationExcludesProcurementTerms(t *testing.T) {
	t.Parallel()

	expanded := strings.ToLower(strings.Join(businessSignalTopicAliases(OpExtractBuyerQuestions, "integration", defaultTopicPackSet()), ","))
	for _, term := range []string{"punchout", "punch out", "eprocurement", "coupa", "ariba", "jaggaer"} {
		if strings.Contains(expanded, term) {
			t.Fatalf("default integration aliases should not include %q: %s", term, expanded)
		}
	}
	if !strings.Contains(expanded, "erp integration") || !strings.Contains(expanded, "api integration") {
		t.Fatalf("default integration aliases should keep generic B2B terms: %s", expanded)
	}
}

func TestBusinessSignalTopicAliasesProcurementPackAddsProcurementTerms(t *testing.T) {
	t.Parallel()

	packs, err := resolveTopicPacks([]string{"procurement"})
	if err != nil {
		t.Fatalf("resolveTopicPacks: %v", err)
	}
	expanded := strings.ToLower(strings.Join(businessSignalTopicAliases(OpExtractBuyerQuestions, "integration", packs), ","))
	if !strings.Contains(expanded, "punchout integration") {
		t.Fatalf("procurement pack integration aliases missing punchout integration: %s", expanded)
	}
	punchoutExpanded := strings.ToLower(strings.Join(businessSignalTopicAliases(OpExtractBuyerQuestions, "punchout", packs), ","))
	for _, term := range []string{"punch out", "eprocurement", "coupa", "ariba", "jaggaer"} {
		if !strings.Contains(punchoutExpanded, term) {
			t.Fatalf("procurement pack punchout aliases missing %q: %s", term, punchoutExpanded)
		}
	}
}

func TestBusinessSignalTopicsDefaultExcludePunchout(t *testing.T) {
	t.Parallel()

	topics := businessSignalTopics(OpExtractBuyerQuestions, businessSignalExtractionArgs{}, defaultTopicPackSet())
	joined := strings.ToLower(strings.Join(topics, ","))
	if strings.Contains(joined, "punchout") {
		t.Fatalf("default buyer-question topics should not include punchout: %s", joined)
	}
	for _, want := range []string{"pricing", "implementation", "integration", "security", "support", "timeline", "data", "erp"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("default buyer-question topics missing %q: %s", want, joined)
		}
	}
}

func TestBusinessSignalTopicsProcurementPackIncludesPunchout(t *testing.T) {
	t.Parallel()

	packs, err := resolveTopicPacks([]string{"procurement"})
	if err != nil {
		t.Fatalf("resolveTopicPacks: %v", err)
	}
	topics := businessSignalTopics(OpExtractBuyerQuestions, businessSignalExtractionArgs{}, packs)
	joined := strings.ToLower(strings.Join(topics, ","))
	if !strings.Contains(joined, "punchout") {
		t.Fatalf("procurement pack default topics should include punchout: %s", joined)
	}
}

func TestResolveTopicPacksRejectsUnknownPack(t *testing.T) {
	t.Parallel()

	_, err := resolveTopicPacks([]string{"unknown_pack"})
	if err == nil {
		t.Fatal("expected unknown topic_pack error")
	}
	if !strings.Contains(err.Error(), "unknown topic_pack") {
		t.Fatalf("unexpected error: %v", err)
	}
}
