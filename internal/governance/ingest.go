package governance

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type IngestDecision struct {
	CallID         string
	Skip           bool
	MatchedLists   []string
	SourceCategory string
}

func EvaluateCallPayload(raw json.RawMessage, cfg *Config) (IngestDecision, error) {
	if cfg == nil {
		return IngestDecision{}, nil
	}
	callID, err := callIDFromRaw(raw)
	if err != nil {
		return IngestDecision{}, err
	}
	candidates := []Candidate{{
		CallID: callID,
		Source: "call_payload",
		Value:  string(raw),
	}}
	audit := AuditCandidates(candidates, cfg)
	if audit.SuppressedCallCount == 0 {
		return IngestDecision{CallID: callID}, nil
	}
	lists := map[string]struct{}{}
	for _, match := range audit.MatchedEntries {
		lists[match.List] = struct{}{}
	}
	out := IngestDecision{
		CallID:         callID,
		Skip:           true,
		MatchedLists:   make([]string, 0, len(lists)),
		SourceCategory: "call_payload",
	}
	for list := range lists {
		out.MatchedLists = append(out.MatchedLists, list)
	}
	sort.Strings(out.MatchedLists)
	return out, nil
}

func RuleHash(configFingerprint string, matchedLists []string) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(strings.TrimSpace(configFingerprint)))
	for _, list := range matchedLists {
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(strings.TrimSpace(list)))
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}

func callIDFromRaw(raw json.RawMessage) (string, error) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", err
	}
	if id := firstRawString(payload, "id", "callId", "call_id"); id != "" {
		return id, nil
	}
	var metaData map[string]json.RawMessage
	if value, ok := payload["metaData"]; ok {
		if err := json.Unmarshal(value, &metaData); err == nil {
			if id := firstRawString(metaData, "id", "callId", "call_id"); id != "" {
				return id, nil
			}
		}
	}
	return "", fmt.Errorf("call payload missing id")
}

func firstRawString(payload map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		if value, ok := payload[key]; ok {
			var id string
			if err := json.Unmarshal(value, &id); err == nil && strings.TrimSpace(id) != "" {
				return strings.TrimSpace(id)
			}
		}
	}
	return ""
}
