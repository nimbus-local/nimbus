# EventBridge

In-memory EventBridge emulator. Events are captured locally and never routed to real targets or external AWS resources. Rules and targets are stored for inspection but event pattern matching and target invocation are not performed.

Detection: `X-Amz-Target: AmazonEventBridge.*`

## Supported operations

| Operation | Notable behaviour |
|-----------|-------------------|
| PutEvents | Captures events in memory; returns an EventId per entry, always 0 failures |
| CreateEventBus | Creates a named event bus; `default` bus is always present |
| DeleteEventBus | Deletes a custom bus; deleting `default` returns an error |
| DescribeEventBus | Returns name and ARN; omitting Name returns the default bus |
| ListEventBuses | Lists all buses; supports optional `NamePrefix` filter |
| PutRule | Creates or updates a rule; defaults to `ENABLED` state |
| DeleteRule | Removes the rule and its targets |
| DescribeRule | Returns rule name, ARN, state, event pattern, schedule, description |
| ListRules | Lists rules on a bus; supports optional `NamePrefix` filter |
| EnableRule / DisableRule | Toggles rule `State` between `ENABLED` and `DISABLED` |
| PutTargets | Associates targets (by Id + ARN) with a rule; upserts on Id |
| RemoveTargets | Removes targets by Id |
| ListTargetsByRule | Returns all targets registered on a rule |

## Inspecting captured events

Events put via `PutEvents` are available for test inspection at Nimbus-specific endpoints.

**List captured events:**
```bash
curl http://localhost:4566/_nimbus/eventbridge/events
```

**Clear between tests:**
```bash
curl -X DELETE http://localhost:4566/_nimbus/eventbridge/events
```

**Example response:**
```json
[
  {
    "EventId": "a1b2c3d4-...",
    "EventBusName": "default",
    "Source": "my-app",
    "DetailType": "order-placed",
    "Detail": "{\"orderId\":\"123\"}",
    "Time": "2026-05-20T10:00:00Z",
    "Resources": []
  }
]
```

## Example

```bash
# Put events
nimbuslocal events put-events \
  --entries '[{"Source":"my-app","DetailType":"order-placed","Detail":"{\"orderId\":\"123\"}","EventBusName":"default"}]'

# Create a custom bus
nimbuslocal events create-event-bus --name my-bus

# Create a rule
nimbuslocal events put-rule \
  --name my-rule \
  --event-bus-name default \
  --event-pattern '{"source":["my-app"]}' \
  --state ENABLED

# Register a Lambda target
nimbuslocal events put-targets \
  --rule my-rule \
  --event-bus-name default \
  --targets '[{"Id":"1","Arn":"arn:aws:lambda:us-east-1:000000000000:function:my-func"}]'

# Inspect captured events
curl http://localhost:4566/_nimbus/eventbridge/events
```
