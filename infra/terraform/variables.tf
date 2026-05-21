variable "nimbus_endpoint" {
  description = "Nimbus endpoint URL"
  type        = string
  default     = "http://localhost:4566"
}

variable "region" {
  description = "AWS region"
  type        = string
  default     = "us-east-1"
}

variable "prefix" {
  description = "Name prefix applied to every resource"
  type        = string
  default     = "nimbus-test"
}

variable "enable_dynamodb" {
  description = "Create DynamoDB resources (requires the DynamoDB Local sidecar)"
  type        = bool
  default     = true
}
