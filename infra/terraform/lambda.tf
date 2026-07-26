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

# Container-image function. The image reference must round-trip through
# GetFunction's Code block or the provider reads image_uri back as empty and
# plans a change on every run — so a clean second plan is what this fixture
# checks. Nimbus never pulls the image, so the tag need not exist.
resource "aws_lambda_function" "nimbus_test_image" {
  function_name = "${var.prefix}-image"
  package_type  = "Image"
  image_uri     = "${aws_ecr_repository.nimbus_test.repository_url}:latest"
  role          = "arn:aws:iam::000000000000:role/lambda-exec"

  memory_size = 2048
  timeout     = 300

  ephemeral_storage {
    size = 1024
  }

  image_config {
    command           = ["app.handler"]
    entry_point       = ["/usr/bin/python3"]
    working_directory = "/var/task"
  }
}
