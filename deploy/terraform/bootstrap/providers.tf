provider "aws" {
  region = var.region

  default_tags {
    tags = {
      Project   = "fraud-signals"
      Component = "bootstrap"
      ManagedBy = "terraform"
    }
  }
}

# NOTE: no backend block on purpose. This layer is the chicken-and-egg bootstrap
# that creates the OIDC provider + the very roles CI assumes to run apply. It is
# run ONCE, by a human with admin credentials, and keeps LOCAL state
# (terraform.tfstate in this dir). Commit that state to a private vault or keep
# it out of band — it contains role ARNs only, no secrets.
