resource "aws_iam_role" "sfn_execution" {
  name = "${var.prefix}-sfn-execution"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Action    = "sts:AssumeRole"
      Effect    = "Allow"
      Principal = { Service = "states.amazonaws.com" }
    }]
  })
}

resource "aws_iam_role_policy" "sfn_invoke_lambda" {
  name = "invoke-lambda"
  role = aws_iam_role.sfn_execution.name

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["lambda:InvokeFunction"]
      Resource = "*"
    }]
  })
}

resource "aws_sfn_state_machine" "nimbus_test" {
  name     = "${var.prefix}-nimbus-sfn"
  role_arn = aws_iam_role.sfn_execution.arn

  definition = jsonencode({
    Comment = "Nimbus smoke test — Pass → Succeed"
    StartAt = "Hello"
    States = {
      Hello = {
        Type   = "Pass"
        Result = { status = "ok" }
        Next   = "Done"
      }
      Done = {
        Type = "Succeed"
      }
    }
  })

  tags = {
    Name = var.prefix
  }
}
