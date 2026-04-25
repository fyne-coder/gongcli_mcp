package transcripts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/arthurlee/gongctl/internal/gong"
	"github.com/arthurlee/gongctl/internal/store/sqlite"
)

const (
	scopeTranscripts  = "transcripts"
	transcriptSyncKey = "transcripts:missing"
	defaultSyncLimit  = 100
)

type SyncResult struct {
	RunID      int64
	Considered int
	Downloaded int
	Stored     int
	Failed     int
}

func SyncMissing(ctx context.Context, client *gong.Client, store *sqlite.Store, outDir string, limit int) (result SyncResult, err error) {
	if client == nil {
		return result, errors.New("gong client is required")
	}
	if store == nil {
		return result, errors.New("sqlite store is required")
	}
	outDir = strings.TrimSpace(outDir)
	if outDir == "" {
		return result, errors.New("output directory is required")
	}
	if limit <= 0 {
		limit = defaultSyncLimit
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return result, err
	}

	run, err := store.StartSyncRun(ctx, sqlite.StartSyncRunParams{
		Scope:          scopeTranscripts,
		SyncKey:        transcriptSyncKey,
		RequestContext: fmt.Sprintf("limit=%d", limit),
	})
	if err != nil {
		return result, err
	}
	result.RunID = run.ID

	defer func() {
		status := "success"
		errorText := ""
		if err != nil {
			status = "error"
			errorText = err.Error()
		}
		finishErr := store.FinishSyncRun(ctx, run.ID, sqlite.FinishSyncRunParams{
			Status:         status,
			RecordsSeen:    int64(result.Considered),
			RecordsWritten: int64(result.Stored),
			ErrorText:      errorText,
		})
		if finishErr != nil {
			if err != nil {
				err = fmt.Errorf("%w; finish sync run: %v", err, finishErr)
				return
			}
			err = finishErr
		}
	}()

	missing, err := store.FindCallsMissingTranscripts(ctx, limit)
	if err != nil {
		return result, err
	}
	result.Considered = len(missing)

	for _, call := range missing {
		resp, syncErr := client.GetTranscript(ctx, gong.TranscriptParams{CallIDs: []string{call.CallID}})
		if syncErr != nil {
			result.Failed++
			continue
		}
		result.Downloaded++

		if _, syncErr := store.UpsertTranscript(ctx, json.RawMessage(resp.Body)); syncErr != nil {
			result.Failed++
			continue
		}
		path := filepath.Join(outDir, syncSafeFilename(call.CallID)+".json")
		if syncErr := syncWriteJSONFileAtomic(path, resp.Body); syncErr != nil {
			result.Failed++
			continue
		}
		result.Stored++
	}

	if result.Failed > 0 {
		return result, fmt.Errorf("transcript sync completed with %d failures", result.Failed)
	}
	return result, nil
}

func syncWriteJSONFileAtomic(path string, body []byte) error {
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
	if !json.Valid(body) {
		_ = temp.Close()
		return fmt.Errorf("%s is not valid JSON", path)
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

	cleanup = false
	return nil
}

func syncSafeFilename(value string) string {
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_")
	return replacer.Replace(value)
}
