resource "aws_ecr_repository" "nimbus_test" {
  name         = var.prefix
  force_delete = true
}
