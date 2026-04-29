package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunRestrictedFlagBlocksAPIRaw(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := Run(context.Background(), []string{"--restricted", "api", "raw", "GET", "/v2/users"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("Run(api raw) code=%d stderr=%q", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout=%q want empty", stdout.String())
	}
	if got := stderr.String(); !strings.Contains(got, "api raw is blocked because restricted mode is enabled") || !strings.Contains(got, "--allow-sensitive-export") {
		t.Fatalf("stderr=%q missing restricted-mode guidance", got)
	}
}

func TestRunAllowSensitiveExportEnvOverridesRestrictedAPIRaw(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/users" {
			t.Fatalf("path=%q want /v2/users", r.URL.Path)
		}
		writeCLIJSON(t, w, map[string]any{"users": []map[string]any{{"id": "user-001"}}})
	}))
	defer server.Close()

	setTestEnv(t, server.URL)
	t.Setenv(restrictedEnvVar, "1")
	t.Setenv(allowSensitiveExportEnvVar, "1")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"api", "raw", "GET", "/v2/users"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run(api raw) code=%d stderr=%q", code, stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, `"id":"user-001"`) {
		t.Fatalf("stdout=%q missing API response", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr=%q want empty", stderr.String())
	}
}

func TestCallsRestrictedModeBlocksSensitiveCommands(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "gong.db")
	outDir := filepath.Join(dir, "transcripts")
	outFile := filepath.Join(dir, "calls.jsonl")

	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "list-extended",
			args: []string{"list", "--from", "2026-04-01", "--to", "2026-04-02", "--context", "extended"},
			want: "calls list --context extended is blocked because restricted mode is enabled",
		},
		{
			name: "export",
			args: []string{"export", "--from", "2026-04-01", "--to", "2026-04-02", "--out", outFile},
			want: "calls export is blocked because restricted mode is enabled",
		},
		{
			name: "show-json",
			args: []string{"show", "--db", dbPath, "--call-id", "call-001", "--json"},
			want: "calls show --json is blocked because restricted mode is enabled",
		},
		{
			name: "transcript",
			args: []string{"transcript", "--call-id", "call-001"},
			want: "calls transcript is blocked because restricted mode is enabled",
		},
		{
			name: "transcript-batch",
			args: []string{"transcript-batch", "--ids-file", filepath.Join(dir, "call_ids.txt"), "--out-dir", outDir},
			want: "calls transcript-batch is blocked because restricted mode is enabled",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			a := &app{out: &stdout, err: &stderr, restricted: true}

			err := a.calls(context.Background(), tc.args)
			if err == nil {
				t.Fatal("calls returned nil error, want restricted-mode failure")
			}
			if got := err.Error(); !strings.Contains(got, tc.want) || !strings.Contains(got, "--allow-sensitive-export") {
				t.Fatalf("err=%q missing restricted-mode guidance", got)
			}
		})
	}
}

func TestCallsListExtendedAllowSensitiveExportFlagOverridesRestrictedMode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/calls/extensive" {
			t.Fatalf("path=%q want /v2/calls/extensive", r.URL.Path)
		}
		writeCLIJSON(t, w, map[string]any{"calls": []map[string]any{{"id": "call-001"}}})
	}))
	defer server.Close()

	setTestEnv(t, server.URL)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	a := &app{out: &stdout, err: &stderr, restricted: true}
	err := a.calls(context.Background(), []string{"list", "--from", "2026-04-01", "--to", "2026-04-02", "--context", "extended", "--allow-sensitive-export"})
	if err != nil {
		t.Fatalf("calls list returned error: %v", err)
	}
	if got := stdout.String(); !strings.Contains(got, `"id":"call-001"`) {
		t.Fatalf("stdout=%q missing calls payload", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr=%q want empty", stderr.String())
	}
}

func TestCallsTranscriptAllowSensitiveExportFlagOverridesRestrictedMode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/calls/transcript" {
			t.Fatalf("path=%q want /v2/calls/transcript", r.URL.Path)
		}
		writeCLIJSON(t, w, map[string]any{
			"callTranscripts": []map[string]any{
				{"callId": "call-001"},
			},
		})
	}))
	defer server.Close()

	setTestEnv(t, server.URL)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	a := &app{out: &stdout, err: &stderr, restricted: true}
	err := a.calls(context.Background(), []string{"transcript", "--call-id", "call-001", "--allow-sensitive-export"})
	if err != nil {
		t.Fatalf("calls transcript returned error: %v", err)
	}
	if got := stdout.String(); !strings.Contains(got, `"callId":"call-001"`) {
		t.Fatalf("stdout=%q missing transcript payload", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr=%q want empty", stderr.String())
	}
}
