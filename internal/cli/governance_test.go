package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGovernanceAuditReportsMatchedAndUnmatchedSyntheticNames(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "gong.db")
	store := openCLITestStore(t, dbPath)

	if _, err := store.UpsertCall(context.Background(), mustMarshalJSON(t, map[string]any{
		"id":       "call-governance-cli-001",
		"title":    "Governance CLI call",
		"started":  "2026-04-24T12:00:00Z",
		"duration": 1200,
		"context": []any{
			map[string]any{
				"objectType": "Account",
				"id":         "acct-governance-cli-001",
				"name":       "Audit Synthetic Corp",
				"fields": []any{
					map[string]any{"name": "Name", "value": "Audit Synthetic Corp"},
				},
			},
		},
	})); err != nil {
		t.Fatalf("upsert governance call: %v", err)
	}
	store.Close()

	configPath := filepath.Join(dir, "ai-governance.yaml")
	if err := os.WriteFile(configPath, []byte(`
version: 1
lists:
  no_ai:
    customers:
      - name: "Audit Synthetic Corp"
  notification_required:
    customers:
      - name: "Missing Synthetic Corp"
        aliases: ["Missing Synthetic Alias"]
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"governance", "audit", "--db", dbPath, "--config", configPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run(governance audit) code=%d stderr=%q", code, stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{
		"matched=1",
		"unmatched=2",
		"suppressed_calls=1",
		"Audit Synthetic Corp",
		"Missing Synthetic Corp",
		"Missing Synthetic Alias",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("audit output missing %q: %s", want, output)
		}
	}
}

func TestGovernanceExportFilteredDBWritesPhysicalMCPDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "gong.db")
	outPath := filepath.Join(dir, "gong-governed.db")
	store := openCLITestStore(t, dbPath)

	if _, err := store.UpsertCall(context.Background(), mustMarshalJSON(t, map[string]any{
		"id":       "call-governance-export-cli-blocked",
		"title":    "Governance export CLI blocked",
		"started":  "2026-04-24T12:00:00Z",
		"duration": 1200,
		"context": []any{
			map[string]any{
				"objectType": "Account",
				"id":         "acct-governance-export-cli-blocked",
				"name":       "Export Synthetic Corp",
				"fields": []any{
					map[string]any{"name": "Name", "value": "Export Synthetic Corp"},
				},
			},
		},
	})); err != nil {
		t.Fatalf("upsert blocked governance call: %v", err)
	}
	if _, err := store.UpsertCall(context.Background(), mustMarshalJSON(t, map[string]any{
		"id":       "call-governance-export-cli-allowed",
		"title":    "Governance export CLI allowed",
		"started":  "2026-04-24T12:30:00Z",
		"duration": 900,
		"context": []any{
			map[string]any{
				"objectType": "Account",
				"id":         "acct-governance-export-cli-allowed",
				"name":       "Allowed Synthetic Corp",
				"fields": []any{
					map[string]any{"name": "Name", "value": "Allowed Synthetic Corp"},
				},
			},
		},
	})); err != nil {
		t.Fatalf("upsert allowed governance call: %v", err)
	}
	if _, err := store.UpsertCall(context.Background(), mustMarshalJSON(t, map[string]any{
		"id":      "call-governance-export-cli-email",
		"title":   "Governance export CLI email domain",
		"started": "2026-04-24T13:00:00Z",
		"parties": []any{
			map[string]any{"speakerId": "buyer-email", "emailAddress": "buyer@exportsynthetic.example"},
		},
		"context": []any{
			map[string]any{
				"objectType": "Account",
				"id":         "acct-governance-export-cli-email",
				"name":       "Allowed Email Corp",
				"fields": []any{
					map[string]any{"name": "Name", "value": "Allowed Email Corp"},
				},
			},
		},
	})); err != nil {
		t.Fatalf("upsert email governance call: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	configPath := filepath.Join(dir, "ai-governance.yaml")
	if err := os.WriteFile(configPath, []byte(`
version: 1
lists:
  no_ai:
    customers:
      - name: "Export Synthetic Corp"
        aliases: ["exportsynthetic.example"]
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"governance", "export-filtered-db", "--db", dbPath, "--config", configPath, "--out", outPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run(governance export-filtered-db) code=%d stderr=%q", code, stderr.String())
	}
	var response struct {
		SuppressedCallCount           int   `json:"suppressed_call_count"`
		DeletedCalls                  int64 `json:"deleted_calls"`
		RemainingSuppressedCandidates int64 `json:"remaining_suppressed_candidates"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("decode export response: %v", err)
	}
	if response.SuppressedCallCount != 2 || response.DeletedCalls != 2 || response.RemainingSuppressedCandidates != 0 {
		t.Fatalf("unexpected export response: %+v stdout=%s", response, stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Run(context.Background(), []string{"governance", "audit", "--db", outPath, "--config", configPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run(governance audit filtered) code=%d stderr=%q", code, stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, "matched=0") || !strings.Contains(got, "suppressed_calls=0") {
		t.Fatalf("filtered audit should not find blocked rows: %s", got)
	}
}
