package s3

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// newTestService creates an S3 service backed by a temp directory
// that is automatically cleaned up after the test.
func newTestService(t *testing.T) (*Service, string) {
	t.Helper()
	dir := t.TempDir()
	svc := New(dir)
	// Pre-create the s3 subdirectory
	if err := os.MkdirAll(dir+"/s3", 0755); err != nil {
		t.Fatalf("failed to create s3 dir: %v", err)
	}
	return svc, dir
}

// do performs an HTTP request against the service and returns the response.
func do(t *testing.T, svc *Service, method, path string, body []byte, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, path, bodyReader)
	req.Host = "localhost:4566"
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	return w
}

// --- Bucket tests ---

func TestCreateBucket(t *testing.T) {
	svc, _ := newTestService(t)

	w := do(t, svc, http.MethodPut, "/test-bucket", nil, nil)
	if w.Code != http.StatusOK {
		t.Errorf("CreateBucket: expected 200, got %d\n%s", w.Code, w.Body.String())
	}
}

func TestCreateBucket_InvalidName(t *testing.T) {
	svc, _ := newTestService(t)

	// Too short
	w := do(t, svc, http.MethodPut, "/ab", nil, nil)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for short name, got %d", w.Code)
	}

	// Uppercase not allowed
	w = do(t, svc, http.MethodPut, "/MyBucket", nil, nil)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for uppercase name, got %d", w.Code)
	}
}

func TestCreateBucket_Idempotent(t *testing.T) {
	svc, _ := newTestService(t)

	// Creating the same bucket twice should both return 200
	do(t, svc, http.MethodPut, "/my-bucket", nil, nil)
	w := do(t, svc, http.MethodPut, "/my-bucket", nil, nil)
	if w.Code != http.StatusOK {
		t.Errorf("second CreateBucket: expected 200, got %d", w.Code)
	}
}

func TestHeadBucket_Exists(t *testing.T) {
	svc, _ := newTestService(t)
	do(t, svc, http.MethodPut, "/my-bucket", nil, nil)

	w := do(t, svc, http.MethodHead, "/my-bucket", nil, nil)
	if w.Code != http.StatusOK {
		t.Errorf("HeadBucket: expected 200, got %d", w.Code)
	}
}

func TestHeadBucket_NotExists(t *testing.T) {
	svc, _ := newTestService(t)

	w := do(t, svc, http.MethodHead, "/no-such-bucket", nil, nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("HeadBucket missing: expected 404, got %d", w.Code)
	}
}

func TestDeleteBucket(t *testing.T) {
	svc, _ := newTestService(t)
	do(t, svc, http.MethodPut, "/my-bucket", nil, nil)

	w := do(t, svc, http.MethodDelete, "/my-bucket", nil, nil)
	if w.Code != http.StatusNoContent {
		t.Errorf("DeleteBucket: expected 204, got %d\n%s", w.Code, w.Body.String())
	}

	// Bucket should no longer exist
	w = do(t, svc, http.MethodHead, "/my-bucket", nil, nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("after delete: expected 404, got %d", w.Code)
	}
}

func TestDeleteBucket_NotEmpty(t *testing.T) {
	svc, _ := newTestService(t)
	do(t, svc, http.MethodPut, "/my-bucket", nil, nil)
	do(t, svc, http.MethodPut, "/my-bucket/file.txt", []byte("hello"), nil)

	w := do(t, svc, http.MethodDelete, "/my-bucket", nil, nil)
	if w.Code != http.StatusConflict {
		t.Errorf("DeleteBucket non-empty: expected 409, got %d", w.Code)
	}
}

func TestListBuckets(t *testing.T) {
	svc, _ := newTestService(t)
	do(t, svc, http.MethodPut, "/bucket-a", nil, nil)
	do(t, svc, http.MethodPut, "/bucket-b", nil, nil)

	w := do(t, svc, http.MethodGet, "/", nil, nil)
	if w.Code != http.StatusOK {
		t.Errorf("ListBuckets: expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "bucket-a") {
		t.Errorf("expected bucket-a in response: %s", body)
	}
	if !strings.Contains(body, "bucket-b") {
		t.Errorf("expected bucket-b in response: %s", body)
	}
}

// --- Object tests ---

func TestPutAndGetObject(t *testing.T) {
	svc, _ := newTestService(t)
	do(t, svc, http.MethodPut, "/my-bucket", nil, nil)

	content := []byte("hello nimbus")
	w := do(t, svc, http.MethodPut, "/my-bucket/hello.txt", content,
		map[string]string{"Content-Type": "text/plain"})
	if w.Code != http.StatusOK {
		t.Fatalf("PutObject: expected 200, got %d", w.Code)
	}
	if w.Header().Get("ETag") == "" {
		t.Error("PutObject: expected ETag header")
	}

	w = do(t, svc, http.MethodGet, "/my-bucket/hello.txt", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GetObject: expected 200, got %d", w.Code)
	}
	if !bytes.Equal(w.Body.Bytes(), content) {
		t.Errorf("GetObject: expected %q, got %q", content, w.Body.Bytes())
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/plain" {
		t.Errorf("GetObject: expected Content-Type text/plain, got %q", ct)
	}
}

func TestGetObject_NotFound(t *testing.T) {
	svc, _ := newTestService(t)
	do(t, svc, http.MethodPut, "/my-bucket", nil, nil)

	w := do(t, svc, http.MethodGet, "/my-bucket/nope.txt", nil, nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("GetObject missing: expected 404, got %d", w.Code)
	}

	// Response must be XML with NoSuchKey error code
	if !strings.Contains(w.Body.String(), "NoSuchKey") {
		t.Errorf("expected NoSuchKey in body: %s", w.Body.String())
	}
}

func TestGetObject_NoBucket(t *testing.T) {
	svc, _ := newTestService(t)

	w := do(t, svc, http.MethodGet, "/no-bucket/key.txt", nil, nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("GetObject no bucket: expected 404, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "NoSuchBucket") {
		t.Errorf("expected NoSuchBucket in body: %s", w.Body.String())
	}
}

func TestHeadObject(t *testing.T) {
	svc, _ := newTestService(t)
	do(t, svc, http.MethodPut, "/my-bucket", nil, nil)
	do(t, svc, http.MethodPut, "/my-bucket/file.txt", []byte("data"), nil)

	w := do(t, svc, http.MethodHead, "/my-bucket/file.txt", nil, nil)
	if w.Code != http.StatusOK {
		t.Errorf("HeadObject: expected 200, got %d", w.Code)
	}
	if w.Header().Get("Content-Length") == "" {
		t.Error("HeadObject: expected Content-Length header")
	}
	// HEAD must not return a body
	if w.Body.Len() != 0 {
		t.Errorf("HeadObject: expected empty body, got %d bytes", w.Body.Len())
	}
}

func TestDeleteObject(t *testing.T) {
	svc, _ := newTestService(t)
	do(t, svc, http.MethodPut, "/my-bucket", nil, nil)
	do(t, svc, http.MethodPut, "/my-bucket/bye.txt", []byte("bye"), nil)

	w := do(t, svc, http.MethodDelete, "/my-bucket/bye.txt", nil, nil)
	if w.Code != http.StatusNoContent {
		t.Errorf("DeleteObject: expected 204, got %d", w.Code)
	}

	w = do(t, svc, http.MethodGet, "/my-bucket/bye.txt", nil, nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("after delete: expected 404, got %d", w.Code)
	}
}

func TestDeleteObject_Idempotent(t *testing.T) {
	svc, _ := newTestService(t)
	do(t, svc, http.MethodPut, "/my-bucket", nil, nil)

	// Deleting a non-existent key should still return 204, like real AWS
	w := do(t, svc, http.MethodDelete, "/my-bucket/ghost.txt", nil, nil)
	if w.Code != http.StatusNoContent {
		t.Errorf("DeleteObject missing: expected 204, got %d", w.Code)
	}
}

func TestPutObject_UserMetadata(t *testing.T) {
	svc, _ := newTestService(t)
	do(t, svc, http.MethodPut, "/my-bucket", nil, nil)

	do(t, svc, http.MethodPut, "/my-bucket/tagged.txt", []byte("data"),
		map[string]string{
			"x-amz-meta-author": "nimbus",
			"x-amz-meta-env":    "test",
		})

	w := do(t, svc, http.MethodGet, "/my-bucket/tagged.txt", nil, nil)
	if w.Header().Get("x-amz-meta-author") != "nimbus" {
		t.Errorf("expected x-amz-meta-author=nimbus, got %q",
			w.Header().Get("x-amz-meta-author"))
	}
}

// --- ListObjectsV2 tests ---

func TestListObjects(t *testing.T) {
	svc, _ := newTestService(t)
	do(t, svc, http.MethodPut, "/my-bucket", nil, nil)
	do(t, svc, http.MethodPut, "/my-bucket/a.txt", []byte("a"), nil)
	do(t, svc, http.MethodPut, "/my-bucket/b.txt", []byte("b"), nil)
	do(t, svc, http.MethodPut, "/my-bucket/c.txt", []byte("c"), nil)

	w := do(t, svc, http.MethodGet, "/my-bucket", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("ListObjects: expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	for _, key := range []string{"a.txt", "b.txt", "c.txt"} {
		if !strings.Contains(body, key) {
			t.Errorf("expected %s in list response", key)
		}
	}
}

func TestListObjects_WithPrefix(t *testing.T) {
	svc, _ := newTestService(t)
	do(t, svc, http.MethodPut, "/my-bucket", nil, nil)
	do(t, svc, http.MethodPut, "/my-bucket/images/cat.png", []byte("cat"), nil)
	do(t, svc, http.MethodPut, "/my-bucket/images/dog.png", []byte("dog"), nil)
	do(t, svc, http.MethodPut, "/my-bucket/docs/readme.txt", []byte("readme"), nil)

	w := do(t, svc, http.MethodGet, "/my-bucket?prefix=images/", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("ListObjects prefix: expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "cat.png") {
		t.Errorf("expected cat.png in response")
	}
	if !strings.Contains(body, "dog.png") {
		t.Errorf("expected dog.png in response")
	}
	if strings.Contains(body, "readme.txt") {
		t.Errorf("did not expect readme.txt in response")
	}
}

func TestListObjects_WithDelimiter(t *testing.T) {
	svc, _ := newTestService(t)
	do(t, svc, http.MethodPut, "/my-bucket", nil, nil)
	do(t, svc, http.MethodPut, "/my-bucket/folder/file1.txt", []byte("1"), nil)
	do(t, svc, http.MethodPut, "/my-bucket/folder/file2.txt", []byte("2"), nil)
	do(t, svc, http.MethodPut, "/my-bucket/root.txt", []byte("root"), nil)

	w := do(t, svc, http.MethodGet, "/my-bucket?delimiter=/", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("ListObjects delimiter: expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	// folder/ should appear as a CommonPrefix, not as individual objects
	if !strings.Contains(body, "folder/") {
		t.Errorf("expected folder/ as CommonPrefix: %s", body)
	}
	if !strings.Contains(body, "root.txt") {
		t.Errorf("expected root.txt in response: %s", body)
	}
}

// --- Batch delete tests ---

func TestDeleteObjects(t *testing.T) {
	svc, _ := newTestService(t)
	do(t, svc, http.MethodPut, "/my-bucket", nil, nil)
	do(t, svc, http.MethodPut, "/my-bucket/a.txt", []byte("a"), nil)
	do(t, svc, http.MethodPut, "/my-bucket/b.txt", []byte("b"), nil)

	deleteBody := `<?xml version="1.0"?>
<Delete>
  <Object><Key>a.txt</Key></Object>
  <Object><Key>b.txt</Key></Object>
</Delete>`

	req := httptest.NewRequest(http.MethodPost, "/my-bucket?delete",
		strings.NewReader(deleteBody))
	req.Host = "localhost:4566"
	w := httptest.NewRecorder()
	svc.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("DeleteObjects: expected 200, got %d\n%s", w.Code, w.Body.String())
	}

	// Both objects should be gone
	for _, key := range []string{"a.txt", "b.txt"} {
		check := do(t, svc, http.MethodGet, "/my-bucket/"+key, nil, nil)
		if check.Code != http.StatusNotFound {
			t.Errorf("after batch delete, %s should be 404, got %d", key, check.Code)
		}
	}
}

// --- Multipart upload tests ---

func TestMultipartUpload(t *testing.T) {
	svc, _ := newTestService(t)
	do(t, svc, http.MethodPut, "/my-bucket", nil, nil)

	// 1. Initiate
	w := do(t, svc, http.MethodPost, "/my-bucket/big-file.bin?uploads", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("CreateMultipartUpload: expected 200, got %d", w.Code)
	}

	var initResp struct {
		UploadId string `xml:"UploadId"`
	}
	if err := xml.Unmarshal(w.Body.Bytes(), &initResp); err != nil {
		t.Fatalf("failed to parse CreateMultipartUpload response: %v", err)
	}
	uploadID := initResp.UploadId
	if uploadID == "" {
		t.Fatal("expected non-empty UploadId")
	}

	// 2. Upload parts
	part1 := bytes.Repeat([]byte("A"), 512)
	part2 := bytes.Repeat([]byte("B"), 512)

	for i, part := range [][]byte{part1, part2} {
		path := fmt.Sprintf("/my-bucket/big-file.bin?partNumber=%d&uploadId=%s", i+1, uploadID)
		pw := do(t, svc, http.MethodPut, path, part, nil)
		if pw.Code != http.StatusOK {
			t.Fatalf("UploadPart %d: expected 200, got %d", i+1, pw.Code)
		}
	}

	// 3. Complete
	completeBody := fmt.Sprintf(`<?xml version="1.0"?>
<CompleteMultipartUpload>
  <Part><PartNumber>1</PartNumber><ETag>"part1"</ETag></Part>
  <Part><PartNumber>2</PartNumber><ETag>"part2"</ETag></Part>
</CompleteMultipartUpload>`)

	req := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/my-bucket/big-file.bin?uploadId=%s", uploadID),
		strings.NewReader(completeBody))
	req.Host = "localhost:4566"
	cw := httptest.NewRecorder()
	svc.ServeHTTP(cw, req)

	if cw.Code != http.StatusOK {
		t.Fatalf("CompleteMultipartUpload: expected 200, got %d\n%s", cw.Code, cw.Body.String())
	}

	// 4. Verify the assembled object
	gw := do(t, svc, http.MethodGet, "/my-bucket/big-file.bin", nil, nil)
	if gw.Code != http.StatusOK {
		t.Fatalf("GetObject after multipart: expected 200, got %d", gw.Code)
	}

	expected := append(part1, part2...)
	if !bytes.Equal(gw.Body.Bytes(), expected) {
		t.Errorf("assembled object content mismatch: got %d bytes, expected %d",
			gw.Body.Len(), len(expected))
	}
}

func TestAbortMultipartUpload(t *testing.T) {
	svc, _ := newTestService(t)
	do(t, svc, http.MethodPut, "/my-bucket", nil, nil)

	w := do(t, svc, http.MethodPost, "/my-bucket/file.bin?uploads", nil, nil)
	var initResp struct {
		UploadId string `xml:"UploadId"`
	}
	xml.Unmarshal(w.Body.Bytes(), &initResp)

	path := fmt.Sprintf("/my-bucket/file.bin?uploadId=%s", initResp.UploadId)
	aw := do(t, svc, http.MethodDelete, path, nil, nil)
	if aw.Code != http.StatusNoContent {
		t.Errorf("AbortMultipartUpload: expected 204, got %d", aw.Code)
	}
}

// --- Service detection tests ---

func TestDetect(t *testing.T) {
	svc := &Service{}

	cases := []struct {
		name     string
		host     string
		target   string
		action   string
		expected bool
	}{
		{"plain localhost", "localhost:4566", "", "", true},
		{"s3 host prefix", "s3.us-east-1.amazonaws.com", "", "", true},
		{"virtual hosted", "my-bucket.s3.localhost:4566", "", "", true},
		{"dynamodb target - not s3", "localhost:4566", "DynamoDB_20120810.ListTables", "", false},
		{"sqs action - not s3", "localhost:4566", "", "CreateQueue", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Host = tc.host
			if tc.target != "" {
				req.Header.Set("X-Amz-Target", tc.target)
			}
			if tc.action != "" {
				q := req.URL.Query()
				q.Set("Action", tc.action)
				req.URL.RawQuery = q.Encode()
			}

			got := svc.Detect(req)
			if got != tc.expected {
				t.Errorf("Detect(%q): expected %v, got %v", tc.name, tc.expected, got)
			}
		})
	}
}
