resource "aws_sqs_queue" "nimbus_test" {
  name                       = var.prefix
  message_retention_seconds  = 86400
  visibility_timeout_seconds = 30
}

resource "aws_sqs_queue" "nimbus_test_dlq" {
  name = "${var.prefix}-dlq"
}
