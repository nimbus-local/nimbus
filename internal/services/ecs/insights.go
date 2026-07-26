package ecs

// Container Insights performance-event synthesis.
//
// A cluster whose containerInsights setting is enabled makes real ECS create the
// log group /aws/ecs/containerinsights/<cluster>/performance and publish one
// event per entity per minute: Cluster, Service, Task, and Container. Readers
// treat that group as the freshest view of task CPU and memory — it lands about
// 80 s after the minute it describes, where the AWS/ECS metric namespace takes
// two to three minutes.
//
// Nimbus does no cgroup accounting, so utilisation figures here are synthetic: a
// bounded random walk per container. What a reader actually depends on is
// reproduced faithfully — the log group name, the stream names, the event
// shapes, the per-minute cadence, and the lag between an event's timestamp and
// its appearance. Field sets and semantics were taken from 157 events captured
// on a live cluster (2026-07-09): *Packets fields are cumulative counters, while
// the *Bytes fields are per-second rates that rise and fall.

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log/slog"
	"math/rand/v2"
	"os"
	"strings"
	"time"
)

const (
	// insightsGroupFormat is the log group real ECS creates per enabled cluster.
	insightsGroupFormat = "/aws/ecs/containerinsights/%s/performance"

	// defaultInsightsInterval is the publishing cadence — real ECS emits one
	// event per entity per minute, stamped on the minute.
	defaultInsightsInterval = time.Minute

	// defaultInsightsDelay is how far behind wall clock the published interval
	// sits. Measured emission→ingestion lag on a live cluster: median 80 s,
	// max 140 s.
	defaultInsightsDelay = 80 * time.Second

	// fargateEphemeralStorageGB is the ephemeral volume every Fargate task gets
	// unless the task definition asks for more: 20 GiB, reported in GB.
	fargateEphemeralStorageGB = 21.47
)

// InsightsSink receives synthesized performance events. The CloudWatch Logs
// service implements it; with no sink wired ECS records the containerInsights
// setting but publishes nothing.
type InsightsSink interface {
	IngestAt(group, stream string, timestampMS int64, message string)
}

// EnableContainerInsights wires the log sink performance events are written to
// and starts the emitter goroutine.
func (s *Service) EnableContainerInsights(sink InsightsSink) {
	if sink == nil {
		return
	}
	s.mu.Lock()
	s.insights = sink
	s.mu.Unlock()

	slog.Info("ECS: Container Insights emitter started",
		"interval", s.insightsInterval, "delay", s.insightsDelay)
	go s.emitInsightsLoop()
}

// emitInsightsLoop publishes one round of events per interval. It wakes more
// often than it emits so a round lands promptly once its interval comes due.
func (s *Service) emitInsightsLoop() {
	tick := s.insightsInterval / 4
	if tick < time.Second {
		tick = time.Second
	}
	if tick > 15*time.Second {
		tick = 15 * time.Second
	}
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	for range ticker.C {
		s.emitInsights(time.Now().UTC())
	}
}

// emitInsights publishes the newest interval that has come due — the one ending
// at or before now minus the ingestion delay. Any interval missed while the
// process was blocked is skipped rather than backfilled, so a stalled emitter
// resumes with one current round instead of a burst of stale ones.
func (s *Service) emitInsights(now time.Time) {
	slot := now.Add(-s.insightsDelay).Truncate(s.insightsInterval)

	s.mu.Lock()
	sink := s.insights
	if sink == nil || !slot.After(s.lastInsightsSlot) {
		s.mu.Unlock()
		return
	}
	s.lastInsightsSlot = slot
	events := s.buildInsightsEvents(slot)
	s.mu.Unlock()

	// Emitted outside the lock: the sink takes its own.
	for _, ev := range events {
		sink.IngestAt(ev.group, ev.stream, ev.timestampMS, ev.message)
	}
}

// insightsEvent is one rendered event addressed to a log stream.
type insightsEvent struct {
	group       string
	stream      string
	timestampMS int64
	message     string
}

// buildInsightsEvents renders every event due for slot. Caller must hold s.mu
// for writing — advancing the synthetic counters mutates s.insightsCounters.
func (s *Service) buildInsightsEvents(slot time.Time) []insightsEvent {
	ts := slot.UnixMilli()
	var out []insightsEvent

	for _, c := range s.clusters {
		if !containerInsightsEnabled(c) {
			continue
		}
		tasks := s.liveClusterTasks(c)
		if len(tasks) == 0 {
			// Real ECS creates the log group with the first task of an enabled
			// cluster; an idle cluster produces no group at all.
			continue
		}
		group := fmt.Sprintf(insightsGroupFormat, c.name)

		out = append(out, insightsEvent{
			group:       group,
			stream:      "ClusterTelemetry-" + c.name,
			timestampMS: ts,
			message:     mustJSON(s.clusterEvent(c, tasks, ts)),
		})

		for _, svc := range s.services {
			if svc.status != "ACTIVE" || svc.clusterArn != c.arn {
				continue
			}
			out = append(out, insightsEvent{
				group:       group,
				stream:      "ServiceTelemetry-" + svc.name,
				timestampMS: ts,
				message:     mustJSON(serviceEvent(svc, c.name, ts)),
			})
		}

		for _, t := range tasks {
			td, ok := s.resolveTaskDef(t.taskDefArn)
			if !ok {
				continue
			}
			// Task and Container events for one task share a stream.
			stream := taskTelemetryStream(t)
			taskEv, containerEvs := s.taskEvents(c, t, td, ts)
			out = append(out, insightsEvent{
				group: group, stream: stream, timestampMS: ts, message: mustJSON(taskEv),
			})
			for _, ce := range containerEvs {
				out = append(out, insightsEvent{
					group: group, stream: stream, timestampMS: ts, message: mustJSON(ce),
				})
			}
		}
	}
	return out
}

// containerInsightsEnabled reports whether a cluster publishes performance
// events. "enhanced" is Container Insights with enhanced observability: it adds
// events rather than replacing the performance ones.
func containerInsightsEnabled(c *cluster) bool {
	switch strings.ToLower(c.settings["containerInsights"]) {
	case "enabled", "enhanced":
		return true
	}
	return false
}

// liveClusterTasks returns the cluster's tasks that are not STOPPED. Caller must
// hold s.mu.
func (s *Service) liveClusterTasks(c *cluster) []*ecsTask {
	var out []*ecsTask
	for _, t := range s.tasks {
		if t.clusterArn == c.arn && t.lastStatus != "STOPPED" {
			out = append(out, t)
		}
	}
	return out
}

// taskTelemetryStream names the stream a task's Task and Container events go to.
// Real Fargate streams are FargateTelemetry-<n> for a small n that is stable for
// the life of the task; deriving n from the task ARN keeps it stable here too.
func taskTelemetryStream(t *ecsTask) string {
	h := fnv.New32a()
	h.Write([]byte(t.taskArn)) //nolint:errcheck — hash writes never fail
	return fmt.Sprintf("FargateTelemetry-%d", h.Sum32()%10000)
}

// --- Event shapes ---

// cwMetric and cwMetricsBlock form the embedded-metric-format header real ECS
// attaches so CloudWatch extracts metrics from the event. Readers that only
// parse fields ignore it, but it is part of the shape they receive.
type cwMetric struct {
	Name string `json:"Name"`
	Unit string `json:"Unit"`
}

type cwMetricsBlock struct {
	Namespace  string     `json:"Namespace"`
	Metrics    []cwMetric `json:"Metrics"`
	Dimensions [][]string `json:"Dimensions"`
}

// resourceUsage is the utilisation block Task and Container events share.
// Field order matches the real events.
type resourceUsage struct {
	CpuUtilized       float64 `json:"CpuUtilized"`
	CpuReserved       float64 `json:"CpuReserved"`
	MemoryUtilized    int64   `json:"MemoryUtilized"`
	MemoryReserved    int64   `json:"MemoryReserved"`
	StorageReadBytes  int64   `json:"StorageReadBytes"`
	StorageWriteBytes int64   `json:"StorageWriteBytes"`
	NetworkRxBytes    int64   `json:"NetworkRxBytes"`
	NetworkRxDropped  int64   `json:"NetworkRxDropped"`
	NetworkRxErrors   int64   `json:"NetworkRxErrors"`
	NetworkRxPackets  int64   `json:"NetworkRxPackets"`
	NetworkTxBytes    int64   `json:"NetworkTxBytes"`
	NetworkTxDropped  int64   `json:"NetworkTxDropped"`
	NetworkTxErrors   int64   `json:"NetworkTxErrors"`
	NetworkTxPackets  int64   `json:"NetworkTxPackets"`
}

type clusterInsightsEvent struct {
	Version                string           `json:"Version"`
	Type                   string           `json:"Type"`
	ClusterName            string           `json:"ClusterName"`
	Timestamp              int64            `json:"Timestamp"`
	TaskCount              int              `json:"TaskCount"`
	ContainerInstanceCount int              `json:"ContainerInstanceCount"`
	ServiceCount           int              `json:"ServiceCount"`
	CloudWatchMetrics      []cwMetricsBlock `json:"CloudWatchMetrics"`
}

type serviceInsightsEvent struct {
	Version           string           `json:"Version"`
	Type              string           `json:"Type"`
	ServiceName       string           `json:"ServiceName"`
	ClusterName       string           `json:"ClusterName"`
	Timestamp         int64            `json:"Timestamp"`
	DesiredTaskCount  int              `json:"DesiredTaskCount"`
	RunningTaskCount  int              `json:"RunningTaskCount"`
	PendingTaskCount  int              `json:"PendingTaskCount"`
	DeploymentCount   int              `json:"DeploymentCount"`
	TaskSetCount      int              `json:"TaskSetCount"`
	CloudWatchMetrics []cwMetricsBlock `json:"CloudWatchMetrics"`
}

// taskInsightsEvent carries the fields a reader keys on: TaskId, the cluster and
// service names, KnownStatus, and utilisation against what the task reserved.
// ServiceName is absent on a standalone RunTask task, as in the real events.
type taskInsightsEvent struct {
	Version                string `json:"Version"`
	Type                   string `json:"Type"`
	TaskId                 string `json:"TaskId"`
	TaskDefinitionFamily   string `json:"TaskDefinitionFamily"`
	TaskDefinitionRevision string `json:"TaskDefinitionRevision"`
	ServiceName            string `json:"ServiceName,omitempty"`
	ClusterName            string `json:"ClusterName"`
	AccountID              string `json:"AccountID"`
	Region                 string `json:"Region"`
	AvailabilityZone       string `json:"AvailabilityZone"`
	KnownStatus            string `json:"KnownStatus"`
	LaunchType             string `json:"LaunchType"`
	CreatedAt              int64  `json:"CreatedAt"`
	StartedAt              int64  `json:"StartedAt,omitempty"`
	Timestamp              int64  `json:"Timestamp"`
	resourceUsage
	EphemeralStorageUtilized float64          `json:"EphemeralStorageUtilized"`
	EphemeralStorageReserved float64          `json:"EphemeralStorageReserved"`
	CloudWatchMetrics        []cwMetricsBlock `json:"CloudWatchMetrics"`
}

// containerInsightsEvent is the per-container sample. Real Container events
// carry no CloudWatchMetrics block — CloudWatch extracts container metrics from
// the Task event instead.
type containerInsightsEvent struct {
	Version                string `json:"Version"`
	Type                   string `json:"Type"`
	ContainerName          string `json:"ContainerName"`
	TaskId                 string `json:"TaskId"`
	TaskDefinitionFamily   string `json:"TaskDefinitionFamily"`
	TaskDefinitionRevision string `json:"TaskDefinitionRevision"`
	ServiceName            string `json:"ServiceName,omitempty"`
	ClusterName            string `json:"ClusterName"`
	Image                  string `json:"Image"`
	ContainerKnownStatus   string `json:"ContainerKnownStatus"`
	Timestamp              int64  `json:"Timestamp"`
	resourceUsage
}

func (s *Service) clusterEvent(c *cluster, tasks []*ecsTask, ts int64) clusterInsightsEvent {
	services := 0
	for _, svc := range s.services {
		if svc.status == "ACTIVE" && svc.clusterArn == c.arn {
			services++
		}
	}
	return clusterInsightsEvent{
		Version:     "0",
		Type:        "Cluster",
		ClusterName: c.name,
		Timestamp:   ts,
		TaskCount:   len(tasks),
		// Fargate tasks run on no container instance the account can see.
		ContainerInstanceCount: 0,
		ServiceCount:           services,
		CloudWatchMetrics: []cwMetricsBlock{{
			Namespace: "ECS/ContainerInsights",
			Metrics: []cwMetric{
				{Name: "TaskCount", Unit: "Count"},
				{Name: "ContainerInstanceCount", Unit: "Count"},
				{Name: "ServiceCount", Unit: "Count"},
			},
			Dimensions: [][]string{{"ClusterName"}},
		}},
	}
}

func serviceEvent(svc *ecsService, clusterName string, ts int64) serviceInsightsEvent {
	return serviceInsightsEvent{
		Version:          "0",
		Type:             "Service",
		ServiceName:      svc.name,
		ClusterName:      clusterName,
		Timestamp:        ts,
		DesiredTaskCount: svc.desiredCount,
		RunningTaskCount: svc.runningCount,
		PendingTaskCount: svc.pendingCount,
		// An ACTIVE service always has its one current deployment; Nimbus models
		// no blue/green task sets.
		DeploymentCount: 1,
		TaskSetCount:    0,
		CloudWatchMetrics: []cwMetricsBlock{{
			Namespace: "ECS/ContainerInsights",
			Metrics: []cwMetric{
				{Name: "DesiredTaskCount", Unit: "Count"},
				{Name: "RunningTaskCount", Unit: "Count"},
				{Name: "PendingTaskCount", Unit: "Count"},
				{Name: "DeploymentCount", Unit: "Count"},
				{Name: "TaskSetCount", Unit: "Count"},
			},
			Dimensions: [][]string{{"ServiceName", "ClusterName"}},
		}},
	}
}

// taskEvents samples every container of a task and returns the Task event plus
// one Container event each. The Task event's utilisation is the sum over its
// containers, while its reservations come from the task definition — the same
// relationship the real events have. Caller must hold s.mu for writing.
func (s *Service) taskEvents(c *cluster, t *ecsTask, td *taskDef, ts int64) (taskInsightsEvent, []containerInsightsEvent) {
	taskCPU := parseUnits(t.cpu)
	taskMem := parseUnits(t.memory)
	serviceName := strings.TrimPrefix(t.group, "service:")
	if serviceName == t.group {
		serviceName = "" // not a service task — real events omit the field
	}
	taskID := strings.ReplaceAll(lastSegment(t.taskArn), "-", "")
	revision := fmt.Sprintf("%d", td.revision)

	var (
		total      resourceUsage
		containers []containerInsightsEvent
	)
	for _, def := range containerDefs(td) {
		cpuReserved := float64(def.CPU)
		if cpuReserved == 0 {
			cpuReserved = float64(taskCPU) // Fargate reserves at the task level
		}
		memReserved := int64(def.Memory)
		if memReserved == 0 {
			memReserved = int64(def.MemoryReservation)
		}
		if memReserved == 0 {
			memReserved = taskMem
		}

		usage := s.sampleContainer(t.taskArn+"/"+def.Name, cpuReserved, memReserved)
		total = addUsage(total, usage)

		containers = append(containers, containerInsightsEvent{
			Version:                "0",
			Type:                   "Container",
			ContainerName:          def.Name,
			TaskId:                 taskID,
			TaskDefinitionFamily:   td.family,
			TaskDefinitionRevision: revision,
			ServiceName:            serviceName,
			ClusterName:            c.name,
			Image:                  def.Image,
			// Nimbus tracks status per task, not per container.
			ContainerKnownStatus: t.lastStatus,
			Timestamp:            ts,
			resourceUsage:        usage,
		})
	}

	// Reservations on the Task event are what the task definition asked for,
	// not the sum of the containers' shares of it.
	total.CpuReserved = float64(taskCPU)
	total.MemoryReserved = taskMem

	task := taskInsightsEvent{
		Version:                  "0",
		Type:                     "Task",
		TaskId:                   taskID,
		TaskDefinitionFamily:     td.family,
		TaskDefinitionRevision:   revision,
		ServiceName:              serviceName,
		ClusterName:              c.name,
		AccountID:                accountID,
		Region:                   s.region,
		AvailabilityZone:         s.region + "a",
		KnownStatus:              t.lastStatus,
		LaunchType:               t.launchType,
		CreatedAt:                t.createdAt.UnixMilli(),
		Timestamp:                ts,
		resourceUsage:            total,
		EphemeralStorageUtilized: round2(float64(len(containers)) * 0.03),
		EphemeralStorageReserved: fargateEphemeralStorageGB,
		CloudWatchMetrics: []cwMetricsBlock{{
			Namespace: "ECS/ContainerInsights",
			Metrics: []cwMetric{
				{Name: "CpuUtilized", Unit: "None"},
				{Name: "CpuReserved", Unit: "None"},
				{Name: "MemoryUtilized", Unit: "Megabytes"},
				{Name: "MemoryReserved", Unit: "Megabytes"},
				{Name: "StorageReadBytes", Unit: "Bytes/Second"},
				{Name: "StorageWriteBytes", Unit: "Bytes/Second"},
				{Name: "NetworkRxBytes", Unit: "Bytes/Second"},
				{Name: "NetworkTxBytes", Unit: "Bytes/Second"},
				{Name: "EphemeralStorageUtilized", Unit: "Gigabytes"},
				{Name: "EphemeralStorageReserved", Unit: "Gigabytes"},
			},
			Dimensions: [][]string{
				{"ClusterName"},
				{"ServiceName", "ClusterName"},
				{"ClusterName", "TaskDefinitionFamily"},
			},
		}},
	}
	if t.lastStatus == "RUNNING" && !t.startedAt.IsZero() {
		task.StartedAt = t.startedAt.UnixMilli()
	}
	return task, containers
}

// --- Synthetic sampling ---

// containerCounters carries one container's synthetic state between intervals so
// its numbers move continuously instead of jumping each round.
type containerCounters struct {
	cpu       float64
	memory    int64
	rxPackets int64
	txPackets int64
}

// sampleContainer advances one container's synthetic usage by an interval.
// Caller must hold s.mu for writing.
func (s *Service) sampleContainer(key string, cpuReserved float64, memReserved int64) resourceUsage {
	cc, ok := s.insightsCounters[key]
	if !ok {
		// Start somewhere plausible rather than at zero, so the very first
		// sample a reader sees is already a believable steady state.
		cc = &containerCounters{
			cpu:       cpuReserved * (0.05 + rand.Float64()*0.15),
			memory:    int64(float64(memReserved) * (0.1 + rand.Float64()*0.2)),
			rxPackets: rand.Int64N(50_000),
			txPackets: rand.Int64N(20_000),
		}
		s.insightsCounters[key] = cc
	}

	// Utilisation drifts within its reservation; packet counts only accumulate.
	cc.cpu = walk(cc.cpu, cpuReserved*0.05, 0, cpuReserved)
	cc.memory = walkInt(cc.memory, max(memReserved/20, 1), 0, memReserved)
	cc.rxPackets += rand.Int64N(4000)
	cc.txPackets += rand.Int64N(1500)

	return resourceUsage{
		CpuUtilized:      round2(cc.cpu),
		CpuReserved:      cpuReserved,
		MemoryUtilized:   cc.memory,
		MemoryReserved:   memReserved,
		StorageReadBytes: rand.Int64N(64 * 1024),
		// Bytes fields are per-second rates in the real events, so they rise and
		// fall independently of the cumulative packet counters above.
		StorageWriteBytes: rand.Int64N(16 * 1024),
		NetworkRxBytes:    rand.Int64N(32 * 1024),
		NetworkRxDropped:  0,
		NetworkRxErrors:   0,
		NetworkRxPackets:  cc.rxPackets,
		NetworkTxBytes:    rand.Int64N(16 * 1024),
		NetworkTxDropped:  0,
		NetworkTxErrors:   0,
		NetworkTxPackets:  cc.txPackets,
	}
}

// addUsage sums the per-container figures that roll up to a task. Reservations
// are set by the caller from the task definition, and the drop/error counters
// stay at zero.
func addUsage(a, b resourceUsage) resourceUsage {
	a.CpuUtilized = round2(a.CpuUtilized + b.CpuUtilized)
	a.MemoryUtilized += b.MemoryUtilized
	a.StorageReadBytes += b.StorageReadBytes
	a.StorageWriteBytes += b.StorageWriteBytes
	a.NetworkRxBytes += b.NetworkRxBytes
	a.NetworkRxPackets += b.NetworkRxPackets
	a.NetworkTxBytes += b.NetworkTxBytes
	a.NetworkTxPackets += b.NetworkTxPackets
	return a
}

// walk nudges v by up to ±step and clamps the result to [lo, hi].
func walk(v, step, lo, hi float64) float64 {
	v += (rand.Float64()*2 - 1) * step
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func walkInt(v, step, lo, hi int64) int64 {
	if step > 0 {
		v += rand.Int64N(2*step+1) - step
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}

// containerDefs parses a task definition's container definitions, skipping any
// that fail to parse or carry no name.
func containerDefs(td *taskDef) []containerDef {
	var out []containerDef
	for _, raw := range td.containerDefinitions {
		var d containerDef
		if err := json.Unmarshal(raw, &d); err != nil || d.Name == "" {
			continue
		}
		out = append(out, d)
	}
	return out
}

// parseUnits reads a task definition's cpu/memory string ("256", "512").
func parseUnits(s string) int64 {
	var n int64
	fmt.Sscanf(s, "%d", &n) //nolint:errcheck — an unparsable value means 0
	return n
}

func mustJSON(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		// Every event here is a struct of scalars, slices, and strings.
		slog.Error("ECS: failed to encode Container Insights event", "err", err)
		return "{}"
	}
	return string(b)
}

// durationFromEnv reads a Go duration ("30s", "2m") from the environment.
func durationFromEnv(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		slog.Warn("ECS: ignoring invalid duration", "var", key, "value", v)
		return fallback
	}
	return d
}
