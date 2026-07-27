# AppSync

In-memory AppSync emulator covering both the management plane (CRUD for APIs, schemas, data sources, resolvers, API keys) and the GraphQL execution plane (running queries and mutations against Lambda-backed resolvers). All state is held in memory and resets on restart or via `/_nimbus/reset`.

**Detection:**
- Management plane: `/v1/apis` or `/v1/tags/` path prefix.
- Execution plane: path `/_appsync/{apiId}/graphql` (path-based, no DNS required) or `Host: {apiId}.appsync-api.{region}.nimbus.local` with path `/graphql` (virtual-host, matches real AppSync URL pattern).

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

## API attributes and re-apply stability

Reads report four attributes the Terraform provider expects, defaulting to the AWS
defaults when the create request omits them:

| Attribute | Default | Notes |
|-----------|---------|-------|
| `apiType` | `GRAPHQL` | ForceNew for the provider |
| `visibility` | `GLOBAL` | ForceNew for the provider |
| `introspectionConfig` | `ENABLED` | |
| `xrayEnabled` | `false` | Recorded only; Nimbus traces nothing |

Nimbus serves one flavour of API regardless of `apiType` and `visibility`, but leaving
either out of the read made every `terraform apply` plan an **API replacement**:

```
+ api_type   = "GRAPHQL" # forces replacement
+ visibility = "GLOBAL"  # forces replacement
```

## GraphQL execution

When a resolver is configured with an `AWS_LAMBDA` data source, Nimbus:

1. Validates the `x-api-key` header against stored API keys for `API_KEY`-auth APIs.
2. Parses the incoming GraphQL query/mutation to extract the operation type, field name, and arguments.
3. Evaluates the request mapping template — supports `$util.toJson($context.arguments)`, `$context.arguments`, `$context.info.fieldName`, and `$context.info.parentTypeName`.
4. Invokes the Lambda function (via mock response or live registered endpoint).
5. Evaluates the response mapping template — supports `$util.toJson($context.result)` and `$context.result`.
6. Returns `{"data": {"<fieldName>": <result>}}`.

The path-based endpoint `/_appsync/{apiId}/graphql` avoids the DNS setup that `*.nimbus.local` requires from a Mac host.

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

# Create a resolver
nimbuslocal appsync create-resolver \
  --api-id <apiId> \
  --type-name Mutation \
  --field-name createNote \
  --data-source-name myLambdaSource \
  --request-mapping-template '{"version":"2017-02-28","operation":"Invoke","payload":{"field":"createNote","args":$util.toJson($context.arguments)}}' \
  --response-mapping-template '$util.toJson($context.result)'

# Create an API key
nimbuslocal appsync create-api-key --api-id <apiId>

# Run a GraphQL mutation (path-based, no DNS needed)
curl -X POST http://localhost:4566/_appsync/<apiId>/graphql \
  -H "Content-Type: application/json" \
  -H "x-api-key: <keyId>" \
  -d '{"query":"mutation { createNote(id: \"1\", content: \"hi\") { id content } }"}'
```

## Inspection endpoint

```bash
# List all GraphQL APIs
curl http://localhost:4566/_nimbus/appsync/apis
```
