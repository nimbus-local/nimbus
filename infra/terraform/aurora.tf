resource "aws_db_subnet_group" "nimbus_test" {
  name        = var.prefix
  description = "Nimbus test subnet group"
  subnet_ids = [
    "subnet-00000000000000001",
    "subnet-00000000000000002",
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
