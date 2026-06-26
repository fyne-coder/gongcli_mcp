package mcp

import (
	"fmt"
	"strings"

	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
)

const defaultInternalParticipantDomain = "internal.example"

var defaultInternalParticipantDomains = []string{defaultInternalParticipantDomain}

// WithInternalParticipantDomains seeds participant-domain classification with
// deployment-specific internal email domains. When unset, a generic example
// domain is used so deployments must configure their own internal domain when
// they need exact seller/buyer affiliation.
func WithInternalParticipantDomains(domains []string) ServerOption {
	return func(s *Server) {
		s.internalParticipantDomains = normalizeInternalParticipantDomains(domains)
	}
}

func normalizeInternalParticipantDomains(domains []string) []string {
	if len(domains) == 0 {
		out := make([]string, len(defaultInternalParticipantDomains))
		copy(out, defaultInternalParticipantDomains)
		return out
	}
	seen := make(map[string]struct{}, len(domains))
	out := make([]string, 0, len(domains))
	for _, raw := range domains {
		domain := normalizeParticipantEmailDomain(raw)
		if domain == "" {
			continue
		}
		if _, ok := seen[domain]; ok {
			continue
		}
		seen[domain] = struct{}{}
		out = append(out, domain)
	}
	if len(out) == 0 {
		out = append(out, defaultInternalParticipantDomain)
	}
	return out
}

func resolveInternalParticipantDomains(serverDomains, argDomains []string) []string {
	if len(argDomains) > 0 {
		return normalizeInternalParticipantDomains(argDomains)
	}
	if len(serverDomains) > 0 {
		return normalizeInternalParticipantDomains(serverDomains)
	}
	return normalizeInternalParticipantDomains(nil)
}

func normalizeParticipantAffiliationFilter(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "any":
		return "", nil
	case "external", "buyer", "customer", "prospect", "marketing":
		return "external", nil
	case "internal", "seller", "rep", "coaching":
		return "internal", nil
	default:
		return "", fmt.Errorf("participant_affiliation_filter must be one of: any, external, internal")
	}
}

func normalizeParticipantEmailDomain(value string) string {
	domain := strings.ToLower(strings.TrimSpace(value))
	domain = strings.TrimPrefix(domain, "@")
	return domain
}

func isParticipantPolicyDimension(dimension string) bool {
	switch strings.ToLower(strings.TrimSpace(dimension)) {
	case "participant_domain", "domain", "email_domain", "participant_email", "participant_affiliation", "participant_affiliation_class":
		return true
	default:
		return false
	}
}

func participantPolicyDimensionCanonical(dimension string) string {
	switch strings.ToLower(strings.TrimSpace(dimension)) {
	case "domain", "email_domain":
		return "participant_domain"
	case "participant_affiliation_class":
		return "participant_affiliation"
	default:
		return strings.ToLower(strings.TrimSpace(dimension))
	}
}

func participantAffiliationSummaryFromDimensionRows(rows []sqlite.BusinessAnalysisDimensionRow) map[string]int64 {
	summary := map[string]int64{
		"internal": 0,
		"external": 0,
		"unknown":  0,
		"mixed":    0,
	}
	for _, row := range rows {
		switch row.Value {
		case "internal", "external", "unknown", "mixed":
			summary[row.Value] += row.CallCount
		}
	}
	return summary
}

func participantResolvableDomainCoverageFromAffiliation(summary map[string]int64) (int64, int64, float64) {
	var total int64
	for _, count := range summary {
		total += count
	}
	resolvable := total - summary["unknown"]
	var rate float64
	if total > 0 {
		rate = float64(resolvable) / float64(total)
	}
	return total, resolvable, rate
}
