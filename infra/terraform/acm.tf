resource "aws_acm_certificate" "nimbus_test" {
  domain_name               = "${var.prefix}.nimbus.local"
  subject_alternative_names = ["*.${var.prefix}.nimbus.local"]
  validation_method         = "DNS"

  tags = {
    Name = var.prefix
  }

  lifecycle {
    create_before_destroy = true
  }
}
