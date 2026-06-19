# Contributing to Nimbus

Thank you for helping make local AWS development free for everyone.

## Adding a new AWS service

1. Create `internal/services/<name>/` and implement the `services.Service` interface:
   ```go
   type Service interface {
       Name()    string
       Detect(r *http.Request) bool
       ServeHTTP(w http.ResponseWriter, r *http.Request)
       Reset()   // clears ALL in-memory state — required for /_nimbus/reset
   }
   ```
   **`Reset()` is mandatory.** The `Service Contract` CI check (a required PR check) enforces this at compile time — `go build` fails if `Reset()` is missing.
2. Register it in `cmd/nimbus/main.go` — more specific detectors before less specific ones. S3 is the catch-all and must stay last.
3. Write tests alongside the implementation (`<name>_test.go`).
4. **Document the service** — see the [Documentation](#documentation) section below.
5. Open a PR.

## Documentation

Every service has its own reference doc in `docs/services/`. When you add a service, you must also:

### 1. Create `docs/services/<name>.md`

Follow the pattern used by existing services. The file should contain:

- A one-paragraph description of what the service does locally (what's in-memory, what's proxied, what's never sent, etc.)
- A **Detection** line explaining how the edge router identifies requests for this service.
- A **Supported operations** table listing each operation and any notable behaviour.
- An **Example** block with `nimbuslocal` CLI commands.
- Any Nimbus-specific inspection endpoints (e.g. `/_nimbus/<name>/...`) if the service exposes them.

Use an existing doc as a starting point — [`docs/services/ses.md`](../docs/services/ses.md) is a good compact example; [`docs/services/lambda.md`](../docs/services/lambda.md) shows the full multi-section style for larger services.

### 2. Add a row to the services table in `README.md`

```markdown
| [Service Name](docs/services/<name>.md) | ✅ Core | `detection hint` | Short summary of supported operations |
```

Link the service name to its doc file. Match the status badge used by comparable services:

| Badge | Meaning |
|-------|---------|
| ✅ Core | Implemented — covers the operations most apps need |
| ✅ Full | Full parity (e.g. proxied to an authoritative implementation) |
| 🚧 In Progress | Work has started but isn't ready |

## Guidelines

- **No external dependencies.** Nimbus has zero runtime dependencies by design. Use stdlib.
- **No telemetry.** Never add any analytics, usage reporting, or outbound calls.
- **No auth enforcement.** Accept any credentials. This is a local dev tool.
- **MIT contributions only.** All code must be compatible with the MIT license.
- **AWS parity over convenience.** If real AWS returns a specific XML error shape, we should too.

## Running locally

**Install git hooks (run once after cloning):**

```bash
make setup
```

This sets `core.hooksPath = .githooks` to activate the committed pre-commit hook. The hook runs on every commit and will:
- Block direct commits to `main` or `master`
- Run `go fmt ./...`
- Run `go build ./...`
- Run `go vet ./...`
- Run `go test ./...`

**Unit tests (fast, no Docker required):**

```bash
go build ./...
go test ./...
go vet ./...
```

**End-to-end smoke tests (requires Docker and Terraform):**

```bash
cd infra
make start        # boot Nimbus + DynamoDB Local
make nuke         # clean slate: reset state + re-provision resources
make smoke-test   # run the full suite
make clean        # tear everything down when done
```

`make test` does all three middle steps in one shot (`nuke` + `smoke-test`).

If `make apply` fails with "already exists" errors from a previous partial run, run `make nuke` to wipe state and re-provision before retrying.

After Go code changes, rebuild before re-running: `make stop && make start && make nuke`.

The smoke test suite is also a required CI check on every PR — it runs automatically via GitHub Actions.

## Release process

Releases are triggered by pushing a semver tag:

```bash
git tag v0.2.0
git push origin v0.2.0
```

GitHub Actions builds a multi-arch Docker image (`linux/amd64`, `linux/arm64`)
and pushes it to GHCR automatically.
