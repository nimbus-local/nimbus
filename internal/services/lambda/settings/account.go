package settings

import (
	"net/http"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

// GetAccountSettings handles GET /2015-03-31/account-settings.
// Returns fixed mock values — no state required.
func (s *Service) GetAccountSettings(w http.ResponseWriter, r *http.Request) {
	jsonhttp.Write(w, http.StatusOK, map[string]any{
		"AccountLimit": AccountLimit{
			CodeSizeUnzipped:               262144000,
			CodeSizeZipped:                 52428800,
			ConcurrentExecutions:           1000,
			TotalCodeSize:                  80530636800,
			UnreservedConcurrentExecutions: 1000,
		},
		"AccountUsage": AccountUsage{
			FunctionCount: 0,
			TotalCodeSize: 0,
		},
	})
}
