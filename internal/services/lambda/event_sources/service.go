package event_sources

import "sync"

// FunctionChecker reports whether a named function exists.
type FunctionChecker interface {
	FunctionExists(name string) bool
}

// Service manages in-memory state for Lambda event source mapping operations.
type Service struct {
	mu       sync.RWMutex
	mappings map[string]*EventSourceMapping // keyed by UUID
	checker  FunctionChecker
}

func New(checker FunctionChecker) *Service {
	return &Service{
		mappings: map[string]*EventSourceMapping{},
		checker:  checker,
	}
}

// Reset clears all event source mappings.
func (s *Service) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mappings = map[string]*EventSourceMapping{}
}

// ActiveCount returns the number of enabled event source mappings.
func (s *Service) ActiveCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for _, m := range s.mappings {
		if m.State == "Enabled" {
			n++
		}
	}
	return n
}
