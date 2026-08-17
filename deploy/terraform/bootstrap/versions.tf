terraform {
  # Pin Terraform + providers so the bootstrap is reproducible across machines.
  required_version = ">= 1.6.0, < 2.0.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.60"
    }
    # Used to read GitHub's TLS cert at plan time so we never hardcode a
    # thumbprint that can rotate out from under us.
    tls = {
      source  = "hashicorp/tls"
      version = "~> 4.0"
    }
  }
}
