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
