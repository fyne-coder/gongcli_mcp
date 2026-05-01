terraform {
  # Starter example only. Not production-ready as-is; add customer gateway/SSO,
  # WAF or equivalent controls, access logs, rate limits, token rotation,
  # least-privilege egress, release approvals, and audited operations before use.
  required_version = ">= 1.6.0"

  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "~> 3.100"
    }
  }
}

provider "azurerm" {
  features {}
}

resource "azurerm_container_app_environment_storage" "gong_data" {
  name                         = "${var.name}-gong-data"
  container_app_environment_id = var.container_app_environment_id
  account_name                 = var.storage_account_name
  share_name                   = var.file_share_name
  access_key                   = var.storage_account_access_key
  access_mode                  = "ReadOnly"
}

resource "azurerm_container_app" "gongmcp" {
  name                         = "${var.name}-gongmcp"
  resource_group_name          = var.resource_group_name
  container_app_environment_id = var.container_app_environment_id
  revision_mode                = "Single"

  secret {
    name  = "gongmcp-bearer-token"
    value = var.bearer_token
  }

  ingress {
    external_enabled = var.external_ingress_enabled
    target_port      = 8080
    transport        = "http"

    traffic_weight {
      latest_revision = true
      percentage      = 100
    }
  }

  template {
    min_replicas = 1
    max_replicas = var.max_replicas

    volume {
      name         = "gong-data"
      storage_type = "AzureFile"
      storage_name = azurerm_container_app_environment_storage.gong_data.name
    }

    container {
      name   = "gongmcp"
      image  = var.gongmcp_image
      cpu    = var.cpu
      memory = var.memory

      args = [
        "--http", "0.0.0.0:8080",
        "--auth-mode", "bearer",
        "--allow-open-network",
        "--db", "/data/gong.db"
      ]

      env {
        name  = "GONGMCP_TOOL_ALLOWLIST"
        value = var.tool_allowlist
      }

      env {
        name  = "GONGMCP_ALLOWED_ORIGINS"
        value = var.allowed_origins
      }

      env {
        name        = "GONGMCP_BEARER_TOKEN"
        secret_name = "gongmcp-bearer-token"
      }

      volume_mounts {
        name = "gong-data"
        path = "/data"
      }
    }
  }
}

output "mcp_url" {
  value       = "https://${azurerm_container_app.gongmcp.latest_revision_fqdn}/mcp"
  description = "Use customer DNS, access restrictions, and TLS policy before sharing with users."
}
