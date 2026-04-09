package capacity

import (
	"fmt"
	"net/http"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

// DELETE /2015-03-31/tags/{ARN}?tagKeys=key1&tagKeys=key2
func (s *Service) UntagResource(w http.ResponseWriter, r *http.Request, resourceArn string) {
	tagKeys := r.URL.Query()["tagKeys"]
	name := functionNameFromARN(resourceArn)

	s.mu.Lock()
	existing, exists := s.store.GetTags(name)
	if !exists {
		s.mu.Unlock()
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("Function not found: %s", name))
		return
	}

	for _, k := range tagKeys {
		delete(existing, k)
	}
	s.store.SetTags(name, existing)
	s.mu.Unlock()

	w.WriteHeader(http.StatusNoContent)
}
