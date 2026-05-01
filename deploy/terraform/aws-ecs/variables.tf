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

variable "internal_alb" {
  type        = bool
  description = "Whether the ALB is internal."
  default     = true
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
