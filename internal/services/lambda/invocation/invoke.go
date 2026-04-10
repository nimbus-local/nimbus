package invocation

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

// POST /2015-03-31/functions/{FunctionName}/invocations
func (s *Service) Invoke(w http.ResponseWriter, r *http.Request, name string) {
	if !s.checker.FunctionExists(name) {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("Function not found: %s", name))
		return
	}

	invocationType := r.Header.Get("X-Amz-Invocation-Type")
	if invocationType == "" {
		invocationType = "RequestResponse"
	}
	qualifier := r.URL.Query().Get("Qualifier")

	var payload json.RawMessage
	if r.ContentLength != 0 {
		json.NewDecoder(r.Body).Decode(&payload) //nolint:errcheck — malformed payload is not fatal for a mock
	}

	s.mu.Lock()
	s.invocations = append(s.invocations, &InvocationRecord{
		FunctionName:   name,
		Qualifier:      qualifier,
		InvocationType: invocationType,
		Payload:        payload,
		InvokedAt:      time.Now().UTC(),
	})
	response := s.responses[name]
	s.mu.Unlock()

	switch invocationType {
	case "DryRun":
		w.WriteHeader(http.StatusNoContent)

	case "Event":
		// Async — acknowledge receipt only, no response body.
		w.WriteHeader(http.StatusAccepted)

	default: // RequestResponse
		if response == nil {
			response = json.RawMessage("null")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(response)
	}
}
