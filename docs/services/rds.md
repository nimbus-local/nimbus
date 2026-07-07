# RDS / Aurora

In-memory Aurora/RDS control plane backed by a real Postgres sidecar. All RDS API calls are handled locally — no traffic is forwarded to AWS. Cluster endpoints resolve to the `postgres` container running alongside Nimbus (default `localhost:5432`), so application code that respects the endpoint returned by `DescribeDBClusters` connects to a real Postgres instance.

Detection: form-encoded body, `Version=2014-10-31`.

## Supported operations

| Operation | Notes |
|-----------|-------|
| `CreateDBSubnetGroup` / `DescribeDBSubnetGroups` / `DeleteDBSubnetGroup` | Stored in-memory; no VPC or subnet validation |
| `CreateDBClusterParameterGroup` / `DescribeDBClusterParameterGroups` / `DeleteDBClusterParameterGroup` | Accepted verbatim; `DescribeDBClusterParameters` returns empty list |
| `ModifyDBClusterParameterGroup` | Accepted, no-op |
| `CreateDBParameterGroup` / `DescribeDBParameterGroups` / `DeleteDBParameterGroup` | Accepted verbatim; `DescribeDBParameters` returns empty list |
| `ModifyDBParameterGroup` | Accepted, no-op |
| `CreateDBCluster` / `DescribeDBClusters` / `ModifyDBCluster` / `DeleteDBCluster` | Status always `available`; endpoint resolves to Postgres sidecar; assigns an immutable `DbClusterResourceId` |
| `CreateDBInstance` / `DescribeDBInstances` / `ModifyDBInstance` / `DeleteDBInstance` | Status always `available`; inherits endpoint from parent cluster; assigns an immutable `DbiResourceId`; standalone instances echo `EngineVersion`, `MasterUsername`, `DBName`, `AllocatedStorage` |
| `AddTagsToResource` / `ListTagsForResource` / `RemoveTagsFromResource` | Per-ARN tag store |
| `DescribeDBEngineVersions` | Returns a single matching version entry |
| `DescribeOrderableDBInstanceOptions` | Returns a minimal valid response |
| `DescribeDBClusterSnapshots` / `DescribeOptionGroups` | Returns empty lists |

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
