package code_signing

import (
	"fmt"
	"sync"

	"github.com/nimbus-local/nimbus/internal/uid"
)

// FunctionChecker lets the code-signing service verify that a function exists
// without importing the function_crud package directly.
type FunctionChecker interface {
	FunctionExists(name string) bool
}

// Service manages in-memory state for Lambda code-signing operations.
type Service struct {
	mu              sync.RWMutex
	configs         map[string]*CodeSigningConfig // keyed by CodeSigningConfigArn
	functionBinding map[string]string             // functionName → CodeSigningConfigArn
	region          string
	account         string
	checker         FunctionChecker
}

func New(region, account string, checker FunctionChecker) *Service {
	return &Service{
		configs:         map[string]*CodeSigningConfig{},
		functionBinding: map[string]string{},
		region:          region,
		account:         account,
		checker:         checker,
	}
}

func (s *Service) newARN() (arn, id string) {
	hex8 := uid.New()[:8]
	id = "csc-" + hex8
	arn = fmt.Sprintf("arn:aws:lambda:%s:%s:code-signing-config:%s", s.region, s.account, id)
	return arn, id
}
