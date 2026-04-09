package concurrency

import (
	"fmt"
	"net/http"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

type putConcurrencyRequest struct {
	ReservedConcurrentExecutions int `json:"ReservedConcurrentExecutions" validate:"min=0"`
}

// Put implements PutFunctionConcurrency.
// PUT /2015-03-31/functions/{FunctionName}/concurrency
func (s *Service) Put(w http.ResponseWriter, r *http.Request, functionName string) {
	if !s.checker.FunctionExists(functionName) {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("Function not found: %s", functionName))
		return
	}

	req, ok := jsonhttp.DecodeAndValidate[putConcurrencyRequest](w, r)
	if !ok {
		return
	}

	s.mu.Lock()
	s.reservedConcurrency[functionName] = req.ReservedConcurrentExecutions
	s.mu.Unlock()

	jsonhttp.Write(w, http.StatusOK, map[string]int{
		"ReservedConcurrentExecutions": req.ReservedConcurrentExecutions,
	})
}

// Get implements GetFunctionConcurrency.
// GET /2015-03-31/functions/{FunctionName}/concurrency
func (s *Service) Get(w http.ResponseWriter, r *http.Request, functionName string) {
	if !s.checker.FunctionExists(functionName) {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("Function not found: %s", functionName))
		return
	}

	s.mu.RLock()
	val, ok := s.reservedConcurrency[functionName]
	s.mu.RUnlock()

	if !ok {
		jsonhttp.Write(w, http.StatusOK, map[string]any{})
		return
	}

	jsonhttp.Write(w, http.StatusOK, map[string]int{
		"ReservedConcurrentExecutions": val,
	})
}

// Delete implements DeleteFunctionConcurrency.
// DELETE /2015-03-31/functions/{FunctionName}/concurrency
func (s *Service) Delete(w http.ResponseWriter, r *http.Request, functionName string) {
	if !s.checker.FunctionExists(functionName) {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("Function not found: %s", functionName))
		return
	}

	s.mu.Lock()
	delete(s.reservedConcurrency, functionName)
	s.mu.Unlock()

	w.WriteHeader(http.StatusNoContent)
}
