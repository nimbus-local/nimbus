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
