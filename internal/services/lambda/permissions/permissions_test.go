package permissions_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nimbus-local/nimbus/internal/services/lambda/permissions"
)

// mockChecker is a simple FunctionChecker for tests.
type mockChecker struct {
	names map[string]bool
}

func (m *mockChecker) FunctionExists(name string) bool {
	return m.names[name]
}

func newTestService(funcNames ...string) *permissions.Service {
	mc := &mockChecker{names: map[string]bool{}}
	for _, n := range funcNames {
		mc.names[n] = true
	}
	return permissions.New("us-east-1", "000000000000", mc)
}

// ---- helpers ----------------------------------------------------------------

func doJSON(t *testing.T, method, path string, body any, handler http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		buf = *bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}

func doJSONWithQuery(t *testing.T, method, path, query string, body any, handler http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	if query != "" {
		path += "?" + query
	}
	return doJSON(t, method, path, body, handler)
}

func decodeMap(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.NewDecoder(w.Body).Decode(&m); err != nil {
		t.Fatalf("decode response: %v\nbody: %s", err, w.Body.String())
	}
	return m
}

func mustAddPermission(t *testing.T, svc *permissions.Service, funcName, stmtID string) map[string]any {
	t.Helper()
	body := map[string]any{
		"Action":      "lambda:InvokeFunction",
		"Principal":   "events.amazonaws.com",
		"StatementId": stmtID,
	}
	w := doJSON(t, http.MethodPost, "/policy", body,
		func(rw http.ResponseWriter, r *http.Request) { svc.AddPermission(rw, r, funcName) })
	if w.Code != http.StatusCreated {
		t.Fatalf("AddPermission: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	return decodeMap(t, w)
}

// ---- AddPermission ----------------------------------------------------------

func TestAddPermission_HappyPath(t *testing.T) {
	svc := newTestService("my-func")
	resp := mustAddPermission(t, svc, "my-func", "stmt-1")

	revID, ok := resp["RevisionId"].(string)
	if !ok || revID == "" {
		t.Fatalf("expected non-empty RevisionId, got %v", resp["RevisionId"])
	}

	stmtStr, ok := resp["Statement"].(string)
	if !ok || stmtStr == "" {
		t.Fatalf("expected Statement as JSON string, got %v", resp["Statement"])
	}

	// Statement must be a valid JSON string (not a nested object)
	var stmt map[string]any
	if err := json.Unmarshal([]byte(stmtStr), &stmt); err != nil {
		t.Fatalf("Statement is not valid JSON: %v", err)
	}
	if stmt["Sid"] != "stmt-1" {
		t.Errorf("expected Sid=stmt-1, got %v", stmt["Sid"])
	}
}

func TestAddPermission_FunctionNotFound(t *testing.T) {
	svc := newTestService() // no functions registered
	body := map[string]any{
		"Action":      "lambda:InvokeFunction",
		"Principal":   "events.amazonaws.com",
		"StatementId": "stmt-1",
	}
	w := doJSON(t, http.MethodPost, "/policy", body,
		func(rw http.ResponseWriter, r *http.Request) { svc.AddPermission(rw, r, "ghost") })
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestAddPermission_DuplicateStatementId(t *testing.T) {
	svc := newTestService("my-func")
	mustAddPermission(t, svc, "my-func", "stmt-1")

	body := map[string]any{
		"Action":      "lambda:InvokeFunction",
		"Principal":   "events.amazonaws.com",
		"StatementId": "stmt-1",
	}
	w := doJSON(t, http.MethodPost, "/policy", body,
		func(rw http.ResponseWriter, r *http.Request) { svc.AddPermission(rw, r, "my-func") })
	if w.Code != http.StatusConflict {
		t.Errorf("expected 409, got %d", w.Code)
	}
	m := decodeMap(t, w)
	if m["__type"] != "ResourceConflictException" {
		t.Errorf("expected ResourceConflictException, got %v", m["__type"])
	}
}

func TestAddPermission_RevisionIdMismatch(t *testing.T) {
	svc := newTestService("my-func")
	body := map[string]any{
		"Action":      "lambda:InvokeFunction",
		"Principal":   "events.amazonaws.com",
		"StatementId": "stmt-1",
		"RevisionId":  "wrong-revision-id",
	}
	w := doJSON(t, http.MethodPost, "/policy", body,
		func(rw http.ResponseWriter, r *http.Request) { svc.AddPermission(rw, r, "my-func") })
	if w.Code != http.StatusPreconditionFailed {
		t.Errorf("expected 412, got %d", w.Code)
	}
}

func TestAddPermission_CorrectRevisionId(t *testing.T) {
	svc := newTestService("my-func")
	resp1 := mustAddPermission(t, svc, "my-func", "stmt-1")
	revID := resp1["RevisionId"].(string)

	// Second add with the correct RevisionId should succeed
	body := map[string]any{
		"Action":      "lambda:InvokeFunction",
		"Principal":   "s3.amazonaws.com",
		"StatementId": "stmt-2",
		"RevisionId":  revID,
	}
	w := doJSON(t, http.MethodPost, "/policy", body,
		func(rw http.ResponseWriter, r *http.Request) { svc.AddPermission(rw, r, "my-func") })
	if w.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
}

// ---- GetPolicy --------------------------------------------------------------

func TestGetPolicy_HappyPath(t *testing.T) {
	svc := newTestService("my-func")
	mustAddPermission(t, svc, "my-func", "stmt-1")
	mustAddPermission(t, svc, "my-func", "stmt-2")

	req := httptest.NewRequest(http.MethodGet, "/policy", nil)
	w := httptest.NewRecorder()
	svc.GetPolicy(w, req, "my-func")

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	resp := decodeMap(t, w)
	policyStr, ok := resp["Policy"].(string)
	if !ok || policyStr == "" {
		t.Fatalf("expected Policy as JSON string, got %v", resp["Policy"])
	}

	var doc map[string]any
	if err := json.Unmarshal([]byte(policyStr), &doc); err != nil {
		t.Fatalf("Policy is not valid JSON: %v", err)
	}
	if doc["Version"] != "2012-10-17" {
		t.Errorf("expected Version=2012-10-17, got %v", doc["Version"])
	}
	stmts, ok := doc["Statement"].([]any)
	if !ok || len(stmts) != 2 {
		t.Errorf("expected 2 statements, got %v", doc["Statement"])
	}
	if resp["RevisionId"] == nil || resp["RevisionId"] == "" {
		t.Error("expected non-empty RevisionId")
	}
}

func TestGetPolicy_FunctionNotFound(t *testing.T) {
	svc := newTestService()
	req := httptest.NewRequest(http.MethodGet, "/policy", nil)
	w := httptest.NewRecorder()
	svc.GetPolicy(w, req, "ghost")
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestGetPolicy_NoPolicyExists(t *testing.T) {
	svc := newTestService("empty-func")
	req := httptest.NewRequest(http.MethodGet, "/policy", nil)
	w := httptest.NewRecorder()
	svc.GetPolicy(w, req, "empty-func")
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// ---- RemovePermission -------------------------------------------------------

func TestRemovePermission_HappyPath(t *testing.T) {
	svc := newTestService("my-func")
	mustAddPermission(t, svc, "my-func", "stmt-1")

	req := httptest.NewRequest(http.MethodDelete, "/policy/stmt-1", nil)
	w := httptest.NewRecorder()
	svc.RemovePermission(w, req, "my-func", "stmt-1")
	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", w.Code)
	}

	// Policy should now be gone
	req2 := httptest.NewRequest(http.MethodGet, "/policy", nil)
	w2 := httptest.NewRecorder()
	svc.GetPolicy(w2, req2, "my-func")
	if w2.Code != http.StatusNotFound {
		t.Errorf("expected 404 after removal, got %d", w2.Code)
	}
}

func TestRemovePermission_FunctionNotFound(t *testing.T) {
	svc := newTestService()
	req := httptest.NewRequest(http.MethodDelete, "/policy/stmt-1", nil)
	w := httptest.NewRecorder()
	svc.RemovePermission(w, req, "ghost", "stmt-1")
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestRemovePermission_StatementNotFound(t *testing.T) {
	svc := newTestService("my-func")
	req := httptest.NewRequest(http.MethodDelete, "/policy/no-such-stmt", nil)
	w := httptest.NewRecorder()
	svc.RemovePermission(w, req, "my-func", "no-such-stmt")
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestRemovePermission_RevisionIdMismatch(t *testing.T) {
	svc := newTestService("my-func")
	mustAddPermission(t, svc, "my-func", "stmt-1")

	req := httptest.NewRequest(http.MethodDelete, "/policy/stmt-1?RevisionId=wrong", nil)
	w := httptest.NewRecorder()
	svc.RemovePermission(w, req, "my-func", "stmt-1")
	if w.Code != http.StatusPreconditionFailed {
		t.Errorf("expected 412, got %d", w.Code)
	}
}

// ---- Layer policy CRUD ------------------------------------------------------

func mustAddLayerPermission(t *testing.T, svc *permissions.Service, layerName string, version int, stmtID string) map[string]any {
	t.Helper()
	body := map[string]any{
		"Action":      "lambda:GetLayerVersion",
		"Principal":   "*",
		"StatementId": stmtID,
	}
	w := doJSON(t, http.MethodPost, "/layer-policy", body,
		func(rw http.ResponseWriter, r *http.Request) {
			svc.AddLayerVersionPermission(rw, r, layerName, version)
		})
	if w.Code != http.StatusCreated {
		t.Fatalf("AddLayerVersionPermission: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	return decodeMap(t, w)
}

func TestAddLayerVersionPermission_HappyPath(t *testing.T) {
	svc := newTestService()
	resp := mustAddLayerPermission(t, svc, "my-layer", 3, "org-stmt")

	revID, ok := resp["RevisionId"].(string)
	if !ok || revID == "" {
		t.Fatalf("expected non-empty RevisionId, got %v", resp["RevisionId"])
	}

	stmtStr, ok := resp["Statement"].(string)
	if !ok || stmtStr == "" {
		t.Fatalf("expected Statement as JSON string, got %v", resp["Statement"])
	}

	var stmt map[string]any
	if err := json.Unmarshal([]byte(stmtStr), &stmt); err != nil {
		t.Fatalf("Statement is not valid JSON: %v", err)
	}
	if stmt["Sid"] != "org-stmt" {
		t.Errorf("expected Sid=org-stmt, got %v", stmt["Sid"])
	}
}

func TestAddLayerVersionPermission_Duplicate(t *testing.T) {
	svc := newTestService()
	mustAddLayerPermission(t, svc, "my-layer", 1, "stmt-1")

	body := map[string]any{
		"Action":      "lambda:GetLayerVersion",
		"Principal":   "*",
		"StatementId": "stmt-1",
	}
	w := doJSON(t, http.MethodPost, "/layer-policy", body,
		func(rw http.ResponseWriter, r *http.Request) {
			svc.AddLayerVersionPermission(rw, r, "my-layer", 1)
		})
	if w.Code != http.StatusConflict {
		t.Errorf("expected 409, got %d", w.Code)
	}
}

func TestGetLayerVersionPolicy_HappyPath(t *testing.T) {
	svc := newTestService()
	mustAddLayerPermission(t, svc, "my-layer", 2, "stmt-a")

	req := httptest.NewRequest(http.MethodGet, "/layer-policy", nil)
	w := httptest.NewRecorder()
	svc.GetLayerVersionPolicy(w, req, "my-layer", 2)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	resp := decodeMap(t, w)
	policyStr, ok := resp["Policy"].(string)
	if !ok || policyStr == "" {
		t.Fatalf("expected Policy as JSON string, got %v", resp["Policy"])
	}

	var doc map[string]any
	if err := json.Unmarshal([]byte(policyStr), &doc); err != nil {
		t.Fatalf("Policy is not valid JSON: %v", err)
	}
	if doc["Version"] != "2012-10-17" {
		t.Errorf("expected Version=2012-10-17, got %v", doc["Version"])
	}
}

func TestGetLayerVersionPolicy_NotFound(t *testing.T) {
	svc := newTestService()
	req := httptest.NewRequest(http.MethodGet, "/layer-policy", nil)
	w := httptest.NewRecorder()
	svc.GetLayerVersionPolicy(w, req, "no-layer", 99)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestRemoveLayerVersionPermission_HappyPath(t *testing.T) {
	svc := newTestService()
	mustAddLayerPermission(t, svc, "my-layer", 5, "stmt-x")

	req := httptest.NewRequest(http.MethodDelete, "/layer-policy/stmt-x", nil)
	w := httptest.NewRecorder()
	svc.RemoveLayerVersionPermission(w, req, "my-layer", 5, "stmt-x")
	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", w.Code)
	}

	// Policy should now return 404
	req2 := httptest.NewRequest(http.MethodGet, "/layer-policy", nil)
	w2 := httptest.NewRecorder()
	svc.GetLayerVersionPolicy(w2, req2, "my-layer", 5)
	if w2.Code != http.StatusNotFound {
		t.Errorf("expected 404 after removal, got %d", w2.Code)
	}
}

func TestRemoveLayerVersionPermission_NotFound(t *testing.T) {
	svc := newTestService()
	req := httptest.NewRequest(http.MethodDelete, "/layer-policy/ghost", nil)
	w := httptest.NewRecorder()
	svc.RemoveLayerVersionPermission(w, req, "my-layer", 1, "ghost")
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestLayerPoliciesAreVersionIsolated(t *testing.T) {
	svc := newTestService()
	mustAddLayerPermission(t, svc, "my-layer", 1, "stmt-v1")

	// Version 2 should have no policy
	req := httptest.NewRequest(http.MethodGet, "/layer-policy", nil)
	w := httptest.NewRecorder()
	svc.GetLayerVersionPolicy(w, req, "my-layer", 2)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for different version, got %d", w.Code)
	}
}
