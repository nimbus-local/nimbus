package ecs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/nimbus-local/nimbus/internal/uid"
)

// containerDef is the subset of the ECS container definition Nimbus supports
// in Phase 1.
//
// Phase 1 limitations (see docs/north-star-roadmap.md Phase 1 notes):
//   - secrets: parsed but NOT injected — values are ignored with a warning.
//   - volumes / mountPoints: ignored.
//   - healthCheck: ignored — Docker's built-in default applies.
//   - logConfiguration: ignored — use /_nimbus/ecs/tasks/{id}/logs instead.
//   - links, dependsOn, ulimits, resourceRequirements: ignored.
//   - image pulls may make RunTask/CreateService slow if the image is not cached locally.
type containerDef struct {
	Name              string        `json:"name"`
	Image             string        `json:"image"`
	CPU               int           `json:"cpu"`
	Memory            int           `json:"memory"`
	MemoryReservation int           `json:"memoryReservation"`
	Essential         *bool         `json:"essential"`
	Environment       []envVar      `json:"environment"`
	Secrets           []ecsSecret   `json:"secrets"`
	PortMappings      []portMapping `json:"portMappings"`
	Command           []string      `json:"command"`
	EntryPoint        []string      `json:"entryPoint"`
}

type envVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type ecsSecret struct {
	Name      string `json:"name"`
	ValueFrom string `json:"valueFrom"`
}

type portMapping struct {
	ContainerPort int    `json:"containerPort"`
	HostPort      int    `json:"hostPort"`
	Protocol      string `json:"protocol"`
}

// initDocker checks whether the Docker CLI is available and the daemon is
// reachable. Returns true if tasks should run as real containers.
func initDocker() bool {
	if err := exec.Command("docker", "info").Run(); err != nil {
		slog.Warn("ECS: Docker not available — tasks will be simulated", "err", err)
		return false
	}
	slog.Info("ECS: Docker connected — RunTask will start real containers")
	return true
}

// startTaskContainers creates and starts a Docker container for each entry in
// td's container definitions via the docker CLI. On success the task's
// containers field is updated under the lock. Callers must NOT hold s.mu.
func (s *Service) startTaskContainers(taskArn string, td *taskDef) error {
	var defs []containerDef
	for _, raw := range td.containerDefinitions {
		var d containerDef
		if err := json.Unmarshal(raw, &d); err != nil {
			return fmt.Errorf("parse container def: %w", err)
		}
		if d.Image == "" {
			continue
		}
		defs = append(defs, d)
	}
	if len(defs) == 0 {
		return fmt.Errorf("task definition has no usable container definitions")
	}

	shortID := lastSegment(taskArn)
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}

	containers := make(map[string]string, len(defs))

	for _, def := range defs {
		if len(def.Secrets) > 0 {
			slog.Warn("ECS: secrets not resolved in Phase 1 — skipping injection",
				"container", def.Name, "secrets", len(def.Secrets))
		}

		args := []string{"run", "-d",
			"--name", fmt.Sprintf("nimbus-ecs-%s-%s", shortID, def.Name),
			"--network", s.networkName,
			"--label", "nimbus.task.arn=" + taskArn,
			"--label", "nimbus.service=ecs",
		}

		for _, e := range def.Environment {
			args = append(args, "-e", e.Name+"="+e.Value)
		}

		for _, pm := range def.PortMappings {
			proto := pm.Protocol
			if proto == "" {
				proto = "tcp"
			}
			if pm.HostPort > 0 {
				args = append(args, "-p", fmt.Sprintf("%d:%d/%s", pm.HostPort, pm.ContainerPort, proto))
			} else {
				args = append(args, "-p", fmt.Sprintf("%d/%s", pm.ContainerPort, proto))
			}
		}

		// Memory: container-level MB; fall back to task-level
		memMB := def.Memory
		if memMB == 0 && td.memory != "" {
			fmt.Sscanf(td.memory, "%d", &memMB)
		}
		if memMB > 0 {
			args = append(args, "--memory", fmt.Sprintf("%dm", memMB))
		}

		// CPU: ECS units → fractional vCPUs (1024 units = 1 vCPU)
		cpuUnits := def.CPU
		if cpuUnits == 0 && td.cpu != "" {
			fmt.Sscanf(td.cpu, "%d", &cpuUnits)
		}
		if cpuUnits > 0 {
			args = append(args, "--cpus", fmt.Sprintf("%.4f", float64(cpuUnits)/1024))
		}

		if len(def.EntryPoint) > 0 {
			args = append(args, "--entrypoint", strings.Join(def.EntryPoint, " "))
		}

		args = append(args, def.Image)
		args = append(args, def.Command...)

		out, err := exec.Command("docker", args...).Output()
		if err != nil {
			// Clean up already-started containers for this task
			for _, cid := range containers {
				exec.Command("docker", "rm", "-f", cid).Run()
			}
			return fmt.Errorf("start container %q: %w", def.Name, err)
		}

		cid := strings.TrimSpace(string(out))
		containers[def.Name] = cid
		slog.Info("ECS: container started", "name", def.Name, "id", cid[:12], "task", shortID)
	}

	s.mu.Lock()
	if t, ok := s.tasks[taskArn]; ok {
		t.containers = containers
	}
	s.mu.Unlock()

	return nil
}

// startTaskAsync starts containers in a background goroutine. On failure the
// task transitions to STOPPED.
func (s *Service) startTaskAsync(taskArn string, td *taskDef) {
	if err := s.startTaskContainers(taskArn, td); err != nil {
		slog.Warn("ECS: container start failed", "task", taskArn, "err", err)
		s.mu.Lock()
		if t, ok := s.tasks[taskArn]; ok && t.lastStatus != "STOPPED" {
			t.lastStatus = "STOPPED"
			t.desiredStatus = "STOPPED"
			t.stoppedReason = err.Error()
		}
		s.mu.Unlock()
	}
}

// stopTaskContainers stops and removes Docker containers. Errors are only logged.
func (s *Service) stopTaskContainers(containerIDs map[string]string) {
	for name, cid := range containerIDs {
		if err := exec.Command("docker", "stop", cid).Run(); err != nil {
			slog.Debug("ECS: stop container", "name", name, "err", err)
		}
		if err := exec.Command("docker", "rm", "-f", cid).Run(); err != nil {
			slog.Debug("ECS: remove container", "name", name, "err", err)
		}
	}
}

// pollTaskLifecycle polls Docker every 5 s to transition task statuses.
func (s *Service) pollTaskLifecycle() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		s.checkTaskContainers()
	}
}

type taskContainerSnapshot struct {
	taskArn    string
	clusterArn string
	lastStatus string
	containers map[string]string
}

func (s *Service) checkTaskContainers() {
	// Step 1: snapshot non-stopped tasks that have real containers (read lock).
	s.mu.RLock()
	var snaps []taskContainerSnapshot
	for _, t := range s.tasks {
		if t.lastStatus == "STOPPED" || len(t.containers) == 0 {
			continue
		}
		ids := make(map[string]string, len(t.containers))
		for k, v := range t.containers {
			ids[k] = v
		}
		snaps = append(snaps, taskContainerSnapshot{
			taskArn:    t.taskArn,
			clusterArn: t.clusterArn,
			lastStatus: t.lastStatus,
			containers: ids,
		})
	}
	s.mu.RUnlock()

	// Step 2: inspect containers via CLI (no lock — commands may block briefly).
	for _, snap := range snaps {
		allRunning := true
		anyExited := false
		for _, cid := range snap.containers {
			out, err := exec.Command("docker", "inspect",
				"--format={{.State.Running}}", cid).Output()
			if err != nil || strings.TrimSpace(string(out)) != "true" {
				allRunning = false
				anyExited = true
			}
		}

		// Step 3: update task state (write lock).
		s.mu.Lock()
		t, ok := s.tasks[snap.taskArn]
		if ok && t.lastStatus != "STOPPED" {
			switch {
			case t.lastStatus == "PENDING" && allRunning:
				t.lastStatus = "RUNNING"
				t.startedAt = time.Now().UTC()
				if c, cok := s.resolveCluster(t.clusterArn); cok {
					c.runningTasksCount++
				}
			case anyExited:
				if t.lastStatus == "RUNNING" {
					if c, cok := s.resolveCluster(t.clusterArn); cok {
						c.runningTasksCount--
					}
				}
				t.lastStatus = "STOPPED"
				t.desiredStatus = "STOPPED"
			}
		}
		s.mu.Unlock()
	}
}

// reconcileServices keeps each service at its desiredCount, restarting
// containers that have exited. Runs every 10 s.
func (s *Service) reconcileServices() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		s.reconcile()
	}
}

type reconcileWork struct {
	svc     *ecsService
	td      taskDef // value copy — safe outside lock
	deficit int
}

func (s *Service) reconcile() {
	s.mu.Lock()
	var work []reconcileWork
	for _, svc := range s.services {
		if svc.status != "ACTIVE" || svc.desiredCount == 0 {
			continue
		}
		active := 0
		for _, t := range s.tasks {
			if t.group == "service:"+svc.name &&
				(t.lastStatus == "RUNNING" || t.lastStatus == "PENDING") {
				active++
			}
		}
		svc.runningCount = active
		deficit := svc.desiredCount - active
		if deficit <= 0 {
			continue
		}
		td, ok := s.resolveTaskDef(svc.taskDefArn)
		if !ok {
			continue
		}
		work = append(work, reconcileWork{svc: svc, td: *td, deficit: deficit})
	}
	s.mu.Unlock()

	for _, w := range work {
		td := w.td
		for i := 0; i < w.deficit; i++ {
			s.launchTaskForService(w.svc, &td)
		}
	}
}

func (s *Service) launchTaskForService(svc *ecsService, td *taskDef) {
	now := time.Now().UTC()
	t := &ecsTask{
		taskArn:       s.taskARN(lastSegment(svc.clusterArn), uid.New()),
		taskDefArn:    td.arn,
		clusterArn:    svc.clusterArn,
		group:         "service:" + svc.name,
		launchType:    svc.launchType,
		cpu:           td.cpu,
		memory:        td.memory,
		lastStatus:    "PENDING",
		desiredStatus: "RUNNING",
		createdAt:     now,
		containers:    map[string]string{},
	}

	s.mu.Lock()
	s.tasks[t.taskArn] = t
	s.mu.Unlock()

	go s.startTaskAsync(t.taskArn, td)
}

// LogsHandler streams stdout/stderr from all containers in a task.
// URL: GET /_nimbus/ecs/tasks/{taskID}/logs
func (s *Service) LogsHandler(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/_nimbus/ecs/tasks/")
	taskID := strings.TrimSuffix(strings.TrimSuffix(path, "/"), "/logs")

	s.mu.RLock()
	t, ok := s.resolveTask("", taskID)
	var containerIDs map[string]string
	if ok && len(t.containers) > 0 {
		containerIDs = make(map[string]string, len(t.containers))
		for k, v := range t.containers {
			containerIDs[k] = v
		}
	}
	s.mu.RUnlock()

	if !ok {
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}
	if !s.dockerAvail || len(containerIDs) == 0 {
		http.Error(w, "no containers for this task (simulation mode or task still pending)",
			http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	for name, cid := range containerIDs {
		fmt.Fprintf(w, "=== %s ===\n", name)
		cmd := exec.CommandContext(context.Background(), "docker", "logs",
			"--timestamps", "--tail", "200", cid)
		cmd.Stdout = w
		cmd.Stderr = w
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(w, "error: %v\n", err)
		}
	}
}

// hasContainerImages reports whether td has at least one container definition
// with a non-empty image. Used to decide whether to use Docker or simulate.
func hasContainerImages(td *taskDef) bool {
	for _, raw := range td.containerDefinitions {
		var d struct {
			Image string `json:"image"`
		}
		if json.Unmarshal(raw, &d) == nil && d.Image != "" {
			return true
		}
	}
	return false
}

func lastSegment(s string) string {
	return s[strings.LastIndex(s, "/")+1:]
}
