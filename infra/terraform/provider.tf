provider "aws" {
  region                      = var.region
  access_key                  = "test"
  secret_key                  = "test"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true

  # Path-style required for Nimbus S3
  s3_use_path_style = true

  # Every service Nimbus emulates needs an entry here. Once this block exists,
  # a service that is missing from it resolves to real AWS — silently, if
  # AWS_ENDPOINT_URL happens to be set in the environment (infra/Makefile
  # exports it, which masks the mistake when running through make).
  endpoints {
    s3             = var.nimbus_endpoint
    sqs            = var.nimbus_endpoint
    dynamodb       = var.nimbus_endpoint
    secretsmanager = var.nimbus_endpoint
    ssm            = var.nimbus_endpoint
    ses            = var.nimbus_endpoint
    sesv2          = var.nimbus_endpoint
    lambda         = var.nimbus_endpoint
    apigateway     = var.nimbus_endpoint
    apigatewayv2   = var.nimbus_endpoint
    ecr            = var.nimbus_endpoint
    ecs            = var.nimbus_endpoint
    kms            = var.nimbus_endpoint
    sns            = var.nimbus_endpoint
    eventbridge    = var.nimbus_endpoint
    iam            = var.nimbus_endpoint
    sts            = var.nimbus_endpoint
    cloudwatch     = var.nimbus_endpoint
    cloudwatchlogs = var.nimbus_endpoint
    scheduler      = var.nimbus_endpoint
    cloudfront     = var.nimbus_endpoint
    elbv2          = var.nimbus_endpoint
    cognitoidp     = var.nimbus_endpoint
    kinesis        = var.nimbus_endpoint
    sfn            = var.nimbus_endpoint
    route53        = var.nimbus_endpoint
    acm            = var.nimbus_endpoint
    elasticache    = var.nimbus_endpoint
    rds            = var.nimbus_endpoint
    efs            = var.nimbus_endpoint
    ec2            = var.nimbus_endpoint
    elb            = var.nimbus_endpoint
    appsync        = var.nimbus_endpoint
  }
}
