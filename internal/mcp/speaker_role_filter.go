package mcp

import (
	"fmt"
	"strings"

	"github.com/fyne-coder/gongcli_mcp/internal/store/sqlite"
)

const speakerRoleExternalOrUnknown = "external_or_unknown"

func normalizeSpeakerRoleFilter(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "any":
		return "", nil
	case speakerRoleExternalOrUnknown:
		return speakerRoleExternalOrUnknown, nil
	case "external", "buyer", "customer", "prospect":
		return sqlite.SpeakerRoleExternal, nil
	case "internal", "seller", "rep", "ae":
		return sqlite.SpeakerRoleInternal, nil
	default:
		return "", fmt.Errorf("speaker_role must be one of: external_or_unknown, external, buyer, customer, prospect, internal, seller, rep, ae, or any")
	}
}

func speakerRoleSchema() map[string]any {
	return map[string]any{
		"type":        "string",
		"enum":        []string{"", "any", speakerRoleExternalOrUnknown, "external", "buyer", "customer", "prospect", "internal", "seller", "rep", "ae"},
		"description": "Optional quote speaker filter. buyer/customer/prospect map to external; seller/rep/ae map to internal; external_or_unknown excludes known internal speakers while preserving rows where speaker_role attribution is unavailable.",
	}
}
