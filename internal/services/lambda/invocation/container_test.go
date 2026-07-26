package invocation

import (
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

func TestImageArgv_ImageConfigOverridesWin(t *testing.T) {
	// Overrides are used verbatim, so no image inspection is needed — which is
	// also what makes this path work for an image that is not pulled yet.
	argv, err := imageArgv(ImageSpec{
		ImageURI:   "example.com/app:v1",
		EntryPoint: []string{"/usr/bin/python3", "-m", "awslambdaric"},
		Command:    []string{"app.handler"},
	})
	if err != nil {
		t.Fatalf("imageArgv: %v", err)
	}
	want := []string{"/usr/bin/python3", "-m", "awslambdaric", "app.handler"}
	if strings.Join(argv, " ") != strings.Join(want, " ") {
		t.Errorf("argv: expected %v, got %v", want, argv)
	}
}

func TestImageArgv_CommandOnly(t *testing.T) {
	argv, err := imageArgv(ImageSpec{
		ImageURI: "example.com/app:v1",
		Command:  []string{"app.handler"},
	})
	if err != nil {
		t.Fatalf("imageArgv: %v", err)
	}
	if len(argv) != 1 || argv[0] != "app.handler" {
		t.Errorf("argv: expected [app.handler], got %v", argv)
	}
}

func TestRIEAsset_MatchesArchitecture(t *testing.T) {
	tests := map[string]string{
		"arm64":   "aws-lambda-rie-arm64",
		"x86_64":  "aws-lambda-rie-x86_64",
		"":        "aws-lambda-rie-x86_64",
		"unknown": "aws-lambda-rie-x86_64",
	}
	for arch, want := range tests {
		if got := rieAsset(arch); got != want {
			t.Errorf("rieAsset(%q): expected %q, got %q", arch, want, got)
		}
	}
}

func TestDockerPlatform(t *testing.T) {
	tests := map[string]string{
		"arm64":  "linux/arm64",
		"x86_64": "linux/amd64",
		"":       "",
	}
	for arch, want := range tests {
		if got := dockerPlatform(arch); got != want {
			t.Errorf("dockerPlatform(%q): expected %q, got %q", arch, want, got)
		}
	}
}

// Function names allow characters Docker rejects in a container name.
func TestContainerNameFor_SanitizesFunctionName(t *testing.T) {
	got := containerNameFor("my:weird/fn name")
	if strings.ContainsAny(got, ":/ ") {
		t.Errorf("container name still holds characters Docker rejects: %q", got)
	}
	if !strings.HasPrefix(got, "nimbus-lambda-") {
		t.Errorf("container name should be namespaced, got %q", got)
	}
}

func TestAWSEnv_PointsHandlersAtNimbus(t *testing.T) {
	env := strings.Join(awsEnv("http://nimbus:4566"), "\n")
	for _, want := range []string{
		"AWS_ENDPOINT_URL=http://nimbus:4566",
		"AWS_ACCESS_KEY_ID=",
		"AWS_SECRET_ACCESS_KEY=",
		"AWS_REGION=",
	} {
		if !strings.Contains(env, want) {
			t.Errorf("expected env to contain %q, got:\n%s", want, env)
		}
	}
}

func TestDefaultAWSEndpoint_HonoursOverride(t *testing.T) {
	t.Setenv("NIMBUS_LAMBDA_AWS_ENDPOINT", "http://custom:9999")
	if got := defaultAWSEndpoint(true); got != "http://custom:9999" {
		t.Errorf("expected the override to win, got %q", got)
	}
}

// Outside Docker the handler reaches Nimbus back across the host boundary.
func TestDefaultAWSEndpoint_OutsideDocker(t *testing.T) {
	t.Setenv("NIMBUS_LAMBDA_AWS_ENDPOINT", "")
	if got := defaultAWSEndpoint(false); !strings.Contains(got, "host.docker.internal") {
		t.Errorf("expected a host-reachable endpoint, got %q", got)
	}
}

func TestInvokeURL(t *testing.T) {
	want := "http://c:8080/2015-03-31/functions/function/invocations"
	if got := invokeURL("http://c:8080"); got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestWaitForPort_ReturnsWhenListening(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	if err := waitForPort("http://"+ln.Addr().String(), 2*time.Second); err != nil {
		t.Errorf("waitForPort on a live listener: %v", err)
	}
}

func TestWaitForPort_TimesOutWhenClosed(t *testing.T) {
	// Bind then close, so the port is almost certainly free.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	if err := waitForPort("http://"+addr, 300*time.Millisecond); err == nil {
		t.Error("expected a timeout against a closed port")
	}
}

func TestEnsureRIE_HonoursPathOverride(t *testing.T) {
	f := t.TempDir() + "/rie"
	if err := writeFile(f, "binary"); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("NIMBUS_LAMBDA_RIE_PATH", f)

	got, err := (&containerRunner{}).ensureRIE("arm64")
	if err != nil {
		t.Fatalf("ensureRIE: %v", err)
	}
	if got != f {
		t.Errorf("expected the override path %q, got %q", f, got)
	}
}

// A misconfigured override must fail loudly rather than silently downloading.
func TestEnsureRIE_RejectsMissingOverridePath(t *testing.T) {
	t.Setenv("NIMBUS_LAMBDA_RIE_PATH", t.TempDir()+"/does-not-exist")

	if _, err := (&containerRunner{}).ensureRIE("arm64"); err == nil {
		t.Error("expected an error for an unreadable override path")
	}
}

// ── Service integration ───────────────────────────────────────────────────────

type stubChecker struct{ names map[string]bool }

func (s stubChecker) FunctionExists(name string) bool { return s.names[name] }

type stubImages struct{ specs map[string]ImageSpec }

func (s stubImages) ImageSpec(name string) (ImageSpec, bool) {
	spec, ok := s.specs[name]
	return spec, ok
}

// Without EnableContainers the mock path is untouched.
func TestContainerTarget_InactiveWhenNotEnabled(t *testing.T) {
	svc := New(stubChecker{names: map[string]bool{"fn": true}})

	endpoint, timeout, err := svc.containerTarget("fn")
	if err != nil || endpoint != "" || timeout != 0 {
		t.Errorf("expected no container target, got %q/%v/%v", endpoint, timeout, err)
	}
}

// A zip function has no image spec, so it keeps using the mock path even when
// container execution is on.
func TestContainerTarget_InactiveForNonImageFunction(t *testing.T) {
	svc := New(stubChecker{names: map[string]bool{"zip": true}})
	svc.images = stubImages{specs: map[string]ImageSpec{}}
	svc.runner = &containerRunner{available: true, containers: map[string]*runningContainer{}}

	endpoint, _, err := svc.containerTarget("zip")
	if err != nil || endpoint != "" {
		t.Errorf("expected no container target for a zip function, got %q/%v", endpoint, err)
	}
}

// Docker being absent is not an error — image functions fall back to the mock.
func TestContainerTarget_InactiveWhenDockerUnavailable(t *testing.T) {
	svc := New(stubChecker{names: map[string]bool{"img": true}})
	svc.images = stubImages{specs: map[string]ImageSpec{"img": {ImageURI: "x:1"}}}
	svc.runner = &containerRunner{available: false, containers: map[string]*runningContainer{}}

	endpoint, _, err := svc.containerTarget("img")
	if err != nil {
		t.Errorf("expected no error when Docker is unavailable, got %v", err)
	}
	if endpoint != "" {
		t.Errorf("expected no container target, got %q", endpoint)
	}
}

func TestContainers_EmptyWhenDisabled(t *testing.T) {
	svc := New(stubChecker{names: map[string]bool{}})
	if got := svc.Containers(); len(got) != 0 {
		t.Errorf("expected no containers, got %v", got)
	}
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}
