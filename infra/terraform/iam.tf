resource "aws_iam_role" "task_execution" {
  name = "${var.prefix}-task-execution"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Action    = "sts:AssumeRole"
      Effect    = "Allow"
      Principal = { Service = "ecs-tasks.amazonaws.com" }
    }]
  })
}

resource "aws_iam_role_policy_attachment" "task_execution" {
  role       = aws_iam_role.task_execution.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy"
}

resource "aws_iam_policy" "custom" {
  name = "${var.prefix}-custom"

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["ssm:GetParameter"]
      Resource = "*"
    }]
  })
}

resource "aws_iam_role_policy_attachment" "custom" {
  role       = aws_iam_role.task_execution.name
  policy_arn = aws_iam_policy.custom.arn
}

resource "aws_iam_role_policy" "inline" {
  name = "${var.prefix}-inline"
  role = aws_iam_role.task_execution.name

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["logs:CreateLogStream"]
      Resource = "*"
    }]
  })
}

resource "aws_iam_instance_profile" "task_execution" {
  name = "${var.prefix}-task-execution"
  role = aws_iam_role.task_execution.name
}
