package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	profilepkg "github.com/arthurlee/gongctl/internal/profile"
	"github.com/arthurlee/gongctl/internal/store/sqlite"
)

func (a *app) profile(ctx context.Context, args []string) error {
	if len(args) == 0 {
		fmt.Fprintln(a.err, "usage: gongctl profile [discover|validate|import|show]")
		return errUsage
	}
	switch args[0] {
	case "discover":
		return a.profileDiscover(ctx, args[1:])
	case "validate":
		return a.profileValidate(ctx, args[1:])
	case "import":
		return a.profileImport(ctx, args[1:])
	case "show":
		return a.profileShow(ctx, args[1:])
	default:
		fmt.Fprintf(a.err, "unknown profile command %q\n", args[0])
		return errUsage
	}
}

func (a *app) profileDiscover(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("profile discover", flag.ContinueOnError)
	fs.SetOutput(a.err)
	dbPath := fs.String("db", "", "SQLite database path")
	outPath := fs.String("out", "-", "output YAML profile path, or - for stdout")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	store, err := openSQLiteStore(ctx, *dbPath)
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
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	_, result, err := a.validateProfileFile(ctx, *dbPath, *profilePath)
	if err != nil {
		return err
	}
	return writeJSONValue(a.out, result)
}

func (a *app) profileImport(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("profile import", flag.ContinueOnError)
	fs.SetOutput(a.err)
	dbPath := fs.String("db", "", "SQLite database path")
	profilePath := fs.String("profile", "", "YAML profile path")
	if err := fs.Parse(args); err != nil {
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
	store, err := openSQLiteStore(ctx, *dbPath)
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
	})
	if err != nil {
		return err
	}
	return writeJSONValue(a.out, profileImportResponse{
		Import:     result,
		Validation: validation,
	})
}

func (a *app) profileShow(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("profile show", flag.ContinueOnError)
	fs.SetOutput(a.err)
	dbPath := fs.String("db", "", "SQLite database path")
	format := fs.String("format", "json", "output format: json or yaml")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	store, err := openSQLiteStore(ctx, *dbPath)
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
	store, err := openSQLiteStore(ctx, dbPath)
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
