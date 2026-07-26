resource "aws_vpc" "nimbus_test" {
  cidr_block           = "10.0.0.0/16"
  enable_dns_support   = true
  enable_dns_hostnames = true

  tags = {
    Name = var.prefix
  }
}

resource "aws_subnet" "nimbus_test_public" {
  vpc_id                  = aws_vpc.nimbus_test.id
  cidr_block              = "10.0.1.0/24"
  availability_zone       = "${var.region}a"
  map_public_ip_on_launch = true

  tags = {
    Name = "${var.prefix}-public"
  }
}

resource "aws_subnet" "nimbus_test_private" {
  vpc_id            = aws_vpc.nimbus_test.id
  cidr_block        = "10.0.4.0/24"
  availability_zone = "${var.region}a"

  tags = {
    Name = "${var.prefix}-private"
  }
}

resource "aws_internet_gateway" "nimbus_test" {
  vpc_id = aws_vpc.nimbus_test.id

  tags = {
    Name = var.prefix
  }
}

resource "aws_route_table" "nimbus_test_public" {
  vpc_id = aws_vpc.nimbus_test.id

  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.nimbus_test.id
  }

  tags = {
    Name = "${var.prefix}-public"
  }
}

resource "aws_route_table_association" "nimbus_test_public" {
  subnet_id      = aws_subnet.nimbus_test_public.id
  route_table_id = aws_route_table.nimbus_test_public.id
}

# Gateway VPC endpoint. Modules that reach S3 from a private subnet look the
# endpoint up rather than manage it, so the read path matters as much as the
# create path.
resource "aws_vpc_endpoint" "s3_gateway" {
  vpc_id          = aws_vpc.nimbus_test.id
  service_name    = "com.amazonaws.${var.region}.s3"
  route_table_ids = [aws_route_table.nimbus_test_public.id]

  tags = {
    Name = "${var.prefix}-s3-gateway"
  }
}

# Resolving the endpoint by service name exercises the DescribeVpcEndpoints
# filters, and reading prefix_list_id off it exercises DescribePrefixLists.
data "aws_vpc_endpoint" "s3_gateway" {
  vpc_id       = aws_vpc.nimbus_test.id
  service_name = "com.amazonaws.${var.region}.s3"

  depends_on = [aws_vpc_endpoint.s3_gateway]
}

# Egress scoped to the gateway endpoint's prefix list rather than a CIDR. The
# rule has to survive the round-trip through DescribeSecurityGroups or the
# provider plans a change on every run.
resource "aws_security_group" "prefix_list_egress" {
  name        = "${var.prefix}-pl-egress"
  description = "Egress restricted to the S3 gateway endpoint prefix list"
  vpc_id      = aws_vpc.nimbus_test.id

  egress {
    description     = "S3 via the gateway endpoint"
    from_port       = 443
    to_port         = 443
    protocol        = "tcp"
    prefix_list_ids = [data.aws_vpc_endpoint.s3_gateway.prefix_list_id]
  }

  tags = {
    Name = "${var.prefix}-pl-egress"
  }
}
