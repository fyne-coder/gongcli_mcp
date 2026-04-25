package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	checkpointstore "github.com/arthurlee/gongctl/internal/checkpoint"
)

func TestNewCLIHTTPClientTimeout(t *testing.T) {
	client := newCLIHTTPClient()
	if client.Timeout != defaultHTTPTimeout {
		t.Fatalf("Timeout = %s, want %s", client.Timeout, defaultHTTPTimeout)
	}
}

func TestWriteJSONFileAtomicKeepsExistingFileOnInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript.json")
	if err := os.WriteFile(path, []byte(`{"old":true}`), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	err := writeJSONFileAtomic(path, []byte(`{"bad"`))
	if err == nil {
		t.Fatal("writeJSONFileAtomic returned nil error, want invalid JSON failure")
	}

	body, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("ReadFile returned error: %v", readErr)
	}
	if string(body) != `{"old":true}` {
		t.Fatalf("body = %q, want original content preserved", body)
	}
}

func TestCallsTranscriptBatchResumeReprocessesInvalidExistingFile(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"callTranscripts":[{"callId":"call-1"}]}`))
	}))
	defer server.Close()

	t.Setenv("GONG_ACCESS_KEY", "key")
	t.Setenv("GONG_ACCESS_KEY_SECRET", "secret")
	t.Setenv("GONG_BASE_URL", server.URL)

	dir := t.TempDir()
	idsFile := filepath.Join(dir, "ids.txt")
	outDir := filepath.Join(dir, "out")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.WriteFile(idsFile, []byte("call-1\n"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	transcriptPath := filepath.Join(outDir, "call-1.json")
	if err := os.WriteFile(transcriptPath, []byte(`{"incomplete"`), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	store, err := checkpointstore.Open(filepath.Join(outDir, ".gongctl-checkpoint.jsonl"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	if err := store.Mark(checkpointstore.Entry{ID: "call-1", Status: "done", Path: transcriptPath}); err != nil {
		t.Fatalf("Mark returned error: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	a := &app{out: &stdout, err: &stderr}

	err = a.callsTranscriptBatch(context.Background(), []string{
		"--ids-file", idsFile,
		"--out-dir", outDir,
		"--resume",
	})
	if err != nil {
		t.Fatalf("callsTranscriptBatch returned error: %v", err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
	if strings.Contains(stderr.String(), "skip call-1") {
		t.Fatalf("stderr = %q, want reprocessing instead of skip", stderr.String())
	}

	body, readErr := os.ReadFile(transcriptPath)
	if readErr != nil {
		t.Fatalf("ReadFile returned error: %v", readErr)
	}
	if string(body) != `{"callTranscripts":[{"callId":"call-1"}]}` {
		t.Fatalf("body = %q, want refreshed valid transcript", body)
	}
}
