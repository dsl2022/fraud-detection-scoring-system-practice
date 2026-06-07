# CloudWatch alarms (ops telemetry). All notify one SNS topic.
#
# NOTE the distinction the spec asks us to make explicit:
#   * CloudWatch (here) = OPERATIONAL telemetry: latency, errors, queue depth.
#   * CloudTrail + AWS Config (modules/audit_trail) = WHO-DID-WHAT governance.
# They answer different questions and have different audiences.

resource "aws_sns_topic" "alarms" {
  name              = "${local.name}-alarms"
  kms_master_key_id = var.kms_key_arn
  tags              = var.tags
}

resource "aws_sns_topic_subscription" "email" {
  count     = var.alarm_email == "" ? 0 : 1
  topic_arn = aws_sns_topic.alarms.arn
  protocol  = "email"
  endpoint  = var.alarm_email
}

# ---- p99 request latency (the SLO) ----
resource "aws_cloudwatch_metric_alarm" "p99_latency" {
  alarm_name          = "${local.name}-p99-latency"
  alarm_description   = "ALB target p99 response time over SLO"
  namespace           = "AWS/ApplicationELB"
  metric_name         = "TargetResponseTime"
  dimensions          = { LoadBalancer = var.alb_arn_suffix }
  extended_statistic  = "p99"
  period              = 60
  evaluation_periods  = 3
  threshold           = var.p99_latency_ms / 1000 # metric is in seconds
  comparison_operator = "GreaterThanThreshold"
  treat_missing_data  = "notBreaching"
  alarm_actions       = [aws_sns_topic.alarms.arn]
  ok_actions          = [aws_sns_topic.alarms.arn]
  tags                = var.tags
}

# ---- 5xx from targets ----
resource "aws_cloudwatch_metric_alarm" "http_5xx" {
  alarm_name          = "${local.name}-5xx"
  alarm_description   = "Elevated 5xx from the service"
  namespace           = "AWS/ApplicationELB"
  metric_name         = "HTTPCode_Target_5XX_Count"
  dimensions          = { LoadBalancer = var.alb_arn_suffix }
  statistic           = "Sum"
  period              = 60
  evaluation_periods  = 3
  threshold           = var.http_5xx_threshold
  comparison_operator = "GreaterThanThreshold"
  treat_missing_data  = "notBreaching"
  alarm_actions       = [aws_sns_topic.alarms.arn]
  tags                = var.tags
}

# ---- PER-VENDOR latency (the alarm we added after the incident) ----
# Reads the EMF metric the app emits (namespace var.metrics_namespace,
# Provider dimension). One alarm per vendor so a single bad vendor pages us.
resource "aws_cloudwatch_metric_alarm" "vendor_latency" {
  for_each            = toset(var.provider_names)
  alarm_name          = "${local.name}-vendor-${each.value}-p99"
  alarm_description   = "Per-vendor p99 latency high: ${each.value}"
  namespace           = var.metrics_namespace
  metric_name         = "ProviderLatencyMs"
  dimensions          = { Provider = each.value }
  extended_statistic  = "p99"
  period              = 60
  evaluation_periods  = 3
  threshold           = var.per_vendor_p99_ms
  comparison_operator = "GreaterThanThreshold"
  treat_missing_data  = "notBreaching"
  alarm_actions       = [aws_sns_topic.alarms.arn]
  tags                = var.tags
}

# ---- async health: DLQ should be empty; main queue shouldn't back up ----
resource "aws_cloudwatch_metric_alarm" "dlq_not_empty" {
  alarm_name          = "${local.name}-dlq-not-empty"
  alarm_description   = "Messages landed in the DLQ — consumer is failing"
  namespace           = "AWS/SQS"
  metric_name         = "ApproximateNumberOfMessagesVisible"
  dimensions          = { QueueName = aws_sqs_queue.dlq.name }
  statistic           = "Maximum"
  period              = 60
  evaluation_periods  = 1
  threshold           = 0
  comparison_operator = "GreaterThanThreshold"
  treat_missing_data  = "notBreaching"
  alarm_actions       = [aws_sns_topic.alarms.arn]
  tags                = var.tags
}

resource "aws_cloudwatch_metric_alarm" "queue_backlog" {
  alarm_name          = "${local.name}-queue-backlog"
  alarm_description   = "Event queue backing up — consumer not keeping pace"
  namespace           = "AWS/SQS"
  metric_name         = "ApproximateNumberOfMessagesVisible"
  dimensions          = { QueueName = aws_sqs_queue.events.name }
  statistic           = "Maximum"
  period              = 60
  evaluation_periods  = 5
  threshold           = var.queue_depth_threshold
  comparison_operator = "GreaterThanThreshold"
  treat_missing_data  = "notBreaching"
  alarm_actions       = [aws_sns_topic.alarms.arn]
  tags                = var.tags
}
