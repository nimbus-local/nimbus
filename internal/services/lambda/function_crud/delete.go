package function_crud

import (
	"fmt"
	"net/http"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

// DELETE /2015-03-31/functions/{FunctionName}[?Qualifier=]
func (s *Service) Delete(w http.ResponseWriter, r *http.Request, name string) {
	qualifier := r.URL.Query().Get("Qualifier")

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.functions[name]; !ok {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("Function not found: %s", name))
		return
	}

	// Deleting a specific published version — keyed as "name:version".
	if qualifier != "" && qualifier != "$LATEST" {
		delete(s.functions, name+":"+qualifier)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	delete(s.functions, name)
	w.WriteHeader(http.StatusNoContent)
}
