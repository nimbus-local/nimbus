resource "aws_ecs_cluster" "nimbus_test" {
  name = var.prefix
}

resource "aws_ecs_task_definition" "nimbus_test" {
  family                   = var.prefix
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = "256"
  memory                   = "512"

  container_definitions = jsonencode([
    {
      name      = "app"
      image     = "nginx:latest"
      essential = true
      portMappings = [
        { containerPort = 80, protocol = "tcp" }
      ]
    }
  ])
}

resource "aws_ecs_service" "nimbus_test" {
  name            = var.prefix
  cluster         = aws_ecs_cluster.nimbus_test.id
  task_definition = aws_ecs_task_definition.nimbus_test.arn
  desired_count   = 2
  launch_type     = "FARGATE"

  # Nimbus does not validate subnet/security-group IDs
  network_configuration {
    subnets = ["subnet-00000000000000001"]
  }

  # container_name/container_port must match a portMappings entry on the "app"
  # container above — Nimbus rejects CreateService otherwise, as real ECS does.
  load_balancer {
    target_group_arn = aws_lb_target_group.nimbus_test.arn
    container_name   = "app"
    container_port   = 80
  }

  depends_on = [aws_lb_listener.nimbus_test]
}
