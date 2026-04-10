package concurrency

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

type putProvisionedRequest struct {
	ProvisionedConcurrentExecutions int `json:"ProvisionedConcurrentExecutions"`
}

func (r *putProvisionedRequest) Validate() error {
	if r.ProvisionedConcurrentExecutions < 1 {
		return errors.New("ProvisionedConcurrentExecutions must be >= 1")
	}
	return nil
}

// PutProvisioned implements PutProvisionedConcurrencyConfig.
// PUT /2015-03-31/functions/{FunctionName}/provisioned-concurrency?Qualifier=
func (s *Service) PutProvisioned(w http.ResponseWriter, r *http.Request, functionName string) {
	qualifier := r.URL.Query().Get("Qualifier")
	if qualifier == "" {
		jsonhttp.Error(w, http.StatusBadRequest, "InvalidParameterValueException", "Qualifier is required")
		return
	}

	if !s.checker.FunctionExists(functionName) {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("Function not found: %s", functionName))
		return
	}

	req, ok := jsonhttp.Decode[putProvisionedRequest](w, r)
	if !ok {
		return
	}

	cfg := &ProvisionedConcurrencyConfig{
		AllocatedProvisionedConcurrentExecutions: req.ProvisionedConcurrentExecutions,
		AvailableProvisionedConcurrentExecutions: req.ProvisionedConcurrentExecutions,
		FunctionArn:                              fmt.Sprintf("arn:aws:lambda:us-east-1:000000000000:function:%s:%s", functionName, qualifier),
		LastModified:                             time.Now().UTC().Format(time.RFC3339),
		RequestedProvisionedConcurrentExecutions: req.ProvisionedConcurrentExecutions,
		Status:                                   "READY",
	}

	s.mu.Lock()
	s.provisioned[functionName+":"+qualifier] = cfg
	s.mu.Unlock()

	jsonhttp.Write(w, http.StatusAccepted, cfg)
}

// GetProvisioned implements GetProvisionedConcurrencyConfig.
// GET /2015-03-31/functions/{FunctionName}/provisioned-concurrency?Qualifier=
func (s *Service) GetProvisioned(w http.ResponseWriter, r *http.Request, functionName string) {
	qualifier := r.URL.Query().Get("Qualifier")
	if qualifier == "" {
		jsonhttp.Error(w, http.StatusBadRequest, "InvalidParameterValueException", "Qualifier is required")
		return
	}

	s.mu.RLock()
	cfg, ok := s.provisioned[functionName+":"+qualifier]
	s.mu.RUnlock()

	if !ok {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("No provisioned concurrency config found for function: %s, qualifier: %s", functionName, qualifier))
		return
	}

	jsonhttp.Write(w, http.StatusOK, cfg)
}

// DeleteProvisioned implements DeleteProvisionedConcurrencyConfig.
// DELETE /2015-03-31/functions/{FunctionName}/provisioned-concurrency?Qualifier=
func (s *Service) DeleteProvisioned(w http.ResponseWriter, r *http.Request, functionName string) {
	qualifier := r.URL.Query().Get("Qualifier")
	if qualifier == "" {
		jsonhttp.Error(w, http.StatusBadRequest, "InvalidParameterValueException", "Qualifier is required")
		return
	}

	s.mu.Lock()
	_, ok := s.provisioned[functionName+":"+qualifier]
	if ok {
		delete(s.provisioned, functionName+":"+qualifier)
	}
	s.mu.Unlock()

	if !ok {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("No provisioned concurrency config found for function: %s, qualifier: %s", functionName, qualifier))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

type listProvisionedResponse struct {
	ProvisionedConcurrencyConfigs []*ProvisionedConcurrencyConfig `json:"ProvisionedConcurrencyConfigs"`
	NextMarker                    string                          `json:"NextMarker,omitempty"`
}

// ListProvisioned implements ListProvisionedConcurrencyConfigs.
// GET /2015-03-31/functions/{FunctionName}/provisioned-concurrency
func (s *Service) ListProvisioned(w http.ResponseWriter, r *http.Request, functionName string) {
	if !s.checker.FunctionExists(functionName) {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("Function not found: %s", functionName))
		return
	}

	q := r.URL.Query()
	marker := q.Get("Marker")
	maxItems := 50
	if raw := q.Get("MaxItems"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			maxItems = n
		}
	}

	prefix := functionName + ":"
	s.mu.RLock()
	var keys []string
	for k := range s.provisioned {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	s.mu.RUnlock()

	sort.Strings(keys)

	if marker != "" {
		for i, k := range keys {
			if k == marker {
				keys = keys[i+1:]
				break
			}
		}
	}

	var nextMarker string
	if len(keys) > maxItems {
		nextMarker = keys[maxItems-1]
		keys = keys[:maxItems]
	}

	s.mu.RLock()
	configs := make([]*ProvisionedConcurrencyConfig, 0, len(keys))
	for _, k := range keys {
		if cfg, ok := s.provisioned[k]; ok {
			configs = append(configs, cfg)
		}
	}
	s.mu.RUnlock()

	jsonhttp.Write(w, http.StatusOK, listProvisionedResponse{
		ProvisionedConcurrencyConfigs: configs,
		NextMarker:                    nextMarker,
	})
}
