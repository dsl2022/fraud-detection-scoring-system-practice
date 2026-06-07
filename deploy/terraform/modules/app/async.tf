# Async pipeline: SQS (+ DLQ), DynamoDB audit table, and the Lambda consumer
# with an SQS event source mapping. All encrypted with the platform CMK.

locals {
  name = "fraud-${var.env_name}"
}

# ---- SQS: main queue + DLQ ----
resource "aws_sqs_queue" "dlq" {
  name                      = "${local.name}-audit-events-dlq"
  message_retention_seconds = 1209600 # 14 days to investigate poison messages
  kms_master_key_id         = var.kms_key_arn
  tags                      = var.tags
}

resource "aws_sqs_queue" "events" {
  name = "${local.name}-audit-events"
  # Visibility must exceed the Lambda timeout (>= timeout; 6x is a safe margin)
  # so a message isn't re-delivered while still being processed.
  visibility_timeout_seconds = var.lambda_timeout * 6
  message_retention_seconds  = 345600 # 4 days
  kms_master_key_id          = var.kms_key_arn
  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.dlq.arn
    maxReceiveCount     = 5
  })
  tags = var.tags
}

# Only this queue may redrive into the DLQ.
resource "aws_sqs_queue_redrive_allow_policy" "dlq" {
  queue_url = aws_sqs_queue.dlq.id
  redrive_allow_policy = jsonencode({
    redrivePermission = "byQueue"
    sourceQueueArns   = [aws_sqs_queue.events.arn]
  })
}

# ---- DynamoDB audit table (write-once via app's conditional puts) ----
resource "aws_dynamodb_table" "audit" {
  name         = "${local.name}-audit"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "pk"

  attribute {
    name = "pk"
    type = "S"
  }

  server_side_encryption {
    enabled     = true
    kms_key_arn = var.kms_key_arn
  }
  point_in_time_recovery { enabled = true }

  # The audit trail is the system of record — never let Terraform destroy it.
  lifecycle { prevent_destroy = true }
  tags = var.tags
}

# ---- Lambda consumer (container image) ----
resource "aws_lambda_function" "consumer" {
  function_name = "${local.name}-consumer"
  role          = aws_iam_role.lambda.arn
  package_type  = "Image"
  image_uri     = var.consumer_image
  timeout       = var.lambda_timeout
  memory_size   = var.lambda_memory

  environment {
    variables = {
      AUDIT_TABLE = aws_dynamodb_table.audit.name
    }
  }

  logging_config {
    log_format = "JSON"
    log_group  = var.consumer_log_group_name
  }

  tags = var.tags
}

# SQS -> Lambda. ReportBatchItemFailures enables partial-batch failure; the
# maximum_concurrency is how the async tier "scales on queue depth" (Lambda's
# native SQS scaling, bounded so we don't stampede DynamoDB).
resource "aws_lambda_event_source_mapping" "sqs" {
  event_source_arn                   = aws_sqs_queue.events.arn
  function_name                      = aws_lambda_function.consumer.arn
  batch_size                         = var.lambda_batch_size
  maximum_batching_window_in_seconds = 5
  function_response_types            = ["ReportBatchItemFailures"]

  scaling_config {
    maximum_concurrency = var.lambda_max_concurrency
  }
}
