# Network module: VPC, public/private subnets across AZs, IGW + NAT, route
# tables, security groups, NACLs, and VPC endpoints. Tasks and data live in
# PRIVATE subnets with no public IPs; only the ALB and NAT sit in public subnets.

data "aws_region" "current" {}

locals {
  name      = "fraud-${var.env_name}"
  nat_count = var.single_nat_gateway ? 1 : length(var.public_subnet_cidrs)
}

resource "aws_vpc" "this" {
  cidr_block           = var.vpc_cidr
  enable_dns_support   = true
  enable_dns_hostnames = true # required for interface VPC endpoints' private DNS
  tags                 = merge(var.tags, { Name = "${local.name}-vpc" })
}

resource "aws_internet_gateway" "this" {
  vpc_id = aws_vpc.this.id
  tags   = merge(var.tags, { Name = "${local.name}-igw" })
}

# ---- subnets ----
resource "aws_subnet" "public" {
  count                   = length(var.public_subnet_cidrs)
  vpc_id                  = aws_vpc.this.id
  cidr_block              = var.public_subnet_cidrs[count.index]
  availability_zone       = var.azs[count.index]
  map_public_ip_on_launch = true
  tags                    = merge(var.tags, { Name = "${local.name}-public-${count.index}", Tier = "public" })
}

resource "aws_subnet" "private" {
  count             = length(var.private_subnet_cidrs)
  vpc_id            = aws_vpc.this.id
  cidr_block        = var.private_subnet_cidrs[count.index]
  availability_zone = var.azs[count.index]
  tags              = merge(var.tags, { Name = "${local.name}-private-${count.index}", Tier = "private" })
}

# ---- NAT (egress for private subnets) ----
resource "aws_eip" "nat" {
  count  = local.nat_count
  domain = "vpc"
  tags   = merge(var.tags, { Name = "${local.name}-nat-eip-${count.index}" })
}

resource "aws_nat_gateway" "this" {
  count         = local.nat_count
  allocation_id = aws_eip.nat[count.index].id
  subnet_id     = aws_subnet.public[count.index].id
  tags          = merge(var.tags, { Name = "${local.name}-nat-${count.index}" })
  depends_on    = [aws_internet_gateway.this]
}

# ---- route tables ----
resource "aws_route_table" "public" {
  vpc_id = aws_vpc.this.id
  tags   = merge(var.tags, { Name = "${local.name}-public-rt" })
}

resource "aws_route" "public_internet" {
  route_table_id         = aws_route_table.public.id
  destination_cidr_block = "0.0.0.0/0"
  gateway_id             = aws_internet_gateway.this.id
}

resource "aws_route_table_association" "public" {
  count          = length(aws_subnet.public)
  subnet_id      = aws_subnet.public[count.index].id
  route_table_id = aws_route_table.public.id
}

# One private route table per AZ so each can use its own NAT when HA is enabled.
resource "aws_route_table" "private" {
  count  = length(var.private_subnet_cidrs)
  vpc_id = aws_vpc.this.id
  tags   = merge(var.tags, { Name = "${local.name}-private-rt-${count.index}" })
}

resource "aws_route" "private_nat" {
  count                  = length(aws_route_table.private)
  route_table_id         = aws_route_table.private[count.index].id
  destination_cidr_block = "0.0.0.0/0"
  # When single_nat_gateway, every private RT points at the one NAT.
  nat_gateway_id = aws_nat_gateway.this[var.single_nat_gateway ? 0 : count.index].id
}

resource "aws_route_table_association" "private" {
  count          = length(aws_subnet.private)
  subnet_id      = aws_subnet.private[count.index].id
  route_table_id = aws_route_table.private[count.index].id
}

# ---- security groups ----
# ALB: accepts 80/443 from the internet.
resource "aws_security_group" "alb" {
  name        = "${local.name}-alb-sg"
  description = "ALB ingress from internet"
  vpc_id      = aws_vpc.this.id
  tags        = merge(var.tags, { Name = "${local.name}-alb-sg" })

  ingress {
    description = "HTTPS"
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }
  ingress {
    description = "HTTP (redirected to HTTPS)"
    from_port   = 80
    to_port     = 80
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }
  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

# Tasks: accept the app port ONLY from the ALB SG (not the internet).
resource "aws_security_group" "tasks" {
  name        = "${local.name}-tasks-sg"
  description = "Fargate tasks: ingress only from ALB"
  vpc_id      = aws_vpc.this.id
  tags        = merge(var.tags, { Name = "${local.name}-tasks-sg" })

  ingress {
    description     = "App port from ALB"
    from_port       = var.app_port
    to_port         = var.app_port
    protocol        = "tcp"
    security_groups = [aws_security_group.alb.id]
  }
  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

# Interface VPC endpoints: accept 443 from the tasks SG.
resource "aws_security_group" "vpce" {
  name        = "${local.name}-vpce-sg"
  description = "Interface VPC endpoints"
  vpc_id      = aws_vpc.this.id
  tags        = merge(var.tags, { Name = "${local.name}-vpce-sg" })

  ingress {
    description     = "HTTPS from tasks"
    from_port       = 443
    to_port         = 443
    protocol        = "tcp"
    security_groups = [aws_security_group.tasks.id]
  }
  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

# ---- NACLs (defense in depth, stateless layer beneath the SGs) ----
resource "aws_network_acl" "private" {
  vpc_id     = aws_vpc.this.id
  subnet_ids = aws_subnet.private[*].id
  tags       = merge(var.tags, { Name = "${local.name}-private-nacl" })

  # Inbound: traffic from within the VPC, plus ephemeral ports for NAT return.
  ingress {
    rule_no    = 100
    action     = "allow"
    protocol   = "-1"
    cidr_block = var.vpc_cidr
    from_port  = 0
    to_port    = 0
  }
  ingress {
    rule_no    = 110
    action     = "allow"
    protocol   = "tcp"
    cidr_block = "0.0.0.0/0"
    from_port  = 1024
    to_port    = 65535
  }
  egress {
    rule_no    = 100
    action     = "allow"
    protocol   = "-1"
    cidr_block = "0.0.0.0/0"
    from_port  = 0
    to_port    = 0
  }
}

# ---- VPC endpoints (private connectivity to AWS services; cheaper + no NAT egress) ----
# S3 + DynamoDB are GATEWAY endpoints (route-table based, free).
resource "aws_vpc_endpoint" "s3" {
  vpc_id            = aws_vpc.this.id
  service_name      = "com.amazonaws.${data.aws_region.current.name}.s3"
  vpc_endpoint_type = "Gateway"
  route_table_ids   = aws_route_table.private[*].id
  tags              = merge(var.tags, { Name = "${local.name}-vpce-s3" })
}

resource "aws_vpc_endpoint" "dynamodb" {
  vpc_id            = aws_vpc.this.id
  service_name      = "com.amazonaws.${data.aws_region.current.name}.dynamodb"
  vpc_endpoint_type = "Gateway"
  route_table_ids   = aws_route_table.private[*].id
  tags              = merge(var.tags, { Name = "${local.name}-vpce-dynamodb" })
}

# Interface endpoints for the services tasks call directly.
locals {
  interface_endpoints = toset([
    "ecr.api", "ecr.dkr", "logs", "sqs", "ssm", "secretsmanager", "kms",
  ])
}

resource "aws_vpc_endpoint" "interface" {
  for_each            = local.interface_endpoints
  vpc_id              = aws_vpc.this.id
  service_name        = "com.amazonaws.${data.aws_region.current.name}.${each.value}"
  vpc_endpoint_type   = "Interface"
  subnet_ids          = aws_subnet.private[*].id
  security_group_ids  = [aws_security_group.vpce.id]
  private_dns_enabled = true
  tags                = merge(var.tags, { Name = "${local.name}-vpce-${each.value}" })
}
