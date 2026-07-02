package mcp

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
	"gopkg.in/yaml.v3"
)

const (
	maxCustomTopicPacks                  = 16
	maxCustomTopicPackAliasKeys          = 64
	maxCustomTopicPackAliasesPerKey      = 12
	maxCustomTopicPackDefaultTopicsPerOp = 12
	maxCustomTopicPackDescriptionLen     = 240
	customTopicPackNameMaxLen            = 64
)

var customTopicPackNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

var allowedCustomTopicPackDefaultOperations = map[string]struct{}{
	OpExtractBuyerQuestions:   {},
	OpExtractObjectionSignals: {},
}

// BusinessTopicPackRegistry holds validated local custom topic packs loaded from
// YAML. Built-in packs (generic_b2b, technical_readiness) are always available
// without appearing in this registry.
type BusinessTopicPackRegistry struct {
	packs map[string]customTopicPack
}

type customTopicPack struct {
	Description   string
	Aliases       map[string][]string
	DefaultTopics map[string][]string
}

type businessTopicPacksConfigFile struct {
	TopicPacks map[string]businessTopicPackConfigEntry `yaml:"topic_packs"`
}

type businessTopicPackConfigEntry struct {
	Description   string              `yaml:"description"`
	Aliases       map[string][]string `yaml:"aliases"`
	DefaultTopics map[string][]string `yaml:"default_topics"`
}

// LoadBusinessTopicPacksFile reads and validates a local business topic packs
// YAML file. An empty path returns an empty registry without error.
func LoadBusinessTopicPacksFile(path string) (BusinessTopicPackRegistry, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return BusinessTopicPackRegistry{}, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return BusinessTopicPackRegistry{}, fmt.Errorf("read business topic packs config: %w", err)
	}
	return ParseBusinessTopicPacksYAML(raw)
}

// ParseBusinessTopicPacksYAML validates YAML bytes into a registry.
func ParseBusinessTopicPacksYAML(raw []byte) (BusinessTopicPackRegistry, error) {
	var file businessTopicPacksConfigFile
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	if err := decoder.Decode(&file); err != nil {
		return BusinessTopicPackRegistry{}, fmt.Errorf("parse business topic packs config: %w", err)
	}
	if len(file.TopicPacks) == 0 {
		return BusinessTopicPackRegistry{}, fmt.Errorf("business topic packs config must define at least one topic_packs entry")
	}
	if len(file.TopicPacks) > maxCustomTopicPacks {
		return BusinessTopicPackRegistry{}, fmt.Errorf("business topic packs config defines %d packs; maximum is %d", len(file.TopicPacks), maxCustomTopicPacks)
	}
	packs := make(map[string]customTopicPack, len(file.TopicPacks))
	seenNames := make(map[string]string, len(file.TopicPacks))
	for rawName, entry := range file.TopicPacks {
		name, err := normalizeCustomTopicPackName(rawName)
		if err != nil {
			return BusinessTopicPackRegistry{}, fmt.Errorf("topic_packs key %q: %w", rawName, err)
		}
		if prev, ok := seenNames[name]; ok && prev != rawName {
			return BusinessTopicPackRegistry{}, fmt.Errorf("topic_packs key %q duplicates pack name %q after normalization", rawName, prev)
		}
		seenNames[name] = rawName
		pack, err := validateCustomTopicPackEntry(name, entry)
		if err != nil {
			return BusinessTopicPackRegistry{}, err
		}
		packs[name] = pack
	}
	return BusinessTopicPackRegistry{packs: packs}, nil
}

func validateCustomTopicPackEntry(name string, entry businessTopicPackConfigEntry) (customTopicPack, error) {
	if len(entry.Aliases) == 0 && len(entry.DefaultTopics) == 0 {
		return customTopicPack{}, fmt.Errorf("topic_packs.%s must define aliases and/or default_topics", name)
	}
	description := strings.TrimSpace(entry.Description)
	if len(description) > maxCustomTopicPackDescriptionLen {
		return customTopicPack{}, fmt.Errorf("topic_packs.%s description exceeds %d characters", name, maxCustomTopicPackDescriptionLen)
	}
	if len(entry.Aliases) > maxCustomTopicPackAliasKeys {
		return customTopicPack{}, fmt.Errorf("topic_packs.%s aliases defines %d keys; maximum is %d", name, len(entry.Aliases), maxCustomTopicPackAliasKeys)
	}
	aliases := make(map[string][]string, len(entry.Aliases))
	seenAliasKeys := make(map[string]string, len(entry.Aliases))
	for rawKey, rawValues := range entry.Aliases {
		key, err := normalizeCustomTopicAliasKey(rawKey)
		if err != nil {
			return customTopicPack{}, fmt.Errorf("topic_packs.%s aliases key %q: %w", name, rawKey, err)
		}
		if prev, ok := seenAliasKeys[key]; ok && prev != rawKey {
			return customTopicPack{}, fmt.Errorf("topic_packs.%s aliases key %q duplicates alias key %q after normalization", name, rawKey, prev)
		}
		seenAliasKeys[key] = rawKey
		values, err := validateCustomTopicPackStrings(fmt.Sprintf("topic_packs.%s.aliases.%s", name, key), rawValues, maxCustomTopicPackAliasesPerKey)
		if err != nil {
			return customTopicPack{}, err
		}
		aliases[key] = values
	}
	defaultTopics := make(map[string][]string, len(entry.DefaultTopics))
	seenDefaultOperations := make(map[string]string, len(entry.DefaultTopics))
	for rawOperation, rawTopics := range entry.DefaultTopics {
		operation := strings.ToLower(strings.TrimSpace(rawOperation))
		if _, ok := allowedCustomTopicPackDefaultOperations[operation]; !ok {
			return customTopicPack{}, fmt.Errorf("topic_packs.%s default_topics operation %q is unsupported; allowed operations are %s and %s", name, rawOperation, OpExtractBuyerQuestions, OpExtractObjectionSignals)
		}
		if prev, ok := seenDefaultOperations[operation]; ok && prev != rawOperation {
			return customTopicPack{}, fmt.Errorf("topic_packs.%s default_topics operation %q duplicates operation %q after normalization", name, rawOperation, prev)
		}
		seenDefaultOperations[operation] = rawOperation
		topics, err := validateCustomTopicPackStrings(fmt.Sprintf("topic_packs.%s.default_topics.%s", name, operation), rawTopics, maxCustomTopicPackDefaultTopicsPerOp)
		if err != nil {
			return customTopicPack{}, err
		}
		defaultTopics[operation] = topics
	}
	return customTopicPack{
		Description:   description,
		Aliases:       aliases,
		DefaultTopics: defaultTopics,
	}, nil
}

func validateCustomTopicPackStrings(field string, values []string, maxItems int) ([]string, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("%s must contain at least one non-empty string", field)
	}
	if len(values) > maxItems {
		return nil, fmt.Errorf("%s defines %d values; maximum is %d", field, len(values), maxItems)
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for idx, raw := range values {
		if raw == "" {
			return nil, fmt.Errorf("%s[%d] must be a non-empty string", field, idx)
		}
		value := strings.TrimSpace(raw)
		if value == "" {
			return nil, fmt.Errorf("%s[%d] must be a non-empty string", field, idx)
		}
		if len(value) > maxBusinessAnalysisFTSQueryLength {
			return nil, fmt.Errorf("%s[%d] exceeds %d characters", field, idx, maxBusinessAnalysisFTSQueryLength)
		}
		if err := sqlite.ValidateBusinessAnalysisFTSQueryValue(value, fmt.Sprintf("%s[%d]", field, idx)); err != nil {
			return nil, err
		}
		key := strings.ToLower(strings.Join(strings.Fields(value), " "))
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s must contain at least one non-empty string", field)
	}
	return out, nil
}

func normalizeCustomTopicPackName(raw string) (string, error) {
	name := strings.ToLower(strings.TrimSpace(raw))
	if name == "" {
		return "", fmt.Errorf("pack name must be a non-empty lowercase identifier")
	}
	if len(name) > customTopicPackNameMaxLen {
		return "", fmt.Errorf("pack name exceeds %d characters", customTopicPackNameMaxLen)
	}
	if !customTopicPackNamePattern.MatchString(name) {
		return "", fmt.Errorf("pack name must match %s", customTopicPackNamePattern.String())
	}
	if _, builtin := knownTopicPacks[name]; builtin {
		return "", fmt.Errorf("pack name %q is reserved for a built-in pack", name)
	}
	return name, nil
}

func normalizeCustomTopicAliasKey(raw string) (string, error) {
	key := strings.ToLower(strings.TrimSpace(raw))
	key = strings.Join(strings.Fields(key), " ")
	if key == "" {
		return "", fmt.Errorf("alias key must be a non-empty string")
	}
	if len(key) > maxBusinessAnalysisFTSQueryLength {
		return "", fmt.Errorf("alias key exceeds %d characters", maxBusinessAnalysisFTSQueryLength)
	}
	for _, r := range key {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("alias key must not contain control characters")
		}
	}
	return key, nil
}

// CustomPackNames returns configured custom pack names in sorted order.
func (r BusinessTopicPackRegistry) CustomPackNames() []string {
	if len(r.packs) == 0 {
		return nil
	}
	out := make([]string, 0, len(r.packs))
	for name := range r.packs {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// SupportedPackNames returns built-in and configured pack names for errors and
// discovery metadata.
func (r BusinessTopicPackRegistry) SupportedPackNames() []string {
	out := []string{topicPackGenericB2B, topicPackTechnicalReadiness}
	out = append(out, r.CustomPackNames()...)
	return out
}

// TopicPackSchemaEnum returns topic_packs enum values for extraction schemas.
func (r BusinessTopicPackRegistry) TopicPackSchemaEnum() []string {
	return r.SupportedPackNames()
}

// CustomPackDescriptions returns non-empty descriptions keyed by configured
// pack name in deterministic name order.
func (r BusinessTopicPackRegistry) CustomPackDescriptions() map[string]string {
	names := r.CustomPackNames()
	if len(names) == 0 {
		return nil
	}
	out := make(map[string]string, len(names))
	for _, name := range names {
		pack, ok := r.customPack(name)
		if !ok {
			continue
		}
		if desc := strings.TrimSpace(pack.Description); desc != "" {
			out[name] = desc
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (r BusinessTopicPackRegistry) customPack(name string) (customTopicPack, bool) {
	if r.packs == nil {
		return customTopicPack{}, false
	}
	pack, ok := r.packs[name]
	return pack, ok
}
