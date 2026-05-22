resource "aws_cloudwatch_metric_alarm" "cpu" {
  alarm_name          = "${var.prefix}-cpu-alarm"
  alarm_description   = "CPU utilization alarm"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 1
  metric_name         = "CPUUtilization"
  namespace           = "AWS/EC2"
  period              = 60
  statistic           = "Average"
  threshold           = 80

  dimensions = {
    InstanceId = "i-00000000000000001"
  }

  tags = {
    Prefix = var.prefix
  }
}
