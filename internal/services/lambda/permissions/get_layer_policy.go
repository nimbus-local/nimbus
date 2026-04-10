package permissions

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

// GET /2018-10-31/layers/{LayerName}/versions/{VersionNumber}/policy
func (s *Service) GetLayerVersionPolicy(w http.ResponseWriter, r *http.Request, layerName string, versionNumber int) {
	key := fmt.Sprintf("%s:%d", layerName, versionNumber)

	s.mu.RLock()
	defer s.mu.RUnlock()

	policy, ok := s.layerPolicies[key]
	if !ok || len(policy) == 0 {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("No policy found for layer version: %s:%d", layerName, versionNumber))
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
		"RevisionId": s.layerRevisions[key],
	})
}
