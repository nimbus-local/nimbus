# Internal Dev APIs

Nimbus-specific HTTP endpoints used by forge dev tooling and test harnesses.
None of these are AWS-compatible — they live under `/_nimbus/` and are never proxied to real AWS.

Detection: path prefix `/_nimbus/` (handled by the mux before the AWS router).

## Supported operations

| Endpoint | Method(s) | Description |
|----------|-----------|-------------|
| `/_nimbus/health` | GET | Server liveness + registered service list + active ESM count |
| `/_nimbus/state` | GET | Counts/names for all in-memory services (functions, topics, queues, etc.) |
| `/_nimbus/reset` | POST | Clear all in-memory state across every service (S3 files on disk are untouched) |
| `/_nimbus/lambda/register` | POST | Register a live local endpoint for a Lambda function (forge dev tunnel) |
| `/_nimbus/lambda/register/{name}` | DELETE | Deregister a live endpoint |
| `/_nimbus/lambda/register` | GET | List all registered live endpoints |
| `/_nimbus/lambda/invocations` | GET / DELETE | Inspect or clear recorded Lambda invocations |
| `/_nimbus/logs/{group}/{stream}` | GET | Tail CloudWatch log stream |
| `/_nimbus/metrics` | GET | Inspect captured CloudWatch metric data points |
| `/_nimbus/ses/messages` | GET / DELETE | Inspect or clear captured SES emails |
| `/_nimbus/sns/messages` | GET / DELETE | Inspect or clear captured SNS messages |
| `/_nimbus/eventbridge/events` | GET / DELETE | Inspect or clear captured EventBridge events |
| `/_nimbus/cloudfront/distributions` | GET | List CloudFront distribution state |
| `/_nimbus/acm/certs/{arn}` | GET | Download ACM certificate PEM |
| `/_nimbus/rds/clusters` | GET | List RDS cluster state |
| `/_nimbus/elasticache/clusters` | GET | List ElastiCache cluster state |
| `/_nimbus/alb/loadbalancers` | GET | List ALB state |
| `/_nimbus/alb/targetgroups` | GET | List target group state |
| `/_nimbus/alb/listeners` | GET | List listener state |
| `/_nimbus/scheduler/schedules` | GET | List schedules with next-fire and last-fired times |
| `/_nimbus/ecs/tasks/{id}/logs` | GET | Stream last 200 log lines from an ECS task |

## forge dev tunnel

The live registration endpoints let a forge dev server wire its local handler directly
into Nimbus so that Lambda invocations hit the running process instead of a mock.

### Round-trip flow

```
AWS SDK / Terraform
  └─► POST http://localhost:4566/2015-03-31/functions/{name}/invocations
        └─► Nimbus Lambda router
              └─► liveEndpoints[name] registered?
                    yes ─► POST {endpoint}   (synchronous proxy, body = payload)
                              └─► forge dev server handler
                                    └─► response piped back to SDK caller
                    no  ─► return mock response (or null)
```

### Required environment variables

| Variable | Value | Purpose |
|----------|-------|---------|
| `AWS_ENDPOINT_URL` | `http://localhost:4566` | Point all AWS SDK calls at Nimbus |
| `AWS_DEFAULT_REGION` | `us-east-1` (any) | Required by SDK; Nimbus accepts any region |
| `AWS_ACCESS_KEY_ID` | any non-empty string | Nimbus accepts all credentials |
| `AWS_SECRET_ACCESS_KEY` | any non-empty string | Nimbus accepts all credentials |

### Example

```bash
# Register a live handler (forge dev server listening on :3001)
curl -X POST http://localhost:4566/_nimbus/lambda/register \
  -H "Content-Type: application/json" \
  -d '{"function_name":"myFunction","endpoint":"http://localhost:3001"}'

# Invoke via AWS CLI — Nimbus proxies to the live server
nimbuslocal lambda invoke \
  --function-name myFunction \
  --payload '{"key":"value"}' \
  /tmp/response.json

# Deregister when the dev server stops
curl -X DELETE http://localhost:4566/_nimbus/lambda/register/myFunction

# Inspect state across all services
curl http://localhost:4566/_nimbus/state

# Reset all in-memory state between test runs
curl -X POST http://localhost:4566/_nimbus/reset
```

## Test harness pattern

```bash
# Before each test suite
curl -X POST http://localhost:4566/_nimbus/reset

# After invocations, inspect what was called
curl http://localhost:4566/_nimbus/lambda/invocations

# Assert on captured side-effects
curl http://localhost:4566/_nimbus/sns/messages
curl http://localhost:4566/_nimbus/ses/messages
```
