package invocation

import (
	"encoding/json"
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
	mu          sync.RWMutex
	checker     FunctionChecker
	responses   map[string]json.RawMessage // configured mock response per function name
	invocations []*InvocationRecord
}

func New(checker FunctionChecker) *Service {
	return &Service{
		checker:   checker,
		responses: map[string]json.RawMessage{},
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
