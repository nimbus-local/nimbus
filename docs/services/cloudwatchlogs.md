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
| `PutLogEvents` | Accepts log events from containers or SDKs *(Part 2)* |
| `GetLogEvents` | Retrieves events from a stream *(Part 3)* |
| `FilterLogEvents` | Pattern-filtered event retrieval *(Part 3)* |

## Inspection endpoint

```bash
# Tail a stream (Part 3)
curl http://localhost:4566/_nimbus/logs/{group}/{stream}
```

## Example

```bash
nimbuslocal logs create-log-group --log-group-name /myapp/prod

nimbuslocal logs create-log-stream \
  --log-group-name /myapp/prod \
  --log-stream-name 2024/01/01/container

nimbuslocal logs describe-log-groups --log-group-name-prefix /myapp
```
