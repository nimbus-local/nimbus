package s3

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// objectMeta is stored as a sidecar JSON file alongside each object
type objectMeta struct {
	Key           string            `json:"key"`
	ContentType   string            `json:"content_type"`
	ContentLength int64             `json:"content_length"`
	ETag          string            `json:"etag"`
	LastModified  time.Time         `json:"last_modified"`
	UserMetadata  map[string]string `json:"user_metadata,omitempty"`
}

func (s *Service) objectPath(bucket, key string) string {
	// Encode slashes in key as directory structure
	return filepath.Join(s.bucketDir(bucket), filepath.FromSlash(key))
}

func (s *Service) metaPath(bucket, key string) string {
	// Store metadata in .nimbus-meta/ with the key encoded as a path
	return filepath.Join(s.bucketDir(bucket), ".nimbus-meta", filepath.FromSlash(key)+".json")
}

func (s *Service) saveMeta(bucket string, meta objectMeta) error {
	p := s.metaPath(bucket, meta.Key)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return err
	}
	data, _ := json.Marshal(meta)
	return os.WriteFile(p, data, 0644)
}

func (s *Service) loadMeta(bucket, key string) (objectMeta, error) {
	var meta objectMeta
	data, err := os.ReadFile(s.metaPath(bucket, key))
	if err != nil {
		return meta, err
	}
	return meta, json.Unmarshal(data, &meta)
}

// PutObject — PUT /:bucket/:key
func (s *Service) putObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	if !s.bucketExists(bucket) {
		s.xmlError(w, http.StatusNotFound, "NoSuchBucket",
			fmt.Sprintf("The specified bucket does not exist: %s", bucket))
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.xmlError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	objPath := s.objectPath(bucket, key)
	if err := os.MkdirAll(filepath.Dir(objPath), 0755); err != nil {
		s.xmlError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	if err := os.WriteFile(objPath, body, 0644); err != nil {
		s.xmlError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	tag := etag(body)

	// Extract user-defined metadata (x-amz-meta-* headers)
	userMeta := map[string]string{}
	for k, v := range r.Header {
		lower := strings.ToLower(k)
		if strings.HasPrefix(lower, "x-amz-meta-") {
			userMeta[lower] = v[0]
		}
	}

	ct := r.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/octet-stream"
	}

	meta := objectMeta{
		Key:           key,
		ContentType:   ct,
		ContentLength: int64(len(body)),
		ETag:          tag,
		LastModified:  time.Now().UTC(),
		UserMetadata:  userMeta,
	}

	if err := s.saveMeta(bucket, meta); err != nil {
		s.xmlError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	w.Header().Set("ETag", tag)
	w.WriteHeader(http.StatusOK)
}

// GetObject — GET /:bucket/:key
func (s *Service) getObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	if !s.bucketExists(bucket) {
		s.xmlError(w, http.StatusNotFound, "NoSuchBucket",
			fmt.Sprintf("The specified bucket does not exist: %s", bucket))
		return
	}

	objPath := s.objectPath(bucket, key)
	data, err := os.ReadFile(objPath)
	if err != nil {
		if os.IsNotExist(err) {
			s.xmlError(w, http.StatusNotFound, "NoSuchKey",
				fmt.Sprintf("The specified key does not exist: %s", key))
			return
		}
		s.xmlError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	meta, _ := s.loadMeta(bucket, key)

	ct := meta.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}

	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
	w.Header().Set("ETag", meta.ETag)
	if !meta.LastModified.IsZero() {
		w.Header().Set("Last-Modified", meta.LastModified.Format(http.TimeFormat))
	}

	// Restore user metadata headers
	for k, v := range meta.UserMetadata {
		w.Header().Set(k, v)
	}

	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

// HeadObject — HEAD /:bucket/:key
func (s *Service) headObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	if !s.bucketExists(bucket) {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	info, err := os.Stat(s.objectPath(bucket, key))
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	meta, _ := s.loadMeta(bucket, key)

	ct := meta.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}

	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size()))
	w.Header().Set("ETag", meta.ETag)
	if !meta.LastModified.IsZero() {
		w.Header().Set("Last-Modified", meta.LastModified.Format(http.TimeFormat))
	}
	w.WriteHeader(http.StatusOK)
}

// DeleteObject — DELETE /:bucket/:key
func (s *Service) deleteObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	if !s.bucketExists(bucket) {
		s.xmlError(w, http.StatusNotFound, "NoSuchBucket",
			fmt.Sprintf("The specified bucket does not exist: %s", bucket))
		return
	}

	// S3 returns 204 even if the object didn't exist
	os.Remove(s.objectPath(bucket, key))
	os.Remove(s.metaPath(bucket, key))

	w.WriteHeader(http.StatusNoContent)
}

// ListObjectsV2 — GET /:bucket?list-type=2
func (s *Service) listObjects(w http.ResponseWriter, r *http.Request, bucket string) {
	if !s.bucketExists(bucket) {
		s.xmlError(w, http.StatusNotFound, "NoSuchBucket",
			fmt.Sprintf("The specified bucket does not exist: %s", bucket))
		return
	}

	q := r.URL.Query()
	prefix := q.Get("prefix")
	delimiter := q.Get("delimiter")
	maxKeysStr := q.Get("max-keys")
	maxKeys := 1000
	if maxKeysStr != "" {
		fmt.Sscanf(maxKeysStr, "%d", &maxKeys)
	}

	type content struct {
		Key          string `xml:"Key"`
		LastModified string `xml:"LastModified"`
		ETag         string `xml:"ETag"`
		Size         int64  `xml:"Size"`
		StorageClass string `xml:"StorageClass"`
	}
	type commonPrefix struct {
		Prefix string `xml:"Prefix"`
	}
	type result struct {
		XMLName        xml.Name       `xml:"ListBucketResult"`
		Name           string         `xml:"Name"`
		Prefix         string         `xml:"Prefix"`
		Delimiter      string         `xml:"Delimiter,omitempty"`
		MaxKeys        int            `xml:"MaxKeys"`
		IsTruncated    bool           `xml:"IsTruncated"`
		KeyCount       int            `xml:"KeyCount"`
		Contents       []content      `xml:"Contents"`
		CommonPrefixes []commonPrefix `xml:"CommonPrefixes"`
	}

	var objects []content
	prefixSet := map[string]bool{}

	bucketDir := s.bucketDir(bucket)
	err := filepath.WalkDir(bucketDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			// Skip internal directories
			if name == ".nimbus-meta" || name == ".nimbus-multipart" {
				return filepath.SkipDir
			}
			return nil
		}
		// Skip internal files
		if d.Name() == ".nimbus-bucket.json" {
			return nil
		}

		// Convert filesystem path back to S3 key
		rel, _ := filepath.Rel(bucketDir, path)
		key := filepath.ToSlash(rel)

		if !strings.HasPrefix(key, prefix) {
			return nil
		}

		// Handle delimiter (folder simulation)
		if delimiter != "" {
			afterPrefix := key[len(prefix):]
			if idx := strings.Index(afterPrefix, delimiter); idx != -1 {
				cp := prefix + afterPrefix[:idx+len(delimiter)]
				prefixSet[cp] = true
				return nil
			}
		}

		meta, _ := s.loadMeta(bucket, key)
		info, _ := d.Info()
		size := int64(0)
		lastMod := nowISO()
		if info != nil {
			size = info.Size()
			lastMod = info.ModTime().UTC().Format(time.RFC3339Nano)
		}
		if !meta.LastModified.IsZero() {
			lastMod = meta.LastModified.UTC().Format(time.RFC3339Nano)
		}
		if meta.ContentLength > 0 {
			size = meta.ContentLength
		}

		objects = append(objects, content{
			Key:          key,
			LastModified: lastMod,
			ETag:         meta.ETag,
			Size:         size,
			StorageClass: "STANDARD",
		})
		return nil
	})
	if err != nil {
		s.xmlError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	sort.Slice(objects, func(i, j int) bool {
		return objects[i].Key < objects[j].Key
	})

	truncated := false
	if len(objects) > maxKeys {
		objects = objects[:maxKeys]
		truncated = true
	}

	var cps []commonPrefix
	for p := range prefixSet {
		cps = append(cps, commonPrefix{Prefix: p})
	}
	sort.Slice(cps, func(i, j int) bool { return cps[i].Prefix < cps[j].Prefix })

	res := result{
		Name:           bucket,
		Prefix:         prefix,
		Delimiter:      delimiter,
		MaxKeys:        maxKeys,
		IsTruncated:    truncated,
		KeyCount:       len(objects) + len(cps),
		Contents:       objects,
		CommonPrefixes: cps,
	}

	xmlWrite(w, http.StatusOK, res)
}

// DeleteObjects — POST /:bucket?delete (batch delete)
func (s *Service) deleteObjects(w http.ResponseWriter, r *http.Request, bucket string) {
	type objectID struct {
		Key string `xml:"Key"`
	}
	type deleteRequest struct {
		Objects []objectID `xml:"Object"`
		Quiet   bool       `xml:"Quiet"`
	}
	type deleted struct {
		Key string `xml:"Key"`
	}
	type deleteError struct {
		Key     string `xml:"Key"`
		Code    string `xml:"Code"`
		Message string `xml:"Message"`
	}
	type result struct {
		XMLName xml.Name      `xml:"DeleteResult"`
		Deleted []deleted     `xml:"Deleted"`
		Error   []deleteError `xml:"Error"`
	}

	var req deleteRequest
	if err := xml.NewDecoder(r.Body).Decode(&req); err != nil {
		s.xmlError(w, http.StatusBadRequest, "MalformedXML", err.Error())
		return
	}

	var res result
	for _, obj := range req.Objects {
		os.Remove(s.objectPath(bucket, obj.Key))
		os.Remove(s.metaPath(bucket, obj.Key))
		if !req.Quiet {
			res.Deleted = append(res.Deleted, deleted{Key: obj.Key})
		}
	}

	xmlWrite(w, http.StatusOK, res)
}
