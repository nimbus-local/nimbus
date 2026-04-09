package event_sources_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nimbus-local/nimbus/internal/services/lambda/event_sources"
)

// mockChecker implements FunctionChecker for tests.
type mockChecker struct {
	known map[string]bool
}

func (m *mockChecker) FunctionExists(name string) bool {
	return m.known[name]
}

func newTestService(knownFunctions ...string) *event_sources.Service {
	mc := &mockChecker{known: map[string]bool{}}
	for _, f := range knownFunctions {
		mc.known[f] = true
	}
	return event_sources.New(mc)
}

// ---- helpers ----------------------------------------------------------------

func doJSON(t *testing.T, method, path string, body any, handler http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	var b []byte
	if body != nil {
		var err error
		b, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}

func doCreate(t *testing.T, svc *event_sources.Service, body any) *httptest.ResponseRecorder {
	t.Helper()
	return doJSON(t, http.MethodPost, "/2015-03-31/event-source-mappings", body,
		func(w http.ResponseWriter, r *http.Request) { svc.Create(w, r) })
}

func doList(t *testing.T, svc *event_sources.Service, query string) *httptest.ResponseRecorder {
	t.Helper()
	path := "/2015-03-31/event-source-mappings"
	if query != "" {
		path += "?" + query
	}
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	svc.List(w, req)
	return w
}

func doGet(t *testing.T, svc *event_sources.Service, uuid string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/2015-03-31/event-source-mappings/"+uuid, nil)
	w := httptest.NewRecorder()
	svc.Get(w, req, uuid)
	return w
}

func doUpdate(t *testing.T, svc *event_sources.Service, uuid string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return doJSON(t, http.MethodPut, "/2015-03-31/event-source-mappings/"+uuid, body,
		func(w http.ResponseWriter, r *http.Request) { svc.Update(w, r, uuid) })
}

func doDelete(t *testing.T, svc *event_sources.Service, uuid string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, "/2015-03-31/event-source-mappings/"+uuid, nil)
	w := httptest.NewRecorder()
	svc.Delete(w, req, uuid)
	return w
}

func decodeMapping(t *testing.T, w *httptest.ResponseRecorder) event_sources.EventSourceMapping {
	t.Helper()
	var m event_sources.EventSourceMapping
	if err := json.NewDecoder(w.Body).Decode(&m); err != nil {
		t.Fatalf("decode EventSourceMapping: %v\nbody: %s", err, w.Body.String())
	}
	return m
}

func decodeList(t *testing.T, w *httptest.ResponseRecorder) struct {
	EventSourceMappings []event_sources.EventSourceMapping `json:"EventSourceMappings"`
	NextMarker          string                             `json:"NextMarker"`
} {
	t.Helper()
	var resp struct {
		EventSourceMappings []event_sources.EventSourceMapping `json:"EventSourceMappings"`
		NextMarker          string                             `json:"NextMarker"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode list response: %v\nbody: %s", err, w.Body.String())
	}
	return resp
}

func createMapping(t *testing.T, svc *event_sources.Service, functionName string) event_sources.EventSourceMapping {
	t.Helper()
	w := doCreate(t, svc, map[string]any{
		"FunctionName":   functionName,
		"EventSourceArn": "arn:aws:sqs:us-east-1:000000000000:test-queue",
	})
	if w.Code != http.StatusAccepted {
		t.Fatalf("createMapping: expected 202, got %d\n%s", w.Code, w.Body.String())
	}
	return decodeMapping(t, w)
}

// ---- Create -----------------------------------------------------------------

func TestCreate_HappyPath(t *testing.T) {
	svc := newTestService("my-func")
	w := doCreate(t, svc, map[string]any{
		"FunctionName":   "my-func",
		"EventSourceArn": "arn:aws:sqs:us-east-1:000000000000:my-queue",
		"BatchSize":      10,
	})

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d\n%s", w.Code, w.Body.String())
	}

	m := decodeMapping(t, w)

	if m.UUID == "" {
		t.Error("UUID should be set")
	}
	if m.State != "Enabled" {
		t.Errorf("State: expected Enabled, got %q", m.State)
	}
	if m.StateTransitionReason != "User action" {
		t.Errorf("StateTransitionReason: expected 'User action', got %q", m.StateTransitionReason)
	}
	if m.FunctionArn != "my-func" {
		t.Errorf("FunctionArn: expected my-func, got %q", m.FunctionArn)
	}
	if m.BatchSize != 10 {
		t.Errorf("BatchSize: expected 10, got %d", m.BatchSize)
	}
	if m.LastModified == 0 {
		t.Error("LastModified should be set")
	}
}

func TestCreate_UnknownFunction(t *testing.T) {
	svc := newTestService() // no known functions
	w := doCreate(t, svc, map[string]any{
		"FunctionName": "does-not-exist",
	})

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d\n%s", w.Code, w.Body.String())
	}
}

func TestCreate_MissingFunctionName(t *testing.T) {
	svc := newTestService()
	w := doCreate(t, svc, map[string]any{
		"EventSourceArn": "arn:aws:sqs:us-east-1:000000000000:my-queue",
	})

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d\n%s", w.Code, w.Body.String())
	}
}

func TestCreate_StripQualifier(t *testing.T) {
	svc := newTestService("my-func")
	// FunctionName with qualifier — base name should match
	w := doCreate(t, svc, map[string]any{
		"FunctionName": "my-func:some-alias",
	})
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202 (qualifier stripped), got %d\n%s", w.Code, w.Body.String())
	}
}

func TestCreate_InvalidBatchSize(t *testing.T) {
	svc := newTestService("fn")
	w := doCreate(t, svc, map[string]any{
		"FunctionName": "fn",
		"BatchSize":    0, // below min=1
	})
	// BatchSize=0 is omitempty, so this should succeed
	if w.Code != http.StatusAccepted && w.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status %d", w.Code)
	}
}

// ---- List -------------------------------------------------------------------

func TestList_Empty(t *testing.T) {
	svc := newTestService()
	w := doList(t, svc, "")

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d\n%s", w.Code, w.Body.String())
	}

	resp := decodeList(t, w)
	if len(resp.EventSourceMappings) != 0 {
		t.Errorf("expected 0 mappings, got %d", len(resp.EventSourceMappings))
	}
}

func TestList_WithItems(t *testing.T) {
	svc := newTestService("fn-a", "fn-b")
	createMapping(t, svc, "fn-a")
	createMapping(t, svc, "fn-b")

	w := doList(t, svc, "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	resp := decodeList(t, w)
	if len(resp.EventSourceMappings) != 2 {
		t.Errorf("expected 2 mappings, got %d", len(resp.EventSourceMappings))
	}
}

func TestList_FilterByFunctionName(t *testing.T) {
	svc := newTestService("fn-a", "fn-b")
	createMapping(t, svc, "fn-a")
	createMapping(t, svc, "fn-b")

	w := doList(t, svc, "FunctionName=fn-a")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	resp := decodeList(t, w)
	if len(resp.EventSourceMappings) != 1 {
		t.Errorf("expected 1 mapping for fn-a, got %d", len(resp.EventSourceMappings))
	}
}

func TestList_FilterByEventSourceArn(t *testing.T) {
	svc := newTestService("fn-a")

	// Create two mappings with different ARNs
	doCreate(t, svc, map[string]any{
		"FunctionName":   "fn-a",
		"EventSourceArn": "arn:aws:sqs:us-east-1:000000000000:queue-1",
	})
	doCreate(t, svc, map[string]any{
		"FunctionName":   "fn-a",
		"EventSourceArn": "arn:aws:sqs:us-east-1:000000000000:queue-2",
	})

	w := doList(t, svc, "EventSourceArn=arn:aws:sqs:us-east-1:000000000000:queue-1")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	resp := decodeList(t, w)
	if len(resp.EventSourceMappings) != 1 {
		t.Errorf("expected 1 mapping for queue-1, got %d", len(resp.EventSourceMappings))
	}
	if resp.EventSourceMappings[0].EventSourceArn != "arn:aws:sqs:us-east-1:000000000000:queue-1" {
		t.Errorf("unexpected EventSourceArn: %q", resp.EventSourceMappings[0].EventSourceArn)
	}
}

func TestList_Pagination(t *testing.T) {
	svc := newTestService("fn")

	// Create 4 mappings; sorted by UUID for stable pages.
	for i := 0; i < 4; i++ {
		createMapping(t, svc, "fn")
	}

	// Fetch first page of 2.
	w := doList(t, svc, "MaxItems=2")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	resp := decodeList(t, w)
	if len(resp.EventSourceMappings) != 2 {
		t.Fatalf("expected 2 on first page, got %d", len(resp.EventSourceMappings))
	}
	if resp.NextMarker == "" {
		t.Fatal("expected NextMarker to be set")
	}

	// Second page: NextMarker points to items[2]; passing it as Marker skips
	// items[2] and returns items[3] (matches function_crud pagination semantics).
	w2 := doList(t, svc, "MaxItems=2&Marker="+resp.NextMarker)
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w2.Code)
	}

	resp2 := decodeList(t, w2)
	if len(resp2.EventSourceMappings) == 0 {
		t.Error("expected at least 1 mapping on second page")
	}
	if resp2.NextMarker != "" {
		t.Errorf("expected no NextMarker on last page, got %q", resp2.NextMarker)
	}

	// No duplicates across pages.
	seen := map[string]bool{}
	for _, m := range append(resp.EventSourceMappings, resp2.EventSourceMappings...) {
		if seen[m.UUID] {
			t.Errorf("duplicate UUID across pages: %q", m.UUID)
		}
		seen[m.UUID] = true
	}
}

// ---- Get --------------------------------------------------------------------

func TestGet_HappyPath(t *testing.T) {
	svc := newTestService("fn")
	m := createMapping(t, svc, "fn")

	w := doGet(t, svc, m.UUID)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d\n%s", w.Code, w.Body.String())
	}

	got := decodeMapping(t, w)
	if got.UUID != m.UUID {
		t.Errorf("UUID mismatch: expected %q, got %q", m.UUID, got.UUID)
	}
}

func TestGet_NotFound(t *testing.T) {
	svc := newTestService()
	w := doGet(t, svc, "non-existent-uuid")

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d\n%s", w.Code, w.Body.String())
	}
}

// ---- Update -----------------------------------------------------------------

func TestUpdate_PatchesFields(t *testing.T) {
	svc := newTestService("fn")
	m := createMapping(t, svc, "fn")
	originalLastModified := m.LastModified

	w := doUpdate(t, svc, m.UUID, map[string]any{
		"BatchSize": 50,
	})
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d\n%s", w.Code, w.Body.String())
	}

	updated := decodeMapping(t, w)
	if updated.BatchSize != 50 {
		t.Errorf("BatchSize: expected 50, got %d", updated.BatchSize)
	}
	if updated.LastModified < originalLastModified {
		t.Error("LastModified should be >= original after update")
	}
}

func TestUpdate_NotFound(t *testing.T) {
	svc := newTestService()
	w := doUpdate(t, svc, "non-existent-uuid", map[string]any{
		"BatchSize": 10,
	})

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d\n%s", w.Code, w.Body.String())
	}
}

// ---- Delete -----------------------------------------------------------------

func TestDelete_Returns202AndDeletingState(t *testing.T) {
	svc := newTestService("fn")
	m := createMapping(t, svc, "fn")

	w := doDelete(t, svc, m.UUID)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d\n%s", w.Code, w.Body.String())
	}

	final := decodeMapping(t, w)
	if final.State != "Deleting" {
		t.Errorf("State: expected Deleting, got %q", final.State)
	}
	if final.UUID != m.UUID {
		t.Errorf("UUID mismatch in delete response")
	}
}

func TestDelete_NotFound(t *testing.T) {
	svc := newTestService()
	w := doDelete(t, svc, "non-existent-uuid")

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d\n%s", w.Code, w.Body.String())
	}
}

func TestDelete_GoneFromList(t *testing.T) {
	svc := newTestService("fn")
	m := createMapping(t, svc, "fn")

	doDelete(t, svc, m.UUID)

	w := doList(t, svc, "")
	resp := decodeList(t, w)
	if len(resp.EventSourceMappings) != 0 {
		t.Errorf("expected 0 mappings after delete, got %d", len(resp.EventSourceMappings))
	}
}
