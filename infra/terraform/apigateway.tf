resource "aws_api_gateway_rest_api" "nimbus_test" {
  name = var.prefix
}

resource "aws_api_gateway_resource" "hello" {
  rest_api_id = aws_api_gateway_rest_api.nimbus_test.id
  parent_id   = aws_api_gateway_rest_api.nimbus_test.root_resource_id
  path_part   = "hello"
}

resource "aws_api_gateway_method" "hello_get" {
  rest_api_id   = aws_api_gateway_rest_api.nimbus_test.id
  resource_id   = aws_api_gateway_resource.hello.id
  http_method   = "GET"
  authorization = "NONE"
}

resource "aws_api_gateway_integration" "hello_mock" {
  rest_api_id = aws_api_gateway_rest_api.nimbus_test.id
  resource_id = aws_api_gateway_resource.hello.id
  http_method = aws_api_gateway_method.hello_get.http_method
  type        = "MOCK"
  request_templates = {
    "application/json" = jsonencode({ statusCode = 200 })
  }
}

resource "aws_api_gateway_method_response" "hello_200" {
  rest_api_id = aws_api_gateway_rest_api.nimbus_test.id
  resource_id = aws_api_gateway_resource.hello.id
  http_method = aws_api_gateway_method.hello_get.http_method
  status_code = "200"
}

resource "aws_api_gateway_integration_response" "hello_200" {
  rest_api_id = aws_api_gateway_rest_api.nimbus_test.id
  resource_id = aws_api_gateway_resource.hello.id
  http_method = aws_api_gateway_method.hello_get.http_method
  status_code = aws_api_gateway_method_response.hello_200.status_code
  response_templates = {
    "application/json" = jsonencode({ message = "hello from nimbus" })
  }
  depends_on = [aws_api_gateway_integration.hello_mock]
}

resource "aws_api_gateway_deployment" "nimbus_test" {
  rest_api_id = aws_api_gateway_rest_api.nimbus_test.id
  depends_on = [
    aws_api_gateway_integration.hello_mock,
    aws_api_gateway_integration_response.hello_200,
  ]
}

resource "aws_api_gateway_stage" "v1" {
  rest_api_id   = aws_api_gateway_rest_api.nimbus_test.id
  deployment_id = aws_api_gateway_deployment.nimbus_test.id
  stage_name    = "v1"
}

# ── WebSocket API (v2, protocolType=WEBSOCKET) ────────────────────────────────

resource "aws_apigatewayv2_api" "nimbus_ws" {
  name                       = "${var.prefix}-ws"
  protocol_type              = "WEBSOCKET"
  route_selection_expression = "$request.body.action"
}

resource "aws_apigatewayv2_integration" "ws_lambda" {
  api_id           = aws_apigatewayv2_api.nimbus_ws.id
  integration_type = "AWS_PROXY"
  integration_uri  = aws_lambda_function.nimbus_test.invoke_arn
}

resource "aws_apigatewayv2_route" "ws_connect" {
  api_id    = aws_apigatewayv2_api.nimbus_ws.id
  route_key = "$connect"
  target    = "integrations/${aws_apigatewayv2_integration.ws_lambda.id}"
}

resource "aws_apigatewayv2_route" "ws_disconnect" {
  api_id    = aws_apigatewayv2_api.nimbus_ws.id
  route_key = "$disconnect"
  target    = "integrations/${aws_apigatewayv2_integration.ws_lambda.id}"
}

resource "aws_apigatewayv2_route" "ws_default" {
  api_id    = aws_apigatewayv2_api.nimbus_ws.id
  route_key = "$default"
  target    = "integrations/${aws_apigatewayv2_integration.ws_lambda.id}"
}

resource "aws_apigatewayv2_stage" "ws_prod" {
  api_id      = aws_apigatewayv2_api.nimbus_ws.id
  name        = "prod"
  auto_deploy = true
}
