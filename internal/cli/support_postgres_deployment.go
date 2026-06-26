package cli

import (
	"context"
	"os"
	"time"

	postgresdeploy "github.com/fyne-coder/gongcli_mcp/internal/deploy/postgres"
	"github.com/fyne-coder/gongcli_mcp/internal/mcp"
	"github.com/fyne-coder/gongcli_mcp/internal/store/postgres"
)

type supportPostgresDeploymentFile struct {
	SchemaVersion           int                      `json:"schema_version"`
	GeneratedAt             string                   `json:"generated_at"`
	Preset                  string                   `json:"preset"`
	DeploymentConfigPosture supportDeploymentPosture `json:"deployment_config_posture"`
	RefreshProgress         supportRefreshProgress   `json:"refresh_progress"`
	Checks                  []postgresdeploy.Check   `json:"checks"`
}

type supportDeploymentPosture struct {
	Policy  string          `json:"policy"`
	Present map[string]bool `json:"present"`
}

type supportRefreshProgress struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

func supportPostgresDeploymentConfigPosture() supportDeploymentPosture {
	keys := []string{
		"GONGCTL_SOURCE_DATABASE_URL",
		"GONGCTL_MCP_DATABASE_URL",
		"GONG_DATABASE_URL",
		"DATABASE_URL",
		"GONGCTL_REFRESH_STATEMENT_TIMEOUT",
		"GONGCTL_AI_GOVERNANCE_CONFIG",
		"GONGMCP_AI_GOVERNANCE_CONFIG",
		"GONGMCP_TOOL_PRESET",
		"GONGMCP_READER_ROLE",
		"GONGMCP_DATABASE_NAME",
		"GONGCTL_MCP_DB",
	}
	present := make(map[string]bool, len(keys))
	for _, key := range keys {
		present[key] = os.Getenv(key) != ""
	}
	return supportDeploymentPosture{
		Policy:  "presence_only_values_not_exported",
		Present: present,
	}
}

func buildSupportPostgresDeployment(ctx context.Context, bundleStore cacheInventoryStore) (*supportPostgresDeploymentFile, error) {
	selectedPreset := deployInputDefault("", "business-workbench", "GONGMCP_TOOL_PRESET")
	role := deployInputDefault("", "gongmcp_business_workbench_reader", "GONGMCP_READER_ROLE")
	database := deployInputDefault("", "gongctl_mcp", "GONGMCP_DATABASE_NAME", "GONGCTL_MCP_DB")
	statementTimeoutInput := refreshServingDBInput("", "GONGCTL_REFRESH_STATEMENT_TIMEOUT")
	governanceConfigured := refreshServingDBInput("", "GONGCTL_AI_GOVERNANCE_CONFIG", "GONGMCP_AI_GOVERNANCE_CONFIG")

	checks := []postgresdeploy.Check{
		{
			Name:    "serving_database_connectivity",
			Status:  postgresdeploy.CheckPass,
			Message: "bundle database opened for Postgres deployment diagnostics",
		},
		envPresenceCheck(
			"governance_config",
			governanceConfigured,
			"set GONGCTL_AI_GOVERNANCE_CONFIG or GONGMCP_AI_GOVERNANCE_CONFIG",
		),
		postgresdeploy.CheckStatementTimeout(statementTimeoutInput),
		postgresdeploy.CheckScopedReaderRoleInput(role, database),
	}

	presetCheck, err := presetCatalogCheck(selectedPreset)
	if err != nil {
		checks = append(checks, postgresdeploy.Check{
			Name:        "tool_preset",
			Status:      postgresdeploy.CheckFail,
			ErrorKind:   "unknown_tool_preset",
			Message:     "selected MCP tool preset is not known",
			Remediation: "use a reviewed preset such as business-workbench",
		})
		checks = append(checks, postgresdeploy.Check{
			Name:        "scoped_reader_grant_sql",
			Status:      postgresdeploy.CheckFail,
			ErrorKind:   "unknown_tool_preset",
			Message:     "scoped reader grant SQL could not be generated for an unknown preset",
			Remediation: "use a reviewed preset such as business-workbench",
			Evidence: []postgresdeploy.Evidence{
				{Key: "role", Value: role},
				{Key: "database", Value: database},
			},
		})
	} else {
		checks = append(checks, presetCheck)
		allowlist, _ := mcp.ExpandToolPresetReaderGrantTools(selectedPreset)
		checks = append(checks, postgresdeploy.CheckScopedReaderGrantSQL(postgres.ScopedReaderGrantSQLParams{
			Allowlist:    allowlist,
			RoleName:     role,
			DatabaseName: database,
			Generator:    "gongctl support bundle",
		}))
	}

	if markerReader, ok := bundleStore.(postgresdeploy.ServingRefreshMarkerReader); ok {
		checks = append(checks, postgresdeploy.CheckServingRefreshMarker(ctx, markerReader, postgresdeploy.ServingRefreshMarkerOptions{
			MaxAge: defaultPostgresDeployMarkerMaxAge,
		}))
	} else {
		checks = append(checks, postgresdeploy.CheckServingRefreshMarkerUnavailable("serving_refresh_marker_unavailable"))
	}

	source := refreshServingDBInput("", "GONGCTL_SOURCE_DATABASE_URL")
	if source == "" {
		checks = append(checks, postgresdeploy.CheckSourceDatabaseConnectivityMissing())
		checks = append(checks, postgresdeploy.CheckSourceReadModelUnavailable("source_database_url_missing"))
	} else {
		sourceStore, sourceErr := deployOpenPostgresStatus(ctx, source)
		checks = append(checks, postgresdeploy.CheckSourceDatabaseConnectivity(sourceErr))
		if sourceErr != nil {
			checks = append(checks, postgresdeploy.CheckSourceReadModelUnavailable("source_database_unavailable"))
		} else {
			defer sourceStore.Close()
			checks = append(checks, postgresdeploy.CheckSourceReadModel(ctx, sourceStore))
		}
	}

	return &supportPostgresDeploymentFile{
		SchemaVersion:           supportBundleSchemaVersion,
		GeneratedAt:             time.Now().UTC().Format(time.RFC3339),
		Preset:                  selectedPreset,
		DeploymentConfigPosture: supportPostgresDeploymentConfigPosture(),
		RefreshProgress: supportRefreshProgress{
			Status:  "not_available",
			Message: "no persisted in-progress refresh state is available in the support bundle path",
		},
		Checks: checks,
	}, nil
}
