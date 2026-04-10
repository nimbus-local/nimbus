package permissions

import (
	"sync"

	"github.com/nimbus-local/nimbus/internal/uid"
)

// FunctionChecker allows the permissions service to verify that a function exists
// without importing the function_crud package directly.
type FunctionChecker interface {
	FunctionExists(name string) bool
}

// Service manages in-memory resource-based policies for Lambda functions and layers.
type Service struct {
	mu               sync.RWMutex
	functionPolicies map[string]map[string]*Statement // functionName → statementId → Statement
	policyRevisions  map[string]string                // functionName → RevisionId
	layerPolicies    map[string]map[string]*Statement // "layerName:version" → statementId → Statement
	layerRevisions   map[string]string                // "layerName:version" → RevisionId
	region           string
	account          string
	checker          FunctionChecker
}

func New(region, account string, checker FunctionChecker) *Service {
	return &Service{
		functionPolicies: map[string]map[string]*Statement{},
		policyRevisions:  map[string]string{},
		layerPolicies:    map[string]map[string]*Statement{},
		layerRevisions:   map[string]string{},
		region:           region,
		account:          account,
		checker:          checker,
	}
}

func newRevisionID() string {
	return uid.New()
}
