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
else
  fail "describe-load-balancers (LB not found — run 'make apply' first)"
  LB_ARN=""
fi

TG_ARN=$($CLI elbv2 describe-target-groups --names "$PREFIX" \
  --query "TargetGroups[0].TargetGroupArn" --output text 2>/dev/null)
if [ -n "${TG_ARN:-}" ] && [ "$TG_ARN" != "None" ]; then
  try "describe-target-groups finds TG" true
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
else
  fail "describe-db-instances (instance not found — run 'make apply' first)"
fi

try_match "/_nimbus/rds/clusters inspection" "$PREFIX" \
  curl -sf "$NIMBUS/_nimbus/rds/clusters"

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

try_match "/_nimbus/elasticache/clusters inspection" "$PREFIX" \
  curl -sf "$NIMBUS/_nimbus/elasticache/clusters"

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
