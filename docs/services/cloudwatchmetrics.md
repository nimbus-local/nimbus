# CloudWatch Metrics

In-memory CloudWatch Metrics emulator. `PutMetricData` stores time-series data per namespace/metric/dimension combination (capped at 10,000 points per series). `GetMetricStatistics` and `GetMetricData` aggregate stored points into period buckets. Alarms are structural only — state is always `OK` and no evaluation logic runs. Nothing is ever sent to AWS.

Detection: `X-Amz-Target: GraniteServiceVersion20100801.*` (awsJson1.0, used by the AWS CLI) or `/service/GraniteServiceVersion20100801/operation/*` path (smithy-rpc-v2-cbor, used by AWS SDK Go v2 / Terraform provider v6+).

Timestamp shapes on the CBOR path are CBOR **tag 1** epoch seconds in both directions: the decoder accepts tag-1 uint/float epochs for request fields (`Timestamp`, `StartTime`, `EndTime`), and `GetMetricData`/`GetMetricStatistics` responses emit tag-1 timestamps — the SDK's deserializer rejects RFC3339 strings there.

## Supported operations

| Operation | Notes |
|-----------|-------|
| `PutMetricData` | Accepts any namespace, metric name, dimensions, and value; stores in-memory |
| `ListMetrics` | Filters by namespace, metric name, and/or dimensions — see [Dimension filters](#dimension-filters) |
| `GetMetricStatistics` | Sum, Average, Minimum, Maximum, SampleCount per period bucket |
| `GetMetricData` | Multi-metric query with `MetricStat` queries |
| `PutMetricAlarm` | Stores alarm definition; state always `OK` |
| `DescribeAlarms` | Returns stored alarms filtered by name or state |
| `DescribeAlarmsForMetric` | Returns alarms matching namespace + metric + dimensions |
| `DeleteAlarms` | Removes stored alarms |
| `SetAlarmState` | Accepted and ignored (state stays `OK`) |
| `EnableAlarmActions` / `DisableAlarmActions` | Accepted and ignored |
| `ListTagsForResource` / `TagResource` / `UntagResource` | Tag support for alarms |

## Dimension filters

`ListMetrics` takes `DimensionFilter` entries, whose matching rules differ from the
`Dimension` lists the other operations take:

| Filter | Matches |
|--------|---------|
| `Name` only | Every metric that **carries** a dimension of that name, whatever its value |
| `Name` + `Value` | Only metrics carrying that exact pair |
| Several filters | ANDed — a metric must satisfy all of them |

A name-only filter is how you discover which series carry a dimension at all:

```bash
# Every RequestCount series that has a TargetDiscoveryName dimension
nimbuslocal cloudwatch list-metrics \
  --namespace AWS/ECS --metric-name RequestCount \
  --dimensions Name=TargetDiscoveryName

# Narrow to one service's edges — filters are ANDed
nimbuslocal cloudwatch list-metrics \
  --namespace AWS/ECS \
  --dimensions Name=ServiceName,Value=web-svc Name=TargetDiscoveryName
```

A filter naming a dimension no metric carries matches nothing.

`GetMetricStatistics`, `GetMetricData`, and `DescribeAlarmsForMetric` do **not** use
these rules: there a dimension list identifies a series, so every entry must match by
value.

## Example

```bash
# Publish a metric
nimbuslocal cloudwatch put-metric-data \
  --namespace MyApp \
  --metric-name RequestCount \
  --value 42 \
  --unit Count

# List metrics
nimbuslocal cloudwatch list-metrics --namespace MyApp

# Query statistics
nimbuslocal cloudwatch get-metric-statistics \
  --namespace MyApp \
  --metric-name RequestCount \
  --start-time 2026-01-01T00:00:00Z \
  --end-time 2026-01-02T00:00:00Z \
  --period 3600 \
  --statistics Sum Average

# Create an alarm
nimbuslocal cloudwatch put-metric-alarm \
  --alarm-name high-cpu \
  --metric-name CPUUtilization \
  --namespace AWS/EC2 \
  --comparison-operator GreaterThanThreshold \
  --threshold 80 \
  --evaluation-periods 1 \
  --period 60 \
  --statistic Average

# Describe alarms
nimbuslocal cloudwatch describe-alarms
```

## Inspection endpoint

```bash
# List all stored metrics and alarms
curl http://localhost:4566/_nimbus/metrics
```

Example response:
```json
{
  "metrics": [
    {"namespace": "MyApp", "metricName": "RequestCount", "points": 3}
  ],
  "alarms": [
    {"name": "high-cpu", "arn": "arn:aws:cloudwatch:us-east-1:000000000000:alarm:high-cpu", "state": "OK"}
  ]
}
```
