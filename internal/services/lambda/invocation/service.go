package invocation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// FunctionChecker lets the invocation service verify a function exists
// without creating an import cycle back to function_crud.
type FunctionChecker interface {
	FunctionExists(name string) bool
}

// InvocationRecord captures a single Lambda invocation for test inspection.
// Exposed via the /_nimbus/lambda/invocations endpoint.
type InvocationRecord struct {
	FunctionName   string          `json:"FunctionName"`
	Qualifier      string          `json:"Qualifier,omitempty"`
	InvocationType string          `json:"InvocationType"`
	Payload        json.RawMessage `json:"Payload,omitempty"`
	InvokedAt      time.Time       `json:"InvokedAt"`
}

// Service handles Lambda invocation operations and stores mock state.
type Service struct {
	mu            sync.RWMutex
	checker       FunctionChecker
	responses     map[string]json.RawMessage // configured mock response per function name
	invocations   []*InvocationRecord
	liveEndpoints map[string]string // function name → live HTTP endpoint
	images        ImageFunctionSource
	runner        *containerRunner
}

func New(checker FunctionChecker) *Service {
	return &Service{
		checker:       checker,
		responses:     map[string]json.RawMessage{},
		liveEndpoints: map[string]string{},
	}
}

// EnableContainers turns on real execution for container-image functions.
// Without it — or without a reachable Docker daemon — image functions keep
// using the mock/proxy path.
func (s *Service) EnableContainers(images ImageFunctionSource, dataDir string, logs LogSink) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.images = images
	s.runner = newContainerRunner(dataDir, logs)
}

// Reset clears all mock responses, recorded invocations, and live endpoints,
// and tears down any running function containers.
func (s *Service) Reset() {
	s.mu.Lock()
	runner := s.runner
	s.responses = map[string]json.RawMessage{}
	s.invocations = nil
	s.liveEndpoints = map[string]string{}
	s.mu.Unlock()

	if runner != nil {
		runner.stopAll()
	}
}

// StopContainers tears down every running function container, leaving the
// service able to start more.
func (s *Service) StopContainers() {
	s.mu.RLock()
	runner := s.runner
	s.mu.RUnlock()
	if runner != nil {
		runner.stopAll()
	}
}

// Shutdown tears down every container and stops the idle reaper. Called on
// process exit so containers do not outlive the process that started them.
func (s *Service) Shutdown() {
	s.mu.RLock()
	runner := s.runner
	s.mu.RUnlock()
	if runner != nil {
		runner.shutdown()
	}
}

// ReleaseFunction tears down a function's container, if it has one. Called
// when the function is deleted.
func (s *Service) ReleaseFunction(name string) {
	s.mu.RLock()
	runner := s.runner
	s.mu.RUnlock()
	if runner != nil {
		runner.stop(name)
	}
}

// containerTarget resolves the invoke URL for a container-image function,
// starting its container if it is not already warm. An empty endpoint with no
// error means the function is not backed by a container.
func (s *Service) containerTarget(name string) (string, time.Duration, func(), error) {
	s.mu.RLock()
	images, runner := s.images, s.runner
	s.mu.RUnlock()

	if images == nil || runner == nil || !runner.available {
		return "", 0, nil, nil
	}
	spec, ok := images.ImageSpec(name)
	if !ok {
		return "", 0, nil, nil
	}

	endpoint, release, err := runner.ensure(name, spec)
	if err != nil {
		return "", 0, nil, fmt.Errorf("start container for %s: %w", name, err)
	}
	return invokeURL(endpoint), time.Duration(spec.TimeoutSec) * time.Second, release, nil
}

// Containers reports the container ID backing each running image function.
func (s *Service) Containers() map[string]string {
	s.mu.RLock()
	runner := s.runner
	s.mu.RUnlock()
	if runner == nil {
		return map[string]string{}
	}
	return runner.running()
}

// ContainersHandler serves GET /_nimbus/lambda/containers for inspection and
// DELETE to tear every running container down.
func (s *Service) ContainersHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(s.Containers()) //nolint:errcheck
	case http.MethodDelete:
		s.StopContainers()
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// LiveEndpoint returns the registered live endpoint for the given function, if any.
func (s *Service) LiveEndpoint(name string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.liveEndpoints[name]
}

// LiveEndpoints returns a snapshot of all registered live endpoints.
func (s *Service) LiveEndpoints() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]string, len(s.liveEndpoints))
	for k, v := range s.liveEndpoints {
		out[k] = v
	}
	return out
}

// RegisterHandler serves POST /_nimbus/lambda/register and
// DELETE /_nimbus/lambda/register/{function_name} for forge live dev.
func (s *Service) RegisterHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var body struct {
			FunctionName string `json:"function_name"`
			Endpoint     string `json:"endpoint"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.FunctionName == "" || body.Endpoint == "" {
			http.Error(w, `{"message":"function_name and endpoint are required"}`, http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.liveEndpoints[body.FunctionName] = body.Endpoint
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"function_name":%q,"endpoint":%q}`, body.FunctionName, body.Endpoint)

	case http.MethodDelete:
		name := strings.TrimPrefix(r.URL.Path, "/_nimbus/lambda/register/")
		if name == "" {
			http.Error(w, `{"message":"function_name is required"}`, http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		delete(s.liveEndpoints, name)
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)

	case http.MethodGet:
		s.mu.RLock()
		out := make(map[string]string, len(s.liveEndpoints))
		for k, v := range s.liveEndpoints {
			out[k] = v
		}
		s.mu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(out) //nolint:errcheck

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// SetResponse configures the payload the mock returns when the named function is invoked.
func (s *Service) SetResponse(name string, payload json.RawMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.responses[name] = payload
}

// Invocations returns a snapshot of all recorded invocations.
func (s *Service) Invocations() []*InvocationRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*InvocationRecord, len(s.invocations))
	copy(out, s.invocations)
	return out
}

// ClearInvocations removes all recorded invocations.
func (s *Service) ClearInvocations() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invocations = nil
}

// InvocationsHandler serves GET /_nimbus/lambda/invocations and
// DELETE /_nimbus/lambda/invocations for test inspection.
func (s *Service) InvocationsHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		invocs := s.Invocations()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(invocs) //nolint:errcheck
	case http.MethodDelete:
		s.ClearInvocations()
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// DirectInvoke invokes a function synchronously without going through HTTP.
// If a live endpoint is registered for the function, it proxies there; otherwise
// it returns the configured mock response.
func (s *Service) DirectInvoke(functionName string, payload []byte) ([]byte, error) {
	if !s.checker.FunctionExists(functionName) {
		return nil, fmt.Errorf("function not found: %s", functionName)
	}

	s.mu.Lock()
	s.invocations = append(s.invocations, &InvocationRecord{
		FunctionName:   functionName,
		InvocationType: "RequestResponse",
		Payload:        payload,
		InvokedAt:      time.Now().UTC(),
	})
	response := s.responses[functionName]
	endpoint := s.liveEndpoints[functionName]
	s.mu.Unlock()

	if endpoint == "" {
		target, _, release, err := s.containerTarget(functionName)
		if err != nil {
			return nil, err
		}
		if release != nil {
			defer release()
		}
		endpoint = target
	}

	if endpoint != "" {
		return directProxyInvoke(endpoint, payload)
	}
	if response == nil {
		return json.RawMessage(`null`), nil
	}
	return response, nil
}

func directProxyInvoke(endpoint string, payload []byte) ([]byte, error) {
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("live endpoint unreachable: %v", err)
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}
