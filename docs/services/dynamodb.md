# DynamoDB

Proxied to [DynamoDB Local](https://hub.docker.com/r/amazon/dynamodb-local) — full API parity.

## Setup

DynamoDB Local runs as a sidecar container. Add it to your `docker-compose.yml`:

```yaml
services:
  nimbus:
    image: ghcr.io/nimbus-local/nimbus:latest
    ports:
      - "4566:4566"
    environment:
      NIMBUS_DYNAMODB_ENDPOINT: http://dynamodb-local:8000

  dynamodb-local:
    image: amazon/dynamodb-local:latest
    command: "-jar DynamoDBLocal.jar -sharedDb -dbPath /data"
    volumes:
      - dynamodb_data:/data

volumes:
  dynamodb_data:
```

Detection: `X-Amz-Target: DynamoDB_*`.

## Example

```bash
nimbuslocal dynamodb create-table \
  --table-name Users \
  --attribute-definitions AttributeName=id,AttributeType=S \
  --key-schema AttributeName=id,KeyType=HASH \
  --billing-mode PAY_PER_REQUEST

nimbuslocal dynamodb put-item \
  --table-name Users \
  --item '{"id":{"S":"1"},"name":{"S":"Alice"}}'

nimbuslocal dynamodb get-item \
  --table-name Users \
  --key '{"id":{"S":"1"}}'
```
