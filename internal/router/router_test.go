package router

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// mockService records whether it was called, for testing dispatch
type mockService struct {
	name    string
	detect  func(r *http.Request) bool
	called  bool
}

func (m *mockService) Name() string { return m.name }
func (m *mockService) Detect(r *http.Request) bool { return m.detect(r) }
func (m *mockService) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.called = true
	w.WriteHeader(http.StatusOK)
}

func newTestRouter() *Router {
	return New(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelError, // suppress output during tests
	})))
}

func TestRouter_DispatchesToCorrectService(t *testing.T) {
	r := newTestRouter()

	dynamo := &mockService{name: "dynamodb", detect: func(r *http.Request) bool {
		return r.Header.Get("X-Amz-Target") != ""
	}}
	s3 := &mockService{name: "s3", detect: func(r *http.Request) bool {
		return true // catch-all
	}}

	r.Register(dynamo)
	r.Register(s3)

	t.Run("DynamoDB request routes to dynamodb", func(t *testing.T) {
		dynamo.called = false
		s3.called = false

		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Set("X-Amz-Target", "DynamoDB_20120810.ListTables")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if !dynamo.called {
			t.Error("expected dynamodb service to be called")
		}
		if s3.called {
			t.Error("expected s3 service NOT to be called")
		}
	})

	t.Run("Fallthrough routes to S3 catch-all", func(t *testing.T) {
		dynamo.called = false
		s3.called = false

		req := httptest.NewRequest(http.MethodGet, "/my-bucket", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if dynamo.called {
			t.Error("expected dynamodb service NOT to be called")
		}
		if !s3.called {
			t.Error("expected s3 service to be called")
		}
	})
}

func TestRouter_Returns400WhenNoServiceMatches(t *testing.T) {
	r := newTestRouter()

	// Register a service that never matches
	r.Register(&mockService{name: "never", detect: func(r *http.Request) bool {
		return false
	}})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestRouter_HealthHandler(t *testing.T) {
	r := newTestRouter()
	r.Register(&mockService{name: "s3", detect: func(*http.Request) bool { return false }})
	r.Register(&mockService{name: "sqs", detect: func(*http.Request) bool { return false }})

	req := httptest.NewRequest(http.MethodGet, "/_nimbus/health", nil)
	w := httptest.NewRecorder()
	r.HealthHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if body == "" {
		t.Error("expected non-empty health response body")
	}
	for _, svc := range []string{"s3", "sqs"} {
		if !contains(body, svc) {
			t.Errorf("expected health response to contain %q, got: %s", svc, body)
		}
	}
}

func TestRouter_SetsRequestIDHeaders(t *testing.T) {
	r := newTestRouter()
	r.Register(&mockService{name: "s3", detect: func(*http.Request) bool { return true }})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Header().Get("x-amz-request-id") == "" {
		t.Error("expected x-amz-request-id header to be set")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr ||
		len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
