# Audit-trail module: CloudTrail (API activity — WHO did WHAT) and AWS Config
# (resource configuration history + compliance). This is the GOVERNANCE plane,
# distinct from CloudWatch ops telemetry.
#
# These are account/region singletons (one Config recorder per region; one trail
# of this name). The root gates the whole module behind enable_account_audit so
# normal per-env applies don't collide — typically you enable it once per account.

data "aws_caller_identity" "current" {}

locals {
  name   = "fraud-${var.env_name}"
  bucket = "fraud-${var.env_name}-audit-trail-${data.aws_caller_identity.current.account_id}"
}

# ---- shared log bucket (CloudTrail + Config delivery) ----
resource "aws_s3_bucket" "trail" {
  bucket        = local.bucket
  force_destroy = true
  tags          = var.tags
}

resource "aws_s3_bucket_public_access_block" "trail" {
  bucket                  = aws_s3_bucket.trail.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_versioning" "trail" {
  bucket = aws_s3_bucket.trail.id
  versioning_configuration { status = "Enabled" }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "trail" {
  bucket = aws_s3_bucket.trail.id
  rule {
    apply_server_side_encryption_by_default { sse_algorithm = "AES256" }
  }
}

data "aws_iam_policy_document" "bucket" {
  statement {
    sid       = "CloudTrailAclCheck"
    actions   = ["s3:GetBucketAcl"]
    resources = [aws_s3_bucket.trail.arn]
    principals {
      type        = "Service"
      identifiers = ["cloudtrail.amazonaws.com"]
    }
  }
  statement {
    sid       = "CloudTrailWrite"
    actions   = ["s3:PutObject"]
    resources = ["${aws_s3_bucket.trail.arn}/cloudtrail/AWSLogs/${data.aws_caller_identity.current.account_id}/*"]
    principals {
      type        = "Service"
      identifiers = ["cloudtrail.amazonaws.com"]
    }
    condition {
      test     = "StringEquals"
      variable = "s3:x-amz-acl"
      values   = ["bucket-owner-full-control"]
    }
  }
  statement {
    sid       = "ConfigAclCheck"
    actions   = ["s3:GetBucketAcl"]
    resources = [aws_s3_bucket.trail.arn]
    principals {
      type        = "Service"
      identifiers = ["config.amazonaws.com"]
    }
  }
  statement {
    sid       = "ConfigWrite"
    actions   = ["s3:PutObject"]
    resources = ["${aws_s3_bucket.trail.arn}/config/AWSLogs/${data.aws_caller_identity.current.account_id}/*"]
    principals {
      type        = "Service"
      identifiers = ["config.amazonaws.com"]
    }
    condition {
      test     = "StringEquals"
      variable = "s3:x-amz-acl"
      values   = ["bucket-owner-full-control"]
    }
  }
}

resource "aws_s3_bucket_policy" "trail" {
  bucket = aws_s3_bucket.trail.id
  policy = data.aws_iam_policy_document.bucket.json
}

# ---- CloudTrail ----
resource "aws_cloudtrail" "this" {
  name                          = "${local.name}-trail"
  s3_bucket_name                = aws_s3_bucket.trail.id
  s3_key_prefix                 = "cloudtrail"
  include_global_service_events = true
  is_multi_region_trail         = true
  enable_log_file_validation    = true # tamper-evident digest files
  tags                          = var.tags
  depends_on                    = [aws_s3_bucket_policy.trail]
}

# ---- AWS Config ----
data "aws_iam_policy_document" "config_assume" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["config.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "config" {
  name               = "${local.name}-config"
  assume_role_policy = data.aws_iam_policy_document.config_assume.json
  tags               = var.tags
}

resource "aws_iam_role_policy_attachment" "config" {
  role       = aws_iam_role.config.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWS_ConfigRole"
}

resource "aws_config_configuration_recorder" "this" {
  name     = "${local.name}-recorder"
  role_arn = aws_iam_role.config.arn
  recording_group { all_supported = true }
}

resource "aws_config_delivery_channel" "this" {
  name           = "${local.name}-channel"
  s3_bucket_name = aws_s3_bucket.trail.id
  s3_key_prefix  = "config"
  depends_on     = [aws_config_configuration_recorder.this, aws_s3_bucket_policy.trail]
}

resource "aws_config_configuration_recorder_status" "this" {
  name       = aws_config_configuration_recorder.this.name
  is_enabled = true
  depends_on = [aws_config_delivery_channel.this]
}
