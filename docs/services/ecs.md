# ECS

ECS/Fargate control-plane emulator. All state is in-memory. No containers are actually started — tasks are simulated as immediately `RUNNING`. A `default` cluster is created automatically on startup.

Detection: `X-Amz-Target: AmazonEC2ContainerServiceV20141113.*`

## Supported operations

### Clusters

| Operation | Notable behaviour |
|-----------|-------------------|
| CreateCluster | Creates cluster; returns existing cluster silently if name already exists |
| DeleteCluster | Removes cluster by name or ARN |
| DescribeClusters | Optionally filtered by name/ARN list; returns `[]` for unknowns |
| ListClusters | Returns all cluster ARNs |

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
| RunTask | Tasks start immediately as `RUNNING`; `count` defaults to 1; `launchType` defaults to `FARGATE` |
| StopTask | Sets `lastStatus` and `desiredStatus` to `STOPPED`; accepts full ARN or short UUID |
| DescribeTasks | Accepts list of ARNs; missing tasks are silently omitted |
| ListTasks | Filterable by `cluster`, `desiredStatus`, and `serviceName` |

### Services

| Operation | Notable behaviour |
|-----------|-------------------|
| CreateService | Creates service and spawns `desiredCount` simulated backing tasks in group `service:<name>` |
| UpdateService | Updates `desiredCount` and/or `taskDefinition` |
| DeleteService | Removes service; `desiredCount` and `runningCount` set to 0 |
| DescribeServices | Filterable by cluster; accepts name or ARN |
| ListServices | Returns all service ARNs for the given cluster |

### Tags

| Operation | Notable behaviour |
|-----------|-------------------|
| TagResource | Adds or replaces tags on any resource ARN |
| UntagResource | Removes tags by key |
| ListTagsForResource | Returns all tags for the given ARN |

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
