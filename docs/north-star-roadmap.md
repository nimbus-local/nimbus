# Project North Star

The goal of Project North Star is to make Nimbus a complete local development
environment for teams running production workloads on AWS ECS. The target stack
is the one most commonly used in real ECS deployments:

> ECR · ECS · ALB · SSM · S3 · Aurora · Valkey · KMS · CloudWatch Logs ·
> CloudWatch Metrics · IAM · Route 53 · ACM

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

## Phase 2 — CloudWatch Logs

Natural follow-on to Phase 1. Once containers run, the `awslogs` log driver can
ship their output to Nimbus instead of real CloudWatch.

| Work item | Notes |
|-----------|-------|
| `CreateLogGroup` / `DeleteLogGroup` / `DescribeLogGroups` | |
| `CreateLogStream` / `DescribeLogStreams` | |
| `PutLogEvents` | Accept from containers via `awslogs` driver |
| `GetLogEvents` / `FilterLogEvents` | Basic retrieval and pattern filter |
| `/_nimbus/logs/{group}/{stream}` inspection endpoint | Human-readable log tailing |

---

## Phase 3 — ALB (Application Load Balancer)

Routes external traffic to running ECS containers. Requires target groups and
listeners — these are inseparable from the load balancer itself.

| Work item | Notes |
|-----------|-------|
| `CreateLoadBalancer` / `DescribeLoadBalancers` / `DeleteLoadBalancer` | Returns a `localhost`-based DNS name |
| `CreateTargetGroup` / `DescribeTargetGroups` / `DeleteTargetGroup` | IP and instance target types |
| `RegisterTargets` / `DeregisterTargets` / `DescribeTargetHealth` | Track registered ECS task IPs |
| `CreateListener` / `DescribeListeners` / `DeleteListener` | HTTP on port 80 first; HTTPS after Phase 8 (ACM) |
| `CreateRule` / `DescribeRules` / `DeleteRule` | Path-based and header-based routing |
| HTTP reverse proxy | Actually forward requests to registered target IPs |
| Health check simulation | Mark targets healthy after registration |

---

## Phase 4 — IAM (structural)

Terraform needs to create roles and policies. No enforcement — any `AssumeRole`
call succeeds and returns fake credentials. The goal is Terraform plans pass and
ARNs are returned correctly.

| Work item | Notes |
|-----------|-------|
| `CreateRole` / `GetRole` / `DeleteRole` / `ListRoles` | |
| `PutRolePolicy` / `GetRolePolicy` / `DeleteRolePolicy` | Inline policies |
| `CreatePolicy` / `GetPolicy` / `DeletePolicy` / `ListPolicies` | Managed policies |
| `AttachRolePolicy` / `DetachRolePolicy` / `ListAttachedRolePolicies` | |
| `CreateInstanceProfile` / `AddRoleToInstanceProfile` | ECS task execution roles |
| `AssumeRole` | Return fake `Credentials` — no enforcement |

---

## Phase 5 — Aurora / RDS

Sidecar pattern: a real Postgres container runs alongside Nimbus. The RDS API
returns endpoints that point to it. Same model as DynamoDB Local today.

| Work item | Notes |
|-----------|-------|
| Add `postgres:16` sidecar to `docker-compose.yml` | |
| `CreateDBCluster` / `DescribeDBClusters` / `DeleteDBCluster` | Aurora Serverless v2 compatible |
| `CreateDBInstance` / `DescribeDBInstances` / `DeleteDBInstance` | |
| Endpoint resolves to `localhost:{postgres_port}` | Apps connect to real Postgres |
| `CreateDBSubnetGroup` / `CreateDBParameterGroup` | Terraform needs these to plan |

---

## Phase 6 — Valkey / ElastiCache

Same sidecar pattern as Phase 5 but with a Valkey (Redis-compatible) container.

| Work item | Notes |
|-----------|-------|
| Add `valkey/valkey` sidecar to `docker-compose.yml` | |
| `CreateReplicationGroup` / `DescribeReplicationGroups` / `DeleteReplicationGroup` | |
| `CreateCacheCluster` / `DescribeCacheClusters` / `DeleteCacheCluster` | |
| Endpoint resolves to `localhost:{valkey_port}` | Apps connect to real Valkey |
| `CreateCacheSubnetGroup` / `CreateCacheParameterGroup` | Terraform stubs |

---

## Phase 7 — ACM (Certificate Manager)

Returns self-signed certificates. Needed for ALB HTTPS listeners. Auto-validates
every certificate — no DNS or email challenge locally.

| Work item | Notes |
|-----------|-------|
| `RequestCertificate` | Generate real self-signed cert for the requested domain |
| `DescribeCertificate` | Return `ISSUED` immediately — no validation pending |
| `ListCertificates` | |
| `DeleteCertificate` | |
| `/_nimbus/acm/certs/{arn}` inspection endpoint | Download the self-signed cert for local trust |

---

## Phase 8 — Route 53

Mostly needed so Terraform plans succeed. Local DNS resolution is a stretch goal.

| Work item | Notes |
|-----------|-------|
| `CreateHostedZone` / `GetHostedZone` / `ListHostedZones` / `DeleteHostedZone` | |
| `ChangeResourceRecordSets` / `ListResourceRecordSets` | Accept any records, store in-memory |
| `GetChange` | Always return `INSYNC` |

---

## Phase 9 — CloudWatch Metrics

Accept metrics from apps and SDKs. Useful for testing dashboards and alarms
without hitting real CloudWatch.

| Work item | Notes |
|-----------|-------|
| `PutMetricData` | Store time series in-memory |
| `GetMetricData` / `GetMetricStatistics` | Basic aggregation (Sum, Average, Max, Min) |
| `ListMetrics` | |
| `PutMetricAlarm` / `DescribeAlarms` | Stub — always return `OK` state |
| `/_nimbus/metrics` inspection endpoint | Human-readable metric dump |

---

## Guiding principles

- **Each phase ships independently** — no phase blocks the next from being useful.
- **Sidecar over emulation** for stateful services (Postgres, Valkey) — real
  data stores are more valuable than partial emulations.
- **Structural IAM only** — enforcement is a separate effort and not required for
  local dev or Terraform testing.
- **Every phase follows the standard checklist**: implementation + Terraform
  fixture + smoke test + service doc + README row.
