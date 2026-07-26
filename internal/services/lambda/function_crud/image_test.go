package function_crud_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/nimbus-local/nimbus/internal/services/lambda/function_crud"
)

// getFunctionEnvelope is the full GetFunction response. The existing helpers
// only decode Configuration; container-image state lives in Code.
type getFunctionEnvelope struct {
	Configuration function_crud.FunctionConfig       `json:"Configuration"`
	Code          function_crud.FunctionCodeLocation `json:"Code"`
	Tags          map[string]string                  `json:"Tags"`
}

func decodeGetFunction(t *testing.T, svc *function_crud.Service, name string) getFunctionEnvelope {
	t.Helper()
	w := doGet(t, svc, name)
	if w.Code != http.StatusOK {
		t.Fatalf("GetFunction: expected 200, got %d\n%s", w.Code, w.Body.String())
	}
	var env getFunctionEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode GetFunction envelope: %v\n%s", err, w.Body.String())
	}
	return env
}

func imageCreateBody(name, imageURI string) map[string]any {
	return map[string]any{
		"FunctionName": name,
		"Role":         "arn:aws:iam::000000000000:role/test-role",
		"PackageType":  "Image",
		"Code":         map[string]any{"ImageUri": imageURI},
	}
}

func TestCreate_Image_ReportsImageInCodeBlock(t *testing.T) {
	svc := newTestService()
	const uri = "123456789012.dkr.ecr.us-east-1.amazonaws.com/my-image:v3"

	if w := doCreate(t, svc, imageCreateBody("image-func", uri)); w.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d\n%s", w.Code, w.Body.String())
	}

	env := decodeGetFunction(t, svc, "image-func")

	if env.Code.ImageUri != uri {
		t.Errorf("Code.ImageUri: expected %q, got %q", uri, env.Code.ImageUri)
	}
	if env.Code.RepositoryType != "ECR" {
		t.Errorf("Code.RepositoryType: expected ECR, got %q", env.Code.RepositoryType)
	}
	if env.Code.Location != "" {
		t.Errorf("Code.Location: expected empty for an image function, got %q", env.Code.Location)
	}
	if !strings.HasPrefix(env.Code.ResolvedImageUri,
		"123456789012.dkr.ecr.us-east-1.amazonaws.com/my-image@sha256:") {
		t.Errorf("Code.ResolvedImageUri: expected the tag replaced by a digest, got %q",
			env.Code.ResolvedImageUri)
	}
	if env.Configuration.PackageType != "Image" {
		t.Errorf("PackageType: expected Image, got %q", env.Configuration.PackageType)
	}
	if env.Configuration.CodeSha256 == "" {
		t.Error("CodeSha256: expected a digest for an image function, got empty")
	}
}

func TestCreate_Image_MissingImageUri(t *testing.T) {
	svc := newTestService()
	w := doCreate(t, svc, map[string]any{
		"FunctionName": "no-image",
		"Role":         "arn:aws:iam::000000000000:role/test-role",
		"PackageType":  "Image",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing ImageUri: expected 400, got %d\n%s", w.Code, w.Body.String())
	}
	if e := decodeError(t, w); e["__type"] != "InvalidParameterValueException" {
		t.Errorf("__type: expected InvalidParameterValueException, got %q", e["__type"])
	}
}

func TestCreate_Image_ImageConfigRoundTrips(t *testing.T) {
	svc := newTestService()
	body := imageCreateBody("configured", "registry.example.com/app:latest")
	body["ImageConfig"] = map[string]any{
		"Command":          []string{"app.handler"},
		"EntryPoint":       []string{"/usr/bin/python3"},
		"WorkingDirectory": "/var/task",
	}
	if w := doCreate(t, svc, body); w.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d\n%s", w.Code, w.Body.String())
	}

	cfg := decodeGetFunction(t, svc, "configured").Configuration
	if cfg.ImageConfigResponse == nil || cfg.ImageConfigResponse.ImageConfig == nil {
		t.Fatalf("ImageConfigResponse: expected the nested envelope, got %+v", cfg.ImageConfigResponse)
	}
	ic := cfg.ImageConfigResponse.ImageConfig
	if len(ic.Command) != 1 || ic.Command[0] != "app.handler" {
		t.Errorf("Command: expected [app.handler], got %v", ic.Command)
	}
	if len(ic.EntryPoint) != 1 || ic.EntryPoint[0] != "/usr/bin/python3" {
		t.Errorf("EntryPoint: expected [/usr/bin/python3], got %v", ic.EntryPoint)
	}
	if ic.WorkingDirectory != "/var/task" {
		t.Errorf("WorkingDirectory: expected /var/task, got %q", ic.WorkingDirectory)
	}
}

func TestGet_Zip_ReportsS3Repository(t *testing.T) {
	svc := newTestService()
	createFunction(t, svc, "zip-func")

	env := decodeGetFunction(t, svc, "zip-func")
	if env.Code.RepositoryType != "S3" {
		t.Errorf("Code.RepositoryType: expected S3, got %q", env.Code.RepositoryType)
	}
	if env.Code.ImageUri != "" {
		t.Errorf("Code.ImageUri: expected empty for a zip function, got %q", env.Code.ImageUri)
	}
	if env.Code.ResolvedImageUri != "" {
		t.Errorf("Code.ResolvedImageUri: expected empty for a zip function, got %q",
			env.Code.ResolvedImageUri)
	}
}

func TestUpdateCode_Image_RepointsAtNewImage(t *testing.T) {
	svc := newTestService()
	if w := doCreate(t, svc, imageCreateBody("repoint", "registry.example.com/app:v1")); w.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d\n%s", w.Code, w.Body.String())
	}
	before := decodeGetFunction(t, svc, "repoint")

	const updated = "registry.example.com/app:v2"
	if w := doUpdateCode(t, svc, "repoint", map[string]any{"ImageUri": updated}); w.Code != http.StatusOK {
		t.Fatalf("update code: expected 200, got %d\n%s", w.Code, w.Body.String())
	}

	after := decodeGetFunction(t, svc, "repoint")
	if after.Code.ImageUri != updated {
		t.Errorf("Code.ImageUri: expected %q, got %q", updated, after.Code.ImageUri)
	}
	if after.Configuration.CodeSha256 == before.Configuration.CodeSha256 {
		t.Error("CodeSha256: expected a new digest after repointing at a different image")
	}
	if after.Configuration.CodeSize != 0 {
		t.Errorf("CodeSize: expected 0 for an image function, got %d", after.Configuration.CodeSize)
	}
}

func TestUpdateConfiguration_AppliesImageConfig(t *testing.T) {
	svc := newTestService()
	if w := doCreate(t, svc, imageCreateBody("recfg", "registry.example.com/app:v1")); w.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d\n%s", w.Code, w.Body.String())
	}

	w := doUpdateConfiguration(t, svc, "recfg", map[string]any{
		"ImageConfig": map[string]any{"Command": []string{"other.handler"}},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("update configuration: expected 200, got %d\n%s", w.Code, w.Body.String())
	}

	cfg := decodeGetFunction(t, svc, "recfg").Configuration
	if cfg.ImageConfigResponse == nil || cfg.ImageConfigResponse.ImageConfig == nil {
		t.Fatalf("ImageConfigResponse: expected the nested envelope, got %+v", cfg.ImageConfigResponse)
	}
	if got := cfg.ImageConfigResponse.ImageConfig.Command; len(got) != 1 || got[0] != "other.handler" {
		t.Errorf("Command: expected [other.handler], got %v", got)
	}
}

// A colon in a registry host is a port, not a tag, and must survive digest
// resolution intact — Nimbus's own registry is reached as localhost:4566.
func TestCreate_Image_RegistryPortIsNotMistakenForATag(t *testing.T) {
	tests := []struct {
		name     string
		imageURI string
		wantRepo string
	}{
		{"port and tag", "localhost:4566/my-repo:dev", "localhost:4566/my-repo"},
		{"port, no tag", "localhost:4566/my-repo", "localhost:4566/my-repo"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := newTestService()
			if w := doCreate(t, svc, imageCreateBody("fn", tc.imageURI)); w.Code != http.StatusCreated {
				t.Fatalf("create: expected 201, got %d\n%s", w.Code, w.Body.String())
			}
			got := decodeGetFunction(t, svc, "fn").Code.ResolvedImageUri
			if !strings.HasPrefix(got, tc.wantRepo+"@sha256:") {
				t.Errorf("ResolvedImageUri: expected prefix %q@sha256:, got %q", tc.wantRepo, got)
			}
		})
	}
}

// An already-pinned reference has no tag to resolve and is reported unchanged.
func TestCreate_Image_DigestPinnedReferenceIsPreserved(t *testing.T) {
	svc := newTestService()
	pinned := "registry.example.com/app@sha256:" + strings.Repeat("a", 64)

	if w := doCreate(t, svc, imageCreateBody("pinned", pinned)); w.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d\n%s", w.Code, w.Body.String())
	}

	env := decodeGetFunction(t, svc, "pinned")
	if env.Code.ImageUri != pinned {
		t.Errorf("Code.ImageUri: expected %q, got %q", pinned, env.Code.ImageUri)
	}
	if env.Code.ResolvedImageUri != pinned {
		t.Errorf("Code.ResolvedImageUri: expected the reference unchanged, got %q",
			env.Code.ResolvedImageUri)
	}
}
