package aliases_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nimbus-local/nimbus/internal/services/lambda/aliases"
)

// mockChecker implements FunctionChecker for tests.
type mockChecker struct {
	existing map[string]bool
}

func (m *mockChecker) FunctionExists(name string) bool {
	return m.existing[name]
}

func newService(funcs ...string) *aliases.Service {
	mc := &mockChecker{existing: map[string]bool{}}
	for _, f := range funcs {
		mc.existing[f] = true
	}
	return aliases.New("us-east-1", "123456789012", mc)
}

func mustJSON(t *testing.T, v any) *bytes.Buffer {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return bytes.NewBuffer(b)
}

func decode(t *testing.T, body *bytes.Buffer, v any) {
	t.Helper()
	if err := json.NewDecoder(body).Decode(v); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

// --- CreateAlias ---

func TestCreate_HappyPath(t *testing.T) {
	svc := newService("my-func")

	body := mustJSON(t, map[string]any{
		"FunctionVersion": "1",
		"Name":            "my-alias",
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", body)

	svc.Create(w, r, "my-func")

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", w.Code)
	}

	var resp aliases.AliasConfig
	decode(t, w.Body, &resp)

	if resp.Name != "my-alias" {
		t.Errorf("Name = %q, want my-alias", resp.Name)
	}
	if resp.FunctionVersion != "1" {
		t.Errorf("FunctionVersion = %q, want 1", resp.FunctionVersion)
	}
	const wantARN = "arn:aws:lambda:us-east-1:123456789012:function:my-func:my-alias"
	if resp.AliasArn != wantARN {
		t.Errorf("AliasArn = %q, want %q", resp.AliasArn, wantARN)
	}
	if resp.RevisionId == "" {
		t.Error("RevisionId should not be empty")
	}
}

func TestCreate_FunctionNotFound(t *testing.T) {
	svc := newService() // no functions registered

	body := mustJSON(t, map[string]any{"FunctionVersion": "1", "Name": "a"})
	w := httptest.NewRecorder()
	svc.Create(w, httptest.NewRequest(http.MethodPost, "/", body), "missing-func")

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestCreate_DuplicateName(t *testing.T) {
	svc := newService("my-func")

	create := func() *httptest.ResponseRecorder {
		body := mustJSON(t, map[string]any{"FunctionVersion": "1", "Name": "dup"})
		w := httptest.NewRecorder()
		svc.Create(w, httptest.NewRequest(http.MethodPost, "/", body), "my-func")
		return w
	}

	if w := create(); w.Code != http.StatusCreated {
		t.Fatalf("first create: expected 201, got %d", w.Code)
	}
	if w := create(); w.Code != http.StatusConflict {
		t.Fatalf("second create: expected 409, got %d", w.Code)
	}
}

func TestCreate_RequiredFieldValidation(t *testing.T) {
	svc := newService("my-func")

	// Missing FunctionVersion
	body := mustJSON(t, map[string]any{"Name": "a"})
	w := httptest.NewRecorder()
	svc.Create(w, httptest.NewRequest(http.MethodPost, "/", body), "my-func")

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- GetAlias ---

func TestGet_HappyPath(t *testing.T) {
	svc := newService("my-func")

	// Create first.
	body := mustJSON(t, map[string]any{"FunctionVersion": "2", "Name": "live"})
	svc.Create(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", body), "my-func")

	w := httptest.NewRecorder()
	svc.Get(w, httptest.NewRequest(http.MethodGet, "/", nil), "my-func", "live")

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp aliases.AliasConfig
	decode(t, w.Body, &resp)
	if resp.Name != "live" {
		t.Errorf("Name = %q, want live", resp.Name)
	}
}

func TestGet_NotFound(t *testing.T) {
	svc := newService("my-func")
	w := httptest.NewRecorder()
	svc.Get(w, httptest.NewRequest(http.MethodGet, "/", nil), "my-func", "nope")

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// --- ListAliases ---

func TestList_Empty(t *testing.T) {
	svc := newService("my-func")
	w := httptest.NewRecorder()
	svc.List(w, httptest.NewRequest(http.MethodGet, "/", nil), "my-func")

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp struct {
		Aliases []aliases.AliasConfig `json:"Aliases"`
	}
	decode(t, w.Body, &resp)
	if len(resp.Aliases) != 0 {
		t.Errorf("expected 0 aliases, got %d", len(resp.Aliases))
	}
}

func TestList_FunctionNotFound(t *testing.T) {
	svc := newService()
	w := httptest.NewRecorder()
	svc.List(w, httptest.NewRequest(http.MethodGet, "/", nil), "ghost")

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestList_SortedByName(t *testing.T) {
	svc := newService("fn")
	for _, name := range []string{"beta", "alpha", "gamma"} {
		body := mustJSON(t, map[string]any{"FunctionVersion": "1", "Name": name})
		svc.Create(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", body), "fn")
	}

	w := httptest.NewRecorder()
	svc.List(w, httptest.NewRequest(http.MethodGet, "/", nil), "fn")

	var resp struct {
		Aliases []aliases.AliasConfig `json:"Aliases"`
	}
	decode(t, w.Body, &resp)

	want := []string{"alpha", "beta", "gamma"}
	if len(resp.Aliases) != len(want) {
		t.Fatalf("expected %d aliases, got %d", len(want), len(resp.Aliases))
	}
	for i, a := range resp.Aliases {
		if a.Name != want[i] {
			t.Errorf("aliases[%d].Name = %q, want %q", i, a.Name, want[i])
		}
	}
}

func TestList_FunctionVersionFilter(t *testing.T) {
	svc := newService("fn")
	for _, tc := range []struct{ name, ver string }{
		{"a1", "1"}, {"a2", "2"}, {"a3", "1"},
	} {
		body := mustJSON(t, map[string]any{"FunctionVersion": tc.ver, "Name": tc.name})
		svc.Create(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", body), "fn")
	}

	w := httptest.NewRecorder()
	svc.List(w, httptest.NewRequest(http.MethodGet, "/?FunctionVersion=1", nil), "fn")

	var resp struct {
		Aliases []aliases.AliasConfig `json:"Aliases"`
	}
	decode(t, w.Body, &resp)
	if len(resp.Aliases) != 2 {
		t.Fatalf("expected 2 aliases for version 1, got %d", len(resp.Aliases))
	}
}

func TestList_Pagination(t *testing.T) {
	svc := newService("fn")
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		body := mustJSON(t, map[string]any{"FunctionVersion": "1", "Name": name})
		svc.Create(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", body), "fn")
	}

	// First page: MaxItems=2
	w := httptest.NewRecorder()
	svc.List(w, httptest.NewRequest(http.MethodGet, "/?MaxItems=2", nil), "fn")

	var page1 struct {
		Aliases    []aliases.AliasConfig `json:"Aliases"`
		NextMarker string                `json:"NextMarker"`
	}
	decode(t, w.Body, &page1)
	if len(page1.Aliases) != 2 {
		t.Fatalf("page1: expected 2, got %d", len(page1.Aliases))
	}
	if page1.NextMarker == "" {
		t.Fatal("page1: expected NextMarker")
	}

	// Second page using the marker.
	w2 := httptest.NewRecorder()
	svc.List(w2, httptest.NewRequest(http.MethodGet, "/?MaxItems=2&Marker="+page1.NextMarker, nil), "fn")

	var page2 struct {
		Aliases    []aliases.AliasConfig `json:"Aliases"`
		NextMarker string                `json:"NextMarker"`
	}
	decode(t, w2.Body, &page2)
	if len(page2.Aliases) != 2 {
		t.Fatalf("page2: expected 2, got %d", len(page2.Aliases))
	}
}

// --- UpdateAlias ---

func TestUpdate_HappyPath(t *testing.T) {
	svc := newService("fn")
	body := mustJSON(t, map[string]any{"FunctionVersion": "1", "Name": "live"})
	svc.Create(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", body), "fn")

	updBody := mustJSON(t, map[string]any{"FunctionVersion": "2", "Description": "new desc"})
	w := httptest.NewRecorder()
	svc.Update(w, httptest.NewRequest(http.MethodPut, "/", updBody), "fn", "live")

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp aliases.AliasConfig
	decode(t, w.Body, &resp)
	if resp.FunctionVersion != "2" {
		t.Errorf("FunctionVersion = %q, want 2", resp.FunctionVersion)
	}
	if resp.Description != "new desc" {
		t.Errorf("Description = %q, want 'new desc'", resp.Description)
	}
}

func TestUpdate_PatchSemantics(t *testing.T) {
	svc := newService("fn")
	body := mustJSON(t, map[string]any{
		"FunctionVersion": "1",
		"Name":            "stable",
		"Description":     "original",
	})
	svc.Create(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", body), "fn")

	// Update only FunctionVersion; Description should remain unchanged.
	updBody := mustJSON(t, map[string]any{"FunctionVersion": "3"})
	w := httptest.NewRecorder()
	svc.Update(w, httptest.NewRequest(http.MethodPut, "/", updBody), "fn", "stable")

	var resp aliases.AliasConfig
	decode(t, w.Body, &resp)
	if resp.Description != "original" {
		t.Errorf("Description = %q, want 'original' (patch should preserve it)", resp.Description)
	}
	if resp.FunctionVersion != "3" {
		t.Errorf("FunctionVersion = %q, want 3", resp.FunctionVersion)
	}
}

func TestUpdate_NotFound(t *testing.T) {
	svc := newService("fn")
	w := httptest.NewRecorder()
	svc.Update(w, httptest.NewRequest(http.MethodPut, "/", mustJSON(t, map[string]any{})), "fn", "ghost")

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestUpdate_RevisionIdMatch(t *testing.T) {
	svc := newService("fn")
	body := mustJSON(t, map[string]any{"FunctionVersion": "1", "Name": "r"})
	cw := httptest.NewRecorder()
	svc.Create(cw, httptest.NewRequest(http.MethodPost, "/", body), "fn")
	var created aliases.AliasConfig
	decode(t, cw.Body, &created)

	// Correct RevisionId should succeed.
	updBody := mustJSON(t, map[string]any{"FunctionVersion": "2", "RevisionId": created.RevisionId})
	w := httptest.NewRecorder()
	svc.Update(w, httptest.NewRequest(http.MethodPut, "/", updBody), "fn", "r")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with correct RevisionId, got %d", w.Code)
	}
}

func TestUpdate_RevisionIdMismatch(t *testing.T) {
	svc := newService("fn")
	body := mustJSON(t, map[string]any{"FunctionVersion": "1", "Name": "r"})
	svc.Create(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", body), "fn")

	updBody := mustJSON(t, map[string]any{"FunctionVersion": "2", "RevisionId": "wrong-id"})
	w := httptest.NewRecorder()
	svc.Update(w, httptest.NewRequest(http.MethodPut, "/", updBody), "fn", "r")

	if w.Code != http.StatusPreconditionFailed {
		t.Fatalf("expected 412, got %d", w.Code)
	}
}

// --- DeleteAlias ---

func TestDelete_HappyPath(t *testing.T) {
	svc := newService("fn")
	body := mustJSON(t, map[string]any{"FunctionVersion": "1", "Name": "to-del"})
	svc.Create(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", body), "fn")

	w := httptest.NewRecorder()
	svc.Delete(w, httptest.NewRequest(http.MethodDelete, "/", nil), "fn", "to-del")
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}
}

func TestDelete_NotFound(t *testing.T) {
	svc := newService("fn")
	w := httptest.NewRecorder()
	svc.Delete(w, httptest.NewRequest(http.MethodDelete, "/", nil), "fn", "ghost")

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestDelete_DisappearsFromList(t *testing.T) {
	svc := newService("fn")
	for _, name := range []string{"keep", "remove"} {
		body := mustJSON(t, map[string]any{"FunctionVersion": "1", "Name": name})
		svc.Create(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", body), "fn")
	}

	svc.Delete(httptest.NewRecorder(), httptest.NewRequest(http.MethodDelete, "/", nil), "fn", "remove")

	w := httptest.NewRecorder()
	svc.List(w, httptest.NewRequest(http.MethodGet, "/", nil), "fn")

	var resp struct {
		Aliases []aliases.AliasConfig `json:"Aliases"`
	}
	decode(t, w.Body, &resp)
	if len(resp.Aliases) != 1 {
		t.Fatalf("expected 1 alias after delete, got %d", len(resp.Aliases))
	}
	if resp.Aliases[0].Name != "keep" {
		t.Errorf("remaining alias = %q, want 'keep'", resp.Aliases[0].Name)
	}
}
