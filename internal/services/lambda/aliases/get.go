package aliases

import (
	"fmt"
	"net/http"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

// GET /2015-03-31/functions/{FunctionName}/aliases/{Name}
func (s *Service) Get(w http.ResponseWriter, r *http.Request, functionName, aliasName string) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	alias, ok := s.aliases[functionName+":"+aliasName]
	if !ok {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("Function not found: %s", aliasName))
		return
	}

	jsonhttp.Write(w, http.StatusOK, alias)
}
