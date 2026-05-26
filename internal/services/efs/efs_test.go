package efs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestService() *Service { return New("us-east-1") }

func efsReq(t *testing.T, svc *Service, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	return w
}

func parseBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.NewDecoder(w.Body).Decode(&m); err != nil {
		t.Fatalf("decode body: %v\nraw: %s", err, w.Body.String())
	}
	return m
}

func str(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

// ── Detect ────────────────────────────────────────────────────────────────────

func TestDetect(t *testing.T) {
	svc := newTestService()
	req := httptest.NewRequest(http.MethodPost, "/2015-02-01/file-systems", nil)
	if !svc.Detect(req) {
		t.Fatal("Detect should return true for /2015-02-01/ path")
	}
}

func TestDetectNoMatch(t *testing.T) {
	svc := newTestService()
	req := httptest.NewRequest(http.MethodPost, "/2020-05-31/something", nil)
	if svc.Detect(req) {
		t.Fatal("Detect should return false for non-EFS path")
	}
}

// ── FileSystem ────────────────────────────────────────────────────────────────

func TestCreateFileSystem(t *testing.T) {
	svc := newTestService()
	w := efsReq(t, svc, http.MethodPost, "/2015-02-01/file-systems", map[string]any{
		"CreationToken": "token-1",
		"Encrypted":     true,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	b := parseBody(t, w)
	id := str(b, "FileSystemId")
	if id == "" {
		t.Fatal("FileSystemId missing from response")
	}
	if !contains(id, "fs-") {
		t.Errorf("FileSystemId %q should start with fs-", id)
	}
	arn := str(b, "FileSystemArn")
	if !contains(arn, "file-system/"+id) {
		t.Errorf("FileSystemArn %q should contain file-system/%s", arn, id)
	}
}

func TestCreateFileSystemEncryptedDefault(t *testing.T) {
	svc := newTestService()
	w := efsReq(t, svc, http.MethodPost, "/2015-02-01/file-systems", map[string]any{
		"CreationToken": "token-enc",
		"Encrypted":     true,
	})
	b := parseBody(t, w)
	if enc, _ := b["Encrypted"].(bool); !enc {
		t.Error("Encrypted should be true in response")
	}
}

func TestCreateFileSystemIdempotent(t *testing.T) {
	svc := newTestService()
	body := map[string]any{"CreationToken": "same-token"}
	w1 := efsReq(t, svc, http.MethodPost, "/2015-02-01/file-systems", body)
	w2 := efsReq(t, svc, http.MethodPost, "/2015-02-01/file-systems", body)
	id1 := str(parseBody(t, w1), "FileSystemId")
	id2 := str(parseBody(t, w2), "FileSystemId")
	if id1 != id2 {
		t.Errorf("idempotent create returned different IDs: %s vs %s", id1, id2)
	}
	if svc.FileSystemCount() != 1 {
		t.Errorf("expected 1 filesystem after idempotent create, got %d", svc.FileSystemCount())
	}
}

func TestDescribeFileSystems(t *testing.T) {
	svc := newTestService()
	efsReq(t, svc, http.MethodPost, "/2015-02-01/file-systems", map[string]any{"CreationToken": "t1"})
	efsReq(t, svc, http.MethodPost, "/2015-02-01/file-systems", map[string]any{"CreationToken": "t2"})

	w := efsReq(t, svc, http.MethodGet, "/2015-02-01/file-systems", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	b := parseBody(t, w)
	fsList, ok := b["FileSystems"].([]any)
	if !ok {
		t.Fatal("FileSystems missing from response")
	}
	if len(fsList) != 2 {
		t.Errorf("expected 2 file systems, got %d", len(fsList))
	}
}

func TestDescribeFileSystemsFilterByID(t *testing.T) {
	svc := newTestService()
	w1 := efsReq(t, svc, http.MethodPost, "/2015-02-01/file-systems", map[string]any{"CreationToken": "t1"})
	efsReq(t, svc, http.MethodPost, "/2015-02-01/file-systems", map[string]any{"CreationToken": "t2"})
	id := str(parseBody(t, w1), "FileSystemId")

	w := efsReq(t, svc, http.MethodGet, "/2015-02-01/file-systems?FileSystemId="+id, nil)
	b := parseBody(t, w)
	fsList := b["FileSystems"].([]any)
	if len(fsList) != 1 {
		t.Errorf("expected 1 filesystem with filter, got %d", len(fsList))
	}
}

func TestGetFileSystemByPath(t *testing.T) {
	svc := newTestService()
	w := efsReq(t, svc, http.MethodPost, "/2015-02-01/file-systems", map[string]any{"CreationToken": "t"})
	id := str(parseBody(t, w), "FileSystemId")

	w2 := efsReq(t, svc, http.MethodGet, "/2015-02-01/file-systems/"+id, nil)
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w2.Code, w2.Body.String())
	}
	if got := str(parseBody(t, w2), "FileSystemId"); got != id {
		t.Errorf("got FileSystemId %q, want %q", got, id)
	}
}

func TestGetFileSystemNotFound(t *testing.T) {
	svc := newTestService()
	w := efsReq(t, svc, http.MethodGet, "/2015-02-01/file-systems/fs-notexist", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestDeleteFileSystem(t *testing.T) {
	svc := newTestService()
	w := efsReq(t, svc, http.MethodPost, "/2015-02-01/file-systems", map[string]any{"CreationToken": "del"})
	id := str(parseBody(t, w), "FileSystemId")

	wd := efsReq(t, svc, http.MethodDelete, "/2015-02-01/file-systems/"+id, nil)
	if wd.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", wd.Code)
	}
	if svc.FileSystemCount() != 0 {
		t.Error("filesystem not deleted")
	}
}

func TestDeleteFileSystemNotFound(t *testing.T) {
	svc := newTestService()
	w := efsReq(t, svc, http.MethodDelete, "/2015-02-01/file-systems/fs-notexist", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestCreateFileSystemWithTags(t *testing.T) {
	svc := newTestService()
	w := efsReq(t, svc, http.MethodPost, "/2015-02-01/file-systems", map[string]any{
		"CreationToken": "tagged",
		"Tags": []map[string]string{
			{"Key": "forge:app", "Value": "myapp"},
			{"Key": "forge:stage", "Value": "test"},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	b := parseBody(t, w)
	tags, ok := b["Tags"].([]any)
	if !ok || len(tags) != 2 {
		t.Errorf("expected 2 tags in response, got %v", b["Tags"])
	}
}

// ── MountTarget ───────────────────────────────────────────────────────────────

func createFS(t *testing.T, svc *Service) string {
	t.Helper()
	w := efsReq(t, svc, http.MethodPost, "/2015-02-01/file-systems",
		map[string]any{"CreationToken": fmt.Sprintf("fs-%s", t.Name())})
	return str(parseBody(t, w), "FileSystemId")
}

func TestCreateMountTarget(t *testing.T) {
	svc := newTestService()
	fsID := createFS(t, svc)

	w := efsReq(t, svc, http.MethodPost, "/2015-02-01/mount-targets", map[string]any{
		"FileSystemId": fsID,
		"SubnetId":     "subnet-00000001",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	b := parseBody(t, w)
	mtID := str(b, "MountTargetId")
	if !contains(mtID, "fsmt-") {
		t.Errorf("MountTargetId %q should start with fsmt-", mtID)
	}
	if got := str(b, "FileSystemId"); got != fsID {
		t.Errorf("FileSystemId = %q, want %q", got, fsID)
	}
}

func TestCreateMountTargetFSNotFound(t *testing.T) {
	svc := newTestService()
	w := efsReq(t, svc, http.MethodPost, "/2015-02-01/mount-targets", map[string]any{
		"FileSystemId": "fs-notexist",
		"SubnetId":     "subnet-1",
	})
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestDescribeMountTargetsByFS(t *testing.T) {
	svc := newTestService()
	fsID := createFS(t, svc)
	efsReq(t, svc, http.MethodPost, "/2015-02-01/mount-targets",
		map[string]any{"FileSystemId": fsID, "SubnetId": "subnet-1"})
	efsReq(t, svc, http.MethodPost, "/2015-02-01/mount-targets",
		map[string]any{"FileSystemId": fsID, "SubnetId": "subnet-2"})

	w := efsReq(t, svc, http.MethodGet,
		"/2015-02-01/mount-targets?FileSystemId="+fsID, nil)
	b := parseBody(t, w)
	mts := b["MountTargets"].([]any)
	if len(mts) != 2 {
		t.Errorf("expected 2 mount targets, got %d", len(mts))
	}
}

func TestDeleteMountTarget(t *testing.T) {
	svc := newTestService()
	fsID := createFS(t, svc)
	w := efsReq(t, svc, http.MethodPost, "/2015-02-01/mount-targets",
		map[string]any{"FileSystemId": fsID, "SubnetId": "subnet-1"})
	mtID := str(parseBody(t, w), "MountTargetId")

	wd := efsReq(t, svc, http.MethodDelete, "/2015-02-01/mount-targets/"+mtID, nil)
	if wd.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", wd.Code)
	}
}

func TestDeleteMountTargetNotFound(t *testing.T) {
	svc := newTestService()
	w := efsReq(t, svc, http.MethodDelete, "/2015-02-01/mount-targets/fsmt-notexist", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// ── AccessPoint ───────────────────────────────────────────────────────────────

func TestCreateAccessPoint(t *testing.T) {
	svc := newTestService()
	fsID := createFS(t, svc)

	w := efsReq(t, svc, http.MethodPost, "/2015-02-01/access-points", map[string]any{
		"FileSystemId": fsID,
		"PosixUser":    map[string]any{"Uid": 1000, "Gid": 1000},
		"RootDirectory": map[string]any{
			"Path": "/lambda",
			"CreationInfo": map[string]any{
				"OwnerUid": 1000, "OwnerGid": 1000, "Permissions": "755",
			},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	b := parseBody(t, w)
	apID := str(b, "AccessPointId")
	if !contains(apID, "fsap-") {
		t.Errorf("AccessPointId %q should start with fsap-", apID)
	}
	apARN := str(b, "AccessPointArn")
	if !contains(apARN, "access-point/"+apID) {
		t.Errorf("AccessPointArn %q should contain access-point/%s", apARN, apID)
	}
	if got := str(b, "FileSystemId"); got != fsID {
		t.Errorf("FileSystemId = %q, want %q", got, fsID)
	}
}

func TestCreateAccessPointIdempotent(t *testing.T) {
	svc := newTestService()
	fsID := createFS(t, svc)
	body := map[string]any{"FileSystemId": fsID, "ClientToken": "ct-abc"}
	w1 := efsReq(t, svc, http.MethodPost, "/2015-02-01/access-points", body)
	w2 := efsReq(t, svc, http.MethodPost, "/2015-02-01/access-points", body)
	id1 := str(parseBody(t, w1), "AccessPointId")
	id2 := str(parseBody(t, w2), "AccessPointId")
	if id1 != id2 {
		t.Errorf("idempotent create returned different IDs: %s vs %s", id1, id2)
	}
}

func TestCreateAccessPointFSNotFound(t *testing.T) {
	svc := newTestService()
	w := efsReq(t, svc, http.MethodPost, "/2015-02-01/access-points", map[string]any{
		"FileSystemId": "fs-notexist",
	})
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestDescribeAccessPoints(t *testing.T) {
	svc := newTestService()
	fsID := createFS(t, svc)
	efsReq(t, svc, http.MethodPost, "/2015-02-01/access-points",
		map[string]any{"FileSystemId": fsID, "ClientToken": "ap1"})
	efsReq(t, svc, http.MethodPost, "/2015-02-01/access-points",
		map[string]any{"FileSystemId": fsID, "ClientToken": "ap2"})

	w := efsReq(t, svc, http.MethodGet, "/2015-02-01/access-points", nil)
	b := parseBody(t, w)
	aps := b["AccessPoints"].([]any)
	if len(aps) != 2 {
		t.Errorf("expected 2 access points, got %d", len(aps))
	}
}

func TestGetAccessPoint(t *testing.T) {
	svc := newTestService()
	fsID := createFS(t, svc)
	w := efsReq(t, svc, http.MethodPost, "/2015-02-01/access-points",
		map[string]any{"FileSystemId": fsID})
	apID := str(parseBody(t, w), "AccessPointId")

	w2 := efsReq(t, svc, http.MethodGet, "/2015-02-01/access-points/"+apID, nil)
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w2.Code)
	}
	if got := str(parseBody(t, w2), "AccessPointId"); got != apID {
		t.Errorf("got %q, want %q", got, apID)
	}
}

func TestDeleteAccessPoint(t *testing.T) {
	svc := newTestService()
	fsID := createFS(t, svc)
	w := efsReq(t, svc, http.MethodPost, "/2015-02-01/access-points",
		map[string]any{"FileSystemId": fsID})
	apID := str(parseBody(t, w), "AccessPointId")

	wd := efsReq(t, svc, http.MethodDelete, "/2015-02-01/access-points/"+apID, nil)
	if wd.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", wd.Code)
	}
}

func TestDeleteAccessPointNotFound(t *testing.T) {
	svc := newTestService()
	w := efsReq(t, svc, http.MethodDelete, "/2015-02-01/access-points/fsap-notexist", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// ── Tagging ───────────────────────────────────────────────────────────────────

func TestTagResource(t *testing.T) {
	svc := newTestService()
	fsID := createFS(t, svc)

	w := efsReq(t, svc, http.MethodPost, "/2015-02-01/resource-tags/"+fsID, map[string]any{
		"Tags": map[string]string{"env": "test", "owner": "forge"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestListTagsForResource(t *testing.T) {
	svc := newTestService()
	fsID := createFS(t, svc)
	efsReq(t, svc, http.MethodPost, "/2015-02-01/resource-tags/"+fsID, map[string]any{
		"Tags": map[string]string{"env": "test"},
	})

	w := efsReq(t, svc, http.MethodGet, "/2015-02-01/resource-tags/"+fsID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	b := parseBody(t, w)
	tags, ok := b["Tags"].(map[string]any)
	if !ok {
		t.Fatalf("Tags not a map: %v", b["Tags"])
	}
	if tags["env"] != "test" {
		t.Errorf("tag env = %q, want test", tags["env"])
	}
}

func TestUntagResource(t *testing.T) {
	svc := newTestService()
	fsID := createFS(t, svc)
	efsReq(t, svc, http.MethodPost, "/2015-02-01/resource-tags/"+fsID, map[string]any{
		"Tags": map[string]string{"env": "test", "owner": "forge"},
	})

	w := efsReq(t, svc, http.MethodDelete, "/2015-02-01/resource-tags/"+fsID+"?tagKeys=env", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	wl := efsReq(t, svc, http.MethodGet, "/2015-02-01/resource-tags/"+fsID, nil)
	tags := parseBody(t, wl)["Tags"].(map[string]any)
	if _, found := tags["env"]; found {
		t.Error("tag 'env' should have been removed")
	}
	if tags["owner"] != "forge" {
		t.Error("tag 'owner' should still exist")
	}
}

func TestTagResourceNotFound(t *testing.T) {
	svc := newTestService()
	w := efsReq(t, svc, http.MethodPost, "/2015-02-01/resource-tags/fs-notexist", map[string]any{
		"Tags": map[string]string{"k": "v"},
	})
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// ── Reset ─────────────────────────────────────────────────────────────────────

func TestReset(t *testing.T) {
	svc := newTestService()
	fsID := createFS(t, svc)
	efsReq(t, svc, http.MethodPost, "/2015-02-01/mount-targets",
		map[string]any{"FileSystemId": fsID, "SubnetId": "subnet-1"})
	efsReq(t, svc, http.MethodPost, "/2015-02-01/access-points",
		map[string]any{"FileSystemId": fsID})

	svc.Reset()

	if svc.FileSystemCount() != 0 {
		t.Error("file systems not cleared by Reset")
	}
	w := efsReq(t, svc, http.MethodGet, "/2015-02-01/mount-targets", nil)
	b := parseBody(t, w)
	if mts := b["MountTargets"].([]any); len(mts) != 0 {
		t.Error("mount targets not cleared by Reset")
	}
	w2 := efsReq(t, svc, http.MethodGet, "/2015-02-01/access-points", nil)
	b2 := parseBody(t, w2)
	if aps := b2["AccessPoints"].([]any); len(aps) != 0 {
		t.Error("access points not cleared by Reset")
	}
}

// ── Mount target security groups ──────────────────────────────────────────────

func TestDescribeMountTargetSecurityGroups(t *testing.T) {
	svc := newTestService()
	wFS := efsReq(t, svc, http.MethodPost, "/2015-02-01/file-systems", map[string]any{"CreationToken": "sg-token-fs"})
	fsID := str(parseBody(t, wFS), "FileSystemId")
	wMT := efsReq(t, svc, http.MethodPost, "/2015-02-01/mount-targets", map[string]any{
		"FileSystemId": fsID, "SubnetId": "subnet-aaa",
	})
	mtID := str(parseBody(t, wMT), "MountTargetId")

	w := efsReq(t, svc, http.MethodGet, fmt.Sprintf("/2015-02-01/mount-targets/%s/security-groups", mtID), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	b := parseBody(t, w)
	if _, ok := b["SecurityGroups"]; !ok {
		t.Error("SecurityGroups missing from response")
	}
}

func TestDescribeMountTargetSecurityGroupsNotFound(t *testing.T) {
	svc := newTestService()
	w := efsReq(t, svc, http.MethodGet, "/2015-02-01/mount-targets/fsmt-notexist/security-groups", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// ── Lifecycle configuration ───────────────────────────────────────────────────

func TestDescribeLifecycleConfiguration(t *testing.T) {
	svc := newTestService()
	w := efsReq(t, svc, http.MethodPost, "/2015-02-01/file-systems", map[string]any{
		"CreationToken": "lc-token-1",
	})
	b := parseBody(t, w)
	id := str(b, "FileSystemId")

	w2 := efsReq(t, svc, http.MethodGet, fmt.Sprintf("/2015-02-01/file-systems/%s/lifecycle-configuration", id), nil)
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w2.Code, w2.Body.String())
	}
	b2 := parseBody(t, w2)
	if _, ok := b2["LifecyclePolicies"]; !ok {
		t.Error("LifecyclePolicies missing from lifecycle configuration response")
	}
}

func TestDescribeLifecycleConfigurationNotFound(t *testing.T) {
	svc := newTestService()
	w := efsReq(t, svc, http.MethodGet, "/2015-02-01/file-systems/fs-notexist/lifecycle-configuration", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func contains(s, sub string) bool {
	return len(s) > 0 && len(sub) > 0 && (s == sub || len(s) >= len(sub) && (s[:len(sub)] == sub || contains(s[1:], sub)))
}
