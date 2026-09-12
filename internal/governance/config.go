package governance

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

const (
	ListNoAI                 = "no_ai"
	ListNotificationRequired = "notification_required"

	// NoExclusionsConfigSHA256 is the stable fingerprint for the explicit
	// operator contract that declares no customer governance exclusions.
	NoExclusionsConfigSHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

type Config struct {
	Version int             `json:"version" yaml:"version"`
	Lists   map[string]List `json:"lists" yaml:"lists"`
}

type List struct {
	Description string  `json:"description,omitempty" yaml:"description,omitempty"`
	Action      string  `json:"action,omitempty" yaml:"action,omitempty"`
	Customers   []Entry `json:"customers" yaml:"customers"`
}

type Entry struct {
	Name    string   `json:"name" yaml:"name"`
	Aliases []string `json:"aliases,omitempty" yaml:"aliases,omitempty"`
	Reason  string   `json:"reason,omitempty" yaml:"reason,omitempty"`
	Notes   string   `json:"notes,omitempty" yaml:"notes,omitempty"`
}

type Target struct {
	List       string `json:"list"`
	Name       string `json:"name"`
	Alias      string `json:"alias,omitempty"`
	Normalized string `json:"normalized"`
}

type Candidate struct {
	CallID string
	Source string
	Value  string
}

type Match struct {
	List       string `json:"list"`
	Name       string `json:"name"`
	Alias      string `json:"alias,omitempty"`
	Normalized string `json:"normalized"`
	CallCount  int    `json:"call_count"`
}

type Audit struct {
	ConfigEntries       int      `json:"config_entries"`
	ConfigAliases       int      `json:"config_aliases"`
	CandidateValues     int      `json:"candidate_values"`
	MatchedEntries      []Match  `json:"matched_entries"`
	UnmatchedEntries    []Target `json:"unmatched_entries"`
	SuppressedCallIDs   []string `json:"suppressed_call_ids,omitempty"`
	SuppressedCallCount int      `json:"suppressed_call_count"`
}

type CandidateStore interface {
	GovernanceNameCandidates(ctx context.Context) ([]Candidate, error)
	GovernanceDataFingerprint(ctx context.Context) (string, error)
}

// StreamingCandidateStore is an optional extension of CandidateStore. When a
// store implements it, BuildAudit consumes candidates row-by-row instead of
// materializing the full slice.
type StreamingCandidateStore interface {
	StreamGovernanceNameCandidates(ctx context.Context, consume func(Candidate) error) error
}

// AuditAccumulator builds an Audit without retaining candidate value strings.
// CandidateValues counts every Consume call, including blank-normalized values,
// matching AuditCandidates' use of len(candidates). Consume is for a single
// goroutine and is not safe for concurrent calls.
type AuditAccumulator struct {
	targets         []Target
	configEntries   int
	configAliases   int
	candidateValues int
	callsByTarget   map[string]map[string]struct{}
	suppressed      map[string]struct{}
}

// NewAuditAccumulator precomputes targets and config entry/alias counts.
func NewAuditAccumulator(cfg *Config) *AuditAccumulator {
	targets := cfg.Targets()
	entries := map[string]struct{}{}
	aliases := 0
	for _, target := range targets {
		entries[target.List+"\x00"+target.Name] = struct{}{}
		if strings.TrimSpace(target.Alias) != "" {
			aliases++
		}
	}
	return &AuditAccumulator{
		targets:       targets,
		configEntries: len(entries),
		configAliases: aliases,
		callsByTarget: map[string]map[string]struct{}{},
		suppressed:    map[string]struct{}{},
	}
}

// Consume records one candidate. Blank-normalized values still increment the
// candidate count but do not match targets.
func (a *AuditAccumulator) Consume(candidate Candidate) {
	if a == nil {
		return
	}
	a.candidateValues++
	normalized := NormalizeName(candidate.Value)
	if normalized == "" {
		return
	}
	for _, target := range a.targets {
		if !normalizedContainsTarget(normalized, target.Normalized) {
			continue
		}
		key := targetKey(target)
		if a.callsByTarget[key] == nil {
			a.callsByTarget[key] = map[string]struct{}{}
		}
		a.callsByTarget[key][candidate.CallID] = struct{}{}
		a.suppressed[candidate.CallID] = struct{}{}
	}
}

// Result finalizes matched/unmatched entries in stable target order and sorts
// suppressed call IDs.
func (a *AuditAccumulator) Result() *Audit {
	if a == nil {
		return &Audit{}
	}
	audit := &Audit{
		ConfigEntries:   a.configEntries,
		ConfigAliases:   a.configAliases,
		CandidateValues: a.candidateValues,
	}
	for _, target := range a.targets {
		calls := a.callsByTarget[targetKey(target)]
		if len(calls) == 0 {
			audit.UnmatchedEntries = append(audit.UnmatchedEntries, target)
			continue
		}
		audit.MatchedEntries = append(audit.MatchedEntries, Match{
			List:       target.List,
			Name:       target.Name,
			Alias:      target.Alias,
			Normalized: target.Normalized,
			CallCount:  len(calls),
		})
	}
	for callID := range a.suppressed {
		audit.SuppressedCallIDs = append(audit.SuppressedCallIDs, callID)
	}
	sort.Strings(audit.SuppressedCallIDs)
	audit.SuppressedCallCount = len(audit.SuppressedCallIDs)
	return audit
}

type RuntimeSnapshot struct {
	ConfigSize    int64
	ConfigModTime int64
	Data          string
}

// NoExclusionsConfig returns the in-memory governance config used when an
// operator explicitly declares that no customer exclusions exist.
func NoExclusionsConfig() *Config {
	return &Config{
		Version: 1,
		Lists:   map[string]List{},
	}
}

// NoExclusionsConfigFingerprint returns the stable config fingerprint for the
// explicit no-exclusions operator contract.
func NoExclusionsConfigFingerprint() string {
	return NoExclusionsConfigSHA256
}

func LoadFile(path string) (*Config, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("governance config path is required")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read governance config: %w", err)
	}
	return ParseYAML(body)
}

func ParseYAML(body []byte) (*Config, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(body))
	decoder.KnownFields(true)
	var cfg Config
	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decode governance config: %w", err)
	}
	if cfg.Version != 1 {
		return nil, fmt.Errorf("governance config version %d is not supported", cfg.Version)
	}
	for listName := range cfg.Lists {
		if listName != ListNoAI && listName != ListNotificationRequired {
			return nil, fmt.Errorf("unknown governance list %q", listName)
		}
	}
	for _, listName := range []string{ListNoAI, ListNotificationRequired} {
		list, ok := cfg.Lists[listName]
		if !ok {
			continue
		}
		for idx, entry := range list.Customers {
			if NormalizeName(entry.Name) == "" {
				return nil, fmt.Errorf("governance list %s customer %d name is required", listName, idx+1)
			}
		}
	}
	if len((&cfg).Targets()) == 0 {
		return nil, errors.New("governance config must contain at least one no_ai or notification_required customer name or alias")
	}
	return &cfg, nil
}

func Snapshot(ctx context.Context, path string, store CandidateStore) (RuntimeSnapshot, error) {
	info, err := os.Stat(strings.TrimSpace(path))
	if err != nil {
		return RuntimeSnapshot{}, fmt.Errorf("stat governance config: %w", err)
	}
	fingerprint, err := store.GovernanceDataFingerprint(ctx)
	if err != nil {
		return RuntimeSnapshot{}, err
	}
	return RuntimeSnapshot{
		ConfigSize:    info.Size(),
		ConfigModTime: info.ModTime().UnixNano(),
		Data:          fingerprint,
	}, nil
}

func NormalizeName(value string) string {
	fields := make([]string, 0)
	var b strings.Builder
	flush := func() {
		if b.Len() == 0 {
			return
		}
		fields = append(fields, b.String())
		b.Reset()
	}
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			b.WriteRune(r)
		default:
			flush()
		}
	}
	flush()
	return strings.Join(fields, " ")
}

func (c *Config) Targets() []Target {
	if c == nil {
		return nil
	}
	out := make([]Target, 0)
	for _, listName := range []string{ListNoAI, ListNotificationRequired} {
		list := c.Lists[listName]
		for _, entry := range list.Customers {
			if normalized := NormalizeName(entry.Name); normalized != "" {
				out = append(out, Target{List: listName, Name: entry.Name, Normalized: normalized})
			}
			for _, alias := range entry.Aliases {
				if normalized := NormalizeName(alias); normalized != "" {
					out = append(out, Target{List: listName, Name: entry.Name, Alias: alias, Normalized: normalized})
				}
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].List != out[j].List {
			return out[i].List < out[j].List
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Alias < out[j].Alias
	})
	return out
}

func (c *Config) Fingerprint() string {
	hash := sha256.New()
	for _, target := range c.Targets() {
		_, _ = hash.Write([]byte(target.List))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(target.Name))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(target.Alias))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(target.Normalized))
		_, _ = hash.Write([]byte{0})
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}

func BuildAudit(ctx context.Context, store CandidateStore, cfg *Config) (*Audit, error) {
	if streamer, ok := store.(StreamingCandidateStore); ok {
		acc := NewAuditAccumulator(cfg)
		if err := streamer.StreamGovernanceNameCandidates(ctx, func(candidate Candidate) error {
			acc.Consume(candidate)
			return nil
		}); err != nil {
			return nil, err
		}
		return acc.Result(), nil
	}
	candidates, err := store.GovernanceNameCandidates(ctx)
	if err != nil {
		return nil, err
	}
	return AuditCandidates(candidates, cfg), nil
}

// AuditCandidates is a compatibility wrapper around AuditAccumulator.
func AuditCandidates(candidates []Candidate, cfg *Config) *Audit {
	acc := NewAuditAccumulator(cfg)
	for _, candidate := range candidates {
		acc.Consume(candidate)
	}
	return acc.Result()
}

func targetKey(target Target) string {
	return target.List + "\x00" + target.Name + "\x00" + target.Alias + "\x00" + target.Normalized
}

func normalizedContainsTarget(candidate string, target string) bool {
	candidate = strings.TrimSpace(candidate)
	target = strings.TrimSpace(target)
	if candidate == "" || target == "" {
		return false
	}
	return strings.Contains(" "+candidate+" ", " "+target+" ")
}
