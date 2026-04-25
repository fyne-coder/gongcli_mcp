package profile

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

const Version = 1

var RequiredLifecycleBuckets = []string{"open", "closed_won", "closed_lost", "post_sales", "unknown"}

var allowedOperators = map[string]struct{}{
	"equals":   {},
	"in":       {},
	"prefix":   {},
	"iprefix":  {},
	"regex":    {},
	"is_set":   {},
	"is_empty": {},
}

const maxRegexLength = 256

var regexCache sync.Map

type Profile struct {
	Version     int                           `json:"version" yaml:"version"`
	Name        string                        `json:"name,omitempty" yaml:"name,omitempty"`
	Objects     map[string]ObjectMapping      `json:"objects,omitempty" yaml:"objects,omitempty"`
	Fields      map[string]FieldMapping       `json:"fields,omitempty" yaml:"fields,omitempty"`
	Lifecycle   map[string]LifecycleBucket    `json:"lifecycle,omitempty" yaml:"lifecycle,omitempty"`
	Methodology map[string]MethodologyConcept `json:"methodology,omitempty" yaml:"methodology,omitempty"`
}

type ObjectMapping struct {
	ObjectTypes []string  `json:"object_types,omitempty" yaml:"object_types,omitempty"`
	Confidence  float64   `json:"confidence,omitempty" yaml:"confidence,omitempty"`
	Evidence    *Evidence `json:"evidence,omitempty" yaml:"evidence,omitempty"`
}

type FieldMapping struct {
	Object     string    `json:"object,omitempty" yaml:"object,omitempty"`
	Names      []string  `json:"names,omitempty" yaml:"names,omitempty"`
	Confidence float64   `json:"confidence,omitempty" yaml:"confidence,omitempty"`
	Evidence   *Evidence `json:"evidence,omitempty" yaml:"evidence,omitempty"`
}

type LifecycleBucket struct {
	Label       string    `json:"label,omitempty" yaml:"label,omitempty"`
	Description string    `json:"description,omitempty" yaml:"description,omitempty"`
	Order       int       `json:"order,omitempty" yaml:"order,omitempty"`
	Rules       []Rule    `json:"rules,omitempty" yaml:"rules,omitempty"`
	Confidence  float64   `json:"confidence,omitempty" yaml:"confidence,omitempty"`
	Evidence    *Evidence `json:"evidence,omitempty" yaml:"evidence,omitempty"`
}

type MethodologyConcept struct {
	Description          string     `json:"description,omitempty" yaml:"description,omitempty"`
	Aliases              []string   `json:"aliases,omitempty" yaml:"aliases,omitempty"`
	Fields               []FieldRef `json:"fields,omitempty" yaml:"fields,omitempty"`
	TrackerIDs           []string   `json:"tracker_ids,omitempty" yaml:"tracker_ids,omitempty"`
	ScorecardQuestionIDs []string   `json:"scorecard_question_ids,omitempty" yaml:"scorecard_question_ids,omitempty"`
}

type FieldRef struct {
	Object string `json:"object,omitempty" yaml:"object,omitempty"`
	Name   string `json:"name,omitempty" yaml:"name,omitempty"`
}

type Rule struct {
	Field     string   `json:"field,omitempty" yaml:"field,omitempty"`
	Object    string   `json:"object,omitempty" yaml:"object,omitempty"`
	FieldName string   `json:"field_name,omitempty" yaml:"field_name,omitempty"`
	Op        string   `json:"op" yaml:"op"`
	Value     string   `json:"value,omitempty" yaml:"value,omitempty"`
	Values    []string `json:"values,omitempty" yaml:"values,omitempty"`
}

type Evidence struct {
	Source             string   `json:"source,omitempty" yaml:"source,omitempty"`
	MatchedHeuristic   string   `json:"matched_heuristic,omitempty" yaml:"matched_heuristic,omitempty"`
	SampleSize         int      `json:"sample_size,omitempty" yaml:"sample_size,omitempty"`
	DistinctValueCount int      `json:"distinct_value_count,omitempty" yaml:"distinct_value_count,omitempty"`
	Values             []string `json:"values,omitempty" yaml:"values,omitempty"`
}

type Finding struct {
	Severity string `json:"severity" yaml:"severity"`
	Code     string `json:"code" yaml:"code"`
	Message  string `json:"message" yaml:"message"`
	Path     string `json:"path,omitempty" yaml:"path,omitempty"`
}

type Inventory struct {
	Objects []ObjectInventory `json:"objects,omitempty"`
	Fields  []FieldInventory  `json:"fields,omitempty"`
}

type ObjectInventory struct {
	ObjectType  string `json:"object_type"`
	ObjectCount int    `json:"object_count"`
	CallCount   int    `json:"call_count"`
}

type FieldInventory struct {
	ObjectType     string   `json:"object_type"`
	FieldName      string   `json:"field_name"`
	FieldLabel     string   `json:"field_label,omitempty"`
	FieldType      string   `json:"field_type,omitempty"`
	ObjectCount    int      `json:"object_count"`
	PopulatedCount int      `json:"populated_count"`
	DistinctValues []string `json:"distinct_values,omitempty"`
}

type ValidationResult struct {
	Valid           bool      `json:"valid"`
	SourceSHA256    string    `json:"source_sha256,omitempty"`
	CanonicalSHA256 string    `json:"canonical_sha256,omitempty"`
	Findings        []Finding `json:"findings"`
}

func ParseYAML(body []byte) (*Profile, error) {
	var p Profile
	decoder := yaml.NewDecoder(bytes.NewReader(body))
	decoder.KnownFields(true)
	if err := decoder.Decode(&p); err != nil {
		return nil, err
	}
	normalizeProfile(&p)
	return &p, nil
}

func MarshalYAML(p *Profile) ([]byte, error) {
	body, err := yaml.Marshal(p)
	if err != nil {
		return nil, err
	}
	return body, nil
}

func SourceHash(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func CanonicalJSON(p *Profile) ([]byte, error) {
	if p == nil {
		return nil, errors.New("profile is nil")
	}
	cp := *p
	normalizeProfile(&cp)
	return json.Marshal(cp)
}

func CanonicalHash(p *Profile) (string, error) {
	body, err := CanonicalJSON(p)
	if err != nil {
		return "", err
	}
	return SourceHash(body), nil
}

func Validate(p *Profile, inventory *Inventory) []Finding {
	var findings []Finding
	if p == nil {
		return []Finding{{Severity: "error", Code: "profile_nil", Message: "profile is nil"}}
	}
	if p.Version == 0 {
		findings = append(findings, Finding{
			Severity: "error",
			Code:     "missing_version",
			Message:  fmt.Sprintf("profile version is required; supported version is %d", Version),
			Path:     "version",
		})
	} else if p.Version != Version {
		findings = append(findings, Finding{
			Severity: "error",
			Code:     "unsupported_version",
			Message:  fmt.Sprintf("profile version %d is not supported; supported version is %d", p.Version, Version),
			Path:     "version",
		})
	}
	if len(p.Objects) == 0 {
		findings = append(findings, Finding{Severity: "warn", Code: "no_objects", Message: "profile has no object aliases", Path: "objects"})
	}
	if len(p.Fields) == 0 {
		findings = append(findings, Finding{Severity: "warn", Code: "no_fields", Message: "profile has no field concepts", Path: "fields"})
	}
	for _, bucket := range RequiredLifecycleBuckets {
		if _, ok := p.Lifecycle[bucket]; !ok {
			findings = append(findings, Finding{
				Severity: "error",
				Code:     "missing_lifecycle_bucket",
				Message:  fmt.Sprintf("required lifecycle bucket %q is missing", bucket),
				Path:     "lifecycle." + bucket,
			})
		}
	}
	for concept, object := range p.Objects {
		if len(object.ObjectTypes) == 0 {
			findings = append(findings, Finding{Severity: "warn", Code: "empty_object_alias", Message: fmt.Sprintf("object alias %q has no object_types", concept), Path: "objects." + concept})
		}
	}
	for concept, field := range p.Fields {
		if strings.TrimSpace(field.Object) == "" {
			findings = append(findings, Finding{Severity: "error", Code: "field_missing_object", Message: fmt.Sprintf("field concept %q must name an object alias", concept), Path: "fields." + concept + ".object"})
		} else if _, ok := p.Objects[field.Object]; !ok {
			findings = append(findings, Finding{Severity: "error", Code: "unknown_object_alias", Message: fmt.Sprintf("field concept %q references unknown object alias %q", concept, field.Object), Path: "fields." + concept + ".object"})
		}
		if len(field.Names) == 0 {
			findings = append(findings, Finding{Severity: "warn", Code: "field_no_names", Message: fmt.Sprintf("field concept %q has no field names", concept), Path: "fields." + concept + ".names"})
		}
	}
	for bucket, lifecycle := range p.Lifecycle {
		if bucket == "" {
			findings = append(findings, Finding{Severity: "error", Code: "empty_lifecycle_bucket", Message: "lifecycle bucket name cannot be empty", Path: "lifecycle"})
			continue
		}
		if len(lifecycle.Rules) == 0 && bucket != "unknown" {
			findings = append(findings, Finding{Severity: "warn", Code: "lifecycle_no_rules", Message: fmt.Sprintf("lifecycle bucket %q has no rules", bucket), Path: "lifecycle." + bucket + ".rules"})
		}
		for idx, rule := range lifecycle.Rules {
			path := fmt.Sprintf("lifecycle.%s.rules.%d", bucket, idx)
			findings = append(findings, validateRule(p, rule, path)...)
		}
	}
	if inventory != nil {
		findings = append(findings, validateAgainstInventory(p, inventory)...)
	}
	sortFindings(findings)
	return findings
}

func IsValid(findings []Finding) bool {
	for _, finding := range findings {
		if finding.Severity == "error" {
			return false
		}
	}
	return true
}

func ValidateBytes(body []byte, inventory *Inventory) (*Profile, ValidationResult, error) {
	p, err := ParseYAML(body)
	if err != nil {
		return nil, ValidationResult{
			Valid: false,
			Findings: []Finding{{
				Severity: "error",
				Code:     "malformed_yaml",
				Message:  err.Error(),
			}},
		}, nil
	}
	canonicalHash, err := CanonicalHash(p)
	if err != nil {
		return nil, ValidationResult{}, err
	}
	findings := Validate(p, inventory)
	return p, ValidationResult{
		Valid:           IsValid(findings),
		SourceSHA256:    SourceHash(body),
		CanonicalSHA256: canonicalHash,
		Findings:        findings,
	}, nil
}

func EvaluateRule(values []string, rule Rule) (bool, error) {
	op := normalizeIdentifier(rule.Op)
	switch op {
	case "is_set":
		for _, value := range values {
			if strings.TrimSpace(value) != "" {
				return true, nil
			}
		}
		return false, nil
	case "is_empty":
		if len(values) == 0 {
			return true, nil
		}
		for _, value := range values {
			if strings.TrimSpace(value) != "" {
				return false, nil
			}
		}
		return true, nil
	}
	candidates := rule.Values
	if strings.TrimSpace(rule.Value) != "" {
		candidates = append(candidates, rule.Value)
	}
	switch op {
	case "equals":
		for _, value := range values {
			for _, candidate := range candidates {
				if strings.TrimSpace(value) == strings.TrimSpace(candidate) {
					return true, nil
				}
			}
		}
		return false, nil
	case "in":
		set := map[string]struct{}{}
		for _, candidate := range candidates {
			set[strings.TrimSpace(candidate)] = struct{}{}
		}
		for _, value := range values {
			if _, ok := set[strings.TrimSpace(value)]; ok {
				return true, nil
			}
		}
		return false, nil
	case "prefix", "iprefix":
		for _, value := range values {
			left := strings.TrimSpace(value)
			for _, candidate := range candidates {
				right := strings.TrimSpace(candidate)
				if op == "iprefix" {
					left = strings.ToLower(left)
					right = strings.ToLower(right)
				}
				if strings.HasPrefix(left, right) {
					return true, nil
				}
			}
		}
		return false, nil
	case "regex":
		if len(candidates) != 1 {
			return false, errors.New("regex rules require exactly one value")
		}
		re, err := compileSafeRegex(candidates[0])
		if err != nil {
			return false, err
		}
		for _, value := range values {
			if re.MatchString(value) {
				return true, nil
			}
		}
		return false, nil
	default:
		return false, fmt.Errorf("unsupported operator %q", rule.Op)
	}
}

func compileSafeRegex(pattern string) (*regexp.Regexp, error) {
	pattern = strings.TrimSpace(pattern)
	if len(pattern) > maxRegexLength {
		return nil, fmt.Errorf("regex exceeds maximum length %d", maxRegexLength)
	}
	if cached, ok := regexCache.Load(pattern); ok {
		return cached.(*regexp.Regexp), nil
	}
	compiled, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	actual, _ := regexCache.LoadOrStore(pattern, compiled)
	return actual.(*regexp.Regexp), nil
}

func Discover(inventory *Inventory) *Profile {
	p := &Profile{
		Version:     Version,
		Name:        "discovered",
		Objects:     map[string]ObjectMapping{},
		Fields:      map[string]FieldMapping{},
		Lifecycle:   map[string]LifecycleBucket{},
		Methodology: map[string]MethodologyConcept{},
	}
	if inventory == nil {
		addDefaultLifecycle(p)
		return p
	}
	deal := discoverObject(inventory.Objects, []string{"opportunity", "deal"}, "deal")
	account := discoverObject(inventory.Objects, []string{"account", "company"}, "account")
	if len(deal.ObjectTypes) > 0 {
		p.Objects["deal"] = deal
	}
	if len(account.ObjectTypes) > 0 {
		p.Objects["account"] = account
	}
	if _, ok := p.Objects["deal"]; ok {
		addDiscoveredField(p, inventory, "deal_stage", "deal", []string{"stagename", "stage", "stage_name", "dealstage", "deal_stage", "phase"})
		addDiscoveredField(p, inventory, "deal_amount", "deal", []string{"amount", "value", "arr", "contractvalue"})
		addDiscoveredField(p, inventory, "deal_owner", "deal", []string{"ownerid", "owner", "ae", "salesrep"})
		addDiscoveredField(p, inventory, "deal_close_date", "deal", []string{"closedate", "close_date", "contractdate"})
		addDiscoveredField(p, inventory, "deal_type", "deal", []string{"type", "dealtype", "opportunity_type"})
	}
	if _, ok := p.Objects["account"]; ok {
		addDiscoveredField(p, inventory, "account_type", "account", []string{"account_type__c", "type", "customer_status", "status"})
		addDiscoveredField(p, inventory, "account_industry", "account", []string{"industry", "vertical", "segment"})
	}
	addDefaultLifecycle(p)
	addLifecycleValuesFromInventory(p, inventory)
	return p
}

func normalizeProfile(p *Profile) {
	p.Name = strings.TrimSpace(p.Name)
	p.Objects = normalizeObjectMappings(p.Objects)
	p.Fields = normalizeFieldMappings(p.Fields)
	p.Lifecycle = normalizeLifecycleMappings(p.Lifecycle)
	p.Methodology = normalizeMethodologyMappings(p.Methodology)
}

func normalizeObjectMappings(in map[string]ObjectMapping) map[string]ObjectMapping {
	if len(in) == 0 {
		return nil
	}
	out := map[string]ObjectMapping{}
	for key, value := range in {
		canonical := normalizeIdentifier(key)
		if canonical == "" {
			continue
		}
		value.ObjectTypes = normalizeStringList(value.ObjectTypes, false)
		if value.Evidence != nil {
			value.Evidence.Values = normalizeStringList(value.Evidence.Values, false)
		}
		out[canonical] = value
	}
	return out
}

func normalizeFieldMappings(in map[string]FieldMapping) map[string]FieldMapping {
	if len(in) == 0 {
		return nil
	}
	out := map[string]FieldMapping{}
	for key, value := range in {
		canonical := normalizeIdentifier(key)
		if canonical == "" {
			continue
		}
		value.Object = normalizeIdentifier(value.Object)
		value.Names = normalizeStringList(value.Names, false)
		if value.Evidence != nil {
			value.Evidence.Values = normalizeStringList(value.Evidence.Values, false)
		}
		out[canonical] = value
	}
	return out
}

func normalizeLifecycleMappings(in map[string]LifecycleBucket) map[string]LifecycleBucket {
	if len(in) == 0 {
		return nil
	}
	out := map[string]LifecycleBucket{}
	for key, value := range in {
		canonical := normalizeIdentifier(key)
		if canonical == "" {
			continue
		}
		value.Label = strings.TrimSpace(value.Label)
		value.Description = strings.TrimSpace(value.Description)
		for idx := range value.Rules {
			value.Rules[idx].Field = normalizeIdentifier(value.Rules[idx].Field)
			value.Rules[idx].Object = normalizeIdentifier(value.Rules[idx].Object)
			value.Rules[idx].FieldName = strings.TrimSpace(value.Rules[idx].FieldName)
			value.Rules[idx].Op = normalizeIdentifier(value.Rules[idx].Op)
			value.Rules[idx].Value = strings.TrimSpace(value.Rules[idx].Value)
			value.Rules[idx].Values = normalizeStringList(value.Rules[idx].Values, false)
		}
		if value.Evidence != nil {
			value.Evidence.Values = normalizeStringList(value.Evidence.Values, false)
		}
		out[canonical] = value
	}
	return out
}

func normalizeMethodologyMappings(in map[string]MethodologyConcept) map[string]MethodologyConcept {
	if len(in) == 0 {
		return nil
	}
	out := map[string]MethodologyConcept{}
	for key, value := range in {
		canonical := normalizeIdentifier(key)
		if canonical == "" {
			continue
		}
		value.Description = strings.TrimSpace(value.Description)
		value.Aliases = normalizeStringList(value.Aliases, false)
		value.TrackerIDs = normalizeStringList(value.TrackerIDs, false)
		value.ScorecardQuestionIDs = normalizeStringList(value.ScorecardQuestionIDs, false)
		for idx := range value.Fields {
			value.Fields[idx].Object = normalizeIdentifier(value.Fields[idx].Object)
			value.Fields[idx].Name = strings.TrimSpace(value.Fields[idx].Name)
		}
		out[canonical] = value
	}
	return out
}

func validateRule(p *Profile, rule Rule, path string) []Finding {
	var findings []Finding
	op := normalizeIdentifier(rule.Op)
	if _, ok := allowedOperators[op]; !ok {
		findings = append(findings, Finding{Severity: "error", Code: "unsupported_rule_operator", Message: fmt.Sprintf("rule operator %q is not supported", rule.Op), Path: path + ".op"})
	}
	if rule.Field == "" && (rule.Object == "" || rule.FieldName == "") {
		findings = append(findings, Finding{Severity: "error", Code: "rule_missing_field", Message: "rule must reference a field concept or direct object/field_name", Path: path})
	}
	if rule.Field != "" {
		if _, ok := p.Fields[rule.Field]; !ok {
			findings = append(findings, Finding{Severity: "error", Code: "unknown_field_concept", Message: fmt.Sprintf("rule references unknown field concept %q", rule.Field), Path: path + ".field"})
		}
	}
	if rule.Object != "" {
		if _, ok := p.Objects[rule.Object]; !ok {
			findings = append(findings, Finding{Severity: "error", Code: "unknown_object_alias", Message: fmt.Sprintf("rule references unknown object alias %q", rule.Object), Path: path + ".object"})
		}
	}
	if op == "regex" {
		patterns := rule.Values
		if strings.TrimSpace(rule.Value) != "" {
			patterns = append(patterns, rule.Value)
		}
		if len(patterns) != 1 {
			findings = append(findings, Finding{Severity: "error", Code: "regex_value_count", Message: "regex rules require exactly one value", Path: path + ".value"})
		} else if _, err := compileSafeRegex(patterns[0]); err != nil {
			if len(strings.TrimSpace(patterns[0])) > maxRegexLength {
				findings = append(findings, Finding{Severity: "error", Code: "regex_too_long", Message: fmt.Sprintf("regex exceeds maximum length %d", maxRegexLength), Path: path + ".value"})
				return findings
			}
			findings = append(findings, Finding{Severity: "error", Code: "regex_invalid", Message: err.Error(), Path: path + ".value"})
		}
	}
	if op != "is_set" && op != "is_empty" && strings.TrimSpace(rule.Value) == "" && len(rule.Values) == 0 {
		findings = append(findings, Finding{Severity: "error", Code: "rule_missing_value", Message: fmt.Sprintf("operator %q requires value or values", op), Path: path})
	}
	return findings
}

func validateAgainstInventory(p *Profile, inventory *Inventory) []Finding {
	var findings []Finding
	objectTypes := map[string]struct{}{}
	fields := map[string]struct{}{}
	for _, object := range inventory.Objects {
		objectTypes[object.ObjectType] = struct{}{}
	}
	for _, field := range inventory.Fields {
		fields[field.ObjectType+"."+field.FieldName] = struct{}{}
		objectTypes[field.ObjectType] = struct{}{}
	}
	for concept, mapping := range p.Objects {
		found := false
		for _, objectType := range mapping.ObjectTypes {
			if _, ok := objectTypes[objectType]; ok {
				found = true
			} else {
				findings = append(findings, Finding{Severity: "warn", Code: "object_type_not_seen", Message: fmt.Sprintf("object type %q for alias %q was not seen in cached data", objectType, concept), Path: "objects." + concept})
			}
		}
		if !found && len(mapping.ObjectTypes) > 0 {
			findings = append(findings, Finding{Severity: "warn", Code: "object_alias_no_seen_types", Message: fmt.Sprintf("object alias %q has no object_types seen in cached data", concept), Path: "objects." + concept})
		}
	}
	for concept, mapping := range p.Fields {
		objectMapping, ok := p.Objects[mapping.Object]
		if !ok {
			continue
		}
		found := false
		for _, objectType := range objectMapping.ObjectTypes {
			for _, fieldName := range mapping.Names {
				if _, ok := fields[objectType+"."+fieldName]; ok {
					found = true
				}
			}
		}
		if !found && len(mapping.Names) > 0 {
			findings = append(findings, Finding{Severity: "error", Code: "field_mapping_not_seen", Message: fmt.Sprintf("field concept %q does not match any cached field for object alias %q", concept, mapping.Object), Path: "fields." + concept})
		}
		if mapping.Confidence > 0 && mapping.Confidence < 0.6 {
			findings = append(findings, Finding{Severity: "warn", Code: "low_confidence_mapping", Message: fmt.Sprintf("field concept %q has low discovery confidence %.2f", concept, mapping.Confidence), Path: "fields." + concept + ".confidence"})
		}
	}
	for bucket, lifecycle := range p.Lifecycle {
		for idx, rule := range lifecycle.Rules {
			if rule.Object == "" || rule.FieldName == "" {
				continue
			}
			objectMapping, ok := p.Objects[rule.Object]
			if !ok {
				continue
			}
			found := false
			for _, objectType := range objectMapping.ObjectTypes {
				if _, ok := fields[objectType+"."+rule.FieldName]; ok {
					found = true
					break
				}
			}
			if !found {
				findings = append(findings, Finding{
					Severity: "error",
					Code:     "rule_field_not_seen",
					Message:  fmt.Sprintf("lifecycle rule %q[%d] references field %q that was not seen for object alias %q", bucket, idx, rule.FieldName, rule.Object),
					Path:     fmt.Sprintf("lifecycle.%s.rules.%d.field_name", bucket, idx),
				})
			}
		}
	}
	return findings
}

func discoverObject(objects []ObjectInventory, needles []string, concept string) ObjectMapping {
	var best ObjectInventory
	bestScore := -1
	for _, object := range objects {
		name := normalizeComparable(object.ObjectType)
		score := 0
		for _, needle := range needles {
			if strings.Contains(name, normalizeComparable(needle)) {
				score += 10
			}
		}
		score += object.ObjectCount
		if score > bestScore && score >= 10 {
			best = object
			bestScore = score
		}
	}
	if bestScore < 10 {
		return ObjectMapping{}
	}
	confidence := 0.7
	if bestScore >= 20 {
		confidence = 0.9
	}
	return ObjectMapping{
		ObjectTypes: []string{best.ObjectType},
		Confidence:  confidence,
		Evidence: &Evidence{
			Source:           best.ObjectType,
			MatchedHeuristic: concept + "_object_name",
			SampleSize:       best.ObjectCount,
		},
	}
}

func addDiscoveredField(p *Profile, inventory *Inventory, concept string, objectConcept string, needles []string) {
	objectMapping, ok := p.Objects[objectConcept]
	if !ok {
		return
	}
	var best FieldInventory
	bestScore := -1
	for _, field := range inventory.Fields {
		if !containsExact(objectMapping.ObjectTypes, field.ObjectType) {
			continue
		}
		normalizedField := normalizeComparable(field.FieldName)
		normalizedLabel := normalizeComparable(field.FieldLabel)
		score := field.PopulatedCount
		for _, needle := range needles {
			n := normalizeComparable(needle)
			if normalizedField == n {
				score += 30
			} else if strings.Contains(normalizedField, n) {
				score += 15
			}
			if normalizedLabel == n {
				score += 20
			} else if strings.Contains(normalizedLabel, n) {
				score += 10
			}
		}
		if score > bestScore && score >= 10 {
			best = field
			bestScore = score
		}
	}
	if bestScore < 10 {
		return
	}
	confidence := 0.65
	if bestScore >= 30 {
		confidence = 0.85
	}
	p.Fields[concept] = FieldMapping{
		Object:     objectConcept,
		Names:      []string{best.FieldName},
		Confidence: confidence,
		Evidence: &Evidence{
			Source:             best.ObjectType + "." + best.FieldName,
			MatchedHeuristic:   concept + "_field_name_or_label",
			SampleSize:         best.ObjectCount,
			DistinctValueCount: len(best.DistinctValues),
			Values:             truncateStrings(best.DistinctValues, 10),
		},
	}
}

func addDefaultLifecycle(p *Profile) {
	ensureLifecycle(p, "open", "Open", 10)
	ensureLifecycle(p, "closed_won", "Closed Won", 20)
	ensureLifecycle(p, "closed_lost", "Closed Lost", 30)
	ensureLifecycle(p, "post_sales", "Post Sales", 40)
	ensureLifecycle(p, "unknown", "Unknown", 999)
}

func addLifecycleValuesFromInventory(p *Profile, inventory *Inventory) {
	stage, ok := p.Fields["deal_stage"]
	if ok && len(stage.Names) > 0 {
		values := distinctValuesForField(inventory, p.Objects[stage.Object].ObjectTypes, stage.Names)
		var openValues, wonValues, lostValues []string
		for _, value := range values {
			normalized := strings.ToLower(value)
			switch {
			case strings.Contains(normalized, "closed won") || normalized == "won":
				wonValues = append(wonValues, value)
			case strings.Contains(normalized, "closed lost") || normalized == "lost":
				lostValues = append(lostValues, value)
			default:
				openValues = append(openValues, value)
			}
		}
		if len(openValues) > 0 {
			bucket := p.Lifecycle["open"]
			bucket.Rules = []Rule{{Field: "deal_stage", Op: "in", Values: truncateStrings(openValues, 50)}}
			bucket.Confidence = stage.Confidence
			p.Lifecycle["open"] = bucket
		}
		if len(wonValues) > 0 {
			bucket := p.Lifecycle["closed_won"]
			bucket.Rules = []Rule{{Field: "deal_stage", Op: "in", Values: wonValues}}
			bucket.Confidence = stage.Confidence
			p.Lifecycle["closed_won"] = bucket
		}
		if len(lostValues) > 0 {
			bucket := p.Lifecycle["closed_lost"]
			bucket.Rules = []Rule{{Field: "deal_stage", Op: "in", Values: lostValues}}
			bucket.Confidence = stage.Confidence
			p.Lifecycle["closed_lost"] = bucket
		}
	}
	if _, ok := p.Fields["deal_type"]; ok {
		bucket := p.Lifecycle["post_sales"]
		bucket.Rules = append(bucket.Rules, Rule{Field: "deal_type", Op: "in", Values: []string{"Renewal", "Upsell", "Expansion", "Existing Business"}})
		p.Lifecycle["post_sales"] = bucket
	}
	if _, ok := p.Fields["account_type"]; ok && len(p.Lifecycle["post_sales"].Rules) == 0 {
		bucket := p.Lifecycle["post_sales"]
		bucket.Rules = append(bucket.Rules, Rule{Field: "account_type", Op: "iprefix", Value: "customer"})
		p.Lifecycle["post_sales"] = bucket
	}
}

func ensureLifecycle(p *Profile, bucket string, label string, order int) {
	if p.Lifecycle == nil {
		p.Lifecycle = map[string]LifecycleBucket{}
	}
	current := p.Lifecycle[bucket]
	if current.Label == "" {
		current.Label = label
	}
	if current.Order == 0 {
		current.Order = order
	}
	p.Lifecycle[bucket] = current
}

func distinctValuesForField(inventory *Inventory, objectTypes []string, fieldNames []string) []string {
	set := map[string]struct{}{}
	for _, field := range inventory.Fields {
		if !containsExact(objectTypes, field.ObjectType) || !containsExact(fieldNames, field.FieldName) {
			continue
		}
		for _, value := range field.DistinctValues {
			if strings.TrimSpace(value) != "" {
				set[value] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func sortFindings(findings []Finding) {
	priority := map[string]int{"error": 0, "warn": 1, "info": 2}
	sort.SliceStable(findings, func(i, j int) bool {
		left := priority[findings[i].Severity]
		right := priority[findings[j].Severity]
		if left != right {
			return left < right
		}
		if findings[i].Path != findings[j].Path {
			return findings[i].Path < findings[j].Path
		}
		return findings[i].Code < findings[j].Code
	})
}

func normalizeIdentifier(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	return value
}

func normalizeComparable(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	replacer := strings.NewReplacer("_", "", "-", "", " ", "", ".", "")
	return replacer.Replace(value)
}

func normalizeStringList(values []string, lower bool) []string {
	set := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if lower {
			value = strings.ToLower(value)
		}
		if value == "" {
			continue
		}
		set[value] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func containsExact(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func truncateStrings(values []string, limit int) []string {
	if len(values) <= limit {
		return values
	}
	return values[:limit]
}
