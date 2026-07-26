resource "aws_cloudwatch_log_group" "app" {
  name = "/nimbus/${var.prefix}/app"

  # Both attributes are read back from DescribeLogGroups. If either is dropped
  # server-side the provider sees it as unset and plans a change every run.
  retention_in_days = 14
  kms_key_id        = aws_kms_key.nimbus_test.arn
}

resource "aws_cloudwatch_log_stream" "app" {
  name           = "container"
  log_group_name = aws_cloudwatch_log_group.app.name
}
