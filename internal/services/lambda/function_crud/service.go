package function_crud

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/nimbus-local/nimbus/internal/uid"
)

// Service manages in-memory state for Lambda function CRUD operations.
type Service struct {
	mu             sync.RWMutex
	functions      map[string]*FunctionConfig // keyed by name, or "name:version" for published snapshots
	versionCounter map[string]int             // latest published version number per function
	region         string
	account        string
}

func New(region, account string) *Service {
	return &Service{
		functions:      map[string]*FunctionConfig{},
		versionCounter: map[string]int{},
		region:         region,
		account:        account,
	}
}

// Reset clears all function state.
func (s *Service) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.functions = map[string]*FunctionConfig{}
	s.versionCounter = map[string]int{}
}

// FunctionNames returns a sorted list of $LATEST function names.
func (s *Service) FunctionNames() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	names := make([]string, 0, len(s.functions))
	for name := range s.functions {
		if !strings.Contains(name, ":") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// FunctionExists reports whether a $LATEST function with the given name is registered.
// Other sub-packages use this to validate function references without importing FunctionConfig.
func (s *Service) FunctionExists(name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.functions[name]
	return ok
}

// Function returns a snapshot of a $LATEST function's configuration. Callers
// get a copy so they can read it without holding the service lock.
func (s *Service) Function(name string) (FunctionConfig, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	fn, ok := s.functions[name]
	if !ok {
		return FunctionConfig{}, false
	}
	return *fn, true
}

func (s *Service) arn(name string) string {
	return fmt.Sprintf("arn:aws:lambda:%s:%s:function:%s", s.region, s.account, name)
}

func newRevisionID() string {
	return uid.New()
}
