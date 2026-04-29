package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	profilepkg "github.com/fyne-coder/gongcli_mcp/internal/profile"
	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
)

func TestToolsListOnlyExposesExpectedReadOnlyTools(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	server := NewServer(store, "gongmcp", "test")
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "init",
		Method:  "initialize",
		Params:  mustJSON(t, map[string]any{}),
	})+requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "tools",
		Method:  "tools/list",
	}))

	if len(responses) != 2 {
		t.Fatalf("response count=%d want 2", len(responses))
	}

	var listed struct {
		Result toolsListResult `json:"result"`
	}
	if err := json.Unmarshal(responses[1], &listed); err != nil {
		t.Fatalf("unmarshal tools/list response: %v", err)
	}

	names := make([]string, 0, len(listed.Result.Tools))
	for _, tool := range listed.Result.Tools {
		names = append(names, tool.Name)
	}

	want := expectedToolNames()
	if len(names) != len(want) {
		t.Fatalf("tool count=%d want %d (%v)", len(names), len(want), names)
	}
	for idx, name := range want {
		if names[idx] != name {
			t.Fatalf("tool[%d]=%q want %q", idx, names[idx], name)
		}
	}
	for _, blocked := range []string{"api_raw", "raw_api", "sql_query"} {
		for _, name := range names {
			if name == blocked {
				t.Fatalf("unexpected tool %q exposed", blocked)
			}
		}
	}
}

func TestToolsListOnlyReturnsAllowlistedTools(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	server := NewServerWithOptions(store, "gongmcp", "test", WithToolAllowlist([]string{
		"get_sync_status",
		"list_scorecards",
		"search_transcript_segments",
	}))
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "init",
		Method:  "initialize",
		Params:  mustJSON(t, map[string]any{}),
	})+requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "tools",
		Method:  "tools/list",
	}))

	var listed struct {
		Result toolsListResult `json:"result"`
	}
	if err := json.Unmarshal(responses[1], &listed); err != nil {
		t.Fatalf("unmarshal tools/list response: %v", err)
	}

	got := make([]string, 0, len(listed.Result.Tools))
	for _, item := range listed.Result.Tools {
		got = append(got, item.Name)
	}
	want := []string{"get_sync_status", "list_scorecards", "search_transcript_segments"}
	if len(got) != len(want) {
		t.Fatalf("tool count=%d want %d (%v)", len(got), len(want), got)
	}
	for idx, name := range want {
		if got[idx] != name {
			t.Fatalf("tool[%d]=%q want %q", idx, got[idx], name)
		}
	}
}

func TestInitializeAdvertisesClaudeDesktopProtocolVersion(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	server := NewServer(store, "gongmcp", "test")
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      0,
		Method:  "initialize",
		Params: mustJSON(t, map[string]any{
			"protocolVersion": "2025-11-25",
			"capabilities": map[string]any{
				"extensions": map[string]any{
					"io.modelcontextprotocol/ui": map[string]any{
						"mimeTypes": []string{"text/html;profile=mcp-app"},
					},
				},
			},
			"clientInfo": map[string]any{
				"name":    "claude-ai",
				"version": "0.1.0",
			},
		}),
	}))

	var envelope struct {
		Result initializeResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &envelope); err != nil {
		t.Fatalf("unmarshal initialize response: %v", err)
	}
	if envelope.Result.ProtocolVersion != "2025-11-25" {
		t.Fatalf("protocolVersion=%q want 2025-11-25", envelope.Result.ProtocolVersion)
	}
}

func TestServerAcceptsJSONLineTransport(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	payload := mustJSON(t, Request{
		JSONRPC: "2.0",
		ID:      0,
		Method:  "initialize",
		Params: mustJSON(t, map[string]any{
			"protocolVersion": "2025-11-25",
			"capabilities": map[string]any{
				"roots":       map[string]any{},
				"elicitation": map[string]any{},
			},
			"clientInfo": map[string]any{
				"name":    "claude-code",
				"version": "2.1.108",
			},
		}),
	})

	server := NewServer(store, "gongmcp", "test")
	var output bytes.Buffer
	if err := server.Serve(context.Background(), bytes.NewReader(append(payload, '\n')), &output); err != nil {
		t.Fatalf("serve JSON line transport: %v", err)
	}

	var envelope struct {
		Result initializeResult `json:"result"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &envelope); err != nil {
		t.Fatalf("unmarshal JSON line response %q: %v", output.String(), err)
	}
	if envelope.Result.ProtocolVersion != "2025-11-25" {
		t.Fatalf("protocolVersion=%q want 2025-11-25", envelope.Result.ProtocolVersion)
	}
}

func TestReadFrameRejectsOversizedJSONLine(t *testing.T) {
	t.Parallel()

	input := "{" + strings.Repeat("x", maxFrameBytes) + "\n"
	_, err := readFrame(bufio.NewReader(strings.NewReader(input)))
	if err == nil {
		t.Fatal("readFrame returned nil error for oversized JSON-line frame")
	}
	if !strings.Contains(err.Error(), "exceeds maximum") {
		t.Fatalf("error=%q want exceeds maximum", err)
	}
}

func TestUnknownToolReturnsToolError(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	server := NewServer(store, "gongmcp", "test")
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "blocked",
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name":      "api_raw",
			"arguments": map[string]any{},
		}),
	}))

	var envelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &envelope); err != nil {
		t.Fatalf("unmarshal tools/call response: %v", err)
	}
	if !envelope.Result.IsError {
		t.Fatalf("expected tool error result, got %+v", envelope.Result)
	}
	if len(envelope.Result.Content) != 1 {
		t.Fatalf("content count=%d want 1", len(envelope.Result.Content))
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(envelope.Result.Content[0].Text), &payload); err != nil {
		t.Fatalf("unmarshal error payload: %v", err)
	}
	if payload["error"] == "" {
		t.Fatalf("expected error message in tool response")
	}
}

func TestAllowlistedServerRejectsNonAllowedToolCallsWithGenericError(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	server := NewServerWithOptions(store, "gongmcp", "test", WithToolAllowlist([]string{"get_sync_status"}))
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "blocked",
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name": "search_calls",
			"arguments": map[string]any{
				"limit": 1,
			},
		}),
	}))

	var envelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &envelope); err != nil {
		t.Fatalf("unmarshal tools/call response: %v", err)
	}
	if !envelope.Result.IsError {
		t.Fatalf("expected tool error result, got %+v", envelope.Result)
	}

	var payload map[string]string
	if err := json.Unmarshal([]byte(envelope.Result.Content[0].Text), &payload); err != nil {
		t.Fatalf("unmarshal error payload: %v", err)
	}
	if got := payload["error"]; got != "tool is not available" {
		t.Fatalf("error=%q want generic hidden-tool message", got)
	}
}

func TestSearchTranscriptSegmentsReturnsSnippetsWithoutTextField(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	server := NewServer(store, "gongmcp", "test")
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "snippets",
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name": "search_transcript_segments",
			"arguments": map[string]any{
				"query": "external",
				"limit": 5,
			},
		}),
	}))

	var envelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &envelope); err != nil {
		t.Fatalf("unmarshal tools/call response: %v", err)
	}
	if envelope.Result.IsError {
		t.Fatalf("unexpected tool error: %+v", envelope.Result)
	}
	if len(envelope.Result.Content) != 1 {
		t.Fatalf("content count=%d want 1", len(envelope.Result.Content))
	}

	var rows []map[string]any
	if err := json.Unmarshal([]byte(envelope.Result.Content[0].Text), &rows); err != nil {
		t.Fatalf("unmarshal snippet payload: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("snippet count=%d want 1", len(rows))
	}
	if _, ok := rows[0]["snippet"]; !ok {
		t.Fatalf("snippet field missing in %v", rows[0])
	}
	if got := rows[0]["call_id"]; got != "" {
		t.Fatalf("call_id default=%v want redacted empty string", got)
	}
	if got := rows[0]["speaker_id"]; got != "" {
		t.Fatalf("speaker_id default=%v want redacted empty string", got)
	}
	if _, ok := rows[0]["text"]; ok {
		t.Fatalf("unexpected raw text field in %v", rows[0])
	}
}

func TestSearchTranscriptSegmentsCanOptIntoIdentifiers(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	server := NewServer(store, "gongmcp", "test")
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "snippets-with-ids",
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name": "search_transcript_segments",
			"arguments": map[string]any{
				"query":               "external",
				"limit":               5,
				"include_call_ids":    true,
				"include_speaker_ids": true,
			},
		}),
	}))

	var envelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &envelope); err != nil {
		t.Fatalf("unmarshal tools/call response: %v", err)
	}
	if envelope.Result.IsError {
		t.Fatalf("unexpected tool error: %+v", envelope.Result)
	}

	var rows []map[string]any
	if err := json.Unmarshal([]byte(envelope.Result.Content[0].Text), &rows); err != nil {
		t.Fatalf("unmarshal snippet payload: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("snippet count=%d want 1", len(rows))
	}
	if got := rows[0]["call_id"]; got == "" {
		t.Fatalf("call_id was not returned after opt-in: %v", rows[0])
	}
	if got := rows[0]["speaker_id"]; got == "" {
		t.Fatalf("speaker_id was not returned after opt-in: %v", rows[0])
	}
}

func TestSearchTranscriptsByCallFactsFiltersAndRedactsIdentifiers(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	ctx := context.Background()

	for _, raw := range []json.RawMessage{
		mustJSON(t, map[string]any{
			"metaData": map[string]any{
				"id":        "call_theme_external",
				"title":     "External theme evidence",
				"started":   "2026-02-10T15:00:00Z",
				"duration":  1800,
				"system":    "Zoom",
				"direction": "Conference",
				"scope":     "External",
			},
		}),
		mustJSON(t, map[string]any{
			"metaData": map[string]any{
				"id":        "call_theme_internal",
				"title":     "Internal theme evidence",
				"started":   "2026-02-11T15:00:00Z",
				"duration":  1800,
				"system":    "Zoom",
				"direction": "Conference",
				"scope":     "Internal",
			},
		}),
	} {
		if _, err := store.UpsertCall(ctx, raw); err != nil {
			t.Fatalf("upsert theme call: %v", err)
		}
	}
	for _, item := range []struct {
		callID    string
		speakerID string
		text      string
	}{
		{callID: "call_theme_external", speakerID: "buyer-external", text: "The implementation timeline is the main objection."},
		{callID: "call_theme_internal", speakerID: "rep-internal", text: "The implementation timeline is the internal concern."},
	} {
		if _, err := store.UpsertTranscript(ctx, mustJSON(t, map[string]any{
			"callTranscripts": []any{
				map[string]any{
					"callId": item.callID,
					"transcript": []any{
						map[string]any{
							"speakerId": item.speakerID,
							"sentences": []any{
								map[string]any{"start": 1000, "end": 2000, "text": item.text},
							},
						},
					},
				},
			},
		})); err != nil {
			t.Fatalf("upsert theme transcript: %v", err)
		}
	}

	server := NewServer(store, "gongmcp", "test")
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "theme-evidence",
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name": "search_transcripts_by_call_facts",
			"arguments": map[string]any{
				"query":     "implementation",
				"from_date": "2026-01-01",
				"to_date":   "2026-03-31",
				"scope":     "External",
				"limit":     5,
			},
		}),
	}))

	var envelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &envelope); err != nil {
		t.Fatalf("unmarshal tools/call response: %v", err)
	}
	if envelope.Result.IsError {
		t.Fatalf("unexpected tool error: %+v", envelope.Result)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(envelope.Result.Content[0].Text), &rows); err != nil {
		t.Fatalf("unmarshal call-facts transcript payload: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("row count=%d want 1: %+v", len(rows), rows)
	}
	if rows[0]["scope"] != "External" || rows[0]["call_date"] != "2026-02-10" {
		t.Fatalf("unexpected filtered row: %+v", rows[0])
	}
	if excerpt, ok := rows[0]["context_excerpt"].(string); !ok || !strings.Contains(excerpt, "main objection") {
		t.Fatalf("missing bounded context excerpt: %+v", rows[0])
	}
	for _, leaked := range []string{"call_id", "title", "speaker_id", "text"} {
		if _, ok := rows[0][leaked]; ok {
			t.Fatalf("call-facts transcript result leaked %s: %+v", leaked, rows[0])
		}
	}
}

func TestSearchTranscriptQuotesWithAttributionRedactsNamesByDefault(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	ctx := context.Background()

	call := mustJSON(t, map[string]any{
		"id":      "call_attribution_mcp",
		"title":   "Attribution MCP call",
		"started": "2026-02-12T15:00:00Z",
		"context": []any{
			map[string]any{
				"objectType": "Account",
				"id":         "acct_attribution_mcp",
				"fields": []any{
					map[string]any{"name": "Name", "value": "Named Account"},
					map[string]any{"name": "Industry", "value": "Manufacturing"},
				},
			},
			map[string]any{
				"objectType": "Opportunity",
				"id":         "opp_attribution_mcp",
				"fields": []any{
					map[string]any{"name": "Name", "value": "Named Opportunity"},
					map[string]any{"name": "StageName", "value": "Discovery"},
				},
			},
		},
	})
	if _, err := store.UpsertCall(ctx, call); err != nil {
		t.Fatalf("upsert attribution call: %v", err)
	}
	if _, err := store.UpsertTranscript(ctx, mustJSON(t, map[string]any{
		"callTranscripts": []any{
			map[string]any{
				"callId": "call_attribution_mcp",
				"transcript": []any{
					map[string]any{
						"speakerId": "buyer",
						"sentences": []any{
							map[string]any{"start": 1000, "end": 2000, "text": "Implementation timeline is the problem."},
						},
					},
				},
			},
		},
	})); err != nil {
		t.Fatalf("upsert attribution transcript: %v", err)
	}

	server := NewServer(store, "gongmcp", "test")
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "attribution",
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name": "search_transcript_quotes_with_attribution",
			"arguments": map[string]any{
				"query":             "implementation",
				"from_date":         "2026-01-01",
				"to_date":           "2026-03-31",
				"industry":          "Manufacturing",
				"opportunity_stage": "Discovery",
				"limit":             5,
			},
		}),
	}))

	var envelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &envelope); err != nil {
		t.Fatalf("unmarshal tools/call response: %v", err)
	}
	if envelope.Result.IsError {
		t.Fatalf("unexpected tool error: %+v", envelope.Result)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(envelope.Result.Content[0].Text), &rows); err != nil {
		t.Fatalf("unmarshal attribution payload: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("row count=%d want 1: %+v", len(rows), rows)
	}
	for _, leaked := range []string{"call_id", "title", "account_name", "account_website", "opportunity_name", "opportunity_close_date", "opportunity_probability"} {
		if _, ok := rows[0][leaked]; ok {
			t.Fatalf("attribution result leaked %s by default: %+v", leaked, rows[0])
		}
	}
	if rows[0]["account_industry"] != "Manufacturing" || rows[0]["opportunity_stage"] != "Discovery" {
		t.Fatalf("missing safe attribution metadata: %+v", rows[0])
	}
	if rows[0]["participant_status"] == "" || rows[0]["person_title_status"] == "" {
		t.Fatalf("missing person/title status: %+v", rows[0])
	}
	if text, ok := rows[0]["context_excerpt"].(string); !ok || !strings.Contains(text, "Implementation timeline") {
		t.Fatalf("missing bounded context excerpt: %+v", rows[0])
	}
}

func TestSearchTranscriptQuotesWithAttributionAccountQueryRequiresNameOptIn(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	server := NewServer(store, "gongmcp", "test")
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "attribution-account-query",
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name": "search_transcript_quotes_with_attribution",
			"arguments": map[string]any{
				"query":         "implementation",
				"account_query": "Named Account",
				"limit":         5,
			},
		}),
	}))

	var envelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &envelope); err != nil {
		t.Fatalf("unmarshal tools/call response: %v", err)
	}
	if !envelope.Result.IsError {
		t.Fatalf("expected account_query tool error, got %+v", envelope.Result)
	}
	if !strings.Contains(envelope.Result.Content[0].Text, "include_account_names") {
		t.Fatalf("tool error missing opt-in guidance: %+v", envelope.Result)
	}
}

func TestGetCallReturnsMinimizedDetail(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	server := NewServer(store, "gongmcp", "test")
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "call-detail",
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name": "get_call",
			"arguments": map[string]any{
				"call_id": "call_extended_001",
			},
		}),
	}))

	var envelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &envelope); err != nil {
		t.Fatalf("unmarshal tools/call response: %v", err)
	}
	if envelope.Result.IsError {
		t.Fatalf("unexpected tool error: %+v", envelope.Result)
	}
	text := envelope.Result.Content[0].Text
	for _, leaked := range []string{"internal@example.invalid", "external@example.invalid", "40000", "125000", "Acme Corp", "Expansion Q2"} {
		if strings.Contains(text, leaked) {
			t.Fatalf("minimized call detail leaked %q in %s", leaked, text)
		}
	}

	var detail callDetail
	if err := json.Unmarshal([]byte(text), &detail); err != nil {
		t.Fatalf("unmarshal call detail: %v", err)
	}
	if detail.CallID != "call_extended_001" || detail.DurationSeconds != 2400 || detail.PartiesCount != 2 {
		t.Fatalf("unexpected call detail: %+v", detail)
	}
	if len(detail.CRMObjects) != 2 {
		t.Fatalf("crm object count=%d want 2", len(detail.CRMObjects))
	}
	for _, object := range detail.CRMObjects {
		if object.ObjectName != "" {
			t.Fatalf("object name leaked in minimized call detail: %+v", object)
		}
		if object.FieldCount == 0 || len(object.FieldNames) == 0 {
			t.Fatalf("object missing field metadata: %+v", object)
		}
	}
}

func TestSearchCallsSummarizesMetaDataEnvelope(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()

	raw := mustJSON(t, map[string]any{
		"metaData": map[string]any{
			"id":       "call_metadata_mcp_001",
			"title":    "Metadata MCP call",
			"started":  "2026-04-24T15:00:00Z",
			"duration": 900,
		},
		"context": []any{
			map[string]any{
				"objectType": "Opportunity",
				"id":         "opp_metadata_mcp_001",
				"name":       "Metadata opp",
				"fields": []any{
					map[string]any{"name": "StageName", "value": "Contract Signing"},
				},
			},
		},
	})
	if _, err := store.UpsertCall(context.Background(), raw); err != nil {
		t.Fatalf("upsert metadata call: %v", err)
	}

	server := NewServer(store, "gongmcp", "test")
	responses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "search",
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name": "search_calls",
			"arguments": map[string]any{
				"crm_object_type": "Opportunity",
				"crm_object_id":   "opp_metadata_mcp_001",
				"limit":           5,
			},
		}),
	}))

	var envelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &envelope); err != nil {
		t.Fatalf("unmarshal tools/call response: %v", err)
	}
	var rows []searchCallSummary
	if err := json.Unmarshal([]byte(envelope.Result.Content[0].Text), &rows); err != nil {
		t.Fatalf("unmarshal search rows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("row count=%d want 1", len(rows))
	}
	if rows[0].CallID != "call_metadata_mcp_001" || rows[0].Title != "Metadata MCP call" || rows[0].DurationSeconds != 900 {
		t.Fatalf("unexpected summary: %+v", rows[0])
	}
}

func TestCRMToolsReturnAggregates(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	seedLateStageMCPCall(t, store)

	server := NewServer(store, "gongmcp", "test")
	responses := runServer(t, server,
		requestFrame(Request{
			JSONRPC: "2.0",
			ID:      "objects",
			Method:  "tools/call",
			Params: mustJSON(t, map[string]any{
				"name":      "list_crm_object_types",
				"arguments": map[string]any{},
			}),
		})+
			requestFrame(Request{
				JSONRPC: "2.0",
				ID:      "fields",
				Method:  "tools/call",
				Params: mustJSON(t, map[string]any{
					"name": "list_crm_fields",
					"arguments": map[string]any{
						"object_type": "Opportunity",
						"limit":       5,
					},
				}),
			})+
			requestFrame(Request{
				JSONRPC: "2.0",
				ID:      "values_redacted",
				Method:  "tools/call",
				Params: mustJSON(t, map[string]any{
					"name": "search_crm_field_values",
					"arguments": map[string]any{
						"object_type": "Opportunity",
						"field_name":  "amount",
						"value_query": "40000",
						"limit":       5,
					},
				}),
			})+
			requestFrame(Request{
				JSONRPC: "2.0",
				ID:      "values",
				Method:  "tools/call",
				Params: mustJSON(t, map[string]any{
					"name": "search_crm_field_values",
					"arguments": map[string]any{
						"object_type":            "Opportunity",
						"field_name":             "amount",
						"value_query":            "40000",
						"include_value_snippets": true,
						"limit":                  5,
					},
				}),
			})+
			requestFrame(Request{
				JSONRPC: "2.0",
				ID:      "signals",
				Method:  "tools/call",
				Params: mustJSON(t, map[string]any{
					"name": "analyze_late_stage_crm_signals",
					"arguments": map[string]any{
						"object_type":       "Opportunity",
						"late_stage_values": []string{"Contract Signing"},
						"limit":             5,
					},
				}),
			}))

	if len(responses) != 5 {
		t.Fatalf("response count=%d want 5", len(responses))
	}
	var objectsEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &objectsEnvelope); err != nil {
		t.Fatalf("unmarshal objects response: %v", err)
	}
	var objects []sqlite.CRMObjectTypeSummary
	if err := json.Unmarshal([]byte(objectsEnvelope.Result.Content[0].Text), &objects); err != nil {
		t.Fatalf("unmarshal object summaries: %v", err)
	}
	if len(objects) == 0 {
		t.Fatal("object summaries empty")
	}

	var fieldsEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[1], &fieldsEnvelope); err != nil {
		t.Fatalf("unmarshal fields response: %v", err)
	}
	var fields []sqlite.CRMFieldSummary
	if err := json.Unmarshal([]byte(fieldsEnvelope.Result.Content[0].Text), &fields); err != nil {
		t.Fatalf("unmarshal field summaries: %v", err)
	}
	if len(fields) == 0 {
		t.Fatal("field summaries empty")
	}
	for _, field := range fields {
		if len(field.ExampleValues) != 0 {
			t.Fatalf("list_crm_fields leaked example values: %+v", fields)
		}
	}

	var redactedEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[2], &redactedEnvelope); err != nil {
		t.Fatalf("unmarshal redacted values response: %v", err)
	}
	var redactedValues []sqlite.CRMFieldValueMatch
	if err := json.Unmarshal([]byte(redactedEnvelope.Result.Content[0].Text), &redactedValues); err != nil {
		t.Fatalf("unmarshal redacted value matches: %v", err)
	}
	if len(redactedValues) != 1 || redactedValues[0].CallID != "" || redactedValues[0].ValueSnippet != "" || redactedValues[0].ObjectID != "" || redactedValues[0].ObjectName != "" || redactedValues[0].Title != "" {
		t.Fatalf("default value search leaked value/object details: %+v", redactedValues)
	}

	var valuesEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[3], &valuesEnvelope); err != nil {
		t.Fatalf("unmarshal values response: %v", err)
	}
	var values []sqlite.CRMFieldValueMatch
	if err := json.Unmarshal([]byte(valuesEnvelope.Result.Content[0].Text), &values); err != nil {
		t.Fatalf("unmarshal value matches: %v", err)
	}
	if len(values) != 1 || values[0].ValueSnippet != "40000" || values[0].Title == "" || values[0].CallID != "" {
		t.Fatalf("unexpected value matches: %+v", values)
	}
	if values[0].ObjectID != "" || values[0].ObjectName != "" {
		t.Fatalf("value search leaked object details: %+v", values[0])
	}

	withIDsResponses := runServer(t, server, requestFrame(Request{
		JSONRPC: "2.0",
		ID:      "values_with_ids",
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name": "search_crm_field_values",
			"arguments": map[string]any{
				"object_type":            "Opportunity",
				"field_name":             "amount",
				"value_query":            "40000",
				"include_value_snippets": true,
				"include_call_ids":       true,
				"limit":                  5,
			},
		}),
	}))

	var withIDsEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(withIDsResponses[0], &withIDsEnvelope); err != nil {
		t.Fatalf("unmarshal values_with_ids response: %v", err)
	}
	var withIDs []sqlite.CRMFieldValueMatch
	if err := json.Unmarshal([]byte(withIDsEnvelope.Result.Content[0].Text), &withIDs); err != nil {
		t.Fatalf("unmarshal value matches with ids: %v", err)
	}
	if len(withIDs) != 1 || withIDs[0].CallID == "" || withIDs[0].ValueSnippet == "" || withIDs[0].Title == "" {
		t.Fatalf("expected opt-in call identifiers and snippets: %+v", withIDs)
	}
	if withIDs[0].ObjectID != "" || withIDs[0].ObjectName != "" {
		t.Fatalf("value search leaked object details with ids: %+v", withIDs[0])
	}

	var signalsEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[4], &signalsEnvelope); err != nil {
		t.Fatalf("unmarshal signals response: %v", err)
	}
	var report sqlite.LateStageSignalsReport
	if err := json.Unmarshal([]byte(signalsEnvelope.Result.Content[0].Text), &report); err != nil {
		t.Fatalf("unmarshal signal report: %v", err)
	}
	if report.ObjectType != "Opportunity" || report.LateCalls == 0 || len(report.Signals) == 0 {
		t.Fatalf("unexpected signal report: %+v", report)
	}
}

func expectedToolNames() []string {
	return []string{
		"get_sync_status",
		"search_calls",
		"get_call",
		"list_crm_object_types",
		"list_crm_fields",
		"list_crm_integrations",
		"list_cached_crm_schema_objects",
		"list_cached_crm_schema_fields",
		"list_gong_settings",
		"list_scorecards",
		"get_scorecard",
		"get_business_profile",
		"list_business_concepts",
		"list_unmapped_crm_fields",
		"search_crm_field_values",
		"analyze_late_stage_crm_signals",
		"opportunities_missing_transcripts",
		"search_transcripts_by_crm_context",
		"opportunity_call_summary",
		"crm_field_population_matrix",
		"list_lifecycle_buckets",
		"summarize_calls_by_lifecycle",
		"search_calls_by_lifecycle",
		"prioritize_transcripts_by_lifecycle",
		"compare_lifecycle_crm_fields",
		"summarize_call_facts",
		"rank_transcript_backlog",
		"search_transcript_segments",
		"search_transcripts_by_call_facts",
		"search_transcript_quotes_with_attribution",
		"missing_transcripts",
	}
}

func TestInventoryToolsReturnCachedMetadata(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	seedInventoryMCPFixtures(t, store)

	server := NewServer(store, "gongmcp", "test")
	responses := runServer(t, server,
		requestFrame(Request{
			JSONRPC: "2.0",
			ID:      "integrations",
			Method:  "tools/call",
			Params: mustJSON(t, map[string]any{
				"name":      "list_crm_integrations",
				"arguments": map[string]any{},
			}),
		})+
			requestFrame(Request{
				JSONRPC: "2.0",
				ID:      "schema-objects",
				Method:  "tools/call",
				Params: mustJSON(t, map[string]any{
					"name": "list_cached_crm_schema_objects",
					"arguments": map[string]any{
						"integration_id": "crm-int-001",
					},
				}),
			})+
			requestFrame(Request{
				JSONRPC: "2.0",
				ID:      "schema-fields",
				Method:  "tools/call",
				Params: mustJSON(t, map[string]any{
					"name": "list_cached_crm_schema_fields",
					"arguments": map[string]any{
						"integration_id": "crm-int-001",
						"object_type":    "DEAL",
						"limit":          10,
					},
				}),
			})+
			requestFrame(Request{
				JSONRPC: "2.0",
				ID:      "settings",
				Method:  "tools/call",
				Params: mustJSON(t, map[string]any{
					"name": "list_gong_settings",
					"arguments": map[string]any{
						"kind":  "trackers",
						"limit": 10,
					},
				}),
			})+
			requestFrame(Request{
				JSONRPC: "2.0",
				ID:      "scorecards",
				Method:  "tools/call",
				Params: mustJSON(t, map[string]any{
					"name": "list_scorecards",
					"arguments": map[string]any{
						"limit": 10,
					},
				}),
			})+
			requestFrame(Request{
				JSONRPC: "2.0",
				ID:      "scorecard-detail",
				Method:  "tools/call",
				Params: mustJSON(t, map[string]any{
					"name": "get_scorecard",
					"arguments": map[string]any{
						"scorecard_id": "scorecard-001",
					},
				}),
			}))

	if len(responses) != 6 {
		t.Fatalf("response count=%d want 6", len(responses))
	}

	var integrationsEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &integrationsEnvelope); err != nil {
		t.Fatalf("unmarshal integrations response: %v", err)
	}
	var integrations []sqlite.CRMIntegrationRecord
	if err := json.Unmarshal([]byte(integrationsEnvelope.Result.Content[0].Text), &integrations); err != nil {
		t.Fatalf("unmarshal integrations: %v", err)
	}
	if len(integrations) != 1 || integrations[0].IntegrationID != "crm-int-001" {
		t.Fatalf("unexpected integrations: %+v", integrations)
	}

	var objectsEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[1], &objectsEnvelope); err != nil {
		t.Fatalf("unmarshal schema objects response: %v", err)
	}
	var objects []sqlite.CRMSchemaObjectRecord
	if err := json.Unmarshal([]byte(objectsEnvelope.Result.Content[0].Text), &objects); err != nil {
		t.Fatalf("unmarshal schema objects: %v", err)
	}
	if len(objects) != 1 || objects[0].ObjectType != "DEAL" || objects[0].FieldCount != 2 {
		t.Fatalf("unexpected schema objects: %+v", objects)
	}

	var fieldsEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[2], &fieldsEnvelope); err != nil {
		t.Fatalf("unmarshal schema fields response: %v", err)
	}
	var fields []sqlite.CRMSchemaFieldRecord
	if err := json.Unmarshal([]byte(fieldsEnvelope.Result.Content[0].Text), &fields); err != nil {
		t.Fatalf("unmarshal schema fields: %v", err)
	}
	if len(fields) != 2 || fields[0].FieldName != "Amount" {
		t.Fatalf("unexpected schema fields: %+v", fields)
	}
	if strings.Contains(fieldsEnvelope.Result.Content[0].Text, "raw_json") {
		t.Fatalf("schema field tool exposed raw payload: %s", fieldsEnvelope.Result.Content[0].Text)
	}

	var settingsEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[3], &settingsEnvelope); err != nil {
		t.Fatalf("unmarshal settings response: %v", err)
	}
	var settings []sqlite.GongSettingRecord
	if err := json.Unmarshal([]byte(settingsEnvelope.Result.Content[0].Text), &settings); err != nil {
		t.Fatalf("unmarshal settings: %v", err)
	}
	if len(settings) != 1 || settings[0].ObjectID != "tracker-001" || !settings[0].Active {
		t.Fatalf("unexpected settings: %+v", settings)
	}

	var scorecardsEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[4], &scorecardsEnvelope); err != nil {
		t.Fatalf("unmarshal scorecards response: %v", err)
	}
	var scorecards []sqlite.ScorecardSummary
	if err := json.Unmarshal([]byte(scorecardsEnvelope.Result.Content[0].Text), &scorecards); err != nil {
		t.Fatalf("unmarshal scorecards: %v", err)
	}
	if len(scorecards) != 1 || scorecards[0].Name != "Discovery quality" || scorecards[0].QuestionCount != 1 {
		t.Fatalf("unexpected scorecards: %+v", scorecards)
	}

	var detailEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[5], &detailEnvelope); err != nil {
		t.Fatalf("unmarshal scorecard detail response: %v", err)
	}
	var detail sqlite.ScorecardDetail
	if err := json.Unmarshal([]byte(detailEnvelope.Result.Content[0].Text), &detail); err != nil {
		t.Fatalf("unmarshal scorecard detail: %v", err)
	}
	if len(detail.Questions) != 1 || detail.Questions[0].QuestionText != "Did the rep confirm pain?" {
		t.Fatalf("unexpected scorecard detail: %+v", detail)
	}
}

func TestOpportunityAggregateMCPTools(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	seedOpportunityAggregateMCPFixtures(t, store)

	server := NewServer(store, "gongmcp", "test")
	responses := runServer(t, server,
		requestFrame(Request{
			JSONRPC: "2.0",
			ID:      "missing-opps",
			Method:  "tools/call",
			Params: mustJSON(t, map[string]any{
				"name": "opportunities_missing_transcripts",
				"arguments": map[string]any{
					"stage_values": []string{"Contract Signing"},
					"limit":        5,
				},
			}),
		})+
			requestFrame(Request{
				JSONRPC: "2.0",
				ID:      "crm-transcripts",
				Method:  "tools/call",
				Params: mustJSON(t, map[string]any{
					"name": "search_transcripts_by_crm_context",
					"arguments": map[string]any{
						"query":       "pricing",
						"object_type": "Opportunity",
						"object_id":   "opp_mcp_gap_001",
						"limit":       5,
					},
				}),
			})+
			requestFrame(Request{
				JSONRPC: "2.0",
				ID:      "opp-summary",
				Method:  "tools/call",
				Params: mustJSON(t, map[string]any{
					"name": "opportunity_call_summary",
					"arguments": map[string]any{
						"stage_values": []string{"Contract Signing"},
						"limit":        5,
					},
				}),
			})+
			requestFrame(Request{
				JSONRPC: "2.0",
				ID:      "matrix",
				Method:  "tools/call",
				Params: mustJSON(t, map[string]any{
					"name": "crm_field_population_matrix",
					"arguments": map[string]any{
						"object_type":    "Opportunity",
						"group_by_field": "StageName",
						"limit":          20,
					},
				}),
			}))

	if len(responses) != 4 {
		t.Fatalf("response count=%d want 4", len(responses))
	}

	var missingEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &missingEnvelope); err != nil {
		t.Fatalf("unmarshal missing response: %v", err)
	}
	var missing []sqlite.OpportunityMissingTranscriptSummary
	if err := json.Unmarshal([]byte(missingEnvelope.Result.Content[0].Text), &missing); err != nil {
		t.Fatalf("unmarshal missing opportunities: %v", err)
	}
	if len(missing) != 1 || missing[0].OpportunityID != "" || missing[0].OpportunityName != "" || missing[0].LatestCallID != "" || missing[0].CallCount != 2 || missing[0].MissingTranscriptCount != 1 || missing[0].TranscriptCount != 1 {
		t.Fatalf("unexpected missing opportunity rows: %+v", missing)
	}

	var transcriptEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[1], &transcriptEnvelope); err != nil {
		t.Fatalf("unmarshal transcript response: %v", err)
	}
	var transcriptRows []sqlite.TranscriptCRMSearchResult
	if err := json.Unmarshal([]byte(transcriptEnvelope.Result.Content[0].Text), &transcriptRows); err != nil {
		t.Fatalf("unmarshal transcript CRM rows: %v", err)
	}
	if len(transcriptRows) != 1 || transcriptRows[0].CallID != "" || transcriptRows[0].Title != "" || transcriptRows[0].ObjectID != "" || transcriptRows[0].ObjectName != "" || transcriptRows[0].SpeakerID != "" {
		t.Fatalf("unexpected transcript CRM rows: %+v", transcriptRows)
	}
	if _, ok := mapFromJSONText(t, transcriptEnvelope.Result.Content[0].Text, 0)["text"]; ok {
		t.Fatalf("raw text leaked in transcript CRM result: %s", transcriptEnvelope.Result.Content[0].Text)
	}

	var summaryEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[2], &summaryEnvelope); err != nil {
		t.Fatalf("unmarshal opportunity summary response: %v", err)
	}
	var summaries []sqlite.OpportunityCallSummary
	if err := json.Unmarshal([]byte(summaryEnvelope.Result.Content[0].Text), &summaries); err != nil {
		t.Fatalf("unmarshal opportunity summaries: %v", err)
	}
	if len(summaries) != 1 || summaries[0].OpportunityID != "" || summaries[0].OpportunityName != "" || summaries[0].LatestCallID != "" || summaries[0].Amount != "" || summaries[0].CloseDate != "" || summaries[0].OwnerID != "" || summaries[0].TotalDurationSeconds != 1800 {
		t.Fatalf("unexpected opportunity summaries: %+v", summaries)
	}

	var matrixEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[3], &matrixEnvelope); err != nil {
		t.Fatalf("unmarshal matrix response: %v", err)
	}
	var matrix sqlite.CRMFieldPopulationMatrix
	if err := json.Unmarshal([]byte(matrixEnvelope.Result.Content[0].Text), &matrix); err != nil {
		t.Fatalf("unmarshal matrix: %v", err)
	}
	if matrix.ObjectType != "Opportunity" || len(matrix.Cells) == 0 {
		t.Fatalf("unexpected matrix: %+v", matrix)
	}
}

func TestLifecycleMCPTools(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	seedLifecycleMCPFixtures(t, store)

	server := NewServer(store, "gongmcp", "test")
	responses := runServer(t, server,
		requestFrame(Request{
			JSONRPC: "2.0",
			ID:      "buckets",
			Method:  "tools/call",
			Params: mustJSON(t, map[string]any{
				"name":      "list_lifecycle_buckets",
				"arguments": map[string]any{},
			}),
		})+
			requestFrame(Request{
				JSONRPC: "2.0",
				ID:      "summary",
				Method:  "tools/call",
				Params: mustJSON(t, map[string]any{
					"name": "summarize_calls_by_lifecycle",
					"arguments": map[string]any{
						"bucket": "renewal",
					},
				}),
			})+
			requestFrame(Request{
				JSONRPC: "2.0",
				ID:      "search",
				Method:  "tools/call",
				Params: mustJSON(t, map[string]any{
					"name": "search_calls_by_lifecycle",
					"arguments": map[string]any{
						"bucket":                   "upsell_expansion",
						"missing_transcripts_only": true,
						"limit":                    5,
					},
				}),
			})+
			requestFrame(Request{
				JSONRPC: "2.0",
				ID:      "priority",
				Method:  "tools/call",
				Params: mustJSON(t, map[string]any{
					"name": "prioritize_transcripts_by_lifecycle",
					"arguments": map[string]any{
						"limit": 5,
					},
				}),
			})+
			requestFrame(Request{
				JSONRPC: "2.0",
				ID:      "compare",
				Method:  "tools/call",
				Params: mustJSON(t, map[string]any{
					"name": "compare_lifecycle_crm_fields",
					"arguments": map[string]any{
						"bucket_a":    "renewal",
						"bucket_b":    "active_sales_pipeline",
						"object_type": "Opportunity",
						"limit":       10,
					},
				}),
			})+
			requestFrame(Request{
				JSONRPC: "2.0",
				ID:      "facts",
				Method:  "tools/call",
				Params: mustJSON(t, map[string]any{
					"name": "summarize_call_facts",
					"arguments": map[string]any{
						"group_by": "lifecycle",
						"limit":    10,
					},
				}),
			})+
			requestFrame(Request{
				JSONRPC: "2.0",
				ID:      "backlog",
				Method:  "tools/call",
				Params: mustJSON(t, map[string]any{
					"name": "rank_transcript_backlog",
					"arguments": map[string]any{
						"limit": 5,
					},
				}),
			}))

	if len(responses) != 7 {
		t.Fatalf("response count=%d want 7", len(responses))
	}

	var bucketsEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &bucketsEnvelope); err != nil {
		t.Fatalf("unmarshal buckets response: %v", err)
	}
	var buckets []sqlite.LifecycleBucketDefinition
	if err := json.Unmarshal([]byte(bucketsEnvelope.Result.Content[0].Text), &buckets); err != nil {
		t.Fatalf("unmarshal lifecycle buckets: %v", err)
	}
	if len(buckets) == 0 || buckets[0].Bucket != "outbound_prospecting" {
		t.Fatalf("unexpected lifecycle buckets: %+v", buckets)
	}

	var summaryEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[1], &summaryEnvelope); err != nil {
		t.Fatalf("unmarshal summary response: %v", err)
	}
	var summaries []sqlite.LifecycleBucketSummary
	if err := json.Unmarshal([]byte(summaryEnvelope.Result.Content[0].Text), &summaries); err != nil {
		t.Fatalf("unmarshal lifecycle summaries: %v", err)
	}
	if len(summaries) != 1 || summaries[0].Bucket != "renewal" || summaries[0].CallCount != 1 {
		t.Fatalf("unexpected lifecycle summary: %+v", summaries)
	}

	var searchEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[2], &searchEnvelope); err != nil {
		t.Fatalf("unmarshal search response: %v", err)
	}
	var searchRows []sqlite.LifecycleCallSearchResult
	if err := json.Unmarshal([]byte(searchEnvelope.Result.Content[0].Text), &searchRows); err != nil {
		t.Fatalf("unmarshal lifecycle search rows: %v", err)
	}
	if len(searchRows) != 1 || searchRows[0].CallID != "call_mcp_lifecycle_upsell" || searchRows[0].TranscriptPresent {
		t.Fatalf("unexpected lifecycle search rows: %+v", searchRows)
	}

	var priorityEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[3], &priorityEnvelope); err != nil {
		t.Fatalf("unmarshal priority response: %v", err)
	}
	var priorities []sqlite.LifecycleTranscriptPriority
	if err := json.Unmarshal([]byte(priorityEnvelope.Result.Content[0].Text), &priorities); err != nil {
		t.Fatalf("unmarshal lifecycle priorities: %v", err)
	}
	if len(priorities) == 0 || priorities[0].Bucket == "" || priorities[0].PriorityScore <= 0 {
		t.Fatalf("unexpected lifecycle priorities: %+v", priorities)
	}
	if priorities[0].CallID != "" || priorities[0].Title != "" {
		t.Fatalf("MCP priority rows should redact call IDs/titles by default: %+v", priorities[0])
	}

	var compareEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[4], &compareEnvelope); err != nil {
		t.Fatalf("unmarshal compare response: %v", err)
	}
	var comparison sqlite.LifecycleCRMFieldComparison
	if err := json.Unmarshal([]byte(compareEnvelope.Result.Content[0].Text), &comparison); err != nil {
		t.Fatalf("unmarshal lifecycle comparison: %v", err)
	}
	if comparison.BucketA != "renewal" || comparison.BucketB != "active_sales_pipeline" || len(comparison.Fields) == 0 {
		t.Fatalf("unexpected lifecycle comparison: %+v", comparison)
	}

	var factsEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[5], &factsEnvelope); err != nil {
		t.Fatalf("unmarshal facts response: %v", err)
	}
	var factRows []sqlite.CallFactsSummaryRow
	if err := json.Unmarshal([]byte(factsEnvelope.Result.Content[0].Text), &factRows); err != nil {
		t.Fatalf("unmarshal call facts rows: %v", err)
	}
	if len(factRows) == 0 || factRows[0].GroupBy != "lifecycle" {
		t.Fatalf("unexpected call facts rows: %+v", factRows)
	}

	var backlogEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[6], &backlogEnvelope); err != nil {
		t.Fatalf("unmarshal backlog response: %v", err)
	}
	var backlog []sqlite.LifecycleTranscriptPriority
	if err := json.Unmarshal([]byte(backlogEnvelope.Result.Content[0].Text), &backlog); err != nil {
		t.Fatalf("unmarshal backlog rows: %v", err)
	}
	if len(backlog) == 0 || backlog[0].PriorityScore <= 0 {
		t.Fatalf("unexpected backlog rows: %+v", backlog)
	}
	if backlog[0].CallID != "" || backlog[0].Title != "" {
		t.Fatalf("MCP backlog rows should redact call IDs/titles by default: %+v", backlog[0])
	}
}

func TestBusinessProfileMCPTools(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	seedBusinessProfileMCPFixtures(t, store)

	server := NewServer(store, "gongmcp", "test")
	responses := runServer(t, server,
		requestFrame(Request{
			JSONRPC: "2.0",
			ID:      "profile",
			Method:  "tools/call",
			Params: mustJSON(t, map[string]any{
				"name":      "get_business_profile",
				"arguments": map[string]any{},
			}),
		})+
			requestFrame(Request{
				JSONRPC: "2.0",
				ID:      "concepts",
				Method:  "tools/call",
				Params: mustJSON(t, map[string]any{
					"name":      "list_business_concepts",
					"arguments": map[string]any{},
				}),
			})+
			requestFrame(Request{
				JSONRPC: "2.0",
				ID:      "facts",
				Method:  "tools/call",
				Params: mustJSON(t, map[string]any{
					"name": "summarize_call_facts",
					"arguments": map[string]any{
						"lifecycle_source": "profile",
						"group_by":         "deal_stage",
						"limit":            10,
					},
				}),
			})+
			requestFrame(Request{
				JSONRPC: "2.0",
				ID:      "unmapped",
				Method:  "tools/call",
				Params: mustJSON(t, map[string]any{
					"name": "list_unmapped_crm_fields",
					"arguments": map[string]any{
						"limit": 10,
					},
				}),
			})+
			requestFrame(Request{
				JSONRPC: "2.0",
				ID:      "unsafe_group",
				Method:  "tools/call",
				Params: mustJSON(t, map[string]any{
					"name": "summarize_call_facts",
					"arguments": map[string]any{
						"lifecycle_source": "profile",
						"group_by":         "secret_id",
						"limit":            10,
					},
				}),
			}))

	if len(responses) != 5 {
		t.Fatalf("response count=%d want 5", len(responses))
	}
	var profileEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[0], &profileEnvelope); err != nil {
		t.Fatalf("unmarshal profile response: %v", err)
	}
	var businessProfile sqlite.BusinessProfile
	if err := json.Unmarshal([]byte(profileEnvelope.Result.Content[0].Text), &businessProfile); err != nil {
		t.Fatalf("unmarshal business profile: %v", err)
	}
	if businessProfile.CanonicalSHA256 != "" || businessProfile.SourceSHA256 != "" || businessProfile.SourcePath != "" || businessProfile.ImportedBy != "redacted" {
		t.Fatalf("unexpected business profile: %+v", businessProfile)
	}

	var conceptsEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[1], &conceptsEnvelope); err != nil {
		t.Fatalf("unmarshal concepts response: %v", err)
	}
	var concepts []sqlite.BusinessConcept
	if err := json.Unmarshal([]byte(conceptsEnvelope.Result.Content[0].Text), &concepts); err != nil {
		t.Fatalf("unmarshal concepts: %v", err)
	}
	if len(concepts) == 0 {
		t.Fatal("business concepts empty")
	}

	var factsEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[2], &factsEnvelope); err != nil {
		t.Fatalf("unmarshal facts response: %v", err)
	}
	var factsResponse struct {
		LifecycleSource string                       `json:"lifecycle_source"`
		Profile         *sqlite.BusinessProfile      `json:"profile"`
		Results         []sqlite.CallFactsSummaryRow `json:"results"`
	}
	if err := json.Unmarshal([]byte(factsEnvelope.Result.Content[0].Text), &factsResponse); err != nil {
		t.Fatalf("unmarshal profile facts: %v", err)
	}
	if factsResponse.LifecycleSource != sqlite.LifecycleSourceProfile || factsResponse.Profile == nil {
		t.Fatalf("unexpected facts envelope: %+v", factsResponse)
	}
	foundProposal := false
	for _, row := range factsResponse.Results {
		if row.GroupValue == "Proposal" {
			foundProposal = true
		}
	}
	if !foundProposal {
		t.Fatalf("profile facts missing Proposal group: %+v", factsResponse.Results)
	}

	var unmappedEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[3], &unmappedEnvelope); err != nil {
		t.Fatalf("unmarshal unmapped response: %v", err)
	}
	if strings.Contains(unmappedEnvelope.Result.Content[0].Text, "10000") {
		t.Fatalf("unmapped fields leaked raw value: %s", unmappedEnvelope.Result.Content[0].Text)
	}
	var unmapped []sqlite.UnmappedCRMField
	if err := json.Unmarshal([]byte(unmappedEnvelope.Result.Content[0].Text), &unmapped); err != nil {
		t.Fatalf("unmarshal unmapped fields: %v", err)
	}
	if len(unmapped) == 0 {
		t.Fatal("unmapped fields empty")
	}
	var unsafeEnvelope struct {
		Result toolCallResult `json:"result"`
	}
	if err := json.Unmarshal(responses[4], &unsafeEnvelope); err != nil {
		t.Fatalf("unmarshal unsafe group response: %v", err)
	}
	if !strings.Contains(unsafeEnvelope.Result.Content[0].Text, "unsupported MCP group_by") || strings.Contains(unsafeEnvelope.Result.Content[0].Text, "tenant-secret") {
		t.Fatalf("unexpected unsafe group response: %s", unsafeEnvelope.Result.Content[0].Text)
	}
}

func TestBusinessProfileMCPNoActiveProfileError(t *testing.T) {
	t.Parallel()

	store := openSeededStore(t)
	defer store.Close()
	server := NewServer(store, "gongmcp", "test")
	responses := runServer(t, server,
		requestFrame(Request{
			JSONRPC: "2.0",
			ID:      "profile",
			Method:  "tools/call",
			Params: mustJSON(t, map[string]any{
				"name":      "get_business_profile",
				"arguments": map[string]any{},
			}),
		})+
			requestFrame(Request{
				JSONRPC: "2.0",
				ID:      "concepts",
				Method:  "tools/call",
				Params: mustJSON(t, map[string]any{
					"name":      "list_business_concepts",
					"arguments": map[string]any{},
				}),
			})+
			requestFrame(Request{
				JSONRPC: "2.0",
				ID:      "unmapped",
				Method:  "tools/call",
				Params: mustJSON(t, map[string]any{
					"name": "list_unmapped_crm_fields",
					"arguments": map[string]any{
						"limit": 5,
					},
				}),
			}))
	if len(responses) != 3 {
		t.Fatalf("response count=%d want 3", len(responses))
	}
	for _, response := range responses {
		var envelope struct {
			Result toolCallResult `json:"result"`
		}
		if err := json.Unmarshal(response, &envelope); err != nil {
			t.Fatalf("unmarshal no-profile response: %v", err)
		}
		text := envelope.Result.Content[0].Text
		if !strings.Contains(text, "run gongctl profile discover, validate, and import first") || strings.Contains(text, "sql: no rows") {
			t.Fatalf("unexpected no-profile error: %s", text)
		}
	}
}

func TestReadFrameRejectsOversizedContentLength(t *testing.T) {
	t.Parallel()

	input := "Content-Length: " + strconv.Itoa(maxFrameBytes+1) + "\r\n\r\n"
	_, err := readFrame(bufio.NewReader(strings.NewReader(input)))
	if err == nil {
		t.Fatal("readFrame returned nil error for oversized frame")
	}
	if !strings.Contains(err.Error(), "exceeds maximum") {
		t.Fatalf("error=%q want exceeds maximum", err)
	}
}

func TestJSONTextRejectsOversizedToolResult(t *testing.T) {
	t.Parallel()

	_, err := jsonText(strings.Repeat("x", maxToolResultBytes+1))
	if err == nil {
		t.Fatal("jsonText returned nil error for oversized tool result")
	}
	if !strings.Contains(err.Error(), "tool result exceeds maximum") {
		t.Fatalf("error=%q want tool result exceeds maximum", err)
	}
}

func TestToolResultEnvelopeRejectsDoubleEncodedOverflow(t *testing.T) {
	t.Parallel()

	itemCount := 200000
	largeRaw := json.RawMessage(`{"id":"large-double-encoded","items":[` + strings.TrimRight(strings.Repeat(`"x",`, itemCount), ",") + `]}`)
	if !json.Valid(largeRaw) {
		t.Fatal("test raw JSON is invalid")
	}
	if len(largeRaw) > maxToolResultBytes {
		t.Fatalf("raw JSON size=%d should be under pre-envelope cap %d", len(largeRaw), maxToolResultBytes)
	}
	if _, err := jsonText(largeRaw); err != nil {
		t.Fatalf("jsonText unexpectedly rejected raw JSON before envelope check: %v", err)
	}

	err := ensureToolResultFits("large-call", toolCallResult{
		Content: []toolContent{{Type: "text", Text: string(largeRaw)}},
	})
	if err == nil {
		t.Fatal("ensureToolResultFits allowed double-encoded response overflow")
	}
	if !strings.Contains(err.Error(), "after MCP framing") {
		t.Fatalf("error=%q want after MCP framing", err)
	}
}

func TestWriteFrameRejectsOversizedResponseEnvelope(t *testing.T) {
	t.Parallel()

	err := writeFrame(io.Discard, response{
		JSONRPC: "2.0",
		ID:      "large",
		Result:  strings.Repeat("x", maxFrameBytes),
	})
	if err == nil {
		t.Fatal("writeFrame returned nil error for oversized response")
	}
	if !strings.Contains(err.Error(), "response frame exceeds maximum") {
		t.Fatalf("error=%q want response frame exceeds maximum", err)
	}
}

func openSeededStore(t *testing.T) *sqlite.Store {
	t.Helper()

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "gongmcp.db")
	store, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}

	callFixture := loadCallFixture(t)
	if _, err := store.UpsertCall(ctx, callFixture); err != nil {
		t.Fatalf("upsert call: %v", err)
	}
	extendedCallFixture := loadFixture(t, "internal/store/sqlite/testdata/call.extended.sample.json")
	if _, err := store.UpsertCall(ctx, extendedCallFixture); err != nil {
		t.Fatalf("upsert extended call: %v", err)
	}

	transcriptFixture := loadFixture(t, "testdata/fixtures/transcript.sample.json")
	if _, err := store.UpsertTranscript(ctx, transcriptFixture); err != nil {
		t.Fatalf("upsert transcript: %v", err)
	}

	return store
}

func seedLateStageMCPCall(t *testing.T, store *sqlite.Store) {
	t.Helper()

	lateCall := mustJSON(t, map[string]any{
		"id":      "call_late_stage_mcp",
		"title":   "Late stage MCP call",
		"started": "2026-04-24T14:00:00Z",
		"context": []any{
			map[string]any{
				"objectType": "Opportunity",
				"id":         "opp_late_stage_mcp",
				"fields": []any{
					map[string]any{"name": "StageName", "value": "Contract Signing"},
					map[string]any{"name": "ISSUE__c", "value": "Procurement review"},
				},
			},
		},
	})
	if _, err := store.UpsertCall(context.Background(), lateCall); err != nil {
		t.Fatalf("upsert late-stage call: %v", err)
	}
}

func seedInventoryMCPFixtures(t *testing.T, store *sqlite.Store) {
	t.Helper()

	ctx := context.Background()
	if _, err := store.UpsertCRMIntegration(ctx, mustJSON(t, map[string]any{
		"integrationId": "crm-int-001",
		"name":          "Salesforce production",
		"crmType":       "Salesforce",
	})); err != nil {
		t.Fatalf("upsert CRM integration: %v", err)
	}
	if _, err := store.UpsertCRMSchema(ctx, "crm-int-001", "DEAL", mustJSON(t, map[string]any{
		"requestId": "schema-request-001",
		"objectTypeToSelectedFields": map[string]any{
			"DEAL": []map[string]any{
				{"fieldName": "Amount", "label": "Amount", "fieldType": "currency"},
				{"fieldName": "StageName", "label": "Stage", "fieldType": "picklist"},
			},
		},
	})); err != nil {
		t.Fatalf("upsert CRM schema: %v", err)
	}
	if _, err := store.UpsertGongSetting(ctx, "trackers", mustJSON(t, map[string]any{
		"id":      "tracker-001",
		"name":    "Pricing objection",
		"enabled": true,
	})); err != nil {
		t.Fatalf("upsert Gong setting: %v", err)
	}
	if _, err := store.UpsertGongSetting(ctx, "scorecards", mustJSON(t, map[string]any{
		"scorecardId":   "scorecard-001",
		"scorecardName": "Discovery quality",
		"enabled":       true,
		"reviewMethod":  "AUTOMATIC",
		"questions": []map[string]any{
			{
				"questionId":   "question-001",
				"questionText": "Did the rep confirm pain?",
				"questionType": "SCALE",
				"minRange":     1,
				"maxRange":     5,
			},
		},
	})); err != nil {
		t.Fatalf("upsert Gong scorecard: %v", err)
	}
}

func seedOpportunityAggregateMCPFixtures(t *testing.T, store *sqlite.Store) {
	t.Helper()

	ctx := context.Background()
	calls := []json.RawMessage{
		mustJSON(t, map[string]any{
			"id":       "call_mcp_gap_covered",
			"title":    "Covered MCP contract call",
			"started":  "2026-04-23T12:00:00Z",
			"duration": 600,
			"context": []any{
				map[string]any{
					"objectType": "Opportunity",
					"id":         "opp_mcp_gap_001",
					"name":       "MCP Gap Opportunity",
					"fields": []any{
						map[string]any{"name": "StageName", "value": "Contract Signing"},
						map[string]any{"name": "Amount", "value": "75000"},
						map[string]any{"name": "CloseDate", "value": "2026-05-15"},
					},
				},
			},
		}),
		mustJSON(t, map[string]any{
			"id":       "call_mcp_gap_missing",
			"title":    "Missing MCP transcript call",
			"started":  "2026-04-24T12:00:00Z",
			"duration": 1200,
			"context": []any{
				map[string]any{
					"objectType": "Opportunity",
					"id":         "opp_mcp_gap_001",
					"name":       "MCP Gap Opportunity",
					"fields": []any{
						map[string]any{"name": "StageName", "value": "Contract Signing"},
						map[string]any{"name": "Amount", "value": "75000"},
						map[string]any{"name": "CloseDate", "value": "2026-05-15"},
					},
				},
			},
		}),
	}
	for _, raw := range calls {
		if _, err := store.UpsertCall(ctx, raw); err != nil {
			t.Fatalf("upsert aggregate MCP call: %v", err)
		}
	}

	transcript := mustJSON(t, map[string]any{
		"callTranscripts": []any{
			map[string]any{
				"callId": "call_mcp_gap_covered",
				"transcript": []any{
					map[string]any{
						"speakerId": "buyer",
						"sentences": []any{
							map[string]any{"start": 1000, "end": 3000, "text": "Pricing needs procurement approval."},
						},
					},
				},
			},
		},
	})
	if _, err := store.UpsertTranscript(ctx, transcript); err != nil {
		t.Fatalf("upsert aggregate MCP transcript: %v", err)
	}
}

func seedBusinessProfileMCPFixtures(t *testing.T, store *sqlite.Store) {
	t.Helper()

	ctx := context.Background()
	if _, err := store.UpsertCall(ctx, mustJSON(t, map[string]any{
		"id":        "call_mcp_profile_001",
		"title":     "MCP profile custom deal call",
		"started":   "2026-04-24T16:00:00Z",
		"duration":  1200,
		"system":    "Zoom",
		"direction": "Conference",
		"scope":     "External",
		"context": []any{
			map[string]any{
				"objectType": "Deal",
				"id":         "deal_mcp_profile_001",
				"name":       "MCP Profile Deal",
				"fields": []any{
					map[string]any{"name": "DealPhase__c", "value": "Proposal"},
					map[string]any{"name": "Amount", "value": "10000"},
					map[string]any{"name": "SecretID__c", "value": "tenant-secret-001"},
				},
			},
			map[string]any{
				"objectType": "Company",
				"id":         "company_mcp_profile_001",
				"name":       "MCP Profile Company",
				"fields": []any{
					map[string]any{"name": "Industry", "value": "Manufacturing"},
				},
			},
		},
	})); err != nil {
		t.Fatalf("upsert business profile MCP call: %v", err)
	}

	body := []byte(`
version: 1
name: MCP custom profile
objects:
  deal:
    object_types: [Deal]
  account:
    object_types: [Company]
fields:
  deal_stage:
    object: deal
    names: [DealPhase__c]
  account_industry:
    object: account
    names: [Industry]
  secret_id:
    object: deal
    names: [SecretID__c]
lifecycle:
  open:
    label: Open
    order: 10
    rules:
      - field: deal_stage
        op: in
        values: [Proposal]
  closed_won:
    label: Closed won
    order: 20
  closed_lost:
    label: Closed lost
    order: 30
  post_sales:
    label: Post sales
    order: 40
  unknown:
    label: Unknown
    order: 999
methodology:
  pain:
    description: Pain evidence
    aliases: [pain]
`)
	inventory, err := store.ProfileInventory(ctx)
	if err != nil {
		t.Fatalf("profile inventory: %v", err)
	}
	p, validation, err := profilepkg.ValidateBytes(body, inventory)
	if err != nil {
		t.Fatalf("validate business profile: %v", err)
	}
	if !validation.Valid {
		t.Fatalf("business profile has validation errors: %+v", validation.Findings)
	}
	canonical, err := profilepkg.CanonicalJSON(p)
	if err != nil {
		t.Fatalf("canonical profile json: %v", err)
	}
	if _, err := store.ImportProfile(ctx, sqlite.ProfileImportParams{
		SourcePath:      "/Users/example/private/mcp-profile.yaml",
		SourceSHA256:    validation.SourceSHA256,
		CanonicalSHA256: validation.CanonicalSHA256,
		RawYAML:         body,
		CanonicalJSON:   canonical,
		Profile:         p,
		Findings:        validation.Findings,
		ImportedBy:      "example-user",
	}); err != nil {
		t.Fatalf("import business profile: %v", err)
	}
}

func seedLifecycleMCPFixtures(t *testing.T, store *sqlite.Store) {
	t.Helper()

	ctx := context.Background()
	calls := []json.RawMessage{
		mustJSON(t, map[string]any{
			"id":       "call_mcp_lifecycle_active",
			"title":    "MCP lifecycle active pipeline call",
			"started":  "2026-04-21T12:00:00Z",
			"duration": 900,
			"context": []any{
				map[string]any{
					"objectType": "Opportunity",
					"id":         "opp_mcp_lifecycle_active",
					"name":       "MCP Active Lifecycle Opportunity",
					"fields": []any{
						map[string]any{"name": "StageName", "value": "MQL"},
						map[string]any{"name": "Type", "value": "New Business"},
						map[string]any{"name": "Primary_Lead_Source__c", "value": "Web"},
					},
				},
			},
		}),
		mustJSON(t, map[string]any{
			"id":       "call_mcp_lifecycle_renewal",
			"title":    "MCP lifecycle renewal call",
			"started":  "2026-04-22T12:00:00Z",
			"duration": 1200,
			"context": []any{
				map[string]any{
					"objectType": "Opportunity",
					"id":         "opp_mcp_lifecycle_renewal",
					"name":       "MCP Renewal Lifecycle Opportunity",
					"fields": []any{
						map[string]any{"name": "StageName", "value": "Discovery & Demo (SQO)"},
						map[string]any{"name": "Type", "value": "Renewal"},
						map[string]any{"name": "Renewal_Process__c", "value": "Standard"},
					},
				},
			},
		}),
		mustJSON(t, map[string]any{
			"id":       "call_mcp_lifecycle_upsell",
			"title":    "MCP lifecycle upsell call",
			"started":  "2026-04-23T12:00:00Z",
			"duration": 2400,
			"context": []any{
				map[string]any{
					"objectType": "Opportunity",
					"id":         "opp_mcp_lifecycle_upsell",
					"name":       "MCP Upsell Lifecycle Opportunity",
					"fields": []any{
						map[string]any{"name": "StageName", "value": "Demo & Business Case"},
						map[string]any{"name": "Type", "value": "Upsell"},
						map[string]any{"name": "One_Year_Upsell__c", "value": "12000"},
					},
				},
			},
		}),
	}
	for _, raw := range calls {
		if _, err := store.UpsertCall(ctx, raw); err != nil {
			t.Fatalf("upsert lifecycle MCP call: %v", err)
		}
	}

	transcript := mustJSON(t, map[string]any{
		"callTranscripts": []any{
			map[string]any{
				"callId": "call_mcp_lifecycle_renewal",
				"transcript": []any{
					map[string]any{
						"speakerId": "speaker",
						"sentences": []any{
							map[string]any{"start": 1000, "end": 2000, "text": "Renewal transcript coverage."},
						},
					},
				},
			},
		},
	})
	if _, err := store.UpsertTranscript(ctx, transcript); err != nil {
		t.Fatalf("upsert lifecycle MCP transcript: %v", err)
	}
}

func loadCallFixture(t *testing.T) json.RawMessage {
	t.Helper()

	var payload struct {
		Calls []json.RawMessage `json:"calls"`
	}
	if err := json.Unmarshal(loadFixture(t, "testdata/fixtures/calls.extensive.sample.json"), &payload); err != nil {
		t.Fatalf("unmarshal call fixture: %v", err)
	}
	if len(payload.Calls) == 0 {
		t.Fatal("calls fixture missing calls")
	}
	return payload.Calls[0]
}

func loadFixture(t *testing.T, rel string) []byte {
	t.Helper()

	path := filepath.Join(repoRoot(t), filepath.FromSlash(rel))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", rel, err)
	}
	return data
}

func repoRoot(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func runServer(t *testing.T, server *Server, input string) [][]byte {
	t.Helper()

	var output bytes.Buffer
	if err := server.Serve(context.Background(), bytes.NewBufferString(input), &output); err != nil {
		t.Fatalf("serve mcp: %v", err)
	}

	reader := bytes.NewReader(output.Bytes())
	bufReader := bufio.NewReader(reader)
	var frames [][]byte
	for {
		payload, err := readFrame(bufReader)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return frames
			}
			t.Fatalf("read response frame: %v", err)
		}
		frames = append(frames, payload)
	}
}

func requestFrame(req Request) string {
	payload, err := json.Marshal(req)
	if err != nil {
		panic(err)
	}
	return "Content-Length: " + strconv.Itoa(len(payload)) + "\r\n\r\n" + string(payload)
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()

	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal JSON: %v", err)
	}
	return payload
}

func mapFromJSONText(t *testing.T, text string, index int) map[string]any {
	t.Helper()

	var rows []map[string]any
	if err := json.Unmarshal([]byte(text), &rows); err != nil {
		t.Fatalf("unmarshal JSON text: %v", err)
	}
	if index >= len(rows) {
		t.Fatalf("row index %d out of range for %d rows", index, len(rows))
	}
	return rows[index]
}
