# ALB (Application Load Balancer)

Nimbus emulates the AWS ELBv2 ALB control plane in-memory. Load balancers, target groups, listeners,
and routing rules are stored in-memory. `DNSName` is localhost-based and `State` is always `active`.
When a listener is created Nimbus starts a real HTTP reverse proxy on that port; incoming requests are
matched against listener rules (path-pattern, host-header) and forwarded to a randomly-selected
registered target. Registered targets are immediately `healthy`.

**Detection:** form-encoded request body containing `Version=2015-12-01`

## Supported operations

| Operation | Notes |
|-----------|-------|
| `CreateLoadBalancer` | Returns `localhost`-based `DNSName`, state `active`. For `application` type, validates the subnets span **at least two Availability Zones** (subnet AZs are resolved via the EC2 store); otherwise rejects with `ValidationError`. The provided subnets/AZs are echoed in `AvailabilityZones` |
| `DescribeLoadBalancers` | Filter by `LoadBalancerArns` or `Names`; returns `LoadBalancerNotFound` when ARN filter matches nothing |
| `DeleteLoadBalancer` | Removes from in-memory store |
| `SetSubnets` | Returns stub availability zone; required by TF provider v6 re-apply |
| `DescribeLoadBalancerAttributes` | Returns sensible defaults (deletion protection off, idle timeout 60 s, etc.) |
| `ModifyLoadBalancerAttributes` | Accepted and ignored |
| `DescribeCapacityReservation` | Returns stub empty reservation state |
| `CreateTargetGroup` | Stores name, protocol, port, VPC, target type, and the health-check settings; unset health-check fields report the AWS defaults |
| `DescribeTargetGroups` | Filter by `TargetGroupArns` or `Names`; `LoadBalancerArns` lists every LB attached via a listener default action or rule |
| `DeleteTargetGroup` | Removes from in-memory store |
| `ModifyTargetGroup` | Returns existing target group unchanged; required by TF provider v6 re-apply |
| `DescribeTargetGroupAttributes` | Returns sensible defaults (deregistration delay 300 s, stickiness off, etc.) |
| `ModifyTargetGroupAttributes` | Accepted and ignored |
| `CreateListener` / `DescribeListeners` / `DeleteListener` | HTTP/HTTPS listeners; `forward` default action stored and echoed back |
| `ModifyListener` | Protocol, port, and default action are updated |
| `DescribeListenerAttributes` / `ModifyListenerAttributes` | Sensible defaults; changes ignored |
| `CreateRule` / `DescribeRules` / `DeleteRule` / `ModifyRule` | Path-pattern and host-header conditions; rules sorted by numeric priority, default rule last |
| `SetRulePriorities` | Updates rule priorities |
| `RegisterTargets` | Stores targets in the target group |
| `DeregisterTargets` | Removes targets from the target group |
| `DescribeTargetHealth` | All registered targets immediately return `healthy` |
| `AddTags` / `RemoveTags` | Accepted and ignored (tags not stored) |
| `DescribeTags` | Returns empty tag list for each requested ARN |

## Example

```bash
# Create a load balancer
aws --endpoint-url http://localhost:4566 elbv2 create-load-balancer \
  --name my-lb --subnets subnet-aaa subnet-bbb

# Describe it
aws --endpoint-url http://localhost:4566 elbv2 describe-load-balancers --names my-lb

# Create a target group
aws --endpoint-url http://localhost:4566 elbv2 create-target-group \
  --name my-tg --protocol HTTP --port 80 --vpc-id vpc-123 --target-type ip

# Create a listener forwarding to the target group
aws --endpoint-url http://localhost:4566 elbv2 create-listener \
  --load-balancer-arn <lb-arn> --protocol HTTP --port 80 \
  --default-actions Type=forward,TargetGroupArn=<tg-arn>

# Register a target and check health
aws --endpoint-url http://localhost:4566 elbv2 register-targets \
  --target-group-arn <tg-arn> --targets Id=10.0.1.1,Port=8080
aws --endpoint-url http://localhost:4566 elbv2 describe-target-health \
  --target-group-arn <tg-arn>

# Inspect via Nimbus endpoint
curl http://localhost:4566/_nimbus/alb/loadbalancers
curl http://localhost:4566/_nimbus/alb/targetgroups
curl http://localhost:4566/_nimbus/alb/listeners
```

## Inspection endpoints

| Endpoint | Description |
|----------|-------------|
| `GET /_nimbus/alb/loadbalancers` | JSON list of all load balancers |
| `GET /_nimbus/alb/targetgroups` | JSON list of all target groups with registered targets |
| `GET /_nimbus/alb/listeners` | JSON list of all listeners with default action |
