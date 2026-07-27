# RDS / Aurora

In-memory Aurora/RDS control plane backed by a real Postgres sidecar. All RDS API calls are handled locally — no traffic is forwarded to AWS. Cluster endpoints resolve to the `postgres` container running alongside Nimbus (default `localhost:5432`), so application code that respects the endpoint returned by `DescribeDBClusters` connects to a real Postgres instance.

Detection: form-encoded body, `Version=2014-10-31`.

## Supported operations

| Operation | Notes |
|-----------|-------|
| `CreateDBSubnetGroup` / `DescribeDBSubnetGroups` / `DeleteDBSubnetGroup` | Records the subnet list; resolves each subnet's VPC and AZ through EC2; delete is rejected while a DB uses the group |
| `ModifyDBSubnetGroup` | Replaces the subnet list when one is supplied, otherwise returns the group unchanged |
| `CreateDBClusterParameterGroup` / `DescribeDBClusterParameterGroups` / `DeleteDBClusterParameterGroup` | Accepted verbatim; `DescribeDBClusterParameters` returns empty list |
| `ModifyDBClusterParameterGroup` | Accepted, no-op |
| `CreateDBParameterGroup` / `DescribeDBParameterGroups` / `DeleteDBParameterGroup` | Accepted verbatim; `DescribeDBParameters` returns empty list |
| `ModifyDBParameterGroup` | Accepted, no-op |
| `CreateDBCluster` / `DescribeDBClusters` / `ModifyDBCluster` / `DeleteDBCluster` | Status always `available`; endpoint resolves to Postgres sidecar; assigns an immutable `DbClusterResourceId`; honors `DBSubnetGroupName` |
| `CreateDBInstance` / `DescribeDBInstances` / `ModifyDBInstance` / `DeleteDBInstance` | Status always `available`; inherits endpoint and subnet group from parent cluster; assigns an immutable `DbiResourceId`; standalone instances echo `EngineVersion`, `MasterUsername`, `DBName`, `AllocatedStorage` |
| `CreateDBProxy` / `DescribeDBProxies` / `ModifyDBProxy` / `DeleteDBProxy` | Status always `available`; endpoint resolves to the Postgres sidecar; creates a `default` target group — see [RDS Proxy](#rds-proxy) |
| `DescribeDBProxyTargetGroups` / `ModifyDBProxyTargetGroup` | Pool settings stored and echoed; only the fields sent are changed |
| `RegisterDBProxyTargets` / `DescribeDBProxyTargets` / `DeregisterDBProxyTargets` | Registers existing clusters and instances; re-registering replaces rather than duplicates |
| `AddTagsToResource` / `ListTagsForResource` / `RemoveTagsFromResource` | Per-ARN tag store |
| `DescribeDBEngineVersions` | Returns a single matching version entry |
| `DescribeOrderableDBInstanceOptions` | Returns a minimal valid response |
| `DescribeDBClusterSnapshots` / `DescribeOptionGroups` | Returns empty lists |

## Subnet groups and subnet dependencies

`CreateDBSubnetGroup` stores the `SubnetIds` it was given. `DescribeDBSubnetGroups` reports them back under `Subnets`, with each subnet's Availability Zone and the group's `VpcId` resolved through the [EC2](ec2.md) service — subnets that were never created via `CreateSubnet` (hardcoded `subnet-000…` style IDs) fall back to `<region>a` and a placeholder VPC.

A cluster or instance created with `DBSubnetGroupName` holds a reference to that group:

- `DescribeDBClusters` reports it as `DBClusters[].DBSubnetGroup` (a bare name, matching real AWS).
- `DescribeDBInstances` reports the full nested structure at `DBInstances[].DBSubnetGroup`.
- Cluster members created without `DBSubnetGroupName` inherit the cluster's group.
- Naming a group that doesn't exist fails with `DBSubnetGroupNotFoundFault`.

That reference is enforced on the delete paths, so Terraform gets the same teardown ordering it gets against real AWS:

| Operation | Rejected while a cluster/instance uses the group | Error |
|-----------|---------------------------------------------------|-------|
| `ec2 delete-subnet` | Subnet belongs to a group a DB sits in | `DependencyViolation` |
| `rds delete-db-subnet-group` | Group is a DB's subnet group | `InvalidDBSubnetGroupStateFault` |

A subnet group with no DB in it pins nothing — its subnets delete normally.

**Known difference**: when `DBSubnetGroupName` is *omitted*, real AWS silently places the DB in the default VPC's default subnet group (or, with no default VPC, any available subnet in the account), creating a dependency invisible in the Terraform graph. Nimbus does not emulate that fallback — an omitted subnet group means no subnet association at all.

## Describe filters

`DescribeDBInstances` and `DescribeDBClusters` honor the `Filters` parameter the Terraform AWS provider uses for reads (instead of the identifier params). Values match by name **or** ARN; multiple values are OR-ed; a filter that matches nothing returns an empty list (not an error). Unknown filter names are ignored.

| Operation | Supported filters |
|-----------|-------------------|
| `DescribeDBInstances` | `db-instance-id`, `db-cluster-id`, `dbi-resource-id` |
| `DescribeDBClusters` | `db-cluster-id`, `db-cluster-resource-id` |

```bash
nimbuslocal rds describe-db-instances \
  --filters "Name=db-instance-id,Values=my-instance-1"
```

## Performance Insights

Clusters and instances accept `EnablePerformanceInsights`, `PerformanceInsightsKMSKeyId`, and `PerformanceInsightsRetentionPeriod` on create and modify, and round-trip them through the Describe responses (`PerformanceInsightsEnabled` etc.). Retention defaults to `7` when PI is enabled without an explicit value. Modify calls that don't include PI fields leave the stored values untouched, so `performance_insights_enabled = true` in Terraform applies and re-applies cleanly.

The `DbiResourceId` returned by `DescribeDBInstances` (and `DbClusterResourceId` on clusters) is the identifier the [Performance Insights API](pi.md) keys off.

## RDS Proxy

A proxy's endpoint resolves to the same Postgres sidecar a cluster endpoint does, so
application code pointed at the proxy reaches a real database. Nimbus does **no
connection pooling of its own** — the pool settings are stored so they read back
without Terraform drift, and nothing multiplexes or pins sessions.

`CreateDBProxy` creates a `default` target group alongside the proxy, matching real
RDS. Terraform's `aws_db_proxy_default_target_group` modifies that group rather than
creating one, so it must exist the moment the proxy does.

| Behaviour | Detail |
|-----------|--------|
| `Status` | Always `available`; `DeleteDBProxy` returns the proxy as `deleting` |
| `DBProxyArn` | Keyed on a generated `prx-` identifier, as in real RDS — not on the proxy name |
| `Endpoint` | The Postgres sidecar host |
| `Auth` / `RoleArn` | Accepted without cross-service validation; the secret is never read and the role never assumed, but both round-trip through `DescribeDBProxies` |
| Default pool config | `MaxConnectionsPercent` 100, `MaxIdleConnectionsPercent` 50, `ConnectionBorrowTimeout` 120 |
| Targets | Registering a cluster yields one `TRACKED_CLUSTER` target, an instance one `RDS_INSTANCE`; registering an unknown identifier is rejected with `DBClusterNotFoundFault` / `DBInstanceNotFoundFault` |
| `TargetHealth` | Always `AVAILABLE` |

Real RDS additionally lists a registered cluster's member instances as extra
`RDS_INSTANCE` targets. Nimbus does not synthesize those — nothing reading a local
proxy needs the expansion, and inventing entries would confuse Terraform's read-back
of `aws_db_proxy_target`.

Proxy metrics (`ClientConnections`, `DatabaseConnections`,
`DatabaseConnectionsBorrowLatency`, pinning counts) are not synthesized either; seed
them through [CloudWatch](cloudwatchmetrics.md) `PutMetricData` with a `ProxyName`
dimension, as with any other metric.

## Example

```bash
# Subnet group
nimbuslocal rds create-db-subnet-group \
  --db-subnet-group-name my-subnets \
  --db-subnet-group-description "dev" \
  --subnet-ids subnet-001 subnet-002

# Aurora Serverless v2 cluster
nimbuslocal rds create-db-cluster \
  --db-cluster-identifier my-cluster \
  --engine aurora-postgresql \
  --engine-version 16.1 \
  --master-username nimbus \
  --master-user-password secret \
  --db-subnet-group-name my-subnets

nimbuslocal rds describe-db-clusters \
  --db-cluster-identifier my-cluster \
  --query "DBClusters[0].Endpoint"

# DB instance with Performance Insights
nimbuslocal rds create-db-instance \
  --db-instance-identifier my-instance-1 \
  --db-cluster-identifier my-cluster \
  --db-instance-class db.serverless \
  --engine aurora-postgresql \
  --enable-performance-insights

nimbuslocal rds describe-db-instances \
  --db-instance-identifier my-instance-1 \
  --query "DBInstances[0].[DbiResourceId,PerformanceInsightsEnabled]"
```

## Inspection

```bash
curl http://localhost:4566/_nimbus/rds/clusters
```

Returns JSON with all clusters and their resolved endpoint/port.
