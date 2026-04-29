package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
)

func TestRunRequiresDBFlag(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run(nil, bytes.NewReader(nil), &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code=%d want 2", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout not empty: %q", stdout.String())
	}
	if got := stderr.String(); got == "" || !bytes.Contains([]byte(got), []byte("--db is required")) {
		t.Fatalf("stderr=%q want missing --db message", got)
	}
}

func TestRunToolAllowlistEnvFiltersCatalog(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gong.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open returned error: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	t.Setenv("GONGMCP_TOOL_ALLOWLIST", "get_sync_status")
	stdin := strings.NewReader(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
	}, "\n") + "\n")
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"--db", dbPath}, stdin, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code=%d stderr=%q", code, stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, `"get_sync_status"`) || strings.Contains(got, `"search_calls"`) {
		t.Fatalf("stdout=%q did not reflect allowlist", got)
	}
}

func TestRunToolAllowlistFlagOverridesEnvAndRejectsUnknownTools(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gong.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open returned error: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	t.Setenv("GONGMCP_TOOL_ALLOWLIST", "get_sync_status")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"--db", dbPath, "--tool-allowlist", "does_not_exist"}, bytes.NewReader(nil), &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code=%d want 2", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout=%q want empty", stdout.String())
	}
	if got := stderr.String(); !strings.Contains(got, `unknown tool "does_not_exist"`) {
		t.Fatalf("stderr=%q missing unknown-tool error", got)
	}
}

func TestRunToolAllowlistFlagPrecedenceOverEnv(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gong.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open returned error: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	t.Setenv("GONGMCP_TOOL_ALLOWLIST", "search_calls")
	stdin := strings.NewReader(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
	}, "\n") + "\n")
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"--db", dbPath, "--tool-allowlist", "get_sync_status"}, stdin, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code=%d stderr=%q", code, stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, `"get_sync_status"`) || strings.Contains(got, `"search_calls"`) {
		t.Fatalf("stdout=%q did not prefer flag allowlist", got)
	}
}
