package governance

import (
	"bytes"
	"context"
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

type RuntimeSnapshot struct {
	ConfigSize    int64
	ConfigModTime int64
	Data          string
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

func BuildAudit(ctx context.Context, store CandidateStore, cfg *Config) (*Audit, error) {
	candidates, err := store.GovernanceNameCandidates(ctx)
	if err != nil {
		return nil, err
	}
	return AuditCandidates(candidates, cfg), nil
}

func AuditCandidates(candidates []Candidate, cfg *Config) *Audit {
	targets := cfg.Targets()
	entries := map[string]struct{}{}
	aliases := 0
	for _, target := range targets {
		entries[target.List+"\x00"+target.Name] = struct{}{}
		if strings.TrimSpace(target.Alias) != "" {
			aliases++
		}
	}

	callsByTarget := map[string]map[string]struct{}{}
	suppressed := map[string]struct{}{}
	for _, candidate := range candidates {
		normalized := NormalizeName(candidate.Value)
		if normalized == "" {
			continue
		}
		for _, target := range targets {
			if !normalizedContainsTarget(normalized, target.Normalized) {
				continue
			}
			key := targetKey(target)
			if callsByTarget[key] == nil {
				callsByTarget[key] = map[string]struct{}{}
			}
			callsByTarget[key][candidate.CallID] = struct{}{}
			suppressed[candidate.CallID] = struct{}{}
		}
	}

	audit := &Audit{
		ConfigEntries:   len(entries),
		ConfigAliases:   aliases,
		CandidateValues: len(candidates),
	}
	for _, target := range targets {
		calls := callsByTarget[targetKey(target)]
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
	for callID := range suppressed {
		audit.SuppressedCallIDs = append(audit.SuppressedCallIDs, callID)
	}
	sort.Strings(audit.SuppressedCallIDs)
	audit.SuppressedCallCount = len(audit.SuppressedCallIDs)
	return audit
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
