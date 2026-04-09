package function_crud

import (
	"fmt"
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

// FunctionExists reports whether a $LATEST function with the given name is registered.
// Other sub-packages use this to validate function references without importing FunctionConfig.
func (s *Service) FunctionExists(name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.functions[name]
	return ok
}

func (s *Service) arn(name string) string {
	return fmt.Sprintf("arn:aws:lambda:%s:%s:function:%s", s.region, s.account, name)
}

func newRevisionID() string {
	return uid.New()
}
