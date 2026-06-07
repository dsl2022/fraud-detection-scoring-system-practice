env_name = "dev"
region   = "us-east-1"

# Small + cheap: single NAT, modest task size, low floor.
single_nat_gateway = true
desired_count      = 1
cpu                = 256
memory             = 512
autoscale_min      = 1
autoscale_max      = 4

# HTTP-only in dev (no ACM cert). Set certificate_arn to enable HTTPS.
certificate_arn = ""

persist_mode = "as_you_go"

# jwt_secret and server_image/consumer_image are injected by CI (-var=...),
# not committed here.
