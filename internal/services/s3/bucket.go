package s3

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// bucketMeta is persisted as .nimbus-bucket.json inside each bucket directory
type bucketMeta struct {
	Name         string    `json:"name"`
	CreationDate time.Time `json:"creation_date"`
	Region       string    `json:"region"`
}

func (s *Service) bucketDir(bucket string) string {
	return filepath.Join(s.dataDir, bucket)
}

func (s *Service) bucketMetaPath(bucket string) string {
	return filepath.Join(s.bucketDir(bucket), ".nimbus-bucket.json")
}

func (s *Service) bucketExists(bucket string) bool {
	_, err := os.Stat(s.bucketDir(bucket))
	return err == nil
}

func (s *Service) loadBucketMeta(bucket string) (bucketMeta, error) {
	var meta bucketMeta
	data, err := os.ReadFile(s.bucketMetaPath(bucket))
	if err != nil {
		return meta, err
	}
	err = json.Unmarshal(data, &meta)
	return meta, err
}

func (s *Service) saveBucketMeta(meta bucketMeta) error {
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return os.WriteFile(s.bucketMetaPath(meta.Name), data, 0644)
}

// ListBuckets — GET /
func (s *Service) listBuckets(w http.ResponseWriter, r *http.Request) {
	type bucket struct {
		Name         string `xml:"Name"`
		CreationDate string `xml:"CreationDate"`
	}
	type result struct {
		XMLName xml.Name `xml:"ListAllMyBucketsResult"`
		Owner   struct {
			ID          string `xml:"ID"`
			DisplayName string `xml:"DisplayName"`
		} `xml:"Owner"`
		Buckets struct {
			Bucket []bucket `xml:"Bucket"`
		} `xml:"Buckets"`
	}

	entries, err := os.ReadDir(s.dataDir)
	if err != nil && !os.IsNotExist(err) {
		s.xmlError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	var res result
	res.Owner.ID = "local"
	res.Owner.DisplayName = "local"

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		meta, err := s.loadBucketMeta(e.Name())
		createdAt := ""
		if err == nil {
			createdAt = meta.CreationDate.UTC().Format(time.RFC3339)
		} else {
			info, _ := e.Info()
			if info != nil {
				createdAt = info.ModTime().UTC().Format(time.RFC3339)
			}
		}
		res.Buckets.Bucket = append(res.Buckets.Bucket, bucket{
			Name:         e.Name(),
			CreationDate: createdAt,
		})
	}

	xmlWrite(w, http.StatusOK, res)
}

// CreateBucket — PUT /:bucket
func (s *Service) createBucket(w http.ResponseWriter, r *http.Request, bucket string) {
	if !validBucketName(bucket) {
		s.xmlError(w, http.StatusBadRequest, "InvalidBucketName",
			fmt.Sprintf("The specified bucket is not valid: %s", bucket))
		return
	}

	dir := s.bucketDir(bucket)
	if err := os.MkdirAll(dir, 0755); err != nil {
		s.xmlError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	// Also create the metadata directory for object metadata
	if err := os.MkdirAll(filepath.Join(dir, ".nimbus-meta"), 0755); err != nil {
		s.xmlError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	meta := bucketMeta{
		Name:         bucket,
		CreationDate: time.Now().UTC(),
		Region:       r.Header.Get("X-Amz-Create-Bucket-Region"),
	}
	if meta.Region == "" {
		meta.Region = "us-east-1"
	}

	if err := s.saveBucketMeta(meta); err != nil {
		s.xmlError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	w.Header().Set("Location", "/"+bucket)
	w.WriteHeader(http.StatusOK)
}

// DeleteBucket — DELETE /:bucket
func (s *Service) deleteBucket(w http.ResponseWriter, r *http.Request, bucket string) {
	if !s.bucketExists(bucket) {
		s.xmlError(w, http.StatusNotFound, "NoSuchBucket",
			fmt.Sprintf("The specified bucket does not exist: %s", bucket))
		return
	}

	// Check if bucket is empty (ignoring Nimbus-internal metadata files/dirs)
	entries, _ := os.ReadDir(s.bucketDir(bucket))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".nimbus-") {
			continue
		}
		s.xmlError(w, http.StatusConflict, "BucketNotEmpty",
			"The bucket you tried to delete is not empty.")
		return
	}

	if err := os.RemoveAll(s.bucketDir(bucket)); err != nil {
		s.xmlError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// HeadBucket — HEAD /:bucket
func (s *Service) headBucket(w http.ResponseWriter, r *http.Request, bucket string) {
	if !s.bucketExists(bucket) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("x-amz-bucket-region", "us-east-1")
	w.WriteHeader(http.StatusOK)
}

// getBucketPolicy returns 404 NoSuchBucketPolicy since Nimbus doesn't store bucket policies.
func (s *Service) getBucketPolicy(w http.ResponseWriter, r *http.Request, bucket string) {
	if !s.bucketExists(bucket) {
		s.xmlError(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist.")
		return
	}
	s.xmlError(w, http.StatusNotFound, "NoSuchBucketPolicy", "The bucket policy does not exist.")
}

func (s *Service) lifecyclePath(bucket string) string {
	return filepath.Join(s.bucketDir(bucket), ".nimbus-lifecycle.xml")
}

// getBucketLifecycle — GET /:bucket?lifecycle
func (s *Service) getBucketLifecycle(w http.ResponseWriter, r *http.Request, bucket string) {
	if !s.bucketExists(bucket) {
		s.xmlError(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist.")
		return
	}
	data, err := os.ReadFile(s.lifecyclePath(bucket))
	if err != nil {
		s.xmlError(w, http.StatusNotFound, "NoSuchLifecycleConfiguration",
			"The lifecycle configuration does not exist.")
		return
	}
	// The Pulumi/TF AWS provider v5.44+ waiter polls GetBucketLifecycleConfiguration
	// and checks the x-amz-transition-default-minimum-object-size response header.
	// AWS sets this header server-side; without it the waiter times out after 3 min.
	w.Header().Set("Content-Type", "application/xml")
	w.Header().Set("x-amz-transition-default-minimum-object-size", "all_storage_classes_128K")
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

// putBucketLifecycle — PUT /:bucket?lifecycle
func (s *Service) putBucketLifecycle(w http.ResponseWriter, r *http.Request, bucket string) {
	if !s.bucketExists(bucket) {
		s.xmlError(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist.")
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.xmlError(w, http.StatusInternalServerError, "InternalError", "failed to read request body")
		return
	}
	if err := os.WriteFile(s.lifecyclePath(bucket), body, 0644); err != nil {
		s.xmlError(w, http.StatusInternalServerError, "InternalError", "failed to store lifecycle config")
		return
	}
	w.WriteHeader(http.StatusOK)
}

// deleteBucketLifecycle — DELETE /:bucket?lifecycle
func (s *Service) deleteBucketLifecycle(w http.ResponseWriter, r *http.Request, bucket string) {
	if !s.bucketExists(bucket) {
		s.xmlError(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist.")
		return
	}
	os.Remove(s.lifecyclePath(bucket))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) publicAccessBlockPath(bucket string) string {
	return filepath.Join(s.bucketDir(bucket), ".nimbus-public-access-block.xml")
}

// getPublicAccessBlock — GET /:bucket?publicAccessBlock
func (s *Service) getPublicAccessBlock(w http.ResponseWriter, r *http.Request, bucket string) {
	if !s.bucketExists(bucket) {
		s.xmlError(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist.")
		return
	}
	data, err := os.ReadFile(s.publicAccessBlockPath(bucket))
	if err != nil {
		s.xmlError(w, http.StatusNotFound, "NoSuchPublicAccessBlockConfiguration",
			"The public access block configuration was not found.")
		return
	}
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

// putPublicAccessBlock — PUT /:bucket?publicAccessBlock
func (s *Service) putPublicAccessBlock(w http.ResponseWriter, r *http.Request, bucket string) {
	if !s.bucketExists(bucket) {
		s.xmlError(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist.")
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.xmlError(w, http.StatusInternalServerError, "InternalError", "failed to read request body")
		return
	}
	if err := os.WriteFile(s.publicAccessBlockPath(bucket), body, 0644); err != nil {
		s.xmlError(w, http.StatusInternalServerError, "InternalError", "failed to store public access block config")
		return
	}
	w.WriteHeader(http.StatusOK)
}

// deletePublicAccessBlock — DELETE /:bucket?publicAccessBlock
func (s *Service) deletePublicAccessBlock(w http.ResponseWriter, r *http.Request, bucket string) {
	if !s.bucketExists(bucket) {
		s.xmlError(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist.")
		return
	}
	os.Remove(s.publicAccessBlockPath(bucket))
	w.WriteHeader(http.StatusNoContent)
}

// validBucketName enforces S3 bucket naming rules
func validBucketName(name string) bool {
	if len(name) < 3 || len(name) > 63 {
		return false
	}
	for _, c := range name {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '.') {
			return false
		}
	}
	return true
}
