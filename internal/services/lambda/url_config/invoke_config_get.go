package url_config

import (
	"fmt"
	"net/http"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

// GET /2015-03-31/functions/{FunctionName}/event-invoke-config
func (s *Service) GetInvokeConfig(w http.ResponseWriter, r *http.Request, functionName string) {
	qualifier := r.URL.Query().Get("Qualifier")
	key := configKey(functionName, qualifier)

	s.mu.RLock()
	defer s.mu.RUnlock()

	cfg, ok := s.invokeConfigs[key]
	if !ok {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("Function event invoke config not found for function: %s", functionName))
		return
	}

	jsonhttp.Write(w, http.StatusOK, cfg)
}
