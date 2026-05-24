# EventBridge Scheduler

EventBridge Scheduler is emulated entirely in-memory. Schedules and schedule groups are stored locally and never reach AWS. Schedule expressions are accepted and stored but the ticker (Part 2) is required before any firing occurs.

**Detection**: `r.URL.Path` starts with `/schedules`, `/schedule-groups`, or `/tags/{arn}` where the ARN contains `scheduler` (the Scheduler REST API uses bare paths, no date prefix — distinct from EventBridge Events which uses `X-Amz-Target`).

## Supported operations

| Operation | Notes |
|-----------|-------|
| `CreateScheduleGroup` | Stores group in-memory; `default` group is always present |
| `GetScheduleGroup` | Returns ARN, state (`ACTIVE`), creation/modification dates |
| `DeleteScheduleGroup` | Cascades — also deletes all schedules in the group |
| `ListScheduleGroups` | Supports `NamePrefix` query filter |
| `CreateSchedule` | Stores schedule with full target; `GroupName` defaults to `default` |
| `GetSchedule` | Returns all fields including raw `Target` JSON |
| `UpdateSchedule` | Replaces schedule expression, state, and target in place |
| `DeleteSchedule` | `groupName` query param selects group (defaults to `default`) |
| `ListSchedules` | Supports `ScheduleGroup` and `NamePrefix` query filters |
| `ListTagsForResource` | GET `/tags/{arn}` — returns stored tags |
| `TagResource` | POST `/tags/{arn}` — accepted; tags stored |
| `UntagResource` | DELETE `/tags/{arn}` — accepted; no-op |

## Example

```bash
# Create a custom schedule group
nimbuslocal scheduler create-schedule-group --name my-group

# Create a schedule in that group
nimbuslocal scheduler create-schedule \
  --name my-job \
  --group-name my-group \
  --schedule-expression "rate(1 hour)" \
  --flexible-time-window '{"Mode":"OFF"}' \
  --target '{"Arn":"arn:aws:lambda:us-east-1:000000000000:function:my-fn","RoleArn":"arn:aws:iam::000000000000:role/scheduler"}'

# Inspect stored schedules
nimbuslocal scheduler get-schedule --name my-job --group-name my-group

# List all schedules in a group
nimbuslocal scheduler list-schedules --group-name my-group
```

## Inspection endpoint

```
GET /_nimbus/scheduler/schedules
```

Returns a JSON array of all schedules across all groups, including their name, group, ARN, state, expression, and target.
