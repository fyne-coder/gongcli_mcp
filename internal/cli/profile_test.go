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

func TestProfileStagedImportHistoryActivateDiffAndSchema(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gong.db")
	store := openCLITestStore(t, dbPath)
	if err := store.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	dir := t.TempDir()
	firstProfile := filepath.Join(dir, "profile-one.yaml")
	secondProfile := filepath.Join(dir, "profile-two.yaml")
	if err := os.WriteFile(firstProfile, []byte(testProfileYAML("Profile One")), 0o600); err != nil {
		t.Fatalf("write first profile: %v", err)
	}
	if err := os.WriteFile(secondProfile, []byte(testProfileYAML("Profile Two")), 0o600); err != nil {
		t.Fatalf("write second profile: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"profile", "import", "--db", dbPath, "--profile", firstProfile}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("import active code=%d stderr=%q", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	code = Run(context.Background(), []string{"profile", "import", "--db", dbPath, "--profile", secondProfile, "--activate=false"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("staged import code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"activated": false`) {
		t.Fatalf("staged import response=%s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Run(context.Background(), []string{"profile", "history", "--db", dbPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("history code=%d stderr=%q", code, stderr.String())
	}
	var history struct {
		Profiles []struct {
			ProfileID       int64  `json:"profile_id"`
			Name            string `json:"name"`
			CanonicalSHA256 string `json:"canonical_sha256"`
			IsActive        bool   `json:"is_active"`
		} `json:"profiles"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &history); err != nil {
		t.Fatalf("unmarshal history: %v", err)
	}
	if len(history.Profiles) != 2 {
		t.Fatalf("profiles=%v want 2", history.Profiles)
	}
	activeName := ""
	for _, profile := range history.Profiles {
		if profile.IsActive {
			activeName = profile.Name
		}
	}
	if activeName != "Profile One" {
		t.Fatalf("unexpected staged history: %+v", history.Profiles)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run(context.Background(), []string{"profile", "diff", "--db", dbPath, "--from", "active", "--to", secondProfile}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("diff code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"changed": true`) || !strings.Contains(stdout.String(), `"name"`) {
		t.Fatalf("diff response=%s", stdout.String())
	}

	stagedSHA := ""
	for _, profile := range history.Profiles {
		if profile.Name == "Profile Two" {
			stagedSHA = profile.CanonicalSHA256
		}
	}
	if stagedSHA == "" {
		t.Fatalf("missing staged SHA in history: %+v", history.Profiles)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run(context.Background(), []string{"profile", "activate", "--db", dbPath, "--canonical-sha", stagedSHA[:12]}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("activate code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"activated": true`) {
		t.Fatalf("activate response=%s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Run(context.Background(), []string{"profile", "show", "--db", dbPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("show code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"name": "Profile Two"`) {
		t.Fatalf("show response=%s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Run(context.Background(), []string{"profile", "schema"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("schema code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"open"`) || !strings.Contains(stdout.String(), `"regex"`) {
		t.Fatalf("schema response=%s", stdout.String())
	}
}

func TestProfileHelpExitsZero(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := Run(context.Background(), []string{"profile", "--help"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "profile history") || !strings.Contains(stderr.String(), "profile schema") {
		t.Fatalf("help output missing new commands: %s", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Run(context.Background(), []string{"profile", "diff", "--help"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("profile diff help code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "-to") {
		t.Fatalf("profile diff help missing flags: %s", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Run(context.Background(), []string{"profile", "activate", "--help"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("profile activate help code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "-canonical-sha") {
		t.Fatalf("profile activate help missing flags: %s", stderr.String())
	}
}

func testProfileYAML(name string) string {
	return `version: 1
name: ` + name + `
lifecycle:
  open:
    label: Open
    order: 10
  closed_won:
    label: Closed Won
    order: 20
  closed_lost:
    label: Closed Lost
    order: 30
  post_sales:
    label: Post Sales
    order: 40
  unknown:
    label: Unknown
    order: 99
`
}
