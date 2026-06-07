variable "env_name" { type = string }
variable "vpc_id" { type = string }
variable "public_subnet_ids" { type = list(string) }
variable "alb_security_group_id" { type = string }
variable "app_port" {
  type    = number
  default = 8080
}

variable "certificate_arn" {
  type        = string
  default     = ""
  description = "ACM cert ARN for the HTTPS listener. Empty => HTTP-only (dev)."
}

variable "log_retention_days" {
  type    = number
  default = 30
}

variable "tags" {
  type    = map(string)
  default = {}
}
