package transcripts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fyne-coder/gongcli_mcp/internal/gong"
	"github.com/fyne-coder/gongcli_mcp/internal/governance"
	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
	"github.com/fyne-coder/gongcli_mcp/internal/store/storeiface"
)

const (
	scopeTranscripts  = "transcripts"
	transcriptSyncKey = "transcripts:missing"
	defaultSyncLimit  = 100
	defaultBatchSize  = 100
	maxBatchSize      = 100
)

type SyncResult struct {
	RunID      int64
	Considered int
	Downloaded int
	Stored     int
	Failed     int
	Requests   int
	BatchSize  int
	Skipped    int
}

func SyncMissing(ctx context.Context, client *gong.Client, store storeiface.TranscriptStore, outDir string, limit int) (result SyncResult, err error) {
	return SyncMissingWithBatch(ctx, client, store, outDir, limit, defaultBatchSize)
}

func SyncMissingWithBatch(ctx context.Context, client *gong.Client, store storeiface.TranscriptStore, outDir string, limit int, batchSize int) (result SyncResult, err error) {
	return syncMissingWithBatch(ctx, client, store, outDir, limit, batchSize, nil)
}

func SyncMissingWithBatchGoverned(ctx context.Context, client *gong.Client, store storeiface.TranscriptStore, outDir string, limit int, batchSize int, cfg *governance.Config) (result SyncResult, err error) {
	return syncMissingWithBatch(ctx, client, store, outDir, limit, batchSize, cfg)
}

func syncMissingWithBatch(ctx context.Context, client *gong.Client, store storeiface.TranscriptStore, outDir string, limit int, batchSize int, cfg *governance.Config) (result SyncResult, err error) {
	if client == nil {
		return result, errors.New("gong client is required")
	}
	if store == nil {
		return result, errors.New("store is required")
	}
	outDir = strings.TrimSpace(outDir)
	if outDir == "" {
		return result, errors.New("output directory is required")
	}
	if limit <= 0 {
		limit = defaultSyncLimit
	}
	batchSize = normalizeBatchSize(batchSize)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return result, err
	}
	result.BatchSize = batchSize

	run, err := store.StartSyncRun(ctx, sqlite.StartSyncRunParams{
		Scope:          scopeTranscripts,
		SyncKey:        transcriptSyncKey,
		RequestContext: transcriptRequestContext(limit, batchSize, cfg),
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
	if cfg != nil {
		missing, result.Skipped, err = filterGovernedTranscriptCandidates(ctx, store, missing, cfg, run.ID)
		if err != nil {
			return result, err
		}
	}

	for _, batch := range transcriptBatches(missing, batchSize) {
		callIDs := make([]string, 0, len(batch))
		for _, call := range batch {
			callIDs = append(callIDs, call.CallID)
		}

		resp, syncErr := client.GetTranscript(ctx, gong.TranscriptParams{CallIDs: callIDs})
		result.Requests++
		if syncErr != nil {
			result.Failed += len(batch)
			continue
		}

		items, syncErr := splitTranscriptResponse(resp.Body)
		if syncErr != nil {
			result.Failed += len(batch)
			continue
		}
		result.Downloaded += len(items)
		if len(items) < len(batch) {
			result.Failed += len(batch) - len(items)
		}

		for _, item := range items {
			if _, syncErr := store.UpsertTranscript(ctx, json.RawMessage(item.Body)); syncErr != nil {
				result.Failed++
				continue
			}
			path := filepath.Join(outDir, syncSafeFilename(item.CallID)+".json")
			if syncErr := syncWriteJSONFileAtomic(path, item.Body); syncErr != nil {
				result.Failed++
				continue
			}
			result.Stored++
		}
	}

	if result.Failed > 0 {
		return result, fmt.Errorf("transcript sync completed with %d failures", result.Failed)
	}
	return result, nil
}

func transcriptRequestContext(limit int, batchSize int, cfg *governance.Config) string {
	context := fmt.Sprintf("limit=%d,batch_size=%d", limit, batchSize)
	if cfg != nil {
		context += ",governance_config_sha256=" + cfg.Fingerprint()
	}
	return context
}

func filterGovernedTranscriptCandidates(ctx context.Context, store storeiface.TranscriptStore, calls []sqlite.MissingTranscriptCall, cfg *governance.Config, runID int64) ([]sqlite.MissingTranscriptCall, int, error) {
	filtered := make([]sqlite.MissingTranscriptCall, 0, len(calls))
	skipped := 0
	for _, call := range calls {
		raw, err := store.CallRawJSON(ctx, call.CallID)
		if err != nil {
			return nil, skipped, fmt.Errorf("load call payload for transcript governance %s: %w", call.CallID, err)
		}
		decision, err := governance.EvaluateCallPayload(raw, cfg)
		if err != nil {
			return nil, skipped, err
		}
		if !decision.Skip {
			filtered = append(filtered, call)
			continue
		}
		if err := store.RecordGovernanceIngestSkippedCallAndDeleteCachedCall(ctx, sqlite.GovernanceIngestSkippedCallParams{
			CallID:         decision.CallID,
			ConfigSHA256:   cfg.Fingerprint(),
			MatchedList:    strings.Join(decision.MatchedLists, ","),
			SourceCategory: decision.SourceCategory,
			RunID:          runID,
		}); err != nil {
			return nil, skipped, err
		}
		skipped++
	}
	return filtered, skipped, nil
}

func normalizeBatchSize(batchSize int) int {
	if batchSize <= 0 {
		return defaultBatchSize
	}
	if batchSize > maxBatchSize {
		return maxBatchSize
	}
	return batchSize
}

type transcriptResponseItem struct {
	CallID string
	Body   []byte
}

func splitTranscriptResponse(body []byte) ([]transcriptResponseItem, error) {
	if !json.Valid(body) {
		return nil, errors.New("transcript response is not valid JSON")
	}

	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, err
	}

	wrapped, ok := envelope["callTranscripts"]
	if !ok {
		callID, err := transcriptCallID(body)
		if err != nil {
			return nil, err
		}
		return []transcriptResponseItem{{CallID: callID, Body: body}}, nil
	}

	var rawItems []json.RawMessage
	if err := json.Unmarshal(wrapped, &rawItems); err != nil {
		return nil, err
	}

	items := make([]transcriptResponseItem, 0, len(rawItems))
	for _, raw := range rawItems {
		normalized, err := normalizeTranscriptItem(raw)
		if err != nil {
			return nil, err
		}
		callID, err := transcriptCallID(normalized)
		if err != nil {
			return nil, err
		}
		items = append(items, transcriptResponseItem{CallID: callID, Body: normalized})
	}
	if len(items) == 0 {
		return nil, errors.New("transcript response contained no call transcripts")
	}
	return items, nil
}

func normalizeTranscriptItem(raw json.RawMessage) ([]byte, error) {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

func transcriptCallID(body []byte) (string, error) {
	var item map[string]any
	if err := json.Unmarshal(body, &item); err != nil {
		return "", err
	}
	for _, key := range []string{"callId", "id"} {
		if value, ok := item[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value), nil
		}
	}
	return "", errors.New("transcript payload missing callId")
}

func transcriptBatches(calls []sqlite.MissingTranscriptCall, batchSize int) [][]sqlite.MissingTranscriptCall {
	if batchSize <= 0 {
		batchSize = defaultBatchSize
	}
	if batchSize > maxBatchSize {
		batchSize = maxBatchSize
	}
	out := make([][]sqlite.MissingTranscriptCall, 0, (len(calls)+batchSize-1)/batchSize)
	for start := 0; start < len(calls); start += batchSize {
		end := start + batchSize
		if end > len(calls) {
			end = len(calls)
		}
		out = append(out, calls[start:end])
	}
	return out
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
