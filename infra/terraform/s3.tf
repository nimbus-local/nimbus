resource "aws_s3_bucket" "nimbus_test" {
  bucket        = var.prefix
  force_destroy = true
}
