package invocation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
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

	// If a live endpoint is registered, proxy the invocation to it. An explicit
	// registration wins over a container: a developer pointing the function at
	// their own process is being deliberate.
	var timeout time.Duration
	var release func()
	if endpoint == "" {
		target, t, rel, err := s.containerTarget(name)
		if err != nil {
			jsonhttp.Error(w, http.StatusBadGateway, "ServiceException", err.Error())
			return
		}
		endpoint, timeout, release = target, t, rel
	}

	if endpoint != "" {
		s.proxyInvoke(w, endpoint, invocationType, payload, timeout, release)
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

// proxyInvoke forwards an invocation to a live endpoint — either one registered
// for local development or the runtime emulator inside a function's container.
// The endpoint receives the raw payload as the POST body and its response is
// forwarded back verbatim, preserving X-Amz-Function-Error if set.
//
// A non-zero timeout bounds the call the way Lambda's function timeout does.
//
// release, when non-nil, marks the backing container free. An async invocation
// holds it until its goroutine finishes, not until this function returns, so a
// long background call is not reaped out from under itself.
func (s *Service) proxyInvoke(w http.ResponseWriter, endpoint, invocationType string, payload json.RawMessage, timeout time.Duration, release func()) {
	if release == nil {
		release = func() {}
	}
	if invocationType == "DryRun" {
		release()
		w.WriteHeader(http.StatusNoContent)
		return
	}
	client := &http.Client{Timeout: timeout}

	if invocationType == "Event" {
		go func() {
			defer release()
			req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
			if err != nil {
				return
			}
			req.Header.Set("Content-Type", "application/json")
			client.Do(req) //nolint:errcheck
		}()
		w.WriteHeader(http.StatusAccepted)
		return
	}
	defer release()

	// RequestResponse — synchronous proxy
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		jsonhttp.Error(w, http.StatusBadGateway, "ServiceException",
			fmt.Sprintf("failed to build proxy request: %v", err))
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		// A timeout is a function failure, not a transport failure — report it
		// the way Lambda reports one so callers can tell the two apart.
		if os.IsTimeout(err) {
			w.Header().Set("X-Amz-Function-Error", "Unhandled")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, `{"errorType":"Function.Timeout","errorMessage":%q}`,
				fmt.Sprintf("Task timed out after %.2f seconds", timeout.Seconds()))
			return
		}
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
