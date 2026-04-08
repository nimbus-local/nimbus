package function_crud_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nimbus-local/nimbus/internal/services/lambda/function_crud"
)

// ---- helpers ----------------------------------------------------------------

func newTestService() *function_crud.Service {
	return function_crud.New("us-east-1", "000000000000")
}

// do sends a JSON request directly to the named method and returns the recorder.
func doCreate(t *testing.T, svc *function_crud.Service, body any) *httptest.ResponseRecorder {
	t.Helper()
	return doJSON(t, http.MethodPost, "/2015-03-31/functions", body,
		func(w http.ResponseWriter, r *http.Request) { svc.Create(w, r) })
}

func doList(t *testing.T, svc *function_crud.Service, query string) *httptest.ResponseRecorder {
	t.Helper()
	path := "/2015-03-31/functions"
	if query != "" {
		path += "?" + query
	}
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	svc.List(w, req)
	return w
}

func doGet(t *testing.T, svc *function_crud.Service, name string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/2015-03-31/functions/"+name, nil)
	w := httptest.NewRecorder()
	svc.Get(w, req, name)
	return w
}

func doGetConfiguration(t *testing.T, svc *function_crud.Service, name string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/2015-03-31/functions/"+name+"/configuration", nil)
	w := httptest.NewRecorder()
	svc.GetConfiguration(w, req, name)
	return w
}

func doUpdateCode(t *testing.T, svc *function_crud.Service, name string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return doJSON(t, http.MethodPut, "/2015-03-31/functions/"+name+"/code", body,
		func(w http.ResponseWriter, r *http.Request) { svc.UpdateCode(w, r, name) })
}

func doUpdateConfiguration(t *testing.T, svc *function_crud.Service, name string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return doJSON(t, http.MethodPut, "/2015-03-31/functions/"+name+"/configuration", body,
		func(w http.ResponseWriter, r *http.Request) { svc.UpdateConfiguration(w, r, name) })
}

func doDelete(t *testing.T, svc *function_crud.Service, name, qualifier string) *httptest.ResponseRecorder {
	t.Helper()
	path := "/2015-03-31/functions/" + name
	if qualifier != "" {
		path += "?Qualifier=" + qualifier
	}
	req := httptest.NewRequest(http.MethodDelete, path, nil)
	w := httptest.NewRecorder()
	svc.Delete(w, req, name)
	return w
}

func doListVersions(t *testing.T, svc *function_crud.Service, name string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/2015-03-31/functions/"+name+"/versions", nil)
	w := httptest.NewRecorder()
	svc.ListVersions(w, req, name)
	return w
}

func doPublishVersion(t *testing.T, svc *function_crud.Service, name string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return doJSON(t, http.MethodPost, "/2015-03-31/functions/"+name+"/versions", body,
		func(w http.ResponseWriter, r *http.Request) { svc.PublishVersion(w, r, name) })
}

// doJSON encodes body as JSON, creates the request, and invokes handler.
func doJSON(t *testing.T, method, path string, body any, handler http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	var bodyReader *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		bodyReader = bytes.NewReader(b)
	} else {
		bodyReader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, bodyReader)
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}

// decodeConfig decodes the response body into a FunctionConfig.
func decodeConfig(t *testing.T, w *httptest.ResponseRecorder) function_crud.FunctionConfig {
	t.Helper()
	var cfg function_crud.FunctionConfig
	if err := json.NewDecoder(w.Body).Decode(&cfg); err != nil {
		t.Fatalf("decode FunctionConfig: %v\nbody: %s", err, w.Body.String())
	}
	return cfg
}

// decodeError decodes the response body into the AWS error envelope.
func decodeError(t *testing.T, w *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	var e map[string]string
	if err := json.NewDecoder(w.Body).Decode(&e); err != nil {
		t.Fatalf("decode error body: %v\nbody: %s", err, w.Body.String())
	}
	return e
}

// minimalCreateBody returns the minimum valid body to create a function.
func minimalCreateBody(name string) map[string]any {
	return map[string]any{
		"FunctionName": name,
		"Role":         "arn:aws:iam::000000000000:role/test-role",
		"Handler":      "index.handler",
		"Runtime":      "nodejs18.x",
	}
}

// createFunction is a test helper that creates a function and fatals on error.
// It returns the parsed FunctionConfig response.
func createFunction(t *testing.T, svc *function_crud.Service, name string) function_crud.FunctionConfig {
	t.Helper()
	w := doCreate(t, svc, minimalCreateBody(name))
	if w.Code != http.StatusCreated {
		t.Fatalf("createFunction %q: expected 201, got %d\n%s", name, w.Code, w.Body.String())
	}
	return decodeConfig(t, w)
}

// ---- Create -----------------------------------------------------------------

func TestCreate_HappyPath(t *testing.T) {
	svc := newTestService()
	w := doCreate(t, svc, minimalCreateBody("my-func"))

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d\n%s", w.Code, w.Body.String())
	}

	cfg := decodeConfig(t, w)

	if cfg.FunctionName != "my-func" {
		t.Errorf("FunctionName: expected %q, got %q", "my-func", cfg.FunctionName)
	}
	if cfg.Handler != "index.handler" {
		t.Errorf("Handler: expected %q, got %q", "index.handler", cfg.Handler)
	}
	if cfg.Runtime != "nodejs18.x" {
		t.Errorf("Runtime: expected %q, got %q", "nodejs18.x", cfg.Runtime)
	}
	if cfg.Version != "$LATEST" {
		t.Errorf("Version: expected $LATEST, got %q", cfg.Version)
	}
	if cfg.State != "Active" {
		t.Errorf("State: expected Active, got %q", cfg.State)
	}
	if cfg.RevisionId == "" {
		t.Error("RevisionId: expected non-empty")
	}
	if !strings.Contains(cfg.FunctionArn, "my-func") {
		t.Errorf("FunctionArn: expected to contain function name, got %q", cfg.FunctionArn)
	}
}

func TestCreate_Defaults(t *testing.T) {
	svc := newTestService()
	cfg := createFunction(t, svc, "defaults-func")

	if cfg.PackageType != "Zip" {
		t.Errorf("PackageType default: expected Zip, got %q", cfg.PackageType)
	}
	if len(cfg.Architectures) != 1 || cfg.Architectures[0] != "x86_64" {
		t.Errorf("Architectures default: expected [x86_64], got %v", cfg.Architectures)
	}
	if cfg.MemorySize != 128 {
		t.Errorf("MemorySize default: expected 128, got %d", cfg.MemorySize)
	}
	if cfg.Timeout != 3 {
		t.Errorf("Timeout default: expected 3, got %d", cfg.Timeout)
	}
	if cfg.EphemeralStorage == nil || cfg.EphemeralStorage.Size != 512 {
		t.Errorf("EphemeralStorage default: expected {Size:512}, got %+v", cfg.EphemeralStorage)
	}
}

func TestCreate_ImagePackageType_NoHandlerRequired(t *testing.T) {
	svc := newTestService()
	body := map[string]any{
		"FunctionName": "image-func",
		"Role":         "arn:aws:iam::000000000000:role/test-role",
		"PackageType":  "Image",
		"Code":         map[string]any{"ImageUri": "123456789.dkr.ecr.us-east-1.amazonaws.com/my-image:latest"},
	}
	w := doCreate(t, svc, body)
	if w.Code != http.StatusCreated {
		t.Errorf("Image PackageType: expected 201, got %d\n%s", w.Code, w.Body.String())
	}
}

func TestCreate_MissingFunctionName(t *testing.T) {
	svc := newTestService()
	body := map[string]any{
		"Role":    "arn:aws:iam::000000000000:role/test-role",
		"Handler": "index.handler",
		"Runtime": "nodejs18.x",
	}
	w := doCreate(t, svc, body)
	if w.Code != http.StatusBadRequest {
		t.Errorf("missing FunctionName: expected 400, got %d", w.Code)
	}
	e := decodeError(t, w)
	if e["__type"] != "InvalidParameterValueException" {
		t.Errorf("expected InvalidParameterValueException, got %q", e["__type"])
	}
}

func TestCreate_MissingRole(t *testing.T) {
	svc := newTestService()
	body := map[string]any{
		"FunctionName": "no-role-func",
		"Handler":      "index.handler",
		"Runtime":      "nodejs18.x",
	}
	w := doCreate(t, svc, body)
	if w.Code != http.StatusBadRequest {
		t.Errorf("missing Role: expected 400, got %d", w.Code)
	}
}

func TestCreate_MissingHandlerForZipPackage(t *testing.T) {
	svc := newTestService()
	body := map[string]any{
		"FunctionName": "no-handler-func",
		"Role":         "arn:aws:iam::000000000000:role/test-role",
		"Runtime":      "nodejs18.x",
	}
	w := doCreate(t, svc, body)
	if w.Code != http.StatusBadRequest {
		t.Errorf("missing Handler for Zip: expected 400, got %d", w.Code)
	}
}

func TestCreate_Conflict(t *testing.T) {
	svc := newTestService()
	createFunction(t, svc, "duplicate-func")

	w := doCreate(t, svc, minimalCreateBody("duplicate-func"))
	if w.Code != http.StatusConflict {
		t.Errorf("duplicate function: expected 409, got %d", w.Code)
	}
	e := decodeError(t, w)
	if e["__type"] != "ResourceConflictException" {
		t.Errorf("expected ResourceConflictException, got %q", e["__type"])
	}
}

func TestCreate_WithOptionalFields(t *testing.T) {
	svc := newTestService()
	body := map[string]any{
		"FunctionName": "rich-func",
		"Role":         "arn:aws:iam::000000000000:role/test-role",
		"Handler":      "main.handler",
		"Runtime":      "python3.11",
		"Description":  "A test function",
		"MemorySize":   512,
		"Timeout":      30,
		"Architectures": []string{"arm64"},
		"Environment": map[string]any{
			"Variables": map[string]string{"KEY": "VALUE"},
		},
	}
	w := doCreate(t, svc, body)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d\n%s", w.Code, w.Body.String())
	}
	cfg := decodeConfig(t, w)
	if cfg.Description != "A test function" {
		t.Errorf("Description: expected %q, got %q", "A test function", cfg.Description)
	}
	if cfg.MemorySize != 512 {
		t.Errorf("MemorySize: expected 512, got %d", cfg.MemorySize)
	}
	if cfg.Timeout != 30 {
		t.Errorf("Timeout: expected 30, got %d", cfg.Timeout)
	}
	if len(cfg.Architectures) != 1 || cfg.Architectures[0] != "arm64" {
		t.Errorf("Architectures: expected [arm64], got %v", cfg.Architectures)
	}
	if cfg.Environment == nil || cfg.Environment.Variables["KEY"] != "VALUE" {
		t.Errorf("Environment.Variables: expected KEY=VALUE, got %+v", cfg.Environment)
	}
}

func TestCreate_ContentTypeHeader(t *testing.T) {
	svc := newTestService()
	w := doCreate(t, svc, minimalCreateBody("ct-func"))
	ct := w.Header().Get("Content-Type")
	if ct != "application/x-amz-json-1.1" {
		t.Errorf("Content-Type: expected application/x-amz-json-1.1, got %q", ct)
	}
}

// ---- List -------------------------------------------------------------------

func TestList_Empty(t *testing.T) {
	svc := newTestService()
	w := doList(t, svc, "")
	if w.Code != http.StatusOK {
		t.Fatalf("List empty: expected 200, got %d", w.Code)
	}
	var resp struct {
		Functions  []function_crud.FunctionConfig `json:"Functions"`
		NextMarker string                         `json:"NextMarker"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode List response: %v", err)
	}
	if len(resp.Functions) != 0 {
		t.Errorf("expected empty list, got %d functions", len(resp.Functions))
	}
	if resp.NextMarker != "" {
		t.Errorf("expected no NextMarker, got %q", resp.NextMarker)
	}
}

func TestList_MultipleFunction(t *testing.T) {
	svc := newTestService()
	for _, name := range []string{"func-a", "func-b", "func-c"} {
		createFunction(t, svc, name)
	}

	w := doList(t, svc, "")
	if w.Code != http.StatusOK {
		t.Fatalf("List: expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	for _, name := range []string{"func-a", "func-b", "func-c"} {
		if !strings.Contains(body, name) {
			t.Errorf("expected %q in List response", name)
		}
	}
}

func TestList_OnlyReturnsLatest(t *testing.T) {
	svc := newTestService()
	createFunction(t, svc, "versioned-func")
	// Publish a version — should not appear in the flat list.
	doPublishVersion(t, svc, "versioned-func", nil)

	w := doList(t, svc, "")
	var resp struct {
		Functions []function_crud.FunctionConfig `json:"Functions"`
	}
	json.NewDecoder(w.Body).Decode(&resp)

	if len(resp.Functions) != 1 {
		t.Errorf("List: expected 1 $LATEST entry, got %d", len(resp.Functions))
	}
	if resp.Functions[0].Version != "$LATEST" {
		t.Errorf("List: expected $LATEST, got %q", resp.Functions[0].Version)
	}
}

func TestList_Pagination(t *testing.T) {
	svc := newTestService()
	// Create 4 functions; names are alphabetically ordered.
	names := []string{"pg-fn-01", "pg-fn-02", "pg-fn-03", "pg-fn-04"}
	for _, n := range names {
		createFunction(t, svc, n)
	}

	// First page: 2 items.
	w := doList(t, svc, "MaxItems=2")
	if w.Code != http.StatusOK {
		t.Fatalf("List page 1: expected 200, got %d", w.Code)
	}
	var page1 struct {
		Functions  []function_crud.FunctionConfig `json:"Functions"`
		NextMarker string                         `json:"NextMarker"`
	}
	json.NewDecoder(w.Body).Decode(&page1)

	if len(page1.Functions) != 2 {
		t.Errorf("page 1: expected 2 functions, got %d", len(page1.Functions))
	}
	// With 4 items and MaxItems=2, NextMarker is the 3rd item's name.
	if page1.NextMarker == "" {
		t.Error("page 1: expected NextMarker to be set")
	}

	// Second page: Marker skips past the NextMarker name, returning the
	// items after it. With sorted [01,02,03,04] and NextMarker=03, page 2
	// skips 03 and returns [04] with no further marker.
	w = doList(t, svc, "MaxItems=2&Marker="+page1.NextMarker)
	if w.Code != http.StatusOK {
		t.Fatalf("List page 2: expected 200, got %d", w.Code)
	}
	var page2 struct {
		Functions  []function_crud.FunctionConfig `json:"Functions"`
		NextMarker string                         `json:"NextMarker"`
	}
	json.NewDecoder(w.Body).Decode(&page2)

	if len(page2.Functions) == 0 {
		t.Error("page 2: expected at least 1 function")
	}
	if page2.NextMarker != "" {
		t.Errorf("page 2: expected no NextMarker (last page), got %q", page2.NextMarker)
	}

	// Page 1 and page 2 together should have no duplicates.
	seen := map[string]bool{}
	for _, fn := range append(page1.Functions, page2.Functions...) {
		if seen[fn.FunctionName] {
			t.Errorf("duplicate function name across pages: %q", fn.FunctionName)
		}
		seen[fn.FunctionName] = true
	}

	// Page 1 must contain the first two names; page 2 must be a subset of the rest.
	if page1.Functions[0].FunctionName != "pg-fn-01" {
		t.Errorf("page 1[0]: expected pg-fn-01, got %q", page1.Functions[0].FunctionName)
	}
	if page1.Functions[1].FunctionName != "pg-fn-02" {
		t.Errorf("page 1[1]: expected pg-fn-02, got %q", page1.Functions[1].FunctionName)
	}
}

// ---- Get --------------------------------------------------------------------

func TestGet_HappyPath(t *testing.T) {
	svc := newTestService()
	created := createFunction(t, svc, "get-func")

	w := doGet(t, svc, "get-func")
	if w.Code != http.StatusOK {
		t.Fatalf("Get: expected 200, got %d\n%s", w.Code, w.Body.String())
	}
	got := decodeConfig(t, w)
	if got.FunctionName != created.FunctionName {
		t.Errorf("FunctionName: expected %q, got %q", created.FunctionName, got.FunctionName)
	}
	if got.RevisionId != created.RevisionId {
		t.Errorf("RevisionId mismatch: expected %q, got %q", created.RevisionId, got.RevisionId)
	}
}

func TestGet_NotFound(t *testing.T) {
	svc := newTestService()
	w := doGet(t, svc, "ghost-func")
	if w.Code != http.StatusNotFound {
		t.Errorf("Get missing: expected 404, got %d", w.Code)
	}
	e := decodeError(t, w)
	if e["__type"] != "ResourceNotFoundException" {
		t.Errorf("expected ResourceNotFoundException, got %q", e["__type"])
	}
	errHeader := w.Header().Get("x-amzn-ErrorType")
	if errHeader != "ResourceNotFoundException" {
		t.Errorf("x-amzn-ErrorType header: expected ResourceNotFoundException, got %q", errHeader)
	}
}

// ---- GetConfiguration -------------------------------------------------------

func TestGetConfiguration_HappyPath(t *testing.T) {
	svc := newTestService()
	createFunction(t, svc, "cfg-func")

	w := doGetConfiguration(t, svc, "cfg-func")
	if w.Code != http.StatusOK {
		t.Fatalf("GetConfiguration: expected 200, got %d\n%s", w.Code, w.Body.String())
	}
	cfg := decodeConfig(t, w)
	if cfg.FunctionName != "cfg-func" {
		t.Errorf("FunctionName: expected cfg-func, got %q", cfg.FunctionName)
	}
}

func TestGetConfiguration_NotFound(t *testing.T) {
	svc := newTestService()
	w := doGetConfiguration(t, svc, "no-such-func")
	if w.Code != http.StatusNotFound {
		t.Errorf("GetConfiguration missing: expected 404, got %d", w.Code)
	}
}

// ---- UpdateCode -------------------------------------------------------------

func TestUpdateCode_HappyPath(t *testing.T) {
	svc := newTestService()
	original := createFunction(t, svc, "code-func")

	w := doUpdateCode(t, svc, "code-func", map[string]any{
		"ZipFile": []byte("new-zip-bytes"),
	})
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateCode: expected 200, got %d\n%s", w.Code, w.Body.String())
	}
	updated := decodeConfig(t, w)

	if updated.RevisionId == original.RevisionId {
		t.Error("UpdateCode: RevisionId should change after update")
	}
	if updated.FunctionName != "code-func" {
		t.Errorf("FunctionName changed unexpectedly: got %q", updated.FunctionName)
	}
}

func TestUpdateCode_NotFound(t *testing.T) {
	svc := newTestService()
	w := doUpdateCode(t, svc, "ghost-func", map[string]any{})
	if w.Code != http.StatusNotFound {
		t.Errorf("UpdateCode missing: expected 404, got %d", w.Code)
	}
	e := decodeError(t, w)
	if e["__type"] != "ResourceNotFoundException" {
		t.Errorf("expected ResourceNotFoundException, got %q", e["__type"])
	}
}

func TestUpdateCode_RevisionIdMatch(t *testing.T) {
	svc := newTestService()
	fn := createFunction(t, svc, "revid-code-func")

	// Correct RevisionId should succeed.
	w := doUpdateCode(t, svc, "revid-code-func", map[string]any{
		"RevisionId": fn.RevisionId,
	})
	if w.Code != http.StatusOK {
		t.Errorf("UpdateCode matching RevisionId: expected 200, got %d\n%s", w.Code, w.Body.String())
	}
}

func TestUpdateCode_RevisionIdMismatch(t *testing.T) {
	svc := newTestService()
	createFunction(t, svc, "stale-code-func")

	w := doUpdateCode(t, svc, "stale-code-func", map[string]any{
		"RevisionId": "00000000-0000-0000-0000-000000000000",
	})
	if w.Code != http.StatusPreconditionFailed {
		t.Errorf("UpdateCode stale RevisionId: expected 412, got %d", w.Code)
	}
	e := decodeError(t, w)
	if e["__type"] != "PreconditionFailedException" {
		t.Errorf("expected PreconditionFailedException, got %q", e["__type"])
	}
}

func TestUpdateCode_DryRun(t *testing.T) {
	svc := newTestService()
	original := createFunction(t, svc, "dryrun-func")

	w := doUpdateCode(t, svc, "dryrun-func", map[string]any{
		"DryRun": true,
	})
	if w.Code != http.StatusNoContent {
		t.Errorf("UpdateCode DryRun: expected 204, got %d", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Errorf("UpdateCode DryRun: expected empty body, got %d bytes", w.Body.Len())
	}

	// The function should be unchanged.
	getCfg := decodeConfig(t, doGet(t, svc, "dryrun-func"))
	if getCfg.RevisionId != original.RevisionId {
		t.Error("UpdateCode DryRun: RevisionId should not change")
	}
}

func TestUpdateCode_UpdatesArchitectures(t *testing.T) {
	svc := newTestService()
	createFunction(t, svc, "arch-func")

	w := doUpdateCode(t, svc, "arch-func", map[string]any{
		"Architectures": []string{"arm64"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateCode arch: expected 200, got %d", w.Code)
	}
	cfg := decodeConfig(t, w)
	if len(cfg.Architectures) != 1 || cfg.Architectures[0] != "arm64" {
		t.Errorf("Architectures: expected [arm64], got %v", cfg.Architectures)
	}
}

// ---- UpdateConfiguration ----------------------------------------------------

func TestUpdateConfiguration_HappyPath(t *testing.T) {
	svc := newTestService()
	createFunction(t, svc, "upd-cfg-func")

	w := doUpdateConfiguration(t, svc, "upd-cfg-func", map[string]any{
		"Description": "updated description",
		"MemorySize":  256,
		"Timeout":     60,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateConfiguration: expected 200, got %d\n%s", w.Code, w.Body.String())
	}
	cfg := decodeConfig(t, w)

	if cfg.Description != "updated description" {
		t.Errorf("Description: expected %q, got %q", "updated description", cfg.Description)
	}
	if cfg.MemorySize != 256 {
		t.Errorf("MemorySize: expected 256, got %d", cfg.MemorySize)
	}
	if cfg.Timeout != 60 {
		t.Errorf("Timeout: expected 60, got %d", cfg.Timeout)
	}
}

func TestUpdateConfiguration_PatchSemantics(t *testing.T) {
	svc := newTestService()
	// Create with specific handler and runtime.
	body := minimalCreateBody("patch-func")
	body["Handler"] = "original.handler"
	body["Runtime"] = "nodejs18.x"
	doCreate(t, svc, body)

	// Update only the handler — runtime must remain unchanged.
	w := doUpdateConfiguration(t, svc, "patch-func", map[string]any{
		"Handler": "new.handler",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	cfg := decodeConfig(t, w)
	if cfg.Handler != "new.handler" {
		t.Errorf("Handler: expected new.handler, got %q", cfg.Handler)
	}
	if cfg.Runtime != "nodejs18.x" {
		t.Errorf("Runtime should be unchanged, got %q", cfg.Runtime)
	}
}

func TestUpdateConfiguration_NotFound(t *testing.T) {
	svc := newTestService()
	w := doUpdateConfiguration(t, svc, "ghost-func", map[string]any{"Timeout": 10})
	if w.Code != http.StatusNotFound {
		t.Errorf("UpdateConfiguration missing: expected 404, got %d", w.Code)
	}
}

func TestUpdateConfiguration_RevisionIdMismatch(t *testing.T) {
	svc := newTestService()
	createFunction(t, svc, "stale-cfg-func")

	w := doUpdateConfiguration(t, svc, "stale-cfg-func", map[string]any{
		"Timeout":    10,
		"RevisionId": "00000000-0000-0000-0000-000000000000",
	})
	if w.Code != http.StatusPreconditionFailed {
		t.Errorf("UpdateConfiguration stale RevisionId: expected 412, got %d", w.Code)
	}
	e := decodeError(t, w)
	if e["__type"] != "PreconditionFailedException" {
		t.Errorf("expected PreconditionFailedException, got %q", e["__type"])
	}
}

func TestUpdateConfiguration_RevisionIdMatch(t *testing.T) {
	svc := newTestService()
	fn := createFunction(t, svc, "matching-cfg-func")

	w := doUpdateConfiguration(t, svc, "matching-cfg-func", map[string]any{
		"Timeout":    15,
		"RevisionId": fn.RevisionId,
	})
	if w.Code != http.StatusOK {
		t.Errorf("UpdateConfiguration matching RevisionId: expected 200, got %d\n%s", w.Code, w.Body.String())
	}
}

func TestUpdateConfiguration_AdvancesRevisionId(t *testing.T) {
	svc := newTestService()
	fn := createFunction(t, svc, "rev-func")

	w := doUpdateConfiguration(t, svc, "rev-func", map[string]any{"Timeout": 10})
	cfg := decodeConfig(t, w)
	if cfg.RevisionId == fn.RevisionId {
		t.Error("UpdateConfiguration: RevisionId should change after update")
	}
}

// ---- Delete -----------------------------------------------------------------

func TestDelete_HappyPath(t *testing.T) {
	svc := newTestService()
	createFunction(t, svc, "del-func")

	w := doDelete(t, svc, "del-func", "")
	if w.Code != http.StatusNoContent {
		t.Errorf("Delete: expected 204, got %d\n%s", w.Code, w.Body.String())
	}

	// Function should no longer be accessible.
	w = doGet(t, svc, "del-func")
	if w.Code != http.StatusNotFound {
		t.Errorf("after delete: expected 404, got %d", w.Code)
	}
}

func TestDelete_NotFound(t *testing.T) {
	svc := newTestService()
	w := doDelete(t, svc, "ghost-func", "")
	if w.Code != http.StatusNotFound {
		t.Errorf("Delete missing: expected 404, got %d", w.Code)
	}
	e := decodeError(t, w)
	if e["__type"] != "ResourceNotFoundException" {
		t.Errorf("expected ResourceNotFoundException, got %q", e["__type"])
	}
}

func TestDelete_PublishedVersion(t *testing.T) {
	svc := newTestService()
	createFunction(t, svc, "versioned-del-func")

	// Publish a version.
	pw := doPublishVersion(t, svc, "versioned-del-func", nil)
	if pw.Code != http.StatusOK {
		t.Fatalf("PublishVersion: expected 200, got %d", pw.Code)
	}
	published := decodeConfig(t, pw)
	version := published.Version

	// Delete the specific published version.
	w := doDelete(t, svc, "versioned-del-func", version)
	if w.Code != http.StatusNoContent {
		t.Errorf("Delete version %q: expected 204, got %d\n%s", version, w.Code, w.Body.String())
	}

	// The $LATEST should still exist.
	w = doGet(t, svc, "versioned-del-func")
	if w.Code != http.StatusOK {
		t.Errorf("$LATEST after version delete: expected 200, got %d", w.Code)
	}
}

func TestDelete_RemovesFromList(t *testing.T) {
	svc := newTestService()
	createFunction(t, svc, "listed-func")
	createFunction(t, svc, "keeper-func")

	doDelete(t, svc, "listed-func", "")

	w := doList(t, svc, "")
	body := w.Body.String()
	if strings.Contains(body, "listed-func") {
		t.Error("deleted function should not appear in List")
	}
	if !strings.Contains(body, "keeper-func") {
		t.Error("non-deleted function should still appear in List")
	}
}

// ---- ListVersions -----------------------------------------------------------

func TestListVersions_OnlyLatestBeforePublish(t *testing.T) {
	svc := newTestService()
	createFunction(t, svc, "ver-func")

	w := doListVersions(t, svc, "ver-func")
	if w.Code != http.StatusOK {
		t.Fatalf("ListVersions: expected 200, got %d\n%s", w.Code, w.Body.String())
	}

	var resp struct {
		Versions []function_crud.FunctionConfig `json:"Versions"`
	}
	json.NewDecoder(w.Body).Decode(&resp)

	if len(resp.Versions) != 1 {
		t.Errorf("ListVersions before publish: expected 1 ($LATEST), got %d", len(resp.Versions))
	}
	if resp.Versions[0].Version != "$LATEST" {
		t.Errorf("expected $LATEST, got %q", resp.Versions[0].Version)
	}
}

func TestListVersions_IncludesPublished(t *testing.T) {
	svc := newTestService()
	createFunction(t, svc, "multi-ver-func")

	// Publish two versions.
	doPublishVersion(t, svc, "multi-ver-func", nil)
	doPublishVersion(t, svc, "multi-ver-func", nil)

	w := doListVersions(t, svc, "multi-ver-func")
	var resp struct {
		Versions []function_crud.FunctionConfig `json:"Versions"`
	}
	json.NewDecoder(w.Body).Decode(&resp)

	// Should have $LATEST + version 1 + version 2 = 3.
	if len(resp.Versions) != 3 {
		t.Errorf("ListVersions after 2 publishes: expected 3, got %d", len(resp.Versions))
	}

	versionSet := map[string]bool{}
	for _, v := range resp.Versions {
		versionSet[v.Version] = true
	}
	for _, expected := range []string{"$LATEST", "1", "2"} {
		if !versionSet[expected] {
			t.Errorf("expected version %q in ListVersions response", expected)
		}
	}
}

func TestListVersions_NotFound(t *testing.T) {
	svc := newTestService()
	w := doListVersions(t, svc, "ghost-func")
	if w.Code != http.StatusNotFound {
		t.Errorf("ListVersions missing: expected 404, got %d", w.Code)
	}
	e := decodeError(t, w)
	if e["__type"] != "ResourceNotFoundException" {
		t.Errorf("expected ResourceNotFoundException, got %q", e["__type"])
	}
}

// ---- PublishVersion ---------------------------------------------------------

func TestPublishVersion_HappyPath(t *testing.T) {
	svc := newTestService()
	createFunction(t, svc, "pub-func")

	w := doPublishVersion(t, svc, "pub-func", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("PublishVersion: expected 200, got %d\n%s", w.Code, w.Body.String())
	}
	published := decodeConfig(t, w)

	if published.Version != "1" {
		t.Errorf("first published version: expected %q, got %q", "1", published.Version)
	}
	if published.FunctionName != "pub-func" {
		t.Errorf("FunctionName: expected pub-func, got %q", published.FunctionName)
	}
}

func TestPublishVersion_IncrementsCounter(t *testing.T) {
	svc := newTestService()
	createFunction(t, svc, "counter-func")

	for i := 1; i <= 3; i++ {
		w := doPublishVersion(t, svc, "counter-func", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("PublishVersion %d: expected 200, got %d", i, w.Code)
		}
		cfg := decodeConfig(t, w)
		expected := fmt.Sprintf("%d", i)
		if cfg.Version != expected {
			t.Errorf("publish %d: expected version %q, got %q", i, expected, cfg.Version)
		}
	}
}

func TestPublishVersion_WithDescription(t *testing.T) {
	svc := newTestService()
	createFunction(t, svc, "desc-ver-func")

	w := doPublishVersion(t, svc, "desc-ver-func", map[string]any{
		"Description": "release v1",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("PublishVersion with description: expected 200, got %d", w.Code)
	}
	cfg := decodeConfig(t, w)
	if cfg.Description != "release v1" {
		t.Errorf("Description: expected %q, got %q", "release v1", cfg.Description)
	}
}

func TestPublishVersion_NotFound(t *testing.T) {
	svc := newTestService()
	w := doPublishVersion(t, svc, "ghost-func", nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("PublishVersion missing: expected 404, got %d", w.Code)
	}
	e := decodeError(t, w)
	if e["__type"] != "ResourceNotFoundException" {
		t.Errorf("expected ResourceNotFoundException, got %q", e["__type"])
	}
}

func TestPublishVersion_RevisionIdMismatch(t *testing.T) {
	svc := newTestService()
	createFunction(t, svc, "stale-pub-func")

	w := doPublishVersion(t, svc, "stale-pub-func", map[string]any{
		"RevisionId": "00000000-0000-0000-0000-000000000000",
	})
	if w.Code != http.StatusPreconditionFailed {
		t.Errorf("PublishVersion stale RevisionId: expected 412, got %d", w.Code)
	}
	e := decodeError(t, w)
	if e["__type"] != "PreconditionFailedException" {
		t.Errorf("expected PreconditionFailedException, got %q", e["__type"])
	}
}

func TestPublishVersion_RevisionIdMatch(t *testing.T) {
	svc := newTestService()
	fn := createFunction(t, svc, "match-pub-func")

	w := doPublishVersion(t, svc, "match-pub-func", map[string]any{
		"RevisionId": fn.RevisionId,
	})
	if w.Code != http.StatusOK {
		t.Errorf("PublishVersion matching RevisionId: expected 200, got %d\n%s", w.Code, w.Body.String())
	}
}

func TestPublishVersion_SnapshotsLatest(t *testing.T) {
	svc := newTestService()
	createFunction(t, svc, "snapshot-func")

	// Update then publish — version should reflect the updated handler.
	doUpdateConfiguration(t, svc, "snapshot-func", map[string]any{"Handler": "updated.handler"})

	w := doPublishVersion(t, svc, "snapshot-func", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	cfg := decodeConfig(t, w)
	if cfg.Handler != "updated.handler" {
		t.Errorf("published version should snapshot $LATEST handler, got %q", cfg.Handler)
	}
	if cfg.Version != "1" {
		t.Errorf("expected version 1, got %q", cfg.Version)
	}
}

// ---- Cross-operation integration tests --------------------------------------

func TestFunctionExists(t *testing.T) {
	svc := newTestService()

	if svc.FunctionExists("missing-func") {
		t.Error("FunctionExists: should return false for missing function")
	}

	createFunction(t, svc, "present-func")

	if !svc.FunctionExists("present-func") {
		t.Error("FunctionExists: should return true after creation")
	}

	doDelete(t, svc, "present-func", "")

	if svc.FunctionExists("present-func") {
		t.Error("FunctionExists: should return false after deletion")
	}
}

func TestCRUD_FullLifecycle(t *testing.T) {
	svc := newTestService()

	// 1. Create
	fn := createFunction(t, svc, "lifecycle-func")
	if fn.Version != "$LATEST" {
		t.Errorf("create: expected $LATEST, got %q", fn.Version)
	}

	// 2. Get confirms creation
	w := doGet(t, svc, "lifecycle-func")
	if w.Code != http.StatusOK {
		t.Fatalf("get after create: expected 200, got %d", w.Code)
	}

	// 3. Update configuration
	w = doUpdateConfiguration(t, svc, "lifecycle-func", map[string]any{"Timeout": 30})
	updated := decodeConfig(t, w)
	if updated.Timeout != 30 {
		t.Errorf("after update: Timeout expected 30, got %d", updated.Timeout)
	}

	// 4. Publish a version
	w = doPublishVersion(t, svc, "lifecycle-func", nil)
	v1 := decodeConfig(t, w)
	if v1.Version != "1" {
		t.Errorf("publish: expected version 1, got %q", v1.Version)
	}

	// 5. ListVersions should have $LATEST and version 1
	w = doListVersions(t, svc, "lifecycle-func")
	var verResp struct {
		Versions []function_crud.FunctionConfig `json:"Versions"`
	}
	json.NewDecoder(w.Body).Decode(&verResp)
	if len(verResp.Versions) != 2 {
		t.Errorf("ListVersions: expected 2, got %d", len(verResp.Versions))
	}

	// 6. Delete the function
	w = doDelete(t, svc, "lifecycle-func", "")
	if w.Code != http.StatusNoContent {
		t.Errorf("delete: expected 204, got %d", w.Code)
	}

	// 7. Get should 404
	w = doGet(t, svc, "lifecycle-func")
	if w.Code != http.StatusNotFound {
		t.Errorf("get after delete: expected 404, got %d", w.Code)
	}
}

func TestRevisionId_IsolatedPerFunction(t *testing.T) {
	svc := newTestService()
	fn1 := createFunction(t, svc, "rev-fn1")
	fn2 := createFunction(t, svc, "rev-fn2")

	if fn1.RevisionId == fn2.RevisionId {
		t.Error("distinct functions should have distinct RevisionIds")
	}
}

func TestARN_Format(t *testing.T) {
	svc := newTestService()
	fn := createFunction(t, svc, "arn-func")

	expectedARN := "arn:aws:lambda:us-east-1:000000000000:function:arn-func"
	if fn.FunctionArn != expectedARN {
		t.Errorf("FunctionArn: expected %q, got %q", expectedARN, fn.FunctionArn)
	}
}
