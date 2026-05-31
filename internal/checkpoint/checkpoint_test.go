package checkpoint

import (
	"path/filepath"
	"testing"
)

func TestStoreMarkAndOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checkpoint.jsonl")

	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	if store.Done("call-1") {
		t.Fatal("new store marked call-1 done")
	}

	if err := store.Mark(Entry{ID: "call-1", Status: "done", Path: "call-1.json"}); err != nil {
		t.Fatalf("Mark returned error: %v", err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen returned error: %v", err)
	}
	if !reopened.Done("call-1") {
		t.Fatal("reopened store did not mark call-1 done")
	}
}
