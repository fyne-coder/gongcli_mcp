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
	customActive       []string
	registry           BusinessTopicPackRegistry
}

func defaultTopicPackSet() topicPackSet {
	set, _ := resolveTopicPacks(nil)
	return set
}

func resolveTopicPacks(requested []string) (topicPackSet, error) {
	return (BusinessTopicPackRegistry{}).ResolveTopicPacks(requested)
}

// ResolveTopicPacks validates requested pack names against built-ins and any
// configured custom packs in the registry.
func (r BusinessTopicPackRegistry) ResolveTopicPacks(requested []string) (topicPackSet, error) {
	if len(requested) == 0 {
		return topicPackSet{
			active:     []string{topicPackGenericB2B},
			genericB2B: true,
			registry:   r,
		}, nil
	}
	seen := make(map[string]struct{}, len(requested))
	active := make([]string, 0, len(requested)+1)
	customActive := make([]string, 0, len(requested))
	technicalReadiness := false
	for _, raw := range requested {
		name := strings.ToLower(strings.TrimSpace(raw))
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		if _, builtin := knownTopicPacks[name]; builtin {
			seen[name] = struct{}{}
			if name == topicPackTechnicalReadiness {
				technicalReadiness = true
			}
			active = append(active, name)
			continue
		}
		if _, ok := r.customPack(name); !ok {
			return topicPackSet{}, unknownTopicPackError(raw, r)
		}
		seen[name] = struct{}{}
		customActive = append(customActive, name)
		active = append(active, name)
	}
	if len(active) == 0 {
		return topicPackSet{
			active:     []string{topicPackGenericB2B},
			genericB2B: true,
			registry:   r,
		}, nil
	}
	if _, ok := seen[topicPackGenericB2B]; !ok {
		active = append([]string{topicPackGenericB2B}, active...)
	}
	return topicPackSet{
		active:             active,
		genericB2B:         true,
		technicalReadiness: technicalReadiness,
		customActive:       customActive,
		registry:           r,
	}, nil
}

func unknownTopicPackError(raw string, registry BusinessTopicPackRegistry) error {
	supported := strings.Join(registry.SupportedPackNames(), ", ")
	return fmt.Errorf("unknown topic_pack %q: supported packs are %s", raw, supported)
}

func (s topicPackSet) provenancePayload() map[string]any {
	note := "generic_b2b is the default pack for generic B2B topic aliases and seeds."
	switch {
	case len(s.customActive) > 0 && s.technicalReadiness:
		note = "Built-in and configured topic packs expand topic aliases and default seeds; technical_readiness adds integration, security, and launch-readiness vocabulary; configured packs add local aliases and operation defaults, with requested custom entries prioritized within query caps."
	case len(s.customActive) > 0:
		note = "Built-in generic_b2b remains available; configured topic packs add local aliases and operation-specific default seeds, with requested custom entries prioritized within query caps."
	case s.technicalReadiness:
		note = "Opt-in topic packs expand topic aliases and default seeds; technical_readiness adds integration, security, and launch-readiness vocabulary."
	}
	payload := map[string]any{
		"active_packs": append([]string{}, s.active...),
		"note":         note,
	}
	if names := s.registry.CustomPackNames(); len(names) > 0 {
		payload["configured_packs"] = names
	}
	return payload
}

func (s topicPackSet) customAliases(topic string) []string {
	if len(s.customActive) == 0 {
		return nil
	}
	key := strings.ToLower(strings.TrimSpace(topic))
	key = strings.Join(strings.Fields(key), " ")
	out := make([]string, 0)
	seen := make(map[string]struct{})
	for _, name := range s.customActive {
		pack, ok := s.registry.customPack(name)
		if !ok {
			continue
		}
		for _, alias := range pack.Aliases[key] {
			normalized := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(alias)), " "))
			if normalized == "" {
				continue
			}
			if _, ok := seen[normalized]; ok {
				continue
			}
			seen[normalized] = struct{}{}
			out = append(out, alias)
		}
	}
	return out
}

func (s topicPackSet) customDefaultTopics(operation string) []string {
	if len(s.customActive) == 0 {
		return nil
	}
	out := make([]string, 0)
	seen := make(map[string]struct{})
	for _, name := range s.customActive {
		pack, ok := s.registry.customPack(name)
		if !ok {
			continue
		}
		for _, topic := range pack.DefaultTopics[operation] {
			key := strings.ToLower(strings.TrimSpace(topic))
			if key == "" {
				continue
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, topic)
		}
	}
	return out
}

func builtinBusinessSignalDefaultTopics(operation string, packs topicPackSet) []string {
	switch operation {
	case OpExtractObjectionSignals:
		return []string{"price", "budget", "timeline", "security review", "integration risk", "IT bandwidth", "ROI", "worried", "blocker", "competitor"}
	default:
		candidates := []string{"pricing", "implementation", "integration", "security", "support", "timeline", "data", "ERP"}
		if packs.technicalReadiness {
			candidates = append(candidates, "launch readiness")
		}
		return candidates
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
