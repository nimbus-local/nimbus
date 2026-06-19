# API Gateway

In-memory API Gateway emulator. Supports **REST API (v1)**, **HTTP API (v2)**, and **WebSocket API (v2)** — management plane and execute-api runtime.

Detection:
- REST API (v1): `/restapis` path prefix
- HTTP API (v2) / WebSocket API: `/apis` path prefix (same endpoint, `protocolType` field differentiates)

## Execute URL format

```
# REST API (v1)
http://localhost:4566/restapis/{apiId}/{stage}/_user_request_/{resource-path}

# HTTP API (v2)
http://localhost:4566/apis/{apiId}/{stage}/_user_request_/{resource-path}
```

Both use the same LocalStack-compatible `_user_request_` format.

---

## REST API (v1)

The original API Gateway model: APIs → Resources (path tree) → Methods → Integrations → Stages.

### Supported operations

#### REST APIs

| Method | Path | Operation |
|--------|------|-----------|
| POST | `/restapis` | CreateRestApi |
| GET | `/restapis` | GetRestApis |
| GET | `/restapis/{id}` | GetRestApi |
| DELETE | `/restapis/{id}` | DeleteRestApi |
| PATCH | `/restapis/{id}` | UpdateRestApi |

#### Resources

| Method | Path | Operation |
|--------|------|-----------|
| GET | `/restapis/{id}/resources` | GetResources |
| POST | `/restapis/{id}/resources/{parentId}` | CreateResource |
| GET | `/restapis/{id}/resources/{resourceId}` | GetResource |
| DELETE | `/restapis/{id}/resources/{resourceId}` | DeleteResource |

#### Methods & Integrations

| Method | Path | Operation |
|--------|------|-----------|
| PUT | `.../resources/{resourceId}/methods/{httpMethod}` | PutMethod |
| GET | `.../resources/{resourceId}/methods/{httpMethod}` | GetMethod |
| DELETE | `.../resources/{resourceId}/methods/{httpMethod}` | DeleteMethod |
| PUT | `.../methods/{httpMethod}/integration` | PutIntegration |
| GET | `.../methods/{httpMethod}/integration` | GetIntegration |
| DELETE | `.../methods/{httpMethod}/integration` | DeleteIntegration |
| PUT | `.../methods/{httpMethod}/responses/{statusCode}` | PutMethodResponse |
| GET | `.../methods/{httpMethod}/responses/{statusCode}` | GetMethodResponse |
| DELETE | `.../methods/{httpMethod}/responses/{statusCode}` | DeleteMethodResponse |
| PUT | `.../methods/{httpMethod}/integration/responses/{statusCode}` | PutIntegrationResponse |
| GET | `.../methods/{httpMethod}/integration/responses/{statusCode}` | GetIntegrationResponse |
| DELETE | `.../methods/{httpMethod}/integration/responses/{statusCode}` | DeleteIntegrationResponse |

#### Deployments & Stages

| Method | Path | Operation |
|--------|------|-----------|
| POST | `/restapis/{id}/deployments` | CreateDeployment |
| GET | `/restapis/{id}/deployments` | GetDeployments |
| GET | `/restapis/{id}/deployments/{deploymentId}` | GetDeployment |
| DELETE | `/restapis/{id}/deployments/{deploymentId}` | DeleteDeployment |
| POST | `/restapis/{id}/stages` | CreateStage |
| GET | `/restapis/{id}/stages` | GetStages |
| GET | `/restapis/{id}/stages/{stageName}` | GetStage |
| DELETE | `/restapis/{id}/stages/{stageName}` | DeleteStage |
| PATCH | `/restapis/{id}/stages/{stageName}` | UpdateStage |

#### Integration types

| Type | Behaviour |
|------|-----------|
| `AWS_PROXY` | Forwards to Lambda with v1 proxy event. Supports `{param}` and `{proxy+}` path parameters. |
| `MOCK` | Returns the configured `IntegrationResponse` directly. |

### Example (v1)

```bash
API=$(nimbuslocal apigateway create-rest-api --name my-api --query 'id' --output text)
ROOT=$(nimbuslocal apigateway get-resources --rest-api-id $API --query 'items[0].id' --output text)

RES=$(nimbuslocal apigateway create-resource \
  --rest-api-id $API --parent-id $ROOT --path-part hello --query 'id' --output text)

nimbuslocal apigateway put-method \
  --rest-api-id $API --resource-id $RES --http-method GET --authorization-type NONE

nimbuslocal apigateway put-integration \
  --rest-api-id $API --resource-id $RES --http-method GET \
  --type AWS_PROXY --integration-http-method POST \
  --uri "arn:aws:apigateway:us-east-1:lambda:path/2015-03-31/functions/arn:aws:lambda:us-east-1:000000000000:function:my-func/invocations"

nimbuslocal apigateway create-deployment --rest-api-id $API --stage-name dev

curl http://localhost:4566/restapis/$API/dev/_user_request_/hello
```

---

## HTTP API (v2)

The newer, simpler model: APIs → Routes (`METHOD /path`) → Integrations → Stages. No resources or method responses.

### Supported operations

#### HTTP APIs

| Method | Path | Operation |
|--------|------|-----------|
| POST | `/apis` | CreateApi |
| GET | `/apis` | GetApis |
| GET | `/apis/{id}` | GetApi |
| DELETE | `/apis/{id}` | DeleteApi |
| PATCH | `/apis/{id}` | UpdateApi |

#### Routes

| Method | Path | Operation |
|--------|------|-----------|
| POST | `/apis/{id}/routes` | CreateRoute |
| GET | `/apis/{id}/routes` | GetRoutes |
| GET | `/apis/{id}/routes/{routeId}` | GetRoute |
| DELETE | `/apis/{id}/routes/{routeId}` | DeleteRoute |
| PATCH | `/apis/{id}/routes/{routeId}` | UpdateRoute |

Route keys use the format `{METHOD} {path}` (e.g. `GET /users/{userId}`) or `$default` as a catch-all.

#### Integrations

| Method | Path | Operation |
|--------|------|-----------|
| POST | `/apis/{id}/integrations` | CreateIntegration |
| GET | `/apis/{id}/integrations` | GetIntegrations |
| GET | `/apis/{id}/integrations/{integrationId}` | GetIntegration |
| DELETE | `/apis/{id}/integrations/{integrationId}` | DeleteIntegration |
| PATCH | `/apis/{id}/integrations/{integrationId}` | UpdateIntegration |

#### Stages & Deployments

| Method | Path | Operation |
|--------|------|-----------|
| POST | `/apis/{id}/stages` | CreateStage |
| GET | `/apis/{id}/stages` | GetStages |
| GET | `/apis/{id}/stages/{stageName}` | GetStage |
| DELETE | `/apis/{id}/stages/{stageName}` | DeleteStage |
| PATCH | `/apis/{id}/stages/{stageName}` | UpdateStage |
| POST | `/apis/{id}/deployments` | CreateDeployment |
| GET | `/apis/{id}/deployments` | GetDeployments |
| GET | `/apis/{id}/deployments/{deploymentId}` | GetDeployment |
| DELETE | `/apis/{id}/deployments/{deploymentId}` | DeleteDeployment |

#### Integration types & payload format

| `integrationType` | `payloadFormatVersion` | Behaviour |
|-------------------|------------------------|-----------|
| `AWS_PROXY` | `2.0` (default) | Lambda receives v2 event: `version`, `routeKey`, `rawPath`, `requestContext.http.*` |
| `AWS_PROXY` | `1.0` | Lambda receives v1 event: `httpMethod`, `resource`, `requestContext` (same shape as REST API) |

### Route matching

Routes are matched in this order:
1. Exact method + path match (`GET /users`)
2. Method + path pattern match (`GET /users/{userId}`)
3. `$default` catch-all

`ANY` as the method matches any HTTP verb.

### Example (v2)

```bash
API=$(nimbuslocal apigatewayv2 create-api --name my-http-api --protocol-type HTTP --query 'ApiId' --output text)

INTEG=$(nimbuslocal apigatewayv2 create-integration \
  --api-id $API \
  --integration-type AWS_PROXY \
  --integration-uri "arn:aws:apigateway:us-east-1:lambda:path/2015-03-31/functions/arn:aws:lambda:us-east-1:000000000000:function:my-func/invocations" \
  --payload-format-version 2.0 \
  --query 'IntegrationId' --output text)

nimbuslocal apigatewayv2 create-route \
  --api-id $API \
  --route-key "GET /hello" \
  --target "integrations/$INTEG"

nimbuslocal apigatewayv2 create-stage \
  --api-id $API --stage-name dev --auto-deploy

curl http://localhost:4566/apis/$API/dev/_user_request_/hello
```

---

## WebSocket API (v2)

Same control plane as HTTP API (v2) — routes, integrations, stages, and deployments are identical. `protocolType: WEBSOCKET` is stored and returned correctly so Terraform drift detection passes.

**Data plane** — WebSocket upgrade, Lambda dispatch, and the `@connections` management API are fully implemented.

### Connection lifecycle

| Event | Route key | Lambda event `requestContext.eventType` |
|-------|-----------|----------------------------------------|
| Client connects | `$connect` | `CONNECT` |
| Client sends a frame | matched route or `$default` | `MESSAGE` |
| Client disconnects | `$disconnect` | `DISCONNECT` |

Route selection for `MESSAGE` events: the `routeSelectionExpression` (e.g. `$request.body.action`) is evaluated against the JSON body. If a route with that key exists it is used; otherwise `$default` is used.

`$connect` Lambda response: if the function returns a non-2xx `statusCode` the connection is rejected with close code 1008.

### Lambda event shape

```json
{
  "requestContext": {
    "connectionId": "abc123",
    "eventType": "CONNECT | MESSAGE | DISCONNECT",
    "routeKey": "$connect | $default | ...",
    "apiId": "...",
    "stage": "prod",
    "domainName": "localhost:4566",
    "connectedAt": 1700000000000,
    "requestTimeEpoch": 1700000000100,
    "requestId": "...",
    "extendedRequestId": "...",
    "messageId": "...",          // MESSAGE only
    "disconnectStatusCode": 1000 // DISCONNECT only
  },
  "headers": { ... },   // CONNECT only
  "body": "...",        // MESSAGE only
  "isBase64Encoded": false
}
```

### Management API (`@connections`)

Lambda functions push data back to clients or close connections via:

| Method | Path | Action |
|--------|------|--------|
| `POST` | `/{stage}/@connections/{connectionId}` | Send data to client |
| `DELETE` | `/{stage}/@connections/{connectionId}` | Disconnect client |
| `GET` | `/{stage}/@connections/{connectionId}` | Get connection metadata |

Configure the management client endpoint as `http://localhost:4566/{stage}`:

```python
# Python Lambda handler example
import boto3, json, os

def handler(event, context):
    rc = event['requestContext']
    if rc['eventType'] == 'CONNECT':
        return {'statusCode': 200}
    if rc['eventType'] == 'MESSAGE':
        client = boto3.client(
            'apigatewaymanagementapi',
            endpoint_url=f"http://{rc['domainName']}/{rc['stage']}",
        )
        client.post_to_connection(
            ConnectionId=rc['connectionId'],
            Data=json.dumps({'echo': event.get('body')}),
        )
    return {'statusCode': 200}
```

### Execute URL

```
ws://localhost:4566/apis/{apiId}/{stage}/_user_request_/
```

### Example

```bash
WS=$(nimbuslocal apigatewayv2 create-api \
  --name my-ws-api \
  --protocol-type WEBSOCKET \
  --route-selection-expression '$request.body.action' \
  --query 'ApiId' --output text)

INTEG=$(nimbuslocal apigatewayv2 create-integration \
  --api-id $WS --integration-type AWS_PROXY \
  --integration-uri "arn:aws:apigateway:us-east-1:lambda:path/2015-03-31/functions/arn:aws:lambda:us-east-1:000000000000:function:my-func/invocations" \
  --query 'IntegrationId' --output text)

nimbuslocal apigatewayv2 create-route --api-id $WS --route-key '$connect'    --target "integrations/$INTEG"
nimbuslocal apigatewayv2 create-route --api-id $WS --route-key '$disconnect' --target "integrations/$INTEG"
nimbuslocal apigatewayv2 create-route --api-id $WS --route-key '$default'    --target "integrations/$INTEG"

nimbuslocal apigatewayv2 create-stage --api-id $WS --stage-name prod --auto-deploy

# Connect with any WebSocket client:
# wscat -c ws://localhost:4566/apis/$WS/prod/_user_request_/
```
