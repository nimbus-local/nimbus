# Step Functions

In-memory Step Functions emulator. State machine definitions (ASL JSON) are stored in memory for the lifetime of the container. Execution engine is added in Parts 2–5; this doc will be extended with execution examples at that point.

Detection: `X-Amz-Target: AWSStepFunctions.*`

## Supported operations

| Operation | Notes |
|-----------|-------|
| `CreateStateMachine` | Stores ASL definition; validates JSON; rejects duplicates |
| `DescribeStateMachine` | Returns definition, roleArn, type, status=ACTIVE |
| `UpdateStateMachine` | Updates definition and/or roleArn |
| `DeleteStateMachine` | Idempotent — returns success even if not found |
| `ListStateMachines` | Returns all state machines |
| `TagResource` | Tags a state machine by ARN |
| `UntagResource` | Removes tags by key |
| `ListTagsForResource` | Returns all tags for a state machine |

## Example

```bash
# Create a state machine
nimbuslocal stepfunctions create-state-machine \
  --name my-workflow \
  --definition '{"Comment":"test","StartAt":"Pass","States":{"Pass":{"Type":"Pass","End":true}}}' \
  --role-arn arn:aws:iam::000000000000:role/sfn-role \
  --type STANDARD

# List state machines
nimbuslocal stepfunctions list-state-machines

# Describe
nimbuslocal stepfunctions describe-state-machine \
  --state-machine-arn arn:aws:states:us-east-1:000000000000:stateMachine:my-workflow

# Delete
nimbuslocal stepfunctions delete-state-machine \
  --state-machine-arn arn:aws:states:us-east-1:000000000000:stateMachine:my-workflow
```
