// Package ecr emulates the AWS Elastic Container Registry management plane
// and the Docker V2 registry protocol for local development.
//
// AWS management API calls arrive via X-Amz-Target: AmazonEC2ContainerRegistry_V20150921.*
// Docker push/pull arrives via the standard /v2/... registry paths.
package ecr

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/nimbus-local/nimbus/internal/uid"
)

const (
	accountID = "000000000000"
	ecrTarget = "AmazonEC2ContainerRegistry_V20150921."
)

// Service implements the ECR management plane and Docker V2 registry.
// All state is in-memory. Blobs are stored globally (deduped by digest);
// manifests are stored per repository.
type Service struct {
	mu      sync.RWMutex
	repos   map[string]*repository // name -> repo
	blobs   map[string][]byte      // digest -> data (global, deduped)
	uploads map[string]*upload     // uploadID -> in-progress upload
	region  string
}

type repository struct {
	name        string
	arn         string
	uri         string
	createdAt   time.Time
	tags        map[string]string
	manifests   map[string][]byte // digest -> raw manifest bytes
	mediaTypes  map[string]string // digest -> Content-Type
	tagToDigest map[string]string // tag -> digest
}

type upload struct {
	id   string
	repo string
	data []byte
}

func New(region string) *Service {
	if region == "" {
		region = "us-east-1"
	}
	return &Service{
		region:  region,
		repos:   map[string]*repository{},
		blobs:   map[string][]byte{},
		uploads: map[string]*upload{},
	}
}

func (s *Service) Name() string { return "ecr" }

// Detect claims ECR management API requests (X-Amz-Target) and Docker V2
// registry requests (/v2/...). /v2/email/ is excluded so SES v2 still works.
func (s *Service) Detect(r *http.Request) bool {
	if strings.HasPrefix(r.Header.Get("X-Amz-Target"), ecrTarget) {
		return true
	}
	p := r.URL.Path
	return strings.HasPrefix(p, "/v2/") && !strings.HasPrefix(p, "/v2/email/")
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Docker V2 registry protocol
	if strings.HasPrefix(r.URL.Path, "/v2/") {
		s.serveRegistry(w, r)
		return
	}

	// ECR management plane
	target := r.Header.Get("X-Amz-Target")
	action := strings.TrimPrefix(target, ecrTarget)

	switch action {
	case "CreateRepository":
		s.createRepository(w, r)
	case "DeleteRepository":
		s.deleteRepository(w, r)
	case "DescribeRepositories":
		s.describeRepositories(w, r)
	case "ListImages":
		s.listImages(w, r)
	case "DescribeImages":
		s.describeImages(w, r)
	case "BatchDeleteImage":
		s.batchDeleteImage(w, r)
	case "BatchGetImage":
		s.batchGetImage(w, r)
	case "GetAuthorizationToken":
		s.getAuthorizationToken(w, r)
	case "TagResource":
		s.tagResource(w, r)
	case "UntagResource":
		s.untagResource(w, r)
	case "ListTagsForResource":
		s.listTagsForResource(w, r)
	default:
		jsonError(w, http.StatusBadRequest, "UnsupportedOperationException",
			fmt.Sprintf("Operation %s is not supported.", action))
	}
}

// --- Management plane ---

func (s *Service) repoARN(name string) string {
	return fmt.Sprintf("arn:aws:ecr:%s:%s:repository/%s", s.region, accountID, name)
}

func (s *Service) repoURI(name string) string {
	return fmt.Sprintf("localhost:4566/%s", name)
}

func (s *Service) createRepository(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RepositoryName string              `json:"repositoryName"`
		Tags           []map[string]string `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.RepositoryName == "" {
		jsonError(w, http.StatusBadRequest, "InvalidParameterException", "repositoryName is required")
		return
	}

	s.mu.Lock()
	if _, exists := s.repos[req.RepositoryName]; exists {
		s.mu.Unlock()
		jsonError(w, http.StatusBadRequest, "RepositoryAlreadyExistsException",
			fmt.Sprintf("Repository %s already exists", req.RepositoryName))
		return
	}
	tags := map[string]string{}
	for _, t := range req.Tags {
		if k := t["Key"]; k != "" {
			tags[k] = t["Value"]
		}
	}
	repo := &repository{
		name:        req.RepositoryName,
		arn:         s.repoARN(req.RepositoryName),
		uri:         s.repoURI(req.RepositoryName),
		createdAt:   time.Now().UTC(),
		tags:        tags,
		manifests:   map[string][]byte{},
		mediaTypes:  map[string]string{},
		tagToDigest: map[string]string{},
	}
	s.repos[req.RepositoryName] = repo
	s.mu.Unlock()

	jsonWrite(w, http.StatusOK, map[string]interface{}{"repository": repoMeta(repo)})
}

func (s *Service) deleteRepository(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RepositoryName string `json:"repositoryName"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.Lock()
	repo, ok := s.repos[req.RepositoryName]
	if ok {
		delete(s.repos, req.RepositoryName)
	}
	s.mu.Unlock()

	if !ok {
		jsonError(w, http.StatusBadRequest, "RepositoryNotFoundException",
			fmt.Sprintf("Repository %s not found", req.RepositoryName))
		return
	}

	jsonWrite(w, http.StatusOK, map[string]interface{}{"repository": repoMeta(repo)})
}

func (s *Service) describeRepositories(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RepositoryNames []string `json:"repositoryNames"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.RLock()
	defer s.mu.RUnlock()

	var repos []map[string]interface{}
	for _, repo := range s.repos {
		if len(req.RepositoryNames) > 0 && !contains(req.RepositoryNames, repo.name) {
			continue
		}
		repos = append(repos, repoMeta(repo))
	}
	if repos == nil {
		repos = []map[string]interface{}{}
	}
	jsonWrite(w, http.StatusOK, map[string]interface{}{"repositories": repos})
}

func (s *Service) listImages(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RepositoryName string `json:"repositoryName"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.RLock()
	repo, ok := s.repos[req.RepositoryName]
	s.mu.RUnlock()

	if !ok {
		jsonError(w, http.StatusBadRequest, "RepositoryNotFoundException",
			fmt.Sprintf("Repository %s not found", req.RepositoryName))
		return
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	type imageID struct {
		ImageTag    string `json:"imageTag,omitempty"`
		ImageDigest string `json:"imageDigest,omitempty"`
	}
	seen := map[string]bool{}
	var ids []imageID
	for tag, digest := range repo.tagToDigest {
		ids = append(ids, imageID{ImageTag: tag, ImageDigest: digest})
		seen[digest] = true
	}
	for digest := range repo.manifests {
		if !seen[digest] {
			ids = append(ids, imageID{ImageDigest: digest})
		}
	}
	if ids == nil {
		ids = []imageID{}
	}
	jsonWrite(w, http.StatusOK, map[string]interface{}{"imageIds": ids})
}

func (s *Service) describeImages(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RepositoryName string `json:"repositoryName"`
		ImageIDs       []struct {
			ImageTag    string `json:"imageTag"`
			ImageDigest string `json:"imageDigest"`
		} `json:"imageIds"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.RLock()
	repo, ok := s.repos[req.RepositoryName]
	s.mu.RUnlock()

	if !ok {
		jsonError(w, http.StatusBadRequest, "RepositoryNotFoundException",
			fmt.Sprintf("Repository %s not found", req.RepositoryName))
		return
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	// Build digest -> tags reverse map
	digestTags := map[string][]string{}
	for tag, digest := range repo.tagToDigest {
		digestTags[digest] = append(digestTags[digest], tag)
	}

	// Collect requested digests
	var digests []string
	if len(req.ImageIDs) == 0 {
		for d := range repo.manifests {
			digests = append(digests, d)
		}
	} else {
		for _, id := range req.ImageIDs {
			if id.ImageDigest != "" {
				digests = append(digests, id.ImageDigest)
			} else if id.ImageTag != "" {
				if d, ok := repo.tagToDigest[id.ImageTag]; ok {
					digests = append(digests, d)
				}
			}
		}
	}

	type detail struct {
		RegistryID             string   `json:"registryId"`
		RepositoryName         string   `json:"repositoryName"`
		ImageDigest            string   `json:"imageDigest"`
		ImageTags              []string `json:"imageTags"`
		ImageSizeInBytes       int      `json:"imageSizeInBytes"`
		ImagePushedAt          float64  `json:"imagePushedAt"`
		ImageManifestMediaType string   `json:"imageManifestMediaType"`
	}
	var details []detail
	for _, d := range digests {
		manifest, ok := repo.manifests[d]
		if !ok {
			continue
		}
		tags := digestTags[d]
		if tags == nil {
			tags = []string{}
		}
		details = append(details, detail{
			RegistryID:             accountID,
			RepositoryName:         repo.name,
			ImageDigest:            d,
			ImageTags:              tags,
			ImageSizeInBytes:       len(manifest),
			ImagePushedAt:          float64(time.Now().Unix()),
			ImageManifestMediaType: repo.mediaTypes[d],
		})
	}
	if details == nil {
		details = []detail{}
	}
	jsonWrite(w, http.StatusOK, map[string]interface{}{"imageDetails": details})
}

func (s *Service) batchDeleteImage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RepositoryName string `json:"repositoryName"`
		ImageIDs       []struct {
			ImageTag    string `json:"imageTag"`
			ImageDigest string `json:"imageDigest"`
		} `json:"imageIds"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.Lock()
	repo, ok := s.repos[req.RepositoryName]
	if !ok {
		s.mu.Unlock()
		jsonError(w, http.StatusBadRequest, "RepositoryNotFoundException",
			fmt.Sprintf("Repository %s not found", req.RepositoryName))
		return
	}

	type imageID struct {
		ImageTag    string `json:"imageTag,omitempty"`
		ImageDigest string `json:"imageDigest,omitempty"`
	}
	var deleted []imageID
	for _, id := range req.ImageIDs {
		if id.ImageTag != "" {
			if digest, ok := repo.tagToDigest[id.ImageTag]; ok {
				delete(repo.tagToDigest, id.ImageTag)
				deleted = append(deleted, imageID{ImageTag: id.ImageTag, ImageDigest: digest})
			}
		} else if id.ImageDigest != "" {
			if _, ok := repo.manifests[id.ImageDigest]; ok {
				delete(repo.manifests, id.ImageDigest)
				delete(repo.mediaTypes, id.ImageDigest)
				for tag, digest := range repo.tagToDigest {
					if digest == id.ImageDigest {
						delete(repo.tagToDigest, tag)
					}
				}
				deleted = append(deleted, imageID{ImageDigest: id.ImageDigest})
			}
		}
	}
	s.mu.Unlock()

	if deleted == nil {
		deleted = []imageID{}
	}
	jsonWrite(w, http.StatusOK, map[string]interface{}{
		"imageIds": deleted,
		"failures": []interface{}{},
	})
}

func (s *Service) batchGetImage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RepositoryName string `json:"repositoryName"`
		ImageIDs       []struct {
			ImageTag    string `json:"imageTag"`
			ImageDigest string `json:"imageDigest"`
		} `json:"imageIds"`
		AcceptedMediaTypes []string `json:"acceptedMediaTypes"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.RLock()
	repo, ok := s.repos[req.RepositoryName]
	s.mu.RUnlock()

	if !ok {
		jsonError(w, http.StatusBadRequest, "RepositoryNotFoundException",
			fmt.Sprintf("Repository %s not found", req.RepositoryName))
		return
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	type imageItem struct {
		ImageID struct {
			ImageTag    string `json:"imageTag,omitempty"`
			ImageDigest string `json:"imageDigest,omitempty"`
		} `json:"imageId"`
		ImageManifest          string `json:"imageManifest"`
		ImageManifestMediaType string `json:"imageManifestMediaType"`
	}
	var images []imageItem
	for _, id := range req.ImageIDs {
		var digest, tag string
		if id.ImageDigest != "" {
			digest = id.ImageDigest
		} else if id.ImageTag != "" {
			tag = id.ImageTag
			digest = repo.tagToDigest[tag]
		}
		if manifest, ok := repo.manifests[digest]; ok {
			item := imageItem{
				ImageManifest:          string(manifest),
				ImageManifestMediaType: repo.mediaTypes[digest],
			}
			item.ImageID.ImageDigest = digest
			item.ImageID.ImageTag = tag
			images = append(images, item)
		}
	}
	if images == nil {
		images = []imageItem{}
	}
	jsonWrite(w, http.StatusOK, map[string]interface{}{"images": images, "failures": []interface{}{}})
}

func (s *Service) getAuthorizationToken(w http.ResponseWriter, r *http.Request) {
	token := base64.StdEncoding.EncodeToString([]byte("AWS:nimbus-local-token"))
	jsonWrite(w, http.StatusOK, map[string]interface{}{
		"authorizationData": []map[string]interface{}{
			{
				"authorizationToken": token,
				"expiresAt":          float64(time.Now().Add(12 * time.Hour).Unix()),
				"proxyEndpoint":      fmt.Sprintf("http://localhost:4566"),
			},
		},
	})
}

func (s *Service) tagResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceArn string              `json:"resourceArn"`
		Tags        []map[string]string `json:"tags"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	name := arnToRepoName(req.ResourceArn)
	s.mu.Lock()
	if repo, ok := s.repos[name]; ok {
		for _, t := range req.Tags {
			repo.tags[t["Key"]] = t["Value"]
		}
	}
	s.mu.Unlock()

	jsonWrite(w, http.StatusOK, map[string]interface{}{})
}

func (s *Service) untagResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceArn string   `json:"resourceArn"`
		TagKeys     []string `json:"tagKeys"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	name := arnToRepoName(req.ResourceArn)
	s.mu.Lock()
	if repo, ok := s.repos[name]; ok {
		for _, k := range req.TagKeys {
			delete(repo.tags, k)
		}
	}
	s.mu.Unlock()

	jsonWrite(w, http.StatusOK, map[string]interface{}{})
}

func (s *Service) listTagsForResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceArn string `json:"resourceArn"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	name := arnToRepoName(req.ResourceArn)
	s.mu.RLock()
	repo, ok := s.repos[name]
	s.mu.RUnlock()

	if !ok {
		jsonError(w, http.StatusBadRequest, "RepositoryNotFoundException", "Repository not found")
		return
	}

	s.mu.RLock()
	type tag struct {
		Key   string `json:"Key"`
		Value string `json:"Value"`
	}
	tags := []tag{}
	for k, v := range repo.tags {
		tags = append(tags, tag{Key: k, Value: v})
	}
	s.mu.RUnlock()

	jsonWrite(w, http.StatusOK, map[string]interface{}{"tags": tags})
}

// --- Helpers ---

func repoMeta(r *repository) map[string]interface{} {
	return map[string]interface{}{
		"repositoryArn":              r.arn,
		"registryId":                 accountID,
		"repositoryName":             r.name,
		"repositoryUri":              r.uri,
		"createdAt":                  float64(r.createdAt.Unix()),
		"imageTagMutability":         "MUTABLE",
		"imageScanningConfiguration": map[string]bool{"scanOnPush": false},
		"encryptionConfiguration":    map[string]string{"encryptionType": "AES256"},
	}
}

func arnToRepoName(arn string) string {
	// arn:aws:ecr:region:account:repository/name
	if idx := strings.LastIndex(arn, "/"); idx != -1 {
		return arn[idx+1:]
	}
	return arn
}

func contains(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}

func newUploadID() string { return uid.New() }

func jsonWrite(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{
		"__type":  code,
		"message": message,
	})
}
