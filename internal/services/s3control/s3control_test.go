package s3control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func svc() *Service { return New() }

func do(t *testing.T, s *Service, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	return w
}

// --- Detect ---

func TestDetect_AccountIDHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("x-amz-account-id", "000000000000")
	if !svc().Detect(req) {
		t.Fatal("expected Detect=true for x-amz-account-id header")
	}
}

func TestDetect_PathPrefix(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v20180820/configuration/publicAccessBlock", nil)
	if !svc().Detect(req) {
		t.Fatal("expected Detect=true for /v20180820/ path")
	}
}

func TestDetect_Miss(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/other", nil)
	if svc().Detect(req) {
		t.Fatal("expected Detect=false for unrelated path")
	}
}

// --- Tags ---

func TestTags_Get(t *testing.T) {
	w := do(t, svc(), http.MethodGet, "/v20180820/tags/arn:aws:s3:::my-bucket")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := body["Tags"]; !ok {
		t.Error("expected Tags key in response")
	}
}

func TestTags_Put(t *testing.T) {
	w := do(t, svc(), http.MethodPut, "/v20180820/tags/arn:aws:s3:::my-bucket")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestTags_Delete(t *testing.T) {
	w := do(t, svc(), http.MethodDelete, "/v20180820/tags/arn:aws:s3:::my-bucket")
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}
}

func TestTags_MethodNotAllowed(t *testing.T) {
	w := do(t, svc(), http.MethodPost, "/v20180820/tags/arn:aws:s3:::my-bucket")
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

// --- Public access block ---

func TestPublicAccessBlock_Get(t *testing.T) {
	w := do(t, svc(), http.MethodGet, "/v20180820/configuration/publicAccessBlock")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	cfg, ok := body["PublicAccessBlockConfiguration"].(map[string]interface{})
	if !ok {
		t.Fatal("expected PublicAccessBlockConfiguration object")
	}
	if cfg["BlockPublicAcls"] != true {
		t.Error("expected BlockPublicAcls=true")
	}
}

func TestPublicAccessBlock_Put(t *testing.T) {
	w := do(t, svc(), http.MethodPut, "/v20180820/configuration/publicAccessBlock")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestPublicAccessBlock_Delete(t *testing.T) {
	w := do(t, svc(), http.MethodDelete, "/v20180820/configuration/publicAccessBlock")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestPublicAccessBlock_MethodNotAllowed(t *testing.T) {
	w := do(t, svc(), http.MethodPost, "/v20180820/configuration/publicAccessBlock")
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

// --- Catch-all ---

func TestCatchAll(t *testing.T) {
	w := do(t, svc(), http.MethodGet, "/v20180820/bucket/my-bucket/policy")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for catch-all, got %d", w.Code)
	}
}
