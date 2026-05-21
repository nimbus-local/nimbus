resource "aws_sns_topic" "nimbus_test" {
  name = var.prefix
}

# SQS subscription — wires SNS -> SQS for end-to-end fan-out testing
resource "aws_sns_topic_subscription" "nimbus_test_sqs" {
  topic_arn = aws_sns_topic.nimbus_test.arn
  protocol  = "sqs"
  endpoint  = aws_sqs_queue.nimbus_test.arn
}
