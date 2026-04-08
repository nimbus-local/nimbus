package invocation_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nimbus-local/nimbus/internal/services/lambda/invocation"
)

// mockChecker implements FunctionChecker for tests.
type mockChecker struct{ names map[string]bool }

func (m *mockChecker) FunctionExists(name string) bool { return m.names[name] }

// newService creates a Service with the given function names pre-registered.
func newService(names ...string) *invocation.Service {
	mc := &mockChecker{names: map[string]bool{}}
	for _, n := range names {
		mc.names[n] = true
	}
	return invocation.New(mc)
}

// invokeRequest sends a POST to the Invoke handler and returns the recorder.
func invokeRequest(t *testing.T, svc *invocation.Service, name, body, invocationType, qualifier string) *httptest.ResponseRecorder {
	t.Helper()
	target := "/2015-03-31/functions/" + name + "/invocations"
	if qualifier != "" {
		target += "?Qualifier=" + qualifier
	}
	var bodyReader *strings.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	} else {
		bodyReader = strings.NewReader("")
	}
	req := httptest.NewRequest(http.MethodPost, target, bodyReader)
	if body != "" {
		req.ContentLength = int64(len(body))
	}
	if invocationType != "" {
		req.Header.Set("X-Amz-Invocation-Type", invocationType)
	}
	w := httptest.NewRecorder()
	svc.Invoke(w, req, name)
	return w
}

// invokeAsyncRequest sends a POST to the InvokeAsync handler and returns the recorder.
func invokeAsyncRequest(t *testing.T, svc *invocation.Service, name, body string) *httptest.ResponseRecorder {
	t.Helper()
	target := "/2015-03-31/functions/" + name + "/invoke-async/"
	var bodyReader *strings.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	} else {
		bodyReader = strings.NewReader("")
	}
	req := httptest.NewRequest(http.MethodPost, target, bodyReader)
	if body != "" {
		req.ContentLength = int64(len(body))
	}
	w := httptest.NewRecorder()
	svc.InvokeAsync(w, req, name)
	return w
}

// streamRequest sends a POST to the InvokeWithResponseStream handler and returns the recorder.
func streamRequest(t *testing.T, svc *invocation.Service, name, body, qualifier string) *httptest.ResponseRecorder {
	t.Helper()
	target := "/2015-03-31/functions/" + name + "/response-streaming-invocations"
	if qualifier != "" {
		target += "?Qualifier=" + qualifier
	}
	var bodyReader *strings.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	} else {
		bodyReader = strings.NewReader("")
	}
	req := httptest.NewRequest(http.MethodPost, target, bodyReader)
	if body != "" {
		req.ContentLength = int64(len(body))
	}
	w := httptest.NewRecorder()
	svc.InvokeWithResponseStream(w, req, name)
	return w
}

// --- Invoke: 404 when function does not exist ---

func TestInvoke_NotFound(t *testing.T) {
	svc := newService() // no functions registered
	w := invokeRequest(t, svc, "missing-fn", "", "", "")
	if w.Code != http.StatusNotFound {
		t.Errorf("Invoke missing function: expected 404, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "ResourceNotFoundException") {
		t.Errorf("expected ResourceNotFoundException in body: %s", w.Body.String())
	}
}

// --- Invoke: RequestResponse (default) ---

func TestInvoke_RequestResponse_DefaultType(t *testing.T) {
	svc := newService("my-fn")
	svc.SetResponse("my-fn", json.RawMessage(`{"status":"ok"}`))

	w := invokeRequest(t, svc, "my-fn", "", "", "")

	if w.Code != http.StatusOK {
		t.Errorf("RequestResponse: expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("RequestResponse: expected Content-Type application/json, got %q", ct)
	}
	if !strings.Contains(w.Body.String(), `"status":"ok"`) {
		t.Errorf("RequestResponse: expected configured payload in body, got: %s", w.Body.String())
	}
}

func TestInvoke_RequestResponse_ExplicitType(t *testing.T) {
	svc := newService("my-fn")
	svc.SetResponse("my-fn", json.RawMessage(`{"result":42}`))

	w := invokeRequest(t, svc, "my-fn", "", "RequestResponse", "")

	if w.Code != http.StatusOK {
		t.Errorf("RequestResponse explicit: expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"result":42`) {
		t.Errorf("RequestResponse explicit: expected payload in body, got: %s", w.Body.String())
	}
}

func TestInvoke_RequestResponse_NullWhenNoPayloadSet(t *testing.T) {
	svc := newService("my-fn")
	// no SetResponse call

	w := invokeRequest(t, svc, "my-fn", "", "", "")

	if w.Code != http.StatusOK {
		t.Errorf("RequestResponse no payload: expected 200, got %d", w.Code)
	}
	if strings.TrimSpace(w.Body.String()) != "null" {
		t.Errorf("RequestResponse no payload: expected body \"null\", got: %s", w.Body.String())
	}
}

// --- Invoke: records invocation correctly ---

func TestInvoke_RecordsInvocation_FunctionName(t *testing.T) {
	svc := newService("record-fn")
	invokeRequest(t, svc, "record-fn", "", "", "")

	recs := svc.Invocations()
	if len(recs) != 1 {
		t.Fatalf("expected 1 invocation record, got %d", len(recs))
	}
	if recs[0].FunctionName != "record-fn" {
		t.Errorf("expected FunctionName %q, got %q", "record-fn", recs[0].FunctionName)
	}
}

func TestInvoke_RecordsInvocation_DefaultInvocationType(t *testing.T) {
	svc := newService("record-fn")
	invokeRequest(t, svc, "record-fn", "", "", "")

	recs := svc.Invocations()
	if recs[0].InvocationType != "RequestResponse" {
		t.Errorf("expected InvocationType %q, got %q", "RequestResponse", recs[0].InvocationType)
	}
}

func TestInvoke_RecordsInvocation_Payload(t *testing.T) {
	svc := newService("record-fn")
	payload := `{"key":"value"}`
	invokeRequest(t, svc, "record-fn", payload, "", "")

	recs := svc.Invocations()
	if string(recs[0].Payload) != payload {
		t.Errorf("expected Payload %q, got %q", payload, string(recs[0].Payload))
	}
}

func TestInvoke_RecordsInvocation_Qualifier(t *testing.T) {
	svc := newService("record-fn")
	invokeRequest(t, svc, "record-fn", "", "", "v2")

	recs := svc.Invocations()
	if recs[0].Qualifier != "v2" {
		t.Errorf("expected Qualifier %q, got %q", "v2", recs[0].Qualifier)
	}
}

func TestInvoke_RecordsInvocation_QualifierEmpty_WhenNotProvided(t *testing.T) {
	svc := newService("record-fn")
	invokeRequest(t, svc, "record-fn", "", "", "")

	recs := svc.Invocations()
	if recs[0].Qualifier != "" {
		t.Errorf("expected empty Qualifier, got %q", recs[0].Qualifier)
	}
}

func TestInvoke_RecordsInvocation_InvokedAtSet(t *testing.T) {
	svc := newService("record-fn")
	invokeRequest(t, svc, "record-fn", "", "", "")

	recs := svc.Invocations()
	if recs[0].InvokedAt.IsZero() {
		t.Error("expected InvokedAt to be set, got zero time")
	}
}

// --- Invoke: Event type ---

func TestInvoke_Event_Returns202(t *testing.T) {
	svc := newService("event-fn")
	w := invokeRequest(t, svc, "event-fn", "", "Event", "")

	if w.Code != http.StatusAccepted {
		t.Errorf("Event type: expected 202, got %d", w.Code)
	}
}

func TestInvoke_Event_NoBody(t *testing.T) {
	svc := newService("event-fn")
	w := invokeRequest(t, svc, "event-fn", "", "Event", "")

	if w.Body.Len() != 0 {
		t.Errorf("Event type: expected empty body, got: %s", w.Body.String())
	}
}

func TestInvoke_Event_RecordsAsEvent(t *testing.T) {
	svc := newService("event-fn")
	invokeRequest(t, svc, "event-fn", "", "Event", "")

	recs := svc.Invocations()
	if len(recs) != 1 {
		t.Fatalf("expected 1 invocation record, got %d", len(recs))
	}
	if recs[0].InvocationType != "Event" {
		t.Errorf("expected InvocationType %q, got %q", "Event", recs[0].InvocationType)
	}
}

// --- Invoke: DryRun type ---

func TestInvoke_DryRun_Returns204(t *testing.T) {
	svc := newService("dry-fn")
	w := invokeRequest(t, svc, "dry-fn", "", "DryRun", "")

	if w.Code != http.StatusNoContent {
		t.Errorf("DryRun: expected 204, got %d", w.Code)
	}
}

func TestInvoke_DryRun_NoBody(t *testing.T) {
	svc := newService("dry-fn")
	w := invokeRequest(t, svc, "dry-fn", "", "DryRun", "")

	if w.Body.Len() != 0 {
		t.Errorf("DryRun: expected empty body, got: %s", w.Body.String())
	}
}

func TestInvoke_DryRun_StillRecordsInvocation(t *testing.T) {
	svc := newService("dry-fn")
	invokeRequest(t, svc, "dry-fn", "", "DryRun", "")

	recs := svc.Invocations()
	if len(recs) != 1 {
		t.Fatalf("DryRun: expected 1 invocation record, got %d", len(recs))
	}
	if recs[0].InvocationType != "DryRun" {
		t.Errorf("DryRun: expected InvocationType %q, got %q", "DryRun", recs[0].InvocationType)
	}
	if recs[0].FunctionName != "dry-fn" {
		t.Errorf("DryRun: expected FunctionName %q, got %q", "dry-fn", recs[0].FunctionName)
	}
}

// --- InvokeAsync ---

func TestInvokeAsync_NotFound(t *testing.T) {
	svc := newService()
	w := invokeAsyncRequest(t, svc, "ghost-fn", "")

	if w.Code != http.StatusNotFound {
		t.Errorf("InvokeAsync missing function: expected 404, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "ResourceNotFoundException") {
		t.Errorf("expected ResourceNotFoundException in body: %s", w.Body.String())
	}
}

func TestInvokeAsync_Returns202(t *testing.T) {
	svc := newService("async-fn")
	w := invokeAsyncRequest(t, svc, "async-fn", "")

	if w.Code != http.StatusAccepted {
		t.Errorf("InvokeAsync: expected 202, got %d", w.Code)
	}
}

func TestInvokeAsync_AlwaysReturns202_WithPayload(t *testing.T) {
	svc := newService("async-fn")
	w := invokeAsyncRequest(t, svc, "async-fn", `{"event":"triggered"}`)

	if w.Code != http.StatusAccepted {
		t.Errorf("InvokeAsync with payload: expected 202, got %d", w.Code)
	}
}

func TestInvokeAsync_RecordsAsEvent(t *testing.T) {
	svc := newService("async-fn")
	invokeAsyncRequest(t, svc, "async-fn", `{"key":"val"}`)

	recs := svc.Invocations()
	if len(recs) != 1 {
		t.Fatalf("InvokeAsync: expected 1 invocation record, got %d", len(recs))
	}
	if recs[0].InvocationType != "Event" {
		t.Errorf("InvokeAsync: expected InvocationType %q, got %q", "Event", recs[0].InvocationType)
	}
}

func TestInvokeAsync_RecordsFunctionName(t *testing.T) {
	svc := newService("async-fn")
	invokeAsyncRequest(t, svc, "async-fn", "")

	recs := svc.Invocations()
	if recs[0].FunctionName != "async-fn" {
		t.Errorf("InvokeAsync: expected FunctionName %q, got %q", "async-fn", recs[0].FunctionName)
	}
}

func TestInvokeAsync_RecordsPayload(t *testing.T) {
	svc := newService("async-fn")
	payload := `{"async":true}`
	invokeAsyncRequest(t, svc, "async-fn", payload)

	recs := svc.Invocations()
	if string(recs[0].Payload) != payload {
		t.Errorf("InvokeAsync: expected Payload %q, got %q", payload, string(recs[0].Payload))
	}
}

// --- InvokeWithResponseStream ---

func TestInvokeWithResponseStream_NotFound(t *testing.T) {
	svc := newService()
	w := streamRequest(t, svc, "ghost-fn", "", "")

	if w.Code != http.StatusNotFound {
		t.Errorf("InvokeWithResponseStream missing function: expected 404, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "ResourceNotFoundException") {
		t.Errorf("expected ResourceNotFoundException in body: %s", w.Body.String())
	}
}

func TestInvokeWithResponseStream_Returns200(t *testing.T) {
	svc := newService("stream-fn")
	w := streamRequest(t, svc, "stream-fn", "", "")

	if w.Code != http.StatusOK {
		t.Errorf("InvokeWithResponseStream: expected 200, got %d", w.Code)
	}
}

func TestInvokeWithResponseStream_ContentType(t *testing.T) {
	svc := newService("stream-fn")
	w := streamRequest(t, svc, "stream-fn", "", "")

	ct := w.Header().Get("Content-Type")
	if ct != "application/vnd.amazon.eventstream" {
		t.Errorf("InvokeWithResponseStream: expected Content-Type %q, got %q",
			"application/vnd.amazon.eventstream", ct)
	}
}

func TestInvokeWithResponseStream_ReturnsConfiguredPayload(t *testing.T) {
	svc := newService("stream-fn")
	svc.SetResponse("stream-fn", json.RawMessage(`{"stream":"data"}`))

	w := streamRequest(t, svc, "stream-fn", "", "")

	if !strings.Contains(w.Body.String(), `"stream":"data"`) {
		t.Errorf("InvokeWithResponseStream: expected configured payload, got: %s", w.Body.String())
	}
}

func TestInvokeWithResponseStream_NullWhenNoPayloadSet(t *testing.T) {
	svc := newService("stream-fn")
	// no SetResponse call

	w := streamRequest(t, svc, "stream-fn", "", "")

	if strings.TrimSpace(w.Body.String()) != "null" {
		t.Errorf("InvokeWithResponseStream no payload: expected body \"null\", got: %s", w.Body.String())
	}
}

func TestInvokeWithResponseStream_RecordsInvocation(t *testing.T) {
	svc := newService("stream-fn")
	streamRequest(t, svc, "stream-fn", `{"input":"x"}`, "")

	recs := svc.Invocations()
	if len(recs) != 1 {
		t.Fatalf("InvokeWithResponseStream: expected 1 invocation record, got %d", len(recs))
	}
	if recs[0].FunctionName != "stream-fn" {
		t.Errorf("InvokeWithResponseStream: expected FunctionName %q, got %q", "stream-fn", recs[0].FunctionName)
	}
	if recs[0].InvocationType != "RequestResponse" {
		t.Errorf("InvokeWithResponseStream: expected InvocationType %q, got %q",
			"RequestResponse", recs[0].InvocationType)
	}
}

func TestInvokeWithResponseStream_RecordsQualifier(t *testing.T) {
	svc := newService("stream-fn")
	streamRequest(t, svc, "stream-fn", "", "prod")

	recs := svc.Invocations()
	if recs[0].Qualifier != "prod" {
		t.Errorf("InvokeWithResponseStream: expected Qualifier %q, got %q", "prod", recs[0].Qualifier)
	}
}

// --- ClearInvocations ---

func TestClearInvocations(t *testing.T) {
	svc := newService("clear-fn")

	invokeRequest(t, svc, "clear-fn", "", "", "")
	invokeRequest(t, svc, "clear-fn", "", "", "")

	if len(svc.Invocations()) != 2 {
		t.Fatalf("expected 2 invocations before clear, got %d", len(svc.Invocations()))
	}

	svc.ClearInvocations()

	if len(svc.Invocations()) != 0 {
		t.Errorf("expected 0 invocations after ClearInvocations, got %d", len(svc.Invocations()))
	}
}

func TestClearInvocations_SubsequentInvocationsStillRecorded(t *testing.T) {
	svc := newService("clear-fn")

	invokeRequest(t, svc, "clear-fn", "", "", "")
	svc.ClearInvocations()
	invokeRequest(t, svc, "clear-fn", `{"after":"clear"}`, "", "")

	recs := svc.Invocations()
	if len(recs) != 1 {
		t.Fatalf("expected 1 invocation after clear+invoke, got %d", len(recs))
	}
	if string(recs[0].Payload) != `{"after":"clear"}` {
		t.Errorf("expected post-clear payload, got %q", string(recs[0].Payload))
	}
}

// --- Qualifier captured via Invoke ---

func TestInvoke_Qualifier_CapturedInRecord(t *testing.T) {
	svc := newService("qual-fn")
	invokeRequest(t, svc, "qual-fn", "", "RequestResponse", "myAlias")

	recs := svc.Invocations()
	if len(recs) != 1 {
		t.Fatalf("expected 1 invocation, got %d", len(recs))
	}
	if recs[0].Qualifier != "myAlias" {
		t.Errorf("expected Qualifier %q, got %q", "myAlias", recs[0].Qualifier)
	}
}

// --- Multiple invocations accumulate ---

func TestInvocations_Accumulate(t *testing.T) {
	svc := newService("acc-fn")

	invokeRequest(t, svc, "acc-fn", `{"n":1}`, "", "")
	invokeRequest(t, svc, "acc-fn", `{"n":2}`, "Event", "")
	invokeAsyncRequest(t, svc, "acc-fn", `{"n":3}`)

	recs := svc.Invocations()
	if len(recs) != 3 {
		t.Fatalf("expected 3 accumulated invocations, got %d", len(recs))
	}
	if recs[0].InvocationType != "RequestResponse" {
		t.Errorf("record 0: expected RequestResponse, got %q", recs[0].InvocationType)
	}
	if recs[1].InvocationType != "Event" {
		t.Errorf("record 1: expected Event, got %q", recs[1].InvocationType)
	}
	if recs[2].InvocationType != "Event" {
		t.Errorf("record 2: expected Event, got %q", recs[2].InvocationType)
	}
}
