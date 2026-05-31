package redact

import "testing"

func TestSecret(t *testing.T) {
	got := Secret("abcdef")
	if got != "ab**ef" {
		t.Fatalf("Secret = %q, want ab**ef", got)
	}
}

func TestTruncate(t *testing.T) {
	got := Truncate("abcdef", 4)
	if got != "a..." {
		t.Fatalf("Truncate = %q, want a...", got)
	}
}
