package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"

	profilepkg "github.com/fyne-coder/gongcli_mcp/internal/profile"
	"github.com/fyne-coder/gongcli_mcp/internal/store/postgres"
	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
)

func (a *app) profile(ctx context.Context, args []string) error {
	if len(args) == 0 {
		a.profileUsage()
		return errUsage
	}
	switch args[0] {
	case "help", "-h", "--help":
		a.profileUsage()
		return nil
	case "discover":
		return a.profileDiscover(ctx, args[1:])
	case "validate":
		return a.profileValidate(ctx, args[1:])
	case "import":
		return a.profileImport(ctx, args[1:])
	case "show":
		return a.profileShow(ctx, args[1:])
	case "history":
		return a.profileHistory(ctx, args[1:])
	case "activate":
		return a.profileActivate(ctx, args[1:])
	case "diff":
		return a.profileDiff(ctx, args[1:])
	case "schema":
		return a.profileSchema(ctx, args[1:])
	default:
		fmt.Fprintf(a.err, "unknown profile command %q\n", args[0])
		return errUsage
	}
}

func (a *app) profileUsage() {
	fmt.Fprint(a.err, `Usage:
  gongctl profile discover --db gong.db --out gongctl-profile.yaml
  gongctl profile validate --db gong.db --profile gongctl-profile.yaml
  gongctl profile import --db gong.db --profile gongctl-profile.yaml [--activate=false]
  gongctl profile show --db gong.db [--format json|yaml]
  gongctl profile history --db gong.db
  gongctl profile activate --db gong.db (--id ID|--canonical-sha SHA)
  gongctl profile diff --db gong.db --from active --to gongctl-profile.yaml
  gongctl profile schema
`)
}

type profileInventoryStore interface {
	ProfileInventory(context.Context) (*profilepkg.Inventory, error)
	Close() error
}

type writableProfileStore interface {
	profileInventoryStore
	ImportProfile(context.Context, sqlite.ProfileImportParams) (*sqlite.ProfileImportResult, error)
	ListProfiles(context.Context) ([]sqlite.ProfileHistoryEntry, error)
	ProfileDocument(context.Context, string) (*sqlite.StoredProfileDocument, error)
	ActivateProfile(context.Context, string) (*sqlite.ProfileImportResult, error)
	ActiveBusinessProfile(context.Context) (*sqlite.BusinessProfile, error)
	ActiveProfileDocument(context.Context) (*profilepkg.Profile, error)
}

type profileDocumentStore interface {
	ProfileDocument(context.Context, string) (*sqlite.StoredProfileDocument, error)
}

func openProfileInventoryStore(ctx context.Context, path string) (profileInventoryStore, error) {
	if strings.TrimSpace(path) != "" {
		return sqlite.Open(ctx, path)
	}
	databaseURL := postgres.URLFromEnv(os.Getenv)
	if databaseURL == "" {
		return nil, errors.New("--db is required")
	}
	return postgres.OpenProfileInventory(ctx, databaseURL)
}

func openWritableProfileStore(ctx context.Context, path string) (writableProfileStore, error) {
	if strings.TrimSpace(path) != "" {
		return sqlite.Open(ctx, path)
	}
	databaseURL := postgres.URLFromEnv(os.Getenv)
	if databaseURL == "" {
		return nil, errors.New("--db is required")
	}
	return postgres.Open(ctx, databaseURL)
}

func (a *app) profileDiscover(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("profile discover", flag.ContinueOnError)
	fs.SetOutput(a.err)
	dbPath := fs.String("db", "", "SQLite database path")
	outPath := fs.String("out", "-", "output YAML profile path, or - for stdout")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return errUsage
	}
	store, err := openProfileInventoryStore(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer store.Close()
	inventory, err := store.ProfileInventory(ctx)
	if err != nil {
		return err
	}
	p := profilepkg.Discover(inventory)
	body, err := profilepkg.MarshalYAML(p)
	if err != nil {
		return err
	}
	return writeOutput(*outPath, a.out, body)
}

func (a *app) profileValidate(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("profile validate", flag.ContinueOnError)
	fs.SetOutput(a.err)
	dbPath := fs.String("db", "", "SQLite database path")
	profilePath := fs.String("profile", "", "YAML profile path")
	gaReadiness := fs.Bool("ga-readiness", false, "fail with non-zero exit when the mechanical GA readiness checklist has blocking findings (CreatedDate-only field concepts, missing lifecycle buckets, methodology unmapped, loss-reason mapping missing); the JSON report is still emitted")
	strictReadiness := fs.Bool("strict-readiness", false, "alias of --ga-readiness")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return errUsage
	}
	p, result, err := a.validateProfileFile(ctx, *dbPath, *profilePath)
	if err != nil {
		return err
	}
	strict := *gaReadiness || *strictReadiness
	if !strict {
		return writeJSONValue(a.out, result)
	}
	checklist := sqlite.EvaluateProfileReadinessChecklist(p)
	gaReport := buildProfileGAReadinessReport(checklist)
	output := struct {
		profilepkg.ValidationResult
		GAReadiness profileGAReadinessReport `json:"ga_readiness"`
	}{
		ValidationResult: result,
		GAReadiness:      gaReport,
	}
	if err := writeJSONValue(a.out, output); err != nil {
		return err
	}
	if !result.Valid {
		return fmt.Errorf("profile validation failed; see findings in JSON output")
	}
	if len(gaReport.BlockingFindings) > 0 {
		return fmt.Errorf("GA readiness gate failed: %d blocking finding(s); see ga_readiness.blocking_findings in JSON output", len(gaReport.BlockingFindings))
	}
	return nil
}

// profileGAReadinessReport is the operator-visible structure surfaced by
// `gongctl profile validate --ga-readiness`. It exposes the mechanical
// readiness checklist and the subset of findings that block GA gating.
type profileGAReadinessReport struct {
	Checklist        sqlite.ProfileReadinessChecklist `json:"checklist"`
	BlockingFindings []string                         `json:"blocking_findings"`
	Passed           bool                             `json:"passed"`
}

func buildProfileGAReadinessReport(checklist sqlite.ProfileReadinessChecklist) profileGAReadinessReport {
	report := profileGAReadinessReport{Checklist: checklist, BlockingFindings: []string{}}
	if len(checklist.CreatedDateOnlyConcepts) > 0 {
		report.BlockingFindings = append(report.BlockingFindings,
			"created_date_only_field_concepts: "+strings.Join(checklist.CreatedDateOnlyConcepts, ","))
	}
	if len(checklist.MissingLifecycleBuckets) > 0 {
		report.BlockingFindings = append(report.BlockingFindings,
			"missing_lifecycle_buckets: "+strings.Join(checklist.MissingLifecycleBuckets, ","))
	}
	if checklist.MethodologyUnmapped {
		report.BlockingFindings = append(report.BlockingFindings,
			"methodology_concepts_unmapped: profile defines no methodology concepts (pain, next steps, MEDDICC, etc.)")
	}
	if checklist.LossReasonMappingMissing {
		report.BlockingFindings = append(report.BlockingFindings,
			"loss_reason_mapping_missing: no field concept references a known loss-reason field")
	}
	sort.Strings(report.BlockingFindings)
	report.Passed = len(report.BlockingFindings) == 0
	return report
}

func (a *app) profileImport(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("profile import", flag.ContinueOnError)
	fs.SetOutput(a.err)
	dbPath := fs.String("db", "", "SQLite database path")
	profilePath := fs.String("profile", "", "YAML profile path")
	activate := fs.Bool("activate", true, "activate profile immediately; set --activate=false to stage without changing the active profile")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return errUsage
	}
	p, validation, err := a.validateProfileFile(ctx, *dbPath, *profilePath)
	if err != nil {
		return err
	}
	if !validation.Valid {
		return fmt.Errorf("profile validation failed; fix error findings before import")
	}
	body, err := os.ReadFile(*profilePath)
	if err != nil {
		return err
	}
	canonical, err := profilepkg.CanonicalJSON(p)
	if err != nil {
		return err
	}
	store, err := openWritableProfileStore(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer store.Close()
	result, err := store.ImportProfile(ctx, sqlite.ProfileImportParams{
		SourcePath:      *profilePath,
		SourceSHA256:    validation.SourceSHA256,
		CanonicalSHA256: validation.CanonicalSHA256,
		RawYAML:         body,
		CanonicalJSON:   canonical,
		Profile:         p,
		Findings:        validation.Findings,
		StageOnly:       !*activate,
	})
	if err != nil {
		return err
	}
	return writeJSONValue(a.out, profileImportResponse{
		Import:     result,
		Validation: validation,
	})
}

func (a *app) profileHistory(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("profile history", flag.ContinueOnError)
	fs.SetOutput(a.err)
	dbPath := fs.String("db", "", "SQLite database path")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return errUsage
	}
	store, err := openWritableProfileStore(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer store.Close()
	profiles, err := store.ListProfiles(ctx)
	if err != nil {
		return err
	}
	return writeJSONValue(a.out, struct {
		Profiles []sqlite.ProfileHistoryEntry `json:"profiles"`
	}{
		Profiles: profiles,
	})
}

func (a *app) profileActivate(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("profile activate", flag.ContinueOnError)
	fs.SetOutput(a.err)
	dbPath := fs.String("db", "", "SQLite database path")
	id := fs.String("id", "", "profile id to activate")
	canonicalSHA := fs.String("canonical-sha", "", "canonical_sha256 or prefix to activate")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return errUsage
	}
	ref := strings.TrimSpace(*id)
	if ref == "" {
		ref = strings.TrimSpace(*canonicalSHA)
		if !strings.HasPrefix(strings.ToLower(ref), "sha:") {
			ref = "sha:" + ref
		}
	}
	if ref == "" || (strings.TrimSpace(*id) != "" && strings.TrimSpace(*canonicalSHA) != "") {
		return fmt.Errorf("set exactly one of --id or --canonical-sha")
	}
	store, err := openWritableProfileStore(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer store.Close()
	result, err := store.ActivateProfile(ctx, ref)
	if err != nil {
		return err
	}
	return writeJSONValue(a.out, struct {
		Activation *sqlite.ProfileImportResult `json:"activation"`
	}{
		Activation: result,
	})
}

func (a *app) profileDiff(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("profile diff", flag.ContinueOnError)
	fs.SetOutput(a.err)
	dbPath := fs.String("db", "", "SQLite database path")
	fromRef := fs.String("from", "active", "source profile: active, profile id, canonical_sha256 prefix, or YAML file path")
	toRef := fs.String("to", "", "target profile: active, profile id, canonical_sha256 prefix, or YAML file path")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return errUsage
	}
	if strings.TrimSpace(*toRef) == "" {
		return fmt.Errorf("--to is required")
	}
	store, err := openWritableProfileStore(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer store.Close()
	from, err := loadProfileDiffSide(ctx, store, *fromRef)
	if err != nil {
		return fmt.Errorf("load --from: %w", err)
	}
	to, err := loadProfileDiffSide(ctx, store, *toRef)
	if err != nil {
		return fmt.Errorf("load --to: %w", err)
	}
	return writeJSONValue(a.out, profileDiffProfiles(from, to))
}

func (a *app) profileSchema(ctx context.Context, args []string) error {
	_ = ctx
	fs := flag.NewFlagSet("profile schema", flag.ContinueOnError)
	fs.SetOutput(a.err)
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return errUsage
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected profile schema arguments: %v", fs.Args())
	}
	return writeJSONValue(a.out, profileSchemaDocument())
}

func (a *app) profileShow(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("profile show", flag.ContinueOnError)
	fs.SetOutput(a.err)
	dbPath := fs.String("db", "", "SQLite database path")
	format := fs.String("format", "json", "output format: json or yaml")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return errUsage
	}
	store, err := openWritableProfileStore(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer store.Close()
	switch strings.ToLower(strings.TrimSpace(*format)) {
	case "", "json":
		profile, err := store.ActiveBusinessProfile(ctx)
		if err != nil {
			return err
		}
		return writeJSONValue(a.out, profile)
	case "yaml":
		p, err := store.ActiveProfileDocument(ctx)
		if err != nil {
			return err
		}
		body, err := profilepkg.MarshalYAML(p)
		if err != nil {
			return err
		}
		_, err = a.out.Write(body)
		return err
	default:
		return fmt.Errorf("--format must be json or yaml")
	}
}

func (a *app) validateProfileFile(ctx context.Context, dbPath string, profilePath string) (*profilepkg.Profile, profilepkg.ValidationResult, error) {
	if strings.TrimSpace(profilePath) == "" {
		return nil, profilepkg.ValidationResult{}, fmt.Errorf("--profile is required")
	}
	body, err := os.ReadFile(profilePath)
	if err != nil {
		return nil, profilepkg.ValidationResult{}, err
	}
	store, err := openProfileInventoryStore(ctx, dbPath)
	if err != nil {
		return nil, profilepkg.ValidationResult{}, err
	}
	defer store.Close()
	inventory, err := store.ProfileInventory(ctx)
	if err != nil {
		return nil, profilepkg.ValidationResult{}, err
	}
	return profilepkg.ValidateBytes(body, inventory)
}

type profileImportResponse struct {
	Import     *sqlite.ProfileImportResult `json:"import"`
	Validation profilepkg.ValidationResult `json:"validation"`
}

type profileDiffSide struct {
	Ref       string              `json:"ref"`
	Meta      any                 `json:"meta,omitempty"`
	Canonical map[string]any      `json:"-"`
	Profile   *profilepkg.Profile `json:"-"`
}

type profileDiffResponse struct {
	From     profileDiffSide      `json:"from"`
	To       profileDiffSide      `json:"to"`
	Changed  bool                 `json:"changed"`
	Sections []profileSectionDiff `json:"sections"`
}

type profileSectionDiff struct {
	Section string   `json:"section"`
	Added   []string `json:"added,omitempty"`
	Removed []string `json:"removed,omitempty"`
	Changed []string `json:"changed,omitempty"`
}

func loadProfileDiffSide(ctx context.Context, store profileDocumentStore, ref string) (profileDiffSide, error) {
	trimmed := strings.TrimSpace(ref)
	if trimmed == "" {
		trimmed = "active"
	}
	if strings.EqualFold(trimmed, "active") {
		doc, err := store.ProfileDocument(ctx, trimmed)
		if err != nil {
			return profileDiffSide{}, err
		}
		canonical, err := profileCanonicalMap(doc.Profile)
		if err != nil {
			return profileDiffSide{}, err
		}
		return profileDiffSide{Ref: trimmed, Meta: doc.Meta, Canonical: canonical, Profile: doc.Profile}, nil
	}
	if info, err := os.Stat(trimmed); err == nil && !info.IsDir() {
		body, err := os.ReadFile(trimmed)
		if err != nil {
			return profileDiffSide{}, err
		}
		p, err := profilepkg.ParseYAML(body)
		if err != nil {
			return profileDiffSide{}, err
		}
		canonical, err := profileCanonicalMap(p)
		if err != nil {
			return profileDiffSide{}, err
		}
		return profileDiffSide{Ref: trimmed, Meta: map[string]string{"source": "file"}, Canonical: canonical, Profile: p}, nil
	}
	doc, err := store.ProfileDocument(ctx, trimmed)
	if err != nil {
		return profileDiffSide{}, err
	}
	canonical, err := profileCanonicalMap(doc.Profile)
	if err != nil {
		return profileDiffSide{}, err
	}
	return profileDiffSide{Ref: trimmed, Meta: doc.Meta, Canonical: canonical, Profile: doc.Profile}, nil
}

func profileCanonicalMap(p *profilepkg.Profile) (map[string]any, error) {
	body, err := profilepkg.CanonicalJSON(p)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func profileDiffProfiles(from, to profileDiffSide) profileDiffResponse {
	sections := []profileSectionDiff{}
	for _, section := range []string{"version", "name", "objects", "fields", "lifecycle", "methodology"} {
		diff := diffProfileSection(section, from.Canonical[section], to.Canonical[section])
		if len(diff.Added) > 0 || len(diff.Removed) > 0 || len(diff.Changed) > 0 {
			sections = append(sections, diff)
		}
	}
	return profileDiffResponse{
		From:     profileDiffSide{Ref: from.Ref, Meta: from.Meta},
		To:       profileDiffSide{Ref: to.Ref, Meta: to.Meta},
		Changed:  len(sections) > 0,
		Sections: sections,
	}
}

func diffProfileSection(section string, from, to any) profileSectionDiff {
	out := profileSectionDiff{Section: section}
	fromMap, fromOK := from.(map[string]any)
	toMap, toOK := to.(map[string]any)
	if !fromOK || !toOK {
		if !reflect.DeepEqual(from, to) {
			out.Changed = []string{section}
		}
		return out
	}
	keys := map[string]struct{}{}
	for key := range fromMap {
		keys[key] = struct{}{}
	}
	for key := range toMap {
		keys[key] = struct{}{}
	}
	for key := range keys {
		_, inFrom := fromMap[key]
		_, inTo := toMap[key]
		switch {
		case !inFrom:
			out.Added = append(out.Added, key)
		case !inTo:
			out.Removed = append(out.Removed, key)
		case !reflect.DeepEqual(fromMap[key], toMap[key]):
			out.Changed = append(out.Changed, key)
		}
	}
	sort.Strings(out.Added)
	sort.Strings(out.Removed)
	sort.Strings(out.Changed)
	return out
}

func profileSchemaDocument() map[string]any {
	stringArray := map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	evidence := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"source":               map[string]any{"type": "string"},
			"matched_heuristic":    map[string]any{"type": "string"},
			"sample_size":          map[string]any{"type": "integer"},
			"distinct_value_count": map[string]any{"type": "integer"},
			"values":               stringArray,
		},
	}
	return map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"$id":                  "https://github.com/fyne-coder/gongcli_mcp/schemas/business-profile.v1.schema.json",
		"title":                "gongctl business profile",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"version", "lifecycle"},
		"properties": map[string]any{
			"version": map[string]any{"type": "integer", "const": profilepkg.Version},
			"name":    map[string]any{"type": "string"},
			"objects": map[string]any{
				"type": "object",
				"additionalProperties": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"object_types": stringArray,
						"confidence":   map[string]any{"type": "number"},
						"evidence":     evidence,
					},
				},
			},
			"fields": map[string]any{
				"type": "object",
				"additionalProperties": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"object":     map[string]any{"type": "string"},
						"names":      stringArray,
						"confidence": map[string]any{"type": "number"},
						"evidence":   evidence,
					},
				},
			},
			"lifecycle": map[string]any{
				"type": "object",
				"required": []string{
					"open",
					"closed_won",
					"closed_lost",
					"post_sales",
					"unknown",
				},
				"additionalProperties": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"label":       map[string]any{"type": "string"},
						"description": map[string]any{"type": "string"},
						"order":       map[string]any{"type": "integer"},
						"confidence":  map[string]any{"type": "number"},
						"evidence":    evidence,
						"rules": map[string]any{
							"type": "array",
							"items": map[string]any{
								"type":                 "object",
								"additionalProperties": false,
								"required":             []string{"op"},
								"properties": map[string]any{
									"field":      map[string]any{"type": "string"},
									"object":     map[string]any{"type": "string"},
									"field_name": map[string]any{"type": "string"},
									"op":         map[string]any{"type": "string", "enum": []string{"equals", "in", "prefix", "iprefix", "regex", "is_set", "is_empty"}},
									"value":      map[string]any{"type": "string"},
									"values":     stringArray,
								},
							},
						},
					},
				},
			},
			"methodology": map[string]any{
				"type": "object",
				"additionalProperties": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"description":            map[string]any{"type": "string"},
						"aliases":                stringArray,
						"tracker_ids":            stringArray,
						"scorecard_question_ids": stringArray,
						"fields":                 map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"object": map[string]any{"type": "string"}, "name": map[string]any{"type": "string"}}}},
					},
				},
			},
		},
	}
}
