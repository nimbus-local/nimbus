// Package efs emulates the AWS Elastic File System (EFS) API.
// Supports the REST endpoints needed by the Pulumi AWS provider v7:
// FileSystem, MountTarget, AccessPoint CRUD and tag operations.
// All state is in-memory; no actual NFS is created.
package efs

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/nimbus-local/nimbus/internal/uid"
)

const account = "000000000000"

// Service implements the AWS EFS emulator.
type Service struct {
	mu           sync.RWMutex
	fileSystems  map[string]*fileSystem  // id -> fs
	mountTargets map[string]*mountTarget // id -> mt
	accessPoints map[string]*accessPoint // id -> ap
	region       string
}

type fileSystem struct {
	id              string
	arn             string
	creationToken   string
	encrypted       bool
	lifeCycleState  string
	performanceMode string
	throughputMode  string
	tags            map[string]string
	createdAt       time.Time
}

type mountTarget struct {
	id             string
	fileSystemID   string
	subnetID       string
	lifeCycleState string
}

type accessPoint struct {
	id             string
	arn            string
	fileSystemID   string
	clientToken    string
	posixUser      *posixUserReq
	rootDirectory  *rootDirReq
	tags           map[string]string
	lifeCycleState string
}

// ── request / response types ──────────────────────────────────────────────────

type tagPair struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

type posixUserReq struct {
	Uid           int64   `json:"Uid"`
	Gid           int64   `json:"Gid"`
	SecondaryGids []int64 `json:"SecondaryGids,omitempty"`
}

type creationInfoReq struct {
	OwnerUid    int64  `json:"OwnerUid"`
	OwnerGid    int64  `json:"OwnerGid"`
	Permissions string `json:"Permissions"`
}

type rootDirReq struct {
	Path         string           `json:"Path,omitempty"`
	CreationInfo *creationInfoReq `json:"CreationInfo,omitempty"`
}

// ── constructor ───────────────────────────────────────────────────────────────

func New(region string) *Service {
	if region == "" {
		region = "us-east-1"
	}
	return &Service{
		fileSystems:  map[string]*fileSystem{},
		mountTargets: map[string]*mountTarget{},
		accessPoints: map[string]*accessPoint{},
		region:       region,
	}
}

// Reset clears all in-memory state.
func (s *Service) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fileSystems = map[string]*fileSystem{}
	s.mountTargets = map[string]*mountTarget{}
	s.accessPoints = map[string]*accessPoint{}
}

// FileSystemCount returns the number of file systems (for /_nimbus/state).
func (s *Service) FileSystemCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.fileSystems)
}

// ── Service interface ─────────────────────────────────────────────────────────

func (s *Service) Name() string { return "efs" }

// Detect identifies EFS REST API requests by the /2015-02-01/ path prefix.
func (s *Service) Detect(r *http.Request) bool {
	return strings.HasPrefix(r.URL.Path, "/2015-02-01/")
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path

	switch {
	// ── File systems ──────────────────────────────────────────────────────────
	case (p == "/2015-02-01/file-systems" || p == "/2015-02-01/file-systems/"):
		switch r.Method {
		case http.MethodPost:
			s.createFileSystem(w, r)
		case http.MethodGet:
			s.describeFileSystems(w, r)
		default:
			writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		}
	case strings.HasPrefix(p, "/2015-02-01/file-systems/") && strings.HasSuffix(p, "/lifecycle-configuration"):
		rest := strings.TrimPrefix(p, "/2015-02-01/file-systems/")
		id := strings.TrimSuffix(rest, "/lifecycle-configuration")
		switch r.Method {
		case http.MethodGet:
			s.describeLifecycleConfiguration(w, r, id)
		case http.MethodPut:
			s.putLifecycleConfiguration(w, r, id)
		default:
			writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		}
	case strings.HasPrefix(p, "/2015-02-01/file-systems/"):
		id := strings.TrimPrefix(p, "/2015-02-01/file-systems/")
		switch r.Method {
		case http.MethodGet:
			s.getFileSystem(w, r, id)
		case http.MethodDelete:
			s.deleteFileSystem(w, r, id)
		default:
			writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		}

	// ── Mount targets ─────────────────────────────────────────────────────────
	case (p == "/2015-02-01/mount-targets" || p == "/2015-02-01/mount-targets/"):
		switch r.Method {
		case http.MethodPost:
			s.createMountTarget(w, r)
		case http.MethodGet:
			s.describeMountTargets(w, r)
		default:
			writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		}
	case strings.HasPrefix(p, "/2015-02-01/mount-targets/"):
		id := strings.TrimPrefix(p, "/2015-02-01/mount-targets/")
		switch r.Method {
		case http.MethodGet:
			s.getMountTarget(w, r, id)
		case http.MethodDelete:
			s.deleteMountTarget(w, r, id)
		default:
			writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		}

	// ── Access points ─────────────────────────────────────────────────────────
	case (p == "/2015-02-01/access-points" || p == "/2015-02-01/access-points/"):
		switch r.Method {
		case http.MethodPost:
			s.createAccessPoint(w, r)
		case http.MethodGet:
			s.describeAccessPoints(w, r)
		default:
			writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		}
	case strings.HasPrefix(p, "/2015-02-01/access-points/"):
		id := strings.TrimPrefix(p, "/2015-02-01/access-points/")
		switch r.Method {
		case http.MethodGet:
			s.getAccessPoint(w, r, id)
		case http.MethodDelete:
			s.deleteAccessPoint(w, r, id)
		default:
			writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		}

	// ── Tagging (newer API: /2015-02-01/resource-tags/{id}) ──────────────────
	case strings.HasPrefix(p, "/2015-02-01/resource-tags/"):
		id := strings.TrimPrefix(p, "/2015-02-01/resource-tags/")
		switch r.Method {
		case http.MethodPost:
			s.tagResource(w, r, id)
		case http.MethodDelete:
			s.untagResource(w, r, id)
		case http.MethodGet:
			s.listTagsForResource(w, r, id)
		default:
			writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		}

	default:
		writeError(w, http.StatusNotFound, "UnsupportedOperation", "path not supported: "+p)
	}
}

// ── File system operations ────────────────────────────────────────────────────

func (s *Service) createFileSystem(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CreationToken   string    `json:"CreationToken"`
		Encrypted       bool      `json:"Encrypted"`
		PerformanceMode string    `json:"PerformanceMode"`
		ThroughputMode  string    `json:"ThroughputMode"`
		Tags            []tagPair `json:"Tags"`
	}
	if !decode(w, r, &req) {
		return
	}

	perf := req.PerformanceMode
	if perf == "" {
		perf = "generalPurpose"
	}
	thru := req.ThroughputMode
	if thru == "" {
		thru = "bursting"
	}
	token := req.CreationToken
	if token == "" {
		token = uid.New()
	}

	s.mu.Lock()
	// Idempotency: return existing fs if same creation token.
	for _, fs := range s.fileSystems {
		if fs.creationToken == token {
			s.mu.Unlock()
			writeJSON(w, http.StatusOK, s.fsView(fs))
			return
		}
	}
	id := newFSID()
	fs := &fileSystem{
		id:              id,
		arn:             s.fsARN(id),
		creationToken:   token,
		encrypted:       req.Encrypted,
		lifeCycleState:  "available",
		performanceMode: perf,
		throughputMode:  thru,
		tags:            tagsFromList(req.Tags),
		createdAt:       time.Now(),
	}
	s.fileSystems[id] = fs
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, s.fsView(fs))
}

func (s *Service) describeFileSystems(w http.ResponseWriter, r *http.Request) {
	filterID := r.URL.Query().Get("FileSystemId")
	s.mu.RLock()
	defer s.mu.RUnlock()

	var out []map[string]any
	for _, fs := range s.fileSystems {
		if filterID != "" && fs.id != filterID {
			continue
		}
		out = append(out, s.fsView(fs))
	}
	if out == nil {
		out = []map[string]any{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"FileSystems": out,
		"Marker":      nil,
		"NextMarker":  nil,
	})
}

func (s *Service) getFileSystem(w http.ResponseWriter, r *http.Request, id string) {
	s.mu.RLock()
	fs, ok := s.fileSystems[id]
	s.mu.RUnlock()
	if !ok {
		writeError(w, http.StatusNotFound, "FileSystemNotFound",
			fmt.Sprintf("File system '%s' does not exist.", id))
		return
	}
	writeJSON(w, http.StatusOK, s.fsView(fs))
}

func (s *Service) deleteFileSystem(w http.ResponseWriter, r *http.Request, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.fileSystems[id]; !ok {
		writeError(w, http.StatusNotFound, "FileSystemNotFound",
			fmt.Sprintf("File system '%s' does not exist.", id))
		return
	}
	delete(s.fileSystems, id)
	w.WriteHeader(http.StatusNoContent)
}

// ── Lifecycle configuration (stub — no lifecycle policies stored) ─────────────

func (s *Service) describeLifecycleConfiguration(w http.ResponseWriter, r *http.Request, id string) {
	s.mu.RLock()
	_, ok := s.fileSystems[id]
	s.mu.RUnlock()
	if !ok {
		writeError(w, http.StatusNotFound, "FileSystemNotFound",
			fmt.Sprintf("File system '%s' does not exist.", id))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"LifecyclePolicies": []any{}})
}

func (s *Service) putLifecycleConfiguration(w http.ResponseWriter, r *http.Request, id string) {
	s.mu.RLock()
	_, ok := s.fileSystems[id]
	s.mu.RUnlock()
	if !ok {
		writeError(w, http.StatusNotFound, "FileSystemNotFound",
			fmt.Sprintf("File system '%s' does not exist.", id))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"LifecyclePolicies": []any{}})
}

// ── Mount target operations ───────────────────────────────────────────────────

func (s *Service) createMountTarget(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FileSystemID   string   `json:"FileSystemId"`
		SubnetID       string   `json:"SubnetId"`
		SecurityGroups []string `json:"SecurityGroups"`
		IPAddress      string   `json:"IpAddress"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.FileSystemID == "" {
		writeError(w, http.StatusBadRequest, "BadRequest", "FileSystemId is required")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.fileSystems[req.FileSystemID]; !ok {
		writeError(w, http.StatusNotFound, "FileSystemNotFound",
			fmt.Sprintf("File system '%s' does not exist.", req.FileSystemID))
		return
	}
	id := newMTID()
	mt := &mountTarget{
		id:             id,
		fileSystemID:   req.FileSystemID,
		subnetID:       req.SubnetID,
		lifeCycleState: "available",
	}
	s.mountTargets[id] = mt
	writeJSON(w, http.StatusOK, s.mtView(mt))
}

func (s *Service) describeMountTargets(w http.ResponseWriter, r *http.Request) {
	filterFS := r.URL.Query().Get("FileSystemId")
	filterMT := r.URL.Query().Get("MountTargetId")
	s.mu.RLock()
	defer s.mu.RUnlock()

	var out []map[string]any
	for _, mt := range s.mountTargets {
		if filterFS != "" && mt.fileSystemID != filterFS {
			continue
		}
		if filterMT != "" && mt.id != filterMT {
			continue
		}
		out = append(out, s.mtView(mt))
	}
	if out == nil {
		out = []map[string]any{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"MountTargets": out,
		"Marker":       nil,
		"NextMarker":   nil,
	})
}

func (s *Service) getMountTarget(w http.ResponseWriter, r *http.Request, id string) {
	s.mu.RLock()
	mt, ok := s.mountTargets[id]
	s.mu.RUnlock()
	if !ok {
		writeError(w, http.StatusNotFound, "MountTargetNotFound",
			fmt.Sprintf("Mount target '%s' does not exist.", id))
		return
	}
	writeJSON(w, http.StatusOK, s.mtView(mt))
}

func (s *Service) deleteMountTarget(w http.ResponseWriter, r *http.Request, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.mountTargets[id]; !ok {
		writeError(w, http.StatusNotFound, "MountTargetNotFound",
			fmt.Sprintf("Mount target '%s' does not exist.", id))
		return
	}
	delete(s.mountTargets, id)
	w.WriteHeader(http.StatusNoContent)
}

// ── Access point operations ───────────────────────────────────────────────────

func (s *Service) createAccessPoint(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FileSystemID  string        `json:"FileSystemId"`
		ClientToken   string        `json:"ClientToken"`
		PosixUser     *posixUserReq `json:"PosixUser"`
		RootDirectory *rootDirReq   `json:"RootDirectory"`
		Tags          []tagPair     `json:"Tags"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.FileSystemID == "" {
		writeError(w, http.StatusBadRequest, "BadRequest", "FileSystemId is required")
		return
	}

	token := req.ClientToken
	if token == "" {
		token = uid.New()
	}

	s.mu.Lock()
	// Idempotency on ClientToken.
	for _, ap := range s.accessPoints {
		if ap.clientToken == token {
			s.mu.Unlock()
			writeJSON(w, http.StatusOK, s.apView(ap))
			return
		}
	}
	if _, ok := s.fileSystems[req.FileSystemID]; !ok {
		s.mu.Unlock()
		writeError(w, http.StatusNotFound, "FileSystemNotFound",
			fmt.Sprintf("File system '%s' does not exist.", req.FileSystemID))
		return
	}
	id := newAPID()
	ap := &accessPoint{
		id:             id,
		arn:            s.apARN(id),
		fileSystemID:   req.FileSystemID,
		clientToken:    token,
		posixUser:      req.PosixUser,
		rootDirectory:  req.RootDirectory,
		tags:           tagsFromList(req.Tags),
		lifeCycleState: "available",
	}
	s.accessPoints[id] = ap
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, s.apView(ap))
}

func (s *Service) describeAccessPoints(w http.ResponseWriter, r *http.Request) {
	filterFS := r.URL.Query().Get("FileSystemId")
	filterAP := r.URL.Query().Get("AccessPointId")
	s.mu.RLock()
	defer s.mu.RUnlock()

	var out []map[string]any
	for _, ap := range s.accessPoints {
		if filterFS != "" && ap.fileSystemID != filterFS {
			continue
		}
		if filterAP != "" && ap.id != filterAP {
			continue
		}
		out = append(out, s.apView(ap))
	}
	if out == nil {
		out = []map[string]any{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"AccessPoints": out,
		"NextToken":    nil,
	})
}

func (s *Service) getAccessPoint(w http.ResponseWriter, r *http.Request, id string) {
	s.mu.RLock()
	ap, ok := s.accessPoints[id]
	s.mu.RUnlock()
	if !ok {
		writeError(w, http.StatusNotFound, "AccessPointNotFound",
			fmt.Sprintf("Access point '%s' does not exist.", id))
		return
	}
	writeJSON(w, http.StatusOK, s.apView(ap))
}

func (s *Service) deleteAccessPoint(w http.ResponseWriter, r *http.Request, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.accessPoints[id]; !ok {
		writeError(w, http.StatusNotFound, "AccessPointNotFound",
			fmt.Sprintf("Access point '%s' does not exist.", id))
		return
	}
	delete(s.accessPoints, id)
	w.WriteHeader(http.StatusNoContent)
}

// ── Tagging operations ────────────────────────────────────────────────────────

func (s *Service) tagResource(w http.ResponseWriter, r *http.Request, resourceID string) {
	var req struct {
		Tags map[string]string `json:"Tags"`
	}
	if !decode(w, r, &req) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tags := s.resolveTags(resourceID)
	if tags == nil {
		writeError(w, http.StatusNotFound, "ResourceNotFound",
			fmt.Sprintf("Resource '%s' not found.", resourceID))
		return
	}
	for k, v := range req.Tags {
		tags[k] = v
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Service) untagResource(w http.ResponseWriter, r *http.Request, resourceID string) {
	keys := r.URL.Query()["tagKeys"]
	s.mu.Lock()
	defer s.mu.Unlock()
	tags := s.resolveTags(resourceID)
	if tags == nil {
		writeError(w, http.StatusNotFound, "ResourceNotFound",
			fmt.Sprintf("Resource '%s' not found.", resourceID))
		return
	}
	for _, k := range keys {
		delete(tags, k)
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Service) listTagsForResource(w http.ResponseWriter, r *http.Request, resourceID string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tags := s.resolveTags(resourceID)
	if tags == nil {
		writeError(w, http.StatusNotFound, "ResourceNotFound",
			fmt.Sprintf("Resource '%s' not found.", resourceID))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"Tags": tags})
}

// resolveTags returns the tag map for a resource ID (fs-, fsmt-, fsap-) or nil.
// Must be called under at least a read lock (write lock for mutations).
func (s *Service) resolveTags(id string) map[string]string {
	if fs, ok := s.fileSystems[id]; ok {
		return fs.tags
	}
	if mt, ok := s.mountTargets[id]; ok {
		_ = mt
		// Mount targets don't support tags in AWS, but return empty map to avoid errors.
		return map[string]string{}
	}
	if ap, ok := s.accessPoints[id]; ok {
		return ap.tags
	}
	return nil
}

// ── View builders ─────────────────────────────────────────────────────────────

func (s *Service) fsView(fs *fileSystem) map[string]any {
	return map[string]any{
		"OwnerId":              account,
		"CreationToken":        fs.creationToken,
		"FileSystemId":         fs.id,
		"FileSystemArn":        fs.arn,
		"CreationTime":         fs.createdAt.Unix(),
		"LifeCycleState":       fs.lifeCycleState,
		"NumberOfMountTargets": 0,
		"SizeInBytes":          map[string]any{"Value": 0, "ValueInIA": 0, "ValueInStandard": 0},
		"PerformanceMode":      fs.performanceMode,
		"Encrypted":            fs.encrypted,
		"ThroughputMode":       fs.throughputMode,
		"Tags":                 tagsToList(fs.tags),
	}
}

func (s *Service) mtView(mt *mountTarget) map[string]any {
	return map[string]any{
		"OwnerId":              account,
		"MountTargetId":        mt.id,
		"FileSystemId":         mt.fileSystemID,
		"SubnetId":             mt.subnetID,
		"LifeCycleState":       mt.lifeCycleState,
		"IpAddress":            "10.0.0.100",
		"NetworkInterfaceId":   "",
		"AvailabilityZoneId":   "",
		"AvailabilityZoneName": "",
		"VpcId":                "",
	}
}

func (s *Service) apView(ap *accessPoint) map[string]any {
	view := map[string]any{
		"OwnerId":        account,
		"ClientToken":    ap.clientToken,
		"AccessPointId":  ap.id,
		"AccessPointArn": ap.arn,
		"FileSystemId":   ap.fileSystemID,
		"LifeCycleState": ap.lifeCycleState,
		"Tags":           tagsToList(ap.tags),
	}
	if ap.posixUser != nil {
		view["PosixUser"] = ap.posixUser
	}
	if ap.rootDirectory != nil {
		view["RootDirectory"] = ap.rootDirectory
	}
	return view
}

// ── ARN / ID helpers ──────────────────────────────────────────────────────────

func (s *Service) fsARN(id string) string {
	return fmt.Sprintf("arn:aws:elasticfilesystem:%s:%s:file-system/%s", s.region, account, id)
}

func (s *Service) apARN(id string) string {
	return fmt.Sprintf("arn:aws:elasticfilesystem:%s:%s:access-point/%s", s.region, account, id)
}

func newFSID() string { return "fs-" + shortID() }
func newMTID() string { return "fsmt-" + shortID() }
func newAPID() string { return "fsap-" + shortID() }

func shortID() string {
	u := strings.ReplaceAll(uid.New(), "-", "")
	return u[:8]
}

// ── Tag conversion helpers ────────────────────────────────────────────────────

func tagsFromList(pairs []tagPair) map[string]string {
	m := make(map[string]string, len(pairs))
	for _, p := range pairs {
		m[p.Key] = p.Value
	}
	return m
}

func tagsToList(m map[string]string) []tagPair {
	out := make([]tagPair, 0, len(m))
	for k, v := range m {
		out = append(out, tagPair{Key: k, Value: v})
	}
	return out
}

// ── HTTP helpers ──────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("x-amzn-ErrorType", code)
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{ //nolint:errcheck
		"errorCode": code,
		"message":   msg,
	})
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if r.Body == nil || r.ContentLength == 0 {
		return true // some GET-like calls have no body
	}
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "BadRequest", "invalid request body: "+err.Error())
		return false
	}
	return true
}
