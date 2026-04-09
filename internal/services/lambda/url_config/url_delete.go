package url_config

import (
	"fmt"
	"net/http"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

// DELETE /2015-03-31/functions/{FunctionName}/url
func (s *Service) DeleteUrl(w http.ResponseWriter, r *http.Request, functionName string) {
	qualifier := r.URL.Query().Get("Qualifier")
	key := configKey(functionName, qualifier)

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.urlConfigs[key]; !ok {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("Function URL config not found for function: %s", functionName))
		return
	}

	delete(s.urlConfigs, key)
	w.WriteHeader(http.StatusNoContent)
}
