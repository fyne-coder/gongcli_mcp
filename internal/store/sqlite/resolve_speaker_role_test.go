package sqlite

import "testing"

// TestResolveSpeakerRole locks in the cross-backend buyer-vs-rep resolution
// rules used by the quote-search evidence path. The MCP layer relies on
// these stable status values to decide whether to emit caveats about
// uncertainty, so the matrix has to stay deterministic.
func TestResolveSpeakerRole(t *testing.T) {
	t.Parallel()

	parties := []callPartyAttribution{
		{SpeakerKeys: map[string]struct{}{"speaker-internal": {}}, Role: SpeakerRoleInternal},
		{SpeakerKeys: map[string]struct{}{"speaker-external": {}}, Role: SpeakerRoleExternal},
		{SpeakerKeys: map[string]struct{}{"speaker-unknown-affil": {}}, Role: ""},
		{SpeakerKeys: map[string]struct{}{"speaker-ambiguous": {}}, Role: SpeakerRoleInternal},
		{SpeakerKeys: map[string]struct{}{"speaker-ambiguous": {}}, Role: SpeakerRoleExternal},
		{SpeakerKeys: map[string]struct{}{"speaker-ambiguous-same": {}}, Role: SpeakerRoleInternal},
		{SpeakerKeys: map[string]struct{}{"speaker-ambiguous-same": {}}, Role: SpeakerRoleInternal},
	}
	cases := []struct {
		name       string
		speakerID  string
		wantRole   string
		wantStatus string
	}{
		{name: "blank speaker_id", speakerID: "", wantRole: SpeakerRoleUnknown, wantStatus: SpeakerRoleStatusSpeakerUnmatched},
		{name: "no party match", speakerID: "speaker-missing", wantRole: SpeakerRoleUnknown, wantStatus: SpeakerRoleStatusSpeakerUnmatched},
		{name: "internal match", speakerID: "speaker-internal", wantRole: SpeakerRoleInternal, wantStatus: SpeakerRoleStatusAvailable},
		{name: "external match", speakerID: "speaker-external", wantRole: SpeakerRoleExternal, wantStatus: SpeakerRoleStatusAvailable},
		{name: "single match no affiliation", speakerID: "speaker-unknown-affil", wantRole: SpeakerRoleUnknown, wantStatus: SpeakerRoleStatusAffiliationMissing},
		{name: "ambiguous conflicting affiliations", speakerID: "speaker-ambiguous", wantRole: SpeakerRoleUnknown, wantStatus: SpeakerRoleStatusSpeakerAmbiguous},
		{name: "ambiguous matching affiliations", speakerID: "speaker-ambiguous-same", wantRole: SpeakerRoleInternal, wantStatus: SpeakerRoleStatusSpeakerAmbiguous},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			role, status := resolveSpeakerRole(tc.speakerID, parties)
			if role != tc.wantRole || status != tc.wantStatus {
				t.Fatalf("resolveSpeakerRole(%q) = (%q,%q), want (%q,%q)", tc.speakerID, role, status, tc.wantRole, tc.wantStatus)
			}
		})
	}
}
