# AppSync

In-memory AppSync GraphQL API management plane emulator. All resources (APIs, schemas, data sources, resolvers, API keys) are stored in memory and reset on restart or via `/_nimbus/reset`. No GraphQL queries are executed — this is a control-plane stub for Pulumi/Terraform lifecycle testing.

Detection: `/v1/apis` path prefix (REST JSON protocol, no `X-Amz-Target` header).

## Supported operations

| Operation | Path |
|-----------|------|
| `CreateGraphqlApi` | `POST /v1/apis` |
| `GetGraphqlApi` | `GET /v1/apis/{apiId}` |
| `UpdateGraphqlApi` | `PUT /v1/apis/{apiId}` |
| `DeleteGraphqlApi` | `DELETE /v1/apis/{apiId}` |
| `StartSchemaCreation` | `POST /v1/apis/{apiId}/schemacreation` — returns `SUCCESS` immediately |
| `GetSchemaCreationStatus` | `GET /v1/apis/{apiId}/schemacreation` — always `SUCCESS` |
| `CreateDataSource` | `POST /v1/apis/{apiId}/datasources` |
| `GetDataSource` | `GET /v1/apis/{apiId}/datasources/{name}` |
| `UpdateDataSource` | `PUT /v1/apis/{apiId}/datasources/{name}` |
| `DeleteDataSource` | `DELETE /v1/apis/{apiId}/datasources/{name}` |
| `ListDataSources` | `GET /v1/apis/{apiId}/datasources` |
| `CreateResolver` | `POST /v1/apis/{apiId}/types/{typeName}/resolvers` |
| `GetResolver` | `GET /v1/apis/{apiId}/types/{typeName}/resolvers/{fieldName}` |
| `UpdateResolver` | `PUT /v1/apis/{apiId}/types/{typeName}/resolvers/{fieldName}` |
| `DeleteResolver` | `DELETE /v1/apis/{apiId}/types/{typeName}/resolvers/{fieldName}` |
| `CreateApiKey` | `POST /v1/apis/{apiId}/ApiKeys` |
| `ListApiKeys` | `GET /v1/apis/{apiId}/ApiKeys` |
| `UpdateApiKey` | `PUT /v1/apis/{apiId}/ApiKeys/{id}` |
| `DeleteApiKey` | `DELETE /v1/apis/{apiId}/ApiKeys/{id}` |
| `TagResource` | `POST /v1/tags/{resourceArn}` |
| `ListTagsForResource` | `GET /v1/tags/{resourceArn}` |
| `UntagResource` | `DELETE /v1/tags/{resourceArn}?tagKeys=...` |

## Example

```bash
# Create a GraphQL API
nimbuslocal appsync create-graphql-api \
  --name MyAPI \
  --authentication-type API_KEY

# Upload schema (base64-encoded SDL)
nimbuslocal appsync start-schema-creation \
  --api-id <apiId> \
  --definition fileb://schema.graphql

# Create a Lambda data source
nimbuslocal appsync create-data-source \
  --api-id <apiId> \
  --name myLambdaSource \
  --type AWS_LAMBDA \
  --lambda-config lambdaFunctionArn=arn:aws:lambda:us-east-1:000000000000:function:myFn \
  --service-role-arn arn:aws:iam::000000000000:role/AppSyncRole

# Create an API key
nimbuslocal appsync create-api-key --api-id <apiId>
```

## Inspection endpoint

```bash
# List all GraphQL APIs
curl http://localhost:4566/_nimbus/appsync/apis
```
