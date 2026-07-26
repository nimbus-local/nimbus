# EC2 (VPC)

In-memory VPC control plane. All state is in-memory; no real networking is created.
Supports the full set of EC2 query-protocol actions required by the Pulumi AWS provider
v7 when deploying a VPC with public/private subnets, an internet gateway, a default
security group, and per-AZ route tables. The `DescribeSubnets` response includes a
synthetic fallback for subnet IDs that were never created via `CreateSubnet`, so
existing smoke stacks that pass hardcoded subnet IDs continue to work.

**Detection:** `POST /` with `Content-Type: application/x-www-form-urlencoded`

## Supported operations

| Operation | Notes |
|-----------|-------|
| `DescribeAvailabilityZones` | Returns 3 AZs for the configured region (a, b, c) |
| `CreateVpc` / `DescribeVpcs` / `DescribeVpcAttribute` / `ModifyVpcAttribute` / `DeleteVpc` | In-memory; auto-creates a default security group and main route table per VPC |
| `CreateSubnet` / `DescribeSubnets` / `ModifySubnetAttribute` / `DeleteSubnet` | `DescribeSubnets` returns synthetic entries for unknown IDs (backwards compat); `DeleteSubnet` returns `DependencyViolation` when another service still uses the subnet — see [subnet dependencies](rds.md#subnet-groups-and-subnet-dependencies) |
| `CreateInternetGateway` / `DescribeInternetGateways` / `AttachInternetGateway` / `DetachInternetGateway` / `DeleteInternetGateway` | In-memory attachment tracking |
| `CreateSecurityGroup` / `DeleteSecurityGroup` | Custom (non-default) security groups, alongside the default SG auto-created with each VPC |
| `DescribeSecurityGroups` / `DescribeSecurityGroupRules` / `AuthorizeSecurityGroupIngress` / `AuthorizeSecurityGroupEgress` / `RevokeSecurityGroupIngress` / `RevokeSecurityGroupEgress` / `ModifySecurityGroupRules` | Rules stored with generated `sgr-` IDs; targets may be a CIDR, a prefix list, or a referenced security group; default SG auto-created with each VPC |
| `CreateVpcEndpoint` / `DescribeVpcEndpoints` / `ModifyVpcEndpoint` / `DeleteVpcEndpoints` | Gateway and Interface endpoints; `DescribeVpcEndpoints` filters on `vpc-id`, `service-name`, `vpc-endpoint-id`, `vpc-endpoint-type`, `vpc-endpoint-state`, and `tag:<key>` |
| `DescribePrefixLists` / `DescribeManagedPrefixLists` / `GetManagedPrefixListEntries` | AWS-managed service prefix lists only; see below |
| `CreateRouteTable` / `DescribeRouteTables` / `DeleteRouteTable` | Includes local route; supports filter by `vpc-id`, `route-table-id`, and `association.*` |
| `CreateRoute` / `DeleteRoute` | Stored per route table; local route is never deleted |
| `AssociateRouteTable` / `DisassociateRouteTable` | Associations tracked by `rtbassoc-` ID |
| `DescribeNetworkInterfaces` | Always returns an empty set — no real ENIs exist; unblocks subnet/VPC teardown |
| `CreateTags` / `DeleteTags` | Applied to VPCs, subnets, IGWs, security groups, and route tables |

## VPC endpoints and prefix lists

The AWS-managed service prefix lists that back gateway endpoints —
`com.amazonaws.<region>.s3` and `com.amazonaws.<region>.dynamodb` — exist from
startup and survive `/_nimbus/reset`, matching AWS, where they are not account
resources. Their IDs are derived from the list name, so they stay the same
across restarts and a recorded ID in a state file keeps resolving. The CIDRs
behind them are reserved documentation ranges: nothing routes through Nimbus,
and a synthetic range makes that obvious.

Customer-managed prefix lists (`CreateManagedPrefixList`) are not implemented.

Endpoints are **not** created automatically with a VPC. A module that looks one
up rather than managing it — the common pattern when the VPC is owned
elsewhere — needs an `aws_vpc_endpoint` resource in the local stack, or
`data.aws_vpc_endpoint` finds nothing:

```hcl
resource "aws_vpc_endpoint" "s3" {
  vpc_id       = data.aws_vpc.selected.id
  service_name = "com.amazonaws.us-east-1.s3"
}
```

Security group rules may target a prefix list instead of a CIDR, which is how
egress is scoped to a gateway endpoint:

```hcl
egress {
  from_port       = 443
  to_port         = 443
  protocol        = "tcp"
  prefix_list_ids = [data.aws_vpc_endpoint.s3.prefix_list_id]
}
```

Nimbus enforces no network policy — rules are stored and reported back so
configuration round-trips, but traffic is never filtered.

## Example

```bash
# Availability zones
nimbuslocal ec2 describe-availability-zones --filters Name=state,Values=available

# VPC lifecycle
VPC_ID=$(nimbuslocal ec2 create-vpc --cidr-block 10.0.0.0/16 \
  --query Vpc.VpcId --output text)

nimbuslocal ec2 modify-vpc-attribute --vpc-id "$VPC_ID" \
  --enable-dns-hostnames

nimbuslocal ec2 describe-vpcs --vpc-ids "$VPC_ID"

# Subnets
SUBNET_ID=$(nimbuslocal ec2 create-subnet \
  --vpc-id "$VPC_ID" \
  --cidr-block 10.0.1.0/24 \
  --availability-zone us-east-1a \
  --query Subnet.SubnetId --output text)

nimbuslocal ec2 modify-subnet-attribute --subnet-id "$SUBNET_ID" \
  --map-public-ip-on-launch

# Internet gateway
IGW_ID=$(nimbuslocal ec2 create-internet-gateway \
  --query InternetGateway.InternetGatewayId --output text)

nimbuslocal ec2 attach-internet-gateway \
  --internet-gateway-id "$IGW_ID" --vpc-id "$VPC_ID"

# Route table
RT_ID=$(nimbuslocal ec2 create-route-table --vpc-id "$VPC_ID" \
  --query RouteTable.RouteTableId --output text)

nimbuslocal ec2 create-route --route-table-id "$RT_ID" \
  --destination-cidr-block 0.0.0.0/0 --gateway-id "$IGW_ID"

ASSOC_ID=$(nimbuslocal ec2 associate-route-table \
  --route-table-id "$RT_ID" --subnet-id "$SUBNET_ID" \
  --query AssociationId --output text)

# Security group rules (default SG)
SG_ID=$(nimbuslocal ec2 describe-security-groups \
  --filters Name=vpc-id,Values="$VPC_ID" Name=group-name,Values=default \
  --query "SecurityGroups[0].GroupId" --output text)

nimbuslocal ec2 authorize-security-group-egress --group-id "$SG_ID" \
  --ip-permissions IpProtocol=-1,FromPort=0,ToPort=0,IpRanges=[{CidrIp=0.0.0.0/0}]

# Custom security group
CUSTOM_SG_ID=$(nimbuslocal ec2 create-security-group \
  --group-name my-app --description "my app" --vpc-id "$VPC_ID" \
  --query GroupId --output text)

nimbuslocal ec2 authorize-security-group-ingress --group-id "$CUSTOM_SG_ID" \
  --ip-permissions IpProtocol=tcp,FromPort=80,ToPort=80,IpRanges=[{CidrIp=10.0.0.0/16}]

nimbuslocal ec2 delete-security-group --group-id "$CUSTOM_SG_ID"

# Tags
nimbuslocal ec2 create-tags \
  --resources "$VPC_ID" "$SUBNET_ID" "$IGW_ID" "$RT_ID" \
  --tags Key=env,Value=smoke

# Cleanup
nimbuslocal ec2 disassociate-route-table --association-id "$ASSOC_ID"
nimbuslocal ec2 delete-route --route-table-id "$RT_ID" \
  --destination-cidr-block 0.0.0.0/0
nimbuslocal ec2 delete-route-table --route-table-id "$RT_ID"
nimbuslocal ec2 detach-internet-gateway \
  --internet-gateway-id "$IGW_ID" --vpc-id "$VPC_ID"
nimbuslocal ec2 delete-internet-gateway --internet-gateway-id "$IGW_ID"
nimbuslocal ec2 delete-subnet --subnet-id "$SUBNET_ID"
nimbuslocal ec2 delete-vpc --vpc-id "$VPC_ID"
```
