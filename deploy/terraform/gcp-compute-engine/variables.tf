variable "project_id" {
  type        = string
  description = "GCP project ID."
}

variable "region" {
  type        = string
  description = "GCP region."
}

variable "zone" {
  type        = string
  description = "GCP zone."
}

variable "name" {
  type        = string
  description = "Deployment name prefix."
  default     = "gongctl"
}

variable "gongmcp_image" {
  type        = string
  description = "Digest-pinned MCP-only image."
  default     = "ghcr.io/fyne-coder/gongcli_mcp/gongmcp:v0.2.0"
}

variable "network" {
  type        = string
  description = "VPC network name."
}

variable "subnetwork" {
  type        = string
  description = "Subnetwork name."
}

variable "assign_public_ip" {
  type        = bool
  description = "Whether the VM gets an external IP. Prefer false behind a load balancer or tunnel."
  default     = false
}

variable "service_account_email" {
  type        = string
  description = "Service account for the VM."
}

variable "service_account_scopes" {
  type        = list(string)
  description = "Least-privilege OAuth scopes for the VM service account."
  default     = ["https://www.googleapis.com/auth/logging.write"]
}

variable "bearer_token_file_path" {
  type        = string
  description = "Host path where customer secret tooling writes the internal bearer token."
  default     = "/etc/gongmcp/bearer_token"
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

variable "machine_type" {
  type        = string
  description = "Compute Engine machine type."
  default     = "e2-small"
}

variable "disk_type" {
  type        = string
  description = "Data disk type for the read-only SQLite cache."
  default     = "pd-balanced"
}

variable "disk_size_gb" {
  type        = number
  description = "Data disk size."
  default     = 20
}
