# SNS

In-memory SNS emulator. Topics and subscriptions are stored locally; published messages are captured and never delivered to real endpoints. All subscription protocols are accepted (`sqs`, `lambda`, `http`, `https`, `email`, `email-json`), and subscriptions are auto-confirmed — no HTTP handshake required.

Detection: `X-Amz-Target: AmazonSimpleNotificationService.*` or `Action` query/body param.

## Supported operations

| Operation | Notable behaviour |
|-----------|-------------------|
| CreateTopic | Idempotent — returns the same ARN if the topic already exists |
| DeleteTopic | Also removes all subscriptions on the topic |
| ListTopics | Returns all topic ARNs |
| GetTopicAttributes | Returns TopicArn, DisplayName, SubscriptionsConfirmed, Owner |
| SetTopicAttributes | Supports `DisplayName` |
| Subscribe | Accepts any protocol; subscription is auto-confirmed |
| Unsubscribe | Removes the subscription from the topic |
| ListSubscriptions | Returns all subscriptions across all topics |
| ListSubscriptionsByTopic | Returns subscriptions for a specific topic |
| GetSubscriptionAttributes | Returns SubscriptionArn, TopicArn, Protocol, Endpoint, Owner |
| ConfirmSubscription | No-op — subscriptions are already confirmed |
| Publish | Captures the message in memory; returns a MessageId |
| PublishBatch | Captures all entries; returns per-entry MessageId in Successful list |

## Inspecting published messages

Messages published via `Publish` or `PublishBatch` are available for test inspection at Nimbus-specific endpoints.

**List captured messages:**
```bash
curl http://localhost:4566/_nimbus/sns/messages
```

**Clear between tests:**
```bash
curl -X DELETE http://localhost:4566/_nimbus/sns/messages
```

**Example response:**
```json
[
  {
    "MessageId": "a1b2c3d4-...",
    "TopicArn": "arn:aws:sns:us-east-1:000000000000:my-topic",
    "Subject": "Order placed",
    "Message": "{\"orderId\":\"123\"}",
    "PublishedAt": "2026-05-20T10:00:00Z"
  }
]
```

## Example

```bash
# Create a topic
nimbuslocal sns create-topic --name my-topic

# Subscribe an SQS queue
nimbuslocal sns subscribe \
  --topic-arn arn:aws:sns:us-east-1:000000000000:my-topic \
  --protocol sqs \
  --notification-endpoint arn:aws:sqs:us-east-1:000000000000:my-queue

# Publish a message
nimbuslocal sns publish \
  --topic-arn arn:aws:sns:us-east-1:000000000000:my-topic \
  --message '{"orderId":"123"}' \
  --subject "Order placed"

# Inspect captured messages
curl http://localhost:4566/_nimbus/sns/messages
```
