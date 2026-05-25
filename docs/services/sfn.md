# Step Functions

In-memory Step Functions emulator. State machine definitions (ASL JSON) and execution history are stored in memory for the lifetime of the Nimbus container. The execution engine runs state machines in goroutines and supports all core state types, including Lambda Task invocation via the local Lambda service.

Detection: `X-Amz-Target: AWSStepFunctions.*`

## Supported operations

| Operation | Notes |
|-----------|-------|
| `CreateStateMachine` | Validates ASL JSON; rejects duplicates; `STANDARD` and `EXPRESS` types accepted |
| `DescribeStateMachine` | Returns definition, roleArn, type, `status=ACTIVE` |
| `UpdateStateMachine` | Updates definition and/or roleArn |
| `DeleteStateMachine` | Idempotent — returns success even if not found |
| `ListStateMachines` | Returns all state machines |
| `TagResource` | Tags a state machine by ARN |
| `UntagResource` | Removes tags by key |
| `ListTagsForResource` | Returns all tags for a state machine |
| `StartExecution` | Runs state machine in a goroutine; returns `executionArn` immediately |
| `DescribeExecution` | Returns status, input, output, start/stop timestamps |
| `GetExecutionHistory` | Returns ordered list of state transition events |
| `StopExecution` | Aborts a running execution; sets status `ABORTED` |

## Supported state types

| State type | Behaviour |
|------------|-----------|
| `Pass` | Passes input to output; supports `Result`, `ResultPath`, `InputPath`, `OutputPath` |
| `Succeed` | Terminates execution with `SUCCEEDED` |
| `Fail` | Terminates execution with `FAILED`; records `Error` and `Cause` |
| `Choice` | Evaluates `Choices` in order; all comparison operators supported (String, Numeric, Boolean, type checks, `And`/`Or`/`Not`, `StringMatches` with `*` wildcards) |
| `Wait` | Sleeps for `Seconds`, `SecondsPath`, `Timestamp`, or `TimestampPath`; abortable via `StopExecution` |
| `Task` | Invokes a Lambda function by ARN via local Lambda HTTP; `Retry` (with exponential backoff) and `Catch` (per-error or `States.ALL`) supported |
| `Parallel` | Runs branches concurrently; output is an ordered array of branch results; failing branch cancels all others |
| `Map` | Iterates over an array (via `ItemsPath`); runs `Iterator` state machine per item; `MaxConcurrency` respected |

## Example

```bash
# Create a Pass → Succeed state machine
nimbuslocal stepfunctions create-state-machine \
  --name my-workflow \
  --definition '{
    "StartAt": "Hello",
    "States": {
      "Hello": {"Type": "Pass", "Result": {"msg": "hi"}, "Next": "Done"},
      "Done":  {"Type": "Succeed"}
    }
  }' \
  --role-arn arn:aws:iam::000000000000:role/sfn-role \
  --type STANDARD

# Start an execution
EXEC_ARN=$(nimbuslocal stepfunctions start-execution \
  --state-machine-arn arn:aws:states:us-east-1:000000000000:stateMachine:my-workflow \
  --input '{"x": 1}' \
  --query executionArn --output text)

# Poll until done
nimbuslocal stepfunctions describe-execution --execution-arn "$EXEC_ARN"

# Inspect history
nimbuslocal stepfunctions get-execution-history --execution-arn "$EXEC_ARN"

# Stop a running execution
nimbuslocal stepfunctions stop-execution \
  --execution-arn "$EXEC_ARN" \
  --error "ManualStop" --cause "testing"
```

### Task state invoking Lambda

```bash
# Create a workflow that calls a Lambda function
nimbuslocal stepfunctions create-state-machine \
  --name lambda-workflow \
  --definition '{
    "StartAt": "CallFn",
    "States": {
      "CallFn": {
        "Type": "Task",
        "Resource": "arn:aws:lambda:us-east-1:000000000000:function:my-fn",
        "Retry": [{"ErrorEquals": ["States.TaskFailed"], "MaxAttempts": 2}],
        "Catch": [{"ErrorEquals": ["States.ALL"], "Next": "Recover"}],
        "End": true
      },
      "Recover": {"Type": "Fail", "Error": "Unrecoverable"}
    }
  }' \
  --role-arn arn:aws:iam::000000000000:role/sfn-role \
  --type STANDARD
```

### Parallel state

```bash
nimbuslocal stepfunctions create-state-machine \
  --name parallel-workflow \
  --definition '{
    "StartAt": "Fan",
    "States": {
      "Fan": {
        "Type": "Parallel",
        "Branches": [
          {"StartAt":"A","States":{"A":{"Type":"Pass","Result":1,"End":true}}},
          {"StartAt":"B","States":{"B":{"Type":"Pass","Result":2,"End":true}}}
        ],
        "End": true
      }
    }
  }' \
  --role-arn arn:aws:iam::000000000000:role/sfn-role \
  --type STANDARD
```

### Map state

```bash
nimbuslocal stepfunctions create-state-machine \
  --name map-workflow \
  --definition '{
    "StartAt": "Process",
    "States": {
      "Process": {
        "Type": "Map",
        "ItemsPath": "$.items",
        "Iterator": {
          "StartAt": "Each",
          "States": {"Each": {"Type": "Pass", "End": true}}
        },
        "ResultPath": "$.results",
        "End": true
      }
    }
  }' \
  --role-arn arn:aws:iam::000000000000:role/sfn-role \
  --type STANDARD

nimbuslocal stepfunctions start-execution \
  --state-machine-arn arn:aws:states:us-east-1:000000000000:stateMachine:map-workflow \
  --input '{"items": ["a", "b", "c"]}'
```
