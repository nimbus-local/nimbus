# Real subnets, not placeholder IDs: the group has to resolve them back to
# their VPC and AZ, and the DB resources below inherit a dependency on them
# (#105) so Terraform tears the databases down before the subnets.
resource "aws_db_subnet_group" "nimbus_test" {
  name        = var.prefix
  description = "Nimbus test subnet group"
  # Real subnet IDs, spanning two AZs — real AWS requires a DB subnet group to
  # cover at least two Availability Zones.
  subnet_ids = [
    aws_subnet.nimbus_test_public.id,
    aws_subnet.nimbus_test_private.id,
  ]
}

resource "aws_rds_cluster_parameter_group" "nimbus_test" {
  name        = var.prefix
  family      = "aurora-postgresql16"
  description = "Nimbus test cluster parameter group"
}

resource "aws_db_parameter_group" "nimbus_test" {
  name        = var.prefix
  family      = "aurora-postgresql16"
  description = "Nimbus test instance parameter group"
}

resource "aws_rds_cluster" "nimbus_test" {
  cluster_identifier              = var.prefix
  engine                          = "aurora-postgresql"
  engine_mode                     = "provisioned"
  engine_version                  = "16.1"
  database_name                   = "nimbus"
  master_username                 = "nimbus"
  master_password                 = "nimbuspass"
  db_subnet_group_name            = aws_db_subnet_group.nimbus_test.name
  db_cluster_parameter_group_name = aws_rds_cluster_parameter_group.nimbus_test.name
  skip_final_snapshot             = true

  serverlessv2_scaling_configuration {
    min_capacity = 0.5
    max_capacity = 1
  }
}

resource "aws_rds_cluster_instance" "nimbus_test" {
  identifier         = "${var.prefix}-instance-1"
  cluster_identifier = aws_rds_cluster.nimbus_test.id
  instance_class     = "db.serverless"
  engine             = aws_rds_cluster.nimbus_test.engine
  engine_version     = aws_rds_cluster.nimbus_test.engine_version

  performance_insights_enabled          = true
  performance_insights_retention_period = 7
}

# Standalone (non-Aurora) instance. Having a second DB instance locks the
# regression where DescribeDBInstances ignored the db-instance-id filter and
# the provider's singleton read failed with "too many results" (#95).
resource "aws_db_instance" "nimbus_test_standalone" {
  identifier           = "${var.prefix}-standalone"
  engine               = "postgres"
  engine_version       = "16.1"
  instance_class       = "db.t3.micro"
  db_name              = "nimbus"
  username             = "nimbus"
  password             = "nimbuspass"
  allocated_storage    = 20
  skip_final_snapshot  = true
  db_subnet_group_name = aws_db_subnet_group.nimbus_test.name
}

# ── RDS Proxy ────────────────────────────────────────────────────────────────
# The proxy fronts the Aurora cluster above. Nimbus does no pooling of its own —
# the proxy endpoint resolves to the same Postgres sidecar the cluster does — but
# the control-plane surface is what argus discovers and what these resources
# exercise: aws_db_proxy, the default target group, and a cluster target.

resource "aws_iam_role" "db_proxy" {
  name = "${var.prefix}-db-proxy"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Action    = "sts:AssumeRole"
      Effect    = "Allow"
      Principal = { Service = "rds.amazonaws.com" }
    }]
  })
}

resource "aws_db_proxy" "nimbus_test" {
  name                = var.prefix
  engine_family       = "POSTGRESQL"
  role_arn            = aws_iam_role.db_proxy.arn
  vpc_subnet_ids      = [aws_subnet.nimbus_test_public.id, aws_subnet.nimbus_test_private.id]
  require_tls         = true
  idle_client_timeout = 900
  debug_logging       = false

  # Nimbus accepts the secret without reading it, but it must round-trip.
  auth {
    auth_scheme = "SECRETS"
    description = "Aurora credentials"
    iam_auth    = "DISABLED"
    secret_arn  = aws_secretsmanager_secret.nimbus_test.arn
  }

  tags = {
    Name = var.prefix
  }
}

# Modifies the "default" target group Nimbus creates alongside the proxy — this
# resource never creates one, so the group has to exist already.
resource "aws_db_proxy_default_target_group" "nimbus_test" {
  db_proxy_name = aws_db_proxy.nimbus_test.name

  connection_pool_config {
    max_connections_percent      = 75
    max_idle_connections_percent = 25
    connection_borrow_timeout    = 30
  }
}

resource "aws_db_proxy_target" "nimbus_test" {
  db_proxy_name         = aws_db_proxy.nimbus_test.name
  target_group_name     = aws_db_proxy_default_target_group.nimbus_test.name
  db_cluster_identifier = aws_rds_cluster.nimbus_test.id
}
