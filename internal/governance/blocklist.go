package governance

import (
	"strings"
)

// BlocklistGuard is a normalized, read-only view over the blocklist/restricted
// names that defend MCP serialization paths from emitting customer-identifying
// values when source-to-serving redaction or scoped-reader grants miss a row.
//
// The guard is intentionally simple: it normalizes input the same way
// AuditCandidates does (NormalizeName), then reports whether a value contains
// any blocklisted target as a whole-word substring. It is not a primary
// authorization layer — it backs up the source-to-serving redaction and the
// MCP account-query gate.
type BlocklistGuard struct {
	terms []string
}

// NewBlocklistGuard builds a guard from raw restricted-name strings. Empty or
// blank entries are dropped. The guard preserves only the normalized form so
// callers can audit it without leaking original casing back through logs.
func NewBlocklistGuard(rawTerms []string) *BlocklistGuard {
	guard := &BlocklistGuard{}
	seen := map[string]struct{}{}
	for _, raw := range rawTerms {
		normalized := NormalizeName(raw)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		guard.terms = append(guard.terms, normalized)
	}
	return guard
}

// NewBlocklistGuardFromConfig is a convenience that wraps NewBlocklistGuard
// over every list+alias target in the supplied governance Config.
func NewBlocklistGuardFromConfig(cfg *Config) *BlocklistGuard {
	if cfg == nil {
		return &BlocklistGuard{}
	}
	targets := cfg.Targets()
	terms := make([]string, 0, len(targets))
	for _, target := range targets {
		if strings.TrimSpace(target.Normalized) != "" {
			terms = append(terms, target.Normalized)
			continue
		}
		if name := strings.TrimSpace(target.Name); name != "" {
			terms = append(terms, name)
		}
	}
	return NewBlocklistGuard(terms)
}

// Empty reports whether the guard has no blocklist terms loaded. A nil guard
// is treated as empty.
func (g *BlocklistGuard) Empty() bool {
	if g == nil {
		return true
	}
	return len(g.terms) == 0
}

// TermCount returns the number of distinct normalized terms loaded into the
// guard. The terms themselves are not exposed so callers cannot accidentally
// log them.
func (g *BlocklistGuard) TermCount() int {
	if g == nil {
		return 0
	}
	return len(g.terms)
}

// MatchValue reports whether the given value contains any blocklisted target
// as a whole-word substring after NormalizeName. Empty/blank input is never a
// match.
func (g *BlocklistGuard) MatchValue(value string) bool {
	if g == nil || len(g.terms) == 0 {
		return false
	}
	normalized := NormalizeName(value)
	if normalized == "" {
		return false
	}
	for _, term := range g.terms {
		if normalizedContainsTarget(normalized, term) {
			return true
		}
	}
	return false
}

// MatchAny reports whether any of the supplied values matches a blocklisted
// target.
func (g *BlocklistGuard) MatchAny(values []string) bool {
	for _, value := range values {
		if g.MatchValue(value) {
			return true
		}
	}
	return false
}
