package export

import (
	"bytes"
	"testing"
)

func TestWritePayloadAsJSONLArrayField(t *testing.T) {
	var out bytes.Buffer
	count, err := WritePayloadAsJSONL(&out, []byte(`{"calls":[{"id":"1"},{"id":"2"}]}`))
	if err != nil {
		t.Fatalf("WritePayloadAsJSONL returned error: %v", err)
	}
	if count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}
	if out.String() != "{\"id\":\"1\"}\n{\"id\":\"2\"}\n" {
		t.Fatalf("output = %q", out.String())
	}
}
