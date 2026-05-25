package invocation

import (
	"encoding/json"
	"fmt"
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
}

func New(checker FunctionChecker) *Service {
	return &Service{
		checker:       checker,
		responses:     map[string]json.RawMessage{},
		liveEndpoints: map[string]string{},
	}
}

// LiveEndpoint returns the registered live endpoint for the given function, if any.
func (s *Service) LiveEndpoint(name string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.liveEndpoints[name]
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
// It records the invocation and returns the configured mock response.
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
	s.mu.Unlock()

	if response == nil {
		return json.RawMessage(`null`), nil
	}
	return response, nil
}
