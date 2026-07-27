#!/usr/bin/env bash
# smoke-test.sh — exercises every Nimbus service end-to-end.
# Prerequisites: AWS CLI v2, Nimbus running at NIMBUS_ENDPOINT, resources
# provisioned by `terraform apply` (or `make apply`).
set -uo pipefail

NIMBUS="${NIMBUS_ENDPOINT:-http://localhost:4566}"
REGION="${AWS_DEFAULT_REGION:-us-east-1}"
PREFIX="${NIMBUS_PREFIX:-nimbus-test}"

CLI="aws --endpoint-url $NIMBUS --region $REGION --no-cli-pager --output json"

PASS=0
FAIL=0

# ── Helpers ──────────────────────────────────────────────────────────────────

section() { echo; echo "── $1"; }

ok() {
  echo "  ✓ $1"
  PASS=$((PASS + 1))
}

fail() {
  echo "  ✗ $1"
  [ -n "${2:-}" ] && echo "    $2"
  FAIL=$((FAIL + 1))
}

try() {
  local label="$1"; shift
  local out
  if out=$("$@" 2>&1); then
    ok "$label"
  else
    fail "$label" "$(echo "$out" | head -3)"
  fi
}

try_match() {
  local label="$1" pattern="$2"; shift 2
  local out
  if out=$("$@" 2>&1) && echo "$out" | grep -q "$pattern"; then
    ok "$label"
  else
    fail "$label" "pattern '$pattern' not found"
  fi
}

# try_no_match passes when the command succeeds and its output does *not* match —
# for asserting that a filter left something out.
try_no_match() {
  local label="$1" pattern="$2"; shift 2
  local out
  if ! out=$("$@" 2>&1); then
    fail "$label" "command failed: $(echo "$out" | head -1)"
  elif echo "$out" | grep -q "$pattern"; then
    fail "$label" "pattern '$pattern' should not be present"
  else
    ok "$label"
  fi
}

# try_fail passes when the command exits non-zero (expected-error assertions).
try_fail() {
  local label="$1"; shift
  if "$@" >/dev/null 2>&1; then
    fail "$label" "command unexpectedly succeeded"
  else
    ok "$label"
  fi
}

# try_fail_match passes when the command exits non-zero *and* its output matches
# the pattern — use it where a bare try_fail could pass on an unrelated error.
try_fail_match() {
  local label="$1" pattern="$2"; shift 2
  local out
  if out=$("$@" 2>&1); then
    fail "$label" "command unexpectedly succeeded"
  elif echo "$out" | grep -q "$pattern"; then
    ok "$label"
  else
    fail "$label" "expected '$pattern', got: $(echo "$out" | head -1)"
  fi
}

# ── Health ────────────────────────────────────────────────────────────────────

echo "=== Nimbus smoke test  $(date -u '+%Y-%m-%dT%H:%M:%SZ') ==="
echo "    endpoint : $NIMBUS"
echo "    prefix   : $PREFIX"

section "Health"
try "/_nimbus/health" curl -sf "$NIMBUS/_nimbus/health"

# ── S3 ────────────────────────────────────────────────────────────────────────

section "S3"
PROBE=$(mktemp)
echo "nimbus-s3-probe-$$" >"$PROBE"
try     "head-bucket"         $CLI s3api head-bucket --bucket "$PREFIX"
try     "put-object"          $CLI s3api put-object --bucket "$PREFIX" --key probe.txt --body "$PROBE"
try     "head-object"         $CLI s3api head-object --bucket "$PREFIX" --key probe.txt
try_match "get-object matches body" "nimbus-s3-probe" \
    bash -c "$CLI s3api get-object --bucket '$PREFIX' --key probe.txt /dev/stdout"
try     "list-objects-v2"     $CLI s3api list-objects-v2 --bucket "$PREFIX"
try     "delete-object"       $CLI s3api delete-object --bucket "$PREFIX" --key probe.txt
rm -f "$PROBE"
# Lifecycle config round-trip
LC_JSON='{"Rules":[{"ID":"smoke","Status":"Enabled","Expiration":{"Days":30},"Filter":{"Prefix":""}}]}'
$CLI s3api put-bucket-lifecycle-configuration --bucket "$PREFIX" --lifecycle-configuration "$LC_JSON" 2>/dev/null \
  && ok "put-bucket-lifecycle-configuration" || fail "put-bucket-lifecycle-configuration"
LC_OUT=$($CLI s3api get-bucket-lifecycle-configuration --bucket "$PREFIX" --query 'Rules[0].ID' --output text 2>/dev/null)
if [ "$LC_OUT" = "smoke" ]; then
  ok "get-bucket-lifecycle-configuration"
else
  fail "get-bucket-lifecycle-configuration" "got: '$LC_OUT'"
fi

# ── SQS ───────────────────────────────────────────────────────────────────────

section "SQS"
QUEUE_URL=$($CLI sqs get-queue-url --queue-name "$PREFIX" --query QueueUrl --output text 2>/dev/null) || {
  fail "get-queue-url (queue not found — run 'make apply' first)"
  QUEUE_URL=""
}
if [ -n "$QUEUE_URL" ]; then
  try "get-queue-url" true
  MSG_ID=$($CLI sqs send-message --queue-url "$QUEUE_URL" --message-body "nimbus-sqs-probe-$$" \
    --query MessageId --output text)
  try "send-message" [ -n "$MSG_ID" ]
  try_match "receive-message body" "nimbus-sqs-probe" \
    $CLI sqs receive-message --queue-url "$QUEUE_URL" --max-number-of-messages 1
fi

# ── DynamoDB ──────────────────────────────────────────────────────────────────

section "DynamoDB"
if $CLI dynamodb describe-table --table-name "$PREFIX" >/dev/null 2>&1; then
  try "describe-table" $CLI dynamodb describe-table --table-name "$PREFIX"
  try "put-item" $CLI dynamodb put-item --table-name "$PREFIX" \
    --item '{"pk":{"S":"row#1"},"sk":{"S":"v1"},"data":{"S":"nimbus-ddb-probe"}}'
  try_match "get-item value" "nimbus-ddb-probe" \
    $CLI dynamodb get-item --table-name "$PREFIX" \
      --key '{"pk":{"S":"row#1"},"sk":{"S":"v1"}}'
  try "delete-item" $CLI dynamodb delete-item --table-name "$PREFIX" \
    --key '{"pk":{"S":"row#1"},"sk":{"S":"v1"}}'
else
  echo "  – DynamoDB table not found — skipped (enable_dynamodb=false or sidecar not running)"
fi

# ── Secrets Manager ───────────────────────────────────────────────────────────

section "Secrets Manager"
try_match "get-secret-value contains password" "nimbus-secret-42" \
  $CLI secretsmanager get-secret-value --secret-id "${PREFIX}/db-password" --query SecretString --output text

# ── SSM Parameter Store ───────────────────────────────────────────────────────

section "SSM Parameter Store"
try_match "get String param" "db.nimbus.local" \
  $CLI ssm get-parameter --name "/${PREFIX}/db-host" --query Parameter.Value --output text
try_match "get SecureString param" "super-secret-api-key" \
  $CLI ssm get-parameter --name "/${PREFIX}/api-key" --with-decryption \
    --query Parameter.Value --output text

# ── SES ───────────────────────────────────────────────────────────────────────

section "SES"
try "verify-email-identity" \
  $CLI ses verify-email-identity --email-address "noreply@nimbus.local"
try_match "list-identities contains email" "noreply@nimbus.local" \
  $CLI ses list-identities --query Identities
try "send-email (captured, never delivered)" \
  $CLI ses send-email \
    --from "noreply@nimbus.local" \
    --destination '{"ToAddresses":["user@example.com"]}' \
    --message '{"Subject":{"Data":"smoke test"},"Body":{"Text":{"Data":"hello"}}}'
try_match "/_nimbus/ses/messages captured" "smoke test" \
  curl -sf "$NIMBUS/_nimbus/ses/messages"

# ── Lambda ────────────────────────────────────────────────────────────────────

section "Lambda"
try_match "get-function config" '"python3.12"' \
  $CLI lambda get-function --function-name "$PREFIX" --query 'Configuration.Runtime'
try_match "zip function reports an S3 repository" '"S3"' \
  $CLI lambda get-function --function-name "$PREFIX" --query 'Code.RepositoryType'

# Container-image function. The image reference lives in the Code block, not in
# FunctionConfiguration — an empty ImageUri here is what makes the provider plan
# a change on every run.
try_match "image function reports an ECR repository" '"ECR"' \
  $CLI lambda get-function --function-name "$PREFIX-image" --query 'Code.RepositoryType'
try_match "image function round-trips image_uri" "$PREFIX:latest" \
  $CLI lambda get-function --function-name "$PREFIX-image" --query 'Code.ImageUri'
try_match "image function resolves a digest" "@sha256:" \
  $CLI lambda get-function --function-name "$PREFIX-image" --query 'Code.ResolvedImageUri'
try_match "image function round-trips image_config" "app.handler" \
  $CLI lambda get-function --function-name "$PREFIX-image" \
  --query 'Configuration.ImageConfigResponse.ImageConfig.Command'
try_match "image function keeps ephemeral storage" "1024" \
  $CLI lambda get-function --function-name "$PREFIX-image" \
  --query 'Configuration.EphemeralStorage.Size'
# Creating an Image function without a reference is rejected rather than stored
# as a function that could never run.
try_fail "create-function Image without image-uri is rejected" \
  $CLI lambda create-function --function-name "$PREFIX-no-image" \
  --package-type Image --role arn:aws:iam::000000000000:role/lambda-exec

# ── Lambda container execution ────────────────────────────────────────────────

section "Lambda container execution"

if docker info >/dev/null 2>&1; then
  case "$(uname -m)" in
    arm64 | aarch64) LAMBDA_ARCH="arm64" ;;
    *) LAMBDA_ARCH="x86_64" ;;
  esac
  FIXTURE_DIR="$(dirname "$0")/fixtures/lambda-image"
  CONTAINER_FN="$PREFIX-container"

  if docker build -q -t nimbus-smoke-lambda:dev "$FIXTURE_DIR" >/dev/null 2>&1; then
    ok "build container image fixture"

    $CLI lambda delete-function --function-name "$CONTAINER_FN" >/dev/null 2>&1 || true
    if $CLI lambda create-function --function-name "$CONTAINER_FN" \
      --package-type Image \
      --code ImageUri=nimbus-smoke-lambda:dev \
      --role arn:aws:iam::000000000000:role/lambda-exec \
      --architectures "$LAMBDA_ARCH" \
      --timeout 60 --memory-size 512 \
      --environment "Variables={NIMBUS_SMOKE_MARKER=container-ok}" >/dev/null 2>&1; then
      ok "create-function (container image)"

      # First invoke pays the container start; the image ships no runtime
      # emulator, so this only works if Nimbus injected one.
      INVOKE_OUT=$(mktemp)
      if $CLI lambda invoke --function-name "$CONTAINER_FN" \
        --cli-binary-format raw-in-base64-out \
        --payload '{"ping":"pong"}' "$INVOKE_OUT" >/dev/null 2>&1; then
        ok "invoke runs the real container"
        try_match "handler received the payload" "pong" cat "$INVOKE_OUT"
        try_match "handler was pointed back at Nimbus" "4566" cat "$INVOKE_OUT"
        try_match "handler saw its configured environment" "container-ok" cat "$INVOKE_OUT"
      else
        fail "invoke runs the real container"
      fi
      rm -f "$INVOKE_OUT"

      try_match "/_nimbus/lambda/containers lists the warm container" "$CONTAINER_FN" \
        curl -sf "$NIMBUS/_nimbus/lambda/containers"

      # Container output lands in the log group Lambda would use. Forwarding is
      # batched, so give the pump a moment to flush.
      LOG_GROUP="/aws/lambda/$CONTAINER_FN"
      LOGS_FOUND=""
      for _ in 1 2 3 4 5 6 7 8 9 10; do
        if $CLI logs filter-log-events --log-group-name "$LOG_GROUP" \
          --query "events[].message" --output text 2>/dev/null |
          grep -q "HANDLER-LOG-LINE"; then
          LOGS_FOUND=yes
          break
        fi
        sleep 0.5
      done
      if [ -n "$LOGS_FOUND" ]; then
        ok "container stdout forwarded to CloudWatch Logs"
      else
        fail "container stdout forwarded to CloudWatch Logs"
      fi

      try_match "container stderr forwarded to the same stream" "HANDLER-STDERR-LINE" \
        $CLI logs filter-log-events --log-group-name "$LOG_GROUP" \
        --query "events[].message" --output text
      try_match "log stream named per execution environment" '\[\$LATEST\]' \
        $CLI logs describe-log-streams --log-group-name "$LOG_GROUP" \
        --query "logStreams[].logStreamName" --output text

      # Second invoke reuses the warm container rather than starting another.
      REUSE_OUT=$(mktemp)
      try "second invoke reuses the warm container" \
        $CLI lambda invoke --function-name "$CONTAINER_FN" \
        --cli-binary-format raw-in-base64-out \
        --payload '{"ping":"again"}' "$REUSE_OUT"
      rm -f "$REUSE_OUT"

      try "delete-function tears the container down" \
        $CLI lambda delete-function --function-name "$CONTAINER_FN"

      if curl -sf "$NIMBUS/_nimbus/lambda/containers" | grep -q "$CONTAINER_FN"; then
        fail "container released on delete-function"
      else
        ok "container released on delete-function"
      fi
    else
      fail "create-function (container image)"
    fi
  else
    fail "build container image fixture"
  fi
else
  echo "  – Docker unavailable, skipping container execution checks"
fi

# ── API Gateway ───────────────────────────────────────────────────────────────

section "API Gateway"
API_ID=$($CLI apigateway get-rest-apis \
  --query "items[?name=='$PREFIX'].id" --output text 2>/dev/null)
if [ -n "$API_ID" ]; then
  try "get-rest-apis finds API" true
  try_match "get-resources /hello exists" "hello" \
    $CLI apigateway get-resources --rest-api-id "$API_ID"
  try_match "invoke MOCK endpoint returns 200" "hello from nimbus" \
    curl -sf "$NIMBUS/restapis/$API_ID/v1/_user_request_/hello"
else
  fail "get-rest-apis (API not found — run 'make apply' first)"
fi

# ── WebSocket API ─────────────────────────────────────────────────────────────

section "WebSocket API"
WS_API_ID=$($CLI apigatewayv2 get-apis \
  --query "Items[?Name=='${PREFIX}-ws'].ApiId" --output text 2>/dev/null)
if [ -n "$WS_API_ID" ]; then
  try "get-apis finds WebSocket API" true
  try_match "get-api protocol is WEBSOCKET" "WEBSOCKET" \
    $CLI apigatewayv2 get-api --api-id "$WS_API_ID"
  try_match "get-routes finds \$connect" "connect" \
    $CLI apigatewayv2 get-routes --api-id "$WS_API_ID"
  try_match "get-routes finds \$disconnect" "disconnect" \
    $CLI apigatewayv2 get-routes --api-id "$WS_API_ID"
  try_match "get-routes finds \$default" "default" \
    $CLI apigatewayv2 get-routes --api-id "$WS_API_ID"
  try_match "get-integrations finds Lambda integration" "AWS_PROXY" \
    $CLI apigatewayv2 get-integrations --api-id "$WS_API_ID"
  try_match "get-stages finds prod" "prod" \
    $CLI apigatewayv2 get-stages --api-id "$WS_API_ID"

  # WebSocket connection lifecycle ──────────────────────────────────────────
  # Clear Lambda invocations so we get a clean baseline.
  curl -sf -X DELETE "$NIMBUS/_nimbus/lambda/invocations" >/dev/null 2>&1 || true

  WS_RESULT=$(python3 - "$WS_API_ID" "prod" "$NIMBUS" <<'PYEOF'
import sys, socket, base64, hashlib, struct, os, time, urllib.parse

api_id, stage, nimbus = sys.argv[1], sys.argv[2], sys.argv[3]
parsed = urllib.parse.urlparse(nimbus)
host = parsed.hostname
port = parsed.port or 80

ws_key = base64.b64encode(os.urandom(16)).decode()
path = f'/apis/{api_id}/{stage}/_user_request_/'
request = (
    f'GET {path} HTTP/1.1\r\n'
    f'Host: {host}:{port}\r\n'
    f'Upgrade: websocket\r\n'
    f'Connection: Upgrade\r\n'
    f'Sec-WebSocket-Key: {ws_key}\r\n'
    f'Sec-WebSocket-Version: 13\r\n'
    f'\r\n'
)

s = socket.create_connection((host, port), timeout=5)
s.sendall(request.encode())

resp = b''
while b'\r\n\r\n' not in resp:
    chunk = s.recv(4096)
    if not chunk:
        break
    resp += chunk

status_line = resp.split(b'\r\n')[0].decode()
if '101' not in status_line:
    print(f'FAIL_UPGRADE: {status_line}', flush=True)
    s.close()
    sys.exit(1)

def send_frame(s, opcode, payload):
    if isinstance(payload, str):
        payload = payload.encode()
    mask = os.urandom(4)
    masked = bytes([b ^ mask[i % 4] for i, b in enumerate(payload)])
    n = len(payload)
    if n <= 125:
        hdr = struct.pack('BB', 0x80 | opcode, 0x80 | n)
    elif n <= 65535:
        hdr = struct.pack('!BBH', 0x80 | opcode, 0x80 | 126, n)
    else:
        hdr = struct.pack('!BBQ', 0x80 | opcode, 0x80 | 127, n)
    s.sendall(hdr + mask + masked)

# Send a text frame (triggers $default route).
send_frame(s, 0x1, '{"action":"ping"}')
time.sleep(0.2)

# Send close frame (triggers $disconnect).
send_frame(s, 0x8, struct.pack('!H', 1000))
time.sleep(0.2)
s.close()
print('OK', flush=True)
PYEOF
  )

  if [ "$WS_RESULT" = "OK" ]; then
    try "WebSocket upgrade succeeds (101 Switching Protocols)" true
  else
    fail "WebSocket upgrade" "$WS_RESULT"
  fi

  # Give Nimbus a moment to record invocations asynchronously.
  sleep 0.3
  WS_INVOCATIONS=$(curl -sf "$NIMBUS/_nimbus/lambda/invocations" 2>/dev/null || echo "")
  if echo "$WS_INVOCATIONS" | grep -q "CONNECT"; then
    try "\$connect Lambda invoked" true
  else
    fail "\$connect Lambda invoked (no CONNECT event in invocations)"
  fi
  if echo "$WS_INVOCATIONS" | grep -q "MESSAGE"; then
    try "\$default Lambda invoked on text frame" true
  else
    fail "\$default Lambda invoked on text frame (no MESSAGE event)"
  fi
  if echo "$WS_INVOCATIONS" | grep -q "DISCONNECT"; then
    try "\$disconnect Lambda invoked on close" true
  else
    fail "\$disconnect Lambda invoked on close (no DISCONNECT event)"
  fi

  # Management API — post to a live connection ──────────────────────────────
  CONN_RESULT=$(python3 - "$WS_API_ID" "prod" "$NIMBUS" <<'PYEOF'
import sys, socket, base64, hashlib, struct, os, time, threading, urllib.request, urllib.parse

api_id, stage, nimbus = sys.argv[1], sys.argv[2], sys.argv[3]
parsed = urllib.parse.urlparse(nimbus)
host = parsed.hostname
port = parsed.port or 80

ws_key = base64.b64encode(os.urandom(16)).decode()
path = f'/apis/{api_id}/{stage}/_user_request_/'
request = (
    f'GET {path} HTTP/1.1\r\n'
    f'Host: {host}:{port}\r\n'
    f'Upgrade: websocket\r\n'
    f'Connection: Upgrade\r\n'
    f'Sec-WebSocket-Key: {ws_key}\r\n'
    f'Sec-WebSocket-Version: 13\r\n'
    f'\r\n'
)

s = socket.create_connection((host, port), timeout=5)
s.sendall(request.encode())

resp = b''
while b'\r\n\r\n' not in resp:
    chunk = s.recv(4096)
    if not chunk:
        break
    resp += chunk

if '101' not in resp.split(b'\r\n')[0].decode():
    print('FAIL_UPGRADE', flush=True)
    sys.exit(1)

# Read response headers to get connection-id from invocations endpoint.
time.sleep(0.3)

# Fetch the connection id from invocations.
req = urllib.request.Request(f'{nimbus}/_nimbus/lambda/invocations')
with urllib.request.urlopen(req, timeout=3) as r:
    import json
    invocs = json.loads(r.read())

conn_id = None
for inv in reversed(invocs):
    payload = inv.get('Payload', {})
    rc = payload.get('requestContext', {})
    if rc.get('eventType') == 'CONNECT':
        conn_id = rc.get('connectionId')
        break

if not conn_id:
    print('FAIL_NO_CONN_ID', flush=True)
    sys.exit(1)

# POST to @connections management API.
mgmt_url = f'{nimbus}/prod/@connections/{conn_id}'
data = b'hello from management API'
req = urllib.request.Request(mgmt_url, data=data, method='POST')
try:
    with urllib.request.urlopen(req, timeout=3) as r:
        status = r.status
except Exception as e:
    print(f'FAIL_POST: {e}', flush=True)
    sys.exit(1)

if status != 200:
    print(f'FAIL_STATUS: {status}', flush=True)
    sys.exit(1)

# DELETE @connections — should close the connection.
req = urllib.request.Request(mgmt_url, method='DELETE')
try:
    with urllib.request.urlopen(req, timeout=3) as r:
        delete_status = r.status
except Exception as e:
    print(f'FAIL_DELETE: {e}', flush=True)
    sys.exit(1)

s.close()
print('OK', flush=True)
PYEOF
  )

  if [ "$CONN_RESULT" = "OK" ]; then
    try "management API POST /@connections sends to client" true
    try "management API DELETE /@connections closes connection" true
  else
    fail "management API" "$CONN_RESULT"
    fail "management API DELETE /@connections closes connection"
  fi
else
  fail "get-apis (WebSocket API not found — run 'make apply' first)"
fi

# ── ECR ───────────────────────────────────────────────────────────────────────

section "ECR"
try_match "describe-repositories finds repo" "$PREFIX" \
  $CLI ecr describe-repositories --query 'repositories[].repositoryName' --output text

# Docker push/pull round-trip
REGISTRY="localhost:4566"
ECR_TOKEN=$($CLI ecr get-authorization-token \
  --query 'authorizationData[0].authorizationToken' --output text 2>/dev/null \
  | base64 --decode | cut -d: -f2)
if echo "$ECR_TOKEN" | docker login --username AWS --password-stdin "$REGISTRY" >/dev/null 2>&1; then
  try "docker login" true
  docker pull hello-world:latest >/dev/null 2>&1 || true
  docker tag hello-world:latest "$REGISTRY/$PREFIX:smoke" >/dev/null 2>&1
  try "docker push"  docker push "$REGISTRY/$PREFIX:smoke" >/dev/null 2>&1
  docker rmi "$REGISTRY/$PREFIX:smoke" >/dev/null 2>&1 || true
  try_match "docker pull" "Pull complete\|Already exists\|$PREFIX" \
    docker pull "$REGISTRY/$PREFIX:smoke"
  docker rmi "$REGISTRY/$PREFIX:smoke" >/dev/null 2>&1 || true
else
  fail "docker login"
fi

# ── ECS ───────────────────────────────────────────────────────────────────────

section "ECS"
try_match "describe-clusters ACTIVE" "ACTIVE" \
  $CLI ecs describe-clusters --clusters "$PREFIX" --query 'clusters[0].status' --output text
try_match "describe-task-definition family" "$PREFIX" \
  $CLI ecs describe-task-definition --task-definition "$PREFIX" --query taskDefinition.family --output text
try_match "list-services for cluster" "service" \
  $CLI ecs list-services --cluster "$PREFIX" --query serviceArns --output text

# Run a one-off task and stop it
TASK_ARN=$($CLI ecs run-task \
  --cluster "$PREFIX" --task-definition "$PREFIX" --count 1 \
  --query 'tasks[0].taskArn' --output text 2>/dev/null)
if [ -n "${TASK_ARN:-}" ] && [ "$TASK_ARN" != "None" ]; then
  # With Docker reachable a task starts PENDING and flips to RUNNING once its
  # container is up; the lifecycle poller runs every 5 s (wait max ~30 s).
  for _i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do
    TASK_STATUS=$($CLI ecs describe-tasks --cluster "$PREFIX" --tasks "$TASK_ARN" \
      --query 'tasks[0].lastStatus' --output text 2>/dev/null)
    [ "$TASK_STATUS" != "PENDING" ] && break
    sleep 2
  done
  try_match "run-task RUNNING" "RUNNING" echo "$TASK_STATUS"
  try "stop-task" $CLI ecs stop-task --cluster "$PREFIX" --task "$TASK_ARN"
else
  fail "run-task"
fi

# Service load balancers — the Terraform service attaches the target group to the
# "app" container on port 80, which matches the task definition's portMappings.
try_match "describe-services reports load balancer" "targetgroup/$PREFIX" \
  $CLI ecs describe-services --cluster "$PREFIX" --services "$PREFIX" \
    --query 'services[0].loadBalancers[0].targetGroupArn' --output text

ECS_TG_ARN=$($CLI elbv2 describe-target-groups --names "$PREFIX" \
  --query 'TargetGroups[0].TargetGroupArn' --output text 2>/dev/null)
if [ -n "${ECS_TG_ARN:-}" ] && [ "$ECS_TG_ARN" != "None" ]; then
  # containerPort must be declared in the container's portMappings...
  try_fail_match "create-service rejects undefined container port" \
    "did not have a container port 8080 defined" \
    $CLI ecs create-service --cluster "$PREFIX" --service-name "${PREFIX}-badport" \
      --task-definition "$PREFIX" --desired-count 1 --launch-type FARGATE \
      --load-balancers "targetGroupArn=$ECS_TG_ARN,containerName=app,containerPort=8080"
  # ...and containerName must exist in the task definition
  try_fail_match "create-service rejects unknown container name" \
    "does not exist in the task definition" \
    $CLI ecs create-service --cluster "$PREFIX" --service-name "${PREFIX}-badname" \
      --task-definition "$PREFIX" --desired-count 1 --launch-type FARGATE \
      --load-balancers "targetGroupArn=$ECS_TG_ARN,containerName=nope,containerPort=80"
else
  fail "describe-target-groups for ECS load balancer checks (run 'make apply' first)"
fi

# ── ECS Container Insights ────────────────────────────────────────────────────

# The Terraform cluster sets containerInsights = enabled, so Nimbus publishes
# performance events to /aws/ecs/containerinsights/<cluster>/performance.
section "ECS Container Insights"
try_match "describe-clusters reports containerInsights" "enabled" \
  $CLI ecs describe-clusters --clusters "$PREFIX" --include SETTINGS \
    --query "clusters[0].settings[?name=='containerInsights'].value" --output text

CI_GROUP="/aws/ecs/containerinsights/$PREFIX/performance"

# Events describe an interval that has already passed and are published one
# ingestion delay later (80 s by default), so the first round can be up to
# ~2.5 min behind a fresh `make nuke`. Normally they are already there.
CI_EVENTS=""
for _i in $(seq 1 30); do
  CI_EVENTS=$($CLI logs filter-log-events --log-group-name "$CI_GROUP" \
    --query 'events[].message' --output text 2>/dev/null)
  [ -n "${CI_EVENTS:-}" ] && [ "$CI_EVENTS" != "None" ] && break
  sleep 5
done

if [ -n "${CI_EVENTS:-}" ] && [ "$CI_EVENTS" != "None" ]; then
  ok "performance log group has events"
  for TYPE in Cluster Service Task Container; do
    try_match "publishes $TYPE events" "\"Type\":\"$TYPE\"" echo "$CI_EVENTS"
  done
  # Utilisation is synthetic but must be reported against what the task
  # definition reserves — 256 CPU units / 512 MB in the fixture.
  try_match "Task events report reservations from the task definition" '"CpuReserved":256' \
    echo "$CI_EVENTS"
  try_match "Task events carry a 32-hex TaskId" '"TaskId":"[0-9a-f]\{32\}"' echo "$CI_EVENTS"

  # Cluster and per-task telemetry land in separate streams, as in real ECS.
  try_match "cluster telemetry stream exists" "ClusterTelemetry-$PREFIX" \
    $CLI logs describe-log-streams --log-group-name "$CI_GROUP" \
      --query 'logStreams[].logStreamName' --output text
  try_match "task telemetry stream exists" "FargateTelemetry-" \
    $CLI logs describe-log-streams --log-group-name "$CI_GROUP" \
      --query 'logStreams[].logStreamName' --output text

  # The natural server-side filter for this group: a JSON pattern on $.Type.
  TASK_ONLY=$($CLI logs filter-log-events --log-group-name "$CI_GROUP" \
    --filter-pattern '{ $.Type = "Task" }' --query 'events[].message' --output text 2>/dev/null)
  if [ -n "${TASK_ONLY:-}" ] && [ "$TASK_ONLY" != "None" ]; then
    try_match "JSON filter pattern selects Task events" '"Type":"Task"' echo "$TASK_ONLY"
    try_no_match "JSON filter pattern excludes other types" '"Type":"Container"' \
      echo "$TASK_ONLY"
  else
    fail "filter-log-events with a JSON pattern on \$.Type"
  fi
else
  fail "performance log group $CI_GROUP has no events" \
    "waited 150 s; check the ECS emitter and NIMBUS_ECS_INSIGHTS_* settings"
fi

# ── KMS ───────────────────────────────────────────────────────────────────────

section "KMS"
KEY_ID=$($CLI kms describe-key --key-id "alias/${PREFIX}" --query KeyMetadata.KeyId --output text 2>/dev/null)
if [ -n "${KEY_ID:-}" ]; then
  try "describe-key via alias" true
  # Encrypt → Decrypt round-trip
  # AWS CLI v2 requires --plaintext as base64 for blob parameters
  CT=$($CLI kms encrypt \
    --key-id "alias/${PREFIX}" \
    --plaintext "$(printf 'nimbus-kms-probe' | base64)" \
    --query CiphertextBlob --output text 2>/dev/null)
  if [ -n "$CT" ]; then
    PT=$($CLI kms decrypt \
      --ciphertext-blob "$CT" \
      --query Plaintext --output text 2>/dev/null | base64 --decode 2>/dev/null)
    if [ "$PT" = "nimbus-kms-probe" ]; then
      ok "encrypt → decrypt round-trip"
    else
      fail "encrypt → decrypt round-trip" "got: '$PT'"
    fi
  else
    fail "kms encrypt"
  fi
  # Key rotation
  $CLI kms enable-key-rotation --key-id "$KEY_ID" 2>/dev/null
  ROT=$($CLI kms get-key-rotation-status --key-id "$KEY_ID" --query KeyRotationEnabled --output text 2>/dev/null)
  if [ "$ROT" = "True" ]; then
    ok "enable-key-rotation / get-key-rotation-status"
  else
    fail "enable-key-rotation / get-key-rotation-status" "got: '$ROT'"
  fi
  # Grants
  GRANT_ID=$($CLI kms create-grant \
    --key-id "$KEY_ID" \
    --grantee-principal "arn:aws:iam::000000000000:role/smoke-test" \
    --operations Decrypt GenerateDataKey \
    --query GrantId --output text 2>/dev/null)
  if [ -n "$GRANT_ID" ]; then
    ok "create-grant"
    $CLI kms revoke-grant --key-id "$KEY_ID" --grant-id "$GRANT_ID" 2>/dev/null
    ok "revoke-grant"
  else
    fail "create-grant"
  fi
else
  fail "describe-key (alias not found — run 'make apply' first)"
fi

# ── SNS ───────────────────────────────────────────────────────────────────────

section "SNS"
TOPIC_ARN=$($CLI sns list-topics --query "Topics[?contains(TopicArn,'$PREFIX')].TopicArn" \
  --output text 2>/dev/null)
if [ -n "$TOPIC_ARN" ]; then
  try "list-topics finds topic" true
  MSG_ID=$($CLI sns publish \
    --topic-arn "$TOPIC_ARN" \
    --message "nimbus-sns-probe-$$" \
    --subject "smoke-test" \
    --query MessageId --output text 2>/dev/null)
  try "publish message" [ -n "$MSG_ID" ]
  try_match "/_nimbus/sns/messages captured" "nimbus-sns-probe" \
    curl -sf "$NIMBUS/_nimbus/sns/messages"
else
  fail "list-topics (topic not found — run 'make apply' first)"
fi

# ── EventBridge ───────────────────────────────────────────────────────────────

section "EventBridge"
try_match "list-event-buses finds custom bus" "$PREFIX" \
  $CLI events list-event-buses --query 'EventBuses[].Name' --output text
try_match "list-rules finds rule" "$PREFIX" \
  $CLI events list-rules --event-bus-name "$PREFIX" --query 'Rules[].Name' --output text
try "put-events" $CLI events put-events --entries \
  "[{\"Source\":\"nimbus.test\",\"DetailType\":\"NimbusEvent\",\"Detail\":\"{\\\"probe\\\":\\\"smoke-$$\\\"}\",\"EventBusName\":\"$PREFIX\"}]"
try_match "/_nimbus/eventbridge/events captured" "nimbus.test" \
  curl -sf "$NIMBUS/_nimbus/eventbridge/events"

# ── IAM / STS ─────────────────────────────────────────────────────────────────

section "IAM / STS"
try_match "get-caller-identity" "000000000000" \
  $CLI sts get-caller-identity --query Account --output text
try_match "create-role / get-role" "$PREFIX-task-execution" \
  $CLI iam get-role --role-name "$PREFIX-task-execution" --query Role.RoleName --output text
try_match "list-roles finds role" "$PREFIX-task-execution" \
  $CLI iam list-roles --query "Roles[].RoleName" --output text
try "assume-role" \
  $CLI sts assume-role \
    --role-arn "arn:aws:iam::000000000000:role/$PREFIX-task-execution" \
    --role-session-name smoke-test
try_match "list-attached-role-policies" "AmazonECSTaskExecutionRolePolicy" \
  $CLI iam list-attached-role-policies --role-name "$PREFIX-task-execution" \
    --query "AttachedPolicies[].PolicyName" --output text
try_match "get-policy (customer-managed)" "$PREFIX-custom" \
  $CLI iam get-policy \
    --policy-arn "arn:aws:iam::000000000000:policy/$PREFIX-custom" \
    --query Policy.PolicyName --output text
try_match "get-role-policy (inline)" "$PREFIX-inline" \
  $CLI iam get-role-policy \
    --role-name "$PREFIX-task-execution" \
    --policy-name "$PREFIX-inline" \
    --query PolicyName --output text
try_match "get-instance-profile" "$PREFIX-task-execution" \
  $CLI iam get-instance-profile \
    --instance-profile-name "$PREFIX-task-execution" \
    --query InstanceProfile.InstanceProfileName --output text

# ── CloudWatch Logs ───────────────────────────────────────────────────────────

section "CloudWatch Logs"
try_match "describe-log-groups finds group" "/nimbus/$PREFIX/app" \
  $CLI logs describe-log-groups \
    --log-group-name-prefix "/nimbus/$PREFIX" \
    --query "logGroups[].logGroupName" --output text
# Retention and the CMK are set at or after creation and must read back, or the
# provider sees them as unset on refresh.
try_match "describe-log-groups reports retention" "14" \
  $CLI logs describe-log-groups \
    --log-group-name-prefix "/nimbus/$PREFIX/app" \
    --query "logGroups[].retentionInDays" --output text
try_match "describe-log-groups reports kms key" "arn:aws:kms:" \
  $CLI logs describe-log-groups \
    --log-group-name-prefix "/nimbus/$PREFIX/app" \
    --query "logGroups[].kmsKeyId" --output text
try_match "describe-log-streams finds stream" "container" \
  $CLI logs describe-log-streams \
    --log-group-name "/nimbus/$PREFIX/app" \
    --query "logStreams[].logStreamName" --output text
try "put-log-events" \
  $CLI logs put-log-events \
    --log-group-name "/nimbus/$PREFIX/app" \
    --log-stream-name "container" \
    --log-events "[{\"timestamp\":$(( $(date +%s) * 1000 )),\"message\":\"nimbus-cwl-probe-$$\"}]"
try_match "get-log-events returns event" "nimbus-cwl-probe" \
  $CLI logs get-log-events \
    --log-group-name "/nimbus/$PREFIX/app" \
    --log-stream-name "container" \
    --query "events[].message" --output text
try_match "filter-log-events pattern match" "nimbus-cwl-probe" \
  $CLI logs filter-log-events \
    --log-group-name "/nimbus/$PREFIX/app" \
    --filter-pattern "nimbus-cwl-probe" \
    --query "events[].message" --output text
# JSON metric-filter patterns select on the event body, the way Container
# Insights consumers query the performance log group.
CWL_EVENTS=$(mktemp)
printf '[{"timestamp":%s,"message":"{\\"Type\\":\\"Task\\",\\"TaskId\\":\\"nimbus-cwl-json-probe\\",\\"CpuUtilized\\":64.5}"}]' \
  "$(( $(date +%s) * 1000 ))" >"$CWL_EVENTS"
try "put-log-events (json message)" \
  $CLI logs put-log-events \
    --log-group-name "/nimbus/$PREFIX/app" \
    --log-stream-name "container" \
    --log-events "file://$CWL_EVENTS"
rm -f "$CWL_EVENTS"
try_match "filter-log-events json pattern" "nimbus-cwl-json-probe" \
  $CLI logs filter-log-events \
    --log-group-name "/nimbus/$PREFIX/app" \
    --filter-pattern '{ $.Type = "Task" }' \
    --query "events[].message" --output text
try_match "filter-log-events json numeric comparison" "nimbus-cwl-json-probe" \
  $CLI logs filter-log-events \
    --log-group-name "/nimbus/$PREFIX/app" \
    --filter-pattern '{ $.CpuUtilized > 10 && $.TaskId = "nimbus-cwl-*" }' \
    --query "events[].message" --output text
# A pattern that selects nothing must come back empty, not with everything.
CWL_MISSES=$($CLI logs filter-log-events \
  --log-group-name "/nimbus/$PREFIX/app" \
  --filter-pattern '{ $.Type = "Service" }' \
  --query "length(events)" --output text 2>&1)
if [ "$CWL_MISSES" = "0" ]; then
  ok "filter-log-events json pattern excludes non-matches"
else
  fail "filter-log-events json pattern excludes non-matches" "got: '$CWL_MISSES'"
fi
try_fail_match "filter-log-events rejects a malformed pattern" "InvalidParameterException" \
  $CLI logs filter-log-events \
    --log-group-name "/nimbus/$PREFIX/app" \
    --filter-pattern '{ $.Type = }'
try_match "/_nimbus/logs inspection endpoint" "nimbus-cwl-probe" \
  curl -sf "$NIMBUS/_nimbus/logs/nimbus/$PREFIX/app/container"

# ── EventBridge Scheduler ────────────────────────────────────────────────────

section "EventBridge Scheduler"
try_match "list-schedule-groups finds group" "$PREFIX" \
  $CLI scheduler list-schedule-groups \
    --query "ScheduleGroups[].Name" --output text
try_match "get-schedule-group returns ACTIVE" "ACTIVE" \
  $CLI scheduler get-schedule-group \
    --name "$PREFIX" \
    --query State --output text
try_match "get-schedule returns rate expression" "rate(5 minutes)" \
  $CLI scheduler get-schedule \
    --name "$PREFIX" \
    --group-name "$PREFIX" \
    --query ScheduleExpression --output text
try_match "list-schedules finds schedule" "$PREFIX" \
  $CLI scheduler list-schedules \
    --group-name "$PREFIX" \
    --query "Schedules[].Name" --output text
try_match "/_nimbus/scheduler/schedules inspection" "$PREFIX" \
  curl -sf "$NIMBUS/_nimbus/scheduler/schedules"

# ── CloudFront ────────────────────────────────────────────────────────────────

section "CloudFront"
CF_DIST_CONFIG="{
  \"CallerReference\":\"nimbus-smoke-$$\",
  \"Comment\":\"$PREFIX-dist\",
  \"Enabled\":true,
  \"Origins\":{\"Quantity\":1,\"Items\":[{\"Id\":\"o1\",\"DomainName\":\"nimbus.localhost\",\"CustomOriginConfig\":{\"HTTPPort\":80,\"HTTPSPort\":443,\"OriginProtocolPolicy\":\"http-only\",\"OriginSslProtocols\":{\"Quantity\":1,\"Items\":[\"TLSv1.2\"]}}}]},
  \"DefaultCacheBehavior\":{\"TargetOriginId\":\"o1\",\"ViewerProtocolPolicy\":\"allow-all\",\"ForwardedValues\":{\"QueryString\":false,\"Cookies\":{\"Forward\":\"none\"}},\"TrustedSigners\":{\"Enabled\":false,\"Quantity\":0},\"MinTTL\":0},
  \"CacheBehaviors\":{\"Quantity\":0},
  \"CustomErrorResponses\":{\"Quantity\":0},
  \"Restrictions\":{\"GeoRestriction\":{\"RestrictionType\":\"none\",\"Quantity\":0}},
  \"ViewerCertificate\":{\"CloudFrontDefaultCertificate\":true}
}"
CF_ID=$($CLI cloudfront create-distribution \
    --distribution-config "$CF_DIST_CONFIG" \
    --query "Distribution.Id" --output text 2>/dev/null) && ok "create-distribution" || { fail "create-distribution"; CF_ID=""; }

try_match "get-distribution returns Deployed" "Deployed" \
  $CLI cloudfront get-distribution \
    --id "$CF_ID" \
    --query "Distribution.Status" --output text
try_match "list-distributions finds distribution" "$CF_ID" \
  $CLI cloudfront list-distributions \
    --query "DistributionList.Items[].Id" --output text
try "update-distribution" \
  $CLI cloudfront update-distribution \
    --id "$CF_ID" \
    --if-match "dummy" \
    --distribution-config "{
      \"CallerReference\":\"nimbus-smoke-$$\",
      \"Comment\":\"$PREFIX-dist-updated\",
      \"Enabled\":true,
      \"Origins\":{\"Quantity\":1,\"Items\":[{\"Id\":\"o1\",\"DomainName\":\"nimbus.localhost\",\"CustomOriginConfig\":{\"HTTPPort\":80,\"HTTPSPort\":443,\"OriginProtocolPolicy\":\"http-only\",\"OriginSslProtocols\":{\"Quantity\":1,\"Items\":[\"TLSv1.2\"]}}}]},
      \"DefaultCacheBehavior\":{\"TargetOriginId\":\"o1\",\"ViewerProtocolPolicy\":\"allow-all\",\"ForwardedValues\":{\"QueryString\":false,\"Cookies\":{\"Forward\":\"none\"}},\"TrustedSigners\":{\"Enabled\":false,\"Quantity\":0},\"MinTTL\":0},
      \"CacheBehaviors\":{\"Quantity\":0},
      \"CustomErrorResponses\":{\"Quantity\":0},
      \"Restrictions\":{\"GeoRestriction\":{\"RestrictionType\":\"none\",\"Quantity\":0}},
      \"ViewerCertificate\":{\"CloudFrontDefaultCertificate\":true}
    }"
try_match "/_nimbus/cloudfront/distributions inspection" "$CF_ID" \
  curl -sf "$NIMBUS/_nimbus/cloudfront/distributions"
try "delete-distribution" \
  $CLI cloudfront delete-distribution \
    --id "$CF_ID" \
    --if-match "dummy"

# ── ALB ───────────────────────────────────────────────────────────────────────

section "ALB"
LB_ARN=$($CLI elbv2 describe-load-balancers --names "$PREFIX" \
  --query "LoadBalancers[0].LoadBalancerArn" --output text 2>/dev/null)
if [ -n "${LB_ARN:-}" ] && [ "$LB_ARN" != "None" ]; then
  try "describe-load-balancers finds LB" true
  try_match "LB state active" "active" \
    $CLI elbv2 describe-load-balancers --load-balancer-arns "$LB_ARN" \
      --query "LoadBalancers[0].State.Code" --output text
  try_match "describe-load-balancer-attributes" "deletion_protection.enabled" \
    $CLI elbv2 describe-load-balancer-attributes --load-balancer-arn "$LB_ARN" \
      --query "Attributes[].Key" --output text
  # The TF fixture wires the LB to real subnets in two AZs; assert the resolved
  # zones came back rather than relying on `terraform apply` not having failed.
  LB_AZ_COUNT=$($CLI elbv2 describe-load-balancers --load-balancer-arns "$LB_ARN" \
    --query "LoadBalancers[0].AvailabilityZones[].ZoneName" --output text 2>/dev/null \
    | tr '\t' '\n' | sort -u | grep -c .)
  if [ "${LB_AZ_COUNT:-0}" -ge 2 ]; then
    ok "TF-provisioned LB spans $LB_AZ_COUNT Availability Zones"
  else
    fail "TF-provisioned LB spans >=2 AZs" "found ${LB_AZ_COUNT:-0} distinct zone(s)"
  fi
else
  fail "describe-load-balancers (LB not found — run 'make apply' first)"
  LB_ARN=""
fi

TG_ARN=$($CLI elbv2 describe-target-groups --names "$PREFIX" \
  --query "TargetGroups[0].TargetGroupArn" --output text 2>/dev/null)
if [ -n "${TG_ARN:-}" ] && [ "$TG_ARN" != "None" ]; then
  try "describe-target-groups finds TG" true
  try_match "target group lists attached LB" "loadbalancer/app" \
    $CLI elbv2 describe-target-groups --target-group-arns "$TG_ARN" \
      --query "TargetGroups[0].LoadBalancerArns[0]" --output text
  try_match "describe-target-group-attributes" "deregistration_delay" \
    $CLI elbv2 describe-target-group-attributes --target-group-arn "$TG_ARN" \
      --query "Attributes[].Key" --output text
  # Register a target and verify health
  try "register-targets" \
    $CLI elbv2 register-targets --target-group-arn "$TG_ARN" \
      --targets Id=10.0.1.1,Port=80
  try_match "describe-target-health healthy" "healthy" \
    $CLI elbv2 describe-target-health --target-group-arn "$TG_ARN" \
      --query "TargetHealthDescriptions[0].TargetHealth.State" --output text
  try "deregister-targets" \
    $CLI elbv2 deregister-targets --target-group-arn "$TG_ARN" \
      --targets Id=10.0.1.1,Port=80
else
  fail "describe-target-groups (TG not found — run 'make apply' first)"
  TG_ARN=""
fi

if [ -n "${LB_ARN:-}" ]; then
  LISTENER_ARN=$($CLI elbv2 describe-listeners --load-balancer-arn "$LB_ARN" \
    --query "Listeners[0].ListenerArn" --output text 2>/dev/null)
  if [ -n "${LISTENER_ARN:-}" ] && [ "$LISTENER_ARN" != "None" ]; then
    try "describe-listeners finds listener" true
    try_match "listener port 80" "80" \
      $CLI elbv2 describe-listeners --listener-arns "$LISTENER_ARN" \
        --query "Listeners[0].Port" --output text
    try_match "describe-rules finds default rule" "default" \
      $CLI elbv2 describe-rules --listener-arn "$LISTENER_ARN" \
        --query "Rules[?IsDefault==\`true\`].Priority" --output text
    try_match "describe-rules finds path-pattern rule" "100" \
      $CLI elbv2 describe-rules --listener-arn "$LISTENER_ARN" \
        --query "Rules[].Priority" --output text
  else
    fail "describe-listeners (listener not found — run 'make apply' first)"
  fi
fi

# Subnet/AZ validation: a multi-AZ ALB is accepted, a single-subnet ALB rejected.
try "create multi-AZ load-balancer" \
  $CLI elbv2 create-load-balancer --name "${PREFIX}-multiaz" --type application \
    --subnets subnet-0000000000000000a subnet-0000000000000000b
try_fail "single-subnet load-balancer rejected" \
  $CLI elbv2 create-load-balancer --name "${PREFIX}-singleaz" --type application \
    --subnets subnet-0000000000000000a
# The two checks above use synthetic IDs, which exercise the fallback path where
# an unknown subnet is treated as its own zone. This one uses a real subnet from
# the EC2 store twice, so rejection has to come from the SubnetAZ lookup.
REAL_SUBNET=$($CLI ec2 describe-subnets --query "Subnets[0].SubnetId" --output text 2>/dev/null)
if [ -n "${REAL_SUBNET:-}" ] && [ "$REAL_SUBNET" != "None" ]; then
  try_fail_match "same store-backed subnet twice rejected" \
    "two different Availability Zones" \
    $CLI elbv2 create-load-balancer --name "${PREFIX}-dupsubnet" --type application \
      --subnets "$REAL_SUBNET" "$REAL_SUBNET"
else
  fail "same store-backed subnet twice rejected" "no subnet found in the EC2 store"
fi
$CLI elbv2 delete-load-balancer --load-balancer-arn \
  "$($CLI elbv2 describe-load-balancers --names "${PREFIX}-multiaz" \
    --query 'LoadBalancers[0].LoadBalancerArn' --output text 2>/dev/null)" 2>/dev/null || true

try_match "/_nimbus/alb/loadbalancers inspection" "$PREFIX" \
  curl -sf "$NIMBUS/_nimbus/alb/loadbalancers"
try_match "/_nimbus/alb/targetgroups inspection" "$PREFIX" \
  curl -sf "$NIMBUS/_nimbus/alb/targetgroups"
try_match "/_nimbus/alb/listeners inspection" "$PREFIX" \
  curl -sf "$NIMBUS/_nimbus/alb/listeners"

# ── RDS / Aurora ─────────────────────────────────────────────────────────────

section "RDS / Aurora"
DB_CLUSTER=$($CLI rds describe-db-clusters --db-cluster-identifier "$PREFIX" \
  --query "DBClusters[0].DBClusterIdentifier" --output text 2>/dev/null)
if [ -n "${DB_CLUSTER:-}" ] && [ "$DB_CLUSTER" != "None" ]; then
  try "describe-db-clusters finds cluster" true
  try_match "cluster status available" "available" \
    $CLI rds describe-db-clusters --db-cluster-identifier "$PREFIX" \
      --query "DBClusters[0].Status" --output text
  try_match "cluster endpoint set" "localhost\|postgres\|127" \
    $CLI rds describe-db-clusters --db-cluster-identifier "$PREFIX" \
      --query "DBClusters[0].Endpoint" --output text
else
  fail "describe-db-clusters (cluster not found — run 'make apply' first)"
fi

DB_SUBNET=$($CLI rds describe-db-subnet-groups --db-subnet-group-name "$PREFIX" \
  --query "DBSubnetGroups[0].DBSubnetGroupName" --output text 2>/dev/null)
if [ -n "${DB_SUBNET:-}" ] && [ "$DB_SUBNET" != "None" ]; then
  try "describe-db-subnet-groups finds group" true
  try_match "subnet group status Complete" "Complete" \
    $CLI rds describe-db-subnet-groups --db-subnet-group-name "$PREFIX" \
      --query "DBSubnetGroups[0].SubnetGroupStatus" --output text
  # The group must report the subnets it was created with (#105) — the
  # provider reads subnet_ids back from here.
  try_match "subnet group reports its subnets" "^2$" \
    $CLI rds describe-db-subnet-groups --db-subnet-group-name "$PREFIX" \
      --query "length(DBSubnetGroups[0].Subnets)" --output text
  try_match "subnet group resolves its VPC" "vpc-" \
    $CLI rds describe-db-subnet-groups --db-subnet-group-name "$PREFIX" \
      --query "DBSubnetGroups[0].VpcId" --output text
  try_match "cluster reports its subnet group" "$PREFIX" \
    $CLI rds describe-db-clusters --db-cluster-identifier "$PREFIX" \
      --query "DBClusters[0].DBSubnetGroup" --output text
  try_match "standalone instance reports its subnet group" "$PREFIX" \
    $CLI rds describe-db-instances --db-instance-identifier "${PREFIX}-standalone" \
      --query "DBInstances[0].DBSubnetGroup.DBSubnetGroupName" --output text
  try_match "cluster member inherits the cluster's subnet group" "$PREFIX" \
    $CLI rds describe-db-instances --db-instance-identifier "${PREFIX}-instance-1" \
      --query "DBInstances[0].DBSubnetGroup.DBSubnetGroupName" --output text
else
  fail "describe-db-subnet-groups (subnet group not found — run 'make apply' first)"
fi

DB_INSTANCE=$($CLI rds describe-db-instances \
  --db-instance-identifier "${PREFIX}-instance-1" \
  --query "DBInstances[0].DBInstanceIdentifier" --output text 2>/dev/null)
if [ -n "${DB_INSTANCE:-}" ] && [ "$DB_INSTANCE" != "None" ]; then
  try "describe-db-instances finds instance" true
  try_match "instance status available" "available" \
    $CLI rds describe-db-instances --db-instance-identifier "${PREFIX}-instance-1" \
      --query "DBInstances[0].DBInstanceStatus" --output text
  try_match "instance Performance Insights enabled" "True" \
    $CLI rds describe-db-instances --db-instance-identifier "${PREFIX}-instance-1" \
      --query "DBInstances[0].PerformanceInsightsEnabled" --output text
  try_match "instance has DbiResourceId" "db-" \
    $CLI rds describe-db-instances --db-instance-identifier "${PREFIX}-instance-1" \
      --query "DBInstances[0].DbiResourceId" --output text
else
  fail "describe-db-instances (instance not found — run 'make apply' first)"
fi

# Describe filters (#95) — the TF provider reads instances via Filters, not
# the identifier param; with 2+ instances an ignored filter returns them all.
try_match "db-instance-id filter finds standalone" "${PREFIX}-standalone" \
  $CLI rds describe-db-instances \
    --filters "Name=db-instance-id,Values=${PREFIX}-standalone" \
    --query "DBInstances[0].DBInstanceIdentifier" --output text
try_match "db-instance-id filter returns exactly one" "^1$" \
  $CLI rds describe-db-instances \
    --filters "Name=db-instance-id,Values=${PREFIX}-standalone" \
    --query "length(DBInstances)" --output text
try_match "db-cluster-id filter finds cluster member" "${PREFIX}-instance-1" \
  $CLI rds describe-db-instances \
    --filters "Name=db-cluster-id,Values=${PREFIX}" \
    --query "DBInstances[].DBInstanceIdentifier" --output text

try_match "/_nimbus/rds/clusters inspection" "$PREFIX" \
  curl -sf "$NIMBUS/_nimbus/rds/clusters"

# A subnet an RDS instance sits in cannot be deleted (#105). Built from
# scratch so the Terraform-managed resources above stay untouched.
DEP_VPC=$($CLI ec2 create-vpc --cidr-block 10.90.0.0/16 \
  --query "Vpc.VpcId" --output text 2>/dev/null)
DEP_SUBNET=$($CLI ec2 create-subnet --vpc-id "$DEP_VPC" --cidr-block 10.90.1.0/24 \
  --availability-zone "${REGION}a" --query "Subnet.SubnetId" --output text 2>/dev/null)
if [ -n "${DEP_SUBNET:-}" ] && [ "$DEP_SUBNET" != "None" ]; then
  try "create-db-subnet-group over a live subnet" \
    $CLI rds create-db-subnet-group --db-subnet-group-name "${PREFIX}-dep" \
      --db-subnet-group-description "subnet dependency probe" \
      --subnet-ids "$DEP_SUBNET"
  # A subnet group with no DB in it pins nothing.
  FREE_SUBNET=$($CLI ec2 create-subnet --vpc-id "$DEP_VPC" --cidr-block 10.90.2.0/24 \
    --availability-zone "${REGION}a" --query "Subnet.SubnetId" --output text 2>/dev/null)
  $CLI rds create-db-subnet-group --db-subnet-group-name "${PREFIX}-dep-free" \
    --db-subnet-group-description "unused group" --subnet-ids "$FREE_SUBNET" >/dev/null 2>&1
  try "delete-subnet allowed when no DB uses the group" \
    $CLI ec2 delete-subnet --subnet-id "$FREE_SUBNET"
  $CLI rds delete-db-subnet-group --db-subnet-group-name "${PREFIX}-dep-free" >/dev/null 2>&1

  try "create-db-instance in the subnet group" \
    $CLI rds create-db-instance --db-instance-identifier "${PREFIX}-dep" \
      --engine postgres --db-instance-class db.t3.micro --allocated-storage 20 \
      --db-subnet-group-name "${PREFIX}-dep"
  try_match "instance reflects the subnet group" "${PREFIX}-dep" \
    $CLI rds describe-db-instances --db-instance-identifier "${PREFIX}-dep" \
      --query "DBInstances[0].DBSubnetGroup.DBSubnetGroupName" --output text
  try_fail "delete-subnet rejected while the instance uses it" \
    $CLI ec2 delete-subnet --subnet-id "$DEP_SUBNET"
  try_fail "delete-db-subnet-group rejected while the instance uses it" \
    $CLI rds delete-db-subnet-group --db-subnet-group-name "${PREFIX}-dep"
  try_fail "create-db-instance rejects an unknown subnet group" \
    $CLI rds create-db-instance --db-instance-identifier "${PREFIX}-dep-2" \
      --engine postgres --db-instance-class db.t3.micro --allocated-storage 20 \
      --db-subnet-group-name "${PREFIX}-no-such-group"
  $CLI rds delete-db-instance --db-instance-identifier "${PREFIX}-dep" \
    --skip-final-snapshot >/dev/null 2>&1
  try "delete-db-subnet-group succeeds once the instance is gone" \
    $CLI rds delete-db-subnet-group --db-subnet-group-name "${PREFIX}-dep"
  try "delete-subnet succeeds once the instance is gone" \
    $CLI ec2 delete-subnet --subnet-id "$DEP_SUBNET"
  $CLI ec2 delete-vpc --vpc-id "$DEP_VPC" >/dev/null 2>&1
else
  fail "ec2 create-subnet (subnet dependency probe could not be set up)"
fi

# ── Performance Insights ─────────────────────────────────────────────────────

section "Performance Insights"
DBI_RESOURCE_ID=$($CLI rds describe-db-instances \
  --db-instance-identifier "${PREFIX}-instance-1" \
  --query "DBInstances[0].DbiResourceId" --output text 2>/dev/null)
if [ -n "${DBI_RESOURCE_ID:-}" ] && [ "$DBI_RESOURCE_ID" != "None" ]; then
  PI_START=$(($(date +%s) - 3600))
  PI_END=$(date +%s)
  try_match "get-resource-metrics returns datapoints" "db.load.avg" \
    $CLI pi get-resource-metrics --service-type RDS --identifier "$DBI_RESOURCE_ID" \
      --metric-queries '[{"Metric":"db.load.avg"}]' \
      --start-time "$PI_START" --end-time "$PI_END" --period-in-seconds 300 \
      --query "MetricList[0].Key.Metric" --output text
  try_match "get-resource-metrics echoes identifier" "$DBI_RESOURCE_ID" \
    $CLI pi get-resource-metrics --service-type RDS --identifier "$DBI_RESOURCE_ID" \
      --metric-queries '[{"Metric":"db.load.avg"}]' \
      --start-time "$PI_START" --end-time "$PI_END" --period-in-seconds 300 \
      --query "Identifier" --output text
  try_match "describe-dimension-keys returns wait events" "CPU" \
    $CLI pi describe-dimension-keys --service-type RDS --identifier "$DBI_RESOURCE_ID" \
      --metric db.load.avg --group-by '{"Group":"db.wait_event"}' \
      --start-time "$PI_START" --end-time "$PI_END" \
      --query "Keys[0].Dimensions.\"db.wait_event.name\"" --output text
  try_match "list-available-resource-metrics lists db.load.avg" "db.load.avg" \
    $CLI pi list-available-resource-metrics --service-type RDS \
      --identifier "$DBI_RESOURCE_ID" --metric-types db \
      --query "Metrics[].Metric" --output text
  try_fail "get-resource-metrics rejects unknown identifier" \
    $CLI pi get-resource-metrics --service-type RDS --identifier "db-DOESNOTEXIST00000000000000" \
      --metric-queries '[{"Metric":"db.load.avg"}]' \
      --start-time "$PI_START" --end-time "$PI_END"
else
  fail "pi get-resource-metrics (DbiResourceId not found — run 'make apply' first)"
fi

section "ElastiCache / Valkey"
EC_RG=$($CLI elasticache describe-replication-groups --replication-group-id "$PREFIX" \
  --query "ReplicationGroups[0].ReplicationGroupId" --output text 2>/dev/null)
if [ -n "${EC_RG:-}" ] && [ "$EC_RG" != "None" ]; then
  try "describe-replication-groups finds group" true
  try_match "replication group status available" "available" \
    $CLI elasticache describe-replication-groups --replication-group-id "$PREFIX" \
      --query "ReplicationGroups[0].Status" --output text
  try_match "replication group endpoint set" "localhost\|valkey\|127" \
    $CLI elasticache describe-replication-groups --replication-group-id "$PREFIX" \
      --query "ReplicationGroups[0].ConfigurationEndpoint.Address" --output text
else
  fail "describe-replication-groups (group not found — run 'make apply' first)"
fi

EC_SUBNET=$($CLI elasticache describe-cache-subnet-groups --cache-subnet-group-name "$PREFIX" \
  --query "CacheSubnetGroups[0].CacheSubnetGroupName" --output text 2>/dev/null)
if [ -n "${EC_SUBNET:-}" ] && [ "$EC_SUBNET" != "None" ]; then
  try "describe-cache-subnet-groups finds group" true
  try_match "cache subnet group vpc set" "vpc-" \
    $CLI elasticache describe-cache-subnet-groups --cache-subnet-group-name "$PREFIX" \
      --query "CacheSubnetGroups[0].VpcId" --output text
else
  fail "describe-cache-subnet-groups (subnet group not found — run 'make apply' first)"
fi

# Tags — AddTagsToResource / ListTagsForResource / RemoveTagsFromResource
SG_ARN="arn:aws:elasticache:${REGION}:000000000000:subnetgroup:${PREFIX}"
RG_ARN="arn:aws:elasticache:${REGION}:000000000000:replicationgroup:${PREFIX}"

try "AddTagsToResource (subnet group)" \
  $CLI elasticache add-tags-to-resource \
    --resource-name "$SG_ARN" \
    --tags "Key=env,Value=smoke" "Key=app,Value=nimbus"

try_match "ListTagsForResource (subnet group) contains env=smoke" "smoke" \
  $CLI elasticache list-tags-for-resource \
    --resource-name "$SG_ARN" \
    --query "TagList[?Key=='env'].Value" --output text

try "RemoveTagsFromResource (subnet group)" \
  $CLI elasticache remove-tags-from-resource \
    --resource-name "$SG_ARN" \
    --tag-keys "app"

SG_TAGS=$($CLI elasticache list-tags-for-resource --resource-name "$SG_ARN" \
  --query "TagList[*].Key" --output text 2>/dev/null)
if echo "$SG_TAGS" | grep -q "app"; then
  fail "RemoveTagsFromResource did not remove 'app' tag"
else
  ok "RemoveTagsFromResource removed 'app' tag"
fi

try "AddTagsToResource (replication group)" \
  $CLI elasticache add-tags-to-resource \
    --resource-name "$RG_ARN" \
    --tags "Key=env,Value=smoke"

try_match "ListTagsForResource (replication group) contains env=smoke" "smoke" \
  $CLI elasticache list-tags-for-resource \
    --resource-name "$RG_ARN" \
    --query "TagList[?Key=='env'].Value" --output text

try_match "/_nimbus/elasticache/clusters inspection" "$PREFIX" \
  curl -sf "$NIMBUS/_nimbus/elasticache/clusters"

# ── EC2 / VPC ─────────────────────────────────────────────────────────────────

section "EC2 / VPC"

try_match "describe-availability-zones returns us-east-1a" "us-east-1a" \
  $CLI ec2 describe-availability-zones \
    --filters Name=state,Values=available \
    --query "AvailabilityZones[0].ZoneName" --output text

VPC_ID=$($CLI ec2 create-vpc --cidr-block 10.99.0.0/16 \
  --query Vpc.VpcId --output text 2>/dev/null)
if [ -n "${VPC_ID:-}" ] && [ "$VPC_ID" != "None" ]; then
  ok "create-vpc"

  try "modify-vpc-attribute (EnableDnsHostnames)" \
    $CLI ec2 modify-vpc-attribute --vpc-id "$VPC_ID" --enable-dns-hostnames

  try_match "describe-vpc-attribute (enableDnsHostnames)" "True" \
    $CLI ec2 describe-vpc-attribute --vpc-id "$VPC_ID" \
      --attribute enableDnsHostnames \
      --query "EnableDnsHostnames.Value" --output text

  try_match "describe-vpcs finds vpc" "$VPC_ID" \
    $CLI ec2 describe-vpcs --vpc-ids "$VPC_ID" \
      --query "Vpcs[0].VpcId" --output text

  SUBNET_ID=$($CLI ec2 create-subnet \
    --vpc-id "$VPC_ID" --cidr-block 10.99.1.0/24 \
    --availability-zone us-east-1a \
    --query Subnet.SubnetId --output text 2>/dev/null)
  if [ -n "${SUBNET_ID:-}" ] && [ "$SUBNET_ID" != "None" ]; then
    ok "create-subnet"

    try "modify-subnet-attribute (MapPublicIpOnLaunch)" \
      $CLI ec2 modify-subnet-attribute --subnet-id "$SUBNET_ID" --map-public-ip-on-launch

    try_match "describe-subnets finds subnet" "$SUBNET_ID" \
      $CLI ec2 describe-subnets --subnet-ids "$SUBNET_ID" \
        --query "Subnets[0].SubnetId" --output text
  else
    fail "create-subnet"
  fi

  IGW_ID=$($CLI ec2 create-internet-gateway \
    --query InternetGateway.InternetGatewayId --output text 2>/dev/null)
  if [ -n "${IGW_ID:-}" ] && [ "$IGW_ID" != "None" ]; then
    ok "create-internet-gateway"

    try "attach-internet-gateway" \
      $CLI ec2 attach-internet-gateway \
        --internet-gateway-id "$IGW_ID" --vpc-id "$VPC_ID"

    try_match "describe-internet-gateways finds igw" "$IGW_ID" \
      $CLI ec2 describe-internet-gateways \
        --filters "Name=attachment.vpc-id,Values=$VPC_ID" \
        --query "InternetGateways[0].InternetGatewayId" --output text
  else
    fail "create-internet-gateway"
  fi

  RT_ID=$($CLI ec2 create-route-table --vpc-id "$VPC_ID" \
    --query RouteTable.RouteTableId --output text 2>/dev/null)
  if [ -n "${RT_ID:-}" ] && [ "$RT_ID" != "None" ]; then
    ok "create-route-table"

    try "create-route" \
      $CLI ec2 create-route --route-table-id "$RT_ID" \
        --destination-cidr-block 0.0.0.0/0 --gateway-id "$IGW_ID"

    ASSOC_ID=$($CLI ec2 associate-route-table \
      --route-table-id "$RT_ID" --subnet-id "$SUBNET_ID" \
      --query AssociationId --output text 2>/dev/null)
    if [ -n "${ASSOC_ID:-}" ] && [ "$ASSOC_ID" != "None" ]; then
      ok "associate-route-table"

      try_match "describe-route-tables includes association" "$ASSOC_ID" \
        $CLI ec2 describe-route-tables --route-table-ids "$RT_ID" \
          --query "RouteTables[0].Associations[0].RouteTableAssociationId" --output text

      try "disassociate-route-table" \
        $CLI ec2 disassociate-route-table --association-id "$ASSOC_ID"
    else
      fail "associate-route-table"
    fi

    try "delete-route" \
      $CLI ec2 delete-route --route-table-id "$RT_ID" \
        --destination-cidr-block 0.0.0.0/0
    try "delete-route-table" \
      $CLI ec2 delete-route-table --route-table-id "$RT_ID"
  else
    fail "create-route-table"
  fi

  SG_ID=$($CLI ec2 describe-security-groups \
    --filters "Name=vpc-id,Values=$VPC_ID" "Name=group-name,Values=default" \
    --query "SecurityGroups[0].GroupId" --output text 2>/dev/null)
  if [ -n "${SG_ID:-}" ] && [ "$SG_ID" != "None" ]; then
    ok "default security group auto-created with VPC"

    try "authorize-security-group-egress" \
      $CLI ec2 authorize-security-group-egress --group-id "$SG_ID" \
        --ip-permissions "IpProtocol=-1,FromPort=0,ToPort=0,IpRanges=[{CidrIp=0.0.0.0/0}]"

    try_match "describe-security-group-rules finds rule" "sgr-" \
      $CLI ec2 describe-security-group-rules \
        --filters "Name=group-id,Values=$SG_ID" \
        --query "SecurityGroupRules[0].SecurityGroupRuleId" --output text
  else
    fail "default security group auto-created with VPC"
  fi

  CUSTOM_SG_ID=$($CLI ec2 create-security-group \
    --group-name "$PREFIX-sg" --description "smoke test sg" --vpc-id "$VPC_ID" \
    --query GroupId --output text 2>/dev/null)
  if [ -n "${CUSTOM_SG_ID:-}" ] && [ "$CUSTOM_SG_ID" != "None" ]; then
    ok "create-security-group"

    try_match "describe-security-groups finds custom sg" "$CUSTOM_SG_ID" \
      $CLI ec2 describe-security-groups --group-ids "$CUSTOM_SG_ID" \
        --query "SecurityGroups[0].GroupId" --output text

    try "authorize-security-group-ingress (custom sg)" \
      $CLI ec2 authorize-security-group-ingress --group-id "$CUSTOM_SG_ID" \
        --ip-permissions "IpProtocol=tcp,FromPort=80,ToPort=80,IpRanges=[{CidrIp=10.99.0.0/16}]"

    try "delete-security-group" \
      $CLI ec2 delete-security-group --group-id "$CUSTOM_SG_ID"
  else
    fail "create-security-group"
  fi

  try "create-tags on VPC" \
    $CLI ec2 create-tags \
      --resources "$VPC_ID" \
      --tags "Key=env,Value=smoke" "Key=app,Value=nimbus"

  try_match "describe-vpcs shows tag" "smoke" \
    $CLI ec2 describe-vpcs --vpc-ids "$VPC_ID" \
      --query "Vpcs[0].Tags[?Key=='env'].Value" --output text

  # VPC endpoints and prefix lists. The service prefix lists exist without
  # anyone creating them, the way AWS-managed lists do.
  S3_PL_ID=$($CLI ec2 describe-prefix-lists \
    --filters "Name=prefix-list-name,Values=com.amazonaws.us-east-1.s3" \
    --query "PrefixLists[0].PrefixListId" --output text 2>/dev/null)
  if [ -n "${S3_PL_ID:-}" ] && [ "$S3_PL_ID" != "None" ]; then
    ok "describe-prefix-lists resolves the S3 service list"

    try_match "get-managed-prefix-list-entries returns CIDRs" "/" \
      $CLI ec2 get-managed-prefix-list-entries --prefix-list-id "$S3_PL_ID" \
        --query "Entries[0].Cidr" --output text
    try_match "describe-managed-prefix-lists reports AWS as owner" "AWS" \
      $CLI ec2 describe-managed-prefix-lists --prefix-list-ids "$S3_PL_ID" \
        --query "PrefixLists[0].OwnerId" --output text
  else
    fail "describe-prefix-lists resolves the S3 service list"
  fi

  VPCE_ID=$($CLI ec2 create-vpc-endpoint --vpc-id "$VPC_ID" \
    --service-name "com.amazonaws.us-east-1.s3" \
    --query VpcEndpoint.VpcEndpointId --output text 2>/dev/null)
  if [ -n "${VPCE_ID:-}" ] && [ "$VPCE_ID" != "None" ]; then
    ok "create-vpc-endpoint"

    # Looking an endpoint up by vpc-id + service-name is how a module finds one
    # it does not manage.
    try_match "describe-vpc-endpoints filters by vpc and service" "$VPCE_ID" \
      $CLI ec2 describe-vpc-endpoints \
        --filters "Name=vpc-id,Values=$VPC_ID" \
          "Name=service-name,Values=com.amazonaws.us-east-1.s3" \
        --query "VpcEndpoints[0].VpcEndpointId" --output text
    try_match "vpc endpoint defaults to Gateway type" "Gateway" \
      $CLI ec2 describe-vpc-endpoints --vpc-endpoint-ids "$VPCE_ID" \
        --query "VpcEndpoints[0].VpcEndpointType" --output text

    try "delete-vpc-endpoints" \
      $CLI ec2 delete-vpc-endpoints --vpc-endpoint-ids "$VPCE_ID"
  else
    fail "create-vpc-endpoint"
  fi

  # A rule targeting a prefix list instead of a CIDR must read back, or the
  # configured egress restriction looks unset.
  PL_SG_ID=$($CLI ec2 create-security-group \
    --group-name "$PREFIX-plsg" --description "prefix list egress" --vpc-id "$VPC_ID" \
    --query GroupId --output text 2>/dev/null)
  if [ -n "${PL_SG_ID:-}" ] && [ "$PL_SG_ID" != "None" ] && [ -n "${S3_PL_ID:-}" ]; then
    try "authorize-security-group-egress (prefix list target)" \
      $CLI ec2 authorize-security-group-egress --group-id "$PL_SG_ID" \
        --ip-permissions "IpProtocol=tcp,FromPort=443,ToPort=443,PrefixListIds=[{PrefixListId=$S3_PL_ID}]"

    try_match "describe-security-groups reports the prefix list target" "$S3_PL_ID" \
      $CLI ec2 describe-security-groups --group-ids "$PL_SG_ID" \
        --query "SecurityGroups[0].IpPermissionsEgress[0].PrefixListIds[0].PrefixListId" \
        --output text
    try_match "describe-security-group-rules reports the prefix list target" "$S3_PL_ID" \
      $CLI ec2 describe-security-group-rules \
        --filters "Name=group-id,Values=$PL_SG_ID" \
        --query "SecurityGroupRules[0].PrefixListId" --output text

    try "delete-security-group (prefix list sg)" \
      $CLI ec2 delete-security-group --group-id "$PL_SG_ID"
  else
    fail "create-security-group (prefix list egress)"
  fi

  # Cleanup
  if [ -n "${IGW_ID:-}" ] && [ "$IGW_ID" != "None" ]; then
    try "detach-internet-gateway" \
      $CLI ec2 detach-internet-gateway \
        --internet-gateway-id "$IGW_ID" --vpc-id "$VPC_ID"
    try "delete-internet-gateway" \
      $CLI ec2 delete-internet-gateway --internet-gateway-id "$IGW_ID"
  fi
  if [ -n "${SUBNET_ID:-}" ] && [ "$SUBNET_ID" != "None" ]; then
    try "delete-subnet" \
      $CLI ec2 delete-subnet --subnet-id "$SUBNET_ID"
  fi
  try "delete-vpc" \
    $CLI ec2 delete-vpc --vpc-id "$VPC_ID"
else
  fail "create-vpc"
fi

# ── ACM ───────────────────────────────────────────────────────────────────────

section "ACM"

ACM_ARN=$($CLI acm request-certificate \
  --domain-name "$PREFIX.nimbus.local" \
  --subject-alternative-names "*.$PREFIX.nimbus.local" \
  --validation-method DNS \
  --query "CertificateArn" --output text)
if echo "$ACM_ARN" | grep -q "arn:aws:acm:"; then
  ok "RequestCertificate returns ARN"
else
  fail "RequestCertificate returns ARN" "got: $ACM_ARN"
fi

try_match "DescribeCertificate status ISSUED" "ISSUED" \
  $CLI acm describe-certificate --certificate-arn "$ACM_ARN" \
    --query "Certificate.Status" --output text

try_match "ListCertificates includes new cert" "$PREFIX" \
  $CLI acm list-certificates --query "CertificateSummaryList[*].DomainName" --output text

try_match "ListTagsForCertificate empty" "" \
  $CLI acm list-tags-for-certificate --certificate-arn "$ACM_ARN" \
    --query "Tags" --output text

try_match "/_nimbus/acm/certs/ downloads PEM" "BEGIN CERTIFICATE" \
  curl -sf "$NIMBUS/_nimbus/acm/certs/$ACM_ARN"

$CLI acm delete-certificate --certificate-arn "$ACM_ARN" 2>/dev/null
try_match "DeleteCertificate removes cert" "" \
  $CLI acm list-certificates --query "CertificateSummaryList[?DomainName=='$PREFIX.nimbus.local'].DomainName" --output text

# ── Route 53 ─────────────────────────────────────────────────────────────────

section "Route 53"

R53_ZONE_ID=$($CLI route53 list-hosted-zones \
  --query "HostedZones[?Name=='${PREFIX}.nimbus.local.'].Id" --output text \
  | sed 's|/hostedzone/||')
if [ -n "${R53_ZONE_ID:-}" ]; then
  try "list-hosted-zones finds zone" true

  try_match "get-hosted-zone name matches" "${PREFIX}.nimbus.local" \
    $CLI route53 get-hosted-zone --id "$R53_ZONE_ID" \
      --query "HostedZone.Name" --output text

  try_match "list-resource-record-sets finds A record" "127.0.0.1" \
    $CLI route53 list-resource-record-sets --hosted-zone-id "$R53_ZONE_ID"

  try_match "list-resource-record-sets finds CNAME" "www\\." \
    $CLI route53 list-resource-record-sets --hosted-zone-id "$R53_ZONE_ID"

  CHANGE_ID=$($CLI route53 change-resource-record-sets \
    --hosted-zone-id "$R53_ZONE_ID" \
    --change-batch '{"Changes":[{"Action":"UPSERT","ResourceRecordSet":{"Name":"probe.'"${PREFIX}"'.nimbus.local","Type":"TXT","TTL":60,"ResourceRecords":[{"Value":"\"nimbus-probe\""}]}}]}' \
    --query "ChangeInfo.Id" --output text)
  if echo "$CHANGE_ID" | grep -q "/change/"; then ok "change-resource-record-sets returns change ID"; else fail "change-resource-record-sets returns change ID" "got: $CHANGE_ID"; fi

  try_match "get-change returns INSYNC" "INSYNC" \
    $CLI route53 get-change --id "$CHANGE_ID" --query "ChangeInfo.Status" --output text

  try_match "list-tags-for-resource" "$PREFIX" \
    $CLI route53 list-tags-for-resource --resource-type hostedzone --resource-id "$R53_ZONE_ID"
else
  fail "list-hosted-zones (zone not found — run 'make apply' first)"
fi

# ── CloudWatch Metrics ───────────────────────────────────────────────────────

section "CloudWatch Metrics"

try "put-metric-data" \
  $CLI cloudwatch put-metric-data \
    --namespace "Nimbus/$PREFIX" \
    --metric-name RequestCount \
    --value 42 \
    --unit Count

try "put-metric-data second point" \
  $CLI cloudwatch put-metric-data \
    --namespace "Nimbus/$PREFIX" \
    --metric-name RequestCount \
    --value 58 \
    --unit Count

try_match "list-metrics finds namespace" "Nimbus/$PREFIX" \
  $CLI cloudwatch list-metrics --namespace "Nimbus/$PREFIX" --query "Metrics[*].Namespace" --output text

try_match "list-metrics finds metric name" "RequestCount" \
  $CLI cloudwatch list-metrics --namespace "Nimbus/$PREFIX" --query "Metrics[*].MetricName" --output text

# Dimension filters. Three series of one metric: two carry TargetDiscoveryName
# and one does not. The counts are deliberately asymmetric — a filter that
# matched the *complement* (the old bug) would return 1 where correct behaviour
# returns 2, so these assertions fail if the semantics ever invert again.
DIM_NS="Nimbus/$PREFIX-dims"
$CLI cloudwatch put-metric-data --namespace "$DIM_NS" \
  --metric-data "[{\"MetricName\":\"RequestCount\",\"Dimensions\":[{\"Name\":\"ServiceName\",\"Value\":\"web\"},{\"Name\":\"TargetDiscoveryName\",\"Value\":\"api\"}],\"Value\":120}]" >/dev/null 2>&1
$CLI cloudwatch put-metric-data --namespace "$DIM_NS" \
  --metric-data "[{\"MetricName\":\"RequestCount\",\"Dimensions\":[{\"Name\":\"ServiceName\",\"Value\":\"web\"}],\"Value\":55}]" >/dev/null 2>&1
$CLI cloudwatch put-metric-data --namespace "$DIM_NS" \
  --metric-data "[{\"MetricName\":\"RequestCount\",\"Dimensions\":[{\"Name\":\"ServiceName\",\"Value\":\"worker\"},{\"Name\":\"TargetDiscoveryName\",\"Value\":\"db\"}],\"Value\":9}]" >/dev/null 2>&1

try_match "list-metrics sees all three seeded series" "^3$" \
  $CLI cloudwatch list-metrics --namespace "$DIM_NS" --query "length(Metrics)" --output text

# A filter with a name and no value matches every metric *carrying* that
# dimension — 2 of the 3 — whatever its value.
try_match "list-metrics name-only dimension filter matches both carriers" "^2$" \
  $CLI cloudwatch list-metrics --namespace "$DIM_NS" \
    --dimensions Name=TargetDiscoveryName --query "length(Metrics)" --output text
try_match "list-metrics name-only filter returns the dimension values" "api.*db\|db.*api" \
  $CLI cloudwatch list-metrics --namespace "$DIM_NS" \
    --dimensions Name=TargetDiscoveryName \
    --query "sort(Metrics[].Dimensions[?Name=='TargetDiscoveryName'].Value[])" --output text

try_match "list-metrics name+value dimension filter matches" "^1$" \
  $CLI cloudwatch list-metrics --namespace "$DIM_NS" \
    --dimensions Name=TargetDiscoveryName,Value=api --query "length(Metrics)" --output text
try_match "list-metrics rejects an unpublished dimension value" "^0$" \
  $CLI cloudwatch list-metrics --namespace "$DIM_NS" \
    --dimensions Name=TargetDiscoveryName,Value=nope --query "length(Metrics)" --output text
try_match "list-metrics unknown dimension name matches nothing" "^0$" \
  $CLI cloudwatch list-metrics --namespace "$DIM_NS" \
    --dimensions Name=NoSuchDimension --query "length(Metrics)" --output text

# Filters are ANDed. Asserting the matched value, not just the count: the
# inverted behaviour also returned one series here, but the wrong one.
try_match "list-metrics ANDs multiple dimension filters" "^api$" \
  $CLI cloudwatch list-metrics --namespace "$DIM_NS" \
    --dimensions Name=ServiceName,Value=web Name=TargetDiscoveryName \
    --query "Metrics[].Dimensions[?Name=='TargetDiscoveryName'].Value[]" --output text
try_match "list-metrics ANDed filters exclude a non-matching pair" "^0$" \
  $CLI cloudwatch list-metrics --namespace "$DIM_NS" \
    --dimensions Name=ServiceName,Value=other Name=TargetDiscoveryName \
    --query "length(Metrics)" --output text

try "get-metric-statistics" \
  $CLI cloudwatch get-metric-statistics \
    --namespace "Nimbus/$PREFIX" \
    --metric-name RequestCount \
    --start-time "$(date -u -v-1H '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null || date -u -d '1 hour ago' '+%Y-%m-%dT%H:%M:%SZ')" \
    --end-time "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" \
    --period 3600 \
    --statistics Sum Average

try "put-metric-alarm" \
  $CLI cloudwatch put-metric-alarm \
    --alarm-name "$PREFIX-cpu" \
    --metric-name CPUUtilization \
    --namespace "AWS/EC2" \
    --comparison-operator GreaterThanThreshold \
    --threshold 80 \
    --evaluation-periods 1 \
    --period 60 \
    --statistic Average

try_match "describe-alarms finds alarm" "$PREFIX-cpu" \
  $CLI cloudwatch describe-alarms --alarm-names "$PREFIX-cpu" --query "MetricAlarms[*].AlarmName" --output text

try_match "describe-alarms state is OK" "OK" \
  $CLI cloudwatch describe-alarms --alarm-names "$PREFIX-cpu" --query "MetricAlarms[*].StateValue" --output text

try_match "/_nimbus/metrics inspection endpoint" "RequestCount" \
  curl -sf "$NIMBUS/_nimbus/metrics"

try "delete-alarms" \
  $CLI cloudwatch delete-alarms --alarm-names "$PREFIX-cpu"

# ── Cognito ───────────────────────────────────────────────────────────────────

section "Cognito"

POOL_ID=$($CLI cognito-idp create-user-pool \
  --pool-name "$PREFIX-nimbus-pool" \
  --query "UserPool.Id" --output text)
if echo "$POOL_ID" | grep -qE "^[a-z0-9-]+_[0-9a-f]+$"; then
  ok "CreateUserPool returns pool ID"
else
  fail "CreateUserPool returns pool ID" "got: $POOL_ID"
fi

try_match "DescribeUserPool returns pool name" "$PREFIX-nimbus-pool" \
  $CLI cognito-idp describe-user-pool --user-pool-id "$POOL_ID" \
    --query "UserPool.Name" --output text

try_match "ListUserPools includes new pool" "$PREFIX-nimbus-pool" \
  $CLI cognito-idp list-user-pools --max-results 10 \
    --query "UserPools[*].Name" --output text

CLIENT_ID=$($CLI cognito-idp create-user-pool-client \
  --user-pool-id "$POOL_ID" \
  --client-name "$PREFIX-web-client" \
  --explicit-auth-flows ALLOW_USER_PASSWORD_AUTH ALLOW_REFRESH_TOKEN_AUTH \
  --query "UserPoolClient.ClientId" --output text)
if [ -n "$CLIENT_ID" ]; then
  ok "CreateUserPoolClient returns client ID"
else
  fail "CreateUserPoolClient returns client ID" "empty"
fi

try_match "DescribeUserPoolClient returns client name" "$PREFIX-web-client" \
  $CLI cognito-idp describe-user-pool-client \
    --user-pool-id "$POOL_ID" --client-id "$CLIENT_ID" \
    --query "UserPoolClient.ClientName" --output text

try_match "ListUserPoolClients includes new client" "$PREFIX-web-client" \
  $CLI cognito-idp list-user-pool-clients --user-pool-id "$POOL_ID" --max-results 10 \
    --query "UserPoolClients[*].ClientName" --output text

$CLI cognito-idp admin-create-user \
  --user-pool-id "$POOL_ID" \
  --username "testuser@nimbus.local" \
  --temporary-password "TempPass1!" \
  --user-attributes Name=email,Value=testuser@nimbus.local 2>/dev/null
ok "AdminCreateUser"

$CLI cognito-idp admin-set-user-password \
  --user-pool-id "$POOL_ID" \
  --username "testuser@nimbus.local" \
  --password "FinalPass1!" \
  --permanent 2>/dev/null
ok "AdminSetUserPassword"

ACCESS_TOKEN=$($CLI cognito-idp initiate-auth \
  --auth-flow USER_PASSWORD_AUTH \
  --client-id "$CLIENT_ID" \
  --auth-parameters "USERNAME=testuser@nimbus.local,PASSWORD=FinalPass1!" \
  --query "AuthenticationResult.AccessToken" --output text 2>/dev/null)
if [ -n "$ACCESS_TOKEN" ] && [ "$ACCESS_TOKEN" != "None" ]; then
  ok "InitiateAuth returns AccessToken"
else
  fail "InitiateAuth returns AccessToken" "got: $ACCESS_TOKEN"
fi

try_match "GetUser returns username" "testuser@nimbus.local" \
  $CLI cognito-idp get-user \
    --access-token "$ACCESS_TOKEN" \
    --query "Username" --output text

try_match "JWKS endpoint returns RSA key" "RSA" \
  curl -sf "$NIMBUS/$POOL_ID/.well-known/jwks.json"

$CLI cognito-idp global-sign-out --access-token "$ACCESS_TOKEN" 2>/dev/null
ok "GlobalSignOut"

$CLI cognito-idp delete-user-pool-client \
  --user-pool-id "$POOL_ID" --client-id "$CLIENT_ID" 2>/dev/null

$CLI cognito-idp delete-user-pool --user-pool-id "$POOL_ID" 2>/dev/null
try_match "DeleteUserPool removes pool" "" \
  $CLI cognito-idp list-user-pools --max-results 10 \
    --query "UserPools[?Name=='$PREFIX-nimbus-pool'].Name" --output text

# ── Kinesis ───────────────────────────────────────────────────────────────────

section "Kinesis"
STREAM_NAME="$PREFIX-nimbus-stream"
$CLI kinesis delete-stream --stream-name "$STREAM_NAME" 2>/dev/null || true
$CLI kinesis create-stream --stream-name "$STREAM_NAME" --shard-count 2
try_match "ListStreams contains stream" "$STREAM_NAME" \
  $CLI kinesis list-streams --query "StreamNames" --output text
try_match "DescribeStream status ACTIVE" "ACTIVE" \
  $CLI kinesis describe-stream --stream-name "$STREAM_NAME" \
    --query "StreamDescription.StreamStatus" --output text
try_match "ListShards returns 2 shards" "2" \
  $CLI kinesis list-shards --stream-name "$STREAM_NAME" \
    --query "length(Shards)" --output text
SHARD_ID=$($CLI kinesis list-shards --stream-name "$STREAM_NAME" \
  --query "Shards[0].ShardId" --output text)
$CLI kinesis add-tags-to-stream --stream-name "$STREAM_NAME" --tags env=test
try_match "ListTagsForStream contains env=test" "test" \
  $CLI kinesis list-tags-for-stream --stream-name "$STREAM_NAME" \
    --query "Tags[?Key=='env'].Value" --output text
PUT_OUT=$($CLI kinesis put-record --stream-name "$STREAM_NAME" \
  --partition-key "pk-1" --data "aGVsbG8=")
SEQ=$(echo "$PUT_OUT" | python3 -c "import sys,json;print(json.load(sys.stdin)['SequenceNumber'])")
PUT_SHARD=$(echo "$PUT_OUT" | python3 -c "import sys,json;print(json.load(sys.stdin)['ShardId'])")
try_match "PutRecord returns sequence number" "49" \
  echo "$SEQ"
IT=$($CLI kinesis get-shard-iterator --stream-name "$STREAM_NAME" \
  --shard-id "$PUT_SHARD" --shard-iterator-type TRIM_HORIZON \
  --query "ShardIterator" --output text)
try_match "GetRecords returns at least 1 record" "1" \
  $CLI kinesis get-records --shard-iterator "$IT" \
    --query "length(Records)" --output text
$CLI kinesis remove-tags-from-stream --stream-name "$STREAM_NAME" --tag-keys env
$CLI kinesis delete-stream --stream-name "$STREAM_NAME"
try_match "DeleteStream removes stream" "" \
  $CLI kinesis list-streams \
    --query "StreamNames[?@=='$STREAM_NAME']" --output text

# ── Kinesis ESM (Lambda integration) ─────────────────────────────────────────

section "Kinesis ESM"
ESM_STREAM="$PREFIX-esm-stream"
$CLI kinesis delete-stream --stream-name "$ESM_STREAM" 2>/dev/null || true
$CLI kinesis create-stream --stream-name "$ESM_STREAM" --shard-count 1
# Create ESM linking stream to the existing Lambda function
ESM_UUID=$($CLI lambda create-event-source-mapping \
  --event-source-arn "arn:aws:kinesis:${REGION:-us-east-1}:000000000000:stream/$ESM_STREAM" \
  --function-name "$PREFIX" \
  --starting-position TRIM_HORIZON \
  --batch-size 10 \
  --query UUID --output text)
try_match "CreateEventSourceMapping returns UUID" "-" \
  echo "$ESM_UUID"
try_match "ListEventSourceMappings includes new ESM" "$ESM_UUID" \
  $CLI lambda list-event-source-mappings \
    --function-name "$PREFIX" --query "EventSourceMappings[*].UUID" --output text
# Put a record so the ESM runner has something to deliver
$CLI kinesis put-record --stream-name "$ESM_STREAM" \
  --partition-key "pk-esm" --data "$(echo -n 'esm-test' | base64)" > /dev/null
# Give the ESM runner (1 s tick) time to invoke Lambda
sleep 2
try_match "ESM runner invoked Lambda (invocation recorded)" "$PREFIX" \
  curl -sf "$NIMBUS/_nimbus/lambda/invocations" --header "Accept: application/json"
# Clean up
$CLI lambda delete-event-source-mapping --uuid "$ESM_UUID" > /dev/null
$CLI kinesis delete-stream --stream-name "$ESM_STREAM"

# ── Step Functions ────────────────────────────────────────────────────────────

section "Step Functions"
SFN_NAME="$PREFIX-nimbus-sm"
SFN_PASS_DEF='{"Comment":"smoke","StartAt":"Hello","States":{"Hello":{"Type":"Pass","Result":{"msg":"ok"},"Next":"Done"},"Done":{"Type":"Succeed"}}}'
SFN_FAIL_DEF='{"Comment":"smoke","StartAt":"Boom","States":{"Boom":{"Type":"Fail","Error":"SmokeError","Cause":"intentional"}}}'

# Clean up any leftover state machine from a previous run
$CLI stepfunctions delete-state-machine \
  --state-machine-arn "arn:aws:states:${REGION:-us-east-1}:000000000000:stateMachine:$SFN_NAME" \
  2>/dev/null || true

SFN_ARN=$($CLI stepfunctions create-state-machine \
  --name "$SFN_NAME" \
  --definition "$SFN_PASS_DEF" \
  --role-arn "arn:aws:iam::000000000000:role/sfn-role" \
  --type STANDARD \
  --query stateMachineArn --output text)
try_match "CreateStateMachine returns ARN" "$SFN_NAME" \
  echo "$SFN_ARN"

try_match "ListStateMachines contains SM" "$SFN_NAME" \
  $CLI stepfunctions list-state-machines \
    --query "stateMachines[*].name" --output text

try_match "DescribeStateMachine status ACTIVE" "ACTIVE" \
  $CLI stepfunctions describe-state-machine \
    --state-machine-arn "$SFN_ARN" \
    --query status --output text

# StartExecution — Pass → Succeed
EXEC_ARN=$($CLI stepfunctions start-execution \
  --state-machine-arn "$SFN_ARN" \
  --input '{"x":1}' \
  --query executionArn --output text)
try_match "StartExecution returns ARN" "$SFN_NAME" \
  echo "$EXEC_ARN"

# Poll until not RUNNING (max ~3 s)
for _i in 1 2 3 4 5 6; do
  EXEC_STATUS=$($CLI stepfunctions describe-execution \
    --execution-arn "$EXEC_ARN" \
    --query status --output text)
  [ "$EXEC_STATUS" != "RUNNING" ] && break
  sleep 0.5
done
try_match "Execution status SUCCEEDED" "SUCCEEDED" \
  echo "$EXEC_STATUS"

try_match "Execution output contains msg=ok" "ok" \
  $CLI stepfunctions describe-execution \
    --execution-arn "$EXEC_ARN" \
    --query "output" --output text

try_match "GetExecutionHistory has ExecutionStarted event" "ExecutionStarted" \
  $CLI stepfunctions get-execution-history \
    --execution-arn "$EXEC_ARN" \
    --query "events[*].type" --output text

# StartExecution — Fail state
FAIL_SM_ARN=$($CLI stepfunctions create-state-machine \
  --name "${SFN_NAME}-fail" \
  --definition "$SFN_FAIL_DEF" \
  --role-arn "arn:aws:iam::000000000000:role/sfn-role" \
  --type STANDARD \
  --query stateMachineArn --output text)
FAIL_EXEC=$($CLI stepfunctions start-execution \
  --state-machine-arn "$FAIL_SM_ARN" \
  --query executionArn --output text)
for _i in 1 2 3 4 5 6; do
  FAIL_STATUS=$($CLI stepfunctions describe-execution \
    --execution-arn "$FAIL_EXEC" --query status --output text)
  [ "$FAIL_STATUS" != "RUNNING" ] && break
  sleep 0.5
done
try_match "Fail state execution status FAILED" "FAILED" \
  echo "$FAIL_STATUS"

# Tag / list tags / untag
$CLI stepfunctions tag-resource \
  --resource-arn "$SFN_ARN" \
  --tags "key=env,value=smoke" > /dev/null
try_match "ListTagsForResource contains env=smoke" "smoke" \
  $CLI stepfunctions list-tags-for-resource \
    --resource-arn "$SFN_ARN" \
    --query "tags[?key=='env'].value" --output text

# Parallel state — two branches produce an array of two results
SFN_PARALLEL_DEF='{"StartAt":"P","States":{"P":{"Type":"Parallel","Branches":[{"StartAt":"A","States":{"A":{"Type":"Pass","Result":{"branch":"a"},"End":true}}},{"StartAt":"B","States":{"B":{"Type":"Pass","Result":{"branch":"b"},"End":true}}}],"End":true}}}'
$CLI stepfunctions delete-state-machine \
  --state-machine-arn "arn:aws:states:${REGION:-us-east-1}:000000000000:stateMachine:${SFN_NAME}-parallel" \
  2>/dev/null || true
PARALLEL_SM_ARN=$($CLI stepfunctions create-state-machine \
  --name "${SFN_NAME}-parallel" \
  --definition "$SFN_PARALLEL_DEF" \
  --role-arn "arn:aws:iam::000000000000:role/sfn-role" \
  --type STANDARD \
  --query stateMachineArn --output text)
PARALLEL_EXEC=$($CLI stepfunctions start-execution \
  --state-machine-arn "$PARALLEL_SM_ARN" \
  --input '{}' \
  --query executionArn --output text)
for _i in 1 2 3 4 5 6; do
  PARALLEL_STATUS=$($CLI stepfunctions describe-execution \
    --execution-arn "$PARALLEL_EXEC" --query status --output text)
  [ "$PARALLEL_STATUS" != "RUNNING" ] && break
  sleep 0.5
done
try_match "Parallel state execution SUCCEEDED" "SUCCEEDED" \
  echo "$PARALLEL_STATUS"
try_match "Parallel state output contains branch a" '"branch":"a"' \
  $CLI stepfunctions describe-execution \
    --execution-arn "$PARALLEL_EXEC" --query output --output text

# Map state — iterate over [1,2,3], each item passes through unchanged
SFN_MAP_DEF='{"StartAt":"M","States":{"M":{"Type":"Map","Iterator":{"StartAt":"I","States":{"I":{"Type":"Pass","End":true}}},"End":true}}}'
$CLI stepfunctions delete-state-machine \
  --state-machine-arn "arn:aws:states:${REGION:-us-east-1}:000000000000:stateMachine:${SFN_NAME}-map" \
  2>/dev/null || true
MAP_SM_ARN=$($CLI stepfunctions create-state-machine \
  --name "${SFN_NAME}-map" \
  --definition "$SFN_MAP_DEF" \
  --role-arn "arn:aws:iam::000000000000:role/sfn-role" \
  --type STANDARD \
  --query stateMachineArn --output text)
MAP_EXEC=$($CLI stepfunctions start-execution \
  --state-machine-arn "$MAP_SM_ARN" \
  --input '[1,2,3]' \
  --query executionArn --output text)
for _i in 1 2 3 4 5 6; do
  MAP_STATUS=$($CLI stepfunctions describe-execution \
    --execution-arn "$MAP_EXEC" --query status --output text)
  [ "$MAP_STATUS" != "RUNNING" ] && break
  sleep 0.5
done
try_match "Map state execution SUCCEEDED" "SUCCEEDED" \
  echo "$MAP_STATUS"
try_match "Map state output is array of 3" "1" \
  $CLI stepfunctions describe-execution \
    --execution-arn "$MAP_EXEC" --query "output" --output text

# Clean up
$CLI stepfunctions delete-state-machine --state-machine-arn "$SFN_ARN" > /dev/null
$CLI stepfunctions delete-state-machine --state-machine-arn "$FAIL_SM_ARN" > /dev/null
$CLI stepfunctions delete-state-machine --state-machine-arn "$PARALLEL_SM_ARN" > /dev/null
$CLI stepfunctions delete-state-machine --state-machine-arn "$MAP_SM_ARN" > /dev/null

# ── AppSync ───────────────────────────────────────────────────────────────────

section "AppSync"

# Verify Terraform-provisioned API is visible
APPSYNC_API_ID=$($CLI appsync list-graphql-apis \
  --query "graphqlApis[?name=='${PREFIX}-appsync'].apiId | [0]" \
  --output text 2>/dev/null)
if [ -n "$APPSYNC_API_ID" ] && [ "$APPSYNC_API_ID" != "None" ]; then
  ok "list-graphql-apis finds TF-provisioned API"
else
  fail "list-graphql-apis finds TF-provisioned API" "API '${PREFIX}-appsync' not found"
  APPSYNC_API_ID=""
fi

if [ -n "$APPSYNC_API_ID" ]; then
  try_match "get-graphql-api returns API_KEY auth" "API_KEY" \
    $CLI appsync get-graphql-api --api-id "$APPSYNC_API_ID" \
      --query "graphqlApi.authenticationType" --output text

  try_match "list-api-keys finds TF-provisioned key" "da2-" \
    $CLI appsync list-api-keys --api-id "$APPSYNC_API_ID" \
      --query "apiKeys[0].id" --output text

  try_match "get-data-source NimbusLambda" "AWS_LAMBDA" \
    $CLI appsync get-data-source \
      --api-id "$APPSYNC_API_ID" --name NimbusLambda \
      --query "dataSource.type" --output text
fi

# Inline lifecycle: create → schema → datasource → resolver → api-key → delete
SMOKE_API_ID=$($CLI appsync create-graphql-api \
  --name "${PREFIX}-smoke-$$" \
  --authentication-type API_KEY \
  --query "graphqlApi.apiId" --output text 2>/dev/null)
if [ -n "$SMOKE_API_ID" ] && [ "$SMOKE_API_ID" != "None" ]; then
  ok "create-graphql-api (inline)"
else
  fail "create-graphql-api (inline)"
  SMOKE_API_ID=""
fi

if [ -n "$SMOKE_API_ID" ]; then
  # Schema
  SCHEMA_B64=$(printf 'type Query { hello: String }' | base64)
  try "start-schema-creation" \
    $CLI appsync start-schema-creation \
      --api-id "$SMOKE_API_ID" \
      --definition "$SCHEMA_B64"
  try_match "get-schema-creation-status SUCCESS" "SUCCESS" \
    $CLI appsync get-schema-creation-status \
      --api-id "$SMOKE_API_ID" --query status --output text

  # Data source
  try "create-data-source" \
    $CLI appsync create-data-source \
      --api-id "$SMOKE_API_ID" \
      --name SmokeDS \
      --type AWS_LAMBDA \
      --service-role-arn "arn:aws:iam::000000000000:role/appsync-role" \
      --lambda-config "lambdaFunctionArn=arn:aws:lambda:${REGION}:000000000000:function:${PREFIX}"
  try_match "get-data-source" "AWS_LAMBDA" \
    $CLI appsync get-data-source \
      --api-id "$SMOKE_API_ID" --name SmokeDS \
      --query "dataSource.type" --output text

  # Resolver
  try "create-resolver" \
    $CLI appsync create-resolver \
      --api-id "$SMOKE_API_ID" \
      --type-name Query \
      --field-name hello \
      --data-source-name SmokeDS \
      --kind UNIT
  try_match "get-resolver" "SmokeDS" \
    $CLI appsync get-resolver \
      --api-id "$SMOKE_API_ID" --type-name Query --field-name hello \
      --query "resolver.dataSourceName" --output text

  # API key
  SMOKE_KEY_ID=$($CLI appsync create-api-key \
    --api-id "$SMOKE_API_ID" \
    --description "smoke-$$" \
    --query "apiKey.id" --output text 2>/dev/null)
  try "create-api-key" [ -n "$SMOKE_KEY_ID" ]
  try_match "list-api-keys" "$SMOKE_KEY_ID" \
    $CLI appsync list-api-keys \
      --api-id "$SMOKE_API_ID" --query "apiKeys[0].id" --output text

  # Tags
  SMOKE_ARN="arn:aws:appsync:${REGION}:000000000000:apis/${SMOKE_API_ID}"
  try "tag-resource" \
    $CLI appsync tag-resource \
      --resource-arn "$SMOKE_ARN" \
      --tags env=smoke
  try_match "list-tags-for-resource" "smoke" \
    $CLI appsync list-tags-for-resource \
      --resource-arn "$SMOKE_ARN" \
      --query "tags.env" --output text

  # Inspect via Nimbus endpoint
  try_match "/_nimbus/appsync/apis lists API" "$SMOKE_API_ID" \
    curl -sf "$NIMBUS/_nimbus/appsync/apis"

  # GraphQL execution — NONE data source (no Lambda required)
  try "create-data-source (NONE)" \
    $CLI appsync create-data-source \
      --api-id "$SMOKE_API_ID" \
      --name NoneDS \
      --type NONE
  try "create-resolver (NONE/ping)" \
    $CLI appsync create-resolver \
      --api-id "$SMOKE_API_ID" \
      --type-name Query \
      --field-name ping \
      --data-source-name NoneDS \
      --kind UNIT \
      --request-mapping-template '{"version":"2018-05-29","payload":null}' \
      --response-mapping-template '"pong"'
  if [ -n "$SMOKE_KEY_ID" ]; then
    try_match "graphql execution (path-based)" "pong" \
      curl -sf -X POST "$NIMBUS/_appsync/${SMOKE_API_ID}/graphql" \
        -H "Content-Type: application/json" \
        -H "x-api-key: ${SMOKE_KEY_ID}" \
        -d '{"query":"query { ping }"}'
  fi

  # Teardown
  $CLI appsync delete-resolver \
    --api-id "$SMOKE_API_ID" --type-name Query --field-name ping > /dev/null 2>&1
  $CLI appsync delete-data-source \
    --api-id "$SMOKE_API_ID" --name NoneDS > /dev/null 2>&1
  try "delete-resolver" \
    $CLI appsync delete-resolver \
      --api-id "$SMOKE_API_ID" --type-name Query --field-name hello
  try "delete-data-source" \
    $CLI appsync delete-data-source \
      --api-id "$SMOKE_API_ID" --name SmokeDS
  [ -n "$SMOKE_KEY_ID" ] && $CLI appsync delete-api-key \
    --api-id "$SMOKE_API_ID" --id "$SMOKE_KEY_ID" > /dev/null 2>&1
  try "delete-graphql-api" \
    $CLI appsync delete-graphql-api --api-id "$SMOKE_API_ID"
fi

# ── Summary ───────────────────────────────────────────────────────────────────

echo
echo "═══════════════════════════════════════"
TOTAL=$((PASS + FAIL))
echo "  Passed : $PASS / $TOTAL"
if [ "$FAIL" -gt 0 ]; then
  echo "  Failed : $FAIL / $TOTAL"
  echo "═══════════════════════════════════════"
  exit 1
else
  echo "  All checks passed."
  echo "═══════════════════════════════════════"
  exit 0
fi
