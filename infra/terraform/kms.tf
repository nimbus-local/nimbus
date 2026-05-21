resource "aws_kms_key" "nimbus_test" {
  description             = "Nimbus test key"
  deletion_window_in_days = 7
  enable_key_rotation     = false
}

resource "aws_kms_alias" "nimbus_test" {
  name          = "alias/${var.prefix}"
  target_key_id = aws_kms_key.nimbus_test.key_id
}
