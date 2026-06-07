# Root composition: wire the three layers (network -> platform -> app) plus the
# optional governance module. Modules are wired by OUTPUT VALUES (e.g.
# module.network.vpc_id flows into platform/app). This is the single-state
# composition; if you preferred a state file per layer, you'd publish each
# module's outputs and read them via `terraform_remote_state` data sources
# instead — same wiring, different blast radius (see the Terraform ADR).

locals {
  # Prod gets HA NAT regardless of the tfvar default.
  single_nat = var.env_name == "prod" ? false : var.single_nat_gateway

  # Image source of truth (decouples the two pipelines that share this state):
  #   * app-deploy.yml passes -var server_image/consumer_image (the SHA it just
  #     built) AND records it to SSM.
  #   * infra.yml does a full apply with NO image var; without this it would
  #     recompute the image to ":latest" and clobber whatever app-deploy
  #     deployed. Reading the LAST-DEPLOYED image from SSM keeps a structure-only
  #     infra apply from rolling the app back. Explicit -var still wins.
  server_image   = var.server_image != "" ? var.server_image : data.aws_ssm_parameter.server_image.value
  consumer_image = var.consumer_image != "" ? var.consumer_image : data.aws_ssm_parameter.consumer_image.value
}

# Last-deployed image URIs, written by app-deploy.yml after a successful push.
# Seeded once at bring-up (see envs/README). Not managed by TF on purpose —
# images are injected out of band, same philosophy as jwt_secret.
data "aws_ssm_parameter" "server_image" {
  name = "/fraud-signals/${var.env_name}/server_image"
}

data "aws_ssm_parameter" "consumer_image" {
  name = "/fraud-signals/${var.env_name}/consumer_image"
}

module "network" {
  source = "./modules/network"

  env_name             = var.env_name
  vpc_cidr             = var.vpc_cidr
  azs                  = var.azs
  public_subnet_cidrs  = var.public_subnet_cidrs
  private_subnet_cidrs = var.private_subnet_cidrs
  single_nat_gateway   = local.single_nat
}

module "platform" {
  source = "./modules/platform"

  env_name              = var.env_name
  vpc_id                = module.network.vpc_id
  public_subnet_ids     = module.network.public_subnet_ids
  alb_security_group_id = module.network.alb_security_group_id
  certificate_arn       = var.certificate_arn
  log_retention_days    = var.log_retention_days
}

module "app" {
  source = "./modules/app"

  env_name = var.env_name

  # platform wiring
  cluster_arn             = module.platform.cluster_arn
  cluster_name            = module.platform.cluster_name
  kms_key_arn             = module.platform.kms_key_arn
  target_group_arn        = module.platform.target_group_arn
  alb_arn_suffix          = module.platform.alb_arn_suffix
  target_group_arn_suffix = module.platform.target_group_arn_suffix
  app_log_group_name      = module.platform.app_log_group_name
  consumer_log_group_name = module.platform.consumer_log_group_name

  # network wiring
  private_subnet_ids      = module.network.private_subnet_ids
  tasks_security_group_id = module.network.tasks_security_group_id

  # Image to run: explicit -var (app-deploy) else the last-deployed image from
  # SSM (see locals above). Never a bare ":latest" that may not exist.
  server_image   = local.server_image
  consumer_image = local.consumer_image

  # sizing / config
  desired_count          = var.desired_count
  cpu                    = var.cpu
  memory                 = var.memory
  autoscale_min          = var.autoscale_min
  autoscale_max          = var.autoscale_max
  lambda_max_concurrency = var.lambda_max_concurrency
  persist_mode           = var.persist_mode
  jwt_secret             = var.jwt_secret
  alarm_email            = var.alarm_email
}

module "audit_trail" {
  source   = "./modules/audit_trail"
  count    = var.enable_account_audit ? 1 : 0
  env_name = var.env_name
}
