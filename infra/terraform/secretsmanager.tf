resource "aws_secretsmanager_secret" "nimbus_test" {
  name                    = "${var.prefix}/db-password"
  recovery_window_in_days = 0
}

resource "aws_secretsmanager_secret_version" "nimbus_test" {
  secret_id     = aws_secretsmanager_secret.nimbus_test.id
  secret_string = jsonencode({ username = "admin", password = "nimbus-secret-42" })
}
