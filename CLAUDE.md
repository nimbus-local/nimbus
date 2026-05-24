# Nimbus — Claude Code guidance

## Documentation — required after every new service

Whenever you implement a new AWS service, you **must** complete all three steps before considering the task done:

1. **Create `docs/services/{name}.md`** following this structure:
   - One-paragraph description (in-memory vs proxied, what's never sent, etc.)
   - **Detection** line (how the router identifies requests)
   - **Supported operations** table — list every implemented operation with notable behaviour
   - **Example** block with `nimbuslocal` CLI commands
   - Any Nimbus-specific inspection endpoints (`/_nimbus/{name}/...`) if present

   Copy the closest existing doc as a template. `docs/services/ses.md` is compact; `docs/services/lambda.md` shows the multi-section style.

2. **Add a row to the services table in `README.md`**:
   ```
   | [Name](docs/services/{name}.md) | ✅ Core | `detection hint` | Brief operation summary |
   ```
   Status badges: `✅ Core` (partial coverage), `✅ Full` (full parity / proxied), `🚧 In Progress`.

3. **Update `.github/CONTRIBUTING.md`** only if the new service introduces a pattern contributors haven't seen before (new detection strategy, new inspection endpoint style, etc.). Otherwise leave it alone.

## Project layout

```
internal/
  router/          # Edge router — detects and dispatches
  services/
    {name}/        # One package per AWS service
  auth/            # Credential extraction (accepts anything)
  config/          # Environment-based configuration
  uid/             # UUID generation
cmd/
  nimbus/          # Server entrypoint — register new services here
  nimbuslocal/     # AWS CLI wrapper
docs/
  services/        # Per-service API reference docs
```

## Adding a new service

1. Create `internal/services/{name}/` and implement the `services.Service` interface (`Name()`, `Detect()`, `ServeHTTP()`).
2. Register it in `cmd/nimbus/main.go` before S3 (the catch-all).
3. Follow the README update steps above.

## Project North Star

The long-term roadmap is tracked in [`docs/north-star-roadmap.md`](docs/north-star-roadmap.md).
It describes the phased plan to make Nimbus a complete local ECS development environment
(ECS container execution, ALB, CloudWatch Logs, IAM, Aurora, Valkey, ACM, Route 53, CloudWatch Metrics).

When implementing a North Star phase, follow the same checklist above **plus**:
- Add a Terraform fixture in `infra/terraform/`
- Add a smoke test section in `infra/scripts/smoke-test.sh`

## Terraform AWS provider v6 compatibility

Hard-won discoveries. Each entry saved hours of debugging — do not re-derive them.

### provider.tf — endpoint entry required for every new service

When `endpoints {}` is explicitly configured in `provider.tf`, any service **not listed** hits real AWS instead of Nimbus. Always add `{service_key} = var.nimbus_endpoint` when implementing a new service. The key is usually the lowercase service name (`cloudwatch`, `cloudwatchlogs`, etc.). Pattern: check the existing entries in `infra/terraform/provider.tf`.

### CloudWatch Metrics — dual protocol (smithy-rpc-v2-cbor vs awsJson1.0)

The AWS CLI uses **awsJson1.0**: `POST /` with `X-Amz-Target: GraniteServiceVersion20100801.{Action}` and `Content-Type: application/x-amz-json-1.0`.

TF provider v6 (AWS SDK Go v2) uses **smithy-rpc-v2-cbor**: `POST /service/GraniteServiceVersion20100801/operation/{Action}` with `Content-Type: application/cbor` and `smithy-protocol: rpc-v2-cbor` header. The response must also set `smithy-protocol: rpc-v2-cbor` + `Content-Type: application/cbor`.

Detect both:
```go
func (s *Service) Detect(r *http.Request) bool {
    return strings.HasPrefix(r.Header.Get("X-Amz-Target"), "GraniteServiceVersion20100801.") ||
        strings.HasPrefix(r.URL.Path, "/service/GraniteServiceVersion20100801/operation/")
}
```

**CBOR timestamp encoding**: timestamps must be CBOR tag-1 epoch seconds (`0xc1` + uint64 epoch), NOT RFC3339 strings. The inline encoder/decoder lives in `internal/services/cloudwatchmetrics/cbor.go`.

Use `TF_LOG=DEBUG terraform apply` to inspect the actual HTTP method, URL, and headers the provider sends if a new service behaves unexpectedly.

### Re-apply stub operations (TF v6 calls these on second `terraform apply`)

TF provider v6 calls update operations not needed during fresh creation. Implement stubs that return the existing resource:

| Service | Operation | Returns |
|---------|-----------|---------|
| RDS | `ModifyDBSubnetGroup` | existing subnet group XML |
| ALB | `SetSubnets` | availability zones XML |
| ALB | `ModifyTargetGroup` | existing target group XML |
| ElastiCache | `ModifyCacheSubnetGroup` | existing subnet group XML |

Without these, `terraform apply` on an already-provisioned environment returns 400/InvalidAction.

### Delete path operations (v6 calls these before deleting resources)

| Service | Operation required before delete |
|---------|----------------------------------|
| IAM | `ListInstanceProfilesForRole`, `ListPolicyVersions` |
| CloudWatch Logs | `DeleteLogStream` |
| ECS services | After `DeleteService`, keep service in map with `status = "INACTIVE"` and filter from `ListServices` but not `DescribeServices` |

### DynamoDB WarmThroughput injection

Provider v6 reads `WarmThroughput.Status` from `DescribeTable`. DynamoDB Local omits this field. Fix: intercept the response with a `captureWriter`, parse JSON, inject `"WarmThroughput":{"Status":"ACTIVE","ReadUnitsPerSecond":0,"WriteUnitsPerSecond":0}` before forwarding to the client.

### S3 `GetBucketLifecycleConfiguration` — `x-amz-transition-default-minimum-object-size` response header

The Pulumi/TF AWS provider v5.44+ includes a waiter after `PutBucketLifecycleConfiguration`. The waiter polls `GetBucketLifecycleConfiguration` with `ContinuousTargetOccurence: 2` (needs 2 consecutive successes) and checks whether the response sets the HTTP header:

```
x-amz-transition-default-minimum-object-size: all_storage_classes_128K
```

**Critical**: the AWS SDK v2 reads `TransitionDefaultMinimumObjectSize` from this HTTP response **header**, not from the XML body. See `deserializers.go` in `aws-sdk-go-v2/service/s3`:

```go
if headerValues := response.Header.Values("x-amz-transition-default-minimum-object-size"); len(headerValues) != 0 {
    v.TransitionDefaultMinimumObjectSize = types.TransitionDefaultMinimumObjectSize(headerValues[0])
}
```

If the header is absent, `TransitionDefaultMinimumObjectSize` is the empty string, the waiter evaluates `false` on every poll, and **the deploy times out after exactly 3 minutes** — a very misleading failure mode with no helpful error message.

**Fix**: `getBucketLifecycle` in `internal/services/s3/bucket.go` sets this header unconditionally on success responses:

```go
w.Header().Set("x-amz-transition-default-minimum-object-size", "all_storage_classes_128K")
```

The value `all_storage_classes_128K` is the AWS server-side default for all lifecycle configurations created since November 2023. The waiter completes in ~25 seconds (two polls separated by the `minDelay`).

Injecting the tag into the XML body has **no effect** — the SDK only reads the header.

## Implementation strategy — parts, not phases

Every phase is split into numbered parts in the roadmap. **Each part is one working
commit.** Never implement an entire phase in one pass.

Rules:
1. **Write code first.** Do not plan, outline, or reason about an implementation
   for more than a few sentences before opening a file and writing real code.
2. **Part 1 always unblocks the critical path** — the minimum needed for Terraform
   `init`/`plan` to pass, or for an integration test to run. Later parts add depth.
3. **Compile and `go vet` after every part.** A part is not done until `go build ./...`
   passes with no errors.
4. **Complete the checklist after Part 1** (service doc, README row, Terraform fixture,
   smoke test). Subsequent parts update those files — they don't defer them.
5. **Never re-read files you just wrote.** Trust that Edit/Write succeeded.
6. **One file at a time.** Write it, move on. No drafting the same file twice.
