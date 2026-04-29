package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/arthurlee/gongctl/internal/mcp"
	"github.com/arthurlee/gongctl/internal/store/sqlite"
	"github.com/arthurlee/gongctl/internal/version"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("gongmcp", flag.ContinueOnError)
	flags.SetOutput(stderr)

	dbPath := flags.String("db", "", "Path to the local gongctl SQLite cache")
	transcriptEvidenceProvenance := flags.String("transcript-evidence-provenance", envDefault("GONGMCP_TRANSCRIPT_EVIDENCE_PROVENANCE", "redacted"), "Transcript evidence provenance mode: redacted, alias, or raw")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "unexpected arguments: %s\n", strings.Join(flags.Args(), " "))
		return 2
	}

	db := strings.TrimSpace(*dbPath)
	if db == "" {
		fmt.Fprintln(stderr, "--db is required")
		return 2
	}
	if _, err := os.Stat(db); err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(stderr, "db file not found: %s\n", filepath.Clean(db))
			return 2
		}
		fmt.Fprintf(stderr, "stat db: %v\n", err)
		return 1
	}
	provenance, err := mcp.ParseTranscriptEvidenceProvenance(*transcriptEvidenceProvenance)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}

	ctx := context.Background()
	store, err := sqlite.OpenReadOnly(ctx, db)
	if err != nil {
		fmt.Fprintf(stderr, "open db: %v\n", err)
		return 1
	}
	defer store.Close()

	server := mcp.NewServerWithOptions(store, "gongmcp", version.DisplayVersion(), mcp.ServerOptions{
		TranscriptEvidenceProvenance: provenance,
	})
	if err := server.Serve(ctx, stdin, stdout); err != nil {
		fmt.Fprintf(stderr, "serve mcp: %v\n", err)
		return 1
	}
	return 0
}

func envDefault(name, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return value
}
