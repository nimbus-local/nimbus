resource "aws_scheduler_schedule_group" "nimbus_test" {
  name = var.prefix
}

resource "aws_scheduler_schedule" "nimbus_test" {
  name       = var.prefix
  group_name = aws_scheduler_schedule_group.nimbus_test.name

  flexible_time_window {
    mode = "OFF"
  }

  schedule_expression = "rate(5 minutes)"

  target {
    arn      = aws_lambda_function.nimbus_test.arn
    role_arn = aws_iam_role.task_execution.arn
  }
}
