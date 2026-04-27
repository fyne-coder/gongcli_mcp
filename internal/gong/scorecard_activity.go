package gong

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type ScorecardActivityParams struct {
	CallFromDate    string
	CallToDate      string
	ReviewFromDate  string
	ReviewToDate    string
	ReviewMethod    string
	ReviewedUserIDs []string
	ScorecardIDs    []string
	Cursor          string
}

func (c *Client) ListAnsweredScorecards(ctx context.Context, params ScorecardActivityParams) (*Response, error) {
	filter := map[string]any{}
	if params.CallFromDate != "" {
		value, err := normalizeGongDate(params.CallFromDate, "callFromDate")
		if err != nil {
			return nil, err
		}
		filter["callFromDate"] = value
	}
	if params.CallToDate != "" {
		value, err := normalizeGongDate(params.CallToDate, "callToDate")
		if err != nil {
			return nil, err
		}
		filter["callToDate"] = value
	}
	if params.ReviewFromDate != "" {
		value, err := normalizeGongDate(params.ReviewFromDate, "reviewFromDate")
		if err != nil {
			return nil, err
		}
		filter["reviewFromDate"] = value
	}
	if params.ReviewToDate != "" {
		value, err := normalizeGongDate(params.ReviewToDate, "reviewToDate")
		if err != nil {
			return nil, err
		}
		filter["reviewToDate"] = value
	}
	if method := strings.ToUpper(strings.TrimSpace(params.ReviewMethod)); method != "" {
		switch method {
		case "AUTOMATIC", "MANUAL", "BOTH":
			filter["reviewMethod"] = method
		default:
			return nil, fmt.Errorf("review method must be one of: AUTOMATIC, MANUAL, BOTH")
		}
	}
	if len(params.ReviewedUserIDs) > 0 {
		filter["reviewedUserIds"] = cleanStrings(params.ReviewedUserIDs)
	}
	if len(params.ScorecardIDs) > 0 {
		filter["scorecardIds"] = cleanStrings(params.ScorecardIDs)
	}

	body := map[string]any{"filter": filter}
	if cursor := strings.TrimSpace(params.Cursor); cursor != "" {
		body["cursor"] = cursor
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return c.do(ctx, "POST", "/v2/stats/activity/scorecards", encoded)
}

func normalizeGongDate(value string, fieldName string) (string, error) {
	day := strings.TrimSpace(value)
	if day == "" {
		return "", nil
	}
	parsed, err := time.Parse("2006-01-02", day)
	if err != nil {
		return "", fmt.Errorf("%s must be YYYY-MM-DD", fieldName)
	}
	return parsed.Format("2006-01-02"), nil
}

func cleanStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if clean := strings.TrimSpace(value); clean != "" {
			out = append(out, clean)
		}
	}
	return out
}
