package governance

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestParseYAMLNormalizesListsAndAliases(t *testing.T) {
	cfg, err := ParseYAML([]byte(`
version: 1
lists:
  no_ai:
    customers:
      - name: "Acme, Inc."
        aliases: ["ACME"]
  notification_required:
    customers:
      - name: "Globex LLC"
        aliases: ["Globex Corporation"]
`))
	if err != nil {
		t.Fatalf("ParseYAML returned error: %v", err)
	}
	targets := cfg.Targets()
	got := map[string]Target{}
	for _, target := range targets {
		got[target.List+"|"+target.Normalized] = target
	}
	if got[ListNoAI+"|acme inc"].Name != "Acme, Inc." {
		t.Fatalf("missing normalized Acme target: %+v", got)
	}
	if got[ListNoAI+"|acme"].Alias != "ACME" {
		t.Fatalf("missing normalized Acme alias: %+v", got)
	}
	if got[ListNotificationRequired+"|globex corporation"].Alias != "Globex Corporation" {
		t.Fatalf("missing notification alias: %+v", got)
	}
}

func TestAuditCandidatesUsesNormalizedTokenPhraseMatch(t *testing.T) {
	cfg, err := ParseYAML([]byte(`
version: 1
lists:
  no_ai:
    customers:
      - name: "Acme, Inc."
  notification_required:
    customers:
      - name: "Globex"
        aliases: ["Globex Corporation"]
      - name: "XYZ"
`))
	if err != nil {
		t.Fatalf("ParseYAML returned error: %v", err)
	}
	audit := AuditCandidates([]Candidate{
		{CallID: "call-acme", Value: "acme inc"},
		{CallID: "call-acme-title", Value: "Acme Inc quarterly review"},
		{CallID: "call-partial", Value: "Acme Partners"},
		{CallID: "call-globex", Value: "Globex Corporation"},
		{CallID: "call-xyz", Value: "XYZ Process Automation overview"},
		{CallID: "call-xylophone", Value: "Xylophone Yard Zone overview"},
	}, cfg)
	if audit.SuppressedCallCount != 4 {
		t.Fatalf("suppressed calls=%d want 4: %+v", audit.SuppressedCallCount, audit)
	}
	for _, callID := range audit.SuppressedCallIDs {
		if callID == "call-partial" || callID == "call-xylophone" {
			t.Fatalf("false-positive match was suppressed: %+v", audit)
		}
	}
	if len(audit.MatchedEntries) != 4 {
		t.Fatalf("matched entries=%d want 4: %+v", len(audit.MatchedEntries), audit)
	}
}

func TestAuditAccumulatorMatchesAuditCandidatesParity(t *testing.T) {
	cfg, err := ParseYAML([]byte(`
version: 1
lists:
  no_ai:
    customers:
      - name: "Acme, Inc."
        aliases: ["ACME"]
  notification_required:
    customers:
      - name: "Globex"
      - name: "Unmatched Co"
`))
	if err != nil {
		t.Fatalf("ParseYAML returned error: %v", err)
	}
	candidates := []Candidate{
		{CallID: "call-b", Value: "ACME"},
		{CallID: "call-a", Value: "Acme, Inc. review"},
		{CallID: "call-a", Value: "duplicate Acme Inc mention"},
		{CallID: "call-blank", Value: "@@@"},
		{CallID: "call-empty", Value: "   "},
		{CallID: "call-globex", Value: "talked with Globex today"},
		{CallID: "call-z", Value: "no match here"},
	}
	want := AuditCandidates(candidates, cfg)
	acc := NewAuditAccumulator(cfg)
	for _, candidate := range candidates {
		acc.Consume(candidate)
	}
	got := acc.Result()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("accumulator result mismatch\ngot:  %+v\nwant: %+v", got, want)
	}
	if got.CandidateValues != len(candidates) {
		t.Fatalf("CandidateValues=%d want %d (blank-normalized must still count)", got.CandidateValues, len(candidates))
	}
	if got.SuppressedCallIDs[0] != "call-a" || got.SuppressedCallIDs[1] != "call-b" || got.SuppressedCallIDs[2] != "call-globex" {
		t.Fatalf("suppressed IDs not sorted uniquely: %+v", got.SuppressedCallIDs)
	}
	if len(got.UnmatchedEntries) != 1 || got.UnmatchedEntries[0].Name != "Unmatched Co" {
		t.Fatalf("unexpected unmatched entries: %+v", got.UnmatchedEntries)
	}
	var acmeAlias *Match
	for i := range got.MatchedEntries {
		if got.MatchedEntries[i].Alias == "ACME" {
			acmeAlias = &got.MatchedEntries[i]
			break
		}
	}
	if acmeAlias == nil || acmeAlias.CallCount != 2 {
		t.Fatalf("alias match call count incorrect: %+v", got.MatchedEntries)
	}
}

func TestBuildAuditPrefersStreamingStore(t *testing.T) {
	cfg, err := ParseYAML([]byte(`
version: 1
lists:
  no_ai:
    customers:
      - name: "Acme"
`))
	if err != nil {
		t.Fatalf("ParseYAML returned error: %v", err)
	}
	streamStore := &fakeStreamingStore{
		candidates: []Candidate{
			{CallID: "call-1", Value: "Acme kickoff"},
			{CallID: "call-2", Value: "other"},
		},
	}
	audit, err := BuildAudit(context.Background(), streamStore, cfg)
	if err != nil {
		t.Fatalf("BuildAudit streaming returned error: %v", err)
	}
	if streamStore.materializeCalls != 0 {
		t.Fatalf("materialize path called %d times; streaming should win", streamStore.materializeCalls)
	}
	if audit.SuppressedCallCount != 1 || audit.SuppressedCallIDs[0] != "call-1" {
		t.Fatalf("unexpected streaming audit: %+v", audit)
	}

	legacy := &fakeLegacyStore{
		candidates: []Candidate{{CallID: "legacy-1", Value: "Acme"}},
	}
	legacyAudit, err := BuildAudit(context.Background(), legacy, cfg)
	if err != nil {
		t.Fatalf("BuildAudit legacy returned error: %v", err)
	}
	if legacyAudit.SuppressedCallCount != 1 || legacyAudit.SuppressedCallIDs[0] != "legacy-1" {
		t.Fatalf("unexpected legacy audit: %+v", legacyAudit)
	}
}

func TestBuildAuditStreamingErrorAndCancel(t *testing.T) {
	cfg, err := ParseYAML([]byte(`
version: 1
lists:
  no_ai:
    customers:
      - name: "Acme"
`))
	if err != nil {
		t.Fatalf("ParseYAML returned error: %v", err)
	}
	streamErr := errors.New("stream boom")
	audit, err := BuildAudit(context.Background(), &fakeStreamingStore{err: streamErr}, cfg)
	if !errors.Is(err, streamErr) {
		t.Fatalf("error=%v want %v", err, streamErr)
	}
	if audit != nil {
		t.Fatalf("partial audit returned on stream error: %+v", audit)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	audit, err = BuildAudit(ctx, &fakeStreamingStore{
		candidates: []Candidate{{CallID: "call-1", Value: "Acme"}},
	}, cfg)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v want context.Canceled", err)
	}
	if audit != nil {
		t.Fatalf("partial audit returned on cancel: %+v", audit)
	}
}

func TestNoExclusionsConfigHasStableFingerprint(t *testing.T) {
	cfg := NoExclusionsConfig()
	if len(cfg.Targets()) != 0 {
		t.Fatalf("targets=%d want 0", len(cfg.Targets()))
	}
	if got := cfg.Fingerprint(); got != NoExclusionsConfigSHA256 {
		t.Fatalf("fingerprint=%q want %q", got, NoExclusionsConfigSHA256)
	}
	if got := NoExclusionsConfigFingerprint(); got != NoExclusionsConfigSHA256 {
		t.Fatalf("NoExclusionsConfigFingerprint=%q want %q", got, NoExclusionsConfigSHA256)
	}
}

func TestParseYAMLRejectsUnknownListAndEmptyTargets(t *testing.T) {
	if _, err := ParseYAML([]byte(`
version: 1
lists:
  noai:
    customers:
      - name: "Typo Corp"
`)); err == nil {
		t.Fatal("ParseYAML allowed unknown list key")
	}
	if _, err := ParseYAML([]byte(`
version: 1
lists:
  no_ai:
    customers: []
`)); err == nil {
		t.Fatal("ParseYAML allowed empty governance target set")
	}
}

type fakeLegacyStore struct {
	candidates []Candidate
}

func (s *fakeLegacyStore) GovernanceNameCandidates(context.Context) ([]Candidate, error) {
	return append([]Candidate(nil), s.candidates...), nil
}

func (s *fakeLegacyStore) GovernanceDataFingerprint(context.Context) (string, error) {
	return "legacy-fingerprint", nil
}

type fakeStreamingStore struct {
	candidates       []Candidate
	err              error
	materializeCalls int
}

func (s *fakeStreamingStore) GovernanceNameCandidates(context.Context) ([]Candidate, error) {
	s.materializeCalls++
	panic("legacy GovernanceNameCandidates must not be used when streaming is available")
}

func (s *fakeStreamingStore) GovernanceDataFingerprint(context.Context) (string, error) {
	return "stream-fingerprint", nil
}

func (s *fakeStreamingStore) StreamGovernanceNameCandidates(ctx context.Context, consume func(Candidate) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.err != nil {
		return s.err
	}
	for _, candidate := range s.candidates {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := consume(candidate); err != nil {
			return err
		}
	}
	return nil
}
