variable "aws_region" {
  type        = string
  description = "AWS region for the ECS service."
}

variable "name" {
  type        = string
  description = "Deployment name prefix."
  default     = "gongctl"

  validation {
    condition     = can(regex("^[A-Za-z0-9]([A-Za-z0-9-]{0,19}[A-Za-z0-9])?$", var.name))
    error_message = "name must be 1-21 characters, contain only letters, numbers, and hyphens, and not start or end with a hyphen so AWS ALB/target-group names remain valid."
  }
}

variable "gongmcp_image" {
  type        = string
  description = "Digest-pinned MCP-only image."
  default     = "ghcr.io/fyne-coder/gongcli_mcp/gongmcp:v0.4.4"
}

variable "vpc_id" {
  type        = string
  description = "Existing VPC ID."
}

variable "alb_subnet_ids" {
  type        = list(string)
  description = "Subnets for the HTTPS ALB."
}

variable "service_subnet_ids" {
  type        = list(string)
  description = "Private subnets for the ECS tasks."
}

variable "allowed_ingress_cidrs" {
  type        = list(string)
  description = "CIDR ranges allowed to reach HTTPS."
}

variable "alb_egress_cidrs" {
  type        = list(string)
  description = "Private CIDR ranges where the ALB may reach ECS tasks on port 8080."
}

variable "internal_alb" {
  type        = bool
  description = "Whether the ALB is internal."
  default     = true
}

variable "acknowledge_no_sso_gateway" {
  type        = bool
  description = "Set true only when an externally reachable static-bearer lab bridge has a customer-approved SSO/gateway/WAF plan outside this starter."
  default     = false
}

variable "acm_certificate_arn" {
  type        = string
  description = "ACM certificate ARN for HTTPS."
}

variable "bearer_token_secret_arn" {
  type        = string
  description = "AWS Secrets Manager secret ARN containing the internal bearer token."
  sensitive   = true
}

variable "database_url_secret_arn" {
  type        = string
  description = "AWS Secrets Manager secret ARN containing the scoped Postgres reader URL for the serving DB. Do not use a writer URL."
  sensitive   = true
}

variable "ai_governance_config_secret_arn" {
  type        = string
  description = "AWS Secrets Manager secret ARN containing the AI governance YAML content. Required when postgres_redacted_serving_db is true."
  default     = ""
  sensitive   = true
}

variable "tool_preset" {
  type        = string
  description = "MCP tool preset exposed by the Postgres runtime. Use business-workbench for the default client-facing facade."
  default     = "business-workbench"

  validation {
    condition     = trimspace(var.tool_preset) != ""
    error_message = "tool_preset must not be empty; Postgres HTTP mode requires an explicit preset."
  }
}

variable "allowed_origins" {
  type        = string
  description = "Comma-separated HTTP Origin allowlist for browser-capable MCP clients hitting the customer HTTPS endpoint."
}

variable "enforce_tool_scoped_db_grants" {
  type        = bool
  description = "Set GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS=1 so startup verifies the selected Postgres reader grants."
  default     = true
}

variable "postgres_redacted_serving_db" {
  type        = bool
  description = "Set GONGMCP_POSTGRES_REDACTED_SERVING_DB=1. Use only when the reader URL points at a DB produced by governance refresh-serving-db."
  default     = true
}

variable "postgres_port" {
  type        = number
  description = "Postgres listener port for service egress."
  default     = 5432
}

variable "postgres_egress_cidrs" {
  type        = list(string)
  description = "Approved CIDR ranges where the ECS task may reach the existing Postgres serving DB."
  default     = []
}

variable "postgres_security_group_ids" {
  type        = list(string)
  description = "Approved security groups where the ECS task may reach the existing Postgres serving DB."
  default     = []
}

variable "service_extra_egress_cidrs" {
  type        = list(string)
  description = "Explicit customer-approved non-Postgres egress CIDRs, for example private VPC endpoints needed for image pulls, logs, or secret reads."
  default     = []
}

variable "extra_environment" {
  type = list(object({
    name  = string
    value = string
  }))
  description = "Additional non-secret environment variables for gongmcp."
  default     = []

  validation {
    condition = length([
      for item in var.extra_environment : item.name
      if contains([
        "GONG_DATABASE_URL",
        "DATABASE_URL",
        "GONGMCP_BEARER_TOKEN",
        "GONGMCP_TOOL_PRESET",
        "GONGMCP_TOOL_ALLOWLIST",
        "GONGMCP_ALLOWED_ORIGINS",
        "GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS",
        "GONGMCP_POSTGRES_REDACTED_SERVING_DB",
      ], item.name)
    ]) == 0
    error_message = "extra_environment must not set reserved gongmcp runtime variables managed by this starter."
  }
}

variable "cloudwatch_log_kms_key_id" {
  type        = string
  description = "Optional KMS key ARN/ID for encrypting the CloudWatch log group."
  default     = ""
}

variable "alb_access_logs_bucket" {
  type        = string
  description = "Optional S3 bucket for ALB access logs. Production deployments should enable this with a customer-owned bucket."
  default     = ""
}

variable "alb_access_logs_prefix" {
  type        = string
  description = "S3 prefix for ALB access logs when alb_access_logs_bucket is set."
  default     = "gongmcp-postgres"
}

variable "cpu" {
  type        = number
  description = "Fargate CPU units."
  default     = 512
}

variable "memory" {
  type        = number
  description = "Fargate memory MiB."
  default     = 1024
}

variable "desired_count" {
  type        = number
  description = "Number of read-only MCP tasks."
  default     = 1
}

variable "log_retention_days" {
  type        = number
  description = "CloudWatch log retention."
  default     = 30
}
