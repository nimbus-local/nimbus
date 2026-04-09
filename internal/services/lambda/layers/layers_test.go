package layers_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nimbus-local/nimbus/internal/services/lambda/layers"
)

// ---- helpers ----------------------------------------------------------------

func newSvc() *layers.Service {
	return layers.New("us-east-1", "000000000000")
}

func doPublish(t *testing.T, svc *layers.Service, layerName string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return doJSON(t, http.MethodPost, "/2015-03-31/layers/"+layerName+"/versions", body,
		func(w http.ResponseWriter, r *http.Request) { svc.Publish(w, r, layerName) })
}

func doListLayers(t *testing.T, svc *layers.Service, query string) *httptest.ResponseRecorder {
	t.Helper()
	path := "/2015-03-31/layers"
	if query != "" {
		path += "?" + query
	}
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	svc.ListLayers(w, req)
	return w
}

func doGetVersion(t *testing.T, svc *layers.Service, layerName string, version int64) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/2015-03-31/layers/%s/versions/%d", layerName, version), nil)
	w := httptest.NewRecorder()
	svc.GetVersion(w, req, layerName, version)
	return w
}

func doListVersions(t *testing.T, svc *layers.Service, layerName, query string) *httptest.ResponseRecorder {
	t.Helper()
	path := fmt.Sprintf("/2015-03-31/layers/%s/versions", layerName)
	if query != "" {
		path += "?" + query
	}
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	svc.ListVersions(w, req, layerName)
	return w
}

func doDeleteVersion(t *testing.T, svc *layers.Service, layerName string, version int64) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete,
		fmt.Sprintf("/2015-03-31/layers/%s/versions/%d", layerName, version), nil)
	w := httptest.NewRecorder()
	svc.DeleteVersion(w, req, layerName, version)
	return w
}

func doJSON(t *testing.T, method, path string, body any, handler http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	var buf *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		buf = bytes.NewReader(b)
	} else {
		buf = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, buf)
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}

func decodeLayerVersion(t *testing.T, w *httptest.ResponseRecorder) layers.LayerVersion {
	t.Helper()
	var lv layers.LayerVersion
	if err := json.NewDecoder(w.Body).Decode(&lv); err != nil {
		t.Fatalf("decode LayerVersion: %v\nbody: %s", err, w.Body.String())
	}
	return lv
}

func decodeError(t *testing.T, w *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	var e map[string]string
	if err := json.NewDecoder(w.Body).Decode(&e); err != nil {
		t.Fatalf("decode error body: %v\nbody: %s", err, w.Body.String())
	}
	return e
}

// ---- Publish ----------------------------------------------------------------

func TestPublish_HappyPath(t *testing.T) {
	svc := newSvc()
	w := doPublish(t, svc, "my-layer", map[string]any{
		"Content":            map[string]any{},
		"CompatibleRuntimes": []string{"python3.11"},
		"Description":        "first",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body)
	}
	lv := decodeLayerVersion(t, w)
	if lv.Version != 1 {
		t.Errorf("want Version=1, got %d", lv.Version)
	}
	wantLayerArn := "arn:aws:lambda:us-east-1:000000000000:layer:my-layer"
	if lv.LayerArn != wantLayerArn {
		t.Errorf("LayerArn: want %s, got %s", wantLayerArn, lv.LayerArn)
	}
	wantVersionArn := "arn:aws:lambda:us-east-1:000000000000:layer:my-layer:1"
	if lv.LayerVersionArn != wantVersionArn {
		t.Errorf("LayerVersionArn: want %s, got %s", wantVersionArn, lv.LayerVersionArn)
	}
}

func TestPublish_IncrementsVersion(t *testing.T) {
	svc := newSvc()
	doPublish(t, svc, "my-layer", map[string]any{"Content": map[string]any{}})
	w := doPublish(t, svc, "my-layer", map[string]any{"Content": map[string]any{}})
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d", w.Code)
	}
	lv := decodeLayerVersion(t, w)
	if lv.Version != 2 {
		t.Errorf("want Version=2, got %d", lv.Version)
	}
}

func TestPublish_MissingContentIsOK(t *testing.T) {
	svc := newSvc()
	// Content has no required fields — an empty body should be accepted.
	w := doPublish(t, svc, "my-layer", map[string]any{})
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body)
	}
}

func TestPublish_CompatibleRuntimesAndArchitectures(t *testing.T) {
	svc := newSvc()
	w := doPublish(t, svc, "my-layer", map[string]any{
		"Content":                 map[string]any{},
		"CompatibleRuntimes":      []string{"nodejs20.x"},
		"CompatibleArchitectures": []string{"arm64"},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body)
	}
	lv := decodeLayerVersion(t, w)
	if len(lv.CompatibleRuntimes) != 1 || lv.CompatibleRuntimes[0] != "nodejs20.x" {
		t.Errorf("CompatibleRuntimes not stored correctly: %v", lv.CompatibleRuntimes)
	}
	if len(lv.CompatibleArchitectures) != 1 || lv.CompatibleArchitectures[0] != "arm64" {
		t.Errorf("CompatibleArchitectures not stored correctly: %v", lv.CompatibleArchitectures)
	}
}

func TestPublish_InvalidArchitecture(t *testing.T) {
	svc := newSvc()
	w := doPublish(t, svc, "my-layer", map[string]any{
		"Content":                 map[string]any{},
		"CompatibleArchitectures": []string{"sparc"},
	})
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400 for invalid architecture, got %d", w.Code)
	}
}

// ---- ListLayers -------------------------------------------------------------

func TestListLayers_Empty(t *testing.T) {
	svc := newSvc()
	w := doListLayers(t, svc, "")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var resp struct {
		Layers []any `json:"Layers"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Layers) != 0 {
		t.Errorf("want empty Layers, got %d", len(resp.Layers))
	}
}

func TestListLayers_OneLayer(t *testing.T) {
	svc := newSvc()
	doPublish(t, svc, "layer-a", map[string]any{"Content": map[string]any{}, "Description": "v1"})
	doPublish(t, svc, "layer-a", map[string]any{"Content": map[string]any{}, "Description": "v2"})

	w := doListLayers(t, svc, "")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var resp struct {
		Layers []struct {
			LayerName             string            `json:"LayerName"`
			LatestMatchingVersion layers.LayerVersion `json:"LatestMatchingVersion"`
		} `json:"Layers"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Layers) != 1 {
		t.Fatalf("want 1 layer, got %d", len(resp.Layers))
	}
	if resp.Layers[0].LatestMatchingVersion.Version != 2 {
		t.Errorf("want latest version=2, got %d", resp.Layers[0].LatestMatchingVersion.Version)
	}
}

func TestListLayers_TwoLayersShowsHighestVersionEach(t *testing.T) {
	svc := newSvc()
	doPublish(t, svc, "alpha", map[string]any{"Content": map[string]any{}})
	doPublish(t, svc, "alpha", map[string]any{"Content": map[string]any{}})
	doPublish(t, svc, "beta", map[string]any{"Content": map[string]any{}})

	w := doListLayers(t, svc, "")
	var resp struct {
		Layers []struct {
			LayerName             string            `json:"LayerName"`
			LatestMatchingVersion layers.LayerVersion `json:"LatestMatchingVersion"`
		} `json:"Layers"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Layers) != 2 {
		t.Fatalf("want 2 layers, got %d", len(resp.Layers))
	}
	// Sorted by name: alpha, beta.
	if resp.Layers[0].LayerName != "alpha" || resp.Layers[0].LatestMatchingVersion.Version != 2 {
		t.Errorf("alpha latest should be 2, got %+v", resp.Layers[0])
	}
	if resp.Layers[1].LayerName != "beta" || resp.Layers[1].LatestMatchingVersion.Version != 1 {
		t.Errorf("beta latest should be 1, got %+v", resp.Layers[1])
	}
}

func TestListLayers_CompatibleRuntimeFilter(t *testing.T) {
	svc := newSvc()
	doPublish(t, svc, "py-layer", map[string]any{
		"Content": map[string]any{}, "CompatibleRuntimes": []string{"python3.11"},
	})
	doPublish(t, svc, "node-layer", map[string]any{
		"Content": map[string]any{}, "CompatibleRuntimes": []string{"nodejs20.x"},
	})

	w := doListLayers(t, svc, "CompatibleRuntime=python3.11")
	var resp struct {
		Layers []struct{ LayerName string } `json:"Layers"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Layers) != 1 || resp.Layers[0].LayerName != "py-layer" {
		t.Errorf("expected only py-layer, got %+v", resp.Layers)
	}
}

func TestListLayers_Pagination(t *testing.T) {
	svc := newSvc()
	for _, name := range []string{"aaa", "bbb", "ccc"} {
		doPublish(t, svc, name, map[string]any{"Content": map[string]any{}})
	}

	w := doListLayers(t, svc, "MaxItems=2")
	var resp struct {
		Layers     []struct{ LayerName string } `json:"Layers"`
		NextMarker string                       `json:"NextMarker"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Layers) != 2 {
		t.Fatalf("want 2 layers on first page, got %d", len(resp.Layers))
	}
	if resp.NextMarker == "" {
		t.Fatal("want NextMarker, got empty")
	}

	w2 := doListLayers(t, svc, "MaxItems=2&Marker="+resp.NextMarker)
	var resp2 struct {
		Layers     []struct{ LayerName string } `json:"Layers"`
		NextMarker string                       `json:"NextMarker"`
	}
	json.NewDecoder(w2.Body).Decode(&resp2)
	if len(resp2.Layers) != 1 {
		t.Fatalf("want 1 layer on second page, got %d", len(resp2.Layers))
	}
	if resp2.NextMarker != "" {
		t.Errorf("want no NextMarker on last page, got %s", resp2.NextMarker)
	}
}

// ---- GetLayerVersion --------------------------------------------------------

func TestGetLayerVersion_HappyPath(t *testing.T) {
	svc := newSvc()
	doPublish(t, svc, "my-layer", map[string]any{"Content": map[string]any{}})

	w := doGetVersion(t, svc, "my-layer", 1)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	lv := decodeLayerVersion(t, w)
	if lv.Version != 1 {
		t.Errorf("want Version=1, got %d", lv.Version)
	}
}

func TestGetLayerVersion_NotFound(t *testing.T) {
	svc := newSvc()
	w := doGetVersion(t, svc, "no-such-layer", 1)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
	e := decodeError(t, w)
	if e["__type"] != "ResourceNotFoundException" {
		t.Errorf("want ResourceNotFoundException, got %s", e["__type"])
	}
}

// ---- ListLayerVersions ------------------------------------------------------

func TestListLayerVersions_SortedDescending(t *testing.T) {
	svc := newSvc()
	for i := 0; i < 3; i++ {
		doPublish(t, svc, "my-layer", map[string]any{"Content": map[string]any{}})
	}

	w := doListVersions(t, svc, "my-layer", "")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var resp struct {
		LayerVersions []layers.LayerVersion `json:"LayerVersions"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.LayerVersions) != 3 {
		t.Fatalf("want 3 versions, got %d", len(resp.LayerVersions))
	}
	if resp.LayerVersions[0].Version != 3 || resp.LayerVersions[2].Version != 1 {
		t.Errorf("versions not sorted descending: %v", resp.LayerVersions)
	}
}

func TestListLayerVersions_CompatibleRuntimeFilter(t *testing.T) {
	svc := newSvc()
	doPublish(t, svc, "my-layer", map[string]any{
		"Content": map[string]any{}, "CompatibleRuntimes": []string{"python3.11"},
	})
	doPublish(t, svc, "my-layer", map[string]any{
		"Content": map[string]any{}, "CompatibleRuntimes": []string{"nodejs20.x"},
	})

	w := doListVersions(t, svc, "my-layer", "CompatibleRuntime=python3.11")
	var resp struct {
		LayerVersions []layers.LayerVersion `json:"LayerVersions"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.LayerVersions) != 1 || resp.LayerVersions[0].Version != 1 {
		t.Errorf("expected only version 1 (python), got %+v", resp.LayerVersions)
	}
}

func TestListLayerVersions_EmptyForUnknownLayer(t *testing.T) {
	svc := newSvc()
	w := doListVersions(t, svc, "unknown-layer", "")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var resp struct {
		LayerVersions []layers.LayerVersion `json:"LayerVersions"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.LayerVersions) != 0 {
		t.Errorf("want empty list, got %d items", len(resp.LayerVersions))
	}
}

// ---- DeleteLayerVersion -----------------------------------------------------

func TestDeleteLayerVersion_HappyPath(t *testing.T) {
	svc := newSvc()
	doPublish(t, svc, "my-layer", map[string]any{"Content": map[string]any{}})

	w := doDeleteVersion(t, svc, "my-layer", 1)
	if w.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d: %s", w.Code, w.Body)
	}
}

func TestDeleteLayerVersion_NotFound(t *testing.T) {
	svc := newSvc()
	w := doDeleteVersion(t, svc, "no-such-layer", 1)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
	e := decodeError(t, w)
	if e["__type"] != "ResourceNotFoundException" {
		t.Errorf("want ResourceNotFoundException, got %s", e["__type"])
	}
}

func TestDeleteLayerVersion_DeletedVersionGoneFromGet(t *testing.T) {
	svc := newSvc()
	doPublish(t, svc, "my-layer", map[string]any{"Content": map[string]any{}})
	doPublish(t, svc, "my-layer", map[string]any{"Content": map[string]any{}})

	doDeleteVersion(t, svc, "my-layer", 1)

	// Version 1 should be gone.
	w := doGetVersion(t, svc, "my-layer", 1)
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404 after delete, got %d", w.Code)
	}
	// Version 2 should still exist.
	w2 := doGetVersion(t, svc, "my-layer", 2)
	if w2.Code != http.StatusOK {
		t.Errorf("want 200 for version 2, got %d", w2.Code)
	}
}
