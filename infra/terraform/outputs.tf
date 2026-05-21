output "s3_bucket" {
  value = aws_s3_bucket.nimbus_test.bucket
}

output "sqs_queue_url" {
  value = aws_sqs_queue.nimbus_test.url
}

output "dynamodb_table" {
  value = var.enable_dynamodb ? aws_dynamodb_table.nimbus_test[0].name : null
}

output "secret_arn" {
  value = aws_secretsmanager_secret.nimbus_test.arn
}

output "ssm_string_param" {
  value = aws_ssm_parameter.nimbus_test_string.name
}

output "ssm_secure_param" {
  value = aws_ssm_parameter.nimbus_test_secure.name
}

output "lambda_function_name" {
  value = aws_lambda_function.nimbus_test.function_name
}

output "api_gateway_id" {
  value = aws_api_gateway_rest_api.nimbus_test.id
}

output "api_invoke_url" {
  value = "${var.nimbus_endpoint}/restapis/${aws_api_gateway_rest_api.nimbus_test.id}/v1/_user_request_/hello"
}

output "ecr_repository_url" {
  value = aws_ecr_repository.nimbus_test.repository_url
}

output "ecs_cluster_arn" {
  value = aws_ecs_cluster.nimbus_test.arn
}

output "kms_key_id" {
  value = aws_kms_key.nimbus_test.key_id
}

output "kms_alias" {
  value = aws_kms_alias.nimbus_test.name
}

output "sns_topic_arn" {
  value = aws_sns_topic.nimbus_test.arn
}

output "eventbridge_bus_name" {
  value = aws_cloudwatch_event_bus.nimbus_test.name
}
