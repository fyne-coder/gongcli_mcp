package gong

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fyne-coder/gongcli_mcp/internal/auth"
)

func TestRawSetsBasicAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "key" || pass != "secret" {
			t.Fatalf("BasicAuth = %q/%q/%v", user, pass, ok)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client, err := NewClient(Options{
		BaseURL: server.URL,
		Credentials: auth.Credentials{
			AccessKey:       "key",
			AccessKeySecret: "secret",
		},
		MaxRetries: 1,
	})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}

	resp, err := client.Raw(context.Background(), "GET", "/v2/users", nil)
	if err != nil {
		t.Fatalf("Raw returned error: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

func TestNormalizeDateTime(t *testing.T) {
	got, err := normalizeDateTime("2026-04-24", false)
	if err != nil {
		t.Fatalf("normalizeDateTime returned error: %v", err)
	}
	if got != "2026-04-24T00:00:00-04:00" {
		t.Fatalf("normalizeDateTime = %q, want 2026-04-24T00:00:00-04:00", got)
	}
}

func TestListCallsIncludesExtendedContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("Decode returned error: %v", err)
		}
		contentSelector, ok := body["contentSelector"].(map[string]any)
		if !ok {
			t.Fatalf("contentSelector missing from request body: %#v", body)
		}
		if got := contentSelector["context"]; got != "Extended" {
			t.Fatalf("contentSelector.context = %q, want Extended", got)
		}
		exposedFields, ok := contentSelector["exposedFields"].(map[string]any)
		if !ok || exposedFields["parties"] != true {
			t.Fatalf("contentSelector.exposedFields = %#v, want parties=true", contentSelector["exposedFields"])
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"calls":[],"records":{"currentPageSize":0}}`))
	}))
	defer server.Close()

	client, err := NewClient(Options{
		BaseURL: server.URL,
		Credentials: auth.Credentials{
			AccessKey:       "key",
			AccessKeySecret: "secret",
		},
		MaxRetries: 1,
	})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}

	if _, err := client.ListCalls(context.Background(), CallListParams{Context: "Extended", ExposeParties: true}); err != nil {
		t.Fatalf("ListCalls returned error: %v", err)
	}
}

func TestRawRejectsCrossOriginAbsoluteURL(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()

	client, err := NewClient(Options{
		BaseURL: server.URL,
		Credentials: auth.Credentials{
			AccessKey:       "key",
			AccessKeySecret: "secret",
		},
		MaxRetries: 0,
	})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}

	_, err = client.Raw(context.Background(), "GET", "https://example.com/v2/users", nil)
	if err == nil {
		t.Fatal("Raw returned nil error, want rejection")
	}
	if !strings.Contains(err.Error(), "must match configured Gong origin") {
		t.Fatalf("error = %q, want same-origin rejection", err)
	}
}

func TestEndpointPreservesRelativeQueryString(t *testing.T) {
	client, err := NewClient(Options{
		BaseURL: "https://api.gong.test/v2",
		Credentials: auth.Credentials{
			AccessKey:       "key",
			AccessKeySecret: "secret",
		},
	})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}

	got, err := client.endpoint("/v2/users?cursor=users-2")
	if err != nil {
		t.Fatalf("endpoint returned error: %v", err)
	}
	if got != "https://api.gong.test/v2/users?cursor=users-2" {
		t.Fatalf("endpoint = %q, want query preserved", got)
	}
}

func TestRawAllowsSameOriginAbsoluteURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/users" {
			t.Fatalf("path = %q, want /v2/users", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client, err := NewClient(Options{
		BaseURL: server.URL,
		Credentials: auth.Credentials{
			AccessKey:       "key",
			AccessKeySecret: "secret",
		},
		MaxRetries: 0,
	})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}

	resp, err := client.Raw(context.Background(), "GET", server.URL+"/v2/users", nil)
	if err != nil {
		t.Fatalf("Raw returned error: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

func TestHTTPErrorRedactsBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"crm":"secret transcript payload"}`))
	}))
	defer server.Close()

	client, err := NewClient(Options{
		BaseURL: server.URL,
		Credentials: auth.Credentials{
			AccessKey:       "key",
			AccessKeySecret: "secret",
		},
		MaxRetries: 0,
	})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}

	_, err = client.Raw(context.Background(), "GET", "/v2/users", nil)
	if err == nil {
		t.Fatal("Raw returned nil error, want HTTPError")
	}
	if strings.Contains(err.Error(), "secret transcript payload") {
		t.Fatalf("error = %q, leaked response body", err)
	}
	if !strings.Contains(err.Error(), "response body redacted") {
		t.Fatalf("error = %q, want redaction marker", err)
	}
}

func TestNormalizeDateTimeEndOfDayHandlesDST(t *testing.T) {
	location := mustLoadLocation("America/New_York")

	got, err := normalizeDateTimeInLocation("2026-11-01", true, location)
	if err != nil {
		t.Fatalf("normalizeDateTimeInLocation returned error: %v", err)
	}
	if got != "2026-11-01T23:59:59-05:00" {
		t.Fatalf("normalizeDateTimeInLocation = %q, want 2026-11-01T23:59:59-05:00", got)
	}
}

func TestNormalizeDateTimeUsesCustomLocation(t *testing.T) {
	location := time.FixedZone("UTC+2", 2*60*60)

	got, err := normalizeDateTimeInLocation("2026-04-24", false, location)
	if err != nil {
		t.Fatalf("normalizeDateTimeInLocation returned error: %v", err)
	}
	if got != "2026-04-24T00:00:00+02:00" {
		t.Fatalf("normalizeDateTimeInLocation = %q, want 2026-04-24T00:00:00+02:00", got)
	}
}
