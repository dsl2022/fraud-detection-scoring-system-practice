# Remote state backend for the main stack: one S3 bucket per env + a shared
# DynamoDB lock table. The main stack's backend.tf points here via
# envs/<env>.backend.hcl, so the bucket names MUST match those files.
#
# This replaces the manual `aws s3api ...` / `aws dynamodb ...` bootstrap that
# used to live in envs/README — now it's reproducible IaC under local state.

locals {
  state_buckets = { for e in var.state_bucket_envs : e => "${var.state_bucket_prefix}-${e}" }
}

# --- state buckets ----------------------------------------------------------

resource "aws_s3_bucket" "state" {
  for_each = local.state_buckets
  bucket   = each.value

  # State is the source of truth for live infra; guard against accidental
  # `terraform destroy` of this bootstrap nuking every env's state.
  lifecycle {
    prevent_destroy = true
  }
}

# Versioning lets you recover from a corrupt/rolled-back state write.
resource "aws_s3_bucket_versioning" "state" {
  for_each = aws_s3_bucket.state
  bucket   = each.value.id
  versioning_configuration {
    status = "Enabled"
  }
}

# Encrypt at rest with the AWS-managed KMS key (aws/s3).
resource "aws_s3_bucket_server_side_encryption_configuration" "state" {
  for_each = aws_s3_bucket.state
  bucket   = each.value.id
  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "aws:kms"
    }
  }
}

# State can contain sensitive values — never allow public access.
resource "aws_s3_bucket_public_access_block" "state" {
  for_each                = aws_s3_bucket.state
  bucket                  = each.value.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

# Reject any non-TLS request to the state bucket.
data "aws_iam_policy_document" "state_tls_only" {
  for_each = aws_s3_bucket.state
  statement {
    sid       = "DenyInsecureTransport"
    effect    = "Deny"
    actions   = ["s3:*"]
    resources = [each.value.arn, "${each.value.arn}/*"]
    principals {
      type        = "*"
      identifiers = ["*"]
    }
    condition {
      test     = "Bool"
      variable = "aws:SecureTransport"
      values   = ["false"]
    }
  }
}

resource "aws_s3_bucket_policy" "state" {
  for_each = aws_s3_bucket.state
  bucket   = each.value.id
  policy   = data.aws_iam_policy_document.state_tls_only[each.key].json
}

# --- lock table (shared across envs) ----------------------------------------

# Terraform's S3 backend uses this for state LOCKING (LockID is the fixed key
# name the backend expects). PAY_PER_REQUEST: lock traffic is tiny + bursty.
resource "aws_dynamodb_table" "lock" {
  name         = var.lock_table_name
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "LockID"

  attribute {
    name = "LockID"
    type = "S"
  }

  lifecycle {
    prevent_destroy = true
  }
}
