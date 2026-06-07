# Bootstrap inputs. These rarely change after the first apply.

variable "region" {
  type    = string
  default = "us-east-1"
}

variable "github_owner" {
  type        = string
  description = "GitHub org/user that owns the repo."
  default     = "dsl2022"
}

variable "github_repo" {
  type        = string
  description = "Repository name (without owner)."
  default     = "fraud-detection-scoring-system-practice"
}

# The OIDC sub claim differs per trigger; these let the trust policies pin
# exactly which refs/events may assume each role (see github-oidc.tf).
variable "default_branch" {
  type    = string
  default = "main"
}

variable "deploy_environments" {
  type        = list(string)
  description = "GitHub Environments the infra APPLY role may run under."
  default     = ["dev", "stage", "prod"]
}

# Remote state backend (S3 + DynamoDB lock) for the MAIN stack. Created here
# because a backend can't provision the bucket/table it stores its own state in
# (chicken-and-egg) — same reason the OIDC roles live in this run-once layer.
# The read-only PLAN role is also granted scoped access to these (see github-oidc.tf).
variable "state_bucket_prefix" {
  type        = string
  description = "S3 state bucket name prefix; '-<env>' is appended per env."
  default     = "fraud-signals-tfstate"
}

# One state bucket per env. Must match the bucket names in envs/<env>.backend.hcl.
variable "state_bucket_envs" {
  type        = list(string)
  description = "Envs that get their own state bucket."
  default     = ["dev", "prod"]
}

variable "lock_table_name" {
  type    = string
  default = "fraud-signals-tflock"
}
