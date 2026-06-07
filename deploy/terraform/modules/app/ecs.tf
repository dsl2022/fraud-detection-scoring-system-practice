# ECS Fargate task definition + service + autoscaling.

data "aws_region" "current" {}

resource "aws_ecs_task_definition" "app" {
  family                   = local.name
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = var.cpu
  memory                   = var.memory
  execution_role_arn       = aws_iam_role.execution.arn
  task_role_arn            = aws_iam_role.task.arn

  container_definitions = jsonencode([
    {
      name      = "server"
      image     = var.server_image
      essential = true
      portMappings = [
        { containerPort = var.app_port, protocol = "tcp" },
        { containerPort = 9090, protocol = "tcp" } # gRPC
      ]
      environment = [
        { name = "PORT", value = tostring(var.app_port) },
        { name = "GRPC_PORT", value = "9090" },
        { name = "AUTH_ENABLED", value = "true" },
        { name = "ASYNC_ENABLED", value = "true" },
        { name = "AUDIT_QUEUE_URL", value = aws_sqs_queue.events.url },
        { name = "PERSIST_MODE", value = var.persist_mode },
        { name = "METRICS_EMF", value = "true" },
        { name = "METRICS_NAMESPACE", value = var.metrics_namespace },
        { name = "AWS_REGION", value = data.aws_region.current.name },
      ]
      # JWT secret injected from SSM by the execution role at start (never in env/code).
      secrets = [
        { name = "JWT_SECRET", valueFrom = aws_ssm_parameter.jwt_secret.arn }
      ]
      logConfiguration = {
        logDriver = "awslogs"
        options = {
          "awslogs-group"         = var.app_log_group_name
          "awslogs-region"        = data.aws_region.current.name
          "awslogs-stream-prefix" = "server"
        }
      }
      # No container HEALTHCHECK: the distroless image has no shell/curl. The ALB
      # target group health check (/readyz) is the source of truth for health.
    }
  ])

  tags = var.tags
}

resource "aws_ecs_service" "app" {
  name            = local.name
  cluster         = var.cluster_arn
  task_definition = aws_ecs_task_definition.app.arn
  desired_count   = var.desired_count
  launch_type     = "FARGATE"

  # Rolling deploy by default: never drop below full capacity, allow 2x during
  # the roll for zero-downtime. (Blue/green via CodeDeploy is documented in the
  # ADR; switch deployment_controller to CODE_DEPLOY to adopt it.)
  deployment_controller { type = "ECS" }
  deployment_minimum_healthy_percent = 100
  deployment_maximum_percent         = 200

  network_configuration {
    subnets          = var.private_subnet_ids
    security_groups  = [var.tasks_security_group_id]
    assign_public_ip = false # tasks are private; egress via NAT/VPC endpoints
  }

  load_balancer {
    target_group_arn = var.target_group_arn
    container_name   = "server"
    container_port   = var.app_port
  }

  health_check_grace_period_seconds = 30

  # Autoscaling owns desired_count at runtime; don't let TF revert it.
  lifecycle { ignore_changes = [desired_count] }

  tags = var.tags
}

# ---- autoscaling: CPU + ALB request-count-per-target ----
resource "aws_appautoscaling_target" "ecs" {
  service_namespace  = "ecs"
  resource_id        = "service/${var.cluster_name}/${aws_ecs_service.app.name}"
  scalable_dimension = "ecs:service:DesiredCount"
  min_capacity       = var.autoscale_min
  max_capacity       = var.autoscale_max
}

resource "aws_appautoscaling_policy" "cpu" {
  name               = "${local.name}-cpu"
  policy_type        = "TargetTrackingScaling"
  service_namespace  = aws_appautoscaling_target.ecs.service_namespace
  resource_id        = aws_appautoscaling_target.ecs.resource_id
  scalable_dimension = aws_appautoscaling_target.ecs.scalable_dimension

  target_tracking_scaling_policy_configuration {
    predefined_metric_specification {
      predefined_metric_type = "ECSServiceAverageCPUUtilization"
    }
    target_value = var.cpu_target
  }
}

resource "aws_appautoscaling_policy" "alb_requests" {
  name               = "${local.name}-alb-rpt"
  policy_type        = "TargetTrackingScaling"
  service_namespace  = aws_appautoscaling_target.ecs.service_namespace
  resource_id        = aws_appautoscaling_target.ecs.resource_id
  scalable_dimension = aws_appautoscaling_target.ecs.scalable_dimension

  target_tracking_scaling_policy_configuration {
    predefined_metric_specification {
      predefined_metric_type = "ALBRequestCountPerTarget"
      # resource_label ties the policy to THIS ALB + target group.
      resource_label = "${var.alb_arn_suffix}/${var.target_group_arn_suffix}"
    }
    target_value = var.requests_per_target
  }
}
