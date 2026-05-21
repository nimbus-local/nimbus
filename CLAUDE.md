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
