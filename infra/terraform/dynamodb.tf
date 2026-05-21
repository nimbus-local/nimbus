resource "aws_dynamodb_table" "nimbus_test" {
  count        = var.enable_dynamodb ? 1 : 0
  name         = var.prefix
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "pk"
  range_key    = "sk"

  attribute {
    name = "pk"
    type = "S"
  }

  attribute {
    name = "sk"
    type = "S"
  }
}
