package permissions

import (
	"fmt"
	"net/http"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

// DELETE /2015-03-31/functions/{FunctionName}/policy/{StatementId}
func (s *Service) RemovePermission(w http.ResponseWriter, r *http.Request, functionName, statementId string) {
	revisionId := r.URL.Query().Get("RevisionId")

	if !s.checker.FunctionExists(functionName) {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("Function not found: %s", functionName))
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	policy, ok := s.functionPolicies[functionName]
	if !ok || policy[statementId] == nil {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("No statement found with provided id: %s", statementId))
		return
	}

	if revisionId != "" && s.policyRevisions[functionName] != revisionId {
		jsonhttp.Error(w, http.StatusPreconditionFailed, "PreconditionFailedException",
			"The RevisionId provided does not match the latest RevisionId for the Lambda function or alias.")
		return
	}

	delete(policy, statementId)
	s.policyRevisions[functionName] = newRevisionID()

	w.WriteHeader(http.StatusNoContent)
}
