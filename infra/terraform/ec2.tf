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
