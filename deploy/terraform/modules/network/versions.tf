# Module-local provider requirements (tflint terraform_required_* / module
# standard structure). The provider is CONFIGURED in the root; modules only
# declare what they require, keeping them self-describing and reusable.
terraform {
  required_version = ">= 1.6.0, < 2.0.0"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.60"
    }
  }
}
