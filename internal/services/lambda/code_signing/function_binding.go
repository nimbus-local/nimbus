package code_signing

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

type putFunctionConfigRequest struct {
	CodeSigningConfigArn string `json:"CodeSigningConfigArn"`
}

func (r *putFunctionConfigRequest) Validate() error {
	if r.CodeSigningConfigArn == "" {
		return errors.New("CodeSigningConfigArn is required")
	}
	return nil
}

type functionConfigResponse struct {
	CodeSigningConfigArn string `json:"CodeSigningConfigArn"`
	FunctionName         string `json:"FunctionName"`
}

// PUT /2015-03-31/functions/{FunctionName}/code-signing-config
func (s *Service) PutFunctionConfig(w http.ResponseWriter, r *http.Request, functionName string) {
	req, ok := jsonhttp.Decode[putFunctionConfigRequest](w, r)
	if !ok {
		return
	}

	if !s.checker.FunctionExists(functionName) {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("Function not found: %s", functionName))
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.configs[req.CodeSigningConfigArn]; !ok {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("CodeSigningConfig not found: %s", req.CodeSigningConfigArn))
		return
	}

	s.functionBinding[functionName] = req.CodeSigningConfigArn

	jsonhttp.Write(w, http.StatusOK, functionConfigResponse{
		CodeSigningConfigArn: req.CodeSigningConfigArn,
		FunctionName:         functionName,
	})
}

// GET /2015-03-31/functions/{FunctionName}/code-signing-config
func (s *Service) GetFunctionConfig(w http.ResponseWriter, r *http.Request, functionName string) {
	if !s.checker.FunctionExists(functionName) {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("Function not found: %s", functionName))
		return
	}

	s.mu.RLock()
	arn, ok := s.functionBinding[functionName]
	s.mu.RUnlock()

	if !ok {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("No code signing config found for function: %s", functionName))
		return
	}

	jsonhttp.Write(w, http.StatusOK, functionConfigResponse{
		CodeSigningConfigArn: arn,
		FunctionName:         functionName,
	})
}

// DELETE /2015-03-31/functions/{FunctionName}/code-signing-config
func (s *Service) DeleteFunctionConfig(w http.ResponseWriter, r *http.Request, functionName string) {
	if !s.checker.FunctionExists(functionName) {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("Function not found: %s", functionName))
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.functionBinding[functionName]; !ok {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("No code signing config found for function: %s", functionName))
		return
	}

	delete(s.functionBinding, functionName)
	w.WriteHeader(http.StatusNoContent)
}
