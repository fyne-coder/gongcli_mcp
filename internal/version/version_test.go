package version

import "testing"

func TestCurrentUsesInjectedVersionFields(t *testing.T) {
	oldVersion := Version
	oldCommit := Commit
	oldDate := Date
	t.Cleanup(func() {
		Version = oldVersion
		Commit = oldCommit
		Date = oldDate
	})

	Version = "1.2.3"
	Commit = "abcdef1"
	Date = "2026-04-25T00:00:00Z"

	info := Current()
	if info.Version != "1.2.3" || info.Commit != "abcdef1" || info.Date != "2026-04-25T00:00:00Z" {
		t.Fatalf("Current()=%+v", info)
	}
}
