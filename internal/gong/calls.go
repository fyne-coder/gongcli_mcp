package gong

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

var defaultDateLocation = mustLoadLocation("America/New_York")

type CallListParams struct {
	From    string
	To      string
	Cursor  string
	Context string
}

type TranscriptParams struct {
	CallIDs []string
	From    string
	To      string
	Cursor  string
}

func (c *Client) ListCalls(ctx context.Context, params CallListParams) (*Response, error) {
	filter := map[string]any{}
	if params.From != "" {
		value, err := c.normalizeDateTime(params.From, false)
		if err != nil {
			return nil, err
		}
		filter["fromDateTime"] = value
	}
	if params.To != "" {
		value, err := c.normalizeDateTime(params.To, true)
		if err != nil {
			return nil, err
		}
		filter["toDateTime"] = value
	}

	body := map[string]any{"filter": filter}
	if params.Cursor != "" {
		body["cursor"] = params.Cursor
	}
	if params.Context != "" {
		body["contentSelector"] = map[string]any{"context": params.Context}
	}

	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return c.do(ctx, "POST", "/v2/calls/extensive", encoded)
}

func (c *Client) GetTranscript(ctx context.Context, params TranscriptParams) (*Response, error) {
	filter := map[string]any{}
	if len(params.CallIDs) > 0 {
		filter["callIds"] = params.CallIDs
	}
	if params.From != "" {
		value, err := c.normalizeDateTime(params.From, false)
		if err != nil {
			return nil, err
		}
		filter["fromDateTime"] = value
	}
	if params.To != "" {
		value, err := c.normalizeDateTime(params.To, true)
		if err != nil {
			return nil, err
		}
		filter["toDateTime"] = value
	}

	body := map[string]any{"filter": filter}
	if params.Cursor != "" {
		body["cursor"] = params.Cursor
	}

	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return c.do(ctx, "POST", "/v2/calls/transcript", encoded)
}

func (c *Client) normalizeDateTime(value string, endOfDay bool) (string, error) {
	return normalizeDateTimeInLocation(value, endOfDay, c.dateLocation)
}

func normalizeDateTime(value string, endOfDay bool) (string, error) {
	return normalizeDateTimeInLocation(value, endOfDay, defaultDateLocation)
}

func normalizeDateTimeInLocation(value string, endOfDay bool, location *time.Location) (string, error) {
	if _, err := time.Parse(time.RFC3339, value); err == nil {
		return value, nil
	}

	if location == nil {
		location = defaultDateLocation
	}

	day, err := time.ParseInLocation("2006-01-02", value, location)
	if err != nil {
		return "", fmt.Errorf("date %q must be YYYY-MM-DD or RFC3339", value)
	}
	if endOfDay {
		day = day.AddDate(0, 0, 1).Add(-time.Nanosecond)
	}
	return day.Format(time.RFC3339), nil
}

func mustLoadLocation(name string) *time.Location {
	location, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return location
}
