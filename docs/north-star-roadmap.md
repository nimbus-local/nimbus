# Project North Star

The goal of Project North Star is to make Nimbus a complete local development
environment for teams running production workloads on AWS — initially focused on
ECS, now expanded to cover the full forge / SST v3 construct set.

**Original ECS target stack:**

> ECR · ECS · IAM · CloudWatch Logs · EventBridge Scheduler · CloudFront ·
> ALB · SSM · S3 · Aurora · Valkey · KMS · CloudWatch Metrics · Route 53 · ACM

**forge / SST v3 additions (Phases 13–22):**

> Lambda · API Gateway (REST + HTTP + WebSocket) · SNS · SES ·
> EventBridge Events · Secrets Manager · Kinesis · Step Functions ·
> Internal dev APIs (state/reset/live registration)

Each phase is a self-contained chunk that ships with:

- **Implementation** — Go service in `internal/services/{name}/`
- **Terraform fixture** — resources in `infra/terraform/`
- **Smoke test** — section added to `infra/scripts/smoke-test.sh`
- **Service doc** — `docs/services/{name}.md`
- **README row** — entry in the services table

---

## Phase 1 — ECS Container Execution ✅ shipped

**The unlock.** Everything else depends on containers actually running.

`/var/run/docker.sock` is mounted into Nimbus. `RunTask` shells out to the
`docker` CLI (no SDK dependency) and starts real containers. Tasks transition
`PENDING → RUNNING → STOPPED` via a 5 s polling goroutine. Services are
kept at `desiredCount` by a 10 s reconciliation loop.

| Work item | Notes |
|-----------|-------|
| Mount `/var/run/docker.sock` in `docker-compose.yml` | ✅ Done — explicit `name: nimbus-net` added so spawned containers can join |
| Container execution via `docker` CLI | ✅ `os/exec` — no SDK dep, works wherever Docker is installed |
| `RunTask` → `docker run -d` | ✅ Maps image, env, portMappings, cpu, memory, command, entryPoint |
| Task lifecycle goroutine (5 s poll) | ✅ `docker inspect` — transitions PENDING → RUNNING → STOPPED |
| Service reconciliation loop (10 s poll) | ✅ Restarts containers that have exited |
| `StopTask` → `docker stop` + `docker rm -f` | ✅ Async to keep API responsive |
| `/_nimbus/ecs/tasks/{id}/logs` inspection endpoint | ✅ Streams last 200 lines from all containers |
| Networking | ✅ `--network nimbus-net` — containers reach each other and Nimbus |

**Phase 1 limitations** (targets for later iterations):

| Limitation | Notes |
|-----------|-------|
| `secrets` field not resolved | Parsed but silently skipped; values are not injected. Future: resolve from Nimbus SSM/SecretsManager |
| `volumes` / `mountPoints` ignored | No bind-mount or named-volume support |
| `healthCheck` ignored | Docker's built-in healthcheck default applies |
| `logConfiguration` ignored | Use `/_nimbus/ecs/tasks/{id}/logs` instead |
| `links`, `dependsOn`, `ulimits`, `resourceRequirements` ignored | No dependency ordering between containers |
| Polling, not event-driven | Replace `docker inspect` loop with `docker events` streaming for lower latency |
| `StopTask` on a service task triggers reconciler restart | This is correct ECS behaviour; scale to 0 via `UpdateService(desiredCount=0)` |
| Image pulls block container start | Large images can make `RunTask` slow; future: pre-pull or surface pull progress |

---

## Phase 2 — IAM (structural) ✅ shipped

**Unblocks all other constructs.** No enforcement — any `AssumeRole` call succeeds
and returns fake credentials. Goal: Terraform `plan`/`apply` passes and ARNs are
returned correctly.

### Part 1 — Roles + STS ✅ shipped

Minimum needed for Terraform initialisation and `aws_iam_role` resources.

| Work item | Status |
|-----------|--------|
| `CreateRole` / `GetRole` / `DeleteRole` / `ListRoles` | ✅ |
| `AssumeRole` — returns fake `Credentials` | ✅ |
| `GetCallerIdentity` (STS) — returns account `000000000000` | ✅ |
| Terraform fixture (`iam.tf`), smoke test section | ✅ |

### Part 2 — Policy attachments ✅ shipped

Needed for `aws_iam_role_policy_attachment`. Handles AWS-managed ARNs (common ECS/Lambda case).

| Work item | Status |
|-----------|--------|
| `AttachRolePolicy` / `DetachRolePolicy` / `ListAttachedRolePolicies` | ✅ |

### Part 3 — Managed + inline policies ✅ shipped

Needed for `aws_iam_policy` and `aws_iam_role_policy` resources.

| Work item | Status |
|-----------|--------|
| `CreatePolicy` / `GetPolicy` / `GetPolicyVersion` / `DeletePolicy` / `ListPolicies` | ✅ |
| `PutRolePolicy` / `GetRolePolicy` / `DeleteRolePolicy` / `ListRolePolicies` | ✅ |

### Part 4 — Instance profiles + role tags ✅ shipped

Needed for `aws_iam_instance_profile` and tag-based resources.

| Work item | Status |
|-----------|--------|
| `CreateInstanceProfile` / `GetInstanceProfile` / `DeleteInstanceProfile` / `ListInstanceProfiles` | ✅ |
| `AddRoleToInstanceProfile` / `RemoveRoleFromInstanceProfile` | ✅ |
| `TagRole` / `UntagRole` / `ListRoleTags` / `UpdateRole` | ✅ |
| `ListInstanceProfilesForRole` / `ListPolicyVersions` — required by provider v6 delete path | ✅ |

---

## Phase 3 — CloudWatch Logs ✅ shipped

**Unblocks Function logging.** Once containers run, the `awslogs` log driver can
ship output to Nimbus instead of real CloudWatch.

### Part 1 — Log group + stream CRUD ✅ shipped

| Work item | Status |
|-----------|--------|
| `CreateLogGroup` / `DeleteLogGroup` / `DescribeLogGroups` | ✅ |
| `CreateLogStream` / `DeleteLogStream` / `DescribeLogStreams` | ✅ |

### Part 2 — Log ingestion ✅ shipped

| Work item | Status |
|-----------|--------|
| `PutLogEvents` — accept from containers via `awslogs` driver | ✅ |

### Part 3 — Retrieval + inspection ✅ shipped

| Work item | Status |
|-----------|--------|
| `GetLogEvents` / `FilterLogEvents` — basic pattern filter | ✅ |
| `/_nimbus/logs/{group}/{stream}` inspection endpoint | ✅ |

---

## Phase 4 — EventBridge Scheduler ✅ shipped

**Unblocks Cron constructs.** Separate service from EventBridge Events.

### Part 1 — Schedule + group CRUD (no firing) ✅ shipped

| Work item | Status |
|-----------|--------|
| `CreateSchedule` / `GetSchedule` / `UpdateSchedule` / `DeleteSchedule` / `ListSchedules` | ✅ |
| `CreateScheduleGroup` / `GetScheduleGroup` / `DeleteScheduleGroup` / `ListScheduleGroups` | ✅ |

### Part 2 — Expression parsing + ticker ✅ shipped

| Work item | Status |
|-----------|--------|
| Cron / rate expression parser — evaluate next-fire time | ✅ `rate(N unit)` + `cron(min hour dom month dow year)` with ranges, steps, name aliases |
| In-memory ticker — fires schedules at the right time | ✅ 5 s tick; advances nextFire past now to handle bursts |

### Part 3 — Target invocation + inspection ✅ shipped

| Work item | Status |
|-----------|--------|
| Target invocation — HTTP POST to Lambda ARN endpoint | ✅ Lambda: `Event` invocation type, goroutine so ticker never blocks; other ARN types log and skip |
| `/_nimbus/scheduler/schedules` inspection endpoint | ✅ (shipped in Part 1; shows NextFire + LastFired since Part 2) |

---

## Phase 5 — CloudFront ✅ shipped

**Unblocks StaticSite / NextjsSite constructs.**

### Part 1 — Distribution CRUD + inspection ✅ shipped

Returns `localhost`-based `DomainName`; status always `Deployed`. Verified against
AWS Terraform provider v6.

| Work item | Status |
|-----------|--------|
| `CreateDistribution` / `GetDistribution` / `UpdateDistribution` / `DeleteDistribution` | ✅ |
| `ListDistributions` | ✅ |
| `ListTagsForResource` / `AddTagsToResource` — required by provider v6 read path | ✅ |
| `/_nimbus/cloudfront/distributions` inspection endpoint | ✅ |
| Echo back verbatim `<DistributionConfig>` — prevents nil-pointer panics in provider v6 flatten | ✅ |
| Inject `<OriginGroups>` / `<LastModifiedTime>` if absent — required by provider v6 SDK | ✅ |
| Normalise `<DistributionConfigWithTags>` → `<DistributionConfig>` on create | ✅ |

### Part 2 — Invalidations

| Work item | Status |
|-----------|--------|
| `CreateInvalidation` / `GetInvalidation` / `ListInvalidations` — complete immediately | |

### Part 3 — Origin proxy

| Work item | Status |
|-----------|--------|
| Origin routing — proxy requests to configured S3 or ALB origin | |
| Cache behaviour matching — path-pattern → behaviour lookup | |

---

## Phase 6 — ALB (Application Load Balancer) ✅ shipped

**Unblocks Service constructs.** Routes external traffic to running ECS containers.

### Part 1 — Load balancer + target group CRUD

| Work item | Status |
|-----------|--------|
| `CreateLoadBalancer` / `DescribeLoadBalancers` / `DeleteLoadBalancer` — returns `localhost`-based DNS name | |
| `CreateTargetGroup` / `DescribeTargetGroups` / `DeleteTargetGroup` | |

### Part 2 — Listeners + target registration

| Work item | Status |
|-----------|--------|
| `CreateListener` / `DescribeListeners` / `DeleteListener` — HTTP port 80 first | |
| `RegisterTargets` / `DeregisterTargets` / `DescribeTargetHealth` | |
| Health check simulation — mark targets healthy after registration | |

### Part 3 — Rules + reverse proxy

| Work item | Status |
|-----------|--------|
| `CreateRule` / `DescribeRules` / `DeleteRule` — path-based and header-based routing | |
| HTTP reverse proxy — forward requests to registered target IPs | |

---

## Phase 7 — Aurora / RDS ✅ shipped

Sidecar pattern: a real Postgres container alongside Nimbus. RDS API returns endpoints pointing to it.

### Part 1 — Terraform stubs ✅ shipped

Needed so `terraform plan` doesn't fail before the cluster exists.

| Work item | Status |
|-----------|--------|
| `CreateDBSubnetGroup` / `DescribeDBSubnetGroups` / `DeleteDBSubnetGroup` | ✅ |
| `CreateDBClusterParameterGroup` / `CreateDBParameterGroup` — accept and ignore | ✅ |

### Part 2 — Cluster + sidecar ✅ shipped

| Work item | Status |
|-----------|--------|
| Add `postgres:16` sidecar to `docker-compose.yml` | ✅ |
| `CreateDBCluster` / `DescribeDBClusters` / `DeleteDBCluster` — Aurora Serverless v2 | ✅ |
| Endpoint resolves to `localhost:{postgres_port}` | ✅ |

### Part 3 — DB instances ✅ shipped

| Work item | Status |
|-----------|--------|
| `CreateDBInstance` / `DescribeDBInstances` / `DeleteDBInstance` | ✅ |

---

## Phase 8 — Valkey / ElastiCache ✅ shipped

Same sidecar pattern as Phase 7 with a Valkey (Redis-compatible) container.

### Part 1 — Terraform stubs

| Work item | Status |
|-----------|--------|
| `CreateCacheSubnetGroup` / `DescribeCacheSubnetGroups` | |
| `CreateCacheParameterGroup` — accept and ignore | |

### Part 2 — Cluster + sidecar

| Work item | Status |
|-----------|--------|
| Add `valkey/valkey` sidecar to `docker-compose.yml` | |
| `CreateCacheCluster` / `DescribeCacheClusters` / `DeleteCacheCluster` | |
| Endpoint resolves to `localhost:{valkey_port}` | |

### Part 3 — Replication groups

| Work item | Status |
|-----------|--------|
| `CreateReplicationGroup` / `DescribeReplicationGroups` / `DeleteReplicationGroup` | ✅ |
| `ModifyReplicationGroup` | ✅ |
| `IncreaseReplicaCount` / `DecreaseReplicaCount` — stubbed (return existing group, don't actually change replica count) | stub |

> **Note:** `IncreaseReplicaCount` and `DecreaseReplicaCount` are stubbed to unblock TF provider v6's
> `aws_elasticache_replication_group` apply. A full implementation would track per-node-group shard/replica
> topology. Only implement if Forge workloads need real multi-replica behaviour locally.

---

## Phase 9 — ACM (Certificate Manager) ✅ shipped

Returns self-signed certificates. Needed for ALB HTTPS listeners. Auto-validates — no DNS or email challenge.

### Part 1 — Certificate CRUD + inspection ✅ shipped

| Work item | Status |
|-----------|--------|
| `RequestCertificate` — generate real self-signed cert (RSA 2048, crypto/x509) | ✅ |
| `DescribeCertificate` — return `ISSUED` immediately | ✅ |
| `ListCertificates` / `DeleteCertificate` | ✅ |
| `GetCertificate` — return cert PEM + chain | ✅ |
| `AddTagsToCertificate` / `RemoveTagsFromCertificate` / `ListTagsForCertificate` | ✅ |
| `/_nimbus/acm/certs/{arn}` — download cert PEM for local trust | ✅ |

---

## Phase 10 — Route 53 ✅ shipped

Mostly needed so Terraform plans succeed. Local DNS resolution is a stretch goal.

### Part 1 — Hosted zone CRUD + record sets ✅ shipped

| Work item | Status |
|-----------|--------|
| `CreateHostedZone` / `GetHostedZone` / `ListHostedZones` / `DeleteHostedZone` | ✅ |
| `GetChange` — always return `INSYNC` | ✅ |
| `ChangeResourceRecordSets` / `ListResourceRecordSets` — accept any records, store in-memory | ✅ |
| `ListTagsForResource` / `ChangeTagsForResource` — tag support | ✅ |
| `GetHostedZoneCount` | ✅ |

---

## Phase 11 — CloudWatch Metrics ✅ shipped

Accept metrics from apps and SDKs.

### Part 1 — Ingest + list ✅ shipped

| Work item | Status |
|-----------|--------|
| `PutMetricData` — store time series in-memory (capped at 10,000 pts/series) | ✅ |
| `ListMetrics` — filter by namespace, metric name, dimensions | ✅ |

### Part 2 — Retrieval ✅ shipped

| Work item | Status |
|-----------|--------|
| `GetMetricStatistics` — Sum, Average, Min, Max, SampleCount per period bucket | ✅ |
| `GetMetricData` — multi-metric query with `MetricStat` queries | ✅ |

### Part 3 — Alarms + inspection ✅ shipped

| Work item | Status |
|-----------|--------|
| `PutMetricAlarm` / `DescribeAlarms` / `DescribeAlarmsForMetric` / `DeleteAlarms` — state always `OK` | ✅ |
| Tag support: `ListTagsForResource` / `TagResource` / `UntagResource` | ✅ |
| `/_nimbus/metrics` inspection endpoint | ✅ |

---

---

## Phase 12 — Cognito User Pools ✅ shipped

Enables Forge to test full authentication flows locally without hitting real AWS. Forge uses Cognito User Pools to protect web apps it deploys, so the emulator needs both the infrastructure layer (for Terraform) and the auth layer (for app sign-in and JWT verification).

### Part 1 — User pool and client CRUD ✅ shipped

| Work item | Status |
|-----------|--------|
| `CreateUserPool` / `DescribeUserPool` / `UpdateUserPool` / `DeleteUserPool` / `ListUserPools` | ✅ |
| `CreateUserPoolClient` / `DescribeUserPoolClient` / `UpdateUserPoolClient` / `DeleteUserPoolClient` / `ListUserPoolClients` | ✅ |
| `ListTagsForResource` / `TagResource` / `UntagResource` | ✅ |
| `DeleteUserPool` cascade-deletes all pool clients | ✅ |
| `cognitoidp` endpoint in `provider.tf` | ✅ |
| Terraform fixture (`cognito.tf`) — user pool + client | ✅ |
| Smoke tests | ✅ |

### Part 2 — JWT issuance + auth flows ✅ shipped

JWT signing makes locally-issued tokens verifiable by app backends — the critical piece for Forge.

| Work item | Status |
|-----------|--------|
| RSA-2048 key pair generated at service startup (in-memory) | ✅ |
| JWKS endpoint: `GET /{userPoolId}/.well-known/jwks.json` | ✅ |
| `InitiateAuth` — `USER_PASSWORD_AUTH` flow → real RS256 access + id + refresh tokens | ✅ |
| `AdminInitiateAuth` — `ADMIN_USER_PASSWORD_AUTH` / `ADMIN_NO_SRP_AUTH` | ✅ |
| `GetUser` — validate access token, return user attributes | ✅ |
| `GlobalSignOut` / `RevokeToken` — invalidate tokens | ✅ |
| `AdminCreateUser` / `AdminSetUserPassword` — user creation needed for auth flows | ✅ |
| `GetUserPoolMfaConfig` / `SetUserPoolMfaConfig` — required by TF provider v6 read path | ✅ |

### Part 3 — User management ✅ shipped

`AdminCreateUser` and `AdminSetUserPassword` shipped early in Part 2 (required to make auth flows testable). This part adds the remaining management operations.

| Work item | Status |
|-----------|--------|
| `AdminGetUser` / `AdminDeleteUser` | ✅ |
| `AdminUpdateUserAttributes` | ✅ |
| `SignUp` (auto-confirm in local mode) / `ConfirmSignUp` | ✅ |
| `ListUsers` | ✅ |

### Part 4 — Groups ✅ shipped

| Work item | Status |
|-----------|--------|
| `CreateGroup` / `DeleteGroup` / `GetGroup` / `ListGroups` | ✅ |
| `AdminAddUserToGroup` / `AdminRemoveUserFromGroup` / `AdminListGroupsForUser` | ✅ |
| `cognito:groups` claim injected into id tokens | ✅ |

---

---

## Phase 13 — Lambda ✅ shipped

Full Lambda control plane and data plane. Foundation for every event-driven forge construct.

### Part 1 — Function CRUD + invocation ✅ shipped

| Work item | Status |
|-----------|--------|
| `CreateFunction` / `GetFunction` / `GetFunctionConfiguration` / `UpdateFunctionCode` / `UpdateFunctionConfiguration` / `DeleteFunction` / `ListFunctions` | ✅ |
| `PublishVersion` / `ListVersions` | ✅ |
| `InvokeFunction` — HTTP proxy mode (live dev endpoint registration) | ✅ |
| `InvokeAsync` — returns 202, executes in background | ✅ |
| `InvokeWithResponseStream` — returns 501 | ✅ |
| `TagResource` / `UntagResource` / `ListTags` | ✅ |

### Part 2 — Aliases + permissions ✅ shipped

| Work item | Status |
|-----------|--------|
| `CreateAlias` / `GetAlias` / `UpdateAlias` / `DeleteAlias` / `ListAliases` | ✅ |
| `AddPermission` / `GetPolicy` / `RemovePermission` | ✅ |

### Part 3 — Event source mappings ✅ shipped

| Work item | Status |
|-----------|--------|
| `CreateEventSourceMapping` / `GetEventSourceMapping` / `UpdateEventSourceMapping` / `DeleteEventSourceMapping` / `ListEventSourceMappings` | ✅ |

### Part 4 — Concurrency + layers ✅ shipped

| Work item | Status |
|-----------|--------|
| `PutFunctionConcurrency` / `GetFunctionConcurrency` / `DeleteFunctionConcurrency` | ✅ |
| `PutProvisionedConcurrencyConfig` / `GetProvisionedConcurrencyConfig` / `DeleteProvisionedConcurrencyConfig` / `ListProvisionedConcurrencyConfigs` | ✅ |
| `PublishLayerVersion` / `GetLayerVersion` / `DeleteLayerVersion` / `ListLayers` / `ListLayerVersions` | ✅ |
| `AddLayerVersionPermission` / `GetLayerVersionPolicy` / `RemoveLayerVersionPermission` | ✅ |

### Part 5 — URL config + code signing + settings ✅ shipped

| Work item | Status |
|-----------|--------|
| `CreateFunctionUrlConfig` / `GetFunctionUrlConfig` / `UpdateFunctionUrlConfig` / `DeleteFunctionUrlConfig` / `ListFunctionUrlConfigs` | ✅ |
| `PutFunctionEventInvokeConfig` / `GetFunctionEventInvokeConfig` / `UpdateFunctionEventInvokeConfig` / `DeleteFunctionEventInvokeConfig` / `ListFunctionEventInvokeConfigs` | ✅ |
| `CreateCodeSigningConfig` / `GetCodeSigningConfig` / `UpdateCodeSigningConfig` / `DeleteCodeSigningConfig` | ✅ |
| `PutFunctionCodeSigningConfig` / `GetFunctionCodeSigningConfig` / `DeleteFunctionCodeSigningConfig` | ✅ |
| `GetAccountSettings` / `GetRuntimeManagementConfig` / `PutRuntimeManagementConfig` | ✅ |
| `PutFunctionRecursionConfig` / `GetFunctionRecursionConfig` | ✅ |

---

## Phase 14 — API Gateway ✅ shipped

Both REST API (v1) and HTTP API (v2) management planes plus the Lambda proxy data plane.

### Part 1 — REST API (v1) control plane ✅ shipped

| Work item | Status |
|-----------|--------|
| `CreateRestApi` / `GetRestApis` / `GetRestApi` / `UpdateRestApi` / `DeleteRestApi` | ✅ |
| `GetResources` / `GetResource` / `CreateResource` / `DeleteResource` | ✅ |
| `PutMethod` / `GetMethod` / `DeleteMethod` | ✅ |
| `PutIntegration` / `GetIntegration` / `DeleteIntegration` | ✅ |
| `PutMethodResponse` / `GetMethodResponse` / `PutIntegrationResponse` / `GetIntegrationResponse` | ✅ |
| `CreateDeployment` / `GetDeployments` / `CreateStage` / `GetStages` / `UpdateStage` / `DeleteStage` | ✅ |
| REST API execute data plane: `/{apiId}/{stage}/_user_request_/{proxy+}` → Lambda `AWS_PROXY` | ✅ |

### Part 2 — HTTP API (v2) control plane + data plane ✅ shipped

Supports both AWS SDK Go v2 path prefix (`/v2/apis/`) and direct (`/apis/`).

| Work item | Status |
|-----------|--------|
| `CreateApi` / `GetApis` / `GetApi` / `UpdateApi` / `DeleteApi` | ✅ |
| `CreateRoute` / `GetRoutes` / `GetRoute` / `UpdateRoute` / `DeleteRoute` | ✅ |
| `CreateIntegration` / `GetIntegrations` / `GetIntegration` / `UpdateIntegration` / `DeleteIntegration` | ✅ |
| `CreateStage` / `GetStages` / `GetStage` / `UpdateStage` / `DeleteStage` | ✅ |
| `CreateDeployment` / `GetDeployments` / `GetDeployment` / `DeleteDeployment` | ✅ |
| HTTP API data plane: payload format v1.0 and v2.0, path parameter extraction, cookie forwarding | ✅ |

### Part 3 — WebSocket API

| Work item | Status |
|-----------|--------|
| `CreateApi` (WebSocket protocol) — reuse v2 store with `protocolType: WEBSOCKET` | |
| WebSocket upgrade via `net/http` hijacker — `$connect` / `$disconnect` / `$default` route dispatch | |
| Lambda event envelope for WebSocket events | |
| Connection registry: `sync.Map[connectionId → conn]` | |
| Management API: `POST /v1/apis/{apiId}/@connections/{connectionId}` → send frame | |
| Management API: `DELETE /v1/apis/{apiId}/@connections/{connectionId}` → close | |

---

## Phase 15 — SNS ✅ shipped

| Work item | Status |
|-----------|--------|
| `CreateTopic` / `DeleteTopic` / `ListTopics` / `GetTopicAttributes` / `SetTopicAttributes` | ✅ |
| `Subscribe` / `Unsubscribe` / `ListSubscriptions` / `ListSubscriptionsByTopic` / `GetSubscriptionAttributes` / `ConfirmSubscription` | ✅ |
| `Publish` / `PublishBatch` — messages captured in-memory | ✅ |
| `ListTagsForResource` / `TagResource` | ✅ |
| `/_nimbus/sns/messages` GET (inspect) / DELETE (clear) | ✅ |

---

## Phase 16 — SES ✅ shipped

| Work item | Status |
|-----------|--------|
| `SendEmail` — log to stdout, capture in-memory | ✅ |
| `SendRawEmail` — parse MIME, log from/to/subject | ✅ |
| `VerifyEmailIdentity` / `VerifyDomainIdentity` / `GetIdentityVerificationAttributes` | ✅ |
| `CreateConfigurationSet` / `DeleteConfigurationSet` | ✅ |
| `/_nimbus/ses/messages` GET (inspect) / DELETE (clear) | ✅ |

---

## Phase 17 — EventBridge Events ✅ shipped

Event buses, rules, and targets. Distinct from EventBridge Scheduler (Phase 4).

| Work item | Status |
|-----------|--------|
| `CreateEventBus` / `DeleteEventBus` / `DescribeEventBus` / `ListEventBuses` | ✅ |
| `PutEvents` — events captured in-memory (bus `default` always present) | ✅ |
| `PutRule` / `DeleteRule` / `DescribeRule` / `ListRules` / `EnableRule` / `DisableRule` | ✅ |
| `PutTargets` / `RemoveTargets` / `ListTargetsByRule` | ✅ |
| `ListTagsForResource` / `TagResource` / `UntagResource` | ✅ |
| Detects `AmazonEventBridge.*`, `AmazonCloudWatchEvents.*`, `AWSEvents.*` targets | ✅ |
| `/_nimbus/eventbridge/events` GET (inspect) / DELETE (clear) | ✅ |

---

## Phase 18 — Secrets Manager + KMS ✅ shipped

| Work item | Status |
|-----------|--------|
| Secrets Manager: `CreateSecret` / `GetSecretValue` / `PutSecretValue` / `UpdateSecret` / `DeleteSecret` / `ListSecrets` / `DescribeSecret` | ✅ |
| Secrets Manager: `TagResource` / `UntagResource` / `ListSecretVersionIds` | ✅ |
| KMS: `CreateKey` / `DescribeKey` / `ListKeys` / `ScheduleKeyDeletion` | ✅ |
| KMS: `CreateAlias` / `ListAliases` / `DeleteAlias` / `UpdateAlias` | ✅ |
| KMS: `Encrypt` / `Decrypt` / `GenerateDataKey` — in-memory AES-256 with no actual protection | ✅ |
| KMS: `TagResource` / `UntagResource` / `ListResourceTags` | ✅ |

---

## Phase 19 — ECR ✅ shipped

| Work item | Status |
|-----------|--------|
| `CreateRepository` / `DescribeRepositories` / `DeleteRepository` / `ListTagsForResource` | ✅ |
| `GetAuthorizationToken` — return a dummy base64 token accepted by `docker login` | ✅ |
| `PutImage` / `DescribeImages` / `BatchDeleteImage` / `BatchGetImage` | ✅ |
| `SetRepositoryPolicy` / `GetRepositoryPolicy` / `DeleteRepositoryPolicy` | ✅ |
| `GetLifecyclePolicy` / `PutLifecyclePolicy` / `DeleteLifecyclePolicy` | ✅ |
| Registry (HTTP v2) data plane: `GET /v2/{name}/manifests/{ref}`, `PUT`, `DELETE`; blob `HEAD`/`GET`/`POST`/`PATCH`/`PUT` | ✅ |

---

## Phase 20 — Kinesis Data Streams

### Part 1 — Stream CRUD + shard model ✅ shipped

| Work item | Status |
|-----------|--------|
| `CreateStream` / `DeleteStream` / `ListStreams` / `DescribeStream` / `DescribeStreamSummary` |✅ |
| `ListShards` — return configured shard count |✅ |
| `AddTagsToStream` / `ListTagsForStream` / `RemoveTagsFromStream` |✅ |

### Part 2 — PutRecord / PutRecords ✅ shipped

| Work item | Status |
|-----------|--------|
| `PutRecord` / `PutRecords` — in-memory ring buffer per shard, monotonic sequence numbers |✅ |
| Partition key → shard via `hash(partitionKey) mod shardCount` | |

### Part 3 — GetRecords + iterators ✅ shipped

| Work item | Status |
|-----------|--------|
| `GetShardIterator` — `TRIM_HORIZON`, `LATEST`, `AT_SEQUENCE_NUMBER`, `AFTER_SEQUENCE_NUMBER` |✅ |
| `GetRecords` — advance iterator, return `MillisBehindLatest` |✅ |
| `MergeShards` / `SplitShard` — stub (return success, no resharding) |✅ |

### Part 4 — Lambda ESM integration ✅ shipped

| Work item | Status |
|-----------|--------|
| Kinesis ESM runner — goroutine per active mapping polling `GetRecords`, building Kinesis event envelope, invoking Lambda |✅ |
| Terraform fixture (`kinesis.tf`), smoke test section |✅ |
| Service doc + README row |✅ |

---

## Phase 21 — Step Functions

### Part 1 — State machine CRUD

| Work item | Status |
|-----------|--------|
| `CreateStateMachine` — parse and store ASL JSON definition | |
| `DescribeStateMachine` / `UpdateStateMachine` / `DeleteStateMachine` / `ListStateMachines` | |
| `TagResource` / `UntagResource` / `ListTagsForResource` | |
| `sfn` endpoint in `provider.tf` | |
| Service doc + README row | |

_Test_: create/describe/update/delete/list a state machine; verify definition round-trips. Smoke: `aws stepfunctions create-state-machine` + `list-state-machines`.

### Part 2 — Execution scaffold + terminal states

| Work item | Status |
|-----------|--------|
| `StartExecution` — Standard (goroutine) + Express (synchronous, awaited) | |
| `Pass` state — pass input to output, apply `Result` / `ResultPath` | |
| `Succeed` / `Fail` states | |
| `DescribeExecution` / `GetExecutionHistory` | |

_Test_: run a `Pass → Succeed` chain, assert status=`SUCCEEDED` and history events present. Smoke: `aws stepfunctions start-execution` + `describe-execution`.

### Part 3 — Choice + Wait states

| Work item | Status |
|-----------|--------|
| `Choice` state — evaluate StringEquals / NumericGreaterThan / BooleanEquals / And / Or / Not conditions, branch | |
| `Wait` state — sleep `Seconds` or until `Timestamp` | |
| `StopExecution` | |

_Test_: Choice: branch on a string condition, verify correct next state. Wait: `Seconds: 1`, assert execution finishes after delay. Smoke: CLI execution with branching input.

### Part 4 — Task state → Lambda invocation

| Work item | Status |
|-----------|--------|
| `Task` state — invoke `arn:aws:lambda:…:function:{name}` via Lambda service HTTP | |
| Error handling — `Catch` / `Retry` on task failure | |

_Test_: unit-mock the Lambda HTTP call; integration test invokes a real registered function and verifies output propagated. Smoke: requires Nimbus running with Lambda registered.

### Part 5 — Parallel + Map states

| Work item | Status |
|-----------|--------|
| `Parallel` — run branches concurrently, merge results into array | |
| `Map` — iterate over input array, run iterator state machine per item | |

_Test_: Parallel: two `Pass` branches merge. Map: iterate over `[1,2,3]`, each item through a `Pass`. Smoke: CLI execution on an array input.

### Part 6 — Terraform fixture, smoke tests, docs

| Work item | Status |
|-----------|--------|
| Terraform fixture (`sfn.tf`) — state machine + execution role | |
| Smoke test section in `smoke-test.sh` | |
| Service doc (`docs/services/sfn.md`) already created in Part 1 — extend with execution examples | |

---

## Phase 22 — Internal Dev APIs + forge Tunnel

These endpoints are not AWS-compatible — they are Nimbus-specific APIs used by forge dev tooling and test harnesses.

### Part 1 — Live function registration ✅ shipped

| Work item | Status |
|-----------|--------|
| `POST /_nimbus/lambda/register` — register `{function_name, endpoint}` for HTTP proxy invocation | ✅ |
| `DELETE /_nimbus/lambda/register/{function_name}` — deregister | ✅ |

### Part 2 — State inspection + reset ✅ shipped

| Work item | Status |
|-----------|--------|
| `GET /_nimbus/state` — dump all in-memory state: functions, queues, topics, buses, ESMs, parameters, schedules | ✅ |
| `POST /_nimbus/reset` — clear all in-memory state (S3 filesystem objects untouched) | ✅ |
| Extend `GET /_nimbus/health` — include active ESM count and registered service list | ✅ |

### Part 3 — forge dev tunnel verification ✅ shipped

| Work item | Status |
|-----------|--------|
| Trace full round-trip: API Gateway → Lambda emulator → live registration proxy → local handler | ✅ |
| Document required env vars (`AWS_ENDPOINT_URL`, `AWS_DEFAULT_REGION`, etc.) | ✅ |

---

## Guiding principles

- **Each part ships as one commit** — compile and `go vet` must pass before moving on.
- **Part 1 unblocks the critical path** — Terraform `plan`/`apply` or integration tests, not the full API.
- **Each phase ships independently** — no phase blocks the next from being useful.
- **Sidecar over emulation** for stateful services (Postgres, Valkey) — real
  data stores are more valuable than partial emulations.
- **Structural IAM only** — enforcement is a separate effort and not required for
  local dev or Terraform testing.
- **Every phase follows the standard checklist**: implementation + Terraform
  fixture + smoke test + service doc + README row (completed after Part 1).
