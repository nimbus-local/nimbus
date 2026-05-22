provider "aws" {
  region                      = var.region
  access_key                  = "test"
  secret_key                  = "test"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true

  # Path-style required for Nimbus S3
  s3_use_path_style = true

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
    ecr            = var.nimbus_endpoint
    ecs            = var.nimbus_endpoint
    kms            = var.nimbus_endpoint
    sns            = var.nimbus_endpoint
    eventbridge    = var.nimbus_endpoint
    iam            = var.nimbus_endpoint
    sts            = var.nimbus_endpoint
    cloudwatchlogs = var.nimbus_endpoint
    scheduler      = var.nimbus_endpoint
    cloudfront     = var.nimbus_endpoint
    elbv2          = var.nimbus_endpoint
  }
}
