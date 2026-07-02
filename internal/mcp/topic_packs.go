package mcp

import (
	"fmt"
	"strings"
)

const (
	topicPackGenericB2B         = "generic_b2b"
	topicPackTechnicalReadiness = "technical_readiness"
)

var knownTopicPacks = map[string]struct{}{
	topicPackGenericB2B:         {},
	topicPackTechnicalReadiness: {},
}

type topicPackSet struct {
	active             []string
	genericB2B         bool
	technicalReadiness bool
}

func defaultTopicPackSet() topicPackSet {
	set, _ := resolveTopicPacks(nil)
	return set
}

func resolveTopicPacks(requested []string) (topicPackSet, error) {
	if len(requested) == 0 {
		return topicPackSet{
			active:     []string{topicPackGenericB2B},
			genericB2B: true,
		}, nil
	}
	seen := make(map[string]struct{}, len(requested))
	active := make([]string, 0, len(requested)+1)
	technicalReadiness := false
	for _, raw := range requested {
		name := strings.ToLower(strings.TrimSpace(raw))
		if name == "" {
			continue
		}
		if _, ok := knownTopicPacks[name]; !ok {
			return topicPackSet{}, fmt.Errorf("unknown topic_pack %q: supported packs are generic_b2b and technical_readiness", raw)
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		if name == topicPackTechnicalReadiness {
			technicalReadiness = true
		}
		active = append(active, name)
	}
	if len(active) == 0 {
		return topicPackSet{
			active:     []string{topicPackGenericB2B},
			genericB2B: true,
		}, nil
	}
	if _, ok := seen[topicPackGenericB2B]; !ok {
		active = append([]string{topicPackGenericB2B}, active...)
	}
	return topicPackSet{
		active:             active,
		genericB2B:         true,
		technicalReadiness: technicalReadiness,
	}, nil
}

func (s topicPackSet) provenancePayload() map[string]any {
	note := "generic_b2b is the default pack for generic B2B topic aliases and seeds."
	if s.technicalReadiness {
		note = "Opt-in topic packs expand topic aliases and default seeds; technical_readiness adds integration, security, and launch-readiness vocabulary."
	}
	return map[string]any{
		"active_packs": append([]string{}, s.active...),
		"note":         note,
	}
}

func genericB2BBusinessSignalTopicAliases() map[string][]string {
	return map[string][]string{
		"implementation":        {"implementation timeline", "implementation plan", "rollout", "deployment", "go live", "launch"},
		"implementation effort": {"implementation timeline", "implementation plan", "rollout effort", "deployment effort", "IT bandwidth", "resource constraints"},
		"integration":           {"ERP integration", "system integration", "API integration"},
		"integration risk":      {"ERP integration", "integration timeline", "integration effort", "API support", "technical lift", "IT bandwidth"},
		"erp integration":       {"ERP", "integrate with ERP", "direct ERP", "SAP integration", "Oracle integration", "NetSuite integration"},
		"security":              {"security review", "security questionnaire", "infosec", "information security", "compliance review", "risk review"},
		"security review":       {"security", "security questionnaire", "infosec", "information security", "compliance review", "risk review"},
		"pricing":               {"price", "budget", "cost", "investment", "pricing model", "quote"},
		"price":                 {"pricing", "budget", "cost", "investment", "quote"},
		"budget":                {"pricing", "price", "cost", "investment", "funding"},
		"roi":                   {"ROI", "return on investment", "business case", "value", "payback", "justify"},
		"manual order entry":    {"manual process", "manual order", "order entry", "manual data entry"},
		"supplier onboarding":   {"supplier enablement", "vendor onboarding", "trading relationship", "supplier setup", "supplier adoption"},
		"timeline":              {"implementation timeline", "go live", "launch date", "rollout", "schedule"},
		"support":               {"customer support", "post implementation support", "training", "enablement", "help desk"},
	}
}

func technicalReadinessBusinessSignalTopicAliasExtensions() map[string][]string {
	return map[string][]string{
		"implementation":   {"implementation readiness", "launch readiness", "training plan", "change management", "resource plan"},
		"integration":      {"SSO integration", "identity provider", "sandbox testing", "data migration", "API readiness"},
		"integration risk": {"SSO readiness", "data migration risk", "sandbox validation", "API readiness", "technical validation"},
		"launch readiness": {"go-live checklist", "rollout plan", "deployment window", "training plan"},
		"security":         {"security readiness", "access review", "audit logging", "data handling", "permission model"},
		"timeline":         {"launch readiness", "go-live checklist", "deployment window", "rollout plan"},
	}
}
