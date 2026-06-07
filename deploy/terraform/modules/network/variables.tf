variable "env_name" {
  type        = string
  description = "Environment name (dev/stage/prod), used in resource names/tags."
}

variable "vpc_cidr" {
  type        = string
  description = "CIDR block for the VPC."
}

variable "azs" {
  type        = list(string)
  description = "Availability zones to spread subnets across."
}

variable "public_subnet_cidrs" {
  type        = list(string)
  description = "CIDRs for public subnets (ALB + NAT). One per AZ."
}

variable "private_subnet_cidrs" {
  type        = list(string)
  description = "CIDRs for private subnets (tasks + data). One per AZ."
}

variable "single_nat_gateway" {
  type        = bool
  default     = true
  description = "One shared NAT (cheap, dev) vs one per AZ (HA, prod)."
}

variable "app_port" {
  type        = number
  default     = 8080
  description = "Container port the ALB forwards to."
}

variable "tags" {
  type        = map(string)
  default     = {}
  description = "Common tags applied to all resources."
}
