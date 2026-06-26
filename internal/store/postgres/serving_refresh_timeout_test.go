package postgres

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestParseRefreshStatementTimeoutAcceptsValidDurations(t *testing.T) {
	got, err := ParseRefreshStatementTimeout("45m")
	if err != nil {
		t.Fatalf("ParseRefreshStatementTimeout: %v", err)
	}
	if got != 45*time.Minute {
		t.Fatalf("timeout=%s want 45m", got)
	}
}

func TestParseRefreshStatementTimeoutRejectsInvalidAndZero(t *testing.T) {
	for _, raw := range []string{"", "not-a-duration", "0", "0s", "disabled", "-5m", "500us"} {
		if _, err := ParseRefreshStatementTimeout(raw); err == nil {
			t.Fatalf("ParseRefreshStatementTimeout(%q) expected error", raw)
		}
	}
}

func TestDatabaseURLWithStatementTimeoutAppliesStartupOption(t *testing.T) {
	base := "postgres://operator:source-secret@source.internal:5432/gongctl_source?sslmode=require"
	got, err := databaseURLWithStatementTimeout(base, 30*time.Minute)
	if err != nil {
		t.Fatalf("databaseURLWithStatementTimeout: %v", err)
	}
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse result URL: %v", err)
	}
	options := parsed.Query().Get("options")
	if !strings.Contains(options, "statement_timeout=30min") {
		t.Fatalf("options=%q want statement_timeout startup option", options)
	}
	if parsed.Query().Get("sslmode") != "require" {
		t.Fatalf("sslmode was dropped from URL")
	}
	for _, leak := range []string{"source-secret", "source.internal"} {
		if strings.Contains(options, leak) {
			t.Fatalf("options leaked %q", leak)
		}
	}
}

func TestDatabaseURLWithStatementTimeoutMergesExistingOptions(t *testing.T) {
	base := "postgres://operator:secret@host:5432/db?options=-c%20search_path%3Dpublic"
	got, err := databaseURLWithStatementTimeout(base, 90*time.Second)
	if err != nil {
		t.Fatalf("databaseURLWithStatementTimeout: %v", err)
	}
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse result URL: %v", err)
	}
	options := parsed.Query().Get("options")
	if !strings.Contains(options, "search_path=public") || !strings.Contains(options, "statement_timeout=90s") {
		t.Fatalf("options=%q want merged startup options", options)
	}
}

func TestFormatRefreshStatementTimeout(t *testing.T) {
	cases := map[string]time.Duration{
		"30m":   30 * time.Minute,
		"90s":   90 * time.Second,
		"2h":    2 * time.Hour,
		"500ms": 500 * time.Millisecond,
	}
	for want, d := range cases {
		if got := FormatRefreshStatementTimeout(d); got != want {
			t.Fatalf("FormatRefreshStatementTimeout(%s)=%q want %q", d, got, want)
		}
	}
}
