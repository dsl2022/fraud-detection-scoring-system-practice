# Wire these ARNs into the repo's GitHub Actions secrets (see README):
#   AWS_PLAN_ROLE_ARN   <- plan_role_arn
#   AWS_APPLY_ROLE_ARN  <- apply_role_arn
#   AWS_DEPLOY_ROLE_ARN <- deploy_role_arn

output "oidc_provider_arn" {
  value = aws_iam_openid_connect_provider.github.arn
}

output "plan_role_arn" {
  value = aws_iam_role.plan.arn
}

output "apply_role_arn" {
  value = aws_iam_role.apply.arn
}

output "deploy_role_arn" {
  value = aws_iam_role.deploy.arn
}

output "state_buckets" {
  description = "Per-env state buckets (wire into envs/<env>.backend.hcl)."
  value       = { for e, b in aws_s3_bucket.state : e => b.id }
}

output "lock_table" {
  value = aws_dynamodb_table.lock.name
}
