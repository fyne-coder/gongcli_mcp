package auth

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFromEnv(t *testing.T) {
	t.Setenv("GONG_ACCESS_KEY", "key")
	t.Setenv("GONG_ACCESS_KEY_SECRET", "secret")

	creds, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv returned error: %v", err)
	}
	if creds.AccessKey != "key" {
		t.Fatalf("AccessKey = %q, want key", creds.AccessKey)
	}
	if creds.AccessKeySecret != "secret" {
		t.Fatalf("AccessKeySecret = %q, want secret", creds.AccessKeySecret)
	}
}

func TestLoadFromEnvMissing(t *testing.T) {
	t.Setenv("GONG_ACCESS_KEY", "")
	t.Setenv("GONG_ACCESS_KEY_SECRET", "secret")

	_, err := LoadFromEnv()
	if err == nil {
		t.Fatal("LoadFromEnv returned nil error for missing key")
	}
}

func TestLoadFromDotEnv(t *testing.T) {
	chdir(t, t.TempDir())
	t.Setenv("GONG_ACCESS_KEY", "")
	t.Setenv("GONG_ACCESS_KEY_SECRET", "")
	t.Setenv("GONG_BASE_URL", "")
	writeFile(t, ".env", `GONG_ACCESS_KEY=file-key
GONG_ACCESS_KEY_SECRET='file-secret'
GONG_BASE_URL="https://example.gong.test"
`)

	creds, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv returned error: %v", err)
	}
	if creds.AccessKey != "file-key" {
		t.Fatalf("AccessKey = %q, want file-key", creds.AccessKey)
	}
	if creds.AccessKeySecret != "file-secret" {
		t.Fatalf("AccessKeySecret = %q, want file-secret", creds.AccessKeySecret)
	}
	if got := os.Getenv("GONG_BASE_URL"); got != "https://example.gong.test" {
		t.Fatalf("GONG_BASE_URL = %q, want https://example.gong.test", got)
	}
}

func TestEnvOverridesDotEnv(t *testing.T) {
	chdir(t, t.TempDir())
	t.Setenv("GONG_ACCESS_KEY", "env-key")
	t.Setenv("GONG_ACCESS_KEY_SECRET", "env-secret")
	writeFile(t, ".env", `GONG_ACCESS_KEY=file-key
GONG_ACCESS_KEY_SECRET=file-secret
`)

	creds, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv returned error: %v", err)
	}
	if creds.AccessKey != "env-key" {
		t.Fatalf("AccessKey = %q, want env-key", creds.AccessKey)
	}
	if creds.AccessKeySecret != "env-secret" {
		t.Fatalf("AccessKeySecret = %q, want env-secret", creds.AccessKeySecret)
	}
}

func TestReadDotEnvRejectsMalformedLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	writeFile(t, path, "GONG_ACCESS_KEY\n")

	if _, err := ReadDotEnv(path); err == nil {
		t.Fatal("ReadDotEnv returned nil error for malformed line")
	}
}

func chdir(t *testing.T, dir string) {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd returned error: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir returned error: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(wd); err != nil {
			t.Fatalf("restore Chdir returned error: %v", err)
		}
	})
}

func writeFile(t *testing.T, path string, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
}
