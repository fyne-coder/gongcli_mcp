package transcripts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/arthurlee/gongctl/internal/gong"
	"github.com/arthurlee/gongctl/internal/store/sqlite"
)

const (
	defaultMissingLimit = 100
	defaultSyncKey      = "transcripts:missing"
	maxSnippetChars     = 240
)

type SyncMissingParams struct {
	Limit   int
	OutDir  string
	SyncKey string
}

type SyncMissingResult struct {
	RunID             int64
	CallsSeen         int
	TranscriptsSynced int
	FilesWritten      int
}

type SearchResult struct {
	CallID       string `json:"call_id"`
	SpeakerID    string `json:"speaker_id,omitempty"`
	SegmentIndex int    `json:"segment_index"`
	StartMS      int64  `json:"start_ms"`
	EndMS        int64  `json:"end_ms"`
	Snippet      string `json:"snippet"`
}

func SyncMissingTranscripts(ctx context.Context, client *gong.Client, store *sqlite.Store, params SyncMissingParams) (result *SyncMissingResult, err error) {
	if client == nil {
		return nil, errors.New("gong client is required")
	}
	if store == nil {
		return nil, errors.New("sqlite store is required")
	}

	run, err := store.StartSyncRun(ctx, sqlite.StartSyncRunParams{
		Scope:          "transcripts",
		SyncKey:        defaultString(strings.TrimSpace(params.SyncKey), defaultSyncKey),
		RequestContext: "missing",
	})
	if err != nil {
		return nil, err
	}

	result = &SyncMissingResult{RunID: run.ID}
	defer func() {
		finishParams := sqlite.FinishSyncRunParams{
			Status:         "success",
			RecordsSeen:    int64(result.CallsSeen),
			RecordsWritten: int64(result.TranscriptsSynced),
		}
		if err != nil {
			finishParams.Status = "error"
			finishParams.ErrorText = err.Error()
		}
		if finishErr := store.FinishSyncRun(ctx, run.ID, finishParams); finishErr != nil {
			if err == nil {
				err = finishErr
				return
			}
			err = errors.Join(err, finishErr)
		}
	}()

	missing, err := store.FindCallsMissingTranscripts(ctx, normalizeLimit(params.Limit))
	if err != nil {
		return result, err
	}
	result.CallsSeen = len(missing)

	for _, call := range missing {
		resp, fetchErr := client.GetTranscript(ctx, gong.TranscriptParams{CallIDs: []string{call.CallID}})
		if fetchErr != nil {
			err = fmt.Errorf("fetch transcript %s: %w", call.CallID, fetchErr)
			return result, err
		}

		record, upsertErr := store.UpsertTranscript(ctx, resp.Body)
		if upsertErr != nil {
			err = fmt.Errorf("upsert transcript %s: %w", call.CallID, upsertErr)
			return result, err
		}

		if outDir := strings.TrimSpace(params.OutDir); outDir != "" {
			path := filepath.Join(outDir, safeFilename(call.CallID)+".json")
			if writeErr := writeJSONFileAtomic(path, record.RawJSON); writeErr != nil {
				err = fmt.Errorf("write transcript %s: %w", call.CallID, writeErr)
				return result, err
			}
			result.FilesWritten++
		}

		result.TranscriptsSynced++
	}

	return result, nil
}

func SearchTranscripts(ctx context.Context, store *sqlite.Store, query string, limit int) ([]SearchResult, error) {
	if store == nil {
		return nil, errors.New("sqlite store is required")
	}

	rows, err := store.SearchTranscriptSegments(ctx, query, limit)
	if err != nil {
		return nil, err
	}

	results := make([]SearchResult, 0, len(rows))
	for _, row := range rows {
		results = append(results, SearchResult{
			CallID:       row.CallID,
			SpeakerID:    row.SpeakerID,
			SegmentIndex: row.SegmentIndex,
			StartMS:      row.StartMS,
			EndMS:        row.EndMS,
			Snippet:      boundedSnippet(row.Snippet, row.Text),
		})
	}
	return results, nil
}

func normalizeLimit(limit int) int {
	if limit <= 0 {
		return defaultMissingLimit
	}
	return limit
}

func defaultString(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func boundedSnippet(snippet string, fallback string) string {
	value := strings.TrimSpace(snippet)
	if value == "" {
		value = strings.TrimSpace(fallback)
	}
	value = strings.Join(strings.Fields(value), " ")
	if value == "" {
		return value
	}

	if utf8.RuneCountInString(value) <= maxSnippetChars {
		return value
	}

	runes := []rune(value)
	if maxSnippetChars <= 3 {
		return string(runes[:maxSnippetChars])
	}
	return string(runes[:maxSnippetChars-3]) + "..."
}

func safeFilename(value string) string {
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_")
	return replacer.Replace(value)
}

func writeJSONFileAtomic(path string, body []byte) error {
	if !json.Valid(body) {
		return fmt.Errorf("%s is not valid JSON", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	temp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tempPath)
		}
	}()

	if _, err := temp.Write(body); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}

	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}

	cleanup = false
	return nil
}
