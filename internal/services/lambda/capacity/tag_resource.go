package capacity

import (
	"fmt"
	"net/http"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

type tagResourceRequest struct {
	Tags map[string]string `json:"Tags"`
}

// POST /2015-03-31/tags/{ARN}
func (s *Service) TagResource(w http.ResponseWriter, r *http.Request, resourceArn string) {
	req, ok := jsonhttp.Decode[tagResourceRequest](w, r)
	if !ok {
		return
	}

	name := functionNameFromARN(resourceArn)

	s.mu.Lock()
	existing, exists := s.store.GetTags(name)
	if !exists {
		s.mu.Unlock()
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("Function not found: %s", name))
		return
	}

	merged := make(map[string]string, len(existing)+len(req.Tags))
	for k, v := range existing {
		merged[k] = v
	}
	for k, v := range req.Tags {
		merged[k] = v
	}
	s.store.SetTags(name, merged)
	s.mu.Unlock()

	w.WriteHeader(http.StatusNoContent)
}
