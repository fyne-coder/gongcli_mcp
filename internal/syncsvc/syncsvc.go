package syncsvc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/fyne-coder/gongcli_mcp/internal/gong"
	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
)

const (
	scopeCalls           = "calls"
	scopeUsers           = "users"
	scopeCRMIntegrations = "crm_integrations"
	scopeCRMSchema       = "crm_schema"
	scopeSettings        = "settings"
)

type CallsParams struct {
	From          string
	To            string
	Cursor        string
	Context       string
	Preset        string
	MaxPages      int
	ExposeParties bool
}

type UsersParams struct {
	Cursor   string
	Limit    int
	Preset   string
	MaxPages int
}

type CRMIntegrationsParams struct{}

type CRMSchemaParams struct {
	IntegrationID string
	ObjectTypes   []string
}

type SettingsParams struct {
	Kind        string
	WorkspaceID string
}

type Result struct {
	RunID                    int64
	Scope                    string
	SyncKey                  string
	Cursor                   string
	Pages                    int
	RecordsSeen              int64
	RecordsWritten           int64
	ParticipantCaptureStatus string
}

func SyncCRMIntegrations(ctx context.Context, client *gong.Client, store *sqlite.Store, params CRMIntegrationsParams) (result Result, err error) {
	if client == nil {
		return result, errors.New("gong client is required")
	}
	if store == nil {
		return result, errors.New("sqlite store is required")
	}

	result.Scope = scopeCRMIntegrations
	result.SyncKey = scopeCRMIntegrations
	run, err := store.StartSyncRun(ctx, sqlite.StartSyncRunParams{
		Scope:          scopeCRMIntegrations,
		SyncKey:        result.SyncKey,
		RequestContext: "endpoint=/v2/crm/integrations",
	})
	if err != nil {
		return result, err
	}
	result.RunID = run.ID
	defer finishRun(ctx, store, run.ID, &result, &err)

	resp, err := client.Raw(ctx, "GET", "/v2/crm/integrations", nil)
	if err != nil {
		return result, err
	}
	items, err := decodeRootItems(resp.Body, "integrations")
	if err != nil {
		return result, err
	}

	result.Pages = 1
	result.RecordsSeen = int64(len(items))
	for _, item := range items {
		if _, err := store.UpsertCRMIntegration(ctx, item); err != nil {
			return result, err
		}
		result.RecordsWritten++
	}
	return result, nil
}

func SyncCRMSchema(ctx context.Context, client *gong.Client, store *sqlite.Store, params CRMSchemaParams) (result Result, err error) {
	if client == nil {
		return result, errors.New("gong client is required")
	}
	if store == nil {
		return result, errors.New("sqlite store is required")
	}
	integrationID := strings.TrimSpace(params.IntegrationID)
	if integrationID == "" {
		return result, errors.New("integration id is required")
	}
	objectTypes := cleanObjectTypes(params.ObjectTypes)
	if len(objectTypes) == 0 {
		return result, errors.New("at least one object type is required")
	}

	result.Scope = scopeCRMSchema
	result.SyncKey = "crm_schema:integration_id=" + integrationID + ":object_types=" + strings.Join(objectTypes, ",")
	requestContext := "integration_id=" + integrationID + ",object_types=" + strings.Join(objectTypes, ",")
	run, err := store.StartSyncRun(ctx, sqlite.StartSyncRunParams{
		Scope:          scopeCRMSchema,
		SyncKey:        result.SyncKey,
		RequestContext: requestContext,
	})
	if err != nil {
		return result, err
	}
	result.RunID = run.ID
	defer finishRun(ctx, store, run.ID, &result, &err)

	for _, objectType := range objectTypes {
		values := url.Values{}
		values.Set("integrationId", integrationID)
		values.Set("objectType", objectType)
		resp, err := client.Raw(ctx, "GET", "/v2/crm/entity-schema?"+values.Encode(), nil)
		if err != nil {
			return result, err
		}
		fields, err := store.UpsertCRMSchema(ctx, integrationID, objectType, resp.Body)
		if err != nil {
			return result, err
		}
		result.Pages++
		result.RecordsSeen++
		result.RecordsWritten += fields
	}
	return result, nil
}

func SyncSettings(ctx context.Context, client *gong.Client, store *sqlite.Store, params SettingsParams) (result Result, err error) {
	if client == nil {
		return result, errors.New("gong client is required")
	}
	if store == nil {
		return result, errors.New("sqlite store is required")
	}
	kind, endpoint, rootKey, err := settingsEndpoint(params.Kind, params.WorkspaceID)
	if err != nil {
		return result, err
	}

	result.Scope = scopeSettings
	result.SyncKey = "settings:kind=" + kind
	requestContext := "kind=" + kind + ",endpoint=" + endpoint
	if workspaceID := strings.TrimSpace(params.WorkspaceID); workspaceID != "" {
		result.SyncKey += ":workspace_id=" + workspaceID
		requestContext += ",workspace_id=" + workspaceID
	}
	run, err := store.StartSyncRun(ctx, sqlite.StartSyncRunParams{
		Scope:          scopeSettings,
		SyncKey:        result.SyncKey,
		RequestContext: requestContext,
	})
	if err != nil {
		return result, err
	}
	result.RunID = run.ID
	defer finishRun(ctx, store, run.ID, &result, &err)

	resp, err := client.Raw(ctx, "GET", endpoint, nil)
	if err != nil {
		return result, err
	}
	items, err := decodeRootItems(resp.Body, rootKey, "items")
	if err != nil {
		return result, err
	}
	result.Pages = 1
	result.RecordsSeen = int64(len(items))
	for _, item := range items {
		if _, err := store.UpsertGongSetting(ctx, kind, item); err != nil {
			return result, err
		}
		result.RecordsWritten++
	}
	return result, nil
}

func SyncCalls(ctx context.Context, client *gong.Client, store *sqlite.Store, params CallsParams) (result Result, err error) {
	if client == nil {
		return result, errors.New("gong client is required")
	}
	if store == nil {
		return result, errors.New("sqlite store is required")
	}
	if params.MaxPages < 0 {
		return result, errors.New("max pages must be >= 0")
	}

	result.Scope = scopeCalls
	result.SyncKey = buildSyncKey(scopeCalls, params.Preset, params.Context, params.From, params.To)
	baseRequestContext := buildRequestContext(params.Preset, params.Context)
	requestContext := buildCallRequestContext(baseRequestContext, params.ExposeParties, false)
	if params.ExposeParties {
		result.ParticipantCaptureStatus = "requested"
	}

	run, err := store.StartSyncRun(ctx, sqlite.StartSyncRunParams{
		Scope:          scopeCalls,
		SyncKey:        result.SyncKey,
		Cursor:         strings.TrimSpace(params.Cursor),
		From:           strings.TrimSpace(params.From),
		To:             strings.TrimSpace(params.To),
		RequestContext: requestContext,
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
			Cursor:         result.Cursor,
			RecordsSeen:    result.RecordsSeen,
			RecordsWritten: result.RecordsWritten,
			ErrorText:      errorText,
			RequestContext: requestContext,
		})
		if finishErr != nil {
			if err != nil {
				err = fmt.Errorf("%w; finish sync run: %v", err, finishErr)
				return
			}
			err = finishErr
		}
	}()

	request := gong.CallListParams{
		From:          strings.TrimSpace(params.From),
		To:            strings.TrimSpace(params.To),
		Cursor:        strings.TrimSpace(params.Cursor),
		Context:       strings.TrimSpace(params.Context),
		ExposeParties: params.ExposeParties,
	}

	seenCursors := map[string]struct{}{}
	if request.Cursor != "" {
		seenCursors[request.Cursor] = struct{}{}
	}

	for {
		resp, err := client.ListCalls(ctx, request)
		if err != nil && request.ExposeParties && result.Pages == 0 && result.RecordsSeen == 0 && canRetryWithoutParties(err) {
			request.ExposeParties = false
			result.ParticipantCaptureStatus = "omitted_fallback"
			requestContext = buildCallRequestContext(baseRequestContext, params.ExposeParties, true)
			resp, err = client.ListCalls(ctx, request)
		}
		if err != nil {
			return result, err
		}

		page, err := decodeCallsPage(resp.Body)
		if err != nil {
			return result, err
		}

		result.Pages++
		result.RecordsSeen += int64(len(page.Items))
		for _, raw := range page.Items {
			if _, err := store.UpsertCall(ctx, raw); err != nil {
				return result, err
			}
			result.RecordsWritten++
		}

		nextCursor := strings.TrimSpace(page.Records.Cursor)
		if nextCursor != "" {
			result.Cursor = nextCursor
		}
		if nextCursor == "" {
			return result, nil
		}
		if params.MaxPages > 0 && result.Pages >= params.MaxPages {
			return result, nil
		}
		if _, ok := seenCursors[nextCursor]; ok {
			return result, fmt.Errorf("pagination cursor repeated after %d page(s): %s", result.Pages, nextCursor)
		}
		seenCursors[nextCursor] = struct{}{}
		request.Cursor = nextCursor
	}
}

func canRetryWithoutParties(err error) bool {
	var httpErr *gong.HTTPError
	if !errors.As(err, &httpErr) {
		return false
	}
	return httpErr.StatusCode == 400 || httpErr.StatusCode == 422
}

func SyncUsers(ctx context.Context, client *gong.Client, store *sqlite.Store, params UsersParams) (result Result, err error) {
	if client == nil {
		return result, errors.New("gong client is required")
	}
	if store == nil {
		return result, errors.New("sqlite store is required")
	}
	if params.MaxPages < 0 {
		return result, errors.New("max pages must be >= 0")
	}

	result.Scope = scopeUsers
	result.SyncKey = buildSyncKey(scopeUsers, params.Preset, "", "", "")

	run, err := store.StartSyncRun(ctx, sqlite.StartSyncRunParams{
		Scope:          scopeUsers,
		SyncKey:        result.SyncKey,
		Cursor:         strings.TrimSpace(params.Cursor),
		RequestContext: buildRequestContext(params.Preset, ""),
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
			Cursor:         result.Cursor,
			RecordsSeen:    result.RecordsSeen,
			RecordsWritten: result.RecordsWritten,
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

	request := gong.UserListParams{
		Cursor: strings.TrimSpace(params.Cursor),
	}
	if params.Limit > 0 {
		request.Limit = params.Limit
	}

	seenCursors := map[string]struct{}{}
	if request.Cursor != "" {
		seenCursors[request.Cursor] = struct{}{}
	}

	for {
		resp, err := listUsers(ctx, client, request)
		if err != nil {
			return result, err
		}

		page, err := decodeUsersPage(resp.Body)
		if err != nil {
			return result, err
		}

		result.Pages++
		result.RecordsSeen += int64(len(page.Items))
		for _, raw := range page.Items {
			if _, err := store.UpsertUser(ctx, raw); err != nil {
				return result, err
			}
			result.RecordsWritten++
		}

		nextCursor := strings.TrimSpace(page.Records.Cursor)
		if nextCursor != "" {
			result.Cursor = nextCursor
		}
		if nextCursor == "" {
			return result, nil
		}
		if params.MaxPages > 0 && result.Pages >= params.MaxPages {
			return result, nil
		}
		if _, ok := seenCursors[nextCursor]; ok {
			return result, fmt.Errorf("pagination cursor repeated after %d page(s): %s", result.Pages, nextCursor)
		}
		seenCursors[nextCursor] = struct{}{}
		request.Cursor = nextCursor
	}
}

func Status(ctx context.Context, store *sqlite.Store) (*sqlite.SyncStatusSummary, error) {
	if store == nil {
		return nil, errors.New("sqlite store is required")
	}
	return store.SyncStatusSummary(ctx)
}

func finishRun(ctx context.Context, store *sqlite.Store, runID int64, result *Result, errp *error) {
	status := "success"
	errorText := ""
	if *errp != nil {
		status = "error"
		errorText = (*errp).Error()
	}
	finishErr := store.FinishSyncRun(ctx, runID, sqlite.FinishSyncRunParams{
		Status:         status,
		Cursor:         result.Cursor,
		RecordsSeen:    result.RecordsSeen,
		RecordsWritten: result.RecordsWritten,
		ErrorText:      errorText,
	})
	if finishErr != nil {
		if *errp != nil {
			*errp = fmt.Errorf("%w; finish sync run: %v", *errp, finishErr)
			return
		}
		*errp = finishErr
	}
}

func listUsers(ctx context.Context, client *gong.Client, params gong.UserListParams) (*gong.Response, error) {
	values := url.Values{}
	if value := strings.TrimSpace(params.Cursor); value != "" {
		values.Set("cursor", value)
	}
	if params.Limit > 0 {
		values.Set("limit", fmt.Sprintf("%d", params.Limit))
	}

	requestPath := "/v2/users"
	if encoded := values.Encode(); encoded != "" {
		requestPath += "?" + encoded
	}
	return client.Raw(ctx, "GET", requestPath, nil)
}

func settingsEndpoint(kind string, workspaceID string) (string, string, string, error) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "tracker", "trackers", "keywordtracker", "keywordtrackers", "keyword_trackers":
		values := url.Values{}
		if value := strings.TrimSpace(workspaceID); value != "" {
			values.Set("workspaceId", value)
		}
		path := "/v2/settings/trackers"
		if encoded := values.Encode(); encoded != "" {
			path += "?" + encoded
		}
		return "trackers", path, "keywordTrackers", nil
	case "scorecard", "scorecards":
		return "scorecards", "/v2/settings/scorecards", "scorecards", nil
	case "workspace", "workspaces":
		return "workspaces", "/v2/workspaces", "workspaces", nil
	default:
		return "", "", "", fmt.Errorf("--kind must be one of: trackers, scorecards, workspaces")
	}
}

type callsPage struct {
	Items   []json.RawMessage
	Records gong.PageRecords
}

type usersPage struct {
	Items   []json.RawMessage
	Records gong.PageRecords
}

func decodeRootItems(body []byte, keys ...string) ([]json.RawMessage, error) {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return nil, errors.New("response body is empty")
	}
	if strings.HasPrefix(trimmed, "[") {
		var items []json.RawMessage
		if err := json.Unmarshal(body, &items); err != nil {
			return nil, err
		}
		return items, nil
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	for _, key := range keys {
		for existing, raw := range payload {
			if !strings.EqualFold(existing, key) {
				continue
			}
			var items []json.RawMessage
			if err := json.Unmarshal(raw, &items); err == nil {
				return items, nil
			}
			var one json.RawMessage
			if err := json.Unmarshal(raw, &one); err == nil && len(one) > 0 {
				return []json.RawMessage{one}, nil
			}
		}
	}
	return nil, fmt.Errorf("response did not contain one of: %s", strings.Join(keys, ", "))
}

func decodeCallsPage(body []byte) (callsPage, error) {
	var payload struct {
		Calls   []json.RawMessage `json:"calls"`
		Records gong.PageRecords  `json:"records"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return callsPage{}, err
	}
	return callsPage{Items: payload.Calls, Records: payload.Records}, nil
}

func decodeUsersPage(body []byte) (usersPage, error) {
	var payload struct {
		Users   []json.RawMessage `json:"users"`
		Records gong.PageRecords  `json:"records"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return usersPage{}, err
	}
	return usersPage{Items: payload.Users, Records: payload.Records}, nil
}

func cleanObjectTypes(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			clean := strings.TrimSpace(part)
			if clean == "" {
				continue
			}
			key := strings.ToUpper(clean)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, clean)
		}
	}
	return out
}

func buildSyncKey(scope string, preset string, requestContext string, from string, to string) string {
	parts := []string{scope}
	if value := strings.TrimSpace(preset); value != "" {
		parts = append(parts, "preset="+value)
	}
	if value := strings.TrimSpace(requestContext); value != "" {
		parts = append(parts, "context="+value)
	}
	if value := strings.TrimSpace(from); value != "" {
		parts = append(parts, "from="+value)
	}
	if value := strings.TrimSpace(to); value != "" {
		parts = append(parts, "to="+value)
	}
	return strings.Join(parts, ":")
}

func buildRequestContext(preset string, requestContext string) string {
	var parts []string
	if value := strings.TrimSpace(preset); value != "" {
		parts = append(parts, "preset="+value)
	}
	if value := strings.TrimSpace(requestContext); value != "" {
		parts = append(parts, "context="+value)
	}
	return strings.Join(parts, ",")
}

func buildCallRequestContext(base string, includeParties bool, fallbackWithoutParties bool) string {
	parts := make([]string, 0, 3)
	if value := strings.TrimSpace(base); value != "" {
		parts = append(parts, value)
	}
	if !includeParties {
		return strings.Join(parts, ",")
	}
	parts = append(parts, "include_parties_requested=true")
	if fallbackWithoutParties {
		parts = append(parts, "include_parties_result=omitted_fallback")
	} else {
		parts = append(parts, "include_parties_result=request_sent")
	}
	return strings.Join(parts, ",")
}
