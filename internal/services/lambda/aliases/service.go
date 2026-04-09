package aliases

import (
	"fmt"
	"sync"

	"github.com/nimbus-local/nimbus/internal/uid"
)

// FunctionChecker lets the aliases service verify a function exists
// without importing the function_crud package directly.
type FunctionChecker interface {
	FunctionExists(name string) bool
}

// Service manages in-memory state for Lambda alias operations.
type Service struct {
	mu      sync.RWMutex
	aliases map[string]*AliasConfig // keyed as "functionName:aliasName"
	region  string
	account string
	checker FunctionChecker
}

func New(region, account string, checker FunctionChecker) *Service {
	return &Service{
		aliases: map[string]*AliasConfig{},
		region:  region,
		account: account,
		checker: checker,
	}
}

func (s *Service) arn(functionName, aliasName string) string {
	return fmt.Sprintf("arn:aws:lambda:%s:%s:function:%s:%s", s.region, s.account, functionName, aliasName)
}

func newRevisionID() string {
	return uid.New()
}
