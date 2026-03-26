# Contributing to Nimbus

Thank you for helping make local AWS development free for everyone.

## Adding a new AWS service

1. Create `internal/services/<name>/<name>.go`
2. Implement the `services.Service` interface:
   ```go
   type Service interface {
       Name()                        string
       Detect(r *http.Request)       bool
       ServeHTTP(w http.ResponseWriter, r *http.Request)
   }
   ```
3. Register it in `cmd/nimbus/main.go` — more specific detectors before less specific ones
4. Add a section to the README services table
5. Open a PR

## Guidelines

- **No external dependencies.** Nimbus has zero runtime dependencies by design. Use stdlib.
- **No telemetry.** Never add any analytics, usage reporting, or outbound calls.
- **No auth enforcement.** Accept any credentials. This is a local dev tool.
- **MIT contributions only.** All code must be compatible with the MIT license.
- **AWS parity over convenience.** If real AWS returns a specific XML error shape, we should too.

## Running locally

```bash
go build ./...
go test ./...
go vet ./...
gofmt -l .
```

## Release process

Releases are triggered by pushing a semver tag:

```bash
git tag v0.2.0
git push origin v0.2.0
```

GitHub Actions builds a multi-arch Docker image (`linux/amd64`, `linux/arm64`)
and pushes it to GHCR automatically.
