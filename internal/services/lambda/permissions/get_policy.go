package permissions

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

// GET /2015-03-31/functions/{FunctionName}/policy
func (s *Service) GetPolicy(w http.ResponseWriter, r *http.Request, functionName string) {
	if !s.checker.FunctionExists(functionName) {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("Function not found: %s", functionName))
		return
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	policy, ok := s.functionPolicies[functionName]
	if !ok || len(policy) == 0 {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("No policy is associated with the given resource: %s", functionName))
		return
	}

	stmts := make([]Statement, 0, len(policy))
	for _, stmt := range policy {
		stmts = append(stmts, *stmt)
	}

	doc := policyDocument{
		Version:   "2012-10-17",
		Statement: stmts,
	}

	docBytes, err := json.Marshal(doc)
	if err != nil {
		jsonhttp.Error(w, http.StatusInternalServerError, "ServiceException", "failed to marshal policy")
		return
	}

	jsonhttp.Write(w, http.StatusOK, map[string]string{
		"Policy":     string(docBytes),
		"RevisionId": s.policyRevisions[functionName],
	})
}
