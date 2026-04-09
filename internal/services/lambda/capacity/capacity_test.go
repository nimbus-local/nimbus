package capacity_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/nimbus-local/nimbus/internal/services/lambda/capacity"
)

// mockTagStore implements TagStore for tests.
type mockTagStore struct {
	mu        sync.RWMutex
	functions map[string]map[string]string // function name -> tags
}

func newMockTagStore(names ...string) *mockTagStore {
	m := &mockTagStore{functions: map[string]map[string]string{}}
	for _, n := range names {
		m.functions[n] = map[string]string{}
	}
	return m
}

func (m *mockTagStore) GetTags(name string) (map[string]string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	tags, ok := m.functions[name]
	if !ok {
		return nil, false
	}
	out := make(map[string]string, len(tags))
	for k, v := range tags {
		out[k] = v
	}
	return out, true
}

func (m *mockTagStore) SetTags(name string, tags map[string]string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.functions[name]; !ok {
		return false
	}
	m.functions[name] = tags
	return true
}

// ---- helpers ----------------------------------------------------------------

func arn(name string) string {
	return fmt.Sprintf("arn:aws:lambda:us-east-1:000000000000:function:%s", name)
}

func doListTags(t *testing.T, svc *capacity.Service, name string) *httptest.ResponseRecorder {
	t.Helper()
	a := arn(name)
	req := httptest.NewRequest(http.MethodGet, "/2015-03-31/tags/"+a, nil)
	w := httptest.NewRecorder()
	svc.ListTags(w, req, a)
	return w
}

func doTagResource(t *testing.T, svc *capacity.Service, name string, tags map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	a := arn(name)
	body, _ := json.Marshal(map[string]any{"Tags": tags})
	req := httptest.NewRequest(http.MethodPost, "/2015-03-31/tags/"+a, bytes.NewReader(body))
	w := httptest.NewRecorder()
	svc.TagResource(w, req, a)
	return w
}

func doUntagResource(t *testing.T, svc *capacity.Service, name string, keys []string) *httptest.ResponseRecorder {
	t.Helper()
	a := arn(name)
	url := "/2015-03-31/tags/" + a
	for i, k := range keys {
		if i == 0 {
			url += "?tagKeys=" + k
		} else {
			url += "&tagKeys=" + k
		}
	}
	req := httptest.NewRequest(http.MethodDelete, url, nil)
	w := httptest.NewRecorder()
	svc.UntagResource(w, req, a)
	return w
}

func decodeTags(t *testing.T, w *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	var resp struct {
		Tags map[string]string `json:"Tags"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode tags: %v", err)
	}
	return resp.Tags
}

// ---- ListTags ---------------------------------------------------------------

func TestListTags_EmptyTags(t *testing.T) {
	store := newMockTagStore("my-func")
	svc := capacity.New(store)

	w := doListTags(t, svc, "my-func")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	tags := decodeTags(t, w)
	if len(tags) != 0 {
		t.Errorf("want empty tags, got %v", tags)
	}
}

func TestListTags_WithTags(t *testing.T) {
	store := newMockTagStore("my-func")
	store.SetTags("my-func", map[string]string{"env": "prod", "team": "infra"})
	svc := capacity.New(store)

	w := doListTags(t, svc, "my-func")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	tags := decodeTags(t, w)
	if tags["env"] != "prod" || tags["team"] != "infra" {
		t.Errorf("unexpected tags: %v", tags)
	}
}

func TestListTags_NotFound(t *testing.T) {
	store := newMockTagStore()
	svc := capacity.New(store)

	w := doListTags(t, svc, "missing-func")
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

// ---- TagResource ------------------------------------------------------------

func TestTagResource_AddsTags(t *testing.T) {
	store := newMockTagStore("my-func")
	svc := capacity.New(store)

	w := doTagResource(t, svc, "my-func", map[string]string{"env": "dev"})
	if w.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d", w.Code)
	}
	tags, _ := store.GetTags("my-func")
	if tags["env"] != "dev" {
		t.Errorf("expected tag env=dev, got %v", tags)
	}
}

func TestTagResource_MergesWithExisting(t *testing.T) {
	store := newMockTagStore("my-func")
	store.SetTags("my-func", map[string]string{"existing": "yes"})
	svc := capacity.New(store)

	w := doTagResource(t, svc, "my-func", map[string]string{"new": "tag"})
	if w.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d", w.Code)
	}
	tags, _ := store.GetTags("my-func")
	if tags["existing"] != "yes" || tags["new"] != "tag" {
		t.Errorf("merge failed, got %v", tags)
	}
}

func TestTagResource_NotFound(t *testing.T) {
	store := newMockTagStore()
	svc := capacity.New(store)

	w := doTagResource(t, svc, "missing-func", map[string]string{"k": "v"})
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

// ---- UntagResource ----------------------------------------------------------

func TestUntagResource_RemovesKeys(t *testing.T) {
	store := newMockTagStore("my-func")
	store.SetTags("my-func", map[string]string{"a": "1", "b": "2", "c": "3"})
	svc := capacity.New(store)

	w := doUntagResource(t, svc, "my-func", []string{"a", "c"})
	if w.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d", w.Code)
	}
	tags, _ := store.GetTags("my-func")
	if _, ok := tags["a"]; ok {
		t.Error("key 'a' should have been removed")
	}
	if tags["b"] != "2" {
		t.Errorf("key 'b' should remain, got %v", tags)
	}
}

func TestUntagResource_NoopForMissingKeys(t *testing.T) {
	store := newMockTagStore("my-func")
	store.SetTags("my-func", map[string]string{"a": "1"})
	svc := capacity.New(store)

	w := doUntagResource(t, svc, "my-func", []string{"nonexistent"})
	if w.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d", w.Code)
	}
	tags, _ := store.GetTags("my-func")
	if tags["a"] != "1" {
		t.Errorf("existing tag should be untouched, got %v", tags)
	}
}

func TestUntagResource_NotFound(t *testing.T) {
	store := newMockTagStore()
	svc := capacity.New(store)

	w := doUntagResource(t, svc, "missing-func", []string{"k"})
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

// ---- Round-trip -------------------------------------------------------------

func TestRoundTrip_TagThenList(t *testing.T) {
	store := newMockTagStore("my-func")
	svc := capacity.New(store)

	doTagResource(t, svc, "my-func", map[string]string{"color": "blue"})
	w := doListTags(t, svc, "my-func")
	tags := decodeTags(t, w)
	if tags["color"] != "blue" {
		t.Errorf("expected color=blue after tag+list, got %v", tags)
	}
}

func TestRoundTrip_UntagThenList(t *testing.T) {
	store := newMockTagStore("my-func")
	store.SetTags("my-func", map[string]string{"x": "1", "y": "2"})
	svc := capacity.New(store)

	doUntagResource(t, svc, "my-func", []string{"x"})
	w := doListTags(t, svc, "my-func")
	tags := decodeTags(t, w)
	if _, ok := tags["x"]; ok {
		t.Error("key 'x' should be absent after untag")
	}
	if tags["y"] != "2" {
		t.Errorf("key 'y' should remain, got %v", tags)
	}
}
