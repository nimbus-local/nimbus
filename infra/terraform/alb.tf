resource "aws_lb" "nimbus_test" {
  name               = var.prefix
  internal           = false
  load_balancer_type = "application"

  # Nimbus does not validate subnet or security-group IDs
  subnets = [
    "subnet-00000000000000001",
    "subnet-00000000000000002",
  ]
}

resource "aws_lb_target_group" "nimbus_test" {
  name        = var.prefix
  port        = 80
  protocol    = "HTTP"
  target_type = "ip"

  # Nimbus does not validate VPC IDs
  vpc_id = "vpc-00000000000000001"

  health_check {
    path                = "/health"
    interval            = 30
    timeout             = 5
    healthy_threshold   = 2
    unhealthy_threshold = 2
  }
}

resource "aws_lb_listener" "nimbus_test" {
  load_balancer_arn = aws_lb.nimbus_test.arn
  port              = 80
  protocol          = "HTTP"

  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.nimbus_test.arn
  }
}

resource "aws_lb_listener_rule" "nimbus_api" {
  listener_arn = aws_lb_listener.nimbus_test.arn
  priority     = 100

  condition {
    path_pattern {
      values = ["/api/*"]
    }
  }

  action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.nimbus_test.arn
  }
}
