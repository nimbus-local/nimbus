package permissions

import (
	"fmt"
	"net/http"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

// DELETE /2018-10-31/layers/{LayerName}/versions/{VersionNumber}/policy/{StatementId}
func (s *Service) RemoveLayerVersionPermission(w http.ResponseWriter, r *http.Request, layerName string, versionNumber int, statementId string) {
	key := fmt.Sprintf("%s:%d", layerName, versionNumber)

	s.mu.Lock()
	defer s.mu.Unlock()

	policy, ok := s.layerPolicies[key]
	if !ok || policy[statementId] == nil {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("No statement found with provided id: %s", statementId))
		return
	}

	delete(policy, statementId)
	s.layerRevisions[key] = newRevisionID()

	w.WriteHeader(http.StatusNoContent)
}
