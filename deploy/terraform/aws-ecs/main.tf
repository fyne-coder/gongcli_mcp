terraform {
  # Starter example only. Not production-ready as-is; add customer gateway/SSO,
  # WAF or equivalent controls, access logs, rate limits, token rotation,
  # least-privilege egress, release approvals, and audited operations before use.
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
}

resource "aws_cloudwatch_log_group" "gongmcp" {
  name              = "/gongctl/${var.name}/gongmcp"
  retention_in_days = var.log_retention_days
  kms_key_id        = var.cloudwatch_log_kms_key_id == "" ? null : var.cloudwatch_log_kms_key_id
}

resource "aws_ecs_cluster" "this" {
  name = "${var.name}-gongmcp"
}

resource "aws_iam_role" "task_execution" {
  name = "${var.name}-gongmcp-exec"

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

resource "aws_iam_role_policy" "read_bearer_secret" {
  name = "${var.name}-read-bearer-secret"
  role = aws_iam_role.task_execution.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["secretsmanager:GetSecretValue"]
      Resource = var.bearer_token_secret_arn
    }]
  })
}

resource "aws_security_group" "alb" {
  name        = "${var.name}-gongmcp-alb"
  description = "HTTPS ingress to customer-hosted gongmcp"
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
  name        = "${var.name}-gongmcp-service"
  description = "ALB to gongmcp HTTP"
  vpc_id      = var.vpc_id

  ingress {
    from_port       = 8080
    to_port         = 8080
    protocol        = "tcp"
    security_groups = [aws_security_group.alb.id]
  }

  dynamic "egress" {
    for_each = var.service_egress_cidrs
    content {
      description = "Explicit customer-approved service egress"
      from_port   = 0
      to_port     = 0
      protocol    = "-1"
      cidr_blocks = [egress.value]
    }
  }
}

resource "aws_lb" "this" {
  name               = "${var.name}-gongmcp"
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
  name        = "${var.name}-gongmcp"
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
  family                   = "${var.name}-gongmcp"
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = var.cpu
  memory                   = var.memory
  execution_role_arn       = aws_iam_role.task_execution.arn

  volume {
    name = "gong-data"

    efs_volume_configuration {
      file_system_id     = var.efs_file_system_id
      transit_encryption = "ENABLED"
      authorization_config {
        access_point_id = var.efs_access_point_id
        iam             = "DISABLED"
      }
    }
  }

  container_definitions = jsonencode([{
    name      = local.container_name
    image     = var.gongmcp_image
    essential = true
    command = [
      "--http", "0.0.0.0:8080",
      "--auth-mode", "bearer",
      "--allow-open-network",
      "--db", "/data/gong.db"
    ]
    portMappings = [{
      containerPort = 8080
      protocol      = "tcp"
    }]
    environment = [{
      name  = "GONGMCP_TOOL_ALLOWLIST"
      value = var.tool_allowlist
      }, {
      name  = "GONGMCP_ALLOWED_ORIGINS"
      value = var.allowed_origins
    }]
    secrets = [{
      name      = "GONGMCP_BEARER_TOKEN"
      valueFrom = var.bearer_token_secret_arn
    }]
    mountPoints = [{
      sourceVolume  = "gong-data"
      containerPath = "/data"
      readOnly      = true
    }]
    logConfiguration = {
      logDriver = "awslogs"
      options = {
        awslogs-group         = aws_cloudwatch_log_group.gongmcp.name
        awslogs-region        = var.aws_region
        awslogs-stream-prefix = "gongmcp"
      }
    }
  }])
}

resource "aws_ecs_service" "gongmcp" {
  name            = "${var.name}-gongmcp"
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
