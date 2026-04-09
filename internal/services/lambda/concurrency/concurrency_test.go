package concurrency_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nimbus-local/nimbus/internal/services/lambda/concurrency"
)

// ---- mockChecker -------------------------------------------------------------

type mockChecker struct {
	known map[string]bool
}

func (m *mockChecker) FunctionExists(name string) bool {
	return m.known[name]
}

func newChecker(names ...string) *mockChecker {
	m := &mockChecker{known: map[string]bool{}}
	for _, n := range names {
		m.known[n] = true
	}
	return m
}

// ---- helpers -----------------------------------------------------------------

func newSvc(names ...string) *concurrency.Service {
	return concurrency.New(newChecker(names...))
}

func doJSON(t *testing.T, method, path string, body any, handler http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	var buf []byte
	if body != nil {
		var err error
		buf, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}

func decodeMap(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.NewDecoder(w.Body).Decode(&m); err != nil {
		t.Fatalf("decode body: %v\nbody: %s", err, w.Body.String())
	}
	return m
}

// ---- Put (PutFunctionConcurrency) -------------------------------------------

func TestPut_HappyPath(t *testing.T) {
	svc := newSvc("myFn")
	w := doJSON(t, http.MethodPut, "/2015-03-31/functions/myFn/concurrency",
		map[string]any{"ReservedConcurrentExecutions": 10},
		func(w http.ResponseWriter, r *http.Request) { svc.Put(w, r, "myFn") })

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	m := decodeMap(t, w)
	if got := int(m["ReservedConcurrentExecutions"].(float64)); got != 10 {
		t.Errorf("want 10, got %d", got)
	}
}

func TestPut_ZeroIsValid(t *testing.T) {
	svc := newSvc("myFn")
	w := doJSON(t, http.MethodPut, "/2015-03-31/functions/myFn/concurrency",
		map[string]any{"ReservedConcurrentExecutions": 0},
		func(w http.ResponseWriter, r *http.Request) { svc.Put(w, r, "myFn") })

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	m := decodeMap(t, w)
	if got := int(m["ReservedConcurrentExecutions"].(float64)); got != 0 {
		t.Errorf("want 0, got %d", got)
	}
}

func TestPut_UnknownFunction(t *testing.T) {
	svc := newSvc()
	w := doJSON(t, http.MethodPut, "/2015-03-31/functions/ghost/concurrency",
		map[string]any{"ReservedConcurrentExecutions": 5},
		func(w http.ResponseWriter, r *http.Request) { svc.Put(w, r, "ghost") })

	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

// ---- Get (GetFunctionConcurrency) -------------------------------------------

func TestGet_ReturnsSetValue(t *testing.T) {
	svc := newSvc("myFn")
	doJSON(t, http.MethodPut, "/2015-03-31/functions/myFn/concurrency",
		map[string]any{"ReservedConcurrentExecutions": 7},
		func(w http.ResponseWriter, r *http.Request) { svc.Put(w, r, "myFn") })

	req := httptest.NewRequest(http.MethodGet, "/2015-03-31/functions/myFn/concurrency", nil)
	w := httptest.NewRecorder()
	svc.Get(w, req, "myFn")

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	m := decodeMap(t, w)
	if got := int(m["ReservedConcurrentExecutions"].(float64)); got != 7 {
		t.Errorf("want 7, got %d", got)
	}
}

func TestGet_ReturnsEmptyWhenNotSet(t *testing.T) {
	svc := newSvc("myFn")
	req := httptest.NewRequest(http.MethodGet, "/2015-03-31/functions/myFn/concurrency", nil)
	w := httptest.NewRecorder()
	svc.Get(w, req, "myFn")

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	m := decodeMap(t, w)
	if _, ok := m["ReservedConcurrentExecutions"]; ok {
		t.Error("expected empty body but got ReservedConcurrentExecutions")
	}
}

func TestGet_UnknownFunction(t *testing.T) {
	svc := newSvc()
	req := httptest.NewRequest(http.MethodGet, "/2015-03-31/functions/ghost/concurrency", nil)
	w := httptest.NewRecorder()
	svc.Get(w, req, "ghost")

	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

// ---- Delete (DeleteFunctionConcurrency) -------------------------------------

func TestDelete_Returns204(t *testing.T) {
	svc := newSvc("myFn")
	doJSON(t, http.MethodPut, "/2015-03-31/functions/myFn/concurrency",
		map[string]any{"ReservedConcurrentExecutions": 5},
		func(w http.ResponseWriter, r *http.Request) { svc.Put(w, r, "myFn") })

	req := httptest.NewRequest(http.MethodDelete, "/2015-03-31/functions/myFn/concurrency", nil)
	w := httptest.NewRecorder()
	svc.Delete(w, req, "myFn")

	if w.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d", w.Code)
	}
}

func TestDelete_UnknownFunction(t *testing.T) {
	svc := newSvc()
	req := httptest.NewRequest(http.MethodDelete, "/2015-03-31/functions/ghost/concurrency", nil)
	w := httptest.NewRecorder()
	svc.Delete(w, req, "ghost")

	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

func TestDelete_SubsequentGetReturnsEmpty(t *testing.T) {
	svc := newSvc("myFn")
	doJSON(t, http.MethodPut, "/2015-03-31/functions/myFn/concurrency",
		map[string]any{"ReservedConcurrentExecutions": 5},
		func(w http.ResponseWriter, r *http.Request) { svc.Put(w, r, "myFn") })

	delReq := httptest.NewRequest(http.MethodDelete, "/2015-03-31/functions/myFn/concurrency", nil)
	svc.Delete(httptest.NewRecorder(), delReq, "myFn")

	getReq := httptest.NewRequest(http.MethodGet, "/2015-03-31/functions/myFn/concurrency", nil)
	w := httptest.NewRecorder()
	svc.Get(w, getReq, "myFn")

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	m := decodeMap(t, w)
	if _, ok := m["ReservedConcurrentExecutions"]; ok {
		t.Error("expected empty body after delete")
	}
}

// ---- PutProvisioned ---------------------------------------------------------

func TestPutProvisioned_HappyPath(t *testing.T) {
	svc := newSvc("myFn")
	w := doJSON(t, http.MethodPut, "/2015-03-31/functions/myFn/provisioned-concurrency?Qualifier=v1",
		map[string]any{"ProvisionedConcurrentExecutions": 3},
		func(w http.ResponseWriter, r *http.Request) { svc.PutProvisioned(w, r, "myFn") })

	if w.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d: %s", w.Code, w.Body.String())
	}
	m := decodeMap(t, w)
	if m["Status"] != "READY" {
		t.Errorf("want READY, got %v", m["Status"])
	}
	if int(m["AllocatedProvisionedConcurrentExecutions"].(float64)) != 3 {
		t.Errorf("want Allocated=3, got %v", m["AllocatedProvisionedConcurrentExecutions"])
	}
}

func TestPutProvisioned_MissingQualifier(t *testing.T) {
	svc := newSvc("myFn")
	w := doJSON(t, http.MethodPut, "/2015-03-31/functions/myFn/provisioned-concurrency",
		map[string]any{"ProvisionedConcurrentExecutions": 3},
		func(w http.ResponseWriter, r *http.Request) { svc.PutProvisioned(w, r, "myFn") })

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

func TestPutProvisioned_UnknownFunction(t *testing.T) {
	svc := newSvc()
	w := doJSON(t, http.MethodPut, "/2015-03-31/functions/ghost/provisioned-concurrency?Qualifier=v1",
		map[string]any{"ProvisionedConcurrentExecutions": 3},
		func(w http.ResponseWriter, r *http.Request) { svc.PutProvisioned(w, r, "ghost") })

	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

// ---- GetProvisioned ---------------------------------------------------------

func TestGetProvisioned_ReturnsConfig(t *testing.T) {
	svc := newSvc("myFn")
	doJSON(t, http.MethodPut, "/2015-03-31/functions/myFn/provisioned-concurrency?Qualifier=v1",
		map[string]any{"ProvisionedConcurrentExecutions": 5},
		func(w http.ResponseWriter, r *http.Request) { svc.PutProvisioned(w, r, "myFn") })

	req := httptest.NewRequest(http.MethodGet, "/2015-03-31/functions/myFn/provisioned-concurrency?Qualifier=v1", nil)
	w := httptest.NewRecorder()
	svc.GetProvisioned(w, req, "myFn")

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	m := decodeMap(t, w)
	if int(m["RequestedProvisionedConcurrentExecutions"].(float64)) != 5 {
		t.Errorf("want 5, got %v", m["RequestedProvisionedConcurrentExecutions"])
	}
}

func TestGetProvisioned_MissingQualifier(t *testing.T) {
	svc := newSvc("myFn")
	req := httptest.NewRequest(http.MethodGet, "/2015-03-31/functions/myFn/provisioned-concurrency", nil)
	w := httptest.NewRecorder()
	svc.GetProvisioned(w, req, "myFn")

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

func TestGetProvisioned_NotFound(t *testing.T) {
	svc := newSvc("myFn")
	req := httptest.NewRequest(http.MethodGet, "/2015-03-31/functions/myFn/provisioned-concurrency?Qualifier=v99", nil)
	w := httptest.NewRecorder()
	svc.GetProvisioned(w, req, "myFn")

	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

// ---- DeleteProvisioned ------------------------------------------------------

func TestDeleteProvisioned_Returns204(t *testing.T) {
	svc := newSvc("myFn")
	doJSON(t, http.MethodPut, "/2015-03-31/functions/myFn/provisioned-concurrency?Qualifier=v1",
		map[string]any{"ProvisionedConcurrentExecutions": 2},
		func(w http.ResponseWriter, r *http.Request) { svc.PutProvisioned(w, r, "myFn") })

	req := httptest.NewRequest(http.MethodDelete, "/2015-03-31/functions/myFn/provisioned-concurrency?Qualifier=v1", nil)
	w := httptest.NewRecorder()
	svc.DeleteProvisioned(w, req, "myFn")

	if w.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d", w.Code)
	}
}

func TestDeleteProvisioned_MissingQualifier(t *testing.T) {
	svc := newSvc("myFn")
	req := httptest.NewRequest(http.MethodDelete, "/2015-03-31/functions/myFn/provisioned-concurrency", nil)
	w := httptest.NewRecorder()
	svc.DeleteProvisioned(w, req, "myFn")

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

func TestDeleteProvisioned_NotFound(t *testing.T) {
	svc := newSvc("myFn")
	req := httptest.NewRequest(http.MethodDelete, "/2015-03-31/functions/myFn/provisioned-concurrency?Qualifier=v99", nil)
	w := httptest.NewRecorder()
	svc.DeleteProvisioned(w, req, "myFn")

	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

// ---- ListProvisioned --------------------------------------------------------

func TestListProvisioned_EmptyList(t *testing.T) {
	svc := newSvc("myFn")
	req := httptest.NewRequest(http.MethodGet, "/2015-03-31/functions/myFn/provisioned-concurrency", nil)
	w := httptest.NewRecorder()
	svc.ListProvisioned(w, req, "myFn")

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	m := decodeMap(t, w)
	items := m["ProvisionedConcurrencyConfigs"].([]any)
	if len(items) != 0 {
		t.Errorf("want empty list, got %d items", len(items))
	}
}

func TestListProvisioned_MultipleConfigs(t *testing.T) {
	svc := newSvc("myFn")
	for _, q := range []string{"v1", "v2", "v3"} {
		q := q
		doJSON(t, http.MethodPut, fmt.Sprintf("/2015-03-31/functions/myFn/provisioned-concurrency?Qualifier=%s", q),
			map[string]any{"ProvisionedConcurrentExecutions": 1},
			func(w http.ResponseWriter, r *http.Request) { svc.PutProvisioned(w, r, "myFn") })
	}

	req := httptest.NewRequest(http.MethodGet, "/2015-03-31/functions/myFn/provisioned-concurrency", nil)
	w := httptest.NewRecorder()
	svc.ListProvisioned(w, req, "myFn")

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	m := decodeMap(t, w)
	items := m["ProvisionedConcurrencyConfigs"].([]any)
	if len(items) != 3 {
		t.Errorf("want 3 items, got %d", len(items))
	}
}

func TestListProvisioned_UnknownFunction(t *testing.T) {
	svc := newSvc()
	req := httptest.NewRequest(http.MethodGet, "/2015-03-31/functions/ghost/provisioned-concurrency", nil)
	w := httptest.NewRecorder()
	svc.ListProvisioned(w, req, "ghost")

	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

func TestListProvisioned_Pagination(t *testing.T) {
	svc := newSvc("myFn")
	for i := 1; i <= 5; i++ {
		q := fmt.Sprintf("v%d", i)
		doJSON(t, http.MethodPut, fmt.Sprintf("/2015-03-31/functions/myFn/provisioned-concurrency?Qualifier=%s", q),
			map[string]any{"ProvisionedConcurrentExecutions": 1},
			func(w http.ResponseWriter, r *http.Request) { svc.PutProvisioned(w, r, "myFn") })
	}

	// First page: MaxItems=2
	req := httptest.NewRequest(http.MethodGet, "/2015-03-31/functions/myFn/provisioned-concurrency?MaxItems=2", nil)
	w := httptest.NewRecorder()
	svc.ListProvisioned(w, req, "myFn")

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	m := decodeMap(t, w)
	items := m["ProvisionedConcurrencyConfigs"].([]any)
	if len(items) != 2 {
		t.Fatalf("want 2 items on first page, got %d", len(items))
	}
	nextMarker, ok := m["NextMarker"].(string)
	if !ok || nextMarker == "" {
		t.Fatal("expected NextMarker to be set")
	}

	// Second page using Marker
	req2 := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/2015-03-31/functions/myFn/provisioned-concurrency?MaxItems=2&Marker=%s", nextMarker), nil)
	w2 := httptest.NewRecorder()
	svc.ListProvisioned(w2, req2, "myFn")

	if w2.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w2.Code)
	}
	m2 := decodeMap(t, w2)
	items2 := m2["ProvisionedConcurrencyConfigs"].([]any)
	if len(items2) == 0 {
		t.Error("expected items on second page")
	}
}
