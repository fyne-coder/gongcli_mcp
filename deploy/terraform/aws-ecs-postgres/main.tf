terraform {
  # Starter example only. Not production-ready as-is; add customer gateway/SSO,
  # WAF or equivalent controls, access logs, rate limits, token rotation,
  # release approvals, audited operations, and customer-managed database
  # backup/PITR before use. This starter deploys only the read-only gongmcp
  # runtime against an existing Postgres serving DB; it does not create RDS.
  required_version = ">= 1.6.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

provider "aws" {
  region = var.aws_region
}

locals {
  container_name = "${var.name}-gongmcp"
  runtime_environment = concat([
    {
      name  = "GONGMCP_TOOL_PRESET"
      value = var.tool_preset
    },
    {
      name  = "GONGMCP_ALLOWED_ORIGINS"
      value = var.allowed_origins
    }
    ],
    var.enforce_tool_scoped_db_grants ? [{
      name  = "GONGMCP_ENFORCE_TOOL_SCOPED_DB_GRANTS"
      value = "1"
    }] : [],
    var.postgres_redacted_serving_db ? [{
      name  = "GONGMCP_POSTGRES_REDACTED_SERVING_DB"
      value = "1"
    }] : [],
    var.postgres_redacted_serving_db ? [{
      name  = "GONGMCP_AI_GOVERNANCE_CONFIG"
      value = "/run/secrets/ai-governance.yaml"
    }] : [],
    var.extra_environment,
  )
  config_writer_container_name = "write-ai-governance-config"
}

resource "aws_cloudwatch_log_group" "gongmcp" {
  name              = "/gongctl/${var.name}/gongmcp-postgres"
  retention_in_days = var.log_retention_days
  kms_key_id        = var.cloudwatch_log_kms_key_id == "" ? null : var.cloudwatch_log_kms_key_id
}

resource "aws_ecs_cluster" "this" {
  name = "${var.name}-gongmcp-postgres"
}

resource "aws_iam_role" "task_execution" {
  name = "${var.name}-gongmcp-postgres-exec"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Principal = {
        Service = "ecs-tasks.amazonaws.com"
      }
      Action = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy_attachment" "task_execution" {
  role       = aws_iam_role.task_execution.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy"
}

resource "aws_iam_role_policy" "read_runtime_secrets" {
  name = "${var.name}-read-gongmcp-postgres-secrets"
  role = aws_iam_role.task_execution.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Action = ["secretsmanager:GetSecretValue"]
      Resource = [
        var.bearer_token_secret_arn,
        var.database_url_secret_arn,
      ]
    }]
  })
}

resource "aws_iam_role_policy" "read_ai_governance_secret" {
  count = var.postgres_redacted_serving_db ? 1 : 0
  name  = "${var.name}-read-ai-governance-config"
  role  = aws_iam_role.task_execution.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["secretsmanager:GetSecretValue"]
      Resource = var.ai_governance_config_secret_arn
    }]
  })
}

resource "aws_security_group" "alb" {
  name        = "${var.name}-gongmcp-postgres-alb"
  description = "HTTPS ingress to customer-hosted gongmcp Postgres runtime"
  vpc_id      = var.vpc_id

  ingress {
    description = "HTTPS from approved networks"
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = var.allowed_ingress_cidrs
  }

  egress {
    description = "ALB egress to private service targets"
    from_port   = 8080
    to_port     = 8080
    protocol    = "tcp"
    cidr_blocks = var.alb_egress_cidrs
  }
}

resource "aws_security_group" "service" {
  name        = "${var.name}-gongmcp-postgres-service"
  description = "ALB to gongmcp HTTP plus approved Postgres egress"
  vpc_id      = var.vpc_id

  ingress {
    from_port       = 8080
    to_port         = 8080
    protocol        = "tcp"
    security_groups = [aws_security_group.alb.id]
  }

  dynamic "egress" {
    for_each = var.postgres_egress_cidrs
    content {
      description = "Postgres egress to approved CIDR"
      from_port   = var.postgres_port
      to_port     = var.postgres_port
      protocol    = "tcp"
      cidr_blocks = [egress.value]
    }
  }

  dynamic "egress" {
    for_each = var.postgres_security_group_ids
    content {
      description     = "Postgres egress to approved security group"
      from_port       = var.postgres_port
      to_port         = var.postgres_port
      protocol        = "tcp"
      security_groups = [egress.value]
    }
  }

  dynamic "egress" {
    for_each = var.service_extra_egress_cidrs
    content {
      description = "Explicit customer-approved service egress"
      from_port   = 0
      to_port     = 0
      protocol    = "-1"
      cidr_blocks = [egress.value]
    }
  }

  lifecycle {
    precondition {
      condition     = length(var.postgres_egress_cidrs) > 0 || length(var.postgres_security_group_ids) > 0
      error_message = "Postgres runtime starter requires postgres_egress_cidrs or postgres_security_group_ids so gongmcp can reach the existing serving DB."
    }
  }
}

resource "aws_lb" "this" {
  name               = "${var.name}-gongmcp-pg"
  load_balancer_type = "application"
  internal           = var.internal_alb
  security_groups    = [aws_security_group.alb.id]
  subnets            = var.alb_subnet_ids

  dynamic "access_logs" {
    for_each = var.alb_access_logs_bucket == "" ? [] : [var.alb_access_logs_bucket]
    content {
      bucket  = access_logs.value
      prefix  = var.alb_access_logs_prefix
      enabled = true
    }
  }

  lifecycle {
    precondition {
      condition     = var.internal_alb || var.acknowledge_no_sso_gateway
      error_message = "Externally reachable static-bearer ALB examples require acknowledge_no_sso_gateway=true and a customer-approved gateway/SSO/WAF plan."
    }
  }
}

resource "aws_lb_target_group" "this" {
  name        = "${var.name}-gongmcp-pg"
  port        = 8080
  protocol    = "HTTP"
  target_type = "ip"
  vpc_id      = var.vpc_id

  health_check {
    enabled             = true
    path                = "/healthz"
    matcher             = "200"
    interval            = 30
    timeout             = 5
    healthy_threshold   = 2
    unhealthy_threshold = 3
  }
}

resource "aws_lb_listener" "https" {
  load_balancer_arn = aws_lb.this.arn
  port              = 443
  protocol          = "HTTPS"
  certificate_arn   = var.acm_certificate_arn

  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.this.arn
  }
}

resource "aws_ecs_task_definition" "gongmcp" {
  family                   = "${var.name}-gongmcp-postgres"
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = var.cpu
  memory                   = var.memory
  execution_role_arn       = aws_iam_role.task_execution.arn

  volume {
    name = "governance-config"
  }

  container_definitions = jsonencode(concat(
    var.postgres_redacted_serving_db ? [{
      name       = local.config_writer_container_name
      image      = var.gongmcp_image
      essential  = false
      user       = "0"
      entryPoint = ["/bin/sh", "-lc"]
      command = [
        "mkdir -p /run/secrets; printf '%s' \"$AI_GOVERNANCE_CONFIG_CONTENT\" > /run/secrets/ai-governance.yaml; chmod 0444 /run/secrets/ai-governance.yaml"
      ]
      secrets = [{
        name      = "AI_GOVERNANCE_CONFIG_CONTENT"
        valueFrom = var.ai_governance_config_secret_arn
      }]
      mountPoints = [{
        sourceVolume  = "governance-config"
        containerPath = "/run/secrets"
        readOnly      = false
      }]
      logConfiguration = {
        logDriver = "awslogs"
        options = {
          awslogs-group         = aws_cloudwatch_log_group.gongmcp.name
          awslogs-region        = var.aws_region
          awslogs-stream-prefix = "gongmcp-config"
        }
      }
    }] : [],
    [{
      name      = local.container_name
      image     = var.gongmcp_image
      essential = true
      dependsOn = var.postgres_redacted_serving_db ? [{
        containerName = local.config_writer_container_name
        condition     = "SUCCESS"
      }] : []
      command = [
        "--http", "0.0.0.0:8080",
        "--auth-mode", "bearer",
        "--allow-open-network"
      ]
      portMappings = [{
        containerPort = 8080
        protocol      = "tcp"
      }]
      environment = local.runtime_environment
      secrets = [{
        name      = "GONGMCP_BEARER_TOKEN"
        valueFrom = var.bearer_token_secret_arn
        }, {
        name      = "GONG_DATABASE_URL"
        valueFrom = var.database_url_secret_arn
      }]
      mountPoints = var.postgres_redacted_serving_db ? [{
        sourceVolume  = "governance-config"
        containerPath = "/run/secrets"
        readOnly      = true
      }] : []
      logConfiguration = {
        logDriver = "awslogs"
        options = {
          awslogs-group         = aws_cloudwatch_log_group.gongmcp.name
          awslogs-region        = var.aws_region
          awslogs-stream-prefix = "gongmcp"
        }
      }
  }]))

  lifecycle {
    precondition {
      condition     = trimspace(var.tool_preset) != ""
      error_message = "Postgres HTTP mode requires an explicit tool_preset."
    }
    precondition {
      condition     = !var.postgres_redacted_serving_db || var.enforce_tool_scoped_db_grants
      error_message = "Redacted Postgres serving DB mode must enforce tool-scoped DB grants."
    }
    precondition {
      condition     = !var.postgres_redacted_serving_db || trimspace(var.ai_governance_config_secret_arn) != ""
      error_message = "Redacted Postgres serving DB mode requires ai_governance_config_secret_arn."
    }
    precondition {
      condition     = !(var.postgres_redacted_serving_db && contains(["all", "all-tools", "all-readonly"], var.tool_preset))
      error_message = "Do not use all-readonly/all/all-tools with the redacted Postgres serving DB runtime starter."
    }
  }
}

resource "aws_ecs_service" "gongmcp" {
  name            = "${var.name}-gongmcp-postgres"
  cluster         = aws_ecs_cluster.this.id
  task_definition = aws_ecs_task_definition.gongmcp.arn
  desired_count   = var.desired_count
  launch_type     = "FARGATE"

  network_configuration {
    subnets          = var.service_subnet_ids
    security_groups  = [aws_security_group.service.id]
    assign_public_ip = false
  }

  load_balancer {
    target_group_arn = aws_lb_target_group.this.arn
    container_name   = local.container_name
    container_port   = 8080
  }

  depends_on = [aws_lb_listener.https]
}

output "mcp_url" {
  value       = "https://${aws_lb.this.dns_name}/mcp"
  description = "Use a customer DNS record in front of this ALB for user-facing setup."
}

output "service_security_group_id" {
  value       = aws_security_group.service.id
  description = "Attach this as an allowed source on the existing Postgres security group when using SG-to-SG egress."
}
