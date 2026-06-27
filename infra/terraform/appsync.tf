resource "aws_iam_role" "appsync_execution" {
  name = "${var.prefix}-appsync-execution"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Action    = "sts:AssumeRole"
      Effect    = "Allow"
      Principal = { Service = "appsync.amazonaws.com" }
    }]
  })
}

resource "aws_iam_role_policy" "appsync_invoke_lambda" {
  name = "invoke-lambda"
  role = aws_iam_role.appsync_execution.name

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["lambda:InvokeFunction"]
      Resource = "*"
    }]
  })
}

resource "aws_appsync_graphql_api" "nimbus_test" {
  name                = "${var.prefix}-appsync"
  authentication_type = "API_KEY"

  tags = {
    env = var.prefix
  }
}

resource "aws_appsync_api_key" "nimbus_test" {
  api_id      = aws_appsync_graphql_api.nimbus_test.id
  description = "nimbus smoke test key"
}

resource "aws_appsync_datasource" "nimbus_test" {
  api_id           = aws_appsync_graphql_api.nimbus_test.id
  name             = "NimbusLambda"
  type             = "AWS_LAMBDA"
  service_role_arn = aws_iam_role.appsync_execution.arn

  lambda_config {
    function_arn = aws_lambda_function.nimbus_test.arn
  }
}
