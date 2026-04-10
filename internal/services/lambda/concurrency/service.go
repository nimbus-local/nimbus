package concurrency

import "sync"

// FunctionChecker reports whether a given function name is registered.
type FunctionChecker interface {
	FunctionExists(name string) bool
}

// Service manages in-memory concurrency state for Lambda functions.
type Service struct {
	mu                  sync.RWMutex
	reservedConcurrency map[string]int                           // functionName → reserved concurrency
	provisioned         map[string]*ProvisionedConcurrencyConfig // key = "functionName:qualifier"
	checker             FunctionChecker
}

// New returns a new Service backed by the given FunctionChecker.
func New(checker FunctionChecker) *Service {
	return &Service{
		reservedConcurrency: map[string]int{},
		provisioned:         map[string]*ProvisionedConcurrencyConfig{},
		checker:             checker,
	}
}
