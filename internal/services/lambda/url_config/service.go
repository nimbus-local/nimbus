package url_config

import (
	"fmt"
	"sync"
)

// FunctionChecker lets url_config verify function existence without importing function_crud.
type FunctionChecker interface {
	FunctionExists(name string) bool
}

// Service manages in-memory URL configs and event-invoke configs for Lambda functions.
type Service struct {
	mu            sync.RWMutex
	urlConfigs    map[string]*FunctionUrlConfig // key = "functionName" or "functionName:qualifier"
	invokeConfigs map[string]*EventInvokeConfig // key = "functionName" or "functionName:qualifier"
	region        string
	account       string
	checker       FunctionChecker
}

func New(region, account string, checker FunctionChecker) *Service {
	return &Service{
		urlConfigs:    map[string]*FunctionUrlConfig{},
		invokeConfigs: map[string]*EventInvokeConfig{},
		region:        region,
		account:       account,
		checker:       checker,
	}
}

func (s *Service) arn(name, qualifier string) string {
	base := fmt.Sprintf("arn:aws:lambda:%s:%s:function:%s", s.region, s.account, name)
	if qualifier != "" {
		return base + ":" + qualifier
	}
	return base
}

func configKey(functionName, qualifier string) string {
	if qualifier != "" {
		return functionName + ":" + qualifier
	}
	return functionName
}
