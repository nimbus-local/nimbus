package code_signing

import (
	"fmt"
	"net/http"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

// GET /2015-03-31/code-signing-configs/{CodeSigningConfigArn}
func (s *Service) GetConfig(w http.ResponseWriter, r *http.Request, arn string) {
	s.mu.RLock()
	cfg, ok := s.configs[arn]
	s.mu.RUnlock()

	if !ok {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("CodeSigningConfig not found: %s", arn))
		return
	}

	jsonhttp.Write(w, http.StatusOK, map[string]any{"CodeSigningConfig": cfg})
}
