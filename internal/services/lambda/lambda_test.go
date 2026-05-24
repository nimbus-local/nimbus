package lambda

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newSvc() *Service { return New("us-east-1") }

func do(t *testing.T, s *Service, method, path string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	return w
}

func TestName(t *testing.T) {
	s := newSvc()
	if s.Name() != "lambda" {
		t.Errorf("expected Name()=lambda, got %s", s.Name())
	}
}

func TestDetect(t *testing.T) {
	s := newSvc()
	cases := []struct {
		path string
		want bool
	}{
		{"/2015-03-31/functions", true},
		{"/2020-06-30/functions/fn/code-signing-config", true},
		{"/other", false},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		if got := s.Detect(req); got != tc.want {
			t.Errorf("Detect(%s) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestResolveFunctionName(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"my-func", "my-func"},
		{"my-func:prod", "my-func"},
		{"arn:aws:lambda:us-east-1:123456789012:function:my-func", "my-func"},
		{"arn:aws:lambda:us-east-1:123456789012:function:my-func:prod", "my-func"},
	}
	for _, c := range cases {
		got := resolveFunctionName(c.input)
		if got != c.want {
			t.Errorf("resolveFunctionName(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestServeHTTP_UnknownPath(t *testing.T) {
	s := newSvc()
	w := do(t, s, http.MethodGet, "/2015-03-31/unknown-resource", nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestServeHTTP_ListFunctions(t *testing.T) {
	s := newSvc()
	w := do(t, s, http.MethodGet, "/2015-03-31/functions", nil)
	if w.Code != http.StatusOK {
		t.Errorf("ListFunctions: expected 200, got %d", w.Code)
	}
}

func TestServeHTTP_CreateAndGetFunction(t *testing.T) {
	s := newSvc()

	createReq := map[string]interface{}{
		"FunctionName": "test-fn",
		"Runtime":      "python3.12",
		"Role":         "arn:aws:iam::000000000000:role/test",
		"Handler":      "index.handler",
		"Code":         map[string]string{"ZipFile": "UEsDBAoAAAAAAHJlY3QAAAAAAAAAAAAAAAAEAAAAdGVzdFBLAQIfAAoAAAAAAHJlY3QAAAAAAAAAAAAAAAAEAAAAdGVzdAAAAAAAAAAAAAAAAAAAAAAAAFBLBQYAAAAAAQABADIAAAAiAAAAAAA="},
	}
	w := do(t, s, http.MethodPost, "/2015-03-31/functions", createReq)
	if w.Code != http.StatusCreated {
		t.Fatalf("Create: expected 201, got %d\n%s", w.Code, w.Body.String())
	}

	w2 := do(t, s, http.MethodGet, "/2015-03-31/functions/test-fn", nil)
	if w2.Code != http.StatusOK {
		t.Errorf("Get: expected 200, got %d", w2.Code)
	}
}

func TestServeHTTP_AccountSettings(t *testing.T) {
	s := newSvc()
	w := do(t, s, http.MethodGet, "/2015-03-31/account-settings", nil)
	if w.Code != http.StatusOK {
		t.Errorf("AccountSettings: expected 200, got %d", w.Code)
	}
}

func TestServeHTTP_Tags(t *testing.T) {
	s := newSvc()

	// Create function first so tags can be looked up.
	do(t, s, http.MethodPost, "/2015-03-31/functions", map[string]interface{}{
		"FunctionName": "tag-fn",
		"Runtime":      "python3.12",
		"Role":         "arn:aws:iam::000000000000:role/test",
		"Handler":      "index.handler",
		"Code":         map[string]string{"ZipFile": "UEsDBAoAAAAAAHJlY3QAAAAAAAAAAAAAAAAEAAAAdGVzdFBLAQIfAAoAAAAAAHJlY3QAAAAAAAAAAAAAAAAEAAAAdGVzdAAAAAAAAAAAAAAAAAAAAAAAAFBLBQYAAAAAAQABADIAAAAiAAAAAAA="},
	})

	arn := "arn:aws:lambda:us-east-1:000000000000:function:tag-fn"
	w := do(t, s, http.MethodGet, "/2015-03-31/tags/"+arn, nil)
	if w.Code != http.StatusOK {
		t.Errorf("ListTags: expected 200, got %d\n%s", w.Code, w.Body.String())
	}
}

func TestServeHTTP_Layers(t *testing.T) {
	s := newSvc()
	w := do(t, s, http.MethodGet, "/2015-03-31/layers", nil)
	if w.Code != http.StatusOK {
		t.Errorf("ListLayers: expected 200, got %d", w.Code)
	}
}

func TestServeHTTP_EventSourceMappings(t *testing.T) {
	s := newSvc()
	w := do(t, s, http.MethodGet, "/2015-03-31/event-source-mappings", nil)
	if w.Code != http.StatusOK {
		t.Errorf("ListEventSourceMappings: expected 200, got %d", w.Code)
	}
}

func TestServeHTTP_CodeSigningConfigs(t *testing.T) {
	s := newSvc()
	w := do(t, s, http.MethodGet, "/2020-06-30/code-signing-configs", nil)
	if w.Code != http.StatusOK {
		t.Errorf("ListCodeSigningConfigs: expected 200, got %d", w.Code)
	}
}
