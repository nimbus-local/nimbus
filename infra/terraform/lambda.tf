data "archive_file" "lambda" {
  type        = "zip"
  output_path = "${path.module}/lambda_function.zip"
  source {
    filename = "handler.py"
    content  = <<-EOF
      import json

      def handler(event, context):
          return {
              "statusCode": 200,
              "headers": {"Content-Type": "application/json"},
              "body": json.dumps({"message": "hello from nimbus", "input": event}),
          }
    EOF
  }
}

resource "aws_lambda_function" "nimbus_test" {
  function_name    = var.prefix
  filename         = data.archive_file.lambda.output_path
  source_code_hash = data.archive_file.lambda.output_base64sha256
  handler          = "handler.handler"
  runtime          = "python3.12"
  # Nimbus doesn't implement IAM — any syntactically valid ARN is accepted
  role = "arn:aws:iam::000000000000:role/lambda-exec"

  environment {
    variables = {
      STAGE = "local"
    }
  }
}
