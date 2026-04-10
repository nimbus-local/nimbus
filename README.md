# Nimbus ☁️

**A free, open-source AWS emulator for local development. Forever.**

Nimbus runs S3, SQS, DynamoDB, Secrets Manager, SSM Parameter Store, and SES locally in a single Docker container on port `4566` — a drop-in replacement for LocalStack Community Edition. No account. No auth token. No commercial restrictions. MIT licensed.

---

## Why Nimbus?

LocalStack built something genuinely useful on the backs of open-source contributors, then locked it behind a paywall. Nimbus exists because local AWS emulation should be free for everyone — individual developers, startups, enterprises, and open-source projects alike.

> *"Free for everyone, forever."*

---

## Quickstart

```bash
docker run -p 4566:4566 ghcr.io/nimbus-local/nimbus:latest
```

Or with Docker Compose:

```yaml
services:
  nimbus:
    image: ghcr.io/nimbus-local/nimbus:latest
    ports:
      - "4566:4566"
    environment:
      AWS_DEFAULT_REGION: us-east-1
    volumes:
      - nimbus_data:/var/lib/nimbus

  dynamodb-local:
    image: amazon/dynamodb-local:latest
    command: "-jar DynamoDBLocal.jar -sharedDb -dbPath /data"
    volumes:
      - dynamodb_data:/data

volumes:
  nimbus_data:
  dynamodb_data:
```

---

## Services

| Service             | Status         | Detection                              | Notes |
|---------------------|----------------|----------------------------------------|-------|
| S3                  | ✅ Core        | catch-all (path / virtual-hosted)      | PutObject, GetObject, DeleteObject, ListObjectsV2, HeadObject, CreateBucket, DeleteBucket, multipart uploads, presigned URLs |
| SQS                 | ✅ Core        | `Action` param or `AmazonSQS.*` target | CreateQueue, SendMessage, ReceiveMessage, DeleteMessage, PurgeQueue, visibility timeout |
| DynamoDB            | ✅ Full        | `DynamoDB_*` target                    | Proxied to [DynamoDB Local](https://hub.docker.com/r/amazon/dynamodb-local) — full parity |
| Secrets Manager     | ✅ Core        | `secretsmanager.*` target              | CreateSecret, GetSecretValue, PutSecretValue, UpdateSecret, DeleteSecret, ListSecrets, DescribeSecret, RestoreSecret |
| SSM Parameter Store | ✅ Core        | `AmazonSSM.*` target                   | PutParameter, GetParameter, GetParameters, GetParametersByPath, DeleteParameter, DeleteParameters, DescribeParameters — String, StringList, SecureString, path hierarchy, versioning |
| SES                 | ✅ Core        | `AmazonSimpleEmailService.*` target or `/v2/email/` path | SendEmail (v1+v2), SendRawEmail, VerifyEmailIdentity, ListIdentities, DeleteIdentity, GetSendQuota — emails captured in memory, never sent |
| Lambda              | ✅ Core        | `/2015-03-31/` path prefix             | Functions (CRUD, versions, publish), invocations, aliases, permissions, event source mappings, concurrency, layers, code signing, function URLs, event invoke config, runtime & recursion settings, tags |
| SNS                 | 🚧 In Progress | `SNS.*` target                         | |

---

## Using the AWS SDK

Point your AWS SDK at `http://localhost:4566`. Nimbus accepts any credentials.

**Python (boto3):**
```python
import boto3

s3 = boto3.client(
    "s3",
    endpoint_url="http://localhost:4566",
    aws_access_key_id="test",
    aws_secret_access_key="test",
    region_name="us-east-1",
)
s3.create_bucket(Bucket="my-bucket")
```

**JavaScript (AWS SDK v3):**
```javascript
import { S3Client } from "@aws-sdk/client-s3";

const s3 = new S3Client({
  endpoint: "http://localhost:4566",
  region: "us-east-1",
  credentials: { accessKeyId: "test", secretAccessKey: "test" },
  forcePathStyle: true,
});
```

**Go:**
```go
cfg, _ := config.LoadDefaultConfig(context.TODO(),
    config.WithRegion("us-east-1"),
    config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
    config.WithEndpointResolverWithOptions(
        aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...interface{}) (aws.Endpoint, error) {
            return aws.Endpoint{URL: "http://localhost:4566"}, nil
        }),
    ),
)
```

---

## nimbuslocal CLI

`nimbuslocal` is a thin wrapper around the `aws` CLI that automatically injects the Nimbus endpoint. It's a drop-in replacement for `awslocal`.

```bash
# Instead of: aws --endpoint-url=http://localhost:4566 s3 mb s3://my-bucket
nimbuslocal s3 mb s3://my-bucket

nimbuslocal s3 ls
nimbuslocal s3 cp ./file.txt s3://my-bucket/file.txt
nimbuslocal sqs create-queue --queue-name my-queue
nimbuslocal dynamodb list-tables
nimbuslocal secretsmanager create-secret --name /myapp/db-password --secret-string "secret"
nimbuslocal ssm put-parameter --name /myapp/db-host --value localhost --type String
nimbuslocal ses verify-email-identity --email-address sender@example.com
nimbuslocal lambda create-function --function-name my-func --runtime nodejs22.x --role arn:aws:iam::000000000000:role/r --handler index.handler --zip-file fileb://fn.zip
nimbuslocal lambda invoke --function-name my-func --payload '{}' response.json
```

Install:
```bash
go install github.com/nimbus-local/nimbus/cmd/nimbuslocal@latest
```

---

## SES — Inspecting Captured Emails

Emails are never actually sent. Instead they are captured in memory and available via a Nimbus-specific HTTP endpoint — useful for asserting email behavior in integration tests.

**List captured emails:**
```bash
curl http://localhost:4566/_nimbus/ses/messages
```

**Clear captured emails between tests:**
```bash
curl -X DELETE http://localhost:4566/_nimbus/ses/messages
```

**Example response:**
```json
[
  {
    "MessageId": "abc123@nimbus.local",
    "From": "no-reply@myapp.com",
    "To": ["user@example.com"],
    "Subject": "Welcome to MyApp",
    "Body": {
      "Text": "Welcome!",
      "HTML": "<p>Welcome!</p>"
    },
    "SentAt": "2026-04-03T21:00:00Z"
  }
]
```

---

## Lambda

Nimbus emulates the Lambda REST API (`/2015-03-31/`) in-memory. Functions are stored and invoked locally — no Docker-per-function, no execution sandbox. Invocations return a configurable response body and are useful for asserting invocation behaviour in integration tests.

### Supported operations

**Functions**
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

**Invocations**
| Method | Path | Operation |
|--------|------|-----------|
| POST | `/2015-03-31/functions/{name}/invocations` | Invoke |
| POST | `/2015-03-31/functions/{name}/invoke-async` | InvokeAsync |
| POST | `/2015-03-31/functions/{name}/response-streaming-invocations` | InvokeWithResponseStream |

**Aliases**
| Method | Path | Operation |
|--------|------|-----------|
| POST | `/2015-03-31/functions/{name}/aliases` | CreateAlias |
| GET | `/2015-03-31/functions/{name}/aliases` | ListAliases |
| GET | `/2015-03-31/functions/{name}/aliases/{alias}` | GetAlias |
| PUT | `/2015-03-31/functions/{name}/aliases/{alias}` | UpdateAlias |
| DELETE | `/2015-03-31/functions/{name}/aliases/{alias}` | DeleteAlias |

**Permissions (resource-based policy)**
| Method | Path | Operation |
|--------|------|-----------|
| POST | `/2015-03-31/functions/{name}/policy` | AddPermission |
| GET | `/2015-03-31/functions/{name}/policy` | GetPolicy |
| DELETE | `/2015-03-31/functions/{name}/policy/{statementId}` | RemovePermission |

**Event Source Mappings**
| Method | Path | Operation |
|--------|------|-----------|
| POST | `/2015-03-31/event-source-mappings` | CreateEventSourceMapping |
| GET | `/2015-03-31/event-source-mappings` | ListEventSourceMappings |
| GET | `/2015-03-31/event-source-mappings/{uuid}` | GetEventSourceMapping |
| PUT | `/2015-03-31/event-source-mappings/{uuid}` | UpdateEventSourceMapping |
| DELETE | `/2015-03-31/event-source-mappings/{uuid}` | DeleteEventSourceMapping |

**Concurrency**
| Method | Path | Operation |
|--------|------|-----------|
| PUT | `/2015-03-31/functions/{name}/concurrency` | PutFunctionConcurrency |
| GET | `/2015-03-31/functions/{name}/concurrency` | GetFunctionConcurrency |
| DELETE | `/2015-03-31/functions/{name}/concurrency` | DeleteFunctionConcurrency |
| PUT | `/2015-03-31/functions/{name}/provisioned-concurrency` | PutProvisionedConcurrencyConfig |
| GET | `/2015-03-31/functions/{name}/provisioned-concurrency` | GetProvisionedConcurrencyConfig / ListProvisionedConcurrencyConfigs |
| DELETE | `/2015-03-31/functions/{name}/provisioned-concurrency` | DeleteProvisionedConcurrencyConfig |

**Layers**
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

**Code Signing**
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

**Function URLs**
| Method | Path | Operation |
|--------|------|-----------|
| POST | `/2015-03-31/functions/{name}/url` | CreateFunctionUrlConfig |
| GET | `/2015-03-31/functions/{name}/url` | GetFunctionUrlConfig |
| PUT | `/2015-03-31/functions/{name}/url` | UpdateFunctionUrlConfig |
| DELETE | `/2015-03-31/functions/{name}/url` | DeleteFunctionUrlConfig |
| GET | `/2015-03-31/functions/{name}/urls` | ListFunctionUrlConfigs |

**Event Invoke Config**
| Method | Path | Operation |
|--------|------|-----------|
| PUT | `/2015-03-31/functions/{name}/event-invoke-config` | PutFunctionEventInvokeConfig |
| GET | `/2015-03-31/functions/{name}/event-invoke-config` | GetFunctionEventInvokeConfig |
| POST | `/2015-03-31/functions/{name}/event-invoke-config` | UpdateFunctionEventInvokeConfig |
| DELETE | `/2015-03-31/functions/{name}/event-invoke-config` | DeleteFunctionEventInvokeConfig |
| GET | `/2015-03-31/event-invoke-config/functions` | ListFunctionEventInvokeConfigs |

**Runtime & Recursion Settings**
| Method | Path | Operation |
|--------|------|-----------|
| GET | `/2015-03-31/functions/{name}/runtime-management-config` | GetRuntimeManagementConfig |
| PUT | `/2015-03-31/functions/{name}/runtime-management-config` | PutRuntimeManagementConfig |
| GET | `/2015-03-31/functions/{name}/recursion-config` | GetFunctionRecursionConfig |
| PUT | `/2015-03-31/functions/{name}/recursion-config` | PutFunctionRecursionConfig |
| GET | `/2015-03-31/account-settings` | GetAccountSettings |

**Tags**
| Method | Path | Operation |
|--------|------|-----------|
| GET | `/2015-03-31/tags/{arn}` | ListTags |
| POST | `/2015-03-31/tags/{arn}` | TagResource |
| DELETE | `/2015-03-31/tags/{arn}` | UntagResource |

### Example usage

```bash
# Create a function
nimbuslocal lambda create-function \
  --function-name my-func \
  --runtime nodejs22.x \
  --role arn:aws:iam::000000000000:role/lambda-role \
  --handler index.handler \
  --zip-file fileb://function.zip

# Invoke it
nimbuslocal lambda invoke \
  --function-name my-func \
  --payload '{"key":"value"}' \
  response.json

# Create an alias
nimbuslocal lambda create-alias \
  --function-name my-func \
  --name live \
  --function-version 1

# Add a trigger (event source mapping)
nimbuslocal lambda create-event-source-mapping \
  --function-name my-func \
  --event-source-arn arn:aws:sqs:us-east-1:000000000000:my-queue \
  --batch-size 10

# Put reserved concurrency
nimbuslocal lambda put-function-concurrency \
  --function-name my-func \
  --reserved-concurrent-executions 5

# Create a layer
nimbuslocal lambda publish-layer-version \
  --layer-name my-layer \
  --zip-file fileb://layer.zip \
  --compatible-runtimes nodejs22.x python3.13
```

---

## Configuration

All configuration is via environment variables:

| Variable                   | Default                      | Description |
|----------------------------|------------------------------|-------------|
| `NIMBUS_PORT`              | `4566`                       | Edge port |
| `NIMBUS_DATA_DIR`          | `/var/lib/nimbus` (Docker)   | Storage root for S3 objects |
| `AWS_DEFAULT_REGION`       | `us-east-1`                  | Default region |
| `NIMBUS_DYNAMODB_ENDPOINT` | `http://dynamodb-local:8000` | DynamoDB Local sidecar URL |
| `NIMBUS_LOG_LEVEL`         | `info`                       | `debug`, `info`, `warn`, `error` |
| `SERVICES`                 | *(all)*                      | Comma-separated list to enable |
| `NIMBUS_ENDPOINT_URL`      | `http://localhost:4566`      | Used by `nimbuslocal` CLI |

---

## Health Check

```
GET /_nimbus/health
GET /_localstack/health   (alias for LocalStack compatibility)
```

```json
{"status":"running","services":["dynamodb","ses","secretsmanager","ssm","sqs","s3"]}
```

---

## Migrating from LocalStack

1. Replace `localstack/localstack` with `ghcr.io/nimbus-local/nimbus` in your `docker-compose.yml`
2. Add the `dynamodb-local` sidecar if you use DynamoDB
3. Change `S3_ENDPOINT_URL` (or equivalent) from `http://localstack:4566` to `http://nimbus:4566`
4. Replace `awslocal` with `nimbuslocal` in scripts
5. That's it. The port, credential handling, and API responses are compatible.

---

## Architecture

Nimbus is a single Go binary. All AWS service traffic enters on port `4566`. The edge router inspects each request — via `X-Amz-Target` header, `Action` query param, or URL path — and dispatches to the appropriate service handler. S3 is the catch-all and is always registered last.

Each service is a self-contained package implementing a simple `Service` interface. Adding a new service means implementing the interface and registering it in `cmd/nimbus/main.go` — nothing else changes.

```
internal/
  router/               # Edge router — detects and dispatches
  services/
    s3/                 # S3 implementation (filesystem-backed)
    sqs/                # SQS implementation (in-memory)
    dynamodb/           # DynamoDB proxy to DynamoDB Local
    secretsmanager/     # Secrets Manager (in-memory)
    ssm/                # SSM Parameter Store (in-memory)
    ses/                # SES — captures emails in memory, never sends
    lambda/             # Lambda REST API — all 11 operation groups
      function_crud/    # CreateFunction, GetFunction, UpdateFunction, DeleteFunction, versions
      invocation/       # Invoke, InvokeAsync, InvokeWithResponseStream
      permissions/      # AddPermission, GetPolicy, RemovePermission, layer policies
      aliases/          # CreateAlias, GetAlias, UpdateAlias, DeleteAlias, ListAliases
      event_sources/    # CreateEventSourceMapping, List, Get, Update, Delete
      concurrency/      # Reserved and provisioned concurrency
      layers/           # PublishLayerVersion, ListLayers, GetLayerVersion, DeleteLayerVersion
      code_signing/     # CreateCodeSigningConfig, function bindings
      url_config/       # Function URLs, event invoke config
      settings/         # Runtime management config, recursion config, account settings
      capacity/         # Tags (ListTags, TagResource, UntagResource)
  auth/                 # Credential extraction (accepts anything)
  config/               # Environment-based configuration
  uid/                  # UUID generation (stdlib only)
cmd/
  nimbus/               # Server entrypoint
  nimbuslocal/          # AWS CLI wrapper
```

---

## Contributing

PRs welcome. If you're adding a new AWS service, implement the `services.Service` interface in `internal/services/<n>/` and register it in `cmd/nimbus/main.go`.

Please keep the spirit of the project: no accounts, no tokens, no telemetry, no commercial restrictions. MIT licensed contributions only.

See [CONTRIBUTING.md](.github/CONTRIBUTING.md) for details.

---

## License

MIT — see [LICENSE](LICENSE).

This project is not affiliated with Amazon Web Services or LocalStack.
