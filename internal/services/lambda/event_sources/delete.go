package event_sources

import (
	"net/http"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

// DELETE /2015-03-31/event-source-mappings/{UUID}
func (s *Service) Delete(w http.ResponseWriter, r *http.Request, uuid string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	m, ok := s.mappings[uuid]
	if !ok {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			"The resource you requested does not exist: "+uuid)
		return
	}

	delete(s.mappings, uuid)

	// Return final state with State="Deleting" — matches real AWS behaviour.
	final := *m
	final.State = "Deleting"
	jsonhttp.Write(w, http.StatusAccepted, &final)
}
