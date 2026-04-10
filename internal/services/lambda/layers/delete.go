package layers

import (
	"fmt"
	"net/http"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

// DELETE /2015-03-31/layers/{LayerName}/versions/{VersionNumber}
func (s *Service) DeleteVersion(w http.ResponseWriter, r *http.Request, layerName string, version int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := versionKey(layerName, version)
	if _, ok := s.versions[key]; !ok {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("Layer version not found: %s version %d", layerName, version))
		return
	}

	delete(s.versions, key)
	w.WriteHeader(http.StatusNoContent)
}
