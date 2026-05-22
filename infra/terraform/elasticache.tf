resource "aws_elasticache_subnet_group" "nimbus_test" {
  name        = var.prefix
  description = "Nimbus test cache subnet group"
  subnet_ids = [
    "subnet-00000000000000001",
    "subnet-00000000000000002",
  ]
}

resource "aws_elasticache_parameter_group" "nimbus_test" {
  name   = var.prefix
  family = "valkey7"
}

resource "aws_elasticache_replication_group" "nimbus_test" {
  replication_group_id = var.prefix
  description          = "Nimbus test Valkey replication group"
  engine               = "valkey"
  engine_version       = "7.2"
  node_type            = "cache.t3.micro"
  num_cache_clusters   = 1
  subnet_group_name    = aws_elasticache_subnet_group.nimbus_test.name
  parameter_group_name = aws_elasticache_parameter_group.nimbus_test.name
}
