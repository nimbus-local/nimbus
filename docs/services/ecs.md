# ECS

ECS/Fargate control-plane emulator. All state is in-memory. `RunTask` shells out to the `docker` CLI (`os/exec`) and starts real containers via `docker run -d`; tasks transition `PENDING → RUNNING → STOPPED` via a 5 s polling goroutine that calls `docker inspect`. Services are kept at `desiredCount` by a 10 s reconciliation loop that restarts exited containers. `StopTask` calls `docker stop` + `docker rm -f`. A `default` cluster is created automatically on startup. Spawned containers join the `nimbus-net` Docker network so they can reach each other and Nimbus.

Detection: `X-Amz-Target: AmazonEC2ContainerServiceV20141113.*`

## Supported operations

### Clusters

| Operation | Notable behaviour |
|-----------|-------------------|
| CreateCluster | Creates cluster; returns existing cluster silently if name already exists; `settings` are stored (see [Container Insights](#container-insights)) |
| DeleteCluster | Removes cluster by name or ARN |
| DescribeClusters | Optionally filtered by name/ARN list; returns `[]` for unknowns; echoes `settings` |
| ListClusters | Returns all cluster ARNs |
| UpdateClusterSettings | Merges the `settings` block into the cluster; `UpdateCluster` is accepted as an alias |

### Task Definitions

| Operation | Notable behaviour |
|-----------|-------------------|
| RegisterTaskDefinition | `family` required; revision auto-increments per family; `containerDefinitions` passed through as-is |
| DeregisterTaskDefinition | Sets status to `INACTIVE`; accepts `family:revision` or full ARN |
| DescribeTaskDefinition | Accepts bare `family` (latest revision), `family:revision`, or full ARN |
| ListTaskDefinitions | Filterable by `familyPrefix` and `status` |
| ListTaskDefinitionFamilies | Returns unique family names; filterable by `familyPrefix` |

### Tasks

| Operation | Notable behaviour |
|-----------|-------------------|
| RunTask | Runs `docker run -d`; task transitions `PENDING → RUNNING` once the container starts; `count` defaults to 1; `launchType` defaults to `FARGATE` |
| StopTask | Runs `docker stop` + `docker rm -f`; accepts full ARN or short UUID |
| DescribeTasks | Accepts list of ARNs; missing tasks are silently omitted |
| ListTasks | Filterable by `cluster`, `desiredStatus`, and `serviceName` |

### Services

| Operation | Notable behaviour |
|-----------|-------------------|
| CreateService | Creates service and starts `desiredCount` real Docker containers; reconciliation loop restarts exited containers every 10 s; `loadBalancers` are validated (see below) and stored |
| UpdateService | Updates `desiredCount`, `taskDefinition` and/or `loadBalancers`; load balancers are validated against the task definition set by the same call |
| DeleteService | Removes service; `desiredCount` and `runningCount` set to 0 |
| DescribeServices | Filterable by cluster; accepts name or ARN; reports `loadBalancers` (empty list when none) |
| ListServices | Returns all service ARNs for the given cluster |

## Load balancer validation

Each `loadBalancers` entry passed to `CreateService`/`UpdateService` must line up with
the task definition, exactly as real ECS requires:

| Condition | Response |
|-----------|----------|
| `containerName` is not a container in the task definition | 400 `InvalidParameterException: The container {name} does not exist in the task definition.` |
| The container declares no `portMappings` entry with that `containerPort` | 400 `InvalidParameterException: The container {name} did not have a container port {port} defined.` |

Without this check a Terraform config that attaches a target group to a container with
no matching `portMappings` applies cleanly against Nimbus and then fails on the first
real AWS deploy.

Nimbus does **not** verify that the `targetGroupArn` exists, and does not register task
IPs as ALB targets — the load balancer set is stored verbatim and echoed back by
`DescribeServices` so the Terraform provider sees no drift.

### Tags

| Operation | Notable behaviour |
|-----------|-------------------|
| TagResource | Adds or replaces tags on any resource ARN |
| UntagResource | Removes tags by key |
| ListTagsForResource | Returns all tags for the given ARN |

## Container Insights

A cluster created with `containerInsights` set to `enabled` (or `enhanced`) publishes
performance events to CloudWatch Logs, the same way real ECS does:

```
/aws/ecs/containerinsights/{cluster}/performance
```

The group is created with the cluster's first task — an enabled cluster sitting idle
produces no group, and tasks that have STOPPED are not reported on. One event is
published per entity per interval, into these streams:

| Stream | Event `Type` | Per |
|--------|--------------|-----|
| `ClusterTelemetry-{cluster}` | `Cluster` | cluster — `TaskCount`, `ServiceCount`, `ContainerInstanceCount` |
| `ServiceTelemetry-{service}` | `Service` | ACTIVE service — `DesiredTaskCount`, `RunningTaskCount`, `PendingTaskCount` |
| `FargateTelemetry-{n}` | `Task` and `Container` | task — `n` is derived from the task ARN, so it is stable for the task's life |

`Task` events carry `TaskId` (32-hex, not the dashed ARN UUID), `ClusterName`,
`ServiceName` (omitted for a standalone `RunTask` task, as in real ECS), `KnownStatus`,
`CpuUtilized`/`CpuReserved` (CPU units), `MemoryUtilized`/`MemoryReserved` (MB), network
and storage counters, ephemeral storage, and a minute-aligned epoch-ms `Timestamp`.
Each event also carries the `CloudWatchMetrics` embedded-metric-format block real ECS
attaches — except `Container` events, which have none.

### Cadence and delay

Real events describe a minute that has already passed and land about 80 s later.
Nimbus reproduces that: an event stamped `12:02:00` is published at `12:03:20`, so a
reader tailing the group sees the same lag it would in AWS rather than instant data.
Intervals missed while the process was blocked are skipped, not backfilled.

After a restart or `/_nimbus/reset` the first round is published on the emitter's next
tick rather than a full interval later, so local work is not held up waiting for the
group to appear. Every event still carries the backdated timestamp described above.

| Variable | Default | Meaning |
|----------|---------|---------|
| `NIMBUS_ECS_INSIGHTS_INTERVAL` | `1m` | Publishing cadence; event timestamps are aligned to it |
| `NIMBUS_ECS_INSIGHTS_DELAY` | `80s` | How far behind wall clock the published interval sits |

Shorten both to exercise a reader without waiting on real-world timing.

### Synthetic values

Nimbus does no cgroup accounting, so utilisation is synthesized: a bounded random walk
per container, kept inside what the task definition reserves, summed into the task's
figure. Matching the real events, `NetworkRxPackets`/`NetworkTxPackets` are cumulative
counters that only grow, while the `*Bytes` fields are per-second rates that rise and
fall. Reservations are real — they come from the task definition's `cpu`/`memory`, or a
container definition's own values when it sets them.

Events are ordinary log events, so the usual API reads them:

```bash
nimbuslocal logs filter-log-events \
  --log-group-name /aws/ecs/containerinsights/staging/performance \
  --filter-pattern '{ $.Type = "Task" && $.CpuUtilized > 0 }'
```

## Inspection endpoints

| Endpoint | Description |
|----------|-------------|
| `GET /_nimbus/ecs/tasks/{id}/logs` | Streams the last 200 lines of stdout/stderr from all containers in the task |

## ARN formats

| Resource | ARN |
|----------|-----|
| Cluster | `arn:aws:ecs:{region}:000000000000:cluster/{name}` |
| Task | `arn:aws:ecs:{region}:000000000000:task/{clusterName}/{uuid}` |
| Task Definition | `arn:aws:ecs:{region}:000000000000:task-definition/{family}:{revision}` |
| Service | `arn:aws:ecs:{region}:000000000000:service/{clusterName}/{serviceName}` |

## Example

```bash
# Register a task definition
nimbuslocal ecs register-task-definition \
  --family my-app \
  --container-definitions '[{"name":"app","image":"nginx:latest","cpu":256,"memory":512}]' \
  --requires-compatibilities FARGATE \
  --network-mode awsvpc \
  --cpu 256 --memory 512

# List task definitions
nimbuslocal ecs list-task-definitions

# Create a cluster
nimbuslocal ecs create-cluster --cluster-name staging

# Create a cluster that publishes Container Insights performance events
nimbuslocal ecs create-cluster --cluster-name staging \
  --settings name=containerInsights,value=enabled

# Turn Container Insights on for a cluster that already exists
nimbuslocal ecs update-cluster-settings --cluster staging \
  --settings name=containerInsights,value=enabled

# Run a task
nimbuslocal ecs run-task \
  --cluster staging \
  --task-definition my-app \
  --launch-type FARGATE \
  --count 2

# List running tasks
nimbuslocal ecs list-tasks --cluster staging --desired-status RUNNING

# Stop a task
nimbuslocal ecs stop-task --cluster staging --task <taskArn>

# Create a service
nimbuslocal ecs create-service \
  --cluster staging \
  --service-name my-service \
  --task-definition my-app \
  --desired-count 3 \
  --launch-type FARGATE

# Create a service behind a target group — containerName/containerPort must match
# a portMappings entry in the task definition, or the call is rejected
nimbuslocal ecs create-service \
  --cluster staging \
  --service-name my-lb-service \
  --task-definition my-app \
  --desired-count 2 \
  --launch-type FARGATE \
  --load-balancers "targetGroupArn=$TG_ARN,containerName=app,containerPort=80"

# List services
nimbuslocal ecs list-services --cluster staging

# Update service desired count
nimbuslocal ecs update-service \
  --cluster staging \
  --service my-service \
  --desired-count 5

# Delete a service
nimbuslocal ecs delete-service --cluster staging --service my-service
```
