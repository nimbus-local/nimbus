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
		{"virtual hosted s3", "my-bucket.s3.localhost:4566", "", "", true},
		{"virtual hosted bucket.localhost", "my-bucket.localhost:4566", "", "", true},
		{"virtual hosted bucket.127.0.0.1", "my-bucket.127.0.0.1:4566", "", "", true},
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

// --- CopyObject tests ---

func TestCopyObject(t *testing.T) {
	svc, _ := newTestService(t)
	do(t, svc, http.MethodPut, "/src-bucket", nil, nil)
	do(t, svc, http.MethodPut, "/dst-bucket", nil, nil)

	content := []byte("copy me")
	do(t, svc, http.MethodPut, "/src-bucket/original.txt", content,
		map[string]string{"Content-Type": "text/plain"})

	// Copy to a different bucket and key
	w := do(t, svc, http.MethodPut, "/dst-bucket/copy.txt", nil,
		map[string]string{"x-amz-copy-source": "/src-bucket/original.txt"})
	if w.Code != http.StatusOK {
		t.Fatalf("CopyObject: expected 200, got %d\n%s", w.Code, w.Body.String())
	}

	// Response must include CopyObjectResult XML with ETag and LastModified
	body := w.Body.String()
	if !strings.Contains(body, "CopyObjectResult") {
		t.Errorf("CopyObject: expected CopyObjectResult in body: %s", body)
	}
	if !strings.Contains(body, "ETag") {
		t.Errorf("CopyObject: expected ETag in body: %s", body)
	}
	if !strings.Contains(body, "LastModified") {
		t.Errorf("CopyObject: expected LastModified in body: %s", body)
	}

	// Destination object must be retrievable and have the same content
	gw := do(t, svc, http.MethodGet, "/dst-bucket/copy.txt", nil, nil)
	if gw.Code != http.StatusOK {
		t.Fatalf("GetObject after copy: expected 200, got %d", gw.Code)
	}
	if !bytes.Equal(gw.Body.Bytes(), content) {
		t.Errorf("CopyObject: destination content mismatch: got %q, want %q",
			gw.Body.Bytes(), content)
	}
	if ct := gw.Header().Get("Content-Type"); ct != "text/plain" {
		t.Errorf("CopyObject: expected Content-Type preserved as text/plain, got %q", ct)
	}
}

func TestCopyObject_WithinBucket(t *testing.T) {
	svc, _ := newTestService(t)
	do(t, svc, http.MethodPut, "/my-bucket", nil, nil)
	do(t, svc, http.MethodPut, "/my-bucket/orig.json", []byte(`{"k":"v"}`),
		map[string]string{"Content-Type": "application/json"})

	w := do(t, svc, http.MethodPut, "/my-bucket/archive/orig.json", nil,
		map[string]string{"x-amz-copy-source": "/my-bucket/orig.json"})
	if w.Code != http.StatusOK {
		t.Fatalf("CopyObject within bucket: expected 200, got %d\n%s", w.Code, w.Body.String())
	}

	gw := do(t, svc, http.MethodGet, "/my-bucket/archive/orig.json", nil, nil)
	if gw.Code != http.StatusOK {
		t.Fatalf("GetObject after within-bucket copy: expected 200, got %d", gw.Code)
	}
	if gw.Body.String() != `{"k":"v"}` {
		t.Errorf("within-bucket copy: content mismatch: %s", gw.Body.String())
	}
}

func TestCopyObject_SourceNotFound(t *testing.T) {
	svc, _ := newTestService(t)
	do(t, svc, http.MethodPut, "/my-bucket", nil, nil)

	w := do(t, svc, http.MethodPut, "/my-bucket/dest.txt", nil,
		map[string]string{"x-amz-copy-source": "/my-bucket/no-such-key.txt"})
	if w.Code != http.StatusNotFound {
		t.Errorf("CopyObject missing source key: expected 404, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "NoSuchKey") {
		t.Errorf("expected NoSuchKey error: %s", w.Body.String())
	}
}

func TestCopyObject_SourceBucketNotFound(t *testing.T) {
	svc, _ := newTestService(t)
	do(t, svc, http.MethodPut, "/dst-bucket", nil, nil)

	w := do(t, svc, http.MethodPut, "/dst-bucket/dest.txt", nil,
		map[string]string{"x-amz-copy-source": "/no-such-bucket/key.txt"})
	if w.Code != http.StatusNotFound {
		t.Errorf("CopyObject missing source bucket: expected 404, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "NoSuchBucket") {
		t.Errorf("expected NoSuchBucket error: %s", w.Body.String())
	}
}

func TestCopyObject_URLEncodedSource(t *testing.T) {
	svc, _ := newTestService(t)
	do(t, svc, http.MethodPut, "/my-bucket", nil, nil)
	do(t, svc, http.MethodPut, "/my-bucket/path/to/file.txt", []byte("encoded"), nil)

	// gocloud.dev sends URL-encoded copy-source
	w := do(t, svc, http.MethodPut, "/my-bucket/dest.txt", nil,
		map[string]string{"x-amz-copy-source": "/my-bucket/path%2Fto%2Ffile.txt"})
	if w.Code != http.StatusOK {
		t.Fatalf("CopyObject URL-encoded source: expected 200, got %d\n%s", w.Code, w.Body.String())
	}
	gw := do(t, svc, http.MethodGet, "/my-bucket/dest.txt", nil, nil)
	if gw.Body.String() != "encoded" {
		t.Errorf("URL-encoded copy: content mismatch: %s", gw.Body.String())
	}
}

func TestVirtualHostedBucketLocalhost(t *testing.T) {
	svc, _ := newTestService(t)

	// CreateBucket via virtual-hosted style: bucket.localhost:4566
	req := httptest.NewRequest(http.MethodPut, "/", nil)
	req.Host = "test-bucket.localhost:4566"
	w := httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("CreateBucket virtual-hosted: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// PutObject
	req = httptest.NewRequest(http.MethodPut, "/mykey", strings.NewReader("hello"))
	req.Host = "test-bucket.localhost:4566"
	w = httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PutObject virtual-hosted: expected 200, got %d", w.Code)
	}

	// GetObject
	req = httptest.NewRequest(http.MethodGet, "/mykey", nil)
	req.Host = "test-bucket.localhost:4566"
	w = httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetObject virtual-hosted: expected 200, got %d", w.Code)
	}
	if got := w.Body.String(); got != "hello" {
		t.Errorf("GetObject virtual-hosted: expected body %q, got %q", "hello", got)
	}
}

// --- Lifecycle tests ---

const sampleLifecycleXML = `<?xml version="1.0" encoding="UTF-8"?>
<LifecycleConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <Rule>
    <ID>expire-old</ID>
    <Status>Enabled</Status>
    <Expiration><Days>90</Days></Expiration>
  </Rule>
</LifecycleConfiguration>`

func TestPutGetBucketLifecycle(t *testing.T) {
	svc, _ := newTestService(t)
	do(t, svc, http.MethodPut, "/lc-bucket", nil, nil)

	// PUT lifecycle
	w := do(t, svc, http.MethodPut, "/lc-bucket?lifecycle",
		[]byte(sampleLifecycleXML),
		map[string]string{"Content-Type": "application/xml"})
	if w.Code != http.StatusOK {
		t.Fatalf("PutBucketLifecycle: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// GET lifecycle — must return the same XML and the Pulumi waiter header.
	w = do(t, svc, http.MethodGet, "/lc-bucket?lifecycle", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GetBucketLifecycle: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != sampleLifecycleXML {
		t.Errorf("GetBucketLifecycle: body mismatch\ngot:  %q\nwant: %q", got, sampleLifecycleXML)
	}
	// The Pulumi/TF AWS provider v5.44+ waiter reads TransitionDefaultMinimumObjectSize
	// from this response header. It must be present or deploys time out after 3 minutes.
	if got := w.Header().Get("x-amz-transition-default-minimum-object-size"); got == "" {
		t.Error("GetBucketLifecycle: missing x-amz-transition-default-minimum-object-size header")
	}
}

func TestGetBucketLifecycle_NotFound(t *testing.T) {
	svc, _ := newTestService(t)
	do(t, svc, http.MethodPut, "/lc-bucket2", nil, nil)

	w := do(t, svc, http.MethodGet, "/lc-bucket2?lifecycle", nil, nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("GetBucketLifecycle (no config): expected 404, got %d", w.Code)
	}
}

func TestDeleteBucketLifecycle(t *testing.T) {
	svc, _ := newTestService(t)
	do(t, svc, http.MethodPut, "/lc-bucket3", nil, nil)

	do(t, svc, http.MethodPut, "/lc-bucket3?lifecycle",
		[]byte(sampleLifecycleXML),
		map[string]string{"Content-Type": "application/xml"})

	w := do(t, svc, http.MethodDelete, "/lc-bucket3?lifecycle", nil, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("DeleteBucketLifecycle: expected 204, got %d", w.Code)
	}

	// GET after delete must return 404
	w = do(t, svc, http.MethodGet, "/lc-bucket3?lifecycle", nil, nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("GetBucketLifecycle after delete: expected 404, got %d", w.Code)
	}
}

// TestReservedKeyBlocked verifies that .nimbus-* keys are blocked at the object API level
// so internal sidecar files (lifecycle config, bucket metadata) are never exposed.
func TestReservedKeyBlocked(t *testing.T) {
	svc, _ := newTestService(t)
	do(t, svc, http.MethodPut, "/rk-bucket", nil, nil)

	// Seed lifecycle so .nimbus-lifecycle.xml exists on disk
	do(t, svc, http.MethodPut, "/rk-bucket?lifecycle",
		[]byte(sampleLifecycleXML),
		map[string]string{"Content-Type": "application/xml"})

	reservedKeys := []string{".nimbus-lifecycle.xml", ".nimbus-bucket.json", ".nimbus-anything"}

	for _, key := range reservedKeys {
		// GET must return 404 NoSuchKey
		w := do(t, svc, http.MethodGet, "/rk-bucket/"+key, nil, nil)
		if w.Code != http.StatusNotFound {
			t.Errorf("GET %s: expected 404, got %d", key, w.Code)
		}

		// HEAD must return 404
		w = do(t, svc, http.MethodHead, "/rk-bucket/"+key, nil, nil)
		if w.Code != http.StatusNotFound {
			t.Errorf("HEAD %s: expected 404, got %d", key, w.Code)
		}

		// PUT must return 400
		w = do(t, svc, http.MethodPut, "/rk-bucket/"+key, []byte("data"), nil)
		if w.Code != http.StatusBadRequest {
			t.Errorf("PUT %s: expected 400, got %d", key, w.Code)
		}

		// DELETE must return 204 without touching the real file
		w = do(t, svc, http.MethodDelete, "/rk-bucket/"+key, nil, nil)
		if w.Code != http.StatusNoContent {
			t.Errorf("DELETE %s: expected 204, got %d", key, w.Code)
		}
	}

	// After DELETE attempts, lifecycle config must still be intact
	w := do(t, svc, http.MethodGet, "/rk-bucket?lifecycle", nil, nil)
	if w.Code != http.StatusOK {
		t.Errorf("lifecycle config should survive DELETE of reserved key, got %d", w.Code)
	}

	// Reserved keys must not appear in ListObjectsV2
	w = do(t, svc, http.MethodGet, "/rk-bucket?list-type=2", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("ListObjectsV2: expected 200, got %d", w.Code)
	}
	for _, key := range reservedKeys {
		if strings.Contains(w.Body.String(), key) {
			t.Errorf("ListObjectsV2 should not include reserved key %q", key)
		}
	}
}

// TestDeleteBucketSubResourceNoOp verifies that DELETE requests for unsupported bucket
// sub-resources (publicAccessBlock, encryption, etc.) return 204 without deleting the bucket.
func TestDeleteBucketSubResourceNoOp(t *testing.T) {
	svc, _ := newTestService(t)
	do(t, svc, http.MethodPut, "/sub-bucket", nil, nil)

	for _, query := range []string{"?publicAccessBlock", "?encryption", "?versioning", "?cors"} {
		w := do(t, svc, http.MethodDelete, "/sub-bucket"+query, nil, nil)
		if w.Code != http.StatusNoContent {
			t.Errorf("DELETE %s: expected 204, got %d", query, w.Code)
		}
		// Bucket must still exist
		w = do(t, svc, http.MethodHead, "/sub-bucket", nil, nil)
		if w.Code != http.StatusOK {
			t.Errorf("HEAD after DELETE %s: bucket should still exist, got %d", query, w.Code)
		}
	}
}

// TestDeleteBucketWithLifecycleNotEmpty verifies that bucket deletion fails if lifecycle
// config exists (lifecycle should be deleted first by Terraform/Pulumi dependency ordering).
func TestDeleteBucketEmptyIgnoresNimbusMetadata(t *testing.T) {
	svc, _ := newTestService(t)
	do(t, svc, http.MethodPut, "/meta-bucket", nil, nil)
	do(t, svc, http.MethodPut, "/meta-bucket?lifecycle",
		[]byte(sampleLifecycleXML),
		map[string]string{"Content-Type": "application/xml"})

	// Delete lifecycle first, then bucket — should succeed
	do(t, svc, http.MethodDelete, "/meta-bucket?lifecycle", nil, nil)
	w := do(t, svc, http.MethodDelete, "/meta-bucket", nil, nil)
	if w.Code != http.StatusNoContent {
		t.Errorf("DeleteBucket after lifecycle removed: expected 204, got %d: %s", w.Code, w.Body.String())
	}
}
