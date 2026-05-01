variable "aws_region" {
  type        = string
  description = "AWS region for the ECS service."
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

variable "efs_file_system_id" {
  type        = string
  description = "EFS file system containing the read-only MCP SQLite cache."
}

variable "efs_access_point_id" {
  type        = string
  description = "EFS access point that exposes gong.db under /data."
}

variable "bearer_token_secret_arn" {
  type        = string
  description = "AWS Secrets Manager secret ARN containing the internal bearer token."
  sensitive   = true
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

variable "service_egress_cidrs" {
  type        = list(string)
  description = "Explicit customer-approved egress CIDRs for the ECS service. Leave empty for no internet egress; add only required private endpoints such as EFS/VPC services."
  default     = []
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
  default     = "gongmcp"
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
