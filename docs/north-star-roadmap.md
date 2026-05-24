# Project North Star

The goal of Project North Star is to make Nimbus a complete local development
environment for teams running production workloads on AWS ECS. The target stack
is the one most commonly used in real ECS deployments:

> ECR · ECS · IAM · CloudWatch Logs · EventBridge Scheduler · CloudFront ·
> ALB · SSM · S3 · Aurora · Valkey · KMS · CloudWatch Metrics · Route 53 · ACM

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

## Phase 2 — IAM (structural)

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

## Phase 4 — EventBridge Scheduler

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

## Phase 6 — ALB (Application Load Balancer)

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

## Phase 7 — Aurora / RDS

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

## Phase 8 — Valkey / ElastiCache

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
| `CreateReplicationGroup` / `DescribeReplicationGroups` / `DeleteReplicationGroup` | |

---

## Phase 9 — ACM (Certificate Manager)

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

## Phase 10 — Route 53

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

## Phase 12 — Cognito User Pools

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

### Part 2 — JWT issuance + auth flows

JWT signing makes locally-issued tokens verifiable by app backends — the critical piece for Forge.

| Work item | Status |
|-----------|--------|
| RSA-2048 key pair generated at service startup (in-memory) | |
| JWKS endpoint: `GET /{userPoolId}/.well-known/jwks.json` | |
| `InitiateAuth` — `USER_PASSWORD_AUTH` flow → real RS256 access + id + refresh tokens | |
| `AdminInitiateAuth` — same as above, admin variant | |
| `GetUser` — validate access token, return user attributes | |
| `GlobalSignOut` / `RevokeToken` — invalidate tokens | |

### Part 3 — User management

| Work item | Status |
|-----------|--------|
| `AdminCreateUser` / `AdminSetUserPassword` / `AdminGetUser` / `AdminDeleteUser` | |
| `SignUp` (auto-confirm in local mode) / `ConfirmSignUp` | |
| `ListUsers` / `AdminListUsers` | |
| `AdminUpdateUserAttributes` / `AdminGetUserAttributes` | |

### Part 4 — Groups

| Work item | Status |
|-----------|--------|
| `CreateGroup` / `DeleteGroup` / `GetGroup` / `ListGroups` | |
| `AdminAddUserToGroup` / `AdminRemoveUserFromGroup` / `AdminListGroupsForUser` | |
| `cognito:groups` claim injected into id tokens | |

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
