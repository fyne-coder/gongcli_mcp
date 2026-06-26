package postgres

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

// ParseRefreshStatementTimeout parses an operator-supplied refresh statement
// timeout. Zero, negative, and common disabled sentinels are rejected.
func ParseRefreshStatementTimeout(raw string) (time.Duration, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, fmt.Errorf("statement timeout is required")
	}
	switch strings.ToLower(value) {
	case "0", "0s", "0ms", "0m", "0h", "disabled", "off", "none", "false":
		return 0, fmt.Errorf("statement timeout must be greater than zero")
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("statement timeout is not a valid duration")
	}
	if d <= 0 {
		return 0, fmt.Errorf("statement timeout must be greater than zero")
	}
	if d < time.Millisecond {
		return 0, fmt.Errorf("statement timeout must be at least 1ms")
	}
	return d, nil
}

// FormatRefreshStatementTimeout renders a parsed timeout for sanitized operator
// output such as deploy JSON.
func FormatRefreshStatementTimeout(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	if d%time.Second == 0 {
		seconds := int(d / time.Second)
		if seconds%3600 == 0 && seconds >= 3600 {
			return fmt.Sprintf("%dh", seconds/3600)
		}
		if seconds%60 == 0 && seconds >= 60 {
			return fmt.Sprintf("%dm", seconds/60)
		}
		return fmt.Sprintf("%ds", seconds)
	}
	return fmt.Sprintf("%dms", d.Milliseconds())
}

func databaseURLWithStatementTimeout(databaseURL string, timeout time.Duration) (string, error) {
	if timeout <= 0 {
		return databaseURL, nil
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		return "", fmt.Errorf("source database URL is invalid")
	}
	if parsed.Scheme == "" {
		return "", fmt.Errorf("source database URL is invalid")
	}
	option := "statement_timeout=" + postgresStatementTimeoutValue(timeout)
	query := parsed.Query()
	existing := strings.TrimSpace(query.Get("options"))
	if existing != "" {
		query.Set("options", existing+" -c "+option)
	} else {
		query.Set("options", "-c "+option)
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func postgresStatementTimeoutValue(d time.Duration) string {
	if d%time.Second == 0 {
		seconds := int(d / time.Second)
		if seconds%3600 == 0 && seconds >= 3600 {
			return fmt.Sprintf("%dh", seconds/3600)
		}
		if seconds%60 == 0 && seconds >= 60 {
			return fmt.Sprintf("%dmin", seconds/60)
		}
		return fmt.Sprintf("%ds", seconds)
	}
	return fmt.Sprintf("%dms", d.Milliseconds())
}
