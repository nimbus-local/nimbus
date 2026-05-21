package ecr

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestService() *Service {
	return New("us-east-1")
}

// ecrRequest sends an ECR management API request.
func ecrRequest(t *testing.T, svc *Service, action string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(body)
	req := httptest.NewRequest(http.MethodPost, "/", &buf)
	req.Header.Set("X-Amz-Target", ecrTarget+action)
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	w := httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	return w
}

// registryRequest sends a Docker V2 registry request.
func registryRequest(t *testing.T, svc *Service, method, path string, body []byte, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var bodyReader *bytes.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	} else {
		bodyReader = bytes.NewReader([]byte{})
	}
	req := httptest.NewRequest(method, path, bodyReader)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	return w
}

func decodeJSON(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var out map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&out); err != nil {
		t.Fatalf("failed to decode JSON: %v\nbody: %s", err, w.Body.String())
	}
	return out
}

func mustCreateRepo(t *testing.T, svc *Service, name string) map[string]interface{} {
	t.Helper()
	w := ecrRequest(t, svc, "CreateRepository", map[string]string{"repositoryName": name})
	if w.Code != http.StatusOK {
		t.Fatalf("CreateRepository %q: expected 200, got %d\n%s", name, w.Code, w.Body.String())
	}
	return decodeJSON(t, w)["repository"].(map[string]interface{})
}

// blobDigest computes sha256:<hex> for a byte slice.
func blobDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// pushBlob pushes a blob using the chunked upload protocol.
func pushBlob(t *testing.T, svc *Service, repoName string, data []byte) string {
	t.Helper()
	digest := blobDigest(data)

	// Initiate upload
	w := registryRequest(t, svc, http.MethodPost,
		fmt.Sprintf("/v2/%s/blobs/uploads/", repoName), nil, nil)
	if w.Code != http.StatusAccepted {
		t.Fatalf("POST blobs/uploads: expected 202, got %d\n%s", w.Code, w.Body.String())
	}
	uploadID := w.Header().Get("Docker-Upload-UUID")

	// Upload data via PATCH
	w = registryRequest(t, svc, http.MethodPatch,
		fmt.Sprintf("/v2/%s/blobs/uploads/%s", repoName, uploadID),
		data, map[string]string{"Content-Type": "application/octet-stream"})
	if w.Code != http.StatusAccepted {
		t.Fatalf("PATCH blobs/uploads: expected 202, got %d\n%s", w.Code, w.Body.String())
	}

	// Complete upload
	w = registryRequest(t, svc, http.MethodPut,
		fmt.Sprintf("/v2/%s/blobs/uploads/%s?digest=%s", repoName, uploadID, digest),
		nil, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("PUT blobs/uploads: expected 201, got %d\n%s", w.Code, w.Body.String())
	}
	return digest
}

// pushManifest pushes a manifest and returns its digest.
func pushManifest(t *testing.T, svc *Service, repoName, tag string, manifest []byte) string {
	t.Helper()
	w := registryRequest(t, svc, http.MethodPut,
		fmt.Sprintf("/v2/%s/manifests/%s", repoName, tag),
		manifest,
		map[string]string{"Content-Type": "application/vnd.docker.distribution.manifest.v2+json"})
	if w.Code != http.StatusCreated {
		t.Fatalf("PUT manifest: expected 201, got %d\n%s", w.Code, w.Body.String())
	}
	return w.Header().Get("Docker-Content-Digest")
}

// --- Detect ---

func TestDetect(t *testing.T) {
	svc := newTestService()
	cases := []struct {
		target   string
		path     string
		expected bool
	}{
		{ecrTarget + "CreateRepository", "", true},
		{ecrTarget + "GetAuthorizationToken", "", true},
		{"", "/v2/", true},
		{"", "/v2/my-repo/manifests/latest", true},
		{"", "/v2/my-repo/blobs/sha256:abc", true},
		{"", "/v2/email/outbound-emails", false}, // SES path — must not claim
		{"AmazonSQS.SendMessage", "", false},
		{"", "/not-v2/something", false},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		if tc.target != "" {
			req.Header.Set("X-Amz-Target", tc.target)
		}
		if tc.path != "" {
			req.URL.Path = tc.path
		}
		if got := svc.Detect(req); got != tc.expected {
			t.Errorf("Detect(target=%q path=%q): expected %v, got %v", tc.target, tc.path, tc.expected, got)
		}
	}
}

// --- Management plane: repositories ---

func TestCreateRepository(t *testing.T) {
	svc := newTestService()

	meta := mustCreateRepo(t, svc, "my-repo")
	if meta["repositoryName"] != "my-repo" {
		t.Errorf("expected repositoryName=my-repo, got %v", meta["repositoryName"])
	}
	if !strings.Contains(meta["repositoryArn"].(string), "my-repo") {
		t.Errorf("expected ARN to contain repo name, got %v", meta["repositoryArn"])
	}
	if !strings.Contains(meta["repositoryUri"].(string), "my-repo") {
		t.Errorf("expected URI to contain repo name, got %v", meta["repositoryUri"])
	}
}

func TestCreateRepository_AlreadyExists(t *testing.T) {
	svc := newTestService()
	mustCreateRepo(t, svc, "my-repo")

	w := ecrRequest(t, svc, "CreateRepository", map[string]string{"repositoryName": "my-repo"})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for duplicate repo, got %d", w.Code)
	}
}

func TestDeleteRepository(t *testing.T) {
	svc := newTestService()
	mustCreateRepo(t, svc, "temp-repo")

	w := ecrRequest(t, svc, "DeleteRepository", map[string]string{"repositoryName": "temp-repo"})
	if w.Code != http.StatusOK {
		t.Fatalf("DeleteRepository: expected 200, got %d", w.Code)
	}

	dw := ecrRequest(t, svc, "DescribeRepositories", map[string][]string{"repositoryNames": {"temp-repo"}})
	resp := decodeJSON(t, dw)
	repos := resp["repositories"].([]interface{})
	if len(repos) != 0 {
		t.Errorf("expected empty list after delete, got %d repos", len(repos))
	}
}

func TestDeleteRepository_NotFound(t *testing.T) {
	svc := newTestService()

	w := ecrRequest(t, svc, "DeleteRepository", map[string]string{"repositoryName": "no-such-repo"})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing repo, got %d", w.Code)
	}
}

func TestDescribeRepositories(t *testing.T) {
	svc := newTestService()
	mustCreateRepo(t, svc, "repo-a")
	mustCreateRepo(t, svc, "repo-b")

	w := ecrRequest(t, svc, "DescribeRepositories", map[string]interface{}{})
	if w.Code != http.StatusOK {
		t.Fatalf("DescribeRepositories: expected 200, got %d", w.Code)
	}
	resp := decodeJSON(t, w)
	repos := resp["repositories"].([]interface{})
	if len(repos) != 2 {
		t.Errorf("expected 2 repos, got %d", len(repos))
	}
}

func TestDescribeRepositories_FilterByName(t *testing.T) {
	svc := newTestService()
	mustCreateRepo(t, svc, "repo-a")
	mustCreateRepo(t, svc, "repo-b")

	w := ecrRequest(t, svc, "DescribeRepositories", map[string]interface{}{
		"repositoryNames": []string{"repo-a"},
	})
	resp := decodeJSON(t, w)
	repos := resp["repositories"].([]interface{})
	if len(repos) != 1 {
		t.Errorf("expected 1 repo, got %d", len(repos))
	}
}

// --- GetAuthorizationToken ---

func TestGetAuthorizationToken(t *testing.T) {
	svc := newTestService()

	w := ecrRequest(t, svc, "GetAuthorizationToken", map[string]interface{}{})
	if w.Code != http.StatusOK {
		t.Fatalf("GetAuthorizationToken: expected 200, got %d", w.Code)
	}
	resp := decodeJSON(t, w)
	data := resp["authorizationData"].([]interface{})
	if len(data) != 1 {
		t.Fatalf("expected 1 authorizationData entry, got %d", len(data))
	}
	entry := data[0].(map[string]interface{})
	if entry["authorizationToken"] == "" {
		t.Error("expected non-empty authorizationToken")
	}
	if entry["proxyEndpoint"] == "" {
		t.Error("expected non-empty proxyEndpoint")
	}
}

// --- Tags ---

func TestTagUntagListTags(t *testing.T) {
	svc := newTestService()
	meta := mustCreateRepo(t, svc, "my-repo")
	arn := meta["repositoryArn"].(string)

	ecrRequest(t, svc, "TagResource", map[string]interface{}{
		"resourceArn": arn,
		"tags": []map[string]string{
			{"Key": "env", "Value": "prod"},
			{"Key": "team", "Value": "platform"},
		},
	})

	w := ecrRequest(t, svc, "ListTagsForResource", map[string]string{"resourceArn": arn})
	resp := decodeJSON(t, w)
	tags := resp["tags"].([]interface{})
	if len(tags) != 2 {
		t.Errorf("expected 2 tags, got %d", len(tags))
	}

	ecrRequest(t, svc, "UntagResource", map[string]interface{}{
		"resourceArn": arn,
		"tagKeys":     []string{"env"},
	})
	w2 := ecrRequest(t, svc, "ListTagsForResource", map[string]string{"resourceArn": arn})
	resp2 := decodeJSON(t, w2)
	tags2 := resp2["tags"].([]interface{})
	if len(tags2) != 1 {
		t.Errorf("expected 1 tag after untag, got %d", len(tags2))
	}
}

// --- Docker V2: version check ---

func TestRegistryVersionCheck(t *testing.T) {
	svc := newTestService()

	w := registryRequest(t, svc, http.MethodGet, "/v2/", nil, nil)
	if w.Code != http.StatusOK {
		t.Errorf("GET /v2/: expected 200, got %d", w.Code)
	}
}

// --- Docker V2: blob push/pull ---

func TestBlobPushPull(t *testing.T) {
	svc := newTestService()
	mustCreateRepo(t, svc, "my-repo")

	data := []byte("fake layer content")
	digest := pushBlob(t, svc, "my-repo", data)

	// HEAD
	hw := registryRequest(t, svc, http.MethodHead,
		fmt.Sprintf("/v2/my-repo/blobs/%s", digest), nil, nil)
	if hw.Code != http.StatusOK {
		t.Fatalf("HEAD blob: expected 200, got %d", hw.Code)
	}
	if hw.Header().Get("Docker-Content-Digest") != digest {
		t.Errorf("expected Docker-Content-Digest=%s", digest)
	}

	// GET
	gw := registryRequest(t, svc, http.MethodGet,
		fmt.Sprintf("/v2/my-repo/blobs/%s", digest), nil, nil)
	if gw.Code != http.StatusOK {
		t.Fatalf("GET blob: expected 200, got %d", gw.Code)
	}
	if string(gw.Body.Bytes()) != string(data) {
		t.Errorf("blob content mismatch")
	}
}

func TestBlobMonolithicUpload(t *testing.T) {
	svc := newTestService()
	mustCreateRepo(t, svc, "my-repo")

	data := []byte("monolithic blob data")
	digest := blobDigest(data)

	w := registryRequest(t, svc, http.MethodPost,
		fmt.Sprintf("/v2/my-repo/blobs/uploads/?digest=%s", digest),
		data, map[string]string{"Content-Type": "application/octet-stream"})
	if w.Code != http.StatusCreated {
		t.Fatalf("monolithic upload: expected 201, got %d\n%s", w.Code, w.Body.String())
	}

	gw := registryRequest(t, svc, http.MethodGet,
		fmt.Sprintf("/v2/my-repo/blobs/%s", digest), nil, nil)
	if gw.Code != http.StatusOK {
		t.Fatalf("GET after monolithic upload: expected 200, got %d", gw.Code)
	}
}

func TestBlobNotFound(t *testing.T) {
	svc := newTestService()
	mustCreateRepo(t, svc, "my-repo")

	w := registryRequest(t, svc, http.MethodGet,
		"/v2/my-repo/blobs/sha256:0000000000000000000000000000000000000000000000000000000000000000",
		nil, nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for missing blob, got %d", w.Code)
	}
}

func TestBlobDigestMismatch(t *testing.T) {
	svc := newTestService()
	mustCreateRepo(t, svc, "my-repo")

	// Initiate upload
	w := registryRequest(t, svc, http.MethodPost, "/v2/my-repo/blobs/uploads/", nil, nil)
	uploadID := w.Header().Get("Docker-Upload-UUID")

	// Complete with wrong digest
	wrong := "sha256:" + strings.Repeat("0", 64)
	pw := registryRequest(t, svc, http.MethodPut,
		fmt.Sprintf("/v2/my-repo/blobs/uploads/%s?digest=%s", uploadID, wrong),
		[]byte("actual data"), nil)
	if pw.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for digest mismatch, got %d", pw.Code)
	}
}

func TestDeleteBlob(t *testing.T) {
	svc := newTestService()
	mustCreateRepo(t, svc, "my-repo")

	data := []byte("to be deleted")
	digest := pushBlob(t, svc, "my-repo", data)

	dw := registryRequest(t, svc, http.MethodDelete,
		fmt.Sprintf("/v2/my-repo/blobs/%s", digest), nil, nil)
	if dw.Code != http.StatusAccepted {
		t.Fatalf("DELETE blob: expected 202, got %d", dw.Code)
	}

	hw := registryRequest(t, svc, http.MethodHead,
		fmt.Sprintf("/v2/my-repo/blobs/%s", digest), nil, nil)
	if hw.Code != http.StatusNotFound {
		t.Errorf("expected 404 after delete, got %d", hw.Code)
	}
}

// --- Docker V2: manifest push/pull ---

func TestManifestPushPull(t *testing.T) {
	svc := newTestService()
	mustCreateRepo(t, svc, "my-repo")

	manifest := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.docker.distribution.manifest.v2+json","config":{"mediaType":"application/vnd.docker.container.image.v1+json","size":0,"digest":"sha256:` + strings.Repeat("a", 64) + `"},"layers":[]}`)
	digest := pushManifest(t, svc, "my-repo", "latest", manifest)

	// GET by tag
	gw := registryRequest(t, svc, http.MethodGet, "/v2/my-repo/manifests/latest", nil, nil)
	if gw.Code != http.StatusOK {
		t.Fatalf("GET manifest by tag: expected 200, got %d\n%s", gw.Code, gw.Body.String())
	}
	if gw.Header().Get("Docker-Content-Digest") != digest {
		t.Errorf("expected digest in response header")
	}

	// GET by digest
	gw2 := registryRequest(t, svc, http.MethodGet,
		fmt.Sprintf("/v2/my-repo/manifests/%s", digest), nil, nil)
	if gw2.Code != http.StatusOK {
		t.Fatalf("GET manifest by digest: expected 200, got %d", gw2.Code)
	}
}

func TestManifestHead(t *testing.T) {
	svc := newTestService()
	mustCreateRepo(t, svc, "my-repo")

	manifest := []byte(`{"schemaVersion":2}`)
	digest := pushManifest(t, svc, "my-repo", "v1.0", manifest)

	hw := registryRequest(t, svc, http.MethodHead,
		fmt.Sprintf("/v2/my-repo/manifests/%s", digest), nil, nil)
	if hw.Code != http.StatusOK {
		t.Fatalf("HEAD manifest: expected 200, got %d", hw.Code)
	}
	if hw.Body.Len() != 0 {
		t.Error("HEAD should return empty body")
	}
}

func TestManifestAutoCreateRepo(t *testing.T) {
	svc := newTestService()
	// No CreateRepository call — push should auto-create

	manifest := []byte(`{"schemaVersion":2}`)
	w := registryRequest(t, svc, http.MethodPut,
		"/v2/auto-created/manifests/latest",
		manifest,
		map[string]string{"Content-Type": "application/vnd.docker.distribution.manifest.v2+json"})
	if w.Code != http.StatusCreated {
		t.Fatalf("auto-create push: expected 201, got %d\n%s", w.Code, w.Body.String())
	}

	// Repo should now exist
	dw := ecrRequest(t, svc, "DescribeRepositories", map[string]interface{}{})
	resp := decodeJSON(t, dw)
	repos := resp["repositories"].([]interface{})
	if len(repos) != 1 {
		t.Errorf("expected auto-created repo in DescribeRepositories, got %d", len(repos))
	}
}

func TestManifestNotFound(t *testing.T) {
	svc := newTestService()
	mustCreateRepo(t, svc, "my-repo")

	w := registryRequest(t, svc, http.MethodGet, "/v2/my-repo/manifests/missing-tag", nil, nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for missing manifest, got %d", w.Code)
	}
}

func TestDeleteManifest(t *testing.T) {
	svc := newTestService()
	mustCreateRepo(t, svc, "my-repo")

	manifest := []byte(`{"schemaVersion":2}`)
	digest := pushManifest(t, svc, "my-repo", "to-delete", manifest)

	dw := registryRequest(t, svc, http.MethodDelete,
		fmt.Sprintf("/v2/my-repo/manifests/%s", digest), nil, nil)
	if dw.Code != http.StatusAccepted {
		t.Fatalf("DELETE manifest: expected 202, got %d", dw.Code)
	}

	gw := registryRequest(t, svc, http.MethodGet,
		fmt.Sprintf("/v2/my-repo/manifests/%s", digest), nil, nil)
	if gw.Code != http.StatusNotFound {
		t.Errorf("expected 404 after delete, got %d", gw.Code)
	}
}

// --- Docker V2: tags list ---

func TestListTags(t *testing.T) {
	svc := newTestService()
	mustCreateRepo(t, svc, "my-repo")

	manifest := []byte(`{"schemaVersion":2}`)
	pushManifest(t, svc, "my-repo", "latest", manifest)
	pushManifest(t, svc, "my-repo", "v1.0", manifest)
	pushManifest(t, svc, "my-repo", "v2.0", manifest)

	w := registryRequest(t, svc, http.MethodGet, "/v2/my-repo/tags/list", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET tags/list: expected 200, got %d", w.Code)
	}
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	tags := resp["tags"].([]interface{})
	if len(tags) != 3 {
		t.Errorf("expected 3 tags, got %d", len(tags))
	}
}

// --- Management: ListImages / DescribeImages / BatchDeleteImage ---

func TestListImages(t *testing.T) {
	svc := newTestService()
	mustCreateRepo(t, svc, "my-repo")

	manifest := []byte(`{"schemaVersion":2}`)
	pushManifest(t, svc, "my-repo", "v1", manifest)
	pushManifest(t, svc, "my-repo", "v2", manifest)

	w := ecrRequest(t, svc, "ListImages", map[string]string{"repositoryName": "my-repo"})
	if w.Code != http.StatusOK {
		t.Fatalf("ListImages: expected 200, got %d", w.Code)
	}
	resp := decodeJSON(t, w)
	ids := resp["imageIds"].([]interface{})
	if len(ids) < 1 {
		t.Errorf("expected at least 1 image, got %d", len(ids))
	}
}

func TestDescribeImages(t *testing.T) {
	svc := newTestService()
	mustCreateRepo(t, svc, "my-repo")

	manifest := []byte(`{"schemaVersion":2}`)
	pushManifest(t, svc, "my-repo", "latest", manifest)

	w := ecrRequest(t, svc, "DescribeImages", map[string]string{"repositoryName": "my-repo"})
	if w.Code != http.StatusOK {
		t.Fatalf("DescribeImages: expected 200, got %d", w.Code)
	}
	resp := decodeJSON(t, w)
	details := resp["imageDetails"].([]interface{})
	if len(details) != 1 {
		t.Errorf("expected 1 image detail, got %d", len(details))
	}
}

func TestBatchDeleteImage(t *testing.T) {
	svc := newTestService()
	mustCreateRepo(t, svc, "my-repo")

	manifest := []byte(`{"schemaVersion":2}`)
	pushManifest(t, svc, "my-repo", "to-delete", manifest)

	w := ecrRequest(t, svc, "BatchDeleteImage", map[string]interface{}{
		"repositoryName": "my-repo",
		"imageIds":       []map[string]string{{"imageTag": "to-delete"}},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("BatchDeleteImage: expected 200, got %d", w.Code)
	}
	resp := decodeJSON(t, w)
	deleted := resp["imageIds"].([]interface{})
	if len(deleted) != 1 {
		t.Errorf("expected 1 deleted image, got %d", len(deleted))
	}
}

func TestBatchGetImage(t *testing.T) {
	svc := newTestService()
	mustCreateRepo(t, svc, "my-repo")

	manifest := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.docker.distribution.manifest.v2+json"}`)
	pushManifest(t, svc, "my-repo", "latest", manifest)

	w := ecrRequest(t, svc, "BatchGetImage", map[string]interface{}{
		"repositoryName": "my-repo",
		"imageIds":       []map[string]string{{"imageTag": "latest"}},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("BatchGetImage: expected 200, got %d", w.Code)
	}
	resp := decodeJSON(t, w)
	images := resp["images"].([]interface{})
	if len(images) != 1 {
		t.Errorf("expected 1 image, got %d", len(images))
	}
	img := images[0].(map[string]interface{})
	if img["imageManifest"] == "" {
		t.Error("expected non-empty imageManifest")
	}
}

// --- Unknown action ---

func TestUnknownAction(t *testing.T) {
	svc := newTestService()

	w := ecrRequest(t, svc, "PutLifecyclePolicy", map[string]string{})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for unknown action, got %d", w.Code)
	}
}
