package gong

import (
	"context"
	"net/url"
	"strconv"
)

type UserListParams struct {
	Cursor string
	Limit  int
}

func (c *Client) ListUsers(ctx context.Context, params UserListParams) (*Response, error) {
	values := url.Values{}
	if params.Cursor != "" {
		values.Set("cursor", params.Cursor)
	}
	if params.Limit > 0 {
		values.Set("limit", strconv.Itoa(params.Limit))
	}

	path := "/v2/users"
	if encoded := values.Encode(); encoded != "" {
		path += "?" + encoded
	}
	return c.do(ctx, "GET", path, nil)
}
