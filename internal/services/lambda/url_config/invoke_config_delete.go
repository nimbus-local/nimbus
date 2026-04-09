package url_config

import (
	"fmt"
	"net/http"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

// DELETE /2015-03-31/functions/{FunctionName}/event-invoke-config
func (s *Service) DeleteInvokeConfig(w http.ResponseWriter, r *http.Request, functionName string) {
	qualifier := r.URL.Query().Get("Qualifier")
	key := configKey(functionName, qualifier)

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.invokeConfigs[key]; !ok {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("Function event invoke config not found for function: %s", functionName))
		return
	}

	delete(s.invokeConfigs, key)
	w.WriteHeader(http.StatusNoContent)
}
