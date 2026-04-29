package gong

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/fyne-coder/gongcli_mcp/internal/auth"
	"github.com/fyne-coder/gongcli_mcp/internal/ratelimit"
)

const DefaultBaseURL = "https://api.gong.io"

type Options struct {
	BaseURL      string
	Credentials  auth.Credentials
	HTTPClient   *http.Client
	Limiter      *ratelimit.Limiter
	MaxRetries   int
	UserAgent    string
	DateLocation *time.Location
}

type Client struct {
	baseURL      *url.URL
	credentials  auth.Credentials
	httpClient   *http.Client
	limiter      *ratelimit.Limiter
	maxRetries   int
	userAgent    string
	dateLocation *time.Location
}

type Response struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

type HTTPError struct {
	StatusCode int
	Body       []byte
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("gong API returned status %d (response body redacted)", e.StatusCode)
}

func NewClient(opts Options) (*Client, error) {
	base := opts.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	parsed, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid Gong base URL %q", base)
	}

	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	maxRetries := opts.MaxRetries
	if maxRetries == 0 {
		maxRetries = 4
	}
	if maxRetries < 0 {
		maxRetries = 0
	}
	userAgent := opts.UserAgent
	if userAgent == "" {
		userAgent = "gongctl/0.1"
	}
	dateLocation := opts.DateLocation
	if dateLocation == nil {
		dateLocation = defaultDateLocation
	}

	return &Client{
		baseURL:      parsed,
		credentials:  opts.Credentials,
		httpClient:   httpClient,
		limiter:      opts.Limiter,
		maxRetries:   maxRetries,
		userAgent:    userAgent,
		dateLocation: dateLocation,
	}, nil
}

func (c *Client) Raw(ctx context.Context, method string, path string, body []byte) (*Response, error) {
	return c.do(ctx, method, path, body)
}

func (c *Client) do(ctx context.Context, method string, path string, body []byte) (*Response, error) {
	endpoint, err := c.endpoint(path)
	if err != nil {
		return nil, err
	}

	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if c.limiter != nil {
			if err := c.limiter.Wait(ctx); err != nil {
				return nil, err
			}
		}

		req, err := http.NewRequestWithContext(ctx, strings.ToUpper(method), endpoint, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", c.userAgent)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		req.SetBasicAuth(c.credentials.AccessKey, c.credentials.AccessKeySecret)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			if attempt == c.maxRetries {
				return nil, err
			}
			if err := sleep(ctx, retryBackoff(nil, attempt)); err != nil {
				return nil, err
			}
			continue
		}

		respBody, readErr := io.ReadAll(resp.Body)
		closeErr := resp.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}

		out := &Response{
			StatusCode: resp.StatusCode,
			Header:     resp.Header.Clone(),
			Body:       respBody,
		}

		if shouldRetry(resp.StatusCode) && attempt < c.maxRetries {
			if err := sleep(ctx, retryBackoff(resp.Header, attempt)); err != nil {
				return nil, err
			}
			continue
		}

		if resp.StatusCode >= 400 {
			return out, &HTTPError{StatusCode: resp.StatusCode, Body: respBody}
		}
		return out, nil
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("request failed after retries")
}

func (c *Client) endpoint(path string) (string, error) {
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		absolute, err := url.Parse(path)
		if err != nil {
			return "", err
		}
		if !sameOrigin(absolute, c.baseURL) {
			return "", fmt.Errorf("absolute URL %q must match configured Gong origin %s://%s", path, c.baseURL.Scheme, c.baseURL.Host)
		}
		return absolute.String(), nil
	}

	u := *c.baseURL
	basePath := strings.TrimRight(u.Path, "/")
	relative, err := url.Parse("/" + strings.TrimLeft(path, "/"))
	if err != nil {
		return "", err
	}
	requestPath := relative.Path
	if strings.HasSuffix(basePath, "/v2") && strings.HasPrefix(requestPath, "/v2/") {
		requestPath = strings.TrimPrefix(requestPath, "/v2")
	}
	u.Path = basePath + requestPath
	u.RawQuery = relative.RawQuery
	return u.String(), nil
}

func shouldRetry(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusBadGateway || status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout
}

func retryBackoff(headers http.Header, attempt int) time.Duration {
	if headers != nil {
		if value := headers.Get("Retry-After"); value != "" {
			if seconds, err := strconv.Atoi(value); err == nil {
				return time.Duration(seconds) * time.Second
			}
			if when, err := http.ParseTime(value); err == nil {
				if delay := time.Until(when); delay > 0 {
					return delay
				}
			}
		}
	}

	delay := time.Duration(1<<attempt) * time.Second
	if delay > 30*time.Second {
		return 30 * time.Second
	}
	return delay
}

func sleep(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func sameOrigin(a *url.URL, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) &&
		strings.EqualFold(a.Hostname(), b.Hostname()) &&
		originPort(a) == originPort(b)
}

func originPort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	switch strings.ToLower(u.Scheme) {
	case "http":
		return "80"
	case "https":
		return "443"
	default:
		return ""
	}
}
