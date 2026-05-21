resource "aws_cloudwatch_event_bus" "nimbus_test" {
  name = var.prefix
}

resource "aws_cloudwatch_event_rule" "nimbus_test" {
  name           = var.prefix
  event_bus_name = aws_cloudwatch_event_bus.nimbus_test.name
  event_pattern = jsonencode({
    source      = ["nimbus.test"]
    detail-type = ["NimbusEvent"]
  })
}
