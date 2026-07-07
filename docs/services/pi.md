# Performance Insights (PI)

In-memory Performance Insights emulator with synthetic metric data — no traffic is forwarded to AWS and no real metrics are collected. Metric values follow a deterministic sinusoid (a 1-hour cycle between 0.1 and 0.9 average active sessions), so repeated queries over the same window return identical datapoints. Identifiers are validated against the [RDS emulator](rds.md)'s `DbiResourceId` / `DbClusterResourceId` values — query `rds describe-db-instances` first to get one.

Detection: `X-Amz-Target: PerformanceInsightsv20180227.*` (awsJson1.1).

## Supported operations

| Operation | Notes |
|-----------|-------|
| `GetResourceMetrics` | Synthetic datapoints aligned to `PeriodInSeconds` (default 60, max 1000 points per series); `GroupBy` splits the load across canned dimension groups; `GroupBy.Limit` and `GroupBy.Dimensions` respected |
| `DescribeDimensionKeys` | Returns canned keys per group with `Total` proportional to the load over the window — `db.wait_event` yields `CPU` (0.6), `IO:DataFileRead` (0.3), `Lock:transactionid` (0.1) |
| `ListAvailableResourceMetrics` | Small catalog (`db.load.avg`, `db.sampledload.avg`, `os.cpuUtilization.total.avg`, `os.memory.free.avg`), filtered by `MetricTypes` prefix |
| `ListAvailableResourceDimensions` | Returns groups `db.wait_event`, `db.sql`, `db.user`, `db.host` for each requested metric |
| `GetResourceMetadata` | Returns `SQL_DIGEST: ENABLED` |

Unknown identifiers return `InvalidArgumentException`.

## Example

```bash
# Get the PI resource identifier from RDS
DBI=$(nimbuslocal rds describe-db-instances \
  --db-instance-identifier my-instance-1 \
  --query "DBInstances[0].DbiResourceId" --output text)

# Database load over the last hour, 5-minute resolution
nimbuslocal pi get-resource-metrics \
  --service-type RDS --identifier "$DBI" \
  --metric-queries '[{"Metric":"db.load.avg"}]' \
  --start-time $(($(date +%s) - 3600)) --end-time $(date +%s) \
  --period-in-seconds 300

# Top wait events
nimbuslocal pi describe-dimension-keys \
  --service-type RDS --identifier "$DBI" \
  --metric db.load.avg \
  --group-by '{"Group":"db.wait_event"}' \
  --start-time $(($(date +%s) - 3600)) --end-time $(date +%s)

nimbuslocal pi list-available-resource-metrics \
  --service-type RDS --identifier "$DBI" --metric-types db os
```
