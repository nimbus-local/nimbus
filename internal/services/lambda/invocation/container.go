package invocation

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/nimbus-local/nimbus/internal/uid"
)

// ImageSpec is everything needed to start a container-image function.
type ImageSpec struct {
	ImageURI    string
	Arch        string // "x86_64" | "arm64"
	MemoryMB    int
	TimeoutSec  int
	EphemeralMB int
	Env         map[string]string
	EntryPoint  []string // ImageConfig overrides, empty to use the image's own
	Command     []string
	WorkingDir  string
}

// ImageFunctionSource supplies container-image details for a function. The
// function store implements it; when it is absent or reports false, invocation
// falls back to the mock/proxy path.
type ImageFunctionSource interface {
	ImageSpec(name string) (ImageSpec, bool)
}

// rieContainerPort is the port the Runtime Interface Emulator listens on.
const rieContainerPort = "8080"

// riePath is where the emulator is placed inside the function's container.
const riePath = "/aws-lambda-rie"

// rieReleaseURL is the published emulator binary for an architecture.
const rieReleaseURL = "https://github.com/aws/aws-lambda-runtime-interface-emulator/" +
	"releases/latest/download/aws-lambda-rie-%s"

// LogSink receives a function container's output. CloudWatch Logs implements
// it; when absent, container output is simply not forwarded.
type LogSink interface {
	Ingest(group, stream string, messages []string)
}

// defaultIdleTimeout is how long a container may sit unused before it is
// reaped. Lambda keeps an execution environment warm for a similar span.
const defaultIdleTimeout = 10 * time.Minute

type runningContainer struct {
	id       string
	endpoint string
	lastUsed time.Time
	inflight int
	stopLogs context.CancelFunc
}

// containerRunner starts and reuses one container per container-image function.
//
// A Lambda container image is not an HTTP server: the process inside is a
// client of the Lambda Runtime API, polling for work that in production AWS
// supplies. The Runtime Interface Emulator provides that missing server half,
// so Nimbus injects it as the entrypoint and hands the image's original
// argv to it as the program to run.
type containerRunner struct {
	mu          sync.Mutex
	available   bool
	network     string
	dataDir     string
	awsEndpoint string
	inDocker    bool
	logs        LogSink
	idleTimeout time.Duration
	containers  map[string]*runningContainer
	done        chan struct{}
	stopOnce    sync.Once
}

func newContainerRunner(dataDir string, logs LogSink) *containerRunner {
	network := os.Getenv("NIMBUS_DOCKER_NETWORK")
	if network == "" {
		network = "nimbus-net"
	}
	r := &containerRunner{
		network:     network,
		dataDir:     dataDir,
		inDocker:    inDocker(),
		logs:        logs,
		idleTimeout: idleTimeoutFromEnv(),
		containers:  map[string]*runningContainer{},
		done:        make(chan struct{}),
		available:   exec.Command("docker", "info").Run() == nil,
	}
	r.awsEndpoint = defaultAWSEndpoint(r.inDocker)
	if !r.available {
		slog.Warn("Lambda: Docker not available — image functions fall back to mock responses")
		return r
	}
	if r.idleTimeout > 0 {
		go r.reapLoop()
	}
	return r
}

// idleTimeoutFromEnv reads NIMBUS_LAMBDA_CONTAINER_IDLE. Zero disables reaping,
// which is useful when attaching a debugger to a warm container.
func idleTimeoutFromEnv() time.Duration {
	raw := os.Getenv("NIMBUS_LAMBDA_CONTAINER_IDLE")
	if raw == "" {
		return defaultIdleTimeout
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		slog.Warn("Lambda: invalid NIMBUS_LAMBDA_CONTAINER_IDLE, using default",
			"value", raw, "default", defaultIdleTimeout)
		return defaultIdleTimeout
	}
	return d
}

// reapLoop removes containers that have gone unused, so a long dev session does
// not accumulate one idle container per function it happened to invoke.
func (r *containerRunner) reapLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.done:
			return
		case <-ticker.C:
			r.reapIdle()
		}
	}
}

func (r *containerRunner) reapIdle() {
	r.mu.Lock()
	var reaped []*runningContainer
	for name, c := range r.containers {
		// An invocation in flight holds the container open however long it runs;
		// a function may legitimately take longer than the idle window.
		if c.inflight == 0 && time.Since(c.lastUsed) > r.idleTimeout {
			reaped = append(reaped, c)
			delete(r.containers, name)
			slog.Info("Lambda: reaping idle container",
				"function", name, "id", shortDockerID(c.id), "idle", time.Since(c.lastUsed))
		}
	}
	r.mu.Unlock()

	for _, c := range reaped {
		r.teardown(c)
	}
}

func inDocker() bool {
	_, err := os.Stat("/.dockerenv")
	return err == nil
}

// defaultAWSEndpoint is the Nimbus URL handed to function containers so their
// SDK calls come back here instead of reaching real AWS.
func defaultAWSEndpoint(inDocker bool) string {
	if v := os.Getenv("NIMBUS_LAMBDA_AWS_ENDPOINT"); v != "" {
		return v
	}
	if !inDocker {
		return "http://host.docker.internal:4566"
	}
	// On a user-defined network Docker's DNS resolves a container by its
	// hostname, so the function container can reach Nimbus by name.
	host, err := os.Hostname()
	if err != nil || host == "" {
		return "http://host.docker.internal:4566"
	}
	return "http://" + host + ":4566"
}

// ensure returns the invoke endpoint for a function's container, starting one
// if it is not already warm. Containers are reused between invocations, which
// is both faster and closer to how Lambda reuses execution environments.
//
// The returned release function must be called when the invocation finishes:
// until it is, the idle reaper leaves the container alone.
func (r *containerRunner) ensure(name string, spec ImageSpec) (string, func(), error) {
	if !r.available {
		return "", nil, fmt.Errorf("Docker is not available")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	c, ok := r.containers[name]
	if ok && !r.isRunning(c.id) {
		// Exited between invocations — clear it out and start fresh.
		r.teardown(c)
		delete(r.containers, name)
		ok = false
	}

	if !ok {
		started, err := r.start(name, spec)
		if err != nil {
			return "", nil, err
		}
		r.containers[name] = started
		c = started
	}

	c.lastUsed = time.Now()
	c.inflight++
	return c.endpoint, func() { r.release(name) }, nil
}

// release marks an invocation finished, making the container eligible for
// reaping once it has been idle long enough.
func (r *containerRunner) release(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.containers[name]; ok {
		if c.inflight > 0 {
			c.inflight--
		}
		c.lastUsed = time.Now()
	}
}

func (r *containerRunner) start(name string, spec ImageSpec) (*runningContainer, error) {
	if err := r.ensureImage(spec.ImageURI); err != nil {
		return nil, err
	}

	rie, err := r.ensureRIE(spec.Arch)
	if err != nil {
		return nil, err
	}

	argv, err := imageArgv(spec)
	if err != nil {
		return nil, err
	}

	containerName := containerNameFor(name)
	r.removeByName(containerName)

	args := []string{"create",
		"--name", containerName,
		"--entrypoint", riePath,
		"--label", "nimbus.service=lambda",
		"--label", "nimbus.lambda.function=" + name,
		// A fresh mount keeps /tmp empty per cold start, the way Lambda's
		// ephemeral storage shadows whatever the image build left behind.
		// Disk-backed rather than tmpfs: a RAM-backed /tmp would be charged
		// against --memory, which real ephemeral storage is not.
		"--mount", "type=volume,dst=/tmp",
	}
	if r.network != "" {
		args = append(args, "--network", r.network)
	}
	if !r.inDocker {
		// Nimbus is outside the container network and reaches the function
		// through a published port instead.
		args = append(args, "-p", "0:"+rieContainerPort)
	}
	if plat := dockerPlatform(spec.Arch); plat != "" {
		args = append(args, "--platform", plat)
	}
	if spec.MemoryMB > 0 {
		args = append(args, "--memory", fmt.Sprintf("%dm", spec.MemoryMB))
	}
	if spec.WorkingDir != "" {
		args = append(args, "--workdir", spec.WorkingDir)
	}
	for _, kv := range awsEnv(r.awsEndpoint) {
		args = append(args, "-e", kv)
	}
	for k, v := range spec.Env {
		args = append(args, "-e", k+"="+v)
	}
	args = append(args, spec.ImageURI)
	args = append(args, argv...)

	out, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("create container: %w: %s", err, strings.TrimSpace(string(out)))
	}
	id := strings.TrimSpace(string(out))

	// docker cp, not a bind mount: Nimbus talks to the host daemon, so a
	// -v source path would be resolved against the host filesystem rather than
	// this process's, which silently mounts an empty directory when Nimbus runs
	// in a container itself.
	if out, err := exec.Command("docker", "cp", rie, id+":"+riePath).CombinedOutput(); err != nil {
		r.remove(id)
		return nil, fmt.Errorf("copy runtime emulator: %w: %s", err, strings.TrimSpace(string(out)))
	}

	if out, err := exec.Command("docker", "start", id).CombinedOutput(); err != nil {
		r.remove(id)
		return nil, fmt.Errorf("start container: %w: %s", err, strings.TrimSpace(string(out)))
	}

	endpoint, err := r.resolveEndpoint(id, containerName)
	if err != nil {
		r.remove(id)
		return nil, err
	}

	if err := waitForPort(endpoint, 30*time.Second); err != nil {
		logs := containerLogs(id)
		r.remove(id)
		return nil, fmt.Errorf("%w\ncontainer output:\n%s", err, logs)
	}

	c := &runningContainer{id: id, endpoint: endpoint, lastUsed: time.Now()}

	// One log stream per container, the way Lambda opens one per execution
	// environment rather than per invocation.
	if r.logs != nil {
		ctx, cancel := context.WithCancel(context.Background())
		c.stopLogs = cancel
		go r.streamLogs(ctx, name, id, logStreamName())
	}

	slog.Info("Lambda: container started",
		"function", name, "image", spec.ImageURI, "id", shortDockerID(id))
	return c, nil
}

// streamLogs forwards a container's output into CloudWatch Logs under the log
// group Lambda would use. It exits when the context is cancelled or the
// container goes away, which ends `docker logs -f`.
func (r *containerRunner) streamLogs(ctx context.Context, function, id, stream string) {
	group := "/aws/lambda/" + function

	cmd := exec.CommandContext(ctx, "docker", "logs", "-f", "--tail", "0", id)
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		slog.Debug("Lambda: log stream unavailable", "function", function, "err", err)
		return
	}
	// A handler's diagnostics go to stderr; Lambda records both in one stream.
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		slog.Debug("Lambda: log stream failed to start", "function", function, "err", err)
		return
	}
	defer cmd.Wait() //nolint:errcheck — a cancelled stream always reports an error

	lines := make(chan string, 256)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(pipe)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			select {
			case lines <- scanner.Text():
			case <-ctx.Done():
				return
			}
		}
	}()

	// Batch before ingesting: a chatty handler would otherwise take the log
	// store's lock once per line.
	flush := time.NewTicker(250 * time.Millisecond)
	defer flush.Stop()
	var batch []string

	send := func() {
		if len(batch) > 0 {
			r.logs.Ingest(group, stream, batch)
			batch = nil
		}
	}

	for {
		select {
		case line, ok := <-lines:
			if !ok {
				send()
				return
			}
			batch = append(batch, line)
			if len(batch) >= 128 {
				send()
			}
		case <-flush.C:
			send()
		case <-ctx.Done():
			send()
			return
		}
	}
}

// logStreamName matches the shape Lambda uses for an execution environment.
func logStreamName() string {
	suffix := strings.ReplaceAll(uid.New(), "-", "")
	return time.Now().UTC().Format("2006/01/02") + "/[$LATEST]" + suffix
}

// teardown stops a container's log stream and removes it.
func (r *containerRunner) teardown(c *runningContainer) {
	if c.stopLogs != nil {
		c.stopLogs()
	}
	r.remove(c.id)
}

// resolveEndpoint returns the base URL Nimbus uses to reach the container:
// its network name when Nimbus shares the network, otherwise the published port.
func (r *containerRunner) resolveEndpoint(id, containerName string) (string, error) {
	if r.inDocker {
		return "http://" + containerName + ":" + rieContainerPort, nil
	}
	out, err := exec.Command("docker", "port", id, rieContainerPort).Output()
	if err != nil {
		return "", fmt.Errorf("read published port: %w", err)
	}
	// Output is one or more "0.0.0.0:32768" lines.
	line := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	idx := strings.LastIndex(line, ":")
	if idx < 0 {
		return "", fmt.Errorf("unexpected docker port output: %q", line)
	}
	return "http://127.0.0.1:" + line[idx+1:], nil
}

// invokeURL is the RIE path that accepts an invocation.
func invokeURL(endpoint string) string {
	return endpoint + "/2015-03-31/functions/function/invocations"
}

// ensureImage makes the image available locally, pulling it if needed. The
// reference may point at Nimbus's own registry or at a remote one.
func (r *containerRunner) ensureImage(imageURI string) error {
	if exec.Command("docker", "image", "inspect", imageURI).Run() == nil {
		return nil
	}
	slog.Info("Lambda: pulling image", "image", imageURI)
	out, err := exec.Command("docker", "pull", imageURI).CombinedOutput()
	if err != nil {
		return fmt.Errorf("image %q is not available locally and could not be pulled: %s",
			imageURI, strings.TrimSpace(string(out)))
	}
	return nil
}

// imageArgv is the program the emulator executes. ImageConfig overrides win, as
// they do in Lambda; otherwise the image's own entrypoint and command are used,
// since overriding --entrypoint to inject the emulator discards them.
func imageArgv(spec ImageSpec) ([]string, error) {
	if len(spec.EntryPoint) > 0 || len(spec.Command) > 0 {
		return append(append([]string{}, spec.EntryPoint...), spec.Command...), nil
	}

	out, err := exec.Command("docker", "inspect",
		"--format", "{{json .Config.Entrypoint}}\n{{json .Config.Cmd}}", spec.ImageURI).Output()
	if err != nil {
		return nil, fmt.Errorf("inspect image %q: %w", spec.ImageURI, err)
	}

	lines := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)
	var argv []string
	for _, line := range lines {
		var part []string
		if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &part); err == nil {
			argv = append(argv, part...)
		}
	}
	if len(argv) == 0 {
		return nil, fmt.Errorf(
			"image %q declares no entrypoint or command; set ImageConfig to supply one",
			spec.ImageURI)
	}
	return argv, nil
}

// ensureRIE returns a local path to the emulator binary for an architecture,
// downloading and caching it on first use. NIMBUS_LAMBDA_RIE_PATH overrides the
// download for offline environments.
func (r *containerRunner) ensureRIE(arch string) (string, error) {
	if p := os.Getenv("NIMBUS_LAMBDA_RIE_PATH"); p != "" {
		if _, err := os.Stat(p); err != nil {
			return "", fmt.Errorf("NIMBUS_LAMBDA_RIE_PATH %q is not readable: %w", p, err)
		}
		return p, nil
	}

	asset := rieAsset(arch)
	dir := filepath.Join(r.dataDir, "rie")
	dest := filepath.Join(dir, asset)
	if fi, err := os.Stat(dest); err == nil && fi.Size() > 0 {
		return dest, nil
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create emulator cache dir: %w", err)
	}

	url := fmt.Sprintf(rieReleaseURL, asset[len("aws-lambda-rie-"):])
	slog.Info("Lambda: downloading runtime interface emulator", "arch", arch, "url", url)

	resp, err := http.Get(url) //nolint:gosec — fixed upstream release URL
	if err != nil {
		return "", fmt.Errorf("download runtime emulator: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download runtime emulator: unexpected status %s", resp.Status)
	}

	// Write to a temporary name first so an interrupted download is never
	// mistaken for a cached binary on the next run.
	tmp := dest + ".partial"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return "", fmt.Errorf("create emulator cache file: %w", err)
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return "", fmt.Errorf("write runtime emulator: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("write runtime emulator: %w", err)
	}
	if err := os.Rename(tmp, dest); err != nil {
		return "", fmt.Errorf("cache runtime emulator: %w", err)
	}
	return dest, nil
}

func rieAsset(arch string) string {
	if arch == "arm64" {
		return "aws-lambda-rie-arm64"
	}
	return "aws-lambda-rie-x86_64"
}

func dockerPlatform(arch string) string {
	switch arch {
	case "arm64":
		return "linux/arm64"
	case "x86_64":
		return "linux/amd64"
	default:
		return ""
	}
}

// awsEnv is the credential and endpoint configuration a handler needs for its
// SDK calls to reach Nimbus. Values are placeholders — Nimbus accepts any.
func awsEnv(endpoint string) []string {
	return []string{
		"AWS_ENDPOINT_URL=" + endpoint,
		"AWS_ACCESS_KEY_ID=test",
		"AWS_SECRET_ACCESS_KEY=test",
		"AWS_DEFAULT_REGION=" + envOr("AWS_DEFAULT_REGION", "us-east-1"),
		"AWS_REGION=" + envOr("AWS_DEFAULT_REGION", "us-east-1"),
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

var unsafeNameChars = regexp.MustCompile(`[^A-Za-z0-9_.-]`)

func containerNameFor(function string) string {
	return "nimbus-lambda-" + unsafeNameChars.ReplaceAllString(function, "-")
}

func (r *containerRunner) isRunning(id string) bool {
	out, err := exec.Command("docker", "inspect", "--format", "{{.State.Running}}", id).Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

func (r *containerRunner) remove(id string) {
	if err := exec.Command("docker", "rm", "-f", id).Run(); err != nil {
		slog.Debug("Lambda: remove container", "id", shortDockerID(id), "err", err)
	}
}

func (r *containerRunner) removeByName(name string) {
	exec.Command("docker", "rm", "-f", name).Run() //nolint:errcheck — absent is the goal state
}

// stop tears down a function's container, if any.
func (r *containerRunner) stop(name string) {
	r.mu.Lock()
	c, ok := r.containers[name]
	delete(r.containers, name)
	r.mu.Unlock()

	if ok {
		r.teardown(c)
	}
}

// stopAll tears down every container this runner started. The reaper keeps
// running: a reset clears state but the service stays up.
func (r *containerRunner) stopAll() {
	r.mu.Lock()
	running := make([]*runningContainer, 0, len(r.containers))
	for _, c := range r.containers {
		running = append(running, c)
	}
	r.containers = map[string]*runningContainer{}
	r.mu.Unlock()

	for _, c := range running {
		r.teardown(c)
	}
}

// shutdown tears everything down and ends the reaper. For process exit only.
func (r *containerRunner) shutdown() {
	r.stopAll()
	if r.done != nil {
		r.stopOnce.Do(func() { close(r.done) })
	}
}

// running reports the container ID per function, for inspection.
func (r *containerRunner) running() map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]string, len(r.containers))
	for name, c := range r.containers {
		out[name] = c.id
	}
	return out
}

func containerLogs(id string) string {
	out, err := exec.Command("docker", "logs", "--tail", "50", id).CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func shortDockerID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// waitForPort blocks until the endpoint accepts a TCP connection.
func waitForPort(endpoint string, timeout time.Duration) error {
	addr := strings.TrimPrefix(strings.TrimPrefix(endpoint, "http://"), "https://")
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("container did not accept connections on %s within %s", addr, timeout)
}
