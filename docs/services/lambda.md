# Lambda

In-memory Lambda emulator. Zip functions are stored but never executed: invocations record the payload and return a configurable mock response, or proxy to an endpoint you register. **Container-image functions run for real** — Nimbus starts the image as a Docker container and invokes the handler inside it (see [Container image execution](#container-image-execution)).

Detection: `/2015-03-31/` path prefix.

## Supported operations

### Functions

| Method | Path | Operation |
|--------|------|-----------|
| POST | `/2015-03-31/functions` | CreateFunction |
| GET | `/2015-03-31/functions` | ListFunctions |
| GET | `/2015-03-31/functions/{name}` | GetFunction |
| GET | `/2015-03-31/functions/{name}/configuration` | GetFunctionConfiguration |
| PUT | `/2015-03-31/functions/{name}/code` | UpdateFunctionCode |
| PUT | `/2015-03-31/functions/{name}/configuration` | UpdateFunctionConfiguration |
| DELETE | `/2015-03-31/functions/{name}` | DeleteFunction |
| GET | `/2015-03-31/functions/{name}/versions` | ListVersionsByFunction |
| POST | `/2015-03-31/functions/{name}/versions` | PublishVersion |

#### Package types

Both `Zip` and `Image` are accepted. `Zip` is the default when `PackageType` is omitted, and requires `Handler` and `Runtime`; `Image` requires `Code.ImageUri` instead and rejects the request without it.

Nimbus stores the image reference and any `ImageConfig` overrides, and runs the image on invoke — see [Container image execution](#container-image-execution).

The image reference is reported the way AWS reports it, in the `Code` block of `GetFunction` rather than in `FunctionConfiguration`:

```json
{
  "Configuration": {
    "PackageType": "Image",
    "CodeSha256": "9f2c…",
    "ImageConfigResponse": { "ImageConfig": { "Command": ["app.handler"] } }
  },
  "Code": {
    "RepositoryType": "ECR",
    "ImageUri": "localhost:4566/my-image:dev",
    "ResolvedImageUri": "localhost:4566/my-image@sha256:9f2c…"
  }
}
```

`ResolvedImageUri` and `CodeSha256` are derived deterministically from the reference — no image is inspected, so the digest is stable and unique per reference but synthetic. A reference that is already digest-pinned is reported unchanged, and a registry port (`localhost:4566/repo`) is never mistaken for a tag.

`UpdateFunctionCode` with an `ImageUri` repoints the function and re-derives the digest.

## Container image execution

Invoking a container-image function starts its image as a real container and runs the handler inside it. It requires a reachable Docker daemon; without one, image functions fall back to the mock response path and a warning is logged at startup.

### Why the runtime emulator is injected

A Lambda container image is **not an HTTP server**. The process inside is a *client* of the Lambda Runtime API: it long-polls `GET $AWS_LAMBDA_RUNTIME_API/2018-06-01/runtime/invocation/next`, runs the handler, and posts the result back. In production AWS supplies the server half. Locally nothing does, so `docker run` on the image alone gives you nothing to call.

Nimbus supplies that half with the AWS Runtime Interface Emulator. It is **not** required in your image — AWS recommends against shipping it in a production image, and Nimbus injects its own copy:

1. `docker create` with `--entrypoint` set to the emulator
2. `docker cp` the emulator binary into the container
3. `docker start`

`docker cp` rather than a bind mount is deliberate: Nimbus talks to the *host* daemon, so a `-v` source path resolves against the host filesystem, not Nimbus's own. When Nimbus itself runs in a container, a bind mount silently produces an empty directory.

Because overriding `--entrypoint` discards the image's own, Nimbus recovers it with `docker inspect` and hands it to the emulator as the program to run. `ImageConfig.EntryPoint`/`Command` override that when set, as they do in Lambda.

The emulator binary is downloaded once per architecture and cached under the data directory. Set `NIMBUS_LAMBDA_RIE_PATH` to a local copy for offline environments.

### Configuration mapping

| Function setting | Container behaviour |
|---|---|
| `MemorySize` | `--memory` |
| `EphemeralStorage.Size` | fresh volume mounted at `/tmp` per cold start — disk-backed, so it is not charged against the memory limit the way a tmpfs would be |
| `Architectures` | `--platform`, and selects the matching emulator build |
| `Timeout` | bounds the invocation; on expiry the response carries `X-Amz-Function-Error: Unhandled` and a `Function.Timeout` body |
| `Environment.Variables` | passed with `-e` |
| `ImageConfig` | entrypoint, command, and working directory overrides |

### Lifecycle

Containers are reused between invocations, matching how Lambda reuses execution environments. A container is torn down when the function is deleted, on `/_nimbus/reset`, on shutdown, and once it has sat unused for `NIMBUS_LAMBDA_CONTAINER_IDLE` (default `10m`; set `0s` to disable, which is useful when attaching a debugger to a warm container).

An invocation in flight holds its container open regardless of the idle window — a function may legitimately run longer than it.

### Logs

Container output is forwarded to CloudWatch Logs under `/aws/lambda/{function-name}`, with one stream per container in Lambda's own format (`YYYY/MM/DD/[$LATEST]{id}`). Both stdout and stderr land in that stream, interleaved with the runtime emulator's `INIT REPORT`, `START`, `END`, and `REPORT RequestId` lines:

```bash
nimbuslocal logs filter-log-events --log-group-name /aws/lambda/my-function \
  --query 'events[].message' --output text
```

The group and stream are created on first output — nothing has to declare them up front.

### Reaching Nimbus from the handler

Containers join the Docker network in `NIMBUS_DOCKER_NETWORK` (default `nimbus-net`) and receive `AWS_ENDPOINT_URL` pointing back at Nimbus, plus placeholder credentials and a region. An SDK client built with no explicit endpoint therefore talks to Nimbus with no code change. Override the URL with `NIMBUS_LAMBDA_AWS_ENDPOINT`.

Running Nimbus in Docker Compose requires the daemon socket:

```yaml
volumes:
  - /var/run/docker.sock:/var/run/docker.sock
networks:
  - nimbus-net
```

### Not emulated

IAM is not enforced and `VpcConfig`, security groups, and subnets are inert. A function's execution role and network restrictions round-trip through the API but place no limits on what the container can reach — a handler runs with whatever access the daemon gives it.

### Inspecting

```bash
# Container ID backing each warm function
curl http://localhost:4566/_nimbus/lambda/containers

# Tear every running function container down
curl -X DELETE http://localhost:4566/_nimbus/lambda/containers
```

### Invocations

| Method | Path | Operation |
|--------|------|-----------|
| POST | `/2015-03-31/functions/{name}/invocations` | Invoke |
| POST | `/2015-03-31/functions/{name}/invoke-async` | InvokeAsync |
| POST | `/2015-03-31/functions/{name}/response-streaming-invocations` | InvokeWithResponseStream |

### Aliases

| Method | Path | Operation |
|--------|------|-----------|
| POST | `/2015-03-31/functions/{name}/aliases` | CreateAlias |
| GET | `/2015-03-31/functions/{name}/aliases` | ListAliases |
| GET | `/2015-03-31/functions/{name}/aliases/{alias}` | GetAlias |
| PUT | `/2015-03-31/functions/{name}/aliases/{alias}` | UpdateAlias |
| DELETE | `/2015-03-31/functions/{name}/aliases/{alias}` | DeleteAlias |

### Permissions

| Method | Path | Operation |
|--------|------|-----------|
| POST | `/2015-03-31/functions/{name}/policy` | AddPermission |
| GET | `/2015-03-31/functions/{name}/policy` | GetPolicy |
| DELETE | `/2015-03-31/functions/{name}/policy/{statementId}` | RemovePermission |

### Event Source Mappings

| Method | Path | Operation |
|--------|------|-----------|
| POST | `/2015-03-31/event-source-mappings` | CreateEventSourceMapping |
| GET | `/2015-03-31/event-source-mappings` | ListEventSourceMappings |
| GET | `/2015-03-31/event-source-mappings/{uuid}` | GetEventSourceMapping |
| PUT | `/2015-03-31/event-source-mappings/{uuid}` | UpdateEventSourceMapping |
| DELETE | `/2015-03-31/event-source-mappings/{uuid}` | DeleteEventSourceMapping |

### Concurrency

| Method | Path | Operation |
|--------|------|-----------|
| PUT | `/2015-03-31/functions/{name}/concurrency` | PutFunctionConcurrency |
| GET | `/2015-03-31/functions/{name}/concurrency` | GetFunctionConcurrency |
| DELETE | `/2015-03-31/functions/{name}/concurrency` | DeleteFunctionConcurrency |
| PUT | `/2015-03-31/functions/{name}/provisioned-concurrency` | PutProvisionedConcurrencyConfig |
| GET | `/2015-03-31/functions/{name}/provisioned-concurrency` | GetProvisionedConcurrencyConfig / ListProvisionedConcurrencyConfigs |
| DELETE | `/2015-03-31/functions/{name}/provisioned-concurrency` | DeleteProvisionedConcurrencyConfig |

### Layers

| Method | Path | Operation |
|--------|------|-----------|
| GET | `/2015-03-31/layers` | ListLayers |
| POST | `/2015-03-31/layers/{name}/versions` | PublishLayerVersion |
| GET | `/2015-03-31/layers/{name}/versions` | ListLayerVersions |
| GET | `/2015-03-31/layers/{name}/versions/{n}` | GetLayerVersion |
| DELETE | `/2015-03-31/layers/{name}/versions/{n}` | DeleteLayerVersion |
| POST | `/2015-03-31/layers/{name}/versions/{n}/policy` | AddLayerVersionPermission |
| GET | `/2015-03-31/layers/{name}/versions/{n}/policy` | GetLayerVersionPolicy |
| DELETE | `/2015-03-31/layers/{name}/versions/{n}/policy/{statementId}` | RemoveLayerVersionPermission |

### Code Signing

| Method | Path | Operation |
|--------|------|-----------|
| POST | `/2015-03-31/code-signing-configs` | CreateCodeSigningConfig |
| GET | `/2015-03-31/code-signing-configs` | ListCodeSigningConfigs |
| GET | `/2015-03-31/code-signing-configs/{arn}` | GetCodeSigningConfig |
| PUT | `/2015-03-31/code-signing-configs/{arn}` | UpdateCodeSigningConfig |
| DELETE | `/2015-03-31/code-signing-configs/{arn}` | DeleteCodeSigningConfig |
| GET | `/2015-03-31/code-signing-configs/{arn}/functions` | ListFunctionsByCodeSigningConfig |
| PUT | `/2015-03-31/functions/{name}/code-signing-config` | PutFunctionCodeSigningConfig |
| GET | `/2015-03-31/functions/{name}/code-signing-config` | GetFunctionCodeSigningConfig |
| DELETE | `/2015-03-31/functions/{name}/code-signing-config` | DeleteFunctionCodeSigningConfig |

### Function URLs

| Method | Path | Operation |
|--------|------|-----------|
| POST | `/2015-03-31/functions/{name}/url` | CreateFunctionUrlConfig |
| GET | `/2015-03-31/functions/{name}/url` | GetFunctionUrlConfig |
| PUT | `/2015-03-31/functions/{name}/url` | UpdateFunctionUrlConfig |
| DELETE | `/2015-03-31/functions/{name}/url` | DeleteFunctionUrlConfig |
| GET | `/2015-03-31/functions/{name}/urls` | ListFunctionUrlConfigs |

### Event Invoke Config

| Method | Path | Operation |
|--------|------|-----------|
| PUT | `/2015-03-31/functions/{name}/event-invoke-config` | PutFunctionEventInvokeConfig |
| GET | `/2015-03-31/functions/{name}/event-invoke-config` | GetFunctionEventInvokeConfig |
| POST | `/2015-03-31/functions/{name}/event-invoke-config` | UpdateFunctionEventInvokeConfig |
| DELETE | `/2015-03-31/functions/{name}/event-invoke-config` | DeleteFunctionEventInvokeConfig |
| GET | `/2015-03-31/event-invoke-config/functions` | ListFunctionEventInvokeConfigs |

### Runtime & Recursion Settings

| Method | Path | Operation |
|--------|------|-----------|
| GET | `/2015-03-31/functions/{name}/runtime-management-config` | GetRuntimeManagementConfig |
| PUT | `/2015-03-31/functions/{name}/runtime-management-config` | PutRuntimeManagementConfig |
| GET | `/2015-03-31/functions/{name}/recursion-config` | GetFunctionRecursionConfig |
| PUT | `/2015-03-31/functions/{name}/recursion-config` | PutFunctionRecursionConfig |
| GET | `/2015-03-31/account-settings` | GetAccountSettings |

### Tags

| Method | Path | Operation |
|--------|------|-----------|
| GET | `/2015-03-31/tags/{arn}` | ListTags |
| POST | `/2015-03-31/tags/{arn}` | TagResource |
| DELETE | `/2015-03-31/tags/{arn}` | UntagResource |

## Inspecting invocations

All invocations are captured in memory for use in integration tests.

```bash
# List all recorded invocations
curl http://localhost:4566/_nimbus/lambda/invocations

# Clear between tests
curl -X DELETE http://localhost:4566/_nimbus/lambda/invocations

# Configure a mock response for a function
curl -X PUT http://localhost:4566/_nimbus/lambda/functions/my-func/response \
  -H 'Content-Type: application/json' \
  -d '{"statusCode":200,"body":"hello"}'
```

## Example

```bash
nimbuslocal lambda create-function \
  --function-name my-func \
  --runtime nodejs22.x \
  --role arn:aws:iam::000000000000:role/lambda-role \
  --handler index.handler \
  --zip-file fileb://function.zip

nimbuslocal lambda invoke \
  --function-name my-func \
  --payload '{"key":"value"}' \
  response.json

nimbuslocal lambda create-alias \
  --function-name my-func \
  --name live \
  --function-version 1

nimbuslocal lambda create-event-source-mapping \
  --function-name my-func \
  --event-source-arn arn:aws:sqs:us-east-1:000000000000:my-queue \
  --batch-size 10
```
