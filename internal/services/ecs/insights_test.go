package ecs

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// --- Test helpers ---

type sunkEvent struct {
	group     string
	stream    string
	timestamp int64
	message   string
}

type fakeSink struct {
	mu     sync.Mutex
	events []sunkEvent
}

func (f *fakeSink) IngestAt(group, stream string, timestampMS int64, message string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, sunkEvent{group, stream, timestampMS, message})
}

func (f *fakeSink) all() []sunkEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sunkEvent(nil), f.events...)
}

// decoded returns every event of the given Type, paired with its stream.
func (f *fakeSink) decoded(t *testing.T, typ string) []map[string]interface{} {
	t.Helper()
	var out []map[string]interface{}
	for _, e := range f.all() {
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(e.message), &m); err != nil {
			t.Fatalf("event is not JSON: %v (%s)", err, e.message)
		}
		if m["Type"] == typ {
			m["__stream"] = e.stream
			out = append(out, m)
		}
	}
	return out
}

// insightsSvc builds a service with the sink attached but the emitter loop left
// unstarted, so tests drive emitInsights with an explicit clock. dockerAvail is
// forced off: task definitions here carry images, and the real Docker path would
// try to start containers.
func insightsSvc(t *testing.T) (*Service, *fakeSink) {
	t.Helper()
	svc := newSvc()
	svc.dockerAvail = false
	sink := &fakeSink{}
	svc.insights = sink
	return svc, sink
}

const insightsContainers = `[
  {"name": "app", "image": "nginx:latest", "portMappings": [{"containerPort": 80}]}
]`

// enabledCluster sets up a containerInsights-enabled cluster running one service
// of one task, the shape the emitter is expected to report on.
func enabledCluster(t *testing.T, svc *Service) {
	t.Helper()
	do(t, svc, "CreateCluster", map[string]interface{}{
		"clusterName": "prod",
		"settings":    []clusterSetting{{Name: "containerInsights", Value: "enabled"}},
	})
	var defs []json.RawMessage
	if err := json.Unmarshal([]byte(insightsContainers), &defs); err != nil {
		t.Fatalf("bad fixture: %v", err)
	}
	do(t, svc, "RegisterTaskDefinition", map[string]interface{}{
		"family":               "web",
		"cpu":                  "256",
		"memory":               "512",
		"containerDefinitions": defs,
	})
	do(t, svc, "CreateService", map[string]interface{}{
		"cluster":        "prod",
		"serviceName":    "api-svc",
		"taskDefinition": "web",
		"desiredCount":   1,
	})
}

// emitAt runs one emission round for a wall clock that puts slot fully in the
// past, so the round is due.
func emitAt(svc *Service, slot time.Time) {
	svc.emitInsights(slot.Add(svc.insightsDelay))
}

// --- Cluster setting ---

func TestCreateClusterRecordsContainerInsights(t *testing.T) {
	svc := newSvc()
	res := do(t, svc, "CreateCluster", map[string]interface{}{
		"clusterName": "prod",
		"settings":    []clusterSetting{{Name: "containerInsights", Value: "enabled"}},
	})
	settings := res["cluster"].(map[string]interface{})["settings"].([]interface{})
	if len(settings) != 1 {
		t.Fatalf("expected 1 setting, got %d", len(settings))
	}
	s := settings[0].(map[string]interface{})
	if s["name"] != "containerInsights" || s["value"] != "enabled" {
		t.Errorf("unexpected setting %v", s)
	}
}

func TestUpdateClusterSettingsEnablesInsights(t *testing.T) {
	svc := newSvc()
	do(t, svc, "CreateCluster", map[string]interface{}{"clusterName": "prod"})

	svc.mu.RLock()
	enabled := containerInsightsEnabled(svc.clusters["prod"])
	svc.mu.RUnlock()
	if enabled {
		t.Fatal("a cluster with no settings must not have Container Insights enabled")
	}

	do(t, svc, "UpdateClusterSettings", map[string]interface{}{
		"cluster":  "prod",
		"settings": []clusterSetting{{Name: "containerInsights", Value: "enabled"}},
	})

	svc.mu.RLock()
	enabled = containerInsightsEnabled(svc.clusters["prod"])
	svc.mu.RUnlock()
	if !enabled {
		t.Error("UpdateClusterSettings did not enable Container Insights")
	}
}

func TestUpdateClusterSettingsUnknownCluster(t *testing.T) {
	svc := newSvc()
	code := status(t, svc, "UpdateClusterSettings", map[string]interface{}{"cluster": "nope"})
	if code != 400 {
		t.Errorf("expected 400 for unknown cluster, got %d", code)
	}
}

func TestContainerInsightsEnabledValues(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"enabled", true},
		{"enhanced", true}, // enhanced observability adds events, it does not replace them
		{"ENABLED", true},
		{"disabled", false},
		{"", false},
	} {
		c := &cluster{settings: map[string]string{"containerInsights": tc.value}}
		if got := containerInsightsEnabled(c); got != tc.want {
			t.Errorf("containerInsights=%q: got %v, want %v", tc.value, got, tc.want)
		}
	}
}

// --- Emission gating ---

func TestInsightsSilentWhenDisabled(t *testing.T) {
	svc, sink := insightsSvc(t)
	do(t, svc, "CreateCluster", map[string]interface{}{"clusterName": "prod"})
	do(t, svc, "RegisterTaskDefinition", map[string]interface{}{"family": "web"})
	do(t, svc, "RunTask", map[string]interface{}{"cluster": "prod", "taskDefinition": "web"})

	emitAt(svc, time.Now().UTC().Truncate(time.Minute))

	if got := len(sink.all()); got != 0 {
		t.Errorf("a cluster without containerInsights must publish nothing, got %d events", got)
	}
}

func TestInsightsSilentWithoutTasks(t *testing.T) {
	svc, sink := insightsSvc(t)
	do(t, svc, "CreateCluster", map[string]interface{}{
		"clusterName": "prod",
		"settings":    []clusterSetting{{Name: "containerInsights", Value: "enabled"}},
	})

	emitAt(svc, time.Now().UTC().Truncate(time.Minute))

	// Real ECS creates the log group with the first task of an enabled cluster.
	if got := len(sink.all()); got != 0 {
		t.Errorf("an idle cluster must publish nothing, got %d events", got)
	}
}

func TestInsightsSilentForStoppedTasks(t *testing.T) {
	svc, sink := insightsSvc(t)
	enabledCluster(t, svc)

	res := do(t, svc, "ListTasks", map[string]interface{}{"cluster": "prod"})
	arn := res["taskArns"].([]interface{})[0].(string)
	do(t, svc, "StopTask", map[string]interface{}{"cluster": "prod", "task": arn})

	emitAt(svc, time.Now().UTC().Truncate(time.Minute))

	if got := len(sink.all()); got != 0 {
		t.Errorf("a cluster whose tasks all stopped must publish nothing, got %d events", got)
	}
}

func TestInsightsEmitsOnePerEntityPerInterval(t *testing.T) {
	svc, sink := insightsSvc(t)
	enabledCluster(t, svc)

	emitAt(svc, time.Now().UTC().Truncate(time.Minute))

	// One Cluster, one Service, one Task, one Container — the entity set of a
	// single-service, single-task, single-container cluster.
	for _, typ := range []string{"Cluster", "Service", "Task", "Container"} {
		if got := len(sink.decoded(t, typ)); got != 1 {
			t.Errorf("expected 1 %s event, got %d", typ, got)
		}
	}
}

func TestInsightsLogGroupAndStreamNames(t *testing.T) {
	svc, sink := insightsSvc(t)
	enabledCluster(t, svc)

	emitAt(svc, time.Now().UTC().Truncate(time.Minute))

	events := sink.all()
	if len(events) == 0 {
		t.Fatal("no events published")
	}
	for _, e := range events {
		if e.group != "/aws/ecs/containerinsights/prod/performance" {
			t.Errorf("unexpected log group %q", e.group)
		}
	}

	streams := map[string]bool{}
	for _, e := range events {
		streams[e.stream] = true
	}
	if !streams["ClusterTelemetry-prod"] {
		t.Errorf("missing ClusterTelemetry-prod stream, got %v", streams)
	}
	if !streams["ServiceTelemetry-api-svc"] {
		t.Errorf("missing ServiceTelemetry-api-svc stream, got %v", streams)
	}

	// Task and Container events for one task share a FargateTelemetry stream.
	task := sink.decoded(t, "Task")[0]
	container := sink.decoded(t, "Container")[0]
	if task["__stream"] != container["__stream"] {
		t.Errorf("Task stream %v and Container stream %v should match",
			task["__stream"], container["__stream"])
	}
	if s, _ := task["__stream"].(string); len(s) < len("FargateTelemetry-") ||
		s[:len("FargateTelemetry-")] != "FargateTelemetry-" {
		t.Errorf("expected a FargateTelemetry-<n> stream, got %v", task["__stream"])
	}
}

// A task's telemetry stream must not change between intervals — a reader that
// tracks streams would otherwise see each round as a new one.
func TestInsightsTaskStreamIsStable(t *testing.T) {
	svc, sink := insightsSvc(t)
	enabledCluster(t, svc)

	slot := time.Now().UTC().Truncate(time.Minute)
	emitAt(svc, slot.Add(-time.Minute))
	emitAt(svc, slot)

	tasks := sink.decoded(t, "Task")
	if len(tasks) != 2 {
		t.Fatalf("expected 2 Task events across 2 intervals, got %d", len(tasks))
	}
	if tasks[0]["__stream"] != tasks[1]["__stream"] {
		t.Errorf("task stream changed between intervals: %v then %v",
			tasks[0]["__stream"], tasks[1]["__stream"])
	}
	if tasks[0]["TaskId"] != tasks[1]["TaskId"] {
		t.Errorf("TaskId changed between intervals")
	}
}

// --- Cadence and delay ---

func TestInsightsTimestampIsIntervalAlignedAndDelayed(t *testing.T) {
	svc, sink := insightsSvc(t)
	enabledCluster(t, svc)

	now := time.Date(2026, 7, 26, 12, 3, 37, 500_000_000, time.UTC)
	svc.emitInsights(now)

	events := sink.all()
	if len(events) == 0 {
		t.Fatal("no events published")
	}
	// now - 80 s = 12:02:17, truncated to the minute = 12:02:00.
	want := time.Date(2026, 7, 26, 12, 2, 0, 0, time.UTC).UnixMilli()
	for _, e := range events {
		if e.timestamp != want {
			t.Errorf("stream %s: timestamp %d, want %d (minute-aligned, %s behind now)",
				e.stream, e.timestamp, want, svc.insightsDelay)
		}
	}

	// The in-message Timestamp must agree with the event timestamp: readers use
	// one or the other interchangeably.
	for _, m := range sink.decoded(t, "Task") {
		if int64(m["Timestamp"].(float64)) != want {
			t.Errorf("in-message Timestamp %v, want %d", m["Timestamp"], want)
		}
	}
}

func TestInsightsSkipsSlotAlreadyEmitted(t *testing.T) {
	svc, sink := insightsSvc(t)
	enabledCluster(t, svc)

	// Three wake-ups that all resolve to the same due interval must publish
	// exactly one round: the emitter loop ticks more often than it emits.
	// now - 80 s = 12:02:10, so offsets up to 49 s stay inside the 12:02 slot.
	now := time.Date(2026, 7, 26, 12, 3, 30, 0, time.UTC)
	svc.emitInsights(now)
	first := len(sink.all())
	svc.emitInsights(now.Add(15 * time.Second))
	svc.emitInsights(now.Add(30 * time.Second))

	if first == 0 {
		t.Fatal("first round published nothing")
	}
	if got := len(sink.all()); got != first {
		t.Errorf("expected no further events within the same interval, got %d after %d", got, first)
	}
}

// A stalled emitter resumes with one current round rather than backfilling every
// interval it missed.
func TestInsightsDoesNotBackfillMissedIntervals(t *testing.T) {
	svc, sink := insightsSvc(t)
	enabledCluster(t, svc)

	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	svc.emitInsights(now)
	perRound := len(sink.all())
	if perRound == 0 {
		t.Fatal("first round published nothing")
	}

	svc.emitInsights(now.Add(30 * time.Minute))

	if got := len(sink.all()); got != 2*perRound {
		t.Errorf("expected %d events after a 30-minute gap (one round), got %d", 2*perRound, got)
	}
}

func TestInsightsNoSinkNoPanic(t *testing.T) {
	svc := newSvc()
	svc.dockerAvail = false
	enabledCluster(t, svc)
	svc.emitInsights(time.Now().UTC()) // no sink wired — must be a no-op
}

// --- Event shapes ---

func TestInsightsTaskEventShape(t *testing.T) {
	svc, sink := insightsSvc(t)
	enabledCluster(t, svc)

	emitAt(svc, time.Now().UTC().Truncate(time.Minute))

	m := sink.decoded(t, "Task")[0]
	for _, field := range []string{
		"Version", "Type", "TaskId", "TaskDefinitionFamily", "TaskDefinitionRevision",
		"ServiceName", "ClusterName", "AccountID", "Region", "AvailabilityZone",
		"KnownStatus", "LaunchType", "CreatedAt", "Timestamp",
		"CpuUtilized", "CpuReserved", "MemoryUtilized", "MemoryReserved",
		"StorageReadBytes", "StorageWriteBytes",
		"NetworkRxBytes", "NetworkRxDropped", "NetworkRxErrors", "NetworkRxPackets",
		"NetworkTxBytes", "NetworkTxDropped", "NetworkTxErrors", "NetworkTxPackets",
		"EphemeralStorageUtilized", "EphemeralStorageReserved", "CloudWatchMetrics",
	} {
		if _, ok := m[field]; !ok {
			t.Errorf("Task event is missing %s", field)
		}
	}

	if m["ClusterName"] != "prod" {
		t.Errorf("ClusterName = %v, want prod", m["ClusterName"])
	}
	if m["ServiceName"] != "api-svc" {
		t.Errorf("ServiceName = %v, want api-svc", m["ServiceName"])
	}
	if m["TaskDefinitionFamily"] != "web" {
		t.Errorf("TaskDefinitionFamily = %v, want web", m["TaskDefinitionFamily"])
	}
	// Revision is a string in the real events, not a number.
	if rev, ok := m["TaskDefinitionRevision"].(string); !ok || rev != "1" {
		t.Errorf("TaskDefinitionRevision = %#v, want string \"1\"", m["TaskDefinitionRevision"])
	}
	// TaskId is the 32-hex form, not the dashed UUID of the ARN.
	if id, ok := m["TaskId"].(string); !ok || len(id) != 32 {
		t.Errorf("TaskId = %#v, want 32 hex characters", m["TaskId"])
	}
	if m["CpuReserved"].(float64) != 256 {
		t.Errorf("CpuReserved = %v, want 256 (task definition cpu)", m["CpuReserved"])
	}
	if m["MemoryReserved"].(float64) != 512 {
		t.Errorf("MemoryReserved = %v, want 512 (task definition memory)", m["MemoryReserved"])
	}
	if m["Region"] != "us-east-1" || m["AvailabilityZone"] != "us-east-1a" {
		t.Errorf("Region/AZ = %v/%v", m["Region"], m["AvailabilityZone"])
	}
}

func TestInsightsContainerEventShape(t *testing.T) {
	svc, sink := insightsSvc(t)
	enabledCluster(t, svc)

	emitAt(svc, time.Now().UTC().Truncate(time.Minute))

	m := sink.decoded(t, "Container")[0]
	if m["ContainerName"] != "app" {
		t.Errorf("ContainerName = %v, want app", m["ContainerName"])
	}
	if m["Image"] != "nginx:latest" {
		t.Errorf("Image = %v, want nginx:latest", m["Image"])
	}
	if _, ok := m["ContainerKnownStatus"]; !ok {
		t.Error("Container event is missing ContainerKnownStatus")
	}
	// Real Container events carry no metric-extraction block.
	if _, ok := m["CloudWatchMetrics"]; ok {
		t.Error("Container event should not carry CloudWatchMetrics")
	}
}

func TestInsightsClusterAndServiceEventShape(t *testing.T) {
	svc, sink := insightsSvc(t)
	enabledCluster(t, svc)

	emitAt(svc, time.Now().UTC().Truncate(time.Minute))

	c := sink.decoded(t, "Cluster")[0]
	if c["ClusterName"] != "prod" {
		t.Errorf("ClusterName = %v, want prod", c["ClusterName"])
	}
	if c["TaskCount"].(float64) != 1 {
		t.Errorf("TaskCount = %v, want 1", c["TaskCount"])
	}
	if c["ServiceCount"].(float64) != 1 {
		t.Errorf("ServiceCount = %v, want 1", c["ServiceCount"])
	}
	if c["ContainerInstanceCount"].(float64) != 0 {
		t.Errorf("ContainerInstanceCount = %v, want 0 for Fargate", c["ContainerInstanceCount"])
	}

	s := sink.decoded(t, "Service")[0]
	if s["ServiceName"] != "api-svc" {
		t.Errorf("ServiceName = %v, want api-svc", s["ServiceName"])
	}
	if s["DesiredTaskCount"].(float64) != 1 {
		t.Errorf("DesiredTaskCount = %v, want 1", s["DesiredTaskCount"])
	}
	if s["RunningTaskCount"].(float64) != 1 {
		t.Errorf("RunningTaskCount = %v, want 1", s["RunningTaskCount"])
	}
}

// A standalone RunTask task belongs to no service, and the real events leave
// ServiceName out entirely rather than sending an empty string.
func TestInsightsStandaloneTaskOmitsServiceName(t *testing.T) {
	svc, sink := insightsSvc(t)
	do(t, svc, "CreateCluster", map[string]interface{}{
		"clusterName": "prod",
		"settings":    []clusterSetting{{Name: "containerInsights", Value: "enabled"}},
	})
	do(t, svc, "RegisterTaskDefinition", map[string]interface{}{"family": "batch", "cpu": "512", "memory": "1024"})
	do(t, svc, "RunTask", map[string]interface{}{"cluster": "prod", "taskDefinition": "batch"})

	emitAt(svc, time.Now().UTC().Truncate(time.Minute))

	m := sink.decoded(t, "Task")[0]
	if _, ok := m["ServiceName"]; ok {
		t.Errorf("standalone task should omit ServiceName, got %v", m["ServiceName"])
	}
	if len(sink.decoded(t, "Service")) != 0 {
		t.Error("no service exists, so no Service event should be published")
	}
}

// --- Synthetic values ---

func TestInsightsUtilisationStaysWithinReservation(t *testing.T) {
	svc, sink := insightsSvc(t)
	enabledCluster(t, svc)

	slot := time.Now().UTC().Truncate(time.Minute)
	for i := 0; i < 20; i++ {
		emitAt(svc, slot.Add(time.Duration(i)*time.Minute))
	}

	for _, m := range sink.decoded(t, "Task") {
		cpu, res := m["CpuUtilized"].(float64), m["CpuReserved"].(float64)
		if cpu < 0 || cpu > res {
			t.Errorf("CpuUtilized %v outside [0, %v]", cpu, res)
		}
		mem, memRes := m["MemoryUtilized"].(float64), m["MemoryReserved"].(float64)
		if mem < 0 || mem > memRes {
			t.Errorf("MemoryUtilized %v outside [0, %v]", mem, memRes)
		}
	}
}

// The *Packets fields are cumulative counters in the real events; the *Bytes
// fields are per-second rates, so only the former may be assumed to grow.
func TestInsightsPacketCountersAccumulate(t *testing.T) {
	svc, sink := insightsSvc(t)
	enabledCluster(t, svc)

	slot := time.Now().UTC().Truncate(time.Minute)
	for i := 0; i < 5; i++ {
		emitAt(svc, slot.Add(time.Duration(i)*time.Minute))
	}

	tasks := sink.decoded(t, "Task")
	if len(tasks) != 5 {
		t.Fatalf("expected 5 Task events, got %d", len(tasks))
	}
	for _, field := range []string{"NetworkRxPackets", "NetworkTxPackets"} {
		prev := tasks[0][field].(float64)
		for _, m := range tasks[1:] {
			cur := m[field].(float64)
			if cur < prev {
				t.Errorf("%s went backwards: %v then %v", field, prev, cur)
			}
			prev = cur
		}
	}
}

func TestResetClearsInsightsState(t *testing.T) {
	svc, sink := insightsSvc(t)
	enabledCluster(t, svc)
	emitAt(svc, time.Now().UTC().Truncate(time.Minute))
	if len(sink.all()) == 0 {
		t.Fatal("no events published")
	}

	svc.Reset()

	svc.mu.RLock()
	counters, slot := len(svc.insightsCounters), svc.lastInsightsSlot
	svc.mu.RUnlock()
	if counters != 0 {
		t.Errorf("Reset left %d synthetic counters behind", counters)
	}
	if !slot.IsZero() {
		t.Errorf("Reset left lastInsightsSlot at %v", slot)
	}
}
