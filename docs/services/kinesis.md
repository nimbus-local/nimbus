# Kinesis Data Streams

In-memory Kinesis emulator. Each shard holds a ring buffer of up to 10 000 records. Records are never persisted to disk. No actual resharding occurs — `MergeShards` and `SplitShard` return success without moving data.

Lambda event source mappings (ESMs) targeting Kinesis streams are automatically polled every second. Batches of records are delivered to the configured Lambda function using the standard Kinesis event envelope (`aws:kinesis:record`). The ESM runner honours `StartingPosition` (`TRIM_HORIZON` or `LATEST`) and respects `BatchSize`.

**Detection:** `X-Amz-Target: Kinesis_20131202.*`

## Supported operations

| Operation | Notes |
|-----------|-------|
| `CreateStream` | `ShardCount` configures the number of shards; create-time `Tags` are stored |
| `DeleteStream` | Removes stream and all records immediately |
| `ListStreams` | Returns all stream names |
| `DescribeStream` | Full stream description including shard list |
| `DescribeStreamSummary` | Lightweight summary (no shard list) |
| `ListShards` | Returns all shards for a stream |
| `AddTagsToStream` / `ListTagsForStream` / `RemoveTagsFromStream` | Tag CRUD |
| `IncreaseStreamRetentionPeriod` / `DecreaseStreamRetentionPeriod` | Updates retention hours (not enforced for eviction) |
| `PutRecord` | Single record; partition key hashed to shard via MD5 mod shardCount |
| `PutRecords` | Batch up to 500 records; `FailedRecordCount` is always 0 |
| `GetShardIterator` | `TRIM_HORIZON`, `LATEST`, `AT_SEQUENCE_NUMBER`, `AFTER_SEQUENCE_NUMBER`, `AT_TIMESTAMP` |
| `GetRecords` | Returns records from iterator position; advances iterator |
| `MergeShards` / `SplitShard` | Stubs — return success, no resharding |
| `EnableEnhancedMonitoring` / `DisableEnhancedMonitoring` | Accepted, no-op |

## Lambda ESM integration

When a Lambda `EventSourceMapping` is created with a Kinesis stream ARN, Nimbus starts polling that stream automatically (1 s interval). Records are wrapped in the standard AWS Kinesis event envelope and delivered to the Lambda function via `Event` (async) invocation.

`StartingPosition` values supported: `TRIM_HORIZON` (from oldest record), `LATEST` (from next new record). `AT_TIMESTAMP` falls back to `TRIM_HORIZON`.

Invocations are recorded at `GET /_nimbus/lambda/invocations` for test inspection.

## Example

```bash
# Create a 2-shard stream
nimbuslocal kinesis create-stream --stream-name my-stream --shard-count 2

# Put a record
nimbuslocal kinesis put-record \
  --stream-name my-stream \
  --partition-key "user-123" \
  --data "$(echo -n 'hello' | base64)"

# Read from the beginning
SHARD=$(nimbuslocal kinesis list-shards --stream-name my-stream \
  --query 'Shards[0].ShardId' --output text)

IT=$(nimbuslocal kinesis get-shard-iterator \
  --stream-name my-stream \
  --shard-id "$SHARD" \
  --shard-iterator-type TRIM_HORIZON \
  --query ShardIterator --output text)

nimbuslocal kinesis get-records --shard-iterator "$IT"

# Clean up
nimbuslocal kinesis delete-stream --stream-name my-stream
```
