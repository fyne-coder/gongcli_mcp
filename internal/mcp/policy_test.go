package mcp

import (
	"strings"
	"testing"
)

func TestParsePolicySwitchesEmptyReturnsBaseline(t *testing.T) {
	t.Parallel()

	got, err := ParsePolicySwitches("")
	if err != nil {
		t.Fatalf("ParsePolicySwitches(empty) returned error: %v", err)
	}
	if got.Any() {
		t.Fatalf("empty policy switch input should not enable any switches: %+v", got)
	}
	if got.HideRawCallIDs {
		t.Fatalf("baseline policy must not flip hide_raw_call_ids without explicit input")
	}
}

func TestParsePolicySwitchesParsesAllKnownNames(t *testing.T) {
	t.Parallel()

	allNames := []string{
		"hide_account_names",
		"hide_call_titles",
		"hide_raw_call_ids",
		"hide_speaker_ids",
		"hide_contact_names",
		"hide_contact_emails",
		"hide_opportunity_names",
		"hide_loss_reasons",
		"hide_crm_value_snippets",
	}
	got, err := ParsePolicySwitches(strings.Join(allNames, ","))
	if err != nil {
		t.Fatalf("ParsePolicySwitches returned error: %v", err)
	}
	for _, name := range allNames {
		if !got.IsEnabled(name) {
			t.Fatalf("policy switch %q should be enabled after parsing %q", name, strings.Join(allNames, ","))
		}
	}
	if !got.HideAccountNames || !got.HideCallTitles || !got.HideRawCallIDs ||
		!got.HideSpeakerIDs || !got.HideContactNames || !got.HideContactEmails ||
		!got.HideOpportunityNames || !got.HideLossReasons || !got.HideCRMValueSnippets {
		t.Fatalf("parsed switches did not flip every flag: %+v", got)
	}
	enabled := got.EnabledNames()
	if len(enabled) != len(allNames) {
		t.Fatalf("EnabledNames count=%d want %d", len(enabled), len(allNames))
	}
}

func TestParsePolicySwitchesRejectsUnknownNames(t *testing.T) {
	t.Parallel()

	if _, err := ParsePolicySwitches("hide_account_names,not_a_switch"); err == nil {
		t.Fatal("ParsePolicySwitches accepted unknown switch name")
	}
}

func TestPolicySwitchNamesContractIsStable(t *testing.T) {
	t.Parallel()

	want := []string{
		"hide_account_names",
		"hide_call_titles",
		"hide_raw_call_ids",
		"hide_speaker_ids",
		"hide_contact_names",
		"hide_contact_emails",
		"hide_opportunity_names",
		"hide_loss_reasons",
		"hide_crm_value_snippets",
	}
	got := PolicySwitchNames()
	if len(got) != len(want) {
		t.Fatalf("PolicySwitchNames len=%d want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("PolicySwitchNames[%d]=%q want %q", i, got[i], want[i])
		}
	}
}

func TestBroadPublicRedactedDefaultPolicyKeepsRawCallIDsHidden(t *testing.T) {
	t.Parallel()

	// "Keep raw call IDs off by default; prefer stable refs." — explicit
	// requirement for the broad-public-redacted profile.
	got := BroadPublicRedactedDefaultPolicySwitches()
	if !got.HideRawCallIDs {
		t.Fatalf("broad-public-redacted defaults must hide raw call IDs: %+v", got)
	}
	// Other switches should default off in broad mode unless the operator
	// opts in via explicit policy switches.
	if got.HideAccountNames || got.HideCallTitles || got.HideContactNames {
		t.Fatalf("broad-public-redacted defaults must not auto-hide account/title/contact names: %+v", got)
	}
}

func TestPolicySwitchesReloadContractIsRestartRequired(t *testing.T) {
	t.Parallel()

	// Foundation D-2 must commit to a single contract: switches take effect
	// on restart, not at runtime. Document the choice machine-readably so
	// downstream tooling and tests can rely on it.
	if PolicySwitchReloadContract() != "restart_required" {
		t.Fatalf("PolicySwitchReloadContract=%q want restart_required", PolicySwitchReloadContract())
	}
}
