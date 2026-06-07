# Root variables. Per-env values come from envs/<env>.tfvars.

variable "env_name" {
  type        = string
  description = "dev | stage | prod"
}

variable "region" {
  type    = string
  default = "us-east-1"
}

# ---- network ----
variable "vpc_cidr" {
  type    = string
  default = "10.0.0.0/16"
}
variable "azs" {
  type    = list(string)
  default = ["us-east-1a", "us-east-1b"]
}
variable "public_subnet_cidrs" {
  type    = list(string)
  default = ["10.0.0.0/24", "10.0.1.0/24"]
}
variable "private_subnet_cidrs" {
  type    = list(string)
  default = ["10.0.10.0/24", "10.0.11.0/24"]
}
variable "single_nat_gateway" {
  type    = bool
  default = true
}

# ---- platform ----
variable "certificate_arn" {
  type    = string
  default = ""
}
variable "log_retention_days" {
  type    = number
  default = 30
}

# ---- app images (set by CI to the freshly pushed tags) ----
variable "server_image" {
  type    = string
  default = ""
}
variable "consumer_image" {
  type    = string
  default = ""
}

# ---- app sizing (prod overrides) ----
variable "desired_count" {
  type    = number
  default = 2
}
variable "cpu" {
  type    = number
  default = 512
}
variable "memory" {
  type    = number
  default = 1024
}
variable "autoscale_min" {
  type    = number
  default = 2
}
variable "autoscale_max" {
  type    = number
  default = 10
}
variable "lambda_max_concurrency" {
  type    = number
  default = 10
}

# ---- app config / secrets ----
variable "jwt_secret" {
  type      = string
  sensitive = true
  default   = "" # MUST be provided per env (CI from Secrets Manager); never commit.
}
variable "persist_mode" {
  type    = string
  default = "combined"
}
variable "alarm_email" {
  type    = string
  default = ""
}

# ---- governance ----
variable "enable_account_audit" {
  type        = bool
  default     = false
  description = "Provision CloudTrail + AWS Config (account/region singletons)."
}
