<p align="center">
  <img src="docs/assets/logo.png" alt="Nimbus" width="300" />
</p>

# Nimbus

**A free, open-source AWS emulator for local development. Forever.**

Nimbus runs S3, SQS, DynamoDB, Secrets Manager, SSM Parameter Store, SES, Lambda, and API Gateway locally in a single Docker container on port `4566` — a drop-in replacement for LocalStack Community Edition. No account. No auth token. No commercial restrictions. MIT licensed.

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

| Service | Status | Detection | Docs |
|---------|--------|-----------|------|
| [S3](docs/services/s3.md) | ✅ Core | catch-all (path / virtual-hosted) | PutObject, GetObject, DeleteObject, ListObjectsV2, HeadObject, CreateBucket, DeleteBucket, multipart uploads, presigned URLs |
| [SQS](docs/services/sqs.md) | ✅ Core | `Action` param or `AmazonSQS.*` target | CreateQueue, SendMessage, ReceiveMessage, DeleteMessage, PurgeQueue, visibility timeout |
| [DynamoDB](docs/services/dynamodb.md) | ✅ Full | `DynamoDB_*` target | Proxied to [DynamoDB Local](https://hub.docker.com/r/amazon/dynamodb-local) — full parity |
| [Secrets Manager](docs/services/secretsmanager.md) | ✅ Core | `secretsmanager.*` target | CreateSecret, GetSecretValue, PutSecretValue, UpdateSecret, DeleteSecret, ListSecrets, DescribeSecret, RestoreSecret |
| [SSM Parameter Store](docs/services/ssm.md) | ✅ Core | `AmazonSSM.*` target | PutParameter, GetParameter, GetParameters, GetParametersByPath, DeleteParameter, DescribeParameters — String, StringList, SecureString, path hierarchy, versioning |
| [SES](docs/services/ses.md) | ✅ Core | `AmazonSimpleEmailService.*` target or `/v2/email/` path | SendEmail (v1+v2), SendRawEmail, VerifyEmailIdentity, ListIdentities — emails captured in memory, never sent |
| [Lambda](docs/services/lambda.md) | ✅ Core | `/2015-03-31/` path prefix | Functions (CRUD, versions, publish), invocations, aliases, permissions, event source mappings, concurrency, layers, code signing, function URLs, event invoke config, runtime & recursion settings, tags |
| [API Gateway](docs/services/apigateway.md) | ✅ Core | `/restapis` (REST v1), `/apis` (HTTP v2) | REST API: resources, methods, integrations (AWS\_PROXY + MOCK), stages. HTTP API: routes, integrations (AWS\_PROXY, payload format v1+v2), stages, `$default` catch-all — execute via `/{apiId}/{stage}/_user_request_/` |
| [ECR](docs/services/ecr.md) | ✅ Core | `AmazonEC2ContainerRegistry_V20150921.*` target or `/v2/` path | CreateRepository, GetAuthorizationToken, ListImages, BatchDeleteImage, BatchGetImage + full Docker V2 registry (push/pull blobs and manifests) |
| [ECS](docs/services/ecs.md) | ✅ Core | `AmazonEC2ContainerServiceV20141113.*` target | Clusters (CRUD), task definitions (register/deregister/describe/list), tasks (run/stop/describe/list), services (CRUD) — tasks simulated as immediately RUNNING |
| [IAM](docs/services/iam.md) | ✅ Core | form-encoded body, `Version=2010-05-08` | CreateRole/GetRole/DeleteRole/ListRoles, policy attachments, inline policies, managed policies, instance profiles; no enforcement — AssumeRole always succeeds |
| [CloudWatch Logs](docs/services/cloudwatchlogs.md) | ✅ Core | `Logs_20140328.*` target | CreateLogGroup/DeleteLogGroup/DescribeLogGroups, CreateLogStream/DescribeLogStreams, PutLogEvents, GetLogEvents/FilterLogEvents |
| [KMS](docs/services/kms.md) | ✅ Core | `TrentService.*` target | CreateKey, Encrypt/Decrypt (real AES-256-GCM), GenerateDataKey, ReEncrypt, aliases, tags, key lifecycle (enable/disable/schedule-deletion) |
| [SNS](docs/services/sns.md) | ✅ Core | `AmazonSimpleNotificationService.*` target or `Action` param | CreateTopic, Subscribe (all protocols, auto-confirmed), Publish, PublishBatch — messages captured in memory, never delivered |
| [EventBridge](docs/services/eventbridge.md) | ✅ Core | `AmazonEventBridge.*` target | PutEvents (captured in memory), event buses (CRUD), rules (CRUD, enable/disable), targets (put/remove/list) |
| [EventBridge Scheduler](docs/services/scheduler.md) | ✅ Core | `/2020-11-23/` path prefix | Schedule groups (CRUD), schedules (CRUD) — expressions stored; firing added in Part 2 |
| [CloudFront](docs/services/cloudfront.md) | ✅ Core | `/2020-05-31/` path prefix | Distribution CRUD — `localhost`-based `DomainName`, status always `Deployed`, ETag per distribution |

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

**AWS CLI v2:**

Set `AWS_ENDPOINT_URL` once and omit `--endpoint-url` from every command:
```bash
export AWS_ENDPOINT_URL=http://localhost:4566
export AWS_ACCESS_KEY_ID=test
export AWS_SECRET_ACCESS_KEY=test
export AWS_DEFAULT_REGION=us-east-1

aws s3 mb s3://my-bucket
aws sqs create-queue --queue-name my-queue
aws lambda invoke --function-name my-func --payload '{}' --cli-binary-format raw-in-base64-out response.json
```

These variables are exported automatically when using the `infra/` dev harness (`make start`).

---

## nimbuslocal CLI

`nimbuslocal` is a thin wrapper around the `aws` CLI that automatically injects the Nimbus endpoint. It's a drop-in replacement for `awslocal`.

```bash
nimbuslocal s3 mb s3://my-bucket
nimbuslocal sqs create-queue --queue-name my-queue
nimbuslocal dynamodb list-tables
nimbuslocal secretsmanager create-secret --name /myapp/db-password --secret-string "secret"
nimbuslocal ssm put-parameter --name /myapp/db-host --value localhost --type String
nimbuslocal ses verify-email-identity --email-address sender@example.com
nimbuslocal lambda invoke --function-name my-func --payload '{}' response.json
nimbuslocal apigateway create-rest-api --name my-api
```

Install:
```bash
curl -fsSL https://raw.githubusercontent.com/nimbus-local/nimbus/master/install.sh | sh
```

The script detects your OS and architecture, downloads the right binary to `~/.local/bin`, and adds it to your shell profile automatically.

Or install manually with Go:
```bash
go install github.com/nimbus-local/nimbus/cmd/nimbuslocal@latest
# ensure $GOPATH/bin is on your PATH
export PATH="$PATH:$HOME/go/bin"
```

To uninstall:
```bash
# If installed via the script:
rm ~/.local/bin/nimbuslocal
# Then remove the managed block from your shell profile (~/.zshrc etc.):
# ### MANAGED BY NIMBUSLOCAL START (DO NOT EDIT)
# ...
# ### MANAGED BY NIMBUSLOCAL END (DO NOT EDIT)

# If installed via go install:
rm ~/go/bin/nimbuslocal
```

---

## Configuration

All configuration is via environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `NIMBUS_PORT` | `4566` | Edge port |
| `NIMBUS_DATA_DIR` | `/var/lib/nimbus` (Docker) | Storage root for S3 objects |
| `AWS_DEFAULT_REGION` | `us-east-1` | Default region |
| `NIMBUS_DYNAMODB_ENDPOINT` | `http://dynamodb-local:8000` | DynamoDB Local sidecar URL |
| `NIMBUS_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `SERVICES` | *(all)* | Comma-separated list to enable |
| `NIMBUS_ENDPOINT_URL` | `http://localhost:4566` | Used by `nimbuslocal` CLI |

---

## Health Check

```
GET /_nimbus/health
GET /_localstack/health   (alias for LocalStack compatibility)
```

```json
{"status":"running","services":["dynamodb","lambda","apigateway","ses","secretsmanager","ssm","sqs","s3"]}
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
    sqs/                # SQS (in-memory)
    dynamodb/           # DynamoDB proxy to DynamoDB Local
    secretsmanager/     # Secrets Manager (in-memory)
    ssm/                # SSM Parameter Store (in-memory)
    ses/                # SES — captures emails, never sends
    lambda/             # Lambda REST API
    apigateway/         # API Gateway management + execute-api
  auth/                 # Credential extraction (accepts anything)
  config/               # Environment-based configuration
  uid/                  # UUID generation
cmd/
  nimbus/               # Server entrypoint
  nimbuslocal/          # AWS CLI wrapper
docs/
  services/             # Per-service API reference
```

---

## Local development

The `infra/` directory contains a full dev harness: Docker Compose, Terraform fixtures, and a smoke test script that exercises every service end-to-end.

**Prerequisites:** Docker, Terraform, AWS CLI v2.

```bash
cd infra
```

| Goal | Command |
|------|---------|
| Start Nimbus + DynamoDB Local | `make start` |
| Provision all test resources | `make apply` |
| Run smoke tests | `make smoke-test` |
| Provision + smoke test in one step | `make test` |
| Rebuild after Go changes | `make stop && make start && make apply` |
| Tear everything down | `make clean` |

`make apply` is idempotent — safe to re-run. All AWS credentials are set automatically to dummy values so no environment setup is required.

---

## Contributing

PRs welcome. If you're adding a new AWS service, implement the `services.Service` interface in `internal/services/<n>/` and register it in `cmd/nimbus/main.go`.

Please keep the spirit of the project: no accounts, no tokens, no telemetry, no commercial restrictions. MIT licensed contributions only.

See [CONTRIBUTING.md](.github/CONTRIBUTING.md) for details.

---

## License

MIT — see [LICENSE](LICENSE).

This project is not affiliated with Amazon Web Services or LocalStack.
