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

## Phase 1 — ECS Container Execution ⬅ start here

**The unlock.** Everything else depends on containers actually running.

Mount the Docker socket into Nimbus so `RunTask` starts real containers on the
host daemon instead of returning a simulated `RUNNING` record.

| Work item | Notes |
|-----------|-------|
| Mount `/var/run/docker.sock` in `docker-compose.yml` | Same pattern as DynamoDB Local sidecar |
| Docker SDK dependency (`github.com/docker/docker/client`) | First runtime dependency — justified by the feature value |
| `RunTask` → `docker.ContainerCreate` + `ContainerStart` | Map ECS container definitions to Docker config (image, env, ports, CPU/mem) |
| Task lifecycle goroutine | Watch real container state: `PENDING → RUNNING → STOPPED` |
| Service reconciliation loop | Keep `desiredCount` containers running; restart on exit |
| `StopTask` → `docker.ContainerStop` + `ContainerRemove` | Clean up on stop/delete |
| `/_nimbus/ecs/tasks/{id}/logs` inspection endpoint | Stream container stdout/stderr |
| Networking | Attach spawned containers to `nimbus-net` so they reach each other and Nimbus |

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
