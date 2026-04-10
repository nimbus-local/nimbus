package aliases

import (
	"fmt"
	"net/http"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

// DELETE /2015-03-31/functions/{FunctionName}/aliases/{Name}
func (s *Service) Delete(w http.ResponseWriter, r *http.Request, functionName, aliasName string) {
	key := functionName + ":" + aliasName

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.aliases[key]; !ok {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("Function not found: %s", aliasName))
		return
	}

	delete(s.aliases, key)
	w.WriteHeader(http.StatusNoContent)
}
