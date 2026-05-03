package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"

	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
	transcriptsearch "github.com/fyne-coder/gongcli_mcp/internal/transcripts"
)

func (a *app) search(ctx context.Context, args []string) error {
	if len(args) == 0 {
		fmt.Fprintln(a.err, "usage: gongctl search [transcripts|calls]")
		return errUsage
	}

	switch args[0] {
	case "transcripts":
		return a.searchTranscripts(ctx, args[1:])
	case "calls":
		return a.searchCalls(ctx, args[1:])
	default:
		fmt.Fprintf(a.err, "unknown search command %q\n", args[0])
		return errUsage
	}
}

func (a *app) searchTranscripts(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("search transcripts", flag.ContinueOnError)
	fs.SetOutput(a.err)
	dbPath := fs.String("db", "", "SQLite database path")
	query := fs.String("query", "", "full-text transcript query")
	limit := fs.Int("limit", 20, "maximum results to return")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	if *query == "" {
		return fmt.Errorf("--query is required")
	}

	store, err := openSQLiteReadOnlyStore(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	results, err := transcriptsearch.SearchTranscripts(ctx, store, *query, *limit)
	if err != nil {
		return err
	}

	response := transcriptSearchResponse{
		Query:   *query,
		Limit:   normalizeSearchLimit(*limit, 20),
		Results: []transcriptSearchResultJSON{},
	}
	for _, result := range results {
		response.Results = append(response.Results, transcriptSearchResultJSON{
			CallID:       result.CallID,
			SpeakerID:    result.SpeakerID,
			SegmentIndex: result.SegmentIndex,
			StartMS:      result.StartMS,
			EndMS:        result.EndMS,
			Snippet:      result.Snippet,
		})
	}
	return writeJSONValue(a.out, response)
}

func (a *app) searchCalls(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("search calls", flag.ContinueOnError)
	fs.SetOutput(a.err)
	dbPath := fs.String("db", "", "SQLite database path")
	crmObjectType := fs.String("crm-object-type", "", "CRM object type filter")
	crmObjectID := fs.String("crm-object-id", "", "CRM object ID filter")
	limit := fs.Int("limit", 20, "maximum results to return")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}

	store, err := openSQLiteReadOnlyStore(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	results, err := store.SearchCallsRaw(ctx, sqlite.CallSearchParams{
		CRMObjectType: *crmObjectType,
		CRMObjectID:   *crmObjectID,
		Limit:         *limit,
	})
	if err != nil {
		return err
	}

	if results == nil {
		results = []json.RawMessage{}
	}

	return writeJSONValue(a.out, callSearchResponse{
		CRMObjectType: *crmObjectType,
		CRMObjectID:   *crmObjectID,
		Limit:         normalizeSearchLimit(*limit, 20),
		Results:       results,
	})
}

type transcriptSearchResponse struct {
	Query   string                       `json:"query"`
	Limit   int                          `json:"limit"`
	Results []transcriptSearchResultJSON `json:"results"`
}

type transcriptSearchResultJSON struct {
	CallID       string `json:"call_id"`
	SpeakerID    string `json:"speaker_id"`
	SegmentIndex int    `json:"segment_index"`
	StartMS      int64  `json:"start_ms"`
	EndMS        int64  `json:"end_ms"`
	Snippet      string `json:"snippet"`
}

type callSearchResponse struct {
	CRMObjectType string            `json:"crm_object_type,omitempty"`
	CRMObjectID   string            `json:"crm_object_id,omitempty"`
	Limit         int               `json:"limit"`
	Results       []json.RawMessage `json:"results"`
}

func normalizeSearchLimit(value int, defaultValue int) int {
	if value > 0 {
		return value
	}
	return defaultValue
}
