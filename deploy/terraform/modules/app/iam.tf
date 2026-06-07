# IAM — least privilege, with the critical SEPARATION the spec calls out:
#
#   * EXECUTION role: what ECS/Fargate needs to START the task — pull the image
#     from ECR, write logs, and fetch the JWT secret from SSM. Used by the agent,
#     not by app code.
#   * TASK role: what the APP's code may call at runtime — only sqs:SendMessage to
#     the event queue (+ KMS for SSE). It cannot pull images or read other secrets.
#   * LAMBDA role: only what the consumer needs — receive/delete from the queue,
#     write to the audit table, and use the CMK.
#
# No long-lived keys anywhere: every principal assumes a role.

data "aws_iam_policy_document" "ecs_assume" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["ecs-tasks.amazonaws.com"]
    }
  }
}

# ---- task EXECUTION role ----
resource "aws_iam_role" "execution" {
  name               = "${local.name}-exec"
  assume_role_policy = data.aws_iam_policy_document.ecs_assume.json
  tags               = var.tags
}

resource "aws_iam_role_policy_attachment" "execution_managed" {
  role       = aws_iam_role.execution.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy"
}

# Let the agent read the JWT secret and decrypt it with the CMK at task start.
data "aws_iam_policy_document" "execution_extra" {
  statement {
    sid       = "ReadJWTSecret"
    actions   = ["ssm:GetParameters"]
    resources = [aws_ssm_parameter.jwt_secret.arn]
  }
  statement {
    sid       = "DecryptForSecret"
    actions   = ["kms:Decrypt"]
    resources = [var.kms_key_arn]
  }
}

resource "aws_iam_role_policy" "execution_extra" {
  name   = "${local.name}-exec-extra"
  role   = aws_iam_role.execution.id
  policy = data.aws_iam_policy_document.execution_extra.json
}

# ---- TASK role (app runtime) ----
resource "aws_iam_role" "task" {
  name               = "${local.name}-task"
  assume_role_policy = data.aws_iam_policy_document.ecs_assume.json
  tags               = var.tags
}

data "aws_iam_policy_document" "task" {
  statement {
    sid       = "PublishEvents"
    actions   = ["sqs:SendMessage", "sqs:GetQueueUrl"]
    resources = [aws_sqs_queue.events.arn]
  }
  # Needed to encrypt the message under the queue's CMK SSE.
  statement {
    sid       = "KMSForQueue"
    actions   = ["kms:GenerateDataKey", "kms:Decrypt"]
    resources = [var.kms_key_arn]
  }
}

resource "aws_iam_role_policy" "task" {
  name   = "${local.name}-task"
  role   = aws_iam_role.task.id
  policy = data.aws_iam_policy_document.task.json
}

# ---- LAMBDA execution role ----
data "aws_iam_policy_document" "lambda_assume" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["lambda.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "lambda" {
  name               = "${local.name}-lambda"
  assume_role_policy = data.aws_iam_policy_document.lambda_assume.json
  tags               = var.tags
}

resource "aws_iam_role_policy_attachment" "lambda_basic" {
  role       = aws_iam_role.lambda.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"
}

data "aws_iam_policy_document" "lambda" {
  statement {
    sid       = "ConsumeQueue"
    actions   = ["sqs:ReceiveMessage", "sqs:DeleteMessage", "sqs:GetQueueAttributes"]
    resources = [aws_sqs_queue.events.arn]
  }
  statement {
    sid       = "WriteAudit"
    actions   = ["dynamodb:PutItem"]
    resources = [aws_dynamodb_table.audit.arn]
  }
  statement {
    sid       = "KMSForQueueAndTable"
    actions   = ["kms:GenerateDataKey", "kms:Decrypt"]
    resources = [var.kms_key_arn]
  }
}

resource "aws_iam_role_policy" "lambda" {
  name   = "${local.name}-lambda"
  role   = aws_iam_role.lambda.id
  policy = data.aws_iam_policy_document.lambda.json
}

# ---- JWT secret in SSM (SecureString, CMK-encrypted) ----
resource "aws_ssm_parameter" "jwt_secret" {
  name   = "/fraud/${var.env_name}/jwt-secret"
  type   = "SecureString"
  value  = var.jwt_secret
  key_id = var.kms_key_arn
  tags   = var.tags
}
