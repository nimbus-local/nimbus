package s3

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/nimbus-local/nimbus/internal/uid"
)

// Multipart uploads are stored as:
//   .nimbus-multipart/<uploadId>/parts/<partNumber>
//   .nimbus-multipart/<uploadId>/meta.json

func (s *Service) multipartDir(bucket, uploadID string) string {
	return filepath.Join(s.bucketDir(bucket), ".nimbus-multipart", uploadID)
}

func (s *Service) partPath(bucket, uploadID string, partNumber int) string {
	return filepath.Join(s.multipartDir(bucket, uploadID), "parts",
		fmt.Sprintf("%05d", partNumber))
}

// CreateMultipartUpload — POST /:bucket/:key?uploads
func (s *Service) createMultipartUpload(w http.ResponseWriter, r *http.Request, bucket, key string) {
	if !s.bucketExists(bucket) {
		s.xmlError(w, http.StatusNotFound, "NoSuchBucket",
			fmt.Sprintf("The specified bucket does not exist: %s", bucket))
		return
	}

	uploadID := uid.New()

	dir := filepath.Join(s.multipartDir(bucket, uploadID), "parts")
	if err := os.MkdirAll(dir, 0755); err != nil {
		s.xmlError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	// Store key in metadata file
	metaFile := filepath.Join(s.multipartDir(bucket, uploadID), "key")
	os.WriteFile(metaFile, []byte(key), 0644)

	type result struct {
		XMLName  xml.Name `xml:"InitiateMultipartUploadResult"`
		Bucket   string   `xml:"Bucket"`
		Key      string   `xml:"Key"`
		UploadId string   `xml:"UploadId"`
	}

	xmlWrite(w, http.StatusOK, result{
		Bucket:   bucket,
		Key:      key,
		UploadId: uploadID,
	})
}

// UploadPart — PUT /:bucket/:key?partNumber=N&uploadId=X
func (s *Service) uploadPart(w http.ResponseWriter, r *http.Request, bucket, key string) {
	uploadID := r.URL.Query().Get("uploadId")
	partNumStr := r.URL.Query().Get("partNumber")

	partNum, err := strconv.Atoi(partNumStr)
	if err != nil || partNum < 1 || partNum > 10000 {
		s.xmlError(w, http.StatusBadRequest, "InvalidPart", "Invalid part number")
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.xmlError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	partFile := s.partPath(bucket, uploadID, partNum)
	if err := os.MkdirAll(filepath.Dir(partFile), 0755); err != nil {
		s.xmlError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	if err := os.WriteFile(partFile, body, 0644); err != nil {
		s.xmlError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	tag := etag(body)
	w.Header().Set("ETag", tag)
	w.WriteHeader(http.StatusOK)
}

// CompleteMultipartUpload — POST /:bucket/:key?uploadId=X
func (s *Service) completeMultipartUpload(w http.ResponseWriter, r *http.Request, bucket, key string) {
	uploadID := r.URL.Query().Get("uploadId")

	type part struct {
		PartNumber int    `xml:"PartNumber"`
		ETag       string `xml:"ETag"`
	}
	type req struct {
		Parts []part `xml:"Part"`
	}

	var request req
	if err := xml.NewDecoder(r.Body).Decode(&request); err != nil {
		s.xmlError(w, http.StatusBadRequest, "MalformedXML", err.Error())
		return
	}

	sort.Slice(request.Parts, func(i, j int) bool {
		return request.Parts[i].PartNumber < request.Parts[j].PartNumber
	})

	// Concatenate all parts
	var combined bytes.Buffer
	for _, p := range request.Parts {
		data, err := os.ReadFile(s.partPath(bucket, uploadID, p.PartNumber))
		if err != nil {
			s.xmlError(w, http.StatusBadRequest, "InvalidPart",
				fmt.Sprintf("Part %d not found", p.PartNumber))
			return
		}
		combined.Write(data)
	}

	// Write final object
	objPath := s.objectPath(bucket, key)
	if err := os.MkdirAll(filepath.Dir(objPath), 0755); err != nil {
		s.xmlError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	finalData := combined.Bytes()
	if err := os.WriteFile(objPath, finalData, 0644); err != nil {
		s.xmlError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	tag := etag(finalData)
	meta := objectMeta{
		Key:           key,
		ContentType:   "application/octet-stream",
		ContentLength: int64(len(finalData)),
		ETag:          tag,
	}
	s.saveMeta(bucket, meta)

	// Clean up multipart temp files
	os.RemoveAll(s.multipartDir(bucket, uploadID))

	// Build response location URL
	host := r.Host
	if host == "" {
		host = "localhost:4566"
	}
	location := fmt.Sprintf("http://%s/%s/%s", host, bucket, key)

	type result struct {
		XMLName  xml.Name `xml:"CompleteMultipartUploadResult"`
		Location string   `xml:"Location"`
		Bucket   string   `xml:"Bucket"`
		Key      string   `xml:"Key"`
		ETag     string   `xml:"ETag"`
	}

	xmlWrite(w, http.StatusOK, result{
		Location: location,
		Bucket:   bucket,
		Key:      key,
		ETag:     tag,
	})
}

// AbortMultipartUpload — DELETE /:bucket/:key?uploadId=X
func (s *Service) abortMultipartUpload(w http.ResponseWriter, r *http.Request, bucket, key string) {
	uploadID := r.URL.Query().Get("uploadId")
	os.RemoveAll(s.multipartDir(bucket, uploadID))
	w.WriteHeader(http.StatusNoContent)
}

// Presigned URL support — presigned URLs hit GetObject/PutObject with
// X-Amz-Signature and related query params. We validate the bucket/key
// exist and serve normally; signature validation is intentionally skipped
// for local dev (same as LocalStack community behavior).
func isPresigned(r *http.Request) bool {
	q := r.URL.Query()
	return q.Has("X-Amz-Signature") || q.Has("X-Amz-Security-Token")
}

// PresignedURL generates a presigned URL for an object.
// Called by the API when NIMBUS_ENDPOINT_URL or S3_PRESIGNED_BASE_URL is set.
func PresignedURL(baseURL, bucket, key string, ttlSeconds int) string {
	baseURL = strings.TrimRight(baseURL, "/")
	return fmt.Sprintf("%s/%s/%s?X-Amz-Expires=%d&X-Amz-Signature=local",
		baseURL, bucket, key, ttlSeconds)
}
