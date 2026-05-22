package cloudwatchlogs

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newSvc() *Service { return New("us-east-1") }

func cwlReq(t *testing.T, svc *Service, action string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(body)
	req := httptest.NewRequest(http.MethodPost, "/", &buf)
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", "Logs_20140328."+action)
	w := httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	return w
}

func decode(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var v map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&v); err != nil {
		t.Fatalf("decode failed: %v\nbody: %s", err, w.Body.String())
	}
	return v
}

// --- Detect ---

func TestDetect(t *testing.T) {
	svc := newSvc()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-Amz-Target", "Logs_20140328.CreateLogGroup")
	if !svc.Detect(req) {
		t.Fatal("expected Detect=true for CloudWatch Logs target")
	}
}

func TestDetect_Other(t *testing.T) {
	svc := newSvc()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-Amz-Target", "AmazonSSM.GetParameter")
	if svc.Detect(req) {
		t.Fatal("expected Detect=false for non-CWL target")
	}
}

// --- Log groups ---

func TestCreateLogGroup(t *testing.T) {
	svc := newSvc()
	w := cwlReq(t, svc, "CreateLogGroup", map[string]string{"logGroupName": "/app/test"})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateLogGroup_Duplicate(t *testing.T) {
	svc := newSvc()
	cwlReq(t, svc, "CreateLogGroup", map[string]string{"logGroupName": "/app/test"})
	w := cwlReq(t, svc, "CreateLogGroup", map[string]string{"logGroupName": "/app/test"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for duplicate, got %d", w.Code)
	}
	body := decode(t, w)
	if body["__type"] != "ResourceAlreadyExistsException" {
		t.Errorf("expected ResourceAlreadyExistsException, got %v", body["__type"])
	}
}

func TestDeleteLogGroup(t *testing.T) {
	svc := newSvc()
	cwlReq(t, svc, "CreateLogGroup", map[string]string{"logGroupName": "/app/test"})
	w := cwlReq(t, svc, "DeleteLogGroup", map[string]string{"logGroupName": "/app/test"})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	// Confirm gone
	w2 := cwlReq(t, svc, "DeleteLogGroup", map[string]string{"logGroupName": "/app/test"})
	if w2.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 after deletion, got %d", w2.Code)
	}
}

func TestDescribeLogGroups(t *testing.T) {
	svc := newSvc()
	cwlReq(t, svc, "CreateLogGroup", map[string]string{"logGroupName": "/app/alpha"})
	cwlReq(t, svc, "CreateLogGroup", map[string]string{"logGroupName": "/app/beta"})
	cwlReq(t, svc, "CreateLogGroup", map[string]string{"logGroupName": "/other/group"})

	w := cwlReq(t, svc, "DescribeLogGroups", map[string]string{"logGroupNamePrefix": "/app/"})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := decode(t, w)
	groups := body["logGroups"].([]interface{})
	if len(groups) != 2 {
		t.Errorf("expected 2 groups with prefix /app/, got %d", len(groups))
	}
}

func TestDescribeLogGroups_All(t *testing.T) {
	svc := newSvc()
	cwlReq(t, svc, "CreateLogGroup", map[string]string{"logGroupName": "/a"})
	cwlReq(t, svc, "CreateLogGroup", map[string]string{"logGroupName": "/b"})
	w := cwlReq(t, svc, "DescribeLogGroups", map[string]interface{}{})
	body := decode(t, w)
	groups := body["logGroups"].([]interface{})
	if len(groups) != 2 {
		t.Errorf("expected 2 groups, got %d", len(groups))
	}
}

// --- Log streams ---

func TestCreateLogStream(t *testing.T) {
	svc := newSvc()
	cwlReq(t, svc, "CreateLogGroup", map[string]string{"logGroupName": "/app/test"})
	w := cwlReq(t, svc, "CreateLogStream", map[string]string{
		"logGroupName":  "/app/test",
		"logStreamName": "2024/01/01/container",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateLogStream_GroupNotFound(t *testing.T) {
	svc := newSvc()
	w := cwlReq(t, svc, "CreateLogStream", map[string]string{
		"logGroupName":  "/missing",
		"logStreamName": "stream",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	body := decode(t, w)
	if body["__type"] != "ResourceNotFoundException" {
		t.Errorf("expected ResourceNotFoundException, got %v", body["__type"])
	}
}

func TestCreateLogStream_Duplicate(t *testing.T) {
	svc := newSvc()
	cwlReq(t, svc, "CreateLogGroup", map[string]string{"logGroupName": "/app"})
	cwlReq(t, svc, "CreateLogStream", map[string]string{"logGroupName": "/app", "logStreamName": "s"})
	w := cwlReq(t, svc, "CreateLogStream", map[string]string{"logGroupName": "/app", "logStreamName": "s"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for duplicate stream, got %d", w.Code)
	}
}

func TestDescribeLogStreams(t *testing.T) {
	svc := newSvc()
	cwlReq(t, svc, "CreateLogGroup", map[string]string{"logGroupName": "/app"})
	cwlReq(t, svc, "CreateLogStream", map[string]string{"logGroupName": "/app", "logStreamName": "stream-a"})
	cwlReq(t, svc, "CreateLogStream", map[string]string{"logGroupName": "/app", "logStreamName": "stream-b"})
	cwlReq(t, svc, "CreateLogStream", map[string]string{"logGroupName": "/app", "logStreamName": "other"})

	w := cwlReq(t, svc, "DescribeLogStreams", map[string]string{
		"logGroupName":        "/app",
		"logStreamNamePrefix": "stream-",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := decode(t, w)
	streams := body["logStreams"].([]interface{})
	if len(streams) != 2 {
		t.Errorf("expected 2 streams with prefix 'stream-', got %d", len(streams))
	}
}

func TestDescribeLogStreams_GroupNotFound(t *testing.T) {
	svc := newSvc()
	w := cwlReq(t, svc, "DescribeLogStreams", map[string]string{"logGroupName": "/missing"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}
