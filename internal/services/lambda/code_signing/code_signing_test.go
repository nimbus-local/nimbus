package code_signing_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nimbus-local/nimbus/internal/services/lambda/code_signing"
)

// mockChecker implements FunctionChecker for tests.
type mockChecker struct {
	known map[string]bool
}

func (m *mockChecker) FunctionExists(name string) bool {
	return m.known[name]
}

func newService(fns ...string) *code_signing.Service {
	mc := &mockChecker{known: map[string]bool{}}
	for _, fn := range fns {
		mc.known[fn] = true
	}
	return code_signing.New("us-east-1", "123456789012", mc)
}

func body(v any) *bytes.Buffer {
	b, _ := json.Marshal(v)
	return bytes.NewBuffer(b)
}

func do(svc *code_signing.Service, method, path string, payload any, handler func(w http.ResponseWriter, r *http.Request)) *httptest.ResponseRecorder {
	var b *bytes.Buffer
	if payload != nil {
		b = body(payload)
	} else {
		b = &bytes.Buffer{}
	}
	req := httptest.NewRequest(method, path, b)
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}

// ---- CreateConfig ----

func TestCreateConfig_HappyPath(t *testing.T) {
	svc := newService()
	w := do(svc, http.MethodPost, "/2015-03-31/code-signing-configs", map[string]any{
		"AllowedPublishers": map[string]any{
			"SigningProfileVersionArns": []string{"arn:aws:signer:::signing-profiles/MyProfile/abcdef"},
		},
	}, svc.Create)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	cfg := resp["CodeSigningConfig"].(map[string]any)

	if cfg["CodeSigningConfigArn"] == "" {
		t.Error("expected ARN to be set")
	}
	if cfg["CodeSigningConfigId"] == "" {
		t.Error("expected CodeSigningConfigId to be set")
	}
	policies := cfg["CodeSigningPolicies"].(map[string]any)
	if policies["UntrustedArtifactOnDeployment"] != "Warn" {
		t.Errorf("expected default policy Warn, got %v", policies["UntrustedArtifactOnDeployment"])
	}
}

func TestCreateConfig_ExplicitPolicy(t *testing.T) {
	svc := newService()
	w := do(svc, http.MethodPost, "/2015-03-31/code-signing-configs", map[string]any{
		"AllowedPublishers": map[string]any{
			"SigningProfileVersionArns": []string{"arn:aws:signer:::signing-profiles/MyProfile/abcdef"},
		},
		"CodeSigningPolicies": map[string]any{
			"UntrustedArtifactOnDeployment": "Enforce",
		},
	}, svc.Create)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	cfg := resp["CodeSigningConfig"].(map[string]any)
	policies := cfg["CodeSigningPolicies"].(map[string]any)
	if policies["UntrustedArtifactOnDeployment"] != "Enforce" {
		t.Errorf("expected Enforce, got %v", policies["UntrustedArtifactOnDeployment"])
	}
}

// ---- GetConfig ----

func TestGetConfig_HappyPath(t *testing.T) {
	svc := newService()
	arn := createConfig(t, svc)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	svc.GetConfig(w, req, arn)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", w.Code)
	}
}

func TestGetConfig_NotFound(t *testing.T) {
	svc := newService()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	svc.GetConfig(w, req, "arn:aws:lambda:us-east-1:123456789012:code-signing-config:csc-notreal")

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 got %d", w.Code)
	}
}

// ---- UpdateConfig ----

func TestUpdateConfig_Patches(t *testing.T) {
	svc := newService()
	arn := createConfig(t, svc)

	desc := "updated description"
	req := httptest.NewRequest(http.MethodPut, "/", body(map[string]any{"Description": desc}))
	w := httptest.NewRecorder()
	svc.UpdateConfig(w, req, arn)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	cfg := resp["CodeSigningConfig"].(map[string]any)
	if cfg["Description"] != desc {
		t.Errorf("expected description %q got %q", desc, cfg["Description"])
	}
}

func TestUpdateConfig_NotFound(t *testing.T) {
	svc := newService()
	req := httptest.NewRequest(http.MethodPut, "/", body(map[string]any{}))
	w := httptest.NewRecorder()
	svc.UpdateConfig(w, req, "arn:aws:lambda:us-east-1:123456789012:code-signing-config:csc-notreal")

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 got %d", w.Code)
	}
}

// ---- DeleteConfig ----

func TestDeleteConfig_204(t *testing.T) {
	svc := newService()
	arn := createConfig(t, svc)

	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	w := httptest.NewRecorder()
	svc.DeleteConfig(w, req, arn)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204 got %d", w.Code)
	}
}

func TestDeleteConfig_NotFound(t *testing.T) {
	svc := newService()
	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	w := httptest.NewRecorder()
	svc.DeleteConfig(w, req, "arn:aws:lambda:us-east-1:123456789012:code-signing-config:csc-notreal")

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 got %d", w.Code)
	}
}

func TestDeleteConfig_ConflictWhenFunctionBound(t *testing.T) {
	svc := newService("my-fn")
	arn := createConfig(t, svc)
	bindFunction(t, svc, "my-fn", arn)

	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	w := httptest.NewRecorder()
	svc.DeleteConfig(w, req, arn)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 got %d", w.Code)
	}
}

// ---- ListConfigs ----

func TestListConfigs_Empty(t *testing.T) {
	svc := newService()
	w := do(svc, http.MethodGet, "/2015-03-31/code-signing-configs", nil, svc.ListConfigs)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	cfgs := resp["CodeSigningConfigs"].([]any)
	if len(cfgs) != 0 {
		t.Errorf("expected 0 configs, got %d", len(cfgs))
	}
}

func TestListConfigs_Multiple(t *testing.T) {
	svc := newService()
	createConfig(t, svc)
	createConfig(t, svc)
	createConfig(t, svc)

	w := do(svc, http.MethodGet, "/2015-03-31/code-signing-configs", nil, svc.ListConfigs)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	cfgs := resp["CodeSigningConfigs"].([]any)
	if len(cfgs) != 3 {
		t.Errorf("expected 3 configs, got %d", len(cfgs))
	}
}

func TestListConfigs_Pagination(t *testing.T) {
	svc := newService()
	// Create 4 configs; NextMarker will be the 3rd ARN, page 2 returns the 4th.
	for i := 0; i < 4; i++ {
		createConfig(t, svc)
	}

	req := httptest.NewRequest(http.MethodGet, "/2015-03-31/code-signing-configs", nil)
	req.URL.RawQuery = "MaxItems=2"
	w := httptest.NewRecorder()
	svc.ListConfigs(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	cfgs := resp["CodeSigningConfigs"].([]any)
	if len(cfgs) != 2 {
		t.Errorf("expected 2 configs on first page, got %d", len(cfgs))
	}
	if resp["NextMarker"] == nil || resp["NextMarker"] == "" {
		t.Fatal("expected NextMarker to be set")
	}

	// Fetch second page — URL-encode the marker (ARN contains colons).
	marker := resp["NextMarker"].(string)
	req2 := httptest.NewRequest(http.MethodGet, "/2015-03-31/code-signing-configs", nil)
	q2 := req2.URL.Query()
	q2.Set("MaxItems", "2")
	q2.Set("Marker", marker)
	req2.URL.RawQuery = q2.Encode()
	w2 := httptest.NewRecorder()
	svc.ListConfigs(w2, req2)

	var resp2 map[string]any
	json.NewDecoder(w2.Body).Decode(&resp2)
	cfgs2 := resp2["CodeSigningConfigs"].([]any)
	// Marker points to item 3; page 2 skips it and returns item 4 only.
	if len(cfgs2) != 1 {
		t.Errorf("expected 1 config on second page, got %d", len(cfgs2))
	}
	if resp2["NextMarker"] != nil && resp2["NextMarker"] != "" {
		t.Errorf("expected no NextMarker on last page, got %q", resp2["NextMarker"])
	}
}

// ---- PutFunctionConfig ----

func TestPutFunctionConfig_200(t *testing.T) {
	svc := newService("my-fn")
	arn := createConfig(t, svc)
	w := bindFunction(t, svc, "my-fn", arn)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["FunctionName"] != "my-fn" {
		t.Errorf("unexpected FunctionName: %v", resp["FunctionName"])
	}
	if resp["CodeSigningConfigArn"] != arn {
		t.Errorf("unexpected ARN: %v", resp["CodeSigningConfigArn"])
	}
}

func TestPutFunctionConfig_UnknownFunction(t *testing.T) {
	svc := newService()
	arn := createConfig(t, svc)

	req := httptest.NewRequest(http.MethodPut, "/", body(map[string]any{"CodeSigningConfigArn": arn}))
	w := httptest.NewRecorder()
	svc.PutFunctionConfig(w, req, "ghost-fn")

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 got %d", w.Code)
	}
}

func TestPutFunctionConfig_UnknownConfigArn(t *testing.T) {
	svc := newService("my-fn")

	req := httptest.NewRequest(http.MethodPut, "/", body(map[string]any{
		"CodeSigningConfigArn": "arn:aws:lambda:us-east-1:123456789012:code-signing-config:csc-notreal",
	}))
	w := httptest.NewRecorder()
	svc.PutFunctionConfig(w, req, "my-fn")

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 got %d", w.Code)
	}
}

// ---- GetFunctionConfig ----

func TestGetFunctionConfig_ReturnsBinding(t *testing.T) {
	svc := newService("my-fn")
	arn := createConfig(t, svc)
	bindFunction(t, svc, "my-fn", arn)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	svc.GetFunctionConfig(w, req, "my-fn")

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", w.Code)
	}
}

func TestGetFunctionConfig_NoBinding(t *testing.T) {
	svc := newService("my-fn")

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	svc.GetFunctionConfig(w, req, "my-fn")

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 got %d", w.Code)
	}
}

// ---- DeleteFunctionConfig ----

func TestDeleteFunctionConfig_204(t *testing.T) {
	svc := newService("my-fn")
	arn := createConfig(t, svc)
	bindFunction(t, svc, "my-fn", arn)

	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	w := httptest.NewRecorder()
	svc.DeleteFunctionConfig(w, req, "my-fn")

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204 got %d", w.Code)
	}
}

func TestDeleteFunctionConfig_NotFound(t *testing.T) {
	svc := newService("my-fn")

	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	w := httptest.NewRecorder()
	svc.DeleteFunctionConfig(w, req, "my-fn")

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 got %d", w.Code)
	}
}

// ---- ListFunctionsByConfig ----

func TestListFunctionsByConfig_ReturnsBoundFunctions(t *testing.T) {
	svc := newService("fn-a", "fn-b")
	arn := createConfig(t, svc)
	bindFunction(t, svc, "fn-a", arn)
	bindFunction(t, svc, "fn-b", arn)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	svc.ListFunctionsByConfig(w, req, arn)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	fns := resp["FunctionArns"].([]any)
	if len(fns) != 2 {
		t.Errorf("expected 2 functions, got %d", len(fns))
	}
}

func TestListFunctionsByConfig_EmptyAfterUnbind(t *testing.T) {
	svc := newService("my-fn")
	arn := createConfig(t, svc)
	bindFunction(t, svc, "my-fn", arn)

	// Unbind.
	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	w := httptest.NewRecorder()
	svc.DeleteFunctionConfig(w, req, "my-fn")

	// List should now be empty.
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	w2 := httptest.NewRecorder()
	svc.ListFunctionsByConfig(w2, req2, arn)

	var resp map[string]any
	json.NewDecoder(w2.Body).Decode(&resp)
	fns := resp["FunctionArns"].([]any)
	if len(fns) != 0 {
		t.Errorf("expected 0 functions, got %d", len(fns))
	}
}

func TestListFunctionsByConfig_UnknownConfig(t *testing.T) {
	svc := newService()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	svc.ListFunctionsByConfig(w, req, "arn:aws:lambda:us-east-1:123456789012:code-signing-config:csc-notreal")

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 got %d", w.Code)
	}
}

// ---- helpers ----

func createConfig(t *testing.T, svc *code_signing.Service) string {
	t.Helper()
	w := do(svc, http.MethodPost, "/2015-03-31/code-signing-configs", map[string]any{
		"AllowedPublishers": map[string]any{
			"SigningProfileVersionArns": []string{"arn:aws:signer:::signing-profiles/MyProfile/abcdef"},
		},
	}, svc.Create)
	if w.Code != http.StatusCreated {
		t.Fatalf("createConfig: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	cfg := resp["CodeSigningConfig"].(map[string]any)
	return cfg["CodeSigningConfigArn"].(string)
}

func bindFunction(t *testing.T, svc *code_signing.Service, functionName, arn string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/", body(map[string]any{"CodeSigningConfigArn": arn}))
	w := httptest.NewRecorder()
	svc.PutFunctionConfig(w, req, functionName)
	return w
}
