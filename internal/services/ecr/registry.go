package ecr

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// serveRegistry handles the Docker V2 registry protocol.
// All /v2/... requests (except /v2/email/) land here.
func (s *Service) serveRegistry(w http.ResponseWriter, r *http.Request) {
	// Version check: GET /v2/
	if r.URL.Path == "/v2/" || r.URL.Path == "/v2" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		return
	}

	repo, suffix := parseV2Path(r.URL.Path)
	if repo == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	parts := strings.SplitN(suffix, "/", 2)
	resource := parts[0]
	ref := ""
	if len(parts) > 1 {
		ref = parts[1]
	}

	switch resource {
	case "manifests":
		s.serveManifest(w, r, repo, ref)
	case "blobs":
		s.serveBlob(w, r, repo, ref)
	case "tags":
		s.serveTags(w, r, repo)
	default:
		registryError(w, http.StatusNotFound, "UNSUPPORTED", "unsupported registry operation")
	}
}

// parseV2Path splits /v2/<repo>/manifests/<ref> into (repo, "manifests/<ref>").
func parseV2Path(path string) (repo, suffix string) {
	p := strings.TrimPrefix(path, "/v2/")
	parts := strings.Split(p, "/")
	for i, part := range parts {
		if part == "manifests" || part == "blobs" || part == "tags" {
			repo = strings.Join(parts[:i], "/")
			suffix = strings.Join(parts[i:], "/")
			return
		}
	}
	repo = p
	return
}

// --- Manifests ---

func (s *Service) serveManifest(w http.ResponseWriter, r *http.Request, repoName, ref string) {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		s.getManifest(w, r, repoName, ref)
	case http.MethodPut:
		s.putManifest(w, r, repoName, ref)
	case http.MethodDelete:
		s.deleteManifest(w, r, repoName, ref)
	default:
		registryError(w, http.StatusMethodNotAllowed, "UNSUPPORTED", "method not allowed")
	}
}

func (s *Service) getManifest(w http.ResponseWriter, r *http.Request, repoName, ref string) {
	s.mu.RLock()
	repo, ok := s.repos[repoName]
	s.mu.RUnlock()

	if !ok {
		registryError(w, http.StatusNotFound, "NAME_UNKNOWN", "repository not found")
		return
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	digest := ref
	if !strings.HasPrefix(ref, "sha256:") {
		// ref is a tag
		digest = repo.tagToDigest[ref]
	}
	manifest, ok := repo.manifests[digest]
	if !ok {
		registryError(w, http.StatusNotFound, "MANIFEST_UNKNOWN", "manifest not found")
		return
	}

	mediaType := repo.mediaTypes[digest]
	if mediaType == "" {
		mediaType = "application/vnd.docker.distribution.manifest.v2+json"
	}
	w.Header().Set("Content-Type", mediaType)
	w.Header().Set("Docker-Content-Digest", digest)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(manifest)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		w.Write(manifest)
	}
}

func (s *Service) putManifest(w http.ResponseWriter, r *http.Request, repoName, ref string) {
	s.mu.Lock()
	repo, ok := s.repos[repoName]
	if !ok {
		// Auto-create repository on first push (mirrors ECR behaviour with create-on-push enabled)
		repo = &repository{
			name:        repoName,
			arn:         s.repoARN(repoName),
			uri:         s.repoURI(repoName),
			createdAt:   nowUTC(),
			tags:        map[string]string{},
			manifests:   map[string][]byte{},
			mediaTypes:  map[string]string{},
			tagToDigest: map[string]string{},
		}
		s.repos[repoName] = repo
	}
	s.mu.Unlock()

	data, err := io.ReadAll(r.Body)
	if err != nil {
		registryError(w, http.StatusInternalServerError, "UNKNOWN", "failed to read manifest")
		return
	}

	// Compute digest from content
	sum := sha256.Sum256(data)
	digest := "sha256:" + hex.EncodeToString(sum[:])

	mediaType := r.Header.Get("Content-Type")
	if mediaType == "" {
		mediaType = "application/vnd.docker.distribution.manifest.v2+json"
	}

	s.mu.Lock()
	repo.manifests[digest] = data
	repo.mediaTypes[digest] = mediaType
	if !strings.HasPrefix(ref, "sha256:") {
		repo.tagToDigest[ref] = digest
	}
	s.mu.Unlock()

	w.Header().Set("Docker-Content-Digest", digest)
	w.Header().Set("Location", fmt.Sprintf("/v2/%s/manifests/%s", repoName, digest))
	w.WriteHeader(http.StatusCreated)
}

func (s *Service) deleteManifest(w http.ResponseWriter, r *http.Request, repoName, ref string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	repo, ok := s.repos[repoName]
	if !ok {
		registryError(w, http.StatusNotFound, "NAME_UNKNOWN", "repository not found")
		return
	}

	digest := ref
	if !strings.HasPrefix(ref, "sha256:") {
		digest = repo.tagToDigest[ref]
		delete(repo.tagToDigest, ref)
	}
	delete(repo.manifests, digest)
	delete(repo.mediaTypes, digest)
	// Remove any tags pointing at this digest
	for tag, d := range repo.tagToDigest {
		if d == digest {
			delete(repo.tagToDigest, tag)
		}
	}

	w.WriteHeader(http.StatusAccepted)
}

// --- Blobs ---

func (s *Service) serveBlob(w http.ResponseWriter, r *http.Request, repoName, ref string) {
	// ref may be "uploads/" or "uploads/<uuid>" (for upload sessions) or "sha256:..."
	if strings.HasPrefix(ref, "uploads") {
		s.serveBlobUpload(w, r, repoName, ref)
		return
	}

	switch r.Method {
	case http.MethodHead:
		s.headBlob(w, r, ref)
	case http.MethodGet:
		s.getBlob(w, r, ref)
	case http.MethodDelete:
		s.deleteBlob(w, r, ref)
	default:
		registryError(w, http.StatusMethodNotAllowed, "UNSUPPORTED", "method not allowed")
	}
}

func (s *Service) headBlob(w http.ResponseWriter, r *http.Request, digest string) {
	s.mu.RLock()
	data, ok := s.blobs[digest]
	s.mu.RUnlock()

	if !ok {
		registryError(w, http.StatusNotFound, "BLOB_UNKNOWN", "blob not found")
		return
	}
	w.Header().Set("Docker-Content-Digest", digest)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(http.StatusOK)
}

func (s *Service) getBlob(w http.ResponseWriter, r *http.Request, digest string) {
	s.mu.RLock()
	data, ok := s.blobs[digest]
	s.mu.RUnlock()

	if !ok {
		registryError(w, http.StatusNotFound, "BLOB_UNKNOWN", "blob not found")
		return
	}
	w.Header().Set("Docker-Content-Digest", digest)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

func (s *Service) deleteBlob(w http.ResponseWriter, r *http.Request, digest string) {
	s.mu.Lock()
	delete(s.blobs, digest)
	s.mu.Unlock()
	w.WriteHeader(http.StatusAccepted)
}

// --- Blob uploads (POST / PATCH / PUT) ---

func (s *Service) serveBlobUpload(w http.ResponseWriter, r *http.Request, repoName, ref string) {
	// ref is "uploads" (POST to start), "uploads/<uuid>" (PATCH/PUT)
	uploadID := strings.TrimPrefix(ref, "uploads/")
	if uploadID == "uploads" {
		uploadID = ""
	}

	switch r.Method {
	case http.MethodPost:
		// POST /v2/<name>/blobs/uploads/  — initiate upload
		// Docker may also do a monolithic upload: POST with digest + body
		id := newUploadID()
		s.mu.Lock()
		s.uploads[id] = &upload{id: id, repo: repoName}
		s.mu.Unlock()

		// Check for monolithic upload (digest provided in query)
		if digest := r.URL.Query().Get("digest"); digest != "" {
			data, _ := io.ReadAll(r.Body)
			if err := s.storeBlob(id, digest, data); err != nil {
				registryError(w, http.StatusBadRequest, "DIGEST_INVALID", err.Error())
				return
			}
			w.Header().Set("Docker-Content-Digest", digest)
			w.Header().Set("Location", fmt.Sprintf("/v2/%s/blobs/%s", repoName, digest))
			w.WriteHeader(http.StatusCreated)
			return
		}

		w.Header().Set("Docker-Upload-UUID", id)
		w.Header().Set("Location", fmt.Sprintf("/v2/%s/blobs/uploads/%s", repoName, id))
		w.Header().Set("Range", "0-0")
		w.WriteHeader(http.StatusAccepted)

	case http.MethodPatch:
		// PATCH /v2/<name>/blobs/uploads/<uuid>  — upload chunk
		s.mu.Lock()
		up, ok := s.uploads[uploadID]
		if !ok {
			s.mu.Unlock()
			registryError(w, http.StatusNotFound, "BLOB_UPLOAD_UNKNOWN", "upload session not found")
			return
		}
		chunk, _ := io.ReadAll(r.Body)
		up.data = append(up.data, chunk...)
		end := len(up.data)
		s.mu.Unlock()

		w.Header().Set("Docker-Upload-UUID", uploadID)
		w.Header().Set("Location", fmt.Sprintf("/v2/%s/blobs/uploads/%s", repoName, uploadID))
		w.Header().Set("Range", fmt.Sprintf("0-%d", end-1))
		w.WriteHeader(http.StatusAccepted)

	case http.MethodPut:
		// PUT /v2/<name>/blobs/uploads/<uuid>?digest=sha256:...  — complete upload
		digest := r.URL.Query().Get("digest")
		if digest == "" {
			registryError(w, http.StatusBadRequest, "DIGEST_INVALID", "digest query parameter required")
			return
		}

		s.mu.Lock()
		up, ok := s.uploads[uploadID]
		if !ok {
			s.mu.Unlock()
			registryError(w, http.StatusNotFound, "BLOB_UPLOAD_UNKNOWN", "upload session not found")
			return
		}
		// Append any final body data
		final, _ := io.ReadAll(r.Body)
		up.data = append(up.data, final...)
		data := up.data
		delete(s.uploads, uploadID)
		s.mu.Unlock()

		if err := s.storeBlob("", digest, data); err != nil {
			registryError(w, http.StatusBadRequest, "DIGEST_INVALID", err.Error())
			return
		}

		w.Header().Set("Docker-Content-Digest", digest)
		w.Header().Set("Location", fmt.Sprintf("/v2/%s/blobs/%s", repoName, digest))
		w.WriteHeader(http.StatusCreated)

	default:
		registryError(w, http.StatusMethodNotAllowed, "UNSUPPORTED", "method not allowed")
	}
}

// storeBlob verifies the digest and stores the data. uploadID is for cleanup only.
func (s *Service) storeBlob(uploadID, digest string, data []byte) error {
	sum := sha256.Sum256(data)
	computed := "sha256:" + hex.EncodeToString(sum[:])
	if computed != digest {
		return fmt.Errorf("digest mismatch: got %s, expected %s", computed, digest)
	}
	s.mu.Lock()
	s.blobs[digest] = data
	s.mu.Unlock()
	return nil
}

// --- Tags ---

func (s *Service) serveTags(w http.ResponseWriter, r *http.Request, repoName string) {
	if r.Method != http.MethodGet {
		registryError(w, http.StatusMethodNotAllowed, "UNSUPPORTED", "method not allowed")
		return
	}

	s.mu.RLock()
	repo, ok := s.repos[repoName]
	s.mu.RUnlock()

	if !ok {
		registryError(w, http.StatusNotFound, "NAME_UNKNOWN", "repository not found")
		return
	}

	s.mu.RLock()
	tags := make([]string, 0, len(repo.tagToDigest))
	for tag := range repo.tagToDigest {
		tags = append(tags, tag)
	}
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"name": repoName,
		"tags": tags,
	})
}

// --- Helpers ---

func registryError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"errors": []map[string]interface{}{
			{"code": code, "message": message, "detail": ""},
		},
	})
}

func nowUTC() time.Time { return time.Now().UTC() }
