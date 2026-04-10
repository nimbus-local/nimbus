package event_sources

import (
	"net/http"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

// GET /2015-03-31/event-source-mappings/{UUID}
func (s *Service) Get(w http.ResponseWriter, r *http.Request, uuid string) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	m, ok := s.mappings[uuid]
	if !ok {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			"The resource you requested does not exist: "+uuid)
		return
	}

	jsonhttp.Write(w, http.StatusOK, m)
}
