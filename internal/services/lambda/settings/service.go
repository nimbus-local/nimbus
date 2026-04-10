package settings

import "sync"

// FunctionChecker validates that a named Lambda function exists.
type FunctionChecker interface {
	FunctionExists(name string) bool
}

// Service manages in-memory state for account, runtime and scaling settings.
type Service struct {
	mu               sync.RWMutex
	runtimeConfigs   map[string]*RuntimeManagementConfig // functionName → config
	recursionConfigs map[string]string                   // functionName → RecursiveLoop value
	checker          FunctionChecker
}

// New returns a ready-to-use Service.
func New(checker FunctionChecker) *Service {
	return &Service{
		runtimeConfigs:   map[string]*RuntimeManagementConfig{},
		recursionConfigs: map[string]string{},
		checker:          checker,
	}
}
