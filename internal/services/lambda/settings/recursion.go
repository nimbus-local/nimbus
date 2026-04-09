package settings

import (
	"fmt"
	"net/http"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

type putRecursionConfigRequest struct {
	RecursiveLoop string `json:"RecursiveLoop" validate:"required,oneof=Allow Terminate"`
}

type recursionConfigResponse struct {
	RecursiveLoop string `json:"RecursiveLoop"`
	FunctionArn   string `json:"FunctionArn"`
}

// PutRecursionConfig handles PUT /2015-03-31/functions/{FunctionName}/recursion-config.
func (s *Service) PutRecursionConfig(w http.ResponseWriter, r *http.Request, functionName string) {
	if !s.checker.FunctionExists(functionName) {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("Function not found: %s", functionName))
		return
	}

	req, ok := jsonhttp.DecodeAndValidate[putRecursionConfigRequest](w, r)
	if !ok {
		return
	}

	s.mu.Lock()
	s.recursionConfigs[functionName] = req.RecursiveLoop
	s.mu.Unlock()

	jsonhttp.Write(w, http.StatusOK, recursionConfigResponse{
		RecursiveLoop: req.RecursiveLoop,
		FunctionArn:   functionArn(functionName),
	})
}

// GetRecursionConfig handles GET /2015-03-31/functions/{FunctionName}/recursion-config.
func (s *Service) GetRecursionConfig(w http.ResponseWriter, r *http.Request, functionName string) {
	if !s.checker.FunctionExists(functionName) {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("Function not found: %s", functionName))
		return
	}

	s.mu.RLock()
	loop, ok := s.recursionConfigs[functionName]
	s.mu.RUnlock()

	if !ok {
		loop = "Terminate"
	}

	jsonhttp.Write(w, http.StatusOK, recursionConfigResponse{
		RecursiveLoop: loop,
		FunctionArn:   functionArn(functionName),
	})
}
