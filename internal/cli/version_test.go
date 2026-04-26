package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestVersionCommandPrintsBuildMetadata(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := Run(context.Background(), []string{"version"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run(version) code=%d stderr=%q", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"version:", "commit:", "date:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("version output %q missing %q", out, want)
		}
	}
}
