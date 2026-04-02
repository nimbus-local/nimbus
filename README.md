# Nimbus ☁️

**A free, open-source AWS emulator for local development. Forever.**

Nimbus runs S3, SQS, and DynamoDB locally in a single Docker container on port `4566` — a drop-in replacement for LocalStack Community Edition. No account. No auth token. No commercial restrictions. MIT licensed.

---

## Why Nimbus?

LocalStack built something genuinely useful on the backs of open-source contributors, then locked it behind a paywall. Nimbus exists because local AWS emulation should be free for everyone — individual developers, startups, enterprises, and open-source projects alike.

> *"Free for everyone, forever."*

---

## Quickstart

```bash
docker run -p 4566:4566 nimbus-local/nimbus:latest
```

Or with Docker Compose:

```yaml
services:
  nimbus:
    image: nimbus-local/nimbus:latest
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

| Service         | Status  | Notes |
|-----------------|---------|-------|
| S3              | ✅ Core | PutObject, GetObject, DeleteObject, ListObjectsV2, HeadObject, CreateBucket, DeleteBucket, multipart uploads, presigned URLs |
| SQS             | ✅ Core | CreateQueue, SendMessage, ReceiveMessage, DeleteMessage, PurgeQueue, visibility timeout, DLQ config |
| DynamoDB        | ✅ Full | Proxied to [DynamoDB Local](https://hub.docker.com/r/amazon/dynamodb-local) (official AWS image) — full parity |
| Secrets Manager | ✅ Core | CreateSecret, GetSecretValue, PutSecretValue, UpdateSecret, DeleteSecret, RestoreSecret, ListSecrets, DescribeSecret |
| SNS             | 🚧 In Progress | |

More services coming. Contributions welcome.

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
nimbuslocal sqs send-message --queue-url http://sqs.us-east-1.localhost:4566/000000000000/my-queue --message-body "hello"
nimbuslocal dynamodb list-tables
```

Install:
```bash
go install github.com/nimbus-local/nimbus/cmd/nimbuslocal@latest
```

---

## Configuration

All configuration is via environment variables:

| Variable                  | Default                        | Description |
|---------------------------|--------------------------------|-------------|
| `NIMBUS_PORT`             | `4566`                         | Edge port |
| `NIMBUS_DATA_DIR`         | `/var/lib/nimbus` (Docker)     | Storage root for S3 objects |
| `AWS_DEFAULT_REGION`      | `us-east-1`                    | Default region |
| `NIMBUS_DYNAMODB_ENDPOINT`| `http://dynamodb-local:8000`   | DynamoDB Local sidecar URL |
| `NIMBUS_LOG_LEVEL`        | `info`                         | `debug`, `info`, `warn`, `error` |
| `SERVICES`                | _(all)_                        | Comma-separated list to enable |
| `NIMBUS_ENDPOINT_URL`     | `http://localhost:4566`        | Used by `nimbuslocal` CLI |

---

## Migrating from LocalStack

1. Replace `localstack/localstack` with `nimbus-local/nimbus` in your `docker-compose.yml`
2. Add the `dynamodb-local` sidecar if you use DynamoDB
3. Change `S3_ENDPOINT_URL` (or equivalent) from `http://localstack:4566` to `http://nimbus:4566`
4. Replace `awslocal` with `nimbuslocal` in scripts
5. That's it. The port, credential handling, and API responses are compatible.

---

## Health Check

```
GET /_nimbus/health
GET /_localstack/health   (alias for LocalStack compatibility)
```

```json
{"status":"running","services":["dynamodb","sqs","s3"]}
```

---

## Architecture

Nimbus is a single Go binary. All AWS service traffic enters on port `4566`. The edge router inspects each request and dispatches to the appropriate service handler:

- **DynamoDB** — detected by `X-Amz-Target: DynamoDB_*` header, proxied to DynamoDB Local
- **Secrets Manager** — detected by `X-Amz-Target: secretsmanager.*` header
- **SQS** — detected by `Action` param or `X-Amz-Target: AmazonSQS.*`
- **SNS** — detected by `X-Amz-Target: SNS.*` header *(in progress)*
- **S3** — catch-all; handles path-style and virtual-hosted-style URLs

Each service is a self-contained package implementing a simple `Service` interface. Adding a new service means implementing the interface and registering it — nothing else changes.
```
internal/
  router/               # Edge router — detects and dispatches
  services/
    s3/                 # S3 implementation (filesystem-backed)
    sqs/                # SQS implementation (in-memory)
    dynamodb/           # DynamoDB proxy to DynamoDB Local
    secretsmanager/     # Secrets Manager implementation (in-memory)
  auth/                 # Credential extraction (accepts anything)
  config/               # Environment-based configuration
  uid/                  # UUID generation (stdlib only, no external deps)
cmd/
  nimbus/               # Server entrypoint
  nimbuslocal/          # AWS CLI wrapper
```

---

## Contributing

PRs welcome. If you're adding a new AWS service, implement the `services.Service` interface in `internal/services/<name>/` and register it in `cmd/nimbus/main.go`.

Please keep the spirit of the project: no accounts, no tokens, no telemetry, no commercial restrictions. MIT licensed contributions only.

---

## License

MIT — see [LICENSE](LICENSE).

This project is not affiliated with Amazon Web Services or LocalStack.
