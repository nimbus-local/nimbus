resource "aws_cloudwatch_log_group" "app" {
  name = "/nimbus/${var.prefix}/app"
}

resource "aws_cloudwatch_log_stream" "app" {
  name           = "container"
  log_group_name = aws_cloudwatch_log_group.app.name
}
