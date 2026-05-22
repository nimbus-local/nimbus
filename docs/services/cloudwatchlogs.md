# CloudWatch Logs

In-memory CloudWatch Logs emulator. Log groups, streams, and events are stored locally — nothing is sent to AWS. Once containers run with the `awslogs` log driver pointed at Nimbus, their output lands here and can be retrieved via the standard API or the `/_nimbus/logs/` inspection endpoint.

**Detection:** `X-Amz-Target: Logs_20140328.*`

## Supported operations

| Operation | Notes |
|-----------|-------|
| `CreateLogGroup` | Creates a log group; returns `ResourceAlreadyExistsException` on duplicate |
| `DeleteLogGroup` | Removes group and all its streams |
| `DescribeLogGroups` | Lists groups; supports `logGroupNamePrefix` filter |
| `CreateLogStream` | Creates a stream inside a group |
| `DescribeLogStreams` | Lists streams; supports `logStreamNamePrefix` filter |
| `PutLogEvents` | Accepts log events from containers (`awslogs` driver) or any SDK caller; capped at 10,000 events per stream |
| `GetLogEvents` | Retrieves events from a stream; supports `startTime`, `endTime`, `limit` |
| `FilterLogEvents` | Substring pattern filter across one or more streams |

## Inspection endpoint

```bash
# Dump all events in a stream as plain text (timestamp + message per line)
curl http://localhost:4566/_nimbus/logs/{group}/{stream}
```

## Example

```bash
nimbuslocal logs create-log-group --log-group-name /myapp/prod

nimbuslocal logs create-log-stream \
  --log-group-name /myapp/prod \
  --log-stream-name 2024/01/01/container

nimbuslocal logs put-log-events \
  --log-group-name /myapp/prod \
  --log-stream-name 2024/01/01/container \
  --log-events "[{\"timestamp\":$(date +%s%3N),\"message\":\"hello world\"}]"

nimbuslocal logs get-log-events \
  --log-group-name /myapp/prod \
  --log-stream-name 2024/01/01/container

nimbuslocal logs filter-log-events \
  --log-group-name /myapp/prod \
  --filter-pattern "ERROR"

curl http://localhost:4566/_nimbus/logs/myapp/prod/2024/01/01/container
```
