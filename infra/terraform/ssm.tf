resource "aws_ssm_parameter" "nimbus_test_string" {
  name  = "/${var.prefix}/db-host"
  type  = "String"
  value = "db.nimbus.local"
}

resource "aws_ssm_parameter" "nimbus_test_secure" {
  name  = "/${var.prefix}/api-key"
  type  = "SecureString"
  value = "super-secret-api-key"
}
