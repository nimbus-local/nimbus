package settings

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

func functionArn(name string) string {
	return fmt.Sprintf("arn:aws:lambda:us-east-1:000000000000:function:%s", name)
}

type putRuntimeConfigRequest struct {
	UpdateRuntimeOn   string `json:"UpdateRuntimeOn"`
	RuntimeVersionArn string `json:"RuntimeVersionArn,omitempty"`
}

func (r *putRuntimeConfigRequest) Validate() error {
	if r.UpdateRuntimeOn == "" {
		return errors.New("UpdateRuntimeOn is required")
	}
	if r.UpdateRuntimeOn != "Auto" && r.UpdateRuntimeOn != "FunctionUpdate" && r.UpdateRuntimeOn != "Manual" {
		return errors.New("UpdateRuntimeOn must be Auto, FunctionUpdate, or Manual")
	}
	return nil
}

// GetRuntimeConfig handles GET /2015-03-31/functions/{FunctionName}/runtime-management-config.
func (s *Service) GetRuntimeConfig(w http.ResponseWriter, r *http.Request, functionName string) {
	if !s.checker.FunctionExists(functionName) {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("Function not found: %s", functionName))
		return
	}

	s.mu.RLock()
	cfg, ok := s.runtimeConfigs[functionName]
	s.mu.RUnlock()

	if !ok {
		cfg = &RuntimeManagementConfig{
			FunctionArn:     functionArn(functionName),
			UpdateRuntimeOn: "Auto",
		}
	}

	jsonhttp.Write(w, http.StatusOK, cfg)
}

// PutRuntimeConfig handles PUT /2015-03-31/functions/{FunctionName}/runtime-management-config.
func (s *Service) PutRuntimeConfig(w http.ResponseWriter, r *http.Request, functionName string) {
	if !s.checker.FunctionExists(functionName) {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("Function not found: %s", functionName))
		return
	}

	req, ok := jsonhttp.Decode[putRuntimeConfigRequest](w, r)
	if !ok {
		return
	}

	cfg := &RuntimeManagementConfig{
		FunctionArn:       functionArn(functionName),
		UpdateRuntimeOn:   req.UpdateRuntimeOn,
		RuntimeVersionArn: req.RuntimeVersionArn,
	}

	s.mu.Lock()
	s.runtimeConfigs[functionName] = cfg
	s.mu.Unlock()

	jsonhttp.Write(w, http.StatusOK, cfg)
}
