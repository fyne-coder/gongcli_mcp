package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/fyne-coder/gongcli_mcp/internal/mcp"
)

func TestInstallClaudeStdioMCPScriptDefaultPresetEnvAndDockerIsolation(t *testing.T) {
	entry := runInstallClaudeStdioMCPScript(t)

	if entry.Command != "docker" {
		t.Fatalf("command=%q want docker", entry.Command)
	}
	assertArgPair(t, entry.Args, "--network", "none")
	assertArgValue(t, entry.Args, "-v", func(value string) bool {
		return strings.HasSuffix(value, ":/data:ro") && strings.Contains(value, filepath.Clean(os.TempDir()))
	})
	assertArgPair(t, entry.Args, "-e", "GONGMCP_TOOL_PRESET=business-pilot")
	assertNoEnvWithPrefix(t, entry.Args, "GONGMCP_TOOL_ALLOWLIST=")
	assertArgPair(t, entry.Args, "--db", "/data/gong.db")
}

func TestInstallClaudeStdioMCPScriptCompatExpandedAllowlistUsesPresetCatalog(t *testing.T) {
	entry := runInstallClaudeStdioMCPScript(t, "--tool-preset", "analyst", "--compat-expanded-allowlist")

	wantTools, err := mcp.ExpandToolPreset("analyst")
	if err != nil {
		t.Fatalf("ExpandToolPreset returned error: %v", err)
	}
	assertArgPair(t, entry.Args, "-e", "GONGMCP_TOOL_ALLOWLIST="+strings.Join(wantTools, ","))
	assertNoEnvWithPrefix(t, entry.Args, "GONGMCP_TOOL_PRESET=")
}

func TestInstallClaudeStdioMCPScriptCompatExpandedAllowlistAcceptsCatalogOnlyPreset(t *testing.T) {
	helperPath := writePresetCatalogHelper(t, `{"presets":[{"name":"new-preset","tools":["get_sync_status","rank_transcript_backlog"],"tool_count":2}]}`)
	stdout, stderr, code := runInstallClaudeStdioMCPScriptRaw(t, "--preset-catalog-bin", helperPath, "--tool-preset", "new-preset", "--compat-expanded-allowlist")
	if code != 0 {
		t.Fatalf("script exit code=%d stderr=%q", code, stderr)
	}
	entry := parseClaudeMCPEntry(t, stdout)
	assertArgPair(t, entry.Args, "-e", "GONGMCP_TOOL_ALLOWLIST=get_sync_status,rank_transcript_backlog")
	assertNoEnvWithPrefix(t, entry.Args, "GONGMCP_TOOL_PRESET=")
}

func TestInstallClaudeStdioMCPScriptRejectsMutuallyExclusiveToolSelection(t *testing.T) {
	_, stderr, code := runInstallClaudeStdioMCPScriptRaw(t, "--tool-preset", "business-pilot", "--tool-allowlist", "get_sync_status")
	if code != 2 {
		t.Fatalf("exit code=%d stderr=%q want 2", code, stderr)
	}
	if !strings.Contains(stderr, "--tool-preset and --tool-allowlist are mutually exclusive") {
		t.Fatalf("stderr=%q missing mutual exclusivity error", stderr)
	}
}

func TestInstallClaudeStdioMCPScriptRejectsInvalidPreset(t *testing.T) {
	_, stderr, code := runInstallClaudeStdioMCPScriptRaw(t, "--tool-preset", "not-a-preset")
	if code != 2 {
		t.Fatalf("exit code=%d stderr=%q want 2", code, stderr)
	}
	if !strings.Contains(stderr, "unknown tool preset: not-a-preset") {
		t.Fatalf("stderr=%q missing invalid preset error", stderr)
	}
}

func TestInstallClaudeStdioMCPScriptRejectsCompatWithCustomAllowlist(t *testing.T) {
	_, stderr, code := runInstallClaudeStdioMCPScriptRaw(t, "--tool-allowlist", "get_sync_status", "--compat-expanded-allowlist")
	if code != 2 {
		t.Fatalf("exit code=%d stderr=%q want 2", code, stderr)
	}
	if !strings.Contains(stderr, "--compat-expanded-allowlist and --tool-allowlist are mutually exclusive") {
		t.Fatalf("stderr=%q missing compat mutual exclusivity error", stderr)
	}
}

func TestInstallClaudeStdioMCPScriptRejectsEscapedDataDir(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	otherDir := filepath.Join(root, "other")
	if err := os.Mkdir(dataDir, 0o755); err != nil {
		t.Fatalf("mkdir data dir: %v", err)
	}
	if err := os.Mkdir(otherDir, 0o755); err != nil {
		t.Fatalf("mkdir other dir: %v", err)
	}
	dbPath := filepath.Join(otherDir, "gong.db")
	if err := os.WriteFile(dbPath, []byte("sqlite placeholder"), 0o600); err != nil {
		t.Fatalf("write escaped db: %v", err)
	}

	_, stderr, code := runInstallClaudeStdioMCPScriptRaw(t, "--db", filepath.Join(dataDir, "..", "other", "gong.db"), "--data-dir", dataDir)
	if code != 1 {
		t.Fatalf("exit code=%d stderr=%q want 1", code, stderr)
	}
	if !strings.Contains(stderr, "database must be inside --data-dir") {
		t.Fatalf("stderr=%q missing data-dir containment error", stderr)
	}
}

func TestInstallClaudeStdioMCPScriptRejectsEscapedDataDirSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink containment regression is Unix-specific")
	}
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	otherDir := filepath.Join(root, "other")
	if err := os.Mkdir(dataDir, 0o755); err != nil {
		t.Fatalf("mkdir data dir: %v", err)
	}
	if err := os.Mkdir(otherDir, 0o755); err != nil {
		t.Fatalf("mkdir other dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(otherDir, "gong.db"), []byte("sqlite placeholder"), 0o600); err != nil {
		t.Fatalf("write escaped db: %v", err)
	}
	linkPath := filepath.Join(dataDir, "gong.db")
	if err := os.Symlink(filepath.Join("..", "other", "gong.db"), linkPath); err != nil {
		t.Fatalf("create db symlink: %v", err)
	}

	_, stderr, code := runInstallClaudeStdioMCPScriptRaw(t, "--db", linkPath, "--data-dir", dataDir)
	if code != 1 {
		t.Fatalf("exit code=%d stderr=%q want 1", code, stderr)
	}
	if !strings.Contains(stderr, "database must be inside --data-dir") {
		t.Fatalf("stderr=%q missing data-dir containment error", stderr)
	}
}

func TestInstallClaudeStdioMCPScriptAvoidsAmbientCatalogExecution(t *testing.T) {
	script, err := os.ReadFile(filepath.Join(testRepoRoot(t), "scripts", "install-claude-stdio-mcp.sh"))
	if err != nil {
		t.Fatalf("read installer script: %v", err)
	}
	for _, forbidden := range []string{"GONGMCP_BIN", "go run ./cmd/gongmcp"} {
		if strings.Contains(string(script), forbidden) {
			t.Fatalf("installer script contains forbidden ambient catalog execution path %q", forbidden)
		}
	}
}

func TestInstallClaudeStdioMCPScriptCatalogHelper(t *testing.T) {
	if os.Getenv("GONGMCP_PRESET_CATALOG_HELPER") != "1" {
		return
	}
	if err := mcp.WriteToolPresetCatalog(os.Stdout); err != nil {
		t.Fatalf("WriteToolPresetCatalog returned error: %v", err)
	}
	os.Exit(0)
}

type claudeMCPEntry struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

func runInstallClaudeStdioMCPScript(t *testing.T, args ...string) claudeMCPEntry {
	t.Helper()

	stdout, stderr, code := runInstallClaudeStdioMCPScriptRaw(t, args...)
	if code != 0 {
		t.Fatalf("script exit code=%d stderr=%q", code, stderr)
	}
	return parseClaudeMCPEntry(t, stdout)
}

func parseClaudeMCPEntry(t *testing.T, stdout string) claudeMCPEntry {
	t.Helper()
	var config struct {
		MCPServers map[string]claudeMCPEntry `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(stdout), &config); err != nil {
		t.Fatalf("unmarshal script JSON: %v\nstdout=%s", err, stdout)
	}
	entry, ok := config.MCPServers["gong"]
	if !ok {
		t.Fatalf("missing gong server entry in %s", stdout)
	}
	return entry
}

func runInstallClaudeStdioMCPScriptRaw(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	return runInstallClaudeStdioMCPScriptRawWithEnv(t, nil, args...)
}

func runInstallClaudeStdioMCPScriptRawWithEnv(t *testing.T, extraEnv []string, args ...string) (string, string, int) {
	t.Helper()
	requireInstallScriptTools(t)

	repoRoot := testRepoRoot(t)
	scriptPath := filepath.Join(repoRoot, "scripts", "install-claude-stdio-mcp.sh")
	dbPath := testDBPath(t)
	cmdArgs := append([]string{scriptPath, "--db", dbPath}, args...)
	if !hasArg(args, "--preset-catalog-bin") {
		cmdArgs = append(cmdArgs, "--preset-catalog-bin", defaultPresetCatalogHelper(t))
	}
	cmd := exec.Command("bash", cmdArgs...)
	cmd.Dir = repoRoot
	cmd.Env = appendEnvOverrides(installScriptTestEnv(t), extraEnv)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.String(), stderr.String(), 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return stdout.String(), stderr.String(), exitErr.ExitCode()
	}
	t.Fatalf("run script: %v stderr=%q", err, stderr.String())
	return "", "", 1
}

func hasArg(args []string, name string) bool {
	for _, arg := range args {
		if arg == name {
			return true
		}
	}
	return false
}

func writePresetCatalogHelper(t *testing.T, catalog string) string {
	t.Helper()
	helperPath := filepath.Join(t.TempDir(), "gongmcp-catalog-helper")
	body := "#!/usr/bin/env bash\ncat <<'JSON'\n" + catalog + "\nJSON\n"
	if err := os.WriteFile(helperPath, []byte(body), 0o700); err != nil {
		t.Fatalf("write catalog helper: %v", err)
	}
	return helperPath
}

func defaultPresetCatalogHelper(t *testing.T) string {
	t.Helper()
	helperPath := filepath.Join(t.TempDir(), "gongmcp-catalog-helper")
	helper := "#!/usr/bin/env bash\nGONGMCP_PRESET_CATALOG_HELPER=1 exec " + shellQuote(os.Args[0]) + " -test.run '^TestInstallClaudeStdioMCPScriptCatalogHelper$' -- \"$@\"\n"
	if err := os.WriteFile(helperPath, []byte(helper), 0o700); err != nil {
		t.Fatalf("write catalog helper: %v", err)
	}
	return helperPath
}

func appendEnvOverrides(env []string, overrides []string) []string {
	out := append([]string{}, env...)
	for _, override := range overrides {
		key, _, ok := strings.Cut(override, "=")
		if !ok {
			out = append(out, override)
			continue
		}
		prefix := key + "="
		filtered := out[:0]
		for _, value := range out {
			if !strings.HasPrefix(value, prefix) {
				filtered = append(filtered, value)
			}
		}
		out = append(filtered, override)
	}
	return out
}

func testRepoRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}

func requireInstallScriptTools(t *testing.T) {
	t.Helper()
	for _, name := range []string{"bash", "jq"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Skipf("%s is required for install script tests: %v", name, err)
		}
	}
}

func installScriptTestEnv(t *testing.T) []string {
	t.Helper()
	env := make([]string, 0, len(os.Environ())+2)
	for _, value := range os.Environ() {
		if strings.HasPrefix(value, "GONGMCP_BIN=") {
			continue
		}
		env = append(env, value)
	}
	env = append(env, "GOCACHE="+filepath.Join(t.TempDir(), "gocache"))
	return env
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func testDBPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "gong.db")
	if err := os.WriteFile(dbPath, []byte("sqlite placeholder"), 0o600); err != nil {
		t.Fatalf("write db placeholder: %v", err)
	}
	return dbPath
}

func assertArgPair(t *testing.T, args []string, key, value string) {
	t.Helper()
	for idx := 0; idx < len(args)-1; idx++ {
		if args[idx] == key && args[idx+1] == value {
			return
		}
	}
	t.Fatalf("args=%v missing adjacent pair %q %q", args, key, value)
}

func assertArgValue(t *testing.T, args []string, key string, accept func(string) bool) {
	t.Helper()
	for idx := 0; idx < len(args)-1; idx++ {
		if args[idx] == key && accept(args[idx+1]) {
			return
		}
	}
	t.Fatalf("args=%v missing acceptable value for %q", args, key)
}

func assertNoEnvWithPrefix(t *testing.T, args []string, prefix string) {
	t.Helper()
	for _, arg := range args {
		if strings.HasPrefix(arg, prefix) {
			t.Fatalf("args=%v unexpectedly contained env prefix %q", args, prefix)
		}
	}
}
