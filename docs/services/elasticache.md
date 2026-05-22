# ElastiCache

In-memory control plane backed by a real **Valkey 7.2** sidecar container. All
ElastiCache API calls are handled by Nimbus; cluster and replication-group
endpoints resolve to `localhost:6379` (or the configured `NIMBUS_VALKEY_HOST` /
`NIMBUS_VALKEY_PORT`). Subnet groups and parameter groups are accepted and stored
verbatim — no VPC or subnet validation is performed.

**Detection:** `Content-Type: application/x-www-form-urlencoded` + `Version=2015-02-02`

## Supported operations

| Operation | Notes |
|-----------|-------|
| `CreateCacheSubnetGroup` / `DescribeCacheSubnetGroups` / `DeleteCacheSubnetGroup` | In-memory; always returns `SubnetGroupStatus: Complete` |
| `CreateCacheParameterGroup` / `DescribeCacheParameterGroups` / `DescribeCacheParameters` / `ModifyCacheParameterGroup` / `DeleteCacheParameterGroup` | Accepted and stored; parameters are always empty |
| `CreateCacheCluster` / `DescribeCacheClusters` / `ModifyCacheCluster` / `DeleteCacheCluster` | Endpoint resolves to Valkey sidecar |
| `CreateReplicationGroup` / `DescribeReplicationGroups` / `ModifyReplicationGroup` / `DeleteReplicationGroup` | Endpoint resolves to Valkey sidecar; `ClusterEnabled: false` |
| `DescribeCacheEngineVersions` | Returns a single version entry for the requested engine |
| `AddTagsToResource` / `ListTagsForResource` / `RemoveTagsFromResource` | In-memory tag store keyed by ARN |

## Example

```bash
# Create subnet group and parameter group
nimbuslocal elasticache create-cache-subnet-group \
  --cache-subnet-group-name my-group \
  --cache-subnet-group-description "test" \
  --subnet-ids subnet-00000000000000001

nimbuslocal elasticache create-cache-parameter-group \
  --cache-parameter-group-name my-params \
  --cache-parameter-group-family valkey7 \
  --description "test params"

# Create a replication group (endpoint → Valkey sidecar)
nimbuslocal elasticache create-replication-group \
  --replication-group-id my-cache \
  --replication-group-description "test cache" \
  --engine valkey \
  --engine-version 7.2 \
  --cache-node-type cache.t3.micro \
  --num-cache-clusters 1

# Inspect
nimbuslocal elasticache describe-replication-groups \
  --replication-group-id my-cache
```

## Inspection endpoint

```
GET /_nimbus/elasticache/clusters
```

Returns a JSON array of all cache clusters and replication groups with their
resolved endpoints, ports, and status.
