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
| `CreateDBCluster` / `DescribeDBClusters` / `ModifyDBCluster` / `DeleteDBCluster` | Status always `available`; endpoint resolves to Postgres sidecar |
| `CreateDBInstance` / `DescribeDBInstances` / `ModifyDBInstance` / `DeleteDBInstance` | Status always `available`; inherits endpoint from parent cluster |
| `AddTagsToResource` / `ListTagsForResource` / `RemoveTagsFromResource` | Per-ARN tag store |
| `DescribeDBEngineVersions` | Returns a single matching version entry |
| `DescribeOrderableDBInstanceOptions` | Returns a minimal valid response |
| `DescribeDBClusterSnapshots` / `DescribeOptionGroups` | Returns empty lists |

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

# DB instance
nimbuslocal rds create-db-instance \
  --db-instance-identifier my-instance-1 \
  --db-cluster-identifier my-cluster \
  --db-instance-class db.serverless \
  --engine aurora-postgresql
```

## Inspection

```bash
curl http://localhost:4566/_nimbus/rds/clusters
```

Returns JSON with all clusters and their resolved endpoint/port.
