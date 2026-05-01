variable "resource_group_name" {
  type        = string
  description = "Resource group for the Container App."
}

variable "name" {
  type        = string
  description = "Deployment name prefix."
  default     = "gongctl"
}

variable "container_app_environment_id" {
  type        = string
  description = "Existing Container Apps environment ID."
}

variable "gongmcp_image" {
  type        = string
  description = "Digest-pinned MCP-only image."
  default     = "ghcr.io/fyne-coder/gongcli_mcp/gongmcp:v0.2.0"
}

variable "storage_account_name" {
  type        = string
  description = "Storage account that hosts the read-only Azure Files share."
}

variable "storage_account_access_key" {
  type        = string
  description = "Storage account access key. Prefer customer secret management in real deployments."
  sensitive   = true
}

variable "file_share_name" {
  type        = string
  description = "Azure Files share containing gong.db."
}

variable "bearer_token" {
  type        = string
  description = "Lab-only internal bearer token for gongmcp HTTP mode. Prefer bearer_token_key_vault_secret_id so the token does not pass through Terraform state."
  sensitive   = true
  default     = ""
}

variable "bearer_token_key_vault_secret_id" {
  type        = string
  description = "Preferred Key Vault secret ID containing the internal bearer token. Requires user_assigned_identity_id for a pre-authorized managed identity."
  default     = ""
}

variable "user_assigned_identity_id" {
  type        = string
  description = "Existing user-assigned managed identity resource ID. Required for Key Vault-backed bearer tokens; grant it Key Vault Secrets User before applying."
  default     = ""
}

variable "tool_allowlist" {
  type        = string
  description = "Comma-separated MCP tool allowlist."
  default     = "get_sync_status,summarize_calls_by_lifecycle,summarize_call_facts,rank_transcript_backlog"
}

variable "allowed_origins" {
  type        = string
  description = "Comma-separated HTTP Origin allowlist for browser-capable MCP clients hitting the customer HTTPS endpoint."
}

variable "external_ingress_enabled" {
  type        = bool
  description = "Whether Container Apps exposes an external HTTPS endpoint."
  default     = false
}

variable "cpu" {
  type        = number
  description = "Container CPU."
  default     = 0.5
}

variable "memory" {
  type        = string
  description = "Container memory."
  default     = "1Gi"
}

variable "max_replicas" {
  type        = number
  description = "Maximum read-only MCP replicas."
  default     = 1
}
