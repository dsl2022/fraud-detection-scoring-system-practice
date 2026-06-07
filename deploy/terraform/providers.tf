provider "aws" {
  region = var.region

  # default_tags stamps every resource for cost allocation + ownership/audit.
  default_tags {
    tags = {
      Project   = "fraud-signals"
      Env       = var.env_name
      ManagedBy = "terraform"
    }
  }
}
