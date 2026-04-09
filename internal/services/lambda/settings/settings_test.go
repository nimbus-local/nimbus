package settings_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nimbus-local/nimbus/internal/services/lambda/settings"
)

// mockChecker implements FunctionChecker for tests.
type mockChecker struct {
	known map[string]bool
}

func (m *mockChecker) FunctionExists(name string) bool {
	return m.known[name]
}

func newService(names ...string) *settings.Service {
	mc := &mockChecker{known: map[string]bool{}}
	for _, n := range names {
		mc.known[n] = true
	}
	return settings.New(mc)
}

func doJSON(method, path string, body any, handler http.HandlerFunc) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}

// ---- GetAccountSettings -----------------------------------------------------

func TestGetAccountSettings_OK(t *testing.T) {
	svc := newService()
	req := httptest.NewRequest(http.MethodGet, "/2015-03-31/account-settings", nil)
	w := httptest.NewRecorder()
	svc.GetAccountSettings(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp struct {
		AccountLimit struct {
			ConcurrentExecutions int `json:"ConcurrentExecutions"`
		} `json:"AccountLimit"`
		AccountUsage struct {
			FunctionCount int64 `json:"FunctionCount"`
		} `json:"AccountUsage"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.AccountLimit.ConcurrentExecutions != 1000 {
		t.Errorf("ConcurrentExecutions: want 1000, got %d", resp.AccountLimit.ConcurrentExecutions)
	}
	if resp.AccountUsage.FunctionCount != 0 {
		t.Errorf("FunctionCount: want 0, got %d", resp.AccountUsage.FunctionCount)
	}
}

// ---- GetRuntimeConfig -------------------------------------------------------

func TestGetRuntimeConfig_DefaultAuto(t *testing.T) {
	svc := newService("myFunc")
	req := httptest.NewRequest(http.MethodGet, "/2015-03-31/functions/myFunc/runtime-management-config", nil)
	w := httptest.NewRecorder()
	svc.GetRuntimeConfig(w, req, "myFunc")

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp struct {
		UpdateRuntimeOn string `json:"UpdateRuntimeOn"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.UpdateRuntimeOn != "Auto" {
		t.Errorf("want Auto, got %s", resp.UpdateRuntimeOn)
	}
}

func TestGetRuntimeConfig_NotFound(t *testing.T) {
	svc := newService()
	req := httptest.NewRequest(http.MethodGet, "/2015-03-31/functions/missing/runtime-management-config", nil)
	w := httptest.NewRecorder()
	svc.GetRuntimeConfig(w, req, "missing")

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// ---- PutRuntimeConfig -------------------------------------------------------

func TestPutRuntimeConfig_StoreAndRetrieve(t *testing.T) {
	svc := newService("fn1")

	w := doJSON(http.MethodPut, "/2015-03-31/functions/fn1/runtime-management-config",
		map[string]string{"UpdateRuntimeOn": "FunctionUpdate"},
		func(w http.ResponseWriter, r *http.Request) { svc.PutRuntimeConfig(w, r, "fn1") })

	if w.Code != http.StatusOK {
		t.Fatalf("put: expected 200, got %d", w.Code)
	}

	// Retrieve and confirm
	req := httptest.NewRequest(http.MethodGet, "/2015-03-31/functions/fn1/runtime-management-config", nil)
	w2 := httptest.NewRecorder()
	svc.GetRuntimeConfig(w2, req, "fn1")

	var resp struct {
		UpdateRuntimeOn string `json:"UpdateRuntimeOn"`
	}
	json.NewDecoder(w2.Body).Decode(&resp)
	if resp.UpdateRuntimeOn != "FunctionUpdate" {
		t.Errorf("want FunctionUpdate, got %s", resp.UpdateRuntimeOn)
	}
}

func TestPutRuntimeConfig_InvalidEnum(t *testing.T) {
	svc := newService("fn1")
	w := doJSON(http.MethodPut, "/2015-03-31/functions/fn1/runtime-management-config",
		map[string]string{"UpdateRuntimeOn": "Bad"},
		func(w http.ResponseWriter, r *http.Request) { svc.PutRuntimeConfig(w, r, "fn1") })

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestPutRuntimeConfig_NotFound(t *testing.T) {
	svc := newService()
	w := doJSON(http.MethodPut, "/2015-03-31/functions/ghost/runtime-management-config",
		map[string]string{"UpdateRuntimeOn": "Auto"},
		func(w http.ResponseWriter, r *http.Request) { svc.PutRuntimeConfig(w, r, "ghost") })

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// ---- PutRecursionConfig -----------------------------------------------------

func TestPutRecursionConfig_StoreAndRetrieve(t *testing.T) {
	svc := newService("fn2")

	w := doJSON(http.MethodPut, "/2015-03-31/functions/fn2/recursion-config",
		map[string]string{"RecursiveLoop": "Allow"},
		func(w http.ResponseWriter, r *http.Request) { svc.PutRecursionConfig(w, r, "fn2") })

	if w.Code != http.StatusOK {
		t.Fatalf("put: expected 200, got %d", w.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/2015-03-31/functions/fn2/recursion-config", nil)
	w2 := httptest.NewRecorder()
	svc.GetRecursionConfig(w2, req, "fn2")

	var resp struct {
		RecursiveLoop string `json:"RecursiveLoop"`
		FunctionArn   string `json:"FunctionArn"`
	}
	json.NewDecoder(w2.Body).Decode(&resp)
	if resp.RecursiveLoop != "Allow" {
		t.Errorf("want Allow, got %s", resp.RecursiveLoop)
	}
	if resp.FunctionArn == "" {
		t.Error("FunctionArn should not be empty")
	}
}

func TestPutRecursionConfig_InvalidEnum(t *testing.T) {
	svc := newService("fn2")
	w := doJSON(http.MethodPut, "/2015-03-31/functions/fn2/recursion-config",
		map[string]string{"RecursiveLoop": "Maybe"},
		func(w http.ResponseWriter, r *http.Request) { svc.PutRecursionConfig(w, r, "fn2") })

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestPutRecursionConfig_NotFound(t *testing.T) {
	svc := newService()
	w := doJSON(http.MethodPut, "/2015-03-31/functions/ghost/recursion-config",
		map[string]string{"RecursiveLoop": "Allow"},
		func(w http.ResponseWriter, r *http.Request) { svc.PutRecursionConfig(w, r, "ghost") })

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// ---- GetRecursionConfig -----------------------------------------------------

func TestGetRecursionConfig_DefaultTerminate(t *testing.T) {
	svc := newService("fn3")
	req := httptest.NewRequest(http.MethodGet, "/2015-03-31/functions/fn3/recursion-config", nil)
	w := httptest.NewRecorder()
	svc.GetRecursionConfig(w, req, "fn3")

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp struct {
		RecursiveLoop string `json:"RecursiveLoop"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.RecursiveLoop != "Terminate" {
		t.Errorf("want Terminate, got %s", resp.RecursiveLoop)
	}
}

func TestGetRecursionConfig_NotFound(t *testing.T) {
	svc := newService()
	req := httptest.NewRequest(http.MethodGet, "/2015-03-31/functions/ghost/recursion-config", nil)
	w := httptest.NewRecorder()
	svc.GetRecursionConfig(w, req, "ghost")

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}
