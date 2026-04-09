package code_signing

import (
	"fmt"
	"net/http"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

// DELETE /2015-03-31/code-signing-configs/{CodeSigningConfigArn}
func (s *Service) DeleteConfig(w http.ResponseWriter, r *http.Request, arn string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.configs[arn]; !ok {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("CodeSigningConfig not found: %s", arn))
		return
	}

	// Reject deletion if any function is still bound to this config.
	for fn, boundArn := range s.functionBinding {
		if boundArn == arn {
			jsonhttp.Error(w, http.StatusConflict, "ResourceConflictException",
				fmt.Sprintf("CodeSigningConfig %s is still associated with function %s", arn, fn))
			return
		}
	}

	delete(s.configs, arn)
	w.WriteHeader(http.StatusNoContent)
}
