package governance

import (
	"testing"
)

func TestBlocklistGuardMatchesNormalizedNames(t *testing.T) {
	t.Parallel()

	guard := NewBlocklistGuard([]string{"Blocked Synthetic Corp", "  ", ""})
	if guard.Empty() {
		t.Fatal("guard with one term should not be empty")
	}
	if guard.TermCount() != 1 {
		t.Fatalf("TermCount=%d want 1", guard.TermCount())
	}
	if !guard.MatchValue("blocked synthetic corp") {
		t.Fatal("guard must match normalized lowercase form")
	}
	if !guard.MatchValue("  Blocked   Synthetic   Corp ") {
		t.Fatal("guard must match values with extra whitespace and casing")
	}
	if !guard.MatchValue("Account: BLOCKED SYNTHETIC CORP, region: NA") {
		t.Fatal("guard must match values containing the blocklisted name as a whole-word substring")
	}
	if guard.MatchValue("Allowed Synthetic Co") {
		t.Fatal("guard must not match unrelated names")
	}
	if guard.MatchValue("") {
		t.Fatal("guard must not match empty string")
	}
}

func TestBlocklistGuardEmptyNeverMatches(t *testing.T) {
	t.Parallel()

	var guard *BlocklistGuard
	if !guard.Empty() {
		t.Fatal("nil guard must report empty")
	}
	if guard.MatchValue("anything") {
		t.Fatal("nil guard must not match any value")
	}
	empty := NewBlocklistGuard(nil)
	if !empty.Empty() {
		t.Fatal("guard built from nil input must be empty")
	}
	if empty.MatchValue("blocked synthetic corp") {
		t.Fatal("empty guard must not match")
	}
}

func TestBlocklistGuardMatchAny(t *testing.T) {
	t.Parallel()

	guard := NewBlocklistGuard([]string{"Blocked Synthetic Corp"})
	if guard.MatchAny(nil) {
		t.Fatal("MatchAny(nil) should be false")
	}
	if guard.MatchAny([]string{"safe co", "blocked synthetic corp"}) != true {
		t.Fatal("MatchAny must return true when any value matches")
	}
	if guard.MatchAny([]string{"safe co", "another safe co"}) {
		t.Fatal("MatchAny must return false when no value matches")
	}
}

func TestNewBlocklistGuardFromConfigUsesConfigTargets(t *testing.T) {
	t.Parallel()

	cfg, err := ParseYAML([]byte(`
version: 1
lists:
  no_ai:
    customers:
      - name: "Blocked Synthetic Corp"
        aliases: ["BSC Inc"]
`))
	if err != nil {
		t.Fatalf("ParseYAML returned error: %v", err)
	}
	guard := NewBlocklistGuardFromConfig(cfg)
	if guard.Empty() {
		t.Fatal("guard from config must not be empty")
	}
	if !guard.MatchValue("blocked synthetic corp") {
		t.Fatal("guard must match primary name")
	}
	if !guard.MatchValue("bsc inc") {
		t.Fatal("guard must match alias")
	}
}
