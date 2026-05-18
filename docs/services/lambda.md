# Lambda

In-memory Lambda emulator. Functions are stored and invoked locally — no Docker-per-function, no execution sandbox. Invocations record the payload and return a configurable mock response.

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
