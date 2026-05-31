package mcp

import (
	"fmt"
	"sort"
	"strings"
)

// PolicySwitches is the customer-deployment policy switch contract for the
// broad-public-redacted profile and stricter customer profiles. Each switch
// suppresses one class of customer-identifying field at MCP emit time.
//
// Reload contract: switches take effect at MCP startup. They are not
// hot-reloaded by gongmcp; operators must restart the MCP process to apply a
// changed policy. This is documented and tested via PolicySwitchReloadContract.
type PolicySwitches struct {
	HideAccountNames     bool `json:"hide_account_names"`
	HideCallTitles       bool `json:"hide_call_titles"`
	HideRawCallIDs       bool `json:"hide_raw_call_ids"`
	HideSpeakerIDs       bool `json:"hide_speaker_ids"`
	HideContactNames     bool `json:"hide_contact_names"`
	HideContactEmails    bool `json:"hide_contact_emails"`
	HideOpportunityNames bool `json:"hide_opportunity_names"`
	HideLossReasons      bool `json:"hide_loss_reasons"`
	HideCRMValueSnippets bool `json:"hide_crm_value_snippets"`
}

// PolicySwitchNames returns the canonical machine-readable list of policy
// switch names recognized by ParsePolicySwitches.
func PolicySwitchNames() []string {
	return []string{
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
}

// PolicySwitchReloadContract returns the documented reload semantics for
// policy switches. Tooling should treat any change to this string as a
// breaking contract change.
func PolicySwitchReloadContract() string {
	return "restart_required"
}

// ParsePolicySwitches parses a comma-separated switch list (e.g.
// "hide_account_names,hide_call_titles") into a PolicySwitches value. Empty
// input yields the zero value with every switch disabled.
func ParsePolicySwitches(raw string) (PolicySwitches, error) {
	out := PolicySwitches{}
	known := map[string]struct{}{}
	for _, name := range PolicySwitchNames() {
		known[name] = struct{}{}
	}
	for _, piece := range strings.Split(raw, ",") {
		name := strings.ToLower(strings.TrimSpace(piece))
		if name == "" {
			continue
		}
		if _, ok := known[name]; !ok {
			return PolicySwitches{}, fmt.Errorf("unknown policy switch %q", name)
		}
		out.set(name, true)
	}
	return out, nil
}

// MergePolicySwitches combines two PolicySwitches values; any flag enabled in
// either input is enabled in the output. Used to layer profile defaults on
// top of operator-provided switches.
func MergePolicySwitches(a, b PolicySwitches) PolicySwitches {
	merged := a
	for _, name := range PolicySwitchNames() {
		if b.IsEnabled(name) {
			merged.set(name, true)
		}
	}
	return merged
}

// BroadPublicRedactedDefaultPolicySwitches returns the broad-public-redacted
// profile's default policy posture. Raw call IDs are off by default; stable
// refs are preferred for client-visible output.
func BroadPublicRedactedDefaultPolicySwitches() PolicySwitches {
	return PolicySwitches{HideRawCallIDs: true}
}

// IsEnabled reports whether the named switch is enabled.
func (p PolicySwitches) IsEnabled(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "hide_account_names":
		return p.HideAccountNames
	case "hide_call_titles":
		return p.HideCallTitles
	case "hide_raw_call_ids":
		return p.HideRawCallIDs
	case "hide_speaker_ids":
		return p.HideSpeakerIDs
	case "hide_contact_names":
		return p.HideContactNames
	case "hide_contact_emails":
		return p.HideContactEmails
	case "hide_opportunity_names":
		return p.HideOpportunityNames
	case "hide_loss_reasons":
		return p.HideLossReasons
	case "hide_crm_value_snippets":
		return p.HideCRMValueSnippets
	}
	return false
}

// Any reports whether at least one switch is enabled.
func (p PolicySwitches) Any() bool {
	for _, name := range PolicySwitchNames() {
		if p.IsEnabled(name) {
			return true
		}
	}
	return false
}

// EnabledNames returns the canonical names of enabled switches in stable
// order.
func (p PolicySwitches) EnabledNames() []string {
	out := make([]string, 0)
	for _, name := range PolicySwitchNames() {
		if p.IsEnabled(name) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func (p *PolicySwitches) set(name string, value bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "hide_account_names":
		p.HideAccountNames = value
	case "hide_call_titles":
		p.HideCallTitles = value
	case "hide_raw_call_ids":
		p.HideRawCallIDs = value
	case "hide_speaker_ids":
		p.HideSpeakerIDs = value
	case "hide_contact_names":
		p.HideContactNames = value
	case "hide_contact_emails":
		p.HideContactEmails = value
	case "hide_opportunity_names":
		p.HideOpportunityNames = value
	case "hide_loss_reasons":
		p.HideLossReasons = value
	case "hide_crm_value_snippets":
		p.HideCRMValueSnippets = value
	}
}
