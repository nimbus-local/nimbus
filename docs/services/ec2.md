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
| `CreateSubnet` / `DescribeSubnets` / `ModifySubnetAttribute` / `DeleteSubnet` | `DescribeSubnets` returns synthetic entries for unknown IDs (backwards compat) |
| `CreateInternetGateway` / `DescribeInternetGateways` / `AttachInternetGateway` / `DetachInternetGateway` / `DeleteInternetGateway` | In-memory attachment tracking |
| `DescribeSecurityGroups` / `DescribeSecurityGroupRules` / `AuthorizeSecurityGroupIngress` / `AuthorizeSecurityGroupEgress` / `RevokeSecurityGroupIngress` / `RevokeSecurityGroupEgress` / `ModifySecurityGroupRules` | Rules stored with generated `sgr-` IDs; default SG auto-created with each VPC |
| `CreateRouteTable` / `DescribeRouteTables` / `DeleteRouteTable` | Includes local route; supports filter by `vpc-id`, `route-table-id`, and `association.*` |
| `CreateRoute` / `DeleteRoute` | Stored per route table; local route is never deleted |
| `AssociateRouteTable` / `DisassociateRouteTable` | Associations tracked by `rtbassoc-` ID |
| `CreateTags` / `DeleteTags` | Applied to VPCs, subnets, IGWs, security groups, and route tables |

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

# Security group rules
SG_ID=$(nimbuslocal ec2 describe-security-groups \
  --filters Name=vpc-id,Values="$VPC_ID" Name=group-name,Values=default \
  --query "SecurityGroups[0].GroupId" --output text)

nimbuslocal ec2 authorize-security-group-egress --group-id "$SG_ID" \
  --ip-permissions IpProtocol=-1,FromPort=0,ToPort=0,IpRanges=[{CidrIp=0.0.0.0/0}]

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
