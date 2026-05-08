package mcp

import (
	"fmt"
	"strings"
)

const (
	fieldProfileCustom      = "custom"
	fieldProfileLimited     = "limited"
	fieldProfileAttribution = "attribution"
	fieldProfileFull        = "full"
)

type fieldProfileApplication struct {
	Profile                 string
	IncludeRawIDs           bool
	IncludeCallTitles       bool
	IncludeAccountNames     bool
	IncludeOpportunityNames bool
	IncludeSpeakerRefs      bool
}

func applyFieldProfile(profile string, current fieldProfileApplication) (fieldProfileApplication, error) {
	canonical, err := normalizeFieldProfile(profile)
	if err != nil {
		return fieldProfileApplication{}, err
	}
	current.Profile = canonical
	switch canonical {
	case "", fieldProfileCustom:
		return current, nil
	case fieldProfileLimited:
		current.IncludeRawIDs = false
		current.IncludeCallTitles = false
		current.IncludeAccountNames = false
		current.IncludeOpportunityNames = false
		current.IncludeSpeakerRefs = false
	case fieldProfileAttribution:
		current.IncludeRawIDs = false
		current.IncludeCallTitles = true
		current.IncludeAccountNames = true
		current.IncludeOpportunityNames = true
		current.IncludeSpeakerRefs = true
	case fieldProfileFull:
		current.IncludeRawIDs = true
		current.IncludeCallTitles = true
		current.IncludeAccountNames = true
		current.IncludeOpportunityNames = true
		current.IncludeSpeakerRefs = true
	}
	return current, nil
}

func normalizeFieldProfile(profile string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case "":
		return "", nil
	case "custom":
		return fieldProfileCustom, nil
	case "limited", "redacted", "redacted_business", "business_limited", "business-limited":
		return fieldProfileLimited, nil
	case "attribution", "business_attribution", "business-attribution":
		return fieldProfileAttribution, nil
	case "full", "all_fields", "all-fields", "full_internal", "full-internal", "internal":
		return fieldProfileFull, nil
	default:
		return "", fmt.Errorf("field_profile must be one of: custom, limited, attribution, full")
	}
}

func fieldProfileSchema() map[string]any {
	return map[string]any{
		"type":        "string",
		"enum":        []string{"", fieldProfileCustom, fieldProfileLimited, fieldProfileAttribution, fieldProfileFull},
		"description": "Optional exposure preset. limited hides raw IDs/names/titles; attribution enables call titles, account/opportunity names, and speaker refs but not raw IDs; full enables every opt-in field subject to active policy switches.",
	}
}
