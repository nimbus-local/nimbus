// Package ecs emulates the AWS Elastic Container Service (ECS) control plane
// for local development. No containers are actually executed; tasks are
// simulated as immediately RUNNING. All state is in-memory.
package ecs

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nimbus-local/nimbus/internal/uid"
)

const (
	accountID = "000000000000"
	ecsTarget = "AmazonEC2ContainerServiceV20141113."
)

// Service implements the ECS control plane (clusters, task definitions,
// tasks, and services). When the Docker CLI is reachable, RunTask/CreateService
// start real containers via exec; otherwise tasks are simulated as immediately RUNNING.
type Service struct {
	mu          sync.RWMutex
	clusters    map[string]*cluster          // name -> cluster
	taskDefs    map[string]*taskDef          // "family:revision" -> taskDef
	taskFams    map[string]int               // family -> latest revision number
	tasks       map[string]*ecsTask          // taskArn -> task
	services    map[string]*ecsService       // serviceArn -> service
	tags        map[string]map[string]string // resourceArn -> tags
	region      string
	dockerAvail bool   // true → shell out to docker CLI; false → simulate
	networkName string // Docker network to attach containers to

	// Container Insights performance events — see insights.go.
	insights         InsightsSink
	insightsInterval time.Duration
	insightsDelay    time.Duration
	lastInsightsSlot time.Time
	insightsCounters map[string]*containerCounters // "taskArn/container" -> synthetic state
}

type cluster struct {
	name                string
	arn                 string
	status              string
	runningTasksCount   int
	pendingTasksCount   int
	activeServicesCount int
	settings            map[string]string // setting name -> value; see clusterSetting
	createdAt           time.Time
}

// clusterSetting is one entry of a cluster's settings block. Nimbus stores every
// setting so it reads back on DescribeClusters, but only containerInsights
// changes behaviour: see insights.go.
type clusterSetting struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type taskDef struct {
	family                  string
	revision                int
	arn                     string
	status                  string
	containerDefinitions    []json.RawMessage
	networkMode             string
	cpu                     string
	memory                  string
	requiresCompatibilities []string
	executionRoleArn        string
	taskRoleArn             string
	registeredAt            time.Time
}

type ecsTask struct {
	taskArn       string
	taskDefArn    string
	clusterArn    string
	group         string
	launchType    string
	cpu           string
	memory        string
	lastStatus    string
	desiredStatus string
	stoppedReason string
	startedAt     time.Time
	createdAt     time.Time
	containers    map[string]string // container name → Docker container ID; nil in simulation mode
}

type ecsService struct {
	name         string
	arn          string
	clusterArn   string
	taskDefArn   string
	desiredCount int
	runningCount int
	pendingCount int
	launchType   string
	// schedulingStrategy is REPLICA or DAEMON. Nimbus schedules the same way for
	// both, but the value is ForceNew for the Terraform provider, so it has to
	// read back — see serviceMeta.
	schedulingStrategy string
	status             string
	loadBalancers      []loadBalancer
	// networkConfiguration is stored verbatim. Nimbus attaches containers to its
	// own Docker network rather than to these subnets, but the block has to read
	// back or the provider re-applies it on every plan.
	networkConfig *networkConfiguration
	createdAt     time.Time
}

// networkConfiguration mirrors the awsvpcConfiguration block of CreateService.
type networkConfiguration struct {
	AwsvpcConfiguration *awsvpcConfiguration `json:"awsvpcConfiguration,omitempty"`
}

type awsvpcConfiguration struct {
	Subnets        []string `json:"subnets"`
	SecurityGroups []string `json:"securityGroups,omitempty"`
	AssignPublicIp string   `json:"assignPublicIp,omitempty"`
}

// loadBalancer is one entry of a service's loadBalancers block. Nimbus does not
// verify that the target group or load balancer exists, but it does validate
// containerName/containerPort against the task definition the way real ECS does.
type loadBalancer struct {
	TargetGroupArn   string `json:"targetGroupArn,omitempty"`
	LoadBalancerName string `json:"loadBalancerName,omitempty"`
	ContainerName    string `json:"containerName"`
	ContainerPort    int    `json:"containerPort"`
}

func New(region string) *Service {
	if region == "" {
		region = "us-east-1"
	}
	network := os.Getenv("NIMBUS_DOCKER_NETWORK")
	if network == "" {
		network = "nimbus-net"
	}
	s := &Service{
		region:           region,
		networkName:      network,
		clusters:         map[string]*cluster{},
		taskDefs:         map[string]*taskDef{},
		taskFams:         map[string]int{},
		tasks:            map[string]*ecsTask{},
		services:         map[string]*ecsService{},
		tags:             map[string]map[string]string{},
		insightsInterval: durationFromEnv("NIMBUS_ECS_INSIGHTS_INTERVAL", defaultInsightsInterval),
		insightsDelay:    durationFromEnv("NIMBUS_ECS_INSIGHTS_DELAY", defaultInsightsDelay),
		insightsCounters: map[string]*containerCounters{},
	}
	s.makeCluster("default")
	s.dockerAvail = initDocker()
	if s.dockerAvail {
		go s.pollTaskLifecycle()
		go s.reconcileServices()
	}
	return s
}

func (s *Service) Name() string { return "ecs" }

// Reset clears all in-memory state and re-creates the default cluster.
func (s *Service) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clusters = map[string]*cluster{}
	s.taskDefs = map[string]*taskDef{}
	s.taskFams = map[string]int{}
	s.tasks = map[string]*ecsTask{}
	s.services = map[string]*ecsService{}
	s.tags = map[string]map[string]string{}
	s.insightsCounters = map[string]*containerCounters{}
	s.lastInsightsSlot = time.Time{}
	s.makeCluster("default")
}

func (s *Service) Detect(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("X-Amz-Target"), ecsTarget)
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	action := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), ecsTarget)
	switch action {
	// Clusters
	case "CreateCluster":
		s.createCluster(w, r)
	case "DeleteCluster":
		s.deleteCluster(w, r)
	case "DescribeClusters":
		s.describeClusters(w, r)
	case "ListClusters":
		s.listClusters(w, r)
	case "UpdateClusterSettings", "UpdateCluster":
		s.updateClusterSettings(w, r)
	// Task definitions
	case "RegisterTaskDefinition":
		s.registerTaskDefinition(w, r)
	case "DeregisterTaskDefinition":
		s.deregisterTaskDefinition(w, r)
	case "DescribeTaskDefinition":
		s.describeTaskDefinition(w, r)
	case "ListTaskDefinitions":
		s.listTaskDefinitions(w, r)
	case "ListTaskDefinitionFamilies":
		s.listTaskDefinitionFamilies(w, r)
	// Tasks
	case "RunTask":
		s.runTask(w, r)
	case "StopTask":
		s.stopTask(w, r)
	case "DescribeTasks":
		s.describeTasks(w, r)
	case "ListTasks":
		s.listTasks(w, r)
	// Services
	case "CreateService":
		s.createService(w, r)
	case "UpdateService":
		s.updateService(w, r)
	case "DeleteService":
		s.deleteService(w, r)
	case "DescribeServices":
		s.describeServices(w, r)
	case "ListServices":
		s.listServices(w, r)
	// Tags
	case "TagResource":
		s.tagResource(w, r)
	case "UntagResource":
		s.untagResource(w, r)
	case "ListTagsForResource":
		s.listTagsForResource(w, r)
	default:
		jsonError(w, http.StatusBadRequest, "UnsupportedOperationException",
			fmt.Sprintf("Operation %s is not supported.", action))
	}
}

// --- Clusters ---

func (s *Service) makeCluster(name string) *cluster {
	c := &cluster{
		name:      name,
		arn:       s.clusterARN(name),
		status:    "ACTIVE",
		settings:  map[string]string{},
		createdAt: time.Now().UTC(),
	}
	s.clusters[name] = c
	return c
}

func (s *Service) createCluster(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ClusterName string              `json:"clusterName"`
		Tags        []map[string]string `json:"tags"`
		Settings    []clusterSetting    `json:"settings"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if req.ClusterName == "" {
		req.ClusterName = "default"
	}

	s.mu.Lock()
	c, exists := s.clusters[req.ClusterName]
	if !exists {
		c = s.makeCluster(req.ClusterName)
		for _, t := range req.Tags {
			s.setTag(c.arn, t["key"], t["value"])
		}
		for _, st := range req.Settings {
			c.settings[st.Name] = st.Value
		}
	}
	meta := clusterMeta(c)
	s.mu.Unlock()

	jsonWrite(w, http.StatusOK, map[string]interface{}{"cluster": meta})
}

// updateClusterSettings serves both UpdateClusterSettings and UpdateCluster.
// Terraform reaches for one or the other depending on which attribute of
// aws_ecs_cluster changed, and both carry the same settings block.
func (s *Service) updateClusterSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cluster  string           `json:"cluster"`
		Settings []clusterSetting `json:"settings"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.Lock()
	c, ok := s.resolveCluster(req.Cluster)
	var meta map[string]interface{}
	if ok {
		for _, st := range req.Settings {
			c.settings[st.Name] = st.Value
		}
		meta = clusterMeta(c)
	}
	s.mu.Unlock()

	if !ok {
		jsonError(w, http.StatusBadRequest, "ClusterNotFoundException", "cluster not found")
		return
	}
	jsonWrite(w, http.StatusOK, map[string]interface{}{"cluster": meta})
}

func (s *Service) deleteCluster(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cluster string `json:"cluster"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.Lock()
	c, ok := s.resolveCluster(req.Cluster)
	var meta map[string]interface{}
	if ok {
		delete(s.clusters, c.name)
		meta = clusterMeta(c)
	}
	s.mu.Unlock()

	if !ok {
		jsonError(w, http.StatusBadRequest, "ClusterNotFoundException", "Cluster not found")
		return
	}
	jsonWrite(w, http.StatusOK, map[string]interface{}{"cluster": meta})
}

func (s *Service) describeClusters(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Clusters []string `json:"clusters"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []map[string]interface{}
	if len(req.Clusters) == 0 {
		for _, c := range s.clusters {
			result = append(result, clusterMeta(c))
		}
	} else {
		for _, id := range req.Clusters {
			if c, ok := s.resolveCluster(id); ok {
				result = append(result, clusterMeta(c))
			}
		}
	}
	if result == nil {
		result = []map[string]interface{}{}
	}
	jsonWrite(w, http.StatusOK, map[string]interface{}{"clusters": result, "failures": []interface{}{}})
}

func (s *Service) listClusters(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	arns := []string{}
	for _, c := range s.clusters {
		arns = append(arns, c.arn)
	}
	jsonWrite(w, http.StatusOK, map[string]interface{}{"clusterArns": arns})
}

// --- Task Definitions ---

func (s *Service) registerTaskDefinition(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Family                  string            `json:"family"`
		ContainerDefinitions    []json.RawMessage `json:"containerDefinitions"`
		NetworkMode             string            `json:"networkMode"`
		CPU                     string            `json:"cpu"`
		Memory                  string            `json:"memory"`
		RequiresCompatibilities []string          `json:"requiresCompatibilities"`
		ExecutionRoleArn        string            `json:"executionRoleArn"`
		TaskRoleArn             string            `json:"taskRoleArn"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Family == "" {
		jsonError(w, http.StatusBadRequest, "InvalidParameterException", "family is required")
		return
	}

	s.mu.Lock()
	revision := s.taskFams[req.Family] + 1
	s.taskFams[req.Family] = revision
	td := &taskDef{
		family:                  req.Family,
		revision:                revision,
		arn:                     s.taskDefARN(req.Family, revision),
		status:                  "ACTIVE",
		containerDefinitions:    req.ContainerDefinitions,
		networkMode:             req.NetworkMode,
		cpu:                     req.CPU,
		memory:                  req.Memory,
		requiresCompatibilities: req.RequiresCompatibilities,
		executionRoleArn:        req.ExecutionRoleArn,
		taskRoleArn:             req.TaskRoleArn,
		registeredAt:            time.Now().UTC(),
	}
	if len(td.containerDefinitions) == 0 {
		td.containerDefinitions = []json.RawMessage{}
	}
	if len(td.requiresCompatibilities) == 0 {
		td.requiresCompatibilities = []string{"EC2"}
	}
	s.taskDefs[tdKey(req.Family, revision)] = td
	s.mu.Unlock()

	jsonWrite(w, http.StatusOK, map[string]interface{}{"taskDefinition": taskDefMeta(td)})
}

func (s *Service) deregisterTaskDefinition(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TaskDefinition string `json:"taskDefinition"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.Lock()
	td, ok := s.resolveTaskDef(req.TaskDefinition)
	if ok {
		td.status = "INACTIVE"
	}
	s.mu.Unlock()

	if !ok {
		jsonError(w, http.StatusBadRequest, "InvalidParameterException", "task definition not found")
		return
	}
	jsonWrite(w, http.StatusOK, map[string]interface{}{"taskDefinition": taskDefMeta(td)})
}

func (s *Service) describeTaskDefinition(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TaskDefinition string `json:"taskDefinition"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.RLock()
	td, ok := s.resolveTaskDef(req.TaskDefinition)
	s.mu.RUnlock()

	if !ok {
		jsonError(w, http.StatusBadRequest, "ClientException", "task definition not found")
		return
	}
	jsonWrite(w, http.StatusOK, map[string]interface{}{"taskDefinition": taskDefMeta(td)})
}

func (s *Service) listTaskDefinitions(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FamilyPrefix string `json:"familyPrefix"`
		Status       string `json:"status"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.RLock()
	defer s.mu.RUnlock()

	arns := []string{}
	for _, td := range s.taskDefs {
		if req.FamilyPrefix != "" && !strings.HasPrefix(td.family, req.FamilyPrefix) {
			continue
		}
		if req.Status != "" && td.status != req.Status {
			continue
		}
		arns = append(arns, td.arn)
	}
	jsonWrite(w, http.StatusOK, map[string]interface{}{"taskDefinitionArns": arns})
}

func (s *Service) listTaskDefinitionFamilies(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FamilyPrefix string `json:"familyPrefix"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.RLock()
	defer s.mu.RUnlock()

	seen := map[string]bool{}
	families := []string{}
	for _, td := range s.taskDefs {
		if req.FamilyPrefix != "" && !strings.HasPrefix(td.family, req.FamilyPrefix) {
			continue
		}
		if !seen[td.family] {
			seen[td.family] = true
			families = append(families, td.family)
		}
	}
	jsonWrite(w, http.StatusOK, map[string]interface{}{"families": families})
}

// --- Tasks ---

// createTaskRecord creates a task record and stores it. Caller must hold s.mu (write).
// In Docker mode the task starts as PENDING; the lifecycle goroutine transitions
// it to RUNNING once containers are up. In simulation mode it is immediately RUNNING.
func (s *Service) createTaskRecord(c *cluster, td *taskDef, group, launchType string) *ecsTask {
	now := time.Now().UTC()
	status := "RUNNING"
	if s.dockerAvail && hasContainerImages(td) {
		status = "PENDING"
	}
	t := &ecsTask{
		taskArn:       s.taskARN(c.name, uid.New()),
		taskDefArn:    td.arn,
		clusterArn:    c.arn,
		group:         group,
		launchType:    launchType,
		cpu:           td.cpu,
		memory:        td.memory,
		lastStatus:    status,
		desiredStatus: "RUNNING",
		startedAt:     now,
		createdAt:     now,
		containers:    map[string]string{},
	}
	s.tasks[t.taskArn] = t
	if status == "RUNNING" {
		c.runningTasksCount++
	}
	return t
}

func (s *Service) runTask(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cluster        string `json:"cluster"`
		TaskDefinition string `json:"taskDefinition"`
		Count          int    `json:"count"`
		LaunchType     string `json:"launchType"`
		Group          string `json:"group"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	if req.Cluster == "" {
		req.Cluster = "default"
	}
	if req.Count <= 0 {
		req.Count = 1
	}
	if req.LaunchType == "" {
		req.LaunchType = "FARGATE"
	}

	s.mu.Lock()
	c, ok := s.resolveCluster(req.Cluster)
	if !ok {
		s.mu.Unlock()
		jsonError(w, http.StatusBadRequest, "ClusterNotFoundException", "cluster not found")
		return
	}
	td, ok := s.resolveTaskDef(req.TaskDefinition)
	if !ok {
		s.mu.Unlock()
		jsonError(w, http.StatusBadRequest, "InvalidParameterException", "task definition not found")
		return
	}

	tdCopy := *td
	var newTasks []*ecsTask
	for i := 0; i < req.Count; i++ {
		newTasks = append(newTasks, s.createTaskRecord(c, td, req.Group, req.LaunchType))
	}
	s.mu.Unlock()

	// Start real containers asynchronously; task transitions PENDING→RUNNING via the poller.
	if s.dockerAvail && hasContainerImages(&tdCopy) {
		for _, t := range newTasks {
			arn := t.taskArn
			td := tdCopy
			go s.startTaskAsync(arn, &td)
		}
	}

	s.mu.RLock()
	var tasks []map[string]interface{}
	for _, t := range newTasks {
		if latest, ok := s.tasks[t.taskArn]; ok {
			tasks = append(tasks, taskMeta(latest))
		}
	}
	s.mu.RUnlock()
	if tasks == nil {
		tasks = []map[string]interface{}{}
	}

	jsonWrite(w, http.StatusOK, map[string]interface{}{"tasks": tasks, "failures": []interface{}{}})
}

func (s *Service) stopTask(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cluster string `json:"cluster"`
		Task    string `json:"task"`
		Reason  string `json:"reason"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.Lock()
	t, ok := s.resolveTask(req.Cluster, req.Task)
	var meta map[string]interface{}
	var containerIDs map[string]string
	if ok {
		if t.lastStatus == "RUNNING" {
			if c, cok := s.resolveCluster(req.Cluster); cok {
				c.runningTasksCount--
			}
		}
		t.lastStatus = "STOPPED"
		t.desiredStatus = "STOPPED"
		if req.Reason != "" {
			t.stoppedReason = req.Reason
		}
		if len(t.containers) > 0 {
			containerIDs = make(map[string]string, len(t.containers))
			for k, v := range t.containers {
				containerIDs[k] = v
			}
		}
		meta = taskMeta(t)
	}
	s.mu.Unlock()

	if !ok {
		jsonError(w, http.StatusBadRequest, "InvalidParameterException", "task not found")
		return
	}

	if s.dockerAvail && len(containerIDs) > 0 {
		go s.stopTaskContainers(containerIDs)
	}

	jsonWrite(w, http.StatusOK, map[string]interface{}{"task": meta})
}

func (s *Service) describeTasks(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cluster string   `json:"cluster"`
		Tasks   []string `json:"tasks"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []map[string]interface{}
	for _, id := range req.Tasks {
		if t, ok := s.resolveTask(req.Cluster, id); ok {
			result = append(result, taskMeta(t))
		}
	}
	if result == nil {
		result = []map[string]interface{}{}
	}
	jsonWrite(w, http.StatusOK, map[string]interface{}{"tasks": result, "failures": []interface{}{}})
}

func (s *Service) listTasks(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cluster       string `json:"cluster"`
		ServiceName   string `json:"serviceName"`
		Family        string `json:"family"`
		DesiredStatus string `json:"desiredStatus"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.RLock()
	defer s.mu.RUnlock()

	var clusterArn string
	if req.Cluster != "" {
		if c, ok := s.resolveCluster(req.Cluster); ok {
			clusterArn = c.arn
		}
	}

	arns := []string{}
	for _, t := range s.tasks {
		if clusterArn != "" && t.clusterArn != clusterArn {
			continue
		}
		if req.DesiredStatus != "" && t.desiredStatus != req.DesiredStatus {
			continue
		}
		if req.ServiceName != "" {
			expectedGroup := "service:" + req.ServiceName
			if t.group != expectedGroup {
				continue
			}
		}
		arns = append(arns, t.taskArn)
	}
	jsonWrite(w, http.StatusOK, map[string]interface{}{"taskArns": arns})
}

// --- Services ---

func (s *Service) createService(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cluster            string                `json:"cluster"`
		ServiceName        string                `json:"serviceName"`
		TaskDefinition     string                `json:"taskDefinition"`
		DesiredCount       int                   `json:"desiredCount"`
		LaunchType         string                `json:"launchType"`
		SchedulingStrategy string                `json:"schedulingStrategy"`
		LoadBalancers      []loadBalancer        `json:"loadBalancers"`
		NetworkConfig      *networkConfiguration `json:"networkConfiguration"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ServiceName == "" {
		jsonError(w, http.StatusBadRequest, "InvalidParameterException", "serviceName is required")
		return
	}
	if req.Cluster == "" {
		req.Cluster = "default"
	}
	if req.LaunchType == "" {
		req.LaunchType = "FARGATE"
	}
	if req.SchedulingStrategy == "" {
		req.SchedulingStrategy = "REPLICA" // the AWS default, and the provider's
	}

	s.mu.Lock()
	c, ok := s.resolveCluster(req.Cluster)
	if !ok {
		s.mu.Unlock()
		jsonError(w, http.StatusBadRequest, "ClusterNotFoundException", "cluster not found")
		return
	}
	td, ok := s.resolveTaskDef(req.TaskDefinition)
	if !ok {
		s.mu.Unlock()
		jsonError(w, http.StatusBadRequest, "InvalidParameterException", "task definition not found")
		return
	}
	if err := validateLoadBalancers(req.LoadBalancers, td); err != nil {
		s.mu.Unlock()
		jsonError(w, http.StatusBadRequest, "InvalidParameterException", err.Error())
		return
	}

	svc := &ecsService{
		name:               req.ServiceName,
		arn:                s.serviceARN(c.name, req.ServiceName),
		clusterArn:         c.arn,
		taskDefArn:         td.arn,
		desiredCount:       req.DesiredCount,
		runningCount:       0, // updated by reconciler (Docker) or createTaskRecord (simulation)
		pendingCount:       0,
		launchType:         req.LaunchType,
		schedulingStrategy: req.SchedulingStrategy,
		status:             "ACTIVE",
		loadBalancers:      req.LoadBalancers,
		networkConfig:      req.NetworkConfig,
		createdAt:          time.Now().UTC(),
	}
	s.services[svc.arn] = svc
	c.activeServicesCount++

	tdCopy := *td
	var svcTasks []*ecsTask
	for i := 0; i < req.DesiredCount; i++ {
		svcTasks = append(svcTasks, s.createTaskRecord(c, td, "service:"+req.ServiceName, req.LaunchType))
	}
	if !s.dockerAvail || !hasContainerImages(td) {
		svc.runningCount = req.DesiredCount // simulation: immediately running
	}
	s.mu.Unlock()

	if s.dockerAvail && hasContainerImages(&tdCopy) {
		for _, t := range svcTasks {
			arn := t.taskArn
			td := tdCopy
			go s.startTaskAsync(arn, &td)
		}
	}

	s.mu.RLock()
	meta := serviceMeta(svc)
	s.mu.RUnlock()

	jsonWrite(w, http.StatusOK, map[string]interface{}{"service": meta})
}

// validateLoadBalancers mirrors the check real ECS performs on the
// loadBalancers block of CreateService/UpdateService: every entry must name a
// container that exists in the task definition, and that container must declare
// the requested port in its portMappings. Rejecting here surfaces the mismatch
// locally instead of on the first deploy to real AWS.
func validateLoadBalancers(lbs []loadBalancer, td *taskDef) error {
	if len(lbs) == 0 {
		return nil
	}

	// container name → set of ports declared in its portMappings
	declared := make(map[string]map[int]bool, len(td.containerDefinitions))
	for _, raw := range td.containerDefinitions {
		var def struct {
			Name         string        `json:"name"`
			PortMappings []portMapping `json:"portMappings"`
		}
		if err := json.Unmarshal(raw, &def); err != nil || def.Name == "" {
			continue
		}
		ports := make(map[int]bool, len(def.PortMappings))
		for _, pm := range def.PortMappings {
			ports[pm.ContainerPort] = true
		}
		declared[def.Name] = ports
	}

	for _, lb := range lbs {
		ports, ok := declared[lb.ContainerName]
		if !ok {
			return fmt.Errorf("The container %s does not exist in the task definition.", lb.ContainerName)
		}
		if !ports[lb.ContainerPort] {
			return fmt.Errorf("The container %s did not have a container port %d defined.",
				lb.ContainerName, lb.ContainerPort)
		}
	}
	return nil
}

func (s *Service) updateService(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cluster        string          `json:"cluster"`
		Service        string          `json:"service"`
		TaskDefinition string          `json:"taskDefinition"`
		DesiredCount   *int            `json:"desiredCount"`
		LoadBalancers  *[]loadBalancer `json:"loadBalancers"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.Lock()
	svc, ok := s.resolveService(req.Cluster, req.Service)
	if !ok {
		s.mu.Unlock()
		jsonError(w, http.StatusBadRequest, "ServiceNotFoundException", "service not found")
		return
	}

	if req.TaskDefinition != "" {
		if td, ok := s.resolveTaskDef(req.TaskDefinition); ok {
			svc.taskDefArn = td.arn
		}
	}
	// Validate against the effective task definition — the one just set, if the
	// same call changed it.
	if req.LoadBalancers != nil {
		td, ok := s.resolveTaskDef(svc.taskDefArn)
		if !ok {
			s.mu.Unlock()
			jsonError(w, http.StatusBadRequest, "InvalidParameterException", "task definition not found")
			return
		}
		if err := validateLoadBalancers(*req.LoadBalancers, td); err != nil {
			s.mu.Unlock()
			jsonError(w, http.StatusBadRequest, "InvalidParameterException", err.Error())
			return
		}
		svc.loadBalancers = *req.LoadBalancers
	}
	if req.DesiredCount != nil {
		svc.desiredCount = *req.DesiredCount
		if !s.dockerAvail {
			svc.runningCount = *req.DesiredCount // simulation only
		}
	}
	meta := serviceMeta(svc)
	s.mu.Unlock()

	jsonWrite(w, http.StatusOK, map[string]interface{}{"service": meta})
}

func (s *Service) deleteService(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cluster string `json:"cluster"`
		Service string `json:"service"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.Lock()
	var meta map[string]interface{}
	svc, ok := s.resolveService(req.Cluster, req.Service)
	if ok {
		svc.status = "INACTIVE"
		svc.desiredCount = 0
		svc.runningCount = 0
		// Keep the service in the map as INACTIVE so the v6 provider's
		// delete waiter can poll DescribeServices and observe the final state.
		if c, cok := s.resolveCluster(req.Cluster); cok {
			c.activeServicesCount--
		}
		meta = serviceMeta(svc)
	}
	s.mu.Unlock()

	if !ok {
		jsonError(w, http.StatusBadRequest, "ServiceNotFoundException", "service not found")
		return
	}
	jsonWrite(w, http.StatusOK, map[string]interface{}{"service": meta})
}

func (s *Service) describeServices(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cluster  string   `json:"cluster"`
		Services []string `json:"services"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []map[string]interface{}
	for _, id := range req.Services {
		if svc, ok := s.resolveService(req.Cluster, id); ok {
			result = append(result, serviceMeta(svc))
		}
	}
	if result == nil {
		result = []map[string]interface{}{}
	}
	jsonWrite(w, http.StatusOK, map[string]interface{}{"services": result, "failures": []interface{}{}})
}

func (s *Service) listServices(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cluster string `json:"cluster"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.RLock()
	defer s.mu.RUnlock()

	var clusterArn string
	if req.Cluster != "" {
		if c, ok := s.resolveCluster(req.Cluster); ok {
			clusterArn = c.arn
		}
	}

	arns := []string{}
	for _, svc := range s.services {
		if clusterArn != "" && svc.clusterArn != clusterArn {
			continue
		}
		if svc.status == "INACTIVE" {
			continue
		}
		arns = append(arns, svc.arn)
	}
	jsonWrite(w, http.StatusOK, map[string]interface{}{"serviceArns": arns})
}

// --- Tags ---

func (s *Service) tagResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceArn string              `json:"resourceArn"`
		Tags        []map[string]string `json:"tags"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.Lock()
	for _, t := range req.Tags {
		s.setTag(req.ResourceArn, t["key"], t["value"])
	}
	s.mu.Unlock()

	jsonWrite(w, http.StatusOK, map[string]interface{}{})
}

func (s *Service) untagResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceArn string   `json:"resourceArn"`
		TagKeys     []string `json:"tagKeys"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.Lock()
	if m, ok := s.tags[req.ResourceArn]; ok {
		for _, k := range req.TagKeys {
			delete(m, k)
		}
	}
	s.mu.Unlock()

	jsonWrite(w, http.StatusOK, map[string]interface{}{})
}

func (s *Service) listTagsForResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceArn string `json:"resourceArn"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.RLock()
	m := s.tags[req.ResourceArn]
	type tag struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	tags := []tag{}
	for k, v := range m {
		tags = append(tags, tag{Key: k, Value: v})
	}
	s.mu.RUnlock()

	jsonWrite(w, http.StatusOK, map[string]interface{}{"tags": tags})
}

// --- ARN helpers ---

func (s *Service) clusterARN(name string) string {
	return fmt.Sprintf("arn:aws:ecs:%s:%s:cluster/%s", s.region, accountID, name)
}

func (s *Service) taskDefARN(family string, revision int) string {
	return fmt.Sprintf("arn:aws:ecs:%s:%s:task-definition/%s:%d", s.region, accountID, family, revision)
}

func (s *Service) taskARN(clusterName, id string) string {
	return fmt.Sprintf("arn:aws:ecs:%s:%s:task/%s/%s", s.region, accountID, clusterName, id)
}

func (s *Service) serviceARN(clusterName, serviceName string) string {
	return fmt.Sprintf("arn:aws:ecs:%s:%s:service/%s/%s", s.region, accountID, clusterName, serviceName)
}

// --- Resolve helpers (caller must hold appropriate lock) ---

// resolveCluster looks up a cluster by name or ARN.
func (s *Service) resolveCluster(id string) (*cluster, bool) {
	// Try name first
	if c, ok := s.clusters[id]; ok {
		return c, true
	}
	// Try ARN — last segment after "cluster/"
	if strings.Contains(id, ":cluster/") {
		name := id[strings.LastIndex(id, "/")+1:]
		if c, ok := s.clusters[name]; ok {
			return c, true
		}
	}
	return nil, false
}

// resolveTaskDef resolves "family", "family:rev", or full ARN.
func (s *Service) resolveTaskDef(id string) (*taskDef, bool) {
	if id == "" {
		return nil, false
	}
	// Full ARN: arn:aws:ecs:...:task-definition/family:rev
	if strings.HasPrefix(id, "arn:") {
		id = id[strings.LastIndex(id, "/")+1:]
	}
	// "family:revision"
	if strings.Contains(id, ":") {
		if td, ok := s.taskDefs[id]; ok {
			return td, true
		}
		return nil, false
	}
	// Bare family name — use latest revision
	if rev, ok := s.taskFams[id]; ok {
		return s.taskDefs[tdKey(id, rev)], true
	}
	return nil, false
}

// resolveTask looks up a task by ARN or short ID within an optional cluster.
func (s *Service) resolveTask(clusterHint, id string) (*ecsTask, bool) {
	if t, ok := s.tasks[id]; ok {
		return t, true
	}
	// id might be a UUID (short ID) — scan
	for arn, t := range s.tasks {
		if strings.HasSuffix(arn, "/"+id) {
			if clusterHint == "" {
				return t, true
			}
			if c, ok := s.resolveCluster(clusterHint); ok && t.clusterArn == c.arn {
				return t, true
			}
		}
	}
	return nil, false
}

// resolveService looks up a service by ARN or name within an optional cluster.
func (s *Service) resolveService(clusterHint, id string) (*ecsService, bool) {
	if svc, ok := s.services[id]; ok {
		return svc, true
	}
	for _, svc := range s.services {
		if svc.name == id || svc.arn == id {
			if clusterHint == "" {
				return svc, true
			}
			if c, ok := s.resolveCluster(clusterHint); ok && svc.clusterArn == c.arn {
				return svc, true
			}
		}
	}
	return nil, false
}

func (s *Service) setTag(arn, key, value string) {
	if s.tags[arn] == nil {
		s.tags[arn] = map[string]string{}
	}
	s.tags[arn][key] = value
}

// --- Metadata serialisers ---

// clusterMeta serialises a cluster. Caller must hold s.mu — the settings map is
// shared with the cluster record.
func clusterMeta(c *cluster) map[string]interface{} {
	settings := make([]clusterSetting, 0, len(c.settings))
	for name, value := range c.settings {
		settings = append(settings, clusterSetting{Name: name, Value: value})
	}
	sort.Slice(settings, func(i, j int) bool { return settings[i].Name < settings[j].Name })

	return map[string]interface{}{
		"clusterArn":                     c.arn,
		"clusterName":                    c.name,
		"status":                         c.status,
		"runningTasksCount":              c.runningTasksCount,
		"pendingTasksCount":              c.pendingTasksCount,
		"activeServicesCount":            c.activeServicesCount,
		"registeredTaskDefinitionsCount": 0,
		"settings":                       settings,
	}
}

func taskDefMeta(td *taskDef) map[string]interface{} {
	containerDefs := td.containerDefinitions
	if containerDefs == nil {
		containerDefs = []json.RawMessage{}
	}
	compat := td.requiresCompatibilities
	if compat == nil {
		compat = []string{"EC2"}
	}
	return map[string]interface{}{
		"taskDefinitionArn":       td.arn,
		"family":                  td.family,
		"revision":                td.revision,
		"status":                  td.status,
		"containerDefinitions":    containerDefs,
		"networkMode":             td.networkMode,
		"cpu":                     td.cpu,
		"memory":                  td.memory,
		"requiresCompatibilities": compat,
		"executionRoleArn":        td.executionRoleArn,
		"taskRoleArn":             td.taskRoleArn,
		"registeredAt":            float64(td.registeredAt.Unix()),
	}
}

func taskMeta(t *ecsTask) map[string]interface{} {
	return map[string]interface{}{
		"taskArn":           t.taskArn,
		"taskDefinitionArn": t.taskDefArn,
		"clusterArn":        t.clusterArn,
		"group":             t.group,
		"launchType":        t.launchType,
		"cpu":               t.cpu,
		"memory":            t.memory,
		"lastStatus":        t.lastStatus,
		"desiredStatus":     t.desiredStatus,
		"stoppedReason":     t.stoppedReason,
		"startedAt":         float64(t.startedAt.Unix()),
		"createdAt":         float64(t.createdAt.Unix()),
		"connectivity":      "CONNECTED",
		"containers":        []interface{}{},
	}
}

func serviceMeta(svc *ecsService) map[string]interface{} {
	lbs := svc.loadBalancers
	if lbs == nil {
		lbs = []loadBalancer{}
	}
	// schedulingStrategy must be reported: it is ForceNew for the Terraform
	// provider, so omitting it makes every re-apply plan a service replacement.
	// Services created before it was modelled read back as REPLICA, the default.
	strategy := svc.schedulingStrategy
	if strategy == "" {
		strategy = "REPLICA"
	}
	meta := map[string]interface{}{
		"serviceArn":         svc.arn,
		"serviceName":        svc.name,
		"clusterArn":         svc.clusterArn,
		"taskDefinition":     svc.taskDefArn,
		"desiredCount":       svc.desiredCount,
		"runningCount":       svc.runningCount,
		"pendingCount":       svc.pendingCount,
		"launchType":         svc.launchType,
		"schedulingStrategy": strategy,
		"status":             svc.status,
		"loadBalancers":      lbs,
		"createdAt":          float64(svc.createdAt.Unix()),
		"deployments":        []interface{}{},
		"events":             []interface{}{},
	}
	if svc.networkConfig != nil {
		meta["networkConfiguration"] = svc.networkConfig
	}
	return meta
}

// --- Key helpers ---

func tdKey(family string, revision int) string {
	return fmt.Sprintf("%s:%d", family, revision)
}

// --- HTTP helpers ---

func jsonWrite(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{
		"__type":  code,
		"message": message,
	})
}
