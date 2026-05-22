# CloudWatch Metrics

In-memory CloudWatch Metrics emulator. `PutMetricData` stores time-series data per namespace/metric/dimension combination (capped at 10,000 points per series). `GetMetricStatistics` and `GetMetricData` aggregate stored points into period buckets. Alarms are structural only — state is always `OK` and no evaluation logic runs. Nothing is ever sent to AWS.

Detection: form-encoded body (`Content-Type: application/x-www-form-urlencoded`) with `Version=2010-08-01`.

## Supported operations

| Operation | Notes |
|-----------|-------|
| `PutMetricData` | Accepts any namespace, metric name, dimensions, and value; stores in-memory |
| `ListMetrics` | Filters by namespace, metric name, and/or dimensions |
| `GetMetricStatistics` | Sum, Average, Minimum, Maximum, SampleCount per period bucket |
| `GetMetricData` | Multi-metric query with `MetricStat` queries |
| `PutMetricAlarm` | Stores alarm definition; state always `OK` |
| `DescribeAlarms` | Returns stored alarms filtered by name or state |
| `DescribeAlarmsForMetric` | Returns alarms matching namespace + metric + dimensions |
| `DeleteAlarms` | Removes stored alarms |
| `SetAlarmState` | Accepted and ignored (state stays `OK`) |
| `EnableAlarmActions` / `DisableAlarmActions` | Accepted and ignored |
| `ListTagsForResource` / `TagResource` / `UntagResource` | Tag support for alarms |

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
