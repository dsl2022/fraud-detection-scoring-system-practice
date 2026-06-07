# PARTIAL backend: the bucket/key/region/lock table are supplied per environment
# at init time so each env has its OWN state file and lock, e.g.:
#
#   terraform init -backend-config=envs/dev.backend.hcl
#
# Remote state in S3 gives durable, shared, encrypted state; the DynamoDB table
# provides state LOCKING so two applies can't corrupt state. The state bucket +
# lock table are bootstrapped once, out of band (see envs/README), because a
# backend can't create the very resources it stores its state in.
terraform {
  backend "s3" {
    encrypt = true
    # bucket, key, region, dynamodb_table come from -backend-config (envs/*.hcl)
  }
}
