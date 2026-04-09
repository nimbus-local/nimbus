package layers

import (
	"fmt"
	"net/http"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

// GET /2015-03-31/layers/{LayerName}/versions/{VersionNumber}
func (s *Service) GetVersion(w http.ResponseWriter, r *http.Request, layerName string, version int64) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	lv, ok := s.versions[versionKey(layerName, version)]
	if !ok {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("Layer version not found: %s version %d", layerName, version))
		return
	}

	jsonhttp.Write(w, http.StatusOK, lv)
}
