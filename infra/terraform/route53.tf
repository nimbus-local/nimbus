resource "aws_route53_zone" "nimbus_test" {
  name    = "${var.prefix}.nimbus.local"
  comment = "Nimbus test zone"

  tags = {
    Name = var.prefix
  }
}

resource "aws_route53_record" "nimbus_test_a" {
  zone_id = aws_route53_zone.nimbus_test.zone_id
  name    = "${var.prefix}.nimbus.local"
  type    = "A"
  ttl     = 300
  records = ["127.0.0.1"]
}

resource "aws_route53_record" "nimbus_test_cname" {
  zone_id = aws_route53_zone.nimbus_test.zone_id
  name    = "www.${var.prefix}.nimbus.local"
  type    = "CNAME"
  ttl     = 300
  records = ["${var.prefix}.nimbus.local"]
}
