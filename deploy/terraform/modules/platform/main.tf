# Platform module: the shared runtime substrate — KMS CMK, ECR repos, ECS
# cluster, log groups, and the ALB (target group + listener). Stateless app
# wiring lives in the app module.

data "aws_caller_identity" "current" {}
data "aws_region" "current" {}

locals {
  name = "fraud-${var.env_name}"
  # HTTPS only when a cert is supplied; dev can run HTTP-only.
  enable_https = var.certificate_arn != ""
}

# ---- KMS customer-managed key: one CMK encrypts logs, SQS, DynamoDB, secrets ----
resource "aws_kms_key" "this" {
  description             = "${local.name} CMK (encryption at rest)"
  enable_key_rotation     = true
  deletion_window_in_days = 7
  tags                    = merge(var.tags, { Name = "${local.name}-cmk" })
}

resource "aws_kms_alias" "this" {
  name          = "alias/${local.name}"
  target_key_id = aws_kms_key.this.key_id
}

# Allow CloudWatch Logs to use the CMK (required to encrypt log groups with it).
resource "aws_kms_key_policy" "this" {
  key_id = aws_kms_key.this.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid       = "AccountRoot"
        Effect    = "Allow"
        Principal = { AWS = "arn:aws:iam::${data.aws_caller_identity.current.account_id}:root" }
        Action    = "kms:*"
        Resource  = "*"
      },
      {
        Sid       = "CloudWatchLogs"
        Effect    = "Allow"
        Principal = { Service = "logs.${data.aws_region.current.name}.amazonaws.com" }
        Action    = ["kms:Encrypt*", "kms:Decrypt*", "kms:ReEncrypt*", "kms:GenerateDataKey*", "kms:Describe*"]
        Resource  = "*"
        Condition = {
          ArnLike = { "kms:EncryptionContext:aws:logs:arn" = "arn:aws:logs:${data.aws_region.current.name}:${data.aws_caller_identity.current.account_id}:log-group:/fraud/${var.env_name}/*" }
        }
      }
    ]
  })
}

# ---- ECR: one repo for the service image, one for the Lambda image ----
resource "aws_ecr_repository" "server" {
  name                 = "${local.name}-server"
  image_tag_mutability = "IMMUTABLE" # tags can't be overwritten -> reproducible deploys
  force_delete         = true
  image_scanning_configuration { scan_on_push = true }
  encryption_configuration {
    encryption_type = "KMS"
    kms_key         = aws_kms_key.this.arn
  }
  tags = var.tags
}

resource "aws_ecr_repository" "consumer" {
  name                 = "${local.name}-consumer"
  image_tag_mutability = "IMMUTABLE"
  force_delete         = true
  image_scanning_configuration { scan_on_push = true }
  encryption_configuration {
    encryption_type = "KMS"
    kms_key         = aws_kms_key.this.arn
  }
  tags = var.tags
}

# ---- ECS cluster ----
resource "aws_ecs_cluster" "this" {
  name = local.name
  setting {
    name  = "containerInsights"
    value = "enabled"
  }
  tags = var.tags
}

# ---- CloudWatch log groups (CMK-encrypted) ----
resource "aws_cloudwatch_log_group" "app" {
  name              = "/fraud/${var.env_name}/app"
  retention_in_days = var.log_retention_days
  kms_key_id        = aws_kms_key.this.arn
  tags              = var.tags
}

resource "aws_cloudwatch_log_group" "consumer" {
  name              = "/fraud/${var.env_name}/consumer"
  retention_in_days = var.log_retention_days
  kms_key_id        = aws_kms_key.this.arn
  tags              = var.tags
}

# ---- ALB + target group + listener ----
resource "aws_lb" "this" {
  name               = "${local.name}-alb"
  load_balancer_type = "application"
  security_groups    = [var.alb_security_group_id]
  subnets            = var.public_subnet_ids
  idle_timeout       = 60
  # Drop malformed/ambiguous HTTP headers at the edge (request-smuggling
  # defense). Trivy AWS-0052 — no reason not to.
  drop_invalid_header_fields = true
  tags                       = var.tags
}

resource "aws_lb_target_group" "app" {
  name        = "${local.name}-tg"
  port        = var.app_port
  protocol    = "HTTP"
  vpc_id      = var.vpc_id
  target_type = "ip" # Fargate awsvpc tasks register by IP

  health_check {
    path                = "/readyz" # readiness gate -> ALB stops routing during drain
    matcher             = "200"
    interval            = 10
    timeout             = 5
    healthy_threshold   = 2
    unhealthy_threshold = 3
  }

  # Avoid a destroy/replace gap when the TG must change.
  lifecycle { create_before_destroy = true }
  tags = var.tags
}

# HTTPS listener (prod): forward to the app; plus an HTTP->HTTPS redirect.
resource "aws_lb_listener" "https" {
  count             = local.enable_https ? 1 : 0
  load_balancer_arn = aws_lb.this.arn
  port              = 443
  protocol          = "HTTPS"
  ssl_policy        = "ELBSecurityPolicy-TLS13-1-2-2021-06"
  certificate_arn   = var.certificate_arn

  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.app.arn
  }
}

resource "aws_lb_listener" "http_redirect" {
  count             = local.enable_https ? 1 : 0
  load_balancer_arn = aws_lb.this.arn
  port              = 80
  protocol          = "HTTP"
  default_action {
    type = "redirect"
    redirect {
      port        = "443"
      protocol    = "HTTPS"
      status_code = "HTTP_301"
    }
  }
}

# HTTP-only listener (dev, no cert): forward directly.
resource "aws_lb_listener" "http" {
  count             = local.enable_https ? 0 : 1
  load_balancer_arn = aws_lb.this.arn
  port              = 80
  protocol          = "HTTP"
  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.app.arn
  }
}
