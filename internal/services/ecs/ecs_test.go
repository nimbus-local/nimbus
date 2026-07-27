package ecs

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newSvc() *Service { return New("us-east-1") }

func call(t *testing.T, svc *Service, action string, body interface{}) (int, map[string]interface{}) {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(b))
	req.Header.Set("X-Amz-Target", ecsTarget+action)
	w := httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	var m map[string]interface{}
	json.NewDecoder(w.Body).Decode(&m)
	return w.Code, m
}

func do(t *testing.T, svc *Service, action string, body interface{}) map[string]interface{} {
	t.Helper()
	_, m := call(t, svc, action, body)
	return m
}

func status(t *testing.T, svc *Service, action string, body interface{}) int {
	t.Helper()
	code, _ := call(t, svc, action, body)
	return code
}

// --- Cluster tests ---

func TestCreateCluster(t *testing.T) {
	svc := newSvc()
	res := do(t, svc, "CreateCluster", map[string]interface{}{"clusterName": "my-cluster"})
	c := res["cluster"].(map[string]interface{})
	if c["clusterName"] != "my-cluster" {
		t.Errorf("expected clusterName my-cluster, got %v", c["clusterName"])
	}
	if c["status"] != "ACTIVE" {
		t.Errorf("expected ACTIVE, got %v", c["status"])
	}
}

func TestCreateClusterDefault(t *testing.T) {
	svc := newSvc()
	res := do(t, svc, "CreateCluster", map[string]interface{}{})
	c := res["cluster"].(map[string]interface{})
	if c["clusterName"] != "default" {
		t.Errorf("expected default cluster, got %v", c["clusterName"])
	}
}

func TestDefaultClusterExists(t *testing.T) {
	svc := newSvc()
	res := do(t, svc, "DescribeClusters", map[string]interface{}{"clusters": []string{"default"}})
	clusters := res["clusters"].([]interface{})
	if len(clusters) != 1 {
		t.Fatalf("expected 1 cluster, got %d", len(clusters))
	}
}

func TestDeleteCluster(t *testing.T) {
	svc := newSvc()
	do(t, svc, "CreateCluster", map[string]interface{}{"clusterName": "to-delete"})
	res := do(t, svc, "DeleteCluster", map[string]interface{}{"cluster": "to-delete"})
	c := res["cluster"].(map[string]interface{})
	if c["clusterName"] != "to-delete" {
		t.Errorf("expected to-delete in response")
	}
	// Describe should show empty
	res2 := do(t, svc, "DescribeClusters", map[string]interface{}{"clusters": []string{"to-delete"}})
	clusters := res2["clusters"].([]interface{})
	if len(clusters) != 0 {
		t.Errorf("expected 0 clusters after delete, got %d", len(clusters))
	}
}

func TestListClusters(t *testing.T) {
	svc := newSvc()
	do(t, svc, "CreateCluster", map[string]interface{}{"clusterName": "alpha"})
	do(t, svc, "CreateCluster", map[string]interface{}{"clusterName": "beta"})
	res := do(t, svc, "ListClusters", map[string]interface{}{})
	arns := res["clusterArns"].([]interface{})
	if len(arns) < 2 {
		t.Errorf("expected at least 2 cluster ARNs, got %d", len(arns))
	}
}

// --- Task definition tests ---

func TestRegisterTaskDefinition(t *testing.T) {
	svc := newSvc()
	res := do(t, svc, "RegisterTaskDefinition", map[string]interface{}{
		"family": "web",
		"containerDefinitions": []map[string]interface{}{
			{"name": "nginx", "image": "nginx:latest"},
		},
		"cpu":    "256",
		"memory": "512",
	})
	td := res["taskDefinition"].(map[string]interface{})
	if td["family"] != "web" {
		t.Errorf("expected family web, got %v", td["family"])
	}
	if td["revision"].(float64) != 1 {
		t.Errorf("expected revision 1, got %v", td["revision"])
	}
	if td["status"] != "ACTIVE" {
		t.Errorf("expected ACTIVE, got %v", td["status"])
	}
}

func TestRegisterTaskDefinitionIncrementsRevision(t *testing.T) {
	svc := newSvc()
	do(t, svc, "RegisterTaskDefinition", map[string]interface{}{"family": "app"})
	res := do(t, svc, "RegisterTaskDefinition", map[string]interface{}{"family": "app"})
	td := res["taskDefinition"].(map[string]interface{})
	if td["revision"].(float64) != 2 {
		t.Errorf("expected revision 2, got %v", td["revision"])
	}
}

func TestRegisterTaskDefinitionMissingFamily(t *testing.T) {
	svc := newSvc()
	code := status(t, svc, "RegisterTaskDefinition", map[string]interface{}{})
	if code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", code)
	}
}

func TestDeregisterTaskDefinition(t *testing.T) {
	svc := newSvc()
	do(t, svc, "RegisterTaskDefinition", map[string]interface{}{"family": "old"})
	res := do(t, svc, "DeregisterTaskDefinition", map[string]interface{}{"taskDefinition": "old:1"})
	td := res["taskDefinition"].(map[string]interface{})
	if td["status"] != "INACTIVE" {
		t.Errorf("expected INACTIVE, got %v", td["status"])
	}
}

func TestDescribeTaskDefinitionByFamily(t *testing.T) {
	svc := newSvc()
	do(t, svc, "RegisterTaskDefinition", map[string]interface{}{"family": "svc", "cpu": "512"})
	do(t, svc, "RegisterTaskDefinition", map[string]interface{}{"family": "svc", "cpu": "1024"})
	res := do(t, svc, "DescribeTaskDefinition", map[string]interface{}{"taskDefinition": "svc"})
	td := res["taskDefinition"].(map[string]interface{})
	if td["revision"].(float64) != 2 {
		t.Errorf("expected latest revision 2, got %v", td["revision"])
	}
}

func TestDescribeTaskDefinitionByFamilyRevision(t *testing.T) {
	svc := newSvc()
	do(t, svc, "RegisterTaskDefinition", map[string]interface{}{"family": "svc", "cpu": "256"})
	do(t, svc, "RegisterTaskDefinition", map[string]interface{}{"family": "svc", "cpu": "512"})
	res := do(t, svc, "DescribeTaskDefinition", map[string]interface{}{"taskDefinition": "svc:1"})
	td := res["taskDefinition"].(map[string]interface{})
	if td["revision"].(float64) != 1 {
		t.Errorf("expected revision 1, got %v", td["revision"])
	}
}

func TestListTaskDefinitions(t *testing.T) {
	svc := newSvc()
	do(t, svc, "RegisterTaskDefinition", map[string]interface{}{"family": "web"})
	do(t, svc, "RegisterTaskDefinition", map[string]interface{}{"family": "worker"})
	res := do(t, svc, "ListTaskDefinitions", map[string]interface{}{})
	arns := res["taskDefinitionArns"].([]interface{})
	if len(arns) != 2 {
		t.Errorf("expected 2 task defs, got %d", len(arns))
	}
}

func TestListTaskDefinitionsByFamilyPrefix(t *testing.T) {
	svc := newSvc()
	do(t, svc, "RegisterTaskDefinition", map[string]interface{}{"family": "web"})
	do(t, svc, "RegisterTaskDefinition", map[string]interface{}{"family": "worker"})
	res := do(t, svc, "ListTaskDefinitions", map[string]interface{}{"familyPrefix": "web"})
	arns := res["taskDefinitionArns"].([]interface{})
	if len(arns) != 1 {
		t.Errorf("expected 1 task def matching prefix, got %d", len(arns))
	}
}

func TestListTaskDefinitionFamilies(t *testing.T) {
	svc := newSvc()
	do(t, svc, "RegisterTaskDefinition", map[string]interface{}{"family": "api"})
	do(t, svc, "RegisterTaskDefinition", map[string]interface{}{"family": "api"})
	do(t, svc, "RegisterTaskDefinition", map[string]interface{}{"family": "worker"})
	res := do(t, svc, "ListTaskDefinitionFamilies", map[string]interface{}{})
	families := res["families"].([]interface{})
	if len(families) != 2 {
		t.Errorf("expected 2 unique families, got %d", len(families))
	}
}

// --- Task tests ---

func TestRunTask(t *testing.T) {
	svc := newSvc()
	do(t, svc, "RegisterTaskDefinition", map[string]interface{}{"family": "app"})
	res := do(t, svc, "RunTask", map[string]interface{}{
		"cluster":        "default",
		"taskDefinition": "app",
		"count":          2,
	})
	tasks := res["tasks"].([]interface{})
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(tasks))
	}
	t0 := tasks[0].(map[string]interface{})
	if t0["lastStatus"] != "RUNNING" {
		t.Errorf("expected RUNNING, got %v", t0["lastStatus"])
	}
}

func TestRunTaskDefaultCount(t *testing.T) {
	svc := newSvc()
	do(t, svc, "RegisterTaskDefinition", map[string]interface{}{"family": "app"})
	res := do(t, svc, "RunTask", map[string]interface{}{
		"taskDefinition": "app",
	})
	tasks := res["tasks"].([]interface{})
	if len(tasks) != 1 {
		t.Errorf("expected 1 task (default count), got %d", len(tasks))
	}
}

func TestRunTaskClusterNotFound(t *testing.T) {
	svc := newSvc()
	code := status(t, svc, "RunTask", map[string]interface{}{
		"cluster":        "nonexistent",
		"taskDefinition": "app",
	})
	if code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", code)
	}
}

func TestStopTask(t *testing.T) {
	svc := newSvc()
	do(t, svc, "RegisterTaskDefinition", map[string]interface{}{"family": "app"})
	runRes := do(t, svc, "RunTask", map[string]interface{}{"taskDefinition": "app"})
	tasks := runRes["tasks"].([]interface{})
	taskArn := tasks[0].(map[string]interface{})["taskArn"].(string)

	stopRes := do(t, svc, "StopTask", map[string]interface{}{
		"cluster": "default",
		"task":    taskArn,
	})
	t0 := stopRes["task"].(map[string]interface{})
	if t0["lastStatus"] != "STOPPED" {
		t.Errorf("expected STOPPED, got %v", t0["lastStatus"])
	}
}

func TestDescribeTasks(t *testing.T) {
	svc := newSvc()
	do(t, svc, "RegisterTaskDefinition", map[string]interface{}{"family": "app"})
	runRes := do(t, svc, "RunTask", map[string]interface{}{"taskDefinition": "app", "count": 2})
	tasks := runRes["tasks"].([]interface{})
	arns := []string{
		tasks[0].(map[string]interface{})["taskArn"].(string),
		tasks[1].(map[string]interface{})["taskArn"].(string),
	}

	res := do(t, svc, "DescribeTasks", map[string]interface{}{"cluster": "default", "tasks": arns})
	described := res["tasks"].([]interface{})
	if len(described) != 2 {
		t.Errorf("expected 2 tasks, got %d", len(described))
	}
}

func TestListTasks(t *testing.T) {
	svc := newSvc()
	do(t, svc, "RegisterTaskDefinition", map[string]interface{}{"family": "app"})
	do(t, svc, "RunTask", map[string]interface{}{"taskDefinition": "app", "count": 3})
	res := do(t, svc, "ListTasks", map[string]interface{}{"cluster": "default"})
	arns := res["taskArns"].([]interface{})
	if len(arns) != 3 {
		t.Errorf("expected 3 task ARNs, got %d", len(arns))
	}
}

func TestListTasksByDesiredStatus(t *testing.T) {
	svc := newSvc()
	do(t, svc, "RegisterTaskDefinition", map[string]interface{}{"family": "app"})
	runRes := do(t, svc, "RunTask", map[string]interface{}{"taskDefinition": "app", "count": 2})
	tasks := runRes["tasks"].([]interface{})
	taskArn := tasks[0].(map[string]interface{})["taskArn"].(string)

	do(t, svc, "StopTask", map[string]interface{}{"cluster": "default", "task": taskArn})

	res := do(t, svc, "ListTasks", map[string]interface{}{
		"cluster":       "default",
		"desiredStatus": "RUNNING",
	})
	arns := res["taskArns"].([]interface{})
	if len(arns) != 1 {
		t.Errorf("expected 1 running task, got %d", len(arns))
	}
}

// --- Service tests ---

func TestCreateService(t *testing.T) {
	svc := newSvc()
	do(t, svc, "RegisterTaskDefinition", map[string]interface{}{"family": "web"})
	res := do(t, svc, "CreateService", map[string]interface{}{
		"cluster":        "default",
		"serviceName":    "web-service",
		"taskDefinition": "web",
		"desiredCount":   3,
	})
	s := res["service"].(map[string]interface{})
	if s["serviceName"] != "web-service" {
		t.Errorf("expected web-service, got %v", s["serviceName"])
	}
	if s["desiredCount"].(float64) != 3 {
		t.Errorf("expected desiredCount 3, got %v", s["desiredCount"])
	}
	if s["runningCount"].(float64) != 3 {
		t.Errorf("expected runningCount 3 (simulated), got %v", s["runningCount"])
	}
	if s["status"] != "ACTIVE" {
		t.Errorf("expected ACTIVE, got %v", s["status"])
	}
}

func TestCreateServiceCreatesBackingTasks(t *testing.T) {
	svc := newSvc()
	do(t, svc, "RegisterTaskDefinition", map[string]interface{}{"family": "web"})
	do(t, svc, "CreateService", map[string]interface{}{
		"serviceName":    "web-service",
		"taskDefinition": "web",
		"desiredCount":   2,
	})
	res := do(t, svc, "ListTasks", map[string]interface{}{
		"cluster":     "default",
		"serviceName": "web-service",
	})
	arns := res["taskArns"].([]interface{})
	if len(arns) != 2 {
		t.Errorf("expected 2 backing tasks for service, got %d", len(arns))
	}
}

func TestUpdateService(t *testing.T) {
	svc := newSvc()
	do(t, svc, "RegisterTaskDefinition", map[string]interface{}{"family": "api"})
	do(t, svc, "CreateService", map[string]interface{}{
		"serviceName":    "api-service",
		"taskDefinition": "api",
		"desiredCount":   1,
	})
	desired := 5
	res := do(t, svc, "UpdateService", map[string]interface{}{
		"cluster":      "default",
		"service":      "api-service",
		"desiredCount": desired,
	})
	s := res["service"].(map[string]interface{})
	if s["desiredCount"].(float64) != 5 {
		t.Errorf("expected desiredCount 5, got %v", s["desiredCount"])
	}
}

func TestUpdateServiceTaskDefinition(t *testing.T) {
	svc := newSvc()
	do(t, svc, "RegisterTaskDefinition", map[string]interface{}{"family": "api"})
	do(t, svc, "RegisterTaskDefinition", map[string]interface{}{"family": "api"})
	do(t, svc, "CreateService", map[string]interface{}{
		"serviceName":    "api-service",
		"taskDefinition": "api:1",
		"desiredCount":   1,
	})
	res := do(t, svc, "UpdateService", map[string]interface{}{
		"cluster":        "default",
		"service":        "api-service",
		"taskDefinition": "api:2",
	})
	s := res["service"].(map[string]interface{})
	if !contains(s["taskDefinition"].(string), ":2") {
		t.Errorf("expected task def updated to revision 2, got %v", s["taskDefinition"])
	}
}

func TestDeleteService(t *testing.T) {
	svc := newSvc()
	do(t, svc, "RegisterTaskDefinition", map[string]interface{}{"family": "job"})
	do(t, svc, "CreateService", map[string]interface{}{
		"serviceName":    "job-service",
		"taskDefinition": "job",
		"desiredCount":   1,
	})
	do(t, svc, "DeleteService", map[string]interface{}{
		"cluster": "default",
		"service": "job-service",
	})
	// DescribeServices returns INACTIVE services when named explicitly (AWS behavior).
	// ListServices filters them out.
	res := do(t, svc, "DescribeServices", map[string]interface{}{
		"cluster":  "default",
		"services": []string{"job-service"},
	})
	services := res["services"].([]interface{})
	if len(services) != 1 {
		t.Fatalf("expected 1 INACTIVE service after delete, got %d", len(services))
	}
	status := services[0].(map[string]interface{})["status"].(string)
	if status != "INACTIVE" {
		t.Errorf("expected status INACTIVE, got %s", status)
	}

	listRes := do(t, svc, "ListServices", map[string]interface{}{"cluster": "default"})
	arns := listRes["serviceArns"].([]interface{})
	if len(arns) != 0 {
		t.Errorf("expected 0 arns in ListServices after delete, got %d", len(arns))
	}
}

func TestDescribeServices(t *testing.T) {
	svc := newSvc()
	do(t, svc, "RegisterTaskDefinition", map[string]interface{}{"family": "app"})
	do(t, svc, "CreateService", map[string]interface{}{
		"serviceName":    "app-service",
		"taskDefinition": "app",
		"desiredCount":   1,
	})
	res := do(t, svc, "DescribeServices", map[string]interface{}{
		"cluster":  "default",
		"services": []string{"app-service"},
	})
	services := res["services"].([]interface{})
	if len(services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(services))
	}
}

func TestListServices(t *testing.T) {
	svc := newSvc()
	do(t, svc, "RegisterTaskDefinition", map[string]interface{}{"family": "app"})
	do(t, svc, "CreateService", map[string]interface{}{"serviceName": "svc-a", "taskDefinition": "app", "desiredCount": 1})
	do(t, svc, "CreateService", map[string]interface{}{"serviceName": "svc-b", "taskDefinition": "app", "desiredCount": 1})
	res := do(t, svc, "ListServices", map[string]interface{}{"cluster": "default"})
	arns := res["serviceArns"].([]interface{})
	if len(arns) != 2 {
		t.Errorf("expected 2 service ARNs, got %d", len(arns))
	}
}

func TestCreateServiceMissingName(t *testing.T) {
	svc := newSvc()
	code := status(t, svc, "CreateService", map[string]interface{}{})
	if code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", code)
	}
}

// --- Service load balancer tests ---

// registerWebTaskDef registers a task definition whose "app" container publishes
// the given ports, plus a "sidecar" container with no port mappings at all.
func registerWebTaskDef(t *testing.T, svc *Service, family string, ports ...int) {
	t.Helper()
	mappings := []map[string]interface{}{}
	for _, p := range ports {
		mappings = append(mappings, map[string]interface{}{"containerPort": p, "protocol": "tcp"})
	}
	do(t, svc, "RegisterTaskDefinition", map[string]interface{}{
		"family": family,
		"containerDefinitions": []map[string]interface{}{
			{"name": "app", "image": "nginx:latest", "essential": true, "portMappings": mappings},
			{"name": "sidecar", "image": "busybox:latest"},
		},
	})
}

const tgArn = "arn:aws:elasticloadbalancing:us-east-1:000000000000:targetgroup/web/abc123"

func TestCreateServiceWithLoadBalancer(t *testing.T) {
	svc := newSvc()
	registerWebTaskDef(t, svc, "web", 80)
	code, res := call(t, svc, "CreateService", map[string]interface{}{
		"serviceName":    "web-service",
		"taskDefinition": "web",
		"desiredCount":   1,
		"loadBalancers": []map[string]interface{}{
			{"targetGroupArn": tgArn, "containerName": "app", "containerPort": 80},
		},
	})
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %v", code, res)
	}
	lbs := res["service"].(map[string]interface{})["loadBalancers"].([]interface{})
	if len(lbs) != 1 {
		t.Fatalf("expected 1 load balancer in response, got %d", len(lbs))
	}
	lb := lbs[0].(map[string]interface{})
	if lb["targetGroupArn"] != tgArn {
		t.Errorf("expected targetGroupArn %s, got %v", tgArn, lb["targetGroupArn"])
	}
	if lb["containerName"] != "app" {
		t.Errorf("expected containerName app, got %v", lb["containerName"])
	}
	if lb["containerPort"].(float64) != 80 {
		t.Errorf("expected containerPort 80, got %v", lb["containerPort"])
	}
}

func TestCreateServiceLoadBalancerPortNotDefined(t *testing.T) {
	svc := newSvc()
	registerWebTaskDef(t, svc, "web", 8080)
	code, res := call(t, svc, "CreateService", map[string]interface{}{
		"serviceName":    "web-service",
		"taskDefinition": "web",
		"desiredCount":   1,
		"loadBalancers": []map[string]interface{}{
			{"targetGroupArn": tgArn, "containerName": "app", "containerPort": 80},
		},
	})
	if code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", code)
	}
	if res["__type"] != "InvalidParameterException" {
		t.Errorf("expected InvalidParameterException, got %v", res["__type"])
	}
	if msg := res["message"].(string); msg != "The container app did not have a container port 80 defined." {
		t.Errorf("unexpected message: %q", msg)
	}
	// The service must not have been created.
	list := do(t, svc, "ListServices", map[string]interface{}{"cluster": "default"})
	if arns := list["serviceArns"].([]interface{}); len(arns) != 0 {
		t.Errorf("expected no services after rejected create, got %d", len(arns))
	}
}

func TestCreateServiceLoadBalancerNoPortMappings(t *testing.T) {
	svc := newSvc()
	registerWebTaskDef(t, svc, "web", 80)
	code, res := call(t, svc, "CreateService", map[string]interface{}{
		"serviceName":    "web-service",
		"taskDefinition": "web",
		"desiredCount":   1,
		"loadBalancers": []map[string]interface{}{
			{"targetGroupArn": tgArn, "containerName": "sidecar", "containerPort": 80},
		},
	})
	if code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", code)
	}
	if msg := res["message"].(string); msg != "The container sidecar did not have a container port 80 defined." {
		t.Errorf("unexpected message: %q", msg)
	}
}

func TestCreateServiceLoadBalancerUnknownContainer(t *testing.T) {
	svc := newSvc()
	registerWebTaskDef(t, svc, "web", 80)
	code, res := call(t, svc, "CreateService", map[string]interface{}{
		"serviceName":    "web-service",
		"taskDefinition": "web",
		"desiredCount":   1,
		"loadBalancers": []map[string]interface{}{
			{"targetGroupArn": tgArn, "containerName": "nope", "containerPort": 80},
		},
	})
	if code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", code)
	}
	if msg := res["message"].(string); msg != "The container nope does not exist in the task definition." {
		t.Errorf("unexpected message: %q", msg)
	}
}

func TestDescribeServicesReportsLoadBalancers(t *testing.T) {
	svc := newSvc()
	registerWebTaskDef(t, svc, "web", 80, 8443)
	do(t, svc, "CreateService", map[string]interface{}{
		"serviceName":    "web-service",
		"taskDefinition": "web",
		"desiredCount":   1,
		"loadBalancers": []map[string]interface{}{
			{"targetGroupArn": tgArn, "containerName": "app", "containerPort": 8443},
		},
	})
	res := do(t, svc, "DescribeServices", map[string]interface{}{
		"cluster":  "default",
		"services": []string{"web-service"},
	})
	services := res["services"].([]interface{})
	if len(services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(services))
	}
	lbs := services[0].(map[string]interface{})["loadBalancers"].([]interface{})
	if len(lbs) != 1 {
		t.Fatalf("expected 1 load balancer, got %d", len(lbs))
	}
	if lbs[0].(map[string]interface{})["containerPort"].(float64) != 8443 {
		t.Errorf("expected containerPort 8443, got %v", lbs[0])
	}
}

// A service without load balancers must still report an empty list, not null —
// the TF provider reads the field on every refresh.
func TestDescribeServicesEmptyLoadBalancers(t *testing.T) {
	svc := newSvc()
	registerWebTaskDef(t, svc, "web", 80)
	res := do(t, svc, "CreateService", map[string]interface{}{
		"serviceName":    "web-service",
		"taskDefinition": "web",
		"desiredCount":   1,
	})
	lbs, ok := res["service"].(map[string]interface{})["loadBalancers"].([]interface{})
	if !ok {
		t.Fatalf("expected loadBalancers to be a list, got %#v",
			res["service"].(map[string]interface{})["loadBalancers"])
	}
	if len(lbs) != 0 {
		t.Errorf("expected 0 load balancers, got %d", len(lbs))
	}
}

func TestUpdateServiceLoadBalancers(t *testing.T) {
	svc := newSvc()
	registerWebTaskDef(t, svc, "web", 80, 8443)
	do(t, svc, "CreateService", map[string]interface{}{
		"serviceName":    "web-service",
		"taskDefinition": "web",
		"desiredCount":   1,
		"loadBalancers": []map[string]interface{}{
			{"targetGroupArn": tgArn, "containerName": "app", "containerPort": 80},
		},
	})
	code, res := call(t, svc, "UpdateService", map[string]interface{}{
		"cluster": "default",
		"service": "web-service",
		"loadBalancers": []map[string]interface{}{
			{"targetGroupArn": tgArn, "containerName": "app", "containerPort": 8443},
		},
	})
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %v", code, res)
	}
	lbs := res["service"].(map[string]interface{})["loadBalancers"].([]interface{})
	if len(lbs) != 1 || lbs[0].(map[string]interface{})["containerPort"].(float64) != 8443 {
		t.Errorf("expected containerPort updated to 8443, got %v", lbs)
	}
}

func TestUpdateServiceLoadBalancerPortNotDefined(t *testing.T) {
	svc := newSvc()
	registerWebTaskDef(t, svc, "web", 80)
	do(t, svc, "CreateService", map[string]interface{}{
		"serviceName":    "web-service",
		"taskDefinition": "web",
		"desiredCount":   1,
	})
	code, res := call(t, svc, "UpdateService", map[string]interface{}{
		"cluster": "default",
		"service": "web-service",
		"loadBalancers": []map[string]interface{}{
			{"targetGroupArn": tgArn, "containerName": "app", "containerPort": 9999},
		},
	})
	if code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", code)
	}
	if msg := res["message"].(string); msg != "The container app did not have a container port 9999 defined." {
		t.Errorf("unexpected message: %q", msg)
	}
}

// UpdateService validates against the task definition set in the same call.
func TestUpdateServiceLoadBalancersAgainstNewTaskDef(t *testing.T) {
	svc := newSvc()
	registerWebTaskDef(t, svc, "web", 80)   // web:1 — port 80 only
	registerWebTaskDef(t, svc, "web", 8443) // web:2 — port 8443 only
	do(t, svc, "CreateService", map[string]interface{}{
		"serviceName":    "web-service",
		"taskDefinition": "web:1",
		"desiredCount":   1,
		"loadBalancers": []map[string]interface{}{
			{"targetGroupArn": tgArn, "containerName": "app", "containerPort": 80},
		},
	})
	code, _ := call(t, svc, "UpdateService", map[string]interface{}{
		"cluster":        "default",
		"service":        "web-service",
		"taskDefinition": "web:2",
		"loadBalancers": []map[string]interface{}{
			{"targetGroupArn": tgArn, "containerName": "app", "containerPort": 8443},
		},
	})
	if code != http.StatusOK {
		t.Fatalf("expected 200 for port defined in the new task def, got %d", code)
	}
}

// --- Tag tests ---

func TestTagAndListTags(t *testing.T) {
	svc := newSvc()
	do(t, svc, "CreateCluster", map[string]interface{}{"clusterName": "tagged"})
	arn := svc.clusterARN("tagged")
	do(t, svc, "TagResource", map[string]interface{}{
		"resourceArn": arn,
		"tags":        []map[string]string{{"key": "env", "value": "prod"}},
	})
	res := do(t, svc, "ListTagsForResource", map[string]interface{}{"resourceArn": arn})
	tags := res["tags"].([]interface{})
	if len(tags) != 1 {
		t.Fatalf("expected 1 tag, got %d", len(tags))
	}
	tag := tags[0].(map[string]interface{})
	if tag["key"] != "env" || tag["value"] != "prod" {
		t.Errorf("unexpected tag: %v", tag)
	}
}

func TestUntagResource(t *testing.T) {
	svc := newSvc()
	do(t, svc, "CreateCluster", map[string]interface{}{"clusterName": "c1"})
	arn := svc.clusterARN("c1")
	do(t, svc, "TagResource", map[string]interface{}{
		"resourceArn": arn,
		"tags":        []map[string]string{{"key": "k1", "value": "v1"}, {"key": "k2", "value": "v2"}},
	})
	do(t, svc, "UntagResource", map[string]interface{}{
		"resourceArn": arn,
		"tagKeys":     []string{"k1"},
	})
	res := do(t, svc, "ListTagsForResource", map[string]interface{}{"resourceArn": arn})
	tags := res["tags"].([]interface{})
	if len(tags) != 1 {
		t.Errorf("expected 1 tag after untag, got %d", len(tags))
	}
}

// --- Detect test ---

func TestDetect(t *testing.T) {
	svc := newSvc()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-Amz-Target", ecsTarget+"CreateCluster")
	if !svc.Detect(req) {
		t.Error("expected Detect to return true for ECS target")
	}
	req2 := httptest.NewRequest(http.MethodPost, "/", nil)
	req2.Header.Set("X-Amz-Target", "SomeOtherService.Action")
	if svc.Detect(req2) {
		t.Error("expected Detect to return false for non-ECS target")
	}
}

func TestName(t *testing.T) {
	svc := newSvc()
	if svc.Name() != "ecs" {
		t.Errorf("expected Name()=ecs, got %s", svc.Name())
	}
}

// --- Helpers ---

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
			return false
		}())
}

// --- Scheduling strategy ---

// schedulingStrategy is ForceNew for the Terraform provider: omitting it from
// DescribeServices made every re-apply plan a service replacement.
func TestCreateServiceDefaultsSchedulingStrategy(t *testing.T) {
	svc := newSvc()
	do(t, svc, "RegisterTaskDefinition", map[string]interface{}{"family": "web"})
	res := do(t, svc, "CreateService", map[string]interface{}{
		"serviceName":    "web-service",
		"taskDefinition": "web",
		"desiredCount":   1,
	})
	s := res["service"].(map[string]interface{})
	if s["schedulingStrategy"] != "REPLICA" {
		t.Errorf("expected REPLICA by default, got %v", s["schedulingStrategy"])
	}
}

func TestCreateServiceRoundTripsSchedulingStrategy(t *testing.T) {
	svc := newSvc()
	do(t, svc, "RegisterTaskDefinition", map[string]interface{}{"family": "agent"})
	res := do(t, svc, "CreateService", map[string]interface{}{
		"serviceName":        "agent-service",
		"taskDefinition":     "agent",
		"desiredCount":       1,
		"schedulingStrategy": "DAEMON",
	})
	s := res["service"].(map[string]interface{})
	if s["schedulingStrategy"] != "DAEMON" {
		t.Errorf("expected DAEMON to round-trip, got %v", s["schedulingStrategy"])
	}
}

// DescribeServices is what the provider reads on every plan.
func TestDescribeServicesReportsSchedulingStrategy(t *testing.T) {
	svc := newSvc()
	do(t, svc, "RegisterTaskDefinition", map[string]interface{}{"family": "agent"})
	do(t, svc, "CreateService", map[string]interface{}{
		"serviceName":        "agent-service",
		"taskDefinition":     "agent",
		"desiredCount":       1,
		"schedulingStrategy": "DAEMON",
	})
	res := do(t, svc, "DescribeServices", map[string]interface{}{
		"cluster":  "default",
		"services": []string{"agent-service"},
	})
	services := res["services"].([]interface{})
	if len(services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(services))
	}
	if got := services[0].(map[string]interface{})["schedulingStrategy"]; got != "DAEMON" {
		t.Errorf("DescribeServices reported %v, want DAEMON", got)
	}
}

// An update must not disturb it — the provider modifies a service in place and
// then re-reads it, and a changed strategy would force a replacement.
func TestUpdateServicePreservesSchedulingStrategy(t *testing.T) {
	svc := newSvc()
	do(t, svc, "RegisterTaskDefinition", map[string]interface{}{"family": "agent"})
	do(t, svc, "CreateService", map[string]interface{}{
		"serviceName":        "agent-service",
		"taskDefinition":     "agent",
		"desiredCount":       1,
		"schedulingStrategy": "DAEMON",
	})
	res := do(t, svc, "UpdateService", map[string]interface{}{
		"service":      "agent-service",
		"desiredCount": 3,
	})
	s := res["service"].(map[string]interface{})
	if s["schedulingStrategy"] != "DAEMON" {
		t.Errorf("UpdateService dropped schedulingStrategy: %v", s["schedulingStrategy"])
	}
}

// A service record predating the field still reads back as REPLICA rather than
// as an empty string, which the provider would treat as drift.
func TestServiceMetaDefaultsEmptySchedulingStrategy(t *testing.T) {
	meta := serviceMeta(&ecsService{name: "legacy", status: "ACTIVE"})
	if meta["schedulingStrategy"] != "REPLICA" {
		t.Errorf("expected REPLICA for an unset strategy, got %v", meta["schedulingStrategy"])
	}
}
