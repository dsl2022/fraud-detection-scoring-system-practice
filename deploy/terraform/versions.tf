terraform {
  # Pin Terraform and the provider so plans are reproducible across machines/CI.
  required_version = ">= 1.6.0, < 2.0.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.61"
    }
  }
}
