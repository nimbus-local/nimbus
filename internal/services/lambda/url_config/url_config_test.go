package url_config_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nimbus-local/nimbus/internal/services/lambda/url_config"
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

func newSvc(names ...string) *url_config.Service {
	return url_config.New("us-east-1", "000000000000", newChecker(names...))
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

// ---- CreateUrl ---------------------------------------------------------------

func TestCreateUrl_HappyPath(t *testing.T) {
	svc := newSvc("myFunc")
	w := doJSON(t, http.MethodPost, "/2015-03-31/functions/myFunc/url",
		map[string]any{"AuthType": "NONE"},
		func(w http.ResponseWriter, r *http.Request) { svc.CreateUrl(w, r, "myFunc") })

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	m := decodeMap(t, w)
	if m["FunctionUrl"] == "" || m["FunctionUrl"] == nil {
		t.Error("expected FunctionUrl to be set")
	}
	if m["AuthType"] != "NONE" {
		t.Errorf("expected AuthType=NONE, got %v", m["AuthType"])
	}
}

func TestCreateUrl_UnknownFunction(t *testing.T) {
	svc := newSvc()
	w := doJSON(t, http.MethodPost, "/2015-03-31/functions/missing/url",
		map[string]any{"AuthType": "NONE"},
		func(w http.ResponseWriter, r *http.Request) { svc.CreateUrl(w, r, "missing") })

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestCreateUrl_Duplicate(t *testing.T) {
	svc := newSvc("myFunc")
	body := map[string]any{"AuthType": "NONE"}
	doJSON(t, http.MethodPost, "/2015-03-31/functions/myFunc/url", body,
		func(w http.ResponseWriter, r *http.Request) { svc.CreateUrl(w, r, "myFunc") })

	w := doJSON(t, http.MethodPost, "/2015-03-31/functions/myFunc/url", body,
		func(w http.ResponseWriter, r *http.Request) { svc.CreateUrl(w, r, "myFunc") })

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", w.Code)
	}
}

func TestCreateUrl_InvalidAuthType(t *testing.T) {
	svc := newSvc("myFunc")
	w := doJSON(t, http.MethodPost, "/2015-03-31/functions/myFunc/url",
		map[string]any{"AuthType": "INVALID"},
		func(w http.ResponseWriter, r *http.Request) { svc.CreateUrl(w, r, "myFunc") })

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestCreateUrl_MissingAuthType(t *testing.T) {
	svc := newSvc("myFunc")
	w := doJSON(t, http.MethodPost, "/2015-03-31/functions/myFunc/url",
		map[string]any{},
		func(w http.ResponseWriter, r *http.Request) { svc.CreateUrl(w, r, "myFunc") })

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// ---- GetUrl ------------------------------------------------------------------

func TestGetUrl_HappyPath(t *testing.T) {
	svc := newSvc("myFunc")
	doJSON(t, http.MethodPost, "/2015-03-31/functions/myFunc/url",
		map[string]any{"AuthType": "AWS_IAM"},
		func(w http.ResponseWriter, r *http.Request) { svc.CreateUrl(w, r, "myFunc") })

	req := httptest.NewRequest(http.MethodGet, "/2015-03-31/functions/myFunc/url", nil)
	w := httptest.NewRecorder()
	svc.GetUrl(w, req, "myFunc")

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	m := decodeMap(t, w)
	if m["AuthType"] != "AWS_IAM" {
		t.Errorf("expected AuthType=AWS_IAM, got %v", m["AuthType"])
	}
}

func TestGetUrl_NotFound(t *testing.T) {
	svc := newSvc("myFunc")
	req := httptest.NewRequest(http.MethodGet, "/2015-03-31/functions/myFunc/url", nil)
	w := httptest.NewRecorder()
	svc.GetUrl(w, req, "myFunc")

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// ---- UpdateUrl ---------------------------------------------------------------

func TestUpdateUrl_PatchesCors(t *testing.T) {
	svc := newSvc("myFunc")
	doJSON(t, http.MethodPost, "/2015-03-31/functions/myFunc/url",
		map[string]any{"AuthType": "NONE"},
		func(w http.ResponseWriter, r *http.Request) { svc.CreateUrl(w, r, "myFunc") })

	w := doJSON(t, http.MethodPut, "/2015-03-31/functions/myFunc/url",
		map[string]any{"Cors": map[string]any{"AllowOrigins": []string{"https://example.com"}}},
		func(w http.ResponseWriter, r *http.Request) { svc.UpdateUrl(w, r, "myFunc") })

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	m := decodeMap(t, w)
	cors, _ := m["Cors"].(map[string]any)
	origins, _ := cors["AllowOrigins"].([]any)
	if len(origins) == 0 || origins[0] != "https://example.com" {
		t.Errorf("Cors.AllowOrigins not patched correctly: %v", cors)
	}
}

func TestUpdateUrl_NotFound(t *testing.T) {
	svc := newSvc("myFunc")
	w := doJSON(t, http.MethodPut, "/2015-03-31/functions/myFunc/url",
		map[string]any{"AuthType": "NONE"},
		func(w http.ResponseWriter, r *http.Request) { svc.UpdateUrl(w, r, "myFunc") })

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// ---- DeleteUrl ---------------------------------------------------------------

func TestDeleteUrl_NoContent(t *testing.T) {
	svc := newSvc("myFunc")
	doJSON(t, http.MethodPost, "/2015-03-31/functions/myFunc/url",
		map[string]any{"AuthType": "NONE"},
		func(w http.ResponseWriter, r *http.Request) { svc.CreateUrl(w, r, "myFunc") })

	req := httptest.NewRequest(http.MethodDelete, "/2015-03-31/functions/myFunc/url", nil)
	w := httptest.NewRecorder()
	svc.DeleteUrl(w, req, "myFunc")

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}
}

func TestDeleteUrl_NotFound(t *testing.T) {
	svc := newSvc("myFunc")
	req := httptest.NewRequest(http.MethodDelete, "/2015-03-31/functions/myFunc/url", nil)
	w := httptest.NewRecorder()
	svc.DeleteUrl(w, req, "myFunc")

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestDeleteUrl_GoneAfterDelete(t *testing.T) {
	svc := newSvc("myFunc")
	doJSON(t, http.MethodPost, "/2015-03-31/functions/myFunc/url",
		map[string]any{"AuthType": "NONE"},
		func(w http.ResponseWriter, r *http.Request) { svc.CreateUrl(w, r, "myFunc") })

	req := httptest.NewRequest(http.MethodDelete, "/2015-03-31/functions/myFunc/url", nil)
	httptest.NewRecorder()
	w2 := httptest.NewRecorder()
	svc.DeleteUrl(w2, req, "myFunc")

	req2 := httptest.NewRequest(http.MethodGet, "/2015-03-31/functions/myFunc/url", nil)
	w3 := httptest.NewRecorder()
	svc.GetUrl(w3, req2, "myFunc")
	if w3.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", w3.Code)
	}
}

// ---- ListUrls ----------------------------------------------------------------

func TestListUrls_Empty(t *testing.T) {
	svc := newSvc("myFunc")
	req := httptest.NewRequest(http.MethodGet, "/2015-03-31/functions/myFunc/urls", nil)
	w := httptest.NewRecorder()
	svc.ListUrls(w, req, "myFunc")

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	m := decodeMap(t, w)
	items := m["FunctionUrlConfigs"].([]any)
	if len(items) != 0 {
		t.Errorf("expected empty list, got %d items", len(items))
	}
}

func TestListUrls_MultipleQualifiers(t *testing.T) {
	svc := newSvc("myFunc")
	for _, q := range []string{"", "v1", "v2"} {
		path := "/2015-03-31/functions/myFunc/url"
		if q != "" {
			path += "?Qualifier=" + q
		}
		doJSON(t, http.MethodPost, path,
			map[string]any{"AuthType": "NONE"},
			func(w http.ResponseWriter, r *http.Request) { svc.CreateUrl(w, r, "myFunc") })
	}

	req := httptest.NewRequest(http.MethodGet, "/2015-03-31/functions/myFunc/urls", nil)
	w := httptest.NewRecorder()
	svc.ListUrls(w, req, "myFunc")

	m := decodeMap(t, w)
	items := m["FunctionUrlConfigs"].([]any)
	if len(items) != 3 {
		t.Errorf("expected 3 items, got %d", len(items))
	}
}

func TestListUrls_Pagination(t *testing.T) {
	svc := newSvc("myFunc")
	qualifiers := []string{"q1", "q2", "q3"}
	for _, q := range qualifiers {
		path := fmt.Sprintf("/2015-03-31/functions/myFunc/url?Qualifier=%s", q)
		doJSON(t, http.MethodPost, path,
			map[string]any{"AuthType": "NONE"},
			func(w http.ResponseWriter, r *http.Request) { svc.CreateUrl(w, r, "myFunc") })
	}

	req := httptest.NewRequest(http.MethodGet, "/2015-03-31/functions/myFunc/urls?MaxItems=2", nil)
	w := httptest.NewRecorder()
	svc.ListUrls(w, req, "myFunc")

	m := decodeMap(t, w)
	items := m["FunctionUrlConfigs"].([]any)
	if len(items) != 2 {
		t.Errorf("expected 2 items on first page, got %d", len(items))
	}
	if m["NextMarker"] == nil || m["NextMarker"] == "" {
		t.Error("expected NextMarker to be set")
	}
}

func TestListUrls_UnknownFunction(t *testing.T) {
	svc := newSvc()
	req := httptest.NewRequest(http.MethodGet, "/2015-03-31/functions/missing/urls", nil)
	w := httptest.NewRecorder()
	svc.ListUrls(w, req, "missing")

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// ---- PutInvokeConfig ---------------------------------------------------------

func TestPutInvokeConfig_Creates(t *testing.T) {
	svc := newSvc("myFunc")
	w := doJSON(t, http.MethodPut, "/2015-03-31/functions/myFunc/event-invoke-config",
		map[string]any{"MaximumRetryAttempts": 1},
		func(w http.ResponseWriter, r *http.Request) { svc.PutInvokeConfig(w, r, "myFunc") })

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPutInvokeConfig_Replaces(t *testing.T) {
	svc := newSvc("myFunc")
	doJSON(t, http.MethodPut, "/2015-03-31/functions/myFunc/event-invoke-config",
		map[string]any{"MaximumRetryAttempts": 2, "MaximumEventAgeInSeconds": 60},
		func(w http.ResponseWriter, r *http.Request) { svc.PutInvokeConfig(w, r, "myFunc") })

	w := doJSON(t, http.MethodPut, "/2015-03-31/functions/myFunc/event-invoke-config",
		map[string]any{"MaximumRetryAttempts": 0},
		func(w http.ResponseWriter, r *http.Request) { svc.PutInvokeConfig(w, r, "myFunc") })

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	m := decodeMap(t, w)
	// Full replace: MaximumEventAgeInSeconds should be reset to 0 (omitted)
	if v, ok := m["MaximumEventAgeInSeconds"]; ok && v != nil && v != float64(0) {
		t.Errorf("expected MaximumEventAgeInSeconds=0 after replace, got %v", v)
	}
}

func TestPutInvokeConfig_NotFound(t *testing.T) {
	svc := newSvc()
	w := doJSON(t, http.MethodPut, "/2015-03-31/functions/missing/event-invoke-config",
		map[string]any{},
		func(w http.ResponseWriter, r *http.Request) { svc.PutInvokeConfig(w, r, "missing") })

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// ---- GetInvokeConfig ---------------------------------------------------------

func TestGetInvokeConfig_HappyPath(t *testing.T) {
	svc := newSvc("myFunc")
	doJSON(t, http.MethodPut, "/2015-03-31/functions/myFunc/event-invoke-config",
		map[string]any{"MaximumRetryAttempts": 1},
		func(w http.ResponseWriter, r *http.Request) { svc.PutInvokeConfig(w, r, "myFunc") })

	req := httptest.NewRequest(http.MethodGet, "/2015-03-31/functions/myFunc/event-invoke-config", nil)
	w := httptest.NewRecorder()
	svc.GetInvokeConfig(w, req, "myFunc")

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestGetInvokeConfig_NotFound(t *testing.T) {
	svc := newSvc("myFunc")
	req := httptest.NewRequest(http.MethodGet, "/2015-03-31/functions/myFunc/event-invoke-config", nil)
	w := httptest.NewRecorder()
	svc.GetInvokeConfig(w, req, "myFunc")

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// ---- UpdateInvokeConfig ------------------------------------------------------

func TestUpdateInvokeConfig_PatchesFields(t *testing.T) {
	svc := newSvc("myFunc")
	doJSON(t, http.MethodPut, "/2015-03-31/functions/myFunc/event-invoke-config",
		map[string]any{"MaximumRetryAttempts": 1, "MaximumEventAgeInSeconds": 60},
		func(w http.ResponseWriter, r *http.Request) { svc.PutInvokeConfig(w, r, "myFunc") })

	w := doJSON(t, http.MethodPost, "/2015-03-31/functions/myFunc/event-invoke-config",
		map[string]any{"MaximumRetryAttempts": 2},
		func(w http.ResponseWriter, r *http.Request) { svc.UpdateInvokeConfig(w, r, "myFunc") })

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	m := decodeMap(t, w)
	if m["MaximumRetryAttempts"] != float64(2) {
		t.Errorf("expected MaximumRetryAttempts=2, got %v", m["MaximumRetryAttempts"])
	}
	// MaximumEventAgeInSeconds should still be 60 (patch, not replace)
	if m["MaximumEventAgeInSeconds"] != float64(60) {
		t.Errorf("expected MaximumEventAgeInSeconds=60, got %v", m["MaximumEventAgeInSeconds"])
	}
}

func TestUpdateInvokeConfig_NotFound(t *testing.T) {
	svc := newSvc("myFunc")
	w := doJSON(t, http.MethodPost, "/2015-03-31/functions/myFunc/event-invoke-config",
		map[string]any{"MaximumRetryAttempts": 1},
		func(w http.ResponseWriter, r *http.Request) { svc.UpdateInvokeConfig(w, r, "myFunc") })

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// ---- DeleteInvokeConfig ------------------------------------------------------

func TestDeleteInvokeConfig_NoContent(t *testing.T) {
	svc := newSvc("myFunc")
	doJSON(t, http.MethodPut, "/2015-03-31/functions/myFunc/event-invoke-config",
		map[string]any{},
		func(w http.ResponseWriter, r *http.Request) { svc.PutInvokeConfig(w, r, "myFunc") })

	req := httptest.NewRequest(http.MethodDelete, "/2015-03-31/functions/myFunc/event-invoke-config", nil)
	w := httptest.NewRecorder()
	svc.DeleteInvokeConfig(w, req, "myFunc")

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}
}

func TestDeleteInvokeConfig_NotFound(t *testing.T) {
	svc := newSvc("myFunc")
	req := httptest.NewRequest(http.MethodDelete, "/2015-03-31/functions/myFunc/event-invoke-config", nil)
	w := httptest.NewRecorder()
	svc.DeleteInvokeConfig(w, req, "myFunc")

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// ---- ListInvokeConfigs -------------------------------------------------------

func TestListInvokeConfigs_Empty(t *testing.T) {
	svc := newSvc()
	req := httptest.NewRequest(http.MethodGet, "/2015-03-31/event-invoke-config/functions", nil)
	w := httptest.NewRecorder()
	svc.ListInvokeConfigs(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	m := decodeMap(t, w)
	items := m["FunctionEventInvokeConfigs"].([]any)
	if len(items) != 0 {
		t.Errorf("expected empty list, got %d items", len(items))
	}
}

func TestListInvokeConfigs_WithItems(t *testing.T) {
	svc := newSvc("fn1", "fn2")
	for _, fn := range []string{"fn1", "fn2"} {
		path := fmt.Sprintf("/2015-03-31/functions/%s/event-invoke-config", fn)
		fnCopy := fn
		doJSON(t, http.MethodPut, path,
			map[string]any{},
			func(w http.ResponseWriter, r *http.Request) { svc.PutInvokeConfig(w, r, fnCopy) })
	}

	req := httptest.NewRequest(http.MethodGet, "/2015-03-31/event-invoke-config/functions", nil)
	w := httptest.NewRecorder()
	svc.ListInvokeConfigs(w, req)

	m := decodeMap(t, w)
	items := m["FunctionEventInvokeConfigs"].([]any)
	if len(items) != 2 {
		t.Errorf("expected 2 items, got %d", len(items))
	}
}

func TestListInvokeConfigs_FunctionNameFilter(t *testing.T) {
	svc := newSvc("fn1", "fn2")
	for _, fn := range []string{"fn1", "fn2"} {
		path := fmt.Sprintf("/2015-03-31/functions/%s/event-invoke-config", fn)
		fnCopy := fn
		doJSON(t, http.MethodPut, path,
			map[string]any{},
			func(w http.ResponseWriter, r *http.Request) { svc.PutInvokeConfig(w, r, fnCopy) })
	}

	req := httptest.NewRequest(http.MethodGet, "/2015-03-31/event-invoke-config/functions?FunctionName=fn1", nil)
	w := httptest.NewRecorder()
	svc.ListInvokeConfigs(w, req)

	m := decodeMap(t, w)
	items := m["FunctionEventInvokeConfigs"].([]any)
	if len(items) != 1 {
		t.Errorf("expected 1 item after filter, got %d", len(items))
	}
}

func TestListInvokeConfigs_Pagination(t *testing.T) {
	svc := newSvc("fn1", "fn2", "fn3")
	for _, fn := range []string{"fn1", "fn2", "fn3"} {
		path := fmt.Sprintf("/2015-03-31/functions/%s/event-invoke-config", fn)
		fnCopy := fn
		doJSON(t, http.MethodPut, path,
			map[string]any{},
			func(w http.ResponseWriter, r *http.Request) { svc.PutInvokeConfig(w, r, fnCopy) })
	}

	req := httptest.NewRequest(http.MethodGet, "/2015-03-31/event-invoke-config/functions?MaxItems=2", nil)
	w := httptest.NewRecorder()
	svc.ListInvokeConfigs(w, req)

	m := decodeMap(t, w)
	items := m["FunctionEventInvokeConfigs"].([]any)
	if len(items) != 2 {
		t.Errorf("expected 2 items on first page, got %d", len(items))
	}
	if m["NextMarker"] == nil || m["NextMarker"] == "" {
		t.Error("expected NextMarker to be set")
	}
}
