variable "env_name" { type = string }
variable "tags" {
  type    = map(string)
  default = {}
}

# From platform/network modules.
variable "cluster_arn" { type = string }
variable "cluster_name" { type = string }
variable "private_subnet_ids" { type = list(string) }
variable "tasks_security_group_id" { type = string }
variable "kms_key_arn" { type = string }
variable "target_group_arn" { type = string }
variable "alb_arn_suffix" { type = string }
variable "target_group_arn_suffix" { type = string }
variable "app_log_group_name" { type = string }
variable "consumer_log_group_name" { type = string }

# Images (pushed to ECR by CI before apply).
variable "server_image" {
  type        = string
  description = "Full server image URI incl. tag."
}
variable "consumer_image" {
  type        = string
  description = "Full Lambda consumer image URI incl. tag."
}

# Service sizing (prod overrides via tfvars).
variable "app_port" {
  type    = number
  default = 8080
}
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
variable "cpu_target" {
  type    = number
  default = 60
}
variable "requests_per_target" {
  type        = number
  default     = 1000
  description = "ALB RequestCountPerTarget scale target."
}

# App config.
variable "jwt_secret" {
  type      = string
  sensitive = true
}
variable "persist_mode" {
  type    = string
  default = "combined"
}
variable "metrics_namespace" {
  type    = string
  default = "FraudSignals"
}
variable "provider_names" {
  type        = list(string)
  default     = ["credit_bureau", "identity_verify", "txn_history"]
  description = "Vendors to create per-vendor latency alarms for."
}

# Async consumer.
variable "lambda_timeout" {
  type    = number
  default = 30
}
variable "lambda_memory" {
  type    = number
  default = 256
}
variable "lambda_batch_size" {
  type    = number
  default = 10
}
variable "lambda_max_concurrency" {
  type        = number
  default     = 10
  description = "Max concurrent Lambda invocations from the queue (SQS-depth scaling)."
}

# Alarming.
variable "p99_latency_ms" {
  type    = number
  default = 200
}
variable "http_5xx_threshold" {
  type    = number
  default = 5
}
variable "per_vendor_p99_ms" {
  type    = number
  default = 150
}
variable "queue_depth_threshold" {
  type    = number
  default = 1000
}
variable "alarm_email" {
  type        = string
  default     = ""
  description = "Optional email subscribed to the alarm SNS topic."
}
