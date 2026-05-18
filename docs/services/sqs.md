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

## Example

```bash
nimbuslocal sqs create-queue --queue-name my-queue
nimbuslocal sqs send-message --queue-url http://sqs.us-east-1.localhost:4566/000000000000/my-queue --message-body '{"event":"test"}'
nimbuslocal sqs receive-message --queue-url http://sqs.us-east-1.localhost:4566/000000000000/my-queue
```
