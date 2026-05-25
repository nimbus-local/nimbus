resource "aws_kinesis_stream" "nimbus_test" {
  name        = "${var.prefix}-nimbus-stream"
  shard_count = 2

  retention_period = 24

  tags = {
    Name = var.prefix
  }
}

resource "aws_lambda_event_source_mapping" "kinesis" {
  event_source_arn  = aws_kinesis_stream.nimbus_test.arn
  function_name     = aws_lambda_function.nimbus_test.function_name
  starting_position = "TRIM_HORIZON"
  batch_size        = 10
}
