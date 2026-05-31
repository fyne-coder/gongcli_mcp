package export

import (
	"encoding/json"
	"fmt"
	"io"
)

var arrayKeys = []string{"calls", "users", "records", "transcripts", "callTranscripts", "items", "data"}

func WritePayloadAsJSONL(w io.Writer, body []byte) (int, error) {
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		return 0, err
	}

	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)

	switch value := payload.(type) {
	case []any:
		return writeArray(enc, value)
	case map[string]any:
		for _, key := range arrayKeys {
			if items, ok := value[key].([]any); ok {
				return writeArray(enc, items)
			}
		}
		if err := enc.Encode(value); err != nil {
			return 0, err
		}
		return 1, nil
	default:
		return 0, fmt.Errorf("payload is %T, want JSON object or array", payload)
	}
}

func writeArray(enc *json.Encoder, values []any) (int, error) {
	for _, value := range values {
		if err := enc.Encode(value); err != nil {
			return 0, err
		}
	}
	return len(values), nil
}
