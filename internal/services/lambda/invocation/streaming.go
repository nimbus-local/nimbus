package invocation

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

// POST /2015-03-31/functions/{FunctionName}/response-streaming-invocations
//
// The real API streams chunks back using chunked transfer encoding.
// This mock returns the configured response as a single write — sufficient
// for SDKs that reassemble the stream before returning it to callers.
func (s *Service) InvokeWithResponseStream(w http.ResponseWriter, r *http.Request, name string) {
	if !s.checker.FunctionExists(name) {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("Function not found: %s", name))
		return
	}

	qualifier := r.URL.Query().Get("Qualifier")

	var payload json.RawMessage
	if r.ContentLength != 0 {
		json.NewDecoder(r.Body).Decode(&payload) //nolint:errcheck
	}

	s.mu.Lock()
	s.invocations = append(s.invocations, &InvocationRecord{
		FunctionName:   name,
		Qualifier:      qualifier,
		InvocationType: "RequestResponse",
		Payload:        payload,
		InvokedAt:      time.Now().UTC(),
	})
	response := s.responses[name]
	s.mu.Unlock()

	if response == nil {
		response = json.RawMessage("null")
	}

	w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
	w.WriteHeader(http.StatusOK)
	w.Write(response)
}
