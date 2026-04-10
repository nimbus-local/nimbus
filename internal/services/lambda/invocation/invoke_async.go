package invocation

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

// POST /2015-03-31/functions/{FunctionName}/invoke-async/
//
// Deprecated by AWS — preserved here for SDK backwards-compatibility.
// Always returns 202; the payload is recorded but no response is sent back.
func (s *Service) InvokeAsync(w http.ResponseWriter, r *http.Request, name string) {
	if !s.checker.FunctionExists(name) {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("Function not found: %s", name))
		return
	}

	var payload json.RawMessage
	if r.ContentLength != 0 {
		json.NewDecoder(r.Body).Decode(&payload) //nolint:errcheck
	}

	s.mu.Lock()
	s.invocations = append(s.invocations, &InvocationRecord{
		FunctionName:   name,
		Qualifier:      "",
		InvocationType: "Event",
		Payload:        payload,
		InvokedAt:      time.Now().UTC(),
	})
	s.mu.Unlock()

	w.WriteHeader(http.StatusAccepted)
}
