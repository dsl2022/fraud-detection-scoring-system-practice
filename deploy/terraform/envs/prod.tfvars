env_name = "prod"
region   = "us-east-1"

# HA + headroom: NAT per AZ, larger tasks, higher floor/ceiling.
single_nat_gateway = false
desired_count      = 3
cpu                = 1024
memory             = 2048
autoscale_min      = 3
autoscale_max      = 30

lambda_max_concurrency = 50

# Provide a real ACM cert ARN to enable the HTTPS listener + HTTP->HTTPS redirect.
certificate_arn = ""

persist_mode = "combined"
alarm_email  = "oncall@example.com"

# Enable the account-wide governance plane (CloudTrail + Config) once, in prod.
enable_account_audit = true
