package s3

import (
	"crypto/md5"
	"encoding/xml"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const xmlHeader = `<?xml version="1.0" encoding="UTF-8"?>`

// Service implements the S3 emulator. Storage is filesystem-backed:
// buckets are directories under DataDir/s3/, objects are files,
// metadata is stored as JSON sidecar files (.nimbus-meta/<key>.json).
type Service struct {
	dataDir string
}

func New(dataDir string) *Service {
	return &Service{dataDir: dataDir + "/s3"}
}

func (s *Service) Name() string { return "s3" }

// Detect identifies S3 requests by:
// 1. Virtual-hosted style: bucket.s3*.amazonaws.com or bucket.localhost
// 2. Path style: host is s3* or localhost/127.0.0.1 with no X-Amz-Target
// 3. s3. prefix in host
func (s *Service) Detect(r *http.Request) bool {
	host := strings.ToLower(r.Host)
	// Strip port
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}

	// Explicit S3 host patterns
	if strings.HasPrefix(host, "s3") {
		return true
	}
	// Virtual hosted: bucket.s3.localhost or bucket.localhost
	if strings.Contains(host, ".s3.") {
		return true
	}

	// SQS uses numeric account ID in path and Action params
	// DynamoDB uses X-Amz-Target header
	// Everything else on localhost with no target header = S3
	if r.Header.Get("X-Amz-Target") != "" {
		return false
	}
	if r.URL.Query().Get("Action") != "" {
		return false
	}

	return host == "localhost" || host == "127.0.0.1" || host == "0.0.0.0"
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	bucket, key := parsePath(r)

	switch {
	case r.Method == http.MethodGet && bucket == "" && key == "":
		s.listBuckets(w, r)

	case r.Method == http.MethodPut && bucket != "" && key == "":
		s.createBucket(w, r, bucket)

	case r.Method == http.MethodDelete && bucket != "" && key == "":
		s.deleteBucket(w, r, bucket)

	case r.Method == http.MethodHead && bucket != "" && key == "":
		s.headBucket(w, r, bucket)

	case r.Method == http.MethodGet && bucket != "" && key == "":
		s.listObjects(w, r, bucket)

	case r.Method == http.MethodPost && bucket != "" && key == "" && r.URL.Query().Has("delete"):
		s.deleteObjects(w, r, bucket)

	case r.Method == http.MethodPut && bucket != "" && key != "" && r.URL.Query().Has("partNumber"):
		s.uploadPart(w, r, bucket, key)

	case r.Method == http.MethodPost && bucket != "" && key != "" && r.URL.Query().Has("uploads"):
		s.createMultipartUpload(w, r, bucket, key)

	case r.Method == http.MethodPost && bucket != "" && key != "" && r.URL.Query().Has("uploadId"):
		s.completeMultipartUpload(w, r, bucket, key)

	case r.Method == http.MethodDelete && bucket != "" && key != "" && r.URL.Query().Has("uploadId"):
		s.abortMultipartUpload(w, r, bucket, key)

	case r.Method == http.MethodPut && bucket != "" && key != "":
		s.putObject(w, r, bucket, key)

	case r.Method == http.MethodGet && bucket != "" && key != "":
		s.getObject(w, r, bucket, key)

	case r.Method == http.MethodHead && bucket != "" && key != "":
		s.headObject(w, r, bucket, key)

	case r.Method == http.MethodDelete && bucket != "" && key != "":
		s.deleteObject(w, r, bucket, key)

	default:
		s.xmlError(w, http.StatusMethodNotAllowed, "MethodNotAllowed",
			fmt.Sprintf("method %s not supported for this operation", r.Method))
	}
}

// parsePath extracts bucket and key from both path-style and virtual-hosted-style URLs.
// Path style:  /bucket/key
// Virtual:     bucket.s3.localhost/key
func parsePath(r *http.Request) (bucket, key string) {
	host := strings.ToLower(r.Host)
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}

	// Virtual hosted style: bucket.s3.localhost or bucket.s3.amazonaws.com
	if strings.Contains(host, ".s3.") {
		parts := strings.SplitN(host, ".s3.", 2)
		bucket = parts[0]
		key = strings.TrimPrefix(r.URL.Path, "/")
		return
	}

	// Path style: /bucket/key
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		return "", ""
	}
	parts := strings.SplitN(path, "/", 2)
	bucket = parts[0]
	if len(parts) > 1 {
		key = parts[1]
	}
	return
}

// --- XML helpers ---

func xmlWrite(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	fmt.Fprint(w, xmlHeader)
	xml.NewEncoder(w).Encode(v)
}

func (s *Service) xmlError(w http.ResponseWriter, status int, code, message string) {
	type errResp struct {
		XMLName xml.Name `xml:"Error"`
		Code    string   `xml:"Code"`
		Message string   `xml:"Message"`
	}
	xmlWrite(w, status, errResp{Code: code, Message: message})
}

// --- Common types ---

func etag(data []byte) string {
	return fmt.Sprintf(`"%x"`, md5.Sum(data))
}

func nowISO() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}
