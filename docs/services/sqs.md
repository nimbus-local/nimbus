# SQS

In-memory SQS emulator with visibility timeout support.

## Supported operations

| Operation | Notes |
|-----------|-------|
| CreateQueue | Standard queues only |
| DeleteQueue | |
| GetQueueUrl | |
| GetQueueAttributes | |
| SetQueueAttributes | |
| ListQueues | Supports `QueueNamePrefix` filter |
| SendMessage | |
| SendMessageBatch | |
| ReceiveMessage | Respects `MaxNumberOfMessages`, `VisibilityTimeout`, `WaitTimeSeconds` |
| DeleteMessage | |
| DeleteMessageBatch | |
| ChangeMessageVisibility | |
| PurgeQueue | |

Detection: `Action` query param or `X-Amz-Target: AmazonSQS.*`.

## Queue URLs

Generated queue URLs have the form `http://sqs.{region}.localhost:{port}/000000000000/{name}`, where `{port}` is `NIMBUS_PORT` (default `4566`). When the advertised address must differ from the listen address — reverse proxy, remapped Docker port mapping like `4577:4566` — set `NIMBUS_EXTERNAL_URL` (e.g. `http://myhost:4577`) and queue URLs become `{NIMBUS_EXTERNAL_URL}/000000000000/{name}` instead.

## Example

```bash
nimbuslocal sqs create-queue --queue-name my-queue
nimbuslocal sqs send-message --queue-url http://sqs.us-east-1.localhost:4566/000000000000/my-queue --message-body '{"event":"test"}'
nimbuslocal sqs receive-message --queue-url http://sqs.us-east-1.localhost:4566/000000000000/my-queue
```
