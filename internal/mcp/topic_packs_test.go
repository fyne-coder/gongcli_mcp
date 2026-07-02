package mcp

import (
	"reflect"
	"strings"
	"testing"
)

func TestBusinessSignalTopicAliasesDefaultIntegrationUsesGenericB2BTerms(t *testing.T) {
	t.Parallel()

	got := businessSignalTopicAliases(OpExtractBuyerQuestions, "integration", defaultTopicPackSet())
	want := []string{"ERP integration", "system integration", "API integration"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("default integration aliases=%v want %v", got, want)
	}
}

func TestBusinessSignalTopicAliasesTechnicalReadinessPackAddsGenericReadinessTerms(t *testing.T) {
	t.Parallel()

	packs, err := resolveTopicPacks([]string{"technical_readiness"})
	if err != nil {
		t.Fatalf("resolveTopicPacks: %v", err)
	}
	expanded := strings.ToLower(strings.Join(businessSignalTopicAliases(OpExtractBuyerQuestions, "integration", packs), ","))
	for _, term := range []string{"sso integration", "sandbox testing", "data migration", "api readiness"} {
		if !strings.Contains(expanded, term) {
			t.Fatalf("technical_readiness pack integration aliases missing %q: %s", term, expanded)
		}
	}
	implementationExpanded := strings.ToLower(strings.Join(businessSignalTopicAliases(OpExtractBuyerQuestions, "implementation", packs), ","))
	for _, term := range []string{"implementation readiness", "launch readiness", "change management"} {
		if !strings.Contains(implementationExpanded, term) {
			t.Fatalf("technical_readiness pack implementation aliases missing %q: %s", term, implementationExpanded)
		}
	}
	launchExpanded := strings.ToLower(strings.Join(businessSignalTopicAliases(OpExtractBuyerQuestions, "launch readiness", packs), ","))
	for _, term := range []string{"go-live checklist", "deployment window", "training plan"} {
		if !strings.Contains(launchExpanded, term) {
			t.Fatalf("technical_readiness pack launch readiness aliases missing %q: %s", term, launchExpanded)
		}
	}
}

func TestBusinessSignalTopicsDefaultUseGenericB2BSeeds(t *testing.T) {
	t.Parallel()

	got := businessSignalTopics(OpExtractBuyerQuestions, businessSignalExtractionArgs{}, defaultTopicPackSet())
	want := []string{"pricing", "implementation", "integration", "security", "support", "timeline", "data", "ERP"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("default buyer-question topics=%v want %v", got, want)
	}
}

func TestBusinessSignalTopicsTechnicalReadinessPackIncludesLaunchReadiness(t *testing.T) {
	t.Parallel()

	packs, err := resolveTopicPacks([]string{"technical_readiness"})
	if err != nil {
		t.Fatalf("resolveTopicPacks: %v", err)
	}
	topics := businessSignalTopics(OpExtractBuyerQuestions, businessSignalExtractionArgs{}, packs)
	joined := strings.ToLower(strings.Join(topics, ","))
	if !strings.Contains(joined, "launch readiness") {
		t.Fatalf("technical_readiness pack default topics should include launch readiness: %s", joined)
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
