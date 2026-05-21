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

# ── ECR ───────────────────────────────────────────────────────────────────────

section "ECR"
try_match "describe-repositories finds repo" "$PREFIX" \
  $CLI ecr describe-repositories --query 'repositories[].repositoryName' --output text

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
  try "run-task RUNNING" \
    $CLI ecs describe-tasks --cluster "$PREFIX" --tasks "$TASK_ARN" \
      --query 'tasks[0].lastStatus' --output text | grep -q RUNNING
  try "stop-task" $CLI ecs stop-task --cluster "$PREFIX" --task "$TASK_ARN"
else
  fail "run-task"
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
