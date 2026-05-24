resource "aws_cognito_user_pool" "nimbus_test" {
  name = "${var.prefix}-nimbus-pool"

  auto_verified_attributes = ["email"]

  username_attributes = ["email"]

  password_policy {
    minimum_length    = 8
    require_uppercase = true
    require_lowercase = true
    require_numbers   = true
    require_symbols   = false
  }

  tags = {
    Name = var.prefix
  }
}

resource "aws_cognito_user_pool_client" "nimbus_test" {
  name         = "${var.prefix}-nimbus-client"
  user_pool_id = aws_cognito_user_pool.nimbus_test.id

  explicit_auth_flows = [
    "ALLOW_USER_PASSWORD_AUTH",
    "ALLOW_REFRESH_TOKEN_AUTH",
  ]

  callback_urls = ["https://${var.prefix}.nimbus.local/callback"]
  logout_urls   = ["https://${var.prefix}.nimbus.local/logout"]

  allowed_oauth_flows                  = ["code"]
  allowed_oauth_scopes                 = ["openid", "email", "profile"]
  allowed_oauth_flows_user_pool_client = true

  supported_identity_providers = ["COGNITO"]
}
