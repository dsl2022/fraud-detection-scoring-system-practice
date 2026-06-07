# GitHub Actions -> AWS via OIDC. No long-lived access keys anywhere: GitHub
# mints a short-lived token per run, AWS trusts GitHub's issuer (the OIDC
# provider below), and each workflow assumes a role scoped to a specific repo +
# trigger via the token's `sub` claim.
#
# Three roles mirror the CI's separation of duties (see .github/workflows):
#   * plan   (read-only)  -> infra.yml plan job   (pull_request)
#   * apply  (write)      -> infra.yml apply job  (push to main / env-gated)
#   * deploy (app)        -> app-deploy.yml       (push to main)

data "aws_caller_identity" "current" {}

# --- OIDC identity provider (account-wide, create once) ---------------------

# GitHub's well-known OIDC discovery cert; its SHA1 fingerprint is the thumbprint
# AWS pins. Reading it dynamically avoids a hardcoded value that rotates.
data "tls_certificate" "github" {
  url = "https://token.actions.githubusercontent.com/.well-known/openid-configuration"
}

resource "aws_iam_openid_connect_provider" "github" {
  url             = "https://token.actions.githubusercontent.com"
  client_id_list  = ["sts.amazonaws.com"]
  thumbprint_list = [data.tls_certificate.github.certificates[0].sha1_fingerprint]
}

# --- trust-policy helper ----------------------------------------------------

locals {
  repo = "${var.github_owner}/${var.github_repo}"

  # The `sub` claim GitHub puts in the token depends on the trigger:
  #   pull_request job          -> repo:OWNER/REPO:pull_request
  #   push to a branch          -> repo:OWNER/REPO:ref:refs/heads/<branch>
  #   job with `environment:`   -> repo:OWNER/REPO:environment:<name>
  sub_pull_request = "repo:${local.repo}:pull_request"
  sub_main_branch  = "repo:${local.repo}:ref:refs/heads/${var.default_branch}"
  sub_environments = [for e in var.deploy_environments : "repo:${local.repo}:environment:${e}"]
}

# Builds an sts:AssumeRoleWithWebIdentity trust doc locked to our provider,
# audience, and the given list of allowed `sub` values.
data "aws_iam_policy_document" "trust" {
  for_each = {
    plan   = [local.sub_pull_request]
    apply  = local.sub_environments
    deploy = [local.sub_main_branch]
  }

  statement {
    effect  = "Allow"
    actions = ["sts:AssumeRoleWithWebIdentity"]

    principals {
      type        = "Federated"
      identifiers = [aws_iam_openid_connect_provider.github.arn]
    }

    condition {
      test     = "StringEquals"
      variable = "token.actions.githubusercontent.com:aud"
      values   = ["sts.amazonaws.com"]
    }

    # The security boundary: only these repo+trigger combinations may assume.
    condition {
      test     = "StringLike"
      variable = "token.actions.githubusercontent.com:sub"
      values   = each.value
    }
  }
}

# --- backend access (for the read-only plan role) ---------------------------

# `terraform plan` still reads remote state and takes the DynamoDB lock, which
# ReadOnlyAccess alone doesn't grant. Scope just the state bucket(s) + lock table.
data "aws_iam_policy_document" "tf_backend" {
  statement {
    sid     = "StateBucket"
    effect  = "Allow"
    actions = ["s3:ListBucket", "s3:GetObject", "s3:PutObject"]
    resources = [
      "arn:aws:s3:::${var.state_bucket_prefix}-*",
      "arn:aws:s3:::${var.state_bucket_prefix}-*/*",
    ]
  }
  statement {
    sid       = "StateLock"
    effect    = "Allow"
    actions   = ["dynamodb:GetItem", "dynamodb:PutItem", "dynamodb:DeleteItem"]
    resources = ["arn:aws:dynamodb:${var.region}:${data.aws_caller_identity.current.account_id}:table/${var.lock_table_name}"]
  }
}

resource "aws_iam_policy" "tf_backend" {
  name   = "fraud-signals-tf-backend"
  policy = data.aws_iam_policy_document.tf_backend.json
}

# --- the three roles --------------------------------------------------------

# PLAN: read-only. Inspects resources + reads state to produce a diff for the PR.
resource "aws_iam_role" "plan" {
  name               = "fraud-signals-gha-plan"
  assume_role_policy = data.aws_iam_policy_document.trust["plan"].json
}

resource "aws_iam_role_policy_attachment" "plan_readonly" {
  role       = aws_iam_role.plan.name
  policy_arn = "arn:aws:iam::aws:policy/ReadOnlyAccess"
}

resource "aws_iam_role_policy_attachment" "plan_backend" {
  role       = aws_iam_role.plan.name
  policy_arn = aws_iam_policy.tf_backend.arn
}

# APPLY: provisions the full stack on merge to main. AdministratorAccess for the
# demo — for real use, scope to the services the stack actually touches (VPC,
# ECS, ELB, ECR, Lambda, SQS, DynamoDB, IAM, KMS, CloudWatch, CloudTrail/Config).
resource "aws_iam_role" "apply" {
  name               = "fraud-signals-gha-apply"
  assume_role_policy = data.aws_iam_policy_document.trust["apply"].json
}

resource "aws_iam_role_policy_attachment" "apply_admin" {
  role       = aws_iam_role.apply.name
  policy_arn = "arn:aws:iam::aws:policy/AdministratorAccess"
}

# DEPLOY: app pipeline — build/push images to ECR + scoped `-target=module.app`
# apply (new task def -> rolling ECS deploy + Lambda image update). Admin for the
# demo; minimally it needs ECR push, ECS, Lambda update, iam:PassRole, + backend.
resource "aws_iam_role" "deploy" {
  name               = "fraud-signals-gha-deploy"
  assume_role_policy = data.aws_iam_policy_document.trust["deploy"].json
}

resource "aws_iam_role_policy_attachment" "deploy_admin" {
  role       = aws_iam_role.deploy.name
  policy_arn = "arn:aws:iam::aws:policy/AdministratorAccess"
}
