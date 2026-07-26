# CloudWatch Logs

In-memory CloudWatch Logs emulator. Log groups, streams, and events are stored locally — nothing is sent to AWS. Retrieve them through the standard API or the `/_nimbus/logs/` inspection endpoint.

Container-image Lambda functions forward their output here automatically, under `/aws/lambda/{function-name}` — see [lambda.md](lambda.md#logs). Containers run with the `awslogs` driver pointed at Nimbus also land here.

**Detection:** `X-Amz-Target: Logs_20140328.*`

## Supported operations

| Operation | Notes |
|-----------|-------|
| `CreateLogGroup` | Creates a log group; accepts `kmsKeyId`; returns `ResourceAlreadyExistsException` on duplicate |
| `DeleteLogGroup` | Removes group and all its streams |
| `DescribeLogGroups` | Lists groups; supports `logGroupNamePrefix` filter; reports `retentionInDays` and `kmsKeyId` when set |
| `CreateLogStream` | Creates a stream inside a group |
| `DeleteLogStream` | Removes stream and all its events from the group |
| `DescribeLogStreams` | Lists streams; supports `logStreamNamePrefix` filter |
| `PutLogEvents` | Accepts log events from containers (`awslogs` driver) or any SDK caller; capped at 10,000 events per stream |
| `GetLogEvents` | Retrieves events from a stream; supports `startTime`, `endTime`, `limit` |
| `FilterLogEvents` | Filters across one or more streams; supports JSON, space-delimited, and term [filter patterns](#filter-patterns). Events come back oldest first, interleaved across streams. A pattern that does not parse returns `InvalidParameterException` |
| `PutRetentionPolicy` | Stores the retention period; read back via `DescribeLogGroups`. Events are never expired |
| `DeleteRetentionPolicy` | Clears the stored retention period |
| `AssociateKmsKey` | Records the CMK for a group; read back via `DescribeLogGroups`. Stored logs are never encrypted |
| `DisassociateKmsKey` | Clears the recorded CMK |
| `ListTagsForResource` / `ListTagsLogGroup` | Returns empty tag map |

## Filter patterns

`FilterLogEvents` understands the three CloudWatch filter-pattern forms. Which
one applies is decided by the first character: `{` for JSON, `[` for
space-delimited, anything else for terms.

**JSON** — for events whose message is a JSON document. Non-JSON events never
match a JSON pattern.

| Form | Example | Notes |
|------|---------|-------|
| Selector | `{ $.Container.Name = "app" }` | Nested members, array indexes (`$.Containers[0].Name`), and quoted names (`$["odd-key"]`) |
| String comparison | `{ $.Type = "Task" }` | `=` and `!=`; `*` is a wildcard, so `{ $.Image = "*:latest" }` works |
| Numeric comparison | `{ $.CpuUtilized >= 64.5 }` | `=`, `!=`, `<`, `<=`, `>`, `>=` against JSON numbers |
| Boolean / null | `{ $.Essential IS TRUE }` | `IS TRUE`, `IS FALSE`, `IS NULL` |
| Existence | `{ $.StoppedReason NOT EXISTS }` | Every other operator treats a missing member as "no match" |
| Composition | `{ ($.a = 1 \|\| $.b = 2) && $.c IS TRUE }` | `&&`, `\|\|`, parentheses |

**Space-delimited** — for positional records. The message is split on
whitespace, keeping `[...]` and `"..."` runs whole, so an Apache-style access
log line reads as seven fields:

```
[ip, id, user, timestamp, request, status_code = 4*, size]
[..., status_code = 404, size]          # ... absorbs any number of leading fields
[ip, id, user, t, req, status >= 500, size]
```

Field names are declared by the pattern; conditions may reference them by name
or by position (`$1` is the first field). Without a `...`, the field count must
match exactly.

**Terms** — for unstructured messages. Bare terms must all be present, `-`
terms must be absent, and where `?` terms are given at least one must be
present. Quotes group a phrase. Matching is case-sensitive substring matching.

```
ERROR                       # contains ERROR
ERROR -Timeout              # contains ERROR, does not contain Timeout
?ERROR ?WARN                # contains either
"connection refused"        # contains the phrase
```

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

# JSON pattern against structured events
nimbuslocal logs filter-log-events \
  --log-group-name /aws/ecs/containerinsights/my-cluster/performance \
  --filter-pattern '{ $.Type = "Task" }'

curl http://localhost:4566/_nimbus/logs/myapp/prod/2024/01/01/container
```
