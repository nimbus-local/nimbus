package invocation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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
	endpoint := s.liveEndpoints[name]
	s.mu.Unlock()

	// If a live endpoint is registered, proxy the invocation to it.
	if endpoint != "" {
		s.proxyInvoke(w, r, endpoint, invocationType, payload)
		return
	}

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
		w.Write(response) //nolint:errcheck
	}
}

// proxyInvoke forwards an invocation to a live registered endpoint.
// The endpoint receives the raw payload as the POST body and its response is
// forwarded back verbatim, preserving X-Amz-Function-Error if set.
func (s *Service) proxyInvoke(w http.ResponseWriter, _ *http.Request, endpoint, invocationType string, payload json.RawMessage) {
	if invocationType == "DryRun" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if invocationType == "Event" {
		go func() {
			req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
			if err != nil {
				return
			}
			req.Header.Set("Content-Type", "application/json")
			http.DefaultClient.Do(req) //nolint:errcheck
		}()
		w.WriteHeader(http.StatusAccepted)
		return
	}

	// RequestResponse — synchronous proxy
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		jsonhttp.Error(w, http.StatusBadGateway, "ServiceException",
			fmt.Sprintf("failed to build proxy request: %v", err))
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		jsonhttp.Error(w, http.StatusBadGateway, "ServiceException",
			fmt.Sprintf("live endpoint unreachable: %v", err))
		return
	}
	defer resp.Body.Close()

	// Forward Lambda error header if the live handler set it.
	if errType := resp.Header.Get("X-Amz-Function-Error"); errType != "" {
		w.Header().Set("X-Amz-Function-Error", errType)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body) //nolint:errcheck
}
